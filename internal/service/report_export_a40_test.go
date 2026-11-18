package service

// Artifact version lifecycle (doc/288 §7.7, A40).
//
// The artifact table is insert-only — the answer-delivery immutability anchors raise
// on any UPDATE — so this path is deliberately *not* §7.7's async
// `pending → ready` backfill. The synchronous render writes one row whose status is
// already final, and a failed render writes `failed` with the version number it
// consumed. Three consequences are asserted here, because each of them is a place
// where "convenience" would quietly rewrite history:
//
//  1. a failed render still consumes its version (`v1`), so a later attempt cannot
//     reuse the number and claim it was the first;
//  2. replaying the same request returns the same `failed` artifact — it does not
//     silently re-render, and it does not mint `v2` for identical content;
//  3. one failed format does not block the other formats of the same revision.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestFailedReportExportConsumesItsVersionAndReplaysAsFailed drives the render
// failure through the persistence leg — a `DataDir` that cannot contain an
// `exports/` directory — because that is the one failure the synchronous path can
// hit with no font or fixture trickery.
func TestFailedReportExportConsumesItsVersionAndReplaysAsFailed(t *testing.T) {
	fx := newReportFixture(t, "a40-failed")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatal(err)
	}

	// A regular file where the exports directory must go: MkdirAll fails, the render
	// is fine, and the delivery cannot be stored.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	healthy := fx.svc.DataDir
	fx.svc.DataDir = blocked

	request := ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	}
	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, request)
	if err != nil {
		t.Fatalf("a failed render is a delivery, not a transport error: %v", err)
	}
	if result.Artifact.ArtifactStatus != model.ExportArtifactFailed {
		t.Fatalf("an unstorable export must be recorded as failed, got %q", result.Artifact.ArtifactStatus)
	}
	if result.Job.Status != model.ExportJobFailed || result.Job.ErrorCode != "export_persist_failed" {
		t.Fatalf("the job must name why it failed: %+v", result.Job)
	}
	// The version is consumed, not reserved-and-released (A40). `v1` is spent.
	if result.Artifact.ArtifactVersion != 1 {
		t.Fatalf("the first attempt owns version 1, got %d", result.Artifact.ArtifactVersion)
	}
	if result.Artifact.ByteSize != 0 || result.Artifact.SHA256 != "" {
		t.Fatalf("a failed artifact must not claim bytes it never wrote: %+v", result.Artifact)
	}
	if result.Downloadable {
		t.Fatal("a failed artifact is never downloadable")
	}
	if _, err := os.Stat(result.Artifact.FileRef); err == nil {
		t.Fatalf("a failed render must not leave a file at %q", result.Artifact.FileRef)
	}

	// Replay with the export path healthy again: the frozen content is identical, so
	// this is the same idempotency key and the answer is the failure that happened —
	// not a second attempt and not a second version.
	fx.svc.DataDir = healthy
	replay, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, request)
	if err != nil {
		t.Fatalf("replay must answer about the delivery as it stands: %v", err)
	}
	if replay.Artifact.ID != result.Artifact.ID || replay.Artifact.ArtifactVersion != 1 {
		t.Fatalf("replay must return the same failed artifact, got %q v%d (want %q v1)",
			replay.Artifact.ID, replay.Artifact.ArtifactVersion, result.Artifact.ID)
	}
	if replay.Artifact.ArtifactStatus != model.ExportArtifactFailed {
		t.Fatal("replay must not upgrade a failed artifact to ready")
	}
	var docxArtifacts int64
	if err := fx.gdb.Model(&model.ExportArtifact{}).
		Where("report_revision_id = ? AND format = ?", revision.ID, model.ExportFormatDOCX).
		Count(&docxArtifacts).Error; err != nil {
		t.Fatal(err)
	}
	if docxArtifacts != 1 {
		t.Fatalf("replay must not write a second artifact row, got %d", docxArtifacts)
	}

	// The failure is per format: Markdown is a different version counter and must
	// still deliver. A broken DOCX must not take the whole delivery surface down.
	markdown, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("markdown export must not be blocked by a failed docx: %v", err)
	}
	if markdown.Artifact.ArtifactStatus != model.ExportArtifactReady || markdown.Artifact.ArtifactVersion != 1 {
		t.Fatalf("markdown must be ready at its own v1, got %q v%d",
			markdown.Artifact.ArtifactStatus, markdown.Artifact.ArtifactVersion)
	}
	if !markdown.Downloadable {
		t.Fatal("a ready markdown artifact must be downloadable")
	}
	// And it is genuinely openable, i.e. the healthy path really did write bytes.
	if _, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, markdown.Artifact.ID); err != nil {
		t.Fatalf("the delivered markdown must open: %v", err)
	}
}

// TestFailedArtifactStillOccupiesItsVersionNumber is the allocator half of A40: the
// next request for the same `(revision, format)` must see the failed row's number as
// taken. A "failed renders are free" shortcut would hand out `v1` twice, and the
// second delivery would silently look like the first.
func TestFailedArtifactStillOccupiesItsVersionNumber(t *testing.T) {
	fx := newReportFixture(t, "a40-version")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatal(err)
	}

	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	healthy := fx.svc.DataDir
	fx.svc.DataDir = blocked
	failed, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatalf("create failed export: %v", err)
	}
	if failed.Artifact.ArtifactVersion != 1 {
		t.Fatalf("first attempt must be v1, got %d", failed.Artifact.ArtifactVersion)
	}
	fx.svc.DataDir = healthy

	// A different content hash for the same revision+format: the shape a later
	// attempt takes when the frozen document changed (partial → published, or a
	// superseding revision re-exported). Modelled at the store because the service
	// deliberately has no second content for one published revision.
	now := time.Now().UTC()
	next, err := fx.svc.Store.CreateReportExport(ctx, reportExportRowsForTest(fx, revision, "a40-second", now))
	if err != nil {
		t.Fatalf("allocate the next version: %v", err)
	}
	if next.ArtifactVersion != 2 {
		t.Fatalf("the failed artifact still owns v1, so the next one is v2 — got v%d",
			next.ArtifactVersion)
	}
	if failed.Artifact.ArtifactVersion != 1 {
		t.Fatal("the failed row's number must not move")
	}
}

// reportExportRowsForTest builds a second write set for the same revision+format,
// the way the service would if the frozen document had changed. It exists so the
// version allocation can be exercised without inventing a published revision whose
// content differs from the one just exported.
func reportExportRowsForTest(
	fx *reportFixture, revision *model.ReportRevision, suffix string, now time.Time,
) repository.ReportExportRows {
	snapshotID, jobID := id.New(), id.New()
	return repository.ReportExportRows{
		TenantID:   fx.tenantID,
		RevisionID: revision.ID,
		Snapshot: &model.ExportSnapshot{
			ID: snapshotID, TenantID: fx.tenantID,
			SubjectType:       model.ExportSubjectReport,
			ReportRevisionID:  revision.ID,
			AnswerSnapshotIDs: "[]",
			TemplateID:        "tpl", TemplateVersion: 1,
			PolicyVersion: reportExportPolicyVersion, PolicyEvaluatedAt: now,
			PolicyInputHash: "policy-" + suffix, CanonicalHash: "canonical-" + suffix,
			PayloadJSON: "{}", CreatedAt: now,
		},
		Job: &model.ExportJob{
			ID: jobID, TenantID: fx.tenantID,
			ExportSnapshotID: snapshotID,
			SubjectType:      model.ExportSubjectReport,
			ReportRevisionID: revision.ID,
			RequestedBy:      fx.owner.ID,
			Format:           model.ExportFormatDOCX,
			Status:           model.ExportJobSucceeded,
			TemplateVersion:  1,
			PolicyVersion:    reportExportPolicyVersion, PolicyEvaluatedAt: now,
			PolicyInputHash: "policy-" + suffix,
			IdempotencyKey:  "key-" + suffix,
			CreatedAt:       now, CompletedAt: &now,
		},
		Artifact: &model.ExportArtifact{
			ID: id.New(), TenantID: fx.tenantID,
			ExportJobID: jobID, SubjectType: model.ExportSubjectReport,
			ReportRevisionID: revision.ID,
			ArtifactStatus:   model.ExportArtifactReady,
			Format:           model.ExportFormatDOCX,
			RendererVersion:  reportExportRendererVersion,
			FileRef:          "unused-" + suffix, Filename: "report-" + suffix + ".docx",
			ByteSize: 1, SHA256: "sha-" + suffix,
			CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedBy: fx.owner.ID,
		},
	}
}

// TestOpenReportArtifactRefusesAFailedArtifact pins the download gate for the state
// this path actually produces: `failed` is not "still generating", so it answers
// 404/44109 rather than 409/44104 — there is nothing to wait for, and the row must
// not double as an existence oracle.
func TestOpenReportArtifactRefusesAFailedArtifact(t *testing.T) {
	fx := newReportFixture(t, "a40-open")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.svc.DataDir = blocked
	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, result.Artifact.ID)
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) || httpErr.Status != 404 || httpErr.Code != codeReportAccessDenied {
		t.Fatalf("a failed artifact must answer 404/44109, got %v", err)
	}
}
