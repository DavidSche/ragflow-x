package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestReleaseGateEvidenceReportFailsClosedWithoutAutomaticGate(t *testing.T) {
	svc, ctx, _, _, candidate, _, _ := newCompletedGovernanceChain(t, true)
	gates, total, err := svc.Store.ListGateDecisions(ctx, candidate.TenantID, candidate.CandidateID, candidate.CandidateVersion, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(gates) != 0 {
		t.Fatalf("fixture must not pre-create a release gate: total=%d gates=%d", total, len(gates))
	}
	report, err := svc.GetReleaseGateEvidenceReport(
		ctx, candidate.TenantID, candidate.CandidateID, candidate.CandidateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !report.AdvisoryOnly || len(report.Items) != len(model.ReleaseGateNames) {
		t.Fatalf("advisory contract mismatch: advisory=%v items=%d", report.AdvisoryOnly, len(report.Items))
	}
	states := map[string]string{}
	for _, item := range report.Items {
		states[item.Gate] = item.State
	}
	if states[model.ReleaseGateImprovement] != ReleaseGateEvidencePass {
		t.Fatalf("explicit completed evaluation must PASS: %+v", states[model.ReleaseGateImprovement])
	}
	for _, name := range model.ReleaseGateNames {
		if states[name] == "" {
			t.Fatalf("gate %s is missing from report: %+v", name, report)
		}
	}
	if report.OverallState != ReleaseGateEvidenceWait {
		t.Fatalf("missing evidence must produce WAIT, got %s", report.OverallState)
	}
	gates, total, err = svc.Store.ListGateDecisions(ctx, candidate.TenantID, candidate.CandidateID, candidate.CandidateVersion, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(gates) != 0 {
		t.Fatalf("evidence report must not create a gate: total=%d gates=%d", total, len(gates))
	}
	updated, err := svc.Store.GetReleaseCandidateVersion(
		ctx, candidate.TenantID, candidate.CandidateID, candidate.CandidateVersion,
	)
	if err != nil || updated == nil {
		t.Fatalf("candidate lookup: candidate=%+v err=%v", updated, err)
	}
	if updated.Status != model.CandidateEvaluated {
		t.Fatalf("evidence report must not change candidate status: %s", updated.Status)
	}
}

func TestReleaseGateEvidenceReportBlocksFailedCompilation(t *testing.T) {
	svc, fixture := newReleaseFixture(t, "evidence-block")
	ctx := context.Background()
	candidate, err := svc.CreateReleaseCandidate(ctx, fixture.tenantID, "tester", ReleaseCandidateInput{
		TargetType: "assistant", TargetID: fixture.assistantID, TargetVersion: "v1",
		CandidateID: "cand-" + id.New()[:8], CandidateVersion: 1,
		ChangeSummary: "release evidence", Manifest: json.RawMessage(manifestJSON()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateExecutionSnapshot(ctx, fixture.tenantID, "tester", ExecutionSnapshotInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotSchemaVersion: "v1", ExecutionConfig: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.CreateCompilationRun(ctx, &model.CompilationRun{
		ID: id.New(), TenantID: fixture.tenantID, ProjectID: fixture.projectID,
		DatasetID: fixture.datasetID, Kind: model.CompilationKindPageIndex,
		IdempotencyKey: "failed-" + candidate.CandidateID, State: model.CompilationRunFailed,
		ManifestHash: "hash", TemplateHash: "hash", PipelineHash: "hash", DocumentIDs: "[]",
		SourceConsistency: model.CompilationSourceConsistencyUnknown, RequestedBy: "tester",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := svc.GetReleaseGateEvidenceReport(
		ctx, fixture.tenantID, candidate.CandidateID, candidate.CandidateVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	var compilation *ReleaseGateEvidenceItem
	for index := range report.Items {
		if report.Items[index].Gate == model.ReleaseGateCompilationContract {
			compilation = &report.Items[index]
			break
		}
	}
	if compilation == nil || compilation.State != ReleaseGateEvidenceBlock {
		t.Fatalf("failed compilation must BLOCK: %+v", compilation)
	}
	if report.OverallState != ReleaseGateEvidenceBlock {
		t.Fatalf("block evidence must produce BLOCK, got %s", report.OverallState)
	}
}
