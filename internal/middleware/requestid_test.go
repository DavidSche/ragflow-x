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
