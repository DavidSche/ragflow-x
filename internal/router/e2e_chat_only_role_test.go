package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// loginAs returns an access token for a user created by the test, exercising
// the same login endpoint a browser uses. Tenants are resolved by username, so
// every user in this test gets a distinct one.
func (a *testApp) loginAs(t *testing.T, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, raw := a.doAuth(t, http.MethodPost, "/api/v1/auth/login", "", body)
	if resp.StatusCode != 200 {
		t.Fatalf("login %s status %d: %s", username, resp.StatusCode, raw)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Token == "" {
		t.Fatalf("login %s returned an empty token", username)
	}
	return out.Data.Token
}

// TestE2E_ChatOnlyRoleTalksWithoutManagingTheAssistant is the full-stack half
// of doc/304 §4 E155. The unit tests pin the matrix row, the route rule and the
// handler guard separately; this one proves the three agree, because a change
// that reverts any of them turns the first request below into a 403 even though
// the role's grants are exactly "talk".
func TestE2E_ChatOnlyRoleTalksWithoutManagingTheAssistant(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()

	platform, err := app.svc.CreateTenant(ctx, "chat-only-demo")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := app.svc.CreateUser(ctx, platform.ID, "", service.CreateUserRequest{
		Username: "chat_only_admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	role, err := app.svc.CreateRole(ctx, admin.ID, model.RoleTenantAdmin, service.CreateRoleRequest{
		Name: "对话使用者", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.svc.SetRolePermissions(ctx, admin.ID, model.RoleTenantAdmin, role.ID, []service.PermissionSpec{
		{Action: "read", Resource: "user"},
		{Action: "read", Resource: "chat"},
		{Action: "execute", Resource: "chat"},
		{Action: "session:create", Resource: "chat"},
	}); err != nil {
		t.Fatal(err)
	}
	member, err := app.svc.CreateUser(ctx, platform.ID, "", service.CreateUserRequest{
		Username: "chat_only_member", Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.svc.AssignRole(ctx, platform.ID, model.RoleTenantAdmin, member.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.svc.CreateUser(ctx, platform.ID, "", service.CreateUserRequest{
		Username: "chat_only_viewer", Password: "secret123", Role: model.RoleViewer,
	}); err != nil {
		t.Fatal(err)
	}
	chat, err := app.svc.CreateChat(ctx, platform.ID, "产品问答", nil)
	if err != nil {
		t.Fatal(err)
	}

	memberToken := app.loginAs(t, "chat_only_member", "secret123")
	path := "/api/v1/chats/" + chat.ID + "/sessions"

	resp, body := app.doAuth(t, http.MethodPost, path, memberToken, []byte(`{"name":"第一条线程"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("chat-only role could not start a conversation: status %d: %s", resp.StatusCode, body)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.ID == "" {
		t.Fatalf("session create returned no id: %s", body)
	}

	// The boundary the split exists for: the same role must not rename or
	// delete the assistant itself.
	resp, body = app.doAuth(t, http.MethodPut, "/api/v1/chats/"+chat.ID, memberToken, []byte(`{"name":"renamed by a talker"}`))
	if resp.StatusCode != 403 {
		t.Fatalf("chat-only role renaming an assistant = %d, want 403: %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/chats/"+chat.ID, memberToken, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("chat-only role deleting an assistant = %d, want 403: %s", resp.StatusCode, body)
	}

	// viewer is the read-only slice (doc/245): it opens no threads at all.
	viewerToken := app.loginAs(t, "chat_only_viewer", "secret123")
	resp, body = app.doAuth(t, http.MethodPost, path, viewerToken, []byte(`{"name":"viewer thread"}`))
	if resp.StatusCode != 403 {
		t.Fatalf("viewer starting a conversation = %d, want 403: %s", resp.StatusCode, body)
	}
}
