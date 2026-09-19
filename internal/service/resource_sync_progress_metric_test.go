package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// ScenarioID: SC-OBS-001
func TestP0_OBS_004_ResourceSyncImportClearsProgressMetric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := obs.New(config.Observability{MetricsEnabled: true, MetricsNamespace: "syncservice"})
	obs.Set(metrics)
	t.Cleanup(func() { obs.Set(nil) })

	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Progress Metric")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "progress-metric"}); err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("successful import expected, got %+v err=%v", run, err)
	}

	router := gin.New()
	router.GET("/metrics", metrics.MetricsHandler())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(response.Body.String(), "resource_sync_run_progress") {
		t.Fatalf("terminal run must clear progress metric: %s", response.Body.String())
	}
}
