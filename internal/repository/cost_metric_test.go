package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRecordCostMetricIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)

	first, err := store.RecordCostMetric(ctx, &model.CostMetric{
		RequestID: "req-1", TenantID: "t1", UserID: "u1", KeyID: "k1",
		Date: "2026-08-24", Model: "qwen-max", Scenario: "chat",
		SessionID: "sess-1", DatasetIDs: "d1,d2", TokensIn: 10, TokensOut: 5,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("expected first write to be recorded")
	}

	// Re-delivery with the same request_id must be ignored (idempotent).
	second, err := store.RecordCostMetric(ctx, &model.CostMetric{
		RequestID: "req-1", TenantID: "t1", UserID: "u1", KeyID: "k1",
		Date: "2026-08-24", Model: "qwen-max", Scenario: "chat", TokensIn: 10, TokensOut: 5,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("duplicate request_id must not be recorded twice")
	}

	items, total, err := store.ListCostMetrics(ctx, "t1", false, 1, 20, CostMetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 detail row, got total=%d len=%d", total, len(items))
	}
	if items[0].Model != "qwen-max" || items[0].SessionID != "sess-1" || items[0].DatasetIDs != "d1,d2" {
		t.Fatalf("detail attributes not persisted: %+v", items[0])
	}
}

func TestListCostMetricsFilterAndScope(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	mk := func(req, tenant, mdl, scenario string, at time.Time) {
		t.Helper()
		if _, err := store.RecordCostMetric(ctx, &model.CostMetric{
			RequestID: req, TenantID: tenant, UserID: "u1", KeyID: "key-1", ChatID: "chat-1", Date: "2026-08-24",
			Model: mdl, Scenario: scenario, TokensIn: 1, TokensOut: 1, CreatedAt: at,
		}); err != nil {
			t.Fatalf("record %s: %v", req, err)
		}
	}
	base := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	mk("r1", "t1", "qwen-max", "chat", base)
	mk("r2", "t1", "gpt-4o", "agent", base.Add(time.Hour))
	mk("r3", "t2", "qwen-max", "chat", base.Add(2*time.Hour))

	// Non-platform scope is constrained to the caller tenant.
	items, total, err := store.ListCostMetrics(ctx, "t1", false, 1, 20, CostMetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("tenant scope: got total=%d len=%d", total, len(items))
	}

	// Model exact filter.
	items, total, err = store.ListCostMetrics(ctx, "t1", false, 1, 20, CostMetricFilter{Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].RequestID != "r2" {
		t.Fatalf("model filter: got total=%d", total)
	}

	// Scenario exact filter.
	items, total, err = store.ListCostMetrics(ctx, "t1", false, 1, 20, CostMetricFilter{Scenario: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].RequestID != "r1" {
		t.Fatalf("scenario filter: got total=%d", total)
	}

	items, total, err = store.ListCostMetrics(ctx, "t1", false, 1, 20, CostMetricFilter{ChatID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("chat filter: got total=%d", total)
	}

	items, total, err = store.ListCostMetrics(ctx, "t1", true, 1, 20, CostMetricFilter{TenantID: "t2", ChatID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].TenantID != "t2" {
		t.Fatalf("tenant detail filter: got total=%d items=%+v", total, items)
	}

	// scopeAll sees every tenant.
	items, total, err = store.ListCostMetrics(ctx, "t1", true, 1, 20, CostMetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("scopeAll: got total=%d len=%d", total, len(items))
	}

	// Date-only range: inclusive start, exclusive full-day end.
	items, total, err = store.ListCostMetrics(ctx, "t1", false, 1, 20, CostMetricFilter{
		DateFrom: "2026-08-24", DateTo: "2026-08-24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("date range filter: got total=%d len=%d", total, len(items))
	}
}
