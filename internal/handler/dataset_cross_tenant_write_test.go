package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func seedCrossTenantDataset(t *testing.T, env *testEnv, name string) string {
	t.Helper()
	datasetID := id.New()
	env.createDatasetLink(t, env.tenantID, datasetID, name)
	return datasetID
}

func TestCrossTenantDatasetWritesSubmitExactTargetApprovals(t *testing.T) {
	tests := []struct {
		name         string
		action       string
		method       string
		path         func(datasetID string) string
		body         map[string]any
		snapshotKeys []string
		seedDataset  bool
	}{
		{
			name: "dataset create", action: model.ApprovalActionCreate, method: http.MethodPost,
			path: func(string) string { return "/api/v1/datasets" },
			body: map[string]any{"name": "cross-tenant-dataset"},
		},
		{
			name: "dataset update", action: model.ApprovalActionUpdate, method: http.MethodPut,
			path: func(datasetID string) string { return "/api/v1/datasets/" + datasetID },
			body: map[string]any{"name": "renamed-dataset"}, snapshotKeys: []string{"name"}, seedDataset: true,
		},
		{
			name: "dataset config", action: model.ApprovalActionUpdate, method: http.MethodPut,
			path: func(datasetID string) string { return "/api/v1/datasets/" + datasetID + "/config" },
			body: map[string]any{"description": "new description"}, snapshotKeys: []string{"name", "ragflow_dataset_id"}, seedDataset: true,
		},
		{
			name: "dataset project bind", action: model.ApprovalActionUpdate, method: http.MethodPut,
			path: func(datasetID string) string { return "/api/v1/datasets/" + datasetID + "/project" },
			body: map[string]any{"project_id": "@@project@@"}, snapshotKeys: []string{"name", "project_id"}, seedDataset: true,
		},
		{
			name: "dataset delete", action: model.ApprovalActionDelete, method: http.MethodDelete,
			path:         func(datasetID string) string { return "/api/v1/datasets/" + datasetID },
			snapshotKeys: []string{"name", "ragflow_dataset_id"}, seedDataset: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := setupTestEnv(t)
			platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
			tenantToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)
			target := env.tenantID
			scope := "?scope=specific&tenant_id=" + target
			datasetID := ""
			if test.seedDataset {
				datasetID = seedCrossTenantDataset(t, env, "source-dataset")
			}
			if test.body["project_id"] != nil {
				projectID := id.New()
				if err := env.svc.Store.CreateProject(t.Context(), &model.Project{
					ID: projectID, TenantID: target, Name: "target project",
				}); err != nil {
					t.Fatal(err)
				}
				test.body["project_id"] = projectID
			}

			for _, invalidScope := range []string{"?scope=all", "?scope=specific&tenant_id=" + id.New()} {
				resp := env.doRequest(t, test.method, test.path(datasetID)+invalidScope, test.body, platformToken)
				if resp.Code != http.StatusBadRequest {
					t.Fatalf("%s invalid scope should fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
				}
			}

			resp := env.doRequest(t, test.method, test.path(datasetID)+scope, test.body, platformToken)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("%s without approval policy must fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
			}

			env.createPolicy(t, tenantToken, model.ApprovalObjectDataset, test.action)
			resp = env.doRequest(t, test.method, test.path(datasetID)+scope, test.body, platformToken)
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
			for _, key := range test.snapshotKeys {
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

func TestCrossTenantDatasetBatchDeleteFailsClosed(t *testing.T) {
	env := setupTestEnv(t)
	platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
	datasetID := seedCrossTenantDataset(t, env, "batch-source")
	resp := env.doRequest(t, http.MethodDelete, "/api/v1/datasets?scope=specific&tenant_id="+env.tenantID, map[string]any{"ids": []string{datasetID}}, platformToken)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("cross-tenant batch delete should fail closed: got %d body %s", resp.Code, resp.Body.String())
	}
}
