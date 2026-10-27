package model

import "time"

type KnowledgeTask struct {
	ID                       string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                 string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	SourceEventID            string     `gorm:"column:source_event_id;size:32;index" json:"source_event_id"`
	SourceRequestID          string     `gorm:"column:source_request_id;size:64;index" json:"source_request_id"`
	SourceAttribution        string     `gorm:"column:source_attribution;size:32;index" json:"source_attribution"`
	SourceAnswerRunID        string     `gorm:"column:source_answer_run_id;size:32;index" json:"source_answer_run_id"`
	SourceAnswerSnapshotID   string     `gorm:"column:source_answer_snapshot_id;size:32;index" json:"source_answer_snapshot_id"`
	SourceTraceID            string     `gorm:"column:source_trace_id;size:64;index" json:"source_trace_id"`
	SourceAssistantID        string     `gorm:"column:source_assistant_id;size:64;index" json:"source_assistant_id"`
	SourceAssistantReleaseID string     `gorm:"column:source_assistant_release_id;size:32;index" json:"source_assistant_release_id"`
	SourceDatasetID          string     `gorm:"column:source_dataset_id;size:32;index" json:"source_dataset_id"`
	SourceCitationID         string     `gorm:"column:source_citation_id;size:128" json:"source_citation_id"`
	SourceChunkID            string     `gorm:"column:source_chunk_id;size:64;index" json:"source_chunk_id"`
	SourceCitationHash       string     `gorm:"column:source_citation_hash;size:64" json:"source_citation_hash"`
	SourceDocumentVersion    string     `gorm:"column:source_document_version;size:64" json:"source_document_version"`
	Title                    string     `gorm:"column:title;size:255;not null" json:"title"`
	Description              string     `gorm:"column:description;type:text" json:"description"`
	Category                 string     `gorm:"column:category;size:32;not null;index" json:"category"`
	OwnerID                  string     `gorm:"column:owner_id;size:32;not null;index" json:"owner_id"`
	DueAt                    *time.Time `gorm:"column:due_at;index" json:"due_at"`
	Priority                 string     `gorm:"column:priority;size:16;not null;default:medium;index" json:"priority"`
	Status                   string     `gorm:"column:status;size:24;not null;default:open;index" json:"status"`
	RequiresApproval         bool       `gorm:"column:requires_approval;not null;default:false" json:"requires_approval"`
	ApprovalID               string     `gorm:"column:approval_id;size:32;index" json:"approval_id"`
	ResolutionNote           string     `gorm:"column:resolution_note;type:text" json:"resolution_note"`
	RegressionEvalSetID      string     `gorm:"column:regression_eval_set_id;size:32;index" json:"regression_eval_set_id"`
	RegressionEvalCaseID     string     `gorm:"column:regression_eval_case_id;size:32;index" json:"regression_eval_case_id"`
	RegressionStatus         string     `gorm:"column:regression_status;size:16;not null;default:pending" json:"regression_status"`
	CreatedBy                string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt                time.Time  `gorm:"column:created_at;index" json:"created_at"`
	UpdatedAt                time.Time  `gorm:"column:updated_at" json:"updated_at"`
	ResolvedAt               *time.Time `gorm:"column:resolved_at" json:"resolved_at"`
}

func (KnowledgeTask) TableName() string { return "rgx_knowledge_task" }

const (
	KnowledgeTaskOpen            = "open"
	KnowledgeTaskInProgress      = "in_progress"
	KnowledgeTaskBlocked         = "blocked"
	KnowledgeTaskPendingApproval = "pending_approval"
	KnowledgeTaskResolved        = "resolved"
	KnowledgeTaskCanceled        = "canceled"

	KnowledgeTaskPriorityLow      = "low"
	KnowledgeTaskPriorityMedium   = "medium"
	KnowledgeTaskPriorityHigh     = "high"
	KnowledgeTaskPriorityCritical = "critical"

	KnowledgeTaskRegressionPending = "pending"
	KnowledgeTaskRegressionPassed  = "passed"
	KnowledgeTaskRegressionFailed  = "failed"
)

type KnowledgeTaskSummary struct {
	Open            int64 `json:"open"`
	InProgress      int64 `json:"in_progress"`
	Blocked         int64 `json:"blocked"`
	PendingApproval int64 `json:"pending_approval"`
	Resolved        int64 `json:"resolved"`
	Canceled        int64 `json:"canceled"`
	Overdue         int64 `json:"overdue"`
}
