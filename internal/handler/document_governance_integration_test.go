package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// governanceAccessRules is the doc/224 T2 route subset used by integration
// tests. Defined here (rather than importing the router package) to avoid
// import cycles.
func governanceAccessRules() []middleware.Rule {
	return []middleware.Rule{
		{Method: "GET", Path: "/api/v1/datasets/:id/documents/:docId/governance", Action: "read", Resource: "document-governance"},
		{Method: "PUT", Path: "/api/v1/datasets/:id/documents/:docId/governance", Action: "manage", Resource: "document-governance"},
		{Method: "DELETE", Path: "/api/v1/datasets/:id/documents/:docId/governance", Action: "manage", Resource: "document-governance"},
		{Method: "POST", Path: "/api/v1/datasets/:id/documents/:docId/governance/:decision", Action: "manage", Resource: "document-governance"},
		{Method: "GET", Path: "/api/v1/documents/governance", Action: "read", Resource: "document-governance"},
		{Method: "GET", Path: "/api/v1/users/:id/clearance", Action: "read", Resource: "user-clearance"},
		{Method: "PUT", Path: "/api/v1/users/:id/clearance", Action: "manage", Resource: "user-clearance"},
	}
}

type governanceTestEnv struct {
	svc          *service.Service
	h            *Handler
	router       *gin.Engine
	jwtMgr       *jwt.Manager
	tenantID     string
	adminID      string
	memberID     string
	datasetID    string
}

func setupGovernanceTestEnv(t *testing.T) *governanceTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "gov_handler_test.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	jwtSecret := "test-jwt-secret-for-governance-tests-32b"
	jwtMgr := jwt.NewManager(jwtSecret, 24)
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwtMgr, "test-encrypt-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	h := New(svc)

	r := gin.New()
	r.Use(middleware.Auth(svc.JWT, svc))
	r.Use(middleware.RBAC(svc, governanceAccessRules()))
	protected := r.Group("/api/v1")
	{
		protected.GET("/datasets/:id/documents/:docId/governance", h.GetDocumentGovernance)
		protected.PUT("/datasets/:id/documents/:docId/governance", h.SetDocumentGovernance)
		protected.DELETE("/datasets/:id/documents/:docId/governance", h.DeleteDocumentGovernance)
		protected.POST("/datasets/:id/documents/:docId/governance/:decision", h.ApproveDocumentGovernance)
		protected.GET("/documents/governance", h.ListDocumentGovernance)
		protected.GET("/users/:id/clearance", h.GetUserClearance)
		protected.PUT("/users/:id/clearance", h.SetUserClearance)
	}

	ctx := context.Background()
	admin, err := svc.CreateUser(ctx, model.PlatformTenantID, string(model.RolePlatformAdmin), service.CreateUserRequest{
		Username: "gv-admin-" + id.New()[:8], Password: "password123", Role: model.RolePlatformAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(ctx, "gv-tenant-"+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	governor, err := svc.CreateUser(ctx, tenant.ID, admin.ID, service.CreateUserRequest{
		Username: "gv-governor-" + id.New()[:8], Password: "password123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := svc.CreateUser(ctx, tenant.ID, admin.ID, service.CreateUserRequest{
		Username: "gv-member-" + id.New()[:8], Password: "password123", Role: model.RoleTeamAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := svc.CreateDataset(ctx, tenant.ID, "gv-dataset-"+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	return &governanceTestEnv{
		svc: svc, h: h, router: r, jwtMgr: jwtMgr,
		tenantID: tenant.ID, adminID: governor.ID, memberID: member.ID, datasetID: ds.ID,
	}
}

func (e *governanceTestEnv) tokenFor(t *testing.T, userID, role string) string {
	t.Helper()
	token, err := e.jwtMgr.Issue(userID, e.tenantID, "testuser", role)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (e *governanceTestEnv) doRequest(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var req *http.Request
	if body != nil {
		raw, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytesReader(raw))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	e.router.ServeHTTP(rec, req)
	return rec
}

func bytesReader(b []byte) *bytes.Buffer { return bytes.NewBuffer(b) }

func decodeOK(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	data, _ := envelope["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("expected data envelope, got %s", rec.Body.String())
	}
	return data
}

func TestDocumentGovernanceHTTPLifecycle(t *testing.T) {
	env := setupGovernanceTestEnv(t)
	govToken := env.tokenFor(t, env.adminID, "tenant_admin")
	memberToken := env.tokenFor(t, env.memberID, "team_admin")

	// No token → 401.
	rec := env.doRequest(t, http.MethodGet, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// Member PUT manage action → 403 at RBAC.
	rec = env.doRequest(t, http.MethodPut, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance",
		map[string]interface{}{"level": "confidential"}, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member PUT expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Governor sets a top_secret annotation → immediately approved.
	rec = env.doRequest(t, http.MethodPut, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance",
		map[string]interface{}{"level": "top_secret"}, govToken)
	out := decodeOK(t, rec)
	if out["approval_state"] != "approved" || out["level"] != "top_secret" {
		t.Fatalf("unexpected created record: %+v", out)
	}

	// GET single reflects it.
	rec = env.doRequest(t, http.MethodGet, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance", nil, govToken)
	out = decodeOK(t, rec)
	if out["document_id"] != "doc-http" {
		t.Fatalf("unexpected GET: %+v", out)
	}

	// Member POST approve is manage action → 403 RBAC before business check.
	rec = env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance/approve", nil, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member approve expected 403, got %d", rec.Code)
	}

	// Approving an already-decided record → 409 conflict.
	rec = env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance/approve", nil, govToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double approve expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}

	// DELETE.
	rec = env.doRequest(t, http.MethodDelete, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance", nil, govToken)
	decodeOK(t, rec)

	// GET after delete → 404.
	rec = env.doRequest(t, http.MethodGet, "/api/v1/datasets/"+env.datasetID+"/documents/doc-http/governance", nil, govToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rec.Code)
	}
}

func TestDocumentGovernanceListAndApprovalWorkbench(t *testing.T) {
	env := setupGovernanceTestEnv(t)
	govToken := env.tokenFor(t, env.adminID, "tenant_admin")
	memberToken := env.tokenFor(t, env.memberID, "team_admin")

	for _, doc := range []string{"doc-l1", "doc-l2"} {
		rec := env.doRequest(t, http.MethodPut, "/api/v1/datasets/"+env.datasetID+"/documents/"+doc+"/governance",
			map[string]interface{}{"level": "internal"}, govToken)
		decodeOK(t, rec)
	}
	// Member creates a pending top_secret (approval goes through service, RBAC
	// allows execute via manage? member PUT blocked at RBAC manage previously —
	// T2 决策：annotation 写入本身也要求 governor；非 governor 经由 approve_pending 工作台触发
	// 这里只验证成员不能直接 manage 写入 → 403。
	rec := env.doRequest(t, http.MethodPut, "/api/v1/datasets/"+env.datasetID+"/documents/doc-l3/governance",
		map[string]interface{}{"level": "top_secret"}, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member annotation write expected 403, got %d", rec.Code)
	}

	rec = env.doRequest(t, http.MethodGet, "/api/v1/documents/governance?dataset_id="+env.datasetID+"&level=internal", nil, govToken)
	out := decodeOK(t, rec)
	items, _ := out["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 internal annotations, got %d: %s", len(items), rec.Body.String())
	}
}

func TestDocumentGovernanceClearanceEndpoints(t *testing.T) {
	env := setupGovernanceTestEnv(t)
	govToken := env.tokenFor(t, env.adminID, "tenant_admin")
	memberToken := env.tokenFor(t, env.memberID, "team_admin")

	// Default clearance is internal even when unrecorded.
	rec := env.doRequest(t, http.MethodGet, "/api/v1/users/me/clearance", nil, memberToken)
	out := decodeOK(t, rec)
	if out["level"] != "internal" || out["recorded"] != false {
		t.Fatalf("unexpected default clearance: %+v", out)
	}

	// Member cannot elevate anyone (manage action → 403).
	rec = env.doRequest(t, http.MethodPut, "/api/v1/users/"+env.memberID+"/clearance",
		map[string]interface{}{"level": "top_secret"}, memberToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member clearance write expected 403, got %d", rec.Code)
	}

	// Governor elevates; member then sees recorded elevated level.
	rec = env.doRequest(t, http.MethodPut, "/api/v1/users/"+env.memberID+"/clearance",
		map[string]interface{}{"level": "confidential"}, govToken)
	decodeOK(t, rec)

	rec = env.doRequest(t, http.MethodGet, "/api/v1/users/"+env.memberID+"/clearance", nil, govToken)
	out = decodeOK(t, rec)
	if out["level"] != "confidential" || out["recorded"] != true {
		t.Fatalf("unexpected elevated clearance: %+v", out)
	}
}
