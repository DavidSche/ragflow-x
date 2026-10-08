package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func createSupersededDocumentVersionForRestore(t *testing.T) (*Service, context.Context, *model.Tenant, *DatasetSummary, *model.LogicalDocument, *model.DocumentVersion, *model.DocumentVersion) {
	t.Helper()
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Restorable Policy", SourceURI: "connector:restore-policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	previousDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "restore-v1"})
	if err != nil {
		t.Fatalf("create previous RAGFlow document: %v", err)
	}
	currentDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "restore-v2"})
	if err != nil {
		t.Fatalf("create current RAGFlow document: %v", err)
	}
	previousFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	currentFrom := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	previous, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", previousFrom, nil,
	))
	if err != nil {
		t.Fatalf("create previous version: %v", err)
	}
	previous.RAGFlowDocumentID = previousDoc.ID
	if err := svc.Store.UpdateDocumentVersion(ctx, previous); err != nil {
		t.Fatalf("bind previous version: %v", err)
	}
	current, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", currentFrom, nil,
	))
	if err != nil {
		t.Fatalf("create current version: %v", err)
	}
	current.RAGFlowDocumentID = currentDoc.ID
	if err := svc.Store.UpdateDocumentVersion(ctx, current); err != nil {
		t.Fatalf("bind current version: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", previous.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatalf("prepare previous version: %v", err)
	}
	if changed, err := svc.Store.UpdateDocumentVersionStatus(ctx, tenant.ID, previous.ID, model.DocumentVersionPublishing, model.DocumentVersionActive); err != nil || !changed {
		t.Fatalf("activate previous version: changed=%t err=%v", changed, err)
	}
	if _, err := svc.SupersedeDocumentVersion(ctx, tenant.ID, "admin", logical.ID, SupersedeDocumentVersionInput{
		VersionID: current.ID, Reason: "replace policy",
	}); err != nil {
		t.Fatalf("supersede version: %v", err)
	}
	initialEvents, err := svc.Store.ClaimOutboxEvents(ctx, 10, time.Now().UTC(), time.Minute)
	if err != nil || len(initialEvents) != 1 {
		t.Fatalf("initial publish outbox events = %d err=%v", len(initialEvents), err)
	}
	if _, err := svc.Store.MarkOutboxEventPublished(ctx, initialEvents[0].ID, time.Now().UTC(), `{"status":"published"}`); err != nil {
		t.Fatalf("publish initial outbox event: %v", err)
	}
	return svc, ctx, tenant, dataset, logical, previous, current
}

func TestRestoreDocumentVersionRepublishesHistoricalVersion(t *testing.T) {
	svc, ctx, tenant, dataset, logical, restored, current := createSupersededDocumentVersionForRestore(t)
	before, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, restored.ID)
	if err != nil || before.Version != 1 || before.ContentHash != restored.ContentHash {
		t.Fatalf("historical version identity changed before restore: %+v err=%v", before, err)
	}
	result, err := svc.RestoreDocumentVersion(ctx, tenant.ID, "admin", logical.ID, SupersedeDocumentVersionInput{
		VersionID: restored.ID, Reason: "restore reviewed policy",
	})
	if err != nil {
		t.Fatalf("restore version: %v", err)
	}
	if result.PublishedVersion != 1 || result.PreviousVersion == nil || *result.PreviousVersion != 2 {
		t.Fatalf("unexpected restore result: %+v", result)
	}
	restoredVersion, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, restored.ID)
	if err != nil {
		t.Fatalf("reload restored version: %v", err)
	}
	if restoredVersion.Status != model.DocumentVersionActive || restoredVersion.Version != 1 || restoredVersion.ContentHash != restored.ContentHash {
		t.Fatalf("restored version identity/state changed: %+v", restoredVersion)
	}
	if restoredVersion.EffectiveTo != nil || restoredVersion.EffectiveFrom.Before(time.Now().UTC().Add(-time.Second)) {
		t.Fatalf("restored version effective range = %+v", restoredVersion)
	}
	currentVersion, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, current.ID)
	if err != nil || currentVersion.Status != model.DocumentVersionSuperseded || currentVersion.EffectiveTo == nil || !currentVersion.EffectiveTo.Equal(restoredVersion.EffectiveFrom) {
		t.Fatalf("previous active after restore = %+v err=%v", currentVersion, err)
	}
	attempts, _, err := svc.ListVersionPublishAttempts(ctx, tenant.ID, logical.ID, 1, 10)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("restore attempts = %d err=%v", len(attempts), err)
	}
	if attempts[0].State != model.VersionPublishCommitted || attempts[0].SourceStatus != model.DocumentVersionSuperseded || attempts[0].NewVersionID != restored.ID {
		t.Fatalf("unexpected restore attempt: %+v", attempts[0])
	}
	events, err := svc.Store.ClaimOutboxEvents(ctx, 10, time.Now().UTC(), time.Minute)
	if err != nil || len(events) != 1 || events[0].AggregateID != restored.ID {
		t.Fatalf("restore outbox events = %+v err=%v", events, err)
	}
	audits, _, err := svc.ListAudits(ctx, tenant.ID, 1, 20, repository.AuditFilter{})
	if err != nil {
		t.Fatalf("list restore audits: %v", err)
	}
	if len(audits) == 0 || audits[0].Action != "document_version.restore" || audits[0].ResourceID != restored.ID {
		t.Fatalf("unexpected restore audit: %+v", audits)
	}
	metadata, err := svc.RAGFlow.GetDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, restored.RAGFlowDocumentID)
	if err != nil || metadata["rgx_version"] != int64(1) || metadata["rgx_status"] != "active" {
		t.Fatalf("restored RAGFlow metadata = %+v err=%v", metadata, err)
	}
}

func TestRestoreDocumentVersionRejectsDraftAndTemporalOverlap(t *testing.T) {
	svc, ctx, tenant, dataset, logical, _, current := createSupersededDocumentVersionForRestore(t)
	if _, err := svc.RestoreDocumentVersion(ctx, tenant.ID, "admin", logical.ID, SupersedeDocumentVersionInput{
		VersionID: current.ID, Reason: "restore active version",
	}); err == nil {
		t.Fatal("active version must not be restored")
	}
	futureDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "restore-future"})
	if err != nil {
		t.Fatalf("create future RAGFlow document: %v", err)
	}
	futureFrom := time.Now().UTC().Add(24 * time.Hour)
	future, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", futureFrom, nil,
	))
	if err != nil {
		t.Fatalf("create future version: %v", err)
	}
	future.RAGFlowDocumentID = futureDoc.ID
	if err := svc.Store.UpdateDocumentVersion(ctx, future); err != nil {
		t.Fatalf("bind future version: %v", err)
	}
	if _, err := svc.RestoreDocumentVersion(ctx, tenant.ID, "admin", logical.ID, SupersedeDocumentVersionInput{
		VersionID: future.ID, Reason: "restore draft version",
	}); err == nil {
		t.Fatal("draft version must not be restored")
	}
}
