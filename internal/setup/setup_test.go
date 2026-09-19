package setup

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/pkg/secretstore"
)

// strongTestJWTSecret satisfies jwt.ValidateSecret (>= 32 bytes).
const strongTestJWTSecret = "test-jwt-secret-0123456789abcdef0123456789abcdef"

func TestApplyPersistsAndReconnects(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	var swapped http.Handler
	cfg := &config.Config{
		App:      config.App{Name: "ragflow-x", JWTSecret: strongTestJWTSecret, JWTExpireHours: 24, EncryptionKey: "test-encryption-key", DataDir: dir},
		Database: config.Database{Driver: "sqlite", DSN: filepath.Join(dir, "s.db")},
		RAGFlow:  config.RAGFlow{Provider: "mock"},
		Setup: config.Setup{
			Enabled:     true,
			SecretsFile: filepath.Join(dir, "secrets.json"),
			RSAKeyPath:  filepath.Join(dir, "rsa.pem"),
		},
	}

	m, err := New(cfg, func(h http.Handler) error {
		swapped = h
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	seal := func(s string) string {
		out, err := m.rsa.Encrypt(s)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	req := ApplyRequest{
		Database: DatabaseReq{Driver: "sqlite", Name: "main", Password: seal("db-pass")},
		RAGFlow:  RAGFlowReq{Provider: "mock", BaseURL: "http://engine", APIKey: seal("engine-key")},
		Admin:    &AdminReq{Username: "root", Password: seal("strong-admin-pass")},
	}
	if err := m.Apply(ctx, req); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if swapped == nil {
		t.Fatal("expected the running handler to be swapped after apply")
	}
	if m.svc == nil {
		t.Fatal("expected operational service after apply")
	}
	if !m.Configured() {
		t.Fatal("expected manager to report configured")
	}

	u, err := m.svc.Store.GetUserByUsername(ctx, "root")
	if err != nil || u == nil {
		t.Fatalf("first admin not created: %v", err)
	}
	if u.Role != "platform_admin" {
		t.Fatalf("expected platform_admin, got %s", u.Role)
	}

	// The on-disk file must reload with the same master key and contain the
	// plaintext secrets only after decryption (never literally on disk).
	loaded, err := secretstore.Load(cfg.Setup.SecretsFile, m.masterKey)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.RAGFlow.APIKey != "engine-key" {
		t.Fatalf("reload mismatch: %+v", loaded)
	}
	if loaded.JWTSecret != strongTestJWTSecret {
		t.Fatalf("expected operator-supplied jwt secret to be persisted, got %q", loaded.JWTSecret)
	}
	if raw, err := os.ReadFile(cfg.Setup.SecretsFile); err == nil {
		if contains(string(raw), "engine-key") {
			t.Fatal("plaintext api key present on disk")
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestDatabaseDSNRoundTrip(t *testing.T) {
	cfg := &config.Config{Database: config.Database{Driver: "sqlite", DSN: "file:/tmp/op.db?cache=shared"}}
	sec := &secretstore.Secrets{Database: toSecretDB(cfg.Database)}

	// Persisted secret carries the DSN for SQLite.
	if sec.Database.DSN != cfg.Database.DSN || sec.Database.Driver != "sqlite" {
		t.Fatalf("toSecretDB must preserve sqlite DSN: %+v", sec.Database)
	}
	// Merging brings it back into the runtime config.
	merged := mergeDB(cfg, sec)
	if merged.Driver != "sqlite" || merged.DSN != cfg.Database.DSN {
		t.Fatalf("mergeDB must restore sqlite DSN: %+v", merged)
	}

	// Empty secret DSN falls back to the base config DSN.
	merge2 := mergeDB(cfg, &secretstore.Secrets{Database: secretstore.Database{Driver: "sqlite"}})
	if merge2.DSN != cfg.Database.DSN {
		t.Fatalf("mergeDB must fall back to base DSN: %q", merge2.DSN)
	}
}

func TestValidateConnectionConfig(t *testing.T) {
	validPostgres := config.Database{Driver: "postgres", Host: "db", User: "postgres", Password: "secret", Name: "ragflow-x"}
	validRAGFlow := config.RAGFlow{Provider: "http", BaseURL: "http://engine", APIKey: "key"}
	if err := validateConnectionConfig(validPostgres, validRAGFlow); err != nil {
		t.Fatalf("valid postgres config rejected: %v", err)
	}
	if err := validateConnectionConfig(config.Database{Driver: "postgres", Host: "db", User: "postgres", Name: "ragflow-x"}, validRAGFlow); err == nil {
		t.Fatal("expected postgres without password to be rejected")
	}
	if err := validateConnectionConfig(config.Database{Driver: "sqlite"}, validRAGFlow); err == nil {
		t.Fatal("expected sqlite without dsn to be rejected")
	}
}
