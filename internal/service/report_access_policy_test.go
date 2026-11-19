package service

// Sharing and budget governance (doc/296 §2.1–§2.3, A19/A65–A71).
//
// Two things are pinned here, and both are cross-entry-point facts rather than unit
// behaviour:
//
//  ① **The four sharing policies mean the same thing on every door.** A report is
//     reachable through the list, the detail read and the artifact download, and the
//     single most likely defect in a sharing feature is that one of the three was
//     taught a different rule. The test walks all four policies through all three
//     doors, including the revoke direction: a grant that survives "save again with a
//     smaller list" is a silent security bug, and `ReportAccessWrite` replaces the
//     whole table for exactly that reason.
//  ② **`budget_policy=observe` never refuses a call, and an envelope only grows
//     through the gate.** Observe is a measuring mode, not a free-spending mode: the
//     ledger still records, the audit still fires, and the only difference is that
//     the gate stops saying no. And an extension may only be an increment on a live
//     job — never a rewrite of what was already spent.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// reportRow reads one report so a test can assert what an operation did *not* touch.
func (fx *reportFixture) reportRow(t *testing.T, reportID string) *model.Report {
	t.Helper()
	var row model.Report
	err := fx.gdb.Where("tenant_id = ? AND id = ?", fx.tenantID, reportID).First(&row).Error
	if err != nil {
		t.Fatalf("load report %s: %v", reportID, err)
	}
	return &row
}

// reportAccessActor builds one more person in the same workspace.
func reportAccessActor(t *testing.T, fx *reportFixture, name string) *model.User {
	t.Helper()
	user, err := fx.svc.CreateUser(context.Background(), fx.tenantID, "", CreateUserRequest{
		Username: name, Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return user
}

// reportAccessTeam builds one team in the workspace and puts `members` in it.
func reportAccessTeam(t *testing.T, fx *reportFixture, name string, members ...*model.User) *model.Team {
	t.Helper()
	ctx := context.Background()
	team, err := fx.svc.CreateTeam(ctx, fx.tenantID, name, fx.owner.ID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	for _, member := range members {
		if err := fx.svc.AddUserToTeam(ctx, fx.tenantID, team.ID, member.ID); err != nil {
			t.Fatalf("add %s to team: %v", member.ID, err)
		}
	}
	return team
}

// reportAccessSeen answers "does this person see this report?" through all three
// doors at once, so a test can never accidentally check only the easy one.
type reportAccessSeen struct {
	listed   bool
	detail   bool
	download bool
}

// reportAccessProbe walks the three doors. `sameWorkspace` is false for an actor of
// another workspace, whose answer is "permission denied" at the role gate — a stronger
// denial than 44109 and therefore also acceptable, but a different fact, so the caller
// must say which one it expects instead of letting one stand in for the other.
func reportAccessProbe(
	t *testing.T, fx *reportFixture, actor *model.User, reportID string, revisionNo int64,
	artifactID string, sameWorkspace bool,
) reportAccessSeen {
	t.Helper()
	ctx := context.Background()
	seen := reportAccessSeen{}
	// `status=all`: this probe asks "may this actor see the row at all", and the
	// lifecycle view must not be what decides it (doc/297 §2.3).
	rows, _, err := fx.svc.ListReports(ctx, actor.ID, fx.tenantID, 1, 50, "all")
	if err != nil {
		if !sameWorkspace {
			return seen
		}
		t.Fatalf("list as %s: %v", actor.Username, err)
	}
	for _, row := range rows {
		if row.ID == reportID {
			seen.listed = true
		}
	}
	if _, err := fx.svc.GetReport(ctx, actor.ID, fx.tenantID, reportID); err == nil {
		seen.detail = true
	} else if sameWorkspace && !isReportNotFound(err) {
		// A 403 here would be the other half of the bug: it discloses existence.
		t.Fatalf("detail as %s must be 44109, got %v", actor.Username, err)
	}
	if artifactID != "" {
		if _, err := fx.svc.OpenReportArtifact(ctx, actor.ID, fx.tenantID, reportID, revisionNo, artifactID); err == nil {
			seen.download = true
		} else if sameWorkspace && !isReportNotFound(err) {
			t.Fatalf("download as %s must be 44109, got %v", actor.Username, err)
		}
	}
	return seen
}

func isReportNotFound(err error) bool {
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Code == codeReportAccessDenied && httpErr.Status == 404
}

func TestReportSharingPoliciesAgreeOnEveryDoor(t *testing.T) {
	fx := newReportFixture(t, "access_policy")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}
	exported, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("export report: %v", err)
	}
	artifactID := exported.Artifact.ID

	colleague := reportAccessActor(t, fx, "report-share-colleague")
	other := reportAccessActor(t, fx, "report-share-other")
	team := reportAccessTeam(t, fx, "合规审阅组", colleague)
	// A person from a different workspace. They hold the same role, so the only thing
	// keeping them out is the tenant predicate — which is the point.
	otherTenantID, outsider := fx.secondWorkspace(t, "share-outsider")

	setPolicy := func(policy string, userIDs, teamIDs []string) {
		t.Helper()
		if _, err := fx.svc.SetReportAccess(ctx, fx.owner.ID, fx.tenantID, report.ID, SetReportAccessRequest{
			Policy: policy, UserIDs: userIDs, TeamIDs: teamIDs,
		}); err != nil {
			t.Fatalf("set policy %s: %v", policy, err)
		}
	}

	// ① owner_only (the default the row was created with): nobody else, no matter
	// which door they try.
	if report.AccessPolicy != model.ReportAccessOwnerOnly {
		t.Fatalf("a fresh report must start owner_only, got %q", report.AccessPolicy)
	}
	for _, actor := range []*model.User{colleague, other} {
		if seen := reportAccessProbe(t, fx, actor, report.ID, revision.Revision, artifactID, true); seen != (reportAccessSeen{}) {
			t.Fatalf("owner_only leaked to %s: %+v", actor.Username, seen)
		}
	}
	if seen := reportAccessProbe(t, fx, outsider, report.ID, revision.Revision, artifactID, false); seen != (reportAccessSeen{}) {
		t.Fatalf("owner_only leaked to another workspace's admin: %+v", seen)
	}
	if seen := reportAccessProbe(t, fx, fx.owner, report.ID, revision.Revision, artifactID, true); !seen.listed || !seen.detail || !seen.download {
		t.Fatalf("the owner must pass every door, got %+v", seen)
	}

	// ② tenant_members: everyone in the workspace, and nobody outside it.
	setPolicy(model.ReportAccessTenantMembers, nil, nil)
	for _, actor := range []*model.User{colleague, other} {
		if seen := reportAccessProbe(t, fx, actor, report.ID, revision.Revision, artifactID, true); !seen.listed || !seen.detail || !seen.download {
			t.Fatalf("tenant_members must open every door for %s, got %+v", actor.Username, seen)
		}
	}
	if seen := reportAccessProbe(t, fx, outsider, report.ID, revision.Revision, artifactID, false); seen != (reportAccessSeen{}) {
		t.Fatalf("tenant_members leaked across workspaces to %s: %+v", outsider.Username, seen)
	}
	// The grant table is empty under this policy: writing it would leave rows that
	// re-apply silently the moment somebody switches back to selected_users.
	if grants, err := fx.svc.Store.ListReportAccessGrants(ctx, fx.tenantID, report.ID); err != nil || len(grants) != 0 {
		t.Fatalf("tenant_members must not store grantees, got %d (%v)", len(grants), err)
	}

	// ③ selected_users: exactly the named person.
	setPolicy(model.ReportAccessSelectedUsers, []string{colleague.ID}, nil)
	if seen := reportAccessProbe(t, fx, colleague, report.ID, revision.Revision, artifactID, true); !seen.listed || !seen.detail || !seen.download {
		t.Fatalf("selected_users must open every door for the grantee, got %+v", seen)
	}
	if seen := reportAccessProbe(t, fx, other, report.ID, revision.Revision, artifactID, true); seen != (reportAccessSeen{}) {
		t.Fatalf("selected_users leaked to an unnamed colleague: %+v", seen)
	}
	// Replacing the list really revokes: the previous grantee loses every door.
	setPolicy(model.ReportAccessSelectedUsers, []string{other.ID}, nil)
	if seen := reportAccessProbe(t, fx, colleague, report.ID, revision.Revision, artifactID, true); seen != (reportAccessSeen{}) {
		t.Fatalf("a revoked grantee kept access: %+v", seen)
	}
	if seen := reportAccessProbe(t, fx, other, report.ID, revision.Revision, artifactID, true); !seen.listed || !seen.detail {
		t.Fatalf("the new grantee must be able to read, got %+v", seen)
	}

	// ④ selected_groups: membership decides, and leaving the team revokes.
	setPolicy(model.ReportAccessSelectedGroups, nil, []string{team.ID})
	if seen := reportAccessProbe(t, fx, colleague, report.ID, revision.Revision, artifactID, true); !seen.listed || !seen.detail || !seen.download {
		t.Fatalf("selected_groups must open every door to a member, got %+v", seen)
	}
	if seen := reportAccessProbe(t, fx, other, report.ID, revision.Revision, artifactID, true); seen != (reportAccessSeen{}) {
		t.Fatalf("selected_groups leaked to a non-member: %+v", seen)
	}
	if err := fx.svc.RemoveUserFromTeam(ctx, fx.tenantID, team.ID, colleague.ID); err != nil {
		t.Fatalf("remove from team: %v", err)
	}
	if seen := reportAccessProbe(t, fx, colleague, report.ID, revision.Revision, artifactID, true); seen != (reportAccessSeen{}) {
		t.Fatalf("removing a user from the granted team must revoke: %+v", seen)
	}

	// A grant naming a workspace member cannot be used from the other workspace even
	// if the same user id were somehow reachable: the tenant predicate is applied in
	// SQL, so this stays true without the service re-checking it.
	if otherTenantID == fx.tenantID {
		t.Fatal("fixture built two workspaces that are the same workspace")
	}
}

// The view is what the panel renders, and it must not describe a grant the predicate
// ignores: an owner who is also "shared with" would be listed as a grantee even though
// no extra access was created.
func TestReportAccessViewNamesGranteesAndFlagsMissingOnes(t *testing.T) {
	fx := newReportFixture(t, "access_view")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-access-view-0001")
	colleague := reportAccessActor(t, fx, "report-view-colleague")
	team := reportAccessTeam(t, fx, "阅读小组", colleague)

	view, err := fx.svc.SetReportAccess(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, SetReportAccessRequest{
		Policy: model.ReportAccessSelectedUsers, UserIDs: []string{colleague.ID, fx.owner.ID},
	})
	if err != nil {
		t.Fatalf("set access: %v", err)
	}
	if view.Policy != model.ReportAccessSelectedUsers || !view.IsOwner || !view.CanManage {
		t.Fatalf("unexpected view header: %+v", view)
	}
	if len(view.Grantees) != 1 || view.Grantees[0].ID != colleague.ID || view.Grantees[0].Kind != model.ReportGrantUser {
		t.Fatalf("the owner must not be listed as a grantee: %+v", view.Grantees)
	}
	if view.Grantees[0].Name == "" || view.Grantees[0].Missing {
		t.Fatalf("a live grantee must carry its display name: %+v", view.Grantees[0])
	}

	// A grantee that has since been deleted is still listed — as missing. Hiding it
	// would let a report keep a grant nobody can see and nobody can revoke.
	if _, err := fx.svc.SetReportAccess(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, SetReportAccessRequest{
		Policy: model.ReportAccessSelectedGroups, TeamIDs: []string{team.ID},
	}); err != nil {
		t.Fatalf("set team access: %v", err)
	}
	if err := fx.gdb.Where("tenant_id = ? AND id = ?", fx.tenantID, team.ID).
		Delete(&model.Team{}).Error; err != nil {
		t.Fatal(err)
	}
	detail, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, created.Report.ID)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if detail.Access == nil || len(detail.Access.Grantees) != 1 {
		t.Fatalf("the detail read must carry the access view: %+v", detail.Access)
	}
	if !detail.Access.Grantees[0].Missing {
		t.Fatalf("a deleted team must be flagged missing: %+v", detail.Access.Grantees[0])
	}
}

// Sharing must never widen the write side: a shared reader holds `read`, not
// `execute`, so re-sharing is refused — and the refusal is 44109, not 403, because
// "you may not re-share this" and "this does not exist" must look the same.
func TestReportSharingDoesNotWidenWriteAccess(t *testing.T) {
	fx := newReportFixture(t, "access_writes")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-access-write-0001")
	colleague := reportAccessActor(t, fx, "report-write-colleague")
	if _, err := fx.svc.SetReportAccess(ctx, fx.owner.ID, fx.tenantID, created.Report.ID, SetReportAccessRequest{
		Policy: model.ReportAccessSelectedUsers, UserIDs: []string{colleague.ID},
	}); err != nil {
		t.Fatalf("share: %v", err)
	}
	if _, err := fx.svc.SetReportAccess(ctx, colleague.ID, fx.tenantID, created.Report.ID, SetReportAccessRequest{
		Policy: model.ReportAccessTenantMembers,
	}); !isReportNotFound(err) {
		t.Fatalf("a shared reader must not be able to re-share, got %v", err)
	}
	// And the read they do hold does not let them cancel somebody else's run.
	if _, err := fx.svc.CancelReport(ctx, colleague.ID, fx.tenantID, created.Report.ID); !isReportNotFound(err) {
		t.Fatalf("a shared reader must not cancel the owner's run, got %v", err)
	}
}

// The two closed sets are validated by the same code that writes them, and a bad value
// never lands: `observe`/`approval` are the only non-default spellings that mean
// anything, and everything else fails closed.
func TestTenantReportSwitchesRejectUnknownValues(t *testing.T) {
	fx := newReportFixture(t, "access_switches")
	ctx := context.Background()
	admin, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-switch-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, TenantReportPolicyRequest{
		BudgetPolicy: "sometimes",
	}); !isReportOutlineInvalid(err) {
		t.Fatalf("an unknown budget policy must be 44105, got %v", err)
	}
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, TenantReportPolicyRequest{
		AutoConfirmPolicy: "yes-please",
	}); !isReportOutlineInvalid(err) {
		t.Fatalf("an unknown auto-confirm policy must be 44105, got %v", err)
	}
	view, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, TenantReportPolicyRequest{
		BudgetPolicy: model.ReportBudgetObserve, AutoConfirmPolicy: model.ReportAutoConfirmApproval,
	})
	if err != nil {
		t.Fatalf("set switches: %v", err)
	}
	if view.BudgetPolicy != model.ReportBudgetObserve || view.AutoConfirmPolicy != model.ReportAutoConfirmApproval {
		t.Fatalf("unexpected switch view: %+v", view)
	}
	// The `System*` pair is the platform default, not the resolved value: it is what
	// the panel shows behind "继承自系统" (doc/296 §2.3), and it must stay `enforce` /
	// `self` even after a workspace opts into measuring and into approval.
	if view.SystemBudgetPolicy != model.ReportBudgetEnforce || view.SystemAutoConfirmPolicy != model.ReportAutoConfirmSelf {
		t.Fatalf("the system row must keep the fail-closed default: %+v", view)
	}
	// The audit names the two switches, because a governance change that leaves no
	// trace is indistinguishable from one that never happened.
	rows := fx.auditRows(t, "report.retention.tenant_overridden")
	if len(rows) == 0 {
		t.Fatal("setting the switches must be audited")
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(rows[0].DetailJSON), &detail); err != nil {
		t.Fatalf("audit detail: %v", err)
	}
	if detail["budget_policy"] != model.ReportBudgetObserve {
		t.Fatalf("audit must name the budget switch: %v", detail)
	}
}

// Observe measures without refusing. The ledger still records the call and the
// settlement, the overage is still audited, and the only thing that disappears is the
// two refusals (envelope and soft threshold).
func TestReportObservePolicyRecordsSpendWithoutRefusing(t *testing.T) {
	fx := newReportFixture(t, "access_observe")
	ctx := context.Background()
	admin, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-observe-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := fx.svc.SetTenantReportPolicy(ctx, admin.ID, fx.tenantID, TenantReportPolicyRequest{
		BudgetPolicy: model.ReportBudgetObserve,
	}); err != nil {
		t.Fatalf("set observe: %v", err)
	}
	created := fx.createReport(t, fx.datasetLink, "req-observe-0001")
	job := fx.reportJob(t, created.Job.ID)
	if job.ReportBudgetPolicy != model.ReportBudgetObserve {
		t.Fatalf("the switch must be frozen onto the job at creation, got %q", job.ReportBudgetPolicy)
	}
	// Shrink the envelope far below what the next call asks for: under `enforce` this
	// is a hard refusal, under `observe` it must be allowed.
	fx.setEnvelope(t, created.Job.ID, 10)

	now := time.Now().UTC()
	reservation, err := fx.svc.ReserveReportCharge(ctx, fx.tenantID, chargeCall(created.Job.ID, 5000), now)
	if err != nil {
		t.Fatalf("observe must not refuse an oversized call: %v", err)
	}
	if _, err := fx.svc.SettleReportCharge(ctx, fx.tenantID, created.Job.ID, reservation.LedgerKey, 5000, now); err != nil {
		t.Fatalf("settle: %v", err)
	}
	usage, err := fx.svc.ReportChargeUsageOf(ctx, fx.tenantID, fx.reportJob(t, created.Job.ID))
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if !usage.Observing {
		t.Fatal("the usage read must report the observing mode")
	}
	if usage.Charged != 5000 || usage.BudgetPolicy != model.ReportBudgetObserve {
		t.Fatalf("observe still books the spend under its own policy, got %+v", usage)
	}

	// An extension is meaningless here, and the service says so instead of booking an
	// approval for an action with no effect.
	if _, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, created.Report.ID,
		ReportBudgetExtensionRequest{Tokens: 100, Reason: "more"}); !isReportInvalidTransition(err) {
		t.Fatalf("observe must refuse a budget extension, got %v", err)
	}
}

// The direct path: no approval policy means the owner's request applies immediately,
// and the increment is arithmetic on the stored envelope.
func TestReportBudgetExtensionAppliesDirectlyAndIsBounded(t *testing.T) {
	fx := newReportFixture(t, "access_extend")
	ctx := context.Background()
	created := fx.createReport(t, fx.datasetLink, "req-extend-direct-0001")
	fx.setEnvelope(t, created.Job.ID, 1000)
	// Raising a limit does not move the report out of its run: the terminal state still
	// belongs to finalize, so `status` must read the same before and after.
	statusBefore := fx.reportRow(t, created.Report.ID).Status

	view, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, created.Report.ID,
		ReportBudgetExtensionRequest{Tokens: 400, Reason: "第三章需要重新取证"})
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if view.Status != ReportBudgetExtensionApplied || view.AddedTokens != 400 || view.BudgetAfter != 1400 {
		t.Fatalf("unexpected extension view: %+v", view)
	}
	if job := fx.reportJob(t, created.Job.ID); job.ReservedTokens != 1400 {
		t.Fatalf("the envelope must grow by the increment, got %d", job.ReservedTokens)
	}
	// Raising a limit never rewrites the ledger.
	if job := fx.reportJob(t, created.Job.ID); job.ChargedTokens != 0 {
		t.Fatalf("an extension must not touch the ledger, got %d", job.ChargedTokens)
	}
	rows := fx.auditRows(t, "report.budget.extended")
	if len(rows) != 1 {
		t.Fatalf("one extension must leave one audit row, got %d", len(rows))
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(rows[0].DetailJSON), &detail); err != nil {
		t.Fatalf("audit detail: %v", err)
	}
	if detail["added_budget"] != float64(400) || detail["budget_after"] != float64(1400) {
		t.Fatalf("the audit must name the before/after numbers: %v", detail)
	}
	// The audit writer redacts every key containing "token", so an amount named
	// `*_tokens` would be stored as "[REDACTED]" and the trail would carry no number.
	for key, value := range detail {
		if strings.Contains(key, "token") {
			t.Fatalf("the extension audit must not use a token-named key: %s=%v", key, value)
		}
		if value == "[REDACTED]" {
			t.Fatalf("the extension audit redacted %s", key)
		}
	}
	if after := fx.reportRow(t, created.Report.ID).Status; after != statusBefore {
		t.Fatalf("an extension must not change the report status: %s -> %s", statusBefore, after)
	}

	// One extension may add at most the current envelope, so a single approval cannot
	// walk the ceiling up without limit.
	if _, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, created.Report.ID,
		ReportBudgetExtensionRequest{Tokens: 5000}); !isReportOutlineInvalid(err) {
		t.Fatalf("an oversized increment must be 44105, got %v", err)
	}
	if _, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, created.Report.ID,
		ReportBudgetExtensionRequest{Tokens: 0}); !isReportOutlineInvalid(err) {
		t.Fatalf("a zero increment must be 44105, got %v", err)
	}
	// A finished run's envelope is history.
	if err := fx.gdb.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", fx.tenantID, created.Job.ID).
		Updates(map[string]any{"finalized_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, created.Report.ID,
		ReportBudgetExtensionRequest{Tokens: 100}); !isReportInvalidTransition(err) {
		t.Fatalf("a finalized job must be 44104, got %v", err)
	}
}

// The governed path: a workspace that configured an approval gets a pending answer and
// no change; executing the approval grows the envelope exactly once.
func TestReportBudgetExtensionWaitsForApprovalAndAppliesOnce(t *testing.T) {
	fx := newReportFixture(t, "access_extend_approval")
	ctx := context.Background()
	admin, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-approval-admin", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	created := fx.createReport(t, fx.datasetLink, "req-extend-approval-0001")
	fx.setEnvelope(t, created.Job.ID, 1000)

	fx.svc.SetApprovalConfig(config.Approval{
		Enabled: true, DefaultExpireHours: 72, ExecutionMaxRetries: 3,
		ExpireScanIntervalSec: 3600, RetentionDays: 365, PolicyCacheTTLSec: 0,
	})
	steps, err := json.Marshal([]model.ApprovalStepSpec{{
		StepNo: 1, Name: "报告预算审批", ApproverType: model.ApprovalApproverRole,
		ApproverValue: model.RoleTenantAdmin, RequiredApprovals: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.ApprovalPolicy{
		ID: "report-budget-extension-policy", TenantID: fx.tenantID,
		ObjectType: model.ApprovalObjectReport, Action: model.ApprovalActionExtendBudget,
		Enabled: true, Priority: 1, ConditionsJSON: "{}", StepsJSON: string(steps),
		ExpireHours: 72, Version: 1, CreatedBy: admin.ID,
	}
	if err := fx.svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	view, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, created.Report.ID,
		ReportBudgetExtensionRequest{Tokens: 300, Reason: "补齐财务章节"})
	if err != nil {
		t.Fatalf("request extension: %v", err)
	}
	if view.Status != ReportBudgetExtensionPending || view.ApprovalID == "" {
		t.Fatalf("a governed workspace must answer pending, got %+v", view)
	}
	if job := fx.reportJob(t, created.Job.ID); job.ReservedTokens != 1000 {
		t.Fatalf("a pending approval must not change the envelope, got %d", job.ReservedTokens)
	}
	// Re-submitting the identical request is the same request, not a second one.
	again, err := fx.svc.RequestReportBudgetExtension(ctx, fx.owner.ID, fx.tenantID, created.Report.ID,
		ReportBudgetExtensionRequest{Tokens: 300, Reason: "补齐财务章节"})
	if err != nil {
		t.Fatalf("repeat request: %v", err)
	}
	if again.ApprovalID != view.ApprovalID {
		t.Fatalf("the same request must reuse the same approval: %s vs %s", again.ApprovalID, view.ApprovalID)
	}

	approval, err := fx.svc.Store.GetApproval(ctx, fx.tenantID, view.ApprovalID)
	if err != nil || approval == nil {
		t.Fatalf("load approval: %v", err)
	}
	claimed, err := fx.svc.Store.TransitionApproval(ctx, fx.tenantID, approval.ID,
		model.ApprovalStatusPendingApproval, model.ApprovalStatusApproved, nil)
	if err != nil || !claimed {
		t.Fatalf("approve: claimed=%v err=%v", claimed, err)
	}
	executor, ok := fx.svc.approvalExecutorRegistry().Get(
		model.ApprovalObjectReport + "." + model.ApprovalActionExtendBudget)
	if !ok {
		t.Fatal("the report budget executor is not registered")
	}
	if err := executor.Validate(ctx, approval); err != nil {
		t.Fatalf("validate approved extension: %v", err)
	}
	result, err := executor.Execute(ctx, approval)
	if err != nil {
		t.Fatalf("execute approved extension: %v", err)
	}
	if result["added_tokens"] != int64(300) || result["budget_tokens"] != int64(1300) {
		t.Fatalf("unexpected executor result: %+v", result)
	}
	if job := fx.reportJob(t, created.Job.ID); job.ReservedTokens != 1300 {
		t.Fatalf("the approved increment must land, got %d", job.ReservedTokens)
	}
	// Executing twice would double the envelope, so a stale approval must be refused by
	// the bound rather than by luck.
	if err := executor.Validate(ctx, approval); err != nil {
		t.Fatalf("re-validating the same approval is still legal: %v", err)
	}

	// Approving an extension after the run finished is refused: the envelope of a
	// delivered report is history.
	if err := fx.gdb.Model(&model.ReportJob{}).
		Where("tenant_id = ? AND id = ?", fx.tenantID, created.Job.ID).
		Updates(map[string]any{"finalized_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := executor.Validate(ctx, approval); !isReportInvalidTransition(err) {
		t.Fatalf("a finished run must refuse a late extension, got %v", err)
	}
}

func isReportInvalidTransition(err error) bool {
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Code == codeReportInvalidTransition && httpErr.Status == 409
}

func isReportOutlineInvalid(err error) bool {
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Code == codeReportOutlineInvalid && httpErr.Status == 400
}
