package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	versionPublishReconcileInterval = time.Minute
	versionPublishReconcileIdleFor  = time.Minute
	versionPublishReconcileLease    = 5 * time.Minute
	versionPublishReconcileBatch    = 10
	versionPublishMaxAttempts       = 5
)

type versionPublishSnapshot struct {
	Desired  map[string]map[string]interface{}
	Rollback map[string]map[string]interface{}
}

// ProcessVersionPublishReconciler recovers publish attempts that survived a
// crash between Transaction A, RAGFlow metadata replacement, and Transaction B.
// It only rewrites RAGFlow metadata when the observed state matches one of the
// two durable snapshots; ambiguous state always becomes manual review.
func (s *Service) ProcessVersionPublishReconciler(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = versionPublishReconcileBatch
	}
	now := time.Now().UTC()
	attempts, err := s.Store.ClaimDueVersionPublishAttempts(
		ctx, limit, now, versionPublishReconcileIdleFor, versionPublishReconcileLease,
	)
	if err != nil {
		return err
	}
	var processErrs []error
	for index := range attempts {
		if err := s.reconcileVersionPublishAttempt(ctx, &attempts[index], now); err != nil {
			processErrs = append(processErrs, err)
		}
	}
	return errors.Join(processErrs...)
}

func (s *Service) reconcileVersionPublishAttempt(ctx context.Context, attempt *model.VersionPublishAttempt, now time.Time) error {
	logical, err := s.Store.GetLogicalDocument(ctx, attempt.TenantID, attempt.LogicalDocumentID)
	if err != nil {
		return s.retryVersionPublishAttempt(ctx, attempt, err, now)
	}
	if logical == nil {
		return s.reviewVersionPublishAttempt(ctx, attempt, errors.New("logical document not found"), now)
	}
	datasetLink, err := s.Store.GetDatasetLink(ctx, attempt.TenantID, logical.DatasetID)
	if err != nil {
		return s.retryVersionPublishAttempt(ctx, attempt, err, now)
	}
	if datasetLink == nil {
		return s.reviewVersionPublishAttempt(ctx, attempt, errors.New("dataset not found"), now)
	}
	newVersion, err := s.Store.GetDocumentVersion(ctx, attempt.TenantID, attempt.NewVersionID)
	if err != nil {
		return s.retryVersionPublishAttempt(ctx, attempt, err, now)
	}
	if newVersion == nil {
		return s.reviewVersionPublishAttempt(ctx, attempt, errors.New("document version not found"), now)
	}
	var previous *model.DocumentVersion
	if attempt.PreviousVersionID != nil && *attempt.PreviousVersionID != "" {
		previous, err = s.Store.GetDocumentVersion(ctx, attempt.TenantID, *attempt.PreviousVersionID)
		if err != nil {
			return s.retryVersionPublishAttempt(ctx, attempt, err, now)
		}
		if previous == nil {
			return s.reviewVersionPublishAttempt(ctx, attempt, errors.New("previous document version not found"), now)
		}
	}
	snapshot, err := decodeVersionPublishSnapshot(attempt)
	if err != nil {
		return s.reviewVersionPublishAttempt(ctx, attempt, err, now)
	}

	observed := map[string]map[string]interface{}{}
	for documentID := range snapshot.Desired {
		metadata, metadataErr := s.RAGFlow.GetDatasetDocumentMetadata(
			ctx, datasetLink.RAGFlowDatasetID, documentID,
		)
		if metadataErr != nil {
			return s.retryVersionPublishAttempt(ctx, attempt, fmt.Errorf("read RAGFlow document metadata: %w", metadataErr), now)
		}
		observed[documentID] = metadata
	}

	var desiredCount, rollbackCount, mismatchCount int
	for documentID, metadata := range observed {
		if equalDocumentMetadata(metadata, snapshot.Desired[documentID]) {
			desiredCount++
		} else if equalDocumentMetadata(metadata, snapshot.Rollback[documentID]) {
			rollbackCount++
		} else {
			mismatchCount++
		}
	}
	if mismatchCount > 0 {
		return s.reviewVersionPublishAttempt(ctx, attempt, errors.New("RAGFlow metadata does not match a durable publish snapshot"), now)
	}

	if rollbackCount == len(observed) {
		return s.compensateReconciledVersionPublish(ctx, attempt, logical, newVersion, previous, now, nil)
	}
	if desiredCount > 0 && rollbackCount > 0 {
		return s.rollbackPartialVersionPublish(
			ctx, attempt, logical, datasetLink, newVersion, previous, snapshot, observed, now,
		)
	}
	return s.commitReconciledVersionPublish(ctx, attempt, logical, newVersion, previous, now)
}

func (s *Service) rollbackPartialVersionPublish(
	ctx context.Context,
	attempt *model.VersionPublishAttempt,
	logical *model.LogicalDocument,
	datasetLink *model.DatasetLink,
	newVersion, previous *model.DocumentVersion,
	snapshot *versionPublishSnapshot,
	observed map[string]map[string]interface{},
	now time.Time,
) error {
	documents := make([]string, 0, len(snapshot.Desired))
	for documentID, metadata := range observed {
		if equalDocumentMetadata(metadata, snapshot.Desired[documentID]) {
			documents = append(documents, documentID)
		}
	}
	rollbackErr := s.rollbackPublishMetadata(
		ctx, datasetLink.RAGFlowDatasetID, snapshot.Rollback, documents,
	)
	if rollbackErr != nil {
		return s.retryVersionPublishAttempt(
			ctx, attempt, fmt.Errorf("rollback partially updated RAGFlow metadata: %w", rollbackErr), now,
		)
	}
	return s.compensateReconciledVersionPublish(ctx, attempt, logical, newVersion, previous, now, nil)
}

func (s *Service) compensateReconciledVersionPublish(
	ctx context.Context,
	attempt *model.VersionPublishAttempt,
	logical *model.LogicalDocument,
	newVersion, previous *model.DocumentVersion,
	now time.Time,
	rollbackErr error,
) error {
	sourceStatus := inferVersionPublishSourceStatus(attempt, newVersion, previous)
	attempt.State = model.VersionPublishCompensated
	attempt.LastError = truncatePublishError(rollbackErr)
	attempt.ClaimedAt = nil
	attempt.ClaimExpiresAt = nil
	attempt.UpdatedAt = now
	err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		changed, updateErr := tx.UpdateDocumentVersionStatus(
			ctx, attempt.TenantID, newVersion.ID, model.DocumentVersionPublishing, sourceStatus,
		)
		if updateErr != nil {
			return updateErr
		}
		if !changed {
			return httperr.New(409, 40995, "target document version changed during rollback")
		}
		if err := tx.UpdateVersionPublishAttempt(ctx, attempt); err != nil {
			return err
		}
		detail := map[string]interface{}{
			"logical_document_id": logical.ID,
			"new_version_id":      newVersion.ID,
			"publish_attempt_id":  attempt.ID,
			"source_status":       sourceStatus,
			"reason":              "rollback_state_recovered",
		}
		if rollbackErr != nil {
			detail["reason"] = "metadata_rollback_failed"
		}
		return tx.CreateAudit(ctx, &model.AuditLog{
			ID: id.New(), TenantID: attempt.TenantID, ActorTenantID: attempt.TenantID,
			TargetTenantID: attempt.TenantID, UserID: "system",
			Action: "document_version.publish.reconciled", Resource: "document-version",
			ResourceID: newVersion.ID, DetailJSON: encodeAuditDetail(detail), At: now,
			Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
		})
	})
	return err
}

func (s *Service) commitReconciledVersionPublish(
	ctx context.Context,
	attempt *model.VersionPublishAttempt,
	logical *model.LogicalDocument,
	newVersion, previous *model.DocumentVersion,
	now time.Time,
) error {
	originalState := attempt.State
	newVersion.Status = model.DocumentVersionActive
	newVersion.UpdatedAt = now
	committedAt := now
	if previous != nil {
		previous.Status = model.DocumentVersionSuperseded
		previous.EffectiveTo = &newVersion.EffectiveFrom
		previous.UpdatedAt = committedAt
	}
	attempt.State = model.VersionPublishCommitted
	attempt.LastError = ""
	attempt.ClaimedAt = nil
	attempt.ClaimExpiresAt = nil
	attempt.UpdatedAt = committedAt
	err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if previous != nil {
			changed, updateErr := tx.UpdateDocumentVersionStatus(
				ctx, attempt.TenantID, previous.ID, model.DocumentVersionActive, model.DocumentVersionSuperseded,
			)
			if updateErr != nil {
				return updateErr
			}
			if !changed {
				return httperr.New(409, 40995, "active document version changed during publish")
			}
			if err := tx.UpdateDocumentVersion(ctx, previous); err != nil {
				return err
			}
		}
		changed, updateErr := tx.UpdateDocumentVersionStatus(
			ctx, attempt.TenantID, newVersion.ID, model.DocumentVersionPublishing, model.DocumentVersionActive,
		)
		if updateErr != nil {
			return updateErr
		}
		if !changed {
			return httperr.New(409, 40995, "target document version changed during publish")
		}
		if err := tx.UpdateDocumentVersion(ctx, newVersion); err != nil {
			return err
		}
		if err := tx.UpdateVersionPublishAttempt(ctx, attempt); err != nil {
			return err
		}
		previousVersionID := ""
		if previous != nil {
			previousVersionID = previous.ID
		}
		eventPayload, eventErr := json.Marshal(DocumentVersionPublishedPayload{
			LogicalDocumentID: logical.ID, NewVersionID: newVersion.ID,
			PreviousVersionID: &previousVersionID, NewVersion: newVersion.Version,
			PreviousVersion: previousVersionNo(previous), PublishAttemptID: attempt.ID,
			ContentHash: newVersion.ContentHash,
		})
		if eventErr != nil {
			return eventErr
		}
		if err := tx.CreateOutboxEvent(ctx, &model.OutboxEvent{
			ID: id.New(), TenantID: attempt.TenantID,
			EventType:     model.EventTypeDocumentVersionPublished,
			AggregateType: model.AggregateTypeDocumentVersion, AggregateID: newVersion.ID,
			Payload: string(eventPayload), OccurredAt: committedAt,
			CreatedAt: committedAt, UpdatedAt: committedAt,
		}); err != nil {
			return err
		}
		detailJSON := ""
		if encoded, encodeErr := json.Marshal(map[string]interface{}{
			"logical_document_id": logical.ID,
			"published_version":   newVersion.Version,
			"previous_version":    previousVersionNo(previous),
			"publish_attempt_id":  attempt.ID,
			"reconciled":          true,
		}); encodeErr == nil {
			detailJSON = string(encoded)
		}
		return tx.CreateAudit(ctx, &model.AuditLog{
			ID: id.New(), TenantID: attempt.TenantID, ActorTenantID: attempt.TenantID,
			TargetTenantID: attempt.TenantID, UserID: "system",
			Action: "document_version.publish", Resource: "document-version",
			ResourceID: newVersion.ID, DetailJSON: detailJSON, At: committedAt,
			Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
		})
	})
	if err == nil {
		return nil
	}
	attempt.State = originalState
	return s.retryVersionPublishAttempt(ctx, attempt, fmt.Errorf("commit reconciled version publish: %w", err), now)
}

func (s *Service) retryVersionPublishAttempt(ctx context.Context, attempt *model.VersionPublishAttempt, cause error, now time.Time) error {
	attempts := attempt.Attempts + 1
	if attempts > versionPublishMaxAttempts {
		return s.reviewVersionPublishAttempt(ctx, attempt, cause, now)
	}
	if err := s.Store.RetryVersionPublishAttempt(ctx, attempt.ID, attempts, truncatePublishError(cause), now); err != nil {
		return err
	}
	attempt.Attempts = attempts
	attempt.LastError = truncatePublishError(cause)
	attempt.ClaimedAt = nil
	attempt.ClaimExpiresAt = nil
	attempt.UpdatedAt = now
	return nil
}

func (s *Service) reviewVersionPublishAttempt(ctx context.Context, attempt *model.VersionPublishAttempt, cause error, now time.Time) error {
	attempt.State = model.VersionPublishManualReview
	attempt.LastError = truncatePublishError(cause)
	attempt.ClaimedAt = nil
	attempt.ClaimExpiresAt = nil
	attempt.UpdatedAt = now
	err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.UpdateVersionPublishAttempt(ctx, attempt); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, &model.AuditLog{
			ID: id.New(), TenantID: attempt.TenantID, ActorTenantID: attempt.TenantID,
			TargetTenantID: attempt.TenantID, UserID: "system",
			Action: "document_version.publish.manual_review", Resource: "document-version",
			ResourceID: attempt.NewVersionID,
			DetailJSON: encodeAuditDetail(map[string]interface{}{
				"publish_attempt_id": attempt.ID,
				"reason":             "reconciliation_requires_manual_review",
			}), At: now,
			Result: "NEEDS_REVIEW", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
		})
	})
	if err != nil {
		return err
	}
	if alertErr := s.RecordAlert(ctx, notify.Event{
		ID: id.New(), Title: "Version publish requires manual review", Severity: "warn",
		Type: "document_version.publish.manual_review", TenantID: attempt.TenantID,
		Resource: "document-version", ResourceID: attempt.NewVersionID,
		Detail:     "A recovered version publish could not be resolved safely from durable snapshots; review RAGFlow metadata before retrying.",
		OccurredAt: now,
		Fields: map[string]string{
			"logical_document_id": attempt.LogicalDocumentID,
			"publish_attempt_id":  attempt.ID,
			"new_version_id":      attempt.NewVersionID,
		},
	}); alertErr != nil {
		logger.Warn("failed to record version publish manual review alert", "attempt_id", attempt.ID, "error", alertErr)
	}
	return nil
}

func decodeVersionPublishSnapshot(attempt *model.VersionPublishAttempt) (*versionPublishSnapshot, error) {
	desired := map[string]map[string]interface{}{}
	rollback := map[string]map[string]interface{}{}
	if strings.TrimSpace(attempt.DesiredMetadata) != "" {
		if err := json.Unmarshal([]byte(attempt.DesiredMetadata), &desired); err != nil {
			return nil, fmt.Errorf("decode desired version publish metadata: %w", err)
		}
	}
	if strings.TrimSpace(attempt.RollbackMetadata) != "" {
		if err := json.Unmarshal([]byte(attempt.RollbackMetadata), &rollback); err != nil {
			return nil, fmt.Errorf("decode rollback version publish metadata: %w", err)
		}
	}
	if len(desired) == 0 {
		return nil, errors.New("desired version publish metadata is missing")
	}
	for documentID, metadata := range desired {
		if metadata == nil {
			desired[documentID] = map[string]interface{}{}
		}
	}
	for documentID, metadata := range rollback {
		if metadata == nil {
			rollback[documentID] = map[string]interface{}{}
		}
	}
	for documentID := range desired {
		if _, exists := rollback[documentID]; !exists {
			return nil, fmt.Errorf("rollback version publish metadata is missing document %s", documentID)
		}
	}
	return &versionPublishSnapshot{Desired: desired, Rollback: rollback}, nil
}

func equalDocumentMetadata(actual, expected map[string]interface{}) bool {
	if expected == nil {
		expected = map[string]interface{}{}
	}
	if actual == nil {
		actual = map[string]interface{}{}
	}
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(expected)
	return actualErr == nil && expectedErr == nil && string(actualJSON) == string(expectedJSON)
}

func encodeAuditDetail(detail map[string]interface{}) string {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func inferVersionPublishSourceStatus(attempt *model.VersionPublishAttempt, newVersion, previous *model.DocumentVersion) string {
	switch strings.TrimSpace(attempt.SourceStatus) {
	case model.DocumentVersionDraft, model.DocumentVersionSuperseded:
		return attempt.SourceStatus
	}
	if previous != nil && newVersion.SupersedesVersion != nil && *newVersion.SupersedesVersion == previous.Version {
		return model.DocumentVersionSuperseded
	}
	if previous == nil {
		return model.DocumentVersionDraft
	}
	return model.DocumentVersionDraft
}

// ScheduleVersionPublishReconciler arms the platform-wide recurring recovery
// scan. It never keeps two active jobs for the same task kind.
func (s *Service) ScheduleVersionPublishReconciler(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindVersionPublishReconcile, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	runAfter := time.Now().UTC().Add(versionPublishReconcileInterval)
	return s.Runner.Enqueue(ctx, model.JobKindVersionPublishReconcile,
		"version_publish_reconcile:"+time.Now().UTC().Format("20060102T150405Z"),
		SystemTenantID, "", runAfter, 0,
	)
}

type versionPublishReconcileWorker struct {
	svc *Service
}

func (w *versionPublishReconcileWorker) Kind() string {
	return model.JobKindVersionPublishReconcile
}

func (w *versionPublishReconcileWorker) Run(ctx context.Context, job *model.Job) error {
	if err := w.svc.ProcessVersionPublishReconciler(ctx, versionPublishReconcileBatch); err != nil {
		logger.Warn("version publish reconcile failed", "job_id", job.ID, "error", err)
		return err
	}
	if _, err := w.svc.ScheduleVersionPublishReconciler(ctx, job.ID); err != nil {
		logger.Warn("version publish reconcile reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	return nil
}
