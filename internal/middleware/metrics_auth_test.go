package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMetricsAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/metrics", MetricsAuth("secret"), func(c *gin.Context) { c.Status(200) })

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 401 {
		t.Fatalf("unauthenticated request: got %d", w.Code)
	}
	w = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	router.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("wrong token: got %d", w.Code)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("valid token: got %d", w.Code)
	}

	disabled := gin.New()
	disabled.GET("/metrics", MetricsAuth(""), func(c *gin.Context) { c.Status(200) })
	w = httptest.NewRecorder()
	disabled.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 404 {
		t.Fatalf("empty token must disable metrics: got %d", w.Code)
	}
}

func TestMetricsAuthAllowCIDR(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/metrics", MetricsAuth("secret", "192.0.2.0/24"), func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "192.0.2.15:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("allowed CIDR request: got %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("peer address outside CIDR: got %d", w.Code)
	}
}
