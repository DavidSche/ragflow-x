package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestRefreshTokenRotationAndRevocation(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "rt.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("sec", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "alice", Password: "secret123", Role: "tenant_admin"}); err != nil {
		t.Fatal(err)
	}

	login, err := svc.Login(ctx, "alice", "secret123", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || login.Refresh == "" {
		t.Fatalf("expected access+refresh pair from login")
	}

	// Rotation: old refresh is revoked, a new pair is returned.
	rot, err := svc.RefreshAccessToken(ctx, login.Refresh)
	if err != nil {
		t.Fatalf("refresh should succeed: %v", err)
	}
	if rot.Refresh == "" || rot.Refresh == login.Refresh {
		t.Fatalf("expected a rotated refresh token")
	}

	// Reuse of the rotated-away token must be rejected.
	if _, err := svc.RefreshAccessToken(ctx, login.Refresh); err == nil {
		t.Fatal("expected reused/revoked refresh token to be rejected")
	}

	// Logout revokes the current token, so a subsequent refresh fails.
	if err := svc.Logout(ctx, rot.Refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefreshAccessToken(ctx, rot.Refresh); err == nil {
		t.Fatal("expected logout token to be rejected")
	}
}
