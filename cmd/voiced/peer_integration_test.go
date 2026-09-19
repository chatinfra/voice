//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Required Linux fixture: absence of Docker or credential support is a failure.
func runInPeerContainer(t *testing.T) bool {
	t.Helper()
	if os.Getenv("VOICED_PEER_CONTAINER") == "1" {
		return false
	}
	if os.Getenv("TMPDIR") == "" {
		t.Fatal("TMPDIR must name the lane-owned test scratch directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	name := fmt.Sprintf("voice-peer-%d-%d", os.Getpid(), time.Now().UnixNano())
	buildDir := t.TempDir()
	// A static test executable carries the real daemon entry point. A scratch image
	// avoids depending on a developer's unrelated mutable systemd fixture image.
	build := exec.CommandContext(ctx, "go", "test", "-c", "-o", filepath.Join(buildDir, "voice-test"), ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build peer fixture: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM scratch\nCOPY voice-test /voice-test\nWORKDIR /run\nENTRYPOINT [\"/voice-test\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "-f", name).Run()
		_ = exec.CommandContext(cleanupCtx, "docker", "image", "rm", name).Run()
	}()
	imageBuild := exec.CommandContext(ctx, "docker", "build", "--network=none", "-t", name, buildDir)
	if out, err := imageBuild.CombinedOutput(); err != nil {
		t.Fatalf("build required container: %v\n%s", err, out)
	}
	cmd := exec.CommandContext(ctx, "docker", "run", "--name", name, "--rm", "--network=none", "-e", "VOICED_PEER_CONTAINER=1", "-e", "TMPDIR=/run", name, "-test.v", "-test.timeout=190s", "-test.run=^"+t.Name()+"$")
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("required peer container failed: %v", err)
	}
	if !bytes.Contains(out, []byte("--- PASS: "+t.Name())) {
		t.Fatal("container did not execute exact test")
	}
	return true
}

func socketTestPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/run", "voice-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "turn.sock")
}

func unixClient(path string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}

func startTestBridge(t *testing.T) (*Bridge, *fakeOpencode) {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.TurnSocket = socketTestPath(t)
	oc := newFakeOpencode("ses-1")
	bridge := NewBridgeWithClient(cfg, nil, oc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bridge.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	waitForStatus(t, filepath.Join(cfg.StateDir, "status.json"), func(s StatusFile) bool { return s.TurnEndpointReady })
	return bridge, oc
}

func sendTurn(path, runtime string) (int, string, error) {
	body, _ := json.Marshal(TurnRequest{RuntimeID: runtime, AgentID: "agent-1", CallSid: "CA0123456789abcdef0123456789abcdef", Transcript: "hello"})
	resp, err := unixClient(path).Post("http://localhost/turn", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out), err
}

func TestUnixTurnAudienceBinding(t *testing.T) {
	if runInPeerContainer(t) {
		return
	}
	b, oc := startTestBridge(t)
	for _, audience := range []string{"", "other-runtime"} {
		status, _, err := sendTurn(b.cfg.TurnSocket, audience)
		if err != nil || status != 400 {
			t.Fatalf("mismatch: %d %v", status, err)
		}
	}
	if len(oc.createTitles()) != 0 || len(oc.promptSessions()) != 0 {
		t.Fatal("audience mismatch reached OpenCode")
	}
	status, reply, err := sendTurn(b.cfg.TurnSocket, "runtime-1")
	if err != nil || status != 200 || !strings.Contains(reply, "reply:hello") {
		t.Fatalf("authorized reply: %d %s %v", status, reply, err)
	}
}

func TestUnixTurnPeerClient(t *testing.T) {
	if os.Getenv("VOICED_PEER_CLIENT") == "" {
		return
	}
	conn, err := net.DialTimeout("unix", os.Getenv("VOICED_TEST_SOCKET"), time.Second)
	if err != nil {
		fmt.Println("filesystem-refused")
		return
	}
	conn.Close()
	fmt.Println("socket-reachable")
	status, reply, err := sendTurn(os.Getenv("VOICED_TEST_SOCKET"), "runtime-1")
	if err == nil {
		t.Fatalf("unauthorized HTTP response %d %s", status, reply)
	}
}

func TestUnixTurnPeerAdmission(t *testing.T) {
	if runInPeerContainer(t) {
		return
	}
	// A root-owned daemon socket is reachable by root; kernel credentials are observed directly.
	path := socketTestPath(t)
	raw, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	accepted, err := raw.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	uid, err := peerUID(accepted)
	if err != nil || uid != uint32(os.Geteuid()) {
		t.Fatalf("kernel uid %d: %v", uid, err)
	}
	// The mandatory multi-process daemon-UID witness exercises rejection below.
	testPeerIsolation(t)
}

func TestUnixTurnPeerIsolationContainer(t *testing.T) {
	if runInPeerContainer(t) {
		return
	}
	testPeerIsolation(t)
}

func testPeerIsolation(t *testing.T) {
	t.Helper()
	// Run the production listener/handler under a non-root UID in a separate process.
	base := socketTestPath(t)
	dir := filepath.Dir(base)
	if err := os.Chown(dir, 1001, 1001); err != nil {
		t.Fatal(err)
	}
	var effects atomic.Int32
	prompted := make(chan struct{}, 8)
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session":
			effects.Add(1)
			if r.Method == "GET" {
				io.WriteString(w, `[]`)
			} else {
				io.WriteString(w, `{"id":"ses-1"}`)
			}
		case "/session/ses-1/message":
			effects.Add(1)
			prompted <- struct{}{}
			io.WriteString(w, `{}`)
		case "/global/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-prompted:
			}
			fmt.Fprint(w, "data: {\"type\":\"message.part.updated\",\"properties\":{\"sessionID\":\"ses-1\",\"part\":{\"id\":\"prt-1\",\"sessionID\":\"ses-1\",\"messageID\":\"msg-1\",\"type\":\"text\",\"text\":\"reply:hello\"}}}\n\n")
			fmt.Fprint(w, "data: {\"type\":\"message.updated\",\"properties\":{\"info\":{\"id\":\"msg-1\",\"sessionID\":\"ses-1\",\"role\":\"assistant\",\"time\":{\"completed\":1}}}}\n\n")
			w.(http.Flusher).Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer recorder.Close()
	exe, _ := os.Executable()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	daemon := exec.CommandContext(ctx, exe, "-test.run=^TestUnixTurnDaemonHelper$")
	daemon.Env = append(os.Environ(), "VOICED_TEST_DAEMON=1", "VOICED_TEST_SOCKET="+base, "VOICED_TEST_STATE="+dir, "OPENCODE_BASE_URL="+recorder.URL)
	daemon.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1001, Gid: 1001}}
	var output bytes.Buffer
	daemon.Stdout = &output
	daemon.Stderr = &output
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = daemon.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(base); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, uid := range []uint32{1002, 1001} {
		cmd := exec.Command(exe, "-test.run=^TestUnixTurnPeerClient$")
		cmd.Env = append(os.Environ(), "VOICED_PEER_CLIENT=1", "VOICED_TEST_SOCKET="+base)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("UID %d unauthorized turn: %s %v", uid, out, err)
		}
		want := "socket-reachable"
		if uid == 1002 {
			want = "filesystem-refused"
		}
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("UID %d: %s", uid, out)
		}
	}
	status, _, err := sendTurn(base, "wrong-runtime")
	if err != nil || status != 400 {
		t.Fatalf("substituted audience: %d %v", status, err)
	}
	if effects.Load() != 0 {
		t.Fatalf("denied turns caused %d OpenCode effects", effects.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "calls.json")); !os.IsNotExist(err) {
		t.Fatal("denied turns created persisted calls")
	}
	status, reply, err := sendTurn(base, "runtime-1")
	if err != nil || status != 200 || !strings.Contains(reply, "reply:hello") {
		t.Fatalf("positive: %d %s %v", status, reply, err)
	}
	t.Logf("root positive recording reply: %s", reply)
}

func TestUnixTurnDaemonHelper(t *testing.T) {
	if os.Getenv("VOICED_TEST_DAEMON") == "" {
		return
	}
	os.Setenv("VOICED_STATE_DIR", os.Getenv("VOICED_TEST_STATE"))
	os.Setenv("VOICED_TURN_SOCKET", os.Getenv("VOICED_TEST_SOCKET"))
	os.Setenv("VOICED_RUNTIME_ID", "runtime-1")
	os.Setenv("OPENCODE_AGENT_ID", "agent-1")
	os.Setenv("OPENCODE_AGENT_NAME", "Ada")
	os.Setenv("OPENCODE_DIRECTORY", "/repo")
	os.Setenv("VOICE_NUMBER_E164", "+15551234567")
	os.Setenv("OPENCODE_PROMPT_TIMEOUT", "3s")
	os.Args = []string{"voiced"}
	main()
}

func TestUnixTurnListenerLifecycle(t *testing.T) {
	if runInPeerContainer(t) {
		return
	}
	path := socketTestPath(t)
	unrelated := filepath.Join(filepath.Dir(path), "unrelated")
	if err := os.WriteFile(unrelated, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if data, err := os.ReadFile(unrelated); err != nil || string(data) != "preserve" {
			t.Error("unrelated file changed")
		}
	}()
	listener, err := listenTurnSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := listenTurnSocket(path); err == nil {
		other.Close()
		t.Fatal("conflicting bind succeeded")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" {
		t.Fatal("replacement removed")
	}
	if other, err := listenTurnSocket(path); err == nil {
		other.Close()
		t.Fatal("unexpected file adopted")
	}
	os.Remove(path)
	listener, err = listenTurnSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("owned socket remains")
	}
	os.Chmod(filepath.Dir(path), 0755)
	if other, err := listenTurnSocket(path); err == nil {
		other.Close()
		t.Fatal("unsafe directory admitted")
	}
	cfg := testConfig(t.TempDir())
	cfg.TurnSocket = path
	client := newFakeOpencode("ses-1")
	bridge := NewBridgeWithClient(cfg, nil, client)
	if err := bridge.Run(context.Background()); err == nil {
		t.Fatal("unsafe bind reported success")
	}
	var failed StatusFile
	readJSON(t, filepath.Join(cfg.StateDir, "status.json"), &failed)
	if failed.TurnEndpointReady {
		t.Fatal("failed bind published readiness")
	}
	if len(client.createTitles()) != 0 || len(client.promptSessions()) != 0 {
		t.Fatal("failed listener caused OpenCode work")
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bridge.Run(ctx) }()
	waitForStatus(t, filepath.Join(cfg.StateDir, "status.json"), func(s StatusFile) bool { return s.TurnEndpointReady })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	readJSON(t, filepath.Join(cfg.StateDir, "status.json"), &failed)
	if failed.TurnEndpointReady {
		t.Fatal("shutdown retained readiness")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("shutdown retained owned endpoint")
	}
}

func TestUnixTurnBodyLimit(t *testing.T) {
	if runInPeerContainer(t) {
		return
	}
	bridge, oc := startTestBridge(t)
	body := `{"runtimeId":"runtime-1","agentId":"agent-1","callSid":"CA0123456789abcdef0123456789abcdef","transcript":"` + strings.Repeat("x", 65536) + `"}`
	response, err := unixClient(bridge.cfg.TurnSocket).Post("http://localhost/turn", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("oversized body accepted: %d", response.StatusCode)
	}
	if len(oc.createTitles()) != 0 || len(oc.promptSessions()) != 0 {
		t.Fatal("oversized body caused OpenCode effects")
	}
}
