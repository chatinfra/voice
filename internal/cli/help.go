package cli

const VoicedHelp = `voiced - bridge phone-call turns to an opencode agent

USAGE
  voiced
  voiced --help

ENVIRONMENT
  OPENCODE_BASE_URL          opencode API base URL (or OPENCODE_PORT)
  OPENCODE_DIRECTORY         opencode working directory
  OPENCODE_AGENT_ID          agent identifier (or AGENT_ID)
  OPENCODE_AGENT_NAME        opencode agent name (or AGENT_NAME)
  VOICED_STATE_DIR           directory for calls.json and status.json
  VOICED_TURN_ADDR           loopback turn endpoint address (default 127.0.0.1:0)
  VOICE_NUMBER_E164          bound voice number in E.164 format
  OPENCODE_PROMPT_TIMEOUT    prompt timeout as a Go duration

OUTPUT
  stdout: reserved for help text only.
  stderr: runtime logs use the standard log-line format with the "voiced:" prefix.

EXAMPLES
  OPENCODE_BASE_URL=http://127.0.0.1:4096 OPENCODE_DIRECTORY=$PWD OPENCODE_AGENT_ID=agent-1 OPENCODE_AGENT_NAME=receptionist VOICED_STATE_DIR=.state/voiced voiced
`

func WantsHelp(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "--help", "-h", "help":
		return true
	default:
		return false
	}
}
