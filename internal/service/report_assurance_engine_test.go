package service

// Tests for the semantic evaluator (doc/301 §5.1 layer ③, §5.4, §6.6).
//
// These exist because §7 item 8 of doc/304 recorded the semantic layer as
// "unverified": on the drill host `report.assurance_review_model` is empty, so
// every run is tier_0 and the model path never executes. A configuration change
// is not a test — nothing about it can be re-run in CI. What *can* be settled
// without a model is the part that decides what a model's answer is allowed to
// mean: which evaluator a run gets, what a verdict outside the closed set turns
// into, and when "no model" is `abstained` rather than `supported`. Those are
// exactly the branches that would be wrong-by-default if nobody wrote them down,
// and they are what this file pins.
//
// The network call itself (`ChatToModel`) is not faked here on purpose: a fake
// would test the fake. What the call *returns* is injected as text, which is the
// whole contract between the model and this layer.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestNewReportAssuranceEvaluatorPicksByRun(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		reviewModel string
		wantMethod  string
		wantModelID string
	}{
		{
			name:        "citation_audit never calls a model even with one configured",
			mode:        model.ReportAssuranceModeCitationAudit,
			reviewModel: "Qwen/Qwen3.8-27B-FP8@ainet@openai",
			wantMethod:  model.ReportEvaluationMethodRule,
			wantModelID: "",
		},
		{
			name:       "no reviewer configured falls back to the rule layer (tier_0, not a failure)",
			mode:       model.ReportAssuranceModeSemantic,
			wantMethod: model.ReportEvaluationMethodRule,
		},
		{
			name:        "a reference with no resolvable instance runs on the engine route",
			mode:        model.ReportAssuranceModeSemantic,
			reviewModel: "Qwen/Qwen3.8-27B-FP8",
			wantMethod:  model.ReportEvaluationMethodSemantic,
			// The engine route never addresses a named model (RAGFlow v1.0.0 ignores
			// `model`), so the row may say a model judged but not *which* one
			// (doc/304 E142). Naming the reference here would describe a call that
			// did not happen — and the live drill produced exactly that row.
			wantModelID: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Only the fields the selector reads are set: this test is about the
			// decision, and a Service would drag the database in.
			var svc Service
			evaluator := svc.newReportAssuranceEvaluator(context.Background(), &model.ReportAssuranceRun{
				Mode: tc.mode, ReviewModelID: tc.reviewModel,
			})
			if got := evaluator.Method(); got != tc.wantMethod {
				t.Fatalf("method = %q, want %q", got, tc.wantMethod)
			}
			if got := evaluator.ModelID(); got != tc.wantModelID {
				t.Fatalf("model id = %q, want %q", got, tc.wantModelID)
			}
		})
	}
}

// The rule layer's job is to have no opinion. `supported` here would be the
// fabricated verdict §5.1 warns about; `abstained` is the only honest answer.
func TestRuleEvaluatorAbstainsRatherThanAgreeing(t *testing.T) {
	var evaluator reportAssuranceRuleEvaluator
	outcome, err := evaluator.Evaluate(t.Context(), reportAssuranceEvalInput{})
	if err != nil {
		t.Fatalf("rule evaluator returned an error: %v", err)
	}
	if outcome.Verdict != model.ReportVerdictAbstained {
		t.Fatalf("verdict = %q, want %q", outcome.Verdict, model.ReportVerdictAbstained)
	}
	if !strings.Contains(outcome.Rationale, "no review model") {
		t.Fatalf("rationale does not say why it abstained: %q", outcome.Rationale)
	}
	if evaluator.Independence() != model.ReportIndependenceUnknown {
		t.Fatalf("independence = %q, want %q", evaluator.Independence(), model.ReportIndependenceUnknown)
	}
}

func TestParseReportAssuranceAnswerClosedSet(t *testing.T) {
	// Every verdict a model is allowed to return survives, so a future prompt
	// that starts asking for one of them cannot be silently dropped here.
	for _, verdict := range []string{
		model.ReportVerdictSupported, model.ReportVerdictPartiallySupported,
		model.ReportVerdictUnsupported, model.ReportVerdictConflicting,
		model.ReportVerdictInsufficient,
	} {
		answer := `{"verdict":"` + verdict + `","relevance":"on_topic","confidence":0.8,` +
			`"rationale":"证据原文写的是 3 天内退款","quote":"3 天内退款"}`
		outcome := parseReportAssuranceAnswer(answer)
		if outcome.Verdict != verdict {
			t.Fatalf("verdict %q parsed as %q", verdict, outcome.Verdict)
		}
		if outcome.Relevance != model.ReportRelevanceOnTopic {
			t.Fatalf("relevance for %q = %q", verdict, outcome.Relevance)
		}
		if outcome.Confidence == nil || *outcome.Confidence != 0.8 {
			t.Fatalf("confidence for %q = %v, want 0.8", verdict, outcome.Confidence)
		}
		if outcome.EvidenceQuote != "3 天内退款" {
			t.Fatalf("evidence quote for %q = %q", verdict, outcome.EvidenceQuote)
		}
	}
}

// A model that answers with a policy verdict is answering a question it was not
// asked. `not_required`/`not_evaluated`/`outdated` are decisions of the rule layer,
// so the parser must refuse them by name instead of storing them.
func TestParseReportAssuranceAnswerRefusesPolicyVerdicts(t *testing.T) {
	for _, verdict := range []string{
		model.ReportVerdictNotRequired, model.ReportVerdictNotEvaluated,
		model.ReportVerdictOutdated, model.ReportVerdictAbstained, "SUPPORTED",
	} {
		answer := `{"verdict":"` + verdict + `","rationale":"x"}`
		outcome := parseReportAssuranceAnswer(answer)
		if outcome.Verdict != model.ReportVerdictAbstained {
			t.Fatalf("verdict %q was accepted as %q", verdict, outcome.Verdict)
		}
		if !strings.Contains(outcome.Rationale, "closed set") {
			t.Fatalf("verdict %q abstained without naming the closed set: %q", verdict, outcome.Rationale)
		}
	}
}

func TestParseReportAssuranceAnswerUnparsable(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		reason string
	}{
		{"no json at all", "抱歉，我无法判断。", "parsable JSON"},
		{"truncated object", `{"verdict":"supported"`, "parsable JSON"},
		// `json.Unmarshal` accepts unknown fields silently, so a well-formed object
		// that answers a different question is caught by the missing verdict, not by
		// the decoder. The two failures read differently to an operator on purpose.
		{"object without a verdict field", `{"answer":"supported"}`, "no verdict field"},
		{"verdict of the wrong type", `{"verdict":12}`, "match the contract"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := parseReportAssuranceAnswer(tc.answer)
			if outcome.Verdict != model.ReportVerdictAbstained {
				t.Fatalf("verdict = %q, want abstained", outcome.Verdict)
			}
			if !strings.Contains(outcome.Rationale, tc.reason) {
				t.Fatalf("rationale = %q, want it to mention %q", outcome.Rationale, tc.reason)
			}
			// An unparsable answer must not smuggle a quote through: the quote is
			// only meaningful next to a verdict that was actually understood.
			if outcome.EvidenceQuote != "" {
				t.Fatalf("unparsable answer produced an evidence quote %q", outcome.EvidenceQuote)
			}
		})
	}
}

// Wrapping is tolerated (models like code fences and arrays), content is not
// negotiated. `extractAssuranceJSON` returns the outermost object, so an answer
// wrapped in `[...]` is the same class of leniency as a code fence.
func TestParseReportAssuranceAnswerAcceptsWrapper(t *testing.T) {
	payload := `{"verdict":"unsupported","relevance":"off_topic","confidence":0.31,"rationale":"引文与断言无关"}`
	cases := []struct {
		name   string
		answer string
	}{
		{"code fence", "```json\n" + payload + "\n```"},
		{"json array", "[" + payload + "]"},
		{"prose before and after", "我判断如下：" + payload + " 以上。"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := parseReportAssuranceAnswer(tc.answer)
			if outcome.Verdict != model.ReportVerdictUnsupported {
				t.Fatalf("verdict = %q, want unsupported", outcome.Verdict)
			}
			if outcome.Relevance != model.ReportRelevanceOffTopic {
				t.Fatalf("relevance = %q, want off_topic", outcome.Relevance)
			}
		})
	}
}

func TestParseReportAssuranceAnswerClampsAndFilters(t *testing.T) {
	answer := `{"verdict":"supported","relevance":"mostly","confidence":1.7,"rationale":"  ok  ","quote":"  x  "}`
	outcome := parseReportAssuranceAnswer(answer)
	if outcome.Relevance != "" {
		t.Fatalf("relevance outside the closed set should be dropped, got %q", outcome.Relevance)
	}
	if outcome.Confidence == nil || *outcome.Confidence != 1 {
		t.Fatalf("confidence should clamp to 1, got %v", outcome.Confidence)
	}
	if outcome.Rationale != "ok" || outcome.EvidenceQuote != "x" {
		t.Fatalf("text fields not trimmed: rationale=%q quote=%q", outcome.Rationale, outcome.EvidenceQuote)
	}

	negative := parseReportAssuranceAnswer(`{"verdict":"supported","confidence":-0.4}`)
	if negative.Confidence == nil || *negative.Confidence != 0 {
		t.Fatalf("confidence should clamp to 0, got %v", negative.Confidence)
	}
	// Absent confidence stays absent: "0.0" and "the model did not say" are
	// different statements and the dashboard prints them differently.
	absent := parseReportAssuranceAnswer(`{"verdict":"supported"}`)
	if absent.Confidence != nil {
		t.Fatalf("absent confidence became %v", *absent.Confidence)
	}
}

func TestExtractAssuranceJSON(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   string
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"with prose around it", `答案如下：{"a":1} 以上。`, `{"a":1}`},
		{"brace inside a string", `{"quote":"退款 {不含} 运费"}`, `{"quote":"退款 {不含} 运费"}`},
		{"escaped quote inside a string", `{"quote":"说 \"可以\" 了"}`, `{"quote":"说 \"可以\" 了"}`},
		{"nested object", `{"a":{"b":2}}`, `{"a":{"b":2}}`},
		{"unbalanced", `{"a":1`, ``},
		{"none", `没有 JSON`, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractAssuranceJSON(tc.answer); got != tc.want {
				t.Fatalf("extract = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildReportAssurancePrompt(t *testing.T) {
	in := reportAssuranceEvalInput{
		Section: model.ReportSection{Title: "退款政策"},
		Claim:   model.ReportClaim{ClaimText: "退款需在 7 天内申请"},
		Quote:   "客户应在收货后 3 天内申请退款。",
	}
	prompt := buildReportAssurancePrompt(in)
	for _, want := range []string{
		reportAssuranceSystemPrompt, "【章节】退款政策", "【报告断言】退款需在 7 天内申请",
		"【证据原文】客户应在收货后 3 天内申请退款。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q", want)
		}
	}
	// The candidate sentence changes what `insufficient_evidence` means, so it
	// must appear exactly when the pool was widened — and never when it was not.
	if strings.Contains(prompt, "冻结了") {
		t.Fatalf("prompt mentions candidates although none were scanned")
	}
	widened := buildReportAssurancePrompt(reportAssuranceEvalInput{
		Section: in.Section, Claim: in.Claim, Quote: in.Quote, CandidateCount: 4,
	})
	if !strings.Contains(widened, "冻结了 4 条") {
		t.Fatalf("widened prompt does not tell the model the pool was widened: %q", widened)
	}
	// The claim/quote must not be able to imitate the template: the template is
	// hashed on its own, and a claim containing "【证据原文】" would otherwise
	// look like a second section to a reader of the prompt.
	if buildReportAssurancePrompt(reportAssuranceEvalInput{
		Section: in.Section, Claim: model.ReportClaim{ClaimText: "【证据原文】x"}, Quote: in.Quote,
	}) == prompt {
		t.Fatalf("two different claims produced the same prompt")
	}
}

// `prompt_hash` is the cache key's template half. If it ever stopped depending on
// the template, bumping `reportAssurancePromptVersion` would silently keep serving
// verdicts obtained under the old question (doc/301 §6.1).
func TestReportAssurancePromptHashPinsTemplate(t *testing.T) {
	first := reportAssurancePromptHash()
	if len(first) != 64 {
		t.Fatalf("prompt hash should be a sha256 hex string, got %q", first)
	}
	if first != reportAssurancePromptHash() {
		t.Fatalf("prompt hash is not stable across calls")
	}
	digest := sha256.Sum256([]byte(reportAssurancePromptVersion + "\x00" + reportAssuranceSystemPrompt))
	sum := hex.EncodeToString(digest[:])
	if first != sum {
		t.Fatalf("prompt hash is not sha256(version \\x00 template): %q != %q", first, sum)
	}
	// The hash must not silently follow the *filled* prompt: the claim and quote
	// are keyed separately, and hashing them twice would only widen collisions.
	if reportAssurancePromptHash() != first {
		t.Fatalf("prompt hash changed while the template did not")
	}
}

func TestReportReviewModelOfIndependence(t *testing.T) {
	generation := ReportModelRef{
		ModelID: "Qwen/Qwen3.5-122B-A10B-FP8", Name: "Qwen/Qwen3.5-122B-A10B-FP8",
		Provider: "ragflow", Instance: "default",
	}
	cases := []struct {
		name      string
		policy    AssurancePolicyResolution
		wantModel string
		wantIndep string
		wantProv  string
		wantInst  string
	}{
		{
			name:      "nothing configured is unknown, not independent",
			policy:    AssurancePolicyResolution{},
			wantIndep: model.ReportIndependenceUnknown,
		},
		{
			name:      "falling back to the generation model is same_model",
			policy:    AssurancePolicyResolution{Model: generation.ModelID},
			wantModel: generation.ModelID,
			wantIndep: model.ReportIndependenceSameModel,
		},
		{
			name:      "review model equal to the generator is same_model",
			policy:    AssurancePolicyResolution{ReviewModel: generation.ModelID, Model: generation.ModelID},
			wantModel: generation.ModelID,
			wantIndep: model.ReportIndependenceSameModel,
		},
		{
			name:      "a different provider is the strongest claim",
			policy:    AssurancePolicyResolution{ReviewModel: "Qwen/Qwen3.8-27B-FP8@ainet@openai"},
			wantModel: "Qwen/Qwen3.8-27B-FP8@ainet@openai",
			wantIndep: model.ReportIndependenceSeparateProvider,
			wantProv:  "openai",
			wantInst:  "ainet",
		},
		{
			name:      "the same provider is only same_family",
			policy:    AssurancePolicyResolution{ReviewModel: "Qwen/Qwen3.8-27B-FP8@pool@ragflow"},
			wantIndep: model.ReportIndependenceSameFamily,
			wantProv:  "ragflow",
			wantInst:  "pool",
		},
		{
			name:      "a reference without a provider cannot claim independence",
			policy:    AssurancePolicyResolution{ReviewModel: "Qwen/Qwen3.8-27B-FP8"},
			wantModel: "Qwen/Qwen3.8-27B-FP8",
			wantIndep: model.ReportIndependenceUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, independence := reportReviewModelOf(tc.policy, generation)
			if independence != tc.wantIndep {
				t.Fatalf("independence = %q, want %q", independence, tc.wantIndep)
			}
			if tc.wantModel != "" && ref.ModelID != tc.wantModel {
				t.Fatalf("model id = %q, want %q", ref.ModelID, tc.wantModel)
			}
			if tc.wantProv != "" && ref.Provider != tc.wantProv {
				t.Fatalf("provider = %q, want %q", ref.Provider, tc.wantProv)
			}
			if tc.wantInst != "" && ref.Instance != tc.wantInst {
				t.Fatalf("instance = %q, want %q", ref.Instance, tc.wantInst)
			}
		})
	}
	// The name half of `Name@Instance@Provider` is kept: an operator reading the
	// receipt needs to see which model was asked, not only which endpoint.
	ref, _ := reportReviewModelOf(
		AssurancePolicyResolution{ReviewModel: "Qwen/Qwen3.8-27B-FP8@ainet@openai"}, generation)
	if ref.Name != "Qwen/Qwen3.8-27B-FP8" {
		t.Fatalf("name = %q, want Qwen/Qwen3.8-27B-FP8", ref.Name)
	}
}

func TestEstimateAssuranceTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int64
	}{
		{"empty", "", 0},
		{"one ascii char still costs one token", "a", 1},
		{"cjk is denser than ascii", "退款政策", 3},                              // 4/1.6=2.5 -> 2+1
		{"sixteen ascii chars are about four tokens", "abcdefghijklmnop", 5}, // 16/4=4 -> 4+1
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimateAssuranceTokens(tc.text); got != tc.want {
				t.Fatalf("tokens(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
	// The estimate is monotone in length — the property the budget check relies on.
	if estimateAssuranceTokens("退款") >= estimateAssuranceTokens(strings.Repeat("退款", 10)) {
		t.Fatalf("token estimate is not monotone")
	}
}
