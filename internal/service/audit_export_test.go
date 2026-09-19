package service

import (
	"context"
	"encoding/csv"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_AuditExportRejectsOverLimitResults(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "audit-export.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	overLimit := &overLimitAuditStore{Store: svc.Store, total: auditExportLimit + 1}
	svc.Store = overLimit

	scope := TenantScope{
		Kind: TenantScopeCurrent, ActorTenantID: model.PlatformTenantID,
		TargetTenantID: model.PlatformTenantID, AllowedTenantIDs: []string{model.PlatformTenantID},
	}
	rows, data, err := svc.ExportAuditsForScope(context.Background(), scope)
	if rows != 0 || data != nil {
		t.Fatalf("over-limit export returned rows=%d data=%v", rows, data)
	}
	var businessErr *httperr.Error
	if !errors.As(err, &businessErr) || businessErr.Status != 400 || businessErr.Code != 40096 {
		t.Fatalf("over-limit error = %v, want 400/40096", err)
	}
	if overLimit.calls != 1 {
		t.Fatalf("store calls = %d, want 1", overLimit.calls)
	}
}

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_AuditCSVNeutralizesFormulaPrefixes(t *testing.T) {
	danger := "=1+1"
	data, err := auditCSV([]model.AuditLog{{
		UserID: danger, Action: danger, Resource: danger, ResourceID: danger,
		DetailJSON: danger, IP: danger, TraceID: danger, Hash: danger,
		PrevHash: danger, At: time.Unix(0, 0).UTC(),
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
		if index == 0 || value == "" {
			continue
		}
		if value[0] != '\'' || value[1:] != danger {
			t.Fatalf("csv column %d = %q, want %q", index, value, "'"+danger)
		}
	}
}

type overLimitAuditStore struct {
	repository.Store
	total int64
	calls int
}

func (store *overLimitAuditStore) ListAuditsForScope(
	context.Context, bool, []string, int, int, repository.AuditFilter,
) ([]model.AuditLog, int64, error) {
	store.calls++
	return nil, store.total, nil
}
