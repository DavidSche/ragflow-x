package db

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The enhancement budget quota key is "rgx-enhancement:<tenant_id>" — 48 chars
// for the standard 32-char tenant IDs. The key_id columns were declared
// varchar(32), which Postgres enforces (SQLSTATE 22001) but SQLite ignores, so
// compilation triggers 500'd on production-shaped deployments while unit tests
// stayed green. Found by the milestone 201 deployment drill; this contract
// pins the widened columns so the regression cannot come back silently.
func TestPG145EnhancementQuotaKeyColumnsAcceptFullQuotaKey(t *testing.T) {
	dsn := os.Getenv("RGX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set RGX_TEST_POSTGRES_DSN to run the PostgreSQL enhancement quota key contract")
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := Migrate(gdb); err != nil {
		t.Fatalf("postgres migration: %v", err)
	}

	tenantID := "0123456789abcdef0123456789abcdef"
	quotaKey := "rgx-enhancement:" + tenantID
	if len(quotaKey) != len("rgx-enhancement:")+32 {
		t.Fatalf("quota key shape changed: %q (%d chars)", quotaKey, len(quotaKey))
	}

	// information_schema must report the widened limits.
	type column struct {
		ColumnName string
		DataType   string
		// CharMax must carry an explicit column tag: gorm's naming strategy
		// would otherwise look for "char_max" and silently bind NULL, making
		// every varchar check pass vacuously (found running against real
		// PostgreSQL 17.4 in the milestone 204 contract pass).
		CharMax *int `gorm:"column:character_maximum_length"`
	}
	for _, tc := range []struct {
		table, column string
		minWidth      int
	}{
		{"rgx_quota", "key_id", 64},
		{"rgx_quota_reservation", "key_id", 64},
		{"rgx_cost_metric", "key_id", 64},
	} {
		var cols []column
		if err := gdb.Raw(
			`SELECT column_name, data_type, character_maximum_length
			 FROM information_schema.columns
			 WHERE table_name = ? AND column_name = ?`, tc.table, tc.column,
		).Scan(&cols).Error; err != nil {
			t.Fatalf("inspect %s.%s: %v", tc.table, tc.column, err)
		}
		if len(cols) != 1 || cols[0].CharMax == nil {
			t.Fatalf("column %s.%s missing or not varchar: %+v", tc.table, tc.column, cols)
		}
		if *cols[0].CharMax < tc.minWidth {
			t.Fatalf("column %s.%s is varchar(%d), want >= varchar(%d)", tc.table, tc.column, *cols[0].CharMax, tc.minWidth)
		}
	}

	// End-to-end: a reservation with the real 48-char key must persist.
	limit := &model.QuotaLimit{
		ID: "pg145-limit", TenantID: tenantID, KeyID: quotaKey, PeriodStart: "2026-01",
		CreatedAt: gdb.NowFunc(), UpdatedAt: gdb.NowFunc(),
	}
	if err := gdb.Create(limit).Error; err != nil {
		t.Fatalf("insert 48-char quota key into rgx_quota: %v", err)
	}
	reservation := &model.QuotaReservation{
		RequestID: "pg145-request", TenantID: tenantID, KeyID: quotaKey, PeriodStart: "2026-01",
		Status: model.QuotaReservationPending, CreatedAt: gdb.NowFunc(), UpdatedAt: gdb.NowFunc(),
	}
	if err := gdb.Create(reservation).Error; err != nil {
		t.Fatalf("insert 48-char quota key into rgx_quota_reservation: %v", err)
	}
	metric := &model.CostMetric{
		ID: "pg145-metric", RequestID: "pg145-request", TenantID: tenantID, KeyID: quotaKey,
		Date: "2026-01-02", Model: "ragflow-compilation", Scenario: "compilation",
		CreatedAt: gdb.NowFunc(),
	}
	if err := gdb.Create(metric).Error; err != nil {
		t.Fatalf("insert 48-char quota key into rgx_cost_metric: %v", err)
	}

	var stored model.QuotaLimit
	if err := gdb.Where("id = ?", limit.ID).First(&stored).Error; err != nil {
		t.Fatalf("reload quota limit: %v", err)
	}
	if stored.KeyID != quotaKey {
		t.Fatalf("quota key round-trip mismatch: %q", stored.KeyID)
	}

	// Cleanup so the contract can run against a shared database.
	for _, stmt := range []string{
		fmt.Sprintf(`DELETE FROM rgx_quota WHERE id = '%s'`, limit.ID),
		fmt.Sprintf(`DELETE FROM rgx_quota_reservation WHERE request_id = '%s'`, reservation.RequestID),
		fmt.Sprintf(`DELETE FROM rgx_cost_metric WHERE id = '%s'`, metric.ID),
	} {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if err := gdb.Exec(stmt).Error; err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
}
