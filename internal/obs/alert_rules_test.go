package obs

// ScenarioID: SC-OBS-001

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type prometheusRuleFile struct {
	Groups []prometheusRuleGroup `yaml:"groups"`
}

type prometheusRuleGroup struct {
	Name     string                `yaml:"name"`
	Interval string                `yaml:"interval"`
	Rules    []prometheusAlertRule `yaml:"rules"`
}

type prometheusAlertRule struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

func TestOBS_003_PrometheusAlertRulesMatchOperationalThresholds(t *testing.T) {
	path := "../../config/observability/prometheus-alerts.yml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Prometheus alert rules: %v", err)
	}
	var file prometheusRuleFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode Prometheus alert rules: %v", err)
	}
	if len(file.Groups) != 1 || file.Groups[0].Name != "ragflow-x-operational-contracts" || file.Groups[0].Interval != "30s" {
		t.Fatalf("unexpected Prometheus rule groups: %+v", file.Groups)
	}

	rules := make(map[string]prometheusAlertRule, len(file.Groups[0].Rules))
	for _, rule := range file.Groups[0].Rules {
		if rule.Alert == "" || strings.TrimSpace(rule.Expr) == "" {
			t.Fatalf("incomplete Prometheus rule: %+v", rule)
		}
		if rules[rule.Alert].Alert != "" {
			t.Fatalf("duplicate Prometheus alert %q", rule.Alert)
		}
		if rule.Labels["service"] != "ragflow-x" {
			t.Fatalf("alert %q missing service label: %+v", rule.Alert, rule.Labels)
		}
		if rule.Annotations["summary"] == "" || rule.Annotations["description"] == "" {
			t.Fatalf("alert %q missing operator guidance: %+v", rule.Alert, rule.Annotations)
		}
		if !strings.HasSuffix(rule.Annotations["runbook"], "doc/08_部署运维与可观测性.md") {
			t.Fatalf("alert %q has unexpected runbook %q", rule.Alert, rule.Annotations["runbook"])
		}
		rules[rule.Alert] = rule
	}

	expected := map[string]struct {
		expression string
		pendingFor string
		severity   string
	}{
		"AlertDeliveryCompensationSkippedHigh": {
			expression: "increase(ragflow_x_alert_delivery_compensation_skipped_total[15m]) > 50",
			pendingFor: "5m", severity: "warning",
		},
		"AlertDeliveryAbandoned": {
			expression: "increase(ragflow_x_alert_delivery_compensation_abandoned_total[15m]) > 0",
			pendingFor: "0m", severity: "critical",
		},
		"AlertDeliveryFencedResult": {
			expression: "increase(ragflow_x_alert_delivery_compensation_fenced_total[5m]) > 0",
			pendingFor: "0m", severity: "critical",
		},
		"ResourceSyncItemsFailedHigh": {
			expression: `increase(ragflow_x_resource_sync_items_total{status="failed"}[15m]) > 5`,
			pendingFor: "10m", severity: "warning",
		},
		"ResourceSyncStaleRunRecovered": {
			expression: "increase(ragflow_x_resource_sync_stale_runs_total[15m]) > 0",
			pendingFor: "0m", severity: "critical",
		},
		"ResourceSyncProgressStalled": {
			expression: `ragflow_x_resource_sync_run_progress{kind="total"} > 0 and on(source_id) sum by (source_id) (increase(ragflow_x_resource_sync_items_total[30m])) == 0`,
			pendingFor: "10m", severity: "critical",
		},
	}
	if len(rules) != len(expected) {
		t.Fatalf("Prometheus alert count = %d, want %d: %+v", len(rules), len(expected), rules)
	}
	for alert, want := range expected {
		rule, exists := rules[alert]
		if !exists {
			t.Fatalf("Prometheus alert %q is missing", alert)
		}
		if rule.Expr != want.expression {
			t.Fatalf("alert %q expr = %q, want %q", alert, rule.Expr, want.expression)
		}
		if rule.For != want.pendingFor || rule.Labels["severity"] != want.severity {
			t.Fatalf("alert %q pending/severity = %q/%q, want %q/%q", alert, rule.For, rule.Labels["severity"], want.pendingFor, want.severity)
		}
	}
}
