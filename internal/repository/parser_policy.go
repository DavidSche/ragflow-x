package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ParserPolicyFilter narrows parser policy list queries while retaining the
// mandatory tenant boundary.
type ParserPolicyFilter struct {
	ProjectID    string
	DatasetID    string
	DocumentType string
	ParseMode    string
	Active       *bool
}

// ParserPolicyRepo persists parser policies and quality profiles.
type ParserPolicyRepo interface {
	CreateParserPolicy(ctx context.Context, policy *model.ParserPolicy) error
	GetParserPolicy(ctx context.Context, tenantID, id string) (*model.ParserPolicy, error)
	ListParserPolicies(ctx context.Context, tenantID string, filter ParserPolicyFilter, page, pageSize int) ([]model.ParserPolicy, int64, error)
	ListAllParserPolicies(ctx context.Context, tenantID string) ([]model.ParserPolicy, error)
	CountActiveParserPolicies(ctx context.Context, tenantID, projectID, datasetID, documentType, excludeID string) (int64, error)
	CountActiveParserPoliciesByQualityProfile(ctx context.Context, tenantID, qualityProfileID string) (int64, error)
	CountActiveParserPoliciesByFallback(ctx context.Context, tenantID, parserPolicyID string) (int64, error)
	UpdateParserPolicy(ctx context.Context, policy *model.ParserPolicy) error
	DeleteParserPolicy(ctx context.Context, tenantID, id string) error

	CreateQualityProfile(ctx context.Context, profile *model.QualityProfile) error
	GetQualityProfile(ctx context.Context, tenantID, id string) (*model.QualityProfile, error)
	GetQualityProfileByName(ctx context.Context, tenantID, name, excludeID string) (*model.QualityProfile, error)
	ListQualityProfiles(ctx context.Context, tenantID string, active *bool, page, pageSize int) ([]model.QualityProfile, int64, error)
	UpdateQualityProfile(ctx context.Context, profile *model.QualityProfile) error
	DeleteQualityProfile(ctx context.Context, tenantID, id string) error
}

func (s *store) CreateParserPolicy(ctx context.Context, policy *model.ParserPolicy) error {
	return s.WithContext(ctx).Create(policy).Error
}

func (s *store) GetParserPolicy(ctx context.Context, tenantID, id string) (*model.ParserPolicy, error) {
	var policy model.ParserPolicy
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *store) ListParserPolicies(ctx context.Context, tenantID string, filter ParserPolicyFilter, page, pageSize int) ([]model.ParserPolicy, int64, error) {
	query, args := parserPolicyQuery(tenantID, filter)
	return listTenantPage(ctx, s.DB, &[]model.ParserPolicy{}, query, "created_at DESC", page, pageSize, args...)
}

func (s *store) ListAllParserPolicies(ctx context.Context, tenantID string) ([]model.ParserPolicy, error) {
	var policies []model.ParserPolicy
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&policies).Error
	return policies, err
}

func (s *store) CountActiveParserPolicies(ctx context.Context, tenantID, projectID, datasetID, documentType, excludeID string) (int64, error) {
	var count int64
	query := s.WithContext(ctx).Model(&model.ParserPolicy{}).Where(
		"tenant_id = ? AND project_id = ? AND dataset_id = ? AND document_type = ? AND active = ?",
		tenantID, projectID, datasetID, documentType, true,
	)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	err := query.Count(&count).Error
	return count, err
}

func (s *store) CountActiveParserPoliciesByQualityProfile(ctx context.Context, tenantID, qualityProfileID string) (int64, error) {
	return s.countParserPolicies(ctx, tenantID, "quality_profile_id = ? AND active = ?", qualityProfileID, true)
}

func (s *store) CountActiveParserPoliciesByFallback(ctx context.Context, tenantID, parserPolicyID string) (int64, error) {
	return s.countParserPolicies(ctx, tenantID, "fallback_policy LIKE ? AND active = ?", "%\"fallback_policy_id\":\""+parserPolicyID+"\"%", true)
}

func (s *store) countParserPolicies(ctx context.Context, tenantID, query string, args ...interface{}) (int64, error) {
	var count int64
	err := s.WithContext(ctx).Model(&model.ParserPolicy{}).
		Where("tenant_id = ?", tenantID).
		Where(query, args...).
		Count(&count).Error
	return count, err
}

func (s *store) UpdateParserPolicy(ctx context.Context, policy *model.ParserPolicy) error {
	return s.WithContext(ctx).Model(&model.ParserPolicy{}).
		Where("tenant_id = ? AND id = ?", policy.TenantID, policy.ID).
		Select("*").
		Updates(policy).Error
}

func (s *store) DeleteParserPolicy(ctx context.Context, tenantID, id string) error {
	res := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.ParserPolicy{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *store) CreateQualityProfile(ctx context.Context, profile *model.QualityProfile) error {
	return s.WithContext(ctx).Create(profile).Error
}

func (s *store) GetQualityProfile(ctx context.Context, tenantID, id string) (*model.QualityProfile, error) {
	var profile model.QualityProfile
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *store) GetQualityProfileByName(ctx context.Context, tenantID, name, excludeID string) (*model.QualityProfile, error) {
	var profile model.QualityProfile
	query := s.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenantID, name)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	err := query.First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *store) ListQualityProfiles(ctx context.Context, tenantID string, active *bool, page, pageSize int) ([]model.QualityProfile, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if active != nil {
		query += " AND active = ?"
		args = append(args, *active)
	}
	return listTenantPage(ctx, s.DB, &[]model.QualityProfile{}, query, "created_at DESC", page, pageSize, args...)
}

func (s *store) UpdateQualityProfile(ctx context.Context, profile *model.QualityProfile) error {
	return s.WithContext(ctx).Model(&model.QualityProfile{}).
		Where("tenant_id = ? AND id = ?", profile.TenantID, profile.ID).
		Select("*").
		Updates(profile).Error
}

func (s *store) DeleteQualityProfile(ctx context.Context, tenantID, id string) error {
	res := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.QualityProfile{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func parserPolicyQuery(tenantID string, filter ParserPolicyFilter) (string, []interface{}) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if filter.DatasetID != "" {
		query += " AND dataset_id = ?"
		args = append(args, filter.DatasetID)
	}
	if filter.DocumentType != "" {
		query += " AND document_type = ?"
		args = append(args, filter.DocumentType)
	}
	if filter.ParseMode != "" {
		query += " AND parse_mode = ?"
		args = append(args, filter.ParseMode)
	}
	if filter.Active != nil {
		query += " AND active = ?"
		args = append(args, *filter.Active)
	}
	return query, args
}
