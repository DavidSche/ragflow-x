package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestUsageReportAndExport(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "usage.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	st := repository.NewStore(gdb)
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.CreateTenant(ctx, &model.Tenant{ID: "t1", Name: "T1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := st.RecordUsage(ctx, &model.QuotaUsage{
			TenantID: "t1", UserID: "u1", KeyID: "k1", Date: "2026-01-01",
			RequestID: fmt.Sprintf("usage-%d", i), TokensIn: 500, TokensOut: 250, Requests: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(st, ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	svc.SetEstimatedCost(0.2, "CNY")
	rows, err := svc.UsageReport(ctx, "t1", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Requests != 2 || rows[0].TokensIn != 1000 || rows[0].TokensOut != 500 {
		t.Fatalf("unexpected usage rows: %+v", rows)
	}
	if rows[0].EstimatedCost < 0.299 || rows[0].EstimatedCost > 0.301 {
		t.Fatalf("unexpected estimated cost %v", rows[0].EstimatedCost)
	}
	csvData, err := svc.ExportUsageCSV(ctx, "t1", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(csvData), "estimated_cost") || strings.Contains(strings.ToLower(string(csvData)), "billing") {
		t.Fatalf("unexpected usage CSV: %s", csvData)
	}
}
