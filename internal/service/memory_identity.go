package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
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
		return nil, "", "", err
	}
	if principal == nil || principal.TenantID != tenantID {
		return nil, "", "", httperr.Forbidden("memory dispatch principal is not tenant scoped")
	}
	if principal.Status != model.UserStatusActive {
		return nil, "", "", httperr.New(409, 40956, "memory dispatch principal is not active")
	}
	authPrincipalID, attributionUserID := memoryDispatchIdentity(tenantID, principal.ID)
	return principal, authPrincipalID, attributionUserID, nil
}
