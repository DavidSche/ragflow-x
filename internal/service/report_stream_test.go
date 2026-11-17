package service

// Job snapshot and event stream (doc/288 §8.3 / T5).
//
// What is pinned here is the pair of rules that make a reconnect honest:
//
//   - the cursor a client resumes from is `rgx_report_job.event_seq`, the same row
//     the allocator updates, so nothing committed between the snapshot and the
//     subscription can be skipped;
//   - a cursor the retention pass has overtaken is refused (44115 with `min_seq`),
//     never answered with the surviving suffix, because a suffix looks exactly like
//     a complete replay.
//
// The stream framing itself is a handler concern and is tested there; this file
// tests what must hold with no socket in the picture, plus the A29 concurrency
// property the sequence depends on.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// reportJobEventCodes reports the code an error carries, 0 when it carries none.
func reportJobEventCode(err error) int {
	var he *httperr.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

// TestReportJobSnapshotCarriesResumeCursor is the "无缺口恢复" half of §8.3: the
// snapshot's `event_seq` is the last allocated seq, the terminal flag is derived
// from the row rather than guessed, and the sections come from the revision the job
// targets.
func TestReportJobSnapshotCarriesResumeCursor(t *testing.T) {
	fx, _ := newReportRunFixture(t, "stream-snapshot")
	created := fx.outlineJob(t, "link-policy-stream-snapshot", "req-stream-snapshot-0001")
	fx.runSlice(t, created.Job.ID)

	job := fx.reportJob(t, created.Job.ID)
	snapshot, err := fx.svc.GetReportJobSnapshot(context.Background(), fx.owner.ID, fx.tenantID, job.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	events, err := fx.svc.Store.ListReportEvents(context.Background(), fx.tenantID, job.ID, 0, 200)
	if err != nil || len(events) == 0 {
		t.Fatalf("the creation path must write events: %v (%d)", err, len(events))
	}
	last := events[len(events)-1].Seq
	if snapshot.EventSeq != last || snapshot.EventSeq != job.EventSeq {
		t.Fatalf("cursor must be the allocator's number: snapshot=%d last_event=%d job=%d",
			snapshot.EventSeq, last, job.EventSeq)
	}
	if snapshot.Terminal {
		t.Fatalf("awaiting_outline is not terminal: %+v", snapshot)
	}
	if snapshot.Status != model.ReportJobAwaitingOutline || snapshot.Stage != job.Stage {
		t.Fatalf("snapshot must carry the live status/stage, got %q/%q", snapshot.Status, snapshot.Stage)
	}
	if snapshot.Revision.ID != job.RevisionID || snapshot.Revision.Revision != 1 {
		t.Fatalf("snapshot must address the job's revision, got %+v", snapshot.Revision)
	}
	if snapshot.Revision.ExecutionSnapshotHash == "" {
		t.Fatal("the outline step froze a snapshot; the read side must show its hash")
	}
	if len(snapshot.Sections) == 0 {
		t.Fatal("the snapshot must list the sections the reviewer is looking at")
	}
	for _, section := range snapshot.Sections {
		// Content is deliberately absent: this is progress, not the document.
		if section.SectionKey == "" || section.Status == "" {
			t.Fatalf("section snapshot is incomplete: %+v", section)
		}
	}
	if snapshot.Gaps == nil {
		t.Fatal("an empty gap list must be [] and never null")
	}
	if snapshot.Tokens.Envelope != job.ReservedTokens {
		t.Fatalf("the envelope is the creation-time reservation: %d vs %d",
			snapshot.Tokens.Envelope, job.ReservedTokens)
	}
}

// TestReportJobSnapshotIsOwnerScoped pins the 44109 answer for a colleague's job id
// in the same workspace and for a job in another workspace. A job id travels in a
// URL, so it must not disclose whether a foreign job exists.
func TestReportJobSnapshotIsOwnerScoped(t *testing.T) {
	fx, _ := newReportRunFixture(t, "stream-owner")
	created := fx.createReport(t, "link-policy-stream-owner", "req-stream-owner-0001")

	colleague, err := fx.svc.CreateUser(context.Background(), fx.tenantID, "", CreateUserRequest{
		Username: "report-colleague-stream-owner", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.GetReportJobSnapshot(context.Background(), colleague.ID, fx.tenantID, created.Job.ID); err == nil {
		t.Fatal("a colleague must not read another user's job snapshot")
	} else if code := reportJobEventCode(err); code != 44109 {
		t.Fatalf("a foreign job must answer 44109, got %d (%v)", code, err)
	}

	other, err := fx.svc.CreateTenant(context.Background(), "报告另一工作区 stream-owner")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := fx.svc.CreateUser(context.Background(), other.ID, "", CreateUserRequest{
		Username: "report-outsider-stream-owner", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.GetReportJobSnapshot(context.Background(), outsider.ID, other.ID, created.Job.ID); err == nil {
		t.Fatal("another workspace must not read this job")
	} else if code := reportJobEventCode(err); code != 44109 {
		t.Fatalf("a cross-workspace job must answer 44109, got %d (%v)", code, err)
	}
}

// TestReportEventCursorNeverSkipsAHole is A42: a cursor below the surviving range
// is refused with the numbers needed to recover, while the two cursors that do not
// claim continuity (0 = full replay, event_seq = nothing new) are never refused.
func TestReportEventCursorNeverSkipsAHole(t *testing.T) {
	fx, _ := newReportRunFixture(t, "stream-cursor")
	created := fx.outlineJob(t, "link-policy-stream-cursor", "req-stream-cursor-0001")
	fx.runSlice(t, created.Job.ID)
	ctx := context.Background()
	job := fx.reportJob(t, created.Job.ID)

	events, err := fx.svc.Store.ListReportEvents(ctx, fx.tenantID, job.ID, 0, 200)
	if err != nil || len(events) < 2 {
		t.Fatalf("need at least two events to cut a hole: %v (%d)", err, len(events))
	}
	// Simulate the janitor: keep only the last event.
	cut := events[len(events)-1].Seq - 1
	if err := fx.gdb.Where("job_id = ? AND seq <= ?", job.ID, cut).
		Delete(&model.ReportJobEvent{}).Error; err != nil {
		t.Fatal(err)
	}

	stream, err := fx.svc.OpenReportJobEventStream(ctx, fx.owner.ID, fx.tenantID, job.ID, 1)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if stream.Reset == nil {
		t.Fatal("a cursor behind the surviving range must be refused")
	}
	if stream.Reset.MinSeq != cut+1 || stream.Reset.EventSeq != job.EventSeq {
		t.Fatalf("reset must hand back min_seq/event_seq, got %+v", stream.Reset)
	}

	// Resuming from the oldest surviving event is continuous: no refusal.
	if stream, err = fx.svc.OpenReportJobEventStream(ctx, fx.owner.ID, fx.tenantID, job.ID, cut+1); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if stream.Reset != nil {
		t.Fatalf("a cursor that is still present must not be refused: %+v", stream.Reset)
	}
	// A full replay never claims continuity, so a hole cannot mislead it.
	if stream, err = fx.svc.OpenReportJobEventStream(ctx, fx.owner.ID, fx.tenantID, job.ID, 0); err != nil {
		t.Fatalf("reopen at 0: %v", err)
	}
	if stream.Reset != nil {
		t.Fatalf("after=0 is a full replay and must not be refused: %+v", stream.Reset)
	}
	// Nothing new: also not a hole.
	if stream, err = fx.svc.OpenReportJobEventStream(ctx, fx.owner.ID, fx.tenantID, job.ID, job.EventSeq); err != nil {
		t.Fatalf("reopen at event_seq: %v", err)
	}
	if stream.Reset != nil {
		t.Fatalf("after=event_seq has nothing to read and must not be refused: %+v", stream.Reset)
	}
}

// TestReportEventCursorSurvivesFullPurge covers the degenerate case: every event was
// cleared while the job still advertises a sequence. The stream must still refuse,
// and the earliest resumable seq is the allocation itself.
func TestReportEventCursorSurvivesFullPurge(t *testing.T) {
	fx, _ := newReportRunFixture(t, "stream-purge")
	created := fx.outlineJob(t, "link-policy-stream-purge", "req-stream-purge-0001")
	fx.runSlice(t, created.Job.ID)
	ctx := context.Background()
	job := fx.reportJob(t, created.Job.ID)

	if err := fx.gdb.Where("job_id = ?", job.ID).Delete(&model.ReportJobEvent{}).Error; err != nil {
		t.Fatal(err)
	}
	stream, err := fx.svc.OpenReportJobEventStream(ctx, fx.owner.ID, fx.tenantID, job.ID, 1)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if stream.Reset == nil {
		t.Fatal("a purged stream must not look resumable")
	}
	if stream.Reset.MinSeq != job.EventSeq || stream.Reset.EventSeq != job.EventSeq {
		t.Fatalf("a purged stream resumes from the allocation, got %+v", stream.Reset)
	}
}

// TestReportEventReadStopsAtPageSize keeps one poll bounded: the handler loops, so an
// unbounded page would be a memory spike per tick for a job with a long backlog.
func TestReportEventReadStopsAtPageSize(t *testing.T) {
	fx, _ := newReportRunFixture(t, "stream-pagesize")
	created := fx.createReport(t, "link-policy-stream-pagesize", "req-stream-pagesize-0001")
	ctx := context.Background()

	// One insert per event: the fixture writes through the repository so the
	// sequence is allocated the way production allocates it.
	total := reportEventPageSize + 5
	for i := 0; i < total; i++ {
		event := &model.ReportJobEvent{
			TenantID: fx.tenantID, JobID: created.Job.ID, Type: reportEventJobProgress,
			PayloadJSON: fmt.Sprintf(`{"progress_done":%d}`, i),
			CreatedAt:   time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
		}
		if _, err := fx.svc.Store.AppendReportEvent(ctx, event); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}
	page, err := fx.svc.ReadReportJobEvents(ctx, fx.tenantID, created.Job.ID, 0)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if len(page) != reportEventPageSize {
		t.Fatalf("one poll must be capped at %d events, got %d", reportEventPageSize, len(page))
	}
	for i := 1; i < len(page); i++ {
		if page[i].Seq <= page[i-1].Seq {
			t.Fatalf("a page must ascend by seq: %d then %d", page[i-1].Seq, page[i].Seq)
		}
	}
	// The next poll continues exactly where the first stopped.
	next, err := fx.svc.ReadReportJobEvents(ctx, fx.tenantID, created.Job.ID, page[len(page)-1].Seq)
	if err != nil {
		t.Fatalf("read next page: %v", err)
	}
	if len(next) == 0 || next[0].Seq != page[len(page)-1].Seq+1 {
		t.Fatalf("the next page must resume at the following seq, got %d", next[0].Seq)
	}
}

// TestAppendReportEventAllocatesUniqueSeq is A29's cheap half: concurrent writers on
// one job must produce a dense, duplicate-free sequence, and the job's `event_seq`
// must end up equal to the number of rows. This is the property the SSE cursor is
// built on — a duplicate seq would make a client drop one of two different events,
// a hole would make `after=` silently skip one.
func TestAppendReportEventAllocatesUniqueSeq(t *testing.T) {
	fx, _ := newReportRunFixture(t, "stream-a29")
	created := fx.createReport(t, "link-policy-stream-a29", "req-stream-a29-0001")
	ctx := context.Background()

	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				event := &model.ReportJobEvent{
					TenantID: fx.tenantID, JobID: created.Job.ID, Type: reportEventJobProgress,
					PayloadJSON: fmt.Sprintf(`{"writer":%d,"i":%d}`, w, i),
					CreatedAt:   time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
				}
				if _, err := fx.svc.Store.AppendReportEvent(ctx, event); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent append failed: %v", err)
	}

	// The repository clamps one page to `maxPageSize` (200), so A29 reads the stream
	// in pages rather than asking for "everything" and silently getting less.
	var events []model.ReportJobEvent
	cursor := int64(0)
	for {
		page, err := fx.svc.ReadReportJobEvents(ctx, fx.tenantID, created.Job.ID, cursor)
		if err != nil {
			t.Fatalf("read events after %d: %v", cursor, err)
		}
		if len(page) == 0 {
			break
		}
		events = append(events, page...)
		cursor = page[len(page)-1].Seq
	}
	// The creation path wrote one event (job.queued) before this test's writers.
	expected := writers*perWriter + 1
	if len(events) != expected {
		t.Fatalf("expected %d events, got %d", expected, len(events))
	}
	seen := make(map[int64]bool, len(events))
	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("seq must be dense and ascending: position %d has seq %d", i, event.Seq)
		}
		if seen[event.Seq] {
			t.Fatalf("duplicate seq %d", event.Seq)
		}
		seen[event.Seq] = true
	}
	job := fx.reportJob(t, created.Job.ID)
	if job.EventSeq != int64(expected) {
		t.Fatalf("event_seq must equal the row count: %d vs %d", job.EventSeq, expected)
	}
}
