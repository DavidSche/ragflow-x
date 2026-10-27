package model

import "time"

// SearchAppShadow is the platform's ownership record for a RAGFlow Search App.
// RAGFlow remains the source of truth for the Search App configuration; this
// table maps a search app id to the platform tenant that owns it so multi-tenant
// isolation holds even though providers authenticate with a shared engine
// credential (mirrors ChatShadow for chat assistants).
type SearchAppShadow struct {
	ID         string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	TenantID   string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name       string    `gorm:"column:name;size:128;not null" json:"name"`
	Status     string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	DatasetIDs string    `gorm:"column:dataset_ids;size:512" json:"dataset_ids"`
	OwnerID    string    `gorm:"column:owner_id;size:32;index" json:"owner_id"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (SearchAppShadow) TableName() string { return "rgx_search_app" }
