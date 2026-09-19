package ragflow

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// RoundTripperMiddleware is a decorator function for http.RoundTripper.
type RoundTripperMiddleware func(next http.RoundTripper) http.RoundTripper

// RoundTripperFunc adapts a function to http.RoundTripper.
type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn RoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

// chainRoundTrippers composes multiple middlewares (last argument = outermost).
func chainRoundTrippers(rt http.RoundTripper, middlewares ...RoundTripperMiddleware) http.RoundTripper {
	for i := len(middlewares) - 1; i >= 0; i-- {
		rt = middlewares[i](rt)
	}
	return rt
}

// ── Logging Middleware ──────────────────────────────────────────

// loggingRT logs every request/response through the provider.
// Sensitive endpoints (matching prefixes) have their body redacted.
type loggingRT struct {
	next              http.RoundTripper
	sensitivePrefixes []string
}

// LoggingMiddleware returns a middleware that logs requests at Info level.
func LoggingMiddleware(sensitivePrefixes ...string) RoundTripperMiddleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &loggingRT{next: next, sensitivePrefixes: sensitivePrefixes}
	}
}

func (rt *loggingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	body := rt.logRequestBody(req)

	logger.Info("ragflow request",
		"method", req.Method,
		"url", req.URL.String(),
		"body", body,
	)

	resp, err := rt.next.RoundTrip(req)
	if err != nil {
		logger.Warn("ragflow request failed",
			"method", req.Method,
			"url", req.URL.String(),
			"error", err.Error(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return nil, err
	}

	logger.Info("ragflow response",
		"method", req.Method,
		"url", req.URL.String(),
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return resp, nil
}

// normalizedPath trims any leading base path (e.g. a reverse-proxy mount) and
// the fixed "/api/v1" segment from a request URL path, so that sensitive
// prefixes configured for the inner RAGFlow route ("/providers/") still match
// requests actually sent to "$baseUrl/api/v1$route".
func normalizedPath(urlPath string) string {
	const apiV1 = "/api/v1"
	if i := strings.Index(urlPath, apiV1); i >= 0 {
		return urlPath[i+len(apiV1):]
	}
	return urlPath
}

// metricPathIDRe matches a single resource-ID segment: a compact (dashless)
// lightweight UUID (RAGFlow's native id format) or a full RFC 4122 UUID.
var metricPathIDRe = regexp.MustCompile(`^[0-9a-f]{32}$|^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// normalizeMetricPath maps a request URL path to a bounded set of label values:
// the fixed "/api/v1" prefix (and any reverse-proxy mount) is trimmed, and
// resource-ID segments are collapsed to "{id}". Grouping by structure keeps the
// Prometheus "path" label cardinality bounded regardless of traffic volume.
func normalizeMetricPath(urlPath string) string {
	segs := strings.Split(normalizedPath(urlPath), "/")
	for i := range segs {
		if segs[i] != "" && metricPathIDRe.MatchString(segs[i]) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

// shouldRedact reports whether the request path falls under a sensitive prefix.
func (rt *loggingRT) shouldRedact(path string) bool {
	normalized := normalizedPath(path)
	for _, prefix := range rt.sensitivePrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

// logRequestBody renders request body for logs, redacting sensitive endpoints.
func (rt *loggingRT) logRequestBody(req *http.Request) string {
	if rt.shouldRedact(req.URL.Path) {
		return "[redacted]"
	}
	if req.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(b))

	ct := req.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") && len(b) > 0 {
		return fmt.Sprintf("[%d bytes %s]", len(b), ct)
	}
	return truncateResponseLog(b)
}

// ── Retry Middleware ──────────────────────────────────────────────

// retryRT retries idempotent requests on transient errors.
type retryRT struct {
	next       http.RoundTripper
	maxRetries int
	backoff    time.Duration
}

// RetryMiddleware returns a middleware that retries idempotent methods (GET/PUT/DELETE/HEAD).
// 429/408/5xx are retried; POST/PATCH are never retried.
// 429 Retry-After header is respected as a backoff cap (max 30s).
func RetryMiddleware(maxRetries int, backoff time.Duration) RoundTripperMiddleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &retryRT{next: next, maxRetries: maxRetries, backoff: backoff}
	}
}

// idempotentMethods defines HTTP methods safe to retry.
var idempotentMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPut:    true,
	http.MethodDelete: true,
	http.MethodHead:   true,
}

func (rt *retryRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if !idempotentMethods[req.Method] {
		return rt.next.RoundTrip(req)
	}

	var (
		lastErr  error
		lastResp *http.Response
	)

	for attempt := 0; attempt <= rt.maxRetries; attempt++ {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, &Error{
				Type:    ErrorTypeCancelled,
				Message: "request context ended during retry",
				Cause:   ctxErr,
			}
		}
		if attempt > 0 {
			backoff := rt.backoff * time.Duration(attempt)

			// On 429, respect Retry-After header as backoff cap.
			if lastResp != nil && lastResp.StatusCode == 429 {
				if ra := time.Duration(retryAfterMilliseconds(lastResp.Header.Get("Retry-After"), time.Now())) * time.Millisecond; ra > 0 {
					if ra > 30*time.Second {
						ra = 30 * time.Second
					}
					backoff = ra
				}
			}

			time.Sleep(backoff)
		}

		// Replay body via GetBody closure.
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, &Error{
					Type:    ErrorTypeTransport,
					Message: "failed to replay request body",
					Cause:   err,
				}
			}
			req.Body = body
		}

		resp, err := rt.next.RoundTrip(req)
		lastErr = err
		lastResp = resp

		if err == nil && !shouldRetry(resp.StatusCode) {
			return resp, nil
		}

		if resp != nil {
			resp.Body.Close()
		}
	}

	// Return structured error (not bare fmt.Errorf).
	if lastErr != nil {
		return nil, &Error{
			Type:    ErrorTypeTransport,
			Message: fmt.Sprintf("all %d retries exhausted", rt.maxRetries),
			Cause:   lastErr,
		}
	}
	return nil, &Error{
		Type:         ErrorTypeTransport,
		HTTPStatus:   lastResp.StatusCode,
		Message:      fmt.Sprintf("all %d retries exhausted (last status %d)", rt.maxRetries, lastResp.StatusCode),
		RetryAfterMs: retryAfterMilliseconds(lastResp.Header.Get("Retry-After"), time.Now()),
	}
}

// shouldRetry reports whether the given status code warrants a retry.
func shouldRetry(statusCode int) bool {
	return statusCode == 429 || statusCode == 408 || statusCode >= 500
}

// ── Metrics Middleware ──────────────────────────────────────────

// metricsRT records RAGFlow provider request metrics via the obs package.
type metricsRT struct {
	next http.RoundTripper
}

// MetricsMiddleware returns a middleware that records request count and duration
// using the ragflow-specific Prometheus vectors in the obs package.
func MetricsMiddleware() RoundTripperMiddleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &metricsRT{next: next}
	}
}

func (rt *metricsRT) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := rt.next.RoundTrip(req)
	duration := time.Since(start).Seconds()

	o := obs.Get()
	status := "0"
	if resp != nil {
		status = strconv.Itoa(resp.StatusCode)
	}
	metricPath := normalizeMetricPath(req.URL.Path)
	o.IncRagFlowRequest(req.Method, metricPath, status)
	o.ObserveRagFlowDuration(req.Method, metricPath, duration)

	return resp, err
}
