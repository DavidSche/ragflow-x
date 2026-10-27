package model

import "time"

// AlertEvent persists one operational notification so it can be reviewed and
// claimed after the webhook has been dispatched.
type AlertEvent struct {
	ID          string     `gorm:"column:id;primaryKey;size:36" json:"id"`
	TenantID    string     `gorm:"column:tenant_id;size:32;index" json:"tenant_id"`
	Title       string     `gorm:"column:title;size:256;not null" json:"title"`
	Severity    string     `gorm:"column:severity;size:16;not null;index" json:"severity"`
	Type        string     `gorm:"column:type;size:64;not null;index" json:"type"`
	Resource    string     `gorm:"column:resource;size:128" json:"resource"`
	ResourceID  string     `gorm:"column:resource_id;size:64" json:"resource_id"`
	Detail      string     `gorm:"column:detail;type:text" json:"detail"`
	FieldsJSON  string     `gorm:"column:fields_json;type:text" json:"fields_json"`
	Fingerprint string     `gorm:"column:fingerprint;size:64;index" json:"fingerprint"`
	OccurredAt  time.Time  `gorm:"column:occurred_at;not null;index" json:"occurred_at"`
	Status      string     `gorm:"column:status;size:16;not null;default:open;index" json:"status"`
	AckedBy     string     `gorm:"column:acked_by;size:32" json:"acked_by"`
	AckedAt     *time.Time `gorm:"column:acked_at" json:"acked_at"`
	CreatedAt   time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (AlertEvent) TableName() string { return "rgx_alert_event" }

// AlertDelivery is the restartable delivery state for one alert and channel.
type AlertDelivery struct {
	AlertEventID    string     `gorm:"column:alert_event_id;primaryKey;size:36" json:"alert_event_id"`
	Channel         string     `gorm:"column:channel;primaryKey;size:64" json:"channel"`
	TenantID        string     `gorm:"column:tenant_id;size:32;index" json:"tenant_id"`
	Status          string     `gorm:"column:status;size:24;not null;default:pending;index" json:"status"`
	Attempts        int        `gorm:"column:attempts;not null;default:0" json:"attempts"`
	LastError       string     `gorm:"column:last_error;type:text" json:"last_error"`
	LastAttemptAt   time.Time  `gorm:"column:last_attempt_at;not null;index" json:"last_attempt_at"`
	NextRetryAt     *time.Time `gorm:"column:next_retry_at;index" json:"next_retry_at"`
	LeaseOwner      string     `gorm:"column:lease_owner;size:128;not null;default:''" json:"lease_owner"`
	LeaseGeneration int64      `gorm:"column:lease_generation;not null;default:0" json:"lease_generation"`
	LeaseExpiresAt  *time.Time `gorm:"column:lease_expires_at;index" json:"lease_expires_at"`
	DeliveredAt     *time.Time `gorm:"column:delivered_at" json:"delivered_at"`
	CreatedAt       time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (AlertDelivery) TableName() string { return "rgx_alert_delivery" }

// Alert lifecycle states.
const (
	AlertStatusOpen    = "open"
	AlertStatusRead    = "read"
	AlertStatusClaimed = "claimed"
)

// Alert delivery lifecycle states.
const (
	AlertDeliveryStatusPending   = "pending"
	AlertDeliveryStatusSucceeded = "succeeded"
	AlertDeliveryStatusFailed    = "failed"
	AlertDeliveryStatusAbandoned = "abandoned"
)
