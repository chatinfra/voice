package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chatinfra/voice/internal/opencode"
)

func TestBridgeCreatesSessionByVoiceReceptionistTitleAndReplies(t *testing.T) {
	stateDir := t.TempDir()
	oc := newFakeOpencode("ses-1")
	bridge := testBridge(stateDir, oc)

	resp, err := bridge.HandleTurn(context.Background(), TurnRequest{CallSid: "CA123", CallerNumber: "+15550001", Transcript: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReplyText != "reply:hello" {
		t.Fatalf("reply=%q", resp.ReplyText)
	}
	if got := oc.createTitles(); len(got) != 1 || got[0] != "Voice Receptionist: Ada (agent-1)" {
		t.Fatalf("create titles=%v", got)
	}
	if got := oc.promptSessions(); len(got) != 1 || got[0] != "ses-1" {
		t.Fatalf("prompt sessions=%v", got)
	}
}

func TestBridgeReusesCallSessionFromCallsFileAcrossRestart(t *testing.T) {
	stateDir := t.TempDir()
	first := newFakeOpencode("ses-1")
	if _, err := testBridge(stateDir, first).HandleTurn(context.Background(), TurnRequest{CallSid: "CA123", Transcript: "hello"}); err != nil {
		t.Fatal(err)
	}
	var calls CallsFile
	readJSON(t, filepath.Join(stateDir, "calls.json"), &calls)
	if calls.Calls["CA123"] != "ses-1" {
		t.Fatalf("calls=%+v", calls)
	}

	second := newFakeOpencode()
	if _, err := testBridge(stateDir, second).HandleTurn(context.Background(), TurnRequest{CallSid: "CA123", Transcript: "again"}); err != nil {
		t.Fatal(err)
	}
	if len(second.createTitles()) != 0 {
		t.Fatalf("unexpected create titles=%v", second.createTitles())
	}
	if got := second.promptSessions(); len(got) != 1 || got[0] != "ses-1" {
		t.Fatalf("prompt sessions=%v", got)
	}
}

func TestBridgeFindsExistingSessionByTitleForNewCall(t *testing.T) {
	stateDir := t.TempDir()
	oc := newFakeOpencode()
	oc.existingByTitle["Voice Receptionist: Ada (agent-1)"] = "ses-existing"
	bridge := testBridge(stateDir, oc)

	if _, err := bridge.HandleTurn(context.Background(), TurnRequest{CallSid: "CA999", Transcript: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(oc.createTitles()) != 0 {
		t.Fatalf("created new session unexpectedly: %v", oc.createTitles())
	}
	if got := oc.promptSessions(); len(got) != 1 || got[0] != "ses-existing" {
		t.Fatalf("prompt sessions=%v", got)
	}
}

func TestBridgeRecreatesRejectedSession(t *testing.T) {
	stateDir := t.TempDir()
	if err := NewStateStore(stateDir).SaveCalls(map[string]string{"CA123": "ses-old"}); err != nil {
		t.Fatal(err)
	}
	oc := newFakeOpencode("ses-new")
	oc.promptFunc = func(ctx context.Context, sessionID, text string) (opencode.AssistantResponse, error) {
		if sessionID == "ses-old" {
			return opencode.AssistantResponse{}, &opencode.StaleSessionError{SessionID: sessionID}
		}
		return opencode.AssistantResponse{SessionID: sessionID, Text: "fresh"}, nil
	}
	resp, err := testBridge(stateDir, oc).HandleTurn(context.Background(), TurnRequest{CallSid: "CA123", Transcript: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReplyText != "fresh" {
		t.Fatalf("reply=%q", resp.ReplyText)
	}
	if got := oc.promptSessions(); len(got) != 2 || got[0] != "ses-old" || got[1] != "ses-new" {
		t.Fatalf("prompt sessions=%v", got)
	}
	var calls CallsFile
	readJSON(t, filepath.Join(stateDir, "calls.json"), &calls)
	if calls.Calls["CA123"] != "ses-new" {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestBridgeReturnsFallbackAndUpdatesStatusOnPromptFailure(t *testing.T) {
	stateDir := t.TempDir()
	oc := newFakeOpencode("ses-1")
	oc.promptFunc = func(context.Context, string, string) (opencode.AssistantResponse, error) {
		return opencode.AssistantResponse{}, errors.New("opencode boom")
	}
	var logs bytes.Buffer
	bridge := NewBridgeWithClient(testConfig(stateDir), log.New(&logs, "", 0), oc)
	resp, err := bridge.HandleTurn(context.Background(), TurnRequest{CallSid: "CA123", Transcript: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReplyText != fallbackReply {
		t.Fatalf("reply=%q", resp.ReplyText)
	}
	if !bytes.Contains(logs.Bytes(), []byte("opencode boom")) {
		t.Fatalf("logs=%s", logs.String())
	}
	var status StatusFile
	readJSON(t, filepath.Join(stateDir, "status.json"), &status)
	if status.LastError != "opencode boom" || status.LastTurnAt == nil || status.ActiveCallCount != 1 {
		t.Fatalf("status=%+v", status)
	}
	if status.LastReplyAt != nil {
		t.Fatalf("last reply should be empty: %+v", status.LastReplyAt)
	}
}

func TestBridgeRunServesTurnEndpointAndWritesHealth(t *testing.T) {
	stateDir := t.TempDir()
	cfg := testConfig(stateDir)
	cfg.ListenAddr = "127.0.0.1:0"
	oc := newFakeOpencode("ses-1")
	bridge := NewBridgeWithClient(cfg, log.New(&bytes.Buffer{}, "", 0), oc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bridge.Run(ctx) }()
	status := waitForStatus(t, filepath.Join(stateDir, "status.json"), func(status StatusFile) bool {
		return status.TurnEndpointReady && status.ListenAddress != ""
	})

	body := bytes.NewBufferString(`{"callSid":"CA123","callerNumber":"+15550001","transcript":"hello"}`)
	resp, err := http.Post("http://"+status.ListenAddress+"/turn", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%s body=%s", resp.Status, data)
	}
	var turn TurnResponse
	if err := json.NewDecoder(resp.Body).Decode(&turn); err != nil {
		t.Fatal(err)
	}
	if turn.ReplyText != "reply:hello" {
		t.Fatalf("turn=%+v", turn)
	}
	running := waitForStatus(t, filepath.Join(stateDir, "status.json"), func(status StatusFile) bool {
		return status.TurnEndpointReady && status.LastTurnAt != nil && status.LastReplyAt != nil && status.ActiveCallCount == 1
	})
	if running.BoundNumberE164 != "+15551234567" {
		t.Fatalf("bound number=%q", running.BoundNumberE164)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var stopped StatusFile
	readJSON(t, filepath.Join(stateDir, "status.json"), &stopped)
	if stopped.TurnEndpointReady {
		t.Fatalf("stopped status=%+v", stopped)
	}
}

func testBridge(stateDir string, oc *fakeOpencode) *Bridge {
	return NewBridgeWithClient(testConfig(stateDir), log.New(&bytes.Buffer{}, "", 0), oc)
}

func testConfig(stateDir string) Config {
	return Config{
		OpencodeBaseURL:   "http://127.0.0.1:2721",
		OpencodeDirectory: "/repo",
		AgentID:           "agent-1",
		AgentName:         "Ada",
		BoundNumberE164:   "+15551234567",
		StateDir:          stateDir,
		ListenAddr:        "127.0.0.1:0",
		PromptTimeout:     time.Second,
		ShutdownTimeout:   time.Second,
	}
}

type fakeOpencode struct {
	mu              sync.Mutex
	sessions        []string
	existingByTitle map[string]string
	createdTitles   []string
	prompts         []promptCall
	promptFunc      func(context.Context, string, string) (opencode.AssistantResponse, error)
}

type promptCall struct{ sessionID, text string }

func newFakeOpencode(sessions ...string) *fakeOpencode {
	return &fakeOpencode{sessions: sessions, existingByTitle: map[string]string{}}
}

func (f *fakeOpencode) GetOrCreateSessionByTitle(ctx context.Context, title string) (opencode.Session, error) {
	f.mu.Lock()
	if sessionID := f.existingByTitle[title]; sessionID != "" {
		f.mu.Unlock()
		return opencode.Session{ID: sessionID, Title: title}, nil
	}
	f.mu.Unlock()
	return f.CreateSession(ctx, title)
}

func (f *fakeOpencode) CreateSession(context.Context, string) (opencode.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	title := "Voice Receptionist: Ada (agent-1)"
	f.createdTitles = append(f.createdTitles, title)
	if len(f.sessions) == 0 {
		return opencode.Session{ID: "ses-created", Title: title}, nil
	}
	sessionID := f.sessions[0]
	f.sessions = f.sessions[1:]
	return opencode.Session{ID: sessionID, Title: title}, nil
}

func (f *fakeOpencode) Prompt(ctx context.Context, sessionID, text string) (opencode.AssistantResponse, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, promptCall{sessionID: sessionID, text: text})
	f.mu.Unlock()
	if f.promptFunc != nil {
		return f.promptFunc(ctx, sessionID, text)
	}
	return opencode.AssistantResponse{SessionID: sessionID, Text: "reply:" + text}, nil
}

func (f *fakeOpencode) createTitles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.createdTitles...)
}

func (f *fakeOpencode) promptSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sessions := make([]string, 0, len(f.prompts))
	for _, prompt := range f.prompts {
		sessions = append(sessions, prompt.sessionID)
	}
	return sessions
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func waitForStatus(t *testing.T, path string, done func(StatusFile) bool) StatusFile {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var status StatusFile
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if done(status) {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	return status
}
