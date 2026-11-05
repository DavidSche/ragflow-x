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

func TestTraceSessionMessagesRehydratesCitationsFromSnapshot(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Citation Rehydrate Tenant")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "cite-rehydrate-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "Cite Rehydrate Agent", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := svc.CreateAgentSession(ctx, tenant.ID, agent.ID, "Cite Rehydrate Session", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, err = svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: session.ID, AssistantID: agent.ID,
		PrincipalID: admin.ID, Question: "引用问题", RequestID: "cite-request-1",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", UserMessage: "引用问题", Content: "引用答案",
		Citations: []model.AnswerCitation{
			{ID: "citation-1", Title: "安全法规.doc", ChunkID: "ck-77", DocumentID: "doc-91",
				DatasetID: "ds-1", CitedContentExcerpt: "安全生产费用专款专用。", CitationLocator: "L3"},
		},
	})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}

	items := []ragflow.Message{
		{Role: "user", Content: "引用问题"},
		{Role: "assistant", Content: "引用答案"},
		{Role: "assistant", Content: "无快照回答"},
	}
	traced := svc.traceSessionMessages(ctx, tenant.ID, agent.ID, session.ID, items)
	if traced[1].RequestID != "cite-request-1" {
		t.Fatalf("request ID not matched: %q", traced[1].RequestID)
	}
	if len(traced[1].Citations) != 1 {
		t.Fatalf("citations not rehydrated: got %d", len(traced[1].Citations))
	}
	chunk := traced[1].Citations[0]
	if stringFromAny(chunk["content"]) != "安全生产费用专款专用。" {
		t.Fatalf("content mismatch: %v", chunk["content"])
	}
	if stringFromAny(chunk["document_name"]) != "安全法规.doc" || stringFromAny(chunk["docnm_kwd"]) != "安全法规.doc" {
		t.Fatalf("document name mismatch: %v", chunk)
	}
	if stringFromAny(chunk["chunk_id"]) != "ck-77" || stringFromAny(chunk["document_id"]) != "doc-91" ||
		stringFromAny(chunk["dataset_id"]) != "ds-1" || stringFromAny(chunk["location"]) != "L3" {
		t.Fatalf("citation fields mismatch: %v", chunk)
	}
	if len(traced[2].Citations) != 0 {
		t.Fatalf("unmatched answer must not receive citations, got %d", len(traced[2].Citations))
	}
}

func TestAnswerCitationsFromSnapshotHandlesMalformedAndEmpty(t *testing.T) {
	if got := answerCitationsFromSnapshot(""); got != nil {
		t.Fatalf("empty citations_json must yield nil, got %v", got)
	}
	if got := answerCitationsFromSnapshot("   "); got != nil {
		t.Fatalf("blank citations_json must yield nil, got %v", got)
	}
	if got := answerCitationsFromSnapshot("not-json"); got != nil {
		t.Fatalf("malformed citations_json must yield nil, got %v", got)
	}
	if got := answerCitationsFromSnapshot("[]"); got != nil {
		t.Fatalf("empty array must yield nil, got %v", got)
	}
}
