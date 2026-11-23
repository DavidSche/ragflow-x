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

	"github.com/ragflow-x/ragflow-x/internal/model"
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
	// CandidatesElsewhere counts exported candidates that the same revision cited in
	// another section. It is printed because the number is the *reason* the dedup unit
	// is the section: without it, "candidate_rows went from 0 to N" reads as if the
	// freeze had been broken, when in fact the filter stopped deleting real rows.
	CandidatesElsewhere int `json:"candidate_rows_cited_elsewhere"`
	EvidenceRows        int `json:"evidence_rows"`
	// Material is the W-2 side of the receipt: "does this export contain the material a
	// stratified draw of the frozen plan needs", answered without changing the plan.
	Material poolMaterial `json:"material"`
	Coverage poolCoverage `json:"coverage"`
}

// poolMaterial is doc/304 §1.3 W-2's re-scan reading, printed instead of hand-counted.
//
// W-2's decision was "expand the corpus, then re-scan `pool` and check whether the
// per-stratum candidate counts support the plan that is already frozen". Two of those
// numbers were not in the receipt before this: how many items carry a frozen candidate
// row at all, how many carry none. They were computed by a person reading the JSONL,
// which is exactly the reading that changes when the file changes.
//
// ExportStampDates is printed *because* it is easy to misread. `Item.AsOfDate` is
// `stamp(time.Now())` at export time (pool.go's mergeRevision call), so one distinct value
// means "the export ran on one day" and nothing at all about the span of time the corpus
// covers. An earlier round of doc/304 read `distinct as_of_date = 1` as "the corpus is a
// single snapshot"; it is not, and the field name here says so.
//
// ConflictingUpperBound is deliberately an upper bound and not a quota, and its *unit* is
// the reason it lives here rather than being derived by the reader. `candsample.go` draws
// **rows**, not claims: the worksheet's unit is `(item_id, candidate_index)`, because one
// claim can carry two candidates and "we drew 120 items" says nothing about how many rows
// an annotator will judge. So the ceiling on E2's 24 `conflicting` samples is the number of
// frozen candidate **rows** — an export with 36 rows across 30 items can yield at most 36
// conflicting samples, not 30. Getting this unit wrong is exactly the mistake doc/304 §1.3
// W-1 warns about (60 条 treatment data is not 60 model calls): printing a plausible number
// with the wrong denominator is worse than printing no number.
//
// The plan's 24 is a target and this field never edits it — an upper bound of 36 does not
// license drawing 36, and an item's rows being frozen does not make them contradictory
// (only the annotator decides that).
type poolMaterial struct {
	ItemsWithCandidate    int      `json:"items_with_candidate_rows"`
	ItemsWithoutCandidate int      `json:"items_without_candidate_rows"`
	CandidateRows         int      `json:"candidate_rows"`
	ExportStampDates      []string `json:"export_stamp_dates"`
	ItemsWithoutStamp     int      `json:"items_without_export_stamp"`
	ConflictingUpperBound int      `json:"conflicting_upper_bound"`
	// NotMeasured names the strata this receipt says *nothing* about, so that
	// `conflicting_upper_bound` reaching the plan cannot be read as "the plan is covered".
	NotMeasured []poolMaterialGap `json:"not_measured"`
	Note        string            `json:"note"`
}

// poolMaterialGap is a stratum plus why the pool cannot state its material as a count.
type poolMaterialGap struct {
	Stratum string `json:"stratum"`
	Why     string `json:"why"`
}

// poolMaterialNote rides in the receipt so a reader of the JSON — or of a CI log — does
// not have to guess whether the bound is a decision, or guess which denominator it uses.
const poolMaterialNote = "conflicting_upper_bound counts frozen candidate rows, the unit " +
	"`goldtool sample` draws (doc/301 §9.1 restricts E2's conflicting samples to frozen " +
	"candidates); it is a ceiling in rows, not a quota, and it does not change the frozen plan " +
	"(doc/304 §1.3 W-2)"

// poolMaterialStampNote is the other half of "do not read this as corpus history".
const poolMaterialStampNote = "ExportStampDates are the export's own as_of_date values, " +
	"which are stamp(time.Now()) at export time: one value means the export ran on one day, " +
	"and this field says nothing about the time the corpus spans"

// poolMaterialNotMeasured is a constant, not a computed list. `outdated` is the stratum the
// plan names (12) and the pool cannot count: report_consistency.go derives `outdated` from
// one document stating a key twice with different values, which is a property of chunk
// *content*, and the pool carries no per-evidence date to count it by. Leaving the list
// empty would let a green `conflicting` bound imply a green plan.
var poolMaterialNotMeasured = []poolMaterialGap{{
	Stratum: model.ReportVerdictOutdated,
	Why: "the pool carries no per-evidence date and no same-document key grouping, and " +
		"report_consistency.go derives `outdated` from content; counting it needs a content " +
		"scan, not a receipt over this file",
}}

// poolMaterialOf is the pure half of the receipt: no database, no clock, so the numbers
// can be asserted in a unit test rather than trusted from a live run.
func poolMaterialOf(items []Item) poolMaterial {
	material := poolMaterial{Note: poolMaterialNote, NotMeasured: poolMaterialNotMeasured}
	seen := make(map[string]struct{}, 4)
	for _, item := range items {
		if len(item.CandidatePool) > 0 {
			material.ItemsWithCandidate++
		} else {
			material.ItemsWithoutCandidate++
		}
		material.CandidateRows += len(item.CandidatePool)
		date := strings.TrimSpace(item.AsOfDate)
		if date == "" {
			material.ItemsWithoutStamp++
			continue
		}
		seen[date] = struct{}{}
	}
	material.ExportStampDates = make([]string, 0, len(seen))
	for date := range seen {
		material.ExportStampDates = append(material.ExportStampDates, date)
	}
	sort.Strings(material.ExportStampDates)
	material.ConflictingUpperBound = material.CandidateRows
	return material
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
		for _, c := range it.CandidatePool {
			if c.CitedElsewhere {
				receipt.CandidatesElsewhere++
			}
		}
	}
	receipt.Datasets = len(ds)
	receipt.Templates = distinctSet(items, func(i Item) string { return i.Source.TemplateKey })
	receipt.Material = poolMaterialOf(items)
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

// itemsOfRevision reads one revision and hands it to mergeRevision.
//
// The merge itself is pure and documented there; what this function owns is the three
// queries and the receipt numbers. The one decision worth restating: candidates are
// deduplicated **per section** and a chunk the revision cited elsewhere is kept with
// `cited_elsewhere: true` (doc/301 §9.1 rules 3/5, 304 D-08/D-10). The previous
// revision-wide filter removed 17 of 17 real candidates on the 151 corpus, which is
// how E2 lost its only source.
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

	// `total` is the number of claims in the revision, not the number of exported
	// items: the receipt's `claims_seen` and `skipped_without_citation` must add up,
	// otherwise "1168 skipped" would silently mean something else after a refactor.
	total := len(items)
	out := mergeRevision(rev.RevisionID, items, cites, cands, src, stamp(time.Now()))
	return out, total, total - len(out), nil
}

// mergeRevision is the pure half of the export: claims + their citations + their
// section's candidates → pool items. It is separate so that the one property the
// whole candidate design rests on can be tested without a database:
//
// **a chunk cited in section A and frozen as a candidate in section B keeps both
// roles.** Section A's citation stays in `evidence` (doc/301 §9.1 rule 5: citation
// and candidate are two role associations over one content entity, and the export may
// never collapse them), and section B's candidate stays in `candidate_pool` with
// `cited_elsewhere: true` (rule 3). Deleting either side is what made the 151 corpus
// lose 17 of 17 real candidates (304 D-08).
func mergeRevision(revisionID string, items []Item, cites map[string][]Evidence,
	cands map[int][]Candidate, src Source, asOf string) []Item {
	// Two scopes: the section an annotator judges in, and "somewhere in this revision"
	// (used only to label a surviving candidate, never to delete one).
	bySection := map[int]map[string]bool{}
	anywhere := map[string]bool{}
	for _, it := range items {
		for _, ev := range cites[it.ClaimID] {
			key := ev.ChunkID + ":" + ev.ContentHash
			anywhere[key] = true
			if bySection[it.SectionOrd] == nil {
				bySection[it.SectionOrd] = map[string]bool{}
			}
			bySection[it.SectionOrd][key] = true
		}
	}

	out := make([]Item, 0, len(items))
	for _, it := range items {
		evs := cites[it.ClaimID]
		if len(evs) == 0 {
			continue // a claim with no citation cannot be judged against evidence
		}
		it.Evidence = evs
		it.CandidatePool = filterCandidates(cands[it.SectionOrd], bySection[it.SectionOrd], anywhere)
		// `scan_scope` is derived from what the export actually carries, not from the
		// setting that was in force: doc/301 §5.2.2 defines K as the number of frozen
		// rows, and the setting can be widened after generation without backfilling.
		it.GoldScanScope = scanScopeOf(it.CandidatePool)
		it.Source = src
		it.ItemID = revisionID + ":" + it.ClaimID
		it.AsOfDate = asOf
		it.GoldEvidenceIDs = make([]string, 0, len(evs))
		out = append(out, it)
	}
	return out
}

// scanScopeOf renders doc/301 §5.2.2's vocabulary. `cited_only` is the default state
// of the product (`report.candidate_scan_top_k=0`), so an export that never saw a
// candidate says so instead of looking like a scan that came back empty.
func scanScopeOf(cands []Candidate) string {
	if len(cands) == 0 {
		return "cited_only"
	}
	return fmt.Sprintf("cited+candidate:%d", len(cands))
}

// filterCandidates drops candidates this **section** cited and flags the ones another
// section of the same revision cited.
//
// `sectionCited` is passed even though the freeze already skipped those rows: an
// export that trusts upstream to have been right is an export that cannot detect
// upstream being wrong. If a row shows up here as section-cited, generation and export
// disagree and the row must not be treated as a candidate.
func filterCandidates(in []Candidate, sectionCited, revisionCited map[string]bool) []Candidate {
	out := make([]Candidate, 0, len(in))
	for _, c := range in {
		key := c.ChunkID + ":" + c.ContentHash
		if sectionCited[key] {
			continue
		}
		c.CitedElsewhere = revisionCited[key]
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
