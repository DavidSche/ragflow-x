package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report content-template contract (doc/288 §11.1 / §9.4 / §9.5). The rules below
// are the ones whose violation would not crash anything — it would silently
// produce an empty, uncited or unbounded report, which is the failure mode this
// feature exists to avoid.

func validReportPayload() ReportTemplatePayload {
	return ReportTemplatePayload{
		Name:          ReportTemplateName{Zh: "制度符合性", En: "Compliance"},
		OutputFormats: []string{"docx", "md"},
		Sections: []ReportTemplateSection{
			{Key: "background", Title: "背景", Prompt: "说明范围", MaxWords: 200},
			{Key: "findings", Title: "判定", Prompt: "逐条判定", RequiresEvidence: true,
				MaxWords: 600, DependsOn: []string{"background"}},
		},
		Limits: ReportTemplateLimits{
			MaxSections: 5, SliceMaxSeconds: 480, TotalMaxSeconds: 3600,
			MaxSlices: 8, MaxTokensPerSection: 1600,
		},
		Retrieval: ReportTemplateRetrieval{TopK: 8, ThresholdPolicy: "dataset_calibrated", MinEvidence: 1},
		EvidencePolicy: ReportTemplateEvidencePolicy{
			ConflictHandling: reportConflictHandlingP0, OutdatedEvidence: reportOutdatedEvidenceP0,
			UnsupportedClaims: reportUnsupportedClaimsP0,
		},
		OutlineConfirmation: model.ReportOutlineRequired,
	}
}

func TestValidateReportTemplatePayloadAcceptsContractShape(t *testing.T) {
	payload := validReportPayload()
	if err := validateReportTemplatePayload(&payload); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
}

func TestValidateReportTemplatePayloadRejectsContractViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ReportTemplatePayload)
		want   string
	}{
		{"no name", func(p *ReportTemplatePayload) { p.Name.En = "" }, "name.zh and name.en"},
		{"pdf not shipped", func(p *ReportTemplatePayload) { p.OutputFormats = []string{"pdf"} }, "unsupported output format"},
		{"absolute threshold", func(p *ReportTemplatePayload) {
			// A hard-coded similarity drifts with the embedding model and silently
			// empties the report, so the contract only accepts the policy name.
			p.Retrieval.ThresholdPolicy = "0.25"
		}, "threshold_policy"},
		{"no sections", func(p *ReportTemplatePayload) { p.Sections = nil }, "sections must not be empty"},
		{"duplicate key", func(p *ReportTemplatePayload) { p.Sections[1].Key = "background" }, "duplicate section key"},
		{"missing prompt", func(p *ReportTemplatePayload) { p.Sections[0].Prompt = " " }, "prompt is required"},
		{"unknown dependency", func(p *ReportTemplatePayload) {
			p.Sections[1].DependsOn = []string{"nope"}
		}, "unknown section"},
		{"cycle", func(p *ReportTemplatePayload) {
			p.Sections[0].DependsOn = []string{"findings"}
		}, "cycle"},
		{"no evidence section", func(p *ReportTemplatePayload) {
			p.Sections[1].RequiresEvidence = false
		}, "at least one section must require evidence"},
		{"slice overruns runtime job", func(p *ReportTemplatePayload) {
			p.Limits.SliceMaxSeconds = 600
		}, "slice_max_seconds"},
		{"too many sections", func(p *ReportTemplatePayload) { p.Limits.MaxSections = 1 }, "max_sections"},
		{"slice budget cannot cover total", func(p *ReportTemplatePayload) {
			p.Limits.MaxSlices = 2
		}, "cannot cover total_max_seconds"},
		{"total below one slice", func(p *ReportTemplatePayload) { p.Limits.TotalMaxSeconds = 60 }, "must be >= slice_max_seconds"},
		{"auto without roles", func(p *ReportTemplatePayload) {
			// auto with an empty role list hands the permission to everyone.
			p.OutlineConfirmation = model.ReportOutlineAuto
		}, "auto_confirm_allowed_roles"},
		{"unknown conflict handling", func(p *ReportTemplatePayload) {
			p.EvidencePolicy.ConflictHandling = "ignore"
		}, "conflict_handling"},
		{"unknown outdated policy", func(p *ReportTemplatePayload) {
			p.EvidencePolicy.OutdatedEvidence = "delete"
		}, "outdated_evidence"},
		{"missing claim policy", func(p *ReportTemplatePayload) {
			p.EvidencePolicy.UnsupportedClaims = ""
		}, "unsupported_claims is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := validReportPayload()
			tc.mutate(&payload)
			err := validateReportTemplatePayload(&payload)
			if err == nil {
				t.Fatalf("payload must be rejected (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestSliceMaxSecondsCeilingTracksRuntimeJobTimeout(t *testing.T) {
	if got := sliceMaxSecondsCeiling(); got != 480 {
		// 0.8 × 600s. If the runtime timeout changes, this constant must change with
		// it: a slice that outlives its job never checkpoints and is redone forever.
		t.Fatalf("slice ceiling must be 480s, got %d", got)
	}
}

func TestShippedReportTemplateSatisfiesItsOwnContract(t *testing.T) {
	templates := BuiltinReportTemplates()
	if len(templates) != 1 {
		t.Fatalf("P0-A ships exactly one template, got %d", len(templates))
	}
	shipped := templates[0]
	if shipped.Key != reportComplianceTemplateKey {
		t.Fatalf("unexpected template key %q", shipped.Key)
	}
	if err := validateReportTemplatePayload(&shipped.Payload); err != nil {
		t.Fatalf("shipped template violates its contract: %v", err)
	}
	if shipped.Payload.OutlineConfirmation != model.ReportOutlineRequired {
		t.Fatal("P0-A must ship outline_confirmation=required")
	}
	if len(shipped.Payload.AutoConfirmAllowedRoles) != 0 {
		t.Fatal("P0-A must not ship auto-confirm roles")
	}
	if shipped.Payload.Retrieval.ThresholdPolicy != "dataset_calibrated" {
		t.Fatal("shipped template must use the calibration policy, not a literal threshold")
	}
	keys := reportTemplateSectionOrder(&shipped.Payload)
	if len(keys) != 5 {
		t.Fatalf("compliance template must have 5 sections, got %v", keys)
	}
	// Order is the body order: background → rules → judgement → gaps → actions.
	want := []string{"background", "applicable_rules", "compliance", "gaps", "remediation"}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("section order drifted: %v", keys)
		}
	}
}

func TestBuiltinReportTemplatesAreDeepCopied(t *testing.T) {
	first := BuiltinReportTemplates()
	first[0].Payload.OutputFormats[0] = "pdf"
	first[0].Payload.Sections[0].Title = "mutated"
	second := BuiltinReportTemplates()
	if second[0].Payload.OutputFormats[0] != "docx" || second[0].Payload.Sections[0].Title == "mutated" {
		t.Fatal("BuiltinReportTemplates must hand out a copy, not the package baseline")
	}
}

func TestEnsureBuiltinReportTemplatesIsIdempotent(t *testing.T) {
	fx := newReportFixture(t, "seed")
	ctx := context.Background()
	// The fixture seeds the platform baseline once; a second pass must not.
	created, err := fx.svc.EnsureBuiltinReportTemplates(ctx, model.PlatformTenantID)
	if err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("second bootstrap must create nothing, created %d", created)
	}
	// A workspace with no fork gets the baseline key on demand.
	if _, err := fx.svc.EnsureBuiltinReportTemplates(ctx, fx.tenantID); err != nil {
		t.Fatal(err)
	}
	forked, err := fx.svc.Store.GetReportTemplate(ctx, fx.tenantID, reportComplianceTemplateKey, 1)
	if err != nil || forked == nil {
		t.Fatalf("workspace seed must create version 1: %v", err)
	}
	// And a second workspace pass leaves the fork exactly as it is.
	again, err := fx.svc.EnsureBuiltinReportTemplates(ctx, fx.tenantID)
	if err != nil || again != 0 {
		t.Fatalf("workspace reseed must be a no-op, created %d (%v)", again, err)
	}
	row, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, reportComplianceTemplateKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil || row.Version != 1 {
		t.Fatalf("baseline must stay at version 1, got %+v", row)
	}
	payload, err := reportTemplatePayloadOf(row)
	if err != nil {
		t.Fatalf("stored baseline is unreadable: %v", err)
	}
	shipped := BuiltinReportTemplates()[0].Payload
	if hashReportTemplatePayload(payload) != hashReportTemplatePayload(&shipped) {
		t.Fatal("stored baseline must round-trip to the shipped payload")
	}
}

func TestEnsureBuiltinReportTemplatesAppendsVersionOnChange(t *testing.T) {
	fx := newReportFixture(t, "uspend")
	ctx := context.Background()
	// Simulate a baseline written by an older release: same key, different content.
	stale := BuiltinReportTemplates()[0].Payload
	stale.Sections[0].Title = "旧版背景"
	staleRow, err := reportTemplateRow(model.PlatformTenantID, reportComplianceTemplateKey, 1, &stale, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportTemplate{}).
		Where("tenant_id = ? AND template_key = ? AND version = 1", model.PlatformTenantID, reportComplianceTemplateKey).
		Update("sections_json", staleRow.SectionsJSON).Error; err != nil {
		t.Fatal(err)
	}
	created, err := fx.svc.EnsureBuiltinReportTemplates(ctx, model.PlatformTenantID)
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("a changed baseline must append exactly one version, created %d", created)
	}
	latest, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, reportComplianceTemplateKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 2 {
		t.Fatalf("new version must be 2, got %d", latest.Version)
	}
	// The old version is immutable: a report pinned to v1 keeps reading v1.
	old, err := fx.svc.Store.GetReportTemplate(ctx, model.PlatformTenantID, reportComplianceTemplateKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	oldPayload, err := reportTemplatePayloadOf(old)
	if err != nil {
		t.Fatal(err)
	}
	if oldPayload.Sections[0].Title != "旧版背景" {
		t.Fatal("appending a version must not rewrite the previous one")
	}
	// And the bump happens exactly once: a third pass is a no-op.
	again, err := fx.svc.EnsureBuiltinReportTemplates(ctx, model.PlatformTenantID)
	if err != nil || again != 0 {
		t.Fatalf("reseed after a bump must create nothing, created %d (%v)", again, err)
	}
}

func TestListReportTemplatesPrefersWorkspaceVersion(t *testing.T) {
	fx := newReportFixture(t, "overlay")
	ctx := context.Background()
	stale := BuiltinReportTemplates()[0].Payload
	stale.Name = ReportTemplateName{Zh: "租户定制模板", En: "Tenant custom"}
	row, err := reportTemplateRow(fx.tenantID, reportComplianceTemplateKey, 1, &stale, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.svc.Store.CreateReportTemplate(ctx, row); err != nil {
		t.Fatal(err)
	}
	views, err := fx.svc.ListReportTemplates(ctx, fx.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("one key must render one row, got %d (%+v)", len(views), views)
	}
	view := views[0]
	if view.TenantID != fx.tenantID || view.Payload.Name.Zh != "租户定制模板" {
		t.Fatalf("workspace version must win over the platform baseline: %+v", view)
	}
	if view.Retired {
		t.Fatal("an active template must not be flagged retired")
	}
}

func TestResolveReportTemplateFallsBackToBaseline(t *testing.T) {
	fx := newReportFixture(t, "resolve")
	ctx := context.Background()
	row, err := fx.svc.resolveReportTemplate(ctx, fx.tenantID, reportComplianceTemplateKey, 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if row.TenantID != model.PlatformTenantID {
		t.Fatalf("a workspace without a fork reads the platform baseline, got tenant %q", row.TenantID)
	}
	if _, err := fx.svc.resolveReportTemplate(ctx, fx.tenantID, "", 0); err == nil {
		t.Fatal("an empty key must not resolve")
	}
}
