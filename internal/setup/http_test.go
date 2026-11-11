package setup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
)

// wizardFixture boots a Manager whose configured database is unreachable, i.e.
// exactly what a freshly unpacked release binary does when nobody edited
// config/config.yaml: Startup falls back to the setup wizard.
func wizardFixture(t *testing.T) (*Manager, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		App: config.App{Name: "ragflow-x", JWTSecret: strongTestJWTSecret, JWTExpireHours: 24, EncryptionKey: "test-encryption-key", DataDir: dir},
		// Postgres on a closed port: buildEngine must fail, so startup lands on
		// the bootstrap router instead of the full application.
		Database:  config.Database{Driver: "postgres", Host: "127.0.0.1", Port: 1, User: "nobody", Password: "nobody", Name: "nobody"},
		RAGFlow:   config.RAGFlow{Provider: "mock"},
		FactGuard: config.FactGuard{Mode: "strict"},
		Setup: config.Setup{
			Enabled:         true,
			RateLimitPerMin: 100,
			SecretsFile:     filepath.Join(dir, "secrets.json"),
			RSAKeyPath:      filepath.Join(dir, "rsa.pem"),
		},
	}
	m, err := New(cfg, func(http.Handler) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, m.bootstrapRouter()
}

func postJSON(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestFirstRunWizardCompletesOverHTTP walks the whole "unpack and run" path a
// browser takes: the wizard page is served, it reads the status and the RSA
// public key, preflights, applies, and the engine is swapped in place — all
// without anyone editing a config file first.
func TestFirstRunWizardCompletesOverHTTP(t *testing.T) {
	m, h := wizardFixture(t)

	// 1. The browser lands on the UI, and the wizard's own probe says "not
	//    configured yet" (that is what makes it render the wizard).
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "RAGFlow-X") {
		t.Fatalf("GET / must serve the bundled UI: %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/setup/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status: %d %s", rec.Code, rec.Body.String())
	}
	var status struct {
		Data struct {
			Configured bool `json:"configured"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Data.Configured {
		t.Fatal("fresh first run must report configured=false")
	}

	// 2. The wizard fetches the RSA public key and seals every secret with it.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/setup/public-key", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public key: %d %s", rec.Code, rec.Body.String())
	}
	var pk struct {
		Data struct {
			PublicKey string `json:"public_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pk); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pk.Data.PublicKey, "BEGIN PUBLIC KEY") {
		t.Fatalf("public key endpoint returned no PEM: %q", pk.Data.PublicKey)
	}

	seal := func(s string) string {
		out, err := m.rsa.Encrypt(s)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	payload := map[string]any{
		// A real operator picks sqlite to try the product without extra services;
		// the wizard must be able to go all the way with that choice.
		"database": map[string]any{
			"driver":       "sqlite",
			"dsn":          dbtest.SQLiteDSN(t, "wizard.db"),
			"password_enc": seal("db-pass"),
		},
		"ragflow": map[string]any{"provider": "mock", "base_url": "http://engine", "api_key_enc": seal("engine-key")},
		"admin":   map[string]any{"username": "root", "password_enc": seal("strong-admin-pass")},
	}

	// 3. Preflight validates without touching anything.
	if rec := postJSON(t, h, "/api/v1/system/setup/preflight", payload); rec.Code != http.StatusOK {
		t.Fatalf("preflight: %d %s", rec.Code, rec.Body.String())
	}

	// 4. Apply persists secrets and swaps the router.
	if rec := postJSON(t, h, "/api/v1/system/setup/apply", payload); rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}

	// 5. The wizard is now closed for business: status flips to configured.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/setup/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status after apply: %d %s", rec.Code, rec.Body.String())
	}
	status.Data.Configured = false
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Data.Configured {
		t.Fatalf("after apply the instance must report configured=true: %s", rec.Body.String())
	}
}

// TestApplyRejectsUnencryptedAdminPassword keeps the wizard from accepting a
// plaintext password: every secret field is RSA-sealed, and a body that skips
// that must be refused rather than stored.
func TestApplyRejectsUnencryptedAdminPassword(t *testing.T) {
	_, h := wizardFixture(t)
	payload := map[string]any{
		"database": map[string]any{"driver": "sqlite", "dsn": dbtest.SQLiteDSN(t, "wizard2.db")},
		"ragflow":  map[string]any{"provider": "mock", "base_url": "http://engine"},
		"admin":    map[string]any{"username": "root", "password_enc": "not-encrypted"},
	}
	if rec := postJSON(t, h, "/api/v1/system/setup/apply", payload); rec.Code == http.StatusOK {
		t.Fatalf("apply must reject a non-encrypted password, got %d %s", rec.Code, rec.Body.String())
	}
}
