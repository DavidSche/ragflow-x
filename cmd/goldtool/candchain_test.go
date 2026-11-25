package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The candidates side is a *unit*, not a display option: one claim can carry two frozen
// candidates, and E2's denominator is rows. Everything downstream of `sample --side
// candidates` therefore has to key on (item_id, candidate_index) — doc/301 §9.1.3 一 says
// so, and §7 item 40 recorded that the label chain keyed on `item_id` alone, which silently
// merged a claim's two rows into one and counted 30 where the plan says 36.
//
// These five tests hold the half a `go test` can hold: that the chain *cannot* fall back to
// the claim key, because a candidate-side file without `candidate_index` is refused rather
// than defaulted, and because the numbers a receipt prints are rows and item_ids, both.

// twoCandidateWorksheet is one claim with two candidates and one claim with one, so the
// difference between "rows" and "claims" is visible in every receipt: 3 rows, 2 claims.
func twoCandidateWorksheet(t *testing.T) (ws string, rowKeys []string) {
	t.Helper()
	items := []Item{
		candItem("claim-two", 0, "cited+candidate:2", []Candidate{
			{ChunkID: "chunk-a", ContentHash: "h-a", Content: "第 12 家权属单位整改未完成。", CitedElsewhere: true, PoolRank: 1},
			{ChunkID: "chunk-b", ContentHash: "h-b", Content: "整改完成率 71%。", CitedElsewhere: true, PoolRank: 2},
		}),
		candItem("claim-one", 1, "cited+candidate:1", []Candidate{
			{ChunkID: "chunk-c", ContentHash: "h-c", Content: "该企业未整改。", CitedElsewhere: true, PoolRank: 1},
		}),
	}
	pool := writePoolFixture(t, items)
	out := filepath.Join(t.TempDir(), "cand-worksheet.jsonl")
	code, _, stderr := runSampleCmd(t, "--pool", pool, "--out", out, "--side", "candidates", "--n", "3", "--seed", "3")
	if code != exitOK {
		t.Fatalf("sample --side candidates exit %d: %s", code, stderr)
	}
	parsed, err := readWorksheet(out)
	if err != nil {
		t.Fatalf("readWorksheet: %v", err)
	}
	if len(parsed.Rows) != 3 {
		t.Fatalf("fixture drew %d rows, want 3", len(parsed.Rows))
	}
	if got := parsed.distinctItemIDs(); got != 2 {
		t.Fatalf("fixture covers %d claims, want 2", got)
	}
	return out, parsed.keys()
}

// chainRun drives one subcommand the way a person would, capturing both streams. The exit
// code is the sentence under test, so it is always returned.
func chainRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	stdoutPath, stderrPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	code := run(args, stdout, stderr)
	stdout.Close()
	stderr.Close()
	return code, readAll(t, stdoutPath), readAll(t, stderrPath)
}

// candIndex is the index half of a worksheet key, so a test never types the number it is
// checking the tool derived.
func candIndex(t *testing.T, key string) *int {
	t.Helper()
	_, idx, ok := strings.Cut(key, "#")
	if !ok {
		t.Fatalf("row key %q is not the composite key", key)
	}
	v := 0
	for _, c := range idx {
		if c < '0' || c > '9' {
			t.Fatalf("candidate index %q in row key %q is not a number", idx, key)
		}
		v = v*10 + int(c-'0')
	}
	return &v
}

// The stub is where the composite key is born: one row per *candidate*, each carrying the
// index the row already has. A stub keyed on claims would hand the annotator 2 rows for 3
// candidates and nobody would notice until κ came out over the wrong denominator.
func TestCandidateStubWritesOneRowPerCandidate(t *testing.T) {
	ws, keys := twoCandidateWorksheet(t)
	stub := filepath.Join(t.TempDir(), "labels-a.jsonl")

	code, stdout, stderr := chainRun(t, "labels", "--stub", "--worksheet", ws, "--annotator", "a1", "--out", stub)
	if code != exitOK {
		t.Fatalf("stub exit %d: %s", code, stderr)
	}
	raw, err := os.ReadFile(stub)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	var rows []stubRow
	for _, ln := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var r stubRow
		if err := json.Unmarshal([]byte(ln), &r); err != nil {
			t.Fatalf("stub line is not JSON: %v", err)
		}
		rows = append(rows, r)
	}
	if len(rows) != len(keys) {
		t.Fatalf("stub wrote %d rows for %d worksheet rows", len(rows), len(keys))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.CandidateIndex == nil {
			t.Fatalf("stub row %s carries no candidate_index: the annotator would have to type it, "+
				"and the claim's two rows would look identical", r.ItemID)
		}
		if r.Annotator != "a1" {
			t.Fatalf("stub row names annotator %q, want a1", r.Annotator)
		}
		seen[rowKey(r.ItemID, r.CandidateIndex)] = true
	}
	for _, k := range keys {
		if !seen[k] {
			t.Fatalf("stub has no row for %s", k)
		}
	}
	// The receipt's numbers are the plan's numbers: 3 rows over 2 claims, said out loud.
	if !strings.Contains(stdout, `"side": "candidates"`) || !strings.Contains(stdout, `"items": 3`) {
		t.Fatalf("receipt does not say 3 candidate rows: %s", stdout)
	}
	if !strings.Contains(stdout, `"item_ids": 2`) {
		t.Fatalf("receipt does not separate rows from claims: %s", stdout)
	}
}

// A label row without candidate_index cannot be placed on the candidates side, and the one
// thing the tool must not do is place it: defaulting to 0 re-keys a whole file toward
// whichever row happens to sit first.
func TestCandidateLabelsRefuseARowWithoutAnIndex(t *testing.T) {
	ws, keys := twoCandidateWorksheet(t)
	claim, _, ok := strings.Cut(keys[0], "#")
	if !ok {
		t.Fatalf("row key %q is not the composite key", keys[0])
	}
	// A complete-looking file: right ids, right annotator, right vocabulary — just keyed on
	// the claim. Every per-label rule passes; only the unit is wrong.
	ls := []Label{
		{ItemID: claim, Annotator: "a1", Verdict: "supported", QuoteStatus: "not_provided"},
		{ItemID: claim, Annotator: "a1", Verdict: "not_found", QuoteStatus: "not_provided"},
	}
	path := filepath.Join(t.TempDir(), "labels-a.jsonl")
	writeLabels(t, path, ls)

	code, _, stderr := chainRun(t, "labels", "--check", "--worksheet", ws, "--labels", path)
	if code == exitOK {
		t.Fatalf("`labels --check` accepted a claim-keyed file on the candidates side")
	}
	if !strings.Contains(stderr, "candidate_index") {
		t.Fatalf("refusal does not name the missing half of the key: %s", stderr)
	}

	// And κ refuses it too, with the same sentence: two readers of one rule.
	code, _, stderr = chainRun(t, "kappa", "--worksheet", ws, "--labels", path)
	if code == exitOK {
		t.Fatalf("`kappa` accepted a claim-keyed file on the candidates side")
	}
	if !strings.Contains(stderr, "candidate_index") {
		t.Fatalf("κ refusal does not name the missing half of the key: %s", stderr)
	}
}

// κ's denominator is the worksheet's unit. On the candidates side that is rows, and the
// receipt has to say both numbers, because "κ over 3 pairs" on a 2-claim file otherwise
// reads as a bug in the arithmetic.
func TestCandidateKappaCountsRowsNotClaims(t *testing.T) {
	ws, keys := twoCandidateWorksheet(t)
	var a, b []Label
	for _, k := range keys {
		id, _, _ := strings.Cut(k, "#")
		idx := candIndex(t, k)
		a = append(a, Label{ItemID: id, CandidateIndex: idx, Annotator: "a1",
			Verdict: "supported", QuoteStatus: "not_provided"})
		b = append(b, Label{ItemID: id, CandidateIndex: idx, Annotator: "b1",
			Verdict: "supported", QuoteStatus: "not_provided"})
	}
	path := filepath.Join(t.TempDir(), "labels.jsonl")
	writeLabels(t, path, append(append([]Label{}, a...), b...))

	code, stdout, stderr := chainRun(t, "kappa", "--worksheet", ws, "--labels", path)
	if code != exitOK {
		t.Fatalf("kappa exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, `"worksheet_items": 3`) {
		t.Fatalf("κ receipt does not count 3 rows: %s", stdout)
	}
	if !strings.Contains(stdout, `"worksheet_item_ids": 2`) {
		t.Fatalf("κ receipt does not count 2 claims: %s", stdout)
	}
	if !strings.Contains(stdout, `"items_labelled_by_all": 3`) {
		t.Fatalf("κ did not pair all three rows: %s", stdout)
	}
}

// `assemble` refuses the candidates side by name. It has to: gold v1 is one row per claim,
// so merging two candidate rows into one would put a verdict on a chunk nobody judged — and
// the file would still pass every other check, just narrower.
func TestAssembleRefusesACandidatesSideLabelFile(t *testing.T) {
	ws, keys := twoCandidateWorksheet(t)
	items, err := readItems(ws)
	if err != nil {
		t.Fatalf("readItems: %v", err)
	}
	pool := filepath.Join(t.TempDir(), "pool.jsonl")
	if err := writeSample(pool, items); err != nil {
		t.Fatalf("write pool: %v", err)
	}
	var a, b []Label
	for _, k := range keys {
		id, _, _ := strings.Cut(k, "#")
		idx := candIndex(t, k)
		a = append(a, Label{ItemID: id, CandidateIndex: idx, Annotator: "a1",
			Verdict: "supported", QuoteStatus: "not_provided"})
		b = append(b, Label{ItemID: id, CandidateIndex: idx, Annotator: "b1",
			Verdict: "supported", QuoteStatus: "not_provided"})
	}
	dir := t.TempDir()
	aPath, bPath := filepath.Join(dir, "labels-a.jsonl"), filepath.Join(dir, "labels-b.jsonl")
	writeLabels(t, aPath, a)
	writeLabels(t, bPath, b)

	code, _, stderr := chainRun(t, "assemble", "--pool", pool, "--labels-a", aPath, "--labels-b", bPath,
		"--out", t.TempDir())
	if code == exitOK {
		t.Fatalf("`assemble` merged a candidates-side label file into a one-row-per-claim gold file")
	}
	if !strings.Contains(stderr, "(item_id, candidate_index)") {
		t.Fatalf("refusal does not name the key it cannot use: %s", stderr)
	}
}

// The adjudication stub is derived from the two label files, so it inherits the unit: a
// disagreement about candidate 1 of a two-candidate claim is one stub row for that row, not
// one row for the claim.
func TestAdjudicateStubKeysTheCandidatesSide(t *testing.T) {
	ws, keys := twoCandidateWorksheet(t)
	var a, b []Label
	for n, k := range keys {
		id, _, _ := strings.Cut(k, "#")
		idx := candIndex(t, k)
		verdictA, verdictB := "supported", "supported"
		if n == 1 {
			verdictA, verdictB = "supported", "not_found"
		}
		a = append(a, Label{ItemID: id, CandidateIndex: idx, Annotator: "a1",
			Verdict: verdictA, QuoteStatus: "not_provided"})
		b = append(b, Label{ItemID: id, CandidateIndex: idx, Annotator: "b1",
			Verdict: verdictB, QuoteStatus: "not_provided"})
	}
	dir := t.TempDir()
	aPath, bPath := filepath.Join(dir, "labels-a.jsonl"), filepath.Join(dir, "labels-b.jsonl")
	writeLabels(t, aPath, a)
	writeLabels(t, bPath, b)

	stubOut := filepath.Join(dir, "adj.jsonl")
	code, stdout, stderr := chainRun(t, "adjudicate", "--stub", "--worksheet", ws,
		"--labels-a", aPath, "--labels-b", bPath, "--out", stubOut)
	if code != exitOK {
		t.Fatalf("adjudicate --stub exit %d: %s", code, stderr)
	}
	raw, err := os.ReadFile(stubOut)
	if err != nil {
		t.Fatalf("read stub: %v", err)
	}
	var rows []adjStubRow
	for _, ln := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var r adjStubRow
		if err := json.Unmarshal([]byte(ln), &r); err != nil {
			t.Fatalf("stub line is not JSON: %v", err)
		}
		rows = append(rows, r)
	}
	if len(rows) != 1 {
		t.Fatalf("stub wrote %d rows for one disagreement, want 1: %s", len(rows), raw)
	}
	if rows[0].CandidateIndex == nil {
		t.Fatalf("stub row for %s carries no candidate_index: the adjudicator would decide the wrong "+
			"candidate", rows[0].ItemID)
	}
	if !strings.Contains(stdout, `"item_ids": 2`) {
		t.Fatalf("stub receipt does not separate rows from claims: %s", stdout)
	}
}
