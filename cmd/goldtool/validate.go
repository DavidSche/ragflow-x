package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Problem is one named violation. `Code` exists so tests (and a future gate row)
// can assert *which* invariant fired instead of matching prose.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Code + ": " + p.Message }

// validateReport is the machine-readable result `validate` prints.
type validateReport struct {
	File        string         `json:"file"`
	Items       int            `json:"items"`
	Revisions   int            `json:"revisions"`
	Datasets    int            `json:"datasets"`
	Templates   []string       `json:"templates"`
	Verdicts    map[string]int `json:"verdicts"`
	QuoteStatus map[string]int `json:"quote_status"`
	Problems    []Problem      `json:"problems"`
	OK          bool           `json:"ok"`
}

func runValidate(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	gold := fs.String("gold", "", "gold JSONL file")
	labels := fs.String("labels", "", "optional per-annotator label file, for the κ gate")
	strictCoverage := fs.Bool("strict-coverage", true, "enforce 6 revisions / 3 templates / 2 datasets (doc/301 §9.1)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*gold) == "" {
		fmt.Fprintln(stderr, "goldtool validate: --gold is required")
		return exitUsage
	}
	// No `goldRefusals` here, on purpose (E78): `eval` and `preds` refuse a gold file that
	// `assemble` refused, because they turn a gold file into numbers. `validate` turns it
	// into a *description* — its product is the list of what is wrong with the file — so
	// refusing a draft would leave the operator with a diagnostic that declines to
	// diagnose. Its `ok` therefore answers "is this file structurally coherent", never
	// "may this file be scored": qualification is the readers' answer, and the manual
	// says so (step 4 of §6). Pinned by
	// TestValidateDescribesARefusedDraftWithoutQualifyingIt.
	items, err := readItems(*gold)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool validate: %v\n", err)
		return exitUsage
	}

	problems := validateItems(items, *strictCoverage)
	report := validateReport{
		File: *gold, Items: len(items),
		Revisions: len(distinctSet(items, func(i Item) string { return i.Source.RevisionID })),
		Datasets:  len(distinctSet(items, func(i Item) string { return joinIDs(i.Source.DatasetIDs) })),
		Templates: distinctSet(items, func(i Item) string { return i.Source.TemplateKey }),
		Verdicts:  countBy(items, func(i Item) string { return i.GoldVerdict }),
		QuoteStatus: countBy(items, func(i Item) string {
			return i.GoldQuoteStatus
		}),
	}

	if *labels != "" {
		ls, err := readLabels(*labels)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool validate: %v\n", err)
			return exitUsage
		}
		ag, err := agreement(items, ls)
		if err != nil {
			// A label file that does not line up with the gold file is a usage error:
			// the pair is not a measurement of anything.
			fmt.Fprintf(stderr, "goldtool validate: %v\n", err)
			return exitUsage
		}
		if ag.Pairs == 0 {
			problems = append(problems, Problem{"kappa_no_pairs", "label file shares no items with the gold file"})
		} else if ag.Kappa < minKappa {
			problems = append(problems, Problem{"kappa_below_target",
				fmt.Sprintf("Cohen's κ = %.4f over %d pairs is below the %.2f floor: fix the manual, not the model (doc/301 §9.1)", ag.Kappa, ag.Pairs, minKappa)})
		}
	}

	report.Problems = problems
	report.OK = len(problems) == 0
	printJSON(stdout, report)
	if report.OK {
		return exitOK
	}
	return exitInvariant
}

// validateItems applies every §9.1 rule that can be checked without a database.
func validateItems(items []Item, strictCoverage bool) []Problem {
	var problems []Problem
	if len(items) == 0 {
		return []Problem{{"empty", "the file has no items"}}
	}

	seenItem := map[string]int{}
	seenClaim := map[string]int{}
	for i, it := range items {
		for _, msg := range checkItem(it, i) {
			problems = append(problems, Problem{"item", msg})
		}
		if prev, dup := seenItem[it.ItemID]; dup {
			problems = append(problems, Problem{"duplicate_item_id",
				fmt.Sprintf("item_id %q appears at lines %d and %d", it.ItemID, prev+1, i+1)})
		} else {
			seenItem[it.ItemID] = i
		}
		if prev, dup := seenClaim[it.ClaimID]; dup {
			problems = append(problems, Problem{"duplicate_claim_id",
				fmt.Sprintf("claim_id %q appears at lines %d and %d: a claim counted twice silently doubles a denominator", it.ClaimID, prev+1, i+1)})
		} else {
			seenClaim[it.ClaimID] = i
		}
	}

	// Distribution. This is the rule that keeps the pre-registered thresholds
	// meaningful, so it reports the *difference*, not just "wrong".
	got := countBy(items, func(i Item) string { return i.GoldVerdict })
	for _, verdict := range GoldVerdicts {
		want := GoldDistribution[verdict]
		if got[verdict] != want {
			problems = append(problems, Problem{"distribution_mismatch",
				fmt.Sprintf("%s: got %d, want %d (doc/301 §9.1)", verdict, got[verdict], want)})
		}
	}
	for verdict, n := range got {
		if _, planned := GoldDistribution[verdict]; !planned && n > 0 {
			problems = append(problems, Problem{"unplanned_verdict",
				fmt.Sprintf("%d items carry %q, which is not a planned class", n, verdict)})
		}
	}
	if len(items) != GoldSize() {
		problems = append(problems, Problem{"size_mismatch",
			fmt.Sprintf("the file has %d items, the plan fixes %d", len(items), GoldSize())})
	}

	// Provenance floors. `strictCoverage=false` exists for mid-annotation previews;
	// the assembled v1 file is always validated with the floors on.
	if strictCoverage {
		revs := distinctSet(items, func(i Item) string { return i.Source.RevisionID })
		if len(revs) < 6 {
			problems = append(problems, Problem{"revision_coverage",
				fmt.Sprintf("%d revisions, the plan requires 6 (3 templates × 2 datasets)", len(revs))})
		}
		ds := map[string]bool{}
		for _, it := range items {
			for _, id := range it.Source.DatasetIDs {
				ds[id] = true
			}
		}
		if len(ds) < 2 {
			problems = append(problems, Problem{"dataset_coverage",
				fmt.Sprintf("%d datasets, the plan requires 2", len(ds))})
		}
		tpl := distinctSet(items, func(i Item) string { return i.Source.TemplateKey })
		if len(tpl) < 3 {
			problems = append(problems, Problem{"template_coverage",
				fmt.Sprintf("%d templates (%v), the plan requires 3", len(tpl), tpl)})
		}
	}

	// One more rule worth its own code: the gold set is quoted evidence, so a
	// `verified`/`ambiguous` status needs text that the snapshot can be checked
	// against later. checkItem enforces quote non-emptiness; this one enforces that
	// the quote actually occurs in one of the cited snapshots *when the pool text is
	// still present* (a redacted gold file cannot be checked here — that check lives
	// in the replay gate, which has the database).
	for i, it := range items {
		if it.GoldQuote == "" {
			continue
		}
		found := false
		for _, ev := range it.Evidence {
			if ev.Content != "" && strings.Contains(ev.Content, it.GoldQuote) {
				found = true
				break
			}
		}
		for _, ev := range it.Evidence {
			if ev.ContentExcerpt != "" && strings.Contains(ev.ContentExcerpt, it.GoldQuote) {
				found = true
				break
			}
		}
		if !found && hasAnyFullText(it) {
			problems = append(problems, Problem{"quote_not_in_evidence",
				fmt.Sprintf("line %d item %q: gold_quote does not occur in any cited snapshot", i+1, it.ItemID)})
		}
	}

	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Code < problems[j].Code })
	return problems
}

func hasAnyFullText(it Item) bool {
	for _, ev := range it.Evidence {
		if ev.Content != "" {
			return true
		}
	}
	return false
}

// readItems reads a JSONL file, skipping blank lines (a trailing newline is not a
// malformed row).
func readItems(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Item
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		it, err := decodeItem(text)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, it)
	}
	return out, sc.Err()
}

func printJSON(w *os.File, v any) {
	raw, err := marshalIndent(v)
	if err != nil {
		fmt.Fprintf(w, "{\"error\": %q}\n", err.Error())
		return
	}
	fmt.Fprintln(w, string(raw))
}

// summariseProblems collapses problems into "how many of each code", which is what
// a human reading a failed validate run needs first.
func summariseProblems(ps []Problem) string {
	if len(ps) == 0 {
		return "no problems"
	}
	byCode := map[string]int{}
	for _, p := range ps {
		byCode[p.Code]++
	}
	codes := make([]string, 0, len(byCode))
	for c := range byCode {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, fmt.Sprintf("%s×%d", c, byCode[c]))
	}
	return strings.Join(parts, " ")
}
