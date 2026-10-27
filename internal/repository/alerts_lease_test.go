package repository

// ScenarioID: SC-AUDIT-002

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func newAlertLeaseStore(t *testing.T) *store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "alert-lease.db")})
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

func mustCreateAlertDelivery(t *testing.T, s *store, alertEventID string, status string, nextRetryAt time.Time, leaseOwner string, leaseExpiresAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	delivery := &model.AlertDelivery{
		AlertEventID: alertEventID, Channel: "primary", TenantID: "tenant-1",
		Status: status, LastAttemptAt: nextRetryAt.Add(-time.Minute), NextRetryAt: &nextRetryAt,
		LeaseOwner: leaseOwner, LeaseExpiresAt: leaseExpiresAt,
		CreatedAt: nextRetryAt.Add(-time.Minute), UpdatedAt: nextRetryAt.Add(-time.Minute),
	}
	if err := s.UpsertAlertDelivery(ctx, delivery); err != nil {
		t.Fatalf("create alert delivery %s: %v", alertEventID, err)
	}
}

func mustCreateTerminalAlertDelivery(t *testing.T, s *store, alertEventID string, lastAttemptAt time.Time) {
	t.Helper()
	ctx := context.Background()
	delivery := &model.AlertDelivery{
		AlertEventID: alertEventID, Channel: "primary", TenantID: "tenant-1",
		Status: model.AlertDeliveryStatusFailed, LastAttemptAt: lastAttemptAt,
		CreatedAt: lastAttemptAt, UpdatedAt: lastAttemptAt,
	}
	if err := s.UpsertAlertDelivery(ctx, delivery); err != nil {
		t.Fatalf("create terminal alert delivery %s: %v", alertEventID, err)
	}
}

func TestP0_AUDIT_002_AlertDeliveryLeasesAreAtomicAndExpiring(t *testing.T) {
	s := newAlertLeaseStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	expired := now.Add(-time.Minute)
	mustCreateAlertDelivery(t, s, "due", model.AlertDeliveryStatusFailed, now, "", nil)
	mustCreateAlertDelivery(t, s, "leased", model.AlertDeliveryStatusFailed, now, "worker-a", &expired)
	mustCreateAlertDelivery(t, s, "future", model.AlertDeliveryStatusFailed, now.Add(time.Minute), "", nil)
	mustCreateAlertDelivery(t, s, "succeeded", model.AlertDeliveryStatusSucceeded, now, "", nil)
	mustCreateTerminalAlertDelivery(t, s, "terminal-failed", now)

	expiresAt := now.Add(time.Minute)
	claimed, err := s.ClaimAlertDeliveryLease(ctx, "due", "primary", "worker-a", expiresAt, now)
	if err != nil || claimed == nil {
		t.Fatalf("claim due delivery: claimed=%v err=%v", claimed, err)
	}
	if claimed.LeaseOwner != "worker-a" || claimed.LeaseExpiresAt == nil || !claimed.LeaseExpiresAt.Equal(expiresAt) {
		t.Fatalf("claimed lease = %+v", claimed)
	}
	second, err := s.ClaimAlertDeliveryLease(ctx, "due", "primary", "worker-b", expiresAt, now)
	if err != nil || second != nil {
		t.Fatalf("second lease claim: claimed=%v err=%v", second, err)
	}
	leased, err := s.ClaimAlertDeliveryLease(ctx, "leased", "primary", "worker-b", expiresAt, now)
	if err != nil || leased == nil || leased.LeaseOwner != "worker-b" {
		t.Fatalf("claim expired lease: claimed=%+v err=%v", leased, err)
	}
	if future, err := s.ClaimAlertDeliveryLease(ctx, "future", "primary", "worker-b", expiresAt, now); err != nil || future != nil {
		t.Fatalf("claim future delivery: claimed=%v err=%v", future, err)
	}
	if terminal, err := s.ClaimAlertDeliveryLease(ctx, "succeeded", "primary", "worker-b", expiresAt, now); err != nil || terminal != nil {
		t.Fatalf("claim terminal delivery: claimed=%v err=%v", terminal, err)
	}
	if nonRetryable, err := s.ClaimAlertDeliveryLease(ctx, "terminal-failed", "primary", "worker-b", expiresAt, now); err != nil || nonRetryable != nil {
		t.Fatalf("claim terminal failed delivery: claimed=%v err=%v", nonRetryable, err)
	}
	if _, err := s.ClaimAlertDeliveryLease(ctx, "due", "primary", "", expiresAt, now); err == nil {
		t.Fatal("empty lease owner accepted")
	}
	if claimed.LeaseGeneration != 1 {
		t.Fatalf("lease generation = %d, want 1", claimed.LeaseGeneration)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_AlertDeliveryLeaseResultIsFenced(t *testing.T) {
	s := newAlertLeaseStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	mustCreateAlertDelivery(t, s, "fenced", model.AlertDeliveryStatusFailed, now.Add(-time.Minute), "", nil)

	expiresAt := now.Add(time.Minute)
	winner, err := s.ClaimAlertDeliveryLease(ctx, "fenced", "primary", "worker-a", expiresAt, now.Add(-time.Minute))
	if err != nil || winner == nil || winner.LeaseGeneration != 1 {
		t.Fatalf("claim fenced delivery: claimed=%+v err=%v", winner, err)
	}
	nextRetryAt := now.Add(time.Minute)
	failed := *winner
	failed.Status = model.AlertDeliveryStatusFailed
	failed.LastError = "slow delivery"
	failed.LastAttemptAt = now
	failed.NextRetryAt = &nextRetryAt
	failed.DeliveredAt = nil
	failed.UpdatedAt = now
	if valid, err := s.UpdateAlertDeliveryLease(ctx, winner.AlertEventID, winner.Channel, "worker-b", 0, -1, &failed); err != nil || valid {
		t.Fatalf("stale lease result accepted: valid=%v err=%v", valid, err)
	}

	succeeded := *winner
	succeeded.Status = model.AlertDeliveryStatusSucceeded
	succeeded.LastError = ""
	succeeded.LastAttemptAt = now
	succeeded.NextRetryAt = nil
	succeeded.DeliveredAt = &now
	succeeded.UpdatedAt = now
	if valid, err := s.UpdateAlertDeliveryLease(ctx, winner.AlertEventID, winner.Channel, winner.LeaseOwner, winner.LeaseGeneration, 1, &succeeded); err != nil || !valid {
		t.Fatalf("valid lease result rejected: valid=%v err=%v", valid, err)
	}
	deliveries, _, err := s.ListAlertDeliveries(ctx, winner.TenantID, false, 1, 20, AlertDeliveryFilter{})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("load fenced delivery: items=%d err=%v", len(deliveries), err)
	}
	delivery := deliveries[0]
	if delivery.Status != model.AlertDeliveryStatusSucceeded || delivery.Attempts != 1 || delivery.LeaseOwner != "" ||
		delivery.LeaseExpiresAt != nil || delivery.DeliveredAt == nil {
		t.Fatalf("fenced result did not finalize delivery: %+v", delivery)
	}
}

func TestP0_AUDIT_002_AlertDeliveryLeaseRequiresFutureExpiry(t *testing.T) {
	s := newAlertLeaseStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	mustCreateAlertDelivery(t, s, "invalid-ttl", model.AlertDeliveryStatusFailed, now, "", nil)

	claimed, err := s.ClaimAlertDeliveryLease(
		ctx, "invalid-ttl", "primary", "worker-a", now, now,
	)
	if err == nil || claimed != nil {
		t.Fatalf("non-future lease accepted: claimed=%v err=%v", claimed, err)
	}

	deliveries, _, err := s.ListAlertDeliveries(ctx, "tenant-1", false, 1, 20, AlertDeliveryFilter{})
	if err != nil {
		t.Fatalf("list deliveries after rejected claim: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].LeaseOwner != "" || deliveries[0].LeaseExpiresAt != nil {
		t.Fatalf("rejected claim mutated delivery: %+v", deliveries)
	}
}
