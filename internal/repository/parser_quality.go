package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ParseQualityRepo persists the quality evaluation trace and its final report.
type ParseQualityRepo interface {
	NextParseAttemptNo(ctx context.Context, tenantID, documentID string) (int64, error)
	CreateParseAttempt(ctx context.Context, attempt *model.ParseAttempt) error
	ListParseAttempts(ctx context.Context, tenantID, documentID string, page, pageSize int) ([]model.ParseAttempt, int64, error)
	CreateParseQualityReport(ctx context.Context, report *model.ParseQualityReport) error
	GetLatestParseQualityReport(ctx context.Context, tenantID, documentID string) (*model.ParseQualityReport, error)
	ListParseQualityReports(ctx context.Context, tenantID, documentID string, page, pageSize int) ([]model.ParseQualityReport, int64, error)
	ListLatestParseQualityReports(ctx context.Context, tenantID string, documentIDs []string) ([]model.ParseQualityReport, int64, error)
}

func (s *store) NextParseAttemptNo(ctx context.Context, tenantID, documentID string) (int64, error) {
	var attemptNo int64
	err := s.WithContext(ctx).Model(&model.ParseAttempt{}).
		Where("tenant_id = ? AND document_id = ?", tenantID, documentID).
		Select("COALESCE(MAX(attempt_no), 0)").
		Scan(&attemptNo).Error
	return attemptNo + 1, err
}

func (s *store) CreateParseAttempt(ctx context.Context, attempt *model.ParseAttempt) error {
	return s.WithContext(ctx).Create(attempt).Error
}

func (s *store) ListParseAttempts(ctx context.Context, tenantID, documentID string, page, pageSize int) ([]model.ParseAttempt, int64, error) {
	return listTenantPage(ctx, s.DB, &[]model.ParseAttempt{}, "tenant_id = ? AND document_id = ?", "attempt_no DESC", page, pageSize, tenantID, documentID)
}

func (s *store) CreateParseQualityReport(ctx context.Context, report *model.ParseQualityReport) error {
	return s.WithContext(ctx).Create(report).Error
}

func (s *store) GetLatestParseQualityReport(ctx context.Context, tenantID, documentID string) (*model.ParseQualityReport, error) {
	var report model.ParseQualityReport
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND document_id = ?", tenantID, documentID).
		Order("created_at DESC").
		First(&report).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &report, nil
}

func (s *store) ListParseQualityReports(ctx context.Context, tenantID, documentID string, page, pageSize int) ([]model.ParseQualityReport, int64, error) {
	return listTenantPage(ctx, s.DB, &[]model.ParseQualityReport{}, "tenant_id = ? AND document_id = ?", "created_at DESC", page, pageSize, tenantID, documentID)
}

func (s *store) ListLatestParseQualityReports(ctx context.Context, tenantID string, documentIDs []string) ([]model.ParseQualityReport, int64, error) {
	if len(documentIDs) == 0 {
		return []model.ParseQualityReport{}, 0, nil
	}
	latestCondition := `NOT EXISTS (
		SELECT 1 FROM rgx_parse_quality_report newer
		WHERE newer.tenant_id = r.tenant_id AND newer.document_id = r.document_id
		AND (newer.created_at > r.created_at OR newer.created_at = r.created_at AND newer.id > r.id)
	)`
	items := make([]model.ParseQualityReport, 0, len(documentIDs))
	var total int64
	for start := 0; start < len(documentIDs); start += 500 {
		end := min(start+500, len(documentIDs))
		ids := documentIDs[start:end]
		args := []interface{}{tenantID, ids}
		query := "SELECT r.* FROM rgx_parse_quality_report r WHERE r.tenant_id = ? AND r.document_id IN (?) AND " + latestCondition
		var pageItems []model.ParseQualityReport
		if err := s.WithContext(ctx).Raw(query, args...).Scan(&pageItems).Error; err != nil {
			return nil, 0, err
		}
		var pageCount int64
		if err := s.WithContext(ctx).Raw("SELECT COUNT(*) FROM ("+query+") latest", args...).Scan(&pageCount).Error; err != nil {
			return nil, 0, err
		}
		items = append(items, pageItems...)
		total += pageCount
	}
	return items, total, nil
}
