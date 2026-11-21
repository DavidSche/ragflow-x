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
}

// Agreement is the pairwise κ receipt.
type Agreement struct {
	Annotators  []string       `json:"annotators"`
	Pairs       int            `json:"pairs"`
	Observed    float64        `json:"observed_agreement"`
	Expected    float64        `json:"expected_agreement"`
	Kappa       float64        `json:"cohen_kappa"`
	Disagreed   int            `json:"disagreed"`
	PerClass    map[string]int `json:"disagreements_by_gold_class"`
	MeetsTarget bool           `json:"meets_target"`
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
	min := fs.Float64("min", minKappa, "κ floor")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*path) == "" {
		fmt.Fprintln(stderr, "goldtool kappa: --labels is required")
		return exitUsage
	}
	ls, err := readLabels(*path)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kappa: %v\n", err)
		return exitUsage
	}
	ag, err := agreement(nil, ls)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool kappa: %v\n", err)
		return exitUsage
	}
	ag.MeetsTarget = ag.Kappa >= *min
	printJSON(stdout, ag)
	if !ag.MeetsTarget {
		return exitInvariant
	}
	return exitOK
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
		return Agreement{}, fmt.Errorf("only %d annotator(s) present; κ needs two", len(names))
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

func readLabels(path string) ([]Label, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Label
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
		out = append(out, l)
	}
	return out, sc.Err()
}
