package model

import "time"

// ChatShadow is the platform's ownership record for a RAGFlow chat assistant.
// RAGFlow remains the source of truth for the chat configuration; this table
// maps a chat_id to the platform tenant that owns it so multi-tenant isolation
// holds even though providers authenticate with a shared engine credential.
type ChatShadow struct {
	ID           string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	TenantID     string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name         string    `gorm:"column:name;size:128;not null" json:"name"`
	Status       string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	DatasetIDs   string    `gorm:"column:dataset_ids;size:512" json:"dataset_ids"`
	ConfigJSON   string    `gorm:"column:config_json;type:text" json:"config_json"`
	OwnerID      string    `gorm:"column:owner_id;size:32;index" json:"owner_id"`
	MessageCount int64     `gorm:"column:message_count;not null;default:0" json:"message_count"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (ChatShadow) TableName() string { return "rgx_chat" }
