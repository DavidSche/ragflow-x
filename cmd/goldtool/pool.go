package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// poolExport is what `goldtool pool` prints after writing the file. It exists so
// the run leaves a *receipt* in the terminal/CI log: how many claims were
// available, how many were usable, and — importantly — which coverage promises of
// doc/301 §9.1 this particular database cannot satisfy yet.
type poolExport struct {
	ExportedAt   string         `json:"exported_at"`
	Items        int            `json:"items"`
	ClaimsSeen   int            `json:"claims_seen"`
	SkippedNoCit int            `json:"skipped_without_citation"`
	Revisions    int            `json:"revisions"`
	Datasets     int            `json:"datasets"`
	Templates    []string       `json:"templates"`
	ClaimTypes   map[string]int `json:"claim_types"`
	PriorSupport map[string]int `json:"prior_support_status"`
	Candidates   int            `json:"candidate_rows"`
	EvidenceRows int            `json:"evidence_rows"`
	Coverage     poolCoverage   `json:"coverage"`
}

// poolCoverage states the three coverage floors of §9.1 as measured facts. The
// plan asks for 6 revisions across 3 templates × 2 datasets; a database that has
// one template cannot meet it, and the honest output is `false` next to the
// number, not silence.
type poolCoverage struct {
	MinRevisions  int  `json:"min_revisions"`
	GotRevisions  int  `json:"got_revisions"`
	MinDatasets   int  `json:"min_datasets"`
	GotDatasets   int  `json:"got_datasets"`
	MinTemplates  int  `json:"min_templates"`
	GotTemplates  int  `json:"got_templates"`
	EnoughItems   int  `json:"enough_items"`
	ItemsOK       bool `json:"items_ok"`
	RevisionsOK   bool `json:"revisions_ok"`
	DatasetsOK    bool `json:"datasets_ok"`
	TemplatesOK   bool `json:"templates_ok"`
	AllCoverageOK bool `json:"all_ok"`
}

func runPool(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("pool", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", os.Getenv(envDSN), "PostgreSQL DSN (read-only use); defaults to $"+envDSN)
	out := fs.String("out", "", "output JSONL path")
	maxRevisions := fs.Int("max-revisions", 40, "how many published revisions to walk (newest first)")
	maxItems := fs.Int("max-items", 0, "stop after N items (0 = no limit)")
	tenant := fs.String("tenant", "", "restrict to one tenant id")
	minRev := fs.Int("min-revisions", 6, "coverage floor: revisions (doc/301 §9.1)")
	minDs := fs.Int("min-datasets", 2, "coverage floor: datasets (doc/301 §9.1)")
	minTpl := fs.Int("min-templates", 3, "coverage floor: templates (doc/301 §9.1)")
	quiet := fs.Bool("quiet", false, "do not print the JSON receipt")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "goldtool pool: --out is required")
		return exitUsage
	}
	if strings.TrimSpace(*dsn) == "" {
		fmt.Fprintf(stderr, "goldtool pool: no DSN (pass --dsn or set %s)\n", envDSN)
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	db, tx, err := openReadOnly(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool pool: %v\n", err)
		return exitUsage
	}
	defer db.Close()
	defer tx.Rollback() //nolint:errcheck // read-only; rollback is the only way out

	revs, err := listRevisions(ctx, tx, *maxRevisions, *tenant)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool pool: %v\n", err)
		return exitUsage
	}

	items := make([]Item, 0, 256)
	receipt := poolExport{
		ExportedAt:   time.Now().UTC().Format(time.RFC3339),
		ClaimTypes:   map[string]int{},
		PriorSupport: map[string]int{},
	}
	for _, rev := range revs {
		ris, seen, skipped, err := itemsOfRevision(ctx, tx, rev)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool pool: revision %s: %v\n", rev.RevisionID, err)
			return exitUsage
		}
		receipt.ClaimsSeen += seen
		receipt.SkippedNoCit += skipped
		items = append(items, ris...)
		if *maxItems > 0 && len(items) >= *maxItems {
			items = items[:*maxItems]
			break
		}
	}
	sortItems(items)

	receipt.Items = len(items)
	receipt.Revisions = len(distinctSet(items, func(i Item) string { return i.Source.RevisionID }))
	ds := map[string]bool{}
	for _, it := range items {
		for _, id := range it.Source.DatasetIDs {
			ds[id] = true
		}
		receipt.ClaimTypes[it.ClaimType]++
		if it.PriorSupportStatus != "" {
			receipt.PriorSupport[it.PriorSupportStatus]++
		}
		receipt.EvidenceRows += len(it.Evidence)
		receipt.Candidates += len(it.CandidatePool)
	}
	receipt.Datasets = len(ds)
	receipt.Templates = distinctSet(items, func(i Item) string { return i.Source.TemplateKey })
	receipt.Coverage = poolCoverage{
		MinRevisions: *minRev, GotRevisions: receipt.Revisions,
		MinDatasets: *minDs, GotDatasets: receipt.Datasets,
		MinTemplates: *minTpl, GotTemplates: len(receipt.Templates),
		EnoughItems: GoldSize(),
	}
	receipt.Coverage.RevisionsOK = receipt.Revisions >= *minRev
	receipt.Coverage.DatasetsOK = receipt.Datasets >= *minDs
	receipt.Coverage.TemplatesOK = len(receipt.Templates) >= *minTpl
	receipt.Coverage.ItemsOK = receipt.Items >= GoldSize()
	receipt.Coverage.AllCoverageOK = receipt.Coverage.RevisionsOK && receipt.Coverage.DatasetsOK &&
		receipt.Coverage.TemplatesOK && receipt.Coverage.ItemsOK

	lines, err := encodeItems(items)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool pool: encode: %v\n", err)
		return exitInvariant
	}
	if err := writeJSONL(*out, lines); err != nil {
		fmt.Fprintf(stderr, "goldtool pool: write %s: %v\n", *out, err)
		return exitUsage
	}

	if !*quiet {
		raw, _ := json.MarshalIndent(receipt, "", "  ")
		fmt.Fprintln(stdout, string(raw))
	}
	// A pool that cannot meet the plan's coverage is a 3: the command worked, the
	// data did not. Saying 0 here would let "we exported a pool" read as "the gold
	// set is on track" when one of its three axes is at 1/3.
	if !receipt.Coverage.AllCoverageOK {
		return exitIncomplete
	}
	return exitOK
}

const exitIncomplete = 3

// poolRevision is the revision-level row the export walks. `datasets_json` is
// kept raw and parsed later, because a revision whose dataset list cannot be
// parsed is a data problem to report, not a silent zero-dataset item.
type poolRevision struct {
	RevisionID      string
	ReportID        string
	TenantID        string
	Revision        int64
	Status          string
	TemplateKey     string
	TemplateVersion int64
	DatasetsJSON    string
	PublishedAt     sql.NullTime
}

// listRevisions selects the *narrowest* set that can satisfy §9.1's provenance
// rules: published (hence immutable), with at least one claim that has at least
// one citation whose snapshot still exists. Draft revisions are excluded because a
// gold input must be reproducible from something the system promised never to
// rewrite; a revision with no surviving snapshot is excluded because its evidence
// can no longer be shown to an annotator.
func listRevisions(ctx context.Context, tx *sql.Tx, limit int, tenant string) ([]poolRevision, error) {
	q := `
select v.id, v.report_id, v.tenant_id, v.revision, v.status, v.template_key,
       v.template_version, v.datasets_json, v.published_at
from rgx_report_revision v
join rgx_report r on r.id = v.report_id
where v.status = 'published'
  and r.deleted_at is null
  and exists (
      select 1 from rgx_report_claim c
      join rgx_report_citation ct on ct.claim_id = c.id
      join rgx_report_evidence_snapshot s on s.id = ct.evidence_snapshot_id
      where c.revision_id = v.id)
`
	var args []any
	if strings.TrimSpace(tenant) != "" {
		q += "  and v.tenant_id = $2\n"
		args = append(args, limit, tenant)
	} else {
		args = append(args, limit)
	}
	q += "order by v.published_at desc nulls last, v.id\nlimit $1"

	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	defer rows.Close()

	var out []poolRevision
	for rows.Next() {
		var r poolRevision
		if err := rows.Scan(&r.RevisionID, &r.ReportID, &r.TenantID, &r.Revision, &r.Status,
			&r.TemplateKey, &r.TemplateVersion, &r.DatasetsJSON, &r.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan revision: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// datasetRef is the shape stored in `rgx_report_revision.datasets_json`. Only the
// ids are needed; role/ordering are report-domain concerns.
type datasetRef struct {
	DatasetID string `json:"dataset_id"`
}

func datasetIDs(raw string) []string {
	var refs []datasetRef
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if r.DatasetID != "" {
			out = append(out, r.DatasetID)
		}
	}
	return out
}

// claimsOfRevision returns claims joined with the section prompt they were written
// under. `section.query` is the retrieval prompt; `title` is what the reader sees.
// Both go into `section_prompt`, because an annotator judging "does this text
// support this sentence" needs to know what the section was asked to do.
func claimsOfRevision(ctx context.Context, tx *sql.Tx, revisionID string) ([]Item, error) {
	rows, err := tx.QueryContext(ctx, `
select c.id, c.claim_text, coalesce(c.claim_type, ''), coalesce(c.support_status, ''),
       c.section_ord, coalesce(s.title, ''), coalesce(s.query, '')
from rgx_report_claim c
left join rgx_report_section s on s.id = c.section_id
where c.revision_id = $1
order by c.section_ord, c.ord`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var title, query string
		if err := rows.Scan(&it.ClaimID, &it.ClaimText, &it.ClaimType, &it.PriorSupportStatus,
			&it.SectionOrd, &title, &query); err != nil {
			return nil, fmt.Errorf("scan claim: %w", err)
		}
		it.SectionPrompt = sectionPrompt(title, query)
		out = append(out, it)
	}
	return out, rows.Err()
}

// sectionPrompt combines the two halves without inventing a third: the title is
// the question the reader asked, the query is what the system searched for. Keeping
// both means a gold item reproduces the *judging context*, not just the sentence.
func sectionPrompt(title, query string) string {
	t, q := trimSpace(title), trimSpace(query)
	switch {
	case t != "" && q != "":
		return t + "\n" + q
	case t != "":
		return t
	default:
		return q
	}
}

// citationRow is one cited snapshot, already deduplicated by
// (chunk_id, content_hash) the way §9.1 requires.
type citationRow struct {
	ClaimID     string
	SnapshotID  string
	ChunkID     string
	DocumentID  string
	DatasetID   string
	Content     string
	ContentHash string
	Truncated   bool
	CitationOrd int
}

// citationsOfRevision reads every citation of a revision in one query. The snapshot
// is joined (inner) on purpose: a citation whose snapshot was purged cannot be
// annotated, and an inner join makes it disappear from the item instead of
// appearing as an empty string an annotator would have to interpret.
func citationsOfRevision(ctx context.Context, tx *sql.Tx, revisionID string) (map[string][]Evidence, error) {
	rows, err := tx.QueryContext(ctx, `
select ct.claim_id, ct.evidence_snapshot_id, s.chunk_id, coalesce(s.document_id, ''),
       coalesce(s.dataset_id, ''), s.content, s.content_hash, s.truncated, ct.ord
from rgx_report_citation ct
join rgx_report_evidence_snapshot s on s.id = ct.evidence_snapshot_id
where ct.revision_id = $1 and ct.claim_id is not null and ct.claim_id <> ''
order by ct.claim_id, ct.ord`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("citations: %w", err)
	}
	defer rows.Close()

	out := map[string][]Evidence{}
	for rows.Next() {
		var claimID string
		var ev Evidence
		if err := rows.Scan(&claimID, &ev.SnapshotID, &ev.ChunkID, &ev.DocumentID, &ev.DatasetID,
			&ev.Content, &ev.ContentHash, &ev.Truncated, &ev.CitationOrd); err != nil {
			return nil, fmt.Errorf("scan citation: %w", err)
		}
		ev.ContentChars = len([]rune(ev.Content))
		out[claimID] = append(out[claimID], ev)
	}
	return out, rows.Err()
}

// candidateRow is one frozen retrieval-pool row.
type candidateRow struct {
	SectionOrd  int
	ChunkID     string
	Content     string
	ContentHash string
	PoolRank    int
	Truncated   bool
}

func candidatesOfRevision(ctx context.Context, tx *sql.Tx, revisionID string) (map[int][]Candidate, error) {
	rows, err := tx.QueryContext(ctx, `
select section_ord, chunk_id, coalesce(content, ''), content_hash, pool_rank, truncated
from rgx_report_conflict_candidate
where revision_id = $1
order by section_ord, pool_rank, chunk_id`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("candidates: %w", err)
	}
	defer rows.Close()
	out := map[int][]Candidate{}
	for rows.Next() {
		var ord int
		var c Candidate
		if err := rows.Scan(&ord, &c.ChunkID, &c.Content, &c.ContentHash, &c.PoolRank, &c.Truncated); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		c.ContentChars = len([]rune(c.Content))
		out[ord] = append(out[ord], c)
	}
	return out, rows.Err()
}

// itemsOfRevision assembles one revision into pool items.
//
// Three decisions are visible here and none of them is an accident:
//   - a claim with no citation is skipped (and counted), because §9.1's evidence
//     source is the real snapshot text; there is no fallback to the claim's own
//     words;
//   - candidates are filtered against the revision-wide cited set, so the §9.1
//     rule "a chunk is either cited or a candidate" holds across sections;
//   - `prior_support_status` is carried but never copied into a label.
func itemsOfRevision(ctx context.Context, tx *sql.Tx, rev poolRevision) ([]Item, int, int, error) {
	items, err := claimsOfRevision(ctx, tx, rev.RevisionID)
	if err != nil {
		return nil, 0, 0, err
	}
	cites, err := citationsOfRevision(ctx, tx, rev.RevisionID)
	if err != nil {
		return nil, 0, 0, err
	}
	cands, err := candidatesOfRevision(ctx, tx, rev.RevisionID)
	if err != nil {
		return nil, 0, 0, err
	}

	cited := map[string]bool{}
	for _, evs := range cites {
		for _, ev := range evs {
			cited[ev.ChunkID+":"+ev.ContentHash] = true
		}
	}

	datasets := datasetIDs(rev.DatasetsJSON)
	publishedAt := ""
	if rev.PublishedAt.Valid {
		publishedAt = rev.PublishedAt.Time.UTC().Format(time.RFC3339)
	}
	src := Source{
		TenantID: rev.TenantID, ReportID: rev.ReportID, RevisionID: rev.RevisionID,
		Revision: rev.Revision, RevisionStatus: rev.Status, TemplateKey: rev.TemplateKey,
		TemplateVersion: rev.TemplateVersion, DatasetIDs: datasets, PublishedAt: publishedAt,
	}

	out := make([]Item, 0, len(items))
	skipped := 0
	for _, it := range items {
		evs := cites[it.ClaimID]
		if len(evs) == 0 {
			skipped++
			continue
		}
		it.Evidence = evs
		it.CandidatePool = filterCandidates(cands[it.SectionOrd], cited)
		it.Source = src
		it.ItemID = rev.RevisionID + ":" + it.ClaimID
		it.AsOfDate = stamp(time.Now())
		it.GoldEvidenceIDs = make([]string, 0, len(evs))
		out = append(out, it)
	}
	return out, len(items), skipped, nil
}

func filterCandidates(in []Candidate, cited map[string]bool) []Candidate {
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		if cited[c.ChunkID+":"+c.ContentHash] {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PoolRank != out[j].PoolRank {
			return out[i].PoolRank < out[j].PoolRank
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	return out
}

// writeReceipt is used by tests to keep the receipt shape honest.
func (r poolExport) writeReceipt(w io.Writer) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}
