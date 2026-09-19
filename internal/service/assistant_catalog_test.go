package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestListConversationAssistantsUnifiedCatalog(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenantA, err := svc.CreateTenant(ctx, "CatalogA")
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := svc.CreateTenant(ctx, "CatalogB")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChat(ctx, tenantA.ID, "catalog-chat", []string{"d1"}); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if _, err := svc.CreateAgent(ctx, tenantA.ID, "catalog-agent", map[string]interface{}{"x": 1}, true, model.AgentCanvasCategoryWorkflow); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := svc.CreateAgent(ctx, tenantA.ID, "catalog-dataflow", map[string]interface{}{"x": 1}, true, model.AgentCanvasCategoryDataflow); err != nil {
		t.Fatalf("create dataflow: %v", err)
	}
	actor, err := svc.CreateUser(ctx, tenantA.ID, "", CreateUserRequest{Username: "catalog_actor", Password: "secret123", Role: model.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}

	items, total, err := svc.ListConversationAssistants(ctx, actor.ID, tenantA.ID, "", nil, 1, 50)
	if err != nil {
		t.Fatalf("list assistants: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("expected one chat and one workflow agent, got total=%d items=%+v", total, items)
	}
	kinds := map[string]string{}
	for _, item := range items {
		kinds[item.Kind] = item.ID
		if item.Status != model.AssistantEffectiveActive {
			t.Fatalf("assistant should be active: %+v", item)
		}
	}
	if _, ok := kinds[model.AssistantKindChat]; !ok {
		t.Fatal("catalog must contain chat assistant")
	}
	if _, ok := kinds[model.AssistantKindAgent]; !ok {
		t.Fatal("catalog must contain workflow agent")
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("get platform admin: %v %+v", err, admin)
	}
	updated, err := svc.UpdateConversationAssistantGovernance(ctx, admin.ID, tenantA.ID,
		model.AssistantKindChat, kinds[model.AssistantKindChat], UpdateConversationAssistantGovernanceRequest{
			GovernanceStatus: ptrAssistantStatus(model.AssistantGovernanceDisabled),
			Discoverable:     ptrAssistantBool(true),
			RoutingWeight:    ptrAssistantWeight(7),
		})
	if err != nil {
		t.Fatalf("update governance: %v", err)
	}
	if _, _, err := svc.ListConversationAssistants(ctx, actor.ID, tenantA.ID, "catalog-dataflow", nil, 1, 50); err != nil {
		t.Fatalf("query dataflow: %v", err)
	}
	items, total, err = svc.ListConversationAssistants(ctx, actor.ID, tenantA.ID, "catalog-dataflow", nil, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("dataflow agent leaked into catalog: total=%d items=%+v", total, items)
	}

	items, _, err = svc.ListConversationAssistants(ctx, actor.ID, tenantA.ID, "", nil, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	var listed *ConversationAssistant
	for index := range items {
		if items[index].Kind == model.AssistantKindChat && items[index].ID == kinds[model.AssistantKindChat] {
			listed = &items[index]
			break
		}
	}
	if listed == nil {
		t.Fatal("governed chat disappeared from catalog")
	}
	if listed.GovernanceStatus != updated.GovernanceStatus || !listed.Discoverable || listed.RoutingWeight != updated.RoutingWeight {
		t.Fatalf("governance read projection mismatch: listed=%+v updated=%+v", listed, updated)
	}

	items, total, err = svc.ListConversationAssistants(ctx, actor.ID, tenantB.ID, "", nil, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("tenant isolation failed: total=%d items=%+v", total, items)
	}
}

func ptrAssistantStatus(value string) *string {
	return &value
}

func ptrAssistantBool(value bool) *bool {
	return &value
}

func ptrAssistantWeight(value float64) *float64 {
	return &value
}

func TestConversationAssistantPermissions(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "CatalogRBAC")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "catalog_operator", Password: "secret123", Role: model.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "catalog_viewer", Password: "secret123", Role: model.RoleViewer})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Authorize(ctx, operator.ID, "read", "assistant"); err != nil {
		t.Fatalf("operator read assistant: %v", err)
	}
	if err := svc.Authorize(ctx, operator.ID, "session:create", "agent"); err != nil {
		t.Fatalf("operator create agent session: %v", err)
	}
	if err := svc.Authorize(ctx, viewer.ID, "read", "assistant"); err != nil {
		t.Fatalf("viewer read assistant: %v", err)
	}
	if err := svc.Authorize(ctx, viewer.ID, "session:create", "agent"); err == nil {
		t.Fatal("viewer must not create agent sessions")
	}
	items, total, err := svc.ListConversationAssistants(ctx, viewer.ID, tenant.ID, "", nil, 1, 50)
	if err != nil {
		t.Fatalf("viewer list assistants: %v", err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("viewer must not discover executable assistants: total=%d items=%+v", total, items)
	}
}
