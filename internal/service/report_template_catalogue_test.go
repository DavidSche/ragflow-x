package service

// The shipped template catalogue (doc/288 §11.1 / §16 T1, completed by P0-B's
// "+2 模板").
//
// Three claims are worth a test, and each one fails silently if wrong:
//
//  1. a second and third template are **reachable**: `CreateReport` accepts the key,
//     pins its version and materialises *that* template's sections — a catalogue
//     entry nobody can run is a decorative JSON blob;
//  2. the section graph is what makes each template different, so the outline order
//     is asserted per template rather than "sorted somehow";
//  3. the export layout id records the content template that actually produced the
//     file (`report-postmortem-v1-docx`), because that column is read as evidence.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestCreateReportAcceptsEveryShippedTemplate(t *testing.T) {
	fx := newReportFixture(t, "catalogue")
	ctx := context.Background()

	for _, shipped := range BuiltinReportTemplates() {
		shipped := shipped
		t.Run(shipped.Key, func(t *testing.T) {
			request := reportCreateRequest(fx.datasetLink)
			request.TemplateKey = shipped.Key
			request.Title = "catalogue-" + shipped.Key
			key := "req-catalogue-" + shipped.Key + "-abcdef"
			result, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, key, request)
			if err != nil {
				t.Fatalf("creation must accept a shipped template: %v", err)
			}
			if result.Revision.TemplateKey != shipped.Key {
				t.Fatalf("revision must pin %q, got %q", shipped.Key, result.Revision.TemplateKey)
			}
			if result.Revision.TemplateVersion != 1 {
				// The platform baseline starts at 1; a re-seeded payload bumping the
				// version is a different test's job (report_template_test.go).
				t.Fatalf("fresh baseline must be v1, got %d", result.Revision.TemplateVersion)
			}
			// The execution snapshot freezes the template body, so the worker writes
			// this template's sections rather than resolving it again at run time.
			row, err := fx.svc.resolveReportTemplate(ctx, fx.tenantID, shipped.Key, result.Revision.TemplateVersion)
			if err != nil || row == nil {
				t.Fatalf("the pinned version must resolve: %v", err)
			}
			payload, err := reportTemplatePayloadOf(row)
			if err != nil {
				t.Fatal(err)
			}
			want := reportTemplateSectionOrder(&shipped.Payload)
			got := reportTemplateSectionOrder(payload)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("pinned payload sections drifted: got %v want %v", got, want)
			}
		})
	}
}

// TestShippedTemplatesOrderSectionsTheWayTheBodyReads pins the outline of the two
// P0-B templates. Order is the reader's argument: a postmortem that opens with root
// cause before the timeline makes the reader accept a conclusion before the facts.
func TestShippedTemplatesOrderSectionsTheWayTheBodyReads(t *testing.T) {
	cases := map[string][]string{
		reportTechnicalReviewKey: {
			"context", "criteria", "proposal", "assessment", "risks", "recommendation",
		},
		reportPostmortemTemplateKey: {
			"summary", "timeline", "impact", "root_cause", "response", "actions",
		},
	}
	for key, want := range cases {
		var payload *ReportTemplatePayload
		for _, shipped := range BuiltinReportTemplates() {
			if shipped.Key == key {
				copied := shipped.Payload
				payload = &copied
				break
			}
		}
		if payload == nil {
			t.Fatalf("template %q is not shipped", key)
		}
		got := reportTemplateSectionOrder(payload)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s section order: got %v want %v", key, got, want)
		}
		// A dependency must appear earlier in the body: the writer of a section is
		// fed its dependencies' text, so a forward reference would feed it nothing.
		position := make(map[string]int, len(got))
		for index, sectionKey := range got {
			position[sectionKey] = index
		}
		for _, section := range payload.Sections {
			for _, dep := range section.DependsOn {
				if position[dep] >= position[section.Key] {
					t.Fatalf("%s: %q depends on %q but is written first", key, section.Key, dep)
				}
			}
		}
	}
}

// TestExportLayoutIDFollowsTheContentTemplate proves the delivered file names the
// template that produced it — and that the pre-P0-B compliance string is unchanged,
// because those rows are already stored and read as evidence.
func TestExportLayoutIDFollowsTheContentTemplate(t *testing.T) {
	cases := []struct {
		key, format, want string
	}{
		{reportComplianceTemplateKey, "docx", "report-compliance-v1-docx"},
		{reportComplianceTemplateKey, "pdf", "report-compliance-v1-pdf"},
		{reportPostmortemTemplateKey, "md", "report-postmortem-v1-md"},
		{reportTechnicalReviewKey, "docx", "report-tech-review-v1-docx"},
		{"", "docx", "report-v1-docx"},
	}
	for _, tc := range cases {
		if got := reportExportLayoutID(tc.key, tc.format); got != tc.want {
			t.Fatalf("layout id for %q/%q: got %q want %q", tc.key, tc.format, got, tc.want)
		}
	}
}

// TestEveryShippedLayoutIDFitsTheColumn is the durable guard: `template_id` is 32
// bytes, and every shipped template × format has to land inside it. Without this, a
// longer key passes on SQLite (which ignores VARCHAR length) and fails on MySQL only
// when someone exports — the worst possible place to learn.
func TestEveryShippedLayoutIDFitsTheColumn(t *testing.T) {
	formats := []string{model.ExportFormatDOCX, model.ExportFormatMarkdown, model.ExportFormatPDF}
	for _, shipped := range BuiltinReportTemplates() {
		for _, format := range formats {
			id := reportExportLayoutID(shipped.Key, format)
			if len(id) > 32 {
				t.Fatalf("layout id %q is %d bytes, column is 32", id, len(id))
			}
		}
	}
}

// TestPostmortemReportExportsUnderItsOwnLayout is the end-to-end half: a published
// postmortem report renders to a file whose own footer names the postmortem layout.
// The layout id is embedded in the delivered bytes, so the claim survives being
// forwarded out of the system — nobody can re-derive it from our database later.
func TestPostmortemReportExportsUnderItsOwnLayout(t *testing.T) {
	fx := newReportFixture(t, "layout")
	ctx := context.Background()
	report, revision := seedPublishedPostmortem(t, fx)

	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.Artifact.ArtifactStatus != model.ExportArtifactReady {
		t.Fatalf("export must render, got artifact %s / job %s", result.Artifact.ArtifactStatus, result.Job.ErrorCode)
	}
	content, err := os.ReadFile(result.Artifact.FileRef)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	wantLayout := reportExportLayoutID(reportPostmortemTemplateKey, model.ExportFormatMarkdown)
	if !strings.Contains(string(content), wantLayout) {
		t.Fatalf("delivered file must name the template that produced it; footer was: %q",
			tail(string(content), 200))
	}
	if strings.Contains(string(content), "report-compliance-v1") {
		t.Fatal("a postmortem file must not claim the compliance layout")
	}
}

// seedPublishedPostmortem publishes a report pinned to the postmortem template, with
// that template's six sections written. The report's sections are keyed by the
// template, not by the compliance keys — which is the whole point of the run.
func seedPublishedPostmortem(t *testing.T, fx *reportFixture) (*model.Report, *model.ReportRevision) {
	t.Helper()
	ctx := context.Background()
	request := reportCreateRequest(fx.datasetLink)
	request.TemplateKey = reportPostmortemTemplateKey
	created, err := fx.svc.CreateReport(ctx, fx.owner.ID, fx.tenantID, "req-postmortem-0001", request)
	if err != nil {
		t.Fatalf("create postmortem report: %v", err)
	}
	fx.seedSections(t, created.Revision.ID, []reportSectionSeed{
		{Key: "summary", Status: model.ReportSectionSucceeded},
		{Key: "timeline", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 2, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
		{Key: "impact", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
		{Key: "root_cause", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 2, EvidenceStatus: model.ReportEvidencePartiallySupported, QualityStatus: model.ReportQualityPassed},
		{Key: "response", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
		{Key: "actions", Status: model.ReportSectionSucceeded, RequiresEvi: true,
			CitationCount: 1, EvidenceStatus: model.ReportEvidenceSupported, QualityStatus: model.ReportQualityPassed},
	})
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatalf("begin finalization: %v", err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionPublished {
		t.Fatalf("revision must be published, got %s", revision.Status)
	}
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, created.Report.ID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}
	return report, revision
}

// tail keeps a failing assertion readable without dumping a whole document.
func tail(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[len(text)-n:]
}
