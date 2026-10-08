package model

import "time"

const (
	SourceTypeTool      = "tool"
	SourceTypeKnowledge = "knowledge"
	SourceTypeDB        = "db"
)

// SourceRoutingRule is the deterministic intent-to-tool mapping used before
// P1-B adds the full four-layer authorization intersection.
type SourceRoutingRule struct {
	ID                  string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID            string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	AssistantID         string    `gorm:"column:assistant_id;index;size:32" json:"assistant_id"`
	SourceType          string    `gorm:"column:source_type;size:32;not null" json:"source_type"`
	Matcher             string    `gorm:"column:matcher;type:text;not null" json:"matcher"`
	ToolID              string    `gorm:"column:tool_id;index;size:64;not null" json:"tool_id"`
	ToolVersion         string    `gorm:"column:tool_version;size:64;not null" json:"tool_version"`
	PolicyVersion       string    `gorm:"column:policy_version;size:64;not null" json:"policy_version"`
	AuthorizationScope  string    `gorm:"column:authorization_scope;type:text;not null" json:"authorization_scope"`
	Priority            int       `gorm:"column:priority;not null" json:"priority"`
	ConfidenceThreshold float64   `gorm:"column:confidence_threshold;not null" json:"confidence_threshold"`
	Active              bool      `gorm:"column:active;not null" json:"active"`
	CreatedBy           string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt           time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (SourceRoutingRule) TableName() string { return "rgx_source_routing_rule" }
