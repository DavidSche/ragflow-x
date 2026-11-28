package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// assuranceCacheProbeStore is a cache with exactly the one method the verdict cache
// uses. Everything else on `repository.Store` is left nil on purpose: if
// `evaluateReportClaim` ever starts reading through this seam, the nil panic names
// the new dependency instead of quietly changing what this test measures.
type assuranceCacheProbeStore struct {
	repository.Store
	mu    sync.Mutex
	rows  map[string]*model.ReportClaimEvaluation
	reads []string
}

func (s *assuranceCacheProbeStore) FindCachedEvaluation(
	_ context.Context, _ string, cacheKey string,
) (*model.ReportClaimEvaluation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, cacheKey)
	if row, ok := s.rows[cacheKey]; ok {
		return row, nil
	}
	return nil, nil
}

func (s *assuranceCacheProbeStore) keysRead() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reads...)
}

func (s *assuranceCacheProbeStore) remember(key string, row *model.ReportClaimEvaluation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]*model.ReportClaimEvaluation{}
	}
	s.rows[key] = row
}

// assurancePromptProbeEvaluator records the prompt it was actually asked, so the
// test can show that the two runs were two different questions rather than merely
// two different hashes.
type assurancePromptProbeEvaluator struct {
	mu      sync.Mutex
	prompts []string
}

func (e *assurancePromptProbeEvaluator) Evaluate(
	_ context.Context, in reportAssuranceEvalInput,
) (reportAssuranceEvalOutcome, error) {
	e.mu.Lock()
	e.prompts = append(e.prompts, buildReportAssurancePrompt(in))
	e.mu.Unlock()
	return reportAssuranceEvalOutcome{
		Verdict: model.ReportVerdictSupported, Relevance: "on_topic",
		Rationale: "probe verdict", EvidenceQuote: in.Quote,
	}, nil
}

func (e *assurancePromptProbeEvaluator) ModelID() string    { return "probe-review-model" }
func (e *assurancePromptProbeEvaluator) PromptHash() string { return reportAssurancePromptHash() }
func (e *assurancePromptProbeEvaluator) Method() string {
	return model.ReportEvaluationMethodSemantic
}
func (e *assurancePromptProbeEvaluator) Independence() string { return "independent" }

func (e *assurancePromptProbeEvaluator) asked() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.prompts...)
}

// TestReportAssuranceCacheKeyCoversSectionTitle is doc/319 R-04, and it is written
// against the cache *reader*, not the key function.
//
// A test that only compares two key strings would pass on an implementation that
// hashes the title and then forgets to use the hash — and it would tell us nothing
// about the failure the reviewer actually sees: rename a section, re-run, and get
// verdicts that were reached while the old title was in 【章节】.
//
// So this walks the real path three times through `evaluateReportClaim`:
//
//  1. cold: a miss, the model is asked once;
//  2. same question again: a hit, and the model is *not* asked again — this is what
//     makes step 3 meaningful, because it proves the cache is genuinely consulted;
//  3. only the section title changed: a miss, the model is asked a second time, and
//     the prompt it sees carries the new title.
func TestReportAssuranceCacheKeyCoversSectionTitle(t *testing.T) {
	ctx := context.Background()
	store := &assuranceCacheProbeStore{}
	evaluator := &assurancePromptProbeEvaluator{}
	svc := &Service{Store: store}

	run := &model.ReportAssuranceRun{
		ID: "run-1", TenantID: "tenant-1", RevisionID: "rev-1",
		MethodVersion: "v1", RequestedPolicy: "standard", PolicyJSON: "{}",
	}
	claim := model.ReportClaim{
		ID: "claim-1", SectionID: "section-1", SectionOrd: 1,
		ClaimText: "退款须在 7 个工作日内完成。", ClaimType: "fact",
	}
	// Same SectionKey, different Title: exactly the shape of a reviewer renaming a
	// section after an audit has already run.
	section := model.ReportSection{
		ID: "section-1", SectionKey: "refund-policy", Title: "旧标题：退款流程",
		RequiresEvidence: true,
	}
	check := reportQuoteCheck{Status: reportQuoteVerified, MatchedText: "退款须在 7 个工作日内完成。"}
	plan := &reportAssurancePlan{Candidates: 0}

	first, _, _, firstHit, _, err := svc.evaluateReportClaim(ctx, evaluator, run, claim, section, plan, check, "snap-1")
	if err != nil {
		t.Fatalf("first evaluation: %v", err)
	}
	if firstHit {
		t.Fatal("the first evaluation cannot be a cache hit")
	}
	if first.Verdict != model.ReportVerdictSupported {
		t.Fatalf("first verdict = %s, want %s", first.Verdict, model.ReportVerdictSupported)
	}
	if len(evaluator.asked()) != 1 {
		t.Fatalf("model calls after the cold run = %d, want 1", len(evaluator.asked()))
	}

	// Stand in for the row the pipeline writes after a real call.
	coldKey := store.keysRead()[0]
	store.remember(coldKey, &model.ReportClaimEvaluation{
		ID: "eval-cached", SupportVerdict: model.ReportVerdictPartiallySupported, Relevance: "on_topic",
		Rationale: "cached under the old title",
	})

	second, _, _, secondHit, secondSource, err := svc.evaluateReportClaim(ctx, evaluator, run, claim, section, plan, check, "snap-1")
	if err != nil {
		t.Fatalf("repeat evaluation: %v", err)
	}
	if !secondHit || secondSource == nil {
		t.Fatal("the same question must be a cache hit: without this, the test below proves nothing about reuse")
	}
	if second.Verdict != model.ReportVerdictPartiallySupported {
		t.Fatalf("cached verdict = %s, want the stored %s", second.Verdict, model.ReportVerdictPartiallySupported)
	}
	if got := len(evaluator.asked()); got != 1 {
		t.Fatalf("a cache hit must not call the model again; calls = %d", got)
	}

	renamed := section
	renamed.Title = "新标题：退款与开票"
	third, _, _, thirdHit, _, err := svc.evaluateReportClaim(ctx, evaluator, run, claim, renamed, plan, check, "snap-1")
	if err != nil {
		t.Fatalf("renamed evaluation: %v", err)
	}
	if thirdHit {
		t.Fatal("renaming the section reused a verdict reached under the old title (doc/319 R-04)")
	}
	if third.Verdict != model.ReportVerdictSupported {
		t.Fatalf("re-evaluated verdict = %s, want the fresh %s", third.Verdict, model.ReportVerdictSupported)
	}

	keys := store.keysRead()
	if len(keys) != 3 {
		t.Fatalf("cache reads = %d, want 3 (cold, hit, renamed)", len(keys))
	}
	if keys[2] == keys[1] {
		t.Fatal("the renamed section hashed to the cached key: the title is not in the cache key")
	}
	prompts := evaluator.asked()
	if len(prompts) != 2 {
		t.Fatalf("model calls = %d, want 2 (cold + renamed)", len(prompts))
	}
	if !strings.Contains(prompts[0], "旧标题：退款流程") {
		t.Fatal("the cold prompt did not carry the old title: the premise of this test is wrong")
	}
	if !strings.Contains(prompts[1], "新标题：退款与开票") {
		t.Fatal("the re-evaluated prompt did not carry the new title")
	}
	if prompts[0] == prompts[1] {
		t.Fatal("the two prompts are identical, so the title never reached the question")
	}
}

// TestReportAssuranceCacheKeyCoversEveryPromptInput closes the class of defect R-04
// belongs to, instead of fixing one instance of it.
//
// The cache key must cover everything `buildReportAssurancePrompt` renders. That is
// checkable without a model: change one input at a time, render both prompts, and
// require that a changed question always comes with a changed key. A future field
// added to the prompt without a matching key component fails here.
func TestReportAssuranceCacheKeyCoversEveryPromptInput(t *testing.T) {
	base := reportAssuranceEvalInput{
		Claim: model.ReportClaim{ClaimText: "退款须在 7 个工作日内完成。"},
		Section: model.ReportSection{
			SectionKey: "refund-policy", Title: "退款流程",
		},
		Quote: "退款须在 7 个工作日内完成。", CandidateCount: 3,
	}
	keyOf := func(in reportAssuranceEvalInput) string {
		// The production assembly, not a copy of it: this is what makes deleting a
		// component from `reportAssuranceCacheKeyOf` fail here instead of passing
		// against a stale duplicate kept in the test.
		return reportAssuranceCacheKeyOf(&model.ReportAssuranceRun{MethodVersion: "v1", RequestedPolicy: "standard"},
			&assurancePromptProbeEvaluator{}, in.Claim, in.Section, in.Quote, in.CandidateCount)
	}
	basePrompt := buildReportAssurancePrompt(base)
	baseKey := keyOf(base)

	mutations := []struct {
		name   string
		change func(*reportAssuranceEvalInput)
	}{
		{"claim text", func(in *reportAssuranceEvalInput) { in.Claim.ClaimText = "退款须在 3 个工作日内完成。" }},
		{"section title", func(in *reportAssuranceEvalInput) { in.Section.Title = "退款与开票流程" }},
		{"quote", func(in *reportAssuranceEvalInput) { in.Quote = "退款须在 7 个工作日内到账。" }},
		{"candidate count", func(in *reportAssuranceEvalInput) { in.CandidateCount = 12 }},
	}
	for _, mutation := range mutations {
		in := base
		mutation.change(&in)
		if buildReportAssurancePrompt(in) == basePrompt {
			t.Fatalf("%s: the mutation did not change the prompt, so this case proves nothing", mutation.name)
		}
		if keyOf(in) == baseKey {
			t.Fatalf("%s: the prompt changed but the cache key did not — a cached verdict would answer a question nobody asked (doc/319 R-04)",
				mutation.name)
		}
	}
}
