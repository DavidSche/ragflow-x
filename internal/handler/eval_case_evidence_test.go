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

func TestEvalCaseEvidenceRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "eval-case-evidence-handler")
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
	RegisterEvalCaseEvidenceRoutes(group)

	evalCaseID, logicalID := id.New(), id.New()
	tenantID := tenant.ID
	now := time.Now().UTC()
	if err = svc.Store.CreateLogicalDocument(t.Context(), &model.LogicalDocument{
		ID: logicalID, TenantID: tenantID, DatasetID: "dataset-1", Name: "Policy",
		CreatedBy: "tester", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	if err = svc.Store.CreateEvalCases(t.Context(), tenantID, []model.EvalCase{{
		ID: evalCaseID, TenantID: tenantID, EvalSetID: id.New(), Question: "question",
		Status: model.EvalStatusPublished, CreatedBy: "tester", CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("create eval case: %v", err)
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"dataset_ids": []string{"dataset-1"},
		"dependencies": []map[string]string{{
			"logical_document_id": logicalID, "binding_type": model.EvidenceBindingRetrieval,
		}},
		"retrieval_policy_version": "retrieval-v1", "prompt_version": "prompt-v1",
		"model_version": "model-v1", "parser_policy_version": "parser-v1",
		"tool_policy_version": "tool-v1", "authorization_policy_version": "authorization-v1",
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/eval-cases/"+evalCaseID+"/evidence", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update evidence: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/eval-cases/"+evalCaseID+"/evidence", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get evidence: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/eval-cases/"+evalCaseID+"/revalidate", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("fresh revalidation: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/eval-cases/"+id.New()+"/evidence", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing evidence: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
