package service

// Artifact version allocation and export dedupe (doc/288 §7.7 / A30a + A30b).
//
// The two assertions are deliberately kept apart because they pull in opposite
// directions, and an implementation can pass one while failing the other:
//
//   - A30a is about *allocation*: N different frozen contents of the same revision
//     must land on versions 1..N with no holes and no 500s, because a version number
//     is the reviewer's handle on "the file I sent".
//   - A30b is about *dedupe*: N simultaneous clicks for the same frozen content must
//     produce one artifact, not N. A retry of a slow request must not multiply files.
//
// The concurrency leg runs real goroutines against the same store on purpose. A
// sequential "call it twice" test would pass even if two writers could both take
// version 2 — the unique index and the conflict read-back are what is being tested.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportExportAllocatesOneVersionPerFrozenContent(t *testing.T) {
	fx := newReportFixture(t, "a30a")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	versions := make(map[int64]string, 10)
	for index := 0; index < 10; index++ {
		// Freeze a different body each round: the idempotency key includes the
		// canonical hash, so this is ten *different* requests for one revision.
		body := fmt.Sprintf("内容：background（第 %d 轮修订）", index+1)
		if err := fx.gdb.Model(&model.ReportSection{}).
			Where("tenant_id = ? AND revision_id = ? AND ord = 0", fx.tenantID, revision.ID).
			Updates(map[string]interface{}{
				"content_md": body, "content_hash": fmt.Sprintf("hash-background-%d", index+1),
			}).Error; err != nil {
			t.Fatal(err)
		}
		result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
			ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
		})
		if err != nil {
			t.Fatalf("export %d must not fail: %v", index+1, err)
		}
		if result.Artifact.ArtifactStatus != model.ExportArtifactReady {
			t.Fatalf("export %d must render: %s", index+1, result.Job.ErrorCode)
		}
		if _, seen := versions[result.Artifact.ArtifactVersion]; seen {
			t.Fatalf("version %d handed out twice", result.Artifact.ArtifactVersion)
		}
		versions[result.Artifact.ArtifactVersion] = result.Artifact.ID
	}
	for want := int64(1); want <= 10; want++ {
		if _, ok := versions[want]; !ok {
			t.Fatalf("version %d is missing: allocation must have no holes, got %v", want, versions)
		}
	}
	rows, err := fx.svc.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Fatalf("ten distinct exports must leave ten rows, got %d", len(rows))
	}
}

func TestConcurrentIdenticalExportsProduceOneArtifact(t *testing.T) {
	fx := newReportFixture(t, "a30b")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	const callers = 5
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			<-start // release together: staggered calls would only test the happy path
			result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
				ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
			})
			if err != nil {
				errs[slot] = err
				return
			}
			ids[slot] = result.Artifact.ID
		}(index)
	}
	close(start)
	wg.Wait()

	for slot, err := range errs {
		if err != nil {
			t.Fatalf("caller %d must get the winning artifact, not an error: %v", slot, err)
		}
	}
	for slot := 1; slot < callers; slot++ {
		if ids[slot] != ids[0] {
			t.Fatalf("concurrent identical exports must share one artifact: %v", ids)
		}
	}
	rows, err := fx.svc.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("A30b: %d concurrent clicks must leave one artifact, got %d", callers, len(rows))
	}
}
