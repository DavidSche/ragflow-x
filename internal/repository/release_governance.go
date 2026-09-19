package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ReleaseGovernanceRepo persists the immutable quality-to-release chain.
type ReleaseGovernanceRepo interface {
	CreateReleaseCandidate(ctx context.Context, candidate *model.ReleaseCandidate) error
	GetReleaseCandidate(ctx context.Context, tenantID, id string) (*model.ReleaseCandidate, error)
	GetReleaseCandidateVersion(ctx context.Context, tenantID, candidateID string, candidateVersion int64) (*model.ReleaseCandidate, error)
	ListReleaseCandidates(ctx context.Context, tenantID string, page, pageSize int) ([]model.ReleaseCandidate, int64, error)
	TransitionReleaseCandidate(ctx context.Context, tenantID, id, fromStatus, toStatus string) error
	CreateExecutionSnapshot(ctx context.Context, snapshot *model.ExecutionSnapshot) error
	GetExecutionSnapshot(ctx context.Context, tenantID, id string) (*model.ExecutionSnapshot, error)
	ListExecutionSnapshots(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.ExecutionSnapshot, int64, error)
	CreateEvaluationSetVersion(ctx context.Context, version *model.EvaluationSetVersion) error
	CreateEvaluationSetVersionWithCases(ctx context.Context, version *model.EvaluationSetVersion, cases []model.EvaluationCaseVersion) error
	GetEvaluationSetVersion(ctx context.Context, tenantID, evalSetID string, version int64) (*model.EvaluationSetVersion, error)
	GetEvaluationCaseVersion(ctx context.Context, tenantID, id string) (*model.EvaluationCaseVersion, error)
	CreateEvaluationCaseVersions(ctx context.Context, versions []model.EvaluationCaseVersion) error
	ListEvaluationCaseVersions(ctx context.Context, tenantID, evalSetID string, evalSetVersion int64) ([]model.EvaluationCaseVersion, error)
	CreateEvaluationRun(ctx context.Context, run *model.EvaluationRun) error
	StartEvaluationRun(ctx context.Context, runID string) error
	GetEvaluationRun(ctx context.Context, tenantID, id string) (*model.EvaluationRun, error)
	ListEvaluationRuns(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.EvaluationRun, int64, error)
	CompleteEvaluationRun(ctx context.Context, runID, status, metrics string, pass *bool) error
	CreateEvaluationCaseResult(ctx context.Context, result *model.EvaluationCaseResult) error
	ListEvaluationCaseResults(ctx context.Context, tenantID, runID string) ([]model.EvaluationCaseResult, error)
	CreateEvidenceBundle(ctx context.Context, bundle *model.EvidenceBundle) error
	GetEvidenceBundle(ctx context.Context, tenantID, id string) (*model.EvidenceBundle, error)
	ListEvidenceBundles(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.EvidenceBundle, int64, error)
	CreateGateDecision(ctx context.Context, decision *model.ReleaseGateDecision) (bool, error)
	GetGateDecision(ctx context.Context, tenantID, id string) (*model.ReleaseGateDecision, error)
	GetActiveGateDecision(ctx context.Context, tenantID, candidateID string, candidateVersion int64) (*model.ReleaseGateDecision, error)
	ListGateDecisions(ctx context.Context, tenantID, candidateID string, candidateVersion int64, page, pageSize int) ([]model.ReleaseGateDecision, int64, error)
	ActivateGateDecision(ctx context.Context, decision *model.ReleaseGateDecision) error
	CreateRelease(ctx context.Context, release *model.Release) error
	GetRelease(ctx context.Context, tenantID, id string) (*model.Release, error)
	ListReleases(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.Release, int64, error)
	TransitionRelease(ctx context.Context, tenantID, id, fromStatus, toStatus string) error
	CreateQualityIssue(ctx context.Context, issue *model.QualityIssue) error
	GetQualityIssue(ctx context.Context, tenantID, id string) (*model.QualityIssue, error)
	ListQualityIssues(ctx context.Context, tenantID, status string, page, pageSize int) ([]model.QualityIssue, int64, error)
	UpdateQualityIssue(ctx context.Context, issue *model.QualityIssue) error
}

func (s *store) CreateReleaseCandidate(ctx context.Context, candidate *model.ReleaseCandidate) error {
	return s.WithContext(ctx).Create(candidate).Error
}

func (s *store) GetReleaseCandidate(ctx context.Context, tenantID, candidateID string) (*model.ReleaseCandidate, error) {
	var candidate model.ReleaseCandidate
	err := s.WithContext(ctx).Where("tenant_id = ? AND candidate_id = ?", tenantID, candidateID).First(&candidate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &candidate, err
}

func (s *store) GetReleaseCandidateVersion(ctx context.Context, tenantID, candidateID string, candidateVersion int64) (*model.ReleaseCandidate, error) {
	var candidate model.ReleaseCandidate
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND candidate_id = ? AND candidate_version = ?", tenantID, candidateID, candidateVersion).
		First(&candidate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &candidate, err
}

func (s *store) ListReleaseCandidates(ctx context.Context, tenantID string, page, pageSize int) ([]model.ReleaseCandidate, int64, error) {
	return listTenantPage(ctx, s.DB, &[]model.ReleaseCandidate{}, "tenant_id = ?", "candidate_version DESC", page, pageSize, tenantID)
}

func (s *store) TransitionReleaseCandidate(ctx context.Context, tenantID, candidateID, fromStatus, toStatus string) error {
	res := s.WithContext(ctx).Model(&model.ReleaseCandidate{}).
		Where("tenant_id = ? AND candidate_id = ? AND status = ?", tenantID, candidateID, fromStatus).
		Updates(map[string]interface{}{"status": toStatus, "updated_at": time.Now().UTC()})
	if res.Error == nil && res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return res.Error
}

func (s *store) CreateExecutionSnapshot(ctx context.Context, snapshot *model.ExecutionSnapshot) error {
	return s.WithContext(ctx).Create(snapshot).Error
}

func (s *store) GetExecutionSnapshot(ctx context.Context, tenantID, id string) (*model.ExecutionSnapshot, error) {
	var snapshot model.ExecutionSnapshot
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &snapshot, err
}

func (s *store) ListExecutionSnapshots(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.ExecutionSnapshot, int64, error) {
	if candidateID == "" {
		return listTenantPage(ctx, s.DB, &[]model.ExecutionSnapshot{}, "tenant_id = ?", "created_at DESC", page, pageSize, tenantID)
	}
	return listTenantPage(ctx, s.DB, &[]model.ExecutionSnapshot{}, "tenant_id = ? AND release_candidate_id = ?", "created_at DESC", page, pageSize, tenantID, candidateID)
}

func (s *store) CreateEvaluationSetVersion(ctx context.Context, version *model.EvaluationSetVersion) error {
	return s.WithContext(ctx).Create(version).Error
}

func (s *store) CreateEvaluationSetVersionWithCases(ctx context.Context, version *model.EvaluationSetVersion, cases []model.EvaluationCaseVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		if len(cases) == 0 {
			return nil
		}
		return tx.Create(&cases).Error
	})
}

func (s *store) GetEvaluationSetVersion(ctx context.Context, tenantID, evalSetID string, version int64) (*model.EvaluationSetVersion, error) {
	var result model.EvaluationSetVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND eval_set_id = ? AND version = ?", tenantID, evalSetID, version).First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &result, err
}

func (s *store) GetEvaluationCaseVersion(ctx context.Context, tenantID, id string) (*model.EvaluationCaseVersion, error) {
	var version model.EvaluationCaseVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &version, err
}

func (s *store) CreateEvaluationCaseVersions(ctx context.Context, versions []model.EvaluationCaseVersion) error {
	if len(versions) == 0 {
		return nil
	}
	return s.WithContext(ctx).Create(&versions).Error
}

func (s *store) ListEvaluationCaseVersions(ctx context.Context, tenantID, evalSetID string, evalSetVersion int64) ([]model.EvaluationCaseVersion, error) {
	var versions []model.EvaluationCaseVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND eval_set_id = ? AND eval_set_version = ?", tenantID, evalSetID, evalSetVersion).
		Order("case_version ASC").Find(&versions).Error
	return versions, err
}

func (s *store) CreateEvaluationRun(ctx context.Context, run *model.EvaluationRun) error {
	return s.WithContext(ctx).Create(run).Error
}

func (s *store) GetEvaluationRun(ctx context.Context, tenantID, id string) (*model.EvaluationRun, error) {
	var run model.EvaluationRun
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &run, err
}

func (s *store) StartEvaluationRun(ctx context.Context, runID string) error {
	res := s.WithContext(ctx).Model(&model.EvaluationRun{}).
		Where("id = ? AND status = ?", runID, model.EvaluationRunQueued).
		Updates(map[string]interface{}{"status": model.EvaluationRunRunning, "started_at": time.Now().UTC(), "updated_at": time.Now().UTC()})
	if res.Error == nil && res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return res.Error
}

func (s *store) ListEvaluationRuns(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.EvaluationRun, int64, error) {
	if candidateID == "" {
		return listTenantPage(ctx, s.DB, &[]model.EvaluationRun{}, "tenant_id = ?", "created_at DESC", page, pageSize, tenantID)
	}
	return listTenantPage(ctx, s.DB, &[]model.EvaluationRun{}, "tenant_id = ? AND release_candidate_id = ?", "created_at DESC", page, pageSize, tenantID, candidateID)
}

func (s *store) CompleteEvaluationRun(ctx context.Context, runID, status, metrics string, pass *bool) error {
	return s.WithContext(ctx).Model(&model.EvaluationRun{}).
		Where("id = ? AND status = ?", runID, model.EvaluationRunRunning).
		Updates(map[string]interface{}{
			"status": status, "metrics": metrics, "pass": pass,
			"completed_at": time.Now().UTC(), "updated_at": time.Now().UTC(),
		}).Error
}

func (s *store) CreateEvaluationCaseResult(ctx context.Context, result *model.EvaluationCaseResult) error {
	return s.WithContext(ctx).Create(result).Error
}

func (s *store) ListEvaluationCaseResults(ctx context.Context, tenantID, runID string) ([]model.EvaluationCaseResult, error) {
	var results []model.EvaluationCaseResult
	err := s.WithContext(ctx).Where("tenant_id = ? AND run_id = ?", tenantID, runID).Order("created_at ASC").Find(&results).Error
	return results, err
}

func (s *store) CreateEvidenceBundle(ctx context.Context, bundle *model.EvidenceBundle) error {
	return s.WithContext(ctx).Create(bundle).Error
}

func (s *store) GetEvidenceBundle(ctx context.Context, tenantID, id string) (*model.EvidenceBundle, error) {
	var bundle model.EvidenceBundle
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&bundle).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &bundle, err
}

func (s *store) ListEvidenceBundles(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.EvidenceBundle, int64, error) {
	if candidateID == "" {
		return listTenantPage(ctx, s.DB, &[]model.EvidenceBundle{}, "tenant_id = ?", "created_at DESC", page, pageSize, tenantID)
	}
	return listTenantPage(ctx, s.DB, &[]model.EvidenceBundle{}, "tenant_id = ? AND release_candidate_id = ?", "created_at DESC", page, pageSize, tenantID, candidateID)
}

func (s *store) CreateGateDecision(ctx context.Context, decision *model.ReleaseGateDecision) (bool, error) {
	res := s.WithContext(ctx).Clauses(clauseOnConflictDoNothing()).Create(decision)
	return res.RowsAffected > 0, res.Error
}

func (s *store) GetGateDecision(ctx context.Context, tenantID, id string) (*model.ReleaseGateDecision, error) {
	var decision model.ReleaseGateDecision
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&decision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &decision, err
}

func (s *store) GetActiveGateDecision(ctx context.Context, tenantID, candidateID string, candidateVersion int64) (*model.ReleaseGateDecision, error) {
	var decision model.ReleaseGateDecision
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND release_candidate_id = ? AND candidate_version = ? AND active_gate = ?",
			tenantID, candidateID, candidateVersion, true).
		First(&decision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &decision, err
}

func (s *store) ListGateDecisions(ctx context.Context, tenantID, candidateID string, candidateVersion int64, page, pageSize int) ([]model.ReleaseGateDecision, int64, error) {
	if candidateID == "" {
		return listTenantPage(ctx, s.DB, &[]model.ReleaseGateDecision{}, "tenant_id = ?", "created_at DESC", page, pageSize, tenantID)
	}
	return listTenantPage(ctx, s.DB, &[]model.ReleaseGateDecision{}, "tenant_id = ? AND release_candidate_id = ? AND candidate_version = ?", "created_at DESC", page, pageSize, tenantID, candidateID, candidateVersion)
}

func (s *store) ActivateGateDecision(ctx context.Context, decision *model.ReleaseGateDecision) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ReleaseGateDecision{}).
			Where("tenant_id = ? AND release_candidate_id = ? AND candidate_version = ? AND active_gate = ?",
				decision.TenantID, decision.ReleaseCandidateID, decision.CandidateVersion, true).
			Update("active_gate", false).Error; err != nil {
			return err
		}
		return tx.Model(&model.ReleaseGateDecision{}).
			Where("tenant_id = ? AND id = ?", decision.TenantID, decision.ID).
			Update("active_gate", true).Error
	})
}

func (s *store) CreateRelease(ctx context.Context, release *model.Release) error {
	return s.WithContext(ctx).Create(release).Error
}

func (s *store) GetRelease(ctx context.Context, tenantID, id string) (*model.Release, error) {
	var release model.Release
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&release).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &release, err
}

func (s *store) ListReleases(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.Release, int64, error) {
	if candidateID == "" {
		return listTenantPage(ctx, s.DB, &[]model.Release{}, "tenant_id = ?", "created_at DESC", page, pageSize, tenantID)
	}
	return listTenantPage(ctx, s.DB, &[]model.Release{}, "tenant_id = ? AND release_candidate_id = ?", "created_at DESC", page, pageSize, tenantID, candidateID)
}

func (s *store) TransitionRelease(ctx context.Context, tenantID, id, fromStatus, toStatus string) error {
	res := s.WithContext(ctx).Model(&model.Release{}).
		Where("tenant_id = ? AND id = ? AND status = ?", tenantID, id, fromStatus).
		Updates(map[string]interface{}{
			"status": toStatus, "released_at": time.Now().UTC(), "updated_at": time.Now().UTC(),
		})
	if res.Error == nil && res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return res.Error
}

func (s *store) CreateQualityIssue(ctx context.Context, issue *model.QualityIssue) error {
	return s.WithContext(ctx).Create(issue).Error
}

func (s *store) GetQualityIssue(ctx context.Context, tenantID, id string) (*model.QualityIssue, error) {
	var issue model.QualityIssue
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&issue).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &issue, err
}

func (s *store) ListQualityIssues(ctx context.Context, tenantID, status string, page, pageSize int) ([]model.QualityIssue, int64, error) {
	if status == "" {
		return listTenantPage(ctx, s.DB, &[]model.QualityIssue{}, "tenant_id = ?", "updated_at DESC", page, pageSize, tenantID)
	}
	return listTenantPage(ctx, s.DB, &[]model.QualityIssue{}, "tenant_id = ? AND status = ?", "updated_at DESC", page, pageSize, tenantID, status)
}

func (s *store) UpdateQualityIssue(ctx context.Context, issue *model.QualityIssue) error {
	issue.UpdatedAt = time.Now().UTC()
	return s.WithContext(ctx).Model(&model.QualityIssue{}).
		Where("tenant_id = ? AND id = ?", issue.TenantID, issue.ID).
		Select(
			"owner", "status", "resolution", "resolution_target_type", "resolution_target_id",
			"resolution_target_version", "eval_case_id", "evaluation_run_id", "release_id",
			"reopen_count", "reopen_reason", "previous_resolution", "previous_run_id", "updated_at",
		).Updates(issue).Error
}

func listTenantPage[T any](ctx context.Context, db *gorm.DB, dest *[]T, query, order string, page, pageSize int, args ...interface{}) ([]T, int64, error) {
	var total int64
	countQuery := db.WithContext(ctx).Model(dest).Where(query, args...)
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	listQuery := db.WithContext(ctx).Model(dest).Where(query, args...)
	if err := listQuery.Order(order).Offset(offset).Limit(limit).Find(dest).Error; err != nil {
		return nil, 0, err
	}
	return *dest, total, nil
}

func clauseOnConflictDoNothing() clause.Expression {
	return clause.OnConflict{DoNothing: true}
}
