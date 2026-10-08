package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func toolRoutingRouter(t *testing.T) (*gin.Engine, *service.Service, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "tool-routing-handler")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenant.ID)
		c.Set(middleware.ContextUserID, "admin")
		c.Next()
	})
	RegisterToolRoutingRoutes(group)
	return router, svc, tenant.ID
}

func decodeObject[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %s: %v", recorder.Body.String(), err)
	}
	return body.Data
}

func TestToolRegistryRoutes(t *testing.T) {
	router, _, _ := toolRoutingRouter(t)
	input := map[string]interface{}{
		"tool_id": "add", "version": "v1", "tool_type": model.ToolTypeCalculation,
		"name": "Add numbers", "input_schema": `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		"output_schema":       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		"authorization_scope": `{"roles":["tenant_admin"]}`, "implementation_ref": "builtin:add",
	}
	payload, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tool-registry", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create tool registry: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	created := decodeObject[model.ToolRegistry](t, recorder)
	if created.ID == "" || created.ToolID != "add" || !created.Active {
		t.Fatalf("unexpected created tool: %+v", created)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tool-registry?tool_type=calculation&active=true", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list tool registry: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	list := decodeObject[struct {
		Items []model.ToolRegistry `json:"items"`
		Total int64                `json:"total"`
	}](t, recorder)
	if len(list.Items) != 1 || list.Total != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("unexpected tool list: %+v", list)
	}

	inactive := false
	input["active"] = inactive
	payload, _ = json.Marshal(input)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/tool-registry/"+created.ID, bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update tool registry: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated := decodeObject[model.ToolRegistry](t, recorder)
	if updated.Active {
		t.Fatalf("unexpected updated tool: %+v", updated)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/tool-registry/"+created.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete tool registry: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tool-registry/"+created.ID, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted tool registry: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestToolRegistryExecuteRoute(t *testing.T) {
	router, svc, tenantID := toolRoutingRouter(t)
	active, err := svc.CreateToolRegistry(t.Context(), tenantID, "admin", service.ToolRegistryInput{
		ToolID: "add", Version: "v1", ToolType: model.ToolTypeCalculation, Name: "Add numbers",
		InputSchema:        `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		OutputSchema:       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: "builtin:add",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"input": map[string]interface{}{"a": 2, "b": 3},
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tool-registry/"+active.ID+"/execute", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("execute tool: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	result := decodeObject[service.ToolExecutionResult](t, recorder)
	if result.Tool.ID != active.ID || string(result.Output) != `{"result":5}` {
		t.Fatalf("unexpected execution result: %+v", result)
	}

	invalid := map[string]interface{}{
		"input": map[string]interface{}{"a": "not-a-number", "b": 3},
	}
	payload, _ = json.Marshal(invalid)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tool-registry/"+active.ID+"/execute", bytes.NewReader(payload)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid tool input: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	inactive := false
	if _, err := svc.UpdateToolRegistry(t.Context(), tenantID, "admin", active.ID, service.ToolRegistryInput{
		ToolID: "add", Version: "v1", ToolType: model.ToolTypeCalculation, Name: "Add numbers",
		InputSchema:        `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		OutputSchema:       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: "builtin:add", Active: &inactive,
	}); err != nil {
		t.Fatal(err)
	}
	payload, _ = json.Marshal(map[string]interface{}{"input": map[string]interface{}{"a": 2, "b": 3}})
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tool-registry/"+active.ID+"/execute", bytes.NewReader(payload)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("inactive tool execution: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRoutedSQLAnswerRoutesValidateAssistantContext(t *testing.T) {
	router, _, _ := toolRoutingRouter(t)
	for _, path := range []string{
		"/api/v1/tool-routing/routed-sql-answer",
		"/api/v1/tool-routing/routed-sql-answers",
		"/api/v1/tool-routing/routed-mixed-tool-answers",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`))))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s empty input: status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRoutedAnswerRunAndEvidenceFactsRoutes(t *testing.T) {
	router, svc, tenantID := toolRoutingRouter(t)
	if err := svc.Store.CreateUser(t.Context(), &model.User{
		ID: "admin", TenantID: tenantID, Username: "evidence-admin", PasswordHash: "test-hash",
		Role: model.RoleTenantAdmin, Status: model.UserStatusActive,
	}); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	agent, err := svc.CreateAgent(t.Context(), tenantID, "Evidence Planner "+id.New(), map[string]interface{}{"steps": []string{}}, true, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create planner agent: %v", err)
	}
	runPayload, _ := json.Marshal(map[string]interface{}{
		"session_id": "session-evidence", "assistant_id": agent.ID,
		"request_id": "request-evidence-" + id.New(), "question": "What was revenue?",
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tool-routing/answer-runs", bytes.NewReader(runPayload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create answer run: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	run := decodeObject[model.AnswerRun](t, recorder)
	if run.ID == "" || run.LifecycleState != model.AnswerLifecycleInit {
		t.Fatalf("unexpected answer run: %+v", run)
	}

	factsPayload, _ := json.Marshal(map[string]interface{}{
		"resolver_mode": "current",
		"facts": []map[string]interface{}{
			{
				"fact_key": "revenue", "value": "10,000,000", "unit": "usd", "time_range": "FY2025",
				"source_doc_id": "doc-1", "claim_type": "extracted", "confidence": 0.9,
			},
			{
				"fact_key": "revenue", "value": "12,000,000", "unit": "usd", "time_range": "FY2025",
				"source_doc_id": "doc-1", "claim_type": "extracted", "confidence": 0.9,
			},
		},
	})
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tool-routing/answer-runs/"+run.ID+"/evidence-facts", bytes.NewReader(factsPayload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("register evidence facts: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	registration := decodeObject[service.EvidenceFactRegistrationResult](t, recorder)
	if len(registration.Facts) != 2 || len(registration.Conflicts) != 1 ||
		registration.Conflicts[0].ConflictType != model.FactConflictValue {
		t.Fatalf("unexpected evidence registration: %+v", registration)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tool-routing/answer-runs/"+run.ID+"/evidence-facts", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list evidence facts: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	evidence := decodeObject[struct {
		AnswerRunID string                   `json:"answer_run_id"`
		Facts       []model.FactRegistry     `json:"facts"`
		Conflicts   []model.EvidenceConflict `json:"conflicts"`
	}](t, recorder)
	if evidence.AnswerRunID != run.ID || len(evidence.Facts) != 2 ||
		len(evidence.Conflicts) != 1 || evidence.Conflicts[0].ConflictType != model.FactConflictValue {
		t.Fatalf("unexpected evidence list: %+v", evidence)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tool-routing/answer-runs/"+run.ID+"/fact-guard", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get fact guard overview: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	overview := decodeObject[service.FactGuardOverview](t, recorder)
	if overview.AnswerRunID != run.ID || len(overview.Facts) != 2 ||
		len(overview.Conflicts) != 1 || overview.Summary.FactCount != 2 ||
		overview.Summary.ConflictCount != 1 || overview.Summary.ClaimCount != 0 {
		t.Fatalf("unexpected fact guard overview: %+v", overview)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tool-routing/answer-runs", bytes.NewReader([]byte(`{}`))))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty answer run input: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSourceRoutingRuleRoutes(t *testing.T) {
	router, svc, tenantID := toolRoutingRouter(t)
	tool, err := svc.CreateToolRegistry(t.Context(), tenantID, "admin", service.ToolRegistryInput{
		ToolID: "add", Version: "v1", ToolType: model.ToolTypeCalculation, Name: "Add numbers",
		InputSchema:        `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
		OutputSchema:       `{"type":"object","required":["result"],"properties":{"result":{"type":"number"}}}`,
		AuthorizationScope: `{"roles":["tenant_admin"]}`, ImplementationRef: "builtin:add",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]interface{}{
		"source_type": model.SourceTypeTool, "matcher": `{"intent":"exposure_query"}`,
		"tool_id": "add", "tool_version": "v1", "policy_version": "policy-v1",
		"authorization_scope": "{\"roles\":[\"tenant_admin\"]}",
		"priority":            10, "confidence_threshold": 0.8,
	}
	payload, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/source-routing-rules", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create source routing rule: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	created := decodeObject[model.SourceRoutingRule](t, recorder)
	if created.ID == "" || created.ToolID != tool.ToolID || created.Matcher != `{"intent":"exposure_query"}` {
		t.Fatalf("unexpected created rule: %+v", created)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/source-routing-rules?source_type=tool&active=true", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list source routing rules: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	list := decodeObject[struct {
		Items []model.SourceRoutingRule `json:"items"`
		Total int64                     `json:"total"`
	}](t, recorder)
	if len(list.Items) != 1 || list.Total != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("unexpected source routing list: %+v", list)
	}

	input["matcher"] = `{"intent":"exposure_summary"}`
	input["priority"] = 5
	payload, _ = json.Marshal(input)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/api/v1/source-routing-rules/"+created.ID, bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update source routing rule: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated := decodeObject[model.SourceRoutingRule](t, recorder)
	if updated.Matcher != `{"intent":"exposure_summary"}` || updated.Priority != 5 {
		t.Fatalf("unexpected updated rule: %+v", updated)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/source-routing-rules/"+created.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete source routing rule: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/source-routing-rules/"+created.ID, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted source routing rule: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
