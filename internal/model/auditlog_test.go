package model

import (
	"testing"
	"time"
)

func TestAuditHashCanonicalizesPostgresTimestampPrecision(t *testing.T) {
	base := time.Date(2026, 9, 2, 11, 38, 36, 123444000, time.UTC)
	entry := AuditLog{ID: "a", TenantID: "t", UserID: "u", Action: "test", Resource: "audit", At: base}
	first := AuditHash("", &entry)
	entry.At = base.Add(123 * time.Nanosecond)
	if AuditHash("", &entry) != first {
		t.Fatal("audit hash changed for sub-microsecond PostgreSQL precision")
	}
}
