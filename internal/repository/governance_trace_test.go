package repository

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestTraceRunProjectionAndTenantScope(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	run := &model.TraceRun{
		TenantID: "t1", TraceID: "request-1", SpanID: "root", RequestID: "request-1",
		SessionID: "session-1", UserID: "user-1", AppType: "chat", AppID: "chat-1",
		Status: model.TraceRunCompleted, QualitySummaryJSON: `{"citations_count":2}`,
		EvidencePointersJSON: `{"knowledge_ops_event_id":"event-1"}`,
	}
	if err := store.UpsertTraceRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	found, err := store.GetTraceRunByTraceID(ctx, "t1", "request-1", false)
	if err != nil || found.ID != run.ID || found.Status != model.TraceRunCompleted {
		t.Fatalf("get trace run: run=%+v err=%v", found, err)
	}
	if _, err := store.GetTraceRunByTraceID(ctx, "t2", "request-1", false); err == nil {
		t.Fatal("cross-tenant trace read must fail")
	}
	run.Status = model.TraceRunNoAnswer
	run.QualitySummaryJSON = `{"citations_count":0}`
	if err := store.UpsertTraceRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	found, err = store.GetTraceRunByTraceID(ctx, "t1", "request-1", false)
	if err != nil || found.Status != model.TraceRunNoAnswer || found.QualitySummaryJSON != run.QualitySummaryJSON {
		t.Fatalf("trace run was not idempotently updated: run=%+v err=%v", found, err)
	}
	tasks, total, err := store.ListTraceRuns(ctx, "t2", false, 1, 10, TraceRunFilter{})
	if err != nil || total != 0 || len(tasks) != 0 {
		t.Fatalf("tenant scoped list: total=%d items=%d err=%v", total, len(tasks), err)
	}
	filtered, total, err := store.ListTraceRuns(ctx, "t1", false, 1, 10, TraceRunFilter{Status: model.TraceRunNoAnswer})
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].ID != run.ID {
		t.Fatalf("filtered trace list: total=%d items=%d err=%v", total, len(filtered), err)
	}
}
