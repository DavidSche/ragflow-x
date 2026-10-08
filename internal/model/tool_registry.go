package model

import "time"

const (
	ToolTypeCalculation        = "calculation"
	ToolTypeSQLQuery           = "sql_query"
	ToolTypeLookup             = "lookup"
	ToolTypeKnowledgeRetrieval = "knowledge_retrieval"
)

// ToolRegistry is the tenant-owned governance record for deterministic tools.
// It stores contracts only; execution remains behind an explicit implementation
// allowlist and a later routing slice.
type ToolRegistry struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;index;size:32;not null;uniqueIndex:uq_rgx_tool_registry_version" json:"tenant_id"`
	ToolID             string    `gorm:"column:tool_id;size:64;not null;uniqueIndex:uq_rgx_tool_registry_version" json:"tool_id"`
	Version            string    `gorm:"column:version;size:64;not null;uniqueIndex:uq_rgx_tool_registry_version" json:"version"`
	ToolType           string    `gorm:"column:tool_type;size:32;not null" json:"tool_type"`
	Name               string    `gorm:"column:name;size:128;not null" json:"name"`
	InputSchema        string    `gorm:"column:input_schema;type:text;not null" json:"input_schema"`
	OutputSchema       string    `gorm:"column:output_schema;type:text;not null" json:"output_schema"`
	AuthorizationScope string    `gorm:"column:authorization_scope;type:text;not null" json:"authorization_scope"`
	ImplementationRef  string    `gorm:"column:implementation_ref;size:256;not null" json:"implementation_ref"`
	Active             bool      `gorm:"column:active;not null" json:"active"`
	CreatedBy          string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt          time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ToolRegistry) TableName() string { return "rgx_tool_registry" }
