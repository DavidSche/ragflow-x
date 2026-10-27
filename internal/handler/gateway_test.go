package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

func TestGatewayStreamErrorFrameFormat(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantMsg  string
		wantCode float64
	}{
		{"httperr", httperr.New(502, 50231, "ragflow stream chat completion failed"), "ragflow stream chat completion failed", 50231},
		{"plain error", errors.New("boom"), "gateway request failed", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			gatewayStreamError(&sb, tc.err)

			line := sb.String()
			if !strings.HasPrefix(line, "data: ") || !strings.HasSuffix(line, "\n\n") {
				t.Fatalf("expected SSE data frame, got %q", line)
			}
			payload := strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n\n")

			// The whole payload must be a single JSON object with an "error" object.
			var frame struct {
				Error *struct {
					Message string  `json:"message"`
					Type    string  `json:"type"`
					Code    float64 `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(payload), &frame); err != nil {
				t.Fatalf("payload is not a JSON object: %v (%s)", err, payload)
			}
			if frame.Error == nil {
				t.Fatalf("missing error field in %s", payload)
			}
			if frame.Error.Message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", frame.Error.Message, tc.wantMsg)
			}
			if frame.Error.Type != "gateway_error" {
				t.Fatalf("type = %q, want gateway_error", frame.Error.Type)
			}
			if frame.Error.Code != tc.wantCode {
				t.Fatalf("code = %v, want %v", frame.Error.Code, tc.wantCode)
			}

			// Legacy format regression guard: the payload must not be a bare
			// JSON string (which OpenAI-compatible clients ignore).
			var asString string
			if err := json.Unmarshal([]byte(payload), &asString); err == nil {
				t.Fatalf("error frame must not be a bare JSON string: %q", asString)
			}
		})
	}
}

// alreadyReported is a stand-in for service.ReportedStreamFailure: the upstream
// error frame is already on the wire, so the gateway must stay silent.
type alreadyReported struct{ message string }

func (e *alreadyReported) Error() string               { return e.message }
func (e *alreadyReported) StreamFailureReported() bool { return true }

// TestGatewayStreamErrorSkipsAlreadyReportedFailure pins doc/227 C3.3: one
// stream carries exactly one terminal error signal. When the provider reported
// the failure in-band the gateway forwards that frame verbatim and must not
// append a second one.
func TestGatewayStreamErrorSkipsAlreadyReportedFailure(t *testing.T) {
	var sb strings.Builder
	gatewayStreamError(&sb, &alreadyReported{message: "retrieval timed out"})
	if sb.String() != "" {
		t.Fatalf("gateway must not duplicate an in-band upstream error frame, wrote %q", sb.String())
	}

	// The wrapper must survive wrapping (the service wraps httperr.Error).
	var sb2 strings.Builder
	gatewayStreamError(&sb2, fmt.Errorf("gateway chat: %w", &alreadyReported{message: "boom"}))
	if sb2.String() != "" {
		t.Fatalf("wrapped reported failure must be detected, wrote %q", sb2.String())
	}

	// A plain provider failure still gets its own terminal frame.
	var sb3 strings.Builder
	gatewayStreamError(&sb3, httperr.New(502, 50231, "ragflow stream chat completion failed"))
	if !strings.Contains(sb3.String(), `"error"`) || !strings.Contains(sb3.String(), "50231") {
		t.Fatalf("plain failure must still emit a gateway error frame, wrote %q", sb3.String())
	}
}
