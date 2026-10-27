package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func documentVersionInput(contentHash string, from time.Time, to *time.Time) DocumentVersionInput {
	return DocumentVersionInput{
		RAGFlowDocumentID: "ragflow-doc-" + contentHash[:8],
		ContentHash:       contentHash,
		EffectiveFrom:     from,
		EffectiveTo:       to,
		ChangeSummary:     "test revision",
	}
}

func TestCreateDocumentVersionDeduplicatesContentHash(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Policy", SourceURI: "connector:policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hash := "1111111111111111111111111111111111111111111111111111111111111111"
	first, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(hash, from, nil))
	if err != nil {
		t.Fatalf("create first version: %v", err)
	}
	duplicate, created, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(hash, from, nil))
	if err != nil || created {
		t.Fatalf("duplicate must return existing version: created=%t err=%v", created, err)
	}
	if duplicate.ID != first.ID || duplicate.Version != first.Version {
		t.Fatalf("unexpected duplicate: first=%+v duplicate=%+v", first, duplicate)
	}
}

func TestDocumentVersionRejectsInvalidRangeAndHash(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Policy", SourceURI: "connector:policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(-time.Second)
	hash := "2222222222222222222222222222222222222222222222222222222222222222"
	if _, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(hash, from, &to)); err == nil {
		t.Fatal("empty effective range must be rejected")
	}
	if _, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput("not-sha256", from, nil)); err == nil {
		t.Fatal("invalid content hash must be rejected")
	}
}

func TestPublishingDocumentVersionRejectsTemporalOverlap(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Temporal Policy", SourceURI: "connector:policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	firstFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	firstTo := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	first, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"3333333333333333333333333333333333333333333333333333333333333333", firstFrom, &firstTo,
	))
	if err != nil {
		t.Fatalf("create first version: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", first.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatalf("publish first version: %v", err)
	}

	secondFrom := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	second, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"4444444444444444444444444444444444444444444444444444444444444444", secondFrom, nil,
	))
	if err != nil {
		t.Fatalf("create draft version: %v", err)
	}
	err = svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", second.ID, model.DocumentVersionPublishing, "overlapping publish")
	if err == nil {
		t.Fatal("overlapping publishing versions must be rejected")
	}
	businessErr, ok := err.(*httperr.Error)
	if !ok || businessErr.Status != 409 || businessErr.Code != 40995 {
		t.Fatalf("expected temporal conflict 409/40995, got %#v", err)
	}
}

func TestDocumentVersionStateMachine(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "State Policy", SourceURI: "connector:policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	version, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"5555555555555555555555555555555555555555555555555555555555555555", from, nil,
	))
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", version.ID, model.DocumentVersionArchived, "withdraw draft"); err != nil {
		t.Fatalf("archive draft: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", version.ID, model.DocumentVersionPublishing, "invalid transition"); err == nil {
		t.Fatal("archived version must not publish")
	}
}

func TestDocumentVersionTransitionAuditRecordsSourceStatus(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Audited Policy", SourceURI: "connector:policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	version, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"6666666666666666666666666666666666666666666666666666666666666666", from, nil,
	))
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", version.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", version.ID, model.DocumentVersionDraft, "rework version"); err != nil {
		t.Fatalf("return version to draft: %v", err)
	}
	audits, _, err := svc.ListAudits(ctx, tenant.ID, 1, 20, repository.AuditFilter{Action: "document_version.transition"})
	if err != nil {
		t.Fatalf("list transition audits: %v", err)
	}
	if len(audits) != 2 {
		t.Fatalf("expected 2 transition audits, got %d", len(audits))
	}
	var detail struct {
		From string `json:"from"`
	}
	if err := json.Unmarshal([]byte(audits[0].DetailJSON), &detail); err != nil {
		t.Fatalf("unmarshal audit detail: %v", err)
	}
	if detail.From != model.DocumentVersionPublishing {
		t.Fatalf("audit from status = %q, want %q", detail.From, model.DocumentVersionPublishing)
	}
}

func TestSupersedeDocumentVersionPublishesMetadataAndAttempts(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Version Policy", SourceURI: "connector:policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	previousDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "policy-v1"})
	if err != nil {
		t.Fatalf("create previous ragflow document: %v", err)
	}
	newDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "policy-v2"})
	if err != nil {
		t.Fatalf("create new ragflow document: %v", err)
	}
	if err := svc.RAGFlow.ReplaceDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, previousDoc.ID, map[string]interface{}{
		"business_owner": "risk-team",
	}); err != nil {
		t.Fatalf("seed business metadata: %v", err)
	}

	previousFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newFrom := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	previous, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"7777777777777777777777777777777777777777777777777777777777777777", previousFrom, nil,
	))
	if err != nil {
		t.Fatalf("create previous version: %v", err)
	}
	previous.RAGFlowDocumentID = previousDoc.ID
	previous.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateDocumentVersion(ctx, previous); err != nil {
		t.Fatalf("bind previous ragflow document: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", previous.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatalf("prepare previous version: %v", err)
	}
	if changed, err := svc.Store.UpdateDocumentVersionStatus(ctx, tenant.ID, previous.ID, model.DocumentVersionPublishing, model.DocumentVersionActive); err != nil || !changed {
		t.Fatalf("activate previous version: changed=%t err=%v", changed, err)
	}
	previous.Status = model.DocumentVersionActive
	newVersion, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"8888888888888888888888888888888888888888888888888888888888888888", newFrom, nil,
	))
	if err != nil {
		t.Fatalf("create new version: %v", err)
	}
	newVersion.RAGFlowDocumentID = newDoc.ID
	newVersion.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateDocumentVersion(ctx, newVersion); err != nil {
		t.Fatalf("bind new ragflow document: %v", err)
	}

	result, err := svc.SupersedeDocumentVersion(ctx, tenant.ID, "admin", logical.ID, SupersedeDocumentVersionInput{
		VersionID: newVersion.ID, Reason: "quarterly policy refresh",
	})
	if err != nil {
		t.Fatalf("supersede document version: %v", err)
	}
	if result.PreviousVersion == nil || *result.PreviousVersion != previous.Version || result.PublishedVersion != newVersion.Version {
		t.Fatalf("unexpected publish result: %+v", result)
	}
	persistedPrevious, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, previous.ID)
	if err != nil {
		t.Fatalf("reload previous version: %v", err)
	}
	if persistedPrevious.Status != model.DocumentVersionSuperseded || persistedPrevious.EffectiveTo == nil || !persistedPrevious.EffectiveTo.Equal(newFrom) {
		t.Fatalf("previous version not superseded correctly: %+v", previous)
	}
	persistedNew, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, newVersion.ID)
	if err != nil {
		t.Fatalf("reload new version: %v", err)
	}
	if persistedNew.Status != model.DocumentVersionActive {
		t.Fatalf("new version status = %q, want active", persistedNew.Status)
	}
	attempts, _, err := svc.ListVersionPublishAttempts(ctx, tenant.ID, logical.ID, 1, 20)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("list publish attempts: %d items err=%v", len(attempts), err)
	}
	if attempts[0].State != model.VersionPublishCommitted || attempts[0].NewVersionID != newVersion.ID {
		t.Fatalf("unexpected publish attempt: %+v", attempts[0])
	}
	events, err := svc.Store.ClaimOutboxEvents(ctx, 10, time.Now().UTC(), time.Minute)
	if err != nil || len(events) != 1 {
		t.Fatalf("list publish events: %d items err=%v", len(events), err)
	}
	if events[0].EventType != model.EventTypeDocumentVersionPublished || events[0].AggregateID != newVersion.ID {
		t.Fatalf("unexpected outbox event: %+v", events[0])
	}
	var eventPayload DocumentVersionPublishedPayload
	if err := json.Unmarshal([]byte(events[0].Payload), &eventPayload); err != nil {
		t.Fatalf("decode publish event payload: %v", err)
	}
	if eventPayload.PublishAttemptID != attempts[0].ID || eventPayload.NewVersion != newVersion.Version {
		t.Fatalf("unexpected publish event payload: %+v", eventPayload)
	}
	if published, err := svc.Store.MarkOutboxEventPublished(ctx, events[0].ID, time.Now().UTC(), `{"status":"published"}`); err != nil || !published {
		t.Fatalf("mark publish event: published=%v err=%v", published, err)
	}
	previousMetadata, err := svc.RAGFlow.GetDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, previousDoc.ID)
	if err != nil {
		t.Fatalf("read previous metadata: %v", err)
	}
	if previousMetadata["business_owner"] != "risk-team" || previousMetadata["rgx_status"] != "active" {
		t.Fatalf("previous metadata lost business data or has wrong governance state: %+v", previousMetadata)
	}
	newMetadata, err := svc.RAGFlow.GetDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, newDoc.ID)
	if err != nil {
		t.Fatalf("read new metadata: %v", err)
	}
	if newMetadata["rgx_logical_doc_id"] != logical.ID || newMetadata["rgx_status"] != "active" {
		t.Fatalf("unexpected new metadata: %+v", newMetadata)
	}
}

type failingReplaceClient struct {
	ragflow.Client
	failDocumentID string
}

func (c failingReplaceClient) ReplaceDatasetDocumentMetadata(ctx context.Context, datasetID, documentID string, metadata map[string]interface{}) error {
	if documentID == c.failDocumentID {
		return errors.New("simulated metadata replace failure")
	}
	return c.Client.ReplaceDatasetDocumentMetadata(ctx, datasetID, documentID, metadata)
}

func TestSupersedeDocumentVersionRollsBackProviderFailure(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Rollback Policy", SourceURI: "connector:policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	previousDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "rollback-v1"})
	if err != nil {
		t.Fatalf("create previous ragflow document: %v", err)
	}
	newDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "rollback-v2"})
	if err != nil {
		t.Fatalf("create new ragflow document: %v", err)
	}
	if err := svc.RAGFlow.ReplaceDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, previousDoc.ID, map[string]interface{}{
		"business_owner": "audit-team",
	}); err != nil {
		t.Fatalf("seed business metadata: %v", err)
	}
	previousFrom := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	newFrom := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	previous, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"9999999999999999999999999999999999999999999999999999999999999999", previousFrom, nil,
	))
	if err != nil {
		t.Fatalf("create previous version: %v", err)
	}
	previous.RAGFlowDocumentID = previousDoc.ID
	previous.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateDocumentVersion(ctx, previous); err != nil {
		t.Fatalf("bind previous ragflow document: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", previous.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatalf("prepare previous version: %v", err)
	}
	if changed, err := svc.Store.UpdateDocumentVersionStatus(ctx, tenant.ID, previous.ID, model.DocumentVersionPublishing, model.DocumentVersionActive); err != nil || !changed {
		t.Fatalf("activate previous version: changed=%t err=%v", changed, err)
	}
	previous.Status = model.DocumentVersionActive
	newVersion, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", newFrom, nil,
	))
	if err != nil {
		t.Fatalf("create new version: %v", err)
	}
	newVersion.RAGFlowDocumentID = newDoc.ID
	newVersion.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateDocumentVersion(ctx, newVersion); err != nil {
		t.Fatalf("bind new ragflow document: %v", err)
	}
	failClient := failingReplaceClient{Client: svc.RAGFlow, failDocumentID: newDoc.ID}
	svc.RAGFlow = failClient

	if _, err := svc.SupersedeDocumentVersion(ctx, tenant.ID, "admin", logical.ID, SupersedeDocumentVersionInput{
		VersionID: newVersion.ID, Reason: "provider failure",
	}); err == nil {
		t.Fatal("provider replace failure must fail publish")
	}
	persistedPrevious, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, previous.ID)
	if err != nil {
		t.Fatalf("reload previous version: %v", err)
	}
	persistedNew, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, newVersion.ID)
	if err != nil {
		t.Fatalf("reload new version: %v", err)
	}
	if persistedNew.Status != model.DocumentVersionDraft {
		t.Fatalf("new version status = %q, want draft", persistedNew.Status)
	}
	if persistedPrevious.Status != model.DocumentVersionActive {
		t.Fatalf("previous version status = %q, want active", persistedPrevious.Status)
	}
	previousMetadata, err := svc.RAGFlow.GetDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, previousDoc.ID)
	if err != nil {
		t.Fatalf("read rolled back metadata: %v", err)
	}
	if previousMetadata["business_owner"] != "audit-team" {
		t.Fatalf("rollback did not preserve business metadata: %+v", previousMetadata)
	}
	for key := range previousMetadata {
		if strings.HasPrefix(key, "rgx_") {
			t.Fatalf("rollback left governance key %q", key)
		}
	}
	attempts, _, err := svc.ListVersionPublishAttempts(ctx, tenant.ID, logical.ID, 1, 20)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("list publish attempts: %d items err=%v", len(attempts), err)
	}
	if attempts[0].State != model.VersionPublishCompensated {
		t.Fatalf("attempt state = %q, want compensated", attempts[0].State)
	}
	if events, err := svc.Store.ClaimOutboxEvents(ctx, 10, time.Now().UTC(), time.Minute); err != nil || len(events) != 0 {
		t.Fatalf("rolled back publish must not emit outbox event: events=%d err=%v", len(events), err)
	}
}
