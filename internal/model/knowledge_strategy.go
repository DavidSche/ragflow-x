package model

import "time"

const (
	KnowledgeStrategySemantic   = "semantic"
	KnowledgeStrategyLexical    = "lexical"
	KnowledgeStrategyHybrid     = "hybrid"
	KnowledgeStrategyCompiled   = "compiled"
	KnowledgeStrategyStructured = "structured"

	RAGFlowCapabilityRetrieval            = "retrieval"
	RAGFlowCapabilityKnowledgeCompilation = "knowledge_compilation"
	RAGFlowCapabilityPipeline             = "pipeline"
	RAGFlowCapabilityMetadataCondition    = "metadata_condition"

	KnowledgeProbeStatusUnknown = "unknown"
	KnowledgeProbeStatusPassed  = "passed"
	KnowledgeProbeStatusFailed  = "failed"
)

type KnowledgeStrategy struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	ProjectID          string     `gorm:"column:project_id;index;size:32" json:"project_id"`
	DatasetID          string     `gorm:"column:dataset_id;index;size:32;not null" json:"dataset_id"`
	StrategyType       string     `gorm:"column:strategy_type;size:32;not null" json:"strategy_type"`
	RAGFlowCapability  string     `gorm:"column:ragflow_capability;size:64;not null" json:"ragflow_capability"`
	Config             string     `gorm:"column:config;type:text;not null" json:"config"`
	FallbackStrategy   string     `gorm:"column:fallback_strategy;size:32;not null" json:"fallback_strategy"`
	AuthorizationScope string     `gorm:"column:authorization_scope;type:text;not null" json:"authorization_scope"`
	ProbeStatus        string     `gorm:"column:probe_status;size:32;not null;default:unknown" json:"probe_status"`
	LastProbeAt        *time.Time `gorm:"column:last_probe_at" json:"last_probe_at"`
	Active             bool       `gorm:"column:active;not null" json:"active"`
	CreatedBy          string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (KnowledgeStrategy) TableName() string { return "rgx_knowledge_strategy" }

type KnowledgeCapabilityProbe struct {
	ID             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID       string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	StrategyID     string    `gorm:"column:strategy_id;index;size:32;not null" json:"strategy_id"`
	Status         string    `gorm:"column:status;size:32;not null" json:"status"`
	LatencyMs      int64     `gorm:"column:latency_ms;not null" json:"latency_ms"`
	ProbeKey       string    `gorm:"column:probe_key;size:64;not null" json:"probe_key"`
	RAGFlowVersion string    `gorm:"column:ragflow_version;size:64;not null" json:"ragflow_version"`
	Detail         string    `gorm:"column:detail;type:text;not null" json:"detail"`
	CheckedAt      time.Time `gorm:"column:checked_at;index;not null" json:"checked_at"`
}

func (KnowledgeCapabilityProbe) TableName() string { return "rgx_knowledge_capability_probe" }
