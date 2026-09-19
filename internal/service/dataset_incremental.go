package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	incrementalBatchLimit  = 100
	maxDocumentUploadBytes = 25 << 20
)

// IncrementalSourceEvent is a normalized connector batch item. An empty
// batch is a valid snapshot and therefore marks all active rows removed.
type IncrementalSourceEvent struct {
	Operation string `json:"operation"`
	SourceKey string `json:"source_key"`
	Name      string `json:"name"`
	Content   []byte `json:"content"`
}

// IncrementalSourceBatch reports connector scan outcomes without second-guessing
// RAGFlow's parser. Removals never delete immediately: they enter the ledger's
// observation period and require the existing document approval path.
type IncrementalSourceBatch struct {
	Added            int                       `json:"added"`
	Unchanged        int                       `json:"unchanged"`
	Changed          int                       `json:"changed"`
	Removed          int                       `json:"removed"`
	ReadyForApproval int                       `json:"ready_for_approval"`
	Failures         []IncrementalBatchFailure `json:"failures"`
}

type IncrementalBatchFailure struct {
	SourceKey string `json:"source_key"`
	Error     string `json:"error"`
}

// UploadDocumentWithSourceKey applies the incremental identity first, then uses
// the normal preflight/parse path. An explicit source_key is preferred for
// connectors; uploads without one retain a workspace/uploader/name identity.
func (s *Service) UploadDocumentWithSourceKey(ctx context.Context, tenantID, datasetID, uploaderID, name, sourceKey string, content []byte) (*DocumentSummary, error) {
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if err := s.prepareDatasetParseReadiness(ctx, d); err != nil {
		return nil, err
	}
	config, err := s.RAGFlow.GetDatasetConfig(ctx, d.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50296, "read dataset parse policy failed")
	}
	return s.uploadDocumentWithDatasetConfig(ctx, tenantID, uploaderID, name, sourceKey, content, d, config)
}

func (s *Service) uploadDocumentWithDatasetConfig(
	ctx context.Context, tenantID, uploaderID, name, sourceKey string, content []byte,
	d *model.DatasetLink, config *ragflow.DatasetConfig,
) (*DocumentSummary, error) {
	if err := validateDocumentUpload(name, content); err != nil {
		return nil, err
	}
	contentHash := incrementalSHA256Hex(content)
	policyHash := parsePolicyHash(config)
	sourceKey, err := normalizeIncrementalSourceKey(sourceKey, uploaderID, name)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	ledger, err := s.Store.GetIncrementalLedger(ctx, tenantID, d.ID, sourceKey)
	if err != nil {
		return nil, err
	}
	if ledger != nil && ledger.State == model.IncrementalStateParsing {
		return nil, httperr.New(409, 40298, "incremental source already has an active parse")
	}
	if ledger != nil && ledger.State == model.IncrementalStateNeedsReview &&
		incrementalRevisionMatches(ledger, name, contentHash, policyHash, config.EmbeddingModel) {
		return nil, httperr.New(409, 40298, "incremental source requires review before retry")
	}
	if ledger != nil && isUnchangedRevision(ledger, name, contentHash, policyHash, config.EmbeddingModel) {
		docs, err := s.RAGFlow.ListDocuments(ctx, d.RAGFlowDatasetID)
		if err != nil {
			return nil, httperr.New(502, 50202, "ragflow list documents failed")
		}
		for _, doc := range docs {
			if doc.ID != ledger.RAGFlowDocumentID {
				continue
			}
			ledger.LastSeenAt = &now
			if err := s.Store.UpdateIncrementalLedger(ctx, ledger); err != nil {
				return nil, err
			}
			s.recordIncrementalAudit(ctx, tenantID, uploaderID, "dataset.document.incremental.unchanged", d.ID, sourceKey, ledger.Revision)
			summary := summaryFromRAGDoc(doc)
			summary.ParseTaskID = ledger.ParseTaskID
			return &summary, nil
		}
	}
	revision := int64(1)
	previousDocumentID := ""
	if ledger != nil {
		revision = ledger.Revision + 1
		previousDocumentID = ledger.RAGFlowDocumentID
	}
	doc, err := s.RAGFlow.CreateDocument(ctx, d.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: name, Content: content})
	if err != nil {
		return nil, httperr.New(502, 50201, "ragflow upload document failed")
	}
	ownership := &model.DocumentOwnershipLink{
		ID: id.New(), TenantID: tenantID, DatasetID: d.ID,
		DocumentID: doc.ID, UploaderID: uploaderID,
	}
	if err := s.Store.CreateDocumentOwnership(ctx, ownership); err != nil {
		s.rollbackExternalDocument(ctx, d.RAGFlowDatasetID, doc.ID, "ownership persistence failure")
		return nil, err
	}

	ledger = &model.IncrementalLedger{
		ID: id.New(), TenantID: tenantID, DatasetID: d.ID, SourceKey: sourceKey,
		Revision: revision, Name: name, ContentSHA256: contentHash,
		ParserPolicyHash: policyHash, EmbeddingRef: config.EmbeddingModel,
		RAGFlowDocumentID: doc.ID, PreviousDocumentID: previousDocumentID,
		State: model.IncrementalStateParsing, LastSeenAt: &now,
	}
	if err := s.Store.CreateIncrementalLedger(ctx, ledger); err != nil {
		s.rollbackExternalDocument(ctx, d.RAGFlowDatasetID, doc.ID, "ledger persistence failure")
		if deleteErr := s.Store.DeleteDocumentOwnership(ctx, tenantID, d.ID, []string{doc.ID}); deleteErr != nil {
			logger.Warn("failed to roll back document ownership after ledger persistence failure",
				"dataset_id", d.ID, "document_id", doc.ID, "error", deleteErr)
		}
		return nil, err
	}
	task := &model.Task{
		ID: id.New(), TenantID: tenantID, DatasetID: d.ID, DocID: doc.ID, DocName: doc.Name,
		TaskType: model.TaskTypeParse, Status: model.TaskStatusQueued,
	}
	if err := s.CreateTask(ctx, task); err != nil {
		ledger.State = model.IncrementalStateFailed
		ledger.FailureReason = "parse task projection unavailable"
		if updateErr := s.Store.UpdateIncrementalLedger(ctx, ledger); updateErr != nil {
			logger.Warn("failed to persist parse projection failure", "source_key", sourceKey, "error", updateErr)
		}
		s.rollbackExternalDocument(ctx, d.RAGFlowDatasetID, doc.ID, "parse task projection failure")
		if deleteErr := s.Store.DeleteDocumentOwnership(ctx, tenantID, d.ID, []string{doc.ID}); deleteErr != nil {
			logger.Warn("failed to roll back document ownership after parse projection failure",
				"dataset_id", d.ID, "document_id", doc.ID, "error", deleteErr)
		}
		return nil, httperr.New(502, 50212, "parse task projection failed")
	}
	ledger.ParseTaskID = task.ID
	if err := s.Store.UpdateIncrementalLedger(ctx, ledger); err != nil {
		logger.Warn("failed to link incremental parse task", "source_key", sourceKey, "error", err)
	}
	if err := s.RAGFlow.ParseDocuments(ctx, d.RAGFlowDatasetID, []string{doc.ID}); err != nil {
		ledger.State = model.IncrementalStateFailed
		ledger.FailureReason = err.Error()
		if updateErr := s.Store.UpdateIncrementalLedger(ctx, ledger); updateErr != nil {
			logger.Warn("failed to persist incremental parse failure", "source_key", sourceKey, "error", updateErr)
		}
		task.Status = model.TaskStatusFailed
		task.Detail = err.Error()
		if updateErr := s.Store.UpdateTask(ctx, task); updateErr != nil {
			logger.Warn("failed to persist parse acceptance failure", "task_id", task.ID, "error", updateErr)
		}
		s.rollbackExternalDocument(ctx, d.RAGFlowDatasetID, doc.ID, "auto-parse failure")
		if deleteErr := s.Store.DeleteDocumentOwnership(ctx, tenantID, d.ID, []string{doc.ID}); deleteErr != nil {
			logger.Warn("failed to roll back document ownership after auto-parse failure",
				"dataset_id", d.ID, "document_id", doc.ID, "error", deleteErr)
		}
		return nil, httperr.New(502, 50211, "ragflow auto-parse document failed")
	}
	if err := s.CreateTask(ctx, &model.Task{
		TenantID: tenantID, DatasetID: d.ID, DocID: doc.ID, DocName: doc.Name,
		TaskType: model.TaskTypeUpload, Status: model.TaskStatusDone,
	}); err != nil {
		logger.Warn("failed to create upload task projection", "document_id", doc.ID, "error", err)
	}
	s.recordIncrementalAudit(ctx, tenantID, uploaderID, "dataset.document.incremental.changed", d.ID, sourceKey, revision)
	s.invalidateDatasets()
	summary := summaryFromRAGDoc(*doc)
	summary.ParseTaskID = task.ID
	return &summary, nil
}

// ApplyIncrementalSourceBatch applies a bounded connector batch. Absence
// tombstones are destructive enough to require an explicit full snapshot.
// Existing approvals remain the only path that deletes an RAGFlow document.
func (s *Service) ApplyIncrementalSourceBatch(
	ctx context.Context, tenantID, datasetID, userID string, events []IncrementalSourceEvent, snapshot bool,
) (*IncrementalSourceBatch, error) {
	if len(events) > incrementalBatchLimit {
		return nil, httperr.BadRequest(40297, fmt.Sprintf("incremental batch exceeds %d events", incrementalBatchLimit))
	}
	result := &IncrementalSourceBatch{Failures: []IncrementalBatchFailure{}}
	seen := make(map[string]struct{}, len(events))
	d, err := s.resolveDataset(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if err := s.prepareDatasetParseReadiness(ctx, d); err != nil {
		return nil, err
	}
	config, err := s.RAGFlow.GetDatasetConfig(ctx, d.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50296, "read dataset parse policy failed")
	}
	for _, event := range events {
		operation := strings.ToLower(strings.TrimSpace(event.Operation))
		if operation == "" {
			operation = "upsert"
		}
		sourceKey := strings.TrimSpace(event.SourceKey)
		switch operation {
		case "upsert", "added", "changed":
			seen[sourceKey] = struct{}{}
			if sourceKey == "" || strings.TrimSpace(event.Name) == "" {
				result.Failures = append(result.Failures, IncrementalBatchFailure{
					SourceKey: sourceKey, Error: "source_key and name are required",
				})
				continue
			}
			action, err := s.classifyIncrementalEventWithConfig(ctx, tenantID, datasetID, sourceKey, event.Name, event.Content, config)
			if err != nil {
				result.Failures = append(result.Failures, IncrementalBatchFailure{SourceKey: sourceKey, Error: err.Error()})
				continue
			}
			if _, err := s.uploadDocumentWithDatasetConfig(ctx, tenantID, userID, event.Name, sourceKey, event.Content, d, config); err != nil {
				result.Failures = append(result.Failures, IncrementalBatchFailure{SourceKey: sourceKey, Error: err.Error()})
				continue
			}
			switch action {
			case "added":
				result.Added++
			case "unchanged":
				result.Unchanged++
			default:
				result.Changed++
			}
		case "removed":
			seen[sourceKey] = struct{}{}
			if sourceKey == "" {
				result.Failures = append(result.Failures, IncrementalBatchFailure{Error: "source_key is required"})
				continue
			}
			if err := s.TombstoneIncrementalSource(ctx, tenantID, datasetID, sourceKey, "connector removed event"); err != nil {
				result.Failures = append(result.Failures, IncrementalBatchFailure{SourceKey: sourceKey, Error: err.Error()})
				continue
			}
			result.Removed++
		default:
			result.Failures = append(result.Failures, IncrementalBatchFailure{
				SourceKey: sourceKey, Error: fmt.Sprintf("unsupported operation %q", operation),
			})
		}
	}

	now := time.Now().UTC()
	if snapshot {
		rows, err := s.Store.ListIncrementalLedger(ctx, tenantID, datasetID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if _, exists := seen[row.SourceKey]; exists {
				continue
			}
			if row.State != model.IncrementalStateActive && row.State != model.IncrementalStateNeedsReview {
				continue
			}
			if err := s.TombstoneIncrementalSource(ctx, tenantID, datasetID, row.SourceKey, "source absent from scan snapshot"); err != nil {
				result.Failures = append(result.Failures, IncrementalBatchFailure{SourceKey: row.SourceKey, Error: err.Error()})
				continue
			}
			result.Removed++
		}
	}
	rows, err := s.Store.ListIncrementalLedger(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.State == model.IncrementalStateTombstone && row.PurgeAfterAt != nil && !row.PurgeAfterAt.After(now) {
			result.ReadyForApproval++
		}
	}
	s.recordIncrementalAudit(ctx, tenantID, userID, "dataset.incremental.scan", datasetID, "", 0)
	return result, nil
}

// TombstoneIncrementalSource never removes knowledge immediately. It disables
// the engine document so removed knowledge stops participating in retrieval,
// records the observation deadline, and leaves physical deletion to approval.
func (s *Service) TombstoneIncrementalSource(ctx context.Context, tenantID, datasetID, sourceKey, reason string) error {
	ledger, err := s.Store.GetIncrementalLedger(ctx, tenantID, datasetID, sourceKey)
	if err != nil {
		return err
	}
	if ledger == nil || ledger.State == model.IncrementalStateTombstone {
		return nil
	}
	if s.RAGFlow.Name() != "mock" {
		dataset, err := s.resolveDataset(ctx, tenantID, datasetID)
		if err != nil {
			return err
		}
		if err := s.RAGFlow.SetDocumentsStatus(ctx, dataset.RAGFlowDatasetID, []string{ledger.RAGFlowDocumentID}, false); err != nil {
			return httperr.New(502, 50206, "disable removed document failed")
		}
	}
	now := time.Now().UTC()
	purgeAfter := now.Add(model.IncrementalTombstoneObservation)
	ledger.State = model.IncrementalStateTombstone
	ledger.TombstonedAt = &now
	ledger.PurgeAfterAt = &purgeAfter
	ledger.FailureReason = reason
	if err := s.Store.UpdateIncrementalLedger(ctx, ledger); err != nil {
		return err
	}
	s.recordIncrementalAudit(ctx, tenantID, "system", "dataset.document.incremental.removed", datasetID, sourceKey, ledger.Revision)
	return nil
}

func (s *Service) syncIncrementalLedgerForDataset(ctx context.Context, tenantID string, dataset *model.DatasetLink, documents []ragflow.Document) error {
	rows, err := s.Store.ListIncrementalLedger(ctx, tenantID, dataset.ID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	isMock := s.RAGFlow.Name() == "mock"
	var config *ragflow.DatasetConfig
	if !isMock {
		if err := s.prepareDatasetParseReadiness(ctx, dataset); err != nil {
			return err
		}
		config, err = s.RAGFlow.GetDatasetConfig(ctx, dataset.RAGFlowDatasetID)
		if err != nil {
			return httperr.New(502, 50296, "read dataset parse policy failed")
		}
	}
	docByID := make(map[string]ragflow.Document, len(documents))
	for _, document := range documents {
		docByID[document.ID] = document
	}
	for index := range rows {
		row := &rows[index]
		if row.State == model.IncrementalStateReplaced || row.State == model.IncrementalStateTombstone {
			continue
		}
		wasActive := row.State == model.IncrementalStateActive
		document, exists := docByID[row.RAGFlowDocumentID]
		status, _, detail := mapRAGFlowDocument(document)
		if !exists {
			row.State = model.IncrementalStateFailed
			row.FailureReason = "engine document no longer exists"
		} else if status == model.TaskStatusDone {
			if isMock || (document.ChunkCount > 0 && document.TokenCount > 0) {
				if !isMock {
					if row.EmbeddingRef != config.EmbeddingModel {
						row.State = model.IncrementalStateNeedsReview
						row.FailureReason = "embedding reference changed after parse"
					} else if document.Enabled != "1" {
						row.State = model.IncrementalStateNeedsReview
						row.FailureReason = "verified document is disabled"
					} else if err := s.verifyIncrementalChunkSample(ctx, dataset, &document); err != nil {
						row.State = model.IncrementalStateNeedsReview
						row.FailureReason = err.Error()
					}
				}
				if row.State == model.IncrementalStateParsing {
					now := time.Now().UTC()
					row.State = model.IncrementalStateActive
					row.VerifiedAt = &now
					row.FailureReason = ""
				}
			} else {
				row.State = model.IncrementalStateNeedsReview
				row.FailureReason = "run=done but chunk_count/token_count is zero"
				if !isMock {
					if err := s.RAGFlow.SetDocumentsStatus(ctx, dataset.RAGFlowDatasetID, []string{document.ID}, false); err != nil {
						logger.Warn("failed to disable empty parsed document", "document_id", document.ID, "error", err)
					}
				}
			}
		} else if status == model.TaskStatusFailed || status == model.TaskStatusStopped {
			row.State = model.IncrementalStateFailed
			row.FailureReason = strings.TrimSpace(detail)
			if row.FailureReason == "" {
				row.FailureReason = "engine parse did not complete"
			}
		} else {
			row.State = model.IncrementalStateParsing
		}
		if err := s.Store.UpdateIncrementalLedger(ctx, row); err != nil {
			return err
		}
		if row.State == model.IncrementalStateActive && !wasActive && status == model.TaskStatusDone {
			if err := s.replacePreviousIncrementalRevisions(ctx, tenantID, dataset, rows, row); err != nil {
				return err
			}
		}
		s.syncIncrementalParseTask(ctx, tenantID, row, status, document)
		if row.State == model.IncrementalStateActive && status == model.TaskStatusDone {
			s.recordIncrementalEvaluationImpact(ctx, tenantID, dataset, row.SourceKey, row.Revision)
		}
	}
	return nil
}

func (s *Service) verifyIncrementalChunkSample(ctx context.Context, dataset *model.DatasetLink, document *ragflow.Document) error {
	chunks, total, err := s.RAGFlow.ListDocumentChunks(ctx, dataset.RAGFlowDatasetID, document.ID, 1, 1)
	if err != nil {
		return fmt.Errorf("sample chunk lookup failed: %w", err)
	}
	if total <= 0 || len(chunks) == 0 {
		return fmt.Errorf("chunk_count is %d but sample lookup returned no chunks", document.ChunkCount)
	}
	chunk := chunks[0]
	if chunk.DocumentID != document.ID || !chunk.Available {
		return fmt.Errorf("sample chunk is unavailable for document %s", document.ID)
	}
	return nil
}

func (s *Service) syncIncrementalParseTask(ctx context.Context, tenantID string, ledger *model.IncrementalLedger, status string, document ragflow.Document) {
	if ledger.ParseTaskID == "" {
		return
	}
	tasks, err := s.Store.ListTasksByIDs(ctx, tenantID, []string{ledger.ParseTaskID})
	if err != nil || len(tasks) == 0 {
		return
	}
	task := tasks[0]
	progress := 100
	if status != model.TaskStatusDone {
		progress = int(document.Progress * 100)
		if progress < 0 || progress > 100 {
			progress = 0
		}
	}
	task.Status = status
	task.Progress = progress
	task.Detail = strings.TrimSpace(document.ProgressMsg)
	if ledger.State == model.IncrementalStateNeedsReview {
		task.Detail = strings.TrimSpace("needs_review: run=done but chunk_count/token_count is zero")
	} else if ledger.State == model.IncrementalStateFailed && ledger.FailureReason != "" {
		task.Detail = ledger.FailureReason
	}
	if err := s.Store.UpdateTask(ctx, &task); err != nil {
		logger.Warn("failed to sync incremental parse task", "task_id", task.ID, "error", err)
	}
}

func (s *Service) markIncrementalLedgerDeleted(ctx context.Context, tenantID, datasetID string, documentIDs []string) {
	deleted := make(map[string]struct{}, len(documentIDs))
	for _, documentID := range documentIDs {
		deleted[documentID] = struct{}{}
	}
	rows, err := s.Store.ListIncrementalLedger(ctx, tenantID, datasetID)
	if err != nil {
		logger.Warn("failed to inspect incremental ledger after deletion", "dataset_id", datasetID, "error", err)
		return
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if _, exists := deleted[row.RAGFlowDocumentID]; !exists {
			continue
		}
		row.State = model.IncrementalStateTombstone
		row.TombstonedAt = &now
		row.PurgeAfterAt = &now
		row.FailureReason = "approved document deletion completed"
		if err := s.Store.UpdateIncrementalLedger(ctx, &row); err != nil {
			logger.Warn("failed to mark incremental ledger deleted", "source_key", row.SourceKey, "error", err)
		}
	}
}

// tombstoneDeleteBlock returns a user-facing reason when a source is still in
// its mandatory observation window. Only the approval executor may delete due
// tombstones; contribution-scoped deletes are rejected even after expiry.
func (s *Service) tombstoneDeleteBlock(ctx context.Context, tenantID, datasetID string, documentIDs []string, allowDue bool) string {
	rows, err := s.Store.ListIncrementalLedger(ctx, tenantID, datasetID)
	if err != nil {
		return "incremental ledger lookup failed"
	}
	for _, row := range rows {
		if row.State != model.IncrementalStateTombstone {
			continue
		}
		for _, documentID := range documentIDs {
			if row.RAGFlowDocumentID != documentID {
				continue
			}
			if row.PurgeAfterAt == nil || row.PurgeAfterAt.After(time.Now().UTC()) {
				return "removed source is still in its observation period"
			}
			if !allowDue {
				return "expired tombstones require governance approval"
			}
			return ""
		}
	}
	return ""
}

func (s *Service) recordIncrementalEvaluationImpact(ctx context.Context, tenantID string, dataset *model.DatasetLink, sourceKey string, revision int64) {
	affected := make([]string, 0, 16)
	chatIDs := s.affectedChatIDs(ctx, tenantID, dataset)
	searchAppIDs := s.affectedSearchAppIDs(ctx, tenantID, dataset)
	affected = append(affected, chatIDs...)
	affected = append(affected, searchAppIDs...)
	if len(affected) == 0 {
		return
	}
	fields := map[string]string{
		"dataset_id": dataset.ID,
		"source_key": sourceKey,
		"revision":   fmt.Sprintf("%d", revision),
		"targets":    strings.Join(affected, ","),
	}
	if err := s.RecordAlert(ctx, notify.Event{
		Title: "Knowledge revision requires evaluation review", Severity: "warn",
		Type: "knowledge.incremental.impact", TenantID: tenantID,
		Resource: "dataset", ResourceID: dataset.ID,
		Detail: "A verified revision changed affected assistant datasets; run evaluations before release, but do not publish automatically.",
		Fields: fields,
	}); err != nil {
		logger.Warn("failed to record incremental evaluation impact", "dataset_id", dataset.ID, "error", err)
	}
}

func (s *Service) affectedChatIDs(ctx context.Context, tenantID string, dataset *model.DatasetLink) []string {
	const pageSize = 200
	chats := make([]model.ChatShadow, 0)
	for page := 1; page <= 100; page++ {
		rows, total, err := s.Store.ListChatShadows(ctx, tenantID, false, repository.ChatFilter{Status: model.TenantStatusActive}, page, pageSize)
		if err != nil {
			logger.Warn("failed to list chats for incremental impact", "dataset_id", dataset.ID, "page", page, "error", err)
			return chatDatasetBoundIDs(chats, dataset)
		}
		chats = append(chats, rows...)
		if int64(len(rows)) < pageSize || int64(len(chats)) >= total {
			break
		}
	}
	return chatDatasetBoundIDs(chats, dataset)
}

func (s *Service) affectedSearchAppIDs(ctx context.Context, tenantID string, dataset *model.DatasetLink) []string {
	const pageSize = 200
	apps := make([]model.SearchAppShadow, 0)
	for page := 1; page <= 100; page++ {
		rows, total, err := s.Store.ListSearchAppShadows(ctx, tenantID, false, repository.SearchAppFilter{Status: model.TenantStatusActive}, page, pageSize)
		if err != nil {
			logger.Warn("failed to list search apps for incremental impact", "dataset_id", dataset.ID, "page", page, "error", err)
			return searchAppDatasetBoundIDs(apps, dataset)
		}
		apps = append(apps, rows...)
		if int64(len(rows)) < pageSize || int64(len(apps)) >= total {
			break
		}
	}
	return searchAppDatasetBoundIDs(apps, dataset)
}

func (s *Service) replacePreviousIncrementalRevisions(
	ctx context.Context, tenantID string, dataset *model.DatasetLink,
	rows []model.IncrementalLedger, current *model.IncrementalLedger,
) error {
	isMock := s.RAGFlow.Name() == "mock"
	for index := range rows {
		if rows[index].SourceKey != current.SourceKey || rows[index].ID == current.ID {
			continue
		}
		if rows[index].Revision < current.Revision {
			previous := &rows[index]
			if !isMock {
				if err := s.RAGFlow.SetDocumentsStatus(ctx, dataset.RAGFlowDatasetID, []string{previous.RAGFlowDocumentID}, false); err != nil {
					return httperr.New(502, 50206, "disable replaced document failed")
				}
			}
			if previous.State != model.IncrementalStateReplaced {
				previous.State = model.IncrementalStateReplaced
				previous.FailureReason = "superseded by revision " + fmt.Sprintf("%d", current.Revision)
				if err := s.Store.UpdateIncrementalLedger(ctx, previous); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) classifyIncrementalEventWithConfig(
	ctx context.Context, tenantID, datasetID, sourceKey, name string,
	content []byte, config *ragflow.DatasetConfig,
) (string, error) {
	ledger, err := s.Store.GetIncrementalLedger(ctx, tenantID, datasetID, sourceKey)
	if err != nil {
		return "", err
	}
	if ledger == nil {
		return "added", nil
	}
	if isUnchangedRevision(ledger, name, incrementalSHA256Hex(content), parsePolicyHash(config), config.EmbeddingModel) {
		return "unchanged", nil
	}
	return "changed", nil
}

func isUnchangedRevision(ledger *model.IncrementalLedger, name, contentHash, policyHash, embeddingRef string) bool {
	return ledger != nil && ledger.State == model.IncrementalStateActive && ledger.VerifiedAt != nil &&
		incrementalRevisionMatches(ledger, name, contentHash, policyHash, embeddingRef)
}

func incrementalRevisionMatches(ledger *model.IncrementalLedger, name, contentHash, policyHash, embeddingRef string) bool {
	return ledger != nil &&
		ledger.Name == name &&
		ledger.ContentSHA256 == contentHash &&
		ledger.ParserPolicyHash == policyHash &&
		ledger.EmbeddingRef == embeddingRef
}

func normalizeIncrementalSourceKey(sourceKey, uploaderID, name string) (string, error) {
	sourceKey = strings.TrimSpace(sourceKey)
	if sourceKey == "" {
		sourceKey = "upload:" + strings.TrimSpace(uploaderID) + ":" + strings.TrimSpace(name)
	}
	if !utf8.ValidString(sourceKey) {
		return "", httperr.BadRequest(40296, "incremental source key must be UTF-8")
	}
	if len(sourceKey) > 255 {
		return "", httperr.BadRequest(40296, "incremental source key must be at most 255 characters")
	}
	return sourceKey, nil
}

func validateDocumentUpload(name string, content []byte) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return httperr.BadRequest(40000, "document name is required")
	}
	if len(content) == 0 {
		return httperr.BadRequest(40000, "document content is required")
	}
	if int64(len(content)) > maxDocumentUploadBytes {
		return httperr.New(413, 41300, "document exceeds the 25 MiB upload limit")
	}
	extension := strings.ToLower(filepath.Ext(name))
	switch extension {
	case ".txt", ".md", ".pdf", ".docx":
		return nil
	default:
		return httperr.BadRequest(40000, "document format must be txt, md, pdf or docx")
	}
}

func parsePolicyHash(config *ragflow.DatasetConfig) string {
	if config == nil {
		return incrementalSHA256Hex(nil)
	}
	raw, err := json.Marshal(map[string]interface{}{
		"chunk_method":  config.ChunkMethod,
		"parser_config": config.ParserConfig,
	})
	if err != nil {
		raw = []byte(config.ChunkMethod)
	}
	return incrementalSHA256Hex(raw)
}

func incrementalSHA256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (s *Service) rollbackExternalDocument(ctx context.Context, ragflowDatasetID, documentID, reason string) {
	if err := s.RAGFlow.DeleteDocuments(ctx, ragflowDatasetID, []string{documentID}); err != nil {
		logger.Warn("failed to roll back external document",
			"ragflow_dataset_id", ragflowDatasetID, "document_id", documentID,
			"reason", reason, "error", err)
	}
}

func (s *Service) recordIncrementalAudit(ctx context.Context, tenantID, userID, action, datasetID, sourceKey string, revision int64) {
	detail := map[string]interface{}{"dataset_id": datasetID}
	if sourceKey != "" {
		detail["source_key"] = sourceKey
	}
	if revision > 0 {
		detail["revision"] = revision
	}
	raw, _ := json.Marshal(detail)
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: action,
		Resource: "document", ResourceID: sourceKey, DetailJSON: string(raw),
	}); err != nil {
		logger.Warn("incremental audit write failed", "action", action, "error", err)
	}
}
