package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

var candidateManifestSections = []string{
	model.CandidateManifestTarget, model.CandidateManifestPrompt, model.CandidateManifestKnowledge,
	model.CandidateManifestModelRoute, model.CandidateManifestPolicy, model.CandidateManifestCatalog,
	model.CandidateManifestRouter, model.CandidateManifestTools, model.CandidateManifestRetrieval,
	model.CandidateManifestExecution,
}

var allowedCandidateTargets = map[string]bool{
	"assistant": true, "prompt_version": true, "template": true, "model_route_package": true,
}

type ReleaseCandidateInput struct {
	TargetType       string          `json:"target_type"`
	TargetID         string          `json:"target_id"`
	TargetVersion    string          `json:"target_version"`
	BaseVersion      string          `json:"base_version"`
	ChangeSummary    string          `json:"change_summary"`
	CandidateID      string          `json:"candidate_id"`
	CandidateVersion int64           `json:"candidate_version"`
	Manifest         json.RawMessage `json:"candidate_manifest"`
}

type ScenarioTemplateReleaseCandidateInput struct {
	BaseVersion   string `json:"base_version"`
	ChangeSummary string `json:"change_summary"`
}

type ScenarioTemplateReleaseCandidateResult struct {
	Candidate                *model.ReleaseCandidate     `json:"candidate"`
	EvalSet                  *model.EvalSet              `json:"eval_set,omitempty"`
	EvalSetCreated           bool                        `json:"eval_set_created"`
	EvalSetVersion           *model.EvaluationSetVersion `json:"eval_set_version,omitempty"`
	EvalSetVersionCreated    bool                        `json:"eval_set_version_created"`
	ExecutionSnapshot        *model.ExecutionSnapshot    `json:"execution_snapshot,omitempty"`
	ExecutionSnapshotCreated bool                        `json:"execution_snapshot_created"`
}

type ExecutionSnapshotInput struct {
	ReleaseCandidateID          string          `json:"release_candidate_id"`
	CandidateVersion            int64           `json:"candidate_version"`
	SnapshotSchemaVersion       string          `json:"snapshot_schema_version"`
	ExecutionConfig             json.RawMessage `json:"execution_config"`
	PromptVersion               string          `json:"prompt_version"`
	ModelRouteVersion           string          `json:"model_route_version"`
	ModelRoutePinID             string          `json:"model_route_pin_id"`
	ModelRoutePinVersion        int64           `json:"model_route_pin_version"`
	EnterpriseConnectionID      string          `json:"enterprise_connection_id"`
	EnterpriseConnectionVersion int64           `json:"enterprise_connection_version"`
	EnterpriseBindingID         string          `json:"enterprise_binding_id"`
	EnterpriseBindingVersion    int64           `json:"enterprise_binding_version"`
	EnterpriseModelRef          string          `json:"enterprise_model_ref"`
	KnowledgeVersion            string          `json:"knowledge_version"`
	RetrievalConfigVersion      string          `json:"retrieval_config_version"`
	CatalogVersion              string          `json:"catalog_version"`
	PolicyVersion               string          `json:"policy_version"`
	RouterVersion               string          `json:"router_version"`
	ToolRegistryVersion         string          `json:"tool_registry_version"`
	ToolSetHash                 string          `json:"tool_set_hash"`
}

type EvaluationRunInput struct {
	ReleaseCandidateID       string `json:"release_candidate_id"`
	CandidateVersion         int64  `json:"candidate_version"`
	EvalSetID                string `json:"eval_set_id"`
	EvalSetVersion           int64  `json:"eval_set_version"`
	EvalSetHash              string `json:"eval_set_hash"`
	EvaluationPolicyVersion  string `json:"evaluation_policy_version"`
	EvaluationPolicyHash     string `json:"evaluation_policy_hash"`
	AggregationPolicyVersion string `json:"aggregation_policy_version"`
	AggregationPolicyHash    string `json:"aggregation_policy_hash"`
	ExecutionSnapshotID      string `json:"execution_snapshot_id"`
}

type EvaluationSetVersionInput struct {
	EvalSetID     string          `json:"eval_set_id"`
	Version       int64           `json:"version"`
	ChangeSummary string          `json:"change_summary"`
	Cases         json.RawMessage `json:"cases"`
}

type EvaluationCaseVersionInput struct {
	EvalSetID              string          `json:"eval_set_id"`
	EvalSetVersion         int64           `json:"eval_set_version"`
	CaseID                 string          `json:"case_id"`
	CaseVersion            int64           `json:"case_version"`
	Question               string          `json:"question"`
	ExpectedAnswer         string          `json:"expected_answer"`
	ExpectedKeywords       string          `json:"expected_keywords"`
	ExpectedCitationDocIDs string          `json:"expected_citation_doc_ids"`
	Metadata               json.RawMessage `json:"metadata"`
}

type EvaluationCaseResultInput struct {
	RunID           string          `json:"run_id"`
	CaseID          string          `json:"case_id"`
	CaseVersionID   string          `json:"case_version_id"`
	CaseVersionHash string          `json:"case_version_hash"`
	ActualAnswer    string          `json:"actual_answer"`
	References      json.RawMessage `json:"references"`
	Metrics         json.RawMessage `json:"metrics"`
	Pass            *bool           `json:"pass"`
	FailureReason   string          `json:"failure_reason"`
	LatencyMs       int64           `json:"latency_ms"`
	Tokens          int64           `json:"tokens"`
}

type EvidenceBundleInput struct {
	ReleaseCandidateID    string          `json:"release_candidate_id"`
	CandidateVersion      int64           `json:"candidate_version"`
	EvaluationRunID       string          `json:"evaluation_run_id"`
	SecurityEvidence      json.RawMessage `json:"security_evidence"`
	PolicyEvidence        json.RawMessage `json:"policy_evidence"`
	RiskEvidence          json.RawMessage `json:"risk_evidence"`
	PermissionEvidence    json.RawMessage `json:"permission_evidence"`
	ConfigurationEvidence json.RawMessage `json:"configuration_evidence"`
	ApprovalEvidence      json.RawMessage `json:"approval_evidence"`
	EvidenceItems         json.RawMessage `json:"evidence_items"`
}

type GateDecisionInput struct {
	ReleaseCandidateID       string          `json:"release_candidate_id"`
	CandidateVersion         int64           `json:"candidate_version"`
	EvidenceBundleID         string          `json:"evidence_bundle_id"`
	Environment              string          `json:"environment"`
	EnvironmentPolicyVersion string          `json:"environment_policy_version"`
	EnvironmentPolicyHash    string          `json:"environment_policy_hash"`
	SubGateStates            json.RawMessage `json:"sub_gate_states"`
	Reason                   string          `json:"reason"`
	Waiver                   json.RawMessage `json:"waiver"`
	ApprovalID               string          `json:"approval_id"`
	Activate                 bool            `json:"activate"`
}

type ReleaseInput struct {
	ReleaseCandidateID string `json:"release_candidate_id"`
	CandidateVersion   int64  `json:"candidate_version"`
	SnapshotID         string `json:"snapshot_id"`
	GateDecisionID     string `json:"gate_decision_id"`
	Environment        string `json:"environment"`
	RollbackBaseline   string `json:"rollback_baseline"`
}

type QualityIssueInput struct {
	Source                  string          `json:"source"`
	SourceID                string          `json:"source_id"`
	Title                   string          `json:"title"`
	Evidence                json.RawMessage `json:"evidence"`
	Owner                   string          `json:"owner"`
	ResolutionTargetType    string          `json:"resolution_target_type"`
	ResolutionTargetID      string          `json:"resolution_target_id"`
	ResolutionTargetVersion string          `json:"resolution_target_version"`
	EvalCaseID              string          `json:"eval_case_id"`
}

type QualityIssueStateInput struct {
	Status                  string `json:"status"`
	Owner                   string `json:"owner"`
	Resolution              string `json:"resolution"`
	ResolutionTargetType    string `json:"resolution_target_type"`
	ResolutionTargetID      string `json:"resolution_target_id"`
	ResolutionTargetVersion string `json:"resolution_target_version"`
	EvalCaseID              string `json:"eval_case_id"`
	EvaluationRunID         string `json:"evaluation_run_id"`
	ReleaseID               string `json:"release_id"`
	ReopenReason            string `json:"reopen_reason"`
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func canonicalHash(raw json.RawMessage) (string, error) {
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", httperr.BadRequest(40070, "payload must be JSON")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", httperr.BadRequest(40070, "payload must be canonicalizable JSON")
	}
	return sha256Hex(string(canonical)), nil
}

func requiredString(value, field string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", httperr.BadRequest(40070, field+" is required")
	}
	return value, nil
}

func (s *Service) CreateReleaseCandidate(ctx context.Context, tenantID, userID string, input ReleaseCandidateInput) (*model.ReleaseCandidate, error) {
	if !allowedCandidateTargets[input.TargetType] {
		return nil, httperr.BadRequest(40070, "unsupported release candidate target")
	}
	if _, err := requiredString(input.TargetID, "target_id"); err != nil {
		return nil, err
	}
	if _, err := requiredString(input.TargetVersion, "target_version"); err != nil {
		return nil, err
	}
	if _, err := requiredString(input.CandidateID, "candidate_id"); err != nil {
		return nil, err
	}
	if input.CandidateVersion <= 0 {
		input.CandidateVersion = 1
	}
	if len(input.Manifest) == 0 {
		return nil, httperr.BadRequest(40070, "candidate_manifest is required")
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(input.Manifest, &manifest); err != nil {
		return nil, httperr.BadRequest(40070, "candidate_manifest must be an object")
	}
	if len(manifest) != len(candidateManifestSections) {
		return nil, httperr.BadRequest(40070, "candidate_manifest must contain exactly the 10 frozen sections")
	}
	for _, section := range candidateManifestSections {
		var item struct {
			Version json.RawMessage `json:"version"`
			Hash    string          `json:"hash"`
		}
		if err := json.Unmarshal(manifest[section], &item); err != nil || len(item.Version) == 0 || item.Hash == "" {
			return nil, httperr.BadRequest(40070, "candidate manifest section requires version and hash: "+section)
		}
	}
	manifest, pinErr := s.pinCandidateModelRoute(ctx, tenantID, input, manifest)
	if pinErr != nil {
		return nil, pinErr
	}
	manifestBytes, marshalErr := json.Marshal(manifest)
	if marshalErr != nil {
		return nil, httperr.BadRequest(40070, "candidate manifest cannot be serialized")
	}
	input.Manifest = json.RawMessage(manifestBytes)
	hash, err := canonicalHash(input.Manifest)
	if err != nil {
		return nil, err
	}
	candidate := &model.ReleaseCandidate{
		ID: id.New(), TenantID: tenantID, CandidateID: input.CandidateID,
		CandidateVersion: input.CandidateVersion, TargetType: input.TargetType,
		TargetID: input.TargetID, TargetVersion: input.TargetVersion,
		BaseVersion: input.BaseVersion, ChangeSummary: input.ChangeSummary,
		CandidateManifest: string(input.Manifest), CandidateHash: hash,
		HashAlgorithm: model.HashAlgorithmSHA256, Status: model.CandidateDraft,
		CreatedBy: userID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateReleaseCandidate(ctx, candidate); err != nil {
		return nil, httperr.New(409, 40983, "release candidate version already exists")
	}
	return candidate, nil
}

// CreateScenarioTemplateReleaseCandidate converts an immutable scenario
// template version into a governed candidate without hand-built manifests.
func (s *Service) CreateScenarioTemplateReleaseCandidate(
	ctx context.Context, tenantID, userID, templateID string,
	input ScenarioTemplateReleaseCandidateInput,
) (*ScenarioTemplateReleaseCandidateResult, error) {
	asset, selected, _, err := s.GetScenarioTemplate(ctx, tenantID, templateID, 0, false)
	if err != nil {
		return nil, err
	}
	if asset.Status == model.AssetStatusArchived {
		return nil, httperr.New(409, 40983, "archived scenario template cannot create a release candidate")
	}
	payloadHash, err := canonicalHash(json.RawMessage(selected.PayloadJSON))
	if err != nil {
		return nil, err
	}
	templatePayload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		return nil, err
	}
	version := fmt.Sprintf("v%d", selected.Version)
	appTypes := []string{}
	if strings.TrimSpace(asset.AppTypes) != "" {
		appTypes = strings.Split(asset.AppTypes, ",")
	}
	manifest := map[string]any{}
	section := func(name string, value any) error {
		payload := map[string]any{
			"schema":                scenarioTemplateSchema,
			"template_payload_hash": payloadHash,
			"value":                 value,
		}
		raw, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return httperr.BadRequest(40070, "candidate manifest section cannot be serialized")
		}
		hash, hashErr := canonicalHash(raw)
		if hashErr != nil {
			return hashErr
		}
		manifest[name] = map[string]any{
			"version": version, "hash": hash, "source": "scenario-template", "payload": payload,
		}
		return nil
	}
	sections := []struct {
		name  string
		value any
	}{
		{model.CandidateManifestTarget, map[string]any{
			"template_id": asset.ID, "template_key": asset.Key, "name": asset.Name,
			"app_types": appTypes, "description": asset.Description,
		}},
		{model.CandidateManifestPrompt, map[string]any{
			"prompt_preset_id":     templatePayload.PromptPresetID,
			"parameter_profile_id": templatePayload.ParameterProfileID,
		}},
		{model.CandidateManifestKnowledge, map[string]any{"dataset_suggestions": templatePayload.DatasetSuggestions}},
		{model.CandidateManifestModelRoute, map[string]any{"template_payload_hash": payloadHash}},
		{model.CandidateManifestPolicy, map[string]any{"payload_hash": payloadHash}},
		{model.CandidateManifestCatalog, map[string]any{"template_id": asset.ID, "name": asset.Name}},
		{model.CandidateManifestRouter, map[string]any{"app_types": appTypes}},
		{model.CandidateManifestTools, map[string]any{"read_only": true}},
		{model.CandidateManifestRetrieval, map[string]any{"template_payload_hash": payloadHash}},
		{model.CandidateManifestExecution, map[string]any{"mode": "read_only"}},
	}
	for _, item := range sections {
		if err := section(item.name, item.value); err != nil {
			return nil, err
		}
	}
	manifestJSON, marshalErr := json.Marshal(manifest)
	if marshalErr != nil {
		return nil, httperr.BadRequest(40070, "candidate manifest cannot be serialized")
	}
	if input.BaseVersion == "" && selected.Version > 1 {
		input.BaseVersion = fmt.Sprintf("v%d", selected.Version-1)
	}
	if input.ChangeSummary == "" {
		input.ChangeSummary = selected.ChangeNote
	}
	evalSet, evalSetVersion, evalSetCreated, evalSetVersionCreated, err := s.PublishScenarioTemplateEvalSetVersion(
		ctx, tenantID, userID, templateID, asset.Name+"评测集", "release candidate "+version,
	)
	if err != nil {
		return nil, err
	}
	candidate, err := s.CreateReleaseCandidate(ctx, tenantID, userID, ReleaseCandidateInput{
		TargetType: "template", TargetID: asset.ID, TargetVersion: version,
		CandidateID: asset.ID, CandidateVersion: selected.Version,
		ChangeSummary: input.ChangeSummary, Manifest: manifestJSON,
	})
	if err != nil {
		return nil, err
	}
	executionConfig, err := json.Marshal(map[string]any{
		"mode":                  "read_only",
		"schema":                scenarioTemplateSchema,
		"candidate_hash":        candidate.CandidateHash,
		"template_payload_hash": payloadHash,
		"eval_set_id":           evalSet.ID,
		"eval_set_version":      evalSetVersion.Version,
		"eval_set_hash":         evalSetVersion.Hash,
	})
	if err != nil {
		return nil, httperr.BadRequest(40071, "execution snapshot config cannot be serialized")
	}
	snapshot, err := s.CreateExecutionSnapshot(ctx, tenantID, userID, ExecutionSnapshotInput{
		ReleaseCandidateID:     candidate.CandidateID,
		CandidateVersion:       candidate.CandidateVersion,
		SnapshotSchemaVersion:  "v1",
		ExecutionConfig:        executionConfig,
		PromptVersion:          manifestHash(manifest, model.CandidateManifestPrompt),
		ModelRouteVersion:      manifestHash(manifest, model.CandidateManifestModelRoute),
		KnowledgeVersion:       manifestHash(manifest, model.CandidateManifestKnowledge),
		RetrievalConfigVersion: manifestHash(manifest, model.CandidateManifestRetrieval),
		CatalogVersion:         manifestHash(manifest, model.CandidateManifestCatalog),
		PolicyVersion:          manifestHash(manifest, model.CandidateManifestPolicy),
		RouterVersion:          manifestHash(manifest, model.CandidateManifestRouter),
		ToolRegistryVersion:    manifestHash(manifest, model.CandidateManifestTools),
		ToolSetHash:            manifestHash(manifest, model.CandidateManifestTools),
	})
	if err != nil {
		return nil, err
	}
	if err := s.MarkReleaseCandidateReady(ctx, tenantID, candidate.CandidateID); err != nil {
		return nil, err
	}
	candidate.Status = model.CandidateReadyForEvaluation
	return &ScenarioTemplateReleaseCandidateResult{
		Candidate: candidate, EvalSet: evalSet, EvalSetCreated: evalSetCreated,
		EvalSetVersion: evalSetVersion, EvalSetVersionCreated: evalSetVersionCreated,
		ExecutionSnapshot: snapshot, ExecutionSnapshotCreated: true,
	}, nil
}

func manifestHash(manifest map[string]any, name string) string {
	section, ok := manifest[name].(map[string]any)
	if !ok {
		return ""
	}
	hash, _ := section["hash"].(string)
	return hash
}

func (s *Service) pinCandidateModelRoute(
	ctx context.Context, tenantID string, input ReleaseCandidateInput,
	manifest map[string]json.RawMessage,
) (map[string]json.RawMessage, error) {
	if input.TargetType != "model_route_package" {
		return manifest, nil
	}
	route, err := s.Store.GetModelRoute(ctx, tenantID, input.TargetID)
	if err != nil {
		return nil, err
	}
	if route == nil {
		return nil, httperr.NotFound("pinned model route not found")
	}
	pin, err := s.ValidateEnterpriseModelRoutePin(ctx, route)
	if err != nil {
		return nil, err
	}
	if pin == nil {
		return nil, httperr.BadRequest(40070, "model route package requires an active enterprise pin")
	}
	connectionVersion, err := s.Store.GetEnterpriseConnectionVersion(ctx, pin.ConnectionID, pin.ConnectionVersion)
	if err != nil {
		return nil, err
	}
	if connectionVersion == nil {
		return nil, httperr.BadRequest(40070, "pinned enterprise connection version not found")
	}
	bindingVersion, err := s.Store.GetEnterpriseBindingVersion(ctx, pin.BindingID, pin.BindingVersion)
	if err != nil {
		return nil, err
	}
	if bindingVersion == nil || bindingVersion.ConnectionID != pin.ConnectionID {
		return nil, httperr.BadRequest(40070, "pinned enterprise binding version not found")
	}

	var modelRoute map[string]any
	if err := json.Unmarshal(manifest[model.CandidateManifestModelRoute], &modelRoute); err != nil {
		return nil, httperr.BadRequest(40070, "candidate manifest modelRoute must be an object")
	}
	modelRoute["model_route_version"] = ModelRoutePinReference(pin)
	modelRoute["model_route_pin_id"] = pin.PinID
	modelRoute["model_route_pin_version"] = pin.Version
	modelRoute["enterprise_connection_id"] = pin.ConnectionID
	modelRoute["enterprise_connection_version"] = pin.ConnectionVersion
	modelRoute["enterprise_connection_config_hash"] = connectionVersion.ConnectionConfigHash
	modelRoute["enterprise_binding_id"] = pin.BindingID
	modelRoute["enterprise_binding_version"] = pin.BindingVersion
	modelRoute["enterprise_model_ref"] = pin.ModelRef
	modelRoute["credential_version"] = connectionVersion.CredentialVersion
	pinnedRoute, err := json.Marshal(modelRoute)
	if err != nil {
		return nil, httperr.BadRequest(40070, "candidate manifest modelRoute cannot be serialized")
	}
	manifest[model.CandidateManifestModelRoute] = pinnedRoute
	return manifest, nil
}

func (s *Service) MarkReleaseCandidateReady(ctx context.Context, tenantID, candidateID string) error {
	return s.Store.TransitionReleaseCandidate(ctx, tenantID, candidateID, model.CandidateDraft, model.CandidateReadyForEvaluation)
}

func (s *Service) ListReleaseCandidates(ctx context.Context, tenantID string, page, pageSize int) ([]model.ReleaseCandidate, int64, error) {
	return s.Store.ListReleaseCandidates(ctx, tenantID, page, pageSize)
}

func (s *Service) ListExecutionSnapshots(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.ExecutionSnapshot, int64, error) {
	return s.Store.ListExecutionSnapshots(ctx, tenantID, candidateID, page, pageSize)
}

func (s *Service) ListEvaluationRuns(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.EvaluationRun, int64, error) {
	return s.Store.ListEvaluationRuns(ctx, tenantID, candidateID, page, pageSize)
}

func (s *Service) ListEvidenceBundles(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.EvidenceBundle, int64, error) {
	return s.Store.ListEvidenceBundles(ctx, tenantID, candidateID, page, pageSize)
}

func (s *Service) ListGateDecisions(ctx context.Context, tenantID, candidateID string, candidateVersion int64, page, pageSize int) ([]model.ReleaseGateDecision, int64, error) {
	return s.Store.ListGateDecisions(ctx, tenantID, candidateID, candidateVersion, page, pageSize)
}

func (s *Service) ListReleases(ctx context.Context, tenantID, candidateID string, page, pageSize int) ([]model.Release, int64, error) {
	return s.Store.ListReleases(ctx, tenantID, candidateID, page, pageSize)
}

func (s *Service) ListQualityIssues(ctx context.Context, tenantID, status string, page, pageSize int) ([]model.QualityIssue, int64, error) {
	return s.Store.ListQualityIssues(ctx, tenantID, status, page, pageSize)
}

func (s *Service) getReleaseCandidateByIdentity(
	ctx context.Context, tenantID, candidateID string, candidateVersion int64,
) (*model.ReleaseCandidate, error) {
	candidate, err := s.Store.GetReleaseCandidateVersion(ctx, tenantID, candidateID, candidateVersion)
	if err != nil || candidate != nil {
		return candidate, err
	}
	return s.Store.GetReleaseCandidate(ctx, tenantID, candidateID)
}

func (s *Service) CreateExecutionSnapshot(ctx context.Context, tenantID, userID string, input ExecutionSnapshotInput) (*model.ExecutionSnapshot, error) {
	candidate, err := s.getReleaseCandidateByIdentity(ctx, tenantID, input.ReleaseCandidateID, input.CandidateVersion)
	if err != nil || candidate == nil {
		return nil, httperr.NotFound("release candidate not found")
	}
	if candidate.CandidateVersion != input.CandidateVersion || candidate.CandidateHash == "" {
		return nil, httperr.BadRequest(40071, "candidate version binding is invalid")
	}
	if len(input.ExecutionConfig) == 0 || input.SnapshotSchemaVersion == "" {
		return nil, httperr.BadRequest(40071, "execution snapshot config and schema version are required")
	}
	var connectionVersion *model.EnterpriseConnectionVersion
	var connectionConfigHash, credentialVersion string
	pinRequested := input.ModelRoutePinID != "" || input.EnterpriseConnectionID != "" ||
		input.ModelRoutePinVersion > 0 || input.EnterpriseBindingID != "" || input.EnterpriseModelRef != "" ||
		input.EnterpriseConnectionVersion > 0 || input.EnterpriseBindingVersion > 0
	if pinRequested {
		if input.ModelRoutePinID == "" || input.EnterpriseConnectionID == "" || input.EnterpriseBindingID == "" ||
			input.ModelRoutePinVersion <= 0 || input.EnterpriseModelRef == "" ||
			input.EnterpriseConnectionVersion <= 0 || input.EnterpriseBindingVersion <= 0 {
			return nil, httperr.BadRequest(40071, "enterprise model route pin identity is incomplete")
		}
		pin, err := s.Store.GetModelRouteEnterprisePinByIdentity(ctx, tenantID, input.ModelRoutePinID, input.ModelRoutePinVersion)
		if err != nil {
			return nil, err
		}
		if pin == nil {
			return nil, httperr.BadRequest(40071, "enterprise model route pin not found")
		}
		route, err := s.Store.GetModelRoute(ctx, tenantID, pin.RouteID)
		if err != nil {
			return nil, err
		}
		if route == nil {
			return nil, httperr.BadRequest(40071, "pinned model route not found")
		}
		validPin, err := s.ValidateEnterpriseModelRoutePin(ctx, route)
		if err != nil {
			return nil, err
		}
		if validPin == nil || validPin.PinID != input.ModelRoutePinID || validPin.Version != pin.Version ||
			validPin.ConnectionID != input.EnterpriseConnectionID || validPin.ConnectionVersion != input.EnterpriseConnectionVersion ||
			validPin.BindingID != input.EnterpriseBindingID || validPin.BindingVersion != input.EnterpriseBindingVersion ||
			validPin.ModelRef != input.EnterpriseModelRef {
			return nil, httperr.BadRequest(40071, "enterprise model route pin identity mismatch")
		}
		connectionVersion, err = s.Store.GetEnterpriseConnectionVersion(ctx, pin.ConnectionID, pin.ConnectionVersion)
		if err != nil {
			return nil, err
		}
		if connectionVersion == nil {
			return nil, httperr.BadRequest(40071, "enterprise connection version is unavailable")
		}
		connectionConfigHash = connectionVersion.ConnectionConfigHash
		credentialVersion = connectionVersion.CredentialVersion
		reference := ModelRoutePinReference(validPin)
		if input.ModelRouteVersion != "" && input.ModelRouteVersion != reference {
			return nil, httperr.BadRequest(40071, "model route version does not match enterprise pin")
		}
		input.ModelRouteVersion = reference
	}
	hash, err := canonicalHash(input.ExecutionConfig)
	if err != nil {
		return nil, err
	}
	snapshot := &model.ExecutionSnapshot{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: candidate.CandidateID,
		CandidateVersion: input.CandidateVersion, TargetType: candidate.TargetType,
		TargetID: candidate.TargetID, TargetVersion: candidate.TargetVersion,
		SnapshotSchemaVersion: input.SnapshotSchemaVersion,
		SnapshotHashAlgorithm: model.HashAlgorithmSHA256, SnapshotHash: hash,
		ExecutionConfig: string(input.ExecutionConfig), PromptVersion: input.PromptVersion,
		ModelRouteVersion: input.ModelRouteVersion, KnowledgeVersion: input.KnowledgeVersion,
		ModelRoutePinID: input.ModelRoutePinID, ModelRoutePinVersion: input.ModelRoutePinVersion,
		EnterpriseConnectionID:         input.EnterpriseConnectionID,
		EnterpriseConnectionVersion:    input.EnterpriseConnectionVersion,
		EnterpriseConnectionConfigHash: connectionConfigHash,
		EnterpriseBindingID:            input.EnterpriseBindingID, EnterpriseBindingVersion: input.EnterpriseBindingVersion,
		EnterpriseModelRef:     input.EnterpriseModelRef,
		CredentialVersion:      credentialVersion,
		RetrievalConfigVersion: input.RetrievalConfigVersion, CatalogVersion: input.CatalogVersion,
		PolicyVersion: input.PolicyVersion, RouterVersion: input.RouterVersion,
		ToolRegistryVersion: input.ToolRegistryVersion, ToolSetHash: input.ToolSetHash,
		CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateExecutionSnapshot(ctx, snapshot); err != nil {
		return nil, httperr.New(409, 40984, "execution snapshot already exists")
	}
	return snapshot, nil
}

func (s *Service) CreateEvaluationRun(ctx context.Context, tenantID, userID string, input EvaluationRunInput) (*model.EvaluationRun, error) {
	candidate, err := s.getReleaseCandidateByIdentity(ctx, tenantID, input.ReleaseCandidateID, input.CandidateVersion)
	if err != nil || candidate == nil {
		return nil, httperr.NotFound("release candidate not found")
	}
	snapshot, err := s.Store.GetExecutionSnapshot(ctx, tenantID, input.ExecutionSnapshotID)
	if err != nil || snapshot == nil {
		return nil, httperr.NotFound("execution snapshot not found")
	}
	if snapshot.ReleaseCandidateID != candidate.CandidateID || snapshot.CandidateVersion != input.CandidateVersion {
		return nil, httperr.BadRequest(40072, "evaluation candidate binding mismatch")
	}
	evalSetVersion, err := s.Store.GetEvaluationSetVersion(ctx, tenantID, input.EvalSetID, input.EvalSetVersion)
	if err != nil || evalSetVersion == nil {
		return nil, httperr.NotFound("evaluation set version not found")
	}
	if evalSetVersion.Hash != input.EvalSetHash || evalSetVersion.CasesSnapshotHash == "" {
		return nil, httperr.BadRequest(40072, "evaluation set hash mismatch")
	}
	for _, value := range []string{input.EvaluationPolicyVersion, input.EvaluationPolicyHash, input.AggregationPolicyVersion, input.AggregationPolicyHash} {
		if _, err := requiredString(value, "evaluation policy identity"); err != nil {
			return nil, err
		}
	}
	run := &model.EvaluationRun{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: candidate.CandidateID,
		CandidateVersion: input.CandidateVersion, EvalSetID: input.EvalSetID,
		EvalSetVersion: input.EvalSetVersion, EvalSetHash: input.EvalSetHash,
		EvaluationPolicyVersion: input.EvaluationPolicyVersion, EvaluationPolicyHash: input.EvaluationPolicyHash,
		AggregationPolicyVersion: input.AggregationPolicyVersion, AggregationPolicyHash: input.AggregationPolicyHash,
		ExecutionSnapshotID: snapshot.ID, Status: model.EvaluationRunQueued,
		Actor: userID, CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateEvaluationRun(ctx, run); err != nil {
		return nil, err
	}
	if err := s.Store.StartEvaluationRun(ctx, run.ID); err != nil {
		return nil, err
	}
	_ = s.Store.TransitionReleaseCandidate(ctx, tenantID, candidate.CandidateID, candidate.Status, model.CandidateEvaluating)
	return run, nil
}

func (s *Service) CreateEvaluationSetVersion(ctx context.Context, tenantID, userID string, input EvaluationSetVersionInput) (*model.EvaluationSetVersion, error) {
	if _, err := requiredString(input.EvalSetID, "eval_set_id"); err != nil {
		return nil, err
	}
	if input.Version <= 0 || len(input.Cases) == 0 {
		return nil, httperr.BadRequest(40072, "evaluation set version and cases are required")
	}
	var cases []map[string]interface{}
	if err := json.Unmarshal(input.Cases, &cases); err != nil || len(cases) == 0 {
		return nil, httperr.BadRequest(40072, "cases must be a non-empty array")
	}
	casesHash, err := canonicalHash(input.Cases)
	if err != nil {
		return nil, err
	}
	version := &model.EvaluationSetVersion{
		ID: id.New(), TenantID: tenantID, EvalSetID: input.EvalSetID, Version: input.Version,
		Hash: casesHash, CasesSnapshotHash: casesHash, Status: model.EvalStatusPublished,
		ChangeSummary: input.ChangeSummary, CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateEvaluationSetVersion(ctx, version); err != nil {
		return nil, httperr.New(409, 40984, "evaluation set version already exists")
	}
	return version, nil
}

func (s *Service) CreateEvaluationCaseVersion(ctx context.Context, tenantID, userID string, input EvaluationCaseVersionInput) (*model.EvaluationCaseVersion, error) {
	evalSet, err := s.Store.GetEvaluationSetVersion(ctx, tenantID, input.EvalSetID, input.EvalSetVersion)
	if err != nil || evalSet == nil {
		return nil, httperr.NotFound("evaluation set version not found")
	}
	if _, err := requiredString(input.CaseID, "case_id"); err != nil {
		return nil, err
	}
	if input.CaseVersion <= 0 || strings.TrimSpace(input.Question) == "" {
		return nil, httperr.BadRequest(40072, "case version and question are required")
	}
	metadata, err := validPayloadJSON(input.Metadata, 40072, "case metadata must be JSON")
	if err != nil {
		return nil, err
	}
	identity, err := json.Marshal(map[string]interface{}{
		"question": input.Question, "expected_answer": input.ExpectedAnswer,
		"expected_keywords": input.ExpectedKeywords, "expected_citation_doc_ids": input.ExpectedCitationDocIDs,
		"metadata": json.RawMessage(metadata),
	})
	if err != nil {
		return nil, err
	}
	version := &model.EvaluationCaseVersion{
		ID: id.New(), TenantID: tenantID, EvalSetID: input.EvalSetID,
		EvalSetVersion: input.EvalSetVersion, CaseID: input.CaseID, CaseVersion: input.CaseVersion,
		Hash: sha256Hex(string(identity)), Question: input.Question, ExpectedAnswer: input.ExpectedAnswer,
		ExpectedKeywords: input.ExpectedKeywords, ExpectedCitationDocIDs: input.ExpectedCitationDocIDs,
		Metadata: metadata, CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateEvaluationCaseVersions(ctx, []model.EvaluationCaseVersion{*version}); err != nil {
		return nil, httperr.New(409, 40984, "evaluation case version already exists")
	}
	return version, nil
}

func (s *Service) AddEvaluationCaseResult(ctx context.Context, tenantID, userID string, input EvaluationCaseResultInput) (*model.EvaluationCaseResult, error) {
	run, err := s.Store.GetEvaluationRun(ctx, tenantID, input.RunID)
	if err != nil || run == nil {
		return nil, httperr.NotFound("evaluation run not found")
	}
	if run.Status != model.EvaluationRunQueued && run.Status != model.EvaluationRunRunning {
		return nil, httperr.New(409, 40989, "evaluation case results are immutable after terminal run")
	}
	caseVersion, err := s.Store.GetEvaluationCaseVersion(ctx, tenantID, input.CaseVersionID)
	if err != nil || caseVersion == nil {
		return nil, httperr.NotFound("evaluation case version not found")
	}
	if input.CaseVersionID == "" || input.CaseVersionHash == "" {
		return nil, httperr.BadRequest(40073, "case version identity is required")
	}
	references, err := validPayloadJSON(input.References, 40073, "references must be JSON")
	if err != nil {
		return nil, err
	}
	metrics, err := validPayloadJSON(input.Metrics, 40073, "metrics must be JSON")
	if err != nil {
		return nil, err
	}
	result := &model.EvaluationCaseResult{
		ID: id.New(), TenantID: tenantID, RunID: run.ID, CaseID: input.CaseID,
		CaseVersionID: input.CaseVersionID, CaseVersionHash: input.CaseVersionHash,
		ActualAnswer: input.ActualAnswer, References: references, Metrics: metrics,
		Pass: input.Pass, FailureReason: input.FailureReason, LatencyMs: input.LatencyMs,
		Tokens: input.Tokens, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateEvaluationCaseResult(ctx, result); err != nil {
		return nil, httperr.New(409, 40985, "case result already exists")
	}
	return result, nil
}

func (s *Service) CompleteEvaluationRun(ctx context.Context, tenantID, runID string, failed bool, failureReason string) (*model.EvaluationRun, error) {
	run, err := s.Store.GetEvaluationRun(ctx, tenantID, runID)
	if err != nil || run == nil {
		return nil, httperr.NotFound("evaluation run not found")
	}
	if failed {
		if err := s.Store.CompleteEvaluationRun(ctx, run.ID, model.EvaluationRunFailed, "", nil); err != nil {
			return nil, err
		}
		return s.Store.GetEvaluationRun(ctx, tenantID, runID)
	}
	results, err := s.Store.ListEvaluationCaseResults(ctx, tenantID, run.ID)
	if err != nil || len(results) == 0 {
		return nil, httperr.BadRequest(40073, "evaluation run has no case results")
	}
	passed, total := 0, len(results)
	for _, result := range results {
		if result.Pass != nil && *result.Pass {
			passed++
		}
	}
	rate := float64(passed) / float64(total)
	metrics, _ := json.Marshal(map[string]interface{}{
		"case_count": total, "passed_case_count": passed, "pass_rate": rate,
	})
	aggregated := rate == 1
	status := model.EvaluationRunCompleted
	var pass *bool
	if aggregated {
		pass = boolPtr(true)
	} else {
		pass = boolPtr(false)
	}
	if err := s.Store.CompleteEvaluationRun(ctx, run.ID, status, string(metrics), pass); err != nil {
		return nil, err
	}
	_ = s.Store.TransitionReleaseCandidate(ctx, tenantID, run.ReleaseCandidateID, model.CandidateEvaluating, model.CandidateEvaluated)
	return s.Store.GetEvaluationRun(ctx, tenantID, runID)
}

func (s *Service) CreateEvidenceBundle(ctx context.Context, tenantID, userID string, input EvidenceBundleInput) (*model.EvidenceBundle, error) {
	candidate, err := s.getReleaseCandidateByIdentity(ctx, tenantID, input.ReleaseCandidateID, input.CandidateVersion)
	if err != nil || candidate == nil {
		return nil, httperr.NotFound("release candidate not found")
	}
	run, err := s.Store.GetEvaluationRun(ctx, tenantID, input.EvaluationRunID)
	if err != nil || run == nil {
		return nil, httperr.NotFound("evaluation run not found")
	}
	snapshot, err := s.Store.GetExecutionSnapshot(ctx, tenantID, run.ExecutionSnapshotID)
	if err != nil || snapshot == nil {
		return nil, httperr.NotFound("execution snapshot not found")
	}
	if run.ReleaseCandidateID != candidate.CandidateID || run.CandidateVersion != input.CandidateVersion ||
		snapshot.ReleaseCandidateID != candidate.CandidateID || snapshot.CandidateVersion != input.CandidateVersion ||
		run.Status != model.EvaluationRunCompleted || run.Pass == nil {
		return nil, httperr.BadRequest(40074, "evidence chain binding mismatch")
	}
	evidence := map[string]json.RawMessage{
		"security_evidence": input.SecurityEvidence, "policy_evidence": input.PolicyEvidence,
		"risk_evidence": input.RiskEvidence, "permission_evidence": input.PermissionEvidence,
		"configuration_evidence": input.ConfigurationEvidence, "approval_evidence": input.ApprovalEvidence,
		"evidence_items": input.EvidenceItems,
	}
	for name, value := range evidence {
		if len(value) == 0 {
			return nil, httperr.BadRequest(40074, "evidence is required: "+name)
		}
	}
	canonical, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	bundleHash := sha256Hex(string(canonical))
	bundle := &model.EvidenceBundle{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: candidate.CandidateID,
		CandidateVersion: input.CandidateVersion, SnapshotID: snapshot.ID, SnapshotHash: snapshot.SnapshotHash,
		ModelRouteVersion: snapshot.ModelRouteVersion, ModelRoutePinID: snapshot.ModelRoutePinID,
		ModelRoutePinVersion:           snapshot.ModelRoutePinVersion,
		EnterpriseConnectionID:         snapshot.EnterpriseConnectionID,
		EnterpriseConnectionVersion:    snapshot.EnterpriseConnectionVersion,
		EnterpriseConnectionConfigHash: snapshot.EnterpriseConnectionConfigHash,
		EnterpriseBindingID:            snapshot.EnterpriseBindingID,
		EnterpriseBindingVersion:       snapshot.EnterpriseBindingVersion,
		EnterpriseModelRef:             snapshot.EnterpriseModelRef,
		CredentialVersion:              snapshot.CredentialVersion,
		EvaluationRunID:                run.ID, EvalSetID: run.EvalSetID, EvalSetVersion: run.EvalSetVersion,
		EvalSetHash: run.EvalSetHash, EvaluationPolicyVersion: run.EvaluationPolicyVersion,
		EvaluationPolicyHash: run.EvaluationPolicyHash, AggregationPolicyVersion: run.AggregationPolicyVersion,
		AggregationPolicyHash: run.AggregationPolicyHash, SecurityEvidence: string(input.SecurityEvidence),
		PolicyEvidence: string(input.PolicyEvidence), RiskEvidence: string(input.RiskEvidence),
		PermissionEvidence: string(input.PermissionEvidence), ConfigurationEvidence: string(input.ConfigurationEvidence),
		ApprovalEvidence: string(input.ApprovalEvidence), EvidenceItems: string(input.EvidenceItems),
		HashAlgorithm: model.HashAlgorithmSHA256, BundleHash: bundleHash,
		CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateEvidenceBundle(ctx, bundle); err != nil {
		return nil, httperr.New(409, 40986, "evidence bundle already exists")
	}
	return bundle, nil
}

func (s *Service) EvaluateReleaseGate(ctx context.Context, tenantID, userID string, input GateDecisionInput) (*model.ReleaseGateDecision, error) {
	candidate, err := s.getReleaseCandidateByIdentity(ctx, tenantID, input.ReleaseCandidateID, input.CandidateVersion)
	if err != nil || candidate == nil {
		return nil, httperr.NotFound("release candidate not found")
	}
	bundle, err := s.Store.GetEvidenceBundle(ctx, tenantID, input.EvidenceBundleID)
	if err != nil || bundle == nil {
		return nil, httperr.NotFound("evidence bundle not found")
	}
	if bundle.ReleaseCandidateID != candidate.CandidateID || bundle.CandidateVersion != input.CandidateVersion {
		return nil, httperr.BadRequest(40075, "gate candidate binding mismatch")
	}
	snapshot, err := s.Store.GetExecutionSnapshot(ctx, tenantID, bundle.SnapshotID)
	if err != nil || snapshot == nil {
		return nil, httperr.NotFound("execution snapshot not found")
	}
	run, err := s.Store.GetEvaluationRun(ctx, tenantID, bundle.EvaluationRunID)
	if err != nil || run == nil {
		return nil, httperr.NotFound("evaluation run not found")
	}
	if bundle.SnapshotHash != snapshot.SnapshotHash ||
		snapshot.ReleaseCandidateID != candidate.CandidateID ||
		snapshot.CandidateVersion != input.CandidateVersion ||
		run.ReleaseCandidateID != candidate.CandidateID ||
		run.CandidateVersion != input.CandidateVersion ||
		run.EvalSetID != bundle.EvalSetID ||
		run.EvalSetVersion != bundle.EvalSetVersion ||
		run.EvalSetHash != bundle.EvalSetHash ||
		run.EvaluationPolicyHash != bundle.EvaluationPolicyHash ||
		run.AggregationPolicyHash != bundle.AggregationPolicyHash {
		return nil, httperr.BadRequest(40075, "gate evidence chain mismatch")
	}
	snapshotHash, err := canonicalHash(json.RawMessage(snapshot.ExecutionConfig))
	if err != nil || snapshotHash != snapshot.SnapshotHash {
		return nil, httperr.BadRequest(40075, "execution snapshot hash mismatch")
	}
	switch input.Environment {
	case "development", "pilot", "production":
	default:
		return nil, httperr.BadRequest(40075, "unsupported release environment")
	}
	var states map[string]string
	if err := json.Unmarshal(input.SubGateStates, &states); err != nil || len(states) != 6 {
		return nil, httperr.BadRequest(40075, "six sub gate states are required")
	}
	overall := model.GateDecisionPass
	hasWarning := false
	hasWaived := false
	if run.Pass != nil && !*run.Pass {
		overall = model.GateDecisionBlock
	}
	for _, state := range states {
		switch state {
		case model.GateDecisionPass:
		case "WARN":
			hasWarning = true
		case model.GateDecisionBlock:
			overall = model.GateDecisionBlock
		case "WAIVED":
			hasWaived = true
			if input.Environment == "production" {
				overall = model.GateDecisionBlock
			} else {
				hasWarning = true
			}
		default:
			return nil, httperr.BadRequest(40075, "invalid sub gate state")
		}
	}
	if overall == model.GateDecisionPass && hasWarning {
		overall = model.GateDecisionPassWithWarning
	}
	statesJSON, _ := json.Marshal(states)
	waiver, err := validPayloadJSON(input.Waiver, 40075, "waiver must be JSON")
	if err != nil {
		return nil, err
	}
	if hasWaived {
		var waiverEvidence struct {
			Actor      string `json:"actor"`
			Reason     string `json:"reason"`
			Scope      string `json:"scope"`
			ExpiresAt  string `json:"expires_at"`
			ApprovalID string `json:"approval_id"`
		}
		_ = json.Unmarshal([]byte(waiver), &waiverEvidence)
		if waiverEvidence.Actor == "" || waiverEvidence.Reason == "" || waiverEvidence.Scope == "" ||
			waiverEvidence.ExpiresAt == "" || waiverEvidence.ApprovalID == "" {
			return nil, httperr.BadRequest(40075, "waiver requires actor, reason, scope, expiry and approval evidence")
		}
	}
	decision := &model.ReleaseGateDecision{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: candidate.CandidateID,
		CandidateVersion: input.CandidateVersion, DecisionVersion: time.Now().UTC().UnixNano(),
		EvidenceBundleID: bundle.ID, Environment: input.Environment,
		EnvironmentPolicyVersion: input.EnvironmentPolicyVersion, EnvironmentPolicyHash: input.EnvironmentPolicyHash,
		SubGateStates: string(statesJSON), Decision: overall, Reason: input.Reason,
		Waiver: waiver, Actor: userID, ApprovalID: input.ApprovalID, CreatedAt: time.Now().UTC(),
	}
	created, err := s.Store.CreateGateDecision(ctx, decision)
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, httperr.New(409, 40987, "gate decision version conflict")
	}
	if input.Activate {
		if err := s.Store.ActivateGateDecision(ctx, decision); err != nil {
			return nil, err
		}
		decision.ActiveGate = true
	}
	nextStatus := model.CandidateGatePending
	if overall == model.GateDecisionPass {
		nextStatus = model.CandidateApproved
	} else if overall == model.GateDecisionBlock {
		nextStatus = model.CandidateRejected
	}
	_ = s.Store.TransitionReleaseCandidate(ctx, tenantID, candidate.CandidateID, candidate.Status, nextStatus)
	return decision, nil
}

func (s *Service) CreateRelease(ctx context.Context, tenantID, userID string, input ReleaseInput) (*model.Release, error) {
	candidate, err := s.getReleaseCandidateByIdentity(ctx, tenantID, input.ReleaseCandidateID, input.CandidateVersion)
	if err != nil || candidate == nil {
		return nil, httperr.NotFound("release candidate not found")
	}
	if input.Environment == "production" && candidate.Status != model.CandidateApproved {
		return nil, httperr.Forbidden("production release requires an approved candidate")
	}
	switch input.Environment {
	case "development", "pilot", "production":
	default:
		return nil, httperr.BadRequest(40076, "unsupported release environment")
	}
	gate, err := s.Store.GetGateDecision(ctx, tenantID, input.GateDecisionID)
	if err != nil || gate == nil {
		return nil, httperr.NotFound("gate decision not found")
	}
	snapshot, err := s.Store.GetExecutionSnapshot(ctx, tenantID, input.SnapshotID)
	if err != nil || snapshot == nil {
		return nil, httperr.NotFound("execution snapshot not found")
	}
	if input.Environment == "production" && gate.Decision != model.GateDecisionPass {
		return nil, httperr.Forbidden("production release requires a PASS gate decision")
	}
	if snapshot.ModelRoutePinID != "" || snapshot.ModelRoutePinVersion > 0 ||
		snapshot.EnterpriseConnectionID != "" || snapshot.EnterpriseBindingID != "" {
		pin, err := s.Store.GetModelRouteEnterprisePinByIdentity(ctx, tenantID, snapshot.ModelRoutePinID, snapshot.ModelRoutePinVersion)
		if err != nil {
			return nil, err
		}
		if pin == nil {
			return nil, httperr.BadRequest(40076, "release enterprise pin is unavailable")
		}
		route, err := s.Store.GetModelRoute(ctx, tenantID, pin.RouteID)
		if err != nil {
			return nil, err
		}
		if route == nil {
			return nil, httperr.BadRequest(40076, "release model route is unavailable")
		}
		validPin, err := s.ValidateEnterpriseModelRoutePin(ctx, route)
		if err != nil {
			return nil, err
		}
		if validPin == nil ||
			validPin.PinID != snapshot.ModelRoutePinID ||
			validPin.Version != snapshot.ModelRoutePinVersion ||
			validPin.ConnectionID != snapshot.EnterpriseConnectionID ||
			validPin.ConnectionVersion != snapshot.EnterpriseConnectionVersion ||
			validPin.BindingID != snapshot.EnterpriseBindingID ||
			validPin.BindingVersion != snapshot.EnterpriseBindingVersion ||
			validPin.ModelRef != snapshot.EnterpriseModelRef {
			return nil, httperr.BadRequest(40076, "release enterprise pin no longer matches the execution snapshot")
		}
	}
	bundle, err := s.Store.GetEvidenceBundle(ctx, tenantID, gate.EvidenceBundleID)
	if err != nil || bundle == nil {
		return nil, httperr.NotFound("evidence bundle not found")
	}
	if gate.Environment != input.Environment ||
		bundle.SnapshotID != snapshot.ID || bundle.SnapshotHash != snapshot.SnapshotHash ||
		gate.ReleaseCandidateID != candidate.CandidateID || gate.CandidateVersion != input.CandidateVersion ||
		snapshot.ReleaseCandidateID != candidate.CandidateID || snapshot.CandidateVersion != input.CandidateVersion {
		return nil, httperr.BadRequest(40076, "release evidence chain mismatch")
	}
	if gate.ReleaseCandidateID != candidate.CandidateID || gate.CandidateVersion != input.CandidateVersion ||
		snapshot.ReleaseCandidateID != candidate.CandidateID || snapshot.CandidateVersion != input.CandidateVersion {
		return nil, httperr.BadRequest(40076, "release evidence chain mismatch")
	}
	release := &model.Release{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: candidate.CandidateID,
		CandidateVersion: input.CandidateVersion, SnapshotID: snapshot.ID,
		GateDecisionID: gate.ID, Environment: input.Environment, Status: model.ReleaseStatusCreated,
		ModelRouteVersion: snapshot.ModelRouteVersion, ModelRoutePinID: snapshot.ModelRoutePinID,
		ModelRoutePinVersion:           snapshot.ModelRoutePinVersion,
		EnterpriseConnectionID:         snapshot.EnterpriseConnectionID,
		EnterpriseConnectionVersion:    snapshot.EnterpriseConnectionVersion,
		EnterpriseConnectionConfigHash: snapshot.EnterpriseConnectionConfigHash,
		EnterpriseBindingID:            snapshot.EnterpriseBindingID,
		EnterpriseBindingVersion:       snapshot.EnterpriseBindingVersion,
		EnterpriseModelRef:             snapshot.EnterpriseModelRef,
		CredentialVersion:              snapshot.CredentialVersion,
		ReleasedBy:                     userID, RollbackBaseline: input.RollbackBaseline, CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateRelease(ctx, release); err != nil {
		return nil, httperr.New(409, 40988, "release already exists")
	}
	return release, nil
}

func (s *Service) CompleteRelease(ctx context.Context, tenantID, releaseID string, failed bool) (*model.Release, error) {
	to := model.ReleaseStatusReleased
	if failed {
		to = model.ReleaseStatusFailed
	}
	_ = s.Store.TransitionRelease(ctx, tenantID, releaseID, model.ReleaseStatusCreated, model.ReleaseStatusReleasing)
	if err := s.Store.TransitionRelease(ctx, tenantID, releaseID, model.ReleaseStatusReleasing, to); err != nil {
		return nil, err
	}
	return s.Store.GetRelease(ctx, tenantID, releaseID)
}

// RollbackRelease records a governance rollback to the newest prior released
// package in the same candidate and environment. Execution remains owned by
// the configured executor; this method protects the auditable state transition.
func (s *Service) RollbackRelease(ctx context.Context, tenantID, userID, releaseID, reason string) (*model.Release, error) {
	if reason == "" {
		return nil, httperr.BadRequest(40077, "rollback reason is required")
	}
	current, err := s.Store.GetRelease(ctx, tenantID, releaseID)
	if err != nil || current == nil {
		return nil, httperr.NotFound("release not found")
	}
	if current.Status != model.ReleaseStatusReleased {
		return nil, httperr.New(409, 40990, "only released packages can be rolled back")
	}
	releases, _, err := s.Store.ListReleases(ctx, tenantID, current.ReleaseCandidateID, 1, 100)
	if err != nil {
		return nil, err
	}
	baseline := model.Release{}
	for _, candidateRelease := range releases {
		if candidateRelease.ID == current.ID ||
			candidateRelease.Environment != current.Environment ||
			candidateRelease.Status != model.ReleaseStatusReleased ||
			candidateRelease.CreatedAt.After(current.CreatedAt) {
			continue
		}
		baseline = candidateRelease
		break
	}
	if baseline.ID == "" {
		return nil, httperr.New(409, 40991, "rollback baseline release not found")
	}
	if err := s.Store.TransitionRelease(ctx, tenantID, current.ID, model.ReleaseStatusReleased, model.ReleaseStatusRolledBack); err != nil {
		return nil, err
	}
	notify.Emit(ctx, notify.Event{
		Title: "release rolled back", Severity: "warn", TenantID: tenantID,
		Resource: "release-governance", Type: "release.rollback", ResourceID: current.ID,
		Detail: reason, Fields: map[string]string{"baseline_release_id": baseline.ID},
	})
	return &baseline, nil
}

func (s *Service) CreateQualityIssue(ctx context.Context, tenantID, userID string, input QualityIssueInput) (*model.QualityIssue, error) {
	if _, err := requiredString(input.Source, "source"); err != nil {
		return nil, err
	}
	if _, err := requiredString(input.Title, "title"); err != nil {
		return nil, err
	}
	evidence, err := validPayloadJSON(input.Evidence, 40077, "evidence must be JSON")
	if err != nil {
		return nil, err
	}
	issue := &model.QualityIssue{
		ID: id.New(), TenantID: tenantID, Source: input.Source, SourceID: input.SourceID,
		Title: input.Title, Evidence: evidence, Owner: input.Owner,
		Status: model.QualityIssueOpen, ResolutionTargetType: input.ResolutionTargetType,
		ResolutionTargetID: input.ResolutionTargetID, ResolutionTargetVersion: input.ResolutionTargetVersion,
		EvalCaseID: input.EvalCaseID, CreatedBy: userID, CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateQualityIssue(ctx, issue); err != nil {
		return nil, err
	}
	return issue, nil
}

func (s *Service) UpdateQualityIssue(ctx context.Context, tenantID, userID string, issueID string, input QualityIssueStateInput) (*model.QualityIssue, error) {
	issue, err := s.Store.GetQualityIssue(ctx, tenantID, issueID)
	if err != nil || issue == nil {
		return nil, httperr.NotFound("quality issue not found")
	}
	if input.Status == model.QualityIssueVerified {
		return nil, httperr.New(409, 40989, "quality issue verification must be driven by an evaluation run")
	}
	if issue.Status == model.QualityIssueClosed && input.Status != model.QualityIssueReopened {
		return nil, httperr.New(409, 40989, "closed quality issues can only be reopened")
	}
	if input.Status == model.QualityIssueReopened {
		if input.ReopenReason == "" {
			return nil, httperr.BadRequest(40077, "reopen reason is required")
		}
		issue.ReopenCount++
		issue.PreviousResolution = issue.Resolution
		issue.PreviousRunID = issue.EvaluationRunID
		issue.ReopenReason = input.ReopenReason
	}
	issue.Owner = input.Owner
	issue.Status = input.Status
	issue.Resolution = input.Resolution
	issue.ResolutionTargetType = input.ResolutionTargetType
	issue.ResolutionTargetID = input.ResolutionTargetID
	issue.ResolutionTargetVersion = input.ResolutionTargetVersion
	issue.EvalCaseID = input.EvalCaseID
	issue.EvaluationRunID = input.EvaluationRunID
	issue.ReleaseID = input.ReleaseID
	if err := s.Store.UpdateQualityIssue(ctx, issue); err != nil {
		return nil, err
	}
	_ = userID
	return issue, nil
}

func (s *Service) VerifyQualityIssue(ctx context.Context, tenantID string, issueID, runID string) (*model.QualityIssue, error) {
	issue, err := s.Store.GetQualityIssue(ctx, tenantID, issueID)
	if err != nil || issue == nil {
		return nil, httperr.NotFound("quality issue not found")
	}
	run, err := s.Store.GetEvaluationRun(ctx, tenantID, runID)
	if err != nil || run == nil {
		return nil, httperr.NotFound("evaluation run not found")
	}
	if run.ReleaseCandidateID != issue.ResolutionTargetID || run.Status != model.EvaluationRunCompleted ||
		run.Pass == nil || !*run.Pass {
		return nil, httperr.New(409, 40989, "quality issue resolution target and passing run do not match")
	}
	if issue.Status != model.QualityIssueRegressionPending {
		return nil, httperr.New(409, 40989, "only regression pending quality issues can be verified")
	}
	issue.Status = model.QualityIssueVerified
	issue.EvaluationRunID = run.ID
	if err := s.Store.UpdateQualityIssue(ctx, issue); err != nil {
		return nil, err
	}
	return issue, nil
}

func ptrTime(value time.Time) *time.Time { return &value }

func boolPtr(value bool) *bool { return &value }
