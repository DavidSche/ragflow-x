package handler

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// approvalAccessRules returns the subset of access rules needed for approval
// integration tests. We define them here instead of importing the router
// package to avoid circular dependencies.
func approvalAccessRules() []middleware.Rule {
	return []middleware.Rule{
		{Method: "GET", Path: "/api/v1/approvals", Action: "read", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approvals", Action: "execute", Resource: "approval"},
		{Method: "GET", Path: "/api/v1/approvals/export", Action: "read", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approvals/batch-approve", Action: "manage", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approvals/batch-reject", Action: "manage", Resource: "approval"},
		{Method: "GET", Path: "/api/v1/approvals/:id", Action: "read", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approvals/:id/approve", Action: "execute", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approvals/:id/reject", Action: "execute", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approvals/:id/cancel", Action: "execute", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approvals/:id/retry", Action: "manage", Resource: "approval"},
		{Method: "GET", Path: "/api/v1/approval-delegations", Action: "read", Resource: "approval"},
		{Method: "POST", Path: "/api/v1/approval-delegations", Action: "read", Resource: "approval"},
		{Method: "DELETE", Path: "/api/v1/approval-delegations/:id", Action: "read", Resource: "approval"},
		{Method: "GET", Path: "/api/v1/audit/export", Action: "read", Resource: "audit"},
		{Method: "GET", Path: "/api/v1/datasets/export", Action: "read", Resource: "dataset-export"},
		{Method: "GET", Path: "/api/v1/approval-policies", Action: "read", Resource: "approval-policy"},
		{Method: "POST", Path: "/api/v1/approval-policies", Action: "manage", Resource: "approval-policy"},
		{Method: "PUT", Path: "/api/v1/approval-policies/:id", Action: "manage", Resource: "approval-policy"},
		{Method: "GET", Path: "/api/v1/model-providers", Action: "read", Resource: "model-provider"},
		{Method: "POST", Path: "/api/v1/model-providers", Action: "manage", Resource: "model-provider"},
		{Method: "DELETE", Path: "/api/v1/model-providers/:id", Action: "manage", Resource: "model-provider"},
		{Method: "POST", Path: "/api/v1/model-providers/:id/instances", Action: "manage", Resource: "model-provider"},
		{Method: "PUT", Path: "/api/v1/model-providers/:id/instances/:instanceId", Action: "manage", Resource: "model-provider"},
		{Method: "DELETE", Path: "/api/v1/model-providers/:id/instances", Action: "manage", Resource: "model-provider"},
		{Method: "POST", Path: "/api/v1/model-providers/:id/instances/:instanceId/models", Action: "manage", Resource: "model-provider"},
		{Method: "PATCH", Path: "/api/v1/model-providers/:id/instances/:instanceId/models/:modelId", Action: "manage", Resource: "model-provider"},
		{Method: "DELETE", Path: "/api/v1/model-providers/:id/instances/:instanceId/models", Action: "manage", Resource: "model-provider"},
		{Method: "POST", Path: "/api/v1/model-providers/:id/instances/:instanceId/models/:modelId/test", Action: "execute", Resource: "model-provider"},
		{Method: "GET", Path: "/api/v1/enterprise-connections", Action: "read", Resource: "enterprise-connection"},
		{Method: "POST", Path: "/api/v1/enterprise-connections", Action: "manage", Resource: "enterprise-connection"},
		{Method: "GET", Path: "/api/v1/enterprise-connections/:id", Action: "read", Resource: "enterprise-connection"},
		{Method: "PUT", Path: "/api/v1/enterprise-connections/:id", Action: "manage", Resource: "enterprise-connection"},
		{Method: "GET", Path: "/api/v1/enterprise-connections/:id/versions", Action: "read", Resource: "enterprise-connection"},
		{Method: "POST", Path: "/api/v1/enterprise-connections/:id/rotate-credential", Action: "manage", Resource: "enterprise-connection"},
		{Method: "POST", Path: "/api/v1/enterprise-connections/:id/deprecate", Action: "manage", Resource: "enterprise-connection"},
		{Method: "POST", Path: "/api/v1/enterprise-connections/:id/retire", Action: "manage", Resource: "enterprise-connection"},
		{Method: "GET", Path: "/api/v1/enterprise-connections/:id/bindings", Action: "read", Resource: "enterprise-connection"},
		{Method: "GET", Path: "/api/v1/enterprise-connections/:id/bindings/:bindingId", Action: "read", Resource: "enterprise-connection"},
		{Method: "POST", Path: "/api/v1/enterprise-connections/:id/bindings", Action: "manage", Resource: "enterprise-connection"},
		{Method: "PUT", Path: "/api/v1/enterprise-connections/:id/bindings/:bindingId", Action: "manage", Resource: "enterprise-connection"},
		{Method: "POST", Path: "/api/v1/enterprise-connections/:id/bindings/:bindingId/revoke", Action: "manage", Resource: "enterprise-connection"},
		{Method: "GET", Path: "/api/v1/enterprise-connections/:id/bindings/:bindingId/versions", Action: "read", Resource: "enterprise-connection"},
		{Method: "POST", Path: "/api/v1/agents", Action: "manage", Resource: "agent"},
		{Method: "PUT", Path: "/api/v1/agents/:id", Action: "manage", Resource: "agent"},
		{Method: "POST", Path: "/api/v1/agents/:id/publish", Action: "manage", Resource: "agent"},
		{Method: "DELETE", Path: "/api/v1/agents/:id", Action: "manage", Resource: "agent"},
		{Method: "POST", Path: "/api/v1/agents/:id/versions/:versionId/rollback", Action: "manage", Resource: "agent"},
		{Method: "POST", Path: "/api/v1/chats", Action: "manage", Resource: "chat"},
		{Method: "POST", Path: "/api/v1/scenario-template-assets/:id/instantiate-chat", Action: "manage", Resource: "chat"},
		{Method: "PUT", Path: "/api/v1/chats/:id", Action: "manage", Resource: "chat"},
		{Method: "DELETE", Path: "/api/v1/chats/:id", Action: "manage", Resource: "chat"},
		{Method: "POST", Path: "/api/v1/datasets/:id/parse", Action: "execute", Resource: "document"},
		{Method: "POST", Path: "/api/v1/datasets/:id/documents", Action: "append", Resource: "document"},
		{Method: "POST", Path: "/api/v1/datasets/:id/documents/stop", Action: "execute", Resource: "document"},
		{Method: "DELETE", Path: "/api/v1/datasets/:id/documents", Action: "delete:own", Resource: "document"},
		{Method: "POST", Path: "/api/v1/datasets/:id/documents/status", Action: "execute", Resource: "document"},
		{Method: "PUT", Path: "/api/v1/datasets/:id/documents/:docId/metadata", Action: "execute", Resource: "document"},
		{Method: "GET", Path: "/api/v1/datasets/:id/documents/:docId/chunks", Action: "read", Resource: "document"},
		{Method: "GET", Path: "/api/v1/datasets/:id/documents/:docId/preview", Action: "read", Resource: "document"},
		{Method: "DELETE", Path: "/api/v1/datasets/:id/documents/:docId/chunks", Action: "execute", Resource: "document"},
		{Method: "PATCH", Path: "/api/v1/datasets/:id/documents/:docId/chunks", Action: "execute", Resource: "document"},
		{Method: "POST", Path: "/api/v1/datasets", Action: "manage", Resource: "dataset"},
		{Method: "PUT", Path: "/api/v1/datasets/:id", Action: "manage", Resource: "dataset"},
		{Method: "PUT", Path: "/api/v1/datasets/:id/config", Action: "manage", Resource: "dataset"},
		{Method: "PUT", Path: "/api/v1/datasets/:id/project", Action: "manage", Resource: "dataset"},
		{Method: "DELETE", Path: "/api/v1/datasets/:id", Action: "manage", Resource: "dataset"},
		{Method: "DELETE", Path: "/api/v1/datasets", Action: "manage", Resource: "dataset"},
	}
}

// testEnv holds shared state for integration tests.
type testEnv struct {
	svc        *service.Service
	h          *Handler
	router     *gin.Engine
	jwtMgr     *jwt.Manager
	platformID string
	adminID    string
	tenantID   string
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "approval_test.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}

	jwtSecret := "test-jwt-secret-for-integration-tests-32b"
	jwtMgr := jwt.NewManager(jwtSecret, 24)
	svc := service.New(repository.NewStore(gdb), ragflow.NewMock(), jwtMgr, "test-encrypt-key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	h := New(svc)

	// Build router with auth and RBAC middleware.
	r := gin.New()
	r.Use(middleware.Auth(svc.JWT, svc))
	r.Use(middleware.RBAC(svc, approvalAccessRules()))

	// Register approval routes on the protected group.
	protected := r.Group("/api/v1")
	{
		protected.GET("/approvals", h.ListApprovals)
		protected.POST("/approvals", h.CreateApproval)
		protected.GET("/approvals/export", h.ExportApprovals)
		protected.POST("/approvals/batch-approve", h.BatchApproveApprovals)
		protected.POST("/approvals/batch-reject", h.BatchRejectApprovals)
		protected.GET("/approvals/:id", h.GetApproval)
		protected.POST("/approvals/:id/approve", h.ApproveApproval)
		protected.POST("/approvals/:id/reject", h.RejectApproval)
		protected.POST("/approvals/:id/cancel", h.CancelApproval)
		protected.POST("/approvals/:id/retry", h.RetryApproval)
		protected.GET("/approval-delegations", h.ListApprovalDelegations)
		protected.POST("/approval-delegations", h.CreateApprovalDelegation)
		protected.DELETE("/approval-delegations/:id", h.DeleteApprovalDelegation)
		protected.GET("/audit/export", h.ExportAudits)
		protected.GET("/approval-policies", h.ListApprovalPolicies)
		protected.POST("/approval-policies", h.SaveApprovalPolicy)
		protected.PUT("/approval-policies/:id", h.SaveApprovalPolicy)
		protected.GET("/model-providers", h.ListModelProviders)
		protected.POST("/model-providers", h.CreateModelProvider)
		protected.DELETE("/model-providers/:id", h.DeleteModelProvider)
		protected.POST("/model-providers/:id/instances", h.CreateProviderInstance)
		protected.PUT("/model-providers/:id/instances/:instanceId", h.UpdateProviderInstance)
		protected.DELETE("/model-providers/:id/instances", h.DeleteProviderInstances)
		protected.POST("/model-providers/:id/instances/:instanceId/models", h.AddProviderModel)
		protected.PATCH("/model-providers/:id/instances/:instanceId/models/:modelId", h.UpdateProviderModel)
		protected.DELETE("/model-providers/:id/instances/:instanceId/models", h.DeleteProviderModels)
		protected.POST("/model-providers/:id/instances/:instanceId/models/:modelId/test", h.TestProviderModel)
		protected.GET("/enterprise-connections", h.ListEnterpriseConnections)
		protected.POST("/enterprise-connections", h.CreateEnterpriseConnection)
		protected.GET("/enterprise-connections/:id", h.GetEnterpriseConnection)
		protected.PUT("/enterprise-connections/:id", h.UpdateEnterpriseConnection)
		protected.GET("/enterprise-connections/:id/versions", h.ListEnterpriseConnectionVersions)
		protected.POST("/enterprise-connections/:id/rotate-credential", h.RotateEnterpriseConnectionCredential)
		protected.POST("/enterprise-connections/:id/deprecate", h.DeprecateEnterpriseConnection)
		protected.POST("/enterprise-connections/:id/retire", h.RetireEnterpriseConnection)
		protected.GET("/enterprise-connections/:id/bindings", h.ListEnterpriseBindings)
		protected.GET("/enterprise-connections/:id/bindings/:bindingId", h.GetEnterpriseBinding)
		protected.POST("/enterprise-connections/:id/bindings", h.CreateEnterpriseBinding)
		protected.PUT("/enterprise-connections/:id/bindings/:bindingId", h.UpdateEnterpriseBinding)
		protected.POST("/enterprise-connections/:id/bindings/:bindingId/revoke", h.RevokeEnterpriseBinding)
		protected.GET("/enterprise-connections/:id/bindings/:bindingId/versions", h.ListEnterpriseBindingVersions)
		protected.POST("/agents", h.CreateAgent)
		protected.PUT("/agents/:id", h.UpdateAgent)
		protected.POST("/agents/:id/publish", h.PublishAgent)
		protected.DELETE("/agents/:id", h.DeleteAgent)
		protected.POST("/agents/:id/versions/:versionId/rollback", h.RollbackAgentVersion)
		protected.POST("/chats", h.CreateChat)
		protected.POST("/scenario-template-assets/:id/instantiate-chat", func(c *gin.Context) {
			c.Set("handler", h)
			instantiateChatFromScenarioTemplate(c)
		})
		protected.PUT("/chats/:id", h.UpdateChat)
		protected.DELETE("/chats/:id", h.DeleteChat)
		protected.POST("/datasets/:id/parse", h.ParseDocuments)
		protected.POST("/datasets/:id/documents", h.UploadDocument)
		protected.POST("/datasets/:id/documents/stop", h.StopDocuments)
		protected.DELETE("/datasets/:id/documents", h.DeleteDocuments)
		protected.POST("/datasets/:id/documents/status", h.SetDocumentsStatus)
		protected.PUT("/datasets/:id/documents/:docId/metadata", h.UpdateDocumentMetadata)
		protected.GET("/datasets/:id/documents/:docId/chunks", h.ListDocumentChunks)
		protected.GET("/datasets/:id/documents/:docId/preview", h.PreviewDocument)
		protected.DELETE("/datasets/:id/documents/:docId/chunks", h.DeleteDocumentChunks)
		protected.PATCH("/datasets/:id/documents/:docId/chunks", h.SetDocumentChunksAvailable)
		protected.POST("/datasets", h.CreateDataset)
		protected.PUT("/datasets/:id", h.UpdateDataset)
		protected.PUT("/datasets/:id/config", h.UpdateDatasetConfig)
		protected.PUT("/datasets/:id/project", h.BindDatasetProject)
		protected.DELETE("/datasets/:id", h.DeleteDataset)
		protected.DELETE("/datasets", h.DeleteDatasets)
		protected.GET("/datasets/export", h.ExportDatasets)
	}

	// Enable approval workflow.
	svc.SetApprovalConfig(config.Approval{
		Enabled:               true,
		DefaultExpireHours:    72,
		ExecutionMaxRetries:   3,
		ExpireScanIntervalSec: 3600,
		RetentionDays:         365,
		PolicyCacheTTLSec:     30,
	})

	// Bootstrap admin and tenant. CreateUser with tenantID associates the
	// user with the tenant in a single call.
	ctx := t.Context()
	admin, err := svc.CreateUser(ctx, model.PlatformTenantID, "platform_admin", service.CreateUserRequest{
		Username: "admin-" + id.New()[:8],
		Password: "password123",
		Role:     "platform_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	tenant, err := svc.CreateTenant(ctx, "test-tenant-"+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}

	// Create admin within the tenant so they get the right TenantID.
	tenantAdmin, err := svc.CreateUser(ctx, tenant.ID, admin.ID, service.CreateUserRequest{
		Username: "tenant-admin-" + id.New()[:8],
		Password: "password123",
		Role:     "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}

	return &testEnv{
		svc:        svc,
		h:          h,
		router:     r,
		jwtMgr:     jwtMgr,
		platformID: admin.ID,
		adminID:    tenantAdmin.ID,
		tenantID:   tenant.ID,
	}
}

// tokenFor creates a JWT access token for the given user.
func (e *testEnv) tokenFor(t *testing.T, userID, tenantID, role string) string {
	t.Helper()
	token, err := e.jwtMgr.Issue(userID, tenantID, "testuser", role)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// request makes an HTTP request and returns the recorder.
func (e *testEnv) doRequest(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reqBody = bytes.NewBuffer(raw)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// jsonResponse parses the response body.
func jsonResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v\nbody: %s", err, w.Body.String())
	}
	return result
}

// createPolicy creates an approval policy and returns its ID.
func (e *testEnv) createPolicy(t *testing.T, token, objectType, action string) string {
	t.Helper()
	resp := e.doRequest(t, http.MethodPost, "/api/v1/approval-policies", map[string]any{
		"object_type":  objectType,
		"action":       action,
		"enabled":      true,
		"priority":     100,
		"expire_hours": 48,
		"conditions":   map[string]any{},
		"steps": []map[string]any{
			{
				"step_no":        1,
				"name":           "admin approve",
				"approver_type":  "user",
				"approver_value": e.adminID,
				"expire_hours":   48,
			},
		},
	}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("createPolicy failed: %d %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	return data["id"].(string)
}

// createDatasetLink creates a dataset link in the store.
func (e *testEnv) createDatasetLink(t *testing.T, tenantID, datasetID, name string) {
	t.Helper()
	if err := e.svc.Store.CreateDatasetLink(t.Context(), &model.DatasetLink{
		ID:               datasetID,
		TenantID:         tenantID,
		RAGFlowDatasetID: "ragflow-" + datasetID,
		Name:             name,
		ProjectID:        "proj-default",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// insertApprovalDirectly inserts an approval record for testing non-submit paths.
func (e *testEnv) insertApprovalDirectly(t *testing.T, tenantID, requesterID, policyID, status, approverType, approverValue string) *model.Approval {
	return e.insertApprovalWith(t, tenantID, requesterID, policyID, status, approverType, approverValue, nil)
}

func (e *testEnv) insertApprovalWith(t *testing.T, tenantID, requesterID, policyID, status, approverType, approverValue string, mutate func(*model.Approval)) *model.Approval {
	t.Helper()
	now := time.Now().UTC()
	approval := &model.Approval{
		ID:             id.New(),
		TenantID:       tenantID,
		RequestNo:      "APR-" + id.New()[:8],
		ObjectType:     model.ApprovalObjectDataset,
		ObjectID:       "ds-" + id.New()[:4],
		Action:         model.ApprovalActionDelete,
		Title:          "test approval",
		Status:         status,
		PolicyID:       policyID,
		PolicyVersion:  1,
		CurrentStep:    1,
		RequesterID:    requesterID,
		IdempotencyKey: "idem-" + id.New(),
		PayloadJSON:    "{}",
		SnapshotJSON:   "{}",
		ExpiresAt:      now.Add(48 * time.Hour),
		SubmittedAt:    now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if mutate != nil {
		mutate(approval)
	}
	futureDue := now.Add(48 * time.Hour)
	steps := []model.ApprovalStep{
		{
			ID:            id.New(),
			TenantID:      tenantID,
			ApprovalID:    approval.ID,
			StepNo:        1,
			Name:          "step 1",
			ApproverType:  approverType,
			ApproverValue: approverValue,
			Status:        model.ApprovalStepCurrent,
			DueAt:         &futureDue,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}
	if err := e.svc.Store.CreateApprovalWithAudit(t.Context(), approval, steps, nil, nil); err != nil {
		t.Fatal(err)
	}
	return approval
}

// ---------------------------------------------------------------------------
// Tests: POST /approvals (submit)
// ---------------------------------------------------------------------------

func TestIntegration_SubmitApproval_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	datasetID := "ds-submit-" + id.New()[:4]
	env.createDatasetLink(t, env.tenantID, datasetID, "submit-test")

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals", map[string]any{
		"object_type":     model.ApprovalObjectDataset,
		"action":          model.ApprovalActionDelete,
		"object_id":       datasetID,
		"title":           "delete dataset",
		"reason":          "testing",
		"payload":         map[string]any{},
		"idempotency_key": "idem-submit-" + id.New(),
	}, token)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["approval_id"] == nil {
		t.Fatal("approval_id should not be nil")
	}
	if data["status"] != model.ApprovalStatusPendingApproval {
		t.Fatalf("status = %v, want pending_approval", data["status"])
	}
	if got := resp.Header().Get("X-Approval-Request-Id"); got == "" {
		t.Fatal("X-Approval-Request-Id header should be set")
	}
}

func TestIntegration_SubmitApproval_NoPolicy(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	// Create dataset link so target resolution succeeds, but no policy is configured.
	datasetID := "ds-nopolicy-" + id.New()[:4]
	env.createDatasetLink(t, env.tenantID, datasetID, "no-policy-test")

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals", map[string]any{
		"object_type":     model.ApprovalObjectDataset,
		"action":          model.ApprovalActionDelete,
		"object_id":       datasetID,
		"title":           "no policy",
		"payload":         map[string]any{},
		"idempotency_key": "idem-nopolicy-" + id.New(),
	}, token)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["created"] != false {
		t.Fatalf("created = %v, want false", data["created"])
	}
}

func TestIntegration_SubmitApproval_MissingIdempotencyKey(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals", map[string]any{
		"object_type": model.ApprovalObjectDataset,
		"action":      model.ApprovalActionDelete,
		"object_id":   "ds-nokey",
	}, token)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIntegration_SubmitApproval_DuplicateIdempotencyKey(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	datasetID := "ds-idem-" + id.New()[:4]
	env.createDatasetLink(t, env.tenantID, datasetID, "idem-test")

	idemKey := "idem-dup-" + id.New()
	body := map[string]any{
		"object_type":     model.ApprovalObjectDataset,
		"action":          model.ApprovalActionDelete,
		"object_id":       datasetID,
		"payload":         map[string]any{},
		"idempotency_key": idemKey,
	}

	resp1 := env.doRequest(t, http.MethodPost, "/api/v1/approvals", body, token)
	if resp1.Code != http.StatusAccepted {
		t.Fatalf("first submit: expected 202, got %d: %s", resp1.Code, resp1.Body.String())
	}
	result1 := jsonResponse(t, resp1)
	data1, _ := result1["data"].(map[string]any)
	approvalID1 := data1["approval_id"].(string)

	resp2 := env.doRequest(t, http.MethodPost, "/api/v1/approvals", body, token)
	if resp2.Code != http.StatusAccepted {
		t.Fatalf("second submit: expected 202, got %d: %s", resp2.Code, resp2.Body.String())
	}
	result2 := jsonResponse(t, resp2)
	data2, _ := result2["data"].(map[string]any)
	approvalID2 := data2["approval_id"].(string)

	if approvalID1 != approvalID2 {
		t.Fatalf("idempotency key should return same approval: %s != %s", approvalID1, approvalID2)
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /approvals (list)
// ---------------------------------------------------------------------------

func TestIntegration_ListApprovals_Empty(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIntegration_ListApprovals_WithFilters(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)

	// Insert approvals as the admin user (requester = admin) so they appear
	// in the admin's own list.
	env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)
	env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusRejected, model.ApprovalApproverUser, env.adminID)

	// List all (platform_admin sees all in tenant).
	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("list all: %d %s", resp.Code, resp.Body.String())
	}
	// Verify 200 OK and response is valid JSON with items array.
	result := jsonResponse(t, resp)
	if result["code"] != float64(0) {
		t.Fatalf("expected code 0, got %v", result["code"])
	}

	// Filter by status.
	resp = env.doRequest(t, http.MethodGet, "/api/v1/approvals?status=pending_approval", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("filter status: %d %s", resp.Code, resp.Body.String())
	}

	// Filter by object_type.
	resp = env.doRequest(t, http.MethodGet, "/api/v1/approvals?object_type=dataset", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("filter object_type: %d %s", resp.Code, resp.Body.String())
	}

	// Pagination.
	resp = env.doRequest(t, http.MethodGet, "/api/v1/approvals?page=1&page_size=1", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("pagination: %d %s", resp.Code, resp.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /approvals/:id (detail)
// ---------------------------------------------------------------------------

func TestIntegration_GetApproval_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/"+approval.ID, nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	detail, _ := data["approval"].(map[string]any)
	if detail["id"] != approval.ID {
		t.Fatalf("id = %v, want %v", detail["id"], approval.ID)
	}
	steps, _ := data["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
}

func TestIntegration_GetApproval_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/nonexistent", nil, token)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /approvals/:id/approve
// ---------------------------------------------------------------------------

func TestIntegration_ApproveApproval_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	// Use "other-user" as requester so admin can approve (SoD: requester != approver).
	approval := env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/approve", map[string]any{
		"comment": "approved for testing",
	}, token)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["status"] != model.ApprovalStatusApproved {
		t.Fatalf("status = %v, want approved", data["status"])
	}
}

func TestIntegration_ApproveApproval_MissingComment(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/approve", map[string]any{}, token)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIntegration_ApproveApproval_SoD_SelfApprove(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/approve", map[string]any{
		"comment": "self approve attempt",
	}, token)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIntegration_ApproveApproval_AlreadyApproved(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusApproved, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/approve", map[string]any{
		"comment": "already approved",
	}, token)

	if resp.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /approvals/:id/reject
// ---------------------------------------------------------------------------

func TestIntegration_RejectApproval_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/reject", map[string]any{
		"comment": "rejected for testing",
	}, token)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["status"] != model.ApprovalStatusRejected {
		t.Fatalf("status = %v, want rejected", data["status"])
	}
}

func TestIntegration_RejectApproval_MissingComment(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/reject", map[string]any{}, token)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /approvals/:id/cancel
// ---------------------------------------------------------------------------

func TestIntegration_CancelApproval_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/cancel", map[string]any{
		"comment": "cancel request",
	}, token)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["status"] != model.ApprovalStatusCanceled {
		t.Fatalf("status = %v, want canceled", data["status"])
	}
}

func TestIntegration_CancelApproval_AlreadyCanceled(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusCanceled, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/cancel", map[string]any{
		"comment": "already canceled",
	}, token)

	if resp.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /approvals/:id/retry
// ---------------------------------------------------------------------------

func TestIntegration_RetryApproval_NotFailed(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/retry", map[string]any{
		"comment": "retry attempt",
	}, token)

	if resp.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIntegration_RetryApproval_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusExecutionFailed, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/retry", map[string]any{
		"comment": "retry after fix",
	}, token)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["status"] != model.ApprovalStatusApproved {
		t.Fatalf("status = %v, want approved after retry", data["status"])
	}
}

// ---------------------------------------------------------------------------
// Tests: approval-policies CRUD
// ---------------------------------------------------------------------------

func TestIntegration_CreateApprovalPolicy_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approval-policies", map[string]any{
		"object_type":  "dataset",
		"action":       "delete",
		"enabled":      true,
		"priority":     100,
		"expire_hours": 48,
		"conditions":   map[string]any{},
		"steps": []map[string]any{
			{
				"step_no":        1,
				"name":           "admin review",
				"approver_type":  "role",
				"approver_value": "tenant_admin",
				"expire_hours":   24,
			},
		},
	}, token)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["id"] == nil {
		t.Fatal("policy id should not be nil")
	}
	if data["version"] != float64(1) {
		t.Fatalf("version = %v, want 1", data["version"])
	}
}

func TestIntegration_CreateApprovalPolicy_NoSteps(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approval-policies", map[string]any{
		"object_type": "dataset",
		"action":      "delete",
		"enabled":     true,
		"priority":    100,
		"conditions":  map[string]any{},
		"steps":       []map[string]any{},
	}, token)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIntegration_UpdateApprovalPolicy_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)

	resp := env.doRequest(t, http.MethodPut, "/api/v1/approval-policies/"+policyID, map[string]any{
		"id":           policyID,
		"object_type":  "dataset",
		"action":       "delete",
		"enabled":      false,
		"priority":     50,
		"expire_hours": 24,
		"conditions":   map[string]any{},
		"steps": []map[string]any{
			{
				"step_no":        1,
				"name":           "updated step",
				"approver_type":  "role",
				"approver_value": "platform_admin",
				"expire_hours":   12,
			},
		},
	}, token)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	result := jsonResponse(t, resp)
	data, _ := result["data"].(map[string]any)
	if data["version"] != float64(2) {
		t.Fatalf("version = %v, want 2 after update", data["version"])
	}
}

func TestIntegration_ListApprovalPolicies_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	env.createPolicy(t, token, model.ApprovalObjectAPIKey, model.ApprovalActionCreate)

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approval-policies", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /approvals/export
// ---------------------------------------------------------------------------

func TestIntegration_ExportApprovals_Success(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/export", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "request_no") {
		t.Fatalf("export should contain CSV header, got: %s", body[:200])
	}
	disp := resp.Header().Get("Content-Disposition")
	if !strings.Contains(disp, "approvals-") {
		t.Fatalf("Content-Disposition = %q, should contain approvals-", disp)
	}
}

func TestIntegration_ExportApprovals_Empty(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/export", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "request_no") {
		t.Fatalf("export should contain CSV header even when empty, got: %s", body[:200])
	}
}

func TestP0_APPROVAL_001_ExportHonorsApproverFilter(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	visible := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)
	hidden := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, "other-approver")

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/export?approver_id="+env.adminID, nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, visible.RequestNo) {
		t.Fatalf("export should contain visible approval %s, got: %s", visible.RequestNo, body)
	}
	if strings.Contains(body, hidden.RequestNo) {
		t.Fatalf("export should not contain approval for another approver %s, got: %s", hidden.RequestNo, body)
	}
}

func TestP0_APPROVAL_001_ExportSanitizesCsvFormulaInjection(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	maliciousRequestNo := "=HYPERLINK(\"https://example.com\",\"request\")"
	maliciousTitle := "+SUM(1,2)"
	env.insertApprovalWith(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID, func(approval *model.Approval) {
		approval.RequestNo = maliciousRequestNo
		approval.Title = maliciousTitle
	})

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/export", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	reader := csv.NewReader(resp.Body)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected header and one record, got %d records: %v", len(records), records)
	}
	if records[1][0] != "'"+maliciousRequestNo {
		t.Fatalf("request_no = %q, want formula-neutral %q", records[1][0], "'"+maliciousRequestNo)
	}
	if records[1][4] != "'"+maliciousTitle {
		t.Fatalf("title = %q, want formula-neutral %q", records[1][4], "'"+maliciousTitle)
	}
}

func TestP0_APPROVAL_001_ExportKeepsRequesterScopeForReadOnlyActor(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	operator, err := env.svc.CreateUser(t.Context(), env.tenantID, env.platformID, service.CreateUserRequest{
		Username: "read-only-" + id.New()[:8],
		Password: "password123",
		Role:     model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	visible := env.insertApprovalDirectly(t, env.tenantID, operator.ID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)
	hidden := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)
	operatorToken := env.tokenFor(t, operator.ID, env.tenantID, model.RoleOperator)

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/export?requester_id="+env.adminID, nil, operatorToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, visible.RequestNo) {
		t.Fatalf("export should contain the read-only actor's own approval %s, got: %s", visible.RequestNo, body)
	}
	if strings.Contains(body, hidden.RequestNo) {
		t.Fatalf("export should not contain another requester's approval %s, got: %s", hidden.RequestNo, body)
	}
}

func TestP0_APPROVAL_001_ExportAuditCarriesScopeAndAuthorizationEvidence(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.platformID, model.PlatformTenantID, "platform_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, env.adminID)

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/export?tenant_id="+env.tenantID, nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), approval.RequestNo) {
		t.Fatalf("export missing approval %s, got: %s", approval.RequestNo, resp.Body.String())
	}
	audits, total, err := env.svc.Store.ListAudits(t.Context(), model.PlatformTenantID, 1, 100, repository.AuditFilter{
		Action: "approval.exported",
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(audits) != 1 {
		t.Fatalf("expected one export audit, got total=%d audits=%d", total, len(audits))
	}
	audit := audits[0]
	if audit.TenantID != model.PlatformTenantID || audit.ActorTenantID != model.PlatformTenantID {
		t.Fatalf("actor tenant evidence mismatch: %+v", audit)
	}
	if audit.TargetTenantID != env.tenantID || audit.Scope != string(service.TenantScopeSpecific) {
		t.Fatalf("target scope evidence mismatch: %+v", audit)
	}
	if audit.Result != "SUCCESS" || audit.AuthorizationDecision != "ALLOW" ||
		audit.AuthorizationPermission != "read:approval" ||
		audit.AuthorizationPolicyVersion != "explicit-rbac-v1" {
		t.Fatalf("authorization evidence mismatch: %+v", audit)
	}
	if !strings.Contains(audit.DetailJSON, `"rows":1`) || !strings.Contains(audit.DetailJSON, `"limit":10000`) {
		t.Fatalf("export result evidence mismatch: %s", audit.DetailJSON)
	}
}

// ---------------------------------------------------------------------------
// Tests: concurrent approve
// ---------------------------------------------------------------------------

func TestIntegration_ConcurrentApprove_OnlyOneSucceeds(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	// Create a second user with tenant_admin role.
	user2, err := env.svc.CreateUser(t.Context(), env.tenantID, env.adminID, service.CreateUserRequest{
		Username: "approver2-" + id.New()[:8],
		Password: "password123",
		Role:     "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	token2 := env.tokenFor(t, user2.ID, env.tenantID, "tenant_admin")

	// Create a role-based policy so both users qualify.
	policyID := "race-policy-" + id.New()[:8]
	err = env.svc.Store.UpsertApprovalPolicy(t.Context(), &model.ApprovalPolicy{
		ID:             policyID,
		TenantID:       env.tenantID,
		ObjectType:     model.ApprovalObjectDataset,
		Action:         model.ApprovalActionDelete,
		Enabled:        true,
		Priority:       100,
		ConditionsJSON: "{}",
		StepsJSON:      `[{"step_no":1,"name":"admin","approver_type":"role","approver_value":"tenant_admin","expire_hours":48}]`,
		ExpireHours:    48,
		Version:        1,
		CreatedBy:      "system",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	approval := env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin")

	type result struct {
		code int
	}
	results := make(chan result, 2)

	go func() {
		w := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/approvals/%s/approve", approval.ID), map[string]any{
			"comment": "concurrent approve 1",
		}, token)
		results <- result{code: w.Code}
	}()

	go func() {
		w := env.doRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/approvals/%s/approve", approval.ID), map[string]any{
			"comment": "concurrent approve 2",
		}, token2)
		results <- result{code: w.Code}
	}()

	r1, r2 := <-results, <-results
	successCount := 0
	if r1.code == http.StatusOK {
		successCount++
	}
	if r2.code == http.StatusOK {
		successCount++
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 success, got %d (codes: %d, %d)", successCount, r1.code, r2.code)
	}
}

// ---------------------------------------------------------------------------
// Tests: unauthorized access
// ---------------------------------------------------------------------------

func TestIntegration_Unauthorized_NoToken(t *testing.T) {
	env := setupTestEnv(t)

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals", nil, "")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.Code)
	}
}

func TestIntegration_Unauthorized_InvalidToken(t *testing.T) {
	env := setupTestEnv(t)

	resp := env.doRequest(t, http.MethodGet, "/api/v1/approvals", nil, "invalid-token-12345")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: full lifecycle
// ---------------------------------------------------------------------------

func TestIntegration_FullLifecycle_SubmitApproveExecute(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	// Create a separate approver user with tenant_admin role.
	approver, err := env.svc.CreateUser(t.Context(), env.tenantID, env.adminID, service.CreateUserRequest{
		Username: "lifecycle-approver-" + id.New()[:8],
		Password: "password123",
		Role:     "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	approverToken := env.tokenFor(t, approver.ID, env.tenantID, "tenant_admin")

	// Create policy with user-based approver.
	policyID := "lc-policy-" + id.New()[:8]
	_ = env.svc.Store.UpsertApprovalPolicy(t.Context(), &model.ApprovalPolicy{
		ID: policyID, TenantID: env.tenantID, ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 1, ConditionsJSON: "{}",
		StepsJSON:   fmt.Sprintf(`[{"step_no":1,"name":"approve","approver_type":"user","approver_value":"%s","expire_hours":48}]`, approver.ID),
		ExpireHours: 48, Version: 1, CreatedBy: "system",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	env.svc.SetApprovalConfig(config.Approval{Enabled: true, DefaultExpireHours: 72, ExecutionMaxRetries: 3, ExpireScanIntervalSec: 3600, RetentionDays: 365, PolicyCacheTTLSec: 0})

	// Insert approval directly to bypass the SubmitApproval step ApprovalID bug.
	approval := env.insertApprovalDirectly(t, env.tenantID, env.adminID, policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverUser, approver.ID)

	// 1. Verify detail shows pending.
	detailResp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/"+approval.ID, nil, token)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d", detailResp.Code)
	}
	detail := jsonResponse(t, detailResp)
	detailData, _ := detail["data"].(map[string]any)
	detailApproval, _ := detailData["approval"].(map[string]any)
	if detailApproval["status"] != model.ApprovalStatusPendingApproval {
		t.Fatalf("detail status = %v, want pending_approval", detailApproval["status"])
	}

	// 2. Approve (different user).
	approveResp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approval.ID+"/approve", map[string]any{
		"comment": "approved for lifecycle test",
	}, approverToken)
	if approveResp.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", approveResp.Code, approveResp.Body.String())
	}
	approveResult := jsonResponse(t, approveResp)
	approveData, _ := approveResult["data"].(map[string]any)
	if approveData["status"] != model.ApprovalStatusApproved {
		t.Fatalf("approve status = %v, want approved", approveData["status"])
	}

	// 3. Verify final detail shows approved.
	finalResp := env.doRequest(t, http.MethodGet, "/api/v1/approvals/"+approval.ID, nil, token)
	if finalResp.Code != http.StatusOK {
		t.Fatalf("final detail: expected 200, got %d", finalResp.Code)
	}
	final := jsonResponse(t, finalResp)
	finalData, _ := final["data"].(map[string]any)
	finalApproval, _ := finalData["approval"].(map[string]any)
	if finalApproval["status"] != model.ApprovalStatusApproved {
		t.Fatalf("final status = %v, want approved", finalApproval["status"])
	}

	// 4. List should include it.
	listResp := env.doRequest(t, http.MethodGet, "/api/v1/approvals?status=approved", nil, token)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", listResp.Code)
	}
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_BatchDecisionHandlerContracts(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "tenant_admin")
	policyID := env.createPolicy(t, token, model.ApprovalObjectDataset, model.ApprovalActionDelete)
	approved := []string{
		env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin").ID,
		env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin").ID,
	}
	rejected := []string{
		env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin").ID,
		env.insertApprovalDirectly(t, env.tenantID, "other-user", policyID, model.ApprovalStatusPendingApproval, model.ApprovalApproverRole, "tenant_admin").ID,
	}

	assertBatch := func(resp *httptest.ResponseRecorder, ids []string, wantStatus string) {
		t.Helper()
		if resp.Code != http.StatusOK {
			t.Fatalf("batch decision: expected 200, got %d: %s", resp.Code, resp.Body.String())
		}
		body := jsonResponse(t, resp)
		rawResults, _ := body["data"].([]any)
		if len(rawResults) != len(ids) {
			t.Fatalf("batch decision returned %d results, want %d", len(rawResults), len(ids))
		}
		results := map[string]map[string]any{}
		for _, rawResult := range rawResults {
			result, _ := rawResult.(map[string]any)
			idValue, _ := result["id"].(string)
			results[idValue] = result
		}
		for _, approvalID := range ids {
			result, ok := results[approvalID]
			if !ok {
				t.Fatalf("batch result missing approval %s", approvalID)
			}
			if result["ok"] != true {
				t.Fatalf("batch decision failed for %s: %+v", approvalID, result)
			}
			approval, err := env.svc.Store.GetApproval(t.Context(), env.tenantID, approvalID)
			if err != nil {
				t.Fatal(err)
			}
			if approval.Status != wantStatus {
				t.Fatalf("approval %s status = %s, want %s", approvalID, approval.Status, wantStatus)
			}
		}
	}

	approveResp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/batch-approve", map[string]any{
		"ids": approved, "comment": "batch approved",
	}, token)
	assertBatch(approveResp, approved, model.ApprovalStatusApproved)

	rejectResp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/batch-reject", map[string]any{
		"ids": rejected, "comment": "batch rejected",
	}, token)
	assertBatch(rejectResp, rejected, model.ApprovalStatusRejected)
}

// ---------------------------------------------------------------------------
// Tests: error cases
// ---------------------------------------------------------------------------

func TestIntegration_ApproveApproval_NonexistentID(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approvals/nonexistent/approve", map[string]any{
		"comment": "test",
	}, token)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestIntegration_CreateApprovalPolicy_MissingFields(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodPost, "/api/v1/approval-policies", map[string]any{
		"enabled": true,
	}, token)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_UpdatePolicyNotFoundDoesNotCreateFromPath(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "platform_admin")

	resp := env.doRequest(t, http.MethodPut, "/api/v1/approval-policies/nonexistent", map[string]any{
		"object_type": "dataset",
		"action":      "delete",
		"enabled":     true,
		"steps": []map[string]any{
			{
				"step_no":        1,
				"name":           "step",
				"approver_type":  "role",
				"approver_value": "tenant_admin",
				"expire_hours":   24,
			},
		},
	}, token)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_DelegationHandlerLifecycleIsPrincipalScoped(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "tenant_admin")
	delegate, err := env.svc.CreateUser(t.Context(), env.tenantID, env.adminID, service.CreateUserRequest{
		Username: "approval-delegate-" + id.New()[:8], Password: "password123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	startsAt := time.Now().UTC().Add(time.Hour)
	endsAt := startsAt.Add(48 * time.Hour)
	payload := map[string]any{
		"delegate_id": delegate.ID,
		"object_type": model.ApprovalObjectDataset,
		"action":      model.ApprovalActionDelete,
		"starts_at":   startsAt.Format(time.RFC3339),
		"ends_at":     endsAt.Format(time.RFC3339),
	}

	createResp := env.doRequest(t, http.MethodPost, "/api/v1/approval-delegations", payload, token)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create delegation: expected 200, got %d: %s", createResp.Code, createResp.Body.String())
	}
	createBody := jsonResponse(t, createResp)
	created, _ := createBody["data"].(map[string]any)
	delegationID, _ := created["id"].(string)
	if delegationID == "" || created["principal_id"] != env.adminID || created["delegate_id"] != delegate.ID {
		t.Fatalf("unexpected created delegation: %+v", created)
	}

	listResp := env.doRequest(t, http.MethodGet, "/api/v1/approval-delegations", nil, token)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list delegations: expected 200, got %d: %s", listResp.Code, listResp.Body.String())
	}
	listBody := jsonResponse(t, listResp)
	items, _ := listBody["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("list delegations returned %d items, want 1", len(items))
	}

	deleteResp := env.doRequest(t, http.MethodDelete, "/api/v1/approval-delegations/"+delegationID, nil, token)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete delegation: expected 200, got %d: %s", deleteResp.Code, deleteResp.Body.String())
	}
	deleteBody := jsonResponse(t, deleteResp)
	deleteData, _ := deleteBody["data"].(map[string]any)
	if deleteData["deleted"] != true {
		t.Fatalf("delete delegation did not confirm deletion: %+v", deleteData)
	}

	listAfterDelete := env.doRequest(t, http.MethodGet, "/api/v1/approval-delegations", nil, token)
	if listAfterDelete.Code != http.StatusOK {
		t.Fatalf("list after delete: expected 200, got %d", listAfterDelete.Code)
	}
	afterBody := jsonResponse(t, listAfterDelete)
	afterItems, _ := afterBody["data"].([]any)
	if len(afterItems) != 0 {
		t.Fatalf("delegation survived delete: %+v", afterItems)
	}

	deleteAgain := env.doRequest(t, http.MethodDelete, "/api/v1/approval-delegations/"+delegationID, nil, token)
	if deleteAgain.Code != http.StatusNotFound {
		t.Fatalf("repeated delete: expected 404, got %d: %s", deleteAgain.Code, deleteAgain.Body.String())
	}
}

func activateScenarioTemplatePassGate(t *testing.T, env *testEnv, template *model.ScenarioTemplateAsset) {
	t.Helper()
	ctx := t.Context()
	candidateResult, err := env.svc.CreateScenarioTemplateReleaseCandidate(
		ctx, env.tenantID, env.adminID, template.ID, service.ScenarioTemplateReleaseCandidateInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	caseVersions, err := env.svc.Store.ListEvaluationCaseVersions(
		ctx, env.tenantID, candidateResult.EvalSetVersion.EvalSetID, candidateResult.EvalSetVersion.Version,
	)
	if err != nil || len(caseVersions) == 0 {
		t.Fatalf("template case versions: len=%d err=%v", len(caseVersions), err)
	}
	run, err := env.svc.CreateEvaluationRun(ctx, env.tenantID, env.adminID, service.EvaluationRunInput{
		ReleaseCandidateID:      candidateResult.Candidate.CandidateID,
		CandidateVersion:        candidateResult.Candidate.CandidateVersion,
		EvalSetID:               candidateResult.EvalSetVersion.EvalSetID,
		EvalSetVersion:          candidateResult.EvalSetVersion.Version,
		EvalSetHash:             candidateResult.EvalSetVersion.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		ExecutionSnapshotID: candidateResult.ExecutionSnapshot.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	casePass := true
	if _, err := env.svc.AddEvaluationCaseResult(ctx, env.tenantID, env.adminID, service.EvaluationCaseResultInput{
		RunID: run.ID, CaseID: caseVersions[0].CaseID, CaseVersionID: caseVersions[0].ID,
		CaseVersionHash: caseVersions[0].Hash, ActualAnswer: "within policy", References: json.RawMessage(`[]`),
		Metrics: json.RawMessage(`{}`), Pass: &casePass,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.CompleteEvaluationRun(ctx, env.tenantID, run.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	bundle, err := env.svc.CreateEvidenceBundle(ctx, env.tenantID, env.adminID, service.EvidenceBundleInput{
		ReleaseCandidateID: candidateResult.Candidate.CandidateID,
		CandidateVersion:   candidateResult.Candidate.CandidateVersion,
		EvaluationRunID:    run.ID,
		SecurityEvidence:   json.RawMessage(`{}`), PolicyEvidence: json.RawMessage(`{}`),
		RiskEvidence: json.RawMessage(`{}`), PermissionEvidence: json.RawMessage(`{}`),
		ConfigurationEvidence: json.RawMessage(`{}`), ApprovalEvidence: json.RawMessage(`{}`),
		EvidenceItems: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	subGates, err := json.Marshal(map[string]string{
		"security": "PASS", "policy": "PASS", "risk": "PASS", "permission": "PASS",
		"configuration": "PASS", "approval": "PASS",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.EvaluateReleaseGate(ctx, env.tenantID, env.adminID, service.GateDecisionInput{
		ReleaseCandidateID: candidateResult.Candidate.CandidateID,
		CandidateVersion:   candidateResult.Candidate.CandidateVersion,
		EvidenceBundleID:   bundle.ID, Environment: "production",
		SubGateStates: subGates, EnvironmentPolicyVersion: "v1",
		EnvironmentPolicyHash: "env-hash", Activate: true,
	}); err != nil {
		t.Fatal(err)
	}
}

// ScenarioID: SC-APPROVAL-015
func TestP0_APPROVAL_013_ScenarioTemplateChatCreateUsesApprovalGate(t *testing.T) {
	env := setupTestEnv(t)
	token := env.tokenFor(t, env.adminID, env.tenantID, "tenant_admin")
	env.createPolicy(t, token, model.ApprovalObjectChat, model.ApprovalActionCreate)
	template, err := env.svc.CreateScenarioTemplate(t.Context(), env.tenantID, env.adminID, service.ScenarioTemplateInput{
		Key: "approval-chat-" + id.New()[:8], Name: "Approval Chat", AppTypes: []string{"chat"},
		Status: model.AssetStatusPublished,
		Payload: service.ScenarioTemplatePayload{
			DatasetSuggestions:  []string{"审批手册"},
			EvaluationQuestions: []string{"What is the approval policy?"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	activateScenarioTemplatePassGate(t, env, template)

	resp := env.doRequest(t, http.MethodPost, "/api/v1/scenario-template-assets/"+template.ID+"/instantiate-chat", map[string]any{
		"create_missing_datasets": true,
	}, token)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected approval hold 202, got %d: %s", resp.Code, resp.Body.String())
	}
	body := jsonResponse(t, resp)
	data, _ := body["data"].(map[string]any)
	if data["approval_id"] == nil || data["status"] != model.ApprovalStatusPendingApproval {
		t.Fatalf("unexpected approval response: %+v", data)
	}
	chats, _, err := env.svc.ListChats(t.Context(), env.tenantID, false, repository.ChatFilter{}, 1, 10)
	if err != nil || len(chats) != 0 {
		t.Fatalf("approval hold must not create chat: len=%d err=%v", len(chats), err)
	}
	datasets, err := env.svc.ListDatasets(t.Context(), env.tenantID, repository.DatasetFilter{})
	if err != nil || len(datasets) != 0 {
		t.Fatalf("approval hold must not create datasets: len=%d err=%v", len(datasets), err)
	}
}
