package model

import "time"

// AuditAnchor is an immutable tamper-evidence snapshot of one tenant's audit
// hash-chain tail. It is the first layer of external anchoring: a periodic
// worker freezes tenant, sequence, hash and time; future export targets can
// copy these records to WORM/object storage without re-reading the full chain.
type AuditAnchor struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uk_audit_anchor_tenant_seq_hash" json:"tenant_id"`
	LastSeq   int64     `gorm:"column:last_seq;not null;uniqueIndex:uk_audit_anchor_tenant_seq_hash" json:"last_seq"`
	LastHash  string    `gorm:"column:last_hash;size:64;not null;uniqueIndex:uk_audit_anchor_tenant_seq_hash" json:"last_hash"`
	Algorithm string    `gorm:"column:algorithm;size:24;not null;default:sha256-v1" json:"algorithm"`
	AnchorAt  time.Time `gorm:"column:anchor_at;not null;index" json:"anchor_at"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName is the physical table name.
func (AuditAnchor) TableName() string { return "rgx_audit_anchor" }

// AuditAnchorAlgorithm is the frozen canonical hash algorithm label.
const AuditAnchorAlgorithm = "sha256-v1"
