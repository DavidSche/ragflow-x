package service

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func memoryMessageWithContent(id int64, content string) ragflow.MemoryMessage {
	return ragflow.MemoryMessage{MessageID: id, MemoryID: "memory-1", UserInput: "question", AgentResponse: "answer", Content: content, Status: true}
}

const stableFactTemplate = `{"stable_fact":{"entity":"Prod DB","attribute":"max connections","value":"200"}}`
const knowledgeFactConflict = `The production database caps at 100 sessions. {"knowledge_fact":{"entity":"prod db","attribute":"Max Connections","value":"100"}}`
const knowledgeFactSame = `{"knowledge_fact":{"entity":"Prod DB","attribute":"max connections","value":" 200 "}}`

func TestEvaluateMemoryConflictsNonAnnotatedStaysHistoricalContext(t *testing.T) {
	summary := evaluateMemoryConflicts([]ragflow.MemoryMessage{
		memoryMessageWithContent(1, "user prefers dark mode"),
	}, []string{knowledgeFactConflict})
	if summary.EvaluatedCount != 0 || summary.NotApplicableCount != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(summary.Items) != 1 || summary.Items[0].Verdict != MemoryConflictNotApplicable {
		t.Fatalf("non-annotated memory must stay historical context: %+v", summary.Items)
	}
	if summary.ContextPolicyVersion != memoryConflictPolicyVersion {
		t.Fatalf("context policy version missing: %+v", summary)
	}
}

func TestEvaluateMemoryConflictsValueConflictDowngrades(t *testing.T) {
	summary := evaluateMemoryConflicts([]ragflow.MemoryMessage{
		memoryMessageWithContent(7, stableFactTemplate),
	}, []string{knowledgeFactConflict})
	if summary.EvaluatedCount != 1 || summary.DowngradedCount != 1 {
		t.Fatalf("conflicting stable fact must downgrade: %+v", summary)
	}
	item := summary.Items[0]
	if item.Verdict != MemoryConflictDowngraded || item.MessageID != 7 {
		t.Fatalf("unexpected item: %+v", item)
	}
	if item.KnowledgeValue != "100" || item.MemoryValue != "200" {
		t.Fatalf("conflict evidence missing: %+v", item)
	}
}

func TestEvaluateMemoryConflictsSameValueConsistent(t *testing.T) {
	summary := evaluateMemoryConflicts([]ragflow.MemoryMessage{
		memoryMessageWithContent(3, stableFactTemplate),
	}, []string{knowledgeFactSame})
	if summary.ConsistentCount != 1 || summary.DowngradedCount != 0 {
		t.Fatalf("matching value must stay consistent: %+v", summary)
	}
	if summary.Items[0].Verdict != MemoryConflictConsistent {
		t.Fatalf("unexpected verdict: %+v", summary.Items[0])
	}
}

func TestEvaluateMemoryConflictsWithoutKnowledgeEvidenceFailsClosed(t *testing.T) {
	summary := evaluateMemoryConflicts([]ragflow.MemoryMessage{
		memoryMessageWithContent(5, stableFactTemplate),
	}, nil)
	if summary.UnknownCount != 1 || summary.DowngradedCount != 0 {
		t.Fatalf("stable fact without knowledge evidence must be fail-closed unknown: %+v", summary)
	}
	if summary.Items[0].Verdict != MemoryConflictUnknown || summary.Items[0].Detail != "no_decisive_knowledge_evidence" {
		t.Fatalf("unexpected item: %+v", summary.Items[0])
	}
}

func TestEvaluateMemoryConflictsMalformedAnnotationDowngrades(t *testing.T) {
	summary := evaluateMemoryConflicts([]ragflow.MemoryMessage{
		memoryMessageWithContent(9, `{"stable_fact":{"entity":"Prod DB","attribute":"max connections"}}`),
	}, []string{knowledgeFactConflict})
	if summary.UnknownCount != 1 {
		t.Fatalf("malformed stable fact must downgrade as unknown: %+v", summary)
	}
	if summary.Items[0].Verdict != MemoryConflictUnknown || summary.Items[0].Detail != "stable_fact_annotation_not_deterministic" {
		t.Fatalf("unexpected item: %+v", summary.Items[0])
	}
}

func TestEvaluateMemoryConflictsNormalizesTokens(t *testing.T) {
	summary := evaluateMemoryConflicts([]ragflow.MemoryMessage{
		memoryMessageWithContent(11, `{"stable_fact":{"entity":"  PROD   db ","attribute":"Max\tConnections","value":"200"}}`),
	}, []string{`{"knowledge_fact":{"entity":"prod db","attribute":"max connections","value":"200"}}`})
	if summary.ConsistentCount != 1 {
		t.Fatalf("normalized entity/attribute/value must match: %+v", summary)
	}
}

func TestEvaluateMemoryConflictsTrailingAnnotationAccepted(t *testing.T) {
	summary := evaluateMemoryConflicts([]ragflow.MemoryMessage{
		memoryMessageWithContent(13, "the deployment runbook states: "+stableFactTemplate),
	}, []string{knowledgeFactConflict})
	if summary.DowngradedCount != 1 {
		t.Fatalf("trailing stable fact annotation must be evaluated: %+v", summary)
	}
}

func TestSearchGovernedMemoryAttachesConflictAndPolicyVersion(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	_, memory := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-1")
	if err := svc.RAGFlow.AddMemoryMessage(t.Context(), ragflow.AddMessageRequest{
		MemoryIDs: []string{memory.ID}, AgentID: "assistant-1", SessionID: "session-1",
		UserID: "rgx:" + tenant.ID + ":" + user.ID, UserInput: "database", AgentResponse: "config",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := svc.SearchGovernedMemory(t.Context(), tenant.ID, user.ID, memory.ID, GovernedMemorySearchInput{
		Query: "database", AssistantID: "assistant-1", SessionID: "session-1",
		RequestID: "request-conflict-search", TopN: 5,
		KnowledgeChunks: []string{knowledgeFactConflict},
	})
	if err != nil {
		t.Fatalf("governed search: %v", err)
	}
	if result.Conflict == nil {
		t.Fatal("conflict summary must always be attached")
	}
	if result.Conflict.ContextPolicyVersion != memoryConflictPolicyVersion {
		t.Fatalf("unexpected conflict version: %+v", result.Conflict)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(result.Decision.Detail), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["context_policy_version"] != memoryConflictPolicyVersion {
		t.Fatalf("decision detail must record context_policy_version: %+v", detail)
	}
}

func TestExecuteToolRegistryMemoryRetrievalConflictOutput(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	_, memory := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-1")
	if err := svc.RAGFlow.AddMemoryMessage(t.Context(), ragflow.AddMessageRequest{
		MemoryIDs: []string{memory.ID}, AgentID: "assistant-1", SessionID: "session-1",
		UserID: "rgx:" + tenant.ID + ":" + user.ID, UserInput: "database", AgentResponse: "config",
	}); err != nil {
		t.Fatal(err)
	}

	spec := builtinToolSpecs["memory_retrieval"]
	tool, err := svc.CreateToolRegistry(t.Context(), tenant.ID, user.ID, ToolRegistryInput{
		ToolID: spec.ToolID, Version: "v1", ToolType: spec.ToolType, Name: "Governed memory",
		InputSchema: spec.InputSchema, OutputSchema: spec.OutputSchema,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: spec.ImplementationRef,
	})
	if err != nil {
		t.Fatalf("create memory tool: %v", err)
	}
	result, err := svc.ExecuteToolRegistry(t.Context(), tenant.ID, user.ID, tool.ID, ToolExecutionInput{
		AnswerRunID: "request-conflict-tool",
		Input: []byte(`{"memory_id":"` + memory.ID + `","assistant_id":"assistant-1","session_id":"session-1","question":"database","knowledge_chunks":[` +
			`"` + strings.ReplaceAll(knowledgeFactConflict, `"`, `\"`) + `"]}`),
	})
	if err != nil {
		t.Fatalf("memory retrieval tool: %v", err)
	}
	var output map[string]interface{}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	conflict, ok := output["conflict"].(map[string]interface{})
	if !ok || conflict["context_policy_version"] != memoryConflictPolicyVersion {
		t.Fatalf("tool output must carry conflict summary: %+v", output)
	}
	messages, ok := output["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected messages: %+v", output["messages"])
	}
	message := messages[0].(map[string]interface{})
	if message["conflict_status"] != MemoryConflictNotApplicable {
		t.Fatalf("mock message without content must stay historical context: %+v", message)
	}
}

func TestExecuteToolRegistryMemoryRetrievalRejectsNonStringKnowledgeChunks(t *testing.T) {
	svc, tenant, user := newMemoryPolicySvc(t)
	_, memory := activateSelfMemoryPolicy(t, svc, tenant.ID, user.ID, "assistant-1")
	spec := builtinToolSpecs["memory_retrieval"]
	tool, err := svc.CreateToolRegistry(t.Context(), tenant.ID, user.ID, ToolRegistryInput{
		ToolID: spec.ToolID, Version: "v1", ToolType: spec.ToolType, Name: "Governed memory",
		InputSchema: spec.InputSchema, OutputSchema: spec.OutputSchema,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: spec.ImplementationRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteToolRegistry(t.Context(), tenant.ID, user.ID, tool.ID, ToolExecutionInput{
		AnswerRunID: "request-conflict-bad-input",
		Input:       []byte(`{"memory_id":"` + memory.ID + `","assistant_id":"assistant-1","session_id":"session-1","question":"database","knowledge_chunks":[1]}`),
	}); err == nil || !strings.Contains(err.Error(), "knowledge_chunks") {
		t.Fatalf("non-string knowledge chunk must fail closed: err=%v", err)
	}
}

func TestEvaluateMemoryConflictsNilDecisionSafeForLegacyCallers(t *testing.T) {
	// Legacy call sites without knowledge chunks still receive a summary with
	// every stable fact downgraded (fail-closed), not a nil pointer.
	summary := evaluateMemoryConflicts(nil, nil)
	if summary == nil || summary.ContextPolicyVersion != memoryConflictPolicyVersion {
		t.Fatalf("empty evaluation must stay non-nil with policy version: %+v", summary)
	}
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "conflict-legacy.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dErr := gdb.DB(); dErr == nil {
			_ = sqlDB.Close()
		}
	})
	if _, _, err := repository.NewStore(gdb).ListContextDecisions(t.Context(), "missing-tenant", repository.ContextDecisionFilter{}, 1, 10); err != nil {
		t.Fatalf("store regression: %v", err)
	}
}
