package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
)

func TestAuthAcceptsHttpOnlyAccessCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	verifier := jwt.NewManager("secret", 1)
	token, err := verifier.Issue("user-1", "tenant-1", "alice", "tenant_admin")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/me", Auth(verifier, nil), func(c *gin.Context) {
		c.String(200, c.GetString(ContextUserID))
	})

	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(&http.Cookie{Name: "rgx_access", Value: token})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "user-1" {
		t.Fatalf("cookie auth failed: %d %s", w.Code, w.Body.String())
	}
}

// ScenarioID: SC-AUTH-001
func TestP0_AUTH_001_AuthRejectsRefreshCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	verifier := jwt.NewManager("secret", 1)
	_, refresh, _, _, err := verifier.IssuePair("user-1", "tenant-1", "alice", "tenant_admin")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/me", Auth(verifier, nil), func(c *gin.Context) { c.Status(200) })
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(&http.Cookie{Name: "rgx_access", Value: refresh})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("refresh cookie must not authorize access: %d", w.Code)
	}
}
