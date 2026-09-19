package config

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root application configuration.
type Config struct {
	Server              Server              `yaml:"server"`
	App                 App                 `yaml:"app"`
	Database            Database            `yaml:"database"`
	RAGFlow             RAGFlow             `yaml:"ragflow"`
	Logging             Logging             `yaml:"logging"`
	Setup               Setup               `yaml:"setup"`
	Gateway             Gateway             `yaml:"gateway"`
	Redis               Redis               `yaml:"redis"`
	Security            Security            `yaml:"security"`
	Observability       Observability       `yaml:"observability"`
	Usage               Usage               `yaml:"usage"`
	Alerting            Alerting            `yaml:"alerting"`
	AuditAnchor         AuditAnchor         `yaml:"audit_anchor"`
	Retention           Retention           `yaml:"retention"`
	Approval            Approval            `yaml:"approval"`
	Runtime             Runtime             `yaml:"runtime"`
	ConversationRouting ConversationRouting `yaml:"conversation_routing"`
	OIDC                OIDC                `yaml:"oidc"`
}

// OIDC configures an enterprise authorization-code flow. Users are matched to
// accounts already provisioned in RAGFlow-X; identities are never silently
// created from an external claim.
type OIDC struct {
	Enabled              bool     `yaml:"enabled"`
	Issuer               string   `yaml:"issuer"`
	TenantID             string   `yaml:"tenant_id"`
	ClientID             string   `yaml:"client_id"`
	ClientSecret         string   `yaml:"client_secret"`
	RedirectURL          string   `yaml:"redirect_url"`
	AuthURL              string   `yaml:"auth_url"`
	TokenURL             string   `yaml:"token_url"`
	JWKSURL              string   `yaml:"jwks_url"`
	Scopes               []string `yaml:"scopes"`
	AllowedEmailDomains  []string `yaml:"allowed_email_domains"`
	RequireEmailVerified bool     `yaml:"require_email_verified"`
	PostLoginPath        string   `yaml:"post_login_path"`
	HTTPTimeoutSec       int      `yaml:"http_timeout_sec"`
}

// Runtime controls cross-instance configuration apply reporting. ReportToken
// authenticates the machine-to-machine report endpoint; HeartbeatTimeoutSec
// marks instances that have stopped reporting as stale.
type Runtime struct {
	ReportToken         string `yaml:"report_token"`
	HeartbeatTimeoutSec int    `yaml:"heartbeat_timeout_sec"`
}

// Logging holds structured logger rotation and retention settings.
type Logging struct {
	Level          string `yaml:"level"`
	Format         string `yaml:"format"`
	OutputPath     string `yaml:"output"`
	RotateDaily    bool   `yaml:"rotate_daily"`
	RotationSizeMB int64  `yaml:"max_size_mb"`
	RotationCount  uint   `yaml:"max_backups"`
	RetentionDays  int    `yaml:"retention_days"`
}

// Setup holds first-run configuration persistence settings. Secrets are kept
// out of YAML/deployment config and are stored encrypted at rest by the
// bootstrap wizard.
type Setup struct {
	Enabled         bool   `yaml:"enabled"`
	SecretsFile     string `yaml:"secrets_file"`
	MasterKey       string `yaml:"master_key"`
	RSAKeyPath      string `yaml:"rsa_key_path"`
	AdminUser       string `yaml:"admin_user"`
	AdminPass       string `yaml:"admin_pass"`
	RateLimitPerMin int    `yaml:"rate_limit_per_min"`
}

// Gateway holds outbound LLM request timeouts. Streaming responses can be
// long-lived, so they use separate timeouts from completed (non-streaming)
// responses.
type Gateway struct {
	NonStreamTimeoutSec    int `yaml:"non_stream_timeout_sec"`
	StreamHeaderTimeoutSec int `yaml:"stream_header_timeout_sec"`
	StreamTimeoutSec       int `yaml:"stream_timeout_sec"`
	MaxConns               int `yaml:"max_conns"`
	RateLimitPerMin        int `yaml:"rate_limit_per_min"`
}

// Redis is an optional distributed rate-limit/cache backend. When disabled,
// the process falls back to in-memory limiting (single instance only).
type Redis struct {
	Enabled  bool   `yaml:"enabled"`
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

// Server holds HTTP server settings.
type Server struct {
	Port         int    `yaml:"port"`
	Mode         string `yaml:"mode"`
	ReadTimeout  int    `yaml:"read_timeout"`
	WriteTimeout int    `yaml:"write_timeout"`
}

// Security holds CORS and HTTP security-header settings. By default CORS is
// disabled (same-origin only) and hardened response headers are applied.
type Security struct {
	// CORS. Empty AllowedOrigins disables cross-origin CORS entirely.
	AllowedOrigins   []string `yaml:"allowed_origins"`
	AllowedMethods   []string `yaml:"allowed_methods"`
	AllowedHeaders   []string `yaml:"allowed_headers"`
	AllowCredentials bool     `yaml:"allow_credentials"`
	MaxAgeSec        int      `yaml:"max_age_sec"`
	// Security headers.
	HSTSEnabled                 bool   `yaml:"hsts_enabled"`
	HSTSMaxAgeSec               int    `yaml:"hsts_max_age_sec"`
	HSTSIncludeSubdomains       bool   `yaml:"hsts_include_subdomains"`
	ContentSecurityPolicy       string `yaml:"content_security_policy"`
	FrameOptions                string `yaml:"frame_options"`
	MaxRequestBytes             int64  `yaml:"max_request_bytes"`
	AllowPrivateProviderBaseURL bool   `yaml:"allow_private_provider_base_url"`
}

// Observability controls Prometheus metrics and OpenTelemetry tracing. Metrics
// are on by default; tracing is opt-in and requires an OTLP collector endpoint.
type Observability struct {
	MetricsEnabled    bool     `yaml:"metrics_enabled"`
	MetricsPath       string   `yaml:"metrics_path"`
	MetricsNamespace  string   `yaml:"metrics_namespace"`
	MetricsToken      string   `yaml:"metrics_token"`
	MetricsAllowCIDRs []string `yaml:"metrics_allow_cidrs"`
	TracingEnabled    bool     `yaml:"tracing_enabled"`
	OTLPEndpoint      string   `yaml:"otel_endpoint"`
	ServiceName       string   `yaml:"service_name"`
	SampleRatio       float64  `yaml:"sample_ratio"`
}

// Usage controls usage-cost attribution. Estimated cost is derived from
// metered tokens at report time and is a governance metric, not financial
// settlement data.
type Usage struct {
	CostPer1KTokens float64 `yaml:"cost_per_1k_tokens"`
	Currency        string  `yaml:"currency"`
}

// Alerting configures outbound alert notifications (webhook for now).
type Alerting struct {
	Enabled                     bool      `yaml:"enabled"`
	Webhooks                    []Webhook `yaml:"webhooks"`
	Email                       Email     `yaml:"email"`
	ThrottleSec                 int       `yaml:"throttle_sec"`
	CompensationIntervalSec     int       `yaml:"compensation_interval_sec"`
	CompensationMaxAttempts     int       `yaml:"compensation_max_attempts"`
	CompensationInitialDelaySec int       `yaml:"compensation_initial_delay_sec"`
	CompensationMaxDelaySec     int       `yaml:"compensation_max_delay_sec"`
	CompensationLeaseSec        int       `yaml:"compensation_lease_sec"`
	CompensationJitterPercent   int       `yaml:"compensation_jitter_percent"`
}

const (
	DefaultWebhookTimeoutSec       = 5
	DefaultWebhookMaxAttempts      = 3
	DefaultWebhookBackoffInitialMs = 250
	DefaultWebhookBackoffMaxMs     = 2000
)

// Webhook is a single outbound alert endpoint (optionally HMAC-signed).
type Webhook struct {
	Name             string `yaml:"name"`
	Type             string `yaml:"type"`
	URL              string `yaml:"url"`
	Secret           string `yaml:"secret"`
	Enabled          bool   `yaml:"enabled"`
	TimeoutSec       int    `yaml:"timeout_sec"`
	MaxAttempts      int    `yaml:"max_attempts"`
	BackoffInitialMs int    `yaml:"backoff_initial_ms"`
	BackoffMaxMs     int    `yaml:"backoff_max_ms"`
}

// Email is the first SMTP channel for approval, alerting and governance
// events. The configured host must expose STARTTLS on the selected port.
type Email struct {
	Enabled          bool   `yaml:"enabled"`
	Host             string `yaml:"host"`
	Port             int    `yaml:"port"`
	Username         string `yaml:"username"`
	Password         string `yaml:"password"`
	From             string `yaml:"from"`
	To               string `yaml:"to"`
	TimeoutSec       int    `yaml:"timeout_sec"`
	MaxAttempts      int    `yaml:"max_attempts"`
	BackoffInitialMs int    `yaml:"backoff_initial_ms"`
	BackoffMaxMs     int    `yaml:"backoff_max_ms"`
}

// Retention configures the automatic expiry of operational data (A5 data
// retention policy; doc/07 §7 / doc/33 Sprint P1-2). Each class has its own
// TTL in days so operators can, for example, keep audit longer than usage
// detail. Disabled (default) performs no deletion, and toggling it off stops
// further purges ("配置启停可控"). Purging is ledged in the audit log with the
// per-class deleted counts, and the audit hash chain is re-anchored after an
// audit purge so the surviving chain stays verifiable.
type Retention struct {
	Enabled      bool `yaml:"enabled"`
	AuditDays    int  `yaml:"audit_days"`
	UsageDays    int  `yaml:"usage_days"`
	FeedbackDays int  `yaml:"feedback_days"`
	JobDays      int  `yaml:"job_days"`

	// IntervalSec is how often the retention janitor runs. Non-positive values
	// fall back to a 24h cadence.
	IntervalSec int `yaml:"interval_sec"`
}

type Approval struct {
	Enabled               bool             `yaml:"enabled"`
	DefaultExpireHours    int              `yaml:"default_expire_hours"`
	ExecutionMaxRetries   int              `yaml:"execution_max_retries"`
	ExpireScanIntervalSec int              `yaml:"expire_scan_interval_sec"`
	ReminderBeforeHours   int              `yaml:"reminder_before_hours"`
	RetentionDays         int              `yaml:"retention_days"`
	PolicyCacheTTLSec     int              `yaml:"policy_cache_ttl_sec"`
	NotifyWebhook         string           `yaml:"notify_webhook"`
	NotifyWebhookSecret   string           `yaml:"notify_webhook_secret"`
	Policies              []ApprovalPolicy `yaml:"policies"`
}

// AuditAnchor configures periodic immutable snapshots of each tenant's audit
// hash-chain tail. Anchors make deletion or tampering of local audit rows
// detectable even after retention purges.
type AuditAnchor struct {
	Enabled     bool `yaml:"enabled"`
	IntervalSec int  `yaml:"interval_sec"`
	// ExportDir is a local path backed by WORM/object storage. One immutable
	// JSON file is written per anchor; empty disables external copy.
	ExportDir string `yaml:"export_dir"`
}

type ConversationRouting struct {
	Mode                string      `yaml:"mode"`
	ConfidenceThreshold float64     `yaml:"confidence_threshold"`
	ConfidenceMargin    float64     `yaml:"confidence_margin"`
	MinimumCandidates   int         `yaml:"minimum_candidates"`
	ReadinessThreshold  float64     `yaml:"readiness_threshold"`
	TotalTimeoutMs      int         `yaml:"total_timeout_ms"`
	CatalogTTLSec       int         `yaml:"catalog_ttl_sec"`
	CandidateLimit      int         `yaml:"candidate_limit"`
	ResponseTopK        int         `yaml:"response_top_k"`
	Rerank              RouteRerank `yaml:"rerank"`
	Gate                RouteGate   `yaml:"gate"`
}

type RouteGate struct {
	Pilot      RouteEvidenceThreshold `yaml:"pilot"`
	Production RouteEvidenceThreshold `yaml:"production"`
}

type RouteEvidenceThreshold struct {
	MinHighConfidence int     `yaml:"min_high_confidence_samples"`
	MinWilsonLower    float64 `yaml:"min_wilson_95_lower_bound"`
	MaxECE            float64 `yaml:"max_ece"`
}

type RouteRerank struct {
	Enabled               bool    `yaml:"enabled"`
	ProviderName          string  `yaml:"provider_name"`
	InstanceName          string  `yaml:"instance_name"`
	ModelName             string  `yaml:"model_name"`
	TimeoutMs             int     `yaml:"timeout_ms"`
	TopK                  int     `yaml:"top_k"`
	MinBaseScore          float64 `yaml:"min_base_score"`
	DistributionValidated bool    `yaml:"distribution_validated"`
}

type ApprovalPolicy struct {
	TenantID    string     `yaml:"tenant_id"`
	ObjectType  string     `yaml:"object_type"`
	Action      string     `yaml:"action"`
	Enabled     bool       `yaml:"enabled"`
	Priority    int        `yaml:"priority"`
	Conditions  string     `yaml:"conditions"`
	ExpireHours int        `yaml:"expire_hours"`
	Steps       []StepSpec `yaml:"steps"`
}

type StepSpec struct {
	StepNo            int            `yaml:"step_no"`
	Name              string         `yaml:"name"`
	ApproverType      string         `yaml:"approver_type"`
	ApproverValue     string         `yaml:"approver_value"`
	ExpireHours       int            `yaml:"expire_hours"`
	ApprovalMode      string         `yaml:"approval_mode"`
	Approvers         []ApproverSpec `yaml:"approvers"`
	RequiredApprovals int            `yaml:"required_approvals"`
}

type ApproverSpec struct {
	Type  string `yaml:"type"`
	Value string `yaml:"value"`
}

// App holds application-level settings.
type App struct {
	Name               string `yaml:"name"`
	JWTSecret          string `yaml:"jwt_secret"`
	JWTExpireHours     int    `yaml:"jwt_expire_hours"`
	RefreshExpireHours int    `yaml:"refresh_expire_hours"`
	LogLevel           string `yaml:"log_level"`
	RequestIDHeader    string `yaml:"request_id_header"`
	EncryptionKey      string `yaml:"encryption_key"`
	DataDir            string `yaml:"data_dir"`
}

// Database holds the operational store settings.
type Database struct {
	Driver             string `yaml:"driver"`
	Host               string `yaml:"host"`
	Port               int    `yaml:"port"`
	User               string `yaml:"user"`
	Password           string `yaml:"password"`
	Name               string `yaml:"name"`
	SSLMode            string `yaml:"sslmode"`
	DSN                string `yaml:"dsn"`
	MigrationUser      string `yaml:"migration_user"`
	MigrationPassword  string `yaml:"migration_password"`
	EnforcePrivileges  bool   `yaml:"enforce_runtime_privileges"`
	MaxOpenConns       int    `yaml:"max_open_conns"`
	MaxIdleConns       int    `yaml:"max_idle_conns"`
	ConnMaxLifetimeSec int    `yaml:"conn_max_lifetime_sec"`
	ConnMaxIdleTimeSec int    `yaml:"conn_max_idle_time_sec"`
}

// RAGFlow holds the engine integration settings.
type RAGFlow struct {
	Provider        string `yaml:"provider"`
	BaseURL         string `yaml:"base_url"`
	APIKey          string `yaml:"api_key"`
	Timeout         int    `yaml:"timeout"`
	MaxConns        int    `yaml:"max_conns"`
	AutoRegister    bool   `yaml:"auto_register"`
	TaskPollSeconds int    `yaml:"task_poll_seconds"`
	RetryMaxRetries int    `yaml:"retry_max_retries"`
	RetryBackoffMs  int    `yaml:"retry_backoff_ms"`
}

// Load reads configuration from a YAML file path and applies environment
// variable overrides. Returns a default config when no file path is given.
func Load(path string) (*Config, error) {
	cfg := defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	applyEnv(cfg)
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: Server{Port: 9191, Mode: "debug", ReadTimeout: 30, WriteTimeout: 60},
		// No hardcoded encryption key: it must come from RGX_ENCRYPTION_KEY or
		// the first-run wizard, otherwise service.New falls back to an
		// ephemeral random key (stored API keys won't survive restart).
		App: App{Name: "ragflow-x", JWTExpireHours: 1, RefreshExpireHours: 168, LogLevel: "info", RequestIDHeader: "X-Request-Id", EncryptionKey: "", DataDir: "./data"},
		Database: Database{
			Driver: "postgres", Host: "localhost", Port: 5432,
			User: "ragflow_x", Password: "change-me", Name: "ragflow_x", SSLMode: "disable",
			DSN: "file:./ragflow-x.db?cache=shared",
		},
		RAGFlow: RAGFlow{Provider: "http", BaseURL: "http://192.168.4.151:8001", Timeout: 30, MaxConns: 20, AutoRegister: true, TaskPollSeconds: 15, RetryMaxRetries: 3, RetryBackoffMs: 100},
		Logging: Logging{Level: "info", Format: "json", OutputPath: "stdout", RotateDaily: true, RotationCount: 7},
		Setup:   Setup{Enabled: true, SecretsFile: "./config/runtime.secrets.json", RSAKeyPath: "./config/rsa_setup.pem", RateLimitPerMin: 10},
		Gateway: Gateway{NonStreamTimeoutSec: 120, StreamHeaderTimeoutSec: 30, StreamTimeoutSec: 0, MaxConns: 20, RateLimitPerMin: 120},
		Redis:   Redis{Addr: "localhost:6379", DB: 0, PoolSize: 10},
		Security: Security{
			AllowedMethods:              []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
			AllowedHeaders:              []string{"Content-Type", "Authorization", "X-Request-Id"},
			AllowCredentials:            true,
			MaxAgeSec:                   600,
			HSTSMaxAgeSec:               31536000,
			HSTSIncludeSubdomains:       true,
			ContentSecurityPolicy:       "default-src 'none'",
			FrameOptions:                "DENY",
			MaxRequestBytes:             25 << 20,
			AllowPrivateProviderBaseURL: false,
		},
		Observability: Observability{
			MetricsEnabled:   true,
			MetricsPath:      "/metrics",
			MetricsNamespace: "ragflow_x",
			ServiceName:      "ragflow-x",
			SampleRatio:      1.0,
		},
		Usage: Usage{
			CostPer1KTokens: 0.0002,
			Currency:        "CNY",
		},
		Alerting: Alerting{
			Enabled: true, ThrottleSec: 60,
			CompensationIntervalSec: 60, CompensationMaxAttempts: 12,
			CompensationInitialDelaySec: 30, CompensationMaxDelaySec: 3600,
			CompensationLeaseSec: 300, CompensationJitterPercent: 20,
		},
		AuditAnchor: AuditAnchor{
			Enabled:     false,
			IntervalSec: 3600,
			ExportDir:   "",
		},
		Retention: Retention{Enabled: false, AuditDays: 365, UsageDays: 180, FeedbackDays: 180, JobDays: 90, IntervalSec: 86400},
		Approval:  Approval{DefaultExpireHours: 72, ExecutionMaxRetries: 3, ExpireScanIntervalSec: 3600, RetentionDays: 365, PolicyCacheTTLSec: 30},
		Runtime:   Runtime{ReportToken: "", HeartbeatTimeoutSec: 60},
		OIDC:      OIDC{RequireEmailVerified: true},
		ConversationRouting: ConversationRouting{
			Mode: "recommend_only", ConfidenceThreshold: 0.86, ConfidenceMargin: 0.12,
			MinimumCandidates: 2, ReadinessThreshold: 0.80, TotalTimeoutMs: 1500, CatalogTTLSec: 300,
			CandidateLimit: 5, ResponseTopK: 3,
			Rerank: RouteRerank{TimeoutMs: 1200, TopK: 5, MinBaseScore: 0.05},
			Gate: RouteGate{
				Pilot:      RouteEvidenceThreshold{MinHighConfidence: 24, MinWilsonLower: 0.85, MaxECE: 0.07},
				Production: RouteEvidenceThreshold{MinHighConfidence: 24, MinWilsonLower: 0.99, MaxECE: 0.05},
			},
		},
	}
}

func applyEnv(cfg *Config) {
	setInt(&cfg.Server.Port, "RGX_PORT")
	setStr(&cfg.Server.Mode, "RGX_MODE")
	setStr(&cfg.App.JWTSecret, "RGX_JWT_SECRET")
	setStr(&cfg.App.EncryptionKey, "RGX_ENCRYPTION_KEY")
	setInt(&cfg.App.RefreshExpireHours, "RGX_REFRESH_EXPIRE_HOURS")
	setStr(&cfg.App.DataDir, "RGX_DATA_DIR")
	setStr(&cfg.Database.Driver, "RGX_DB_DRIVER")
	setStr(&cfg.Database.DSN, "RGX_DB_DSN")
	setStr(&cfg.Database.Host, "RGX_DB_HOST")
	setInt(&cfg.Database.Port, "RGX_DB_PORT")
	setStr(&cfg.Database.User, "RGX_DB_USER")
	setStr(&cfg.Database.Password, "RGX_DB_PASSWORD")
	setStr(&cfg.Database.Name, "RGX_DB_NAME")
	setStr(&cfg.Database.MigrationUser, "RGX_DB_MIGRATION_USER")
	setStr(&cfg.Database.MigrationPassword, "RGX_DB_MIGRATION_PASSWORD")
	setBool(&cfg.Database.EnforcePrivileges, "RGX_DB_ENFORCE_RUNTIME_PRIVILEGES")
	setInt(&cfg.Database.MaxOpenConns, "RGX_DB_MAX_OPEN_CONNS")
	setInt(&cfg.Database.MaxIdleConns, "RGX_DB_MAX_IDLE_CONNS")
	setInt(&cfg.Database.ConnMaxLifetimeSec, "RGX_DB_CONN_MAX_LIFETIME_SEC")
	setInt(&cfg.Database.ConnMaxIdleTimeSec, "RGX_DB_CONN_MAX_IDLE_TIME_SEC")
	setStr(&cfg.RAGFlow.Provider, "RGX_RAGFLOW_PROVIDER")
	setStr(&cfg.RAGFlow.BaseURL, "RGX_RAGFLOW_BASE_URL")
	setStr(&cfg.RAGFlow.APIKey, "RGX_RAGFLOW_API_KEY")
	setBool(&cfg.RAGFlow.AutoRegister, "RGX_RAGFLOW_AUTO_REGISTER")
	setInt(&cfg.RAGFlow.TaskPollSeconds, "RGX_RAGFLOW_TASK_POLL_SECONDS")
	setInt(&cfg.RAGFlow.Timeout, "RGX_RAGFLOW_TIMEOUT")
	setInt(&cfg.RAGFlow.MaxConns, "RGX_RAGFLOW_MAX_CONNS")
	setInt(&cfg.RAGFlow.RetryMaxRetries, "RGX_RAGFLOW_RETRY_MAX_RETRIES")
	setInt(&cfg.RAGFlow.RetryBackoffMs, "RGX_RAGFLOW_RETRY_BACKOFF_MS")
	setInt(&cfg.Gateway.NonStreamTimeoutSec, "RGX_GATEWAY_NON_STREAM_TIMEOUT")
	setInt(&cfg.Gateway.StreamHeaderTimeoutSec, "RGX_GATEWAY_STREAM_HEADER_TIMEOUT")
	setInt(&cfg.Gateway.StreamTimeoutSec, "RGX_GATEWAY_STREAM_TIMEOUT")
	setInt(&cfg.Gateway.MaxConns, "RGX_GATEWAY_MAX_CONNS")
	setInt(&cfg.Gateway.RateLimitPerMin, "RGX_GATEWAY_RATE_LIMIT")
	setBool(&cfg.Redis.Enabled, "RGX_REDIS_ENABLED")
	setStr(&cfg.Redis.Addr, "RGX_REDIS_ADDR")
	setStr(&cfg.Redis.Username, "RGX_REDIS_USERNAME")
	setStr(&cfg.Redis.Password, "RGX_REDIS_PASSWORD")
	setInt(&cfg.Redis.DB, "RGX_REDIS_DB")
	setInt(&cfg.Redis.PoolSize, "RGX_REDIS_POOL_SIZE")
	setStr(&cfg.Logging.Level, "RGX_LOG_LEVEL")
	setStr(&cfg.Logging.Format, "RGX_LOG_FORMAT")
	setStr(&cfg.Logging.OutputPath, "RGX_LOG_OUTPUT")
	setBool(&cfg.Logging.RotateDaily, "RGX_LOG_ROTATE_DAILY")
	setInt64(&cfg.Logging.RotationSizeMB, "RGX_LOG_MAX_SIZE_MB")
	setUint(&cfg.Logging.RotationCount, "RGX_LOG_MAX_BACKUPS")
	setInt(&cfg.Logging.RetentionDays, "RGX_LOG_RETENTION_DAYS")
	setBool(&cfg.Setup.Enabled, "RGX_SETUP_ENABLED")
	setInt(&cfg.Setup.RateLimitPerMin, "RGX_SETUP_RATE_LIMIT")
	setStr(&cfg.Setup.SecretsFile, "RGX_SETUP_SECRETS_FILE")
	setStr(&cfg.Setup.MasterKey, "RGX_MASTER_KEY")
	setStr(&cfg.Setup.RSAKeyPath, "RGX_SETUP_RSA_KEY")
	setStr(&cfg.Setup.AdminUser, "RGX_ADMIN_USER")
	setStr(&cfg.Setup.AdminPass, "RGX_ADMIN_PASSWORD")
	setBool(&cfg.OIDC.Enabled, "RGX_OIDC_ENABLED")
	setStr(&cfg.OIDC.Issuer, "RGX_OIDC_ISSUER")
	setStr(&cfg.OIDC.TenantID, "RGX_OIDC_TENANT_ID")
	setStr(&cfg.OIDC.ClientID, "RGX_OIDC_CLIENT_ID")
	setStr(&cfg.OIDC.ClientSecret, "RGX_OIDC_CLIENT_SECRET")
	setStr(&cfg.OIDC.RedirectURL, "RGX_OIDC_REDIRECT_URL")
	setStr(&cfg.OIDC.AuthURL, "RGX_OIDC_AUTH_URL")
	setStr(&cfg.OIDC.TokenURL, "RGX_OIDC_TOKEN_URL")
	setStr(&cfg.OIDC.JWKSURL, "RGX_OIDC_JWKS_URL")
	setStrSlice(&cfg.OIDC.Scopes, "RGX_OIDC_SCOPES")
	setStrSlice(&cfg.OIDC.AllowedEmailDomains, "RGX_OIDC_ALLOWED_EMAIL_DOMAINS")
	setBool(&cfg.OIDC.RequireEmailVerified, "RGX_OIDC_REQUIRE_EMAIL_VERIFIED")
	setStr(&cfg.OIDC.PostLoginPath, "RGX_OIDC_POST_LOGIN_PATH")
	setInt(&cfg.OIDC.HTTPTimeoutSec, "RGX_OIDC_HTTP_TIMEOUT_SEC")
	setStrSlice(&cfg.Security.AllowedOrigins, "RGX_CORS_ALLOWED_ORIGINS")
	setBool(&cfg.Security.AllowCredentials, "RGX_SECURITY_ALLOW_CREDENTIALS")
	setInt64(&cfg.Security.MaxRequestBytes, "RGX_SECURITY_MAX_REQUEST_BYTES")
	setBool(&cfg.Security.AllowPrivateProviderBaseURL, "RGX_SECURITY_ALLOW_PRIVATE_PROVIDER_BASE_URL")
	setInt(&cfg.Security.MaxAgeSec, "RGX_SECURITY_CORS_MAX_AGE")
	setBool(&cfg.Security.HSTSEnabled, "RGX_SECURITY_HSTS_ENABLED")
	setInt(&cfg.Security.HSTSMaxAgeSec, "RGX_SECURITY_HSTS_MAX_AGE")
	setStr(&cfg.Security.ContentSecurityPolicy, "RGX_SECURITY_CSP")
	setStr(&cfg.Security.FrameOptions, "RGX_SECURITY_FRAME_OPTIONS")
	setBool(&cfg.Observability.MetricsEnabled, "RGX_METRICS_ENABLED")
	setStr(&cfg.Observability.MetricsPath, "RGX_METRICS_PATH")
	setStr(&cfg.Observability.MetricsNamespace, "RGX_METRICS_NAMESPACE")
	setStr(&cfg.Observability.MetricsToken, "RGX_METRICS_TOKEN")
	setStrSlice(&cfg.Observability.MetricsAllowCIDRs, "RGX_METRICS_ALLOW_CIDRS")
	setBool(&cfg.Observability.TracingEnabled, "RGX_TRACING_ENABLED")
	setStr(&cfg.Observability.OTLPEndpoint, "RGX_OTEL_ENDPOINT")
	setStr(&cfg.Observability.ServiceName, "RGX_OTEL_SERVICE_NAME")
	setFloat64(&cfg.Observability.SampleRatio, "RGX_OTEL_SAMPLE_RATIO")
	setFloat64(&cfg.Usage.CostPer1KTokens, "RGX_USAGE_COST_PER_1K")
	setStr(&cfg.Usage.Currency, "RGX_USAGE_CURRENCY")
	setBool(&cfg.Alerting.Enabled, "RGX_ALERTING_ENABLED")
	setInt(&cfg.Alerting.ThrottleSec, "RGX_ALERTING_THROTTLE")
	setInt(&cfg.Alerting.CompensationIntervalSec, "RGX_ALERTING_COMPENSATION_INTERVAL_SEC")
	setInt(&cfg.Alerting.CompensationMaxAttempts, "RGX_ALERTING_COMPENSATION_MAX_ATTEMPTS")
	setInt(&cfg.Alerting.CompensationInitialDelaySec, "RGX_ALERTING_COMPENSATION_INITIAL_DELAY_SEC")
	setInt(&cfg.Alerting.CompensationMaxDelaySec, "RGX_ALERTING_COMPENSATION_MAX_DELAY_SEC")
	setInt(&cfg.Alerting.CompensationLeaseSec, "RGX_ALERTING_COMPENSATION_LEASE_SEC")
	setInt(&cfg.Alerting.CompensationJitterPercent, "RGX_ALERTING_COMPENSATION_JITTER_PERCENT")
	setBool(&cfg.AuditAnchor.Enabled, "RGX_AUDIT_ANCHOR_ENABLED")
	setInt(&cfg.AuditAnchor.IntervalSec, "RGX_AUDIT_ANCHOR_INTERVAL_SEC")
	setStr(&cfg.AuditAnchor.ExportDir, "RGX_AUDIT_ANCHOR_EXPORT_DIR")
	setBool(&cfg.Retention.Enabled, "RGX_RETENTION_ENABLED")
	setInt(&cfg.Retention.AuditDays, "RGX_RETENTION_AUDIT_DAYS")
	setInt(&cfg.Retention.UsageDays, "RGX_RETENTION_USAGE_DAYS")
	setInt(&cfg.Retention.FeedbackDays, "RGX_RETENTION_FEEDBACK_DAYS")
	setInt(&cfg.Retention.JobDays, "RGX_RETENTION_JOB_DAYS")
	setInt(&cfg.Retention.IntervalSec, "RGX_RETENTION_INTERVAL_SEC")
	setBool(&cfg.Approval.Enabled, "RGX_APPROVAL_ENABLED")
	setInt(&cfg.Approval.DefaultExpireHours, "RGX_APPROVAL_DEFAULT_EXPIRE_HOURS")
	setInt(&cfg.Approval.ExecutionMaxRetries, "RGX_APPROVAL_EXECUTION_MAX_RETRIES")
	setInt(&cfg.Approval.ExpireScanIntervalSec, "RGX_APPROVAL_EXPIRE_SCAN_INTERVAL_SEC")
	setInt(&cfg.Approval.ReminderBeforeHours, "RGX_APPROVAL_REMINDER_BEFORE_HOURS")
	setInt(&cfg.Approval.RetentionDays, "RGX_APPROVAL_RETENTION_DAYS")
	setInt(&cfg.Approval.PolicyCacheTTLSec, "RGX_APPROVAL_POLICY_CACHE_TTL_SEC")
	setStr(&cfg.Approval.NotifyWebhook, "RGX_APPROVAL_NOTIFY_WEBHOOK")
	setStr(&cfg.Approval.NotifyWebhookSecret, "RGX_APPROVAL_NOTIFY_WEBHOOK_SECRET")
	setStr(&cfg.Runtime.ReportToken, "RGX_RUNTIME_REPORT_TOKEN")
	setInt(&cfg.Runtime.HeartbeatTimeoutSec, "RGX_RUNTIME_HEARTBEAT_TIMEOUT_SEC")
	setStr(&cfg.ConversationRouting.Mode, "RGX_CONVERSATION_ROUTE_MODE")
	setFloat64(&cfg.ConversationRouting.ConfidenceThreshold, "RGX_CONVERSATION_ROUTE_CONFIDENCE")
	setFloat64(&cfg.ConversationRouting.ConfidenceMargin, "RGX_CONVERSATION_ROUTE_MARGIN")
	setInt(&cfg.ConversationRouting.MinimumCandidates, "RGX_CONVERSATION_ROUTE_MIN_CANDIDATES")
	setFloat64(&cfg.ConversationRouting.ReadinessThreshold, "RGX_CONVERSATION_ROUTE_READINESS")
	setInt(&cfg.ConversationRouting.TotalTimeoutMs, "RGX_CONVERSATION_ROUTE_TOTAL_TIMEOUT_MS")
	setInt(&cfg.ConversationRouting.CatalogTTLSec, "RGX_CONVERSATION_ROUTE_CATALOG_TTL_SEC")
	setInt(&cfg.ConversationRouting.CandidateLimit, "RGX_CONVERSATION_ROUTE_CANDIDATE_LIMIT")
	setInt(&cfg.ConversationRouting.ResponseTopK, "RGX_CONVERSATION_ROUTE_RESPONSE_TOP_K")
	setBool(&cfg.ConversationRouting.Rerank.Enabled, "RGX_CONVERSATION_ROUTE_RERANK_ENABLED")
	setStr(&cfg.ConversationRouting.Rerank.ProviderName, "RGX_CONVERSATION_ROUTE_RERANK_PROVIDER")
	setStr(&cfg.ConversationRouting.Rerank.InstanceName, "RGX_CONVERSATION_ROUTE_RERANK_INSTANCE")
	setStr(&cfg.ConversationRouting.Rerank.ModelName, "RGX_CONVERSATION_ROUTE_RERANK_MODEL")
	setInt(&cfg.ConversationRouting.Rerank.TimeoutMs, "RGX_CONVERSATION_ROUTE_RERANK_TIMEOUT_MS")
	setInt(&cfg.ConversationRouting.Rerank.TopK, "RGX_CONVERSATION_ROUTE_RERANK_TOP_K")
	setFloat64(&cfg.ConversationRouting.Rerank.MinBaseScore, "RGX_CONVERSATION_ROUTE_RERANK_MIN_BASE")
	setBool(&cfg.ConversationRouting.Rerank.DistributionValidated, "RGX_CONVERSATION_ROUTE_RERANK_VALIDATED")
	setInt(&cfg.ConversationRouting.Gate.Pilot.MinHighConfidence, "RGX_CONVERSATION_ROUTE_PILOT_MIN_SAMPLES")
	setFloat64(&cfg.ConversationRouting.Gate.Pilot.MinWilsonLower, "RGX_CONVERSATION_ROUTE_PILOT_MIN_WILSON")
	setFloat64(&cfg.ConversationRouting.Gate.Pilot.MaxECE, "RGX_CONVERSATION_ROUTE_PILOT_MAX_ECE")
	setInt(&cfg.ConversationRouting.Gate.Production.MinHighConfidence, "RGX_CONVERSATION_ROUTE_PRODUCTION_MIN_SAMPLES")
	setFloat64(&cfg.ConversationRouting.Gate.Production.MinWilsonLower, "RGX_CONVERSATION_ROUTE_PRODUCTION_MIN_WILSON")
	setFloat64(&cfg.ConversationRouting.Gate.Production.MaxECE, "RGX_CONVERSATION_ROUTE_PRODUCTION_MAX_ECE")
	if url := os.Getenv("RGX_ALERTING_WEBHOOK_URL"); url != "" && len(cfg.Alerting.Webhooks) == 0 {
		name := os.Getenv("RGX_ALERTING_WEBHOOK_NAME")
		if name == "" {
			name = "default"
		}
		cfg.Alerting.Webhooks = append(cfg.Alerting.Webhooks, Webhook{
			Name: name, URL: url, Secret: os.Getenv("RGX_ALERTING_WEBHOOK_SECRET"), Enabled: true,
			TimeoutSec:       envInt("RGX_ALERTING_WEBHOOK_TIMEOUT_SEC", DefaultWebhookTimeoutSec),
			MaxAttempts:      envInt("RGX_ALERTING_WEBHOOK_MAX_ATTEMPTS", DefaultWebhookMaxAttempts),
			BackoffInitialMs: envInt("RGX_ALERTING_WEBHOOK_BACKOFF_INITIAL_MS", DefaultWebhookBackoffInitialMs),
			BackoffMaxMs:     envInt("RGX_ALERTING_WEBHOOK_BACKOFF_MAX_MS", DefaultWebhookBackoffMaxMs),
		})
	}
	setBool(&cfg.Alerting.Email.Enabled, "RGX_ALERTING_EMAIL_ENABLED")
	setStr(&cfg.Alerting.Email.Host, "RGX_ALERTING_EMAIL_HOST")
	setInt(&cfg.Alerting.Email.Port, "RGX_ALERTING_EMAIL_PORT")
	setStr(&cfg.Alerting.Email.Username, "RGX_ALERTING_EMAIL_USERNAME")
	setStr(&cfg.Alerting.Email.Password, "RGX_ALERTING_EMAIL_PASSWORD")
	setStr(&cfg.Alerting.Email.From, "RGX_ALERTING_EMAIL_FROM")
	setStr(&cfg.Alerting.Email.To, "RGX_ALERTING_EMAIL_TO")
	setInt(&cfg.Alerting.Email.TimeoutSec, "RGX_ALERTING_EMAIL_TIMEOUT_SEC")
	setInt(&cfg.Alerting.Email.MaxAttempts, "RGX_ALERTING_EMAIL_MAX_ATTEMPTS")
	setInt(&cfg.Alerting.Email.BackoffInitialMs, "RGX_ALERTING_EMAIL_BACKOFF_INITIAL_MS")
	setInt(&cfg.Alerting.Email.BackoffMaxMs, "RGX_ALERTING_EMAIL_BACKOFF_MAX_MS")
}

func envInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func setStr(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func setStrSlice(dst *[]string, key string) {
	if v := os.Getenv(key); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		*dst = out
	}
}

func setInt(dst *int, key string) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func setInt64(dst *int64, key string) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			*dst = n
		}
	}
}

func setUint(dst *uint, key string) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			*dst = uint(n)
		}
	}
}

func setBool(dst *bool, key string) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

func setFloat64(dst *float64, key string) {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*dst = f
		}
	}
}
