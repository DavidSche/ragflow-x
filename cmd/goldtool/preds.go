package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// `preds` writes the other half of an experiment: the *predictions* file that
// `goldtool eval` scores. Until this existed, doc/301 §9.2's "方法 A / 方法 B" was
// unimplementable end to end — a run stored its verdicts in
// `rgx_report_claim_evaluation`, `eval` wanted a JSONL, and nothing connected them
// (304 §7 item 17). The first person to need the connection would have written an
// ad-hoc sql→jq→csv pipeline, and the first thing such a pipeline loses is which
// attempt, which run, and which claims it silently dropped.
//
// Three rules, each one closing a way the file could look more complete than the
// run was:
//
//  1. **The population comes from the items file, not from the run.** One row per
//     item in `--items`, in that file's order. A run that judged 87 of 120 claims
//     produces 87 rows plus a named list of the 33 it never judged; it does not
//     produce a 120-row file with 33 invented `not_evaluated` rows, because the
//     reason for those 33 would be a guess (budget? policy? never triggered?).
//  2. **One method, named on the command line.** `--method` has no default. E1
//     compares methods, so a file that mixes `deterministic` and `semantic` rows
//     is not "method A's" file — it is two files shuffled together.
//  3. **A verdict is transcribed, never repaired.** An unknown verdict, an
//     `abstained` without a reason, or a `supported` whose `quote_status` is empty
//     makes the export fail with the claim ids rather than fill in a plausible
//     value. `not_provided` looks harmless in a `supported` row, but it makes
//     `quote_verified_rate` fail for a pipeline reason while the report blames the
//     model (doc/301 §4.2/§5.1).

// predRow is one evaluation row joined with the run that owns it. It is a struct
// rather than a `model.ReportClaimEvaluation` because the selection needs the run's
// status/mode/created_at, and because the export must not accidentally depend on
// columns it does not read.
type predRow struct {
	RunID         string
	RunStatus     string
	RunMode       string
	RunCreatedAt  time.Time
	Method        string
	MethodVersion string
	ModelID       string
	RevisionID    string
	ClaimID       string
	Attempt       int
	Verdict       string
	QuoteStatus   string
	AbstainReason string
	NotEvalReason string
	CacheHit      bool
}

// predsReceipt is what the command leaves in the log. It is a JSON object like
// `pool`'s receipt, for the same reason: the numbers that make a file trustworthy
// (which runs, which claims were missing, how many had several candidate rows) are
// exactly the numbers nobody re-derives by hand later.
type predsReceipt struct {
	ExportedAt string `json:"exported_at"`
	ItemsFile  string `json:"items_file"`
	// ItemsHasLabels states whether the population carries `gold_verdict`. A
	// worksheet can be transcribed (to look at what a run did) but cannot be
	// scored, and `eval` refuses it — printing this here means the operator learns
	// that now, not two commands later.
	ItemsHasLabels bool   `json:"items_has_labels"`
	Method         string `json:"method"`
	Mode           string `json:"mode"`
	Items          int    `json:"items"`
	Written        int    `json:"rows_written"`
	RowsConsidered int    `json:"rows_considered"`
	// MultiSource counts items where more than one row competed. The newest row
	// wins; the count is printed because "we took the latest re-run" is a decision,
	// and a silent one would make two exports from the same database disagree.
	MultiSource     int            `json:"items_with_multiple_candidate_rows"`
	ByVerdict       map[string]int `json:"by_verdict"`
	ByQuoteStatus   map[string]int `json:"by_quote_status"`
	ByAbstainReason map[string]int `json:"by_abstain_reason"`
	EmptyQuote      int            `json:"empty_quote_status_filled_not_provided"`
	NotRequired     int            `json:"not_required_rows"`
	CacheHitRows    int            `json:"cache_hit_rows"`
	Runs            []predRunRef   `json:"runs"`
	Missing         []string       `json:"missing_items"`
	MissingCount    int            `json:"missing_items_count"`
	// MethodsSeen is a diagnosis, not a filter: if `--method` is a typo the file
	// would be empty, and this map says what the database actually holds.
	MethodsSeen map[string]int `json:"methods_seen_in_these_revisions"`
	Degraded    []string       `json:"degraded_runs"`
	Rejected    []string       `json:"rejected_rows"`
}

type predRunRef struct {
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Mode      string `json:"mode"`
	CreatedAt string `json:"created_at"`
	Rows      int    `json:"rows_used"`
}

func runPreds(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("preds", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", os.Getenv(envDSN), "PostgreSQL DSN (read-only use); defaults to $"+envDSN)
	itemsPath := fs.String("items", "", "items JSONL that defines the population (gold file, or a worksheet to inspect)")
	out := fs.String("out", "", "output predictions JSONL path")
	method := fs.String("method", "", "evaluation method to export: "+strings.Join(model.ReportEvaluationMethods(), "|"))
	mode := fs.String("mode", model.ReportAssuranceModeFull, "assurance run mode to read (default: full)")
	maxRevisions := fs.Int("max-revisions", 64, "safety cap on revisions read from the items file")
	quiet := fs.Bool("quiet", false, "do not print the JSON receipt")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*itemsPath) == "" || strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "goldtool preds: --items and --out are required")
		return exitUsage
	}
	if strings.TrimSpace(*method) == "" {
		// No default on purpose: doc/301 §9.2 compares methods, so "which method is
		// this file" must be an answer the operator gave, not one the tool assumed.
		fmt.Fprintf(stderr, "goldtool preds: --method is required (%s)\n",
			strings.Join(model.ReportEvaluationMethods(), "|"))
		return exitUsage
	}
	if !containsStr(model.ReportEvaluationMethods(), *method) {
		fmt.Fprintf(stderr, "goldtool preds: --method %q is not in the closed set %s\n",
			*method, strings.Join(model.ReportEvaluationMethods(), "/"))
		return exitUsage
	}
	if strings.TrimSpace(*dsn) == "" {
		fmt.Fprintf(stderr, "goldtool preds: no DSN (pass --dsn or set %s)\n", envDSN)
		return exitUsage
	}

	// The population must be a file an assembly accepted: `preds` copies verdicts out of a
	// run and binds them to these item ids, so transcribing them against a draft gold file
	// would make the predictions look like they belong to a gold set that does not exist yet.
	if refusals := goldRefusals(*itemsPath); len(refusals) > 0 {
		for _, r := range refusals {
			fmt.Fprintf(stderr, "goldtool preds: --items: %s\n", r)
		}
		return exitInvariant
	}

	items, err := readItems(*itemsPath)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool preds: %v\n", err)
		return exitUsage
	}
	if len(items) == 0 {
		fmt.Fprintf(stderr, "goldtool preds: %s holds no items; an empty population would write an empty file\n", *itemsPath)
		return exitUsage
	}
	revs := distinctSet(items, func(it Item) string { return it.Source.RevisionID })
	if len(revs) > *maxRevisions {
		fmt.Fprintf(stderr, "goldtool preds: items span %d revisions, above --max-revisions %d; "+
			"refusing to walk an unbounded set\n", len(revs), *maxRevisions)
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, tx, err := openReadOnly(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool preds: %v\n", err)
		return exitUsage
	}
	defer db.Close()
	defer tx.Rollback() //nolint:errcheck // read-only; rollback is the only way out

	rows, err := evalRowsForRevisions(ctx, tx, revs, *method, *mode)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool preds: %v\n", err)
		return exitUsage
	}
	seen, err := methodsSeen(ctx, tx, revs, *mode)
	if err != nil {
		fmt.Fprintf(stderr, "goldtool preds: %v\n", err)
		return exitUsage
	}

	preds, receipt, problems := planPredictions(items, rows, *method, *mode)
	receipt.ExportedAt = time.Now().UTC().Format(time.RFC3339)
	receipt.ItemsFile = *itemsPath
	receipt.MethodsSeen = seen
	receipt.Items = len(items)

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "goldtool preds: %s\n", p)
		}
		return exitInvariant
	}

	lines := make([]string, 0, len(preds))
	for _, p := range preds {
		raw, err := json.Marshal(p)
		if err != nil {
			fmt.Fprintf(stderr, "goldtool preds: encode %s: %v\n", p.ItemID, err)
			return exitInvariant
		}
		lines = append(lines, string(raw))
	}
	if err := writeJSONL(*out, lines); err != nil {
		fmt.Fprintf(stderr, "goldtool preds: write %s: %v\n", *out, err)
		return exitUsage
	}

	if !*quiet {
		raw, _ := json.MarshalIndent(receipt, "", "  ")
		fmt.Fprintln(stdout, string(raw))
	}

	return predsExitCode(receipt)
}

// predsExitCode is the whole "is this file complete" judgement in one place, so a test
// can hold it to the same standard as the report rows: every incomplete outcome has to
// be reachable, and a complete one has to reach 0.
func predsExitCode(receipt predsReceipt) int {
	// Two reasons to say "incomplete" rather than "0", both about the file being
	// smaller or weaker than the population it claims to describe:
	//   - some items have no row for this method (the file cannot cover them);
	//   - a run that supplied rows is `degraded` (it finished with a fallback, e.g.
	//     no review model, so the method's answer is not the method as designed).
	if receipt.MissingCount > 0 || len(receipt.Degraded) > 0 || receipt.Written == 0 {
		return exitIncomplete
	}
	return exitOK
}

// planPredictions is the pure core: items in, rows in, one prediction per item out.
//
// Ordering follows the items file, not the database, so two exports of the same
// pair are byte-identical and a `diff` between method A and method B shows only
// verdict differences.
func planPredictions(items []Item, rows []predRow, method, mode string) ([]Prediction, predsReceipt, []string) {
	receipt := predsReceipt{
		Method:          method,
		Mode:            mode,
		ByVerdict:       map[string]int{},
		ByQuoteStatus:   map[string]int{},
		ByAbstainReason: map[string]int{},
	}
	var problems []string

	// A file is either labelled or not; half-labelled means two populations were
	// concatenated, and the rows that have labels would be scored against claims
	// the annotators never saw.
	labelled := 0
	for _, it := range items {
		if strings.TrimSpace(it.GoldVerdict) != "" {
			labelled++
		}
	}
	receipt.ItemsHasLabels = labelled == len(items)
	if labelled != 0 && !receipt.ItemsHasLabels {
		problems = append(problems, fmt.Sprintf(
			"%d of %d items carry gold_verdict: a half-labelled population cannot be scored "+
				"(the labelled rows would be compared against claims nobody annotated)", labelled, len(items)))
	}

	// Index rows per (revision, claim). A claim is identified by both: a claim id is
	// unique inside a revision, not across them.
	byClaim := map[string][]predRow{}
	for _, r := range rows {
		key := r.RevisionID + "\x00" + r.ClaimID
		byClaim[key] = append(byClaim[key], r)
	}

	runRows := map[string]*predRunRef{}
	runOrder := make([]string, 0, 4)

	preds := make([]Prediction, 0, len(items))
	for _, it := range items {
		cands := byClaim[it.Source.RevisionID+"\x00"+it.ClaimID]
		if len(cands) == 0 {
			receipt.MissingCount++
			if len(receipt.Missing) < 10 {
				receipt.Missing = append(receipt.Missing, it.ItemID)
			}
			continue
		}
		receipt.RowsConsidered += len(cands)
		if len(cands) > 1 {
			receipt.MultiSource++
		}
		chosen := cands[0]
		for _, c := range cands[1:] {
			if predRowNewer(c, chosen) {
				chosen = c
			}
		}

		// Every competing row is validated, not just the winner: cherry-picking the
		// row that happens to be well-formed is how a defect survives an export.
		for _, c := range cands {
			if bad := validatePredRow(it.ItemID, c, method); bad != "" {
				receipt.Rejected = append(receipt.Rejected, bad)
			}
		}
		if len(receipt.Rejected) > 0 {
			problems = append(problems, receipt.Rejected...)
			receipt.Rejected = nil
		}

		if !predRunUsable(chosen.RunStatus) {
			problems = append(problems, fmt.Sprintf(
				"%s: run %s is %q; a %s run's rows are not a finished method's answer "+
					"(re-run it, or export an older run explicitly by removing the failed one from the query)",
				it.ItemID, chosen.RunID, chosen.RunStatus, chosen.RunStatus))
			continue
		}

		quoteStatus := chosen.QuoteStatus
		if quoteStatus == "" {
			// Reaching here with an empty quote_status means the verdict is not one of
			// the two that must carry a verified quote; validatePredRow already refused
			// that case. `not_provided` is the honest value for the rest.
			quoteStatus = model.ReportQuoteStatusNotProvided
			receipt.EmptyQuote++
		}

		preds = append(preds, Prediction{
			ItemID:             it.ItemID,
			Verdict:            chosen.Verdict,
			QuoteStatus:        quoteStatus,
			AbstainReason:      chosen.AbstainReason,
			NotEvaluatedReason: chosen.NotEvalReason,
		})
		receipt.ByVerdict[chosen.Verdict]++
		receipt.ByQuoteStatus[quoteStatus]++
		if chosen.AbstainReason != "" {
			receipt.ByAbstainReason[chosen.AbstainReason]++
		}
		if chosen.Verdict == model.ReportVerdictNotRequired {
			receipt.NotRequired++
		}
		if chosen.CacheHit {
			receipt.CacheHitRows++
		}
		if chosen.RunStatus == model.ReportAssuranceStatusDegraded {
			receipt.Degraded = appendOnce(receipt.Degraded, chosen.RunID)
		}

		ref := runRows[chosen.RunID]
		if ref == nil {
			ref = &predRunRef{RunID: chosen.RunID, Status: chosen.RunStatus, Mode: chosen.RunMode,
				CreatedAt: chosen.RunCreatedAt.UTC().Format(time.RFC3339)}
			runRows[chosen.RunID] = ref
			runOrder = append(runOrder, chosen.RunID)
		}
		ref.Rows++
	}
	receipt.Written = len(preds)

	sort.Strings(runOrder)
	for _, id := range runOrder {
		receipt.Runs = append(receipt.Runs, *runRows[id])
	}
	if len(problems) > 0 {
		return nil, receipt, problems
	}
	if len(preds) == 0 {
		// Not an error: the query ran, the database answered "no rows for this
		// method". Saying 0 bytes written while listing what methods do exist is the
		// difference between a typo and an empty result.
		receipt.Missing = nil
		receipt.MissingCount = len(items)
		return preds, receipt, nil
	}
	return preds, receipt, nil
}

// predRowNewer is the selection order, spelled out so two exports from the same
// database cannot disagree: newest run first, then the highest attempt, then the run
// id (a deterministic tiebreak for runs created inside the same second).
func predRowNewer(a, b predRow) bool {
	if !a.RunCreatedAt.Equal(b.RunCreatedAt) {
		return a.RunCreatedAt.After(b.RunCreatedAt)
	}
	if a.Attempt != b.Attempt {
		return a.Attempt > b.Attempt
	}
	return a.RunID > b.RunID
}

// predRunUsable keeps half-finished runs out of an experiment: `queued`/`running`
// wrote partial rows, and `failed`/`cancelled` runs wrote rows their own status
// already disowned.
func predRunUsable(status string) bool {
	switch status {
	case model.ReportAssuranceStatusSucceeded, model.ReportAssuranceStatusDegraded:
		return true
	}
	return false
}

// validatePredRow transcribes one row's contract. It returns "" when the row can be
// written as-is, otherwise one named problem. Every message carries the run id: when a
// revision was evaluated twice, "which run wrote this" is the first thing to know.
func validatePredRow(itemID string, r predRow, method string) string {
	const who = "%s: run %s: "
	if r.Method != method {
		return fmt.Sprintf(who+"wrote method %q while --method is %q; "+
			"a predictions file belongs to one method", itemID, r.RunID, r.Method, method)
	}
	if !containsStr(model.ReportVerdicts(), r.Verdict) {
		return fmt.Sprintf(who+"verdict %q is not in the runtime vocabulary (doc/301 §5.2)",
			itemID, r.RunID, r.Verdict)
	}
	if r.AbstainReason != "" && !containsStr(model.ReportAbstainReasons(), r.AbstainReason) {
		return fmt.Sprintf(who+"abstain_reason %q is not in the closed set %s",
			itemID, r.RunID, r.AbstainReason, strings.Join(model.ReportAbstainReasons(), "/"))
	}
	if r.NotEvalReason != "" && !containsStr(model.ReportNotEvaluatedReasons(), r.NotEvalReason) {
		return fmt.Sprintf(who+"not_evaluated_reason %q is not in the closed set %s",
			itemID, r.RunID, r.NotEvalReason, strings.Join(model.ReportNotEvaluatedReasons(), "/"))
	}
	if r.Verdict == model.ReportVerdictAbstained && r.AbstainReason == "" {
		return fmt.Sprintf(who+"verdict is abstained but abstain_reason is empty; "+
			"§4.2's four abstain rows would all report zero", itemID, r.RunID)
	}
	if r.Verdict != model.ReportVerdictAbstained && r.AbstainReason != "" {
		return fmt.Sprintf(who+"abstain_reason %q on verdict %q; a reason for refusing to "+
			"conclude cannot belong to a row that concluded", itemID, r.RunID, r.AbstainReason, r.Verdict)
	}
	if r.Verdict == model.ReportVerdictNotEvaluated && r.NotEvalReason == "" {
		return fmt.Sprintf(who+"verdict is not_evaluated but not_evaluated_reason is empty; "+
			"budget_exhausted / policy_skipped / not_triggered must stay distinguishable", itemID, r.RunID)
	}
	if r.Verdict != model.ReportVerdictNotEvaluated && r.NotEvalReason != "" {
		return fmt.Sprintf(who+"not_evaluated_reason %q on verdict %q",
			itemID, r.RunID, r.NotEvalReason, r.Verdict)
	}
	// `supported`/`partially_supported` require a quote *status*, not just a quote:
	// §4.2's quote_verified_rate is the row that catches a model inventing text, and
	// an empty column would make that row fail for a pipeline reason while the
	// report reads as a model failure.
	if (r.Verdict == model.ReportVerdictSupported || r.Verdict == model.ReportVerdictPartiallySupported) &&
		r.QuoteStatus == "" {
		return fmt.Sprintf(who+"verdict %q with an empty quote_status; the mechanical quote layer "+
			"never ran for this row, and exporting `not_provided` would turn a pipeline gap into "+
			"a quote_verified_rate failure", itemID, r.RunID, r.Verdict)
	}
	if r.QuoteStatus != "" && !containsStr(model.ReportQuoteStatuses(), r.QuoteStatus) {
		return fmt.Sprintf(who+"quote_status %q is not in the closed set %s",
			itemID, r.RunID, r.QuoteStatus, strings.Join(model.ReportQuoteStatuses(), "/"))
	}
	return ""
}

// evalRowsForRevisions reads every evaluation row of the given revisions for one
// method and one run mode, joined with its run. The `any(string_to_array(...))` form
// rather than a driver array keeps the query portable across the two DSN shapes this
// tool is used with (local psql and the 151 compose stack).
func evalRowsForRevisions(ctx context.Context, tx *sql.Tx, revisions []string, method, mode string) ([]predRow, error) {
	if len(revisions) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `
select e.run_id, r.status, r.mode, r.created_at, e.method, coalesce(e.method_version, ''),
       coalesce(e.model_id, ''), e.revision_id, e.claim_id, e.attempt, e.support_verdict,
       coalesce(e.quote_status, ''), coalesce(e.abstain_reason, ''),
       coalesce(e.not_evaluated_reason, ''), e.cache_hit
from rgx_report_claim_evaluation e
join rgx_report_assurance_run r on r.id = e.run_id
where e.revision_id = any(string_to_array($1, ','))
  and e.method = $2
  and r.mode = $3
order by e.revision_id, e.claim_id, r.created_at, e.attempt, e.run_id`,
		strings.Join(revisions, ","), method, mode)
	if err != nil {
		return nil, fmt.Errorf("claim evaluations: %w", err)
	}
	defer rows.Close()

	var out []predRow
	for rows.Next() {
		var r predRow
		if err := rows.Scan(&r.RunID, &r.RunStatus, &r.RunMode, &r.RunCreatedAt, &r.Method,
			&r.MethodVersion, &r.ModelID, &r.RevisionID, &r.ClaimID, &r.Attempt, &r.Verdict,
			&r.QuoteStatus, &r.AbstainReason, &r.NotEvalReason, &r.CacheHit); err != nil {
			return nil, fmt.Errorf("scan claim evaluation: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// methodsSeen answers "what does the database hold" when the export found nothing:
// an empty file caused by a typo in --method must look different from an empty file
// caused by a revision that was never evaluated.
func methodsSeen(ctx context.Context, tx *sql.Tx, revisions []string, mode string) (map[string]int, error) {
	out := map[string]int{}
	if len(revisions) == 0 {
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `
select e.method, count(*)
from rgx_report_claim_evaluation e
join rgx_report_assurance_run r on r.id = e.run_id
where e.revision_id = any(string_to_array($1, ','))
  and r.mode = $2
group by e.method order by e.method`,
		strings.Join(revisions, ","), mode)
	if err != nil {
		return nil, fmt.Errorf("methods seen: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			return nil, fmt.Errorf("scan method: %w", err)
		}
		out[m] = n
	}
	return out, rows.Err()
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
