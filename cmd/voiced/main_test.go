package main

import (
	"io"
	"os"
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

func runMainForHelp(t *testing.T, arg string) string {
	t.Helper()
	oldArgs := os.Args
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"voiced", arg}
	os.Stdout = writer
	defer func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
		_ = reader.Close()
	}()

	main()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
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
