package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func dbConnectionRouter(t *testing.T) (*gin.Engine, *service.Service, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.New(repository.NewStore(openHandlerTestDB(t)), ragflow.NewMock(), nil, "test-encryption-key")
	tenant, err := svc.CreateTenant(t.Context(), "db-connection-handler")
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
	RegisterDBConnectionRoutes(group)
	return router, svc, tenant.ID
}

func TestDBConnectionRoutesHideSecretReference(t *testing.T) {
	router, _, tenantID := dbConnectionRouter(t)
	payload, _ := json.Marshal(map[string]interface{}{
		"name": "Risk DB", "driver": model.DBDriverSQLite, "dsn_ref": "secret://tenant/risk-db",
		"authorization_scope": "{\"roles\":[\"platform_admin\",\"tenant_admin\"]}",
		"max_open_conns":      5, "max_idle_conns": 1,
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/db-connections", bytes.NewReader(payload)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("create db connection: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret://") || strings.Contains(recorder.Body.String(), "dsn_ref") {
		t.Fatalf("create response leaked secret reference: %s", recorder.Body.String())
	}
	var created struct {
		Data model.DBConnection `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/db-connections?driver=sqlite&health_status=unknown", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list db connections: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret://") || strings.Contains(recorder.Body.String(), "dsn_ref") {
		t.Fatalf("list response leaked secret reference: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/db-connections/"+created.Data.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get db connection: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret://") || strings.Contains(recorder.Body.String(), "dsn_ref") {
		t.Fatalf("detail response leaked secret reference: %s", recorder.Body.String())
	}
	_ = tenantID
}

func TestDBConnectionTestRouteProjectsStatusWithoutLeakingConnection(t *testing.T) {
	router, svc, tenantID := dbConnectionRouter(t)
	sqlite, err := svc.CreateDBConnection(t.Context(), tenantID, "admin", service.DBConnectionInput{
		Name: "SQLite", Driver: model.DBDriverSQLite, DSNRef: "secret://tenant/risk-db",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	unreachable, err := svc.CreateDBConnection(t.Context(), tenantID, "admin", service.DBConnectionInput{
		Name: "Unreachable", Driver: model.DBDriverPostgres, DSNRef: "vault://tenant/unreachable",
		AuthorizationScope: `{"roles":["platform_admin","tenant_admin"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := filepath.Join(t.TempDir(), "risk.db")
	svc.SecretResolver = service.DBConnectionSecretResolverFunc(func(_ context.Context, connection *model.DBConnection) (string, error) {
		if connection.ID == sqlite.ID {
			return resolved, nil
		}
		return "postgres://user:super-secret@localhost:1/risk", nil
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/db-connections/"+sqlite.ID+"/test", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthy test: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"healthy"`) || strings.Contains(recorder.Body.String(), resolved) {
		t.Fatalf("unexpected healthy response: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/db-connections/"+unreachable.ID+"/test", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("unhealthy test: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "super-secret") || strings.Contains(recorder.Body.String(), "postgres://") || strings.Contains(recorder.Body.String(), "vault://") {
		t.Fatalf("unhealthy response leaked connection data: %s", recorder.Body.String())
	}
}
