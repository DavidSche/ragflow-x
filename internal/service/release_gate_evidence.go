package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	ReleaseGateEvidencePass       = "PASS"
	ReleaseGateEvidenceWarn       = "WARN"
	ReleaseGateEvidenceBlock      = "BLOCK"
	ReleaseGateEvidenceWait       = "WAIT"
	ReleaseGateEvidenceNotDerived = "NOT_DERIVED"
)

const releaseGateMinimumSchemaVersion = 142

const (
	releaseGatePermissionContractVersion   = "release-gate-permission-v1"
	releaseGateExperienceContractVersion   = "release-gate-experience-v1"
	releaseGateConversationContractVersion = "phase0-fail-closed-v1"
)

type ReleaseGateEvidenceItem struct {
	Gate   string         `json:"gate"`
	State  string         `json:"state"`
	Reason string         `json:"reason"`
	Data   map[string]any `json:"data,omitempty"`
}

type ReleaseGateEvidenceReport struct {
	CandidateID      string                    `json:"candidate_id"`
	CandidateVersion int64                     `json:"candidate_version"`
	GeneratedAt      time.Time                 `json:"generated_at"`
	OverallState     string                    `json:"overall_state"`
	AdvisoryOnly     bool                      `json:"advisory_only"`
	Items            []ReleaseGateEvidenceItem `json:"items"`
}

type releaseGateEvidenceSource struct {
	candidate        *model.ReleaseCandidate
	snapshot         *model.ExecutionSnapshot
	run              *model.EvaluationRun
	bundle           *model.EvidenceBundle
	release          *model.AssistantRelease
	datasets         []model.DatasetBindingVersion
	compilations     []model.CompilationRun
	memoryPolicies   []model.MemoryPolicy
	memoryOperations []model.MemoryOperation
	artifactCounts   map[string]int64
	schemaVersion    int64
	runtimeHealthy   *model.RuntimeHealth
	costEvidence     *model.ReleaseGateRuntimeEvidenceSummary
	conversation     *model.ReleaseGateConversationEvidenceSummary
	experience       *model.ReleaseGateExperienceEvidenceSummary
}

func (s *Service) GetReleaseGateEvidenceReport(
	ctx context.Context, tenantID, candidateID string, candidateVersion int64,
) (*ReleaseGateEvidenceReport, error) {
	if strings.TrimSpace(candidateID) == "" || candidateVersion <= 0 {
		return nil, httperr.BadRequest(40077, "candidate_id and candidate_version are required")
	}
	source, err := s.releaseGateEvidenceSource(ctx, tenantID, candidateID, candidateVersion)
	if err != nil {
		return nil, err
	}
	experienceConfiguration := ""
	if source.bundle != nil {
		experienceConfiguration = source.bundle.ConfigurationEvidence
	}
	items := []ReleaseGateEvidenceItem{
		s.releaseGateCompilationEvidence(ctx, source),
		s.releaseGateMemoryEvidence(source),
		s.releaseGateIdentityEvidence(source),
		s.releaseGateMigrationEvidence(source),
		s.releaseGatePermissionEvidence(source),
		s.releaseGateImprovementEvidence(source),
		s.releaseGatePrivacyEvidence(source),
		s.releaseGateCostEvidenceFor(source.costEvidence),
		s.releaseGateConversationEvidenceFor(source.conversation),
		s.releaseGateExperienceEvidenceFor(
			source.experience, source.candidate, experienceConfiguration,
		),
		s.releaseGateOperationsEvidence(source),
	}
	states := make(map[string]string, len(items))
	for _, item := range items {
		states[item.Gate] = item.State
	}
	return &ReleaseGateEvidenceReport{
		CandidateID: candidateID, CandidateVersion: candidateVersion,
		GeneratedAt: time.Now().UTC(), OverallState: releaseGateEvidenceOverall(states),
		AdvisoryOnly: true, Items: items,
	}, nil
}

func (s *Service) releaseGateEvidenceSource(
	ctx context.Context, tenantID, candidateID string, candidateVersion int64,
) (*releaseGateEvidenceSource, error) {
	candidate, err := s.Store.GetReleaseCandidateVersion(ctx, tenantID, candidateID, candidateVersion)
	if err != nil {
		return nil, httperr.New(502, 50210, "release candidate lookup failed")
	}
	if candidate == nil {
		return nil, httperr.NotFound("release candidate not found")
	}
	source := &releaseGateEvidenceSource{candidate: candidate}
	if source.snapshot, err = s.Store.GetExecutionSnapshotByCandidate(
		ctx, tenantID, candidateID, candidateVersion,
	); err != nil {
		return nil, httperr.New(502, 50210, "execution snapshot lookup failed")
	}
	filter := repository.EvaluationScopeFilter{
		CandidateID: candidateID, CandidateVer: candidateVersion,
		Scope: model.EvaluationScopeRelease,
	}
	runs, _, err := s.Store.ListEvaluationRuns(ctx, tenantID, filter, 1, 1)
	if err != nil {
		return nil, httperr.New(502, 50210, "evaluation run lookup failed")
	}
	if len(runs) > 0 {
		source.run = &runs[0]
	}
	bundles, _, err := s.Store.ListEvidenceBundles(ctx, tenantID, filter, 1, 1)
	if err != nil {
		return nil, httperr.New(502, 50210, "evidence bundle lookup failed")
	}
	if len(bundles) > 0 {
		source.bundle = &bundles[0]
	}
	if candidate.TargetType == "assistant" {
		releaseVersion, parseErr := strconv.ParseInt(strings.TrimPrefix(candidate.TargetVersion, "v"), 10, 64)
		if parseErr == nil && releaseVersion > 0 {
			releases, _, listErr := s.Store.ListAssistantReleases(
				ctx, tenantID, candidate.TargetID, "", 1, 1,
			)
			if listErr != nil {
				return nil, httperr.New(502, 50210, "assistant release lookup failed")
			}
			for index := range releases {
				if releases[index].ReleaseVersion == releaseVersion {
					source.release = &releases[index]
					break
				}
			}
			if source.release != nil {
				if source.datasets, err = s.Store.ListDatasetBindingVersions(
					ctx, tenantID, source.release.ID,
				); err != nil {
					return nil, httperr.New(502, 50210, "dataset binding lookup failed")
				}
				if source.memoryPolicies, _, err = s.Store.ListMemoryPolicies(
					ctx, tenantID, repository.MemoryPolicyFilter{AssistantID: candidate.TargetID}, 1, 200,
				); err != nil {
					return nil, httperr.New(502, 50210, "memory policy lookup failed")
				}
				if source.memoryOperations, _, err = s.Store.ListMemoryOperations(
					ctx, tenantID, repository.MemoryOperationFilter{AssistantID: candidate.TargetID}, 1, 500,
				); err != nil {
					return nil, httperr.New(502, 50210, "memory operation lookup failed")
				}
				if health, healthErr := s.Store.GetRuntimeHealth(
					ctx, tenantID, source.release.ID,
				); healthErr == nil {
					source.runtimeHealthy = health
				}
				if source.costEvidence, err = s.Store.GetReleaseGateCostEvidence(
					ctx, tenantID, source.release.ID,
				); err != nil {
					return nil, httperr.New(502, 50210, "candidate cost evidence lookup failed")
				}
				if source.conversation, err = s.Store.GetReleaseGateConversationEvidence(
					ctx, tenantID, source.release.ID, releaseGateConversationContractVersion,
				); err != nil {
					return nil, httperr.New(502, 50210, "candidate conversation evidence lookup failed")
				}
				if source.experience, err = s.Store.GetReleaseGateExperienceEvidence(
					ctx, tenantID, source.release.ID,
				); err != nil {
					return nil, httperr.New(502, 50210, "candidate experience evidence lookup failed")
				}
			}
		}
	}
	if len(source.datasets) > 0 {
		source.compilations, source.artifactCounts, err = s.releaseGateCompilationSource(ctx, tenantID, source.datasets)
		if err != nil {
			return nil, err
		}
	}
	if source.schemaVersion, err = s.Store.GetAppliedSchemaVersion(ctx); err != nil {
		return nil, httperr.New(502, 50210, "applied schema version lookup failed")
	}
	return source, nil
}

func (s *Service) releaseGateCompilationSource(
	ctx context.Context, tenantID string, datasets []model.DatasetBindingVersion,
) ([]model.CompilationRun, map[string]int64, error) {
	runs, _, err := s.Store.ListCompilationRuns(
		ctx, tenantID, repository.CompilationRunFilter{}, 1, 500,
	)
	if err != nil {
		return nil, nil, httperr.New(502, 50210, "compilation run lookup failed")
	}
	latest := make(map[string]model.CompilationRun, len(datasets))
	for index := range runs {
		run := runs[index]
		current, found := latest[run.DatasetID]
		if !found || run.CreatedAt.After(current.CreatedAt) ||
			(run.CreatedAt.Equal(current.CreatedAt) && run.ID > current.ID) {
			latest[run.DatasetID] = run
		}
	}
	ordered := make([]model.CompilationRun, 0, len(latest))
	counts := make(map[string]int64, len(latest))
	for _, dataset := range datasets {
		run, found := latest[dataset.DatasetID]
		if !found {
			continue
		}
		counts[run.ID] = releaseGateArtifactCount(ctx, s.Store, tenantID, dataset.DatasetID, run.ID)
		ordered = append(ordered, run)
	}
	return ordered, counts, nil
}

func releaseGateArtifactCount(
	ctx context.Context, store repository.Store, tenantID, datasetID, runID string,
) int64 {
	_, total, err := store.ListArtifactProjections(ctx, tenantID, datasetID, runID, 1, 1)
	if err == nil {
		return total
	}
	artifacts, _, _ := store.ListArtifactProjections(ctx, tenantID, datasetID, runID, 1, 200)
	return int64(len(artifacts))
}

func releaseGateEvidenceItem(gate, state, reason string, data ...map[string]any) ReleaseGateEvidenceItem {
	item := ReleaseGateEvidenceItem{Gate: gate, State: state, Reason: reason}
	if len(data) > 0 {
		item.Data = data[0]
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	return item
}

func (s *Service) releaseGateCompilationEvidence(ctx context.Context, source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	if source.release == nil || len(source.datasets) == 0 {
		return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceNotDerived,
			"release dataset bindings are unavailable")
	}
	runsByDataset := make(map[string]model.CompilationRun, len(source.compilations))
	for _, run := range source.compilations {
		runsByDataset[run.DatasetID] = run
	}
	for _, dataset := range source.datasets {
		run, found := runsByDataset[dataset.DatasetID]
		if !found {
			return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceNotDerived,
				"compilation run is missing for bound dataset "+dataset.DatasetID,
				map[string]any{"dataset_id": dataset.DatasetID})
		}
		switch run.State {
		case model.CompilationRunAccepted, model.CompilationRunDispatched, model.CompilationRunUnknown:
			return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceWait,
				"compilation has not reached a confirmed terminal state",
				map[string]any{"dataset_id": dataset.DatasetID, "run_id": run.ID, "state": run.State})
		case model.CompilationRunFailed, model.CompilationRunCancelled, model.CompilationRunBlocked:
			return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceBlock,
				"compilation terminated without confirmed artifacts",
				map[string]any{"dataset_id": dataset.DatasetID, "run_id": run.ID, "state": run.State})
		case model.CompilationRunConfirmed, model.CompilationRunReconciled, model.CompilationRunSkipped:
			if !run.ManifestComplete || run.SourceConsistency != model.CompilationSourceConsistencyConsistent {
				return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceNotDerived,
					"compilation source consistency is not proven",
					map[string]any{"dataset_id": dataset.DatasetID, "run_id": run.ID,
						"manifest_complete": run.ManifestComplete, "source_consistency": run.SourceConsistency})
			}
			if artifacts := source.artifactCounts[run.ID]; artifacts == 0 {
				return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceNotDerived,
					"confirmed compilation has no artifact projection",
					map[string]any{"dataset_id": dataset.DatasetID, "run_id": run.ID})
			}
		default:
			return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceNotDerived,
				"compilation state is unrecognized",
				map[string]any{"dataset_id": dataset.DatasetID, "run_id": run.ID, "state": run.State})
		}
	}
	for _, run := range source.compilations {
		projections, _, err := s.Store.ListArtifactProjections(
			ctx, run.TenantID, run.DatasetID, run.ID, 1, 200,
		)
		if err != nil {
			return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceNotDerived,
				"artifact projections cannot be evaluated", map[string]any{"run_id": run.ID})
		}
		for _, projection := range projections {
			if projection.State != model.ArtifactProjectionReady {
				return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidenceWarn,
					"artifact projection is not ready", map[string]any{
						"run_id": run.ID, "artifact_id": projection.ArtifactID, "state": projection.State,
					})
			}
		}
	}
	return releaseGateEvidenceItem(model.ReleaseGateCompilationContract, ReleaseGateEvidencePass,
		"all bound datasets have confirmed, consistent, ready compilations")
}

func (s *Service) releaseGateMemoryEvidence(source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	if source.release == nil {
		return releaseGateEvidenceItem(model.ReleaseGateMemoryContract, ReleaseGateEvidenceNotDerived,
			"assistant release binding is unavailable")
	}
	if len(source.memoryPolicies) == 0 {
		return releaseGateEvidenceItem(model.ReleaseGateMemoryContract, ReleaseGateEvidenceNotDerived,
			"no memory policy is explicitly bound to the assistant")
	}
	for _, policy := range source.memoryPolicies {
		if policy.State != model.MemoryPolicyActive || policy.RagflowMemoryID == "" {
			return releaseGateEvidenceItem(model.ReleaseGateMemoryContract, ReleaseGateEvidenceNotDerived,
				"memory policy is not an active bound policy", map[string]any{"policy_id": policy.ID})
		}
	}
	for _, operation := range source.memoryOperations {
		switch operation.State {
		case model.MemoryOperationUnknown:
			return releaseGateEvidenceItem(model.ReleaseGateMemoryContract, ReleaseGateEvidenceWait,
				"memory operation result is undetermined", map[string]any{"operation_id": operation.ID})
		case model.MemoryOperationFailed, model.MemoryOperationBlocked:
			return releaseGateEvidenceItem(model.ReleaseGateMemoryContract, ReleaseGateEvidenceBlock,
				"memory operation did not complete under policy", map[string]any{"operation_id": operation.ID})
		case model.MemoryOperationAccepted, model.MemoryOperationAdmitted,
			model.MemoryOperationDispatched:
			return releaseGateEvidenceItem(model.ReleaseGateMemoryContract, ReleaseGateEvidenceWait,
				"memory operation has not reached a terminal state", map[string]any{"operation_id": operation.ID})
		}
	}
	return releaseGateEvidenceItem(model.ReleaseGateMemoryContract, ReleaseGateEvidencePass,
		"all bound memory policies are active and observed operations are terminal")
}

func (s *Service) releaseGateIdentityEvidence(source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	if source.snapshot == nil || source.bundle == nil {
		return releaseGateEvidenceItem(model.ReleaseGateIdentity, ReleaseGateEvidenceNotDerived,
			"execution snapshot or evidence bundle is missing")
	}
	pins := map[string]any{
		"model_route_version":     source.snapshot.ModelRouteVersion,
		"model_route_pin_id":      source.snapshot.ModelRoutePinID,
		"model_route_pin_version": source.snapshot.ModelRoutePinVersion,
		"credential_version":      source.snapshot.CredentialVersion,
	}
	if strings.TrimSpace(source.snapshot.ModelRouteVersion) == "" ||
		strings.TrimSpace(source.snapshot.ModelRoutePinID) == "" ||
		source.snapshot.ModelRoutePinVersion <= 0 ||
		strings.TrimSpace(source.snapshot.CredentialVersion) == "" {
		return releaseGateEvidenceItem(model.ReleaseGateIdentity, ReleaseGateEvidenceNotDerived,
			"runtime pin is incomplete", pins)
	}
	if source.snapshot.SnapshotHash != source.bundle.SnapshotHash ||
		source.snapshot.ModelRouteVersion != source.bundle.ModelRouteVersion ||
		source.snapshot.ModelRoutePinID != source.bundle.ModelRoutePinID ||
		source.snapshot.ModelRoutePinVersion != source.bundle.ModelRoutePinVersion ||
		source.snapshot.EnterpriseConnectionID != source.bundle.EnterpriseConnectionID ||
		source.snapshot.EnterpriseConnectionVersion != source.bundle.EnterpriseConnectionVersion ||
		source.snapshot.EnterpriseConnectionConfigHash != source.bundle.EnterpriseConnectionConfigHash ||
		source.snapshot.EnterpriseBindingID != source.bundle.EnterpriseBindingID ||
		source.snapshot.EnterpriseBindingVersion != source.bundle.EnterpriseBindingVersion ||
		source.snapshot.EnterpriseModelRef != source.bundle.EnterpriseModelRef ||
		source.snapshot.CredentialVersion != source.bundle.CredentialVersion {
		return releaseGateEvidenceItem(model.ReleaseGateIdentity, ReleaseGateEvidenceBlock,
			"evidence bundle runtime pins do not match execution snapshot", pins)
	}
	return releaseGateEvidenceItem(model.ReleaseGateIdentity, ReleaseGateEvidencePass,
		"evidence bundle runtime pins match execution snapshot", pins)
}

func (s *Service) releaseGateMigrationEvidence(source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	data := map[string]any{
		"applied_schema_version": source.schemaVersion,
		"minimum_schema_version": releaseGateMinimumSchemaVersion,
	}
	if source.schemaVersion < releaseGateMinimumSchemaVersion {
		return releaseGateEvidenceItem(model.ReleaseGateMigration, ReleaseGateEvidenceBlock,
			"applied migration is below the required release floor", data)
	}
	return releaseGateEvidenceItem(model.ReleaseGateMigration, ReleaseGateEvidencePass,
		"applied migration is at or above the required release floor", data)
}

func (s *Service) releaseGatePermissionEvidence(source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	if source.bundle == nil {
		return releaseGateEvidenceItem(model.ReleaseGatePermission, ReleaseGateEvidenceNotDerived,
			"evidence bundle is unavailable")
	}
	data, valid, reason := releaseGateScopedContract(
		source.bundle.PermissionEvidence, "permission_gate", releaseGatePermissionContractVersion,
		source.candidate, releaseGatePermissionRequirements(),
	)
	data["contract_version"] = releaseGatePermissionContractVersion
	if !valid {
		state := ReleaseGateEvidenceNotDerived
		if reason == "candidate identity mismatch" {
			state = ReleaseGateEvidenceBlock
		}
		return releaseGateEvidenceItem(model.ReleaseGatePermission, state, reason, data)
	}
	return releaseGateEvidenceItem(model.ReleaseGatePermission, ReleaseGateEvidencePass,
		"strict candidate-scoped permission contract passes", data)
}

func (s *Service) releaseGateImprovementEvidence(source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	if source.run == nil {
		return releaseGateEvidenceItem(model.ReleaseGateImprovement, ReleaseGateEvidenceNotDerived,
			"release evaluation run is missing")
	}
	data := map[string]any{
		"evaluation_run_id": source.run.ID,
		"status":            source.run.Status,
		"gate_status":       source.run.GateStatus,
	}
	if source.run.Status != model.EvaluationRunCompleted || source.run.Pass == nil {
		return releaseGateEvidenceItem(model.ReleaseGateImprovement, ReleaseGateEvidenceNotDerived,
			"latest release evaluation is not a completed explicit pass/fail", data)
	}
	switch *source.run.Pass {
	case true:
		return releaseGateEvidenceItem(model.ReleaseGateImprovement, ReleaseGateEvidencePass,
			"latest release evaluation explicitly passed", data)
	default:
		return releaseGateEvidenceItem(model.ReleaseGateImprovement, ReleaseGateEvidenceBlock,
			"latest release evaluation explicitly failed", data)
	}
}

func (s *Service) releaseGatePrivacyEvidence(source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	if source.release == nil {
		return releaseGateEvidenceItem(model.ReleaseGatePrivacy, ReleaseGateEvidenceNotDerived,
			"assistant release binding is unavailable")
	}
	if len(source.memoryPolicies) == 0 {
		return releaseGateEvidenceItem(model.ReleaseGatePrivacy, ReleaseGateEvidenceNotDerived,
			"privacy policy evidence is unavailable")
	}
	for _, operation := range source.memoryOperations {
		if operation.State == model.MemoryOperationUnknown {
			return releaseGateEvidenceItem(model.ReleaseGatePrivacy, ReleaseGateEvidenceWait,
				"privacy outcome cannot be determined from an unknown operation",
				map[string]any{"operation_id": operation.ID})
		}
	}
	return releaseGateEvidenceItem(model.ReleaseGatePrivacy, ReleaseGateEvidencePass,
		"bound memory policies and observed operations provide a closed privacy outcome")
}

func (s *Service) releaseGateCostEvidenceFor(
	summary *model.ReleaseGateRuntimeEvidenceSummary,
) ReleaseGateEvidenceItem {
	if summary == nil {
		return releaseGateEvidenceItem(model.ReleaseGateCost, ReleaseGateEvidenceNotDerived,
			"no release-attributed runtime contract is available")
	}
	data := map[string]any{
		"runtime_requests":   summary.RuntimeRequests,
		"metered_requests":   summary.CostRequests,
		"unmetered_requests": summary.UnmeteredRequests,
		"unpolicy_requests":  summary.UnpolicyRequests,
		"tokens_in":          summary.TokensIn,
		"tokens_out":         summary.TokensOut,
		"estimated_cost":     summary.EstimatedCost,
		"provider_billed":    false,
	}
	if summary.RuntimeRequests == 0 {
		return releaseGateEvidenceItem(model.ReleaseGateCost, ReleaseGateEvidenceWait,
			"candidate has no attributed runtime requests", data)
	}
	if summary.UnmeteredRequests > 0 || summary.CostRequests != summary.RuntimeRequests {
		return releaseGateEvidenceItem(model.ReleaseGateCost, ReleaseGateEvidenceBlock,
			"candidate runtime requests are missing cost attribution", data)
	}
	if summary.UnpolicyRequests > 0 {
		return releaseGateEvidenceItem(model.ReleaseGateCost, ReleaseGateEvidenceBlock,
			"candidate cost attribution lacks estimation policy", data)
	}
	return releaseGateEvidenceItem(model.ReleaseGateCost, ReleaseGateEvidencePass,
		"candidate runtime requests have policy-versioned estimated cost attribution", data)
}

func (s *Service) releaseGateConversationEvidenceFor(
	summary *model.ReleaseGateConversationEvidenceSummary,
) ReleaseGateEvidenceItem {
	if summary == nil {
		return releaseGateEvidenceItem(model.ReleaseGateConversation, ReleaseGateEvidenceNotDerived,
			"conversation evidence has no candidate-scoped runtime contract")
	}
	data := map[string]any{
		"contract_version":              releaseGateConversationContractVersion,
		"runtime_requests":              summary.RuntimeRequests,
		"decision_requests":             summary.DecisionRequests,
		"decision_count":                summary.DecisionCount,
		"invalid_decision_count":        summary.InvalidDecisionCount,
		"invalid_contract_count":        summary.InvalidContractCount,
		"search_memory_operation_count": summary.SearchMemoryOperationCount,
	}
	if summary.RuntimeRequests == 0 {
		return releaseGateEvidenceItem(model.ReleaseGateConversation, ReleaseGateEvidenceWait,
			"candidate has no attributed runtime requests", data)
	}
	if summary.DecisionRequests != summary.RuntimeRequests ||
		summary.DecisionCount != summary.RuntimeRequests {
		return releaseGateEvidenceItem(model.ReleaseGateConversation, ReleaseGateEvidenceBlock,
			"candidate runtime requests lack one immutable context decision", data)
	}
	if summary.InvalidDecisionCount > 0 || summary.InvalidContractCount > 0 {
		return releaseGateEvidenceItem(model.ReleaseGateConversation, ReleaseGateEvidenceBlock,
			"candidate context decisions violate the fail-closed contract", data)
	}
	if summary.SearchMemoryOperationCount > 0 {
		return releaseGateEvidenceItem(model.ReleaseGateConversation, ReleaseGateEvidenceBlock,
			"candidate search decisions produced memory operations", data)
	}
	return releaseGateEvidenceItem(model.ReleaseGateConversation, ReleaseGateEvidencePass,
		"candidate runtime decisions satisfy the fail-closed conversation contract", data)
}

func (s *Service) releaseGateExperienceEvidenceFor(
	summary *model.ReleaseGateExperienceEvidenceSummary,
	candidate *model.ReleaseCandidate, configuration string,
) ReleaseGateEvidenceItem {
	config, valid, reason := releaseGateScopedContract(
		configuration, "experience_gate", releaseGateExperienceContractVersion,
		candidate, releaseGateExperienceRequirements(),
	)
	if !valid {
		state := ReleaseGateEvidenceNotDerived
		if reason == "candidate identity mismatch" {
			state = ReleaseGateEvidenceBlock
		}
		return releaseGateEvidenceItem(model.ReleaseGateExperience, state, reason, config)
	}
	if summary == nil {
		return releaseGateEvidenceItem(model.ReleaseGateExperience, ReleaseGateEvidenceNotDerived,
			"strict UI contract is present but candidate runtime is unavailable", config)
	}
	data := map[string]any{
		"contract_version": releaseGateExperienceContractVersion,
		"runtime_requests": summary.RuntimeRequests,
		"ledger_requests":  summary.LedgerRequests,
		"completed":        summary.Completed,
		"no_answer":        summary.NoAnswer,
		"failed":           summary.Failed,
		"negative":         summary.Negative,
	}
	if summary.RuntimeRequests == 0 {
		return releaseGateEvidenceItem(model.ReleaseGateExperience, ReleaseGateEvidenceWait,
			"strict UI contract passes but candidate has no runtime ledger", data)
	}
	if summary.LedgerRequests != summary.RuntimeRequests {
		return releaseGateEvidenceItem(model.ReleaseGateExperience, ReleaseGateEvidenceBlock,
			"candidate runtime turns are missing knowledge-operations ledger entries", data)
	}
	if summary.Failed > 0 || summary.NoAnswer > 0 || summary.Negative > 0 {
		return releaseGateEvidenceItem(model.ReleaseGateExperience, ReleaseGateEvidenceWarn,
			"candidate runtime ledger contains degraded experiences", data)
	}
	return releaseGateEvidenceItem(model.ReleaseGateExperience, ReleaseGateEvidencePass,
		"strict UI contract and candidate runtime ledger close successfully", data)
}

func (s *Service) releaseGateOperationsEvidence(source *releaseGateEvidenceSource) ReleaseGateEvidenceItem {
	if source.release == nil {
		return releaseGateEvidenceItem(model.ReleaseGateOperations, ReleaseGateEvidenceNotDerived,
			"assistant release binding is unavailable")
	}
	data := map[string]any{
		"assistant_release_id": source.release.ID,
		"release_state":        source.release.ReleaseState,
		"reconcile_status":     source.release.ReconcileStatus,
	}
	if source.runtimeHealthy == nil {
		return releaseGateEvidenceItem(model.ReleaseGateOperations, ReleaseGateEvidenceNotDerived,
			"runtime health has not been reported", data)
	}
	data["health"] = source.runtimeHealthy.Health
	switch {
	case source.release.ReconcileStatus == model.ReconcileDrifted ||
		source.runtimeHealthy.Health == model.AssistantRuntimeHealthUnavailable:
		return releaseGateEvidenceItem(model.ReleaseGateOperations, ReleaseGateEvidenceBlock,
			"release is drifted or runtime health is unavailable", data)
	case source.release.ReconcileStatus != model.ReconcileConsistent:
		return releaseGateEvidenceItem(model.ReleaseGateOperations, ReleaseGateEvidenceWait,
			"release reconciliation is not consistent", data)
	case source.runtimeHealthy.Health == model.AssistantRuntimeHealthDegraded:
		return releaseGateEvidenceItem(model.ReleaseGateOperations, ReleaseGateEvidenceWarn,
			"runtime health is degraded", data)
	case source.runtimeHealthy.Health == model.AssistantRuntimeHealthHealthy:
		return releaseGateEvidenceItem(model.ReleaseGateOperations, ReleaseGateEvidencePass,
			"release is reconciled and runtime is healthy", data)
	default:
		return releaseGateEvidenceItem(model.ReleaseGateOperations, ReleaseGateEvidenceNotDerived,
			"runtime health state is unrecognized", data)
	}
}

func releaseGateEvidenceOverall(states map[string]string) string {
	hasWarning := false
	hasWait := false
	for _, state := range states {
		switch state {
		case ReleaseGateEvidenceBlock:
			return ReleaseGateEvidenceBlock
		case ReleaseGateEvidenceWait, ReleaseGateEvidenceNotDerived:
			hasWait = true
		case ReleaseGateEvidenceWarn:
			hasWarning = true
		}
	}
	if hasWait {
		return ReleaseGateEvidenceWait
	}
	if hasWarning {
		return ReleaseGateEvidenceWarn
	}
	return ReleaseGateEvidencePass
}

func releaseGateScopedContract(
	raw, gate, contractVersion string, candidate *model.ReleaseCandidate,
	requirements map[string]string,
) (map[string]any, bool, string) {
	data := map[string]any{"gate": gate, "requirements": requirements}
	if !json.Valid([]byte(raw)) {
		return data, false, "contract is not valid JSON"
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return data, false, "contract is not a JSON object"
	}
	data["contract"] = root[gate]
	section, ok := root[gate].(map[string]any)
	if !ok {
		return data, false, gate + " section is missing"
	}
	if candidate != nil {
		candidateID, _ := section["candidate_id"].(string)
		version, _ := section["candidate_version"].(float64)
		data["candidate_id"] = candidateID
		data["candidate_version"] = version
		if candidateID != candidate.CandidateID || int64(version) != candidate.CandidateVersion {
			return data, false, "candidate identity mismatch"
		}
	}
	if version, ok := section["contract_version"].(string); !ok || version != contractVersion {
		return data, false, "contract version mismatch"
	}
	passed, ok := section["passed"].(bool)
	if !ok || !passed {
		return data, false, gate + " does not explicitly assert passed=true"
	}
	for key := range requirements {
		value, ok := section[key].(bool)
		if !ok || !value {
			return data, false, gate + " requirement " + key + " is not explicitly true"
		}
	}
	return data, true, ""
}

func releaseGatePermissionRequirements() map[string]string {
	return map[string]string{
		"resource_matrix":              "new resources are registered in the permission matrix",
		"builtin_reconciliation":       "six builtin roles reconcile transactionally",
		"pre_model_authorization":      "pre-model authorization is enforced",
		"membership_revocation":        "membership revocation takes effect immediately",
		"endpoint_abac_matrix":         "endpoint to resource/action/ABAC matrix is covered",
		"pii_template_tenant_boundary": "PII and self-policy templates enforce tenant boundaries",
		"preview_redaction":            "preview permissions and redaction are covered",
	}
}

func releaseGateExperienceRequirements() map[string]string {
	return map[string]string{
		"dataset_details":     "dataset details UI is integrated",
		"knowledge_ops":       "knowledge operations UI is integrated",
		"memory_workbench":    "memory workbench UI is integrated",
		"conversation_center": "conversation center UI is integrated",
		"assistant_release":   "assistant release UI is integrated",
		"loading_state":       "loading state is handled",
		"empty_state":         "empty state is handled",
		"wait_state":          "WAIT state is handled",
		"error_state":         "error state is handled",
		"unauthorized_state":  "unauthorized state is handled",
		"retry_state":         "retry state is handled",
		"i18n_en":             "English localization is verified",
		"i18n_zh":             "Chinese localization is verified",
	}
}

func validateReleaseGateEvidenceContracts(
	candidate *model.ReleaseCandidate,
	permission, configuration json.RawMessage,
) error {
	if _, valid, reason := releaseGateScopedContract(
		string(permission), "permission_gate", releaseGatePermissionContractVersion,
		candidate, releaseGatePermissionRequirements(),
	); !valid {
		return httperr.BadRequest(40074, "permission evidence contract invalid: "+reason)
	}
	if _, valid, reason := releaseGateScopedContract(
		string(configuration), "experience_gate", releaseGateExperienceContractVersion,
		candidate, releaseGateExperienceRequirements(),
	); !valid {
		return httperr.BadRequest(40074, "experience evidence contract invalid: "+reason)
	}
	return nil
}
