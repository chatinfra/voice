package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chatinfra/voice/internal/opencode"
)

const fallbackReply = "I'm sorry, I'm having trouble answering right now. Please try again in a moment."

var (
	sessionRetryInitialDelay = time.Second
	sessionRetryMaxElapsed   = 2 * time.Minute
)

type opencodeClient interface {
	GetOrCreateSessionByTitle(context.Context, string) (opencode.Session, error)
	CreateSession(context.Context, string) (opencode.Session, error)
	Prompt(context.Context, string, string) (opencode.AssistantResponse, error)
}

type TurnRequest struct {
	CallSid      string `json:"callSid"`
	CallerNumber string `json:"callerNumber,omitempty"`
	Transcript   string `json:"transcript"`
}

type TurnResponse struct {
	ReplyText string `json:"replyText"`
}

type Bridge struct {
	cfg      Config
	logger   *log.Logger
	opencode opencodeClient
	store    *StateStore

	mu          sync.Mutex
	calls       map[string]string
	callsLoaded bool
	status      StatusFile
}

func NewBridge(cfg Config, logger *log.Logger) (*Bridge, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	opencodeClient, err := opencode.New(opencode.Config{
		BaseURL:       cfg.OpencodeBaseURL,
		Directory:     cfg.OpencodeDirectory,
		Agent:         cfg.AgentName,
		PromptTimeout: cfg.PromptTimeout,
	})
	if err != nil {
		return nil, err
	}
	return NewBridgeWithClient(cfg, logger, opencodeClient), nil
}

func NewBridgeWithClient(cfg Config, logger *log.Logger, opencodeClient opencodeClient) *Bridge {
	if logger == nil {
		logger = log.Default()
	}
	return &Bridge{
		cfg:      cfg,
		logger:   logger,
		opencode: opencodeClient,
		store:    NewStateStore(cfg.StateDir),
		status: StatusFile{
			StartedAt:       time.Now().UTC(),
			BoundNumberE164: cfg.BoundNumberE164,
		},
	}
}

func (b *Bridge) Run(ctx context.Context) error {
	calls, err := b.store.LoadCalls()
	if err != nil {
		return fmt.Errorf("load calls: %w", err)
	}
	listener, err := net.Listen("tcp", b.cfg.ListenAddr)
	if err != nil {
		b.recordError(fmt.Errorf("listen turn endpoint: %w", err))
		return err
	}
	actualAddr := listener.Addr().String()
	b.mu.Lock()
	b.calls = calls
	b.callsLoaded = true
	b.status.TurnEndpointReady = true
	b.status.ListenAddress = actualAddr
	b.status.ActiveCallCount = len(calls)
	b.mu.Unlock()
	b.flushStatus()

	server := &http.Server{Handler: b.routes()}
	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()
	b.logger.Printf("listening addr=%s agent_id=%s agent_name=%q number=%s", actualAddr, b.cfg.AgentID, b.cfg.AgentName, b.cfg.BoundNumberE164)

	select {
	case err, ok := <-serveErr:
		b.setReady(false)
		if ok {
			b.recordError(fmt.Errorf("serve turn endpoint: %w", err))
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), b.cfg.ShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-serveErr
		b.setReady(false)
		b.flushCalls()
		b.flushStatus()
		return nil
	}
}

func (b *Bridge) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/turn", b.handleTurnHTTP)
	mux.HandleFunc("/health", b.handleHealthHTTP)
	return mux
}

func (b *Bridge) handleTurnHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req TurnRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	resp, err := b.HandleTurn(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, resp)
}

func (b *Bridge) handleHealthHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b.mu.Lock()
	status := b.status
	b.mu.Unlock()
	writeJSON(w, status)
}

func (b *Bridge) HandleTurn(ctx context.Context, req TurnRequest) (TurnResponse, error) {
	req.CallSid = strings.TrimSpace(req.CallSid)
	req.Transcript = strings.TrimSpace(req.Transcript)
	if req.CallSid == "" {
		return TurnResponse{}, errors.New("callSid is required")
	}
	if req.Transcript == "" {
		return TurnResponse{}, errors.New("transcript is required")
	}
	now := time.Now().UTC()
	b.mu.Lock()
	b.status.LastTurnAt = &now
	b.mu.Unlock()
	b.flushStatus()

	sessionID, err := b.sessionForWithRetry(ctx, req.CallSid)
	if err != nil {
		b.logOnlyError("create opencode session", err)
		return TurnResponse{ReplyText: fallbackReply}, nil
	}
	response, err := b.opencode.Prompt(ctx, sessionID, req.Transcript)
	if opencode.IsStaleSession(err) {
		b.logger.Printf("recreating stale opencode session call_sid=%s", req.CallSid)
		sessionID, err = b.recreateSession(ctx, req.CallSid)
		if err == nil {
			response, err = b.opencode.Prompt(ctx, sessionID, req.Transcript)
		}
	}
	if err != nil {
		b.logOnlyError("opencode prompt", err)
		return TurnResponse{ReplyText: fallbackReply}, nil
	}
	if strings.TrimSpace(response.Text) == "" {
		b.logOnlyError("opencode prompt", errors.New("assistant response had no text"))
		return TurnResponse{ReplyText: fallbackReply}, nil
	}
	now = time.Now().UTC()
	b.mu.Lock()
	b.status.LastReplyAt = &now
	b.mu.Unlock()
	b.flushStatus()
	return TurnResponse{ReplyText: strings.TrimSpace(response.Text)}, nil
}

func (b *Bridge) sessionFor(ctx context.Context, callSid string) (string, error) {
	if err := b.ensureCallsLoaded(); err != nil {
		return "", err
	}
	b.mu.Lock()
	sessionID := b.calls[callSid]
	b.mu.Unlock()
	if sessionID != "" {
		return sessionID, nil
	}
	return b.getOrCreateSession(ctx, callSid)
}

func (b *Bridge) ensureCallsLoaded() error {
	b.mu.Lock()
	loaded := b.callsLoaded
	b.mu.Unlock()
	if loaded {
		return nil
	}
	calls, err := b.store.LoadCalls()
	if err != nil {
		return fmt.Errorf("load calls: %w", err)
	}
	b.mu.Lock()
	if !b.callsLoaded {
		b.calls = calls
		b.callsLoaded = true
		b.status.ActiveCallCount = len(calls)
	}
	b.mu.Unlock()
	b.flushStatus()
	return nil
}

func (b *Bridge) sessionForWithRetry(ctx context.Context, callSid string) (string, error) {
	startedAt := time.Now()
	delay := sessionRetryInitialDelay
	attempt := 0
	for {
		attempt++
		sessionID, err := b.sessionFor(ctx, callSid)
		if err == nil {
			return sessionID, nil
		}
		if !opencode.IsRetryable(err) || time.Since(startedAt) >= sessionRetryMaxElapsed {
			return "", err
		}
		b.logger.Printf("create session transient failure call_sid=%s attempt=%d retry_in=%s: %v", callSid, attempt, delay, err)
		b.recordError(err)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
		}
	}
}

func (b *Bridge) getOrCreateSession(ctx context.Context, callSid string) (string, error) {
	session, err := b.opencode.GetOrCreateSessionByTitle(ctx, b.cfg.SessionTitle())
	if err != nil {
		return "", err
	}
	b.rememberCall(callSid, session.ID)
	return session.ID, nil
}

func (b *Bridge) recreateSession(ctx context.Context, callSid string) (string, error) {
	session, err := b.opencode.CreateSession(ctx, b.cfg.SessionTitle())
	if err != nil {
		return "", err
	}
	b.rememberCall(callSid, session.ID)
	return session.ID, nil
}

func (b *Bridge) rememberCall(callSid, sessionID string) {
	b.mu.Lock()
	if b.calls == nil {
		b.calls = map[string]string{}
	}
	b.callsLoaded = true
	b.calls[callSid] = sessionID
	b.status.ActiveCallCount = len(b.calls)
	b.mu.Unlock()
	if err := b.flushCalls(); err != nil {
		b.recordError(err)
	}
	b.flushStatus()
}

func (b *Bridge) setReady(ready bool) {
	b.mu.Lock()
	b.status.TurnEndpointReady = ready
	b.mu.Unlock()
	b.flushStatus()
}

func (b *Bridge) recordError(err error) {
	if err == nil {
		return
	}
	b.mu.Lock()
	b.status.LastError = err.Error()
	b.mu.Unlock()
	b.flushStatus()
}

func (b *Bridge) logOnlyError(action string, err error) {
	if err == nil {
		return
	}
	b.logger.Printf("%s failed: %v", action, err)
	b.recordError(err)
}

func (b *Bridge) flushCalls() error {
	b.mu.Lock()
	calls := make(map[string]string, len(b.calls))
	for key, value := range b.calls {
		calls[key] = value
	}
	b.mu.Unlock()
	return b.store.SaveCalls(calls)
}

func (b *Bridge) flushStatus() {
	b.mu.Lock()
	status := b.status
	b.mu.Unlock()
	if err := b.store.SaveStatus(status); err != nil {
		b.logger.Printf("write status failed: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
