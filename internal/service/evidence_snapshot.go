package service

import (
	"context"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	evidenceMaxDatasetIDs   = 50
	evidenceMaxDependencies = 200
)

type EvidenceDependencyInput struct {
	LogicalDocumentID string `json:"logical_document_id"`
	BindingType       string `json:"binding_type"`
}

type EvidenceSnapshotInput struct {
	EvalCaseID                 string                    `json:"eval_case_id"`
	DatasetIDs                 []string                  `json:"dataset_ids"`
	Dependencies               []EvidenceDependencyInput `json:"dependencies"`
	RetrievalPolicyVersion     string                    `json:"retrieval_policy_version"`
	PromptVersion              string                    `json:"prompt_version"`
	ModelVersion               string                    `json:"model_version"`
	ParserPolicyVersion        string                    `json:"parser_policy_version"`
	ToolPolicyVersion          string                    `json:"tool_policy_version"`
	AuthorizationPolicyVersion string                    `json:"authorization_policy_version"`
}

type EvidenceSnapshotResult struct {
	Snapshot     *model.EvidenceSnapshot
	Evidence     *model.EvalCaseEvidence
	Dependencies []model.EvalCaseDependency
}

func (s *Service) CreateEvidenceSnapshot(
	ctx context.Context, tenantID, userID string, input EvidenceSnapshotInput,
) (*EvidenceSnapshotResult, error) {
	bundle, err := s.UpdateEvalCaseEvidence(ctx, tenantID, userID, input.EvalCaseID, input)
	if err != nil {
		return nil, err
	}
	return &EvidenceSnapshotResult{
		Snapshot: bundle.Snapshot, Evidence: bundle.Evidence, Dependencies: bundle.Dependencies,
	}, nil
}

func (s *Service) GetEvidenceSnapshot(
	ctx context.Context, tenantID, actorID, snapshotID string,
) (*repository.EvidenceSnapshotBundle, error) {
	tenantID = strings.TrimSpace(tenantID)
	snapshotID = strings.TrimSpace(snapshotID)
	actorID = strings.TrimSpace(actorID)
	if tenantID == "" || actorID == "" || snapshotID == "" {
		return nil, httperr.BadRequest(40090, "tenant, actor and snapshot id are required")
	}
	bundle, err := s.Store.GetEvidenceSnapshotBundle(ctx, tenantID, snapshotID)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, httperr.NotFound("evidence snapshot not found")
	}
	projectIDs, scoped, err := s.teamAdminOwnedProjectIDs(ctx, tenantID, actorID)
	if err != nil {
		return nil, err
	}
	if scoped {
		snapshotDatasetIDs, datasetErr := s.Store.ListEvidenceSnapshotDatasetIDs(ctx, tenantID, snapshotID)
		if datasetErr != nil {
			return nil, datasetErr
		}
		if len(snapshotDatasetIDs) == 0 {
			return nil, httperr.Forbidden("evidence snapshot is outside owned teams")
		}
		datasetIDs, datasetErr := s.Store.ListDatasetIDsByProjectIDs(ctx, tenantID, projectIDs)
		if datasetErr != nil {
			return nil, datasetErr
		}
		allowed := make(map[string]struct{}, len(datasetIDs))
		for _, datasetID := range datasetIDs {
			allowed[datasetID] = struct{}{}
		}
		for _, datasetID := range snapshotDatasetIDs {
			if _, exists := allowed[datasetID]; !exists {
				return nil, httperr.Forbidden("evidence snapshot is outside owned teams")
			}
		}
	}
	return bundle, nil
}

func (s *Service) ListEvidenceSnapshots(
	ctx context.Context, tenantID, actorID string, filter repository.EvidenceSnapshotFilter, page, pageSize int,
) ([]repository.EvidenceSnapshotListItem, int64, error) {
	tenantID = strings.TrimSpace(tenantID)
	actorID = strings.TrimSpace(actorID)
	if tenantID == "" || actorID == "" {
		return nil, 0, httperr.BadRequest(40090, "tenant and actor are required")
	}
	if page < 1 || pageSize < 1 || pageSize > 200 {
		return nil, 0, httperr.BadRequest(40090, "page must be at least 1 and page_size must be between 1 and 200")
	}
	filter.EvalCaseID = strings.TrimSpace(filter.EvalCaseID)
	filter.SnapshotHash = strings.TrimSpace(filter.SnapshotHash)
	filter.StaleStatus = strings.TrimSpace(filter.StaleStatus)
	if filter.StaleStatus != "" && filter.StaleStatus != model.EvidenceStatusFresh &&
		filter.StaleStatus != model.EvidenceStatusStale && filter.StaleStatus != model.EvidenceStatusRevalidated {
		return nil, 0, httperr.BadRequest(40090, "stale_status must be fresh, stale or revalidated")
	}
	projectIDs, scoped, err := s.teamAdminOwnedProjectIDs(ctx, tenantID, actorID)
	if err != nil {
		return nil, 0, err
	}
	if scoped {
		datasetIDs, datasetErr := s.Store.ListDatasetIDsByProjectIDs(ctx, tenantID, projectIDs)
		if datasetErr != nil {
			return nil, 0, datasetErr
		}
		if len(datasetIDs) == 0 {
			return []repository.EvidenceSnapshotListItem{}, 0, nil
		}
		filter.DatasetIDs = datasetIDs
	}
	return s.Store.ListEvidenceSnapshots(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetEvalCaseEvidence(
	ctx context.Context, tenantID, evalCaseID string,
) (*repository.EvidenceSnapshotBundle, error) {
	tenantID = strings.TrimSpace(tenantID)
	evalCaseID = strings.TrimSpace(evalCaseID)
	if tenantID == "" || evalCaseID == "" {
		return nil, httperr.BadRequest(40090, "tenant and eval case id are required")
	}
	evalCase, err := s.Store.GetEvalCase(ctx, tenantID, evalCaseID)
	if err != nil {
		return nil, err
	}
	if evalCase == nil {
		return nil, httperr.NotFound("eval case not found")
	}
	bundle, err := s.Store.GetEvalCaseEvidenceBundle(ctx, tenantID, evalCaseID)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, httperr.NotFound("eval case evidence not found")
	}
	return bundle, nil
}

func (s *Service) UpdateEvalCaseEvidence(
	ctx context.Context, tenantID, userID, evalCaseID string, input EvidenceSnapshotInput,
) (*repository.EvidenceSnapshotBundle, error) {
	tenantID = strings.TrimSpace(tenantID)
	evalCaseID = strings.TrimSpace(evalCaseID)
	if tenantID == "" || evalCaseID == "" {
		return nil, httperr.BadRequest(40090, "tenant and eval case id are required")
	}
	input.EvalCaseID = evalCaseID
	evalCase, err := s.Store.GetEvalCase(ctx, tenantID, evalCaseID)
	if err != nil {
		return nil, err
	}
	if evalCase == nil {
		return nil, httperr.NotFound("eval case not found")
	}
	current, err := s.Store.GetEvalCaseEvidenceBundle(ctx, tenantID, evalCaseID)
	if err != nil {
		return nil, err
	}
	datasetIDs, datasetIDsJSON, err := normalizeEvidenceDatasetIDs(input.DatasetIDs)
	if err != nil {
		return nil, err
	}
	dependencies, dependencySet, err := s.normalizeEvidenceDependencies(ctx, tenantID, input.Dependencies)
	if err != nil {
		return nil, err
	}
	policy, err := normalizeEvidencePolicyVersions(input)
	if err != nil {
		return nil, err
	}
	hash := evidenceSnapshotHash(datasetIDs, dependencies, policy)
	if current != nil && current.Snapshot.SnapshotHash == hash {
		return current, nil
	}
	now := time.Now().UTC()
	snapshot := &model.EvidenceSnapshot{
		ID: id.New(), TenantID: tenantID, SnapshotHash: hash,
		DatasetIDs: datasetIDsJSON, DocumentVersions: dependencySet,
		RetrievalPolicyVersion: policy.retrieval, PromptVersion: policy.prompt,
		ModelVersion: policy.model, ParserPolicyVersion: policy.parser,
		ToolPolicyVersion: policy.tool, AuthorizationPolicyVersion: policy.authorization,
		CreatedAt: now,
	}
	evidence := &model.EvalCaseEvidence{
		ID: id.New(), EvalCaseID: evalCaseID, TenantID: tenantID,
		EvidenceSnapshotID: snapshot.ID, DependencySet: dependencySet,
		StaleStatus: model.EvidenceStatusFresh, CreatedAt: now,
	}
	for index := range dependencies {
		dependencies[index].EvalCaseEvidenceID = evidence.ID
	}
	previousSnapshotID := ""
	if current != nil {
		previousSnapshotID = current.Snapshot.ID
	}
	audit := auditForEvalCaseEvidenceUpdate(
		snapshot, datasetIDs, dependencies, evidence, userID, previousSnapshotID, now,
	)
	if err := s.Store.CreateOrReplaceEvidenceSnapshotBundle(
		ctx, snapshot, evidence, dependencies, audit,
	); err != nil {
		return nil, err
	}
	return &repository.EvidenceSnapshotBundle{
		Snapshot: snapshot, Evidence: evidence, Dependencies: dependencies,
	}, nil
}

func (s *Service) RevalidateEvalCaseEvidence(
	ctx context.Context, tenantID, userID, evalCaseID string,
) (*repository.EvidenceSnapshotBundle, error) {
	current, err := s.GetEvalCaseEvidence(ctx, tenantID, evalCaseID)
	if err != nil {
		return nil, err
	}
	if current.Evidence.StaleStatus != model.EvidenceStatusStale {
		return nil, httperr.New(409, 40996, "only stale eval case evidence can be revalidated")
	}
	now := time.Now().UTC()
	audit := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, ActorTenantID: tenantID,
		TargetTenantID: tenantID, UserID: userID, Action: "eval_case_evidence.revalidated",
		Resource: "eval-set", ResourceID: current.Evidence.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"eval_case_id": current.Evidence.EvalCaseID,
			"snapshot_id":  current.Snapshot.ID,
		}), At: now,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		updated, updateErr := tx.UpdateEvalCaseEvidenceTransition(
			ctx, tenantID, current.Evidence.ID, model.EvidenceStatusStale,
			model.EvidenceStatusRevalidated, &now,
		)
		if updateErr != nil {
			return updateErr
		}
		if !updated {
			return httperr.New(409, 40996, "eval case evidence state changed")
		}
		return tx.CreateAudit(ctx, audit)
	})
	if err != nil {
		return nil, err
	}
	return s.GetEvalCaseEvidence(ctx, tenantID, evalCaseID)
}

func normalizeEvidenceDatasetIDs(values []string) ([]string, string, error) {
	if len(values) == 0 || len(values) > evidenceMaxDatasetIDs {
		return nil, "", httperr.BadRequest(40090, "dataset_ids must contain between 1 and 50 items")
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 64 {
			return nil, "", httperr.BadRequest(40090, "dataset_ids entries must be non-empty strings of at most 64 characters")
		}
		if _, exists := seen[value]; exists {
			return nil, "", httperr.BadRequest(40090, "dataset_ids entries must be unique")
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	encoded, err := evidenceCanonicalJSON(normalized)
	if err != nil {
		return nil, "", err
	}
	return normalized, encoded, nil
}

func (s *Service) normalizeEvidenceDependencies(
	ctx context.Context, tenantID string, inputs []EvidenceDependencyInput,
) ([]model.EvalCaseDependency, string, error) {
	if len(inputs) == 0 || len(inputs) > evidenceMaxDependencies {
		return nil, "", httperr.BadRequest(40090, "dependencies must contain between 1 and 200 items")
	}
	dependencies := make([]model.EvalCaseDependency, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, item := range inputs {
		logicalDocumentID := strings.TrimSpace(item.LogicalDocumentID)
		bindingType := strings.TrimSpace(item.BindingType)
		if logicalDocumentID == "" || len(logicalDocumentID) > 32 {
			return nil, "", httperr.BadRequest(40090, "dependencies[].logical_document_id is invalid")
		}
		if bindingType != model.EvidenceBindingRetrieval && bindingType != model.EvidenceBindingManual {
			return nil, "", httperr.BadRequest(40090, "dependencies[].binding_type must be retrieval_binding or manual_binding")
		}
		key := logicalDocumentID + "|" + bindingType
		if _, exists := seen[key]; exists {
			return nil, "", httperr.BadRequest(40090, "dependencies must not contain duplicate logical document bindings")
		}
		document, err := s.Store.GetLogicalDocument(ctx, tenantID, logicalDocumentID)
		if err != nil {
			return nil, "", err
		}
		if document == nil {
			return nil, "", httperr.NotFound("logical document not found")
		}
		seen[key] = struct{}{}
		dependencies = append(dependencies, model.EvalCaseDependency{
			ID: id.New(), TenantID: tenantID, LogicalDocumentID: logicalDocumentID,
			BindingType: bindingType,
		})
	}
	encoded, err := evidenceCanonicalJSON(evidenceDependencyValue(dependencies))
	if err != nil {
		return nil, "", err
	}
	return dependencies, encoded, nil
}

type evidencePolicyVersions struct {
	retrieval     string
	prompt        string
	model         string
	parser        string
	tool          string
	authorization string
}

func normalizeEvidencePolicyVersions(input EvidenceSnapshotInput) (*evidencePolicyVersions, error) {
	policy := &evidencePolicyVersions{
		retrieval:     strings.TrimSpace(input.RetrievalPolicyVersion),
		prompt:        strings.TrimSpace(input.PromptVersion),
		model:         strings.TrimSpace(input.ModelVersion),
		parser:        strings.TrimSpace(input.ParserPolicyVersion),
		tool:          strings.TrimSpace(input.ToolPolicyVersion),
		authorization: strings.TrimSpace(input.AuthorizationPolicyVersion),
	}
	for _, value := range []string{policy.retrieval, policy.prompt, policy.model, policy.parser, policy.tool, policy.authorization} {
		if value == "" {
			return nil, httperr.BadRequest(40090, "all evidence policy versions are required")
		}
	}
	if len(policy.model) > 128 {
		return nil, httperr.BadRequest(40090, "model_version must be at most 128 characters")
	}
	for _, value := range []string{policy.retrieval, policy.prompt, policy.parser, policy.tool, policy.authorization} {
		if len(value) > 64 {
			return nil, httperr.BadRequest(40090, "policy versions must be at most 64 characters")
		}
	}
	return policy, nil
}

func evidenceDependencyValue(dependencies []model.EvalCaseDependency) []map[string]string {
	values := make([]map[string]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		values = append(values, map[string]string{
			"binding_type":        dependency.BindingType,
			"logical_document_id": dependency.LogicalDocumentID,
		})
	}
	return values
}

func evidenceCanonicalJSON(value interface{}) (string, error) {
	return canonicalJSON(value)
}

func evidenceSnapshotHash(
	datasetIDs []string, dependencies []model.EvalCaseDependency, policy *evidencePolicyVersions,
) string {
	return canonicalJSONHash(map[string]interface{}{
		"schema":                       "evidence_snapshot_v1",
		"dataset_ids":                  datasetIDs,
		"document_versions":            evidenceDependencyValue(dependencies),
		"retrieval_policy_version":     policy.retrieval,
		"prompt_version":               policy.prompt,
		"model_version":                policy.model,
		"parser_policy_version":        policy.parser,
		"tool_policy_version":          policy.tool,
		"authorization_policy_version": policy.authorization,
	})
}

func auditForEvalCaseEvidenceUpdate(
	snapshot *model.EvidenceSnapshot, datasetIDs []string,
	dependencies []model.EvalCaseDependency, evidence *model.EvalCaseEvidence,
	userID, previousSnapshotID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: evidence.TenantID, ActorTenantID: evidence.TenantID,
		TargetTenantID: evidence.TenantID, UserID: userID, Action: "eval_case_evidence.updated",
		Resource: "eval-set", ResourceID: evidence.EvalCaseID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"previous_snapshot_id": previousSnapshotID,
			"snapshot_id":          snapshot.ID,
			"dataset_count":        len(datasetIDs),
			"dependency_count":     len(dependencies),
			"snapshot_hash":        snapshot.SnapshotHash,
		}), At: at,
		Result: "SUCCESS", AuthorizationDecision: "ALLOW", AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}
