package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/db/dbtest"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The asset-governance board renders `assistant_ids.length`,
// `chunk_evidence.length` and `knowledge_tasks.length` directly, so a dataset
// with no ACTIVE binding must still serialize those fields as `[]`. A nil slice
// marshals as `null` and blanked the whole page with
// "TypeError: Cannot read properties of null (reading 'length')" (E159).
func TestKnowledgeAssetViewSerializesEmptyListsAsArrays(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: dbtest.SQLiteDSN(t, "knowledge-asset-view.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := NewStore(gdb)
	t.Cleanup(func() { _ = s.Close() })

	// A dataset with no rgx_dataset_binding_version row at all: the binding maps
	// have no key for it, which is the nil-slice path.
	datasets := []model.DatasetLink{
		{ID: "ds-no-binding", TenantID: "tenant-1", Name: "no-binding"},
		{ID: "ds-no-binding-2", TenantID: "tenant-1", Name: "no-binding-2"},
	}
	views, err := s.ListKnowledgeAssetViews(ctx, "tenant-1", datasets, 5)
	if err != nil {
		t.Fatalf("list views: %v", err)
	}
	if len(views) != len(datasets) {
		t.Fatalf("views = %d, want %d", len(views), len(datasets))
	}

	listFields := []string{"assistant_ids", "assistant_release_ids", "trace_runs", "chunk_evidence", "knowledge_tasks"}
	for _, view := range views {
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatalf("marshal view: %v", err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &raw); err != nil {
			t.Fatalf("unmarshal view: %v", err)
		}
		for _, field := range listFields {
			if string(raw[field]) == "null" {
				t.Errorf("%s of dataset %s serialized as null, want []: %s", field, view.ID, encoded)
			}
		}
	}
}
