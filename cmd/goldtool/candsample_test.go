package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// candItem builds a pool item that carries real candidate rows: the probe needs the item
// to be shaped like a pool row (no labels) and the scan scope to agree with the rows.
func candItem(id string, ord int, scope string, cands []Candidate) Item {
	it := sampleItem(ord+1, "")
	it.ItemID = id
	it.SectionOrd = ord
	it.GoldScanScope = scope
	it.CandidatePool = cands
	it.GoldVerdict = ""
	it.GoldQuoteStatus = ""
	it.GoldEvidenceIDs = nil
	it.Annotators = nil
	return it
}

func writePoolFixture(t *testing.T, items []Item) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pool.jsonl")
	if err := writeSample(path, items); err != nil {
		t.Fatalf("write pool: %v", err)
	}
	return path
}

// poolWithCandidates writes a pool whose candidate side holds four rows: 3 cited
// elsewhere, 1 never cited. The numbers are small on purpose so every assertion can be
// read off the fixture.
func poolWithCandidates(t *testing.T) string {
	t.Helper()
	items := []Item{
		candItem("cand-a", 0, "cited_only", nil),
		candItem("cand-b", 1, "cited+candidate:1", []Candidate{
			{ChunkID: "chunk-cited-elsewhere", ContentHash: "h1", Content: strings.Repeat("别的章节引用过的原文。", 4), CitedElsewhere: true, PoolRank: 1},
		}),
		candItem("cand-c", 2, "cited+candidate:1", []Candidate{
			{ChunkID: "chunk-never-cited", ContentHash: "h2", Content: "从未被引用过的候选原文。", PoolRank: 2},
		}),
		candItem("cand-d", 3, "cited+candidate:2", []Candidate{
			{ChunkID: "chunk-d1", ContentHash: "h3", Content: "第一条候选。", CitedElsewhere: true, PoolRank: 1},
			{ChunkID: "chunk-d2", ContentHash: "h4", Content: "第二条候选。", CitedElsewhere: true, PoolRank: 2},
		}),
	}
	return writePoolFixture(t, items)
}

func worksheetRows(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read worksheet: %v", err)
	}
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			t.Fatalf("worksheet line is not JSON: %v", err)
		}
		out = append(out, row)
	}
	return out
}

// runSampleCmd calls the CLI entry with stdout/stderr captured to files, so a test can
// assert on the printed receipt without touching os.Stdout.
func runSampleCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	stdoutPath, stderrPath := filepath.Join(dir, "stdout.json"), filepath.Join(dir, "stderr.txt")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer stderr.Close()
	code := run(append([]string{"sample"}, args...), stdout, stderr)
	o, _ := os.ReadFile(stdoutPath)
	e, _ := os.ReadFile(stderrPath)
	return code, string(o), string(e)
}

// The candidate side exists because E2's source is rows, not claims: one claim can carry
// two frozen candidates, and the evaluator judges the row.
func TestSampleCandidateSideDrawsRows(t *testing.T) {
	pool := poolWithCandidates(t)
	out := filepath.Join(t.TempDir(), "cand.jsonl")
	code, stdout, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates", "--n", "4", "--seed", "7")
	if code != exitOK {
		t.Fatalf("exit %d, stderr=%s", code, stderr)
	}
	rows := worksheetRows(t, out)
	if len(rows) != 4 {
		t.Fatalf("drew %d rows, want 4 (the whole candidate side of the fixture)", len(rows))
	}
	for _, r := range rows {
		if r["unit"] != "candidate_row" {
			t.Fatalf("unit = %v, want candidate_row", r["unit"])
		}
		if r["gold_scan_scope"] == "cited_only" {
			t.Fatalf("a cited_only item cannot contribute a candidate row: %v", r["item_id"])
		}
		if _, has := r["gold_verdict"]; has {
			t.Fatalf("the worksheet must not carry gold_verdict: %v", r)
		}
		if r["candidate_excerpt"] == "" {
			t.Fatalf("row carries no candidate text: %v", r)
		}
		if r["sample_id"] != "primary-random-candidates-7" {
			t.Fatalf("row sample_id = %v, want the side-specific id", r["sample_id"])
		}
	}
	if !strings.Contains(stdout, `"side": "candidates"`) {
		t.Fatalf("receipt does not name its side: %s", stdout)
	}
	if !strings.Contains(stdout, "primary-random-candidates-7") {
		t.Fatalf("receipt sample_id should be side-specific: %s", stdout)
	}
}

// §9.1 asks for the two cited_elsewhere groups separately. The fixture has 3 such rows and
// 1 not-cited-elsewhere row; the receipt must say so.
func TestSampleCandidateSideCountsCitedElsewhereGroups(t *testing.T) {
	pool := poolWithCandidates(t)
	out := filepath.Join(t.TempDir(), "cand.jsonl")
	code, stdout, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates", "--n", "4", "--seed", "7")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var receipt sampleReceipt
	if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, stdout)
	}
	got := map[string]int{}
	for _, g := range receipt.CandidateGroups {
		got[g.Key] = g.Selected
	}
	if got["cited_elsewhere=true"] != 3 || got["cited_elsewhere=false"] != 1 {
		t.Fatalf("group counts = %v, want true:3 false:1", got)
	}
}

// A short group is reported with its actual n, and without --allow-short nothing is
// written. Padding to the target would inflate E2's denominator; inventing a row would
// violate the one source rule in §9.1.
func TestSampleCandidateSideShortGroupReportsActualN(t *testing.T) {
	pool := poolWithCandidates(t)
	out := filepath.Join(t.TempDir(), "cand.jsonl")
	code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates",
		"--strategy", "stratified", "--targets", "cited_elsewhere=true=2,cited_elsewhere=false=2", "--seed", "7")
	if code != exitIncomplete {
		t.Fatalf("exit %d, want %d (short group)", code, exitIncomplete)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a short draw must not write a worksheet without --allow-short")
	}
	if !strings.Contains(stderr, "cited_elsewhere=false") {
		t.Fatalf("the short group must be named: %s", stderr)
	}

	code, stdout, _ := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates",
		"--strategy", "stratified", "--targets", "cited_elsewhere=true=2,cited_elsewhere=false=2",
		"--seed", "7", "--allow-short")
	if code != exitIncomplete {
		t.Fatalf("--allow-short writes the file but still exits %d, got %d", exitIncomplete, code)
	}
	rows := worksheetRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("wrote %d rows, want 3 (2 cited elsewhere + 1 available)", len(rows))
	}
	var receipt sampleReceipt
	if err := json.Unmarshal([]byte(stdout), &receipt); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if len(receipt.Shortfall) != 1 || receipt.Shortfall[0].Available != 1 || receipt.Shortfall[0].Gap != 1 {
		t.Fatalf("shortfall = %+v, want one row with available=1 gap=1", receipt.Shortfall)
	}
}

// The stratified vocabulary on this side is closed: `--targets "supported=80"` is an item
// request, and accepting it here would produce an empty draw that looks like a corpus fact.
func TestSampleCandidateSideRejectsItemTargets(t *testing.T) {
	pool := poolWithCandidates(t)
	out := filepath.Join(t.TempDir(), "cand.jsonl")
	code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates",
		"--strategy", "stratified", "--targets", "supported=2", "--seed", "7")
	if code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "cited_elsewhere") {
		t.Fatalf("error should name the only legal key: %s", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a refused draw must not write a worksheet")
	}
}

// A pool with no candidates is a corpus fact, and the tool says it is one instead of
// writing an empty file that reads like a completed draw.
func TestSampleCandidateSideRefusesPoolWithoutCandidates(t *testing.T) {
	pool := writePoolFixture(t, []Item{candItem("no-cand", 0, "cited_only", nil)})
	out := filepath.Join(t.TempDir(), "cand.jsonl")
	code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates", "--n", "1", "--seed", "7")
	if code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "no candidate rows") {
		t.Fatalf("stderr should explain the empty side: %s", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("no worksheet should be written")
	}
}

func TestSampleCandidateSideIsDeterministic(t *testing.T) {
	pool := poolWithCandidates(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "a.jsonl")
	second := filepath.Join(dir, "b.jsonl")
	for _, out := range []string{first, second} {
		if code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates", "--n", "3", "--seed", "11", "--quiet"); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if string(a) != string(b) {
		t.Fatalf("same seed + same pool must draw byte-identical rows")
	}
}

// The item side must be untouched by the new flag: no --side means one claim per row.
func TestSampleDefaultSideIsItems(t *testing.T) {
	pool := poolWithCandidates(t)
	out := filepath.Join(t.TempDir(), "items.jsonl")
	code, stdout, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--n", "2", "--seed", "7")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, `"side": "items"`) {
		t.Fatalf("default receipt should say side=items: %s", stdout)
	}
	rows := worksheetRows(t, out)
	for _, r := range rows {
		if _, has := r["unit"]; has {
			t.Fatalf("item-side rows must keep their old shape: %v", r)
		}
	}
	if strings.Contains(stdout, "-candidates-") {
		t.Fatalf("item-side sample_id must not carry the candidate suffix: %s", stdout)
	}
}
