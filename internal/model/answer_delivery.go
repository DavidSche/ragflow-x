package model

import "time"

const (
	AnswerSchemaVersion = "answer.v1"
	AnswerHashAlgorithm = "SHA256"
	AnswerPolicyVersion = "authorization.v1"

	AnswerLifecycleInit       = "INIT"
	AnswerLifecycleGenerating = "GENERATING"
	AnswerLifecycleFinalizing = "FINALIZING"
	AnswerLifecycleCompleted  = "COMPLETED"
	AnswerLifecycleCancelled  = "CANCELLED"
	AnswerLifecycleFailed     = "FAILED"

	AnswerStatusAnswered             = "ANSWERED"
	AnswerStatusPartial              = "PARTIAL"
	AnswerStatusNoAnswer             = "NO_ANSWER"
	AnswerStatusInsufficientEvidence = "INSUFFICIENT_EVIDENCE"
	AnswerStatusPermissionLimited    = "PERMISSION_LIMITED"
	AnswerStatusToolFailed           = "TOOL_FAILED"
	AnswerStatusSystemFailed         = "SYSTEM_FAILED"

	CompletionReasonNormal        = "NORMAL"
	CompletionReasonUserCancelled = "USER_CANCELLED"
	CompletionReasonTimeout       = "TIMEOUT"
	CompletionReasonToolFailed    = "TOOL_FAILED"
	CompletionReasonSystemFailed  = "SYSTEM_FAILED"

	AnswerEventTypeContent   = "CONTENT"
	AnswerEventTypeCitation  = "CITATION"
	AnswerEventTypeArtifact  = "ARTIFACT"
	AnswerEventTypeExecution = "EXECUTION"
	AnswerEventTypeStatus    = "STATUS"

	AuthorizationVisibilityVisible  = "VISIBLE"
	AuthorizationVisibilityRedacted = "REDACTED"
	AuthorizationVisibilityDenied   = "DENIED"

	ExportFormatMarkdown = "markdown"
	ExportFormatHTML     = "html"
	ExportFormatJSON     = "json"
	ExportFormatDOCX     = "docx"
	ExportFormatPDF      = "pdf"

	ExportJobQueued    = "QUEUED"
	ExportJobRunning   = "RUNNING"
	ExportJobSucceeded = "SUCCEEDED"
	ExportJobPartial   = "PARTIAL"
	ExportJobFailed    = "FAILED"
	ExportJobExpired   = "EXPIRED"
	ExportJobCancelled = "CANCELLED"
)

type AnswerRun struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID          string     `gorm:"column:project_id;size:32;index" json:"project_id"`
	SessionID          string     `gorm:"column:session_id;size:128;not null;index" json:"session_id"`
	AssistantID        string     `gorm:"column:assistant_id;size:64;not null;index" json:"assistant_id"`
	AssistantReleaseID string     `gorm:"column:assistant_release_id;size:32;index" json:"assistant_release_id"`
	QuestionRef        string     `gorm:"column:question_ref;size:128;not null;index" json:"question_ref"`
	Model              string     `gorm:"column:model;size:128" json:"model"`
	TraceID            string     `gorm:"column:trace_id;size:64;index" json:"trace_id"`
	RequestID          string     `gorm:"column:request_id;size:64;not null;uniqueIndex:uk_answer_run_request" json:"request_id"`
	LifecycleState     string     `gorm:"column:lifecycle_state;size:16;not null;index" json:"lifecycle_state"`
	AnswerStatus       string     `gorm:"column:answer_status;size:32;not null;index" json:"answer_status"`
	CompletionReason   string     `gorm:"column:completion_reason;size:24;not null;index" json:"completion_reason"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null;index" json:"created_at"`
	CompletedAt        *time.Time `gorm:"column:completed_at" json:"completed_at"`
}

func (AnswerRun) TableName() string { return "rgx_answer_run" }

type AnswerSnapshot struct {
	ID                  string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID            string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID           string     `gorm:"column:project_id;size:32;index" json:"project_id"`
	AnswerRunID         string     `gorm:"column:answer_run_id;size:32;not null;uniqueIndex" json:"answer_run_id"`
	AnswerSchemaVersion string     `gorm:"column:answer_schema_version;size:32;not null;index" json:"answer_schema_version"`
	LifecycleState      string     `gorm:"column:lifecycle_state;size:16;not null" json:"lifecycle_state"`
	AnswerStatus        string     `gorm:"column:answer_status;size:32;not null;index" json:"answer_status"`
	CompletionReason    string     `gorm:"column:completion_reason;size:24;not null" json:"completion_reason"`
	ReasonCode          string     `gorm:"column:reason_code;size:96;not null" json:"reason_code"`
	UserMessage         string     `gorm:"column:user_message;type:text" json:"user_message"`
	AdminReason         string     `gorm:"column:admin_reason;type:text" json:"admin_reason"`
	Summary             string     `gorm:"column:summary;type:text" json:"summary"`
	Content             string     `gorm:"column:content;type:text;not null" json:"content"`
	CitationsJSON       string     `gorm:"column:citations_json;type:text;not null" json:"citations"`
	ArtifactsJSON       string     `gorm:"column:artifacts_json;type:text;not null" json:"artifacts"`
	ExecutionJSON       string     `gorm:"column:execution_json;type:text;not null" json:"execution"`
	LimitationsJSON     string     `gorm:"column:limitations_json;type:text;not null" json:"limitations"`
	ActionsJSON         string     `gorm:"column:actions_json;type:text;not null" json:"actions"`
	CanonicalHash       string     `gorm:"column:canonical_hash;size:64;not null;uniqueIndex" json:"canonical_hash"`
	HashAlgorithm       string     `gorm:"column:hash_algorithm;size:16;not null" json:"hash_algorithm"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;index" json:"created_at"`
	CompletedAt         *time.Time `gorm:"column:completed_at" json:"completed_at"`
}

func (AnswerSnapshot) TableName() string { return "rgx_answer_snapshot" }

type AnswerEvent struct {
	ID           string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID     string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID    string    `gorm:"column:project_id;size:32;index" json:"project_id"`
	AnswerRunID  string    `gorm:"column:answer_run_id;size:32;not null;uniqueIndex:uk_answer_event_run_seq" json:"answer_run_id"`
	EventID      string    `gorm:"column:event_id;size:64;not null;uniqueIndex" json:"event_id"`
	EventSeq     int64     `gorm:"column:event_seq;not null;uniqueIndex:uk_answer_event_run_seq" json:"event_seq"`
	ResourceType string    `gorm:"column:resource_type;size:32" json:"resource_type"`
	ResourceSeq  string    `gorm:"column:resource_seq;size:128" json:"resource_seq"`
	EventType    string    `gorm:"column:event_type;size:24;not null;index" json:"event_type"`
	Status       string    `gorm:"column:status;size:24" json:"status"`
	PayloadJSON  string    `gorm:"column:payload_json;type:text;not null" json:"payload"`
	OccurredAt   time.Time `gorm:"column:occurred_at;not null;index" json:"occurred_at"`
}

func (AnswerEvent) TableName() string { return "rgx_answer_event" }

type AuthorizationProjection struct {
	ID                   string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID             string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID            string    `gorm:"column:project_id;size:32;index" json:"project_id"`
	AnswerSnapshotID     string    `gorm:"column:answer_snapshot_id;size:32;not null;uniqueIndex:uk_authz_snapshot_principal,priority:1" json:"answer_snapshot_id"`
	PrincipalID          string    `gorm:"column:principal_id;size:32;not null;uniqueIndex:uk_authz_snapshot_principal,priority:2" json:"principal_id"`
	Channel              string    `gorm:"column:channel;size:32;not null;uniqueIndex:uk_authz_snapshot_principal,priority:3" json:"channel"`
	PolicyVersion        string    `gorm:"column:policy_version;size:32;not null" json:"policy_version"`
	PolicyEvaluatedAt    time.Time `gorm:"column:policy_evaluated_at;not null" json:"policy_evaluated_at"`
	PolicyInputHash      string    `gorm:"column:policy_input_hash;size:64;not null" json:"policy_input_hash"`
	DecisionHash         string    `gorm:"column:decision_hash;size:64;not null" json:"decision_hash"`
	AnswerVisibility     string    `gorm:"column:answer_visibility;size:16;not null" json:"answer_visibility"`
	ContentVisibility    string    `gorm:"column:content_visibility;size:16;not null" json:"content_visibility"`
	CitationVisibility   string    `gorm:"column:citation_visibility;size:16;not null" json:"citation_visibility"`
	ArtifactVisibility   string    `gorm:"column:artifact_visibility;size:16;not null" json:"artifact_visibility"`
	ExecutionVisibility  string    `gorm:"column:execution_visibility;size:16;not null" json:"execution_visibility"`
	MetadataVisibility   string    `gorm:"column:metadata_visibility;size:16;not null" json:"metadata_visibility"`
	ActionsVisibility    string    `gorm:"column:actions_visibility;size:16;not null" json:"actions_visibility"`
	RedactionReasonsJSON string    `gorm:"column:redaction_reasons_json;type:text;not null" json:"redaction_reasons"`
	// RetrievalPushdownJSON records the retrieval-layer metadata_condition
	// pushdown evidence (doc/123 §7): {"status":"applied|bypassed|absent",
	// "condition":{...}}. absent = scenario has no pushdown channel.
	RetrievalPushdownJSON string    `gorm:"column:retrieval_pushdown_json;type:text;not null" json:"retrieval_pushdown"`
	CreatedAt             time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
}

func (AuthorizationProjection) TableName() string { return "rgx_answer_authorization_projection" }

// RetrievalPushdown is the persisted evidence of a retrieval-layer filter.
type RetrievalPushdown struct {
	Status    string                  `json:"status"`
	Policy    string                  `json:"policy_version"`
	Condition *map[string]interface{} `json:"condition,omitempty"`
	Reason    string                  `json:"reason,omitempty"`
}

// RetrievalPushdown statuses.
const (
	RetrievalPushdownApplied  = "applied"
	RetrievalPushdownBypassed = "bypassed"
	RetrievalPushdownAbsent   = "absent"
)

// RetrievalPushdownPolicyVersion is the strategy version recorded in the
// AuthorizationProjection policy input (doc/123 §7).
const RetrievalPushdownPolicyVersion = "retrieval-pushdown.v1"

type ExportTemplateVersion struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uk_export_template_version" json:"tenant_id"`
	TemplateID         string    `gorm:"column:template_id;size:32;not null;uniqueIndex:uk_export_template_version" json:"template_id"`
	Version            int64     `gorm:"column:version;not null;uniqueIndex:uk_export_template_version" json:"version"`
	Format             string    `gorm:"column:format;size:16;not null" json:"format"`
	RendererVersion    string    `gorm:"column:renderer_version;size:32;not null" json:"renderer_version"`
	SchemaVersionsJSON string    `gorm:"column:schema_versions_json;type:text;not null" json:"schema_versions"`
	TemplateJSON       string    `gorm:"column:template_json;type:text;not null" json:"template"`
	CreatedBy          string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
}

func (ExportTemplateVersion) TableName() string { return "rgx_export_template_version" }

type ExportSnapshot struct {
	ID                string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID         string    `gorm:"column:project_id;size:32;index" json:"project_id"`
	AnswerSnapshotIDs string    `gorm:"column:answer_snapshot_ids;type:text;not null" json:"answer_snapshot_ids"`
	TemplateID        string    `gorm:"column:template_id;size:32;not null" json:"template_id"`
	TemplateVersion   int64     `gorm:"column:template_version;not null" json:"template_version"`
	PolicyVersion     string    `gorm:"column:policy_version;size:32;not null" json:"policy_version"`
	PolicyEvaluatedAt time.Time `gorm:"column:policy_evaluated_at;not null" json:"policy_evaluated_at"`
	PolicyInputHash   string    `gorm:"column:policy_input_hash;size:64;not null" json:"policy_input_hash"`
	CanonicalHash     string    `gorm:"column:canonical_hash;size:64;not null;index" json:"canonical_hash"`
	PayloadJSON       string    `gorm:"column:payload_json;type:text;not null" json:"payload"`
	CreatedAt         time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
}

func (ExportSnapshot) TableName() string { return "rgx_export_snapshot" }

type ExportJob struct {
	ID                string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID         string     `gorm:"column:project_id;size:32;index" json:"project_id"`
	ExportSnapshotID  string     `gorm:"column:export_snapshot_id;size:32;not null;index" json:"export_snapshot_id"`
	AnswerSnapshotID  string     `gorm:"column:answer_snapshot_id;size:32;not null;index" json:"answer_snapshot_id"`
	RequestedBy       string     `gorm:"column:requested_by;size:32;not null;index" json:"requested_by"`
	Format            string     `gorm:"column:format;size:16;not null;index" json:"format"`
	Status            string     `gorm:"column:status;size:16;not null;index" json:"status"`
	TemplateVersion   int64      `gorm:"column:template_version;not null" json:"template_version"`
	PolicyVersion     string     `gorm:"column:policy_version;size:32;not null" json:"policy_version"`
	PolicyEvaluatedAt time.Time  `gorm:"column:policy_evaluated_at;not null" json:"policy_evaluated_at"`
	PolicyInputHash   string     `gorm:"column:policy_input_hash;size:64;not null" json:"policy_input_hash"`
	ErrorCode         string     `gorm:"column:error_code;size:64" json:"error_code"`
	TraceID           string     `gorm:"column:trace_id;size:64;index" json:"trace_id"`
	IdempotencyKey    string     `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:uk_export_job_idempotency" json:"idempotency_key"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;index" json:"created_at"`
	CompletedAt       *time.Time `gorm:"column:completed_at" json:"completed_at"`
}

func (ExportJob) TableName() string { return "rgx_export_job" }

type ExportArtifact struct {
	ID              string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID        string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID       string    `gorm:"column:project_id;size:32;index" json:"project_id"`
	ExportJobID     string    `gorm:"column:export_job_id;size:32;not null;uniqueIndex" json:"export_job_id"`
	Format          string    `gorm:"column:format;size:16;not null" json:"format"`
	RendererVersion string    `gorm:"column:renderer_version;size:32;not null" json:"renderer_version"`
	FileRef         string    `gorm:"column:file_ref;size:255;not null" json:"file_ref"`
	Filename        string    `gorm:"column:filename;size:255;not null" json:"filename"`
	MimeType        string    `gorm:"column:mime_type;size:128;not null" json:"mime_type"`
	ByteSize        int64     `gorm:"column:byte_size;not null" json:"byte_size"`
	SHA256          string    `gorm:"column:sha256;size:64;not null" json:"sha256"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
	ExpiresAt       time.Time `gorm:"column:expires_at;not null;index" json:"expires_at"`
}

func (ExportArtifact) TableName() string { return "rgx_export_artifact" }

type ExportDownloadAudit struct {
	ID               string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID        string    `gorm:"column:project_id;size:32;index" json:"project_id"`
	ExportArtifactID string    `gorm:"column:export_artifact_id;size:32;not null;index" json:"export_artifact_id"`
	ExportJobID      string    `gorm:"column:export_job_id;size:32;not null;index" json:"export_job_id"`
	AnswerSnapshotID string    `gorm:"column:answer_snapshot_id;size:32;not null;index" json:"answer_snapshot_id"`
	DownloadedBy     string    `gorm:"column:downloaded_by;size:32;not null;index" json:"downloaded_by"`
	DownloadedAt     time.Time `gorm:"column:downloaded_at;not null;index" json:"downloaded_at"`
	// DownloadCount is always 1: each download appends one audit row (event
	// semantics). Reconciliation must COUNT(*) rows per artifact, never treat
	// this column as a mutable counter (doc/118 F-09).
	DownloadCount int64  `gorm:"column:download_count;not null;default:1" json:"download_count"`
	DecisionHash  string `gorm:"column:decision_hash;size:64;not null" json:"decision_hash"`
	TraceID       string `gorm:"column:trace_id;size:64;index" json:"trace_id"`
}

func (ExportDownloadAudit) TableName() string { return "rgx_export_download_audit" }

type AnswerCitation struct {
	ID                  string `json:"id"`
	Title               string `json:"title,omitempty"`
	DatasetID           string `json:"dataset_id,omitempty"`
	DocumentID          string `json:"document_id,omitempty"`
	DocumentVersion     string `json:"document_version,omitempty"`
	ChunkID             string `json:"chunk_id,omitempty"`
	CitedContentExcerpt string `json:"cited_content_excerpt"`
	CitationLocator     string `json:"citation_locator"`
	CitationContentHash string `json:"citation_content_hash"`
}

type AnswerArtifact struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	URI     string `json:"uri,omitempty"`
	Type    string `json:"type,omitempty"`
	Summary string `json:"summary,omitempty"`
}
