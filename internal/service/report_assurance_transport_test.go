package service

// Tests for the review transport decision and the direct provider call
// (doc/304 E127).
//
// The point of these tests is the property E125 found missing: *which model
// answered must be observable*. `assuranceReviewRouteOf` is a pure decision over
// rows, so every branch that sends a run back to the unobservable engine route is
// pinned here without a database; the direct transport is exercised against an
// OpenAI-shaped server at the transport seam, so the production code path (body,
// headers, echo check, parse) is the one under test.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
)

func assuranceTestInstance(id, name, providerID, baseURL, key string) model.ModelProviderInstance {
	return model.ModelProviderInstance{
		ID: id, InstanceName: name, ProviderID: providerID,
		BaseURL: baseURL, APIKeyEnc: key, Status: model.ProviderStatusActive,
	}
}

func assuranceTestProvider(id, name, baseURL, key string) model.ModelProvider {
	return model.ModelProvider{ID: id, Name: name, BaseURL: baseURL, APIKeyEnc: key}
}

func assuranceTestModel(instanceID, name, status string, modelType int) model.ModelProviderModel {
	return model.ModelProviderModel{
		ID: "m-" + name, InstanceID: instanceID, ModelName: name,
		Status: status, ModelType: modelType,
	}
}

func TestAssuranceReviewRouteOf(t *testing.T) {
	ref := ReportModelRef{
		ModelID: "Qwen/Qwen3.8-27B-FP8", Name: "Qwen/Qwen3.8-27B-FP8",
		Instance: "ainet", Provider: "ainet",
	}
	instances := []model.ModelProviderInstance{
		assuranceTestInstance("i1", "ainet", "p1", "https://gw.example/llm/v1", "sealed-1"),
		assuranceTestInstance("i2", "loopback", "p1", "", ""),
		assuranceTestInstance("i3", "retired", "p1", "https://old.example/v1", ""),
	}
	instances[2].Status = "disabled"
	providers := []model.ModelProvider{
		assuranceTestProvider("p1", "ainet", "https://gw.example/llm/v1", "sealed-provider"),
		assuranceTestProvider("p2", "other", "https://other.example/v1", ""),
	}
	models := map[string][]model.ModelProviderModel{
		"i1": {assuranceTestModel("i1", "Qwen/Qwen3.8-27B-FP8", model.ProviderStatusActive, model.ModelTypeChat)},
		"i2": {assuranceTestModel("i2", "Qwen/Qwen3.8-27B-FP8", model.ProviderStatusActive, model.ModelTypeChat)},
		"i3": {assuranceTestModel("i3", "Qwen/Qwen3.8-27B-FP8", model.ProviderStatusActive, model.ModelTypeChat)},
	}

	cases := []struct {
		name      string
		ref       ReportModelRef
		instances []model.ModelProviderInstance
		providers []model.ModelProvider
		models    map[string][]model.ModelProviderModel
		want      string
		wantURL   string
		wantKey   string
	}{
		{
			name: "a reference without an instance cannot be addressed directly",
			ref:  ReportModelRef{Name: "Qwen/Qwen3.8-27B-FP8", Provider: "ainet"},
			want: assuranceTransportEngine,
		},
		{
			name: "an unregistered instance falls back to the engine",
			ref:  ReportModelRef{Name: "Qwen/Qwen3.8-27B-FP8", Instance: "nowhere"},
			want: assuranceTransportEngine,
		},
		{
			name:      "an instance whose provider name disagrees with the reference falls back",
			ref:       ReportModelRef{Name: "Qwen/Qwen3.8-27B-FP8", Instance: "ainet", Provider: "other"},
			instances: instances, providers: providers, models: models,
			want: assuranceTransportEngine,
		},
		{
			name:      "an unregistered model name falls back instead of trusting the provider",
			ref:       ReportModelRef{Name: "not-a-model", Instance: "ainet"},
			instances: instances, providers: providers, models: models,
			want: assuranceTransportEngine,
		},
		{
			name:      "a non-chat model falls back",
			ref:       ReportModelRef{Name: "emb", Instance: "ainet"},
			instances: instances, providers: providers,
			models: map[string][]model.ModelProviderModel{
				"i1": {assuranceTestModel("i1", "emb", model.ProviderStatusActive, model.ModelTypeEmbedding)},
			},
			want: assuranceTransportEngine,
		},
		{
			name:      "a retired instance falls back",
			ref:       ReportModelRef{Name: "Qwen/Qwen3.8-27B-FP8", Instance: "retired"},
			instances: instances, providers: providers, models: models,
			want: assuranceTransportEngine,
		},
		{
			name:      "an instance without a URL borrows the provider's URL and key",
			ref:       ReportModelRef{Name: "Qwen/Qwen3.8-27B-FP8", Instance: "loopback"},
			instances: instances, providers: providers, models: models,
			want: assuranceTransportProviderDirect, wantURL: "https://gw.example/llm/v1",
			wantKey: "sealed-provider",
		},
		{
			name: "a registered chat model on an active instance goes direct",
			ref:  ref, instances: instances, providers: providers, models: models,
			want: assuranceTransportProviderDirect, wantURL: "https://gw.example/llm/v1",
			wantKey: "sealed-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route := assuranceReviewRouteOf(tc.ref, tc.instances, tc.providers, tc.models)
			if route.Transport != tc.want {
				t.Fatalf("transport = %q (%s), want %q", route.Transport, route.Reason, tc.want)
			}
			if route.Reason == "" {
				t.Fatalf("every route needs a reason an operator can read")
			}
			if tc.want != assuranceTransportProviderDirect {
				return
			}
			if route.BaseURL != tc.wantURL {
				t.Fatalf("base url = %q, want %q", route.BaseURL, tc.wantURL)
			}
			if route.APIKeyEnc != tc.wantKey {
				t.Fatalf("credential = %q, want %q", route.APIKeyEnc, tc.wantKey)
			}
			if route.Model != tc.ref.Name {
				t.Fatalf("model = %q, want the reference's model name", route.Model)
			}
		})
	}
}

// The independence claim is only allowed to weaken, never to strengthen: a route
// that cannot show the answerer reports `unknown` whatever was configured.
func TestAssuranceIndependenceFor(t *testing.T) {
	direct := assuranceReviewRoute{Transport: assuranceTransportProviderDirect}
	engine := engineRoute("no instance")
	cases := []struct {
		configured string
		route      assuranceReviewRoute
		want       string
	}{
		{"", direct, model.ReportIndependenceUnknown},
		{"", engine, model.ReportIndependenceUnknown},
		{model.ReportIndependenceSeparateProvider, engine, model.ReportIndependenceUnknown},
		{model.ReportIndependenceSameModel, engine, model.ReportIndependenceUnknown},
		{model.ReportIndependenceSeparateProvider, direct, model.ReportIndependenceSeparateProvider},
		{model.ReportIndependenceDifferentFamily, direct, model.ReportIndependenceDifferentFamily},
	}
	for _, tc := range cases {
		if got := assuranceIndependenceFor(tc.configured, tc.route); got != tc.want {
			t.Fatalf("assuranceIndependenceFor(%q, %s) = %q, want %q",
				tc.configured, tc.route.Transport, got, tc.want)
		}
	}
}

// A direct transport must name the model, keep thinking off (a vLLM route spends
// the whole budget inside `reasoning` otherwise), and send the decrypted key.
func TestDirectReviewTransportSendsTheRegisteredModel(t *testing.T) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	sealed, err := crypto.Encrypt(key, "sk-live-test")
	if err != nil {
		t.Fatalf("seal key: %v", err)
	}
	var seen map[string]interface{}
	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seen)
		_, _ = w.Write([]byte(`{"model":"Qwen/Qwen3.8-27B-FP8","choices":[{"message":{"content":"{\"verdict\":\"supported\"}"}}]}`))
	}))
	defer srv.Close()

	transport := newDirectReviewTransport(&Service{EncryptKey: key}, assuranceReviewRoute{
		Transport: assuranceTransportProviderDirect, Model: "Qwen/Qwen3.8-27B-FP8",
		BaseURL: srv.URL, APIKeyEnc: sealed,
	})
	if _, err := transport.Chat(context.Background(), "判断这句话"); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if seen["model"] != "Qwen/Qwen3.8-27B-FP8" {
		t.Fatalf("body must name the model, got %+v", seen["model"])
	}
	if seen["temperature"] != float64(0) {
		t.Fatalf("a review call is not a creative call: temperature = %+v", seen["temperature"])
	}
	kwargs, _ := seen["chat_template_kwargs"].(map[string]interface{})
	if kwargs["enable_thinking"] != false {
		t.Fatalf("thinking must be off explicitly, got %+v", seen["chat_template_kwargs"])
	}
	if authorization != "Bearer sk-live-test" {
		t.Fatalf("authorization header = %q", authorization)
	}
}

// The refusals are the value of this path: each one is a different thing that went
// wrong, and none of them may be silently turned into "some model said".
func TestDirectReviewTransportRefusesUnattributableAnswers(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "no model echo",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			},
			want: "did not echo the model",
		},
		{
			name: "a different model answered",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"model":"some-other-model","choices":[{"message":{"content":"ok"}}]}`))
			},
			want: "answered as some-other-model",
		},
		{
			name: "empty answer",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"content":"   "}}]}`))
			},
			want: "empty answer",
		},
		{
			name: "http error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"model not found"}}`))
			},
			want: "HTTP 404",
		},
		{
			name: "not json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`<html>gateway</html>`))
			},
			want: "not JSON",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			transport := newDirectReviewTransport(&Service{}, assuranceReviewRoute{
				Transport: assuranceTransportProviderDirect, Model: "m", BaseURL: srv.URL,
			})
			_, err := transport.Chat(context.Background(), "x")
			if err == nil {
				t.Fatalf("%s must be an error, not an answer", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), tc.want)
			}
		})
	}
}

// The measurement E125 asked for: two models behind one provider, and the run's
// verdict follows the *named* model. A transport that ignored `model` (the engine's
// v1.0.0 behaviour) would return the same answer for both.
func TestDirectReviewTransportSelectsTheNamedModel(t *testing.T) {
	answers := map[string]string{
		"Qwen/Qwen3.8-27B-FP8": `{"verdict":"supported","rationale":"引文直接支持"}`,
		"deepseek-v4-flash":    `{"verdict":"unsupported","rationale":"引文与断言矛盾"}`,
	}
	srv := assuranceProviderStub(t, answers, nil)
	defer srv.Close()

	verdictOf := func(modelName string) string {
		transport := newDirectReviewTransport(&Service{}, assuranceReviewRoute{
			Transport: assuranceTransportProviderDirect, Model: modelName, BaseURL: srv.URL,
		})
		answer, err := transport.Chat(context.Background(), "x")
		if err != nil {
			t.Fatalf("%s: %v", modelName, err)
		}
		return parseReportAssuranceAnswer(answer).Verdict
	}
	if got := verdictOf("Qwen/Qwen3.8-27B-FP8"); got != model.ReportVerdictSupported {
		t.Fatalf("first model verdict = %q, want supported", got)
	}
	if got := verdictOf("deepseek-v4-flash"); got != model.ReportVerdictUnsupported {
		t.Fatalf("second model verdict = %q, want unsupported — the transport did not select", got)
	}
}

// A provider answering in Chinese must not produce half a rune in the log line: the
// truncation runs on a body that may be CJK, and a mojibake ellipsis costs more to
// read than the extra bytes (E127).
func TestTruncateBodyForLogKeepsRunesIntact(t *testing.T) {
	long := strings.Repeat("模型不存在", 100)
	cut := truncateBodyForLog([]byte(long))
	if !strings.HasSuffix(cut, "…") {
		t.Fatalf("truncated body should end with an ellipsis: %q", cut)
	}
	if limit := 300 + len("…"); len(cut) > limit {
		t.Fatalf("truncated body is %d bytes, want <= %d (300 + the ellipsis; strutil.Truncate is byte-bounded)",
			len(cut), limit)
	}
	if !utf8.ValidString(cut) {
		t.Fatal("truncation split a rune; the log line would be mojibake")
	}
	if short := truncateBodyForLog([]byte("ok")); short != "ok" {
		t.Fatalf("short body should pass through untouched, got %q", short)
	}
}

// The run row stores the operator's whole `model@instance@provider` reference, so the
// worker has to split it back out before it can match a registered model. Passing the
// raw string as the name matched nothing and silently fell back to the engine while the
// frozen policy still said `provider_direct` — a receipt describing a call that did not
// happen. This test pins the round trip: trigger-time reference → row → execution-time
// reference → the same direct route (doc/304 E127).
func TestReviewRouteSurvivesTheRunRow(t *testing.T) {
	instances := []model.ModelProviderInstance{{
		ID: "inst-default", InstanceName: "default", ProviderID: "prov-ainet",
		BaseURL: "https://www.ainet.uno/llm/v1", APIKeyEnc: "sealed", Status: model.ProviderStatusActive,
	}}
	providers := []model.ModelProvider{{ID: "prov-ainet", Name: "ainet", BaseURL: "https://www.ainet.uno/llm/v1"}}
	modelsByInstance := map[string][]model.ModelProviderModel{
		"inst-default": {{ModelName: "Qwen/Qwen3.8-27B-FP8", ModelType: model.ModelTypeChat, Status: model.ProviderStatusActive}},
	}

	policy := AssurancePolicyResolution{ReviewModel: "Qwen/Qwen3.8-27B-FP8@default@ainet"}
	review, _ := reportReviewModelOf(policy, ReportModelRef{ModelID: "generation-model", Provider: "gpustack-local"})
	atTrigger := assuranceReviewRouteOf(review, instances, providers, modelsByInstance)
	if !atTrigger.Direct() {
		t.Fatalf("trigger-time route = %q (%s), want provider_direct", atTrigger.Transport, atTrigger.Reason)
	}

	// What the run row holds after `CreateAssuranceRun`.
	run := &model.ReportAssuranceRun{
		ReviewModelID: review.ModelID, ReviewProvider: review.Provider, ReviewDeployment: review.Instance,
	}
	atExecution := assuranceReviewRouteOf(reviewRefOfRun(run), instances, providers, modelsByInstance)
	if !atExecution.Direct() {
		t.Fatalf("execution-time route = %q (%s), want provider_direct — the row's reference was not parsed back",
			atExecution.Transport, atExecution.Reason)
	}
	if atExecution.Model != atTrigger.Model || atExecution.BaseURL != atTrigger.BaseURL {
		t.Fatalf("execution route drifted from trigger route: %+v vs %+v", atExecution, atTrigger)
	}

	// A bare model id (no `@`) keeps working: independence `same_model` sets the whole
	// reference to the generation model, and the name is then the id itself.
	bare := assuranceReviewRouteOf(reviewRefOfRun(&model.ReportAssuranceRun{ReviewModelID: "Qwen/Qwen3.8-27B-FP8"}),
		instances, providers, modelsByInstance)
	if bare.Transport != assuranceTransportEngine {
		t.Fatalf("a bare model id has no instance to address; got %q", bare.Transport)
	}
}

// TestReviewRouteReasonIsReadableFromTheReceipt pins the second half of E127's goal:
// a run must be able to say *why* it did not go direct, without anyone reading logs.
// The reason is written into the frozen policy, the run view serves the policy, and the
// reason has to name the gate that refused — "fell back" alone still leaves four
// candidate causes (doc/304 E129).
func TestReviewRouteReasonIsReadableFromTheReceipt(t *testing.T) {
	instances := []model.ModelProviderInstance{{
		ID: "inst-default", InstanceName: "default", ProviderID: "prov-ainet",
		BaseURL: "https://www.ainet.uno/llm/v1", APIKeyEnc: "sealed", Status: model.ProviderStatusActive,
	}}
	providers := []model.ModelProvider{{ID: "prov-ainet", Name: "ainet", BaseURL: "https://www.ainet.uno/llm/v1"}}
	// The instance exists, but the named model was not registered on it as chat.
	modelsByInstance := map[string][]model.ModelProviderModel{
		"inst-default": {{ModelName: "qwen3-embedding-0.6b", ModelType: model.ModelTypeEmbedding, Status: model.ProviderStatusActive}},
	}
	route := assuranceReviewRouteOf(
		ReportModelRef{ModelID: "Qwen/Qwen3.8-27B-FP8@default@ainet", Name: "Qwen/Qwen3.8-27B-FP8", Instance: "default", Provider: "ainet"},
		instances, providers, modelsByInstance)
	if route.Direct() {
		t.Fatalf("route = %q, want the engine route: the model is registered as embedding", route.Transport)
	}
	for _, want := range []string{"Qwen/Qwen3.8-27B-FP8", "default"} {
		if !strings.Contains(route.Reason, want) {
			t.Fatalf("reason = %q, want it to name %q — four gates share one sentence otherwise", route.Reason, want)
		}
	}

	// The trigger freezes the route and its reason together; the run view is the
	// receipt an operator actually opens.
	policy := AssurancePolicyResolution{
		Gate: "advise", ScanTopK: 5, BudgetTokens: 1000, Source: map[string]string{},
		ReviewTransport: assuranceTransportEngine, ReviewTransportReason: route.Reason,
	}
	frozen, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal policy: %v", err)
	}
	run := &model.ReportAssuranceRun{ID: "run-1", TenantID: "tenant-1", PolicyJSON: string(frozen)}
	view := assuranceRunViewOf(run)
	if view.Policy.ReviewTransport != assuranceTransportEngine || view.Policy.ReviewTransportReason != route.Reason {
		t.Fatalf("receipt = %q / %q, want %q / %q",
			view.Policy.ReviewTransport, view.Policy.ReviewTransportReason, assuranceTransportEngine, route.Reason)
	}
}

// TestFrozenPolicyComparedWithTheRouteThatRuns covers the comparison E127's write-up
// promised and the code did not have: trigger freezes one transport, the worker
// re-resolves another, and the divergence must be visible. Runs that froze nothing
// (older rows, tier_0) must stay quiet, or the warning becomes noise (doc/304 E129).
func TestFrozenPolicyComparedWithTheRouteThatRuns(t *testing.T) {
	frozen := func(transport, reason string) string {
		blob, err := json.Marshal(AssurancePolicyResolution{ReviewTransport: transport, ReviewTransportReason: reason})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(blob)
	}
	cases := []struct {
		name      string
		policy    string
		resolved  string
		want      bool
		wantInLog string
	}{
		{"same route", frozen(assuranceTransportProviderDirect, "registered chat model"), assuranceTransportProviderDirect, false, ""},
		{"frozen direct, executed engine", frozen(assuranceTransportProviderDirect, "registered chat model"), assuranceTransportEngine, true,
			"frozen provider_direct (registered chat model), resolved engine_chat"},
		{"frozen engine, executed direct", frozen(assuranceTransportEngine, "model is registered as embedding"), assuranceTransportProviderDirect, true,
			"frozen engine_chat"},
		{"nothing frozen is not a divergence", `{"gate":"advise"}`, assuranceTransportEngine, false, ""},
		{"unreadable policy is not a divergence", `not json`, assuranceTransportEngine, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := frozenReviewTransportMismatch(tc.policy, tc.resolved)
			if got != tc.want {
				t.Fatalf("mismatch = %v (%s), want %v", got, detail, tc.want)
			}
			if tc.wantInLog != "" && !strings.Contains(detail, tc.wantInLog) {
				t.Fatalf("detail = %q, want it to contain %q", detail, tc.wantInLog)
			}
		})
	}
}
