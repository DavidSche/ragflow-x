//go:build live

package service

// A live drill of the semantic layer against a real model.
//
// Build-tagged so it can never run in CI (`go test` without `-tags live`), and
// env-gated so it can never run by accident. It exists because doc/304 §7 item 8
// is only half about code: the contract is pinned by the untagged tests next to
// this file, but "does the judge actually judge" needs a model, and the drill
// host has none registered (E124). Running it here, against the aggregation
// gateway in doc/environment §1.4, answers that question without touching any
// tenant setting and without deciding which model the release will use (item 15).
//
//	RGX_LIVE_REVIEW_KEY=sk-… go test ./internal/service/ -tags live \
//	  -run TestReviewModelLiveDrill -count=1 -v
//
// The shim in the middle is deliberate. `ChatToModel` speaks RAGFlow's envelope
// (`{code:0,data:{answer}}`) and an OpenAI-compatible gateway speaks
// `choices[0].message.content`; translating in the *test* keeps every line of the
// production path (prompt rendering, HTTP client, parser, token estimator) under
// test, and puts the fake exactly where the engine would be.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func liveReviewConfig(t *testing.T) (base, key, modelName string) {
	t.Helper()
	key = strings.TrimSpace(os.Getenv("RGX_LIVE_REVIEW_KEY"))
	if key == "" {
		t.Skip("set RGX_LIVE_REVIEW_KEY to run the live review drill")
	}
	base = strings.TrimSpace(os.Getenv("RGX_LIVE_REVIEW_BASE"))
	if base == "" {
		base = "https://www.ainet.uno/llm/v1"
	}
	modelName = strings.TrimSpace(os.Getenv("RGX_LIVE_REVIEW_MODEL"))
	if modelName == "" {
		modelName = "Qwen/Qwen3.8-27B-FP8"
	}
	return base, key, modelName
}

// openAIEnvelopeShim translates the RAGFlow chat route into an OpenAI-compatible
// call and back, logging the usage numbers the gateway reports (the production
// client cannot see them: the engine envelope carries no usage).
func openAIEnvelopeShim(t *testing.T, base, key string) *httptest.Server {
	t.Helper()
	client := &http.Client{Timeout: 120 * time.Second}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			http.Error(w, "{}", http.StatusBadRequest)
			return
		}
		var engineReq struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &engineReq); err != nil {
			t.Errorf("engine request is not JSON: %s", string(raw))
			http.Error(w, "{}", http.StatusBadRequest)
			return
		}
		forward, err := json.Marshal(map[string]interface{}{
			"model":    engineReq.Model,
			"messages": engineReq.Messages,
			"stream":   false,
			// The gateway's Qwen routes burn the completion budget inside
			// `reasoning` unless thinking is switched off (doc/environment §1.4).
			"chat_template_kwargs": map[string]interface{}{"enable_thinking": false},
			"max_tokens":           512,
			"temperature":          0,
		})
		if err != nil {
			t.Errorf("encode upstream request: %v", err)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(forward))
		if err != nil {
			t.Errorf("build upstream request: %v", err)
			return
		}
		request.Header.Set("content-type", "application/json")
		request.Header.Set("authorization", "Bearer "+key)
		started := time.Now()
		resp, err := client.Do(request)
		if err != nil {
			t.Errorf("upstream call: %v", err)
			http.Error(w, `{"code":502}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("upstream %d: %s", resp.StatusCode, truncateForLog(string(body), 300))
			http.Error(w, `{"code":502}`, http.StatusBadGateway)
			return
		}
		var completion struct {
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &completion); err != nil || len(completion.Choices) == 0 {
			t.Errorf("upstream answer is not an OpenAI completion: %s", truncateForLog(string(body), 300))
			http.Error(w, `{"code":502}`, http.StatusBadGateway)
			return
		}
		choice := completion.Choices[0]
		t.Logf("upstream: %s model=%s %.1fs prompt=%d completion=%d finish=%s reasoning=%d chars",
			time.Since(started).Round(100*time.Millisecond), engineReq.Model,
			time.Since(started).Seconds(), completion.Usage.PromptTokens,
			completion.Usage.CompletionTokens, choice.FinishReason, len(choice.Message.Reasoning))
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "message": "", "data": map[string]interface{}{"answer": choice.Message.Content},
		})
	}))
}

// TestReviewDirectTransportLiveDrill is the E127 counterpart of the drill above.
//
// The shim is gone: this calls the provider the way the run will, so `model`
// selects and the answer says which model produced it. Two readings, both of them
// things the engine route cannot give:
//
//  1. the registered model answers and the answer parses (the same four
//     claim/quote pairs are not repeated here — this drill is about the *transport*,
//     not about judgement);
//
//  2. a model name the provider does not have **fails**. On the engine route the
//     same bogus name answered 200 with a correct paragraph (doc/304 E125), which
//     is exactly why the direct path exists.
//
//     RGX_LIVE_REVIEW_KEY=sk-… go test ./internal/service/ -tags live //	  -run TestReviewDirectTransportLiveDrill -count=1 -v
func TestReviewDirectTransportLiveDrill(t *testing.T) {
	base, key, modelName := liveReviewConfig(t)
	// A throwaway sealing key: the transport's job is to decrypt what the store
	// holds, and one AES-GCM round trip is enough to prove it does.
	sealing := make([]byte, 32)
	for index := range sealing {
		sealing[index] = byte(index*7 + 3)
	}
	sealed, err := crypto.Encrypt(sealing, key)
	if err != nil {
		t.Fatalf("seal key: %v", err)
	}
	svc := &Service{EncryptKey: sealing}
	modelName = liveReviewModelName(t, base, key, modelName)

	transport := newDirectReviewTransport(svc, assuranceReviewRoute{
		Transport: assuranceTransportProviderDirect, Model: modelName,
		BaseURL: base, APIKeyEnc: sealed,
	})
	prompt := buildReportAssurancePrompt(reportAssuranceEvalInput{
		Section: model.ReportSection{Title: "退款政策"},
		Claim:   model.ReportClaim{ClaimText: "退款申请须在收货后 7 天内提出。"},
		Quote:   "客户应在收货后 3 天内联系客服申请退款，逾期不再受理。",
	})
	started := time.Now()
	answer, err := transport.Chat(context.Background(), prompt)
	if err != nil {
		t.Fatalf("direct transport: %v", err)
	}
	outcome := parseReportAssuranceAnswer(answer)
	t.Logf("direct transport: %.1fs bytes=%d verdict=%s rationale=%q",
		time.Since(started).Seconds(), len(answer), outcome.Verdict, outcome.Rationale)
	if !reportAssuranceVerdictFromModel(outcome.Verdict) {
		t.Fatalf("verdict %q is outside the closed set (raw answer: %s)", outcome.Verdict, truncateForLog(answer, 200))
	}

	bogus := newDirectReviewTransport(svc, assuranceReviewRoute{
		Transport: assuranceTransportProviderDirect, Model: "does-not-exist-xyz",
		BaseURL: base, APIKeyEnc: sealed,
	})
	if _, err := bogus.Chat(context.Background(), "只回复两个字：收到"); err == nil {
		t.Fatal("a model name the provider does not have must fail on the direct path; " +
			"if it answers, `model` is inert again and the independence claim is unprovable")
	} else {
		t.Logf("bogus model refused as expected: %v", err)
	}
}

// liveReviewModelName picks a model name the provider actually serves, using
// `/models` rather than a guess: the drill's value is that it runs against
// reality, and a hard-coded name that drifted would make the drill "fail" for the
// wrong reason.
func liveReviewModelName(t *testing.T, base, key, fallback string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return fallback
	}
	request.Header.Set("authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return fallback
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		return fallback
	}
	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return fallback
	}
	for _, entry := range listing.Data {
		if entry.ID == fallback {
			return fallback
		}
	}
	for _, entry := range listing.Data {
		if strings.Contains(strings.ToLower(entry.ID), "qwen") {
			t.Logf("model %q is not served; using %q from /models", fallback, entry.ID)
			return entry.ID
		}
	}
	return fallback
}

func truncateForLog(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n] + "…"
}

// TestReviewModelLiveDrill feeds four hand-written claim/quote pairs through the
// production evaluator and prints the model's parsed answer for each.
//
// The three assertions are deliberately weak: a closed-set verdict, a non-empty
// rationale, and "a quote that says 3 days does not support a claim that says 7".
// Anything stronger would be asserting a model's judgement in a test, which is
// the thing a gold-labelled sample is for. The point of this drill is the middle
// of the sentence in item 8 — 判得动 — not 判得准.
func TestReviewModelLiveDrill(t *testing.T) {
	base, key, modelName := liveReviewConfig(t)
	shim := openAIEnvelopeShim(t, base, key)
	defer shim.Close()

	svc := &Service{RAGFlow: ragflow.NewHTTPClient(shim.URL, "engine-key", 60*time.Second, 1)}
	evaluator := svc.newReportAssuranceEvaluator(context.Background(), &model.ReportAssuranceRun{
		Mode: model.ReportAssuranceModeSemantic, ReviewModelID: modelName,
		ReviewProvider: "openai", ReviewDeployment: "ainet",
		Independence: model.ReportIndependenceSeparateProvider,
	})

	cases := []struct {
		name    string
		claim   string
		quote   string
		mustNot string
	}{
		{
			name:  "quote restates the claim",
			claim: "退款申请须在收货后 3 天内提出。",
			quote: "客户应在收货后 3 天内联系客服申请退款，逾期不再受理。",
		},
		{
			name:    "quote contradicts the claim",
			claim:   "退款申请须在收货后 7 天内提出。",
			quote:   "客户应在收货后 3 天内联系客服申请退款，逾期不再受理。",
			mustNot: model.ReportVerdictSupported,
		},
		{
			name:    "quote is about something else",
			claim:   "退款申请须在收货后 3 天内提出。",
			quote:   "本产品的保修期为自购买之日起十二个月，保修范围不含人为损坏。",
			mustNot: model.ReportVerdictSupported,
		},
		{
			name:  "quote is weaker than the claim (scope inflation)",
			claim: "所有渠道购买的订单均可无理由退款。",
			quote: "线上渠道购买的订单可在 7 天内无理由退款。",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := evaluator.Evaluate(context.Background(), reportAssuranceEvalInput{
				Section: model.ReportSection{Title: "退款政策"},
				Claim:   model.ReportClaim{ClaimText: tc.claim},
				Quote:   tc.quote,
			})
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			confidence := "nil"
			if outcome.Confidence != nil {
				confidence = fmt.Sprintf("%.2f", *outcome.Confidence)
			}
			t.Logf("verdict=%s relevance=%s confidence=%s tokens=%d/%d rationale=%q quote=%q",
				outcome.Verdict, outcome.Relevance, confidence,
				outcome.TokensIn, outcome.TokensOut, outcome.Rationale, outcome.EvidenceQuote)
			if !reportAssuranceVerdictFromModel(outcome.Verdict) {
				t.Fatalf("verdict %q is outside the closed set a model may return", outcome.Verdict)
			}
			if strings.TrimSpace(outcome.Rationale) == "" {
				t.Fatalf("verdict %q came with no rationale", outcome.Verdict)
			}
			if tc.mustNot != "" && outcome.Verdict == tc.mustNot {
				t.Fatalf("verdict %q for %q (the quote does not support it)", outcome.Verdict, tc.name)
			}
			if outcome.TokensIn <= 0 || outcome.TokensOut <= 0 {
				t.Fatalf("token estimate missing: in=%d out=%d", outcome.TokensIn, outcome.TokensOut)
			}
			// The prompt asks for a verbatim slice or an empty string, and the
			// citation checker downstream *depends* on that: a paraphrased quote
			// becomes `quote_status=not_found` and blocks the semantic verdict
			// (runbook「AssuranceQuoteNotFound」). Checking it here keeps the
			// promise in the prompt from being unobservable.
			if outcome.EvidenceQuote != "" && !strings.Contains(tc.quote, outcome.EvidenceQuote) {
				t.Fatalf("quote %q is not a verbatim slice of the evidence %q", outcome.EvidenceQuote, tc.quote)
			}
		})
	}
}
