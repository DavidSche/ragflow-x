package model

import "time"

const (
	RouteSelectionNew      = "NEW"
	RouteSelectionReserved = "RESERVED"
	RouteSelectionConsumed = "CONSUMED"
	RouteSelectionFailed   = "FAILED"
	RouteSelectionExpired  = "EXPIRED"

	BootstrapPending   = "PENDING"
	BootstrapReserved  = "RESERVED"
	BootstrapSucceeded = "SUCCEEDED"
	BootstrapFailed    = "FAILED"
	BootstrapExpired   = "EXPIRED"

	BootstrapTypeCreateSession = "CREATE_SESSION"

	RouteEvalSplitTrain      = "train"
	RouteEvalSplitValidation = "validation"
	RouteEvalSplitHoldout    = "holdout"

	RouteEvalCasePositive     = "positive"
	RouteEvalCaseHardNegative = "hard_negative"
	RouteEvalCaseAmbiguous    = "ambiguous"
	RouteEvalCasePermission   = "permission"

	RouteEvalGatePassed  = "passed"
	RouteEvalGateFailed  = "failed"
	RouteEvalGateInsuffi = "insufficient_samples"
)

// RouteDecision is a short-lived server-owned recommendation. It stores the
// candidate snapshot, not the user prompt.
type RouteDecision struct {
	ID                      string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                string    `gorm:"column:tenant_id;size:32;not null;index:idx_route_decision_actor" json:"tenant_id"`
	UserID                  string    `gorm:"column:user_id;size:32;not null;index:idx_route_decision_actor" json:"user_id"`
	QueryFingerprint        string    `gorm:"column:query_fingerprint;size:128;not null" json:"query_fingerprint"`
	QueryFingerprintVersion string    `gorm:"column:query_fingerprint_version;size:16;not null" json:"query_fingerprint_version"`
	QueryLength             int       `gorm:"column:query_length;not null" json:"query_length"`
	CandidatesJSON          string    `gorm:"column:candidates_json;type:text;not null" json:"candidates_json"`
	RouterVersion           string    `gorm:"column:router_version;size:32;not null" json:"router_version"`
	EffectiveMode           string    `gorm:"column:effective_mode;size:24;not null;default:suggest" json:"effective_mode"`
	ExpiresAt               time.Time `gorm:"column:expires_at;not null;index:idx_route_decision_expiry" json:"expires_at"`
	CreatedAt               time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (RouteDecision) TableName() string { return "rgx_conversation_route_decisions" }

// RouteSelection binds a user choice to one candidate. It never creates or
// authorizes a session by itself.
type RouteSelection struct {
	ID                   string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID             string    `gorm:"column:tenant_id;size:32;not null;index:idx_route_selection_actor;uniqueIndex:uk_route_selection_scope" json:"tenant_id"`
	UserID               string    `gorm:"column:user_id;size:32;not null;index:idx_route_selection_actor;uniqueIndex:uk_route_selection_scope" json:"user_id"`
	RouteID              string    `gorm:"column:route_id;size:32;not null;uniqueIndex:uk_route_selection_scope" json:"route_id"`
	Kind                 string    `gorm:"column:kind;size:16;not null" json:"kind"`
	TargetID             string    `gorm:"column:target_id;size:64;not null" json:"target_id"`
	CatalogVersion       int64     `gorm:"column:catalog_version;not null" json:"catalog_version"`
	State                string    `gorm:"column:state;size:16;not null;default:NEW" json:"state"`
	IdempotencyKey       string    `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:uk_route_selection_scope" json:"idempotency_key"`
	BootstrapOperationID string    `gorm:"column:bootstrap_operation_id;size:32" json:"bootstrap_operation_id"`
	ExpiresAt            time.Time `gorm:"column:expires_at;not null;index:idx_route_selection_expiry" json:"expires_at"`
	CreatedAt            time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (RouteSelection) TableName() string { return "rgx_conversation_route_selections" }

// BootstrapOperation is the cross-system idempotency record for creating a
// Chat or Agent session. The persisted reference prevents duplicate sessions.
type BootstrapOperation struct {
	ID               string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	UserID           string    `gorm:"column:user_id;size:32;not null" json:"user_id"`
	RouteSelectionID string    `gorm:"column:route_selection_id;size:32;not null;uniqueIndex:uk_bootstrap_selection_operation" json:"route_selection_id"`
	IdempotencyKey   string    `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:uk_bootstrap_selection_operation" json:"idempotency_key"`
	BootstrapType    string    `gorm:"column:bootstrap_type;size:24;not null" json:"bootstrap_type"`
	Kind             string    `gorm:"column:kind;size:16;not null" json:"kind"`
	TargetID         string    `gorm:"column:target_id;size:64;not null" json:"target_id"`
	State            string    `gorm:"column:state;size:16;not null;default:PENDING" json:"state"`
	SessionID        string    `gorm:"column:session_id;size:64" json:"session_id"`
	LastError        string    `gorm:"column:last_error;size:255" json:"last_error"`
	ExpiresAt        time.Time `gorm:"column:expires_at;not null" json:"expires_at"`
	CreatedAt        time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (BootstrapOperation) TableName() string { return "rgx_conversation_bootstrap_operations" }

// RouteEvaluationRun stores an immutable offline evaluation outcome. It never
// stores evaluation prompt text; cases remain governed by the eval source.
type RouteEvaluationRun struct {
	ID                  string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID            string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name                string    `gorm:"column:name;size:128;not null" json:"name"`
	Source              string    `gorm:"column:source;size:64;not null" json:"source"`
	ActorID             string    `gorm:"column:actor_id;size:32;not null" json:"actor_id"`
	RouterVersion       string    `gorm:"column:router_version;size:32;not null" json:"router_version"`
	CalibrationVersion  string    `gorm:"column:calibration_version;size:32;not null" json:"calibration_version"`
	NormalizationMethod string    `gorm:"column:normalization_method;size:32;not null" json:"normalization_method"`
	CaseCount           int       `gorm:"column:case_count;not null" json:"case_count"`
	MetricsJSON         string    `gorm:"column:metrics_json;type:text;not null" json:"metrics_json"`
	BoundaryJSON        string    `gorm:"column:boundary_json;type:text;not null" json:"boundary_json"`
	CalibrationJSON     string    `gorm:"column:calibration_json;type:text;not null;default:''" json:"calibration_json"`
	GateState           string    `gorm:"column:gate_state;size:32;not null" json:"gate_state"`
	AllowAutoLowRisk    bool      `gorm:"column:allow_auto_low_risk;not null;default:false" json:"allow_auto_low_risk"`
	CreatedAt           time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (RouteEvaluationRun) TableName() string { return "rgx_route_evaluation_runs" }
