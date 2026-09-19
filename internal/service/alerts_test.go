package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_ProviderCompensationAuditEmitsOperationalAlert(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()

	events := make(chan notify.Event, 1)
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(_ context.Context, event notify.Event) error {
		select {
		case events <- event:
		default:
		}
		return nil
	})}, 0)
	hub.SetSink(svc)
	notify.Set(hub)
	t.Cleanup(func() {
		hub.Shutdown()
		notify.Set(nil)
	})

	if err := svc.RecordAudit(ctx, &model.AuditLog{
		TenantID:   tenantA,
		Action:     "provider.compensation.failed",
		Resource:   "model-provider",
		ResourceID: "provider-1",
		DetailJSON: `{"operation":"update-provider-instance","stage":"restore-local-shadow","error":"upstream timeout"}`,
		Result:     "FAILURE",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-events:
		if event.TenantID != tenantA || event.Type != "provider.compensation.failed" ||
			event.Resource != "model-provider" || event.ResourceID != "provider-1" ||
			event.Severity != "error" {
			t.Fatalf("unexpected alert event: %+v", event)
		}
		if event.Fields["operation"] != "update-provider-instance" || event.Fields["stage"] != "restore-local-shadow" {
			t.Fatalf("unexpected alert fields: %+v", event.Fields)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider compensation audit did not emit an alert within 2s")
	}

	alerts, total, err := svc.ListAlerts(ctx, tenantA, false, 1, 20, repository.AlertFilter{
		Type: "provider.compensation.failed",
	})
	if err != nil || total != 1 || len(alerts) != 1 {
		t.Fatalf("list alerts: alerts=%d total=%d err=%v", len(alerts), total, err)
	}
	if alerts[0].Status != model.AlertStatusOpen || alerts[0].ResourceID != "provider-1" {
		t.Fatalf("unexpected persisted alert: %+v", alerts[0])
	}
	tenantB, err := svc.CreateTenant(ctx, "compensation-alert-other-tenant")
	if err != nil {
		t.Fatal(err)
	}
	_, otherTotal, err := svc.ListAlerts(ctx, tenantB.ID, false, 1, 20, repository.AlertFilter{
		Type: "provider.compensation.failed",
	})
	if err != nil || otherTotal != 0 {
		t.Fatalf("cross-tenant alert list: total=%d err=%v", otherTotal, err)
	}
}

type testNotifierFunc func(context.Context, notify.Event) error

func (fn testNotifierFunc) Send(ctx context.Context, event notify.Event) error {
	return fn(ctx, event)
}

func (testNotifierFunc) Name() string { return "test" }

func TestRecordAlertAndLifecycle(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	event := notify.Event{
		ID: "alert-1", Title: "quota exhausted", Severity: "warn", Type: "quota_exhausted",
		TenantID: tenantA, Resource: "api-key", ResourceID: "key-1", Detail: "request denied",
		Fields: map[string]string{"scope": "REQUEST_COUNT"}, OccurredAt: time.Now().UTC(),
	}
	if err := svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	items, total, err := svc.ListAlerts(ctx, tenantA, false, 1, 20, repository.AlertFilter{Status: model.AlertStatusOpen})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("list: items=%d total=%d err=%v", len(items), total, err)
	}
	if items[0].FieldsJSON != `{"scope":"REQUEST_COUNT"}` {
		t.Fatalf("fields json = %q", items[0].FieldsJSON)
	}
	if err := svc.UpdateAlertStatus(ctx, tenantA, event.ID, false, repository.AlertReview{Status: model.AlertStatusRead, Actor: "user-a"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateAlertStatus(ctx, tenantA, event.ID, false, repository.AlertReview{Status: model.AlertStatusClaimed, Actor: "user-a"}); err != nil {
		t.Fatal(err)
	}
	items, _, err = svc.ListAlerts(ctx, tenantA, false, 1, 20, repository.AlertFilter{Status: model.AlertStatusClaimed})
	if err != nil || len(items) != 1 || items[0].AckedBy != "user-a" {
		t.Fatalf("claimed: items=%+v err=%v", items, err)
	}
	audits, _, err := svc.ListAudits(ctx, tenantA, 1, 20, repository.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	foundAlertAudit := false
	for _, audit := range audits {
		if audit.Action == "alert.claimed" {
			foundAlertAudit = true
			if audit.TenantID != tenantA {
				t.Fatalf("audit tenant = %s, want %s", audit.TenantID, tenantA)
			}
		}
	}
	if !foundAlertAudit {
		t.Fatal("alert.claimed audit was not recorded")
	}
}

func TestAlertStatusAndCrossTenantScope(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, tenantB, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	if err := svc.RecordAlert(ctx, notify.Event{ID: "alert-a", Type: "provider_error", TenantID: tenantA, Resource: "chat", ResourceID: "chat-a", Title: "failure"}); err != nil {
		t.Fatal(err)
	}
	items, total, err := svc.ListAlerts(ctx, tenantB, false, 1, 20, repository.AlertFilter{})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("cross-tenant list: total=%d items=%v err=%v", total, items, err)
	}
	if err := svc.UpdateAlertStatus(ctx, tenantB, "alert-a", false, repository.AlertReview{Status: model.AlertStatusRead}); err == nil {
		t.Fatalf("cross-tenant update err=%v, want not found", err)
	} else if typed, ok := err.(*httperr.Error); !ok || typed.Status != 404 {
		t.Fatalf("cross-tenant update err=%v, want 404", err)
	}
	if err := svc.UpdateAlertStatus(ctx, tenantB, "alert-a", true, repository.AlertReview{Status: "invalid"}); err == nil {
		t.Fatal("invalid status accepted")
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_WebhookDeliveryLifecyclePersistsAcrossRetries(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, tenantB, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	event := notify.Event{ID: "alert-delivery-lifecycle", TenantID: tenantA, Type: "provider.compensation.failed", Title: "failure"}
	if err := svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}

	if err := svc.RecordAlertDeliveryPending(ctx, event, "primary-webhook"); err != nil {
		t.Fatal(err)
	}
	pending, total, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{
		Status: model.AlertDeliveryStatusPending,
	})
	if err != nil || total != 1 || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatalf("pending delivery: items=%d total=%d err=%v", len(pending), total, err)
	}

	if err := svc.RecordAlertDeliveryResult(ctx, event, "primary-webhook", 3, errors.New("webhook returned 503")); err != nil {
		t.Fatal(err)
	}
	failed, total, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{
		Status: model.AlertDeliveryStatusFailed,
	})
	if err != nil || total != 1 || len(failed) != 1 || failed[0].Attempts != 3 || failed[0].LastError == "" {
		t.Fatalf("failed delivery: items=%d total=%d err=%v", len(failed), total, err)
	}

	if err := svc.RecordAlertDeliveryPending(ctx, event, "primary-webhook"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryResult(ctx, event, "primary-webhook", 1, nil); err != nil {
		t.Fatal(err)
	}
	items, total, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{
		Channel: "primary-webhook",
	})
	if err != nil || total != 1 || len(items) != 1 || items[0].Status != model.AlertDeliveryStatusSucceeded || items[0].Attempts != 4 || items[0].DeliveredAt == nil {
		t.Fatalf("succeeded delivery: items=%d total=%d err=%v", len(items), total, err)
	}

	_, otherTotal, err := svc.ListAlertDeliveries(ctx, tenantB, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || otherTotal != 0 {
		t.Fatalf("cross-tenant delivery list: total=%d err=%v", otherTotal, err)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationWorkerRetriesDueWebhooksWithBackoff(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, ThrottleSec: 1,
		CompensationMaxAttempts: 6, CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
	})
	var calls atomic.Int32
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error {
		calls.Add(1)
		return nil
	})}, 0)
	hub.SetSink(svc)
	notify.Set(hub)
	t.Cleanup(func() { notify.Set(nil) })

	event := notify.Event{ID: "compensation-due", TenantID: tenantA, Type: "provider.compensation.failed"}
	if err := svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryResult(ctx, event, "test", 3, &notify.DeliveryError{
		Attempts: 3, Retryable: true, LastError: errors.New("webhook returned 503"),
	}); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.RetryDueAlertDeliveries(ctx, 10)
	if err != nil || summary.Scanned != 1 || summary.Compensated != 1 || summary.Abandoned != 0 {
		t.Fatalf("first compensation summary=%+v err=%v", summary, err)
	}
	items, total, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || total != 1 || len(items) != 1 || items[0].Status != model.AlertDeliveryStatusSucceeded || items[0].Attempts != 4 || items[0].DeliveredAt == nil {
		t.Fatalf("compensated delivery: items=%+v total=%d err=%v", items, total, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("notifier calls = %d, want 1", got)
	}
	if summary, err = svc.RetryDueAlertDeliveries(ctx, 10); err != nil || summary.Scanned != 0 || summary.Compensated != 0 {
		t.Fatalf("second compensation summary=%+v err=%v", summary, err)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP1_NOTIFY_002_ManualDeliveryRetryIsTenantScopedAndLeaseFenced(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	tenantB, err := svc.CreateTenant(context.Background(), "manual-retry-other-tenant")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
		CompensationLeaseSec: 300, CompensationJitterPercent: 0,
	})
	var calls atomic.Int32
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error {
		calls.Add(1)
		return nil
	})}, 0)
	hub.SetSink(svc)
	notify.Set(hub)
	t.Cleanup(func() {
		hub.Shutdown()
		notify.Set(nil)
	})

	createFailed := func(eventID string) {
		t.Helper()
		event := notify.Event{ID: eventID, TenantID: tenantA, Type: "provider.compensation.failed"}
		if err := svc.RecordAlert(ctx, event); err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordAlertDeliveryResult(ctx, event, "test", 3, &notify.DeliveryError{
			Attempts: 3, Retryable: true, LastError: errors.New("503"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	createFailed("manual-retry-failed")
	createFailed("manual-retry-abandoned")

	summary, err := svc.RetryDueAlertDeliveries(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Abandoned != 2 {
		t.Fatalf("expected both over-budget rows to abandon, summary=%+v", summary)
	}

	summary, err = svc.RetryAlertDelivery(ctx, tenantA, false, "manual-retry-failed", "test", "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Scanned != 1 || summary.Compensated != 1 || summary.Skipped != 0 {
		t.Fatalf("manual failed retry summary=%+v", summary)
	}
	items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(items) != 2 {
		t.Fatalf("load retried deliveries: items=%d err=%v", len(items), err)
	}

	if _, err = svc.RetryAlertDelivery(ctx, tenantB.ID, false, "manual-retry-failed", "test", "operator-1"); err == nil {
		t.Fatal("cross-tenant retry must fail")
	} else if typed, ok := err.(*httperr.Error); !ok || typed.Status != 404 {
		t.Fatalf("cross-tenant retry err=%v, want 404", err)
	}

	_, err = svc.RetryAlertDelivery(ctx, tenantA, false, "manual-retry-failed", "test", "operator-1")
	if typed, ok := err.(*httperr.Error); !ok || typed.Status != 400 || typed.Code != 42973 {
		t.Fatalf("succeeded retry err=%v, want 400/42973", err)
	}

	summary, err = svc.RetryAlertDelivery(ctx, tenantA, false, "manual-retry-abandoned", "test", "operator-2")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Compensated != 1 {
		t.Fatalf("manual abandoned retry summary=%+v", summary)
	}
	abandoned, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(abandoned) != 2 {
		t.Fatalf("load final deliveries: items=%d err=%v", len(abandoned), err)
	}
	for _, item := range abandoned {
		if item.Status != model.AlertDeliveryStatusSucceeded || item.DeliveredAt == nil {
			t.Fatalf("manual retry did not converge: %+v", item)
		}
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationWorkerHonorsMaximumAndTerminalFailures(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{CompensationMaxAttempts: 3})
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
	})
	var calls atomic.Int32
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error {
		calls.Add(1)
		return nil
	})}, 0)
	notify.Set(hub)
	t.Cleanup(func() { notify.Set(nil) })
	hub.SetSink(svc)

	retryable := notify.Event{ID: "compensation-max", TenantID: tenantA, Type: "provider.compensation.failed"}
	terminal := notify.Event{ID: "compensation-terminal", TenantID: tenantA, Type: "provider.compensation.failed"}
	for _, event := range []notify.Event{retryable, terminal} {
		if err := svc.RecordAlert(ctx, event); err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.RecordAlertDeliveryResult(ctx, retryable, "test", 3, &notify.DeliveryError{Attempts: 3, Retryable: true, LastError: errors.New("503")}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryResult(ctx, terminal, "test", 1, &notify.DeliveryError{Attempts: 1, Retryable: false, LastError: errors.New("400")}); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.RetryDueAlertDeliveries(ctx, 10)
	if err != nil || summary.Scanned != 1 || summary.Compensated != 0 || summary.Abandoned != 1 {
		t.Fatalf("maximum compensation summary=%+v err=%v", summary, err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("notifier calls = %d, want 0", got)
	}
	items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(items) != 2 {
		t.Fatalf("delivery rows: items=%+v err=%v", items, err)
	}
	byID := map[string]model.AlertDelivery{}
	for _, item := range items {
		byID[item.AlertEventID] = item
	}
	if byID[retryable.ID].Status != model.AlertDeliveryStatusAbandoned || byID[retryable.ID].NextRetryAt != nil ||
		byID[retryable.ID].LeaseOwner != "" || byID[retryable.ID].LeaseExpiresAt != nil {
		t.Fatalf("retryable delivery was not abandoned: %+v", byID[retryable.ID])
	}
	if byID[terminal.ID].Status != model.AlertDeliveryStatusFailed || byID[terminal.ID].NextRetryAt != nil {
		t.Fatalf("terminal delivery should not be compensable: %+v", byID[terminal.ID])
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_DurableCompensationJobRunsAndReschedules(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		CompensationIntervalSec: 1, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
	})
	runner := svc.SetupWorker(fastWorkerConfig())
	runner.Start(ctx)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runner.Stop(stopCtx)
	})

	queued, err := svc.ScheduleAlertDeliveryCompensation(ctx, "")
	if err != nil || !queued {
		t.Fatalf("schedule compensation job: queued=%v err=%v", queued, err)
	}
	queuedAgain, err := svc.ScheduleAlertDeliveryCompensation(ctx, "")
	if err != nil || queuedAgain {
		t.Fatalf("schedule duplicate compensation job: queued=%v err=%v", queuedAgain, err)
	}
	job := waitJobStatus(t, svc.Store, SystemTenantID, model.JobKindAlertDeliveryCompensation, model.JobStatusSucceeded, 3*time.Second)
	if job == nil {
		t.Fatal("compensation job did not run")
	}
	active, err := svc.Store.CountActiveJobs(ctx, model.JobKindAlertDeliveryCompensation, SystemTenantID)
	if err != nil || active != 1 {
		t.Fatalf("rescheduled active jobs=%d err=%v", active, err)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationRespectsActiveLease(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
		CompensationLeaseSec: 300, CompensationJitterPercent: 0,
	})
	var calls atomic.Int32
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error {
		calls.Add(1)
		return nil
	})}, 0)
	notify.Set(hub)
	t.Cleanup(func() { notify.Set(nil) })
	hub.SetSink(svc)

	event := notify.Event{ID: "compensation-lease", TenantID: tenantA, Type: "provider.compensation.failed"}
	if err := svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryResult(ctx, event, "test", 1, &notify.DeliveryError{Attempts: 1, Retryable: true, LastError: errors.New("503")}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("load delivery: items=%d err=%v", len(items), err)
	}
	leased := items[0]
	leaseUntil := now.Add(5 * time.Minute)
	leased.LeaseOwner = "other-worker"
	leased.LeaseExpiresAt = &leaseUntil
	leased.NextRetryAt = &now
	if err := svc.Store.UpsertAlertDelivery(ctx, &leased); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.RetryDueAlertDeliveries(ctx, 10)
	if err != nil || summary.Scanned != 1 || summary.Compensated != 0 || summary.Skipped != 1 {
		t.Fatalf("leased compensation summary=%+v err=%v", summary, err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("notifier calls = %d, want 0", got)
	}
	items, _, err = svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(items) != 1 || items[0].LeaseOwner != "other-worker" || items[0].Status != model.AlertDeliveryStatusFailed {
		t.Fatalf("lease changed delivery: items=%+v err=%v", items, err)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationPreservesLeaseUntilFencedResult(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
		CompensationLeaseSec: 300, CompensationJitterPercent: 0,
	})
	var leaseDuringSend *model.AlertDelivery
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error {
		items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
		if err == nil && len(items) == 1 {
			leaseDuringSend = &items[0]
		}
		return nil
	})}, 0)
	notify.Set(hub)
	t.Cleanup(func() { notify.Set(nil) })

	hub.SetSink(svc)

	event := notify.Event{ID: "compensation-fencing", TenantID: tenantA, Type: "provider.compensation.failed"}
	if err := svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryResult(ctx, event, "test", 1, &notify.DeliveryError{Attempts: 1, Retryable: true, LastError: errors.New("503")}); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.RetryDueAlertDeliveries(ctx, 10)
	if err != nil || summary.Scanned != 1 || summary.Compensated != 1 {
		t.Fatalf("compensation summary=%+v err=%v", summary, err)
	}
	if leaseDuringSend == nil || leaseDuringSend.LeaseOwner == "" || leaseDuringSend.LeaseExpiresAt == nil || leaseDuringSend.LeaseGeneration != 1 {
		t.Fatalf("lease not preserved during delivery: %+v", leaseDuringSend)
	}
	items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("load delivery after result: items=%d err=%v", len(items), err)
	}
	if items[0].Status != model.AlertDeliveryStatusSucceeded || items[0].Attempts != 2 ||
		items[0].LeaseOwner != "" || items[0].LeaseExpiresAt != nil || items[0].LeaseGeneration != 1 {
		t.Fatalf("fenced compensation result: %+v", items[0])
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationUsesWebhookRetryAfter(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
		CompensationJitterPercent: 0,
	})
	event := notify.Event{ID: "compensation-retry-after", TenantID: tenantA, Type: "provider.compensation.failed"}
	if err := svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryResult(ctx, event, "test", 1, &notify.DeliveryError{
		Attempts: 1, Retryable: true, RetryAfter: 2 * time.Second, LastError: errors.New("429"),
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(items) != 1 || items[0].NextRetryAt == nil {
		t.Fatalf("retry-after delivery: items=%+v err=%v", items, err)
	}
	if delay := time.Until(*items[0].NextRetryAt); delay < 1800*time.Millisecond {
		t.Fatalf("next retry delay = %v, want about 2s", delay)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationBackoffAppliesConfiguredJitter(t *testing.T) {
	cfg := config.Alerting{CompensationInitialDelaySec: 10, CompensationMaxDelaySec: 10, CompensationJitterPercent: 20}
	base := 10 * time.Second
	for range 100 {
		delay := alertDeliveryRetryDelay(cfg)
		if delay < base || delay > base+(base*20/100) {
			t.Fatalf("jitter delay = %v, want [%v,%v]", delay, base, base+(base*20/100))
		}
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationPassExposesDistributionMetrics(t *testing.T) {
	metrics := obs.New(config.Observability{MetricsEnabled: true, MetricsNamespace: "alertcomp"})
	obs.Set(metrics)
	t.Cleanup(func() { obs.Set(nil) })
	svc := newAuthzSvc(t)
	tenantID, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
		CompensationLeaseSec: 300, CompensationJitterPercent: 0,
	})
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error {
		return nil
	})}, 0)
	notify.Set(hub)
	t.Cleanup(func() { notify.Set(nil) })
	hub.SetSink(svc)

	createDelivery := func(eventID string, attempts int) {
		event := notify.Event{ID: eventID, TenantID: tenantID, Type: "provider.compensation.failed"}
		if err := svc.RecordAlert(ctx, event); err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordAlertDeliveryResult(ctx, event, "test", attempts, &notify.DeliveryError{
			Attempts: attempts, Retryable: true, LastError: errors.New("503"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	createDelivery("compensation-metric-skipped", 1)
	createDelivery("compensation-metric-compensated", 1)
	createDelivery("compensation-metric-abandoned", 3)
	deliveries, _, err := svc.ListAlertDeliveries(ctx, tenantID, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(deliveries) != 3 {
		t.Fatalf("load deliveries: items=%d err=%v", len(deliveries), err)
	}
	for _, delivery := range deliveries {
		if delivery.AlertEventID != "compensation-metric-skipped" {
			continue
		}
		leased := delivery
		leaseUntil := time.Now().UTC().Add(5 * time.Minute)
		leased.LeaseOwner = "other-worker"
		leased.LeaseExpiresAt = &leaseUntil
		if err := svc.Store.UpsertAlertDelivery(ctx, &leased); err != nil {
			t.Fatal(err)
		}
	}

	svc.SetupWorker(fastWorkerConfig())

	err = (&alertDeliveryCompensationWorker{svc: svc}).Run(ctx, &model.Job{
		ID: "compensation-metrics-job", Kind: model.JobKindAlertDeliveryCompensation,
	})
	if err != nil {
		t.Fatalf("compensation worker: %v", err)
	}
	router := gin.New()
	router.GET("/metrics", metrics.MetricsHandler())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	for _, metric := range []string{
		"alertcomp_alert_delivery_compensation_scanned_total 3",
		"alertcomp_alert_delivery_compensation_compensated_total 1",
		"alertcomp_alert_delivery_compensation_skipped_total 1",
		"alertcomp_alert_delivery_compensation_abandoned_total 1",
		"alertcomp_alert_delivery_compensation_fenced_total 0",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics output missing %q: %s", metric, body)
		}
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_CompensationReportsFencedLeaseResult(t *testing.T) {
	svc := newAuthzSvc(t)
	tenantA, _, _, _ := setupApprovalEnv(t, svc)
	ctx := context.Background()
	svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
		CompensationLeaseSec: 300, CompensationJitterPercent: 0,
	})
	hub := notify.NewHubWithNotifiers([]notify.Notifier{testNotifierFunc(func(context.Context, notify.Event) error {
		items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
		if err != nil || len(items) != 1 {
			return err
		}
		takenOver := items[0]
		leaseUntil := time.Now().UTC().Add(5 * time.Minute)
		takenOver.LeaseOwner = "new-worker"
		takenOver.LeaseGeneration = 2
		takenOver.LeaseExpiresAt = &leaseUntil
		return svc.Store.UpsertAlertDelivery(ctx, &takenOver)
	})}, 0)
	notify.Set(hub)
	t.Cleanup(func() { notify.Set(nil) })
	hub.SetSink(svc)

	event := notify.Event{ID: "compensation-fenced-metric", TenantID: tenantA, Type: "provider.compensation.failed"}
	if err := svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAlertDeliveryResult(ctx, event, "test", 1, &notify.DeliveryError{Attempts: 1, Retryable: true, LastError: errors.New("503")}); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.RetryDueAlertDeliveries(ctx, 10)
	if err != nil {
		t.Fatalf("fenced compensation: %v", err)
	}
	if summary.Scanned != 1 || summary.Compensated != 0 || summary.Skipped != 1 || summary.Fenced != 1 || summary.Abandoned != 0 {
		t.Fatalf("fenced summary=%+v", summary)
	}
	items, _, err := svc.ListAlertDeliveries(ctx, tenantA, false, 1, 20, repository.AlertDeliveryFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("load fenced delivery: items=%d err=%v", len(items), err)
	}
	if items[0].LeaseOwner != "new-worker" || items[0].LeaseGeneration != 2 || items[0].Status != model.AlertDeliveryStatusFailed {
		t.Fatalf("new owner was overwritten: %+v", items[0])
	}
}
