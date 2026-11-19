package repository

// Shared access predicate for the report surface (doc/296 §2.1 / A65).
//
// The rule the whole file exists for: **the list and the detail must answer from
// the same predicate.** A `WHERE` clause in the list and an `if` in the detail are
// two implementations of one policy, and the day they disagree the product either
// hides a report the owner shared with you (a complaint) or shows one it did not
// (a leak). Everything below is therefore written once:
//
//   - `reportAccessCondition` is the SQL form, parameterised on a table prefix so
//     the list query can use it on `rgx_report` and the visibility probe can use
//     it on a joined/sub-selected row;
//   - `service.authorizeReportRead` is the row form, and it reads the *same*
//     grants the SQL joins.
//
// Two shapes are deliberate:
//
//   - `owner_only` grants nothing beyond the owner, and the SQL only ever tests
//     `owner_user_id = ?` for that policy: a shared report has to be visible
//     through `tenant_members` or a grant row, never through "same tenant".
//   - `tenant_members` is an equality test on the policy column, not a tenant
//     comparison. Both are true for the same rows today, but the column is what
//     the owner actually published, and reading the tenant instead would make
//     "share with everyone" the default the moment the policy write failed.

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ReportVisibility is the caller's standing for one tenant, as far as the report
// list is concerned.
//
// It is a value, not a predicate closure, so a caller can log or test it: "why did
// this row show up" must be answerable without re-running the query.
type ReportVisibility struct {
	TenantID string
	// ActorID is the reader. The owner clause comes from this field alone.
	ActorID string
	// TeamIDs are the teams the reader belongs to *in this tenant*. They are
	// resolved by the service (one query) rather than joined here, so the same list
	// can be reused by a caller that already knows its teams.
	TeamIDs []string
	// Status narrows the list to one lifecycle value (doc/297 §2.3); empty means
	// "every status", which is what the `all` view asks for. It lives here rather
	// than in the call's arguments because it must narrow `ListVisibleReports` and
	// `CountVisibleReports` identically — a page and its total that disagree is the
	// one defect a hand-written count clause always produces.
	Status string
}

// reportAccessCondition is the single SQL predicate behind "may this reader see
// this report".
//
// `prefix` is the table alias of the report row ("", "r.") — nothing else is ever
// interpolated, and every id goes through a placeholder.
//
// The report columns are always table-qualified. Without the qualifier the grant
// subquery resolves `report_id = id` against the grant's *own* `id` column, which
// is a silently false predicate: the query runs, the count is zero, and the owner's
// share simply never shows up. That is exactly the class of bug this file's
// "one predicate" rule is meant to rule out, so the qualification is written here
// once instead of being repeated by three callers.
func reportAccessCondition(prefix string) string {
	table := "rgx_report"
	if prefix != "" {
		table = strings.TrimSuffix(prefix, ".")
	}
	owner := table + ".owner_user_id"
	policy := table + ".access_policy"
	id := table + ".id"
	tenant := table + ".tenant_id"
	return "(" + owner + " = ? OR " + policy + " = '" + model.ReportAccessTenantMembers + "'" +
		" OR EXISTS (SELECT 1 FROM rgx_report_access_grant g WHERE g.report_id = " + id +
		" AND g.tenant_id = " + tenant + " AND g.grant_kind = '" + model.ReportGrantUser +
		"' AND g.grant_id = ?)" +
		" OR EXISTS (SELECT 1 FROM rgx_report_access_grant g WHERE g.report_id = " + id +
		" AND g.tenant_id = " + tenant + " AND g.grant_kind = '" + model.ReportGrantTeam +
		"' AND g.grant_id IN ?))"
}

// reportAccessArgs are the bindings of `reportAccessCondition`, in order.
//
// An empty team list is expanded to a single impossible id rather than skipped:
// GORM renders `IN ()` as `IN (NULL)`, but relying on that would make "the reader
// has no teams" depend on a dialect detail, and the branch that skipped the clause
// entirely would be a silent "all teams" if someone edited it later.
func reportAccessArgs(visibility ReportVisibility) []interface{} {
	teams := visibility.TeamIDs
	if len(teams) == 0 {
		teams = []string{"-"}
	}
	return []interface{}{visibility.ActorID, visibility.ActorID, teams}
}

// ListVisibleReports returns the reports a reader may open, newest first.
//
// `ownerUserID` narrows to "my reports" on top of the visibility rule; it is not
// itself an access decision (the predicate already allows the owner), it is the
// list filter the UI asks for. An empty value lists everything visible in the
// tenant, which is what `tenant_members` and a grant are for.
func (s *store) ListVisibleReports(
	ctx context.Context, visibility ReportVisibility, ownerUserID string, limit, offset int,
) ([]model.Report, error) {
	if limit <= 0 || limit > maxPageSize {
		limit = maxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	q := s.WithContext(ctx).
		Where("tenant_id = ? AND deleted_at IS NULL", visibility.TenantID).
		Where(reportAccessCondition(""), reportAccessArgs(visibility)...)
	if visibility.Status != "" {
		q = q.Where("status = ?", visibility.Status)
	}
	if ownerUserID != "" {
		q = q.Where("owner_user_id = ?", ownerUserID)
	}
	var reports []model.Report
	err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&reports).Error
	return reports, err
}

// CountVisibleReports counts the same set as `ListVisibleReports` without paging.
// The two share one predicate, so the page and the total cannot describe different
// sets (the defect a hand-written count clause produces).
func (s *store) CountVisibleReports(
	ctx context.Context, visibility ReportVisibility, ownerUserID string,
) (int64, error) {
	q := s.WithContext(ctx).Model(&model.Report{}).
		Where("tenant_id = ? AND deleted_at IS NULL", visibility.TenantID).
		Where(reportAccessCondition(""), reportAccessArgs(visibility)...)
	if visibility.Status != "" {
		q = q.Where("status = ?", visibility.Status)
	}
	if ownerUserID != "" {
		q = q.Where("owner_user_id = ?", ownerUserID)
	}
	var count int64
	err := q.Count(&count).Error
	return count, err
}

// ReportVisibleTo answers the detail-page half of the shared predicate.
//
// It is a separate method because a read of one report already has the row in
// hand; asking the database "is this visible to me" after loading it is one
// equality check, not a second policy.
func (s *store) ReportVisibleTo(
	ctx context.Context, visibility ReportVisibility, reportID string,
) (bool, error) {
	if reportID == "" {
		return false, nil
	}
	var count int64
	err := s.WithContext(ctx).Model(&model.Report{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", visibility.TenantID, reportID).
		Where(reportAccessCondition(""), reportAccessArgs(visibility)...).
		Count(&count).Error
	return count > 0, err
}

// ListReportAccessGrants returns the named grantees of one report, in insertion
// order so the UI can show the owner's list as they built it.
func (s *store) ListReportAccessGrants(
	ctx context.Context, tenantID, reportID string,
) ([]model.ReportAccessGrant, error) {
	var grants []model.ReportAccessGrant
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND report_id = ?", tenantID, reportID).
		Order("created_at ASC, grant_kind ASC, grant_id ASC").
		Find(&grants).Error
	return grants, err
}

// ListReportAccessGrantsFor answers "is this reader named on this report" for the
// detail check, without loading every grantee.
func (s *store) ListReportAccessGrantsFor(
	ctx context.Context, tenantID, reportID, grantKind, grantID string,
) ([]model.ReportAccessGrant, error) {
	var grants []model.ReportAccessGrant
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND report_id = ? AND grant_kind = ? AND grant_id = ?",
			tenantID, reportID, grantKind, grantID).
		Limit(1).Find(&grants).Error
	return grants, err
}

// ListReportTeamsOfUser returns the teams a user belongs to, inside one tenant.
//
// Teams are tenant-scoped (`rgx_team.tenant_id`), while membership itself carries
// no tenant: the join is what keeps a cross-tenant membership from widening report
// access in the wrong workspace.
func (s *store) ListReportTeamsOfUser(
	ctx context.Context, tenantID, userID string,
) ([]string, error) {
	if tenantID == "" || userID == "" {
		return nil, nil
	}
	ids := []string{}
	err := s.WithContext(ctx).
		Model(&model.UserTeam{}).
		Select("rgx_user_team.team_id").
		Joins("JOIN rgx_team ON rgx_team.id = rgx_user_team.team_id").
		Where("rgx_user_team.user_id = ? AND rgx_team.tenant_id = ?", userID, tenantID).
		Order("rgx_user_team.team_id ASC").
		Scan(&ids).Error
	return ids, err
}

// ReportAccessWrite replaces a report's policy, its scope payload and its grants
// in one transaction.
//
// Replace-not-merge is the rule: a PATCH that merged would leave a revoked grant
// behind every time the owner removed one, and the revoke would silently not
// happen. `RowsAffected == 0` means the report vanished (or moved contents) between
// the service's read and this write; the caller re-reads to explain.
func (s *store) ReportAccessWrite(
	ctx context.Context, tenantID, reportID, policy, scopeJSON string,
	grants []model.ReportAccessGrant, now time.Time,
) (bool, error) {
	applied := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Report{}).
			Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, reportID).
			Updates(map[string]interface{}{
				"access_policy":     policy,
				"access_scope_json": scopeJSON,
				"updated_at":        now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Where("tenant_id = ? AND report_id = ?", tenantID, reportID).
			Delete(&model.ReportAccessGrant{}).Error; err != nil {
			return err
		}
		if len(grants) == 0 {
			applied = true
			return nil
		}
		if err := tx.Create(&grants).Error; err != nil {
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
