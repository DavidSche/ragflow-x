package repository

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func newOutboxProjectionStore(t *testing.T) *store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "outbox.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb).(*store)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createOutboxProjectionEvent(t *testing.T, s *store, tenantID string, published bool, attempts int) model.OutboxEvent {
	t.Helper()
	now := time.Now().UTC()
	event := model.OutboxEvent{
		ID: id.New(), TenantID: tenantID, EventType: model.EventTypeDocumentVersionPublished,
		AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
		Payload:   `{"secret":"do-not-project","logical_document_id":"logical-1"}`,
		LastError: "internal failure with sensitive diagnostic",
		Attempts:  attempts, OccurredAt: now.Add(-time.Second), CreatedAt: now, UpdatedAt: now,
	}
	if published {
		publishedAt := now
		event.PublishedAt = &publishedAt
		event.Result = `{"status":"published","affected_eval_case_dependencies":2,"internal_error":"secret"}`
	}
	if err := s.CreateOutboxEvent(context.Background(), &event); err != nil {
		t.Fatalf("create outbox event: %v", err)
	}
	return event
}

func TestOutboxProjectionIsTenantScopedAndRedacted(t *testing.T) {
	s := newOutboxProjectionStore(t)
	tenantA, tenantB := id.New(), id.New()
	event := createOutboxProjectionEvent(t, s, tenantA, true, 1)
	createOutboxProjectionEvent(t, s, tenantB, false, 2)

	items, total, err := s.ListOutboxEvents(context.Background(), tenantA, OutboxEventFilter{}, 1, 20)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != event.ID {
		t.Fatalf("tenant-scoped list mismatch: total=%d items=%+v err=%v", total, items, err)
	}
	if !reflect.DeepEqual(items[0].Result, map[string]interface{}{
		"status": "published", "affected_eval_case_dependencies": float64(2),
	}) {
		t.Fatalf("projection result mismatch: %+v", items[0])
	}
	if items[0].Status != OutboxEventStatusPublished {
		t.Fatalf("published status = %q", items[0].Status)
	}

	filter := OutboxEventFilter{Status: OutboxEventStatusRetrying}
	items, total, err = s.ListOutboxEvents(context.Background(), tenantB, filter, 1, 20)
	if err != nil || total != 1 || len(items) != 1 || items[0].Status != OutboxEventStatusRetrying {
		t.Fatalf("retry filter mismatch: total=%d items=%+v err=%v", total, items, err)
	}

	view, err := s.GetOutboxEvent(context.Background(), tenantA, event.ID)
	if err != nil || view == nil || view.ID != event.ID ||
		!reflect.DeepEqual(view.Result, map[string]interface{}{
			"status": "published", "affected_eval_case_dependencies": float64(2),
		}) {
		t.Fatalf("tenant-scoped get mismatch: view=%+v err=%v", view, err)
	}
	if view, err = s.GetOutboxEvent(context.Background(), tenantB, event.ID); err != nil || view != nil {
		t.Fatalf("cross-tenant get must be empty: view=%+v err=%v", view, err)
	}
}

func TestRetryOutboxEventIsTenantScopedAndDueOnly(t *testing.T) {
	s := newOutboxProjectionStore(t)
	tenant := id.New()
	pending := createOutboxProjectionEvent(t, s, tenant, false, 3)
	published := createOutboxProjectionEvent(t, s, tenant, true, 0)
	now := time.Now().UTC()

	view, retried, err := s.RetryOutboxEventForTenant(context.Background(), tenant, pending.ID, now)
	if err != nil || !retried || view == nil || view.NextRetryAt == nil {
		t.Fatalf("pending retry mismatch: view=%+v retried=%t err=%v", view, retried, err)
	}
	if view.Attempts != 3 || view.ClaimedAt != nil || view.ClaimExpiresAt != nil {
		t.Fatalf("retry must preserve attempts and release claim: %+v", view)
	}

	if view, retried, err = s.RetryOutboxEventForTenant(context.Background(), tenant, published.ID, now); err != nil || retried || view != nil {
		t.Fatalf("published retry mismatch: view=%+v retried=%t err=%v", view, retried, err)
	}
	if view, retried, err = s.RetryOutboxEventForTenant(context.Background(), id.New(), pending.ID, now); err != nil || retried || view != nil {
		t.Fatalf("cross-tenant retry mismatch: view=%+v retried=%t err=%v", view, retried, err)
	}
}
