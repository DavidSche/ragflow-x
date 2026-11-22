package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// labelsRun drives the subcommand the way a person would, capturing both streams: the
// receipt goes to stdout, the refusals to stderr, and the exit code is the sentence.
func labelsRun(t *testing.T, args ...string) (int, string, string) {
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
	code := run(append([]string{"labels"}, args...), stdout, stderr)
	out := readAll(t, stdout.Name())
	errText := readAll(t, stderr.Name())
	return code, out, errText
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// labelsFixture is three worksheet items with one candidate-bearing item, so both the
// evidence-id rules and the candidate-reference rules have something to point at.
type labelsFixture struct {
	dir       string
	worksheet string
	items     []Item
}

func newLabelsFixture(t *testing.T) *labelsFixture {
	t.Helper()
	dir := t.TempDir()
	var items []Item
	for n := 1; n <= 3; n++ {
		it := sampleItem(n, "")
		// A worksheet asks; it does not answer. Everything `assemble` would fill in is
		// blank here, which is exactly what a `goldtool sample` export looks like.
		it.GoldVerdict = ""
		it.GoldQuoteStatus = ""
		it.GoldQuote = ""
		it.GoldEvidenceIDs = nil
		it.Annotators = nil
		it.GoldScanScope = "cited_only"
		items = append(items, it)
	}
	items[2].GoldScanScope = "cited+candidate:1"
	items[2].CandidatePool = []Candidate{{
		ChunkID: "chunk-conflict", ContentHash: "hash-conflict",
		Content: "该企业第 12 家权属单位整改尚未完成。", PoolRank: 1,
	}}
	path := filepath.Join(dir, "worksheet.jsonl")
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
	return &labelsFixture{dir: dir, worksheet: path, items: items}
}

func (f *labelsFixture) stub(t *testing.T, annotator string) string {
	t.Helper()
	out := filepath.Join(f.dir, "labels-"+annotator+".jsonl")
	code, _, errText := labelsRun(t, "--stub", "--worksheet", f.worksheet, "--annotator", annotator, "--out", out)
	if code != exitOK {
		t.Fatalf("stub exit = %d: %s", code, errText)
	}
	return out
}

// fullLabels is a complete, valid answer set for the fixture: three items, both
// questions answered, evidence ids drawn from the cited snapshots only.
func (f *labelsFixture) fullLabels(annotator string) []Label {
	labels := make([]Label, 0, len(f.items))
	for _, it := range f.items {
		labels = append(labels, Label{
			ItemID: it.ItemID, Annotator: annotator,
			Verdict: "supported", QuoteStatus: "not_provided",
			EvidenceIDs: []string{it.Evidence[0].SnapshotID},
			Note:        "证据与断言一致",
		})
	}
	return labels
}

func (f *labelsFixture) writeLabels(t *testing.T, name string, ls []Label) string {
	t.Helper()
	path := filepath.Join(f.dir, name)
	writeLabels(t, path, ls)
	return path
}

// The stub's whole job: one row per item, `item_id` copied (never typed), and nothing
// else. No claim text, no prior_support_status (the model's old opinion, which the manual's
// §4 rule 3 says to treat as absent), no empty verdict field that could be read as "judged
// empty".
func TestLabelsStubCarriesOnlyIdentity(t *testing.T) {
	f := newLabelsFixture(t)
	out := f.stub(t, "a1")

	raw := readAll(t, out)
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != len(f.items) {
		t.Fatalf("stub has %d line(s), want one per item (%d)", len(lines), len(f.items))
	}
	for i, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if len(row) != 2 {
			t.Fatalf("line %d carries %d field(s) %v; a stub is item_id + annotator and nothing else",
				i+1, len(row), row)
		}
		if row["item_id"] != f.items[i].ItemID {
			t.Fatalf("line %d item_id = %v, want %s (order must follow the worksheet)", i+1, row["item_id"], f.items[i].ItemID)
		}
		if row["annotator"] != "a1" {
			t.Fatalf("line %d annotator = %v, want a1", i+1, row["annotator"])
		}
	}
}

// A stub over a gold file would hand the annotator the answers.
func TestLabelsStubRefusesAnAlreadyLabelledWorksheet(t *testing.T) {
	f := newLabelsFixture(t)
	f.items[0].GoldVerdict = "supported"
	lines, err := encodeItems(f.items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(f.worksheet, lines); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(f.dir, "labels-a1.jsonl")
	code, _, errText := labelsRun(t, "--stub", "--worksheet", f.worksheet, "--annotator", "a1", "--out", out)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d (1 = an invariant failed)", code, exitInvariant)
	}
	if !strings.Contains(errText, "would hand the annotator the answer") {
		t.Fatalf("message does not name the blindness rule: %s", errText)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused stub still wrote %s", out)
	}
}

// Regenerating a stub over a half-filled file loses work that exists nowhere else.
func TestLabelsStubRefusesToOverwrite(t *testing.T) {
	f := newLabelsFixture(t)
	out := f.stub(t, "a1")
	before := readAll(t, out)

	// The annotator has started: one item is judged.
	ls := f.fullLabels("a1")[:1]
	writeLabels(t, out, ls)
	started := readAll(t, out)

	code, _, errText := labelsRun(t, "--stub", "--worksheet", f.worksheet, "--annotator", "a1", "--out", out)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (2 = refuse to do it)", code, exitUsage)
	}
	if !strings.Contains(errText, "refusing to overwrite") {
		t.Fatalf("message does not refuse the overwrite: %s", errText)
	}
	if got := readAll(t, out); got != started || got == before {
		t.Fatalf("the file changed: before-stub == %v, still-started == %v", got == before, got == started)
	}
}

func TestLabelsStubFlagDiscipline(t *testing.T) {
	f := newLabelsFixture(t)
	cases := []struct {
		name string
		args []string
		msg  string
		code int
	}{
		{"no mode", []string{"--worksheet", f.worksheet}, "pass --stub", exitUsage},
		{"both modes", []string{"--worksheet", f.worksheet, "--stub", "--check"}, "pick one", exitUsage},
		{"no worksheet", []string{"--stub", "--annotator", "a1", "--out", filepath.Join(f.dir, "x.jsonl")},
			"--worksheet is required", exitUsage},
		{"no annotator", []string{"--stub", "--worksheet", f.worksheet, "--out", filepath.Join(f.dir, "y.jsonl")},
			"--annotator is required", exitUsage},
		{"no out", []string{"--stub", "--worksheet", f.worksheet, "--annotator", "a1"},
			"--out is required", exitUsage},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, errText := labelsRun(t, c.args...)
			if code != c.code {
				t.Fatalf("exit = %d, want %d: %s", code, c.code, errText)
			}
			if !strings.Contains(errText, c.msg) {
				t.Fatalf("message does not mention %q: %s", c.msg, errText)
			}
		})
	}
}

// A file where every item is judged and every rule holds is the only shape that exits 0.
func TestLabelsCheckAcceptsACompleteFile(t *testing.T) {
	f := newLabelsFixture(t)
	path := f.writeLabels(t, "labels-a1.jsonl", f.fullLabels("a1"))
	code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path, "--annotator", "a1")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0: %s", code, errText)
	}
	if strings.Contains(out, "unfilled") || strings.Contains(out, "missing") {
		t.Fatalf("a complete file reports gaps: %s", out)
	}
	var receipt labelsReceipt
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, out)
	}
	if receipt.Items != len(f.items) || receipt.Labelled != len(f.items) || !receipt.OK {
		t.Fatalf("receipt = %+v", receipt)
	}
	if receipt.ByVerdict["supported"] != len(f.items) || receipt.ByQuoteStatus["not_provided"] != len(f.items) {
		t.Fatalf("receipt counts = %+v / %+v", receipt.ByVerdict, receipt.ByQuoteStatus)
	}
	if receipt.WorksheetSHA256 == "" || receipt.FileSHA256 == "" {
		t.Fatalf("receipt does not pin both files: %+v", receipt)
	}
}

// "Not finished" must be exit 3, not exit 1 and not exit 0: an unfinished file is not
// wrong, and it is certainly not a pass.
func TestLabelsCheckSeparatesUnfinishedFromWrong(t *testing.T) {
	f := newLabelsFixture(t)
	full := f.fullLabels("a1")

	t.Run("missing rows", func(t *testing.T) {
		path := f.writeLabels(t, "missing.jsonl", full[:2])
		code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path)
		if code != exitIncomplete {
			t.Fatalf("exit = %d, want %d: %s", code, exitIncomplete, errText)
		}
		if !strings.Contains(out, f.items[2].ItemID) {
			t.Fatalf("the missing item is not named: %s", out)
		}
		if !strings.Contains(errText, "not wrong, it is unfinished") {
			t.Fatalf("stderr does not separate unfinished from wrong: %s", errText)
		}
	})

	t.Run("blank verdict", func(t *testing.T) {
		ls := append([]Label{}, full...)
		ls[1] = Label{ItemID: ls[1].ItemID, Annotator: "a1"}
		path := f.writeLabels(t, "blank.jsonl", ls)
		code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path)
		if code != exitIncomplete {
			t.Fatalf("exit = %d, want %d: %s", code, exitIncomplete, errText)
		}
		if !strings.Contains(out, "unfilled (1)") {
			t.Fatalf("blank row is not counted as unfilled: %s", out)
		}
	})

	t.Run("wrong wins over unfinished", func(t *testing.T) {
		ls := append([]Label{}, full[:2]...)
		ls[0].Verdict = "supported_enough"
		path := f.writeLabels(t, "wrong-and-unfinished.jsonl", ls)
		code, _, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path)
		if code != exitInvariant {
			t.Fatalf("exit = %d, want %d (a named violation outranks an unfinished file): %s",
				code, exitInvariant, errText)
		}
	})
}

// The per-label rules, one subcase each. Every one of them previously surfaced either not
// at all or as a gold-item error that named neither the annotator nor the line.
func TestLabelsCheckCatchesEveryPerLabelRule(t *testing.T) {
	f := newLabelsFixture(t)
	cases := []struct {
		name   string
		mutate func(ls []Label) []Label
		code   string
		msg    string
	}{
		{"unknown item_id", func(ls []Label) []Label {
			ls[0].ItemID = "rev00:claim999"
			return ls
		}, "label_for_unknown_item", "κ would skip it silently"},
		{"duplicate row", func(ls []Label) []Label {
			return append(ls, ls[0])
		}, "duplicate_label", "was already labelled at line"},
		{"verdict outside the vocabulary", func(ls []Label) []Label {
			ls[0].Verdict = "insufficient_evidence"
			return ls
		}, "label_rule", "is not one of"},
		{"no quote_status", func(ls []Label) []Label {
			ls[0].QuoteStatus = ""
			return ls
		}, "label_rule", "quote_status is empty"},
		{"quote_status outside the vocabulary", func(ls []Label) []Label {
			ls[0].QuoteStatus = "verifyed"
			return ls
		}, "label_rule", "quote_status \"verifyed\" is not one of"},
		{"not_provided with a quote", func(ls []Label) []Label {
			ls[0].Quote = "证据原文"
			return ls
		}, "label_rule", "quote is non-empty"},
		{"verified without a quote", func(ls []Label) []Label {
			ls[0].QuoteStatus = "verified"
			return ls
		}, "label_rule", "unfalsifiable"},
		{"evidence id is a candidate chunk", func(ls []Label) []Label {
			ls[2].EvidenceIDs = []string{"chunk-conflict"}
			return ls
		}, "label_rule", "candidates are never evidence"},
		{"evidence id is a cited chunk, not a snapshot", func(ls []Label) []Label {
			ls[0].EvidenceIDs = []string{f.items[0].Evidence[0].ChunkID}
			return ls
		}, "label_rule", "cannot be replayed"},
		{"evidence id that does not exist", func(ls []Label) []Label {
			ls[0].EvidenceIDs = []string{"snap-invented"}
			return ls
		}, "label_rule", "not one of this item's"},
		{"candidate_index out of range", func(ls []Label) []Label {
			idx := 7
			ls[2].CandidateIndex = &idx
			return ls
		}, "label_rule", "outside the"},
		{"duplicate keys silently lose an item", func(ls []Label) []Label {
			// Two rows for the same item where the second looks valid: without the
			// duplicate rule this would be "three labelled" over two items.
			return append(ls[:2], ls[1])
		}, "duplicate_label", "was already labelled at line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ls := c.mutate(append([]Label{}, f.fullLabels("a1")...))
			path := f.writeLabels(t, "case.jsonl", ls)
			code, _, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path)
			if code != exitInvariant {
				t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
			}
			if !strings.Contains(errText, c.code) || !strings.Contains(errText, c.msg) {
				t.Fatalf("stderr does not carry %q / %q:\n%s", c.code, c.msg, errText)
			}
			if !strings.Contains(errText, "line ") {
				t.Fatalf("the refusal does not name the line:\n%s", errText)
			}
		})
	}
}

// A merged file (`cat a b`) is what κ eats, but `--check` reads one person's file: pooling
// the two answers makes "A labelled how many" unanswerable. `--annotator` scopes the check
// to one side, which is the only way to read a pooled file without editing it.
func TestLabelsCheckRefusesAMergedFile(t *testing.T) {
	f := newLabelsFixture(t)
	ls := append(f.fullLabels("a1"), f.fullLabels("a2")...)
	path := f.writeLabels(t, "merged.jsonl", ls)
	code, _, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "labels_mix_annotators") || !strings.Contains(errText, "pass --annotator NAME") {
		t.Fatalf("merged-file refusal is not actionable:\n%s", errText)
	}

	// Scoped to a1, the pooled file is exactly a1's file plus rows to skip — and the
	// duplicate rows belonging to a2 must not be counted against a1.
	code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path, "--annotator", "a1")
	if code != exitOK {
		t.Fatalf("--annotator should scope the check: exit = %d\n%s%s", code, out, errText)
	}
	var receipt labelsReceipt
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &receipt); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if receipt.SkippedOther != len(f.items) || len(receipt.Annotators) != 2 {
		t.Fatalf("receipt does not disclose the skipped rows: %+v", receipt)
	}
}

func TestLabelsCheckRefusesAnUnknownAnnotator(t *testing.T) {
	f := newLabelsFixture(t)
	path := f.writeLabels(t, "a2.jsonl", f.fullLabels("a2"))
	code, _, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path, "--annotator", "a1")
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "label_annotator_absent") || !strings.Contains(errText, "the file names [a2]") {
		t.Fatalf("a wrong annotator name is not named as one:\n%s", errText)
	}
}

// The stub → fill → check loop has to be the loop the manual describes, so the check must
// accept a file that started life as a stub.
func TestLabelsStubAndCheckRoundTrip(t *testing.T) {
	f := newLabelsFixture(t)
	stub := f.stub(t, "a1")

	// Still a stub: exit 3, every item named as unfilled.
	code, out, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", stub)
	if code != exitIncomplete {
		t.Fatalf("a fresh stub exits %d, want %d: %s", code, exitIncomplete, errText)
	}
	if !strings.Contains(out, "unfilled (3)") {
		t.Fatalf("a fresh stub does not report three unfilled items: %s", out)
	}

	// Fill it in place with the valid answers.
	writeLabels(t, stub, f.fullLabels("a1"))
	code, _, errText = labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", stub, "--annotator", "a1")
	if code != exitOK {
		t.Fatalf("filled stub exits %d, want 0: %s", code, errText)
	}
}

// Question ① is a fact, so two different answers cannot both stand. This is the failure the
// old merge had: A said `verified`, B said `not_found`, and the gold file silently took A's.
func TestAssembleRefusesASilentQuoteStatusPick(t *testing.T) {
	_, _, it := candRefFixture(t)
	it.GoldVerdict = ""
	it.GoldQuoteStatus = ""
	it.Annotators = nil
	pool := []Item{it}

	a := Label{ItemID: it.ItemID, Annotator: "qa-a", Verdict: "supported", QuoteStatus: "verified",
		Quote: "摘录十二字", EvidenceIDs: []string{it.Evidence[0].SnapshotID}}
	b := Label{ItemID: it.ItemID, Annotator: "qa-b", Verdict: "supported", QuoteStatus: "not_found"}

	gold, problems := assembleItems(pool, []Label{a}, []Label{b}, nil, false)
	if len(gold) != 0 {
		t.Fatalf("a disputed question ① still produced %d gold item(s)", len(gold))
	}
	found := false
	for _, p := range problems {
		if p.Code == "quote_status_disagreement" {
			found = true
			// The message has to say what may not be done, not just that something is off.
			if !strings.Contains(p.Message, "gold may not pick one") ||
				!strings.Contains(p.Message, `A="verified" B="not_found"`) {
				t.Fatalf("message does not name both answers and the rule: %s", p.Message)
			}
		}
	}
	if !found {
		t.Fatalf("no quote_status_disagreement in %v", problems)
	}

	// With an adjudication row that answers question ①, the pair assembles and the decision
	// is recorded in the notes: the gold file says who settled it and why.
	adj := Adjudication{ItemID: it.ItemID, Verdict: "supported", QuoteStatus: "not_found",
		Adjudicator: "pm", Reason: "回查证据，原文写的是「基本完成」而不是「完成」"}
	gold, problems = assembleItems(pool, []Label{a}, []Label{b}, []Adjudication{adj}, false)
	if len(problems) != 0 {
		t.Fatalf("adjudicated pair still has problems: %v", problems)
	}
	if len(gold) != 1 || gold[0].GoldQuoteStatus != "not_found" {
		t.Fatalf("gold quote_status = %q, want the adjudicated not_found", gold[0].GoldQuoteStatus)
	}
	if !strings.Contains(gold[0].Notes, "quote_status adjudicated by pm") {
		t.Fatalf("gold notes do not record the decision: %q", gold[0].Notes)
	}
}

// The other half of the same rule: agreeing on question ① must still let `mergeQuote` pick
// a quote text, so the new check cannot be a blanket refusal. The merge keeps the longer
// span when one contains the other (it is the one a reviewer can verify against), and takes
// A's evidence ids.
func TestAssembleStillMergesQuotesWhenStatusesAgree(t *testing.T) {
	_, _, it := candRefFixture(t)
	it.GoldVerdict = ""
	it.GoldQuoteStatus = ""
	it.Annotators = nil
	pool := []Item{it}
	a := Label{ItemID: it.ItemID, Annotator: "qa-a", Verdict: "supported", QuoteStatus: "verified",
		Quote: "摘录十二字", EvidenceIDs: []string{it.Evidence[0].SnapshotID}}
	b := Label{ItemID: it.ItemID, Annotator: "qa-b", Verdict: "supported", QuoteStatus: "verified",
		Quote: "摘录十二字，另一版本还多写了半句"}
	gold, problems := assembleItems(pool, []Label{a}, []Label{b}, nil, false)
	if len(problems) != 0 {
		t.Fatalf("agreement on question ① should assemble: %v", problems)
	}
	if len(gold) != 1 || gold[0].GoldQuote != b.Quote {
		t.Fatalf("gold quote = %q, want the containing span", gold[0].GoldQuote)
	}
	if len(gold[0].GoldEvidenceIDs) != 1 || gold[0].GoldEvidenceIDs[0] != it.Evidence[0].SnapshotID {
		t.Fatalf("gold evidence = %v, want A's snapshot", gold[0].GoldEvidenceIDs)
	}
}

// A worksheet that repeats an item_id would produce a skeleton with the same duplicate, and
// `--check` would then blame the annotator for a fault that came from upstream.
func TestLabelsStubRefusesADuplicateWorksheet(t *testing.T) {
	f := newLabelsFixture(t)
	items := append(append([]Item{}, f.items...), f.items[0])
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(f.worksheet, lines); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(f.dir, "labels-a1.jsonl")
	code, _, errText := labelsRun(t, "--stub", "--worksheet", f.worksheet, "--annotator", "a1", "--out", out)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "repeats 1 item_id") || !strings.Contains(errText, f.items[0].ItemID) {
		t.Fatalf("the duplicate id is not named:\n%s", errText)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused stub still wrote %s", out)
	}
}

// `--annotator` scopes the file to one person; a row that names nobody is still a fault, not
// somebody else's row to skip.
func TestLabelsCheckStillCatchesARowWithoutAnnotator(t *testing.T) {
	f := newLabelsFixture(t)
	ls := f.fullLabels("a1")
	ls[1].Annotator = ""
	path := f.writeLabels(t, "anon.jsonl", ls)
	code, _, errText := labelsRun(t, "--check", "--worksheet", f.worksheet, "--labels", path, "--annotator", "a1")
	if code != exitInvariant {
		t.Fatalf("exit = %d, want %d: %s", code, exitInvariant, errText)
	}
	if !strings.Contains(errText, "label_without_annotator") {
		t.Fatalf("an anonymous row was skipped instead of reported:\n%s", errText)
	}
}
