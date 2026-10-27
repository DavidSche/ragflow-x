package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

// EvidenceSnapshotBundle returns the immutable snapshot and its associated
// evidence and dependency rows in one tenant-scoped read.
type EvidenceSnapshotBundle struct {
	Snapshot     *model.EvidenceSnapshot
	Evidence     *model.EvalCaseEvidence
	Dependencies []model.EvalCaseDependency
}

// EvidenceSnapshotFilter narrows snapshot reads without exposing business
// question or answer content.
type EvidenceSnapshotFilter struct {
	EvalCaseID   string
	StaleStatus  string
	SnapshotHash string
	DatasetIDs   []string
}

// EvidenceSnapshotListItem combines an immutable snapshot with lifecycle state
// and a dependency count for compact list views.
type EvidenceSnapshotListItem struct {
	Snapshot        model.EvidenceSnapshot
	Evidence        model.EvalCaseEvidence
	DependencyCount int64
}

func (s *store) GetEvalCase(ctx context.Context, tenantID, evalCaseID string) (*model.EvalCase, error) {
	var evalCase model.EvalCase
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, evalCaseID).First(&evalCase).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &evalCase, nil
}

func (s *store) CreateEvidenceSnapshotBundle(
	ctx context.Context, snapshot *model.EvidenceSnapshot, evidence *model.EvalCaseEvidence,
	dependencies []model.EvalCaseDependency, audit *model.AuditLog,
) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(snapshot).Error; err != nil {
			return err
		}
		if err := tx.Create(evidence).Error; err != nil {
			return err
		}
		if len(dependencies) > 0 {
			if err := tx.Create(&dependencies).Error; err != nil {
				return err
			}
		}
		if audit != nil {
			auditStore := &store{DB: tx}
			if err := auditStore.CreateAudit(ctx, audit); err != nil {
				return err
			}
		}
		return createEvidenceSnapshotDatasetProjection(tx, snapshot)
	})
}

func (s *store) GetEvidenceSnapshotBundle(ctx context.Context, tenantID, snapshotID string) (*EvidenceSnapshotBundle, error) {
	snapshot := &model.EvidenceSnapshot{}
	if err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, snapshotID).
		First(snapshot).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	evidence := &model.EvalCaseEvidence{}
	if err := s.WithContext(ctx).Where("tenant_id = ? AND evidence_snapshot_id = ?", tenantID, snapshotID).
		First(evidence).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	dependencies := make([]model.EvalCaseDependency, 0)
	if err := s.WithContext(ctx).Where("tenant_id = ? AND eval_case_evidence_id = ?", tenantID, evidence.ID).
		Order("logical_document_id ASC, binding_type ASC").Find(&dependencies).Error; err != nil {
		return nil, err
	}
	return &EvidenceSnapshotBundle{
		Snapshot: snapshot, Evidence: evidence, Dependencies: dependencies,
	}, nil
}

func (s *store) ListEvidenceSnapshotDatasetIDs(ctx context.Context, tenantID, snapshotID string) ([]string, error) {
	datasetIDs := make([]string, 0)
	if err := s.WithContext(ctx).Model(&model.EvidenceSnapshotDataset{}).
		Where("tenant_id = ? AND snapshot_id = ?", tenantID, snapshotID).
		Order("dataset_id ASC").Pluck("dataset_id", &datasetIDs).Error; err != nil {
		return nil, err
	}
	return datasetIDs, nil
}

func (s *store) GetEvalCaseEvidenceBundle(ctx context.Context, tenantID, evalCaseID string) (*EvidenceSnapshotBundle, error) {
	evidence := &model.EvalCaseEvidence{}
	if err := s.WithContext(ctx).Where("tenant_id = ? AND eval_case_id = ?", tenantID, evalCaseID).
		First(evidence).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	snapshot := &model.EvidenceSnapshot{}
	if err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, evidence.EvidenceSnapshotID).
		First(snapshot).Error; err != nil {
		return nil, err
	}
	dependencies := make([]model.EvalCaseDependency, 0)
	if err := s.WithContext(ctx).Where("tenant_id = ? AND eval_case_evidence_id = ?", tenantID, evidence.ID).
		Order("logical_document_id ASC, binding_type ASC").Find(&dependencies).Error; err != nil {
		return nil, err
	}
	return &EvidenceSnapshotBundle{
		Snapshot: snapshot, Evidence: evidence, Dependencies: dependencies,
	}, nil
}

func (s *store) CreateOrReplaceEvidenceSnapshotBundle(
	ctx context.Context, snapshot *model.EvidenceSnapshot, evidence *model.EvalCaseEvidence,
	dependencies []model.EvalCaseDependency, audit *model.AuditLog,
) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		previousEvidence := tx.Model(&model.EvalCaseEvidence{}).
			Select("id").
			Where("tenant_id = ? AND eval_case_id = ? AND id <> ?", evidence.TenantID, evidence.EvalCaseID, evidence.ID)
		if err := tx.Where("tenant_id = ? AND eval_case_evidence_id IN (?)", evidence.TenantID, previousEvidence).
			Delete(&model.EvalCaseDependency{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND eval_case_id = ? AND id <> ?", evidence.TenantID, evidence.EvalCaseID, evidence.ID).
			Delete(&model.EvalCaseEvidence{}).Error; err != nil {
			return err
		}
		if err := tx.Create(snapshot).Error; err != nil {
			return err
		}
		if err := tx.Create(evidence).Error; err != nil {
			return err
		}
		if len(dependencies) > 0 {
			if err := tx.Create(&dependencies).Error; err != nil {
				return err
			}
		}
		if audit != nil {
			auditStore := &store{DB: tx}
			if err := auditStore.CreateAudit(ctx, audit); err != nil {
				return err
			}
		}
		return createEvidenceSnapshotDatasetProjection(tx, snapshot)
	})
}

func createEvidenceSnapshotDatasetProjection(tx *gorm.DB, snapshot *model.EvidenceSnapshot) error {
	var datasetIDs []string
	if err := json.Unmarshal([]byte(snapshot.DatasetIDs), &datasetIDs); err != nil {
		return err
	}
	if len(datasetIDs) == 0 {
		return gorm.ErrRecordNotFound
	}
	projections := make([]model.EvidenceSnapshotDataset, 0, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		if datasetID == "" {
			return gorm.ErrRecordNotFound
		}
		projections = append(projections, model.EvidenceSnapshotDataset{
			ID: id.New(), TenantID: snapshot.TenantID,
			SnapshotID: snapshot.ID, DatasetID: datasetID,
		})
	}
	return tx.Create(&projections).Error
}

func (s *store) UpdateEvalCaseEvidenceTransition(
	ctx context.Context, tenantID, evidenceID, fromStatus, toStatus string, revalidatedAt *time.Time,
) (bool, error) {
	updates := map[string]interface{}{"stale_status": toStatus}
	if revalidatedAt != nil {
		updates["revalidated_at"] = *revalidatedAt
	}
	result := s.WithContext(ctx).Model(&model.EvalCaseEvidence{}).
		Where("tenant_id = ? AND id = ? AND stale_status = ?", tenantID, evidenceID, fromStatus).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *store) ListEvidenceSnapshots(
	ctx context.Context, tenantID string, filter EvidenceSnapshotFilter, page, pageSize int,
) ([]EvidenceSnapshotListItem, int64, error) {
	query := s.WithContext(ctx).Model(&model.EvidenceSnapshot{}).Where("tenant_id = ?", tenantID)
	if filter.EvalCaseID != "" {
		query = query.Where("id IN (?)", s.WithContext(ctx).Model(&model.EvalCaseEvidence{}).
			Select("evidence_snapshot_id").
			Where("tenant_id = ? AND eval_case_id = ?", tenantID, filter.EvalCaseID),
		)
	}
	if filter.StaleStatus != "" {
		query = query.Where("id IN (?)", s.WithContext(ctx).Model(&model.EvalCaseEvidence{}).
			Select("evidence_snapshot_id").
			Where("tenant_id = ? AND stale_status = ?", tenantID, filter.StaleStatus),
		)
	}
	if filter.SnapshotHash != "" {
		query = query.Where("snapshot_hash = ?", filter.SnapshotHash)
	}
	if len(filter.DatasetIDs) > 0 {
		query = query.Where(
			"NOT EXISTS (SELECT 1 FROM rgx_evidence_snapshot_dataset projection "+
				"WHERE projection.tenant_id = rgx_evidence_snapshot.tenant_id "+
				"AND projection.snapshot_id = rgx_evidence_snapshot.id "+
				"AND projection.dataset_id NOT IN ?)",
			filter.DatasetIDs,
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []EvidenceSnapshotListItem{}, 0, nil
	}
	snapshots := make([]model.EvidenceSnapshot, 0, min(pageSize, 200))
	if err := query.Order("created_at DESC, id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&snapshots).Error; err != nil {
		return nil, 0, err
	}
	snapshotIDs := make([]string, 0, len(snapshots))
	for index := range snapshots {
		snapshotIDs = append(snapshotIDs, snapshots[index].ID)
	}

	evidenceRows := make([]model.EvalCaseEvidence, 0, len(snapshots))
	if err := s.WithContext(ctx).Where("tenant_id = ? AND evidence_snapshot_id IN ?", tenantID, snapshotIDs).
		Order("created_at DESC, id DESC").Find(&evidenceRows).Error; err != nil {
		return nil, 0, err
	}
	evidenceBySnapshot := make(map[string]model.EvalCaseEvidence, len(evidenceRows))
	for _, evidence := range evidenceRows {
		evidenceBySnapshot[evidence.EvidenceSnapshotID] = evidence
	}
	evidenceIDs := make([]string, 0, len(evidenceRows))
	for _, evidence := range evidenceRows {
		evidenceIDs = append(evidenceIDs, evidence.ID)
	}
	dependencyRows := make([]model.EvalCaseDependency, 0)
	if err := s.WithContext(ctx).Where("tenant_id = ? AND eval_case_evidence_id IN ?", tenantID, evidenceIDs).
		Find(&dependencyRows).Error; err != nil {
		return nil, 0, err
	}
	countByEvidence := make(map[string]int64, len(evidenceRows))
	for _, dependency := range dependencyRows {
		countByEvidence[dependency.EvalCaseEvidenceID]++
	}

	items := make([]EvidenceSnapshotListItem, 0, len(snapshots))
	for _, snapshot := range snapshots {
		evidence, exists := evidenceBySnapshot[snapshot.ID]
		if !exists {
			return nil, 0, gorm.ErrRecordNotFound
		}
		items = append(items, EvidenceSnapshotListItem{
			Snapshot: snapshot, Evidence: evidence,
			DependencyCount: countByEvidence[evidence.ID],
		})
	}
	return items, total, nil
}
