package router_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestE2E_EvaluationProtocolRegistrationGovernance exercises the M205
// preregistration governance surface over HTTP: registration freezes the
// canonical protocol snapshot + hash, listing is tenant-scoped, and the
// revoke lifecycle is one-way. Execution-time fail-closed enforcement is
// covered by internal/service tests.
func TestE2E_EvaluationProtocolRegistrationGovernance(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	const protocol = `{
		"contract_version":"scoped-paired-ab-v3","top_k":8,"min_samples":30,
		"min_hit_lift_pp":0,"max_p95_latency_delta_ms":2500,
		"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
		"min_citation_precision_lift_pp":20,"min_compiled_artifact_effect_pairs":15,
		"answer_correctness_method":"expected_evidence_match",
		"require_citation_precision":true
	}`

	// Unauthenticated requests are denied.
	resp, _ := app.doAuth(t, http.MethodGet, "/api/v1/evaluation-protocol-registrations", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status: %d", resp.StatusCode)
	}

	registerBody, _ := json.Marshal(map[string]interface{}{
		"capability": "Wiki",
		"protocol":   json.RawMessage(protocol),
		"note":       "wiki v3 preregistration",
	})
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/evaluation-protocol-registrations", token, registerBody)
	if resp.StatusCode != 200 {
		t.Fatalf("register status %d: %s", resp.StatusCode, body)
	}
	var registered struct {
		Data struct {
			ID               string `json:"id"`
			Capability       string `json:"capability"`
			ContractVersion  string `json:"contract_version"`
			ProtocolHash     string `json:"protocol_hash"`
			ProtocolSnapshot string `json:"protocol_snapshot"`
			Status           string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &registered); err != nil {
		t.Fatal(err)
	}
	registration := registered.Data
	if registration.ID == "" || registration.Capability != "wiki" ||
		registration.ContractVersion != "scoped-paired-ab-v3" ||
		len(registration.ProtocolHash) != 64 || registration.Status != "active" {
		t.Fatalf("unexpected registration: %+v", registration)
	}
	// The snapshot must be canonical JSON with the pinned thresholds.
	if !strings.Contains(registration.ProtocolSnapshot, `"min_citation_precision_lift_pp":20`) {
		t.Fatalf("protocol snapshot is not canonical: %s", registration.ProtocolSnapshot)
	}

	// The list endpoint exposes the registration with filters.
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/evaluation-protocol-registrations?capability=wiki&status=active", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list status %d: %s", resp.StatusCode, body)
	}
	var list struct {
		Data struct {
			Items []map[string]interface{} `json:"items"`
			Total int64                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 1 || len(list.Data.Items) != 1 {
		t.Fatalf("unexpected list: %+v", list.Data)
	}

	// Revocation requires a reason and is one-way.
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/evaluation-protocol-registrations/"+registration.ID+"/revoke", token, []byte(`{}`))
	if resp.StatusCode != 400 {
		t.Fatalf("empty revoke reason status %d: %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/evaluation-protocol-registrations/"+registration.ID+"/revoke", token, []byte(`{"reason":"superseded by v4"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("revoke status %d: %s", resp.StatusCode, body)
	}
	var revoked struct {
		Data struct {
			Status       string `json:"status"`
			RevokeReason string `json:"revoke_reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &revoked); err != nil {
		t.Fatal(err)
	}
	if revoked.Data.Status != "revoked" || revoked.Data.RevokeReason != "superseded by v4" {
		t.Fatalf("unexpected revocation: %+v", revoked.Data)
	}
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/evaluation-protocol-registrations/"+registration.ID+"/revoke", token, []byte(`{"reason":"double revoke"}`))
	if resp.StatusCode != 409 {
		t.Fatalf("double revoke status %d: %s", resp.StatusCode, body)
	}

	// The filtered list now reflects the revoked status.
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/evaluation-protocol-registrations?status=active", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("active list status %d: %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 0 {
		t.Fatalf("active registrations must be empty after revocation: %+v", list.Data)
	}
}
