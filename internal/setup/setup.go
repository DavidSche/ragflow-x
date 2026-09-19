// Package setup implements the first-run bootstrap control plane: it lets the
// server start without any database, accepts encrypted connection settings from
// the browser wizard, persists them (AES-GCM at rest), and reconnects in-place
// by swapping the running HTTP router with the fully initialized one.
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/handler"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/approvalcache"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/pkg/routestate"
	"github.com/ragflow-x/ragflow-x/internal/pkg/rsaseal"
	"github.com/ragflow-x/ragflow-x/internal/pkg/secretstore"
	"github.com/ragflow-x/ragflow-x/internal/provider"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/router"
	"github.com/ragflow-x/ragflow-x/internal/service"
	"gorm.io/gorm"
)

// keepSecret is the RSA-encrypted sentinel a caller submits to keep the
// existing secret unchanged (used by platform-admin System Configuration when
// a secret field is left blank).
const keepSecret = "__KEEP__"

// resolveSecret returns the submitted secret unless it is blank or the keep
// sentinel, in which case the existing persisted secret is preserved.
func (m *Manager) resolveSecret(existing, submitted string) string {
	if submitted == "" || submitted == keepSecret {
		return existing
	}
	return submitted
}

func existingDBPassword(sec *secretstore.Secrets) string {
	if sec == nil {
		return ""
	}
	return sec.Database.Password
}

func existingDBDSN(sec *secretstore.Secrets) string {
	if sec == nil {
		return ""
	}
	return sec.Database.DSN
}

func existingRGKey(sec *secretstore.Secrets) string {
	if sec == nil {
		return ""
	}
	return sec.RAGFlow.APIKey
}

func existingRedisPassword(sec *secretstore.Secrets) string {
	if sec == nil {
		return ""
	}
	return sec.Redis.Password
}

// Status describes the first-run state of the control plane.
type Status struct {
	Configured  bool   `json:"configured"`
	DBUp        bool   `json:"db_up"`
	RAGFlowUp   bool   `json:"ragflow_up"`
	RAGFlowName string `json:"ragflow_provider"`
	Initialized bool   `json:"initialized"`
}

// ApplyRequest is the payload submitted by the setup wizard. Sensitive fields
// (database password, RAGFlow api key, admin password) arrive RSA-OAEP
// encrypted with the server's public key.
type ApplyRequest struct {
	Database DatabaseReq `json:"database"`
	RAGFlow  RAGFlowReq  `json:"ragflow"`
	Admin    *AdminReq   `json:"admin"`
	Redis    *RedisReq   `json:"redis"`
}

// DatabaseReq holds non-sensitive DB fields plus an encrypted password.
type DatabaseReq struct {
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password_enc"`
	Name     string `json:"name"`
	SSLMode  string `json:"sslmode"`
	DSN      string `json:"dsn"`
}

// RAGFlowReq holds non-sensitive engine fields plus an encrypted api key.
type RAGFlowReq struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key_enc"`
	Timeout  int    `json:"timeout"`
	MaxConns int    `json:"max_conns"`
}

// AdminReq holds first-admin credentials with an encrypted password.
type AdminReq struct {
	Username string `json:"username"`
	Password string `json:"password_enc"`
}

// RedisReq holds optional distributed rate-limit settings; password_enc is
// RSA-OAEP encrypted like the other secrets.
type RedisReq struct {
	Enabled  bool   `json:"enabled"`
	Addr     string `json:"addr"`
	Username string `json:"username"`
	Password string `json:"password_enc"`
	DB       int    `json:"db"`
	PoolSize int    `json:"pool_size"`
}

// Manager owns the first-run lifecycle. Swap installs the fully initialized
// router into the running HTTP server, enabling in-place reconnect.
type Manager struct {
	cfg       *config.Config
	masterKey []byte
	rsa       *rsaseal.KeyPair
	secrets   *secretstore.Secrets
	closer    io.Closer
	svc       *service.Service
	limiter   ratelimit.Limiter
	swap      func(http.Handler) error
}

// New builds a Manager, loading or creating the master key and RSA keypair and
// reading any previously persisted secrets. A missing secrets file is fine; the
// server then runs in bootstrap mode until Apply is called.
func New(cfg *config.Config, swap func(http.Handler) error) (*Manager, error) {
	if swap == nil {
		return nil, errors.New("setup swap callback is required")
	}
	masterKey, err := secretstore.LoadMasterKey(cfg.Setup.MasterKey, defaultMasterKeyFile(cfg))
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}
	rsaKey, err := rsaseal.Ensure(cfg.Setup.RSAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load rsa key: %w", err)
	}
	secrets, err := secretstore.Load(cfg.Setup.SecretsFile, masterKey)
	if err != nil {
		return nil, err
	}
	obs.Set(obs.New(cfg.Observability))
	notify.Set(notify.NewHub(cfg.Alerting, cfg.Approval))
	return &Manager{
		cfg:       cfg,
		masterKey: masterKey,
		rsa:       rsaKey,
		secrets:   secrets,
		swap:      swap,
	}, nil
}

func defaultMasterKeyFile(cfg *config.Config) string {
	dir := cfg.Setup.SecretsFile
	if i := strings.LastIndexAny(dir, `/\`); i >= 0 {
		dir = dir[:i+1]
	} else {
		dir = "./config/"
	}
	return dir + ".master.key"
}

// PublicKeyPEM returns the PEM public key used by the browser to encrypt fields.
func (m *Manager) PublicKeyPEM() string { return m.rsa.PublicPEM() }

// Configured reports whether connection secrets have been persisted.
func (m *Manager) Configured() bool { return m.secrets != nil }

// Service returns the operational service once the full engine is built, or
// nil while running in bootstrap mode.
func (m *Manager) Service() *service.Service { return m.svc }

// BootstrapEnvAdmin creates the first admin from operator-supplied environment
// variables when the engine is already running (non-wizard config path).
func (m *Manager) BootstrapEnvAdmin(ctx context.Context, username, pass string) error {
	if m.svc == nil || pass == "" {
		return nil
	}
	return m.svc.BootstrapAdmin(ctx, username, pass)
}

// Status reports configuration and dependency reachability.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	if m.secrets == nil {
		return &Status{}, nil
	}
	dbc := mergeDB(m.cfg, m.secrets)
	rgCfg := mergeRAGFlow(m.cfg, m.secrets)

	res := &Status{Configured: true}
	probeDB(ctx, dbc, res)
	probeRAGFlow(ctx, rgCfg, res)
	return res, nil
}

// ConfigView returns the currently applied connection settings with all
// secret values masked (passwords/api keys are never returned). It drives the
// platform-admin System Configuration page. Returns a JSON-safe map so the
// handler layer does not need to import this package.
func (m *Manager) ConfigView() (map[string]interface{}, error) {
	if m.secrets == nil {
		return map[string]interface{}{
			"configured": false,
			"database":   nil,
			"ragflow":    nil,
			"redis":      nil,
		}, nil
	}
	dbc := mergeDB(m.cfg, m.secrets)
	rg := mergeRAGFlow(m.cfg, m.secrets)
	return map[string]interface{}{
		"configured": true,
		"database": map[string]interface{}{
			"driver":       dbc.Driver,
			"host":         dbc.Host,
			"port":         dbc.Port,
			"user":         dbc.User,
			"name":         dbc.Name,
			"sslmode":      dbc.SSLMode,
			"dsn":          dbc.DSN,
			"has_password": dbc.Password != "",
		},
		"ragflow": map[string]interface{}{
			"provider":    rg.Provider,
			"base_url":    rg.BaseURL,
			"timeout":     rg.Timeout,
			"max_conns":   rg.MaxConns,
			"has_api_key": rg.APIKey != "",
		},
		"redis": map[string]interface{}{
			"enabled":      m.secrets.Redis.Enabled,
			"addr":         m.secrets.Redis.Addr,
			"username":     m.secrets.Redis.Username,
			"db":           m.secrets.Redis.DB,
			"pool_size":    m.secrets.Redis.PoolSize,
			"has_password": m.secrets.Redis.Password != "",
		},
	}, nil
}

// PreflightRaw is the JSON-friendly preflight for the System Configuration
// page: it decodes the same ApplyRequest shape, validates reachability without
// persisting anything, and returns a JSON-safe status map.
func (m *Manager) PreflightRaw(ctx context.Context, raw []byte) (map[string]interface{}, error) {
	var req ApplyRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("invalid config payload: %w", err)
	}
	st, err := m.Preflight(ctx, req)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"db_up":      st.DBUp,
		"ragflow_up": st.RAGFlowUp,
		"configured": st.Configured,
	}
	if st.RAGFlowName != "" {
		out["ragflow_provider"] = st.RAGFlowName
	}
	return out, nil
}

// Preflight verifies DB and RAGFlow reachability for the proposed settings
// without persisting anything.
func (m *Manager) Preflight(ctx context.Context, req ApplyRequest) (*Status, error) {
	dec, err := m.decrypt(req)
	if err != nil {
		return nil, err
	}
	res := &Status{Configured: true}
	probeDB(ctx, dec.DB, res)
	probeRAGFlow(ctx, dec.RG, res)
	return res, nil
}

func validateBootstrapAdmin(req ApplyRequest) error {
	if req.Admin == nil {
		return nil
	}
	username := strings.TrimSpace(req.Admin.Username)
	if len(username) < 3 {
		return errors.New("admin username must be at least 3 characters")
	}
	if len(req.Admin.Password) < 8 {
		return errors.New("admin password must be at least 8 characters")
	}
	return nil
}

func validateConnectionConfig(dbc config.Database, rg config.RAGFlow) error {
	switch dbc.Driver {
	case "postgres":
		if dbc.Host == "" || dbc.User == "" || dbc.Name == "" || dbc.Password == "" {
			return errors.New("postgres host, user, database name and password are required")
		}
	case "sqlite":
		if dbc.DSN == "" {
			return errors.New("sqlite dsn is required")
		}
	default:
		return fmt.Errorf("unsupported database driver: %s", dbc.Driver)
	}
	if rg.Provider == "" || rg.BaseURL == "" || rg.APIKey == "" {
		return errors.New("ragflow provider, base url and api key are required")
	}
	return nil
}

// Apply decrypts the submitted settings, persists them encrypted, reconnects
// by building the full application, and swaps it into the running server.
func (m *Manager) Apply(ctx context.Context, req ApplyRequest) error {
	dec, err := m.decrypt(req)
	if err != nil {
		return err
	}
	if err := validateBootstrapAdmin(req); err != nil {
		return err
	}
	if err := validateConnectionConfig(dec.DB, dec.RG); err != nil {
		return err
	}
	sec := &secretstore.Secrets{Database: toSecretDB(dec.DB), RAGFlow: toSecretRG(dec.RG)}
	sec.Redis = dec.Redis
	// Preserve an already-persisted JWT signing secret across wizard re-runs:
	// rotating it here would silently invalidate every issued session.
	if m.secrets != nil && m.secrets.JWTSecret != "" {
		sec.JWTSecret = m.secrets.JWTSecret
	}
	if req.Admin != nil {
		sec.Admin = &secretstore.Admin{
			Username: strings.TrimSpace(req.Admin.Username),
			Password: req.Admin.Password,
		}
	}
	// Resolve and validate the JWT signing secret up front so an insecure
	// explicit configuration is reported to the wizard caller instead of
	// failing deep inside engine construction.
	if err := m.ensureJWTSecret(sec); err != nil {
		return err
	}
	if err := secretstore.Save(m.cfg.Setup.SecretsFile, m.masterKey, sec); err != nil {
		return fmt.Errorf("persist secrets: %w", err)
	}

	engine, closer, err := m.buildEngine(m.cfg, sec)
	if err != nil {
		return err
	}
	if err := m.swap(engine); err != nil {
		_ = closer.Close()
		return err
	}
	if old := m.closer; old != nil {
		_ = old.Close()
	}
	m.closer = closer
	m.secrets = sec
	return nil
}

// ApplyRaw is the HTTP-friendly variant of Apply: it decodes the raw JSON body
// (used by the main router to expose the same first-run wizard endpoints).
func (m *Manager) ApplyRaw(ctx context.Context, raw []byte) error {
	var req ApplyRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid setup payload: %w", err)
	}
	return m.Apply(ctx, req)
}

// Startup selects the initial engine: a full application when persisted or
// YAML/env connection settings are usable, otherwise the first-run bootstrap
// router that only serves the setup endpoints.
//
// A JWT-secret problem is fatal here: unlike infrastructure errors it must
// never degrade a configured server into wizard mode, where the setup API
// would be exposed on an already-operational deployment.
func (m *Manager) Startup() (http.Handler, error) {
	if m.secrets != nil {
		if err := m.ensureJWTSecret(m.secrets); err != nil {
			return nil, err
		}
		engine, closer, err := m.buildEngine(m.cfg, m.secrets)
		if err == nil {
			m.closer = closer
			return engine, nil
		}
	}
	databaseConfigured := m.cfg.Database.Driver == "sqlite" ||
		m.cfg.Database.Password != "" || m.cfg.Database.DSN != ""
	if databaseConfigured {
		sec := &secretstore.Secrets{Database: toSecretDB(m.cfg.Database), RAGFlow: toSecretRG(m.cfg.RAGFlow), Redis: toSecretRedis(m.cfg.Redis)}
		if err := m.ensureJWTSecret(sec); err != nil {
			return nil, err
		}
		engine, closer, err := m.buildEngine(m.cfg, sec)
		if err == nil {
			m.closer = closer
			return engine, nil
		}
	}
	return m.bootstrapRouter(), nil
}

// Close releases the currently running operational resources.
func (m *Manager) Close() error {
	if m.limiter != nil {
		_ = m.limiter.Close()
		m.limiter = nil
	}
	notify.Shutdown()
	if m.closer != nil {
		return m.closer.Close()
	}
	return nil
}

// decrypted is the resolved plaintext projection of an ApplyRequest.
type decrypted struct {
	DB    config.Database
	RG    config.RAGFlow
	Redis secretstore.Redis
}

func (m *Manager) decrypt(req ApplyRequest) (*decrypted, error) {
	pass, err := m.rsa.Decrypt(req.Database.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypt database password: %w", err)
	}
	key, err := m.rsa.Decrypt(req.RAGFlow.APIKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt ragflow api key: %w", err)
	}

	dbc := m.cfg.Database
	dbc.Driver = first(req.Database.Driver, dbc.Driver)
	dbc.Host = first(req.Database.Host, dbc.Host)
	dbc.Port = intIf(req.Database.Port, dbc.Port)
	dbc.User = first(req.Database.User, dbc.User)
	dbc.Password = m.resolveSecret(existingDBPassword(m.secrets), pass)
	dbc.Name = first(req.Database.Name, dbc.Name)
	dbc.SSLMode = first(req.Database.SSLMode, dbc.SSLMode)
	if dbc.Driver == "sqlite" {
		dbc.DSN = first(req.Database.DSN, first(existingDBDSN(m.secrets), dbc.DSN))
	} else {
		dbc.DSN = ""
	}

	rg := m.cfg.RAGFlow
	rg.Provider = first(req.RAGFlow.Provider, rg.Provider)
	rg.BaseURL = first(req.RAGFlow.BaseURL, rg.BaseURL)
	rg.APIKey = m.resolveSecret(existingRGKey(m.secrets), key)
	rg.Timeout = intIf(req.RAGFlow.Timeout, rg.Timeout)
	rg.MaxConns = intIf(req.RAGFlow.MaxConns, rg.MaxConns)

	if req.Admin != nil && strings.TrimSpace(req.Admin.Password) != "" {
		ap, err := m.rsa.Decrypt(req.Admin.Password)
		if err != nil {
			return nil, fmt.Errorf("decrypt admin password: %w", err)
		}
		req.Admin.Password = ap
	}
	redisSec := secretstore.Redis{}
	if req.Redis != nil {
		if strings.TrimSpace(req.Redis.Password) != "" {
			rp, err := m.rsa.Decrypt(req.Redis.Password)
			if err != nil {
				return nil, fmt.Errorf("decrypt redis password: %w", err)
			}
			req.Redis.Password = rp
		}
		redisSec = secretstore.Redis{
			Enabled:  req.Redis.Enabled,
			Addr:     first(req.Redis.Addr, m.cfg.Redis.Addr),
			Username: first(req.Redis.Username, m.cfg.Redis.Username),
			Password: m.resolveSecret(existingRedisPassword(m.secrets), req.Redis.Password),
			DB:       intIf(req.Redis.DB, m.cfg.Redis.DB),
			PoolSize: intIf(req.Redis.PoolSize, m.cfg.Redis.PoolSize),
		}
	}
	return &decrypted{DB: dbc, RG: rg, Redis: redisSec}, nil
}

func probeDB(ctx context.Context, dbc config.Database, res *Status) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	gdb, err := db.Open(dbc)
	if err != nil {
		return
	}
	defer closeGDB(gdb)
	sqlDB, err := gdb.DB()
	if err != nil {
		return
	}
	defer sqlDB.Close()
	if sqlDB.PingContext(ctx) == nil {
		res.DBUp = true
	}
}

func probeRAGFlow(ctx context.Context, rgCfg config.RAGFlow, res *Status) {
	rg, err := provider.NewRAGFlow(rgCfg)
	if err != nil {
		return
	}
	res.RAGFlowName = rg.Name()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := rg.Health(ctx); err == nil {
		res.RAGFlowUp = true
	}
}

// buildEngine constructs the fully initialized application from the merged
// config and yields its router plus a closer (the operational store).
func (m *Manager) buildEngine(cfg *config.Config, sec *secretstore.Secrets) (http.Handler, io.Closer, error) {
	// Defense in depth: callers resolve this up front; repeat here so any
	// future caller can never construct an engine under an unsafe secret.
	if err := m.ensureJWTSecret(sec); err != nil {
		return nil, nil, err
	}

	dbc := mergeDB(cfg, sec)
	rgCfg := mergeRAGFlow(cfg, sec)

	if err := migrateDatabase(dbc); err != nil {
		return nil, nil, err
	}
	gdb, err := db.Open(dbc)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	check, cerr := gdb.DB()
	if cerr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cerr = check.PingContext(ctx)
		cancel()
	}
	if cerr != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("ping database: %w", cerr)
	}
	if dbc.Driver == "postgres" && dbc.EnforcePrivileges {
		if err := db.VerifyPostgresRuntimePrivileges(gdb); err != nil {
			closeGDB(gdb)
			return nil, nil, fmt.Errorf("runtime database privilege check: %w", err)
		}
	}

	store := repository.NewStore(gdb)
	rg, err := provider.NewRAGFlow(rgCfg)
	if err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("init ragflow provider: %w", err)
	}

	jm := jwt.NewManager(sec.JWTSecret, cfg.App.JWTExpireHours)
	jm.SetRefreshTTL(time.Duration(cfg.App.RefreshExpireHours) * time.Hour)

	cryptoKey, err := m.resolveCryptoKey(cfg, sec)
	if err != nil {
		closeGDB(gdb)
		return nil, nil, err
	}
	svc := service.New(store, rg, jm, cryptoKey)
	notify.Get().SetSink(svc)
	svc.SetRoutePolicy(cfg.ConversationRouting)
	svc.SetProviderURLPolicy(cfg.Security.AllowPrivateProviderBaseURL)
	svc.SetSecurityConfig(cfg.Security)
	svc.SetObservabilityConfig(cfg.Observability)
	svc.SetAlertingConfig(cfg.Alerting)
	svc.SetOIDCConfig(cfg.OIDC)
	svc.StartCapabilityVerification(context.Background())
	routeState, routeStateErr := m.buildRouteState(sec)
	if routeStateErr != nil {
		logger.Warn("redis route state store unavailable; falling back to database store", "error", routeStateErr)
	} else {
		svc.SetRouteStateStore(routeState)
	}
	approvalCache, approvalCacheErr := m.buildApprovalPolicyCache(sec)
	if approvalCacheErr != nil {
		logger.Warn("redis approval policy cache unavailable; falling back to process-local cache", "error", approvalCacheErr)
	} else if approvalCache != nil {
		svc.SetApprovalPolicySharedCache(approvalCache)
	}
	svc.SetGatewayClients(service.GatewayConfig{
		NonStreamTimeout:    time.Duration(cfg.Gateway.NonStreamTimeoutSec) * time.Second,
		StreamHeaderTimeout: time.Duration(cfg.Gateway.StreamHeaderTimeoutSec) * time.Second,
		StreamTimeout:       time.Duration(cfg.Gateway.StreamTimeoutSec) * time.Second,
		MaxConns:            cfg.Gateway.MaxConns,
	})
	limiter, err := m.buildLimiter(sec)
	if err != nil {
		closeGDB(gdb)
		return nil, nil, err
	}
	if old := m.limiter; old != nil {
		_ = old.Close()
	}
	m.limiter = limiter
	svc.SetLoginLimiter(limiter)
	svc.SetEstimatedCost(cfg.Usage.CostPer1KTokens, cfg.Usage.Currency)
	svc.RegisterLLM = rgCfg.AutoRegister
	svc.DataDir = cfg.App.DataDir
	if err := svc.SetupApproval(cfg.Approval); err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("setup approval workflow: %w", err)
	}
	if err := svc.InitializeSystemSettingsFromConfig(context.Background(), *cfg); err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("initialize system settings: %w", err)
	}
	effectiveCfg, err := svc.EffectiveSystemSettingsConfig(context.Background(), *cfg)
	if err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("resolve effective system settings: %w", err)
	}
	obs.Set(obs.New(effectiveCfg.Observability))
	hub := notify.NewHub(effectiveCfg.Alerting, effectiveCfg.Approval)
	hub.SetSink(svc)
	notify.Set(hub)

	// A2 async worker: start the in-process runner with the document-sync
	// consumer. It is stopped via the returned closer (graceful shutdown, with
	// running jobs heartbeated and recovered on restart).
	runner := svc.SetupWorker(service.DefaultWorkerConfig())

	// A5 data retention janitor (doc/33 Sprint P1-2): register the worker on
	// the runner and arm the recurring purge when retention is enabled in
	// config (the janitor self-schedules its next run via the durable job
	// table). Disabled retention makes ScheduleRetention a no-op.
	svc.SetupRetention(cfg.Retention)
	if _, err := svc.ScheduleRetention(context.Background(), ""); err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("schedule data retention: %w", err)
	}

	// Audit anchors are immutable, periodic chain-tail snapshots. They are
	// disabled by default and armed through the durable worker like retention.
	svc.SetupAuditAnchor(cfg.AuditAnchor)
	if _, err := svc.ScheduleAuditAnchor(context.Background(), ""); err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("schedule audit anchoring: %w", err)
	}
	if _, err := svc.ScheduleResourceSyncReconcile(context.Background(), ""); err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("schedule ragflow reconciliation: %w", err)
	}
	if _, err := svc.ScheduleAlertDeliveryCompensation(context.Background(), ""); err != nil {
		closeGDB(gdb)
		return nil, nil, fmt.Errorf("schedule alert delivery compensation: %w", err)
	}

	runner.Start(context.Background())

	if sec.Admin != nil && sec.Admin.Username != "" && sec.Admin.Password != "" {
		if err := svc.BootstrapAdmin(context.Background(), sec.Admin.Username, sec.Admin.Password); err != nil {
			closeGDB(gdb)
			return nil, nil, fmt.Errorf("bootstrap admin: %w", err)
		}
	}

	m.svc = svc
	h := handler.New(svc)
	h.SetSetupRSA(m.rsa)
	engine := router.New(effectiveCfg, h, m.limiter)
	h.SetSetupManager(m)
	closers := []io.Closer{runnerStopper{runner}, store}
	if routeState != nil {
		closers = append(closers, routeState)
	}
	if approvalCache != nil {
		closers = append(closers, approvalCache)
	}
	return engine, closeAll(closers...), nil
}

func migrateDatabase(dbc config.Database) error {
	if dbc.Driver != "postgres" {
		gdb, err := db.Open(dbc)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer closeGDB(gdb)
		if err := db.Migrate(gdb); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
		return nil
	}

	if dbc.MigrationUser != "" || dbc.MigrationPassword != "" {
		if dbc.MigrationUser == "" || dbc.MigrationPassword == "" {
			return fmt.Errorf("postgres migration user and password must be configured together")
		}
		if dbc.EnforcePrivileges && dbc.MigrationUser == dbc.User {
			return fmt.Errorf("postgres migration user must differ from runtime database user")
		}
		migrationDBC := dbc
		migrationDBC.User = dbc.MigrationUser
		migrationDBC.Password = dbc.MigrationPassword
		gdb, err := db.Open(migrationDBC)
		if err != nil {
			return fmt.Errorf("open migration database: %w", err)
		}
		defer closeGDB(gdb)
		if err := db.Migrate(gdb); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
		return nil
	}

	if dbc.EnforcePrivileges {
		return fmt.Errorf("postgres migration credentials are required when runtime privileges are enforced")
	}

	gdb, err := db.Open(dbc)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer closeGDB(gdb)
	if err := db.Migrate(gdb); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

func (m *Manager) buildRouteState(sec *secretstore.Secrets) (routestate.Store, error) {
	if !sec.Redis.Enabled || strings.TrimSpace(sec.Redis.Addr) == "" {
		return nil, nil
	}
	return routestate.NewRedis(routestate.Config{
		Addr:     sec.Redis.Addr,
		Username: sec.Redis.Username,
		Password: sec.Redis.Password,
		DB:       sec.Redis.DB,
		PoolSize: sec.Redis.PoolSize,
	})
}

func (m *Manager) buildApprovalPolicyCache(sec *secretstore.Secrets) (*approvalcache.Redis, error) {
	if !sec.Redis.Enabled || strings.TrimSpace(sec.Redis.Addr) == "" {
		return nil, nil
	}
	return approvalcache.NewRedis(approvalcache.Config{
		Addr: sec.Redis.Addr, Username: sec.Redis.Username, Password: sec.Redis.Password,
		DB: sec.Redis.DB, PoolSize: sec.Redis.PoolSize,
	})
}

func mergeDB(cfg *config.Config, sec *secretstore.Secrets) config.Database {
	dbc := cfg.Database
	s := sec.Database
	dbc.Driver = first(s.Driver, dbc.Driver)
	dbc.Host = first(s.Host, dbc.Host)
	dbc.Port = intIf(s.Port, dbc.Port)
	dbc.User = first(s.User, dbc.User)
	dbc.Password = s.Password
	dbc.Name = first(s.Name, dbc.Name)
	dbc.SSLMode = first(s.SSLMode, dbc.SSLMode)
	dbc.DSN = first(s.DSN, dbc.DSN)
	return dbc
}

func mergeRAGFlow(cfg *config.Config, sec *secretstore.Secrets) config.RAGFlow {
	rg := cfg.RAGFlow
	s := sec.RAGFlow
	rg.Provider = first(s.Provider, rg.Provider)
	rg.BaseURL = first(s.BaseURL, rg.BaseURL)
	rg.APIKey = s.APIKey
	rg.Timeout = intIf(s.Timeout, rg.Timeout)
	rg.MaxConns = intIf(s.MaxConns, rg.MaxConns)
	return rg
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func intIf(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}

// ensureJWTSecret resolves the JWT signing secret for an engine build and
// enforces the security baseline. Resolution order:
//
//  1. A value already persisted in the secrets file (survives restarts).
//  2. An operator-supplied app.jwt_secret / RGX_JWT_SECRET, which must pass
//     jwt.ValidateSecret (non-empty, not a placeholder, >= MinSecretLen) or
//     startup is refused.
//  3. Otherwise a strong random secret is generated and persisted, so a
//     fresh install can never run under an empty or guessable key.
func (m *Manager) ensureJWTSecret(sec *secretstore.Secrets) error {
	if sec.JWTSecret != "" {
		return jwt.ValidateSecret(sec.JWTSecret)
	}
	if s := strings.TrimSpace(m.cfg.App.JWTSecret); s != "" {
		if err := jwt.ValidateSecret(s); err != nil {
			return err
		}
		sec.JWTSecret = s
	} else {
		sec.JWTSecret = randomHex(32)
		logger.Info("no jwt secret configured; generated a random one and persisted it to the secrets file (set RGX_JWT_SECRET for multi-replica deployments)")
	}
	if err := secretstore.Save(m.cfg.Setup.SecretsFile, m.masterKey, sec); err != nil {
		return fmt.Errorf("persist jwt secret: %w", err)
	}
	return nil
}

// resolveCryptoKey returns the key used to seal stored API keys, generating
// and persisting one (encrypted at rest) when neither the persisted secrets
// nor RGX_ENCRYPTION_KEY provide one.
func (m *Manager) resolveCryptoKey(cfg *config.Config, sec *secretstore.Secrets) (string, error) {
	if sec.CryptoKey != "" {
		return sec.CryptoKey, nil
	}
	if cfg.App.EncryptionKey != "" {
		sec.CryptoKey = cfg.App.EncryptionKey
		return cfg.App.EncryptionKey, nil
	}
	key := randomHex(32)
	sec.CryptoKey = key
	if err := secretstore.Save(m.cfg.Setup.SecretsFile, m.masterKey, sec); err != nil {
		return "", fmt.Errorf("persist crypto key: %w", err)
	}
	return key, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func closeGDB(g *gorm.DB) {
	if sqlDB, err := g.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func toSecretDB(c config.Database) secretstore.Database {
	return secretstore.Database{Driver: c.Driver, Host: c.Host, Port: c.Port, User: c.User, Password: c.Password, Name: c.Name, SSLMode: c.SSLMode, DSN: c.DSN}
}

func toSecretRG(c config.RAGFlow) secretstore.RAGFlow {
	return secretstore.RAGFlow{Provider: c.Provider, BaseURL: c.BaseURL, APIKey: c.APIKey, Timeout: c.Timeout, MaxConns: c.MaxConns}
}

func toSecretRedis(c config.Redis) secretstore.Redis {
	return secretstore.Redis{Enabled: c.Enabled, Addr: c.Addr, Username: c.Username, Password: c.Password, DB: c.DB, PoolSize: c.PoolSize}
}

// buildLimiter returns the sliding-window limiter for the engine. Redis is
// used when enabled; if it cannot be reached, we degrade to the process-local
// memory backend (with a warning) so startup/operation is not blocked.
func (m *Manager) buildLimiter(sec *secretstore.Secrets) (ratelimit.Limiter, error) {
	if !sec.Redis.Enabled || strings.TrimSpace(sec.Redis.Addr) == "" {
		return ratelimit.NewMemory(), nil
	}
	rl, err := ratelimit.NewRedis(ratelimit.RedisConfig{
		Addr:     sec.Redis.Addr,
		Username: sec.Redis.Username,
		Password: sec.Redis.Password,
		DB:       sec.Redis.DB,
		PoolSize: sec.Redis.PoolSize,
	})
	if err != nil {
		logger.Warn("redis rate-limit backend unavailable; falling back to in-memory", "error", err)
		return ratelimit.NewMemory(), nil
	}
	return rl, nil
}

// closeAll composes io.Closers into one that closes each in order, returning
// the first error encountered.
func closeAll(closers ...io.Closer) io.Closer {
	return io.Closer(closerFunc(func() error {
		var firstErr error
		for _, c := range closers {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}))
}

// closerFunc adapts a plain func to io.Closer.
type closerFunc func() error

// Close implements io.Closer.
func (f closerFunc) Close() error { return f() }

// runnerStopper adapts the async worker Runner to io.Closer so it is stopped
// gracefully (bounded wait) when the engine is torn down.
type runnerStopper struct{ r *service.Runner }

// Close implements io.Closer.
func (s runnerStopper) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.r.Stop(ctx)
}
