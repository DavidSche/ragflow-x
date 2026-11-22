package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// adjudicateRun drives the subcommand the way a person would: receipt on stdout, refusals
// on stderr, the exit code is the sentence.
func adjudicateRun(t *testing.T, args ...string) (int, string, string) {
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
	code := run(append([]string{"adjudicate"}, args...), stdout, stderr)
	return code, readAll(t, stdout.Name()), readAll(t, stderr.Name())
}

// adjudicateFixture is five items covering the whole decision space of the merge: an
// agreed item, a verdict disagreement, a question-① disagreement under an agreed verdict, a
// candidate disagreement under an agreed verdict, and one item only annotator A labelled.
//
// The candidate item carries *two* frozen candidates, because the disagreement it models is
// "they picked different rows of the same pool", which one row cannot express.
type adjudicateFixture struct {
	dir       string
	worksheet string
	items     []Item
	a, b      []Label
}

func newAdjudicateFixture(t *testing.T) *adjudicateFixture {
	t.Helper()
	dir := t.TempDir()
	var items []Item
	for n := 1; n <= 5; n++ {
		it := sampleItem(n, "")
		it.GoldVerdict = ""
		it.GoldQuoteStatus = ""
		it.GoldQuote = ""
		it.GoldEvidenceIDs = nil
		it.Annotators = nil
		it.Notes = ""
		it.CandidatePool = nil
		items = append(items, it)
	}
	items[3].CandidatePool = []Candidate{
		{ChunkID: "chunk-a", ContentHash: "h-a", Content: "候选甲", PoolRank: 1},
		{ChunkID: "chunk-b", ContentHash: "h-b", Content: "候选乙", PoolRank: 2},
	}
	items[3].GoldScanScope = "cited+candidate:2"
	path := filepath.Join(dir, "worksheet.jsonl")
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}

	row := func(n int, annotator, verdict, status string) Label {
		return Label{
			ItemID: items[n-1].ItemID, Annotator: annotator,
			Verdict: verdict, QuoteStatus: status,
			Quote:       "摘录十二字",
			EvidenceIDs: []string{items[n-1].Evidence[0].SnapshotID},
		}
	}
	a := []Label{
		row(1, "qa-a", "supported", "verified"),
		row(2, "qa-a", "supported", "verified"), // B says unsupported
		row(3, "qa-a", "supported", "verified"), // B says ambiguous
		row(4, "qa-a", "conflicting", "verified"),
		row(5, "qa-a", "supported", "verified"), // B never judged it
	}
	b := []Label{
		row(1, "qa-b", "supported", "verified"),
		row(2, "qa-b", "unsupported", "verified"),
		row(3, "qa-b", "supported", "ambiguous"),
		row(4, "qa-b", "conflicting", "verified"),
	}
	a[3].CandidateChunkID = "chunk-a"
	b[3].CandidateChunkID = "chunk-b"

	f := &adjudicateFixture{dir: dir, worksheet: path, items: items, a: a, b: b}
	return f
}

func (f *adjudicateFixture) write(t *testing.T, name string, ls []Label) string {
	t.Helper()
	path := filepath.Join(f.dir, name)
	writeLabels(t, path, ls)
	return path
}

func (f *adjudicateFixture) stub(t *testing.T, aPath, bPath string) string {
	t.Helper()
	out := filepath.Join(f.dir, "adjudication.jsonl")
	code, _, errText := adjudicateRun(t, "--stub", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--out", out)
	if code != exitOK {
		t.Fatalf("stub exit = %d: %s", code, errText)
	}
	return out
}

func readAdjRows(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw := strings.TrimSpace(readAll(t, path))
	if raw == "" {
		return nil
	}
	var out []map[string]any
	for i, line := range strings.Split(raw, "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		out = append(out, row)
	}
	return out
}

// The invariant that keeps two readers of one rule in step: the items `adjudicate --stub`
// asks about must be exactly the items `assemble` would drop for want of a decision, and
// nothing else. Both directions matter — a stub that misses a row costs a merge retry, and a
// stub that adds one invites the adjudicator to overwrite an agreement.
func TestAdjudicateStubMatchesTheRowsAssembleCannotMerge(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)
	stub := f.stub(t, aPath, bPath)

	rows := readAdjRows(t, stub)
	stubIDs := map[string]bool{}
	for _, r := range rows {
		stubIDs[r["item_id"].(string)] = true
	}

	// The other reader: no adjudication file at all, so every case the merge cannot settle
	// becomes a named problem.
	_, problems := assembleItems(f.items, f.a, f.b, nil, true)
	mergeIDs := map[string]bool{}
	wantCodes := map[string]bool{
		"unadjudicated_disagreement": true,
		"quote_status_disagreement":  true,
		"candidate_ref_disagreement": true,
	}
	for _, p := range problems {
		if !wantCodes[p.Code] {
			continue
		}
		// The problem's message starts with the item id, and an item id itself contains a
		// colon (`rev02:claim002`), so the id is found by matching the fixture's ids rather
		// than by splitting the sentence.
		for _, it := range f.items {
			if strings.HasPrefix(p.Message, it.ItemID+":") {
				mergeIDs[it.ItemID] = true
			}
		}
	}

	if len(mergeIDs) != 3 {
		names := make([]string, 0, len(mergeIDs))
		for id := range mergeIDs {
			names = append(names, id)
		}
		sort.Strings(names)
		t.Fatalf("fixture should need three decisions, assemble named %d (%v): the fixture, not the "+
			"command, is what broke", len(mergeIDs), names)
	}
	for id := range mergeIDs {
		if !stubIDs[id] {
			t.Fatalf("assemble cannot merge %s but the stub never asked about it — the adjudicator would "+
				"discover it as a failed assemble", id)
		}
	}
	for id := range stubIDs {
		if !mergeIDs[id] {
			t.Fatalf("the stub asks about %s but assemble merges it without a decision — the row invites "+
				"overwriting an agreement", id)
		}
	}
}

// A stub is "the decisions, and only the decisions": the cells the two files already agree
// on are facts and are filled in; the cells they disagree on are blank. If a fact were blank
// the adjudicator would have to retype it (and could typo it), and if a decision were
// pre-filled the tool would be answering the question it asks.
func TestAdjudicateStubBlanksOnlyTheDecisions(t *testing.T) {
	f := newAdjudicateFixture(t)
	stub := f.stub(t, f.write(t, "labels-a.jsonl", f.a), f.write(t, "labels-b.jsonl", f.b))

	for _, row := range readAdjRows(t, stub) {
		id := row["item_id"].(string)
		kinds := map[string]bool{}
		for _, k := range row["disagrees_on"].([]any) {
			kinds[k.(string)] = true
		}
		for _, cell := range []string{adjKindVerdict, adjKindQuoteStatus} {
			value, _ := row[cell].(string)
			if kinds[cell] && value != "" {
				t.Fatalf("%s: %s is disputed but the stub pre-fills %q", id, cell, value)
			}
			if !kinds[cell] && value == "" {
				t.Fatalf("%s: %s is agreed by both files but the stub leaves it blank", id, cell)
			}
		}
		if row["adjudicator"].(string) != "" || row["reason"].(string) != "" {
			t.Fatalf("%s: the stub carries a decision (%v)", id, row)
		}
	}
}

// The file the stub writes is the file `assemble` reads once the blanks are filled: one
// file, no transcription step. The test fills only the blank cells and the two free-text
// fields, then asks the merge whether anything is left to complain about.
func TestAdjudicateFilledStubAssemblesWithoutProblems(t *testing.T) {
	f := newAdjudicateFixture(t)
	stub := f.stub(t, f.write(t, "labels-a.jsonl", f.a), f.write(t, "labels-b.jsonl", f.b))

	filled := fillStub(t, readAdjRows(t, stub), "unsupported", "ambiguous", "chunk-b")
	path := filepath.Join(f.dir, "adjudication-filled.jsonl")
	if err := writeJSONL(path, filled); err != nil {
		t.Fatal(err)
	}

	code, out, errText := adjudicateRun(t, "--check", "--worksheet", f.worksheet,
		"--labels-a", filepath.Join(f.dir, "labels-a.jsonl"),
		"--labels-b", filepath.Join(f.dir, "labels-b.jsonl"),
		"--adjudication", path)
	if code != exitOK {
		t.Fatalf("check exit = %d, want 0: %s%s", code, out, errText)
	}
	if !strings.Contains(out, "3/3 decided") {
		t.Fatalf("receipt should say 3/3 decided, got:\n%s", out)
	}

	adj, err := readAdjudications(path)
	if err != nil {
		t.Fatalf("assemble must be able to read the file the stub wrote: %v", err)
	}
	gold, problems := assembleItems(f.items, f.a, f.b, adj, true)
	// The half-labelled item is a coverage fact (`unlabelled_item`), not a decision: it stays
	// reported, and this test is about the three codes an adjudication can resolve.
	for _, p := range problems {
		if p.Code != "unlabelled_item" {
			t.Fatalf("a filled stub must assemble cleanly, got %v", problems)
		}
	}
	// Three decided items plus the agreed one; the half-labelled item is not gold.
	if len(gold) != 4 {
		t.Fatalf("gold has %d item(s), want 4 (three adjudicated + one agreed; the unpaired item is not a "+
			"gold standard)", len(gold))
	}
	for _, it := range gold {
		if it.Annotators[0] == it.Annotators[1] {
			t.Fatalf("%s: gold recorded one annotator twice: %v", it.ItemID, it.Annotators)
		}
	}
}

// Unfinished is exit 3, not a finding and not a pass — the same three sentences as `labels
// --check`. This is the state the real command is in today for the 120-item sample.
func TestAdjudicateCheckUnfinishedIsExit3(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)
	stub := f.stub(t, aPath, bPath)

	code, out, errText := adjudicateRun(t, "--check", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--adjudication", stub)
	if code != exitIncomplete {
		t.Fatalf("exit = %d, want %d (unfinished, not pass): %s", code, exitIncomplete, out)
	}
	if !strings.Contains(out, "0/3 decided") {
		t.Fatalf("receipt should say 0/3 decided, got:\n%s", out)
	}
	if !strings.Contains(errText, "unfinished (exit 3, not pass)") {
		t.Fatalf("stderr should say the file is unfinished, got:\n%s", errText)
	}
	if !strings.Contains(out, "undecided (3)") {
		t.Fatalf("the three undecided ids must be named, got:\n%s", out)
	}
}

// A stale file: the adjudicator filled two rows, and the third disagreement was labelled
// after the stub was written. Coverage comes from the two label files, so the missing row is
// visible without a receipt to compare against.
func TestAdjudicateCheckNamesRowsTheStubNeverSaw(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)

	// Stub from a world where item 3's quote_status disagreement did not exist yet.
	olderB := append([]Label{}, f.b...)
	for i := range olderB {
		if olderB[i].ItemID == f.items[2].ItemID {
			olderB[i].QuoteStatus = "verified"
		}
	}
	oldB := f.write(t, "labels-b-old.jsonl", olderB)
	stub := f.stub(t, aPath, oldB)

	filled := fillStub(t, readAdjRows(t, stub), "unsupported", "verified", "chunk-b")
	path := filepath.Join(f.dir, "stale.jsonl")
	if err := writeJSONL(path, filled); err != nil {
		t.Fatal(err)
	}

	code, out, _ := adjudicateRun(t, "--check", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--adjudication", path)
	if code != exitIncomplete {
		t.Fatalf("a stale adjudication file must be exit %d (unfinished), got %d:\n%s", exitIncomplete, code, out)
	}
	if !strings.Contains(out, "no row at all (1)") || !strings.Contains(out, f.items[2].ItemID) {
		t.Fatalf("the newly-disagreed item must be named as uncovered, got:\n%s", out)
	}
}

// The narrowness rule: a row may settle a disagreement, never edit an agreement.
func TestAdjudicateCheckRefusesEditingAnAgreedFact(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)
	stub := f.stub(t, aPath, bPath)

	rows := readAdjRows(t, stub)
	filled := fillStub(t, rows, "unsupported", "ambiguous", "chunk-b")
	for _, row := range rows {
		if row["item_id"] == f.items[1].ItemID {
			// Both files said `verified` on item 2 (a verdict disagreement, so question ① is
			// not a decision here); the row "tidies" it to `not_found`.
			row["quote_status"] = "not_found"
		}
	}
	filled = fillStub(t, rows, "unsupported", "ambiguous", "chunk-b")
	path := filepath.Join(f.dir, "edited-fact.jsonl")
	if err := writeJSONL(path, filled); err != nil {
		t.Fatal(err)
	}
	code, _, errText := adjudicateRun(t, "--check", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--adjudication", path)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d (a named violation): %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "adjudication_changed_agreed_fact") {
		t.Fatalf("the refusal must name the rule, got:\n%s", errText)
	}
}

// A row for an item the two annotators agreed on is not redundant, it is destructive: it
// replaces a judgement nobody disputed with a third person's, and the gold file records the
// adjudicator's name as if there had been a disagreement.
func TestAdjudicateCheckRefusesARowForAnAgreedItem(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)
	stub := f.stub(t, aPath, bPath)

	rows := fillStub(t, readAdjRows(t, stub), "unsupported", "verified", "chunk-b")
	extra := map[string]any{
		"item_id": f.items[0].ItemID, "verdict": "supported", "quote_status": "verified",
		"adjudicator": "pm", "reason": "两个人本来就一致，这一行是多余的",
	}
	raw, _ := json.Marshal(extra)
	rows = append(rows, string(raw))
	path := filepath.Join(f.dir, "extra.jsonl")
	if err := writeJSONL(path, rows); err != nil {
		t.Fatal(err)
	}

	code, _, errText := adjudicateRun(t, "--check", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--adjudication", path)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "adjudication_without_disagreement") {
		t.Fatalf("the refusal must name the rule, got:\n%s", errText)
	}
}

// "No disagreements" is the one answer this command can give that looks like a pass and
// means nothing. Two files from one person agree with themselves.
func TestAdjudicateRefusesTheSameAnnotatorTwice(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)
	aPath2 := f.write(t, "labels-a-again.jsonl", f.a)

	code, _, errText := adjudicateRun(t, "--stub", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", aPath2, "--out", filepath.Join(f.dir, "x.jsonl"))
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "same_annotator_twice") {
		t.Fatalf("the refusal must name the rule, got:\n%s", errText)
	}
	// And the honest pair still works: the refusal is about the names, not about refusing.
	if code, _, errText := adjudicateRun(t, "--stub", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--out", filepath.Join(f.dir, "y.jsonl")); code != exitOK {
		t.Fatalf("a two-person pair must be accepted, got %d: %s", code, errText)
	}
}

// A stub that can replace a filled file can erase reasons somebody wrote by reading the
// evidence. Second stubs go to a new path.
func TestAdjudicateStubRefusesToOverwrite(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)
	out := f.stub(t, aPath, bPath)

	code, _, errText := adjudicateRun(t, "--stub", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--out", out)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (usage: this path is taken): %s", code, exitUsage, errText)
	}
	if !strings.Contains(errText, "refusing to overwrite") {
		t.Fatalf("the refusal should say why, got:\n%s", errText)
	}
}

// A candidate reference is validated against the item's own frozen pool: a row that cites a
// chunk from another revision would make E2's `cited_elsewhere` grouping a guess.
func TestAdjudicateCheckRefusesACandidateOutsideTheItem(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.b)
	stub := f.stub(t, aPath, bPath)

	rows := fillStub(t, readAdjRows(t, stub), "unsupported", "ambiguous", "chunk-from-another-revision")
	path := filepath.Join(f.dir, "bad-candidate.jsonl")
	if err := writeJSONL(path, rows); err != nil {
		t.Fatal(err)
	}
	code, _, errText := adjudicateRun(t, "--check", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--adjudication", path)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "adjudication_candidate_unknown") {
		t.Fatalf("the refusal must name the rule, got:\n%s", errText)
	}
}

// The sample asks, it does not answer: handing `--stub` the assembled gold file would have
// the adjudicator read a later opinion while deciding between the two earlier ones.
func TestAdjudicateRefusesAGoldWorksheet(t *testing.T) {
	f := newAdjudicateFixture(t)
	answered := append([]Item{}, f.items...)
	answered[0].GoldVerdict = "supported"
	path := filepath.Join(f.dir, "gold.jsonl")
	lines, err := encodeItems(answered)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
	code, _, errText := adjudicateRun(t, "--stub", "--worksheet", path,
		"--labels-a", f.write(t, "labels-a.jsonl", f.a),
		"--labels-b", f.write(t, "labels-b.jsonl", f.b),
		"--out", filepath.Join(f.dir, "z.jsonl"))
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "pass the worksheet") {
		t.Fatalf("the refusal should point at the right file, got:\n%s", errText)
	}
}

// A symmetric pair needs no adjudication, and an empty stub is a legitimate, complete
// answer — not an error. `assemble` then needs no --adjudication at all.
func TestAdjudicateNoDisagreementsIsAnEmptyStub(t *testing.T) {
	f := newAdjudicateFixture(t)
	aPath := f.write(t, "labels-a.jsonl", f.a)
	bPath := f.write(t, "labels-b.jsonl", f.a[:4]) // same verdicts, second name
	fixed := append([]Label{}, f.a[:4]...)
	for i := range fixed {
		fixed[i].Annotator = "qa-b"
	}
	bPath = f.write(t, "labels-b-agree.jsonl", fixed)

	out := filepath.Join(f.dir, "empty.jsonl")
	code, stdout, errText := adjudicateRun(t, "--stub", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--out", out)
	if code != exitOK {
		t.Fatalf("exit = %d, want 0: %s", code, errText)
	}
	if !strings.Contains(stdout, "nothing to adjudicate") {
		t.Fatalf("the receipt should say there is nothing to decide, got:\n%s", stdout)
	}
	if info, err := os.Stat(out); err != nil || info.Size() != 0 {
		t.Fatalf("the stub file should exist and be empty (err=%v size=%v)", err, info)
	}
	if code, _, errText := adjudicateRun(t, "--check", "--worksheet", f.worksheet,
		"--labels-a", aPath, "--labels-b", bPath, "--adjudication", out); code != exitOK {
		t.Fatalf("checking an empty stub over an agreeing pair must pass, got %d: %s", code, errText)
	}
}

// `--help` is a usage sentence, not an environment probe: no flags, no file reads.
func TestAdjudicateUsageIsExit2(t *testing.T) {
	if code, _, errText := adjudicateRun(t); code != exitUsage {
		t.Fatalf("exit = %d, want %d: %s", code, exitUsage, errText)
	}
}

// fillStub writes the decisions a stub asks for — and only those — which is exactly what the
// adjudicator is asked to do. A helper rather than five copies of the loop: the tests below
// differ in what they get *wrong*, not in how a correct row looks.
func fillStub(t *testing.T, rows []map[string]any, verdict, status, candidate string) []string {
	t.Helper()
	var out []string
	for _, row := range rows {
		kinds := map[string]bool{}
		for _, k := range row["disagrees_on"].([]any) {
			kinds[k.(string)] = true
		}
		if kinds[adjKindVerdict] {
			row["verdict"] = verdict
		}
		if kinds[adjKindQuoteStatus] {
			row["quote_status"] = status
		}
		if kinds[adjKindCandidate] {
			row["candidate_chunk_id"] = candidate
		}
		row["adjudicator"] = "pm"
		row["reason"] = "读证据后的判断：引用只支持断言的一部分，候选乙与断言相反"
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}
