package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The two-annotator loop has one hand-written file in it: each annotator's
// `labels-<name>.jsonl`. The manual's §6 step 1 says so out loud ("文件由人写，工具不生成"),
// and that is right about the *judgements* — `verdict` and `quote_status` must come from a
// person. But everything around them is transcription: 120 `item_id`s copied out of a
// worksheet, and a `quote_status` that nothing checks until `assemble` has already merged
// two files and the error message is about a gold item rather than about line 47 of
// somebody's file.
//
// So `labels` does the two halves that are not judgement:
//
//	--stub   emits one row per worksheet item with `item_id` and `annotator` and nothing
//	         else. No id is ever typed by hand, and no worksheet field is echoed (an
//	         echoed `prior_support_status` is the model's opinion sitting in the annotator's
//	         file — doc/301 §4 rule 3 says treat it as absent, so the stub never carries it).
//	--check  runs every per-label rule the manual states, against the worksheet, before κ:
//	         unknown ids (κ drops them silently), duplicates, closed vocabularies, and the
//	         two question-① rules (`not_provided` ⇔ empty quote, evidence ids are
//	         snapshot ids and never candidate chunk ids).
//
// `--stub` refuses a source that already carries `gold_verdict`, and refuses to overwrite a
// file: a stub that can replace a half-filled file can erase judgements that were never
// recorded anywhere else.

// labelsSchema identifies the receipt shape so a future reader can tell receipts apart.
const labelsSchema = "rgx.goldtool.labels/1"

// stubRow is the whole content of a stub line. Kept as its own type rather than a `Label`
// with empty fields: `{"verdict":""}` and "the field is absent" would mean different things
// to a reader ("judged empty" vs "not judged yet"), and only one of them is true.
type stubRow struct {
	ItemID    string `json:"item_id"`
	Annotator string `json:"annotator"`
}

// labelsReceipt is what both modes print. `Labelled` counts rows that carry a verdict;
// `Items` is the worksheet's size. They differ while the annotator is still working, and
// that difference is the receipt's most useful number.
type labelsReceipt struct {
	Schema          string         `json:"schema"`
	Mode            string         `json:"mode"`
	Worksheet       string         `json:"worksheet"`
	WorksheetSHA256 string         `json:"worksheet_sha256"`
	File            string         `json:"file"`
	FileSHA256      string         `json:"file_sha256,omitempty"`
	Annotator       string         `json:"annotator,omitempty"`
	Items           int            `json:"items"`
	Rows            int            `json:"rows"`
	Labelled        int            `json:"labelled"`
	Unfilled        []string       `json:"unfilled,omitempty"`
	Missing         []string       `json:"missing,omitempty"`
	Annotators      []string       `json:"annotators,omitempty"`
	SkippedOther    int            `json:"skipped_other_annotator,omitempty"`
	ByVerdict       map[string]int `json:"by_verdict,omitempty"`
	ByQuoteStatus   map[string]int `json:"by_quote_status,omitempty"`
	Problems        []Problem      `json:"problems,omitempty"`
	OK              bool           `json:"ok"`
}

func runLabels(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("labels", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stub := fs.Bool("stub", false, "emit a blank label skeleton (one row per worksheet item)")
	check := fs.Bool("check", false, "check a filled label file against the worksheet")
	worksheet := fs.String("worksheet", "", "worksheet JSONL produced by `goldtool sample`")
	labelsPath := fs.String("labels", "", "label JSONL to check (--check)")
	annotator := fs.String("annotator", "", "the annotator this file belongs to")
	out := fs.String("out", "", "stub output path (--stub)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch {
	case *stub && *check:
		fmt.Fprintln(stderr, "goldtool labels: --stub and --check are different jobs; pick one")
		return exitUsage
	case !*stub && !*check:
		fmt.Fprintln(stderr, "goldtool labels: pass --stub (emit a skeleton) or --check (verify a filled file)")
		return exitUsage
	}
	if trimSpace(*worksheet) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --worksheet is required: a label file is only meaningful next to the sample it labels")
		return exitUsage
	}

	items, err := readItems(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: worksheet: %v\n", err)
		return exitUsage
	}
	if len(items) == 0 {
		fmt.Fprintf(stderr, "goldtool labels: worksheet %s has no items\n", *worksheet)
		return exitInvariant
	}
	sum, err := hashFile(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: worksheet: %v\n", err)
		return exitUsage
	}

	if *stub {
		return labelsStub(items, *worksheet, sum, *annotator, *out, stdout, stderr)
	}
	return labelsCheck(items, *worksheet, sum, *labelsPath, *annotator, stdout, stderr)
}

func labelsStub(items []Item, worksheet, sum, annotator, out string, stdout, stderr *os.File) int {
	if trimSpace(annotator) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --annotator is required with --stub; a stub without a name is a file "+
			"two people can both fill in and merge into one opinion")
		return exitUsage
	}
	if trimSpace(out) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --out is required with --stub")
		return exitUsage
	}
	if n := countLabelled(items); n > 0 {
		fmt.Fprintf(stderr, "goldtool labels: the worksheet carries gold_verdict on %d of %d item(s): stubbing it "+
			"would hand the annotator the answer to the question they are supposed to answer (doc/301 §4 rule 3)\n",
			n, len(items))
		return exitInvariant
	}
	// A worksheet with duplicate ids would produce a skeleton that `--check` then rejects as
	// the annotator's duplicate rows. Refuse it here, where the fault actually is.
	if dups := duplicateItemIDs(items); len(dups) > 0 {
		fmt.Fprintf(stderr, "goldtool labels: the worksheet repeats %d item_id(s) %v: a stub would copy the "+
			"duplicate, and `--check` would report it as the annotator's mistake\n", len(dups), cap10(dups))
		return exitInvariant
	}

	lines := make([]string, 0, len(items))
	for _, it := range items {
		raw, err := json.Marshal(stubRow{ItemID: it.ItemID, Annotator: trimSpace(annotator)})
		if err != nil {
			fmt.Fprintf(stderr, "goldtool labels: encode %s: %v\n", it.ItemID, err)
			return exitInvariant
		}
		lines = append(lines, string(raw))
	}
	code := writeLinesExclusive(out, lines, stderr)
	if code != exitOK {
		return code
	}

	fmt.Fprintf(stdout, "goldtool labels: stub for %q over %d item(s) -> %s\n", trimSpace(annotator), len(items), out)
	fmt.Fprintf(stdout, "  worksheet %s  sha256 %s\n", worksheet, sum)
	fmt.Fprintf(stdout, "  fill in, per row: verdict, quote_status, quote (unless not_provided), "+
		"evidence_ids (snapshot ids only), candidate_chunk_id (only when a candidate row is the basis), note\n")
	fmt.Fprintf(stdout, "  then: goldtool labels --check --worksheet %s --labels %s\n", worksheet, out)
	printJSON(stdout, labelsReceipt{
		Schema: labelsSchema, Mode: "stub", Worksheet: worksheet, WorksheetSHA256: sum,
		File: out, Annotator: trimSpace(annotator), Items: len(items), Rows: len(lines), OK: true,
	})
	return exitOK
}

// countLabelled is the blindness check: a worksheet is a question, a gold file is an answer.
func countLabelled(items []Item) int {
	n := 0
	for _, it := range items {
		if trimSpace(it.GoldVerdict) != "" {
			n++
		}
	}
	return n
}

// duplicateItemIDs is the other shape a worksheet must never have: two rows for one claim
// would give the annotator two places to write the same judgement, and κ would see one.
func duplicateItemIDs(items []Item) []string {
	seen := map[string]int{}
	var dups []string
	for _, it := range items {
		seen[it.ItemID]++
		if seen[it.ItemID] == 2 {
			dups = append(dups, it.ItemID)
		}
	}
	return dups
}

// sortedKeys is the deterministic rendering of a set of names; map order would make the
// same file produce a different message on every run.
func sortedCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeLinesExclusive(path string, lines []string, stderr *os.File) int {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "goldtool labels: %v\n", err)
			return exitUsage
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			// The file may already hold judgements; regenerating it "because the stub is
			// only the skeleton" is how a half-filled file loses an hour of reading.
			fmt.Fprintf(stderr, "goldtool labels: %s already exists: refusing to overwrite it "+
				"(a second stub must go to a new path, so nothing that was already judged can be erased)\n", path)
			return exitUsage
		}
		fmt.Fprintf(stderr, "goldtool labels: %v\n", err)
		return exitUsage
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			fmt.Fprintf(stderr, "goldtool labels: write %s: %v\n", path, err)
			return exitUsage
		}
	}
	return exitOK
}

func labelsCheck(items []Item, worksheet, sum, labelsPath, wantAnnotator string, stdout, stderr *os.File) int {
	if trimSpace(labelsPath) == "" {
		fmt.Fprintln(stderr, "goldtool labels: --labels is required with --check")
		return exitUsage
	}
	ls, err := readLabels(labelsPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: labels: %v\n", err)
		return exitUsage
	}
	fileSum, err := hashFile(labelsPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool labels: labels: %v\n", err)
		return exitUsage
	}

	byID := make(map[string]Item, len(items))
	order := make([]string, 0, len(items))
	for _, it := range items {
		if _, dup := byID[it.ItemID]; !dup {
			order = append(order, it.ItemID)
		}
		byID[it.ItemID] = it
	}

	var problems []Problem
	seen := map[string]int{}
	annotators := map[string]int{}
	for _, l := range ls {
		if a := trimSpace(l.Annotator); a != "" {
			annotators[a]++
		}
	}
	want := trimSpace(wantAnnotator)
	if want != "" && annotators[want] == 0 {
		// The whole file would be skipped, and an empty check reads like a pass. A name
		// that matches nothing is a typo, and saying so beats reporting 0/120 labelled.
		problems = append(problems, Problem{"label_annotator_absent",
			fmt.Sprintf("no row in %s says annotator %q (the file names %v): check the spelling before reading "+
				"the coverage number", labelsPath, want, sortedCountKeys(annotators))})
	}
	receipt := labelsReceipt{
		Schema: labelsSchema, Mode: "check", Worksheet: worksheet, WorksheetSHA256: sum,
		File: labelsPath, FileSHA256: fileSum, Annotator: want,
		Items: len(items), Rows: len(ls), Annotators: sortedCountKeys(annotators),
		ByVerdict: map[string]int{}, ByQuoteStatus: map[string]int{},
	}
	var unfilled []string

	for i, l := range ls {
		line := i + 1
		at := fmt.Sprintf("line %d", line)
		// `--annotator` scopes the check to one person's rows: κ eats a pooled file, but
		// "did A judge every item" is only answerable from one side at a time. A row with no
		// annotator at all is *not* someone else's row — it falls through to the named
		// problem below.
		if want != "" {
			if a := trimSpace(l.Annotator); a != "" && a != want {
				receipt.SkippedOther++
				continue
			}
		}
		switch {
		case trimSpace(l.ItemID) == "":
			problems = append(problems, Problem{"label_without_item_id", at})
			continue
		case trimSpace(l.Annotator) == "":
			problems = append(problems, Problem{"label_without_annotator",
				at + ": every row names who judged it, or two files merge into one opinion"})
			continue
		}
		it, known := byID[l.ItemID]
		if !known {
			// κ drops ids it does not recognise, so a single mistyped character here
			// silently shrinks the pair count — the number still looks like a κ.
			problems = append(problems, Problem{"label_for_unknown_item",
				fmt.Sprintf("%s: %s is not in %s (κ would skip it silently, so the item would never be counted)",
					at, l.ItemID, worksheet)})
			continue
		}
		if prev, dup := seen[l.ItemID]; dup {
			problems = append(problems, Problem{"duplicate_label",
				fmt.Sprintf("%s: %s was already labelled at line %d", at, l.ItemID, prev)})
			continue
		}
		seen[l.ItemID] = line

		if trimSpace(l.Verdict) == "" {
			// Not a violation: the annotator is not finished. Counted separately so
			// "3 rows still blank" never reads as "3 rows are wrong".
			unfilled = append(unfilled, l.ItemID)
			continue
		}
		receipt.Labelled++
		receipt.ByVerdict[l.Verdict]++
		if qs := trimSpace(l.QuoteStatus); qs != "" {
			receipt.ByQuoteStatus[qs]++
		}
		for _, msg := range checkLabelRules(it, l) {
			problems = append(problems, Problem{"label_rule", at + " " + l.ItemID + ": " + msg})
		}
	}

	if len(annotators) > 1 && want == "" {
		names := sortedCountKeys(annotators)
		problems = append(problems, Problem{"labels_mix_annotators",
			fmt.Sprintf("the file carries %d annotators %v: κ needs a pooled file, but coverage and question-① rules "+
				"are per person, so pass --annotator NAME to check one side", len(names), names)})
	}

	var missing []string
	for _, id := range order {
		if _, ok := seen[id]; ok {
			continue
		}
		missing = append(missing, id)
	}
	todo := len(unfilled) + len(missing)

	receipt.Unfilled = cap10(unfilled)
	receipt.Missing = cap10(missing)
	receipt.Problems = problems
	receipt.OK = len(problems) == 0 && todo == 0

	for _, pr := range problems {
		fmt.Fprintf(stderr, "goldtool labels: %s: %s\n", pr.Code, pr.Message)
	}
	fmt.Fprintf(stdout, "goldtool labels: %s  %d/%d labelled, %d problem(s)\n",
		labelsPath, receipt.Labelled, receipt.Items, len(problems))
	if len(unfilled) > 0 {
		fmt.Fprintf(stdout, "  unfilled (%d): %s\n", len(unfilled), strings.Join(cap10(unfilled), " "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(stdout, "  missing (%d): %s\n", len(missing), strings.Join(cap10(missing), " "))
	}
	printJSON(stdout, receipt)
	switch {
	case len(problems) > 0:
		return exitInvariant
	case todo > 0:
		// Not finished is not a finding: exit 3 keeps "I could not check" apart from
		// "it holds", the same three-sentence discipline as `prereg --check`.
		fmt.Fprintf(stderr, "goldtool labels: %d item(s) not judged yet: this file is not wrong, it is unfinished "+
			"(exit 3, not pass)\n", todo)
		return exitIncomplete
	}
	return exitOK
}

// checkLabelRules is the per-label half of the manual, applied to a real worksheet item.
// It exists because every one of these rules was previously enforced only *after* merging:
// an invalid `quote_status` surfaced as "gold item has no valid gold_quote_status", which
// names neither the annotator nor the line, and a `quote_status` disagreement between the
// two annotators surfaced as nothing at all.
func checkLabelRules(it Item, l Label) []string {
	var v []string
	verdict := trimSpace(l.Verdict)
	if !contains(GoldVerdicts, verdict) {
		v = append(v, fmt.Sprintf("verdict %q is not one of %v (doc/301 §9.1: the evaluator states "+
			"insufficient/abstained/not_required are not labels)", verdict, GoldVerdicts))
	}

	status := trimSpace(l.QuoteStatus)
	quote := trimSpace(l.Quote)
	switch {
	case status == "":
		v = append(v, "quote_status is empty: the manual asks two separate questions, and a verdict without a "+
			"quote judgement has only answered the second one")
	case !contains(model.ReportQuoteStatuses(), status):
		v = append(v, fmt.Sprintf("quote_status %q is not one of %v", status, model.ReportQuoteStatuses()))
	case status == model.ReportQuoteStatusNotProvided && quote != "":
		v = append(v, "quote_status is not_provided but quote is non-empty (the two must agree: "+
			"\"there was no quote\" and \"here is the quote\" cannot both be true)")
	case status != model.ReportQuoteStatusNotProvided && quote == "":
		v = append(v, fmt.Sprintf("quote_status is %q but quote is empty: a quote claim without the text is "+
			"unfalsifiable", status))
	}

	// Evidence ids are snapshot ids (doc/301 §9.1: candidate searching may not create
	// brand-new citations). Naming a candidate chunk here is the single most likely
	// confusion in the whole manual — §2 says candidates are never evidence — so it gets
	// its own sentence instead of "not a cited snapshot".
	snaps := map[string]bool{}
	chunks := map[string]bool{}
	for _, ev := range it.Evidence {
		snaps[ev.SnapshotID] = true
		chunks[ev.ChunkID] = true
	}
	candidates := map[string]bool{}
	for _, c := range it.CandidatePool {
		candidates[c.ChunkID] = true
	}
	for _, id := range l.EvidenceIDs {
		id = trimSpace(id)
		if id == "" {
			continue
		}
		if snaps[id] {
			continue
		}
		if candidates[id] {
			v = append(v, fmt.Sprintf("evidence_ids names %q, which is a candidate chunk_id: candidates are never "+
				"evidence (doc/301 §5.3.1) — use evidence[].snapshot_id", id))
			continue
		}
		if chunks[id] {
			v = append(v, fmt.Sprintf("evidence_ids names %q, which is an evidence chunk_id: the gold file records "+
				"snapshot ids, so a chunk id here cannot be replayed", id))
			continue
		}
		v = append(v, fmt.Sprintf("evidence_ids names %q, which is not one of this item's %d cited snapshot(s)",
			id, len(snaps)))
	}

	if bad := checkLabelCandidateRef(it, l); bad != "" {
		v = append(v, bad)
	}
	return v
}
