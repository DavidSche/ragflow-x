package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func knowledgeStrategyRouter(t *testing.T) (*gin.Engine, *service.Service, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "knowledge-strategy-handler")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(t.Context(), tenant.ID, "", service.CreateUserRequest{
		Username: "strategy-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	group := router.Group("/api/v1", func(c *gin.Context) {
		c.Set("handler", New(svc))
		c.Set(middleware.ContextTenantID, tenant.ID)
		c.Set(middleware.ContextUserID, admin.ID)
		c.Next()
	})
	RegisterKnowledgeStrategyRoutes(group)
	return router, svc, tenant.ID
}

func createHandlerDatasetLink(t *testing.T, svc *service.Service, tenantID, name string) string {
	t.Helper()
	dataset := model.DatasetLink{
		ID: id.New(), TenantID: tenantID, RAGFlowDatasetID: "ragflow-" + id.New(),
		Name: name,
	}
	if err := svc.Store.CreateDatasetLink(t.Context(), &dataset); err != nil {
		t.Fatal(err)
	}
	return dataset.ID
}

func TestKnowledgeStrategyRoutes(t *testing.T) {
	gin, svc, tenantID := knowledgeStrategyRouter(t)
	datasetID := createHandlerDatasetLink(t, svc, tenantID, "Strategy Dataset")
	input := service.KnowledgeStrategyInput{
		DatasetID: datasetID, StrategyType: "semantic",
		Config:             `{"probe_key":"strategy"}`,
		FallbackStrategy:   "hybrid",
		RAGFlowCapability:  "retrieval",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	}
	payload, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-strategies", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	created := decodeObject[map[string]interface{}](t, recorder)
	strategyID, _ := created["id"].(string)
	if strategyID == "" {
		t.Fatalf("created strategy has no id: %v", created)
	}

	recorder = httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-strategies?strategy_type=semantic&active=true", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list strategies: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	input.StrategyType = "hybrid"
	input.FallbackStrategy = "semantic"
	payload, _ = json.Marshal(input)
	recorder = httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/knowledge-strategies/"+strategyID, bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/knowledge-strategies/"+strategyID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-strategies/"+strategyID, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestKnowledgeStrategyProbeRoute(t *testing.T) {
	gin, svc, tenantID := knowledgeStrategyRouter(t)
	datasetID := createHandlerDatasetLink(t, svc, tenantID, "Probe Strategy Dataset")
	probeDatasetID := createHandlerDatasetLink(t, svc, tenantID, "Probe Dataset")
	input := service.KnowledgeStrategyInput{
		DatasetID: datasetID, StrategyType: "semantic",
		Config:             `{"dataset_id":"` + probeDatasetID + `"}`,
		FallbackStrategy:   "hybrid",
		RAGFlowCapability:  "retrieval",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	}
	payload, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-strategies", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	created := decodeObject[map[string]interface{}](t, recorder)
	strategyID, _ := created["id"].(string)

	recorder = httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-strategies/"+strategyID+"/probe", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("probe strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	probe := decodeObject[map[string]interface{}](t, recorder)
	if probe["status"] != "passed" {
		t.Fatalf("unexpected probe response: %v", probe)
	}
}

func TestKnowledgeStrategyRetrieveRoute(t *testing.T) {
	gin, svc, tenantID := knowledgeStrategyRouter(t)
	datasetID := createHandlerDatasetLink(t, svc, tenantID, "Retrieve Dataset")
	input := service.KnowledgeStrategyInput{
		DatasetID: datasetID, StrategyType: "semantic",
		FallbackStrategy:   "hybrid",
		RAGFlowCapability:  "retrieval",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	}
	payload, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-strategies", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	created := decodeObject[map[string]interface{}](t, recorder)
	strategyID, _ := created["id"].(string)
	strategy, err := svc.GetKnowledgeStrategy(t.Context(), tenantID, strategyID)
	if err != nil {
		t.Fatal(err)
	}
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(t.Context(), strategy); err != nil {
		t.Fatal(err)
	}

	pageSize := 5
	payload, _ = json.Marshal(service.KnowledgeStrategyRetrieveInput{Question: "What changed?", PageSize: &pageSize})
	recorder = httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPost, "/api/v1/knowledge-strategies/"+strategyID+"/retrieve", bytes.NewReader(payload),
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("retrieve strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	result := decodeObject[map[string]interface{}](t, recorder)
	if result["used_strategy_type"] != "semantic" || result["fallback_used"] != false {
		t.Fatalf("unexpected retrieve response: %v", result)
	}
}

func TestKnowledgeStrategyRetrieveRouteBindsVersionResolver(t *testing.T) {
	gin, svc, tenantID := knowledgeStrategyRouter(t)
	datasetID := createHandlerDatasetLink(t, svc, tenantID, "Version Resolver Dataset")
	input := service.KnowledgeStrategyInput{
		DatasetID: datasetID, StrategyType: "semantic",
		FallbackStrategy:   "hybrid",
		RAGFlowCapability:  "retrieval",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	}
	payload, _ := json.Marshal(input)
	recorder := httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-strategies", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create strategy: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	strategyID, _ := decodeObject[map[string]interface{}](t, recorder)["id"].(string)
	strategy, err := svc.GetKnowledgeStrategy(t.Context(), tenantID, strategyID)
	if err != nil {
		t.Fatal(err)
	}
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(t.Context(), strategy); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	logical := model.LogicalDocument{
		ID: id.New(), TenantID: tenantID, DatasetID: datasetID, Name: "Logical",
		SourceURI: "test://logical", CreatedBy: "admin", CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateLogicalDocument(t.Context(), &logical); err != nil {
		t.Fatal(err)
	}
	version := model.DocumentVersion{
		ID: id.New(), TenantID: tenantID, LogicalDocumentID: logical.ID,
		RAGFlowDocumentID: "ragflow-version", Version: 1, Status: model.DocumentVersionActive,
		EffectiveFrom: now.Add(-time.Hour), ContentHash: "version-handler",
		CreatedBy: "admin", CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateDocumentVersion(t.Context(), &version); err != nil {
		t.Fatal(err)
	}

	payload, _ = json.Marshal(map[string]interface{}{
		"question": "What changed?",
		"version_resolver": map[string]interface{}{
			"mode": "current", "logical_document_id": logical.ID,
		},
	})
	recorder = httptest.NewRecorder()
	gin.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPost, "/api/v1/knowledge-strategies/"+strategyID+"/retrieve", bytes.NewReader(payload),
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("retrieve with resolver: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	result := decodeObject[map[string]interface{}](t, recorder)
	resolver, ok := result["version_resolver"].(map[string]interface{})
	if !ok || resolver["version_id"] != version.ID || resolver["pushdown_used"] != true {
		t.Fatalf("unexpected resolver response: %v", result)
	}
}
