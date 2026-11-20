package model

import "time"

// Gateway billing self-consistency (doc/301 §5.10 GW-3 / T21).
//
// The gateway writes four ledgers for one served request: `rgx_gateway_idempotency`
// (the caller-facing retry record), `rgx_meter_request` (the once-only metering
// stamp), `rgx_quota_reservation` (the pre-reserved then settled budget) and
// `rgx_cost_metric` (the per-request cost row). Four writes, one request — so the
// only way to know that a replay did not double-bill, or that a 200-then-failure did
// not leave a charge, is to reconcile the four against each other.
//
// The closed sets below are deliberately small: every finding kind names one
// *specific* way the four ledgers can disagree, because "mismatch" as a single bucket
// would tell an operator nothing they could act on.

// GatewayBillingReconcileRun statuses.
const (
	GatewayBillingReconcileRunning   = "running"
	GatewayBillingReconcileCompleted = "completed"
	GatewayBillingReconcileFailed    = "failed"
)

// GatewayBillingReconcileStatuses returns the closed set of run statuses.
func GatewayBillingReconcileStatuses() []string {
	return []string{
		GatewayBillingReconcileRunning,
		GatewayBillingReconcileCompleted,
		GatewayBillingReconcileFailed,
	}
}

// Finding kinds. Each names one ledger pair that disagrees.
const (
	// GatewayBillingFindingMissingMeter: a gateway request finished (idempotency row
	// COMPLETED) but the once-only metering stamp is absent, so usage and cost were
	// never accrued. The request was served for free.
	GatewayBillingFindingMissingMeter = "missing_meter"
	// GatewayBillingFindingMissingCost: metered but no cost row, so cost attribution
	// under-reports while usage looks correct. A silent under-estimate of spend.
	GatewayBillingFindingMissingCost = "missing_cost"
	// GatewayBillingFindingChargedAfterFailure: the idempotency row is FAILED but a
	// cost row exists for the same request — the failure path billed anyway.
	GatewayBillingFindingChargedAfterFailure = "charged_after_failure"
	// GatewayBillingFindingDuplicateRequest: one request_id appears on more than one
	// idempotency row. That is the replay-double-billing shape: the unique key is
	// (tenant, principal, idempotency_key), so two rows for one request mean two
	// caller keys were spent on one call.
	GatewayBillingFindingDuplicateRequest = "duplicate_request"
	// GatewayBillingFindingStaleReservation: a budget reservation is still `pending`
	// long after the request could have finished, so a phantom balance keeps locking
	// budget until the reaper happens to run.
	GatewayBillingFindingStaleReservation = "stale_reservation"
	// GatewayBillingFindingQuotaDrift: the settled reservations of one (key, period)
	// do not sum to that key's used balance. Counted per bucket, not per request.
	GatewayBillingFindingQuotaDrift = "quota_drift"
	// GatewayBillingFindingUsageDrift: the day's metering stamps and the daily usage
	// aggregate disagree on how many requests were billed. One row per tenant-day,
	// because the aggregate is the only ledger that has no per-request identity left
	// to point at — and a count mismatch on it means some request was counted twice
	// or not at all.
	GatewayBillingFindingUsageDrift = "usage_drift"
)

// GatewayBillingFindingKinds returns the closed set of finding kinds.
func GatewayBillingFindingKinds() []string {
	return []string{
		GatewayBillingFindingMissingMeter,
		GatewayBillingFindingMissingCost,
		GatewayBillingFindingChargedAfterFailure,
		GatewayBillingFindingDuplicateRequest,
		GatewayBillingFindingStaleReservation,
		GatewayBillingFindingQuotaDrift,
		GatewayBillingFindingUsageDrift,
	}
}

// IsGatewayBillingFindingKind reports whether value belongs to the closed set.
func IsGatewayBillingFindingKind(value string) bool {
	for _, kind := range GatewayBillingFindingKinds() {
		if kind == value {
			return true
		}
	}
	return false
}

// IsGatewayBillingReconcileStatus reports whether value belongs to the closed set.
func IsGatewayBillingReconcileStatus(value string) bool {
	for _, status := range GatewayBillingReconcileStatuses() {
		if status == value {
			return true
		}
	}
	return false
}

// GatewayBillingReconcileRun is one tenant's reconciliation of one UTC day.
//
// `Checked` and `Findings` always travel together: the ratio is derived by the
// caller and stored nowhere, because a stored rate would survive a re-run with a
// different denominator and stop meaning anything.
type GatewayBillingReconcileRun struct {
	ID       string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID string `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uidx_gateway_billing_reconcile_day,priority:1;index:idx_gateway_billing_reconcile_scope,priority:1" json:"tenant_id"`
	// Day is the reconciled UTC date ("2006-01-02"). It is the idempotency guard
	// together with the tenant: a replayed daily job cannot reconcile the same day
	// twice and double-count its findings.
	Day                 string `gorm:"column:day;size:10;not null;uniqueIndex:uidx_gateway_billing_reconcile_day,priority:2;index:idx_gateway_billing_reconcile_scope,priority:2" json:"day"`
	Status              string `gorm:"column:status;size:16;not null;index" json:"status"`
	Checked             int    `gorm:"column:checked;not null;default:0" json:"checked"`
	Findings            int    `gorm:"column:findings;not null;default:0" json:"findings"`
	MissingMeter        int    `gorm:"column:missing_meter;not null;default:0" json:"missing_meter"`
	MissingCost         int    `gorm:"column:missing_cost;not null;default:0" json:"missing_cost"`
	ChargedAfterFailure int    `gorm:"column:charged_after_failure;not null;default:0" json:"charged_after_failure"`
	DuplicateRequest    int    `gorm:"column:duplicate_request;not null;default:0" json:"duplicate_request"`
	StaleReservation    int    `gorm:"column:stale_reservation;not null;default:0" json:"stale_reservation"`
	QuotaDriftBuckets   int    `gorm:"column:quota_drift_buckets;not null;default:0" json:"quota_drift_buckets"`
	UsageDrift          int    `gorm:"column:usage_drift;not null;default:0" json:"usage_drift"`
	// QuotaExpectedTokens is the sum of settled reservations and
	// QuotaActualTokens the sum of the keys' used balances, over the buckets this
	// run examined. Both raw numbers, so a reader can redo the subtraction.
	QuotaExpectedTokens int64 `gorm:"column:quota_expected_tokens;not null;default:0" json:"quota_expected_tokens"`
	QuotaActualTokens   int64 `gorm:"column:quota_actual_tokens;not null;default:0" json:"quota_actual_tokens"`
	// UsageMeteredRequests / UsageBilledRequests are the two sides of the third
	// ledger pair. Stored even when they agree: "0 vs 0" and "40 vs 40" are different
	// statements about the day, and only one of them means requests happened.
	UsageMeteredRequests int64      `gorm:"column:usage_metered_requests;not null;default:0" json:"usage_metered_requests"`
	UsageBilledRequests  int64      `gorm:"column:usage_billed_requests;not null;default:0" json:"usage_billed_requests"`
	FailureReason        string     `gorm:"column:failure_reason;size:512" json:"failure_reason,omitempty"`
	StartedAt            time.Time  `gorm:"column:started_at;not null" json:"started_at"`
	FinishedAt           *time.Time `gorm:"column:finished_at" json:"finished_at,omitempty"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null;index" json:"created_at"`
}

// TableName is the physical table name.
func (GatewayBillingReconcileRun) TableName() string { return "rgx_gateway_billing_reconcile_run" }

// GatewayBillingReconcileFinding is one disagreement, kept as a row so the drill can
// name the requests instead of only counting them.
type GatewayBillingReconcileFinding struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null;index:idx_gateway_billing_finding_run,priority:1" json:"tenant_id"`
	RunID     string    `gorm:"column:run_id;size:32;not null;index:idx_gateway_billing_finding_run,priority:2" json:"run_id"`
	Day       string    `gorm:"column:day;size:10;not null;index" json:"day"`
	Kind      string    `gorm:"column:kind;size:32;not null;index" json:"kind"`
	RequestID string    `gorm:"column:request_id;size:64" json:"request_id,omitempty"`
	KeyID     string    `gorm:"column:key_id;size:64" json:"key_id,omitempty"`
	Detail    string    `gorm:"column:detail;size:512" json:"detail,omitempty"`
	CreatedAt time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
}

// TableName is the physical table name.
func (GatewayBillingReconcileFinding) TableName() string {
	return "rgx_gateway_billing_reconcile_finding"
}

// GatewayBillingSnapshot is one tenant-day of the four ledgers, already reduced to
// the refs the reconciliation needs. It is a plain value on purpose: the arithmetic
// that decides "who disagrees with whom" is then a pure function a test can drive
// with a hand-built snapshot, without a database and without a clock.
type GatewayBillingSnapshot struct {
	// Idempotency holds every caller-facing retry row created in the window.
	Idempotency []GatewayBillingLedgerRef
	// Metered holds the request ids that carry a metering stamp for the day.
	Metered []string
	// Charged holds the cost rows of the day.
	Charged []GatewayBillingLedgerRef
	// DuplicateRequests holds request ids that appear on more than one idempotency
	// row in the window.
	DuplicateRequests []string
	// StaleReservations holds pending reservations older than the grace window. They
	// are not day-scoped: a reservation leaked last week is still leaking today.
	StaleReservations []GatewayBillingReservationRef
	// Buckets holds the (key, period) balance pairs to compare.
	Buckets []GatewayBillingBucketDrift
	// UsageRequests is the day's billed request count from the daily aggregate.
	UsageRequests int64
}

// GatewayBillingLedgerRef is a request_id plus the identity a finding may need.
type GatewayBillingLedgerRef struct {
	RequestID string
	KeyID     string
	Status    string
}

// GatewayBillingReservationRef is one leaked reservation.
type GatewayBillingReservationRef struct {
	RequestID   string
	KeyID       string
	PeriodStart string
	Estimated   int64
	CreatedAt   time.Time
}

// GatewayBillingBucketDrift is one (key, period) balance pair: what the settled
// reservations say was spent, and what the key's counter says.
type GatewayBillingBucketDrift struct {
	KeyID          string
	PeriodStart    string
	ExpectedTokens int64
	ActualTokens   int64
}
