// Package obs wires Prometheus metrics and OpenTelemetry tracing for the whole
// server. It is always safe to call (returns a no-op default when unconfigured)
// so middlewares never need nil checks.
package obs

import (
	"context"
	"strconv"
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

	resourceSyncRunProgress    *prometheus.GaugeVec
	resourceSyncItemsTotal     *prometheus.CounterVec
	resourceSyncStaleRunsTotal *prometheus.CounterVec

	metricsEnabled bool

	tracer    trace.Tracer
	hasTracer bool
	shutdown  func(context.Context) error
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
		registry.MustRegister(o.httpRequestsTotal, o.httpRequestDuration, o.rateLimitRejected, o.limiterFailOpen,
			o.ragflowRequestsTotal, o.ragflowRequestDuration, o.jobEnqueuedTotal, o.jobProcessedTotal,
			o.jobQueueDepth, o.jobFailed, o.retentionRunTotal, o.retentionPurgedTotal,
			o.approvalSubmittedTotal, o.approvalExecutionTotal, o.approvalPending,
			o.alertDeliveryCompensationScannedTotal, o.alertDeliveryCompensationCompensatedTotal,
			o.alertDeliveryCompensationSkippedTotal, o.alertDeliveryCompensationAbandonedTotal,
			o.alertDeliveryCompensationFencedTotal, o.resourceSyncRunProgress,
			o.resourceSyncItemsTotal, o.resourceSyncStaleRunsTotal)
	}

	o.setupTracer(cfg)
	return o
}

func (o *Observability) setupTracer(cfg config.Observability) {
	o.tracer = noop.NewTracerProvider().Tracer("ragflow-x")
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
	o.shutdown = func(ctx context.Context) error { return tp.Shutdown(ctx) }
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
