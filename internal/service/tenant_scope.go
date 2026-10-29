package service

import (
	"context"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// TenantScopeKind is the server-authorized scope produced by the resolver.
type TenantScopeKind string

const (
	TenantScopeCurrent       TenantScopeKind = "CURRENT"
	TenantScopeSpecific      TenantScopeKind = "SPECIFIC"
	TenantScopeAllAuthorized TenantScopeKind = "ALL_AUTHORIZED"
	TenantScopeValueCurrent  string          = "current"
	TenantScopeValueSpecific string          = "specific"
	TenantScopeValueAll      string          = "all"
)

// TenantScope is immutable after resolution. Handlers may pass it to service
// methods, but must never reinterpret raw client scope or tenant parameters.
type TenantScope struct {
	Kind             TenantScopeKind
	ActorTenantID    string
	TargetTenantID   string
	AllowedTenantIDs []string
	// PermittedBy records the permission that authorized a cross-workspace
	// scope ("governance.read:tenant" or a fine-grained platform capability), so
	// audit entries can name the real reason instead of a hardcoded label
	// (doc/248). It is empty for CURRENT scopes, which need no cross-tenant
	// permission.
	PermittedBy string
}

func (s TenantScope) CurrentTenantID() string {
	switch s.Kind {
	case TenantScopeSpecific:
		return s.TargetTenantID
	case TenantScopeAllAuthorized:
		return ""
	default:
		return s.ActorTenantID
	}
}

func (s TenantScope) ScopeAll() bool { return s.Kind == TenantScopeAllAuthorized }

// ContainsTenant determines whether an explicit target belongs to the resolved
// authorization boundary. All-authorized scopes still use their expanded set.
func (s TenantScope) ContainsTenant(tenantID string) bool {
	if tenantID == "" {
		return false
	}
	for _, allowedID := range s.AllowedTenantIDs {
		if allowedID == tenantID {
			return true
		}
	}
	return false
}

// RepositoryTenantIDs keeps an explicit set for scoped reads while allowing
// all-authorized reads to use the repository's all-scope branch instead of a
// potentially very long SQL IN list.
func (s TenantScope) RepositoryTenantIDs() []string {
	if s.ScopeAll() {
		return nil
	}
	return s.AllowedTenantIDs
}

// ResolveTenantScope turns a client request into the only tenant scope the
// service layer may consume. It never grants authority from tenant_id, scope,
// or role alone: platform capability is resolved from authoritative RBAC.
func (s *Service) ResolveTenantScope(ctx context.Context, userID, actorTenantID, requestedScope, requestedTenantID string) (TenantScope, error) {
	return s.ResolveTenantScopeFor(ctx, userID, actorTenantID, requestedScope, requestedTenantID, "")
}

// ResolveTenantScopeFor is ResolveTenantScope with a governance capability
// ("audit", "usage", ...). A caller that owns a dedicated platform capability
// lets a delegated auditor read across workspaces without handing over the whole
// governance.read surface; every other capability keeps the previous behaviour
// (doc/248).
func (s *Service) ResolveTenantScopeFor(ctx context.Context, userID, actorTenantID, requestedScope, requestedTenantID, capability string) (TenantScope, error) {
	authzCtx, err := s.authz(ctx, userID)
	if err != nil {
		return TenantScope{}, err
	}
	if strings.TrimSpace(actorTenantID) == "" {
		return TenantScope{}, httperr.BadRequest(400, "actor tenant is required")
	}

	scope := strings.ToLower(strings.TrimSpace(requestedScope))
	if scope == "" {
		scope = TenantScopeValueCurrent
	}
	switch scope {
	case TenantScopeValueCurrent:
		if strings.TrimSpace(requestedTenantID) != "" {
			return TenantScope{}, httperr.BadRequest(400, "tenant_id requires scope=specific")
		}
		return TenantScope{Kind: TenantScopeCurrent, ActorTenantID: actorTenantID, TargetTenantID: actorTenantID, AllowedTenantIDs: []string{actorTenantID}}, nil
	case TenantScopeValueAll:
		ref, ok := authzCtx.matchAny(GovernanceReadRefs(capability))
		if !ok {
			return TenantScope{}, ErrForbidden
		}
		if strings.TrimSpace(requestedTenantID) != "" {
			return TenantScope{}, httperr.BadRequest(400, "scope=all cannot include tenant_id")
		}
		tenants, err := s.Store.ListAllTenants(ctx)
		if err != nil {
			return TenantScope{}, err
		}
		allowed := make([]string, 0, len(tenants))
		for _, tenant := range tenants {
			allowed = append(allowed, tenant.ID)
		}
		return TenantScope{
			Kind: TenantScopeAllAuthorized, ActorTenantID: actorTenantID, AllowedTenantIDs: allowed,
			PermittedBy: ref.String(),
		}, nil
	case TenantScopeValueSpecific:
		ref, ok := authzCtx.matchAny(GovernanceReadRefs(capability))
		if !ok {
			return TenantScope{}, ErrForbidden
		}
		target := strings.TrimSpace(requestedTenantID)
		if target == "" {
			return TenantScope{}, httperr.BadRequest(400, "scope=specific requires tenant_id")
		}
		tenant, err := s.Store.GetTenant(ctx, target)
		if err != nil {
			return TenantScope{}, err
		}
		if tenant == nil {
			return TenantScope{}, httperr.BadRequest(400, "target tenant not found")
		}
		return TenantScope{
			Kind: TenantScopeSpecific, ActorTenantID: actorTenantID, TargetTenantID: target,
			AllowedTenantIDs: []string{target}, PermittedBy: ref.String(),
		}, nil
	default:
		return TenantScope{}, httperr.BadRequest(400, "scope must be current, specific or all")
	}
}
