package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// CreateRoleRequest carries a new RBAC role.
type CreateRoleRequest struct {
	Name        string
	Scope       string
	Description string
	ParentID    string
}

// PermissionSpec is a single grant/deny rule.
type PermissionSpec struct {
	Action   string
	Resource string
	Effect   string
}

var roleScopes = map[string]bool{
	model.RoleScopePlatform: true,
	model.RoleScopeTenant:   true,
	"project":               true,
}

// requireRoleScopeAccess loads a role and denies non-platform users access to
// platform-scoped roles such as the platform_admin builtin, so a tenant admin
// can never read, edit, or configure platform role permissions.
func (s *Service) requireRoleScopeAccess(ctx context.Context, actorID, roleID string) (*model.Role, error) {
	r, err := s.Store.GetRole(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, httperr.NotFound("role not found")
	}
	if r.Scope == model.RoleScopePlatform && s.Authorize(ctx, actorID, "governance.manage", "tenant") != nil {
		return nil, ErrForbidden
	}
	return r, nil
}

// CreateRole creates a role. Only platform admins may create platform-scoped
// roles; others need role management permission.
func (s *Service) CreateRole(ctx context.Context, actorID, actorRole string, req CreateRoleRequest) (*model.Role, error) {
	name, err := normalizeDisplayName(req.Name, "role name is required", 40080)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.GetRoleByName(ctx, name, "")
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("role")
	}
	if !roleScopes[req.Scope] {
		return nil, httperr.BadRequest(40081, "invalid role scope")
	}
	if req.Scope == model.RoleScopePlatform && s.Authorize(ctx, actorID, "governance.manage", "tenant") != nil {
		return nil, ErrForbidden
	}
	if err := s.Authorize(ctx, actorID, "manage", "role"); err != nil {
		return nil, err
	}
	r := &model.Role{
		ID: id.New(), Name: name, Scope: req.Scope,
		Description: req.Description, ParentID: req.ParentID,
	}
	if err := s.Store.CreateRole(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteRole removes a role (platform/build-in roles are protected).
func (s *Service) DeleteRole(ctx context.Context, actorID, actorRole, roleID string) error {
	if err := s.Authorize(ctx, actorID, "manage", "role"); err != nil {
		return err
	}
	if _, err := s.requireRoleScopeAccess(ctx, actorID, roleID); err != nil {
		return err
	}
	if isBuiltinRole(roleID) {
		return httperr.BadRequest(40082, "built-in roles cannot be deleted")
	}
	assigned, err := s.Store.RoleUserCount(ctx, roleID)
	if err != nil {
		return err
	}
	if assigned > 0 {
		return httperr.BadRequest(40083, "role is assigned to users and cannot be deleted")
	}
	children, err := s.Store.RoleAsParentCount(ctx, roleID)
	if err != nil {
		return err
	}
	if children > 0 {
		return httperr.BadRequest(40084, "role is a parent of other roles and cannot be deleted")
	}
	return s.Store.DeleteRole(ctx, roleID)
}

// UpdateRoleRequest carries editable role fields.
type UpdateRoleRequest struct {
	Name        string
	Scope       string
	Description string
	ParentID    string
}

// UpdateRole edits a role name/description/scope/parent.
func (s *Service) UpdateRole(ctx context.Context, actorID, actorRole, roleID string, req UpdateRoleRequest) (*model.Role, error) {
	if err := s.Authorize(ctx, actorID, "manage", "role"); err != nil {
		return nil, err
	}
	r, err := s.requireRoleScopeAccess(ctx, actorID, roleID)
	if err != nil {
		return nil, err
	}
	if isBuiltinRole(r.ID) {
		return nil, httperr.BadRequest(40082, "built-in roles cannot be modified")
	}
	if req.Scope != "" {
		if !roleScopes[req.Scope] {
			return nil, httperr.BadRequest(40081, "invalid role scope")
		}
		if req.Scope == model.RoleScopePlatform && s.Authorize(ctx, actorID, "governance.manage", "tenant") != nil {
			return nil, ErrForbidden
		}
		if isBuiltinRole(roleID) && req.Scope != r.Scope {
			return nil, httperr.BadRequest(40082, "cannot change scope of built-in roles")
		}
		r.Scope = req.Scope
	}
	if req.Name != "" {
		name, err := normalizeDisplayName(req.Name, "role name is required", 40080)
		if err != nil {
			return nil, err
		}
		existing, err := s.Store.GetRoleByName(ctx, name, roleID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, displayNameConflict("role")
		}
		r.Name = name
	}
	if req.Description != "" {
		r.Description = req.Description
	}
	if req.ParentID != "" {
		if req.ParentID == roleID {
			return nil, httperr.BadRequest(40085, "role cannot be its own parent")
		}
		parent, perr := s.Store.GetRole(ctx, req.ParentID)
		if perr != nil {
			return nil, perr
		}
		if parent == nil {
			return nil, httperr.BadRequest(40086, "parent role not found")
		}
		r.ParentID = req.ParentID
	}
	if err := s.Store.UpdateRole(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// GetRolePermissions returns a role's policy rules.
func (s *Service) GetRolePermissions(ctx context.Context, actorID, actorRole, roleID string) ([]model.Permission, error) {
	if err := s.Authorize(ctx, actorID, "read", "role"); err != nil {
		return nil, err
	}
	if _, err := s.requireRoleScopeAccess(ctx, actorID, roleID); err != nil {
		return nil, err
	}
	return s.Store.GetPermissions(ctx, roleID)
}

// SetRolePermissions replaces a role's policy rules.
func (s *Service) SetRolePermissions(ctx context.Context, actorID, actorRole, roleID string, specs []PermissionSpec) error {
	if err := s.Authorize(ctx, actorID, "manage", "role"); err != nil {
		return err
	}
	if _, err := s.requireRoleScopeAccess(ctx, actorID, roleID); err != nil {
		return err
	}
	if isBuiltinRole(roleID) {
		return httperr.BadRequest(40082, "built-in role permissions cannot be changed")
	}
	perms := make([]model.Permission, 0, len(specs))
	for _, sp := range specs {
		if !isCatalogPermission(sp.Resource, sp.Action) {
			return httperr.BadRequest(40087, "permission is outside the permission catalog")
		}
		effect := sp.Effect
		if effect != model.PermissionEffectAllow && effect != model.PermissionEffectDeny {
			effect = model.PermissionEffectAllow
		}
		perms = append(perms, model.Permission{ID: id.New(), RoleID: roleID, Action: sp.Action, Resource: sp.Resource, Effect: effect})
	}
	return s.Store.SetPermissions(ctx, roleID, perms)
}

func isCatalogPermission(resource, action string) bool {
	for _, entry := range db.PermissionCatalog() {
		if entry.Resource != resource {
			continue
		}
		for _, catalogAction := range entry.Actions {
			if catalogAction == action {
				return true
			}
		}
	}
	return false
}

func isBuiltinRole(roleID string) bool {
	return roleID == model.RolePlatformAdmin || roleID == model.RoleTenantAdmin ||
		roleID == model.RoleOperator || roleID == model.RoleViewer ||
		roleID == model.RoleTeamAdmin || roleID == model.RoleBusinessUser
}
