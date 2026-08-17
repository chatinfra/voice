package opencode

import (
	"strings"
	"testing"
)

func TestResponseStateWaitsThroughToolCallsFinish(t *testing.T) {
	response := observeResponse(t, "ses-1", []string{
		`{"type":"message.updated","properties":{"sessionID":"ses-1","info":{"id":"msg-tool","sessionID":"ses-1","role":"assistant","finish":"tool-calls","time":{"created":1,"completed":2}}}}`,
		`{"type":"message.part.updated","properties":{"sessionID":"ses-1","part":{"id":"text-1","sessionID":"ses-1","messageID":"msg-final","type":"text"}}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"ses-1","messageID":"msg-final","partID":"text-1","field":"text","delta":"Final "}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"ses-1","messageID":"msg-final","partID":"text-1","field":"text","delta":"answer"}}`,
		`{"type":"message.updated","properties":{"sessionID":"ses-1","info":{"id":"msg-final","sessionID":"ses-1","role":"assistant","finish":"stop","time":{"created":3,"completed":4}}}}`,
	})

	if response.MessageID != "msg-final" || response.Text != "Final answer" {
		t.Fatalf("response=%+v", response)
	}
}

func TestResponseStateWaitsThroughToolCallsUnderscoreFinish(t *testing.T) {
	response := observeResponse(t, "ses-1", []string{
		`{"type":"message.updated","properties":{"sessionID":"ses-1","info":{"id":"msg-tool","sessionID":"ses-1","role":"assistant","finish":"tool_calls","time":{"created":1,"completed":2}}}}`,
		`{"type":"message.updated","properties":{"sessionID":"ses-1","info":{"id":"msg-final","sessionID":"ses-1","role":"assistant","finish":"stop","content":[{"type":"text","text":"done"}],"time":{"created":3,"completed":4}}}}`,
	})

	if response.MessageID != "msg-final" || response.Text != "done" {
		t.Fatalf("response=%+v", response)
	}
}

func TestResponseStateExcludesReasoningParts(t *testing.T) {
	response := observeResponse(t, "ses-1", []string{
		`{"type":"message.part.updated","properties":{"sessionID":"ses-1","part":{"id":"reason-1","sessionID":"ses-1","messageID":"msg-1","type":"reasoning","text":"internal plan "}}}`,
		`{"type":"message.part.updated","properties":{"sessionID":"ses-1","part":{"id":"text-1","sessionID":"ses-1","messageID":"msg-1","type":"text"}}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"ses-1","messageID":"msg-1","partID":"reason-1","field":"text","delta":"hidden chain "}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"ses-1","messageID":"msg-1","partID":"text-1","field":"text","delta":"Visible "}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"ses-1","messageID":"msg-1","partID":"reason-1","field":"text","delta":"secret"}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"ses-1","messageID":"msg-1","partID":"text-1","field":"text","delta":"answer"}}`,
		`{"type":"message.updated","properties":{"sessionID":"ses-1","info":{"id":"msg-1","sessionID":"ses-1","role":"assistant","time":{"created":1,"completed":2}}}}`,
	})

	if response.Text != "Visible answer" {
		t.Fatalf("Text=%q, want %q", response.Text, "Visible answer")
	}
	for _, forbidden := range []string{"internal plan", "hidden chain", "secret"} {
		if strings.Contains(response.Text, forbidden) {
			t.Fatalf("Text=%q contains reasoning content %q", response.Text, forbidden)
		}
	}
}

func TestResponseStateTerminalEmptyTextIsError(t *testing.T) {
	state := newResponseState("ses-1")
	_, done, err := state.Observe([]byte(`{"type":"message.updated","properties":{"sessionID":"ses-1","info":{"id":"msg-empty","sessionID":"ses-1","role":"assistant","finish":"stop","time":{"created":1,"completed":2}}}}`))

	if done {
		t.Fatal("empty terminal response unexpectedly completed")
	}
	if err == nil || err.Error() != "opencode assistant response completed without text" {
		t.Fatalf("err=%v, want completed without text", err)
	}
}

func TestDecodeEventSyncEnvelopeMessageUpdated(t *testing.T) {
	response := observeResponse(t, "ses-sync", []string{
		`{"directory":"/repo","project":"global","payload":{"type":"sync","syncEvent":{"type":"message.updated.1","seq":89,"aggregateID":"ses-sync","data":{"sessionID":"ses-sync","info":{"id":"msg-sync","sessionID":"ses-sync","role":"assistant","finish":"stop","content":[{"type":"text","text":"sync complete"}],"time":{"created":1,"completed":2}}}}}}`,
	})

	if response.SessionID != "ses-sync" || response.MessageID != "msg-sync" || response.Text != "sync complete" {
		t.Fatalf("response=%+v", response)
	}
}

func TestDecodeEventSyncEnvelopePartUpdated(t *testing.T) {
	response := observeResponse(t, "ses-sync", []string{
		`{"payload":{"type":"sync","syncEvent":{"type":"message.part.updated.1","data":{"sessionID":"ses-sync","part":{"id":"part-1","sessionID":"ses-sync","messageID":"msg-sync","type":"text","text":"part text"}}}}}`,
		`{"payload":{"type":"sync","syncEvent":{"type":"message.updated.1","data":{"sessionID":"ses-sync","info":{"id":"msg-sync","sessionID":"ses-sync","role":"assistant","finish":"stop","time":{"created":1,"completed":2}}}}}}`,
	})

	if response.MessageID != "msg-sync" || response.Text != "part text" {
		t.Fatalf("response=%+v", response)
	}
}

func TestDecodeEventSyncEnvelopeSessionScoping(t *testing.T) {
	state := newResponseState("ses-active")
	response, done, err := state.Observe([]byte(`{"payload":{"type":"sync","syncEvent":{"type":"message.updated.1","data":{"sessionID":"ses-other","info":{"id":"msg-other","sessionID":"ses-other","role":"assistant","finish":"stop","content":[{"type":"text","text":"wrong session"}],"time":{"created":1,"completed":2}}}}}}`))
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if done || response.Text != "" {
		t.Fatalf("Observe completed for wrong session: done=%v response=%+v", done, response)
	}
}

func TestDecodeEventLegacyEnvelopeStillDecodes(t *testing.T) {
	response := observeResponse(t, "ses-legacy", []string{
		`{"type":"message.updated","properties":{"sessionID":"ses-legacy","info":{"id":"msg-legacy","sessionID":"ses-legacy","role":"assistant","finish":"stop","content":[{"type":"text","text":"legacy complete"}],"time":{"created":1,"completed":2}}}}`,
	})

	if response.SessionID != "ses-legacy" || response.MessageID != "msg-legacy" || response.Text != "legacy complete" {
		t.Fatalf("response=%+v", response)
	}
}

func observeResponse(t *testing.T, sessionID string, events []string) AssistantResponse {
	t.Helper()
	state := newResponseState(sessionID)
	var response AssistantResponse
	for index, event := range events {
		got, done, err := state.Observe([]byte(event))
		if err != nil {
			t.Fatalf("Observe event %d: %v", index, err)
		}
		if done {
			response = got
			if index != len(events)-1 {
				t.Fatalf("Observe event %d completed before final event", index)
			}
		}
	}
	if response.Text == "" {
		t.Fatal("response did not complete")
	}
	return response
}
