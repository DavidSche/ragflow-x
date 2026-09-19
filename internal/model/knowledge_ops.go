package model

import "time"

// KnowledgeOpsEvent is the per-turn knowledge quality ledger. It captures the
// question, answer state, citation evidence and usage required by the
// knowledge-operations board without joining engine-internal tables.
type KnowledgeOpsEvent struct {
	ID             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	RequestID      string    `gorm:"column:request_id;size:64;not null;uniqueIndex:uk_knowledge_ops_request" json:"request_id"`
	TenantID       string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	UserID         string    `gorm:"column:user_id;size:32;not null;index" json:"user_id"`
	AppType        string    `gorm:"column:app_type;size:16;not null;index" json:"app_type"` // chat | search | agent
	AppID          string    `gorm:"column:app_id;size:64;not null;index" json:"app_id"`
	SessionID      string    `gorm:"column:session_id;size:64;index" json:"session_id"`
	Question       string    `gorm:"column:question;type:text" json:"question"`
	QuestionHash   string    `gorm:"column:question_hash;size:64;not null;index" json:"question_hash"`
	AnswerExcerpt  string    `gorm:"column:answer_excerpt;size:1024" json:"answer_excerpt"`
	Status         string    `gorm:"column:status;size:16;not null;index" json:"status"` // completed | no_answer | failed
	CitationsCount int       `gorm:"column:citations_count;not null;default:0" json:"citations_count"`
	DurationMs     int64     `gorm:"column:duration_ms;not null;default:0" json:"duration_ms"`
	TokensIn       int64     `gorm:"column:tokens_in;not null;default:0" json:"tokens_in"`
	TokensOut      int64     `gorm:"column:tokens_out;not null;default:0" json:"tokens_out"`
	CreatedAt      time.Time `gorm:"column:created_at;index" json:"created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at" json:"updated_at"`

	// Review state closes the loop from badcase detection to a disposition.
	ReviewStatus         string     `gorm:"column:review_status;size:16;not null;default:open;index" json:"review_status"` // open | resolved | ignored
	ReviewNote           string     `gorm:"column:review_note;size:1024" json:"review_note"`
	ReviewedBy           string     `gorm:"column:reviewed_by;size:32" json:"reviewed_by"`
	ReviewedAt           *time.Time `gorm:"column:reviewed_at" json:"reviewed_at"`
	ResolvedAt           *time.Time `gorm:"column:resolved_at" json:"resolved_at"`
	ResolutionDurationMs int64      `gorm:"column:resolution_duration_ms;not null;default:0" json:"resolution_duration_ms"`
	EvalSetID            string     `gorm:"column:eval_set_id;size:32;index" json:"eval_set_id"`
	EvalCaseID           string     `gorm:"column:eval_case_id;size:32;index" json:"eval_case_id"`
	FeedbackID           string     `gorm:"column:feedback_id;size:32;index" json:"feedback_id"`
	FeedbackRating       string     `gorm:"column:feedback_rating;size:16" json:"feedback_rating"`
	FeedbackComment      string     `gorm:"column:feedback_comment;size:1024" json:"feedback_comment"`
	FeedbackAt           *time.Time `gorm:"column:feedback_at" json:"feedback_at"`
}

// TableName is the physical table name.
func (KnowledgeOpsEvent) TableName() string { return "rgx_knowledge_ops_event" }

// Knowledge Ops event statuses and review dispositions.
const (
	KnowledgeOpsCompleted = "completed"
	KnowledgeOpsNoAnswer  = "no_answer"
	KnowledgeOpsFailed    = "failed"

	KnowledgeOpsReviewOpen     = "open"
	KnowledgeOpsReviewResolved = "resolved"
	KnowledgeOpsReviewIgnored  = "ignored"
)

// KnowledgeOpsSummary is the aggregate block rendered by the operations board.
type KnowledgeOpsSummary struct {
	TotalTurns         int64   `json:"total_turns"`
	ActiveUsers        int64   `json:"active_users"`
	ActiveSessions     int64   `json:"active_sessions"`
	Completed          int64   `json:"completed"`
	NoAnswer           int64   `json:"no_answer"`
	Failed             int64   `json:"failed"`
	WithCitations      int64   `json:"with_citations"`
	TokensIn           int64   `json:"tokens_in"`
	TokensOut          int64   `json:"tokens_out"`
	Positive           int64   `json:"positive"`
	Negative           int64   `json:"negative"`
	CitationRate       float64 `json:"citation_rate"`
	NoAnswerRate       float64 `json:"no_answer_rate"`
	FailureRate        float64 `json:"failure_rate"`
	SatisfactionRate   float64 `json:"satisfaction_rate"`
	AvgLatencyMs       float64 `json:"avg_latency_ms"`
	AvgResolutionHours float64 `json:"avg_resolution_hours"`
}

// KnowledgeOpsQueryRow is a normalized Top Query aggregate.
type KnowledgeOpsQueryRow struct {
	Question        string  `json:"question"`
	QuestionHash    string  `json:"question_hash"`
	Requests        int64   `json:"requests"`
	LastAskedAt     string  `json:"last_asked_at"`
	NoAnswerCount   int64   `json:"no_answer_count"`
	FailedCount     int64   `json:"failed_count"`
	CitationMissing int64   `json:"citation_missing_count"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`
}
