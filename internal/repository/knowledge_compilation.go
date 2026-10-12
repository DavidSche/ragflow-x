package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type KnowledgeProfileFilter struct {
	DatasetID string
	ProjectID string
	Status    string
}

type CompilationRunFilter struct {
	DatasetID string
	ProfileID string
	State     string
}

type KnowledgeCompilationRepo interface {
	CreateKnowledgeProfile(ctx context.Context, profile *model.KnowledgeProfile) error
	GetKnowledgeProfile(ctx context.Context, tenantID, id string) (*model.KnowledgeProfile, error)
	UpdateKnowledgeProfile(ctx context.Context, profile *model.KnowledgeProfile) error
	DeleteKnowledgeProfile(ctx context.Context, tenantID, id string) error
	ListKnowledgeProfiles(ctx context.Context, tenantID string, filter KnowledgeProfileFilter, page, pageSize int) ([]model.KnowledgeProfile, int64, error)

	CreateCompilationRun(ctx context.Context, run *model.CompilationRun) error
	GetCompilationRun(ctx context.Context, tenantID, id string) (*model.CompilationRun, error)
	GetCompilationRunByIdempotency(ctx context.Context, tenantID, datasetID, idempotencyKey string) (*model.CompilationRun, error)
	UpdateCompilationRun(ctx context.Context, run *model.CompilationRun) error
	ListCompilationRuns(ctx context.Context, tenantID string, filter CompilationRunFilter, page, pageSize int) ([]model.CompilationRun, int64, error)
	ListCompilationRecoveryRuns(ctx context.Context, tenantID, datasetID string, page, pageSize int) ([]model.CompilationRun, int64, error)
	ListActiveCompilationRuns(ctx context.Context, tenantID, datasetID string, limit int) ([]model.CompilationRun, error)
	TransitionCompilationRun(ctx context.Context, tenantID, runID string, fromStates []string, updates map[string]interface{}) (bool, error)
	ListArtifactProjections(ctx context.Context, tenantID, datasetID, runID string, page, pageSize int) ([]model.ArtifactProjection, int64, error)
	UpsertArtifactProjection(ctx context.Context, projection *model.ArtifactProjection) error
}

func (s *store) CreateKnowledgeProfile(ctx context.Context, profile *model.KnowledgeProfile) error {
	return s.WithContext(ctx).Create(profile).Error
}

func (s *store) GetKnowledgeProfile(ctx context.Context, tenantID, profileID string) (*model.KnowledgeProfile, error) {
	var profile model.KnowledgeProfile
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, profileID).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *store) UpdateKnowledgeProfile(ctx context.Context, profile *model.KnowledgeProfile) error {
	return s.WithContext(ctx).Save(profile).Error
}

func (s *store) DeleteKnowledgeProfile(ctx context.Context, tenantID, profileID string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, profileID).Delete(&model.KnowledgeProfile{}).Error
}

func (s *store) ListKnowledgeProfiles(
	ctx context.Context, tenantID string, filter KnowledgeProfileFilter, page, pageSize int,
) ([]model.KnowledgeProfile, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.DatasetID != "" {
		query += " AND dataset_id = ?"
		args = append(args, filter.DatasetID)
	}
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	return listTenantPage(
		ctx, s.DB, &[]model.KnowledgeProfile{}, query,
		"created_at DESC, id DESC", page, pageSize, args...,
	)
}

func (s *store) CreateCompilationRun(ctx context.Context, run *model.CompilationRun) error {
	return s.WithContext(ctx).Create(run).Error
}

func (s *store) GetCompilationRun(ctx context.Context, tenantID, runID string) (*model.CompilationRun, error) {
	var run model.CompilationRun
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, runID).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *store) GetCompilationRunByIdempotency(
	ctx context.Context, tenantID, datasetID, idempotencyKey string,
) (*model.CompilationRun, error) {
	var run model.CompilationRun
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND dataset_id = ? AND idempotency_key = ?",
		tenantID, datasetID, idempotencyKey,
	).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *store) UpdateCompilationRun(ctx context.Context, run *model.CompilationRun) error {
	return s.WithContext(ctx).Save(run).Error
}

func (s *store) ListCompilationRuns(
	ctx context.Context, tenantID string, filter CompilationRunFilter, page, pageSize int,
) ([]model.CompilationRun, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.DatasetID != "" {
		query += " AND dataset_id = ?"
		args = append(args, filter.DatasetID)
	}
	if filter.ProfileID != "" {
		query += " AND profile_id = ?"
		args = append(args, filter.ProfileID)
	}
	if filter.State != "" {
		query += " AND state = ?"
		args = append(args, filter.State)
	}
	return listTenantPage(
		ctx, s.DB, &[]model.CompilationRun{}, query,
		"created_at DESC, id DESC", page, pageSize, args...,
	)
}

func (s *store) ListCompilationRecoveryRuns(
	ctx context.Context, tenantID, datasetID string, page, pageSize int,
) ([]model.CompilationRun, int64, error) {
	query := s.WithContext(ctx).Model(&model.CompilationRun{}).
		Where("tenant_id = ?", tenantID).
		Where(
			"state = ? OR (state = ? AND failure_code = ? AND recovery_state IN ?)",
			model.CompilationRunUnknown,
			model.CompilationRunReconciled,
			model.CompilationFailureDownstreamNotFound,
			[]string{model.CompilationRecoveryConfirmed, model.CompilationRecoveryReplacementPending},
		)
	if datasetID != "" {
		query = query.Where("dataset_id = ?", datasetID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	runs := []model.CompilationRun{}
	if err := query.Order("updated_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&runs).Error; err != nil {
		return nil, 0, err
	}
	return runs, total, nil
}

func (s *store) ListActiveCompilationRuns(
	ctx context.Context, tenantID, datasetID string, limit int,
) ([]model.CompilationRun, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var runs []model.CompilationRun
	query := s.WithContext(ctx).Where(
		"tenant_id = ? AND state IN ?",
		tenantID,
		[]string{
			model.CompilationRunAccepted, model.CompilationRunDispatched, model.CompilationRunUnknown,
		},
	)
	if datasetID != "" {
		query = query.Where("dataset_id = ?", datasetID)
	}
	if err := query.Order("created_at ASC, id ASC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *store) TransitionCompilationRun(
	ctx context.Context, tenantID, runID string, fromStates []string, updates map[string]interface{},
) (bool, error) {
	now := time.Now().UTC()
	updates["updated_at"] = now
	result := s.WithContext(ctx).Model(&model.CompilationRun{}).
		Where("tenant_id = ? AND id = ? AND state IN ?", tenantID, runID, fromStates).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *store) ListArtifactProjections(
	ctx context.Context, tenantID, datasetID, runID string, page, pageSize int,
) ([]model.ArtifactProjection, int64, error) {
	query := "tenant_id = ? AND dataset_id = ?"
	args := []interface{}{tenantID, datasetID}
	if runID != "" {
		query += " AND run_id = ?"
		args = append(args, runID)
	}
	return listTenantPage(
		ctx, s.DB, &[]model.ArtifactProjection{}, query,
		"created_at DESC, id DESC", page, pageSize, args...,
	)
}

func (s *store) UpsertArtifactProjection(ctx context.Context, projection *model.ArtifactProjection) error {
	now := projection.UpdatedAt
	existing := &model.ArtifactProjection{}
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND dataset_id = ? AND run_id = ? AND artifact_id = ?",
		projection.TenantID, projection.DatasetID, projection.RunID, projection.ArtifactID,
	).First(existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.WithContext(ctx).Create(projection).Error
	}
	if err != nil {
		return err
	}
	projection.ID = existing.ID
	projection.CreatedAt = existing.CreatedAt
	projection.UpdatedAt = now
	return s.WithContext(ctx).Model(existing).Updates(map[string]interface{}{
		"kind": projection.Kind, "state": projection.State,
		"artifact_hash": projection.ArtifactHash, "content_hash": projection.ContentHash,
		"artifact_json": projection.ArtifactJSON,
		"updated_at":    projection.UpdatedAt,
	}).Error
}
