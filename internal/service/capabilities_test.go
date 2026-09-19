package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type failingEngine struct {
	ragflow.Client
}

func (failingEngine) EngineVersion(context.Context) (string, error) {
	return "", errors.New("engine unavailable")
}

type fixedVersionEngine struct {
	ragflow.Client
	version string
}

func (e fixedVersionEngine) EngineVersion(context.Context) (string, error) {
	return e.version, nil
}

func TestRAGFlowCapabilityVerificationSeparatesVerifiedFromRuntimeHealth(t *testing.T) {
	svc := &Service{RAGFlow: ragflow.NewMock(), capabilities: newCapabilityRegistry()}
	initial := svc.ListRAGFlowCapabilities()
	if len(initial.Items) != 10 {
		t.Fatalf("declared capabilities = %d, want 10", len(initial.Items))
	}
	if initial.RuntimeHealth != RuntimeUnknown {
		t.Fatalf("initial runtime health = %s", initial.RuntimeHealth)
	}
	report, err := svc.VerifyRAGFlowCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.DetectedVersion != "0.27.1" || report.RuntimeHealth != RuntimeHealthy {
		t.Fatalf("report: %+v", report)
	}
	byName := map[string]RAGFlowCapability{}
	for _, item := range report.Items {
		byName[item.Name] = item
	}
	if byName["chat"].Status != CapabilityVerified || byName["chat"].VerifiedVersion != "0.27.1" {
		t.Fatalf("chat capability: %+v", byName["chat"])
	}
	if byName["structured_output"].Status != CapabilityUnverified {
		t.Fatalf("untested capability must remain unverified: %+v", byName["structured_output"])
	}
	if byName["chat"].RuntimeHealth != RuntimeHealthy {
		t.Fatalf("runtime health changed verified capability: %+v", byName["chat"])
	}
}

func TestRAGFlowCapabilityVerificationFailsClosedOnEngineError(t *testing.T) {
	svc := &Service{RAGFlow: failingEngine{}, capabilities: newCapabilityRegistry()}
	report, err := svc.VerifyRAGFlowCapabilities(context.Background())
	if err == nil {
		t.Fatal("expected verification error")
	}
	if report.RuntimeHealth != RuntimeUnavailable {
		t.Fatalf("runtime health = %s, want UNAVAILABLE", report.RuntimeHealth)
	}
	for _, item := range report.Items {
		if item.Status == CapabilityVerified {
			t.Fatalf("verified capability changed on runtime failure: %+v", item)
		}
	}
}

func TestRAGFlowCapabilityUpgradeGateRequiresRealContractEvidence(t *testing.T) {
	verifiedSvc := &Service{RAGFlow: fixedVersionEngine{version: "0.27.1"}, capabilities: newCapabilityRegistry()}
	verified, err := verifiedSvc.VerifyRAGFlowCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if verified.UpgradeDecision != "READY" || len(verified.BlockingCapabilities) != 0 {
		t.Fatalf("0.27 upgrade gate: %+v", verified)
	}

	candidate := func() RAGFlowCapabilityReport {
		svc := &Service{RAGFlow: fixedVersionEngine{version: "0.28.0"}, capabilities: newCapabilityRegistry()}
		report, err := svc.VerifyRAGFlowCapabilities(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return report
	}()
	byName := map[string]RAGFlowCapability{}
	for _, item := range candidate.Items {
		byName[item.Name] = item
	}
	if byName["chat"].Status != CapabilityCompatible || byName["chat"].VerifiedVersion != "" {
		t.Fatalf("0.28 must be a compatible trial, not verified: %+v", byName["chat"])
	}
	if candidate.UpgradeDecision != "BLOCKED_CONTRACT_DRILL_REQUIRED" ||
		len(candidate.BlockingCapabilities) == 0 || len(candidate.TrialCapabilities) == 0 {
		t.Fatalf("0.28 upgrade gate: %+v", candidate)
	}
}

func TestRAGFlowCapabilityUpgradeGateRejectsUnknownAndUnsupportedVersions(t *testing.T) {
	unknown := (&Service{RAGFlow: ragflow.NewMock(), capabilities: newCapabilityRegistry()}).ListRAGFlowCapabilities()
	if unknown.UpgradeDecision != "UNKNOWN_RUNTIME" {
		t.Fatalf("unknown runtime gate: %+v", unknown)
	}

	svc := &Service{RAGFlow: fixedVersionEngine{version: "0.29.0"}, capabilities: newCapabilityRegistry()}
	report, err := svc.VerifyRAGFlowCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.UpgradeDecision != "BLOCKED_VERSION_NOT_APPROVED" {
		t.Fatalf("unsupported version gate: %+v", report)
	}
}

func TestRAGFlowCapabilityMatrixClassifiesZeroPointTwentyEightAsTrial(t *testing.T) {
	svc := &Service{RAGFlow: fixedVersionEngine{version: "0.28.0"}, capabilities: newCapabilityRegistry()}
	report, err := svc.VerifyRAGFlowCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]RAGFlowCapability{}
	for _, item := range report.Items {
		byName[item.Name] = item
	}
	if byName["chat"].Status != CapabilityCompatible || byName["chat"].VerifiedVersion != "" {
		t.Fatalf("chat capability: %+v", byName["chat"])
	}
	if byName["agent"].Fallback != "private endpoint disabled" {
		t.Fatalf("agent fallback must keep private endpoint out of the main path: %+v", byName["agent"])
	}
}
