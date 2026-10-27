package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// ScenarioID: SC-OBS-T3-003
// T3.3 归因防火墙观测：allowed/warn/block 三种决策都被计数并暴露，供
// confident-wrong A/B 观测期（T3.4）在 alert 侧看到无核实引用的具体断言流量。
func TestAttributionFirewallMetric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	o := New(config.Observability{MetricsEnabled: true, MetricsNamespace: "testobst3"})
	o.RecordAttributionFirewall("allowed")
	o.RecordAttributionFirewall("warn")
	o.RecordAttributionFirewall("block")
	o.RecordAttributionFirewall("warn")

	r := gin.New()
	r.GET("/metrics", o.MetricsHandler())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "testobst3_answer_attribution_firewall_total") {
		t.Fatalf("expected attribution firewall metric in /metrics output, got:\n%s", body)
	}
	if !strings.Contains(body, `verdict="warn"} 2`) || !strings.Contains(body, `verdict="allowed"} 1`) ||
		!strings.Contains(body, `verdict="block"} 1`) {
		t.Fatalf("attribution firewall counters wrong:\n%s", body)
	}
}
