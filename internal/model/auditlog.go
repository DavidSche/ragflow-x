package model

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// AuditHash computes the canonical SHA-256 hash for an audit log entry given
// the previous entry's hash. It is the single source of truth for audit-chain
// integrity: service verification, repository writes, and the backfill
// migration all use it so they can never diverge.
func AuditHash(prev string, l *AuditLog) string {
	// PostgreSQL timestamp columns keep microsecond precision. Canonicalize
	// before hashing so a persisted and re-read timestamp hashes identically.
	at := l.At.UTC().Truncate(time.Microsecond)
	sum := sha256.Sum256([]byte(prev + "|" + l.TenantID + "|" + l.ActorTenantID + "|" + l.TargetTenantID + "|" + l.UserID + "|" + l.Action + "|" +
		l.Resource + "|" + l.ResourceID + "|" + l.DetailJSON + "|" + l.IP + "|" + l.TraceID + "|" +
		l.Scope + "|" + l.Result + "|" + l.ApprovalID + "|" + l.ApprovalActionHash + "|" +
		l.ActingContextID + "|" + l.AuthorizationDecision + "|" + l.AuthorizationPermission + "|" + l.AuthorizationPolicyVersion + "|" +
		at.Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}
