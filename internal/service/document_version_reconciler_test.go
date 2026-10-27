package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type versionPublishReconcilerFixture struct {
	svc         *Service
	ctx         context.Context
	tenant      *model.Tenant
	dataset     *DatasetSummary
	logical     *model.LogicalDocument
	previous    *model.DocumentVersion
	new         *model.DocumentVersion
	previousDoc string
	newDoc      string
}

func createVersionPublishReconcilerFixture(t *testing.T, name string) *versionPublishReconcilerFixture {
	t.Helper()
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: name, SourceURI: "connector:" + name,
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	previousDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: name + "-v1"})
	if err != nil {
		t.Fatalf("create previous RAGFlow document: %v", err)
	}
	newDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: name + "-v2"})
	if err != nil {
		t.Fatalf("create new RAGFlow document: %v", err)
	}
	if err := svc.RAGFlow.ReplaceDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, previousDoc.ID, map[string]interface{}{
		"business_owner": "risk-team",
	}); err != nil {
		t.Fatalf("seed previous metadata: %v", err)
	}
	if err := svc.RAGFlow.ReplaceDatasetDocumentMetadata(ctx, dataset.RAGFlowDatasetID, newDoc.ID, map[string]interface{}{
		"business_owner": "legal-team",
	}); err != nil {
		t.Fatalf("seed new metadata: %v", err)
	}

	previousFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newFrom := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	previous, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"9999999999999999999999999999999999999999999999999999999999999991", previousFrom, nil,
	))
	if err != nil {
		t.Fatalf("create previous version: %v", err)
	}
	previous.RAGFlowDocumentID = previousDoc.ID
	if err := svc.Store.UpdateDocumentVersion(ctx, previous); err != nil {
		t.Fatalf("bind previous version: %v", err)
	}
	if err := svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", previous.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatalf("prepare previous version: %v", err)
	}
	if changed, err := svc.Store.UpdateDocumentVersionStatus(ctx, tenant.ID, previous.ID, model.DocumentVersionPublishing, model.DocumentVersionActive); err != nil || !changed {
		t.Fatalf("activate previous version: changed=%t err=%v", changed, err)
	}
	previous.Status = model.DocumentVersionActive
	newVersion, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"9999999999999999999999999999999999999999999999999999999999999992", newFrom, nil,
	))
	if err != nil {
		t.Fatalf("create new version: %v", err)
	}
	newVersion.RAGFlowDocumentID = newDoc.ID
	if err := svc.Store.UpdateDocumentVersion(ctx, newVersion); err != nil {
		t.Fatalf("bind new version: %v", err)
	}
	return &versionPublishReconcilerFixture{
		svc: svc, ctx: ctx, tenant: tenant, dataset: dataset, logical: logical,
		previous: previous, new: newVersion, previousDoc: previousDoc.ID, newDoc: newDoc.ID,
	}
}

func (f *versionPublishReconcilerFixture) seedAttempt(t *testing.T, state string, now time.Time) *model.VersionPublishAttempt {
	t.Helper()
	previousMetadata, err := f.svc.RAGFlow.GetDatasetDocumentMetadata(f.ctx, f.dataset.RAGFlowDatasetID, f.previous.RAGFlowDocumentID)
	if err != nil {
		t.Fatalf("read previous metadata: %v", err)
	}
	newMetadata, err := f.svc.RAGFlow.GetDatasetDocumentMetadata(f.ctx, f.dataset.RAGFlowDatasetID, f.new.RAGFlowDocumentID)
	if err != nil {
		t.Fatalf("read new metadata: %v", err)
	}
	desired := map[string]map[string]interface{}{
		f.previous.RAGFlowDocumentID: documentVersionMetadata(previousMetadata, f.logical, f.previous, f.new.EffectiveFrom),
		f.new.RAGFlowDocumentID:      documentVersionMetadata(newMetadata, f.logical, f.new, time.Time{}),
	}
	rollback := map[string]map[string]interface{}{
		f.previous.RAGFlowDocumentID: copyDocumentMetadata(previousMetadata),
		f.new.RAGFlowDocumentID:      copyDocumentMetadata(newMetadata),
	}
	desiredJSON, err := documentVersionMetadataJSON(desired)
	if err != nil {
		t.Fatalf("encode desired metadata: %v", err)
	}
	rollbackJSON, err := documentVersionMetadataJSON(rollback)
	if err != nil {
		t.Fatalf("encode rollback metadata: %v", err)
	}
	previousID := f.previous.ID
	attempt := &model.VersionPublishAttempt{
		ID: id.New(), TenantID: f.tenant.ID, LogicalDocumentID: f.logical.ID,
		NewVersionID: f.new.ID, PreviousVersionID: &previousID, State: state,
		SourceStatus: model.DocumentVersionDraft, DesiredMetadata: desiredJSON,
		RollbackMetadata: rollbackJSON, Attempts: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	err = f.svc.Store.WithinTransaction(f.ctx, func(tx repository.Store) error {
		changed, updateErr := tx.UpdateDocumentVersionStatus(
			f.ctx, f.tenant.ID, f.new.ID, f.new.Status, model.DocumentVersionPublishing,
		)
		if updateErr != nil {
			return updateErr
		}
		if !changed {
			t.Fatal("target version transition failed while seeding")
		}
		return tx.CreateVersionPublishAttempt(f.ctx, attempt)
	})
	if err != nil {
		t.Fatalf("seed publish attempt: %v", err)
	}
	return attempt
}

func TestVersionPublishReconcilerCommitsRecoveredDesiredState(t *testing.T) {
	fixture := createVersionPublishReconcilerFixture(t, "Recover Commit")
	now := time.Now().UTC().Add(-2 * time.Minute)
	_ = fixture.seedAttempt(t, model.VersionPublishRAGFlowUpdate, now)
	for documentID, metadata := range map[string]map[string]interface{}{
		fixture.previous.RAGFlowDocumentID: documentVersionMetadata(mustMetadata(t, fixture, fixture.previousDoc), fixture.logical, fixture.previous, fixture.new.EffectiveFrom),
		fixture.new.RAGFlowDocumentID:      documentVersionMetadata(mustMetadata(t, fixture, fixture.newDoc), fixture.logical, fixture.new, time.Time{}),
	} {
		if err := fixture.svc.RAGFlow.ReplaceDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, documentID, metadata); err != nil {
			t.Fatalf("seed desired metadata: %v", err)
		}
	}
	if err := fixture.svc.ProcessVersionPublishReconciler(fixture.ctx, 10); err != nil {
		t.Fatalf("reconcile desired state: %v", err)
	}
	newVersion, err := fixture.svc.Store.GetDocumentVersion(fixture.ctx, fixture.tenant.ID, fixture.new.ID)
	if err != nil || newVersion.Status != model.DocumentVersionActive {
		t.Fatalf("recovered version status = %+v err=%v", newVersion, err)
	}
	previous, err := fixture.svc.Store.GetDocumentVersion(fixture.ctx, fixture.tenant.ID, fixture.previous.ID)
	if err != nil || previous.Status != model.DocumentVersionSuperseded || previous.EffectiveTo == nil || !previous.EffectiveTo.Equal(fixture.new.EffectiveFrom) {
		t.Fatalf("recovered previous version = %+v err=%v", previous, err)
	}
	events, err := fixture.svc.Store.ClaimOutboxEvents(fixture.ctx, 10, time.Now().UTC(), time.Minute)
	if err != nil || len(events) != 1 {
		t.Fatalf("recovered publish outbox events = %d err=%v", len(events), err)
	}
	attempts, _, err := fixture.svc.ListVersionPublishAttempts(fixture.ctx, fixture.tenant.ID, fixture.logical.ID, 1, 10)
	if err != nil || len(attempts) != 1 || attempts[0].State != model.VersionPublishCommitted {
		t.Fatalf("recovered attempts = %+v err=%v", attempts, err)
	}
}

func TestVersionPublishReconcilerRollsBackRecoveredRollbackState(t *testing.T) {
	fixture := createVersionPublishReconcilerFixture(t, "Recover Rollback")
	now := time.Now().UTC().Add(-2 * time.Minute)
	previousMetadata := mustMetadata(t, fixture, fixture.previousDoc)
	newMetadata := mustMetadata(t, fixture, fixture.newDoc)
	_ = fixture.seedAttempt(t, model.VersionPublishRAGFlowUpdate, now)
	rollback := map[string]map[string]interface{}{
		fixture.previous.RAGFlowDocumentID: copyDocumentMetadata(previousMetadata),
		fixture.new.RAGFlowDocumentID:      copyDocumentMetadata(newMetadata),
	}
	for documentID, metadata := range rollback {
		if err := fixture.svc.RAGFlow.ReplaceDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, documentID, metadata); err != nil {
			t.Fatalf("seed rollback metadata: %v", err)
		}
	}
	if err := fixture.svc.ProcessVersionPublishReconciler(fixture.ctx, 10); err != nil {
		t.Fatalf("reconcile rollback state: %v", err)
	}
	newVersion, err := fixture.svc.Store.GetDocumentVersion(fixture.ctx, fixture.tenant.ID, fixture.new.ID)
	if err != nil || newVersion.Status != model.DocumentVersionDraft {
		t.Fatalf("rolled back version status = %+v err=%v", newVersion, err)
	}
	previous, err := fixture.svc.Store.GetDocumentVersion(fixture.ctx, fixture.tenant.ID, fixture.previous.ID)
	if err != nil || previous.Status != model.DocumentVersionActive {
		t.Fatalf("rolled back previous status = %+v err=%v", previous, err)
	}
	events, err := fixture.svc.Store.ClaimOutboxEvents(fixture.ctx, 10, time.Now().UTC(), time.Minute)
	if err != nil || len(events) != 0 {
		t.Fatalf("rollback emitted outbox events = %d err=%v", len(events), err)
	}
	attempts, _, err := fixture.svc.ListVersionPublishAttempts(fixture.ctx, fixture.tenant.ID, fixture.logical.ID, 1, 10)
	if err != nil || len(attempts) != 1 || attempts[0].State != model.VersionPublishCompensated {
		t.Fatalf("rollback attempts = %+v err=%v", attempts, err)
	}
}

func TestVersionPublishReconcilerRollsBackMixedSafeState(t *testing.T) {
	fixture := createVersionPublishReconcilerFixture(t, "Recover Mixed")
	now := time.Now().UTC().Add(-2 * time.Minute)
	previousMetadata := mustMetadata(t, fixture, fixture.previousDoc)
	newMetadata := mustMetadata(t, fixture, fixture.newDoc)
	_ = fixture.seedAttempt(t, model.VersionPublishRAGFlowUpdate, now)
	if err := fixture.svc.RAGFlow.ReplaceDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, fixture.previousDoc,
		documentVersionMetadata(previousMetadata, fixture.logical, fixture.previous, fixture.new.EffectiveFrom),
	); err != nil {
		t.Fatalf("seed mixed desired metadata: %v", err)
	}
	if err := fixture.svc.RAGFlow.ReplaceDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, fixture.newDoc,
		copyDocumentMetadata(newMetadata),
	); err != nil {
		t.Fatalf("seed mixed rollback metadata: %v", err)
	}
	if err := fixture.svc.ProcessVersionPublishReconciler(fixture.ctx, 10); err != nil {
		t.Fatalf("reconcile mixed state: %v", err)
	}
	current, err := fixture.svc.RAGFlow.GetDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, fixture.previousDoc)
	if err != nil {
		t.Fatalf("read rolled back metadata: %v", err)
	}
	if _, exists := current["rgx_effective_to"]; exists {
		t.Fatalf("mixed state was not rolled back: %+v", current)
	}
	attempts, _, err := fixture.svc.ListVersionPublishAttempts(fixture.ctx, fixture.tenant.ID, fixture.logical.ID, 1, 10)
	if err != nil || len(attempts) != 1 || attempts[0].State != model.VersionPublishCompensated {
		t.Fatalf("mixed attempts = %+v err=%v", attempts, err)
	}
}

func TestVersionPublishReconcilerRequiresReviewForAmbiguousMetadata(t *testing.T) {
	fixture := createVersionPublishReconcilerFixture(t, "Ambiguous Metadata")
	now := time.Now().UTC().Add(-2 * time.Minute)
	previousMetadata := mustMetadata(t, fixture, fixture.previousDoc)
	_ = fixture.seedAttempt(t, model.VersionPublishRAGFlowUpdate, now)
	ambiguous := copyDocumentMetadata(previousMetadata)
	ambiguous["business_owner"] = "changed-by-operator"
	if err := fixture.svc.RAGFlow.ReplaceDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, fixture.previousDoc, ambiguous); err != nil {
		t.Fatalf("seed ambiguous metadata: %v", err)
	}
	if err := fixture.svc.ProcessVersionPublishReconciler(fixture.ctx, 10); err != nil {
		t.Fatalf("reconcile ambiguous state: %v", err)
	}
	current, err := fixture.svc.RAGFlow.GetDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, fixture.previousDoc)
	if err != nil || current["business_owner"] != "changed-by-operator" {
		t.Fatalf("ambiguous metadata was changed: %+v err=%v", current, err)
	}
	attempts, _, err := fixture.svc.ListVersionPublishAttempts(fixture.ctx, fixture.tenant.ID, fixture.logical.ID, 1, 10)
	if err != nil || len(attempts) != 1 || attempts[0].State != model.VersionPublishManualReview {
		t.Fatalf("ambiguous attempts = %+v err=%v", attempts, err)
	}
	alerts, _, err := fixture.svc.ListAlerts(fixture.ctx, fixture.tenant.ID, false, 1, 10, repository.AlertFilter{
		Type: "document_version.publish.manual_review",
	})
	if err != nil || len(alerts) != 1 {
		t.Fatalf("manual review alerts = %d err=%v", len(alerts), err)
	}
}

func TestVersionPublishReconcilerReleasesClaimOnProviderFailure(t *testing.T) {
	fixture := createVersionPublishReconcilerFixture(t, "Provider Failure")
	now := time.Now().UTC().Add(-2 * time.Minute)
	attempt := fixture.seedAttempt(t, model.VersionPublishRAGFlowUpdate, now)
	fixture.svc.RAGFlow = failingMetadataReadClient{Client: fixture.svc.RAGFlow}
	if err := fixture.svc.ProcessVersionPublishReconciler(fixture.ctx, 10); err != nil {
		t.Fatalf("reconcile provider failure: %v", err)
	}
	reloaded, err := fixture.svc.Store.GetVersionPublishAttempt(fixture.ctx, fixture.tenant.ID, attempt.ID)
	if err != nil {
		t.Fatalf("reload attempt: %v", err)
	}
	if reloaded.Attempts != 2 || reloaded.LastError == "" {
		t.Fatalf("provider failure did not record retry: %+v", reloaded)
	}
	if reloaded.ClaimedAt != nil || reloaded.ClaimExpiresAt != nil {
		t.Fatalf("provider failure left a claim: %+v", reloaded)
	}
}

func TestManualReviewPublishAttemptBlocksNewSupersede(t *testing.T) {
	fixture := createVersionPublishReconcilerFixture(t, "Manual Review Block")
	now := time.Now().UTC().Add(-2 * time.Minute)
	attempt := fixture.seedAttempt(t, model.VersionPublishRAGFlowUpdate, now)
	attempt.State = model.VersionPublishManualReview
	attempt.ClaimedAt = nil
	attempt.ClaimExpiresAt = nil
	if err := fixture.svc.Store.UpdateVersionPublishAttempt(fixture.ctx, attempt); err != nil {
		t.Fatalf("mark attempt manual review: %v", err)
	}
	// The open-attempt query intentionally includes manual_review, even though
	// it is outside the database partial unique index.
	open, err := fixture.svc.Store.GetOpenVersionPublishAttempt(fixture.ctx, fixture.tenant.ID, fixture.logical.ID)
	if err != nil || open == nil {
		t.Fatalf("manual review attempt is not open: %+v err=%v", open, err)
	}
	if _, err := fixture.svc.SupersedeDocumentVersion(fixture.ctx, fixture.tenant.ID, "admin", fixture.logical.ID, SupersedeDocumentVersionInput{
		VersionID: fixture.new.ID, Reason: "blocked by manual review",
	}); err == nil {
		t.Fatal("manual review attempt must block a new supersede")
	}
}

func mustMetadata(t *testing.T, fixture *versionPublishReconcilerFixture, documentID string) map[string]interface{} {
	t.Helper()
	metadata, err := fixture.svc.RAGFlow.GetDatasetDocumentMetadata(fixture.ctx, fixture.dataset.RAGFlowDatasetID, documentID)
	if err != nil {
		t.Fatalf("read metadata %s: %v", documentID, err)
	}
	return metadata
}

type failingMetadataReadClient struct {
	ragflow.Client
}

func (c failingMetadataReadClient) GetDatasetDocumentMetadata(ctx context.Context, datasetID, documentID string) (map[string]interface{}, error) {
	return nil, errors.New("simulated metadata read failure")
}
