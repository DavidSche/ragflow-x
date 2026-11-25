package model

import "time"

// Metering states. They answer one question: did this request's metering land?
//
// The vocabulary is deliberately five-valued rather than a boolean, because the
// three "not recorded" cases have different remedies and different evidence:
//
//   - pending  — the intent was written and no ledger write has been confirmed.
//     Recovery retries it; a completion must not claim it landed.
//   - failed   — every write attempt failed and the error is stored on the row.
//     Recovery retries it; the loss is *known* and recoverable.
//   - none     — this request never had a metering intent (it never reached the
//     metering point, e.g. it failed before the provider answered). Nothing is
//     owed, so nothing is missing. Without this value an empty string would have
//     to mean both "nothing owed" and "no idea", which is the ambiguity E126
//     already paid for once.
//   - recorded — both ledgers are written for this request id.
//   - not_tracked — a completion row written before this column existed. It is
//     history, not a reading: it must never be counted as `recorded`.
const (
	MeteringPending    = "pending"
	MeteringRecorded   = "recorded"
	MeteringFailed     = "failed"
	MeteringNone       = "none"
	MeteringNotTracked = "not_tracked"
)

// MeteringIntent is the write-ahead record of one gateway request's metering
// (doc/304 §4 E147). It exists because of a specific failure: a request could be
// served, returned to the caller, marked COMPLETED in the idempotency ledger, and
// never appear in `rgx_meter_request` — with nothing anywhere saying why
// (doc/304 §4 E146: 14 `missing_meter` findings, unexplained).
//
// The row is written *before* the ledger writes, so a crash between the two
// leaves a readable `pending` row instead of a silent gap. It carries the whole
// payload rather than a pointer to one, because recovery has to be able to
// replay the exact intent hours later: recomputing tokens or cost from
// configuration would let a config change rewrite history.
//
// `request_id` is unique, and it is the same key `rgx_meter_request` uses, so
// replaying this intent is a no-op at the ledger level — that is what makes
// recovery safe to run repeatedly (the "补偿不重复" half of E147).
type MeteringIntent struct {
	ID        string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string `gorm:"column:tenant_id;size:32;not null;index:idx_metering_intent_tenant" json:"tenant_id"`
	RequestID string `gorm:"column:request_id;size:64;not null;uniqueIndex" json:"request_id"`
	UserID    string `gorm:"column:user_id;size:32;not null" json:"user_id"`
	KeyID     string `gorm:"column:key_id;size:32" json:"key_id"`
	// Date is the metering day, fixed when the intent is prepared: a recovery at
	// 00:05 must book the request to the day it was served, not to today.
	Date     string `gorm:"column:date;size:10;not null" json:"date"`
	TokensIn int64  `gorm:"column:tokens_in;not null;default:0" json:"tokens_in"`
	// TokensOut and Requests are stored so a replay reproduces the original
	// aggregate increment exactly (Requests is 1 per request today).
	TokensOut               int64   `gorm:"column:tokens_out;not null;default:0" json:"tokens_out"`
	Requests                int64   `gorm:"column:requests;not null;default:1" json:"requests"`
	ChatID                  string  `gorm:"column:chat_id;size:64" json:"chat_id"`
	Model                   string  `gorm:"column:model;size:128" json:"model"`
	Scenario                string  `gorm:"column:scenario;size:32" json:"scenario"`
	SessionID               string  `gorm:"column:session_id;size:64" json:"session_id"`
	EstimatedCost           float64 `gorm:"column:estimated_cost" json:"estimated_cost"`
	Estimated               bool    `gorm:"column:estimated;not null;default:false" json:"estimated"`
	EstimationPolicyVersion string  `gorm:"column:estimation_policy_version;size:32" json:"estimation_policy_version"`
	State                   string  `gorm:"column:state;size:16;not null;default:pending;index:idx_metering_intent_state,priority:1;index:idx_metering_intent_tenant,priority:2" json:"state"`
	// Attempts counts write attempts across the first try and every recovery
	// pass, so "kept failing" and "failed once, recovered" read differently.
	Attempts   int        `gorm:"column:attempts;not null;default:0" json:"attempts"`
	LastError  string     `gorm:"column:last_error;type:text" json:"last_error"`
	CreatedAt  time.Time  `gorm:"column:created_at;index" json:"created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at" json:"updated_at"`
	RecordedAt *time.Time `gorm:"column:recorded_at" json:"recorded_at"`
}

// TableName is the physical table name.
func (MeteringIntent) TableName() string { return "rgx_metering_intent" }

// UsageRecord renders the daily aggregate row this intent owes. It is derived
// rather than stored as a second payload so the intent stays the only place that
// decides what a request costs.
func (m *MeteringIntent) UsageRecord() *QuotaUsage {
	return &QuotaUsage{
		TenantID: m.TenantID, UserID: m.UserID, KeyID: m.KeyID, RequestID: m.RequestID,
		Date: m.Date, TokensIn: m.TokensIn, TokensOut: m.TokensOut, Requests: m.Requests,
		Estimated: m.Estimated, EstimationPolicyVersion: m.EstimationPolicyVersion,
	}
}

// CostRecord renders the per-request detail row this intent owes.
func (m *MeteringIntent) CostRecord() *CostMetric {
	return &CostMetric{
		TenantID: m.TenantID, UserID: m.UserID, KeyID: m.KeyID, RequestID: m.RequestID,
		Date: m.Date, ChatID: m.ChatID, Model: m.Model, Scenario: m.Scenario, SessionID: m.SessionID,
		TokensIn: m.TokensIn, TokensOut: m.TokensOut,
		EstimatedCost: m.EstimatedCost, Estimated: m.Estimated,
		EstimationPolicyVersion: m.EstimationPolicyVersion,
	}
}
