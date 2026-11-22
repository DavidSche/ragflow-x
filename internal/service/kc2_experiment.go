package service

// doc/301 §5.7's KC-2 experiment (task T11) is the plan's one *retrieval* claim that
// has to be settled by measurement rather than by review: "Compiled Locator 辅助定位 +
// Raw Retrieval" is allowed to enter report evidence production only if it lifts
// `evidence_coverage` by ≥10pp, keeps raw traceability at 100%, and costs ≤30% more.
//
// The two hard parts of that sentence are not the arithmetic:
//
//  1. **The rule has to exist before the numbers.** §9.2's discipline ("先写判定规则再
//     跑") is what `goldtool prereg` froze for E1/E2; KC-2's rule is written here as
//     constants so a pre-registration file can be compared against them, and so a
//     report cannot quietly apply a rule it was not frozen with.
//  2. **"Can't tell" is not "failed".** A shared universe of 30 sections that yields 12
//     pairs says nothing about a 10pp lift; reporting that as "refused" would be as
//     dishonest as reporting it as "allowed" (doc/301 §9.2's E2 precedent: 22/24 is
//     "点估计达标、下界不达标", not a verdict). So the verdict has three values, and
//     the sample-size row is what promotes a comparison from "cannot judge" to a
//     judgement.
//
// What this file deliberately does not do: it does not compute an interval for the
// lift. Two Clopper–Pearson intervals subtracted from each other are not a paired
// difference interval, and §5.7 states the decision as a point-estimate floor
// ("lift ≥10pp"). The report says so out loud in `NotClaimed` instead of printing a
// plausible-looking band that nobody could defend.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The three pre-registered numbers of doc/301 §5.7, verbatim. A pre-registration file
// is only worth freezing if the compiled rule is the same rule, so `goldtool kc2
// prereg --check` compares the frozen file against these constants in both directions.
const (
	KC2LiftFloorPP       = 10.0
	KC2TraceabilityFloor = 1.0
	KC2CostDeltaCeiling  = 0.30
)

// KC2PlanSectionsPerArm is a *plan* quantity, not a threshold of §5.7: the experiment
// needs a sample size fixed in advance or "expand until it looks right" becomes the
// rule. 30 sections per arm is doc/301 §9.2's own E2 section count; the pre-registration
// records it and its quote says which of the two it is, because confusing a plan
// quantity with a threshold is how a gate gets relaxed.
const KC2PlanSectionsPerArm = 30

const (
	KC2ArmControl   = "control"
	KC2ArmTreatment = "treatment"
)

// Verdicts. `refused` is a result (the rule says no), `unverified` is the absence of a
// judgement (the sample cannot support either answer), and they are never merged.
const (
	KC2VerdictAllowed    = "allowed"
	KC2VerdictRefused    = "refused"
	KC2VerdictUnverified = "unverified"
)

// KC2Section is one section of one arm: the only unit the experiment is defined on.
// Every field is an observation made by the collector (`goldtool kc2 collect`), never
// an inference: `Traceable` counts citations that resolve to a raw chunk, `HintUsed`
// says a hinted document's raw chunk was actually cited.
type KC2Section struct {
	SectionKey string `json:"section_key"`
	Ord        int    `json:"ord"`
	Arm        string `json:"arm"`
	Covered    bool   `json:"covered"`
	Citations  int    `json:"citations"`
	Traceable  int    `json:"traceable"`
	Hints      int    `json:"hints"`
	HintUsed   bool   `json:"hint_used"`
	Tokens     int    `json:"tokens"`
	LatencyMS  int64  `json:"latency_ms"`
}

// KC2ArmSummary is the per-arm arithmetic. Both arms carry the same fields so the
// table cannot show a treatment number with no control next to it.
type KC2ArmSummary struct {
	Arm       string
	Sections  int64
	Covered   int64
	Citations int64
	Traceable int64
	Hinted    int64
	HintUsed  int64
	Tokens    int64
	LatencyMS int64
}

// KC2Row is one judging row. It always carries its own denominator in `Sample`, and a
// row with an empty denominator is `unverified` — never `pass`.
type KC2Row struct {
	ID          string
	Check       string
	Expectation string
	Observed    string
	Sample      string
	Verdict     string
}

// KC2Observation is a secondary metric of §5.7. It has no threshold, so it is printed
// as a measurement and kept out of the verdict: "locator_hit_rate 63.6%" is a fact,
// and calling it a pass would imply a bar nobody agreed on.
type KC2Observation struct {
	ID    string
	Check string
	Value string
}

type KC2Result struct {
	Control      KC2ArmSummary
	Treatment    KC2ArmSummary
	LiftPPM      int64
	LiftPP       float64
	LiftKnown    bool
	Rows         []KC2Row
	Observations []KC2Observation
	Problems     []string
	Gaps         []string
	Verdict      string
	NotClaimed   []string
}

// Blocking returns the rows that are not `pass`. It exists so a caller cannot report
// "allowed" by only looking at the verdict while ignoring a row that could not be
// measured.
func (r KC2Result) Blocking() []KC2Row {
	out := make([]KC2Row, 0, len(r.Rows))
	for _, row := range r.Rows {
		if row.Verdict != "pass" {
			out = append(out, row)
		}
	}
	return out
}

// EvaluateKC2 turns section records into the §5.7 decision. It is pure: no clock, no
// database, no "which pairs count" decision — the caller hands over exactly the
// sections it collected, and the shape checks are what catch a caller that handed over
// something that is not a paired A/B.
func EvaluateKC2(sections []KC2Section) KC2Result {
	result := KC2Result{
		NotClaimed: []string{
			"lift 只是两个臂覆盖率的差：两个 CP 区间相减不是配对差分的区间，本报告不打印 lift 的区间（§5.7 的判定量是点估计）。",
			"每臂 30 节是预注册的规划量（沿用 §9.2 E2 的口径），不是 §5.7 的门槛；样本不足时结论是 unverified，不是 refused。",
			"本报告只说 section 级证据覆盖与成本，不声称端到端质量：KC-2 允许的是「实验开关下可用于报告取证」，不是默认打开。",
		},
	}
	control := map[string]KC2Section{}
	treatment := map[string]KC2Section{}
	// A record that failed a shape check is dropped from the arithmetic — it would
	// contaminate the metric the report is about — but the *pair* must not then be
	// reported as missing as well: one broken record should read as one problem, not
	// two. These sets let the pairing check tell "absent" from "rejected".
	rejected := map[string]map[string]struct{}{KC2ArmControl: {}, KC2ArmTreatment: {}}
	var problems []string
	addProblem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	for _, s := range sections {
		key := strings.TrimSpace(s.SectionKey)
		// Both arms' rejected sets are marked for a record that cannot be used: its
		// counterpart would otherwise be reported as "unpaired" on top of the real
		// problem, and a reader would go fix a pairing that is not broken.
		reject := func() {
			rejected[KC2ArmControl][key] = struct{}{}
			rejected[KC2ArmTreatment][key] = struct{}{}
		}
		switch {
		case key == "":
			addProblem("section_without_key: ord=%d arm=%q — 配对靠 section_key，没有键的节无法证明两臂看的是同一节", s.Ord, s.Arm)
			continue
		case s.Citations < 0 || s.Traceable < 0 || s.Hints < 0 || s.Tokens < 0 || s.LatencyMS < 0:
			addProblem("negative_count:%s/%s — 负计数说明采集器或手写文件有一处是错的", s.Arm, key)
			reject()
			continue
		case s.Traceable > s.Citations:
			addProblem("traceable_exceeds_citations:%s/%s — %d 条可回溯引用 > %d 条引用", s.Arm, key, s.Traceable, s.Citations)
			reject()
			continue
		case s.Covered && s.Traceable == 0:
			addProblem("covered_without_traceable_citation:%s/%s — 覆盖的定义就是「至少一条可回溯引用」", s.Arm, key)
			reject()
			continue
		}
		switch s.Arm {
		case KC2ArmControl:
			if s.Hints > 0 {
				// The control arm is *defined* as the run that does not see locator hints.
				// A control record with hints is not a small blemish: it means the two arms
				// differ in more than the variable under test.
				addProblem("control_with_hints:%s — control 臂看到了 %d 条 locator hint；两臂只允许在 hint 上有差别", key, s.Hints)
				reject()
				continue
			}
			if _, dup := control[key]; dup {
				addProblem("duplicate_section:%s/%s", s.Arm, key)
				continue
			}
			control[key] = s
		case KC2ArmTreatment:
			if _, dup := treatment[key]; dup {
				addProblem("duplicate_section:%s/%s", s.Arm, key)
				continue
			}
			treatment[key] = s
		default:
			addProblem("unknown_arm:%q — 只有 %q 与 %q 两个臂（§5.7）", s.Arm, KC2ArmControl, KC2ArmTreatment)
			rejected[KC2ArmControl][key] = struct{}{}
			rejected[KC2ArmTreatment][key] = struct{}{}
		}
	}

	// A paired experiment means one section set. A section in only one arm is a
	// collection defect, not a datum: comparing different sections would report a
	// difference in *outline*, not a difference in retrieval.
	for key := range control {
		if _, ok := treatment[key]; !ok {
			if _, dropped := rejected[KC2ArmTreatment][key]; dropped {
				continue
			}
			addProblem("unpaired_section:%s — 只出现在 control 臂", key)
		}
	}
	for key := range treatment {
		if _, ok := control[key]; !ok {
			if _, dropped := rejected[KC2ArmControl][key]; dropped {
				continue
			}
			addProblem("unpaired_section:%s — 只出现在 treatment 臂", key)
		}
	}

	result.Control = summariseKC2Arm(KC2ArmControl, control)
	result.Treatment = summariseKC2Arm(KC2ArmTreatment, treatment)

	treatmentHinted := false
	for _, s := range treatment {
		if s.Hints > 0 {
			treatmentHinted = true
			break
		}
	}
	if len(treatment) > 0 && !treatmentHinted {
		// This is the failure mode the live drill actually has: every section has zero
		// hints, so "treatment" is the same run as "control" with a different label. A
		// difference measured from two identical inputs is noise dressed as a lift.
		addProblem("treatment_without_hints — treatment 臂没有任何 locator hint：这不是 A/B，是同一种检索跑了两遍")
	}

	if result.Control.Sections > 0 && result.Treatment.Sections > 0 {
		result.LiftKnown = true
		result.LiftPPM = kc2LiftPPM(result.Control.Covered, result.Control.Sections,
			result.Treatment.Covered, result.Treatment.Sections)
		// PPM is parts-per-million *of the rate*; the report speaks in percentage points,
		// and one point is 10,000 ppm.
		result.LiftPP = float64(result.LiftPPM) / 1e4
	}

	rows, gaps := kc2Rows(result)
	result.Rows = rows
	result.Gaps = gaps

	sort.Strings(problems)
	result.Problems = problems

	result.Observations = kc2Observations(result)

	switch {
	case kc2RowOf(rows, "raw_traceability").Verdict == "fail":
		// Traceability is an invariant of the evidence chain, not a threshold of the
		// experiment: a single untraceable citation disqualifies the arm regardless of
		// how small the sample is, because the thing being protected is "every citation
		// goes back to a raw chunk". Sample size cannot make that acceptable.
		result.Verdict = KC2VerdictRefused
	case len(problems) > 0:
		// A shape problem means the input is not an A/B at all (treatment with no hint is
		// the same retrieval run twice), or a count cannot be read. That is "I could not
		// check", not "it does not hold": exit 3, never exit 1. Calling it refused would
		// let a broken collector masquerade as a finished experiment with a negative
		// result — the one outcome §5.7 cannot tolerate, because a refused KC-2 is a
		// decision to keep the feature off.
		result.Verdict = KC2VerdictUnverified
	case len(gaps) > 0:
		// Sample gaps dominate the point estimates, in both directions. 12 sections with a
		// −9pp lift is not "the treatment failed" either — it is the same 12 sections that
		// could not have shown a +10pp lift. §9.2's E2 precedent is exactly this: 22/24 was
		// deferred to a bigger sample, not published as a verdict.
		result.Verdict = KC2VerdictUnverified
	case kc2AnyFail(rows):
		result.Verdict = KC2VerdictRefused
	case len(result.Blocking()) > 0:
		result.Verdict = KC2VerdictUnverified
	default:
		result.Verdict = KC2VerdictAllowed
	}
	return result
}

func summariseKC2Arm(arm string, sections map[string]KC2Section) KC2ArmSummary {
	out := KC2ArmSummary{Arm: arm, Sections: int64(len(sections))}
	for _, s := range sections {
		if s.Covered {
			out.Covered++
		}
		out.Citations += int64(s.Citations)
		out.Traceable += int64(s.Traceable)
		out.Tokens += int64(s.Tokens)
		out.LatencyMS += s.LatencyMS
		if s.Hints > 0 {
			out.Hinted++
		}
		if s.HintUsed {
			out.HintUsed++
		}
	}
	return out
}

// kc2Rows builds the four judging rows, in §5.7's reading order: whether the sample can
// support a judgement, then the main metric, then the invariant, then the cost.
func kc2Rows(r KC2Result) ([]KC2Row, []string) {
	var gaps []string
	rows := make([]KC2Row, 0, 4)

	minSections := r.Control.Sections
	if r.Treatment.Sections < minSections {
		minSections = r.Treatment.Sections
	}
	sampleVerdict := "pass"
	sampleObserved := fmt.Sprintf("control %d / treatment %d", r.Control.Sections, r.Treatment.Sections)
	switch {
	case minSections == 0:
		sampleVerdict = "unverified"
		for _, arm := range []KC2ArmSummary{r.Control, r.Treatment} {
			if arm.Sections == 0 {
				gaps = append(gaps, fmt.Sprintf("arm_has_no_sections:%s — 该臂一条 section 都没采到", arm.Arm))
			}
		}
	case minSections < KC2PlanSectionsPerArm:
		sampleVerdict = "unverified"
		gaps = append(gaps, fmt.Sprintf("sections_below_plan:%d < %d — 样本不足，任何 lift 都还不能算结论", minSections, KC2PlanSectionsPerArm))
	}
	rows = append(rows, KC2Row{
		ID:          "sample_size",
		Check:       "每臂可配对的 section 数",
		Expectation: fmt.Sprintf("每臂 ≥%d 节（预注册的规划量）", KC2PlanSectionsPerArm),
		Observed:    sampleObserved,
		Sample:      fmt.Sprintf("n=%d", minSections),
		Verdict:     sampleVerdict,
	})

	coverageVerdict := "unverified"
	coverageObserved := "—"
	coverageSample := "n=0"
	if r.LiftKnown {
		coverageVerdict = "fail"
		// Compared in integer parts-per-million, not as a difference of two float ratios:
		// 7/10 − 6/10 is 9.999999999999998pp in float64, and a lift of *exactly* the
		// pre-registered floor must pass. The floor is converted once, with Round, because
		// 0.3*100 is 30.000000000000004 in float64.
		if r.LiftPPM >= int64(math.Round(KC2LiftFloorPP*1e4)) {
			coverageVerdict = "pass"
		}
		coverageObserved = fmt.Sprintf("control %s / treatment %s / lift %+.1fpp",
			kc2Rate(r.Control.Covered, r.Control.Sections),
			kc2Rate(r.Treatment.Covered, r.Treatment.Sections),
			r.LiftPP)
		coverageSample = fmt.Sprintf("n=%d/%d", r.Control.Sections, r.Treatment.Sections)
	} else {
		gaps = append(gaps, "evidence_coverage_unknown — 有一臂没有 section，lift 无从计算")
	}
	rows = append(rows, KC2Row{
		ID:          "evidence_coverage",
		Check:       "主指标：有 ≥1 条可用证据的节占比（lift = treatment − control）",
		Expectation: fmt.Sprintf("lift ≥%.0fpp", KC2LiftFloorPP),
		Observed:    coverageObserved,
		Sample:      coverageSample,
		Verdict:     coverageVerdict,
	})

	traceCitations := r.Control.Citations + r.Treatment.Citations
	traceable := r.Control.Traceable + r.Treatment.Traceable
	traceVerdict := "unverified"
	if traceCitations > 0 {
		traceVerdict = "pass"
		if float64(traceable)/float64(traceCitations) < KC2TraceabilityFloor {
			traceVerdict = "fail"
		}
	} else {
		gaps = append(gaps, "raw_traceability_unknown — 两臂都没有引用，回溯率没有分母")
	}
	rows = append(rows, KC2Row{
		ID:          "raw_traceability",
		Check:       "不变量：每条引用都能回到 raw chunk",
		Expectation: "100%",
		Observed:    fmt.Sprintf("%d/%d = %s", traceable, traceCitations, kc2Rate(traceable, traceCitations)),
		Sample:      fmt.Sprintf("n=%d", traceCitations),
		Verdict:     traceVerdict,
	})

	costVerdict := "unverified"
	costObserved := "—"
	costSample := "n=0"
	if r.Control.Tokens > 0 && r.Treatment.Tokens > 0 {
		// `(t−c)/c ≤ 30%` cross-multiplied into integers: no float comparison at the
		// boundary, so exactly +30.0% is a pass and +30.1% is a fail, always.
		ceiling := int64(math.Round(KC2CostDeltaCeiling * 100))
		costVerdict = "pass"
		if (r.Treatment.Tokens-r.Control.Tokens)*100 > ceiling*r.Control.Tokens {
			costVerdict = "fail"
		}
		costObserved = fmt.Sprintf("control %d / treatment %d tokens = %+.1f%%", r.Control.Tokens, r.Treatment.Tokens,
			float64(r.Treatment.Tokens-r.Control.Tokens)/float64(r.Control.Tokens)*100)
		costSample = fmt.Sprintf("n=%d/%d", r.Control.Sections, r.Treatment.Sections)
	} else {
		for _, arm := range []KC2ArmSummary{r.Control, r.Treatment} {
			if arm.Tokens == 0 {
				gaps = append(gaps, fmt.Sprintf("tokens_missing:%s — 该臂 token 合计为 0，成本增幅没有分母", arm.Arm))
			}
		}
	}
	rows = append(rows, KC2Row{
		ID:          "cost_delta_tokens",
		Check:       "成本：treatment 相对 control 的 token 增幅（按 token，不按货币，§9.3）",
		Expectation: fmt.Sprintf("≤%.0f%%", KC2CostDeltaCeiling*100),
		Observed:    costObserved,
		Sample:      costSample,
		Verdict:     costVerdict,
	})
	return rows, gaps
}

// kc2Observations are §5.7's secondary metrics. They inform a reader and never change
// the verdict; `locator_hit_rate` in particular has no threshold anywhere in the plan.
func kc2Observations(r KC2Result) []KC2Observation {
	out := make([]KC2Observation, 0, 2)
	hit := "—"
	if r.Treatment.Hinted > 0 {
		hit = fmt.Sprintf("%s（%d/%d 节，有 hint 的 treatment 节里 hint 指向的原文被采用）",
			kc2Rate(r.Treatment.HintUsed, r.Treatment.Hinted), r.Treatment.HintUsed, r.Treatment.Hinted)
	}
	out = append(out, KC2Observation{ID: "locator_hit_rate", Check: "次指标：locator_hit_rate（无门槛）", Value: hit})

	latency := "—"
	if r.Control.Sections > 0 && r.Treatment.Sections > 0 && r.Control.LatencyMS > 0 {
		delta := float64(r.Treatment.LatencyMS-r.Control.LatencyMS) / float64(r.Control.LatencyMS)
		latency = fmt.Sprintf("%+.1f%%（control %dms / treatment %dms）", delta*100, r.Control.LatencyMS, r.Treatment.LatencyMS)
	}
	out = append(out, KC2Observation{ID: "latency_delta", Check: "次指标：检索耗时（无门槛，不计入 30% 成本）", Value: latency})
	return out
}

func kc2RowOf(rows []KC2Row, id string) KC2Row {
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	return KC2Row{ID: id, Verdict: "unverified"}
}

func kc2AnyFail(rows []KC2Row) bool {
	for _, row := range rows {
		if row.Verdict == "fail" {
			return true
		}
	}
	return false
}

// kc2Rate formats a share over its own denominator, and refuses to print a
// measured-looking 0% over nothing: an empty denominator is "unverified", never "0.0%".
func kc2Rate(k, n int64) string {
	if n <= 0 {
		return "unverified"
	}
	return fmt.Sprintf("%.1f%%", float64(k)/float64(n)*100)
}

// kc2LiftPPM is the main metric's lift in integer parts-per-million: the difference of
// two ratios computed by cross-multiplication, so a boundary case is decided by integers
// rather than by the last bit of a float64 division. Truncation is fine here — the
// pre-registered floor is 10pp, nine orders of magnitude above the precision lost.
func kc2LiftPPM(controlCovered, controlSections, treatmentCovered, treatmentSections int64) int64 {
	if controlSections <= 0 || treatmentSections <= 0 {
		return 0
	}
	return (treatmentCovered*controlSections - controlCovered*treatmentSections) * 1_000_000 /
		(treatmentSections * controlSections)
}

// RenderKC2Markdown renders the report a run binds into doc/305. It prints every row
// with its denominator, the secondary metrics separately, and the verdict sentence from
// §5.7 — because the verdict word alone ("refused") does not say which of the three
// conditions said no.
func RenderKC2Markdown(r KC2Result) string {
	var b strings.Builder
	b.WriteString("### KC-2（doc/301 §5.7）实验判定\n\n")
	b.WriteString("| # | 检查 | 期望 | 实测 | n | 判定 |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | **%s** |\n",
			row.ID, row.Check, row.Expectation, row.Observed, row.Sample, row.Verdict)
	}
	if len(r.Observations) > 0 {
		b.WriteString("\n**次指标（不参与判定）**\n\n| 指标 | 值 |\n| --- | --- |\n")
		for _, obs := range r.Observations {
			fmt.Fprintf(&b, "| %s | %s |\n", obs.Check, obs.Value)
		}
	}
	if len(r.Problems) > 0 {
		b.WriteString("\n**输入形状问题（不是实验结论，是采集/文件的问题）**\n\n")
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if len(r.Gaps) > 0 {
		b.WriteString("\n**无法判定（unverified，不是通过）**\n\n")
		for _, g := range r.Gaps {
			fmt.Fprintf(&b, "- %s\n", g)
		}
	}
	fmt.Fprintf(&b, "\n**判定**：`%s`\n\n", r.Verdict)
	switch r.Verdict {
	case KC2VerdictAllowed:
		b.WriteString("lift、回溯与成本三条同时达到 §5.7 的门槛，且样本达到预注册的每臂 30 节：允许在实验开关下用于报告取证，**不**允许默认打开。\n")
	case KC2VerdictRefused:
		b.WriteString("§5.7 的判定为否：保持关闭，并按 §5.7 的要求写负面实测报告。`refused` 是结论，不是错误码——它与「采集坏了」共用 exit 1，报告里两者分开列。\n")
	case KC2VerdictUnverified:
		if len(r.Problems) > 0 {
			// The reason matters: "the sample is short" is fixed by running more sections,
			// "the input is not an A/B" is fixed by fixing the collector. One sentence for
			// both would send the reader to the wrong job.
			b.WriteString("输入形状有问题——这不是一次可判定的实验（不是 A/B，或计数读不出来），所以既不是通过也不是失败，是**还不能判**。先修采集，再重跑；不改门槛。\n")
		} else {
			b.WriteString("样本或分母不足以判定：这不是通过，也不是失败，是**还不能判**。要得到结论只能加样本（预注册的每臂 30 节），不能改门槛。\n")
		}
	}
	if len(r.NotClaimed) > 0 {
		b.WriteString("\n**不声称**\n\n")
		for _, n := range r.NotClaimed {
			fmt.Fprintf(&b, "- %s\n", n)
		}
	}
	return b.String()
}
