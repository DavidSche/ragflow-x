package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kappaRun drives `goldtool kappa` the way the manual's §6 step does: merged label file in,
// worksheet as the denominator, receipt out.
func kappaRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "kout-*")
	if err != nil {
		t.Fatalf("stdout temp: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.CreateTemp(t.TempDir(), "kerr-*")
	if err != nil {
		t.Fatalf("stderr temp: %v", err)
	}
	defer stderr.Close()
	code := run(append([]string{"kappa"}, args...), stdout, stderr)
	return code, readAll(t, stdout.Name()), readAll(t, stderr.Name())
}

// kappaReceipt pulls the JSON block out of stdout; the first line is the sentence a
// person reads.
func kappaReceipt(t *testing.T, out string) Agreement {
	t.Helper()
	if i := strings.LastIndex(out, "\n{\n"); i >= 0 {
		out = out[i+1:]
	}
	var ag Agreement
	if err := json.Unmarshal([]byte(out), &ag); err != nil {
		t.Fatalf("kappa stdout is not the Agreement JSON: %v\n%s", err, out)
	}
	return ag
}

// kappaFixture is three worksheet items — enough for κ to have a denominator and for a
// single missing cell to be visible in the receipt.
type kappaFixture struct {
	dir       string
	worksheet string
	items     []Item
}

func newKappaFixture(t *testing.T) *kappaFixture {
	t.Helper()
	dir := t.TempDir()
	var items []Item
	for n := 1; n <= 3; n++ {
		it := sampleItem(n, "")
		it.GoldVerdict, it.GoldQuoteStatus, it.GoldQuote = "", "", ""
		it.GoldEvidenceIDs, it.Annotators = nil, nil
		items = append(items, it)
	}
	path := filepath.Join(dir, "worksheet.jsonl")
	lines, err := encodeItems(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONL(path, lines); err != nil {
		t.Fatal(err)
	}
	return &kappaFixture{dir: dir, worksheet: path, items: items}
}

// answered is one annotator's complete side: every worksheet item, `supported`, with the
// cited snapshot as evidence.
func (f *kappaFixture) answered(annotator, verdict string) []Label {
	out := make([]Label, 0, len(f.items))
	for _, it := range f.items {
		out = append(out, Label{ItemID: it.ItemID, Annotator: annotator, Verdict: verdict,
			QuoteStatus: "not_provided", EvidenceIDs: []string{it.Evidence[0].SnapshotID}})
	}
	return out
}

func (f *kappaFixture) file(t *testing.T, name string, ls []Label) string {
	t.Helper()
	path := filepath.Join(f.dir, name)
	writeLabels(t, path, ls)
	return path
}

// The gap this closes: without the worksheet, an item_id with one wrong character simply
// falls out of every pair. κ still prints, `pairs` is one smaller, and nothing in the
// receipt says a row was dropped. Now the typo is a named problem with its line number.
func TestKappaRefusesALabelForAnUnknownItem(t *testing.T) {
	f := newKappaFixture(t)
	merged := append(f.answered("qa", "supported"), f.answered("pm", "supported")...)
	merged[4].ItemID = f.items[1].ItemID + "x" // second annotator's row for item 2
	path := f.file(t, "labels-merged.jsonl", merged)

	code, out, errText := kappaRun(t, "--labels", path, "--worksheet", f.worksheet)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1 (a typo is a named violation, not a κ)\n%s%s", code, out, errText)
	}
	if !strings.Contains(errText, "label_for_unknown_item") || !strings.Contains(errText, "line 5") {
		t.Fatalf("the refusal must name the code and the line:\n%s", errText)
	}
	if !strings.Contains(errText, "κ is not computed") {
		t.Fatalf("the sentence must say κ was not computed:\n%s", errText)
	}
	if ag := kappaReceipt(t, out); ag.Pairs != 0 {
		t.Fatalf("no κ may be reported over a file with an unusable row: pairs=%d", ag.Pairs)
	}
}

// The number people quote is "κ over the sample". With one side short, the honest receipt
// is "unfinished" (exit 3), not a κ computed over the overlap.
func TestKappaReportsAnIncompleteWorksheetAsUnfinished(t *testing.T) {
	f := newKappaFixture(t)
	qa := f.answered("qa", "supported")
	pm := f.answered("pm", "supported")[:2] // pm never got to the third item
	path := f.file(t, "labels-merged.jsonl", append(qa, pm...))

	code, out, errText := kappaRun(t, "--labels", path, "--worksheet", f.worksheet)
	if code != exitIncomplete {
		t.Fatalf("exit = %d, want 3: agreeing on 2 of 3 items is unfinished, not κ=1\n%s%s", code, out, errText)
	}
	if !strings.Contains(errText, "unfinished") || !strings.Contains(errText, "not pass") {
		t.Fatalf("exit 3 must say it is unfinished, not wrong:\n%s", errText)
	}
	ag := kappaReceipt(t, out)
	if ag.WorksheetItems != 3 || ag.Pairs != 2 || ag.MissingCells != 1 || !ag.Unfinished || ag.OK {
		t.Fatalf("receipt must account for the gap: items=%d pairs=%d missing=%d unfinished=%v ok=%v",
			ag.WorksheetItems, ag.Pairs, ag.MissingCells, ag.Unfinished, ag.OK)
	}
	if ag.LabelledByAnnotator["qa"] != 3 || ag.LabelledByAnnotator["pm"] != 2 {
		t.Fatalf("per-annotator coverage is the number a PM plans with: %v", ag.LabelledByAnnotator)
	}
}

// A row with no verdict is not a disagreement and not a judgement: it is a row the
// annotator has not reached yet, so κ over the overlap is unfinished (exit 3) rather than
// a number. Filling it in — with any verdict in the closed set — makes the same file a
// complete κ. (`abstained` is deliberately *not* the way out: it is the evaluator's state,
// not a gold value — see the next test. doc/301 §9.1 要点 4 says the opposite and is the
// stale wording; 304 §4 carries the deviation.)
func TestKappaCountsAJudgementNotABlankRow(t *testing.T) {
	f := newKappaFixture(t)
	qa := f.answered("qa", "supported")
	pm := f.answered("pm", "supported")
	pm[2].Verdict = "" // row present, judgement missing
	blank := f.file(t, "labels-blank.jsonl", append(qa, pm...))
	code, out, errText := kappaRun(t, "--labels", blank, "--worksheet", f.worksheet)
	if code != exitIncomplete {
		t.Fatalf("a blank row is unfinished, not a κ: exit = %d\n%s%s", code, out, errText)
	}
	if ag := kappaReceipt(t, out); ag.MissingCells != 1 || ag.Pairs != 2 || !ag.Unfinished {
		t.Fatalf("receipt: missing=%d pairs=%d unfinished=%v (want 1/2/true)",
			ag.MissingCells, ag.Pairs, ag.Unfinished)
	}

	pm[2].Verdict = "unsupported"
	answered := f.file(t, "labels-filled.jsonl", append(qa, pm...))
	code, out, errText = kappaRun(t, "--labels", answered, "--worksheet", f.worksheet, "--min", "0")
	if code != exitOK {
		t.Fatalf("the same file with the row filled in is complete: exit = %d\n%s%s", code, out, errText)
	}
	if ag := kappaReceipt(t, out); ag.Pairs != 3 || ag.MissingCells != 0 {
		t.Fatalf("filled: pairs=%d missing=%d (want 3/0)", ag.Pairs, ag.MissingCells)
	}
}

// `abstained` is the evaluator's state, not a human label (item.go's closed set says so, and
// the manual repeats it). κ is where a wrong vocabulary would otherwise become a number: an
// annotator following doc/301 §9.1 要点 4 literally would write it and be refused here.
func TestKappaRefusesAnEvaluatorStateAsAVerdict(t *testing.T) {
	f := newKappaFixture(t)
	qa := f.answered("qa", "supported")
	pm := f.answered("pm", "abstained")
	path := f.file(t, "labels-merged.jsonl", append(qa, pm...))
	code, _, errText := kappaRun(t, "--labels", path, "--worksheet", f.worksheet)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1 for a verdict outside the gold vocabulary\n%s", code, errText)
	}
	if !strings.Contains(errText, "label_verdict_outside_vocabulary") || !strings.Contains(errText, "abstained") {
		t.Fatalf("the refusal must name the value and its line:\n%s", errText)
	}
}

func TestKappaRequiresTheWorksheetAsItsDenominator(t *testing.T) {
	f := newKappaFixture(t)
	path := f.file(t, "labels-merged.jsonl", append(f.answered("qa", "supported"), f.answered("pm", "supported")...))
	code, _, errText := kappaRun(t, "--labels", path)
	if code != exitUsage {
		t.Fatalf("exit = %d, want 2 (a usage error, before any number)\n%s", code, errText)
	}
	if !strings.Contains(errText, "denominator") || !strings.Contains(errText, "mistyped item_id") {
		t.Fatalf("the refusal must say why the worksheet is not optional:\n%s", errText)
	}
}

func TestKappaPassesOnlyOnAFullWorksheetAtTheFloor(t *testing.T) {
	f := newKappaFixture(t)
	// 3 items, qa says supported everywhere; pm agrees on two and says unsupported on one.
	qa := f.answered("qa", "supported")
	pm := f.answered("pm", "supported")
	pm[2].Verdict = "unsupported"
	path := f.file(t, "labels-merged.jsonl", append(qa, pm...))

	code, out, errText := kappaRun(t, "--labels", path, "--worksheet", f.worksheet)
	if code != exitInvariant {
		t.Fatalf("exit = %d, want 1 (a real disagreement below the floor)\n%s%s", code, out, errText)
	}
	if ag := kappaReceipt(t, out); ag.MeetsTarget || ag.OK || ag.Disagreed != 1 || ag.Pairs != 3 {
		t.Fatalf("receipt: meeds_target=%v ok=%v disagreed=%d pairs=%d (want false/false/1/3)",
			ag.MeetsTarget, ag.OK, ag.Disagreed, ag.Pairs)
	}
	if ag := kappaReceipt(t, out); len(ag.PerClass) == 0 {
		t.Fatal("a disagreement must be named by class: supported vs unsupported is a certification argument")
	}

	// A floor the pair is allowed to pass: the same files at --min 0.0 are complete and
	// accepted, which is what proves the exit code above came from κ and not from coverage.
	if code, out, errText := kappaRun(t, "--labels", path, "--worksheet", f.worksheet, "--min", "0"); code != exitOK {
		t.Fatalf("--min 0 over a complete worksheet must pass: exit = %d\n%s%s", code, out, errText)
	}
}

// A file carrying one name is not κ — and the tool cannot tell "the second annotator has not
// merged yet" from "both files mis-typed the same name". It refuses and says both out loud,
// at exit 3: neither reading is a pass, and "unfinished" is the honest one when the second
// half is missing. What it must not do is print κ and let the reader assume two people.
func TestKappaRefusesToGuessFromASingleAnnotator(t *testing.T) {
	f := newKappaFixture(t)
	path := f.file(t, "labels-one-person.jsonl", f.answered("qa", "supported"))

	code, out, errText := kappaRun(t, "--labels", path, "--worksheet", f.worksheet)
	if code != exitIncomplete {
		t.Fatalf("exit = %d, want 3 (one name: not a κ, not yet a fault)\n%s%s", code, out, errText)
	}
	for _, want := range []string{"only 1 annotator(s)", "same `annotator` name", "exit 3, not pass"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("the refusal must name both readings (%q):\n%s", want, errText)
		}
	}
	if !strings.Contains(out, "κ not computed over 0 pair(s)") || !strings.Contains(out, "only annotator (qa)") {
		t.Fatalf("the sentence must not print a κ or claim everyone labelled:\n%s", out)
	}
	if ag := kappaReceipt(t, out); ag.Pairs != 0 || ag.OK || !ag.Unfinished {
		t.Fatalf("receipt: pairs=%d ok=%v unfinished=%v (want 0/false/true)", ag.Pairs, ag.OK, ag.Unfinished)
	}
}
