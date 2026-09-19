package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

const SystemSettingsSchemaVersion = 1

type SettingSourcePolicy struct {
	Source           string `json:"source"`
	Editable         bool   `json:"editable"`
	DatabaseOverride bool   `json:"database_override"`
	RestartRequired  bool   `json:"restart_required"`
	RiskLevel        string `json:"risk_level"`
	Impact           string `json:"impact"`
	EffectiveMode    string `json:"effective_mode"`
}

type SettingKeyMetadata struct {
	Group   string
	Field   string
	Type    string
	Min     float64
	Max     float64
	Allowed []string
}

var settingKeyPolicies = map[string]SettingSourcePolicy{
	"server.port": {
		Source: "yaml_env", Editable: false, DatabaseOverride: false,
		RestartRequired: true, RiskLevel: "high",
	},
	"server.data_dir": {
		Source: "yaml_env", Editable: false, DatabaseOverride: false,
		RestartRequired: true, RiskLevel: "high",
	},
	"server.log_path": {
		Source: "yaml_env", Editable: false, DatabaseOverride: false,
		RestartRequired: true, RiskLevel: "medium",
	},
	"gateway.non_stream_timeout_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "low",
	},
	"gateway.stream_header_timeout_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "low",
	},
	"gateway.stream_total_timeout_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "low",
	},
	"gateway.max_conns": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium",
	},
	"gateway.rate_limit_per_minute": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium",
	},
	"retention.audit_days": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high",
	},
	"retention.usage_days": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium",
	},
	"routing.default_mode": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "changes assistant routing", EffectiveMode: "hot_reload",
	},
	"security.allowed_origins": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "expands cross-origin browser access", EffectiveMode: "hot_reload",
	},
	"security.allowed_methods": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium", Impact: "changes allowed HTTP methods", EffectiveMode: "hot_reload",
	},
	"security.allowed_headers": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium", Impact: "changes allowed request headers", EffectiveMode: "hot_reload",
	},
	"security.allow_credentials": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "allows credentialed cross-origin requests", EffectiveMode: "hot_reload",
	},
	"security.max_age_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "low", Impact: "changes CORS preflight cache", EffectiveMode: "hot_reload",
	},
	"security.hsts_enabled": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "requires HTTPS and can lock out HTTP clients", EffectiveMode: "hot_reload",
	},
	"security.hsts_max_age_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "controls browser HTTPS pin duration", EffectiveMode: "hot_reload",
	},
	"security.hsts_include_subdomains": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "extends HTTPS requirement to subdomains", EffectiveMode: "hot_reload",
	},
	"security.content_security_policy": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "changes browser content loading policy", EffectiveMode: "hot_reload",
	},
	"security.frame_options": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "controls frame embedding", EffectiveMode: "hot_reload",
	},
	"security.max_request_bytes": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium", Impact: "changes request body limit", EffectiveMode: "hot_reload",
	},
	"security.allow_private_provider_base_url": {
		Source: "yaml_env", Editable: false, DatabaseOverride: false,
		RestartRequired: false, RiskLevel: "high", Impact: "allows provider requests to private networks", EffectiveMode: "deployment_controlled",
	},
	"observability.metrics_enabled": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "medium", Impact: "exposes or hides metrics endpoint", EffectiveMode: "pending_restart",
	},
	"observability.metrics_path": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "medium", Impact: "changes metrics endpoint", EffectiveMode: "pending_restart",
	},
	"observability.metrics_namespace": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "medium", Impact: "renames Prometheus metric namespace", EffectiveMode: "pending_restart",
	},
	"observability.metrics_allow_cidrs": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "allows selected networks to read metrics", EffectiveMode: "hot_reload",
	},
	"observability.tracing_enabled": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "medium", Impact: "enables or disables OpenTelemetry", EffectiveMode: "pending_restart",
	},
	"observability.otlp_endpoint": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "high", Impact: "sends traces to external collector", EffectiveMode: "pending_restart",
	},
	"observability.service_name": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "low", Impact: "changes trace service identity", EffectiveMode: "pending_restart",
	},
	"observability.sample_ratio": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "medium", Impact: "changes trace sampling", EffectiveMode: "pending_restart",
	},
	"approval.enabled": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "enforces or bypasses governance approvals", EffectiveMode: "hot_reload",
	},
	"approval.default_expire_hours": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium", Impact: "changes default approval expiry", EffectiveMode: "hot_reload",
	},
	"approval.execution_max_retries": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium", Impact: "changes approved action retry budget", EffectiveMode: "hot_reload",
	},
	"approval.expire_scan_interval_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: true, RiskLevel: "medium", Impact: "changes approval maintenance interval", EffectiveMode: "pending_restart",
	},
	"approval.reminder_before_hours": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "low", Impact: "changes approval reminder lead time", EffectiveMode: "hot_reload",
	},
	"approval.retention_days": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "changes approval record retention", EffectiveMode: "hot_reload",
	},
	"approval.policy_cache_ttl_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium", Impact: "changes approval policy cache freshness", EffectiveMode: "hot_reload",
	},
	"approval.notify_webhook": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "sends approval events to external system", EffectiveMode: "hot_reload",
	},
	"runtime.heartbeat_timeout_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "changes when runtime instances are marked stale", EffectiveMode: "hot_reload",
	},
	"alerting.enabled": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "medium", Impact: "starts or stops outbound alert delivery", EffectiveMode: "hot_reload",
	},
	"alerting.throttle_sec": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "low", Impact: "changes duplicate alert throttle", EffectiveMode: "hot_reload",
	},
	"alerting.webhooks": {
		Source: "yaml_env_db", Editable: true, DatabaseOverride: true,
		RestartRequired: false, RiskLevel: "high", Impact: "sends alerts to external systems", EffectiveMode: "hot_reload",
	},
}

var settingKeyValidators = map[string]SettingKeyMetadata{
	"server.port":                       {Group: "server", Field: "port", Type: "number", Min: 1, Max: 65535},
	"server.data_dir":                   {Group: "server", Field: "data_dir", Type: "string"},
	"server.log_path":                   {Group: "server", Field: "log_path", Type: "string"},
	"gateway.non_stream_timeout_sec":    {Group: "gateway", Field: "non_stream_timeout_sec", Type: "number", Min: 1, Max: 3600},
	"gateway.stream_header_timeout_sec": {Group: "gateway", Field: "stream_header_timeout_sec", Type: "number", Min: 1, Max: 600},
	"gateway.stream_total_timeout_sec":  {Group: "gateway", Field: "stream_total_timeout_sec", Type: "number", Min: 1, Max: 86400},
	"gateway.max_conns":                 {Group: "gateway", Field: "max_conns", Type: "number", Min: 1, Max: 10000},
	"gateway.rate_limit_per_minute":     {Group: "gateway", Field: "rate_limit_per_minute", Type: "number", Min: 1, Max: 1000000},
	"retention.audit_days":              {Group: "retention", Field: "audit_days", Type: "number", Min: 1, Max: 3650},
	"retention.usage_days":              {Group: "retention", Field: "usage_days", Type: "number", Min: 1, Max: 3650},
	"routing.default_mode":              {Group: "routing", Field: "default_mode", Type: "enum", Allowed: []string{"disabled", "recommend_only", "auto_low_risk"}},
	"security.allowed_origins":          {Group: "security", Field: "allowed_origins", Type: "origin_list"},
	"security.allowed_methods":          {Group: "security", Field: "allowed_methods", Type: "method_list"},
	"security.allowed_headers":          {Group: "security", Field: "allowed_headers", Type: "string_list"},
	"security.allow_credentials":        {Group: "security", Field: "allow_credentials", Type: "bool"},
	"security.max_age_sec":              {Group: "security", Field: "max_age_sec", Type: "number", Min: 0, Max: 86400},
	"security.hsts_enabled":             {Group: "security", Field: "hsts_enabled", Type: "bool"},
	"security.hsts_max_age_sec":         {Group: "security", Field: "hsts_max_age_sec", Type: "number", Min: 0, Max: 31536000},
	"security.hsts_include_subdomains":  {Group: "security", Field: "hsts_include_subdomains", Type: "bool"},
	"security.content_security_policy":  {Group: "security", Field: "content_security_policy", Type: "string"},
	"security.frame_options":            {Group: "security", Field: "frame_options", Type: "enum", Allowed: []string{"DENY", "SAMEORIGIN"}},
	"security.max_request_bytes":        {Group: "security", Field: "max_request_bytes", Type: "number", Min: 1024, Max: 104857600},
	"observability.metrics_enabled":     {Group: "observability", Field: "metrics_enabled", Type: "bool"},
	"observability.metrics_path":        {Group: "observability", Field: "metrics_path", Type: "metrics_path"},
	"observability.metrics_namespace":   {Group: "observability", Field: "metrics_namespace", Type: "string"},
	"observability.metrics_allow_cidrs": {Group: "observability", Field: "metrics_allow_cidrs", Type: "cidr_list"},
	"observability.tracing_enabled":     {Group: "observability", Field: "tracing_enabled", Type: "bool"},
	"observability.otlp_endpoint":       {Group: "observability", Field: "otlp_endpoint", Type: "url"},
	"observability.service_name":        {Group: "observability", Field: "service_name", Type: "string"},
	"observability.sample_ratio":        {Group: "observability", Field: "sample_ratio", Type: "number", Min: 0, Max: 1},
	"approval.enabled":                  {Group: "approval", Field: "enabled", Type: "bool"},
	"approval.default_expire_hours":     {Group: "approval", Field: "default_expire_hours", Type: "number", Min: 1, Max: 8760},
	"approval.execution_max_retries":    {Group: "approval", Field: "execution_max_retries", Type: "number", Min: 0, Max: 10},
	"approval.expire_scan_interval_sec": {Group: "approval", Field: "expire_scan_interval_sec", Type: "number", Min: 10, Max: 86400},
	"approval.reminder_before_hours":    {Group: "approval", Field: "reminder_before_hours", Type: "number", Min: 0, Max: 168},
	"approval.retention_days":           {Group: "approval", Field: "retention_days", Type: "number", Min: 1, Max: 3650},
	"approval.policy_cache_ttl_sec":     {Group: "approval", Field: "policy_cache_ttl_sec", Type: "number", Min: 0, Max: 3600},
	"approval.notify_webhook":           {Group: "approval", Field: "notify_webhook", Type: "url"},
	"runtime.heartbeat_timeout_sec":     {Group: "runtime", Field: "heartbeat_timeout_sec", Type: "number", Min: 10, Max: 86400},
	"alerting.enabled":                  {Group: "alerting", Field: "enabled", Type: "bool"},
	"alerting.throttle_sec":             {Group: "alerting", Field: "throttle_sec", Type: "number", Min: 1, Max: 86400},
	"alerting.webhooks":                 {Group: "alerting", Field: "webhooks", Type: "webhook_list"},
}

func defaultSystemSettingsSnapshot() map[string]interface{} {
	return map[string]interface{}{
		"schema_version": SystemSettingsSchemaVersion,
		"groups": map[string]interface{}{
			"server": map[string]interface{}{"port": 9191},
			"gateway": map[string]interface{}{
				"non_stream_timeout_sec":    120,
				"stream_header_timeout_sec": 30,
				"stream_total_timeout_sec":  3600,
				"max_conns":                 20,
				"rate_limit_per_minute":     600,
			},
			"retention": map[string]interface{}{"audit_days": 365, "usage_days": 180},
			"routing":   map[string]interface{}{"default_mode": "recommend_only"},
			"security": map[string]interface{}{
				"allowed_origins":                 []interface{}{},
				"allowed_methods":                 []interface{}{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
				"allowed_headers":                 []interface{}{"Content-Type", "Authorization", "X-Request-Id"},
				"allow_credentials":               true,
				"max_age_sec":                     600,
				"hsts_enabled":                    false,
				"hsts_max_age_sec":                31536000,
				"hsts_include_subdomains":         true,
				"content_security_policy":         "default-src 'none'",
				"frame_options":                   "DENY",
				"max_request_bytes":               26214400,
				"allow_private_provider_base_url": false,
			},
			"observability": map[string]interface{}{
				"metrics_enabled":     true,
				"metrics_path":        "/metrics",
				"metrics_namespace":   "ragflow_x",
				"metrics_allow_cidrs": []interface{}{},
				"tracing_enabled":     false,
				"otlp_endpoint":       "",
				"service_name":        "ragflow-x",
				"sample_ratio":        1.0,
			},
			"approval": map[string]interface{}{
				"enabled":                  false,
				"default_expire_hours":     72,
				"execution_max_retries":    3,
				"expire_scan_interval_sec": 3600,
				"reminder_before_hours":    24,
				"retention_days":           365,
				"policy_cache_ttl_sec":     30,
				"notify_webhook":           "",
			},
			"runtime": map[string]interface{}{"heartbeat_timeout_sec": 60},
			"alerting": map[string]interface{}{
				"enabled":      false,
				"throttle_sec": 60,
				"webhooks":     []interface{}{},
			},
		},
	}
}

func systemSettingsSnapshotFromConfig(cfg config.Config) map[string]interface{} {
	snapshot := defaultSystemSettingsSnapshot()
	groups := snapshot["groups"].(map[string]interface{})
	groups["server"] = map[string]interface{}{
		"port": cfg.Server.Port, "data_dir": cfg.App.DataDir, "log_path": cfg.Logging.OutputPath,
	}
	groups["gateway"] = map[string]interface{}{
		"non_stream_timeout_sec":    cfg.Gateway.NonStreamTimeoutSec,
		"stream_header_timeout_sec": cfg.Gateway.StreamHeaderTimeoutSec,
		"stream_total_timeout_sec":  cfg.Gateway.StreamTimeoutSec,
		"max_conns":                 cfg.Gateway.MaxConns,
		"rate_limit_per_minute":     cfg.Gateway.RateLimitPerMin,
	}
	groups["retention"] = map[string]interface{}{
		"audit_days": cfg.Retention.AuditDays, "usage_days": cfg.Retention.UsageDays,
	}
	groups["routing"] = map[string]interface{}{
		"default_mode": firstNonEmpty(cfg.ConversationRouting.Mode, "recommend_only"),
	}
	groups["security"] = map[string]interface{}{
		"allowed_origins":                 interfaceSlice(cfg.Security.AllowedOrigins),
		"allowed_methods":                 interfaceSlice(cfg.Security.AllowedMethods),
		"allowed_headers":                 interfaceSlice(cfg.Security.AllowedHeaders),
		"allow_credentials":               cfg.Security.AllowCredentials,
		"max_age_sec":                     cfg.Security.MaxAgeSec,
		"hsts_enabled":                    cfg.Security.HSTSEnabled,
		"hsts_max_age_sec":                cfg.Security.HSTSMaxAgeSec,
		"hsts_include_subdomains":         cfg.Security.HSTSIncludeSubdomains,
		"content_security_policy":         cfg.Security.ContentSecurityPolicy,
		"frame_options":                   cfg.Security.FrameOptions,
		"max_request_bytes":               cfg.Security.MaxRequestBytes,
		"allow_private_provider_base_url": cfg.Security.AllowPrivateProviderBaseURL,
	}
	groups["observability"] = map[string]interface{}{
		"metrics_enabled":     cfg.Observability.MetricsEnabled,
		"metrics_path":        cfg.Observability.MetricsPath,
		"metrics_namespace":   cfg.Observability.MetricsNamespace,
		"metrics_allow_cidrs": interfaceSlice(cfg.Observability.MetricsAllowCIDRs),
		"tracing_enabled":     cfg.Observability.TracingEnabled,
		"otlp_endpoint":       cfg.Observability.OTLPEndpoint,
		"service_name":        cfg.Observability.ServiceName,
		"sample_ratio":        cfg.Observability.SampleRatio,
	}
	groups["approval"] = map[string]interface{}{
		"enabled":                  cfg.Approval.Enabled,
		"default_expire_hours":     cfg.Approval.DefaultExpireHours,
		"execution_max_retries":    cfg.Approval.ExecutionMaxRetries,
		"expire_scan_interval_sec": cfg.Approval.ExpireScanIntervalSec,
		"reminder_before_hours":    cfg.Approval.ReminderBeforeHours,
		"retention_days":           cfg.Approval.RetentionDays,
		"policy_cache_ttl_sec":     cfg.Approval.PolicyCacheTTLSec,
		"notify_webhook":           cfg.Approval.NotifyWebhook,
	}
	groups["alerting"] = map[string]interface{}{
		"enabled":      cfg.Alerting.Enabled,
		"throttle_sec": cfg.Alerting.ThrottleSec,
		"webhooks":     alertWebhooksToInterfaces(cfg.Alerting.Webhooks),
	}
	groups["runtime"] = map[string]interface{}{
		"heartbeat_timeout_sec": cfg.Runtime.HeartbeatTimeoutSec,
	}
	return snapshot
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func settingGroup(snapshot map[string]interface{}, name string) map[string]interface{} {
	groups, _ := snapshot["groups"].(map[string]interface{})
	group, _ := groups[name].(map[string]interface{})
	return group
}

func withDefaultSettingGroups(snapshot map[string]interface{}) map[string]interface{} {
	defaults := defaultSystemSettingsSnapshot()
	defaultGroups, _ := defaults["groups"].(map[string]interface{})
	groups, _ := snapshot["groups"].(map[string]interface{})
	if groups == nil {
		groups = map[string]interface{}{}
		snapshot["groups"] = groups
	}
	for group, defaultGroupValue := range defaultGroups {
		defaultGroup, _ := defaultGroupValue.(map[string]interface{})
		existingGroupValue, exists := groups[group]
		existingGroup, _ := existingGroupValue.(map[string]interface{})
		if !exists || existingGroup == nil {
			if defaultGroup != nil {
				groups[group] = defaultGroup
			} else {
				groups[group] = defaultGroupValue
			}
			continue
		}
		for field, fieldValue := range defaultGroup {
			if _, exists := existingGroup[field]; !exists {
				existingGroup[field] = fieldValue
			}
		}
	}
	return snapshot
}

func settingNumber(snapshot map[string]interface{}, group, field string, fallback int) int {
	groupValue := settingGroup(snapshot, group)
	if groupValue == nil {
		return fallback
	}
	number, err := toFloat64(groupValue[field])
	if err != nil || number <= 0 {
		return fallback
	}
	return int(number)
}

func settingString(snapshot map[string]interface{}, group, field, fallback string) string {
	groupValue := settingGroup(snapshot, group)
	if groupValue == nil {
		return fallback
	}
	if text, ok := groupValue[field].(string); ok && strings.TrimSpace(text) != "" {
		return text
	}
	return fallback
}

func settingBool(snapshot map[string]interface{}, group, field string, fallback bool) bool {
	groupValue := settingGroup(snapshot, group)
	if groupValue == nil {
		return fallback
	}
	if value, ok := groupValue[field].(bool); ok {
		return value
	}
	return fallback
}

func settingStringList(snapshot map[string]interface{}, group, field string, fallback []string) []string {
	groupValue := settingGroup(snapshot, group)
	if groupValue == nil {
		return fallback
	}
	values, ok := groupValue[field].([]interface{})
	if !ok {
		return fallback
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func settingWebhookList(snapshot map[string]interface{}, group, field string, fallback []config.Webhook) []config.Webhook {
	groupValue := settingGroup(snapshot, group)
	if groupValue == nil {
		return fallback
	}
	raw, ok := groupValue[field].([]interface{})
	if !ok {
		return fallback
	}
	result := make([]config.Webhook, 0, len(raw))
	for _, item := range raw {
		object, _ := item.(map[string]interface{})
		name, _ := object["name"].(string)
		channelType, _ := object["type"].(string)
		url, _ := object["url"].(string)
		enabled, _ := object["enabled"].(bool)
		result = append(result, config.Webhook{
			Name: name, Type: channelType, URL: url, Enabled: enabled,
		})
	}
	return result
}

func interfaceSlice(values []string) []interface{} {
	result := make([]interface{}, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func alertWebhooksToInterfaces(values []config.Webhook) []interface{} {
	result := make([]interface{}, 0, len(values))
	for _, value := range values {
		result = append(result, map[string]interface{}{
			"name": value.Name, "type": value.Type, "url": value.URL, "enabled": value.Enabled,
		})
	}
	return result
}

// ApplySystemSettingsSnapshot converges only restart-independent keys. Startup
// values remain represented in the desired snapshot but are never mutated here.
func (s *Service) ApplySystemSettingsSnapshot(snapshot map[string]interface{}) error {
	security := s.CurrentSecurityConfig()
	security.AllowedOrigins = settingStringList(snapshot, "security", "allowed_origins", security.AllowedOrigins)
	security.AllowedMethods = settingStringList(snapshot, "security", "allowed_methods", security.AllowedMethods)
	security.AllowedHeaders = settingStringList(snapshot, "security", "allowed_headers", security.AllowedHeaders)
	security.AllowCredentials = settingBool(snapshot, "security", "allow_credentials", security.AllowCredentials)
	security.MaxAgeSec = settingNumber(snapshot, "security", "max_age_sec", security.MaxAgeSec)
	security.HSTSEnabled = settingBool(snapshot, "security", "hsts_enabled", security.HSTSEnabled)
	security.HSTSMaxAgeSec = settingNumber(snapshot, "security", "hsts_max_age_sec", security.HSTSMaxAgeSec)
	security.HSTSIncludeSubdomains = settingBool(snapshot, "security", "hsts_include_subdomains", security.HSTSIncludeSubdomains)
	security.ContentSecurityPolicy = settingString(snapshot, "security", "content_security_policy", security.ContentSecurityPolicy)
	security.FrameOptions = settingString(snapshot, "security", "frame_options", security.FrameOptions)
	security.MaxRequestBytes = int64(settingNumber(snapshot, "security", "max_request_bytes", int(security.MaxRequestBytes)))
	s.SetSecurityConfig(security)

	observability := s.CurrentObservabilityConfig()
	observability.MetricsEnabled = settingBool(snapshot, "observability", "metrics_enabled", observability.MetricsEnabled)
	observability.MetricsPath = settingString(snapshot, "observability", "metrics_path", observability.MetricsPath)
	observability.MetricsNamespace = settingString(snapshot, "observability", "metrics_namespace", observability.MetricsNamespace)
	observability.MetricsAllowCIDRs = settingStringList(snapshot, "observability", "metrics_allow_cidrs", observability.MetricsAllowCIDRs)
	observability.TracingEnabled = settingBool(snapshot, "observability", "tracing_enabled", observability.TracingEnabled)
	observability.OTLPEndpoint = settingString(snapshot, "observability", "otlp_endpoint", observability.OTLPEndpoint)
	observability.ServiceName = settingString(snapshot, "observability", "service_name", observability.ServiceName)
	if value, err := toFloat64(settingGroup(snapshot, "observability")["sample_ratio"]); err == nil {
		observability.SampleRatio = value
	}
	if token, err := s.SettingSecretValue(context.Background(), "observability.metrics_token"); err == nil {
		observability.MetricsToken = token
	}
	s.SetObservabilityConfig(observability)

	s.SetGatewayClients(GatewayConfig{
		NonStreamTimeout:    time.Duration(settingNumber(snapshot, "gateway", "non_stream_timeout_sec", 120)) * time.Second,
		StreamHeaderTimeout: time.Duration(settingNumber(snapshot, "gateway", "stream_header_timeout_sec", 30)) * time.Second,
		StreamTimeout:       time.Duration(settingNumber(snapshot, "gateway", "stream_total_timeout_sec", 3600)) * time.Second,
		MaxConns:            settingNumber(snapshot, "gateway", "max_conns", 20),
	})
	approval := s.ApprovalConfig()
	approval.Enabled = settingBool(snapshot, "approval", "enabled", approval.Enabled)
	approval.DefaultExpireHours = settingNumber(snapshot, "approval", "default_expire_hours", approval.DefaultExpireHours)
	approval.ExecutionMaxRetries = settingNumber(snapshot, "approval", "execution_max_retries", approval.ExecutionMaxRetries)
	approval.ExpireScanIntervalSec = settingNumber(snapshot, "approval", "expire_scan_interval_sec", approval.ExpireScanIntervalSec)
	approval.ReminderBeforeHours = settingNumber(snapshot, "approval", "reminder_before_hours", approval.ReminderBeforeHours)
	approval.RetentionDays = settingNumber(snapshot, "approval", "retention_days", approval.RetentionDays)
	approval.PolicyCacheTTLSec = settingNumber(snapshot, "approval", "policy_cache_ttl_sec", approval.PolicyCacheTTLSec)
	approval.NotifyWebhook = settingString(snapshot, "approval", "notify_webhook", approval.NotifyWebhook)
	s.SetApprovalConfig(approval)

	alerting := s.CurrentAlertingConfig()
	alerting.Enabled = settingBool(snapshot, "alerting", "enabled", alerting.Enabled)
	alerting.ThrottleSec = settingNumber(snapshot, "alerting", "throttle_sec", alerting.ThrottleSec)
	alerting.Webhooks = settingWebhookList(snapshot, "alerting", "webhooks", alerting.Webhooks)
	s.SetAlertingConfig(alerting)

	runtime := s.CurrentRuntimeConfig()
	runtime.HeartbeatTimeoutSec = settingNumber(snapshot, "runtime", "heartbeat_timeout_sec", runtime.HeartbeatTimeoutSec)
	if token, err := s.SettingSecretValue(context.Background(), "runtime.report_token"); err == nil {
		runtime.ReportToken = token
	}
	s.SetRuntimeConfig(runtime)

	retention := s.currentRetentionPolicy()
	retention.AuditDays = settingNumber(snapshot, "retention", "audit_days", retention.AuditDays)
	retention.UsageDays = settingNumber(snapshot, "retention", "usage_days", retention.UsageDays)
	s.SetRetentionPolicy(retention)
	routing := s.RoutePolicy
	routing.Mode = settingString(snapshot, "routing", "default_mode", routing.Mode)
	s.SetRoutePolicy(routing)
	s.refreshNotificationHub(context.Background())
	return nil
}

func (s *Service) refreshNotificationHub(ctx context.Context) {
	approval := s.ApprovalConfig()
	approvalSecret, err := s.SettingSecretValue(ctx, "approval.notify_webhook_secret")
	if err != nil {
		approvalSecret = ""
	}
	approval.NotifyWebhookSecret = approvalSecret
	alerting := s.CurrentAlertingConfig()
	alertSecret, err := s.SettingSecretValue(ctx, "alerting.webhook_secret")
	if err != nil {
		alertSecret = ""
	}
	for index := range alerting.Webhooks {
		alerting.Webhooks[index].Secret = alertSecret
	}
	hub := notify.NewHub(alerting, approval)
	hub.SetSink(s)
	previous := notify.Get()
	notify.Set(hub)
	if previous != nil {
		previous.Shutdown()
	}
}

type SystemSettingsPatchRequest struct {
	ExpectedRevision int64                  `json:"expected_revision"`
	Changes          map[string]interface{} `json:"changes" binding:"required"`
	Note             string                 `json:"note"`
	Confirmed        bool                   `json:"confirmed"`
}

type SystemSettingsView struct {
	Revision                 int64                               `json:"revision"`
	SchemaVersion            int                                 `json:"schema_version"`
	DeploymentEffectiveState string                              `json:"deployment_effective_state"`
	DesiredRevisionID        string                              `json:"desired_revision_id"`
	Groups                   map[string]map[string]interface{}   `json:"groups"`
	RuntimeInstances         []model.RuntimeSettingInstanceState `json:"runtime_instances"`
	SourcePolicies           map[string]SettingSourcePolicy      `json:"source_policies"`
	SecretReferences         map[string]map[string]interface{}   `json:"secret_references"`
}

func (s *Service) GetSystemSettings(ctx context.Context) (*SystemSettingsView, error) {
	revision, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]interface{}
	if revision == nil {
		snapshot = defaultSystemSettingsSnapshot()
	} else if err := json.Unmarshal([]byte(revision.SnapshotJSON), &snapshot); err != nil {
		return nil, httperr.Internal("invalid system settings snapshot")
	}
	snapshot = withDefaultSettingGroups(snapshot)
	groups, _ := snapshot["groups"].(map[string]interface{})
	viewGroups := map[string]map[string]interface{}{}
	for group, value := range groups {
		fields, _ := value.(map[string]interface{})
		viewGroups[group] = map[string]interface{}{}
		for field, fieldValue := range fields {
			policy, ok := settingKeyPolicies[group+"."+field]
			if !ok {
				continue
			}
			metadata := settingKeyValidators[group+"."+field]
			item := map[string]interface{}{
				"effective": fieldValue, "source": policy.Source,
				"editable": policy.Editable, "database_override": policy.DatabaseOverride,
				"restart_required": policy.RestartRequired, "risk_level": policy.RiskLevel,
				"impact": policy.Impact, "effective_mode": policy.EffectiveMode,
			}
			if metadata.Type != "" {
				item["type"] = metadata.Type
				item["min"] = metadata.Min
				item["max"] = metadata.Max
				if metadata.Allowed != nil {
					item["allowed"] = metadata.Allowed
				}
			}
			viewGroups[group][field] = item
		}
	}
	states, err := s.Store.ListRuntimeSettingInstanceStates(ctx)
	if err != nil {
		return nil, err
	}
	runtime := s.CurrentRuntimeConfig()
	for index := range states {
		states[index].HeartbeatState = runtimeHeartbeatState(states[index], runtime.HeartbeatTimeoutSec, time.Now().UTC())
	}
	view := &SystemSettingsView{
		SchemaVersion:            SystemSettingsSchemaVersion,
		Groups:                   viewGroups,
		RuntimeInstances:         states,
		SourcePolicies:           settingKeyPolicies,
		DeploymentEffectiveState: deploymentEffectiveState(revision, states, runtime.HeartbeatTimeoutSec),
		SecretReferences:         map[string]map[string]interface{}{},
	}
	if revision != nil {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(revision.SnapshotJSON), &raw); err == nil {
			references, _ := raw["secret_refs"].(map[string]interface{})
			for key, value := range references {
				reference, _ := value.(map[string]interface{})
				if reference == nil {
					continue
				}
				view.SecretReferences[key] = map[string]interface{}{
					"id": reference["id"], "version": reference["version"],
				}
			}
		}
	}
	if revision != nil {
		view.Revision = revision.Revision
		view.DesiredRevisionID = revision.ID
	}
	return view, nil
}

var allowedSettingSecretKeys = map[string]struct{}{
	"approval.notify_webhook_secret": {},
	"runtime.report_token":           {},
	"alerting.webhook_secret":        {},
	"observability.metrics_token":    {},
}

type SystemSettingSecretRequest struct {
	Secret    string `json:"secret" binding:"required"`
	Note      string `json:"note"`
	Confirmed bool   `json:"confirmed"`
}

type RuntimeSettingStateReport struct {
	RuntimeRevisionID string `json:"runtime_revision_id" binding:"required"`
	RuntimeInstanceID string `json:"runtime_instance_id" binding:"required"`
	InstanceIdentity  string `json:"instance_identity" binding:"required"`
	ApplyStatus       string `json:"apply_status" binding:"required"`
	ApplyError        string `json:"apply_error"`
}

func (s *Service) UpdateSystemSettingSecret(ctx context.Context, updatedBy, secretKey, plaintext, note string, confirmed bool) (*model.SettingRevision, error) {
	if _, ok := allowedSettingSecretKeys[secretKey]; !ok {
		return nil, httperr.BadRequest(40098, "unsupported system setting secret: "+secretKey)
	}
	if err := requireHighRiskConfirmation(confirmed, note, "secret rotation"); err != nil {
		return nil, err
	}
	current, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, httperr.New(409, 40901, "system settings are not initialized")
	}
	sealed, err := crypto.Encrypt(s.EncryptKey, plaintext)
	if err != nil {
		return nil, httperr.Internal("unable to seal system setting secret")
	}
	secret := &model.SettingSecretVersion{
		SecretKey:   secretKey,
		Ciphertext:  sealed,
		Fingerprint: hex.EncodeToString(hmacSha256(s.HMACKey, []byte(plaintext))),
		CreatedBy:   updatedBy,
	}
	now := time.Now().UTC()
	revision := &model.SettingRevision{
		SchemaVersion: SystemSettingsSchemaVersion, CreatedBy: updatedBy, UpdatedAt: now,
		Note: note,
	}
	if err := s.Store.CreateSettingSecretVersionWithPointer(ctx, secret, revision, current.ID); err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return nil, httperr.New(409, 40901, "system settings revision conflict")
		}
		return nil, err
	}
	var snapshot map[string]interface{}
	if err := json.Unmarshal([]byte(revision.SnapshotJSON), &snapshot); err != nil {
		return nil, httperr.Internal("invalid system settings snapshot")
	}
	if err := s.convergeLocalRuntime(ctx, revision, snapshot); err != nil {
		return nil, err
	}
	return revision, nil
}

func (s *Service) SettingSecretValue(ctx context.Context, secretKey string) (string, error) {
	version, err := s.Store.GetLatestSettingSecretVersion(ctx, secretKey)
	if err != nil || version == nil {
		return "", err
	}
	plaintext, err := crypto.Decrypt(s.EncryptKey, version.Ciphertext)
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

func hmacSha256(key, plaintext []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(plaintext)
	return mac.Sum(nil)
}

func deploymentEffectiveState(desired *model.SettingRevision, states []model.RuntimeSettingInstanceState, heartbeatTimeoutSec int) string {
	if desired == nil {
		return "not_initialized"
	}
	if len(states) == 0 {
		return "pending_apply"
	}
	for _, state := range states {
		if state.RuntimeRevisionID != desired.ID || state.ApplyStatus != model.RuntimeApplyApplied ||
			runtimeHeartbeatState(state, heartbeatTimeoutSec, time.Now().UTC()) != "live" {
			return "degraded"
		}
	}
	return "consistent"
}

func runtimeHeartbeatState(state model.RuntimeSettingInstanceState, timeoutSec int, now time.Time) string {
	if timeoutSec <= 0 {
		return "live"
	}
	if state.LastSeenAt.IsZero() || now.Sub(state.LastSeenAt.UTC()) > time.Duration(timeoutSec)*time.Second {
		return "stale"
	}
	return "live"
}

func (s *Service) SetRuntimeConfig(cfg config.Runtime) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.runtimeConfig = cfg
}

func (s *Service) CurrentRuntimeConfig() config.Runtime {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.runtimeConfig
}

func (s *Service) CurrentRuntimeReportToken() string {
	return s.CurrentRuntimeConfig().ReportToken
}

func validateSettingChange(key string, value interface{}) error {
	metadata, ok := settingKeyValidators[key]
	if !ok {
		return httperr.BadRequest(40098, "unsupported system setting: "+key)
	}
	switch metadata.Type {
	case "number":
		number, err := toFloat64(value)
		if err != nil {
			return httperr.BadRequest(40098, "system setting "+key+" must be numeric")
		}
		if number < metadata.Min || number > metadata.Max {
			return httperr.BadRequest(40098, fmt.Sprintf("system setting %s must be between %v and %v", key, metadata.Min, metadata.Max))
		}
	case "enum":
		text, _ := value.(string)
		for _, allowed := range metadata.Allowed {
			if text == allowed {
				return nil
			}
		}
		return httperr.BadRequest(40098, "system setting "+key+" has an invalid value")
	case "string":
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return httperr.BadRequest(40098, "system setting "+key+" must be a non-empty string")
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return httperr.BadRequest(40098, "system setting "+key+" must be a boolean")
		}
	case "string_list", "origin_list", "method_list", "cidr_list":
		texts, err := settingListStrings(value)
		if err != nil {
			return httperr.BadRequest(40098, "system setting "+key+" must be a string array")
		}
		if metadata.Type != "cidr_list" && len(texts) == 0 {
			return nil
		}
		for _, text := range texts {
			switch metadata.Type {
			case "origin_list":
				if err := validateOrigin(text); err != nil {
					return httperr.BadRequest(40098, "system setting "+key+" has an invalid origin: "+text)
				}
			case "method_list":
				if err := validateHTTPMethod(text); err != nil {
					return httperr.BadRequest(40098, "system setting "+key+" has an invalid HTTP method: "+text)
				}
			case "cidr_list":
				if _, _, err := net.ParseCIDR(text); err != nil {
					return httperr.BadRequest(40098, "system setting "+key+" has an invalid CIDR: "+text)
				}
			}
		}
	case "url":
		return validateURL(key, value)
	case "metrics_path":
		text, _ := value.(string)
		if !strings.HasPrefix(text, "/") || strings.Contains(text, "..") {
			return httperr.BadRequest(40098, "system setting "+key+" must be an absolute path")
		}
	case "webhook_list":
		items, ok := value.([]interface{})
		if !ok || len(items) > 20 {
			return httperr.BadRequest(40098, "system setting "+key+" must contain at most 20 webhook objects")
		}
		for _, item := range items {
			object, ok := item.(map[string]interface{})
			if !ok {
				return httperr.BadRequest(40098, "system setting "+key+" must contain webhook objects")
			}
			if value, ok := object["name"].(string); !ok || len(value) > 128 {
				return httperr.BadRequest(40098, "system setting "+key+" has an invalid webhook name")
			}
			if err := validateURL(key+".url", object["url"]); err != nil {
				return httperr.BadRequest(40098, "system setting "+key+" has an invalid webhook URL")
			}
			channelType, _ := object["type"].(string)
			switch strings.ToLower(strings.TrimSpace(channelType)) {
			case "", "generic", "wecom", "wechat-work", "dingtalk":
			default:
				return httperr.BadRequest(40098, "system setting "+key+" has an unsupported webhook type")
			}
			if _, ok := object["enabled"].(bool); !ok {
				return httperr.BadRequest(40098, "system setting "+key+" has an invalid webhook enabled flag")
			}
		}
	}
	return nil
}

func validateURL(key string, value interface{}) error {
	text, _ := value.(string)
	if text == "" {
		return nil
	}
	parsed, err := url.Parse(text)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return httperr.BadRequest(40098, "system setting "+key+" must be an http or https URL")
	}
	return nil
}

func settingListStrings(value interface{}) ([]string, error) {
	items, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("not a list")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, errors.New("not a string list")
		}
		result = append(result, text)
	}
	return result, nil
}

func validateOrigin(value string) error {
	if value == "*" {
		return errors.New("wildcard origin requires deployment configuration")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" {
		return errors.New("invalid origin")
	}
	return nil
}

func validateHTTPMethod(value string) error {
	switch value {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD":
		return nil
	default:
		return errors.New("unsupported HTTP method")
	}
}

func toFloat64(value interface{}) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case string:
		return strconv.ParseFloat(typed, 64)
	default:
		return 0, errors.New("not numeric")
	}
}

func requireHighRiskConfirmation(confirmed bool, note, operation string) error {
	if !confirmed {
		return httperr.New(409, 40902, operation+" requires explicit confirmation")
	}
	if len(strings.TrimSpace(note)) < 8 {
		return httperr.BadRequest(40098, operation+" requires an operation note of at least 8 characters")
	}
	return nil
}

func (s *Service) UpdateSystemSettings(ctx context.Context, updatedBy string, request SystemSettingsPatchRequest) (*model.SettingRevision, error) {
	revision, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]interface{}
	if revision == nil {
		snapshot = defaultSystemSettingsSnapshot()
	} else {
		if revision.Revision != request.ExpectedRevision {
			return nil, httperr.New(409, 40901, "system settings revision conflict")
		}
		if err := json.Unmarshal([]byte(revision.SnapshotJSON), &snapshot); err != nil {
			return nil, httperr.Internal("invalid system settings snapshot")
		}
	}
	snapshot = withDefaultSettingGroups(snapshot)
	highRiskChange := false
	for key, value := range request.Changes {
		if strings.Contains(strings.ToLower(key), "password") ||
			strings.Contains(strings.ToLower(key), "api_key") ||
			strings.Contains(strings.ToLower(key), "token") ||
			strings.Contains(strings.ToLower(key), "secret") {
			return nil, httperr.BadRequest(40098, "system setting secrets must use the credential API")
		}
		policy, ok := settingKeyPolicies[key]
		if !ok {
			return nil, httperr.BadRequest(40098, "unsupported system setting: "+key)
		}
		if policy.RiskLevel == "high" {
			highRiskChange = true
		}
		if !policy.Editable || !policy.DatabaseOverride {
			return nil, httperr.Forbidden("system setting cannot be overridden by database: " + key)
		}
		if err := validateSettingChange(key, value); err != nil {
			return nil, err
		}
		parts := strings.SplitN(key, ".", 2)
		groups, _ := snapshot["groups"].(map[string]interface{})
		group, _ := groups[parts[0]].(map[string]interface{})
		if group == nil {
			group = map[string]interface{}{}
			groups[parts[0]] = group
		}
		group[parts[1]] = value
	}
	if highRiskChange {
		if err := requireHighRiskConfirmation(request.Confirmed, request.Note, "high-risk system settings"); err != nil {
			return nil, err
		}
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	newRevision := &model.SettingRevision{
		SchemaVersion: SystemSettingsSchemaVersion,
		SnapshotJSON:  string(snapshotJSON),
		CreatedBy:     updatedBy, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		Note: request.Note, Checksum: canonicalJSONHash(snapshot),
	}
	expectedID := ""
	if revision != nil {
		expectedID = revision.ID
	}
	if err := s.Store.CreateSettingRevisionWithPointer(ctx, newRevision, expectedID); err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return nil, httperr.New(409, 40901, "system settings revision conflict")
		}
		return nil, err
	}
	if err := s.convergeLocalRuntime(ctx, newRevision, snapshot); err != nil {
		return nil, err
	}
	return newRevision, nil
}

func (s *Service) runtimeInstanceID() (string, string) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}
	instanceID := strings.TrimSpace(os.Getenv("RGX_RUNTIME_INSTANCE_ID"))
	if instanceID == "" {
		instanceID = "runtime-" + hostname
	}
	return instanceID, hostname
}

func (s *Service) markRuntimeState(ctx context.Context, revision *model.SettingRevision, status, applyError string) error {
	instanceID, identity := s.runtimeInstanceID()
	now := time.Now().UTC()
	return s.Store.UpsertRuntimeSettingInstanceState(ctx, &model.RuntimeSettingInstanceState{
		RuntimeInstanceID: instanceID, InstanceIdentity: identity,
		RuntimeRevisionID: revision.ID, ApplyStatus: status,
		ApplyError: applyError, LastSeenAt: now,
		CreatedAt: now, UpdatedAt: now,
	})
}

func (s *Service) convergeLocalRuntime(ctx context.Context, revision *model.SettingRevision, snapshot map[string]interface{}) error {
	if err := s.markRuntimeState(ctx, revision, model.RuntimeApplyPending, ""); err != nil {
		return err
	}
	if s.snapshotRequiresRestart(snapshot) {
		return s.markRuntimeState(ctx, revision, model.RuntimeApplyPendingRestart, "")
	}
	if err := s.ApplySystemSettingsSnapshot(snapshot); err != nil {
		return err
	}
	return s.markRuntimeState(ctx, revision, model.RuntimeApplyApplied, "")
}

func (s *Service) snapshotRequiresRestart(snapshot map[string]interface{}) bool {
	observability := s.CurrentObservabilityConfig()
	if settingBool(snapshot, "observability", "metrics_enabled", observability.MetricsEnabled) != observability.MetricsEnabled ||
		settingString(snapshot, "observability", "metrics_path", observability.MetricsPath) != observability.MetricsPath ||
		settingString(snapshot, "observability", "metrics_namespace", observability.MetricsNamespace) != observability.MetricsNamespace ||
		settingBool(snapshot, "observability", "tracing_enabled", observability.TracingEnabled) != observability.TracingEnabled ||
		settingString(snapshot, "observability", "otlp_endpoint", observability.OTLPEndpoint) != observability.OTLPEndpoint ||
		settingString(snapshot, "observability", "service_name", observability.ServiceName) != observability.ServiceName {
		return true
	}
	if value, err := toFloat64(settingGroup(snapshot, "observability")["sample_ratio"]); err == nil && value != observability.SampleRatio {
		return true
	}
	approval := s.ApprovalConfig()
	return settingNumber(snapshot, "approval", "expire_scan_interval_sec", approval.ExpireScanIntervalSec) != approval.ExpireScanIntervalSec
}

func (s *Service) EffectiveSystemSettingsConfig(ctx context.Context, cfg config.Config) (config.Config, error) {
	current, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return cfg, err
	}
	snapshot := defaultSystemSettingsSnapshot()
	if current != nil {
		if err := json.Unmarshal([]byte(current.SnapshotJSON), &snapshot); err != nil {
			return cfg, httperr.Internal("invalid system settings snapshot")
		}
	}
	snapshot = withDefaultSettingGroups(snapshot)
	security := cfg.Security
	security.AllowedOrigins = settingStringList(snapshot, "security", "allowed_origins", security.AllowedOrigins)
	security.AllowedMethods = settingStringList(snapshot, "security", "allowed_methods", security.AllowedMethods)
	security.AllowedHeaders = settingStringList(snapshot, "security", "allowed_headers", security.AllowedHeaders)
	security.AllowCredentials = settingBool(snapshot, "security", "allow_credentials", security.AllowCredentials)
	security.MaxAgeSec = settingNumber(snapshot, "security", "max_age_sec", security.MaxAgeSec)
	security.HSTSEnabled = settingBool(snapshot, "security", "hsts_enabled", security.HSTSEnabled)
	security.HSTSMaxAgeSec = settingNumber(snapshot, "security", "hsts_max_age_sec", security.HSTSMaxAgeSec)
	security.HSTSIncludeSubdomains = settingBool(snapshot, "security", "hsts_include_subdomains", security.HSTSIncludeSubdomains)
	security.ContentSecurityPolicy = settingString(snapshot, "security", "content_security_policy", security.ContentSecurityPolicy)
	security.FrameOptions = settingString(snapshot, "security", "frame_options", security.FrameOptions)
	security.MaxRequestBytes = int64(settingNumber(snapshot, "security", "max_request_bytes", int(security.MaxRequestBytes)))
	security.AllowPrivateProviderBaseURL = settingBool(snapshot, "security", "allow_private_provider_base_url", security.AllowPrivateProviderBaseURL)
	cfg.Security = security

	observability := cfg.Observability
	observability.MetricsEnabled = settingBool(snapshot, "observability", "metrics_enabled", observability.MetricsEnabled)
	observability.MetricsPath = settingString(snapshot, "observability", "metrics_path", observability.MetricsPath)
	observability.MetricsNamespace = settingString(snapshot, "observability", "metrics_namespace", observability.MetricsNamespace)
	observability.MetricsAllowCIDRs = settingStringList(snapshot, "observability", "metrics_allow_cidrs", observability.MetricsAllowCIDRs)
	observability.TracingEnabled = settingBool(snapshot, "observability", "tracing_enabled", observability.TracingEnabled)
	observability.OTLPEndpoint = settingString(snapshot, "observability", "otlp_endpoint", observability.OTLPEndpoint)
	observability.ServiceName = settingString(snapshot, "observability", "service_name", observability.ServiceName)
	if value, err := toFloat64(settingGroup(snapshot, "observability")["sample_ratio"]); err == nil {
		observability.SampleRatio = value
	}
	if token, err := s.SettingSecretValue(ctx, "observability.metrics_token"); err == nil {
		observability.MetricsToken = token
	}
	cfg.Observability = observability

	approval := cfg.Approval
	approval.Enabled = settingBool(snapshot, "approval", "enabled", approval.Enabled)
	approval.DefaultExpireHours = settingNumber(snapshot, "approval", "default_expire_hours", approval.DefaultExpireHours)
	approval.ExecutionMaxRetries = settingNumber(snapshot, "approval", "execution_max_retries", approval.ExecutionMaxRetries)
	approval.ExpireScanIntervalSec = settingNumber(snapshot, "approval", "expire_scan_interval_sec", approval.ExpireScanIntervalSec)
	approval.ReminderBeforeHours = settingNumber(snapshot, "approval", "reminder_before_hours", approval.ReminderBeforeHours)
	approval.RetentionDays = settingNumber(snapshot, "approval", "retention_days", approval.RetentionDays)
	approval.PolicyCacheTTLSec = settingNumber(snapshot, "approval", "policy_cache_ttl_sec", approval.PolicyCacheTTLSec)
	approval.NotifyWebhook = settingString(snapshot, "approval", "notify_webhook", approval.NotifyWebhook)
	if secret, err := s.SettingSecretValue(ctx, "approval.notify_webhook_secret"); err == nil {
		approval.NotifyWebhookSecret = secret
	}
	cfg.Approval = approval

	runtime := cfg.Runtime
	runtime.HeartbeatTimeoutSec = settingNumber(snapshot, "runtime", "heartbeat_timeout_sec", runtime.HeartbeatTimeoutSec)
	if token, err := s.SettingSecretValue(ctx, "runtime.report_token"); err == nil {
		runtime.ReportToken = token
	}
	cfg.Runtime = runtime

	alerting := cfg.Alerting
	alerting.Enabled = settingBool(snapshot, "alerting", "enabled", alerting.Enabled)
	alerting.ThrottleSec = settingNumber(snapshot, "alerting", "throttle_sec", alerting.ThrottleSec)
	alerting.Webhooks = settingWebhookList(snapshot, "alerting", "webhooks", alerting.Webhooks)
	if secret, err := s.SettingSecretValue(ctx, "alerting.webhook_secret"); err == nil {
		for index := range alerting.Webhooks {
			alerting.Webhooks[index].Secret = secret
		}
	}
	cfg.Alerting = alerting
	if err := s.ApplySystemSettingsSnapshot(snapshot); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Service) InitializeSystemSettingsFromConfig(ctx context.Context, cfg config.Config) error {
	current, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return err
	}
	if current != nil {
		var snapshot map[string]interface{}
		if err := json.Unmarshal([]byte(current.SnapshotJSON), &snapshot); err != nil {
			return httperr.Internal("invalid system settings snapshot")
		}
		if err := s.ApplySystemSettingsSnapshot(snapshot); err != nil {
			return err
		}
		return s.importConfigSecrets(ctx, cfg)
	}
	snapshot := systemSettingsSnapshotFromConfig(cfg)
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	revision := &model.SettingRevision{
		ID: id.New(), Revision: 1, SchemaVersion: SystemSettingsSchemaVersion,
		SnapshotJSON: string(snapshotJSON), RevisionStatus: model.SettingRevisionStatusCurrent,
		CreatedBy: "system-bootstrap", CreatedAt: now, UpdatedAt: now,
		Note: "initial settings from deployment configuration", Checksum: canonicalJSONHash(snapshot),
	}
	if err := s.Store.CreateSettingRevisionWithPointer(ctx, revision, ""); err != nil {
		return err
	}
	if err := s.importConfigSecrets(ctx, cfg); err != nil {
		return err
	}
	return s.markRuntimeState(ctx, revision, model.RuntimeApplyApplied, "")
}

func (s *Service) importConfigSecrets(ctx context.Context, cfg config.Config) error {
	alertingSecret := ""
	for _, webhook := range cfg.Alerting.Webhooks {
		if strings.TrimSpace(webhook.Secret) != "" {
			alertingSecret = webhook.Secret
			break
		}
	}
	imports := []struct {
		key       string
		plaintext string
	}{
		{key: "observability.metrics_token", plaintext: cfg.Observability.MetricsToken},
		{key: "approval.notify_webhook_secret", plaintext: cfg.Approval.NotifyWebhookSecret},
		{key: "runtime.report_token", plaintext: cfg.Runtime.ReportToken},
		{key: "alerting.webhook_secret", plaintext: alertingSecret},
	}
	for _, item := range imports {
		if strings.TrimSpace(item.plaintext) == "" {
			continue
		}
		latest, err := s.Store.GetLatestSettingSecretVersion(ctx, item.key)
		if err != nil {
			return err
		}
		fingerprint := hex.EncodeToString(hmacSha256(s.HMACKey, []byte(item.plaintext)))
		if latest != nil && latest.Fingerprint == fingerprint {
			continue
		}
		if _, err := s.UpdateSystemSettingSecret(ctx, "system-bootstrap", item.key, item.plaintext, "import deployment secret", true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListSystemSettingRevisions(ctx context.Context, page, pageSize int) ([]model.SettingRevision, int64, error) {
	return s.Store.ListSettingRevisions(ctx, page, pageSize)
}

func (s *Service) ReportRuntimeSettingState(ctx context.Context, request RuntimeSettingStateReport) (*model.RuntimeSettingInstanceState, error) {
	instanceID := strings.TrimSpace(request.RuntimeInstanceID)
	identity := strings.TrimSpace(request.InstanceIdentity)
	applyError := strings.TrimSpace(request.ApplyError)
	if instanceID == "" || len(instanceID) > 64 {
		return nil, httperr.BadRequest(40099, "runtime instance id must contain 1 to 64 characters")
	}
	if identity == "" || len(identity) > 255 {
		return nil, httperr.BadRequest(40099, "runtime instance identity must contain 1 to 255 characters")
	}
	if len(applyError) > 8192 {
		return nil, httperr.BadRequest(40099, "runtime apply error is too long")
	}
	allowed := map[string]struct{}{
		model.RuntimeApplyApplying:       {},
		model.RuntimeApplyApplied:        {},
		model.RuntimeApplyFailed:         {},
		model.RuntimeApplyPendingRestart: {},
	}
	if _, ok := allowed[request.ApplyStatus]; !ok {
		return nil, httperr.BadRequest(40099, "unsupported runtime apply status")
	}
	revision, err := s.Store.GetSettingRevision(ctx, request.RuntimeRevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, httperr.NotFound("system setting revision not found")
	}
	current, err := s.Store.GetSettingCurrentPointer(ctx)
	if err != nil {
		return nil, err
	}
	if current == nil || current.DesiredRevisionID != revision.ID {
		return nil, httperr.New(409, 40901, "runtime state must reference the current system settings revision")
	}
	now := time.Now().UTC()
	state := &model.RuntimeSettingInstanceState{
		RuntimeInstanceID: instanceID, InstanceIdentity: identity,
		RuntimeRevisionID: revision.ID, ApplyStatus: request.ApplyStatus,
		ApplyError: applyError, LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.ReportRuntimeSettingInstanceState(ctx, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Service) GetSystemSettingRevision(ctx context.Context, id string) (*model.SettingRevision, error) {
	revision, err := s.Store.GetSettingRevision(ctx, id)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, httperr.NotFound("system setting revision not found")
	}
	return revision, nil
}

func (s *Service) RollbackSystemSettings(ctx context.Context, updatedBy, revisionID, note string, confirmed bool) (*model.SettingRevision, error) {
	target, err := s.GetSystemSettingRevision(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	if err := requireHighRiskConfirmation(confirmed, note, "settings rollback"); err != nil {
		return nil, err
	}
	current, err := s.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, httperr.New(409, 40901, "system settings are not initialized")
	}
	var snapshot map[string]interface{}
	if err := json.Unmarshal([]byte(target.SnapshotJSON), &snapshot); err != nil {
		return nil, httperr.Internal("invalid system settings snapshot")
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	now := time.Now().UTC()
	newRevision := &model.SettingRevision{
		SchemaVersion: SystemSettingsSchemaVersion, SnapshotJSON: string(snapshotJSON),
		CreatedBy: updatedBy, CreatedAt: now, UpdatedAt: now,
		Note:     strings.TrimSpace(note),
		Checksum: canonicalJSONHash(snapshot), RollbackFromRevisionID: target.ID,
	}
	if err := s.Store.CreateSettingRevisionWithPointer(ctx, newRevision, current.ID); err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return nil, httperr.New(409, 40901, "system settings revision conflict")
		}
		return nil, err
	}
	if err := s.convergeLocalRuntime(ctx, newRevision, snapshot); err != nil {
		return nil, err
	}
	return newRevision, nil
}
