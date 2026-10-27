package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestProcessOutboxEventsClaimsAndMarksPublished(t *testing.T) {
	svc := newRunnerSvc(t)
	ctx := context.Background()
	now := time.Now().UTC()
	event := &model.OutboxEvent{
		ID: id.New(), TenantID: SystemTenantID, EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload:    `{"logical_document_id":"logical-1","new_version_id":"version-1","publish_attempt_id":"attempt-1","new_version":2}`,
		OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateOutboxEvent(ctx, event); err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox events: %v", err)
	}
	if events, err := svc.Store.ClaimOutboxEvents(ctx, 10, now, time.Minute); err != nil || len(events) != 0 {
		t.Fatalf("published event must not be reclaimed: events=%d err=%v", len(events), err)
	}
}

func TestProcessOutboxEventsRetriesInvalidPayload(t *testing.T) {
	svc := newRunnerSvc(t)
	ctx := context.Background()
	now := time.Now().UTC()
	event := &model.OutboxEvent{
		ID: id.New(), TenantID: SystemTenantID, EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload: `{`, OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateOutboxEvent(ctx, event); err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err == nil {
		t.Fatal("invalid payload must fail dispatch")
	}
	if events, err := svc.Store.ClaimOutboxEvents(ctx, 10, now, time.Minute); err != nil || len(events) != 0 {
		t.Fatalf("retrying event must not be immediately reclaimable: events=%d err=%v", len(events), err)
	}
}

func TestProcessOutboxEventsStalesMatchingEvidence(t *testing.T) {
	svc := newRunnerSvc(t)
	ctx := context.Background()
	tenantID, evalCaseID, logicalDocumentID := createEvidenceSnapshotFixture(t, svc)
	created, err := svc.CreateEvidenceSnapshot(ctx, tenantID, "tester", evidenceSnapshotInput(
		evalCaseID, logicalDocumentID,
	))
	if err != nil {
		t.Fatalf("create evidence snapshot: %v", err)
	}
	now := time.Now().UTC()
	event := &model.OutboxEvent{
		ID: id.New(), TenantID: tenantID, EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload:    `{"logical_document_id":"` + logicalDocumentID + `","new_version_id":"version-1","publish_attempt_id":"attempt-1","new_version":2}`,
		OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateOutboxEvent(ctx, event); err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox events: %v", err)
	}
	bundle, err := svc.Store.GetEvidenceSnapshotBundle(ctx, tenantID, created.Snapshot.ID)
	if err != nil || bundle == nil || bundle.Evidence.StaleStatus != model.EvidenceStatusStale ||
		bundle.Evidence.StaleDetectedAt == nil {
		t.Fatalf("matching evidence was not staled: bundle=%+v err=%v", bundle, err)
	}
	events, err := svc.Store.ClaimOutboxEvents(ctx, 10, now.Add(time.Minute), time.Minute)
	if err != nil || len(events) != 0 {
		t.Fatalf("published event must not be reclaimed: events=%d err=%v", len(events), err)
	}
}

func TestProcessOutboxEventsDoesNotStaleCrossTenantEvidence(t *testing.T) {
	svc := newRunnerSvc(t)
	ctx := context.Background()
	tenantID, evalCaseID, logicalDocumentID := createEvidenceSnapshotFixture(t, svc)
	created, err := svc.CreateEvidenceSnapshot(ctx, tenantID, "tester", evidenceSnapshotInput(
		evalCaseID, logicalDocumentID,
	))
	if err != nil {
		t.Fatalf("create evidence snapshot: %v", err)
	}
	now := time.Now().UTC()
	event := &model.OutboxEvent{
		ID: id.New(), TenantID: id.New(), EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload:    `{"logical_document_id":"` + logicalDocumentID + `","new_version_id":"version-1","publish_attempt_id":"attempt-1","new_version":2}`,
		OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateOutboxEvent(ctx, event); err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox events: %v", err)
	}
	bundle, err := svc.Store.GetEvidenceSnapshotBundle(ctx, tenantID, created.Snapshot.ID)
	if err != nil || bundle == nil || bundle.Evidence.StaleStatus != model.EvidenceStatusFresh ||
		bundle.Evidence.StaleDetectedAt != nil {
		t.Fatalf("cross-tenant event changed evidence: bundle=%+v err=%v", bundle, err)
	}
}
