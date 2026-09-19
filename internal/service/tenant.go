package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// CreateTenant creates a new tenant in the operational store.
func (s *Service) CreateTenant(ctx context.Context, name string) (model.Tenant, error) {
	name, err := normalizeDisplayName(name, "tenant name is required", 40000)
	if err != nil {
		return model.Tenant{}, err
	}
	existing, err := s.Store.GetTenantByName(ctx, name)
	if err != nil {
		return model.Tenant{}, err
	}
	if existing != nil {
		return model.Tenant{}, displayNameConflict("workspace")
	}
	t := model.Tenant{
		ID:     id.New(),
		Name:   name,
		Type:   model.TenantTypeWorkspace,
		Status: model.TenantStatusActive,
	}
	if err := s.Store.CreateTenant(ctx, &t); err != nil {
		return model.Tenant{}, err
	}
	return t, nil
}

// EnsureTenantActive is the fail-closed lifecycle gate for access tokens,
// API keys, governance scopes and runtime workers.
func (s *Service) EnsureTenantActive(ctx context.Context, tenantID string) error {
	tenant, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return httperr.Forbidden("workspace is disabled")
	}
	return nil
}

// EnsureGovernanceApprovalEnabled is the route-contract runtime gate for every
// operation whose resource registry declares RequiresApproval.
func (s *Service) EnsureGovernanceApprovalEnabled(ctx context.Context) error {
	if !s.currentApprovalConfig().Enabled {
		return httperr.New(400, 40086, "approval workflow must be enabled for governed operations")
	}
	return nil
}

// EnsureGovernanceActingContextTarget validates the cross-tenant write target
// before the handler runs. Approval itself is still bound and validated by the
// ApprovalGate and Acting Context protocol.
func (s *Service) EnsureGovernanceActingContextTarget(ctx context.Context, actorID, actorTenantID, targetTenantID string) error {
	if targetTenantID == "" || targetTenantID == actorTenantID {
		return nil
	}
	if err := s.Authorize(ctx, actorID, "governance.manage", "tenant"); err != nil {
		return err
	}
	return s.EnsureTenantActive(ctx, targetTenantID)
}

// ListTenants returns a page of tenants. Non-platform callers are scoped to
// their own tenant so a tenant user can never enumerate another tenant.
func (s *Service) ListTenants(ctx context.Context, actorID, actorTenantID string, page, pageSize int, filter repository.TenantFilter) ([]model.Tenant, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	if s.Authorize(ctx, actorID, "governance.read", "tenant") == nil {
		return s.Store.ListTenants(ctx, page, pageSize, filter)
	}
	t, err := s.Store.GetTenant(ctx, actorTenantID)
	if err != nil {
		return nil, 0, err
	}
	if t == nil {
		return nil, 0, nil
	}
	if page > 1 {
		return []model.Tenant{}, 1, nil
	}
	if !tenantMatchesFilter(t, filter) {
		return []model.Tenant{}, 0, nil
	}
	return []model.Tenant{*t}, 1, nil
}

func (s *Service) ListTenantsForScope(ctx context.Context, scope TenantScope, page, pageSize int, filter repository.TenantFilter) ([]model.Tenant, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListTenantsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), page, pageSize, filter)
}

// tenantMatchesFilter applies the same filtering semantics as the repository
// layer to a single resident tenant for non-platform callers: a non-platform
// user is always scoped to their own tenant, but the requested criteria still
// decide whether it is returned.
func tenantMatchesFilter(t *model.Tenant, filter repository.TenantFilter) bool {
	if filter.Name != "" && !strings.Contains(t.Name, filter.Name) {
		return false
	}
	if filter.Status != "" && t.Status != filter.Status {
		return false
	}
	if from, ok := parseServiceDate(filter.CreatedFrom); ok && t.CreatedAt.Before(from) {
		return false
	}
	if to, ok := parseServiceDate(filter.CreatedTo); ok {
		// created_to is exclusive; date-only input runs through end of day.
		if filter.CreatedTo != to.Format(time.RFC3339) {
			to = to.AddDate(0, 0, 1)
		}
		if !t.CreatedAt.Before(to) {
			return false
		}
	}
	return true
}

// parseServiceDate mirrors repository date parsing for the in-memory tenant
// filter path (RFC3339 or YYYY-MM-DD).
func parseServiceDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts, true
	}
	if ts, err := time.Parse("2006-01-02", s); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

// UpdateTenantRequest carries editable tenant fields.
type UpdateTenantRequest struct {
	Name      string
	Status    string
	BrandName string
	BrandLogo string
}

// UpdateTenant edits a tenant. Platform admins may edit any tenant; tenant
// admins may only edit their own.
func (s *Service) UpdateTenant(ctx context.Context, actorID, actorTenantID, tenantID string, req UpdateTenantRequest) (*model.Tenant, error) {
	if tenantID != actorTenantID && s.Authorize(ctx, actorID, "governance.manage", "tenant") != nil {
		return nil, ErrForbidden
	}
	t, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, httperr.NotFound("tenant not found")
	}
	if req.Name != "" {
		name, err := normalizeDisplayName(req.Name, "tenant name is required", 40000)
		if err != nil {
			return nil, err
		}
		existing, err := s.Store.GetTenantByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if existing != nil && existing.ID != t.ID {
			return nil, displayNameConflict("workspace")
		}
		t.Name = name
	}
	if req.Status != "" {
		if req.Status != model.TenantStatusActive && req.Status != model.TenantStatusDisabled {
			return nil, httperr.BadRequest(40002, "invalid status")
		}
		t.Status = req.Status
	}
	if req.BrandName != "" {
		t.BrandName = req.BrandName
	}
	if req.BrandLogo != "" {
		t.BrandLogo = req.BrandLogo
	}
	if err := s.Store.UpdateTenant(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// BatchUpdateTenantStatus enables or disables multiple tenants in one call.
// Only platform admins may modify other tenants' states in bulk; the platform
// tenant itself is protected.
func (s *Service) BatchUpdateTenantStatus(ctx context.Context, actorID string, ids []string, status string) error {
	if s.Authorize(ctx, actorID, "governance.manage", "tenant") != nil {
		return ErrForbidden
	}
	if status != model.TenantStatusActive && status != model.TenantStatusDisabled {
		return httperr.BadRequest(40002, "invalid status")
	}
	for _, tenantID := range ids {
		if tenantID == SystemTenantID {
			return httperr.BadRequest(40003, "platform tenant cannot be modified")
		}
		t, err := s.Store.GetTenant(ctx, tenantID)
		if err != nil {
			return err
		}
		if t == nil {
			return httperr.NotFound("tenant not found")
		}
		t.Status = status
		if err := s.Store.UpdateTenant(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// ExportTenants renders all tenants as a CSV document.
func (s *Service) ExportTenants(ctx context.Context, actorID, actorTenantID string) ([]byte, error) {
	page, pageSize := 1, 200
	var all []model.Tenant
	for {
		items, total, err := s.ListTenants(ctx, actorID, actorTenantID, page, pageSize, repository.TenantFilter{})
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(all) >= int(total) {
			break
		}
		page++
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"id", "name", "status", "brand_name", "brand_logo", "created_at"})
	for _, t := range all {
		_ = w.Write([]string{t.ID, t.Name, t.Status, t.BrandName, t.BrandLogo, t.CreatedAt.Format(time.RFC3339)})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// DeleteTenant deletes a tenant. The platform tenant is protected; a tenant
// with users/teams/datasets can only be removed with force (which also
// removes its shadow users, teams, projects and dataset links).
func (s *Service) DeleteTenant(ctx context.Context, actorID, actorTenantID, tenantID string, force bool) error {
	if tenantID != actorTenantID && s.Authorize(ctx, actorID, "governance.manage", "tenant") != nil {
		return ErrForbidden
	}
	if tenantID == SystemTenantID {
		return httperr.BadRequest(40003, "platform tenant cannot be deleted")
	}
	t, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if t == nil {
		return httperr.NotFound("tenant not found")
	}
	links, err := s.Store.ListByTenant(ctx, tenantID, repository.DatasetFilter{})
	if err != nil {
		return err
	}
	teams, err := s.Store.ListTeams(ctx, tenantID, repository.TeamFilter{})
	if err != nil {
		return err
	}
	_, userTotal, err := s.Store.ListUsersByTenant(ctx, tenantID, 1, 1, repository.UserFilter{})
	if err != nil {
		return err
	}
	hasChildren := len(links) > 0 || len(teams) > 0 || userTotal > 0
	if hasChildren && !force {
		return httperr.BadRequest(40004, "tenant has users/teams/datasets; delete with force to remove them")
	}
	if force {
		s.deleteTenantChildren(ctx, tenantID)
	}
	return s.Store.DeleteTenant(ctx, tenantID)
}

func (s *Service) deleteTenantChildren(ctx context.Context, tenantID string) {
	users, _, _ := s.Store.ListUsersByTenant(ctx, tenantID, 1, 100000, repository.UserFilter{})
	for _, u := range users {
		_ = s.Store.RemoveUserRoles(ctx, u.ID)
		_ = s.Store.DeleteUser(ctx, u.ID)
	}
	if teams, err := s.Store.ListTeams(ctx, tenantID, repository.TeamFilter{}); err == nil {
		for _, team := range teams {
			_ = s.Store.DeleteTeam(ctx, tenantID, team.ID)
		}
	}
	if projects, err := s.Store.ListProjects(ctx, tenantID); err == nil {
		for _, p := range projects {
			if members, err := s.Store.ListProjectUsers(ctx, p.ID); err == nil {
				for _, m := range members {
					_ = s.Store.RemoveProjectMember(ctx, p.ID, m.ID)
				}
			}
			_ = s.Store.DeleteProject(ctx, tenantID, p.ID)
		}
	}
	if links, err := s.Store.ListByTenant(ctx, tenantID, repository.DatasetFilter{}); err == nil {
		for _, l := range links {
			_ = s.Store.DeleteDatasetLinkByTenant(ctx, tenantID, l.ID)
		}
	}
}
