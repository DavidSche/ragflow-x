package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCookieCSRFRejectsCrossSiteBrowserRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CookieCSRF([]string{"https://app.example.com"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "rgx_access", Value: "token"})
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestCookieCSRFAcceptsSameOriginAndAllowedOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CookieCSRF([]string{"https://app.example.com"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, origin := range []string{"https://app.example.com", "http://localhost:5173"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "localhost:5173"
		req.AddCookie(&http.Cookie{Name: "rgx_access", Value: "token"})
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("origin %s: expected 200, got %d", origin, w.Code)
		}
	}
}

func TestCookieCSRFAcceptsBrowserDeclaredSameOriginWithProxiedHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CookieCSRF(nil))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "localhost:8080"
	req.AddCookie(&http.Cookie{Name: "rgx_access", Value: "token"})
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestCookieCSRFAllowsNonBrowserBearerClients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CookieCSRF(nil))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer access")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
