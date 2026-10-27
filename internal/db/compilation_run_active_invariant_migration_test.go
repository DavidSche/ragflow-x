package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// Milestone 212: migration 147 enforces the plan-137 §4.3 single-active-run
// invariant per (tenant, dataset, kind): deterministic backfill of pre-existing
// duplicates (oldest kept, later ones cancelled with
// cancel_scope=invariant_backfill) plus the partial unique index backstop.

func seedCompilationRun(t *testing.T, gdb *gorm.DB, id, tenant, dataset, kind, state string, createdAt time.Time) {
	t.Helper()
	run := model.CompilationRun{
		ID: id, TenantID: tenant, DatasetID: dataset, Kind: kind, State: state,
		IdempotencyKey: "key-" + id, ManifestHash: "m", TemplateHash: "t", PipelineHash: "p",
		DocumentIDs: "[]", ManifestComplete: false,
		SourceConsistency: model.CompilationSourceConsistencyUnverified,
		CancelScope:       model.CompilationCancelScopeXSchedulingOnly,
		RequestedBy:       "tester", Detail: "{}",
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := gdb.Create(&run).Error; err != nil {
		t.Fatalf("seed run %s: %v", id, err)
	}
}

func TestMigrationV147CompilationRunActiveInvariant(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "run-invariant.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	// Migrate to the pre-invariant version and seed a violating group.
	if err := MigrateTo(gdb, 146); err != nil {
		t.Fatalf("migrate to 146: %v", err)
	}
	base := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	tenant, dataset := "tenant-inv", "dataset-inv"
	seedCompilationRun(t, gdb, "run-old", tenant, dataset, model.CompilationKindWiki, model.CompilationRunAccepted, base)
	seedCompilationRun(t, gdb, "run-new", tenant, dataset, model.CompilationKindWiki, model.CompilationRunAccepted, base.Add(time.Minute))
	seedCompilationRun(t, gdb, "run-third", tenant, dataset, model.CompilationKindWiki, model.CompilationRunUnknown, base.Add(2*time.Minute))
	// Non-violating rows that must stay untouched.
	seedCompilationRun(t, gdb, "run-other-kind", tenant, dataset, model.CompilationKindPageIndex, model.CompilationRunAccepted, base)
	seedCompilationRun(t, gdb, "run-other-dataset", tenant, "dataset-else", model.CompilationKindWiki, model.CompilationRunDispatched, base)
	seedCompilationRun(t, gdb, "run-terminal", tenant, dataset, model.CompilationKindWiki, model.CompilationRunConfirmed, base)

	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}

	var old, newer, third, otherKind, otherDataset, terminal model.CompilationRun
	for _, tc := range []struct {
		id   string
		into *model.CompilationRun
	}{{"run-old", &old}, {"run-new", &newer}, {"run-third", &third}, {"run-other-kind", &otherKind},
		{"run-other-dataset", &otherDataset}, {"run-terminal", &terminal}} {
		if err := gdb.Where("id = ?", tc.id).First(tc.into).Error; err != nil {
			t.Fatalf("load %s: %v", tc.id, err)
		}
	}
	if old.State != model.CompilationRunAccepted {
		t.Fatalf("oldest active run must be retained, got %q", old.State)
	}
	for _, tc := range []struct {
		run  *model.CompilationRun
		name string
	}{{&newer, "run-new"}, {&third, "run-third"}} {
		if tc.run.State != model.CompilationRunCancelled {
			t.Fatalf("%s must be cancelled by backfill, got %q", tc.name, tc.run.State)
		}
		if tc.run.CancelScope != model.CompilationCancelScopeInvariantBackfill {
			t.Fatalf("%s cancel_scope must be invariant_backfill, got %q", tc.name, tc.run.CancelScope)
		}
		if tc.run.FailureCode != "superseded_active_invariant" {
			t.Fatalf("%s failure_code mismatch: %q", tc.name, tc.run.FailureCode)
		}
	}
	if otherKind.State != model.CompilationRunAccepted || otherDataset.State != model.CompilationRunDispatched ||
		terminal.State != model.CompilationRunConfirmed {
		t.Fatalf("non-violating rows must stay untouched: %s/%s/%s",
			otherKind.State, otherDataset.State, terminal.State)
	}

	// The unique index must now block a second active run for the same group.
	if err := Migrate(gdb); err != nil {
		t.Fatalf("migrate must be idempotent: %v", err)
	}
	seedCompilationRun(t, gdb, "run-violating", tenant, dataset, model.CompilationKindTree, model.CompilationRunAccepted, base)
	if err := gdb.Create(&model.CompilationRun{
		ID: "run-violating-2", TenantID: tenant, DatasetID: dataset, Kind: model.CompilationKindTree,
		State: model.CompilationRunAccepted, IdempotencyKey: "key-violating-2",
		ManifestHash: "m", TemplateHash: "t", PipelineHash: "p", DocumentIDs: "[]",
		RequestedBy: "tester", Detail: "{}", CreatedAt: base, UpdatedAt: base,
	}).Error; err == nil {
		t.Fatal("second active run for the same (tenant, dataset, kind) must violate the unique index")
	}
	// Terminal rows never collide with the active invariant.
	if err := gdb.Create(&model.CompilationRun{
		ID: "run-terminal-ok", TenantID: tenant, DatasetID: dataset, Kind: model.CompilationKindPageIndex,
		State: model.CompilationRunFailed, IdempotencyKey: "key-terminal-ok",
		ManifestHash: "m", TemplateHash: "t", PipelineHash: "p", DocumentIDs: "[]",
		RequestedBy: "tester", Detail: "{}", CreatedAt: base, UpdatedAt: base,
	}).Error; err != nil {
		t.Fatalf("terminal run insert must succeed: %v", err)
	}
}
