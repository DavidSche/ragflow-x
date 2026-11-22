package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unlabelledPool writes the worksheet-shaped pool the composition test starts from: full
// item text (that is what an annotator reads), evidence snapshots with content, and no
// `gold_*` field at all — a pool is a question, not an answer.
func unlabelledPool(t *testing.T, dir string) ([]Item, string) {
	t.Helper()
	items := goldWithDistribution()
	for i := range items {
		items[i].GoldVerdict = ""
		items[i].GoldQuote = ""
		items[i].GoldQuoteStatus = ""
		items[i].GoldEvidenceIDs = nil
		items[i].Annotators = nil
		for j := range items[i].Evidence {
			items[i].Evidence[j].ContentExcerpt, items[i].Evidence[j].ContentChars = "", 0
			items[i].Evidence[j].Content = "某企业应当建立安全生产责任制台账，并按季度上报。"
		}
	}
	path := filepath.Join(dir, "pool.jsonl")
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
	return items, path
}

// TestPipelineFromWorksheetToE1Report ties the four offline deliverables together in the
// order T02/T05 actually use them: freeze the rules, stub the label files, refuse an
// unfinished file, fill it, check it, κ, assemble, validate, then eval — twice, once with
// predictions that clear every row and once with a prediction file that fails exactly one
// class.
//
// Each half is unit-tested on its own. What was untested is that the *output* of one
// command is the *input* the next one accepts: `assemble`'s gold file is `eval`'s `--gold`,
// `labels --stub`'s skeleton is what the annotator edits and what `kappa`/`assemble` read,
// and the hash `prereg --freeze` records is the worksheet's. The live run on 151 gets no
// further than "has no gold_verdict", so without this test the rows eval computes after
// that refusal would never have been exercised on a file the tool itself produced.
func TestPipelineFromWorksheetToE1Report(t *testing.T) {
	dir := t.TempDir()
	items, poolPath := unlabelledPool(t, dir)

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	// 1. The rules are frozen before a single label exists. The sample IS the worksheet the
	//    annotators will see, which is what makes the freeze binding on the run below.
	f := newPreregFixture(t, items, items)
	preregPath, code, out := f.freeze(t)
	if code != exitOK {
		t.Fatalf("prereg --freeze exited %d:\n%s", code, out)
	}
	if code, _, errText := runPreregCmd(t, "--check", preregPath); code != exitOK {
		t.Fatalf("the file it just froze does not check out (%d):\n%s", code, errText)
	}

	// 2. Skeleton for each annotator, then fill it. The ids come out of the stub file, not
	//    out of this test: if --stub ever emitted a wrong or truncated id, κ and assemble
	//    would both see a different item set and the assertions below would move.
	filled := map[string]string{}
	for _, annotator := range []string{"qa", "pm"} {
		stubPath := filepath.Join(dir, "labels-"+annotator+".jsonl")
		if code, _, errText := labelsRun(t, "--stub", "--worksheet", f.sample,
			"--annotator", annotator, "--out", stubPath); code != exitOK {
			t.Fatalf("labels --stub for %s exited %d:\n%s", annotator, code, errText)
		}
		if code, _, errText := labelsRun(t, "--check", "--worksheet", f.sample,
			"--labels", stubPath); code != exitIncomplete {
			t.Fatalf("an untouched stub must be unfinished (exit %d), got %d:\n%s",
				exitIncomplete, code, errText)
		}
		ids := stubIDs(t, stubPath)
		if len(ids) != len(items) {
			t.Fatalf("stub for %s has %d rows, want %d", annotator, len(ids), len(items))
		}
		ls := make([]Label, 0, len(ids))
		for i, id := range ids {
			verdict := plannedVerdict(i)
			// A `supported` claim without a verified quote is a contradictory label (doc/301
			// §9.1 question ①), and eval fails quote_verified_rate on it. The plan says 36+8
			// of the 120 are supported/partially_supported, so those are the rows that must
			// carry the span — which is also the first thing this composition test caught.
			status, quote := "not_provided", ""
			if verdict == "supported" || verdict == "partially_supported" {
				status, quote = "verified", "建立安全生产责任制台账"
			}
			ls = append(ls, Label{ItemID: id, Annotator: annotator, Verdict: verdict,
				QuoteStatus: status, Quote: quote})
		}
		path := filepath.Join(dir, "labels-"+annotator+"-filled.jsonl")
		writeLabels(t, path, ls)
		if code, _, errText := labelsRun(t, "--check", "--worksheet", f.sample,
			"--labels", path); code != exitOK {
			t.Fatalf("a fully judged file must pass (exit 0), got %d:\n%s", code, errText)
		}
		filled[annotator] = path
	}

	// 3. κ over the merged file: the two annotators agree everywhere, so κ = 1 — which also
	//    proves the merged file is the shape `kappa` accepts.
	mergedPath := filepath.Join(dir, "labels-merged.jsonl")
	mergeLabelFiles(t, mergedPath, filled["qa"], filled["pm"])
	kappaOut, err := os.CreateTemp(t.TempDir(), "kappa-*")
	if err != nil {
		t.Fatal(err)
	}
	defer kappaOut.Close()
	if code := run([]string{"kappa", "--labels", mergedPath}, kappaOut, devnull); code != exitOK {
		t.Fatalf("kappa on a perfectly agreeing pair exited %d", code)
	}
	rawKappa, err := os.ReadFile(kappaOut.Name())
	if err != nil {
		t.Fatal(err)
	}
	var ag Agreement
	if err := json.Unmarshal(rawKappa, &ag); err != nil {
		t.Fatalf("kappa output is not the Agreement JSON: %v\n%s", err, rawKappa)
	}
	if ag.Pairs == 0 || ag.Kappa != 1 {
		t.Fatalf("κ over two identical files: pairs=%d kappa=%v", ag.Pairs, ag.Kappa)
	}

	// 4. assemble + validate, the same pair the κ gate accepts.
	outDir := filepath.Join(dir, "gold")
	if code := run([]string{"assemble", "--pool", poolPath,
		"--labels-a", filled["qa"], "--labels-b", filled["pm"],
		"--version", "v1", "--out", outDir}, devnull, devnull); code != exitOK {
		t.Fatalf("assemble from two filled label files exited %d", code)
	}
	goldPath := filepath.Join(outDir, "report-assurance-gold.v1.jsonl")
	if code := run([]string{"validate", "--gold", goldPath}, devnull, devnull); code != exitOK {
		t.Fatalf("validate on the assembled file exited %d", code)
	}

	// 5. Predictions that agree with gold everywhere: every hard row clears, so eval exits 0.
	gold, err := readItems(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	goodPath := filepath.Join(dir, "pred-good.jsonl")
	writePredictions(t, goodPath, gold, "")
	reportPath := filepath.Join(dir, "e1.md")
	if code, diag := runCapturing(t, "eval", "--gold", goldPath, "--pred", goodPath,
		"--exp", "E1", "--out", reportPath); code != exitOK {
		t.Fatalf("a prediction file that matches gold must pass E1, exited %d:\n%s\n%s", code, diag,
			readFileText(t, reportPath))
	}
	report := readFileText(t, reportPath)
	if !strings.Contains(report, "预注册：**未引用**") {
		t.Fatalf("a run without --prereg must say so in the report:\n%s", firstLines(report, 25))
	}
	// The 36 supported + 8 partially_supported rows are the only ones that may carry
	// `verified`; if the fixture ever stopped supplying the span, eval would fail this row
	// instead of quietly counting 44 rows with no quote behind them.
	if !strings.Contains(report, "quote_verified_rate") ||
		!strings.Contains(report, "| 44/44 |") {
		t.Fatalf("quote_verified_rate is not 44/44 on a fixture that quotes every supported claim:\n%s",
			firstLines(report, 30))
	}

	// 6. The same run bound to the freeze: the arm, its cost_rank and the sample hash come
	//    from the frozen file, and the gold ids must be a subset of the frozen sample.
	if code, diag := runCapturing(t, "eval", "--gold", goldPath, "--pred", goodPath,
		"--exp", "E1", "--prereg", preregPath, "--method", "deterministic",
		"--arm", "rule-v1", "--out", reportPath); code != exitOK {
		t.Fatalf("the same run bound to its freeze exited %d:\n%s", code, diag)
	}
	report = readFileText(t, reportPath)
	for _, want := range []string{"预注册：`", "臂：**rule-v1**", "cost_rank 1", "frozen "} {
		if !strings.Contains(report, want) {
			t.Fatalf("the bound report does not state %q:\n%s", want, firstLines(report, 30))
		}
	}

	// 7. Fail exactly one class: every `conflicting` item is predicted `supported`. The run
	//    must exit 1 on conflicting_recall and must name that row, while the report keeps the
	//    denominators a reader needs to see that the other rows were not the reason.
	badPath := filepath.Join(dir, "pred-conflicting-missed.jsonl")
	writePredictions(t, badPath, gold, "conflicting")
	if code, diag := runCapturing(t, "eval", "--gold", goldPath, "--pred", badPath,
		"--exp", "E1", "--out", reportPath); code != exitInvariant {
		t.Fatalf("missing every conflicting claim must fail E1, exited %d:\n%s", code, diag)
	}
	report = readFileText(t, reportPath)
	if !strings.Contains(report, "conflicting_recall") || !strings.Contains(report, "| fail |") {
		t.Fatalf("the failing row is not named as failing:\n%s", firstLines(report, 40))
	}
}

// stubIDs reads the item ids out of a `labels --stub` file, refusing anything that is not
// exactly the two-field skeleton: a stub that grew a third field would be the tool deciding
// something a human is supposed to decide.
func stubIDs(t *testing.T, path string) []string {
	t.Helper()
	raw := readFileText(t, path)
	var ids []string
	for i, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("%s:%d: %v", path, i+1, err)
		}
		if len(row) != 2 {
			t.Fatalf("%s:%d: the stub carries %d field(s) %v; a stub is item_id + annotator only",
				path, i+1, len(row), row)
		}
		id, _ := row["item_id"].(string)
		if strings.TrimSpace(id) == "" {
			t.Fatalf("%s:%d: the stub row has no item_id", path, i+1)
		}
		ids = append(ids, id)
	}
	return ids
}

func mergeLabelFiles(t *testing.T, dst string, srcs ...string) {
	t.Helper()
	var all []string
	for _, src := range srcs {
		raw := strings.TrimRight(readFileText(t, src), "\n")
		if raw != "" {
			all = append(all, strings.Split(raw, "\n")...)
		}
	}
	if err := writeJSONL(dst, all); err != nil {
		t.Fatal(err)
	}
}

// writePredictions writes predictions that copy gold, except that every item of the named
// class (if any) is predicted `supported` — one class wrong and nothing else, so the report
// can only fail on that class.
func writePredictions(t *testing.T, path string, gold []Item, missClass string) {
	t.Helper()
	var lines []string
	for _, it := range gold {
		verdict := it.GoldVerdict
		if missClass != "" && it.GoldVerdict == missClass {
			verdict = "supported"
		}
		raw, err := json.Marshal(Prediction{ItemID: it.ItemID, Verdict: verdict,
			QuoteStatus: it.GoldQuoteStatus})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
}

// runCapturing runs one subcommand and returns its exit code plus both streams, so a
// failing assertion can show the row that failed instead of only the exit code.
func runCapturing(t *testing.T, args ...string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()
	code := run(args, outFile, errFile)
	stdout, _ := os.ReadFile(outFile.Name())
	stderr, _ := os.ReadFile(errFile.Name())
	return code, string(stdout) + string(stderr)
}

func readFileText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func firstLines(s string, lines int) string {
	parts := strings.Split(s, "\n")
	if len(parts) > lines {
		parts = parts[:lines]
	}
	return strings.Join(parts, "\n")
}
