package service

// Report sharing: the four access policies (doc/288 §13, doc/296 §2.1 / A65–A67).
//
// P0-A froze three of the four policies in the contract and only ever wrote
// `owner_only`. This file opens them, and the whole design is about one sentence:
// **the owner publishes one predicate, and every entry point reads it.**
//
// The four policies mean exactly this, no more:
//
//   - `owner_only`        → the owner (and a platform governance read).
//   - `tenant_members`    → every member of the report's own workspace.
//   - `selected_users`    → the owner plus the named users of the same workspace.
//   - `selected_groups`   → the owner plus the members of the named teams.
//
// Three properties are non-negotiable, because each of them has a specific failure
// mode that looks fine in a demo:
//
//  1. **"not visible" is 44109, never 403.** A colleague who may not read a report
//     must not be able to tell it apart from one that does not exist (§7.6). The
//     sharing UI is the one place that does answer "you are not the owner" — it is
//     a write, and it addresses a report you are already looking at.
//  2. **Sharing never widens a write.** `tenant_members` opens reading, not
//     regeneration, cancellation or deletion: those stay `owner` (+ platform
//     `manage`) through `assertReportOwner`. A shared reader who could delete the
//     report would make every share a one-click way to lose it.
//  3. **A stale scope payload authorizes nothing.** `access_scope_json` records the
//     request; the grant rows decide. Reading the JSON copy in the predicate would
//     let a hand-repaired or half-written row widen access after a revoke.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// reportAccessMaxGrantees bounds one report's grantee list. It is a guard against a
// client that sends a whole workspace's user ids: at that size the tenant-wide
// policy is both cheaper and honest about the intent.
const reportAccessMaxGrantees = 200

// ReportAccessGrantee is one named grantee as the UI receives it: the id it must
// send back, and a display name so the panel never has to print a machine id.
//
// `Missing` is set when the id no longer resolves (a deleted user or team). The
// grant row stays — removing it silently would change the policy behind the owner's
// back — and the UI shows "已失效" instead of an empty name.
type ReportAccessGrantee struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Missing bool   `json:"missing"`
}

// ReportAccessView is the sharing state of one report, as the detail read returns
// it: the policy, the viewer's own standing, and the grantees with names resolved.
type ReportAccessView struct {
	Policy string `json:"policy"`
	// IsOwner tells the UI whether the share editor is offered. It is not a
	// permission decision — the write route re-checks it — it is what stops a
	// reader from being handed a form that will only answer "not found".
	IsOwner bool `json:"is_owner"`
	// CanManage is true when this caller may change the policy: the owner, or a
	// platform administrator with `manage` (the same union the write enforces).
	CanManage bool                  `json:"can_manage"`
	Grantees  []ReportAccessGrantee `json:"grantees"`
}

// SetReportAccessRequest is the share editor's payload.
//
// Both lists are accepted together because one request must be able to express
// "switch from user A to team B" atomically: two requests would leave a window in
// which neither applies, and a crash between them would leave the report shared with
// a grantee the owner already removed.
type SetReportAccessRequest struct {
	Policy  string   `json:"policy"`
	UserIDs []string `json:"user_ids"`
	TeamIDs []string `json:"team_ids"`
}

// reportVisibilityFor resolves what the reader may see in one workspace.
//
// The team list is loaded once per request rather than joined per row: the list
// endpoint renders a page of reports, and a correlated team join would turn one
// page into N membership lookups.
func (s *Service) reportVisibilityFor(ctx context.Context, actorID, tenantID string) (repository.ReportVisibility, error) {
	teams, err := s.Store.ListReportTeamsOfUser(ctx, tenantID, actorID)
	if err != nil {
		return repository.ReportVisibility{}, err
	}
	return repository.ReportVisibility{TenantID: tenantID, ActorID: actorID, TeamIDs: teams}, nil
}

// authorizeReportRead is the row-level half of the shared predicate.
//
// Order matters: owner first (a single comparison, no queries), then the platform
// governance read, then the policy. The empty-tenant case fails closed — a report
// whose tenant is blank cannot be matched to anybody's membership.
func (s *Service) authorizeReportRead(ctx context.Context, actorID string, report *model.Report) error {
	if report == nil {
		return errReportNotFound()
	}
	if report.OwnerUserID == actorID {
		return nil
	}
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	// A platform administrator may read across tenants through the governance
	// surface; that is an audit read, not a share.
	if s.canAccessAcrossTenant(ac, "read") {
		return nil
	}
	// Same tenant is *not* enough: the policy decides. Reading the tenant here is
	// the historical bug this file exists to prevent (§13 "列表 SQL 必须与详情判定同源").
	if report.TenantID == "" || ac.user.TenantID != report.TenantID {
		return errReportNotFound()
	}
	if report.AccessPolicy == model.ReportAccessTenantMembers {
		return nil
	}
	// The team list is resolved only for `selected_groups`: a `selected_users` read
	// must not pay for a membership query, and an `owner_only` read must reach the
	// same "not found" through exactly the same predicate (so the two policies can
	// never drift into different answers).
	var teams []string
	if report.AccessPolicy == model.ReportAccessSelectedGroups {
		teams, err = s.Store.ListReportTeamsOfUser(ctx, report.TenantID, actorID)
		if err != nil {
			return err
		}
		if len(teams) == 0 {
			return errReportNotFound()
		}
	}
	visible, err := s.Store.ReportVisibleTo(ctx, repository.ReportVisibility{
		TenantID: report.TenantID, ActorID: actorID, TeamIDs: teams,
	}, report.ID)
	if err != nil {
		return err
	}
	if !visible {
		return errReportNotFound()
	}
	return nil
}

// reportAccessViewOf assembles the share panel's read model.
//
// Names are resolved in one query per kind (grantees are at most a page of ids), and
// a grantee that no longer resolves is marked `missing` rather than dropped: the
// owner must be able to see that a share points at a deleted team, otherwise the
// policy silently means something else than it says.
func (s *Service) reportAccessViewOf(
	ctx context.Context, actorID string, report *model.Report,
) (*ReportAccessView, error) {
	if report == nil {
		return nil, errReportNotFound()
	}
	view := &ReportAccessView{
		Policy:   report.AccessPolicy,
		IsOwner:  report.OwnerUserID == actorID,
		Grantees: []ReportAccessGrantee{},
	}
	if view.IsOwner {
		view.CanManage = true
	} else if ac, err := s.authz(ctx, actorID); err == nil {
		view.CanManage = s.canAccessAcrossTenant(ac, "manage")
	}
	grants, err := s.Store.ListReportAccessGrants(ctx, report.TenantID, report.ID)
	if err != nil {
		return nil, err
	}
	for _, grant := range grants {
		grantee := ReportAccessGrantee{Kind: grant.GrantKind, ID: grant.GrantID}
		switch grant.GrantKind {
		case model.ReportGrantUser:
			user, err := s.Store.GetUser(ctx, grant.GrantID)
			if err != nil {
				return nil, err
			}
			if user == nil || user.TenantID != report.TenantID || user.Status != model.UserStatusActive {
				grantee.Missing = true
			} else {
				grantee.Name = reportAccessUserLabel(user)
			}
		case model.ReportGrantTeam:
			team, err := s.Store.GetTeam(ctx, report.TenantID, grant.GrantID)
			if err != nil {
				return nil, err
			}
			if team == nil {
				grantee.Missing = true
			} else {
				grantee.Name = team.Name
			}
		default:
			// An unknown kind can only come from a hand-edited row; it is shown as
			// missing so the owner can revoke it.
			grantee.Missing = true
		}
		view.Grantees = append(view.Grantees, grantee)
	}
	return view, nil
}

// reportAccessUserLabel is the display name of a grantee. The user model has no
// separate display name (doc/296 §2.1 note), so the username *is* the human label the
// product already shows in the member list. The id is never used: the share panel must
// not print a machine identifier.
func reportAccessUserLabel(user *model.User) string {
	if user == nil {
		return ""
	}
	return strings.TrimSpace(user.Username)
}

// SetReportAccess replaces one report's sharing policy.
//
// Only the owner (or a platform administrator with `manage`) may call it: a shared
// reader who could re-share would turn one deliberate share into a workspace-wide
// one. The 44109 answer for everybody else keeps the existence rule intact.
func (s *Service) SetReportAccess(
	ctx context.Context, actorID, tenantID, reportID string, req SetReportAccessRequest,
) (*ReportAccessView, error) {
	if actorID == "" || tenantID == "" {
		return nil, httperr.Forbidden("report sharing requires an authenticated workspace")
	}
	if err := s.AuthorizeObject(ctx, actorID, "execute", "report", tenantID, ""); err != nil {
		return nil, err
	}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, errReportNotFound()
	}
	if err := s.assertReportOwner(ctx, actorID, report); err != nil {
		return nil, err
	}
	// An archived report produces nothing new (doc/297 §2): unarchive first.
	if err := s.assertReportArchivedBlocksWork(report); err != nil {
		return nil, err
	}
	policy := strings.TrimSpace(req.Policy)
	if !containsString(model.ReportAccessPolicies, policy) {
		return nil, errReportAccessInvalid("access policy must be one of " + strings.Join(model.ReportAccessPolicies, ", "))
	}
	userIDs := reportAccessNormalizeIDs(req.UserIDs)
	teamIDs := reportAccessNormalizeIDs(req.TeamIDs)
	switch policy {
	case model.ReportAccessOwnerOnly, model.ReportAccessTenantMembers:
		if len(userIDs) > 0 || len(teamIDs) > 0 {
			// Silently dropping the lists would leave the panel showing grantees that
			// the policy does not use, and the next edit would look like it re-added
			// them. The refusal is the honest answer.
			return nil, errReportAccessInvalid("this policy does not take a grantee list")
		}
	case model.ReportAccessSelectedUsers:
		if len(teamIDs) > 0 {
			return nil, errReportAccessInvalid("selected_users does not take team ids")
		}
		if len(userIDs) == 0 {
			return nil, errReportAccessInvalid("selected_users requires at least one user id")
		}
	case model.ReportAccessSelectedGroups:
		if len(userIDs) > 0 {
			return nil, errReportAccessInvalid("selected_groups does not take user ids")
		}
		if len(teamIDs) == 0 {
			return nil, errReportAccessInvalid("selected_groups requires at least one team id")
		}
	}
	if len(userIDs)+len(teamIDs) > reportAccessMaxGrantees {
		return nil, errReportAccessInvalid("one report can name at most 200 grantees; use the tenant-wide policy instead")
	}
	now := time.Now().UTC()
	previousPolicy := report.AccessPolicy
	grants := make([]model.ReportAccessGrant, 0, len(userIDs)+len(teamIDs))
	// Every id is validated against the report's own workspace. An id from another
	// tenant would never grant access (the predicate joins on tenant) but would sit
	// in the editor looking like a share, so it is refused at write time.
	for _, userID := range userIDs {
		user, err := s.Store.GetUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if user == nil || user.TenantID != tenantID {
			return nil, errReportAccessInvalid("user " + userID + " is not a member of this workspace")
		}
		if user.ID == report.OwnerUserID {
			// The owner is always allowed; recording them as a grantee would make the
			// panel list the owner as somebody the report was shared with.
			continue
		}
		grants = append(grants, model.ReportAccessGrant{
			ID:       reportAccessGrantID(report.ID, model.ReportGrantUser, userID),
			TenantID: tenantID, ReportID: report.ID, GrantKind: model.ReportGrantUser,
			GrantID: userID, CreatedBy: actorID, CreatedAt: now,
		})
	}
	for _, teamID := range teamIDs {
		team, err := s.Store.GetTeam(ctx, tenantID, teamID)
		if err != nil {
			return nil, err
		}
		if team == nil {
			return nil, errReportAccessInvalid("team " + teamID + " does not exist in this workspace")
		}
		grants = append(grants, model.ReportAccessGrant{
			ID:       reportAccessGrantID(report.ID, model.ReportGrantTeam, teamID),
			TenantID: tenantID, ReportID: report.ID, GrantKind: model.ReportGrantTeam,
			GrantID: teamID, CreatedBy: actorID, CreatedAt: now,
		})
	}
	scope := model.ReportAccessScope{}
	if len(userIDs) > 0 {
		scope.UserIDs = userIDs
	}
	if len(teamIDs) > 0 {
		scope.TeamIDs = teamIDs
	}
	scopeJSON := ""
	if len(scope.UserIDs) > 0 || len(scope.TeamIDs) > 0 {
		encoded, err := json.Marshal(scope)
		if err != nil {
			return nil, httperr.Internal("failed to encode report access scope")
		}
		scopeJSON = string(encoded)
	}
	applied, err := s.Store.ReportAccessWrite(ctx, tenantID, report.ID, policy, scopeJSON, grants, now)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, errReportNotFound()
	}
	report.AccessPolicy = policy
	report.AccessScopeJSON = scopeJSON
	report.UpdatedAt = now
	view, err := s.reportAccessViewOf(ctx, actorID, report)
	if err != nil {
		return nil, err
	}
	detail, err := json.Marshal(map[string]interface{}{
		"policy": policy, "users": len(userIDs), "teams": len(teamIDs),
		// The counts, not the ids: an audit row is read by an auditor who must be
		// able to see that a share happened without the trail becoming a user
		// directory (the same redaction rule as the charge audit).
		"previous_policy": previousPolicy,
	})
	if err != nil {
		detail = []byte("{}")
	}
	if auditErr := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: actorID, Action: "report.access.updated",
		Resource: "report", ResourceID: report.ID, Result: "SUCCESS",
		DetailJSON: string(detail), At: now,
	}); auditErr != nil {
		// The policy is already written. Failing the request now would tell the owner
		// nothing happened when it did; the gap is logged loudly instead (same rule as
		// the outline confirmation).
		logger.Warn("report access audit failed", "report_id", report.ID, "error", auditErr)
	}
	logger.Info("report access updated",
		"report_id", report.ID, "policy", policy, "users", len(userIDs), "teams", len(teamIDs))
	return view, nil
}

// reportAccessNormalizeIDs drops blanks, trims, and dedupes while keeping the
// caller's order (the panel shows grantees in the order they were added).
func reportAccessNormalizeIDs(input []string) []string {
	out := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, raw := range input {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

// reportAccessGrantID is a deterministic id for one (report, kind, grantee) triple.
//
// The unique index would refuse a duplicate anyway; deriving the id means a replayed
// write replaces the same row instead of failing on a fresh random one, which is
// what makes "save the same policy twice" idempotent (A66). The value is 32 hex
// characters, the width of every other id on this surface.
func reportAccessGrantID(reportID, kind, grantID string) string {
	sum := sha256.Sum256([]byte(reportID + "|" + kind + "|" + grantID))
	return hex.EncodeToString(sum[:16])
}
