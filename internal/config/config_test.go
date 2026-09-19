package config

import "testing"

func TestDefaults_ServerPort(t *testing.T) {
	cfg := defaults()
	if cfg.Server.Port != 9191 {
		t.Fatalf("default server port must be 9191, got %d", cfg.Server.Port)
	}
}

func TestDefaults_OIDCRequiresVerifiedEmail(t *testing.T) {
	cfg := defaults()
	if !cfg.OIDC.RequireEmailVerified {
		t.Fatal("OIDC must require verified email claims by default")
	}
}

func TestDefaults_NoHardcodedEncryptionKey(t *testing.T) {
	cfg := defaults()
	if cfg.App.EncryptionKey != "" {
		t.Fatalf("default encryption key must be empty, got %q", cfg.App.EncryptionKey)
	}
}

func TestDefaults_GatewayTimeouts(t *testing.T) {
	cfg := defaults()
	if cfg.Gateway.NonStreamTimeoutSec != 120 {
		t.Fatalf("expected non-stream timeout 120s, got %d", cfg.Gateway.NonStreamTimeoutSec)
	}
	if cfg.Gateway.StreamHeaderTimeoutSec != 30 {
		t.Fatalf("expected stream header timeout 30s, got %d", cfg.Gateway.StreamHeaderTimeoutSec)
	}
	if cfg.Gateway.StreamTimeoutSec != 0 {
		t.Fatalf("expected streaming overall timeout to be 0 (unlimited), got %d", cfg.Gateway.StreamTimeoutSec)
	}
}

func TestDefaults_Observability(t *testing.T) {
	cfg := defaults()
	if !cfg.Observability.MetricsEnabled {
		t.Fatal("metrics should be enabled by default")
	}
	if cfg.Observability.MetricsPath != "/metrics" {
		t.Fatalf("unexpected metrics path: %s", cfg.Observability.MetricsPath)
	}
	if cfg.Observability.TracingEnabled {
		t.Fatal("tracing should be opt-in")
	}
}

func TestDefaults_Usage(t *testing.T) {
	cfg := defaults()
	if cfg.Usage.CostPer1KTokens <= 0 {
		t.Fatalf("expected a default estimated-cost rate, got %v", cfg.Usage.CostPer1KTokens)
	}
	if cfg.Usage.Currency == "" {
		t.Fatal("expected a default currency")
	}
}

func TestDefaults_RAGFlowRetry(t *testing.T) {
	cfg := defaults()
	if cfg.RAGFlow.RetryMaxRetries != 3 {
		t.Fatalf("expected default retry_max_retries 3, got %d", cfg.RAGFlow.RetryMaxRetries)
	}
	if cfg.RAGFlow.RetryBackoffMs != 100 {
		t.Fatalf("expected default retry_backoff_ms 100, got %d", cfg.RAGFlow.RetryBackoffMs)
	}
}

func TestDefaults_Retention(t *testing.T) {
	cfg := defaults()
	if cfg.Retention.Enabled {
		t.Fatal("data retention must default to disabled (no implicit deletion)")
	}
	if cfg.Retention.AuditDays <= 0 || cfg.Retention.UsageDays <= 0 ||
		cfg.Retention.FeedbackDays <= 0 || cfg.Retention.JobDays <= 0 {
		t.Fatalf("expected positive per-class TTLS, got %+v", cfg.Retention)
	}
	if cfg.Retention.IntervalSec <= 0 {
		t.Fatalf("expected a positive janitor interval, got %d", cfg.Retention.IntervalSec)
	}
}

func TestApplyEnv_RetentionOverrides(t *testing.T) {
	cfg := defaults()
	t.Setenv("RGX_RETENTION_ENABLED", "true")
	t.Setenv("RGX_RETENTION_AUDIT_DAYS", "90")
	t.Setenv("RGX_RETENTION_USAGE_DAYS", "30")
	t.Setenv("RGX_RETENTION_FEEDBACK_DAYS", "45")
	t.Setenv("RGX_RETENTION_JOB_DAYS", "10")
	t.Setenv("RGX_RETENTION_INTERVAL_SEC", "3600")
	t.Setenv("RGX_RUNTIME_REPORT_TOKEN", "report-token")
	t.Setenv("RGX_RUNTIME_HEARTBEAT_TIMEOUT_SEC", "120")
	applyEnv(cfg)
	if !cfg.Retention.Enabled {
		t.Fatal("RGX_RETENTION_ENABLED=true must enable retention")
	}
	if cfg.Retention.AuditDays != 90 || cfg.Retention.UsageDays != 30 ||
		cfg.Retention.FeedbackDays != 45 || cfg.Retention.JobDays != 10 {
		t.Fatalf("unexpected TTL overrides: %+v", cfg.Retention)
	}
	if cfg.Retention.IntervalSec != 3600 {
		t.Fatalf("expected interval 3600, got %d", cfg.Retention.IntervalSec)
	}
	if cfg.Runtime.HeartbeatTimeoutSec != 120 || cfg.Runtime.ReportToken != "report-token" {
		t.Fatalf("unexpected runtime overrides: %+v", cfg.Runtime)
	}
}

func TestApplyEnv_RAGFlowConnectionOverrides(t *testing.T) {
	cfg := defaults()
	t.Setenv("RGX_RAGFLOW_TIMEOUT", "45")
	t.Setenv("RGX_RAGFLOW_MAX_CONNS", "50")
	t.Setenv("RGX_RAGFLOW_RETRY_MAX_RETRIES", "5")
	t.Setenv("RGX_RAGFLOW_RETRY_BACKOFF_MS", "250")
	applyEnv(cfg)
	if cfg.RAGFlow.Timeout != 45 {
		t.Fatalf("expected timeout 45, got %d", cfg.RAGFlow.Timeout)
	}
	if cfg.RAGFlow.MaxConns != 50 {
		t.Fatalf("expected max_conns 50, got %d", cfg.RAGFlow.MaxConns)
	}
	if cfg.RAGFlow.RetryMaxRetries != 5 {
		t.Fatalf("expected retry_max_retries 5, got %d", cfg.RAGFlow.RetryMaxRetries)
	}
	if cfg.RAGFlow.RetryBackoffMs != 250 {
		t.Fatalf("expected retry_backoff_ms 250, got %d", cfg.RAGFlow.RetryBackoffMs)
	}
}

func TestApplyEnv_DatabasePrivilegeSeparation(t *testing.T) {
	cfg := defaults()
	t.Setenv("RGX_DB_MIGRATION_USER", "ragflow_x_migration")
	t.Setenv("RGX_DB_MIGRATION_PASSWORD", "migration-secret")
	t.Setenv("RGX_DB_ENFORCE_RUNTIME_PRIVILEGES", "true")
	applyEnv(cfg)
	if cfg.Database.MigrationUser != "ragflow_x_migration" {
		t.Fatalf("unexpected migration user: %q", cfg.Database.MigrationUser)
	}
	if cfg.Database.MigrationPassword != "migration-secret" {
		t.Fatal("migration password override was not applied")
	}
	if !cfg.Database.EnforcePrivileges {
		t.Fatal("runtime privilege enforcement override was not applied")
	}
}

func TestApplyEnv_OIDCOverrides(t *testing.T) {
	cfg := defaults()
	t.Setenv("RGX_OIDC_ENABLED", "true")
	t.Setenv("RGX_OIDC_ISSUER", "https://sso.example.internal/realms/enterprise")
	t.Setenv("RGX_OIDC_TENANT_ID", "tenant-1")
	t.Setenv("RGX_OIDC_CLIENT_ID", "ragflow-x")
	t.Setenv("RGX_OIDC_CLIENT_SECRET", "sso-secret")
	t.Setenv("RGX_OIDC_REDIRECT_URL", "https://app.example.com/api/v1/auth/oidc/callback")
	t.Setenv("RGX_OIDC_ALLOWED_EMAIL_DOMAINS", "example.com,partner.example.com")
	t.Setenv("RGX_OIDC_REQUIRE_EMAIL_VERIFIED", "false")
	t.Setenv("RGX_OIDC_POST_LOGIN_PATH", "/workbench")
	t.Setenv("RGX_OIDC_HTTP_TIMEOUT_SEC", "15")
	applyEnv(cfg)
	if !cfg.OIDC.Enabled || cfg.OIDC.Issuer == "" || cfg.OIDC.TenantID != "tenant-1" ||
		cfg.OIDC.ClientID != "ragflow-x" || cfg.OIDC.ClientSecret != "sso-secret" {
		t.Fatalf("OIDC connection overrides were not applied: %+v", cfg.OIDC)
	}
	if len(cfg.OIDC.AllowedEmailDomains) != 2 || cfg.OIDC.PostLoginPath != "/workbench" ||
		cfg.OIDC.RequireEmailVerified || cfg.OIDC.HTTPTimeoutSec != 15 {
		t.Fatalf("unexpected OIDC policy overrides: %+v", cfg.OIDC)
	}
}
