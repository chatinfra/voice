package cli

import (
	"strings"
	"testing"
)

func TestWantsHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "long", args: []string{"--help"}, want: true},
		{name: "short", args: []string{"-h"}, want: true},
		{name: "subcommand", args: []string{"help"}, want: true},
		{name: "none", args: nil},
		{name: "extra", args: []string{"--help", "extra"}},
		{name: "unknown", args: []string{"--version"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WantsHelp(tt.args); got != tt.want {
				t.Fatalf("WantsHelp(%v)=%t want %t", tt.args, got, tt.want)
			}
		})
	}
}

func TestHelpDocumentsEnvironment(t *testing.T) {
	for _, token := range []string{
		"OPENCODE_BASE_URL",
		"OPENCODE_DIRECTORY",
		"OPENCODE_AGENT_ID",
		"OPENCODE_AGENT_NAME",
		"VOICED_STATE_DIR",
		"VOICED_TURN_ADDR",
		"VOICE_NUMBER_E164",
		"OPENCODE_PROMPT_TIMEOUT",
	} {
		if !strings.Contains(VoicedHelp, token) {
			t.Fatalf("help missing %q:\n%s", token, VoicedHelp)
		}
	}
}
