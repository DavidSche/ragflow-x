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
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func queryTemplateRouter(t *testing.T) (*gin.Engine, *service.Service, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "query-template-handler")
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
	RegisterQueryTemplateRoutes(group)
	return router, svc, tenant.ID
}

func TestQueryTemplateRoutes(t *testing.T) {
	router, svc, tenantID := queryTemplateRouter(t)
	connection, err := svc.CreateDBConnection(t.Context(), tenantID, "admin", service.DBConnectionInput{
		Name: "Risk DB", Driver: model.DBDriverSQLite, DSNRef: "secret://tenant/risk-db",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"name": "Risk exposure", "sql_template": "SELECT tenant_id, exposure FROM risk_exposure WHERE tenant_id = :tenant_id",
		"parameter_schema":    `{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		"result_schema":       `{"type":"object","required":["exposure"],"properties":{"exposure":{"type":"number"}}}`,
		"execution_policy":    `{"read_only":true,"max_rows":1000,"timeout_ms":5000,"allowed_tables":["risk_exposure"],"allowed_columns":{"risk_exposure":["exposure","tenant_id"]}}`,
		"authorization_scope": `{"roles":["tenant_admin"]}`, "connection_id": connection.ID,
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/query-templates", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create query template: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	created := decodeObject[model.QueryTemplate](t, recorder)
	if created.ID == "" || created.ConnectionID != connection.ID || !created.Active {
		t.Fatalf("unexpected created query template: %+v", created)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/query-templates?active=true&connection_id="+connection.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list query templates: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	list := decodeObject[struct {
		Items []model.QueryTemplate `json:"items"`
		Total int64                 `json:"total"`
	}](t, recorder)
	if len(list.Items) != 1 || list.Total != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("unexpected query template list: %+v", list)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/query-templates/"+created.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get query template: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	inactive := false
	payload, _ = json.Marshal(map[string]interface{}{
		"name": "Risk exposure", "sql_template": "SELECT exposure, tenant_id FROM risk_exposure WHERE tenant_id = :tenant_id",
		"parameter_schema":    `{"type":"object","required":["tenant_id"],"properties":{"tenant_id":{"type":"string"}}}`,
		"result_schema":       `{"type":"object","required":["exposure"],"properties":{"exposure":{"type":"number"}}}`,
		"execution_policy":    `{"read_only":true,"max_rows":100,"timeout_ms":2000,"allowed_tables":["risk_exposure"],"allowed_columns":{"risk_exposure":["exposure","tenant_id"]}}`,
		"authorization_scope": `{"roles":["tenant_admin"]}`, "connection_id": connection.ID, "active": inactive,
	})
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/query-templates/"+created.ID, bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("update query template: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated := decodeObject[model.QueryTemplate](t, recorder)
	if updated.Active {
		t.Fatalf("unexpected updated query template: %+v", updated)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/query-templates/"+created.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete query template: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/query-templates/"+created.ID, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted query template: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
