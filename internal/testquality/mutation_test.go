package testquality

import (
	"strings"
	"testing"
	"time"
)

func mutationTestManifest(generatedAt time.Time) MutationEvidenceManifest {
	nextReviewAt := generatedAt.AddDate(0, 1, 0)
	reviewedAt := generatedAt.Format(time.RFC3339)
	return MutationEvidenceManifest{
		Version:      1,
		GeneratedAt:  generatedAt.Format(time.RFC3339),
		NextReviewAt: nextReviewAt.Format(time.RFC3339),
		Records: []MutationEvidenceRecord{
			{ScenarioID: "SC-TENANT-001", TestPackage: "internal/service", TestName: "TestP0_TENANT_001_ResolveTenantScopeRejectsTenantGovernanceRequests", EvidenceType: mutationEvidenceNegativeProof, Target: "tenant-filter", Mutation: "remove tenant scope", ExpectedFailure: "cross-tenant request succeeds", ActualResult: "request is denied", ReviewedAt: reviewedAt},
			{ScenarioID: "SC-AUTHZ-001", TestPackage: "internal/middleware", TestName: "TestP0_AUTHZ_001_RBACRejectsCrossTenantGovernanceTarget", EvidenceType: mutationEvidenceNegativeProof, Target: "permission", Mutation: "bypass permission check", ExpectedFailure: "unauthorized actor is allowed", ActualResult: "permission is denied", ReviewedAt: reviewedAt},
			{ScenarioID: "SC-APPROVAL-001", TestPackage: "internal/service", TestName: "TestP0_APPROVAL_001_RequesterCannotApproveOwn", EvidenceType: mutationEvidenceNegativeProof, Target: "state-check", Mutation: "allow requester approval", ExpectedFailure: "self approval succeeds", ActualResult: "transition is rejected", ReviewedAt: reviewedAt},
			{ScenarioID: "SC-AUDIT-001", TestPackage: "internal/service", TestName: "TestP0_AUDIT_001_AuditAnchorCreatesImmutableTenantTails", EvidenceType: mutationEvidenceNegativeProof, Target: "audit-write", Mutation: "skip audit append", ExpectedFailure: "audit tail is absent", ActualResult: "audit assertion fails", ReviewedAt: reviewedAt},
			{ScenarioID: "SC-QUOTA-001", TestPackage: "internal/repository", TestName: "TestP0_QUOTA_001_ReserveQuotaTokensConcurrentNoOvershoot", EvidenceType: mutationEvidenceNegativeProof, Target: "quota-deduction", Mutation: "remove quota deduction", ExpectedFailure: "overshoot is permitted", ActualResult: "overshoot assertion fails", ReviewedAt: reviewedAt},
		},
	}
}

func TestValidateMutationEvidenceAcceptsCatalogueAlignedSample(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	catalogue, err := LoadScenarioCatalogue("testdata/scenario_catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	report, err := ValidateMutationEvidenceWithCatalogue(mutationTestManifest(now), catalogue, now)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Total != 5 || report.Valid != 5 || report.Invalid != 0 || report.Survived != 0 {
		t.Fatalf("unexpected mutation report: %+v", report)
	}
}

func TestValidateMutationEvidenceRejectsNonApplicableTarget(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	catalogue, err := LoadScenarioCatalogue("testdata/scenario_catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest := mutationTestManifest(now)
	manifest.Records[0].Target = "network-policy"
	_, err = ValidateMutationEvidenceWithCatalogue(manifest, catalogue, now)
	if err == nil || !strings.Contains(err.Error(), "not applicable") {
		t.Fatalf("expected applicable-target failure, got %v", err)
	}
}

func TestValidateMutationEvidenceRequiresActionForSurvivor(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	catalogue, err := LoadScenarioCatalogue("testdata/scenario_catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest := mutationTestManifest(now)
	manifest.Records[1].MutationSurvived = true
	report, err := ValidateMutationEvidenceWithCatalogue(manifest, catalogue, now)
	if err == nil || !strings.Contains(err.Error(), "requires action") {
		t.Fatalf("expected survivor action failure, got %v", err)
	}
	if report.Passed || report.Invalid != 1 {
		t.Fatalf("unexpected mutation report: %+v", report)
	}
}

func TestValidateMutationEvidenceAllowsSurvivorWithAction(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	catalogue, err := LoadScenarioCatalogue("testdata/scenario_catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest := mutationTestManifest(now)
	manifest.Records[1].MutationSurvived = true
	manifest.Records[1].Action = "strengthen the authorization assertion"
	report, err := ValidateMutationEvidenceWithCatalogue(manifest, catalogue, now)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Survived != 1 {
		t.Fatalf("expected survivor to pass with action, got %+v", report)
	}
}
