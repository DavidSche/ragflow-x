package model

import (
	"time"

	"gorm.io/gorm"
)

// Data-source connector lifecycle (doc/225 §5). Providers are constrained to a
// deterministic first-release set; SaaS connectors enter through the adapter
// layer (doc/225 §8.1) rather than bespoke scanning code.
const (
	ConnectorProviderLocalFolder = "local_folder"
	ConnectorProviderS3          = "s3"
	ConnectorProviderWebDAV      = "webdav"

	// ConnectorAdapterNative executes the source scan inside RAGFlow-X, so all
	// governance semantics (preview, approval, cursor, run detail) are owned by
	// X (doc/227 §4.2.0).
	ConnectorAdapterNative = "native"
	// ConnectorAdapterRAGFlow reserves the RAGFlow-backed adapter slot. It is
	// modelled now so bindings never need a destructive migration when the
	// adapter lands, but this release fails closed instead of pretending the
	// remote connector semantics are equivalent (doc/227 C2.3).
	ConnectorAdapterRAGFlow = "ragflow"

	ConnectorScheduleManual   = "manual"
	ConnectorScheduleInterval = "interval"
	ConnectorScheduleDaily    = "daily"

	ConnectorStatusIdle     = "idle"
	ConnectorStatusDegraded = "degraded"
	ConnectorStatusDisabled = "disabled"

	ConnectorRunTriggerManual    = "manual"
	ConnectorRunTriggerScheduled = "scheduled"

	ConnectorRunStatusPending   = "pending"
	ConnectorRunStatusRunning   = "running"
	ConnectorRunStatusSucceeded = "succeeded"
	ConnectorRunStatusFailed    = "failed"
	ConnectorRunStatusSkipped   = "skipped"
	ConnectorRunStatusCancelled = "cancelled"

	// ConnectorRetentionDefaultDays is the default run-history retention (doc/225 §6.2 F4).
	ConnectorRetentionDefaultDays = 90
	// ConnectorDefaultMaxConcurrent bounds concurrent connector syncs process-wide
	// (doc/225 §6.2; overridable through connector.max_concurrent).
	ConnectorDefaultMaxConcurrent = 3
	// ConnectorDefaultRunTimeoutSec is the per-run hard deadline (doc/225 §6.2).
	ConnectorDefaultRunTimeoutSec = 1800
	// ConnectorDefaultTZOffsetMin is the default display offset (+08:00, doc/225 F7).
	ConnectorDefaultTZOffsetMin = 480
	// ConnectorMaxBatch is the per-poll provider scan budget before the cursor
	// advances and the loop re-checks context/cancellation (doc/225 §8 F5/F8).
	ConnectorMaxBatch = 100
)

// Connector is the workspace-scoped dataset-content ingestion runtime binding.
// Credentials are sealed with crypto.Encrypt under the shared EncryptKey and
// never serialized by API views (doc/225 §9 F1).
type Connector struct {
	ID       string `gorm:"column:id;primaryKey;size:64" json:"id"`
	TenantID string `gorm:"column:tenant_id;size:32;not null;index:idx_connector_tenant" json:"tenant_id"`
	Name     string `gorm:"column:name;size:128;not null" json:"name"`
	Provider string `gorm:"column:provider;size:32;not null;index" json:"provider"`
	// AdapterKind records who executes the source scan. Native keeps the whole
	// business loop inside X; ragflow is reserved for the Tier-B adapter and is
	// currently rejected at create/update time.
	AdapterKind     string `gorm:"column:adapter_kind;size:16;not null;default:native;index" json:"adapter_kind"`
	TargetDatasetID string `gorm:"column:target_dataset_id;size:32;not null;index:idx_connector_tenant" json:"target_dataset_id"`
	// SourceOwnershipKey is the canonical "one logical source, one connector"
	// identity (tenant + provider + target dataset + source target) and backs a
	// unique index. Credentials are deliberately excluded so rotation never
	// changes identity (doc/227 C2.3).
	SourceOwnershipKey string `gorm:"column:source_ownership_key;size:128;not null;default:''" json:"source_ownership_key"`
	// ExternalConnectorID is the remote connector id for a RAGFlow-backed
	// adapter. Empty for native bindings.
	ExternalConnectorID string `gorm:"column:external_connector_id;size:128;not null;default:''" json:"external_connector_id,omitempty"`
	SpecJSON            string `gorm:"column:spec_json;type:text;not null" json:"spec_json"`
	CredentialCrypted   string `gorm:"column:credential_crypted;type:text;not null" json:"-"`
	HasCredential       bool   `gorm:"column:has_credential;not null;default:false" json:"has_credential"`
	// CredentialVersion increments on every successful rotation so run/audit
	// evidence can be tied to the credential generation in use.
	CredentialVersion   int    `gorm:"column:credential_version;not null;default:0" json:"credential_version"`
	CursorJSON          string `gorm:"column:cursor_json;type:text;not null;default:'{}'" json:"-"`
	ScheduleKind        string `gorm:"column:schedule_kind;size:16;not null" json:"schedule_kind"`
	ScheduleValue       int    `gorm:"column:schedule_value;not null;default:0" json:"schedule_value"`
	ScheduleTZOffsetMin int    `gorm:"column:schedule_tz_offset_min;not null;default:0" json:"schedule_tz_offset_min"` // doc/225 F7
	Sensitive           bool   `gorm:"column:sensitive;not null;default:false" json:"sensitive"`
	// Enabled is the explicit scheduling switch (doc/225 §5.1). Status is the
	// runtime health signal (idle|degraded|disabled); keeping both avoids
	// overloading a health field with operator intent. The column default is
	// false (fail-closed for raw inserts); CreateConnector explicitly opts in,
	// because GORM replaces zero values with a true default and would silently
	// re-enable a connector created with enabled=false.
	Enabled       bool       `gorm:"column:enabled;not null;default:false;index" json:"enabled"`
	Status        string     `gorm:"column:status;size:16;not null;default:idle;index" json:"status"`
	LastRunAt     *time.Time `gorm:"column:last_run_at" json:"last_run_at,omitempty"`
	LastRunStatus string     `gorm:"column:last_run_status;size:16;not null;default:''" json:"last_run_status"`
	// CreatedBy is the user who submitted the binding (approval requester when
	// governed). It is immutable after creation.
	CreatedBy string         `gorm:"column:created_by;size:32;not null;default:''" json:"created_by"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`
	CreatedAt time.Time      `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (Connector) TableName() string { return "rgx_connector" }

// ConnectorRun is one sync execution record (doc/225 §5.2). Rows are pruned by
// the retention janitor (JobKindConnectorRetention) after the configurable
// retention window; removal never touches the ledger or target documents.
type ConnectorRun struct {
	ID          string `gorm:"column:id;primaryKey;size:64" json:"id"`
	ConnectorID string `gorm:"column:connector_id;size:64;not null;index:idx_connector_run_rel" json:"connector_id"`
	TenantID    string `gorm:"column:tenant_id;size:32;not null;index:idx_connector_run_tenant" json:"tenant_id"`
	Trigger     string `gorm:"column:trigger;size:16;not null" json:"trigger"`
	// TriggeredBy is the acting user for manual runs and "system" for scheduled
	// runs, so the run row is self-describing without joining audit logs.
	TriggeredBy string `gorm:"column:triggered_by;size:32;not null;default:''" json:"triggered_by"`
	// ExternalRunID is the remote run id for a RAGFlow-backed adapter, kept on
	// the X run row so operators never have to open the engine to explain a run.
	ExternalRunID string     `gorm:"column:external_run_id;size:128;not null;default:''" json:"external_run_id,omitempty"`
	Status        string     `gorm:"column:status;size:16;not null;index:idx_connector_run_rel" json:"status"`
	StartedAt     *time.Time `gorm:"column:started_at" json:"started_at,omitempty"`
	FinishedAt    *time.Time `gorm:"column:finished_at;index" json:"finished_at,omitempty"`
	Added         int        `gorm:"column:added;not null;default:0" json:"added"`
	Changed       int        `gorm:"column:changed;not null;default:0" json:"changed"`
	Deleted       int        `gorm:"column:deleted;not null;default:0" json:"deleted"`
	Skipped       int        `gorm:"column:skipped;not null;default:0" json:"skipped"`
	Failed        int        `gorm:"column:failed;not null;default:0" json:"failed"`
	DurationMS    int64      `gorm:"column:duration_ms;not null;default:0" json:"duration_ms"`
	ErrorSummary  string     `gorm:"column:error_summary;size:512;not null;default:''" json:"error_summary"`
	DetailJSON    string     `gorm:"column:detail_json;type:text;not null;default:'{}'" json:"-"`
	TraceID       string     `gorm:"column:trace_id;size:64;not null;default:''" json:"trace_id"`
	CreatedAt     time.Time  `gorm:"column:created_at;index:idx_connector_run_rel" json:"created_at"`
}

// TableName is the physical table name.
func (ConnectorRun) TableName() string { return "rgx_connector_run" }
