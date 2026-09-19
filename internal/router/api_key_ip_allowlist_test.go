package router_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestE2E_APIKeyIPAllowlist(t *testing.T) {
	providerHits := 0
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerHits++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-ip", "model": "alias-model",
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "ip ok"}}},
			"usage":   map[string]int64{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer providerSrv.Close()

	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	provBody := fmt.Sprintf(`{"provider_type":"openai","name":"local","base_url":%q,"api_key":"sk-proxy","model_name":"m","register":false}`, providerSrv.URL)
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/model-providers", token, []byte(provBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create provider: %d %s", resp.StatusCode, body)
	}
	var provider struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &provider); err != nil {
		t.Fatal(err)
	}
	routeBody := fmt.Sprintf(`{"provider_id":%q,"model_alias":"alias-model","target_model":"m"}`, provider.Data.ID)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-routes", token, []byte(routeBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create route: %d %s", resp.StatusCode, body)
	}

	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{
		"name":"ip-allowlist",
		"allowed_ips":["127.0.0.1","192.168.10.0/24"],
		"request_quota":10
	}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create allowlisted key: %d %s", resp.StatusCode, body)
	}
	var created struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	gatewayCall := func(secret, forwardedFor, idempotencyKey string) (*http.Response, []byte) {
		req, err := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions",
			bytes.NewReader([]byte(`{"model":"alias-model","messages":[{"role":"user","content":"ip"}]}`)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Idempotency-Key", idempotencyKey)
		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		resp.Body.Close()
		return resp, buf.Bytes()
	}

	resp, body = gatewayCall(created.Data.Secret, "", "ip-allowed")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("ip ok")) {
		t.Fatalf("allowed call: status=%d body=%s", resp.StatusCode, body)
	}
	if providerHits != 1 {
		t.Fatalf("allowed provider hits = %d, want 1", providerHits)
	}

	resp, body = gatewayCall(created.Data.Secret, "203.0.113.10", "ip-denied")
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("not authorized from this ip")) {
		t.Fatalf("denied call: status=%d body=%s", resp.StatusCode, body)
	}
	if providerHits != 1 {
		t.Fatalf("denied request reached provider, hits=%d", providerHits)
	}
	var idempotencyCount int64
	if err := app.db.Raw(`SELECT COUNT(*) FROM rgx_gateway_idempotency WHERE idempotency_key = ?`, "ip-denied").Scan(&idempotencyCount).Error; err != nil {
		t.Fatal(err)
	}
	if idempotencyCount != 0 {
		t.Fatalf("IP-denied request created idempotency rows = %d, want 0", idempotencyCount)
	}
	var auditCount int64
	if err := app.db.Raw(`SELECT COUNT(*) FROM rgx_audit_log WHERE action = ?`, "gateway.ip_denied").Scan(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("gateway.ip_denied audit rows = %d, want 1", auditCount)
	}

	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{
		"name":"ip-deny-all","allowed_ips":[]
	}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create deny-all key: %d %s", resp.StatusCode, body)
	}
	var denyAll struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &denyAll); err != nil {
		t.Fatal(err)
	}
	resp, body = gatewayCall(denyAll.Data.Secret, "", "ip-deny-all")
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("not authorized from this ip")) {
		t.Fatalf("deny-all call: status=%d body=%s", resp.StatusCode, body)
	}
	if providerHits != 1 {
		t.Fatalf("deny-all request reached provider, hits=%d", providerHits)
	}
}
