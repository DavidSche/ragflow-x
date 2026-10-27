package service

import "errors"

// errUpstreamStreamError is the internal cause used when the provider reported
// the failure through an in-band frame instead of returning a Go error.
var errUpstreamStreamError = errors.New("upstream stream reported an error")

// ReportedStreamFailure is implemented by stream errors whose terminal signal
// has already been written to the client by the upstream provider (an in-band
// SSE `data: {"error": {...}}` frame on a committed 200 response).
//
// The gateway still has to fail the idempotency record, write the audit entry
// and release the quota reservation, but it must not append a second error
// frame: OpenAI-compatible clients stop at the first error object and a second
// frame makes one failure look like a duplicated request.
type ReportedStreamFailure interface {
	error
	StreamFailureReported() bool
}

type reportedStreamError struct {
	cause error
}

func (e *reportedStreamError) Error() string {
	if e == nil || e.cause == nil {
		return "upstream stream reported an error"
	}
	return e.cause.Error()
}

func (e *reportedStreamError) Unwrap() error { return e.cause }

// StreamFailureReported marks the error as already delivered to the client.
func (e *reportedStreamError) StreamFailureReported() bool { return true }

// reportedStreamFailure wraps cause so the gateway knows the client already saw
// the upstream failure signal.
func reportedStreamFailure(cause error) error {
	if cause == nil {
		return nil
	}
	return &reportedStreamError{cause: cause}
}

// IsReportedStreamFailure reports whether err was already signalled to the
// client by the upstream error frame.
//
// The contract (doc/233, doc/227 C3.3): a client sees exactly one terminal
// signal per stream - either the upstream error frame passed through verbatim
// or a gateway error frame - never both.
func IsReportedStreamFailure(err error) bool {
	var reported ReportedStreamFailure
	return errors.As(err, &reported)
}
