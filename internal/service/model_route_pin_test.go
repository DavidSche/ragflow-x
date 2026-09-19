package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestEnterpriseModelRouteRuntimePinRejectsChangedVersions(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	workspace, err := svc.CreateTenant(ctx, "Route Workspace")
	if err != nil {
		t.Fatal(err)
	}
	provider := &model.ModelProvider{
		ID: "route-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible",
		Name: "Route Provider", BaseURL: "https://internal.llm.example/v1",
		Enabled: true, Status: model.ProviderStatusActive,
	}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "enterprise-model", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Route Connection",
		BaseURL: "https://internal.llm.example/v1", CredentialRef: "vault://route/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspace.ID,
		AllowedCapabilities: []string{"chat"}, AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1,
		ModelRef: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pin.Version != 1 || pin.ConnectionVersion != 1 || pin.BindingVersion != 1 {
		t.Fatalf("unexpected pin: %+v", pin)
	}
	route, err = svc.Store.GetModelRoute(ctx, workspace.ID, route.ID)
	if err != nil || route.CurrentPinID != pin.PinID || route.CurrentPinVersion != 1 {
		t.Fatalf("route pointer was not advanced: route=%+v err=%v", route, err)
	}
	validated, err := svc.ValidateEnterpriseModelRoutePin(ctx, route)
	if err != nil || validated == nil || validated.PinID != pin.PinID {
		t.Fatalf("runtime pin validation failed: pin=%+v err=%v", validated, err)
	}
	if ModelRoutePinReference(validated) != "pin:"+pin.PinID+":v1" {
		t.Fatalf("unexpected pin reference: %s", ModelRoutePinReference(validated))
	}
	if err := svc.DeleteModelRoute(ctx, workspace.ID, route.ID); err == nil {
		t.Fatal("pinned model route must not be deleted")
	}

	if _, err := svc.UpdateEnterpriseConnection(ctx, admin.ID, connection.Connection.ID, UpdateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Route Connection v2",
		BaseURL: "https://internal.llm.example/v2", CredentialRef: "vault://route/openai",
		CredentialVersion: "v2", CredentialPolicyVersion: "v1",
		Visibility:      model.EnterpriseConnectionVisibilityShared,
		UsableBy:        EnterpriseConnectionUsableByBindings,
		ManagedBy:       model.EnterpriseConnectionManagedByPlatform,
		CredentialScope: model.EnterpriseConnectionCredentialScopeConnection,
	}); err != nil {
		t.Fatal(err)
	}
	validated, err = svc.ValidateEnterpriseModelRoutePin(ctx, route)
	if err != nil || validated == nil {
		t.Fatal("runtime must continue using the exact pinned connection version")
	}
	if validated.ConnectionVersion != 1 || validated.BindingVersion != 1 {
		t.Fatalf("runtime moved from immutable versions: %+v", validated)
	}
}

func TestExecutionSnapshotCapturesEnterpriseRoutePin(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	workspace, err := svc.CreateTenant(ctx, "Snapshot Workspace")
	if err != nil {
		t.Fatal(err)
	}
	provider := &model.ModelProvider{
		ID: "snapshot-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible",
		Name: "Snapshot Provider", BaseURL: "https://internal.llm.example/v1",
		Enabled: true, Status: model.ProviderStatusActive,
	}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "snapshot-model", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Snapshot Connection",
		BaseURL: "https://internal.llm.example/v1", CredentialRef: "vault://snapshot/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspace.ID,
		AllowedCapabilities: []string{"chat"}, AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1, ModelRef: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidateID := id.New()[:12]
	candidate, err := svc.CreateReleaseCandidate(ctx, workspace.ID, admin.ID, ReleaseCandidateInput{
		TargetType: "assistant", TargetID: candidateID, TargetVersion: "v1",
		CandidateID: candidateID, CandidateVersion: 1, Manifest: json.RawMessage(manifestJSON()),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.CreateExecutionSnapshot(ctx, workspace.ID, admin.ID, ExecutionSnapshotInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: 1,
		SnapshotSchemaVersion: "v1", ExecutionConfig: json.RawMessage(`{"mode":"read_only"}`),
		ModelRouteVersion: ModelRoutePinReference(pin),
		ModelRoutePinID:   pin.PinID, ModelRoutePinVersion: pin.Version,
		EnterpriseConnectionID: pin.ConnectionID, EnterpriseConnectionVersion: pin.ConnectionVersion,
		EnterpriseBindingID: pin.BindingID, EnterpriseBindingVersion: pin.BindingVersion,
		EnterpriseModelRef: pin.ModelRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ModelRouteVersion != ModelRoutePinReference(pin) ||
		snapshot.ModelRoutePinID != pin.PinID || snapshot.ModelRoutePinVersion != pin.Version ||
		snapshot.EnterpriseConnectionVersion != 1 ||
		snapshot.EnterpriseBindingVersion != 1 || snapshot.EnterpriseModelRef != "gpt-4o-mini" {
		t.Fatalf("snapshot did not capture exact enterprise pin: %+v", snapshot)
	}
}

func TestReleaseCreationRevalidatesEnterprisePin(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v err=%v", admin, err)
	}
	workspace, err := svc.CreateTenant(ctx, "Release Pin Workspace")
	if err != nil {
		t.Fatal(err)
	}
	provider := &model.ModelProvider{
		ID: "release-pin-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible",
		Name: "Release Pin Provider", BaseURL: "https://internal.llm.example/v1",
		Enabled: true, Status: model.ProviderStatusActive,
	}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "release-pin", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Release Pin Connection",
		BaseURL: "https://internal.llm.example/v1", CredentialRef: "vault://release-pin/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspace.ID,
		AllowedCapabilities: []string{"chat"}, AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1, ModelRef: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidateID := id.New()[:12]
	candidate, err := svc.CreateReleaseCandidate(ctx, workspace.ID, admin.ID, ReleaseCandidateInput{
		TargetType: "assistant", TargetID: candidateID, TargetVersion: "v2",
		CandidateID: candidateID, CandidateVersion: 1, Manifest: json.RawMessage(manifestJSON()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkReleaseCandidateReady(ctx, workspace.ID, candidate.CandidateID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.CreateExecutionSnapshot(ctx, workspace.ID, admin.ID, ExecutionSnapshotInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: 1,
		SnapshotSchemaVersion: "v1", ExecutionConfig: json.RawMessage(`{"mode":"read_only"}`),
		ModelRouteVersion: ModelRoutePinReference(pin), ModelRoutePinID: pin.PinID, ModelRoutePinVersion: pin.Version,
		EnterpriseConnectionID: pin.ConnectionID, EnterpriseConnectionVersion: pin.ConnectionVersion,
		EnterpriseBindingID: pin.BindingID, EnterpriseBindingVersion: pin.BindingVersion,
		EnterpriseModelRef: pin.ModelRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, workspace.ID, admin.ID, EvaluationSetVersionInput{
		EvalSetID: candidateID + "-set", Version: 1, Cases: json.RawMessage(`[{"case_id":"case-1"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	caseVersion, err := svc.CreateEvaluationCaseVersion(ctx, workspace.ID, admin.ID, EvaluationCaseVersionInput{
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, CaseID: "case-1", CaseVersion: 1,
		Question: "What is X?", ExpectedAnswer: "X is 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, workspace.ID, admin.ID, EvaluationRunInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		ExecutionSnapshotID: snapshot.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	pass := true
	if _, err := svc.AddEvaluationCaseResult(ctx, workspace.ID, admin.ID, EvaluationCaseResultInput{
		RunID: run.ID, CaseID: caseVersion.CaseID, CaseVersionID: caseVersion.ID,
		CaseVersionHash: caseVersion.Hash, ActualAnswer: "X is 1", References: json.RawMessage(`[]`),
		Metrics: json.RawMessage(`{}`), Pass: &pass,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteEvaluationRun(ctx, workspace.ID, run.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	bundle, err := svc.CreateEvidenceBundle(ctx, workspace.ID, admin.ID, EvidenceBundleInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvaluationRunID: run.ID, SecurityEvidence: json.RawMessage(`{}`), PolicyEvidence: json.RawMessage(`{}`),
		RiskEvidence: json.RawMessage(`{}`), PermissionEvidence: json.RawMessage(`{}`),
		ConfigurationEvidence: json.RawMessage(`{}`), ApprovalEvidence: json.RawMessage(`{}`),
		EvidenceItems: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := svc.EvaluateReleaseGate(ctx, workspace.ID, admin.ID, GateDecisionInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		EvidenceBundleID: bundle.ID, Environment: "development", SubGateStates: json.RawMessage(passingSubGates("PASS")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeprecateEnterpriseConnection(ctx, admin.ID, connection.Connection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRelease(ctx, workspace.ID, admin.ID, ReleaseInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: candidate.CandidateVersion,
		SnapshotID: snapshot.ID, GateDecisionID: gate.ID, Environment: "development",
	}); err == nil {
		t.Fatal("deprecated enterprise pin must block release creation")
	}
}

func TestReleaseCandidateModelRoutePackageCapturesEnterprisePin(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}
	workspace, err := svc.CreateTenant(ctx, "Route Candidate Workspace")
	if err != nil {
		t.Fatal(err)
	}
	provider := &model.ModelProvider{
		ID: "route-candidate-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible",
		Name: "Route Candidate Provider", BaseURL: "https://internal.llm.example/v1",
		Enabled: true, Status: model.ProviderStatusActive,
	}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "candidate-model", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := svc.CreateEnterpriseConnection(ctx, admin.ID, admin.TenantID, CreateEnterpriseConnectionRequest{
		ProviderName: "openai-api-compatible", DisplayName: "Candidate Connection",
		BaseURL: "https://internal.llm.example/v1", CredentialRef: "vault://candidate/openai",
		Visibility: model.EnterpriseConnectionVisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := svc.CreateEnterpriseBinding(ctx, admin.ID, CreateEnterpriseBindingRequest{
		ConnectionID: connection.Connection.ID, TenantID: workspace.ID,
		AllowedCapabilities: []string{"chat"}, AllowedModelRefs: []string{"gpt-4o-mini"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pin, err := svc.PinEnterpriseModelRoute(ctx, admin.ID, workspace.ID, route.ID, PinEnterpriseModelRouteRequest{
		ConnectionID: connection.Connection.ID, ConnectionVersion: 1,
		BindingID: binding.Binding.BindingID, BindingVersion: 1, ModelRef: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidateID := id.New()[:12]
	candidate, err := svc.CreateReleaseCandidate(ctx, workspace.ID, admin.ID, ReleaseCandidateInput{
		TargetType: "model_route_package", TargetID: route.ID, TargetVersion: "v1",
		CandidateID: candidateID, CandidateVersion: 1, Manifest: json.RawMessage(manifestJSON()),
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ModelRoute struct {
			ModelRouteVersion           string `json:"model_route_version"`
			ModelRoutePinID             string `json:"model_route_pin_id"`
			ModelRoutePinVersion        int64  `json:"model_route_pin_version"`
			EnterpriseConnectionID      string `json:"enterprise_connection_id"`
			EnterpriseConnectionVersion int64  `json:"enterprise_connection_version"`
			EnterpriseConnectionHash    string `json:"enterprise_connection_config_hash"`
			EnterpriseBindingID         string `json:"enterprise_binding_id"`
			EnterpriseBindingVersion    int64  `json:"enterprise_binding_version"`
			EnterpriseModelRef          string `json:"enterprise_model_ref"`
			CredentialVersion           string `json:"credential_version"`
		} `json:"modelRoute"`
	}
	if err := json.Unmarshal([]byte(candidate.CandidateManifest), &manifest); err != nil {
		t.Fatal(err)
	}
	connectionVersion, err := svc.Store.GetEnterpriseConnectionVersion(ctx, pin.ConnectionID, pin.ConnectionVersion)
	if err != nil || connectionVersion == nil {
		t.Fatal(err)
	}
	if manifest.ModelRoute.ModelRouteVersion != ModelRoutePinReference(pin) ||
		manifest.ModelRoute.ModelRoutePinID != pin.PinID || manifest.ModelRoute.ModelRoutePinVersion != pin.Version ||
		manifest.ModelRoute.EnterpriseConnectionID != pin.ConnectionID || manifest.ModelRoute.EnterpriseConnectionVersion != 1 ||
		manifest.ModelRoute.EnterpriseConnectionHash != connectionVersion.ConnectionConfigHash ||
		manifest.ModelRoute.EnterpriseBindingID != pin.BindingID || manifest.ModelRoute.EnterpriseBindingVersion != 1 ||
		manifest.ModelRoute.EnterpriseModelRef != pin.ModelRef ||
		manifest.ModelRoute.CredentialVersion != connectionVersion.CredentialVersion {
		t.Fatalf("candidate manifest did not capture enterprise pin: %+v", manifest.ModelRoute)
	}
	snapshot, err := svc.CreateExecutionSnapshot(ctx, workspace.ID, admin.ID, ExecutionSnapshotInput{
		ReleaseCandidateID: candidate.CandidateID, CandidateVersion: 1,
		SnapshotSchemaVersion: "v1", ExecutionConfig: json.RawMessage(`{"mode":"read_only"}`),
		ModelRouteVersion: ModelRoutePinReference(pin), ModelRoutePinID: pin.PinID,
		ModelRoutePinVersion: pin.Version, EnterpriseConnectionID: pin.ConnectionID,
		EnterpriseConnectionVersion: pin.ConnectionVersion, EnterpriseBindingID: pin.BindingID,
		EnterpriseBindingVersion: pin.BindingVersion, EnterpriseModelRef: pin.ModelRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EnterpriseConnectionConfigHash != connectionVersion.ConnectionConfigHash ||
		snapshot.CredentialVersion != connectionVersion.CredentialVersion {
		t.Fatalf("snapshot did not capture connection credential identity: %+v", snapshot)
	}
}

func TestReleaseCandidateModelRoutePackageRequiresEnterprisePin(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetProviderURLPolicy(true)
	ctx := context.Background()
	workspace, err := svc.CreateTenant(ctx, "Route Candidate No Pin Workspace")
	if err != nil {
		t.Fatal(err)
	}
	provider := &model.ModelProvider{
		ID: "route-candidate-no-pin-provider", TenantID: workspace.ID, ProviderType: "openai-api-compatible",
		Name: "Route Candidate No Pin Provider", BaseURL: "https://internal.llm.example/v1",
		Enabled: true, Status: model.ProviderStatusActive,
	}
	if err := svc.Store.CreateModelProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	route, err := svc.CreateModelRoute(ctx, workspace.ID, CreateModelRouteRequest{
		ProviderID: provider.ID, Scenario: "chat", ModelAlias: "candidate-no-pin", TargetModel: "gpt-4o-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateReleaseCandidate(ctx, workspace.ID, "release-user", ReleaseCandidateInput{
		TargetType: "model_route_package", TargetID: route.ID, TargetVersion: "v1",
		CandidateID: id.New()[:12], CandidateVersion: 1, Manifest: json.RawMessage(manifestJSON()),
	})
	var businessErr *httperr.Error
	if !errors.As(err, &businessErr) || businessErr.Status != 400 || businessErr.Code != 40070 ||
		businessErr.Message != "model route package requires an active enterprise pin" {
		t.Fatalf("expected active enterprise pin rejection, got %v", err)
	}
}
