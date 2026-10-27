package handler

import (
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

func evidenceSnapshotRouter(t *testing.T) (*gin.Engine, *service.Service, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "evidence-snapshot-handler")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(t.Context(), tenant.ID, "", service.CreateUserRequest{
		Username: "evidence-admin", Password: "secret123", Role: model.RoleTenantAdmin,
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
	RegisterEvidenceSnapshotRoutes(group)
	return router, svc, tenant.ID
}

func createHandlerEvidenceSnapshot(t *testing.T, svc *service.Service, tenantID string) string {
	t.Helper()
	now := time.Now().UTC()
	logicalID := id.New()
	if err := svc.Store.CreateLogicalDocument(t.Context(), &model.LogicalDocument{
		ID: logicalID, TenantID: tenantID, DatasetID: "dataset-1", Name: "Policy",
		CreatedBy: "tester", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	evalCaseID := id.New()
	if err := svc.Store.CreateEvalCases(t.Context(), tenantID, []model.EvalCase{{
		ID: evalCaseID, TenantID: tenantID, EvalSetID: id.New(), Question: "question",
		Status: model.EvalStatusPublished, CreatedBy: "tester", CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("create eval case: %v", err)
	}
	created, err := svc.CreateEvidenceSnapshot(t.Context(), tenantID, "tester", service.EvidenceSnapshotInput{
		EvalCaseID: evalCaseID, DatasetIDs: []string{"dataset-1"},
		Dependencies: []service.EvidenceDependencyInput{{
			LogicalDocumentID: logicalID, BindingType: model.EvidenceBindingRetrieval,
		}},
		RetrievalPolicyVersion: "retrieval-v1", PromptVersion: "prompt-v1", ModelVersion: "model-v1",
		ParserPolicyVersion: "parser-v1", ToolPolicyVersion: "tool-v1",
		AuthorizationPolicyVersion: "authorization-v1",
	})
	if err != nil {
		t.Fatalf("create evidence snapshot: %v", err)
	}
	return created.Snapshot.ID
}

func TestEvidenceSnapshotRoutes(t *testing.T) {
	router, svc, tenantID := evidenceSnapshotRouter(t)
	snapshotID := createHandlerEvidenceSnapshot(t, svc, tenantID)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/evidence-snapshots?stale_status=fresh", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list evidence snapshots: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page struct {
		Items []repository.EvidenceSnapshotListItem `json:"items"`
		Total int64                                 `json:"total"`
	}
	if err := json.Unmarshal(decodeObject[json.RawMessage](t, recorder), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].DependencyCount != 1 ||
		page.Items[0].Snapshot.ID != snapshotID {
		t.Fatalf("unexpected evidence snapshot page: %+v", page)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/evidence-snapshots/"+snapshotID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get evidence snapshot: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/evidence-snapshots/"+id.New(), nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing evidence snapshot: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
