package service

import (
	"context"
	"encoding/csv"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_DatasetExportRejectsOverLimitResults(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "dataset-export.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	overLimit := &overLimitDatasetStore{Store: svc.Store, total: datasetExportLimit + 1}
	svc.Store = overLimit

	scope := TenantScope{
		Kind: TenantScopeCurrent, ActorTenantID: model.PlatformTenantID,
		TargetTenantID: model.PlatformTenantID, AllowedTenantIDs: []string{model.PlatformTenantID},
	}
	rows, data, err := svc.ExportDatasetsForScope(context.Background(), scope)
	if rows != 0 || data != nil {
		t.Fatalf("over-limit export returned rows=%d data=%v", rows, data)
	}
	var businessErr *httperr.Error
	if !errors.As(err, &businessErr) || businessErr.Status != 400 || businessErr.Code != 40104 {
		t.Fatalf("over-limit error = %v, want 400/40104", err)
	}
	if overLimit.calls != 1 {
		t.Fatalf("store calls = %d, want 1", overLimit.calls)
	}
}

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_DatasetCSVNeutralizesFormulaPrefixes(t *testing.T) {
	danger := "=1+1"
	data, err := datasetCSV([]DatasetSummary{{
		ID: danger, TenantID: danger, TenantName: danger, Name: danger,
		RAGFlowDatasetID: danger, DocumentCount: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("csv records = %d, want 2", len(records))
	}
	for index, value := range records[1] {
		if index == 4 {
			if value != "2" {
				t.Fatalf("csv document count = %q, want 2", value)
			}
			continue
		}
		if value[0] != '\'' || value[1:] != danger {
			t.Fatalf("csv column %d = %q, want %q", index, value, "'"+danger)
		}
	}
}

type overLimitDatasetStore struct {
	repository.Store
	total int64
	calls int
}

func (store *overLimitDatasetStore) ListDatasetLinksForScopePage(
	context.Context, bool, []string, repository.DatasetFilter, int, int,
) ([]model.DatasetLink, int64, error) {
	store.calls++
	return nil, store.total, nil
}
