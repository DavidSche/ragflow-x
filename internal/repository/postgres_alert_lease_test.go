package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm/clause"
)

// ScenarioID: SC-AUDIT-002
func TestP0_PG_008_PostgresAlertDeliveryLeasesAreAtomic(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	delivery := &model.AlertDelivery{
		AlertEventID: "pg-alert-delivery-lease", Channel: "primary", TenantID: "tenant-1",
		Status: model.AlertDeliveryStatusFailed, LastAttemptAt: now.Add(-time.Minute),
		NextRetryAt: &now, LeaseOwner: "worker-a", LeaseExpiresAt: &now,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}
	if err := store.UpsertAlertDelivery(ctx, delivery); err != nil {
		t.Fatalf("create leased delivery: %v", err)
	}

	expiresAt := now.Add(time.Minute)
	claimed, err := store.ClaimAlertDeliveryLease(ctx, delivery.AlertEventID, delivery.Channel, "worker-b", expiresAt, now)
	if err != nil || claimed == nil || claimed.LeaseOwner != "worker-b" || claimed.LeaseExpiresAt == nil || !claimed.LeaseExpiresAt.Equal(expiresAt) || claimed.LeaseGeneration != 1 {
		t.Fatalf("claim expired PostgreSQL lease: claimed=%+v err=%v", claimed, err)
	}
	second, err := store.ClaimAlertDeliveryLease(ctx, delivery.AlertEventID, delivery.Channel, "worker-c", expiresAt, now)
	if err != nil || second != nil {
		t.Fatalf("second PostgreSQL lease claim: claimed=%v err=%v", second, err)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_PG_011_PostgresAlertDeliveryLeaseResultIsFenced(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	delivery := &model.AlertDelivery{
		AlertEventID: "pg-alert-delivery-fencing", Channel: "primary", TenantID: "tenant-1",
		Status: model.AlertDeliveryStatusFailed, LastAttemptAt: now.Add(-time.Minute),
		NextRetryAt: &now, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}
	if err := store.UpsertAlertDelivery(ctx, delivery); err != nil {
		t.Fatalf("create PostgreSQL delivery: %v", err)
	}

	expiresAt := now.Add(time.Minute)
	winner, err := store.ClaimAlertDeliveryLease(ctx, delivery.AlertEventID, delivery.Channel, "new-worker", expiresAt, now)
	if err != nil || winner == nil || winner.LeaseGeneration != 1 {
		t.Fatalf("claim PostgreSQL lease: claimed=%+v err=%v", winner, err)
	}

	succeeded := *winner
	succeeded.Status = model.AlertDeliveryStatusSucceeded
	succeeded.LastAttemptAt = now
	succeeded.DeliveredAt = &now
	succeeded.UpdatedAt = now
	if valid, err := store.UpdateAlertDeliveryLease(ctx, winner.AlertEventID, winner.Channel, "old-worker", 0, 1, &succeeded); err != nil || valid {
		t.Fatalf("stale PostgreSQL lease result accepted: valid=%v err=%v", valid, err)
	}

	valid, err := store.UpdateAlertDeliveryLease(ctx, winner.AlertEventID, winner.Channel, winner.LeaseOwner, winner.LeaseGeneration, 1, &succeeded)
	if err != nil || !valid {
		t.Fatalf("valid PostgreSQL lease result rejected: valid=%v err=%v", valid, err)
	}
	deliveries, _, err := store.ListAlertDeliveries(ctx, "", true, 1, 20, AlertDeliveryFilter{Channel: winner.Channel})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("load PostgreSQL delivery: items=%d err=%v", len(deliveries), err)
	}
	if persisted := deliveries[0]; persisted.Status != model.AlertDeliveryStatusSucceeded || persisted.LeaseOwner != "" || persisted.LeaseExpiresAt != nil || persisted.DeliveredAt == nil {
		t.Fatalf("PostgreSQL fenced result: %+v", persisted)
	}
}

func TestP0_PG_009_PostgresExpiredAlertDeliveryLeaseHasSingleWinner(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	delivery := &model.AlertDelivery{
		AlertEventID: "pg-alert-delivery-lease-race", Channel: "primary", TenantID: "tenant-1",
		Status: model.AlertDeliveryStatusFailed, LastAttemptAt: now.Add(-time.Minute),
		NextRetryAt: &now, LeaseOwner: "partitioned-worker", LeaseExpiresAt: &now,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}
	if err := store.UpsertAlertDelivery(ctx, delivery); err != nil {
		t.Fatalf("create expired leased delivery: %v", err)
	}

	const claimantCount = 8
	claims := make([]*model.AlertDelivery, claimantCount)
	claimErrors := make([]error, claimantCount)
	var wg sync.WaitGroup
	wg.Add(claimantCount)
	for index := range claimantCount {
		go func(index int) {
			defer wg.Done()
			owner := fmt.Sprintf("worker-%d", index)
			claims[index], claimErrors[index] = store.ClaimAlertDeliveryLease(
				ctx, delivery.AlertEventID, delivery.Channel, owner,
				now.Add(time.Minute), now,
			)
		}(index)
	}
	wg.Wait()

	winner := -1
	for index := range claimantCount {
		if claimErrors[index] != nil {
			t.Fatalf("lease claimant %d: %v", index, claimErrors[index])
		}
		if claims[index] != nil {
			if winner >= 0 {
				t.Fatalf("multiple lease winners: %d and %d", winner, index)
			}
			winner = index
		}
	}
	if winner < 0 {
		t.Fatal("expired lease had no winner")
	}
	expectedOwner := fmt.Sprintf("worker-%d", winner)
	if claims[winner].LeaseOwner != expectedOwner {
		t.Fatalf("lease owner = %q, want %q", claims[winner].LeaseOwner, expectedOwner)
	}

	lateClaimant, err := store.ClaimAlertDeliveryLease(
		ctx, delivery.AlertEventID, delivery.Channel, "late-worker",
		now.Add(time.Minute), now,
	)
	if err != nil || lateClaimant != nil {
		t.Fatalf("active PostgreSQL lease was overtaken: claimed=%v err=%v", lateClaimant, err)
	}
}

func TestP0_PG_010_PostgresLongTransactionSerializesAlertDeliveryLease(t *testing.T) {
	leaseStore := newPostgresContractStore(t)
	root, ok := leaseStore.(*store)
	if !ok {
		t.Fatalf("postgres contract store type = %T", leaseStore)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	delivery := &model.AlertDelivery{
		AlertEventID: "pg-alert-delivery-lease-long-tx", Channel: "primary", TenantID: "tenant-1",
		Status: model.AlertDeliveryStatusFailed, LastAttemptAt: now.Add(-time.Minute),
		NextRetryAt: &now, LeaseOwner: "expired-worker", LeaseExpiresAt: &now,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}
	if err := leaseStore.UpsertAlertDelivery(ctx, delivery); err != nil {
		t.Fatalf("create leased delivery: %v", err)
	}

	longTx := root.DB.WithContext(ctx).Begin()
	if longTx.Error != nil {
		t.Fatalf("begin long transaction: %v", longTx.Error)
	}
	t.Cleanup(func() { _ = longTx.Rollback() })
	var locked model.AlertDelivery
	if err := longTx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("alert_event_id = ? AND channel = ?", delivery.AlertEventID, delivery.Channel).
		First(&locked).Error; err != nil {
		t.Fatalf("lock delivery in long transaction: %v", err)
	}

	type leaseClaim struct {
		claim *model.AlertDelivery
		err   error
	}
	results := make(chan leaseClaim, 1)
	claimStarted := make(chan struct{})
	go func() {
		close(claimStarted)
		claim, err := leaseStore.ClaimAlertDeliveryLease(
			ctx, delivery.AlertEventID, delivery.Channel, "waiting-worker",
			now.Add(time.Minute), now,
		)
		results <- leaseClaim{claim: claim, err: err}
	}()
	<-claimStarted

	select {
	case result := <-results:
		t.Fatalf("claim bypassed uncommitted long transaction: claim=%+v err=%v", result.claim, result.err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := longTx.Commit().Error; err != nil {
		t.Fatalf("commit long transaction: %v", err)
	}
	result := <-results
	if result.err != nil || result.claim == nil || result.claim.LeaseOwner != "waiting-worker" {
		t.Fatalf("claim after long transaction commit: claim=%+v err=%v", result.claim, result.err)
	}
}
