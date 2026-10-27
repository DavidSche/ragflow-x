package router_test

import (
	"context"
	"errors"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"net/http"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_AlertWorklistAndDeliveryLifecycle(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()

	tenant, err := app.svc.CreateTenant(ctx, "alerts-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "alerts-viewer", Password: "secret123", Role: model.RoleViewer,
	}); err != nil {
		t.Fatal(err)
	}
	event := notify.Event{
		ID: "alert-e2e", Title: "provider failed", Severity: "error", Type: "provider_error",
		TenantID: tenant.ID, Resource: "chat", ResourceID: "chat-1", Detail: "upstream unavailable",
		OccurredAt: time.Now().UTC(),
	}
	if err := app.svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.RecordAlertDeliveryPending(ctx, event, "primary-webhook"); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.RecordAlertDeliveryResult(ctx, event, "primary-webhook", 1, nil); err != nil {
		t.Fatal(err)
	}

	viewerToken := loginAs(t, app, "alerts-viewer", "secret123")
	resp, body := app.doAuth(t, http.MethodGet, "/api/v1/alerts?status=open", viewerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer list status %d: %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/alerts/deliveries", viewerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer delivery list status %d: %s", resp.StatusCode, body)
	}

	adminToken := app.login(t)
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/alerts?status=open&scope=specific&tenant_id="+tenant.ID, adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin list status %d: %s", resp.StatusCode, body)
	}
	var list struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := decodeBody(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 1 || len(list.Data.Items) != 1 || list.Data.Items[0].ID != event.ID {
		t.Fatalf("alert list = %+v", list.Data)
	}

	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/alerts/deliveries?status=succeeded&scope=specific&tenant_id="+tenant.ID, adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delivery list status %d: %s", resp.StatusCode, body)
	}
	var deliveries struct {
		Data struct {
			Items []struct {
				AlertEventID string `json:"alert_event_id"`
				Channel      string `json:"channel"`
				Status       string `json:"status"`
				Attempts     int    `json:"attempts"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := decodeBody(body, &deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries.Data.Total != 1 || len(deliveries.Data.Items) != 1 {
		t.Fatalf("delivery list = %+v", deliveries.Data)
	}
	delivery := deliveries.Data.Items[0]
	if delivery.AlertEventID != event.ID || delivery.Channel != "primary-webhook" ||
		delivery.Status != model.AlertDeliveryStatusSucceeded || delivery.Attempts != 1 {
		t.Fatalf("delivery item = %+v", delivery)
	}

	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/alerts/"+event.ID+"/read", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mark read status %d: %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/alerts/"+event.ID+"/claim", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim status %d: %s", resp.StatusCode, body)
	}
	resp, body = app.doAuth(t, http.MethodGet, "/api/v1/alerts?status=claimed&scope=specific&tenant_id="+tenant.ID, adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claimed list status %d: %s", resp.StatusCode, body)
	}
	if err := decodeBody(body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 1 || len(list.Data.Items) != 1 {
		t.Fatalf("claimed list = %+v", list.Data)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP1_NOTIFY_002_ManualDeliveryRetryRouteRequiresAlertManageScope(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	ctx := context.Background()

	tenant, err := app.svc.CreateTenant(ctx, "manual-retry-e2e-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.svc.CreateUser(ctx, tenant.ID, "", service.CreateUserRequest{
		Username: "manual-retry-viewer", Password: "secret123", Role: model.RoleViewer,
	}); err != nil {
		t.Fatal(err)
	}
	app.svc.SetAlertingConfig(config.Alerting{
		Enabled: true, CompensationMaxAttempts: 3,
		CompensationInitialDelaySec: 0, CompensationMaxDelaySec: 0,
		CompensationLeaseSec: 300, CompensationJitterPercent: 0,
	})
	hub := notify.NewHubWithNotifiers([]notify.Notifier{manualRetryNotifier{}}, 0)
	hub.SetSink(app.svc)
	notify.Set(hub)
	t.Cleanup(func() {
		hub.Shutdown()
		notify.Set(nil)
	})

	event := notify.Event{
		ID: "manual-retry-e2e", Title: "manual retry", Severity: "error",
		Type: "provider.compensation.failed", TenantID: tenant.ID,
		Resource: "chat", ResourceID: "chat-1", OccurredAt: time.Now().UTC(),
	}
	if err := app.svc.RecordAlert(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.RecordAlertDeliveryPending(ctx, event, "test"); err != nil {
		t.Fatal(err)
	}
	if err := app.svc.RecordAlertDeliveryResult(ctx, event, "test", 1, &notify.DeliveryError{
		Attempts: 1, Retryable: true, LastError: errors.New("503"),
	}); err != nil {
		t.Fatal(err)
	}

	viewerToken := loginAs(t, app, "manual-retry-viewer", "secret123")
	resp, body := app.doAuth(t, http.MethodPost, "/api/v1/alerts/"+event.ID+"/deliveries/test/retry", viewerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer retry status %d: %s", resp.StatusCode, body)
	}

	adminToken := app.login(t)
	resp, body = app.doAuth(t, http.MethodPost, "/api/v1/alerts/"+event.ID+"/deliveries/test/retry", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin retry status %d: %s", resp.StatusCode, body)
	}
	var retry struct {
		Data struct {
			Summary struct {
				Scanned     int `json:"scanned"`
				Compensated int `json:"compensated"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := decodeBody(body, &retry); err != nil {
		t.Fatal(err)
	}
	if retry.Data.Summary.Scanned != 1 || retry.Data.Summary.Compensated != 1 {
		t.Fatalf("retry response=%+v", retry.Data)
	}

	audits, _, err := app.svc.Store.ListAudits(ctx, tenant.ID, 1, 20, repository.AuditFilter{
		Action: "alert.delivery.retry",
	})
	if err != nil || len(audits) != 1 {
		t.Fatalf("retry audit: audits=%d err=%v", len(audits), err)
	}
}

type manualRetryNotifier struct{}

func (manualRetryNotifier) Send(context.Context, notify.Event) error { return nil }
func (manualRetryNotifier) Name() string                             { return "test" }
