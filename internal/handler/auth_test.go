package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestLogoutAcceptsEmptyBodyAndRevokesCookieToken(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "auth.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("sec", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "alice", Password: "secret123", Role: "tenant_admin",
	}); err != nil {
		t.Fatal(err)
	}
	login, err := svc.Login(ctx, "alice", "secret123", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	h := New(svc)
	r := gin.New()
	r.POST("/auth/logout", h.Logout)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "rgx_refresh", Value: login.Refresh})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := svc.RefreshAccessToken(ctx, login.Refresh); err == nil {
		t.Fatal("expected refresh token to be revoked by empty-body logout")
	}
}
