package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestTraceSessionMessagesMapsServerSnapshotRequestIDs(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Snapshot Trace Tenant")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "snapshot-trace-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "Snapshot Trace Agent", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := svc.CreateAgentSession(ctx, tenant.ID, agent.ID, "Snapshot Trace Session", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	finalize := func(question, answer, requestID string) {
		t.Helper()
		_, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
			TenantID: tenant.ID, SessionID: session.ID, AssistantID: agent.ID,
			PrincipalID: admin.ID, Question: question, RequestID: requestID,
			AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
			ReasonCode: "TEST_ANSWERED", UserMessage: question, Content: answer,
		})
		if err != nil {
			t.Fatalf("finalize %s: %v", requestID, err)
		}
	}
	finalize("first question", "same answer", "trace-request-1")
	finalize("second question", "same answer", "trace-request-2")

	items := []ragflow.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "same answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "same answer"},
		{Role: "assistant", Content: "unserviced answer"},
	}
	traced := svc.traceSessionMessages(ctx, tenant.ID, agent.ID, session.ID, items)
	if traced[1].RequestID != "trace-request-1" || traced[3].RequestID != "trace-request-2" {
		t.Fatalf("request IDs mismatched: got %q and %q", traced[1].RequestID, traced[3].RequestID)
	}
	if traced[4].RequestID != "" || traced[0].RequestID != "" || traced[2].RequestID != "" {
		t.Fatal("unmatched or user messages must not receive a request ID")
	}

	crossTenant := svc.traceSessionMessages(ctx, "other-tenant", agent.ID, session.ID, items)
	if crossTenant[1].RequestID != "" || crossTenant[3].RequestID != "" {
		t.Fatal("cross-tenant lookup leaked server snapshot request IDs")
	}
}
