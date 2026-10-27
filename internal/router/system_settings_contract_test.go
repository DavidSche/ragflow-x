package router_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

type settingRevision struct {
	ID                     string          `json:"id"`
	Revision               int64           `json:"revision"`
	Snapshot               json.RawMessage `json:"snapshot"`
	RevisionStatus         string          `json:"revision_status"`
	CreatedBy              string          `json:"created_by"`
	Note                   string          `json:"note"`
	Checksum               string          `json:"checksum"`
	RollbackFromRevisionID string          `json:"rollback_from_revision_id"`
}

type settingRevisionListResponse struct {
	Data struct {
		Items    []settingRevision `json:"items"`
		Total    int               `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"page_size"`
	} `json:"data"`
}

type settingRevisionResponse struct {
	Data settingRevision `json:"data"`
}

type settingErrorResponse struct {
	Code int `json:"code"`
}

type settingsViewResponse struct {
	Data struct {
		Revision          int64                             `json:"revision"`
		DesiredRevisionID string                            `json:"desired_revision_id"`
		Groups            map[string]map[string]interface{} `json:"groups"`
		SecretReferences  map[string]map[string]interface{} `json:"secret_references"`
		DeploymentState   string                            `json:"deployment_effective_state"`
	} `json:"data"`
}

func decodeSettingError(t *testing.T, payload []byte) int {
	t.Helper()
	var out settingErrorResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode error response %s: %v", payload, err)
	}
	return out.Code
}

func requireSettingStatus(t *testing.T, resp *http.Response, payload []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d, payload = %s", resp.StatusCode, want, payload)
	}
}

func getSettingsView(t *testing.T, app *testApp, token string) settingsViewResponse {
	t.Helper()
	resp, payload := app.doAuth(t, http.MethodGet, "/api/v1/system/settings", token, nil)
	requireSettingStatus(t, resp, payload, http.StatusOK)
	var out settingsViewResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func groupValue(t *testing.T, view settingsViewResponse, group, field string) interface{} {
	t.Helper()
	item, ok := view.Data.Groups[group][field].(map[string]interface{})
	if !ok {
		t.Fatalf("setting %s.%s missing in view: %#v", group, field, view.Data.Groups)
	}
	value, ok := item["effective"]
	if !ok {
		t.Fatalf("setting %s.%s effective value missing: %#v", group, field, item)
	}
	return value
}

// ScenarioID: SC-SETTING-001
func TestP0_SETTING_001_SettingsRouterLifecycleRollbackAndSecretBoundary(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := t.Context()

	cfg := config.Config{}
	cfg.Gateway.MaxConns = 20
	cfg.Gateway.RateLimitPerMin = 120
	if err := app.svc.InitializeSystemSettingsFromConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	token := app.login(t)
	initial := getSettingsView(t, app, token)
	if initial.Data.Revision != 1 || initial.Data.DesiredRevisionID == "" {
		t.Fatalf("unexpected initial settings view: %+v", initial.Data)
	}
	if groupValue(t, initial, "gateway", "max_conns").(float64) != 20 {
		t.Fatalf("initial gateway.max_conns = %v", groupValue(t, initial, "gateway", "max_conns"))
	}

	resp, payload := app.doAuth(t, http.MethodPatch, "/api/v1/system/settings", token, []byte(`{
		"expected_revision":1,
		"changes":{"gateway.max_conns":30},
		"note":"update gateway capacity"
	}`))
	requireSettingStatus(t, resp, payload, http.StatusOK)
	var updated settingRevisionResponse
	if err := json.Unmarshal(payload, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Data.Revision != 2 {
		t.Fatalf("updated revision = %d, want 2", updated.Data.Revision)
	}

	resp, payload = app.doAuth(t, http.MethodPatch, "/api/v1/system/settings", token, []byte(`{
		"expected_revision":1,
		"changes":{"gateway.max_conns":40},
		"note":"stale optimistic lock"
	}`))
	requireSettingStatus(t, resp, payload, http.StatusConflict)
	if code := decodeSettingError(t, payload); code != 40901 {
		t.Fatalf("revision conflict code = %d, want 40901", code)
	}

	resp, payload = app.doAuth(t, http.MethodPatch, "/api/v1/system/settings", token, []byte(`{
		"expected_revision":2,
		"changes":{"observability.metrics_token":"plaintext-secret"}
	}`))
	requireSettingStatus(t, resp, payload, http.StatusBadRequest)
	if code := decodeSettingError(t, payload); code != 40098 {
		t.Fatalf("credential update code = %d, want 40098", code)
	}

	changed := getSettingsView(t, app, token)
	if groupValue(t, changed, "gateway", "max_conns").(float64) != 30 {
		t.Fatalf("changed gateway.max_conns = %v", groupValue(t, changed, "gateway", "max_conns"))
	}

	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/system/settings/revisions?page=1&page_size=20", token, nil)
	requireSettingStatus(t, resp, payload, http.StatusOK)
	var revisions settingRevisionListResponse
	if err := json.Unmarshal(payload, &revisions); err != nil {
		t.Fatal(err)
	}
	if revisions.Data.Total != 2 || len(revisions.Data.Items) != 2 {
		t.Fatalf("revision history = %+v", revisions.Data)
	}

	resp, payload = app.doAuth(t, http.MethodGet, "/api/v1/system/settings/revisions/"+updated.Data.ID, token, nil)
	requireSettingStatus(t, resp, payload, http.StatusOK)
	var fetched settingRevisionResponse
	if err := json.Unmarshal(payload, &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Data.ID != updated.Data.ID || fetched.Data.Revision != 2 {
		t.Fatalf("revision fetch mismatch: %+v", fetched.Data)
	}

	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/system/settings/revisions/"+initial.Data.DesiredRevisionID+"/rollback", token, []byte(`{
		"confirmed":true,
		"note":"short"
	}`))
	requireSettingStatus(t, resp, payload, http.StatusBadRequest)
	if code := decodeSettingError(t, payload); code != 40098 {
		t.Fatalf("short rollback note code = %d, want 40098", code)
	}
	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/system/settings/revisions/"+initial.Data.DesiredRevisionID+"/rollback", token, []byte(`{
		"confirmed":true,
		"note":"restore reviewed gateway baseline"
	}`))
	requireSettingStatus(t, resp, payload, http.StatusOK)
	var rolled settingRevisionResponse
	if err := json.Unmarshal(payload, &rolled); err != nil {
		t.Fatal(err)
	}
	if rolled.Data.Revision != 3 || rolled.Data.RollbackFromRevisionID != initial.Data.DesiredRevisionID {
		t.Fatalf("rollback revision mismatch: %+v", rolled.Data)
	}
	restored := getSettingsView(t, app, token)
	if groupValue(t, restored, "gateway", "max_conns").(float64) != 20 {
		t.Fatalf("restored gateway.max_conns = %v", groupValue(t, restored, "gateway", "max_conns"))
	}

	sealed, err := app.rsa.Encrypt("rotated-metrics-secret")
	if err != nil {
		t.Fatal(err)
	}
	resp, payload = app.doAuth(t, http.MethodPost, "/api/v1/system/settings/secrets/observability.metrics_token", token, []byte(`{
		"secret":"`+sealed+`",
		"confirmed":true,
		"note":"rotate metrics collector token"
	}`))
	requireSettingStatus(t, resp, payload, http.StatusOK)
	var secretRevision settingRevisionResponse
	if err := json.Unmarshal(payload, &secretRevision); err != nil {
		t.Fatal(err)
	}
	if secretRevision.Data.Revision != 4 || !strings.Contains(string(secretRevision.Data.Snapshot), "secret_refs") {
		t.Fatalf("secret revision mismatch: %+v", secretRevision.Data)
	}
	plaintext, err := app.svc.SettingSecretValue(ctx, "observability.metrics_token")
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "rotated-metrics-secret" {
		t.Fatalf("rotated secret was not applied: %q", plaintext)
	}
	finalPayload := getSettingsView(t, app, token)
	raw, err := json.Marshal(finalPayload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "rotated-metrics-secret") {
		t.Fatal("settings view leaked secret plaintext")
	}
	if finalPayload.Data.SecretReferences["observability.metrics_token"] == nil {
		t.Fatalf("secret reference missing: %#v", finalPayload.Data.SecretReferences)
	}
}
