package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// versionProbeClient overrides EngineVersion while embedding ragflow.Mock for
// the remaining Client surface, so tests can simulate an engine inside or
// outside the verified version band without a live engine.
type versionProbeClient struct {
	*ragflow.Mock
	version string
}

func (v versionProbeClient) EngineVersion(ctx context.Context) (string, error) {
	return v.version, nil
}

// createPassingProductionRelease builds the full governance chain, takes a
// PASS decision and creates the production release under the default mock
// engine (0.27.1, below the verified band) — the happy path that must records
// degrade evidence rather than blocking.
func createPassingProductionRelease(t *testing.T) (*Service, context.Context, model.Tenant, *model.User, *model.Release) {
	t.Helper()
	svc, ctx, tenant, user, candidate, _, bundle := newCompletedGovernanceChain(t, true)
	gate, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: "production", SubGateStates: json.RawMessage(passingSubGates("PASS")),
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gate.Decision != model.GateDecisionPass {
		t.Fatalf("gate decision = %s, want PASS", gate.Decision)
	}
	release, err := svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: bundle.SnapshotID, GateDecisionID: gate.ID, Environment: "production",
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, ctx, tenant, user, release
}

func releaseGateProxy(t *testing.T, env, version string) (*Service, context.Context, model.Tenant, *model.User, *model.ReleaseCandidate, *model.ReleaseGateDecision, *model.EvidenceBundle) {
	t.Helper()
	svc, ctx, tenant, user, candidate, _, bundle := newCompletedGovernanceChain(t, true)
	gate, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: env, SubGateStates: json.RawMessage(passingSubGates("PASS")),
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.RAGFlow = versionProbeClient{ragflow.NewMock(), version}
	return svc, ctx, tenant, user, candidate, gate, bundle
}

// TestReleaseCreateProductionRecordsVersionPreflight pins the T6.3 wiring: the
// mock engine (0.27.1) is below the verified band, so production release
// proceeds but the observed/decision/reason evidence lands on the release row.
func TestReleaseCreateProductionRecordsVersionPreflight(t *testing.T) {
	svc, ctx, tenant, _, release := createPassingProductionRelease(t)
	if release.VersionPreflightObserved != "0.27.1" {
		t.Fatalf("preflight observed = %q, want 0.27.1", release.VersionPreflightObserved)
	}
	if release.VersionPreflightDecision != string(ragflow.VersionGateDegrade) {
		t.Fatalf("preflight decision = %q, want degrade", release.VersionPreflightDecision)
	}
	if release.VersionPreflightReason == "" {
		t.Fatal("preflight reason must be recorded")
	}
	// reloaded row keeps the evidence
	got, err := svc.Store.GetRelease(ctx, tenant.ID, release.ID)
	if err != nil || got == nil {
		t.Fatalf("get release: %+v err=%v", got, err)
	}
	if got.VersionPreflightObserved != "0.27.1" || got.VersionPreflightDecision != string(ragflow.VersionGateDegrade) {
		t.Fatalf("reloaded preflight evidence lost: %+v", got)
	}
}

// TestReleaseCreateProductionBlocksNewerThanVerifiedBand pins the fail-closed
// behaviour: an engine version exceeding the verified ceiling (v1.0.0-rc2 over
// v1.0.0-rc1) blocks production release until a live contract re-drill has
// closed the band.
func TestReleaseCreateProductionBlocksNewerThanVerifiedBand(t *testing.T) {
	svc, ctx, tenant, user, candidate, gate, bundle := releaseGateProxy(t, "production", "v1.0.0-rc2")
	_, err := svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: bundle.SnapshotID, GateDecisionID: gate.ID, Environment: "production",
	})
	if err == nil {
		t.Fatal("expected version-band verification_required to block production release")
	}
	he, ok := err.(*httperr.Error)
	if !ok || he.Status != 403 || he.Code != 40076 {
		t.Fatalf("error = %#v, want httperr 403/40076", err)
	}
	if !strings.Contains(he.Message, "version preflight") {
		t.Fatalf("error message = %q, want version preflight detail", he.Message)
	}
}

// TestReleaseCreateProductionBlocksUnparseableVersion pins fail-closed for an
// engine version that the comparator cannot parse.
func TestReleaseCreateProductionBlocksUnparseableVersion(t *testing.T) {
	svc, ctx, tenant, user, candidate, gate, bundle := releaseGateProxy(t, "production", "not-a-version")
	_, err := svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: bundle.SnapshotID, GateDecisionID: gate.ID, Environment: "production",
	})
	if err == nil {
		t.Fatal("expected unparseable engine version to block production release")
	}
	he, ok := err.(*httperr.Error)
	if !ok || he.Status != 403 || he.Code != 40076 {
		t.Fatalf("error = %#v, want httperr 403/40076", err)
	}
}

// TestReleaseCreateDevelopmentRecordsButDoesNotBlock: the same out-of-band
// engine is recorded for a development release but does not block it — the
// band fail-closed applies to production only.
func TestReleaseCreateDevelopmentRecordsButDoesNotBlock(t *testing.T) {
	svc, ctx, tenant, user, candidate, gate, bundle := releaseGateProxy(t, "development", "v1.0.0-rc2")
	release, err := svc.CreateRelease(ctx, tenant.ID, user.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: bundle.SnapshotID, GateDecisionID: gate.ID, Environment: "development",
	})
	if err != nil {
		t.Fatalf("development release must not be blocked by version preflight: %v", err)
	}
	if release.VersionPreflightDecision != string(ragflow.VersionGateVerificationRequired) {
		t.Fatalf("preflight decision = %q, want verification_required recorded", release.VersionPreflightDecision)
	}
}
