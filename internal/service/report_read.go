package service

import (
	"context"
	"encoding/json"
	"strings"
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
// ReportDetailBudget is the live job's spend, in the shape the extension card needs.
//
// `MaxExtensionTokens` is the server's own bound (`reportBudgetExtensionMaxFactor`),
// published rather than recomputed in the browser: the rule "at most the current
// envelope again" is a governance decision, and a second copy of it in TypeScript
// would be a second thing to keep true.
type ReportDetailBudget struct {
	BudgetTokens       int64  `json:"budget_tokens"`
	SpentTokens        int64  `json:"spent_tokens"`
	AvailableTokens    int64  `json:"available_tokens"`
	MaxExtensionTokens int64  `json:"max_extension_tokens"`
	BudgetPolicy       string `json:"budget_policy"`
	Observing          bool   `json:"observing"`
}

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
	// Metrics is `metrics_json` decoded, so the reader can show what the revision's
	// own quality pass concluded without re-parsing an escaped string. R16's claim
	// support ratio and cross-section consistency issues live here (doc/288 §9.1 L3).
	// Nil means the revision never finalized: "not measured yet" is not "all good".
	Metrics json.RawMessage `json:"metrics,omitempty"`
	// Budget is the spend side of the *live* run (doc/296 §2.2). Nil means there is
	// nothing to extend: no job, or a job that already finalized. It rides on the
	// detail read because the extension card has to state the current envelope before
	// it can ask for more — a client that guessed it would either over-ask (refused)
	// or under-ask (a second approval round for a number it could have named).
	Budget *ReportDetailBudget `json:"budget,omitempty"`
	// Access is the sharing state (doc/296 §2.1): the policy, whether this caller
	// may edit it, and the named grantees with their names resolved. It rides on the
	// detail read instead of a `GET /reports/:id/access` route because a reader that
	// may open the report can always see how it is shared, and a second route would
	// be a second place to get the predicate wrong.
	Access *ReportAccessView `json:"access,omitempty"`
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
	return s.reportDetailWithEvidence(ctx, actorID, report, revision)
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
	RequiresEvidence bool `json:"requires_evidence"`
	CitationCount    int  `json:"citation_count"`
	// RewriteReason answers "was this paragraph written for this version?" (R14).
	// `reuse` means the body was carried over from an earlier revision by request;
	// empty means it was written here.
	RewriteReason string `json:"rewrite_reason"`
	// Claims are the structured claims of this section (R11 / doc/288 §9.2). They ride
	// on the section read rather than a route of their own: a claim has no meaning
	// apart from the paragraph it came from, and a separate collection would need its
	// own access rule, its own pagination and its own way of being wrong.
	Claims    []ReportSectionClaimView `json:"claims"`
	ContentMD string                   `json:"content_md"`
	GapReason string                   `json:"gap_reason"`
	// LocationHints are R17's locators, already rendered as display lines such as
	// 「页面索引：差旅报销制度」. The client prints them under the explicit
	// 「定位线索（非证据）」 label; they are never mixed into the citation list, and the
	// server sends no chunk ids or scores with them (doc/295 §2.1).
	LocationHints []string `json:"location_hints"`
	// ResearchPlan is R18's disclosure: `nil` for a standard section, and otherwise
	// the depth, the planning state and how much the extra queries added.
	ResearchPlan *reportDocumentResearch `json:"research_plan,omitempty"`
}

// ReportSectionClaimView is one claim as the reader receives it.
//
// `Markers` is derived from the stored text, not read from a column: claim rows keep
// no marker list, and the citation numbers are already in the sentence. Recovering
// them here keeps a single source of truth for "what this claim cites".
type ReportSectionClaimView struct {
	Ord           int    `json:"ord"`
	Text          string `json:"claim_text"`
	Type          string `json:"claim_type"`
	SupportStatus string `json:"support_status"`
	Markers       []int  `json:"markers"`
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
	// One read for the whole revision rather than one per section: the section list is
	// polled while a report is being read, and a per-section query would turn a ten
	// section report into eleven round trips.
	claimRows, err := s.Store.ListReportClaims(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	claimsBySection := make(map[string][]ReportSectionClaimView, len(rows))
	for _, claim := range claimRows {
		claimsBySection[claim.SectionID] = append(claimsBySection[claim.SectionID], ReportSectionClaimView{
			Ord: claim.Ord, Text: claim.ClaimText, Type: claim.ClaimType,
			SupportStatus: claim.SupportStatus, Markers: reportMarkersInText(claim.ClaimText),
		})
	}
	views := make([]ReportSectionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, ReportSectionView{
			ID: row.ID, Ord: row.Ord, SectionKey: row.SectionKey, Title: row.Title,
			SectionVersion: row.SectionVersion, Status: row.Status,
			QualityStatus: row.QualityStatus, EvidenceStatus: row.EvidenceStatus,
			RequiresEvidence: row.RequiresEvidence, CitationCount: row.CitationCount,
			RewriteReason: row.RewriteReason,
			Claims:        claimsBySection[row.ID],
			ContentMD:     row.ContentMD, GapReason: row.GapReason,
			LocationHints: reportDocumentSectionHints(row),
			ResearchPlan:  reportDocumentSectionResearch(row),
		})
	}
	return views, nil
}

// reportDetailBudgetOf describes the spend of the run an extension would touch.
//
// Only a *live* job can be extended: a finalized one has already been settled, and
// raising its envelope would change the meaning of a closed ledger. `nil` therefore
// covers both "never ran" and "already finished", and the card renders "no live run"
// instead of offering a request the service will refuse with 44104.
func (s *Service) reportDetailBudgetOf(
	ctx context.Context, report *model.Report,
) (*ReportDetailBudget, error) {
	job, err := s.Store.GetLatestReportJob(ctx, report.TenantID, report.ID)
	if err != nil {
		return nil, err
	}
	if job == nil || job.FinalizedAt != nil {
		return nil, nil
	}
	usage, err := s.ReportChargeUsageOf(ctx, report.TenantID, job)
	if err != nil {
		return nil, err
	}
	return &ReportDetailBudget{
		BudgetTokens: usage.Budget, SpentTokens: usage.Actual,
		AvailableTokens: usage.Available,
		// One request may add at most the current envelope again, so the template's
		// original number can at most double (doc/296 §2.2).
		MaxExtensionTokens: usage.Budget * reportBudgetExtensionMaxFactor,
		BudgetPolicy:       usage.BudgetPolicy, Observing: usage.Observing,
	}, nil
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
	ctx context.Context, actorID string, report *model.Report, revision *model.ReportRevision,
) (*ReportDetail, error) {
	detail := &ReportDetail{Report: report, Revision: revision, EvidenceVerifiable: true}
	access, err := s.reportAccessViewOf(ctx, actorID, report)
	if err != nil {
		return nil, err
	}
	detail.Access = access
	budget, err := s.reportDetailBudgetOf(ctx, report)
	if err != nil {
		return nil, err
	}
	detail.Budget = budget
	// A stored metrics payload is passed through verbatim: re-deriving it here would
	// describe today's data rather than the moment the revision was finalized, which
	// is the one thing a quality record must not do.
	if raw := strings.TrimSpace(revision.MetricsJSON); raw != "" {
		detail.Metrics = json.RawMessage(raw)
	}
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

// ReportListRow is one row of `GET /api/v1/reports` (doc/288 §7.6).
//
// The list has to answer two questions per row: "which reports do I have" and
// "where does each stand". `report.status` cannot answer the second one — it is a
// lifecycle flag (`active` = 存续, and nothing else is ever written), so it is
// still `active` on a report published last week. Rendering that flag as「进行中」
// told a reader their finished report was still generating (found in the
// 2026-11-18 live pass, doc/44). The delivery state is the current revision's
// status, so the list carries it: `revision_status` plus the frozen completeness
// and publish time the drawer would otherwise be the only place to see.
type ReportListRow struct {
	model.Report
	// Revision is the number of the current revision (0 when the report has no
	// revision row left — the retention purge removes both, so this is a
	// transient shape, not a state to render as a fact).
	Revision       int64      `json:"revision"`
	RevisionStatus string     `json:"revision_status"`
	Completeness   string     `json:"completeness"`
	PublishedAt    *time.Time `json:"published_at"`
}

// ListReports returns the live reports the caller may open, newest first: their own
// plus the ones shared with them by policy (doc/296 §2.1 / A67).
//
// The narrowing is the *shared predicate*, not a second policy: a report is listed
// when `authorizeReportRead` would open it. That is the whole point of §13's "列表
// SQL 必须与详情判定同源" — a list that answered "same tenant" while the detail
// answered "owner only" would either advertise a 404 or hide a report the owner
// deliberately shared.
//
// `status` is the lifecycle view (doc/297 §2.3): `active` (the default), `archived`
// or `all`. The predicate is applied inside the visibility struct so the page and its
// total are computed from one clause — a total that counts archived rows while the
// page hides them is the kind of "pagination is off by a few" nobody can reproduce.
func (s *Service) ListReports(
	ctx context.Context, actorID, tenantID string, page, pageSize int, status string,
) ([]ReportListRow, int64, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report", tenantID, ""); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	lifecycle, err := reportLifecycleFilter(status)
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	visibility, err := s.reportVisibilityFor(ctx, actorID, tenantID)
	if err != nil {
		return nil, 0, err
	}
	visibility.Status = lifecycle
	items, err := s.Store.ListVisibleReports(ctx, visibility, "", pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.Store.CountVisibleReports(ctx, visibility, "")
	if err != nil {
		return nil, 0, err
	}
	rows := make([]ReportListRow, 0, len(items))
	if len(items) == 0 {
		return rows, total, nil
	}
	pointerIDs := make([]string, 0, len(items))
	for _, item := range items {
		if item.CurrentRevisionID != "" {
			pointerIDs = append(pointerIDs, item.CurrentRevisionID)
		}
	}
	revisions, err := s.Store.ListReportRevisionsByID(ctx, tenantID, pointerIDs)
	if err != nil {
		return nil, 0, err
	}
	byID := make(map[string]model.ReportRevision, len(revisions))
	for _, revision := range revisions {
		byID[revision.ID] = revision
	}
	for _, item := range items {
		row := ReportListRow{Report: item}
		if revision, ok := byID[item.CurrentRevisionID]; ok {
			row.Revision = revision.Revision
			row.RevisionStatus = revision.Status
			row.Completeness = revision.Completeness
			row.PublishedAt = revision.PublishedAt
		}
		rows = append(rows, row)
	}
	return rows, total, nil
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
