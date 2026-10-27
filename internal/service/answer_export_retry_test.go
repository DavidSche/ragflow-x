package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestExportJobRetryRequiresFailedOwnedJob(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Export Retry Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "export-retry-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := finalizeTestAnswer(t, svc, ctx, tenant.ID, admin.ID, "request-export-retry")
	t.Setenv("RGX_EXPORT_PDF_FONT", filepath.Join(t.TempDir(), "missing-font.ttf"))
	_, _ = svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatPDF,
		IdempotencyKey: "export-retry-key", TraceID: "trace-export-retry",
	})
	created, err := svc.Store.GetExportJobByIdempotencyKey(ctx, tenant.ID, "export-retry-key")
	if err != nil || created == nil || created.Status != model.ExportJobFailed {
		t.Fatalf("expected failed export job: job=%+v err=%v", created, err)
	}
	if _, err := svc.RetryExportJob(ctx, admin.ID, tenant.ID, created.ID, "trace-retry-not-failed"); err == nil {
		t.Fatal("succeeded export must not be retried")
	}
	if _, err := svc.RetryExportJob(ctx, admin.ID, tenant.ID, created.ID, "trace-retry-failed"); err == nil {
		t.Fatal("failed export retry must fail closed without a valid PDF font")
	}
	if err := svc.Store.UpdateExportJob(ctx, tenant.ID, created.ID, map[string]interface{}{
		"status": model.ExportJobFailed, "error_code": "export_render_failed",
	}); err != nil {
		t.Fatal(err)
	}
	pdfFont := filepath.Join(`C:\Windows\Fonts`, "Deng.ttf")
	if _, err := os.Stat(pdfFont); err != nil {
		t.Skip("valid CJK PDF font is unavailable")
	}
	t.Setenv("RGX_EXPORT_PDF_FONT", pdfFont)
	retried, err := svc.RetryExportJob(ctx, admin.ID, tenant.ID, created.ID, "trace-export-retry-2")
	if err != nil || retried.Job.Status != model.ExportJobSucceeded || retried.Artifact == nil {
		t.Fatalf("retry export: job=%+v artifact=%+v err=%v", retried.Job, retried.Artifact, err)
	}
	if retried.Job.TraceID != "trace-export-retry-2" || retried.Job.ErrorCode != "" {
		t.Fatalf("retry did not refresh lifecycle evidence: %+v", retried.Job)
	}
	otherTenant, err := svc.CreateTenant(ctx, "Other Export Retry Tenant")
	if err != nil {
		t.Fatal(err)
	}
	otherAdmin, err := svc.CreateUser(ctx, otherTenant.ID, "", CreateUserRequest{
		Username: "other-retry-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetExportJob(ctx, otherAdmin.ID, otherTenant.ID, created.ID); err == nil {
		t.Fatal("cross-principal export job access must be denied")
	}
}

func TestQueuedExportFailureCreatesOwnedAlert(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Queued Export Failure Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "queued-failure-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: "queued-failure-session", AssistantID: "assistant-1",
		PrincipalID: admin.ID, Question: "queued export failure", RequestID: "request-queued-failure",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Content: strings.Repeat("answer ", 40960),
	})
	if err != nil {
		t.Fatal(err)
	}
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error { return nil })}, 0)
	hub.SetSink(svc)
	notify.Set(hub)
	t.Cleanup(func() {
		hub.Shutdown()
		notify.Set(nil)
	})
	t.Setenv("RGX_EXPORT_PDF_FONT", filepath.Join(t.TempDir(), "missing-font.ttf"))
	result, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatPDF,
		IdempotencyKey: "queued-failure-key", TraceID: "trace-queued-failure",
	})
	if err != nil || result.Job.Status != model.ExportJobQueued {
		t.Fatalf("queued export: job=%+v err=%v", result.Job, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := svc.Store.GetExportJob(ctx, tenant.ID, result.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == model.ExportJobFailed {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	alerts, total, err := svc.ListAlerts(ctx, tenant.ID, false, 1, 20, repository.AlertFilter{
		Type: "export_failed", Search: result.Job.ID,
	})
	if err != nil || total != 1 || len(alerts) != 1 {
		t.Fatalf("export failure alert: total=%d alerts=%+v err=%v", total, alerts, err)
	}
	if alerts[0].TenantID != tenant.ID || alerts[0].Status != model.AlertStatusOpen {
		t.Fatalf("unexpected export failure alert: %+v", alerts[0])
	}
}
