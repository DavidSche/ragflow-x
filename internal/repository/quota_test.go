package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newQuotaStore(t *testing.T) *store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "q.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := &store{DB: gdb}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustLimit(t *testing.T, store Store, keyID, period string, limit int64) {
	t.Helper()
	if err := store.UpsertQuotaLimit(context.Background(), &model.QuotaLimit{
		TenantID: "t1", KeyID: keyID, PeriodStart: period, TokenLimit: limit,
	}); err != nil {
		t.Fatalf("upsert quota limit: %v", err)
	}
	got, err := store.GetQuotaLimit(context.Background(), keyID, period)
	if err != nil || got == nil {
		t.Fatalf("get quota limit: %v %v", got, err)
	}
}

func mustRequestLimit(t *testing.T, store Store, keyID, period string, limit int64) {
	t.Helper()
	if err := store.UpsertQuotaLimit(context.Background(), &model.QuotaLimit{
		TenantID: "t1", KeyID: keyID, PeriodStart: period, RequestLimit: limit,
	}); err != nil {
		t.Fatalf("upsert request quota limit: %v", err)
	}
}

func TestUpsertQuotaLimitIdempotent(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustLimit(t, store, "k1", "2026-08", 100)
	// Second upsert with a different limit must NOT overwrite the first row.
	mustLimit(t, store, "k1", "2026-08", 200)
	got, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenLimit != 100 {
		t.Fatalf("expected first limit preserved (100), got %d", got.TokenLimit)
	}
}

func TestReserveQuotaTokensEnforcesLimit(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustLimit(t, store, "k1", "2026-08", 100)

	applied, remaining, requestRemaining, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", "req-first", 60, false)
	if err != nil || !applied {
		t.Fatalf("first reserve: applied=%v remaining=%d err=%v", applied, remaining, err)
	}
	if remaining != 40 {
		t.Fatalf("expected remaining 40, got %d", remaining)
	}
	if requestRemaining != -1 {
		t.Fatalf("expected unlimited request remaining -1, got %d", requestRemaining)
	}
	applied, remaining, requestRemaining, err = store.ReserveQuotaTokens(ctx, "k1", "2026-08", "req-second", 60, false)
	if err != nil {
		t.Fatalf("second reserve err: %v", err)
	}
	if applied {
		t.Fatalf("second reserve must be rejected, remaining=%d", remaining)
	}
	if remaining != 40 {
		t.Fatalf("expected remaining still 40, got %d", remaining)
	}
	if requestRemaining != -1 {
		t.Fatalf("expected unlimited request remaining still -1, got %d", requestRemaining)
	}
	q, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if err != nil || q.Pending != 60 {
		t.Fatalf("expected pending 60, got %+v err=%v", q, err)
	}
}

func TestReserveQuotaTokensUnlimited(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustLimit(t, store, "k1", "2026-08", 0) // 0 = unlimited
	applied, remaining, requestRemaining, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", "reservation", 1<<30, false)
	if err != nil || !applied {
		t.Fatalf("unlimited reserve: applied=%v remaining=%d err=%v", applied, remaining, err)
	}
	if remaining != -1 {
		t.Fatalf("expected unlimited remaining -1, got %d", remaining)
	}
	if requestRemaining != -1 {
		t.Fatalf("expected unlimited request remaining -1, got %d", requestRemaining)
	}
}

// ScenarioID: SC-QUOTA-001
func TestP0_QUOTA_001_ReserveQuotaTokensConcurrentNoOvershoot(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustLimit(t, store, "k1", "2026-08", 100)

	const workers = 20
	results := make([]bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			applied, _, _, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", fmt.Sprintf("req-%02d", i), 10, false)
			if err != nil {
				t.Errorf("reserve %d err: %v", i, err)
				return
			}
			results[i] = applied
		}(i)
	}
	wg.Wait()

	appliedCount := 0
	for i := range results {
		if results[i] {
			appliedCount++
		}
	}
	if appliedCount > 10 {
		t.Fatalf("overshoot: %d reservations applied with a 100-token budget of 10 each", appliedCount)
	}
	if appliedCount != 10 {
		t.Fatalf("expected exactly 10 reservations to apply, got %d", appliedCount)
	}
	q, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if err != nil || q.Pending != 100 {
		t.Fatalf("expected pending 100, got %+v err=%v", q, err)
	}
}

// ScenarioID: SC-QUOTA-001
func TestP0_QUOTA_001_QuotaReservationIdempotent(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()

	inserted, err := store.AddQuotaReservation(ctx, &model.QuotaReservation{
		RequestID: "req-1", TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
		Estimated: 10, Status: model.QuotaReservationPending,
	})
	if err != nil || !inserted {
		t.Fatalf("first reservation: inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.AddQuotaReservation(ctx, &model.QuotaReservation{
		RequestID: "req-1", TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
		Estimated: 10, Status: model.QuotaReservationPending,
	})
	if err != nil || inserted {
		t.Fatalf("second reservation must be a no-op: inserted=%v err=%v", inserted, err)
	}
}

func TestReserveQuotaRequestOnlyRejectsOverLimit(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustRequestLimit(t, store, "k1", "2026-08", 2)

	for i, requestID := range []string{"req-0", "req-1"} {
		inserted, err := store.AddQuotaReservation(ctx, &model.QuotaReservation{
			RequestID: requestID, TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
			Status: model.QuotaReservationPending,
		})
		if err != nil || !inserted {
			t.Fatalf("add reservation %d: inserted=%v err=%v", i, inserted, err)
		}
		applied, _, requestRemaining, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", requestID, 0, true)
		if err != nil || !applied || requestRemaining != int64(1-i) {
			t.Fatalf("reserve %d: applied=%v remaining=%d err=%v", i, applied, requestRemaining, err)
		}
		q, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
		if err != nil || q.PendingRequests != int64(i+1) {
			t.Fatalf("reserve %d pending: %+v err=%v", i, q, err)
		}
	}

	inserted, err := store.AddQuotaReservation(ctx, &model.QuotaReservation{
		RequestID: "req-2", TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
		Status: model.QuotaReservationPending,
	})
	if err != nil || !inserted {
		t.Fatalf("add rejected reservation: inserted=%v err=%v", inserted, err)
	}
	applied, _, requestRemaining, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", "req-2", 0, true)
	if err != nil || applied || requestRemaining != 0 {
		t.Fatalf("expected request quota rejection, applied=%v remaining=%d err=%v", applied, requestRemaining, err)
	}
	if err := store.ReleaseQuotaReservation(ctx, "req-2"); err != nil {
		t.Fatalf("release rejected reservation: %v", err)
	}
	q, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if err != nil || q.PendingRequests != 2 {
		t.Fatalf("expected pending requests unchanged at 2, got %+v err=%v", q, err)
	}
}

func TestReserveQuotaRequestsConcurrentNoOvershoot(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustRequestLimit(t, store, "k1", "2026-08", 10)

	const workers = 20
	results := make([]bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			requestID := fmt.Sprintf("req-%02d", i)
			inserted, err := store.AddQuotaReservation(ctx, &model.QuotaReservation{
				RequestID: requestID, TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
				Status: model.QuotaReservationPending,
			})
			if err != nil || !inserted {
				t.Errorf("add reservation %d: inserted=%v err=%v", i, inserted, err)
				return
			}
			applied, _, _, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", requestID, 0, true)
			if err != nil {
				t.Errorf("reserve %d err: %v", i, err)
				return
			}
			results[i] = applied
		}(i)
	}
	wg.Wait()

	appliedCount := 0
	for _, applied := range results {
		if applied {
			appliedCount++
		}
	}
	if appliedCount != 10 {
		t.Fatalf("expected exactly 10 requests to apply, got %d", appliedCount)
	}
	q, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if err != nil || q.PendingRequests != 10 {
		t.Fatalf("expected pending requests 10, got %+v err=%v", q, err)
	}
}

// ScenarioID: SC-QUOTA-001
func TestP0_QUOTA_001_QuotaRequestLifecycleAndRetryIdempotency(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustRequestLimit(t, store, "k1", "2026-08", 3)

	for attempt := 0; attempt < 2; attempt++ {
		inserted, err := store.AddQuotaReservation(ctx, &model.QuotaReservation{
			RequestID: "req-retry", TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
			Status: model.QuotaReservationPending,
		})
		if err != nil {
			t.Fatalf("retry add: %v", err)
		}
		if attempt == 0 && !inserted {
			t.Fatal("first retry reservation must insert")
		}
		if attempt == 1 && inserted {
			t.Fatal("retry with the same request ID must not insert")
		}
	}
	applied, _, requestRemaining, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", "req-retry", 0, true)
	if err != nil || !applied || requestRemaining != 2 {
		t.Fatalf("reserve retry: applied=%v remaining=%d err=%v", applied, requestRemaining, err)
	}

	if err := store.FinalizeQuotaReservation(ctx, "req-retry", 0); err != nil {
		t.Fatalf("finalize request: %v", err)
	}
	q, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if err != nil || q.RequestsUsed != 1 || q.PendingRequests != 0 {
		t.Fatalf("after finalize requests: %+v err=%v", q, err)
	}
	if err := store.FinalizeQuotaReservation(ctx, "req-retry", 0); err != nil {
		t.Fatalf("double finalize request: %v", err)
	}
	q, _ = store.GetQuotaLimit(ctx, "k1", "2026-08")
	if q.RequestsUsed != 1 || q.PendingRequests != 0 {
		t.Fatalf("double finalize must be idempotent: %+v", q)
	}

	mustReserve(t, store, "req-released", 0)
	inserted, err := store.AddQuotaReservation(ctx, &model.QuotaReservation{
		RequestID: "req-request-released", TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
		Status: model.QuotaReservationPending,
	})
	if err != nil || !inserted {
		t.Fatalf("add released request: inserted=%v err=%v", inserted, err)
	}
	applied, _, _, err = store.ReserveQuotaTokens(ctx, "k1", "2026-08", "req-request-released", 0, true)
	if err != nil || !applied {
		t.Fatalf("reserve released request: applied=%v err=%v", applied, err)
	}
	if err := store.ReleaseQuotaReservation(ctx, "req-released"); err != nil {
		t.Fatalf("release request: %v", err)
	}
	if err := store.ReleaseQuotaReservation(ctx, "req-request-released"); err != nil {
		t.Fatalf("release request reservation: %v", err)
	}
	q, _ = store.GetQuotaLimit(ctx, "k1", "2026-08")
	if q.RequestsUsed != 1 || q.PendingRequests != 0 {
		t.Fatalf("after release requests: %+v", q)
	}
}

func TestFinalizeAndReleaseBalance(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustLimit(t, store, "k1", "2026-08", 100)

	mustReserve(t, store, "req-1", 10)
	if err := store.FinalizeQuotaReservation(ctx, "req-1", 6); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	q, _ := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if q.TokensUsed != 6 || q.Pending != 0 {
		t.Fatalf("after finalize: used=%d pending=%d", q.TokensUsed, q.Pending)
	}
	// A second finalize for the same request must be a no-op.
	if err := store.FinalizeQuotaReservation(ctx, "req-1", 6); err != nil {
		t.Fatalf("double finalize: %v", err)
	}
	q, _ = store.GetQuotaLimit(ctx, "k1", "2026-08")
	if q.TokensUsed != 6 || q.Pending != 0 {
		t.Fatalf("after double finalize: used=%d pending=%d", q.TokensUsed, q.Pending)
	}

	mustReserve(t, store, "req-2", 10)
	if err := store.ReleaseQuotaReservation(ctx, "req-2"); err != nil {
		t.Fatalf("release: %v", err)
	}
	q, _ = store.GetQuotaLimit(ctx, "k1", "2026-08")
	if q.TokensUsed != 6 || q.Pending != 0 {
		t.Fatalf("after release: used=%d pending=%d", q.TokensUsed, q.Pending)
	}
	if err := store.ReleaseQuotaReservation(ctx, "req-2"); err != nil {
		t.Fatalf("double release: %v", err)
	}
}

func TestReapStaleQuotaReservations(t *testing.T) {
	store := newQuotaStore(t)
	ctx := context.Background()
	mustLimit(t, store, "k1", "2026-08", 100)

	mustReserve(t, store, "req-stale", 40)
	// Simulate a crash: backdate the reservation far into the past without
	// reconcile. The reap treats it as orphaned and releases the pending.
	db := store.WithContext(ctx).Model(&model.QuotaReservation{}).
		Where("request_id = ?", "req-stale").
		Update("created_at", time.Now().Add(-2*time.Hour))
	if db.Error != nil {
		t.Fatal(db.Error)
	}

	released, err := store.ReapStaleQuotaReservations(ctx, "k1", "2026-08", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if released != 40 {
		t.Fatalf("expected 40 tokens reaped, got %d", released)
	}
	q, err := store.GetQuotaLimit(ctx, "k1", "2026-08")
	if err != nil || q.Pending != 0 {
		t.Fatalf("expected pending 0 after reap, got %+v err=%v", q, err)
	}
	r, err := store.GetQuotaReservation(ctx, "req-stale")
	if err != nil || r.Status != model.QuotaReservationReleased {
		t.Fatalf("expected reservation released, got %+v err=%v", r, err)
	}
}

// mustReserve reserves est tokens into the pending balance and records a
// reservation ledger row, mirroring the service preauthorization sequence.
func mustReserve(t *testing.T, store Store, requestID string, est int64) {
	t.Helper()
	ctx := context.Background()
	inserted, err := store.AddQuotaReservation(ctx, &model.QuotaReservation{
		RequestID: requestID, TenantID: "t1", KeyID: "k1", PeriodStart: "2026-08",
		Estimated: est, Status: model.QuotaReservationPending,
	})
	if err != nil || !inserted {
		t.Fatalf("add reservation %s: inserted=%v err=%v", requestID, inserted, err)
	}
	applied, _, _, err := store.ReserveQuotaTokens(ctx, "k1", "2026-08", requestID, est, false)
	if err != nil || !applied {
		t.Fatalf("reserve %s: applied=%v err=%v", requestID, applied, err)
	}
}
