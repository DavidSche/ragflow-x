package model

import "time"

// AgentShadow is the platform's ownership record for a RAGFlow agent canvas.
// RAGFlow remains the source of truth for the DSL; this table maps an agent id
// to the platform tenant that owns it so multi-tenant isolation holds even
// though providers authenticate with a shared engine credential.
type AgentShadow struct {
	ID         string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	TenantID   string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	TenantName string    `gorm:"-" json:"tenant_name,omitempty"`
	Title      string    `gorm:"column:title;size:128;not null" json:"title"`
	Status     string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	Release    bool      `gorm:"column:release;not null;default:false;index" json:"release"`
	OwnerID    string    `gorm:"column:owner_id;size:32;index" json:"owner_id"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`

	// CanvasCategory is copied from the live RAGFlow canvas for API views.
	// RAGFlow remains authoritative and the ownership shadow does not store it.
	CanvasCategory string `gorm:"-" json:"canvas_category,omitempty"`
}

const (
	AgentCanvasCategoryWorkflow = "agent_canvas"
	AgentCanvasCategoryDataflow = "dataflow_canvas"
)

// TableName is the physical table name.
func (AgentShadow) TableName() string { return "rgx_agent" }
