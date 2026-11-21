package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
)

// The candidate side of `goldtool sample`.
//
// doc/301 §9.1 constrains E2's source: the 24 conflicting samples may only come from
// **real frozen candidate rows**, with `cited_elsewhere=true/false` counted and reported
// separately, and a short group reported as an actual n rather than topped up. The item
// side of the sampler cannot express that: an item is a claim, and one claim can carry
// two candidate rows — drawing 120 items says nothing about how many *candidate rows* the
// evaluator will have to judge.
//
// So this side draws rows, not claims. The unit is therefore `(item_id, candidate_index)`
// and the receipt counts by `cited_elsewhere`, which is the only grouping §9.1 allows the
// sampler to decide on its own.

// worksheetRow is one annotatable row of the E2 worksheet: a claim, the claim's cited
// evidence context, and exactly one frozen candidate. It deliberately carries no
// `verdict`/`quote_status` field — the worksheet is input, and only `assemble` writes
// labels, from two independent annotators plus adjudication.
type worksheetRow struct {
	Unit           string `json:"unit"`
	SampleID       string `json:"sample_id"`
	ItemID         string `json:"item_id"`
	ClaimID        string `json:"claim_id"`
	CandidateIndex int    `json:"candidate_index"`
	chunkFields
	ClaimText          string `json:"claim_text"`
	ClaimType          string `json:"claim_type"`
	SectionPrompt      string `json:"section_prompt"`
	SectionOrd         int    `json:"section_ord"`
	PriorSupportStatus string `json:"prior_support_status,omitempty"`
	// Why this row is in the worksheet: the scan width it was drawn under
	// (doc/301 §5.2.2) and whether the revision cited the same chunk elsewhere.
	GoldScanScope  string     `json:"gold_scan_scope"`
	CitedElsewhere bool       `json:"cited_elsewhere"`
	Evidence       []Evidence `json:"evidence"`
	Source         Source     `json:"source"`
	AsOfDate       string     `json:"as_of_date"`
}

// chunkFields keeps the candidate half of the row readable: these four fields are what
// `gold_candidate_chunk_id` will point at once the row is annotated.
type chunkFields struct {
	ChunkID       string `json:"chunk_id"`
	ContentHash   string `json:"content_hash"`
	PoolRank      int    `json:"pool_rank"`
	Truncated     bool   `json:"truncated"`
	CandidateText string `json:"candidate_excerpt"`
	ContentChars  int    `json:"content_chars"`
}

// checkCandidateTargets closes the stratified vocabulary on this side. `--targets
// "supported=80"` would silently fill nothing here (candidate rows have no prior
// support status), and an empty row set that looks like a request that could not be met
// is the kind of quiet mismatch this tool exists to avoid.
func checkCandidateTargets(spec string) string {
	var problems []Problem
	wanted, order := parseTargets(spec, &problems)
	if len(problems) > 0 {
		return problems[0].String()
	}
	for _, name := range order {
		if name != "cited_elsewhere=true" && name != "cited_elsewhere=false" {
			return fmt.Sprintf("unknown candidate stratum %q", name)
		}
	}
	if len(order) != len(wanted) {
		return "duplicate candidate stratum"
	}
	return ""
}

func citedElsewhereKey(it Item, idx int) string {
	if it.CandidatePool[idx].CitedElsewhere {
		return "cited_elsewhere=true"
	}
	return "cited_elsewhere=false"
}

// candidateRowsOf flattens a pool into rows in a stable order: items by item_id (the
// caller already sorted), then each item's candidate_pool in its exported order, which is
// `pool_rank` order and therefore meaningful.
func candidateRowsOf(items []Item) []worksheetRow {
	var rows []worksheetRow
	for _, it := range items {
		for idx, c := range it.CandidatePool {
			rows = append(rows, worksheetRow{
				Unit: "candidate_row", ItemID: it.ItemID, ClaimID: it.ClaimID,
				CandidateIndex: idx,
				chunkFields: chunkFields{
					ChunkID: c.ChunkID, ContentHash: c.ContentHash, PoolRank: c.PoolRank,
					Truncated: c.Truncated, ContentChars: c.ContentChars,
					CandidateText: trimBounded(firstNonEmpty(c.ContentExcerpt, c.Content), maxExcerptRunes),
				},
				ClaimText: it.ClaimText, ClaimType: it.ClaimType,
				SectionPrompt: it.SectionPrompt, SectionOrd: it.SectionOrd,
				PriorSupportStatus: it.PriorSupportStatus,
				GoldScanScope:      it.GoldScanScope, CitedElsewhere: c.CitedElsewhere,
				Evidence: it.Evidence, Source: it.Source, AsOfDate: it.AsOfDate,
			})
		}
	}
	return rows
}

// runCandidateSide draws rows, prints the two-group split, and refuses to look like a
// completed draw when a group is short.
func runCandidateSide(items []Item, receipt sampleReceipt, out, strategy, targets string,
	n int, allowShort, quiet bool, rng *rand.Rand, stdout, stderr *os.File) int {

	rows := candidateRowsOf(items)
	if len(rows) == 0 {
		fmt.Fprintln(stderr, "goldtool sample: this pool carries no candidate rows, so there is nothing to draw.\n"+
			"  E2's conflicting samples may only come from frozen candidates (doc/301 §9.1);\n"+
			"  an empty candidate side is a corpus fact, not a sampling choice.")
		return exitUsage
	}
	receipt.Available = len(rows)
	receipt.Stratum = "cited_elsewhere"

	var chosen []worksheetRow
	var strata, gap []stratumCount
	var problems []Problem
	if strategy == "stratified" {
		chosen, strata, gap, problems = drawCandidateStrata(rows, targets, rng)
	} else {
		perm := rng.Perm(len(rows))
		take := n
		if take > len(rows) {
			take = len(rows)
		}
		for _, idx := range perm[:take] {
			chosen = append(chosen, rows[idx])
		}
		if take < n {
			problems = append(problems, Problem{"pool_too_small",
				fmt.Sprintf("asked for %d candidate rows, the pool holds %d", n, len(rows))})
		}
		// Report what the draw actually contains, per group — an unreported group split
		// is what makes a "we covered both kinds of candidate" claim unfalsifiable.
		strata = countCandidateGroups(rows, chosen, true)
	}
	// The group split is reported over the *drawn* rows, with the pool's availability
	// beside it: §9.1 wants both numbers, and a group with 0 available is a corpus fact
	// that must stay visible instead of disappearing from the receipt.
	receipt.CandidateGroups = countCandidateGroups(rows, chosen, true)
	sort.SliceStable(chosen, func(i, j int) bool {
		if chosen[i].ItemID != chosen[j].ItemID {
			return chosen[i].ItemID < chosen[j].ItemID
		}
		return chosen[i].CandidateIndex < chosen[j].CandidateIndex
	})
	receipt.Strata, receipt.Shortfall, receipt.Problems = strata, gap, problems
	if strategy == "stratified" {
		// `requested` must describe this side's ask; the item-side default (120 claims)
		// would otherwise describe a draw that never happened.
		receipt.Requested = 0
		for _, row := range strata {
			receipt.Requested += row.Wanted
		}
	}
	receipt.Selected = len(chosen)

	code := exitOK
	if len(gap) > 0 || len(problems) > 0 {
		code = exitIncomplete
		if !allowShort {
			fmt.Fprintf(stderr, "goldtool sample: %d candidate group(s) cannot be filled "+
				"(actual n printed in the receipt); pass --allow-short to write the worksheet anyway (exit 3)\n", len(gap))
			if !quiet {
				_ = receipt.writeSampleReceipt(stderr)
			}
			return exitIncomplete
		}
	}
	if err := writeCandidateRows(out, chosen, receipt.SampleID); err != nil {
		fmt.Fprintf(stderr, "goldtool sample: %v\n", err)
		return exitUsage
	}
	if !quiet {
		_ = receipt.writeSampleReceipt(stdout)
	}
	return code
}

// countCandidateGroups reports the two §9.1 groups with *both* numbers: how many rows the
// pool holds (`available`) and how many this draw took (`selected`). Reporting only the
// drawn count would make an empty group look like a group that was never asked for, and
// reporting only the pool count would make a 12-of-36 draw look complete.
func countCandidateGroups(all, chosen []worksheetRow, withSelected bool) []stratumCount {
	key := func(r worksheetRow) string {
		if r.CitedElsewhere {
			return "cited_elsewhere=true"
		}
		return "cited_elsewhere=false"
	}
	avail, take := map[string]int{}, map[string]int{}
	for _, r := range all {
		avail[key(r)]++
	}
	for _, r := range chosen {
		take[key(r)]++
	}
	// Both keys are always emitted, even when a group is empty: a group that vanished from
	// the receipt would read as a group that was never defined (doc/301 §9.1 defines two).
	keys := []string{"cited_elsewhere=false", "cited_elsewhere=true"}
	out := make([]stratumCount, 0, len(keys))
	for _, k := range keys {
		row := stratumCount{Key: k, Available: avail[k]}
		if withSelected {
			row.Selected = take[k]
		}
		out = append(out, row)
	}
	return out
}

// drawCandidateStrata fills each group up to its target in target order and reports the
// gap. A group that is short is *reported*, never padded: repeating a row would inflate
// the denominator of E2's recall, and inventing a candidate row would break the one rule
// the whole gold file rests on ("候选只能来自真实冻结行").
func drawCandidateStrata(rows []worksheetRow, targets string, rng *rand.Rand) ([]worksheetRow, []stratumCount, []stratumCount, []Problem) {
	var problems []Problem
	wanted, order := parseTargets(targets, &problems)
	buckets := map[string][]worksheetRow{}
	for _, r := range rows {
		key := "cited_elsewhere=false"
		if r.CitedElsewhere {
			key = "cited_elsewhere=true"
		}
		buckets[key] = append(buckets[key], r)
	}
	var chosen []worksheetRow
	var strata, gap []stratumCount
	for _, name := range order {
		bucket := buckets[name]
		row := stratumCount{Key: name, Wanted: wanted[name], Available: len(bucket)}
		if row.Wanted > row.Available {
			row.Gap = row.Wanted - row.Available
			gap = append(gap, row)
		}
		take := row.Wanted
		if take > len(bucket) {
			take = len(bucket)
		}
		for _, idx := range rng.Perm(len(bucket))[:take] {
			chosen = append(chosen, bucket[idx])
		}
		row.Selected = take
		strata = append(strata, row)
	}
	for name := range buckets {
		if !containsStr(order, name) {
			strata = append(strata, stratumCount{Key: name, Available: len(buckets[name])})
		}
	}
	sort.Slice(strata, func(i, j int) bool { return strata[i].Key < strata[j].Key })
	return chosen, strata, gap, problems
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func writeCandidateRows(path string, rows []worksheetRow, sampleID string) error {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		r.SampleID = sampleID
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		lines = append(lines, string(raw))
	}
	return writeJSONL(path, lines)
}

// trimBounded keeps the worksheet from carrying more customer text than the gold file
// eventually will: the annotator needs enough to judge, and the row is a working file.
func trimBounded(s string, max int) string {
	if max <= 0 {
		max = maxExcerptRunes
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}

// firstNonEmpty is the pool's "either already redacted or still full text" choice.
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
