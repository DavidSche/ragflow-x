package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const knowledgeCompilationLiveContractPending = "phase0_live_contract_unverified"

type KnowledgeProfileInput struct {
	DatasetID   string `json:"dataset_id" binding:"required"`
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	ProfileType string `json:"profile_type" binding:"required"`
	Config      string `json:"config"`
	Status      string `json:"status"`
}

type CompilationRunTriggerInput struct {
	ProfileID      string                         `json:"profile_id"`
	IdempotencyKey string                         `json:"idempotency_key"`
	ManifestHash   string                         `json:"manifest_hash"`
	TemplateHash   string                         `json:"template_hash"`
	PipelineHash   string                         `json:"pipeline_hash"`
	Kind           string                         `json:"kind"`
	DocumentIDs    []string                       `json:"document_ids"`
	Config         string                         `json:"config"`
	Recovery       *CompilationRunRecoveryContext `json:"-"`
}

type CompilationRunRecoveryContext struct {
	SourceRunID        string
	ApprovalID         string
	Evidence           string
	State              string
	DecisionAt         *time.Time
	ObservationSeconds int64
	ApprovedBy         string
	OperatedBy         string
}

func buildKnowledgeProfile(
	tenantID, userID string, input KnowledgeProfileInput, now time.Time,
) (*model.KnowledgeProfile, error) {
	input.DatasetID = strings.TrimSpace(input.DatasetID)
	input.Name = strings.TrimSpace(input.Name)
	input.ProfileType = strings.TrimSpace(input.ProfileType)
	input.Status = strings.TrimSpace(input.Status)
	if input.DatasetID == "" || input.Name == "" || input.ProfileType == "" {
		return nil, httperr.BadRequest(40120, "dataset_id, name and profile_type are required")
	}
	if len(input.Name) > 128 || len(input.Description) > 512 {
		return nil, httperr.BadRequest(40120, "knowledge profile name or description is too long")
	}
	config := strings.TrimSpace(input.Config)
	if config == "" {
		config = "{}"
	}
	if !json.Valid([]byte(config)) || !strings.HasPrefix(config, "{") {
		return nil, httperr.BadRequest(40120, "config must be a JSON object")
	}
	if _, err := parseKnowledgeProfileEnhancementBudget(config); err != nil {
		return nil, httperr.BadRequest(40120, "knowledge profile enhancement budget is invalid")
	}
	status := model.KnowledgeProfileStatusActive
	if input.Status != "" {
		if input.Status != model.KnowledgeProfileStatusActive && input.Status != model.KnowledgeProfileStatusInactive {
			return nil, httperr.BadRequest(40120, "status must be active or inactive")
		}
		status = input.Status
	}
	return &model.KnowledgeProfile{
		ID: id.New(), TenantID: tenantID, DatasetID: input.DatasetID,
		Name: input.Name, Description: input.Description, ProfileType: input.ProfileType,
		Config: config, Status: status, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Service) validateKnowledgeProfileDataset(
	ctx context.Context, tenantID string, profile *model.KnowledgeProfile,
) error {
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, profile.DatasetID)
	if err != nil {
		return err
	}
	if dataset == nil || dataset.ID == "" {
		return httperr.BadRequest(40120, "dataset_id must reference a tenant dataset")
	}
	profile.ProjectID = dataset.ProjectID
	return nil
}

func (s *Service) CreateKnowledgeProfile(
	ctx context.Context, tenantID, userID string, input KnowledgeProfileInput,
) (*model.KnowledgeProfile, error) {
	profile, err := buildKnowledgeProfile(tenantID, userID, input, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.validateKnowledgeProfileDataset(ctx, tenantID, profile); err != nil {
		return nil, err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, profile.ProjectID); err != nil {
		return nil, err
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateKnowledgeProfile(ctx, profile); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForKnowledgeProfile(profile, "knowledge_profile.created", userID, profile.CreatedAt))
	})
	if err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *Service) ListKnowledgeProfiles(
	ctx context.Context, tenantID, actorID string,
	filter repository.KnowledgeProfileFilter, page, pageSize int,
) ([]model.KnowledgeProfile, int64, error) {
	filter.DatasetID = strings.TrimSpace(filter.DatasetID)
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	if filter.ProjectID != "" {
		if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, filter.ProjectID); err != nil {
			return nil, 0, err
		}
	}
	return s.Store.ListKnowledgeProfiles(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetKnowledgeProfile(
	ctx context.Context, tenantID, profileID string,
) (*model.KnowledgeProfile, error) {
	profile, err := s.Store.GetKnowledgeProfile(ctx, tenantID, profileID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, httperr.NotFound("knowledge profile not found")
	}
	return profile, nil
}

func (s *Service) UpdateKnowledgeProfile(
	ctx context.Context, tenantID, userID, profileID string, input KnowledgeProfileInput,
) (*model.KnowledgeProfile, error) {
	existing, err := s.GetKnowledgeProfile(ctx, tenantID, profileID)
	if err != nil {
		return nil, err
	}
	profile, err := buildKnowledgeProfile(tenantID, existing.CreatedBy, input, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.validateKnowledgeProfileDataset(ctx, tenantID, profile); err != nil {
		return nil, err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, profile.ProjectID); err != nil {
		return nil, err
	}
	profile.ID = existing.ID
	profile.CreatedBy = existing.CreatedBy
	profile.CreatedAt = existing.CreatedAt
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.UpdateKnowledgeProfile(ctx, profile); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForKnowledgeProfile(profile, "knowledge_profile.updated", userID, profile.UpdatedAt))
	}); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *Service) DeleteKnowledgeProfile(
	ctx context.Context, tenantID, userID, profileID string,
) error {
	profile, err := s.GetKnowledgeProfile(ctx, tenantID, profileID)
	if err != nil {
		return err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, profile.ProjectID); err != nil {
		return err
	}
	return s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.DeleteKnowledgeProfile(ctx, tenantID, profile.ID); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForKnowledgeProfile(profile, "knowledge_profile.deleted", userID, time.Now().UTC()))
	})
}

func (s *Service) TriggerCompilationRun(
	ctx context.Context, tenantID, userID, datasetID string, input CompilationRunTriggerInput,
) (*model.CompilationRun, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.ProfileID = strings.TrimSpace(input.ProfileID)
	input.Kind = normalizeCompilationKind(input.Kind)
	documentIDs, err := normalizeCompilationDocumentIDs(input.DocumentIDs)
	if err != nil {
		return nil, err
	}
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || len(documentIDs) == 0 {
		return nil, httperr.BadRequest(40146, "idempotency_key and at least one document_id are required")
	}
	config := strings.TrimSpace(input.Config)
	if config == "" {
		config = "{}"
	}
	if !json.Valid([]byte(config)) || !strings.HasPrefix(config, "{") {
		return nil, httperr.BadRequest(40120, "config must be a JSON object")
	}
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if dataset == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	tenant, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if input.ProfileID != "" {
		profile, err := s.GetKnowledgeProfile(ctx, tenantID, input.ProfileID)
		if err != nil {
			return nil, err
		}
		if profile.DatasetID != datasetID || profile.Status != model.KnowledgeProfileStatusActive {
			return nil, httperr.BadRequest(40120, "profile must be active and belong to the dataset")
		}
		if input.Kind == "" {
			input.Kind = normalizeCompilationKind(profile.ProfileType)
		}
	}
	if input.Kind == "" {
		input.Kind = model.CompilationKindWiki
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, dataset.ProjectID); err != nil {
		return nil, err
	}
	if existing, err := s.Store.GetCompilationRunByIdempotency(ctx, tenantID, datasetID, input.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50202, "ragflow list documents failed")
	}
	selected, err := selectCompilationDocuments(documents, documentIDs)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ManifestHash) == "" {
		input.ManifestHash = compilationDocumentManifestHash(selected)
	}
	if strings.TrimSpace(input.TemplateHash) == "" {
		input.TemplateHash = compilationTemplateHash(input.Kind, input.ProfileID)
	}
	if strings.TrimSpace(input.PipelineHash) == "" {
		input.PipelineHash = compilationPipelineHash(selected)
	}
	documentIDsJSON, err := json.Marshal(documentIDs)
	if err != nil {
		return nil, httperr.Internal("encode compilation document manifest failed")
	}
	now := time.Now().UTC()
	run := &model.CompilationRun{
		ID: id.New(), TenantID: tenantID, ProjectID: dataset.ProjectID,
		DatasetID: datasetID, ProfileID: input.ProfileID, Kind: input.Kind,
		IdempotencyKey: input.IdempotencyKey, State: model.CompilationRunAccepted,
		ManifestHash:      strings.TrimSpace(input.ManifestHash),
		TemplateHash:      strings.TrimSpace(input.TemplateHash),
		PipelineHash:      strings.TrimSpace(input.PipelineHash),
		DocumentIDs:       string(documentIDsJSON),
		ManifestComplete:  compilationManifestComplete(selected),
		SourceConsistency: model.CompilationSourceConsistencyUnverified,
		RequestedBy:       userID,
		Detail: compilationRunDetail(input.Kind, "accepted", map[string]interface{}{
			"dispatch_policy": "explicit_document_parse", "document_count": len(selected),
		}),
		CreatedAt: now, UpdatedAt: now,
	}
	var recovery *CompilationRunRecoveryContext
	if recovery = input.Recovery; recovery != nil {
		source, sourceErr := s.Store.GetCompilationRun(ctx, tenantID, recovery.SourceRunID)
		if sourceErr != nil {
			return nil, sourceErr
		}
		if source == nil || source.State != model.CompilationRunReconciled ||
			source.FailureCode != model.CompilationFailureDownstreamNotFound ||
			source.RecoveryEvidence != recovery.Evidence ||
			recovery.ApprovalID == "" || recovery.State != model.CompilationRecoveryReplacement ||
			recovery.DecisionAt == nil || strings.TrimSpace(recovery.Evidence) == "" ||
			strings.TrimSpace(recovery.ApprovedBy) == "" || strings.TrimSpace(recovery.OperatedBy) == "" {
			return nil, httperr.New(409, 40976, "invalid compilation recovery replacement context")
		}
		run.TriggeredByXRunID = recovery.SourceRunID
		run.RecoveryState = recovery.State
		run.RecoveryEvidence = recovery.Evidence
		run.ApprovalID = recovery.ApprovalID
		run.RecoveryDecisionAt = recovery.DecisionAt
		run.RecoveryObservationSeconds = recovery.ObservationSeconds
		run.RecoveryApprovedBy = recovery.ApprovedBy
		run.RecoveryOperatedBy = recovery.OperatedBy
	}
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		profileConfig := "{}"
		if input.ProfileID != "" {
			profile, profileErr := tx.GetKnowledgeProfile(ctx, tenantID, input.ProfileID)
			if profileErr != nil {
				return profileErr
			}
			if profile != nil {
				profileConfig = profile.Config
			}
		}
		if _, reserveErr := s.reserveCompilationBudget(
			ctx, tx, run, tenant, dataset, profileConfig, len(selected),
		); reserveErr != nil {
			return reserveErr
		}
		if err := tx.CreateCompilationRun(ctx, run); err != nil {
			return err
		}
		if err := tx.CreateAudit(ctx, auditForCompilationRun(run, "compilation_run.accepted", userID, now)); err != nil {
			return err
		}
		if recovery != nil {
			changed, transitionErr := tx.TransitionCompilationRun(ctx, tenantID, recovery.SourceRunID,
				[]string{model.CompilationRunReconciled},
				map[string]interface{}{
					"recovery_state": recovery.State, "updated_at": now,
				},
			)
			if transitionErr != nil || !changed {
				return transitionErr
			}
			return tx.CreateAudit(ctx, &model.AuditLog{
				ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID, TargetTenantID: tenantID,
				UserID: recovery.ApprovedBy, Action: "compilation_run.replacement_created", Resource: "compilation",
				ResourceID: run.ID, ApprovalID: recovery.ApprovalID,
				DetailJSON: encodeAuditDetail(map[string]interface{}{
					"new_run_id": run.ID, "old_run_id": recovery.SourceRunID,
					"evidence_hash":       compilationRecoveryHashFromRaw(recovery.Evidence),
					"observation_seconds": recovery.ObservationSeconds,
				}), At: now, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
				AuthorizationPermission:    "manage:approval",
				AuthorizationPolicyVersion: "explicit-rbac-v1",
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if _, err := s.ScheduleCompilationReconcile(ctx, tenantID, ""); err != nil {
		_ = s.releaseCompilationReservation(ctx, run)
		return nil, err
	}
	return run, nil
}

func (s *Service) ListCompilationRuns(
	ctx context.Context, tenantID, userID, datasetID string, filter repository.CompilationRunFilter, page, pageSize int,
) ([]model.CompilationRun, int64, error) {
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, 0, err
	}
	if dataset == nil {
		return nil, 0, httperr.NotFound("dataset not found")
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, dataset.ProjectID); err != nil {
		return nil, 0, err
	}
	if datasetID != "" {
		filter.DatasetID = datasetID
	}
	return s.Store.ListCompilationRuns(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) ListArtifactProjections(
	ctx context.Context, tenantID, userID, datasetID, runID string, page, pageSize int,
) ([]model.ArtifactProjection, int64, error) {
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, 0, err
	}
	if dataset == nil {
		return nil, 0, httperr.NotFound("dataset not found")
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, dataset.ProjectID); err != nil {
		return nil, 0, err
	}
	return s.Store.ListArtifactProjections(ctx, tenantID, datasetID, runID, page, pageSize)
}

func auditForKnowledgeProfile(
	profile *model.KnowledgeProfile, action, userID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: profile.TenantID, ActorTenantID: profile.TenantID,
		TargetTenantID: profile.TenantID, UserID: userID, Action: action,
		Resource: "knowledge-profile", ResourceID: profile.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"profile_id": profile.ID, "dataset_id": profile.DatasetID,
			"profile_type": profile.ProfileType, "status": profile.Status,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission:    "manage:knowledge-profile",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

func auditForCompilationRun(
	run *model.CompilationRun, action, userID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: run.TenantID, ActorTenantID: run.TenantID,
		TargetTenantID: run.TenantID, UserID: userID, Action: action,
		Resource: "compilation", ResourceID: run.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"run_id": run.ID, "dataset_id": run.DatasetID, "state": run.State,
			"dispatch_policy": "fail_closed", "reason": knowledgeCompilationLiveContractPending,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission:    "manage:compilation",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}
