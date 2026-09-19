package model

import "time"

// TaskType enumerates queued operations.
const (
	TaskTypeUpload = "upload"
	TaskTypeParse  = "parse"
	TaskTypeStop   = "stop"
	TaskTypeDelete = "delete"
)

// TaskStatus enumerates task lifecycle states.
const (
	TaskStatusQueued  = "queued"
	TaskStatusRunning = "running"
	TaskStatusDone    = "done"
	TaskStatusFailed  = "failed"
	TaskStatusStopped = "stopped"
)

// Task is an async operation tracked by the control plane. RAGFlow remains the
// source of truth for engine-side progress; this table is the operator-facing
// queue projection.
type Task struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	DatasetID string    `gorm:"column:dataset_id;size:32;index" json:"dataset_id"`
	DocID     string    `gorm:"column:doc_id;size:64;index" json:"doc_id"`
	DocName   string    `gorm:"column:doc_name;size:255" json:"doc_name"`
	TaskType  string    `gorm:"column:task_type;size:32;not null" json:"task_type"`
	Status    string    `gorm:"column:status;size:32;not null;default:queued" json:"status"`
	Progress  int       `gorm:"column:progress;not null;default:0" json:"progress"`
	Detail    string    `gorm:"column:detail;type:text" json:"detail"`
	CreatedAt time.Time `gorm:"column:created_at;index" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (Task) TableName() string { return "rgx_task" }

// QuotaUsage is a daily per (tenant,user,key) aggregated usage row used for
// metering and usage reconciliation.
type QuotaUsage struct {
	ID                      string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_usage_scope;index" json:"tenant_id"`
	UserID                  string    `gorm:"column:user_id;size:32;uniqueIndex:idx_usage_scope;index" json:"user_id"`
	KeyID                   string    `gorm:"column:key_id;size:32;uniqueIndex:idx_usage_scope;index" json:"key_id"`
	RequestID               string    `gorm:"column:request_id;size:64;index" json:"request_id"`
	Date                    string    `gorm:"column:date;size:10;not null;uniqueIndex:idx_usage_scope;index" json:"date"`
	TokensIn                int64     `gorm:"column:tokens_in;not null;default:0" json:"tokens_in"`
	TokensOut               int64     `gorm:"column:tokens_out;not null;default:0" json:"tokens_out"`
	Requests                int64     `gorm:"column:requests;not null;default:0" json:"requests"`
	Cost                    float64   `gorm:"column:cost;not null;default:0" json:"estimated_cost"`
	Estimated               bool      `gorm:"column:estimated;not null;default:false" json:"estimated"`
	EstimationPolicyVersion string    `gorm:"column:estimation_policy_version;size:32" json:"estimation_policy_version"`
	CreatedAt               time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt               time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (QuotaUsage) TableName() string { return "rgx_quota_usage" }

// MeterRequest is an idempotency ledger for gateway metering: each unique
// request_id is recorded at most once so client/provider retries never
// double-count usage.
type MeterRequest struct {
	RequestID string    `gorm:"column:request_id;primaryKey;size:64" json:"request_id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null" json:"tenant_id"`
	UserID    string    `gorm:"column:user_id;size:32;not null" json:"user_id"`
	KeyID     string    `gorm:"column:key_id;size:32;not null" json:"key_id"`
	Date      string    `gorm:"column:date;size:10;not null" json:"date"`
	CreatedAt time.Time `gorm:"column:created_at;index" json:"created_at"`
}

// TableName is the physical table name.
func (MeterRequest) TableName() string { return "rgx_meter_request" }

// Gateway idempotency lifecycle states.
const (
	IdempotencyInProgress = "IN_PROGRESS"
	IdempotencyCompleted  = "COMPLETED"
	IdempotencyFailed     = "FAILED"
	IdempotencyExpired    = "EXPIRED"
)

// GatewayIdempotency is the caller-facing retry ledger. The unique key excludes
// fingerprint so a repeated key cannot create a second row; fingerprint is then
// compared to reject Method/Target/Body conflicts.
type GatewayIdempotency struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_gateway_idempotency_key" json:"tenant_id"`
	PrincipalID        string     `gorm:"column:principal_id;size:32;not null;uniqueIndex:idx_gateway_idempotency_key" json:"principal_id"`
	IdempotencyKey     string     `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:idx_gateway_idempotency_key" json:"idempotency_key"`
	RequestFingerprint string     `gorm:"column:request_fingerprint;size:64;not null" json:"request_fingerprint"`
	Status             string     `gorm:"column:status;size:16;not null;default:IN_PROGRESS" json:"status"`
	RequestID          string     `gorm:"column:request_id;size:64;not null;index" json:"request_id"`
	ResponseRef        string     `gorm:"column:response_ref;type:text" json:"response_ref"`
	CreatedAt          time.Time  `gorm:"column:created_at" json:"created_at"`
	ExpiresAt          time.Time  `gorm:"column:expires_at;not null;index" json:"expires_at"`
	CompletedAt        *time.Time `gorm:"column:completed_at" json:"completed_at"`
}

func (GatewayIdempotency) TableName() string { return "rgx_gateway_idempotency" }

// QuotaReservation lifecycle states.
const (
	QuotaReservationPending   = "pending"
	QuotaReservationFinalized = "finalized"
	QuotaReservationReleased  = "released"
)

// QuotaLimit is the configured monthly token and logical-request budget for a
// gateway API key and its running balances. A limit <= 0 means unlimited. The
// period is a UTC month identified by period_start ("2006-01"). Pending fields
// hold live, unreconciled reservations so concurrent in-flight requests cannot
// collectively exceed either budget.
type QuotaLimit struct {
	ID              string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID        string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	KeyID           string    `gorm:"column:key_id;size:32;not null;uniqueIndex:idx_quota_limit_period" json:"key_id"`
	PeriodStart     string    `gorm:"column:period_start;size:7;not null;uniqueIndex:idx_quota_limit_period" json:"period_start"`
	TokenLimit      int64     `gorm:"column:token_limit;not null;default:0" json:"token_limit"`
	TokensUsed      int64     `gorm:"column:tokens_used;not null;default:0" json:"tokens_used"`
	Pending         int64     `gorm:"column:pending;not null;default:0" json:"pending"`
	RequestLimit    int64     `gorm:"column:request_limit;not null;default:0" json:"request_limit"`
	RequestsUsed    int64     `gorm:"column:requests_used;not null;default:0" json:"requests_used"`
	PendingRequests int64     `gorm:"column:pending_requests;not null;default:0" json:"pending_requests"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (QuotaLimit) TableName() string { return "rgx_quota" }

// QuotaReservation is the idempotency + audit ledger for a pre-reserved token
// estimate per gateway request. It is keyed by request_id so client retries
// never double-reserve and reconciliation (FinalizeQuota/release) happens
// exactly once per reservation.
type QuotaReservation struct {
	RequestID    string `gorm:"column:request_id;primaryKey;size:64" json:"request_id"`
	TenantID     string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	KeyID        string `gorm:"column:key_id;size:32;not null;index" json:"key_id"`
	PeriodStart  string `gorm:"column:period_start;size:7;not null;index" json:"period_start"`
	Estimated    int64  `gorm:"column:estimated;not null;default:0" json:"estimated"`
	ActualTokens int64  `gorm:"column:actual_tokens;not null;default:0" json:"actual_tokens"`
	Status       string `gorm:"column:status;size:16;not null;default:pending" json:"status"`
	// Applied is true only after the atomic budget row was incremented. It
	// prevents release/reconciliation from draining balances for a request
	// rejected before reservation or interrupted before it could reserve.
	Applied   bool      `gorm:"column:applied;not null;default:false" json:"applied"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (QuotaReservation) TableName() string { return "rgx_quota_reservation" }

// CostMetric is a per-request metering detail row used for cost breakdown and
// reconciliation. It is idempotent by request_id and independent from the
// daily aggregate so per-model/scenario/session attribution can be computed
// without polluting the daily aggregation key.
type CostMetric struct {
	ID                      string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	RequestID               string    `gorm:"column:request_id;size:64;not null;uniqueIndex:idx_cost_metric_request" json:"request_id"`
	TenantID                string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	UserID                  string    `gorm:"column:user_id;size:32;not null;index" json:"user_id"`
	KeyID                   string    `gorm:"column:key_id;size:32;index" json:"key_id"`
	Date                    string    `gorm:"column:date;size:10;not null;index" json:"date"`
	Model                   string    `gorm:"column:model;size:128;index" json:"model"`
	Scenario                string    `gorm:"column:scenario;size:32" json:"scenario"`
	ChatID                  string    `gorm:"column:chat_id;size:64;index" json:"chat_id"`
	SessionID               string    `gorm:"column:session_id;size:64;index" json:"session_id"`
	DatasetIDs              string    `gorm:"column:dataset_ids;size:512" json:"dataset_ids"`
	TokensIn                int64     `gorm:"column:tokens_in;not null;default:0" json:"tokens_in"`
	TokensOut               int64     `gorm:"column:tokens_out;not null;default:0" json:"tokens_out"`
	EstimatedCost           float64   `gorm:"column:cost;not null;default:0" json:"estimated_cost"`
	Estimated               bool      `gorm:"column:estimated;not null;default:false" json:"estimated"`
	EstimationPolicyVersion string    `gorm:"column:estimation_policy_version;size:32" json:"estimation_policy_version"`
	CreatedAt               time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (CostMetric) TableName() string { return "rgx_cost_metric" }
