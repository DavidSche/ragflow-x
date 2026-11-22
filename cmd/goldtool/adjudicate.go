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

// The two-annotator loop had exactly one hand-written file left in it: `adjudication.jsonl`.
// `labels` (v1.6) removed the transcription from the annotators' side — no `item_id` is
// typed by hand, and a stub refuses to overwrite a half-filled file. The adjudicator's side
// kept the older habit: read two files, work out *which* items disagree, type their
// `item_id`s into a third file, and get the reason right.
//
// That is the same class of hole `labels` closed, one step later, and it fails in the same
// quiet way. A dropped row is not a typo the tool can see: `assemble` reports it as
// `unadjudicated_disagreement` — one of N, deep inside a merge — and a row written for an
// item the two annotators *agreed* on is worse, because it replaces a judgement nobody
// disputed. The first costs an hour of re-reading; the second silently rewrites gold.
//
// So `adjudicate` does the two halves that are not judgement, and it does them from the two
// label files rather than from a summary of them:
//
//	--stub   derives the items `assemble` would otherwise drop, and writes one row per item
//	         with the cells the adjudicator has to decide left blank and the cells the two
//	         files already agree on filled in as facts. No id is typed by hand.
//	--check  proves the file is *complete* (every disagreement has a row), *narrow* (no row
//	         for an agreed item) and *decided* (every required cell in its closed set, with a
//	         reason and a name). An unfinished row is exit 3 — not a finding, the same
//	         three-sentence discipline as `labels --check` and `prereg --check`.
//
// The derivation is deliberately the mirror of `assembleItems`: the same three situations
// that produce `unadjudicated_disagreement`, `quote_status_disagreement` and
// `candidate_ref_disagreement` are the three situations that produce a stub row. A test
// pins the equivalence in both directions, because two readers of one rule drift apart
// unless something reads them against each other.

// adjudicateSchema identifies the receipt shape, so a future reader can tell a stub
// receipt from a check receipt without reading the mode.
const adjudicateSchema = "rgx.goldtool.adjudicate/1"

// The three cells an adjudication may have to settle. They are named after the `assemble`
// problem each one resolves, so the stub's `disagrees_on` can be read against the merge
// error without a translation table.
const (
	adjKindVerdict     = "verdict"
	adjKindQuoteStatus = "quote_status"
	adjKindCandidate   = "candidate_chunk_id"
)

// adjOpinion is what one annotator's file says about one item, reduced to the fields the
// merge compares. `quote_status` is the *effective* value (`labelQuoteStatus`), because a
// blank field plus a quote text means `verified` to the merge, and showing the raw blank
// would make the adjudicator decide between "" and "verified" — a formatting question, not
// the question they are asked.
type adjOpinion struct {
	Verdict          string `json:"verdict"`
	QuoteStatus      string `json:"quote_status"`
	CandidateChunkID string `json:"candidate_chunk_id,omitempty"`
	Note             string `json:"note,omitempty"`
}

// adjStubRow is the whole content of a stub line.
//
// `verdict`, `quote_status`, `candidate_chunk_id`, `adjudicator` and `reason` are
// `Adjudication`'s own field names, so the file the adjudicator fills in is the file
// `assemble` reads — one file, not a worksheet plus a transcription. `disagrees_on`, `a`
// and `b` are ignored by `assemble` (it decodes into `Adjudication`, which has no such
// fields) and exist so the person filling the row can see the case they are deciding
// without opening two other files.
//
// An empty `verdict` is what makes a half-filled stub *loud*: `assemble` refuses it as
// `bad_adjudication_verdict` rather than treating the empty string as "keep the annotators'
// choice".
type adjStubRow struct {
	ItemID      string     `json:"item_id"`
	DisagreesOn []string   `json:"disagrees_on"`
	A           adjOpinion `json:"a"`
	B           adjOpinion `json:"b"`
	// Decided cells. Pre-filled only when the two label files already agree on them, so
	// the blank cells in a stub are exactly the decisions the adjudicator owes.
	Verdict          string `json:"verdict"`
	QuoteStatus      string `json:"quote_status"`
	CandidateChunkID string `json:"candidate_chunk_id,omitempty"`
	Adjudicator      string `json:"adjudicator"`
	Reason           string `json:"reason"`
}

// adjNeed is one item's case, derived from the two label files.
type adjNeed struct {
	ItemID      string
	DisagreesOn []string
	A, B        adjOpinion
	// The facts the two files agree on. A row that changes one of them is not deciding a
	// disagreement, it is overwriting an agreement.
	Verdict          string
	QuoteStatus      string
	CandidateChunkID string
}

// adjudicateReceipt is what both modes print; the fields a mode does not fill are omitted
// rather than zero, so "0 rows" and "rows were not counted" never look alike.
type adjudicateReceipt struct {
	Schema          string         `json:"schema"`
	Mode            string         `json:"mode"`
	Worksheet       string         `json:"worksheet"`
	WorksheetSHA256 string         `json:"worksheet_sha256"`
	LabelsA         string         `json:"labels_a"`
	LabelsB         string         `json:"labels_b"`
	LabelsASHA256   string         `json:"labels_a_sha256,omitempty"`
	LabelsBSHA256   string         `json:"labels_b_sha256,omitempty"`
	File            string         `json:"file,omitempty"`
	Items           int            `json:"items"`
	Paired          int            `json:"items_labelled_by_both"`
	Unpaired        []string       `json:"unpaired,omitempty"`
	Needs           int            `json:"rows_needed"`
	Rows            int            `json:"rows"`
	Decided         int            `json:"decided"`
	Undecided       []string       `json:"undecided,omitempty"`
	Missing         []string       `json:"missing,omitempty"`
	ByKind          map[string]int `json:"by_kind,omitempty"`
	ByVerdict       map[string]int `json:"by_verdict,omitempty"`
	Problems        []Problem      `json:"problems,omitempty"`
	OK              bool           `json:"ok"`
}

func runAdjudicate(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("adjudicate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stub := fs.Bool("stub", false, "derive the rows that need a decision and emit a skeleton")
	check := fs.Bool("check", false, "check an adjudication file against the two label files")
	worksheet := fs.String("worksheet", "", "worksheet JSONL produced by `goldtool sample`")
	labelA := fs.String("labels-a", "", "annotator A label file (output of `goldtool labels --stub`, filled in)")
	labelB := fs.String("labels-b", "", "annotator B label file")
	adjPath := fs.String("adjudication", "", "adjudication JSONL to check (--check)")
	out := fs.String("out", "", "stub output path (--stub)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	switch {
	case *stub && *check:
		fmt.Fprintln(stderr, "goldtool adjudicate: --stub and --check are different jobs; pick one")
		return exitUsage
	case !*stub && !*check:
		fmt.Fprintln(stderr, "goldtool adjudicate: pass --stub (derive the rows that need a decision) or --check "+
			"(verify a filled adjudication file)")
		return exitUsage
	}
	if trimSpace(*worksheet) == "" {
		fmt.Fprintln(stderr, "goldtool adjudicate: --worksheet is required: a disagreement is only meaningful "+
			"against the sample the two annotators judged")
		return exitUsage
	}
	for name, v := range map[string]string{"labels-a": *labelA, "labels-b": *labelB} {
		if trimSpace(v) == "" {
			fmt.Fprintf(stderr, "goldtool adjudicate: --%s is required\n", name)
			return exitUsage
		}
	}

	items, err := readItems(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool adjudicate: worksheet: %v\n", err)
		return exitUsage
	}
	if len(items) == 0 {
		fmt.Fprintf(stderr, "goldtool adjudicate: worksheet %s has no items\n", *worksheet)
		return exitInvariant
	}
	wsSum, err := hashFile(*worksheet)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool adjudicate: worksheet: %v\n", err)
		return exitUsage
	}
	la, err := readLabels(*labelA)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool adjudicate: labels-a: %v\n", err)
		return exitUsage
	}
	lb, err := readLabels(*labelB)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool adjudicate: labels-b: %v\n", err)
		return exitUsage
	}

	byItem := make(map[string]Item, len(items))
	order := make([]string, 0, len(items))
	for _, it := range items {
		if _, dup := byItem[it.ItemID]; !dup {
			order = append(order, it.ItemID)
		}
		byItem[it.ItemID] = it
	}
	// The sample asks; it does not answer. A worksheet that already carries `gold_verdict` is
	// the assembled file, and deriving disagreements from it would compare two label files
	// against a third, later opinion — the adjudicator would be reading a decision that was
	// made after the two they are adjudicating.
	if n := countLabelled(items); n > 0 {
		fmt.Fprintf(stderr, "goldtool adjudicate: %s carries gold_verdict on %d of %d item(s): pass the "+
			"worksheet the two annotators were given, not the assembled gold file\n", *worksheet, n, len(items))
		return exitInvariant
	}

	var problems []Problem
	// The same two-annotator rule κ rests on, enforced here because "no disagreements" is
	// the one answer this command can give that looks like a pass and means nothing: the
	// same file twice agrees with itself perfectly.
	nameA, nameB, nameProblems := adjudicateAnnotators(la, lb)
	problems = append(problems, nameProblems...)

	ia := indexLabels(la, &problems, "labels-a", byItem)
	ib := indexLabels(lb, &problems, "labels-b", byItem)
	needs, unpaired := adjudicationNeeds(items, ia, ib)
	if len(problems) > 0 {
		// A stub derived from files that already violate a rule would carry the fault into
		// the adjudicator's row; a check over such files would count coverage that means
		// nothing.
		for _, pr := range problems {
			fmt.Fprintf(stderr, "goldtool adjudicate: %s: %s\n", pr.Code, pr.Message)
		}
		fmt.Fprintf(stderr, "goldtool adjudicate: %s / %s must each be one person's complete file before "+
			"adjudication (fix them with `goldtool labels --check`)\n", *labelA, *labelB)
		return exitInvariant
	}

	sumA, err := hashFile(*labelA)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool adjudicate: labels-a: %v\n", err)
		return exitUsage
	}
	sumB, err := hashFile(*labelB)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool adjudicate: labels-b: %v\n", err)
		return exitUsage
	}

	receipt := adjudicateReceipt{
		Schema: adjudicateSchema, Worksheet: *worksheet, WorksheetSHA256: wsSum,
		LabelsA: *labelA, LabelsB: *labelB, LabelsASHA256: sumA, LabelsBSHA256: sumB,
		Items: len(items), Unpaired: cap10(unpaired),
		Needs: len(needs), ByKind: map[string]int{},
	}
	// `Paired` counts items *both* files judged, which is the only denominator an
	// adjudication can have: a half-labelled item needs a label, not a decision.
	for _, id := range order {
		if _, ok := ia[id]; !ok {
			continue
		}
		if _, ok := ib[id]; !ok {
			continue
		}
		receipt.Paired++
	}
	for _, n := range needs {
		for _, k := range n.DisagreesOn {
			receipt.ByKind[k]++
		}
	}

	if *stub {
		receipt.Mode = "stub"
		return adjudicateStub(needs, receipt, *out, nameA, nameB, stdout, stderr)
	}
	receipt.Mode = "check"
	return adjudicateCheck(needs, byItem, receipt, *adjPath, stdout, stderr)
}

// adjudicateAnnotators enforces the precondition the whole round rests on: two files, two
// names, one name per file. It returns the two names so the stub can seed `adjudicator`'s
// neighbour field in the printed instructions ("decided by a third person, not by either
// of the two who disagreed").
func adjudicateAnnotators(la, lb []Label) (string, string, []Problem) {
	var problems []Problem
	names := func(ls []Label) (string, int) {
		seen := map[string]bool{}
		for _, l := range ls {
			if a := trimSpace(l.Annotator); a != "" {
				seen[a] = true
			}
		}
		if len(seen) == 1 {
			for a := range seen {
				return a, 1
			}
		}
		return "", len(seen)
	}
	nameA, nA := names(la)
	nameB, nB := names(lb)
	switch {
	case nA == 0:
		problems = append(problems, Problem{"labels_without_annotator",
			"no row names an annotator: the two-anonymity rule is what κ measures, so a file with no name " +
				"cannot be one side of it"})
	case nA > 1:
		problems = append(problems, Problem{"labels_mix_annotators",
			fmt.Sprintf("labels-a carries %d annotators: an adjudication is between exactly two people", nA)})
	}
	switch {
	case nB == 0:
		problems = append(problems, Problem{"labels_without_annotator",
			"labels-b has no row naming an annotator"})
	case nB > 1:
		problems = append(problems, Problem{"labels_mix_annotators",
			fmt.Sprintf("labels-b carries %d annotators: an adjudication is between exactly two people", nB)})
	}
	if nameA != "" && nameA == nameB {
		problems = append(problems, Problem{"same_annotator_twice",
			fmt.Sprintf("both files say annotator %q: one person's file agrees with itself perfectly, so "+
				"\"no disagreements\" here would be an artefact of the command, not a measurement", nameA)})
	}
	return nameA, nameB, problems
}

// adjudicationNeeds is `assembleItems` read from the other side: exactly the items whose
// merge would be dropped, and exactly the cells that would have to be settled.
//
// The three cases, in the order the merge hits them:
//
//   - verdicts differ            → the verdict is the decision (and so is question ①,
//     because a differing verdict means neither file's answer can be assumed: the merge
//     takes the adjudication's `quote_status` as given);
//   - verdicts agree, ① differs  → `quote_status` is the decision; the verdict is a fact;
//   - verdicts agree, ① agrees   → if both named a *different* candidate row, the reference
//     is the decision (E2 groups by `cited_elsewhere`, so which row it was is a reported
//     number). When the verdicts differ the candidate is optional: the merge takes whatever
//     the adjudication names, including nothing.
func adjudicationNeeds(items []Item, ia, ib map[string]Label) ([]adjNeed, []string) {
	var needs []adjNeed
	var unpaired []string
	for _, it := range items {
		a, aok := ia[it.ItemID]
		b, bok := ib[it.ItemID]
		if !aok || !bok {
			// One person's judgement is not a gold standard. It is also not a decision the
			// adjudicator can make: a missing label has to be written by the person who
			// owes it.
			if aok || bok {
				unpaired = append(unpaired, it.ItemID)
			}
			continue
		}
		n := adjNeed{
			ItemID: it.ItemID,
			A:      opinionOf(a),
			B:      opinionOf(b),
		}
		sa, sb := labelQuoteStatus(a), labelQuoteStatus(b)
		if a.Verdict != b.Verdict {
			n.DisagreesOn = append(n.DisagreesOn, adjKindVerdict)
			if sa != sb {
				n.DisagreesOn = append(n.DisagreesOn, adjKindQuoteStatus)
			} else {
				n.QuoteStatus = sa
			}
		} else {
			n.Verdict = a.Verdict
			if sa != sb {
				n.DisagreesOn = append(n.DisagreesOn, adjKindQuoteStatus)
			} else {
				n.QuoteStatus = sa
				ra, rb := candRefOf(a), candRefOf(b)
				switch {
				case ra != "" && rb != "" && ra != rb:
					n.DisagreesOn = append(n.DisagreesOn, adjKindCandidate)
				case ra != "":
					n.CandidateChunkID = ra
				default:
					n.CandidateChunkID = rb
				}
			}
		}
		if len(n.DisagreesOn) > 0 {
			needs = append(needs, n)
		}
	}
	return needs, unpaired
}

func opinionOf(l Label) adjOpinion {
	return adjOpinion{
		Verdict:          trimSpace(l.Verdict),
		QuoteStatus:      labelQuoteStatus(l),
		CandidateChunkID: candRefOf(l),
		Note:             trimSpace(l.Note),
	}
}

func adjudicateStub(needs []adjNeed, receipt adjudicateReceipt, out, nameA, nameB string, stdout, stderr *os.File) int {
	if trimSpace(out) == "" {
		fmt.Fprintln(stderr, "goldtool adjudicate: --out is required with --stub")
		return exitUsage
	}
	lines := make([]string, 0, len(needs))
	for _, n := range needs {
		row := adjStubRow{
			ItemID: n.ItemID, DisagreesOn: n.DisagreesOn, A: n.A, B: n.B,
			Verdict: n.Verdict, QuoteStatus: n.QuoteStatus, CandidateChunkID: n.CandidateChunkID,
		}
		raw, err := json.Marshal(row)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool adjudicate: encode %s: %v\n", n.ItemID, err)
			return exitInvariant
		}
		lines = append(lines, string(raw))
	}
	if code := writeAdjudications(out, lines, stderr); code != exitOK {
		return code
	}

	receipt.File = out
	receipt.Rows = len(lines)
	receipt.OK = true
	fmt.Fprintf(stdout, "goldtool adjudicate: %d row(s) need a decision over %d sampled item(s) -> %s\n",
		len(needs), receipt.Items, out)
	if len(needs) == 0 {
		fmt.Fprintln(stdout, "  the two files agree on every item both labelled: nothing to adjudicate, and "+
			"`assemble` needs no --adjudication")
	}
	for _, k := range []string{adjKindVerdict, adjKindQuoteStatus, adjKindCandidate} {
		if receipt.ByKind[k] > 0 {
			fmt.Fprintf(stdout, "  %-19s %d\n", k, receipt.ByKind[k])
		}
	}
	if len(receipt.Unpaired) > 0 {
		fmt.Fprintf(stdout, "  %d item(s) labelled by one annotator only (an adjudication cannot replace a "+
			"missing label; it needs `goldtool labels --check` on that side)\n", len(receipt.Unpaired))
	}
	fmt.Fprintf(stdout, "  fill in, per row: %s, quote_status (%v), adjudicator, reason; candidate_chunk_id only "+
		"when \"disagrees_on\" names it\n", "verdict ("+strings.Join(GoldVerdicts, "/")+")",
		model.ReportQuoteStatuses())
	fmt.Fprintf(stdout, "  the third person is neither %s nor %s\n", nameA, nameB)
	fmt.Fprintf(stdout, "  then: goldtool adjudicate --check --worksheet %s --labels-a %s --labels-b %s "+
		"--adjudication %s\n", receipt.Worksheet, receipt.LabelsA, receipt.LabelsB, out)
	printJSON(stdout, receipt)
	return exitOK
}

// writeAdjudications is `writeLinesExclusive` with this command's nouns in the refusal: the
// reason a stub may not overwrite is not that a file is precious, it is that the file may
// already hold reasons somebody wrote by reading the evidence.
func writeAdjudications(path string, lines []string, stderr *os.File) int {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "goldtool adjudicate: %v\n", err)
			return exitUsage
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			fmt.Fprintf(stderr, "goldtool adjudicate: %s already exists: refusing to overwrite it (a second "+
				"stub must go to a new path, so no decision already written can be erased)\n", path)
			return exitUsage
		}
		fmt.Fprintf(stderr, "goldtool adjudicate: %v\n", err)
		return exitUsage
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			fmt.Fprintf(stderr, "goldtool adjudicate: write %s: %v\n", path, err)
			return exitUsage
		}
	}
	return exitOK
}

func adjudicateCheck(needs []adjNeed, byItem map[string]Item, receipt adjudicateReceipt, adjPath string, stdout, stderr *os.File) int {
	if trimSpace(adjPath) == "" {
		fmt.Fprintln(stderr, "goldtool adjudicate: --adjudication is required with --check")
		return exitUsage
	}
	rows, err := readAdjudications(adjPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool adjudicate: adjudication: %v\n", err)
		return exitUsage
	}

	needByID := make(map[string]adjNeed, len(needs))
	for _, n := range needs {
		needByID[n.ItemID] = n
	}
	var problems []Problem
	var undecided []string
	decided := map[string]bool{}
	seen := map[string]int{}
	receipt.ByVerdict = map[string]int{}

	for i, r := range rows {
		at := fmt.Sprintf("line %d", i+1)
		id := trimSpace(r.ItemID)
		if id == "" {
			problems = append(problems, Problem{"adjudication_without_item_id", at})
			continue
		}
		if prev, dup := seen[id]; dup {
			problems = append(problems, Problem{"duplicate_adjudication",
				fmt.Sprintf("%s: %s was already adjudicated at line %d", at, id, prev)})
			continue
		}
		seen[id] = i + 1
		n, needed := needByID[id]
		switch {
		case !needed:
			// Two different faults share this branch on purpose: an id nobody labelled is a
			// typo, an id the two annotators agreed on is an overwrite. Both are refused, and
			// the message says which one this is.
			if _, known := byItem[id]; known {
				problems = append(problems, Problem{"adjudication_without_disagreement",
					fmt.Sprintf("%s: %s: the two annotators agree on verdict and quote_status — an adjudication "+
						"row here would overwrite a judgement nobody disputed, not settle one", at, id)})
			} else {
				problems = append(problems, Problem{"adjudication_for_unknown_item",
					fmt.Sprintf("%s: %s is not in the worksheet (assemble would ignore this row, so the reason "+
						"written for it would never reach gold)", at, id)})
			}
			continue
		}

		verdict := trimSpace(r.Verdict)
		status := trimSpace(r.QuoteStatus)
		adj := trimSpace(r.Adjudicator)
		reason := trimSpace(r.Reason)
		before := len(problems)

		if !contains(GoldVerdicts, verdict) {
			// An empty verdict is the unfinished case, not a violation: the row exists and the
			// person has not decided. Anything else is a fault.
			if verdict == "" {
				receipt.ByVerdict["(blank)"]++
			} else {
				problems = append(problems, Problem{"bad_adjudication_verdict",
					fmt.Sprintf("%s: %s: %q is outside the gold vocabulary %v", at, id, verdict, GoldVerdicts)})
			}
		} else {
			receipt.ByVerdict[verdict]++
		}
		if status != "" && !contains(model.ReportQuoteStatuses(), status) {
			problems = append(problems, Problem{"bad_adjudication_quote_status",
				fmt.Sprintf("%s: %s: %q is not one of %v", at, id, status, model.ReportQuoteStatuses())})
		}
		// A blank reason or name is unfinished, not wrong: a fresh stub is all blank cells, and
		// a checker that called the starting state "problems" would bury the one number the
		// adjudicator needs (how many are left). `assemble` is where a *submitted* row without a
		// reason is refused (`adjudication_without_reason`), and this command does not get to be
		// more forgiving than the merge it feeds.

		// The narrowness rule: a row may only decide what the two files did not agree on. A
		// "tidied" fact (A and B both said `verified`, the row says `not_found`) is not an
		// adjudication of a disagreement — it is an edit of an agreement, and gold would carry
		// it with the adjudicator's name on it.
		if !disagreesOn(n, adjKindVerdict) && verdict != "" && n.Verdict != "" && verdict != n.Verdict {
			problems = append(problems, Problem{"adjudication_changed_agreed_fact",
				fmt.Sprintf("%s: %s: verdict is %q in both files but the row says %q", at, id, n.Verdict, verdict)})
		}
		if !disagreesOn(n, adjKindQuoteStatus) && status != "" && n.QuoteStatus != "" && status != n.QuoteStatus {
			problems = append(problems, Problem{"adjudication_changed_agreed_fact",
				fmt.Sprintf("%s: %s: quote_status is %q in both files but the row says %q", at, id, n.QuoteStatus, status)})
		}
		ref := trimSpace(r.CandidateChunkID)
		if !disagreesOn(n, adjKindCandidate) && ref != "" && n.CandidateChunkID != "" && ref != n.CandidateChunkID {
			problems = append(problems, Problem{"adjudication_changed_agreed_fact",
				fmt.Sprintf("%s: %s: the two files name candidate %q but the row says %q", at, id, n.CandidateChunkID, ref)})
		}
		if ref != "" {
			if bad := candidateRefInItem(byItem[id], ref); bad != "" {
				problems = append(problems, Problem{"adjudication_candidate_unknown",
					fmt.Sprintf("%s: %s: %s", at, id, bad)})
			}
		}
		if len(problems) > before {
			continue
		}

		// Decided: every required cell is in its closed set, and the row has a reason and a
		// name. A required-but-blank cell is unfinished, never "the merge will work it out".
		unfinished := verdict == "" || status == "" || reason == "" || adj == ""
		if disagreesOn(n, adjKindCandidate) && ref == "" {
			unfinished = true
		}
		if unfinished {
			undecided = append(undecided, id)
		} else {
			decided[id] = true
		}
	}

	var missing []string
	for _, n := range needs {
		if _, ok := seen[n.ItemID]; !ok {
			missing = append(missing, n.ItemID)
		}
	}
	todo := len(undecided) + len(missing)

	receipt.File = adjPath
	receipt.Rows = len(rows)
	receipt.Decided = len(decided)
	receipt.Undecided = cap10(undecided)
	receipt.Missing = cap10(missing)
	receipt.Problems = problems
	receipt.OK = len(problems) == 0 && todo == 0

	for _, pr := range problems {
		fmt.Fprintf(stderr, "goldtool adjudicate: %s: %s\n", pr.Code, pr.Message)
	}
	fmt.Fprintf(stdout, "goldtool adjudicate: %s  %d/%d decided, %d problem(s)\n",
		adjPath, receipt.Decided, len(needs), len(problems))
	if len(undecided) > 0 {
		fmt.Fprintf(stdout, "  undecided (%d): %s\n", len(undecided), strings.Join(cap10(undecided), " "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(stdout, "  no row at all (%d): %s\n", len(missing), strings.Join(cap10(missing), " "))
	}
	printJSON(stdout, receipt)
	switch {
	case len(problems) > 0:
		return exitInvariant
	case todo > 0:
		fmt.Fprintf(stderr, "goldtool adjudicate: %d row(s) not decided yet: this file is not wrong, it is "+
			"unfinished (exit 3, not pass)\n", todo)
		return exitIncomplete
	}
	return exitOK
}

func disagreesOn(n adjNeed, kind string) bool {
	for _, k := range n.DisagreesOn {
		if k == kind {
			return true
		}
	}
	return false
}

// sortedAdjItemIDs keeps the printed lists stable; map iteration would make the same input
// produce a different message on every run, which reads like the sample changed.
func sortedAdjItemIDs(m map[string]adjNeed) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
