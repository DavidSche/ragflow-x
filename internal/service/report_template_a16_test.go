package service

// A16 (doc/288 §7.8 / §14.3) — "租户克隆模板不影响平台模板；老报告按老模板版本
// 可重现（结构层面）".
//
// The clone tests next door prove the *fork* contract (own version sequence, a
// platform upgrade does not overwrite the fork). A16 needs the two directions that
// contract only implies:
//
//   - outward: cloning is a *read* of the platform baseline. A tenant admin who forks
//     `report-compliance` must not be able to mutate the baseline row, and must not
//     see or affect another workspace's fork.
//   - backward: a published report pins `(template_key, template_version)` on its
//     revision. When the platform retires that version, the report must still resolve
//     *that* version — the reproducibility promise is about the 章节 structure the
//     report was written against, not about the template being current.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// secondWorkspace builds a second tenant with its own tenant admin — what "another
// workspace" means for the clone surface.
func (fx *reportFixture) secondWorkspace(t *testing.T, name string) (tenantID string, admin *model.User) {
	t.Helper()
	ctx := context.Background()
	tenant, err := fx.svc.CreateTenant(ctx, "第二工作区 "+name)
	if err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	user, err := fx.svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "template-admin-b-" + name, Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create second admin: %v", err)
	}
	return tenant.ID, user
}

// templateFingerprint is the *structure* of one template version — key, version,
// confirmation mode and the ordered section list with prompts and evidence flags.
// Lifecycle columns (status/retired_at) are deliberately excluded: retiring a version
// is a legitimate change, while the section structure is what a published report
// reproduces. Callers that care about the lifecycle assert it separately.
func templateFingerprint(t *testing.T, row *model.ReportTemplate) string {
	t.Helper()
	payload, err := reportTemplatePayloadOf(row)
	if err != nil {
		t.Fatalf("payload of %s v%d: %v", row.TemplateKey, row.Version, err)
	}
	// Sections in order, with their prompts and evidence flags: the structural
	// identity of one template version.
	var blob strings.Builder
	blob.WriteString(row.TemplateKey + "|" + strconv.FormatInt(row.Version, 10) + "|" +
		row.OutlineConfirmation)
	for _, section := range payload.Sections {
		blob.WriteString("|" + section.Key + ":" + section.Title + ":" + section.Prompt +
			":" + strconv.FormatBool(section.RequiresEvidence) + ":" +
			strconv.Itoa(section.MaxWords))
	}
	return blob.String()
}

// TestCloneLeavesThePlatformBaselineAndOtherWorkspacesAlone is A16's outward half.
func TestCloneLeavesThePlatformBaselineAndOtherWorkspacesAlone(t *testing.T) {
	fx := newReportFixture(t, "a16-isolation")
	ctx := context.Background()
	adminA := fx.cloneActor(t)
	tenantB, adminB := fx.secondWorkspace(t, "a16")

	platform, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, reportComplianceTemplateKey, 0)
	if err != nil || platform == nil {
		t.Fatalf("load platform baseline: %v", err)
	}
	before := templateFingerprint(t, platform)

	forkA, err := fx.svc.CloneReportTemplate(ctx, adminA.ID, fx.tenantID, reportComplianceTemplateKey)
	if err != nil {
		t.Fatalf("tenant A clone: %v", err)
	}
	fingerprintA := templateFingerprint(t, mustTemplateVersion(t, fx, fx.tenantID, forkA.Template.Version))

	// Tenant B forks the same key. Its source must be the *platform* row, never A's
	// fork: a clone means "fork upstream", and resolving another workspace's row
	// would leak one tenant's customisation into another's version sequence.
	forkB, err := fx.svc.CloneReportTemplate(ctx, adminB.ID, tenantB, reportComplianceTemplateKey)
	if err != nil {
		t.Fatalf("tenant B clone: %v", err)
	}
	if forkB.SourceTemplateID != platform.ID {
		t.Fatalf("tenant B must fork the platform baseline (%s), got source=%s",
			platform.ID, forkB.SourceTemplateID)
	}
	if forkB.Template.TenantID != tenantB || forkB.Template.Version != 1 {
		t.Fatalf("tenant B fork must be its own v1, got %+v", forkB.Template)
	}

	// Nothing moved: the baseline is unchanged (structure *and* lifecycle) and B's
	// clone left A's fork alone.
	baseline := mustTemplateVersion(t, fx, model.PlatformTenantID, platform.Version)
	if baseline.Status != platform.Status || (baseline.RetiredAt == nil) != (platform.RetiredAt == nil) {
		t.Fatalf("cloning changed the baseline lifecycle: %+v vs %+v", baseline, platform)
	}
	after := templateFingerprint(t, baseline)
	if after != before {
		t.Fatalf("cloning mutated the platform baseline\nbefore: %s\nafter:  %s", before, after)
	}
	if now := templateFingerprint(t, mustTemplateVersion(t, fx, fx.tenantID, forkA.Template.Version)); now != fingerprintA {
		t.Fatalf("another workspace's clone changed tenant A's fork\nbefore: %s\nafter:  %s", fingerprintA, now)
	}

	// And the list face keeps each fork private: A sees platform rows + its own fork,
	// never B's.
	views, err := fx.svc.ListReportTemplates(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	sawForkA := false
	for _, view := range views {
		if view.TenantID != model.PlatformTenantID && view.TenantID != fx.tenantID {
			t.Fatalf("tenant A's catalog leaked a row owned by %s", view.TenantID)
		}
		if view.TenantID == fx.tenantID {
			sawForkA = true
		}
	}
	if !sawForkA {
		t.Fatal("tenant A's own fork must appear in its catalog")
	}
}

// TestCloneRefusesToWriteIntoAnotherWorkspace pins the scope argument: the tenant is
// the *actor's* workspace. A tenant admin naming someone else's workspace is refused
// before any row is written, so the clone route cannot become cross-tenant template
// injection.
func TestCloneRefusesToWriteIntoAnotherWorkspace(t *testing.T) {
	fx := newReportFixture(t, "a16-scope")
	ctx := context.Background()
	adminA := fx.cloneActor(t)
	tenantB, _ := fx.secondWorkspace(t, "a16-scope")

	if _, err := fx.svc.CloneReportTemplate(ctx, adminA.ID, tenantB, reportComplianceTemplateKey); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cloning into another workspace must be forbidden, got %v", err)
	}
	if row := maybeTemplate(t, fx, tenantB, 1); row != nil {
		t.Fatalf("a refused clone must write nothing, found %+v", row)
	}
}

// TestRetiredTemplateVersionStaysReproducibleForPublishedReports is A16's backward
// half at the layer the promise lives in: `resolveReportTemplate` with a pinned
// version is a historical read that ignores retirement, while the *new-report*
// lookup (version 0) must stop offering the retired baseline.
func TestRetiredTemplateVersionStaysReproducibleForPublishedReports(t *testing.T) {
	fx := newReportFixture(t, "a16-pinned")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())

	pinned := revision.TemplateVersion
	if pinned <= 0 || revision.TemplateKey != reportComplianceTemplateKey {
		t.Fatalf("a published revision must pin its template, got %s v%d",
			revision.TemplateKey, pinned)
	}
	original, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, revision.TemplateKey, pinned)
	if err != nil || original == nil {
		t.Fatalf("load the pinned version: %v", err)
	}
	structure := templateFingerprint(t, original)

	// The platform ships v2 and retires v1 — the normal upgrade path (§7.8 rule ②: a
	// new platform row plus a retired old row, nothing rewritten).
	next := *original
	next.ID = ""
	next.Version = pinned + 1
	next.RetiredAt = nil
	next.Status = model.ReportTemplateStatusActive
	if err := fx.svc.Store.CreateReportTemplate(ctx, &next); err != nil {
		t.Fatalf("create v%d: %v", pinned+1, err)
	}
	retiredAt := time.Now().UTC()
	if err := fx.gdb.Exec(
		`UPDATE rgx_report_template SET retired_at = ?, status = ? WHERE id = ?`,
		retiredAt, model.ReportTemplateStatusRetired, original.ID,
	).Error; err != nil {
		t.Fatalf("retire v%d: %v", pinned, err)
	}

	// New reports move to v2 …
	resolved, err := fx.svc.resolveReportTemplate(ctx, fx.tenantID, revision.TemplateKey, 0)
	if err != nil {
		t.Fatalf("resolve current template: %v", err)
	}
	if resolved.Version != pinned+1 {
		t.Fatalf("new reports must pick the live version, got v%d", resolved.Version)
	}
	// … while the published report still resolves the version it was written against,
	// with the same section structure, even though that row is retired.
	replay, err := fx.svc.resolveReportTemplate(ctx, fx.tenantID, revision.TemplateKey, pinned)
	if err != nil {
		t.Fatalf("a retired pinned version must stay resolvable: %v", err)
	}
	if replay.Version != pinned {
		t.Fatalf("pinned resolution returned v%d, want v%d", replay.Version, pinned)
	}
	if got := templateFingerprint(t, replay); got != structure {
		t.Fatalf("pinned structure drifted\nbefore: %s\nafter:  %s", structure, got)
	}
}

// mustTemplateVersion loads one exact template version or fails.
func mustTemplateVersion(t *testing.T, fx *reportFixture, tenantID string, version int64) *model.ReportTemplate {
	t.Helper()
	row := maybeTemplate(t, fx, tenantID, version)
	if row == nil {
		t.Fatalf("template %s v%d must exist", tenantID, version)
	}
	return row
}

// maybeTemplate is mustTemplateVersion's "may be absent" sibling.
func maybeTemplate(t *testing.T, fx *reportFixture, tenantID string, version int64) *model.ReportTemplate {
	t.Helper()
	row, err := fx.svc.Store.GetReportTemplate(context.Background(), tenantID, reportComplianceTemplateKey, version)
	if err != nil {
		t.Fatalf("load template %s v%d: %v", tenantID, version, err)
	}
	return row
}
