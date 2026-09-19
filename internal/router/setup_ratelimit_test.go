package router_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/handler"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/router"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// TestSetupAdminRateLimited proves the setup wizard's admin creation endpoint
// is guarded against brute force: after the configured per-minute limit, the
// same client IP is rejected with 429.
func TestSetupAdminRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)

	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "rl.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("sec", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	cfg := config.Config{
		Server: config.Server{Mode: "test"},
		App:    config.App{Name: "ragflow-x", RequestIDHeader: "X-Request-Id"},
		Setup:  config.Setup{RateLimitPerMin: 2},
	}
	engine := router.New(cfg, handler.New(svc), ratelimit.NewMemory())

	body := []byte(`{"username":"root","password":"strong-pass"}`)
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/system/setup/admin", bytes.NewReader(body))
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		if i == 2 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("expected setup /admin to be rate limited, got %d", w.Code)
		}
		if i < 2 && w.Code == http.StatusTooManyRequests {
			t.Fatalf("setup /admin limited too early: %d", w.Code)
		}
	}
}
