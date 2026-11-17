package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// CheckReportTemplateCapabilities is the read-only capability probe behind
// `POST /report-templates/:key/capability-check` (§11.4 / K57). It reuses the
// scenario-wizard's EvaluateRequirements and the same contract the create-time
// recheck applies, so the wizard's preview and the server's 44102 gate cannot
// disagree. Unlike the scenario check it persists no observation rows: report
// evidence is ledger-fed, and a shadow table would advertise a repair channel
// report runs do not have.
func TestCheckReportTemplateCapabilitiesReportsSatisfied(t *testing.T) {
	fx := newReportFixture(t, "cap-check")
	ctx := context.Background()
	view, err := fx.svc.CheckReportTemplateCapabilities(ctx, fx.owner.ID, fx.tenantID,
		reportComplianceTemplateKey, CapabilityCheckInput{
			AppType:  "chat",
			Datasets: []CapabilityCheckDataset{{Role: scenarioDatasetRolePrimary, DatasetID: fx.datasetLink}},
		})
	if err != nil {
		t.Fatalf("capability check: %v", err)
	}
	if len(view.Datasets) != 1 {
		t.Fatalf("expected exactly one dataset verdict, got %d", len(view.Datasets))
	}
	got := view.Datasets[0]
	if got.DatasetID != fx.datasetLink || got.Role != scenarioDatasetRolePrimary {
		t.Fatalf("verdict must address the probed dataset/role: %+v", got)
	}
	// The fixture's dataset is parsed, chunked and embedding-bound, so the report
	// contract (vector at operational) must be honestly satisfied — the sibling
	// scenario mock has no embedding model and stops at repairable, which is why
	// this probe stays satisfied only for the report fixture.
	if view.Verdict != CapabilityVerdictSatisfied {
		t.Fatalf("expected satisfied, got %q (%+v)", view.Verdict, got.Requirements)
	}
	if got.ProbeStatus != CapabilityProbeSuccess {
		t.Fatalf("probe must succeed against the seeded dataset, got %q", got.ProbeStatus)
	}
	if view.TemplateVersion <= 0 || view.TemplateID == "" {
		t.Fatalf("check must resolve the template version: %+v", view)
	}
	if view.CapabilitySetVersion != CapabilityCheckVersion {
		t.Fatalf("capability set version missing: %q", view.CapabilitySetVersion)
	}
}

func TestCheckReportTemplateCapabilitiesRequiresDatasets(t *testing.T) {
	fx := newReportFixture(t, "cap-empty")
	ctx := context.Background()
	_, err := fx.svc.CheckReportTemplateCapabilities(ctx, fx.owner.ID, fx.tenantID,
		reportComplianceTemplateKey, CapabilityCheckInput{})
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != 40105 || httpErr.Status != 400 {
		t.Fatalf("empty datasets must be 400/40105, got %v", err)
	}
}

func TestCheckReportTemplateCapabilitiesUnknownTemplate(t *testing.T) {
	fx := newReportFixture(t, "cap-unknown")
	ctx := context.Background()
	_, err := fx.svc.CheckReportTemplateCapabilities(ctx, fx.owner.ID, fx.tenantID,
		"no-such-template", CapabilityCheckInput{
			Datasets: []CapabilityCheckDataset{{Role: scenarioDatasetRolePrimary, DatasetID: fx.datasetLink}},
		})
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportTemplateNotFound || httpErr.Status != 404 {
		t.Fatalf("unknown template must be 404/44101, got %v", err)
	}
}
