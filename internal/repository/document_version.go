package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// DocumentVersionFilter narrows logical document reads without weakening the
// mandatory tenant boundary.
type DocumentVersionFilter struct {
	DatasetID string
}

// DocumentVersionRepo persists stable logical identities and their immutable
// business versions.
type DocumentVersionRepo interface {
	CreateLogicalDocument(ctx context.Context, document *model.LogicalDocument) error
	GetLogicalDocument(ctx context.Context, tenantID, id string) (*model.LogicalDocument, error)
	ListLogicalDocuments(ctx context.Context, tenantID string, filter DocumentVersionFilter, page, pageSize int) ([]model.LogicalDocument, int64, error)
	UpdateLogicalDocument(ctx context.Context, document *model.LogicalDocument) error
	DeleteLogicalDocument(ctx context.Context, tenantID, id string) error

	CreateDocumentVersion(ctx context.Context, version *model.DocumentVersion) error
	GetDocumentVersion(ctx context.Context, tenantID, id string) (*model.DocumentVersion, error)
	ListDocumentVersions(ctx context.Context, tenantID, logicalDocumentID string, page, pageSize int) ([]model.DocumentVersion, int64, error)
	FindDocumentVersionByContentHash(ctx context.Context, tenantID, logicalDocumentID, contentHash string) (*model.DocumentVersion, error)
	CountDocumentVersions(ctx context.Context, tenantID, logicalDocumentID string) (int64, error)
	NextDocumentVersionNo(ctx context.Context, tenantID, logicalDocumentID string) (int64, error)
	ListGovernanceDocumentVersions(ctx context.Context, tenantID, logicalDocumentID string) ([]model.DocumentVersion, error)
	UpdateDocumentVersion(ctx context.Context, version *model.DocumentVersion) error
	UpdateDocumentVersionStatus(ctx context.Context, tenantID, id, from, to string) (bool, error)

	GetVersionFilterPolicy(ctx context.Context, tenantID string) (*model.VersionFilterPolicy, error)
	UpsertVersionFilterPolicy(ctx context.Context, policy *model.VersionFilterPolicy) error
	GetActiveDocumentVersion(ctx context.Context, tenantID, logicalDocumentID string) (*model.DocumentVersion, error)
	CreateVersionPublishAttempt(ctx context.Context, attempt *model.VersionPublishAttempt) error
	GetVersionPublishAttempt(ctx context.Context, tenantID, id string) (*model.VersionPublishAttempt, error)
	GetOpenVersionPublishAttempt(ctx context.Context, tenantID, logicalDocumentID string) (*model.VersionPublishAttempt, error)
	UpdateVersionPublishAttempt(ctx context.Context, attempt *model.VersionPublishAttempt) error
	ListVersionPublishAttempts(ctx context.Context, tenantID, logicalDocumentID string, page, pageSize int) ([]model.VersionPublishAttempt, int64, error)
	ClaimDueVersionPublishAttempts(ctx context.Context, limit int, now time.Time, idleFor, leaseDuration time.Duration) ([]model.VersionPublishAttempt, error)
	RetryVersionPublishAttempt(ctx context.Context, id string, attempts int, lastError string, now time.Time) error

	CreateOutboxEvent(ctx context.Context, event *model.OutboxEvent) error
	ListOutboxEvents(ctx context.Context, tenantID string, filter OutboxEventFilter, page, pageSize int) ([]OutboxEventView, int64, error)
	GetOutboxEvent(ctx context.Context, tenantID, id string) (*OutboxEventView, error)
	RetryOutboxEventForTenant(ctx context.Context, tenantID, id string, now time.Time) (*OutboxEventView, bool, error)
	ClaimOutboxEvents(ctx context.Context, limit int, now time.Time, leaseDuration time.Duration) ([]model.OutboxEvent, error)
	MarkOutboxEventPublished(ctx context.Context, id string, now time.Time, result string) (bool, error)
	MarkDocumentVersionPublishedImpact(
		ctx context.Context, tenantID, eventID, logicalDocumentID string, now time.Time,
		buildResult func(affected int64) (string, error),
	) (int64, bool, error)
	RetryOutboxEvent(ctx context.Context, id string, attempts int, nextRetryAt time.Time, lastError string) error
}

const (
	OutboxEventStatusPending   = "pending"
	OutboxEventStatusPublished = "published"
	OutboxEventStatusRetrying  = "retrying"
)

type OutboxEventFilter struct {
	EventType     string
	AggregateType string
	Status        string
}

// OutboxEventView intentionally excludes the domain payload and raw error text.
type OutboxEventView struct {
	ID             string                 `json:"id"`
	EventType      string                 `json:"event_type"`
	AggregateType  string                 `json:"aggregate_type"`
	AggregateID    string                 `json:"aggregate_id"`
	OccurredAt     time.Time              `json:"occurred_at"`
	PublishedAt    *time.Time             `json:"published_at"`
	Attempts       int                    `json:"attempts"`
	NextRetryAt    *time.Time             `json:"next_retry_at"`
	ClaimedAt      *time.Time             `json:"claimed_at"`
	ClaimExpiresAt *time.Time             `json:"claim_expires_at"`
	Status         string                 `json:"status"`
	Result         map[string]interface{} `json:"result,omitempty"`
}

func outboxEventView(event *model.OutboxEvent) *OutboxEventView {
	status := OutboxEventStatusPending
	if event.PublishedAt != nil {
		status = OutboxEventStatusPublished
	} else if event.Attempts > 0 {
		status = OutboxEventStatusRetrying
	}
	view := &OutboxEventView{
		ID: event.ID, EventType: event.EventType, AggregateType: event.AggregateType,
		AggregateID: event.AggregateID, OccurredAt: event.OccurredAt,
		PublishedAt: event.PublishedAt, Attempts: event.Attempts,
		NextRetryAt: event.NextRetryAt, ClaimedAt: event.ClaimedAt,
		ClaimExpiresAt: event.ClaimExpiresAt, Status: status,
	}
	if event.Result != "" {
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(event.Result), &result); err == nil {
			view.Result = sanitizeOutboxResult(result)
		}
	}
	return view
}

func sanitizeOutboxResult(result map[string]interface{}) map[string]interface{} {
	sanitized := make(map[string]interface{}, 2)
	if status, ok := result["status"].(string); ok {
		sanitized["status"] = status
	}
	if affected, ok := result["affected_eval_case_dependencies"].(float64); ok {
		sanitized["affected_eval_case_dependencies"] = affected
	}
	return sanitized
}

func (s *store) CreateLogicalDocument(ctx context.Context, document *model.LogicalDocument) error {
	return s.WithContext(ctx).Create(document).Error
}

func (s *store) GetLogicalDocument(ctx context.Context, tenantID, id string) (*model.LogicalDocument, error) {
	var document model.LogicalDocument
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&document).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &document, nil
}

func (s *store) ListLogicalDocuments(ctx context.Context, tenantID string, filter DocumentVersionFilter, page, pageSize int) ([]model.LogicalDocument, int64, error) {
	query := "tenant_id = ?"
	args := []interface{}{tenantID}
	if filter.DatasetID != "" {
		query += " AND dataset_id = ?"
		args = append(args, filter.DatasetID)
	}
	return listTenantPage(ctx, s.DB, &[]model.LogicalDocument{}, query, "created_at DESC, id DESC", page, pageSize, args...)
}

func (s *store) UpdateLogicalDocument(ctx context.Context, document *model.LogicalDocument) error {
	return s.WithContext(ctx).Save(document).Error
}

func (s *store) DeleteLogicalDocument(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.LogicalDocument{}).Error
}

func (s *store) CreateDocumentVersion(ctx context.Context, version *model.DocumentVersion) error {
	return s.WithContext(ctx).Create(version).Error
}

func (s *store) GetDocumentVersion(ctx context.Context, tenantID, id string) (*model.DocumentVersion, error) {
	var version model.DocumentVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (s *store) ListDocumentVersions(ctx context.Context, tenantID, logicalDocumentID string, page, pageSize int) ([]model.DocumentVersion, int64, error) {
	return listTenantPage(ctx, s.DB, &[]model.DocumentVersion{}, "tenant_id = ? AND logical_document_id = ?", "version ASC", page, pageSize, tenantID, logicalDocumentID)
}

func (s *store) FindDocumentVersionByContentHash(ctx context.Context, tenantID, logicalDocumentID, contentHash string) (*model.DocumentVersion, error) {
	var version model.DocumentVersion
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND logical_document_id = ? AND content_hash = ?", tenantID, logicalDocumentID, contentHash,
	).First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (s *store) CountDocumentVersions(ctx context.Context, tenantID, logicalDocumentID string) (int64, error) {
	var count int64
	err := s.WithContext(ctx).Model(&model.DocumentVersion{}).
		Where("tenant_id = ? AND logical_document_id = ?", tenantID, logicalDocumentID).
		Count(&count).Error
	return count, err
}

func (s *store) NextDocumentVersionNo(ctx context.Context, tenantID, logicalDocumentID string) (int64, error) {
	var versionNo int64
	err := s.WithContext(ctx).Model(&model.DocumentVersion{}).
		Where("tenant_id = ? AND logical_document_id = ?", tenantID, logicalDocumentID).
		Select("COALESCE(MAX(version), 0)").Scan(&versionNo).Error
	return versionNo + 1, err
}

func (s *store) ListGovernanceDocumentVersions(ctx context.Context, tenantID, logicalDocumentID string) ([]model.DocumentVersion, error) {
	versions := make([]model.DocumentVersion, 0)
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND logical_document_id = ? AND status IN (?)",
		tenantID, logicalDocumentID,
		[]string{model.DocumentVersionPublishing, model.DocumentVersionActive, model.DocumentVersionSuperseded},
	).Find(&versions).Error
	return versions, err
}

func (s *store) UpdateDocumentVersion(ctx context.Context, version *model.DocumentVersion) error {
	return s.WithContext(ctx).Save(version).Error
}

func (s *store) UpdateDocumentVersionStatus(ctx context.Context, tenantID, id, from, to string) (bool, error) {
	result := s.WithContext(ctx).Model(&model.DocumentVersion{}).
		Where("tenant_id = ? AND id = ? AND status = ?", tenantID, id, from).
		Update("status", to)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (s *store) GetVersionFilterPolicy(ctx context.Context, tenantID string) (*model.VersionFilterPolicy, error) {
	var policy model.VersionFilterPolicy
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func (s *store) UpsertVersionFilterPolicy(ctx context.Context, policy *model.VersionFilterPolicy) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"max_pushdown_ids":  policy.MaxPushdownIDs,
			"max_request_bytes": policy.MaxRequestBytes,
			"updated_at":        policy.UpdatedAt,
		}),
	}).Create(policy).Error
}

func (s *store) GetActiveDocumentVersion(ctx context.Context, tenantID, logicalDocumentID string) (*model.DocumentVersion, error) {
	var version model.DocumentVersion
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND logical_document_id = ? AND status = ?",
		tenantID, logicalDocumentID, model.DocumentVersionActive,
	).First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (s *store) CreateVersionPublishAttempt(ctx context.Context, attempt *model.VersionPublishAttempt) error {
	return s.WithContext(ctx).Create(attempt).Error
}

func (s *store) GetVersionPublishAttempt(ctx context.Context, tenantID, id string) (*model.VersionPublishAttempt, error) {
	var attempt model.VersionPublishAttempt
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}

func (s *store) GetOpenVersionPublishAttempt(ctx context.Context, tenantID, logicalDocumentID string) (*model.VersionPublishAttempt, error) {
	var attempt model.VersionPublishAttempt
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND logical_document_id = ? AND state IN (?)",
		tenantID, logicalDocumentID,
		[]string{model.VersionPublishPreparing, model.VersionPublishRAGFlowUpdate, model.VersionPublishManualReview},
	).First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}

func (s *store) UpdateVersionPublishAttempt(ctx context.Context, attempt *model.VersionPublishAttempt) error {
	return s.WithContext(ctx).Save(attempt).Error
}

func (s *store) ListVersionPublishAttempts(ctx context.Context, tenantID, logicalDocumentID string, page, pageSize int) ([]model.VersionPublishAttempt, int64, error) {
	return listTenantPage(
		ctx, s.DB, &[]model.VersionPublishAttempt{},
		"tenant_id = ? AND logical_document_id = ?", "created_at DESC, id DESC",
		page, pageSize, tenantID, logicalDocumentID,
	)
}

func (s *store) ClaimDueVersionPublishAttempts(ctx context.Context, limit int, now time.Time, idleFor, leaseDuration time.Duration) ([]model.VersionPublishAttempt, error) {
	if limit <= 0 {
		limit = 1
	}
	if idleFor < 0 {
		idleFor = 0
	}
	if leaseDuration <= 0 {
		leaseDuration = time.Minute
	}
	attempts := make([]model.VersionPublishAttempt, 0, limit)
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&model.VersionPublishAttempt{}).
			Where("state IN (?) AND (claim_expires_at IS NULL OR claim_expires_at <= ?) AND updated_at <= ?",
				[]string{model.VersionPublishPreparing, model.VersionPublishRAGFlowUpdate},
				now, now.Add(-idleFor),
			).
			Order("updated_at ASC, id ASC").
			Limit(limit).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		expiresAt := now.Add(leaseDuration)
		claimedIDs := make([]string, 0, len(ids))
		for _, attemptID := range ids {
			result := tx.Model(&model.VersionPublishAttempt{}).
				Where("id = ? AND state IN (?) AND (claim_expires_at IS NULL OR claim_expires_at <= ?)",
					attemptID,
					[]string{model.VersionPublishPreparing, model.VersionPublishRAGFlowUpdate},
					now,
				).
				Updates(map[string]interface{}{
					"claimed_at":       now,
					"claim_expires_at": expiresAt,
					"updated_at":       now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				claimedIDs = append(claimedIDs, attemptID)
			}
		}
		if len(claimedIDs) == 0 {
			return nil
		}
		return tx.Where("id IN ?", claimedIDs).Order("updated_at ASC, id ASC").Find(&attempts).Error
	})
	if err != nil {
		return nil, err
	}
	return attempts, nil
}

func (s *store) RetryVersionPublishAttempt(ctx context.Context, id string, attempts int, lastError string, now time.Time) error {
	return s.WithContext(ctx).Model(&model.VersionPublishAttempt{}).
		Where("id = ? AND state IN (?)", id,
			[]string{model.VersionPublishPreparing, model.VersionPublishRAGFlowUpdate},
		).
		Updates(map[string]interface{}{
			"attempts":         attempts,
			"last_error":       lastError,
			"claimed_at":       nil,
			"claim_expires_at": nil,
			"updated_at":       now,
		}).Error
}

func (s *store) CreateOutboxEvent(ctx context.Context, event *model.OutboxEvent) error {
	return s.WithContext(ctx).Create(event).Error
}

func (s *store) ListOutboxEvents(
	ctx context.Context, tenantID string, filter OutboxEventFilter, page, pageSize int,
) ([]OutboxEventView, int64, error) {
	query := s.WithContext(ctx).Model(&model.OutboxEvent{}).Where("tenant_id = ?", tenantID)
	if filter.EventType != "" {
		query = query.Where("event_type = ?", filter.EventType)
	}
	if filter.AggregateType != "" {
		query = query.Where("aggregate_type = ?", filter.AggregateType)
	}
	switch filter.Status {
	case OutboxEventStatusPublished:
		query = query.Where("published_at IS NOT NULL")
	case OutboxEventStatusPending:
		query = query.Where("published_at IS NULL AND attempts <= 0")
	case OutboxEventStatusRetrying:
		query = query.Where("published_at IS NULL AND attempts > 0")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	events := make([]model.OutboxEvent, 0, limit)
	if err := query.Order("occurred_at DESC, id DESC").Offset(offset).Limit(limit).Find(&events).Error; err != nil {
		return nil, 0, err
	}
	views := make([]OutboxEventView, 0, len(events))
	for index := range events {
		views = append(views, *outboxEventView(&events[index]))
	}
	return views, total, nil
}

func (s *store) GetOutboxEvent(ctx context.Context, tenantID, id string) (*OutboxEventView, error) {
	event := &model.OutboxEvent{}
	if err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(event).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return outboxEventView(event), nil
}

func (s *store) RetryOutboxEventForTenant(ctx context.Context, tenantID, id string, now time.Time) (*OutboxEventView, bool, error) {
	updateResult := s.WithContext(ctx).Model(&model.OutboxEvent{}).
		Where("tenant_id = ? AND id = ? AND published_at IS NULL", tenantID, id).
		Updates(map[string]interface{}{
			"next_retry_at":    now,
			"claimed_at":       nil,
			"claim_expires_at": nil,
			"updated_at":       now,
		})
	if updateResult.Error != nil {
		return nil, false, updateResult.Error
	}
	if updateResult.RowsAffected == 1 {
		view, err := s.GetOutboxEvent(ctx, tenantID, id)
		if err != nil || view == nil {
			return nil, false, err
		}
		return view, true, nil
	}
	return nil, false, nil
}

func (s *store) ClaimOutboxEvents(ctx context.Context, limit int, now time.Time, leaseDuration time.Duration) ([]model.OutboxEvent, error) {
	if limit <= 0 {
		limit = 1
	}
	if leaseDuration <= 0 {
		leaseDuration = time.Minute
	}
	events := make([]model.OutboxEvent, 0, limit)
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&model.OutboxEvent{}).
			Where("published_at IS NULL AND (next_retry_at IS NULL OR next_retry_at <= ?) AND (claim_expires_at IS NULL OR claim_expires_at <= ?)", now, now).
			Order("occurred_at ASC, id ASC").
			Limit(limit).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		expiresAt := now.Add(leaseDuration)
		claimedIDs := make([]string, 0, len(ids))
		for _, eventID := range ids {
			result := tx.Model(&model.OutboxEvent{}).
				Where("id = ? AND published_at IS NULL AND (claim_expires_at IS NULL OR claim_expires_at <= ?)", eventID, now).
				Updates(map[string]interface{}{
					"claimed_at":       now,
					"claim_expires_at": expiresAt,
					"updated_at":       now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				claimedIDs = append(claimedIDs, eventID)
			}
		}
		if len(claimedIDs) == 0 {
			return nil
		}
		return tx.Where("id IN ?", claimedIDs).Order("occurred_at ASC, id ASC").Find(&events).Error
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *store) MarkOutboxEventPublished(ctx context.Context, id string, now time.Time, result string) (bool, error) {
	dbResult := s.WithContext(ctx).Model(&model.OutboxEvent{}).
		Where("id = ? AND published_at IS NULL", id).
		Updates(map[string]interface{}{
			"published_at":     now,
			"result":           result,
			"claimed_at":       nil,
			"claim_expires_at": nil,
			"updated_at":       now,
		})
	if dbResult.Error != nil {
		return false, dbResult.Error
	}
	return dbResult.RowsAffected == 1, nil
}

func (s *store) MarkDocumentVersionPublishedImpact(
	ctx context.Context, tenantID, eventID, logicalDocumentID string, now time.Time,
	buildResult func(affected int64) (string, error),
) (int64, bool, error) {
	var affected int64
	var published bool
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		dependencyEvidence := tx.Model(&model.EvalCaseDependency{}).
			Select("eval_case_evidence_id").
			Where("tenant_id = ? AND logical_document_id = ?", tenantID, logicalDocumentID)
		updateResult := tx.Model(&model.EvalCaseEvidence{}).
			Where("tenant_id = ? AND id IN (?)", tenantID, dependencyEvidence).
			Updates(map[string]interface{}{
				"stale_status":      model.EvidenceStatusStale,
				"stale_detected_at": now,
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		affected = updateResult.RowsAffected

		result, encodeErr := buildResult(affected)
		if encodeErr != nil {
			return encodeErr
		}

		markResult := tx.Model(&model.OutboxEvent{}).
			Where("id = ? AND tenant_id = ? AND published_at IS NULL", eventID, tenantID).
			Updates(map[string]interface{}{
				"published_at":     now,
				"result":           result,
				"claimed_at":       nil,
				"claim_expires_at": nil,
				"updated_at":       now,
			})
		if markResult.Error != nil {
			return markResult.Error
		}
		published = markResult.RowsAffected == 1
		if !published {
			return fmt.Errorf("outbox event %s was not claimable for tenant %s", eventID, tenantID)
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return affected, published, nil
}

func (s *store) RetryOutboxEvent(ctx context.Context, id string, attempts int, nextRetryAt time.Time, lastError string) error {
	return s.WithContext(ctx).Model(&model.OutboxEvent{}).
		Where("id = ? AND published_at IS NULL", id).
		Updates(map[string]interface{}{
			"attempts":         attempts,
			"next_retry_at":    nextRetryAt,
			"claimed_at":       nil,
			"claim_expires_at": nil,
			"last_error":       lastError,
			"updated_at":       time.Now().UTC(),
		}).Error
}
