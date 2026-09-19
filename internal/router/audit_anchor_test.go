package router_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func TestE2E_AuditAnchorListRBACAndTenantIsolation(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()

	first, err := app.svc.CreateTenant(ctx, "anchor-first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.svc.CreateTenant(ctx, "anchor-second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.svc.CreateUser(ctx, first.ID, "", service.CreateUserRequest{
		Username: "anchor-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.RecordAudit(ctx, &model.AuditLog{TenantID: first.ID, Action: "one", Resource: "test", ResourceID: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.RecordAudit(ctx, &model.AuditLog{TenantID: second.ID, Action: "two", Resource: "test", ResourceID: "2"}); err != nil {
		t.Fatal(err)
	}

	summary, err := app.svc.RunAuditAnchoring(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Enabled {
		t.Fatal("test app audit anchoring should be disabled by policy")
	}
	app.svc.SetAuditAnchorPolicy(config.AuditAnchor{Enabled: true, IntervalSec: 3600})
	summary, err = app.svc.RunAuditAnchoring(ctx)
	if err != nil || summary.Anchored != 2 {
		t.Fatalf("anchor summary = %+v err=%v", summary, err)
	}

	tenantToken := loginAs(t, app, "anchor-admin", "secret123")
	resp, body := app.doAuth(t, http.MethodGet, "/api/v1/audit/anchors", tenantToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant admin status %d: %s", resp.StatusCode, body)
	}
	var list struct {
		Data struct {
			Items []struct {
				TenantID string `json:"tenant_id"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := decodeBody(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 1 || len(list.Data.Items) != 1 || list.Data.Items[0].TenantID != first.ID {
		t.Fatalf("tenant anchor list = %+v", list.Data)
	}

	adminToken := app.login(t)
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/audit/anchors", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("platform admin status %d: %s", resp.StatusCode, body)
	}
	if err := decodeBody(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 2 || len(list.Data.Items) != 2 {
		t.Fatalf("platform anchor list = %+v", list.Data)
	}
}
