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
}

// manifest is what the freeze record points at: file hash, counts, κ, provenance and
// the exact command that produced it. Without the last field, "which pool produced
// this gold file" is a memory.
type manifest struct {
	Version     string         `json:"version"`
	File        string         `json:"file"`
	SHA256      string         `json:"sha256"`
	Bytes       int            `json:"bytes"`
	Items       int            `json:"items"`
	Verdicts    map[string]int `json:"verdicts"`
	QuoteStatus map[string]int `json:"quote_status"`
	Revisions   []string       `json:"revisions"`
	Templates   []string       `json:"templates"`
	Datasets    []string       `json:"datasets"`
	Annotators  []string       `json:"annotators"`
	Adjudicator string         `json:"adjudicator,omitempty"`
	Adjudicated int            `json:"adjudicated"`
	Agreement   Agreement      `json:"agreement"`
	PoolFile    string         `json:"pool_file"`
	FrozenAt    string         `json:"frozen_at"`
	Problems    []Problem      `json:"problems,omitempty"`
	Redaction   redactionNote  `json:"redaction"`
}

// redactionNote records the promise that made the file committable: customer text is
// a hash plus a bounded excerpt, and the full text is resolved from the database at
// replay time by snapshot id.
type redactionNote struct {
	MaxExcerptRunes int    `json:"max_excerpt_runes"`
	Rule            string `json:"rule"`
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

	ag, agErr := agreement(gold, append(append([]Label{}, la...), lb...))
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
		Agreement:   ag,
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
func assembleItems(pool []Item, a, b []Label, adj []Adjudication, requireAll bool) ([]Item, []Problem) {
	var problems []Problem
	byItem := map[string]Item{}
	for _, it := range pool {
		byItem[it.ItemID] = it
	}
	la := indexLabels(a, &problems, "labels-a", byItem)
	lb := indexLabels(b, &problems, "labels-b", byItem)
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

	out := make([]Item, 0, len(ids))
	for _, id := range ids {
		it := byItem[id]
		aLabel, aok := la[id]
		bLabel, bok := lb[id]
		if !aok || !bok {
			if requireAll {
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
			quoteStatus, quote, evIDs = mergeQuote(aLabel, bLabel)
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

		it.GoldVerdict = verdict
		it.GoldQuoteStatus = quoteStatus
		it.GoldQuote = quote
		if len(evIDs) == 0 {
			// Default the support set to "every cited snapshot", which is the only
			// defensible default: an annotator who means "only snapshot 2 supports this"
			// must say so explicitly, and the difference shows up as a diff later.
			evIDs = citedSnapshotIDs(it)
		}
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
func redactItem(it Item) Item {
	evs := make([]Evidence, 0, len(it.Evidence))
	for _, ev := range it.Evidence {
		excerpt, chars := redact(ev.Content, ev.ContentHash)
		ev.ContentExcerpt = excerpt
		ev.ContentChars = chars
		ev.Content = ""
		evs = append(evs, ev)
	}
	it.Evidence = evs
	cands := make([]Candidate, 0, len(it.CandidatePool))
	for _, c := range it.CandidatePool {
		excerpt, chars := redact(c.Content, c.ContentHash)
		c.ContentExcerpt = excerpt
		c.ContentChars = chars
		c.Content = ""
		cands = append(cands, c)
	}
	it.CandidatePool = cands
	return it
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
