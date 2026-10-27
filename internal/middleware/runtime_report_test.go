package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// ScenarioID: SC-OBS-001
func TestRuntimeReportAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	build := func(token string) *gin.Engine {
		router := gin.New()
		router.POST("/report", RuntimeReportAuth(func() string { return token }), func(c *gin.Context) {
			if _, ok := c.Get(ContextServiceReport); !ok {
				t.Fatal("authenticated service report context missing")
			}
			c.Status(http.StatusOK)
		})
		return router
	}

	router := build("secret")
	request := httptest.NewRequest(http.MethodPost, "/report", nil)
	request.Header.Set("Authorization", "Bearer secret")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid report token rejected: %d", recorder.Code)
	}

	request.Header.Set("Authorization", "Bearer wrong")
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid report token accepted: %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	router = build("")
	request = httptest.NewRequest(http.MethodPost, "/report", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing report token did not fail closed: %d", recorder.Code)
	}
}
