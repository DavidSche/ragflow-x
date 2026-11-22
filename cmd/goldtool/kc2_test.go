package main

// Tests for `goldtool kc2` (task T11, doc/301 §5.7).
//
// Three properties matter more than the arithmetic, and each one is a test here:
//
//   - a pre-registration can be written once and never edited into agreement with the
//     compiled rule (the checker is bidirectional, and every drift gets its own sentence);
//   - `eval` cannot be run without a frozen file at all — "we ran the experiment" and "we
//     ran it as registered" are different claims;
//   - `collect`'s pure half counts a hint as *used* only when a hinted document's raw chunk
//     was actually cited, because that is the definition of `locator_hit_rate`.
//
// The frozen file under internal/service/testdata/gold/ is checked by name in the last
// test: `go test ./cmd/goldtool` is what keeps a committed pre-registration from silently
// disagreeing with the code.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/service"
)

func runKC2Cmd(t *testing.T, args ...string) (int, string, string) {
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
	code := run(append([]string{"kc2"}, args...), stdout, stderr)
	outBuf, _ := os.ReadFile(stdout.Name())
	errBuf, _ := os.ReadFile(stderr.Name())
	return code, string(outBuf), string(errBuf)
}

// kc2CommittedPrereg is the frozen file this repository ships. KC2_PREREG_FILE exists for
// scripts/release-gate/kc2-prereg.sh --selftest, which has to point the same check at a
// drifted copy without editing a tracked file.
const kc2CommittedPrereg = "internal/service/testdata/gold/prereg-kc2-v1.json"

func kc2PreregPath() string {
	if p := os.Getenv("KC2_PREREG_FILE"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "internal", "service", "testdata", "gold", "prereg-kc2-v1.json")
}

// kc2TestClock is the freeze time used by the drift table: the checker does not judge
// wall-clock freshness (it cannot), so a fixed value keeps the mutation messages stable.
var kc2TestClock = time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)

func kc2TestSections(n, controlCovered, treatmentCovered int) []service.KC2Section {
	out := make([]service.KC2Section, 0, 2*n)
	for i := 1; i <= n; i++ {
		key := "sec-" + strconv.Itoa(i)
		out = append(out,
			service.KC2Section{SectionKey: key, Ord: i, Arm: service.KC2ArmControl,
				Covered: i <= controlCovered, Citations: 2, Traceable: 2, Tokens: 1000, LatencyMS: 100},
			service.KC2Section{SectionKey: key, Ord: i, Arm: service.KC2ArmTreatment,
				Covered: i <= treatmentCovered, Citations: 2, Traceable: 2,
				Hints: 1, HintUsed: i <= treatmentCovered, Tokens: 1200, LatencyMS: 110})
	}
	return out
}

// TestKC2PreregIsFrozenOnce covers the freeze → check → refuse-to-overwrite loop. A
// pre-registration that can be rewritten in place is a note, not a freeze.
func TestKC2PreregIsFrozenOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prereg-kc2-v1.json")

	code, out, errOut := runKC2Cmd(t, "prereg", "--freeze", "--author", "test", "--out", path)
	if code != exitOK {
		t.Fatalf("freeze exit = %d, want 0 (%s%s)", code, out, errOut)
	}
	if !strings.Contains(out, "thresholds 3") || !strings.Contains(out, "plan 30 sections/arm") {
		t.Fatalf("freeze output does not name what was frozen:\n%s", out)
	}

	// The same path again must be refused, not overwritten.
	code, _, errOut = runKC2Cmd(t, "prereg", "--freeze", "--author", "test", "--out", path)
	if code != exitInvariant {
		t.Fatalf("second freeze exit = %d, want %d (refusing to overwrite)", code, exitInvariant)
	}
	if !strings.Contains(errOut, "exist") {
		t.Fatalf("second freeze must say the file exists, got:\n%s", errOut)
	}

	// --check prints the three rows the gate script greps, and the plan quantity.
	code, out, errOut = runKC2Cmd(t, "prereg", "--check", path)
	if code != exitOK {
		t.Fatalf("check exit = %d, want 0 (%s)", code, errOut)
	}
	for _, want := range []string{
		"kc2-quote: lift ≥10pp",
		"kc2-quote: 原文可回溯率 **100%**",
		"kc2-quote: 成本增幅 ≤30%",
		"kc2-decision: ",
		"kc2-plan: sections_per_arm=30",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--check output is missing %q:\n%s", want, out)
		}
	}
}

// TestKC2PreregCheckNamesEveryDrift: ten ways to make the file disagree with the compiled
// rule, ten sentences that name the field. A checker that only says "invalid" would leave
// a reader editing blind.
func TestKC2PreregCheckNamesEveryDrift(t *testing.T) {
	base := buildKC2Prereg("test", kc2TestClock)
	cases := []struct {
		name   string
		mutate func(*kc2PreregFile)
		want   string
	}{
		{"lift floor edited down", func(p *kc2PreregFile) { p.Thresholds[0].Value = 5 }, "kc2LiftFloorPP = 5"},
		{"traceability floor edited", func(p *kc2PreregFile) { p.Thresholds[1].Value = 0.95 }, "kc2TraceabilityFloor = 0.95"},
		{"cost ceiling relaxed", func(p *kc2PreregFile) { p.Thresholds[2].Value = 0.5 }, "kc2CostDeltaCeiling = 0.5"},
		{"quote paraphrased", func(p *kc2PreregFile) { p.Thresholds[0].Quote = "lift 有明显提升" }, "quote = \"lift 有明显提升\""},
		{"threshold dropped", func(p *kc2PreregFile) { p.Thresholds = p.Thresholds[:2] }, "declares 2 threshold(s)"},
		{"plan lowered", func(p *kc2PreregFile) { p.Plan.SectionsPerArm = 12 }, "plan.sections_per_arm = 12"},
		{"arm renamed", func(p *kc2PreregFile) { p.Arms[1].Name = "compiled" }, `arm[1].name = "compiled"`},
		{"decision paraphrased", func(p *kc2PreregFile) { p.Decision = "效果显著就打开" }, "not doc/301 §5.7's sentence verbatim"},
		{"caveats stripped", func(p *kc2PreregFile) { p.NotClaimed = nil }, "not_claimed is empty"},
		{"schema from another tool", func(p *kc2PreregFile) { p.Schema = "rgx.goldtool.prereg/1" }, "schema ="},
		{"unsigned", func(p *kc2PreregFile) { p.Author = " " }, "author is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			p.Thresholds = append([]kc2Threshold(nil), base.Thresholds...)
			p.Arms = append([]kc2ArmDef(nil), base.Arms...)
			tc.mutate(&p)
			problems := checkKC2Prereg(p)
			if len(problems) == 0 {
				t.Fatalf("mutation %q was not caught", tc.name)
			}
			for _, pr := range problems {
				if strings.Contains(pr, tc.want) {
					return
				}
			}
			t.Fatalf("no problem mentions %q; got:\n%s", tc.want, strings.Join(problems, "\n"))
		})
	}
}

// TestKC2EvalRequiresTheFrozenPrereg: without --prereg the tool refuses to judge, because
// a verdict computed against no registered rule cannot be compared with "the experiment".
func TestKC2EvalRequiresTheFrozenPrereg(t *testing.T) {
	items := filepath.Join(t.TempDir(), "items.jsonl")
	if err := writeKC2Items(items, kc2TestSections(30, 18, 25)); err != nil {
		t.Fatalf("write items: %v", err)
	}
	code, _, errOut := runKC2Cmd(t, "eval", "--items", items)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d without --prereg", code, exitUsage)
	}
	if !strings.Contains(errOut, "§5.7") || !strings.Contains(errOut, "--prereg is required") {
		t.Fatalf("the refusal must name the rule it is missing:\n%s", errOut)
	}
}

// TestKC2EvalExitCodes: 0 allowed, 1 refused, 3 unverified. The middle one is a result and
// the last one is the absence of one; a tool that returned 0 for either would be lying.
func TestKC2EvalExitCodes(t *testing.T) {
	frozen := kc2PreregPath()
	dir := t.TempDir()
	write := func(name string, sections []service.KC2Section) string {
		path := filepath.Join(dir, name)
		if err := writeKC2Items(path, sections); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	cases := []struct {
		name  string
		items string
		want  int
		body  string
	}{
		{"all three met", write("pass.jsonl", kc2TestSections(30, 18, 25)), exitOK, "`allowed`"},
		{"lift below floor", write("fail.jsonl", kc2TestSections(30, 18, 20)), exitInvariant, "`refused`"},
		{"too few sections", write("short.jsonl", kc2TestSections(12, 3, 12)), exitIncomplete, "`unverified`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runKC2Cmd(t, "eval", "--items", tc.items, "--prereg", frozen)
			if code != tc.want {
				t.Fatalf("exit = %d, want %d\n%s%s", code, tc.want, out, errOut)
			}
			if !strings.Contains(out, tc.body) {
				t.Fatalf("output does not contain %q:\n%s", tc.body, out)
			}
			// Every verdict must print its rows with denominators, not just the word.
			for _, row := range []string{"sample_size", "evidence_coverage", "raw_traceability", "cost_delta_tokens"} {
				if !strings.Contains(out, "| "+row+" |") {
					t.Fatalf("row %s missing from the table:\n%s", row, out)
				}
			}
		})
	}
}

// TestKC2CollectCountsAHintAsUsedOnlyWhenItsChunkWasCited: `locator_hit_rate`'s numerator is
// "a hinted document's raw chunk was actually adopted", not "a hint existed".
func TestKC2CollectCountsAHintAsUsedOnlyWhenItsChunkWasCited(t *testing.T) {
	control := []kc2SectionRow{
		{Ord: 1, Key: "sec-1", Citations: 2, Traceable: 2, CitedDocs: []string{"doc-a"}, Tokens: 900},
		{Ord: 2, Key: "sec-2", Citations: 0, Traceable: 0, Tokens: 800},
	}
	treatment := []kc2SectionRow{
		// Hint points at doc-b, but the section cited doc-a: the hint was available and not used.
		{Ord: 1, Key: "sec-1", Citations: 3, Traceable: 3, CitedDocs: []string{"doc-a", "doc-b"},
			HintDocs: []string{"doc-b"}, Tokens: 1100},
		// Hint points at doc-c, and doc-c was cited: used.
		{Ord: 2, Key: "sec-2", Citations: 1, Traceable: 1, CitedDocs: []string{"doc-c"},
			HintDocs: []string{"doc-c"}, Tokens: 1000},
		// A hint that pointed somewhere the retrieval did not go: available, not used. This
		// is the case that separates "had a hint" from "the hint helped".
		{Ord: 3, Key: "sec-3", Citations: 2, Traceable: 2, CitedDocs: []string{"doc-a"},
			HintDocs: []string{"doc-d"}, Tokens: 1000},
	}
	records := kc2Records(control, treatment)
	if len(records) != 5 {
		t.Fatalf("want 5 records, got %d", len(records))
	}
	// records[0..1] are control, records[2..3] treatment (arm-major on purpose, so a diff
	// between two collects reads as one block per arm).
	if !records[2].HintUsed || !records[3].HintUsed {
		t.Fatalf("a cited hinted document must count as used: %+v %+v", records[2], records[3])
	}
	if records[1].Covered {
		t.Fatalf("a section with no traceable citation is not covered: %+v", records[1])
	}
	if records[4].HintUsed {
		t.Fatalf("a hint whose document was never cited is not a hit: %+v", records[4])
	}
	if records[3].Hints != 1 {
		t.Fatalf("hints counts the documents a locator points at, got %d", records[3].Hints)
	}
	if records[0].HintUsed || records[0].Hints != 0 {
		t.Fatalf("control must not carry hint state: %+v", records[0])
	}
}

// TestFrozenKC2PreregMatchesTheCompiledRule is the pin `go test ./cmd/goldtool` provides for
// the committed file: a threshold edited in Go without re-freezing turns this red, and so
// does a hand-edited pre-registration.
func TestFrozenKC2PreregMatchesTheCompiledRule(t *testing.T) {
	p, err := readKC2Prereg(kc2PreregPath())
	if err != nil {
		t.Fatalf("read the committed pre-registration: %v", err)
	}
	if problems := checkKC2Prereg(p); len(problems) > 0 {
		t.Fatalf("the committed pre-registration disagrees with the compiled rule:\n%s", strings.Join(problems, "\n"))
	}
	if p.Experiment != "KC-2" || len(p.Thresholds) != 3 || p.Plan.SectionsPerArm != service.KC2PlanSectionsPerArm {
		t.Fatalf("unexpected frozen file: %+v", p)
	}
}

// TestTheFrozenKC2PreregPrintsForTheGateScript gives scripts/release-gate/kc2-prereg.sh the
// three quotes and the plan quantity without a second copy of them in bash: the script
// parses these lines and greps the document, so a paraphrase in either place is red and
// the numbers never have to be transcribed by hand.
func TestTheFrozenKC2PreregPrintsForTheGateScript(t *testing.T) {
	path := kc2PreregPath()
	p, err := readKC2Prereg(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if problems := checkKC2Prereg(p); len(problems) > 0 {
		t.Fatalf("the pre-registration disagrees with the compiled rule:\n%s", strings.Join(problems, "\n"))
	}
	sum, err := hashFile(path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	t.Logf("kc2-prereg: file=%s sha256=%s frozen_at=%s", path, sum, p.FrozenAt)
	for _, th := range p.Thresholds {
		t.Logf("kc2-threshold: key=%s value=%v unit=%s", th.Key, th.Value, th.Unit)
		t.Logf("kc2-quote: %s", th.Quote)
	}
	t.Logf("kc2-decision: %s", p.Decision)
	t.Logf("kc2-plan: sections_per_arm=%d", p.Plan.SectionsPerArm)
}
