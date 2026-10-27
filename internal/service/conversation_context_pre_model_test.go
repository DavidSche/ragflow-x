package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func chatContextDecision(t *testing.T, decision *model.ContextDecision) map[string]interface{} {
	t.Helper()
	detail := map[string]interface{}{}
	if err := json.Unmarshal([]byte(decision.Detail), &detail); err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestChatAppCompletionPersistsPreModelContextDecision(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	mock := svc.RAGFlow.(*ragflow.Mock)
	chat, err := mock.CreateChat(ctx, ragflow.CreateChatRequest{Name: "Governed Chat"})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, chat.ID)
	session, err := mock.CreateChatSession(ctx, chat.ID, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{
		ID: chat.ID, TenantID: tenant.ID, Name: "Governed Chat",
		Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mock.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{policy.RagflowMemoryID}, AgentID: chat.ID, SessionID: session.ID,
		UserID:    "rgx:" + tenant.ID + ":" + user.ID,
		UserInput: "preferred deployment", AgentResponse: "on-premise",
	}); err != nil {
		t.Fatal(err)
	}
	key := &model.APIKey{ID: id.New(), TenantID: tenant.ID, UserID: user.ID}
	if _, err := svc.ChatAppCompletion(ctx, key, chat.ID, session.ID,
		[]byte(`{"messages":[{"role":"user","content":"deployment"}]}`), "request-pre-model-1"); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-pre-model-1")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "memory_allowed" || decision.MemoryPolicyID != policy.ID {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	detail := chatContextDecision(t, decision)
	generation, _ := detail["policy_generation"].(float64)
	if detail["reason"] != "" || detail["memory_retrieved"] != true || int64(generation) != policy.PolicyGeneration {
		t.Fatalf("unexpected decision detail: %+v", detail)
	}
	if count, _ := detail["memory_result_count"].(float64); int64(count) != 1 {
		t.Fatalf("memory result count missing: %+v", detail)
	}
	params := mock.LastMemorySearchParams()
	if params.Query != "deployment" || params.AgentID != chat.ID ||
		params.SessionID != session.ID || params.UserID != "rgx:"+tenant.ID+":"+user.ID {
		t.Fatalf("governed memory filters missing: %+v", params)
	}
	sentMessages := mock.LastChatCompletionMessages()
	if len(sentMessages) != 2 || sentMessages[0].Role != "system" ||
		!strings.Contains(sentMessages[0].Content, "on-premise") {
		t.Fatalf("governed memory was not injected: %+v", sentMessages)
	}
}

func TestPreModelContextPolicyMismatchFailsClosed(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	policy, _ := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-policy-change")
	input := ConversationContextAuthorizationInput{
		ActorID: user.ID, RequestID: "request-policy-change", RequestKind: model.ContextDecisionChat,
		TargetKind: "chat", AssistantID: "assistant-policy-change", SessionID: "session-1", Query: "same question",
	}
	ctx := context.Background()
	first, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input)
	if err != nil || first.Decision != "memory_allowed" {
		t.Fatalf("initial authorization: decision=%+v err=%v", first, err)
	}
	if _, err := svc.SetMemoryPolicyConsent(ctx, tenant.ID, user.ID, policy.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input); err == nil {
		t.Fatal("policy change must invalidate the allowed decision")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40960 {
		t.Fatalf("expected policy conflict, got %v", err)
	}
}

func TestStreamChatAppCompletionInjectsGovernedMemoryContext(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	mock := svc.RAGFlow.(*ragflow.Mock)
	chat, err := mock.CreateChat(ctx, ragflow.CreateChatRequest{Name: "Governed Stream Chat"})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, chat.ID)
	session, err := mock.CreateChatSession(ctx, chat.ID, "stream-session-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{
		ID: chat.ID, TenantID: tenant.ID, Name: "Governed Stream Chat",
		Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mock.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{policy.RagflowMemoryID}, AgentID: chat.ID, SessionID: session.ID,
		UserID:    "rgx:" + tenant.ID + ":" + user.ID,
		UserInput: "preferred deployment", AgentResponse: "on-premise",
	}); err != nil {
		t.Fatal(err)
	}
	key := &model.APIKey{ID: id.New(), TenantID: tenant.ID, UserID: user.ID}
	raw := []byte(`{"messages":[{"role":"user","content":"deployment"}]}`)
	output := &strings.Builder{}
	if _, _, err := svc.StreamChatAppCompletion(ctx, key, chat.ID, session.ID, raw, output, "request-stream-pre-model-1"); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-stream-pre-model-1")
	if err != nil {
		t.Fatal(err)
	}
	detail := chatContextDecision(t, decision)
	if detail["memory_retrieved"] != true || detail["memory_result_count"] != float64(1) {
		t.Fatalf("stream retrieval evidence missing: %+v", detail)
	}
	params := mock.LastMemorySearchParams()
	if params.AgentID != chat.ID || params.SessionID != session.ID ||
		params.UserID != "rgx:"+tenant.ID+":"+user.ID {
		t.Fatalf("stream governed memory filters missing: %+v", params)
	}
	sentMessages := mock.LastChatCompletionMessages()
	if len(sentMessages) != 2 || sentMessages[0].Role != "system" ||
		!strings.Contains(sentMessages[0].Content, "on-premise") {
		t.Fatalf("stream governed memory was not injected: %+v", sentMessages)
	}
}

func TestPreModelContextRequestConflictDoesNotCallRAGFlow(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	input := ConversationContextAuthorizationInput{
		ActorID: user.ID, RequestID: "request-conflict", RequestKind: model.ContextDecisionChat,
		TargetKind: "chat", AssistantID: "assistant-conflict", SessionID: "session-1", Query: "first question",
	}
	ctx := context.Background()
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input); err != nil {
		t.Fatal(err)
	}
	input.Query = "different question"
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input); err == nil {
		t.Fatal("same request id with a different question must conflict")
	}
	usage, err := svc.Store.SummarizeUsage(ctx, tenant.ID, "")
	if err != nil || len(usage) != 0 {
		t.Fatalf("conflict must not reach RAGFlow: usage=%+v err=%v", usage, err)
	}
}

func TestPreModelContextTargetKindConflictDoesNotCallRAGFlow(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	input := ConversationContextAuthorizationInput{
		ActorID: user.ID, RequestID: "request-target-conflict", RequestKind: model.ContextDecisionChat,
		TargetKind: "chat", AssistantID: "assistant-target-conflict", SessionID: "session-1", Query: "same question",
	}
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input); err != nil {
		t.Fatal(err)
	}
	input.TargetKind = "agent"
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input); err == nil {
		t.Fatal("same request id with a different target kind must conflict")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40959 {
		t.Fatalf("expected request conflict, got %v", err)
	}
}

func TestPreModelContextVerifiesReleaseManifest(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-release-context")
	ctx := context.Background()
	now := time.Now().UTC()
	manifest := &model.SnapshotManifest{
		ID: id.New(), TenantID: tenant.ID, AssistantReleaseID: "release-context",
		SchemaVersion: "v1", AssistantVersionID: "version-1", ManifestJSON: "{}",
		SnapshotHash: "manifest-hash", Complete: true, CreatedAt: now,
	}
	if err := svc.Store.CreateSnapshotManifest(ctx, manifest); err != nil {
		t.Fatal(err)
	}
	release := &model.AssistantRelease{
		ID: "release-context", TenantID: tenant.ID, AssistantID: "assistant-release-context",
		AssistantVersionID: "version-1", ReleaseVersion: 1, ReleaseState: model.ReleaseActive,
		DesiredStateJSON: "{}", DesiredStateHash: "desired-hash", ActualStateJSON: "{}",
		ActualStateHash: "desired-hash", ReconcileStatus: model.ReconcileConsistent,
		SnapshotManifestID: manifest.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateAssistantRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateAssistant(ctx, &model.Assistant{
		ID: "assistant-release-context", TenantID: tenant.ID, ProjectID: "project-1",
		Name: "Governed assistant", OwnerID: user.ID, LifecycleStatus: "ENABLED",
		CurrentAssistantReleaseID: release.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	input := ConversationContextAuthorizationInput{
		ActorID: user.ID, RequestID: "request-release-1", RequestKind: model.ContextDecisionChat,
		TargetKind: "chat", AssistantID: "assistant-release-context", SessionID: "session-1", Query: "release question",
	}
	first, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input)
	if err != nil || first.Decision != "memory_allowed" {
		t.Fatalf("initial release authorization: decision=%+v err=%v", first, err)
	}
	detail := chatContextDecision(t, first)
	if detail["release_manifest_status"] != "verified" || detail["release_manifest_hash"] != manifest.SnapshotHash {
		t.Fatalf("release manifest was not snapshotted: %+v", detail)
	}
	if err := svc.Store.ReconcileAssistantRelease(ctx, &model.AssistantRelease{
		ID: release.ID, TenantID: tenant.ID, ActualStateHash: "actual-hash", ReconcileStatus: model.ReconcileDrifted,
		ActualObservedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, input); err == nil {
		t.Fatal("drifted release manifest must invalidate reuse")
	}
}

func TestContextDecisionLookupRequiresOwnerOrGovernance(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	other, err := svc.CreateUser(context.Background(), tenant.ID, model.RoleBusinessUser, CreateUserRequest{
		Username: "other-user", Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.AuthorizeConversationContext(ctx, tenant.ID, user.ID, ConversationContextAuthorizationInput{
		ActorID: user.ID, RequestID: "request-owner-only", RequestKind: model.ContextDecisionChat,
		TargetKind: "chat", AssistantID: "assistant-owner-only", SessionID: "session-1", Query: "private question",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, other.ID, "request-owner-only"); err == nil {
		t.Fatal("non-owner without governance permission must not read the decision")
	}
	if _, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-owner-only"); err != nil {
		t.Fatalf("owner lookup failed: %v", err)
	}
	otherTenant, err := svc.CreateTenant(ctx, "Other Tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetConversationContextDecisionByRequest(ctx, otherTenant.ID, user.ID, "request-owner-only"); err == nil {
		t.Fatal("cross-tenant decision lookup must not be visible")
	}
}

func TestSearchAppCompletionPersistsPreModelContextDecision(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "context-kb")
	if err != nil {
		t.Fatal(err)
	}
	searchApp, err := svc.CreateSearchApp(ctx, tenant.ID, "context-search", &ragflow.SearchConfig{
		KbIDs: []string{dataset.RAGFlowDatasetID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SearchAppCompletion(ctx, tenant.ID, user.ID, searchApp.ID, "context question", "request-search-context", false); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-search-context")
	if err != nil {
		t.Fatal(err)
	}
	detail := chatContextDecision(t, decision)
	if decision.RequestKind != model.ContextDecisionSearch || decision.Decision != "memory_denied" ||
		detail["target_kind"] != "search" || detail["reason"] != "no_active_policy" {
		t.Fatalf("unexpected search decision: %+v detail=%+v", decision, detail)
	}
	var output bytes.Buffer
	if err := svc.StreamSearchAppCompletion(ctx, tenant.ID, user.ID, searchApp.ID,
		"stream question", "request-search-stream", false, &output); err != nil {
		t.Fatal(err)
	}
	streamDecision, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-search-stream")
	if err != nil {
		t.Fatal(err)
	}
	streamDetail := chatContextDecision(t, streamDecision)
	if streamDecision.RequestKind != model.ContextDecisionSearch || streamDetail["target_kind"] != "search" {
		t.Fatalf("unexpected search stream decision: %+v detail=%+v", streamDecision, streamDetail)
	}
}

func TestAgentCompletionPersistsPreModelContextDecision(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	ctx := context.Background()
	mock := svc.RAGFlow.(*ragflow.Mock)
	agent, err := svc.CreateAgent(ctx, tenant.ID, "context-agent", map[string]interface{}{"x": 1}, true, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	messages := []ragflow.Message{{Role: "user", Content: "context question"}}
	if _, err := svc.AgentChatCompletion(ctx, tenant.ID, agent.ID, user.ID, messages, nil, "request-agent-context", "agent-session-1"); err != nil {
		t.Fatal(err)
	}
	decision, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-agent-context")
	if err != nil {
		t.Fatal(err)
	}
	detail := chatContextDecision(t, decision)
	if decision.RequestKind != model.ContextDecisionChat || decision.Decision != "memory_denied" ||
		detail["target_kind"] != "agent" || detail["reason"] != "no_active_policy" {
		t.Fatalf("unexpected agent decision: %+v detail=%+v", decision, detail)
	}
	policy, _ := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, agent.ID)
	if err := mock.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{policy.RagflowMemoryID}, AgentID: agent.ID, SessionID: "agent-session-1",
		UserID:    "rgx:" + tenant.ID + ":" + user.ID,
		UserInput: "preferred deployment", AgentResponse: "on-premise",
	}); err != nil {
		t.Fatal(err)
	}
	messages = []ragflow.Message{{Role: "user", Content: "deployment"}}
	if _, err := svc.AgentChatCompletion(ctx, tenant.ID, agent.ID, user.ID, messages, nil, "request-agent-governed", "agent-session-1"); err != nil {
		t.Fatal(err)
	}
	decision, err = svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-agent-governed")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "memory_allowed" || decision.MemoryPolicyID != policy.ID {
		t.Fatalf("unexpected governed agent decision: %+v", decision)
	}
	detail = chatContextDecision(t, decision)
	if detail["memory_retrieved"] != true || detail["memory_result_count"] != float64(1) {
		t.Fatalf("agent memory retrieval evidence missing: %+v", detail)
	}
	sentMessages := mock.LastAgentCompletionMessages()
	if len(sentMessages) != 2 || sentMessages[0].Role != "system" ||
		!strings.Contains(sentMessages[0].Content, "on-premise") {
		t.Fatalf("agent governed memory was not injected: %+v", sentMessages)
	}
	if err := svc.StreamAgentChatCompletion(ctx, tenant.ID, agent.ID, user.ID, messages, nil, "request-agent-stream", "agent-session-1", io.Discard); err != nil {
		t.Fatal(err)
	}
	streamDecision, err := svc.GetConversationContextDecisionByRequest(ctx, tenant.ID, user.ID, "request-agent-stream")
	if err != nil {
		t.Fatal(err)
	}
	streamDetail := chatContextDecision(t, streamDecision)
	if streamDecision.RequestKind != model.ContextDecisionChat || streamDetail["target_kind"] != "agent" {
		t.Fatalf("unexpected agent stream decision: %+v detail=%+v", streamDecision, streamDetail)
	}
	if streamDetail["memory_retrieved"] != true || streamDetail["memory_result_count"] != float64(1) {
		t.Fatalf("agent stream retrieval evidence missing: %+v", streamDetail)
	}
	streamMessages := mock.LastAgentCompletionMessages()
	if len(streamMessages) != 2 || streamMessages[0].Role != "system" ||
		!strings.Contains(streamMessages[0].Content, "on-premise") {
		t.Fatalf("agent stream governed memory was not injected: %+v", streamMessages)
	}
}
