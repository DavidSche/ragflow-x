package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// minKappa is the plan's floor (doc/301 §9.1): two humans must agree at κ ≥ 0.70
// before their labels may become a gold standard. Below 0.60 the instruction is
// explicit — fix the manual, do not tune the model, because a disagreement between
// annotators means the *definition* is ambiguous and a model calibrated against an
// ambiguous definition is calibrated against nothing.
const minKappa = 0.70

// kappaSchema identifies the receipt shape so a future reader can tell receipts apart.
const kappaSchema = "rgx.goldtool.kappa/1"

// Label is one annotator's opinion of one item. It is deliberately a separate file
// from the gold item: keeping the two raw opinions means κ can be recomputed and
// audited forever, and it makes "who said what" impossible to overwrite by
// re-assembling.
type Label struct {
	ItemID      string   `json:"item_id"`
	Annotator   string   `json:"annotator"`
	Verdict     string   `json:"verdict"`
	QuoteStatus string   `json:"quote_status"`
	Quote       string   `json:"quote,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Note        string   `json:"note,omitempty"`
	// CandidateIndex / CandidateChunkID let an annotator say *which* frozen
	// candidate row drove the judgement. They are optional because most items have no
	// candidate at all, but when they are present they are checked against the item's
	// own candidate_pool (`assemble` refuses a reference to a chunk the item does not
	// carry) and carried into the gold file as `gold_candidate_chunk_id`.
	CandidateIndex   *int   `json:"candidate_index,omitempty"`
	CandidateChunkID string `json:"candidate_chunk_id,omitempty"`
}

// Agreement is the pairwise κ receipt.
//
// The four coverage fields exist because κ alone cannot tell "they agree" from "they
// agree on the five items they both got to". `Pairs` is the κ denominator; when it is
// smaller than `WorksheetItems` the number describes a subset of the sample, and the
// receipt says so instead of leaving the reader to compare two files by hand.
type Agreement struct {
	Schema              string         `json:"schema"`
	Annotators          []string       `json:"annotators"`
	Worksheet           string         `json:"worksheet"`
	WorksheetItems      int            `json:"worksheet_items"`
	LabelledByAnnotator map[string]int `json:"labelled_by_annotator"`
	LabelledByAll       int            `json:"items_labelled_by_all"`
	MissingCells        int            `json:"annotator_item_cells_missing"`
	Pairs               int            `json:"pairs"`
	Observed            float64        `json:"observed_agreement"`
	Expected            float64        `json:"expected_agreement"`
	Kappa               float64        `json:"cohen_kappa"`
	Disagreed           int            `json:"disagreed"`
	PerClass            map[string]int `json:"disagreements_by_gold_class"`
	MeetsTarget         bool           `json:"meets_target"`
	Unfinished          bool           `json:"unfinished"`
	Problems            []Problem      `json:"problems,omitempty"`
	OK                  bool           `json:"ok"`
}

// labelledLine keeps the file position: every κ problem is somebody's line 47, and a
// receipt that says "an unknown item_id" without the line sends the annotator hunting.
type labelledLine struct {
	Label
	Line int
}

// cohenKappa implements Cohen's κ on two label vectors: κ = (p0 − pe) / (1 − pe).
//
// It is written out rather than pulled from a library because the two quantities it
// needs are exactly the two the report must print: observed agreement (what a
// reader thinks "agreement" means) and expected-by-chance (what makes κ different
// from it). A library call returns a float and hides the denominator.
func cohenKappa(a, b []string) (kappa, observed, expected float64, n int) {
	if len(a) != len(b) || len(a) == 0 {
		return 0, 0, 0, 0
	}
	n = len(a)
	agree := 0
	countA := map[string]int{}
	countB := map[string]int{}
	for i := range a {
		if a[i] == b[i] {
			agree++
		}
		countA[a[i]]++
		countB[b[i]]++
	}
	observed = float64(agree) / float64(n)
	for label, ca := range countA {
		cb := countB[label]
		expected += (float64(ca) / float64(n)) * (float64(cb) / float64(n))
	}
	if expected == 1 {
		// Both annotators used a single identical label everywhere: κ is 1 by
		// definition (1−pe is 0). Guarding here keeps a degenerate file from
		// producing NaN.
		return 1, observed, expected, n
	}
	kappa = (observed - expected) / (1 - expected)
	return kappa, observed, expected, n
}

func runKappa(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("kappa", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("labels", "", "per-annotator label JSONL file")
	worksheetPath := fs.String("worksheet", "", "worksheet JSONL produced by `goldtool sample` (the κ denominator)")
	min := fs.Float64("min", minKappa, "κ floor")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if trimSpace(*path) == "" {
		fmt.Fprintln(stderr, "goldtool kappa: --labels is required")
		return exitUsage
	}
	if trimSpace(*worksheetPath) == "" {
		// Without the worksheet, an item_id with one wrong character is simply not in any
		// pair: κ still prints, the pair count silently shrinks, and the number looks like a
		// κ. The denominator has to be a file, not an assumption.
		fmt.Fprintln(stderr, "goldtool kappa: --worksheet is required: κ is an agreement rate over a known "+
			"denominator, and without the worksheet a mistyped item_id is silently dropped instead of reported")
		return exitUsage
	}

	items, err := readItems(*worksheetPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kappa: worksheet: %v\n", err)
		return exitUsage
	}
	if len(items) == 0 {
		fmt.Fprintf(stderr, "goldtool kappa: worksheet %s has no items\n", *worksheetPath)
		return exitInvariant
	}
	lines, err := readLabelsAt(*path)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kappa: %v\n", err)
		return exitUsage
	}

	problems := validateLabelsAgainstWorksheet(items, lines, *worksheetPath)
	ag := Agreement{Schema: kappaSchema, Worksheet: *worksheetPath, WorksheetItems: len(items),
		Problems: problems, PerClass: map[string]int{}}

	// κ pairs only the rows that carry a judgement. A blank verdict is not a disagreement,
	// it is a row nobody judged yet — and 301 §9.1 says so: an item the annotator cannot
	// decide is `abstained`, which *is* a verdict and *does* enter the denominator. A blank
	// row is therefore unfinished, and the exit code has to say so (3), not 0.
	judged := make([]Label, 0, len(lines))
	for _, l := range lines {
		if trimSpace(l.Verdict) == "" {
			continue
		}
		judged = append(judged, l.Label)
	}
	names := annotatorNames(lines)
	ag.Annotators = names
	ag.LabelledByAnnotator, ag.LabelledByAll, ag.MissingCells = coverage(items, lines, names)
	ag.Unfinished = ag.MissingCells > 0

	if len(problems) == 0 && len(names) < 2 {
		// One name in the file is not a κ: either the other half has not been merged yet, or
		// both files say the same `annotator`. The tool cannot tell those apart, so it does not
		// pick — it refuses, and names both readings. Exit 3 because "the second half is
		// missing" is the common case and neither reading is a pass.
		ag.OK = false
		ag.Unfinished = true
		reportKappa(stdout, stderr, ag, *path)
		fmt.Fprintf(stderr, "goldtool kappa: only %d annotator(s) in %s: either the second half is not merged yet "+
			"or both files carry the same `annotator` name — check the field before reading any κ "+
			"(exit 3, not pass)\n", len(names), *path)
		return exitIncomplete
	}

	if len(problems) == 0 {
		pair, err := agreement(items, judged)
		if err != nil {
			// "κ needs two annotators" over a one-sided file is not a mis-typed command: the
			// second half of the sample has not been labelled. Report it as unfinished when
			// the coverage numbers already say so; otherwise it is a named violation (one file
			// carrying one annotator is not κ, it is a draft).
			if ag.Unfinished {
				ag.OK = false
				reportKappa(stdout, stderr, ag, *path)
				fmt.Fprintf(stderr, "goldtool kappa: %v; %d of %d annotator-item cells are still blank: "+
					"this is not a low κ, it is unfinished (exit 3, not pass)\n", err, ag.MissingCells,
					len(items)*max(1, len(names)))
				return exitIncomplete
			}
			fmt.Fprintf(stderr, "goldtool kappa: %v\n", err)
			return exitInvariant
		}
		ag.Pairs, ag.Observed, ag.Expected, ag.Kappa = pair.Pairs, pair.Observed, pair.Expected, pair.Kappa
		ag.Disagreed = pair.Disagreed
		if pair.PerClass != nil {
			ag.PerClass = pair.PerClass
		}
		if len(pair.Annotators) > 0 {
			ag.Annotators = pair.Annotators
		}
	}
	ag.MeetsTarget = ag.Kappa >= *min && ag.Pairs > 0
	ag.OK = len(problems) == 0 && !ag.Unfinished && ag.MeetsTarget

	reportKappa(stdout, stderr, ag, *path)
	switch {
	case len(problems) > 0:
		// A label naming an item the worksheet does not have, a duplicate row, a verdict
		// outside the closed set: each is a fault in the file, and κ over the remaining rows
		// would be a number about a sample that does not exist.
		fmt.Fprintf(stderr, "goldtool kappa: %d problem(s) in %s; κ is not computed over a file whose rows "+
			"do not line up with the worksheet\n", len(problems), *path)
		return exitInvariant
	case ag.Unfinished:
		fmt.Fprintf(stderr, "goldtool kappa: %d of %d annotator-item cells are blank (%d of %d items labelled by "+
			"everyone): this is not a low κ, it is unfinished (exit 3, not pass)\n",
			ag.MissingCells, len(items)*max(1, len(names)), ag.LabelledByAll, len(items))
		return exitIncomplete
	case !ag.MeetsTarget:
		if ag.Kappa < 0.60 {
			fmt.Fprintf(stderr, "goldtool kappa: κ %.3f is below 0.60: fix the manual before touching the model "+
				"(doc/301 §9.1)\n", ag.Kappa)
		} else {
			fmt.Fprintf(stderr, "goldtool kappa: κ %.3f is below the %.2f floor over %d pair(s)\n",
				ag.Kappa, *min, ag.Pairs)
		}
		return exitInvariant
	}
	return exitOK
}

func reportKappa(stdout, stderr *os.File, ag Agreement, path string) {
	for _, pr := range ag.Problems {
		fmt.Fprintf(stderr, "goldtool kappa: %s: %s\n", pr.Code, pr.Message)
	}
	// κ 0.000 over 0 pairs reads like a measured zero. It is not: nothing was paired, so
	// the receipt says the number was never computed and the pair count carries the fact.
	kappaText := "κ not computed"
	if ag.Pairs > 0 {
		kappaText = fmt.Sprintf("κ %.3f", ag.Kappa)
	}
	coverage := "no labels yet"
	switch {
	case len(ag.Annotators) == 1:
		coverage = fmt.Sprintf("%d item(s) labelled by the only annotator (%s)",
			ag.LabelledByAnnotator[ag.Annotators[0]], ag.Annotators[0])
	case len(ag.Annotators) > 1:
		coverage = fmt.Sprintf("%d/%d item(s) labelled by everyone, %d cell(s) blank",
			ag.LabelledByAll, ag.WorksheetItems, ag.MissingCells)
	}
	fmt.Fprintf(stdout, "goldtool kappa: %s  %s over %d pair(s); %s\n", path, kappaText, ag.Pairs, coverage)
	printJSON(stdout, ag)
}

// validateLabelsAgainstWorksheet states, per line, everything that makes a label file
// unusable as κ input. It is the same set `goldtool labels --check` enforces, re-stated
// here because `--check` is a step an annotator may skip: the guard has to exist where
// the number is produced, or "I ran --check" becomes the only thing standing between a
// typo and a published κ.
func validateLabelsAgainstWorksheet(items []Item, lines []labelledLine, worksheet string) []Problem {
	byID := make(map[string]bool, len(items))
	for _, it := range items {
		byID[it.ItemID] = true
	}
	var problems []Problem
	seen := map[string]int{}
	for _, l := range lines {
		at := fmt.Sprintf("line %d", l.Line)
		switch {
		case trimSpace(l.ItemID) == "":
			problems = append(problems, Problem{"label_without_item_id", at})
			continue
		case trimSpace(l.Annotator) == "":
			problems = append(problems, Problem{"label_without_annotator",
				at + ": every row names who judged it, or two files merge into one opinion"})
			continue
		}
		if !byID[l.ItemID] {
			problems = append(problems, Problem{"label_for_unknown_item",
				fmt.Sprintf("%s: %s is not in %s — κ would skip it, so the item would never be counted", at,
					l.ItemID, worksheet)})
			continue
		}
		key := l.ItemID + "\x00" + l.Annotator
		if prev, dup := seen[key]; dup {
			problems = append(problems, Problem{"duplicate_label",
				fmt.Sprintf("%s: %s already has a judgement from %q at line %d", at, l.ItemID, l.Annotator, prev)})
			continue
		}
		seen[key] = l.Line
		if v := trimSpace(l.Verdict); v != "" && !contains(GoldVerdicts, v) {
			problems = append(problems, Problem{"label_verdict_outside_vocabulary",
				fmt.Sprintf("%s: verdict %q is not one of %v", at, v, GoldVerdicts)})
		}
	}
	return problems
}

// annotatorNames is the sorted set of names that labelled at least one item. A row with
// no name is not a third annotator; it is a problem `validateLabelsAgainstWorksheet`
// already reported.
func annotatorNames(lines []labelledLine) []string {
	set := map[string]bool{}
	for _, l := range lines {
		if a := trimSpace(l.Annotator); a != "" && trimSpace(l.ItemID) != "" {
			set[a] = true
		}
	}
	names := make([]string, 0, len(set))
	for a := range set {
		names = append(names, a)
	}
	sort.Strings(names)
	return names
}

// coverage answers the question κ cannot: for how much of the worksheet do we actually
// have everyone's opinion? `missing` counts (item, annotator) cells, so one person
// leaving two items blank is 2 missing cells, not 1 — the number a project manager
// needs in order to know how many hours are left.
func coverage(items []Item, lines []labelledLine, names []string) (map[string]int, int, int) {
	filled := map[string]map[string]bool{}
	for _, l := range lines {
		if trimSpace(l.Verdict) == "" || trimSpace(l.Annotator) == "" {
			continue
		}
		if filled[l.ItemID] == nil {
			filled[l.ItemID] = map[string]bool{}
		}
		filled[l.ItemID][l.Annotator] = true
	}
	perName := map[string]int{}
	all := 0
	for _, it := range items {
		everyone := len(names) > 0
		for _, n := range names {
			if filled[it.ItemID][n] {
				perName[n]++
			} else {
				everyone = false
			}
		}
		if everyone {
			all++
		}
	}
	missing := 0
	for _, n := range names {
		missing += len(items) - perName[n]
	}
	return perName, all, missing
}

// agreement computes the *pairwise* κ over every pair of annotators that labelled
// overlapping items, and averages nothing: with two annotators there is one pair and
// the average of one number is that number; with three (the adjudicator counts as one
// only if they also labelled blindly) the per-pair numbers are what a reader needs.
//
// When `items` is non-nil, only pairs of labels whose item is in the gold file are
// used — that is what makes "κ over the gold set" a different claim from "κ over
// whatever we annotated".
func agreement(items []Item, labels []Label) (Agreement, error) {
	if len(labels) == 0 {
		return Agreement{}, fmt.Errorf("label file is empty")
	}
	keep := map[string]bool{}
	if items != nil {
		for _, it := range items {
			keep[it.ItemID] = true
		}
	}

	// byItem[item][annotator] = verdict
	byItem := map[string]map[string]string{}
	annotators := map[string]bool{}
	for _, l := range labels {
		if l.Annotator == "" || l.ItemID == "" {
			return Agreement{}, fmt.Errorf("label with empty annotator or item_id: %+v", l)
		}
		if items != nil && !keep[l.ItemID] {
			continue
		}
		if !contains(GoldVerdicts, l.Verdict) {
			return Agreement{}, fmt.Errorf("label %s/%s has verdict %q outside the gold vocabulary", l.ItemID, l.Annotator, l.Verdict)
		}
		if byItem[l.ItemID] == nil {
			byItem[l.ItemID] = map[string]string{}
		}
		if _, dup := byItem[l.ItemID][l.Annotator]; dup {
			return Agreement{}, fmt.Errorf("annotator %q labelled item %q twice", l.Annotator, l.ItemID)
		}
		byItem[l.ItemID][l.Annotator] = l.Verdict
		annotators[l.Annotator] = true
	}

	names := make([]string, 0, len(annotators))
	for a := range annotators {
		names = append(names, a)
	}
	sort.Strings(names)
	if len(names) < 2 {
		return Agreement{Annotators: names}, fmt.Errorf("only %d annotator(s) present; κ needs two", len(names))
	}

	// Pool the pairwise comparisons into one κ: for each pair of annotators, every
	// common item contributes one (a,b) observation. This is the standard way to
	// read "two of three agreed" without inventing a majority vote.
	var av, bv []string
	disagreed := map[string]int{}
	for _, it := range sortedItemIDs(byItem) {
		perAnnotator := byItem[it]
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				a, aok := perAnnotator[names[i]]
				b, bok := perAnnotator[names[j]]
				if !aok || !bok {
					continue
				}
				av = append(av, a)
				bv = append(bv, b)
				if a != b {
					disagreed[rootClass(a, b)]++
				}
			}
		}
	}
	if len(av) == 0 {
		return Agreement{Annotators: names}, nil
	}

	kappa, observed, expected, n := cohenKappa(av, bv)
	disagreeTotal := 0
	for _, c := range disagreed {
		disagreeTotal += c
	}
	return Agreement{
		Annotators: names, Pairs: n, Observed: observed, Expected: expected,
		Kappa: kappa, Disagreed: disagreeTotal, PerClass: disagreed,
		MeetsTarget: kappa >= minKappa,
	}, nil
}

// rootClass names a disagreement in the direction that matters for the experiment:
// a `supported` vs `unsupported` argument is a false-certification risk, while
// `supported` vs `partially_supported` is a boundary-drawing argument. Lump them
// together and the κ report tells you "they disagree" without telling you whether
// the disagreement is expensive.
func rootClass(a, b string) string {
	hi := func(v string) bool { return v == "supported" || v == "partially_supported" }
	if hi(a) && hi(b) {
		return "supported_boundary"
	}
	if hi(a) || hi(b) {
		return "certification"
	}
	return "other"
}

func sortedItemIDs(byItem map[string]map[string]string) []string {
	ids := make([]string, 0, len(byItem))
	for id := range byItem {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// readLabelsAt is `readLabels` plus the line number, for callers whose job is to point
// at the faulty row.
func readLabelsAt(path string) ([]labelledLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []labelledLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if text == "" {
			continue
		}
		var l Label
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, labelledLine{Label: l, Line: line})
	}
	return out, sc.Err()
}

func readLabels(path string) ([]Label, error) {
	lines, err := readLabelsAt(path)
	if err != nil {
		return nil, err
	}
	out := make([]Label, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Label)
	}
	return out, nil
}
