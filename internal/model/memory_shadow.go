package model

import "time"

// MemoryShadow is the platform's ownership record for a RAGFlow Memory.
// RAGFlow remains the source of truth for the memory configuration; this table
// maps a memory id to the platform tenant that owns it so multi-tenant isolation
// holds even though providers authenticate with a shared engine credential.
type MemoryShadow struct {
	ID         string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	TenantID   string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name       string    `gorm:"column:name;size:128;not null" json:"name"`
	MemoryType string    `gorm:"column:memory_type;size:64" json:"memory_type"`
	OwnerID    string    `gorm:"column:owner_id;size:32;index" json:"owner_id"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (MemoryShadow) TableName() string { return "rgx_memory" }
