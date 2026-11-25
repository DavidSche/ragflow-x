package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// `goldtool readiness` exists because doc/304 §7 item 30's answer was being taken by hand
// with three commands and then explained in prose. The prose is the fragile part, so these
// tests pin the properties that make the receipt re-runnable — and, one per test, the ways
// the two conditions could quietly be merged back into one number:
//
//   - a stratum the export cannot count must not be reported as `pass` (that is how
//     "A 成立" gets quoted about `outdated 12` that nobody measured);
//   - a known-and-zero count must be `fail`, not `unverified` (one annotator who judged 0
//     rows is an unanswered question, not an unanswerable one);
//   - the ceiling is counted in rows, so it must equal `pool-material`'s number;
//   - the plan comes from the prereg file, so there is nothing to point at a smaller target;
//   - `ready` is derived from the two states, never set.

func readinessRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "rout-*")
	if err != nil {
		t.Fatalf("stdout temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "rerr-*")
	if err != nil {
		t.Fatalf("stderr temp: %v", err)
	}
	defer stderr.Close()
	code := run(append([]string{"readiness"}, args...), stdout, stderr)
	return code, readAll(t, stdout.Name()), readAll(t, stderr.Name())
}

func readinessReceipt(t *testing.T, out string) Readiness {
	t.Helper()
	if i := strings.LastIndex(out, "\n{\n"); i >= 0 {
		out = out[i+1:]
	}
	var rec Readiness
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("readiness stdout is not the Readiness JSON: %v\n%s", err, out)
	}
	return rec
}

// writeReadinessPrereg writes a frozen plan. `plan` is the denominator, so every test that
// wants a clean A has to supply strata the pool *can* count.
func writeReadinessPrereg(t *testing.T, dir string, name string, planTotal int, plan map[string]int) string {
	t.Helper()
	doc := map[string]any{"schema": "rgx.prereg/1", "sample_id": "primary-random-20261121",
		"plan_total": planTotal, "plan": plan}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// freezePrereg records the input chain the way `goldtool prereg --freeze` + `--digest` do for
// the tracked plan: `inputs[]` (role, path, sha256, rows) plus the companion digest file. A
// fixture prereg without it can prove nothing about *which* pool and worksheet it belongs to,
// which is the finding behind E132 — so tests that want a green identity have to record it.
func freezePrereg(t *testing.T, preregPath, poolPath, worksheetPath string) {
	t.Helper()
	raw, err := os.ReadFile(preregPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["inputs"] = []map[string]any{
		{"role": "sample", "path": worksheetPath, "sha256": fileSHA(t, worksheetPath), "items": countLines(t, worksheetPath)},
		{"role": "pool", "path": poolPath, "sha256": fileSHA(t, poolPath), "items": countLines(t, poolPath)},
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preregPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	writePreregDigestOnly(t, preregPath)
}

// writePreregDigestOnly re-freezes the companion digest for the bytes now on disk. Tests that
// want to make exactly one thing wrong use it to take "the prereg changed" out of play.
func writePreregDigestOnly(t *testing.T, preregPath string) {
	t.Helper()
	raw, err := os.ReadFile(preregPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest, err := json.MarshalIndent(map[string]any{
		"schema": "rgx.goldtool.prereg.digest/1", "prereg_sha256": hex.EncodeToString(sum[:]),
		"bytes": len(raw),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readinessDigestPath(preregPath), digest, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimRight(string(raw), "\n"), "\n"))
}

// completePair is two annotators who judged every row identically: the smallest input that
// can make B pass, so a test about A is not silently also a test about B.
func completePair(t *testing.T, f *kappaFixture) string {
	t.Helper()
	// Two files, one name each: that is how annotations actually arrive (manual §2), and since
	// E132 it is also the only shape the identity rules accept — a merged file cannot say which
	// row is whose opinion.
	return f.file(t, "qa-a.jsonl", f.answered("qa-a", "supported")) + "," +
		f.file(t, "pm-b.jsonl", f.answered("pm-b", "supported"))
}

// writeReadinessPool writes a pool export whose material is exactly what the caller asked
// for: `candidates[i]` is how many frozen candidate rows item i carries.
func writeReadinessPool(t *testing.T, dir string, candidates []int) string {
	t.Helper()
	items := make([]Item, 0, len(candidates))
	for n, rows := range candidates {
		it := sampleItem(n+1, "")
		it.GoldVerdict, it.GoldQuote, it.GoldQuoteStatus = "", "", ""
		it.GoldEvidenceIDs, it.Annotators = nil, nil
		it.CandidatePool = nil
		for k := 0; k < rows; k++ {
			it.CandidatePool = append(it.CandidatePool, Candidate{ChunkID: fmt.Sprintf("chunk%03d", n), CitedElsewhere: true})
		}
		items = append(items, it)
	}
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pool.jsonl")
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
	return path
}

// The plan is a file. A target that could be typed on the command line is a target that can
// be typed to whatever the current pool reaches — the same hazard `pool-material` closed by
// reading `GoldDistribution` instead of accepting `--conflicting`.
func TestReadinessReadsThePlanFromThePreregFileNotAFlag(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{1, 1, 1}) // ceiling 3
	loose := writeReadinessPrereg(t, f.dir, "loose.json", 3, map[string]int{"conflicting": 3})
	tight := writeReadinessPrereg(t, f.dir, "tight.json", 3, map[string]int{"conflicting": 4})
	freezePrereg(t, loose, pool, f.worksheet)
	freezePrereg(t, tight, pool, f.worksheet)

	complete := completePair(t, f)
	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet, "--prereg", loose,
		"--labels", complete)
	if code != exitOK {
		t.Fatalf("a plan the pool reaches must be ready, got exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.Prereg.Path != loose || len(rec.Prereg.SHA256) != 64 {
		t.Fatalf("the receipt must name the file it read and its digest: %+v", rec.Prereg)
	}
	if !rec.Ready || rec.A.State != readinessPass || rec.B.State != readinessPass {
		t.Fatalf("ready, A and B must agree: %+v", rec)
	}

	code, out, _ = readinessRun(t, "--pool", pool, "--worksheet", f.worksheet, "--prereg", tight,
		"--labels", complete)
	if code != exitInvariant {
		t.Fatalf("a plan the pool cannot reach is a fail, got exit %d\n%s", code, out)
	}
	rec = readinessReceipt(t, out)
	if rec.Ready || rec.A.State != readinessFail {
		t.Fatalf("conflicting 4 over 3 candidate rows must fail A: %+v", rec.A)
	}
	if rec.Prereg.Path != tight {
		t.Fatalf("the second reading must name the second file: %s", rec.Prereg.Path)
	}
}

// 36 candidate rows over 30 claims is a ceiling of 36: the sampler draws rows, so an item
// with three candidates can yield three conflicting samples. Reporting 30 here would be the
// denominator mistake doc/304 §1.3 W-1 warns about, one layer up.
func TestReadinessCountsTheCeilingInRowsNotItems(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet, "--prereg", prereg,
		"--labels", completePair(t, f))
	if code != exitOK {
		t.Fatalf("ceiling 3 reaches target 3, got exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.A.CandidateRows != 3 || rec.A.ItemsWithCands != 1 || rec.A.ItemsWithoutCand != 2 {
		t.Fatalf("rows and items must stay distinguishable: rows=%d items=%d without=%d",
			rec.A.CandidateRows, rec.A.ItemsWithCands, rec.A.ItemsWithoutCand)
	}
	if rec.A.CandidateRows == rec.A.ItemsWithCands {
		t.Fatalf("fixture cannot tell rows from items: both are %d", rec.A.CandidateRows)
	}
}

// The single most important assertion in this file: a stratum the export cannot count is
// `unverified`, never `pass`. `outdated` is a content property (same document, same key,
// different value) and a pool export has neither key grouping nor per-evidence dates.
func TestReadinessNeverPassesAStratumTheExportCannotCount(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{1, 1, 1})
	onlyCountable := writeReadinessPrereg(t, f.dir, "a.json", 3, map[string]int{"conflicting": 3})
	withOutdated := writeReadinessPrereg(t, f.dir, "b.json", 3,
		map[string]int{"conflicting": 2, "outdated": 1})
	freezePrereg(t, onlyCountable, pool, f.worksheet)
	freezePrereg(t, withOutdated, pool, f.worksheet)

	complete := completePair(t, f)
	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", onlyCountable, "--labels", complete)
	if code != exitOK {
		t.Fatalf("every stratum is countable and both halves are in, so this is ready: %d\n%s%s", code, out, errText)
	}
	if rec := readinessReceipt(t, out); rec.A.State != readinessPass || len(rec.A.NotCounted) != 0 {
		t.Fatalf("every stratum is countable here, so A must pass: %+v", rec.A)
	}

	// With B passed, the uncountable stratum is the only thing standing — and it must block
	// with exit 3 ("cannot answer"), not exit 0 and not a soft pass.
	code, out, errText = readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", withOutdated, "--labels", complete)
	rec := readinessReceipt(t, out)
	if rec.A.State != readinessUnverified {
		t.Fatalf("A must be unverified while a planned stratum has no ceiling, got %q\n%s", rec.A.State, out)
	}
	if rec.B.State != readinessPass {
		t.Fatalf("B is complete in this case, so the blocked condition must be A alone: %+v", rec.B)
	}
	if len(rec.A.NotCounted) != 1 || rec.A.NotCounted[0] != string(model.ReportVerdictOutdated) {
		t.Fatalf("the receipt must name the stratum it could not count: %v", rec.A.NotCounted)
	}
	if code != exitIncomplete {
		t.Fatalf("unverified blocks with exit 3 even when nothing failed, got %d\n%s%s", code, out, errText)
	}
	// When a describable failure exists as well, exit 1 wins: "this condition has to change" is
	// a stronger statement than "this file cannot say".
	if code, _, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", withOutdated); code != exitInvariant {
		t.Fatalf("with no labels at all, B fails and the exit is 1, got %d", code)
	}
	for _, target := range rec.A.Targets {
		if target.Verdict == string(model.ReportVerdictOutdated) && target.State == readinessPass {
			t.Fatalf("an uncountable stratum must never be pass: %+v", target)
		}
	}
}

// A prereg whose parts do not add up is two plans in one file. It must not be resolved by
// preferring either number.
func TestReadinessRefusesAPlanWhoseStrataDoNotAddUp(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{1, 1, 1})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3, "supported": 1})

	code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet, "--prereg", prereg)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1 (the plan contradicts itself)\n%s", code, out)
	}
	rec := readinessReceipt(t, out)
	if rec.A.State != readinessFail || !strings.Contains(rec.A.Reason, "plan_total") {
		t.Fatalf("the reason must name the contradiction: %+v", rec.A)
	}
	if len(rec.A.Targets) != 0 {
		t.Fatalf("no per-stratum verdicts may be reported against an unusable plan: %+v", rec.A.Targets)
	}
}

// The reading item 30 recorded by hand: one annotator, 0 of 120 judged. `fail`, not
// `unverified` — and the receipt has to carry both facts, because "no second person" is a
// staffing action while "nobody has judged a row" is a calendar one.
func TestReadinessCallsOneAnnotatorAndZeroJudgedAFailure(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	labels := f.file(t, "labels-qa.jsonl", []Label{
		{ItemID: f.items[0].ItemID, Annotator: "qa-a"},
		{ItemID: f.items[1].ItemID, Annotator: "qa-a"},
		{ItemID: f.items[2].ItemID, Annotator: "qa-a"},
	})

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1: a known-and-zero count is a failure\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.B.State != readinessFail || rec.Ready {
		t.Fatalf("one annotator cannot be ready: %+v", rec.B)
	}
	if rec.B.DistinctPeople != 1 || rec.B.JudgedRows != 0 {
		t.Fatalf("receipt must report who and how much: people=%d judged=%d", rec.B.DistinctPeople, rec.B.JudgedRows)
	}
	if !strings.Contains(rec.B.Reason, "一行都还没判") {
		t.Fatalf("the reason must say nobody started, not just that someone is missing: %s", rec.B.Reason)
	}
	if len(rec.B.Annotators) != 1 || rec.B.Annotators[0].File != labels {
		t.Fatalf("the receipt must say which file the name came from: %+v", rec.B.Annotators)
	}
}

// Two people who have not finished is not low agreement; it is unfinished, and κ is not
// computed over the overlap (the same sentence `kappa` prints).
func TestReadinessCallsAnUnfinishedPairUnfinished(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	qa := f.answered("qa-a", "supported")
	pm := f.answered("pm-b", "supported")[:2]
	labels := f.file(t, "qa-a.jsonl", qa) + "," + f.file(t, "pm-b.jsonl", pm)

	code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	rec := readinessReceipt(t, out)
	if rec.B.State != readinessFail || rec.B.MissingCells != 1 || rec.B.JudgedRows != 2 {
		t.Fatalf("the gap must be counted: %+v", rec.B)
	}
	if rec.B.Kappa != nil {
		t.Fatalf("κ must stay unset while the sample is unfinished, got %v", *rec.B.Kappa)
	}
	if !strings.Contains(rec.B.Reason, "没做完") {
		t.Fatalf("the reason must say unfinished, not low: %s", rec.B.Reason)
	}
}

// Once both halves are in, B answers with κ — and a κ under the floor fails with the plan's
// own instruction (below 0.60: fix the manual, not the model).
func TestReadinessReadsKappaOnceBothHalvesAreIn(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)

	agree := f.file(t, "agree-a.jsonl", f.answered("qa-a", "supported")) + "," +
		f.file(t, "agree-b.jsonl", f.answered("pm-b", "supported"))
	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", agree)
	if code != exitOK {
		t.Fatalf("two people who agree must be ready, got exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.B.Kappa == nil || *rec.B.Kappa < 0.70 {
		t.Fatalf("κ must be in the receipt once computable: %+v", rec.B)
	}
	if !strings.Contains(rec.Boundary, "不表示判得准") {
		t.Fatalf("every receipt must carry the boundary sentence: %s", rec.Boundary)
	}

	half := f.file(t, "half-a.jsonl", f.answered("qa-a", "supported")) + "," +
		f.file(t, "half-b.jsonl", f.answered("pm-b", "conflicting"))
	code, out, _ = readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", half)
	if code != exitInvariant {
		t.Fatalf("disagreement under the floor must fail, got exit %d\n%s", code, out)
	}
	rec = readinessReceipt(t, out)
	if rec.B.State != readinessFail || rec.B.Kappa == nil || *rec.B.Kappa >= 0.70 {
		t.Fatalf("the receipt must report the low κ it measured: %+v", rec.B)
	}
	if !strings.Contains(rec.B.Reason, "手册") {
		t.Fatalf("a κ this low must send the reader to the manual (301 §9.1): %s", rec.B.Reason)
	}
}

// A worksheet that is not the one the prereg froze makes every count above a count about a
// different sample — and the numbers would still look healthy.
func TestReadinessRefusesAWorksheetThatIsNotTheFrozenSample(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 120, map[string]int{"conflicting": 24})

	code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet, "--prereg", prereg)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	rec := readinessReceipt(t, out)
	if rec.B.State != readinessFail || !strings.Contains(rec.B.Reason, "plan_total") {
		t.Fatalf("the reason must name the mismatch: %+v", rec.B)
	}
	if rec.B.WorksheetRows != len(f.items) || rec.B.PlanTotal != 120 {
		t.Fatalf("both numbers must be in the receipt: %d vs %d", rec.B.WorksheetRows, rec.B.PlanTotal)
	}
}

// A label file whose rows do not line up with the worksheet is refused before any κ, with
// the codes — the same set `labels --check` and `kappa` enforce, so a skipped `--check`
// cannot change what the readiness reading says.
func TestReadinessNamesLabelProblemsInsteadOfKappa(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	qa := f.answered("qa-a", "supported")
	pm := f.answered("pm-b", "supported")
	pm[0].Verdict = "abstained" // outside the gold closed set
	labels := f.file(t, "merged.jsonl", append(qa, pm...))

	code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	rec := readinessReceipt(t, out)
	if rec.B.State != readinessFail || len(rec.B.Problems) == 0 {
		t.Fatalf("problems must be carried, not summarized away: %+v", rec.B)
	}
	if rec.B.Kappa != nil {
		t.Fatalf("no κ may be reported over a file with an unusable row: %v", *rec.B.Kappa)
	}
}

// Half the question is not a smaller question: A asks about the pool, B about the worksheet,
// and answering one of them is exactly the merged condition this command exists to prevent.
func TestReadinessRefusesToRunOnHalfTheQuestion(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--pool", "pool.jsonl"},
		{"--worksheet", "worksheet.jsonl"},
	} {
		code, out, errText := readinessRun(t, args...)
		if code != exitUsage {
			t.Fatalf("%v: exit = %d, want 2\n%s%s", args, code, out, errText)
		}
		if !strings.Contains(errText, "--pool and --worksheet are both required") {
			t.Fatalf("%v: the refusal must name what is missing: %s", args, errText)
		}
	}
	// An unreadable prereg is a usage error, not "no plan": a clean refusal of a file that was
	// fine would send the reader looking for a plan that exists.
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{1, 1, 1})
	code, _, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", filepath.Join(f.dir, "nope.json"))
	if code != exitUsage || !strings.Contains(errText, "prereg:") {
		t.Fatalf("missing prereg: exit = %d, want 2 naming the flag\n%s", code, errText)
	}
}
