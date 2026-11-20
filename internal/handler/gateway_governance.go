package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/service"
)

// Gateway SSE governance frame (doc/301 §5.10 GW-1b).
//
// The frame is inserted *before* the terminal `data: [DONE]`, which means the
// terminal frame has to be held back by one write: a stream cannot end and then
// say something after it. Holding exactly the last terminal frame is the smallest
// intervention that satisfies three invariants at once:
//
//  1. the original `answer` / `reference` events keep their content and order;
//  2. `[DONE]` stays unique and stays last, so an SSE parser that only knows the
//     old event names still terminates correctly (old-client compatibility);
//  3. the frame is written through the *same* writer the handler captures for
//     idempotent replay and quota estimation, so a replay reproduces the bytes a
//     live caller saw.
//
// If the stream never produced a terminal frame, no governance frame is invented:
// a stream that did not terminate normally must not be made to look terminated.

// governanceEventName is the SSE event name of the governance frame. It is
// namespaced (`ragflowx.`) so it cannot collide with an upstream event.
const governanceEventName = "ragflowx.governance"

// governanceStreamWriter intercepts the terminal frame and injects the governance
// frame before it. Every other byte passes through untouched and unbuffered, so
// streaming latency is unchanged.
type governanceStreamWriter struct {
	dst     io.Writer
	traceID string
	pending []byte // bytes of an incomplete line
	held    []byte // the terminal frame, waiting for Close
	written bool
}

func newGovernanceStreamWriter(dst io.Writer, traceID string) *governanceStreamWriter {
	return &governanceStreamWriter{dst: dst, traceID: traceID}
}

func (w *governanceStreamWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		line := w.pending[:index+1]
		w.pending = w.pending[index+1:]
		if isSSETerminalFrame(line) {
			// Keep the frame; it is written after the governance frame.
			w.held = append(w.held, line...)
			continue
		}
		if _, err := w.dst.Write(line); err != nil {
			return 0, err
		}
	}
	// Report the full input as consumed: the caller (the service stream proxy) uses
	// the return value of io.Writer, and reporting a short write would look like a
	// broken pipe mid-stream while we are deliberately buffering.
	return len(p), nil
}

// Close emits the governance frame (when a terminal frame was seen and the caller
// asked for it) followed by the held terminal frame.
//
// `inject` is false on the failure path: a stream that is about to report a
// gateway error must not also claim governance success. The held terminal frame is
// still released, because a client waiting for `[DONE]` would otherwise hang.
func (w *governanceStreamWriter) Close(inject bool) error {
	if w.written {
		return nil
	}
	w.written = true
	if inject && len(w.held) > 0 {
		if _, err := w.dst.Write([]byte(w.frame())); err != nil {
			return err
		}
	}
	if len(w.held) > 0 {
		if _, err := w.dst.Write(w.held); err != nil {
			return err
		}
	}
	if len(w.pending) > 0 {
		if _, err := w.dst.Write(w.pending); err != nil {
			return err
		}
	}
	w.held, w.pending = nil, nil
	return nil
}

// frame renders `event: ragflowx.governance` + one JSON data line.
//
// Only the fields that are true are included: `trace_id` is always known here, and
// answerability is not — the per-turn claim needs the finished answer and the
// reference list, which the streaming path does not hand to the handler. Omitting
// it is the contract's own rule ("没有的字段省略，不编造"); a client that wants it
// uses the non-streaming route.
func (w *governanceStreamWriter) frame() string {
	payload := map[string]any{"schema": service.GovernanceSchema}
	if w.traceID != "" {
		payload["trace_id"] = w.traceID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// Unreachable for a two-key map of strings, but a mangled JSON line would be
		// worse than no frame: fall back to the schema-only payload.
		body = []byte(`{"schema":"` + service.GovernanceSchema + `"}`)
	}
	var out strings.Builder
	out.WriteString("event: " + governanceEventName + "\n")
	out.WriteString("data: " + string(body) + "\n\n")
	return out.String()
}

// isSSETerminalFrame recognises the terminal data line. It is deliberately narrow
// — `[DONE]` inside a JSON answer is not a terminal frame — so the check is on the
// trimmed line equalling the `data:` + `[DONE]` form rather than on containment.
func isSSETerminalFrame(line []byte) bool {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return false
	}
	if !strings.HasPrefix(trimmed, "data:") {
		return false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	return payload == "[DONE]"
}
