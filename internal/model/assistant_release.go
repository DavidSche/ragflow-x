package model

import "time"

const (
	AssistantLifecycleEnabled   = "ENABLED"
	AssistantLifecycleDisabled  = "DISABLED"
	AssistantLifecycleSuspended = "SUSPENDED"
	AssistantLifecycleArchived  = "ARCHIVED"

	ReleaseDraft              = "DRAFT"
	ReleaseValidating         = "VALIDATING"
	ReleaseSnapshotted        = "SNAPSHOTTED"
	ReleaseEvaluating         = "EVALUATING"
	ReleaseGated              = "GATED"
	ReleaseApproved           = "APPROVED"
	ReleaseApplying           = "APPLYING"
	ReleaseVerifyFailed       = "VERIFY_FAILED"
	ReleaseApplyingFailed     = "APPLY_FAILED"
	ReleaseVerifying          = "VERIFYING"
	ReleaseVerified           = "VERIFIED"
	ReleaseCanaryActive       = "CANARY_ACTIVE"
	ReleaseActive             = "ACTIVE"
	ReleaseRetired            = "RETIRED"
	ReleaseRollingBack        = "ROLLING_BACK"
	ReleaseRollbackFailed     = "ROLLBACK_FAILED"
	ReleaseCompensating       = "COMPENSATING"
	ReleaseCompensated        = "COMPENSATED"
	ReleaseCompensationFailed = "COMPENSATION_FAILED"

	ReconcileConsistent = "CONSISTENT"
	ReconcileDrifted    = "DRIFTED"
	ReconcileUnknown    = "UNKNOWN"

	OperationCreate        = "CREATE"
	OperationApply         = "APPLY"
	OperationActivate      = "ACTIVATE"
	OperationPromoteCanary = "PROMOTE_CANARY"
	OperationRollback      = "ROLLBACK"
	OperationReconcile     = "RECONCILE"
	OperationCompensate    = "COMPENSATE"
	OperationRetire        = "RETIRE"

	OperationPending            = "PENDING"
	OperationRunning            = "RUNNING"
	OperationWaitingGate        = "WAITING_GATE"
	OperationWaitingApproval    = "WAITING_APPROVAL"
	OperationWaitingWindow      = "WAITING_WINDOW"
	OperationSucceeded          = "SUCCEEDED"
	OperationFailed             = "FAILED"
	OperationCancelled          = "CANCELLED"
	OperationCompensated        = "COMPENSATED"
	OperationCompensationFailed = "COMPENSATION_FAILED"

	CapabilityKnowledgeChat  = "knowledge_chat"
	CapabilityAgenticTask    = "agentic_task"
	CapabilityExplicitSearch = "explicit_search"

	AssistantRuntimeHealthHealthy     = "HEALTHY"
	AssistantRuntimeHealthDegraded    = "DEGRADED"
	AssistantRuntimeHealthUnavailable = "UNAVAILABLE"

	RolloutStagePending    = "PENDING"
	RolloutStageRunning    = "RUNNING"
	RolloutStageCompleted  = "COMPLETED"
	RolloutStageCancelled  = "CANCELLED"
	RolloutStageRolledBack = "ROLLED_BACK"

	AuthorizationAllow = "ALLOW"
	AuthorizationDeny  = "DENY"
	AuthorizationError = "ERROR"
)

// TemplateInstance is the mutable project-side governance projection of a
// template instantiation. It never owns upstream resources or the stable
// release fact; Assistant.current_assistant_release_id owns that fact.
type TemplateInstance struct {
	ID                        string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                  string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID                 string `gorm:"column:project_id;size:32;not null;index" json:"project_id"`
	TemplateID                string `gorm:"column:template_id;size:32;not null;index" json:"template_id"`
	TemplateVersionID         string `gorm:"column:template_version_id;size:32;not null;index" json:"template_version_id"`
	AssistantID               string `gorm:"column:assistant_id;size:32;not null;index" json:"assistant_id"`
	CurrentAssistantReleaseID string `gorm:"column:current_assistant_release_id;size:32;index" json:"current_assistant_release_id"`
	GovernanceStatus          string `gorm:"column:governance_status;size:32;not null;default:DRAFT;index" json:"governance_status"`
	OwnerID                   string `gorm:"column:owner_id;size:32;not null;index" json:"owner_id"`
	// NameKey records the requested instance name so instantiation replays can
	// find the same projection (doc/124 §2.2); it is not the assistant name.
	NameKey   string    `gorm:"column:name_key;size:192;index" json:"name_key"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (TemplateInstance) TableName() string { return "rgx_template_instance" }

// ScenarioPackVersion is the immutable packaged declaration asset.
type ScenarioPackVersion struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	TemplateID         string    `gorm:"column:template_id;size:32;not null;index" json:"template_id"`
	TemplateVersionID  string    `gorm:"column:template_version_id;size:32;not null;index" json:"template_version_id"`
	SchemaVersion      string    `gorm:"column:schema_version;size:32;not null" json:"schema_version"`
	MinRuntimeSchema   string    `gorm:"column:min_runtime_schema;size:32;not null" json:"min_runtime_schema"`
	MigrationVersion   string    `gorm:"column:migration_version;size:32;not null" json:"migration_version"`
	CompatibilityLevel string    `gorm:"column:compatibility_level;size:32;not null" json:"compatibility_level"`
	PayloadJSON        string    `gorm:"column:payload_json;type:text;not null" json:"payload_json"`
	PayloadHash        string    `gorm:"column:payload_hash;size:64;not null" json:"payload_hash"`
	HashAlgorithm      string    `gorm:"column:hash_algorithm;size:16;not null;default:SHA256" json:"hash_algorithm"`
	CreatedBy          string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ScenarioPackVersion) TableName() string { return "rgx_scenario_pack_version" }

// Assistant is the unified business entry object and stable-release pointer.
type Assistant struct {
	ID                        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                  string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID                 string    `gorm:"column:project_id;size:32;not null;index" json:"project_id"`
	TemplateInstanceID        string    `gorm:"column:template_instance_id;size:32;index" json:"template_instance_id"`
	ScenarioTemplateID        string    `gorm:"column:scenario_template_id;size:32;index" json:"scenario_template_id"`
	Name                      string    `gorm:"column:name;size:192;not null" json:"name"`
	Description               string    `gorm:"column:description;type:text" json:"description"`
	OwnerID                   string    `gorm:"column:owner_id;size:32;not null;index" json:"owner_id"`
	RiskLevel                 string    `gorm:"column:risk_level;size:16;not null;default:low" json:"risk_level"`
	LifecycleStatus           string    `gorm:"column:lifecycle_status;size:16;not null;default:ENABLED;index" json:"lifecycle_status"`
	CurrentAssistantReleaseID string    `gorm:"column:current_assistant_release_id;size:32;index" json:"current_assistant_release_id"`
	MetadataJSON              string    `gorm:"column:metadata_json;type:text" json:"metadata_json"`
	CreatedAt                 time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt                 time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (Assistant) TableName() string { return "rgx_assistant" }

// AssistantVersion is an immutable declaration version.
type AssistantVersion struct {
	ID                      string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantID             string    `gorm:"column:assistant_id;size:32;not null;uniqueIndex:uk_assistant_version" json:"assistant_id"`
	Version                 int64     `gorm:"column:version;not null;uniqueIndex:uk_assistant_version" json:"version"`
	TemplateVersionID       string    `gorm:"column:template_version_id;size:32;not null;index" json:"template_version_id"`
	ScenarioPackVersionID   string    `gorm:"column:scenario_pack_version_id;size:32;not null;index" json:"scenario_pack_version_id"`
	ExecutionContractSchema string    `gorm:"column:execution_contract_schema;size:32;not null" json:"execution_contract_schema"`
	ExecutionContractJSON   string    `gorm:"column:execution_contract_json;type:text;not null" json:"execution_contract_json"`
	ExecutionContractHash   string    `gorm:"column:execution_contract_hash;size:64;not null" json:"execution_contract_hash"`
	PolicyVersion           string    `gorm:"column:policy_version;size:64;not null" json:"policy_version"`
	RiskLevel               string    `gorm:"column:risk_level;size:16;not null;default:low" json:"risk_level"`
	CreatedBy               string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt               time.Time `gorm:"column:created_at" json:"created_at"`
}

func (AssistantVersion) TableName() string { return "rgx_assistant_version" }

// AssistantRelease is an immutable runtime snapshot. Only reconciliation and
// workflow state columns may change; declaration and snapshot identity are frozen.
type AssistantRelease struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantID        string     `gorm:"column:assistant_id;size:32;not null;index:uk_assistant_release_version" json:"assistant_id"`
	AssistantVersionID string     `gorm:"column:assistant_version_id;size:32;not null;index" json:"assistant_version_id"`
	ReleaseVersion     int64      `gorm:"column:release_version;not null;uniqueIndex:uk_assistant_release_version" json:"release_version"`
	ReleaseState       string     `gorm:"column:release_state;size:32;not null;default:DRAFT;index" json:"release_state"`
	DesiredStateJSON   string     `gorm:"column:desired_state_json;type:text;not null" json:"desired_state_json"`
	DesiredStateHash   string     `gorm:"column:desired_state_hash;size:64;not null" json:"desired_state_hash"`
	ActualStateJSON    string     `gorm:"column:actual_state_json;type:text" json:"actual_state_json"`
	ActualStateHash    string     `gorm:"column:actual_state_hash;size:64" json:"actual_state_hash"`
	ActualObservedAt   *time.Time `gorm:"column:actual_observed_at" json:"actual_observed_at"`
	ProviderVersion    string     `gorm:"column:provider_version;size:64" json:"provider_version"`
	ProviderRequestID  string     `gorm:"column:provider_request_id;size:128" json:"provider_request_id"`
	ReconcileStatus    string     `gorm:"column:reconcile_status;size:16;not null;default:UNKNOWN;index" json:"reconcile_status"`
	SnapshotManifestID string     `gorm:"column:snapshot_manifest_id;size:32;not null;index" json:"snapshot_manifest_id"`
	PreviousReleaseID  string     `gorm:"column:previous_release_id;size:32;index" json:"previous_release_id"`
	FencingToken       int64      `gorm:"column:fencing_token;not null;default:0" json:"fencing_token"`
	OptimisticVersion  int64      `gorm:"column:optimistic_version;not null;default:1" json:"optimistic_version"`
	EvidenceBundleID   string     `gorm:"column:evidence_bundle_id;size:32;index" json:"evidence_bundle_id"`
	GateDecisionID     string     `gorm:"column:gate_decision_id;size:32;index" json:"gate_decision_id"`
	GateResult         string     `gorm:"column:gate_result;size:32" json:"gate_result"`
	ApprovalID         string     `gorm:"column:approval_id;size:32;index" json:"approval_id"`
	CreatedBy          string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt          time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (AssistantRelease) TableName() string { return "rgx_assistant_release" }

// CapabilityBindingVersion materializes capability to upstream target binding.
type CapabilityBindingVersion struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantReleaseID string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	BindingID          string    `gorm:"column:binding_id;size:64;not null" json:"binding_id"`
	Version            int64     `gorm:"column:version;not null;default:1" json:"version"`
	Capability         string    `gorm:"column:capability;size:32;not null;index" json:"capability"`
	Adapter            string    `gorm:"column:adapter;size:64;not null" json:"adapter"`
	TargetType         string    `gorm:"column:target_type;size:32;not null;index" json:"target_type"`
	TargetID           string    `gorm:"column:target_id;size:64;not null;index" json:"target_id"`
	TargetVersion      string    `gorm:"column:target_version;size:64" json:"target_version"`
	RGXResourceID      string    `gorm:"column:rgx_resource_id;size:64" json:"rgx_resource_id"`
	ProjectID          string    `gorm:"column:project_id;size:32;not null;index" json:"project_id"`
	OwnershipVerified  bool      `gorm:"column:ownership_verified;not null;default:false" json:"ownership_verified"`
	PolicyID           string    `gorm:"column:policy_id;size:64" json:"policy_id"`
	ConfigJSON         string    `gorm:"column:config_json;type:text" json:"config_json"`
	Status             string    `gorm:"column:status;size:16;not null;default:ACTIVE" json:"status"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (CapabilityBindingVersion) TableName() string { return "rgx_capability_binding_version" }

// DatasetBindingVersion freezes the knowledge resources used by a release.
type DatasetBindingVersion struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantReleaseID string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	BindingID          string    `gorm:"column:binding_id;size:64;not null" json:"binding_id"`
	Version            int64     `gorm:"column:version;not null;default:1" json:"version"`
	KnowledgeDomainID  string    `gorm:"column:knowledge_domain_id;size:64" json:"knowledge_domain_id"`
	DatasetID          string    `gorm:"column:dataset_id;size:64;not null;index" json:"dataset_id"`
	DatasetVersion     string    `gorm:"column:dataset_version;size:64" json:"dataset_version"`
	FreshnessPolicy    string    `gorm:"column:freshness_policy;type:text" json:"freshness_policy"`
	SensitivityLevel   string    `gorm:"column:sensitivity_level;size:16;not null;default:internal" json:"sensitivity_level"`
	ConfigJSON         string    `gorm:"column:config_json;type:text" json:"config_json"`
	Status             string    `gorm:"column:status;size:16;not null;default:ACTIVE" json:"status"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (DatasetBindingVersion) TableName() string { return "rgx_dataset_binding_version" }

// ModelRouteBindingVersion freezes the model routing used by a release.
type ModelRouteBindingVersion struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantReleaseID string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	BindingID          string    `gorm:"column:binding_id;size:64;not null" json:"binding_id"`
	Version            int64     `gorm:"column:version;not null;default:1" json:"version"`
	ProviderID         string    `gorm:"column:provider_id;size:64;not null;index" json:"provider_id"`
	ModelID            string    `gorm:"column:model_id;size:64;not null;index" json:"model_id"`
	ModelVersion       string    `gorm:"column:model_version;size:64" json:"model_version"`
	RouteWeight        float64   `gorm:"column:route_weight;not null;default:1" json:"route_weight"`
	FallbackModelsJSON string    `gorm:"column:fallback_models_json;type:text" json:"fallback_models_json"`
	FallbackPolicyID   string    `gorm:"column:fallback_policy_id;size:64" json:"fallback_policy_id"`
	ConfigJSON         string    `gorm:"column:config_json;type:text" json:"config_json"`
	Status             string    `gorm:"column:status;size:16;not null;default:ACTIVE" json:"status"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ModelRouteBindingVersion) TableName() string { return "rgx_model_route_binding_version" }

// ToolBindingVersion freezes tool and external-system bindings.
type ToolBindingVersion struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantReleaseID string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	BindingID          string    `gorm:"column:binding_id;size:64;not null" json:"binding_id"`
	Version            int64     `gorm:"column:version;not null;default:1" json:"version"`
	ToolID             string    `gorm:"column:tool_id;size:64;not null;index" json:"tool_id"`
	ToolVersion        string    `gorm:"column:tool_version;size:64" json:"tool_version"`
	RiskLevel          string    `gorm:"column:risk_level;size:16;not null;default:low" json:"risk_level"`
	ApprovalPolicyID   string    `gorm:"column:approval_policy_id;size:64" json:"approval_policy_id"`
	ConfigJSON         string    `gorm:"column:config_json;type:text" json:"config_json"`
	Status             string    `gorm:"column:status;size:16;not null;default:ACTIVE" json:"status"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ToolBindingVersion) TableName() string { return "rgx_tool_binding_version" }

// RuntimeProfileSnapshot freezes compiled adapter execution parameters.
type RuntimeProfileSnapshot struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantReleaseID string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	SchemaVersion      string    `gorm:"column:schema_version;size:32;not null" json:"schema_version"`
	ProfileJSON        string    `gorm:"column:profile_json;type:text;not null" json:"profile_json"`
	ProfileHash        string    `gorm:"column:profile_hash;size:64;not null" json:"profile_hash"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (RuntimeProfileSnapshot) TableName() string { return "rgx_runtime_profile_snapshot" }

// PolicySnapshot freezes permission, risk, quality and degradation policy.
type PolicySnapshot struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantReleaseID string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	PolicyVersion      string    `gorm:"column:policy_version;size:64;not null" json:"policy_version"`
	PolicyJSON         string    `gorm:"column:policy_json;type:text;not null" json:"policy_json"`
	PolicyHash         string    `gorm:"column:policy_hash;size:64;not null" json:"policy_hash"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (PolicySnapshot) TableName() string { return "rgx_policy_snapshot" }

// SnapshotManifest is the complete, hash-verifiable list of release references.
type SnapshotManifest struct {
	ID                    string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID              string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantReleaseID    string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	SchemaVersion         string    `gorm:"column:schema_version;size:32;not null" json:"schema_version"`
	AssistantVersionID    string    `gorm:"column:assistant_version_id;size:32;not null;index" json:"assistant_version_id"`
	ScenarioPackVersionID string    `gorm:"column:scenario_pack_version_id;size:32;not null;index" json:"scenario_pack_version_id"`
	ManifestJSON          string    `gorm:"column:manifest_json;type:text;not null" json:"manifest_json"`
	SnapshotHash          string    `gorm:"column:snapshot_hash;size:64;not null" json:"snapshot_hash"`
	Complete              bool      `gorm:"column:complete;not null;default:false;index" json:"complete"`
	CreatedAt             time.Time `gorm:"column:created_at" json:"created_at"`
}

func (SnapshotManifest) TableName() string { return "rgx_snapshot_manifest" }

// ReleaseOperation records an operation without changing release snapshot semantics.
type ReleaseOperation struct {
	ID                        string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                  string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ReleaseID                 string     `gorm:"column:release_id;size:32;not null;index" json:"release_id"`
	SourceReleaseID           string     `gorm:"column:source_release_id;size:32;index" json:"source_release_id"`
	TargetReleaseID           string     `gorm:"column:target_release_id;size:32;index" json:"target_release_id"`
	OperationType             string     `gorm:"column:operation_type;size:32;not null;index" json:"operation_type"`
	IdempotencyKey            string     `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:uk_release_operation_idempotency" json:"idempotency_key"`
	RequestFingerprint        string     `gorm:"column:request_fingerprint;size:64" json:"request_fingerprint"`
	Attempt                   int64      `gorm:"column:attempt;not null;default:1" json:"attempt"`
	FencingToken              int64      `gorm:"column:fencing_token;not null;default:0" json:"fencing_token"`
	ExpectedCurrentReleaseID  string     `gorm:"column:expected_current_release_id;size:32" json:"expected_current_release_id"`
	OperationState            string     `gorm:"column:operation_state;size:32;not null;default:PENDING;index" json:"operation_state"`
	CurrentStep               string     `gorm:"column:current_step;size:32" json:"current_step"`
	ErrorCode                 string     `gorm:"column:error_code;size:64" json:"error_code"`
	ErrorMessage              string     `gorm:"column:error_message;size:512" json:"error_message"`
	ProviderOperationRefsJSON string     `gorm:"column:provider_operation_refs_json;type:text" json:"provider_operation_refs_json"`
	StartedAt                 *time.Time `gorm:"column:started_at" json:"started_at"`
	FinishedAt                *time.Time `gorm:"column:finished_at" json:"finished_at"`
	CreatedBy                 string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt                 time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt                 time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (ReleaseOperation) TableName() string { return "rgx_release_operation" }

// RuntimeHealth is mutable dependency observation and is never release workflow state.
type RuntimeHealth struct {
	ID                  string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID            string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantID         string     `gorm:"column:assistant_id;size:32;not null;index" json:"assistant_id"`
	AssistantReleaseID  string     `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	BindingID           string     `gorm:"column:binding_id;size:64;not null;default:''" json:"binding_id"`
	Health              string     `gorm:"column:health;size:16;not null;index" json:"health"`
	Reason              string     `gorm:"column:reason;size:512" json:"reason"`
	Threshold           int64      `gorm:"column:threshold;not null;default:1" json:"threshold"`
	EvaluationWindowSec int64      `gorm:"column:evaluation_window_sec;not null;default:300" json:"evaluation_window_sec"`
	ConsecutiveCount    int64      `gorm:"column:consecutive_count;not null;default:0" json:"consecutive_count"`
	RecoveryThreshold   int64      `gorm:"column:recovery_threshold;not null;default:1" json:"recovery_threshold"`
	CooldownSec         int64      `gorm:"column:cooldown_sec;not null;default:60" json:"cooldown_sec"`
	LastRecoveredAt     *time.Time `gorm:"column:last_recovered_at" json:"last_recovered_at"`
	CheckedAt           time.Time  `gorm:"column:checked_at" json:"checked_at"`
	CreatedAt           time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt           time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (RuntimeHealth) TableName() string { return "rgx_runtime_health" }

// DependencyHealthSignal is the normalized adapter health event.
type DependencyHealthSignal struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	SignalID           string    `gorm:"column:signal_id;size:64;not null;uniqueIndex:uk_dependency_health_signal" json:"signal_id"`
	TraceID            string    `gorm:"column:trace_id;size:64;index" json:"trace_id"`
	RequestID          string    `gorm:"column:request_id;size:64;index" json:"request_id"`
	AssistantReleaseID string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	BindingID          string    `gorm:"column:binding_id;size:64" json:"binding_id"`
	DependencyType     string    `gorm:"column:dependency_type;size:32;not null;index" json:"dependency_type"`
	SignalType         string    `gorm:"column:signal_type;size:32;not null;index" json:"signal_type"`
	ObservedAt         time.Time `gorm:"column:observed_at;not null" json:"observed_at"`
	Threshold          int64     `gorm:"column:threshold;not null;default:1" json:"threshold"`
	Sample             string    `gorm:"column:sample;size:512" json:"sample"`
	Source             string    `gorm:"column:source;size:32;not null" json:"source"`
	Severity           string    `gorm:"column:severity;size:16;not null" json:"severity"`
	Scope              string    `gorm:"column:scope;size:16;not null;default:REQUEST" json:"scope"`
	RecommendedAction  string    `gorm:"column:recommended_action;size:64" json:"recommended_action"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

func (DependencyHealthSignal) TableName() string { return "rgx_dependency_health_signal" }

// RolloutPolicy holds only canary targeting and traffic policy. It does not
// duplicate the stable release fact.
type RolloutPolicy struct {
	ID                string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	AssistantID       string     `gorm:"column:assistant_id;size:32;not null;index" json:"assistant_id"`
	CanaryReleaseID   string     `gorm:"column:canary_release_id;size:32;not null;index" json:"canary_release_id"`
	Stage             string     `gorm:"column:stage;size:32;not null;default:PENDING" json:"stage"`
	Percentage        float64    `gorm:"column:percentage;not null;default:0" json:"percentage"`
	TargetingJSON     string     `gorm:"column:targeting_json;type:text" json:"targeting_json"`
	StartAt           *time.Time `gorm:"column:start_at" json:"start_at"`
	EndAt             *time.Time `gorm:"column:end_at" json:"end_at"`
	RollbackCondition string     `gorm:"column:rollback_condition;type:text" json:"rollback_condition"`
	Status            string     `gorm:"column:status;size:32;not null;default:PENDING;index" json:"status"`
	OptimisticVersion int64      `gorm:"column:optimistic_version;not null;default:1" json:"optimistic_version"`
	CreatedBy         string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt         time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (RolloutPolicy) TableName() string { return "rgx_rollout_policy" }

// AuthorizationSnapshot records an actual runtime authorization decision.
type AuthorizationSnapshot struct {
	ID                      string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProjectID               string    `gorm:"column:project_id;size:32;not null;index" json:"project_id"`
	TraceID                 string    `gorm:"column:trace_id;size:64;not null;index" json:"trace_id"`
	AuthorizationDecisionID string    `gorm:"column:authorization_decision_id;size:64;not null;uniqueIndex:uk_authorization_decision" json:"authorization_decision_id"`
	ParentDecisionID        string    `gorm:"column:parent_decision_id;size:64" json:"parent_decision_id"`
	RequestScope            string    `gorm:"column:request_scope;size:32;not null" json:"request_scope"`
	UserID                  string    `gorm:"column:user_id;size:32;not null;index" json:"user_id"`
	AssistantReleaseID      string    `gorm:"column:assistant_release_id;size:32;not null;index" json:"assistant_release_id"`
	Capability              string    `gorm:"column:capability;size:32;not null" json:"capability"`
	BindingID               string    `gorm:"column:binding_id;size:64" json:"binding_id"`
	ResourceType            string    `gorm:"column:resource_type;size:64;not null" json:"resource_type"`
	ResourceID              string    `gorm:"column:resource_id;size:64;not null" json:"resource_id"`
	ResourceVersion         string    `gorm:"column:resource_version;size:64" json:"resource_version"`
	PolicyVersion           string    `gorm:"column:policy_version;size:64;not null" json:"policy_version"`
	Decision                string    `gorm:"column:decision;size:16;not null;index" json:"decision"`
	DecisionTime            time.Time `gorm:"column:decision_time;not null" json:"decision_time"`
	CreatedAt               time.Time `gorm:"column:created_at" json:"created_at"`
}

func (AuthorizationSnapshot) TableName() string { return "rgx_authorization_snapshot" }

// TemplateRunbook captures release operations guidance for a template.
type TemplateRunbook struct {
	ID                string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	TemplateID        string    `gorm:"column:template_id;size:32;not null;index" json:"template_id"`
	TemplateVersionID string    `gorm:"column:template_version_id;size:32;not null;index" json:"template_version_id"`
	Title             string    `gorm:"column:title;size:192;not null" json:"title"`
	RunbookJSON       string    `gorm:"column:runbook_json;type:text;not null" json:"runbook_json"`
	CreatedBy         string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
}

func (TemplateRunbook) TableName() string { return "rgx_template_runbook" }
