package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"gorm.io/gorm"
)

func deploymentConfig() config.Config {
	return config.Config{
		Server:   config.Server{Mode: "release"},
		App:      config.App{Name: "ragflow-x", JWTSecret: "0123456789abcdef0123456789abcdef"},
		Database: config.Database{Driver: "postgres", Name: "drill", Host: "db", MaxOpenConns: 10, SSLMode: "require"},
		RAGFlow:  config.RAGFlow{Provider: "http", BaseURL: "http://ragflow.internal"},
		Security: config.Security{MaxRequestBytes: 1024},
	}
}

func newDrillDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "drill.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	tenant := model.Tenant{ID: "tenant", Name: "Tenant", Status: model.TenantStatusActive}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	record := model.AuditLog{
		ID: "audit", Seq: 1, TenantID: tenant.ID, Action: "test", Resource: "deployment", At: time.Now().UTC(),
	}
	record.Hash = model.AuditHash("", &record)
	if err := database.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return database
}

func newHealthServer(t *testing.T, ready bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/healthz" && request.URL.Path != "/api/v1/readyz" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v1/readyz" && !ready {
			writer.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(writer, `{"status":"unavailable"}`)
			return
		}
		fmt.Fprint(writer, `{"status":"ok"}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func productionSecurityConfig() config.Config {
	cfg := deploymentConfig()
	cfg.Security = config.Security{
		AllowedOrigins:              []string{"https://app.example.com"},
		AllowedMethods:              []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowedHeaders:              []string{"Content-Type", "Authorization", "X-Request-Id"},
		AllowCredentials:            true,
		MaxAgeSec:                   600,
		HSTSEnabled:                 true,
		HSTSMaxAgeSec:               31536000,
		HSTSIncludeSubdomains:       true,
		ContentSecurityPolicy:       "default-src 'none'; frame-ancestors 'none'",
		FrameOptions:                "DENY",
		MaxRequestBytes:             1 << 20,
		AllowPrivateProviderBaseURL: false,
	}
	cfg.Observability = config.Observability{MetricsEnabled: true, MetricsToken: "metrics-token"}
	cfg.Setup.RateLimitPerMin = 10
	cfg.Gateway.RateLimitPerMin = 120
	cfg.AuditAnchor = config.AuditAnchor{Enabled: true, IntervalSec: 3600, ExportDir: "/mnt/worm/audit-anchors"}
	return cfg
}

func newSecurityBaselineServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		switch request.URL.Path {
		case "/api/v1/healthz", "/api/v1/readyz":
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/auth/logout":
			http.SetCookie(writer, &http.Cookie{
				Name: "rgx_access", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			http.SetCookie(writer, &http.Cookie{
				Name: "rgx_refresh", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			writer.WriteHeader(http.StatusOK)
		case "/api/v1/auth/me":
			if request.Header.Get("Origin") == "https://cross-site.example" && request.Header.Get("Cookie") != "" {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.WriteHeader(http.StatusUnauthorized)
		case "/api/v1/tenants":
			writer.WriteHeader(http.StatusUnauthorized)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeAuditBackup(t *testing.T) (string, string) {
	t.Helper()
	backup := filepath.Join(t.TempDir(), "audit.dump")
	manifest := backup + ".sha256"
	content := []byte("audit backup")
	if err := os.WriteFile(backup, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	manifestData := fmt.Sprintf("sha256:%s\nsize:%d\ntimestamp:20260905T010203Z\n", hex.EncodeToString(sum[:]), len(content))
	if err := os.WriteFile(manifest, []byte(manifestData), 0o600); err != nil {
		t.Fatal(err)
	}
	return backup, manifest
}

func TestRun_PassesHealthyDeployment(t *testing.T) {
	database := newDrillDB(t)
	server := newHealthServer(t, true)
	backup, manifest := writeAuditBackup(t)
	report, err := Run(context.Background(), Options{
		Config:            deploymentConfig(),
		BaseURL:           server.URL,
		Timeout:           30 * time.Second,
		DB:                database,
		Provider:          ragflow.NewMock(),
		AuditTenantID:     "tenant",
		AuditBackupPath:   backup,
		AuditManifestPath: manifest,
		HTTPClient:        server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Overall != "passed" {
		t.Fatalf("overall = %s, checks = %+v", report.Overall, report.Checks)
	}
	if report.Summary.Total != 7 || report.Summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
}

func TestRun_SecurityBaselinePassesProductionProbes(t *testing.T) {
	server := newSecurityBaselineServer(t)
	report, err := Run(context.Background(), Options{
		Config:           productionSecurityConfig(),
		BaseURL:          server.URL,
		Timeout:          30 * time.Second,
		SecurityBaseline: true,
		HTTPClient:       server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Overall != "passed" {
		t.Fatalf("overall = %s, checks = %+v", report.Overall, report.Checks)
	}
	if report.Summary.Total != 4 || report.Summary.Passed != 3 || report.Summary.Skipped != 1 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	var security *CheckResult
	for index := range report.Checks {
		if report.Checks[index].Name == "security_baseline" {
			security = &report.Checks[index]
		}
	}
	if security == nil || security.Details["cors_origin_mode"] != "explicit-https" || security.Details["audit_anchor_export"] != true {
		t.Fatalf("unexpected security result: %+v", security)
	}
}

func TestRun_SecurityBaselineFailsWeakConfiguration(t *testing.T) {
	server := newSecurityBaselineServer(t)
	cfg := productionSecurityConfig()
	cfg.Security.HSTSEnabled = false
	report, err := Run(context.Background(), Options{
		Config: cfg, BaseURL: server.URL, Timeout: time.Second,
		SecurityBaseline: true, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Overall != "failed" || report.Summary.Failed != 1 {
		t.Fatalf("overall = %s, summary = %+v", report.Overall, report.Summary)
	}
	var security *CheckResult
	for index := range report.Checks {
		if report.Checks[index].Name == "security_baseline" {
			security = &report.Checks[index]
		}
	}
	if security == nil || security.Details["configuration"] != false || !strings.Contains(security.Error, "hsts") {
		t.Fatalf("unexpected security result: %+v", security)
	}
}

func TestRun_FailsTamperedAuditAndUnreadyServer(t *testing.T) {
	database := newDrillDB(t)
	server := newHealthServer(t, false)
	if err := database.Model(&model.AuditLog{}).Where("tenant_id = ?", "tenant").Update("hash", "bad").Error; err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), Options{
		Config: deploymentConfig(), BaseURL: server.URL, Timeout: time.Second, DB: database,
		AuditTenantID: "tenant", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Overall != "failed" || report.Summary.Failed < 2 {
		t.Fatalf("overall = %s, summary = %+v", report.Overall, report.Summary)
	}
	markdown, err := report.RenderMarkdown()
	if err != nil || !bytes.Contains(markdown, []byte("audit_chain")) {
		t.Fatalf("RenderMarkdown: %v %s", err, markdown)
	}
}

func TestParseAuditManifest_RejectsDuplicateKeys(t *testing.T) {
	hash := strings.Repeat("a", 64)
	manifest := "sha256:" + hash + "\nsha256:" + hash + "\nsize:1\ntimestamp:20260905T010203Z"
	if _, err := parseAuditManifest(manifest); err == nil {
		t.Fatal("duplicate manifest key accepted")
	}
}
