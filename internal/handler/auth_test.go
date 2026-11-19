package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestLogoutAcceptsEmptyBodyAndRevokesCookieToken(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "auth.db")})
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

// TestMeCarriesTheWorkspaceName pins the field the report workspace footer
// prints. `/me` used to answer `tenant_id` only, so the footer showed a 32-char
// hex string where a workspace name belongs (live 2026-11-18). The name is a
// display detail: an unknown tenant must degrade to an empty string, never fail
// the identity call.
func TestMeCarriesTheWorkspaceName(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "auth-me.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("sec", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "最佳实践工作区")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "bob", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	h := New(svc)
	r := gin.New()
	r.GET("/me", func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("tenant_id", tenant.ID)
		c.Next()
	}, h.Me)
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("me: %d (%s)", w.Code, w.Body.String())
	}

	var envelope struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if got := envelope.Data["tenant_id"]; got != tenant.ID {
		t.Fatalf("tenant_id must stay the raw id, got %v", got)
	}
	if got, _ := envelope.Data["tenant_name"].(string); got != "最佳实践工作区" {
		t.Fatalf("tenant_name must be the workspace name, got %q", got)
	}

	// A tenant row that no longer exists is not an error for this field: the
	// footer must degrade, not turn /me into a 500.
	if name := svc.TenantDisplayName(ctx, "no-such-tenant"); name != "" {
		t.Fatalf("unknown tenant must answer an empty name, got %q", name)
	}
	if name := svc.TenantDisplayName(ctx, "   "); name != "" {
		t.Fatalf("blank tenant must answer an empty name, got %q", name)
	}
}
