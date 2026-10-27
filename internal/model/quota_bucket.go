package model

import "time"

// Quota bucket dimension keys (doc/224 §2.5 T5.1). A gateway budget may be
// enforced per key (legacy rgx_quota), per tenant, per model or per assistant.
// The tenant bucket is the v0.4 MVP wiring; model/assistant reuse the same
// ledger shape so the gate can be widened without a schema break.
const (
	QuotaBucketDimensionTenant    = "tenant"
	QuotaBucketDimensionModel     = "model"
	QuotaBucketDimensionAssistant = "assistant"
)

// QuotaBucket is a monthly quota ledger row for a gateway quota dimension
// beyond the key (tenant / model / assistant). It mirrors the key-scoped
// QuotaLimit shape so batch pre-deduction semantics stay identical: pending
// holds live, unreconciled reservations so concurrent in-flight requests
// cannot collectively exceed either budget. A limit <= 0 means unlimited.
type QuotaBucket struct {
	ID              string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID        string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Dimension       string    `gorm:"column:dimension;size:16;not null" json:"dimension"`
	DimensionKey    string    `gorm:"column:dimension_key;size:128;not null;uniqueIndex:idx_quota_bucket_period" json:"dimension_key"`
	PeriodStart     string    `gorm:"column:period_start;size:7;not null;uniqueIndex:idx_quota_bucket_period" json:"period_start"`
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
func (QuotaBucket) TableName() string { return "rgx_quota_bucket" }

// QuotaBucketReservation is the idempotency + audit ledger for a pre-reserved
// token estimate against a quota bucket, keyed by request_id so client retries
// never double-reserve and reconciliation happens exactly once per request.
// It parallels QuotaReservation (key-scoped) for the tenant/model/assistant
// dimensions.
type QuotaBucketReservation struct {
	RequestID    string `gorm:"column:request_id;primaryKey;size:64" json:"request_id"`
	TenantID     string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Dimension    string `gorm:"column:dimension;size:16;not null" json:"dimension"`
	DimensionKey string `gorm:"column:dimension_key;size:128;not null;index" json:"dimension_key"`
	PeriodStart  string `gorm:"column:period_start;size:7;not null;index" json:"period_start"`
	Estimated    int64  `gorm:"column:estimated;not null;default:0" json:"estimated"`
	ActualTokens int64  `gorm:"column:actual_tokens;not null;default:0" json:"actual_tokens"`
	Status       string `gorm:"column:status;size:16;not null;default:pending" json:"status"`
	// Applied is true only after the atomic bucket row was incremented. It
	// prevents release/reconciliation from draining balances for a request
	// rejected before reservation or interrupted before it could reserve.
	Applied   bool      `gorm:"column:applied;not null;default:false" json:"applied"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (QuotaBucketReservation) TableName() string { return "rgx_quota_bucket_reservation" }
