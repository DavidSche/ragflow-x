package service

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type authFailureStore struct {
	repository.Store
	getUserByUsernameError error
	getTenantError         error
	getUserError           error
	getTenantNil           bool
	createTenantError      error
	createRefreshError     error
	findRefreshError       error
	revokeActiveError      error
	createUserError        error
	setPrimaryRoleError    error
	auditError             error
}

func (s *authFailureStore) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	if s.getUserByUsernameError != nil {
		return nil, s.getUserByUsernameError
	}
	return s.Store.GetUserByUsername(ctx, username)
}

func (s *authFailureStore) GetTenant(ctx context.Context, tenantID string) (*model.Tenant, error) {
	if s.getTenantNil {
		return nil, nil
	}
	if s.getTenantError != nil {
		return nil, s.getTenantError
	}
	return s.Store.GetTenant(ctx, tenantID)
}

func (s *authFailureStore) GetUser(ctx context.Context, userID string) (*model.User, error) {
	if s.getUserError != nil {
		return nil, s.getUserError
	}
	return s.Store.GetUser(ctx, userID)
}

func (s *authFailureStore) CreateTenant(ctx context.Context, tenant *model.Tenant) error {
	if s.createTenantError != nil {
		return s.createTenantError
	}
	return s.Store.CreateTenant(ctx, tenant)
}

func (s *authFailureStore) CreateRefreshToken(ctx context.Context, token *model.RefreshToken) error {
	if s.createRefreshError != nil {
		return s.createRefreshError
	}
	return s.Store.CreateRefreshToken(ctx, token)
}

func (s *authFailureStore) FindRefreshTokenByHash(ctx context.Context, hash string) (*model.RefreshToken, error) {
	if s.findRefreshError != nil {
		return nil, s.findRefreshError
	}
	return s.Store.FindRefreshTokenByHash(ctx, hash)
}

func (s *authFailureStore) RevokeActiveRefreshToken(ctx context.Context, tokenID string, now time.Time) (bool, error) {
	if s.revokeActiveError != nil {
		return false, s.revokeActiveError
	}
	return s.Store.RevokeActiveRefreshToken(ctx, tokenID, now)
}

func (s *authFailureStore) CreateUser(ctx context.Context, user *model.User) error {
	if s.createUserError != nil {
		return s.createUserError
	}
	return s.Store.CreateUser(ctx, user)
}

func (s *authFailureStore) SetUserPrimaryRole(ctx context.Context, userID, roleID string) error {
	if s.setPrimaryRoleError != nil {
		return s.setPrimaryRoleError
	}
	return s.Store.SetUserPrimaryRole(ctx, userID, roleID)
}

func (s *authFailureStore) CreateAudit(ctx context.Context, entry *model.AuditLog) error {
	if s.auditError != nil {
		return s.auditError
	}
	return s.Store.CreateAudit(ctx, entry)
}

// ScenarioID: SC-AUTH-001
func TestP0_AUTH_001_LoginFailureContractPreservesSecurityBoundaries(t *testing.T) {
	ctx := context.Background()
	remoteIP := "192.0.2.10"
	persistenceError := errors.New("auth persistence unavailable")

	t.Run("rate limited login is rejected with HTTP 429", func(t *testing.T) {
		svc := newAuthzSvc(t)
		tenant, err := svc.CreateTenant(ctx, "rate limited")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
			Username: "limited", Password: "secret123", Role: model.RoleTenantAdmin,
		}); err != nil {
			t.Fatal(err)
		}
		for range loginMaxAttempts {
			if _, err := svc.Login(ctx, "limited", "wrong-password", remoteIP); err == nil {
				t.Fatal("invalid credentials must fail")
			}
		}
		_, err = svc.Login(ctx, "limited", "secret123", remoteIP)
		if authErr, ok := err.(*httperr.Error); !ok || authErr.Status != http.StatusTooManyRequests || authErr.Code != 429 {
			t.Fatalf("expected HTTP 429/429, got %v", err)
		}
	})

	t.Run("user lookup failure does not issue tokens", func(t *testing.T) {
		svc := newAuthzSvc(t)
		svc.Store = &authFailureStore{Store: svc.Store, getUserByUsernameError: persistenceError}
		if _, err := svc.Login(ctx, "any-user", "secret123", remoteIP); !errors.Is(err, persistenceError) {
			t.Fatalf("expected user lookup failure, got %v", err)
		}
	})

	t.Run("invalid credentials do not leak user existence", func(t *testing.T) {
		svc := newAuthzSvc(t)
		tenant, err := svc.CreateTenant(ctx, "invalid credentials")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
			Username: "existing", Password: "secret123", Role: model.RoleTenantAdmin,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Login(ctx, "existing", "wrong-password", remoteIP); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("expected stable invalid-credentials error, got %v", err)
		}
	})

	t.Run("tenant lookup failure does not issue tokens", func(t *testing.T) {
		svc := newAuthzSvc(t)
		tenant, err := svc.CreateTenant(ctx, "tenant lookup failure")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
			Username: "tenant-failure", Password: "secret123", Role: model.RoleTenantAdmin,
		}); err != nil {
			t.Fatal(err)
		}
		svc.Store = &authFailureStore{Store: svc.Store, getTenantError: persistenceError}
		if _, err := svc.Login(ctx, "tenant-failure", "secret123", remoteIP); !errors.Is(err, persistenceError) {
			t.Fatalf("expected tenant lookup failure, got %v", err)
		}
	})

	t.Run("refresh persistence failure does not return a token pair", func(t *testing.T) {
		svc := newAuthzSvc(t)
		tenant, err := svc.CreateTenant(ctx, "refresh persistence")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
			Username: "refresh-failure", Password: "secret123", Role: model.RoleTenantAdmin,
		}); err != nil {
			t.Fatal(err)
		}
		svc.Store = &authFailureStore{Store: svc.Store, createRefreshError: persistenceError}
		if _, err := svc.Login(ctx, "refresh-failure", "secret123", remoteIP); !errors.Is(err, persistenceError) {
			t.Fatalf("expected refresh persistence failure, got %v", err)
		}
	})

	t.Run("login audit failure revokes the issued refresh token", func(t *testing.T) {
		svc := newAuthzSvc(t)
		tenant, err := svc.CreateTenant(ctx, "audit rollback")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
			Username: "audited", Password: "secret123", Role: model.RoleTenantAdmin,
		}); err != nil {
			t.Fatal(err)
		}
		svc.Store = &authFailureStore{Store: svc.Store, auditError: persistenceError}
		if _, err := svc.Login(ctx, "audited", "secret123", remoteIP); !errors.Is(err, persistenceError) {
			t.Fatalf("expected audit rollback failure, got %v", err)
		}
	})
}

// ScenarioID: SC-AUTH-001
func TestP0_AUTH_001_RefreshAndLogoutContractPreservesAtomicRotation(t *testing.T) {
	ctx := context.Background()
	persistenceError := errors.New("refresh persistence unavailable")

	t.Run("refresh storage failure rejects rotation", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		svc.Store = &authFailureStore{Store: svc.Store, findRefreshError: persistenceError}
		if _, err := svc.RefreshAccessToken(ctx, refresh); !errors.Is(err, persistenceError) {
			t.Fatalf("expected refresh lookup failure, got %v", err)
		}
	})

	t.Run("refresh user lookup failure rejects rotation", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		svc.Store = &authFailureStore{Store: svc.Store, getUserError: persistenceError}
		if _, err := svc.RefreshAccessToken(ctx, refresh); !errors.Is(err, persistenceError) {
			t.Fatalf("expected refresh user lookup failure, got %v", err)
		}
	})

	t.Run("refresh tenant lookup failure rejects rotation", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		svc.Store = &authFailureStore{Store: svc.Store, getTenantError: persistenceError}
		if _, err := svc.RefreshAccessToken(ctx, refresh); !errors.Is(err, persistenceError) {
			t.Fatalf("expected refresh tenant lookup failure, got %v", err)
		}
	})

	t.Run("disabled user cannot refresh", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		user, err := svc.Store.GetUserByUsername(ctx, "rotation-user")
		if err != nil || user == nil {
			t.Fatalf("rotation user missing: %v err=%v", user, err)
		}
		user.Status = model.UserStatusDisabled
		if err := svc.Store.UpdateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.RefreshAccessToken(ctx, refresh); err == nil {
			t.Fatal("disabled user refresh must fail")
		}
	})

	t.Run("failed atomic refresh claim rejects rotation", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		svc.Store = &authFailureStore{Store: svc.Store, revokeActiveError: persistenceError}
		if _, err := svc.RefreshAccessToken(ctx, refresh); !errors.Is(err, persistenceError) {
			t.Fatalf("expected atomic claim failure, got %v", err)
		}
	})

	t.Run("new refresh persistence failure rejects rotation", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		svc.Store = &authFailureStore{Store: svc.Store, createRefreshError: persistenceError}
		if _, err := svc.RefreshAccessToken(ctx, refresh); !errors.Is(err, persistenceError) {
			t.Fatalf("expected refresh persistence failure, got %v", err)
		}
	})

	t.Run("refresh audit failure revokes the new refresh token", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		svc.Store = &authFailureStore{Store: svc.Store, auditError: persistenceError}
		if _, err := svc.RefreshAccessToken(ctx, refresh); !errors.Is(err, persistenceError) {
			t.Fatalf("expected refresh audit rollback failure, got %v", err)
		}
	})

	t.Run("logout is tolerant to invalid tokens and strict to store failures", func(t *testing.T) {
		svc, refresh := loginForRefreshContract(t)
		if err := svc.Logout(ctx, "not-a-token"); err != nil {
			t.Fatalf("invalid logout token must not fail: %v", err)
		}
		svc.Store = &authFailureStore{Store: svc.Store, findRefreshError: persistenceError}
		if err := svc.Logout(ctx, refresh); !errors.Is(err, persistenceError) {
			t.Fatalf("expected logout lookup failure, got %v", err)
		}
	})

	t.Run("logout of a valid but unknown refresh token is idempotent", func(t *testing.T) {
		svc, _ := loginForRefreshContract(t)
		_, refresh, _, _, err := svc.JWT.IssuePair("unknown-user", "unknown-tenant", "unknown", model.RoleTenantAdmin)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Logout(ctx, refresh); err != nil {
			t.Fatalf("unknown valid refresh token must be idempotent: %v", err)
		}
	})
}

// ScenarioID: SC-AUTH-001
func TestP0_AUTH_001_BootstrapAdminFailureContract(t *testing.T) {
	ctx := context.Background()
	persistenceError := errors.New("bootstrap persistence unavailable")

	t.Run("reports system tenant lookup failure", func(t *testing.T) {
		svc := newAuthzSvc(t)
		svc.Store = &authFailureStore{Store: svc.Store, getTenantError: persistenceError}
		if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); !errors.Is(err, persistenceError) {
			t.Fatalf("expected tenant lookup failure, got %v", err)
		}
	})

	t.Run("reports tenant creation failure", func(t *testing.T) {
		svc := newBootstrapSvc(t)
		svc.Store = &authFailureStore{Store: svc.Store, getTenantNil: true, createTenantError: persistenceError}
		if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); !errors.Is(err, persistenceError) {
			t.Fatalf("expected tenant creation failure, got %v", err)
		}
	})

	t.Run("reports primary role reconciliation failure", func(t *testing.T) {
		svc := newAuthzSvc(t)
		svc.Store = &authFailureStore{Store: svc.Store, setPrimaryRoleError: persistenceError}
		if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); !errors.Is(err, persistenceError) {
			t.Fatalf("expected primary role failure, got %v", err)
		}
	})

	t.Run("reports admin creation failure", func(t *testing.T) {
		svc := newBootstrapSvc(t)
		svc.Store = &authFailureStore{Store: svc.Store, createUserError: persistenceError}
		if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); !errors.Is(err, persistenceError) {
			t.Fatalf("expected admin creation failure, got %v", err)
		}
	})
}

func loginForRefreshContract(t *testing.T) (*Service, string) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "refresh-contract.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	if err := svc.BootstrapAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "rotation contract")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "rotation-user", Password: "secret123", Role: model.RoleTenantAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	login, err := svc.Login(ctx, "rotation-user", "secret123", "192.0.2.20")
	if err != nil {
		t.Fatal(err)
	}
	return svc, login.Refresh
}

func newBootstrapSvc(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "bootstrap-contract.db")})
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
