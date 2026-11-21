package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func runPreregCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("stdout temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("stderr temp: %v", err)
	}
	defer stderr.Close()
	code := run(append([]string{"prereg"}, args...), stdout, stderr)
	outBuf, _ := os.ReadFile(stdout.Name())
	errBuf, _ := os.ReadFile(stderr.Name())
	return code, string(outBuf), string(errBuf)
}

func writeItemsFile(t *testing.T, path string, items []Item) string {
	t.Helper()
	lines := make([]string, 0, len(items))
	for _, it := range items {
		line, err := encodeItem(it)
		if err != nil {
			t.Fatalf("encode item: %v", err)
		}
		lines = append(lines, line)
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatalf("write items: %v", err)
	}
	return path
}

// preregFixture is a believable freeze: a worksheet drawn from a pool, and the sample
// receipt that records which pool. The worksheet contains the gold's item ids so binding can
// succeed; the pool is a separate file so the frame is recorded too.
type preregFixture struct {
	dir        string
	sample     string
	pool       string
	receipt    string
	gold       []Item
	sampleSize int
}

func newPreregFixture(t *testing.T, goldItems []Item, poolItems []Item) preregFixture {
	t.Helper()
	dir := t.TempDir()
	f := preregFixture{dir: dir, gold: goldItems, sampleSize: len(goldItems)}
	f.sample = writeItemsFile(t, filepath.Join(dir, "worksheet-primary-v1.jsonl"), goldItems)
	f.pool = writeItemsFile(t, filepath.Join(dir, "pool-v5.jsonl"), poolItems)
	poolSum, err := hashFile(f.pool)
	if err != nil {
		t.Fatalf("hash pool: %v", err)
	}
	rc := map[string]any{
		"sample_id":   "primary-random-20261121",
		"pool_sha256": poolSum,
		"selected":    len(goldItems),
		"role":        "primary",
		"strategy":    "random",
	}
	raw, _ := json.Marshal(rc)
	f.receipt = filepath.Join(dir, "receipt-primary-v1.json")
	if err := os.WriteFile(f.receipt, raw, 0o644); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
	return f
}

func (f preregFixture) freeze(t *testing.T, extra ...string) (string, int, string) {
	t.Helper()
	out := filepath.Join(f.dir, "prereg-e1-v1.json")
	args := []string{
		"--freeze", "--exp", "E1",
		"--sample", f.sample, "--pool", f.pool, "--sample-receipt", f.receipt,
		"--author", "测试用流程负责人", "--out", out,
	}
	args = append(args, extra...)
	code, stdout, stderr := runPreregCmd(t, args...)
	return out, code, stdout + stderr
}

func loadPrereg(t *testing.T, path string) preregFile {
	t.Helper()
	p, err := readPrereg(path)
	if err != nil {
		t.Fatalf("read prereg: %v", err)
	}
	return p
}

func writePreregFile(t *testing.T, path string, p preregFile) {
	t.Helper()
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestPreregThresholdTableIsTriplicate: every number `eval` judges by must be in the table
// with the sentence it came from, and the sentence must contain its own number. This is the
// test that fails when someone edits a constant in eval.go and forgets that the citation is
// part of the number.
func TestPreregThresholdTableIsTriplicate(t *testing.T) {
	named := map[string]float64{
		"evalUnsupportedFPUpper":      evalUnsupportedFPUpper,
		"evalAccuracyPointFloor":      evalAccuracyPointFloor,
		"evalAccuracyLowerFloor":      evalAccuracyLowerFloor,
		"evalCoverageFloorProduction": evalCoverageFloorProduction,
		"evalMacroF1Floor":            evalMacroF1Floor,
		"evalFalseCertPointCeiling":   evalFalseCertPointCeiling,
		"evalFalseCertUpperCeiling":   evalFalseCertUpperCeiling,
		"evalAbstainCeiling":          evalAbstainCeiling,
		"evalConflictingRecallFloor":  evalConflictingRecallFloor,
		"evalConflictingLowerFloor":   evalConflictingLowerFloor,
		"evalE2MisreportCeiling":      evalE2MisreportCeiling,
		"evalFalseCertUpperBudget":    evalFalseCertUpperBudget,
		"evalZeroErrorFloor":          evalZeroErrorFloor,
		"evalE2ExpansionSample":       evalE2ExpansionSample,
		"evalAccuracyFloorSample":     evalAccuracyFloorSample,
	}
	if len(evalThresholdTable) != len(named) {
		t.Fatalf("table has %d thresholds, the constants number %d: a number without a citation, or a "+
			"citation without a number", len(evalThresholdTable), len(named))
	}
	seen := map[string]bool{}
	for _, th := range evalThresholdTable {
		if seen[th.Key] {
			t.Fatalf("threshold %q appears twice", th.Key)
		}
		seen[th.Key] = true
		want, ok := named[th.Key]
		if !ok {
			t.Fatalf("threshold %q has no matching constant in eval.go", th.Key)
		}
		if th.Value != want {
			t.Fatalf("%s: table says %v, constant says %v — the pre-registration and the judge disagree",
				th.Key, th.Value, want)
		}
		if th.Unit != prUnitRate && th.Unit != prUnitCount {
			t.Fatalf("%s: unit %q is neither rate nor count", th.Key, th.Unit)
		}
		if strings.TrimSpace(th.Ref) == "" {
			t.Fatalf("%s: no section reference", th.Key)
		}
		if !quoteRenders(th) {
			t.Fatalf("%s: quote %q does not contain its own value %s", th.Key, th.Quote, fmtThreshold(th))
		}
	}
	for key := range named {
		if !hasThreshold(evalThresholdTable, key) {
			t.Fatalf("constant %s is judged by eval but not pre-registered", key)
		}
	}
}

// TestPreregFreezeWritesAFrozenFile: the file is the deliverable, so its every machine-checked
// field is asserted rather than eyeballed.
func TestPreregFreezeWritesAFrozenFile(t *testing.T) {
	gold := evalGold(t)
	pool := append(append([]Item(nil), gold...), sampleItem(9999, model.ReportVerdictSupported))
	f := newPreregFixture(t, gold, pool)

	path, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze exit = %d, want 0\n%s", code, out)
	}
	p := loadPrereg(t, path)

	if p.Schema != preregSchema {
		t.Fatalf("schema = %q", p.Schema)
	}
	if p.Experiment != "E1" {
		t.Fatalf("experiment = %q", p.Experiment)
	}
	if _, err := time.Parse(time.RFC3339, p.FrozenAt); err != nil {
		t.Fatalf("frozen_at %q is not RFC3339: %v", p.FrozenAt, err)
	}
	if p.Author == "" {
		t.Fatal("author is empty")
	}
	if p.Primary != "unsupported_false_positive_rate" {
		t.Fatalf("primary_metric = %q", p.Primary)
	}
	if p.Decision != preregDecision("E1") {
		t.Fatal("decision text is not doc/301 §9.2 E1 verbatim")
	}
	if len(p.Arms) != 4 || p.Arms[0].Label != "rule-v1" || p.Arms[3].Label != "single-model+quote+rule" {
		t.Fatalf("arms = %s", armSummary(p.Arms))
	}
	if p.Arms[1].Method != model.ReportEvaluationMethodSemantic || p.Arms[3].Method != model.ReportEvaluationMethodSemantic {
		t.Fatalf("the two model arms must both record semantic (the runtime cannot separate them): %s", armSummary(p.Arms))
	}
	if len(p.Thresholds) != len(evalThresholdTable) {
		t.Fatalf("thresholds = %d, want %d", len(p.Thresholds), len(evalThresholdTable))
	}
	if !sameIntMap(p.Plan, GoldDistribution) || p.PlanTotal != GoldSize() {
		t.Fatalf("plan = %s total %d", fmtIntMap(p.Plan), p.PlanTotal)
	}
	if p.SampleID != "primary-random-20261121" {
		t.Fatalf("sample_id = %q", p.SampleID)
	}
	if len(p.Disciplines) != 5 {
		t.Fatalf("disciplines = %d, want 5", len(p.Disciplines))
	}
	if len(p.NotClaimed) == 0 {
		t.Fatal("not_claimed is empty")
	}
	if len(p.Inputs) != 2 || p.Inputs[0].Role != "sample" || p.Inputs[1].Role != "pool" {
		t.Fatalf("inputs = %+v", p.Inputs)
	}
	sampleSum, err := hashFile(f.sample)
	if err != nil {
		t.Fatalf("hash sample: %v", err)
	}
	if p.Inputs[0].SHA256 != sampleSum {
		t.Fatalf("sample sha256 = %s, want %s", p.Inputs[0].SHA256, sampleSum)
	}
	if p.Inputs[0].Items != len(gold) {
		t.Fatalf("sample items = %d, want %d", p.Inputs[0].Items, len(gold))
	}

	// The freeze reports its own hash, and --check accepts it.
	if !strings.Contains(out, "thresholds 15") {
		t.Fatalf("freeze output does not state the threshold count:\n%s", out)
	}
	code, _, checkOut := runPreregCmd(t, "--check", path)
	if code != exitOK {
		t.Fatalf("check exit = %d, want 0\n%s", code, checkOut)
	}
}

// TestPreregRefusesToRewriteAFrozenFile: the O_EXCL write is the evidence that the rules
// predate the results. A second freeze must produce a new file, not a new content.
func TestPreregRefusesToRewriteAFrozenFile(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	path, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen file: %v", err)
	}
	_, second, secondOut := f.freeze(t)
	if second != exitInvariant {
		t.Fatalf("second freeze exit = %d, want 1\n%s", second, secondOut)
	}
	if !strings.Contains(secondOut, "never rewritten") {
		t.Fatalf("refusal does not name the rule:\n%s", secondOut)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen file: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("the frozen file changed: the original freeze is the evidence")
	}
}

// TestPreregFreezeFlagDiscipline: an unterminated freeze is worse than none, so every required
// input and both closed-set choices are refused loudly.
func TestPreregFreezeFlagDiscipline(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no mode", []string{"--exp", "E1"}, "exactly one of --freeze or --check"},
		{"both modes", []string{"--freeze", "--check", "x.json"}, "exactly one of --freeze or --check"},
		{"E0 is not judgeable", []string{"--freeze", "--exp", "E0", "--sample", f.sample, "--author", "x",
			"--out", filepath.Join(f.dir, "e0.json")}, "no judging"},
		{"no author", []string{"--freeze", "--exp", "E1", "--sample", f.sample,
			"--out", filepath.Join(f.dir, "x.json")}, "--author is required"},
		{"no sample", []string{"--freeze", "--exp", "E1", "--author", "x",
			"--out", filepath.Join(f.dir, "x.json")}, "--out and --sample are required"},
		{"no receipt", []string{"--freeze", "--exp", "E1", "--sample", f.sample, "--author", "x",
			"--out", filepath.Join(f.dir, "x.json")}, "has no identity"},
		{"partial arms", []string{"--freeze", "--exp", "E1", "--sample", f.sample, "--author", "x",
			"--sample-receipt", f.receipt, "--arms", "rule-v1", "--out", filepath.Join(f.dir, "x.json")},
			"partial arm list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, out := runPreregCmd(t, c.args...)
			if code != exitUsage {
				t.Fatalf("exit = %d, want 2\n%s", code, out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("message does not mention %q:\n%s", c.want, out)
			}
		})
	}
}

// TestPreregFreezeCrossChecksTheSampleReceipt: the receipt is the only independent record of
// which pool the sample came from. Freezing a different pool would make the frame a claim.
func TestPreregFreezeCrossChecksTheSampleReceipt(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	other := writeItemsFile(t, filepath.Join(f.dir, "pool-other.jsonl"), append(gold, sampleItem(7777, model.ReportVerdictSupported)))

	code, _, out := runPreregCmd(t, "--freeze", "--exp", "E1", "--sample", f.sample, "--pool", other,
		"--sample-receipt", f.receipt, "--author", "x", "--out", filepath.Join(f.dir, "x.json"))
	if code != exitUsage {
		t.Fatalf("exit = %d, want 2 (a different pool is a usage error, not a violation)\n%s", code, out)
	}
	if !strings.Contains(out, "drawn from a different pool") {
		t.Fatalf("message does not name the mismatch:\n%s", out)
	}

	code, _, out = runPreregCmd(t, "--freeze", "--exp", "E1", "--sample", f.sample,
		"--sample-receipt", f.receipt, "--author", "x", "--out", filepath.Join(f.dir, "y.json"))
	if code != exitUsage {
		t.Fatalf("exit = %d, want 2 (a receipt naming a pool without --pool)\n%s", code, out)
	}
	if !strings.Contains(out, "unrecorded") {
		t.Fatalf("message does not explain the gap:\n%s", out)
	}
}

// TestPreregCheckCatchesEveryDrift: one subtest per promise the file makes. Each mutation is a
// single field, so a failure names the field that broke rather than "invalid file".
func TestPreregCheckCatchesEveryDrift(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	path, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	base := loadPrereg(t, path)

	mutate := func(t *testing.T, fn func(*preregFile)) string {
		t.Helper()
		p := loadPrereg(t, path)
		fn(&p)
		mut := filepath.Join(t.TempDir(), "mutated.json")
		writePreregFile(t, mut, p)
		return mut
	}

	cases := []struct {
		name string
		fn   func(*preregFile)
		want string
	}{
		{"schema", func(p *preregFile) { p.Schema = "rgx.goldtool.prereg/0" }, "schema is"},
		{"experiment", func(p *preregFile) { p.Experiment = "E3" }, "not one this tool can judge"},
		{"frozen_at", func(p *preregFile) { p.FrozenAt = "yesterday" }, "not RFC3339"},
		{"author", func(p *preregFile) { p.Author = " " }, "author is empty"},
		{"decision", func(p *preregFile) { p.Decision = "主指标看起来不错就行" }, "verbatim"},
		{"primary", func(p *preregFile) { p.Primary = "abstain_rate" }, "primary_metric"},
		{"plan", func(p *preregFile) { p.Plan["unsupported"] = 30; p.PlanTotal = GoldSize() - 10 }, "plan is"},
		{"plan_total", func(p *preregFile) { p.PlanTotal = 119 }, "plan_total"},
		{"one arm", func(p *preregFile) { p.Arms = p.Arms[:1] }, "not a comparison"},
		{"duplicate label", func(p *preregFile) { p.Arms[1].Label = p.Arms[0].Label }, "appears twice"},
		{"duplicate rank", func(p *preregFile) { p.Arms[1].CostRank = p.Arms[0].CostRank }, "share cost_rank"},
		{"unknown method", func(p *preregFile) { p.Arms[1].Method = "gpt" }, "can never write"},
		{"missing threshold", func(p *preregFile) { p.Thresholds = p.Thresholds[:len(p.Thresholds)-1] }, "is missing"},
		{"moved threshold", func(p *preregFile) { p.Thresholds[1].Value = 0.80 }, "moved without a new freeze"},
		{"extra threshold", func(p *preregFile) {
			p.Thresholds = append(p.Thresholds, evalThreshold{Key: "evalVibes", Unit: prUnitRate, Value: 0.9,
				Quote: "90%", Ref: "hr"})
		}, "not one `eval` knows"},
		{"paraphrased citation", func(p *preregFile) { p.Thresholds[0].Quote = "上界不超过一成" }, "citation is part of the number"},
		{"unit", func(p *preregFile) { p.Thresholds[0].Unit = prUnitCount }, "unit"},
		{"disciplines count", func(p *preregFile) { p.Disciplines = p.Disciplines[:4] }, "want 5"},
		{"disciplines text", func(p *preregFile) { p.Disciplines[0] = "抽样随意即可" }, "quoted, never paraphrased"},
		{"not_claimed", func(p *preregFile) { p.NotClaimed = nil }, "overstates itself"},
		{"no sample role", func(p *preregFile) { p.Inputs[0].Role = "pool" }, "no input has role sample"},
		{"bad sha", func(p *preregFile) { p.Inputs[0].SHA256 = "abc" }, "64-hex"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mut := mutate(t, c.fn)
			code, _, out := runPreregCmd(t, "--check", mut)
			if code != exitInvariant {
				t.Fatalf("exit = %d, want 1\n%s", code, out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("message does not mention %q:\n%s", c.want, out)
			}
		})
	}

	// The untouched file still passes: the mutations above are the only difference.
	if problems, gaps := checkPrereg(base); len(problems) > 0 || len(gaps) > 0 {
		t.Fatalf("the control file fails its own check: %v %v", problems, gaps)
	}
}

// TestPreregCheckStrictDecodeRefusesAnUnknownField: a typo'd field name would otherwise be
// dropped silently and the file would look like it pre-registered nothing while passing.
func TestPreregCheckStrictDecodeRefusesAnUnknownField(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	path, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	broken := strings.Replace(string(raw), `"thresholds": [`, `"threshold": [`, 1)
	if broken == string(raw) {
		t.Fatal("mutation did not apply")
	}
	bad := filepath.Join(t.TempDir(), "typo.json")
	if err := os.WriteFile(bad, []byte(broken), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, out = runPreregCmd(t, "--check", bad)
	if code != exitUsage {
		t.Fatalf("exit = %d, want 2 (a file that cannot be read is not a violation)\n%s", code, out)
	}
	if !strings.Contains(out, "unknown field") {
		t.Fatalf("message does not name the unknown field:\n%s", out)
	}
}

// TestPreregCheckSeparatesUnverifiableFromViolated: on a machine without the sample (a fresh
// clone: tmp/gold is gitignored) the check cannot prove the hash, and that is exit 3 — not a
// pass, and not a violation. "I could not check" is not "it holds".
func TestPreregCheckSeparatesUnverifiableFromViolated(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	path, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}

	if err := os.Remove(f.pool); err != nil {
		t.Fatalf("remove pool: %v", err)
	}
	code, stdout, out := runPreregCmd(t, "--check", path)
	if code != exitIncomplete {
		t.Fatalf("missing input exit = %d, want 3\n%s%s", code, stdout, out)
	}
	if !strings.Contains(out, "not present here") {
		t.Fatalf("message does not distinguish absence from violation:\n%s", out)
	}

	// A changed sample is a violation, not a gap.
	f2 := newPreregFixture(t, gold, gold)
	path2, code, out := f2.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	withExtra := append([]Item(nil), gold...)
	withExtra = append(withExtra, sampleItem(4242, model.ReportVerdictOutdated))
	writeItemsFile(t, f2.sample, withExtra)
	code, _, out = runPreregCmd(t, "--check", path2)
	if code != exitInvariant {
		t.Fatalf("changed sample exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "changed after the rules were written") {
		t.Fatalf("message does not name the hazard:\n%s", out)
	}
}

// TestEvalBindsPreregToTheFrozenRun: with a freeze in hand the report names it, and the three
// things a post-hoc arm choice would hide are refused.
func TestEvalBindsPreregToTheFrozenRun(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	preregPath, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))

	// Control: bound, one method with one arm, everything green.
	reportPath := filepath.Join(t.TempDir(), "report.md")
	code, _, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodRule, "--arm", "rule-v1",
		"--prereg", preregPath, "--out", reportPath)
	if code != exitOK {
		t.Fatalf("bound control exit = %d, want 0\n%s", code, stderr)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	sum, err := hashFile(preregPath)
	if err != nil {
		t.Fatalf("hash prereg: %v", err)
	}
	for _, want := range []string{"预注册：`" + preregPath + "`", sum, "**rule-v1**", "cost_rank 1", "primary-random-20261121"} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("report does not contain %q:\n%s", want, report)
		}
	}

	// A method that was never an arm cannot be compared to the arms.
	code, _, stderr = runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodHuman, "--prereg", preregPath)
	if code != exitInvariant || !strings.Contains(stderr, "not one of the pre-registered arms") {
		t.Fatalf("unregistered method: exit = %d\n%s", code, stderr)
	}

	// Two arms share `semantic`, so the runtime's method column is not enough.
	code, _, stderr = runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodSemantic, "--prereg", preregPath)
	if code != exitInvariant || !strings.Contains(stderr, "cannot tell them apart, so pass --arm") {
		t.Fatalf("ambiguous arm: exit = %d\n%s", code, stderr)
	}

	// An arm that does not belong to the method is a mismatch, not a synonym.
	code, _, stderr = runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodCross, "--arm", "rule-v1", "--prereg", preregPath)
	if code != exitInvariant || !strings.Contains(stderr, "is not a pre-registered arm for method") {
		t.Fatalf("arm/method mismatch: exit = %d\n%s", code, stderr)
	}

	// Wrong experiment.
	code, _, stderr = runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E2",
		"--method", model.ReportEvaluationMethodRule, "--arm", "rule-v1", "--prereg", preregPath)
	if code != exitInvariant || !strings.Contains(stderr, "cannot share one frozen rule") {
		t.Fatalf("wrong experiment: exit = %d\n%s", code, stderr)
	}

	// --arm without --prereg is a nickname, not an arm.
	code, _, stderr = runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodRule, "--arm", "rule-v1")
	if code != exitUsage || !strings.Contains(stderr, "is a nickname") {
		t.Fatalf("arm without prereg: exit = %d\n%s", code, stderr)
	}
}

// TestEvalRefusesAGoldItemOutsideTheFrozenSample: the hash exists to make "a claim swapped in
// after the freeze" impossible to hide, so one foreign item is enough to stop the run.
func TestEvalRefusesAGoldItemOutsideTheFrozenSample(t *testing.T) {
	full := evalGold(t)
	sub := full[:10]
	f := newPreregFixture(t, sub, full)
	preregPath, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	goldPath, predPath := writeEvalFiles(t, full, perfectPredictions(full))

	code, _, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodRule, "--arm", "rule-v1", "--prereg", preregPath)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "are not in the pre-registered sample") {
		t.Fatalf("message does not name the swap:\n%s", stderr)
	}
	if !strings.Contains(stderr, "110 gold item(s)") {
		t.Fatalf("message does not count the offending items:\n%s", stderr)
	}
}

// TestEvalCannotClaimAPassWhenTheFreezeIsUnverifiable: rows pass, but the sample is gone — the
// verdict stays `pass` (the rows did pass) while the run exits 3, because the binding it cites
// could not be proven here.
func TestEvalCannotClaimAPassWhenTheFreezeIsUnverifiable(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	preregPath, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))

	// First with the sample present: the same inputs reach `pass` and exit 0, so whatever the
	// next step reports is attributable to the disappearance and nothing else.
	if code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodRule, "--arm", "rule-v1", "--prereg", preregPath); code != exitOK {
		t.Fatalf("control exit = %d, want 0\n%s\n%s", code, stderr, stdout)
	}
	if err := os.Remove(f.sample); err != nil {
		t.Fatalf("remove sample: %v", err)
	}

	code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodRule, "--arm", "rule-v1", "--prereg", preregPath)
	if code != exitIncomplete {
		t.Fatalf("exit = %d, want 3\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "cannot verify") {
		t.Fatalf("message does not say what could not be verified:\n%s", stderr)
	}
	// The rows themselves still pass; the run is not renamed a failure, it is denied the exit
	// code that would let a script treat it as certified.
	if !strings.Contains(stdout, "判定：**pass**") {
		t.Fatalf("the row verdict should stay `pass` while the run exits 3:\n%s", stdout)
	}
}

// TestEvalWithoutPreregSaysSo: an unbound run may still be judged, but it must not read as if it
// followed a frozen rule.
func TestEvalWithoutPreregSaysSo(t *testing.T) {
	gold := evalGold(t)
	goldPath, predPath := writeEvalFiles(t, gold, perfectPredictions(gold))
	code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodRule)
	if code != exitOK {
		t.Fatalf("exit = %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "no --prereg") {
		t.Fatalf("stderr does not disclose the missing freeze:\n%s", stderr)
	}
	if !strings.Contains(stdout, "预注册：**未引用**") {
		t.Fatalf("report does not disclose the missing freeze:\n%s", stdout)
	}
}

// TestEvalPrintsPlanDriftAboveTheTable: a short class is disclosed where a reader cannot miss
// it, because it moves every CP bound below.
func TestEvalPrintsPlanDriftAboveTheTable(t *testing.T) {
	gold := evalGold(t)
	short := make([]Item, 0, len(gold))
	for _, it := range gold {
		if it.GoldVerdict == model.ReportVerdictUnsupported && len(short)%7 == 0 {
			continue // thin the unsupported class without touching the others
		}
		short = append(short, it)
	}
	f := newPreregFixture(t, gold, gold)
	preregPath, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("freeze: %d\n%s", code, out)
	}
	goldPath, predPath := writeEvalFiles(t, short, perfectPredictions(short))

	code, stdout, stderr := runEvalCmd(t, "--gold", goldPath, "--pred", predPath, "--exp", "E1",
		"--method", model.ReportEvaluationMethodRule, "--arm", "rule-v1", "--prereg", preregPath)
	// The shrunken class is not a *binding* violation (the sample still contains every gold
	// item); it fails the hard row on its own merits, because 0/n can no longer clear a 10%
	// upper bound. Both facts are asserted, so neither can hide behind the other.
	if strings.Contains(stderr, "--prereg:") {
		t.Fatalf("a shorter class must not be reported as a binding violation:\n%s", stderr)
	}
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1 (the CP upper bound cannot clear 10%% at this n)\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "计划 n") || !strings.Contains(stdout, "| unsupported | 40 |") {
		t.Fatalf("report does not print the plan drift:\n%s", stdout)
	}
	if !strings.Contains(stdout, "不补样本、不改抽样") {
		t.Fatalf("report does not state the discipline:\n%s", stdout)
	}
}

// TestPreregE2FreezesTheCandidateFrame: E2's arms are scan scopes, not evaluation methods, so
// both record `deterministic` and `--arm` is mandatory for it too.
func TestPreregE2FreezesTheCandidateFrame(t *testing.T) {
	gold := evalGold(t)
	f := newPreregFixture(t, gold, gold)
	out := filepath.Join(f.dir, "prereg-e2-v1.json")
	code, _, msg := runPreregCmd(t, "--freeze", "--exp", "E2", "--sample", f.sample, "--pool", f.pool,
		"--sample-receipt", f.receipt, "--author", "x", "--out", out)
	if code != exitOK {
		t.Fatalf("freeze E2 exit = %d\n%s", code, msg)
	}
	p := loadPrereg(t, out)
	if p.Primary != "conflicting_recall" {
		t.Fatalf("E2 primary = %q", p.Primary)
	}
	if !sameIntMap(p.Plan, map[string]int{"conflicting_claims": 24, "sections": 30}) {
		t.Fatalf("E2 plan = %s", fmtIntMap(p.Plan))
	}
	if len(p.Arms) != 2 || p.Arms[0].Label != "cited_only" || p.Arms[1].Label != "cited+candidate:1" {
		t.Fatalf("E2 arms = %s", armSummary(p.Arms))
	}
	if code, _, msg = runPreregCmd(t, "--check", out); code != exitOK {
		t.Fatalf("check E2 exit = %d\n%s", code, msg)
	}
}

// TestPreregQuoteRendersBothNotations keeps the citation check honest: the plan writes rates as
// «85%» in §4.2 and as «0.80» for macro-F1, so both must count as "the sentence contains the
// number".
func TestPreregQuoteRendersBothNotations(t *testing.T) {
	cases := []struct {
		th   evalThreshold
		want bool
	}{
		{evalThreshold{Unit: prUnitRate, Value: 0.85, Quote: "点估计 ≥85%"}, true},
		{evalThreshold{Unit: prUnitRate, Value: 0.80, Quote: "macro_f1_core ≥0.80"}, true},
		{evalThreshold{Unit: prUnitRate, Value: 0.80, Quote: "macro_f1_core ≥0.8"}, true},
		{evalThreshold{Unit: prUnitRate, Value: 0.10, Quote: "上界 ≤1%"}, false},
		{evalThreshold{Unit: prUnitCount, Value: 72, Quote: "分母 ≥72"}, true},
		{evalThreshold{Unit: prUnitCount, Value: 72, Quote: "分母 ≥36"}, false},
	}
	for _, c := range cases {
		if got := quoteRenders(c.th); got != c.want {
			t.Fatalf("quoteRenders(%s, %q) = %v, want %v", fmtThreshold(c.th), c.th.Quote, got, c.want)
		}
	}
}
