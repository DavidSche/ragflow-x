package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-ENTERPRISE-001
func TestP0_ENTERPRISE_001_ConnectionAndBindingVersionChains(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	workspace, err := svc.CreateTenant(ctx, "Workspace")
	if err != nil {
		t.Fatal(err)
	}

	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName:  "openai-api-compatible",
		DisplayName:   "Enterprise OpenAI",
		BaseURL:       "https://internal.llm.example/v1",
		CredentialRef: "vault://enterprise/openai",
		Visibility:    model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.Connection.CurrentConnectionVersion != 1 || connection.Version.Version != 1 {
		t.Fatalf("unexpected initial connection version: %+v", connection)
	}
	if connection.Version.ConnectionConfigHash == "" {
		t.Fatal("connection config hash is required")
	}

	same, err := svc.UpdateEnterpriseConnection(ctx, admin.ID, connection.Connection.ID, UpdateEnterpriseConnectionRequest{
		ProviderName:            connection.Version.ProviderName,
		DisplayName:             connection.Version.DisplayName,
		BaseURL:                 connection.Version.BaseURL,
		CredentialRef:           "vault://enterprise/openai",
		CredentialVersion:       connection.Version.CredentialVersion,
		CredentialPolicyVersion: connection.Version.CredentialPolicyVersion,
		Visibility:              connection.Connection.Visibility,
		UsableBy:                connection.Connection.UsableBy,
		ManagedBy:               connection.Connection.ManagedBy,
		CredentialScope:         connection.Connection.CredentialScope,
	})
	if err != nil {
		t.Fatal(err)
	}
	if same.Connection.CurrentConnectionVersion != 1 {
		t.Fatal("unchanged configuration must not create a new connection version")
	}

	updated, err := svc.UpdateEnterpriseConnection(ctx, admin.ID, connection.Connection.ID, UpdateEnterpriseConnectionRequest{
		ProviderName:            "openai-api-compatible",
		DisplayName:             "Enterprise OpenAI v2",
		BaseURL:                 "https://internal.llm.example/v2",
		CredentialRef:           "vault://enterprise/openai",
		CredentialVersion:       "v2",
		CredentialPolicyVersion: "v1",
		Visibility:              model.EnterpriseConnectionVisibilityShared,
		UsableBy:                EnterpriseConnectionUsableByBindings,
		ManagedBy:               model.EnterpriseConnectionManagedByPlatform,
		CredentialScope:         model.EnterpriseConnectionCredentialScopeConnection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Connection.CurrentConnectionVersion != 2 || updated.Version.Version != 2 {
		t.Fatalf("connection version was not advanced: %+v", updated)
	}
	if updated.Version.ConnectionConfigHash == connection.Version.ConnectionConfigHash {
		t.Fatal("changed connection configuration must change config hash")
	}

	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID:        connection.Connection.ID,
		TenantID:            workspace.ID,
		AllowedCapabilities: []string{"chat"},
		AllowedModelRefs:    []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.Binding.CurrentBindingVersion != 1 || binding.Version.BindingID != binding.Binding.BindingID {
		t.Fatalf("unexpected initial binding: %+v", binding)
	}
	if binding.TenantName == "" {
		t.Fatal("binding governance view lost tenant attribution")
	}

	updatedBinding, err := svc.UpdateEnterpriseBinding(ctx, admin.ID, binding.Binding.BindingID, UpdateEnterpriseBindingRequest{
		AllowedCapabilities: []string{"chat", "embedding"},
		AllowedModelRefs:    []string{"gpt-4o-mini", "text-embedding-3-small"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updatedBinding.Binding.CurrentBindingVersion != 2 || updatedBinding.Version.Version != 2 {
		t.Fatalf("binding version was not advanced: %+v", updatedBinding)
	}
	history, err := svc.ListEnterpriseBindingVersions(ctx, binding.Binding.BindingID)
	if err != nil || len(history) != 2 {
		t.Fatalf("binding history: count=%d err=%v", len(history), err)
	}

	revoked, err := svc.RevokeEnterpriseBinding(ctx, admin.ID, binding.Binding.BindingID)
	if err != nil || revoked.Binding.LifecycleStatus != model.EnterpriseLifecycleDisabled {
		t.Fatalf("binding revoke: %+v err=%v", revoked, err)
	}
	if _, err := svc.UpdateEnterpriseBinding(ctx, admin.ID, binding.Binding.BindingID, UpdateEnterpriseBindingRequest{
		AllowedCapabilities: []string{"chat"},
		AllowedModelRefs:    []string{"gpt-4o-mini"},
	}); err == nil {
		t.Fatal("revoked binding must not be editable")
	}
}

func TestListEnterpriseConnectionsByCursorIsStable(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	ids := make([]string, 0, 3)
	for index := range 3 {
		connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
			ProviderName: "openai", DisplayName: "Connection " + strconv.Itoa(index),
			BaseURL: "https://api.openai.com/v1", CredentialRef: "cred-" + strconv.Itoa(index),
			Visibility: model.EnterpriseConnectionVisibilityPrivate,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, connection.Connection.ID)
	}
	first, err := svc.ListEnterpriseConnectionsByCursor(ctx, []string{admin.TenantID}, "", "", "", 2)
	if err != nil || len(first.Items) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first cursor page: %+v err=%v", first, err)
	}
	second, err := svc.ListEnterpriseConnectionsByCursor(ctx, []string{admin.TenantID}, "", "", first.NextCursor, 2)
	if err != nil || len(second.Items) != 1 || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second cursor page: %+v err=%v", second, err)
	}
	seen := map[string]bool{first.Items[0].Connection.ID: true, first.Items[1].Connection.ID: true, second.Items[0].Connection.ID: true}
	for _, expected := range ids {
		if !seen[expected] {
			t.Fatalf("cursor pagination omitted connection %s", expected)
		}
	}
}

// ScenarioID: SC-ENTERPRISE-001
func TestP0_ENTERPRISE_001_BindingRejectsNonWorkspaceTarget(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName:  "openai-api-compatible",
		DisplayName:   "Enterprise OpenAI",
		BaseURL:       "https://internal.llm.example/v1",
		CredentialRef: "vault://enterprise/openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID:        connection.Connection.ID,
		TenantID:            admin.TenantID,
		AllowedCapabilities: []string{"chat"},
		AllowedModelRefs:    []string{"gpt-4o-mini"},
	}); err == nil {
		t.Fatal("platform tenant must not be used as binding target")
	}
}
