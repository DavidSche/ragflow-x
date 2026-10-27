// Package obs wires Prometheus metrics and OpenTelemetry tracing for the whole
// server. It is always safe to call (returns a no-op default when unconfigured)
// so middlewares never need nil checks.
package obs

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.28.0"
	"go.opentelemetry.io/otel/trace"
	noop "go.opentelemetry.io/otel/trace/noop"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Observability bundles the process-wide metrics registry and tracer.
type Observability struct {
	registry *prometheus.Registry

	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	rateLimitRejected   prometheus.Counter
	limiterFailOpen     prometheus.Counter

	ragflowRequestsTotal   *prometheus.CounterVec
	ragflowRequestDuration *prometheus.HistogramVec

	jobEnqueuedTotal  *prometheus.CounterVec
	jobProcessedTotal *prometheus.CounterVec
	jobQueueDepth     prometheus.Gauge
	jobFailed         prometheus.Gauge

	retentionRunTotal    prometheus.Counter
	retentionPurgedTotal *prometheus.CounterVec

	approvalSubmittedTotal                    *prometheus.CounterVec
	approvalExecutionTotal                    *prometheus.CounterVec
	approvalPending                           prometheus.Gauge
	alertDeliveryCompensationScannedTotal     prometheus.Counter
	alertDeliveryCompensationCompensatedTotal prometheus.Counter
	alertDeliveryCompensationSkippedTotal     prometheus.Counter
	alertDeliveryCompensationAbandonedTotal   prometheus.Counter
	alertDeliveryCompensationFencedTotal      prometheus.Counter

	memoryDispatchExternalBindTotal   *prometheus.CounterVec
	memoryCompensationScannedTotal    prometheus.Counter
	memoryCompensationResultTotal     *prometheus.CounterVec
	memoryCompensationPassTotal       prometheus.Counter
	memoryCompensationPassFailedTotal prometheus.Counter

	compilationUpstreamStallAge *prometheus.GaugeVec
	compilationUnknownAge       *prometheus.GaugeVec
	enhancementBudgetRejected   *prometheus.CounterVec

	// 137 §11.1 Security/Permission Gate metrics (M211).
	memorySearchAuthorizedFilterTotal *prometheus.CounterVec
	contextPreModelAuthTotal          *prometheus.CounterVec
	memoryPrincipalMappingTotal       *prometheus.CounterVec
	memoryUnauthorizedDispatchTotal   *prometheus.CounterVec
	memoryRevocationEventTotal        *prometheus.CounterVec
	// 137 §11.1 Operational Alert metrics (M211).
	compilationRunTerminalTotal      *prometheus.CounterVec
	compilationReadyEvalTotal        *prometheus.CounterVec
	compilationArtifactBindingTotal  *prometheus.CounterVec
	compilationProjectionStaleTotal  prometheus.Counter
	compilationQueueDepth            *prometheus.GaugeVec
	retrievalModeTotal               *prometheus.CounterVec
	memoryExternalResultTotal        *prometheus.CounterVec
	memoryPendingInvalidatedTotal    *prometheus.CounterVec
	memoryTombstoneVerificationTotal *prometheus.CounterVec
	memoryPrivacyGateTotal           *prometheus.CounterVec
	contextDecisionDuration          *prometheus.HistogramVec

	governanceProjectionTotal         *prometheus.CounterVec
	governanceExcludedDocsTotal       *prometheus.CounterVec
	governanceEmptyGuardTotal         prometheus.Counter
	governanceDictionaryApprovalTotal *prometheus.CounterVec
	governanceAutoProjectionTotal     *prometheus.CounterVec
	attributionFirewallTotal          *prometheus.CounterVec

	resourceSyncRunProgress    *prometheus.GaugeVec
	resourceSyncItemsTotal     *prometheus.CounterVec
	resourceSyncStaleRunsTotal *prometheus.CounterVec

	// doc/225 §10: data-source connector sync metrics.
	connectorRunsTotal        *prometheus.CounterVec
	connectorRunDuration      *prometheus.HistogramVec
	connectorSyncItemsTotal   *prometheus.CounterVec
	connectorCredentialFailed prometheus.Counter
	connectorActive           prometheus.Gauge
	connectorRunsPruned       *prometheus.CounterVec

	metricsEnabled bool

	tracer    trace.Tracer
	hasTracer bool
	shutdown  func(context.Context) error

	// otlpEndpoint keeps the configured collector host (sans scheme and path)
	// so persisted trace pointers can build deep links to the tracing UI.
	otlpEndpoint string

	// traceUIURLTemplate is the configured deep-link template (doc/104 §15);
	// empty when unset so evidence only carries raw ids.
	traceUIURLTemplate string
}

var (
	mu     sync.RWMutex
	global *Observability
)

// New builds an Observability from config. Tracing is only wired when both
// enabled and an OTLP endpoint is set; otherwise a no-op tracer is used.
func New(cfg config.Observability) *Observability {
	namespace := cfg.MetricsNamespace
	if namespace == "" {
		namespace = "ragflow_x"
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	o := &Observability{
		registry:       registry,
		metricsEnabled: cfg.MetricsEnabled,
		shutdown:       func(context.Context) error { return nil },
	}

	if cfg.MetricsEnabled {
		o.httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "http_requests_total",
			Help: "Total HTTP requests by method, route and status.",
		}, []string{"method", "route", "status"})
		o.httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"})
		o.rateLimitRejected = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "rate_limit_rejected_total",
			Help: "Total requests rejected by the rate limiter.",
		})
		o.limiterFailOpen = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "rate_limiter_failopen_total",
			Help: "Total attempts allowed because the rate limiter backend was unavailable (fail-open degradation).",
		})
		o.ragflowRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "ragflow",
			Name: "requests_total",
			Help: "Total RAGFlow provider requests by method, path and status.",
		}, []string{"method", "path", "status"})
		o.ragflowRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "ragflow",
			Name:    "request_duration_seconds",
			Help:    "RAGFlow provider request duration in seconds by method and path.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path"})
		o.jobEnqueuedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "worker",
			Name: "job_enqueued_total",
			Help: "Total async jobs enqueued by kind.",
		}, []string{"kind"})
		o.jobProcessedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "worker",
			Name: "job_processed_total",
			Help: "Total async jobs reconciled by kind and outcome status.",
		}, []string{"kind", "status"})
		o.jobQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "worker",
			Name: "job_queue_depth",
			Help: "Number of queued/running async jobs.",
		})
		o.jobFailed = prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "worker",
			Name: "job_failed",
			Help: "Number of async jobs that exhausted retries.",
		})
		o.retentionRunTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: "retention_run_total",
			Help: "Total data-retention janitor cycles (doc/33 A5).",
		})
		o.retentionPurgedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "retention_purged_rows_total",
			Help: "Total rows purged by the retention janitor per class (audit/usage/feedback/job).",
		}, []string{"class"})
		o.approvalSubmittedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "approval",
			Name: "submitted_total",
			Help: "Total approval requests submitted by object and action.",
		}, []string{"object_type", "action"})
		o.approvalExecutionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "approval",
			Name: "execution_total",
			Help: "Total approval executions by object, action and result.",
		}, []string{"object_type", "action", "result"})
		o.approvalPending = prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "approval",
			Name: "pending",
			Help: "Number of approvals waiting for a decision.",
		})
		o.alertDeliveryCompensationScannedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "alert_delivery",
			Name: "compensation_scanned_total",
			Help: "Total alert delivery rows scanned by compensation passes.",
		})
		o.alertDeliveryCompensationCompensatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "alert_delivery",
			Name: "compensation_compensated_total",
			Help: "Total alert delivery rows successfully compensated.",
		})
		o.alertDeliveryCompensationSkippedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "alert_delivery",
			Name: "compensation_skipped_total",
			Help: "Total alert delivery rows skipped because another worker held an active lease.",
		})
		o.alertDeliveryCompensationAbandonedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "alert_delivery",
			Name: "compensation_abandoned_total",
			Help: "Total alert delivery rows abandoned after exhausting compensation policy.",
		})
		o.alertDeliveryCompensationFencedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "alert_delivery",
			Name: "compensation_fenced_total",
			Help: "Total alert delivery results rejected because the lease generation or owner was no longer valid.",
		})
		o.memoryDispatchExternalBindTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_dispatch",
			Name: "external_bind_total",
			Help: "Total Memory dispatch external-message identity binding outcomes by result.",
		}, []string{"result"})
		o.memoryCompensationScannedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_tombstone",
			Name: "compensation_scanned_total",
			Help: "Total Memory tombstone operations claimed for compensation.",
		})
		o.memoryCompensationResultTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_tombstone",
			Name: "compensation_result_total",
			Help: "Total Memory tombstone compensation outcomes by result.",
		}, []string{"result"})
		o.memoryCompensationPassTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_tombstone",
			Name: "compensation_pass_total",
			Help: "Total Memory tombstone compensation worker passes.",
		})
		o.memoryCompensationPassFailedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_tombstone",
			Name: "compensation_pass_failed_total",
			Help: "Total Memory tombstone compensation passes containing provider or state errors.",
		})
		o.compilationUpstreamStallAge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "compilation",
			Name: "upstream_stall_seconds",
			Help: "Age of the most recent upstream pending/running compilation status by tenant and kind.",
		}, []string{"tenant_id", "kind"})
		o.compilationUnknownAge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "compilation",
			Name: "unknown_age_seconds",
			Help: "Age of the most recent unknown compilation run by tenant and kind.",
		}, []string{"tenant_id", "kind"})
		o.enhancementBudgetRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "enhancement",
			Name: "budget_rejected_total",
			Help: "Total enhancement tasks rejected by budget governance or cost-accounting failures by tenant and scope.",
		}, []string{"tenant_id", "scope"})
		o.resourceSyncRunProgress = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "resource_sync",
			Name: "run_progress",
			Help: "Current resource sync run progress by source and kind (total, done, failed).",
		}, []string{"source_id", "kind"})
		o.resourceSyncItemsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "resource_sync",
			Name: "items_total",
			Help: "Total resource sync items processed by type and terminal status.",
		}, []string{"resource_type", "source_id", "status"})
		o.resourceSyncStaleRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "resource_sync",
			Name: "stale_runs_total",
			Help: "Total stale resource sync runs recovered.",
		}, []string{"source_id"})
		o.connectorRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "connector",
			Name: "runs_total",
			Help: "Total connector sync runs by provider, trigger and terminal status (doc/225 §10).",
		}, []string{"provider", "trigger", "status"})
		o.connectorRunDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "connector",
			Name:    "run_duration_seconds",
			Help:    "Connector sync run duration in seconds by provider and status.",
			Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1800},
		}, []string{"provider", "status"})
		o.connectorSyncItemsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "connector",
			Name: "sync_items_total",
			Help: "Connector sync items by outcome (added/changed/deleted/skipped/failed).",
		}, []string{"outcome"})
		o.connectorCredentialFailed = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "connector",
			Name: "credential_failure_total",
			Help: "Total connector credential decryption failures (fail-closed, doc/225 F1).",
		})
		o.connectorActive = prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "connector",
			Name: "active_connectors",
			Help: "Number of connector bindings currently visible to the control plane.",
		})
		o.connectorRunsPruned = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "connector",
			Name: "runs_pruned_total",
			Help: "Connector run-history rows pruned by the retention janitor per provider.",
		}, []string{"provider"})
		o.memorySearchAuthorizedFilterTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_search",
			Name: "authorized_filter_total",
			Help: "Outbound governed memory searches by authorization filter outcome (137 §11.1).",
		}, []string{"result"})
		o.contextPreModelAuthTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "context",
			Name: "pre_model_auth_total",
			Help: "Pre-model context authorization outcomes by result (137 §11.1).",
		}, []string{"result"})
		o.memoryPrincipalMappingTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_dispatch",
			Name: "principal_mapping_total",
			Help: "Memory principal mapping outcomes by result and classification reason (137 §11.1).",
		}, []string{"result", "reason"})
		o.memoryUnauthorizedDispatchTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_dispatch",
			Name: "unauthorized_external_dispatch_total",
			Help: "External dispatch attempts observed without a valid admission state; must stay zero (137 §11.1).",
		}, []string{"operation_type"})
		o.memoryRevocationEventTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_dispatch",
			Name: "revocation_event_total",
			Help: "Sampled Memory revocation events by kind (137 §11.1).",
		}, []string{"kind"})
		o.compilationRunTerminalTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "compilation",
			Name: "run_terminal_total",
			Help: "Compilation run state transitions to outcome states by tenant and result (137 §11.1).",
		}, []string{"tenant_id", "result"})
		o.compilationReadyEvalTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "compilation",
			Name: "ready_eval_total",
			Help: "Compilation artifact readiness evaluations by tenant and result (137 §11.1).",
		}, []string{"tenant_id", "result"})
		o.compilationArtifactBindingTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "compilation",
			Name: "artifact_binding_total",
			Help: "Compilation artifact run/content hash binding verifications by tenant and result (137 §11.1).",
		}, []string{"tenant_id", "result"})
		o.compilationProjectionStaleTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "compilation",
			Name: "projection_stale_total",
			Help: "Artifact projections marked stale by replacement invalidation (137 §11.1).",
		})
		o.compilationQueueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "compilation",
			Name: "queue_depth",
			Help: "Upstream compilation queue Inflight and Backlog depth by tenant and kind (137 §11.1).",
		}, []string{"tenant_id", "kind", "dimension"})
		o.retrievalModeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "retrieval",
			Name: "mode_total",
			Help: "Retrieval executions by mode and result, including compiled_first raw fallback (137 §11.1).",
		}, []string{"mode", "result"})
		o.memoryExternalResultTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_dispatch",
			Name: "external_result_total",
			Help: "Memory external dispatch terminal results by operation type and result (137 §11.1).",
		}, []string{"operation_type", "result"})
		o.memoryPendingInvalidatedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_dispatch",
			Name: "pending_invalidated_total",
			Help: "Accepted memory operations invalidated before external dispatch by reason class (137 §11.1).",
		}, []string{"reason_class"})
		o.memoryTombstoneVerificationTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_tombstone",
			Name: "verification_total",
			Help: "Tombstone enforcement verification outcomes by result (137 §11.1).",
		}, []string{"result"})
		o.memoryPrivacyGateTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "memory_privacy",
			Name: "gate_decision_total",
			Help: "Deterministic privacy gate decisions on dispatch payloads by decision (137 §11.1).",
		}, []string{"decision"})
		o.governanceProjectionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "document_governance",
			Name: "projection_total",
			Help: "Document-level governance projection outcomes (doc/224 T2): ok\u002Fexcluded\u002Ffiltered_out\u002Fdegraded.",
		}, []string{"outcome"})
		o.governanceExcludedDocsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "document_governance",
			Name: "excluded_docs_total",
			Help: "Documents excluded by governance projection by exclusion reason (doc/224 T2).",
		}, []string{"reason"})
		o.governanceEmptyGuardTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "document_governance",
			Name: "empty_guard_total",
			Help: "Retrieval executions short-circuited by the empty-set fail-safe guard (doc/224 T2).",
		})
		o.governanceDictionaryApprovalTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "document_governance",
			Name: "dictionary_approval_total",
			Help: "Controlled-vocabulary dictionary decisions by category and decision (doc/224 T1).",
		}, []string{"category", "decision"})
		o.governanceAutoProjectionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "document_governance",
			Name: "auto_projection_total",
			Help: "Auto governance projection outcomes by outcome label (doc/224 T1): matched/quality_skipped/manual_override/synced/degraded/dormant.",
		}, []string{"outcome"})
		o.attributionFirewallTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "answer_attribution",
			Name: "firewall_total",
			Help: "Generation attribution firewall decisions by verdict (doc/224 T3.3): allowed/warn/block — claims carrying a specific assertion with no verified citation.",
		}, []string{"verdict"})
		o.contextDecisionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "context",
			Name:    "decision_duration_seconds",
			Help:    "Pre-model context decision latency by request kind; 300ms budget (137 §11.1).",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 1, 2.5, 5, 10},
		}, []string{"request_kind"})
		registry.MustRegister(o.httpRequestsTotal, o.httpRequestDuration, o.rateLimitRejected, o.limiterFailOpen,
			o.ragflowRequestsTotal, o.ragflowRequestDuration, o.jobEnqueuedTotal, o.jobProcessedTotal,
			o.jobQueueDepth, o.jobFailed, o.retentionRunTotal, o.retentionPurgedTotal,
			o.approvalSubmittedTotal, o.approvalExecutionTotal, o.approvalPending,
			o.alertDeliveryCompensationScannedTotal, o.alertDeliveryCompensationCompensatedTotal,
			o.alertDeliveryCompensationSkippedTotal, o.alertDeliveryCompensationAbandonedTotal,
			o.alertDeliveryCompensationFencedTotal, o.memoryDispatchExternalBindTotal,
			o.memoryCompensationScannedTotal, o.memoryCompensationResultTotal,
			o.memoryCompensationPassTotal, o.memoryCompensationPassFailedTotal,
			o.compilationUpstreamStallAge, o.compilationUnknownAge, o.enhancementBudgetRejected,
			o.resourceSyncRunProgress,
			o.resourceSyncItemsTotal, o.resourceSyncStaleRunsTotal,
			o.connectorRunsTotal, o.connectorRunDuration, o.connectorSyncItemsTotal,
			o.connectorCredentialFailed, o.connectorActive, o.connectorRunsPruned,
			o.memorySearchAuthorizedFilterTotal, o.contextPreModelAuthTotal,
			o.memoryPrincipalMappingTotal, o.memoryUnauthorizedDispatchTotal,
			o.memoryRevocationEventTotal, o.compilationRunTerminalTotal,
			o.compilationReadyEvalTotal, o.compilationArtifactBindingTotal,
			o.compilationProjectionStaleTotal, o.compilationQueueDepth,
			o.retrievalModeTotal, o.memoryExternalResultTotal,
			o.memoryPendingInvalidatedTotal, o.memoryTombstoneVerificationTotal,
			o.memoryPrivacyGateTotal, o.contextDecisionDuration,
			o.governanceProjectionTotal, o.governanceExcludedDocsTotal, o.governanceEmptyGuardTotal,
			o.governanceDictionaryApprovalTotal, o.governanceAutoProjectionTotal,
			o.attributionFirewallTotal)
	}

	o.setupTracer(cfg)
	return o
}

func (o *Observability) setupTracer(cfg config.Observability) {
	o.tracer = noop.NewTracerProvider().Tracer("ragflow-x")
	// The deep-link template is pure configuration: it must be available even
	// when local tracing is off but an upstream system publishes otel ids.
	o.traceUIURLTemplate = normalizeTraceUIURLTemplate(cfg.TraceUIURLTemplate)
	if !cfg.TracingEnabled || cfg.OTLPEndpoint == "" {
		return
	}
	expr, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint),
	)
	if err != nil {
		logger.Warn("otlp exporter init failed; tracing disabled", "error", err)
		return
	}
	sample := cfg.SampleRatio
	if sample <= 0 {
		sample = 1.0
	}
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sample))),
		sdktrace.WithBatcher(expr),
		sdktrace.WithResource(res),
	)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}),
	)
	o.tracer = tp.Tracer(cfg.ServiceName)
	o.hasTracer = true
	o.otlpEndpoint = otlpEndpointHost(cfg.OTLPEndpoint)
	o.shutdown = func(ctx context.Context) error { return tp.Shutdown(ctx) }
}

// normalizeTraceUIURLTemplate keeps only http(s) templates: anything else
// (javascript:, data:, ...) must never become a persisted deep link.
func normalizeTraceUIURLTemplate(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}
	if !strings.Contains(value, "{trace_id}") {
		return ""
	}
	return value
}

// OTelTraceUILink renders the configured trace UI deep link for a trace/span
// pair; empty when no template is configured (doc/104 §15).
func OTelTraceUILink(traceID, spanID string) string {
	template := Get().traceUIURLTemplate
	if template == "" || traceID == "" || spanID == "" {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(template,
		"{trace_id}", traceID), "{span_id}", spanID)
}

// otlpEndpointHost strips scheme, userinfo and path so only the host:port of
// the collector remains — safe to persist as a deep-link pointer.
func otlpEndpointHost(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "https://"), "http://")
	if index := strings.IndexAny(value, "/?#"); index >= 0 {
		value = value[:index]
	}
	return value
}

// SpanContextFromContext returns the hex trace and span ids of the current
// span so persistence layers can store deep-link pointers without depending
// on the trace SDK directly. valid is false when tracing is disabled or no
// valid span is attached to ctx.
func SpanContextFromContext(ctx context.Context) (traceID, spanID string, valid bool) {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", "", false
	}
	return sc.TraceID().String(), sc.SpanID().String(), true
}

// OTelEndpointHost exposes the configured collector host for trace pointers;
// empty when tracing is not configured.
func OTelEndpointHost() string {
	return Get().otlpEndpoint
}

// Set installs the process-wide instance (e.g. at startup).
func Set(o *Observability) {
	mu.Lock()
	global = o
	mu.Unlock()
}

// Get returns the process-wide instance, defaulting to a safe no-op.
func Get() *Observability {
	mu.RLock()
	defer mu.RUnlock()
	if global == nil {
		return New(config.Observability{})
	}
	return global
}

// Middleware records HTTP metrics and, when tracing is enabled, starts a span
// propagated from incoming trace context.
func (o *Observability) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		var span trace.Span
		if o.hasTracer {
			ctx, s := o.tracer.Start(c.Request.Context(), "http "+c.Request.Method+" "+route,
				trace.WithSpanKind(trace.SpanKindServer))
			span = s
			if !span.SpanContext().IsValid() {
				span = nil
			}
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()

		if span != nil {
			span.SetAttributes(
				attribute.String("http.request.method", c.Request.Method),
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", c.Writer.Status()),
			)
			span.End()
		}

		if o.metricsEnabled && o.httpRequestsTotal != nil {
			status := strconv.Itoa(c.Writer.Status())
			o.httpRequestsTotal.WithLabelValues(c.Request.Method, route, status).Inc()
			o.httpRequestDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
		}
	}
}

// MetricsHandler exposes the Prometheus registry via Gin.
func (o *Observability) MetricsHandler() gin.HandlerFunc {
	h := promhttp.HandlerFor(o.registry, promhttp.HandlerOpts{})
	return func(c *gin.Context) { h.ServeHTTP(c.Writer, c.Request) }
}

// IncRateLimitReject records a rate-limit rejection (used by middleware).
func (o *Observability) IncRateLimitReject() {
	if o.metricsEnabled && o.rateLimitRejected != nil {
		o.rateLimitRejected.Inc()
	}
}

// IncLimiterFailOpen records an attempt allowed because the limiter backend
// was unavailable — the security-relevant fail-open degradation must stay visible.
func (o *Observability) IncLimiterFailOpen() {
	if o.metricsEnabled && o.limiterFailOpen != nil {
		o.limiterFailOpen.Inc()
	}
}

// IncRagFlowRequest increments the RAGFlow provider request counter.
func (o *Observability) IncRagFlowRequest(method, path, status string) {
	if o.metricsEnabled && o.ragflowRequestsTotal != nil {
		o.ragflowRequestsTotal.WithLabelValues(method, path, status).Inc()
	}
}

// ObserveRagFlowDuration records the duration of a RAGFlow provider request.
func (o *Observability) ObserveRagFlowDuration(method, path string, seconds float64) {
	if o.metricsEnabled && o.ragflowRequestDuration != nil {
		o.ragflowRequestDuration.WithLabelValues(method, path).Observe(seconds)
	}
}

// IncJobEnqueued records an async job enqueued by kind (worker).
func (o *Observability) IncJobEnqueued(kind string) {
	if o.metricsEnabled && o.jobEnqueuedTotal != nil {
		o.jobEnqueuedTotal.WithLabelValues(kind).Inc()
	}
}

// IncJobProcessed records an async job outcome by kind and status (worker).
func (o *Observability) IncJobProcessed(kind, status string) {
	if o.metricsEnabled && o.jobProcessedTotal != nil {
		o.jobProcessedTotal.WithLabelValues(kind, status).Inc()
	}
}

// SetJobQueueDepth publishes the async queue depth (queued + running).
func (o *Observability) SetJobQueueDepth(depth int64) {
	if o.metricsEnabled && o.jobQueueDepth != nil {
		o.jobQueueDepth.Set(float64(depth))
	}
}

// SetJobFailed publishes the number of async jobs that exhausted retries.
func (o *Observability) SetJobFailed(count int64) {
	if o.metricsEnabled && o.jobFailed != nil {
		o.jobFailed.Set(float64(count))
	}
}

// IncRetentionRun records a data-retention janitor cycle (doc/33 A5).
func (o *Observability) IncRetentionRun() {
	if o.metricsEnabled && o.retentionRunTotal != nil {
		o.retentionRunTotal.Inc()
	}
}

// IncApprovalSubmitted records a newly submitted approval request.
func (o *Observability) IncApprovalSubmitted(objectType, action string) {
	if o.metricsEnabled && o.approvalSubmittedTotal != nil {
		o.approvalSubmittedTotal.WithLabelValues(objectType, action).Inc()
	}
}

// IncApprovalExecution records a terminal approval execution outcome.
func (o *Observability) IncApprovalExecution(objectType, action, result string) {
	if o.metricsEnabled && o.approvalExecutionTotal != nil {
		o.approvalExecutionTotal.WithLabelValues(objectType, action, result).Inc()
	}
}

// SetApprovalPending publishes the current pending approval backlog.
func (o *Observability) SetApprovalPending(count int64) {
	if o.metricsEnabled && o.approvalPending != nil {
		o.approvalPending.Set(float64(count))
	}
}

// AddAlertDeliveryCompensation publishes one compensation pass outcome.
func (o *Observability) AddAlertDeliveryCompensation(scanned, compensated, skipped, abandoned, fenced int64) {
	if !o.metricsEnabled {
		return
	}
	if o.alertDeliveryCompensationScannedTotal != nil {
		o.alertDeliveryCompensationScannedTotal.Add(float64(scanned))
	}
	if o.alertDeliveryCompensationCompensatedTotal != nil {
		o.alertDeliveryCompensationCompensatedTotal.Add(float64(compensated))
	}
	if o.alertDeliveryCompensationSkippedTotal != nil {
		o.alertDeliveryCompensationSkippedTotal.Add(float64(skipped))
	}
	if o.alertDeliveryCompensationAbandonedTotal != nil {
		o.alertDeliveryCompensationAbandonedTotal.Add(float64(abandoned))
	}
	if o.alertDeliveryCompensationFencedTotal != nil {
		o.alertDeliveryCompensationFencedTotal.Add(float64(fenced))
	}
}

// AddMemoryDispatchExternalBind records whether a successful provider write
// could be bound to a durable external message identity.
func (o *Observability) AddMemoryDispatchExternalBind(result string) {
	if o.metricsEnabled && o.memoryDispatchExternalBindTotal != nil {
		o.memoryDispatchExternalBindTotal.WithLabelValues(result).Inc()
	}
}

// AddMemoryCompensation publishes one bounded tombstone compensation pass.
func (o *Observability) AddMemoryCompensation(
	scanned, compensated, absent, pending, skipped, failed int64,
) {
	if !o.metricsEnabled {
		return
	}
	if o.memoryCompensationScannedTotal != nil {
		o.memoryCompensationScannedTotal.Add(float64(scanned))
	}
	if o.memoryCompensationResultTotal != nil {
		o.memoryCompensationResultTotal.WithLabelValues("compensated").Add(float64(compensated))
		o.memoryCompensationResultTotal.WithLabelValues("absent").Add(float64(absent))
		o.memoryCompensationResultTotal.WithLabelValues("pending").Add(float64(pending))
		o.memoryCompensationResultTotal.WithLabelValues("skipped").Add(float64(skipped))
	}
	if o.memoryCompensationPassTotal != nil {
		o.memoryCompensationPassTotal.Inc()
	}
	if o.memoryCompensationPassFailedTotal != nil && failed > 0 {
		o.memoryCompensationPassFailedTotal.Inc()
	}
}

// SetCompilationUpstreamStallAge publishes the age of a non-terminal
// compilation status. The gauge is cleared when reconciliation reaches a
// terminal decision for the same tenant/kind series.
func (o *Observability) SetCompilationUpstreamStallAge(tenantID, kind string, seconds float64) {
	if !o.metricsEnabled || o.compilationUpstreamStallAge == nil {
		return
	}
	o.compilationUpstreamStallAge.WithLabelValues(tenantID, kind).Set(seconds)
}

// ClearCompilationUpstreamStallAge removes a terminal series so completed or
// unknown recovery states cannot keep a stale stall alert alive.
func (o *Observability) ClearCompilationUpstreamStallAge(tenantID, kind string) {
	if !o.metricsEnabled || o.compilationUpstreamStallAge == nil {
		return
	}
	_ = o.compilationUpstreamStallAge.DeleteLabelValues(tenantID, kind)
}

// SetCompilationUnknownAge publishes the age of the most recent unknown run.
func (o *Observability) SetCompilationUnknownAge(tenantID, kind string, seconds float64) {
	if !o.metricsEnabled || o.compilationUnknownAge == nil {
		return
	}
	o.compilationUnknownAge.WithLabelValues(tenantID, kind).Set(seconds)
}

// ClearCompilationUnknownAge removes the unknown series after reconciliation.
func (o *Observability) ClearCompilationUnknownAge(tenantID, kind string) {
	if !o.metricsEnabled || o.compilationUnknownAge == nil {
		return
	}
	_ = o.compilationUnknownAge.DeleteLabelValues(tenantID, kind)
}

// IncEnhancementBudgetRejected records budget enforcement or cost accounting
// failures without leaking document identities or payload details.
func (o *Observability) IncEnhancementBudgetRejected(tenantID, scope string) {
	if !o.metricsEnabled || o.enhancementBudgetRejected == nil {
		return
	}
	o.enhancementBudgetRejected.WithLabelValues(tenantID, scope).Inc()
}

// AddMemorySearchAuthorizedFilter counts one governed memory search by
// authorization filter outcome (137 §11.1 Security Gate).
func (o *Observability) AddMemorySearchAuthorizedFilter(result string) {
	if !o.metricsEnabled || o.memorySearchAuthorizedFilterTotal == nil {
		return
	}
	o.memorySearchAuthorizedFilterTotal.WithLabelValues(result).Inc()
}

// AddContextPreModelAuth counts one pre-model context authorization by result
// (137 §11.1 Security Gate).
func (o *Observability) AddContextPreModelAuth(result string) {
	if !o.metricsEnabled || o.contextPreModelAuthTotal == nil {
		return
	}
	o.contextPreModelAuthTotal.WithLabelValues(result).Inc()
}

// AddMemoryPrincipalMapping counts one principal mapping outcome; reason is
// empty on success and the failure classification otherwise (137 §11.1).
func (o *Observability) AddMemoryPrincipalMapping(result, reason string) {
	if !o.metricsEnabled || o.memoryPrincipalMappingTotal == nil {
		return
	}
	o.memoryPrincipalMappingTotal.WithLabelValues(result, reason).Inc()
}

// IncMemoryUnauthorizedExternalDispatch counts an external dispatch observed
// without a valid admission state. The counter is a fail-closed tripwire and
// must stay at zero (137 §11.1 Security Gate).
func (o *Observability) IncMemoryUnauthorizedExternalDispatch(operationType string) {
	if !o.metricsEnabled || o.memoryUnauthorizedDispatchTotal == nil {
		return
	}
	o.memoryUnauthorizedDispatchTotal.WithLabelValues(operationType).Inc()
}

// AddMemoryRevocationEvent samples one Memory revocation event by kind (owner
// opt-out, personal opt-out, member exit, tombstone) (137 §11.1).
func (o *Observability) AddMemoryRevocationEvent(kind string) {
	if !o.metricsEnabled || o.memoryRevocationEventTotal == nil {
		return
	}
	o.memoryRevocationEventTotal.WithLabelValues(kind).Inc()
}

// AddCompilationRunTerminal counts one compilation run outcome transition by
// tenant and result (137 §11.1 Operational Alert).
func (o *Observability) AddCompilationRunTerminal(tenantID, result string) {
	if !o.metricsEnabled || o.compilationRunTerminalTotal == nil {
		return
	}
	o.compilationRunTerminalTotal.WithLabelValues(tenantID, result).Inc()
}

// AddCompilationReadyEval counts one artifact readiness evaluation by tenant
// and result (137 §11.1 Operational Alert).
func (o *Observability) AddCompilationReadyEval(tenantID, result string) {
	if !o.metricsEnabled || o.compilationReadyEvalTotal == nil {
		return
	}
	o.compilationReadyEvalTotal.WithLabelValues(tenantID, result).Inc()
}

// AddCompilationArtifactBinding counts one artifact run/content hash binding
// verification by tenant and result (137 §11.1 Operational Alert).
func (o *Observability) AddCompilationArtifactBinding(tenantID, result string) {
	if !o.metricsEnabled || o.compilationArtifactBindingTotal == nil {
		return
	}
	o.compilationArtifactBindingTotal.WithLabelValues(tenantID, result).Inc()
}

// RecordDocumentGovernanceProjection counts one governance projection outcome.
// Outcomes: ok (no exclusions), excluded (≥1 excluded by clearance/draft/
// inactive), filtered_out (empty-set guard short-circuit), degraded
// (projection could not be evaluated; fail-open).
func (o *Observability) RecordDocumentGovernanceProjection(outcome string, excludedDocs int) {
	if !o.metricsEnabled || o.governanceProjectionTotal == nil {
		return
	}
	o.governanceProjectionTotal.WithLabelValues(outcome).Inc()
	o.governanceExcludedDocsTotal.WithLabelValues("all").Add(float64(excludedDocs))
	if outcome != "filtered_out" {
		return
	}
	if o.governanceEmptyGuardTotal == nil {
		return
	}
	o.governanceEmptyGuardTotal.Inc()
}

func (o *Observability) RecordAttributionFirewall(verdict string) {
	if !o.metricsEnabled || o.attributionFirewallTotal == nil {
		return
	}
	o.attributionFirewallTotal.WithLabelValues(verdict).Inc()
}

// RecordGovernanceDictionaryApproval counts a controlled-vocabulary dictionary
// decision (doc/224 T1).
func (o *Observability) RecordGovernanceDictionaryApproval(category, decision string) {
	if !o.metricsEnabled || o.governanceDictionaryApprovalTotal == nil {
		return
	}
	o.governanceDictionaryApprovalTotal.WithLabelValues(category, decision).Inc()
}

// RecordGovernanceAutoProjection counts one auto governance projection outcome
// (doc/224 T1): matched/quality_skipped/manual_override/synced/degraded/dormant.
func (o *Observability) RecordGovernanceAutoProjection(outcome string) {
	if !o.metricsEnabled || o.governanceAutoProjectionTotal == nil {
		return
	}
	o.governanceAutoProjectionTotal.WithLabelValues(outcome).Inc()
}

// AddCompilationProjectionStale counts artifact projections marked stale by
// replacement invalidation (137 §11.1 Operational Alert).
func (o *Observability) AddCompilationProjectionStale(count int64) {
	if !o.metricsEnabled || o.compilationProjectionStaleTotal == nil {
		return
	}
	o.compilationProjectionStaleTotal.Add(float64(count))
}

// SetCompilationQueueDepth publishes upstream Inflight and Backlog depth for
// one tenant/kind compilation queue (137 §11.1 Operational Alert).
func (o *Observability) SetCompilationQueueDepth(tenantID, kind string, inflight, backlog int64) {
	if !o.metricsEnabled || o.compilationQueueDepth == nil {
		return
	}
	o.compilationQueueDepth.WithLabelValues(tenantID, kind, "inflight").Set(float64(inflight))
	o.compilationQueueDepth.WithLabelValues(tenantID, kind, "backlog").Set(float64(backlog))
}

// ClearCompilationQueueDepth removes the queue gauges for one tenant/kind
// once the upstream queue is no longer active (137 §11.1).
func (o *Observability) ClearCompilationQueueDepth(tenantID, kind string) {
	if !o.metricsEnabled || o.compilationQueueDepth == nil {
		return
	}
	o.compilationQueueDepth.DeleteLabelValues(tenantID, kind, "inflight")
	o.compilationQueueDepth.DeleteLabelValues(tenantID, kind, "backlog")
}

// AddRetrievalMode counts one retrieval execution by mode and result,
// including compiled_first degrading to raw-only evidence (137 §11.1).
func (o *Observability) AddRetrievalMode(mode, result string) {
	if !o.metricsEnabled || o.retrievalModeTotal == nil {
		return
	}
	o.retrievalModeTotal.WithLabelValues(mode, result).Inc()
}

// AddMemoryExternalResult counts one memory external dispatch terminal result
// by operation type (137 §11.1 Operational Alert).
func (o *Observability) AddMemoryExternalResult(operationType, result string) {
	if !o.metricsEnabled || o.memoryExternalResultTotal == nil {
		return
	}
	o.memoryExternalResultTotal.WithLabelValues(operationType, result).Inc()
}

// AddMemoryPendingInvalidated counts one accepted memory operation invalidated
// before external dispatch by reason class (137 §11.1 Operational Alert).
func (o *Observability) AddMemoryPendingInvalidated(reasonClass string) {
	if !o.metricsEnabled || o.memoryPendingInvalidatedTotal == nil {
		return
	}
	o.memoryPendingInvalidatedTotal.WithLabelValues(reasonClass).Inc()
}

// AddMemoryTombstoneVerification counts one tombstone enforcement verification
// outcome (137 §11.1 Operational Alert).
func (o *Observability) AddMemoryTombstoneVerification(result string) {
	if !o.metricsEnabled || o.memoryTombstoneVerificationTotal == nil {
		return
	}
	o.memoryTombstoneVerificationTotal.WithLabelValues(result).Inc()
}

// AddMemoryPrivacyGateDecision counts one deterministic privacy gate decision
// on a real dispatch payload (137 §11.1 Operational Alert).
func (o *Observability) AddMemoryPrivacyGateDecision(decision string) {
	if !o.metricsEnabled || o.memoryPrivacyGateTotal == nil {
		return
	}
	o.memoryPrivacyGateTotal.WithLabelValues(decision).Inc()
}

// ObserveContextDecisionDuration records one pre-model context decision
// latency sample by request kind; budget is 300ms (137 §11.1).
func (o *Observability) ObserveContextDecisionDuration(requestKind string, seconds float64) {
	if !o.metricsEnabled || o.contextDecisionDuration == nil {
		return
	}
	o.contextDecisionDuration.WithLabelValues(requestKind).Observe(seconds)
}

// RecordConnectorRun records one terminal connector sync run.
func (o *Observability) RecordConnectorRun(provider, trigger, status string, seconds float64) {
	if !o.metricsEnabled || o.connectorRunsTotal == nil {
		return
	}
	o.connectorRunsTotal.WithLabelValues(provider, trigger, status).Inc()
	o.connectorRunDuration.WithLabelValues(provider, status).Observe(seconds)
}

// AddConnectorSyncItems records connector sync item outcomes.
func (o *Observability) AddConnectorSyncItems(outcome string, count int64) {
	if !o.metricsEnabled || o.connectorSyncItemsTotal == nil || count == 0 {
		return
	}
	o.connectorSyncItemsTotal.WithLabelValues(outcome).Add(float64(count))
}

// IncConnectorCredentialFailure records a fail-closed credential decrypt error.
func (o *Observability) IncConnectorCredentialFailure() {
	if !o.metricsEnabled || o.connectorCredentialFailed == nil {
		return
	}
	o.connectorCredentialFailed.Inc()
}

// SetConnectorActiveConnectors publishes the connector binding count.
func (o *Observability) SetConnectorActiveConnectors(count int64) {
	if !o.metricsEnabled || o.connectorActive == nil {
		return
	}
	o.connectorActive.Set(float64(count))
}

// AddConnectorRunsPruned records retention-janitor deletions.
func (o *Observability) AddConnectorRunsPruned(provider string, count int64) {
	if !o.metricsEnabled || o.connectorRunsPruned == nil || count == 0 {
		return
	}
	o.connectorRunsPruned.WithLabelValues(provider).Add(float64(count))
}

// SetResourceSyncRunProgress publishes the current resource sync run counters.
func (o *Observability) SetResourceSyncRunProgress(sourceID string, total, done, failed int64) {
	if !o.metricsEnabled || o.resourceSyncRunProgress == nil {
		return
	}
	o.resourceSyncRunProgress.WithLabelValues(sourceID, "total").Set(float64(total))
	o.resourceSyncRunProgress.WithLabelValues(sourceID, "done").Set(float64(done))
	o.resourceSyncRunProgress.WithLabelValues(sourceID, "failed").Set(float64(failed))
}

// ClearResourceSyncRunProgress removes progress series after terminal handling
// so completed runs cannot remain visible to stall alert expressions.
func (o *Observability) ClearResourceSyncRunProgress(sourceID string) {
	if !o.metricsEnabled || o.resourceSyncRunProgress == nil {
		return
	}
	o.resourceSyncRunProgress.DeleteLabelValues(sourceID, "total")
	o.resourceSyncRunProgress.DeleteLabelValues(sourceID, "done")
	o.resourceSyncRunProgress.DeleteLabelValues(sourceID, "failed")
}

// AddResourceSyncItems records terminal resource sync item outcomes.
func (o *Observability) AddResourceSyncItems(sourceID, resourceType, status string, count int64) {
	if !o.metricsEnabled || o.resourceSyncItemsTotal == nil || count == 0 {
		return
	}
	o.resourceSyncItemsTotal.WithLabelValues(resourceType, sourceID, status).Add(float64(count))
}

// AddResourceSyncStaleRuns records stale resource sync runs recovered by a worker.
func (o *Observability) AddResourceSyncStaleRuns(sourceID string, count int64) {
	if !o.metricsEnabled || o.resourceSyncStaleRunsTotal == nil || count == 0 {
		return
	}
	o.resourceSyncStaleRunsTotal.WithLabelValues(sourceID).Add(float64(count))
}

// IncRetentionPurged records rows purged by the retention janitor, grouped
// by class (audit/usage/feedback/job).
func (o *Observability) IncRetentionPurged(class string, rows int64) {
	if o.metricsEnabled && o.retentionPurgedTotal != nil && rows > 0 {
		o.retentionPurgedTotal.WithLabelValues(class).Add(float64(rows))
	}
}

// Shutdown flushes tracing spans and releases resources.
func (o *Observability) Shutdown(ctx context.Context) error {
	return o.shutdown(ctx)
}
