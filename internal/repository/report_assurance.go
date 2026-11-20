package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The assurance store (doc/301 §6.1). Five shapes are deliberate:
//
//  1. **Every read and write is tenant-scoped.** A run id, finding id or revision
//     id is a user-supplied string; loading one without the tenant is an
//     enumeration primitive. Missing and cross-tenant both answer "not found",
//     which is what keeps 44120 from leaking existence.
//  2. **Evaluation rows are insert-only.** There is no `UpdateClaimEvaluation`; a
//     re-run writes new rows. A verdict is a measurement, and a measurement that
//     can be edited after the fact is not evidence (invariant 2).
//  3. **`release_eligibility` is written by one call and never by a request
//     handler.** `FinishAssuranceRun` takes the tier as a parameter and refuses to
//     overwrite a terminal run, so there is exactly one writer per run (G-A8).
//  4. **The revision columns are a projection.** They are updated by
//     `ProjectAssuranceOnRevision` only, and losing them costs a rebuild, never a
//     fact.
//  5. **Candidate reads never trust the frozen ACL** — they return the rows and let
//     the service re-check the caller against the *current* ACL (§5.3.1). The
//     repository cannot do that check because it does not know the session.
type ReportAssuranceRepo interface {
	// CreateAssuranceRun inserts a run. The unique index on the idempotency key is
	// what turns a double click into one run; callers translate the violation into
	// 44118 or the original run's id.
	CreateAssuranceRun(ctx context.Context, run *model.ReportAssuranceRun) error
	GetAssuranceRun(ctx context.Context, tenantID, runID string) (*model.ReportAssuranceRun, error)
	// FindAssuranceRunByIdempotencyKey resolves a retried trigger to the run it
	// already created.
	FindAssuranceRunByIdempotencyKey(ctx context.Context, tenantID, key string) (*model.ReportAssuranceRun, error)
	// FindActiveAssuranceRun is the 44118 business mutex: one queued/running run per
	// revision. Without it two runs would evaluate the same claims concurrently and
	// both write, which is exactly the race a unique `cache_key` cannot prevent.
	FindActiveAssuranceRun(ctx context.Context, tenantID, revisionID string) (*model.ReportAssuranceRun, error)
	ListAssuranceRuns(ctx context.Context, tenantID, reportID, revisionID string, limit int) ([]model.ReportAssuranceRun, error)
	// ListStaleAssuranceRuns returns the non-terminal runs that stopped making
	// progress before `staleBefore` (doc/301 §6.6). `queued` rows are re-armed and
	// `running` rows are closed as failed by the reconciler: a stuck `running` row
	// holds the 44118 mutex for its revision forever.
	ListStaleAssuranceRuns(ctx context.Context, staleBefore time.Time) ([]model.ReportAssuranceRun, error)
	// StartAssuranceRun moves queued → running (CAS). `applied=false` means another
	// worker won, not an error.
	StartAssuranceRun(ctx context.Context, tenantID, runID string, now time.Time) (bool, error)
	// FinishAssuranceRun writes the terminal state and is the ONLY writer of
	// `release_eligibility`. It refuses to touch an already-terminal run, so a
	// retried worker cannot overwrite a published conclusion.
	FinishAssuranceRun(ctx context.Context, finish ReportAssuranceFinish) (bool, error)
	// UpdateAssuranceRunProgress persists running counters so the workbench can show
	// "evaluated 40/131" without waiting for the end.
	UpdateAssuranceRunProgress(ctx context.Context, tenantID, runID string, usedTokens int64, counters ReportAssuranceCounters) error

	// InsertClaimEvaluations appends one batch. Duplicate `(run_id, claim_id,
	// attempt)` rows are skipped, which makes a retried batch safe.
	InsertClaimEvaluations(ctx context.Context, rows []model.ReportClaimEvaluation) error
	ListClaimEvaluations(ctx context.Context, tenantID, runID string) ([]model.ReportClaimEvaluation, error)
	ListClaimEvaluationsByClaim(ctx context.Context, tenantID, claimID string, limit int) ([]model.ReportClaimEvaluation, error)
	// FindCachedEvaluation looks up a previous verdict for one `cache_key`. It is the
	// explicit cache read that replaces a unique index: same input, same verdict, no
	// second model call (§6.1).
	FindCachedEvaluation(ctx context.Context, tenantID, cacheKey string) (*model.ReportClaimEvaluation, error)

	// Findings.
	CreateReviewFinding(ctx context.Context, finding *model.ReportReviewFinding) error
	// CreateReviewFindings inserts a generated batch idempotently: a retried stage
	// must not double the finding list.
	CreateReviewFindings(ctx context.Context, findings []model.ReportReviewFinding) error
	GetReviewFinding(ctx context.Context, tenantID, findingID string) (*model.ReportReviewFinding, error)
	ListReviewFindings(ctx context.Context, tenantID string, filter ReviewFindingFilter) ([]model.ReportReviewFinding, error)
	CountReviewFindings(ctx context.Context, tenantID string, filter ReviewFindingFilter) (int64, error)
	// UpdateReviewFindingStatus is the only writer of `status`/`reviewed_*`. It is a
	// CAS on the expected previous status, so two reviewers cannot both "resolve" it.
	UpdateReviewFindingStatus(ctx context.Context, update ReviewFindingUpdate) (bool, error)

	// Conflict candidates (generation-time frozen, §5.3.1).
	InsertConflictCandidates(ctx context.Context, rows []model.ReportConflictCandidate) error
	ListConflictCandidates(ctx context.Context, tenantID, revisionID string, sectionOrd int) ([]model.ReportConflictCandidate, error)
	// ListConflictCandidatesByRevision reads the frozen pool of a whole revision in
	// one query, ordered by section then pool rank. It exists for the workbench, which
	// draws candidates beside their section: one query per returned section would make
	// the read cost grow with the report instead of with the pool.
	ListConflictCandidatesByRevision(ctx context.Context, tenantID, revisionID string, limit int) ([]model.ReportConflictCandidate, error)
	// CountConflictCandidates reports the pool size without reading content, which is
	// what a caller without dataset read permission is allowed to see (§5.3.1).
	CountConflictCandidates(ctx context.Context, tenantID, revisionID string) (int64, error)

	// ProjectAssuranceOnRevision rebuilds the four projection columns from a finished
	// run. Only this method writes them.
	ProjectAssuranceOnRevision(ctx context.Context, project AssuranceProjection) error
	// SetRevisionAssuranceAck records the explicit "export anyway" acknowledgement
	// on the revision (doc/301 §7.2). It is a single-column write and it is *not*
	// part of the projection: the projection may be overwritten by a later run,
	// while the acknowledgement is a human decision that a later run must invalidate
	// by disagreeing with it, not by erasing it.
	SetRevisionAssuranceAck(ctx context.Context, tenantID, revisionID, ackJSON string) error
	// SumAssuranceSettledTokens and ListAssuranceQuotaBuckets feed the monthly
	// allowance reconciliation (doc/301 §6.5 / T25): the bucket is the ledger of
	// record, the runs are the ledger of claims, and the drift between them is the
	// only evidence that per-call reservation actually held.
	SumAssuranceSettledTokens(ctx context.Context, tenantID, period string) (settled, runs int64, err error)
	ListAssuranceQuotaBuckets(ctx context.Context, dimension, period string) ([]model.QuotaBucket, error)
}

// ReportAssuranceFinish is the terminal write of one run.
type ReportAssuranceFinish struct {
	TenantID string
	RunID    string
	Status   string
	// ReleaseEligibility must come from `reportAssuranceEligibility`; the repository
	// does not validate the value because it must not know the policy.
	ReleaseEligibility string
	MetricsJSON        string
	Counters           ReportAssuranceCounters
	UsedTokens         int64
	BudgetExhausted    bool
	FailureCode        string
	FailureReason      string
	QuotaPeriod        string
	QuotaSettledTokens int64
	Now                time.Time
}

// ReportAssuranceCounters is the per-verdict tally written on progress and finish.
// It is a struct rather than a map so a missing counter is a compile error, not a
// silently absent column.
type ReportAssuranceCounters struct {
	Evaluated           int
	Supported           int
	Partially           int
	Unsupported         int
	Conflicting         int
	Outdated            int
	Insufficient        int
	Abstained           int
	NotEvaluated        int
	NotRequired         int
	AbstainedUncertain  int
	AbstainedInvalid    int
	AbstainedNotFound   int
	AbstainedNoQuote    int
	NotEvaluatedBudget  int
	NotEvaluatedPolicy  int
	NotEvaluatedTrigger int
	StructurePass       int
	StructureFail       int
}

// AssuranceProjection is the revision-side projection (§6.2).
type AssuranceProjection struct {
	TenantID           string
	RevisionID         string
	RunID              string
	Status             string
	SummaryJSON        string
	ReleaseEligibility string
}

// SetRevisionAssuranceAck writes one column on one revision.
func (s *store) SetRevisionAssuranceAck(ctx context.Context, tenantID, revisionID, ackJSON string) error {
	return s.WithContext(ctx).
		Model(&model.ReportRevision{}).
		Where("tenant_id = ? AND id = ?", tenantID, revisionID).
		Update("assurance_ack_json", ackJSON).Error
}

// ReviewFindingFilter narrows a finding list. Empty fields mean "no filter" — the
// workbench always wants a section and a status, but the export and the metrics
// scan want everything.
type ReviewFindingFilter struct {
	ReportID   string
	RevisionID string
	RunID      string
	ClaimID    string
	SectionOrd *int
	Statuses   []string
	Severities []string
	Categories []string
	Limit      int
	Offset     int
}

// ReviewFindingUpdate is one status transition, expressed as a CAS.
type ReviewFindingUpdate struct {
	TenantID           string
	FindingID          string
	FromStatus         string
	ToStatus           string
	ReviewedBy         string
	ReviewNote         string
	ResolvedRevisionID *string
	Now                time.Time
}

func (s *store) CreateAssuranceRun(ctx context.Context, run *model.ReportAssuranceRun) error {
	return s.DB.WithContext(ctx).Create(run).Error
}

func (s *store) GetAssuranceRun(ctx context.Context, tenantID, runID string) (*model.ReportAssuranceRun, error) {
	var run model.ReportAssuranceRun
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, runID).
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *store) FindAssuranceRunByIdempotencyKey(ctx context.Context, tenantID, key string) (*model.ReportAssuranceRun, error) {
	var run model.ReportAssuranceRun
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND idempotency_key = ?", tenantID, key).
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *store) FindActiveAssuranceRun(ctx context.Context, tenantID, revisionID string) (*model.ReportAssuranceRun, error) {
	var run model.ReportAssuranceRun
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND revision_id = ? AND status IN ?", tenantID, revisionID,
			[]string{model.ReportAssuranceStatusQueued, model.ReportAssuranceStatusRunning}).
		Order("created_at DESC").
		First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// ListStaleAssuranceRuns is the reconciler's read. It is not tenant-scoped: the
// reconciler is a background sweep over the whole installation, and it selects only
// the columns the two closes need.
func (s *store) ListStaleAssuranceRuns(ctx context.Context, staleBefore time.Time) ([]model.ReportAssuranceRun, error) {
	var rows []model.ReportAssuranceRun
	err := s.WithContext(ctx).
		Where("status IN ?", []string{
			model.ReportAssuranceStatusQueued, model.ReportAssuranceStatusRunning}).
		Where("COALESCE(started_at, created_at) < ?", staleBefore).
		Order("created_at ASC").
		Limit(200).
		Find(&rows).Error
	return rows, err
}

func (s *store) ListAssuranceRuns(ctx context.Context, tenantID, reportID, revisionID string, limit int) ([]model.ReportAssuranceRun, error) {
	query := s.DB.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if reportID != "" {
		query = query.Where("report_id = ?", reportID)
	}
	if revisionID != "" {
		query = query.Where("revision_id = ?", revisionID)
	}
	if limit <= 0 || limit > maxPageSize {
		limit = defaultPageSize
	}
	var runs []model.ReportAssuranceRun
	if err := query.Order("created_at DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *store) StartAssuranceRun(ctx context.Context, tenantID, runID string, now time.Time) (bool, error) {
	result := s.DB.WithContext(ctx).Model(&model.ReportAssuranceRun{}).
		Where("tenant_id = ? AND id = ? AND status = ?", tenantID, runID, model.ReportAssuranceStatusQueued).
		Updates(map[string]any{"status": model.ReportAssuranceStatusRunning, "started_at": now})
	return result.RowsAffected > 0, result.Error
}

func (s *store) FinishAssuranceRun(ctx context.Context, finish ReportAssuranceFinish) (bool, error) {
	// The `status NOT IN (terminal)` guard is the one-writer rule: a retried worker
	// whose run was already finished (or cancelled by an operator) gets
	// `applied=false` and must not overwrite the recorded conclusion.
	result := s.DB.WithContext(ctx).Model(&model.ReportAssuranceRun{}).
		Where("tenant_id = ? AND id = ? AND status IN ?", finish.TenantID, finish.RunID,
			[]string{model.ReportAssuranceStatusQueued, model.ReportAssuranceStatusRunning}).
		Updates(map[string]any{
			"status":                    finish.Status,
			"release_eligibility":       finish.ReleaseEligibility,
			"metrics_json":              finish.MetricsJSON,
			"evaluated":                 finish.Counters.Evaluated,
			"supported":                 finish.Counters.Supported,
			"partially":                 finish.Counters.Partially,
			"unsupported":               finish.Counters.Unsupported,
			"conflicting":               finish.Counters.Conflicting,
			"outdated":                  finish.Counters.Outdated,
			"insufficient":              finish.Counters.Insufficient,
			"abstained":                 finish.Counters.Abstained,
			"not_evaluated":             finish.Counters.NotEvaluated,
			"not_required":              finish.Counters.NotRequired,
			"abstained_model_uncertain": finish.Counters.AbstainedUncertain,
			"abstained_input_invalid":   finish.Counters.AbstainedInvalid,
			"abstained_quote_not_found": finish.Counters.AbstainedNotFound,
			"abstained_no_quote":        finish.Counters.AbstainedNoQuote,
			"not_evaluated_budget":      finish.Counters.NotEvaluatedBudget,
			"not_evaluated_policy":      finish.Counters.NotEvaluatedPolicy,
			"not_evaluated_trigger":     finish.Counters.NotEvaluatedTrigger,
			"structure_pass":            finish.Counters.StructurePass,
			"structure_fail":            finish.Counters.StructureFail,
			"used_tokens":               finish.UsedTokens,
			"budget_exhausted":          finish.BudgetExhausted,
			"failure_code":              finish.FailureCode,
			"failure_reason":            finish.FailureReason,
			"quota_period":              finish.QuotaPeriod,
			"quota_settled_tokens":      finish.QuotaSettledTokens,
			"finished_at":               finish.Now,
		})
	return result.RowsAffected > 0, result.Error
}

func (s *store) UpdateAssuranceRunProgress(ctx context.Context, tenantID, runID string, usedTokens int64, counters ReportAssuranceCounters) error {
	result := s.DB.WithContext(ctx).Model(&model.ReportAssuranceRun{}).
		Where("tenant_id = ? AND id = ? AND status = ?", tenantID, runID, model.ReportAssuranceStatusRunning).
		Updates(map[string]any{
			"used_tokens":               usedTokens,
			"evaluated":                 counters.Evaluated,
			"supported":                 counters.Supported,
			"partially":                 counters.Partially,
			"unsupported":               counters.Unsupported,
			"conflicting":               counters.Conflicting,
			"outdated":                  counters.Outdated,
			"insufficient":              counters.Insufficient,
			"abstained":                 counters.Abstained,
			"not_evaluated":             counters.NotEvaluated,
			"not_required":              counters.NotRequired,
			"abstained_model_uncertain": counters.AbstainedUncertain,
			"abstained_input_invalid":   counters.AbstainedInvalid,
			"abstained_quote_not_found": counters.AbstainedNotFound,
			"abstained_no_quote":        counters.AbstainedNoQuote,
			"not_evaluated_budget":      counters.NotEvaluatedBudget,
			"not_evaluated_policy":      counters.NotEvaluatedPolicy,
			"not_evaluated_trigger":     counters.NotEvaluatedTrigger,
			"structure_pass":            counters.StructurePass,
			"structure_fail":            counters.StructureFail,
		})
	return result.Error
}

func (s *store) InsertClaimEvaluations(ctx context.Context, rows []model.ReportClaimEvaluation) error {
	if len(rows) == 0 {
		return nil
	}
	// DoNothing on conflict: the unique `(run_id, claim_id, attempt)` is the retry
	// guard. The alternative — upsert — would let a second write replace a recorded
	// verdict, which invariant 2 forbids.
	return s.DB.WithContext(ctx).
		Clauses(onConflictDoNothing()).
		CreateInBatches(rows, 100).Error
}

func (s *store) ListClaimEvaluations(ctx context.Context, tenantID, runID string) ([]model.ReportClaimEvaluation, error) {
	var rows []model.ReportClaimEvaluation
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Order("section_ord ASC, created_at ASC").
		Find(&rows).Error
	return rows, err
}

func (s *store) ListClaimEvaluationsByClaim(ctx context.Context, tenantID, claimID string, limit int) ([]model.ReportClaimEvaluation, error) {
	if limit <= 0 || limit > maxPageSize {
		limit = defaultPageSize
	}
	var rows []model.ReportClaimEvaluation
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND claim_id = ?", tenantID, claimID).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (s *store) FindCachedEvaluation(ctx context.Context, tenantID, cacheKey string) (*model.ReportClaimEvaluation, error) {
	if cacheKey == "" {
		return nil, nil
	}
	var row model.ReportClaimEvaluation
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND cache_key = ? AND cache_hit = ?", tenantID, cacheKey, false).
		Order("created_at DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *store) CreateReviewFinding(ctx context.Context, finding *model.ReportReviewFinding) error {
	return s.DB.WithContext(ctx).Create(finding).Error
}

func (s *store) CreateReviewFindings(ctx context.Context, findings []model.ReportReviewFinding) error {
	if len(findings) == 0 {
		return nil
	}
	return s.DB.WithContext(ctx).CreateInBatches(findings, 100).Error
}

func (s *store) GetReviewFinding(ctx context.Context, tenantID, findingID string) (*model.ReportReviewFinding, error) {
	var finding model.ReportReviewFinding
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, findingID).
		First(&finding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &finding, nil
}

func (s *store) ListReviewFindings(ctx context.Context, tenantID string, filter ReviewFindingFilter) ([]model.ReportReviewFinding, error) {
	query := s.reviewFindingQuery(ctx, tenantID, filter)
	limit := filter.Limit
	if limit <= 0 || limit > maxPageSize {
		limit = defaultPageSize
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	var findings []model.ReportReviewFinding
	// `severity` is a text column, so ordering by it would sort `high` < `low` <
	// `medium` alphabetically — the opposite of the workbench's intent. The rank is an
	// explicit CASE rather than a generated column because migration 183 is purely
	// additive (doc/301 §6.1): a derived column can be added later without a data
	// rewrite, and this expression is identical on SQLite and PostgreSQL.
	if err := query.Order(orderReviewFindingsBySeverity).
		Limit(limit).Offset(offset).Find(&findings).Error; err != nil {
		return nil, err
	}
	return findings, nil
}

func (s *store) CountReviewFindings(ctx context.Context, tenantID string, filter ReviewFindingFilter) (int64, error) {
	var total int64
	err := s.reviewFindingQuery(ctx, tenantID, filter).
		Model(&model.ReportReviewFinding{}).Count(&total).Error
	return total, err
}

// orderReviewFindingsBySeverity is the only severity ordering in the repository.
// A second copy (in a service or a handler) is how "high findings first" ends up
// disagreeing between the page and the badge count.
const orderReviewFindingsBySeverity = "CASE severity WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END ASC, section_ord ASC, created_at ASC"

// reviewFindingQuery builds the *same* predicate for the list and the count. A
// page whose total comes from a different WHERE clause is the classic "page 3 of
// 2" defect, and a workbench with a wrong badge count is a workbench nobody trusts.
func (s *store) reviewFindingQuery(ctx context.Context, tenantID string, filter ReviewFindingFilter) *gorm.DB {
	query := s.DB.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if filter.ReportID != "" {
		query = query.Where("report_id = ?", filter.ReportID)
	}
	if filter.RevisionID != "" {
		query = query.Where("revision_id = ?", filter.RevisionID)
	}
	if filter.RunID != "" {
		query = query.Where("run_id = ?", filter.RunID)
	}
	if filter.ClaimID != "" {
		query = query.Where("claim_id = ?", filter.ClaimID)
	}
	if filter.SectionOrd != nil {
		query = query.Where("section_ord = ?", *filter.SectionOrd)
	}
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	if len(filter.Severities) > 0 {
		query = query.Where("severity IN ?", filter.Severities)
	}
	if len(filter.Categories) > 0 {
		query = query.Where("category IN ?", filter.Categories)
	}
	return query
}

func (s *store) UpdateReviewFindingStatus(ctx context.Context, update ReviewFindingUpdate) (bool, error) {
	result := s.DB.WithContext(ctx).Model(&model.ReportReviewFinding{}).
		Where("tenant_id = ? AND id = ? AND status = ?", update.TenantID, update.FindingID, update.FromStatus).
		Updates(map[string]any{
			"status":               update.ToStatus,
			"reviewed_by":          update.ReviewedBy,
			"reviewed_at":          update.Now,
			"review_note":          update.ReviewNote,
			"resolved_revision_id": update.ResolvedRevisionID,
			"updated_at":           update.Now,
		})
	return result.RowsAffected > 0, result.Error
}

func (s *store) InsertConflictCandidates(ctx context.Context, rows []model.ReportConflictCandidate) error {
	if len(rows) == 0 {
		return nil
	}
	// Idempotent per (revision, section, chunk, content hash): a retried generation
	// stage re-writes the same pool instead of doubling it.
	return s.DB.WithContext(ctx).
		Clauses(onConflictDoNothing()).
		CreateInBatches(rows, 100).Error
}

func (s *store) ListConflictCandidates(ctx context.Context, tenantID, revisionID string, sectionOrd int) ([]model.ReportConflictCandidate, error) {
	var rows []model.ReportConflictCandidate
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND revision_id = ? AND section_ord = ?", tenantID, revisionID, sectionOrd).
		Order("pool_rank ASC").
		Find(&rows).Error
	return rows, err
}

func (s *store) ListConflictCandidatesByRevision(
	ctx context.Context, tenantID, revisionID string, limit int,
) ([]model.ReportConflictCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	var rows []model.ReportConflictCandidate
	err := s.DB.WithContext(ctx).
		Where("tenant_id = ? AND revision_id = ?", tenantID, revisionID).
		Order("section_ord ASC, pool_rank ASC, chunk_id ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (s *store) CountConflictCandidates(ctx context.Context, tenantID, revisionID string) (int64, error) {
	var total int64
	err := s.DB.WithContext(ctx).Model(&model.ReportConflictCandidate{}).
		Where("tenant_id = ? AND revision_id = ?", tenantID, revisionID).
		Count(&total).Error
	return total, err
}

func (s *store) ProjectAssuranceOnRevision(ctx context.Context, project AssuranceProjection) error {
	// Only the four projection columns. Deliberately not a full-row update: the
	// revision body is immutable since publication, and a save() here would rewrite
	// columns this method has no business touching (invariant 1).
	return s.DB.WithContext(ctx).Model(&model.ReportRevision{}).
		Where("tenant_id = ? AND id = ?", project.TenantID, project.RevisionID).
		Updates(map[string]any{
			"assurance_run_id":       project.RunID,
			"assurance_status":       project.Status,
			"assurance_summary_json": project.SummaryJSON,
		}).Error
}

// onConflictDoNothing is the dialect-neutral "skip duplicates" clause. It exists so
// the two batch inserts above do not each hard-code `clause.OnConflict`; the
// assurance tables only ever conflict on their own unique indexes.
func onConflictDoNothing() clause.OnConflict {
	return clause.OnConflict{DoNothing: true}
}
