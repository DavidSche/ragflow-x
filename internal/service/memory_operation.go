package service

import (
	"context"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func isValidMemoryOperationState(state string) bool {
	switch state {
	case model.MemoryOperationAccepted, model.MemoryOperationAdmitted,
		model.MemoryOperationDispatched, model.MemoryOperationExecuted,
		model.MemoryOperationFailed, model.MemoryOperationUnknown,
		model.MemoryOperationBlocked, model.MemoryOperationSkipped,
		model.MemoryOperationReconciled:
		return true
	default:
		return false
	}
}

func sanitizeMemoryOperation(operation model.MemoryOperation) model.MemoryOperation {
	operation.Detail = ""
	return operation
}

func (s *Service) memoryOperationScope(
	ctx context.Context, tenantID, actorID string,
) (tenantScoped, ownScoped, teamScoped bool, projectIDs []string, err error) {
	roles, err := s.Store.ListRolesByUser(ctx, actorID)
	if err != nil {
		return false, false, false, nil, err
	}
	for _, role := range roles {
		if role.ID == model.RolePlatformAdmin || role.ID == model.RoleTenantAdmin ||
			role.ID == model.RoleOperator || role.ID == model.RoleViewer {
			return true, false, false, nil, nil
		}
	}
	if hasRoleID(roles, model.RoleTeamAdmin) {
		projectIDs, scoped, err := s.teamAdminOwnedProjectIDs(ctx, tenantID, actorID)
		if err != nil {
			return false, false, false, nil, err
		}
		return false, false, scoped, projectIDs, nil
	}
	if hasRoleID(roles, model.RoleBusinessUser) {
		return false, true, false, nil, nil
	}
	return false, false, false, nil, httperr.Forbidden("memory operation access is not scoped")
}

func (s *Service) ListMemoryOperations(
	ctx context.Context, tenantID, actorID string,
	filter repository.MemoryOperationFilter, page, pageSize int,
) ([]model.MemoryOperation, int64, error) {
	tenantScoped, ownScoped, teamScoped, projectIDs, err := s.memoryOperationScope(ctx, tenantID, actorID)
	if err != nil {
		return nil, 0, err
	}
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.ProjectIDs = nil
	filter.UserID = strings.TrimSpace(filter.UserID)
	filter.AssistantID = strings.TrimSpace(filter.AssistantID)
	filter.MemoryID = strings.TrimSpace(filter.MemoryID)
	filter.Operation = strings.TrimSpace(filter.Operation)
	if filter.State != "" && !isValidMemoryOperationState(filter.State) {
		return nil, 0, httperr.BadRequest(40141, "state is invalid")
	}
	switch {
	case tenantScoped:
	case teamScoped:
		if filter.ProjectID != "" && !containsString(projectIDs, filter.ProjectID) {
			return nil, 0, httperr.Forbidden("project is outside team scope")
		}
		filter.ProjectIDs = projectIDs
		if len(filter.ProjectIDs) == 0 {
			return []model.MemoryOperation{}, 0, nil
		}
	case ownScoped:
		filter.UserID = actorID
	default:
		return nil, 0, httperr.Forbidden("memory operation access is not scoped")
	}
	items, total, err := s.Store.ListMemoryOperations(ctx, tenantID, filter, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	for index := range items {
		items[index] = sanitizeMemoryOperation(items[index])
	}
	return items, total, nil
}

func (s *Service) GetMemoryOperation(
	ctx context.Context, tenantID, actorID, operationID string,
) (*model.MemoryOperation, error) {
	operation, err := s.Store.GetMemoryOperation(ctx, tenantID, operationID)
	if err != nil {
		return nil, err
	}
	if operation == nil {
		return nil, httperr.NotFound("memory operation not found")
	}
	tenantScoped, ownScoped, teamScoped, projectIDs, err := s.memoryOperationScope(ctx, tenantID, actorID)
	if err != nil {
		return nil, err
	}
	switch {
	case tenantScoped:
	case teamScoped:
		if !containsString(projectIDs, operation.ProjectID) {
			return nil, httperr.NotFound("memory operation not found")
		}
	case ownScoped:
		if operation.UserID != actorID {
			return nil, httperr.NotFound("memory operation not found")
		}
	default:
		return nil, httperr.Forbidden("memory operation access is not scoped")
	}
	sanitized := sanitizeMemoryOperation(*operation)
	return &sanitized, nil
}
