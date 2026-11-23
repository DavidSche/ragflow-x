package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The material half of the pool receipt exists because doc/304 §1.3 W-2's re-scan
// question ("do the per-stratum candidate counts still support the frozen plan?") had
// no reading in `goldtool pool`'s output — the three numbers were computed by a person
// grepping the JSONL. These tests pin the three properties that make the reading
// trustworthy, and the two ways it could quietly stop meaning anything:
//
//   - reporting the ceiling in *items* when the sampler draws *rows*, which turns 36
//     frozen candidate rows spread over 30 items into a ceiling of 30 (the same
//     denominator mistake doc/304 §1.3 W-1 warns about);
//   - dropping `cited_elsewhere` rows, which are the only survivors of the section-level
//     dedup (304 D-08) and therefore the bulk of the material that exists at all.

func item(candidates int, citedElsewhere bool, asOf string) Item {
	it := Item{ItemID: "i", ClaimID: "c", AsOfDate: asOf}
	for n := 0; n < candidates; n++ {
		it.CandidatePool = append(it.CandidatePool, Candidate{ChunkID: "chunk", CitedElsewhere: citedElsewhere})
	}
	return it
}

func TestPoolMaterialCountsItemsNotRows(t *testing.T) {
	items := []Item{
		item(3, false, "2026-02-14"),
		item(1, false, "2026-02-14"),
		item(0, false, "2026-02-14"),
	}
	got := poolMaterialOf(items)
	if got.ItemsWithCandidate != 2 {
		t.Fatalf("items_with_candidate_rows = %d, want 2 (one item carries 3 rows, which is still one item)", got.ItemsWithCandidate)
	}
	if got.ItemsWithoutCandidate != 1 {
		t.Fatalf("items_without_candidate_rows = %d, want 1", got.ItemsWithoutCandidate)
	}
	if got.CandidateRows != 4 {
		t.Fatalf("candidate_rows = %d, want 4 (3 + 1 + 0)", got.CandidateRows)
	}
	// The ceiling is in rows: one item carrying three candidates can yield three
	// conflicting samples, so reporting 2 here would understate the material and
	// reporting 2 would also be the "items" denominator the sampler does not use.
	if got.ConflictingUpperBound != got.CandidateRows {
		t.Fatalf("conflicting_upper_bound = %d, want %d (the ceiling is rows, not items)",
			got.ConflictingUpperBound, got.CandidateRows)
	}
	if got.ConflictingUpperBound == got.ItemsWithCandidate {
		t.Fatalf("conflicting_upper_bound = %d equals items_with_candidate_rows; "+
			"a fixture where rows and items coincide cannot tell the two units apart",
			got.ConflictingUpperBound)
	}
	if got.ItemsWithCandidate+got.ItemsWithoutCandidate != len(items) {
		t.Fatalf("the two counts do not add up to the export: %d + %d != %d",
			got.ItemsWithCandidate, got.ItemsWithoutCandidate, len(items))
	}
}

// A `cited_elsewhere` row is still a frozen candidate row: doc/301 §9.1's E2 rule counts
// it, and D-08 made it the only survivor of the section-level dedup. If a future change
// decided these rows were "not really candidates", the ceiling would collapse toward zero
// while the receipt still looked plausible — one early corpus had *every* row in this
// state.
func TestPoolMaterialKeepsCitedElsewhereRows(t *testing.T) {
	items := []Item{
		item(2, true, "2026-02-14"),
		item(0, false, "2026-02-14"),
	}
	got := poolMaterialOf(items)
	if got.ItemsWithCandidate != 1 || got.CandidateRows != 2 || got.ConflictingUpperBound != 2 {
		t.Fatalf("cited_elsewhere rows were dropped: with=%d rows=%d bound=%d, want 1/2/2",
			got.ItemsWithCandidate, got.CandidateRows, got.ConflictingUpperBound)
	}
}

// The stamp axis is *provenance*, and the field name says so: `as_of_date` is
// `stamp(time.Now())` at export time, so one distinct value means the export ran on one day.
// An earlier round of doc/304 read it as "the corpus is a single snapshot" — this test keeps
// the reading from drifting back into a corpus claim, which is why it also asserts the
// note that travels with the field.
func TestPoolMaterialAsOfDatesAreDistinctSortedAndHonest(t *testing.T) {
	items := []Item{
		item(0, false, "2026-02-14"),
		item(0, false, "2026-02-14"),
		item(0, false, "  2026-01-02  "),
		item(0, false, ""),
	}
	got := poolMaterialOf(items)
	want := []string{"2026-01-02", "2026-02-14"}
	if len(got.ExportStampDates) != len(want) {
		t.Fatalf("export_stamp_dates = %v, want %v (distinct, trimmed)", got.ExportStampDates, want)
	}
	for i := range want {
		if got.ExportStampDates[i] != want[i] {
			t.Fatalf("export_stamp_dates = %v, want %v (sorted)", got.ExportStampDates, want)
		}
	}
	if got.ItemsWithoutStamp != 1 {
		t.Fatalf("items_without_export_stamp = %d, want 1 (a missing date is counted, not invented)",
			got.ItemsWithoutStamp)
	}
}

func TestPoolMaterialEmptyExportIsZeroNotMissing(t *testing.T) {
	got := poolMaterialOf(nil)
	if got.ItemsWithCandidate != 0 || got.ItemsWithoutCandidate != 0 || got.ConflictingUpperBound != 0 ||
		got.CandidateRows != 0 {
		t.Fatalf("empty export produced non-zero counts: %+v", got)
	}
	if got.ExportStampDates == nil {
		t.Fatal("export_stamp_dates is nil; a JSON `null` reads as \"not measured\" where 0 items were measured")
	}
	if len(got.NotMeasured) == 0 {
		t.Fatal("not_measured is empty: a green conflicting bound would then read as a covered plan")
	}
	if got.Note == "" {
		t.Fatal("note is empty: the receipt has to say the bound is a ceiling, not a quota")
	}
}

// The note is the only place a reader learns that the ceiling is not permission. Pinning
// the two claims keeps a future edit from turning "ceiling" into "quota" without a test
// noticing.
func TestPoolMaterialNoteKeepsTheTwoClaims(t *testing.T) {
	got := poolMaterialOf(nil)
	for _, want := range []string{"candidate rows", "ceiling in rows", "not a quota",
		"does not change the frozen plan"} {
		if !strings.Contains(got.Note, want) {
			t.Fatalf("receipt note no longer says %q: %s", want, got.Note)
		}
	}
	for _, want := range []string{"stamp(time.Now())", "says nothing about the time the corpus spans"} {
		if !strings.Contains(poolMaterialStampNote, want) {
			t.Fatalf("export stamp note no longer says %q: %s", want, poolMaterialStampNote)
		}
	}
}

// The field names are the interface: a rename would leave the reading out of every
// archived receipt without failing anything.
func TestPoolMaterialJSONNames(t *testing.T) {
	receipt := poolExport{
		ClaimTypes:   map[string]int{},
		PriorSupport: map[string]int{},
		Material:     poolMaterialOf([]Item{item(1, false, "2026-02-14")}),
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	material, ok := back["material"].(map[string]any)
	if !ok {
		t.Fatalf("receipt has no `material` object: %s", raw)
	}
	for _, key := range []string{
		"items_with_candidate_rows",
		"items_without_candidate_rows",
		"candidate_rows",
		"export_stamp_dates",
		"items_without_export_stamp",
		"conflicting_upper_bound",
		"not_measured",
		"note",
	} {
		if _, ok := material[key]; !ok {
			t.Fatalf("material object is missing %q: %s", key, raw)
		}
	}
	if bound, _ := material["conflicting_upper_bound"].(float64); int(bound) != 1 {
		t.Fatalf("conflicting_upper_bound did not survive marshal: %s", raw)
	}
	if rows, _ := material["candidate_rows"].(float64); int(rows) != 1 {
		t.Fatalf("candidate_rows did not survive marshal: %s", raw)
	}
}

// poolMaterialRun drives `goldtool pool-material` the way W-2's re-scan does: an archived
// pool file in, the material reading out, no DSN and no database.
func poolMaterialRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "pm-out-*")
	if err != nil {
		t.Fatalf("stdout temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "pm-err-*")
	if err != nil {
		t.Fatalf("stderr temp: %v", err)
	}
	defer stderr.Close()
	code := run(append([]string{"pool-material"}, args...), stdout, stderr)
	return code, readAll(t, stdout.Name()), readAll(t, stderr.Name())
}

// writePoolJSONL writes a pool export through the *same* encoder `pool` uses, so the
// fixture cannot drift away from the file the command will meet in production.
func writePoolJSONL(t *testing.T, items []Item) string {
	t.Helper()
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatalf("encode items: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pool.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write pool: %v", err)
	}
	return path
}

// A pool that can support the frozen plan exits 0 and says so in a field — not by the
// absence of a complaint.
func TestPoolMaterialCommandReadsThePlanFromTheTree(t *testing.T) {
	need := GoldDistribution[model.ReportVerdictConflicting]
	items := make([]Item, 0, need)
	for i := 0; i < need; i++ {
		items = append(items, item(1, false, "2026-02-14"))
	}
	code, out, errOut := poolMaterialRun(t, "--in", writePoolJSONL(t, items))
	if code != exitOK {
		t.Fatalf("exit = %d, want 0 (stderr %s)", code, errOut)
	}
	var got poolMaterialExport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not the receipt JSON: %v\n%s", err, out)
	}
	if got.Plan.Conflicting != need {
		t.Fatalf("frozen_plan.conflicting = %d, want %d read from GoldDistribution",
			got.Plan.Conflicting, need)
	}
	if !strings.Contains(got.StampNote, "stamp(time.Now())") {
		t.Fatalf("receipt does not carry the stamp note: %q", got.StampNote)
	}
	if len(got.Material.NotMeasured) == 0 || got.Material.NotMeasured[0].Stratum != model.ReportVerdictOutdated {
		t.Fatalf("receipt does not name the stratum it cannot count: %+v", got.Material.NotMeasured)
	}
	if !got.Plan.Reachable || got.Material.ConflictingUpperBound != need {
		t.Fatalf("reachable=%v bound=%d, want true/%d", got.Plan.Reachable,
			got.Material.ConflictingUpperBound, need)
	}
	if got.Material.ConflictingUpperBound < got.Plan.Conflicting {
		t.Fatal("reachable is true while the bound is below the plan")
	}
}

// One row short is a 3, and the number that failed is printed: the operator has to learn
// "23 of 24" from the receipt, not from re-deriving it.
func TestPoolMaterialCommandFallsShortByARow(t *testing.T) {
	need := GoldDistribution[model.ReportVerdictConflicting]
	items := make([]Item, 0, need)
	for i := 0; i < need-1; i++ {
		items = append(items, item(1, false, "2026-02-14"))
	}
	code, out, errOut := poolMaterialRun(t, "--in", writePoolJSONL(t, items))
	if code != exitIncomplete {
		t.Fatalf("exit = %d, want 3 (stderr %s)", code, errOut)
	}
	var got poolMaterialExport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not the receipt JSON: %v\n%s", err, out)
	}
	if got.Plan.Reachable {
		t.Fatal("reachable is true with one row fewer than the plan needs")
	}
	if got.Material.ConflictingUpperBound != need-1 {
		t.Fatalf("bound = %d, want %d", got.Material.ConflictingUpperBound, need-1)
	}
}

// A corpus that has not been re-scanned is a 3, not a 0: an export with no candidate rows
// is the current state of the tree (doc/304 E64) and must not read as "planned and fine".
func TestPoolMaterialCommandEmptyCandidateSideIsNotAGreen(t *testing.T) {
	items := []Item{item(0, false, "2026-02-14"), item(0, false, "2026-02-14")}
	code, out, _ := poolMaterialRun(t, "--in", writePoolJSONL(t, items))
	if code != exitIncomplete {
		t.Fatalf("exit = %d, want 3", code)
	}
	if strings.Contains(out, `"reachable": true`) {
		t.Fatalf("an export with zero candidate rows reported reachable:\n%s", out)
	}
}

// Missing flag and unreadable file are 2 with the reason on stderr — "usage", not "the
// corpus is short", because the two send the operator to different places.
func TestPoolMaterialCommandUsageErrors(t *testing.T) {
	if code, _, errOut := poolMaterialRun(t); code != exitUsage || !strings.Contains(errOut, "--in is required") {
		t.Fatalf("no --in: exit=%d stderr=%q, want 2 with --in is required", code, errOut)
	}
	code, out, errOut := poolMaterialRun(t, "--in", filepath.Join(t.TempDir(), "absent.jsonl"))
	if code != exitUsage {
		t.Fatalf("absent file: exit = %d, want 2", code)
	}
	if out != "" {
		t.Fatalf("absent file printed a receipt:\n%s", out)
	}
	if !strings.Contains(errOut, "absent.jsonl") {
		t.Fatalf("stderr does not name the file it could not read: %q", errOut)
	}
}
