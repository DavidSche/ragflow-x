package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gold file's population is what the two annotators judged, not what the pool
// happens to contain. These tests pin the two ways that matters: a pool bigger than the
// sample must be *one* statement (not one per untouched row), and a row one annotator
// touched but the other did not must still be named.
//
// The live run that produced E77 passed the 371-row pool for a 120-row sample and got 251
// `unlabelled_item` rows; the six rows that were actually wrong were inside that noise.

// unlabelledRows is this file's own pool builder: `poolOf` in sample_test.go writes a
// file, and these tests need the slice.
func unlabelledRows(n int) []Item {
	pool := make([]Item, 0, n)
	for i := 1; i <= n; i++ {
		pool = append(pool, sampleItem(i, ""))
	}
	return pool
}

func agreeOn(items []Item, from, to int, who string) []Label {
	out := make([]Label, 0, to-from)
	for _, it := range items[from:to] {
		out = append(out, Label{ItemID: it.ItemID, Annotator: who,
			Verdict: "supported", QuoteStatus: "not_provided"})
	}
	return out
}

func messageOf(problems []Problem, code string) string {
	for _, p := range problems {
		if p.Code == code {
			return p.Message
		}
	}
	return ""
}

func TestAssembleNamesAPoolBiggerThanTheSampleOnce(t *testing.T) {
	pool := unlabelledRows(6)
	var a, b []Label
	for _, who := range []string{"qa", "pm"} {
		ls := agreeOn(pool, 0, 4, who) // the sample is 4 rows of a 6-row pool
		if who == "qa" {
			a = ls
		} else {
			b = ls
		}
	}

	gold, problems := assembleItems(pool, a, b, nil, true)
	if len(gold) != 4 {
		t.Fatalf("gold should carry the 4 judged rows, got %d", len(gold))
	}
	cs := codes(problems)
	if cs["pool_outside_sample"] != 1 {
		t.Fatalf("a pool bigger than the sample is one statement, not %d rows: %v",
			cs["unlabelled_item"], cs)
	}
	if cs["unlabelled_item"] != 0 {
		t.Fatalf("no annotator touched the extra rows, so they are not missing work: %v", cs)
	}
	msg := messageOf(problems, "pool_outside_sample")
	for _, want := range []string{"2 of 6", "judge 4", pool[5].ItemID, "--pool"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the statement has to carry %q, and carry the fix; got %q", want, msg)
		}
	}
}

func TestAssembleStillNamesTheItemOnlyOneAnnotatorTouched(t *testing.T) {
	pool := unlabelledRows(3)
	a := agreeOn(pool, 0, 3, "qa")
	b := agreeOn(pool, 0, 2, "pm") // the third row is only half judged

	gold, problems := assembleItems(pool, a, b, nil, true)
	if len(gold) != 2 {
		t.Fatalf("a row labelled by one person stays out of gold, got %d items", len(gold))
	}
	cs := codes(problems)
	if cs["unlabelled_item"] != 1 {
		t.Fatalf("the half-judged row must be named: %v", cs)
	}
	if cs["pool_outside_sample"] != 0 {
		t.Fatalf("a row someone touched is not a population problem: %v", cs)
	}
	if msg := messageOf(problems, "unlabelled_item"); !strings.Contains(msg, pool[2].ItemID) ||
		!strings.Contains(msg, "1 of 2") {
		t.Fatalf("the named row must be the one short an annotator; got %q", msg)
	}
}

// readManifest loads the manifest assemble wrote.
func readManifest(t *testing.T, outDir, version string) manifest {
	t.Helper()
	path := filepath.Join(outDir, "report-assurance-gold."+version+".manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var man manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return man
}

// writePoolFile writes a pool-shaped file and returns its path.
func writePoolFile(t *testing.T, dir string, items []Item) string {
	t.Helper()
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

// A refused assembly must not leave a manifest that reads like a pass. Before E77 the
// manifest copied the κ receipt struct, which assembly cannot fill: a 262-problem run
// wrote `ok: true`, `worksheet_items: 0`, `unfinished: false`.
func TestRejectedAssemblyManifestSaysOKFalse(t *testing.T) {
	dir := t.TempDir()
	pool := unlabelledRows(5)
	poolPath := writePoolFile(t, dir, pool)
	writeLabels(t, filepath.Join(dir, "a.jsonl"), agreeOn(pool, 0, 5, "qa"))
	writeLabels(t, filepath.Join(dir, "b.jsonl"), agreeOn(pool, 0, 4, "pm")) // one row short

	outDir := filepath.Join(dir, "gold")
	code := run([]string{"assemble", "--pool", poolPath,
		"--labels-a", filepath.Join(dir, "a.jsonl"),
		"--labels-b", filepath.Join(dir, "b.jsonl"),
		"--version", "v1", "--out", outDir}, devnull(t), devnull(t))
	if code != exitInvariant {
		t.Fatalf("an assembly with problems must exit 1, got %d", code)
	}

	man := readManifest(t, outDir, "v1")
	if man.Agreement.OK {
		t.Fatalf("a refused assembly wrote agreement.ok = true: %+v", man.Agreement)
	}
	if len(man.Problems) == 0 {
		t.Fatal("a refused assembly must say what was wrong")
	}
	if man.Agreement.Population != 5 {
		t.Fatalf("population_items should be the 5 items the files judge, got %d", man.Agreement.Population)
	}
	if man.Agreement.Pairs != 4 || man.Agreement.Dropped != 1 {
		t.Fatalf("1 of 5 items was dropped: pairs=%d dropped=%d",
			man.Agreement.Pairs, man.Agreement.Dropped)
	}
	if man.Items != 4 {
		t.Fatalf("manifest items = %d, want the 4 that survived", man.Items)
	}
}

// A pool that is bigger than the sample must be visible in the manifest too: the number
// under `population_items` is the sample, and the file an operator passed is `pool_file`.
func TestManifestSeparatesPopulationFromPool(t *testing.T) {
	dir := t.TempDir()
	pool := unlabelledRows(6)
	poolPath := writePoolFile(t, dir, pool)
	writeLabels(t, filepath.Join(dir, "a.jsonl"), agreeOn(pool, 0, 4, "qa"))
	writeLabels(t, filepath.Join(dir, "b.jsonl"), agreeOn(pool, 0, 4, "pm"))

	outDir := filepath.Join(dir, "gold")
	if code := run([]string{"assemble", "--pool", poolPath,
		"--labels-a", filepath.Join(dir, "a.jsonl"),
		"--labels-b", filepath.Join(dir, "b.jsonl"),
		"--version", "v1", "--out", outDir}, devnull(t), devnull(t)); code != exitInvariant {
		t.Fatalf("a 4-of-6 sample cannot assemble cleanly, got exit %d", code)
	}

	man := readManifest(t, outDir, "v1")
	if man.Agreement.Population != 4 {
		t.Fatalf("population_items = %d, want the 4 rows the files judge", man.Agreement.Population)
	}
	if man.PoolFile != "pool.jsonl" {
		t.Fatalf("pool_file = %q", man.PoolFile)
	}
	if codes(man.Problems)["pool_outside_sample"] != 1 || codes(man.Problems)["unlabelled_item"] != 0 {
		t.Fatalf("the manifest must carry the population statement, once: %v", codes(man.Problems))
	}
}
