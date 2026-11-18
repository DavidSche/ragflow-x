package service

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ReportDetail is the read aggregate of one report: the row plus the revision a
// caller is allowed to see.
//
// The job is deliberately absent from this slice. A report's live job is the
// SSE surface (`GET /report-jobs/:id` + `/events`, Slice G) and reading it here
// without the cursor would hand the UI a second, racing progress source.
type ReportDetail struct {
	Report   *model.Report         `json:"report"`
	Revision *model.ReportRevision `json:"revision"`
	// EvidenceVerifiable answers "can this report's citations still be re-checked?"
	// (doc/288 §7.6 / A43). It is false once any cited snapshot has been purged or
	// has passed its own clock — the body and the recorded judgement stay readable,
	// and the UI must say so instead of quietly offering a citation that opens
	// nothing. A report with no citations is verifiable: nothing was lost.
	EvidenceVerifiable bool `json:"evidence_verifiable"`
	// EvidenceExpiredAt is the earliest snapshot clock of this revision, so a client
	// can say *when* verification stopped rather than just that it did. Nil when no
	// citations exist.
	EvidenceExpiredAt *time.Time `json:"evidence_expired_at"`
	// Artifacts is the delivered-file history of this revision, newest version
	// first (A11: "跨会话/刷新可见可下载").
	//
	// It belongs in the read aggregate, not only in the POST that minted it: an
	// export list that lives in a component's memory disappears on refresh, which
	// would make yesterday's delivery un-findable while the file itself still sits
	// on disk. `downloadable` is derived server-side by the same
	// `reportArtifactDownloadable` the download endpoint uses, so the button state
	// and the endpoint cannot disagree.
	Artifacts []ReportArtifactView `json:"artifacts"`
}

// GetReport returns one report for its owner.
//
// Two gates, in this order:
//
//  1. RBAC `read:report` (the handler's route rule does the same, defence in
//     depth), then the ABAC tenant check;
//  2. the row's own access policy. P0-A only ever writes `owner_only`, so a
//     same-tenant colleague is answered "not found" (44109) rather than 403:
//     §7.6 requires the isolation test to be unable to tell a foreign report id
//     from a nonexistent one.
func (s *Service) GetReport(ctx context.Context, actorID, tenantID, reportID string) (*ReportDetail, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	revision, err := s.reportCurrentRevision(ctx, tenantID, report)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		// A report without a revision cannot exist: creation writes both in one
		// transaction, and the retention purge removes both. Answering the honest
		// "gone" is better than returning a half aggregate.
		return nil, httperr.New(404, codeReportRevisionNotFound, "report revision is no longer available")
	}
	return s.reportDetailWithEvidence(ctx, report, revision)
}

// ReportRevisionSummary is one row of the version history (doc/288 §7.10 / A32).
//
// It deliberately carries no body: the list answers "which revisions does this
// report have and what became of them", and the body is a separate read by number
// (`GET /reports/:id/revisions/:n`). Loading every historical body to render a
// history picker is how a five-revision report turns into a five-document page.
type ReportRevisionSummary struct {
	ID                    string     `json:"id"`
	Revision              int64      `json:"revision"`
	Status                string     `json:"status"`
	Completeness          string     `json:"completeness"`
	TemplateKey           string     `json:"template_key"`
	TemplateVersion       int64      `json:"template_version"`
	OutlineConfirmation   string     `json:"outline_confirmation"`
	ExecutionSnapshotHash string     `json:"execution_snapshot_hash"`
	PublishedAt           *time.Time `json:"published_at"`
	SupersededAt          *time.Time `json:"superseded_at"`
	CreatedAt             time.Time  `json:"created_at"`
	// IsCurrent is the report's own pointer, not "the newest row". A regenerate that
	// is still running leaves the pointer on the published base, and a history list
	// that marks the attempt as current would tell the reader their published report
	// is the draft that has not finished (the R13/§7.10 defect class).
	IsCurrent bool `json:"is_current"`
}

// ListReportRevisions returns the report's version history for its owner.
func (s *Service) ListReportRevisions(
	ctx context.Context, actorID, tenantID, reportID string,
) ([]ReportRevisionSummary, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	rows, err := s.Store.ListReportRevisions(ctx, tenantID, report.ID, 0)
	if err != nil {
		return nil, err
	}
	summaries := make([]ReportRevisionSummary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, ReportRevisionSummary{
			ID: row.ID, Revision: row.Revision, Status: row.Status,
			Completeness: row.Completeness, TemplateKey: row.TemplateKey,
			TemplateVersion:       row.TemplateVersion,
			OutlineConfirmation:   row.OutlineConfirmation,
			ExecutionSnapshotHash: row.ExecutionSnapshotHash,
			PublishedAt:           row.PublishedAt, SupersededAt: row.SupersededAt,
			CreatedAt: row.CreatedAt,
			IsCurrent: report.CurrentRevisionID != "" && report.CurrentRevisionID == row.ID,
		})
	}
	return summaries, nil
}

// ReportSectionView is one section of a revision, body included.
//
// The job snapshot answers "what is happening"; this answers "what does it say".
// They are separate reads on purpose: the snapshot is polled on every SSE event and
// must stay small, while a body is read once when someone actually opens the report.
// Shipping bodies in the snapshot would multiply the cost of following a run by the
// size of the document.
type ReportSectionView struct {
	ID             string `json:"id"`
	Ord            int    `json:"ord"`
	SectionKey     string `json:"section_key"`
	Title          string `json:"title"`
	SectionVersion int    `json:"section_version"`
	Status         string `json:"status"`
	QualityStatus  string `json:"quality_status"`
	EvidenceStatus string `json:"evidence_status"`
	// RequiresEvidence lets the reader explain an empty body ("这一节本来不要求证据"),
	// instead of leaving the reader to guess why a section is blank.
	RequiresEvidence bool   `json:"requires_evidence"`
	CitationCount    int    `json:"citation_count"`
	ContentMD        string `json:"content_md"`
	GapReason        string `json:"gap_reason"`
}

// ListReportSections returns one revision's sections with their bodies.
//
// It reads the revision by number rather than `current_revision_id` so a historical
// revision can be read in full (A41); the caller that wants "what is live" passes the
// current number, which the read aggregate already gave it.
func (s *Service) ListReportSections(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64,
) ([]ReportSectionView, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeReportRead(ctx, actorID, report); err != nil {
		return nil, err
	}
	if revisionNo <= 0 {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	revision, err := s.Store.GetReportRevision(ctx, tenantID, report.ID, revisionNo)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, errReportRevisionNotFound(revisionNo)
	}
	rows, err := s.Store.ListReportSections(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	views := make([]ReportSectionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, ReportSectionView{
			ID: row.ID, Ord: row.Ord, SectionKey: row.SectionKey, Title: row.Title,
			SectionVersion: row.SectionVersion, Status: row.Status,
			QualityStatus: row.QualityStatus, EvidenceStatus: row.EvidenceStatus,
			RequiresEvidence: row.RequiresEvidence, CitationCount: row.CitationCount,
			ContentMD: row.ContentMD, GapReason: row.GapReason,
		})
	}
	return views, nil
}

// reportCurrentRevision resolves what the detail page shows: the report's
// `current_revision_id`, not "the newest row".
//
// The pointer only moves inside the publish transaction (doc/288 §7.10 提升动作 /
// A32), so a regenerate that is still running, failed or was cancelled leaves it
// on the published base. Reading `ORDER BY revision DESC` instead showed the
// *attempt* — live full7: after cancelling v3 the detail page answered
// `revision=3 cancelled`, i.e. the user's published report disappeared behind a
// cancelled draft, and the artifact list came from the revision that has none.
//
// The fallback covers rows whose pointer is empty (a hand-repaired row, or a
// report whose only revision row was purged): the newest revision is then the
// honest answer, and `nil` when there is none at all.
func (s *Service) reportCurrentRevision(
	ctx context.Context, tenantID string, report *model.Report,
) (*model.ReportRevision, error) {
	if report.CurrentRevisionID != "" {
		revision, err := s.Store.GetReportRevisionByID(ctx, tenantID, report.CurrentRevisionID)
		if err != nil {
			return nil, err
		}
		if revision != nil {
			return revision, nil
		}
	}
	return s.Store.GetReportRevision(ctx, tenantID, report.ID, 0)
}

// reportDetailWithEvidence completes the aggregate with the evidence-verifiability
// facts. It runs after the access checks, so a caller who may not read the report
// never reaches the citation join.
func (s *Service) reportDetailWithEvidence(
	ctx context.Context, report *model.Report, revision *model.ReportRevision,
) (*ReportDetail, error) {
	detail := &ReportDetail{Report: report, Revision: revision, EvidenceVerifiable: true}
	now := time.Now().UTC()
	artifacts, err := s.ListReportArtifacts(ctx, report.TenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	detail.Artifacts = artifacts
	unverifiable, err := s.Store.CountUnverifiableReportEvidence(ctx, report.TenantID, revision.ID, now)
	if err != nil {
		return nil, err
	}
	if unverifiable > 0 {
		detail.EvidenceVerifiable = false
		// The precise instant is not available from a count, and inventing one would
		// be worse than naming the fact that it is already true: a client rendering
		// "verification ended at <now>" is telling the truth, because by the time it
		// reads this the evidence *is* gone.
		detail.EvidenceExpiredAt = &now
	}
	return detail, nil
}

// authorizeReportRead applies the report's access policy to one principal.
// `report == nil` is the same answer as "somebody else's report": 44109.
func (s *Service) authorizeReportRead(ctx context.Context, actorID string, report *model.Report) error {
	if report == nil {
		return errReportNotFound()
	}
	if report.OwnerUserID == actorID {
		return nil
	}
	// A platform administrator may read across tenants through the governance
	// surface; that is an audit read, not a share. `tenant_members` and the two
	// selected_* policies stay frozen until P1 (292 D6), so they must not fall
	// through to "same tenant = allowed".
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	if s.canAccessAcrossTenant(ac, "read") {
		return nil
	}
	return errReportNotFound()
}

// ListReports returns the caller's own live reports, newest first.
//
// The narrowing to `actorID` is not a convenience filter: P0-A has no shared
// reports, so a tenant-wide list would be the first place one colleague sees
// another's question text. A governance listing is a separate surface (doc/288
// §7.6 item 1) and is not exposed here.
func (s *Service) ListReports(ctx context.Context, actorID, tenantID string, page, pageSize int) ([]model.Report, int64, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * pageSize
	items, err := s.Store.ListReports(ctx, tenantID, actorID, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.Store.CountReports(ctx, tenantID, actorID)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListReportTemplatesView is the read side of the content-template catalog: one
// row per key, workspace fork first, retired baselines flagged rather than
// hidden (they explain historical reports). The payload is part of the view, so
// there is no separate "template detail" endpoint in P0-A: the only thing a
// detail route would add is a second way to read the same rows (doc/288 §11.4).
func (s *Service) ListReportTemplatesView(ctx context.Context, actorID, tenantID string) ([]ReportTemplateView, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report-template", tenantID, ""); err != nil {
		return nil, err
	}
	return s.ListReportTemplates(ctx, tenantID)
}
