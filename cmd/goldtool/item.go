package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// GoldVerdicts is the closed set a *gold label* may take. It is narrower than the
// runtime verdict vocabulary on purpose (doc/301 §9.1): a gold set is built by
// sampling for the classes the plan needs denominators for. `insufficient_evidence`,
// `abstained`, `not_evaluated` and `not_required` describe the *evaluator*, not the
// text, so they can never be a human label for a claim that was selected to be
// decidable — if they show up, the sampler picked the wrong claim, not the
// annotator's problem to paper over.
var GoldVerdicts = []string{
	model.ReportVerdictSupported,
	model.ReportVerdictUnsupported,
	model.ReportVerdictConflicting,
	model.ReportVerdictOutdated,
	model.ReportVerdictPartiallySupported,
}

// GoldDistribution is the fixed sample plan of doc/301 §9.1, and the numbers are
// not round numbers: `unsupported 40` is what puts the Clopper–Pearson upper bound
// of "zero misses" at 8.81% (≤10%), `supported 36` puts "zero false blocks" at
// 9.74%, `conflicting 24` is E2's denominator (23/24 is the only count whose lower
// bound clears 78%). A validator that let the counts drift would silently retune
// the thresholds of an experiment that has already been pre-registered.
var GoldDistribution = map[string]int{
	model.ReportVerdictSupported:          36,
	model.ReportVerdictUnsupported:        40,
	model.ReportVerdictConflicting:        24,
	model.ReportVerdictOutdated:           12,
	model.ReportVerdictPartiallySupported: 8,
}

// GoldSize is the resulting file size.
func GoldSize() int {
	total := 0
	for _, n := range GoldDistribution {
		total += n
	}
	return total
}

// maxExcerptRunes bounds how much customer text may sit in a committed fixture
// (doc/301 §9.1 rule ②: hash + a short excerpt ≤200 字, everything else by hash).
const maxExcerptRunes = 200

// Evidence is one cited snapshot as the annotator sees it. In a *pool* file the
// full `content` is present (the file never leaves the workstation: it lives in
// tmp/ and is gitignored); in a *gold* file the text is replaced by
// `content_excerpt` + `content_chars`, because replay reads the real snapshot from
// the database by id. Both carry `content_hash`, so an excerpt can always be
// proved to belong to the text it claims to summarise.
type Evidence struct {
	SnapshotID     string `json:"snapshot_id"`
	ChunkID        string `json:"chunk_id"`
	DocumentID     string `json:"document_id"`
	DatasetID      string `json:"dataset_id"`
	ContentHash    string `json:"content_hash"`
	ContentChars   int    `json:"content_chars"`
	Content        string `json:"content,omitempty"`
	ContentExcerpt string `json:"content_excerpt,omitempty"`
	Truncated      bool   `json:"truncated"`
	CitationOrd    int    `json:"citation_ord"`
}

// Candidate is one frozen retrieval-pool row: text the search saw and the report
// did *not* cite. It is never evidence (doc/301 §5.3.1) — it exists so E2 can ask
// "would scanning the pool have found the contradiction".
type Candidate struct {
	ChunkID      string `json:"chunk_id"`
	ContentHash  string `json:"content_hash"`
	ContentChars int    `json:"content_chars"`
	// CitedElsewhere is true when this revision cited the chunk in a *different*
	// section. The dedup unit is the section (2026-11-21 amendment to doc/301 §9.1,
	// recorded as 304 D-08): the freeze in `report_generate.go` skips the citing
	// section's own citations, so exporting against the whole revision would delete
	// rows the writer really did see — on a 16-document corpus it deleted *all* of
	// them, and E2's `conflicting` class lost its only source.
	//
	// Keeping the row is the honest half; flagging it is the other half. An annotator
	// judging "did this section miss a contradiction" is entitled to know that the
	// same chunk was quoted three sections away: it changes the judgement from "the
	// writer never saw it" to "the writer saw it and used it elsewhere", which is a
	// different defect and may not be one at all.
	CitedElsewhere bool   `json:"cited_elsewhere"`
	Content        string `json:"content,omitempty"`
	ContentExcerpt string `json:"content_excerpt,omitempty"`
	PoolRank       int    `json:"pool_rank"`
	Truncated      bool   `json:"truncated"`
}

// Source is provenance. `revision_status` is stored rather than looked up so that
// a gold file can be audited offline years later, and so that a sampler that
// accidentally picks a draft revision leaves a trace instead of a mystery.
type Source struct {
	TenantID        string   `json:"tenant_id"`
	ReportID        string   `json:"report_id"`
	RevisionID      string   `json:"revision_id"`
	Revision        int64    `json:"revision"`
	RevisionStatus  string   `json:"revision_status"`
	TemplateKey     string   `json:"template_key"`
	TemplateVersion int64    `json:"template_version"`
	DatasetIDs      []string `json:"dataset_ids"`
	PublishedAt     string   `json:"published_at,omitempty"`
}

// Item is one claim, in exactly the field set doc/301 §9.1 lists. The pool file
// carries empty labels; `assemble` fills them and redacts the text.
type Item struct {
	ItemID string `json:"item_id"`
	// ClaimID is the runtime id (`rgx_report_claim.id`). It is what makes a replay
	// comparable: the same claim must be judged, not a paraphrase of it.
	ClaimID       string `json:"claim_id"`
	ClaimText     string `json:"claim_text"`
	ClaimType     string `json:"claim_type"`
	SectionPrompt string `json:"section_prompt"`
	SectionOrd    int    `json:"section_ord"`
	// PriorSupportStatus is the *runtime* opinion of the revision that produced
	// this claim. It is written into the pool as a stratification hint and is
	// explicitly NOT a label: sampling by it is fine, copying it into
	// `gold_verdict` would make the evaluation grade the thing it is measuring.
	PriorSupportStatus string      `json:"prior_support_status,omitempty"`
	Evidence           []Evidence  `json:"evidence"`
	CandidatePool      []Candidate `json:"candidate_pool"`
	Source             Source      `json:"source"`
	AsOfDate           string      `json:"as_of_date"`
	GoldVerdict        string      `json:"gold_verdict"`
	GoldQuote          string      `json:"gold_quote"`
	GoldQuoteStatus    string      `json:"gold_quote_status"`
	GoldEvidenceIDs    []string    `json:"gold_evidence_ids"`
	Annotators         []string    `json:"annotators"`
	Notes              string      `json:"notes"`
}

// encodeItem renders one JSONL line. Field order is the struct order, which keeps
// diffs of two gold versions readable instead of alphabetised noise.
func encodeItem(it Item) (string, error) {
	raw, err := json.Marshal(it)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func encodeItems(items []Item) ([]string, error) {
	out := make([]string, 0, len(items))
	for _, it := range items {
		line, err := encodeItem(it)
		if err != nil {
			return nil, err
		}
		out = append(out, line)
	}
	return out, nil
}

func decodeItem(line string) (Item, error) {
	var it Item
	if err := json.Unmarshal([]byte(line), &it); err != nil {
		return Item{}, err
	}
	return it, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// checkItem is the single-item half of validate. It returns violations, never a
// bool: "invalid" without the invariant that failed is what makes a validator easy
// to route around (doc/301 §9.4 — the check must be able to fail *and* say why).
func checkItem(it Item, idx int) []string {
	var v []string
	add := func(format string, args ...any) {
		v = append(v, fmt.Sprintf("line %d item %q: %s", idx+1, it.ItemID, fmt.Sprintf(format, args...)))
	}

	if trimSpace(it.ItemID) == "" || trimSpace(it.ClaimID) == "" {
		add("item_id and claim_id are required (a label without an identity cannot be replayed)")
	}
	if trimSpace(it.ClaimText) == "" {
		add("claim_text is empty")
	}
	if trimSpace(it.SectionPrompt) == "" {
		add("section_prompt is empty (the judging prompt cannot be reconstructed without it)")
	}
	if trimSpace(it.AsOfDate) == "" {
		add("as_of_date is empty")
	}
	if _, err := parseDay(it.AsOfDate); err != nil {
		add("as_of_date %q is not YYYY-MM-DD: %v", it.AsOfDate, err)
	}
	if trimSpace(it.Source.RevisionID) == "" {
		add("source.revision_id is empty (the item must come from a real revision)")
	}
	if trimSpace(it.Source.ReportID) == "" {
		add("source.report_id is empty")
	}
	if n := len(it.Source.DatasetIDs); n == 0 {
		add("source.dataset_ids is empty")
	}

	// Verdict vocabulary. Empty is only legal in a *pool* file, which validate is
	// never asked to bless (assemble always fills it) — so treat empty as a failure
	// here and let pool files skip validate.
	if it.GoldVerdict == "" {
		add("gold_verdict is empty")
	} else if !contains(GoldVerdicts, it.GoldVerdict) {
		add("gold_verdict %q is not one of %v (the evaluator states insufficient/abstained/not_required are not labels)",
			it.GoldVerdict, GoldVerdicts)
	}
	if it.GoldQuoteStatus == "" {
		add("gold_quote_status is empty")
	} else if !contains(model.ReportQuoteStatuses(), it.GoldQuoteStatus) {
		add("gold_quote_status %q is not one of %v", it.GoldQuoteStatus, model.ReportQuoteStatuses())
	}
	if it.GoldQuoteStatus != model.ReportQuoteStatusNotProvided && trimSpace(it.GoldQuote) == "" {
		add("gold_quote_status is %q but gold_quote is empty (a quote claim without a quote is unfalsifiable)",
			it.GoldQuoteStatus)
	}
	if it.GoldQuoteStatus == model.ReportQuoteStatusNotProvided && trimSpace(it.GoldQuote) != "" {
		add("gold_quote_status is not_provided but gold_quote is non-empty")
	}
	if len(it.Annotators) < 2 {
		add("annotators has %d entries; the plan requires two independent annotators plus adjudication", len(it.Annotators))
	} else if it.Annotators[0] == it.Annotators[1] {
		add("annotators lists %q twice — two labels from one person are not two labels", it.Annotators[0])
	}

	// Evidence side.
	if len(it.Evidence) == 0 {
		add("evidence is empty (a claim with no cited text cannot be judged supported or unsupported)")
	}
	citedSnapshot := map[string]bool{}
	citedChunk := map[string]bool{}
	for i, ev := range it.Evidence {
		if trimSpace(ev.SnapshotID) == "" {
			add("evidence[%d].snapshot_id is empty", i)
		}
		if trimSpace(ev.ChunkID) == "" {
			add("evidence[%d].chunk_id is empty", i)
		}
		if trimSpace(ev.ContentHash) == "" {
			add("evidence[%d].content_hash is empty (redacted text must still be provable)", i)
		}
		if utf8.RuneCountInString(ev.ContentExcerpt) > maxExcerptRunes {
			add("evidence[%d].content_excerpt is %d runes, limit %d", i,
				utf8.RuneCountInString(ev.ContentExcerpt), maxExcerptRunes)
		}
		if ev.Content != "" && ev.ContentExcerpt != "" {
			add("evidence[%d] carries both full content and an excerpt; a gold file must be redacted, a pool must not be",
				i)
		}
		if ev.Content == "" && ev.ContentExcerpt == "" {
			add("evidence[%d] carries neither content nor excerpt", i)
		}
		citedSnapshot[ev.SnapshotID] = true
		citedChunk[ev.ChunkID+":"+ev.ContentHash] = true
	}

	// Candidate side, including the rule that makes candidates mean something.
	for i, c := range it.CandidatePool {
		if trimSpace(c.ChunkID) == "" || trimSpace(c.ContentHash) == "" {
			add("candidate_pool[%d] is missing chunk_id or content_hash", i)
		}
		if utf8.RuneCountInString(c.ContentExcerpt) > maxExcerptRunes {
			add("candidate_pool[%d].content_excerpt is %d runes, limit %d", i,
				utf8.RuneCountInString(c.ContentExcerpt), maxExcerptRunes)
		}
		if c.Content != "" && c.ContentExcerpt != "" {
			add("candidate_pool[%d] carries both full content and an excerpt; a gold file must be redacted, a pool must not be",
				i)
		}
		if c.Content == "" && c.ContentExcerpt == "" {
			add("candidate_pool[%d] carries neither content nor excerpt", i)
		}
		// doc/301 §9.1 (2026-11-21 amendment, 304 D-08): the dedup unit is the
		// **section**, so a candidate may be cited elsewhere in the revision — that is
		// what `cited_elsewhere` records. What it may never do is repeat a chunk this
		// item itself cites: then "in the pool and not cited" would be false in the one
		// scope the annotator is actually judging.
		if citedChunk[c.ChunkID+":"+c.ContentHash] {
			add("candidate_pool[%d] repeats a chunk that this item cites (%s) — a candidate is by definition uncited in this item's section",
				i, c.ChunkID)
		}
	}

	// gold_evidence_ids must name cited snapshots; anything else would let an
	// annotator "support" a claim with a chunk that the revision never used.
	for _, id := range it.GoldEvidenceIDs {
		if !citedSnapshot[id] {
			add("gold_evidence_ids names %q, which is not one of the item's cited snapshots", id)
		}
	}
	if it.GoldVerdict == model.ReportVerdictSupported && len(it.GoldEvidenceIDs) == 0 {
		add("gold_verdict is supported but gold_evidence_ids is empty (no source, no certification)")
	}

	return v
}

func parseDay(s string) (string, error) {
	// Deliberately strict: a date-only contract, because `as_of_date` is compared
	// against evidence ages elsewhere and a timestamp invites timezone arguments.
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02"), nil
}

// sortItems gives the gold file a deterministic order. Without it, two assemblies
// of the same labels differ by line order, the file hash differs, and the freeze
// record proves nothing.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Source.RevisionID != b.Source.RevisionID {
			return a.Source.RevisionID < b.Source.RevisionID
		}
		if a.SectionOrd != b.SectionOrd {
			return a.SectionOrd < b.SectionOrd
		}
		return a.ClaimID < b.ClaimID
	})
}

// countBy tallies one string field over items.
func countBy(items []Item, pick func(Item) string) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		out[pick(it)]++
	}
	return out
}

// distinctSet collects distinct values of a field, sorted, for coverage checks
// whose failure message should name what was *missing* rather than a count.
func distinctSet(items []Item, pick func(Item) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		v := pick(it)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// redact turns full text into the committed form: hash stays, text becomes a
// rune-safe prefix. `content_chars` is recorded so a reader can tell "the excerpt
// is the whole chunk" from "the excerpt is a window".
func redact(text, hash string) (excerpt string, chars int) {
	chars = utf8.RuneCountInString(text)
	if chars <= maxExcerptRunes {
		return text, chars
	}
	runes := []rune(text)
	return string(runes[:maxExcerptRunes]), chars
}

func joinIDs(ids []string) string { return strings.Join(ids, ",") }
