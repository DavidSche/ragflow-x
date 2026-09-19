package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type tenantProjectResource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
	TenantID    string `json:"tenant_id"`
}

type tenantProjectListResponse struct {
	Data struct {
		Items    []tenantProjectResource `json:"items"`
		Total    int                     `json:"total"`
		Page     int                     `json:"page"`
		PageSize int                     `json:"page_size"`
	} `json:"data"`
}

type tenantProjectResponse struct {
	Data tenantProjectResource `json:"data"`
}

type tenantProjectArrayResponse struct {
	Data []tenantProjectResource `json:"data"`
}

type tenantProjectErrorResponse struct {
	Code int `json:"code"`
}

type auditRecord struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Detail   string `json:"detail_json"`
	Hash     string `json:"hash"`
	PrevHash string `json:"prev_hash"`
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
}

type auditListResponse struct {
	Data struct {
		Items []auditRecord `json:"items"`
		Total int           `json:"total"`
	} `json:"data"`
}

func loginAsUser(t *testing.T, app *testApp, username, password string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	resp, payload := app.doAuth(t, http.MethodPost, "/api/v1/auth/login", "", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d payload %s", username, resp.StatusCode, payload)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Token == "" {
		t.Fatalf("login %s returned an empty token", username)
	}
	return out.Data.Token
}

func errorCode(t *testing.T, payload []byte) int {
	t.Helper()
	var out tenantProjectErrorResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode error response %s: %v", payload, err)
	}
	return out.Code
}

func requireTenantProjectStatus(t *testing.T, resp *http.Response, payload []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d, payload = %s", resp.StatusCode, want, payload)
	}
}

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_AuditContractScopeExportAndIntegrity(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()
	platformToken := app.login(t)

	tenant, err := app.svc.CreateTenant(ctx, "audit-contract")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := app.svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "audit-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"dataset.create", "dataset.delete", "project.create"} {
		entry := appAuditRequest(tenant.ID, admin.ID, action)
		if err := app.svc.RecordAudit(ctx, &entry); err != nil {
			t.Fatal(err)
		}
	}
	token := loginAsUser(t, app, admin.Username, "secret123")

	resp, payload := app.doAuth(t, http.MethodGet, "/api/v1/audit?action=dataset&page=1&page_size=100", token, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var list auditListResponse
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 2 || len(list.Data.Items) != 2 {
		t.Fatalf("filtered audit list = %+v", list.Data)
	}
	for _, item := range list.Data.Items {
		if item.TenantID != tenant.ID || !strings.HasPrefix(item.Action, "dataset") {
			t.Fatalf("audit list leaked or mistiltered: %+v", item)
		}
	}

	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/audit/export", token, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/csv") {
		t.Fatalf("audit export content type = %q", contentType)
	}
	lines := strings.Count(string(payload), "\n")
	if lines < 4 {
		t.Fatalf("audit export rows = %d, want header plus 3 records", lines)
	}

	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/audit/verify", token, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var verify struct {
		Data struct {
			Valid bool `json:"valid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &verify); err != nil {
		t.Fatal(err)
	}
	if !verify.Data.Valid {
		t.Fatalf("audit chain should be valid: %s", payload)
	}

	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/audit/repair", platformToken, []byte(`{}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusBadRequest)
	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/audit/repair", platformToken, []byte(`{"tenant_id":"`+tenant.ID+`"}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	if err := json.Unmarshal(payload, &verify); err != nil {
		t.Fatal(err)
	}
	if !verify.Data.Valid {
		t.Fatalf("audit chain repair did not verify: %s", payload)
	}
}

func appAuditRequest(tenantID, userID, action string) model.AuditLog {
	return model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: action,
		Resource: "contract", ResourceID: action, DetailJSON: `{"safe":"ok"}`,
	}
}

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_TenantLifecycleIsScopedAndProtected(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	name := "tenant-contract-" + id.New()[:8]

	resp, payload := app.doAuth(t, http.MethodPost, "/api/v1/tenants", token, []byte(`{
		"name":"`+name+`",
		"brand_name":"Contract Brand",
		"brand_logo":"https://example.test/logo.svg"
	}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var created tenantProjectResponse
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.ID == "" || created.Data.Name != name || created.Data.Status != "active" {
		t.Fatalf("unexpected tenant: %+v", created.Data)
	}

	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/tenants", token, []byte(`{"name":"`+name+`"}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusConflict)
	if code := errorCode(t, payload); code != 40911 {
		t.Fatalf("duplicate tenant error code = %d, want 40911", code)
	}

	if _, err := app.svc.CreateUser(context.Background(), created.Data.ID, "", service.CreateUserRequest{
		Username: "tenant-contract-user",
		Password: "secret123",
		Role:     "operator",
	}); err != nil {
		t.Fatal(err)
	}

	resp, payload = app.doAuth(t, http.MethodDelete, "/api/v1/tenants/"+created.Data.ID, token, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusBadRequest)
	if code := errorCode(t, payload); code != 40004 {
		t.Fatalf("non-forced tenant delete error code = %d, want 40004", code)
	}

	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/tenants", token, []byte(`{"name":"tenant-contract-other"}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var other tenantProjectResponse
	if err := json.Unmarshal(payload, &other); err != nil {
		t.Fatal(err)
	}

	resp, payload = app.doAuth(t, http.MethodPut, "/api/v1/tenants/status", token, []byte(`{
		"ids":["`+other.Data.ID+`","`+created.Data.ID+`"],
		"status":"disabled"
	}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)

	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/tenants?scope=all&status=disabled&page=1&page_size=100", token, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var list tenantProjectListResponse
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range list.Data.Items {
		statuses[item.ID] = item.Status
	}
	if statuses[created.Data.ID] != "disabled" || statuses[other.Data.ID] != "disabled" {
		t.Fatalf("batch tenant status missing: %#v", statuses)
	}

	resp, payload = app.doAuth(t, http.MethodDelete, "/api/v1/tenants/"+created.Data.ID+"?force=true", token, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/tenants?page=1&page_size=200", token, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var listAfterDelete tenantProjectListResponse
	if err := json.Unmarshal(payload, &listAfterDelete); err != nil {
		t.Fatal(err)
	}
	for _, item := range listAfterDelete.Data.Items {
		if item.ID == created.Data.ID {
			t.Fatal("force-deleted tenant remains in the tenant list")
		}
	}
}

// ScenarioID: SC-PROJ-001
func TestP0_PROJ_001_ProjectLifecycleMemberBoundaryAndAudit(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()

	tenantA, err := app.svc.CreateTenant(ctx, "project-contract-a")
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := app.svc.CreateTenant(ctx, "project-contract-b")
	if err != nil {
		t.Fatal(err)
	}
	adminA, err := app.svc.CreateUser(ctx, tenantA.ID, "", service.CreateUserRequest{
		Username: "project-admin-a", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := app.svc.CreateUser(ctx, tenantA.ID, "", service.CreateUserRequest{
		Username: "project-member-a", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := app.svc.CreateUser(ctx, tenantB.ID, "", service.CreateUserRequest{
		Username: "project-admin-b", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminToken := loginAsUser(t, app, adminA.Username, "secret123")
	memberToken := loginAsUser(t, app, member.Username, "secret123")
	foreignToken := loginAsUser(t, app, foreign.Username, "secret123")

	resp, payload := app.doAuth(t, http.MethodPost, "/api/v1/projects", adminToken, []byte(`{
		"name":"Delivery Platform",
		"description":"contract project"
	}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var project tenantProjectResponse
	if err := json.Unmarshal(payload, &project); err != nil {
		t.Fatal(err)
	}
	if project.Data.ID == "" || project.Data.TenantID != tenantA.ID {
		t.Fatalf("unexpected project: %+v", project.Data)
	}

	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/projects", adminToken, []byte(`{"name":"Delivery Platform"}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusConflict)
	if code := errorCode(t, payload); code != 40911 {
		t.Fatalf("duplicate project error code = %d, want 40911", code)
	}

	if _, err := app.svc.CreateProject(ctx, tenantA.ID, "Hidden Project", ""); err != nil {
		t.Fatal(err)
	}
	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/projects?page=1&page_size=100", adminToken, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var adminList tenantProjectArrayResponse
	if err := json.Unmarshal(payload, &adminList); err != nil {
		t.Fatal(err)
	}
	if len(adminList.Data) != 2 {
		t.Fatalf("tenant admin project list = %+v", adminList)
	}

	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/projects?page=1&page_size=100", memberToken, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	var memberList tenantProjectArrayResponse
	if err := json.Unmarshal(payload, &memberList); err != nil {
		t.Fatal(err)
	}
	if len(memberList.Data) != 0 {
		t.Fatalf("non-member project list = %+v", memberList)
	}

	resp, _ = app.doAuth(t, http.MethodGet, "/api/v1/projects/"+project.Data.ID, memberToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-member project show status = %d, want 403", resp.StatusCode)
	}
	resp, _ = app.doAuth(t, http.MethodGet, "/api/v1/projects/"+project.Data.ID, foreignToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant project show status = %d, want neutral 404", resp.StatusCode)
	}

	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/projects/"+project.Data.ID+"/users", adminToken, []byte(`{
		"user_id":"`+member.ID+`"
	}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/projects", memberToken, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	if err := json.Unmarshal(payload, &memberList); err != nil {
		t.Fatal(err)
	}
	if len(memberList.Data) != 1 || memberList.Data[0].ID != project.Data.ID {
		t.Fatalf("project member list = %+v", memberList.Data)
	}

	resp, payload = app.doAuth(t, http.MethodPut, "/api/v1/projects/"+project.Data.ID, adminToken, []byte(`{
		"name":"Delivery Platform Renamed",
		"description":"updated contract project"
	}`))
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	if err := json.Unmarshal(payload, &project); err != nil {
		t.Fatal(err)
	}
	if project.Data.Name != "Delivery Platform Renamed" || project.Data.Description != "updated contract project" {
		t.Fatalf("project was not updated: %+v", project.Data)
	}

	resp, payload = app.doAuth(t, http.MethodDelete, "/api/v1/projects/"+project.Data.ID+"/users/"+member.ID, adminToken, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	resp, _ = app.doAuth(t, http.MethodGet, "/api/v1/projects/"+project.Data.ID, memberToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("removed member project show status = %d, want 403", resp.StatusCode)
	}

	resp, payload = app.doAuth(t, http.MethodDelete, "/api/v1/projects/missing-project", adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing project delete status = %d, want 404, payload = %s", resp.StatusCode, payload)
	}

	resp, payload = app.doAuth(t, http.MethodDelete, "/api/v1/projects/"+project.Data.ID, adminToken, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusOK)
	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/projects/"+project.Data.ID, adminToken, nil)
	requireTenantProjectStatus(t, resp, payload, http.StatusNotFound)

	audits, _, err := app.svc.Store.ListAudits(ctx, tenantA.ID, 1, 100, repository.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, audit := range audits {
		found[audit.Action] = true
	}
	for _, action := range []string{"project.create", "project.update", "project.member.add", "project.member.remove", "project.delete"} {
		if !found[action] {
			t.Fatalf("project audit action %q missing: %#v", action, found)
		}
	}
}
