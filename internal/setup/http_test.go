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

// TestBootstrapRouterAdminStepExplainsItself covers the wizard state a launcher
// trial lands in — database reachable, engine not configured yet, so only the
// bootstrap router exists. The SPA's admin-only step posts /system/setup/admin;
// that endpoint lives on the full router, and a bare 404 there was a dead end
// (the browser logged "Failed to load resource: 404" and the form did nothing).
func TestBootstrapRouterAdminStepExplainsItself(t *testing.T) {
	_, h := wizardFixture(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/setup/status", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: code = %d, body %s", rec.Code, rec.Body.String())
	}
	var st struct {
		Data Status `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("status body: %v", err)
	}
	if st.Data.Mode != SetupModeWizard {
		t.Fatalf("status mode = %q, want %q: the wizard must know the admin-only step is unavailable", st.Data.Mode, SetupModeWizard)
	}

	rec = postJSON(t, h, "/api/v1/system/setup/admin", map[string]string{"username": "admin", "password": "RgxAdmin#2026"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /admin: code = %d, want %d (body %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("admin body: %v", err)
	}
	if body.Code != 40009 {
		t.Fatalf("POST /admin: code = %d, want 40009", body.Code)
	}
	if !strings.Contains(body.Message, "setup/apply") {
		t.Fatalf("POST /admin: message must name the endpoint that does work, got %q", body.Message)
	}
}

// TestBootstrapRouterServesContainerProbes keeps the image's HEALTHCHECK honest.
// It curls /api/v1/readyz; those probe endpoints only existed on the full router,
// so a freshly started container in wizard mode was reported `unhealthy` for its
// whole first run even though it was serving the wizard correctly.
func TestBootstrapRouterServesContainerProbes(t *testing.T) {
	_, h := wizardFixture(t)

	for path, wantKey := range map[string]string{
		"/api/v1/healthz": "status",
		"/api/v1/readyz":  "setup_required",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: code = %d, want 200 (body %s)", path, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("GET %s: body: %v", path, err)
		}
		if _, okKey := body[wantKey]; !okKey {
			t.Fatalf("GET %s: body %v missing %q", path, body, wantKey)
		}
	}
}
