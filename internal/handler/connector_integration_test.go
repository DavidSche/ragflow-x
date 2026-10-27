package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// connectorAccessRules mirrors internal/router/access.go for the connector
// resource; kept inline to avoid an import cycle handler -> router.
func connectorAccessRules() []middleware.Rule {
	return []middleware.Rule{
		{Method: "GET", Path: "/api/v1/connectors", Action: "read", Resource: "connector"},
		{Method: "GET", Path: "/api/v1/connectors/providers/meta", Action: "read", Resource: "connector"},
		{Method: "POST", Path: "/api/v1/connectors", Action: "manage", Resource: "connector"},
		{Method: "POST", Path: "/api/v1/connectors/test", Action: "manage", Resource: "connector"},
		{Method: "GET", Path: "/api/v1/connectors/:id", Action: "read", Resource: "connector"},
		{Method: "PUT", Path: "/api/v1/connectors/:id", Action: "manage", Resource: "connector"},
		{Method: "DELETE", Path: "/api/v1/connectors/:id", Action: "manage", Resource: "connector"},
		{Method: "POST", Path: "/api/v1/connectors/:id/enable", Action: "manage", Resource: "connector"},
		{Method: "POST", Path: "/api/v1/connectors/:id/disable", Action: "manage", Resource: "connector"},
		{Method: "POST", Path: "/api/v1/connectors/:id/test", Action: "manage", Resource: "connector"},
		{Method: "POST", Path: "/api/v1/connectors/:id/sync", Action: "manage", Resource: "connector"},
		{Method: "PUT", Path: "/api/v1/connectors/:id/schedule", Action: "manage", Resource: "connector"},
		{Method: "GET", Path: "/api/v1/connectors/:id/runs", Action: "read", Resource: "connector"},
		{Method: "GET", Path: "/api/v1/connectors/:id/runs/:runID", Action: "read", Resource: "connector"},
		{Method: "POST", Path: "/api/v1/connectors/:id/preview", Action: "manage", Resource: "connector"},
	}
}

// buildConnectorRouter overrides the testEnv router to include the connector
// RBAC rules and route group.
func (e *testEnv) buildConnectorRouter(t *testing.T, h *Handler) {
	r := gin.New()
	r.Use(middleware.Auth(e.svc.JWT, e.svc))
	r.Use(middleware.RBAC(e.svc, connectorAccessRules()))
	protected := r.Group("/api/v1")
	{
		protected.GET("/connectors", h.ListConnectors)
		protected.GET("/connectors/providers/meta", h.ListConnectorProvidersMeta)
		protected.POST("/connectors", h.CreateConnector)
		protected.POST("/connectors/test", h.TestConnector)
		protected.GET("/connectors/:id", h.GetConnector)
		protected.PUT("/connectors/:id", h.UpdateConnector)
		protected.DELETE("/connectors/:id", h.DeleteConnector)
		protected.POST("/connectors/:id/enable", h.SetConnectorEnabled)
		protected.POST("/connectors/:id/disable", h.SetConnectorEnabled)
		protected.POST("/connectors/:id/test", h.TestConnectorByID)
		protected.POST("/connectors/:id/sync", h.SyncConnectorNow)
		protected.PUT("/connectors/:id/schedule", h.SetConnectorSchedule)
		protected.GET("/connectors/:id/runs", h.ListConnectorRuns)
		protected.GET("/connectors/:id/runs/:runID", h.GetConnectorRun)
		protected.POST("/connectors/:id/preview", h.PreviewConnector)
	}
	e.router = r
}

func TestIntegration_ConnectorLifecycle(t *testing.T) {
	e := setupTestEnv(t)
	h := New(e.svc)
	e.buildConnectorRouter(t, h)

	// RBAC negative: no token -> 401/403, never 200.
	resp := e.doRequest(t, http.MethodGet, "/api/v1/connectors", nil, "")
	if resp.Code == http.StatusOK {
		t.Fatalf("anonymous connector list must be denied, got %d", resp.Code)
	}

	token := e.tokenFor(t, e.adminID, e.tenantID, model.RolePlatformAdmin)

	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors/providers/meta", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("providers meta = %d %s", resp.Code, resp.Body.String())
	}
	meta := jsonResponse(t, resp)
	if _, ok := meta["data"]; !ok {
		t.Fatalf("meta missing data: %s", resp.Body.String())
	}

	if err := e.svc.Store.CreateDatasetLink(context.Background(), &model.DatasetLink{
		ID: "ds-conn-1", TenantID: e.tenantID, RAGFlowDatasetID: "rf-ds-conn-1", Name: "connector-target",
	}); err != nil {
		t.Fatalf("create target dataset link: %v", err)
	}
	root := t.TempDir()
	create := e.doRequest(t, http.MethodPost, "/api/v1/connectors", map[string]any{
		"name":              "local-connector",
		"provider":          model.ConnectorProviderLocalFolder,
		"target_dataset_id": "ds-conn-1",
		"spec":              map[string]any{"root": root},
		"credential":        map[string]any{"note": "plain"},
		"schedule":          "manual",
	}, token)
	// Without a policy, ApprovalGate lets control-plane create pass through
	// directly. If a default escalation kicks in the request only holds.
	if create.Code == http.StatusAccepted {
		t.Logf("connector create held for approval (202), skipping direct lifecycle assertions")
		return
	}
	if create.Code != http.StatusOK {
		t.Fatalf("create connector = %d %s", create.Code, create.Body.String())
	}
	body := jsonResponse(t, create)
	data, _ := body["data"].(map[string]any)
	connID, _ := data["id"].(string)
	if connID == "" {
		t.Fatalf("create returned no connector id: %s", create.Body.String())
	}
	if data["created_by"] != e.adminID || data["enabled"] != true {
		t.Fatalf("connector audit fields = created_by:%v enabled:%v", data["created_by"], data["enabled"])
	}
	if data["adapter_kind"] != model.ConnectorAdapterNative || data["credential_version"] != float64(1) {
		t.Fatalf("connector identity fields = adapter:%v credential_version:%v", data["adapter_kind"], data["credential_version"])
	}
	if key, _ := data["source_ownership_key"].(string); key == "" {
		t.Fatalf("connector ownership key missing: %s", create.Body.String())
	}
	// One logical source may only be bound once per tenant + dataset.
	dup := e.doRequest(t, http.MethodPost, "/api/v1/connectors", map[string]any{
		"name":              "local-connector-copy",
		"provider":          model.ConnectorProviderLocalFolder,
		"target_dataset_id": "ds-conn-1",
		"spec":              map[string]any{"root": root},
		"schedule":          "manual",
	}, token)
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate source must be rejected = %d %s", dup.Code, dup.Body.String())
	}
	// A legacy row can carry a non-canonical ownership key. The service must
	// still detect the source conflict by canonicalizing its spec, otherwise
	// deleting the canonical row would allow a duplicate to survive.
	first, err := e.svc.Store.GetConnector(context.Background(), e.tenantID, connID)
	if err != nil {
		t.Fatalf("reload connector: %v", err)
	}
	first.SourceOwnershipKey = "legacy:" + first.ID
	if ok, err := e.svc.Store.UpdateConnector(context.Background(), first); err != nil || !ok {
		t.Fatalf("rewrite ownership key to legacy: ok=%v err=%v", ok, err)
	}
	legacyDup := e.doRequest(t, http.MethodPost, "/api/v1/connectors", map[string]any{
		"name":              "local-connector-legacy-copy",
		"provider":          model.ConnectorProviderLocalFolder,
		"target_dataset_id": "ds-conn-1",
		"spec":              map[string]any{"root": root + "/"},
		"schedule":          "manual",
	}, token)
	if legacyDup.Code != http.StatusConflict {
		t.Fatalf("legacy source duplicate must be rejected = %d %s", legacyDup.Code, legacyDup.Body.String())
	}
	// credential plaintext must never leak in responses
	if raw := create.Body.String(); containsJSONKey(raw, "credential_crypted") || containsJSONKey(raw, "note") {
		t.Fatalf("credential leaked in create response: %s", raw)
	}

	// list contains the new connector
	got := e.doRequest(t, http.MethodGet, "/api/v1/connectors", nil, token)
	if got.Code != http.StatusOK {
		t.Fatalf("list = %d %s", got.Code, got.Body.String())
	}
	// single read
	single := e.doRequest(t, http.MethodGet, "/api/v1/connectors/"+connID, nil, token)
	if single.Code != http.StatusOK {
		t.Fatalf("get = %d %s", single.Code, single.Body.String())
	}
	if raw := single.Body.String(); containsJSONKey(raw, "note") {
		t.Fatalf("credential leaked in get response: %s", raw)
	}

	// connectivity test against a real temp dir
	testResp := e.doRequest(t, http.MethodPost, "/api/v1/connectors/test", map[string]any{
		"provider": model.ConnectorProviderLocalFolder,
		"spec":     map[string]any{"root": root},
	}, token)
	if testResp.Code != http.StatusOK {
		t.Fatalf("test = %d %s", testResp.Code, testResp.Body.String())
	}

	// persisted probe is read-only and reports reachability
	probe := e.doRequest(t, http.MethodPost, "/api/v1/connectors/"+connID+"/test", nil, token)
	if probe.Code != http.StatusOK {
		t.Fatalf("probe = %d %s", probe.Code, probe.Body.String())
	}

	// dry-run preview never writes documents or the cursor
	preview := e.doRequest(t, http.MethodPost, "/api/v1/connectors/"+connID+"/preview", map[string]any{"limit": 10}, token)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
	}
	if pv, _ := jsonResponse(t, preview)["data"].(map[string]any); pv == nil {
		t.Fatalf("preview missing data: %s", preview.Body.String())
	}

	// schedule update (doc/225 §7.2 PUT /schedule)
	sched := e.doRequest(t, http.MethodPut, "/api/v1/connectors/"+connID+"/schedule", map[string]any{
		"kind": model.ConnectorScheduleInterval, "minutes": 15,
	}, token)
	if sched.Code != http.StatusOK {
		t.Fatalf("schedule = %d %s", sched.Code, sched.Body.String())
	}
	// doc/225 F7: daily object HHMM is wall-clock local (+08:00), stored as UTC.
	daily := e.doRequest(t, http.MethodPut, "/api/v1/connectors/"+connID+"/schedule", map[string]any{
		"schedule": map[string]any{"kind": "daily", "hhmm": "09:00"},
	}, token)
	if daily.Code != http.StatusOK {
		t.Fatalf("daily schedule = %d %s", daily.Code, daily.Body.String())
	}
	if data, _ := jsonResponse(t, daily)["data"].(map[string]any); data == nil ||
		data["schedule_value"] != float64(60) || data["schedule_tz_offset_min"] != float64(480) {
		t.Fatalf("daily schedule must store UTC minute 60: %s", daily.Body.String())
	}
	badSched := e.doRequest(t, http.MethodPut, "/api/v1/connectors/"+connID+"/schedule", map[string]any{
		"kind": model.ConnectorScheduleInterval, "minutes": 1,
	}, token)
	if badSched.Code == http.StatusOK {
		t.Fatalf("invalid schedule must be rejected: %s", badSched.Body.String())
	}

	// runs history initially empty
	runs := e.doRequest(t, http.MethodGet, "/api/v1/connectors/"+connID+"/runs", nil, token)
	if runs.Code != http.StatusOK {
		t.Fatalf("runs = %d %s", runs.Code, runs.Body.String())
	}

	// unknown run detail is a scoped 404, never another tenant's row
	missingRun := e.doRequest(t, http.MethodGet, "/api/v1/connectors/"+connID+"/runs/run-missing", nil, token)
	if missingRun.Code != http.StatusNotFound {
		t.Fatalf("missing run = %d %s", missingRun.Code, missingRun.Body.String())
	}

	// run detail exposes parsed skip reasons and the triggering actor.
	runID := "run-detail-1"
	if err := e.svc.Store.CreateConnectorRun(context.Background(), &model.ConnectorRun{
		ID: runID, ConnectorID: connID, TenantID: e.tenantID,
		Trigger: model.ConnectorRunTriggerManual, TriggeredBy: e.adminID,
		Status:     model.ConnectorRunStatusSucceeded,
		DetailJSON: `{"skipped":[{"key":"huge.bin","reason":"large","size":27262976}],"skipped_unchanged":2}`,
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	runDetail := e.doRequest(t, http.MethodGet, "/api/v1/connectors/"+connID+"/runs/"+runID, nil, token)
	if runDetail.Code != http.StatusOK {
		t.Fatalf("run detail = %d %s", runDetail.Code, runDetail.Body.String())
	}
	rd, _ := jsonResponse(t, runDetail)["data"].(map[string]any)
	if rd["triggered_by"] != e.adminID {
		t.Fatalf("run triggered_by = %v", rd["triggered_by"])
	}
	detail, _ := rd["detail"].(map[string]any)
	skips, _ := detail["skipped"].([]any)
	if len(skips) != 1 || skips[0].(map[string]any)["reason"] != "large" {
		t.Fatalf("run detail skipped = %s", runDetail.Body.String())
	}

	// sync enqueue (durable queue), then disable/enable
	if e.svc.Runner == nil {
		e.svc.Runner = service.NewRunner(e.svc.Store, service.WorkerConfig{})
	}
	syncT := e.doRequest(t, http.MethodPost, "/api/v1/connectors/"+connID+"/sync", nil, token)
	if syncT.Code != http.StatusOK {
		t.Fatalf("sync = %d %s", syncT.Code, syncT.Body.String())
	}
	// Credential rotation is versioned and must not leak plaintext.
	rot := e.doRequest(t, http.MethodPut, "/api/v1/connectors/"+connID, map[string]any{
		"credential": map[string]any{"note": "rotated"},
	}, token)
	if rot.Code == http.StatusAccepted {
		t.Logf("credential rotation held for approval (202)")
	} else if rot.Code != http.StatusOK {
		t.Fatalf("credential rotate = %d %s", rot.Code, rot.Body.String())
	} else if d, _ := jsonResponse(t, rot)["data"].(map[string]any); d["credential_version"] != float64(2) {
		t.Fatalf("credential version = %v, want 2: %s", d["credential_version"], rot.Body.String())
	} else if containsJSONKey(rot.Body.String(), "rotated") {
		t.Fatalf("rotated credential leaked: %s", rot.Body.String())
	}

	// enabled is an explicit model field (doc/225 §5.1), not only a status.
	updOff := e.doRequest(t, http.MethodPut, "/api/v1/connectors/"+connID, map[string]any{"enabled": false}, token)
	if updOff.Code != http.StatusOK {
		t.Fatalf("update disable = %d %s", updOff.Code, updOff.Body.String())
	}
	if d, _ := jsonResponse(t, updOff)["data"].(map[string]any); d["enabled"] != false || d["status"] != model.ConnectorStatusDisabled {
		t.Fatalf("update disable fields = %s", updOff.Body.String())
	}
	updOn := e.doRequest(t, http.MethodPut, "/api/v1/connectors/"+connID, map[string]any{"enabled": true}, token)
	if updOn.Code != http.StatusOK {
		t.Fatalf("update enable = %d %s", updOn.Code, updOn.Body.String())
	}
	if d, _ := jsonResponse(t, updOn)["data"].(map[string]any); d["enabled"] != true || d["status"] == model.ConnectorStatusDisabled {
		t.Fatalf("update enable fields = %s", updOn.Body.String())
	}
	dis := e.doRequest(t, http.MethodPost, "/api/v1/connectors/"+connID+"/disable", nil, token)
	if dis.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", dis.Code, dis.Body.String())
	}
	en := e.doRequest(t, http.MethodPost, "/api/v1/connectors/"+connID+"/enable", nil, token)
	if en.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", en.Code, en.Body.String())
	}
}

func containsJSONKey(raw, key string) bool {
	for i := 0; i+len(key) <= len(raw); i++ {
		if raw[i:i+len(key)] == key {
			return true
		}
	}
	return false
}

// TestIntegration_ConnectorScopeAndRBACBoundaries pins the governance surface:
// viewers never gain connector access through navigation, and cross-workspace
// reads require the explicit sensitive-governance permission instead of a
// tenant-scoped token silently widening its scope.
func TestIntegration_ConnectorScopeAndRBACBoundaries(t *testing.T) {
	e := setupTestEnv(t)
	h := New(e.svc)
	e.buildConnectorRouter(t, h)

	ctx := context.Background()
	if err := e.svc.Store.CreateTenant(ctx, &model.Tenant{ID: "tenant-conn-2", Name: "connector-other", Status: "active"}); err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	seed := func(id, tenantID, key string) {
		t.Helper()
		if err := e.svc.Store.CreateConnector(ctx, &model.Connector{
			ID: id, TenantID: tenantID, Name: id, Provider: model.ConnectorProviderLocalFolder,
			AdapterKind: model.ConnectorAdapterNative, TargetDatasetID: "ds-" + id,
			SourceOwnershipKey: key, SpecJSON: `{"root":"/srv/` + id + `"}`,
			ScheduleKind: model.ConnectorScheduleManual, Enabled: true, Status: model.ConnectorStatusIdle,
		}); err != nil {
			t.Fatalf("seed connector %s: %v", id, err)
		}
	}
	seed("conn-scope-a", e.tenantID, "scope:tenant-a:local")
	seed("conn-scope-b", "tenant-conn-2", "scope:tenant-b:local")

	viewerUser, err := e.svc.CreateUser(ctx, e.tenantID, e.adminID, service.CreateUserRequest{
		Username: "viewer-" + e.tenantID, Password: "password123", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewer := e.tokenFor(t, viewerUser.ID, e.tenantID, model.RoleViewer)
	resp := e.doRequest(t, http.MethodGet, "/api/v1/connectors", nil, viewer)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer connector list = %d %s, want 403", resp.Code, resp.Body.String())
	}
	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors/conn-scope-a", nil, viewer)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer connector read = %d %s, want 403", resp.Code, resp.Body.String())
	}

	tenantAdmin := e.tokenFor(t, e.adminID, e.tenantID, model.RoleTenantAdmin)
	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors", nil, tenantAdmin)
	if resp.Code != http.StatusOK {
		t.Fatalf("tenant admin current list = %d %s", resp.Code, resp.Body.String())
	}
	body := jsonResponse(t, resp)
	data, _ := body["data"].(map[string]any)
	if data["total"] != float64(1) {
		t.Fatalf("tenant admin current list leaked rows: %s", resp.Body.String())
	}
	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors/conn-scope-b", nil, tenantAdmin)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant connector read = %d %s, want scoped 404", resp.Code, resp.Body.String())
	}
	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors?scope=all", nil, tenantAdmin)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("tenant admin scope=all = %d %s, want 403", resp.Code, resp.Body.String())
	}
	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors?scope=specific&tenant_id=tenant-conn-2", nil, tenantAdmin)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("tenant admin specific other tenant = %d %s, want 403", resp.Code, resp.Body.String())
	}

	platformAdmin := e.tokenFor(t, e.platformID, e.tenantID, model.RolePlatformAdmin)
	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors?scope=all", nil, platformAdmin)
	if resp.Code != http.StatusOK {
		t.Fatalf("platform scope=all = %d %s", resp.Code, resp.Body.String())
	}
	data, _ = jsonResponse(t, resp)["data"].(map[string]any)
	if data["total"] != float64(2) {
		t.Fatalf("platform scope=all total = %v, want 2: %s", data["total"], resp.Body.String())
	}
	resp = e.doRequest(t, http.MethodGet, "/api/v1/connectors?scope=specific&tenant_id=tenant-conn-2", nil, platformAdmin)
	if resp.Code != http.StatusOK {
		t.Fatalf("platform specific tenant = %d %s", resp.Code, resp.Body.String())
	}
	data, _ = jsonResponse(t, resp)["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "conn-scope-b" {
		t.Fatalf("specific scope returned wrong rows: %s", resp.Body.String())
	}

	// doc/225 §7.2: preview limit is capped at 50; over-limit is 40337, not a
	// silently truncated scan.
	resp = e.doRequest(t, http.MethodPost, "/api/v1/connectors/conn-scope-a/preview", map[string]any{"limit": 51}, platformAdmin)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("preview limit 51 = %d %s, want 40337 bad request", resp.Code, resp.Body.String())
	}
	if code, _ := jsonResponse(t, resp)["code"].(float64); code != 40337 {
		t.Fatalf("preview limit 51 code = %v, want 40337: %s", code, resp.Body.String())
	}
}
