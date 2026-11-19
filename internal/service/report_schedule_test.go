package service

// Recurring reports (doc/295 §2.4 / R20). A60–A62.
//
// The three assertions are deliberately about *time*, not about shapes: a schedule's
// only observable behaviour is "which instant does it fire at", and every bug in a
// scheduler eventually shows up as a plan that fires twice, never, or at the wrong
// wall-clock time.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// A60: `next_run_at` is the first occurrence strictly after `from`, in the plan's
// wall clock, and repeated advancement never revisits an instant.
func TestReportScheduleNextRunAtIsStrictlyForwardAndLocal(t *testing.T) {
	// 2026-03-02 is a Monday. 09:00 at UTC+8 = 01:00 UTC.
	mondayNine := time.Date(2026, 3, 2, 1, 0, 0, 0, time.UTC)

	t.Run("daily advances one day and keeps the local clock", func(t *testing.T) {
		plan := reportSchedulePlan{
			Cadence: model.ReportScheduleCadenceDaily, Hour: 9, Minute: 0, TZOffsetMin: 480,
		}
		next := reportScheduleNextRunAt(plan, mondayNine)
		want := mondayNine.Add(24 * time.Hour)
		if !next.Equal(want) {
			t.Fatalf("daily from an exact occurrence must move to tomorrow: got %v want %v", next, want)
		}
		// The stored instant is UTC with no location: the offset is applied and then
		// removed, so a reader never has to know which zone the column is in.
		if next.Location() != time.UTC {
			t.Fatalf("next_run_at must be a UTC instant, got location %v", next.Location())
		}
		if local := next.Add(480 * time.Minute); local.Hour() != 9 || local.Minute() != 0 {
			t.Fatalf("local wall clock must stay 09:00, got %02d:%02d", local.Hour(), local.Minute())
		}
	})

	t.Run("from an instant between occurrences finds the same day", func(t *testing.T) {
		plan := reportSchedulePlan{
			Cadence: model.ReportScheduleCadenceDaily, Hour: 9, Minute: 0, TZOffsetMin: 480,
		}
		// 00:30 UTC = 08:30 local, still before 09:00 that day.
		from := time.Date(2026, 3, 2, 0, 30, 0, 0, time.UTC)
		if next := reportScheduleNextRunAt(plan, from); !next.Equal(mondayNine) {
			t.Fatalf("must fire at 09:00 the same local day: got %v want %v", next, mondayNine)
		}
	})

	t.Run("a negative offset shifts the date", func(t *testing.T) {
		// 09:00 at UTC-5 = 14:00 UTC.
		plan := reportSchedulePlan{
			Cadence: model.ReportScheduleCadenceDaily, Hour: 9, Minute: 0, TZOffsetMin: -300,
		}
		next := reportScheduleNextRunAt(plan, mondayNine)
		want := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("UTC-5 09:00 must be 14:00Z: got %v want %v", next, want)
		}
	})

	t.Run("weekly lands on the named weekday", func(t *testing.T) {
		// Wednesday 08:00, offset 0.
		plan := reportSchedulePlan{
			Cadence: model.ReportScheduleCadenceWeekly, Hour: 8, Minute: 0,
			Weekday: 3, TZOffsetMin: 0,
		}
		next := reportScheduleNextRunAt(plan, mondayNine)
		if next.Weekday() != time.Wednesday {
			t.Fatalf("weekly plan must land on Wednesday, got %v", next.Weekday())
		}
		if want := time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC); !next.Equal(want) {
			t.Fatalf("got %v want %v", next, want)
		}
	})

	t.Run("advancing an already advanced row keeps moving", func(t *testing.T) {
		// The claim writes `next_run_at` forward *before* the run, so the function has
		// to be stable when fed its own output; a fixed point would retrigger forever.
		row := &model.ReportSchedule{
			Cadence: model.ReportScheduleCadenceDaily, Hour: 9, TZOffsetMin: 480,
		}
		seen := map[time.Time]struct{}{}
		cursor := mondayNine
		for i := 0; i < 5; i++ {
			cursor = reportScheduleAdvance(row, cursor)
			if _, ok := seen[cursor]; ok {
				t.Fatalf("advance revisited %v at step %d", cursor, i)
			}
			seen[cursor] = struct{}{}
			if cursor.Location() != time.UTC {
				t.Fatalf("advance must stay in UTC, got %v", cursor.Location())
			}
		}
	})
}

// A61: the plan is per-report, owned by the report's owner, and creating it is a
// 201 while changing it is a 200. Cross-user access is the same 404 as "no such
// report" — a colleague must not learn that a report they cannot see has a timer.
func TestReportScheduleIsPerReportAndOwnerOnly(t *testing.T) {
	fx := newReportFixture(t, "schedule")
	ctx := context.Background()
	datasetID := "link-policy-schedule"
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-sched-0001",
		reportCreateRequest(datasetID))
	if err != nil {
		t.Fatal(err)
	}
	reportID := created.Report.ID

	hour := 9
	request := ReportScheduleRequest{
		Cadence: model.ReportScheduleCadenceWeekly, Hour: &hour, TZOffsetMin: intPtr(480),
		UpdateMode: model.ReportScheduleUpdateReuseStatic,
	}
	view, isNew, err := fx.svc.UpsertReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID, request)
	if err != nil {
		t.Fatalf("arm schedule: %v", err)
	}
	if !isNew {
		t.Fatal("first arming must report created=true")
	}
	if view.ReportID != reportID || view.Cadence != model.ReportScheduleCadenceWeekly {
		t.Fatalf("schedule view wrong: %+v", view)
	}
	if !view.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("next_run_at must be in the future, got %v", view.NextRunAt)
	}
	firstNext := view.NextRunAt

	// Same report, second write: an edit, not a new plan. The unique index on
	// report_id is what makes "one plan per report" true; the flag is what tells the
	// client which of the two it got.
	hour = 10
	request.Hour = &hour
	view2, isNew2, err := fx.svc.UpsertReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID, request)
	if err != nil {
		t.Fatal(err)
	}
	if isNew2 {
		t.Fatal("second write must report created=false")
	}
	if view2.Hour != 10 {
		t.Fatalf("edit must land, got hour %d", view2.Hour)
	}
	if view2.NextRunAt == firstNext {
		// A different wall-clock time on the same day is a different instant; if the
		// recompute were skipped the plan would keep firing at the old hour.
		t.Fatalf("an edit must recompute next_run_at (still %v)", view2.NextRunAt)
	}

	// Validation: the closed sets and range limits are 400/44105, not silent clamps.
	for name, bad := range map[string]ReportScheduleRequest{
		"cadence":   {Cadence: "monthly"},
		"update":    {UpdateMode: "always"},
		"hour":      {Hour: intPtr(24)},
		"minute":    {Minute: intPtr(-1)},
		"weekday":   {Weekday: intPtr(7)},
		"tz offset": {TZOffsetMin: intPtr(841)},
	} {
		if _, _, err := fx.svc.UpsertReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID, bad); err == nil {
			t.Fatalf("%s: invalid plan must be refused", name)
		}
	}

	// A colleague in the same workspace: RBAC allows `execute:report`, ownership
	// does not. Answer must be 44109/404, indistinguishable from a missing report.
	colleague, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-colleague-schedule", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = fx.svc.UpsertReportSchedule(ctx, colleague.ID, fx.tenantID, reportID, request)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Status != 404 || httpErr.Code != codeReportAccessDenied {
		t.Fatalf("cross-user arming must be 404/%d, got %v", codeReportAccessDenied, err)
	}
	if _, err := fx.svc.DeleteReportSchedule(ctx, colleague.ID, fx.tenantID, reportID); err == nil {
		t.Fatal("cross-user delete must be refused")
	}
	// A missing id answers the same 404/44109 as a report in someone else's
	// workspace, so the read path is not an existence oracle either.
	missing, err := fx.svc.GetReportSchedule(ctx, colleague.ID, fx.tenantID, "rep-does-not-exist")
	if missing != nil {
		t.Fatalf("a missing report has no plan, got %+v", missing)
	}
	if !errors.As(err, &httpErr) || httpErr.Status != 404 || httpErr.Code != codeReportAccessDenied {
		t.Fatalf("missing report must be 404/%d, got %v", codeReportAccessDenied, err)
	}

	// Deleting twice: the second call wrote nothing, and neither call errors.
	if deleted, err := fx.svc.DeleteReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID); err != nil || !deleted {
		t.Fatalf("first delete must remove the plan: %v / %v", deleted, err)
	}
	if deleted, err := fx.svc.DeleteReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID); err != nil || deleted {
		t.Fatalf("second delete must be a no-op: %v / %v", deleted, err)
	}
	if view, err := fx.svc.GetReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID); err != nil || view != nil {
		t.Fatalf("plan must be gone: %v / %v", view, err)
	}
}

// A62: a tick claims each due plan exactly once. The claim advances `next_run_at`
// before any work is attempted, so a crash (here: a second tick immediately after
// the first) does not refire the same occurrence.
func TestReportScheduleTickClaimsOnceAndAdvances(t *testing.T) {
	fx := newReportFixture(t, "tick")
	ctx := context.Background()
	datasetID := "link-policy-tick"
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-tick-0001",
		reportCreateRequest(datasetID))
	if err != nil {
		t.Fatal(err)
	}
	reportID := created.Report.ID
	if _, _, err := fx.svc.UpsertReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID,
		ReportScheduleRequest{Cadence: model.ReportScheduleCadenceDaily}); err != nil {
		t.Fatal(err)
	}

	// Backdate the plan so it is due now. This is the one write the service never
	// performs (it always arms forward); a test needs it to reach the due branch.
	now := time.Now().UTC()
	if err := fx.gdb.Model(&model.ReportSchedule{}).
		Where("tenant_id = ? AND report_id = ?", fx.tenantID, reportID).
		Update("next_run_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}

	before, err := fx.svc.Store.GetReportSchedule(ctx, fx.tenantID, reportID)
	if err != nil || before == nil {
		t.Fatalf("plan must exist: %v", err)
	}

	summary, err := fx.svc.RunReportScheduleTick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if summary.Claimed != 1 {
		t.Fatalf("one due plan must be claimed once, got %d", summary.Claimed)
	}

	after, err := fx.svc.Store.GetReportSchedule(ctx, fx.tenantID, reportID)
	if err != nil || after == nil {
		t.Fatalf("reload plan: %v", err)
	}
	if !after.NextRunAt.After(now) {
		t.Fatalf("a claim must advance next_run_at past now: got %v", after.NextRunAt)
	}
	if !after.UpdatedAt.After(before.UpdatedAt.Add(-time.Second)) {
		t.Fatalf("claim must touch the row clock")
	}

	// A second tick in the same second must claim nothing: the occurrence was spent
	// even though the run it triggered may still be in flight.
	second, err := fx.svc.RunReportScheduleTick(ctx)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if second.Claimed != 0 {
		t.Fatalf("the same occurrence must not be claimed twice, got %d", second.Claimed)
	}
	final, err := fx.svc.Store.GetReportSchedule(ctx, fx.tenantID, reportID)
	if err != nil || final == nil {
		t.Fatal(err)
	}
	if !final.NextRunAt.Equal(after.NextRunAt) {
		t.Fatalf("a no-op tick must not move next_run_at: %v -> %v", after.NextRunAt, final.NextRunAt)
	}

	// A disabled plan is not claimed at all, even when it is overdue: "enabled" is
	// part of the due predicate, not a post-claim check.
	if err := fx.gdb.Model(&model.ReportSchedule{}).
		Where("tenant_id = ? AND report_id = ?", fx.tenantID, reportID).
		Updates(map[string]interface{}{
			"enabled": false, "next_run_at": now.Add(-2 * time.Minute),
		}).Error; err != nil {
		t.Fatal(err)
	}
	third, err := fx.svc.RunReportScheduleTick(ctx)
	if err != nil {
		t.Fatalf("third tick: %v", err)
	}
	if third.Claimed != 0 {
		t.Fatalf("a disabled plan must never be claimed, got %d", third.Claimed)
	}
}

// A62b: with no published base revision the run has nothing to update from, so the
// tick records `skipped_no_base` instead of failing. This is the shape a freshly
// created report has: the plan can be armed before the first version is published.
func TestReportScheduleTickSkipsWithoutPublishedBase(t *testing.T) {
	fx := newReportFixture(t, "tick-skip")
	ctx := context.Background()
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-tick-0002",
		reportCreateRequest("link-policy-tick-skip"))
	if err != nil {
		t.Fatal(err)
	}
	reportID := created.Report.ID
	if _, _, err := fx.svc.UpsertReportSchedule(ctx, fx.owner.ID, fx.tenantID, reportID,
		ReportScheduleRequest{Cadence: model.ReportScheduleCadenceDaily}); err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ReportSchedule{}).
		Where("tenant_id = ? AND report_id = ?", fx.tenantID, reportID).
		Update("next_run_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.RunReportScheduleTick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	row, err := fx.svc.Store.GetReportSchedule(ctx, fx.tenantID, reportID)
	if err != nil || row == nil {
		t.Fatal(err)
	}
	if row.LastStatus != model.ReportScheduleStatusSkippedNoBase {
		t.Fatalf("unpublished base must record %q, got %q (error=%q)",
			model.ReportScheduleStatusSkippedNoBase, row.LastStatus, row.LastError)
	}
	if row.LastError != "" {
		t.Fatalf("a skip is not an error; last_error must stay empty, got %q", row.LastError)
	}
	if row.LastRevision != 0 {
		t.Fatalf("a skip mints no revision, got %d", row.LastRevision)
	}
}
