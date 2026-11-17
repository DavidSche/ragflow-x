package service

import (
	"context"

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
	revision, err := s.Store.GetReportRevision(ctx, tenantID, report.ID, 0)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		// A report without a revision cannot exist: creation writes both in one
		// transaction, and the retention purge removes both. Answering the honest
		// "gone" is better than returning a half aggregate.
		return nil, httperr.New(404, codeReportRevisionNotFound, "report revision is no longer available")
	}
	return &ReportDetail{Report: report, Revision: revision}, nil
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
