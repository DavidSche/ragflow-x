package service

// Tests for Q-1..Q-5 (doc/301 §9.4) — the review-quality self-check as a function.
//
// The five checks are easy to write in a form that cannot fail, and each of those
// forms is one of the tests below:
//
//   - a share over an empty denominator prints a green 0% (Q-1, Q-3, Q-5);
//   - "identical" is asserted over a replay that never happened (Q-2);
//   - an overlap count that flags a *correct* system (Q-4, see the file header);
//   - a paraphrase that quietly asks for something easier than §9.4 does.
//
// The fixtures start from `passingQualityInput()` and break exactly one row, so a
// failure can never be blamed on a neighbour.

import (
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// passingQualityInput is a window in which every §9.4 property holds. It is shaped
// like the live drill's numbers where they exist (548 rows with a quote status, 2915
// citations, 127 abstentions) so the fixture and doc/305 §2.16 do not drift apart in
// kind, only in size.
func passingQualityInput() QualitySelfCheckInput {
	return QualitySelfCheckInput{
		QuoteStatuses: map[string]int64{
			model.ReportQuoteStatusVerified:    540,
			model.ReportQuoteStatusNotFound:    0,
			model.ReportQuoteStatusAmbiguous:   5,
			model.ReportQuoteStatusNotProvided: 3,
		},
		Replays: []QualitySelfCheckReplay{
			{CacheKey: "aaaaaaaaaaaa", Rows: 2, Variants: []string{"supported|passed|on_topic|verified"}},
			{CacheKey: "bbbbbbbbbbbb", Rows: 3, Variants: []string{"unsupported|passed|on_topic|verified"}},
		},
		Supported:        36,
		SupportedUncited: 0,
		Citations: QualitySelfCheckCitations{
			Columns: []string{"id", "tenant_id", "revision_id", "evidence_snapshot_id",
				"source_chunk_id", "document_label", "used_in_text"},
			Rows:             2915,
			DanglingSnapshot: 0,
		},
		AbstainReasons: map[string]int64{
			model.ReportAbstainNoQuote:        100,
			model.ReportAbstainInputInvalid:   5,
			model.ReportAbstainQuoteNotFound:  15,
			model.ReportAbstainModelUncertain: 20, // 20/140 = 14.3% ≤ 15%
		},
	}
}

func rowOf(result QualitySelfCheckResult, id string) QualitySelfCheckRow {
	for _, row := range result.Rows {
		if row.ID == id {
			return row
		}
	}
	return QualitySelfCheckRow{}
}

func TestQualitySelfCheckPassesOnAHealthyWindow(t *testing.T) {
	result := EvaluateQualitySelfCheck(passingQualityInput())
	if len(result.Rows) != 5 {
		t.Fatalf("§9.4 has five checks, this result has %d", len(result.Rows))
	}
	for _, want := range []string{"Q-1", "Q-2", "Q-3", "Q-4", "Q-5"} {
		if row := rowOf(result, want); row.Verdict != releaseGatePass {
			t.Fatalf("%s must pass on a healthy window, got %q (%s)", want, row.Verdict, row.Detail)
		}
	}
	if result.Verdict() != releaseGatePass {
		t.Fatalf("the whole check must be pass, got %q", result.Verdict())
	}
	if blocking := result.Blocking(); len(blocking) != 0 {
		t.Fatalf("a passing check has nothing blocking, got %d row(s)", len(blocking))
	}
}

func TestEveryQualitySelfCheckRowCanFail(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*QualitySelfCheckInput)
		want   string
		// contains is a fragment the failure must name, because a red row that does
		// not say what it saw is a row nobody can act on.
		contains string
	}{
		{
			name:     "Q-1 a quote whose text is not in the snapshot",
			mutate:   func(in *QualitySelfCheckInput) { in.QuoteStatuses[model.ReportQuoteStatusNotFound] = 3 },
			want:     "Q-1",
			contains: "`not_found` = 3",
		},
		{
			name: "Q-2 two answers under one verdict input set",
			mutate: func(in *QualitySelfCheckInput) {
				in.Replays[0].Variants = append(in.Replays[0].Variants, "unsupported|passed|on_topic|verified")
			},
			want:     "Q-2",
			contains: "1 of 2 replay(s) differ",
		},
		{
			name:     "Q-3 a supported verdict with no source",
			mutate:   func(in *QualitySelfCheckInput) { in.SupportedUncited = 2 },
			want:     "Q-3",
			contains: "2 supported row(s) carry no evidence id",
		},
		{
			name: "Q-4 a citation table that can name a candidate",
			mutate: func(in *QualitySelfCheckInput) {
				in.Citations.Columns = append(in.Citations.Columns, "conflict_ref")
			},
			want:     "Q-4",
			contains: "conflict_ref",
		},
		{
			name:     "Q-4 a citation whose snapshot does not exist",
			mutate:   func(in *QualitySelfCheckInput) { in.Citations.DanglingSnapshot = 1 },
			want:     "Q-4",
			contains: "dangling",
		},
		{
			name:     "Q-5 the model hedging past the ceiling",
			mutate:   func(in *QualitySelfCheckInput) { in.AbstainReasons[model.ReportAbstainModelUncertain] = 40 },
			want:     "Q-5",
			contains: "40 of 160",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := passingQualityInput()
			tc.mutate(&in)
			result := EvaluateQualitySelfCheck(in)
			for _, row := range result.Rows {
				if row.ID != tc.want {
					if row.Verdict != releaseGatePass {
						// A mutation must not turn a neighbour red: that would make the
						// red row unattributable.
						t.Fatalf("%s went red (%s) while testing %s", row.ID, row.Verdict, tc.want)
					}
					continue
				}
				if row.Verdict != releaseGateFail {
					t.Fatalf("%s must fail, got %q", tc.want, row.Verdict)
				}
				if !strings.Contains(row.Detail, tc.contains) {
					t.Fatalf("%s must name %q, got %q", tc.want, tc.contains, row.Detail)
				}
			}
			if result.Verdict() != releaseGateFail {
				t.Fatalf("a failing row must make the whole check fail, got %q", result.Verdict())
			}
		})
	}
}

// TestEveryQualitySelfCheckRowSaysUnverifiedWithoutADenominator is rule 1. Each empty
// input is a window in which the check *could not look*, and §9.4's heading says the
// check runs in CI precisely so nobody has to accept "it probably held".
func TestEveryQualitySelfCheckRowSaysUnverifiedWithoutADenominator(t *testing.T) {
	empties := []struct {
		name   string
		in     QualitySelfCheckInput
		want   string
		detail string
	}{
		{
			name:   "no quote status at all",
			in:     QualitySelfCheckInput{},
			want:   "Q-1",
			detail: "nothing was checked",
		},
		{
			name:   "every replay has one row only",
			in:     QualitySelfCheckInput{Replays: []QualitySelfCheckReplay{{CacheKey: "k", Rows: 1, Variants: []string{"supported|passed|on_topic|verified"}}}},
			want:   "Q-2",
			detail: "was never tested",
		},
		{
			name:   "no supported row exists",
			in:     QualitySelfCheckInput{Supported: 0},
			want:   "Q-3",
			detail: "undefined",
		},
		{
			name:   "the table shape was not read",
			in:     QualitySelfCheckInput{},
			want:   "Q-4",
			detail: "cannot say whether a candidate reference is even",
		},
		{
			name:   "the shape is right but there is no citation",
			in:     QualitySelfCheckInput{Citations: QualitySelfCheckCitations{Columns: []string{"id", "evidence_snapshot_id"}}},
			want:   "Q-4",
			detail: "was not tested",
		},
		{
			name:   "no abstention to attribute",
			in:     QualitySelfCheckInput{},
			want:   "Q-5",
			detail: "undefined",
		},
	}
	for _, tc := range empties {
		t.Run(tc.name, func(t *testing.T) {
			result := EvaluateQualitySelfCheck(tc.in)
			found := false
			for _, row := range result.Rows {
				if row.Verdict == releaseGatePass {
					t.Fatalf("%s passed on an input with no denominator: %q", row.ID, row.Observed)
				}
				if row.ID == tc.want {
					found = true
					if row.Verdict != releaseGateUnverified {
						t.Fatalf("%s must be unverified, got %q", tc.want, row.Verdict)
					}
					if !strings.Contains(row.Detail, tc.detail) {
						t.Fatalf("%s must say %q, got %q", tc.want, tc.detail, row.Detail)
					}
				}
			}
			if !found {
				t.Fatalf("%s is missing from the result", tc.want)
			}
			if result.Verdict() != releaseGateUnverified {
				t.Fatalf("the whole check must be unverified (never pass), got %q", result.Verdict())
			}
			if len(result.Blocking()) != 5 {
				t.Fatalf("unverified rows must block: got %d blocking row(s)", len(result.Blocking()))
			}
		})
	}
}

func TestQualitySelfCheckRefusesAWordOutsideTheClosedSet(t *testing.T) {
	in := passingQualityInput()
	in.QuoteStatuses["unclear"] = 1
	in.AbstainReasons["model_slow"] = 1
	result := EvaluateQualitySelfCheck(in)
	for _, id := range []string{"Q-1", "Q-5"} {
		row := rowOf(result, id)
		if row.Verdict != releaseGateFail {
			t.Fatalf("%s must fail on an unknown word, got %q", id, row.Verdict)
		}
	}
	if !strings.Contains(rowOf(result, "Q-1").Observed, "unclear") {
		t.Fatalf("Q-1 must name the word it could not count: %q", rowOf(result, "Q-1").Observed)
	}
	if !strings.Contains(rowOf(result, "Q-5").Observed, "model_slow") {
		t.Fatalf("Q-5 must name the word it could not count: %q", rowOf(result, "Q-5").Observed)
	}
}

// TestTheAbstainCeilingIsFifteenPercentOfAbstentions pins both halves of Q-5: the
// denominator is abstentions (§9.4), not claims (§4.2), and 15% exactly is inside the
// ceiling — a threshold that fails at its own stated value is a different rule.
func TestTheAbstainCeilingIsFifteenPercentOfAbstentions(t *testing.T) {
	in := passingQualityInput()
	in.AbstainReasons = map[string]int64{
		model.ReportAbstainNoQuote:        17,
		model.ReportAbstainModelUncertain: 3, // 3/20 = exactly 15%
	}
	if row := rowOf(EvaluateQualitySelfCheck(in), "Q-5"); row.Verdict != releaseGatePass {
		t.Fatalf("15%% must be inside §9.4's ceiling, got %q (%s)", row.Verdict, row.Detail)
	}
	in.AbstainReasons = map[string]int64{
		model.ReportAbstainNoQuote:        16,
		model.ReportAbstainModelUncertain: 4, // 4/20 = 20%
	}
	row := rowOf(EvaluateQualitySelfCheck(in), "Q-5")
	if row.Verdict != releaseGateFail {
		t.Fatalf("20%% must be outside the ceiling, got %q", row.Verdict)
	}
	if !strings.Contains(row.Detail, "4 of 20") {
		t.Fatalf("the failure must print its denominator, got %q", row.Detail)
	}
}

// TestEveryNonPassingRowRepeatsItsNumber pins the prefix rule added in
// EvaluateQualitySelfCheck: a red or unverified row carries its own observation, so
// the line in the log can be read without the table above it.
func TestEveryNonPassingRowRepeatsItsNumber(t *testing.T) {
	inputs := []QualitySelfCheckInput{
		{},
		{Citations: QualitySelfCheckCitations{Columns: []string{"conflict_ref"}, Rows: 3}},
		{Supported: 4, SupportedUncited: 1},
	}
	for _, in := range inputs {
		for _, row := range EvaluateQualitySelfCheck(in).Rows {
			if row.Verdict == releaseGatePass {
				continue
			}
			if !strings.HasPrefix(row.Detail, row.Observed) {
				t.Fatalf("%s is %s but its detail does not repeat %q: %q", row.ID, row.Verdict, row.Observed, row.Detail)
			}
			if !strings.Contains(row.Detail, row.Sample) {
				t.Fatalf("%s's detail lost its sample %q: %q", row.ID, row.Sample, row.Detail)
			}
		}
	}
}

// TestQualitySelfCheckHashesTheWholeTuple: §9.4 says "判定 hash", and the hash covers
// structure, relevance and quote_status as well as the verdict. Comparing verdicts
// alone would call two different judgments identical.
func TestQualitySelfCheckHashesTheWholeTuple(t *testing.T) {
	in := passingQualityInput()
	in.Replays = []QualitySelfCheckReplay{{
		CacheKey: "cccccccccccc",
		Rows:     2,
		Variants: []string{
			"supported|passed|on_topic|verified",
			"supported|passed|on_topic|ambiguous", // same verdict, different quote_status
		},
	}}
	row := rowOf(EvaluateQualitySelfCheck(in), "Q-2")
	if row.Verdict != releaseGateFail {
		t.Fatalf("a differing quote_status is a differing judgment, got %q", row.Verdict)
	}
	if !strings.Contains(row.Detail, "ambiguous") {
		t.Fatalf("the finding must name the tuple it saw, got %q", row.Detail)
	}
}

func TestQualitySelfCheckCitationColumnsMatchByShape(t *testing.T) {
	for _, forbidden := range []string{"conflict_ref", "candidate_id", "conflict_candidate_uuid", "pool_rank", "candidateRef"} {
		in := passingQualityInput()
		in.Citations.Columns = append(in.Citations.Columns, forbidden)
		if row := rowOf(EvaluateQualitySelfCheck(in), "Q-4"); row.Verdict != releaseGateFail {
			t.Fatalf("column %q must be refused, got %q", forbidden, row.Verdict)
		}
	}
	in := passingQualityInput()
	in.Citations.Columns = []string{"id", "evidence_snapshot_id", "source_chunk_id", "used_in_text"}
	if row := rowOf(EvaluateQualitySelfCheck(in), "Q-4"); row.Verdict != releaseGatePass {
		t.Fatalf("an ordinary citation table must pass, got %q (%s)", row.Verdict, row.Detail)
	}
}

// TestTheQualitySelfCheckPrintsEveryRowWithItsSample is what makes the CI log
// trustworthy rather than decorative: every row prints `n=`, and a row with `n=0` is
// never printed as a pass.
func TestTheQualitySelfCheckPrintsEveryRowWithItsSample(t *testing.T) {
	for _, in := range []QualitySelfCheckInput{passingQualityInput(), {}} {
		text := RenderQualitySelfCheckMarkdown(EvaluateQualitySelfCheck(in))
		for _, id := range []string{"Q-1", "Q-2", "Q-3", "Q-4", "Q-5"} {
			if !strings.Contains(text, "| "+id+" |") {
				t.Fatalf("row %s is missing from the log output:\n%s", id, text)
			}
		}
		for _, row := range EvaluateQualitySelfCheck(in).Rows {
			if row.Sample == "" {
				t.Fatalf("%s printed no sample", row.ID)
			}
			if strings.Contains(row.Sample, "n=0") && row.Verdict == releaseGatePass {
				t.Fatalf("%s printed a pass over n=0", row.ID)
			}
			if !strings.Contains(text, row.Sample) {
				t.Fatalf("%s's sample %q never reached the log", row.ID, row.Sample)
			}
		}
	}
}

// TestTheQualitySelfCheckPrintsTheTableForTheGateScript is the machine-readable half:
// `scripts/release-gate/assurance-quality.sh` reads these lines, so a row deleted from
// the evaluator fails here first.
func TestTheQualitySelfCheckPrintsTheTableForTheGateScript(t *testing.T) {
	result := EvaluateQualitySelfCheck(passingQualityInput())
	if len(result.Rows) != 5 {
		t.Fatalf("the script parses exactly five quality-row lines, got %d", len(result.Rows))
	}
	for _, row := range result.Rows {
		t.Logf("quality-row: id=%s verdict=%s sample=%s observed=%s", row.ID, row.Verdict, row.Sample, row.Observed)
		t.Logf("quality-expectation: id=%s check=%s expectation=%s", row.ID, row.Check, row.Expectation)
	}
	t.Logf("quality-ceiling: abstain_model_uncertain=%.2f", qualitySelfCheckUncertainCeiling)
}
