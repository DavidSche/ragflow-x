package ragflow

import (
	"errors"
	"strings"
	"testing"
)

func TestError_IsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want bool
	}{
		{"5xx is retryable", &Error{HTTPStatus: 500, Type: ErrorTypeTransport}, true},
		{"429 is retryable", &Error{HTTPStatus: 429, Type: ErrorTypeTransport}, true},
		{"408 is retryable", &Error{HTTPStatus: 408, Type: ErrorTypeTimeout}, true},
		{"400 is not retryable", &Error{HTTPStatus: 400, Type: ErrorTypeBusiness}, false},
		{"404 is not retryable", &Error{HTTPStatus: 404, Type: ErrorTypeBusiness}, false},
		{"timeout is retryable", &Error{Type: ErrorTypeTimeout}, true},
		{"network error is retryable", &Error{Type: ErrorTypeTransport, HTTPStatus: 0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.IsRetryable(); got != tt.want {
				t.Fatalf("IsRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestError_Is_WithSentinelErrors(t *testing.T) {
	// 404 goes to Business, not Capability
	err := &Error{Type: ErrorTypeBusiness, HTTPStatus: 404}
	if errors.Is(err, ErrCapabilityMissing) {
		t.Fatal("404 should not be ErrCapabilityMissing")
	}
	if errors.Is(err, ErrTransient) {
		t.Fatal("404 should not be ErrTransient")
	}

	// Only explicitly probed Capability matches
	capErr := &Error{Type: ErrorTypeCapability, HTTPStatus: 404}
	if !errors.Is(capErr, ErrCapabilityMissing) {
		t.Fatal("expected ErrCapabilityMissing")
	}

	// Transient matches via IsRetryable
	trErr := &Error{Type: ErrorTypeTransport, HTTPStatus: 500}
	if !errors.Is(trErr, ErrTransient) {
		t.Fatal("500 transport should be ErrTransient")
	}
}

func TestError_ErrorMessageFormat(t *testing.T) {
	// Verify Error() output is compatible with isTransientRAGFlowErr string matching
	err := &Error{HTTPStatus: 500, Message: "database is locked"}
	if !isTransientRAGFlowErr(err) {
		t.Fatalf("isTransientRAGFlowErr should match 500 error: %s", err.Error())
	}

	// Test format preservation
	err2 := &Error{HTTPStatus: 500, Message: "something"}
	if !strings.Contains(err2.Error(), "ragflow returned status 500:") {
		t.Fatalf("unexpected format: %s", err2.Error())
	}

	err3 := &Error{Code: 102, Message: "bad"}
	if !strings.Contains(err3.Error(), "ragflow error 102: bad") {
		t.Fatalf("unexpected format: %s", err3.Error())
	}
}

func TestNewErrorFromResponse_NonEnvelope(t *testing.T) {
	// FastAPI {"detail":"..."} non-envelope error body
	body := []byte(`{"detail":"Internal Server Error: database is locked"}`)
	err := NewErrorFromResponse(500, body)
	if err.HTTPStatus != 500 {
		t.Fatalf("HTTPStatus = %d", err.HTTPStatus)
	}
	// Message should contain original body (not <nil>)
	if !strings.Contains(err.Message, "database is locked") {
		t.Fatalf("Message should contain original body, got: %s", err.Message)
	}
	// isTransientRAGFlowErr should still match
	if !isTransientRAGFlowErr(err) {
		t.Fatalf("isTransientRAGFlowErr should match non-envelope 500: %s", err.Error())
	}
}

func TestNewErrorFromResponse_Envelope(t *testing.T) {
	body := []byte(`{"code":102,"message":"failed","data":null}`)
	err := NewErrorFromResponse(200, body)
	if err.Code != 102 || err.Message != "failed" {
		t.Fatalf("unexpected: code=%d message=%s", err.Code, err.Message)
	}
	if err.Type != ErrorTypeBusiness {
		t.Fatalf("Type = %d, want ErrorTypeBusiness", err.Type)
	}
}

func TestNewErrorFromResponse_EmptyMessage(t *testing.T) {
	// Empty message in envelope should fall back to truncated body
	body := []byte(`{"code":100,"message":"","data":null}`)
	err := NewErrorFromResponse(200, body)
	if !strings.Contains(err.Message, "code") {
		t.Fatalf("empty message should fall back to body, got: %s", err.Message)
	}
}

func TestNewErrorFromResponse_404(t *testing.T) {
	body := []byte(`{"detail":"Not Found"}`)
	err := NewErrorFromResponse(404, body)
	if err.Type != ErrorTypeBusiness {
		t.Fatalf("404 Type = %d, want ErrorTypeBusiness", err.Type)
	}
}

func TestNewErrorFromResponse_401(t *testing.T) {
	body := []byte(`{"detail":"Unauthorized"}`)
	err := NewErrorFromResponse(401, body)
	if err.Type != ErrorTypeTransport {
		t.Fatalf("401 Type = %d, want ErrorTypeTransport", err.Type)
	}
}
