package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestGatewayTargetAuthorizationRequiresSamePrincipal(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Gateway Target Tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertSearchAppShadow(ctx, &model.SearchAppShadow{
		ID: "search-app-1", TenantID: tenant.ID, Name: "Search", OwnerID: "owner-1", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertAgentShadow(ctx, &model.AgentShadow{
		ID: "agent-1", TenantID: tenant.ID, Title: "Agent", OwnerID: "owner-1", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	ownerKey := &model.APIKey{TenantID: tenant.ID, UserID: "owner-1"}
	otherKey := &model.APIKey{TenantID: tenant.ID, UserID: "other-1"}

	if err := svc.AuthorizeGatewaySearchAppTarget(ctx, ownerKey, "search-app-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeGatewaySearchAppTarget(ctx, otherKey, "search-app-1"); err == nil {
		t.Fatal("expected another principal to be denied")
	}
	if err := svc.AuthorizeGatewayAgentTarget(ctx, ownerKey, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeGatewayAgentTarget(ctx, otherKey, "agent-1"); err == nil {
		t.Fatal("expected another principal to be denied")
	}
}
