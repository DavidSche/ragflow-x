package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type plannerAgentClient struct {
	ragflow.Mock
	request   *ragflow.CompletionRequest
	responses []string
	response  string
}

func (client *plannerAgentClient) AgentChatCompletion(
	_ context.Context, request ragflow.CompletionRequest,
) (*ragflow.CompletionResponse, error) {
	client.request = &request
	content := client.response
	if len(client.responses) > 0 {
		content = client.responses[0]
		client.responses = client.responses[1:]
	}
	return &ragflow.CompletionResponse{
		Choices: []ragflow.CompletionChoice{{Message: ragflow.Message{Content: content}}},
	}, nil
}

func TestExecuteRoutedSQLMultiSubqueryAnswer(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	svc := fixture.service
	tenant, user := fixture.tenantID, fixture.adminUserID
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(_ context.Context, connection *model.DBConnection) (string, error) {
		if connection.TenantID != tenant {
			t.Fatalf("resolver got unexpected connection tenant %s", connection.TenantID)
		}
		return "file:" + fixture.targetDSN, nil
	})
	_ = fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
	requestID := "request-multi-subquery-" + id.New()
	result, err := svc.ExecuteRoutedSQLMultiSubqueryAnswer(ctx, tenant, user, "assistant-multi", RoutedSQLMultiSubqueryAnswerInput{
		SessionID: "session-multi", AssistantID: "assistant-multi", RequestID: requestID,
		Question: "What are the exposures?", SubQueries: []RoutedSQLSubqueryInput{
			{Intent: "risk_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"tenant_id":"tenant-a"}`)},
			{Intent: "risk_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"tenant_id":"tenant-b"}`)},
		},
	})
	if err != nil {
		t.Fatalf("execute multi-subquery sql answer: %v", err)
	}
	if result.Run.RequestID != requestID || result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.AnswerStatus != model.AnswerStatusAnswered {
		t.Fatalf("unexpected answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
	if len(result.ToolExecutions) != 2 {
		t.Fatalf("expected two tool executions, got %+v", result.ToolExecutions)
	}
	var envelope struct {
		Schema  string `json:"schema"`
		Results []struct {
			SubQueryIndex int `json:"sub_query_index"`
			Output        struct {
				Rows []struct {
					TenantID string  `json:"tenant_id"`
					Exposure float64 `json:"exposure"`
				} `json:"rows"`
			} `json:"output"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &envelope); err != nil {
		t.Fatalf("decode answer content: %v", err)
	}
	if envelope.Schema != "routed_sql_multi_subquery_v1" || len(envelope.Results) != 2 {
		t.Fatalf("unexpected answer envelope: %+v", envelope)
	}
	if envelope.Results[0].SubQueryIndex != 0 || envelope.Results[0].Output.Rows[0].Exposure != 10.5 ||
		envelope.Results[1].SubQueryIndex != 1 || envelope.Results[1].Output.Rows[0].Exposure != 12.5 {
		t.Fatalf("unexpected subquery results: %+v", envelope.Results)
	}
	var execution []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Snapshot.ExecutionJSON), &execution); err != nil {
		t.Fatalf("decode execution: %v", err)
	}
	if len(execution) != 2 || execution[0]["source_routing_rule_id"] != fixture.sourceRuleID ||
		execution[1]["source_routing_rule_id"] != fixture.sourceRuleID {
		t.Fatalf("unexpected execution trace: %+v", execution)
	}
	if strings.Contains(result.Snapshot.ExecutionJSON, fixture.targetDSN) {
		t.Fatal("multi-subquery answer must not leak database DSN")
	}
}

func TestExecuteRoutedSQLMultiSubqueryAnswerFailsClosed(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	_ = fixture.createTool(t, fixture.tenantID, fixture.adminUserID, "query-template:"+fixture.templateID)
	fixture.service.SecretResolver = DBConnectionSecretResolverFunc(func(_ context.Context, _ *model.DBConnection) (string, error) {
		return "file:" + fixture.targetDSN, nil
	})
	requestID := "request-multi-subquery-deny-" + id.New()
	_, err := fixture.service.ExecuteRoutedSQLMultiSubqueryAnswer(ctx, fixture.tenantID, fixture.adminUserID, "assistant-multi", RoutedSQLMultiSubqueryAnswerInput{
		SessionID: "session-multi", AssistantID: "assistant-multi", RequestID: requestID,
		Question: "What are the exposures?", SubQueries: []RoutedSQLSubqueryInput{
			{Intent: "risk_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"tenant_id":"tenant-a"}`)},
			{Intent: "unknown_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"tenant_id":"tenant-b"}`)},
		},
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != 404 {
		t.Fatalf("expected route miss, got %#v", err)
	}
	run, err := fixture.service.Store.GetAnswerRunByRequest(ctx, fixture.tenantID, requestID)
	if err != nil || run == nil {
		t.Fatalf("failed answer run was not recorded: %v", err)
	}
	if run.AnswerStatus != model.AnswerStatusSystemFailed || run.CompletionReason != model.CompletionReasonSystemFailed {
		t.Fatalf("unexpected failed answer run: %+v", run)
	}
}

func TestExecuteRoutedSQLMultiSubqueryAnswerPartial(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 1)
	targetDB, err := sql.Open("sqlite", fixture.targetDSN)
	if err != nil {
		t.Fatalf("open target database: %v", err)
	}
	defer targetDB.Close()
	if _, err := targetDB.Exec(
		`INSERT INTO risk_exposure (tenant_id, exposure) VALUES (?, ?)`, "tenant-a", 11.5,
	); err != nil {
		t.Fatalf("seed second matching row: %v", err)
	}
	fixture.service.SecretResolver = DBConnectionSecretResolverFunc(func(_ context.Context, _ *model.DBConnection) (string, error) {
		return "file:" + fixture.targetDSN, nil
	})
	_ = fixture.createTool(t, fixture.tenantID, fixture.adminUserID, "query-template:"+fixture.templateID)
	result, err := fixture.service.ExecuteRoutedSQLMultiSubqueryAnswer(
		ctx, fixture.tenantID, fixture.adminUserID, "assistant-multi",
		RoutedSQLMultiSubqueryAnswerInput{
			SessionID: "session-multi-partial", AssistantID: "assistant-multi",
			RequestID: "request-multi-subquery-partial", Question: "What are the exposures?",
			SubQueries: []RoutedSQLSubqueryInput{
				{Intent: "risk_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"tenant_id":"tenant-a"}`)},
			},
		},
	)
	if err != nil {
		t.Fatalf("execute truncated multi-subquery sql answer: %v", err)
	}
	if result.Run.AnswerStatus != model.AnswerStatusPartial ||
		result.Snapshot.ReasonCode != "SQL_TOOL_ANSWERED_PARTIAL" ||
		result.Snapshot.AnswerStatus != model.AnswerStatusPartial {
		t.Fatalf("unexpected partial answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
}

func TestExecuteRoutedMixedToolAnswer(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLQueryFixture(t, 10)
	svc := fixture.service
	tenant, user := fixture.tenantID, fixture.adminUserID
	svc.SecretResolver = DBConnectionSecretResolverFunc(func(_ context.Context, _ *model.DBConnection) (string, error) {
		return "file:" + fixture.targetDSN, nil
	})
	_ = fixture.createTool(t, tenant, user, "query-template:"+fixture.templateID)
	calculation, err := svc.CreateToolRegistry(ctx, tenant, user, ToolRegistryInput{
		ToolID: "add", Version: "v1", ToolType: model.ToolTypeCalculation, Name: "Add numbers",
		InputSchema:        `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		OutputSchema:       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: "builtin:add", Active: nil,
	})
	if err != nil {
		t.Fatalf("create calculation tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`, SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["sum_query"]}`, ToolID: calculation.ToolID, ToolVersion: calculation.Version,
		PolicyVersion: "calculation-policy-v1", Priority: 20, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create calculation rule: %v", err)
	}
	requestID := "request-mixed-tool-" + id.New()
	result, err := svc.ExecuteRoutedMixedToolAnswer(ctx, tenant, user, "assistant-mixed", RoutedMixedToolAnswerInput{
		SessionID: "session-mixed", AssistantID: "assistant-mixed", RequestID: requestID,
		Question: "What is the exposure and its add calculation?", SubQueries: []RoutedToolSubqueryInput{
			{Intent: "risk_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"tenant_id":"tenant-a"}`)},
			{Intent: "sum_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"a":2,"b":3}`)},
		},
	})
	if err != nil {
		t.Fatalf("execute mixed tool answer: %v", err)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.ReasonCode != "MIXED_TOOL_ANSWERED" {
		t.Fatalf("unexpected answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
	if len(result.ToolExecutions) != 2 || result.ToolExecutions[0].Tool.ToolType != model.ToolTypeSQLQuery ||
		result.ToolExecutions[1].Tool.ToolType != model.ToolTypeCalculation {
		t.Fatalf("unexpected tool executions: %+v", result.ToolExecutions)
	}
	var envelope struct {
		Schema  string `json:"schema"`
		Results []struct {
			SubQueryIndex int            `json:"sub_query_index"`
			Source        string         `json:"source"`
			Output        map[string]any `json:"output"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &envelope); err != nil {
		t.Fatalf("decode mixed answer content: %v", err)
	}
	if envelope.Schema != "routed_mixed_tool_v1" || len(envelope.Results) != 2 ||
		envelope.Results[0].Source != "db" || envelope.Results[1].Source != "tool" ||
		envelope.Results[1].Output["result"] != 5.0 {
		t.Fatalf("unexpected mixed answer envelope: %+v", envelope)
	}
	var execution []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Snapshot.ExecutionJSON), &execution); err != nil {
		t.Fatalf("decode execution: %v", err)
	}
	if len(execution) != 2 || execution[0]["source"] != "db" || execution[1]["source"] != "tool" ||
		execution[1]["authorization_trace"] == nil {
		t.Fatalf("unexpected execution metadata: %+v", execution)
	}
}

func TestExecuteRoutedMixedToolAnswerSupportsFactLookup(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "mixed-fact-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	factTool, err := svc.CreateToolRegistry(ctx, tenant, user, factLookupToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create fact lookup tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["tenant_admin"]}`, SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["fact_query"]}`, ToolID: factTool.ToolID, ToolVersion: factTool.Version,
		PolicyVersion: "lookup-policy-v1", Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create lookup rule: %v", err)
	}
	calculation, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create calculation tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["tenant_admin"]}`, SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["sum_query"]}`, ToolID: calculation.ToolID, ToolVersion: calculation.Version,
		PolicyVersion: "calculation-policy-v1", Priority: 20, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create calculation rule: %v", err)
	}
	requestID := "request-mixed-fact-" + id.New()
	now := time.Now().UTC()
	run := &model.AnswerRun{
		ID: id.New(), TenantID: tenant, SessionID: "session-mixed", AssistantID: "assistant-mixed",
		QuestionRef: "question", RequestID: requestID, LifecycleState: model.AnswerLifecycleInit,
		AnswerStatus: model.AnswerStatusNoAnswer, CompletionReason: model.CompletionReasonNormal,
		CreatedAt: now,
	}
	if err := svc.Store.CreateAnswerRun(ctx, run); err != nil {
		t.Fatalf("create answer run: %v", err)
	}
	fact := &model.FactRegistry{
		ID: id.New(), TenantID: tenant, AnswerRunID: run.ID, FactKey: "revenue",
		Value: "10,000,000", Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-1",
		ClaimType: model.FactClaimTypeExtracted, ConflictStatus: model.FactConflictNone, CreatedAt: now,
	}
	if err := svc.Store.CreateFactRegistry(ctx, fact); err != nil {
		t.Fatalf("create fact: %v", err)
	}

	result, err := svc.ExecuteRoutedMixedToolAnswer(ctx, tenant, user, "assistant-mixed", RoutedMixedToolAnswerInput{
		SessionID: "session-mixed", AssistantID: "assistant-mixed", RequestID: requestID,
		Question: "What is the fact and its calculation?", SubQueries: []RoutedToolSubqueryInput{
			{Intent: "fact_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"fact_key":"revenue"}`)},
			{Intent: "sum_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"a":2,"b":3}`)},
		},
	})
	if err != nil {
		t.Fatalf("execute mixed fact answer: %v", err)
	}
	if result.Run.ID != run.ID || result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Run.LifecycleState != model.AnswerLifecycleCompleted {
		t.Fatalf("unexpected answer run: %+v", result.Run)
	}
	if len(result.ToolExecutions) != 2 ||
		result.ToolExecutions[0].Tool.ToolType != model.ToolTypeLookup ||
		result.ToolExecutions[1].Tool.ToolType != model.ToolTypeCalculation {
		t.Fatalf("unexpected tool executions: %+v", result.ToolExecutions)
	}
	var envelope struct {
		Results []struct {
			Source string         `json:"source"`
			Output map[string]any `json:"output"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &envelope); err != nil {
		t.Fatalf("decode mixed answer content: %v", err)
	}
	if len(envelope.Results) != 2 || envelope.Results[0].Source != "tool" ||
		envelope.Results[0].Output["value"] != "10,000,000" || envelope.Results[0].Output["unit"] != "USD" ||
		envelope.Results[1].Output["result"] != 5.0 {
		t.Fatalf("unexpected mixed fact envelope: %+v", envelope)
	}
}

func TestExecuteRoutedMixedToolAnswerSupportsKnowledgeRetrieval(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user, project := id.New(), id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "mixed-knowledge-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	datasetID := createKnowledgeStrategyDataset(t, svc, tenant, project)
	strategy, err := svc.CreateKnowledgeStrategy(ctx, tenant, user, knowledgeStrategyInput(datasetID, model.KnowledgeStrategySemantic))
	if err != nil {
		t.Fatalf("create knowledge strategy: %v", err)
	}
	svc.RAGFlow = knowledgeProbeStub{chunks: []map[string]interface{}{
		{"id": "unsafe-id", "dataset_id": "unsafe-dataset", "content": "Policy allows encrypted storage."},
	}}

	knowledgeTool, err := svc.CreateToolRegistry(ctx, tenant, user, ToolRegistryInput{
		ToolID: "knowledge_retrieval", Version: "v1", ToolType: model.ToolTypeKnowledgeRetrieval,
		Name:               "Retrieve governed knowledge",
		InputSchema:        knowledgeRetrievalInputSchema(),
		OutputSchema:       knowledgeRetrievalOutputSchema(),
		AuthorizationScope: `{"roles":["tenant_admin"]}`,
		ImplementationRef:  "builtin:knowledge_retrieval", Active: nil,
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
	calculation, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create calculation tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["tenant_admin"]}`, SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["sum_query"]}`, ToolID: calculation.ToolID, ToolVersion: calculation.Version,
		PolicyVersion: "calculation-policy-v1", Priority: 20, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create calculation rule: %v", err)
	}

	requestID := "request-mixed-knowledge-" + id.New()
	result, err := svc.ExecuteRoutedMixedToolAnswer(ctx, tenant, user, "assistant-mixed", RoutedMixedToolAnswerInput{
		SessionID: "session-mixed", AssistantID: "assistant-mixed", RequestID: requestID,
		Question: "What policy applies and what is the sum?",
		SubQueries: []RoutedToolSubqueryInput{
			{Intent: "policy_query", Confidence: 0.9, ToolInput: json.RawMessage(
				`{"strategy_id":"` + strategy.ID + `","question":"Which storage policy applies?"}`,
			)},
			{Intent: "sum_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"a":2,"b":3}`)},
		},
	})
	if err != nil {
		t.Fatalf("execute mixed knowledge answer: %v", err)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.ReasonCode != "MIXED_TOOL_ANSWERED" ||
		result.ToolExecutions[0].Tool.ToolType != model.ToolTypeKnowledgeRetrieval ||
		result.ToolExecutions[1].Tool.ToolType != model.ToolTypeCalculation {
		t.Fatalf("unexpected mixed knowledge answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
	var envelope struct {
		Results []struct {
			Source string         `json:"source"`
			Output map[string]any `json:"output"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &envelope); err != nil {
		t.Fatalf("decode mixed answer content: %v", err)
	}
	if len(envelope.Results) != 2 || envelope.Results[0].Source != "knowledge" ||
		envelope.Results[1].Source != "tool" ||
		envelope.Results[0].Output["total"] != float64(1) ||
		envelope.Results[0].Output["used_strategy_type"] != model.KnowledgeStrategyHybrid ||
		envelope.Results[0].Output["fallback_used"] != true ||
		len(envelope.Results[0].Output["chunks"].([]any)) != 1 ||
		envelope.Results[0].Output["chunks"].([]any)[0] != "Policy allows encrypted storage." ||
		envelope.Results[0].Output["dataset_id"] != nil ||
		envelope.Results[0].Output["id"] != nil ||
		envelope.Results[1].Output["result"] != 5.0 {
		t.Fatalf("unexpected mixed knowledge envelope: %+v", envelope)
	}
}

func TestExecuteRoutedMixedToolAnswerRejectsUnsupportedSource(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, factLookupToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create fact lookup tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["tenant_admin"]}`, SourceType: model.SourceTypeDB,
		Matcher: `{"intent":["fact_query"]}`, ToolID: tool.ToolID, ToolVersion: tool.Version,
		PolicyVersion: "lookup-policy-v1", Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create lookup rule: %v", err)
	}
	requestID := "request-mixed-lookup-" + id.New()
	_, err = svc.ExecuteRoutedMixedToolAnswer(ctx, tenant, user, "assistant-mixed", RoutedMixedToolAnswerInput{
		SessionID: "session-mixed", AssistantID: "assistant-mixed", RequestID: requestID,
		Question: "What is the fact?", SubQueries: []RoutedToolSubqueryInput{
			{Intent: "fact_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"fact_key":"revenue"}`)},
		},
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusBadRequest {
		t.Fatalf("expected unsupported source 400, got %#v", err)
	}
	run, runErr := svc.Store.GetAnswerRunByRequest(ctx, tenant, requestID)
	if runErr != nil || run == nil || run.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("failed answer run was not recorded: run=%+v err=%v", run, runErr)
	}
}

func TestPlanRoutedMixedToolAnswer(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "planner-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant, "Planner "+id.New(), map[string]interface{}{"steps": []string{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AssistantID: agent.ID, AuthorizationScope: `{"roles":["tenant_admin"]}`,
		SourceType: model.SourceTypeTool, Matcher: `{"intent":["sum_query"]}`,
		ToolID: tool.ToolID, ToolVersion: tool.Version, PolicyVersion: "calculation-policy-v1",
		Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	planner := &plannerAgentClient{response: "```json\n" + `{
		"sub_queries":[
			{"intent":"sum_query","confidence":0.9,"tool_input":{"a":2,"b":3}},
			{"intent":"sum_query","confidence":0.8,"tool_input":{"a":4,"b":5}}
		]
	}` + "\n```"}
	svc.RAGFlow = planner

	requestID := "request-planned-" + id.New()
	result, err := svc.PlanRoutedMixedToolAnswer(ctx, tenant, user, agent.ID, RoutedPlannedToolAnswerInput{
		SessionID: "session-planned", AssistantID: agent.ID, RequestID: requestID,
		Question: "What are the two sums?",
	})
	if err != nil {
		t.Fatalf("plan mixed answer: %v", err)
	}
	if planner.request == nil || planner.request.ChatID != agent.ID ||
		len(planner.request.Messages) != 2 ||
		planner.request.Messages[len(planner.request.Messages)-1].Content != "What are the two sums?" ||
		!strings.Contains(planner.request.Messages[0].Content, "tool_id") {
		t.Fatalf("unexpected planner request: %+v", planner.request)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.ReasonCode != "MIXED_TOOL_ANSWERED" || len(result.ToolExecutions) != 2 {
		t.Fatalf("unexpected planned answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
	var envelope struct {
		Results []struct {
			SubQueryIndex int            `json:"sub_query_index"`
			Output        map[string]any `json:"output"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &envelope); err != nil {
		t.Fatalf("decode planned answer: %v", err)
	}
	if len(envelope.Results) != 2 || envelope.Results[0].Output["result"] != 5.0 ||
		envelope.Results[1].Output["result"] != 9.0 {
		t.Fatalf("unexpected planned envelope: %+v", envelope)
	}
}

func TestPlanRoutedMixedToolAnswerFailsClosedOnInvalidPlan(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "invalid-planner-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant, "Invalid Planner "+id.New(), map[string]interface{}{"steps": []string{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AssistantID: agent.ID, AuthorizationScope: `{"roles":["tenant_admin"]}`,
		SourceType: model.SourceTypeTool, Matcher: `{"intent":["sum_query"]}`,
		ToolID: tool.ToolID, ToolVersion: tool.Version, PolicyVersion: "calculation-policy-v1",
		Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	svc.RAGFlow = &plannerAgentClient{response: "I cannot return valid JSON"}
	requestID := "request-invalid-planned-" + id.New()
	_, err = svc.PlanRoutedMixedToolAnswer(ctx, tenant, user, agent.ID, RoutedPlannedToolAnswerInput{
		SessionID: "session-invalid-planned", AssistantID: agent.ID, RequestID: requestID,
		Question: "What is the sum?",
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusBadGateway {
		t.Fatalf("expected planner failure 502, got %#v", err)
	}
	run, runErr := svc.Store.GetAnswerRunByRequest(ctx, tenant, requestID)
	if runErr != nil || run == nil || run.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("planner failure was not recorded: run=%+v err=%v", run, runErr)
	}
}

func TestPlanRoutedMixedToolAnswerSynthesizesAnswer(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "synthesis-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant, "Synthesis Planner "+id.New(), map[string]interface{}{"steps": []string{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AssistantID: agent.ID, AuthorizationScope: `{"roles":["tenant_admin"]}`,
		SourceType: model.SourceTypeTool, Matcher: `{"intent":["sum_query"]}`,
		ToolID: tool.ToolID, ToolVersion: tool.Version, PolicyVersion: "calculation-policy-v1",
		Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	planner := &plannerAgentClient{responses: []string{
		`{"sub_queries":[{"intent":"sum_query","confidence":0.9,"tool_input":{"a":2,"b":3}}]}`,
		"The two values add up to 5.",
	}}
	svc.RAGFlow = planner
	requestID := "request-synthesized-" + id.New()
	result, err := svc.PlanRoutedMixedToolAnswer(ctx, tenant, user, agent.ID, RoutedPlannedToolAnswerInput{
		SessionID: "session-synthesized", AssistantID: agent.ID, RequestID: requestID,
		Question: "What is the sum?", Synthesize: true,
	})
	if err != nil {
		t.Fatalf("plan synthesized answer: %v", err)
	}
	if planner.request == nil || len(planner.request.Messages) != 2 {
		t.Fatalf("unexpected planner request: %+v", planner.request)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.ReasonCode != "MIXED_TOOL_ANSWERED_SYNTHESIZED" ||
		len(result.ToolExecutions) != 1 {
		t.Fatalf("unexpected synthesized answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
	var content struct {
		Schema      string `json:"schema"`
		Answer      string `json:"answer"`
		ToolResults struct {
			Results []struct {
				Output map[string]any `json:"output"`
			} `json:"results"`
		} `json:"tool_results"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &content); err != nil {
		t.Fatalf("decode synthesized content: %v", err)
	}
	if content.Schema != "planned_mixed_tool_answer_v1" || content.Answer != "The two values add up to 5." ||
		len(content.ToolResults.Results) != 1 || content.ToolResults.Results[0].Output["result"] != 5.0 {
		t.Fatalf("unexpected synthesized content: %+v", content)
	}
}

func TestPlanRoutedMixedToolAnswerSynthesisFailureFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "synthesis-failure-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant, "Synthesis Failure "+id.New(), map[string]interface{}{"steps": []string{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, addToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AssistantID: agent.ID, AuthorizationScope: `{"roles":["tenant_admin"]}`,
		SourceType: model.SourceTypeTool, Matcher: `{"intent":["sum_query"]}`,
		ToolID: tool.ToolID, ToolVersion: tool.Version, PolicyVersion: "calculation-policy-v1",
		Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	planner := &plannerAgentClient{responses: []string{
		`{"sub_queries":[{"intent":"sum_query","confidence":0.9,"tool_input":{"a":2,"b":3}}]}`,
		"  ",
	}}
	svc.RAGFlow = planner
	requestID := "request-synthesis-failed-" + id.New()
	_, err = svc.PlanRoutedMixedToolAnswer(ctx, tenant, user, agent.ID, RoutedPlannedToolAnswerInput{
		SessionID: "session-synthesis-failed", AssistantID: agent.ID, RequestID: requestID,
		Question: "What is the sum?", Synthesize: true,
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusBadGateway {
		t.Fatalf("expected synthesis failure 502, got %#v", err)
	}
	run, runErr := svc.Store.GetAnswerRunByRequest(ctx, tenant, requestID)
	if runErr != nil || run == nil || run.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("synthesis failure was not recorded: run=%+v err=%v", run, runErr)
	}
}

type plannedFactGuardFixture struct {
	service     *Service
	tenantID    string
	userID      string
	agentID     string
	planner     *plannerAgentClient
	answerRun   *model.AnswerRun
	revenueFact *model.FactRegistry
}

func newPlannedFactGuardFixture(t *testing.T, factGuardMode string) *plannedFactGuardFixture {
	t.Helper()
	ctx := context.Background()
	svc := newToolRegistryService(t)
	svc.SetFactGuardConfig(config.FactGuard{Mode: factGuardMode})
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "fact-guard-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant, "Fact Guard Planner "+id.New(), map[string]interface{}{"steps": []string{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, factLookupToolRegistryInput("v1", nil))
	if err != nil {
		t.Fatalf("create fact lookup tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AssistantID: agent.ID, AuthorizationScope: `{"roles":["tenant_admin"]}`,
		SourceType: model.SourceTypeTool, Matcher: `{"intent":["fact_query"]}`,
		ToolID: tool.ToolID, ToolVersion: tool.Version, PolicyVersion: "fact-policy-v1",
		Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	requestID := "request-fact-guard-" + id.New()
	answerRun := &model.AnswerRun{
		ID: id.New(), TenantID: tenant, SessionID: "session-fact-guard", AssistantID: agent.ID,
		QuestionRef: "question", RequestID: requestID, LifecycleState: model.AnswerLifecycleInit,
		AnswerStatus: model.AnswerStatusNoAnswer, CompletionReason: model.CompletionReasonNormal,
		CreatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateAnswerRun(ctx, answerRun); err != nil {
		t.Fatalf("create answer run: %v", err)
	}
	registration, err := svc.RegisterEvidenceFacts(ctx, tenant, user, RegisterEvidenceFactsInput{
		AnswerRunID: answerRun.ID, Facts: []EvidenceFactInput{factInput("10,000,000", nil)},
	})
	if err != nil {
		t.Fatalf("register evidence fact: %v", err)
	}
	planner := &plannerAgentClient{responses: []string{
		`{"sub_queries":[{"intent":"fact_query","confidence":0.9,"tool_input":{"fact_key":"revenue"}}]}`,
		"Revenue was ten million.",
	}}
	svc.RAGFlow = planner
	return &plannedFactGuardFixture{
		service: svc, tenantID: tenant, userID: user, agentID: agent.ID,
		planner: planner, answerRun: answerRun, revenueFact: &registration.Facts[0],
	}
}

func TestPlanRoutedMixedToolAnswerFactGuardAllowsSupportedClaim(t *testing.T) {
	ctx := context.Background()
	fixture := newPlannedFactGuardFixture(t, factGuardModeStrict)
	requestID := fixture.answerRun.RequestID
	result, err := fixture.service.PlanRoutedMixedToolAnswer(ctx, fixture.tenantID, fixture.userID, fixture.agentID, RoutedPlannedToolAnswerInput{
		SessionID: fixture.answerRun.SessionID, AssistantID: fixture.agentID,
		RequestID: requestID, Question: "What was revenue?", Synthesize: true,
		FactClaims: []PlannedFactClaimInput{{
			LLMClaim: "Revenue was ten million", FactIDs: []string{fixture.revenueFact.ID},
			FactKey: "revenue", Value: "10,000,000", Unit: "usd", TimeRange: "FY2025",
		}},
	})
	if err != nil {
		t.Fatalf("plan fact guarded answer: %v", err)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered ||
		result.Snapshot.ReasonCode != "MIXED_TOOL_ANSWERED_SYNTHESIZED" {
		t.Fatalf("unexpected fact guarded answer: run=%+v snapshot=%+v", result.Run, result.Snapshot)
	}
	validations, err := fixture.service.Store.ListClaimValidations(ctx, fixture.tenantID, result.Run.ID)
	if err != nil || len(validations) != 1 || validations[0].ValidationStatus != model.ClaimValidationSupported ||
		validations[0].Action != model.ClaimActionKeep {
		t.Fatalf("claim validation was not persisted: validations=%+v err=%v", validations, err)
	}
	var content struct {
		Schema    string `json:"schema"`
		Answer    string `json:"answer"`
		FactGuard struct {
			ClaimCount       int            `json:"claim_count"`
			ValidationStatus map[string]int `json:"validation_status"`
			Action           map[string]int `json:"action"`
		} `json:"fact_guard"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &content); err != nil {
		t.Fatalf("decode fact guarded content: %v", err)
	}
	if content.Schema != "planned_mixed_tool_answer_v1" || content.FactGuard.ClaimCount != 1 ||
		content.FactGuard.ValidationStatus["supported"] != 1 || content.FactGuard.Action["keep"] != 1 {
		t.Fatalf("unexpected fact guard summary: %+v", content)
	}
	overview, err := fixture.service.GetRoutedFactGuardOverview(ctx, fixture.tenantID, result.Run.ID)
	if err != nil {
		t.Fatalf("get fact guard overview: %v", err)
	}
	if overview.AnswerRunID != result.Run.ID || len(overview.Facts) != 1 ||
		len(overview.Claims) != 1 || overview.Summary.FactCount != 1 ||
		overview.Summary.ClaimCount != 1 ||
		overview.Summary.ValidationStatus["supported"] != 1 ||
		overview.Summary.Action["keep"] != 1 {
		t.Fatalf("unexpected fact guard overview: %+v", overview)
	}
	if overview.Claims[0].ID != validations[0].ID || overview.Claims[0].FactCount != 1 {
		t.Fatalf("unexpected claim summary: %+v", overview.Claims[0])
	}
}

func TestPlanRoutedMixedToolAnswerFactGuardBlocksContradictedClaim(t *testing.T) {
	ctx := context.Background()
	fixture := newPlannedFactGuardFixture(t, factGuardModeStrict)
	requestID := fixture.answerRun.RequestID
	_, err := fixture.service.PlanRoutedMixedToolAnswer(ctx, fixture.tenantID, fixture.userID, fixture.agentID, RoutedPlannedToolAnswerInput{
		SessionID: fixture.answerRun.SessionID, AssistantID: fixture.agentID,
		RequestID: requestID, Question: "What was revenue?", Synthesize: true,
		FactClaims: []PlannedFactClaimInput{{
			LLMClaim: "Revenue was eight million", FactIDs: []string{fixture.revenueFact.ID},
			FactKey: "revenue", Value: "8,000,000", Unit: "usd", TimeRange: "FY2025",
		}},
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusConflict ||
		businessErr.Code != 40996 {
		t.Fatalf("expected fact guard conflict 409/40996, got %#v", err)
	}
	run, runErr := fixture.service.Store.GetAnswerRunByRequest(ctx, fixture.tenantID, requestID)
	if runErr != nil || run == nil || run.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("fact guard failure was not recorded: run=%+v err=%v", run, runErr)
	}
	validations, validationErr := fixture.service.Store.ListClaimValidations(ctx, fixture.tenantID, run.ID)
	if validationErr != nil || len(validations) != 1 ||
		validations[0].ValidationStatus != model.ClaimValidationContradicted ||
		validations[0].Action != model.ClaimActionBlock {
		t.Fatalf("blocked claim validation was not persisted: validations=%+v err=%v", validations, validationErr)
	}
	if len(fixture.planner.responses) != 1 {
		t.Fatalf("synthesis must not run after fact guard block")
	}
}

func TestPlanRoutedMixedToolAnswerProjectsFlaggedClaims(t *testing.T) {
	ctx := context.Background()
	fixture := newPlannedFactGuardFixture(t, factGuardModeWarn)
	requestID := fixture.answerRun.RequestID
	result, err := fixture.service.PlanRoutedMixedToolAnswer(ctx, fixture.tenantID, fixture.userID, fixture.agentID, RoutedPlannedToolAnswerInput{
		SessionID: fixture.answerRun.SessionID, AssistantID: fixture.agentID,
		RequestID: requestID, Question: "What was revenue?", Synthesize: true,
		FactClaims: []PlannedFactClaimInput{{
			LLMClaim: "Profit was ten million", FactIDs: []string{fixture.revenueFact.ID},
			FactKey: "profit", Value: "10,000,000", Unit: "usd", TimeRange: "FY2025",
		}},
	})
	if err != nil {
		t.Fatalf("plan flagged fact guarded answer: %v", err)
	}
	if result.Run.AnswerStatus != model.AnswerStatusAnswered {
		t.Fatalf("flagged claim must not fail the answer: %+v", result.Run)
	}
	var limitations []string
	if err := json.Unmarshal([]byte(result.Snapshot.LimitationsJSON), &limitations); err != nil {
		t.Fatalf("decode limitations: %v", err)
	}
	if len(limitations) != 1 || limitations[0] != "fact_guard.flagged_claims:1" {
		t.Fatalf("unexpected fact guard limitation: %+v", limitations)
	}
	validations, err := fixture.service.Store.ListClaimValidations(ctx, fixture.tenantID, result.Run.ID)
	if err != nil || len(validations) != 1 || validations[0].ValidationStatus != model.ClaimValidationUnsupported ||
		validations[0].Action != model.ClaimActionFlag {
		t.Fatalf("flagged claim validation was not persisted: validations=%+v err=%v", validations, err)
	}
	var content struct {
		FactGuard struct {
			ValidationStatus map[string]int `json:"validation_status"`
			Action           map[string]int `json:"action"`
		} `json:"fact_guard"`
	}
	if err := json.Unmarshal([]byte(result.Snapshot.Content), &content); err != nil {
		t.Fatalf("decode flagged content: %v", err)
	}
	if content.FactGuard.ValidationStatus["unsupported"] != 1 || content.FactGuard.Action["flag"] != 1 {
		t.Fatalf("unexpected flagged summary: %+v", content.FactGuard)
	}
}

func TestPlanRoutedMixedToolAnswerRejectsClaimsWithoutSynthesis(t *testing.T) {
	ctx := context.Background()
	fixture := newPlannedFactGuardFixture(t, factGuardModeWarn)
	_, err := fixture.service.PlanRoutedMixedToolAnswer(ctx, fixture.tenantID, fixture.userID, fixture.agentID, RoutedPlannedToolAnswerInput{
		SessionID: fixture.answerRun.SessionID, AssistantID: fixture.agentID,
		RequestID: fixture.answerRun.RequestID, Question: "What was revenue?",
		FactClaims: []PlannedFactClaimInput{{
			LLMClaim: "Revenue was ten million", FactIDs: []string{fixture.revenueFact.ID},
			Value: "10,000,000",
		}},
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusBadRequest {
		t.Fatalf("expected claims without synthesis 400, got %#v", err)
	}
}

func TestCreateRoutedAnswerRunReusesOpenRun(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "answer-run-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	agent, err := svc.CreateAgent(ctx, tenant, "Answer Run Planner "+id.New(), map[string]interface{}{"steps": []string{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
	input := RoutedAnswerRunInput{
		SessionID: "session-answer-run", AssistantID: agent.ID,
		RequestID: "request-answer-run-" + id.New(), Question: "What was revenue?",
	}
	created, err := svc.CreateRoutedAnswerRun(ctx, tenant, user, agent.ID, input)
	if err != nil {
		t.Fatalf("create answer run: %v", err)
	}
	reused, err := svc.CreateRoutedAnswerRun(ctx, tenant, user, agent.ID, input)
	if err != nil {
		t.Fatalf("reuse answer run: %v", err)
	}
	if created.ID == "" || created.ID != reused.ID || created.LifecycleState != model.AnswerLifecycleInit {
		t.Fatalf("unexpected answer runs: created=%+v reused=%+v", created, reused)
	}
	input.AssistantID = "assistant-mismatch"
	if _, err := svc.CreateRoutedAnswerRun(ctx, tenant, user, agent.ID, input); err == nil {
		t.Fatal("answer run request mismatch must fail")
	}
}

func TestExecuteRoutedMixedToolAnswerRejectsToolScopeMismatch(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	if err := svc.Store.CreateUser(ctx, &model.User{
		ID: user, TenantID: tenant, Username: "mixed-tool-user-" + user,
		PasswordHash: "test-hash", Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	tool, err := svc.CreateToolRegistry(ctx, tenant, user, ToolRegistryInput{
		ToolID: "add", Version: "v1", ToolType: model.ToolTypeCalculation, Name: "Add numbers",
		InputSchema:        `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		OutputSchema:       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		AuthorizationScope: `{"roles":["platform_admin"]}`, ImplementationRef: "builtin:add", Active: nil,
	})
	if err != nil {
		t.Fatalf("create calculation tool: %v", err)
	}
	if _, err := svc.CreateSourceRoutingRule(ctx, tenant, user, SourceRoutingRuleInput{
		AuthorizationScope: `{"roles":["tenant_admin"]}`, SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["sum_query"]}`, ToolID: tool.ToolID, ToolVersion: tool.Version,
		PolicyVersion: "calculation-policy-v1", Priority: 10, ConfidenceThreshold: 0.8,
	}); err != nil {
		t.Fatalf("create calculation rule: %v", err)
	}
	requestID := "request-mixed-scope-" + id.New()
	_, err = svc.ExecuteRoutedMixedToolAnswer(ctx, tenant, user, "assistant-mixed", RoutedMixedToolAnswerInput{
		SessionID: "session-mixed", AssistantID: "assistant-mixed", RequestID: requestID,
		Question: "What is the total?", SubQueries: []RoutedToolSubqueryInput{
			{Intent: "sum_query", Confidence: 0.9, ToolInput: json.RawMessage(`{"a":2,"b":3}`)},
		},
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusForbidden {
		t.Fatalf("expected scope mismatch 403, got %#v", err)
	}
	run, err := svc.Store.GetAnswerRunByRequest(ctx, tenant, requestID)
	if err != nil || run == nil || run.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("failed answer run was not recorded: run=%+v err=%v", run, err)
	}
}
