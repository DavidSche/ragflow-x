package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// RateLimit enforces a sliding-window limit keyed by the provided function
// (e.g. client IP or tenant). It is a safety valve against runaway traffic;
// the backing limiter is pluggable (memory or Redis).
func RateLimit(limiter ratelimit.Limiter, keyFn func(*gin.Context) string, limit int64, window time.Duration) gin.HandlerFunc {
	return rateLimit(limiter, keyFn, limit, window, true)
}

// RateLimitStrict behaves like RateLimit but rejects when the distributed
// limiter backend is unavailable. Use it for gateway and high-value endpoints
// where unbounded traffic is more damaging than short-term unavailability.
func RateLimitStrict(limiter ratelimit.Limiter, keyFn func(*gin.Context) string, limit int64, window time.Duration) gin.HandlerFunc {
	return rateLimit(limiter, keyFn, limit, window, false)
}

func rateLimit(limiter ratelimit.Limiter, keyFn func(*gin.Context) string, limit int64, window time.Duration, failOpen bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter == nil || limit <= 0 {
			c.Next()
			return
		}
		count, err := limiter.Incr(c.Request.Context(), "http:"+keyFn(c), window)
		if err != nil {
			if !failOpen {
				response.Fail(c, 503, 50300, "rate limiter unavailable")
				c.Abort()
				return
			}
			// Fail open only for availability-oriented paths, while keeping the
			// security-relevant degradation observable.
			key := keyFn(c)
			logger.Warn("rate limiter backend unavailable; failing open", "error", err, "key", key, "path", c.Request.URL.Path)
			obs.Get().IncLimiterFailOpen()
			notify.Emit(c.Request.Context(), notify.Event{
				Title: "限流器不可用，已降级放行", Severity: "warn", Type: "limiter_failopen",
				Resource: "gateway", ResourceID: key,
				Detail: c.ClientIP() + " " + c.Request.Method + " " + c.Request.URL.Path + ": " + err.Error(),
			})
			c.Next()
			return
		}
		if count > limit {
			response.Fail(c, 429, 429, "rate limit exceeded, please retry later")
			obs.Get().IncRateLimitReject()
			notify.Emit(c.Request.Context(), notify.Event{
				Title: "rate limit exceeded", Severity: "warn", Type: "rate_limit",
				Resource: "gateway", ResourceID: keyFn(c),
				Detail: c.ClientIP() + " " + c.Request.Method + " " + c.Request.URL.Path,
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// ClientIPKey returns a rate-limit key scoped to the caller's IP.
func ClientIPKey(c *gin.Context) string { return c.ClientIP() }

// BearerKey returns a rate-limit key scoped to the caller's API key (the
// SHA-256 of the presented bearer token, so raw keys never appear in limiter
// keys or notify events). Requests without a bearer token fall back to the
// client IP. This keeps tenants behind a shared egress IP from squeezing each
// other's gateway traffic.
func BearerKey(c *gin.Context) string {
	raw := c.GetHeader("Authorization")
	if len(raw) > 7 && strings.EqualFold(raw[:7], "Bearer ") {
		sum := sha256.Sum256([]byte(raw[7:]))
		return "key:" + hex.EncodeToString(sum[:])
	}
	return c.ClientIP()
}
