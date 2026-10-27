package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// DocumentGovernanceRepo persists document-level governance annotations and
// user clearance levels (T2, doc/224 §2.1 结构性新建顶点).
type DocumentGovernanceRepo interface {
	UpsertDocumentGovernance(ctx context.Context, g *model.DocumentGovernance) error
	GetDocumentGovernance(ctx context.Context, tenantID, datasetID, documentID string) (*model.DocumentGovernance, error)
	ListDocumentGovernance(ctx context.Context, tenantID string, filter DocumentGovernanceFilter, page, pageSize int) ([]model.DocumentGovernance, int64, error)
	ListDatasetDocumentGovernance(ctx context.Context, tenantID, datasetID string) ([]model.DocumentGovernance, error)
	DeleteDocumentGovernance(ctx context.Context, tenantID, datasetID, documentID string) error

	GetUserClearance(ctx context.Context, tenantID, userID string) (*model.UserClearance, error)
	SetUserClearance(ctx context.Context, c *model.UserClearance) error
}

// DocumentGovernanceFilter narrows ListDocumentGovernance results.
type DocumentGovernanceFilter struct {
	DatasetID      string
	DocumentID     string
	Level          string
	Status         string
	ApprovalState  string
	ApprovePending bool // 仅返回待审批（审批工作台用）
}

func (s *store) UpsertDocumentGovernance(ctx context.Context, g *model.DocumentGovernance) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "dataset_id"}, {Name: "document_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"level", "authority", "status", "source", "reason", "expires_at",
			"approval_state", "created_by", "approved_by", "approved_at", "updated_at",
		}),
	}).Create(g).Error
}

func (s *store) GetDocumentGovernance(ctx context.Context, tenantID, datasetID, documentID string) (*model.DocumentGovernance, error) {
	var g model.DocumentGovernance
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND dataset_id = ? AND document_id = ?", tenantID, datasetID, documentID).
		First(&g).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &g, err
}

func (s *store) ListDocumentGovernance(
	ctx context.Context, tenantID string, filter DocumentGovernanceFilter, page, pageSize int,
) ([]model.DocumentGovernance, int64, error) {
	q := s.WithContext(ctx).Model(&model.DocumentGovernance{}).Where("tenant_id = ?", tenantID)
	if filter.DatasetID != "" {
		q = q.Where("dataset_id = ?", filter.DatasetID)
	}
	if filter.DocumentID != "" {
		q = q.Where("document_id = ?", filter.DocumentID)
	}
	if filter.Level != "" {
		q = q.Where("level = ?", filter.Level)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.ApprovalState != "" {
		q = q.Where("approval_state = ?", filter.ApprovalState)
	}
	if filter.ApprovePending {
		q = q.Where("approval_state = ?", model.GovernanceApprovalPending)
	}
	var items []model.DocumentGovernance
	var total int64
	q.Group("id") // ensure count stays per-row even with the unique index pair
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	if err := q.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListDatasetDocumentGovernance loads every annotation for one dataset ordered
// by sensitivity descending — the projection reader's working set.
func (s *store) ListDatasetDocumentGovernance(ctx context.Context, tenantID, datasetID string) ([]model.DocumentGovernance, error) {
	var items []model.DocumentGovernance
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND dataset_id = ?", tenantID, datasetID).
		Order("document_id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *store) DeleteDocumentGovernance(ctx context.Context, tenantID, datasetID, documentID string) error {
	return s.WithContext(ctx).
		Where("tenant_id = ? AND dataset_id = ? AND document_id = ?", tenantID, datasetID, documentID).
		Delete(&model.DocumentGovernance{}).Error
}

func (s *store) GetUserClearance(ctx context.Context, tenantID, userID string) (*model.UserClearance, error) {
	var c model.UserClearance
	err := s.WithContext(ctx).Where("tenant_id = ? AND user_id = ?", tenantID, userID).First(&c).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &c, err
}

func (s *store) SetUserClearance(ctx context.Context, c *model.UserClearance) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"level", "updated_by", "updated_at",
		}),
	}).Create(c).Error
}
