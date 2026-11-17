package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// ReportRepo is the report answer family (doc/288 §7.2). Every method that
// addresses a row by id takes the tenant and filters on it: a report id is a
// user-supplied string, so "load by id" without a tenant is an enumeration
// primitive. Missing and cross-tenant are the same answer (nil / NotFound), which
// is what keeps 44107/44108/44109 from leaking existence (doc/288 §8.7 rule 1).
type ReportRepo interface {
	// Templates (content structure, not rendering).
	CreateReportTemplate(ctx context.Context, template *model.ReportTemplate) error
	GetReportTemplate(ctx context.Context, tenantID, templateKey string, version int64) (*model.ReportTemplate, error)
	ListReportTemplates(ctx context.Context, tenantID string) ([]model.ReportTemplate, error)

	// Aggregate writes. Creation composes them inside one transaction
	// (WithinTransaction) so a report can never exist without its first
	// revision and job.
	CreateReport(ctx context.Context, report *model.Report) error
	CreateReportRevision(ctx context.Context, revision *model.ReportRevision) error
	CreateReportJob(ctx context.Context, job *model.ReportJob) error
	CreateReportJobAttempt(ctx context.Context, attempt *model.ReportJobAttempt) error
	AppendReportEvent(ctx context.Context, event *model.ReportJobEvent) (int64, error)

	// Finalize (doc/288 §8.2). These two are the ONLY writers of a terminal
	// job state and of a published revision: the runner asks, the service
	// derives, the repository applies. Nothing else may set `finalized_at`.
	BeginReportFinalization(ctx context.Context, start ReportFinalizationStart) (bool, error)
	ApplyReportFinalization(ctx context.Context, plan ReportFinalizationPlan) (bool, error)
	// RecordReportExecutionSnapshot freezes the generation conditions once.
	RecordReportExecutionSnapshot(ctx context.Context, tenantID, revisionID, snapshotJSON, hash string) (bool, error)
	// ListStaleFinalizingReportJobs finds jobs whose terminal write was requested
	// but never landed (the crash window §8.2 / A35 leaves open). The reconcile
	// worker is the second trigger; ApplyReportFinalization is what makes the
	// double trigger harmless.
	ListStaleFinalizingReportJobs(ctx context.Context, staleBefore time.Time, limit int) ([]ReportFinalizeCandidate, error)

	// Slices (doc/288 §7.9). `rgx_job` is one execution slice; the business
	// state lives in `rgx_report_job`. These two arm a slice and its
	// `rgx_report_job_attempt` mapping in the same transaction as the pointer
	// CAS, because "running means a valid slice" (I1) is a cross-table fact.
	DispatchReportSlice(ctx context.Context, start ReportSliceStart) (bool, error)
	AdvanceReportSlice(ctx context.Context, advance ReportSliceAdvance) (bool, error)
	// RequestReportJobCancel flips an active job to `cancel_requested`; it never
	// writes the terminal state (that stays finalize's job, §8.4).
	RequestReportJobCancel(ctx context.Context, tenantID, jobID string, now time.Time) (bool, error)
	// ListStaleActiveReportJobs finds jobs that are still `running` (or
	// `cancel_requested`) long after their last row update, so the reconcile
	// sweep can ask for a terminal write that nobody else will.
	ListStaleActiveReportJobs(ctx context.Context, staleBefore time.Time, limit int) ([]ReportConvergenceCandidate, error)
	// RebuildReportAttemptIfMissing restores the `rgx_report_job_attempt`
	// mapping for a business job that already points at a live runtime slice but
	// lost its mapping row (doc/288 §7.9 class 3, crash before the mapping
	// insert). It is idempotent on the `(report_job_id, attempt_seq, slice_seq)`
	// unique index: it returns `rebuilt=true` only when it actually created a row.
	RebuildReportAttemptIfMissing(ctx context.Context, rebuild ReportAttemptRebuild) (bool, error)

	// Outline + section writes (doc/288 §7.3 / §7.4 / §7.9 T3/T4). These are the
	// only writers of `outline_json`, of the frozen execution snapshot (write
	// once) and of an individual section's content: the outline parks the job in
	// `awaiting_outline` together with the section skeleton, a confirmation arms
	// a fresh attempt, and each section is started and committed under an attempt
	// CAS so a duplicate slice cannot overwrite what a live slice is writing.
	BeginReportOutline(ctx context.Context, start ReportOutlineStart) (bool, error)
	ConfirmReportOutline(ctx context.Context, confirm ReportOutlineConfirm) (bool, error)
	BeginReportSection(ctx context.Context, start ReportSectionStart) (bool, error)
	CommitReportSection(ctx context.Context, commit ReportSectionCommit) (bool, error)
	CloseReportSection(ctx context.Context, closeReport ReportSectionClose) (bool, error)
	// SkipReportSections closes the sections a run will never write (budget
	// exhausted, or a dependency that already failed). It is separate from
	// `CloseReportSection` because those rows never entered `writing`: routing them
	// through the start/close pair would record an attempt that never happened and
	// make the disclosure claim work was begun.
	SkipReportSections(ctx context.Context, skip ReportSectionSkip) (int64, error)
	// CreateReportSection inserts one immutable section row (doc/288 §7.10: any
	// content change writes a new row, never an update).
	CreateReportSection(ctx context.Context, section *model.ReportSection) error
	// SupersedeReportSection retires a `failed` section row whose version is being
	// replaced by a retried row. `applied=false` means the row already moved.
	SupersedeReportSection(ctx context.Context, sectionID string, now time.Time) (bool, error)

	// Reads.
	GetReport(ctx context.Context, tenantID, reportID string) (*model.Report, error)
	GetLatestReportJob(ctx context.Context, tenantID, reportID string) (*model.ReportJob, error)
	GetReportJob(ctx context.Context, tenantID, jobID string) (*model.ReportJob, error)
	GetReportJobByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*model.ReportJob, error)
	GetReportRevision(ctx context.Context, tenantID, reportID string, revision int64) (*model.ReportRevision, error)
	GetReportRevisionByID(ctx context.Context, tenantID, revisionID string) (*model.ReportRevision, error)
	ListReportSections(ctx context.Context, tenantID, revisionID string) ([]model.ReportSection, error)
	ListReportCitations(ctx context.Context, tenantID, revisionID string) ([]model.ReportCitation, error)
	ListReports(ctx context.Context, tenantID, ownerUserID string, limit, offset int) ([]model.Report, error)
	CountReports(ctx context.Context, tenantID, ownerUserID string) (int64, error)
	CountActiveReportJobs(ctx context.Context, tenantID, reportID string) (int64, error)
	SumReportCharges(ctx context.Context, tenantID, jobID string) (ReportChargeSums, error)

	// Charges (doc/288 §8.6). The ledger is append-only: a call inserts one
	// `reserved` row, and only a transition moves it out of that state. The job's
	// summary columns are a cache of the ledger and are refreshed from it, never
	// incremented, so a crash between the two writes heals on the next refresh.
	ReserveReportCharge(ctx context.Context, reserve ReportChargeReserve) (*model.ReportCharge, error)
	// TouchReportJob takes the job row's write lock and refreshes `updated_at`.
	// The budget gate reads "how much is still pinned", so it must run *after*
	// this lock: two concurrent sections that each read a stale total would both
	// pass the gate and together overspend the envelope (doc/288 §8.6).
	TouchReportJob(ctx context.Context, tenantID, jobID string, now time.Time) (bool, error)
	TransitionReportCharge(ctx context.Context, transition ReportChargeTransition) (bool, error)
	// RefreshReportChargeTotals rewrites the ledger-derived summary columns
	// (actual / charged / pending_reconcile) of one job from its ledger rows.
	RefreshReportChargeTotals(ctx context.Context, tenantID, jobID string, now time.Time) (ReportChargeSums, error)
	// ListStaleReservedReportCharges finds reservations whose call never came
	// back, so the sweep can write them down instead of leaking budget forever.
	ListStaleReservedReportCharges(ctx context.Context, staleBefore time.Time, limit int) ([]ReportChargeCandidate, error)
	// OrphanReportCharge writes one stale reservation down as `orphan`. It is a
	// CAS on `state = reserved`, so a call that settled first wins.
	OrphanReportCharge(ctx context.Context, candidate ReportChargeCandidate, now time.Time) (bool, error)
	ListReportEvents(ctx context.Context, tenantID, jobID string, afterSeq int64, limit int) ([]model.ReportJobEvent, error)
	// FirstReportEventSeq returns the smallest `seq` that survived the event
	// retention pass. A cursor below it cannot be resumed: the events in between
	// are gone, so answering "here are the rest" would present a partial replay
	// as a complete one (doc/288 §8.3 / A42).
	FirstReportEventSeq(ctx context.Context, tenantID, jobID string) (int64, bool, error)

	// Exports (doc/288 §7.7 / §12). One published revision can be rendered many
	// times, so the artifact version is allocated under the revision row lock and
	// the snapshot/job/artifact rows are written in that same transaction.
	CreateReportExport(ctx context.Context, rows ReportExportRows) (*model.ExportArtifact, error)
	// ReportExportArtifact loads one artifact by its own id within one tenant.
	// The caller pairs it with the export job to confirm `subject_type=report`
	// and that the artifact really belongs to the addressed revision.
	ReportExportArtifact(ctx context.Context, tenantID, artifactID string) (*model.ExportArtifact, error)
	// ListReportArtifacts returns the export history of one revision, newest
	// artifact version first. Every version is returned (pending/ready/failed/
	// expired), because the UI must show "已过期，可重新导出" and "生成失败" next
	// to a valid download; only soft-deleted rows are hidden. Deriving
	// `artifact_downloadable` from the rows stays on the service side
	// (doc/288 §7.7).
	ListReportArtifacts(ctx context.Context, tenantID, revisionID string) ([]model.ExportArtifact, error)

	// SoftDeleteReport writes the recovery window. It never extends
	// `expires_at`: the hold is a grace period on a deleted report, not a TTL
	// extension (doc/288 §7.6).
	SoftDeleteReport(ctx context.Context, tenantID, reportID string, now, holdUntil time.Time) error

	// Retention surface (doc/288 §7.6).
	ListExpiredReportIDs(ctx context.Context, now time.Time, limit int) ([]model.Report, error)
	PurgeReport(ctx context.Context, tenantID, reportID string) error
	PurgeExpiredReportEvidence(ctx context.Context, now time.Time, limit int) (map[string]int64, error)
	PurgeExpiredReportEvents(ctx context.Context, now time.Time, limit int) (map[string]int64, error)
}

// Report purge helpers (doc/288 §7.6). Three deletions are driven by the frozen
// per-row `expires_at` only: the janitor never recomputes a TTL from the live
// policy, so changing `retention.report_*` affects new rows and nothing else.
//
// Evidence snapshots are NOT deleted together with their report: they carry
// their own (never shorter) clock so "报告还在，证据就可复核" holds, and the
// janitor clears them on their own pass.

// ListExpiredReportIDs returns the reports whose report-class clock has run out
// and whose soft-delete recovery window (if any) is over. The split into
// list-then-purge keeps each transaction small and makes the per-report purge
// idempotent: a concurrent purger simply finds the children already gone.
func (s *store) ListExpiredReportIDs(ctx context.Context, now time.Time, limit int) ([]model.Report, error) {
	if limit <= 0 {
		limit = 100
	}
	var reports []model.Report
	err := s.WithContext(ctx).
		Select("id", "tenant_id").
		Where("expires_at <= ?", now).
		Where("cleanup_hold_until IS NULL OR cleanup_hold_until <= ?", now).
		Order("expires_at ASC").
		Limit(limit).
		Find(&reports).Error
	return reports, err
}

// PurgeReport deletes one report together with everything that cannot outlive
// it. Children are removed before the parent so a partially failed purge is
// retried by the next pass instead of leaving orphans behind.
//
// A report that is already gone returns gorm.ErrRecordNotFound: the janitor
// must not count a row it did not delete, otherwise the retention.purge audit
// record overstates the purge (two overlapping passes is a normal outcome on a
// 24h cadence with a manual "run now" button).
func (s *store) PurgeReport(ctx context.Context, tenantID, reportID string) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return purgeReportRows(tx, tenantID, reportID)
	})
}

func purgeReportRows(tx *gorm.DB, tenantID, reportID string) error {
	var revisionIDs []string
	if err := tx.Model(&model.ReportRevision{}).
		Where("tenant_id = ? AND report_id = ?", tenantID, reportID).
		Pluck("id", &revisionIDs).Error; err != nil {
		return err
	}
	var jobIDs []string
	if err := tx.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND report_id = ?", tenantID, reportID).
		Pluck("id", &jobIDs).Error; err != nil {
		return err
	}
	if len(revisionIDs) > 0 {
		for _, target := range []any{
			&model.ReportCitation{}, &model.ReportClaim{}, &model.ReportSection{},
		} {
			if err := tx.Where("tenant_id = ? AND revision_id IN ?", tenantID, revisionIDs).
				Delete(target).Error; err != nil {
				return err
			}
		}
	}
	if len(jobIDs) > 0 {
		for _, target := range []any{&model.ReportJobAttempt{}, &model.ReportCharge{}} {
			if err := tx.Where("tenant_id = ? AND report_job_id IN ?", tenantID, jobIDs).
				Delete(target).Error; err != nil {
				return err
			}
		}
		// Events key on job_id, not report_job_id, and are purged with the job
		// so a replay can never resurrect a deleted report.
		if err := tx.Where("tenant_id = ? AND job_id IN ?", tenantID, jobIDs).
			Delete(&model.ReportJobEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND id IN ?", tenantID, jobIDs).
			Delete(&model.ReportJob{}).Error; err != nil {
			return err
		}
	}
	if len(revisionIDs) > 0 {
		if err := tx.Where("tenant_id = ? AND id IN ?", tenantID, revisionIDs).
			Delete(&model.ReportRevision{}).Error; err != nil {
			return err
		}
	}
	result := tx.Where("tenant_id = ? AND id = ?", tenantID, reportID).
		Delete(&model.Report{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// PurgeExpiredReportEvidence deletes evidence snapshots past their own clock and
// reports the per-tenant count. Citations reference snapshots by id; the report
// page keeps rendering the frozen snippet, so deleting the snapshot only flips
// `evidence_verifiable` to false (doc/288 §7.5).
func (s *store) PurgeExpiredReportEvidence(ctx context.Context, now time.Time, limit int) (map[string]int64, error) {
	counts := map[string]int64{}
	if limit <= 0 {
		limit = 100
	}
	var snapshots []model.ReportEvidenceSnapshot
	if err := s.WithContext(ctx).
		Select("id", "tenant_id").
		Where("expires_at <= ?", now).
		Order("expires_at ASC").
		Limit(limit).
		Find(&snapshots).Error; err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return counts, nil
	}
	// Delete per tenant and count RowsAffected: a batch-wide delete would have to
	// assume every listed row was still there, and a concurrent pass would then
	// inflate the retention.purge audit.
	grouped := map[string][]string{}
	for _, snapshot := range snapshots {
		grouped[snapshot.TenantID] = append(grouped[snapshot.TenantID], snapshot.ID)
	}
	for tenantID, ids := range grouped {
		result := s.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids).
			Delete(&model.ReportEvidenceSnapshot{})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected > 0 {
			counts[tenantID] += result.RowsAffected
		}
	}
	return counts, nil
}

// PurgeExpiredReportEvents deletes replay events past their clock. The event
// stream is the only class allowed to disappear before its report, which is why
// the stream endpoint answers `reset_required` rather than pretending a gap is
// a live tail (doc/288 §8.3).
func (s *store) PurgeExpiredReportEvents(ctx context.Context, now time.Time, limit int) (map[string]int64, error) {
	counts := map[string]int64{}
	if limit <= 0 {
		limit = 100
	}
	var events []model.ReportJobEvent
	if err := s.WithContext(ctx).
		Select("id", "tenant_id").
		Where("expires_at <= ?", now).
		Order("expires_at ASC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return counts, nil
	}
	grouped := map[string][]string{}
	for _, event := range events {
		grouped[event.TenantID] = append(grouped[event.TenantID], event.ID)
	}
	for tenantID, ids := range grouped {
		result := s.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids).
			Delete(&model.ReportJobEvent{})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected > 0 {
			counts[tenantID] += result.RowsAffected
		}
	}
	return counts, nil
}

// ---------------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------------

// CreateReportTemplate inserts one immutable template version. Versions are
// never rewritten: an "edit" is a new version, so a report that pinned version 3
// keeps reading version 3 after the platform ships version 4 (292 D4).
func (s *store) CreateReportTemplate(ctx context.Context, template *model.ReportTemplate) error {
	if template.ID == "" {
		template.ID = id.New()
	}
	if template.CreatedAt.IsZero() {
		template.CreatedAt = time.Now().UTC()
	}
	if template.Status == "" {
		template.Status = model.ReportTemplateStatusActive
	}
	return s.WithContext(ctx).Create(template).Error
}

// GetReportTemplate loads one template version. `version <= 0` means "latest for
// this tenant". A retired template still resolves: retirement stops new reports
// from pinning it, and callers check `RetiredAt` themselves.
func (s *store) GetReportTemplate(ctx context.Context, tenantID, templateKey string, version int64) (*model.ReportTemplate, error) {
	q := s.WithContext(ctx).Where("tenant_id = ? AND template_key = ?", tenantID, templateKey)
	if version > 0 {
		q = q.Where("version = ?", version)
	} else {
		q = q.Order("version DESC")
	}
	var template model.ReportTemplate
	err := q.First(&template).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &template, nil
}

// ListReportTemplates returns every version visible to one tenant, ordered by
// key and version so the caller can build a "latest per key" view without a
// second query.
func (s *store) ListReportTemplates(ctx context.Context, tenantID string) ([]model.ReportTemplate, error) {
	var templates []model.ReportTemplate
	err := s.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("template_key ASC, version ASC").
		Find(&templates).Error
	return templates, err
}

// ---------------------------------------------------------------------------
// Aggregate writes
// ---------------------------------------------------------------------------

// CreateReport inserts the report identity row.
func (s *store) CreateReport(ctx context.Context, report *model.Report) error {
	if report.ID == "" {
		report.ID = id.New()
	}
	now := time.Now().UTC()
	if report.CreatedAt.IsZero() {
		report.CreatedAt = now
	}
	if report.UpdatedAt.IsZero() {
		report.UpdatedAt = now
	}
	if report.Status == "" {
		report.Status = model.ReportStatusActive
	}
	if report.AccessPolicy == "" {
		report.AccessPolicy = model.ReportAccessOwnerOnly
	}
	return s.WithContext(ctx).Create(report).Error
}

// CreateReportRevision inserts one immutable revision row.
func (s *store) CreateReportRevision(ctx context.Context, revision *model.ReportRevision) error {
	if revision.ID == "" {
		revision.ID = id.New()
	}
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(revision).Error
}

// CreateReportJob inserts the business job row — the single authority on the
// status a caller sees.
func (s *store) CreateReportJob(ctx context.Context, job *model.ReportJob) error {
	if job.ID == "" {
		job.ID = id.New()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = time.Now().UTC()
	}
	return s.WithContext(ctx).Create(job).Error
}

// CreateReportJobAttempt records the business→runtime mapping. The unique index
// on (report_job_id, attempt_seq, slice_seq) turns a replayed slice into a
// duplicate key instead of a second execution.
func (s *store) CreateReportJobAttempt(ctx context.Context, attempt *model.ReportJobAttempt) error {
	if attempt.ID == "" {
		attempt.ID = id.New()
	}
	return s.WithContext(ctx).Create(attempt).Error
}

// AppendReportEvent allocates the next per-job sequence and inserts the event in
// one transaction. Allocation is an UPDATE on the job row, which is what makes it
// safe under concurrency: Postgres locks the row, SQLite serializes writers, and
// both serialize the following SELECT. Read-then-write would hand the same
// sequence to two racing writers (doc/288 R23).
//
// `event.ExpiresAt` is frozen by the caller from the event-class TTL at write
// time; the janitor reads it verbatim.
func (s *store) AppendReportEvent(ctx context.Context, event *model.ReportJobEvent) (int64, error) {
	var seq int64
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allocated, err := appendReportEventInTx(tx, event)
		seq = allocated
		return err
	})
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// appendReportEventInTx is the body of AppendReportEvent, usable from a caller
// that already owns a transaction. Finalize must write its event inside *its*
// terminal-write transaction: an event committed separately could announce a
// terminal state that was rolled back (doc/288 §8.2 "事件一致性").
func appendReportEventInTx(tx *gorm.DB, event *model.ReportJobEvent) (int64, error) {
	if event.JobID == "" {
		return 0, gorm.ErrRecordNotFound
	}
	result := tx.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", event.TenantID, event.JobID).
		UpdateColumn("event_seq", gorm.Expr("event_seq + 1"))
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		// The job is gone (purged, or another tenant's id): an event without
		// a job would replay forever with no owner.
		return 0, gorm.ErrRecordNotFound
	}
	var job model.ReportJob
	if err := tx.Select("event_seq").
		Where("tenant_id = ? AND id = ?", event.TenantID, event.JobID).
		First(&job).Error; err != nil {
		return 0, err
	}
	if event.ID == "" {
		event.ID = id.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.Seq = job.EventSeq
	return job.EventSeq, tx.Create(event).Error
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// GetReport loads one report for one tenant. Soft-deleted rows are returned as
// well: the caller decides whether the recovery window is still open, and a
// soft-deleted report must remain restorable.
func (s *store) GetReport(ctx context.Context, tenantID, reportID string) (*model.Report, error) {
	var report model.Report
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, reportID).
		First(&report).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// GetReportJob loads one business job for one tenant.
func (s *store) GetReportJob(ctx context.Context, tenantID, jobID string) (*model.ReportJob, error) {
	var job model.ReportJob
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, jobID).
		First(&job).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// GetReportJobByIdempotencyKey resolves the stored key. Creation stores
// `<user_id>:<client key>`; a bare client key would let two users in one tenant
// collide on each other's reports (doc/288 §8.7 / 291 P0-4).
func (s *store) GetReportJobByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*model.ReportJob, error) {
	if idempotencyKey == "" {
		return nil, nil
	}
	var job model.ReportJob
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).
		First(&job).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// GetReportRevision loads one revision of one report. `revision <= 0` means the
// newest revision of that report.
func (s *store) GetReportRevision(ctx context.Context, tenantID, reportID string, revision int64) (*model.ReportRevision, error) {
	q := s.WithContext(ctx).Where("tenant_id = ? AND report_id = ?", tenantID, reportID)
	if revision > 0 {
		q = q.Where("revision = ?", revision)
	} else {
		q = q.Order("revision DESC")
	}
	var row model.ReportRevision
	err := q.First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListReports returns one tenant's live reports, newest first, optionally
// narrowed to a single owner. Soft-deleted rows are excluded: they are in their
// recovery window, and a restorable report is not a list entry. Cross-tenant
// listing deliberately has no path here — the platform scope belongs to the
// governance surface, not to this query.
func (s *store) ListReports(ctx context.Context, tenantID, ownerUserID string, limit, offset int) ([]model.Report, error) {
	if limit <= 0 || limit > maxPageSize {
		limit = maxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	q := s.WithContext(ctx).Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if ownerUserID != "" {
		q = q.Where("owner_user_id = ?", ownerUserID)
	}
	var reports []model.Report
	err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&reports).Error
	return reports, err
}

// CountReports counts exactly the rows ListReports returns, so a paged answer
// can carry a total without a second full read. The narrowing is identical on
// purpose: an owner-only surface must not advertise a total the caller cannot
// page through, or the count itself becomes an existence oracle.
func (s *store) CountReports(ctx context.Context, tenantID, ownerUserID string) (int64, error) {
	q := s.WithContext(ctx).Model(&model.Report{}).
		Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if ownerUserID != "" {
		q = q.Where("owner_user_id = ?", ownerUserID)
	}
	var count int64
	err := q.Count(&count).Error
	return count, err
}

// CountActiveReportJobs counts the non-terminal jobs of one report.
// Regeneration is refused while one exists (44110): two active jobs on one report
// would race for the same revision (doc/288 §7.10).
func (s *store) CountActiveReportJobs(ctx context.Context, tenantID, reportID string) (int64, error) {
	var count int64
	err := s.WithContext(ctx).Model(&model.ReportJob{}).
		Where("tenant_id = ? AND report_id = ? AND status IN ?", tenantID, reportID,
			[]string{
				model.ReportJobQueued, model.ReportJobRunning,
				model.ReportJobAwaitingOutline, model.ReportJobFinalizing,
				model.ReportJobCancelRequested,
			}).
		Count(&count).Error
	return count, err
}

// SoftDeleteReport stamps the recovery window. The update only touches a live
// row, so a second delete is a no-op rather than a silent extension of the
// window, and `expires_at` is untouched — the hold is a grace period on a
// deleted report, not a TTL extension (doc/288 §7.6).
func (s *store) SoftDeleteReport(ctx context.Context, tenantID, reportID string, now, holdUntil time.Time) error {
	updates := map[string]interface{}{
		"deleted_at": now,
		"updated_at": now,
	}
	if !holdUntil.IsZero() {
		updates["cleanup_hold_until"] = holdUntil
	}
	result := s.WithContext(ctx).Model(&model.Report{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, reportID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListReportEvents replays the job event stream after a cursor. The cursor is the
// last `seq` the client has, not a timestamp: `seq` is allocated per job inside the
// append transaction, so it is strictly increasing and gap-free, which a timestamp
// is not (two events can share a millisecond).
func (s *store) ListReportEvents(ctx context.Context, tenantID, jobID string, afterSeq int64, limit int) ([]model.ReportJobEvent, error) {
	if limit <= 0 || limit > maxPageSize {
		limit = maxPageSize
	}
	var events []model.ReportJobEvent
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND job_id = ? AND seq > ?", tenantID, jobID, afterSeq).
		Order("seq ASC").Limit(limit).
		Find(&events).Error
	return events, err
}

// FirstReportEventSeq reports the smallest surviving `seq` of one job's stream.
//
// `ok=false` means nothing survived, which is not "the job has no events": the
// caller distinguishes the two with `rgx_report_job.event_seq` (0 = never wrote
// one). Without that number a fully purged stream would look like an empty one and
// a stale cursor would be answered with silence.
func (s *store) FirstReportEventSeq(ctx context.Context, tenantID, jobID string) (int64, bool, error) {
	var first sql.NullInt64
	row := s.WithContext(ctx).Model(&model.ReportJobEvent{}).
		Where("tenant_id = ? AND job_id = ?", tenantID, jobID).
		Select("MIN(seq)").Row()
	if err := row.Scan(&first); err != nil {
		return 0, false, err
	}
	if !first.Valid {
		return 0, false, nil
	}
	return first.Int64, true, nil
}

// ---------------------------------------------------------------------------
// Finalize: the only writer of a report job's terminal state (doc/288 §8.2)
// ---------------------------------------------------------------------------

// ReportFinalizationStart is the request to close a slice and ask for a
// terminal-state write. Everything it needs is passed in, so the transaction
// contains no read-modify-write and no decision: the caller has already derived
// the facts (which ords completed, which failed) from the section rows.
type ReportFinalizationStart struct {
	TenantID   string
	JobID      string
	AttemptSeq int
	SliceSeq   int
	Now        time.Time
	// Event is the `job.finalizing` broadcast event, written in the same
	// transaction as the status flip. An event committed separately could
	// announce a finalize that was rolled back.
	Event *model.ReportJobEvent
	// Outbox is the durable finalize request. It is inserted in the same
	// transaction for the reason §8.2 spells out: "there is a `finalizing` row"
	// must imply "there is an undelivered row that says so", otherwise a crash
	// between the two leaves the report stuck with no persistent clue.
	Outbox *model.OutboxEvent
}

// BeginReportFinalization moves a running slice to `finalizing` and records the
// durable request to write the terminal state.
//
// The transition is conditional on `attempt_seq`/`slice_seq` as well as the
// status: a superseded slice (the worker yielded and a newer slice already took
// over) must not close the job, and a replayed slice must not write a second
// outbox row. `applied=false` means exactly "someone else owns this job now" and
// is not an error.
func (s *store) BeginReportFinalization(ctx context.Context, start ReportFinalizationStart) (bool, error) {
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportJob{}).
			Where("tenant_id = ? AND id = ? AND attempt_seq = ? AND slice_seq = ? AND finalized_at IS NULL AND status IN ?",
				start.TenantID, start.JobID, start.AttemptSeq, start.SliceSeq,
				[]string{model.ReportJobRunning, model.ReportJobCancelRequested}).
			Updates(map[string]interface{}{
				"status": model.ReportJobFinalizing, "updated_at": start.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if _, err := appendReportEventInTx(tx, start.Event); err != nil {
			return err
		}
		if start.Outbox != nil {
			if start.Outbox.ID == "" {
				start.Outbox.ID = id.New()
			}
			if start.Outbox.CreatedAt.IsZero() {
				start.Outbox.CreatedAt = start.Now
			}
			if start.Outbox.UpdatedAt.IsZero() {
				start.Outbox.UpdatedAt = start.Now
			}
			if err := tx.Create(start.Outbox).Error; err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ReportFinalizationPlan is the derived decision. The service derives it from
// facts (section rows, cancel flag, pinned template) and this method applies the
// whole thing atomically, because a half-applied finalize is exactly the
// "terminal state but unpublished revision" window §8.2 exists to remove.
type ReportFinalizationPlan struct {
	TenantID   string
	JobID      string
	ReportID   string
	RevisionID string
	// Status is one of succeeded/partial/failed/cancelled.
	Status string
	// Completeness is full/partial for a published revision, empty otherwise.
	Completeness string
	// GapJSON is the disclosure of every missing section. A published `partial`
	// revision without it would be a silent omission (§7.10).
	GapJSON string
	// ErrorCode is the report-family code explaining a failure (44105 for a
	// structural failure, 44112 for an impossible ledger). Empty on success.
	ErrorCode string
	// MetricsJSON is the §14.1 quality snapshot. It is written for every terminal
	// outcome, not just published ones: the numbers explain *why* a revision was
	// not published, and losing them would leave the banner with adjectives only.
	MetricsJSON string
	// Publish turns the revision into the report's current content. It is false
	// for failed/cancelled: a broken version must never replace a working one
	// (§7.10 row "失败/取消").
	Publish bool
	Now     time.Time
	// Event is the terminal broadcast event (job.succeeded / job.partial /
	// job.failed / job.cancelled).
	Event *model.ReportJobEvent
}

// ApplyReportFinalization writes the terminal state, publishes or buries the
// revision, settles the ledger and appends the terminal event in one
// transaction.
//
// `applied=false` means the job was already finalized (or is no longer
// `finalizing`): the first statement is the condition, so a duplicate delivery
// produces no second revision, no second event and no second settlement
// (doc/288 §8.2 幂等 / A45).
func (s *store) ApplyReportFinalization(ctx context.Context, plan ReportFinalizationPlan) (bool, error) {
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportJob{}).
			Where("tenant_id = ? AND id = ? AND status = ? AND finalized_at IS NULL",
				plan.TenantID, plan.JobID, model.ReportJobFinalizing).
			Updates(map[string]interface{}{
				"status": plan.Status, "error_code": plan.ErrorCode,
				"finished_at": plan.Now, "finalized_at": plan.Now, "updated_at": plan.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := settleReportChargesInTx(tx, plan); err != nil {
			return err
		}
		if err := applyReportRevisionInTx(tx, plan); err != nil {
			return err
		}
		if _, err := appendReportEventInTx(tx, plan.Event); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// settleReportChargesInTx closes the billing window (doc/288 §8.6).
//
// Three rules are load-bearing:
//
//   - every still-`reserved` row is released in full. At terminal time no call is
//     in flight any more, so an unreleased reservation would be a leak, not a
//     pending cost.
//   - the ledger is append-only and therefore the truth for the four *ledger*
//     columns (actual / charged / released-per-call / pending). The job columns
//     are rewritten from the ledger SUM, never incremented.
//   - the job's `reserved_tokens` is the budget envelope frozen at creation, not a
//     ledger total, so the two terminal differences are derived from both sides:
//     `released = max(budget − charged, 0)` returns the unspent envelope and
//     `overage = max(charged − budget, 0)` records spend beyond it. §8.6 requires
//     an overage to be billable but *written down*, never silently absorbed —
//     hence the audit row. A job without an envelope (budget 0) falls back to the
//     ledger-reserved reading: writing `released = 0` there would claim the whole
//     budget was consumed.
func settleReportChargesInTx(tx *gorm.DB, plan ReportFinalizationPlan) error {
	scope := func() *gorm.DB {
		return tx.Model(&model.ReportCharge{}).
			Where("tenant_id = ? AND report_job_id = ?", plan.TenantID, plan.JobID)
	}
	if err := scope().Where("state = ?", model.ReportChargeReserved).
		Updates(map[string]interface{}{
			"state":             model.ReportChargeReleased,
			"released_tokens":   gorm.Expr("reserved_tokens"),
			"reconcile_outcome": model.ReportReconcileOutcomeReleased,
			"settled_at":        plan.Now,
		}).Error; err != nil {
		return err
	}
	var sums reportChargeSumsRow
	if err := scope().Select(reportChargeSumSelect).Scan(&sums).Error; err != nil {
		return err
	}
	var job model.ReportJob
	if err := tx.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", plan.TenantID, plan.JobID).
		Select("reserved_tokens").Take(&job).Error; err != nil {
		return err
	}
	budget := job.ReservedTokens
	if budget <= 0 {
		budget = sums.Reserved
	}
	released := budget - sums.Charged
	overage := int64(0)
	if released < 0 {
		overage = -released
		released = 0
	}
	updates := map[string]interface{}{
		"actual_tokens":            sums.Actual,
		"charged_tokens":           sums.Charged,
		"released_tokens":          released,
		"overage_tokens":           overage,
		"pending_reconcile_tokens": sums.Orphan,
	}
	if err := tx.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", plan.TenantID, plan.JobID).
		Updates(updates).Error; err != nil {
		return err
	}
	if released > 0 {
		if err := auditReportCharge(tx, plan.TenantID, plan.ReportID, plan.Now,
			"report.charge.released", map[string]interface{}{
				"llm_usage": map[string]interface{}{
					"reserved": budget, "charged": sums.Charged, "released": released,
				},
			}); err != nil {
			return err
		}
	}
	if overage <= 0 {
		return nil
	}
	return auditReportCharge(tx, plan.TenantID, plan.ReportID, plan.Now,
		"report.charge.overage", map[string]interface{}{
			"llm_usage": map[string]interface{}{
				"charged": sums.Charged, "reserved": budget, "overage": overage,
			},
		})
}

// auditReportCharge writes one billing audit row *inside the caller's
// transaction*. It must ride the same tamper-evident chain as every other
// audit (a call to the standalone append would nest a transaction inside the
// settlement's, orphaning the row if the outer transaction rolled back), so the
// per-tenant seq and hash are allocated here from the chain tail. A charge fact
// that cannot be audited must not commit.
func auditReportCharge(
	tx *gorm.DB, tenantID, reportID string, now time.Time, action string, detail map[string]interface{},
) error {
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return appendAuditInTx(tx, &model.AuditLog{
		TenantID: tenantID, Action: action,
		Resource: "report", ResourceID: reportID, Result: "SUCCESS",
		DetailJSON: string(payload), At: now,
	})
}

// reportChargeSumSelect is shared by the settlement and the reconcile read so the
// two can never disagree about what "charged" means.
const reportChargeSumSelect = "COALESCE(SUM(reserved_tokens),0) AS reserved, " +
	"COALESCE(SUM(actual_tokens),0) AS actual, " +
	"COALESCE(SUM(CASE WHEN state = 'settled' THEN charged_tokens ELSE 0 END),0) AS charged, " +
	"COALESCE(SUM(released_tokens),0) AS released, " +
	"COALESCE(SUM(CASE WHEN state = 'reserved' THEN reserved_tokens ELSE 0 END),0) AS held, " +
	"COALESCE(SUM(CASE WHEN state = 'orphan' AND (reconcile_outcome = '' OR reconcile_outcome = 'unverified') " +
	"THEN reserved_tokens ELSE 0 END),0) AS orphan"

type reportChargeSumsRow struct {
	Reserved int64 `gorm:"column:reserved"`
	Actual   int64 `gorm:"column:actual"`
	Charged  int64 `gorm:"column:charged"`
	Released int64 `gorm:"column:released"`
	// Held is the budget that is still pinned by an in-flight call: the gate
	// subtracts it from the envelope together with `actual`.
	Held   int64 `gorm:"column:held"`
	Orphan int64 `gorm:"column:orphan"`
}

// applyReportRevisionInTx is the publish matrix of §7.10, expressed as writes.
func applyReportRevisionInTx(tx *gorm.DB, plan ReportFinalizationPlan) error {
	if !plan.Publish {
		// failed / cancelled: the revision keeps its sections (they are the
		// history of the attempt) but never becomes current, and a previously
		// published revision stays exactly as it was.
		status := model.ReportRevisionFailed
		if plan.Status == model.ReportJobCancelled {
			status = model.ReportRevisionCancelled
		}
		return tx.Model(&model.ReportRevision{}).
			Where("tenant_id = ? AND id = ?", plan.TenantID, plan.RevisionID).
			Updates(map[string]interface{}{
				"status": status, "gap_json": plan.GapJSON,
				"completeness": plan.Completeness, "metrics_json": plan.MetricsJSON,
			}).Error
	}
	// The outgoing version is superseded, not deleted: its sections, citations
	// and artifacts stay readable (A41).
	if err := tx.Model(&model.ReportRevision{}).
		Where("tenant_id = ? AND report_id = ? AND status = ? AND id <> ?",
			plan.TenantID, plan.ReportID, model.ReportRevisionPublished, plan.RevisionID).
		Updates(map[string]interface{}{
			"status": model.ReportRevisionSuperseded, "superseded_at": plan.Now,
		}).Error; err != nil {
		return err
	}
	result := tx.Model(&model.ReportRevision{}).
		Where("tenant_id = ? AND id = ?", plan.TenantID, plan.RevisionID).
		Updates(map[string]interface{}{
			"status": model.ReportRevisionPublished, "published_at": plan.Now,
			"completeness": plan.Completeness, "gap_json": plan.GapJSON,
			"metrics_json": plan.MetricsJSON,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	// `current_revision_id` moves in the same transaction, so no reader can see
	// "published revision that is not current" (§7.10 提升动作).
	return tx.Model(&model.Report{}).
		Where("tenant_id = ? AND id = ?", plan.TenantID, plan.ReportID).
		Updates(map[string]interface{}{
			"current_revision_id": plan.RevisionID, "updated_at": plan.Now,
		}).Error
}

// ReportChargeSums is the ledger total of one job, read outside the finalize
// transaction. It exists so a reconcile pass can compare the cache with the
// ledger without re-implementing the SQL.
type ReportChargeSums struct {
	Reserved int64 `json:"reserved_tokens"`
	Actual   int64 `json:"actual_tokens"`
	Charged  int64 `json:"charged_tokens"`
	Released int64 `json:"released_tokens"`
	// Held is the still-`reserved` share, i.e. budget pinned by in-flight calls.
	Held   int64 `json:"held_tokens"`
	Orphan int64 `json:"pending_reconcile_tokens"`
}

// SumReportCharges returns the ledger total of one job.
func (s *store) SumReportCharges(ctx context.Context, tenantID, jobID string) (ReportChargeSums, error) {
	var sums ReportChargeSums
	err := s.WithContext(ctx).Model(&model.ReportCharge{}).
		Where("tenant_id = ? AND report_job_id = ?", tenantID, jobID).
		Select(reportChargeSumSelect).
		Scan(&sums).Error
	return sums, err
}

// RecordReportExecutionSnapshot freezes the generation conditions of a revision
// exactly once (doc/288 §7.7 / A8).
//
// Write-once is the point: the snapshot answers "with which model, threshold and
// dataset binding was this text produced?", and a second write would turn that
// answer into "whatever the config said the second time". `applied=false` means
// the snapshot was already frozen; callers must treat it as a no-op, not as an
// error, because a retried outline step is a normal outcome.
func (s *store) RecordReportExecutionSnapshot(
	ctx context.Context, tenantID, revisionID, snapshotJSON, hash string,
) (bool, error) {
	if snapshotJSON == "" || hash == "" {
		return false, gorm.ErrRecordNotFound
	}
	result := s.WithContext(ctx).Model(&model.ReportRevision{}).
		Where("tenant_id = ? AND id = ? AND (execution_snapshot_hash IS NULL OR execution_snapshot_hash = '')",
			tenantID, revisionID).
		Updates(map[string]interface{}{
			"execution_snapshot_json": snapshotJSON, "execution_snapshot_hash": hash,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ListReportSections returns every section row of one revision in display order.
// Finalize derives the terminal state from these rows and nothing else, so the
// query is deliberately unfiltered: a filtered read would let a section silently
// disappear from the derivation.
func (s *store) ListReportSections(ctx context.Context, tenantID, revisionID string) ([]model.ReportSection, error) {
	var sections []model.ReportSection
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND revision_id = ?", tenantID, revisionID).
		Order("ord ASC, section_version ASC").
		Find(&sections).Error
	return sections, err
}

// CreateReportSection inserts one section row. Section rows are immutable
// history once written: any content change writes a *new* row (section_version + 1)
// and supersedes the old one (doc/288 §7.10), so there is no update path here.
// The unique index (revision_id, ord, section_version) is the last line of
// defence against a racing retry allocating the same version.
func (s *store) CreateReportSection(ctx context.Context, section *model.ReportSection) error {
	if section.ID == "" {
		section.ID = id.New()
	}
	return s.WithContext(ctx).Create(section).Error
}

// SupersedeReportSection retires a failed section row so a later version becomes
// the live one. `applied=false` means the row was not `failed` any more (a
// concurrent retry won, or finalize already replaced it): the caller must not
// write the new row — the winner owns the change.
func (s *store) SupersedeReportSection(ctx context.Context, sectionID string, now time.Time) (bool, error) {
	result := s.WithContext(ctx).Model(&model.ReportSection{}).
		Where("id = ? AND status = ?", sectionID, model.ReportSectionFailed).
		Updates(map[string]interface{}{
			"status": model.ReportSectionSuperseded,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// GetReportRevisionByID loads a revision by its own id, not by report+number:
// finalize addresses the revision the job was created with, which is not
// necessarily the report's newest one once regeneration exists.
func (s *store) GetReportRevisionByID(ctx context.Context, tenantID, revisionID string) (*model.ReportRevision, error) {
	var revision model.ReportRevision
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, revisionID).
		First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

// ReportFinalizeCandidate is one stale `finalizing` job found by the reconcile
// sweep. It carries only the three identifiers finalize needs: a wider projection
// would let the sweep make decisions it has no business making.
type ReportFinalizeCandidate struct {
	TenantID     string
	JobID        string
	RuntimeJobID string
}

// ListStaleFinalizingReportJobs returns jobs stuck in `finalizing` whose row has
// not moved for `staleBefore`.
//
// Two things are deliberate. First, the window is measured on `updated_at`: a
// job that is being finalized right now keeps touching its row, so the sweep does
// not race the outbox consumer for the common case. Second, the sweep does not
// check the outbox at all — "the outbox row was published" is not a proof that the
// terminal write happened, and treating it as one is how a report ends up
// finalizing forever after a crash between the two (A35 ④).
func (s *store) ListStaleFinalizingReportJobs(
	ctx context.Context, staleBefore time.Time, limit int,
) ([]ReportFinalizeCandidate, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	var candidates []ReportFinalizeCandidate
	err := s.WithContext(ctx).Model(&model.ReportJob{}).
		Select("tenant_id, id AS job_id, current_runtime_job_id AS runtime_job_id").
		Where("status = ? AND finalized_at IS NULL AND updated_at < ?",
			model.ReportJobFinalizing, staleBefore).
		Order("updated_at ASC").
		Limit(limit).
		Scan(&candidates).Error
	return candidates, err
}

// ---------------------------------------------------------------------------
// Slices: one runtime job per business attempt/slice (doc/288 §7.9)
// ---------------------------------------------------------------------------

// ReportSliceStart describes one runtime slice to arm.
//
// The business job is the authority: `rgx_job` only describes one execution, and
// the pointer `current_runtime_job_id` is the only link between them. Both are
// written in one transaction with the `rgx_report_job_attempt` mapping, because
// "the job is running" must imply "the slice it points at exists and is mapped"
// (I1) — a pointer written after the insert would leave a window in which the
// job names a row nobody can execute.
type ReportSliceStart struct {
	TenantID     string
	JobID        string
	ReportID     string
	RevisionID   string
	AttemptSeq   int
	SliceSeq     int
	RuntimeJobID string
	// RuntimeKey is `report:<report_job_id>:<attempt_seq>:<slice_seq>`. It is the
	// execution idempotency key: a replayed trigger enqueues nothing.
	RuntimeKey  string
	PayloadJSON string
	// Stage is the business stage this slice starts in; it is display state, not
	// a scheduling decision.
	Stage string
	Now   time.Time
}

// ReportSliceAdvance is "this slice yielded, arm the next one". `Slice.SliceSeq`
// is the *new* slice's seq; the previous one is `Slice.SliceSeq - 1`.
type ReportSliceAdvance struct {
	Slice ReportSliceStart
	// PreviousRuntimeJobID is the slice that just yielded. The CAS requires the
	// job to still point at it, so a superseded slice cannot arm a new one.
	PreviousRuntimeJobID string
	// ProgressDone is written in the same statement as the new slice: a progress
	// counter that moves without a slice is a lie the UI cannot explain.
	ProgressDone int
	// Event is the `job.progress` checkpoint event, written in the same
	// transaction as the new slice.
	Event *model.ReportJobEvent
}

// insertReportSliceInTx inserts the runtime slice and its attempt mapping.
//
// A slice key that already exists with a different runtime id aborts the
// transaction: the pointer would name a row that is not the one executing, and
// reconciling the two copies by guesswork is worse than failing the arm.
func insertReportSliceInTx(tx *gorm.DB, start ReportSliceStart) error {
	if start.RuntimeJobID == "" || start.RuntimeKey == "" {
		return errors.New("report slice requires a runtime job id and key")
	}
	slice := &model.Job{
		ID: start.RuntimeJobID, Kind: model.JobKindReportGenerate, Key: start.RuntimeKey,
		TenantID: start.TenantID, Payload: start.PayloadJSON, Status: model.JobStatusQueued,
		MaxRetry: defaultJobMaxRetry, RunAfter: start.Now, CreatedAt: start.Now, UpdatedAt: start.Now,
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoNothing: true,
	}).Create(slice)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("report slice %s already exists", start.RuntimeKey)
	}
	attempt := &model.ReportJobAttempt{
		ID: id.New(), TenantID: start.TenantID, ReportJobID: start.JobID,
		AttemptSeq: start.AttemptSeq, SliceSeq: start.SliceSeq,
		RuntimeJobID: start.RuntimeJobID, RuntimeJobKey: start.RuntimeKey,
		Status: model.JobStatusQueued,
	}
	return tx.Create(attempt).Error
}

// ReportAttemptRebuild is the class-3 repair (doc/288 §7.9): the business job
// already points at a live runtime slice, only the mapping row is missing.
// Rebuilding never fabricates runtime state — it mirrors the triple the job
// declares, so it is record-keyed idempotent on the same unique index.
type ReportAttemptRebuild struct {
	TenantID   string
	JobID      string
	AttemptSeq int
	SliceSeq   int
	// RuntimeJobID is the slice the business job currently points at.
	RuntimeJobID  string
	RuntimeJobKey string
}

func (s *store) RebuildReportAttemptIfMissing(
	ctx context.Context, rebuild ReportAttemptRebuild,
) (bool, error) {
	if rebuild.RuntimeJobID == "" || rebuild.RuntimeJobKey == "" {
		return false, errors.New("report attempt rebuild requires a runtime id and key")
	}
	attempt := &model.ReportJobAttempt{
		ID: id.New(), TenantID: rebuild.TenantID, ReportJobID: rebuild.JobID,
		AttemptSeq: rebuild.AttemptSeq, SliceSeq: rebuild.SliceSeq,
		RuntimeJobID: rebuild.RuntimeJobID, RuntimeJobKey: rebuild.RuntimeJobKey,
		Status: model.JobStatusQueued,
	}
	result := s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "report_job_id"}, {Name: "attempt_seq"}, {Name: "slice_seq"}},
		DoNothing: true,
	}).Create(attempt)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// DispatchReportSlice arms the first slice of a `queued` business job.
//
// `applied=false` means the job is no longer queued (a second dispatch won the
// race, or the user cancelled first) and is not an error: dispatch is a
// transition, not an insert.
func (s *store) DispatchReportSlice(ctx context.Context, start ReportSliceStart) (bool, error) {
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportJob{}).
			Where(`tenant_id = ? AND id = ? AND attempt_seq = ? AND slice_seq = ? AND
				finalized_at IS NULL AND status = ? AND
				(current_runtime_job_id IS NULL OR current_runtime_job_id = '')`,
				start.TenantID, start.JobID, start.AttemptSeq, start.SliceSeq, model.ReportJobQueued).
			Updates(map[string]interface{}{
				"status":                 model.ReportJobRunning,
				"current_runtime_job_id": start.RuntimeJobID,
				"stage":                  start.Stage,
				"started_at":             start.Now,
				"updated_at":             start.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := insertReportSliceInTx(tx, start); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// AdvanceReportSlice moves a running job to its next slice after a checkpoint
// (the 0.8×JobTimeout yield, doc/288 §8.2).
//
// The single statement bumps `slice_seq`, re-points `current_runtime_job_id`,
// records the checkpoint clock and the progress counter; only then is the next
// slice inserted. The guard includes the old runtime id, so the yield is a no-op
// for a slice that has already been superseded — a late checkpoint must never
// resurrect an old slice's numbering.
func (s *store) AdvanceReportSlice(ctx context.Context, advance ReportSliceAdvance) (bool, error) {
	next := advance.Slice
	if next.SliceSeq < 2 {
		return false, errors.New("report slice advance requires slice_seq >= 2")
	}
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ReportJob{}).
			Where(`tenant_id = ? AND id = ? AND attempt_seq = ? AND slice_seq = ? AND
				current_runtime_job_id = ? AND finalized_at IS NULL AND status = ?`,
				next.TenantID, next.JobID, next.AttemptSeq, next.SliceSeq-1,
				advance.PreviousRuntimeJobID, model.ReportJobRunning).
			Updates(map[string]interface{}{
				"slice_seq":              next.SliceSeq,
				"current_runtime_job_id": next.RuntimeJobID,
				"stage":                  next.Stage,
				"progress_done":          advance.ProgressDone,
				"last_checkpoint_at":     next.Now,
				"updated_at":             next.Now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := insertReportSliceInTx(tx, next); err != nil {
			return err
		}
		applied = true
		if advance.Event == nil {
			return nil
		}
		if _, err := appendReportEventInTx(tx, advance.Event); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// RequestReportJobCancel records the user's cancel intent.
//
// It deliberately writes only `cancel_requested_at` + `cancel_requested` and never
// a terminal state: §8.4 makes `cancelled` a *derived* outcome, so that a cancel
// racing a finished slice can still publish the finished work instead of throwing
// it away. `updated_at` moves because the reconcile sweep measures staleness on it.
func (s *store) RequestReportJobCancel(ctx context.Context, tenantID, jobID string, now time.Time) (bool, error) {
	result := s.WithContext(ctx).Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ? AND finalized_at IS NULL AND status IN ?",
			tenantID, jobID,
			[]string{model.ReportJobQueued, model.ReportJobRunning, model.ReportJobAwaitingOutline}).
		Updates(map[string]interface{}{
			"status":              model.ReportJobCancelRequested,
			"cancel_requested_at": now,
			"updated_at":          now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ReportConvergenceCandidate is one active job the sweep found untouched for
// longer than the stale window. It carries the status too, so the caller can tell
// "a running job whose slice vanished" from "a cancel that nobody converged".
type ReportConvergenceCandidate struct {
	TenantID string
	JobID    string
	Status   string
}

// ListStaleActiveReportJobs returns `running`/`cancel_requested` jobs whose row has
// not moved for `staleBefore`.
//
// `finalized_at IS NULL` is the only terminal-state filter: a job that already has
// its terminal write is never candidate, no matter what its status says.
func (s *store) ListStaleActiveReportJobs(
	ctx context.Context, staleBefore time.Time, limit int,
) ([]ReportConvergenceCandidate, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	var candidates []ReportConvergenceCandidate
	err := s.WithContext(ctx).Model(&model.ReportJob{}).
		Select("tenant_id, id AS job_id, status").
		Where("status IN ? AND finalized_at IS NULL AND updated_at < ?",
			[]string{model.ReportJobRunning, model.ReportJobCancelRequested}, staleBefore).
		Order("updated_at ASC").
		Limit(limit).
		Scan(&candidates).Error
	return candidates, err
}

// GetLatestReportJob returns the job the user is currently looking at.
//
// Cancel and status reads are addressed by report id, so the newest row decides:
// a regeneration creates a new job rather than reviving the old one (§7.10), and a
// cancel must reach the job that is actually running.
func (s *store) GetLatestReportJob(ctx context.Context, tenantID, reportID string) (*model.ReportJob, error) {
	var job model.ReportJob
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND report_id = ?", tenantID, reportID).
		Order("updated_at DESC, id DESC").
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// ---------------------------------------------------------------------------
// Charge ledger (doc/288 §8.6)
// ---------------------------------------------------------------------------
//
// The ledger is the only place a token is counted. Every call reserves before it
// is placed and settles after it returns; a call that never returns is written
// down as `orphan` by the sweep rather than guessed at. Nothing here increments a
// counter: rows are appended, transitions are CAS'd, and the job's summary columns
// are recomputed from the rows.

// ReportChargeReserve is one call about to be placed on the provider.
type ReportChargeReserve struct {
	TenantID       string
	JobID          string
	SectionID      string
	AttemptSeq     int
	SliceSeq       int
	SectionAttempt int
	ReservedTokens int64
	ModelID        string
	RuntimeJobID   string
	Now            time.Time
}

// ReportChargeTransition is one ledger state move. `From` is the state the move is
// allowed to start from, which is also the concurrency guard: a settle and an
// orphan racing on the same row cannot both win.
type ReportChargeTransition struct {
	TenantID  string
	LedgerKey string
	From      string
	Set       map[string]interface{}
}

// ReportChargeCandidate is one stale reservation the sweep found.
type ReportChargeCandidate struct {
	ID             string
	TenantID       string
	JobID          string
	LedgerKey      string
	ReservedTokens int64
	// Reason is the `reconcile_reason` written when the row is orphaned. Empty
	// means the sweep's own reason (`stale_reserved`); a writer that knows the
	// call went out but never came back passes `provider_unconfirmed` instead
	// (doc/288 §8.6). The two are different stories for the same accounting and
	// the tenant-facing backlog report must not merge them.
	Reason string
}

// TouchReportJob takes the job's write lock and refreshes `updated_at`.
//
// It is an UPDATE rather than `SELECT ... FOR UPDATE` on purpose: the same code runs
// on PostgreSQL (where the row lock is what serialises two sections) and on SQLite
// (where the write lock is), and `FOR UPDATE` is not portable. The `updated_at` bump
// is not a side effect to be apologised for — a job with a call in flight is alive,
// so it must not look stale to the reconcile sweep.
func (s *store) TouchReportJob(ctx context.Context, tenantID, jobID string, now time.Time) (bool, error) {
	result := s.WithContext(ctx).Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", tenantID, jobID).
		Update("updated_at", now)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// ReserveReportCharge appends a `reserved` row and allocates its `call_seq`.
//
// Allocation is `MAX(call_seq)+1` inside the same transaction as the insert, so two
// concurrent reserves on one section cannot mint the same sequence; the unique index
// on `ledger_key` is the second line of defence. Allocating from a counter read
// outside a transaction, or from a clock, would let a replayed slice mint a "new"
// key for a call that already happened.
func (s *store) ReserveReportCharge(
	ctx context.Context, reserve ReportChargeReserve,
) (*model.ReportCharge, error) {
	if reserve.ReservedTokens <= 0 {
		return nil, errors.New("report charge reservation must be positive")
	}
	if reserve.TenantID == "" || reserve.JobID == "" || reserve.SectionID == "" {
		return nil, errors.New("report charge reservation requires tenant, job and section")
	}
	charge := &model.ReportCharge{
		ID: id.New(), TenantID: reserve.TenantID, ReportJobID: reserve.JobID,
		AttemptSeq: reserve.AttemptSeq, SliceSeq: reserve.SliceSeq,
		SectionID: reserve.SectionID, SectionAttempt: reserve.SectionAttempt,
		State: model.ReportChargeReserved, ReservedTokens: reserve.ReservedTokens,
		ModelID: reserve.ModelID, RuntimeJobID: reserve.RuntimeJobID,
		CreatedAt: reserve.Now,
	}
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var callSeq int
		if err := tx.Model(&model.ReportCharge{}).
			Select("COALESCE(MAX(call_seq),0)").
			Where(`tenant_id = ? AND report_job_id = ? AND attempt_seq = ? AND
				slice_seq = ? AND section_id = ? AND section_attempt = ?`,
				charge.TenantID, charge.ReportJobID, charge.AttemptSeq, charge.SliceSeq,
				charge.SectionID, charge.SectionAttempt).
			Scan(&callSeq).Error; err != nil {
			return err
		}
		charge.CallSeq = callSeq + 1
		charge.LedgerKey = model.ReportLedgerKey(
			charge.ReportJobID, charge.AttemptSeq, charge.SliceSeq,
			charge.SectionID, charge.SectionAttempt, charge.CallSeq)
		if err := tx.Create(charge).Error; err != nil {
			return err
		}
		// A live call is a heartbeat: it must not look stale to the reconcile
		// sweep while the provider is still thinking.
		return tx.Model(&model.ReportJob{}).
			Where("tenant_id = ? AND id = ?", charge.TenantID, charge.ReportJobID).
			Updates(map[string]interface{}{"updated_at": reserve.Now}).Error
	})
	if err != nil {
		return nil, err
	}
	return charge, nil
}

// TransitionReportCharge applies one CAS'd state move and reports whether it won.
func (s *store) TransitionReportCharge(
	ctx context.Context, transition ReportChargeTransition,
) (bool, error) {
	if len(transition.Set) == 0 {
		return false, errors.New("report charge transition requires at least one column")
	}
	result := s.WithContext(ctx).Model(&model.ReportCharge{}).
		Where("tenant_id = ? AND ledger_key = ? AND state = ?",
			transition.TenantID, transition.LedgerKey, transition.From).
		Updates(transition.Set)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// RefreshReportChargeTotals rewrites the ledger-derived summary columns of one job.
//
// Only the three running-time columns are touched: `reserved_tokens` is the
// envelope frozen at creation and `released`/`overage` are terminal derivations
// (settleReportChargesInTx owns them). Rewriting from the ledger instead of
// incrementing means a double refresh is a no-op rather than a double count.
func (s *store) RefreshReportChargeTotals(
	ctx context.Context, tenantID, jobID string, now time.Time,
) (ReportChargeSums, error) {
	sums, err := s.SumReportCharges(ctx, tenantID, jobID)
	if err != nil {
		return ReportChargeSums{}, err
	}
	err = s.WithContext(ctx).Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", tenantID, jobID).
		Updates(map[string]interface{}{
			"actual_tokens":            sums.Actual,
			"charged_tokens":           sums.Charged,
			"pending_reconcile_tokens": sums.Orphan,
			"updated_at":               now,
		}).Error
	if err != nil {
		return ReportChargeSums{}, err
	}
	return sums, nil
}

// ListStaleReservedReportCharges returns reservations older than `staleBefore`.
//
// Age is measured on `created_at` — the reservation's own clock — and not on the
// job's: a job may legitimately run for an hour while every individual call is
// short, so a job-level timestamp would either never fire or fire on a healthy
// job. The index `idx_report_charge_stale` exists for exactly this query.
func (s *store) ListStaleReservedReportCharges(
	ctx context.Context, staleBefore time.Time, limit int,
) ([]ReportChargeCandidate, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var candidates []ReportChargeCandidate
	err := s.WithContext(ctx).Model(&model.ReportCharge{}).
		Select("id, tenant_id, report_job_id AS job_id, ledger_key, reserved_tokens").
		Where("state = ? AND created_at < ?", model.ReportChargeReserved, staleBefore).
		Order("created_at ASC").
		Limit(limit).
		Scan(&candidates).Error
	return candidates, err
}

// OrphanReportCharge writes one stale reservation down as `orphan`.
//
// `reconcile_outcome = unverified` is what makes it countable as
// `pending_reconcile_tokens`: the provider may or may not have billed the call, and
// pretending either way would be a guess. The CAS keeps a settle from being
// overwritten by a sweep that read the row a moment earlier.
func (s *store) OrphanReportCharge(
	ctx context.Context, candidate ReportChargeCandidate, now time.Time,
) (bool, error) {
	reason := candidate.Reason
	if reason == "" {
		reason = model.ReportReconcileStaleReserved
	}
	result := s.WithContext(ctx).Model(&model.ReportCharge{}).
		Where("tenant_id = ? AND id = ? AND state = ?",
			candidate.TenantID, candidate.ID, model.ReportChargeReserved).
		Updates(map[string]interface{}{
			"state":             model.ReportChargeOrphan,
			"reconcile_reason":  reason,
			"reconcile_outcome": model.ReportReconcileOutcomeUnverified,
			"settled_at":        now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
