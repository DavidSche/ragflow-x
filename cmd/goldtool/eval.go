package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/stats"
)

// `eval` is the missing half of doc/301 §9.2. The plan pre-registers the *decision
// rules* of E1/E2 ("主指标 CP 上界 ≤10% 且 coverage = 100% 且准确率点估计 ≥85%"), and
// before this subcommand those rules lived only in prose: a human would read four
// numbers off a spreadsheet and decide, and the first thing a spreadsheet does under
// deadline pressure is quote the point estimate and forget the lower bound. §4.2 says
// exactly that: 22/24 的点估计是 91.7%，CP 下界只有 73.0%，而 E2 的门槛是下界。
//
// Two properties are enforced here rather than documented:
//
//  1. **Every row can fail, and a row without a denominator is `unverified`, never
//     `pass`.** Same rule as the release gate (doc/301 §11.1 G-A1), same reason: a
//     missing input must block a claim, not certify it.
//  2. **The point estimate and the interval are separate rows.** `verdict_accuracy`
//     and `conflicting_recall` each appear twice — once against the point-estimate
//     threshold, once against the CP lower bound — because the plan gives them
//     different tiers, and one number cannot answer both.
//
// The arithmetic is `internal/pkg/stats`, the package the release gate uses, so a run
// and the experiment behind it cannot be certified with two different intervals.

// Prediction is one claim's model output, in the *runtime's* vocabulary (not the gold
// label's). It is deliberately not an Item: predictions and labels are produced by
// different agents for different reasons, and a file that could be both would let a
// model output be re-read as ground truth.
type Prediction struct {
	ItemID        string `json:"item_id"`
	Verdict       string `json:"verdict"`
	QuoteStatus   string `json:"quote_status"`
	AbstainReason string `json:"abstain_reason,omitempty"`
	// NotEvaluatedReason is the third vocabulary a row can carry. Without it a file
	// could say `not_evaluated` and nothing about why, and §4.2's three-cause
	// breakdown would print "无" for a run that skipped claims on budget.
	NotEvaluatedReason string `json:"not_evaluated_reason,omitempty"`
}

// evalRow is one line of the experiment report. `Class` carries doc/301's own
// vocabulary (`硬门槛` / `Tier-2 前置` / `观察`) for §4.2 rows, and §9.2's decision
// column for experiment rows, so a reader can always tell which document a threshold
// came from — and can tell a pre-registered E2 condition from a release gate.
type evalRow struct {
	Class       string `json:"class"`
	Metric      string `json:"metric"`
	Group       string `json:"group,omitempty"`
	Numerator   int64  `json:"numerator"`
	Denominator int64  `json:"denominator"`
	Point       string `json:"point_estimate"`
	Interval    string `json:"clopper_pearson_95"`
	Threshold   string `json:"threshold"`
	Verdict     string `json:"verdict"`
	// OutOfScope marks a row the plan itself says this version cannot decide, e.g. the
	// `false_certification_rate` CP upper bound, which needs a denominator ≥72 that the
	// 36-item supported class cannot supply (doc/301 §4.2, second review P0-2). Such a
	// row still reports its actual n and is still able to *fail* once the denominator
	// exists; what it must not do is make every otherwise-complete run look unverified.
	OutOfScope bool   `json:"out_of_scope_v0_7_0,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type evalReceipt struct {
	Experiment string    `json:"experiment"`
	Method     string    `json:"method"`
	GoldFile   string    `json:"gold_file"`
	PredFile   string    `json:"pred_file"`
	GoldItems  int       `json:"gold_items"`
	Predicted  int       `json:"predicted_items"`
	Rows       []evalRow `json:"rows"`
	Decision   string    `json:"decision"`
	Verdict    string    `json:"verdict"`
	// Prereg is the freeze this run is judged against (doc/301 §9.2 / T05). Nil when the run
	// was not bound to one — which the report says out loud, because an unbound run cannot
	// claim it followed a rule written before the numbers existed.
	Prereg *preregBinding `json:"prereg,omitempty"`
	// Manifest / ManifestSHA256 name the sidecar the judgment was allowed by, and the
	// sidecar's own hash. Provenance, not authentication (304 §7 item 19): a reviewer can
	// pin these in a note and see that they changed; nobody claims the manifest is
	// tamper-proof. Both are omitted when there is no sidecar (a worksheet claims nothing).
	Manifest       string `json:"manifest,omitempty"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
}

const (
	evalPass       = "pass"
	evalFail       = "fail"
	evalUnverified = "unverified"
	evalObserved   = "observed"

	evalClassHard     = "硬门槛"
	evalClassTier2    = "Tier-2 前置"
	evalClassObserved = "观察"
	evalClassE2       = "§9.2 E2 判定"
)

// The thresholds are literals copied from doc/301 §4.2 and §9.2 on purpose: editing one
// of these is a *change to a pre-registered threshold*, and the plan says a threshold is
// only ever met by adding samples, never by relaxing the gate.
const (
	evalUnsupportedFPUpper      = 0.10 // §4.2: unsupported_false_positive_rate CP 上界 ≤10%
	evalAccuracyPointFloor      = 0.85 // §4.2: verdict_accuracy@evaluated 点估计 ≥85%
	evalAccuracyLowerFloor      = 0.78 // §4.2 Tier-2: CP 下界 ≥78%
	evalCoverageFloorProduction = 0.85 // §4.2: 生产 run coverage ≥85%
	evalMacroF1Floor            = 0.80 // §4.2: macro_f1_core ≥0.80
	evalFalseCertPointCeiling   = 0.05 // §4.2: false_certification_rate 点估计 ≤5%
	evalFalseCertUpperCeiling   = 0.05 // §4.2 Tier-2: CP 上界 ≤5%
	evalFalseCertUpperBudget    = 72   // §4.2: 上界 ≤5% 需要分母 ≥72（0/72 = 4.99%）
	evalAbstainCeiling          = 0.15 // §4.2: abstain_rate(model_uncertain) ≤15%
	evalConflictingRecallFloor  = 0.90 // §4.2: conflicting_recall 点估计 ≥90%
	evalConflictingLowerFloor   = 0.78 // §9.2 E2: 召回 CP 下界 ≥78% 才有资格调高 scan_top_k
	evalE2MisreportCeiling      = 0.10 // §9.2 E2: 误报 ≤10%
	evalZeroErrorFloor          = 36   // §4.2/§9.2: 谈"零错误"的最小分母
	evalE2ExpansionSample       = 40   // §9.2 E2: 22/24 时扩样本到 40 条以上再判
	evalAccuracyFloorSample     = 102  // §4.2: 点估计 ≥85% 且下界 ≥78% 需要 n ≥ 102
)

func runEval(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	goldPath := fs.String("gold", "", "assembled gold JSONL (must carry gold_verdict)")
	predPath := fs.String("pred", "", "predictions JSONL: item_id, verdict, quote_status[, abstain_reason]")
	exp := fs.String("exp", "", "experiment: E1 (method selection) or E2 (candidate scan scope)")
	method := fs.String("method", "", "label of the method being evaluated (e.g. rule-v1, single-model)")
	preregPath := fs.String("prereg", "", "pre-registration file to bind this run to (goldtool prereg --freeze)")
	arm := fs.String("arm", "", "pre-registered arm label, required when one method maps to several arms")
	out := fs.String("out", "", "write the markdown report here (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*goldPath) == "" || strings.TrimSpace(*predPath) == "" {
		fmt.Fprintln(stderr, "goldtool eval: --gold and --pred are required")
		return exitUsage
	}
	if strings.TrimSpace(*arm) != "" && strings.TrimSpace(*preregPath) == "" {
		fmt.Fprintln(stderr, "goldtool eval: --arm only means something with --prereg; an arm label without a "+
			"frozen arm list is a nickname")
		return exitUsage
	}
	experiment := strings.ToUpper(strings.TrimSpace(*exp))
	if experiment != "E1" && experiment != "E2" {
		fmt.Fprintf(stderr, "goldtool eval: --exp must be E1 or E2 (doc/301 §9.2), got %q\n", *exp)
		return exitUsage
	}

	// Before judging anything: was this file accepted by the assembly that wrote it? A
	// refused assembly (exit 1) still writes the JSONL so an operator can see what was
	// wrong; scoring that draft here would turn its problems into rows that look measured.
	if refusals := goldRefusals(*goldPath); len(refusals) > 0 {
		for _, r := range refusals {
			fmt.Fprintf(stderr, "goldtool eval: --gold: %s\n", r)
		}
		return exitInvariant
	}

	items, err := readItems(*goldPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool eval: %v\n", err)
		return exitUsage
	}
	preds, err := readPredictions(*predPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool eval: %v\n", err)
		return exitUsage
	}

	var binding *preregBinding
	var bindGaps []string
	if strings.TrimSpace(*preregPath) != "" {
		var problems []string
		var err error
		binding, problems, bindGaps, err = bindPrereg(*preregPath, experiment, *method, *arm, items)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool eval: --prereg: %v\n", err)
			return exitUsage
		}
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintf(stderr, "goldtool eval: --prereg: %s\n", p)
			}
			return exitInvariant
		}
		for _, g := range bindGaps {
			fmt.Fprintf(stderr, "goldtool eval: --prereg: cannot verify: %s\n", g)
		}
	} else {
		// Not an error: doc/301 §9.2's rules are still enforced by the rows. But a run that
		// cites no freeze must say so, otherwise "照抄预注册" is an assertion about a file
		// nobody can produce.
		fmt.Fprintf(stderr, "goldtool eval: no --prereg: rows are judged, but this run cannot claim it "+
			"followed a rule frozen before the numbers existed\n")
	}

	receipt, problems := evaluateExperiment(experiment, *method, items, preds)
	receipt.GoldFile, receipt.PredFile = *goldPath, *predPath
	receipt.Prereg = binding
	receipt.Manifest, receipt.ManifestSHA256 = sidecarFingerprint(*goldPath)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "goldtool eval: %s\n", p)
		}
		return exitInvariant
	}

	md := renderEvalMarkdown(receipt)
	if strings.TrimSpace(*out) != "" {
		if err := writeText(*out, md); err != nil {
			fmt.Fprintf(stderr, "goldtool eval: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "goldtool eval: %s report written to %s (rows=%d verdict=%s)\n",
			receipt.Experiment, *out, len(receipt.Rows), receipt.Verdict)
	} else {
		fmt.Fprint(stdout, md)
	}

	switch receipt.Verdict {
	case evalFail:
		return exitInvariant
	case evalUnverified:
		// Ran correctly, but at least one promise has no denominator to check it
		// against. Same meaning as everywhere else in this tool: declared, never silent.
		return exitIncomplete
	}
	if len(bindGaps) > 0 {
		// The rows passed, but the freeze they are supposed to belong to could not be hashed
		// on this machine — so the binding is unverified, and `pass` would be the one thing
		// this tool never prints without evidence.
		return exitIncomplete
	}
	return exitOK
}

// evaluateExperiment is the pure core, so the tests drive it without files.
func evaluateExperiment(experiment, method string, items []Item, preds []Prediction) (evalReceipt, []string) {
	receipt := evalReceipt{Experiment: experiment, Method: method, GoldItems: len(items)}
	var problems []string

	gold := map[string]Item{}
	for _, it := range items {
		if strings.TrimSpace(it.GoldVerdict) == "" {
			problems = append(problems, fmt.Sprintf("item %q has no gold_verdict: this is a pool/worksheet, "+
				"not a gold file, and an evaluation over it would grade the annotator's absence", it.ItemID))
			continue
		}
		if !containsStr(GoldVerdicts, it.GoldVerdict) {
			problems = append(problems, fmt.Sprintf("item %q: gold_verdict %q is not a gold label (closed set: %s)",
				it.ItemID, it.GoldVerdict, strings.Join(GoldVerdicts, "/")))
			continue
		}
		if _, dup := gold[it.ItemID]; dup {
			problems = append(problems, fmt.Sprintf("item_id %q appears twice in the gold file: "+
				"a claim counted twice silently doubles a denominator", it.ItemID))
			continue
		}
		gold[it.ItemID] = it
	}

	seen := map[string]bool{}
	for _, p := range preds {
		if !containsStr(model.ReportVerdicts(), p.Verdict) {
			problems = append(problems, fmt.Sprintf("%s: verdict %q is not in the runtime vocabulary (doc/301 §5.2)",
				p.ItemID, p.Verdict))
			continue
		}
		if p.QuoteStatus == "" {
			problems = append(problems, fmt.Sprintf("%s: quote_status is empty; a scored verdict must say "+
				"whether its quote was verified, otherwise quote_verified_rate cannot be computed from "+
				"the file (use %q when there is no quote)", p.ItemID, model.ReportQuoteStatusNotProvided))
			continue
		}
		if !containsStr(model.ReportQuoteStatuses(), p.QuoteStatus) {
			problems = append(problems, fmt.Sprintf("%s: quote_status %q is not in the closed set %s",
				p.ItemID, p.QuoteStatus, strings.Join(model.ReportQuoteStatuses(), "/")))
			continue
		}
		if p.AbstainReason != "" && !containsStr(model.ReportAbstainReasons(), p.AbstainReason) {
			problems = append(problems, fmt.Sprintf("%s: abstain_reason %q is not in the closed set %s",
				p.ItemID, p.AbstainReason, strings.Join(model.ReportAbstainReasons(), "/")))
			continue
		}
		// The three state words are mutually exclusive, and each one's reason is what
		// makes its §4.2 row computable. A row that says `abstained` with no reason still
		// counts in the denominator and in none of the four reason buckets, which is how a
		// run appears to abstain less than it did.
		if p.Verdict == model.ReportVerdictAbstained && p.AbstainReason == "" {
			problems = append(problems, fmt.Sprintf("%s: verdict is abstained but abstain_reason is empty; "+
				"§4.2's four abstain rows would all under-report", p.ItemID))
			continue
		}
		if p.Verdict != model.ReportVerdictAbstained && p.AbstainReason != "" {
			problems = append(problems, fmt.Sprintf("%s: abstain_reason %q on verdict %q: a reason for refusing "+
				"to conclude cannot belong to a row that concluded", p.ItemID, p.AbstainReason, p.Verdict))
			continue
		}
		if p.NotEvaluatedReason != "" && !containsStr(model.ReportNotEvaluatedReasons(), p.NotEvaluatedReason) {
			problems = append(problems, fmt.Sprintf("%s: not_evaluated_reason %q is not in the closed set %s",
				p.ItemID, p.NotEvaluatedReason, strings.Join(model.ReportNotEvaluatedReasons(), "/")))
			continue
		}
		if p.Verdict == model.ReportVerdictNotEvaluated && p.NotEvaluatedReason == "" {
			problems = append(problems, fmt.Sprintf("%s: verdict is not_evaluated but not_evaluated_reason is empty; "+
				"budget_exhausted / policy_skipped / not_triggered must stay distinguishable", p.ItemID))
			continue
		}
		if p.Verdict != model.ReportVerdictNotEvaluated && p.NotEvaluatedReason != "" {
			problems = append(problems, fmt.Sprintf("%s: not_evaluated_reason %q on verdict %q",
				p.ItemID, p.NotEvaluatedReason, p.Verdict))
			continue
		}
		if _, ok := gold[p.ItemID]; !ok {
			problems = append(problems, fmt.Sprintf("%s: prediction names an item that is not in the gold file "+
				"(the two files must describe the same claims)", p.ItemID))
			continue
		}
		if seen[p.ItemID] {
			problems = append(problems, fmt.Sprintf("%s: predicted twice; two verdicts for one claim cannot both be scored", p.ItemID))
			continue
		}
		seen[p.ItemID] = true
	}
	if len(problems) > 0 {
		return receipt, problems
	}

	byItem := map[string]Prediction{}
	for _, p := range preds {
		byItem[p.ItemID] = p
	}

	c := tally(items, byItem)
	receipt.Predicted = len(byItem)

	if experiment == "E1" {
		receipt.Rows, receipt.Decision = e1Rows(c)
	} else {
		receipt.Rows, receipt.Decision = e2Rows(items, c, byItem)
	}
	receipt.Verdict = rollUp(receipt.Rows)
	return receipt, nil
}

// evalCounts is the confusion matrix and its marginals. Everything reported is derived
// from it, so two rows can never disagree about the same set of claims.
type evalCounts struct {
	conf          map[string]map[string]int64 // gold verdict -> predicted verdict -> n
	goldClass     map[string]int64
	predClass     map[string]int64
	total         int64
	evaluated     int64 // model.ReportVerdictScored(pred)
	correct       int64 // gold == pred, over evaluated
	abstainReason map[string]int64
	notEvaluated  map[string]int64 // pred verdict == not_evaluated, keyed by reason
	notRequired   int64            // pred verdict == not_required, counted, never scored
	quoteStatus   map[string]map[string]int64
}

func tally(items []Item, preds map[string]Prediction) evalCounts {
	c := evalCounts{
		conf:          map[string]map[string]int64{},
		goldClass:     map[string]int64{},
		predClass:     map[string]int64{},
		abstainReason: map[string]int64{},
		notEvaluated:  map[string]int64{},
		quoteStatus:   map[string]map[string]int64{},
	}
	for _, it := range items {
		c.total++
		c.goldClass[it.GoldVerdict]++
		p, ok := preds[it.ItemID]
		// A claim with no prediction is `not_evaluated`, not "agreed": the coverage row
		// must be able to see it, and a missing file row is the most common way a run
		// looks complete while quietly shrinking its denominator.
		verdict, reason := model.ReportVerdictNotEvaluated, "missing_prediction"
		if ok {
			verdict = p.Verdict
			switch verdict {
			case model.ReportVerdictAbstained:
				reason = p.AbstainReason
			case model.ReportVerdictNotEvaluated:
				reason = p.NotEvaluatedReason
			default:
				reason = ""
			}
		}
		if c.conf[it.GoldVerdict] == nil {
			c.conf[it.GoldVerdict] = map[string]int64{}
		}
		c.conf[it.GoldVerdict][verdict]++
		c.predClass[verdict]++
		if ok {
			if c.quoteStatus[verdict] == nil {
				c.quoteStatus[verdict] = map[string]int64{}
			}
			c.quoteStatus[verdict][p.QuoteStatus]++
		}

		switch {
		case model.ReportVerdictScored(verdict):
			c.evaluated++
			if verdict == it.GoldVerdict {
				c.correct++
			}
		case verdict == model.ReportVerdictAbstained:
			if reason == "" {
				reason = "(no abstain_reason)"
			}
			c.abstainReason[reason]++
		case verdict == model.ReportVerdictNotEvaluated:
			c.notEvaluated[reason]++
		case verdict == model.ReportVerdictNotRequired:
			// `not_required` is a legal runtime verdict ("nothing was asked") but it is not
			// a judgement, so it shrinks coverage. Counting it separately keeps the
			// coverage row from blaming policy/budget for a claim the evaluator declined.
			c.notRequired++
		}
	}
	return c
}

// e1Rows is doc/301 §4.2's computable subset, in the table's own order. Rows that need
// a production run (cache replay, token budget, P95 latency, gateway contract) are not
// silently dropped: the report ends with an explicit list of what it does not cover,
// because a missing row reads as a passing row.
func e1Rows(c evalCounts) ([]evalRow, string) {
	var rows []evalRow

	// 1. claim_evaluation_coverage, gold replay: = 100%, and `not_evaluated` 三因全 0.
	covNum, covDen := c.evaluated, c.total
	reasons := "无"
	if len(c.notEvaluated) > 0 {
		var parts []string
		for _, r := range sortedKeys(c.notEvaluated) {
			parts = append(parts, fmt.Sprintf("%s=%d", r, c.notEvaluated[r]))
		}
		reasons = strings.Join(parts, " / ")
	}
	row := ratioRow(evalClassHard, "claim_evaluation_coverage", "", covNum, covDen,
		"=100%（金标 replay；not_evaluated 三因全 0）")
	if covDen == 0 {
		row.Verdict = evalUnverified
	} else if covNum == covDen && len(c.notEvaluated) == 0 {
		row.Verdict = evalPass
	} else {
		row.Verdict = evalFail
	}
	row.Detail = fmt.Sprintf("not_evaluated 原因：%s（离线 replay 不得有策略跳过或预算退出）", reasons)
	if c.notRequired > 0 {
		row.Detail += fmt.Sprintf("；另有 %d 条被判 not_required（运行时认为无需判定，不计入 coverage 分子）", c.notRequired)
	}
	rows = append(rows, row)

	// 2. unsupported_false_positive_rate: CP 上界 ≤10%.
	ufpNum := c.conf[model.ReportVerdictUnsupported][model.ReportVerdictSupported]
	ufpDen := c.goldClass[model.ReportVerdictUnsupported]
	row = cpUpperRow(evalClassHard, "unsupported_false_positive_rate", "", ufpNum, ufpDen,
		evalUnsupportedFPUpper, "CP 上界 ≤10%")
	if ufpDen > 0 && row.Verdict == evalFail && ufpNum == 0 {
		row.Detail = fmt.Sprintf("0/%d 的上界是 1−α^(1/n)：n<%d 时过不了 10%%，只能加样本不能降门槛",
			ufpDen, evalZeroErrorFloor)
	}
	rows = append(rows, row)

	// 3+4. verdict_accuracy@evaluated: point estimate and CP lower bound, two rows.
	accNum, accDen := c.correct, c.evaluated
	accPoint := ratio(accNum, accDen)
	row = ratioRow(evalClassHard, "verdict_accuracy@evaluated", "点估计", accNum, accDen,
		"≥85%，且 coverage ≥85%（成对达标）")
	switch {
	case accDen == 0:
		row.Verdict = evalUnverified
	case accPoint < evalAccuracyPointFloor:
		row.Verdict = evalFail
		row.Detail = fmt.Sprintf("点估计 %s < 85%%", pct(accPoint))
	case ratio(covNum, covDen) < evalCoverageFloorProduction:
		row.Verdict = evalFail
		row.Detail = fmt.Sprintf("准确率达标但 coverage %s < 85%%：两项必须同时达标", pct(ratio(covNum, covDen)))
	default:
		row.Verdict = evalPass
	}
	rows = append(rows, row)

	row = cpLowerRow(evalClassTier2, "verdict_accuracy@evaluated", "CP 下界", accNum, accDen,
		evalAccuracyLowerFloor, "CP 下界 ≥78%（不得用点估计替代）")
	if accDen > 0 && accDen < evalAccuracyFloorSample && row.Verdict != evalUnverified {
		row.Detail = fmt.Sprintf("n=%d < %d：下界项的可判定样本量是 %d（88/102 = [78.0%%, 92.3%%]），"+
			"该 n 下的下界结论仅供参考", accDen, evalAccuracyFloorSample, evalAccuracyFloorSample)
	}
	rows = append(rows, row)

	// 5. macro_f1_core over the three classes that carry denominators.
	core := []string{model.ReportVerdictSupported, model.ReportVerdictUnsupported, model.ReportVerdictConflicting}
	var f1s []float64
	var parts []string
	missing := ""
	for _, cls := range core {
		if c.goldClass[cls] == 0 {
			missing = cls
			break
		}
		f1 := f1Of(c, cls)
		f1s = append(f1s, f1)
		parts = append(parts, fmt.Sprintf("F1(%s)=%.3f", cls, f1))
	}
	row = evalRow{Class: evalClassHard, Metric: "macro_f1_core", Interval: "—",
		Threshold: "≥0.80（只用 supported/unsupported/conflicting）"}
	if missing != "" {
		row.Verdict = evalUnverified
		row.Point = "—"
		row.Detail = fmt.Sprintf("金标里没有 %s 类样本：宏平均没有分母，不能算", missing)
	} else {
		macro := mean(f1s)
		row.Numerator, row.Denominator = int64(len(core)), c.total
		row.Point = fmt.Sprintf("%.3f", macro)
		if macro >= evalMacroF1Floor {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
		}
		row.Detail = strings.Join(parts, "；")
	}
	rows = append(rows, row)

	// 6+7. false_certification_rate: point ≤5%, and the Tier-2 upper bound that needs
	// n ≥ 72 — which the 36-item supported class cannot supply (doc/301 §4.2).
	fcNum := c.conf[model.ReportVerdictUnsupported][model.ReportVerdictSupported] +
		c.conf[model.ReportVerdictConflicting][model.ReportVerdictSupported]
	fcDen := c.predClass[model.ReportVerdictSupported]
	row = ratioRow(evalClassHard, "false_certification_rate", "点估计", fcNum, fcDen, "≤5%")
	if fcDen > 0 {
		if ratio(fcNum, fcDen) <= evalFalseCertPointCeiling {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
		}
		row.Detail = "金标 unsupported/conflicting 被判 supported 的条数 / 模型判 supported 的全部条数"
	}
	rows = append(rows, row)

	row = cpUpperRow(evalClassTier2, "false_certification_rate", "CP 上界", fcNum, fcDen,
		evalFalseCertUpperCeiling, "CP 上界 ≤5%")
	if fcDen > 0 && fcDen < evalFalseCertUpperBudget {
		row.Verdict = evalUnverified
		row.OutOfScope = true
		row.Detail = fmt.Sprintf("分母 n=%d < %d：上界 ≤5%% 需要 0/72 = 4.99%%，而分母来自模型输出条数，不可控；"+
			"此项是 v0.7.1 预算项，不在本版承诺范围（§4.2 Tier-2 行）", fcDen, evalFalseCertUpperBudget)
	}
	rows = append(rows, row)

	// 8+9. abstain rates. `model_uncertain` is gated; `input_invalid` is an observation.
	muNum := c.abstainReason[model.ReportAbstainModelUncertain]
	row = ratioRow(evalClassHard, "abstain_rate(model_uncertain)", "", muNum, c.total, "≤15%")
	if c.total > 0 {
		if ratio(muNum, c.total) <= evalAbstainCeiling {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
		}
	}
	rows = append(rows, row)

	iiNum := c.abstainReason[model.ReportAbstainInputInvalid]
	row = ratioRow(evalClassObserved, "abstain_rate(input_invalid)", "", iiNum, c.total, "≤5%（观察项）")
	if c.total > 0 {
		row.Verdict = evalObserved
	}
	rows = append(rows, row)

	// 10+11. the two quote rows: the direct counters for a model inventing text.
	qhNum := c.abstainReason[model.ReportAbstainQuoteNotFound] + c.abstainReason[model.ReportAbstainNoQuote]
	row = ratioRow(evalClassHard, "quote_hallucination_rate", "", qhNum, c.total, "= 0")
	if c.total > 0 {
		if qhNum == 0 {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
			row.Detail = "abstain_reason 为 quote_not_found/no_quote：编造原文的直接计数器，出现即 finding + 告警"
		}
	}
	rows = append(rows, row)

	qvNum, qvDen := quoteVerifiedCounts(c)
	row = ratioRow(evalClassHard, "quote_verified_rate", "", qvNum, qvDen,
		"=100%（判 supported/partially_supported 必带已验证 quote）")
	if qvDen > 0 {
		if qvNum == qvDen {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
			row.Detail = fmt.Sprintf("%d 条判 supported/partially_supported 但 quote_status≠verified", qvDen-qvNum)
		}
	}
	rows = append(rows, row)

	// 12+13. conflicting_recall, point and lower bound.
	crNum, crDen := c.conf[model.ReportVerdictConflicting][model.ReportVerdictConflicting],
		c.goldClass[model.ReportVerdictConflicting]
	row = ratioRow(evalClassHard, "conflicting_recall", "点估计", crNum, crDen, "≥90%（≥22/24）")
	if crDen > 0 {
		if ratio(crNum, crDen) >= evalConflictingRecallFloor {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
		}
	}
	rows = append(rows, row)

	rows = append(rows, cpLowerRow(evalClassTier2, "conflicting_recall", "CP 下界", crNum, crDen,
		evalConflictingLowerFloor, "CP 下界 ≥78%（≥23/24）"))

	// 14–16. observation rows: reported, never judged.
	orNum := c.conf[model.ReportVerdictOutdated][model.ReportVerdictOutdated]
	rows = append(rows, observedRow("outdated_recall", orNum, c.goldClass[model.ReportVerdictOutdated]))
	rows = append(rows, observedF1Row("f1_outdated", f1Of(c, model.ReportVerdictOutdated),
		c.goldClass[model.ReportVerdictOutdated], c.conf[model.ReportVerdictOutdated][model.ReportVerdictOutdated]))
	rows = append(rows, observedF1Row("f1_partially_supported", f1Of(c, model.ReportVerdictPartiallySupported),
		c.goldClass[model.ReportVerdictPartiallySupported],
		c.conf[model.ReportVerdictPartiallySupported][model.ReportVerdictPartiallySupported]))

	return rows, e1Decision(ufpNum, ufpDen, covNum, covDen, accNum, accDen)
}

// e1Decision restates §9.2's decision column in one line: 主指标 CP 上界 ≤10% 且
// coverage = 100% 且准确率点估计 ≥85%，且主指标分母 ≥36（否则"零错误"这句话没有资格说）。
func e1Decision(ufpNum, ufpDen, covNum, covDen, accNum, accDen int64) string {
	var blockers []string
	upper := stats.Upper(ufpNum, ufpDen, stats.Alpha)
	if ufpDen == 0 {
		blockers = append(blockers, "主指标无分母（金标里没有 unsupported 样本）")
	} else if upper > evalUnsupportedFPUpper {
		blockers = append(blockers, fmt.Sprintf("unsupported_false_positive_rate CP 上界 %.1f%% > 10%%", 100*upper))
	}
	if covDen == 0 || covNum != covDen {
		blockers = append(blockers, fmt.Sprintf("coverage %d/%d ≠ 100%%", covNum, covDen))
	}
	if accDen == 0 || ratio(accNum, accDen) < evalAccuracyPointFloor {
		blockers = append(blockers, "准确率点估计 < 85%")
	}
	if ufpDen < evalZeroErrorFloor {
		blockers = append(blockers, fmt.Sprintf("主指标分母 n=%d < %d：样本量不足，没有资格谈零错误",
			ufpDen, evalZeroErrorFloor))
	}
	if len(blockers) > 0 {
		return "ineligible：" + strings.Join(blockers, "；")
	}
	return fmt.Sprintf("eligible：主指标 CP 上界 %.1f%% ≤ 10%% 且 coverage = 100%% 且准确率点估计 %s ≥ 85%%；"+
		"若多种方法并列，选成本低者（§9.2）", 100*upper, pct(ratio(accNum, accDen)))
}

// e2Rows is §9.2's E2: would scanning the frozen candidate pool have found the
// contradiction? The two `cited_elsewhere` groups must both be reported even when one
// of them has n=0, because a group that vanishes from the table reads as "never
// defined" (doc/301 §9.1.2/§9.1.3).
func e2Rows(items []Item, c evalCounts, preds map[string]Prediction) ([]evalRow, string) {
	var rows []evalRow

	crNum, crDen := c.conf[model.ReportVerdictConflicting][model.ReportVerdictConflicting],
		c.goldClass[model.ReportVerdictConflicting]
	row := ratioRow(evalClassHard, "conflicting_recall", "点估计", crNum, crDen, "≥90%（≥22/24）")
	if crDen > 0 {
		if ratio(crNum, crDen) >= evalConflictingRecallFloor {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
		}
	}
	rows = append(rows, row)

	rows = append(rows, cpLowerRow(evalClassTier2, "conflicting_recall", "CP 下界", crNum, crDen,
		evalConflictingLowerFloor, "CP 下界 ≥78%（≥23/24）"))

	// 误报：金标不是 conflicting，模型却说 conflicting。
	missNum := int64(0)
	for _, it := range items {
		if it.GoldVerdict == model.ReportVerdictConflicting {
			continue
		}
		if p, ok := preds[it.ItemID]; ok && p.Verdict == model.ReportVerdictConflicting {
			missNum++
		}
	}
	missDen := c.predClass[model.ReportVerdictConflicting]
	row = ratioRow(evalClassE2, "conflicting_misreport_rate", "", missNum, missDen, "≤10%")
	if missDen > 0 {
		if ratio(missNum, missDen) <= evalE2MisreportCeiling {
			row.Verdict = evalPass
		} else {
			row.Verdict = evalFail
		}
	}
	rows = append(rows, row)

	groupRows, groups, unreachable := e2Groups(items, preds)
	rows = append(rows, groupRows...)

	decision := e2Decision(crNum, crDen, missNum, missDen, groups)
	if len(unreachable) > 0 {
		decision += "；注意：" + strings.Join(unreachable, "、") +
			" n=0（语料里没有该组样本），结论不覆盖该组（§9.1.2：报实际 n，不得插补）"
	}
	row = evalRow{Class: evalClassE2, Metric: "scan_top_k 决策", Threshold: "召回 CP 下界 ≥78% 且误报 ≤10%"}
	row.Point, row.Interval, row.Detail = decision, "—", groups
	switch {
	case strings.HasPrefix(decision, "raise"):
		row.Verdict = evalPass
	case strings.HasPrefix(decision, "hold"):
		row.Verdict = evalFail
	default:
		row.Verdict = evalUnverified
	}
	rows = append(rows, row)
	return rows, decision
}

type groupTally struct {
	label        string
	total        int64
	recalled     int64
	unattributed int64
}

// e2Groups counts recall inside each `cited_elsewhere` group. A `conflicting` gold item
// that names no candidate row is *unattributed*: it cannot be bucketed honestly, so it
// gets its own row instead of being folded into a group it may not belong to — and the
// group row turns `unverified`, because a recall number computed over a guess is not a
// number this report may print as a result.
func e2Groups(items []Item, preds map[string]Prediction) ([]evalRow, string, []string) {
	order := []string{
		"cited_only",
		"cited+candidate|cited_elsewhere=true",
		"cited+candidate|cited_elsewhere=false",
	}
	groups := map[string]*groupTally{}
	for _, k := range order {
		groups[k] = &groupTally{label: e2GroupLabel(k)}
	}
	for _, it := range items {
		if it.GoldVerdict != model.ReportVerdictConflicting {
			continue
		}
		key, unattributed := e2GroupOf(it)
		g, ok := groups[key]
		if !ok {
			g = &groupTally{label: key}
			groups[key] = g
			order = append(order, key)
		}
		g.total++
		if unattributed {
			g.unattributed++
		}
		if p, ok := preds[it.ItemID]; ok && p.Verdict == model.ReportVerdictConflicting {
			g.recalled++
		}
	}

	var rows []evalRow
	var parts, unreachable []string
	for _, k := range order {
		g := groups[k]
		row := ratioRow(evalClassObserved, "conflicting_recall", g.label, g.recalled, g.total,
			"分组计数（§9.1.2：不可达组报实际 n）")
		if g.total == 0 {
			row.Verdict = evalUnverified
			row.Detail = "该组无样本，结论不覆盖该组（不得插补、不得合并到另一组）"
			unreachable = append(unreachable, g.label)
		} else {
			row.Verdict = evalObserved
		}
		if g.unattributed > 0 {
			// A conflicting label that names no frozen candidate row is a *data* defect, not
			// a corpus fact: it enters the receipt's verdict, unlike an empty group.
			row.Class = evalClassE2
			row.Verdict = evalUnverified
			row.Detail = fmt.Sprintf("%d 条 conflicting 未写下 gold_candidate_chunk_id，无法归组", g.unattributed)
		}
		rows = append(rows, row)
		parts = append(parts, fmt.Sprintf("%s n=%d", g.label, g.total))
	}
	return rows, strings.Join(parts, "；"), unreachable
}

// e2GroupOf maps a conflicting gold item to its group. The `cited_elsewhere` value is a
// property of the *frozen candidate row the label rests on*, not of the item: that is
// why the label has to name it (doc/301 §9.1.3).
func e2GroupOf(it Item) (string, bool) {
	if !strings.HasPrefix(it.GoldScanScope, "cited+candidate") {
		return "cited_only", false
	}
	if strings.TrimSpace(it.GoldCandidateChunkID) == "" {
		return "cited+candidate|cited_elsewhere=unattributed", true
	}
	for _, cand := range it.CandidatePool {
		if cand.ChunkID == it.GoldCandidateChunkID {
			if cand.CitedElsewhere {
				return "cited+candidate|cited_elsewhere=true", false
			}
			return "cited+candidate|cited_elsewhere=false", false
		}
	}
	return "cited+candidate|cited_elsewhere=unattributed", true
}

func e2GroupLabel(key string) string {
	switch key {
	case "cited_only":
		return "cited_only（无冻结候选）"
	case "cited+candidate|cited_elsewhere=true":
		return "cited+candidate|cited_elsewhere=true"
	case "cited+candidate|cited_elsewhere=false":
		return "cited+candidate|cited_elsewhere=false"
	}
	return key
}

// e2Decision is §9.2's E2 decision column, verbatim. The 22/24 branch is the whole
// point: the point estimate is above the floor and the answer is still "no".
func e2Decision(num, den, missNum, missDen int64, groups string) string {
	if den == 0 {
		return "unverified：无冲突样本（n=0），不构成任何关于 scan_top_k 的结论"
	}
	if missDen == 0 {
		return "unverified：模型一次也没判 conflicting，误报率没有分母"
	}
	point := ratio(num, den)
	lower := stats.Lower(num, den, stats.Alpha)
	misreport := ratio(missNum, missDen)
	if lower >= evalConflictingLowerFloor && misreport <= evalE2MisreportCeiling {
		return fmt.Sprintf("raise：召回 %d/%d = %s，CP 下界 %s ≥ 78%%，误报 %d/%d = %s ≤ 10%%；"+
			"有资格把该模板的 scan_top_k 从 0 调高（分组：%s）",
			num, den, pct(point), pct(lower), missNum, missDen, pct(misreport), groups)
	}
	if point >= evalConflictingRecallFloor {
		return fmt.Sprintf("hold：点估计达标、下界不达标（%d/%d = %s，CP 下界 %s < 78%%），"+
			"不得据此调高 scan_top_k——扩样本到 %d 条以上再判（分组：%s）",
			num, den, pct(point), pct(lower), evalE2ExpansionSample, groups)
	}
	if misreport > evalE2MisreportCeiling {
		return fmt.Sprintf("hold：误报 %d/%d = %s > 10%%，即使召回达标也不得调高 scan_top_k",
			missNum, missDen, pct(misreport))
	}
	return fmt.Sprintf("hold：召回 %d/%d = %s 低于 90%%，不达标", num, den, pct(point))
}

// --- row builders -----------------------------------------------------------------

func ratioRow(class, metric, group string, num, den int64, threshold string) evalRow {
	row := evalRow{Class: class, Metric: metric, Group: group, Numerator: num, Denominator: den,
		Threshold: threshold, Verdict: evalUnverified, Point: "—", Interval: "—"}
	if den <= 0 {
		// The reason is part of the row, not of the caller: a `unverified` row that prints only
		// `—` makes "this class has no samples" look like "nobody filled the table in". Callers
		// that can be more specific (out_of_scope, unattributed) overwrite this afterwards.
		row.Detail = "分母 0：这一类没有可判定的样本，既不是「0 错误」也不构成通过"
		return row
	}
	low, high := stats.Interval(num, den, 0.95)
	row.Point = pct(ratio(num, den))
	row.Interval = fmt.Sprintf("[%s, %s]", pct(low), pct(high))
	return row
}

func cpUpperRow(class, metric, group string, num, den int64, ceiling float64, threshold string) evalRow {
	row := ratioRow(class, metric, group, num, den, threshold)
	if den <= 0 {
		return row
	}
	upper := stats.Upper(num, den, stats.Alpha)
	row.Interval = fmt.Sprintf("[%.1f%%, %.1f%%]（CP 上界 %.1f%%）",
		100*stats.Lower(num, den, stats.Alpha), 100*upper, 100*upper)
	if upper <= ceiling {
		row.Verdict = evalPass
	} else {
		row.Verdict = evalFail
	}
	return row
}

func cpLowerRow(class, metric, group string, num, den int64, floor float64, threshold string) evalRow {
	row := ratioRow(class, metric, group, num, den, threshold)
	if den <= 0 {
		return row
	}
	lower := stats.Lower(num, den, stats.Alpha)
	if lower >= floor {
		row.Verdict = evalPass
	} else {
		row.Verdict = evalFail
	}
	row.Detail = fmt.Sprintf("CP 下界 %s（点估计 %s）", pct(lower), row.Point)
	return row
}

func observedRow(metric string, num, den int64) evalRow {
	row := ratioRow(evalClassObserved, metric, "", num, den, "只报告，不判定")
	if den > 0 {
		row.Verdict = evalObserved
	} else {
		row.Detail = "观察项，且分母 0：只说明这一类没有样本，不构成任何结论"
	}
	return row
}

func observedF1Row(metric string, f1 float64, den, tp int64) evalRow {
	row := evalRow{Class: evalClassObserved, Metric: metric, Numerator: tp, Denominator: den,
		Interval: "—", Threshold: "只报告，不判定", Verdict: evalUnverified, Point: "—"}
	if den > 0 {
		row.Point = fmt.Sprintf("%.3f", f1)
		low, high := stats.Interval(tp, den, 0.95)
		row.Interval = fmt.Sprintf("召回 CP [%s, %s]（F1 本身无区间）", pct(low), pct(high))
		row.Verdict = evalObserved
	} else {
		row.Detail = "观察项，且分母 0：F1 没有这一类样本可算，不构成任何结论"
	}
	return row
}

// --- small maths ------------------------------------------------------------------

func ratio(num, den int64) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func pct(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// f1Of is the per-class F1 over the confusion matrix: precision from the predicted
// column, recall from the gold row.
func f1Of(c evalCounts, class string) float64 {
	tp := c.conf[class][class]
	precision, recall := 0.0, 0.0
	if c.predClass[class] > 0 {
		precision = float64(tp) / float64(c.predClass[class])
	}
	if c.goldClass[class] > 0 {
		recall = float64(tp) / float64(c.goldClass[class])
	}
	if precision+recall == 0 {
		return 0
	}
	return 2 * precision * recall / (precision + recall)
}

func quoteVerifiedCounts(c evalCounts) (num, den int64) {
	for _, v := range []string{model.ReportVerdictSupported, model.ReportVerdictPartiallySupported} {
		den += c.predClass[v]
		num += c.quoteStatus[v][model.ReportQuoteStatusVerified]
	}
	return num, den
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// rollUp is the receipt's single-word verdict: a failure dominates, then a missing
// denominator. Two kinds of row do not enter it — `观察` rows never make anything pass
// or fail, and an `OutOfScope` row is a promise this version explicitly deferred, which
// must stay visible in the table without turning every complete run into `unverified`.
func rollUp(rows []evalRow) string {
	verdict := evalPass
	for _, r := range rows {
		if r.Class == evalClassObserved || r.OutOfScope {
			continue
		}
		switch r.Verdict {
		case evalFail:
			return evalFail
		case evalUnverified:
			verdict = evalUnverified
		}
	}
	return verdict
}

// --- io ----------------------------------------------------------------------------

func readPredictions(path string) ([]Prediction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Prediction
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if text == "" {
			continue
		}
		var p Prediction
		if err := json.Unmarshal([]byte(text), &p); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if strings.TrimSpace(p.ItemID) == "" {
			return nil, fmt.Errorf("%s:%d: prediction has no item_id", path, line)
		}
		out = append(out, p)
	}
	return out, sc.Err()
}

func writeText(path, content string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func renderEvalMarkdown(r evalReceipt) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s 评价报告（%s）\n\n", r.Experiment, orDash(r.Method))
	fmt.Fprintf(&b, "- 金标文件：`%s`（%d 条）\n", r.GoldFile, r.GoldItems)
	fmt.Fprintf(&b, "- 预测文件：`%s`（%d 条）\n", r.PredFile, r.Predicted)
	fmt.Fprintf(&b, "- 判定：**%s**\n", r.Verdict)
	if r.Prereg != nil {
		fmt.Fprintf(&b, "- 预注册：`%s`（%s，frozen %s，作者 %s）sha256 `%s`\n",
			r.Prereg.Path, r.Prereg.Experiment, r.Prereg.FrozenAt, r.Prereg.Author, r.Prereg.SHA256)
		fmt.Fprintf(&b, "- 臂：**%s**（runtime method `%s`，cost_rank %d）；样本 `%s`（%d 条），金标 %d 条\n",
			r.Prereg.Arm, r.Prereg.ArmMethod, r.Prereg.ArmCostRank,
			orDash(r.Prereg.SampleID), r.Prereg.SampleItems, r.Prereg.GoldItems)
	} else {
		b.WriteString("- 预注册：**未引用**（本报告的行仍按 doc/301 §9.2 判定，但不能主张「照抄冻结规则」）\n")
	}
	if r.Manifest != "" {
		// Provenance, not authentication (304 §7 item 19): the reader can pin these two
		// strings in a review note and see later that they changed. Nothing here defends
		// against a manifest edited together with the gold file.
		fmt.Fprintf(&b, "- 伴随件：`%s` sha256 `%s`（记录来源，不构成签名）\n", r.Manifest, r.ManifestSHA256)
	}
	b.WriteString("\n")
	if r.Prereg != nil && len(r.Prereg.PlanDrift) > 0 {
		// A shorter class is disclosed, not repaired (doc/301 §9.1.1 discipline 5): it moves
		// every CP bound in the table below, so it belongs above the table, not in a footnote.
		b.WriteString("| 类别 | 计划 n | 实际 n | 差 |\n| --- | --- | --- | --- |\n")
		for _, d := range r.Prereg.PlanDrift {
			fmt.Fprintf(&b, "| %s | %d | %d | %+d |\n", d.Class, d.Planned, d.Actual, d.Actual-d.Planned)
		}
		b.WriteString("\n计划与实际不一致时，按 301 §9.1.1 纪律 5 披露实际分母，不补样本、不改抽样。\n\n")
	}
	fmt.Fprintf(&b, "统计口径：比例一律给分子/分母 + Clopper–Pearson 精确 95%% 区间（单侧 α=%.3f）；"+
		"点估计与 CP 下界分两行报告（doc/301 §4.2）。\n\n", stats.Alpha)
	b.WriteString("| 类别 | 指标 | 组 | 分子/分母 | 点估计 | CP 95% | 门槛 | 判定 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, row := range r.Rows {
		group := row.Group
		if group == "" {
			group = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d/%d | %s | %s | %s | %s |\n",
			row.Class, row.Metric, group, row.Numerator, row.Denominator,
			row.Point, row.Interval, row.Threshold, row.Verdict)
	}
	// The table is too wide for a detail column, but an `unverified` row without its reason
	// is unreadable: “没有分母” and “不在本版承诺范围” look identical in the 判定 column, and
	// the difference is exactly what a reader has to act on. So the reasons go below.
	var explained []evalRow
	for _, row := range r.Rows {
		if strings.TrimSpace(row.Detail) != "" {
			explained = append(explained, row)
		}
	}
	if len(explained) > 0 {
		b.WriteString("\n### 行说明（为什么这一行不是 pass / 数字怎么来的）\n\n")
		for _, row := range explained {
			name := row.Metric
			if row.Group != "" {
				name += " / " + row.Group
			}
			fmt.Fprintf(&b, "- `%s`（%s）— %s\n", name, row.Verdict, row.Detail)
		}
	}
	b.WriteString("\n## 结论\n\n")
	fmt.Fprintf(&b, "%s\n", r.Decision)
	b.WriteString("\n## 本报告的边界\n\n")
	b.WriteString("这张表只覆盖**离线 replay 能从（金标, 预测）两个文件算出**的指标。它不覆盖：\n\n")
	b.WriteString("1. 生产 run 的 `coverage`（要真实流量才有分母；doc/301 §4.2 归 T16 live drill）；\n")
	b.WriteString("2. `cache_replay_consistency` / `model_reexecution_consistency`（要重放同一 run）；\n")
	b.WriteString("3. 单修订 token ≤80k、P95 ≤90s、计费自洽（要一次真实修订的执行记录）；\n")
	b.WriteString("4. 网关契约覆盖、候选与引用边界（要契约测试与数据库断言）。\n\n")
	b.WriteString("缺行不等于过得去：上面四项要么在 G-A1..G-A8，要么在 T16 的 live drill 里，不由本报告主张。\n")
	return b.String()
}
