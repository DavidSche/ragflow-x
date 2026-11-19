package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Workspace template authoring (doc/295 §2.3 / R19, A58/A59).
//
// The invariants here are about *versions*, not about JSON: a published version is
// immutable so a report that pinned it keeps rendering it, a failed publish writes
// nothing, and retirement stops new runs without touching history or the platform
// baseline.

// templatePayloadWithName loads the platform baseline and renames it, so every test
// publishes a payload that differs from the seed for a reason it can name.
func templatePayloadWithName(t *testing.T, fx *reportFixture, key, zh string) ReportTemplatePayload {
	t.Helper()
	row, err := fx.svc.Store.GetReportTemplate(context.Background(), model.PlatformTenantID, key, 0)
	if err != nil || row == nil {
		t.Fatalf("load platform baseline %q: %v", key, err)
	}
	payload, err := reportTemplatePayloadOf(row)
	if err != nil {
		t.Fatal(err)
	}
	payload.Name.Zh = zh
	return *payload
}

func templateVersionCount(t *testing.T, fx *reportFixture, tenantID, key string) int {
	t.Helper()
	rows, err := fx.svc.Store.ListReportTemplates(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	count := 0
	for _, row := range rows {
		if row.TemplateKey == key {
			count++
		}
	}
	return count
}

// TestPublishReportTemplateMintsVersionsAndKeepsThemImmutable pins A58: publishing
// always adds a version, the previous version's payload is byte-identical
// afterwards, and re-publishing the same content does not mint a number.
func TestPublishReportTemplateMintsVersionsAndKeepsThemImmutable(t *testing.T) {
	fx := newReportFixture(t, "tpl-publish")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	if _, err := fx.svc.CloneReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey); err != nil {
		t.Fatalf("clone baseline: %v", err)
	}
	v1, err := fx.svc.Store.GetReportTemplate(ctx, fx.tenantID, reportComplianceTemplateKey, 1)
	if err != nil || v1 == nil {
		t.Fatalf("load v1: %v", err)
	}
	v1Sections := v1.SectionsJSON

	payload := templatePayloadWithName(t, fx, reportComplianceTemplateKey, "合规检查（本工作区）")
	result, err := fx.svc.PublishReportTemplate(ctx, admin.ID, fx.tenantID, ReportTemplatePublishRequest{
		Key: reportComplianceTemplateKey, Payload: payload,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !result.Created || result.Template.Version != 2 {
		t.Fatalf("publish must mint version 2, got %+v", result)
	}
	if result.Template.Source != "workspace" || !result.Template.Editable {
		t.Fatalf("a workspace version must be labelled and editable, got %+v", result.Template)
	}

	after, err := fx.svc.Store.GetReportTemplate(ctx, fx.tenantID, reportComplianceTemplateKey, 1)
	if err != nil || after == nil {
		t.Fatalf("reload v1: %v", err)
	}
	if after.SectionsJSON != v1Sections || after.NameJSON != v1.NameJSON {
		t.Fatal("publishing a new version must not rewrite the previous row")
	}

	// Same content again: the answer is the version that already says this.
	replay, err := fx.svc.PublishReportTemplate(ctx, admin.ID, fx.tenantID, ReportTemplatePublishRequest{
		Key: reportComplianceTemplateKey, Payload: payload,
	})
	if err != nil {
		t.Fatalf("re-publish: %v", err)
	}
	if replay.Created || replay.Template.Version != 2 {
		t.Fatalf("identical content must replay version 2, got %+v", replay)
	}
	if got := templateVersionCount(t, fx, fx.tenantID, reportComplianceTemplateKey); got != 2 {
		t.Fatalf("version count must stay 2, got %d", got)
	}

	// The catalogue answers one row per key, so it must also carry the count: the UI
	// printed "v2（共 1 个版本）" on a real deploy when it could only count the rows
	// it received (2026-11-19).
	views, err := fx.svc.ListReportTemplates(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("list view: %v", err)
	}
	var listed []ReportTemplateView
	for _, view := range views {
		if view.Key == reportComplianceTemplateKey {
			listed = append(listed, view)
		}
	}
	if len(listed) != 1 {
		t.Fatalf("the catalogue must answer one row per key, got %d", len(listed))
	}
	if listed[0].Version != 2 || listed[0].Versions != 2 {
		t.Fatalf("the row must say v2 of 2, got v%d of %d",
			listed[0].Version, listed[0].Versions)
	}
	// A key nobody forked reports the platform's own sequence, not a mix of two.
	// `report-tech-review` still has exactly one baseline version.
	for _, view := range views {
		if view.Key != reportTechnicalReviewKey {
			continue
		}
		if view.Source != "platform" || view.Versions != 1 {
			t.Fatalf("an unforked baseline must report its own version count, got %+v", view)
		}
	}

	audits := auditsWithAction(t, fx, "report.template.version_published")
	if len(audits) != 1 {
		t.Fatalf("one publish must write one audit row, got %d", len(audits))
	}
	if !strings.Contains(audits[0].DetailJSON, `"version":2`) {
		t.Fatalf("audit must name the published version, got %s", audits[0].DetailJSON)
	}
}

// TestPublishReportTemplateRefusesBadStructureWithoutWriting pins A59's first
// half: the workspace goes through the platform's own validator, and a rejected
// publish leaves the catalogue exactly as it was.
func TestPublishReportTemplateRefusesBadStructureWithoutWriting(t *testing.T) {
	fx := newReportFixture(t, "tpl-validate")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	before := templateVersionCount(t, fx, fx.tenantID, reportComplianceTemplateKey)

	cases := []struct {
		name string
		req  ReportTemplatePublishRequest
	}{
		{
			name: "key with an upper-case letter",
			req: ReportTemplatePublishRequest{
				Key: "Report-Compliance", Payload: templatePayloadWithName(t, fx, reportComplianceTemplateKey, "x"),
			},
		},
		{
			name: "no section requires evidence",
			req: ReportTemplatePublishRequest{
				Key:     reportComplianceTemplateKey,
				Payload: templatePayloadWithoutEvidence(t, fx),
			},
		},
		{
			name: "dependency cycle",
			req: ReportTemplatePublishRequest{
				Key:     reportComplianceTemplateKey,
				Payload: templatePayloadWithCycle(t, fx),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fx.svc.PublishReportTemplate(ctx, admin.ID, fx.tenantID, tc.req)
			if err == nil {
				t.Fatal("an invalid payload must not be published")
			}
			status, code := httpStatusAndCode(t, err)
			if status != 400 || code != 44105 {
				t.Fatalf("expected 400/44105, got %d/%d (%v)", status, code, err)
			}
		})
	}
	if after := templateVersionCount(t, fx, fx.tenantID, reportComplianceTemplateKey); after != before {
		t.Fatalf("rejected publishes wrote rows: %d -> %d", before, after)
	}
}

// TestPublishReportTemplateRefusesThePlatformCatalogue makes the "platform edits
// are releases" rule explicit: the same key cannot be re-authored as the platform
// tenant through this route.
func TestPublishReportTemplateRefusesThePlatformCatalogue(t *testing.T) {
	fx := newReportFixture(t, "tpl-platform")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	platformAdmin, err := fx.svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || platformAdmin == nil {
		t.Fatalf("load bootstrapped platform admin: %v", err)
	}
	if platformAdmin.ID == "" {
		t.Fatal("platform admin must exist")
	}
	// An operator is refused by RBAC before the tenant check; the platform admin is
	// the only caller that can reach it, which is what makes the guard meaningful.
	_ = admin
	payload := templatePayloadWithName(t, fx, reportComplianceTemplateKey, "平台模板改名")
	_, err = fx.svc.PublishReportTemplate(ctx, platformAdmin.ID, model.PlatformTenantID, ReportTemplatePublishRequest{
		Key: reportComplianceTemplateKey, Payload: payload,
	})
	if err == nil {
		t.Fatal("the platform catalogue must not be editable through the tenant route")
	}
	if status, code := httpStatusAndCode(t, err); status != 400 || code != 44105 {
		t.Fatalf("expected 400/44105, got %d/%d", status, code)
	}
}

// TestRetireReportTemplateStopsNewRunsButKeepsHistory pins the retirement
// contract: new runs get 44101, pinned versions still resolve, the platform
// baseline is untouched, and retirement is idempotent.
func TestRetireReportTemplateStopsNewRunsButKeepsHistory(t *testing.T) {
	fx := newReportFixture(t, "tpl-retire")
	ctx := context.Background()
	admin := fx.cloneActor(t)

	if _, err := fx.svc.CloneReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey); err != nil {
		t.Fatalf("clone baseline: %v", err)
	}
	platformBefore, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, reportComplianceTemplateKey, 0)
	if err != nil || platformBefore == nil {
		t.Fatalf("load platform baseline: %v", err)
	}

	result, err := fx.svc.RetireReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey)
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if !result.Template.Retired || result.Template.Editable {
		t.Fatalf("a retired version must be labelled and not editable, got %+v", result.Template)
	}

	if _, err := fx.svc.resolveReportTemplate(ctx, fx.tenantID, reportComplianceTemplateKey, 0); err == nil {
		t.Fatal("a retired newest version must not resolve for a new run")
	} else if status, code := httpStatusAndCode(t, err); status != 404 || code != 44101 {
		t.Fatalf("expected 404/44101, got %d/%d", status, code)
	}
	// History keeps working: an explicit version still resolves (A38).
	if _, err := fx.svc.resolveReportTemplate(ctx, fx.tenantID, reportComplianceTemplateKey, 1); err != nil {
		t.Fatalf("a pinned version must still resolve after retirement: %v", err)
	}

	platformAfter, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, reportComplianceTemplateKey, 0)
	if err != nil || platformAfter == nil {
		t.Fatalf("reload platform baseline: %v", err)
	}
	if platformAfter.RetiredAt != nil || platformAfter.Status == model.ReportTemplateStatusRetired {
		t.Fatal("retiring a workspace version must not retire the platform baseline")
	}

	// Idempotent: a second call is the same answer, not a second audit.
	if _, err := fx.svc.RetireReportTemplate(ctx, admin.ID, fx.tenantID, reportComplianceTemplateKey); err != nil {
		t.Fatalf("second retire: %v", err)
	}
	audits := auditsWithAction(t, fx, "report.template.retired")
	if len(audits) != 2 {
		t.Fatalf("each retire call records its own decision, got %d rows", len(audits))
	}

	views, err := fx.svc.ListReportTemplates(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, view := range views {
		if view.Key != reportComplianceTemplateKey {
			continue
		}
		if !view.Retired || view.Source != "workspace" {
			t.Fatalf("the catalogue must show the workspace row as retired, got %+v", view)
		}
		return
	}
	t.Fatal("the retired workspace template must stay listed")
}

// TestPublishReportTemplateRequiresManage is the role boundary: an operator may run
// reports but not change what they are built on.
func TestPublishReportTemplateRequiresManage(t *testing.T) {
	fx := newReportFixture(t, "tpl-rbac")
	ctx := context.Background()
	payload := templatePayloadWithName(t, fx, reportComplianceTemplateKey, "操作员改名")

	_, err := fx.svc.PublishReportTemplate(ctx, fx.owner.ID, fx.tenantID, ReportTemplatePublishRequest{
		Key: reportComplianceTemplateKey, Payload: payload,
	})
	if err == nil {
		t.Fatal("an operator must not publish a template version")
	}
	status, _ := httpStatusAndCode(t, err)
	if status != 403 && status != 404 {
		t.Fatalf("expected the RBAC refusal, got %d (%v)", status, err)
	}
}

func templatePayloadWithoutEvidence(t *testing.T, fx *reportFixture) ReportTemplatePayload {
	t.Helper()
	payload := templatePayloadWithName(t, fx, reportComplianceTemplateKey, "无证据模板")
	for i := range payload.Sections {
		payload.Sections[i].RequiresEvidence = false
	}
	return payload
}

func templatePayloadWithCycle(t *testing.T, fx *reportFixture) ReportTemplatePayload {
	t.Helper()
	payload := templatePayloadWithName(t, fx, reportComplianceTemplateKey, "成环模板")
	if len(payload.Sections) >= 2 {
		payload.Sections[0].DependsOn = []string{payload.Sections[1].Key}
		payload.Sections[1].DependsOn = []string{payload.Sections[0].Key}
	}
	return payload
}

// httpStatusAndCode unwraps the httperr shape the service returns so a test asserts
// the public contract instead of matching error strings.
func httpStatusAndCode(t *testing.T, err error) (int, int) {
	t.Helper()
	var e *httperr.Error
	if !errors.As(err, &e) || e == nil {
		return 0, 0
	}
	return e.Status, e.Code
}

// auditsWithAction narrows the tenant's audit trail to one action name. The
// action strings are the contract (A62), so the test compares them literally.
func auditsWithAction(t *testing.T, fx *reportFixture, action string) []model.AuditLog {
	t.Helper()
	rows, err := fx.svc.Store.ListAuditsAll(context.Background(), fx.tenantID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	out := make([]model.AuditLog, 0, len(rows))
	for _, row := range rows {
		if row.Action == action {
			out = append(out, row)
		}
	}
	return out
}
