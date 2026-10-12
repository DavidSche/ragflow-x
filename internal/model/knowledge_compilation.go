package model

import "time"

const (
	KnowledgeProfileStatusActive   = "active"
	KnowledgeProfileStatusInactive = "inactive"

	CompilationKindWiki      = "wiki"
	CompilationKindTree      = "tree"
	CompilationKindGraph     = "graph"
	CompilationKindMindmap   = "mindmap"
	CompilationKindTimeline  = "timeline"
	CompilationKindPageIndex = "page_index"

	CompilationRunAccepted   = "accepted"
	CompilationRunDispatched = "dispatched"
	CompilationRunConfirmed  = "confirmed"
	CompilationRunFailed     = "failed"
	CompilationRunUnknown    = "unknown"
	CompilationRunReconciled = "reconciled"
	CompilationRunCancelled  = "cancelled"
	CompilationRunBlocked    = "blocked"
	CompilationRunSkipped    = "skipped"

	CompilationRecoveryPending              = "pending_recovery"
	CompilationRecoveryInsufficientEvidence = "insufficient_evidence"
	CompilationRecoveryWaitingDownstream    = "waiting_downstream"
	CompilationRecoveryNeedsApproval        = "needs_approval"
	CompilationRecoveryConfirmed            = "downstream_absent_confirmed"
	CompilationRecoveryReplacementPending   = "replacement_pending_approval"
	CompilationRecoveryReplacement          = "replacement_created"

	CompilationSourceConsistencyUnverified = "unverified"
	CompilationSourceConsistencyConsistent = "consistent"
	CompilationSourceConsistencyChanged    = "changed"
	CompilationSourceConsistencyUnknown    = "unknown"

	ArtifactProjectionUnverified = "unverified"
	ArtifactProjectionReady      = "ready"
	ArtifactProjectionStale      = "stale"

	MemoryPolicyActive  = "active"
	MemoryPolicyPaused  = "paused"
	MemoryPolicyRetired = "retired"

	MemoryOperationAccepted   = "accepted"
	MemoryOperationAdmitted   = "admitted"
	MemoryOperationDispatched = "dispatched"
	MemoryOperationExecuted   = "executed"
	MemoryOperationFailed     = "failed"
	MemoryOperationUnknown    = "unknown"
	MemoryOperationBlocked    = "blocked"
	MemoryOperationSkipped    = "skipped"
	MemoryOperationReconciled = "reconciled"

	ContextDecisionSearch = "search"
	ContextDecisionChat   = "chat"
)

const (
	CompilationFailureDownstreamNotFound = "downstream_not_found"
	CompilationFailureUnknownState       = "unknown_state"

	CompilationCancelSchedulingOnly          = "x_scheduling_only"
	CompilationCancelDownstreamConfirmedStop = "downstream_confirmed_stopped"

	CompilationRecoveryWindow = 30 * time.Minute
)

type KnowledgeProfile struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	ProjectID   string    `gorm:"column:project_id;index;size:32" json:"project_id"`
	DatasetID   string    `gorm:"column:dataset_id;index;size:32;not null" json:"dataset_id"`
	Name        string    `gorm:"column:name;size:128;not null" json:"name"`
	Description string    `gorm:"column:description;size:512" json:"description"`
	ProfileType string    `gorm:"column:profile_type;size:32;not null" json:"profile_type"`
	Config      string    `gorm:"column:config;type:text;not null" json:"config"`
	Status      string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	CreatedBy   string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (KnowledgeProfile) TableName() string { return "rgx_knowledge_profile" }

type CompilationRun struct {
	ID                         string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                   string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	ProjectID                  string     `gorm:"column:project_id;index;size:32" json:"project_id"`
	DatasetID                  string     `gorm:"column:dataset_id;index;size:32;not null" json:"dataset_id"`
	ProfileID                  string     `gorm:"column:profile_id;index;size:32" json:"profile_id"`
	Kind                       string     `gorm:"column:kind;size:32;not null;default:wiki" json:"kind"`
	IdempotencyKey             string     `gorm:"column:idempotency_key;size:128;not null" json:"idempotency_key"`
	State                      string     `gorm:"column:state;size:32;not null" json:"state"`
	ManifestHash               string     `gorm:"column:manifest_hash;size:128;not null" json:"manifest_hash"`
	TemplateHash               string     `gorm:"column:template_hash;size:128;not null" json:"template_hash"`
	PipelineHash               string     `gorm:"column:pipeline_hash;size:128;not null" json:"pipeline_hash"`
	DocumentIDs                string     `gorm:"column:document_ids;type:text;not null;default:'[]'" json:"document_ids"`
	ManifestComplete           bool       `gorm:"column:manifest_complete;not null;default:false" json:"manifest_complete"`
	DownstreamSourceSnapshotID string     `gorm:"column:downstream_source_snapshot_id;size:128;not null;default:''" json:"downstream_source_snapshot_id"`
	PostRunSourceManifestHash  string     `gorm:"column:post_run_source_manifest_hash;size:128;not null;default:''" json:"post_run_source_manifest_hash"`
	SourceConsistency          string     `gorm:"column:source_consistency;size:32;not null;default:unverified" json:"source_consistency"`
	DownstreamTaskID           string     `gorm:"column:downstream_task_id;size:128;not null;default:''" json:"downstream_task_id"`
	TriggeredByXRunID          string     `gorm:"column:triggered_by_x_run_id;size:32;not null;default:''" json:"triggered_by_x_run_id"`
	BudgetSnapshot             string     `gorm:"column:budget_snapshot;type:text;not null;default:'{}'" json:"budget_snapshot"`
	QuotaReservationID         string     `gorm:"column:quota_reservation_id;size:64;not null;default:''" json:"quota_reservation_id"`
	CancelScope                string     `gorm:"column:cancel_scope;size:64;not null;default:x_scheduling_only" json:"cancel_scope"`
	FailureCode                string     `gorm:"column:failure_code;size:64;not null;default:''" json:"failure_code"`
	RecoveryState              string     `gorm:"column:recovery_state;size:64;not null;default:''" json:"recovery_state"`
	RecoveryEvidence           string     `gorm:"column:recovery_evidence;type:text;not null;default:'{}'" json:"recovery_evidence"`
	ApprovalID                 string     `gorm:"column:approval_id;size:32;not null;default:''" json:"approval_id"`
	UnknownAt                  *time.Time `gorm:"column:unknown_at" json:"unknown_at"`
	RecoveryFirstNoMatchAt     *time.Time `gorm:"column:recovery_first_no_match_at" json:"recovery_first_no_match_at"`
	RecoveryLastNoMatchAt      *time.Time `gorm:"column:recovery_last_no_match_at" json:"recovery_last_no_match_at"`
	RecoveryNoMatchCount       int        `gorm:"column:recovery_no_match_count;not null;default:0" json:"recovery_no_match_count"`
	RecoveryDecisionAt         *time.Time `gorm:"column:recovery_decision_at" json:"recovery_decision_at"`
	RecoveryObservationSeconds int64      `gorm:"column:recovery_observation_seconds;not null;default:0" json:"recovery_observation_seconds"`
	RecoveryApprovedBy         string     `gorm:"column:recovery_approved_by;size:32;not null;default:''" json:"recovery_approved_by"`
	RecoveryOperatedBy         string     `gorm:"column:recovery_operated_by;size:32;not null;default:''" json:"recovery_operated_by"`
	RequestedBy                string     `gorm:"column:requested_by;size:32;not null" json:"requested_by"`
	DispatchedAt               *time.Time `gorm:"column:dispatched_at" json:"dispatched_at"`
	CompletedAt                *time.Time `gorm:"column:completed_at" json:"completed_at"`
	Error                      string     `gorm:"column:error;size:512" json:"error"`
	Detail                     string     `gorm:"column:detail;type:text;not null" json:"detail"`
	CreatedAt                  time.Time  `gorm:"column:created_at;index;not null" json:"created_at"`
	UpdatedAt                  time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (CompilationRun) TableName() string { return "rgx_compilation_run" }

type ArtifactProjection struct {
	ID           string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID     string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	DatasetID    string    `gorm:"column:dataset_id;index;size:32;not null" json:"dataset_id"`
	RunID        string    `gorm:"column:run_id;index;size:32;not null" json:"run_id"`
	ArtifactID   string    `gorm:"column:artifact_id;index;size:64;not null" json:"artifact_id"`
	Kind         string    `gorm:"column:kind;size:32;not null" json:"kind"`
	State        string    `gorm:"column:state;size:32;not null" json:"state"`
	ArtifactHash string    `gorm:"column:artifact_hash;size:128;not null" json:"artifact_hash"`
	ContentHash  string    `gorm:"column:content_hash;size:128" json:"content_hash"`
	ArtifactJSON string    `gorm:"column:artifact_json;type:text;not null" json:"artifact_json"`
	CreatedAt    time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ArtifactProjection) TableName() string { return "rgx_artifact_projection" }

type MemoryPolicy struct {
	ID               string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	ProjectID        string    `gorm:"column:project_id;index;size:32" json:"project_id"`
	AssistantID      string    `gorm:"column:assistant_id;index;size:32" json:"assistant_id"`
	UserID           string    `gorm:"column:user_id;index;size:32" json:"user_id"`
	RagflowMemoryID  string    `gorm:"column:ragflow_memory_id;index;size:32" json:"ragflow_memory_id"`
	State            string    `gorm:"column:state;size:32;not null;default:paused" json:"state"`
	ReadPolicy       string    `gorm:"column:read_policy;size:32;not null;default:deny" json:"read_policy"`
	WritePolicy      string    `gorm:"column:write_policy;size:32;not null;default:deny" json:"write_policy"`
	PolicyVersion    int64     `gorm:"column:policy_version;not null;default:1" json:"policy_version"`
	PolicyGeneration int64     `gorm:"column:policy_generation;not null;default:1" json:"policy_generation"`
	ConsentVersion   int64     `gorm:"column:consent_version;not null;default:0" json:"consent_version"`
	Config           string    `gorm:"column:config;type:text;not null" json:"config"`
	CreatedBy        string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (MemoryPolicy) TableName() string { return "rgx_memory_policy" }

type MemoryOperation struct {
	ID                        string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                  string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	ProjectID                 string     `gorm:"column:project_id;index;size:32" json:"project_id"`
	PolicyID                  string     `gorm:"column:policy_id;index;size:32;not null" json:"policy_id"`
	MemoryID                  string     `gorm:"column:memory_id;index;size:32;not null" json:"memory_id"`
	AssistantID               string     `gorm:"column:assistant_id;index;size:32;not null" json:"assistant_id"`
	SessionID                 string     `gorm:"column:session_id;size:128" json:"session_id,omitempty"`
	UserID                    string     `gorm:"column:user_id;index;size:32;not null" json:"user_id"`
	OutboxEventID             string     `gorm:"column:outbox_event_id;index;size:32" json:"outbox_event_id,omitempty"`
	RequestID                 string     `gorm:"column:request_id;size:128;not null" json:"request_id"`
	OperationType             string     `gorm:"column:operation_type;size:32;not null" json:"operation_type"`
	IdempotencyKey            string     `gorm:"column:idempotency_key;size:128;not null" json:"idempotency_key"`
	State                     string     `gorm:"column:state;size:32;not null" json:"state"`
	TombstoneActive           bool       `gorm:"column:tombstone_active;not null;default:false" json:"tombstone_active"`
	TombstoneScopeHash        string     `gorm:"column:tombstone_scope_hash;size:128" json:"-"`
	TombstonePayloadHash      string     `gorm:"column:tombstone_payload_hash;size:128" json:"-"`
	TombstoneMessageHash      string     `gorm:"column:tombstone_message_hash;size:128" json:"-"`
	TombstoneCreatedAt        *time.Time `gorm:"column:tombstone_created_at" json:"tombstone_created_at,omitempty"`
	PolicyVersion             int64      `gorm:"column:policy_version;not null;default:1" json:"policy_version"`
	PolicyGeneration          int64      `gorm:"column:policy_generation;not null" json:"policy_generation"`
	ConsentVersion            int64      `gorm:"column:consent_version;not null" json:"consent_version"`
	PayloadCiphertext         string     `gorm:"column:payload_ciphertext;type:text" json:"-"`
	PayloadKeyID              string     `gorm:"column:payload_key_id;size:128" json:"-"`
	PayloadHash               string     `gorm:"column:payload_hash;size:192" json:"payload_hash,omitempty"`
	ExternalMessageHash       string     `gorm:"column:external_message_hash;size:128" json:"-"`
	MemoryType                string     `gorm:"column:memory_type;size:32" json:"memory_type,omitempty"`
	RagflowAuthPrincipalID    string     `gorm:"column:ragflow_auth_principal_id;size:128" json:"ragflow_auth_principal_id,omitempty"`
	RagflowAttributionUserID  string     `gorm:"column:ragflow_attribution_user_id;size:128" json:"ragflow_attribution_user_id,omitempty"`
	RagflowMessageID          string     `gorm:"column:ragflow_message_id;size:128" json:"ragflow_message_id,omitempty"`
	RagflowTaskID             string     `gorm:"column:ragflow_task_id;size:128" json:"ragflow_task_id,omitempty"`
	AdmissionPolicyGeneration *int64     `gorm:"column:admission_policy_generation" json:"admission_policy_generation,omitempty"`
	AdmissionConsentVersion   *int64     `gorm:"column:admission_consent_version" json:"admission_consent_version,omitempty"`
	DispatchAdmittedAt        *time.Time `gorm:"column:dispatch_admitted_at" json:"dispatch_admitted_at,omitempty"`
	ReasonCode                string     `gorm:"column:reason_code;size:128" json:"reason_code,omitempty"`
	RecordVersion             int64      `gorm:"column:record_version;not null;default:1" json:"record_version"`
	ClaimOwner                string     `gorm:"column:claim_owner;size:128" json:"claim_owner,omitempty"`
	ClaimExpiresAt            *time.Time `gorm:"column:claim_expires_at" json:"claim_expires_at,omitempty"`
	Detail                    string     `gorm:"column:detail;type:text;not null" json:"detail,omitempty"`
	CreatedAt                 time.Time  `gorm:"column:created_at;index;not null" json:"created_at"`
	UpdatedAt                 time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (MemoryOperation) TableName() string { return "rgx_memory_operation" }

type ContextDecision struct {
	ID             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID       string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	RequestID      string    `gorm:"column:request_id;index;size:64;not null" json:"request_id"`
	RequestKind    string    `gorm:"column:request_kind;size:32;not null" json:"request_kind"`
	AssistantID    string    `gorm:"column:assistant_id;index;size:32;not null" json:"assistant_id"`
	SessionID      string    `gorm:"column:session_id;index;size:32" json:"session_id"`
	UserID         string    `gorm:"column:user_id;index;size:32;not null" json:"user_id"`
	Decision       string    `gorm:"column:decision;size:32;not null" json:"decision"`
	MemoryPolicyID string    `gorm:"column:memory_policy_id;size:32" json:"memory_policy_id"`
	DecisionHash   string    `gorm:"column:decision_hash;size:128;not null" json:"decision_hash"`
	Detail         string    `gorm:"column:detail;type:text;not null" json:"detail"`
	CreatedAt      time.Time `gorm:"column:created_at;index;not null" json:"created_at"`
}

func (ContextDecision) TableName() string { return "rgx_context_decision" }
