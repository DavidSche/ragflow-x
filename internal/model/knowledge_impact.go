package model

import "time"

const (
	ImpactPolicyVersion = "knowledge-impact.v1"
	ImpactLevelLow      = "LOW"
	ImpactLevelMedium   = "MEDIUM"
	ImpactLevelHigh     = "HIGH"
	ImpactLevelCritical = "CRITICAL"

	DuplicateCandidatePending   = "PENDING"
	DuplicateCandidateConfirmed = "CONFIRMED"
	DuplicateCandidateRejected  = "REJECTED"
	DuplicateCandidateExpired   = "EXPIRED"

	DuplicateRelationDuplicate  = "duplicate"
	DuplicateRelationSupersedes = "supersedes"
	DuplicateRelationConflicts  = "conflicts"
	DuplicateRelationRelated    = "related"
)

type ImpactReport struct {
	ID                     string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID               string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID              string    `gorm:"column:project_id;size:32;index" json:"project_id"`
	TargetType             string    `gorm:"column:target_type;size:32;not null;index" json:"target_type"`
	TargetID               string    `gorm:"column:target_id;size:64;not null;index" json:"target_id"`
	TargetVersion          string    `gorm:"column:target_version;size:64" json:"target_version"`
	ImpactPolicyVersion    string    `gorm:"column:impact_policy_version;size:32;not null" json:"impact_policy_version"`
	ImpactLevel            string    `gorm:"column:impact_level;size:16;not null;index" json:"impact_level"`
	ImpactDimensionsJSON   string    `gorm:"column:impact_dimensions_json;type:text;not null" json:"impact_dimensions"`
	MetricsJSON            string    `gorm:"column:metrics_json;type:text;not null" json:"metrics"`
	AffectedAssistantsJSON string    `gorm:"column:affected_assistants_json;type:text;not null" json:"affected_assistants"`
	AffectedReleasesJSON   string    `gorm:"column:affected_releases_json;type:text;not null" json:"affected_releases"`
	AffectedEvalSetsJSON   string    `gorm:"column:affected_eval_sets_json;type:text;not null" json:"affected_eval_sets"`
	AffectedBadcasesJSON   string    `gorm:"column:affected_badcases_json;type:text;not null" json:"affected_badcases"`
	CreatedBy              string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt              time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
}

func (ImpactReport) TableName() string { return "rgx_knowledge_impact_report" }

type KnowledgeImpactFacts struct {
	AffectedAssistantCount int64 `gorm:"column:affected_assistant_count" json:"affected_assistant_count"`
	AffectedReleaseCount   int64 `gorm:"column:affected_release_count" json:"affected_release_count"`
	AffectedEvalSetCount   int64 `gorm:"column:affected_eval_set_count" json:"affected_eval_set_count"`
	HistoricalBadcaseCount int64 `gorm:"column:historical_badcase_count" json:"historical_badcase_count"`
	ProductionUsageCount   int64 `gorm:"column:production_usage_count" json:"production_usage_count"`
	AffectedTenantCount    int64 `gorm:"column:affected_tenant_count" json:"affected_tenant_count"`
}

type DuplicateCandidate struct {
	ID                    string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID              string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID             string     `gorm:"column:project_id;size:32;index" json:"project_id"`
	SourceType            string     `gorm:"column:source_type;size:32;not null;index" json:"source_type"`
	SourceID              string     `gorm:"column:source_id;size:64;not null;index" json:"source_id"`
	SourceVersion         string     `gorm:"column:source_version;size:64" json:"source_version"`
	CandidateType         string     `gorm:"column:candidate_type;size:32;not null;index" json:"candidate_type"`
	CandidateID           string     `gorm:"column:candidate_id;size:64;not null;index" json:"candidate_id"`
	CandidateVersion      string     `gorm:"column:candidate_version;size:64" json:"candidate_version"`
	SimilarityScore       float64    `gorm:"column:similarity_score;not null" json:"similarity_score"`
	SimilarityReasonsJSON string     `gorm:"column:similarity_reasons_json;type:text;not null" json:"similarity_reasons"`
	EvidenceJSON          string     `gorm:"column:evidence_json;type:text;not null" json:"evidence"`
	Status                string     `gorm:"column:status;size:16;not null;default:PENDING;index" json:"status"`
	FinalRelation         string     `gorm:"column:final_relation;size:16" json:"final_relation"`
	DecisionBy            string     `gorm:"column:decision_by;size:32;index" json:"decision_by"`
	DecisionAt            *time.Time `gorm:"column:decision_at" json:"decision_at"`
	DecisionNote          string     `gorm:"column:decision_note;type:text" json:"decision_note"`
	CreatedBy             string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null;index" json:"created_at"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (DuplicateCandidate) TableName() string { return "rgx_duplicate_candidate" }
