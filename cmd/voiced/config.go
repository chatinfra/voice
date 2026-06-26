package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	OpencodeBaseURL   string
	OpencodeDirectory string
	AgentID           string
	AgentName         string
	BoundNumberE164   string
	StateDir          string
	ListenAddr        string
	PromptTimeout     time.Duration
	ShutdownTimeout   time.Duration
}

func ConfigFromEnv() (Config, error) {
	baseURL := strings.TrimSpace(os.Getenv("OPENCODE_BASE_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OPENCODE_URL"))
	}
	if baseURL == "" {
		port := strings.TrimSpace(os.Getenv("OPENCODE_PORT"))
		if port != "" {
			host := strings.TrimSpace(os.Getenv("OPENCODE_HOST"))
			if host == "" {
				host = "127.0.0.1"
			}
			baseURL = "http://" + host + ":" + port
		}
	}
	listenAddr := firstEnv("VOICED_TURN_ADDR", "VOICE_TURN_ADDR", "TURN_ENDPOINT_ADDR")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	timeout, err := envDuration("OPENCODE_PROMPT_TIMEOUT", 0)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := envDuration("VOICED_SHUTDOWN_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		OpencodeBaseURL:   baseURL,
		OpencodeDirectory: firstEnv("OPENCODE_DIRECTORY", "OPENCODE_DIR"),
		AgentID:           firstEnv("OPENCODE_AGENT_ID", "AGENT_ID"),
		AgentName:         firstEnv("OPENCODE_AGENT_NAME", "AGENT_NAME", "OPENCODE_AGENT"),
		BoundNumberE164:   firstEnv("VOICE_NUMBER_E164", "BOUND_VOICE_NUMBER_E164"),
		StateDir:          firstEnv("VOICED_STATE_DIR", "STATE_DIR"),
		ListenAddr:        listenAddr,
		PromptTimeout:     timeout,
		ShutdownTimeout:   shutdownTimeout,
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	if strings.TrimSpace(c.OpencodeBaseURL) == "" {
		missing = append(missing, "OPENCODE_BASE_URL or OPENCODE_PORT")
	} else if parsed, err := url.Parse(c.OpencodeBaseURL); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid opencode base URL %q", c.OpencodeBaseURL)
	}
	if strings.TrimSpace(c.OpencodeDirectory) == "" {
		missing = append(missing, "OPENCODE_DIRECTORY")
	}
	if strings.TrimSpace(c.AgentID) == "" {
		missing = append(missing, "OPENCODE_AGENT_ID or AGENT_ID")
	}
	if strings.TrimSpace(c.AgentName) == "" {
		missing = append(missing, "OPENCODE_AGENT_NAME or AGENT_NAME")
	}
	if strings.TrimSpace(c.BoundNumberE164) == "" {
		missing = append(missing, "VOICE_NUMBER_E164")
	}
	if strings.TrimSpace(c.StateDir) == "" {
		missing = append(missing, "VOICED_STATE_DIR")
	}
	if strings.TrimSpace(c.ListenAddr) == "" {
		missing = append(missing, "VOICED_TURN_ADDR")
	} else if err := validateLoopbackAddress(c.ListenAddr); err != nil {
		return err
	}
	if len(missing) > 0 {
		return errors.New("missing required environment: " + strings.Join(missing, ", "))
	}
	return nil
}

func (c Config) SessionTitle() string {
	return fmt.Sprintf("Voice Receptionist: %s (%s)", c.AgentName, c.AgentID)
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func validateLoopbackAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid VOICED_TURN_ADDR %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("VOICED_TURN_ADDR must bind a loopback address, got %q", addr)
	}
	return nil
}
