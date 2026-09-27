package repository

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestOperationalAttribution(t *testing.T) {
	ctx := context.Background()
	store := newFilterStore(t)
	seed := func(tenantID, requestID, appType, status string, projectID, assistantID, releaseID string) {
		t.Helper()
		if err := store.UpsertTraceRun(ctx, &model.TraceRun{
			TenantID: tenantID, TraceID: requestID, RequestID: requestID, UserID: "user-1",
			ProjectID: projectID, AssistantID: assistantID, AssistantReleaseID: releaseID,
			AppType: appType, AppID: assistantID, Status: status,
		}); err != nil {
			t.Fatalf("upsert trace run: %v", err)
		}
		if err := store.UpsertKnowledgeOpsEvent(ctx, &model.KnowledgeOpsEvent{
			RequestID: requestID, TenantID: tenantID, UserID: "user-1",
			AppType: appType, AppID: assistantID, Status: status,
			DurationMs: 120, TokensIn: 30, TokensOut: 20,
		}); err != nil {
			t.Fatalf("upsert knowledge ops event: %v", err)
		}
		if _, err := store.RecordCostMetric(ctx, &model.CostMetric{
			RequestID: requestID, TenantID: tenantID, UserID: "user-1", Date: "2026-09-20",
			Model: appType, Scenario: appType, TokensIn: 25, TokensOut: 15,
			EstimatedCost: 0.008, Estimated: true, EstimationPolicyVersion: "v1",
		}); err != nil {
			t.Fatalf("record cost metric: %v", err)
		}
	}

	seed("tenant-a", "op-1", "chat", model.KnowledgeOpsCompleted, "project-a", "assistant-a", "release-a")
	seed("tenant-a", "op-2", "chat", model.KnowledgeOpsNoAnswer, "project-a", "assistant-a", "release-a")
	seed("tenant-a", "op-3", "agent", model.KnowledgeOpsFailed, "project-b", "assistant-b", "release-b")
	seed("tenant-b", "op-4", "chat", model.KnowledgeOpsCompleted, "project-c", "assistant-c", "release-c")

	rows, err := store.SummarizeOperationsByAttribution(ctx, "tenant-a", false, OperationalReportFilter{})
	if err != nil {
		t.Fatalf("tenant-scoped report: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 tenant-scoped rows, got %d: %+v", len(rows), rows)
	}
	byScenario := map[string]model.OperationalAttributionRow{}
	for _, row := range rows {
		byScenario[row.Scenario] = row
	}
	chat := byScenario["chat"]
	if chat.Requests != 2 || chat.Failed != 0 || chat.NoAnswer != 1 || chat.TokensIn != 60 ||
		chat.TokensOut != 40 || chat.EstimatedCost != 0.016 || chat.AvgLatencyMs != 120 ||
		chat.QuotaConsumedTokens != 80 || chat.ProjectID != "project-a" ||
		chat.AssistantID != "assistant-a" || chat.AssistantReleaseID != "release-a" {
		t.Fatalf("unexpected chat attribution: %+v", chat)
	}
	agent := byScenario["agent"]
	if agent.Requests != 1 || agent.Failed != 1 || agent.NoAnswer != 0 || agent.EstimatedCost != 0.008 {
		t.Fatalf("unexpected agent attribution: %+v", agent)
	}

	filtered, err := store.SummarizeOperationsByAttribution(ctx, "tenant-a", false, OperationalReportFilter{
		ProjectID: "project-b", AssistantID: "assistant-b",
		AssistantReleaseID: "release-b", Scenario: "agent",
	})
	if err != nil {
		t.Fatalf("filtered report: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Requests != 1 || filtered[0].Failed != 1 {
		t.Fatalf("unexpected filtered report: %+v", filtered)
	}

	platformRows, err := store.SummarizeOperationsByAttribution(ctx, "tenant-a", true, OperationalReportFilter{})
	if err != nil {
		t.Fatalf("platform report: %v", err)
	}
	if len(platformRows) != 3 {
		t.Fatalf("expected all tenants, got %d: %+v", len(platformRows), platformRows)
	}
}
