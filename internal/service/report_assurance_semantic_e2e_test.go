package service

// The end-to-end half of the semantic layer: the evaluator, the real RAGFlow
// client, and an HTTP server standing in for the engine.
//
// doc/304 §7 item 8 says the semantic layer has never been exercised — on the
// drill host no review model is configured, so every run is tier_0. A
// configuration change cannot become a test (it is not in the repo, it cannot be
// re-run), but everything between "a model is configured" and "a verdict row is
// written" can: that is this file. The fake sits at the transport, i.e. exactly
// where the engine sits, so the code under test is the code that runs in
// production (prompt rendering, the client, the parser, the estimator).
//
// What this does NOT prove, and item 8 keeps saying: whether a given model's
// verdicts are *right*. That needs a gold-labelled sample and a chosen model, and
// choosing the evaluation model is a PM/QA decision (item 15), not a default this
// test could smuggle in.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// assuranceEngineStub answers the chat-completion route the way the live engine
// does: `{code:0, data:{answer:…}}`, with the model name echoed back only so the
// test can assert which model was addressed.
func assuranceEngineStub(t *testing.T, answer string, seen *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Errorf("evaluator must use the chat-completion route, got %s", r.URL.Path)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if seen != nil {
			var body map[string]interface{}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("request body is not JSON: %s", string(raw))
				return
			}
			*seen = body
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "message": "", "data": map[string]interface{}{"answer": answer},
		})
	}))
}

// The end-to-end case runs over the *direct* transport, because that is the path a
// resolved review reference takes (doc/304 E127). The prompt rendering, the client,
// the parser and the estimator are the same code either way; what the direct path
// adds is that the model name is observable, so this test can assert the body names
// it *and* that the stub echoes it back.
func TestModelEvaluatorEndToEnd(t *testing.T) {
	var seen map[string]interface{}
	answer := `{"verdict":"partially_supported","relevance":"on_topic","confidence":0.62,` +
		`"rationale":"引文写 3 天，断言写 7 天","quote":"3 天内申请退款"}`
	srv := assuranceProviderStub(t, map[string]string{"Qwen/Qwen3.8-27B-FP8": answer}, &seen)
	defer srv.Close()

	svc := &Service{}
	evaluator := svc.assuranceModelEvaluatorFor(&model.ReportAssuranceRun{
		Mode:          model.ReportAssuranceModeSemantic,
		ReviewModelID: "Qwen/Qwen3.8-27B-FP8", ReviewProvider: "openai",
		ReviewDeployment: "ainet", Independence: model.ReportIndependenceSeparateProvider,
	}, assuranceReviewRoute{
		Transport: assuranceTransportProviderDirect, Instance: "ainet", Model: "Qwen/Qwen3.8-27B-FP8",
		Provider: "openai", BaseURL: srv.URL, Reason: "test route",
	})
	if evaluator.Method() != model.ReportEvaluationMethodSemantic {
		t.Fatalf("method = %q, want semantic", evaluator.Method())
	}

	outcome, err := evaluator.Evaluate(context.Background(), reportAssuranceEvalInput{
		Section:        model.ReportSection{Title: "退款政策"},
		Claim:          model.ReportClaim{ClaimText: "退款需在 7 天内申请"},
		Quote:          "客户应在收货后 3 天内申请退款。",
		CandidateCount: 2,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if outcome.Verdict != model.ReportVerdictPartiallySupported {
		t.Fatalf("verdict = %q, want partially_supported", outcome.Verdict)
	}
	if outcome.Confidence == nil || *outcome.Confidence != 0.62 {
		t.Fatalf("confidence = %v, want 0.62", outcome.Confidence)
	}
	// The prompt that reached the model is the rendered template — claim, quote,
	// section title and the widened-pool note — not a bare claim. If this drifts,
	// `prompt_hash` stops describing what was asked (doc/301 §6.1).
	if seen["model"] != "Qwen/Qwen3.8-27B-FP8" {
		t.Fatalf("body must name the review model, got %+v", seen["model"])
	}
	messages, _ := seen["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("body must carry one user message, got %+v", seen["messages"])
	}
	prompt, _ := messages[0].(map[string]interface{})["content"].(string)
	for _, want := range []string{
		"【章节】退款政策", "【报告断言】退款需在 7 天内申请",
		"【证据原文】客户应在收货后 3 天内申请退款。", "冻结了 2 条",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt sent to the model is missing %q", want)
		}
	}
	// Tokens are estimated (the engine returns no usage), and the estimate must
	// cover both directions so the budget guard has something to subtract.
	if outcome.TokensIn <= 0 || outcome.TokensOut <= 0 {
		t.Fatalf("token estimate missing: in=%d out=%d", outcome.TokensIn, outcome.TokensOut)
	}
	if evaluator.PromptHash() != reportAssurancePromptHash() {
		t.Fatalf("evaluator prompt hash is not the template hash")
	}
	if evaluator.Independence() != model.ReportIndependenceSeparateProvider {
		t.Fatalf("independence = %q, want independent_provider", evaluator.Independence())
	}
	// The model id stored on the evaluation row is the registered name, not the
	// `model@instance@provider` reference an operator typed.
	if evaluator.ModelID() != "Qwen/Qwen3.8-27B-FP8" {
		t.Fatalf("model id = %q, want the registered model name", evaluator.ModelID())
	}
}

// The engine route cannot show who answered, so a run that falls back to it must
// report `unknown` — a configured provider on that path is a statement about
// configuration, not about the answerer (doc/304 E125/E127).
func TestModelEvaluatorDowngradesIndependenceOnTheEngineRoute(t *testing.T) {
	var seen map[string]interface{}
	srv := assuranceEngineStub(t, `{"verdict":"supported"}`, &seen)
	defer srv.Close()

	svc := &Service{RAGFlow: ragflow.NewHTTPClient(srv.URL, "key", 5*time.Second, 1)}
	evaluator := svc.assuranceModelEvaluatorFor(&model.ReportAssuranceRun{
		TenantID: "t", Mode: model.ReportAssuranceModeSemantic,
		ReviewModelID: "Qwen/Qwen3.8-27B-FP8@ainet@openai", ReviewProvider: "openai",
		ReviewDeployment: "ainet", Independence: model.ReportIndependenceSeparateProvider,
	}, engineRoute("the store is not configured in this process"))
	if evaluator.Independence() != model.ReportIndependenceUnknown {
		t.Fatalf("independence on the engine route = %q, want unknown", evaluator.Independence())
	}
	// And it must not name a model either: `method=semantic` says a model judged,
	// `model_id` empty says we cannot name it. The two columns are separate facts
	// (doc/304 E142), and the live drill showed the old wiring naming the
	// operator's reference on a route that never addressed it.
	if evaluator.ModelID() != "" {
		t.Fatalf("model id on the engine route = %q, want empty", evaluator.ModelID())
	}
	// The call still happens — a tier-0 answer is a real answer, it is the *claim*
	// that weakens, not the run.
	if _, err := evaluator.Evaluate(context.Background(), reportAssuranceEvalInput{
		Claim: model.ReportClaim{ClaimText: "x"}, Quote: "y",
	}); err != nil {
		t.Fatalf("the engine route must still answer: %v", err)
	}
}

// assuranceProviderStub stands in for an OpenAI-compatible provider: it answers
// `{model, choices:[{message:{content}}]}`, and it echoes the model it was asked
// for — which is the property the direct transport requires and the engine route
// cannot provide.
func assuranceProviderStub(t *testing.T, answers map[string]string, seen *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("direct transport must post to /chat/completions, got %s", r.URL.Path)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %s", string(raw))
			return
		}
		if seen != nil {
			*seen = body
		}
		model, _ := body["model"].(string)
		answer, ok := answers[model]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"model_not_found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"model": model,
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": answer}},
			},
		})
	}))
}

// A transport failure must stay an error so the caller records `model_error` and
// the run degrades. Turning it into `abstained` here would hide an outage inside
// a verdict, which is the failure mode the tier vocabulary exists to prevent.
func TestModelEvaluatorKeepsTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":502,"message":"upstream unavailable"}`))
	}))
	defer srv.Close()

	svc := &Service{RAGFlow: ragflow.NewHTTPClient(srv.URL, "key", 5*time.Second, 1)}
	evaluator := svc.newReportAssuranceEvaluator(context.Background(), &model.ReportAssuranceRun{
		Mode: model.ReportAssuranceModeSemantic, ReviewModelID: "m",
	})
	if _, err := evaluator.Evaluate(context.Background(), reportAssuranceEvalInput{
		Claim: model.ReportClaim{ClaimText: "x"}, Quote: "y",
	}); err == nil {
		t.Fatal("an upstream 502 must surface as an error, not as a verdict")
	}
}

// The engine answers 200 with an envelope-level error when the model rejects the
// prompt (probed live: a bool `thinking` field), so a 200 body without an answer
// is another form of "no model answered".
func TestModelEvaluatorTreatsEmptyAnswerAsError(t *testing.T) {
	srv := assuranceEngineStub(t, "   ", nil)
	defer srv.Close()

	svc := &Service{RAGFlow: ragflow.NewHTTPClient(srv.URL, "key", 5*time.Second, 1)}
	evaluator := svc.newReportAssuranceEvaluator(context.Background(), &model.ReportAssuranceRun{
		Mode: model.ReportAssuranceModeSemantic, ReviewModelID: "m",
	})
	if _, err := evaluator.Evaluate(context.Background(), reportAssuranceEvalInput{
		Claim: model.ReportClaim{ClaimText: "x"}, Quote: "y",
	}); err == nil {
		t.Fatal("an empty answer must surface as an error")
	}
}
