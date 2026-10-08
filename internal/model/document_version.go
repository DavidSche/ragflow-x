package model

import "time"

const (
	DocumentVersionDraft      = "draft"
	DocumentVersionPublishing = "publishing"
	DocumentVersionActive     = "active"
	DocumentVersionSuperseded = "superseded"
	DocumentVersionArchived   = "archived"
)

// LogicalDocument is the stable business identity of knowledge across
// RAGFlow document revisions.
type LogicalDocument struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	DatasetID   string    `gorm:"column:dataset_id;index;size:32;not null" json:"dataset_id"`
	Name        string    `gorm:"column:name;size:256;not null" json:"name"`
	SourceURI   string    `gorm:"column:source_uri;size:512" json:"source_uri"`
	Description string    `gorm:"column:description;size:1024" json:"description"`
	CreatedBy   string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (LogicalDocument) TableName() string { return "rgx_logical_document" }

// DocumentVersion is the immutable business-version identity. RAGFlow remains
// the execution-plane document owner; this table only records governance state.
type DocumentVersion struct {
	ID                string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	LogicalDocumentID string     `gorm:"column:logical_document_id;index;size:32;not null;uniqueIndex:uk_rgx_document_version_number;uniqueIndex:uk_rgx_document_version_content_hash" json:"logical_document_id"`
	RAGFlowDocumentID string     `gorm:"column:ragflow_document_id;index;size:64;not null" json:"ragflow_document_id"`
	Version           int64      `gorm:"column:version;not null;uniqueIndex:uk_rgx_document_version_number" json:"version"`
	Status            string     `gorm:"column:status;index;size:32;not null" json:"status"`
	EffectiveFrom     time.Time  `gorm:"column:effective_from;not null" json:"effective_from"`
	EffectiveTo       *time.Time `gorm:"column:effective_to" json:"effective_to"`
	ContentHash       string     `gorm:"column:content_hash;size:64;not null;uniqueIndex:uk_rgx_document_version_content_hash" json:"content_hash"`
	ChangeSummary     string     `gorm:"column:change_summary;size:1024" json:"change_summary"`
	SupersedesVersion *int64     `gorm:"column:supersedes_version" json:"supersedes_version"`
	CreatedBy         string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt         time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (DocumentVersion) TableName() string { return "rgx_document_version" }

// VersionFilterPolicy controls when version IDs are pushed down to RAGFlow.
type VersionFilterPolicy struct {
	ID              string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID        string    `gorm:"column:tenant_id;uniqueIndex:uk_rgx_version_filter_policy_tenant;size:32;not null" json:"tenant_id"`
	MaxPushdownIDs  int       `gorm:"column:max_pushdown_ids;not null;default:200" json:"max_pushdown_ids"`
	MaxRequestBytes int       `gorm:"column:max_request_bytes;not null;default:32768" json:"max_request_bytes"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (VersionFilterPolicy) TableName() string { return "rgx_version_filter_policy" }

const (
	VersionPublishPreparing     = "preparing"
	VersionPublishRAGFlowUpdate = "ragflow_updated"
	VersionPublishCommitted     = "committed"
	VersionPublishFailed        = "failed"
	VersionPublishCompensated   = "compensated"
	VersionPublishManualReview  = "manual_review"
)

// VersionPublishAttempt is the durable coordination record for a logical
// atomic publish across ragflow-x state and RAGFlow metadata.
type VersionPublishAttempt struct {
	ID                string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	LogicalDocumentID string     `gorm:"column:logical_document_id;index;size:32;not null" json:"logical_document_id"`
	NewVersionID      string     `gorm:"column:new_version_id;index;size:32;not null" json:"new_version_id"`
	PreviousVersionID *string    `gorm:"column:previous_version_id;size:32" json:"previous_version_id"`
	State             string     `gorm:"column:state;index;size:32;not null" json:"state"`
	SourceStatus      string     `gorm:"column:source_status;size:32" json:"source_status"`
	DesiredMetadata   string     `gorm:"column:desired_metadata;type:text" json:"desired_metadata"`
	RollbackMetadata  string     `gorm:"column:rollback_metadata;type:text" json:"rollback_metadata"`
	Attempts          int        `gorm:"column:attempts;not null;default:0" json:"attempts"`
	LastError         string     `gorm:"column:last_error;size:1024" json:"last_error"`
	ClaimedAt         *time.Time `gorm:"column:claimed_at" json:"claimed_at"`
	ClaimExpiresAt    *time.Time `gorm:"column:claim_expires_at" json:"claim_expires_at"`
	CreatedAt         time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (VersionPublishAttempt) TableName() string { return "rgx_version_publish_attempt" }

const (
	EventTypeDocumentVersionPublished = "document_version_published"
	AggregateTypeDocumentVersion      = "document_version"
)

// OutboxEvent is a durable domain event written in the same transaction as
// its triggering state change. Worker dispatch is therefore at-least-once.
type OutboxEvent struct {
	ID             string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID       string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	EventType      string     `gorm:"column:event_type;index;size:64;not null" json:"event_type"`
	AggregateType  string     `gorm:"column:aggregate_type;index;size:64;not null" json:"aggregate_type"`
	AggregateID    string     `gorm:"column:aggregate_id;index;size:32;not null" json:"aggregate_id"`
	Payload        string     `gorm:"column:payload;type:text;not null" json:"payload"`
	Result         string     `gorm:"column:result;type:text" json:"result"`
	OccurredAt     time.Time  `gorm:"column:occurred_at;not null" json:"occurred_at"`
	PublishedAt    *time.Time `gorm:"column:published_at" json:"published_at"`
	Attempts       int        `gorm:"column:attempts;not null;default:0" json:"attempts"`
	NextRetryAt    *time.Time `gorm:"column:next_retry_at" json:"next_retry_at"`
	ClaimedAt      *time.Time `gorm:"column:claimed_at" json:"claimed_at"`
	ClaimExpiresAt *time.Time `gorm:"column:claim_expires_at" json:"claim_expires_at"`
	LastError      string     `gorm:"column:last_error;type:text" json:"last_error"`
	CreatedAt      time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (OutboxEvent) TableName() string { return "rgx_outbox_event" }
