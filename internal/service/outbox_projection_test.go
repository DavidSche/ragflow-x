package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func createOutboxServiceEvent(t *testing.T, svc *Service, tenantID string, published bool) string {
	t.Helper()
	now := time.Now().UTC()
	event := &model.OutboxEvent{
		ID: id.New(), TenantID: tenantID, EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload: `{"secret":"do-not-project"}`, LastError: "sensitive diagnostic",
		Attempts: 2, OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if published {
		publishedAt := now
		event.PublishedAt = &publishedAt
		event.Result = `{"status":"published","affected_eval_case_dependencies":1}`
	}
	if err := svc.Store.CreateOutboxEvent(context.Background(), event); err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	return event.ID
}

func TestOutboxEventServiceProjectionAndRetry(t *testing.T) {
	svc := newAuthzSvc(t)
	_ = svc.SetupWorker(DefaultWorkerConfig())
	ctx := context.Background()
	tenantID := id.New()
	pendingID := createOutboxServiceEvent(t, svc, tenantID, false)
	publishedID := createOutboxServiceEvent(t, svc, tenantID, true)
	otherTenantID := id.New()

	items, total, err := svc.ListOutboxEvents(ctx, tenantID, repository.OutboxEventFilter{}, 1, 20)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("list outbox events: total=%d items=%+v err=%v", total, items, err)
	}
	for _, item := range items {
		_ = item
	}

	if _, _, err = svc.ListOutboxEvents(ctx, tenantID, repository.OutboxEventFilter{Status: "invalid"}, 1, 20); err == nil {
		t.Fatal("invalid outbox status must be rejected")
	}
	if _, err = svc.GetOutboxEvent(ctx, otherTenantID, pendingID); err == nil {
		t.Fatal("cross-tenant outbox event must be rejected")
	}

	_, err = svc.RequestOutboxEventRetry(ctx, tenantID, "tester", publishedID)
	if httpErr, ok := err.(*httperr.Error); !ok || httpErr.Status != 409 {
		t.Fatalf("published retry status = %T %v", err, err)
	}
	view, err := svc.RequestOutboxEventRetry(ctx, tenantID, "tester", pendingID)
	if err != nil || view == nil || view.NextRetryAt == nil || view.ClaimedAt != nil {
		t.Fatalf("request retry: view=%+v err=%v", view, err)
	}
	jobs, _, err := svc.Store.ListJobs(ctx, SystemTenantID, model.JobKindOutboxDispatch, "", 1, 10)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("retry must schedule dispatch: jobs=%+v err=%v", jobs, err)
	}
	audits, _, err := svc.ListAudits(ctx, tenantID, 1, 20, repository.AuditFilter{Resource: "outbox-event"})
	if err != nil || len(audits) != 1 || audits[0].Action != "outbox_event.retry_requested" {
		t.Fatalf("retry audit: audits=%+v err=%v", audits, err)
	}
}
