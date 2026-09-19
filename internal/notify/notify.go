// Package notify dispatches alert events to outbound channels. Webhook is the
// first channel; the design allows adding email/WeCom/DingTalk later behind the
// same Notifier interface. A hub throttles duplicates so noisy alerts do not
// flood webhook endpoints.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Event is a single alert occurrence.
type Event struct {
	ID         string
	Title      string
	Severity   string // info | warn | error | critical
	Type       string
	TenantID   string
	Resource   string
	ResourceID string
	Detail     string
	OccurredAt time.Time
	Fields     map[string]string
}

func (e Event) fingerprint() string {
	return e.Type + "|" + e.Resource + "|" + e.ResourceID + "|" + e.TenantID
}

func (e Event) payload() map[string]any {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now()
	}
	return map[string]any{
		"id":          e.ID,
		"title":       e.Title,
		"severity":    e.Severity,
		"type":        e.Type,
		"tenant_id":   e.TenantID,
		"resource":    e.Resource,
		"resource_id": e.ResourceID,
		"detail":      e.Detail,
		"occurred_at": e.OccurredAt.UTC().Format(time.RFC3339),
		"fields":      e.Fields,
	}
}

// Notifier sends an event to a single channel.
type Notifier interface {
	Send(ctx context.Context, ev Event) error
	Name() string
}

// Sink persists events for the in-app alert worklist.
type Sink interface {
	RecordAlert(ctx context.Context, ev Event) error
}

// DeliverySink can optionally persist the restartable delivery lifecycle.
type DeliverySink interface {
	RecordAlertDeliveryPending(ctx context.Context, ev Event, notifier string) error
	RecordAlertDeliveryResult(ctx context.Context, ev Event, notifier string, attempts int, err error) error
}

// DeliveryLease carries the fencing identity acquired before retry delivery.
type DeliveryLease struct {
	Owner      string
	Generation int64
}

// LeasedDeliverySink can optionally persist compensation results only when the
// retrying worker still owns the lease generation it claimed.
type LeasedDeliverySink interface {
	RecordAlertDeliveryLeaseResult(
		ctx context.Context, ev Event, notifier string, attempts int,
		lease DeliveryLease, err error,
	) (bool, error)
}

// EventMatcher allows a wrapper notifier to declare whether it handles an event
// before delivery side effects begin.
type EventMatcher interface {
	Handles(ev Event) bool
}

// filteredNotifier delivers only events accepted by match.
type filteredNotifier struct {
	Notifier
	match func(Event) bool
}

func (n filteredNotifier) Send(ctx context.Context, ev Event) error {
	if !n.match(ev) {
		return nil
	}
	return n.Notifier.Send(ctx, ev)
}

func (n filteredNotifier) Handles(ev Event) bool {
	return n.match(ev)
}

// WebhookNotifier delivers events as JSON POSTs, optionally HMAC-SHA256 signed.
type WebhookNotifier struct {
	name   string
	url    string
	secret string
	client *http.Client
	format webhookFormat

	timeout        time.Duration
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

const (
	defaultWebhookTimeout        = time.Duration(config.DefaultWebhookTimeoutSec) * time.Second
	defaultWebhookMaxAttempts    = config.DefaultWebhookMaxAttempts
	defaultWebhookInitialBackoff = time.Duration(config.DefaultWebhookBackoffInitialMs) * time.Millisecond
	defaultWebhookMaxBackoff     = time.Duration(config.DefaultWebhookBackoffMaxMs) * time.Millisecond
)

type DeliveryError struct {
	Notifier   string
	Attempts   int
	Retryable  bool
	RetryAfter time.Duration
	LastError  error
}

func (e *DeliveryError) Error() string {
	if e.LastError == nil {
		return fmt.Sprintf("webhook %s delivery failed after %d attempts", e.Notifier, e.Attempts)
	}
	return fmt.Sprintf("webhook %s delivery failed after %d attempts: %v", e.Notifier, e.Attempts, e.LastError)
}

func (e *DeliveryError) Unwrap() error {
	return e.LastError
}

type webhookStatusError struct {
	statusCode int
	retryAfter time.Duration
}

func (e webhookStatusError) Error() string {
	return fmt.Sprintf("webhook returned %d", e.statusCode)
}

type WebhookOption func(*WebhookNotifier)

func WithTimeout(timeout time.Duration) WebhookOption {
	return func(w *WebhookNotifier) {
		if timeout > 0 {
			w.timeout = timeout
		}
	}
}

func WithRetryPolicy(maxAttempts int, initialBackoff, maxBackoff time.Duration) WebhookOption {
	return func(w *WebhookNotifier) {
		if maxAttempts < 1 {
			maxAttempts = 1
		}
		if initialBackoff < 0 {
			initialBackoff = 0
		}
		if maxBackoff < initialBackoff {
			maxBackoff = initialBackoff
		}
		w.maxAttempts = maxAttempts
		w.initialBackoff = initialBackoff
		w.maxBackoff = maxBackoff
	}
}

// NewWebhook builds a webhook notifier.
func NewWebhook(name, url, secret string, options ...WebhookOption) *WebhookNotifier {
	result := &WebhookNotifier{
		name:           name,
		url:            url,
		secret:         secret,
		client:         &http.Client{Timeout: defaultWebhookTimeout},
		timeout:        defaultWebhookTimeout,
		maxAttempts:    defaultWebhookMaxAttempts,
		initialBackoff: defaultWebhookInitialBackoff,
		maxBackoff:     defaultWebhookMaxBackoff,
	}
	for _, option := range options {
		option(result)
	}
	return result
}

// NewChannel builds the outbound notifier for a configured alert webhook.
// It keeps one lifecycle/retry model for generic HTTP, WeCom and DingTalk.
func NewChannel(cfg config.Webhook, options ...WebhookOption) Notifier {
	notifier := NewWebhook(cfg.Name, cfg.URL, cfg.Secret, options...)
	notifier.format = normalizeChannelType(cfg.Type)
	return notifier
}

// Name returns the notifier's friendly name for logs.
func (w *WebhookNotifier) Name() string { return w.name }

// Send posts the event to the configured URL, signing the body when a secret
// is set.
func (w *WebhookNotifier) Send(ctx context.Context, ev Event) error {
	if w.url == "" {
		return &DeliveryError{Notifier: w.name, LastError: errors.New("webhook url is required")}
	}
	body, err := json.Marshal(w.payload(ev))
	if err != nil {
		return &DeliveryError{Notifier: w.name, LastError: err}
	}

	for attempt := 1; attempt <= w.maxAttempts; attempt++ {
		err := w.sendAttempt(ctx, body, ev.ID)
		if err == nil {
			return nil
		}
		var statusErr webhookStatusError
		_ = errors.As(err, &statusErr)
		if attempt == w.maxAttempts || ctx.Err() != nil || !isRetryableWebhookError(err) {
			return &DeliveryError{Notifier: w.name, Attempts: attempt, Retryable: isRetryableWebhookError(err) && ctx.Err() == nil, RetryAfter: statusErr.retryAfter, LastError: err}
		}
		delay := w.backoffDelay(attempt)
		if statusErr.retryAfter > delay {
			delay = statusErr.retryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return &DeliveryError{Notifier: w.name, Attempts: attempt, LastError: ctx.Err()}
		case <-timer.C:
		}
	}

	return &DeliveryError{Notifier: w.name, LastError: errors.New("webhook delivery policy allows no attempts")}
}

func (w *WebhookNotifier) sendAttempt(ctx context.Context, body []byte, alertID string) error {
	attemptCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	targetURL := w.url
	if w.format == webhookFormatDingTalk && w.secret != "" {
		targetURL = w.signedDingTalkURL()
	}
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ragflow-x-alert/1.0")
	if alertID != "" {
		req.Header.Set("Idempotency-Key", "alert:"+alertID)
		req.Header.Set("X-RagflowX-Alert-ID", alertID)
	}
	if w.secret != "" && w.format == webhookFormatGeneric {
		mac := hmac.New(sha256.New, []byte(w.secret))
		_, _ = mac.Write(body)
		req.Header.Set("X-RagflowX-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	response, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return webhookStatusError{statusCode: resp.StatusCode, retryAfter: retryAfterDuration(resp.Header.Get("Retry-After"), time.Now())}
	}
	if w.format != webhookFormatGeneric {
		if err := checkEnterpriseRobotResponse(response); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebhookNotifier) backoffDelay(attempt int) time.Duration {
	shift := attempt - 1
	if shift > 4 {
		shift = 4
	}
	delay := w.initialBackoff << shift
	if delay > w.maxBackoff {
		return w.maxBackoff
	}
	return delay
}

func retryAfterDuration(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := retryAt.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

func isRetryableWebhookError(err error) bool {
	var robotErr *enterpriseRobotError
	if errors.As(err, &robotErr) {
		return false
	}
	var statusErr webhookStatusError
	if errors.As(err, &statusErr) {
		return statusErr.statusCode == http.StatusRequestTimeout ||
			statusErr.statusCode == http.StatusTooEarly ||
			statusErr.statusCode == http.StatusTooManyRequests ||
			statusErr.statusCode >= http.StatusInternalServerError
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// Hub fans events out to configured notifiers with per-key throttling and a
// bounded in-memory queue so alerting never blocks the request path.
type Hub struct {
	enabled   bool
	throttle  time.Duration
	notifiers []Notifier
	sink      Sink

	queue chan Event
	stop  chan struct{}
	wg    sync.WaitGroup

	tmu  sync.Mutex
	last map[string]time.Time
}

// NewHub builds a Hub from config.
func NewHub(cfg config.Alerting, approval config.Approval) *Hub {
	h := &Hub{
		enabled:  cfg.Enabled || approval.NotifyWebhook != "",
		throttle: time.Duration(cfg.ThrottleSec) * time.Second,
		queue:    make(chan Event, 256),
		stop:     make(chan struct{}),
		last:     map[string]time.Time{},
	}
	for _, wh := range cfg.Webhooks {
		if !wh.Enabled || wh.URL == "" {
			continue
		}
		h.notifiers = append(h.notifiers, NewChannel(wh, webhookOptionsFromConfig(wh)...))
	}
	if cfg.Email.Enabled {
		h.notifiers = append(h.notifiers, NewEmail("email", cfg.Email))
	}
	if approval.NotifyWebhook != "" {
		h.notifiers = append(h.notifiers, filteredNotifier{
			Notifier: NewWebhook("approval-notify", approval.NotifyWebhook, approval.NotifyWebhookSecret),
			match: func(ev Event) bool {
				return strings.HasPrefix(ev.Type, "approval.")
			},
		})
	}
	h.wg.Add(1)
	go h.run()
	return h
}

func webhookOptionsFromConfig(cfg config.Webhook) []WebhookOption {
	timeout := defaultWebhookTimeout
	if cfg.TimeoutSec > 0 {
		timeout = time.Duration(cfg.TimeoutSec) * time.Second
	}
	maxAttempts := defaultWebhookMaxAttempts
	if cfg.MaxAttempts > 0 {
		maxAttempts = cfg.MaxAttempts
	}
	initialBackoff := defaultWebhookInitialBackoff
	if cfg.BackoffInitialMs > 0 {
		initialBackoff = time.Duration(cfg.BackoffInitialMs) * time.Millisecond
	}
	maxBackoff := defaultWebhookMaxBackoff
	if cfg.BackoffMaxMs > 0 {
		maxBackoff = time.Duration(cfg.BackoffMaxMs) * time.Millisecond
	}
	return []WebhookOption{
		WithTimeout(timeout),
		WithRetryPolicy(maxAttempts, initialBackoff, maxBackoff),
	}
}

// NewHubWithNotifiers builds a Hub with caller-provided notifiers (used by
// tests and programmatic wiring).
func NewHubWithNotifiers(notifiers []Notifier, throttle time.Duration) *Hub {
	h := &Hub{
		enabled:   true,
		throttle:  throttle,
		notifiers: notifiers,
		queue:     make(chan Event, 64),
		stop:      make(chan struct{}),
		last:      map[string]time.Time{},
	}
	h.wg.Add(1)
	go h.run()
	return h
}

// Notify enqueues an event for delivery, throttling duplicate fingerprints.
func (h *Hub) Notify(ctx context.Context, ev Event) {
	if !h.enabled || len(h.notifiers) == 0 {
		return
	}
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	fp := ev.fingerprint()
	h.tmu.Lock()
	if last, ok := h.last[fp]; ok && time.Since(last) < h.throttle {
		h.tmu.Unlock()
		return
	}
	h.last[fp] = time.Now()
	h.tmu.Unlock()

	select {
	case h.queue <- ev:
	default: // queue full: drop rather than block the caller
	}
}

func (h *Hub) run() {
	defer h.wg.Done()
	for {
		select {
		case <-h.stop:
			return
		case ev := <-h.queue:
			h.deliver(ev)
		}
	}
}

func (h *Hub) deliver(ev Event) {
	if h.sink != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.sink.RecordAlert(ctx, ev); err != nil {
			logger.Warn("alert persistence failed", "type", ev.Type, "error", err)
		}
		cancel()
	}
	h.send(ev)
}

// SetSink attaches the persistent alert worklist sink.
func (h *Hub) SetSink(sink Sink) {
	h.sink = sink
}

func (h *Hub) send(ev Event) {
	ctx := context.Background()
	recorder, _ := h.sink.(DeliverySink)
	for _, n := range h.notifiers {
		if matcher, ok := n.(EventMatcher); ok && !matcher.Handles(ev) {
			continue
		}
		h.deliverTo(ctx, recorder, n, ev)
	}
}

// Retry redelivers one event to one named channel without consulting throttle
// state. It is intended for durable compensation after a process restart.
func (h *Hub) Retry(ctx context.Context, ev Event, channel string) bool {
	if h == nil || !h.enabled || ev.ID == "" {
		return false
	}
	recorder, _ := h.sink.(DeliverySink)
	for _, notifier := range h.notifiers {
		if notifier.Name() != channel {
			continue
		}
		if matcher, ok := notifier.(EventMatcher); ok && !matcher.Handles(ev) {
			return false
		}
		h.deliverTo(ctx, recorder, notifier, ev)
		return true
	}
	return false
}

// RetryLeased redelivers one event under an existing delivery lease. It never
// writes a fresh pending state, so a slow worker cannot resurrect a row that a
// newer lease owner has already taken over. leaseValid is false when the fenced
// sink rejects the result or when no fencing sink is configured.
func (h *Hub) RetryLeased(ctx context.Context, ev Event, channel string, lease DeliveryLease) (bool, bool) {
	if h == nil || !h.enabled || ev.ID == "" {
		return false, false
	}
	recorder, _ := h.sink.(LeasedDeliverySink)
	for _, notifier := range h.notifiers {
		if notifier.Name() != channel {
			continue
		}
		if matcher, ok := notifier.(EventMatcher); ok && !matcher.Handles(ev) {
			return true, false
		}
		err := notifier.Send(ctx, ev)
		leaseValid := false
		if recorder != nil {
			valid, resultErr := recorder.RecordAlertDeliveryLeaseResult(
				ctx, ev, notifier.Name(), deliveryAttempts(err), lease, err,
			)
			if resultErr != nil {
				logger.Warn("alert delivery persistence failed",
					"webhook", notifier.Name(), "stage", "fenced-result", "error", resultErr)
			}
			leaseValid = resultErr == nil && valid
		}
		if err != nil {
			logger.Warn("alert notification failed",
				"webhook", notifier.Name(), "type", ev.Type, "error", err)
		}
		return true, leaseValid
	}
	return false, false
}

func (h *Hub) deliverTo(ctx context.Context, recorder DeliverySink, notifier Notifier, ev Event) {
	if recorder != nil {
		if err := recorder.RecordAlertDeliveryPending(ctx, ev, notifier.Name()); err != nil {
			logger.Warn("alert delivery persistence failed", "webhook", notifier.Name(), "stage", "pending", "error", err)
		}
	}
	err := notifier.Send(ctx, ev)
	if recorder != nil {
		if resultErr := recorder.RecordAlertDeliveryResult(ctx, ev, notifier.Name(), deliveryAttempts(err), err); resultErr != nil {
			logger.Warn("alert delivery persistence failed", "webhook", notifier.Name(), "stage", "result", "error", resultErr)
		}
	}
	if err != nil {
		logger.Warn("alert notification failed",
			"webhook", notifier.Name(), "type", ev.Type, "error", err)
	}
}

func deliveryAttempts(err error) int {
	var deliveryErr *DeliveryError
	if errors.As(err, &deliveryErr) && deliveryErr.Attempts > 0 {
		return deliveryErr.Attempts
	}
	return 1
}

// Shutdown stops the delivery goroutine.
func (h *Hub) Shutdown() {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
	h.wg.Wait()
	for {
		select {
		case ev := <-h.queue:
			h.deliver(ev)
		default:
			return
		}
	}
}

var (
	gmu    sync.RWMutex
	global *Hub
)

// Set installs the process-wide hub (called at startup).
func Set(h *Hub) {
	gmu.Lock()
	global = h
	gmu.Unlock()
}

// Get returns the process-wide hub, defaulting to a disabled no-op.
func Get() *Hub {
	gmu.RLock()
	defer gmu.RUnlock()
	if global == nil {
		return &Hub{enabled: false, queue: make(chan Event, 1), stop: make(chan struct{}), last: map[string]time.Time{}}
	}
	return global
}

// Emit is the convenience entrypoint used by services and middleware.
func Emit(ctx context.Context, ev Event) {
	Get().Notify(ctx, ev)
}

// Shutdown flushes process-wide alert delivery.
func Shutdown() {
	gmu.RLock()
	h := global
	gmu.RUnlock()
	if h != nil {
		h.Shutdown()
	}
}
