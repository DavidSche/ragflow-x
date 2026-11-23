package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// sampleReceipt is what `goldtool sample` records about a draw. It exists because a
// sample is only usable as evidence if it can be *re-drawn*: the seed, the pool's
// sha256 and the strategy are the whole reproducibility argument (doc/301 §4.4 D5).
type sampleReceipt struct {
	CreatedAt  string `json:"created_at"`
	Pool       string `json:"pool"`
	PoolSHA256 string `json:"pool_sha256"`
	Out        string `json:"out"`
	// Role separates the two tracks of doc/301 §9.1's pre-registration: `primary` is
	// the equal-probability random sample the acceptance metrics are computed on;
	// `supplementary` is a stratified draw that exists to *diagnose* rare classes and
	// must never be merged into a primary denominator (over-sampling a class moves what
	// the CP bound means). The two tracks are independently numbered and independently
	// reported, so a supplementary file cannot be mistaken for the primary one later.
	Role     string `json:"role"`
	SampleID string `json:"sample_id"`
	// Side is the *unit* of the draw: `items` (one claim per row, the gold set of
	// doc/301 §9.1) or `candidates` (one frozen candidate row per row, the only
	// admissible source for E2's 24 conflicting samples). The two sides have
	// different available counts and different stratified keys, so a receipt that did
	// not name its side would make two incompatible draws look comparable.
	Side      string `json:"side"`
	Strategy  string `json:"strategy"`
	Stratum   string `json:"stratum,omitempty"`
	Seed      int64  `json:"seed"`
	Requested int    `json:"requested"`
	Available int    `json:"available"`
	Selected  int    `json:"selected"`
	// CandidateGroups counts the drawn candidate rows by `cited_elsewhere`, because
	// doc/301 §9.1 requires the two groups to be *reported separately*: a candidate the
	// revision cited in another section is a different judgment ("the writer saw it and
	// used it elsewhere") from one it never cited. An unreported group split would turn
	// that distinction into a footnote discovered during analysis.
	CandidateGroups []stratumCount `json:"candidate_groups,omitempty"`
	Strata          []stratumCount `json:"strata"`
	Shortfall       []stratumCount `json:"shortfall,omitempty"`
	Problems        []Problem      `json:"problems,omitempty"`
	// Blind, when `--blind-out` was used, is the annotation packet: the same rows with
	// no prior_*/gold_* key. It sits beside `out` rather than replacing it because the
	// worksheet is the sampling record and the packet is the reading material, and a
	// receipt naming only one of them would make the other unaccounted for.
	Blind *blindBlock `json:"blind,omitempty"`
}

type stratumCount struct {
	Key       string `json:"key"`
	Wanted    int    `json:"wanted"`
	Available int    `json:"available"`
	Selected  int    `json:"selected"`
	Gap       int    `json:"gap"`
}

// runSample draws the annotation worksheet out of a pool export.
//
// Two things it deliberately does not do:
//
//   - it does not invent the sampling 口径. `--strategy stratified` requires
//     `--targets`, because choosing how many `unsupported`-prior items to over-sample
//     is a plan decision that changes what the CP bound means (304 §7 item 10). A
//     default here would look like a tool decision and be read as one;
//   - it does not write verdicts. The worksheet is the pool subset that annotators
//     label; `assemble` is the only thing that fills `gold_verdict`, and only from two
//     independent labels plus adjudication.
func runSample(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("sample", flag.ContinueOnError)
	fs.SetOutput(stderr)
	poolPath := fs.String("pool", "", "pool JSONL produced by `goldtool pool`")
	out := fs.String("out", "", "output worksheet JSONL path")
	n := fs.Int("n", GoldSize(), "how many items to draw (doc/301 §9.1 fixes 120)")
	strategy := fs.String("strategy", "random", "random | stratified")
	stratum := fs.String("stratum", "prior_support_status", "stratification key: prior_support_status | claim_type | template_key")
	targets := fs.String("targets", "", `stratum targets for --strategy stratified, e.g. "supported=80,unsupported=40"`)
	seed := fs.Int64("seed", 0, "draw seed (0 = derive from the clock and print it)")
	allowShort := fs.Bool("allow-short", false, "write the worksheet even when a stratum cannot be filled (exit 3)")
	role := fs.String("role", "", "primary | supplementary (default: random→primary, stratified→supplementary)")
	side := fs.String("side", "items", "items (one claim per row) | candidates (one frozen candidate row per row)")
	blindOut := fs.String("blind-out", "", "also write an annotation packet: the same rows with no prior_*/gold_* key (doc/301 §9.1 rule 3)")
	quiet := fs.Bool("quiet", false, "do not print the JSON receipt")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*poolPath) == "" || strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "goldtool sample: --pool and --out are required")
		return exitUsage
	}
	if err := blindTargetOK(*out, *blindOut); err != nil {
		fmt.Fprintf(stderr, "goldtool sample: %v\n", err)
		return exitUsage
	}
	if *side != "items" && *side != "candidates" {
		fmt.Fprintf(stderr, "goldtool sample: unknown side %q (items | candidates)\n", *side)
		return exitUsage
	}
	if *strategy != "random" && *strategy != "stratified" {
		fmt.Fprintf(stderr, "goldtool sample: unknown strategy %q (random | stratified)\n", *strategy)
		return exitUsage
	}
	if *side == "candidates" && *strategy == "stratified" && strings.TrimSpace(*targets) != "" {
		if bad := checkCandidateTargets(*targets); bad != "" {
			fmt.Fprintf(stderr, "goldtool sample: %s\n"+
				"  With --side candidates the only stratified key is cited_elsewhere\n"+
				"  (doc/301 §9.1: the two groups are counted and reported separately).\n", bad)
			return exitUsage
		}
	}
	if *strategy == "stratified" && strings.TrimSpace(*targets) == "" {
		fmt.Fprintln(stderr, "goldtool sample: --strategy stratified needs --targets.\n"+
			"  The targets are how much each class is over-sampled, and over-sampling a class\n"+
			"  moves the CP bound. That is a pre-registration decision (doc/304 §7 item 10),\n"+
			"  not a tool default.")
		return exitUsage
	}

	// The pre-registration is not a preference: a primary sample has to be
	// equal-probability random, because only then does the class mix in the file
	// estimate the class mix of the corpus and the CP bound mean what it says.
	// A stratified draw over-samples by construction, so it may only be supplementary
	// (doc/301 §9.1, doc/304 §7 item 10).
	want := "primary"
	if *strategy == "stratified" {
		want = "supplementary"
	}
	if strings.TrimSpace(*role) == "" {
		*role = want
	}
	if *role != "primary" && *role != "supplementary" {
		fmt.Fprintf(stderr, "goldtool sample: unknown role %q (primary | supplementary)\n", *role)
		return exitUsage
	}
	if *strategy == "stratified" && *role == "primary" {
		fmt.Fprintln(stderr, "goldtool sample: --strategy stratified cannot be --role primary.\n"+
			"  A stratified draw over-samples some classes by construction, so its class mix\n"+
			"  no longer estimates the corpus and a plain Clopper-Pearson bound on it would\n"+
			"  be systematically optimistic. Primary samples are equal-probability random;\n"+
			"  stratified draws are supplementary and are reported separately\n"+
			"  (doc/301 §9.1 pre-registration, doc/304 §7 item 10).")
		return exitUsage
	}
	if *strategy == "random" && *role == "supplementary" && strings.TrimSpace(*targets) != "" {
		fmt.Fprintln(stderr, "goldtool sample: --targets only applies to --strategy stratified")
		return exitUsage
	}

	items, err := readItems(*poolPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool sample: %v\n", err)
		return exitUsage
	}
	if len(items) == 0 {
		fmt.Fprintln(stderr, "goldtool sample: the pool is empty")
		return exitUsage
	}
	raw, err := os.ReadFile(*poolPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool sample: %v\n", err)
		return exitUsage
	}
	sum := sha256.Sum256(raw)

	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(*seed))

	// Determinism starts with a stable input order: a pool file that was appended to in
	// a different order must not change what the same seed draws.
	sorted := append([]Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ItemID < sorted[j].ItemID })

	receipt := sampleReceipt{
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Pool: filepath.Base(*poolPath),
		PoolSHA256: hex.EncodeToString(sum[:]), Out: filepath.Base(*out),
		Role: *role, Strategy: *strategy, Seed: *seed, Requested: *n, Side: *side,
	}
	receipt.SampleID = fmt.Sprintf("%s-%s-%d", receipt.Role, receipt.Strategy, receipt.Seed)
	if *side == "candidates" {
		// A distinct id, because the same seed over the same pool draws a completely
		// different set of rows on this side: two files sharing an id would look like
		// two draws of one sample.
		receipt.SampleID = fmt.Sprintf("%s-%s-candidates-%d", receipt.Role, receipt.Strategy, receipt.Seed)
		return runCandidateSide(sorted, receipt, *out, *blindOut, *strategy, *targets, *n, *allowShort, *quiet, rng, stdout, stderr)
	}
	receipt.Available = len(sorted)
	if *strategy == "stratified" {
		receipt.Stratum = *stratum
		selected, strata, gap, problems := drawStratified(sorted, *stratum, *targets, rng)
		receipt.Strata, receipt.Shortfall, receipt.Problems = strata, gap, problems
		receipt.Selected = len(selected)
		code := exitOK
		if len(gap) > 0 || len(problems) > 0 {
			code = exitIncomplete
			if !*allowShort {
				// A short stratum is not a bug in the tool; writing a worksheet that
				// quietly under-fills one class would let the shortfall become an
				// invisible property of the gold file instead of a printed fact.
				fmt.Fprintf(stderr, "goldtool sample: %d class(es) cannot be filled; "+
					"pass --allow-short to write the worksheet anyway (exit 3)\n", len(gap))
				if !*quiet {
					_ = receipt.writeSampleReceipt(stderr)
				}
				return exitIncomplete
			}
		}
		if err := writeSample(*out, selected); err != nil {
			fmt.Fprintf(stderr, "goldtool sample: %v\n", err)
			return exitUsage
		}
		if code, ok := writeBlindIfAsked(&receipt, *blindOut, itemRows(selected), stderr); !ok {
			return code
		}
		if !*quiet {
			_ = receipt.writeSampleReceipt(stdout)
		}
		return code
	}

	// random: a uniform draw. It is the unbiased option of 304 §7 item 10 — the CP
	// bound is computed on whatever class denominators come out, and those denominators
	// are printed rather than engineered.
	perm := rng.Perm(len(sorted))
	take := *n
	if take > len(sorted) {
		take = len(sorted)
	}
	chosen := make([]Item, 0, take)
	for _, idx := range perm[:take] {
		chosen = append(chosen, sorted[idx])
	}
	sort.SliceStable(chosen, func(i, j int) bool { return chosen[i].ItemID < chosen[j].ItemID })
	receipt.Selected = len(chosen)
	receipt.Strata = countStrata(chosen, "prior_support_status")
	if err := writeSample(*out, chosen); err != nil {
		fmt.Fprintf(stderr, "goldtool sample: %v\n", err)
		return exitUsage
	}
	if code, ok := writeBlindIfAsked(&receipt, *blindOut, itemRows(chosen), stderr); !ok {
		return code
	}
	code := exitOK
	if len(chosen) < *n {
		receipt.Problems = append(receipt.Problems, Problem{"pool_too_small",
			fmt.Sprintf("asked for %d items, the pool holds %d", *n, len(sorted))})
		code = exitIncomplete
	}
	if !*quiet {
		_ = receipt.writeSampleReceipt(stdout)
	}
	return code
}

// drawStratified fills each requested stratum up to its target, in target order, and
// reports the gap per stratum. Order is part of the contract: the same seed and the
// same targets must draw the same items, so strata are filled in a fixed order.
func drawStratified(items []Item, key, targets string, rng *rand.Rand) ([]Item, []stratumCount, []stratumCount, []Problem) {
	var problems []Problem
	wanted, order := parseTargets(targets, &problems)
	buckets := map[string][]Item{}
	for _, it := range items {
		buckets[stratumOf(it, key)] = append(buckets[stratumOf(it, key)], it)
	}
	var selected []Item
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
		perm := rng.Perm(len(bucket))
		for _, idx := range perm[:take] {
			selected = append(selected, bucket[idx])
		}
		row.Selected = take
		strata = append(strata, row)
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].ItemID < selected[j].ItemID })
	return selected, strata, gap, problems
}

func parseTargets(spec string, problems *[]Problem) (map[string]int, []string) {
	wanted := map[string]int{}
	var order []string
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Split at the *last* "=": a candidate-side key is itself an equality
		// (`cited_elsewhere=true=12`), so splitting on the first one would read the
		// key as `cited_elsewhere` and the count as `true=12`.
		cut := strings.LastIndex(part, "=")
		kv := []string{part, ""}
		if cut >= 0 {
			kv = []string{part[:cut], part[cut+1:]}
		}
		if cut < 0 {
			*problems = append(*problems, Problem{"bad_target",
				fmt.Sprintf("%q is not key=count", part)})
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(kv[1]), "%d", &n); err != nil || n < 0 {
			*problems = append(*problems, Problem{"bad_target",
				fmt.Sprintf("%q has a non-numeric count", part)})
			continue
		}
		name := strings.TrimSpace(kv[0])
		if _, dup := wanted[name]; dup {
			*problems = append(*problems, Problem{"duplicate_target",
				fmt.Sprintf("%q appears twice; a doubled stratum doubles a denominator", name)})
			continue
		}
		wanted[name] = n
		order = append(order, name)
	}
	return wanted, order
}

// stratumOf is the only place the stratum vocabulary lives. An unknown key is a
// usage error elsewhere; here it degrades to a single bucket rather than silently
// stratifying by "" (which would look like one perfectly filled class).
func stratumOf(it Item, key string) string {
	switch key {
	case "claim_type":
		return it.ClaimType
	case "template_key":
		return it.Source.TemplateKey
	case "prior_support_status":
		return it.PriorSupportStatus
	default:
		return it.PriorSupportStatus
	}
}

// countStrata reuses item.go's countBy (one counting function, not two) and turns it
// into receipt rows.
func countStrata(items []Item, key string) []stratumCount {
	counts := countBy(items, func(it Item) string { return stratumOf(it, key) })
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]stratumCount, 0, len(keys))
	for _, k := range keys {
		out = append(out, stratumCount{Key: k, Selected: counts[k], Available: counts[k]})
	}
	return out
}

func writeSample(path string, items []Item) error {
	lines := make([]string, 0, len(items))
	for _, it := range items {
		// One physical line per item, like every other file this tool writes: an
		// indented item would be a valid-looking file whose every second line is
		// invalid JSONL, and the reader would blame line 1 for a formatting choice.
		raw, err := json.Marshal(it)
		if err != nil {
			return err
		}
		lines = append(lines, string(raw))
	}
	return writeJSONL(path, lines)
}

func (r sampleReceipt) writeSampleReceipt(w *os.File) error {
	raw, err := marshalIndent(r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}
