// Package requestid shares request correlation context between HTTP
// middleware and outbound provider calls without creating a middleware
// dependency from the provider layer.
package requestid

import "context"

type contextKey struct{}

// NewContext stores the current request's correlation ID in the standard
// request context in addition to gin.Context so background and outbound calls
// can preserve it.
func NewContext(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, requestID)
}

// FromContext returns the request ID when present.
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value, ok := ctx.Value(contextKey{}).(string); ok {
		return value
	}
	return ""
}
