package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func seedCrossTenantAgent(t *testing.T, env *testEnv, title string) *model.AgentShadow {
	t.Helper()
	agent, err := env.svc.CreateAgent(t.Context(), env.tenantID, title, map[string]interface{}{"nodes": []interface{}{}}, false, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetAgentOwner(t.Context(), agent, env.adminID); err != nil {
		t.Fatal(err)
	}
	return agent
}

func TestCrossTenantAgentWritesSubmitExactTargetApprovals(t *testing.T) {
	tests := []struct {
		name         string
		action       string
		method       string
		path         func(agentID string) string
		body         map[string]any
		snapshotKeys []string
		seedAgent    bool
	}{
		{
			name: "agent create", action: model.ApprovalActionCreate, method: http.MethodPost,
			path: func(string) string { return "/api/v1/agents" },
			body: map[string]any{"title": "cross agent", "dsl": map[string]any{"nodes": []any{}}, "canvas_category": model.AgentCanvasCategoryWorkflow},
		},
		{
			name: "agent update", action: model.ApprovalActionUpdate, method: http.MethodPut,
			path: func(agentID string) string { return "/api/v1/agents/" + agentID },
			body: map[string]any{"title": "renamed agent"}, snapshotKeys: []string{"title", "status", "agent_updated_at"}, seedAgent: true,
		},
		{
			name: "agent publish", action: model.ApprovalActionUpdate, method: http.MethodPost,
			path: func(agentID string) string { return "/api/v1/agents/" + agentID + "/publish" },
			body: map[string]any{"release": true}, snapshotKeys: []string{"title", "release", "agent_updated_at"}, seedAgent: true,
		},
		{
			name: "agent rollback", action: model.ApprovalActionUpdate, method: http.MethodPost,
			path: func(agentID string) string { return "/api/v1/agents/" + agentID + "/versions/v1/rollback" },
			body: map[string]any{"rollback_version_id": "v1"}, snapshotKeys: []string{"title", "agent_updated_at", "rollback_version_id"}, seedAgent: true,
		},
		{
			name: "agent delete", action: model.ApprovalActionDelete, method: http.MethodDelete,
			path:         func(agentID string) string { return "/api/v1/agents/" + agentID },
			snapshotKeys: []string{"title", "status", "agent_updated_at"}, seedAgent: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := setupTestEnv(t)
			platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
			tenantToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)
			target := env.tenantID
			scope := "?scope=specific&tenant_id=" + target
			agentID := ""
			if test.seedAgent {
				agentID = seedCrossTenantAgent(t, env, "source-agent").ID
			}

			for _, invalidScope := range []string{"?scope=all", "?scope=specific&tenant_id=" + id.New()} {
				resp := env.doRequest(t, test.method, test.path(agentID)+invalidScope, test.body, platformToken)
				if resp.Code != http.StatusBadRequest {
					t.Fatalf("%s invalid scope should fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
				}
			}

			resp := env.doRequest(t, test.method, test.path(agentID)+scope, test.body, platformToken)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("%s without approval policy must fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
			}

			env.createPolicy(t, tenantToken, model.ApprovalObjectAgent, test.action)
			resp = env.doRequest(t, test.method, test.path(agentID)+scope, test.body, platformToken)
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
