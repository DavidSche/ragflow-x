package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ResolveContext returns the object-scope attributes (tenant/project/owner) for
// a request so ABAC can be evaluated on the concrete resource.
type ResolveContext func(c *gin.Context) (tenantID, projectID, ownerID string, err error)

const RouteClassGovernance = "GOVERNANCE"

// ContextTenantScope carries the server-resolved governance scope from RBAC to
// the handler layer. Handlers must consume this value instead of resolving and
// auditing the same request twice.
const ContextTenantScope = "tenant_scope"

// ContextGovernanceTarget is set by RBAC when the request body names a target
// workspace. It is advisory context for handlers; authority is always resolved
// server-side before any service call.
const ContextGovernanceTarget = "governance_target_tenant_id"

// Rule binds an HTTP method+route to an (action, resource) permission and an
// optional ABAC scope resolver.
type Rule struct {
	Method                   string
	Path                     string
	Action                   string
	Resource                 string
	RouteClass               string
	ResourceScope            string
	AllowedScope             []string
	SupportsCrossTenantWrite bool
	RequiresApproval         bool
	ActingContextRequired    bool
	AuditRequired            bool
	Resolve                  ResolveContext
}

// RBAC enforces permissions at the middleware layer for every matching route,
// complementing the service-layer checks (defense in depth). This middleware is
// applied to the authenticated route group only; unregistered routes inside
// that group are denied by default so a new endpoint can never silently bypass
// authorization, and a failing scope resolver fails closed.
func RBAC(svc *service.Service, rules []Rule) gin.HandlerFunc {
	byKey := map[string]Rule{}
	for _, r := range rules {
		byKey[r.Method+" "+r.Path] = r
	}
	return func(c *gin.Context) {
		rule, ok := byKey[c.Request.Method+" "+c.FullPath()]
		if !ok {
			// Default-deny: a protected route without a registered access rule
			// is treated as forbidden rather than skipped.
			response.Err(c, httperr.Forbidden("forbidden"))
			c.Abort()
			return
		}
		actorID := c.GetString(ContextUserID)
		tenantID := c.GetString(ContextTenantID)
		projectID, ownerID := "", ""
		if rule.Resolve != nil {
			rt, rp, ro, err := rule.Resolve(c)
			if err != nil {
				// Fail closed: if object scope cannot be resolved we must not
				// degrade access to a less constrained check.
				response.Err(c, httperr.Internal("scope resolution failed"))
				c.Abort()
				return
			}
			if rt != "" {
				tenantID = rt
			}
			projectID, ownerID = rp, ro
		}
		if err := svc.AuthorizeABAC(c.Request.Context(), actorID, rule.Action, rule.Resource, tenantID, projectID, ownerID); err != nil {
			response.Err(c, err)
			c.Abort()
			return
		}
		if rule.ActingContextRequired && rule.SupportsCrossTenantWrite && c.Request.Method != http.MethodGet {
			targetTenantID, hasTarget, err := governanceTargetTenant(c)
			if err != nil {
				response.Fail(c, http.StatusBadRequest, 400, "invalid governance target")
				c.Abort()
				return
			}
			if hasTarget && targetTenantID != c.GetString(ContextTenantID) {
				if !rule.RequiresApproval {
					response.Fail(c, http.StatusForbidden, 403, "cross-tenant write requires governed approval")
					c.Abort()
					return
				}
				if err := svc.EnsureGovernanceApprovalEnabled(c.Request.Context()); err != nil {
					response.Err(c, err)
					c.Abort()
					return
				}
				if err := svc.EnsureGovernanceActingContextTarget(c.Request.Context(), actorID, c.GetString(ContextTenantID), targetTenantID); err != nil {
					response.Err(c, err)
					c.Abort()
					return
				}
			}
			c.Set(ContextGovernanceTarget, targetTenantID)
		}
		if len(rule.AllowedScope) > 0 {
			requestedScope, requestedTenantID := c.Query("scope"), c.Query("tenant_id")
			if requestedScope == "" && requestedTenantID == "" {
				requestedScope = "current"
			}
			allowed := false
			for _, scope := range rule.AllowedScope {
				if scopeValueMatches(scope, requestedScope) {
					allowed = true
					break
				}
			}
			if !allowed {
				response.Fail(c, http.StatusBadRequest, 400, "requested scope is not allowed")
				c.Abort()
				return
			}
			if requestedScope != "current" {
				if _, err := svc.ResolveTenantScope(c.Request.Context(), actorID, tenantID, requestedScope, requestedTenantID); err != nil {
					response.Err(c, err)
					c.Abort()
					return
				}
			}
		}
		if rule.AuditRequired && c.Request.Method == http.MethodGet {
			scope, err := svc.ResolveTenantScope(c.Request.Context(), actorID, c.GetString(ContextTenantID), c.Query("scope"), c.Query("tenant_id"))
			if err != nil {
				response.Err(c, err)
				c.Abort()
				return
			}
			if scope.ScopeAll() || scope.Kind == service.TenantScopeSpecific {
				entry := &model.AuditLog{
					TenantID:   c.GetString(ContextTenantID),
					UserID:     actorID,
					Action:     rule.Resource + ".governance.read",
					Resource:   rule.Resource,
					ResourceID: c.Query("tenant_id"),
					IP:         c.ClientIP(),
					TraceID:    c.GetString("request_id"),
				}
				entry.Scope = string(scope.Kind)
				entry.TargetTenantID = scope.TargetTenantID
				entry.AuthorizationPermission = "governance.read:tenant"
				entry.AuthorizationPolicyVersion = "explicit-rbac-v1"
				if err := svc.RecordAudit(c.Request.Context(), entry); err != nil {
					response.Err(c, err)
					c.Abort()
					return
				}
			}
			c.Set(ContextTenantScope, scope)
		}
		c.Next()
	}
}

func scopeValueMatches(registered, requested string) bool {
	registered, requested = strings.ToLower(strings.TrimSpace(registered)), strings.ToLower(strings.TrimSpace(requested))
	switch registered {
	case "current":
		return requested == "current"
	case "specific":
		return requested == "specific"
	case "all_authorized":
		return requested == "all"
	default:
		return false
	}
}

func governanceTargetTenant(c *gin.Context) (string, bool, error) {
	if target := strings.TrimSpace(c.Query("target_tenant_id")); target != "" {
		return target, true, nil
	}
	if tenantID := strings.TrimSpace(c.Query("tenant_id")); tenantID != "" {
		return tenantID, true, nil
	}
	if !strings.Contains(c.ContentType(), "application/json") || c.Request.Body == nil {
		return "", false, nil
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		return "", false, err
	}
	if err := c.Request.Body.Close(); err != nil {
		return "", false, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", false, nil
	}
	var body struct {
		TargetTenantID string `json:"target_tenant_id"`
		TenantID       string `json:"tenant_id"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", false, err
	}
	if target := strings.TrimSpace(body.TargetTenantID); target != "" {
		return target, true, nil
	}
	if tenantID := strings.TrimSpace(body.TenantID); tenantID != "" {
		return tenantID, true, nil
	}
	return "", false, nil
}
