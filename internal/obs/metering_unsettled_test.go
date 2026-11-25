package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// TestPublishMeteringUnsettledReplacesVanishedTenants pins the two properties the
// repair depends on (doc/304 §4 E148): a tenant that still owes books is readable at
// its new value, and a tenant that no longer owes anything loses its series instead
// of leaving a stale non-zero page behind. Deleting only the vanished series — as
// opposed to resetting the whole vector — keeps every other tenant readable
// throughout; that part is a property of the code, not something a scrape can see.
func TestPublishMeteringUnsettledReplacesVanishedTenants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	o := New(config.Observability{MetricsEnabled: true, MetricsNamespace: "testobse148"})

	o.PublishMeteringUnsettled(map[string]int64{"tenant-a": 2, "tenant-b": 1})
	if body := o.scrape(t); !strings.Contains(body, `testobse148_gateway_metering_unsettled_intents{tenant_id="tenant-a"} 2`) ||
		!strings.Contains(body, `testobse148_gateway_metering_unsettled_intents{tenant_id="tenant-b"} 1`) {
		t.Fatalf("first publish not readable:\n%s", body)
	}

	// tenant-a repaired, tenant-b got worse.
	o.PublishMeteringUnsettled(map[string]int64{"tenant-b": 3})
	body := o.scrape(t)
	if strings.Contains(body, `tenant_id="tenant-a"`) {
		t.Fatalf("a repaired tenant kept its series, so the gap alert pages on history:\n%s", body)
	}
	if !strings.Contains(body, `testobse148_gateway_metering_unsettled_intents{tenant_id="tenant-b"} 3`) {
		t.Fatalf("the remaining tenant did not get its new value:\n%s", body)
	}

	// Everything repaired: no series at all, and no stray zero either.
	o.PublishMeteringUnsettled(map[string]int64{})
	if body := o.scrape(t); strings.Contains(body, "gateway_metering_unsettled_intents{") {
		t.Fatalf("gauge still has series after every tenant was repaired:\n%s", body)
	}
}

func (o *Observability) scrape(t *testing.T) string {
	t.Helper()
	r := gin.New()
	r.GET("/metrics", o.MetricsHandler())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", w.Code)
	}
	return w.Body.String()
}
