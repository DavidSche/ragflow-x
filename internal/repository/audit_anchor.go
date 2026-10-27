package repository

import (
	"context"
	"errors"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm/clause"
)

// AuditAnchorRepo persists immutable audit hash-chain tails.
type AuditAnchorRepo interface {
	CreateAuditAnchor(ctx context.Context, anchor *model.AuditAnchor) (bool, error)
	ListAuditAnchors(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int) ([]model.AuditAnchor, int64, error)
	ListAuditAnchorsAll(ctx context.Context, tenantID string, scopeAll bool) ([]model.AuditAnchor, error)
}

// CreateAuditAnchor inserts a new anchor. Re-running the worker over an
// unchanged chain tail is idempotent and returns created=false.
func (s *store) CreateAuditAnchor(ctx context.Context, anchor *model.AuditAnchor) (bool, error) {
	if anchor.TenantID == "" || anchor.LastSeq <= 0 || anchor.LastHash == "" {
		return false, errors.New("tenant_id, last_seq and last_hash are required")
	}
	if anchor.ID == "" {
		anchor.ID = id.New()
	}
	if anchor.Algorithm == "" {
		anchor.Algorithm = model.AuditAnchorAlgorithm
	}
	res := s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "last_seq"}, {Name: "last_hash"}},
		DoNothing: true,
	}).Create(anchor)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListAuditAnchors returns anchors with strict tenant isolation unless the
// caller is allowed to view all tenants.
func (s *store) ListAuditAnchors(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int) ([]model.AuditAnchor, int64, error) {
	var list []model.AuditAnchor
	var total int64
	q := s.WithContext(ctx).Model(&model.AuditAnchor{})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("last_seq DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// ListAuditAnchorsAll returns every anchor permitted by the scope. It is used
// only by the platform-side external exporter, never by tenant-facing APIs.
func (s *store) ListAuditAnchorsAll(ctx context.Context, tenantID string, scopeAll bool) ([]model.AuditAnchor, error) {
	var list []model.AuditAnchor
	q := s.WithContext(ctx).Model(&model.AuditAnchor{})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	err := q.Order("tenant_id ASC, last_seq ASC").Find(&list).Error
	return list, err
}
