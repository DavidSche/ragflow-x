package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type LogicalDocumentInput struct {
	DatasetID   string `json:"dataset_id" binding:"required"`
	Name        string `json:"name" binding:"required"`
	SourceURI   string `json:"source_uri"`
	Description string `json:"description"`
}

type LogicalDocumentPatchInput struct {
	Name        *string `json:"name"`
	SourceURI   *string `json:"source_uri"`
	Description *string `json:"description"`
}

type DocumentVersionInput struct {
	RAGFlowDocumentID string     `json:"ragflow_document_id" binding:"required"`
	ContentHash       string     `json:"content_hash" binding:"required"`
	EffectiveFrom     time.Time  `json:"effective_from" binding:"required"`
	EffectiveTo       *time.Time `json:"effective_to"`
	ChangeSummary     string     `json:"change_summary"`
	SupersedesVersion *int64     `json:"supersedes_version"`
}

type SupersedeDocumentVersionInput struct {
	VersionID string `json:"version_id" binding:"required"`
	Reason    string `json:"reason" binding:"required"`
}

type VersionPublishResult struct {
	LogicalDocumentID         string    `json:"logical_document_id"`
	PreviousVersion           *int64    `json:"previous_version"`
	PublishedVersion          int64     `json:"published_version"`
	PreviousRAGFlowDocumentID string    `json:"previous_ragflow_document_id"`
	NewRAGFlowDocumentID      string    `json:"new_ragflow_document_id"`
	EffectiveFrom             time.Time `json:"effective_from"`
	ContentHash               string    `json:"content_hash"`
	PublishAttemptID          string    `json:"publish_attempt_id"`
	PublishedAt               time.Time `json:"published_at"`
}

type DocumentVersionPublishedPayload struct {
	LogicalDocumentID string  `json:"logical_document_id"`
	NewVersionID      string  `json:"new_version_id"`
	PreviousVersionID *string `json:"previous_version_id"`
	NewVersion        int64   `json:"new_version"`
	PreviousVersion   *int64  `json:"previous_version"`
	PublishAttemptID  string  `json:"publish_attempt_id"`
	ContentHash       string  `json:"content_hash"`
}

func (s *Service) CreateLogicalDocument(ctx context.Context, tenantID, userID string, input LogicalDocumentInput) (*model.LogicalDocument, error) {
	dataset, err := s.resolveDataset(ctx, tenantID, strings.TrimSpace(input.DatasetID))
	if err != nil {
		return nil, err
	}
	name, err := normalizeDisplayName(input.Name, "logical document name is required", 40090)
	if err != nil {
		return nil, err
	}
	sourceURI, err := normalizeDocumentSourceURI(input.SourceURI)
	if err != nil {
		return nil, err
	}
	if len(input.Description) > 1024 {
		return nil, httperr.BadRequest(40090, "description must be at most 1024 characters")
	}
	now := time.Now().UTC()
	document := &model.LogicalDocument{
		ID: id.New(), TenantID: tenantID, DatasetID: dataset.ID, Name: name,
		SourceURI: sourceURI, Description: strings.TrimSpace(input.Description),
		CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateLogicalDocument(ctx, document); err != nil {
		return nil, err
	}
	if err := s.recordDocumentVersionAudit(ctx, tenantID, userID, "logical_document.create", "logical-document", document.ID, map[string]interface{}{
		"dataset_id": dataset.ID, "name": document.Name, "source_uri": document.SourceURI,
	}); err != nil {
		return nil, err
	}
	return document, nil
}

func (s *Service) GetLogicalDocument(ctx context.Context, tenantID, documentID string) (*model.LogicalDocument, error) {
	document, err := s.Store.GetLogicalDocument(ctx, tenantID, documentID)
	if err != nil {
		return nil, err
	}
	if document == nil {
		return nil, httperr.NotFound("logical document not found")
	}
	return document, nil
}

func (s *Service) ListLogicalDocuments(ctx context.Context, tenantID string, filter repository.DocumentVersionFilter, page, pageSize int) ([]model.LogicalDocument, int64, error) {
	return s.Store.ListLogicalDocuments(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) UpdateLogicalDocument(ctx context.Context, tenantID, userID, documentID string, input LogicalDocumentPatchInput) (*model.LogicalDocument, error) {
	document, err := s.GetLogicalDocument(ctx, tenantID, documentID)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		name, nameErr := normalizeDisplayName(*input.Name, "logical document name is required", 40090)
		if nameErr != nil {
			return nil, nameErr
		}
		document.Name = name
	}
	if input.SourceURI != nil {
		sourceURI, sourceErr := normalizeDocumentSourceURI(*input.SourceURI)
		if sourceErr != nil {
			return nil, sourceErr
		}
		document.SourceURI = sourceURI
	}
	if input.Description != nil {
		if len(*input.Description) > 1024 {
			return nil, httperr.BadRequest(40090, "description must be at most 1024 characters")
		}
		document.Description = strings.TrimSpace(*input.Description)
	}
	document.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateLogicalDocument(ctx, document); err != nil {
		return nil, err
	}
	if err := s.recordDocumentVersionAudit(ctx, tenantID, userID, "logical_document.update", "logical-document", document.ID, map[string]interface{}{
		"name": document.Name, "source_uri": document.SourceURI,
	}); err != nil {
		return nil, err
	}
	return document, nil
}

func (s *Service) DeleteLogicalDocument(ctx context.Context, tenantID, userID, documentID string) error {
	document, err := s.GetLogicalDocument(ctx, tenantID, documentID)
	if err != nil {
		return err
	}
	count, err := s.Store.CountDocumentVersions(ctx, tenantID, document.ID)
	if err != nil {
		return err
	}
	if count > 0 {
		return httperr.New(409, 40996, "logical document still has versions")
	}
	if err := s.Store.DeleteLogicalDocument(ctx, tenantID, document.ID); err != nil {
		return err
	}
	return s.recordDocumentVersionAudit(ctx, tenantID, userID, "logical_document.delete", "logical-document", document.ID, nil)
}

func (s *Service) CreateDocumentVersion(ctx context.Context, tenantID, logicalDocumentID, userID string, input DocumentVersionInput) (*model.DocumentVersion, bool, error) {
	logical, err := s.GetLogicalDocument(ctx, tenantID, logicalDocumentID)
	if err != nil {
		return nil, false, err
	}
	contentHash, err := normalizeDocumentContentHash(input.ContentHash)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(input.RAGFlowDocumentID) == "" {
		return nil, false, httperr.BadRequest(40090, "ragflow_document_id is required")
	}
	if err := validateDocumentEffectiveRange(input.EffectiveFrom, input.EffectiveTo); err != nil {
		return nil, false, err
	}
	existing, err := s.Store.FindDocumentVersionByContentHash(ctx, tenantID, logical.ID, contentHash)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	versionNo, err := s.nextDocumentVersionNo(ctx, tenantID, logical.ID)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	version := &model.DocumentVersion{
		ID: id.New(), TenantID: tenantID, LogicalDocumentID: logical.ID,
		RAGFlowDocumentID: strings.TrimSpace(input.RAGFlowDocumentID), Version: versionNo,
		Status: model.DocumentVersionDraft, EffectiveFrom: input.EffectiveFrom.UTC(),
		EffectiveTo: toUTCTime(input.EffectiveTo), ContentHash: contentHash,
		ChangeSummary:     strings.TrimSpace(input.ChangeSummary),
		SupersedesVersion: input.SupersedesVersion, CreatedBy: userID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateDocumentVersion(ctx, version); err != nil {
		return nil, false, err
	}
	if err := s.recordDocumentVersionAudit(ctx, tenantID, userID, "document_version.create", "document-version", version.ID, map[string]interface{}{
		"logical_document_id": logical.ID, "version": version.Version,
		"content_hash": version.ContentHash, "status": version.Status,
	}); err != nil {
		return nil, false, err
	}
	return version, true, nil
}

func (s *Service) ListDocumentVersions(ctx context.Context, tenantID, logicalDocumentID string, page, pageSize int) ([]model.DocumentVersion, int64, error) {
	if _, err := s.GetLogicalDocument(ctx, tenantID, logicalDocumentID); err != nil {
		return nil, 0, err
	}
	return s.Store.ListDocumentVersions(ctx, tenantID, logicalDocumentID, page, pageSize)
}

func (s *Service) TransitionDocumentVersion(ctx context.Context, tenantID, userID, versionID, status, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return httperr.BadRequest(40090, "transition reason is required")
	}
	version, err := s.Store.GetDocumentVersion(ctx, tenantID, versionID)
	if err != nil {
		return err
	}
	if version == nil {
		return httperr.NotFound("document version not found")
	}
	if !documentVersionTransitionAllowed(version.Status, status) {
		return httperr.New(409, 40995, "document version transition is not allowed")
	}
	if status == model.DocumentVersionPublishing {
		if err := s.ensureNoDocumentVersionOverlap(ctx, tenantID, version.LogicalDocumentID, version.ID, version.EffectiveFrom, version.EffectiveTo); err != nil {
			return err
		}
	}
	sourceStatus := version.Status
	version.Status = status
	version.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpdateDocumentVersion(ctx, version); err != nil {
		return err
	}
	return s.recordDocumentVersionAudit(ctx, tenantID, userID, "document_version.transition", "document-version", version.ID, map[string]interface{}{
		"logical_document_id": version.LogicalDocumentID, "version": version.Version,
		"from": sourceStatus, "to": status, "reason": reason,
	})
}

func (s *Service) SupersedeDocumentVersion(ctx context.Context, tenantID, userID, logicalDocumentID string, input SupersedeDocumentVersionInput) (*VersionPublishResult, error) {
	return s.publishDocumentVersion(ctx, tenantID, userID, logicalDocumentID, input, "document_version.publish")
}

func (s *Service) RestoreDocumentVersion(ctx context.Context, tenantID, userID, logicalDocumentID string, input SupersedeDocumentVersionInput) (*VersionPublishResult, error) {
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || len(reason) > 512 {
		return nil, httperr.BadRequest(40090, "reason must contain 1 to 512 characters")
	}
	logical, err := s.GetLogicalDocument(ctx, tenantID, logicalDocumentID)
	if err != nil {
		return nil, err
	}
	target, err := s.Store.GetDocumentVersion(ctx, tenantID, strings.TrimSpace(input.VersionID))
	if err != nil {
		return nil, err
	}
	if target == nil || target.LogicalDocumentID != logical.ID {
		return nil, httperr.NotFound("document version not found")
	}
	if target.Status != model.DocumentVersionSuperseded {
		return nil, httperr.New(409, 40995, "only superseded document versions can be restored")
	}
	active, err := s.Store.GetActiveDocumentVersion(ctx, tenantID, logical.ID)
	if err != nil {
		return nil, err
	}
	if active == nil || active.ID == target.ID {
		return nil, httperr.New(409, 40995, "document version is already active")
	}
	return s.publishDocumentVersion(ctx, tenantID, userID, logicalDocumentID, input, "document_version.restore")
}

func (s *Service) publishDocumentVersion(
	ctx context.Context,
	tenantID, userID, logicalDocumentID string,
	input SupersedeDocumentVersionInput,
	publishAction string,
) (*VersionPublishResult, error) {
	reason := strings.TrimSpace(input.Reason)
	if reason == "" || len(reason) > 512 {
		return nil, httperr.BadRequest(40090, "reason must contain 1 to 512 characters")
	}
	logical, err := s.GetLogicalDocument(ctx, tenantID, logicalDocumentID)
	if err != nil {
		return nil, err
	}
	datasetLink, err := s.Store.GetDatasetLink(ctx, tenantID, logical.DatasetID)
	if err != nil {
		return nil, err
	}
	if datasetLink == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	open, err := s.Store.GetOpenVersionPublishAttempt(ctx, tenantID, logical.ID)
	if err != nil {
		return nil, err
	}
	if open != nil {
		return nil, httperr.New(409, 40997, "document version publish already in progress")
	}
	newVersion, err := s.Store.GetDocumentVersion(ctx, tenantID, input.VersionID)
	if err != nil {
		return nil, err
	}
	if newVersion == nil || newVersion.LogicalDocumentID != logical.ID {
		return nil, httperr.NotFound("document version not found")
	}
	if newVersion.Status != model.DocumentVersionDraft && newVersion.Status != model.DocumentVersionSuperseded {
		return nil, httperr.New(409, 40995, "document version transition is not allowed")
	}
	existingByHash, err := s.Store.FindDocumentVersionByContentHash(ctx, tenantID, logical.ID, newVersion.ContentHash)
	if err != nil {
		return nil, err
	}
	if existingByHash != nil && existingByHash.ID != newVersion.ID {
		return nil, httperr.New(409, 40995, "document version content hash already exists")
	}
	previous, err := s.Store.GetActiveDocumentVersion(ctx, tenantID, logical.ID)
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.ID == newVersion.ID {
		return nil, httperr.New(409, 40995, "document version is already active")
	}
	if publishAction == "document_version.restore" {
		newVersion.EffectiveFrom = time.Now().UTC().Truncate(time.Second)
		newVersion.EffectiveTo = nil
	}
	if err := s.ensureSupersedeVersionOverlap(ctx, tenantID, logical.ID, newVersion, previous); err != nil {
		return nil, err
	}
	if previous != nil && previous.RAGFlowDocumentID == newVersion.RAGFlowDocumentID {
		return nil, httperr.New(409, 40995, "active and target versions must reference different RAGFlow documents")
	}

	newMetadata, err := s.RAGFlow.GetDatasetDocumentMetadata(ctx, datasetLink.RAGFlowDatasetID, newVersion.RAGFlowDocumentID)
	if err != nil {
		return nil, fmt.Errorf("read target document metadata: %w", err)
	}
	rollbackMetadata := map[string]map[string]interface{}{}
	desiredMetadata := map[string]map[string]interface{}{}
	updatedDocuments := make([]string, 0, 2)
	if previous != nil {
		previousMetadata, metadataErr := s.RAGFlow.GetDatasetDocumentMetadata(ctx, datasetLink.RAGFlowDatasetID, previous.RAGFlowDocumentID)
		if metadataErr != nil {
			return nil, fmt.Errorf("read active document metadata: %w", metadataErr)
		}
		rollbackMetadata[previous.RAGFlowDocumentID] = copyDocumentMetadata(previousMetadata)
		desiredMetadata[previous.RAGFlowDocumentID] = documentVersionMetadata(previousMetadata, logical, previous, newVersion.EffectiveFrom)
	}
	rollbackMetadata[newVersion.RAGFlowDocumentID] = copyDocumentMetadata(newMetadata)
	desiredMetadata[newVersion.RAGFlowDocumentID] = documentVersionMetadata(newMetadata, logical, newVersion, time.Time{})

	targetSourceStatus := newVersion.Status
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		changed, updateErr := tx.UpdateDocumentVersionStatus(ctx, tenantID, newVersion.ID, targetSourceStatus, model.DocumentVersionPublishing)
		if updateErr != nil {
			return updateErr
		}
		if !changed {
			return httperr.New(409, 40995, "document version transition is not allowed")
		}
		now := time.Now().UTC()
		attempt := &model.VersionPublishAttempt{
			ID: id.New(), TenantID: tenantID, LogicalDocumentID: logical.ID,
			NewVersionID: newVersion.ID, State: model.VersionPublishPreparing,
			SourceStatus: targetSourceStatus, Attempts: 1, CreatedAt: now, UpdatedAt: now,
		}
		if previous != nil {
			previousID := previous.ID
			attempt.PreviousVersionID = &previousID
		}
		if attempt.DesiredMetadata, err = documentVersionMetadataJSON(desiredMetadata); err != nil {
			return err
		}
		if attempt.RollbackMetadata, err = documentVersionMetadataJSON(rollbackMetadata); err != nil {
			return err
		}
		return tx.CreateVersionPublishAttempt(ctx, attempt)
	})
	if err != nil {
		return nil, err
	}
	createdAttempt, err := s.Store.GetOpenVersionPublishAttempt(ctx, tenantID, logical.ID)
	if err != nil || createdAttempt == nil {
		if err == nil {
			err = fmt.Errorf("version publish attempt is missing after transaction")
		}
		_, _ = s.Store.UpdateDocumentVersionStatus(ctx, tenantID, newVersion.ID, model.DocumentVersionPublishing, targetSourceStatus)
		return nil, err
	}

	replaced := false
	for _, ragflowDocumentID := range []string{previousRAGFlowDocumentID(previous), newVersion.RAGFlowDocumentID} {
		if ragflowDocumentID == "" {
			continue
		}
		if err = s.RAGFlow.ReplaceDatasetDocumentMetadata(ctx, datasetLink.RAGFlowDatasetID, ragflowDocumentID, desiredMetadata[ragflowDocumentID]); err != nil {
			err = fmt.Errorf("update RAGFlow document metadata: %w", err)
			break
		}
		replaced = true
		updatedDocuments = append(updatedDocuments, ragflowDocumentID)
	}
	if replaced {
		createdAttempt.State = model.VersionPublishRAGFlowUpdate
		createdAttempt.UpdatedAt = time.Now().UTC()
		if attemptErr := s.Store.UpdateVersionPublishAttempt(ctx, createdAttempt); attemptErr != nil {
			updatedDocuments = rollbackDocumentMetadataOrder(updatedDocuments)
			if rollbackErr := s.rollbackPublishMetadata(ctx, datasetLink.RAGFlowDatasetID, rollbackMetadata, updatedDocuments); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			_, _ = s.Store.UpdateDocumentVersionStatus(ctx, tenantID, newVersion.ID, model.DocumentVersionPublishing, targetSourceStatus)
			createdAttempt.State = model.VersionPublishCompensated
			createdAttempt.LastError = truncatePublishError(err)
			createdAttempt.UpdatedAt = time.Now().UTC()
			_ = s.Store.UpdateVersionPublishAttempt(ctx, createdAttempt)
			return nil, err
		}
	}
	if err != nil {
		rollbackOrder := rollbackDocumentMetadataOrder(updatedDocuments)
		rollbackErr := s.rollbackPublishMetadata(ctx, datasetLink.RAGFlowDatasetID, rollbackMetadata, rollbackOrder)
		_, _ = s.Store.UpdateDocumentVersionStatus(ctx, tenantID, newVersion.ID, model.DocumentVersionPublishing, targetSourceStatus)
		createdAttempt.State = model.VersionPublishCompensated
		createdAttempt.LastError = truncatePublishError(errors.Join(err, rollbackErr))
		createdAttempt.UpdatedAt = time.Now().UTC()
		_ = s.Store.UpdateVersionPublishAttempt(ctx, createdAttempt)
		return nil, errors.Join(err, rollbackErr)
	}
	if err == nil {
		newVersion.Status = model.DocumentVersionActive
		newVersion.UpdatedAt = time.Now().UTC()
		committedAt := time.Now().UTC()
		if previous != nil {
			previous.Status = model.DocumentVersionSuperseded
			previous.EffectiveTo = &newVersion.EffectiveFrom
			previous.UpdatedAt = committedAt
		}
		createdAttempt.State = model.VersionPublishCommitted
		createdAttempt.UpdatedAt = committedAt
		err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
			if previous != nil {
				changed, updateErr := tx.UpdateDocumentVersionStatus(ctx, tenantID, previous.ID, model.DocumentVersionActive, model.DocumentVersionSuperseded)
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
			changed, updateErr := tx.UpdateDocumentVersionStatus(ctx, tenantID, newVersion.ID, model.DocumentVersionPublishing, model.DocumentVersionActive)
			if updateErr != nil {
				return updateErr
			}
			if !changed {
				return httperr.New(409, 40995, "target document version changed during publish")
			}
			if err := tx.UpdateDocumentVersion(ctx, newVersion); err != nil {
				return err
			}
			if err := tx.UpdateVersionPublishAttempt(ctx, createdAttempt); err != nil {
				return err
			}
			previousVersionID := ""
			if previous != nil {
				previousVersionID = previous.ID
			}
			eventPayload, eventErr := json.Marshal(DocumentVersionPublishedPayload{
				LogicalDocumentID: logical.ID, NewVersionID: newVersion.ID,
				PreviousVersionID: &previousVersionID, NewVersion: newVersion.Version,
				PreviousVersion: previousVersionNo(previous), PublishAttemptID: createdAttempt.ID,
				ContentHash: newVersion.ContentHash,
			})
			if eventErr != nil {
				return eventErr
			}
			if err := tx.CreateOutboxEvent(ctx, &model.OutboxEvent{
				ID: id.New(), TenantID: tenantID, EventType: model.EventTypeDocumentVersionPublished,
				AggregateType: model.AggregateTypeDocumentVersion, AggregateID: newVersion.ID,
				Payload: string(eventPayload), OccurredAt: committedAt,
				CreatedAt: committedAt, UpdatedAt: committedAt,
			}); err != nil {
				return err
			}
			detailJSON := ""
			if encoded, encodeErr := json.Marshal(map[string]interface{}{
				"logical_document_id": logical.ID, "published_version": newVersion.Version,
				"previous_version": previousVersionNo(previous), "reason": reason,
				"publish_attempt_id": createdAttempt.ID,
			}); encodeErr == nil {
				detailJSON = string(encoded)
			}
			return tx.CreateAudit(ctx, &model.AuditLog{
				ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
				UserID: userID, Action: publishAction, Resource: "document-version",
				ResourceID: newVersion.ID, DetailJSON: detailJSON, At: committedAt,
				Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
			})
		})
		if err != nil {
			rollbackOrder := rollbackDocumentMetadataOrder(updatedDocuments)
			rollbackErr := s.rollbackPublishMetadata(ctx, datasetLink.RAGFlowDatasetID, rollbackMetadata, rollbackOrder)
			_, _ = s.Store.UpdateDocumentVersionStatus(ctx, tenantID, newVersion.ID, model.DocumentVersionPublishing, targetSourceStatus)
			createdAttempt.State = model.VersionPublishCompensated
			createdAttempt.LastError = truncatePublishError(errors.Join(err, rollbackErr))
			createdAttempt.UpdatedAt = time.Now().UTC()
			_ = s.Store.UpdateVersionPublishAttempt(ctx, createdAttempt)
			return nil, errors.Join(err, rollbackErr)
		}
	}
	return &VersionPublishResult{
		LogicalDocumentID: logical.ID, PreviousVersion: previousVersionNo(previous), PublishedVersion: newVersion.Version,
		PreviousRAGFlowDocumentID: previousRAGFlowDocumentID(previous), NewRAGFlowDocumentID: newVersion.RAGFlowDocumentID,
		EffectiveFrom: newVersion.EffectiveFrom, ContentHash: newVersion.ContentHash,
		PublishAttemptID: createdAttempt.ID, PublishedAt: newVersion.UpdatedAt,
	}, nil
}

func (s *Service) ListVersionPublishAttempts(ctx context.Context, tenantID, logicalDocumentID string, page, pageSize int) ([]model.VersionPublishAttempt, int64, error) {
	if _, err := s.GetLogicalDocument(ctx, tenantID, logicalDocumentID); err != nil {
		return nil, 0, err
	}
	return s.Store.ListVersionPublishAttempts(ctx, tenantID, logicalDocumentID, page, pageSize)
}

func (s *Service) ensureSupersedeVersionOverlap(ctx context.Context, tenantID, logicalDocumentID string, newVersion, previous *model.DocumentVersion) error {
	versions, err := s.Store.ListGovernanceDocumentVersions(ctx, tenantID, logicalDocumentID)
	if err != nil {
		return err
	}
	for index := range versions {
		other := &versions[index]
		if other.ID == newVersion.ID || (previous != nil && other.ID == previous.ID) {
			continue
		}
		if documentEffectiveRangesOverlap(newVersion.EffectiveFrom, newVersion.EffectiveTo, other.EffectiveFrom, other.EffectiveTo) {
			return httperr.New(409, 40995, "document version effective ranges overlap")
		}
	}
	return nil
}

func previousRAGFlowDocumentID(previous *model.DocumentVersion) string {
	if previous == nil {
		return ""
	}
	return previous.RAGFlowDocumentID
}

func previousVersionNo(previous *model.DocumentVersion) *int64 {
	if previous == nil {
		return nil
	}
	return &previous.Version
}

func copyDocumentMetadata(metadata map[string]interface{}) map[string]interface{} {
	copied := map[string]interface{}{}
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}

func documentVersionMetadata(base map[string]interface{}, logical *model.LogicalDocument, version *model.DocumentVersion, effectiveTo time.Time) map[string]interface{} {
	metadata := copyDocumentMetadata(base)
	metadata["rgx_logical_doc_id"] = logical.ID
	metadata["rgx_version"] = version.Version
	metadata["rgx_status"] = "active"
	metadata["rgx_effective_from"] = version.EffectiveFrom.UTC().Format(time.RFC3339Nano)
	if !effectiveTo.IsZero() {
		metadata["rgx_effective_to"] = effectiveTo.UTC().Format(time.RFC3339Nano)
	} else {
		delete(metadata, "rgx_effective_to")
	}
	metadata["rgx_content_hash"] = version.ContentHash
	return metadata
}

func documentVersionMetadataJSON(metadata map[string]map[string]interface{}) (string, error) {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode version publish metadata: %w", err)
	}
	return string(encoded), nil
}

func rollbackDocumentMetadataOrder(updated []string) []string {
	rolledBack := make([]string, 0, len(updated))
	for index := len(updated) - 1; index >= 0; index-- {
		rolledBack = append(rolledBack, updated[index])
	}
	return rolledBack
}

func (s *Service) rollbackPublishMetadata(ctx context.Context, datasetID string, rollbackMetadata map[string]map[string]interface{}, documents []string) error {
	var rollbackErrs []error
	for _, documentID := range documents {
		metadata, ok := rollbackMetadata[documentID]
		if !ok {
			metadata = map[string]interface{}{}
		}
		if err := s.RAGFlow.ReplaceDatasetDocumentMetadata(ctx, datasetID, documentID, metadata); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("rollback RAGFlow document metadata %s: %w", documentID, err))
		}
	}
	return errors.Join(rollbackErrs...)
}

func truncatePublishError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}

func (s *Service) nextDocumentVersionNo(ctx context.Context, tenantID, logicalDocumentID string) (int64, error) {
	return s.Store.NextDocumentVersionNo(ctx, tenantID, logicalDocumentID)
}

func (s *Service) ensureNoDocumentVersionOverlap(ctx context.Context, tenantID, logicalDocumentID, excludeVersionID string, from time.Time, to *time.Time) error {
	versions, err := s.Store.ListGovernanceDocumentVersions(ctx, tenantID, logicalDocumentID)
	if err != nil {
		return err
	}
	for index := range versions {
		other := &versions[index]
		if other.ID == excludeVersionID {
			continue
		}
		if documentEffectiveRangesOverlap(from, to, other.EffectiveFrom, other.EffectiveTo) {
			return httperr.New(409, 40995, "document version effective ranges overlap")
		}
	}
	return nil
}

func (s *Service) recordDocumentVersionAudit(ctx context.Context, tenantID, userID, action, resource, resourceID string, detail map[string]interface{}) error {
	detailJSON := ""
	if detail != nil {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		detailJSON = string(encoded)
	}
	return s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
		UserID: userID, Action: action, Resource: resource, ResourceID: resourceID,
		DetailJSON: detailJSON, At: time.Now().UTC(), Result: "SUCCESS",
		AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	})
}

func documentVersionTransitionAllowed(from, to string) bool {
	switch from {
	case model.DocumentVersionDraft:
		return to == model.DocumentVersionPublishing || to == model.DocumentVersionArchived
	case model.DocumentVersionPublishing:
		return to == model.DocumentVersionDraft
	case model.DocumentVersionActive:
		return to == model.DocumentVersionSuperseded
	case model.DocumentVersionSuperseded:
		return to == model.DocumentVersionPublishing
	default:
		return false
	}
}

func normalizeDocumentSourceURI(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 512 {
		return "", httperr.BadRequest(40090, "source_uri must be at most 512 characters")
	}
	return value, nil
}

func normalizeDocumentContentHash(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !sha256Pattern.MatchString(value) {
		return "", httperr.BadRequest(40090, "content_hash must be a SHA-256 hex digest")
	}
	return value, nil
}

func validateDocumentEffectiveRange(from time.Time, to *time.Time) error {
	if from.IsZero() {
		return httperr.BadRequest(40090, "effective_from is required")
	}
	if to != nil && !to.After(from) {
		return httperr.BadRequest(40090, "effective_to must be after effective_from")
	}
	return nil
}

func toUTCTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func documentEffectiveRangesOverlap(fromA time.Time, toA *time.Time, fromB time.Time, toB *time.Time) bool {
	endA := fromA
	if toA != nil {
		endA = *toA
	}
	endB := fromB
	if toB != nil {
		endB = *toB
	}
	return fromA.Before(endB) && fromB.Before(endA)
}
