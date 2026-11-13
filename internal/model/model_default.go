package model

import "time"

// ModelDefaultRecoveryClaim is the thin, non-audit table that gives the
// default-model recovery compensator a safe idempotent claim on a pending
// operation (doc/269 §4.2, v9 self-audit ⑧(a)). It exists precisely because
// the audit log is an append-only hash chain: marking a pending row with the
// table's own unique key or UPDATE would corrupt PrevHash links, and an
// audit-table unique constraint on operation_ref would swallow the terminal
// row B (rows A/B share one operation_ref). The claim row is the single
// worker-take coordination point, never a mutation of an audit row.
type ModelDefaultRecoveryClaim struct {
	OperationRef string    `gorm:"column:operation_ref;primaryKey;size:128" json:"operation_ref"`
	WorkerID     string    `gorm:"column:worker_id;size:64;not null" json:"worker_id"`
	ClaimedAt    time.Time `gorm:"column:claimed_at;index" json:"claimed_at"`
	// FinishedAt is set when the compensator has appended the terminal audit
	// row B; a NULL value means the claim is still held (or abandoned and
	// re-scannable after the hold deadline outside this table).
	FinishedAt *time.Time `gorm:"column:finished_at" json:"finished_at"`
}

// TableName is the physical table name.
func (ModelDefaultRecoveryClaim) TableName() string { return "rgx_model_default_recovery_claim" }
