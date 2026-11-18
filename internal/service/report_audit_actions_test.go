package service

// Report audit trail (doc/288 §11.3).
//
// The design lists six actions a tenant admin must be able to find in the audit
// chain. Three of them — `report.section.failed`, `report.artifact.exported`,
// `report.artifact.downloaded` — were described but never written: the section
// failure only reached the job event stream (purged by its own clock), and the
// artifact actions only reached `rgx_export_download_audit` (a counter ledger with
// no actor trail). A reader auditing "what happened to this report" therefore saw
// a hole exactly where the interesting things happened.
//
// These tests assert the three rows exist, carry the identifiers an auditor needs,
// and — for the section failure — land in the same transaction as the failed
// section, so a process death cannot leave a failed section nobody can explain.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func reportAuditDetail(t *testing.T, detail string) map[string]interface{} {
	t.Helper()
	decoded := map[string]interface{}{}
	if err := json.Unmarshal([]byte(detail), &decoded); err != nil {
		t.Fatalf("audit detail must be readable json: %v (%q)", err, detail)
	}
	return decoded
}

// TestAFailedSectionWritesItsOwnAuditRow: the section failure must be readable
// from the audit chain, with the identity of the section (not just the job).
func TestAFailedSectionWritesItsOwnAuditRow(t *testing.T) {
	fx, engine := newReportRunFixture(t, "audit-section-failed")
	ctx := context.Background()
	created := fx.outlineJob(t, fx.datasetLink, "req-audit-section-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	// A business refusal is not retried (§7.9), so the very first answer closes the
	// section — one failed section, one audit row.
	engine.mu.Lock()
	engine.replies = []string{
		`{"answer":"本次评估覆盖差旅报销流程。","usage":{"prompt_tokens":40,"completion_tokens":10}}`,
		`{"answer":"","usage":{"prompt_tokens":40,"completion_tokens":0}}`,
	}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)

	var logs []model.AuditLog
	if err := fx.gdb.Where("tenant_id = ? AND action = ?", fx.tenantID, "report.section.failed").
		Order("seq ASC").Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("a section that ended failed must leave a report.section.failed audit row")
	}
	row := logs[0]
	if row.Resource != "report" || row.ResourceID != created.Report.ID {
		t.Fatalf("the audit row must name the report, got resource=%q id=%q",
			row.Resource, row.ResourceID)
	}
	if row.Result != "FAILURE" {
		t.Fatalf("a failed section must not be audited as success, got %q", row.Result)
	}
	detail := reportAuditDetail(t, row.DetailJSON)
	for _, field := range []string{"report_job_id", "revision_id", "section_key", "section_ord", "attempt", "error_code"} {
		if _, ok := detail[field]; !ok {
			t.Fatalf("audit detail must carry %q, got %v", field, detail)
		}
	}
	if detail["report_job_id"] != created.Job.ID {
		t.Fatalf("audit must name the job it belongs to, got %v", detail["report_job_id"])
	}

	// One row per failed section, not one per attempt: the runner retries transient
	// failures, and an audit chain that grows with retries cannot answer "which
	// sections failed" without deduplication.
	var failedSections int64
	if err := fx.gdb.Model(&model.ReportSection{}).
		Where("tenant_id = ? AND revision_id = ? AND status = ?",
			fx.tenantID, created.Revision.ID, model.ReportSectionFailed).Count(&failedSections).Error; err != nil {
		t.Fatal(err)
	}
	if int64(len(logs)) != failedSections {
		t.Fatalf("%d failed sections must produce %d audit rows, got %d",
			failedSections, failedSections, len(logs))
	}
}

// TestExportAndDownloadLeaveActorAuditRows: producing and taking a file are two
// different actions, and both must be attributable to the actor who asked.
func TestExportAndDownloadLeaveActorAuditRows(t *testing.T) {
	fx := newReportFixture(t, "audit-artifact")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := fx.svc.OpenReportArtifact(ctx, fx.owner.ID, fx.tenantID,
		report.ID, revision.Revision, result.Artifact.ID); err != nil {
		t.Fatalf("download: %v", err)
	}

	exported := latestAudit(t, fx, "report.artifact.exported")
	if exported.UserID != fx.owner.ID {
		t.Fatalf("the export audit must name the actor, got %q", exported.UserID)
	}
	if exported.Result != "SUCCESS" {
		t.Fatalf("a ready artifact must be audited as success, got %q", exported.Result)
	}
	exportDetail := reportAuditDetail(t, exported.DetailJSON)
	if exportDetail["sha256"] != result.Artifact.SHA256 {
		t.Fatalf("the audit must name the bytes that were delivered: %v vs %q",
			exportDetail["sha256"], result.Artifact.SHA256)
	}
	if exportDetail["artifact_version"] != float64(result.Artifact.ArtifactVersion) {
		t.Fatalf("the audit must name the version, got %v", exportDetail["artifact_version"])
	}

	downloaded := latestAudit(t, fx, "report.artifact.downloaded")
	if downloaded.UserID != fx.owner.ID {
		t.Fatalf("the download audit must name the actor, got %q", downloaded.UserID)
	}
	downloadDetail := reportAuditDetail(t, downloaded.DetailJSON)
	if downloadDetail["artifact_id"] != result.Artifact.ID {
		t.Fatalf("download audit names the wrong artifact: %v", downloadDetail["artifact_id"])
	}
	if downloadDetail["revision_id"] != revision.ID {
		t.Fatalf("download audit must name the revision, got %v", downloadDetail["revision_id"])
	}

	// The download trail keeps its own row: the two ledgers answer different
	// questions and neither is derived from the other.
	var trail int64
	if err := fx.gdb.Model(&model.ExportDownloadAudit{}).
		Where("tenant_id = ? AND export_artifact_id = ?", fx.tenantID, result.Artifact.ID).
		Count(&trail).Error; err != nil {
		t.Fatal(err)
	}
	if trail != 1 {
		t.Fatalf("one download must append one trail row, got %d", trail)
	}
}

// TestAFailedExportIsAuditedAsAFailure: a render the service could not store still
// consumed a version (A40), so it must also be visible in the audit chain with the
// reason — otherwise the only trace of a burned version is a zero-byte file.
func TestAFailedExportIsAuditedAsAFailure(t *testing.T) {
	fx := newReportFixture(t, "audit-artifact-failed")
	ctx := context.Background()
	revision := publishReport(t, fx, successSeeds())
	report, err := fx.svc.Store.GetReport(ctx, fx.tenantID, revision.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	// A regular file where the exports directory must go: MkdirAll fails.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.svc.DataDir = blocked

	if _, err := fx.svc.CreateReportExport(ctx, fx.owner.ID, fx.tenantID, ReportExportRequest{
		ReportID: report.ID, Revision: revision.Revision, Format: model.ExportFormatDOCX,
	}); err != nil {
		t.Fatalf("a failed render is a delivery, not a transport error: %v", err)
	}

	exported := latestAudit(t, fx, "report.artifact.exported")
	if exported.Result != "FAILURE" {
		t.Fatalf("a failed render must be audited as failure, got %q", exported.Result)
	}
	detail := reportAuditDetail(t, exported.DetailJSON)
	if detail["error_code"] == "" {
		t.Fatalf("a failed export must name its reason, got %v", detail)
	}
	if detail["artifact_status"] != model.ExportArtifactFailed {
		t.Fatalf("the audit must record the artifact status, got %v", detail["artifact_status"])
	}
}

func latestAudit(t *testing.T, fx *reportFixture, action string) model.AuditLog {
	t.Helper()
	var row model.AuditLog
	if err := fx.gdb.Where("tenant_id = ? AND action = ?", fx.tenantID, action).
		Order("seq DESC").First(&row).Error; err != nil {
		t.Fatalf("no %s audit row: %v", action, err)
	}
	return row
}
