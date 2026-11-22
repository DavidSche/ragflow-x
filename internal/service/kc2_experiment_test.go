package service

// Tests for doc/301 §5.7's KC-2 decision rule (task T11).
//
// The rule is three conditions and a sample-size plan, and every wrong version of it is
// one of the tests below:
//
//   - a rule that only prints numbers and never says no (no "refused" path);
//   - a rule that turns "12 sections" into a verdict in either direction — §9.2's E2
//     precedent says 22/24 is "defer to a bigger sample", not a negative result;
//   - a rule that lets a "treatment" arm with zero locator hints be compared with the
//     control — that is the same retrieval run twice with two labels;
//   - a rule that compares a lift at the boundary in float64, where 7/10 − 6/10 is
//     9.999999999999998pp and a lift of exactly the pre-registered floor fails.
//
// The last test prints the four rows in the shape `scripts/release-gate/kc2-prereg.sh`
// reads, so the gate's verbatim check cannot drift from what the function judges with.

import (
	"fmt"
	"strings"
	"testing"
)

// kc2Fixture builds a paired sample: n sections per arm, the first `controlCovered`
// covered in the control arm and the first `treatmentCovered` in the treatment arm.
// Tokens are 1000/section (control) and 1200/section (treatment), i.e. +20%: inside the
// 30% ceiling, so a test that wants a cost failure has to say so.
func kc2Fixture(n, controlCovered, treatmentCovered int) []KC2Section {
	out := make([]KC2Section, 0, 2*n)
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("sec-%02d", i)
		out = append(out,
			KC2Section{SectionKey: key, Ord: i, Arm: KC2ArmControl,
				Covered: i <= controlCovered, Citations: 2, Traceable: 2,
				Tokens: 1000, LatencyMS: 100},
			KC2Section{SectionKey: key, Ord: i, Arm: KC2ArmTreatment,
				Covered: i <= treatmentCovered, Citations: 2, Traceable: 2,
				Hints: 1, HintUsed: i <= treatmentCovered, Tokens: 1200, LatencyMS: 110})
	}
	return out
}

func kc2Mutate(in []KC2Section, fn func(*KC2Section)) []KC2Section {
	out := make([]KC2Section, len(in))
	copy(out, in)
	for i := range out {
		fn(&out[i])
	}
	return out
}

func kc2RowByID(t *testing.T, r KC2Result, id string) KC2Row {
	t.Helper()
	for _, row := range r.Rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("row %q is missing from the report (rows: %d)", id, len(r.Rows))
	return KC2Row{}
}

func kc2Problem(t *testing.T, r KC2Result, prefix string) string {
	t.Helper()
	for _, p := range r.Problems {
		if strings.HasPrefix(p, prefix) {
			return p
		}
	}
	t.Fatalf("problem %q is missing; got %v", prefix, r.Problems)
	return ""
}

// TestKC2ThresholdsAreThePlansLiterals pins the three numbers to doc/301 §5.7's wording
// and the plan quantity to §9.2's 30 sections. A change here is a change to a
// pre-registered rule, which is why the frozen file has to be re-frozen with it.
func TestKC2ThresholdsAreThePlansLiterals(t *testing.T) {
	if KC2LiftFloorPP != 10.0 {
		t.Fatalf("KC2LiftFloorPP = %v, doc/301 §5.7 says lift ≥10pp", KC2LiftFloorPP)
	}
	if KC2TraceabilityFloor != 1.0 {
		t.Fatalf("KC2TraceabilityFloor = %v, doc/301 §5.7 says 回溯 100%%", KC2TraceabilityFloor)
	}
	if KC2CostDeltaCeiling != 0.30 {
		t.Fatalf("KC2CostDeltaCeiling = %v, doc/301 §5.7 says 成本增幅 ≤30%%", KC2CostDeltaCeiling)
	}
	if KC2PlanSectionsPerArm != 30 {
		t.Fatalf("KC2PlanSectionsPerArm = %d, the pre-registration says 30", KC2PlanSectionsPerArm)
	}
	if KC2VerdictAllowed+"|"+KC2VerdictRefused+"|"+KC2VerdictUnverified != "allowed|refused|unverified" {
		t.Fatalf("the three verdict words are the report's vocabulary: %q %q %q",
			KC2VerdictAllowed, KC2VerdictRefused, KC2VerdictUnverified)
	}
}

// TestKC2AllowsOnlyWhenAllThreeConditionsHold is the positive case plus one mutation per
// condition: if any of the three can be broken without the verdict changing, the rule is
// decoration.
func TestKC2AllowsOnlyWhenAllThreeConditionsHold(t *testing.T) {
	base := kc2Fixture(30, 18, 25) // lift +23.3pp, traceability 100%, cost +20%
	ok := EvaluateKC2(base)
	if ok.Verdict != KC2VerdictAllowed {
		t.Fatalf("verdict = %q, want allowed (rows: %+v)", ok.Verdict, ok.Rows)
	}
	if len(ok.Problems) != 0 || len(ok.Gaps) != 0 {
		t.Fatalf("a clean paired sample must have no problems/gaps: %v %v", ok.Problems, ok.Gaps)
	}

	// 1. lift below the floor: 20/30 vs 18/30 is +6.7pp.
	liftFail := EvaluateKC2(kc2Fixture(30, 18, 20))
	if liftFail.Verdict != KC2VerdictRefused {
		t.Fatalf("verdict = %q, want refused when the lift is below 10pp", liftFail.Verdict)
	}
	if row := kc2RowByID(t, liftFail, "evidence_coverage"); row.Verdict != "fail" {
		t.Fatalf("evidence_coverage = %q, want fail (%s)", row.Verdict, row.Observed)
	}

	// 2. one untraceable citation out of 120.
	traceFail := EvaluateKC2(kc2Mutate(base, func(s *KC2Section) {
		if s.Arm == KC2ArmTreatment && s.SectionKey == "sec-03" {
			s.Traceable = 1
		}
	}))
	if traceFail.Verdict != KC2VerdictRefused {
		t.Fatalf("verdict = %q, want refused on one untraceable citation", traceFail.Verdict)
	}
	if row := kc2RowByID(t, traceFail, "raw_traceability"); row.Verdict != "fail" {
		t.Fatalf("raw_traceability = %q, want fail (%s)", row.Verdict, row.Observed)
	}

	// 3. cost +40%: 1400 tokens/section against 1000.
	costFail := EvaluateKC2(kc2Mutate(base, func(s *KC2Section) {
		if s.Arm == KC2ArmTreatment {
			s.Tokens = 1400
		}
	}))
	if costFail.Verdict != KC2VerdictRefused {
		t.Fatalf("verdict = %q, want refused at +40%% cost", costFail.Verdict)
	}
	if row := kc2RowByID(t, costFail, "cost_delta_tokens"); row.Verdict != "fail" {
		t.Fatalf("cost_delta_tokens = %q, want fail (%s)", row.Verdict, row.Observed)
	}
}

// TestKC2ThresholdBoundariesAreExact: the floor is inclusive and decided with integers.
// 7/10 − 6/10 is 9.999999999999998pp in float64; that must still be a 10pp pass.
func TestKC2ThresholdBoundariesAreExact(t *testing.T) {
	if ppm := kc2LiftPPM(6, 10, 7, 10); ppm != 100_000 {
		t.Fatalf("kc2LiftPPM(6/10, 7/10) = %d ppm, want exactly 100000 (the float64 difference is 9.999999999999998pp)", ppm)
	}
	// exactly 10pp on the frozen 30-section plan: 18/30 vs 15/30.
	atFloor := EvaluateKC2(kc2Fixture(30, 15, 18))
	if row := kc2RowByID(t, atFloor, "evidence_coverage"); row.Verdict != "pass" {
		t.Fatalf("exactly +10.0pp must pass, got %q (%s)", row.Verdict, row.Observed)
	}
	// exactly +30.0%: 39000 tokens against 30000.
	atCeiling := EvaluateKC2(kc2Mutate(kc2Fixture(30, 15, 18), func(s *KC2Section) {
		if s.Arm == KC2ArmTreatment {
			s.Tokens = 1300
		}
	}))
	if row := kc2RowByID(t, atCeiling, "cost_delta_tokens"); row.Verdict != "pass" {
		t.Fatalf("exactly +30.0%% must pass, got %q (%s)", row.Verdict, row.Observed)
	}
	// +30.1% must fail.
	overCeiling := EvaluateKC2(kc2Mutate(kc2Fixture(30, 15, 18), func(s *KC2Section) {
		if s.Arm == KC2ArmTreatment {
			s.Tokens = 1301
		}
	}))
	if row := kc2RowByID(t, overCeiling, "cost_delta_tokens"); row.Verdict != "fail" {
		t.Fatalf("+30.1%% must fail, got %q (%s)", row.Verdict, row.Observed)
	}
}

// TestKC2BelowPlanIsUnverifiedNotRefused is §9.2's E2 precedent applied to §5.7: a
// sample below the pre-registered size supports neither answer, in either direction.
func TestKC2BelowPlanIsUnverifiedNotRefused(t *testing.T) {
	// A spectacular lift on 12 sections is still not a result.
	short := EvaluateKC2(kc2Fixture(12, 3, 12))
	if short.Verdict != KC2VerdictUnverified {
		t.Fatalf("verdict = %q, want unverified at n=12 (lift %+.1fpp)", short.Verdict, short.LiftPP)
	}
	if row := kc2RowByID(t, short, "sample_size"); row.Verdict != "unverified" {
		t.Fatalf("sample_size = %q, want unverified", row.Verdict)
	}
	if blocking := short.Blocking(); len(blocking) != 1 || blocking[0].ID != "sample_size" {
		t.Fatalf("the only blocking row may be sample_size, got %+v", blocking)
	}

	// Neither is a negative result on 12 sections: gaps dominate the point estimates.
	negative := EvaluateKC2(kc2Mutate(kc2Fixture(12, 10, 1), func(s *KC2Section) {
		if s.Arm == KC2ArmTreatment {
			s.Tokens = 2000 // +100% cost, and coverage down 8pp
		}
	}))
	if negative.Verdict != KC2VerdictUnverified {
		t.Fatalf("verdict = %q, want unverified: 12 sections cannot establish a failure either", negative.Verdict)
	}
	// The ordering is the claim, so assert the row it outranks: coverage really did fail
	// here, and the verdict is still unverified because the sample was not the registered
	// one. A test that only checked the verdict could pass on a fixture where nothing failed.
	if row := kc2RowByID(t, negative, "evidence_coverage"); row.Verdict != "fail" {
		t.Fatalf("evidence_coverage = %q, want fail, otherwise the ordering is untested", row.Verdict)
	}
}

// TestKC2UntraceableCitationRefusesEvenBelowPlan: 100% traceability is an invariant of
// the evidence chain, not a threshold the sample size can excuse.
func TestKC2UntraceableCitationRefusesEvenBelowPlan(t *testing.T) {
	r := EvaluateKC2(kc2Mutate(kc2Fixture(12, 3, 12), func(s *KC2Section) {
		if s.Arm == KC2ArmControl && s.SectionKey == "sec-01" {
			s.Traceable = 1
		}
	}))
	if r.Verdict != KC2VerdictRefused {
		t.Fatalf("verdict = %q, want refused: a citation that does not resolve to a raw chunk is not a sample-size problem", r.Verdict)
	}
	if row := kc2RowByID(t, r, "raw_traceability"); row.Verdict != "fail" {
		t.Fatalf("raw_traceability = %q, want fail", row.Verdict)
	}
	// Traceability outranks a shape problem too: an input that is not an A/B *and* has an
	// unresolvable citation is still a broken evidence chain, and that is a refusal.
	both := EvaluateKC2(kc2Mutate(kc2Fixture(30, 18, 25), func(s *KC2Section) {
		if s.Arm == KC2ArmTreatment {
			s.Hints = 0
			s.HintUsed = false
		}
		if s.Arm == KC2ArmControl && s.SectionKey == "sec-01" {
			s.Traceable = 0
			s.Covered = false
		}
	}))
	if both.Verdict != KC2VerdictRefused {
		t.Fatalf("verdict = %q, want refused: an untraceable citation is not excused by a shape problem", both.Verdict)
	}
	kc2Problem(t, both, "treatment_without_hints")
}

// TestKC2InputShapeProblemsAreNamed: each malformed input produces its own sentence and
// exactly one problem, so a reader can fix one thing at a time. The pairing check must not
// add "unpaired" noise on top of a record that was already rejected.
func TestKC2InputShapeProblemsAreNamed(t *testing.T) {
	base := kc2Fixture(30, 18, 25)
	cases := []struct {
		name   string
		input  []KC2Section
		prefix string
	}{
		{
			name: "control saw hints",
			input: kc2Mutate(base, func(s *KC2Section) {
				if s.Arm == KC2ArmControl && s.SectionKey == "sec-04" {
					s.Hints = 2
				}
			}),
			prefix: "control_with_hints:sec-04",
		},
		{
			name: "one unpaired section",
			input: func() []KC2Section {
				out := make([]KC2Section, 0, len(base)-1)
				for _, s := range base {
					if s.Arm == KC2ArmTreatment && s.SectionKey == "sec-05" {
						continue
					}
					out = append(out, s)
				}
				return out
			}(),
			prefix: "unpaired_section:sec-05",
		},
		{
			name:   "duplicate section",
			input:  append(append([]KC2Section{}, base...), base[12]),
			prefix: "duplicate_section:control/sec-07",
		},
		{
			name:   "unknown arm",
			input:  append(append([]KC2Section{}, base...), KC2Section{SectionKey: "sec-99", Ord: 99, Arm: "treatment-2"}),
			prefix: `unknown_arm:"treatment-2"`,
		},
		{
			name: "negative count",
			input: kc2Mutate(base, func(s *KC2Section) {
				if s.Arm == KC2ArmControl && s.SectionKey == "sec-02" {
					s.Tokens = -5
				}
			}),
			prefix: "negative_count:control/sec-02",
		},
		{
			name: "covered without a traceable citation",
			input: kc2Mutate(base, func(s *KC2Section) {
				if s.Arm == KC2ArmControl && s.SectionKey == "sec-02" {
					s.Traceable = 0
				}
			}),
			prefix: "covered_without_traceable_citation:control/sec-02",
		},
		{
			name: "more traceable than cited",
			input: kc2Mutate(base, func(s *KC2Section) {
				if s.Arm == KC2ArmControl && s.SectionKey == "sec-02" {
					s.Traceable = 5
				}
			}),
			prefix: "traceable_exceeds_citations:control/sec-02",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := EvaluateKC2(tc.input)
			// A shape problem is "I could not read this experiment", not "the experiment
			// failed": exit 3. Only the traceability invariant is allowed to refuse on
			// unreadable input (proven in TestKC2UntraceableCitationRefusesEvenBelowPlan).
			if r.Verdict != KC2VerdictUnverified {
				t.Fatalf("verdict = %q, want unverified: a broken record means the experiment is unreadable, not failed", r.Verdict)
			}
			if len(r.Problems) != 1 {
				t.Fatalf("want exactly one problem (one broken record, one sentence), got %v", r.Problems)
			}
			if got := kc2Problem(t, r, tc.prefix); !strings.HasPrefix(got, tc.prefix) {
				t.Fatalf("problem = %q, want prefix %q", got, tc.prefix)
			}
		})
	}
}

// TestKC2TreatmentWithoutHintsIsNotAnExperiment is the exact state of the live drill
// (doc/305 §2.17): zero locator hints everywhere. A "lift" measured between two identical
// retrievals is noise, so the input is refused rather than measured.
func TestKC2TreatmentWithoutHintsIsNotAnExperiment(t *testing.T) {
	r := EvaluateKC2(kc2Mutate(kc2Fixture(30, 18, 25), func(s *KC2Section) {
		if s.Arm == KC2ArmTreatment {
			s.Hints = 0
			s.HintUsed = false
		}
	}))
	// Not an experiment is not a negative result: the treatment saw the same retrieval as
	// the control, so §5.7 has not been tested at all. exit 3, not exit 1.
	if r.Verdict != KC2VerdictUnverified {
		t.Fatalf("verdict = %q, want unverified: with no hint the two arms are the same retrieval twice, so there is nothing to refuse yet", r.Verdict)
	}
	kc2Problem(t, r, "treatment_without_hints")
	if obs := r.Observations[0]; obs.ID != "locator_hit_rate" || obs.Value != "—" {
		t.Fatalf("locator_hit_rate must have no value when nothing was hinted, got %+v", obs)
	}
}

// TestKC2EmptyInputIsUnverified: no sample is not a pass, and every one of the four rows
// says so in its own words.
func TestKC2EmptyInputIsUnverified(t *testing.T) {
	r := EvaluateKC2(nil)
	if r.Verdict != KC2VerdictUnverified {
		t.Fatalf("verdict = %q, want unverified on an empty items file", r.Verdict)
	}
	if len(r.Rows) != 4 {
		t.Fatalf("want 4 judging rows, got %d", len(r.Rows))
	}
	if blocking := r.Blocking(); len(blocking) != 4 {
		t.Fatalf("want 4 blocking rows on empty input, got %d", len(blocking))
	}
	for _, row := range r.Rows {
		if !strings.Contains(row.Sample, "n=") {
			t.Fatalf("row %s must carry its denominator, got %q", row.ID, row.Sample)
		}
		if strings.Contains(row.Observed, "0.0%") && row.ID != "raw_traceability" {
			t.Fatalf("row %s printed a measured-looking zero over nothing: %q", row.ID, row.Observed)
		}
	}
}

// TestTheKC2RowsPrintForTheGateScript is what scripts/release-gate/kc2-prereg.sh reads:
// four rows, each with a denominator, and the expectation sentences the document has to
// contain verbatim.
func TestTheKC2RowsPrintForTheGateScript(t *testing.T) {
	r := EvaluateKC2(kc2Fixture(30, 18, 25))
	if len(r.Rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(r.Rows))
	}
	for _, row := range r.Rows {
		if strings.TrimSpace(row.Sample) == "" || !strings.Contains(row.Sample, "n=") {
			t.Fatalf("row %s has no denominator (%q)", row.ID, row.Sample)
		}
		if strings.TrimSpace(row.Expectation) == "" {
			t.Fatalf("row %s has no expectation", row.ID)
		}
		if row.Verdict != "pass" {
			t.Fatalf("row %s is %q on a passing fixture — the gate would report a false red", row.ID, row.Verdict)
		}
		t.Logf("kc2-row: id=%s sample=%s verdict=%s", row.ID, row.Sample, row.Verdict)
		t.Logf("kc2-expectation: %s", row.Expectation)
	}
	if got := r.Control.Sections + r.Treatment.Sections; got != 60 {
		t.Fatalf("fixture should be 60 section records, got %d", got)
	}
	if md := RenderKC2Markdown(r); !strings.Contains(md, "| evidence_coverage |") || !strings.Contains(md, "`allowed`") {
		t.Fatalf("rendered report lost the table or the verdict:\n%s", md)
	}
}
