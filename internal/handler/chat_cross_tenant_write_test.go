package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func seedCrossTenantChat(t *testing.T, env *testEnv, name string) *model.ChatShadow {
	t.Helper()
	chat, err := env.svc.CreateChatWithConfig(t.Context(), env.tenantID, name, []string{}, service.ChatAuthoring{})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.svc.SetChatOwner(t.Context(), chat, env.adminID); err != nil {
		t.Fatal(err)
	}
	return chat
}

func TestCrossTenantChatWritesSubmitExactTargetApprovals(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		method    string
		path      func(chatID string) string
		body      map[string]any
		seedChat  bool
		snapshots []string
	}{
		{
			name:   "chat create",
			action: model.ApprovalActionCreate,
			method: http.MethodPost,
			path:   func(string) string { return "/api/v1/chats" },
			body: map[string]any{
				"name": "cross chat", "dataset_ids": []string{},
				"prompt_config": map[string]any{"system": "test"},
			},
		},
		{
			name:      "chat update",
			action:    model.ApprovalActionUpdate,
			method:    http.MethodPut,
			path:      func(chatID string) string { return "/api/v1/chats/" + chatID },
			body:      map[string]any{"name": "renamed chat"},
			seedChat:  true,
			snapshots: []string{"name", "status", "dataset_ids", "owner_id", "chat_updated_at"},
		},
		{
			name:      "chat delete",
			action:    model.ApprovalActionDelete,
			method:    http.MethodDelete,
			path:      func(chatID string) string { return "/api/v1/chats/" + chatID },
			seedChat:  true,
			snapshots: []string{"name", "status", "dataset_ids", "owner_id", "chat_updated_at"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := setupTestEnv(t)
			platformToken := env.tokenFor(t, env.platformID, model.PlatformTenantID, model.RolePlatformAdmin)
			tenantToken := env.tokenFor(t, env.adminID, env.tenantID, model.RoleTenantAdmin)
			target := env.tenantID
			scope := "?scope=specific&tenant_id=" + target
			chatID := ""
			if test.seedChat {
				chatID = seedCrossTenantChat(t, env, "source chat").ID
			}

			for _, invalidScope := range []string{"?scope=all", "?scope=specific&tenant_id=" + id.New()} {
				resp := env.doRequest(t, test.method, test.path(chatID)+invalidScope, test.body, platformToken)
				if resp.Code != http.StatusBadRequest {
					t.Fatalf("%s invalid scope should fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
				}
			}

			resp := env.doRequest(t, test.method, test.path(chatID)+scope, test.body, platformToken)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("%s without approval policy must fail closed: got %d body %s", test.name, resp.Code, resp.Body.String())
			}

			env.createPolicy(t, tenantToken, model.ApprovalObjectChat, test.action)
			resp = env.doRequest(t, test.method, test.path(chatID)+scope, test.body, platformToken)
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
			for _, key := range test.snapshots {
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
