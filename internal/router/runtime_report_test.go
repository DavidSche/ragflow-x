package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRuntimeReportEndpointUsesServiceToken(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	if err := app.svc.InitializeSystemSettingsFromConfig(t.Context(), config.Config{
		Runtime: config.Runtime{ReportToken: "service-secret", HeartbeatTimeoutSec: 30},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := app.svc.Store.GetCurrentSettingRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]string{
		"runtime_revision_id": current.ID,
		"runtime_instance_id": "runtime-service",
		"instance_identity":   "node-service",
		"apply_status":        model.RuntimeApplyApplied,
	}
	body, _ := json.Marshal(report)
	request, err := http.NewRequest(http.MethodPost, app.ts.URL+"/api/v1/system/settings/runtime/report", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer service-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("service token report status: %d", response.StatusCode)
	}

	userToken := app.login(t)
	request, err = http.NewRequest(http.MethodPost, app.ts.URL+"/api/v1/system/settings/runtime/report", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+userToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("user token report status: %d", response.StatusCode)
	}
}
