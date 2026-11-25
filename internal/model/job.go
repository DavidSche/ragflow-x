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
	// JobKindInstantiationOrphanReconcile periodically reconciles stale or
	// orphan-marked instantiations so upstream resources created by a crashed
	// run stay legible for cleanup (doc/276 §7.5 rule 4).
	JobKindInstantiationOrphanReconcile = "instantiation_orphan_reconcile"
	// JobKindAlertDeliveryCompensation retries persisted failed webhook
	// deliveries after restarts and transient downstream failures.
	JobKindAlertDeliveryCompensation = "alert_delivery_compensation"
	// JobKindOutboxDispatch periodically dispatches due durable domain events.
	JobKindOutboxDispatch = "outbox_dispatch"
	// JobKindMemoryCompensation reconciles tombstoned post-admission Memory
	// writes against the external execution plane.
	JobKindMemoryCompensation = "memory_compensation"
	// JobKindCompilationReconcile dispatches accepted compilation runs and
	// reconciles dispatched/unknown runs against RAGFlow read-only status.
	JobKindCompilationReconcile = "compilation_reconcile"
	// JobKindEvaluationRun executes a paired scoped evaluation run.
	JobKindEvaluationRun = "evaluation_run"
	// JobKindVersionPublishReconcile recovers interrupted document version
	// publish coordination by comparing Attempt snapshots with RAGFlow state.
	JobKindVersionPublishReconcile = "version_publish_reconcile"
	// JobKindReportGenerate is one execution slice of a report job (doc/288
	// §7.9): `rgx_job` describes a slice, while `rgx_report_job` stays the only
	// authoritative business state. The slice key
	// `report:<report_job_id>:<attempt_seq>:<slice_seq>` is what makes execution
	// idempotent — a replayed trigger enqueues nothing.
	JobKindReportGenerate = "report_generate"
	// JobKindReportFinalizeReconcile is the second trigger of the report
	// terminal write (doc/288 §8.2 / A35 ④): a report left in `finalizing` by a
	// crash between the outbox insert and the terminal transaction is finished
	// here. It exists because "the event was published" is not proof that the
	// terminal state was written.
	JobKindReportFinalizeReconcile = "report_finalize_reconcile"
	// JobKindConnectorSync executes a data-source connector sync run (doc/225
	// §6): scan -> diff -> ledger/idempotent apply -> upload & parse.
	JobKindConnectorSync = "connector_sync"
	// JobKindConnectorRetention prunes expired connector run history (doc/225
	// §6.2 F4) on a self-rescheduling daily cadence.
	JobKindConnectorRetention = "connector_retention"
	// JobKindReportScheduleTick is R20's scheduler tick (doc/295 §2.4): one job
	// that claims the schedules whose `next_run_at` has passed and starts their
	// report runs. It advances `next_run_at` *before* doing any work, so a crash
	// delays the next occurrence instead of firing the same one twice; the run
	// itself is deduplicated by `schedule:<schedule_id>:<due_at>`.
	JobKindReportScheduleTick = "report_schedule_tick"
	// JobKindReportAssurance executes one assurance run of one immutable revision
	// (doc/301 §6.6 / T04b). The key `assurance:<run_id>` makes the enqueue
	// idempotent, and the payload carries only the run id so a replayed job cannot
	// describe a run that has since been cancelled.
	JobKindReportAssurance = "report_assurance"
	// JobKindReportAssuranceReconcile re-arms runs left `queued` without a runtime
	// job (crash between the run insert and the enqueue) and closes `running` runs
	// whose worker died. It is the second trigger of the assurance terminal write,
	// the same posture `report_finalize_reconcile` has for reports.
	JobKindReportAssuranceReconcile = "report_assurance_reconcile"
	// JobKindChatCitationAudit is QA-2's weekly sweep (doc/301 §5.10 / T19): one
	// job that judges the previous completed week for every tenant that asked
	// questions, then schedules the next sweep. It is self-rescheduling rather than
	// cron-driven so a crashed process resumes the cadence instead of stopping it.
	JobKindChatCitationAudit = "chat_citation_audit"
	// JobKindGatewayBillingReconcile is GW-3's daily four-ledger check (doc/301
	// §5.10 / T21): it reconciles the previous completed UTC day for every tenant
	// with gateway activity, then schedules the next run. Self-rescheduling for the
	// same reason as the citation sweep — a crashed process resumes the cadence
	// instead of silently ending it, and a day that was never reconciled is exactly
	// the failure this job exists to make visible.
	JobKindGatewayBillingReconcile = "gateway_billing_reconcile"
	// JobKindMeteringRecovery replays metering intents whose ledger write never
	// landed (doc/304 §4 E147). Minutes-scale rather than daily: a failed write
	// is a served request whose books are missing *now*, and the replay is
	// idempotent by `request_id`, so running it often costs nothing while
	// waiting a day would leave a whole day of gaps in the cost report.
	JobKindMeteringRecovery = "metering_recovery"
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
