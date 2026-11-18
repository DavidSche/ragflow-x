package service

// One closed format set, one vocabulary (doc/288 §11.3 vs §12).
//
// The template catalogue advertises `output_formats: [docx, md, pdf]`, and the
// panel's export buttons are built from that list; the artifact format vocabulary
// is `markdown`. Found live on 4.151 (R10): a client exporting exactly what the
// catalogue advertised was answered 400/40130 "unsupported report export format"
// for a supported format.
//
// The assertion is deliberately stronger than "md is accepted": an alias that
// lands on a *different* identity would mint a second artifact version for the
// same frozen content, breaking A30b (a duplicate click must never mint two
// versions) and splitting the canonical hash. So `md` and `markdown` must resolve
// to one artifact row.

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestReportExportTreatsMdAndMarkdownAsOneFormat(t *testing.T) {
	fx := newReportFixture(t, "export-alias")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())

	fromCatalogue, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: revision.ReportID, Revision: revision.Revision, Format: "md",
	})
	if err != nil {
		t.Fatalf("the catalogue's `md` must export, not 40130: %v", err)
	}
	if fromCatalogue.Artifact.Format != model.ExportFormatMarkdown {
		t.Fatalf("`md` must be stored as %q, got %q",
			model.ExportFormatMarkdown, fromCatalogue.Artifact.Format)
	}
	if fromCatalogue.Artifact.ArtifactVersion != 1 {
		t.Fatalf("the first export of a revision is version 1, got %d",
			fromCatalogue.Artifact.ArtifactVersion)
	}

	// Same frozen content, the API's own spelling: the same artifact, not version 2.
	fromAPI, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: revision.ReportID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("markdown export: %v", err)
	}
	if fromAPI.Artifact.ID != fromCatalogue.Artifact.ID {
		t.Fatalf("`md` and `markdown` must be one identity, got %s then %s",
			fromCatalogue.Artifact.ID, fromAPI.Artifact.ID)
	}
	rows, err := fx.svc.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ArtifactVersion != 1 {
		t.Fatalf("one frozen content must leave one version-1 row, got %+v", rows)
	}
}

// TestReportExportRejectsFormatsOutsideTheReportSet keeps the alias from
// widening the set: `html` and `json` are answer-side formats, and a report export
// asking for them must still be refused with the one code that says so.
func TestReportExportRejectsFormatsOutsideTheReportSet(t *testing.T) {
	fx := newReportFixture(t, "export-closed-set")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())

	for _, format := range []string{"html", "json", "", "MDX"} {
		_, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
			ReportID: revision.ReportID, Revision: revision.Revision, Format: format,
		})
		if code := reportErrorCode(err); code != 40130 {
			t.Fatalf("format %q must be refused with 40130, got %d (%v)", format, code, err)
		}
	}
}
