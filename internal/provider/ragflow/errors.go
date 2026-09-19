package ragflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Error is the structured error type for the RAGFlow provider layer.
type Error struct {
	// Code is the business error code from the RAGFlow envelope (0 = success).
	Code int
	// Message is the error message from RAGFlow or a truncated response body.
	Message string
	// HTTPStatus is the HTTP response status code (0 for non-HTTP errors).
	HTTPStatus int
	// Type distinguishes the error source.
	Type ErrorType
	// Cause is the underlying error (network, JSON parse, etc.).
	Cause error
	// RetryAfterMs is the suggested retry delay from 429 responses (milliseconds).
	RetryAfterMs int
}

// ErrorType classifies the origin of an Error.
type ErrorType int

const (
	ErrorTypeTransport  ErrorType = iota // Network/connection/auth errors
	ErrorTypeProtocol                    // Protocol parse errors (JSON/SSE)
	ErrorTypeBusiness                    // RAGFlow business errors (code != 0) or resource not found (404)
	ErrorTypeCapability                  // Capability not supported (only for startup probe consumers)
	ErrorTypeTimeout                     // Timeout
	ErrorTypeCancelled                   // Caller canceled the request
)

// Sentinel errors for use with errors.Is.
var (
	ErrCapabilityMissing = errors.New("ragflow: capability not supported")
	ErrTransient         = errors.New("ragflow: transient error")
)

func (e *Error) Error() string {
	// Preserve original message format for compatibility with isTransientRAGFlowErr.
	if e.HTTPStatus > 0 && e.Code != 0 {
		return fmt.Sprintf("ragflow error %d: %v (HTTP %d)", e.Code, e.Message, e.HTTPStatus)
	}
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("ragflow returned status %d: %s", e.HTTPStatus, e.Message)
	}
	if e.Code != 0 {
		return fmt.Sprintf("ragflow error %d: %v", e.Code, e.Message)
	}
	return fmt.Sprintf("ragflow: %s", e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// Is implements errors.Is semantics for sentinel error matching.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrCapabilityMissing:
		return e.Type == ErrorTypeCapability
	case ErrTransient:
		return e.IsRetryable()
	}
	return false
}

// IsRetryable reports whether the error is retryable (for idempotent methods only).
func (e *Error) IsRetryable() bool {
	switch {
	case e.Type == ErrorTypeTimeout:
		return true
	case e.Type == ErrorTypeTransport && e.HTTPStatus == 0:
		// Pure network error (connection refused, DNS, etc.)
		return true
	case e.HTTPStatus == 408:
		// Request Timeout
		return true
	case e.HTTPStatus == 429:
		return true
	case e.HTTPStatus >= 500:
		return true
	default:
		return false
	}
}

// IsCapabilityMissing reports whether the error indicates a missing capability.
// Note: only consumed by startup version probe; ordinary 404s do not use this.
func (e *Error) IsCapabilityMissing() bool {
	return e.Type == ErrorTypeCapability
}

// truncate cuts off overly long body text for error messages.
func truncateError(b []byte) string {
	const maxLen = 500
	if len(b) > maxLen {
		return string(b[:maxLen])
	}
	return string(b)
}

// NewErrorFromResponse builds a structured Error from an HTTP response.
// When envelope parsing fails, it falls back to truncate(body) as Message,
// ensuring:
//  1. isTransientRAGFlowErr string matching (lock/busy/serialization) still works
//  2. FastAPI {"detail":...} non-envelope error bodies are not silently dropped
func NewErrorFromResponse(statusCode int, body []byte) *Error {
	var env struct {
		Code    int         `json:"code"`
		Message interface{} `json:"message"`
	}

	msg := truncateError(body) // default: raw body truncated

	if err := json.Unmarshal(body, &env); err == nil && env.Message != nil {
		if m := strings.TrimSpace(fmt.Sprintf("%v", env.Message)); m != "" {
			// Envelope parsed successfully with non-empty message: use it (more precise).
			msg = m
		}
	}

	err := &Error{
		HTTPStatus: statusCode,
		Code:       env.Code,
		Message:    msg,
	}

	// Classify error type.
	// Note: 404 goes to ErrorTypeBusiness (resource not found), not Capability.
	// ErrorTypeCapability is only set explicitly by the startup version probe.
	switch {
	case statusCode == 401 || statusCode == 403:
		err.Type = ErrorTypeTransport
	case statusCode == 404:
		err.Type = ErrorTypeBusiness
	case statusCode == 408:
		err.Type = ErrorTypeTimeout
	case statusCode == 429:
		err.Type = ErrorTypeTransport
	case statusCode >= 500:
		err.Type = ErrorTypeTransport
	case statusCode >= 400:
		err.Type = ErrorTypeBusiness
	case env.Code != 0:
		err.Type = ErrorTypeBusiness
	default:
		err.Type = ErrorTypeProtocol
	}

	return err
}

// NewErrorFromHTTPResponse augments the structured response error with the
// HTTP-level Retry-After contract when RAGFlow signals throttling.
func NewErrorFromHTTPResponse(resp *http.Response, body []byte) *Error {
	providerErr := NewErrorFromResponse(resp.StatusCode, body)
	if resp.StatusCode == http.StatusTooManyRequests {
		providerErr.RetryAfterMs = retryAfterMilliseconds(resp.Header.Get("Retry-After"), time.Now())
	}
	return providerErr
}

// retryAfterMilliseconds parses a numeric or HTTP-date Retry-After value. The
// result is bounded so a hostile header cannot overflow the int contract.
func retryAfterMilliseconds(value string, now time.Time) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	var delay time.Duration
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		delay = time.Duration(seconds) * time.Second
	} else if retryAt, err := http.ParseTime(value); err == nil {
		if !retryAt.After(now) {
			return 0
		}
		delay = retryAt.Sub(now)
	} else {
		return 0
	}

	const maxDelay = 5 * time.Minute
	if delay > maxDelay {
		delay = maxDelay
	}
	return int(delay / time.Millisecond)
}

// wrapError wraps an underlying error as a structured *Error.
func wrapError(msg string, cause error) *Error {
	return &Error{
		Type:    ErrorTypeTransport,
		Message: msg,
		Cause:   cause,
	}
}
