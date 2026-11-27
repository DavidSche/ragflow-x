package service

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/password"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

var emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

func validEmail(email string) bool {
	return emailPattern.MatchString(email)
}

// CreateUserRequest carries a new tenant user's attributes.
type CreateUserRequest struct {
	Username string
	Password string
	Email    string
	Role     string
}

// guardRoleMutation blocks role changes that would remove the last active
// admin of a scope and protects platform admins from demotion, so a tenant
// can never be left without a tenant admin.
func (s *Service) guardRoleMutation(ctx context.Context, u *model.User, nextRole string) error {
	if u.Role == model.RolePlatformAdmin {
		return httperr.New(409, 40904, "platform admin users cannot be changed")
	}
	if u.Role == model.RoleTenantAdmin && nextRole != model.RoleTenantAdmin {
		n, err := s.Store.CountActiveUsersByTenantAndRole(ctx, u.TenantID, model.RoleTenantAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return httperr.New(409, 40905, "cannot remove the last tenant admin")
		}
	}
	return nil
}

// tenantRoles is the set of roles assignable to tenant users.
// validateRole ensures a role exists, that a platform role is only handed out by
// a platform admin, and that the role is not owned by another tenant. The last
// check arrived with doc/304 §4 E175: before it, a workspace admin could assign
// a role another workspace had authored, which is both a cross-tenant read of
// that role and a way to hand members whatever it grants. The refusal reuses the
// same code and message as "no such role" on purpose — distinguishing the two
// would tell the caller which role IDs exist elsewhere.
//
// `targetTenantID` is the tenant of the account that will *hold* the role, not
// the tenant of the actor. Callers used to pass the actor's tenant, which was
// equivalent for a workspace admin (they may only touch their own members) but
// not for a platform admin: assigning tenant A's custom role to a member of
// tenant B passed here, and the resulting account was then refused by `authz`
// with 40305 on every single request (doc/315 review). The tenant of the holder
// is the only one that decides whether the pair is coherent, so the platform
// bypass is gone as well — a platform identity may hand out platform-scoped and
// shared roles, never another tenant's private one.
func (s *Service) validateRole(ctx context.Context, targetTenantID, actorRole, roleID string) error {
	r, err := s.Store.GetRole(ctx, roleID)
	if err != nil {
		return err
	}
	if r == nil {
		return httperr.BadRequest(40021, "role is not assignable")
	}
	// A quarantined role is not a shared role any more: it is waiting for a
	// platform admin to say who authored it (doc/304 §4 E178). Refusing the
	// assignment for every actor keeps a legacy row out of new hands without
	// revoking what existing holders already have.
	if roleDisabled(r) {
		return httperr.BadRequest(40021, "role is not assignable")
	}
	// platform roles may only be assigned by platform admins
	if r.Scope == model.RoleScopePlatform && actorRole != model.RolePlatformAdmin {
		return httperr.BadRequest(40021, "platform role requires a platform admin")
	}
	if !roleVisibleToTenant(r, targetTenantID) {
		return httperr.BadRequest(40021, "role is not assignable")
	}
	return nil
}

// CreateUser creates a user scoped to a tenant with a validated role.
func (s *Service) CreateUser(ctx context.Context, tenantID, actorRole string, req CreateUserRequest) (*model.User, error) {
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if req.Username == "" || req.Password == "" {
		return nil, httperr.BadRequest(40020, "username and password are required")
	}
	if utf8.RuneCountInString(req.Username) > 128 {
		return nil, httperr.BadRequest(40020, "username is too long")
	}
	if utf8.RuneCountInString(req.Email) > 255 {
		return nil, httperr.BadRequest(40023, "email is too long")
	}
	if req.Email != "" && !validEmail(req.Email) {
		return nil, httperr.BadRequest(40023, "invalid email address")
	}
	if len(req.Password) < 8 {
		return nil, httperr.BadRequest(40024, "password must be at least 8 characters")
	}
	if err := s.validateRole(ctx, tenantID, actorRole, req.Role); err != nil {
		return nil, err
	}
	if err := s.EnsureTenantActive(ctx, tenantID); err != nil {
		return nil, err
	}
	existing, err := s.Store.GetUserByUsername(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, httperr.BadRequest(40022, "username already exists")
	}
	if req.Email != "" {
		existingEmail, err := s.Store.GetUserByEmail(ctx, req.Email)
		if err != nil {
			return nil, err
		}
		if existingEmail != nil {
			return nil, httperr.BadRequest(40026, "email already exists")
		}
	}
	hash, err := password.Hash(req.Password)
	if err != nil {
		return nil, err
	}
	u := &model.User{
		ID:           id.New(),
		TenantID:     tenantID,
		Username:     req.Username,
		PasswordHash: hash,
		Email:        req.Email,
		Role:         req.Role,
		Status:       model.UserStatusActive,
	}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	if err := s.Store.SetUserPrimaryRole(ctx, u.ID, u.Role); err != nil {
		return nil, err
	}
	return u, nil
}

// ListUsers returns a page of users within a tenant.
func (s *Service) ListUsers(ctx context.Context, tenantID string, page, pageSize int, filter repository.UserFilter) ([]model.User, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListUsersByTenant(ctx, tenantID, page, pageSize, filter)
}

// ListRoles returns the roles a caller may see. Filters (Name/Scope) are
// applied on the same full role set whether or not any filter is present, so
// filtering never changes which roles are reachable for a given actor.
//
// What "may see" means: a platform operator sees every row; everyone else sees
// the platform-owned rows (built-ins and platform-authored roles, `tenant_id`
// empty) plus their own tenant's. Another tenant's custom role is not part of the
// answer at all — not filtered out afterwards, but never handed to the filter
// (doc/304 §4 E175).
func (s *Service) ListRoles(ctx context.Context, actorID string, filter repository.RoleFilter) ([]model.Role, error) {
	roles, err := s.Store.ListAllRoles(ctx, filter)
	if err != nil {
		return nil, err
	}
	tenantID, platform, err := s.actorIdentity(ctx, actorID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Role, 0, len(roles))
	for i := range roles {
		r := roles[i]
		r.BuiltIn = isBuiltinRole(r.ID)
		if r.Status == "" {
			r.Status = model.RoleStatusActive
		}
		// The console needs to know, per row, whether the buttons it renders are
		// ones the server will honour: a shared role is readable everywhere but
		// writable only by the platform (doc/315 finding 1). Deriving it here
		// keeps the client from re-implementing the ownership rule.
		r.Owned = roleOwnedByTenant(&r, tenantID)
		// A built-in is never writable by anyone, not even the platform — the write
		// path answers 40082 — so it must not advertise itself as editable either.
		r.Editable = !r.BuiltIn && (platform || r.Owned)
		if !platform {
			if r.Scope == model.RoleScopePlatform {
				continue
			}
			// Quarantined legacy rows are only listed for the platform admin who
			// has to review them (E178).
			if roleDisabled(&r) {
				continue
			}
			if !roleVisibleToTenant(&r, tenantID) {
				continue
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// UserSummary is a user row enriched with the owning tenant's name.
type UserSummary struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	Status     string    `json:"status"`
	TenantID   string    `json:"tenant_id"`
	TenantName string    `json:"tenant_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// AssignRole updates a user's role. Platform admins may target any tenant;
// tenant admins may only target users within their own tenant.
func (s *Service) AssignRole(ctx context.Context, actorTenantID, actorRole, userID, role string) error {
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return httperr.NotFound("user not found")
	}
	if actorRole != model.RolePlatformAdmin && u.TenantID != actorTenantID {
		return httperr.Forbidden("cannot change role across tenants")
	}
	// Validate against the holder's tenant, after the cross-tenant check above
	// has decided whether this actor may touch this account at all.
	if err := s.validateRole(ctx, u.TenantID, actorRole, role); err != nil {
		return err
	}
	if err := s.guardRoleMutation(ctx, u, role); err != nil {
		return err
	}
	// rgx_user_role is the source of truth; User.Role is a derived marker kept
	// consistent in the same transaction.
	return s.Store.SetUserPrimaryRole(ctx, userID, role)
}

// SetUserStatus enables or disables a user.
func (s *Service) SetUserStatus(ctx context.Context, actorTenantID, actorRole, userID, status string) error {
	if status != model.UserStatusActive && status != model.UserStatusDisabled {
		return httperr.BadRequest(40025, "invalid status")
	}
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return httperr.NotFound("user not found")
	}
	if actorRole != model.RolePlatformAdmin && u.TenantID != actorTenantID {
		return httperr.Forbidden("cannot modify user across tenants")
	}
	if u.Role == model.RolePlatformAdmin {
		return httperr.New(409, 40904, "platform admin users cannot be changed")
	}
	if status == model.UserStatusDisabled && u.Role == model.RoleTenantAdmin {
		n, err := s.Store.CountActiveUsersByTenantAndRole(ctx, u.TenantID, model.RoleTenantAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return httperr.New(409, 40905, "cannot disable the last tenant admin")
		}
	}
	u.Status = status
	return s.Store.UpdateUser(ctx, u)
}

// UpdateUserProfile edits a user's username/email/role and optionally resets
// the password. A blank field leaves the corresponding attribute unchanged.
func (s *Service) UpdateUserProfile(ctx context.Context, actorTenantID, actorRole, userID, username, email, role, newPassword string) error {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return httperr.NotFound("user not found")
	}
	if actorRole != model.RolePlatformAdmin && u.TenantID != actorTenantID {
		return httperr.Forbidden("cannot modify user across tenants")
	}
	if utf8.RuneCountInString(username) > 128 {
		return httperr.BadRequest(40020, "username is too long")
	}
	if username != "" && username != u.Username {
		existing, err := s.Store.GetUserByUsername(ctx, username)
		if err != nil {
			return err
		}
		if existing != nil {
			return httperr.BadRequest(40022, "username already exists")
		}
		u.Username = username
	}
	if role != "" {
		if err := s.validateRole(ctx, u.TenantID, actorRole, role); err != nil {
			return err
		}
		if err := s.guardRoleMutation(ctx, u, role); err != nil {
			return err
		}
		u.Role = role
	}
	if email != "" {
		if !validEmail(email) {
			return httperr.BadRequest(40023, "invalid email address")
		}
		if utf8.RuneCountInString(email) > 255 {
			return httperr.BadRequest(40023, "email is too long")
		}
		existingEmail, err := s.Store.GetUserByEmail(ctx, email)
		if err != nil {
			return err
		}
		if existingEmail != nil && existingEmail.ID != userID {
			return httperr.BadRequest(40026, "email already exists")
		}
		u.Email = email
	}
	if newPassword != "" {
		if len(newPassword) < 8 {
			return httperr.BadRequest(40024, "password must be at least 8 characters")
		}
		hash, herr := password.Hash(newPassword)
		if herr != nil {
			return herr
		}
		u.PasswordHash = hash
	}
	if err := s.Store.UpdateUser(ctx, u); err != nil {
		return err
	}
	if role != "" {
		if err := s.Store.SetUserPrimaryRole(ctx, userID, role); err != nil {
			return err
		}
	}
	return nil
}

// ListAllUsers returns users across all tenants for platform admins.
func (s *Service) ListAllUsers(ctx context.Context, page, pageSize int, filter repository.UserFilter) ([]UserSummary, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	users, total, err := s.Store.ListUsers(ctx, page, pageSize, filter)
	if err != nil {
		return nil, 0, err
	}
	name := map[string]string{}
	if ts, err := s.Store.ListAllTenants(ctx); err == nil {
		for _, t := range ts {
			name[t.ID] = t.Name
		}
	}
	out := make([]UserSummary, 0, len(users))
	for _, u := range users {
		out = append(out, UserSummary{ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role, Status: u.Status, TenantID: u.TenantID, TenantName: name[u.TenantID], CreatedAt: u.CreatedAt})
	}
	return out, total, nil
}

// DeleteUser removes a user. Platform admins may delete any non-admin user;
// tenant admins may only delete users in their own tenant. A user holding the
// platform_admin role can never be deleted, so the platform can never be left
// without its last administrator.
func (s *Service) DeleteUser(ctx context.Context, actorTenantID, actorRole, userID string) error {
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return httperr.NotFound("user not found")
	}
	if u.Role == model.RolePlatformAdmin {
		return httperr.New(409, 40903, "platform admin users cannot be deleted")
	}
	if actorRole != model.RolePlatformAdmin && u.TenantID != actorTenantID {
		return httperr.Forbidden("cannot delete user across tenants")
	}
	if u.Role == model.RoleTenantAdmin && u.Status == model.UserStatusActive {
		n, err := s.Store.CountActiveUsersByTenantAndRole(ctx, u.TenantID, model.RoleTenantAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return httperr.New(409, 40905, "cannot delete the last tenant admin")
		}
	}
	return s.Store.DeleteUser(ctx, userID)
}
