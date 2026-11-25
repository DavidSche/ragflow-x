package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// doc/304 §4 E91: the route decision belongs to the gateway configuration, not
// to the caller's request body. resolveRoute matches the client's `model`
// against ModelAlias, and the body that leaves the gateway must spell the
// route's TargetModel; everything else is carried through untouched.
//
// These tests read the body the fake upstream actually received (not a log
// line), because "we rewrote it" and "we sent the rewrite" are different
// claims and only the second one is worth anything.

type capturedUpstream struct {
	mu     sync.Mutex
	bodies []string
}

func (c *capturedUpstream) record(body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies = append(c.bodies, string(body))
}

func (c *capturedUpstream) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.bodies))
	copy(out, c.bodies)
	return out
}

// newUpstreamModelSvc builds a service with one enabled provider pointing at a
// fake upstream, plus the chat routes the test asks for. The upstream answers
// 200 (JSON or SSE depending on the request's `stream` flag) and records every
// request body it receives.
func newUpstreamModelSvc(t *testing.T, routes ...CreateModelRouteRequest) (*Service, *model.APIKey, *capturedUpstream, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	svc := newAuthzSvc(t)
	// The fake upstream listens on loopback, which the SSRF guard rejects by
	// default (internal/service/provider_url.go). Production default stays
	// false; security_fix_test.go pins that. This is the same switch the
	// performance runner flips for its in-process provider.
	svc.SetProviderURLPolicy(true)
	tenant, err := svc.CreateTenant(ctx, "Upstream Model Tenant")
	if err != nil {
		t.Fatal(err)
	}
	captured := &capturedUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.record(body)
		var probe struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	t.Cleanup(server.Close)

	sealed, err := crypto.Encrypt(svc.EncryptKey, "sk-upstream")
	if err != nil {
		t.Fatal(err)
	}
	provider := &model.ModelProvider{
		ID: "upstream-provider", TenantID: tenant.ID, ProviderType: "openai-api-compatible",
		Name: "Upstream", BaseURL: server.URL + "/v1", APIKeyEnc: sealed,
		Enabled: true, Status: model.ProviderStatusActive,
	}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	for _, req := range routes {
		req.ProviderID = provider.ID
		if _, err := svc.CreateModelRoute(ctx, tenant.ID, req); err != nil {
			t.Fatal(err)
		}
	}
	key := &model.APIKey{ID: "key-upstream-model", TenantID: tenant.ID, UserID: "user-1", Enabled: true}
	return svc, key, captured, server
}

func modelOf(t *testing.T, body string) string {
	t.Helper()
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatalf("upstream body is not JSON: %v (%q)", err, body)
	}
	return probe.Model
}

// 1. Alias translation, non-streaming: the client asks for the logical name,
// the upstream receives the route's target model.
func TestGatewayChatSendsTargetModelNotClientAlias(t *testing.T) {
	svc, key, captured, _ := newUpstreamModelSvc(t, CreateModelRouteRequest{
		Scenario: "chat", ModelAlias: "qwen3.8-27b", TargetModel: "Qwen3.8-27B",
	})
	raw := []byte(`{"model":"qwen3.8-27b","messages":[{"role":"user","content":"ping"}]}`)
	res, err := svc.ChatCompletion(context.Background(), key, raw, "req-alias-nonstream")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 {
		t.Fatalf("status = %d, want 200 (%s)", res.Status, res.Body)
	}
	bodies := captured.all()
	if len(bodies) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(bodies))
	}
	if got := modelOf(t, bodies[0]); got != "Qwen3.8-27B" {
		t.Fatalf("upstream model = %q, want the route target %q (the alias must not leak)", got, "Qwen3.8-27B")
	}
	if strings.Contains(bodies[0], "qwen3.8-27b") {
		t.Fatalf("the client alias survived into the upstream body: %s", bodies[0])
	}
}

// 2. Field preservation: every other member keeps its exact spelling,
// including numbers, nested objects and nulls.
func TestGatewayChatPreservesEveryOtherRequestField(t *testing.T) {
	svc, key, captured, _ := newUpstreamModelSvc(t, CreateModelRouteRequest{
		Scenario: "chat", ModelAlias: "alias-1", TargetModel: "target-1",
	})
	raw := []byte(`{"model":"alias-1","temperature":0.35,"top_p":1,"max_tokens":512,` +
		`"stop":null,"seed":1234567890123456789,` +
		`"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi"}],` +
		`"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}],` +
		`"metadata":{"trace":{"id":"abc","tags":["a","b"]}}}`)
	if _, err := svc.ChatCompletion(context.Background(), key, raw, "req-fields"); err != nil {
		t.Fatal(err)
	}
	body := captured.all()[0]

	var sent, want map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("upstream body is not JSON: %v", err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(sent) != len(want) {
		t.Fatalf("field count changed: upstream has %d fields, client sent %d (%s)", len(sent), len(want), body)
	}
	for field, wantRaw := range want {
		gotRaw, ok := sent[field]
		if !ok {
			t.Fatalf("field %q was dropped on the way to the upstream (%s)", field, body)
		}
		if field == "model" {
			continue
		}
		if string(gotRaw) != string(wantRaw) {
			t.Fatalf("field %q changed: upstream %s, client %s", field, gotRaw, wantRaw)
		}
	}
	if got := modelOf(t, body); got != "target-1" {
		t.Fatalf("upstream model = %q, want target-1", got)
	}
}

// 3+4. Streaming gets the same rewrite (the stream path builds its own
// request; it is easy to fix one and forget the other).
func TestGatewayStreamSendsTargetModelNotClientAlias(t *testing.T) {
	svc, key, captured, _ := newUpstreamModelSvc(t, CreateModelRouteRequest{
		Scenario: "chat", ModelAlias: "alias-stream", TargetModel: "target-stream",
	})
	raw := []byte(`{"model":"alias-stream","stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	var out strings.Builder
	status, _, err := svc.WriteStreamCompletion(context.Background(), key, raw, &out, "req-alias-stream")
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	bodies := captured.all()
	if len(bodies) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(bodies))
	}
	if got := modelOf(t, bodies[0]); got != "target-stream" {
		t.Fatalf("stream upstream model = %q, want target-stream", got)
	}
	var probe struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &probe); err != nil || !probe.Stream {
		t.Fatalf("stream flag lost on the way upstream: %s", bodies[0])
	}
	if !strings.Contains(out.String(), "[DONE]") {
		t.Fatalf("stream was not passed through: %q", out.String())
	}
}

// 5. Route isolation: two aliases pointing at different targets must not
// cross. The upstream sees exactly the target of the alias that was called.
func TestGatewayRoutesDoNotCrossModels(t *testing.T) {
	var captured *capturedUpstream
	svc, key, cap, _ := newUpstreamModelSvc(t,
		CreateModelRouteRequest{Scenario: "chat", ModelAlias: "fast", TargetModel: "model-fast"},
		CreateModelRouteRequest{Scenario: "chat", ModelAlias: "smart", TargetModel: "model-smart"},
	)
	captured = cap
	for _, tc := range []struct{ alias, want string }{
		{"fast", "model-fast"},
		{"smart", "model-smart"},
		{"fast", "model-fast"},
	} {
		raw := []byte(`{"model":"` + tc.alias + `","messages":[{"role":"user","content":"x"}]}`)
		if _, err := svc.ChatCompletion(context.Background(), key, raw, "req-cross-"+tc.alias); err != nil {
			t.Fatalf("%s: %v", tc.alias, err)
		}
	}
	bodies := captured.all()
	want := []string{"model-fast", "model-smart", "model-fast"}
	if len(bodies) != len(want) {
		t.Fatalf("upstream saw %d requests, want %d", len(bodies), len(want))
	}
	for i := range want {
		if got := modelOf(t, bodies[i]); got != want[i] {
			t.Fatalf("request %d: upstream model = %q, want %q (routes crossed)", i, got, want[i])
		}
	}
}

// 6. Client forgery: a caller cannot pick the upstream model by writing it
// into the body — not into `model` (that only selects the route) and not into
// look-alike fields.
func TestGatewayClientCannotForgeUpstreamModel(t *testing.T) {
	svc, key, captured, _ := newUpstreamModelSvc(t, CreateModelRouteRequest{
		Scenario: "chat", ModelAlias: "logical", TargetModel: "configured-target",
	})
	raw := []byte(`{"model":"logical","target_model":"evil-target","upstream_model":"evil-target",` +
		`"messages":[{"role":"user","content":"x"}],"model_override":{"model":"evil-target"}}`)
	if _, err := svc.ChatCompletion(context.Background(), key, raw, "req-forge"); err != nil {
		t.Fatal(err)
	}
	body := captured.all()[0]
	if got := modelOf(t, body); got != "configured-target" {
		t.Fatalf("upstream model = %q, want configured-target", got)
	}
	// The look-alike fields are the caller's data and travel verbatim; the
	// upstream model is decided by the first `model` key only.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatal(err)
	}
	if string(probe["target_model"]) != `"evil-target"` {
		t.Fatalf("an unrelated field was tampered with: %s", probe["target_model"])
	}
}

// 7. Error paths: unknown route stays 404, a route with no target model is
// refused instead of falling back to the client's spelling, and an upstream
// 404 keeps the provider's own semantics.
func TestGatewayUpstreamModelErrorPaths(t *testing.T) {
	svc, key, _, server := newUpstreamModelSvc(t, CreateModelRouteRequest{
		Scenario: "chat", ModelAlias: "known", TargetModel: "known-target",
	})
	ctx := context.Background()

	_, err := svc.ChatCompletion(ctx, key, []byte(`{"model":"unknown","messages":[]}`), "req-unknown")
	var he *httperr.Error
	if !asHTTPErr(err, &he) || he.Status != 404 {
		t.Fatalf("unknown route: %v, want 404", err)
	}

	// A route row with an empty target model is a misconfiguration: refusing
	// it is the point (falling back to the client's spelling would restore the
	// very behaviour this fix removes).
	tenantID := key.TenantID
	if err := svc.Store.CreateModelRoute(ctx, &model.ModelRoute{
		ID: "route-no-target", TenantID: tenantID, ProviderID: "upstream-provider",
		Scenario: "chat", ModelAlias: "no-target", TargetModel: "", Enabled: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ChatCompletion(ctx, key, []byte(`{"model":"no-target","messages":[]}`), "req-no-target")
	if !asHTTPErr(err, &he) || he.Status != 502 || he.Code != 50223 {
		t.Fatalf("route without a target model: %v, want 502/50223", err)
	}

	// Upstream 404: the provider's status and body are passed back unchanged
	// (the caller must be able to see which model the provider refuses).
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"Model known-target is not available","code":"model_not_found"}}`)
	})
	res, err := svc.ChatCompletion(ctx, key, []byte(`{"model":"known","messages":[]}`), "req-upstream-404")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 404 || !strings.Contains(string(res.Body), "model_not_found") {
		t.Fatalf("upstream 404 was rewritten: %d %s", res.Status, res.Body)
	}
}

func asHTTPErr(err error, target **httperr.Error) bool {
	if err == nil {
		return false
	}
	if he, ok := err.(*httperr.Error); ok {
		*target = he
		return true
	}
	return false
}

// E95：`target_model` 除了「缺失」，还有两种「看着有值其实是空」的形态。
// 空白串如果被原样转发，上游会以 400/404 的形式把它变成一次难排查的失败。
func TestGatewayRejectsBlankTargetModel(t *testing.T) {
	svc, _, _, _ := newUpstreamModelSvc(t, CreateModelRouteRequest{
		Scenario: "chat", ModelAlias: "blank-alias", TargetModel: "some-target",
	})
	route := &model.ModelRoute{
		ID: "route-blank", TenantID: "t", Scenario: "chat", ModelAlias: "blank-alias",
		TargetModel: "   ", Enabled: true,
	}
	_, err := rewriteModelForUpstream([]byte(`{"model":"blank-alias","messages":[]}`), route)
	var he *httperr.Error
	if !asHTTPErr(err, &he) || he.Status != 502 || he.Code != 50223 {
		t.Fatalf("whitespace-only target model: %v, want 502/50223", err)
	}

	// 配置层同样拒绝：`" Qwen3.8-27B"` 与 `Qwen3.8-27B` 是两条永远匹配不上的
	// 路由，而在配置页上两者看起来一模一样。
	for _, name := range []struct{ alias, target string }{
		{"", "target"},
		{"alias", ""},
		{"   ", "target"},
		{"alias", "	"},
		{" alias", "target"},
		{"alias", "target "},
	} {
		if _, err := svc.CreateModelRoute(context.Background(), "t", CreateModelRouteRequest{
			ProviderID: "p", Scenario: "chat", ModelAlias: name.alias, TargetModel: name.target,
		}); err == nil {
			t.Fatalf("CreateModelRoute accepted alias=%q target=%q", name.alias, name.target)
		} else if !asHTTPErr(err, &he) || he.Code != 40031 {
			t.Fatalf("CreateModelRoute(%q,%q): %v, want 40031", name.alias, name.target, err)
		}
	}
}

// E95：重复的顶层 `model` 键必须拒绝，而不是「后者胜」。
// 静默选一个会让「跑哪个模型」取决于键的书写顺序，而审计看到的是另一个。
func TestGatewayRejectsDuplicateModelField(t *testing.T) {
	svc, key, rec, _ := newUpstreamModelSvc(t, CreateModelRouteRequest{
		Scenario: "chat", ModelAlias: "dup-alias", TargetModel: "dup-target",
	})
	ctx := context.Background()

	// 两个 model 键，值不同：无论留下哪一个都在猜。
	_, err := svc.ChatCompletion(ctx, key, []byte(`{"model":"dup-alias","model":"other","messages":[]}`), "req-dup")
	var he *httperr.Error
	if !asHTTPErr(err, &he) || he.Status != 400 || he.Code != 40060 {
		t.Fatalf("duplicate model key: %v, want 400/40060", err)
	}
	if got := len(rec.all()); got != 0 {
		t.Fatalf("duplicate model key still reached the upstream %d time(s)", got)
	}

	// 没有 model 键在选路由那一步就被拒（40061）：改写要求「恰好一个」，
	// 但「一个都没有」有更准的话术，不该被折进「非法请求体」。
	_, err = svc.ChatCompletion(ctx, key, []byte(`{"messages":[]}`), "req-no-model")
	if !asHTTPErr(err, &he) || he.Code != 40061 {
		t.Fatalf("missing model key: %v, want 400/40061", err)
	}

	// 嵌套对象里的 model 不算顶层重复（工具定义里常有 `"model"` 字段）。
	if _, err := svc.ChatCompletion(ctx, key, []byte(
		`{"model":"dup-alias","messages":[],"metadata":{"model":"inner"},"tools":[{"function":{"name":"f","parameters":{"model":"x"}}}]}`,
	), "req-nested-model"); err != nil {
		t.Fatalf("nested model field inside metadata/tools was treated as a duplicate: %v", err)
	}
	bodies := rec.all()
	if len(bodies) != 1 {
		t.Fatalf("nested-model request reached the upstream %d time(s), want 1", len(bodies))
	}
	if got := modelOf(t, bodies[len(bodies)-1]); got != "dup-target" {
		t.Fatalf("upstream model = %q, want dup-target", got)
	}

	// 顶层恰好一个 model 但值不是字符串：同样是 400/40060，且**不**转发。
	// 把它「当成能覆盖」会让 `"model":123` 这种请求体一路走到上游。
	if _, err := svc.ChatCompletion(ctx, key, []byte(`{"model":123,"messages":[]}`), "req-model-number"); err == nil {
		t.Fatal("non-string model value was accepted")
	} else if !asHTTPErr(err, &he) || he.Code != 40060 {
		t.Fatalf("non-string model value: %v, want 400/40060", err)
	}
	if got := len(rec.all()); got != 1 {
		t.Fatalf("non-string model request reached the upstream (total %d, want 1)", got)
	}
}
