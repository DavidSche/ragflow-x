package model

import "time"

const (
	FactClaimTypeExtracted = "extracted"
	FactClaimTypeComputed  = "computed"

	FactConflictNone     = "none"
	FactConflictTemporal = "temporal_conflict"
	FactConflictSource   = "source_conflict"
	FactConflictValue    = "value_conflict"

	ClaimValidationSupported    = "supported"
	ClaimValidationContradicted = "contradicted"
	ClaimValidationUnsupported  = "unsupported"

	ClaimActionKeep       = "keep"
	ClaimActionRegenerate = "regenerate"
	ClaimActionFlag       = "flag"
	ClaimActionBlock      = "block"

	EvidenceConflictUnresolved        = "unresolved"
	EvidenceConflictResolvedByVersion = "resolved_by_version"
	EvidenceConflictRetainedForReview = "retained_for_review"
)

// FactRegistry stores normalized facts extracted from one answer run's
// evidence. EvidenceConflict is the authoritative conflict relationship.
type FactRegistry struct {
	ID                string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	AnswerRunID       string    `gorm:"column:answer_run_id;index;size:32;not null" json:"answer_run_id"`
	LogicalDocumentID string    `gorm:"column:logical_document_id;index;size:32" json:"logical_document_id"`
	DocumentVersionID string    `gorm:"column:document_version_id;index;size:32" json:"document_version_id"`
	FactKey           string    `gorm:"column:fact_key;size:128;not null" json:"fact_key"`
	Value             string    `gorm:"column:value;size:512;not null" json:"value"`
	Unit              string    `gorm:"column:unit;size:32" json:"unit"`
	TimeRange         string    `gorm:"column:time_range;size:128" json:"time_range"`
	SourceChunkID     string    `gorm:"column:source_chunk_id;size:64" json:"source_chunk_id"`
	SourceDocID       string    `gorm:"column:source_doc_id;size:64;not null" json:"source_doc_id"`
	SourceSpan        string    `gorm:"column:source_span;size:256" json:"source_span"`
	ClaimType         string    `gorm:"column:claim_type;size:32;not null" json:"claim_type"`
	ConflictStatus    string    `gorm:"column:conflict_status;size:32;not null;default:none" json:"conflict_status"`
	Confidence        float64   `gorm:"column:confidence" json:"confidence"`
	CreatedAt         time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (FactRegistry) TableName() string { return "rgx_fact_registry" }

type ClaimValidation struct {
	ID               string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	AnswerRunID      string    `gorm:"column:answer_run_id;index;size:32;not null" json:"answer_run_id"`
	PrimaryFactID    *string   `gorm:"column:primary_fact_id;size:32" json:"primary_fact_id"`
	FactIDs          string    `gorm:"column:fact_ids;type:text;not null" json:"fact_ids"`
	LLMClaim         string    `gorm:"column:llm_claim;type:text;not null" json:"llm_claim"`
	FactKey          string    `gorm:"column:fact_key;size:128" json:"fact_key"`
	Value            string    `gorm:"column:value;size:512" json:"value"`
	Unit             string    `gorm:"column:unit;size:32" json:"unit"`
	TimeRange        string    `gorm:"column:time_range;size:128" json:"time_range"`
	ValidationStatus string    `gorm:"column:validation_status;size:32;not null" json:"validation_status"`
	Action           string    `gorm:"column:action;size:32;not null" json:"action"`
	RegeneratedText  string    `gorm:"column:regenerated_text;type:text" json:"regenerated_text"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (ClaimValidation) TableName() string { return "rgx_claim_validation" }

type EvidenceConflict struct {
	ID           string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID     string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	AnswerRunID  string    `gorm:"column:answer_run_id;index;size:32;not null" json:"answer_run_id"`
	LeftFactID   string    `gorm:"column:left_fact_id;index;size:32;not null" json:"left_fact_id"`
	RightFactID  string    `gorm:"column:right_fact_id;index;size:32;not null" json:"right_fact_id"`
	ConflictType string    `gorm:"column:conflict_type;size:32;not null" json:"conflict_type"`
	Resolution   string    `gorm:"column:resolution;size:32;not null" json:"resolution"`
	ResolverMode string    `gorm:"column:resolver_mode;size:32" json:"resolver_mode"`
	EvidenceRefs string    `gorm:"column:evidence_refs;type:text" json:"evidence_refs"`
	CreatedAt    time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (EvidenceConflict) TableName() string { return "rgx_evidence_conflict" }
