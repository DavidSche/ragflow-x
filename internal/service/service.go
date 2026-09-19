// Package service holds the application business logic. Services depend only
// on repository and provider interfaces, keeping them unit-testable.
package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/pkg/routestate"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ErrForbidden is returned when the caller lacks the required permission.
var ErrForbidden = httperr.Forbidden("insufficient permission")

// Service bundles the dependencies shared by the business services.
type Service struct {
	Store       repository.Store
	RAGFlow     ragflow.Client
	RouteState  routestate.Store
	JWT         *jwt.Manager
	EncryptKey  []byte
	HMACKey     []byte
	RegisterLLM bool
	DataDir     string
	// allowPrivateProviderBaseURL is an explicit deployment exception for
	// internal LLM gateways. Production internet-facing deployments keep it off.
	allowPrivateProviderBaseURL bool
	// httpClient/streamClient handle outbound provider requests. They use
	// separate timeouts so long-lived SSE streams aren't cut short.
	httpClient   *http.Client
	streamClient *http.Client
	// LoginLimiter is the pluggable sliding-window limiter (memory by default,
	// Redis when configured for distributed deployments).
	LoginLimiter          ratelimit.Limiter
	EstimatedCostPer1K    float64
	EstimatedCostCurrency string
	oidcMu                sync.RWMutex
	oidcConfig            config.OIDC
	oidcClient            OIDCClient
	// quotaReapMu/quotaLastReap throttle stale-reservation sweeps per key/period.
	quotaReapMu   sync.Mutex
	quotaLastReap map[string]time.Time
	// Runner is the in-process async worker (doc/33 A2); nil until SetupWorker.
	Runner       *Runner
	RoutePolicy  config.ConversationRouting
	capabilities *capabilityRegistry
	// retentionMu guards retentionPolicy (A5 data retention, doc/33 Sprint P1-2).
	retentionMu                      sync.Mutex
	retentionPolicy                  config.Retention
	auditAnchorMu                    sync.Mutex
	auditAnchorPolicy                config.AuditAnchor
	securityMu                       sync.RWMutex
	securityConfig                   config.Security
	observabilityMu                  sync.RWMutex
	observabilityConfig              config.Observability
	alertingMu                       sync.RWMutex
	alertingConfig                   config.Alerting
	dsCacheMu                        sync.Mutex
	dsCache                          map[string]dsCacheEntry
	catalogReconcileMu               sync.Mutex
	catalogReconcileLast             map[string]time.Time
	approvalMu                       sync.RWMutex
	approvalConfig                   config.Approval
	approvalCacheRevision            string
	approvalPolicyCache              map[string]approvalPolicyCacheEntry
	approvalSharedCache              ApprovalPolicySharedCache
	approvalSubmitLimiter            ratelimit.Limiter
	approvalDecisionLimiter          ratelimit.Limiter
	approvalExecutorRegistryOverride *ApprovalExecutorRegistry
	runtimeMu                        sync.RWMutex
	runtimeConfig                    config.Runtime
}

// SetRouteStateStore installs the production short-TTL route store. Tests and
// single-node deployments may leave it nil to use the transactional DB store.
func (s *Service) SetRouteStateStore(store routestate.Store) {
	s.RouteState = store
}

// New builds a Service.
func New(store repository.Store, ragClient ragflow.Client, jm *jwt.Manager, encryptSecret string) *Service {
	encryptSecret = effectiveEncryptSecret(encryptSecret)
	sum := sha256.Sum256([]byte(encryptSecret))
	svc := &Service{Store: store, RAGFlow: ragClient, JWT: jm, EncryptKey: sum[:], HMACKey: hmacKey(sum[:]), DataDir: "./data"}
	svc.SetGatewayClients(DefaultGatewayConfig())
	svc.LoginLimiter = ratelimit.NewMemory()
	svc.EstimatedCostPer1K = 0.0002
	svc.EstimatedCostCurrency = "CNY"
	svc.dsCache = map[string]dsCacheEntry{}
	svc.catalogReconcileLast = map[string]time.Time{}
	svc.approvalPolicyCache = map[string]approvalPolicyCacheEntry{}
	svc.approvalSubmitLimiter = ratelimit.NewMemory()
	svc.approvalDecisionLimiter = ratelimit.NewMemory()
	svc.SetRuntimeConfig(config.Runtime{HeartbeatTimeoutSec: 60})
	svc.capabilities = newCapabilityRegistry()
	return svc
}

type approvalPolicyCacheEntry struct {
	policies []model.ApprovalPolicy
	expireAt time.Time
}

// ApprovalPolicySharedCache keeps policy reads consistent across replicas.
// A nil implementation means single-node mode and leaves the local cache in use.
type ApprovalPolicySharedCache interface {
	LoadPolicies(ctx context.Context, key string) ([]model.ApprovalPolicy, bool, error)
	SavePolicies(ctx context.Context, key string, policies []model.ApprovalPolicy, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Close() error
}

// SetApprovalPolicySharedCache installs a Redis-backed shared cache.
func (s *Service) SetApprovalPolicySharedCache(cache ApprovalPolicySharedCache) {
	s.approvalMu.Lock()
	s.approvalSharedCache = cache
	s.approvalMu.Unlock()
}

const defaultAllZeroKey = "00000000000000000000000000000000"

// effectiveEncryptSecret refuses empty or all-zero placeholder values. When
// no usable key is supplied it derives an ephemeral random key, ensuring
// stored API keys are never encrypted under a known default. Persist the key
// via RGX_ENCRYPTION_KEY or the first-run wizard so keys survive restarts.
func effectiveEncryptSecret(secret string) string {
	if secret != "" && secret != defaultAllZeroKey {
		return secret
	}
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return "rgx-" + hex.EncodeToString(buf)
}

func hmacKey(master []byte) []byte {
	mac := hmac.New(sha256.New, []byte("ragflow-x:hmac-key-v1"))
	_, _ = mac.Write(master)
	return mac.Sum(nil)
}

// GatewayConfig controls outbound provider HTTP timeouts.
type GatewayConfig struct {
	NonStreamTimeout    time.Duration
	StreamHeaderTimeout time.Duration
	StreamTimeout       time.Duration
	MaxConns            int
}

// DefaultGatewayConfig returns safe defaults: a bounded request timeout for
// non-streaming and no overall timeout for SSE (headers are still bounded).
func DefaultGatewayConfig() GatewayConfig {
	return GatewayConfig{
		NonStreamTimeout:    120 * time.Second,
		StreamHeaderTimeout: 30 * time.Second,
		StreamTimeout:       0,
		MaxConns:            20,
	}
}

// SetGatewayClients rebuilds the non-streaming and streaming provider HTTP
// clients from configuration. Streaming responses can be long-lived, so the
// stream client has no overall timeout while still bounding response headers.
func (s *Service) SetGatewayClients(cfg GatewayConfig) {
	maxConns := cfg.MaxConns
	if maxConns <= 0 {
		maxConns = 20
	}
	tr := &http.Transport{
		DialContext:           s.enterpriseProbeDialContext,
		MaxConnsPerHost:       maxConns,
		ResponseHeaderTimeout: cfg.StreamHeaderTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	s.httpClient = &http.Client{Transport: tr, Timeout: cfg.NonStreamTimeout, CheckRedirect: s.providerRedirectPolicy}
	s.streamClient = &http.Client{Transport: tr, Timeout: cfg.StreamTimeout, CheckRedirect: s.providerRedirectPolicy}
}

// providerRedirectPolicy applies the same network policy to redirect targets
// as the initially configured provider URL.
func (s *Service) providerRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return http.ErrUseLastResponse
	}
	if err := validateProviderHost(req.URL.Hostname(), s.allowPrivateProviderBaseURL); err != nil {
		return err
	}
	return nil
}

// SetProviderURLPolicy controls whether provider BaseURL may resolve to a
// private, loopback, link-local, or carrier NAT address.
func (s *Service) SetProviderURLPolicy(allowPrivateNetwork bool) {
	s.allowPrivateProviderBaseURL = allowPrivateNetwork
}

func (s *Service) SetSecurityConfig(cfg config.Security) {
	s.securityMu.Lock()
	s.securityConfig = cfg
	s.securityMu.Unlock()
	s.SetProviderURLPolicy(cfg.AllowPrivateProviderBaseURL)
}

func (s *Service) CurrentSecurityConfig() config.Security {
	s.securityMu.RLock()
	defer s.securityMu.RUnlock()
	return s.securityConfig
}

func (s *Service) SetObservabilityConfig(cfg config.Observability) {
	s.observabilityMu.Lock()
	s.observabilityConfig = cfg
	s.observabilityMu.Unlock()
}

func (s *Service) CurrentObservabilityConfig() config.Observability {
	s.observabilityMu.RLock()
	defer s.observabilityMu.RUnlock()
	return s.observabilityConfig
}

func (s *Service) SetAlertingConfig(cfg config.Alerting) {
	if cfg.ThrottleSec <= 0 {
		cfg.ThrottleSec = 60
	}
	if cfg.CompensationIntervalSec <= 0 {
		cfg.CompensationIntervalSec = 60
	}
	if cfg.CompensationMaxAttempts <= 0 {
		cfg.CompensationMaxAttempts = 12
	}
	if cfg.CompensationInitialDelaySec < 0 {
		cfg.CompensationInitialDelaySec = 30
	}
	if cfg.CompensationMaxDelaySec < cfg.CompensationInitialDelaySec {
		cfg.CompensationMaxDelaySec = cfg.CompensationInitialDelaySec
	}
	if cfg.CompensationLeaseSec <= 0 {
		cfg.CompensationLeaseSec = 300
	}
	if cfg.CompensationJitterPercent < 0 {
		cfg.CompensationJitterPercent = 20
	}
	if cfg.CompensationJitterPercent > 100 {
		cfg.CompensationJitterPercent = 100
	}
	s.alertingMu.Lock()
	s.alertingConfig = cfg
	s.alertingMu.Unlock()
}

func (s *Service) CurrentAlertingConfig() config.Alerting {
	s.alertingMu.RLock()
	defer s.alertingMu.RUnlock()
	return s.alertingConfig
}

// SetLoginLimiter swaps the sliding-window limiter, e.g. from the process-local
// memory backend to a distributed Redis backend.
func (s *Service) SetLoginLimiter(l ratelimit.Limiter) {
	if l != nil {
		s.LoginLimiter = l
	}
}

// SetEstimatedCost configures the estimated token-cost rate and currency used by
// usage reports.
func (s *Service) SetEstimatedCost(costPer1K float64, currency string) {
	if costPer1K >= 0 {
		s.EstimatedCostPer1K = costPer1K
	}
	if currency != "" {
		s.EstimatedCostCurrency = currency
	}
}

// CurrentUser loads a user from the store by id.
func (s *Service) CurrentUser(ctx context.Context, userID string) (*model.User, error) {
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil || u == nil {
		return nil, err
	}
	return u, nil
}

// authzContext carries the resolved user and effective permissions for one
// authorization decision.
type authzContext struct {
	user     *model.User
	platform bool
	allow    []model.Permission
	deny     []model.Permission
}

// Authorize decides whether a user may perform an action on a resource using
// RBAC. It resolves all of a user's roles (plus inherited parents), evaluates
// deny rules before allow rules, and grants platform-scope roles the wildcard.
func (s *Service) Authorize(ctx context.Context, userID, action, resource string) error {
	ac, err := s.authz(ctx, userID)
	if err != nil {
		return err
	}
	return ac.evaluate(action, resource)
}

// AuthorizeObject extends RBAC with ABAC: the principal must hold the RBAC
// permission AND the object must belong to the caller's tenant, be owned by the
// caller, or the principal must be a platform role scoped to manage it.
func (s *Service) AuthorizeObject(ctx context.Context, userID, action, resource string, objectTenantID, ownerID string) error {
	if err := s.Authorize(ctx, userID, action, resource); err != nil {
		return err
	}
	ac, err := s.authz(ctx, userID)
	if err != nil {
		return err
	}
	// Empty objectTenantID is treated as out of scope (fail closed): callers
	// resolve and pass the owning tenant explicitly, so a missing tenant can
	// never be mistaken for a match.
	inTenant := ac.user.TenantID == objectTenantID
	isOwner := ownerID != "" && ownerID == userID
	if inTenant || isOwner {
		return nil
	}
	if s.canAccessAcrossTenant(ac, action) {
		return nil
	}
	return ErrForbidden
}

// authz loads the user and resolves effective RBAC permissions across all of
// the user's roles (including any inherited parents).
func (s *Service) authz(ctx context.Context, userID string) (*authzContext, error) {
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Status != model.UserStatusActive {
		return nil, httperr.Unauthorized("user is disabled or missing")
	}
	if err := s.EnsureTenantActive(ctx, u.TenantID); err != nil {
		return nil, err
	}
	roles, err := s.Store.ListRolesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(roles) == 0 {
		// rgx_user_role is the authoritative source. User.Role is a derived
		// marker; we only fall back to it for legacy rows that predate the RBAC
		// association, and only when it resolves to an actual role.
		r, rerr := s.Store.GetRole(ctx, u.Role)
		if rerr != nil {
			return nil, rerr
		}
		if r != nil {
			roles = append(roles, *r)
		}
	}
	roleIDs, err := s.expandRoles(ctx, roles)
	if err != nil {
		return nil, err
	}
	ac := &authzContext{user: u, platform: false, allow: nil, deny: nil}
	platformRole := false
	seen := map[string]bool{}
	for _, rid := range roleIDs {
		if role, rerr := s.Store.GetRole(ctx, rid); rerr != nil {
			return nil, rerr
		} else if role != nil && role.Scope == model.RoleScopePlatform {
			platformRole = true
		}
		perms, perr := s.Store.GetPermissions(ctx, rid)
		if perr != nil {
			return nil, perr
		}
		for _, p := range perms {
			key := p.RoleID + "|" + p.Action + "|" + p.Resource + "|" + p.Effect
			if seen[key] {
				continue
			}
			seen[key] = true
			if p.Effect == model.PermissionEffectDeny {
				ac.deny = append(ac.deny, p)
			} else {
				ac.allow = append(ac.allow, p)
			}
		}
	}
	ac.platform = platformRole && ac.evaluate("governance.manage", "tenant") == nil
	return ac, nil
}

// expandRoles returns the closure of role ids including inherited parents, and
// whether any resolved role is platform scoped.
func (s *Service) expandRoles(ctx context.Context, roles []model.Role) ([]string, error) {
	collected := map[string]bool{}
	queue := make([]string, 0, len(roles))
	for _, r := range roles {
		if r.ID != "" && !collected[r.ID] {
			collected[r.ID] = true
			queue = append(queue, r.ID)
		}
	}
	result := make([]string, 0, len(collected))
	known := map[string]bool{}

	for i := 0; i < len(queue); i++ {
		rid := queue[i]
		result = append(result, rid)
		if known[rid] {
			continue
		}
		known[rid] = true
		r, err := s.Store.GetRole(ctx, rid)
		if err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		if r.ParentID != "" && !collected[r.ParentID] {
			collected[r.ParentID] = true
			queue = append(queue, r.ParentID)
		}
	}
	return result, nil
}

func (c *authzContext) evaluate(action, resource string) error {
	for _, p := range c.deny {
		if matches(p.Action, action) && matches(p.Resource, resource) {
			return ErrForbidden
		}
	}
	for _, p := range c.allow {
		if matches(p.Action, action) && matches(p.Resource, resource) {
			return nil
		}
	}
	return ErrForbidden
}

func matches(rule, value string) bool {
	return rule == "*" || rule == value
}

// MePermissions returns the authenticated user's effective RBAC grants so the
// client can render permission-aware navigation. Platform-scoped users carry a
// wildcard allow plus the platform flag.
type MePermissions struct {
	Platform    bool               `json:"platform"`
	Permissions []model.Permission `json:"permissions"`
}

func (s *Service) MePermissions(ctx context.Context, userID string) (*MePermissions, error) {
	ac, err := s.authz(ctx, userID)
	if err != nil {
		return nil, err
	}
	perms := make([]model.Permission, 0, len(ac.allow)+len(ac.deny)+1)
	perms = append(perms, ac.allow...)
	perms = append(perms, ac.deny...)
	return &MePermissions{Platform: ac.platform, Permissions: perms}, nil
}
