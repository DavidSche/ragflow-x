package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// cloneActor returns a tenant-admin user, the role with `manage, report-template`
// (migrate.go builtin matrix, mirroring the sibling scenario-template resource).
func (fx *reportFixture) cloneActor(t *testing.T) *model.User {
	t.Helper()
	admin, err := fx.svc.CreateUser(context.Background(), fx.tenantID, "", CreateUserRequest{
		Username: "template-admin-" + idShort(t), Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create tenant admin: %v", err)
	}
	return admin
}

func idShort(t *testing.T) string {
	t.Helper()
	return "clone-" + t.Name()[:4]
}

// TestCloneReportTemplateForksPlatformBaseline pins the D4 fork contract: the
// clone is a *tenant* row with version 1 of its own sequence, provenance points
// at the platform baseline it forked, and the payload is a faithful copy.
func TestCloneReportTemplateForksPlatformBaseline(t *testing.T) {
	fx := newReportFixture(t, "clone-fork")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	platform, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, reportComplianceTemplateKey, 0)
	if err != nil || platform == nil {
		t.Fatalf("load platform baseline: %v", err)
	}
	wantPayload, err := reportTemplatePayloadOf(platform)
	if err != nil {
		t.Fatal(err)
	}

	result, err := fx.svc.CloneReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if !result.Created {
		t.Fatal("a fresh clone must report created=true")
	}
	view := result.Template
	if view.TenantID != fx.tenantID || view.Version != 1 {
		t.Fatalf("clone must be the tenant's own version-1 fork, got %+v", view)
	}
	if result.SourceTemplateID != platform.ID || result.SourceTemplateVersion != platform.Version {
		t.Fatalf("provenance must point at the forked baseline, got %+v", result)
	}
	if !result.SourceVersionPinned {
		t.Fatal("a fork pins the upstream version it cloned")
	}
	gotPayload, err := reportTemplatePayloadOf(platformRowOf(fx, t, fx.tenantID, reportComplianceTemplateKey))
	if err != nil {
		t.Fatal(err)
	}
	if hashReportTemplatePayload(gotPayload) != hashReportTemplatePayload(wantPayload) {
		t.Fatalf("clone payload must be a faithful copy of the baseline")
	}
}

func platformRowOf(fx *reportFixture, t *testing.T, tenantID, key string) *model.ReportTemplate {
	t.Helper()
	row, err := fx.svc.Store.GetReportTemplate(context.Background(), tenantID, key, 0)
	if err != nil || row == nil {
		t.Fatalf("load %s/%s latest: %v", tenantID, key, err)
	}
	return row
}

// TestCloneReportTemplateSecondCloneAdvancesOwnSequence: the second fork of the
// (unchanged) baseline is the tenant's version 2; the first fork is left intact.
// This is the P0-B "re-fork to catch up" path before upgrades exist.
func TestCloneReportTemplateSecondCloneAdvancesOwnSequence(t *testing.T) {
	fx := newReportFixture(t, "clone-seq")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	first, err := fx.svc.CloneReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey)
	if err != nil {
		t.Fatalf("first clone: %v", err)
	}
	second, err := fx.svc.CloneReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey)
	if err != nil {
		t.Fatalf("second clone: %v", err)
	}
	if !second.Created {
		t.Fatal("the second fork is a new version, not a replay")
	}
	if first.Template.Version != 1 || second.Template.Version != 2 {
		t.Fatalf("versions must advance 1 -> 2 in the tenant's own sequence, got %d -> %d",
			first.Template.Version, second.Template.Version)
	}
	if first.Template.TemplateID == second.Template.TemplateID {
		t.Fatal("each fork must be its own row")
	}
	if second.Template.TemplateID == first.Template.TemplateID {
		t.Fatal("the second fork must not overwrite the first")
	}
	// The catalog now resolves to the workspace's newest fork (v2), not the
	// baseline — a clone must win over the platform row for this tenant.
	views, err := fx.svc.ListReportTemplatesView(ctx, admin.ID, fx.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, view := range views {
		if view.Key != reportComplianceTemplateKey {
			continue
		}
		found = true
		if view.TenantID != fx.tenantID || view.Version != 2 {
			t.Fatalf("catalog must resolve the workspace fork, got %+v", view)
		}
	}
	if !found {
		t.Fatalf("template %s missing from the catalog", reportComplianceTemplateKey)
	}
}

// TestClonePlatformUpgradeDoesNotOverwriteFork is A16: the platform ships v2
// (a NEW platform row, old one retired), and the workspace clone stays byte-for-
// byte intact. Upgrades are insert-only, so the fork "一字不改".
func TestClonePlatformUpgradeDoesNotOverwriteFork(t *testing.T) {
	fx := newReportFixture(t, "clone-upgrade")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	result, err := fx.svc.CloneReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	frozenVersion := result.Template.Version
	frozenID := result.Template.TemplateID
	frozenPayload, err := reportTemplatePayloadOf(platformRowOf(fx, t, fx.tenantID, reportComplianceTemplateKey))
	if err != nil {
		t.Fatal(err)
	}

	// Fleet upgrade: retire the v1 baseline and ship v2 with a changed limit.
	old := platformRowOf(fx, t, model.PlatformTenantID, reportComplianceTemplateKey)
	retire := time.Now().UTC()
	sNow := retire.Add(-time.Minute)
	old.Status = model.ReportTemplateStatusRetired
	old.RetiredAt = &retire
	if err := fx.gdb.Save(old).Error; err != nil {
		t.Fatal(err)
	}
	next, err := reportTemplatePayloadOf(old)
	if err != nil {
		t.Fatal(err)
	}
	next.Limits.MaxTokensPerSection = 999
	upgraded, err := reportTemplateRow(model.PlatformTenantID, reportComplianceTemplateKey, old.Version+1, next, sNow)
	if err != nil {
		t.Fatal(err)
	}
	upgraded.CreatedBy = builtinReportBootstrapActor
	if err := fx.svc.Store.CreateReportTemplate(ctx, upgraded); err != nil {
		t.Fatalf("ship upgraded baseline: %v", err)
	}

	// The platform now resolves v2; the workspace still resolves its untouched v1.
	if got := platformRowOf(fx, t, model.PlatformTenantID, reportComplianceTemplateKey).Version; got != 2 {
		t.Fatalf("platform must resolve the new v2, got %d", got)
	}
	fork := platformRowOf(fx, t, fx.tenantID, reportComplianceTemplateKey)
	if fork.Version != frozenVersion || fork.ID != frozenID {
		t.Fatalf("upgrade must not touch the fork: %+v", fork)
	}
	after, err := reportTemplatePayloadOf(fork)
	if err != nil {
		t.Fatal(err)
	}
	if after.Limits.MaxTokensPerSection != frozenPayload.Limits.MaxTokensPerSection {
		t.Fatalf("upgrade must not leak into the fork's payload")
	}
	// Provenance still names the baseline it was forked from (v1), not v2.
	if fork.SourceTemplateVersion != 1 {
		t.Fatalf("source version must stay pinned at fork time, got %d", fork.SourceTemplateVersion)
	}
}

func TestCloneReportTemplateUnknownKey(t *testing.T) {
	fx := newReportFixture(t, "clone-unknown")
	ctx := context.Background()
	admin := fx.cloneActor(t)
	_, err := fx.svc.CloneReportTemplate(ctx, admin.ID, fx.tenantID, "no-such-template")
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportTemplateNotFound || httpErr.Status != 404 {
		t.Fatalf("unknown template must be 404/44101, got %v", err)
	}
}

func TestCloneReportTemplateRequiresManage(t *testing.T) {
	fx := newReportFixture(t, "clone-rbac")
	ctx := context.Background()
	// The fixture owner is an operator: read-only on report-template, so cloning
	// must be refused even though the key resolves.
	_, err := fx.svc.CloneReportTemplate(ctx, fx.owner.ID, fx.tenantID, reportComplianceTemplateKey)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator must not clone, got %v", err)
	}
}
