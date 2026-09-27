package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type AnswerDeliveryRepo interface {
	CreateAnswerRun(ctx context.Context, run *model.AnswerRun) error
	UpdateAnswerRunTerminal(ctx context.Context, tenantID, runID string, lifecycle, status, completionReason string) error
	GetAnswerRunByRequest(ctx context.Context, tenantID, requestID string) (*model.AnswerRun, error)
	GetAnswerRun(ctx context.Context, tenantID, runID string) (*model.AnswerRun, error)
	GetAnswerSnapshotByRequest(ctx context.Context, tenantID, requestID string) (*model.AnswerSnapshot, error)
	CreateAnswerSnapshot(ctx context.Context, snapshot *model.AnswerSnapshot) error
	GetAnswerSnapshot(ctx context.Context, tenantID, snapshotID string) (*model.AnswerSnapshot, error)
	ListAnswerSnapshots(ctx context.Context, tenantID, sessionID string) ([]model.AnswerSnapshot, error)
	CreateAnswerEvent(ctx context.Context, event *model.AnswerEvent) error
	CreateAuthorizationProjection(ctx context.Context, projection *model.AuthorizationProjection) error
	GetAuthorizationProjection(ctx context.Context, tenantID, snapshotID, principalID, channel string) (*model.AuthorizationProjection, error)
	GetAuthorizationProjectionAny(ctx context.Context, tenantID, snapshotID string) (*model.AuthorizationProjection, error)
	UpsertDefaultExportTemplate(ctx context.Context, template *model.ExportTemplateVersion) error
	CreateExportSnapshot(ctx context.Context, snapshot *model.ExportSnapshot) error
	GetExportSnapshot(ctx context.Context, tenantID, snapshotID string) (*model.ExportSnapshot, error)
	CreateExportJob(ctx context.Context, job *model.ExportJob) error
	UpdateExportJob(ctx context.Context, tenantID, jobID string, updates map[string]interface{}) error
	GetExportJob(ctx context.Context, tenantID, jobID string) (*model.ExportJob, error)
	GetExportJobByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*model.ExportJob, error)
	CreateExportArtifact(ctx context.Context, artifact *model.ExportArtifact) error
	GetExportArtifactByJob(ctx context.Context, tenantID, jobID string) (*model.ExportArtifact, error)
	GetExportArtifact(ctx context.Context, tenantID, artifactID string) (*model.ExportArtifact, error)
	ListExpiredExportArtifacts(ctx context.Context, now time.Time, limit int) ([]model.ExportArtifact, error)
	ExpireExportArtifactJob(ctx context.Context, tenantID, artifactID string, now time.Time) error
	CreateExportDownloadAudit(ctx context.Context, audit *model.ExportDownloadAudit) error
}

func (s *store) CreateAnswerRun(ctx context.Context, run *model.AnswerRun) error {
	if run.ID == "" {
		run.ID = id.New()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(run).Error
}

func (s *store) UpdateAnswerRunTerminal(ctx context.Context, tenantID, runID, lifecycle, status, completionReason string) error {
	now := time.Now().UTC()
	result := s.WithContext(ctx).Model(&model.AnswerRun{}).
		Where("tenant_id = ? AND id = ? AND lifecycle_state IN ?", tenantID, runID,
			[]string{model.AnswerLifecycleInit, model.AnswerLifecycleGenerating, model.AnswerLifecycleFinalizing}).
		Updates(map[string]interface{}{
			"lifecycle_state": lifecycle, "answer_status": status,
			"completion_reason": completionReason, "completed_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *store) GetAnswerRunByRequest(ctx context.Context, tenantID, requestID string) (*model.AnswerRun, error) {
	var run model.AnswerRun
	err := s.WithContext(ctx).Where("tenant_id = ? AND request_id = ?", tenantID, requestID).First(&run).Error
	return &run, err
}

func (s *store) GetAnswerRun(ctx context.Context, tenantID, runID string) (*model.AnswerRun, error) {
	var run model.AnswerRun
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, runID).First(&run).Error
	return &run, err
}

func (s *store) GetAnswerSnapshotByRequest(ctx context.Context, tenantID, requestID string) (*model.AnswerSnapshot, error) {
	var snapshot model.AnswerSnapshot
	err := s.WithContext(ctx).
		Joins("JOIN rgx_answer_run ON rgx_answer_run.id = rgx_answer_snapshot.answer_run_id").
		Where("rgx_answer_snapshot.tenant_id = ? AND rgx_answer_run.request_id = ?", tenantID, requestID).
		Order("rgx_answer_snapshot.created_at DESC").First(&snapshot).Error
	return &snapshot, err
}

func (s *store) CreateAnswerSnapshot(ctx context.Context, snapshot *model.AnswerSnapshot) error {
	if snapshot.ID == "" {
		snapshot.ID = id.New()
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(snapshot).Error
}

func (s *store) GetAnswerSnapshot(ctx context.Context, tenantID, snapshotID string) (*model.AnswerSnapshot, error) {
	var snapshot model.AnswerSnapshot
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, snapshotID).First(&snapshot).Error
	return &snapshot, err
}

func (s *store) ListAnswerSnapshots(ctx context.Context, tenantID, sessionID string) ([]model.AnswerSnapshot, error) {
	var list []model.AnswerSnapshot
	err := s.WithContext(ctx).
		Joins("JOIN rgx_answer_run ON rgx_answer_run.id = rgx_answer_snapshot.answer_run_id").
		Where("rgx_answer_snapshot.tenant_id = ? AND rgx_answer_run.session_id = ?", tenantID, sessionID).
		Order("rgx_answer_snapshot.created_at ASC").
		Find(&list).Error
	return list, err
}

func (s *store) CreateAnswerEvent(ctx context.Context, event *model.AnswerEvent) error {
	if event.ID == "" {
		event.ID = id.New()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(event).Error
}

func (s *store) CreateAuthorizationProjection(ctx context.Context, projection *model.AuthorizationProjection) error {
	if projection.ID == "" {
		projection.ID = id.New()
	}
	if projection.CreatedAt.IsZero() {
		projection.CreatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(projection).Error
}

func (s *store) GetAuthorizationProjection(ctx context.Context, tenantID, snapshotID, principalID, channel string) (*model.AuthorizationProjection, error) {
	var projection model.AuthorizationProjection
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND answer_snapshot_id = ? AND principal_id = ? AND channel = ?",
			tenantID, snapshotID, principalID, channel).
		Order("created_at DESC").First(&projection).Error
	return &projection, err
}

// GetAuthorizationProjectionAny returns the newest projection for a snapshot
// regardless of principal/channel. It backs the audit-grade export channel
// (doc/124 §4.2), where the delivery-time projection is evidence rather than
// a visibility filter.
func (s *store) GetAuthorizationProjectionAny(ctx context.Context, tenantID, snapshotID string) (*model.AuthorizationProjection, error) {
	var projection model.AuthorizationProjection
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND answer_snapshot_id = ?", tenantID, snapshotID).
		Order("created_at DESC").First(&projection).Error
	return &projection, err
}

func (s *store) UpsertDefaultExportTemplate(ctx context.Context, template *model.ExportTemplateVersion) error {
	template.CreatedAt = time.Now().UTC()
	template.CreatedBy = "system"
	return s.WithContext(ctx).
		Where("tenant_id = ? AND template_id = ? AND version = ?", template.TenantID, template.TemplateID, template.Version).
		FirstOrCreate(template).Error
}

func (s *store) CreateExportSnapshot(ctx context.Context, snapshot *model.ExportSnapshot) error {
	if snapshot.ID == "" {
		snapshot.ID = id.New()
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(snapshot).Error
}

func (s *store) GetExportSnapshot(ctx context.Context, tenantID, snapshotID string) (*model.ExportSnapshot, error) {
	var snapshot model.ExportSnapshot
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, snapshotID).First(&snapshot).Error
	return &snapshot, err
}

func (s *store) CreateExportJob(ctx context.Context, job *model.ExportJob) error {
	if job.ID == "" {
		job.ID = id.New()
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(job).Error
}

func (s *store) UpdateExportJob(ctx context.Context, tenantID, jobID string, updates map[string]interface{}) error {
	result := s.WithContext(ctx).Model(&model.ExportJob{}).
		Where("tenant_id = ? AND id = ?", tenantID, jobID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *store) GetExportJob(ctx context.Context, tenantID, jobID string) (*model.ExportJob, error) {
	var job model.ExportJob
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, jobID).First(&job).Error
	return &job, err
}

func (s *store) GetExportJobByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*model.ExportJob, error) {
	var job model.ExportJob
	err := s.WithContext(ctx).Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &job, err
}

func (s *store) CreateExportArtifact(ctx context.Context, artifact *model.ExportArtifact) error {
	if artifact.ID == "" {
		artifact.ID = id.New()
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(artifact).Error
}

func (s *store) GetExportArtifactByJob(ctx context.Context, tenantID, jobID string) (*model.ExportArtifact, error) {
	var artifact model.ExportArtifact
	err := s.WithContext(ctx).Where("tenant_id = ? AND export_job_id = ?", tenantID, jobID).First(&artifact).Error
	return &artifact, err
}

func (s *store) GetExportArtifact(ctx context.Context, tenantID, artifactID string) (*model.ExportArtifact, error) {
	var artifact model.ExportArtifact
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, artifactID).First(&artifact).Error
	return &artifact, err
}

func (s *store) ListExpiredExportArtifacts(ctx context.Context, now time.Time, limit int) ([]model.ExportArtifact, error) {
	if limit <= 0 {
		limit = 100
	}
	var artifacts []model.ExportArtifact
	err := s.WithContext(ctx).
		Joins("JOIN rgx_export_job ON rgx_export_job.id = rgx_export_artifact.export_job_id AND rgx_export_job.tenant_id = rgx_export_artifact.tenant_id").
		Where("rgx_export_artifact.expires_at <= ? AND rgx_export_job.status <> ?", now, model.ExportJobExpired).
		Order("expires_at ASC").
		Limit(limit).
		Find(&artifacts).Error
	return artifacts, err
}

func (s *store) ExpireExportArtifactJob(ctx context.Context, tenantID, artifactID string, now time.Time) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var artifact model.ExportArtifact
		if err := tx.Where("tenant_id = ? AND id = ? AND expires_at <= ?", tenantID, artifactID, now).
			First(&artifact).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ExportJob{}).
			Where("tenant_id = ? AND id = ?", tenantID, artifact.ExportJobID).
			Update("status", model.ExportJobExpired)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *store) CreateExportDownloadAudit(ctx context.Context, audit *model.ExportDownloadAudit) error {
	if audit.ID == "" {
		audit.ID = id.New()
	}
	if audit.DownloadedAt.IsZero() {
		audit.DownloadedAt = time.Now().UTC()
	}
	audit.DownloadCount = 1
	return s.WithContext(ctx).Create(audit).Error
}
