package model

import "time"

const (
	IncrementalStateActive      = "active"
	IncrementalStateParsing     = "parsing"
	IncrementalStateReplaced    = "replaced"
	IncrementalStateTombstone   = "tombstone"
	IncrementalStateFailed      = "failed"
	IncrementalStateNeedsReview = "needs_review"

	IncrementalTombstoneObservation = 7 * 24 * time.Hour
)

// IncrementalLedger is the workspace-scoped logical revision projection for a
// stable source document. RAGFlow remains the parser/index owner; this ledger
// only decides whether an upload is unchanged, changed, or removed.
type IncrementalLedger struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uk_incremental_revision,priority:1" json:"tenant_id"`
	DatasetID          string     `gorm:"column:dataset_id;size:32;not null;uniqueIndex:uk_incremental_revision,priority:2" json:"dataset_id"`
	SourceKey          string     `gorm:"column:source_key;size:255;not null;uniqueIndex:uk_incremental_revision,priority:3" json:"source_key"`
	Revision           int64      `gorm:"column:revision;not null;uniqueIndex:uk_incremental_revision,priority:4" json:"revision"`
	Name               string     `gorm:"column:name;size:255;not null" json:"name"`
	ContentSHA256      string     `gorm:"column:content_sha256;size:64;not null" json:"content_sha256"`
	ParserPolicyHash   string     `gorm:"column:parser_policy_hash;size:64;not null" json:"parser_policy_hash"`
	EmbeddingRef       string     `gorm:"column:embedding_ref;size:255;not null" json:"embedding_ref"`
	RAGFlowDocumentID  string     `gorm:"column:ragflow_document_id;size:64;not null;index" json:"ragflow_document_id"`
	PreviousDocumentID string     `gorm:"column:previous_document_id;size:64;index" json:"previous_document_id"`
	ParseTaskID        string     `gorm:"column:parse_task_id;size:32;index" json:"parse_task_id"`
	State              string     `gorm:"column:state;size:24;not null;default:parsing;index" json:"state"`
	Version            int64      `gorm:"column:version;not null;default:0" json:"version"`
	FailureReason      string     `gorm:"column:failure_reason;type:text" json:"failure_reason"`
	VerifiedAt         *time.Time `gorm:"column:verified_at" json:"verified_at"`
	LastSeenAt         *time.Time `gorm:"column:last_seen_at" json:"last_seen_at"`
	TombstonedAt       *time.Time `gorm:"column:tombstoned_at" json:"tombstoned_at"`
	PurgeAfterAt       *time.Time `gorm:"column:purge_after_at;index" json:"purge_after_at"`
	CreatedAt          time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (IncrementalLedger) TableName() string { return "rgx_incremental_ledger" }
