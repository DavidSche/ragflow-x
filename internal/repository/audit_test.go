package repository

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// newAuditStore builds a store whose SQLite backend tolerates concurrent
// writers via busy_timeout, so tests exercise chain logic, not driver limits.
func newAuditStore(t *testing.T) Store {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "audit.db") + "?_pragma=busy_timeout(10000)"
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// verifyTenantChain asserts the tenant's audit chain is gapless, correctly
// linked, and hash-valid when walked in seq order.
func verifyTenantChain(t *testing.T, store Store, tenantID string, wantLen int) {
	t.Helper()
	rows, err := store.ListAuditsAll(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != wantLen {
		t.Fatalf("tenant %s: expected %d audit rows, got %d", tenantID, wantLen, len(rows))
	}
	prev := ""
	for i, r := range rows {
		if r.Seq != int64(i+1) {
			t.Fatalf("tenant %s row %d: seq = %d, want %d (gap or duplicate)", tenantID, i, r.Seq, i+1)
		}
		if r.PrevHash != prev {
			t.Fatalf("tenant %s row %d: prev_hash mismatch", tenantID, i)
		}
		if want := model.AuditHash(prev, &r); r.Hash != want {
			t.Fatalf("tenant %s row %d: hash mismatch", tenantID, i)
		}
		prev = r.Hash
	}
}

// TestCreateAuditConcurrentChainIntact reproduces the historical race: many
// goroutines appending to the same tenant's chain concurrently must produce a
// single linear chain, not a fork.
func TestCreateAuditConcurrentChainIntact(t *testing.T) {
	store := newAuditStore(t)
	ctx := context.Background()

	const goroutines = 16
	const perGoroutine = 5
	const total = goroutines * perGoroutine

	var wg sync.WaitGroup
	errs := make(chan error, total)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				entry := &model.AuditLog{
					TenantID: "t-race", UserID: "u1",
					Action: "gateway.chat", Resource: "chat", ResourceID: "c1",
				}
				if err := store.CreateAudit(ctx, entry); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent CreateAudit failed: %v", err)
	}

	verifyTenantChain(t, store, "t-race", total)

	ok, err := store.LastAudit(ctx, "t-race")
	if err != nil || ok == nil || ok.Seq != total {
		t.Fatalf("chain tail mismatch: %+v, %v", ok, err)
	}
}

// TestCreateAuditPerTenantChainsIndependent asserts interleaved writes to two
// tenants keep two separate, individually intact chains.
// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_CreateAuditPerTenantChainsIndependent(t *testing.T) {
	store := newAuditStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for _, tenant := range []string{"t-a", "t-b"} {
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(tid string) {
				defer wg.Done()
				for i := 0; i < 3; i++ {
					if err := store.CreateAudit(ctx, &model.AuditLog{
						TenantID: tid, Action: "auth.login", Resource: "auth",
					}); err != nil {
						t.Errorf("create audit for %s: %v", tid, err)
						return
					}
				}
			}(tenant)
		}
	}
	wg.Wait()

	verifyTenantChain(t, store, "t-a", 24)
	verifyTenantChain(t, store, "t-b", 24)
}
