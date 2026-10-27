package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRecordUsageIdempotent(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "u.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	usage := &model.QuotaUsage{
		TenantID: "t1", UserID: "u1", KeyID: "k1", RequestID: "req-abc",
		Date: "2026-08-20", TokensIn: 10, TokensOut: 20, Requests: 1,
	}

	first, err := store.RecordUsage(ctx, usage)
	if err != nil {
		t.Fatalf("first record failed: %v", err)
	}
	if !first {
		t.Fatal("expected first record to meter")
	}

	// A retry with the same request_id must be a no-op.
	second, err := store.RecordUsage(ctx, usage)
	if err != nil {
		t.Fatalf("second record failed: %v", err)
	}
	if second {
		t.Fatal("expected retry to be idempotent (not metered)")
	}

	sum, err := store.SummarizeUsage(ctx, "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sum) != 1 {
		t.Fatalf("expected one aggregate row, got %d", len(sum))
	}
	if sum[0].TokensIn != 10 || sum[0].TokensOut != 20 || sum[0].Requests != 1 {
		t.Fatalf("usage double-counted: %+v", sum[0])
	}
}

func TestRecordUsageRejectsMissingRequestID(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "u2.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.RecordUsage(context.Background(), &model.QuotaUsage{}); err == nil {
		t.Fatal("expected error for missing request_id")
	}
}

// TestRecordUsageConcurrentNoLoss proves the daily aggregate uses an atomic
// database upsert: concurrent requests for different request_ids must not lose
// counts to a read-modify-write race.
func TestRecordUsageConcurrentNoLoss(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "u3.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = store.RecordUsage(ctx, &model.QuotaUsage{
				TenantID: "t1", UserID: "u1", KeyID: "k1", RequestID: fmt.Sprintf("req-%d", i),
				Date: "2026-08-20", TokensIn: 10, TokensOut: 20, Requests: 1,
			})
		}(i)
	}
	wg.Wait()

	sum, err := store.SummarizeUsage(ctx, "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sum) != 1 || sum[0].TokensIn != 10*n || sum[0].Requests != n {
		t.Fatalf("concurrent metering lost updates: %+v", sum)
	}
}
