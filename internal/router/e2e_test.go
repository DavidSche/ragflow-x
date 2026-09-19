package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/handler"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/pkg/rsaseal"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/router"
	"github.com/ragflow-x/ragflow-x/internal/service"
	"gorm.io/gorm"
)

type testApp struct {
	db   *gorm.DB
	ts   *httptest.Server
	svc  *service.Service
	mock *ragflow.Mock
	rsa  *rsaseal.KeyPair
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Server: config.Server{Mode: "test"},
		App:    config.App{JWTSecret: "test-secret", JWTExpireHours: 24},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "test.db"),
		},
		RAGFlow: config.RAGFlow{Provider: "mock"},
	}

	gdb, err := db.Open(cfg.Database)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := repository.NewStore(gdb)
	mock := ragflow.NewMock()
	jm := jwt.NewManager(cfg.App.JWTSecret, cfg.App.JWTExpireHours)
	svc := service.New(store, mock, jm, "test-secret")
	svc.SetProviderURLPolicy(true)
	if err := svc.BootstrapAdmin(t.Context(), "admin", "admin123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// A2 async worker: start the in-process runner with aggressive intervals so
	// the jobs endpoint and enqueue/sync paths settle quickly in tests. It is
	// stopped in close() and also auto-stops when the test context is done.
	wcfg := service.DefaultWorkerConfig()
	wcfg.PollInterval = 20 * time.Millisecond
	wcfg.HeartbeatInterval = 10 * time.Millisecond
	wcfg.HeartbeatTimeout = time.Second
	wcfg.RequeueInterval = 50 * time.Millisecond
	wcfg.JobTimeout = 10 * time.Second
	wcfg.BaseBackoff = 10 * time.Millisecond
	wcfg.MaxBackoff = 100 * time.Millisecond
	svc.SetupWorker(wcfg).Start(t.Context())

	keyPair, err := rsaseal.Ensure(filepath.Join(t.TempDir(), "router-test-rsa.pem"))
	if err != nil {
		t.Fatalf("create test rsa key: %v", err)
	}
	h := handler.New(svc)
	h.SetSetupRSA(keyPair)
	engine := router.New(cfg, h, ratelimit.NewMemory())
	ts := httptest.NewServer(engine)

	return &testApp{db: gdb, ts: ts, svc: svc, mock: mock, rsa: keyPair}
}

// close releases the test server and the database file handle so that
// t.TempDir() cleanup can remove the underlying file. The async worker is
// stopped first so it never polls a closed database.
func (a *testApp) close() {
	if a.svc != nil && a.svc.Runner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.svc.Runner.Stop(ctx)
		cancel()
	}
	if a.ts != nil {
		a.ts.Close()
	}
	if raw, err := a.db.DB(); err == nil {
		_ = raw.Close()
	}
}

func (a *testApp) login(t *testing.T) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	resp, err := http.Post(a.ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login status: %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Token == "" {
		t.Fatal("empty token")
	}
	return out.Data.Token
}

func (a *testApp) doAuth(t *testing.T, method, path, token string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, a.ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func decodeBody(data []byte, out any) error {
	return json.Unmarshal(data, out)
}

func TestE2E_SmokeFlow(t *testing.T) {
	app := newTestApp(t)
	defer app.close()

	token := app.login(t)

	// 1. create a tenant
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/tenants", token, []byte(`{"name":"acme"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create tenant status %d: %s", resp.StatusCode, body)
	}

	// 2. list datasets (empty for the platform tenant)
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/datasets", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list datasets status %d", resp.StatusCode)
	}
	var listOut struct {
		Data []interface{} `json:"data"`
	}
	_ = json.Unmarshal(body, &listOut)
	if len(listOut.Data) != 0 {
		t.Fatalf("expected no datasets yet, got %d", len(listOut.Data))
	}

	// 3. create a dataset
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/datasets", token, []byte(`{"name":"kb-a"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create dataset status %d: %s", resp.StatusCode, body)
	}
	var ds struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ds); err != nil {
		t.Fatal(err)
	}
	if ds.Data.ID == "" {
		t.Fatal("empty dataset id")
	}

	// 4. upload a document via multipart
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "intro.txt")
	_, _ = fw.Write([]byte("RAGFlow-X smoke test document."))
	_ = w.Close()

	upReq, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/api/v1/datasets/"+ds.Data.ID+"/documents", &buf)
	upReq.Header.Set("Authorization", "Bearer "+token)
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		t.Fatal(err)
	}
	upResp.Body.Close()
	if upResp.StatusCode != 200 {
		t.Fatalf("upload document status %d", upResp.StatusCode)
	}

	// 5. list documents to learn the document id
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/datasets/"+ds.Data.ID+"/documents", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list documents status %d", resp.StatusCode)
	}
	var docs struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &docs); err != nil {
		t.Fatal(err)
	}
	if len(docs.Data) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs.Data))
	}

	// 6. parse the document
	parseBody, _ := json.Marshal(map[string]interface{}{"document_ids": []string{docs.Data[0].ID}})
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/datasets/"+ds.Data.ID+"/parse", token, parseBody)
	if resp.StatusCode != 200 {
		t.Fatalf("parse status %d: %s", resp.StatusCode, body)
	}

	// 7. list documents again and confirm the parsed status
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/datasets/"+ds.Data.ID+"/documents", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("final list status %d", resp.StatusCode)
	}
	var final struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &final)
	if len(final.Data) != 1 || final.Data[0].Status != "parsed" {
		t.Fatalf("unexpected final documents: %+v", final.Data)
	}
}

func TestE2E_ApprovalFeatureDisabled(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	resp, body := app.doAuth(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("me status %d: %s", resp.StatusCode, body)
	}
	var me struct {
		Data struct {
			ApprovalEnabled bool `json:"approval_enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatal(err)
	}
	if me.Data.ApprovalEnabled {
		t.Fatal("expected approval feature to be disabled")
	}

	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/approvals", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled approvals status %d: %s", resp.StatusCode, body)
	}
}

func TestE2E_RequiresAuth(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	resp, _ := app.doAuth(t, http.MethodGet, "/api/v1/datasets", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestE2E_M2ControlPlane(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	// roles are seeded
	resp, body := app.doAuth(t, http.MethodGet, "/api/v1/roles", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("roles status %d: %s", resp.StatusCode, body)
	}

	// create a tenant user
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/users", token, []byte(`{"username":"op1","password":"secret123","role":"operator"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create user status %d: %s", resp.StatusCode, body)
	}

	// create a model provider
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-providers", token, []byte(`{"provider_type":"openai","name":"llm","base_url":"https://x","api_key":"sk-test"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create provider status %d: %s", resp.StatusCode, body)
	}
	var prov struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &prov)

	// create a model route
	routeBody := fmt.Sprintf(`{"provider_id":%q,"model_alias":"gpt","target_model":"gpt-4o"}`, prov.Data.ID)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-routes", token, []byte(routeBody))
	if resp.StatusCode != 200 {
		t.Fatalf("create route status %d: %s", resp.StatusCode, body)
	}

	// create an API key
	_, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"gw"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create key status %d: %s", resp.StatusCode, body)
	}
	var key struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &key)
	if key.Data.Secret == "" {
		t.Fatal("expected a raw api key secret")
	}

	// dashboard returns counts
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/dashboard", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("dashboard status %d: %s", resp.StatusCode, body)
	}
	var dash struct {
		Data struct {
			Users int64 `json:"users"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &dash)
	if dash.Data.Users < 2 {
		t.Fatalf("expected at least 2 users, got %d", dash.Data.Users)
	}

	// gateway accepts a valid api key then returns not-configured, rejects missing key
	req, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt"}`)))
	req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
	gr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	gr.Body.Close()
	if gr.StatusCode != 502 {
		t.Fatalf("gateway with key expected 502, got %d", gr.StatusCode)
	}
	req2, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt"}`)))
	gr2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	gr2.Body.Close()
	if gr2.StatusCode != 401 {
		t.Fatalf("gateway without key expected 401, got %d", gr2.StatusCode)
	}
}

func TestE2E_GatewayForwardingAndUsage(t *testing.T) {
	var gotAuth string
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-1",
			"model":   "alias-model",
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "hi from provider"}}},
			"usage":   map[string]int64{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer providerSrv.Close()

	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	provBody := fmt.Sprintf(`{"provider_type":"openai","name":"local","base_url":%q,"api_key":"sk-proxy","model_name":"m","register":false}`, providerSrv.URL)
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/model-providers", token, []byte(provBody))
	if resp.StatusCode != 200 {
		t.Fatalf("create provider: %d %s", resp.StatusCode, body)
	}
	var prov struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &prov)
	routeBody := fmt.Sprintf(`{"provider_id":%q,"model_alias":"alias-model","target_model":"m"}`, prov.Data.ID)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-routes", token, []byte(routeBody))
	if resp.StatusCode != 200 {
		t.Fatalf("create route: %d %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"gw"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create key: %d %s", resp.StatusCode, body)
	}
	var key struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &key)

	req, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"alias-model","messages":[{"role":"user","content":"x"}]}`)))
	req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
	gr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(gr.Body)
	gr.Body.Close()
	if gr.StatusCode != 200 || !strings.Contains(buf.String(), "hi from provider") {
		t.Fatalf("gateway response: status=%d body=%s", gr.StatusCode, buf.String())
	}
	if !strings.Contains(gotAuth, "sk-proxy") {
		t.Fatalf("provider auth header not propagated: %q", gotAuth)
	}

	_, body = app.doAuth(t, http.MethodGet, "/api/v1/usage", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("usage: %d", resp.StatusCode)
	}
	var usage struct {
		Data []struct {
			TokensIn int64 `json:"tokens_in"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &usage)
	if len(usage.Data) == 0 || usage.Data[0].TokensIn != 10 {
		t.Fatalf("expected 10 prompt tokens metered, got %+v", usage.Data)
	}

	// The gateway must also persist the per-request cost metric detail.
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/usage/details", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("usage details: %d %s", resp.StatusCode, body)
	}
	var det struct {
		Data struct {
			Items []struct {
				Model    string `json:"model"`
				TokensIn int64  `json:"tokens_in"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &det); err != nil {
		t.Fatalf("usage details decode: %v (%s)", err, body)
	}
	if len(det.Data.Items) == 0 || det.Data.Items[0].TokensIn != 10 || det.Data.Items[0].Model != "alias-model" {
		t.Fatalf("expected cost detail row, got %+v", det.Data.Items)
	}
}

func TestE2E_GatewaySearchAppAndAgentScopes(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	_, body := app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"chat-only","scopes":{"routes":["chat"]}}`))
	if body == nil {
		t.Fatal("empty key response")
	}
	var key struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatal(err)
	}
	if key.Data.Secret == "" {
		t.Fatal("expected raw api key")
	}

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"search app", "/v1/search-apps/resource/completions", `{"question":"what is rag"}`},
		{"agent", "/v1/agents/resource/completions", `{"messages":[{"role":"user","content":"what is rag"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, app.ts.URL+tc.path, bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403, body=%s", resp.StatusCode, data)
			}
		})
	}
}

func TestE2E_GatewayQuotaHeadersOnAllPaths(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := t.Context()
	token := app.login(t)

	dataset, err := app.svc.CreateDataset(ctx, model.PlatformTenantID, "quota-search-kb")
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	searchApp, err := app.svc.CreateSearchApp(ctx, model.PlatformTenantID, "quota-search",
		&ragflow.SearchConfig{KbIDs: []string{dataset.RAGFlowDatasetID}})
	if err != nil {
		t.Fatalf("create search app: %v", err)
	}
	agent, err := app.svc.CreateAgent(ctx, model.PlatformTenantID, "quota-agent",
		map[string]interface{}{"x": 1}, false, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	var admin model.User
	if err := app.db.Where("username = ?", "admin").First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := app.svc.SetSearchAppOwner(ctx, searchApp, admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.SetAgentOwner(ctx, agent, admin.ID); err != nil {
		t.Fatal(err)
	}

	_, body := app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"gateway-quota","request_quota":2,"scopes":{"routes":["search-app","agent"]}}`))
	var key struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		path      string
		payload   string
		contains  string
		remaining string
	}{
		{"search app", "/v1/search-apps/" + searchApp.ID + "/completions", `{"question":"quota search"}`, "mock answer", "1"},
		{"agent", "/v1/agents/" + agent.ID + "/completions", `{"messages":[{"role":"user","content":"quota agent"}]}`, "mock agent response", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, app.ts.URL+tc.path, bytes.NewReader([]byte(tc.payload)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), tc.contains) {
				t.Fatalf("status=%d body=%s", resp.StatusCode, data)
			}
			if got := resp.Header.Get("X-Quota-Requests-Remaining"); got != tc.remaining {
				t.Fatalf("X-Quota-Requests-Remaining = %q, want %q", got, tc.remaining)
			}
			if resp.Header.Get("X-Quota-Remaining") != "" {
				t.Fatal("request-only key must not advertise the token header")
			}
		})
	}

	req, err := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/agents/"+agent.ID+"/completions",
		bytes.NewReader([]byte(`{"messages":[{"role":"user","content":"exhaust quota"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("quota exhaustion: status=%d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Quota-Requests-Remaining"); got != "0" {
		t.Fatalf("X-Quota-Requests-Remaining = %q, want 0", got)
	}
}

func TestE2E_SystemCapabilities(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	resp, body := app.doAuth(t, http.MethodGet, "/api/v1/system/capabilities", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list capabilities: %d %s", resp.StatusCode, body)
	}
	var list struct {
		Data struct {
			UpgradeDecision string `json:"upgrade_decision"`
			Items           []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data.Items) != 10 {
		t.Fatalf("declared capabilities = %d, want 10", len(list.Data.Items))
	}
	if list.Data.UpgradeDecision != "UNKNOWN_RUNTIME" {
		t.Fatalf("initial upgrade gate = %s, want UNKNOWN_RUNTIME", list.Data.UpgradeDecision)
	}

	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/system/capabilities/verify", token, []byte(`{}`))
	if resp.StatusCode != 200 {
		t.Fatalf("verify capabilities: %d %s", resp.StatusCode, body)
	}
	var verify struct {
		Data struct {
			DetectedVersion string `json:"detected_version"`
			RuntimeHealth   string `json:"runtime_health"`
			UpgradeDecision string `json:"upgrade_decision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &verify); err != nil {
		t.Fatal(err)
	}
	if verify.Data.DetectedVersion != "0.27.1" || verify.Data.RuntimeHealth != "HEALTHY" {
		t.Fatalf("verification: %+v", verify.Data)
	}
	if verify.Data.UpgradeDecision != "READY" {
		t.Fatalf("0.27 upgrade gate = %s, want READY", verify.Data.UpgradeDecision)
	}
}

func TestE2E_TaskQueueAndBatch(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	// create dataset + upload + parse
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/datasets", token, []byte(`{"name":"kb"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("dataset: %d", resp.StatusCode)
	}
	var ds struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &ds)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "a.txt")
	_, _ = fw.Write([]byte("hello"))
	_ = w.Close()
	upReq, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/api/v1/datasets/"+ds.Data.ID+"/documents", &buf)
	upReq.Header.Set("Authorization", "Bearer "+token)
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upr, _ := http.DefaultClient.Do(upReq)
	upr.Body.Close()
	if upr.StatusCode != 200 {
		t.Fatalf("upload: %d", upr.StatusCode)
	}

	// list tasks: should contain upload type
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/tasks", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("tasks: %d", resp.StatusCode)
	}
	var tasks struct {
		Data struct {
			Items []struct {
				TaskType string `json:"task_type"`
			} `json:"items"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &tasks)
	uploadSeen := false
	for _, item := range tasks.Data.Items {
		if item.TaskType == "upload" {
			uploadSeen = true
			break
		}
	}
	if !uploadSeen {
		t.Fatalf("expected an upload task, got %+v", tasks.Data.Items)
	}
}

func TestE2E_StreamingMetering(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer providerSrv.Close()

	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	provBody := fmt.Sprintf(`{"provider_type":"openai","name":"stream","base_url":%q,"api_key":"sk-s","model_name":"m","register":false}`, providerSrv.URL)
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/model-providers", token, []byte(provBody))
	if resp.StatusCode != 200 {
		t.Fatalf("provider: %d %s", resp.StatusCode, body)
	}
	var prov struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &prov)
	routeBody := fmt.Sprintf(`{"provider_id":%q,"model_alias":"alias","target_model":"m"}`, prov.Data.ID)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-routes", token, []byte(routeBody))
	if resp.StatusCode != 200 {
		t.Fatalf("route: %d %s", resp.StatusCode, body)
	}
	_, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"gw"}`))
	var key struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &key)

	req, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"alias","stream":true,"messages":[{"role":"user","content":"x"}]}`)))
	req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
	gr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(gr.Body)
	gr.Body.Close()
	if gr.StatusCode != 200 || !strings.Contains(buf.String(), "hi") || !strings.Contains(buf.String(), "[DONE]") {
		t.Fatalf("stream response: status=%d body=%s", gr.StatusCode, buf.String())
	}

	_, body = app.doAuth(t, http.MethodGet, "/api/v1/usage", token, nil)
	var usage struct {
		Data []struct {
			TokensIn int64 `json:"tokens_in"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &usage)
	if len(usage.Data) == 0 || usage.Data[0].TokensIn != 20 {
		t.Fatalf("expected 20 streamed prompt tokens, got %+v", usage.Data)
	}
}

// ScenarioID: SC-IDEM-001
func TestP0_IDEM_001_GatewayReplayCallsProviderOnce(t *testing.T) {
	var providerCalls int
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-idem", "model": "alias",
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "same"}}},
			"usage":   map[string]int64{"prompt_tokens": 4, "completion_tokens": 2},
		})
	}))
	defer providerSrv.Close()

	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	provBody := fmt.Sprintf(`{"provider_type":"openai","name":"idem","base_url":%q,"api_key":"sk-idem","model_name":"m","register":false}`, providerSrv.URL)
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/model-providers", token, []byte(provBody))
	if resp.StatusCode != 200 {
		t.Fatalf("provider: %d %s", resp.StatusCode, body)
	}
	var prov struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &prov)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-routes", token, []byte(fmt.Sprintf(`{"provider_id":%q,"model_alias":"alias","target_model":"m"}`, prov.Data.ID)))
	if resp.StatusCode != 200 {
		t.Fatalf("route: %d %s", resp.StatusCode, body)
	}
	_, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"idem"}`))
	var key struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &key)

	doGateway := func(payload, idempotencyKey string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions", bytes.NewReader([]byte(payload)))
		req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		response, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, response
	}

	payload := `{"model":"alias","messages":[{"role":"user","content":"same"}]}`
	first, firstBody := doGateway(payload, "same-key")
	second, secondBody := doGateway(payload, "same-key")
	if first.StatusCode != 200 || second.StatusCode != 200 {
		t.Fatalf("gateway status: first=%d second=%d", first.StatusCode, second.StatusCode)
	}
	if string(firstBody) != string(secondBody) {
		t.Fatalf("idempotent replay changed body: %s vs %s", firstBody, secondBody)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}

	conflicted, conflictBody := doGateway(`{"model":"alias","messages":[{"role":"user","content":"different"}]}`, "same-key")
	if conflicted.StatusCode != 409 {
		t.Fatalf("expected fingerprint conflict 409, got %d %s", conflicted.StatusCode, conflictBody)
	}
}

func TestE2E_TeamsAndCrossTenantDataset(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	// teams CRUD
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/teams", token, []byte(`{"name":"core"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create team: %d %s", resp.StatusCode, body)
	}
	var team struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &team)
	if team.Data.ID == "" {
		t.Fatal("empty team id")
	}
	resp, _ = app.doAuth(t, http.MethodPut, "/api/v1/teams/"+team.Data.ID, token, []byte(`{"name":"core-v2"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("update team: %d", resp.StatusCode)
	}
	resp, _ = app.doAuth(t, http.MethodDelete, "/api/v1/teams/"+team.Data.ID, token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete team: %d", resp.StatusCode)
	}

	// dataset create + cross-tenant list + delete
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/datasets", token, []byte(`{"name":"kb-x"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create dataset: %d %s", resp.StatusCode, body)
	}
	var ds struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &ds)
	_, body = app.doAuth(t, http.MethodGet, "/api/v1/datasets?scope=all", token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list all: %d", resp.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &list)
	found := false
	for _, d := range list.Data {
		if d.ID == ds.Data.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cross-tenant list missing dataset %s", ds.Data.ID)
	}
	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/datasets/"+ds.Data.ID, token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete dataset: %d %s", resp.StatusCode, body)
	}
	_, body = app.doAuth(t, http.MethodGet, "/api/v1/datasets?scope=all", token, nil)
	_ = json.Unmarshal(body, &list)
	for _, d := range list.Data {
		if d.ID == ds.Data.ID {
			t.Fatalf("dataset %s still present after delete", ds.Data.ID)
		}
	}
}

func TestE2E_UserTeamsAndDatasetOps(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/users", token, []byte(`{"username":"u1","password":"secret123","role":"operator"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create user: %d %s", resp.StatusCode, body)
	}
	var user struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &user)
	_, body = app.doAuth(t, http.MethodPost, "/api/v1/teams", token, []byte(`{"name":"dev"}`))
	var team struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &team)
	addBody := fmt.Sprintf(`{"user_id":%q}`, user.Data.ID)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/teams/"+team.Data.ID+"/users", token, []byte(addBody))
	if resp.StatusCode != 200 {
		t.Fatalf("add member: %d %s", resp.StatusCode, body)
	}
	_, body = app.doAuth(t, http.MethodGet, "/api/v1/teams/"+team.Data.ID+"/users", token, nil)
	var members struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &members)
	if len(members.Data) != 1 || members.Data[0].ID != user.Data.ID {
		t.Fatalf("expected 1 member, got %+v", members.Data)
	}
	resp, _ = app.doAuth(t, http.MethodDelete, "/api/v1/teams/"+team.Data.ID+"/users/"+user.Data.ID, token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("remove member: %d", resp.StatusCode)
	}

	_, body = app.doAuth(t, http.MethodGet, "/api/v1/users?scope=all", token, nil)
	var all struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &all)
	found := false
	for _, u := range all.Data.Items {
		if u.ID == user.Data.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("user u1 not in scope=all list")
	}
	resp, body = app.doAuth(t, http.MethodPut, "/api/v1/users/"+user.Data.ID+"/status", token, []byte(`{"status":"disabled"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("disable: %d %s", resp.StatusCode, body)
	}
	resp, _ = app.doAuth(t, http.MethodPut, "/api/v1/users/"+user.Data.ID+"/role", token, []byte(`{"role":"viewer"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("change role: %d", resp.StatusCode)
	}

	_, body = app.doAuth(t, http.MethodPost, "/api/v1/datasets", token, []byte(`{"name":"b1"}`))
	var ds struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &ds)
	expReq, _ := http.NewRequest(http.MethodGet, app.ts.URL+"/api/v1/datasets/export", nil)
	expReq.Header.Set("Authorization", "Bearer "+token)
	expResp, err := http.DefaultClient.Do(expReq)
	if err != nil {
		t.Fatal(err)
	}
	expBody := new(bytes.Buffer)
	_, _ = expBody.ReadFrom(expResp.Body)
	expResp.Body.Close()
	if expResp.StatusCode != 200 || !strings.Contains(expBody.String(), "ragflow_dataset_id") {
		t.Fatalf("export: %d", expResp.StatusCode)
	}
	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/datasets", token, []byte(`{"ids":["`+ds.Data.ID+`"]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("batch delete: %d %s", resp.StatusCode, body)
	}
}

func TestE2E_RBACEnforcement(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	adminToken := app.login(t)
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/users", adminToken, []byte(`{"username":"vw","password":"secret123","role":"viewer"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create viewer: %d %s", resp.StatusCode, body)
	}

	vwResp, err := http.Post(app.ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader([]byte(`{"username":"vw","password":"secret123"}`)))
	if err != nil {
		t.Fatal(err)
	}
	var vwOut struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.NewDecoder(vwResp.Body).Decode(&vwOut)
	vwResp.Body.Close()
	if vwOut.Data.Token == "" {
		t.Fatal("viewer login failed")
	}

	resp, _ = app.doAuth(t, http.MethodGet, "/api/v1/datasets", vwOut.Data.Token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("viewer read should pass, got %d", resp.StatusCode)
	}
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/datasets", vwOut.Data.Token, []byte(`{"name":"forbidden"}`))
	if resp.StatusCode != 403 {
		t.Fatalf("viewer manage should be 403, got %d %s", resp.StatusCode, body)
	}
}

func TestE2E_ChatManagement(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/chats", token, []byte(`{"name":"kb-assistant","dataset_ids":["d1"]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create chat: %d %s", resp.StatusCode, body)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("decode create chat: %v (%s)", err, body)
	}
	chatID := created.Data.ID

	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/chats", token, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), chatID) {
		t.Fatalf("list chats: %d %s", resp.StatusCode, body)
	}

	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/chats/"+chatID, token, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "kb-assistant") {
		t.Fatalf("get chat: %d %s", resp.StatusCode, body)
	}

	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/chats/"+chatID+"/sessions", token, []byte(`{"name":"Session 1"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create session: %d %s", resp.StatusCode, body)
	}
	var sess struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &sess); err != nil || sess.Data.ID == "" {
		t.Fatalf("decode session: %v (%s)", err, body)
	}

	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/chats/"+chatID+"/sessions", token, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), sess.Data.ID) {
		t.Fatalf("list sessions: %d %s", resp.StatusCode, body)
	}

	resp, body = app.doAuth(t, http.MethodPut, "/api/v1/chats/"+chatID, token, []byte(`{"name":"kb-assistant-v2"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("update chat: %d %s", resp.StatusCode, body)
	}

	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/chats/"+chatID+"/sessions", token, []byte(`{"ids":["`+sess.Data.ID+`"]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("delete sessions: %d %s", resp.StatusCode, body)
	}

	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/chats/"+chatID, token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete chat: %d %s", resp.StatusCode, body)
	}
}

func TestE2E_ChatFeedback(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/chats", token, []byte(`{"name":"fb-chat","dataset_ids":["d1"]}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create chat: %d %s", resp.StatusCode, body)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("decode chat: %v (%s)", err, body)
	}

	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/chats/"+created.Data.ID+"/sessions", token, []byte(`{"name":"s"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create session: %d %s", resp.StatusCode, body)
	}
	var sess struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &sess); err != nil || sess.Data.ID == "" {
		t.Fatalf("decode session: %v (%s)", err, body)
	}

	fb, _ := json.Marshal(map[string]string{"chat_id": created.Data.ID, "session_id": sess.Data.ID, "message_id": "m1", "rating": "positive"})
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/chat/feedback", token, fb)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "positive") {
		t.Fatalf("feedback: %d %s", resp.StatusCode, body)
	}
}

func TestE2E_ChatSessionOps(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	createBody, _ := json.Marshal(map[string]interface{}{"name": "sess-ops", "dataset_ids": []string{"d1"}})
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/chats", token, createBody)
	if resp.StatusCode != 200 {
		t.Fatalf("create chat: %d %s", resp.StatusCode, body)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("decode chat: %v (%s)", err, body)
	}

	sessBody, _ := json.Marshal(map[string]string{"name": "s1"})
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/chats/"+created.Data.ID+"/sessions", token, sessBody)
	if resp.StatusCode != 200 {
		t.Fatalf("create session: %d %s", resp.StatusCode, body)
	}
	var sess struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &sess); err != nil || sess.Data.ID == "" {
		t.Fatalf("decode session: %v (%s)", err, body)
	}

	renameBody, _ := json.Marshal(map[string]string{"name": "renamed"})
	resp, body = app.doAuth(t, http.MethodPatch, "/api/v1/chats/"+created.Data.ID+"/sessions/"+sess.Data.ID, token, renameBody)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "renamed") {
		t.Fatalf("rename: %d %s", resp.StatusCode, body)
	}

	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/chats/"+created.Data.ID+"/sessions/"+sess.Data.ID, token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete session: %d %s", resp.StatusCode, body)
	}
}

func TestE2E_GatewayQuotaPreauthorization(t *testing.T) {
	providerHits := 0
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerHits++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-q",
			"model":   "alias-model",
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "quota ok"}}},
			"usage":   map[string]int64{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer providerSrv.Close()

	app := newTestApp(t)
	defer app.close()
	token := app.login(t)

	// Base provider + route so a quota-free key has a live forwarding path.
	provBody := fmt.Sprintf(`{"provider_type":"openai","name":"local","base_url":%q,"api_key":"sk-proxy","model_name":"m","register":false}`, providerSrv.URL)
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/model-providers", token, []byte(provBody))
	if resp.StatusCode != 200 {
		t.Fatalf("create provider: %d %s", resp.StatusCode, body)
	}
	var prov struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &prov)
	routeBody := fmt.Sprintf(`{"provider_id":%q,"model_alias":"alias-model","target_model":"m"}`, prov.Data.ID)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/model-routes", token, []byte(routeBody))
	if resp.StatusCode != 200 {
		t.Fatalf("create route: %d %s", resp.StatusCode, body)
	}

	// A quota-limited key: monthly budget is tiny (50 tokens).
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"quotakey","token_quota":50}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create quota key: %d %s", resp.StatusCode, body)
	}
	var key struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &key)
	if key.Data.Secret == "" {
		t.Fatal("empty key secret")
	}

	// The over-budget request must be rejected with 429 before any provider
	// call: the estimate (len/2 + 100000) dwarfs the 50-token budget.
	req, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"alias-model","messages":[{"role":"user","content":"x"}],"max_tokens":100000}`)))
	req.Header.Set("Authorization", "Bearer "+key.Data.Secret)
	gr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(gr.Body)
	gr.Body.Close()
	if gr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for over-budget request, got %d: %s", gr.StatusCode, buf.String())
	}
	if gr.Header.Get("X-Quota-Remaining") == "" {
		t.Fatal("missing X-Quota-Remaining header on the 429 response")
	}
	if providerHits != 0 {
		t.Fatalf("over-budget request must never reach the provider, hits=%d", providerHits)
	}

	// A within-budget request passes through and advertises a reduced remaining.
	req2, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"alias-model","messages":[{"role":"user","content":"x"}],"max_tokens":1}`)))
	req2.Header.Set("Authorization", "Bearer "+key.Data.Secret)
	gr2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	buf2 := new(bytes.Buffer)
	_, _ = buf2.ReadFrom(gr2.Body)
	gr2.Body.Close()
	if gr2.StatusCode != 200 || !strings.Contains(buf2.String(), "quota ok") {
		t.Fatalf("within-budget request: status=%d body=%s", gr2.StatusCode, buf2.String())
	}
	remaining, rerr := strconv.Atoi(gr2.Header.Get("X-Quota-Remaining"))
	if rerr != nil || remaining < 0 || remaining >= 50 {
		t.Fatalf("X-Quota-Remaining should be in [0,50) after a metered call, got %q (%v)", gr2.Header.Get("X-Quota-Remaining"), rerr)
	}
	if providerHits != 1 {
		t.Fatalf("expected exactly one provider hit, got %d", providerHits)
	}

	// A key without a quota (nil token_quota -> unlimited) must bypass the
	// quota path entirely and carry no remaining header.
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"freekey"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create free key: %d %s", resp.StatusCode, body)
	}
	var freeKey struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &freeKey)
	req3, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"alias-model","messages":[{"role":"user","content":"x"}]}`)))
	req3.Header.Set("Authorization", "Bearer "+freeKey.Data.Secret)
	gr3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	buf3 := new(bytes.Buffer)
	_, _ = buf3.ReadFrom(gr3.Body)
	gr3.Body.Close()
	if gr3.StatusCode != 200 {
		t.Fatalf("quota-free request: status=%d body=%s", gr3.StatusCode, buf3.String())
	}
	if got := gr3.Header.Get("X-Quota-Remaining"); got != "" {
		t.Fatalf("quota-free key must not advertise a remaining header, got %q", got)
	}

	// A request-count-only key must advertise the logical request budget,
	// consume it on success, and reject the next logical request.
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/keys", token, []byte(`{"name":"request-quota-key","request_quota":1}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create request quota key: %d %s", resp.StatusCode, body)
	}
	var requestKey struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &requestKey)
	req4, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"alias-model","messages":[{"role":"user","content":"x"}]}`)))
	req4.Header.Set("Authorization", "Bearer "+requestKey.Data.Secret)
	gr4, err := http.DefaultClient.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	buf4 := new(bytes.Buffer)
	_, _ = buf4.ReadFrom(gr4.Body)
	gr4.Body.Close()
	if gr4.StatusCode != 200 || !strings.Contains(buf4.String(), "quota ok") {
		t.Fatalf("request-quota request: status=%d body=%s", gr4.StatusCode, buf4.String())
	}
	if remaining, err := strconv.Atoi(gr4.Header.Get("X-Quota-Requests-Remaining")); err != nil || remaining != 0 {
		t.Fatalf("X-Quota-Requests-Remaining should be 0 after success, got %q (%v)", gr4.Header.Get("X-Quota-Requests-Remaining"), err)
	}
	if gr4.Header.Get("X-Quota-Remaining") != "" {
		t.Fatal("request-only key must not advertise the token header")
	}

	req5, _ := http.NewRequest(http.MethodPost, app.ts.URL+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"alias-model","messages":[{"role":"user","content":"x"}]}`)))
	req5.Header.Set("Authorization", "Bearer "+requestKey.Data.Secret)
	gr5, err := http.DefaultClient.Do(req5)
	if err != nil {
		t.Fatal(err)
	}
	buf5 := new(bytes.Buffer)
	_, _ = buf5.ReadFrom(gr5.Body)
	gr5.Body.Close()
	if gr5.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected request quota 429, got %d: %s", gr5.StatusCode, buf5.String())
	}
	if gr5.Header.Get("X-Quota-Requests-Remaining") != "0" {
		t.Fatalf("missing exhausted request quota header, got %q", gr5.Header.Get("X-Quota-Requests-Remaining"))
	}
	var used int64
	if err := app.db.Raw("SELECT requests_used FROM rgx_quota WHERE key_id IN (SELECT id FROM rgx_api_key WHERE name = 'request-quota-key')").Scan(&used).Error; err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatalf("successful request must increment requests_used once, got %d", used)
	}
}

// loginAs authenticates an arbitrary user and returns its bearer token.
func loginAs(t *testing.T, app *testApp, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(app.ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Token == "" {
		t.Fatalf("login %s failed (status %d)", username, resp.StatusCode)
	}
	return out.Data.Token
}

// waitE2EJob polls GET /tasks/jobs until a job of kind reaches want status and
// returns its id, failing the test on timeout.
func waitE2EJob(t *testing.T, app *testApp, kind, want string) string {
	t.Helper()
	adminToken := app.login(t)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, body := app.doAuth(t, http.MethodGet, "/api/v1/tasks/jobs?kind="+kind, adminToken, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("jobs poll: %d %s", resp.StatusCode, body)
		}
		var out struct {
			Data struct {
				Items []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		for _, it := range out.Data.Items {
			if it.Status == want {
				return it.ID
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job kind %s never reached status %s within 5s", kind, want)
	return ""
}

func TestE2E_AsyncJobsObservationAndRBAC(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	adminToken := app.login(t)

	// Manual sync schedules a document-sync job on the async worker instead of
	// blocking on the request path.
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/tasks/sync", adminToken, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("sync: %d %s", resp.StatusCode, body)
	}
	var syncOut struct {
		Data struct {
			Enqueued bool `json:"enqueued"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &syncOut); err != nil {
		t.Fatal(err)
	}
	if !syncOut.Data.Enqueued {
		t.Fatalf("expected sync to enqueue a job: %s", body)
	}

	// The worker must execute the job through queued -> running -> succeeded.
	jobID := waitE2EJob(t, app, "document_sync", "succeeded")
	if jobID == "" {
		t.Fatal("empty job id")
	}

	// Jobs endpoint is tenant-scoped and observable with status filter.
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/tasks/jobs?status=succeeded", adminToken, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("jobs: %d %s", resp.StatusCode, body)
	}
	var jobsOut struct {
		Data struct {
			Items []map[string]interface{} `json:"items"`
			Total int64                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &jobsOut); err != nil {
		t.Fatal(err)
	}
	if jobsOut.Data.Total < 1 {
		t.Fatalf("expected at least one succeeded job, total=%d", jobsOut.Data.Total)
	}

	// 越权 case 1: team_admin has no "read task" grant -> 403.
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/users", adminToken, []byte(`{"username":"ta","password":"secret123","role":"team_admin"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create team_admin: %d %s", resp.StatusCode, body)
	}
	taToken := loginAs(t, app, "ta", "secret123")
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/tasks/jobs", taToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("team_admin reading jobs should be 403, got %d %s", resp.StatusCode, body)
	}

	// 越权 case 2: viewer has "read task" -> 200 (allowed).
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/users", adminToken, []byte(`{"username":"vw2","password":"secret123","role":"viewer"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("create viewer: %d %s", resp.StatusCode, body)
	}
	vwToken := loginAs(t, app, "vw2", "secret123")
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/tasks/jobs", vwToken, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("viewer reading jobs should be 200, got %d %s", resp.StatusCode, body)
	}
}
