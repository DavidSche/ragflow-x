package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func createSettingRevision(t *testing.T, store Store, createdBy, snapshot string) *model.SettingRevision {
	t.Helper()
	current, err := store.GetSettingCurrentPointer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected := ""
	if current != nil {
		expected = current.DesiredRevisionID
	}
	now := time.Now().UTC()
	revision := &model.SettingRevision{
		SchemaVersion: 1, SnapshotJSON: snapshot, CreatedBy: createdBy,
		CreatedAt: now, UpdatedAt: now, Note: "test", Checksum: "checksum",
	}
	if err := store.CreateSettingRevisionWithPointer(context.Background(), revision, expected); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestSettingRevisionImmutableAndPointerIntegrity(t *testing.T) {
	store, gdb := newResourceSyncStore(t)
	ctx := context.Background()
	first := createSettingRevision(t, store, "admin", `{"groups":{"gateway":{"max_conns":20}}}`)

	if err := gdb.Model(&model.SettingRevision{}).Where("id = ?", first.ID).
		Update("snapshot_json", `{"groups":{"gateway":{"max_conns":99}}}`).Error; err == nil {
		t.Fatal("immutable setting revision update must be rejected")
	}
	if err := gdb.Where("id = ?", first.ID).Delete(&model.SettingRevision{}).Error; err == nil {
		t.Fatal("setting revision delete must be rejected")
	}
	stored, err := store.GetSettingRevision(ctx, first.ID)
	if err != nil || stored == nil || stored.SnapshotJSON != first.SnapshotJSON {
		t.Fatalf("setting revision was mutated: %+v err=%v", stored, err)
	}

	second := createSettingRevision(t, store, "admin", `{"groups":{"gateway":{"max_conns":40}}}`)
	if second.Revision != first.Revision+1 {
		t.Fatalf("revision must be monotonic: first=%d second=%d", first.Revision, second.Revision)
	}
	pointer, err := store.GetSettingCurrentPointer(ctx)
	if err != nil || pointer == nil || pointer.DesiredRevisionID != second.ID {
		t.Fatalf("current pointer must move atomically: %+v err=%v", pointer, err)
	}
	if err := gdb.Model(&model.SettingCurrent{}).Where("id = ?", pointer.ID).
		Update("desired_revision_id", first.ID).Error; err == nil {
		t.Fatal("current pointer must reject superseded revision")
	}
	if err := gdb.Model(&model.SettingCurrent{}).Where("id = ?", pointer.ID).
		Update("desired_revision_id", id.New()).Error; err == nil {
		t.Fatal("current pointer must reject missing revision")
	}
}
