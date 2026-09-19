package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestListRecentCrossAppSessionsGroupsAndOrdersByTenant(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Recent Sessions")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "recent-operator", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, tenant.ID, "Contract Chat", []string{})
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "Approval Agent", map[string]interface{}{"components": []interface{}{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	searchApp, err := svc.CreateSearchApp(ctx, tenant.ID, "Policy Search", nil)
	if err != nil {
		t.Fatalf("create search app: %v", err)
	}

	now := time.Now().UTC()
	events := []model.KnowledgeOpsEvent{
		{RequestID: "chat-turn-1", TenantID: tenant.ID, UserID: actor.ID, AppType: "chat", AppID: chat.ID,
			SessionID: "session-1", Question: "合同条款", Status: model.KnowledgeOpsCompleted,
			CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
		{RequestID: "chat-turn-2", TenantID: tenant.ID, UserID: actor.ID, AppType: "chat", AppID: chat.ID,
			SessionID: "session-1", Question: "合同期限", Status: model.KnowledgeOpsCompleted,
			CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{RequestID: "agent-turn-1", TenantID: tenant.ID, UserID: actor.ID, AppType: "agent", AppID: agent.ID,
			SessionID: "agent-session-1", Question: "发起审批", Status: model.KnowledgeOpsCompleted,
			CreatedAt: now.Add(-30 * time.Minute), UpdatedAt: now.Add(-30 * time.Minute)},
		{RequestID: "search-turn-1", TenantID: tenant.ID, UserID: actor.ID, AppType: "search", AppID: searchApp.ID,
			Question: "检索政策", Status: model.KnowledgeOpsCompleted,
			CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now.Add(-10 * time.Minute)},
		{RequestID: "other-tenant-turn", TenantID: "other-tenant", UserID: actor.ID, AppType: "chat", AppID: "foreign-chat",
			SessionID: "foreign-session", Question: "外部租户", Status: model.KnowledgeOpsCompleted,
			CreatedAt: now, UpdatedAt: now},
	}
	for index := range events {
		if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, &events[index]); err != nil {
			t.Fatalf("seed event %d: %v", index, err)
		}
	}

	sessions, err := svc.ListRecentCrossAppSessions(ctx, actor.ID, "", 20)
	if err != nil {
		t.Fatalf("list recent sessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("recent session count = %d, want 3: %+v", len(sessions), sessions)
	}
	if sessions[0].AppType != "search" || sessions[0].TargetName != "Policy Search" || sessions[0].ContextID != "search-turn-1" {
		t.Fatalf("unexpected newest search session: %+v", sessions[0])
	}
	if sessions[1].AppType != "agent" || sessions[1].TargetName != "Approval Agent" {
		t.Fatalf("unexpected agent session: %+v", sessions[1])
	}
	if sessions[2].AppType != "chat" || sessions[2].Title != "合同期限" || sessions[2].TurnCount != 2 {
		t.Fatalf("unexpected grouped chat session: %+v", sessions[2])
	}
}

func TestListRecentCrossAppSessionsRejectsMissingActor(t *testing.T) {
	svc := newAuthzSvc(t)
	if _, err := svc.ListRecentCrossAppSessions(context.Background(), "missing-actor", "", 20); err == nil {
		t.Fatal("missing actor must be rejected")
	}
}
