package model

import "time"

const (
	SettingRevisionStatusCreated    = "created"
	SettingRevisionStatusCurrent    = "current"
	SettingRevisionStatusSuperseded = "superseded"

	RuntimeApplyPending        = "pending"
	RuntimeApplyApplying       = "applying"
	RuntimeApplyApplied        = "applied"
	RuntimeApplyFailed         = "apply_failed"
	RuntimeApplyPendingRestart = "pending_restart"
)

// SettingRevision is append-only. Repository code must never expose update
// operations for immutable fields or delete operations for the record.
type SettingRevision struct {
	ID                     string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	Revision               int64     `gorm:"column:revision;not null;uniqueIndex:uk_setting_revision_number" json:"revision"`
	SchemaVersion          int       `gorm:"column:schema_version;not null" json:"schema_version"`
	SnapshotJSON           string    `gorm:"column:snapshot_json;type:text;not null" json:"snapshot"`
	RevisionStatus         string    `gorm:"column:revision_status;size:24;not null;default:created;index" json:"revision_status"`
	CreatedBy              string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt              time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt              time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
	Note                   string    `gorm:"column:note;type:text" json:"note"`
	Checksum               string    `gorm:"column:checksum;size:64;not null" json:"checksum"`
	RollbackFromRevisionID string    `gorm:"column:rollback_from_revision_id;size:32;index" json:"rollback_from_revision_id"`
}

func (SettingRevision) TableName() string { return "rgx_setting_revision" }

// SettingCurrent is a singleton desired-state pointer. It may only move to a
// committed SettingRevision; history is never rewritten.
type SettingCurrent struct {
	ID                string    `gorm:"column:id;primaryKey;size:32;not null;default:current" json:"id"`
	DesiredRevisionID string    `gorm:"column:desired_revision_id;size:32;not null;uniqueIndex" json:"desired_revision_id"`
	UpdatedBy         string    `gorm:"column:updated_by;size:32;not null" json:"updated_by"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
	CreatedAt         time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (SettingCurrent) TableName() string { return "rgx_setting_current" }

// SettingSecretVersion is append-only and stores ciphertext only. Setting
// revisions reference these immutable versions; plaintext never enters
// snapshots, diffs, audit payloads, or rollback history.
type SettingSecretVersion struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	SecretKey   string    `gorm:"column:secret_key;size:128;not null;uniqueIndex:uk_setting_secret_version,priority:1" json:"secret_key"`
	Version     int64     `gorm:"column:version;not null;uniqueIndex:uk_setting_secret_version,priority:2" json:"version"`
	Ciphertext  string    `gorm:"column:ciphertext;type:text;not null" json:"-"`
	Fingerprint string    `gorm:"column:fingerprint;size:64;not null" json:"fingerprint"`
	CreatedBy   string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (SettingSecretVersion) TableName() string { return "rgx_setting_secret_version" }

// RuntimeSettingInstanceState records actual application per process instance.
type RuntimeSettingInstanceState struct {
	RuntimeInstanceID string    `gorm:"column:runtime_instance_id;primaryKey;size:64" json:"runtime_instance_id"`
	InstanceIdentity  string    `gorm:"column:instance_identity;size:255;not null;uniqueIndex" json:"instance_identity"`
	RuntimeRevisionID string    `gorm:"column:runtime_revision_id;size:32;not null;index" json:"runtime_revision_id"`
	ApplyStatus       string    `gorm:"column:apply_status;size:24;not null;default:pending;index" json:"apply_status"`
	ApplyError        string    `gorm:"column:apply_error;type:text" json:"apply_error"`
	LastSeenAt        time.Time `gorm:"column:last_seen_at;not null" json:"last_seen_at"`
	CreatedAt         time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
	HeartbeatState    string    `gorm:"-" json:"heartbeat_state"`
}

func (RuntimeSettingInstanceState) TableName() string { return "rgx_runtime_instance_state" }
