package obs

// ScenarioID: SC-OBS-001

import (
	"os"
	"path/filepath"
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
		runbook := rule.Annotations["runbook"]
		if runbook == "" || strings.Contains(runbook, "..") {
			t.Fatalf("alert %q has unexpected runbook %q", rule.Alert, rule.Annotations["runbook"])
		}
		if _, err := os.Stat(filepath.Join("..", "..", runbook)); err != nil {
			t.Fatalf("alert %q runbook %q is missing: %v", rule.Alert, runbook, err)
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
		"CompilationUpstreamStalled": {
			expression: "max by (tenant_id, kind) (ragflow_x_compilation_upstream_stall_seconds) > 86400",
			pendingFor: "10m", severity: "critical",
		},
		"CompilationUnknownAgeWarning": {
			expression: "max by (tenant_id, kind) (ragflow_x_compilation_unknown_age_seconds) > 86400",
			pendingFor: "10m", severity: "warning",
		},
		"CompilationUnknownAgeCritical": {
			expression: "max by (tenant_id, kind) (ragflow_x_compilation_unknown_age_seconds) > 259200",
			pendingFor: "10m", severity: "critical",
		},
		"EnhancementBudgetRejected": {
			expression: "increase(ragflow_x_enhancement_budget_rejected_total[15m]) > 0",
			pendingFor: "0m", severity: "warning",
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
		// 137 §11.1 Security/Permission Gate alerts (M211).
		"MemorySearchUnauthorizedFilter": {
			expression: `increase(ragflow_x_memory_search_authorized_filter_total{result="unauthorized"}[15m]) > 0`,
			pendingFor: "0m", severity: "critical",
		},
		"ContextPreModelAuthError": {
			expression: `increase(ragflow_x_context_pre_model_auth_total{result="error"}[15m]) > 0`,
			pendingFor: "5m", severity: "warning",
		},
		"MemoryPrincipalMappingFailure": {
			expression: `increase(ragflow_x_memory_dispatch_principal_mapping_total{result="failure"}[15m]) > 0`,
			pendingFor: "5m", severity: "warning",
		},
		"MemoryUnauthorizedExternalDispatch": {
			expression: "increase(ragflow_x_memory_dispatch_unauthorized_external_dispatch_total[15m]) > 0",
			pendingFor: "0m", severity: "critical",
		},
		"MemoryRevocationInvalidationAnomaly": {
			expression: `increase(ragflow_x_memory_dispatch_pending_invalidated_total{reason_class="user_action"}[30m]) > 0 and increase(ragflow_x_memory_dispatch_revocation_event_total[30m]) == 0`,
			pendingFor: "10m", severity: "critical",
		},
		// 137 §11.1 Operational Alert additions (M211).
		"CompilationRunFailedDetected": {
			expression: `increase(ragflow_x_compilation_run_terminal_total{result="failed"}[30m]) > 0`,
			pendingFor: "5m", severity: "warning",
		},
		"CompilationRunUnknownDetected": {
			expression: `increase(ragflow_x_compilation_run_terminal_total{result="unknown"}[30m]) > 0`,
			pendingFor: "5m", severity: "warning",
		},
		"CompilationReadyRatioLow": {
			expression: `sum by (tenant_id) (increase(ragflow_x_compilation_ready_eval_total{result="ready"}[1h])) / clamp_min(sum by (tenant_id) (increase(ragflow_x_compilation_ready_eval_total[1h])), 1) < 1 and sum by (tenant_id) (increase(ragflow_x_compilation_ready_eval_total[1h])) >= 5`,
			pendingFor: "15m", severity: "warning",
		},
		"CompilationArtifactBindingIncomplete": {
			expression: `increase(ragflow_x_compilation_artifact_binding_total{result="incomplete"}[30m]) > 0`,
			pendingFor: "10m", severity: "warning",
		},
		"CompilationQueueBacklogHigh": {
			expression: `max(ragflow_x_compilation_queue_depth{dimension="backlog"}) > 0`,
			pendingFor: "15m", severity: "warning",
		},
		"RetrievalRawFallbackRatioHigh": {
			expression: `sum(increase(ragflow_x_retrieval_mode_total{result="raw_fallback"}[30m])) / clamp_min(sum(increase(ragflow_x_retrieval_mode_total{mode="compiled_first"}[30m])), 1) > 0.25 and sum(increase(ragflow_x_retrieval_mode_total{mode="compiled_first"}[30m])) >= 5`,
			pendingFor: "10m", severity: "warning",
		},
		"MemoryWriteSuccessRatioLow": {
			expression: `sum(increase(ragflow_x_memory_dispatch_external_result_total{result="executed"}[30m])) / clamp_min(sum(increase(ragflow_x_memory_dispatch_external_result_total[30m])), 1) < 0.95 and sum(increase(ragflow_x_memory_dispatch_external_result_total[30m])) >= 5`,
			pendingFor: "10m", severity: "warning",
		},
		"MemoryUnknownResultAll": {
			expression: `sum(increase(ragflow_x_memory_dispatch_external_result_total{result="unknown"}[20m])) / clamp_min(sum(increase(ragflow_x_memory_dispatch_external_result_total[20m])), 1) == 1 and sum(increase(ragflow_x_memory_dispatch_external_result_total[20m])) >= 5`,
			pendingFor: "0m", severity: "critical",
		},
		"MemoryPendingInvalidatedSystemMismatch": {
			expression: `increase(ragflow_x_memory_dispatch_pending_invalidated_total{reason_class="system_mismatch"}[30m]) > 0`,
			pendingFor: "5m", severity: "warning",
		},
		"CompilationProjectionStaleDetected": {
			expression: "increase(ragflow_x_compilation_projection_stale_total[30m]) > 0",
			pendingFor: "10m", severity: "warning",
		},
		"MemoryTombstoneVerificationFailed": {
			expression: `increase(ragflow_x_memory_tombstone_verification_total{result="failed"}[15m]) > 0`,
			pendingFor: "0m", severity: "critical",
		},
		"MemoryPrivacyGateBlockedRatioHigh": {
			expression: `sum(increase(ragflow_x_memory_privacy_gate_decision_total{decision="block"}[30m])) / clamp_min(sum(increase(ragflow_x_memory_privacy_gate_decision_total[30m])), 1) > 0.05 and sum(increase(ragflow_x_memory_privacy_gate_decision_total[30m])) >= 5`,
			pendingFor: "15m", severity: "warning",
		},
		"ContextDecisionLatencyHigh": {
			expression: `histogram_quantile(0.95, sum by (le, request_kind) (increase(ragflow_x_context_decision_duration_seconds_bucket{request_kind=~"chat|preview"}[15m]))) > 0.3`,
			pendingFor: "10m", severity: "warning",
		},
		"ContextDecisionSearchLatencyHigh": {
			expression: `histogram_quantile(0.95, sum by (le) (increase(ragflow_x_context_decision_duration_seconds_bucket{request_kind="search"}[15m]))) > 0.5`,
			pendingFor: "10m", severity: "warning",
		},
		// doc/225 §10 data-source connector alerts.
		"ConnectorSyncFailureRateHigh": {
			expression: `sum(increase(ragflow_x_connector_runs_total{status="failed"}[1h])) / clamp_min(sum(increase(ragflow_x_connector_runs_total[1h])), 1) > 0.3 and sum(increase(ragflow_x_connector_runs_total[1h])) >= 3`,
			pendingFor: "10m", severity: "warning",
		},
		"ConnectorCredentialFailure": {
			expression: "increase(ragflow_x_connector_credential_failure_total[30m]) >= 2",
			pendingFor: "0m", severity: "critical",
		},
	}
	expectedRunbook := map[string]string{
		"CompilationUpstreamStalled":    "doc/runbooks/CompilationUpstreamRecoveryRunbook.md",
		"CompilationUnknownAgeWarning":  "doc/runbooks/CompilationUpstreamRecoveryRunbook.md",
		"CompilationUnknownAgeCritical": "doc/runbooks/CompilationUpstreamRecoveryRunbook.md",
	}
	for alert, wantRunbook := range expectedRunbook {
		rule, exists := rules[alert]
		if !exists {
			t.Fatalf("compilation alert %q is missing", alert)
		}
		if rule.Annotations["runbook"] != wantRunbook {
			t.Fatalf("alert %q runbook = %q, want %q", alert, rule.Annotations["runbook"], wantRunbook)
		}
		if !strings.Contains(rule.Expr, "tenant_id") || !strings.Contains(rule.Expr, "kind") {
			t.Fatalf("alert %q must retain tenant and kind dimensions: %s", alert, rule.Expr)
		}
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
