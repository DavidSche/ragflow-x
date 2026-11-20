package handler

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// sseEvents splits a stream into (event, data) pairs the way a minimal client does:
// it only understands `event:` and `data:` lines and treats a blank line as the end
// of a frame. The old-client compatibility test depends on this being intentionally
// naive — a parser that knew about `ragflowx.governance` could not prove anything
// about a client that does not.
type sseEvent struct {
	event string
	data  string
}

func parseSSEEvents(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var events []sseEvent
	var current sseEvent
	var hasData bool
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if current.event != "" || hasData {
				events = append(events, current)
			}
			current, hasData = sseEvent{}, false
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			current.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			current.data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			hasData = true
		}
	}
	if current.event != "" || hasData {
		events = append(events, current)
	}
	return events
}

// TestGovernanceStreamWriterOrdersFrameBeforeDone pins doc/301 §5.10 GW-1b: the
// governance frame goes immediately before `[DONE]`, `[DONE]` stays unique and last,
// and the answer frames are byte-identical to what the upstream emitted.
func TestGovernanceStreamWriterOrdersFrameBeforeDone(t *testing.T) {
	var out bytes.Buffer
	w := newGovernanceStreamWriter(&out, "trace-1")

	// Deliberately split mid-line to prove the writer does not depend on write
	// boundaries: gin flushes per write, and a proxy can chunk anywhere.
	writes := []string{
		"data: {\"answer\":\"he", "llo\"}\n\n",
		"data: {\"reference\":[]}\n\n",
		"data: [DONE]\n",
	}
	for _, chunk := range writes {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := w.Close(true); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw := out.String()
	events := parseSSEEvents(t, raw)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %q", len(events), raw)
	}
	if events[0].data != `{"answer":"hello"}` {
		t.Fatalf("answer frame altered: %q", events[0].data)
	}
	if events[1].data != `{"reference":[]}` {
		t.Fatalf("reference frame altered: %q", events[1].data)
	}
	if events[2].event != governanceEventName {
		t.Fatalf("expected governance frame third, got %+v", events[2])
	}
	if events[3].data != "[DONE]" {
		t.Fatalf("terminal frame must stay last, got %+v", events[3])
	}
	if strings.Count(raw, "[DONE]") != 1 {
		t.Fatalf("terminal frame must be unique, stream: %q", raw)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(events[2].data), &payload); err != nil {
		t.Fatalf("governance frame is not JSON: %q", events[2].data)
	}
	if payload["schema"] != "ragflowx.governance/v1" {
		t.Fatalf("governance schema = %q", payload["schema"])
	}
	// The header value and the frame value come from the same request id; a mismatch
	// would make the frame unattributable to the response that carried it.
	if payload["trace_id"] != "trace-1" {
		t.Fatalf("governance trace_id = %q, want trace-1", payload["trace_id"])
	}
}

// TestGovernanceStreamWriterOldClientStillTerminates is the compatibility half of
// GW-1b: a parser that knows only `data:` frames must still reach `[DONE]` and stop.
func TestGovernanceStreamWriterOldClientStillTerminates(t *testing.T) {
	var out bytes.Buffer
	w := newGovernanceStreamWriter(&out, "trace-2")
	if _, err := w.Write([]byte("data: {\"answer\":\"ok\"}\n\ndata: [DONE]\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(true); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The old client: consume `data:` frames, ignore unknown event names, stop at [DONE].
	var answers []string
	terminated := false
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			terminated = true
			break
		}
		var frame struct {
			Answer string `json:"answer"`
		}
		if json.Unmarshal([]byte(payload), &frame) == nil && frame.Answer != "" {
			answers = append(answers, frame.Answer)
		}
	}
	if !terminated {
		t.Fatalf("old client never saw [DONE]: %q", out.String())
	}
	if len(answers) != 1 || answers[0] != "ok" {
		t.Fatalf("old client answers = %v", answers)
	}
}

// TestGovernanceStreamWriterWithoutTerminalFrameInjectsNothing encodes the rule that
// keeps this from lying: no `[DONE]` observed means no governance frame, and the
// bytes are still forwarded untouched.
func TestGovernanceStreamWriterWithoutTerminalFrameInjectsNothing(t *testing.T) {
	var out bytes.Buffer
	w := newGovernanceStreamWriter(&out, "trace-3")
	if _, err := w.Write([]byte("data: {\"answer\":\"partial\"}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(true); err != nil {
		t.Fatalf("close: %v", err)
	}
	if strings.Contains(out.String(), governanceEventName) {
		t.Fatalf("governance frame injected into a stream that never terminated: %q", out.String())
	}
	if !strings.Contains(out.String(), `"partial"`) {
		t.Fatalf("held bytes lost: %q", out.String())
	}
}

// TestGovernanceStreamWriterFailurePathReleasesTerminalFrame: on the error path the
// governance frame is suppressed but `[DONE]` must still arrive, or a client blocks
// forever waiting for a terminal frame that the handler decided not to send.
func TestGovernanceStreamWriterFailurePathReleasesTerminalFrame(t *testing.T) {
	var out bytes.Buffer
	w := newGovernanceStreamWriter(&out, "trace-4")
	if _, err := w.Write([]byte("data: [DONE]\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(false); err != nil {
		t.Fatalf("close: %v", err)
	}
	if strings.Contains(out.String(), governanceEventName) {
		t.Fatalf("governance frame must not be emitted on the failure path: %q", out.String())
	}
	if !strings.Contains(out.String(), "[DONE]") {
		t.Fatalf("terminal frame must still be released: %q", out.String())
	}
}

// TestSSETerminalFrameIsNarrow guards against a JSON answer that merely contains the
// literal `[DONE]` being swallowed and reordered.
func TestSSETerminalFrameIsNarrow(t *testing.T) {
	cases := map[string]bool{
		"data: [DONE]\n":                       true,
		"data:[DONE]\n":                        true,
		"data: [DONE]  \n":                     true,
		`data: {"answer":"say [DONE]"}` + "\n": false,
		"event: answer\n":                      false,
		"data: [DONE]ing\n":                    false,
	}
	for line, want := range cases {
		if got := isSSETerminalFrame([]byte(line)); got != want {
			t.Fatalf("isSSETerminalFrame(%q) = %v, want %v", line, got, want)
		}
	}
}
