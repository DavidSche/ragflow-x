package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// `prereg` is the file task T05 (doc/304 §4) has been waiting for: "预注册文件先于运行
// 提交". doc/301 §9.2 says the decision rules are written down *before* the run and then
// "跑完照抄"; until this subcommand existed there was no artifact for "written down" —
// only a paragraph in a planning document, which is editable at any time and says nothing
// about *which* sample or *which* gold file the rules were meant for.
//
// A pre-registration that only restates thresholds would be decoration. What makes it
// worth freezing is that it pins three things a spreadsheet cannot:
//
//  1. **The thresholds, in triplicate.** Each number carries the sentence it was copied
//     from (`quote`) and the section it lives in (`ref`). `--check` compares the file
//     against the literals compiled into `eval` and fails on any difference in either
//     direction: editing a constant in Go without re-freezing is a red check, and editing
//     the file without editing Go is a red check. A threshold is met by adding samples,
//     never by editing either copy.
//  2. **The sample, by hash.** The worksheet's sha256 is recorded with its item count, so
//     "the gold set was drawn from the pre-registered sample" becomes a set-containment
//     proof (`gold items ⊆ sample items`, checked by `eval --prereg`) instead of a claim.
//  3. **The arms and their cost order.** doc/301 §9.2 breaks ties by cost, so the order is
//     part of the pre-registration. An arm also names the `method` the runtime records
//     (`preds --method`), because the plan's four arms do not map one-to-one onto the four
//     evaluation methods in `model.ReportEvaluationMethods()` — "模型" and "模型 + quote +
//     规则" are both `semantic`. That is a real limitation of the run table and the file
//     records it rather than hiding it: `eval` requires `--arm` whenever a method matches
//     more than one arm.
//
// What `--check` cannot do is prove that the freeze happened before the results were seen.
// `frozen_at` is a self-declared timestamp; the machine-checkable parts are the hashes and
// the threshold table. The file says so in its own `not_claimed` list, because a
// pre-registration that overstates what it guarantees is worse than none.

const preregSchema = "rgx.goldtool.prereg/1"

const (
	prUnitRate  = "rate"
	prUnitCount = "count"
)

// evalThreshold is one pre-registered number plus the sentence and section it came from.
// The table below is built out of the same constants `eval` judges with — there is no
// second copy of a number anywhere — so the only way to move a threshold is to move it in
// Go, which `--check` then reports against every frozen file.
type evalThreshold struct {
	Key   string  `json:"key"`
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
	Quote string  `json:"quote"`
	Ref   string  `json:"ref"`
}

// evalThresholdTable is evaluation order, not alphabetical: it reads like §4.2 and §9.2.
var evalThresholdTable = []evalThreshold{
	{"evalUnsupportedFPUpper", prUnitRate, evalUnsupportedFPUpper, "主指标 CP 上界 ≤10%", "301 §4.2"},
	{"evalAccuracyPointFloor", prUnitRate, evalAccuracyPointFloor, "verdict_accuracy@evaluated 点估计 ≥85%", "301 §4.2"},
	{"evalAccuracyLowerFloor", prUnitRate, evalAccuracyLowerFloor, "准确率 CP 下界 ≥78%", "301 §4.2 Tier-2"},
	{"evalCoverageFloorProduction", prUnitRate, evalCoverageFloorProduction, "生产 run coverage ≥85%", "301 §4.2"},
	{"evalMacroF1Floor", prUnitRate, evalMacroF1Floor, "macro_f1_core ≥0.80", "301 §4.2"},
	{"evalFalseCertPointCeiling", prUnitRate, evalFalseCertPointCeiling, "false_certification_rate 点估计 ≤5%", "301 §4.2"},
	{"evalFalseCertUpperCeiling", prUnitRate, evalFalseCertUpperCeiling, "Tier-2 CP 上界 ≤5%", "301 §4.2"},
	{"evalAbstainCeiling", prUnitRate, evalAbstainCeiling, "abstain_rate(model_uncertain) ≤15%", "301 §4.2"},
	{"evalConflictingRecallFloor", prUnitRate, evalConflictingRecallFloor, "conflicting_recall 点估计 ≥90%", "301 §4.2"},
	{"evalConflictingLowerFloor", prUnitRate, evalConflictingLowerFloor, "E2 召回 CP 下界 ≥78% 才有资格调高 scan_top_k", "301 §9.2"},
	{"evalE2MisreportCeiling", prUnitRate, evalE2MisreportCeiling, "E2 误报 ≤10%", "301 §9.2"},
	{"evalFalseCertUpperBudget", prUnitCount, evalFalseCertUpperBudget, "CP 上界 ≤5% 需要分母 ≥72", "301 §4.2"},
	{"evalZeroErrorFloor", prUnitCount, evalZeroErrorFloor, "谈「零错误」的最小分母 ≥36", "301 §4.2"},
	{"evalE2ExpansionSample", prUnitCount, evalE2ExpansionSample, "22/24 时扩样本到 40 条以上再判", "301 §9.2"},
	{"evalAccuracyFloorSample", prUnitCount, evalAccuracyFloorSample, "点估计 ≥85% 且下界 ≥78% 需要 n ≥102", "301 §4.2"},
}

// preregArm is one arm of the experiment. `Method` is the runtime's closed-set method
// label (what `preds --method` writes), `Label` is the plan's own wording, and `CostRank`
// is the tie-break doc/301 §9.2 asks for ("若多条并列，选成本低者").
type preregArm struct {
	Label    string `json:"label"`
	Method   string `json:"method"`
	CostRank int    `json:"cost_rank"`
}

// preregInput is one frozen input: the sample the acceptance metrics are computed on, and
// the pool it was drawn from. Both are recorded by hash and item count, so `--check` can
// answer "is this still the file the rules were written for".
type preregInput struct {
	Role   string `json:"role"` // sample | pool
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Items  int    `json:"items"`
}

type preregFile struct {
	Schema     string          `json:"schema"`
	Experiment string          `json:"experiment"`
	FrozenAt   string          `json:"frozen_at"`
	Author     string          `json:"author"`
	Primary    string          `json:"primary_metric"`
	Decision   string          `json:"decision"`
	Arms       []preregArm     `json:"arms"`
	Thresholds []evalThreshold `json:"thresholds"`
	Plan       map[string]int  `json:"plan"`
	PlanTotal  int             `json:"plan_total"`
	Inputs     []preregInput   `json:"inputs"`
	SampleID   string          `json:"sample_id,omitempty"`
	Receipt    string          `json:"sample_receipt,omitempty"`
	// Disciplines is doc/301 §9.1.1's five rules. They are part of the file because they
	// are the part a reader is most tempted to skip: "don't merge the stratified supplement
	// into the primary denominator" is the difference between a CP bound that estimates a
	// population and one that estimates a convenience sample.
	Disciplines []string `json:"sampling_disciplines"`
	NotClaimed  []string `json:"not_claimed"`
}

// preregDecision returns the verbatim decision rule of doc/301 §9.2 for an experiment.
// E0 and E3 are deliberately absent: neither has a judging instrument in this tool yet, and
// freezing a rule nothing can execute would rebuild exactly the prose problem `eval` exists
// to remove.
func preregDecision(exp string) string {
	if exp == "E2" {
		return "召回 CP 下界 ≥78% 且误报 ≤10% 才有资格把某模板的 scan_top_k 从 0 调高；" +
			"24 条样本下 22/24 属「点估计达标、下界不达标」，不得据此调高，扩样本到 40 条以上再判。"
	}
	return "主指标 CP 上界 ≤10% 且 coverage = 100%（金标 replay 口径：离线、预算充足、无策略跳过，" +
		"not_evaluated 三因必须为 0）且准确率点估计 ≥85%；若多条并列，选成本低者。" +
		"样本量必须 ≥36 才有资格谈「零错误」。"
}

func preregPrimary(exp string) string {
	if exp == "E2" {
		return "conflicting_recall"
	}
	return "unsupported_false_positive_rate"
}

// defaultArms is doc/301 §9.2's comparison column, in the order the plan lists it — which
// is also increasing cost, so `cost_rank` is the list position. The method mapping is the
// honest one: the runtime records `deterministic` for the rule layer and cannot tell
// "single model" from "single model + quote + rule", so both are `semantic` and `eval`
// demands `--arm` to disambiguate.
func defaultArms(exp string) []preregArm {
	if exp == "E2" {
		return []preregArm{
			{Label: "cited_only", Method: model.ReportEvaluationMethodRule, CostRank: 1},
			{Label: "cited+candidate:1", Method: model.ReportEvaluationMethodRule, CostRank: 2},
		}
	}
	return []preregArm{
		{Label: "rule-v1", Method: model.ReportEvaluationMethodRule, CostRank: 1},
		{Label: "single-model", Method: model.ReportEvaluationMethodSemantic, CostRank: 2},
		{Label: "dual-model", Method: model.ReportEvaluationMethodCross, CostRank: 3},
		{Label: "single-model+quote+rule", Method: model.ReportEvaluationMethodSemantic, CostRank: 4},
	}
}

// preregPlan is the sample plan the file freezes. E1's plan is the gold distribution of
// doc/301 §9.1 — the numbers are not round because the CP bounds are not; E2's plan is 24
// conflicting claims plus 30 sections, from §9.2.
func preregPlan(exp string) map[string]int {
	if exp == "E2" {
		return map[string]int{"conflicting_claims": 24, "sections": 30}
	}
	out := make(map[string]int, len(GoldDistribution))
	for k, v := range GoldDistribution {
		out[k] = v
	}
	return out
}

func preregDisciplines() []string {
	return []string{
		"主样本用固定 seed 的等概率随机抽样，从冻结后的完整候选框内抽取；每个指标明确自己的实际分母，不足即披露不足，不事后调整抽样策略去满足门槛。",
		"分层样本独立生成、独立编号、独立报告：与主样本不共用文件名、不合并、不互相补位。",
		"主验收指标只使用本文件登记的样本；分层样本的类别占比不得被当作总体占比。",
		"分层补充样本只用于诊断，除非另行预注册加权估计方法；参与总体估计必须用符合抽样设计的加权估计与区间，不能把两套样本简单合并后用普通 CP。",
		"实际类别分母不足时披露样本不足（unreachable / 实际 n），不允许用重复抽样或人工补造样本凑数。",
	}
}

func preregNotClaimed(exp string) []string {
	out := []string{
		"frozen_at 是自述时间：机器可检的是输入 sha256 与阈值表一致，不能证明冻结发生在看到结果之前（git 历史是唯一的旁证）。",
		"E0/E3 未在本文件内预注册：E0 需要语义层实跑，E3 需要编译取证 PoC，goldtool 目前没有判定它们的工具。",
	}
	if exp == "E2" {
		out = append(out, "本文件冻结的是候选框与判定规则；24 条冲突分母来自 T02 金标，若不可达按 §9.1.2 报实际 n 并降级。")
	} else {
		out = append(out, "分层补充样本不参与主验收指标（301 §9.1.1 纪律 4）；本文件只登记主样本。")
	}
	return out
}

// --- commands ------------------------------------------------------------------------

func runPrereg(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("prereg", flag.ContinueOnError)
	fs.SetOutput(stderr)
	freeze := fs.Bool("freeze", false, "write a new pre-registration file (refuses to overwrite one)")
	checkPath := fs.String("check", "", "validate an existing pre-registration file")
	digestMode := fs.Bool("digest", false, "write a digest of the frozen file (no corpus needed; doc/304 §7 item 18)")
	preregIn := fs.String("prereg", "", "the frozen file to digest (with --digest)")
	digestFile := fs.String("digest-file", "", "the digest to check --check against (proves the file is the frozen one)")
	exp := fs.String("exp", "", "experiment to freeze: E1 or E2 (doc/301 §9.2)")
	samplePath := fs.String("sample", "", "the frozen primary sample (worksheet) the metrics will be computed on")
	poolPath := fs.String("pool", "", "the pool the sample was drawn from")
	receiptPath := fs.String("sample-receipt", "", "the `goldtool sample` receipt for --sample; required with --freeze (names sample_id and the pool it was drawn from)")
	armsFlag := fs.String("arms", "", "arm labels in cost order, comma separated (default: doc/301 §9.2's comparison column)")
	author := fs.String("author", "", "who froze this file")
	out := fs.String("out", "", "write the pre-registration here (required with --freeze)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	modes := 0
	for _, on := range []bool{*freeze, *checkPath != "", *digestMode} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		fmt.Fprintln(stderr, "goldtool prereg: pass exactly one of --freeze, --check FILE or --digest "+
			"(three different jobs: write the freeze, verify it, or pin it without the corpus)")
		return exitUsage
	}
	if !*digestMode && strings.TrimSpace(*preregIn) != "" {
		fmt.Fprintln(stderr, "goldtool prereg: --prereg is the file to digest and only means something with --digest")
		return exitUsage
	}
	if *digestMode {
		return runPreregDigest(*preregIn, *out, *digestFile, stdout, stderr)
	}

	if *checkPath != "" {
		if strings.TrimSpace(*digestFile) != "" {
			// Answer the first question first: is this the file that was frozen? On a clean
			// clone the corpus is absent, so without the digest both questions end as the same
			// "cannot verify" — and the stronger one (this is the frozen file) gets no answer.
			d, err := readPreregDigest(*digestFile)
			if err != nil {
				fmt.Fprintf(stderr, "goldtool prereg: %v\n", err)
				return exitUsage
			}
			dps, lineEndingsOnly := checkPreregDigest(d, *checkPath)
			if len(dps) > 0 {
				for _, dp := range dps {
					fmt.Fprintf(stderr, "goldtool prereg: %s\n", dp)
				}
				if lineEndingsOnly {
					// Exit 3, not 1: the freeze was not edited, this checkout's bytes were
					// converted. "Cannot establish byte identity here" is the true sentence, and
					// the one that tells the operator what to fix.
					return exitIncomplete
				}
				return exitInvariant
			}
			fmt.Fprintf(stdout, "goldtool prereg: %s is the frozen file (sha256 %s matches %s)\n",
				*checkPath, d.PreregSHA256, *digestFile)
		}
		p, err := readPrereg(*checkPath)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool prereg: %v\n", err)
			return exitUsage
		}
		if strings.TrimSpace(*digestFile) != "" {
			// The digest repeats each input's hash and row count. Compare the copy with the frozen
			// file's own records before trusting either: an edited digest used to pass silently.
			d, derr := readPreregDigest(*digestFile)
			if derr != nil {
				fmt.Fprintf(stderr, "goldtool prereg: %v\n", derr)
				return exitUsage
			}
			if aps := checkPreregDigestAgrees(d, p); len(aps) > 0 {
				for _, ap := range aps {
					fmt.Fprintf(stderr, "goldtool prereg: %s\n", ap)
				}
				return exitInvariant
			}
		}
		problems, gaps := checkPrereg(p)
		for _, g := range gaps {
			fmt.Fprintf(stderr, "goldtool prereg: cannot verify: %s\n", g)
		}
		for _, pr := range problems {
			fmt.Fprintf(stderr, "goldtool prereg: %s\n", pr)
		}
		switch {
		case len(problems) > 0:
			return exitInvariant
		case len(gaps) > 0:
			if strings.TrimSpace(*digestFile) != "" {
				// The digest has already settled "is this the frozen file"; what is left is only
				// the corpus, and the sentence says so instead of letting one exit 3 cover both.
				fmt.Fprintf(stdout, "goldtool prereg: %s is the frozen file and consistent with the code's "+
					"thresholds; %d input(s) are absent here (exit 3, corpus only)\n", *checkPath, len(gaps))
				return exitIncomplete
			}
			fmt.Fprintf(stdout, "goldtool prereg: %s is consistent with the code's thresholds, but %d input(s) "+
				"could not be hashed (exit 3)\n", *checkPath, len(gaps))
			return exitIncomplete
		}
		sum, err := hashFile(*checkPath)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool prereg: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "goldtool prereg: %s ok\n  experiment %s  frozen %s  arms %d  thresholds %d  sha256 %s\n",
			*checkPath, p.Experiment, p.FrozenAt, len(p.Arms), len(p.Thresholds), sum)
		return exitOK
	}

	experiment := strings.ToUpper(strings.TrimSpace(*exp))
	if experiment != "E1" && experiment != "E2" {
		fmt.Fprintf(stderr, "goldtool prereg: --exp must be E1 or E2 (doc/301 §9.2); E0/E3 have no judging "+
			"instrument yet, got %q\n", *exp)
		return exitUsage
	}
	if strings.TrimSpace(*out) == "" || strings.TrimSpace(*samplePath) == "" {
		fmt.Fprintln(stderr, "goldtool prereg: --out and --sample are required with --freeze")
		return exitUsage
	}
	if strings.TrimSpace(*author) == "" {
		fmt.Fprintln(stderr, "goldtool prereg: --author is required; an unsigned pre-registration has nobody to ask")
		return exitUsage
	}
	if strings.TrimSpace(*receiptPath) == "" {
		// Without the receipt the sample has no identity: the file would record 120 rows and
		// a hash, and nobody could tell which population the bounds describe. That is the
		// same failure as a moved threshold, one step earlier.
		fmt.Fprintln(stderr, "goldtool prereg: --sample-receipt is required with --freeze: the "+experiment+
			" sample has no identity without it (the receipt names sample_id and the pool it was drawn from)")
		return exitUsage
	}

	arms := defaultArms(experiment)
	if strings.TrimSpace(*armsFlag) != "" {
		labels := strings.Split(*armsFlag, ",")
		if len(labels) != len(arms) {
			fmt.Fprintf(stderr, "goldtool prereg: --arms declares %d arm(s), but doc/301 §9.2 %s has %d; "+
				"a partial arm list would look like the plan only ever had those\n", len(labels), experiment, len(arms))
			return exitUsage
		}
		for i := range arms {
			arms[i].Label = strings.TrimSpace(labels[i])
		}
	}

	p, err := buildPrereg(experiment, *samplePath, *poolPath, *receiptPath, arms, *author, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(stderr, "goldtool prereg: %v\n", err)
		return exitUsage
	}
	if problems, gaps := checkPrereg(p); len(problems) > 0 || len(gaps) > 0 {
		// A file the checker would reject must never reach disk.
		for _, g := range gaps {
			fmt.Fprintf(stderr, "goldtool prereg: %s\n", g)
		}
		for _, pr := range problems {
			fmt.Fprintf(stderr, "goldtool prereg: %s\n", pr)
		}
		return exitInvariant
	}
	if err := writePreregExclusive(*out, p); err != nil {
		fmt.Fprintf(stderr, "goldtool prereg: %v\n", err)
		if errors.Is(err, os.ErrExist) {
			return exitInvariant
		}
		return exitUsage
	}
	sum, _ := hashFile(*out)
	fmt.Fprintf(stdout, "goldtool prereg: froze %s → %s\n  sha256 %s\n  experiment %s  frozen %s\n  arms %s\n  "+
		"thresholds %d (copied from the compiled constants, each with its sentence)\n  sample %s (%d items, sample_id %s)\n  pool %s\n",
		p.Experiment, *out, sum, p.Experiment, p.FrozenAt, armSummary(p.Arms), len(p.Thresholds),
		p.Inputs[0].Path, p.Inputs[0].Items, orDash(p.SampleID), poolLabel(p.Inputs))
	return exitOK
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func poolLabel(inputs []preregInput) string {
	for _, in := range inputs {
		if in.Role == "pool" {
			return fmt.Sprintf("%s (%d items)", in.Path, in.Items)
		}
	}
	return "未登记"
}

func armSummary(arms []preregArm) string {
	parts := make([]string, 0, len(arms))
	for _, a := range arms {
		parts = append(parts, fmt.Sprintf("%s[%s,cost=%d]", a.Label, a.Method, a.CostRank))
	}
	return strings.Join(parts, " ")
}

// --- freeze --------------------------------------------------------------------------

// buildPrereg assembles the file from hashed inputs plus the compiled table. It does not
// write anything: the caller validates first, so a broken file never becomes the record.
func buildPrereg(exp, samplePath, poolPath, receiptPath string, arms []preregArm, author string, now time.Time) (preregFile, error) {
	sample, err := describeInput("sample", samplePath)
	if err != nil {
		return preregFile{}, fmt.Errorf("--sample: %w", err)
	}
	inputs := []preregInput{sample}
	if strings.TrimSpace(poolPath) != "" {
		pool, err := describeInput("pool", poolPath)
		if err != nil {
			return preregFile{}, fmt.Errorf("--pool: %w", err)
		}
		inputs = append(inputs, pool)
	}
	p := preregFile{
		Schema:      preregSchema,
		Experiment:  exp,
		FrozenAt:    now.Format(time.RFC3339),
		Author:      strings.TrimSpace(author),
		Primary:     preregPrimary(exp),
		Decision:    preregDecision(exp),
		Arms:        arms,
		Thresholds:  append([]evalThreshold(nil), evalThresholdTable...),
		Plan:        preregPlan(exp),
		Inputs:      inputs,
		Disciplines: preregDisciplines(),
		NotClaimed:  preregNotClaimed(exp),
	}
	for _, n := range p.Plan {
		p.PlanTotal += n
	}
	if strings.TrimSpace(receiptPath) != "" {
		rc, err := readSampleReceipt(receiptPath)
		if err != nil {
			return preregFile{}, fmt.Errorf("--sample-receipt: %w", err)
		}
		p.SampleID = rc.SampleID
		p.Receipt = filepath.ToSlash(receiptPath)
		// The receipt is the only independent record of *which* pool the sample came from;
		// if the pool we are about to freeze disagrees with it, one of the two files is not
		// what the sample was drawn from.
		if rc.PoolSHA256 != "" {
			for _, in := range inputs {
				if in.Role == "pool" {
					if in.SHA256 != rc.PoolSHA256 {
						return preregFile{}, fmt.Errorf("the sample receipt says its pool was %s…, but --pool %s hashes "+
							"to %s…; the sample was drawn from a different pool than the one being frozen",
							rc.PoolSHA256[:12], in.Path, in.SHA256[:12])
					}
					continue
				}
			}
			if len(inputs) == 1 {
				return preregFile{}, fmt.Errorf("the sample receipt names pool %s… but no --pool was given, so the "+
					"frame the sample was drawn from would be unrecorded", rc.PoolSHA256[:12])
			}
		}
		if rc.Selected != 0 && rc.Selected != sample.Items {
			return preregFile{}, fmt.Errorf("the sample receipt selected %d row(s) but --sample has %d; "+
				"the worksheet is not the file the receipt describes", rc.Selected, sample.Items)
		}
	}
	return p, nil
}

func describeInput(role, path string) (preregInput, error) {
	sum, err := hashFile(path)
	if err != nil {
		return preregInput{}, err
	}
	lines, err := countRows(path)
	if err != nil {
		return preregInput{}, err
	}
	return preregInput{Role: role, Path: filepath.ToSlash(path), SHA256: sum, Items: lines}, nil
}

func writePreregExclusive(path string, p preregFile) error {
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// O_EXCL is the whole point: a frozen pre-registration is append-only. Rewriting one in
	// place after seeing results would erase the only evidence that the rules predate them,
	// so a second freeze gets a new filename (`-v2`), a new date, and a diff for review.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists: a frozen pre-registration is never rewritten; "+
				"freeze a new version (-v2) so the change is visible in review: %w", path, os.ErrExist)
		}
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

// --- check ---------------------------------------------------------------------------

// checkPrereg returns violations (the file contradicts something we can prove) separately
// from gaps (an input cannot be hashed here, e.g. a fresh clone without tmp/gold). A gap is
// exit 3, never a pass: "I could not check" and "I checked and it holds" are different
// statements, and that distinction is the reason this tool exists.
func checkPrereg(p preregFile) (problems, gaps []string) {
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if p.Schema != preregSchema {
		add("schema is %q, want %q", p.Schema, preregSchema)
	}
	exp := strings.ToUpper(strings.TrimSpace(p.Experiment))
	if exp != "E1" && exp != "E2" {
		add("experiment %q is not one this tool can judge (E1/E2): freezing a rule nothing executes is the "+
			"prose problem `eval` was built to remove", p.Experiment)
	}
	if _, err := time.Parse(time.RFC3339, p.FrozenAt); err != nil {
		add("frozen_at %q is not RFC3339: an undated pre-registration cannot be shown to predate anything", p.FrozenAt)
	}
	if strings.TrimSpace(p.Author) == "" {
		add("author is empty: an unsigned pre-registration has nobody to ask")
	}

	if exp == "E1" || exp == "E2" {
		if want := preregDecision(exp); p.Decision != want {
			add("decision text does not match doc/301 §9.2 %s verbatim (quoted, never paraphrased): want %q", exp, want)
		}
		if want := preregPrimary(exp); p.Primary != want {
			add("primary_metric is %q, want %q (doc/301 §9.2)", p.Primary, want)
		}
		want := preregPlan(exp)
		total := 0
		for _, n := range p.Plan {
			total += n
		}
		if !sameIntMap(p.Plan, want) {
			add("plan is %s, want %s (doc/301 §9.%s); the sample plan is what makes the CP bounds mean anything",
				fmtIntMap(p.Plan), fmtIntMap(want), map[string]string{"E1": "1", "E2": "2"}[exp])
		}
		if p.PlanTotal != total {
			add("plan_total is %d but the plan sums to %d", p.PlanTotal, total)
		}
	}

	// Arms: the tie-break and the method mapping both live here, so a duplicate label,
	// a duplicate cost rank or a method the runtime cannot emit all make the file unusable.
	if len(p.Arms) < 2 {
		add("arms has %d entry(ies): an experiment with one arm is a demo, not a comparison (doc/301 §9.2)", len(p.Arms))
	}
	labels := map[string]bool{}
	ranks := map[int]string{}
	for i, a := range p.Arms {
		label := strings.TrimSpace(a.Label)
		if label == "" {
			add("arms[%d] has no label", i)
			continue
		}
		if labels[label] {
			add("arm %q appears twice: two arms sharing a label cannot be told apart in the receipt", label)
		}
		labels[label] = true
		if a.CostRank < 1 {
			add("arm %q has cost_rank %d; the tie-break needs a 1-based order", label, a.CostRank)
			continue
		}
		if other, dup := ranks[a.CostRank]; dup {
			add("arms %q and %q share cost_rank %d: «选成本低者» needs a strict order", other, label, a.CostRank)
		}
		ranks[a.CostRank] = label
		if !containsStr(model.ReportEvaluationMethods(), a.Method) {
			add("arm %q records method %q, which `preds --method` can never write (closed set: %s); "+
				"the arm could never be bound to a run", label, a.Method, strings.Join(model.ReportEvaluationMethods(), "/"))
		}
	}

	// Thresholds: exact triplicate match against the compiled table, both directions. A
	// missing key would silently drop a hard row from the pre-registration; an extra key
	// would be a threshold nobody judges by.
	byKey := map[string]evalThreshold{}
	for _, th := range p.Thresholds {
		if _, dup := byKey[th.Key]; dup {
			add("threshold %q appears twice", th.Key)
		}
		byKey[th.Key] = th
	}
	for _, want := range evalThresholdTable {
		got, ok := byKey[want.Key]
		if !ok {
			add("threshold %q is missing: every number `eval` judges by must be pre-registered", want.Key)
			continue
		}
		if got.Value != want.Value {
			add("threshold %q is %s in the file but %s in the code: a threshold moved without a new freeze "+
				"(want %s %s)", want.Key, fmtThreshold(got), fmtThreshold(want), want.Quote, want.Ref)
		}
		if got.Unit != want.Unit {
			add("threshold %q has unit %q, want %q", want.Key, got.Unit, want.Unit)
		}
		if got.Quote != want.Quote || got.Ref != want.Ref {
			add("threshold %q cites %q (%s), want %q (%s): the citation is part of the number",
				want.Key, got.Quote, got.Ref, want.Quote, want.Ref)
		}
		if !quoteRenders(got) {
			add("threshold %q: its quoted sentence %q does not contain its own value %s, so the prose and "+
				"the number have drifted apart", want.Key, got.Quote, fmtThreshold(got))
		}
	}
	for key := range byKey {
		if !hasThreshold(evalThresholdTable, key) {
			add("threshold %q is not one `eval` knows; an unjudged number is not a pre-registration", key)
		}
	}

	if len(p.Disciplines) != 5 {
		add("sampling_disciplines has %d entr(ies), want 5 (doc/301 §9.1.1)", len(p.Disciplines))
	} else if want := preregDisciplines(); !sameStrings(p.Disciplines, want) {
		add("sampling_disciplines differ from doc/301 §9.1.1's five rules; they are quoted, never paraphrased")
	}
	if len(p.NotClaimed) == 0 {
		add("not_claimed is empty: a pre-registration that lists nothing it cannot prove overstates itself")
	}

	// Inputs: the sample is mandatory (the metrics are computed on it), the pool is what
	// makes "equal-probability from a frozen frame" checkable.
	sawSample := false
	for i, in := range p.Inputs {
		switch in.Role {
		case "sample":
			sawSample = true
		case "pool":
		default:
			add("inputs[%d] has role %q, want sample|pool", i, in.Role)
		}
		if strings.TrimSpace(in.Path) == "" {
			add("inputs[%d] has no path", i)
			continue
		}
		if len(in.SHA256) != 64 || !isHex(in.SHA256) {
			add("inputs[%d] (%s) has no 64-hex sha256: a path without a hash pins nothing", i, in.Path)
			continue
		}
		got, err := hashFile(in.Path)
		if err != nil {
			gaps = append(gaps, fmt.Sprintf("input %s (%s) is not present here (%v); the freeze is not "+
				"contradicted, it is unverifiable on this machine", in.Role, in.Path, err))
			continue
		}
		if got != in.SHA256 {
			add("input %s (%s) hashes to %s…, but the file was frozen with %s…: the sample changed after the "+
				"rules were written", in.Role, in.Path, got[:12], in.SHA256[:12])
		}
	}
	if !sawSample {
		add("no input has role sample: the acceptance metrics have no registered denominator")
	}
	return problems, gaps
}

func readPrereg(path string) (preregFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return preregFile{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	// Strict: a typo'd field name ("threshold" for "thresholds") would otherwise be dropped
	// silently, and the file would look like it pre-registered nothing while passing.
	dec.DisallowUnknownFields()
	var p preregFile
	if err := dec.Decode(&p); err != nil {
		return preregFile{}, fmt.Errorf("%s: %w", path, err)
	}
	if dec.More() {
		return preregFile{}, fmt.Errorf("%s: trailing content after the JSON object", path)
	}
	return p, nil
}

// sampleReceiptReading is the slice of `goldtool sample`'s receipt this file depends on.
type sampleReceiptReading struct {
	SampleID   string `json:"sample_id"`
	PoolSHA256 string `json:"pool_sha256"`
	Selected   int    `json:"selected"`
}

func readSampleReceipt(path string) (sampleReceiptReading, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sampleReceiptReading{}, err
	}
	var rc sampleReceiptReading
	if err := json.Unmarshal(raw, &rc); err != nil {
		return sampleReceiptReading{}, err
	}
	if strings.TrimSpace(rc.SampleID) == "" {
		return sampleReceiptReading{}, fmt.Errorf("%s has no sample_id: the receipt is not a `goldtool sample` receipt", path)
	}
	return rc, nil
}

// --- binding (used by `eval --prereg`) -----------------------------------------------

// preregBinding is what the report records about the freeze it was judged against. Keeping
// it in the receipt means a reader of the markdown can find the file, its hash, the arm and
// the plan-vs-actual drift without trusting the person who ran the command.
type preregBinding struct {
	Path        string      `json:"path"`
	SHA256      string      `json:"sha256"`
	FrozenAt    string      `json:"frozen_at"`
	Author      string      `json:"author"`
	Experiment  string      `json:"experiment"`
	Arm         string      `json:"arm"`
	ArmMethod   string      `json:"arm_method"`
	ArmCostRank int         `json:"arm_cost_rank"`
	SampleID    string      `json:"sample_id,omitempty"`
	SampleItems int         `json:"sample_items"`
	GoldItems   int         `json:"gold_items"`
	PlanDrift   []planDrift `json:"plan_vs_actual,omitempty"`
}

type planDrift struct {
	Class   string `json:"class"`
	Planned int    `json:"planned"`
	Actual  int    `json:"actual"`
}

// bindPrereg is the `eval --prereg` half: it re-runs the whole check (a drifted or stale
// file must not be citable), then proves the run belongs to the frozen experiment — same
// experiment, a declared arm, and a gold set drawn from the frozen sample.
func bindPrereg(path, exp, method, arm string, items []Item) (*preregBinding, []string, []string, error) {
	p, err := readPrereg(path)
	if err != nil {
		return nil, nil, nil, err
	}
	problems, gaps := checkPrereg(p)
	if len(problems) > 0 {
		return nil, problems, gaps, nil
	}
	if strings.ToUpper(p.Experiment) != exp {
		problems = append(problems, fmt.Sprintf("this pre-registration is for %s, but the run is %s: two experiments "+
			"cannot share one frozen rule", p.Experiment, exp))
		return nil, problems, gaps, nil
	}

	var matches []preregArm
	for _, a := range p.Arms {
		if a.Method == method {
			matches = append(matches, a)
		}
	}
	if len(matches) == 0 {
		return nil, []string{fmt.Sprintf("method %q is not one of the pre-registered arms (%s): an arm chosen after "+
			"the numbers are in is not a comparison", method, armSummary(p.Arms))}, gaps, nil
	}
	chosen := matches[0]
	if strings.TrimSpace(arm) != "" {
		found := false
		for _, a := range matches {
			if a.Label == arm {
				chosen, found = a, true
			}
		}
		if !found {
			return nil, []string{fmt.Sprintf("arm %q is not a pre-registered arm for method %q (arms: %s)",
				arm, method, armSummary(p.Arms))}, gaps, nil
		}
	} else if len(matches) > 1 {
		return nil, []string{fmt.Sprintf("method %q matches %d pre-registered arms (%s): the runtime's method column "+
			"cannot tell them apart, so pass --arm", method, len(matches), armSummary(matches))}, gaps, nil
	}

	sample := itemIDSetOf(p, "sample")
	if sample != nil {
		var foreign []string
		for _, it := range items {
			if !sample[it.ItemID] {
				foreign = append(foreign, it.ItemID)
			}
		}
		sort.Strings(foreign)
		if len(foreign) > 0 {
			problems = append(problems, fmt.Sprintf("%d gold item(s) are not in the pre-registered sample (e.g. %s): "+
				"a claim swapped in after the freeze is exactly what the hash was for",
				len(foreign), strings.Join(cap10(foreign), ", ")))
		}
	}

	b := &preregBinding{
		Path: path, Experiment: p.Experiment, FrozenAt: p.FrozenAt, Author: p.Author,
		Arm: chosen.Label, ArmMethod: chosen.Method, ArmCostRank: chosen.CostRank,
		SampleID: p.SampleID, SampleItems: len(sample), GoldItems: len(items),
	}
	if b.SHA256, err = hashFile(path); err != nil {
		return nil, nil, nil, err
	}
	b.PlanDrift = planDriftFor(p, items)
	return b, problems, gaps, nil
}

// itemIDSetOf reads the recorded sample back and returns its item ids. When the sample is
// absent (a gap, already reported by checkPrereg) it returns nil and containment is not
// claimed either way.
func itemIDSetOf(p preregFile, role string) map[string]bool {
	for _, in := range p.Inputs {
		if in.Role != role {
			continue
		}
		items, err := readItems(in.Path)
		if err != nil {
			return nil
		}
		out := make(map[string]bool, len(items))
		for _, it := range items {
			out[it.ItemID] = true
		}
		return out
	}
	return nil
}

// planDriftFor answers "is this gold still the sample the rules were written for", as a
// per-class table. A short class is not a violation — doc/301 §9.1.1 discipline 5 says to
// disclose the actual n — but it does move every CP bound, so the report prints it next to
// the freeze instead of leaving it to be discovered.
func planDriftFor(p preregFile, items []Item) []planDrift {
	actual := map[string]int{}
	for _, it := range items {
		actual[it.GoldVerdict]++
	}
	keys := make([]string, 0, len(p.Plan)+len(actual))
	seen := map[string]bool{}
	for k := range p.Plan {
		keys, seen[k] = append(keys, k), true
	}
	for k := range actual {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []planDrift
	for _, k := range keys {
		if p.Plan[k] != actual[k] {
			out = append(out, planDrift{Class: k, Planned: p.Plan[k], Actual: actual[k]})
		}
	}
	return out
}

// --- helpers -------------------------------------------------------------------------

func hashFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func countRows(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

func fmtThreshold(th evalThreshold) string {
	if th.Unit == prUnitCount {
		return strconv.Itoa(int(th.Value))
	}
	return strconv.FormatFloat(th.Value, 'f', -1, 64)
}

// quoteRenders checks that the sentence a threshold was copied from actually contains the
// number. It accepts both ways the plan writes rates («85%» and «0.80»), because rejecting
// one of them would force the citation to be rewritten rather than the number corrected.
func quoteRenders(th evalThreshold) bool {
	if th.Unit == prUnitCount {
		return strings.Contains(th.Quote, strconv.Itoa(int(th.Value)))
	}
	if strings.Contains(th.Quote, strconv.FormatFloat(th.Value*100, 'f', -1, 64)+"%") {
		return true
	}
	for _, dec := range []string{
		strconv.FormatFloat(th.Value, 'f', 2, 64),
		strconv.FormatFloat(th.Value, 'f', -1, 64),
	} {
		if strings.Contains(th.Quote, dec) {
			return true
		}
	}
	return false
}

func hasThreshold(table []evalThreshold, key string) bool {
	for _, th := range table {
		if th.Key == key {
			return true
		}
	}
	return false
}

func sameIntMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func fmtIntMap(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ",")
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func cap10(xs []string) []string {
	if len(xs) <= 10 {
		return xs
	}
	return append(append([]string(nil), xs[:10]...), fmt.Sprintf("… (%d more)", len(xs)-10))
}
