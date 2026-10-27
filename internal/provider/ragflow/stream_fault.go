package ragflow

import (
	"encoding/json"
	"errors"
	"io"
)

// writeStreamFault reproduces a configured mid-stream failure on w. It returns
// handled=false when the fault is the zero value, so callers keep their normal
// happy path untouched.
//
// The two failure shapes matter for the gateway contract (doc/227 C3.3):
//   - ErrorFrame models RAGFlow reporting the failure in-band on a committed
//     200 text/event-stream response. The HTTP status is already sent, so the
//     only signal a client can see is the frame itself.
//   - TransportError models the connection dropping mid-stream. The provider
//     surfaces it as an error and the gateway appends its own error frame.
func writeStreamFault(w io.Writer, fault StreamFault) (bool, error) {
	if fault.ContentBeforeFault == "" && !fault.ErrorFrame && fault.TransportError == "" {
		return false, nil
	}
	if fault.ContentBeforeFault != "" {
		frame, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{"role": "assistant", "content": fault.ContentBeforeFault},
			}},
		})
		if err != nil {
			return true, err
		}
		if _, err := io.WriteString(w, "data: "+string(frame)+"\n\n"); err != nil {
			return true, err
		}
	}
	if fault.TransportError != "" {
		return true, errors.New(fault.TransportError)
	}
	if fault.ErrorFrame {
		code := fault.ErrorCode
		if code == 0 {
			code = 500
		}
		message := fault.ErrorMessage
		if message == "" {
			message = "upstream generation failed"
		}
		frame, err := json.Marshal(map[string]any{
			"error": map[string]any{"code": code, "message": message},
		})
		if err != nil {
			return true, err
		}
		if _, err := io.WriteString(w, "data: "+string(frame)+"\n\n"); err != nil {
			return true, err
		}
	}
	return true, nil
}

// consumeStreamFault returns the configured fault once and clears it, so a
// test that arms a fault observes exactly one affected call.
func (m *Mock) consumeStreamFault() StreamFault {
	m.mu.Lock()
	defer m.mu.Unlock()
	fault := m.streamFault
	if fault != (StreamFault{}) {
		m.streamFaultCalls++
		m.streamFault = StreamFault{}
	}
	return fault
}
