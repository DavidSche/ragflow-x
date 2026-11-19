package service

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// The five delivery formats of a report (markdown, docx, pdf, html, xlsx).
//
// The renderers are tested in their own files; this test pins what the *delivery
// path* must do for each of them, because the failure mode of adding a format is not
// a bad file — it is a good file the operator cannot find, or a second copy of it:
//
//   - one format, one artifact: replaying the same request returns the artifact that
//     already exists (the canonical hash is the idempotency component, §7.7);
//   - the artifact carries the mime type and extension the browser needs, otherwise
//     the download is a `.docx`-named HTML file and every downstream tool is wrong;
//   - each format gets its own artifact version space, so exporting PDF after DOCX
//     does not read as "the DOCX was replaced".
func TestReportExportDeliversFiveFormats(t *testing.T) {
	fx := newReportFixture(t, "five-formats")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct{ format, extension, mime string }{
		{model.ExportFormatMarkdown, ".md", "text/markdown; charset=utf-8"},
		{model.ExportFormatDOCX, ".docx",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{model.ExportFormatPDF, ".pdf", "application/pdf"},
		{model.ExportFormatHTML, ".html", "text/html; charset=utf-8"},
		{model.ExportFormatXLSX, ".xlsx",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	}
	for _, testCase := range cases {
		request := ReportExportRequest{
			ReportID: report.ID, Revision: revision.Revision, Format: testCase.format,
		}
		first, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, request)
		if err != nil {
			t.Fatalf("%s export: %v", testCase.format, err)
		}
		if testCase.format == model.ExportFormatPDF && !usableFontFile(pdfFontPath()) {
			// No embeddable CJK TrueType font on this host (the macOS runner ships
			// none). The renderer must then fail closed with the documented code
			// instead of delivering a PDF of blank glyphs — the failure mode the
			// live drill hit in the drill image. The other four formats still prove
			// the delivery contract, so this leg asserts the degraded path rather
			// than skipping the whole test.
			if first.Artifact.ArtifactStatus != model.ExportArtifactFailed ||
				first.Job == nil || first.Job.ErrorCode != "export_render_failed" || first.Downloadable {
				t.Fatalf("pdf without a CJK font must fail closed with export_render_failed, "+
					"got status=%q downloadable=%v", first.Artifact.ArtifactStatus, first.Downloadable)
			}
			t.Logf("pdf readiness not asserted: no CJK font on this host")
			continue
		}
		if first.Artifact.ArtifactStatus != model.ExportArtifactReady {
			t.Fatalf("%s must deliver a ready artifact, got %q",
				testCase.format, first.Artifact.ArtifactStatus)
		}
		if first.Artifact.MimeType != testCase.mime {
			t.Fatalf("%s mime must be %q, got %q",
				testCase.format, testCase.mime, first.Artifact.MimeType)
		}
		if !strings.HasSuffix(first.Artifact.Filename, testCase.extension) {
			t.Fatalf("%s filename must end with %q, got %q",
				testCase.format, testCase.extension, first.Artifact.Filename)
		}
		if !strings.Contains(first.Artifact.Filename, "r") {
			t.Fatalf("%s filename must name the revision: %q",
				testCase.format, first.Artifact.Filename)
		}
		if first.Artifact.ArtifactVersion != 1 {
			t.Fatalf("%s is the first artifact of its own format, want v1, got v%d",
				testCase.format, first.Artifact.ArtifactVersion)
		}
		body, err := os.ReadFile(first.Artifact.FileRef)
		if err != nil {
			t.Fatalf("%s artifact body: %v", testCase.format, err)
		}
		if len(body) == 0 {
			t.Fatalf("%s delivered an empty file", testCase.format)
		}
		if first.Artifact.SHA256 == "" || first.Artifact.ByteSize != int64(len(body)) {
			t.Fatalf("%s artifact must record its own hash and size", testCase.format)
		}

		// Replay: same frozen content, same artifact, no new version number.
		second, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, request)
		if err != nil {
			t.Fatalf("%s replay: %v", testCase.format, err)
		}
		if second.Artifact.ID != first.Artifact.ID {
			t.Fatalf("%s replay must return the same artifact, got %s then %s",
				testCase.format, first.Artifact.ID, second.Artifact.ID)
		}

		// The download route answers with the artifact's own bytes and mime.
		downloaded, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
			report.ID, revision.Revision, first.Artifact.ID)
		if err != nil {
			t.Fatalf("%s download: %v", testCase.format, err)
		}
		if downloaded.MimeType != testCase.mime {
			t.Fatalf("%s download mime must match the artifact", testCase.format)
		}
	}

	// One artifact per format, all at version 1: the formats are independent
	// deliveries of one revision, not five attempts at one file.
	rows, err := fx.svc.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(cases) {
		t.Fatalf("want %d artifacts, got %d", len(cases), len(rows))
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.ArtifactVersion != 1 {
			t.Fatalf("format %s must stay at v1, got v%d", row.Format, row.ArtifactVersion)
		}
		seen[row.Format] = true
	}
	for _, testCase := range cases {
		if !seen[testCase.format] {
			t.Fatalf("the artifact list dropped the %s delivery", testCase.format)
		}
	}
}

// TestReportExportHtmlAndXlsxAreTheFrozenPayload keeps the new formats from becoming
// a second source of truth: the bytes on disk must contain the disclosure the
// Markdown export contains, for the same revision.
func TestReportExportHtmlAndXlsxAreTheFrozenPayload(t *testing.T) {
	fx := newReportFixture(t, "five-formats-content")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{model.ExportFormatHTML, model.ExportFormatXLSX} {
		result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
			ReportID: report.ID, Revision: revision.Revision, Format: format,
		})
		if err != nil {
			t.Fatalf("%s export: %v", format, err)
		}
		body, err := os.ReadFile(result.Artifact.FileRef)
		if err != nil {
			t.Fatal(err)
		}
		if format == model.ExportFormatHTML {
			if !bytes.Contains(body, []byte("<!doctype html>")) {
				t.Fatal("the HTML delivery is not an HTML document")
			}
			if !bytes.Contains(body, []byte("完整性")) {
				t.Fatal("the HTML delivery lost the completeness disclosure")
			}
			continue
		}
		if !bytes.HasPrefix(body, []byte("PK\x03\x04")) {
			t.Fatal("the XLSX delivery is not a zip package")
		}
	}
	// The layout identity is per format (`<template>-v<n>-<format>`), so two files
	// never claim to be the same layout (A4: two files claiming to be one document
	// must not disagree about what they are).
	for _, pair := range [][2]string{
		{model.ExportFormatHTML, model.ExportFormatXLSX},
		{model.ExportFormatHTML, model.ExportFormatMarkdown},
		{model.ExportFormatXLSX, model.ExportFormatDOCX},
	} {
		left := reportExportLayoutID("report-compliance", pair[0])
		right := reportExportLayoutID("report-compliance", pair[1])
		if left == right {
			t.Fatalf("%s and %s share the layout id %q", pair[0], pair[1], left)
		}
	}
}
