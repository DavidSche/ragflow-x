package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func seedCrossTenantDocument(t *testing.T, env *testEnv) (string, string) {
	t.Helper()
	dataset, err := env.svc.CreateDataset(t.Context(), env.tenantID, "cross docs")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := env.svc.UploadDocument(t.Context(), env.tenantID, dataset.ID, env.adminID, "policy.md", []byte("cross tenant content"))
	if err != nil {
		t.Fatal(err)
	}
	return dataset.ID, doc.ID
}

func TestCrossTenantDocumentAndChunkWritesSubmitExactTargetApprovals(t *testing.T) {
	tests := []struct {
		name       string
		objectType string
		action     string
		method     string
		path       func(datasetID, documentID string) string
		body       map[string]any
		chunk      bool
		snapshot   []string
	}{
		{
			name: "document parse", objectType: model.ApprovalObjectDocument, action: model.ApprovalActionParse,
			method: http.MethodPost, path: func(datasetID, _ string) string { return "/api/v1/datasets/" + datasetID + "/parse" },
			body: map[string]any{"document_ids": []string{""}}, snapshot: []string{"name", "status", "update_time"},
		},
		{
			name: "document stop", objectType: model.ApprovalObjectDocument, action: model.ApprovalActionStop,
			method: http.MethodPost, path: func(datasetID, _ string) string { return "/api/v1/datasets/" + datasetID + "/documents/stop" },
			body: map[string]any{"document_ids": []string{""}}, snapshot: []string{"name", "status", "update_time"},
		},
		{
			name: "document delete", objectType: model.ApprovalObjectDocument, action: model.ApprovalActionDelete,
			method: http.MethodDelete, path: func(datasetID, _ string) string { return "/api/v1/datasets/" + datasetID + "/documents" },
			body: map[string]any{"document_ids": []string{""}}, snapshot: []string{"name", "status", "update_time"},
		},
		{
			name: "document enable", objectType: model.ApprovalObjectDocument, action: model.ApprovalActionEnable,
			method: http.MethodPost, path: func(datasetID, _ string) string { return "/api/v1/datasets/" + datasetID + "/documents/status" },
			body: map[string]any{"document_ids": []string{""}, "enabled": true}, snapshot: []string{"name", "status", "update_time"},
		},
		{
			name: "document metadata", objectType: model.ApprovalObjectDocument, action: model.ApprovalActionUpdate,
			method: http.MethodPut, path: func(datasetID, documentID string) string {
				return "/api/v1/datasets/" + datasetID + "/documents/" + documentID + "/metadata"
			},
			body: map[string]any{"metadata": map[string]any{"source": "governance"}}, snapshot: []string{"name", "status", "update_time"},
		},
		{
			name: "chunk delete", objectType: model.ApprovalObjectDocumentChunk, action: model.ApprovalActionDelete,
			method: http.MethodDelete, path: func(datasetID, documentID string) string {
				return "/api/v1/datasets/" + datasetID + "/documents/" + documentID + "/chunks"
			},
			body: map[string]any{"chunk_ids": []string{""}}, chunk: true, snapshot: []string{"content_hash", "available", "document_id"},
		},
		{
			name: "chunk enable", objectType: model.ApprovalObjectDocumentChunk, action: model.ApprovalActionEnable,
			method: http.MethodPatch, path: func(datasetID, documentID string) string {
				return "/api/v1/datasets/" + datasetID + "/documents/" + documentID + "/chunks"
			},
			body: map[string]any{"chunk_ids": []string{""}, "enabled": true}, chunk: true, snapshot: []string{"content_hash", "available", "document_id"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := setupTestEnv(t)
			platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
			tenantToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)
			target := env.tenantID
			scope := "?scope=specific&tenant_id=" + target
			datasetID, documentID := seedCrossTenantDocument(t, env)
			datasetLink, err := env.svc.Store.GetDatasetLink(t.Context(), env.tenantID, datasetID)
			if err != nil || datasetLink == nil {
				t.Fatalf("dataset link missing: %+v err=%v", datasetLink, err)
			}
			objectID := documentID
			if test.chunk {
				objectID = id.New()
				env.svc.RAGFlow.(*ragflow.Mock).SeedDocumentChunk(datasetLink.RAGFlowDatasetID, documentID, objectID, "approved chunk", false)
			}
			if ids, ok := test.body["document_ids"].([]string); ok {
				ids[0] = documentID
			}
			if ids, ok := test.body["chunk_ids"].([]string); ok {
				ids[0] = objectID
			}

			for _, invalidScope := range []string{"?scope=all", "?scope=specific&tenant_id=" + id.New()} {
				resp := env.doRequest(t, test.method, test.path(datasetID, documentID)+invalidScope, test.body, platformToken)
				if resp.Code != http.StatusBadRequest {
					t.Fatalf("%s invalid scope should fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
				}
			}
			resp := env.doRequest(t, test.method, test.path(datasetID, documentID)+scope, test.body, platformToken)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("%s without approval policy must fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
			}

			env.createPolicy(t, tenantToken, test.objectType, test.action)
			resp = env.doRequest(t, test.method, test.path(datasetID, documentID)+scope, test.body, platformToken)
			if resp.Code != http.StatusAccepted {
				t.Fatalf("%s should submit approval: got %d body %s", test.name, resp.Code, resp.Body.String())
			}
			approval, err := env.svc.Store.GetApproval(t.Context(), target, approvalIDFromResponse(t, resp.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if approval.TenantID != target || approval.TargetTenantID != target {
				t.Fatalf("%s approval target mismatch: tenant=%s target=%s", test.name, approval.TenantID, approval.TargetTenantID)
			}
			if approval.ResourceVersion == "" || approval.ApprovalActionHash == "" {
				t.Fatalf("%s approval missing fingerprint/version: %+v", test.name, approval)
			}
			snapshot := map[string]any{}
			if err := json.Unmarshal([]byte(approval.SnapshotJSON), &snapshot); err != nil {
				t.Fatal(err)
			}
			for _, key := range test.snapshot {
				if _, ok := snapshot[key]; !ok {
					t.Fatalf("%s snapshot missing %s: %s", test.name, key, approval.SnapshotJSON)
				}
			}
			audits, _, err := env.svc.Store.ListAudits(t.Context(), target, 1, 100, repository.AuditFilter{Action: "approval.submitted"})
			if err != nil {
				t.Fatal(err)
			}
			if len(audits) == 0 || audits[0].ResourceID != approval.ID {
				t.Fatalf("%s approval submission audit missing", test.name)
			}
		})
	}
}

func TestCrossTenantDocumentUploadAndBatchesFailClosed(t *testing.T) {
	env := setupTestEnv(t)
	platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
	datasetID, documentID := seedCrossTenantDocument(t, env)
	scope := "?scope=specific&tenant_id=" + env.tenantID
	if err := env.svc.Authorize(t.Context(), env.platformID, "execute", "document"); err != nil {
		t.Fatalf("platform authorize failed: %v", err)
	}
	if _, err := env.svc.ResolveTenantScope(t.Context(), env.platformID, model.PlatformTenantID, "specific", env.tenantID); err != nil {
		t.Fatalf("platform resolve failed: %v", err)
	}

	resp := env.doRequest(t, http.MethodPost, "/api/v1/datasets/"+datasetID+"/parse"+scope, map[string]any{"document_ids": []string{documentID, documentID}}, platformToken)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("batch parse should fail closed: got %d body %s", resp.Code, resp.Body.String())
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "cross-upload.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("cross tenant upload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/"+datasetID+"/documents"+scope, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+platformToken)
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-tenant upload should fail closed: got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestCrossTenantDocumentSubresourceReadsUseGovernanceScope(t *testing.T) {
	env := setupTestEnv(t)
	platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
	datasetID, documentID := seedCrossTenantDocument(t, env)
	link, err := env.svc.Store.GetDatasetLink(t.Context(), env.tenantID, datasetID)
	if err != nil || link == nil {
		t.Fatalf("dataset link missing: %+v err=%v", link, err)
	}
	env.svc.RAGFlow.(*ragflow.Mock).SeedDocumentChunk(link.RAGFlowDatasetID, documentID, "chunk-1", "readable chunk", true)

	valid := []string{
		"/api/v1/datasets/" + datasetID + "/documents/" + documentID + "/chunks?scope=all",
		"/api/v1/datasets/" + datasetID + "/documents/" + documentID + "/preview?scope=specific&tenant_id=" + env.tenantID,
	}
	for _, path := range valid {
		resp := env.doRequest(t, http.MethodGet, path, nil, platformToken)
		if resp.Code != http.StatusOK {
			t.Fatalf("governance read %s should pass: got %d body %s", path, resp.Code, resp.Body.String())
		}
	}
	resp := env.doRequest(t, http.MethodGet, "/api/v1/datasets/"+datasetID+"/documents/"+documentID+"/chunks?scope=specific&tenant_id=missing", nil, platformToken)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unknown target tenant should fail closed: got %d body %s", resp.Code, resp.Body.String())
	}
}
