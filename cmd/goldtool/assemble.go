package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Adjudication is the third person's decision on an item the two annotators did not
// agree on. `Reason` is required: an adjudicated label without a reason is a coin
// flip with a signature, and the annotator manual's next revision is exactly the
// list of these reasons.
type Adjudication struct {
	ItemID      string   `json:"item_id"`
	Verdict     string   `json:"verdict"`
	QuoteStatus string   `json:"quote_status"`
	Quote       string   `json:"quote,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Adjudicator string   `json:"adjudicator"`
	Reason      string   `json:"reason"`
	// CandidateChunkID is the candidate row the adjudicated verdict rests on, when the
	// disagreement was resolved by pointing at a frozen candidate. It wins over
	// whatever the two annotators named: the adjudicator is the decision.
	CandidateChunkID string `json:"candidate_chunk_id,omitempty"`
}

// manifest is what the freeze record points at: file hash, counts, κ, provenance and
// the exact command that produced it. Without the last field, "which pool produced
// this gold file" is a memory.
type manifest struct {
	Version     string            `json:"version"`
	File        string            `json:"file"`
	SHA256      string            `json:"sha256"`
	Bytes       int               `json:"bytes"`
	Items       int               `json:"items"`
	Verdicts    map[string]int    `json:"verdicts"`
	QuoteStatus map[string]int    `json:"quote_status"`
	Revisions   []string          `json:"revisions"`
	Templates   []string          `json:"templates"`
	Datasets    []string          `json:"datasets"`
	Annotators  []string          `json:"annotators"`
	Adjudicator string            `json:"adjudicator,omitempty"`
	Adjudicated int               `json:"adjudicated"`
	Agreement   manifestAgreement `json:"agreement"`
	PoolFile    string            `json:"pool_file"`
	FrozenAt    string            `json:"frozen_at"`
	Problems    []Problem         `json:"problems,omitempty"`
	Redaction   redactionNote     `json:"redaction"`
}

// manifestAgreement is the κ block of a gold manifest. It is deliberately *not* the
// `kappa` receipt type (Agreement): a manifest never sees the worksheet, so the four
// coverage fields of the receipt have nothing to put in them. Reusing that struct left
// them at zero — a manifest that read `ok: true`, `worksheet_items: 0` next to a
// 262-problem assembly, and `unfinished: false` on a file six items short.
//
// The fields here are the ones assembly can actually answer. `Population` is how many
// distinct items the two label files judge (that is the sample, as far as gold can know
// it), `Pairs` is how many of them survived into the file, and `Dropped` is the
// difference — the number a reader must see before trusting κ over a gold file.
// `OK` means "this assembly wrote a file with no problems" — `len(problems) == 0` — not
// "the sample passed": a clean assembly still says nothing about whether the judgments
// are right, and a refused one always reads `ok: false`.
type manifestAgreement struct {
	Pairs       int            `json:"pairs"`
	Observed    float64        `json:"observed_agreement"`
	Expected    float64        `json:"expected_agreement"`
	Kappa       float64        `json:"cohen_kappa"`
	Disagreed   int            `json:"disagreed"`
	ByClass     map[string]int `json:"disagreements_by_gold_class"`
	Annotators  []string       `json:"annotators"`
	Population  int            `json:"population_items"`
	Dropped     int            `json:"dropped_items"`
	MeetsTarget bool           `json:"meets_target"`
	OK          bool           `json:"ok"`
}

// redactionNote records the promise that made the file committable: customer text is
// a hash plus a bounded excerpt, and the full text is resolved from the database at
// replay time by snapshot id.
type redactionNote struct {
	MaxExcerptRunes int    `json:"max_excerpt_runes"`
	Rule            string `json:"rule"`
}

// manifestAgreementOf turns the recomputed agreement into the manifest block. It takes
// the raw label files because the only honest denominator available here is "how many
// items did the two files judge" — `ag` was computed over the gold file, which is the
// numerator. The difference between the two is `Dropped`, and a reader who does not see
// it will read "κ = 1.00 over 114 pairs" as a statement about the 120-item sample.
func manifestAgreementOf(ag Agreement, raw []Label, goldItems int, ok bool) manifestAgreement {
	population := map[string]bool{}
	for _, l := range raw {
		population[l.ItemID] = true
	}
	dropped := len(population) - goldItems
	if dropped < 0 {
		dropped = 0
	}
	m := manifestAgreement{
		Pairs: ag.Pairs, Observed: ag.Observed, Expected: ag.Expected, Kappa: ag.Kappa,
		Disagreed: ag.Disagreed, ByClass: ag.PerClass, Annotators: ag.Annotators,
		Population: len(population), Dropped: dropped,
		MeetsTarget: ag.Pairs > 0 && ag.Kappa >= minKappa,
		OK:          ok,
	}
	if m.ByClass == nil {
		m.ByClass = map[string]int{}
	}
	if m.Annotators == nil {
		m.Annotators = []string{}
	}
	return m
}

func runAssemble(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("assemble", flag.ContinueOnError)
	fs.SetOutput(stderr)
	poolPath := fs.String("pool", "", "pool JSONL produced by `goldtool pool`")
	labelA := fs.String("labels-a", "", "annotator A label file")
	labelB := fs.String("labels-b", "", "annotator B label file")
	adjPath := fs.String("adjudication", "", "optional adjudication file (required when A and B disagree)")
	version := fs.String("version", "v1", "gold version (v1, v2, …)")
	outDir := fs.String("out", "", "output directory")
	requireAll := fs.Bool("require-all-labels", true, "every item must be labelled by both annotators")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	for name, v := range map[string]string{"pool": *poolPath, "labels-a": *labelA, "labels-b": *labelB, "out": *outDir} {
		if strings.TrimSpace(v) == "" {
			fmt.Fprintf(stderr, "goldtool assemble: --%s is required\n", name)
			return exitUsage
		}
	}

	pool, err := readItems(*poolPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool assemble: pool: %v\n", err)
		return exitUsage
	}
	la, err := readLabels(*labelA)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool assemble: labels-a: %v\n", err)
		return exitUsage
	}
	lb, err := readLabels(*labelB)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool assemble: labels-b: %v\n", err)
		return exitUsage
	}
	var adj []Adjudication
	if strings.TrimSpace(*adjPath) != "" {
		adj, err = readAdjudications(*adjPath)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool assemble: adjudication: %v\n", err)
			return exitUsage
		}
	}

	gold, problems := assembleItems(pool, la, lb, adj, *requireAll)

	// Validate before writing: a gold file that fails its own invariants must never
	// reach the tree, or the next person reads "v1 exists" as "v1 is usable".
	problems = append(problems, validateItems(gold, true)...)

	allLabels := append(append([]Label{}, la...), lb...)
	ag, agErr := agreement(gold, allLabels)
	if agErr != nil {
		problems = append(problems, Problem{"agreement", agErr.Error()})
	} else if ag.Kappa < minKappa && len(gold) > 0 {
		problems = append(problems, Problem{"kappa_below_target",
			fmt.Sprintf("Cohen's κ = %.4f over %d pairs is below %.2f", ag.Kappa, ag.Pairs, minKappa)})
	}

	if len(gold) > 0 {
		sortItems(gold)
	}
	lines, err := encodeItems(gold)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool assemble: encode: %v\n", err)
		return exitInvariant
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "goldtool assemble: %v\n", err)
		return exitUsage
	}
	name := fmt.Sprintf("report-assurance-gold.%s.jsonl", *version)
	goldPath := filepath.Join(*outDir, name)
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}

	// The manifest is written even when there are problems: a rejected assembly is
	// evidence too ("we tried, κ was 0.61, we fixed the manual"). Only a clean one is
	// allowed to be the file the plan points at.
	man := manifest{
		Version: *version, File: name, SHA256: hashString(body), Bytes: len(body),
		Items:       len(gold),
		Verdicts:    countBy(gold, func(i Item) string { return i.GoldVerdict }),
		QuoteStatus: countBy(gold, func(i Item) string { return i.GoldQuoteStatus }),
		Revisions:   distinctSet(gold, func(i Item) string { return i.Source.RevisionID }),
		Templates:   distinctSet(gold, func(i Item) string { return i.Source.TemplateKey }),
		Datasets:    distinctDatasetIDs(gold),
		Annotators:  ag.Annotators,
		Adjudicated: countAdjudicated(gold),
		Agreement:   manifestAgreementOf(ag, allLabels, len(gold), len(problems) == 0),
		PoolFile:    filepath.Base(*poolPath),
		FrozenAt:    time.Now().UTC().Format(time.RFC3339),
		Problems:    problems,
		Redaction: redactionNote{MaxExcerptRunes: maxExcerptRunes,
			Rule: "content_hash + ≤200-rune excerpt; full text is resolved from rgx_report_evidence_snapshot at replay time (doc/301 §9.1)"},
	}
	if len(adj) > 0 {
		man.Adjudicator = adj[0].Adjudicator
	}

	if err := writeJSONL(goldPath, lines); err != nil {
		fmt.Fprintf(stderr, "goldtool assemble: write %s: %v\n", goldPath, err)
		return exitUsage
	}
	manLines, _ := json.MarshalIndent(man, "", "  ")
	manPath := filepath.Join(*outDir, fmt.Sprintf("report-assurance-gold.%s.manifest.json", *version))
	if err := os.WriteFile(manPath, append(manLines, '\n'), 0o644); err != nil {
		fmt.Fprintf(stderr, "goldtool assemble: write manifest: %v\n", err)
		return exitUsage
	}

	fmt.Fprintf(stdout, "assembled %d items → %s\n  sha256 %s\n  manifest %s\n  problems: %s\n",
		len(gold), goldPath, man.SHA256, manPath, summariseProblems(problems))
	if len(problems) > 0 {
		return exitInvariant
	}
	return exitOK
}

// assembleItems is the merge rule, in one place:
//
//   - both annotators agree              → that verdict is the gold verdict;
//   - they disagree and an adjudication   → the adjudication wins, with its reason
//     row exists                          recorded in `notes`;
//   - they disagree with no adjudication  → the item is *dropped* and a problem is
//     reported. Dropping (rather than defaulting) is what keeps the file honest:
//     defaulting to "unsupported" would inflate the class with the highest stakes.
//
// `requireAll` additionally fails items that one annotator never labelled, because an
// item labelled by one person is a sample of one, not a gold standard.
//
// The pool is not necessarily the sample. `pool` is the file the caller passed, and the
// two label files judge a subset of it whenever the sample was drawn from a bigger pool
// — the documented command passed the 371-row pool for a 120-row sample and got **251**
// `unlabelled_item` rows, which buried the six rows that were actually wrong. Items no
// annotator touched are therefore reported once, as a population statement: the
// population of gold is what the annotators judged, and a pool bigger than that is a
// wrong `--pool`, not 251 missing labels. An item one annotator did touch is still a
// named `unlabelled_item`, because that one *is* missing work.
func assembleItems(pool []Item, a, b []Label, adj []Adjudication, requireAll bool) ([]Item, []Problem) {
	var problems []Problem
	byItem := map[string]Item{}
	for _, it := range pool {
		byItem[it.ItemID] = it
	}
	la := indexLabels(a, &problems, "labels-a", byItem)
	lb := indexLabels(b, &problems, "labels-b", byItem)
	touched := map[string]bool{}
	for id := range la {
		touched[id] = true
	}
	for id := range lb {
		touched[id] = true
	}
	adjByItem := map[string]Adjudication{}
	for _, r := range adj {
		if _, dup := adjByItem[r.ItemID]; dup {
			problems = append(problems, Problem{"duplicate_adjudication", r.ItemID})
			continue
		}
		if !contains(GoldVerdicts, r.Verdict) {
			problems = append(problems, Problem{"bad_adjudication_verdict",
				fmt.Sprintf("%s: %q is outside the gold vocabulary", r.ItemID, r.Verdict)})
			continue
		}
		if strings.TrimSpace(r.Reason) == "" {
			problems = append(problems, Problem{"adjudication_without_reason", r.ItemID})
			continue
		}
		adjByItem[r.ItemID] = r
	}

	ids := make([]string, 0, len(byItem))
	for id := range byItem {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// One statement, not one per row: "the pool is bigger than the sample" and "20 items
	// are half-labelled" are different faults with different fixes, and a reader who sees
	// 251 lines of the first cannot find the second.
	outside := 0
	firstOutside := ""
	for _, id := range ids {
		if !touched[id] {
			outside++
			if firstOutside == "" {
				firstOutside = id
			}
		}
	}
	if outside > 0 {
		problems = append(problems, Problem{"pool_outside_sample",
			fmt.Sprintf("%d of %d pool row(s) appear in no label file (the files judge %d, e.g. %s is outside them): "+
				"the population of gold is what the annotators judged, so pass the worksheet sample as --pool, not "+
				"the pool it was drawn from", outside, len(ids), len(touched), firstOutside)})
	}

	out := make([]Item, 0, len(ids))
	for _, id := range ids {
		it := byItem[id]
		aLabel, aok := la[id]
		bLabel, bok := lb[id]
		if !aok || !bok {
			if requireAll && touched[id] {
				problems = append(problems, Problem{"unlabelled_item",
					fmt.Sprintf("%s: labelled by %d of 2 annotators", id, boolCount(aok, bok))})
			}
			continue
		}

		var verdict, quoteStatus, quote string
		var evIDs []string
		var notes []string
		if aLabel.Verdict == bLabel.Verdict {
			verdict = aLabel.Verdict
			// Question ① is a fact, not a preference. If the two annotators disagree about
			// whether the quoted text is in the evidence, one of them is wrong, and the gold
			// file must record which one was right. Falling back to A's answer (the old
			// behaviour) made that disagreement vanish at the exact place the manual says the
			// two questions must never be merged (doc/301 §9.1 "两步不许合并").
			sa, sb := labelQuoteStatus(aLabel), labelQuoteStatus(bLabel)
			if sa != sb {
				r, adjudicated := adjByItem[id]
				if !adjudicated || trimSpace(r.QuoteStatus) == "" {
					problems = append(problems, Problem{"quote_status_disagreement",
						fmt.Sprintf("%s: verdicts agree (%s) but question ① does not: A=%q B=%q — one of them is "+
							"wrong about whether the quote is in the evidence, so gold may not pick one; an "+
							"adjudication row must settle it (with quote_status)", id, aLabel.Verdict, sa, sb)})
					continue
				}
				quoteStatus, quote = trimSpace(r.QuoteStatus), r.Quote
				evIDs = r.EvidenceIDs
				notes = append(notes, fmt.Sprintf("quote_status adjudicated by %s: A=%s B=%s reason=%s",
					r.Adjudicator, sa, sb, r.Reason))
			} else {
				quoteStatus, quote, evIDs = mergeQuote(aLabel, bLabel)
			}
		} else {
			r, ok := adjByItem[id]
			if !ok {
				problems = append(problems, Problem{"unadjudicated_disagreement",
					fmt.Sprintf("%s: A=%s B=%s and no adjudication row", id, aLabel.Verdict, bLabel.Verdict)})
				continue
			}
			verdict, quoteStatus, quote, evIDs = r.Verdict, r.QuoteStatus, r.Quote, r.EvidenceIDs
			notes = append(notes, fmt.Sprintf("adjudicated by %s: A=%s B=%s reason=%s",
				r.Adjudicator, aLabel.Verdict, bLabel.Verdict, r.Reason))
		}

		if quoteStatus == "" {
			quoteStatus = quoteStatusFrom(quote)
		}
		candRef, refOK := mergeCandidateRef(it, aLabel, bLabel, adjByItem[id], aLabel.Verdict == bLabel.Verdict)
		if bad := candidateRefInItem(it, candRef); refOK && bad != "" {
			problems = append(problems, Problem{"candidate_ref_unknown",
				fmt.Sprintf("%s: %s", id, bad)})
			continue
		}
		if !refOK {
			problems = append(problems, Problem{"candidate_ref_disagreement",
				fmt.Sprintf("%s: A=%q B=%q — which candidate a verdict rests on must be adjudicated, not guessed",
					id, candRefOf(aLabel), candRefOf(bLabel))})
			continue
		}

		it.GoldVerdict = verdict
		it.GoldQuoteStatus = quoteStatus
		it.GoldQuote = quote
		if len(evIDs) == 0 {
			// Default the support set to "every cited snapshot", which is the only
			// defensible default: an annotator who means "only snapshot 2 supports this"
			// must say so explicitly, and the difference shows up as a diff later.
			evIDs = citedSnapshotIDs(it)
		}
		it.GoldCandidateChunkID = candRef
		it.GoldEvidenceIDs = evIDs
		it.Annotators = []string{aLabel.Annotator, bLabel.Annotator}
		if it.Annotators[0] > it.Annotators[1] {
			it.Annotators[0], it.Annotators[1] = it.Annotators[1], it.Annotators[0]
		}
		it.Notes = strings.Join(notes, "; ")
		it = redactItem(it)
		out = append(out, it)
	}
	return out, problems
}

// labelQuoteStatus reads a label's answer to question ①. When the field is absent it falls
// back to the only other thing the row says about it — whether a quote text is present — so
// that an older file is compared on what it meant rather than on whitespace.
func labelQuoteStatus(l Label) string {
	if s := trimSpace(l.QuoteStatus); s != "" {
		return s
	}
	return quoteStatusFrom(l.Quote)
}

func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

func indexLabels(ls []Label, problems *[]Problem, who string, pool map[string]Item) map[string]Label {
	out := map[string]Label{}
	for _, l := range ls {
		if _, inPool := pool[l.ItemID]; !inPool {
			*problems = append(*problems, Problem{"label_for_unknown_item",
				fmt.Sprintf("%s: %s is not in the pool", who, l.ItemID)})
			continue
		}
		if bad := checkLabelCandidateRef(pool[l.ItemID], l); bad != "" {
			*problems = append(*problems, Problem{"label_candidate_unknown",
				fmt.Sprintf("%s: %s: %s", who, l.ItemID, bad)})
			continue
		}
		if prev, dup := out[l.ItemID]; dup {
			*problems = append(*problems, Problem{"duplicate_label",
				fmt.Sprintf("%s: %s labelled twice (%s then %s)", who, l.ItemID, prev.Verdict, l.Verdict)})
			continue
		}
		out[l.ItemID] = l
	}
	return out
}

// mergeQuote takes the quote side of an agreed pair. When both annotators picked a
// quote, the longer one wins *only if it contains the shorter one*; otherwise the
// shorter (more conservative) quote is kept and the mismatch is not silently
// treated as agreement about text.
func mergeQuote(a, b Label) (status, quote string, ids []string) {
	status = a.QuoteStatus
	if status == "" {
		status = b.QuoteStatus
	}
	qa, qb := trimSpace(a.Quote), trimSpace(b.Quote)
	switch {
	case qa == "":
		quote = qb
	case qb == "":
		quote = qa
	case qa == qb:
		quote = qa
	case strings.Contains(qb, qa):
		quote = qb
	case strings.Contains(qa, qb):
		quote = qa
	default:
		quote = qa
	}
	if len(a.EvidenceIDs) > 0 {
		ids = a.EvidenceIDs
	} else {
		ids = b.EvidenceIDs
	}
	return status, quote, ids
}

func quoteStatusFrom(quote string) string {
	if trimSpace(quote) == "" {
		return "not_provided"
	}
	return "verified"
}

func citedSnapshotIDs(it Item) []string {
	out := make([]string, 0, len(it.Evidence))
	for _, ev := range it.Evidence {
		out = append(out, ev.SnapshotID)
	}
	sort.Strings(out)
	return out
}

// redactItem is the tree-safety step: full text out, hash + bounded excerpt in.
// redactItem turns a pool item (full text) into a gold item (hash + bounded excerpt).
//
// It is **idempotent on purpose**: an item that already carries only an excerpt keeps
// it. Without that, running assembly twice — a retry after a κ failure, say — would
// blank every excerpt (`redact("")` returns "") and the second file would pass the
// excerpt-length check while carrying no text at all: a gold set that validates and
// proves nothing.
func redactItem(it Item) Item {
	evs := make([]Evidence, 0, len(it.Evidence))
	for _, ev := range it.Evidence {
		ev.ContentExcerpt, ev.ContentChars = redactOnce(ev.Content, ev.ContentExcerpt, ev.ContentHash)
		ev.Content = ""
		evs = append(evs, ev)
	}
	it.Evidence = evs
	cands := make([]Candidate, 0, len(it.CandidatePool))
	for _, c := range it.CandidatePool {
		c.ContentExcerpt, c.ContentChars = redactOnce(c.Content, c.ContentExcerpt, c.ContentHash)
		c.Content = ""
		cands = append(cands, c)
	}
	it.CandidatePool = cands
	return it
}

// redactOnce is "redact, unless it is already redacted".
func redactOnce(content, excerpt, hash string) (string, int) {
	if strings.TrimSpace(content) == "" && excerpt != "" {
		return excerpt, utf8.RuneCountInString(excerpt)
	}
	return redact(content, hash)
}

func countAdjudicated(items []Item) int {
	n := 0
	for _, it := range items {
		if strings.Contains(it.Notes, "adjudicated by") {
			n++
		}
	}
	return n
}

func distinctDatasetIDs(items []Item) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		for _, id := range it.Source.DatasetIDs {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func readAdjudications(path string) ([]Adjudication, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var out []Adjudication
	for {
		var r Adjudication
		if err := dec.Decode(&r); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// candRefOf is a label's candidate reference, tolerating the redundant pair
// (`candidate_index` + `candidate_chunk_id`): index is the position in the item's
// candidate_pool, chunk id is what the gold file records. A label that names both must
// name the same row — checked in indexLabels, so here the chunk id is authoritative.
func candRefOf(l Label) string {
	return trimSpace(l.CandidateChunkID)
}

// mergeCandidateRef decides which candidate row a resolved verdict rests on.
//
// The rule is "the adjudicator decides; agreement needs no decision; a disagreement
// without adjudication is not resolved". The third case is why this returns ok=false
// instead of picking one: silently taking annotator A's candidate would make the gold
// file's E2 grouping (`cited_elsewhere=true/false`) depend on which file was read
// first, and that grouping is a reported number.
func mergeCandidateRef(it Item, a, b Label, adj Adjudication, agreed bool) (string, bool) {
	fromAdj := trimSpace(adj.CandidateChunkID)
	if !agreed {
		return fromAdj, true
	}
	ra, rb := candRefOf(a), candRefOf(b)
	switch {
	case ra == rb:
		return ra, true
	case ra == "":
		return rb, true
	case rb == "":
		return ra, true
	}
	// Same verdict, different candidate: the pair still needs one row recorded, because
	// E2 counts its results per `cited_elsewhere` group. An adjudication may resolve the
	// reference even when the two annotators already agreed on the verdict — that is a
	// smaller decision than the verdict, but it is still a decision.
	if fromAdj != "" {
		return fromAdj, true
	}
	return ra, false
}

// checkLabelCandidateRef refuses a label that points at a candidate row the item does
// not carry. Without this, an annotator could cite a candidate from a *different*
// revision (or one that a later rescan dropped) and the gold file would look like it
// had used the frozen row when it had not.
func checkLabelCandidateRef(it Item, l Label) string {
	ref := trimSpace(l.CandidateChunkID)
	if ref == "" {
		if l.CandidateIndex == nil {
			return ""
		}
		idx := *l.CandidateIndex
		if idx < 0 || idx >= len(it.CandidatePool) {
			return fmt.Sprintf("candidate_index %d is outside the %d candidate row(s) of this item",
				idx, len(it.CandidatePool))
		}
		return ""
	}
	idx, found := -1, false
	for i, c := range it.CandidatePool {
		if c.ChunkID == ref {
			idx, found = i, true
			break
		}
	}
	if !found {
		return fmt.Sprintf("candidate_chunk_id %q is not one of the %d candidate row(s) of this item",
			ref, len(it.CandidatePool))
	}
	if l.CandidateIndex != nil && *l.CandidateIndex != idx {
		return fmt.Sprintf("candidate_index %d points at a different row than candidate_chunk_id %q (index %d)",
			*l.CandidateIndex, ref, idx)
	}
	return ""
}

// candidateRefInItem is the same membership test checkLabelCandidateRef performs, for
// the reference that came out of the merge (which may have come from the adjudication).
func candidateRefInItem(it Item, ref string) string {
	if ref == "" {
		return ""
	}
	for _, c := range it.CandidatePool {
		if c.ChunkID == ref {
			return ""
		}
	}
	return fmt.Sprintf("candidate_chunk_id %q is not one of the %d candidate row(s) of this item",
		ref, len(it.CandidatePool))
}
