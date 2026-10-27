package model

import "time"

type TraceRun struct {
	ID                    string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID              string    `gorm:"column:tenant_id;size:32;not null;index;uniqueIndex:uk_trace_run_tenant_trace" json:"tenant_id"`
	TraceID               string    `gorm:"column:trace_id;size:64;not null;uniqueIndex:uk_trace_run_tenant_trace;index" json:"trace_id"`
	SpanID                string    `gorm:"column:span_id;size:64;not null;default:root" json:"span_id"`
	RequestID             string    `gorm:"column:request_id;size:64;not null;index" json:"request_id"`
	SessionID             string    `gorm:"column:session_id;size:64;index" json:"session_id"`
	UserID                string    `gorm:"column:user_id;size:32;not null;index" json:"user_id"`
	ProjectID             string    `gorm:"column:project_id;size:32;index" json:"project_id"`
	AssistantID           string    `gorm:"column:assistant_id;size:32;index" json:"assistant_id"`
	AssistantReleaseID    string    `gorm:"column:assistant_release_id;size:32;index" json:"assistant_release_id"`
	AppType               string    `gorm:"column:app_type;size:16;not null;index" json:"app_type"`
	AppID                 string    `gorm:"column:app_id;size:64;not null;index" json:"app_id"`
	Channel               string    `gorm:"column:channel;size:32;not null;default:web" json:"channel"`
	Status                string    `gorm:"column:status;size:16;not null;default:completed;index" json:"status"`
	RouteSummaryJSON      string    `gorm:"column:route_summary_json;type:text" json:"route_summary_json"`
	RetrievalSummaryJSON  string    `gorm:"column:retrieval_summary_json;type:text" json:"retrieval_summary_json"`
	ModelSummaryJSON      string    `gorm:"column:model_summary_json;type:text" json:"model_summary_json"`
	ToolSummaryJSON       string    `gorm:"column:tool_summary_json;type:text" json:"tool_summary_json"`
	GovernanceSummaryJSON string    `gorm:"column:governance_summary_json;type:text" json:"governance_summary_json"`
	QualitySummaryJSON    string    `gorm:"column:quality_summary_json;type:text" json:"quality_summary_json"`
	EvidencePointersJSON  string    `gorm:"column:evidence_pointers_json;type:text" json:"evidence_pointers_json"`
	CreatedAt             time.Time `gorm:"column:created_at;index" json:"created_at"`
	UpdatedAt             time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (TraceRun) TableName() string { return "rgx_trace_run" }

const (
	TraceRunCompleted = "completed"
	TraceRunNoAnswer  = "no_answer"
	TraceRunFailed    = "failed"
)
