package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type CallsFile struct {
	Calls map[string]string `json:"calls"`
}

type StatusFile struct {
	TurnEndpointReady bool       `json:"turnEndpointReady"`
	ListenAddress     string     `json:"listenAddress,omitempty"`
	BoundNumberE164   string     `json:"boundNumberE164,omitempty"`
	LastTurnAt        *time.Time `json:"lastTurnAt,omitempty"`
	LastReplyAt       *time.Time `json:"lastReplyAt,omitempty"`
	LastError         string     `json:"lastError,omitempty"`
	ActiveCallCount   int        `json:"activeCallCount"`
	StartedAt         time.Time  `json:"startedAt"`
}

type StateStore struct {
	dir string
}

func NewStateStore(dir string) *StateStore { return &StateStore{dir: dir} }

func (s *StateStore) LoadCalls() (map[string]string, error) {
	path := filepath.Join(s.dir, "calls.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var file CallsFile
	if err := json.Unmarshal(data, &file); err == nil && file.Calls != nil {
		return file.Calls, nil
	}
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	if legacy == nil {
		legacy = map[string]string{}
	}
	return legacy, nil
}

func (s *StateStore) SaveCalls(calls map[string]string) error {
	copyMap := make(map[string]string, len(calls))
	for key, value := range calls {
		copyMap[key] = value
	}
	return s.writeJSON("calls.json", CallsFile{Calls: copyMap}, 0o600)
}

func (s *StateStore) SaveStatus(status StatusFile) error {
	return s.writeJSON("status.json", status, 0o644)
}

func (s *StateStore) writeJSON(name string, value any, perm os.FileMode) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}
