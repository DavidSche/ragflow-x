package repository

import (
	"context"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// ErrRouteTxNotFound tells callers that the transactional row transition did
// not match the expected tenant/user/state.
var ErrRouteTransitionMiss = errors.New("route transition miss")

// ConversationRouteRepo owns short-lived decision/selection state and durable
// bootstrap idempotency references.
type ConversationRouteRepo interface {
	CreateRouteDecision(ctx context.Context, decision *model.RouteDecision) error
	GetRouteDecision(ctx context.Context, tenantID, userID, routeID string) (*model.RouteDecision, error)
	CreateRouteSelection(ctx context.Context, selection *model.RouteSelection) error
	GetRouteSelectionByScope(ctx context.Context, tenantID, userID, routeID, idempotencyKey string) (*model.RouteSelection, error)
	GetRouteSelection(ctx context.Context, tenantID, userID, selectionID string) (*model.RouteSelection, error)
	GetBootstrapOperationByScope(ctx context.Context, tenantID, userID, selectionID, idempotencyKey string) (*model.BootstrapOperation, error)
	GetBootstrapOperation(ctx context.Context, tenantID, userID, operationID string) (*model.BootstrapOperation, error)
	ReserveSelectionForBootstrap(ctx context.Context, selection *model.RouteSelection, operation *model.BootstrapOperation, now time.Time) error
	CompleteBootstrap(ctx context.Context, selectionID, operationID, state, selectionState, sessionID, lastError string, now time.Time) error
	CreateRouteEvaluationRun(ctx context.Context, run *model.RouteEvaluationRun) error
	GetRouteEvaluationRun(ctx context.Context, tenantID, runID string) (*model.RouteEvaluationRun, error)
	ListRouteEvaluationRuns(ctx context.Context, tenantID string, page, pageSize int) ([]model.RouteEvaluationRun, int64, error)
	DeleteExpiredRouteData(ctx context.Context, now time.Time, limit int) (int64, error)
}

func (s *store) CreateRouteDecision(ctx context.Context, decision *model.RouteDecision) error {
	return s.WithContext(ctx).Create(decision).Error
}

func (s *store) GetRouteDecision(ctx context.Context, tenantID, userID, routeID string) (*model.RouteDecision, error) {
	var decision model.RouteDecision
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, routeID).
		First(&decision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &decision, err
}

func (s *store) CreateRouteSelection(ctx context.Context, selection *model.RouteSelection) error {
	return s.WithContext(ctx).Create(selection).Error
}

func (s *store) GetRouteSelectionByScope(ctx context.Context, tenantID, userID, routeID, idempotencyKey string) (*model.RouteSelection, error) {
	return getRouteSelection(s.WithContext(ctx), tenantID, userID, map[string]interface{}{
		"route_id": routeID, "idempotency_key": idempotencyKey,
	})
}

func (s *store) GetRouteSelection(ctx context.Context, tenantID, userID, selectionID string) (*model.RouteSelection, error) {
	return getRouteSelection(s.WithContext(ctx), tenantID, userID, map[string]interface{}{"id": selectionID})
}

func getRouteSelection(q *gorm.DB, tenantID, userID string, conditions map[string]interface{}) (*model.RouteSelection, error) {
	var selection model.RouteSelection
	query := q.Where("tenant_id = ? AND user_id = ?", tenantID, userID)
	for column, value := range conditions {
		query = query.Where(column+" = ?", value)
	}
	err := query.First(&selection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &selection, err
}

func (s *store) GetBootstrapOperationByScope(ctx context.Context, tenantID, userID, selectionID, idempotencyKey string) (*model.BootstrapOperation, error) {
	return getBootstrapOperation(s.WithContext(ctx), tenantID, userID, map[string]interface{}{
		"route_selection_id": selectionID, "idempotency_key": idempotencyKey,
	})
}

func (s *store) GetBootstrapOperation(ctx context.Context, tenantID, userID, operationID string) (*model.BootstrapOperation, error) {
	return getBootstrapOperation(s.WithContext(ctx), tenantID, userID, map[string]interface{}{"id": operationID})
}

func getBootstrapOperation(q *gorm.DB, tenantID, userID string, conditions map[string]interface{}) (*model.BootstrapOperation, error) {
	var operation model.BootstrapOperation
	query := q.Where("tenant_id = ? AND user_id = ?", tenantID, userID)
	for column, value := range conditions {
		query = query.Where(column+" = ?", value)
	}
	err := query.First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &operation, err
}

// ReserveSelectionForBootstrap atomically transitions NEW -> RESERVED and
// creates the PENDING operation. No live session creation occurs in this tx.
func (s *store) ReserveSelectionForBootstrap(ctx context.Context, selection *model.RouteSelection, operation *model.BootstrapOperation, now time.Time) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.RouteSelection{}).
			Where("id = ? AND tenant_id = ? AND user_id = ? AND state = ? AND expires_at > ?", selection.ID, selection.TenantID, selection.UserID, model.RouteSelectionNew, now).
			Updates(map[string]interface{}{"state": model.RouteSelectionReserved, "bootstrap_operation_id": operation.ID, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRouteTransitionMiss
		}
		return tx.Create(operation).Error
	})
}

// CompleteBootstrap records the durable outcome and the one-time selection
// lifecycle transition in the same transaction.
func (s *store) CompleteBootstrap(ctx context.Context, selectionID, operationID, state, selectionState, sessionID, lastError string, now time.Time) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.BootstrapOperation{}).
			Where("id = ? AND state IN ?", operationID, []string{model.BootstrapPending, model.BootstrapReserved}).
			Updates(map[string]interface{}{"state": state, "session_id": sessionID, "last_error": lastError, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&model.RouteSelection{}).
			Where("id = ? AND state = ?", selectionID, model.RouteSelectionReserved).
			Updates(map[string]interface{}{"state": selectionState, "updated_at": now}).Error
	})
}

func (s *store) CreateRouteEvaluationRun(ctx context.Context, run *model.RouteEvaluationRun) error {
	return s.WithContext(ctx).Create(run).Error
}

func (s *store) GetRouteEvaluationRun(ctx context.Context, tenantID, runID string) (*model.RouteEvaluationRun, error) {
	var run model.RouteEvaluationRun
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, runID).
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &run, err
}

func (s *store) ListRouteEvaluationRuns(ctx context.Context, tenantID string, page, pageSize int) ([]model.RouteEvaluationRun, int64, error) {
	var runs []model.RouteEvaluationRun
	var total int64
	q := s.WithContext(ctx).Model(&model.RouteEvaluationRun{}).Where("tenant_id = ?", tenantID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&runs).Error
	return runs, total, err
}

// DeleteExpiredRouteData removes expired route decisions, selections and
// terminal bootstrap operations so the three routing tables do not grow
// indefinitely. Each pass deletes at most `limit` rows per table.
func (s *store) DeleteExpiredRouteData(ctx context.Context, now time.Time, limit int) (int64, error) {
	var deleted int64
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("expires_at < ?", now).Delete(&model.RouteDecision{}).Limit(limit)
		if res.Error != nil {
			return res.Error
		}
		deleted += res.RowsAffected
		res = tx.Where("expires_at < ?", now).Delete(&model.RouteSelection{}).Limit(limit)
		if res.Error != nil {
			return res.Error
		}
		deleted += res.RowsAffected
		res = tx.Where("created_at < ? AND state IN ('SUCCEEDED','FAILED')", now.Add(-24*time.Hour)).Delete(&model.BootstrapOperation{}).Limit(limit)
		if res.Error != nil {
			return res.Error
		}
		deleted += res.RowsAffected
		return nil
	})
	return deleted, err
}
