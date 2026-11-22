// Command assurance-quality runs doc/301 §9.4's five review-quality self-checks
// against a replay window and reports one verdict per check.
//
// §9.4's own heading says the checks run 「在 CI 里，不靠人」. The CI half is
// `scripts/release-gate/assurance-quality.sh`, which drives the pure function on
// fixtures (and proves each of the five can go red); this binary is the other half —
// the same function pointed at a real database, so the numbers in doc/305 §2.16 are
// produced by the code the gate checks rather than by a hand-written query.
//
// Two properties of this command are deliberate:
//
//   - **It opens a read-only transaction.** A self-check that can write is a
//     self-check that can fix its own finding.
//   - **A missing denominator exits 3, not 0.** "I could not measure it" and "it
//     holds" are different sentences, and a CI step that folds them together is how
//     the empty-table green badge happens. `--receipt` records the same distinction
//     (`ok: false`, `verdict: unverified`), because the JSON outlives the console.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

const (
	receiptVersion = "rgx.assurance.qualityselfcheck/1"
	exitOK         = 0
	exitFailed     = 1
	exitUsage      = 2
	exitUnverified = 3
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// errHelp exists so `--help` can be exit 0: asking what a command does is not a usage
// error, and a CI step that treats help as failure teaches people not to ask.
var errHelp = errors.New("help requested")

type options struct {
	dsn     string
	since   string
	tenant  string
	receipt string
}

func usage(w io.Writer) {
	fmt.Fprint(w, `assurance-quality — doc/301 §9.4's five review-quality self-checks over a replay window.

  assurance-quality --dsn <dsn> [--since <duration|RFC3339>] [--tenant <id>] [--receipt <path>]

  --dsn      database DSN, opened read-only; also read from RGX_ASSURANCE_DSN
  --since    window start: a duration back from now (168h) or an RFC3339 instant.
             Default is every row, and the chosen window is printed with the report
             because it changes the answer.
  --tenant   restrict the window to one tenant_id (default: every tenant)
  --receipt  write the JSON receipt to this path; the path must not exist yet,
             because a receipt is evidence of one run and is not rewritten

Exit codes: 0 all five pass · 1 a check failed · 2 usage · 3 a check could not be
measured (3 is not a pass: a check without a denominator never passes).
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args, stdout, stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			return exitOK
		}
		fmt.Fprintf(stderr, "assurance-quality: %v\n", err)
		return exitUsage
	}
	now := time.Now().UTC()
	since, err := parseSince(opts.since, now)
	if err != nil {
		fmt.Fprintf(stderr, "assurance-quality: %v\n", err)
		return exitUsage
	}

	ctx := context.Background()
	db, tx, err := openReadOnly(ctx, opts.dsn)
	if err != nil {
		fmt.Fprintf(stderr, "assurance-quality: %v\n", err)
		return exitUsage
	}
	defer db.Close()
	defer tx.Rollback()

	w := window{Since: since, Tenant: opts.tenant}
	input, err := loadInput(ctx, tx, w)
	if err != nil {
		fmt.Fprintf(stderr, "assurance-quality: %v\n", err)
		return exitUsage
	}
	result := service.EvaluateQualitySelfCheck(input)

	fmt.Fprintf(stdout, "# doc/301 §9.4 self-check — %s\n\n", describeWindow(w))
	fmt.Fprint(stdout, service.RenderQualitySelfCheckMarkdown(result))
	failed, unverified := 0, 0
	for _, row := range result.Rows {
		switch row.Verdict {
		case "fail":
			failed++
		case "unverified":
			unverified++
		}
	}
	fmt.Fprintf(stdout, "quality-selfcheck: verdict=%s rows=%d failed=%d unverified=%d\n",
		result.Verdict(), len(result.Rows), failed, unverified)

	if opts.receipt != "" {
		if err := writeReceipt(opts.receipt, w, result); err != nil {
			fmt.Fprintf(stderr, "assurance-quality: %v\n", err)
			return exitUsage
		}
	}

	switch result.Verdict() {
	case "pass":
		return exitOK
	case "fail":
		return exitFailed
	default:
		return exitUnverified
	}
}

func parseOptions(args []string, stdout, stderr io.Writer) (options, error) {
	var opts options
	fs := flag.NewFlagSet("assurance-quality", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // usage() is the one place the flags are described
	fs.StringVar(&opts.dsn, "dsn", "", "")
	fs.StringVar(&opts.since, "since", "", "")
	fs.StringVar(&opts.tenant, "tenant", "", "")
	fs.StringVar(&opts.receipt, "receipt", "", "")
	fs.Usage = func() { usage(stderr) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return opts, errHelp
		}
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if opts.dsn == "" {
		opts.dsn = os.Getenv("RGX_ASSURANCE_DSN")
	}
	if opts.dsn == "" {
		return opts, errors.New("--dsn is required (or set RGX_ASSURANCE_DSN): the checks are counts, and " +
			"a count needs a database")
	}
	// Checked here rather than at write time: a run that prints five rows and then
	// refuses its own receipt has spent the operator's window and produced evidence
	// nobody kept. Failing before the first query costs nothing.
	if opts.receipt != "" {
		if _, err := os.Stat(opts.receipt); err == nil {
			return opts, fmt.Errorf("refusing to overwrite %s: a receipt is the evidence of one run, so a "+
				"re-run writes a new file", opts.receipt)
		}
	}
	return opts, nil
}

// parseSince accepts a duration back from now or an instant, because both spellings
// appear in operator hands: `--since 168h` in CI and `--since 2026-11-22T00:00:00Z` when
// re-checking a deploy. An empty value means "every row", which the report prints.
func parseSince(raw string, now time.Time) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			return nil, fmt.Errorf("--since %q is negative: the window must end in the present", raw)
		}
		at := now.Add(-d)
		return &at, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("--since %q is neither a duration (168h) nor an RFC3339 instant", raw)
	}
	at = at.UTC()
	return &at, nil
}

// window is the selection this run audited. It is printed and stored with the result
// because every one of the five numbers below is a statement about *this* window.
type window struct {
	Since  *time.Time
	Tenant string
}

func describeWindow(w window) string {
	parts := make([]string, 0, 2)
	if w.Since != nil {
		parts = append(parts, "rows written since "+w.Since.Format(time.RFC3339))
	} else {
		parts = append(parts, "every row")
	}
	if w.Tenant != "" {
		parts = append(parts, "tenant "+w.Tenant)
	} else {
		parts = append(parts, "every tenant")
	}
	return strings.Join(parts, ", ")
}

// openReadOnly is deliberately the same shape as goldtool's: a read-only transaction,
// so the audit cannot be the thing that changes the numbers it reports.
func openReadOnly(ctx context.Context, dsn string) (*sql.DB, *sql.Tx, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(2)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("ping database: %w", err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("begin read-only transaction: %w", err)
	}
	return db, tx, nil
}

// filter renders the window as a WHERE clause over the given timestamp and tenant
// columns. Both are parameters because both differ between the tables: an evaluation
// is stamped when it was written, a citation when it was referenced, and a joined
// query has two `tenant_id`s — naming the table is what keeps "the window" from
// silently becoming "the window over whichever column the planner picked".
func (w window) filter(timeColumn, tenantColumn string) (string, []any) {
	clauses := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if w.Since != nil {
		args = append(args, *w.Since)
		clauses = append(clauses, fmt.Sprintf("%s >= $%d", timeColumn, len(args)))
	}
	if w.Tenant != "" {
		args = append(args, w.Tenant)
		clauses = append(clauses, fmt.Sprintf("%s = $%d", tenantColumn, len(args)))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " where " + strings.Join(clauses, " and "), args
}

func loadInput(ctx context.Context, tx *sql.Tx, w window) (service.QualitySelfCheckInput, error) {
	input := service.QualitySelfCheckInput{}
	var err error
	if input.QuoteStatuses, err = quoteStatusCounts(ctx, tx, w); err != nil {
		return input, err
	}
	if input.Replays, err = replayGroups(ctx, tx, w); err != nil {
		return input, err
	}
	if input.Supported, input.SupportedUncited, err = supportedEvidence(ctx, tx, w); err != nil {
		return input, err
	}
	if input.Citations, err = citationShape(ctx, tx, w); err != nil {
		return input, err
	}
	if input.AbstainReasons, err = abstainReasons(ctx, tx, w); err != nil {
		return input, err
	}
	return input, nil
}

// quoteStatusCounts is Q-1's numerator and denominator in one pass: every status word
// with its count, including the words the closed set does not name (the check turns
// those into a named fault instead of dropping them).
func quoteStatusCounts(ctx context.Context, tx *sql.Tx, w window) (map[string]int64, error) {
	where, args := w.filter("created_at", "tenant_id")
	counts := map[string]int64{}
	rows, err := tx.QueryContext(ctx, `
		select quote_status, count(*)
		  from rgx_report_claim_evaluation`+andWhere(where, "coalesce(quote_status, '') <> ''")+`
		 group by quote_status`, args...)
	if err != nil {
		return nil, fmt.Errorf("count quote statuses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

// replayGroups is Q-2's population: cache keys seen more than once, with the distinct
// judgment tuples underneath them.
func replayGroups(ctx context.Context, tx *sql.Tx, w window) ([]service.QualitySelfCheckReplay, error) {
	where, args := w.filter("created_at", "tenant_id")
	rows, err := tx.QueryContext(ctx, `
		select cache_key, count(*),
		       string_agg(distinct concat_ws('|', support_verdict, coalesce(structure_verdict, ''),
		                                    coalesce(relevance, ''), coalesce(quote_status, '')), chr(10))
		  from rgx_report_claim_evaluation`+andWhere(where, "coalesce(cache_key, '') <> ''")+`
		 group by cache_key
		having count(*) > 1`, args...)
	if err != nil {
		return nil, fmt.Errorf("group verdict input sets: %w", err)
	}
	defer rows.Close()
	var replays []service.QualitySelfCheckReplay
	for rows.Next() {
		var replay service.QualitySelfCheckReplay
		var variants string
		if err := rows.Scan(&replay.CacheKey, &replay.Rows, &variants); err != nil {
			return nil, err
		}
		replay.Variants = strings.Split(variants, "\n")
		sort.Strings(replay.Variants)
		replays = append(replays, replay)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(replays, func(i, j int) bool { return replays[i].CacheKey < replays[j].CacheKey })
	return replays, nil
}

// supportedEvidence is Q-3: the denominator is `supported` rows, not all rows, because
// the question is whether a conclusion that claims support names its source.
func supportedEvidence(ctx context.Context, tx *sql.Tx, w window) (int64, int64, error) {
	where, args := w.filter("created_at", "tenant_id")
	var supported, uncited int64
	err := tx.QueryRowContext(ctx, `
		select count(*),
		       count(*) filter (where btrim(coalesce(evidence_ids_json, '')) in ('', '[]'))
		  from rgx_report_claim_evaluation`+
		andWhere(where, "support_verdict = '"+model.ReportVerdictSupported+"'"), args...).
		Scan(&supported, &uncited)
	if err != nil {
		return 0, 0, fmt.Errorf("count supported rows without evidence: %w", err)
	}
	return supported, uncited, nil
}

// citationShape is Q-4. It reads the shape (the column list) and the resolvability
// (every citation reaches an evidence snapshot) rather than an overlap count; see
// internal/service/report_quality_selfcheck.go's header for why an overlap count would
// report a correct system as broken.
func citationShape(ctx context.Context, tx *sql.Tx, w window) (service.QualitySelfCheckCitations, error) {
	shape := service.QualitySelfCheckCitations{}
	columns, err := tx.QueryContext(ctx, `
		select column_name from information_schema.columns
		 where table_name = 'rgx_report_citation'
		 order by ordinal_position`)
	if err != nil {
		return shape, fmt.Errorf("read citation columns: %w", err)
	}
	for columns.Next() {
		var name string
		if err := columns.Scan(&name); err != nil {
			columns.Close()
			return shape, err
		}
		shape.Columns = append(shape.Columns, name)
	}
	if err := columns.Err(); err != nil {
		columns.Close()
		return shape, err
	}
	columns.Close()

	where, args := w.filter("c.referenced_at", "c.tenant_id")
	err = tx.QueryRowContext(ctx, `
		select count(*), count(*) filter (where s.id is null)
		  from rgx_report_citation c
		  left join rgx_report_evidence_snapshot s on s.id = c.evidence_snapshot_id`+where, args...).
		Scan(&shape.Rows, &shape.DanglingSnapshot)
	if err != nil {
		return shape, fmt.Errorf("count citations: %w", err)
	}
	return shape, nil
}

// abstainReasons is Q-5's denominator: abstentions, by reason.
func abstainReasons(ctx context.Context, tx *sql.Tx, w window) (map[string]int64, error) {
	where, args := w.filter("created_at", "tenant_id")
	counts := map[string]int64{}
	rows, err := tx.QueryContext(ctx, `
		select abstain_reason, count(*)
		  from rgx_report_claim_evaluation`+
		andWhere(where, "support_verdict = '"+model.ReportVerdictAbstained+"'")+`
		 group by abstain_reason`, args...)
	if err != nil {
		return nil, fmt.Errorf("count abstain reasons: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var reason string
		var count int64
		if err := rows.Scan(&reason, &count); err != nil {
			return nil, err
		}
		if reason == "" {
			// An abstention with no reason is a fault the check must be able to
			// name; an anonymous bucket would be counted and invisible.
			reason = "(none)"
		}
		counts[reason] = count
	}
	return counts, rows.Err()
}

// andWhere appends a predicate to a window's WHERE clause (or starts one), so the
// window and the check's own filter cannot disagree about which clause comes first.
func andWhere(where, predicate string) string {
	if where == "" {
		return " where " + predicate
	}
	return where + " and " + predicate
}

type receipt struct {
	Version    string        `json:"version"`
	Window     receiptWindow `json:"window"`
	Rows       []receiptRow  `json:"rows"`
	Verdict    string        `json:"verdict"`
	Failed     int           `json:"failed"`
	Unverified int           `json:"unverified"`
	Problems   []string      `json:"problems"`
	OK         bool          `json:"ok"`
}

type receiptWindow struct {
	Since  string `json:"since"`
	Tenant string `json:"tenant,omitempty"`
}

type receiptRow struct {
	ID          string `json:"id"`
	Check       string `json:"check"`
	Expectation string `json:"expectation"`
	Observed    string `json:"observed"`
	Sample      string `json:"sample"`
	Verdict     string `json:"verdict"`
	Detail      string `json:"detail"`
}

// writeReceipt refuses an existing path: a receipt is the evidence for one run, and
// overwriting it is how two runs become one number nobody can reproduce.
func writeReceipt(path string, w window, result service.QualitySelfCheckResult) error {
	rec := receipt{
		Version:  receiptVersion,
		Window:   receiptWindow{Tenant: w.Tenant},
		Verdict:  result.Verdict(),
		Problems: make([]string, 0, len(result.Rows)),
	}
	if w.Since != nil {
		rec.Window.Since = w.Since.Format(time.RFC3339)
	}
	for _, row := range result.Rows {
		rec.Rows = append(rec.Rows, receiptRow{
			ID: row.ID, Check: row.Check, Expectation: row.Expectation,
			Observed: row.Observed, Sample: row.Sample, Verdict: row.Verdict, Detail: row.Detail,
		})
		switch row.Verdict {
		case "fail":
			rec.Failed++
			rec.Problems = append(rec.Problems, row.ID+" fail: "+row.Detail)
		case "unverified":
			rec.Unverified++
			rec.Problems = append(rec.Problems, row.ID+" unverified: "+row.Detail)
		}
	}
	rec.OK = rec.Verdict == "pass" && rec.Failed == 0 && rec.Unverified == 0

	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing to overwrite %s: a receipt is the evidence of one run, so a re-run "+
				"writes a new file", path)
		}
		return fmt.Errorf("write receipt: %w", err)
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("write receipt: %w", err)
	}
	return f.Close()
}
