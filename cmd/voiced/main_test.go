package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
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
