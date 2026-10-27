package service

import (
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestTemporalVersionPublishOutboxRecoveryE2E(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	_ = svc.SetupWorker(DefaultWorkerConfig())
	logical, err := svc.CreateLogicalDocument(ctx, tenant.ID, "admin", LogicalDocumentInput{
		DatasetID: dataset.ID, Name: "Temporal Recovery Policy", SourceURI: "connector:recovery-policy",
	})
	if err != nil {
		t.Fatalf("create logical document: %v", err)
	}

	previousFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	previousTo := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	currentFrom := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	previous, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", previousFrom, &previousTo,
	))
	if err != nil {
		t.Fatalf("create previous version: %v", err)
	}
	current, _, err := svc.CreateDocumentVersion(ctx, tenant.ID, logical.ID, "admin", documentVersionInput(
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", currentFrom, nil,
	))
	if err != nil {
		t.Fatalf("create current version: %v", err)
	}

	previousDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "recovery-v1"})
	if err != nil {
		t.Fatalf("create previous RAGFlow document: %v", err)
	}
	currentDoc, err := svc.RAGFlow.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "recovery-v2"})
	if err != nil {
		t.Fatalf("create current RAGFlow document: %v", err)
	}
	previous.RAGFlowDocumentID = previousDoc.ID
	current.RAGFlowDocumentID = currentDoc.ID
	if err = svc.Store.UpdateDocumentVersion(ctx, previous); err != nil {
		t.Fatalf("bind previous version: %v", err)
	}
	if err = svc.Store.UpdateDocumentVersion(ctx, current); err != nil {
		t.Fatalf("bind current version: %v", err)
	}

	evalCaseID := "temporal-recovery-case"
	now := time.Now().UTC()
	if err = svc.Store.CreateEvalCases(ctx, tenant.ID, []model.EvalCase{{
		ID: evalCaseID, TenantID: tenant.ID, EvalSetID: "eval-set", Question: "question",
		Status: model.EvalStatusPublished, CreatedBy: "admin", CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("create eval case: %v", err)
	}
	input := evidenceSnapshotInput(evalCaseID, logical.ID)
	input.DatasetIDs = []string{dataset.ID}
	snapshot, err := svc.CreateEvidenceSnapshot(ctx, tenant.ID, "admin", input)
	if err != nil {
		t.Fatalf("create evidence snapshot: %v", err)
	}

	if err = svc.TransitionDocumentVersion(ctx, tenant.ID, "admin", previous.ID, model.DocumentVersionPublishing, "initial publish"); err != nil {
		t.Fatalf("publish previous version: %v", err)
	}
	if changed, err := svc.Store.UpdateDocumentVersionStatus(
		ctx, tenant.ID, previous.ID, model.DocumentVersionPublishing, model.DocumentVersionActive,
	); err != nil || !changed {
		t.Fatalf("activate previous version: changed=%t err=%v", changed, err)
	}
	if _, err = svc.SupersedeDocumentVersion(ctx, tenant.ID, "admin", logical.ID, SupersedeDocumentVersionInput{
		VersionID: current.ID, Reason: "replace temporal version",
	}); err != nil {
		t.Fatalf("supersede temporal version: %v", err)
	}

	previousVersion, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, previous.ID)
	if err != nil || previousVersion.Status != model.DocumentVersionSuperseded ||
		previousVersion.EffectiveTo == nil || !previousVersion.EffectiveTo.Equal(currentFrom) {
		t.Fatalf("previous temporal version after publish: %+v err=%v", previousVersion, err)
	}
	currentVersion, err := svc.Store.GetDocumentVersion(ctx, tenant.ID, current.ID)
	if err != nil || currentVersion.Status != model.DocumentVersionActive || !currentVersion.EffectiveFrom.Equal(currentFrom) {
		t.Fatalf("current temporal version after publish: %+v err=%v", currentVersion, err)
	}

	claimedAt := time.Now().UTC()
	events, err := svc.Store.ClaimOutboxEvents(ctx, 10, claimedAt, time.Minute)
	if err != nil || len(events) != 1 || events[0].EventType != model.EventTypeDocumentVersionPublished ||
		events[0].AggregateID != current.ID {
		t.Fatalf("claim publish outbox event: events=%+v err=%v", events, err)
	}
	if err = svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process while lease is active: %v", err)
	}
	bundle, err := svc.GetEvalCaseEvidence(ctx, tenant.ID, evalCaseID)
	if err != nil || bundle == nil || bundle.Evidence.StaleStatus != model.EvidenceStatusFresh {
		t.Fatalf("evidence must stay fresh while lease is active: bundle=%+v err=%v", bundle, err)
	}

	retried, err := svc.RequestOutboxEventRetry(ctx, tenant.ID, "admin", events[0].ID)
	if err != nil || retried == nil || retried.NextRetryAt == nil || retried.ClaimedAt != nil {
		t.Fatalf("request outbox recovery: view=%+v err=%v", retried, err)
	}
	if err = svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process after recovery request: %v", err)
	}
	remaining, err := svc.Store.ClaimOutboxEvents(ctx, 10, time.Now().UTC(), time.Minute)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("recovered event must be published: events=%+v err=%v", remaining, err)
	}
	bundle, err = svc.GetEvalCaseEvidence(ctx, tenant.ID, evalCaseID)
	if err != nil || bundle == nil || bundle.Evidence.StaleStatus != model.EvidenceStatusStale ||
		bundle.Evidence.StaleDetectedAt == nil {
		t.Fatalf("recovered event must stale evidence: bundle=%+v err=%v", bundle, err)
	}
	if bundle.Evidence.EvidenceSnapshotID != snapshot.Snapshot.ID {
		t.Fatalf("unexpected evidence snapshot: %+v", bundle.Evidence)
	}

	revalidated, err := svc.RevalidateEvalCaseEvidence(ctx, tenant.ID, "admin", evalCaseID)
	if err != nil || revalidated == nil || revalidated.Evidence.StaleStatus != model.EvidenceStatusRevalidated ||
		revalidated.Evidence.RevalidatedAt == nil {
		t.Fatalf("revalidate stale evidence: result=%+v err=%v", revalidated, err)
	}
}
