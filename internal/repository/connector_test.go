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

func newConnectorRepoStore(t *testing.T) Store {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "connector-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	s := NewStore(gdb)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func connectorFixture(tenantID, name string) *model.Connector {
	return &model.Connector{
		ID:                 id.New(),
		TenantID:           tenantID,
		Name:               name,
		Provider:           model.ConnectorProviderLocalFolder,
		AdapterKind:        model.ConnectorAdapterNative,
		SourceOwnershipKey: "src:test:" + id.New(),
		TargetDatasetID:    "ds-1",
		SpecJSON:           `{"root":"/tmp/src"}`,
		CursorJSON:         "{}",
		ScheduleKind:       model.ConnectorScheduleInterval,
		ScheduleValue:      5,
		Enabled:            true,
		Status:             model.ConnectorStatusIdle,
		CreatedBy:          "u-creator",
	}
}

func TestConnectorCRUDSoftDeleteAndCursor(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	c := connectorFixture("tenant-a", "local")
	if err := s.CreateConnector(ctx, c); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetConnector(ctx, "tenant-a", c.ID)
	if err != nil || got == nil || got.Name != "local" {
		t.Fatalf("GetConnector = %v, %v", got, err)
	}
	if !got.Enabled || got.CreatedBy != "u-creator" {
		t.Fatalf("audit fields = enabled:%v created_by:%q", got.Enabled, got.CreatedBy)
	}

	// cursor + last status persist
	c1 := model.ConnectorStatusIdle
	ok, err := s.SetConnectorCursor(ctx, "tenant-a", c.ID, `{"a.txt":"1-123"}`, &got.UpdatedAt, c1, model.ConnectorStatusIdle)
	if err != nil || !ok {
		t.Fatalf("SetConnectorCursor ok=%v err=%v", ok, err)
	}
	got2, _ := s.GetConnector(ctx, "tenant-a", c.ID)
	if got2.CursorJSON != `{"a.txt":"1-123"}` {
		t.Fatalf("cursor = %q", got2.CursorJSON)
	}

	// disable → schedulable list excludes it
	if _, err := s.SetConnectorEnabled(ctx, "tenant-a", c.ID, true); err != nil {
		t.Fatal(err)
	}
	if disabled, _ := s.GetConnector(ctx, "tenant-a", c.ID); disabled.Enabled || disabled.Status != model.ConnectorStatusDisabled {
		t.Fatalf("disable must flip enabled/status, got enabled=%v status=%q", disabled.Enabled, disabled.Status)
	}
	sched, err := s.ListSchedulableConnectors(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range sched {
		if r.ID == c.ID {
			t.Fatalf("disabled connector still schedulable")
		}
	}
	// disable then re-enable
	if _, err := s.SetConnectorEnabled(ctx, "tenant-a", c.ID, false); err != nil {
		t.Fatal(err)
	}

	// soft delete
	deleted, err := s.DeleteConnector(ctx, "tenant-a", c.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteConnector = %v, %v", deleted, err)
	}
	after, _ := s.GetConnector(ctx, "tenant-a", c.ID)
	if after != nil {
		t.Fatalf("connector visible after soft delete: %+v", after)
	}
	// second delete reports not-found
	deleted2, _ := s.DeleteConnector(ctx, "tenant-a", c.ID)
	if deleted2 {
		t.Fatalf("second delete of soft-deleted connector reported ok")
	}
}

func TestConnectorSourceOwnershipUniqueAndReleasedOnDelete(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	key := "src:v1:test-ownership"
	a := connectorFixture("tenant-a", "source-a")
	a.SourceOwnershipKey = key
	if err := s.CreateConnector(ctx, a); err != nil {
		t.Fatal(err)
	}
	dup := connectorFixture("tenant-a", "source-a-copy")
	dup.SourceOwnershipKey = key
	if err := s.CreateConnector(ctx, dup); err == nil {
		t.Fatal("duplicate tenant+source ownership key must be rejected")
	}
	// The same physical source may be bound once per tenant.
	otherTenant := connectorFixture("tenant-b", "source-b")
	otherTenant.SourceOwnershipKey = key
	if err := s.CreateConnector(ctx, otherTenant); err != nil {
		t.Fatalf("tenant isolation should allow the same key: %v", err)
	}

	if deleted, err := s.DeleteConnector(ctx, "tenant-a", a.ID); err != nil || !deleted {
		t.Fatalf("DeleteConnector = %v, %v", deleted, err)
	}
	// The soft-deleted row keeps audit history but releases the unique key.
	var old model.Connector
	if err := s.(*store).Unscoped().First(&old, "id = ?", a.ID).Error; err != nil {
		t.Fatal(err)
	}
	if old.SourceOwnershipKey == key {
		t.Fatal("soft delete must release the source ownership key")
	}
	recreated := connectorFixture("tenant-a", "source-a-recreated")
	recreated.SourceOwnershipKey = key
	if err := s.CreateConnector(ctx, recreated); err != nil {
		t.Fatalf("recreate after delete = %v", err)
	}
}

func TestConnectorSchedulableRequiresEnabled(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	c := connectorFixture("tenant-a", "disabled-but-idle")
	c.Enabled = false
	c.Status = model.ConnectorStatusIdle
	if err := s.CreateConnector(ctx, c); err != nil {
		t.Fatal(err)
	}
	assertSchedulable := func(want bool) {
		t.Helper()
		rows, err := s.ListSchedulableConnectors(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == c.ID {
				if !want {
					t.Fatal("connector with enabled=false is still schedulable")
				}
				return
			}
		}
		if want {
			t.Fatal("enabled connector is missing from schedulable list")
		}
	}
	assertSchedulable(false)
	if _, err := s.UpdateConnector(ctx, func() *model.Connector {
		c.Enabled = true
		return c
	}()); err != nil {
		t.Fatal(err)
	}
	assertSchedulable(true)
}

func TestConnectorScheduleRunAfter(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

	// manual → zero
	m := connectorFixture("t", "m")
	m.ScheduleKind = model.ConnectorScheduleManual
	got, err := s.ConnectorScheduleRunAfter(ctx, m, now)
	if err != nil || !got.IsZero() {
		t.Fatalf("manual runAfter = %v, %v", got, err)
	}

	// interval value honored
	iv := connectorFixture("t", "iv")
	iv.ScheduleKind = model.ConnectorScheduleInterval
	iv.ScheduleValue = 5
	got, err = s.ConnectorScheduleRunAfter(ctx, iv, now)
	if err != nil || !got.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("interval 5m runAfter = %v, want %v", got, now.Add(5*time.Minute))
	}
	// interval sub-5 clamps to 30m (self-destruction guard)
	iv.ScheduleValue = 0
	got, _ = s.ConnectorScheduleRunAfter(ctx, iv, now)
	if !got.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("interval 0 runAfter = %v, want %v", got, now.Add(30*time.Minute))
	}

	// daily: minute-of-day later today
	d := connectorFixture("t", "d")
	d.ScheduleKind = model.ConnectorScheduleDaily
	d.ScheduleValue = 720 // 12:00 UTC
	got, _ = s.ConnectorScheduleRunAfter(ctx, d, now.Add(-3*time.Hour))
	want := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("daily later-today runAfter = %v, want %v", got, want)
	}
	// daily: minute already passed at 13:00 → tomorrow
	d2now := time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)
	got, _ = s.ConnectorScheduleRunAfter(ctx, d, d2now)
	want = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("daily next-day runAfter = %v, want %v", got, want)
	}
}

func TestConnectorRunLifecycleAndRetention(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	c := connectorFixture("tenant-a", "local")
	if err := s.CreateConnector(ctx, c); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-3 * time.Minute)
	run := &model.ConnectorRun{
		ID: id.New(), ConnectorID: c.ID, TenantID: c.TenantID,
		Trigger: model.ConnectorRunTriggerScheduled, TriggeredBy: "u-runner",
		Status: model.ConnectorRunStatusRunning, StartedAt: &started,
	}
	if err := s.CreateConnectorRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	active, err := s.ListActiveConnectorRuns(ctx, c.ID)
	if err != nil || active != 1 {
		t.Fatalf("active runs = %d, %v", active, err)
	}

	finished := time.Now().UTC().Add(-100 * 24 * time.Hour)
	ok, err := s.SetConnectorRunResult(ctx, run.ID, ConnectorRunOutcome{
		Status: model.ConnectorRunStatusSucceeded, StartedAt: &started, FinishedAt: &finished,
		Added: 3, Changed: 1, DetailJSON: "{}", TraceID: "trace-1",
	})
	if err != nil || !ok {
		t.Fatalf("SetConnectorRunResult ok=%v err=%v", ok, err)
	}
	active, _ = s.ListActiveConnectorRuns(ctx, c.ID)
	if active != 0 {
		t.Fatalf("active runs after result = %d", active)
	}

	// run history retained beyond connector delete
	deleted, _ := s.DeleteConnector(ctx, "tenant-a", c.ID)
	if !deleted {
		t.Fatal("expected connector delete ok")
	}
	runs, total, err := s.ListConnectorRunsForScope(ctx, false, []string{"tenant-a"}, c.ID, 1, 10)
	if err != nil || total != 1 || len(runs) != 1 {
		t.Fatalf("runs after connector delete = %d total=%d err=%v", len(runs), total, err)
	}
	if runs[0].TriggeredBy != "u-runner" {
		t.Fatalf("triggered_by = %q, want u-runner", runs[0].TriggeredBy)
	}

	// retention janitor removes only rows whose finished_at predates the cutoff
	_ = runs
	cutoff := time.Now().UTC().Add(-90 * 24 * time.Hour)
	counts, err := s.DeleteExpiredConnectorRuns(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	_, totalAfter, _ := s.ListConnectorRunsForScope(ctx, false, []string{"tenant-a"}, c.ID, 1, 100)
	if counts[model.ConnectorProviderLocalFolder] != 1 || totalAfter != 0 {
		t.Fatalf("retention purge by_provider=%v totalAfter=%d (want 1 local_folder, 0 left)", counts, totalAfter)
	}
}

func TestConnectorListForScopeCrossTenant(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	for _, tc := range []struct{ tenant, name string }{
		{"tenant-a", "a1"}, {"tenant-a", "a2"}, {"tenant-b", "b1"},
	} {
		if err := s.CreateConnector(ctx, connectorFixture(tc.tenant, tc.name)); err != nil {
			t.Fatal(err)
		}
	}
	list, total, err := s.ListConnectorsForScope(ctx, false, []string{"tenant-a"}, "", "", 1, 10)
	if err != nil || total != 2 {
		t.Fatalf("tenant-a list total=%d err=%v", total, err)
	}
	if len(list) != 2 {
		t.Fatalf("tenant-a rows=%d", len(list))
	}
	all, totalAll, err := s.ListConnectorsForScope(ctx, true, nil, "", "", 1, 10)
	if err != nil || totalAll != 3 || len(all) != 3 {
		t.Fatalf("scope-all total=%d rows=%d err=%v", totalAll, len(all), err)
	}
}

func TestConnectorListForScopeFilters(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	idle := connectorFixture("tenant-a", "idle")
	disabled := connectorFixture("tenant-a", "disabled")
	disabled.TargetDatasetID = "ds-2"
	disabled.Status = model.ConnectorStatusDisabled
	for _, c := range []*model.Connector{idle, disabled} {
		if err := s.CreateConnector(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	byStatus, totalStatus, err := s.ListConnectorsForScope(ctx, false, []string{"tenant-a"}, model.ConnectorStatusDisabled, "", 1, 10)
	if err != nil || totalStatus != 1 || len(byStatus) != 1 || byStatus[0].ID != disabled.ID {
		t.Fatalf("status filter total=%d rows=%d err=%v", totalStatus, len(byStatus), err)
	}
	byDataset, totalDataset, err := s.ListConnectorsForScope(ctx, false, []string{"tenant-a"}, "", "ds-2", 1, 10)
	if err != nil || totalDataset != 1 || len(byDataset) != 1 || byDataset[0].ID != disabled.ID {
		t.Fatalf("dataset filter total=%d rows=%d err=%v", totalDataset, len(byDataset), err)
	}
	both, totalBoth, err := s.ListConnectorsForScope(ctx, false, []string{"tenant-a"}, model.ConnectorStatusIdle, "ds-2", 1, 10)
	if err != nil || totalBoth != 0 || len(both) != 0 {
		t.Fatalf("combined filter total=%d rows=%d err=%v", totalBoth, len(both), err)
	}
	// Source-identity checks need every binding for the dataset, including
	// disabled rows (disable is not delete).
	dsRows, err := s.ListConnectorsForDataset(ctx, "tenant-a", "ds-2")
	if err != nil || len(dsRows) != 1 || dsRows[0].ID != disabled.ID {
		t.Fatalf("dataset bindings rows=%d err=%v", len(dsRows), err)
	}
	if rows, err := s.ListConnectorsForDataset(ctx, "tenant-b", "ds-2"); err != nil || len(rows) != 0 {
		t.Fatalf("dataset bindings leaked across tenants: rows=%d err=%v", len(rows), err)
	}
}

func TestConnectorRunStateKeepsCursor(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	c := connectorFixture("tenant-a", "cursor")
	if err := s.CreateConnector(ctx, c); err != nil {
		t.Fatal(err)
	}
	last := time.Now().UTC()
	if ok, err := s.SetConnectorCursor(ctx, "tenant-a", c.ID, `{"p":"1"}`, &last, model.ConnectorRunStatusRunning, model.ConnectorStatusIdle); err != nil || !ok {
		t.Fatalf("SetConnectorCursor ok=%v err=%v", ok, err)
	}
	// Terminal bookkeeping must not roll back the mid-run cursor (doc/225 F5).
	if ok, err := s.SetConnectorRunState(ctx, "tenant-a", c.ID, &last, model.ConnectorRunStatusFailed, model.ConnectorStatusDegraded); err != nil || !ok {
		t.Fatalf("SetConnectorRunState ok=%v err=%v", ok, err)
	}
	got, _ := s.GetConnector(ctx, "tenant-a", c.ID)
	if got == nil || got.CursorJSON != `{"p":"1"}` || got.Status != model.ConnectorStatusDegraded {
		t.Fatalf("after run state = %+v", got)
	}
}

func TestConnectorUpdatePersistsStatus(t *testing.T) {
	ctx := context.Background()
	s := newConnectorRepoStore(t)
	c := connectorFixture("tenant-a", "status")
	if err := s.CreateConnector(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.Status = model.ConnectorStatusDisabled
	if ok, err := s.UpdateConnector(ctx, c); err != nil || !ok {
		t.Fatalf("UpdateConnector ok=%v err=%v", ok, err)
	}
	got, _ := s.GetConnector(ctx, "tenant-a", c.ID)
	if got == nil || got.Status != model.ConnectorStatusDisabled {
		t.Fatalf("status = %+v", got)
	}
}
