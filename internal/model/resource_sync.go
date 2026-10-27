package model

import "time"

const (
	SyncTriggerManualImport       = "manual_import"
	SyncTriggerScheduledReconcile = "scheduled_reconcile"
	SyncTriggerManualReconcile    = "manual_reconcile"

	SyncRunPending       = "pending"
	SyncRunScanning      = "scanning"
	SyncRunPlanned       = "planned"
	SyncRunRunning       = "running"
	SyncRunSucceeded     = "succeeded"
	SyncRunFailed        = "failed"
	SyncRunCanceled      = "canceled"
	SyncRunPartialFailed = "partial_failed"

	SyncConsistencyCursorConsistent = "cursor_consistent"
	SyncConsistencyPageScan         = "page_scan_approximate"
	SyncConsistencyIncomplete       = "incomplete"

	SyncItemTypeDataset   = "dataset"
	SyncItemTypeChat      = "chat"
	SyncItemTypeAgent     = "agent"
	SyncItemTypeSearchApp = "search_app"
	SyncItemTypeMemory    = "memory"

	SyncActionCreate      = "create"
	SyncActionUpdate      = "update"
	SyncActionSkip        = "skip"
	SyncActionConflict    = "conflict"
	SyncActionMarkMissing = "mark_missing"
	SyncActionRelink      = "relink"

	SyncItemStatusPending   = "pending"
	SyncItemStatusSucceeded = "succeeded"
	SyncItemStatusFailed    = "failed"
	SyncItemStatusSkipped   = "skipped"

	SyncConflictNone         = "none"
	SyncConflictContent      = "content"
	SyncConflictIdentity     = "identity"
	SyncConflictMapping      = "mapping"
	SyncConflictLocalMissing = "local_missing"

	ResourceSyncStateSynced          = "synced"
	ResourceSyncStateUpstreamChanged = "upstream_changed"
	ResourceSyncStateLocalChanged    = "local_changed"
	ResourceSyncStateConflict        = "conflict"
	ResourceSyncStateStale           = "stale"

	ResourceBindingLifecycleActive   = "active"
	ResourceBindingLifecycleDisabled = "disabled"
	ResourceBindingLifecycleMissing  = "missing"
	ResourceBindingLifecycleRetired  = "retired"

	ResourceGovernanceNormal        = "normal"
	ResourceGovernancePendingReview = "pending_review"
	ResourceGovernanceOwnerInvalid  = "owner_invalid"
)

// ResourceSyncSetting is the durable configuration for one RAGFlow source.
type ResourceSyncSetting struct {
	ID                        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	SourceID                  string    `gorm:"column:source_id;size:64;not null;uniqueIndex" json:"source_id"`
	TenantID                  string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Enabled                   bool      `gorm:"column:enabled;not null;default:false" json:"enabled"`
	ScheduledReconcileEnabled bool      `gorm:"column:scheduled_reconcile_enabled;not null;default:false" json:"scheduled_reconcile_enabled"`
	ResourceTypesJSON         string    `gorm:"column:resource_types_json;type:text;not null;default:'[\"dataset\",\"chat\",\"agent\",\"search_app\"]'" json:"resource_types"`
	ScopeJSON                 string    `gorm:"column:scope_json;type:text;not null;default:'{\"scope_type\":\"ALL_AUTHORIZED\",\"tenant_ids\":[]}'" json:"scope"`
	IntervalSeconds           int       `gorm:"column:interval_seconds;not null;default:900" json:"interval_seconds"`
	BatchSize                 int       `gorm:"column:batch_size;not null;default:100" json:"batch_size"`
	MaxResources              int       `gorm:"column:max_resources;not null;default:5000" json:"max_resources"`
	DeletionConfirmations     int       `gorm:"column:deletion_confirmations;not null;default:3" json:"deletion_confirmations"`
	DefaultTargetTenantID     string    `gorm:"column:default_target_tenant_id;size:32;index" json:"default_target_tenant_id"`
	DefaultOwnerID            string    `gorm:"column:default_owner_id;size:32;index" json:"default_owner_id"`
	CreatedAt                 time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt                 time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ResourceSyncSetting) TableName() string { return "rgx_resource_sync_settings" }

// SyncRun records one scan/import/reconcile attempt.
type SyncRun struct {
	ID                      string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	SourceID                string     `gorm:"column:source_id;size:64;not null;index" json:"source_id"`
	SourceCredentialVersion string     `gorm:"column:source_credential_version;size:64;not null" json:"source_credential_version"`
	TriggerType             string     `gorm:"column:trigger_type;size:32;not null" json:"trigger_type"`
	Status                  string     `gorm:"column:status;size:24;not null;default:pending;index" json:"status"`
	ResourceTypesJSON       string     `gorm:"column:resource_types_json;type:text;not null" json:"resource_types"`
	ScopeJSON               string     `gorm:"column:scope_json;type:text;not null" json:"scope"`
	ScanStartedAt           *time.Time `gorm:"column:scan_started_at" json:"scan_started_at"`
	ScanCompletedAt         *time.Time `gorm:"column:scan_completed_at" json:"scan_completed_at"`
	ScanConsistency         string     `gorm:"column:scan_consistency;size:32;not null;default:incomplete" json:"scan_consistency"`
	DeletionSafe            bool       `gorm:"column:deletion_safe;not null;default:false" json:"deletion_safe"`
	SourceSnapshotRef       string     `gorm:"column:source_snapshot_ref;size:96" json:"source_snapshot_ref"`
	SourceSnapshotJSON      string     `gorm:"column:source_snapshot_json;type:text" json:"source_snapshot"`
	PlanSummaryJSON         string     `gorm:"column:plan_summary_json;type:text" json:"plan_summary"`
	ResultSummaryJSON       string     `gorm:"column:result_summary_json;type:text" json:"result_summary"`
	ProgressTotal           int        `gorm:"column:progress_total;not null;default:0" json:"progress_total"`
	ProgressDone            int        `gorm:"column:progress_done;not null;default:0" json:"progress_done"`
	ProgressFailed          int        `gorm:"column:progress_failed;not null;default:0" json:"progress_failed"`
	Error                   string     `gorm:"column:error;type:text" json:"error"`
	LeaseOwner              string     `gorm:"column:lease_owner;size:128" json:"lease_owner"`
	LeaseExpiresAt          *time.Time `gorm:"column:lease_expires_at" json:"lease_expires_at"`
	FencingToken            int64      `gorm:"column:fencing_token;not null;default:0" json:"fencing_token"`
	StartedAt               *time.Time `gorm:"column:started_at" json:"started_at"`
	FinishedAt              *time.Time `gorm:"column:finished_at" json:"finished_at"`
	CreatedBy               string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt               time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt               time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (SyncRun) TableName() string { return "rgx_sync_run" }

// SyncItem is one resource's plan/execution evidence for a run. Its hashes are
// run evidence only; cross-run conflict baselines live on ResourceBinding.
type SyncItem struct {
	ID                     string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	RunID                  string     `gorm:"column:run_id;size:32;not null;index" json:"run_id"`
	ResourceType           string     `gorm:"column:resource_type;size:24;not null;index" json:"resource_type"`
	ExternalScopeKey       string     `gorm:"column:external_scope_key;size:128;not null;index" json:"external_scope_key"`
	ExternalID             string     `gorm:"column:external_id;size:64;not null;index" json:"external_id"`
	ExternalTenantID       string     `gorm:"column:external_tenant_id;size:128;not null;default:'__GLOBAL__'" json:"external_tenant_id"`
	ExternalVersion        string     `gorm:"column:external_version;size:64" json:"external_version"`
	ExternalUpdatedAt      *time.Time `gorm:"column:external_updated_at" json:"external_updated_at"`
	LocalID                string     `gorm:"column:local_id;size:64" json:"local_id"`
	TenantID               string     `gorm:"column:tenant_id;size:32;index" json:"tenant_id"`
	WorkspaceID            string     `gorm:"column:workspace_id;size:32" json:"workspace_id"`
	OwnerID                string     `gorm:"column:owner_id;size:32" json:"owner_id"`
	Action                 string     `gorm:"column:action;size:24;not null" json:"action"`
	Status                 string     `gorm:"column:status;size:16;not null;default:pending;index" json:"status"`
	ConflictType           string     `gorm:"column:conflict_type;size:24;not null;default:none" json:"conflict_type"`
	Error                  string     `gorm:"column:error;type:text" json:"error"`
	UpstreamCurrentHash    string     `gorm:"column:upstream_current_hash;size:64;not null" json:"upstream_current_hash"`
	UpstreamLastSyncedHash string     `gorm:"column:upstream_last_synced_hash;size:64;not null" json:"upstream_last_synced_hash"`
	LocalCurrentHash       string     `gorm:"column:local_current_hash;size:64;not null" json:"local_current_hash"`
	LocalLastSyncedHash    string     `gorm:"column:local_last_synced_hash;size:64;not null" json:"local_last_synced_hash"`
	PayloadDiffJSON        string     `gorm:"column:payload_diff_json;type:text" json:"payload_diff"`
	PayloadDiffRef         string     `gorm:"column:payload_diff_ref;size:96" json:"payload_diff_ref"`
	CreatedAt              time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt              time.Time  `gorm:"column:updated_at" json:"updated_at"`

	// SyncState is service-derived synchronization evidence for UI use.
	SyncState string `gorm:"-" json:"sync_state"`
}

func (SyncItem) TableName() string { return "rgx_sync_item" }

type ResourceSyncTenantMapping struct {
	ID               string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	SourceID         string    `gorm:"column:source_id;size:64;not null;uniqueIndex:uk_resource_sync_tenant_mapping,priority:1" json:"source_id"`
	ExternalTenantID string    `gorm:"column:external_tenant_id;size:128;not null;default:'__GLOBAL__';uniqueIndex:uk_resource_sync_tenant_mapping,priority:2" json:"external_tenant_id"`
	TargetTenantID   string    `gorm:"column:target_tenant_id;size:32;not null;index" json:"target_tenant_id"`
	Status           string    `gorm:"column:status;size:24;not null;default:active" json:"status"`
	CreatedBy        string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt        time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ResourceSyncTenantMapping) TableName() string { return "rgx_resource_sync_tenant_mapping" }

// ResourceBinding is the identity entity and current pointer for an external
// resource. It also owns the cross-run conflict baseline.
type ResourceBinding struct {
	ID                      string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	SourceID                string     `gorm:"column:source_id;size:64;not null;uniqueIndex:uk_resource_binding_identity,priority:1" json:"source_id"`
	ResourceType            string     `gorm:"column:resource_type;size:24;not null;uniqueIndex:uk_resource_binding_identity,priority:2" json:"resource_type"`
	ExternalScopeKey        string     `gorm:"column:external_scope_key;size:128;not null;uniqueIndex:uk_resource_binding_identity,priority:3" json:"external_scope_key"`
	ExternalID              string     `gorm:"column:external_id;size:64;not null;uniqueIndex:uk_resource_binding_identity,priority:4" json:"external_id"`
	ExternalTenantID        string     `gorm:"column:external_tenant_id;size:128;not null;default:'__GLOBAL__'" json:"external_tenant_id"`
	LocalType               string     `gorm:"column:local_type;size:24;not null" json:"local_type"`
	LocalID                 string     `gorm:"column:local_id;size:64;not null;index" json:"local_id"`
	TenantID                string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ExternalIdentityVersion int64      `gorm:"column:external_identity_version;not null;default:1" json:"external_identity_version"`
	CurrentBindingVersionID *string    `gorm:"column:current_binding_version_id;size:32" json:"current_binding_version_id"`
	LastSyncedUpstreamHash  string     `gorm:"column:last_synced_upstream_hash;size:64;not null;default:''" json:"last_synced_upstream_hash"`
	LastSyncedLocalHash     string     `gorm:"column:last_synced_local_hash;size:64;not null;default:''" json:"last_synced_local_hash"`
	ConflictType            string     `gorm:"column:conflict_type;size:24;not null;default:none" json:"conflict_type"`
	BindingLifecycle        string     `gorm:"column:binding_lifecycle;size:24;not null;default:active;index" json:"binding_lifecycle"`
	GovernanceState         string     `gorm:"column:governance_state;size:24;not null;default:pending_review;index" json:"governance_state"`
	MissingConfirmations    int        `gorm:"column:missing_confirmations;not null;default:0" json:"missing_confirmations"`
	ActiveMutationRunID     string     `gorm:"column:active_mutation_run_id;size:32;index" json:"active_mutation_run_id"`
	MutationFencingToken    int64      `gorm:"column:mutation_fencing_token;not null;default:0" json:"mutation_fencing_token"`
	LastSyncedAt            *time.Time `gorm:"column:last_synced_at" json:"last_synced_at"`
	LastSeenAt              *time.Time `gorm:"column:last_seen_at" json:"last_seen_at"`
	CreatedAt               time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt               time.Time  `gorm:"column:updated_at" json:"updated_at"`

	// SyncState is derived from lifecycle, governance, and baseline evidence;
	// it is intentionally not persisted.
	SyncState string `gorm:"-" json:"sync_state"`
}

func (ResourceBinding) TableName() string { return "rgx_resource_binding" }

// ResourceBindingVersion is append-only. Repository code must never expose
// update or delete operations for this model.
type ResourceBindingVersion struct {
	ID                      string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	BindingID               string     `gorm:"column:binding_id;size:32;not null;uniqueIndex:uk_resource_binding_version,priority:1;index" json:"binding_id"`
	Version                 int64      `gorm:"column:version;not null;uniqueIndex:uk_resource_binding_version,priority:2" json:"version"`
	UpstreamHash            string     `gorm:"column:upstream_hash;size:64;not null" json:"upstream_hash"`
	UpstreamPayloadRef      string     `gorm:"column:upstream_payload_ref;size:96" json:"upstream_payload_ref"`
	UpstreamPayloadJSON     string     `gorm:"column:upstream_payload_json;type:text;not null" json:"upstream_payload"`
	SourceCredentialVersion string     `gorm:"column:source_credential_version;size:64;not null" json:"source_credential_version"`
	ExternalVersion         string     `gorm:"column:external_version;size:64" json:"external_version"`
	ExternalUpdatedAt       *time.Time `gorm:"column:external_updated_at" json:"external_updated_at"`
	SyncRunID               string     `gorm:"column:sync_run_id;size:32;not null;index" json:"sync_run_id"`
	SyncItemID              string     `gorm:"column:sync_item_id;size:32;not null;index" json:"sync_item_id"`
	CreatedBy               string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt               time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt               time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (ResourceBindingVersion) TableName() string { return "rgx_resource_binding_version" }

// ResourceSyncArtifact is immutable, content-addressed normalized evidence.
// It never stores provider credentials, user messages, or document/chunk text.
type ResourceSyncArtifact struct {
	ID                      string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	ArtifactType            string     `gorm:"column:artifact_type;size:32;not null;uniqueIndex:uk_resource_sync_artifact_content,priority:1" json:"artifact_type"`
	ResourceType            string     `gorm:"column:resource_type;size:24" json:"resource_type"`
	SourceID                string     `gorm:"column:source_id;size:64;not null;index" json:"source_id"`
	SourceCredentialVersion string     `gorm:"column:source_credential_version;size:64;not null" json:"source_credential_version"`
	SchemaVersion           int        `gorm:"column:schema_version;not null;default:1" json:"schema_version"`
	ContentHash             string     `gorm:"column:content_hash;size:64;not null;uniqueIndex:uk_resource_sync_artifact_content,priority:2" json:"content_hash"`
	ContentJSON             string     `gorm:"column:content_json;type:text;not null" json:"content"`
	SyncRunID               string     `gorm:"column:sync_run_id;size:32;index" json:"sync_run_id"`
	SyncItemID              string     `gorm:"column:sync_item_id;size:32;index" json:"sync_item_id"`
	BindingVersionID        string     `gorm:"column:binding_version_id;size:32;index" json:"binding_version_id"`
	RetainUntil             *time.Time `gorm:"column:retain_until" json:"retain_until"`
	CreatedAt               time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt               time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (ResourceSyncArtifact) TableName() string { return "rgx_resource_sync_artifact" }

// ResourceSyncArtifactLifecycle is the mutable safety horizon for an otherwise
// immutable artifact. It may only be extended when the same content is reused
// by later evidence records; it is never used to mutate artifact content.
type ResourceSyncArtifactLifecycle struct {
	ArtifactID  string     `gorm:"column:artifact_id;primaryKey;size:32" json:"artifact_id"`
	RetainUntil *time.Time `gorm:"column:retain_until" json:"retain_until"`
	CreatedAt   time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (ResourceSyncArtifactLifecycle) TableName() string {
	return "rgx_resource_sync_artifact_lifecycle"
}
