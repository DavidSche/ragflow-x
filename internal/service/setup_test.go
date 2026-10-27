package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newUninitializedSvc(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func TestCreateFirstAdminSuccess(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)

	status, err := svc.SetupStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Initialized {
		t.Fatal("expected uninitialized status")
	}

	if err := svc.CreateFirstAdmin(ctx, "root", "strong-password"); err != nil {
		t.Fatalf("create first admin failed: %v", err)
	}
	u, err := svc.Store.GetUserByUsername(ctx, "root")
	if err != nil || u == nil {
		t.Fatalf("first admin not created: %v", err)
	}
	if u.Role != "platform_admin" {
		t.Fatalf("expected platform_admin role, got %s", u.Role)
	}
}

func TestCreateFirstAdminRejectsWhenInitialized(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	if err := svc.CreateFirstAdmin(ctx, "root", "strong-password"); err != nil {
		t.Fatal(err)
	}

	err := svc.CreateFirstAdmin(ctx, "root", "another-pass")
	if err == nil {
		t.Fatal("expected error when system already initialized")
	}
	if he, ok := err.(*httperr.Error); !ok || he.Status != 409 {
		t.Fatalf("expected 409 error, got %v", err)
	}
}

func TestCreateFirstAdminValidatesPassword(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	if err := svc.CreateFirstAdmin(ctx, "short", "abc"); err == nil {
		t.Fatal("expected password validation error")
	}
}
