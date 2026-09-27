package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type DuplicateCandidateInput struct {
	SourceType        string                 `json:"source_type"`
	SourceID          string                 `json:"source_id"`
	SourceVersion     string                 `json:"source_version"`
	CandidateType     string                 `json:"candidate_type"`
	CandidateID       string                 `json:"candidate_id"`
	CandidateVersion  string                 `json:"candidate_version"`
	SimilarityScore   float64                `json:"similarity_score"`
	SimilarityReasons []string               `json:"similarity_reasons"`
	Evidence          map[string]interface{} `json:"evidence"`
}

type DuplicateCandidateDecision struct {
	FinalRelation string `json:"final_relation"`
	DecisionNote  string `json:"decision_note"`
}

func resolveImpactLevel(facts *model.KnowledgeImpactFacts) string {
	switch {
	case facts.AffectedAssistantCount >= 3 || facts.ProductionUsageCount >= 5000:
		return model.ImpactLevelCritical
	case facts.AffectedAssistantCount >= 1 || facts.AffectedEvalSetCount >= 1 || facts.HistoricalBadcaseCount >= 5:
		return model.ImpactLevelHigh
	case facts.HistoricalBadcaseCount >= 1 || facts.ProductionUsageCount >= 100:
		return model.ImpactLevelMedium
	default:
		return model.ImpactLevelLow
	}
}

func (s *Service) CreateKnowledgeImpactReport(ctx context.Context, actorID, tenantID, projectID, targetType, targetID, targetVersion string) (*model.ImpactReport, error) {
	if err := s.Authorize(ctx, actorID, "manage", "knowledge-ops"); err != nil {
		return nil, err
	}
	if targetType != "dataset" || strings.TrimSpace(targetID) == "" {
		return nil, httperr.BadRequest(40140, "dataset impact target is required")
	}
	facts, err := s.Store.GetKnowledgeImpactFacts(ctx, tenantID, targetID)
	if err != nil {
		return nil, err
	}
	facts.AffectedTenantCount = 1
	affectedAssistants, affectedReleases, affectedSets, affectedBadcases, err := s.Store.GetKnowledgeImpactAffectedLists(ctx, tenantID, targetID)
	if err != nil {
		return nil, err
	}
	impactLevel := resolveImpactLevel(facts)
	dimensions := map[string]interface{}{
		"production_usage": map[string]int64{"usage_count": facts.ProductionUsageCount},
		"assistant_scope":  map[string]int64{"assistant_count": facts.AffectedAssistantCount},
		"evaluation_scope": map[string]int64{"evaluation_set_count": facts.AffectedEvalSetCount},
		"badcase_scope":    map[string]int64{"badcase_count": facts.HistoricalBadcaseCount},
		"tenant_scope":     map[string]int64{"tenant_count": facts.AffectedTenantCount},
	}
	marshal := func(value interface{}) (string, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	dimensionsJSON, err := marshal(dimensions)
	if err != nil {
		return nil, err
	}
	metricsJSON, err := marshal(facts)
	if err != nil {
		return nil, err
	}
	assistantsJSON, err := marshal(affectedAssistants)
	if err != nil {
		return nil, err
	}
	releasesJSON, err := marshal(affectedReleases)
	if err != nil {
		return nil, err
	}
	evalSetsJSON, err := marshal(affectedSets)
	if err != nil {
		return nil, err
	}
	badcasesJSON, err := marshal(affectedBadcases)
	if err != nil {
		return nil, err
	}
	report := &model.ImpactReport{
		TenantID: tenantID, ProjectID: projectID, TargetType: targetType,
		TargetID: targetID, TargetVersion: targetVersion,
		ImpactPolicyVersion: model.ImpactPolicyVersion, ImpactLevel: impactLevel,
		ImpactDimensionsJSON: dimensionsJSON, MetricsJSON: metricsJSON,
		AffectedAssistantsJSON: assistantsJSON, AffectedReleasesJSON: releasesJSON,
		AffectedEvalSetsJSON: evalSetsJSON, AffectedBadcasesJSON: badcasesJSON,
		CreatedBy: actorID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateImpactReport(ctx, report); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Service) ListKnowledgeImpactReports(ctx context.Context, actorID, tenantID string, page, pageSize int, filter repository.KnowledgeImpactFilter) ([]model.ImpactReport, int64, error) {
	if err := s.Authorize(ctx, actorID, "read", "knowledge-ops"); err != nil {
		return nil, 0, err
	}
	return s.Store.ListImpactReports(ctx, tenantID, page, pageSize, filter)
}

func (s *Service) CreateDuplicateCandidate(ctx context.Context, actorID, tenantID, projectID string, input DuplicateCandidateInput) (*model.DuplicateCandidate, error) {
	if err := s.Authorize(ctx, actorID, "manage", "knowledge-ops"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.SourceType) == "" || strings.TrimSpace(input.SourceID) == "" ||
		strings.TrimSpace(input.CandidateType) == "" || strings.TrimSpace(input.CandidateID) == "" ||
		input.SimilarityScore < 0 || input.SimilarityScore > 1 || len(input.SimilarityReasons) == 0 {
		return nil, httperr.BadRequest(40141, "duplicate source, candidate and at least one reason are required")
	}
	if input.SourceType == input.CandidateType && input.SourceID == input.CandidateID {
		return nil, httperr.BadRequest(40141, "source and candidate must differ")
	}
	reasons, err := json.Marshal(input.SimilarityReasons)
	if err != nil {
		return nil, err
	}
	evidence, err := json.Marshal(input.Evidence)
	if err != nil {
		return nil, err
	}
	candidate := &model.DuplicateCandidate{
		TenantID: tenantID, ProjectID: projectID,
		SourceType: input.SourceType, SourceID: input.SourceID, SourceVersion: input.SourceVersion,
		CandidateType: input.CandidateType, CandidateID: input.CandidateID, CandidateVersion: input.CandidateVersion,
		SimilarityScore: input.SimilarityScore, SimilarityReasonsJSON: string(reasons),
		EvidenceJSON: string(evidence), Status: model.DuplicateCandidatePending,
		CreatedBy: actorID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateDuplicateCandidate(ctx, candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (s *Service) DecideDuplicateCandidate(ctx context.Context, actorID, tenantID, candidateID string, input DuplicateCandidateDecision) (*model.DuplicateCandidate, error) {
	if err := s.Authorize(ctx, actorID, "manage", "knowledge-ops"); err != nil {
		return nil, err
	}
	switch input.FinalRelation {
	case model.DuplicateRelationDuplicate, model.DuplicateRelationSupersedes,
		model.DuplicateRelationConflicts, model.DuplicateRelationRelated:
	default:
		return nil, httperr.BadRequest(40142, "relation must be duplicate, supersedes, conflicts or related")
	}
	if strings.TrimSpace(input.DecisionNote) == "" {
		return nil, httperr.BadRequest(40142, "decision note is required")
	}
	now := time.Now().UTC()
	updated, err := s.Store.UpdateDuplicateCandidate(ctx, tenantID, candidateID, map[string]interface{}{
		"status": model.DuplicateCandidateConfirmed, "final_relation": input.FinalRelation,
		"decision_by": actorID, "decision_at": now, "decision_note": strings.TrimSpace(input.DecisionNote),
	})
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, httperr.NotFound("pending duplicate candidate not found")
	}
	candidate, err := s.Store.GetDuplicateCandidate(ctx, tenantID, candidateID)
	if err != nil {
		return nil, err
	}
	if input.FinalRelation == model.DuplicateRelationSupersedes {
		if err := s.retireDuplicateSource(ctx, actorID, tenantID, candidate); err != nil {
			return nil, err
		}
	}
	return candidate, nil
}

func (s *Service) retireDuplicateSource(ctx context.Context, actorID, tenantID string, candidate *model.DuplicateCandidate) error {
	switch strings.ToLower(strings.TrimSpace(candidate.SourceType)) {
	case "dataset":
		dataset, err := s.Store.GetDatasetLink(ctx, tenantID, candidate.SourceID)
		if err != nil {
			return err
		}
		if dataset == nil {
			return httperr.NotFound("source dataset not found")
		}
		reviewedAt := time.Now().UTC()
		_, err = s.UpdateKnowledgeLifecycle(ctx, tenantID, candidate.SourceID, DatasetLifecycleInput{
			OwnerID: dataset.OwnerID, OwnerTeamID: dataset.OwnerTeamID,
			SourceType: dataset.SourceType, BusinessDomain: dataset.BusinessDomain,
			Sensitivity: dataset.Sensitivity, EffectiveAt: dataset.EffectiveAt,
			ExpiresAt: dataset.ExpiresAt, LastReviewedAt: &reviewedAt,
			ReviewStatus: model.KnowledgeReviewExpired, QualityScore: dataset.QualityScore,
		})
		return err
	case "document":
		var evidence map[string]interface{}
		if err := json.Unmarshal([]byte(candidate.EvidenceJSON), &evidence); err != nil {
			return err
		}
		datasetID := stringFromAny(evidence["dataset_id"])
		if datasetID == "" {
			datasetID = stringFromAny(evidence["datasetId"])
		}
		if datasetID == "" {
			return httperr.BadRequest(40143, "document retirement requires dataset_id evidence")
		}
		dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
		if err != nil {
			return err
		}
		if dataset == nil {
			return httperr.NotFound("source dataset not found")
		}
		return s.SetDocumentsStatus(ctx, tenantID, datasetID, []string{candidate.SourceID}, false)
	default:
		return httperr.BadRequest(40143, "supersedes retirement supports dataset or document sources")
	}
}

func (s *Service) ListDuplicateCandidates(ctx context.Context, actorID, tenantID string, page, pageSize int, filter repository.KnowledgeImpactFilter) ([]model.DuplicateCandidate, int64, error) {
	if err := s.Authorize(ctx, actorID, "read", "knowledge-ops"); err != nil {
		return nil, 0, err
	}
	return s.Store.ListDuplicateCandidates(ctx, tenantID, page, pageSize, filter)
}
