package model

import "time"

const (
	ParseModeBuiltin  = "builtin"
	ParseModePipeline = "pipeline"

	QualityEvaluationHeuristicOnly          = "heuristic_only"
	QualityEvaluationHeuristicPlusReference = "heuristic_plus_reference"
)

// ParserPolicy is the strategy-layer mapping from a business document type to
// the RAGFlow parse mode. It deliberately stores upstream IDs only; ragflow-x
// never forks or owns RAGFlow pipeline internals.
type ParserPolicy struct {
	ID               string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	ProjectID        string    `gorm:"column:project_id;index;size:32" json:"project_id"`
	DatasetID        string    `gorm:"column:dataset_id;index;size:32" json:"dataset_id"`
	DocumentType     string    `gorm:"column:document_type;index;size:64;not null" json:"document_type"`
	ParseMode        string    `gorm:"column:parse_mode;size:32;not null" json:"parse_mode"`
	ChunkMethod      string    `gorm:"column:chunk_method;size:64" json:"chunk_method"`
	PipelineID       string    `gorm:"column:pipeline_id;size:64" json:"pipeline_id"`
	ParserConfig     string    `gorm:"column:parser_config;type:text" json:"parser_config"`
	FallbackPolicy   string    `gorm:"column:fallback_policy;type:text" json:"fallback_policy"`
	QualityProfileID string    `gorm:"column:quality_profile_id;index;size:32;not null" json:"quality_profile_id"`
	Active           bool      `gorm:"column:active;not null;default:true" json:"active"`
	CreatedAt        time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ParserPolicy) TableName() string { return "rgx_parser_policy" }

// QualityProfile is the tenant-owned parsing quality baseline. JSON payloads
// are canonicalized and structurally validated before persistence.
type QualityProfile struct {
	ID                string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_rgx_quality_profile_name" json:"tenant_id"`
	Name              string    `gorm:"column:name;size:128;not null;uniqueIndex:idx_rgx_quality_profile_name" json:"name"`
	Metrics           string    `gorm:"column:metrics;type:text;not null" json:"metrics"`
	Thresholds        string    `gorm:"column:thresholds;type:text" json:"thresholds"`
	EvaluationMode    string    `gorm:"column:evaluation_mode;size:32;not null;default:heuristic_only" json:"evaluation_mode"`
	ReferenceWeight   float64   `gorm:"column:reference_weight;not null;default:0" json:"reference_weight"`
	ReferenceHardFail bool      `gorm:"column:reference_hard_fail;not null;default:false" json:"reference_hard_fail"`
	Active            bool      `gorm:"column:active;not null;default:true" json:"active"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (QualityProfile) TableName() string { return "rgx_quality_profile" }
