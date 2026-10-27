package model

import "time"

const (
	ApprovalOperationRunning   = "RUNNING"
	ApprovalOperationCompleted = "COMPLETED"
	ApprovalOperationFailed    = "FAILED"
	ApprovalOperationUnknown   = "UNKNOWN"
)

// ApprovalOperation is the durable business outcome ledger for one immutable
// Acting Context attempt. It answers whether a retry may re-execute, may
// replay a result, or must stop for reconciliation.
type ApprovalOperation struct {
	ID                   string     `gorm:"column:id;primaryKey;size:64" json:"id"`
	TenantID             string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ApprovalID           string     `gorm:"column:approval_id;size:32;not null;index:uk_approval_operation_attempt,priority:1" json:"approval_id"`
	ActingContextID      string     `gorm:"column:acting_context_id;size:64;not null;uniqueIndex" json:"acting_context_id"`
	AttemptNo            int64      `gorm:"column:attempt_no;not null;uniqueIndex:uk_approval_operation_attempt,priority:2" json:"attempt_no"`
	PrincipalID          string     `gorm:"column:principal_id;size:32;not null;index" json:"principal_id"`
	IdempotencyKey       string     `gorm:"column:idempotency_key;size:128;not null;index" json:"idempotency_key"`
	RequestID            string     `gorm:"column:request_id;size:128;not null" json:"request_id"`
	OperationFingerprint string     `gorm:"column:operation_fingerprint;size:80;not null" json:"operation_fingerprint"`
	ApprovalActionHash   string     `gorm:"column:approval_action_hash;size:80;not null" json:"approval_action_hash"`
	Status               string     `gorm:"column:status;size:16;not null;default:RUNNING;index" json:"status"`
	ResultJSON           string     `gorm:"column:result_json;type:text;not null;default:'{}'" json:"result_json"`
	LastError            string     `gorm:"column:last_error;type:text;not null;default:''" json:"last_error"`
	ClaimedBy            string     `gorm:"column:claimed_by;size:128;not null;default:''" json:"claimed_by"`
	ClaimedAt            *time.Time `gorm:"column:claimed_at" json:"claimed_at,omitempty"`
	ClaimExpiresAt       *time.Time `gorm:"column:claim_expires_at" json:"claim_expires_at,omitempty"`
	StartedAt            time.Time  `gorm:"column:started_at;not null" json:"started_at"`
	CompletedAt          *time.Time `gorm:"column:completed_at" json:"completed_at,omitempty"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ApprovalOperation) TableName() string { return "rgx_approval_operation" }
