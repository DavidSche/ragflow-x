package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestVersionPublishAttemptClaimLeaseAndRetry(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "claim.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now().UTC().Add(-2 * time.Minute)
	attempt := &model.VersionPublishAttempt{
		ID: id.New(), TenantID: id.New(), LogicalDocumentID: id.New(), NewVersionID: id.New(),
		State: model.VersionPublishPreparing, Attempts: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateVersionPublishAttempt(ctx, attempt); err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	claimed, err := store.ClaimDueVersionPublishAttempts(ctx, 10, time.Now().UTC(), 0, 5*time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != attempt.ID {
		t.Fatalf("first claim = %+v err=%v", claimed, err)
	}
	active, err := store.ClaimDueVersionPublishAttempts(ctx, 10, time.Now().UTC().Add(4*time.Minute), 0, 5*time.Minute)
	if err != nil || len(active) != 0 {
		t.Fatalf("active lease must block duplicate claim: %+v err=%v", active, err)
	}
	retryAt := time.Now().UTC()
	if err := store.RetryVersionPublishAttempt(ctx, attempt.ID, 2, "transient provider failure", retryAt); err != nil {
		t.Fatalf("retry attempt: %v", err)
	}
	released, err := store.GetVersionPublishAttempt(ctx, attempt.TenantID, attempt.ID)
	if err != nil || released.Attempts != 2 || released.LastError == "" || released.ClaimedAt != nil || released.ClaimExpiresAt != nil {
		t.Fatalf("released retry attempt = %+v err=%v", released, err)
	}
	due, err := store.ClaimDueVersionPublishAttempts(ctx, 10, retryAt.Add(2*time.Minute), time.Minute, 5*time.Minute)
	if err != nil || len(due) != 1 || due[0].ID != attempt.ID {
		t.Fatalf("retry delay claim = %+v err=%v", due, err)
	}
}
