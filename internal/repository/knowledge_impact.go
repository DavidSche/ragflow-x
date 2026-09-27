package repository

import (
	"context"
	"time"

	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type KnowledgeImpactFilter struct {
	Status        string
	Relation      string
	TargetType    string
	TargetID      string
	SourceType    string
	SourceID      string
	CandidateType string
	CandidateID   string
}

type KnowledgeImpactRepo interface {
	GetKnowledgeImpactFacts(ctx context.Context, tenantID, datasetID string) (*model.KnowledgeImpactFacts, error)
	GetKnowledgeImpactAffectedLists(ctx context.Context, tenantID, datasetID string) (assistants, releases, evalSets, badcases []string, err error)
	CreateImpactReport(ctx context.Context, report *model.ImpactReport) error
	ListImpactReports(ctx context.Context, tenantID string, page, pageSize int, filter KnowledgeImpactFilter) ([]model.ImpactReport, int64, error)
	CreateDuplicateCandidate(ctx context.Context, candidate *model.DuplicateCandidate) error
	GetDuplicateCandidate(ctx context.Context, tenantID, candidateID string) (*model.DuplicateCandidate, error)
	UpdateDuplicateCandidate(ctx context.Context, tenantID, candidateID string, updates map[string]interface{}) (bool, error)
	ListDuplicateCandidates(ctx context.Context, tenantID string, page, pageSize int, filter KnowledgeImpactFilter) ([]model.DuplicateCandidate, int64, error)
}

func (s *store) GetKnowledgeImpactFacts(ctx context.Context, tenantID, datasetID string) (*model.KnowledgeImpactFacts, error) {
	facts := &model.KnowledgeImpactFacts{}
	// One row per fact group, combined in Go: each sub-select below aggregates
	// a different facet of dataset impact (doc/118 F-04):
	//   1. assistant/release reach via ACTIVE dataset bindings,
	//   2. eval sets whose dataset_ids reference this dataset,
	//   3. historical badcases flowing from knowledge-ops events (source='badcase'),
	//   4. production usage approximated by citation reference counts.
	err := s.WithContext(ctx).Raw(`
		SELECT
			(SELECT COUNT(DISTINCT b.assistant_release_id)
				FROM rgx_dataset_binding_version b
				WHERE b.tenant_id = ? AND b.dataset_id = ? AND b.status = 'ACTIVE') AS affected_release_count,
			(SELECT COUNT(DISTINCT r.assistant_id)
				FROM rgx_dataset_binding_version b
				JOIN rgx_assistant_release r ON r.id = b.assistant_release_id
				WHERE b.tenant_id = ? AND b.dataset_id = ? AND b.status = 'ACTIVE') AS affected_assistant_count,
			(SELECT COUNT(*)
				FROM rgx_eval_set es
				WHERE es.tenant_id = ? AND (',' || es.dataset_ids || ',') LIKE ('%,' || ? || ',%')) AS affected_eval_set_count,
			(SELECT COUNT(*)
				FROM rgx_eval_case ec
				JOIN rgx_knowledge_ops_event ke ON ke.tenant_id = ec.tenant_id AND ke.eval_case_id = ec.id
				JOIN rgx_citation_reference cr ON cr.tenant_id = ke.tenant_id AND cr.request_id = ke.request_id
				WHERE ec.tenant_id = ? AND ec.source = 'badcase' AND cr.dataset_id = ?) AS historical_badcase_count,
			(SELECT COUNT(*)
				FROM rgx_citation_reference cr
				WHERE cr.tenant_id = ? AND cr.dataset_id = ?) AS production_usage_count
	`, tenantID, datasetID, tenantID, datasetID, tenantID, datasetID, tenantID, datasetID, tenantID, datasetID).Scan(facts).Error
	if err != nil {
		return nil, err
	}
	return facts, nil
}

// GetKnowledgeImpactAffectedLists returns the per-facet affected detail lists
// persisted into the ImpactReport JSON columns (doc/118 F-04).
func (s *store) GetKnowledgeImpactAffectedLists(ctx context.Context, tenantID, datasetID string) (assistants, releases, evalSets, badcases []string, err error) {
	assistants, releases = []string{}, []string{}
	evalSets, badcases = []string{}, []string{}
	if err = s.WithContext(ctx).Raw(`
		SELECT DISTINCT r.assistant_id
		FROM rgx_dataset_binding_version b
		JOIN rgx_assistant_release r ON r.id = b.assistant_release_id
		WHERE b.tenant_id = ? AND b.dataset_id = ? AND b.status = 'ACTIVE'
		ORDER BY r.assistant_id
	`, tenantID, datasetID).Scan(&assistants).Error; err != nil {
		return
	}
	if err = s.WithContext(ctx).Raw(`
		SELECT DISTINCT b.assistant_release_id
		FROM rgx_dataset_binding_version b
		WHERE b.tenant_id = ? AND b.dataset_id = ? AND b.status = 'ACTIVE'
		ORDER BY b.assistant_release_id
	`, tenantID, datasetID).Scan(&releases).Error; err != nil {
		return
	}
	if err = s.WithContext(ctx).Raw(`
		SELECT es.id
		FROM rgx_eval_set es
		WHERE es.tenant_id = ? AND (',' || es.dataset_ids || ',') LIKE ('%,' || ? || ',%')
		ORDER BY es.id
	`, tenantID, datasetID).Scan(&evalSets).Error; err != nil {
		return
	}
	err = s.WithContext(ctx).Raw(`
		SELECT DISTINCT ec.id
		FROM rgx_eval_case ec
		JOIN rgx_knowledge_ops_event ke ON ke.tenant_id = ec.tenant_id AND ke.eval_case_id = ec.id
		JOIN rgx_citation_reference cr ON cr.tenant_id = ke.tenant_id AND cr.request_id = ke.request_id
		WHERE ec.tenant_id = ? AND ec.source = 'badcase' AND cr.dataset_id = ?
		ORDER BY ec.id
	`, tenantID, datasetID).Scan(&badcases).Error
	return
}

func (s *store) CreateImpactReport(ctx context.Context, report *model.ImpactReport) error {
	if report.ID == "" {
		report.ID = id.New()
	}
	if report.CreatedAt.IsZero() {
		report.CreatedAt = time.Now().UTC()
	}
	if report.ImpactPolicyVersion == "" {
		report.ImpactPolicyVersion = model.ImpactPolicyVersion
	}
	return s.WithContext(ctx).Create(report).Error
}

func (s *store) ListImpactReports(ctx context.Context, tenantID string, page, pageSize int, filter KnowledgeImpactFilter) ([]model.ImpactReport, int64, error) {
	var list []model.ImpactReport
	var total int64
	q := s.WithContext(ctx).Model(&model.ImpactReport{})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.TargetType != "" {
		q = q.Where("target_type = ?", filter.TargetType)
	}
	if filter.TargetID != "" {
		q = q.Where("target_id = ?", filter.TargetID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) CreateDuplicateCandidate(ctx context.Context, candidate *model.DuplicateCandidate) error {
	if candidate.ID == "" {
		candidate.ID = id.New()
	}
	now := time.Now().UTC()
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	}
	candidate.UpdatedAt = now
	if candidate.Status == "" {
		candidate.Status = model.DuplicateCandidatePending
	}
	return s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(candidate).Error
}

func (s *store) GetDuplicateCandidate(ctx context.Context, tenantID, candidateID string) (*model.DuplicateCandidate, error) {
	var candidate model.DuplicateCandidate
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, candidateID).First(&candidate).Error
	return &candidate, err
}

func (s *store) UpdateDuplicateCandidate(ctx context.Context, tenantID, candidateID string, updates map[string]interface{}) (bool, error) {
	updates["updated_at"] = time.Now().UTC()
	result := s.WithContext(ctx).Model(&model.DuplicateCandidate{}).
		Where("tenant_id = ? AND id = ? AND status = ?", tenantID, candidateID, model.DuplicateCandidatePending).
		Updates(updates)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *store) ListDuplicateCandidates(ctx context.Context, tenantID string, page, pageSize int, filter KnowledgeImpactFilter) ([]model.DuplicateCandidate, int64, error) {
	var list []model.DuplicateCandidate
	var total int64
	q := s.WithContext(ctx).Model(&model.DuplicateCandidate{})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Relation != "" {
		q = q.Where("final_relation = ?", filter.Relation)
	}
	if filter.SourceType != "" {
		q = q.Where("source_type = ?", filter.SourceType)
	}
	if filter.SourceID != "" {
		q = q.Where("source_id = ?", filter.SourceID)
	}
	if filter.CandidateType != "" {
		q = q.Where("candidate_type = ?", filter.CandidateType)
	}
	if filter.CandidateID != "" {
		q = q.Where("candidate_id = ?", filter.CandidateID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}
