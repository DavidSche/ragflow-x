package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
)

// TestBootstrapRouterAnswersSPABootCalls covers the regression a first run
// showed in the browser console: the bundled login page asks for branding, OIDC
// status and logout before the wizard can render, and the bootstrap router used
// to answer all three with 404. Those 404s are the first thing an operator sees,
// so they read as "the product is broken" on a perfectly healthy first run.
func TestBootstrapRouterAnswersSPABootCalls(t *testing.T) {
	_, h := wizardFixture(t)

	cases := []struct {
		method, path string
		status       int
		assert       func(t *testing.T, body map[string]any)
	}{
		{http.MethodGet, "/api/v1/branding/public", http.StatusOK, func(t *testing.T, body map[string]any) {
			data, _ := body["data"].(map[string]any)
			if data["name"] != "ragflow-x" {
				t.Fatalf("branding name = %v, want the app name", data["name"])
			}
			if _, ok := data["logo"]; !ok {
				t.Fatal("branding must carry a logo field (empty is fine)")
			}
		}},
		{http.MethodGet, "/api/v1/auth/oidc/status", http.StatusOK, func(t *testing.T, body map[string]any) {
			data, _ := body["data"].(map[string]any)
			// No database means no OIDC settings: the login page must not render
			// an SSO button that cannot work.
			if data["enabled"] != false {
				t.Fatalf("oidc enabled = %v, want false during setup", data["enabled"])
			}
		}},
		{http.MethodPost, "/api/v1/auth/logout", http.StatusOK, func(t *testing.T, body map[string]any) {
			if body["code"] != float64(0) {
				t.Fatalf("logout code = %v, want 0", body["code"])
			}
		}},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if rec.Code != tc.status {
			t.Fatalf("%s %s = %d, want %d (%s)", tc.method, tc.path, rec.Code, tc.status, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: not JSON: %v", tc.method, tc.path, err)
		}
		tc.assert(t, body)
	}

	// The stubs must not open the router to anything else.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("unknown API path must stay a JSON 404, got %d %q", rec.Code, rec.Body.String())
	}
}

// TestBuiltinPlaceholderDatabaseOpensTheWizardDirectly pins the unpack-and-run
// path: with no config file and nothing typed, the shipped placeholder
// credentials cannot work, so startup must not spend a dial timeout and a red
// driver error on them.
func TestBuiltinPlaceholderDatabaseOpensTheWizardDirectly(t *testing.T) {
	def := config.Database{Driver: "postgres", Host: "localhost", Port: 5432, User: "ragflow_x", Password: defaultDBPassword, Name: "ragflow_x", SSLMode: "disable"}

	cfgWith := func(mutate func(*config.Config)) *config.Config {
		cfg := &config.Config{
			App:       config.App{Name: "ragflow-x", JWTSecret: strongTestJWTSecret, JWTExpireHours: 24, EncryptionKey: "test-encryption-key"},
			Database:  def,
			RAGFlow:   config.RAGFlow{Provider: "mock"},
			FactGuard: config.FactGuard{Mode: "strict"},
			Setup:     config.Setup{Enabled: true, RateLimitPerMin: 100},
		}
		if mutate != nil {
			mutate(cfg)
		}
		dir := t.TempDir()
		cfg.App.DataDir = dir
		cfg.Setup.SecretsFile = filepath.Join(dir, "secrets.json")
		cfg.Setup.RSAKeyPath = filepath.Join(dir, "rsa.pem")
		return cfg
	}

	// 1. Untouched defaults, no config file: placeholder.
	m, err := New(cfgWith(nil), func(http.Handler) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !m.builtinPlaceholderDatabase() {
		t.Fatal("default config must be recognised as the built-in placeholder")
	}
	engine, err := m.Startup()
	if err != nil {
		t.Fatalf("startup on a fresh unpack must not fail: %v", err)
	}
	if engine == nil {
		t.Fatal("startup must return the bootstrap router")
	}
	if m.Configured() {
		t.Fatal("a placeholder database must not count as configured")
	}
	_ = m.Close()

	// 2. The very same values written into a config file are the operator's
	//    decision and must be honoured (a dev box really can run this).
	honoured := []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"config file present", func(c *config.Config) { c.Setup.ConfigFile = "/etc/ragflow-x/config.yaml" }},
		{"different host", func(c *config.Config) { c.Database.Host = "10.0.0.5" }},
		{"different password", func(c *config.Config) { c.Database.Password = "real-password" }},
		{"sqlite", func(c *config.Config) {
			c.Database = config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "placeholder.db")}
		}},
	}
	for _, tc := range honoured {
		mm, err := New(cfgWith(tc.mutate), func(http.Handler) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		if mm.builtinPlaceholderDatabase() {
			t.Fatalf("%s: must not be treated as the placeholder", tc.name)
		}
		_ = mm.Close()
	}
}

// TestProbeReportsWhyTheDatabaseIsDown makes the first-run failure actionable:
// "数据库不可达" alone told an operator nothing, while the actual cause was that
// the target database had never been created.
func TestProbeReportsWhyTheDatabaseIsDown(t *testing.T) {
	// A missing parent directory is the portable way to make sqlite refuse to
	// open a file (it reports "unable to open database file").
	sqliteDSN := "file:" + filepath.Join(t.TempDir(), "missing-dir", "rgx.db")
	res := &Status{}
	probeDB(context.Background(), config.Database{Driver: "sqlite", DSN: sqliteDSN}, res)
	if res.DBUp {
		t.Fatal("probe must report the database as down")
	}
	if res.DBError == "" {
		t.Fatal("probe must report the driver message")
	}
	if res.DBHint != DBHintNotWritable {
		t.Fatalf("hint = %q, want %q (message: %s)", res.DBHint, DBHintNotWritable, res.DBError)
	}

	// A closed port must read as "unreachable", not as a credential problem.
	res = &Status{}
	probeDB(context.Background(), config.Database{Driver: "postgres", Host: "127.0.0.1", Port: 1, User: "u", Password: "p", Name: "n"}, res)
	if res.DBUp || res.DBHint != DBHintUnreachable {
		t.Fatalf("closed port: up=%v hint=%q", res.DBUp, res.DBHint)
	}
}

// TestClassifyDBError maps driver errors to the hint the wizard prints.
func TestClassifyDBError(t *testing.T) {
	cases := []struct {
		driver, msg, want string
	}{
		{"postgres", `failed to connect: FATAL: database "ragflow_x" does not exist (SQLSTATE 3D000)`, DBHintDatabaseMissing},
		{"postgres", `FATAL: password authentication failed for user "ragflow_x" (SQLSTATE 28P01)`, DBHintAuthFailed},
		{"postgres", "dial error: dial tcp 10.20.30.40:5432: connect: connection refused", DBHintUnreachable},
		{"sqlite", "attempt to write a readonly database", DBHintNotWritable},
		{"sqlite", "unable to open database file: no such file or directory", DBHintNotWritable},
		{"postgres", `FATAL: role "ragflow_x" does not exist (SQLSTATE 28000)`, DBHintAuthFailed},
		{"postgres", "attempt to write a readonly transaction", DBHintNotWritable},
		{"postgres", "something nobody has seen before", DBHintUnknown},
	}
	for _, tc := range cases {
		msg, hint := classifyDBError(tc.driver, errString(tc.msg))
		if msg != tc.msg {
			t.Fatalf("message must be passed through verbatim, got %q", msg)
		}
		if hint != tc.want {
			t.Fatalf("%s: %q -> hint %q, want %q", tc.driver, tc.msg, hint, tc.want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestPreflightSurfacesTheReasonOverHTTP is the operator-facing half: the wizard
// reads preflight, so the reason must survive the HTTP envelope.
func TestPreflightSurfacesTheReasonOverHTTP(t *testing.T) {
	m, h := wizardFixture(t)
	seal := func(s string) string {
		out, err := m.rsa.Encrypt(s)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	payload := map[string]any{
		"database": map[string]any{
			"driver": "postgres", "host": "127.0.0.1", "port": 1,
			"user": "postgres", "name": "ragflow_x", "sslmode": "disable",
			"password_enc": seal("x"),
		},
		"ragflow": map[string]any{"provider": "http", "base_url": "http://engine:9380", "api_key_enc": seal("k")},
		"admin":   map[string]any{"username": "root", "password_enc": seal("strong-admin-pass")},
	}
	rec := postJSON(t, h, "/api/v1/system/setup/preflight", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			DBUp    bool   `json:"db_up"`
			DBError string `json:"db_error"`
			DBHint  string `json:"db_hint"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.DBUp {
		t.Fatal("preflight must report the unreachable database as down")
	}
	if body.Data.DBError == "" || body.Data.DBHint == "" {
		t.Fatalf("preflight must carry the reason and a hint: %s", rec.Body.String())
	}
}

// TestApplyUpdatesAnExistingAdminPassword is the trap a second wizard run used to
// fall into: buildEngine's bootstrap is idempotent and leaves an existing account
// alone, so submitting a new admin password changed nothing — and the operator
// then could not sign in with the password the wizard had just accepted.
func TestApplyUpdatesAnExistingAdminPassword(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	cfg := &config.Config{
		App:       config.App{Name: "ragflow-x", JWTSecret: strongTestJWTSecret, JWTExpireHours: 24, EncryptionKey: "test-encryption-key", DataDir: dir},
		Database:  config.Database{Driver: "sqlite", DSN: filepath.Join(dir, "rerun.db")},
		RAGFlow:   config.RAGFlow{Provider: "mock"},
		FactGuard: config.FactGuard{Mode: "strict"},
		Setup: config.Setup{
			Enabled:     true,
			SecretsFile: filepath.Join(dir, "secrets.json"),
			RSAKeyPath:  filepath.Join(dir, "rsa.pem"),
			AdminUser:   "root",
		},
	}
	m, err := New(cfg, func(http.Handler) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	apply := func(pass string) {
		t.Helper()
		req := ApplyRequest{
			Database: DatabaseReq{Driver: "sqlite", Name: "main", Password: sealFor(t, m, "db-pass")},
			RAGFlow:  RAGFlowReq{Provider: "mock", BaseURL: "http://engine", APIKey: sealFor(t, m, "engine-key")},
			Admin:    &AdminReq{Username: "root", Password: sealFor(t, m, pass)},
		}
		if err := m.Apply(ctx, req); err != nil {
			t.Fatalf("apply(%s): %v", pass, err)
		}
	}

	apply("first-password")
	if _, err := m.svc.Login(ctx, "root", "first-password", "127.0.0.1"); err != nil {
		t.Fatalf("first password must sign in: %v", err)
	}

	apply("second-password")
	if _, err := m.svc.Login(ctx, "root", "second-password", "127.0.0.1"); err != nil {
		t.Fatalf("re-running the wizard must set the password it accepted: %v", err)
	}
	if _, err := m.svc.Login(ctx, "root", "first-password", "127.0.0.1"); err == nil {
		t.Fatal("the previous password must no longer work")
	}
}

func sealFor(t *testing.T, m *Manager, s string) string {
	t.Helper()
	out, err := m.rsa.Encrypt(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
