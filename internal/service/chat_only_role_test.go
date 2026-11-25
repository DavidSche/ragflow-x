package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestChatOnlyRoleIsExpressibleWithoutManageChat is the executable form of
// doc/304 §4 E155: "a role that may only talk" must be a set of grants the
// matrix can actually hold, not a hope. It builds that role the way an
// administrator would — a custom role with no parent, four grants — assigns it
// to a user, and then checks the three things the role promises:
//
//  1. it can start a conversation (session:create),
//  2. it cannot edit or delete the assistant itself (manage),
//  3. the conversation really opens on the service path, not just in theory.
//
// Before E155 step 1 was impossible without step 2: `POST /chats/:id/sessions`
// required `manage`, so every "chat user" was also an assistant administrator.
func TestChatOnlyRoleIsExpressibleWithoutManageChat(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	tenant, err := svc.CreateTenant(ctx, "ChatOnlyTenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "chatonlyadmin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}

	// No parent role: inheriting business_user would grant 20 resources and
	// then need ~30 deny rows to take them away again. The smallest honest
	// definition of "may only talk" is an allow list, not a deny list.
	role, err := svc.CreateRole(ctx, admin.ID, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "对话使用者", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatalf("create custom role: %v", err)
	}
	if err := svc.SetRolePermissions(ctx, admin.ID, model.RoleTenantAdmin, role.ID, []PermissionSpec{
		{Action: "read", Resource: "user"}, // /auth/me for its own identity
		{Action: "read", Resource: "chat"},
		{Action: "execute", Resource: "chat"},
		{Action: "session:create", Resource: "chat"},
	}); err != nil {
		t.Fatalf("set chat-only grants: %v", err)
	}

	member, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "talker", Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AssignRole(ctx, tenant.ID, model.RoleTenantAdmin, member.ID, role.ID); err != nil {
		t.Fatalf("assign custom chat-only role: %v", err)
	}

	if err := svc.Authorize(ctx, member.ID, "session:create", "chat"); err != nil {
		t.Fatalf("chat-only role must be able to start a conversation: %v", err)
	}
	if err := svc.Authorize(ctx, member.ID, "manage", "chat"); err == nil {
		t.Fatal("chat-only role must not be able to edit or delete the assistant (manage@chat)")
	}

	// The permission feed the web client reads must agree with the enforcement
	// point above; otherwise the UI hides a button the server would honour, or
	// shows one it refuses (doc/223 §3 T8 inference reads this list).
	me, err := svc.MePermissions(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	granted := map[string]bool{}
	for _, perm := range me.Permissions {
		if perm.Resource == "chat" && perm.Effect != model.PermissionEffectDeny {
			granted[perm.Action] = true
		}
	}
	if !granted["session:create"] || !granted["execute"] {
		t.Fatalf("MePermissions chat grants = %v, want session:create and execute", granted)
	}
	if granted["manage"] {
		t.Fatal("MePermissions reports manage@chat for a chat-only role")
	}

	// And the real path: a chat created by the tenant admin can be opened by
	// the chat-only member. The handler's `session:create` check is what the
	// route rule mirrors; the service call proves the rest of the path holds.
	chat, err := svc.CreateChat(ctx, tenant.ID, "Product Q&A", nil)
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	session, err := svc.CreateChatSession(ctx, tenant.ID, chat.ID, "first thread", false)
	if err != nil {
		t.Fatalf("chat-only member must be able to open a session: %v", err)
	}
	if session == nil || session.ID == "" {
		t.Fatalf("create chat session returned %#v", session)
	}
}

// TestBusinessUserCanStartAConversationWithoutManagingAssistants pins the
// built-in role to the same boundary: business_user talks, it does not author
// assistants. It also documents the migration note in doc/305 §2.53 — a
// deployment that granted `manage@chat` to a custom role for the sole purpose
// of opening threads should switch that grant to `session:create`.
func TestBusinessUserCanStartAConversationWithoutManagingAssistants(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "BuiltinChatUser")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "builtin_talker", Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, user.ID, "session:create", "chat"); err != nil {
		t.Fatalf("business_user must be able to start a conversation: %v", err)
	}
	if err := svc.Authorize(ctx, user.ID, "manage", "chat"); err == nil {
		t.Fatal("business_user must not manage assistants")
	}
}

// TestCreateRoleRejectsUnknownParent closes the silent half of doc/304 §4 E155.
// CreateRole used to write ParentID without a lookup, and expandRoles skips a
// parent it cannot load, so a typo produced a role that claimed a parent and
// inherited nothing. UpdateRole already refused this (40086); creation did not.
func TestCreateRoleRejectsUnknownParent(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "ParentLookup")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "parentadmin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRole(ctx, admin.ID, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "orphan", Scope: model.RoleScopeTenant, ParentID: "00000000000000000000000000000000",
	}); err == nil {
		t.Fatal("CreateRole accepted a parent id that does not exist")
	}
}

// TestUpdateRoleRejectsParentCycle keeps inheritance acyclic. A cycle does not
// crash expandRoles (it tracks visited ids), so the only way to notice is to
// refuse it where it is created.
func TestUpdateRoleRejectsParentCycle(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "CycleTenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "cycleadmin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	top, err := svc.CreateRole(ctx, admin.ID, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "top", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := svc.CreateRole(ctx, admin.ID, model.RoleTenantAdmin, CreateRoleRequest{
		Name: "child", Scope: model.RoleScopeTenant, ParentID: top.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// top -> child would make top its own ancestor.
	if _, err := svc.UpdateRole(ctx, admin.ID, model.RoleTenantAdmin, top.ID, UpdateRoleRequest{ParentID: child.ID}); err == nil {
		t.Fatal("UpdateRole accepted a parent that closes an inheritance cycle")
	}
	if _, err := svc.UpdateRole(ctx, admin.ID, model.RoleTenantAdmin, top.ID, UpdateRoleRequest{ParentID: top.ID}); err == nil {
		t.Fatal("UpdateRole accepted the role as its own parent")
	}
}
