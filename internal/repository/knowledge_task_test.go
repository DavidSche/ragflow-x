package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestKnowledgeTaskLifecycleAndTenantScope(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	due := time.Now().UTC().Add(-24 * time.Hour)
	task := &model.KnowledgeTask{
		TenantID: "t1", SourceEventID: "event-1", SourceRequestID: "request-1",
		SourceAttribution: model.FeedbackAttributionRetrieval, Title: "修复检索配置",
		Description: "调整切分与元数据", Category: model.FeedbackAttributionRetrieval,
		OwnerID: "owner-1", DueAt: &due, Priority: model.KnowledgeTaskPriorityHigh,
		RegressionStatus: model.KnowledgeTaskRegressionPending, CreatedBy: "creator",
	}
	if err := store.CreateKnowledgeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	found, err := store.GetKnowledgeTask(ctx, "t1", task.ID, false)
	if err != nil || found.ID != task.ID || found.Status != model.KnowledgeTaskOpen {
		t.Fatalf("get task: task=%+v err=%v", found, err)
	}
	if _, err := store.GetKnowledgeTask(ctx, "t2", task.ID, false); err == nil {
		t.Fatal("cross-tenant task read must fail")
	}
	updated, err := store.UpdateKnowledgeTask(ctx, "t1", task.ID, false, map[string]interface{}{
		"status": model.KnowledgeTaskInProgress, "regression_eval_set_id": "set-1",
	})
	if err != nil || !updated {
		t.Fatalf("update task: updated=%v err=%v", updated, err)
	}
	found, err = store.GetKnowledgeTask(ctx, "t1", task.ID, false)
	if err != nil || found.Status != model.KnowledgeTaskInProgress || found.RegressionEvalSetID != "set-1" {
		t.Fatalf("updated task: task=%+v err=%v", found, err)
	}
	summary, err := store.KnowledgeTaskSummary(ctx, "t1", false)
	if err != nil || summary.InProgress != 1 || summary.Overdue != 1 {
		t.Fatalf("summary: summary=%+v err=%v", summary, err)
	}
	tasks, total, err := store.ListKnowledgeTasks(ctx, "t2", false, 1, 10, KnowledgeTaskFilter{})
	if err != nil || total != 0 || len(tasks) != 0 {
		t.Fatalf("tenant scope: total=%d tasks=%d err=%v", total, len(tasks), err)
	}
	filtered, total, err := store.ListKnowledgeTasks(ctx, "t1", false, 1, 10, KnowledgeTaskFilter{
		Status: model.KnowledgeTaskInProgress, Category: model.FeedbackAttributionRetrieval, Overdue: true,
	})
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].ID != task.ID {
		t.Fatalf("filtered list: total=%d tasks=%d err=%v", total, len(filtered), err)
	}
}
