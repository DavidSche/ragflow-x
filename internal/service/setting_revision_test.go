package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestSystemSettingsRuntimeApplyAndImmutableBaseline(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	cfg := config.Config{}
	if err := svc.InitializeSystemSettingsFromConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	initial, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil || initial == nil || initial.Revision != 1 {
		t.Fatalf("initial revision missing: %+v err=%v", initial, err)
	}
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: initial.Revision,
		Changes:          map[string]interface{}{"server.port": 9999},
	}); err == nil {
		t.Fatal("startup configuration must reject database override")
	}
	revision, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: initial.Revision,
		Changes:          map[string]interface{}{"gateway.max_conns": 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Revision != 2 {
		t.Fatalf("revision must be monotonic: %d", revision.Revision)
	}
	states, err := svc.Store.ListRuntimeSettingInstanceStates(ctx)
	if err != nil || len(states) != 1 {
		t.Fatalf("runtime states: %+v err=%v", states, err)
	}
	if states[0].RuntimeRevisionID != revision.ID || states[0].ApplyStatus != model.RuntimeApplyApplied {
		t.Fatalf("runtime was not applied: %+v", states[0])
	}
	if svc.RoutePolicy.Mode != "recommend_only" {
		t.Fatalf("unexpected normalized route mode: %s", svc.RoutePolicy.Mode)
	}
}

func TestSettingSecretVersionRollbackKeepsReferenceAndNoPlaintext(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	first, err := svc.UpdateSystemSettingSecret(ctx, "admin", "observability.metrics_token", "first-secret", "rotate metrics token", true)
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.SecretReferences) != 1 || !strings.Contains(first.SnapshotJSON, "secret_refs") {
		t.Fatalf("secret reference missing: %+v", view.SecretReferences)
	}
	if strings.Contains(first.SnapshotJSON, "first-secret") || strings.Contains(view.SecretReferences["observability.metrics_token"]["id"].(string), "first-secret") {
		t.Fatal("plaintext must not enter revision or reference")
	}
	latest, err := svc.Store.GetLatestSettingSecretVersion(ctx, "observability.metrics_token")
	if err != nil || latest == nil || latest.Ciphertext == "" || latest.Fingerprint == "" {
		t.Fatalf("secret version missing: %+v err=%v", latest, err)
	}
	if strings.Contains(latest.Ciphertext, "first-secret") {
		t.Fatal("secret ciphertext must not contain plaintext")
	}
	second, err := svc.UpdateSystemSettingSecret(ctx, "admin", "observability.metrics_token", "second-secret", "rotate metrics token again", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RollbackSystemSettings(ctx, "admin", first.ID, "", true); err == nil {
		t.Fatal("rollback without an operation note must be rejected")
	}
	rolled, err := svc.RollbackSystemSettings(ctx, "admin", first.ID, "restore approved metrics token", true)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		SecretRefs map[string]map[string]interface{} `json:"secret_refs"`
	}
	if err := json.Unmarshal([]byte(rolled.SnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	reference := snapshot.SecretRefs["observability.metrics_token"]
	if reference == nil || reference["id"] == nil || reference["version"] != float64(1) {
		t.Fatalf("rollback must restore immutable secret reference: %+v", reference)
	}
	if second.Revision != first.Revision+1 || rolled.Revision != second.Revision+1 {
		t.Fatalf("rollback must create a new revision: first=%d second=%d rolled=%d", first.Revision, second.Revision, rolled.Revision)
	}
	if rolled.Note != "restore approved metrics token" {
		t.Fatalf("rollback must preserve the operator note: %q", rolled.Note)
	}
}

func TestSystemSettingsCoversSecurityObservabilityApprovalAndAlerting(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	cfg := config.Config{}
	cfg.Security = config.Security{
		AllowedOrigins:        []string{"https://console.example.com"},
		AllowedMethods:        []string{"GET", "POST"},
		AllowedHeaders:        []string{"Content-Type", "Authorization"},
		AllowCredentials:      true,
		MaxAgeSec:             600,
		HSTSEnabled:           true,
		HSTSMaxAgeSec:         31536000,
		HSTSIncludeSubdomains: true,
		ContentSecurityPolicy: "default-src 'none'",
		FrameOptions:          "DENY",
		MaxRequestBytes:       26214400,
	}
	cfg.Observability = config.Observability{
		MetricsEnabled:    true,
		MetricsPath:       "/metrics",
		MetricsNamespace:  "ragflow_x",
		MetricsToken:      "initial-metrics-secret",
		MetricsAllowCIDRs: []string{"10.0.0.0/8"},
		OTLPEndpoint:      "http://otel.example.com",
		ServiceName:       "ragflow-x",
		SampleRatio:       0.5,
	}
	cfg.Approval = config.Approval{
		Enabled:               true,
		DefaultExpireHours:    72,
		ExecutionMaxRetries:   3,
		ExpireScanIntervalSec: 3600,
		ReminderBeforeHours:   24,
		RetentionDays:         365,
		PolicyCacheTTLSec:     30,
		NotifyWebhook:         "https://approval.example.com/hook",
		NotifyWebhookSecret:   "initial-approval-secret",
	}
	cfg.Alerting = config.Alerting{
		Enabled:     true,
		ThrottleSec: 60,
		Webhooks:    []config.Webhook{{Name: "ops", URL: "https://alerts.example.com/hook", Secret: "initial-alerting-secret", Enabled: true}},
	}
	svc.SetSecurityConfig(cfg.Security)
	svc.SetObservabilityConfig(cfg.Observability)
	svc.SetAlertingConfig(cfg.Alerting)
	svc.SetApprovalConfig(cfg.Approval)
	if err := svc.InitializeSystemSettingsFromConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectedKeys := []string{
		"security.allowed_origins", "security.allow_credentials", "security.max_request_bytes",
		"security.allow_private_provider_base_url", "observability.metrics_enabled",
		"observability.metrics_allow_cidrs", "observability.tracing_enabled", "observability.sample_ratio",
		"approval.enabled", "approval.notify_webhook", "approval.expire_scan_interval_sec",
		"alerting.enabled", "alerting.throttle_sec", "alerting.webhooks",
	}
	for _, key := range expectedKeys {
		parts := strings.SplitN(key, ".", 2)
		if _, ok := view.Groups[parts[0]][parts[1]]; !ok {
			t.Fatalf("system setting missing: %s", key)
		}
	}
	for _, secretKey := range []string{"observability.metrics_token", "approval.notify_webhook_secret", "alerting.webhook_secret"} {
		if _, ok := view.SecretReferences[secretKey]; !ok {
			t.Fatalf("secret reference missing: %s", secretKey)
		}
	}
	current, err := svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil || current == nil {
		t.Fatalf("current revision missing: %+v err=%v", current, err)
	}
	for _, secret := range []string{"initial-metrics-secret", "initial-approval-secret", "initial-alerting-secret"} {
		if strings.Contains(current.SnapshotJSON, secret) {
			t.Fatalf("plaintext secret leaked into snapshot: %s", secret)
		}
	}
	if svc.CurrentObservabilityConfig().MetricsToken != "initial-metrics-secret" ||
		svc.ApprovalConfig().NotifyWebhookSecret != "initial-approval-secret" {
		t.Fatalf("secrets were not applied to runtime: metrics=%v approval=%v",
			svc.CurrentObservabilityConfig().MetricsToken, svc.ApprovalConfig().NotifyWebhookSecret)
	}
	if svc.CurrentSecurityConfig().ContentSecurityPolicy != "default-src 'none'" ||
		!svc.CurrentAlertingConfig().Enabled {
		t.Fatalf("configuration groups were not applied to runtime")
	}

	revision, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: view.Revision,
		Changes: map[string]interface{}{
			"security.allowed_origins": []interface{}{"https://new.example.com"},
			"approval.enabled":         false,
			"alerting.webhooks":        []interface{}{map[string]interface{}{"name": "ops", "type": "dingtalk", "url": "https://new.example.com/hook", "enabled": true}},
		},
		Confirmed: true,
		Note:      "apply reviewed security, approval and alerting changes",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(svc.CurrentSecurityConfig().AllowedOrigins) != 1 ||
		svc.CurrentSecurityConfig().AllowedOrigins[0] != "https://new.example.com" ||
		svc.ApprovalConfig().Enabled ||
		len(svc.CurrentAlertingConfig().Webhooks) != 1 ||
		svc.CurrentAlertingConfig().Webhooks[0].URL != "https://new.example.com/hook" ||
		svc.CurrentAlertingConfig().Webhooks[0].Type != "dingtalk" {
		t.Fatalf("runtime settings were not hot reloaded: %+v %+v %+v",
			svc.CurrentSecurityConfig(), svc.ApprovalConfig(), svc.CurrentAlertingConfig())
	}
	if revision.Revision <= current.Revision {
		t.Fatalf("revision must increase: current=%d revision=%d", current.Revision, revision.Revision)
	}
}

// ScenarioID: SC-SETTING-001
func TestP0_SETTING_001_SystemSettingsValidatesHighRiskValues(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key   string
		value interface{}
	}{
		{"security.allowed_origins", []interface{}{"*"}},
		{"security.frame_options", "NEVER"},
		{"observability.metrics_allow_cidrs", []interface{}{"not-cidr"}},
		{"observability.sample_ratio", 1.2},
		{"alerting.webhooks", []interface{}{map[string]interface{}{"name": "ops", "url": "http://bad url", "enabled": true}}},
		{"alerting.webhooks", []interface{}{map[string]interface{}{"name": "ops", "type": "slack", "url": "https://new.example.com/hook", "enabled": true}}},
	}
	for _, item := range cases {
		if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
			ExpectedRevision: view.Revision,
			Changes:          map[string]interface{}{item.key: item.value},
		}); err == nil {
			t.Fatalf("expected invalid value to be rejected: %s=%v", item.key, item.value)
		}
	}
}

// ScenarioID: SC-SETTING-001
func TestP0_SETTING_001_SystemSettingsRequiresHighRiskConfirmation(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	change := map[string]interface{}{"routing.default_mode": "auto_low_risk"}
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: view.Revision, Changes: change, Note: "enable low-risk auto routing", Confirmed: false,
	}); err == nil {
		t.Fatal("high-risk settings require explicit confirmation")
	}
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: view.Revision, Changes: change, Note: "short", Confirmed: true,
	}); err == nil {
		t.Fatal("high-risk settings require an operation note")
	}
	revision, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: view.Revision, Changes: change,
		Note: "enable low-risk auto routing for approved assistants", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Revision != view.Revision+1 || svc.RoutePolicy.Mode != "auto_low_risk" {
		t.Fatalf("confirmed high-risk settings were not applied: %+v", revision)
	}
}
