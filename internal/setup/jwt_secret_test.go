package setup

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/secretstore"
)

// newTestManager builds a Manager wired to a temp secrets store.
func newTestManager(t *testing.T, jwtSecret string) *Manager {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		App: config.App{Name: "ragflow-x", JWTSecret: jwtSecret, JWTExpireHours: 24, DataDir: dir},
		Setup: config.Setup{
			Enabled:     true,
			SecretsFile: filepath.Join(dir, "secrets.json"),
			RSAKeyPath:  filepath.Join(dir, "rsa.pem"),
		},
	}
	m, err := New(cfg, func(h http.Handler) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestEnsureJWTSecretGeneratesAndPersistsWhenUnset(t *testing.T) {
	m := newTestManager(t, "")
	sec := &secretstore.Secrets{}

	if err := m.ensureJWTSecret(sec); err != nil {
		t.Fatalf("ensureJWTSecret with empty config failed: %v", err)
	}
	if sec.JWTSecret == "" {
		t.Fatal("expected a generated jwt secret")
	}
	if err := jwt.ValidateSecret(sec.JWTSecret); err != nil {
		t.Fatalf("generated secret must pass validation: %v", err)
	}

	// The generated value must survive restarts via the encrypted secrets file.
	loaded, err := secretstore.Load(m.cfg.Setup.SecretsFile, m.masterKey)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.JWTSecret != sec.JWTSecret {
		t.Fatalf("persisted jwt secret mismatch: %+v vs %q", loaded, sec.JWTSecret)
	}
}

func TestEnsureJWTSecretReusesPersistedValue(t *testing.T) {
	m := newTestManager(t, "change-me-in-prod") // insecure explicit value must be ignored
	sec := &secretstore.Secrets{JWTSecret: strongTestJWTSecret}

	if err := m.ensureJWTSecret(sec); err != nil {
		t.Fatalf("valid persisted secret must be reused as-is: %v", err)
	}
	if sec.JWTSecret != strongTestJWTSecret {
		t.Fatalf("persisted secret was mutated: %q", sec.JWTSecret)
	}
}

// ScenarioID: SC-SECRET-001
func TestP0_SECRET_001_EnsureJWTSecretRejectsInsecureExplicitValues(t *testing.T) {
	// Note: an empty explicit value is NOT rejected here — it triggers secure
	// generation (TestEnsureJWTSecretGeneratesAndPersistsWhenUnset). Only
	// explicitly configured weak/placeholder values are refused.
	cases := map[string]string{
		"placeholder": "change-me-in-prod",
		"too short":   "jwt-secret",
	}
	for name, cfgSecret := range cases {
		m := newTestManager(t, cfgSecret)
		sec := &secretstore.Secrets{}
		err := m.ensureJWTSecret(sec)
		if err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
		if !strings.Contains(err.Error(), "jwt secret") {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
}
