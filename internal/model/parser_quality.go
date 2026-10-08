package model

import "time"

const (
	QualityStatusPass = "PASS"
	QualityStatusWarn = "WARN"
	QualityStatusFail = "FAIL"

	GateActionPublish         = "PUBLISH"
	GateActionPublishWithRisk = "PUBLISH_WITH_RISK"
	GateActionRetry           = "RETRY"
	GateActionQuarantine      = "QUARANTINE"
)

// ParseAttempt is the immutable trace of one parse execution. Fallback chains
// remain auditable because every retry gets a new attempt row.
type ParseAttempt struct {
	ID             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID       string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_rgx_parse_attempt_document_no" json:"tenant_id"`
	DocumentID     string    `gorm:"column:document_id;size:64;not null;uniqueIndex:idx_rgx_parse_attempt_document_no;index" json:"document_id"`
	AttemptNo      int64     `gorm:"column:attempt_no;not null;uniqueIndex:idx_rgx_parse_attempt_document_no" json:"attempt_no"`
	ParserPolicyID string    `gorm:"column:parser_policy_id;index;size:32;not null" json:"parser_policy_id"`
	ParseMode      string    `gorm:"column:parse_mode;size:32;not null" json:"parse_mode"`
	StartedAt      time.Time `gorm:"column:started_at" json:"started_at"`
	FinishedAt     time.Time `gorm:"column:finished_at" json:"finished_at"`
	QualityStatus  string    `gorm:"column:quality_status;size:32;not null" json:"quality_status"`
	QualityScore   float64   `gorm:"column:quality_score;not null;default:0" json:"quality_score"`
	FailureReason  string    `gorm:"column:failure_reason;size:1024" json:"failure_reason"`
	CreatedAt      time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ParseAttempt) TableName() string { return "rgx_parse_attempt" }

// ParseQualityReport is the final quality decision associated with an attempt.
// QualityStatus has exactly three states; QUARANTINE is only a GateAction.
type ParseQualityReport struct {
	ID             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID       string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	DocumentID     string    `gorm:"column:document_id;index;size:64;not null" json:"document_id"`
	ParserPolicyID string    `gorm:"column:parser_policy_id;index;size:32;not null" json:"parser_policy_id"`
	AttemptID      string    `gorm:"column:attempt_id;index;size:32;not null" json:"attempt_id"`
	ParseMode      string    `gorm:"column:parse_mode;size:32;not null" json:"parse_mode"`
	QualityStatus  string    `gorm:"column:quality_status;size:32;not null" json:"quality_status"`
	GateAction     string    `gorm:"column:gate_action;size:32;not null" json:"gate_action"`
	HeuristicScore float64   `gorm:"column:heuristic_score;not null;default:0" json:"heuristic_score"`
	ReferenceScore *float64  `gorm:"column:reference_score" json:"reference_score"`
	EffectiveScore float64   `gorm:"column:effective_score;not null;default:0" json:"effective_score"`
	Metrics        string    `gorm:"column:metrics;type:text;not null" json:"metrics"`
	CreatedAt      time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ParseQualityReport) TableName() string { return "rgx_parse_quality_report" }
