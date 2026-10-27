package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func memoryDispatchIdentity(tenantID, userID string) (string, string) {
	return repository.MemoryDispatchIdentity(tenantID, userID)
}

func (s *Service) resolveMemoryDispatchPrincipal(
	ctx context.Context, tenantID, userID string,
) (*model.User, string, string, error) {
	principal, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		obs.Get().AddMemoryPrincipalMapping("failure", "lookup_error")
		return nil, "", "", err
	}
	if principal == nil || principal.TenantID != tenantID {
		obs.Get().AddMemoryPrincipalMapping("failure", "principal_not_tenant_scoped")
		return nil, "", "", httperr.Forbidden("memory dispatch principal is not tenant scoped")
	}
	if principal.Status != model.UserStatusActive {
		obs.Get().AddMemoryPrincipalMapping("failure", "principal_not_active")
		return nil, "", "", httperr.New(409, 40956, "memory dispatch principal is not active")
	}
	authPrincipalID, attributionUserID := memoryDispatchIdentity(tenantID, principal.ID)
	obs.Get().AddMemoryPrincipalMapping("success", "")
	return principal, authPrincipalID, attributionUserID, nil
}
