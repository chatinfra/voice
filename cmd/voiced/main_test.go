package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVoicedHelpHasSections(t *testing.T) {
	stdout := runMainForHelp(t, "--help")
	for _, header := range []string{"USAGE", "ENVIRONMENT", "OUTPUT", "EXAMPLES"} {
		if !hasHelpHeader(stdout, header) {
			t.Fatalf("help output missing %s header:\n%s", header, stdout)
		}
	}
}

func TestHelpAliasesProduceIdenticalStdout(t *testing.T) {
	want := runMainForHelp(t, "--help")
	for _, arg := range []string{"help", "-h"} {
		if got := runMainForHelp(t, arg); got != want {
			t.Fatalf("voiced %s help differs\nwant:\n%s\ngot:\n%s", arg, want, got)
		}
	}
}

func TestJSONFlagRejectedWithTextError(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestVoicedMainHelper")
	cmd.Env = append(os.Environ(), "VOICED_MAIN_HELPER=1", "VOICED_MAIN_ARGS=--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		t.Fatal("expected --json to be rejected")
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q; want empty stdout", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--json is not supported") {
		t.Fatalf("stderr = %q; want unsupported --json diagnostic", stderr.String())
	}
}

func TestVoicedMainHelper(t *testing.T) {
	if os.Getenv("VOICED_MAIN_HELPER") != "1" {
		return
	}
	os.Args = append([]string{"voiced"}, strings.Fields(os.Getenv("VOICED_MAIN_ARGS"))...)
	main()
}

func runMainForHelp(t *testing.T, arg string) string {
	t.Helper()
	oldArgs := os.Args
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"voiced", arg}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	defer func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
	}()

	main()
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatal(err)
	}
	if string(errOut) != "" {
		t.Fatalf("stderr = %q; want empty stderr for help", string(errOut))
	}
	return string(out)
}

func hasHelpHeader(text, header string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == header {
			return true
		}
	}
	return false
}

func TestVoicedRejectsInvalidSocketConfiguration(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, socket := range []string{"", "127.0.0.1:1234", "/run/../turn.sock", "/" + strings.Repeat("x", 108)} {
		cmd := exec.Command(exe, "-test.run=^TestVoicedMainHelper$")
		cmd.Env = []string{"VOICED_MAIN_HELPER=1", "OPENCODE_BASE_URL=http://127.0.0.1:1", "OPENCODE_DIRECTORY=/repo", "OPENCODE_AGENT_ID=agent-1", "OPENCODE_AGENT_NAME=Ada", "VOICE_NUMBER_E164=+15551234567", "VOICED_STATE_DIR=" + t.TempDir(), "VOICED_RUNTIME_ID=runtime-1", "VOICED_TURN_ADDR=127.0.0.1:1234", "VOICED_TURN_SOCKET=" + socket}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			t.Fatalf("accepted socket %q", socket)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "VOICED_TURN_SOCKET") {
			t.Fatalf("invalid CLI failure: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}

// WASI executes the real non-Linux startup path without a test-only platform override.
func TestVoicedUnsupportedPlatformFailsClosed(t *testing.T) {
	if os.Getenv("TMPDIR") == "" {
		t.Fatal("TMPDIR must name lane-owned scratch")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "voiced.wasm")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build unsupported target: %v\n%s", err, out)
	}
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	script := `
 const {WASI} = require('node:wasi');
 const fs = require('node:fs');
 const wasi = new WASI({version:'preview1', returnOnExit:true, args:['voiced'], env:{
 OPENCODE_BASE_URL:'http://127.0.0.1:1', OPENCODE_DIRECTORY:'/repo',
 OPENCODE_AGENT_ID:'agent-1', OPENCODE_AGENT_NAME:'Ada', VOICE_NUMBER_E164:'+15551234567',
 VOICED_STATE_DIR:'/state', VOICED_TURN_SOCKET:'/state/turn.sock', VOICED_RUNTIME_ID:'runtime-1'
 },preopens:{'/state':process.argv[2]}});
 const module = new WebAssembly.Module(fs.readFileSync(process.argv[1]));
 const instance = new WebAssembly.Instance(module,{wasi_snapshot_preview1:wasi.wasiImport});
 process.exitCode = wasi.start(instance);
 `
	// Name the missing prerequisite. Without this, a host with no node on PATH fails the
	// assertion below with two empty strings and nothing that points at the cause.
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required to execute the wasip1 build: %v", err)
	}
	cmd := exec.CommandContext(ctx, "node", "--no-warnings", "-e", script, binary, state)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("unsupported platform started successfully")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "configuration error: Unix peer authentication requires Linux") {
		t.Fatalf("unsupported startup: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	files, err := os.ReadDir(state)
	if err != nil || len(files) != 0 {
		t.Fatalf("unsupported platform created runtime state: %v %v", files, err)
	}
}
