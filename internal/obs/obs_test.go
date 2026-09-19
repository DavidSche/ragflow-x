package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// ScenarioID: SC-OBS-001
func TestMetricsExposedEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	o := New(config.Observability{MetricsEnabled: true, MetricsNamespace: "testobs"})

	r := gin.New()
	r.Use(o.Middleware())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/metrics", o.MetricsHandler())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "testobs_http_requests_total") {
		t.Fatal("expected http_requests_total metric in /metrics output")
	}
}

// ScenarioID: SC-OBS-001
func TestMetricsDoNotProjectRequestSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	o := New(config.Observability{MetricsEnabled: true, MetricsNamespace: "secretobs"})
	router := gin.New()
	router.Use(o.Middleware())
	router.GET("/authorized", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/metrics", o.MetricsHandler())

	request := httptest.NewRequest(http.MethodGet, "/authorized", nil)
	request.Header.Set("Authorization", "Bearer super-secret-value")
	first := httptest.NewRecorder()
	router.ServeHTTP(first, request)

	metrics := httptest.NewRecorder()
	router.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metrics.Body.String()
	if !strings.Contains(body, "secretobs_http_requests_total") {
		t.Fatal("expected http request metric")
	}
	if strings.Contains(body, "super-secret-value") || strings.Contains(body, "Bearer") {
		t.Fatalf("metrics projected request credentials: %s", body)
	}
}

func TestUnconfiguredNoOpDoesNotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	o := New(config.Observability{}) // metrics disabled + tracing off
	o.AddAlertDeliveryCompensation(1, 1, 1, 1, 1)
	r := gin.New()
	r.Use(o.Middleware())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_AUDIT_002_AlertDeliveryCompensationMetricsAreExposed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	o := New(config.Observability{MetricsEnabled: true, MetricsNamespace: "alertcomp"})
	o.AddAlertDeliveryCompensation(3, 1, 1, 1, 2)

	router := gin.New()
	router.GET("/metrics", o.MetricsHandler())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	for _, metric := range []string{
		"alertcomp_alert_delivery_compensation_scanned_total 3",
		"alertcomp_alert_delivery_compensation_compensated_total 1",
		"alertcomp_alert_delivery_compensation_skipped_total 1",
		"alertcomp_alert_delivery_compensation_abandoned_total 1",
		"alertcomp_alert_delivery_compensation_fenced_total 2",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics output missing %q: %s", metric, body)
		}
	}
}

// ScenarioID: SC-OBS-001
func TestP0_OBS_002_ResourceSyncOperationalMetricsAreExposed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	o := New(config.Observability{MetricsEnabled: true, MetricsNamespace: "resourcesync"})
	o.SetResourceSyncRunProgress("ragflow_global", 10, 8, 2)
	o.AddResourceSyncItems("ragflow_global", "dataset", "failed", 2)
	o.AddResourceSyncItems("ragflow_global", "chat", "succeeded", 6)
	o.AddResourceSyncStaleRuns("ragflow_global", 1)

	router := gin.New()
	router.GET("/metrics", o.MetricsHandler())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	for _, metric := range []string{
		`resourcesync_resource_sync_run_progress{kind="done",source_id="ragflow_global"} 8`,
		`resourcesync_resource_sync_run_progress{kind="failed",source_id="ragflow_global"} 2`,
		`resourcesync_resource_sync_run_progress{kind="total",source_id="ragflow_global"} 10`,
		`resourcesync_resource_sync_items_total{resource_type="dataset",source_id="ragflow_global",status="failed"} 2`,
		`resourcesync_resource_sync_items_total{resource_type="chat",source_id="ragflow_global",status="succeeded"} 6`,
		`resourcesync_resource_sync_stale_runs_total{source_id="ragflow_global"} 1`,
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics output missing %q: %s", metric, body)
		}
	}
}

// ScenarioID: SC-OBS-001
func TestP0_OBS_004_ResourceSyncProgressClearsAfterRun(t *testing.T) {
	o := New(config.Observability{MetricsEnabled: true, MetricsNamespace: "syncdone"})
	o.SetResourceSyncRunProgress("ragflow_global", 10, 8, 2)
	o.ClearResourceSyncRunProgress("ragflow_global")

	router := gin.New()
	router.GET("/metrics", o.MetricsHandler())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(w.Body.String(), "resource_sync_run_progress") {
		t.Fatalf("completed run progress must not remain active: %s", w.Body.String())
	}
}
