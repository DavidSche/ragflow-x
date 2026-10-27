package repository

// ScenarioID: SC-APPROVAL-001

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestP0_APPROVAL_001_ExportIDSnapshotRemainsStableAcrossChanges(t *testing.T) {
	store := newApprovalStore(t)
	ctx := context.Background()
	first := createTestApproval(t, store, "t1", "first")
	second := createTestApproval(t, store, "t1", "second")
	foreign := createTestApproval(t, store, "t2", "foreign")
	base := time.Now().UTC().Truncate(time.Second)
	createdAt := map[string]time.Time{first.ID: base.Add(time.Second), second.ID: base, foreign.ID: base.Add(2 * time.Second)}
	for _, item := range []*model.Approval{first, second, foreign} {
		if err := store.DB.Model(&model.Approval{}).Where("id = ?", item.ID).Update("created_at", createdAt[item.ID]).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, total, err := store.ListApprovalExportIDs(ctx, false, "t1", ApprovalFilter{}, ApprovalListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	snapshotSet := map[string]bool{first.ID: true, second.ID: true}
	if total != 2 || len(snapshot) != 2 || !snapshotSet[snapshot[0]] || !snapshotSet[snapshot[1]] || snapshot[0] == snapshot[1] {
		t.Fatalf("tenant snapshot = %v, total=%d, want only first/second IDs", snapshot, total)
	}

	if err := store.DB.Where("id = ?", first.ID).Delete(&model.Approval{}).Error; err != nil {
		t.Fatal(err)
	}
	createTestApproval(t, store, "t1", "new")
	current, total, err := store.ListApprovalExportIDs(ctx, false, "t1", ApprovalFilter{}, ApprovalListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(current) != 2 || current[0] == first.ID {
		t.Fatalf("current export IDs = %v, total=%d; deleted ID must not remain", current, total)
	}

	rows, err := store.ListApprovalsByIDs(ctx, false, "t1", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != second.ID {
		t.Fatalf("stable ID detail rows = %+v, want only %s", rows, second.ID)
	}
}
