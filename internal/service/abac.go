package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// AuthorizeABAC is the unified authorizer: it first enforces RBAC (role ->
// permissions with deny-override and inheritance), then applies attribute-based
// checks on resource ownership (tenant/project) and owner==current_user.
func (s *Service) AuthorizeABAC(ctx context.Context, actorID, action, resource string, objectTenantID, projectID, ownerID string) error {
	if err := s.Authorize(ctx, actorID, action, resource); err != nil {
		return err
	}
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	if ac.user.TenantID != objectTenantID {
		if s.canAccessAcrossTenant(ac, action) {
			return nil
		}
		return ErrForbidden
	}
	if projectID != "" && ac.user.TenantID == objectTenantID {
		if ac.user.Role != model.RoleTenantAdmin {
			roles, err := s.Store.ListRolesByUser(ctx, actorID)
			if err != nil {
				return err
			}
			if hasRoleID(roles, model.RoleTeamAdmin) {
				ownedBound, err := s.isProjectBoundToOwnedTeam(ctx, objectTenantID, actorID, projectID)
				if err != nil {
					return err
				}
				if !ownedBound {
					return ErrForbidden
				}
			} else {
				member, err := s.projectMember(ctx, actorID, projectID)
				if err != nil {
					return err
				}
				if !member && ownerID != actorID {
					return ErrForbidden
				}
			}
		}
	}
	if ownerID != "" && ownerID == actorID {
		return nil
	}
	return nil
}

// canAccessAcrossTenant grants explicit governance permissions a fail-closed
// cross-tenant override. It intentionally never grants read access from
// governance.manage or write access from governance.read.
func (s *Service) canAccessAcrossTenant(ac *authzContext, action string) bool {
	if !ac.platform {
		return false
	}
	switch action {
	case "read", "test":
		return ac.evaluate("governance.read", "tenant") == nil
	case "manage", "execute":
		return ac.evaluate("governance.manage", "tenant") == nil
	default:
		return false
	}
}
