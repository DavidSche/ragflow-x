package model

import "time"

// Job kinds that the async worker can execute. Add a new kind here (and a
// matching Worker registration) when onboarding a new asynchronous task.
const (
	// JobKindDocumentSync is the first worker consumer: it polls RAGFlow's
	// real document parsing state and pushes derived status/progress onto the
	// operator queue (see service.SyncTaskProgress).
	JobKindDocumentSync = "document_sync"
	// JobKindDataRetention is the data-retention janitor (doc/33 A5): it
	// executes the bounded TTL purge of audit/usage/message-feedback/job ledgers
	// on a configurable cadence and self-schedules its next run through the
	// durable job table (crash-safe recurring execution via the A2 worker).
	JobKindDataRetention = "data_retention"
	// JobKindResourceReconciliation removes local Dataset/Chat shadow rows
	// after the external RAGFlow resource can no longer be found.
	JobKindResourceReconciliation = "resource_reconciliation"
	// JobKindRAGFlowImport executes a planned immutable-version resource import.
	JobKindRAGFlowImport = "ragflow_import"
	// JobKindRAGFlowReconcile scans and reconciles RAGFlow resources deletion-safely.
	JobKindRAGFlowReconcile = "ragflow_reconcile"
	// JobKindApprovalExecute executes an approved governance action through the
	// existing business services after final approval.
	JobKindApprovalExecute = "approval_execute"
	// JobKindAuditAnchor periodically freezes each tenant's audit hash-chain
	// tail for deletion/tamper evidence.
	JobKindAuditAnchor = "audit_anchor"
	// JobKindAlertDeliveryCompensation retries persisted failed webhook
	// deliveries after restarts and transient downstream failures.
	JobKindAlertDeliveryCompensation = "alert_delivery_compensation"
)

// Job lifecycle states (doc/21 §2).
const (
	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
	JobStatusCanceled  = "canceled"
)

// Job is the durable async work unit (rgx_job). It doubles as the execution
// record/audit ledger and the scheduling ledger:
//
//   - Enqueue is idempotent by the unique Key (ON CONFLICT DO NOTHING), so a
//     duplicate trigger never enqueues a second copy of the same job.
//   - Claim is an atomic queued -> running transition, so concurrent runners
//     can never execute the same job twice (exactly-once consumption).
//   - Retries use exponential backoff via RunAfter; exceeding MaxRetry moves
//     the job to failed and raises an alert.
//   - LastHeartbeat leases an in-flight job; a crashed process's running jobs
//     are re-queued once their heartbeat goes stale (crash recovery).
type Job struct {
	ID            string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	Kind          string    `gorm:"column:kind;size:64;not null;index" json:"kind"`
	Key           string    `gorm:"column:key;size:128;not null;uniqueIndex" json:"key"`
	TenantID      string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Payload       string    `gorm:"column:payload;type:text" json:"payload"`
	Status        string    `gorm:"column:status;size:16;not null;default:queued;index" json:"status"`
	Attempts      int       `gorm:"column:attempts;not null;default:0" json:"attempts"`
	MaxRetry      int       `gorm:"column:max_retry;not null;default:3" json:"max_retry"`
	LastError     string    `gorm:"column:last_error;type:text" json:"last_error"`
	Result        string    `gorm:"column:result;type:text" json:"result"`
	RunAfter      time.Time `gorm:"column:run_after;index" json:"run_after"`
	LastHeartbeat time.Time `gorm:"column:last_heartbeat" json:"last_heartbeat"`
	CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (Job) TableName() string { return "rgx_job" }
