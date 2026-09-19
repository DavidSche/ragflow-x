package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func createIncrementalFixture(t *testing.T) (*Service, context.Context, *model.Tenant, *DatasetSummary) {
	t.Helper()
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Incremental Docs")
	if err != nil {
		t.Fatal(err)
	}
	return svc, ctx, &tenant, dataset
}

func TestUploadDocumentReusesUnchangedIncrementalRevision(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	sourceKey := "connector:policy:1"

	first, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("same"))
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if first.ParseTaskID == "" {
		t.Fatalf("first upload must expose a parse task id: %+v", first)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatalf("sync first revision: %v", err)
	}

	second, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("same"))
	if err != nil {
		t.Fatalf("unchanged upload should reuse active revision: %v", err)
	}
	if second.ID != first.ID || second.ParseTaskID != first.ParseTaskID {
		t.Fatalf("unchanged upload created a new revision: first=%+v second=%+v", first, second)
	}
	docs, err := svc.ListDocuments(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("unchanged upload must not create another RAGFlow document: %+v", docs)
	}
}

func TestChangedRevisionOnlyReplacesActiveAfterVerification(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	sourceKey := "connector:policy:1"

	active, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("old"))
	if err != nil {
		t.Fatalf("active upload: %v", err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatalf("verify active upload: %v", err)
	}
	changed, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("new"))
	if err != nil {
		t.Fatalf("changed upload: %v", err)
	}
	if changed.ID == active.ID {
		t.Fatal("changed content must create a new RAGFlow document")
	}

	docs, err := svc.ListDocuments(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("old revision must remain while the new revision is parsing: %+v", docs)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatalf("sync changed revision: %v", err)
	}

	rows, err := svc.Store.ListIncrementalLedger(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected old and new ledger revisions: %+v", rows)
	}
	for _, row := range rows {
		switch row.RAGFlowDocumentID {
		case active.ID:
			if row.State != model.IncrementalStateReplaced || row.VerifiedAt == nil {
				t.Fatalf("old revision should be replaced after verification: %+v", row)
			}
		case changed.ID:
			if row.State != model.IncrementalStateActive || row.VerifiedAt == nil {
				t.Fatalf("new revision should become verified active: %+v", row)
			}
		default:
			t.Fatalf("unexpected ledger row: %+v", row)
		}
	}
}

func TestRemovedSourceTombstonesDuringObservationPeriod(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	sourceKey := "connector:policy:1"
	if _, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("old")); err != nil {
		t.Fatalf("upload removed source: %v", err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}

	result, err := svc.ApplyIncrementalSourceBatch(ctx, tenant.ID, dataset.ID, "admin", nil, true)
	if err != nil {
		t.Fatalf("source batch without the seen key should tombstone: %v", err)
	}
	if result.Removed != 1 || result.ReadyForApproval != 0 {
		t.Fatalf("unexpected removal result: %+v", result)
	}
	rows, err := svc.Store.ListIncrementalLedger(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != model.IncrementalStateTombstone || rows[0].PurgeAfterAt == nil {
		t.Fatalf("removed source should be tombstoned for observation: %+v", rows)
	}
	if rows[0].PurgeAfterAt.Truncate(time.Second).Before(time.Now().UTC().Truncate(time.Second).Add(model.IncrementalTombstoneObservation)) {
		t.Fatalf("purge deadline should respect the observation period: %+v", rows[0])
	}
	documentID := rows[0].RAGFlowDocumentID
	if err := svc.DeleteDocuments(ctx, tenant.ID, dataset.ID, []string{documentID}); err == nil {
		t.Fatal("observed tombstone must not be deleted before the deadline")
	}
}

func TestStreamedCitationReferencesPreserveRevisionSnapshot(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	document, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", "connector:policy:1", []byte("citation"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{
		ID: "chat-1", TenantID: tenant.ID, Name: "Incremental Chat", Status: model.TenantStatusActive,
		DatasetIDs: dataset.RAGFlowDatasetID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	citation := []map[string]interface{}{{
		"dataset_id":  dataset.RAGFlowDatasetID,
		"document_id": document.ID,
		"chunk_id":    "chunk-1",
		"content":     "citation snapshot",
	}}
	svc.recordCitationReferences(ctx, tenant.ID, "chat-1", "session-1", "request-1", citation)

	rows, err := svc.Store.ListCitationReferences(ctx, tenant.ID, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChunkID != "chunk-1" || rows[0].Revision != 1 || rows[0].Content != "citation snapshot" {
		t.Fatalf("citation snapshot was not preserved: %+v", rows)
	}
}

func TestIncrementalRevisionStatesGuardReuploads(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	sourceKey := "connector:policy:1"
	first, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("same"))
	if err != nil {
		t.Fatalf("initial upload: %v", err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}

	setLatestState := func(state, reason string) {
		t.Helper()
		rows, err := svc.Store.ListIncrementalLedger(ctx, tenant.ID, dataset.ID)
		if err != nil {
			t.Fatal(err)
		}
		rows[0].State = state
		rows[0].FailureReason = reason
		if err := svc.Store.UpdateIncrementalLedger(ctx, &rows[0]); err != nil {
			t.Fatal(err)
		}
	}
	setLatestState(model.IncrementalStateParsing, "")
	_, err = svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("same"))
	assertIncrementalConflict(t, err)

	setLatestState(model.IncrementalStateNeedsReview, "run=done but chunk_count/token_count is zero")
	_, err = svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("same"))
	assertIncrementalConflict(t, err)

	setLatestState(model.IncrementalStateFailed, "engine parse did not complete")
	retry, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", sourceKey, []byte("same"))
	if err != nil {
		t.Fatalf("failed revision should allow a new retry revision: %v", err)
	}
	if retry.ID == first.ID {
		t.Fatal("retry after failure must create a new engine revision")
	}
}

func TestEmptyIncrementalBatchRequiresExplicitSnapshot(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	if _, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", "connector:policy:1", []byte("same")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ApplyIncrementalSourceBatch(ctx, tenant.ID, dataset.ID, "admin", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.ReadyForApproval != 0 {
		t.Fatalf("partial scan must not tombstone by absence: %+v", result)
	}
	rows, err := svc.Store.ListIncrementalLedger(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != model.IncrementalStateActive {
		t.Fatalf("partial scan must preserve active source: %+v", rows)
	}
	result, err = svc.ApplyIncrementalSourceBatch(ctx, tenant.ID, dataset.ID, "admin", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 {
		t.Fatalf("explicit empty snapshot should tombstone by absence: %+v", result)
	}
}

func TestVerifiedRevisionDisablesPreviousEngineDocument(t *testing.T) {
	svc, ctx, tenant, _ := createIncrementalFixture(t)
	baseMock := ragflow.NewMock()
	svc.RAGFlow = baseMock
	verifiedDataset, err := svc.CreateDataset(ctx, tenant.ID, "Verified Replace Docs")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &documentStatusRecorder{Client: baseMock, disabled: map[string]bool{}}
	svc.RAGFlow = recorder
	active, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, verifiedDataset.ID, "admin", "policy.md", "connector:policy:1", []byte("old"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, verifiedDataset.ID, "admin", "policy.md", "connector:policy:1", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if !recorder.disabled[active.ID] {
		t.Fatalf("replaced revision must be disabled: %+v", recorder.disabled)
	}
	rows, err := svc.Store.ListIncrementalLedger(ctx, tenant.ID, verifiedDataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].State != model.IncrementalStateReplaced || rows[1].State != model.IncrementalStateActive {
		t.Fatalf("revision states are incorrect: %+v", rows)
	}
}

func TestDueTombstoneRequiresApprovalExecutor(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	if _, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", "connector:policy:1", []byte("same")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyIncrementalSourceBatch(ctx, tenant.ID, dataset.ID, "admin", nil, true); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.Store.ListIncrementalLedger(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	rows[0].PurgeAfterAt = &past
	if err := svc.Store.UpdateIncrementalLedger(ctx, &rows[0]); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDocuments(ctx, tenant.ID, dataset.ID, []string{rows[0].RAGFlowDocumentID}); err == nil {
		t.Fatal("public delete path must not remove a due tombstone")
	}
	if err := svc.DeleteApprovedDocuments(ctx, tenant.ID, dataset.ID, []string{rows[0].RAGFlowDocumentID}); err != nil {
		t.Fatalf("approval executor should remove due tombstone: %v", err)
	}
}

func TestUploadFailsWithoutParseTaskProjection(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	svc.Store = &failingTaskStore{Store: svc.Store}
	_, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.md", "connector:policy:1", []byte("same"))
	if err == nil {
		t.Fatal("upload must fail when parse task projection cannot be persisted")
	}
	var apiErr *httperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != 50212 {
		t.Fatalf("expected parse projection failure, got %v", err)
	}
	docs, err := svc.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("engine document must roll back when projection fails: %+v", docs)
	}
	rows, err := svc.Store.ListIncrementalLedger(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != model.IncrementalStateFailed {
		t.Fatalf("failure evidence must remain in ledger: %+v", rows)
	}
}

func assertIncrementalConflict(t *testing.T, err error) {
	t.Helper()
	var apiErr *httperr.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Code != 40298 {
		t.Fatalf("expected incremental conflict, got %v", err)
	}
}

type documentStatusRecorder struct {
	ragflow.Client
	disabled map[string]bool
}

func (r *documentStatusRecorder) Name() string { return "http" }

func (r *documentStatusRecorder) GetDatasetConfig(ctx context.Context, datasetID string) (*ragflow.DatasetConfig, error) {
	return &ragflow.DatasetConfig{
		ID: datasetID, ChunkMethod: "naive", Permission: "team",
		EmbeddingModel: "canonical-embedding",
		ParserConfig:   map[string]interface{}{},
	}, nil
}

func (r *documentStatusRecorder) ListModels(ctx context.Context, modelType string) ([]ragflow.AddedModel, error) {
	if modelType != "embedding" {
		return nil, nil
	}
	return []ragflow.AddedModel{{ModelID: "canonical-embedding", Name: "canonical-embedding", Type: []string{"embedding"}}}, nil
}

func (r *documentStatusRecorder) ListDocuments(ctx context.Context, datasetID string) ([]ragflow.Document, error) {
	docs, err := r.Client.ListDocuments(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	for index := range docs {
		docs[index].Enabled = "1"
		docs[index].ChunkCount = 1
		docs[index].TokenCount = 1
	}
	return docs, nil
}

func (r *documentStatusRecorder) ListDocumentChunks(ctx context.Context, datasetID, documentID string, page, pageSize int) ([]ragflow.Chunk, int64, error) {
	if page != 1 || pageSize != 1 {
		return nil, 0, nil
	}
	return []ragflow.Chunk{{ID: "sample", DocumentID: documentID, Available: true}}, 1, nil
}

func (r *documentStatusRecorder) SetDocumentsStatus(ctx context.Context, datasetID string, documentIDs []string, enabled bool) error {
	if enabled {
		return nil
	}
	for _, documentID := range documentIDs {
		r.disabled[documentID] = true
	}
	return nil
}

type failingTaskStore struct {
	repository.Store
}

func (s *failingTaskStore) CreateTask(ctx context.Context, task *model.Task) error {
	return errors.New("task store unavailable")
}
