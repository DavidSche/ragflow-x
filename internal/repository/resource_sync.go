package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// staleSyncRunLease is the refresh interval promised while a run is making
// progress. It is shorter than the service's stale-run recovery window.
const staleSyncRunLease = 5 * time.Minute

// ResourceSyncRepo persists RAGFlow import/reconciliation state.
type ResourceSyncRepo interface {
	GetResourceSyncSetting(ctx context.Context, sourceID string) (*model.ResourceSyncSetting, error)
	SaveResourceSyncSetting(ctx context.Context, setting *model.ResourceSyncSetting) error

	CreateSyncRun(ctx context.Context, run *model.SyncRun) error
	GetSyncRun(ctx context.Context, id string) (*model.SyncRun, error)
	BeginSyncRun(ctx context.Context, id, owner string, now time.Time) (bool, error)
	ListSyncRuns(ctx context.Context, page, pageSize int, sourceID, status string) ([]model.SyncRun, int64, error)
	UpdateSyncRun(ctx context.Context, run *model.SyncRun) error
	TouchSyncRunLease(ctx context.Context, id, owner string, now time.Time) error
	TouchSyncRunProgress(ctx context.Context, id, owner string, total, done, failed int, now time.Time) error
	FailStaleSyncRuns(ctx context.Context, sourceID string, before, now time.Time, reason string) (int64, error)
	CreateSyncItems(ctx context.Context, items []model.SyncItem) error
	ListSyncItems(ctx context.Context, runID string, page, pageSize int) ([]model.SyncItem, int64, error)
	GetSyncItem(ctx context.Context, id string) (*model.SyncItem, error)
	UpdateSyncItem(ctx context.Context, item *model.SyncItem) error
	MarkResourceSyncItemsSucceeded(ctx context.Context, resourceType, externalScopeKey, externalID, conflictType string, now time.Time) (int64, error)
	CreateResourceSyncArtifact(ctx context.Context, artifact *model.ResourceSyncArtifact, lifecycle *model.ResourceSyncArtifactLifecycle) error
	CreateResourceSyncArtifacts(ctx context.Context, artifacts []model.ResourceSyncArtifact, lifecycles []model.ResourceSyncArtifactLifecycle) error
	GetResourceSyncArtifact(ctx context.Context, id string) (*model.ResourceSyncArtifact, error)
	GetResourceSyncArtifactByHash(ctx context.Context, artifactType, contentHash string) (*model.ResourceSyncArtifact, error)
	ListResourceSyncArtifacts(ctx context.Context, page, pageSize int) ([]model.ResourceSyncArtifact, int64, error)
	ListResourceSyncArtifactsByIDs(ctx context.Context, ids []string) ([]model.ResourceSyncArtifact, error)
	GetResourceSyncArtifactLifecycle(ctx context.Context, artifactID string) (*model.ResourceSyncArtifactLifecycle, error)
	ListResourceSyncArtifactLifecyclesByIDs(ctx context.Context, artifactIDs []string) ([]model.ResourceSyncArtifactLifecycle, error)
	ExtendResourceSyncArtifactLifecycle(ctx context.Context, lifecycle *model.ResourceSyncArtifactLifecycle) error
	CountActiveSyncRuns(ctx context.Context, sourceID string) (int64, error)
	CreateSyncRunIfSourceIdle(ctx context.Context, run *model.SyncRun) (bool, error)

	UpsertTenantMapping(ctx context.Context, mapping *model.ResourceSyncTenantMapping) error
	ListTenantMappings(ctx context.Context, sourceID string) ([]model.ResourceSyncTenantMapping, error)
	GetTenantMapping(ctx context.Context, sourceID, externalTenantID string) (*model.ResourceSyncTenantMapping, error)
	DeleteTenantMapping(ctx context.Context, sourceID, externalTenantID string) error

	GetResourceBindingByIdentity(ctx context.Context, sourceID, resourceType, externalScopeKey, externalID string) (*model.ResourceBinding, error)
	GetResourceBindingByLocal(ctx context.Context, resourceType, localID string) (*model.ResourceBinding, error)
	ListResourceBindings(ctx context.Context, sourceID, resourceType, lifecycle string, page, pageSize int) ([]model.ResourceBinding, int64, error)
	ListAllResourceBindings(ctx context.Context, sourceID string, resourceTypes []string) ([]model.ResourceBinding, error)
	UpdateResourceBinding(ctx context.Context, binding *model.ResourceBinding) error
	IncrementMissingConfirmations(ctx context.Context, id string, now time.Time) (int, error)
	ResetMissingConfirmations(ctx context.Context, id string, now time.Time) error
	GetCurrentBindingVersion(ctx context.Context, bindingID string) (*model.ResourceBindingVersion, error)
	CountBindingVersions(ctx context.Context, bindingID string) (int64, error)
	ListBindingVersions(ctx context.Context, bindingID string, page, pageSize int) ([]model.ResourceBindingVersion, int64, error)
	CreateBindingVersionWithPointer(ctx context.Context, version *model.ResourceBindingVersion, baselineUpstreamHash, baselineLocalHash string) error
}

func (s *store) GetResourceSyncSetting(ctx context.Context, sourceID string) (*model.ResourceSyncSetting, error) {
	var setting model.ResourceSyncSetting
	err := s.WithContext(ctx).Where("source_id = ?", sourceID).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &setting, nil
}

func (s *store) SaveResourceSyncSetting(ctx context.Context, setting *model.ResourceSyncSetting) error {
	return s.WithContext(ctx).Save(setting).Error
}

func normalizeSyncRunJSON(run *model.SyncRun) {
	if strings.TrimSpace(run.ResourceTypesJSON) == "" {
		run.ResourceTypesJSON = "[]"
	}
	for _, field := range []*string{
		&run.ScopeJSON, &run.SourceSnapshotJSON, &run.PlanSummaryJSON, &run.ResultSummaryJSON,
	} {
		if strings.TrimSpace(*field) == "" {
			*field = "{}"
		}
	}
}

func (s *store) normalizedSyncRunForWrite(run *model.SyncRun) *model.SyncRun {
	persisted := *run
	normalizeSyncRunJSON(&persisted)
	if s.Dialector != nil && s.Dialector.Name() == "postgres" &&
		strings.HasPrefix(persisted.SourceSnapshotRef, "artifact:") &&
		strings.TrimSpace(persisted.SourceSnapshotJSON) == "" {
		persisted.SourceSnapshotJSON = "null"
	}
	return &persisted
}

func (s *store) CreateSyncRun(ctx context.Context, run *model.SyncRun) error {
	return s.WithContext(ctx).Create(s.normalizedSyncRunForWrite(run)).Error
}

func (s *store) GetSyncRun(ctx context.Context, id string) (*model.SyncRun, error) {
	var run model.SyncRun
	err := s.WithContext(ctx).Where("id = ?", id).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// BeginSyncRun atomically claims a PLANNED run as RUNNING. The status predicate
// prevents a direct API request and a queued worker from executing the same run.
func (s *store) BeginSyncRun(ctx context.Context, id, owner string, now time.Time) (bool, error) {
	result := s.WithContext(ctx).Model(&model.SyncRun{}).
		Where("id = ? AND status = ?", id, model.SyncRunPlanned).
		Updates(map[string]interface{}{
			"status":           model.SyncRunRunning,
			"started_at":       now,
			"lease_owner":      owner,
			"lease_expires_at": now.Add(staleSyncRunLease),
			"updated_at":       now,
		})
	return result.RowsAffected == 1, result.Error
}

func (s *store) ListSyncRuns(ctx context.Context, page, pageSize int, sourceID, status string) ([]model.SyncRun, int64, error) {
	var list []model.SyncRun
	var total int64
	q := s.WithContext(ctx).Model(&model.SyncRun{})
	if sourceID != "" {
		q = q.Where("source_id = ?", sourceID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) UpdateSyncRun(ctx context.Context, run *model.SyncRun) error {
	return s.WithContext(ctx).Save(s.normalizedSyncRunForWrite(run)).Error
}

func (s *store) TouchSyncRunLease(ctx context.Context, id, owner string, now time.Time) error {
	expiresAt := now.Add(staleSyncRunLease)
	return s.WithContext(ctx).Model(&model.SyncRun{}).
		Where("id = ? AND status IN ?", id, []string{model.SyncRunScanning, model.SyncRunRunning, model.SyncRunPlanned}).
		Updates(map[string]interface{}{
			"lease_owner":      owner,
			"lease_expires_at": expiresAt,
			"updated_at":       now,
		}).Error
}

// TouchSyncRunLeaseProgress advances the run-level progress counters and
// refreshes the lease in one statement so a long execution surfaces both
// "alive" and "how far along" without a separate read.
// TouchSyncRunProgress advances the run-level progress counters and refreshes
// the lease in one statement so a long execution surfaces both "alive" and
// "how far along" without a separate read. The total is written explicitly; done
// and failed are monotonic within a single execution.
func (s *store) TouchSyncRunProgress(ctx context.Context, id, owner string, total, done, failed int, now time.Time) error {
	expiresAt := now.Add(staleSyncRunLease)
	return s.WithContext(ctx).Model(&model.SyncRun{}).
		Where("id = ? AND status IN ?", id, []string{model.SyncRunScanning, model.SyncRunRunning, model.SyncRunPlanned}).
		Updates(map[string]interface{}{
			"progress_total":   total,
			"progress_done":    done,
			"progress_failed":  failed,
			"lease_owner":      owner,
			"lease_expires_at": expiresAt,
			"updated_at":       now,
		}).Error
}

func (s *store) FailStaleSyncRuns(ctx context.Context, sourceID string, before, now time.Time, reason string) (int64, error) {
	statuses := []string{model.SyncRunScanning, model.SyncRunRunning}
	query := s.WithContext(ctx).Model(&model.SyncRun{})
	if s.Dialector != nil && s.Dialector.Name() == "postgres" {
		query = query.Where("source_id = ? AND status IN ? AND (lease_expires_at IS NULL OR lease_expires_at < now())", sourceID, statuses)
	} else {
		query = query.Where("source_id = ? AND status IN ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)", sourceID, statuses, now)
	}
	result := query.Updates(map[string]interface{}{
		"status":      model.SyncRunFailed,
		"error":       reason,
		"finished_at": now,
		"updated_at":  now,
	})
	_ = before
	return result.RowsAffected, result.Error
}

func (s *store) CreateSyncItems(ctx context.Context, items []model.SyncItem) error {
	if len(items) == 0 {
		return nil
	}
	return s.WithContext(ctx).Create(&items).Error
}

// CreateSyncRunIfSourceIdle serializes a source-level scan/reconcile mutex in
// one transaction. Locking the durable setting row also blocks PostgreSQL
// transactions that see no active runs at the same instant.
func (s *store) CreateSyncRunIfSourceIdle(ctx context.Context, run *model.SyncRun) (bool, error) {
	var created bool
	persisted := s.normalizedSyncRunForWrite(run)
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var setting model.ResourceSyncSetting
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_id = ?", run.SourceID).
			First(&setting).Error; err != nil {
			return err
		}
		var active int64
		if err := tx.Model(&model.SyncRun{}).
			Where("source_id = ? AND status IN ?", run.SourceID, []string{
				model.SyncRunScanning, model.SyncRunRunning,
			}).
			Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		if err := tx.Create(persisted).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func (s *store) ListSyncItems(ctx context.Context, runID string, page, pageSize int) ([]model.SyncItem, int64, error) {
	var list []model.SyncItem
	var total int64
	q := s.WithContext(ctx).Model(&model.SyncItem{}).Where("run_id = ?", runID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("resource_type ASC, external_id ASC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) GetSyncItem(ctx context.Context, id string) (*model.SyncItem, error) {
	var item model.SyncItem
	err := s.WithContext(ctx).Where("id = ?", id).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *store) UpdateSyncItem(ctx context.Context, item *model.SyncItem) error {
	return s.WithContext(ctx).Save(item).Error
}

func (s *store) MarkResourceSyncItemsSucceeded(ctx context.Context, resourceType, externalScopeKey, externalID, conflictType string, now time.Time) (int64, error) {
	result := s.WithContext(ctx).Model(&model.SyncItem{}).
		Where(
			"resource_type = ? AND external_scope_key = ? AND external_id = ? AND conflict_type = ? AND action IN ? AND status = ?",
			resourceType, externalScopeKey, externalID, conflictType,
			[]string{model.SyncActionConflict, model.SyncActionRelink}, model.SyncItemStatusSkipped,
		).
		Updates(map[string]interface{}{
			"status":     model.SyncItemStatusSucceeded,
			"error":      "",
			"updated_at": now,
		})
	return result.RowsAffected, result.Error
}

func (s *store) CreateResourceSyncArtifact(ctx context.Context, artifact *model.ResourceSyncArtifact, lifecycle *model.ResourceSyncArtifactLifecycle) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(artifact).Error; err != nil {
			return err
		}
		return tx.Create(lifecycle).Error
	})
}

// CreateResourceSyncArtifacts persists a batch of artifacts and their
// lifecycles in a single transaction, replacing per-item round trips.
func (s *store) CreateResourceSyncArtifacts(ctx context.Context, artifacts []model.ResourceSyncArtifact, lifecycles []model.ResourceSyncArtifactLifecycle) error {
	if len(artifacts) == 0 {
		return nil
	}
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&artifacts).Error; err != nil {
			return err
		}
		return tx.Create(&lifecycles).Error
	})
}

func (s *store) GetResourceSyncArtifact(ctx context.Context, artifactID string) (*model.ResourceSyncArtifact, error) {
	var artifact model.ResourceSyncArtifact
	err := s.WithContext(ctx).Where("id = ?", artifactID).First(&artifact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (s *store) GetResourceSyncArtifactByHash(ctx context.Context, artifactType, contentHash string) (*model.ResourceSyncArtifact, error) {
	var artifact model.ResourceSyncArtifact
	err := s.WithContext(ctx).Where("artifact_type = ? AND content_hash = ?", artifactType, contentHash).First(&artifact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (s *store) ListResourceSyncArtifacts(ctx context.Context, page, pageSize int) ([]model.ResourceSyncArtifact, int64, error) {
	var total int64
	if err := s.WithContext(ctx).Model(&model.ResourceSyncArtifact{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	artifacts := []model.ResourceSyncArtifact{}
	offset, limit := paginate(page, pageSize)
	err := s.WithContext(ctx).Order("created_at ASC").Offset(offset).Limit(limit).Find(&artifacts).Error
	return artifacts, total, err
}

func (s *store) ListResourceSyncArtifactsByIDs(ctx context.Context, ids []string) ([]model.ResourceSyncArtifact, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.ResourceSyncArtifact
	err := s.WithContext(ctx).
		Joins("JOIN rgx_resource_sync_artifact_lifecycle ON rgx_resource_sync_artifact_lifecycle.artifact_id = rgx_resource_sync_artifact.id").
		Where("rgx_resource_sync_artifact.id IN ? AND (rgx_resource_sync_artifact_lifecycle.retain_until IS NULL OR rgx_resource_sync_artifact_lifecycle.retain_until >= ?)", ids, time.Now().UTC()).
		Find(&list).Error
	return list, err
}

func (s *store) GetResourceSyncArtifactLifecycle(ctx context.Context, artifactID string) (*model.ResourceSyncArtifactLifecycle, error) {
	var lifecycle model.ResourceSyncArtifactLifecycle
	err := s.WithContext(ctx).Where("artifact_id = ?", artifactID).First(&lifecycle).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lifecycle, nil
}

func (s *store) ListResourceSyncArtifactLifecyclesByIDs(ctx context.Context, artifactIDs []string) ([]model.ResourceSyncArtifactLifecycle, error) {
	if len(artifactIDs) == 0 {
		return nil, nil
	}
	var list []model.ResourceSyncArtifactLifecycle
	err := s.WithContext(ctx).Where("artifact_id IN ?", artifactIDs).Find(&list).Error
	return list, err
}

func (s *store) ExtendResourceSyncArtifactLifecycle(ctx context.Context, lifecycle *model.ResourceSyncArtifactLifecycle) error {
	if lifecycle.RetainUntil == nil {
		result := s.WithContext(ctx).Model(&model.ResourceSyncArtifactLifecycle{}).
			Where("artifact_id = ?", lifecycle.ArtifactID).
			Updates(map[string]interface{}{"retain_until": nil, "updated_at": lifecycle.UpdatedAt})
		if result.Error != nil {
			return result.Error
		}
		return nil
	}
	result := s.WithContext(ctx).Model(&model.ResourceSyncArtifactLifecycle{}).
		Where("artifact_id = ? AND (retain_until IS NULL OR retain_until < ?)", lifecycle.ArtifactID, lifecycle.RetainUntil).
		Updates(map[string]interface{}{"retain_until": lifecycle.RetainUntil, "updated_at": lifecycle.UpdatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var count int64
	if err := s.WithContext(ctx).Model(&model.ResourceSyncArtifactLifecycle{}).
		Where("artifact_id = ?", lifecycle.ArtifactID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return s.WithContext(ctx).Create(lifecycle).Error
}

func (s *store) CountActiveSyncRuns(ctx context.Context, sourceID string) (int64, error) {
	var count int64
	err := s.WithContext(ctx).Model(&model.SyncRun{}).
		Where("source_id = ? AND status IN ?", sourceID, []string{
			model.SyncRunPending, model.SyncRunScanning, model.SyncRunPlanned, model.SyncRunRunning,
		}).Count(&count).Error
	return count, err
}

func (s *store) UpsertTenantMapping(ctx context.Context, mapping *model.ResourceSyncTenantMapping) error {
	return s.WithContext(ctx).Save(mapping).Error
}

func (s *store) ListTenantMappings(ctx context.Context, sourceID string) ([]model.ResourceSyncTenantMapping, error) {
	var list []model.ResourceSyncTenantMapping
	err := s.WithContext(ctx).Where("source_id = ?", sourceID).Order("external_tenant_id ASC").Find(&list).Error
	return list, err
}

func (s *store) GetTenantMapping(ctx context.Context, sourceID, externalTenantID string) (*model.ResourceSyncTenantMapping, error) {
	var mapping model.ResourceSyncTenantMapping
	err := s.WithContext(ctx).Where("source_id = ? AND external_tenant_id = ?", sourceID, externalTenantID).First(&mapping).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &mapping, nil
}

func (s *store) DeleteTenantMapping(ctx context.Context, sourceID, externalTenantID string) error {
	return s.WithContext(ctx).
		Where("source_id = ? AND external_tenant_id = ?", sourceID, externalTenantID).
		Delete(&model.ResourceSyncTenantMapping{}).Error
}

func (s *store) GetResourceBindingByIdentity(ctx context.Context, sourceID, resourceType, externalScopeKey, externalID string) (*model.ResourceBinding, error) {
	var binding model.ResourceBinding
	err := s.WithContext(ctx).
		Where("source_id = ? AND resource_type = ? AND external_scope_key = ? AND external_id = ?",
			sourceID, resourceType, externalScopeKey, externalID).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (s *store) GetResourceBindingByLocal(ctx context.Context, resourceType, localID string) (*model.ResourceBinding, error) {
	var binding model.ResourceBinding
	err := s.WithContext(ctx).
		Where("resource_type = ? AND local_id = ?", resourceType, localID).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (s *store) ListResourceBindings(ctx context.Context, sourceID, resourceType, lifecycle string, page, pageSize int) ([]model.ResourceBinding, int64, error) {
	var list []model.ResourceBinding
	var total int64
	q := s.WithContext(ctx).Model(&model.ResourceBinding{}).Where("source_id = ?", sourceID)
	if resourceType != "" {
		q = q.Where("resource_type = ?", resourceType)
	}
	if lifecycle != "" {
		q = q.Where("binding_lifecycle = ?", lifecycle)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) UpdateResourceBinding(ctx context.Context, binding *model.ResourceBinding) error {
	return s.WithContext(ctx).Save(binding).Error
}

func (s *store) ListAllResourceBindings(ctx context.Context, sourceID string, resourceTypes []string) ([]model.ResourceBinding, error) {
	var list []model.ResourceBinding
	q := s.WithContext(ctx).Where("source_id = ?", sourceID)
	if len(resourceTypes) > 0 {
		q = q.Where("resource_type IN ?", resourceTypes)
	}
	err := q.Order("resource_type ASC, external_id ASC").Find(&list).Error
	return list, err
}

func (s *store) IncrementMissingConfirmations(ctx context.Context, id string, now time.Time) (int, error) {
	result := s.WithContext(ctx).Model(&model.ResourceBinding{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"missing_confirmations": gorm.Expr("missing_confirmations + 1"),
			"updated_at":            now,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	var binding model.ResourceBinding
	if err := s.WithContext(ctx).Where("id = ?", id).First(&binding).Error; err != nil {
		return 0, err
	}
	return binding.MissingConfirmations, nil
}

func (s *store) ResetMissingConfirmations(ctx context.Context, id string, now time.Time) error {
	return s.WithContext(ctx).Model(&model.ResourceBinding{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{"missing_confirmations": 0, "last_seen_at": now, "updated_at": now}).Error
}

func (s *store) GetCurrentBindingVersion(ctx context.Context, bindingID string) (*model.ResourceBindingVersion, error) {
	var version model.ResourceBindingVersion
	err := s.WithContext(ctx).
		Where("binding_id = ?", bindingID).
		Order("version DESC").
		First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (s *store) CountBindingVersions(ctx context.Context, bindingID string) (int64, error) {
	var count int64
	err := s.WithContext(ctx).Model(&model.ResourceBindingVersion{}).
		Where("binding_id = ?", bindingID).
		Count(&count).Error
	return count, err
}

func (s *store) ListBindingVersions(ctx context.Context, bindingID string, page, pageSize int) ([]model.ResourceBindingVersion, int64, error) {
	var list []model.ResourceBindingVersion
	var total int64
	q := s.WithContext(ctx).Model(&model.ResourceBindingVersion{}).Where("binding_id = ?", bindingID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("version DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// CreateBindingVersionWithPointer atomically appends an immutable version and
// moves the binding's current pointer and conflict baseline.
func (s *store) CreateBindingVersionWithPointer(ctx context.Context, version *model.ResourceBindingVersion, baselineUpstreamHash, baselineLocalHash string) error {
	return s.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{
			"current_binding_version_id": version.ID,
			"last_synced_upstream_hash":  baselineUpstreamHash,
			"last_synced_local_hash":     baselineLocalHash,
			"last_synced_at":             version.CreatedAt,
			"updated_at":                 version.CreatedAt,
		}
		return tx.Model(&model.ResourceBinding{}).
			Where("id = ?", version.BindingID).
			Updates(updates).Error
	})
}
