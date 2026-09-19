package model

import "time"

// MessageFeedback stores a user's like/dislike (and optional comment) on a
// chat turn so quality improvement (B3) has a durable, tenant-scoped source.
// It is keyed per (chat, session, message, user): a user can update their
// rating on the same turn but never create duplicates.
type MessageFeedback struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ChatID    string    `gorm:"column:chat_id;size:64;not null;uniqueIndex:idx_feedback_scope" json:"chat_id"`
	SessionID string    `gorm:"column:session_id;size:64;not null;uniqueIndex:idx_feedback_scope" json:"session_id"`
	MessageID string    `gorm:"column:message_id;size:64;not null;uniqueIndex:idx_feedback_scope" json:"message_id"`
	UserID    string    `gorm:"column:user_id;size:32;not null;uniqueIndex:idx_feedback_scope" json:"user_id"`
	RequestID string    `gorm:"column:request_id;size:64;index" json:"request_id"`
	Rating    string    `gorm:"column:rating;size:16;not null" json:"rating"` // positive | negative
	Comment   string    `gorm:"column:comment;size:1024" json:"comment"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (MessageFeedback) TableName() string { return "rgx_message_feedback" }

// Feedback ratings.
const (
	FeedbackPositive = "positive"
	FeedbackNegative = "negative"
)
