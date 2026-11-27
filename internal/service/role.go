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

// roleOwnedByTenant reports whether the tenant actor owns a role row. An empty
// `tenant_id` means "owned by the platform": the built-in lattice and any role a
// platform admin authors are shared with every tenant, but only a platform admin
// may edit them (doc/304 §4 E175).
func roleOwnedByTenant(r *model.Role, tenantID string) bool {
	return tenantID != "" && r.TenantID == tenantID
}

// roleVisibleToTenant reports whether a tenant actor may at least see and assign
// a role: its own rows, plus the platform-owned ones. Visibility is deliberately
// weaker than ownership — being able to hand out a shared role must not imply
// being able to rewrite it.
func roleVisibleToTenant(r *model.Role, tenantID string) bool {
	return r.TenantID == "" || roleOwnedByTenant(r, tenantID)
}

// actorIdentity answers the two questions the role endpoints must not take from
// the token: which tenant is this actor in, and may they act platform-wide.
//
// Both answers come from `authz`, the same resolution `/auth/me` uses. The
// tenant is read from the store because a claim can be stale after a user is
// moved between tenants. The platform answer must be `ac.platform` and not
// "the actor can exercise governance.manage": a tenant-scope role may be
// granted governance.manage on purpose (doc/304 §4 E176), so the permission
// alone would turn a delegable grant into a platform identity and let its
// holder author platform-scoped roles (doc/315 finding 2).
func (s *Service) actorIdentity(ctx context.Context, actorID string) (string, bool, error) {
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return "", false, err
	}
	return ac.user.TenantID, ac.platform, nil
}

// roleDisabled reports whether a role is parked for platform review instead of
// being a shared role any tenant may hand out (doc/304 §4 E178).
//
// The empty value is treated as active on purpose: a row read from a database
// whose column was just added must not become invisible to everyone.
func roleDisabled(r *model.Role) bool {
	return r != nil && r.Status == model.RoleStatusQuarantined
}

// requireRoleWritable loads a role a tenant actor may rewrite, and reports
// whether the actor is a platform identity (which may rewrite everything).
//
// Two boundaries a tenant actor must never cross: platform-scoped rows are
// platform-only, and a role owned by another tenant does not exist for them.
//
// The second answer is 404, not 403: confirming that an id is "somebody else's
// role" already discloses what the tenant isolation is hiding (doc/304 §4 E139).
// A platform-owned row is different — it is visible to everyone by design, so
// refusing to *edit* it is an honest 403. A quarantined row is not visible to
// tenants at all, so it is 404 for them and writable by the platform, which is
// the only way to review and re-activate it (E178).
func (s *Service) requireRoleWritable(ctx context.Context, actorID, roleID string) (*model.Role, bool, error) {
	r, err := s.Store.GetRole(ctx, roleID)
	if err != nil {
		return nil, false, err
	}
	if r == nil {
		return nil, false, httperr.NotFound("role not found")
	}
	tenantID, platform, err := s.actorIdentity(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	if platform {
		return r, true, nil
	}
	if r.Scope == model.RoleScopePlatform {
		return nil, false, ErrForbidden
	}
	if roleDisabled(r) {
		return nil, false, httperr.NotFound("role not found")
	}
	if roleOwnedByTenant(r, tenantID) {
		return r, false, nil
	}
	if r.TenantID == "" {
		return nil, false, ErrForbidden
	}
	return nil, false, httperr.NotFound("role not found")
}

// requireRoleReadable is the read path: reading a role's policy rules is not
// editing it. A tenant admin must be able to inspect the built-ins it can hand
// out, otherwise the permission dialog of every shared role answers 403 and the
// console shows an error where it should show a read-only matrix (doc/315
// finding 1). Writes keep the ownership gate above.
func (s *Service) requireRoleReadable(ctx context.Context, actorID, roleID string) (*model.Role, error) {
	r, err := s.Store.GetRole(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, httperr.NotFound("role not found")
	}
	tenantID, platform, err := s.actorIdentity(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if platform {
		return r, nil
	}
	if r.Scope == model.RoleScopePlatform {
		return nil, ErrForbidden
	}
	if roleDisabled(r) {
		return nil, httperr.NotFound("role not found")
	}
	if !roleVisibleToTenant(r, tenantID) {
		return nil, httperr.NotFound("role not found")
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
	if !roleScopes[req.Scope] {
		return nil, httperr.BadRequest(40081, "invalid role scope")
	}
	tenantID, platform, err := s.actorIdentity(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if req.Scope == model.RoleScopePlatform && !platform {
		return nil, ErrForbidden
	}
	if err := s.Authorize(ctx, actorID, "manage", "role"); err != nil {
		return nil, err
	}
	// Ownership is what keeps one tenant's custom roles out of every other
	// tenant's list, editor and assignment picker (doc/304 §4 E175). A role the
	// platform authors stays shared, exactly like the built-ins it sits next to.
	owner := ""
	if req.Scope != model.RoleScopePlatform && !platform {
		owner = tenantID
	}
	existing, err := s.Store.GetRoleByName(ctx, name, "", owner)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("role")
	}
	if err := s.validateRoleParent(ctx, actorID, owner, "", req.Scope, req.ParentID); err != nil {
		return nil, err
	}
	// `status` is set here rather than left to the column default: the response of
	// POST /roles is read by the console, and a row whose status is "" would render
	// as neither active nor parked while GET /roles reports "active" for it.
	r := &model.Role{
		ID: id.New(), TenantID: owner, Name: name, Scope: req.Scope,
		Description: req.Description, ParentID: req.ParentID,
		Status: model.RoleStatusActive,
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
	if _, _, err := s.requireRoleWritable(ctx, actorID, roleID); err != nil {
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

// UpdateRoleRequest carries editable role fields. Status is platform-only and
// nil means "leave it alone"; it exists so a quarantined legacy role can be
// re-authorised instead of staying dark forever (E178).
type UpdateRoleRequest struct {
	Name        string
	Scope       string
	Description string
	ParentID    string
	Status      *string
}

// UpdateRole edits a role name/description/scope/parent.
func (s *Service) UpdateRole(ctx context.Context, actorID, actorRole, roleID string, req UpdateRoleRequest) (*model.Role, error) {
	if err := s.Authorize(ctx, actorID, "manage", "role"); err != nil {
		return nil, err
	}
	r, platform, err := s.requireRoleWritable(ctx, actorID, roleID)
	if err != nil {
		return nil, err
	}
	if isBuiltinRole(r.ID) {
		return nil, httperr.BadRequest(40082, "built-in roles cannot be modified")
	}
	scopeChanged := false
	if req.Scope != "" && req.Scope != r.Scope {
		if !roleScopes[req.Scope] {
			return nil, httperr.BadRequest(40081, "invalid role scope")
		}
		if req.Scope == model.RoleScopePlatform && !platform {
			return nil, ErrForbidden
		}
		if isBuiltinRole(roleID) {
			return nil, httperr.BadRequest(40082, "cannot change scope of built-in roles")
		}
		// Every child of this role must sit in the same scope (E175), so changing
		// the scope of a parent would leave edges `expandRoles` has to refuse.
		// Refuse here instead of invalidating children in the background.
		children, err := s.Store.RoleAsParentCount(ctx, roleID)
		if err != nil {
			return nil, err
		}
		if children > 0 {
			return nil, httperr.BadRequest(httperr.CodeRoleScopeChangeWithChildren, "role has child roles; change their parent first")
		}
		r.Scope = req.Scope
		scopeChanged = true
		if r.Scope == model.RoleScopePlatform {
			r.TenantID = ""
		}
	}
	if req.Name != "" {
		name, err := normalizeDisplayName(req.Name, "role name is required", 40080)
		if err != nil {
			return nil, err
		}
		existing, err := s.Store.GetRoleByName(ctx, name, roleID, r.TenantID)
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
		if err := s.validateRoleParent(ctx, actorID, r.TenantID, roleID, r.Scope, req.ParentID); err != nil {
			return nil, err
		}
		r.ParentID = req.ParentID
	} else if scopeChanged && r.ParentID != "" {
		// Changing the scope re-checks the parent that is already there. The
		// create path validates the pair, but until doc/315 finding 3 the update
		// path did not: a tenant role could become scope=platform while keeping
		// parent_id=tenant_admin, and the next reader had to refuse the row it
		// had just been allowed to write (E177).
		if err := s.validateRoleParent(ctx, actorID, r.TenantID, roleID, r.Scope, r.ParentID); err != nil {
			return nil, err
		}
	}
	if req.Status != nil {
		if !platform {
			return nil, ErrForbidden
		}
		switch *req.Status {
		case model.RoleStatusActive, model.RoleStatusQuarantined:
			r.Status = *req.Status
		default:
			return nil, httperr.BadRequest(40000, "invalid role status")
		}
		// Bringing a role back into service re-checks its parent: re-activating a
		// child of a role that is itself quarantined would recreate exactly the
		// inheritance edge `validateRoleParent` refuses to create today.
		if r.Status == model.RoleStatusActive && r.ParentID != "" {
			if err := s.validateRoleParent(ctx, actorID, r.TenantID, roleID, r.Scope, r.ParentID); err != nil {
				return nil, err
			}
		}
	}
	if err := s.Store.UpdateRole(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// validateRoleParent refuses a parent that does not exist, that would make the
// role its own ancestor, that sits in another scope or tenant, or that grants
// something the actor could not hand out themselves.
//
// Until doc/304 §4 E175 this checked existence and cycles only. Because `authz`
// unions every ancestor's rules and treated *any* platform-scoped role in the
// closure as a platform identity, `parent_id = platform_admin` on a
// tenant-scoped role was a one-field platform takeover: `validateRole` saw a
// tenant role, `expandRoles` walked into the platform one. The three checks
// below close the write path; `expandRoles` re-checks the same invariant when
// reading, so a row written before the fix cannot keep working.
//
// A deeper ancestor that no longer exists only ends the walk: that hole is the
// pre-existing defect (E155), and refusing to edit around it would make it
// permanent.
func (s *Service) validateRoleParent(ctx context.Context, actorID, childTenantID, roleID, childScope, parentID string) error {
	if parentID == "" {
		return nil
	}
	if roleID != "" && parentID == roleID {
		return httperr.BadRequest(40085, "role cannot be its own parent")
	}
	seen := map[string]bool{parentID: true}
	inherited := make([]model.Permission, 0, 16)
	cursor := parentID
	for {
		parent, err := s.Store.GetRole(ctx, cursor)
		if err != nil {
			return err
		}
		if parent == nil {
			if cursor == parentID {
				return httperr.BadRequest(40086, "parent role not found")
			}
			break
		}
		// A quarantined role is not a shared role: it grants nothing new until a
		// platform admin reviews it (E178). Report it like any other missing
		// parent so the quarantine is not advertised to tenants.
		if roleDisabled(parent) {
			return httperr.BadRequest(40086, "parent role not found")
		}
		// A parent may be equal to the child, never wider: inheritance must not
		// move a role up the scope ladder (tenant → platform) or across tenants.
		if parent.Scope != childScope {
			return httperr.BadRequest(httperr.CodeRoleParentScopeMismatch, "parent role scope must match the role's scope")
		}
		if !roleVisibleToTenant(parent, childTenantID) {
			return httperr.BadRequest(httperr.CodeRoleParentForeignTenant, "parent role belongs to another tenant")
		}
		perms, err := s.Store.GetPermissions(ctx, parent.ID)
		if err != nil {
			return err
		}
		inherited = append(inherited, perms...)
		if parent.ParentID == "" {
			break
		}
		if roleID != "" && parent.ParentID == roleID {
			return httperr.BadRequest(httperr.CodeRoleParentCycle, "role parent would create an inheritance cycle")
		}
		if seen[parent.ParentID] {
			return httperr.BadRequest(httperr.CodeRoleParentCycle, "role parent would create an inheritance cycle")
		}
		seen[parent.ParentID] = true
		cursor = parent.ParentID
	}
	// The actor must be able to hand out everything the parent chain grants —
	// the rule `SetRolePermissions` already applies to explicit grants, now
	// applied to the inherited closure. Without it a workspace admin could attach
	// a parent holding permissions they do not have and mint them for a member.
	if len(inherited) > 0 {
		if err := s.assertCanDelegate(ctx, actorID, inherited); err != nil {
			return err
		}
	}
	return nil
}

// GetRolePermissions returns a role's policy rules.
func (s *Service) GetRolePermissions(ctx context.Context, actorID, actorRole, roleID string) ([]model.Permission, error) {
	if err := s.Authorize(ctx, actorID, "read", "role"); err != nil {
		return nil, err
	}
	if _, err := s.requireRoleReadable(ctx, actorID, roleID); err != nil {
		return nil, err
	}
	return s.Store.GetPermissions(ctx, roleID)
}

// SetRolePermissions replaces a role's policy rules.
func (s *Service) SetRolePermissions(ctx context.Context, actorID, actorRole, roleID string, specs []PermissionSpec) error {
	if err := s.Authorize(ctx, actorID, "manage", "role"); err != nil {
		return err
	}
	if _, _, err := s.requireRoleWritable(ctx, actorID, roleID); err != nil {
		return err
	}
	if isBuiltinRole(roleID) {
		return httperr.BadRequest(40082, "built-in role permissions cannot be changed")
	}
	perms := make([]model.Permission, 0, len(specs))
	for _, sp := range specs {
		if !isCatalogPermission(sp.Resource, sp.Action) {
			return httperr.BadRequest(httperr.CodeRolePermissionOutsideCatalog, "permission is outside the permission catalog")
		}
		effect := sp.Effect
		if effect != model.PermissionEffectAllow && effect != model.PermissionEffectDeny {
			effect = model.PermissionEffectAllow
		}
		perms = append(perms, model.Permission{ID: id.New(), RoleID: roleID, Action: sp.Action, Resource: sp.Resource, Effect: effect})
	}
	// "You cannot grant what you cannot exercise": without this check a
	// workspace admin holding role:manage can attach any catalog permission
	// (the catalog is the union of the builtin matrix, so it includes
	// platform-only actions) to a tenant-scoped role and then assign that role
	// to itself. The platform identity flag (ac.platform) would still be false,
	// but handlers such as ListTenants treat governance.read on tenant as a
	// full platform read, so the grant alone is the escalation (doc/247 R-24).
	if err := s.assertCanDelegate(ctx, actorID, perms); err != nil {
		return err
	}
	return s.Store.SetPermissions(ctx, roleID, perms)
}

// assertCanDelegate requires every allow-spec to be already exercisable by the
// actor. Deny-specs only narrow a role, so they are always delegable.
func (s *Service) assertCanDelegate(ctx context.Context, actorID string, perms []model.Permission) error {
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	for _, p := range perms {
		if p.Effect == model.PermissionEffectDeny {
			continue
		}
		if ac.evaluate(p.Action, p.Resource) != nil {
			return httperr.New(403, 40302, "cannot grant a permission you do not hold")
		}
	}
	return nil
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
