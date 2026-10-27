// Package httperr defines a typed error that carries an HTTP status and a
// business error code so handlers can render consistent responses.
package httperr

// Error is an HTTP-aware business error.
type Error struct {
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string { return e.Message }

// New constructs an Error.
func New(status, code int, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// BadRequest returns a 400 business error.
func BadRequest(code int, message string) *Error { return New(400, code, message) }

// Unauthorized returns a 401 business error.
func Unauthorized(message string) *Error { return New(401, 401, message) }

// Forbidden returns a 403 business error.
func Forbidden(message string) *Error { return New(403, 403, message) }

// NotFound returns a 404 business error.
func NotFound(message string) *Error { return New(404, 404, message) }

// QuotaExceeded returns a 429 business error for gateway quota exhaustion.
func QuotaExceeded(message string) *Error { return New(429, 42930, message) }

// Internal returns a 500 business error.
func Internal(message string) *Error { return New(500, 500, message) }
