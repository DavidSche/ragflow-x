package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func createExportRetentionFixture(t *testing.T, svc *Service, tenantID, suffix string, expiresAt time.Time) *model.ExportArtifact {
	t.Helper()
	job := &model.ExportJob{
		ID: "job-" + tenantID + suffix, TenantID: tenantID, ExportSnapshotID: "export-snapshot",
		AnswerSnapshotID: "answer-snapshot", RequestedBy: "admin", Format: model.ExportFormatPDF,
		Status: model.ExportJobSucceeded, IdempotencyKey: "key-" + tenantID + suffix,
		CreatedAt: time.Now().UTC(),
	}
	if err := os.MkdirAll(filepath.Join(svc.DataDir, "exports", tenantID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateExportJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(svc.DataDir, "exports", tenantID, job.ID+".pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &model.ExportArtifact{
		ID: "artifact-" + tenantID, TenantID: tenantID, ProjectID: "project",
		ExportJobID: job.ID, Format: model.ExportFormatPDF, RendererVersion: "export.renderer.v1",
		FileRef: path, Filename: "answer.pdf", MimeType: "application/pdf",
		ByteSize: 10, SHA256: "hash", CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt,
	}
}

func TestRetentionRemovesExpiredExportArtifactsAndMarksJobsExpired(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.DataDir = t.TempDir()
	tenant, err := svc.CreateTenant(ctx, "Export Artifact Tenant")
	if err != nil {
		t.Fatal(err)
	}
	expired := createExportRetentionFixture(t, svc, tenant.ID, "-expired", time.Now().UTC().Add(-time.Hour))
	if err := svc.Store.CreateExportArtifact(ctx, expired); err != nil {
		t.Fatal(err)
	}
	fresh := createExportRetentionFixture(t, svc, tenant.ID, "-fresh", time.Now().UTC().Add(time.Hour))
	fresh.ID = "artifact-fresh"
	fresh.ExportJobID = "job-fresh"
	if err := svc.Store.CreateExportArtifact(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	svc.SetRetentionPolicy(config.Retention{Enabled: true, IntervalSec: 60})

	summary, err := svc.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ExportArtifacts[tenant.ID] != 1 {
		t.Fatalf("expected one expired artifact purged, got %+v", summary.ExportArtifacts)
	}
	if _, err := os.Stat(expired.FileRef); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired artifact file must be removed: err=%v", err)
	}
	if _, err := os.Stat(fresh.FileRef); err != nil {
		t.Fatalf("fresh artifact file must remain: %v", err)
	}
	retained, err := svc.Store.GetExportArtifact(ctx, tenant.ID, expired.ID)
	if err != nil || retained == nil {
		t.Fatalf("immutable expired artifact record must remain: artifact=%+v err=%v", retained, err)
	}
	if _, err := svc.Store.GetExportArtifact(ctx, tenant.ID, fresh.ID); err != nil {
		t.Fatalf("fresh artifact row must remain: %v", err)
	}
	job, err := svc.Store.GetExportJob(ctx, tenant.ID, expired.ExportJobID)
	if err != nil || job.Status != model.ExportJobExpired {
		t.Fatalf("expired job status: job=%+v err=%v", job, err)
	}
	repeated, err := svc.RunRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.ExportArtifacts[tenant.ID] != 0 {
		t.Fatalf("already expired artifact must not be processed twice: %+v", repeated.ExportArtifacts)
	}
}

func TestRetentionRejectsExportArtifactOutsideTenantDirectory(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.DataDir = t.TempDir()
	tenant, err := svc.CreateTenant(ctx, "Unsafe Export Artifact Tenant")
	if err != nil {
		t.Fatal(err)
	}
	unsafePath := filepath.Join(svc.DataDir, "unsafe.pdf")
	if err := os.WriteFile(unsafePath, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expired := createExportRetentionFixture(t, svc, tenant.ID, "", time.Now().UTC().Add(-time.Hour))
	expired.FileRef = unsafePath
	if err := svc.Store.CreateExportArtifact(ctx, expired); err != nil {
		t.Fatal(err)
	}
	svc.SetRetentionPolicy(config.Retention{Enabled: true, IntervalSec: 60})
	if _, err := svc.RunRetention(ctx); err == nil {
		t.Fatal("artifact outside tenant export directory must fail closed")
	}
	if _, err := os.Stat(unsafePath); err != nil {
		t.Fatalf("unsafe file must remain after validation failure: %v", err)
	}
	if _, err := svc.Store.GetExportArtifact(ctx, tenant.ID, expired.ID); err != nil {
		t.Fatalf("validated artifact row must remain after failure: %v", err)
	}
}
