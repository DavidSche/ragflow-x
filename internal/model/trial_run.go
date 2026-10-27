package model

import "time"

// Trial run states (doc/228 §3.1 T7.3′, appendix B.5). A trial run is a
// diagnostic record, deliberately separate from the release-evidence chain:
// it is never gate eligible, has no pre-registration/min-sample requirement
// and stores its own immutable configuration snapshot.
const (
	TrialRunQueued    = "QUEUED"
	TrialRunRunning   = "RUNNING"
	TrialRunCompleted = "COMPLETED"
	TrialRunFailed    = "FAILED"
	TrialRunCancelled = "CANCELLED"

	TrialModeRetrievalOnly = "retrieval_only"

	TrialAssistantKindChat = "chat"

	// TrialFailureInterrupted marks a run that was still RUNNING when the
	// process restarted. Trial runs are not resumable by design.
	TrialFailureInterrupted = "interrupted"
)

// TrialRun is one retrieval-only trial execution with the exact configuration
// snapshot it ran under. Terminal rows are immutable.
type TrialRun struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uk_trial_run_idempotency;index" json:"tenant_id"`
	IdempotencyKey     string     `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:uk_trial_run_idempotency" json:"idempotency_key"`
	ScenarioTemplateID string     `gorm:"column:scenario_template_id;size:32;not null;index" json:"scenario_template_id"`
	TemplateVersion    int64      `gorm:"column:template_version;not null;default:0" json:"template_version"`
	AssistantID        string     `gorm:"column:assistant_id;size:64;not null;index" json:"assistant_id"`
	AssistantKind      string     `gorm:"column:assistant_kind;size:16;not null;default:chat" json:"assistant_kind"`
	EvalSetID          string     `gorm:"column:eval_set_id;size:32;not null" json:"eval_set_id"`
	EvalSetVersion     int64      `gorm:"column:eval_set_version;not null;default:0" json:"eval_set_version"`
	EvalSetHash        string     `gorm:"column:eval_set_hash;size:64;not null" json:"eval_set_hash"`
	PolicyID           string     `gorm:"column:policy_id;size:32;not null;default:trial-retrieval-v1" json:"policy_id"`
	PolicyHash         string     `gorm:"column:policy_hash;size:64;not null" json:"policy_hash"`
	Mode               string     `gorm:"column:mode;size:16;not null;default:retrieval_only" json:"mode"`
	AuthoringSnapshot  string     `gorm:"column:authoring_snapshot;type:text;not null" json:"authoring_snapshot"`
	DatasetIDs         string     `gorm:"column:dataset_ids;type:text;not null" json:"dataset_ids"`
	RAGFlowVersion     string     `gorm:"column:ragflow_version;size:64" json:"ragflow_version"`
	Status             string     `gorm:"column:status;size:16;not null;default:QUEUED;index" json:"status"`
	FailureReason      string     `gorm:"column:failure_reason;size:32" json:"failure_reason"`
	Summary            string     `gorm:"column:summary;type:text" json:"summary"`
	CancelRequestedAt  *time.Time `gorm:"column:cancel_requested_at" json:"cancel_requested_at"`
	StartedAt          *time.Time `gorm:"column:started_at" json:"started_at"`
	CompletedAt        *time.Time `gorm:"column:completed_at" json:"completed_at"`
	CreatedAt          time.Time  `gorm:"column:created_at;index" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at" json:"updated_at"`
	Actor              string     `gorm:"column:actor;size:32;not null" json:"actor"`
	TraceID            string     `gorm:"column:trace_id;size:64" json:"trace_id"`
}

// TableName is the physical table name.
func (TrialRun) TableName() string { return "rgx_trial_run" }

// TrialCaseResult is the immutable per-question projection of a trial run.
// hit_at_k is a pointer because "not computable" (nil) must never render as 0%.
type TrialCaseResult struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	RunID              string    `gorm:"column:run_id;size:32;not null;uniqueIndex:uk_trial_case_result_case;index" json:"run_id"`
	CaseID             string    `gorm:"column:case_id;size:32;not null;uniqueIndex:uk_trial_case_result_case" json:"case_id"`
	CaseVersionID      string    `gorm:"column:case_version_id;size:32" json:"case_version_id"`
	Question           string    `gorm:"column:question;type:text;not null" json:"question"`
	Returned           bool      `gorm:"column:returned;not null;default:false" json:"returned"`
	HitAtK             *int      `gorm:"column:hit_at_k" json:"hit_at_k"`
	HitUndefinedReason string    `gorm:"column:hit_undefined_reason;size:64" json:"hit_undefined_reason"`
	TopScore           float64   `gorm:"column:top_score;not null;default:0" json:"top_score"`
	TopNScores         string    `gorm:"column:top_n_scores;type:text" json:"top_n_scores"`
	Evidence           string    `gorm:"column:evidence;type:text" json:"evidence"`
	LatencyMs          int64     `gorm:"column:latency_ms;not null;default:0" json:"latency_ms"`
	ErrorCode          string    `gorm:"column:error_code;size:32" json:"error_code"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (TrialCaseResult) TableName() string { return "rgx_trial_case_result" }
