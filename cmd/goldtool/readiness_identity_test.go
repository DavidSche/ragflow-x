package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The A and B blocks can each be correct while the *combination* is a category error: a
// 120-row worksheet drawn from last week's pool, a pool re-exported after a re-freeze, a
// prereg edited after its digest was written. None of those changes a single count, so no
// count can catch them. These tests are about the chain, not about the numbers.

// repairedPrereg rewrites the frozen plan's `inputs[]` the way `prereg --freeze` would, so a
// test can make exactly one thing wrong at a time.
func editPrereg(t *testing.T, path string, edit func(doc map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A pool can be frozen, a worksheet can be sampled from it, and the plan can still name a
// different worksheet digest — which is precisely what a re-freeze after a resample looks
// like. Every row count agrees; the receipt must not.
func TestReadinessRefusesAWorksheetFromAnotherBatch(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)
	editPrereg(t, prereg, func(doc map[string]any) {
		for _, row := range doc["inputs"].([]any) {
			m := row.(map[string]any)
			if m["role"] == "sample" {
				m["sha256"] = strings.Repeat("0", 64)
			}
		}
	})
	// Re-freeze the companion digest so the *only* disagreement left is the sample row: without
	// this the test would also be about "the prereg changed", and would pass for that reason.
	writePreregDigestOnly(t, prereg)

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", completePair(t, f))
	if code != exitInvariant {
		t.Fatalf("a worksheet from another batch is an invariant, got exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.Identity.State != readinessFail || rec.Ready {
		t.Fatalf("identity must fail: %+v", rec.Identity)
	}
	if !strings.Contains(rec.Identity.Reason, "sample") {
		t.Fatalf("the reason must name the file that disagreed: %s", rec.Identity.Reason)
	}
	// A and B are untouched by this: the point is that only identity can see it.
	if rec.A.State == readinessFail || rec.B.State != readinessPass {
		t.Fatalf("this test must fail on identity alone: A=%s B=%s", rec.A.State, rec.B.State)
	}
	var sample readinessInput
	for _, in := range rec.Identity.Inputs {
		if in.Role == "sample" {
			sample = in
		}
	}
	if sample.State != identityMismatch || sample.FrozenSHA256 == sample.SHA256 {
		t.Fatalf("the row must carry both digests so a reader sees them disagree: %+v", sample)
	}
}

// The relation a digest cannot express: a sheet can hash to itself and still carry item ids
// the pool never had. "The worksheet was drawn from this pool" is a key fact, not a byte fact.
func TestReadinessRefusesAWorksheetItemTheePoolNeverHad(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})

	// Same row count, same vocabulary, one item that is not in the pool.
	stray := f.items[0]
	stray.ItemID = "ffffffffffffffffffffffffffffffff"
	wsPath := filepath.Join(f.dir, "stray-worksheet.jsonl")
	lines, err := encodeItems([]Item{f.items[1], f.items[2], stray})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(wsPath, lines); err != nil {
		t.Fatal(err)
	}
	freezePrereg(t, prereg, pool, wsPath)

	code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", wsPath, "--prereg", prereg)
	if code != exitInvariant {
		t.Fatalf("an item outside the pool is an invariant, got exit %d\n%s", code, out)
	}
	rec := readinessReceipt(t, out)
	if rec.Identity.WorksheetItemsOutsidePool != 1 {
		t.Fatalf("the receipt must count the stray items, got %d", rec.Identity.WorksheetItemsOutsidePool)
	}
	if rec.Identity.State != readinessFail {
		t.Fatalf("a stray item fails identity even with a matching digest: %+v", rec.Identity)
	}
}

// The receipt has to be reproducible by someone who was not here. That means every file the
// reading touched, with its digest — not only the plan.
func TestReadinessReportsTheWholeInputChain(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)
	labels := completePair(t, f)

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitOK {
		t.Fatalf("a frozen, drawn-from-this-pool batch must pass identity: exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	// One row per file read, including each annotation file: the chain has to be reproducible,
	// and "labels" is two files here (qa-a and pm-b) because one file is one person.
	if rec.Identity.State != readinessPass || len(rec.Identity.Inputs) != 5 {
		t.Fatalf("identity must list prereg/pool/sample/labels x2: %+v", rec.Identity.Inputs)
	}
	roles := map[string]readinessInput{}
	for _, in := range rec.Identity.Inputs {
		roles[in.Role] = in
		if len(in.SHA256) != 64 || in.Bytes == 0 || in.Rows == 0 {
			t.Fatalf("%s must carry sha256/bytes/rows: %+v", in.Role, in)
		}
	}
	if roles["pool"].State != identityMatch || roles["pool"].FrozenSHA256 != roles["pool"].SHA256 {
		t.Fatalf("pool must equal the frozen digest: %+v", roles["pool"])
	}
	if len(roles) != 4 {
		t.Fatalf("four roles: prereg/pool/sample/labels, got %d", len(roles))
	}
	if roles["labels"].State != identityByDesign {
		t.Fatalf("labels have no frozen reference and must say so, got %q", roles["labels"].State)
	}
	if rec.Identity.PreregDigest.State != identityMatch || !rec.Identity.PreregDigest.Present {
		t.Fatalf("the companion digest must be read and matched: %+v", rec.Identity.PreregDigest)
	}
}

// `prereg --digest` exists so that "this is the frozen plan" is checkable later. A prereg
// edited after the digest was written is the case that check is for; it must fail the chain,
// not silently pass because the file is well-formed.
func TestReadinessRefusesAPreregEditedAfterItsDigest(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)

	// Inert-looking edit: nothing this command counts changes, only the frozen text.
	editPrereg(t, prereg, func(doc map[string]any) { doc["sample_id"] = "primary-random-20261122" })

	code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", completePair(t, f))
	if code != exitInvariant {
		t.Fatalf("a prereg edited after its digest must fail, got exit %d\n%s", code, out)
	}
	rec := readinessReceipt(t, out)
	if rec.Identity.PreregDigest.State != identityMismatch {
		t.Fatalf("the digest row must report the mismatch: %+v", rec.Identity.PreregDigest)
	}
	if rec.Identity.State != readinessFail {
		t.Fatalf("a stale digest is a fail even though every input row still matches: %+v", rec.Identity)
	}
	if !strings.Contains(rec.Identity.Reason, "prereg") {
		t.Fatalf("the reason must name the prereg: %s", rec.Identity.Reason)
	}
}

// A census that only counts "how many rows are filled" misses the two shapes that make the
// count lie: the same cell judged twice, and a row for something the sheet never asked. Both
// make κ a number over a set the worksheet does not describe.
func TestReadinessCountsDuplicateAndExtraLabelRows(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)

	qa := f.answered("qa-a", "supported")
	dup := append(append([]Label{}, qa...), qa[0]) // the same cell, twice
	stray := f.answered("pm-b", "supported")
	stray[0].ItemID = "not-in-the-worksheet"
	labels := f.file(t, "qa-a.jsonl", dup) + "," + f.file(t, "pm-b.jsonl", stray)

	code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitInvariant {
		t.Fatalf("duplicate and unknown rows are refused, got exit %d\n%s", code, out)
	}
	rec := readinessReceipt(t, out)
	if rec.B.DuplicateRows != 1 || rec.B.ExtraRows != 1 {
		t.Fatalf("the receipt must count both shapes: duplicate=%d extra=%d",
			rec.B.DuplicateRows, rec.B.ExtraRows)
	}
	if rec.B.Kappa != nil {
		t.Fatalf("no κ over a set that includes a row the sheet never asked for: %+v", rec.B)
	}
	if rec.Identity.LabelsRowsOutsideWorksheet != 1 {
		t.Fatalf("identity must repeat the same count (one implementation): %d",
			rec.Identity.LabelsRowsOutsideWorksheet)
	}
}

// Annotator identity is the row's `annotator` field, not the file name. Two files can carry
// the same name — which reads as one person — and one file can carry two names, which reads
// as two people who never disagree with themselves.
func TestReadinessRefusesNamesThatDoNotAddUpToTwoPeople(t *testing.T) {
	build := func(t *testing.T) (string, string, string) {
		f := newKappaFixture(t)
		pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
		prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
		freezePrereg(t, prereg, pool, f.worksheet)
		return f.dir, pool, prereg
	}

	t.Run("same name in two files", func(t *testing.T) {
		dir, pool, prereg := build(t)
		f := &kappaFixture{dir: dir, worksheet: filepath.Join(dir, "worksheet.jsonl"),
			items: itemsOf(t, filepath.Join(dir, "worksheet.jsonl"))}
		// Two "annotators", both signing qa-a: distinct_annotators would be 1, and a staffing
		// report would say "find a second person" — but the second file exists.
		labels := f.file(t, "run1.jsonl", f.answered("qa-a", "supported")) + "," +
			f.file(t, "run2.jsonl", f.answered("qa-a", "supported"))
		code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
			"--prereg", prereg, "--labels", labels)
		if code != exitInvariant {
			t.Fatalf("exit = %d, want 1\n%s", code, out)
		}
		rec := readinessReceipt(t, out)
		if !hasProblem(rec.B.Problems, "annotator_name_in_two_files") {
			t.Fatalf("the problem must name the shape: %+v", rec.B.Problems)
		}
		if !strings.Contains(rec.B.Reason, "身份") {
			t.Fatalf("the reason must say this is an identity problem, not staffing: %s", rec.B.Reason)
		}
	})

	t.Run("two names in one file", func(t *testing.T) {
		dir, pool, prereg := build(t)
		f := &kappaFixture{dir: dir, worksheet: filepath.Join(dir, "worksheet.jsonl"),
			items: itemsOf(t, filepath.Join(dir, "worksheet.jsonl"))}
		merged := f.file(t, "merged.jsonl",
			append(f.answered("qa-a", "supported"), f.answered("pm-b", "supported")...))
		code, out, _ := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
			"--prereg", prereg, "--labels", merged)
		if code != exitInvariant {
			t.Fatalf("exit = %d, want 1\n%s", code, out)
		}
		rec := readinessReceipt(t, out)
		if !hasProblem(rec.B.Problems, "label_file_mixes_annotators") {
			t.Fatalf("a merged file cannot say whose row is whose: %+v", rec.B.Problems)
		}
	})
}

// The κ recipe is a description of frozen choices, so it travels in every receipt — and there
// is no flag that moves the floor. `--min` used to exist; it does not any more, and an unknown
// flag is exit 2 rather than a silently ignored argument.
func TestReadinessFreezesTheKappaRecipeAndRefusesToBeTuned(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})
	freezePrereg(t, prereg, pool, f.worksheet)
	labels := completePair(t, f)

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels)
	if code != exitOK {
		t.Fatalf("exit %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	m := rec.B.Method
	if m.Statistic != "cohen_kappa_unweighted" || len(m.Classes) != len(GoldVerdicts) ||
		m.Floor != minKappa || !strings.Contains(m.Unit, "worksheet row") {
		t.Fatalf("the receipt must describe the arithmetic it used: %+v", m)
	}
	if !strings.Contains(m.Missing, "blocks κ") {
		t.Fatalf("missing-cell handling is part of the recipe: %q", m.Missing)
	}
	if !strings.Contains(m.FloorSource, "常量") {
		t.Fatalf("the floor must say where it comes from: %q", m.FloorSource)
	}

	if code, _, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", labels, "--min", "0.1"); code != exitUsage {
		t.Fatalf("a floor flag must not exist (exit 2), got %d\n%s", code, errText)
	}
}

// A prereg that never recorded what it was frozen against can prove nothing about the batch.
// That is `unverified`, not `pass`: the plan is well-formed, the files are well-formed, and
// the relation between them is simply not written down anywhere.
func TestReadinessNeedsAPreregThatRecordedItsInputs(t *testing.T) {
	f := newKappaFixture(t)
	pool := writeReadinessPool(t, f.dir, []int{3, 0, 0})
	prereg := writeReadinessPrereg(t, f.dir, "plan.json", 3, map[string]int{"conflicting": 3})

	code, out, errText := readinessRun(t, "--pool", pool, "--worksheet", f.worksheet,
		"--prereg", prereg, "--labels", completePair(t, f))
	if code != exitIncomplete {
		t.Fatalf("no recorded inputs and no digest is unverified (exit 3), got %d\n%s%s", code, out, errText)
	}
	rec := readinessReceipt(t, out)
	if rec.Identity.State != readinessUnverified || rec.Ready {
		t.Fatalf("identity must be unverified: %+v", rec.Identity)
	}
	if rec.A.State != readinessPass || rec.B.State != readinessPass {
		t.Fatalf("this test must be unverified on identity alone: A=%s B=%s", rec.A.State, rec.B.State)
	}
	var row readinessInput
	for _, in := range rec.Identity.Inputs {
		if in.Role == "pool" {
			row = in
		}
	}
	if row.State != identityNotRecorded || row.FrozenSHA256 != "" {
		t.Fatalf("the pool row must say nobody recorded it: %+v", row)
	}
}

func hasProblem(problems []Problem, code string) bool {
	for _, p := range problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

func itemsOf(t *testing.T, path string) []Item {
	t.Helper()
	items, err := readItems(path)
	if err != nil {
		t.Fatal(err)
	}
	return items
}
