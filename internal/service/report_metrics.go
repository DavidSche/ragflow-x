package service

// A15 — the report quality trend (doc/288 §11 / §14.1).
//
// Every terminal revision already stores its verdict in `metrics_json` (§14.2:
// "可回归的产品行为，不是开发者日志"). A15 is the read that turns those stored
// verdicts into a series, so "is the quality gate getting worse?" is a question with
// an answer instead of an anecdote from the last drill.
//
// Three decisions shape this file:
//
//  1. **Ratios are `null` when their denominator is empty, never 0.** §14.1's
//     denominator discipline exists for the same reason: a stored `0` and a stored
//     "never measured" must not be the same number, or the first trend chart drawn
//     from this endpoint reports a quality collapse that never happened (the live
//     revision that motivated the quality-caliber fix stored exactly that).
//  2. **Only delivered revisions carry the quality ratios.** `published` and
//     `superseded` describe a report somebody could read; a `failed` revision has a
//     structure score and nothing else, and a `cancelled` one has no sections at all.
//     Counting them in the citation/quality means would make one crashed run look
//     like a workspace-wide quality drop. They are still counted, in their own
//     columns.
//  3. **Only buckets that exist are emitted.** A day with no reports is an absence,
//     not a zero: padding the series would draw a dip that never happened.
//
// View: the default is the caller's own reports. A workspace-wide view needs
// `manage:report` (tenant_admin and above) — the aggregate is small, but "how many
// reports did my colleagues run" is still somebody else's activity, and P0-A's read
// policy is owner-only (§7.6).
//
// The parameter is called `view`, not `scope`, because `scope` already means
// something else platform-wide: on every GET the RBAC middleware reads
// `scope=current|specific|all` as the tenant scope of a governance read and
// answers a bare 400 for anything else (middleware/rbac.go `scopeValueMatches`).
// A second meaning for the same word on one route is exactly the class of defect
// this feature keeps having to unwind, so the trend gets its own word.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Report metrics trend bounds. The window is a product choice, not a paging one:
// one year of revisions is more than any quality discussion needs, and 180 days
// keeps the row read bounded without a hidden truncation.
const (
	reportMetricsTrendDefaultDays = 30
	reportMetricsTrendMaxDays     = 180
	// reportMetricsRowLimit mirrors the repository read cap, so `truncated` can be
	// decided without asking the store how many rows it would have returned.
	reportMetricsRowLimit = 5000
)

// ReportMetricsTrendRequest is the A15 query surface.
type ReportMetricsTrendRequest struct {
	Days        int
	Bucket      string
	View        string
	TemplateKey string
}

// ReportMetricsTotals is one bucket (or the whole window) of the trend.
//
// The counts and the ratios answer different questions and are kept apart on
// purpose: `Revisions` says how much work happened, `QualityGatePassRate` says how
// it went, and `MetricsMissing` says how much of the window the ratios do not cover.
type ReportMetricsTotals struct {
	Revisions  int `json:"revisions"`
	Published  int `json:"published"`
	Partial    int `json:"partial"`
	Superseded int `json:"superseded"`
	Failed     int `json:"failed"`
	Cancelled  int `json:"cancelled"`
	// MetricsMissing counts terminal revisions whose `metrics_json` could not be
	// read. They are counted, never silently dropped: a trend that hides the rows it
	// could not parse reports a cleaner workspace than the one that exists.
	MetricsMissing int `json:"metrics_missing"`
	// StructureCompleteness averages §14.1's structural ratio over delivered
	// revisions; CitationCoverage likewise.
	StructureCompleteness *float64 `json:"structure_completeness"`
	CitationCoverage      *float64 `json:"citation_coverage"`
	// QualityJudged / QualityPassed / QualityNeedsReview / QualityNotChecked are the
	// §14.2 gate counts, summed from the stored per-revision metrics.
	QualityJudged       int      `json:"quality_judged"`
	QualityPassed       int      `json:"quality_passed"`
	QualityNeedsReview  int      `json:"quality_needs_review"`
	QualityNotChecked   int      `json:"quality_not_checked"`
	QualityGatePassRate *float64 `json:"quality_gate_pass_rate"`
	// DegradedRevisions counts revisions whose stored `quality_flag=degraded`
	// (citation coverage below the floor, §14.2). It is a count, not a ratio, so a
	// single flagged revision is visible on a quiet day.
	DegradedRevisions int `json:"degraded_revisions"`
}

// ReportMetricsBucket is one day or week of the series.
type ReportMetricsBucket struct {
	// Date is the bucket start in UTC: `2026-11-18` for a day, the Monday of the
	// week (`2026-11-16`) for a week. A start date is a stable key and cannot be
	// misread as a label; presentation decides how to print it.
	Date string `json:"date"`
	ReportMetricsTotals
}

// ReportMetricsTrendView is the A15 answer.
type ReportMetricsTrendView struct {
	// From is inclusive, To exclusive, both UTC — the same half-open window the
	// query used, so a caller can page forward without re-deriving it.
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Days        int       `json:"days"`
	Bucket      string    `json:"bucket"`
	View        string    `json:"view"`
	TemplateKey string    `json:"template_key,omitempty"`
	// Truncated is true when the window held more terminal revisions than the read
	// cap. The numbers then describe the newest `Revisions` rows and say so, instead
	// of quietly averaging a subset.
	Truncated bool                  `json:"truncated"`
	Totals    ReportMetricsTotals   `json:"totals"`
	Buckets   []ReportMetricsBucket `json:"buckets"`
}

// ReportMetricsTrend answers A15: the stored quality verdicts of a window, rolled up
// per day or week.
func (s *Service) ReportMetricsTrend(
	ctx context.Context, actorID, tenantID string, request ReportMetricsTrendRequest,
) (*ReportMetricsTrendView, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	days := request.Days
	if days == 0 {
		days = reportMetricsTrendDefaultDays
	}
	if days < 1 || days > reportMetricsTrendMaxDays {
		return nil, httperr.New(400, codeReportOutlineInvalid,
			"days must be between 1 and 180")
	}
	bucket := strings.ToLower(strings.TrimSpace(request.Bucket))
	if bucket == "" {
		bucket = "day"
	}
	if bucket != "day" && bucket != "week" {
		return nil, httperr.New(400, codeReportOutlineInvalid, "bucket must be day or week")
	}
	view := strings.ToLower(strings.TrimSpace(request.View))
	if view == "" {
		view = "own"
	}
	if view != "own" && view != "tenant" {
		return nil, httperr.New(400, codeReportOutlineInvalid, "view must be own or tenant")
	}
	templateKey := strings.ToLower(strings.TrimSpace(request.TemplateKey))
	if templateKey != "" && !templateKeyPattern.MatchString(templateKey) {
		return nil, httperr.New(400, codeReportOutlineInvalid, "template_key is not a valid key")
	}
	ownerUserID := actorID
	if view == "tenant" {
		// Workspace-wide rollups are governance, not read: they describe colleagues'
		// runs. Same rule as the retention write path.
		if err := s.AuthorizeObject(ctx, actorID, "manage", "report", tenantID, ""); err != nil {
			return nil, err
		}
		ownerUserID = ""
	}

	// The window ends at the end of the current UTC day, so "last 7 days" includes
	// today's reports instead of stopping at midnight.
	now := time.Now().UTC()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -days)

	revisions, err := s.Store.ListReportRevisionsForMetrics(ctx, repository.ReportMetricsQuery{
		TenantID: tenantID, OwnerUserID: ownerUserID,
		From: from, To: to, TemplateKey: templateKey,
	})
	if err != nil {
		return nil, err
	}

	result := &ReportMetricsTrendView{
		From: from, To: to, Days: days, Bucket: bucket, View: view,
		TemplateKey: templateKey,
		Buckets:     []ReportMetricsBucket{},
		Truncated:   len(revisions) >= reportMetricsRowLimit,
	}
	accumulators := map[string]*reportMetricsAccumulator{}
	order := make([]string, 0, len(revisions))
	for index := range revisions {
		revision := &revisions[index]
		key := reportMetricsBucketKey(revision.CreatedAt, bucket)
		accumulator, ok := accumulators[key]
		if !ok {
			accumulator = &reportMetricsAccumulator{}
			accumulators[key] = accumulator
			order = append(order, key)
		}
		accumulateReportMetric(accumulator, revision)
	}
	for _, key := range order {
		result.Buckets = append(result.Buckets, ReportMetricsBucket{
			Date: key, ReportMetricsTotals: accumulators[key].view(),
		})
	}

	total := &reportMetricsAccumulator{}
	for index := range revisions {
		accumulateReportMetric(total, &revisions[index])
	}
	// `view()` (not `totals`) so the window-level ratios are derived the same way the
	// per-bucket ones are; assigning the raw accumulator would answer counts with a
	// null rate at the top level and a real rate inside the buckets.
	result.Totals = total.view()
	return result, nil
}

// reportMetricsAccumulator sums one bucket, keeping each ratio's own denominator.
// Averaging the stored per-revision ratios (equal weight per revision) is a choice:
// weighting by section count would let one large template dominate a workspace
// trend, and the question A15 answers is "how did our reports do", not "how did our
// sections do".
type reportMetricsAccumulator struct {
	structureSum   float64
	structureCount int
	citationSum    float64
	citationCount  int
	judged         int
	passed         int
	needsReview    int
	totals         ReportMetricsTotals
}

// accumulateReportMetric folds one revision into an accumulator and reports whether
// its metrics were readable.
func accumulateReportMetric(acc *reportMetricsAccumulator, revision *model.ReportRevision) bool {
	acc.totals.Revisions++
	switch revision.Status {
	case model.ReportRevisionPublished:
		acc.totals.Published++
		if revision.Completeness == model.ReportCompletenessPartial {
			acc.totals.Partial++
		}
	case model.ReportRevisionSuperseded:
		acc.totals.Superseded++
		if revision.Completeness == model.ReportCompletenessPartial {
			acc.totals.Partial++
		}
	case model.ReportRevisionFailed:
		acc.totals.Failed++
	case model.ReportRevisionCancelled:
		acc.totals.Cancelled++
	}

	var metrics reportQualityMetrics
	if revision.MetricsJSON == "" {
		acc.totals.MetricsMissing++
		return false
	}
	if err := json.Unmarshal([]byte(revision.MetricsJSON), &metrics); err != nil {
		acc.totals.MetricsMissing++
		return false
	}
	// Ratios and the gate verdicts describe delivered reports only; see the file
	// header for why a failed or cancelled revision must not move them.
	delivered := revision.Status == model.ReportRevisionPublished ||
		revision.Status == model.ReportRevisionSuperseded
	if delivered {
		acc.structureSum += metrics.StructureCompleteness
		acc.structureCount++
		acc.citationSum += metrics.CitationCoverage
		acc.citationCount++
		acc.judged += metrics.QualityJudged
		acc.passed += metrics.QualityPassed
		acc.needsReview += metrics.QualityNeedsReview
		acc.totals.QualityNotChecked += metrics.QualityNotChecked
		if metrics.QualityFlag == model.ReportQualityFlagDegraded {
			acc.totals.DegradedRevisions++
		}
	}
	return true
}

// view materializes the accumulated numbers, leaving ratios nil when nothing fed
// their denominator.
func (acc *reportMetricsAccumulator) view() ReportMetricsTotals {
	totals := acc.totals
	totals.QualityJudged = acc.judged
	totals.QualityPassed = acc.passed
	totals.QualityNeedsReview = acc.needsReview
	if acc.structureCount > 0 {
		value := roundRatio(acc.structureSum / float64(acc.structureCount))
		totals.StructureCompleteness = &value
	}
	if acc.citationCount > 0 {
		value := roundRatio(acc.citationSum / float64(acc.citationCount))
		totals.CitationCoverage = &value
	}
	if acc.judged > 0 {
		value := roundRatio(float64(acc.passed) / float64(acc.judged))
		totals.QualityGatePassRate = &value
	}
	return totals
}

// reportMetricsBucketKey names the bucket a revision falls into, in UTC.
func reportMetricsBucketKey(at time.Time, bucket string) string {
	utc := at.UTC()
	if bucket == "week" {
		// Monday-based weeks: ISO-8601's convention, and the one an operations
		// calendar uses. Sunday would silently move every weekend report into the
		// previous week's bucket.
		offset := (int(utc.Weekday()) + 6) % 7
		utc = utc.AddDate(0, 0, -offset)
	}
	return utc.Format("2006-01-02")
}
