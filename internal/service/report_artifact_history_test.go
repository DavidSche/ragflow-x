package service

// A11 — "产物跨会话/刷新可见可下载；`artifact_version` 递增不覆盖旧版".
//
// The interesting failure isn't rendering: it's the *second* page load. An export
// list that only exists in the response of the POST that created it is invisible
// after a refresh, so a file delivered yesterday cannot be found again even though
// it is still on disk and still downloadable by id. The read aggregate is therefore
// part of the assertion, not a UI detail.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportReadCarriesTheArtifactHistory(t *testing.T) {
	fx := newReportFixture(t, "artifact_history")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}

	// A fresh page load before any export: an empty list, never a null.
	detail, err := fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, report.ID)
	if err != nil {
		t.Fatalf("read detail: %v", err)
	}
	if detail.Artifacts == nil {
		t.Fatal("artifacts must be an empty slice, not null: the UI renders a list")
	}
	if len(detail.Artifacts) != 0 {
		t.Fatalf("no export happened yet, got %d artifacts", len(detail.Artifacts))
	}

	first, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatalf("export docx: %v", err)
	}
	// Second format ⇒ second version of the same revision. It must not overwrite
	// the first row: "再次导出" is a delivery, not a correction.
	second, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("export md: %v", err)
	}
	// Versions run per (revision, format): this Markdown file is the *first* Markdown
	// export of revision 1 even though a DOCX was delivered just before it. Sharing a
	// single counter would make "第 2 次导出 DOCX" unanswerable after one Markdown
	// export, and the version is a per-format claim (§7.7 / A30a).
	if first.Artifact.ArtifactVersion != 1 || second.Artifact.ArtifactVersion != 1 {
		t.Fatalf("each format keeps its own version sequence: docx=%d md=%d",
			first.Artifact.ArtifactVersion, second.Artifact.ArtifactVersion)
	}
	if first.Artifact.Format == second.Artifact.Format {
		t.Fatalf("the two exports must differ in format: %q", first.Artifact.Format)
	}

	// "Cross-session": a brand-new read (as after F5) sees both deliveries, newest
	// version first, both downloadable.
	detail, err = fx.svc.GetReport(ctx, fx.owner.ID, fx.tenantID, report.ID)
	if err != nil {
		t.Fatalf("re-read detail: %v", err)
	}
	if len(detail.Artifacts) != 2 {
		t.Fatalf("both deliveries must survive a re-read, got %d", len(detail.Artifacts))
	}
	// Newest delivery first: a mixed-format history is ordered by when the file was
	// written, because the version numbers live in separate per-format sequences.
	if detail.Artifacts[0].CreatedAt.Before(detail.Artifacts[1].CreatedAt) {
		t.Fatalf("history must be newest delivery first: %s then %s",
			detail.Artifacts[0].CreatedAt, detail.Artifacts[1].CreatedAt)
	}
	for _, item := range detail.Artifacts {
		if !item.Downloadable {
			t.Fatalf("a ready, unexpired artifact must be downloadable: %+v", item)
		}
		if item.ArtifactStatus != model.ExportArtifactReady {
			t.Fatalf("unexpected status %q", item.ArtifactStatus)
		}
	}

	// The revision-numbered read (the surface that also exposes `superseded`
	// revisions, A41) carries the same history — otherwise an old revision would
	// show its sections but lose its files.
	byNumber, err := fx.svc.GetReportRevisionByNumber(ctx, fx.owner.ID, fx.tenantID, report.ID, revision.Revision)
	if err != nil {
		t.Fatalf("read by number: %v", err)
	}
	if len(byNumber.Artifacts) != 2 {
		t.Fatalf("the numbered read must carry the same history, got %d", len(byNumber.Artifacts))
	}

	// Every listed row's flag comes from the same derivation the download endpoint
	// uses. Asserting the equality (rather than "it happens to be true today") keeps
	// the page and the endpoint from forking when a status is added.
	clock := time.Now().UTC()
	for _, item := range detail.Artifacts {
		want := reportArtifactDownloadable(item.ExportArtifact, clock)
		if item.Downloadable != want {
			t.Fatalf("read flag %v disagrees with the download decision %v for %s",
				item.Downloadable, want, item.ID)
		}
	}

	// "不覆盖旧版" is structural, not a promise: the artifact table is insert-only, so
	// there is no code path — including one written later by mistake — that can
	// rewrite a delivered version in place. That is why the 30-day clock expiring a
	// row later is a *state* change the row then reports (via `downloadable`) rather
	// than a rewrite of the delivery.
	if err := fx.gdb.Exec("UPDATE rgx_export_artifact SET expires_at = ? WHERE id = ?",
		time.Now().UTC().Add(-time.Hour), first.Artifact.ID).Error; err == nil {
		t.Fatal("a delivered artifact must be immutable: the update succeeded")
	}
}
