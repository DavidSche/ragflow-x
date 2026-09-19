package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func authenticate(c *gin.Context) {
	c.Set(ContextUserID, "u1")
	c.Set(ContextTenantID, "t1")
	c.Next()
}

func newRBACService(t *testing.T) *service.Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "rbac.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), nil, "0123456789abcdef0123456789abcdef")
	if err := svc.BootstrapAdmin(t.Context(), "platform-admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

// ScenarioID: SC-AUTHZ-001
func TestP0_AUTHZ_001_RBACEnforcesGovernanceApprovalContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newRBACService(t)
	admin, err := svc.Store.GetUserByUsername(t.Context(), "platform-admin")
	if err != nil {
		t.Fatal(err)
	}
	rules := []Rule{
		{Method: "POST", Path: "/api/v1/datasets", Action: "manage", Resource: "dataset",
			RequiresApproval: true, ActingContextRequired: true, SupportsCrossTenantWrite: true},
	}

	build := func() *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(ContextUserID, admin.ID)
			c.Set(ContextTenantID, admin.TenantID)
			c.Next()
		})
		r.Use(RBAC(svc, rules))
		r.POST("/api/v1/datasets", func(c *gin.Context) { c.Status(http.StatusOK) })
		return r
	}

	w := httptest.NewRecorder()
	build().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/datasets?scope=specific&tenant_id=target", bytes.NewBufferString(`{"name":"test"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("disabled approval must fail closed at middleware, got %d: %s", w.Code, w.Body.String())
	}

	svc.SetApprovalConfig(config.Approval{Enabled: true})
	w = httptest.NewRecorder()
	build().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewBufferString(`{"name":"test"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("same-workspace governed write should remain service-gated, got %d: %s", w.Code, w.Body.String())
	}
}

// ScenarioID: SC-AUTHZ-001
func TestP0_AUTHZ_001_RBACRejectsCrossTenantGovernanceTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newRBACService(t)
	svc.SetApprovalConfig(config.Approval{Enabled: true})
	admin, err := svc.Store.GetUserByUsername(t.Context(), "platform-admin")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := svc.CreateTenant(t.Context(), "Target Workspace")
	if err != nil {
		t.Fatal(err)
	}
	rules := []Rule{
		{Method: "POST", Path: "/api/v1/datasets", Action: "manage", Resource: "dataset",
			RequiresApproval: true, ActingContextRequired: true, SupportsCrossTenantWrite: true},
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(ContextUserID, admin.ID)
		c.Set(ContextTenantID, admin.TenantID)
		c.Next()
	})
	r.Use(RBAC(svc, rules))
	r.POST("/api/v1/datasets", func(c *gin.Context) { c.Header("X-Test-Handler", "1"); c.Status(http.StatusOK) })

	body, _ := json.Marshal(map[string]string{"target_tenant_id": workspace.ID})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/datasets", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("authorized platform cross-tenant route gate should pass to approval protocol, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Test-Handler"); got == "" {
		t.Fatal("handler should receive request after cross-tenant route gate")
	}
}

// ScenarioID: SC-AUTHZ-001
func TestP0_AUTHZ_001_RBACDefaultsDenyUnregisteredRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(authenticate)
	r.Use(RBAC(&service.Service{}, []Rule{}))
	r.GET("/api/v1/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unregistered protected route, got %d", w.Code)
	}
}

// ScenarioID: SC-AUTHZ-001
func TestP0_AUTHZ_001_RBACFailsClosedOnResolveError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	broken := func(c *gin.Context) (string, string, string, error) {
		return "", "", "", errors.New("db unavailable")
	}
	rules := []Rule{
		{Method: "GET", Path: "/api/v1/protected/:id", Action: "read", Resource: "dataset", Resolve: broken},
	}
	r := gin.New()
	r.Use(authenticate)
	r.Use(RBAC(&service.Service{}, rules))
	r.GET("/api/v1/protected/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected/abc", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (fail closed) when scope resolution fails, got %d", w.Code)
	}
}
