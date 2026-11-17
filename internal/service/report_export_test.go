package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Report export T11 (doc/288 §7.7 / §12). These tests drive the real store and the
// real finalized pipeline: a revision is published through the normal
// generate→finalize path, then frozen into Markdown/DOCX. The invariants pinned
// here are cross-table facts —
//
//   - one export writes snapshot + job + artifact in the same transaction;
//   - the artifact is backfilled to `ready` (sha256 + byte_size) after the render;
//   - the same frozen content is idempotent: a second click returns the same
//     artifact, it never mints a second version;
//   - only `published` revisions export (44106), but a `superseded` revision stays
//     downloadable;
//   - the DOCX package carries the 9 parts and the §12 header/footer, with no PAGE
//     field;
//   - download gates: owner-only (44109), revision-paired, expired → not found.

// publishReport drives one report to a published revision through the real finalize
// path. It returns a helper the export tests can address.
func publishReport(t *testing.T, fx *reportFixture, seeds []reportSectionSeed) *model.ReportRevision {
	t.Helper()
	created := fx.createReport(t, fx.datasetLink, "req-export-0001")
	fx.seedSections(t, created.Revision.ID, seeds)
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(context.Background(), fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatalf("begin finalization: %v", err)
	}
	if _, err := fx.svc.FinalizeReportJob(context.Background(), fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionPublished {
		t.Fatalf("revision must be published before export: %s", revision.Status)
	}
	return revision
}

func TestCreateReportExportFreezesPublishedRevision(t *testing.T) {
	fx := newReportFixture(t, "frozen")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil || report == nil {
		t.Fatalf("load report: %v", err)
	}

	now := time.Now().UTC()
	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.Artifact == nil || result.Job == nil {
		t.Fatalf("export must return both artifact and job: %+v", result)
	}
	artifact := result.Artifact
	if artifact.ArtifactStatus != model.ExportArtifactReady {
		t.Fatalf("sync export must end ready, got %s", artifact.ArtifactStatus)
	}
	if artifact.Format != model.ExportFormatDOCX || artifact.RendererVersion != reportExportRendererVersion {
		t.Fatalf("artifact metadata wrong: %+v", artifact)
	}
	if artifact.SubjectType != model.ExportSubjectReport || artifact.ReportRevisionID != revision.ID {
		t.Fatalf("artifact must name subject+revision: %+v", artifact)
	}
	if artifact.ByteSize <= 0 || len(artifact.SHA256) != 64 {
		t.Fatalf("backfill must record size+hash: %d / %q", artifact.ByteSize, artifact.SHA256)
	}
	if delta := artifact.ExpiresAt.Sub(now) - 30*24*time.Hour; delta < -time.Minute || delta > time.Minute {
		t.Fatalf("artifact TTL must be the configured 30 days, off by %v", delta)
	}

	// Job: succeeded, traced to the revision, idempotency namespaced.
	job := result.Job
	if job.Status != model.ExportJobSucceeded || job.SubjectType != model.ExportSubjectReport ||
		job.ReportRevisionID != revision.ID || job.RequestedBy != fx.owner.ID {
		t.Fatalf("job wrong: %+v", job)
	}
	wantKey := "report:" + revision.ID + ":" + model.ExportFormatDOCX + ":" + result.Canonical
	if job.IdempotencyKey != wantKey {
		t.Fatalf("idempotency key wrong: %q != %q", job.IdempotencyKey, wantKey)
	}

	// Snapshot: frozen payload is exactly the assembled document's JSON.
	snapshot, err := fx.svc.Store.GetExportSnapshot(ctx, fx.tenantID, job.ExportSnapshotID)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snapshot.SubjectType != model.ExportSubjectReport || snapshot.ReportRevisionID != revision.ID {
		t.Fatalf("snapshot must carry report identity: %+v", snapshot)
	}
	document := result.Document
	if document == nil || len(document.Sections) != 5 {
		t.Fatalf("document must contain the 5 succeeded sections, got %d", len(document.Sections))
	}
	if document.Completeness != model.ReportCompletenessFull || len(document.Gaps) != 0 {
		t.Fatalf("full report must assemble with no gaps: %s / %d", document.Completeness, len(document.Gaps))
	}
	if result.Canonical != sha256Hex(snapshot.PayloadJSON) {
		t.Fatalf("canonical hash must equal the payload hash")
	}

	// The file landed where the artifact points, and is a valid 9-part docx.
	if _, err := os.Stat(artifact.FileRef); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
	content, err := os.ReadFile(artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ByteSize != int64(len(content)) {
		t.Fatalf("byte_size must match the file, %d != %d", artifact.ByteSize, len(content))
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("docx must be a valid zip: %v", err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	for _, part := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml",
		"word/_rels/document.xml.rels", "word/styles.xml", "word/header1.xml",
		"word/footer1.xml", "docProps/core.xml", "docProps/app.xml"} {
		if !names[part] {
			t.Fatalf("docx missing part %s", part)
		}
	}
	if names["word/numbering.xml"] {
		t.Fatal("P0-A docx must not carry a numbering part")
	}

	// §12: no PAGE/NUMPAGES fields anywhere; header carries title+version+time;
	// footer carries the canonical hash in small type; body has no Answer
	// Snapshot / Canonical Hash heading.
	joined := readAllZipEntries(t, reader)
	if strings.Contains(joined, "PAGE ") || strings.Contains(joined, "NUMPAGES") {
		t.Fatal("§12 forbids page-number fields; found PAGE/NUMPAGES in the docx")
	}
	if !strings.Contains(joined, "Canonical "+result.Canonical) {
		t.Fatal("footer must carry the canonical hash")
	}
	if !strings.Contains(joined, reportExportRendererVersion) {
		t.Fatal("footer must name the renderer version")
	}
	if strings.Contains(joined, "Answer Snapshot:") || strings.Contains(joined, "Canonical Hash:") {
		t.Fatal("§12 moves the snapshot header out of the body")
	}

	// Chinese font pinned on every style; eastAsia must appear.
	if !strings.Contains(joined, "w:eastAsia=\"SimHei\"") {
		t.Fatal("heading styles must pin an eastAsia font")
	}
}

func readAllZipEntries(t *testing.T, reader *zip.Reader) string {
	t.Helper()
	var builder strings.Builder
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		builder.Write(data)
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestCreateReportExportIsIdempotentAndReplayNotSecondVersion(t *testing.T) {
	fx := newReportFixture(t, "replay")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	first, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Artifact.ID != first.Artifact.ID || second.Job.ID != first.Job.ID {
		t.Fatalf("same-content replay must return the same artifact/job")
	}
	if second.Canonical != first.Canonical {
		t.Fatalf("same payload must produce the same canonical hash")
	}
	// §7.7 rule 2 (A30b): a duplicate click must NOT mint a second version.
	rows, err := fx.svc.Store.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("replay must not mint a second artifact, got %d", len(rows))
	}
	if rows[0].ArtifactVersion != 1 {
		t.Fatalf("first export must be version 1, got %d", rows[0].ArtifactVersion)
	}
}

func TestCreateReportExportRendersMarkdownDeterministically(t *testing.T) {
	fx := newReportFixture(t, "md")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	one, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if one.Artifact.MimeType != "text/markdown; charset=utf-8" {
		t.Fatalf("markdown mime wrong: %s", one.Artifact.MimeType)
	}
	content, err := os.ReadFile(one.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "# "+report.Title) {
		t.Fatalf("markdown must open with the title:\n%s", text)
	}
	if !strings.HasPrefix(text, "# ") || !strings.Contains(text, "## 1. background") {
		t.Fatalf("markdown must render numbered section headings:\n%s", text)
	}
	// The canonical hash freezes the assembled payload (the content identity that
	// drives idempotency); the artifact's sha256 freezes the delivered file. They
	// are intentionally different facts, and each must match its own file.
	if one.Artifact.SHA256 != sha256Hex(string(content)) {
		t.Fatalf("artifact sha256 must hash the delivered file, %q != %q",
			one.Artifact.SHA256, sha256Hex(string(content)))
	}
}

func TestCreateReportExportPartialRendersGapBanner(t *testing.T) {
	fx := newReportFixture(t, "partial")
	ctx := context.Background()
	seeds := successSeeds()
	seeds[3].Status = model.ReportSectionFailed
	seeds[3].CitationCount = 0
	seeds[3].EvidenceStatus = model.ReportEvidenceUnsupported
	revision := publishReport(t, fx, seeds)
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Document.Completeness != model.ReportCompletenessPartial || len(result.Document.Gaps) != 1 {
		t.Fatalf("partial report must carry the frozen gap: %s / %d",
			result.Document.Completeness, len(result.Document.Gaps))
	}
	content, err := os.ReadFile(result.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "完整性声明") {
		t.Fatalf("the delivered file must disclose the partial state:\n%s", text)
	}
	if !strings.Contains(text, "第 4 节") {
		t.Fatalf("the gap banner must name the missing section:\n%s", text)
	}
	// The disclosure must come from the frozen payload, never the live API: both
	// markdown and docx renderers go through the same document struct.
	for _, format := range []string{model.ExportFormatDOCX} {
		docxResult, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
			ReportID: report.ID, Revision: revision.Revision, Format: format,
		})
		if err != nil {
			t.Fatal(err)
		}
		docxBytes := mustReadFile(t, docxResult.Artifact.FileRef)
		reader, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
		if err != nil {
			t.Fatal(err)
		}
		xml := readAllZipEntries(t, reader)
		if !strings.Contains(xml, "完整性声明") {
			t.Fatalf("docx must disclose the partial state too")
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCreateReportExportRefusesUnpublishedRevision(t *testing.T) {
	fx := newReportFixture(t, "draft")
	ctx := context.Background()
	created := fx.createReport(t, "link-policy-draft", "req-draft-0001")
	fx.seedSections(t, created.Revision.ID, successSeeds())

	_, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: created.Report.ID, Revision: created.Revision.Revision, Format: model.ExportFormatDOCX,
	})
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportExportRequiresPublished || httpErr.Status != 409 {
		t.Fatalf("draft revision must refuse 409/%d, got %v", codeReportExportRequiresPublished, err)
	}
	var artifacts []model.ExportArtifact
	if err := fx.gdb.Find(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("a refused export must not leave artifact rows, got %d", len(artifacts))
	}
}

func TestCreateReportExportUnknownRevisionIsNotFound(t *testing.T) {
	fx := newReportFixture(t, "rev404")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	_, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: 99, Format: model.ExportFormatDOCX,
	})
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportRevisionNotFound || httpErr.Status != 404 {
		t.Fatalf("unknown revision must be 404/%d, got %v", codeReportRevisionNotFound, err)
	}
}

func TestCreateReportExportRejectsUnsupportedFormat(t *testing.T) {
	fx := newReportFixture(t, "fmt")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	_, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatPDF,
	})
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != 40130 || httpErr.Status != 400 {
		t.Fatalf("pdf must be refused as unsupported, got %v", err)
	}
}

func TestOpenReportArtifactDownloadsAndAudits(t *testing.T) {
	fx := newReportFixture(t, "download")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	exported, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, exported.Artifact.ID)
	if err != nil {
		t.Fatalf("owner must download own artifact: %v", err)
	}
	if artifact.ID != exported.Artifact.ID {
		t.Fatal("download must return the addressed artifact")
	}
	if _, err := os.Stat(artifact.FileRef); err != nil {
		t.Fatalf("downloaded artifact must exist on disk: %v", err)
	}

	// Exactly one download audit row, tracing back the revision.
	var audits []model.ExportDownloadAudit
	if err := fx.gdb.Model(&model.ExportDownloadAudit{}).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("one download must write one audit row, got %d", len(audits))
	}
	if audits[0].ExportArtifactID != exported.Artifact.ID || audits[0].ReportRevisionID != revision.ID ||
		audits[0].AnswerSnapshotID != "" {
		t.Fatalf("audit row must trace the report revision: %+v", audits[0])
	}
	if audits[0].DownloadedBy != fx.owner.ID || audits[0].DecisionHash == "" {
		t.Fatalf("audit must name actor and policy decision: %+v", audits[0])
	}
	// A second download appends a second row (event semantics, never a counter).
	if _, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, exported.Artifact.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ExportDownloadAudit{}).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 {
		t.Fatalf("each download appends one audit row, got %d", len(audits))
	}
}

func TestOpenReportArtifactGatesOwnershipAndPairing(t *testing.T) {
	fx := newReportFixture(t, "gates")
	ctx := context.Background()
	// A colleague in the same tenant.
	colleague, err := fx.svc.CreateUser(ctx, fx.tenantID, "", CreateUserRequest{
		Username: "report-colleague-" + t.Name()[:8], Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	exported, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A colleague reaches nothing: the id is real, the answer is 44109, and there
	// is no way to distinguish it from a missing artifact.
	_, err = fx.svc.OpenReportArtifact(ctx, colleague.ID, fx.tenantID,
		report.ID, revision.Revision, exported.Artifact.ID)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
		t.Fatalf("colleague must hit 404/44109, got %v", err)
	}
	// A user in another tenant cannot reach the artifact either — no existence
	// oracle: the report lookup misses, and authorizeReportRead answers 44109.
	otherTenant, err := fx.svc.CreateTenant(ctx, "报告工作区 foreign")
	if err != nil {
		t.Fatal(err)
	}
	foreignUser, err := fx.svc.CreateUser(ctx, otherTenant.ID, "", CreateUserRequest{
		Username: "report-foreign-" + t.Name()[:8], Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fx.svc.OpenReportArtifact(ctx, foreignUser.ID, otherTenant.ID,
		report.ID, revision.Revision, exported.Artifact.ID)
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
		t.Fatalf("cross-tenant must hit 404/44109, got %v", err)
	}
	// A random artifact id is not found.
	_, err = fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, "artifact-does-not-exist")
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
		t.Fatalf("random artifact id must be 404/44109, got %v", err)
	}
	// Wrong revision pairing: URL says r2 but the artifact belongs to r1.
	_, err = fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision+1, exported.Artifact.ID)
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportRevisionNotFound {
		t.Fatalf("mismatched revision must fail on the revision, got %v", err)
	}
}

// The artifact table is insert-only — the immutability anchors raise on any
// UPDATE (migrate.go:1875) — so the pending/expired rows in this test are INSERTed
// directly, exactly like the service's own failure path would write them.
func TestReportArtifactExpiredOrPendingRefusesDownload(t *testing.T) {
	fx := newReportFixture(t, "expiry")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	now := time.Now().UTC()
	// A pending placeholder that will never be backfilled (walked over as-is on
	// replay — §7.7 / A30b): the row is yours and addressed, just not done yet.
	pendingJob := exportTestJob(fx, revision, "pending-job-0001", "key-pending-0001", model.ExportJobRunning)
	pendingArtifact := exportTestArtifact(fx, revision, pendingJob, "pending-artifact-0001",
		model.ExportArtifactPending, "", 0, 1, now.Add(reportDefaultArtifactDays*24*time.Hour))
	if err := fx.gdb.Create(pendingJob).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Create(pendingArtifact).Error; err != nil {
		t.Fatal(err)
	}
	_, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, pendingArtifact.ID)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportInvalidTransition || httpErr.Status != 409 {
		t.Fatalf("pending download must answer 409/44104, got %v", err)
	}
	if httpErr.Message == "" {
		t.Fatal("pending must explain itself")
	}

	// An expired ready artifact: the file used to exist, the clock ran out, and
	// the answer is 404/44109 — not "still generating", and no audit row leaks.
	expiredJob := exportTestJob(fx, revision, "expired-job-0001", "key-expired-0001", model.ExportJobSucceeded)
	expiredArtifact := exportTestArtifact(fx, revision, expiredJob, "expired-artifact-0001",
		model.ExportArtifactReady, "not-a-real-file", 1, 2, now.Add(-time.Hour))
	if err := fx.gdb.Create(expiredJob).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Create(expiredArtifact).Error; err != nil {
		t.Fatal(err)
	}
	_, err = fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, expiredArtifact.ID)
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportAccessDenied || httpErr.Status != 404 {
		t.Fatalf("expired download must answer 404/44109, got %v", err)
	}
	var audits []model.ExportDownloadAudit
	if err := fx.gdb.Model(&model.ExportDownloadAudit{}).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 0 {
		t.Fatalf("a refused download must not audit, got %d rows", len(audits))
	}
}

// exportTestJob builds a job row that pairs with a directly-inserted artifact.
func exportTestJob(fx *reportFixture, revision *model.ReportRevision, id, key, status string) *model.ExportJob {
	now := time.Now().UTC()
	return &model.ExportJob{
		ID: id, TenantID: fx.tenantID,
		SubjectType:       model.ExportSubjectReport,
		ReportRevisionID:  revision.ID,
		AnswerSnapshotID:  "",
		ExportSnapshotID:  "",
		RequestedBy:       fx.owner.ID,
		Format:            model.ExportFormatDOCX,
		Status:            status,
		PolicyVersion:     reportExportPolicyVersion,
		PolicyEvaluatedAt: now,
		PolicyInputHash:   "policy-hash",
		IdempotencyKey:    "report:" + revision.ID + ":docx:" + key,
		CreatedAt:         now,
	}
}

// exportTestArtifact builds one directly-inserted artifact row.
func exportTestArtifact(fx *reportFixture, revision *model.ReportRevision, job *model.ExportJob,
	id, status, fileRef string, byteSize, version int64, expiresAt time.Time) *model.ExportArtifact {
	now := time.Now().UTC()
	return &model.ExportArtifact{
		ID: id, TenantID: fx.tenantID,
		ExportJobID:      job.ID,
		SubjectType:      model.ExportSubjectReport,
		ReportRevisionID: revision.ID,
		ArtifactVersion:  version,
		ArtifactStatus:   status,
		Format:           model.ExportFormatDOCX,
		RendererVersion:  reportExportRendererVersion,
		FileRef:          fileRef,
		Filename:         "report-test-0.docx",
		MimeType:         "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		ByteSize:         byteSize,
		SHA256:           "",
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
		CreatedBy:        fx.owner.ID,
	}
}

func TestSupersededRevisionKeepsArtifactDownloadable(t *testing.T) {
	fx := newReportFixture(t, "supersede")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	exported, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A newer revision lands (simulated; the regeneration route is a later slice):
	// the old revision is marked superseded but must keep its delivered file.
	if err := fx.gdb.Model(&model.ReportRevision{}).Where("id = ?", revision.ID).
		Update("status", model.ReportRevisionSuperseded).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, exported.Artifact.ID); err != nil {
		t.Fatalf("superseded revision must stay downloadable: %v", err)
	}
	// The list view still derives downloadable=true for it.
	views, err := fx.svc.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || !views[0].Downloadable {
		t.Fatalf("superseded artifact must stay downloadable in the view: %+v", views)
	}
	// ...but you cannot freeze *another* delivery from a superseded revision.
	var httpErr *httperr.Error
	_, err = fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if !errors.As(err, &httpErr) || httpErr.Code != codeReportExportRequiresPublished {
		t.Fatalf("superseded revision must refuse new exports with 44106, got %v", err)
	}
}

func TestReportExportArtifactVersionIncrementsWithNewContent(t *testing.T) {
	fx := newReportFixture(t, "veridx")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)

	first, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifact.ArtifactVersion != 1 {
		t.Fatalf("first export must be version 1")
	}

	// The content of the same revision changes (simulating a repaired section
	// backfilled before the next export): new canonical → new idempotency key →
	// the (revision, format) allocation must hand out version 2 without touching
	// the version-1 row.
	var section model.ReportSection
	if err := fx.gdb.Where("revision_id = ? AND ord = 0", revision.ID).First(&section).Error; err != nil {
		t.Fatal(err)
	}
	section.ContentMD = "内容：background（修订后补充法规背景）"
	section.ContentHash = "hash-background-v2"
	if err := fx.gdb.Model(&model.ReportSection{}).Where("id = ?", section.ID).
		Updates(map[string]interface{}{"content_md": section.ContentMD, "content_hash": section.ContentHash}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Artifact.ID == first.Artifact.ID {
		t.Fatal("changed content must produce a distinct artifact")
	}
	if second.Artifact.ArtifactVersion != 2 {
		t.Fatalf("changed content must take version 2, got %d", second.Artifact.ArtifactVersion)
	}
	rows, err := fx.svc.ListReportArtifacts(ctx, fx.tenantID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ArtifactVersion != 2 || rows[1].ArtifactVersion != 1 {
		t.Fatalf("version list wrong: %d rows, first version %d", len(rows), rows[0].ArtifactVersion)
	}
	if !rows[0].Downloadable {
		t.Fatal("the newest artifact must be downloadable")
	}
}

func TestJsonBytesStableForCanonicalHash(t *testing.T) {
	// The assemble payload is content-only; re-marshaling the doc struct must be
	// byte-identical, or canonical_hash would rotate on every export (§7.7 rule 1).
	doc := &reportDocument{
		Schema: reportDocumentSchema, Title: "季度差旅合规检查", Revision: 1,
		TemplateKey: reportComplianceTemplateKey, TemplateVersion: 1,
		Completeness: model.ReportCompletenessFull, Gaps: []reportGapEntry{},
		Sections: []reportDocumentSection{
			{Ord: 0, SectionKey: "background", Title: "background", ContentMD: "内容：background", ContentHash: "hash-background"},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, data2) {
		t.Fatal("marshaling the same doc twice must yield identical bytes")
	}
	// The renderer's own canonical derivation matches the service's hash.
	if reportDocxCanonical(doc) != sha256Hex(string(data)) {
		t.Fatal("renderer canonical must equal the payload hash")
	}
}

// reportArtifactDownloadable is the unit of the download decision and must hold
// its three legs even though the artifact table is insert-only (the triggers make
// the `deleted_at` leg unreachable in practice — it stays because a future policy
// may open soft-delete, and the truth must not fork when it does).
func TestDownloadableFalseOnlyWhenNotReadyOrExpiredOrDeleted(t *testing.T) {
	now := time.Now().UTC()
	ready := &model.ExportArtifact{
		ArtifactStatus: model.ExportArtifactReady,
		ExpiresAt:      now.Add(time.Hour),
	}
	if !reportArtifactDownloadable(ready, now) {
		t.Fatal("ready + live + not deleted must be downloadable")
	}
	for name, tweak := range map[string]func(*model.ExportArtifact){
		"pending": func(a *model.ExportArtifact) { a.ArtifactStatus = model.ExportArtifactPending },
		"failed":  func(a *model.ExportArtifact) { a.ArtifactStatus = model.ExportArtifactFailed },
		"expired": func(a *model.ExportArtifact) { a.ExpiresAt = now.Add(-time.Minute) },
		"deleted": func(a *model.ExportArtifact) {
			a.DeletedAt = &now
			a.DeletedBy, a.DeleteReason = "someone", "royal-purge"
		},
	} {
		copy := *ready
		tweak(&copy)
		if reportArtifactDownloadable(&copy, now) {
			t.Fatalf("a %s artifact must never be downloadable", name)
		}
	}
	if reportArtifactDownloadable(nil, now) {
		t.Fatal("nil artifact must never be downloadable")
	}
}

// The store transaction must be real: three rows arrive together or not at all.
func TestCreateReportExportWritesRowsInOneTransaction(t *testing.T) {
	fx := newReportFixture(t, "txn")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, _ := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}
	var snapshotCount, jobCount, artifactCount int64
	if err := fx.gdb.Model(&model.ExportSnapshot{}).Where("id = ?", result.Job.ExportSnapshotID).Count(&snapshotCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ExportJob{}).Where("id = ?", result.Job.ID).Count(&jobCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.gdb.Model(&model.ExportArtifact{}).Where("id = ?", result.Artifact.ID).Count(&artifactCount).Error; err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 1 || jobCount != 1 || artifactCount != 1 {
		t.Fatalf("snapshot/job/artifact must all exist: %d/%d/%d", snapshotCount, jobCount, artifactCount)
	}
}
