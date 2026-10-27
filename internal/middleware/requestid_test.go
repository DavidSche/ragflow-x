package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/pkg/requestid"
)

// ScenarioID: SC-OBS-001
func TestRequestID_SharedWithRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var contextID, ginID string
	router.Use(RequestID("X-Request-Id"))
	router.GET("/", func(c *gin.Context) {
		contextID = requestid.FromContext(c.Request.Context())
		ginID = c.GetString("request_id")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "request-123")
	router.ServeHTTP(httptest.NewRecorder(), req)
	if contextID != "request-123" || ginID != "request-123" {
		t.Fatalf("request ids: context=%q gin=%q", contextID, ginID)
	}
}

// ScenarioID: SC-OBS-002 (doc/224 T5.2)
// Both the configured request-id header and the canonical X-Trace-Id must be
// present and carry the same value, so curl/http tooling can correlate a
// gateway request with its trace without knowing the configured header name.
func TestRequestIDExposesTraceIDAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID("X-Request-Id"))
	router.GET("/", func(c *gin.Context) { c.Writer.WriteHeader(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(rec, req)
	rid := rec.Header().Get("X-Request-Id")
	tid := rec.Header().Get(TraceIDHeader)
	if rid == "" || tid == "" {
		t.Fatalf("standard headers missing: X-Request-Id=%q X-Trace-Id=%q", rid, tid)
	}
	if rid != tid {
		t.Fatalf("trace alias must equal request id: rid=%q tid=%q", rid, tid)
	}
}

// ScenarioID: SC-OBS-003 (doc/224 T5.2)
// When a caller supplies its own request id, the trace alias echoes it rather
// than minting a fresh one, keeping end-to-end correlation stable across
// gateway entrypoints.
func TestRequestIDTraceAliasEchoesCallerID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID("X-Request-Id"))
	router.GET("/", func(c *gin.Context) { c.Writer.WriteHeader(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "caller-rid-42")
	router.ServeHTTP(rec, req)
	if got := rec.Header().Get(TraceIDHeader); got != "caller-rid-42" {
		t.Fatalf("trace alias must echo caller id, got %q", got)
	}
}
