// Package deployment implements repeatable deployment-readiness drills.
package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"gorm.io/gorm"
)

const checkTimeout = 10 * time.Second

// CheckResult is one machine-readable deployment drill outcome.
type CheckResult struct {
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	DurationMS int64          `json:"duration_ms"`
	Error      string         `json:"error,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

// DrillReport is the complete, repeatable deployment drill artifact.
type DrillReport struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Overall     string         `json:"overall"`
	TimeoutMS   int64          `json:"timeout_ms"`
	Summary     DrillSummary   `json:"summary"`
	Checks      []CheckResult  `json:"checks"`
	Options     map[string]any `json:"options,omitempty"`
}

// DrillSummary aggregates check outcomes.
type DrillSummary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Options controls which checks run. Nil slices and empty paths are skipped.
type Options struct {
	Config            config.Config
	BaseURL           string
	Timeout           time.Duration
	DB                *gorm.DB
	Provider          ragflow.Client
	AuditTenantID     string
	AuditBackupPath   string
	AuditManifestPath string
	LatestVersion     int64
	SecurityBaseline  bool
	HTTPClient        *http.Client
	Now               func() time.Time
}

// Run executes every configured deployment drill check. A returned error means
// the report could not be produced; individual check failures stay in Report.
func Run(ctx context.Context, options Options) (*DrillReport, error) {
	nowFn := options.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	report := &DrillReport{
		GeneratedAt: nowFn().UTC(),
		TimeoutMS:   options.Timeout.Milliseconds(),
		Options: map[string]any{
			"base_url":             strings.TrimRight(options.BaseURL, "/"),
			"database_driver":      options.Config.Database.Driver,
			"audit_tenant_id":      options.AuditTenantID,
			"audit_backup_enabled": options.AuditBackupPath != "",
			"latest_version":       options.LatestVersion,
			"security_baseline":    options.SecurityBaseline,
		},
	}

	report.add(runCheck(ctx, "configuration", checkConfig(options.Config)))
	if options.DB != nil {
		report.add(runCheck(ctx, "database_connectivity", checkDatabase(ctx, options.DB)))
		report.add(runCheck(ctx, "schema_version", checkSchemaVersion(options.DB, options.LatestVersion)))
	}
	if options.BaseURL != "" {
		report.add(runCheck(ctx, "http_health", checkHTTPHealth(ctx, options.HTTPClient, options.BaseURL)))
		if options.SecurityBaseline {
			report.add(runCheck(ctx, "security_baseline", checkSecurityBaseline(ctx, options.Config, options.HTTPClient, options.BaseURL)))
		}
	}
	if options.DB != nil {
		report.add(runCheck(ctx, "dependency_health", checkDependencies(ctx, options)))
		report.add(runCheck(ctx, "audit_chain", checkAuditChain(ctx, options.DB, options.AuditTenantID)))
	}
	if options.AuditBackupPath != "" {
		report.add(runCheck(ctx, "audit_backup_manifest", checkAuditBackup(options.AuditBackupPath, options.AuditManifestPath)))
	} else {
		report.addSkipped("audit_backup_manifest", "no --audit-backup supplied")
	}
	report.finish()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}

func runCheck(ctx context.Context, name string, check func(context.Context) (map[string]any, error)) CheckResult {
	start := time.Now()
	details, err := check(ctx)
	result := CheckResult{
		Name:       name,
		DurationMS: time.Since(start).Milliseconds(),
		Details:    details,
	}
	if ctx.Err() != nil {
		err = fmt.Errorf("check %s: %w", name, ctx.Err())
	}
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	} else {
		result.Status = "passed"
	}
	return result
}

func checkConfig(cfg config.Config) func(context.Context) (map[string]any, error) {
	return func(context.Context) (map[string]any, error) {
		problems := make([]string, 0)
		if cfg.Server.Mode != "release" {
			problems = append(problems, "deployment drill requires server.mode=release")
		}
		if strings.TrimSpace(cfg.App.Name) == "" {
			problems = append(problems, "app.name is required")
		}
		if cfg.App.JWTSecret != "" && len(cfg.App.JWTSecret) < 32 {
			problems = append(problems, "app.jwt_secret must be at least 32 bytes")
		}
		if strings.EqualFold(cfg.App.JWTSecret, "change-me") || strings.EqualFold(cfg.App.JWTSecret, "secret") {
			problems = append(problems, "app.jwt_secret uses a known placeholder")
		}
		if cfg.Database.Driver != "postgres" {
			problems = append(problems, "deployment drill requires database.driver=postgres")
		}
		if strings.TrimSpace(cfg.Database.Name) == "" {
			problems = append(problems, "database.name is required")
		}
		if cfg.Database.MaxOpenConns <= 0 {
			problems = append(problems, "database.max_open_conns must be positive")
		}
		if cfg.RAGFlow.Provider != "http" {
			problems = append(problems, "deployment drill requires ragflow.provider=http")
		}
		if strings.TrimSpace(cfg.RAGFlow.BaseURL) == "" {
			problems = append(problems, "ragflow.base_url is required")
		}
		if cfg.Security.MaxRequestBytes <= 0 {
			problems = append(problems, "security.max_request_bytes must be positive")
		}
		if len(problems) > 0 {
			return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
		}
		return map[string]any{
			"server_mode":       cfg.Server.Mode,
			"database_driver":   cfg.Database.Driver,
			"database_host":     cfg.Database.Host,
			"database_sslmode":  cfg.Database.SSLMode,
			"ragflow_provider":  cfg.RAGFlow.Provider,
			"jwt_secret_pinned": cfg.App.JWTSecret != "",
		}, nil
	}
}

func checkDatabase(ctx context.Context, database *gorm.DB) func(context.Context) (map[string]any, error) {
	return func(context.Context) (map[string]any, error) {
		sqlDB, err := database.DB()
		if err != nil {
			return nil, fmt.Errorf("get database handle: %w", err)
		}
		pingCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		defer cancel()
		if err := sqlDB.PingContext(pingCtx); err != nil {
			return nil, fmt.Errorf("database ping: %w", err)
		}
		stats := sqlDB.Stats()
		return map[string]any{
			"open_connections": stats.OpenConnections,
			"in_use":           stats.InUse,
			"idle":             stats.Idle,
			"max_open":         stats.MaxOpenConnections,
		}, nil
	}
}

func checkSchemaVersion(database *gorm.DB, expected int64) func(context.Context) (map[string]any, error) {
	return func(context.Context) (map[string]any, error) {
		if expected <= 0 {
			expected = db.LatestMigrationVersion()
		}
		var applied []model.SchemaVersion
		if err := database.Order("version ASC").Find(&applied).Error; err != nil {
			return nil, fmt.Errorf("query schema versions: %w", err)
		}
		if len(applied) == 0 {
			return nil, fmt.Errorf("schema version table is empty")
		}
		versions := make([]int64, 0, len(applied))
		for _, version := range applied {
			versions = append(versions, version.Version)
		}
		missing := make([]int64, 0)
		for version := int64(1); version <= expected; version++ {
			if !containsVersion(versions, version) {
				missing = append(missing, version)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("missing migrations: %v", missing)
		}
		latest := applied[len(applied)-1].Version
		if latest != expected {
			return nil, fmt.Errorf("latest migration %d does not match expected %d", latest, expected)
		}
		return map[string]any{
			"latest": latest,
			"count":  len(applied),
		}, nil
	}
}

func containsVersion(values []int64, expected int64) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func checkHTTPHealth(ctx context.Context, client *http.Client, baseURL string) func(context.Context) (map[string]any, error) {
	return func(context.Context) (map[string]any, error) {
		if client == nil {
			client = http.DefaultClient
		}
		endpoints := []string{"/api/v1/healthz", "/api/v1/readyz"}
		details := make(map[string]any)
		for _, endpoint := range endpoints {
			requestCtx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+endpoint, nil)
			if err != nil {
				return nil, fmt.Errorf("build %s request: %w", endpoint, err)
			}
			response, err := client.Do(request)
			if err != nil {
				return nil, fmt.Errorf("%s request: %w", endpoint, err)
			}
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			status := strconv.Itoa(response.StatusCode)
			details[endpoint] = map[string]any{
				"status_code": status,
				"body":        strings.TrimSpace(string(body)),
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return nil, fmt.Errorf("%s returned %s", endpoint, status)
			}
		}
		return details, nil
	}
}

// checkSecurityBaseline performs the pre-production security regression. It
// combines deny-by-default configuration checks with three runtime probes:
// hardened headers, Secure/HttpOnly auth cookies behind TLS termination, and
// CSRF rejection for an ambient cookie sent cross-site.
func checkSecurityBaseline(ctx context.Context, cfg config.Config, client *http.Client, baseURL string) func(context.Context) (map[string]any, error) {
	return func(ctx context.Context) (map[string]any, error) {
		problems := make([]string, 0)
		if cfg.App.JWTSecret == "" || len(cfg.App.JWTSecret) < 32 {
			problems = append(problems, "app.jwt_secret must be pinned with at least 32 bytes")
		}
		switch cfg.Database.SSLMode {
		case "require", "verify-ca", "verify-full":
		default:
			problems = append(problems, "database.sslmode must be require, verify-ca, or verify-full")
		}
		if strings.EqualFold(cfg.Logging.Level, "debug") {
			problems = append(problems, "logging.level must not be debug in production")
		}
		if cfg.Security.MaxRequestBytes <= 0 || cfg.Security.MaxRequestBytes > 25<<20 {
			problems = append(problems, "security.max_request_bytes must be between 1 byte and 25 MiB")
		}
		if !strings.EqualFold(cfg.Security.FrameOptions, "DENY") {
			problems = append(problems, "security.frame_options must be DENY")
		}
		if strings.TrimSpace(cfg.Security.ContentSecurityPolicy) == "" {
			problems = append(problems, "security.content_security_policy is required")
		}
		lowerCSP := strings.ToLower(cfg.Security.ContentSecurityPolicy)
		if strings.Contains(lowerCSP, "unsafe-inline") || strings.Contains(lowerCSP, "unsafe-eval") {
			problems = append(problems, "security.content_security_policy must not allow unsafe-inline or unsafe-eval")
		}
		if !cfg.Security.HSTSEnabled || cfg.Security.HSTSMaxAgeSec < 31536000 {
			problems = append(problems, "security.hsts must be enabled for at least 31536000 seconds")
		}
		for _, origin := range cfg.Security.AllowedOrigins {
			if strings.EqualFold(strings.TrimSpace(origin), "*") {
				problems = append(problems, "security.allowed_origins must not contain a wildcard")
				continue
			}
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
				problems = append(problems, "security.allowed_origins entries must be absolute HTTPS origins")
			}
		}
		if cfg.Observability.MetricsEnabled && cfg.Observability.MetricsToken == "" && len(cfg.Observability.MetricsAllowCIDRs) == 0 {
			problems = append(problems, "metrics endpoint requires a token or explicit CIDR allowlist")
		}
		if cfg.Setup.RateLimitPerMin <= 0 {
			problems = append(problems, "setup.rate_limit_per_min must be positive")
		}
		if cfg.Gateway.RateLimitPerMin <= 0 {
			problems = append(problems, "gateway.rate_limit_per_min must be positive")
		}
		if !cfg.AuditAnchor.Enabled || strings.TrimSpace(cfg.AuditAnchor.ExportDir) == "" {
			problems = append(problems, "audit_anchor must be enabled with a WORM export_dir")
		}
		if cfg.Approval.NotifyWebhook != "" && cfg.Approval.NotifyWebhookSecret == "" {
			problems = append(problems, "approval.notify_webhook requires an HMAC secret")
		}
		if len(problems) > 0 {
			return map[string]any{"configuration": false}, fmt.Errorf("%s", strings.Join(problems, "; "))
		}

		if client == nil {
			client = http.DefaultClient
		}
		root := strings.TrimRight(baseURL, "/")
		headers, err := securityProbe(ctx, client, http.MethodGet, root+"/api/v1/healthz", nil, "", "")
		if err != nil {
			return nil, err
		}
		if headers.headers.Get("X-Content-Type-Options") != "nosniff" {
			problems = append(problems, "health response is missing X-Content-Type-Options: nosniff")
		}
		if !strings.EqualFold(headers.headers.Get("X-Frame-Options"), cfg.Security.FrameOptions) {
			problems = append(problems, "health response X-Frame-Options does not match configuration")
		}
		if headers.headers.Get("Referrer-Policy") != "no-referrer" {
			problems = append(problems, "health response is missing Referrer-Policy: no-referrer")
		}
		if headers.headers.Get("Content-Security-Policy") == "" {
			problems = append(problems, "health response is missing Content-Security-Policy")
		}
		if cfg.Security.HSTSEnabled && headers.headers.Get("Strict-Transport-Security") == "" {
			problems = append(problems, "health response is missing Strict-Transport-Security")
		}

		cookieProbeHeaders, err := securityProbe(ctx, client, http.MethodPost, root+"/api/v1/auth/logout", nil, "", "https")
		if err != nil {
			return nil, err
		}
		cookies := cookieProbeHeaders.headers.Values("Set-Cookie")
		required := map[string]bool{"rgx_access": false, "rgx_refresh": false}
		for _, raw := range cookies {
			cookie, parseErr := http.ParseSetCookie(raw)
			if parseErr != nil {
				continue
			}
			if _, ok := required[cookie.Name]; ok {
				required[cookie.Name] = true
			}
			if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				problems = append(problems, "authentication cookies must be Secure, HttpOnly, and SameSite=Lax")
				break
			}
		}
		for name, seen := range required {
			if !seen {
				problems = append(problems, "authentication response is missing cookie "+name)
			}
		}

		csrfRequestHeaders := map[string]string{"Origin": "https://cross-site.example", "Cookie": "rgx_access=drill-probe"}
		csrfHeaders, err := securityProbe(ctx, client, http.MethodGet, root+"/api/v1/auth/me", csrfRequestHeaders, "", "")
		if err != nil {
			return nil, err
		}
		if csrfHeaders.status != http.StatusForbidden {
			problems = append(problems, fmt.Sprintf("cross-site ambient cookie returned %d, want 403", csrfHeaders.status))
		}

		unauthHeaders, err := securityProbe(ctx, client, http.MethodGet, root+"/api/v1/tenants", nil, "", "")
		if err != nil {
			return nil, err
		}
		if unauthHeaders.status != http.StatusUnauthorized {
			problems = append(problems, fmt.Sprintf("unauthenticated protected endpoint returned %d, want 401", unauthHeaders.status))
		}
		if len(problems) > 0 {
			return map[string]any{"runtime": false}, fmt.Errorf("%s", strings.Join(problems, "; "))
		}
		originMode := "same-origin"
		if len(cfg.Security.AllowedOrigins) > 0 {
			originMode = "explicit-https"
		}
		return map[string]any{
			"cors_origin_mode":           originMode,
			"database_sslmode":           cfg.Database.SSLMode,
			"hsts":                       cfg.Security.HSTSEnabled,
			"provider_private_exception": cfg.Security.AllowPrivateProviderBaseURL,
			"audit_anchor_export":        true,
		}, nil
	}
}

type securityProbeResponse struct {
	headers http.Header
	status  int
}

func securityProbe(ctx context.Context, client *http.Client, method, endpoint string, headers map[string]string, body, forwardedProto string) (securityProbeResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, reader)
	if err != nil {
		return securityProbeResponse{}, fmt.Errorf("build security probe %s: %w", endpoint, err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if forwardedProto != "" {
		request.Header.Set("X-Forwarded-Proto", forwardedProto)
	}
	response, err := client.Do(request)
	if err != nil {
		return securityProbeResponse{}, fmt.Errorf("security probe %s: %w", endpoint, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	return securityProbeResponse{headers: response.Header, status: response.StatusCode}, nil
}

func checkDependencies(ctx context.Context, options Options) func(context.Context) (map[string]any, error) {
	return func(context.Context) (map[string]any, error) {
		provider := options.Provider
		if provider == nil {
			var err error
			provider, err = newRAGFlowProvider(options.Config.RAGFlow)
			if err != nil {
				return nil, err
			}
		}
		healthCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		defer cancel()
		health, err := provider.Health(healthCtx)
		if err != nil {
			return nil, fmt.Errorf("ragflow health: %w", err)
		}
		return map[string]any{
			"provider":   provider.Name(),
			"status":     health.Status,
			"db":         health.DB,
			"redis":      health.Redis,
			"doc_engine": health.DocEngine,
		}, nil
	}
}

func newRAGFlowProvider(cfg config.RAGFlow) (ragflow.Client, error) {
	switch cfg.Provider {
	case "http":
		return ragflow.NewHTTPClient(cfg.BaseURL, cfg.APIKey, time.Duration(cfg.Timeout)*time.Second, cfg.MaxConns), nil
	case "mock":
		return ragflow.NewMock(), nil
	default:
		return nil, fmt.Errorf("unsupported ragflow provider %q", cfg.Provider)
	}
}

func checkAuditChain(ctx context.Context, database *gorm.DB, tenantID string) func(context.Context) (map[string]any, error) {
	return func(context.Context) (map[string]any, error) {
		tenantIDs := []string{}
		if strings.TrimSpace(tenantID) != "" {
			var count int64
			if err := database.WithContext(ctx).Model(&model.Tenant{}).Where("id = ?", tenantID).Count(&count).Error; err != nil {
				return nil, fmt.Errorf("query tenant %s: %w", tenantID, err)
			}
			if count == 0 {
				return nil, fmt.Errorf("audit tenant %s does not exist", tenantID)
			}
			tenantIDs = append(tenantIDs, tenantID)
		} else {
			var tenants []model.Tenant
			if err := database.WithContext(ctx).Select("id").Find(&tenants).Error; err != nil {
				return nil, fmt.Errorf("query tenants: %w", err)
			}
			for _, tenant := range tenants {
				tenantIDs = append(tenantIDs, tenant.ID)
			}
		}
		if len(tenantIDs) == 0 {
			return nil, fmt.Errorf("no tenants found for audit chain validation")
		}

		totalRecords := 0
		for _, id := range tenantIDs {
			var records []model.AuditLog
			if err := database.WithContext(ctx).
				Where("tenant_id = ?", id).
				Order("seq ASC").
				Find(&records).Error; err != nil {
				return nil, fmt.Errorf("query audit records for tenant %s: %w", id, err)
			}
			previousHash := ""
			for index, record := range records {
				if record.Seq != int64(index+1) || record.PrevHash != previousHash || record.Hash != model.AuditHash(previousHash, &record) {
					return nil, fmt.Errorf("audit chain is invalid at tenant %s seq %d", id, record.Seq)
				}
				previousHash = record.Hash
			}
			totalRecords += len(records)
		}
		sort.Strings(tenantIDs)
		return map[string]any{
			"tenants_checked": len(tenantIDs),
			"records_checked": totalRecords,
		}, nil
	}
}

func checkAuditBackup(backupPath, manifestPath string) func(context.Context) (map[string]any, error) {
	return func(context.Context) (map[string]any, error) {
		if manifestPath == "" {
			manifestPath = backupPath + ".sha256"
		}
		backup, err := os.Stat(backupPath)
		if err != nil {
			return nil, fmt.Errorf("stat audit backup: %w", err)
		}
		if backup.IsDir() || backup.Size() == 0 {
			return nil, fmt.Errorf("audit backup is a directory or empty")
		}
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read audit manifest: %w", err)
		}
		values, err := parseAuditManifest(string(manifestData))
		if err != nil {
			return nil, err
		}
		file, err := os.Open(backupPath)
		if err != nil {
			return nil, fmt.Errorf("open audit backup: %w", err)
		}
		defer file.Close()
		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			return nil, fmt.Errorf("hash audit backup: %w", err)
		}
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		expectedHash := values["sha256"]
		expectedSize, err := strconv.ParseInt(values["size"], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("manifest size is invalid: %w", err)
		}
		if actualHash != expectedHash {
			return nil, fmt.Errorf("audit backup hash mismatch: expected %s, actual %s", expectedHash, actualHash)
		}
		if expectedSize != backup.Size() {
			return nil, fmt.Errorf("audit backup size mismatch: expected %d, actual %d", expectedSize, backup.Size())
		}
		if _, err := time.Parse("20060102T150405Z", values["timestamp"]); err != nil {
			return nil, fmt.Errorf("manifest timestamp is invalid: %w", err)
		}
		return map[string]any{
			"manifest":       filepath.ToSlash(manifestPath),
			"sha256":         actualHash,
			"size":           backup.Size(),
			"timestamp":      values["timestamp"],
			"manifest_valid": true,
		}, nil
	}
}

func parseAuditManifest(manifest string) (map[string]string, error) {
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		line = strings.TrimSpace(line)
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("invalid audit manifest line %q", line)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("audit manifest has duplicate key %q", key)
		}
		values[key] = value
	}
	for _, key := range []string{"sha256", "size", "timestamp"} {
		if strings.TrimSpace(values[key]) == "" {
			return nil, fmt.Errorf("audit manifest is missing %s", key)
		}
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("audit manifest must contain exactly sha256, size, and timestamp")
	}
	if _, err := hex.DecodeString(values["sha256"]); err != nil || len(values["sha256"]) != sha256.Size*2 {
		return nil, fmt.Errorf("audit manifest sha256 is invalid")
	}
	return values, nil
}

func (report *DrillReport) add(result CheckResult) {
	report.Checks = append(report.Checks, result)
}

func (report *DrillReport) addSkipped(name, reason string) {
	report.add(CheckResult{
		Name:    name,
		Status:  "skipped",
		Details: map[string]any{"reason": reason},
	})
}

func (report *DrillReport) finish() {
	report.Summary.Total = len(report.Checks)
	for _, result := range report.Checks {
		switch result.Status {
		case "passed":
			report.Summary.Passed++
		case "failed":
			report.Summary.Failed++
		default:
			report.Summary.Skipped++
		}
	}
	report.Overall = "passed"
	if report.Summary.Failed > 0 {
		report.Overall = "failed"
	}
}

// WriteJSON returns the canonical machine-readable report.
func (report *DrillReport) WriteJSON() ([]byte, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode drill report: %w", err)
	}
	return append(data, '\n'), nil
}

// RenderMarkdown returns a concise human-readable review artifact.
func (report *DrillReport) RenderMarkdown() ([]byte, error) {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Deployment Drill Report\n\n")
	fmt.Fprintf(&builder, "- Generated At: %s\n", report.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&builder, "- Overall: %s\n", report.Overall)
	fmt.Fprintf(&builder, "- Timeout: %dms\n", report.TimeoutMS)
	fmt.Fprintf(&builder, "- Summary: %d total, %d passed, %d failed, %d skipped\n\n", report.Summary.Total, report.Summary.Passed, report.Summary.Failed, report.Summary.Skipped)
	fmt.Fprintf(&builder, "| Check | Status | Duration (ms) | Error | Details |\n|---|---|---:|---|---|\n")
	for _, result := range report.Checks {
		details := ""
		if len(result.Details) > 0 {
			data, err := json.Marshal(result.Details)
			if err != nil {
				return nil, err
			}
			details = strings.ReplaceAll(strings.ReplaceAll(string(data), "|", "\\|"), "\n", " ")
		}
		fmt.Fprintf(&builder, "| %s | %s | %d | %s | %s |\n", result.Name, result.Status, result.DurationMS, result.Error, details)
	}
	return []byte(builder.String()), nil
}
