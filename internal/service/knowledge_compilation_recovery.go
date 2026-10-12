package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type CompilationRecoveryEvidence struct {
	TenantID                 string                     `json:"tenant_id"`
	DatasetID                string                     `json:"dataset_id"`
	Kind                     string                     `json:"kind"`
	QueriedAt                time.Time                  `json:"queried_at"`
	Status                   *ragflow.CompilationStatus `json:"status,omitempty"`
	ArtifactTotal            int64                      `json:"artifact_total"`
	BindingCandidates        []string                   `json:"binding_candidates"`
	ManifestComplete         bool                       `json:"manifest_complete"`
	NoMatch                  bool                       `json:"no_match"`
	EvidenceState            string                     `json:"evidence_state"`
	ProbeError               string                     `json:"probe_error,omitempty"`
	ObservationWindowSeconds int64                      `json:"observation_window_seconds"`
}

type CompilationRecoveryQueueItem struct {
	Run              model.CompilationRun         `json:"run"`
	Evidence         *CompilationRecoveryEvidence `json:"evidence"`
	UnknownSeconds   int64                        `json:"unknown_seconds"`
	UnknownAgeBucket string                       `json:"unknown_age_bucket"`
}

type CompilationRecoveryApprovalRequest struct {
	Action                    string `json:"action" binding:"required,oneof=confirm_downstream_absent create_replacement_run"`
	IdempotencyKey            string `json:"idempotency_key" binding:"required"`
	ObservationWindowSeconds  int64  `json:"observation_window_seconds" binding:"required,gt"`
	ObservationFirstMatchedAt string `json:"observation_first_matched_at" binding:"required"`
	Reason                    string `json:"reason"`
}

func decodeCompilationRecoveryEvidence(raw string) (*CompilationRecoveryEvidence, error) {
	evidence := &CompilationRecoveryEvidence{}
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return evidence, nil
	}
	if err := json.Unmarshal([]byte(raw), evidence); err != nil {
		return nil, httperr.Internal("decode compilation recovery evidence failed")
	}
	return evidence, nil
}

func encodeCompilationRecoveryEvidence(evidence *CompilationRecoveryEvidence) (string, error) {
	if evidence.BindingCandidates == nil {
		evidence.BindingCandidates = []string{}
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", httperr.Internal("encode compilation recovery evidence failed")
	}
	return string(encoded), nil
}

func compilationRecoveryHash(evidence *CompilationRecoveryEvidence) string {
	encoded, err := encodeCompilationRecoveryEvidence(evidence)
	if err != nil {
		return ""
	}
	hash, err := hashStableJSON(map[string]interface{}{"recovery_evidence": encoded})
	if err != nil {
		return ""
	}
	return hash
}

func (e *CompilationRecoveryEvidence) hasConsistentNoMatch(count int, firstAt *time.Time, lastAt *time.Time, now time.Time) bool {
	return e.NoMatch && count >= 2 && firstAt != nil && lastAt != nil &&
		now.Sub(*firstAt) <= model.CompilationRecoveryWindow &&
		!lastAt.Before(*firstAt)
}

func (s *Service) saveCompilationRecoveryEvidence(
	ctx context.Context, run *model.CompilationRun, evidence *CompilationRecoveryEvidence,
) error {
	now := time.Now().UTC()
	evidence.TenantID = run.TenantID
	evidence.DatasetID = run.DatasetID
	evidence.Kind = run.Kind
	evidence.QueriedAt = now
	evidence.ObservationWindowSeconds = int64(model.CompilationRecoveryWindow / time.Second)

	unknownAt := run.UnknownAt
	if unknownAt == nil {
		unknownAt = &now
	}
	firstAt := run.RecoveryFirstNoMatchAt
	lastAt := run.RecoveryLastNoMatchAt
	noMatchCount := run.RecoveryNoMatchCount
	if evidence.NoMatch {
		if firstAt == nil || now.Sub(*firstAt) > model.CompilationRecoveryWindow {
			firstAt = &now
			noMatchCount = 1
		} else {
			noMatchCount++
		}
		lastAt = &now
	} else {
		firstAt = nil
		lastAt = nil
		noMatchCount = 0
	}

	switch evidence.EvidenceState {
	case model.CompilationRecoveryWaitingDownstream:
		evidence.NoMatch = false
		firstAt = nil
		lastAt = nil
		noMatchCount = 0
	}

	encoded, err := encodeCompilationRecoveryEvidence(evidence)
	if err != nil {
		return err
	}
	recoveryState := model.CompilationRecoveryInsufficientEvidence
	if evidence.EvidenceState == model.CompilationRecoveryWaitingDownstream {
		recoveryState = model.CompilationRecoveryWaitingDownstream
	} else if evidence.hasConsistentNoMatch(noMatchCount, firstAt, lastAt, now) {
		recoveryState = model.CompilationRecoveryNeedsApproval
	}
	updates := map[string]interface{}{
		"state":                      model.CompilationRunUnknown,
		"recovery_state":             recoveryState,
		"recovery_evidence":          encoded,
		"unknown_at":                 unknownAt,
		"recovery_first_no_match_at": firstAt,
		"recovery_last_no_match_at":  lastAt,
		"recovery_no_match_count":    noMatchCount,
		"failure_code":               model.CompilationFailureUnknownState,
	}
	changed, err := s.Store.TransitionCompilationRun(ctx, run.TenantID, run.ID,
		[]string{model.CompilationRunUnknown}, updates,
	)
	if err != nil || !changed {
		return err
	}
	run.State = model.CompilationRunUnknown
	run.RecoveryState = recoveryState
	run.RecoveryEvidence = encoded
	run.UnknownAt = unknownAt
	run.RecoveryFirstNoMatchAt = firstAt
	run.RecoveryLastNoMatchAt = lastAt
	run.RecoveryNoMatchCount = noMatchCount
	run.FailureCode = model.CompilationFailureUnknownState
	run.UnknownAt = unknownAt
	return s.Store.CreateAudit(ctx, compilationRunAuditState(
		run, model.CompilationRunUnknown, "compilation_run.recovery_evidence", run.RequestedBy, now,
	))
}

func (s *Service) reconcileUnknownCompilationRun(
	ctx context.Context, run *model.CompilationRun, dataset *model.DatasetLink,
) error {
	now := time.Now().UTC()
	obs.Get().SetCompilationUnknownAge(run.TenantID, run.Kind, compilationUnknownAge(run, now))
	status, err := s.RAGFlow.GetCompilationStatusForKind(ctx, dataset.RAGFlowDatasetID, run.Kind)
	if err != nil {
		evidence := &CompilationRecoveryEvidence{EvidenceState: model.CompilationRecoveryInsufficientEvidence, ProbeError: err.Error()}
		if evidenceErr := s.saveCompilationRecoveryEvidence(ctx, run, evidence); evidenceErr != nil {
			return evidenceErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "status_probe_failed", nil, now, nil)
	}
	if !ragflow.IsKnownCompilationStatusState(status.State) {
		evidence := &CompilationRecoveryEvidence{
			Status: status, EvidenceState: model.CompilationRecoveryInsufficientEvidence,
			ProbeError: "unrecognized upstream state",
		}
		if evidenceErr := s.saveCompilationRecoveryEvidence(ctx, run, evidence); evidenceErr != nil {
			return evidenceErr
		}
		obs.Get().ClearCompilationUpstreamStallAge(run.TenantID, run.Kind)
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "upstream_state_unrecognized", map[string]interface{}{
			"upstream_state": status.State,
		}, now, nil)
	}

	if status.State == "pending" || status.State == "running" {
		evidence := &CompilationRecoveryEvidence{
			Status: status, EvidenceState: model.CompilationRecoveryWaitingDownstream,
			ManifestComplete: run.ManifestComplete,
		}
		if evidenceErr := s.saveCompilationRecoveryEvidence(ctx, run, evidence); evidenceErr != nil {
			return evidenceErr
		}
		if sourceErr := s.checkCompilationSourceDuringExecution(ctx, run, dataset); sourceErr != nil {
			return sourceErr
		}
		stallAge := compilationUpstreamStallAge(run, status, now)
		obs.Get().SetCompilationUpstreamStallAge(run.TenantID, run.Kind, stallAge)
		if stallAge >= compilationUpstreamStallThreshold.Seconds() {
			return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "upstream_stalled", map[string]interface{}{
				"upstream_state": status.State, "progress": status.Progress,
				"current_phase": status.CurrentPhase, "inflight": status.Inflight,
				"backlog": status.Backlog, "upstream_error": status.Error,
				"upstream_updated_at": status.UpdatedAt.UTC(), "upstream_age_seconds": stallAge,
				"stall_threshold_seconds": compilationUpstreamStallThreshold.Seconds(),
			}, now, nil)
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunDispatched, "upstream_running", map[string]interface{}{
			"upstream_state": status.State, "progress": status.Progress,
			"current_phase": status.CurrentPhase, "inflight": status.Inflight,
			"backlog": status.Backlog, "upstream_error": status.Error,
			"upstream_updated_at": status.UpdatedAt.UTC(), "upstream_age_seconds": stallAge,
		}, now, nil)
	}

	obs.Get().ClearCompilationUpstreamStallAge(run.TenantID, run.Kind)
	documentIDs, err := decodeCompilationRunDocumentIDs(run.DocumentIDs)
	if err != nil {
		return err
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		evidence := &CompilationRecoveryEvidence{
			Status: status, EvidenceState: model.CompilationRecoveryInsufficientEvidence,
			ProbeError: "source manifest unavailable",
		}
		if evidenceErr := s.saveCompilationRecoveryEvidence(ctx, run, evidence); evidenceErr != nil {
			return evidenceErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "source_manifest_probe_failed", nil, now, nil)
	}
	selected, err := selectCompilationDocuments(documents, documentIDs)
	if err != nil {
		if staleErr := s.markCompilationArtifactsStale(ctx, run); staleErr != nil {
			return staleErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunFailed, "source_manifest_changed_during_execution", map[string]interface{}{
			"source_consistency": model.CompilationSourceConsistencyChanged,
		}, now, nil)
	}
	currentManifest := compilationDocumentManifestHash(selected)
	if currentManifest != run.ManifestHash {
		if staleErr := s.markCompilationArtifactsStale(ctx, run); staleErr != nil {
			return staleErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunFailed, "source_manifest_changed_during_execution", map[string]interface{}{
			"source_consistency":            model.CompilationSourceConsistencyChanged,
			"post_run_source_manifest_hash": currentManifest,
		}, now, nil)
	}

	var bindingCandidates []string
	artifactTotal := int64(0)
	upstreamCompleted := compilationStatusCompletedAfterDispatch(run, status)
	if upstreamCompleted && status.State != "completed" {
		documents, sourceErr := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
		if sourceErr != nil || !compilationRunSourceDocumentsTerminal(run, documents) {
			evidence := &CompilationRecoveryEvidence{
				Status: status, EvidenceState: model.CompilationRecoveryWaitingDownstream,
				ManifestComplete: run.ManifestComplete,
			}
			if evidenceErr := s.saveCompilationRecoveryEvidence(ctx, run, evidence); evidenceErr != nil {
				return evidenceErr
			}
			return s.storeCompilationAudit(ctx, run, model.CompilationRunDispatched, "upstream_source_reparse_running", map[string]interface{}{
				"upstream_state": status.State,
			}, now, nil)
		}
	}
	if upstreamCompleted {
		artifacts, total, probeErr := s.RAGFlow.ListDatasetArtifacts(ctx, dataset.RAGFlowDatasetID, ragflow.DatasetArtifactFilter{
			Page: 1, PageSize: 100, PageType: run.Kind,
		})
		if probeErr != nil {
			evidence := &CompilationRecoveryEvidence{
				Status: status, EvidenceState: model.CompilationRecoveryInsufficientEvidence,
				ProbeError: "artifact list unavailable", ManifestComplete: compilationManifestComplete(selected),
			}
			if evidenceErr := s.saveCompilationRecoveryEvidence(ctx, run, evidence); evidenceErr != nil {
				return evidenceErr
			}
			return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "artifact_probe_failed", nil, now, nil)
		}
		artifactTotal = total
		for _, artifact := range artifacts {
			bindingCandidates = append(bindingCandidates, artifact.Slug)
		}
	}
	noMatch := (status.State == "idle" && !upstreamCompleted) || (upstreamCompleted && artifactTotal == 0)
	evidenceState := model.CompilationRecoveryInsufficientEvidence
	if noMatch {
		evidenceState = model.CompilationRecoveryNeedsApproval
	} else if artifactTotal > 0 {
		evidenceState = model.CompilationRecoveryWaitingDownstream
	}
	evidence := &CompilationRecoveryEvidence{
		Status: status, ArtifactTotal: artifactTotal, BindingCandidates: bindingCandidates,
		ManifestComplete: compilationManifestComplete(selected), NoMatch: noMatch,
		EvidenceState: evidenceState,
	}
	if evidenceErr := s.saveCompilationRecoveryEvidence(ctx, run, evidence); evidenceErr != nil {
		return evidenceErr
	}
	if upstreamCompleted && artifactTotal > 0 {
		return s.completeCompilationRun(ctx, run, dataset, status)
	}
	return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "upstream_idle_after_dispatch", map[string]interface{}{
		"upstream_state": status.State,
	}, now, nil)
}

func compilationUnknownAge(run *model.CompilationRun, now time.Time) float64 {
	reference := run.UnknownAt
	if reference == nil {
		reference = &run.UpdatedAt
	}
	age := now.Sub(*reference)
	if age < 0 {
		age = 0
	}
	return age.Seconds()
}

func (s *Service) ListCompilationRecovery(
	ctx context.Context, actorID, tenantID, datasetID string, page, pageSize int,
) ([]CompilationRecoveryQueueItem, int64, error) {
	datasetID = strings.TrimSpace(datasetID)
	if err := s.AuthorizeABAC(ctx, actorID, "read", "knowledge-ops", tenantID, "", ""); err != nil {
		return nil, 0, err
	}
	if datasetID != "" {
		dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
		if err != nil {
			return nil, 0, err
		}
		if dataset == nil {
			return nil, 0, httperr.NotFound("dataset not found")
		}
		if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, dataset.ProjectID); err != nil {
			return nil, 0, err
		}
	}
	runs, total, err := s.Store.ListCompilationRecoveryRuns(ctx, tenantID, datasetID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now().UTC()
	items := make([]CompilationRecoveryQueueItem, 0, len(runs))
	for _, run := range runs {
		if datasetID == "" {
			if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, run.ProjectID); err != nil {
				return nil, 0, err
			}
		}
		evidence, evidenceErr := decodeCompilationRecoveryEvidence(run.RecoveryEvidence)
		if evidenceErr != nil {
			return nil, 0, evidenceErr
		}
		seconds := int64(compilationUnknownAge(&run, now))
		bucket := "0-24h"
		if seconds >= 72*3600 {
			bucket = ">72h"
		} else if seconds >= 24*3600 {
			bucket = "24-72h"
		}
		items = append(items, CompilationRecoveryQueueItem{
			Run: run, Evidence: evidence, UnknownSeconds: seconds, UnknownAgeBucket: bucket,
		})
	}
	return items, total, nil
}

func (s *Service) ProbeCompilationRecoveryRun(ctx context.Context, actorID, tenantID, runID string) (*model.CompilationRun, error) {
	run, err := s.Store.GetCompilationRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("compilation run not found")
	}
	if err := s.AuthorizeABAC(ctx, actorID, "execute", "compilation", tenantID, run.ProjectID, run.RequestedBy); err != nil {
		return nil, err
	}
	if err := s.ProcessCompilationRuns(ctx, tenantID, run.DatasetID, ""); err != nil {
		return nil, err
	}
	return s.Store.GetCompilationRun(ctx, tenantID, runID)
}

func (s *Service) RequestCompilationRecoveryApproval(
	ctx context.Context, actorID, tenantID, runID string, request CompilationRecoveryApprovalRequest,
) (*model.Approval, error) {
	if request.Action != model.ApprovalActionConfirmDownstreamAbsent &&
		request.Action != model.ApprovalActionCreate {
		return nil, httperr.BadRequest(40152, "unsupported compilation recovery action")
	}
	if !s.currentApprovalConfig().Enabled {
		return nil, httperr.New(409, 40976, "approval workflow must be enabled for compilation recovery")
	}
	run, err := s.Store.GetCompilationRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("compilation run not found")
	}
	if err := s.AuthorizeABAC(ctx, actorID, "execute", "compilation", tenantID, run.ProjectID, ""); err != nil {
		return nil, err
	}
	evidence, err := decodeCompilationRecoveryEvidence(run.RecoveryEvidence)
	if err != nil {
		return nil, err
	}
	if err := validateCompilationRecoveryRequest(run, evidence, request); err != nil {
		return nil, err
	}
	firstAt := run.RecoveryFirstNoMatchAt.UTC().Format(time.RFC3339Nano)
	lastAt := run.RecoveryLastNoMatchAt.UTC().Format(time.RFC3339Nano)
	payload := map[string]any{
		"dataset_id": run.DatasetID, "old_run_id": run.ID, "kind": run.Kind,
		"evidence_hash": compilationRecoveryHash(evidence), "manifest_complete": run.ManifestComplete,
		"no_match_count": run.RecoveryNoMatchCount, "first_no_match_at": firstAt,
		"last_no_match_at": lastAt, "observation_window_seconds": request.ObservationWindowSeconds,
	}
	if request.Action == model.ApprovalActionCreate {
		confirmedApprovalID := run.ApprovalID
		if confirmedApprovalID == "" {
			return nil, httperr.New(409, 40976, "downstream absence has no approval evidence")
		}
		payload["confirmed_approval_id"] = confirmedApprovalID
		payload["profile_id"] = run.ProfileID
		payload["manifest_hash"] = run.ManifestHash
		payload["template_hash"] = run.TemplateHash
		payload["pipeline_hash"] = run.PipelineHash
		var documentIDs []string
		if err := json.Unmarshal([]byte(run.DocumentIDs), &documentIDs); err != nil {
			return nil, httperr.Internal("decode compilation document ids failed")
		}
		payload["document_ids"] = documentIDs
	}
	approval, err := s.SubmitApproval(ctx, actorID, ApprovalSubmitRequest{
		TenantID: tenantID, ObjectType: model.ApprovalObjectCompilationRun,
		Action: request.Action, ObjectID: run.ID, Title: "Compilation unknown recovery",
		Reason: request.Reason, Payload: payload, IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	if approval == nil {
		return nil, httperr.New(409, 40976, "compilation recovery approval policy is required")
	}
	recoveryState := model.CompilationRecoveryNeedsApproval
	if request.Action == model.ApprovalActionCreate {
		recoveryState = model.CompilationRecoveryReplacementPending
	}
	if _, err := s.Store.TransitionCompilationRun(ctx, tenantID, run.ID, []string{run.State}, map[string]interface{}{
		"approval_id": approval.ID, "recovery_state": recoveryState,
	}); err != nil {
		return nil, err
	}
	if err := s.Store.CreateAudit(ctx, &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
		UserID: actorID, Action: "compilation_run.recovery_approval_requested", Resource: "compilation",
		ResourceID: run.ID, ApprovalID: approval.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"run_id": run.ID, "approval_id": approval.ID, "action": request.Action,
			"recovery_state": recoveryState, "evidence_hash": compilationRecoveryHash(evidence),
		}), At: time.Now().UTC(), Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission:    "execute:compilation",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}); err != nil {
		return nil, err
	}
	return approval, nil
}

func validateCompilationRecoveryRequest(
	run *model.CompilationRun, evidence *CompilationRecoveryEvidence, request CompilationRecoveryApprovalRequest,
) error {
	if request.IdempotencyKey == "" || request.ObservationWindowSeconds <= 0 {
		return httperr.BadRequest(40152, "idempotency key and observation window are required")
	}
	firstAt := request.ObservationFirstMatchedAt
	if run.RecoveryFirstNoMatchAt == nil || firstAt != run.RecoveryFirstNoMatchAt.UTC().Format(time.RFC3339Nano) {
		return httperr.New(409, 40976, "recovery observation first match no longer exists")
	}
	if run.RecoveryNoMatchCount < 2 || run.RecoveryLastNoMatchAt == nil ||
		run.RecoveryLastNoMatchAt.Sub(*run.RecoveryFirstNoMatchAt) > model.CompilationRecoveryWindow ||
		!evidence.ManifestComplete || !evidence.NoMatch {
		return httperr.New(409, 40976, "two consistent no-match results are required")
	}
	if request.Action == model.ApprovalActionConfirmDownstreamAbsent &&
		(run.State != model.CompilationRunUnknown || run.RecoveryState != model.CompilationRecoveryNeedsApproval) {
		return httperr.New(409, 40976, "run is not ready for downstream absence confirmation")
	}
	if request.Action == model.ApprovalActionCreate &&
		(run.State != model.CompilationRunReconciled || run.FailureCode != model.CompilationFailureDownstreamNotFound) {
		return httperr.New(409, 40976, "downstream absence must be confirmed before replacement")
	}
	return nil
}

func approvalApproverID(ctx context.Context, store repository.Store, approval *model.Approval) (string, error) {
	steps, err := store.ListApprovalSteps(ctx, approval.TenantID, approval.ID)
	if err != nil {
		return "", err
	}
	for _, step := range steps {
		if step.ActedBy != nil && strings.TrimSpace(*step.ActedBy) != "" {
			return *step.ActedBy, nil
		}
	}
	return "", httperr.Internal("approval approver is missing")
}

func (s *Service) validateCompilationRecoveryApproval(ctx context.Context, approval *model.Approval) error {
	if approval.ObjectType != model.ApprovalObjectCompilationRun {
		return httperr.BadRequest(40152, "unexpected compilation recovery object")
	}
	if approval.Action != model.ApprovalActionConfirmDownstreamAbsent &&
		approval.Action != model.ApprovalActionCreate {
		return httperr.BadRequest(40152, "unexpected compilation recovery action")
	}
	run, err := s.Store.GetCompilationRun(ctx, approval.TenantID, approval.ObjectID)
	if err != nil {
		return err
	}
	if run == nil {
		return httperr.NotFound("compilation run not found")
	}
	if run.State == model.CompilationRunUnknown && run.RecoveryState != model.CompilationRecoveryNeedsApproval {
		return httperr.New(409, 40976, "recovery evidence is insufficient")
	}
	if approval.Action == model.ApprovalActionCreate {
		if run.State != model.CompilationRunReconciled || run.FailureCode != model.CompilationFailureDownstreamNotFound {
			return httperr.New(409, 40976, "downstream absence must be confirmed first")
		}
	}
	return nil
}

func (s *Service) executeCompilationRecoveryConfirm(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	run, err := s.Store.GetCompilationRun(ctx, approval.TenantID, approval.ObjectID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("compilation run not found")
	}
	if run.State != model.CompilationRunUnknown || run.RecoveryState != model.CompilationRecoveryNeedsApproval {
		return nil, httperr.New(409, 40976, "run is not ready for downstream absence confirmation")
	}
	evidence, err := decodeCompilationRecoveryEvidence(run.RecoveryEvidence)
	if err != nil {
		return nil, err
	}
	if !evidence.NoMatch || run.RecoveryNoMatchCount < 2 || run.RecoveryFirstNoMatchAt == nil || run.RecoveryLastNoMatchAt == nil {
		return nil, httperr.New(409, 40976, "two consistent no-match results are required")
	}
	approverID, err := approvalApproverID(ctx, s.Store, approval)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	observationSeconds := int64(run.RecoveryLastNoMatchAt.Sub(*run.RecoveryFirstNoMatchAt) / time.Second)
	evidence.EvidenceState = model.CompilationRecoveryConfirmed
	encodedEvidence, err := encodeCompilationRecoveryEvidence(evidence)
	if err != nil {
		return nil, err
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		changed, transitionErr := tx.TransitionCompilationRun(ctx, run.TenantID, run.ID,
			[]string{model.CompilationRunUnknown},
			map[string]interface{}{
				"state": model.CompilationRunReconciled, "recovery_state": model.CompilationRecoveryConfirmed,
				"failure_code": model.CompilationFailureDownstreamNotFound, "recovery_evidence": encodedEvidence,
				"approval_id": approval.ID, "recovery_decision_at": now,
				"recovery_observation_seconds": observationSeconds,
				"recovery_approved_by":         approverID, "recovery_operated_by": approval.RequesterID,
			},
		)
		if transitionErr != nil || !changed {
			return transitionErr
		}
		return tx.CreateAudit(ctx, &model.AuditLog{
			ID: id.New(), TenantID: run.TenantID, ActorTenantID: run.TenantID, TargetTenantID: run.TenantID,
			UserID: approverID, Action: "compilation_run.downstream_absent_confirmed", Resource: "compilation",
			ResourceID: run.ID, ApprovalID: approval.ID,
			DetailJSON: encodeAuditDetail(map[string]interface{}{
				"run_id": run.ID, "evidence_hash": compilationRecoveryHash(evidence),
				"no_match_count": run.RecoveryNoMatchCount, "observation_seconds": observationSeconds,
			}), At: now, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
			AuthorizationPermission:    "manage:approval",
			AuthorizationPolicyVersion: "explicit-rbac-v1",
		})
	})
	if err != nil {
		return nil, err
	}
	if err := s.releaseCompilationReservation(ctx, run); err != nil {
		obs.Get().IncEnhancementBudgetRejected(run.TenantID, "reservation_release_failed")
	}
	obs.Get().ClearCompilationUnknownAge(run.TenantID, run.Kind)
	return map[string]any{"run_id": run.ID, "recovery_state": model.CompilationRecoveryConfirmed}, nil
}

func (s *Service) executeCompilationRecoveryCreate(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	run, err := s.Store.GetCompilationRun(ctx, approval.TenantID, approval.ObjectID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("compilation run not found")
	}
	if run.State != model.CompilationRunReconciled || run.FailureCode != model.CompilationFailureDownstreamNotFound {
		return nil, httperr.New(409, 40976, "downstream absence must be confirmed first")
	}
	rawDocumentIDs, _ := payload["document_ids"].([]any)
	documentIDs := make([]string, 0, len(rawDocumentIDs))
	for _, item := range rawDocumentIDs {
		if value, ok := item.(string); ok {
			documentIDs = append(documentIDs, value)
		}
	}
	kind, _ := payload["kind"].(string)
	manifestHash, _ := payload["manifest_hash"].(string)
	templateHash, _ := payload["template_hash"].(string)
	pipelineHash, _ := payload["pipeline_hash"].(string)
	profileID, _ := payload["profile_id"].(string)
	approverID, err := approvalApproverID(ctx, s.Store, approval)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	observationSeconds := int64(run.RecoveryLastNoMatchAt.Sub(*run.RecoveryFirstNoMatchAt) / time.Second)
	newRun, err := s.TriggerCompilationRun(ctx, approval.TenantID, approval.RequesterID, run.DatasetID, CompilationRunTriggerInput{
		ProfileID: profileID, IdempotencyKey: "recovery-replacement-" + approval.ID,
		ManifestHash: manifestHash, TemplateHash: templateHash, PipelineHash: pipelineHash,
		Kind: kind, DocumentIDs: documentIDs,
		Recovery: &CompilationRunRecoveryContext{
			SourceRunID: run.ID, ApprovalID: approval.ID, Evidence: run.RecoveryEvidence,
			State: model.CompilationRecoveryReplacement, DecisionAt: &now,
			ObservationSeconds: int64(observationSeconds), ApprovedBy: approverID,
			OperatedBy: approval.RequesterID,
		},
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"new_run_id": newRun.ID, "old_run_id": run.ID}, nil
}

func compilationRecoveryHashFromRaw(raw string) string {
	hash, err := hashStableJSON(map[string]interface{}{"recovery_evidence": raw})
	if err != nil {
		return ""
	}
	return hash
}
