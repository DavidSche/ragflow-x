package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// mixedMemoryRAGFlowStub delegates memory governance to the Mock while serving
// deterministic knowledge chunks for the knowledge_retrieval leg.
type mixedMemoryRAGFlowStub struct {
	*ragflow.Mock
	chunks []map[string]interface{}
}

func (stub *mixedMemoryRAGFlowStub) RetrieveDatasets(
	_ context.Context, _ ragflow.RetrieveDatasetsRequest,
) (*ragflow.RetrieveDatasetsResult, error) {
	return &ragflow.RetrieveDatasetsResult{Chunks: stub.chunks, Total: int64(len(stub.chunks))}, nil
}

func newMixedMemoryAnswerFixture(t *testing.T) (*Service, string, string, *model.MemoryPolicy) {
	t.Helper()
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenantRecord, err := svc.CreateTenant(ctx, "MixedMemoryTenant")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	tenant := tenantRecord.ID
	user := id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "mixed-memory-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	policy, _ := activateSelfMemoryPolicy(t, svc, tenant, user, "assistant-mixed")
	return svc, tenant, user, policy
}

func createMixedMemoryTools(t *testing.T, svc *Service, tenant, user, strategyID string) {
	t.Helper()
	ctx := context.Background()
	knowledgeTool, err := svc.CreateToolRegistry(ctx, tenant, user, ToolRegistryInput{
		ToolID: "knowledge_retrieval", Version: "v1", ToolType: model.ToolTypeKnowledgeRetrieval,
		Name: "Retrieve governed knowledge", InputSchema: knowledgeRetrievalInputSchema(),
		OutputSchema:       knowledgeRetrievalOutputSchema(),
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: "builtin:knowledge_retrieval",
	})
	if err != nil {
		t.Fatalf("create knowledge tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["tenant_admin"]}`, SourceType: model.SourceTypeKnowledge,
		Matcher: `{"intent":["policy_query"]}`, ToolID: knowledgeTool.ToolID, ToolVersion: knowledgeTool.Version,
		PolicyVersion: "knowledge-policy-v1", Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create knowledge rule: %v", err)
	}
	spec := builtinToolSpecs["memory_retrieval"]
	memoryTool, err := svc.CreateToolRegistry(ctx, tenant, user, ToolRegistryInput{
		ToolID: spec.ToolID, Version: "v1", ToolType: spec.ToolType, Name: "Governed memory",
		InputSchema: spec.InputSchema, OutputSchema: spec.OutputSchema,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: spec.ImplementationRef,
	})
	if err != nil {
		t.Fatalf("create memory tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["tenant_admin"]}`, SourceType: model.SourceTypeMemory,
		Matcher: `{"intent":["recall_query"]}`, ToolID: memoryTool.ToolID, ToolVersion: memoryTool.Version,
		PolicyVersion: "memory-policy-v1", Priority: 20, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create memory rule: %v", err)
	}
}

func TestExecuteRoutedMixedToolAnswerSupportsMemoryRetrieval(t *testing.T) {
	ctx := context.Background()
	svc, tenant, user, policy := newMixedMemoryAnswerFixture(t)
	datasetID := createKnowledgeStrategyDataset(t, svc, tenant, id.New())
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, knowledgeStrategyInput(datasetID, model.KnowledgeStrategySemantic))
	if err != nil {
		t.Fatalf("create knowledge strategy: %v", err)
	}
	mockClient := svc.RAGFlow.(*ragflow.Mock)
	svc.RAGFlow = &mixedMemoryRAGFlowStub{Mock: mockClient, chunks: []map[string]interface{}{
		{"id": "chunk-1", "dataset_id": datasetID, "content": knowledgeFactConflict},
	}}
	mockClient.SetNextMemoryMessageContent(`{"stable_fact":{"entity":"prod db","attribute":"max connections","value":"200"}}`)
	if err := svc.RAGFlow.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{policy.RagflowMemoryID}, AgentID: "assistant-mixed", SessionID: "session-mixed",
		UserID: "rgx:" + tenant + ":" + user, UserInput: "database limits", AgentResponse: "config",
	}); err != nil {
		t.Fatalf("add memory message: %v", err)
	}
	createMixedMemoryTools(t, svc, tenant, user, strategy.ID)

	result, err := svc.ExecuteRoutedMixedToolAnswer(ctx, tenant, user, "assistant-mixed", RoutedMixedToolAnswerInput{
		SessionID: "session-mixed", AssistantID: "assistant-mixed", RequestID: "request-mixed-memory-" + id.New(),
		Question: "Which policy applies and what do we remember?",
		SubQueries: []RoutedToolSubqueryInput{
			{Intent: "policy_query", Confidence: 0.9, ToolInput: json.RawMessage(
				`{"strategy_id":"` + strategy.ID + `","question":"Which storage policy applies?"}`,
			)},
			{Intent: "recall_query", Confidence: 0.9, ToolInput: json.RawMessage(
				`{"memory_id":"` + policy.RagflowMemoryID + `","assistant_id":"assistant-mixed","session_id":"session-mixed","question":"database limits"}`,
			)},
		},
	})
	if err != nil {
		t.Fatalf("execute mixed memory answer: %v", err)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered || result.Snapshot.ReasonCode != "MIXED_TOOL_ANSWERED" {
		t.Fatalf("unexpected run: %+v snapshot=%+v", result.Run, result.Snapshot)
	}
	var envelope struct {
		Results []struct {
			Source string          `json:"source"`
			Output json.RawMessage `json:"output"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if len(envelope.Results) != 2 || envelope.Results[0].Source != "knowledge" || envelope.Results[1].Source != "memory" {
		t.Fatalf("unexpected envelope sources: %+v", envelope)
	}
	var memoryOutput map[string]interface{}
	if err := json.Unmarshal(envelope.Results[1].Output, &memoryOutput); err != nil {
		t.Fatal(err)
	}
	// The knowledge leg returned knowledgeFactConflict, whose knowledge_fact
	// contradicts the memory Stable Fact (200 vs 100). The mixed path must have
	// wired the knowledge chunks into the memory leg and downgraded it.
	conflict, ok := memoryOutput["conflict"].(map[string]interface{})
	if !ok {
		t.Fatalf("memory leg output missing conflict summary: %+v", memoryOutput)
	}
	if conflict["downgraded_count"] != float64(1) || conflict["context_policy_version"] != memoryConflictPolicyVersion {
		t.Fatalf("expected deterministic downgrade through mixed path: %+v", conflict)
	}
	messages, ok := memoryOutput["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected memory messages: %+v", memoryOutput["messages"])
	}
	message := messages[0].(map[string]interface{})
	if message["conflict_status"] != MemoryConflictDowngraded {
		t.Fatalf("memory message must be downgraded: %+v", message)
	}
}

func TestExecuteRoutedMixedToolAnswerRejectsMemorySourceWithoutPolicy(t *testing.T) {
	ctx := context.Background()
	svc, tenant, user, policy := newMixedMemoryAnswerFixture(t)
	if err := svc.DeleteMemoryPolicy(ctx, tenant, user, policy.ID); err != nil {
		t.Fatalf("delete policy: %v", err)
	}
	createMixedMemoryTools(t, svc, tenant, user, "")
	if _, err := svc.ExecuteRoutedMixedToolAnswer(ctx, tenant, user, "assistant-mixed", RoutedMixedToolAnswerInput{
		SessionID: "session-mixed", AssistantID: "assistant-mixed", RequestID: "request-mixed-memory-denied-" + id.New(),
		Question: "What do we remember?",
		SubQueries: []RoutedToolSubqueryInput{
			{Intent: "recall_query", Confidence: 0.9, ToolInput: json.RawMessage(
				`{"memory_id":"` + policy.RagflowMemoryID + `","assistant_id":"assistant-mixed","session_id":"session-mixed","question":"database limits"}`,
			)},
		},
	}); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Fatalf("memory source without an active policy must fail closed: err=%v", err)
	}
}

func TestInjectMemoryKnowledgeChunksValidatesAndFills(t *testing.T) {
	wired, err := injectMemoryKnowledgeChunks(json.RawMessage(`{"memory_id":"m1","question":"q"}`), []string{"chunk-a"})
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(wired, &decoded); err != nil {
		t.Fatal(err)
	}
	chunks, ok := decoded["knowledge_chunks"].([]interface{})
	if !ok || len(chunks) != 1 || chunks[0] != "chunk-a" {
		t.Fatalf("knowledge chunks not injected: %+v", decoded)
	}
	if decoded["memory_id"] != "m1" {
		t.Fatalf("existing input fields must be preserved: %+v", decoded)
	}
	if _, err := injectMemoryKnowledgeChunks(json.RawMessage(`not-json`), []string{"chunk-a"}); err == nil {
		t.Fatal("non-object memory input must fail closed")
	}
}

func TestKnowledgeRetrievalChunkBodiesDecodesOutput(t *testing.T) {
	bodies := knowledgeRetrievalChunkBodies(json.RawMessage(`{"chunks":["a","b"],"total":2}`))
	if len(bodies) != 2 || bodies[0] != "a" || bodies[1] != "b" {
		t.Fatalf("unexpected chunk bodies: %+v", bodies)
	}
	if bodies := knowledgeRetrievalChunkBodies(json.RawMessage(`not-json`)); bodies != nil {
		t.Fatalf("invalid output must yield nil: %+v", bodies)
	}
}
