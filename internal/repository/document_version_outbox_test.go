package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestOutboxEventLeaseAndRetry(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "outbox.db")})
	if err != nil {
		t.Fatal(err)
	}
	s := &store{DB: gdb}
	t.Cleanup(func() { _ = s.Close() })
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	future := now.Add(time.Minute)
	events := []model.OutboxEvent{
		{ID: id.New(), TenantID: "tenant-1", EventType: model.EventTypeDocumentVersionPublished,
			AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
			Payload: "{}", OccurredAt: now.Add(-time.Second), CreatedAt: now, UpdatedAt: now},
		{ID: id.New(), TenantID: "tenant-1", EventType: model.EventTypeDocumentVersionPublished,
			AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
			Payload: "{}", OccurredAt: now.Add(-2 * time.Second), CreatedAt: now, UpdatedAt: now,
			ClaimExpiresAt: &future},
		{ID: id.New(), TenantID: "tenant-1", EventType: model.EventTypeDocumentVersionPublished,
			AggregateType: model.AggregateTypeDocumentVersion, AggregateID: id.New(),
			Payload: "{}", OccurredAt: now.Add(-3 * time.Second), CreatedAt: now, UpdatedAt: now,
			NextRetryAt: &future},
	}
	for index := range events {
		if err := s.CreateOutboxEvent(ctx, &events[index]); err != nil {
			t.Fatalf("create outbox event %d: %v", index, err)
		}
	}

	claimed, err := s.ClaimOutboxEvents(ctx, 10, now, time.Minute)
	if err != nil {
		t.Fatalf("claim due events: %v", err)
	}
	if len(claimed) != 1 {
		for index := range claimed {
			t.Logf("unexpected event %d: id=%s retry=%v lease=%v", index, claimed[index].ID, claimed[index].NextRetryAt, claimed[index].ClaimExpiresAt)
		}
		t.Fatalf("expected only the event without future lease and retry, got %d", len(claimed))
	}
	if again, err := s.ClaimOutboxEvents(ctx, 10, now, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("active lease must prevent duplicate claim: events=%d err=%v", len(again), err)
	}
	if published, err := s.MarkOutboxEventPublished(ctx, claimed[0].ID, now, `{"status":"published"}`); err != nil || !published {
		t.Fatalf("mark published: published=%v err=%v", published, err)
	}
	reclaimable, err := s.ClaimOutboxEvents(ctx, 10, now.Add(2*time.Minute), time.Minute)
	if err != nil || len(reclaimable) != 2 {
		t.Fatalf("published event must not be reclaimed but future events should be due: events=%d err=%v", len(reclaimable), err)
	}
	if err := s.RetryOutboxEvent(ctx, reclaimable[0].ID, 2, now.Add(5*time.Minute), "resolver unavailable"); err != nil {
		t.Fatalf("retry event: %v", err)
	}
}
