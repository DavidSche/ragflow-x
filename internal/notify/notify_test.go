package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestNewHub_WiresApprovalWebhookWithFilter(t *testing.T) {
	mu := sync.Mutex{}
	received := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Type string `json:"type"`
		}
		if err := decodeJSON(request, &payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		mu.Lock()
		received = append(received, payload.Type)
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hub := NewHub(config.Alerting{}, config.Approval{NotifyWebhook: server.URL})
	hub.Notify(context.Background(), Event{Type: "provider_error", Resource: "chat", ResourceID: "not-approval"})
	hub.Notify(context.Background(), Event{Type: "approval.submitted", Resource: "approval", ResourceID: "approval-1"})
	hub.Shutdown()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0] != "approval.submitted" {
		t.Fatalf("received = %v, want only approval.submitted", received)
	}
}

func TestHub_DeliversEventToSinkBeforeWebhook(t *testing.T) {
	delivered := make(chan Event, 1)
	hub := NewHubWithNotifiers([]Notifier{notifierFunc(func(_ context.Context, event Event) error {
		delivered <- event
		return nil
	})}, 0)
	sink := &memorySink{}
	hub.SetSink(sink)
	event := Event{ID: "alert-1", Type: "quota_exhausted", Resource: "api-key", ResourceID: "key-1", TenantID: "tenant"}
	hub.Notify(context.Background(), event)
	hub.Shutdown()
	select {
	case <-delivered:
	default:
		t.Fatal("webhook was not delivered")
	}
	if sink.Count() != 1 || sink.events[0].ID != event.ID {
		t.Fatalf("sink = %+v", sink.events)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_001_WebhookRetriesWithBackoffUntilConfirmed(t *testing.T) {
	var attempts atomic.Int32
	var requestAt []time.Time
	var requestMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		requestMu.Lock()
		requestAt = append(requestAt, time.Now())
		requestMu.Unlock()
		if count <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
			return
		}
		if payload["id"] != "alert-retry" || payload["type"] != "provider.compensation.failed" {
			t.Errorf("payload = %v", payload)
		}
		mac := hmac.New(sha256.New, []byte("webhook-secret"))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-RagflowX-Signature") != want {
			t.Errorf("request headers: type=%q signature=%q", r.Header.Get("Content-Type"), r.Header.Get("X-RagflowX-Signature"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	notifier := NewWebhook("test", server.URL, "webhook-secret", WithRetryPolicy(3, time.Millisecond, 2*time.Millisecond))
	err := notifier.Send(context.Background(), Event{ID: "alert-retry", Type: "provider.compensation.failed"})
	if err != nil {
		t.Fatalf("send after retries: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	requestMu.Lock()
	defer requestMu.Unlock()
	for index := 1; index < len(requestAt); index++ {
		if delay := requestAt[index].Sub(requestAt[index-1]); delay < time.Millisecond {
			t.Fatalf("attempt %d delay = %v, want at least 1ms", index, delay)
		}
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_001_WebhookStopsAfterRetryExhaustion(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	notifier := NewWebhook("test", server.URL, "", WithRetryPolicy(3, time.Millisecond, time.Millisecond))
	err := notifier.Send(context.Background(), Event{ID: "alert-terminal"})
	if err == nil {
		t.Fatal("send err = nil, want terminal delivery error")
	}
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("err = %v, want *DeliveryError", err)
	}
	if deliveryErr.Attempts != 3 || deliveryErr.LastError == nil {
		t.Fatalf("delivery error = %+v", deliveryErr)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_001_WebhookClientErrorIsTerminalWithoutRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	notifier := NewWebhook("test", server.URL, "", WithRetryPolicy(3, time.Millisecond, time.Millisecond))
	err := notifier.Send(context.Background(), Event{ID: "alert-client-error"})
	var deliveryErr *DeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Attempts != 1 {
		t.Fatalf("err = %v, want terminal delivery error after one attempt", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_001_HubAppliesConfiguredWebhookRetryPolicy(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	hub := NewHub(config.Alerting{
		Enabled: true,
		Webhooks: []config.Webhook{{
			Name: "provider-alerts", URL: server.URL, Enabled: true,
			MaxAttempts: 2, BackoffInitialMs: 1, BackoffMaxMs: 2,
		}},
	}, config.Approval{})
	hub.Notify(context.Background(), Event{ID: "hub-retry", Type: "provider.compensation.failed"})
	hub.Shutdown()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

type notifierFunc func(context.Context, Event) error

func (fn notifierFunc) Send(ctx context.Context, event Event) error { return fn(ctx, event) }
func (notifierFunc) Name() string                                   { return "test" }

type memorySink struct {
	events []Event
}

func (s *memorySink) RecordAlert(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *memorySink) Count() int { return len(s.events) }

type deliverySink struct {
	mu      sync.Mutex
	pending []string
	results []string
}

func (s *deliverySink) RecordAlert(_ context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, "alert:"+event.ID)
	return nil
}

func (s *deliverySink) RecordAlertDeliveryPending(_ context.Context, event Event, notifier string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, "pending:"+event.ID+":"+notifier)
	return nil
}

func (s *deliverySink) RecordAlertDeliveryResult(_ context.Context, event Event, notifier string, attempts int, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "succeeded"
	if err != nil {
		status = "failed"
	}
	s.results = append(s.results, fmt.Sprintf("%s:%s:%s:%d", status, event.ID, notifier, attempts))
	return nil
}

type leasedDeliverySink struct {
	mu         sync.Mutex
	records    []string
	leaseValid bool
}

func (s *leasedDeliverySink) RecordAlert(_ context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, "alert:"+event.ID)
	return nil
}

func (s *leasedDeliverySink) RecordAlertDeliveryLeaseResult(_ context.Context, event Event, notifier string, attempts int, lease DeliveryLease, _ error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, fmt.Sprintf("fenced:%s:%s:%d:%s:%d", event.ID, notifier, attempts, lease.Owner, lease.Generation))
	return s.leaseValid, nil
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_002_RetryLeasedWritesOnlyFencedResult(t *testing.T) {
	sink := &leasedDeliverySink{leaseValid: true}
	var sends atomic.Int32
	hub := NewHubWithNotifiers([]Notifier{notifierFunc(func(context.Context, Event) error {
		sends.Add(1)
		return &DeliveryError{Notifier: "test", Attempts: 2, LastError: errors.New("boom")}
	})}, 0)
	hub.SetSink(sink)

	known, leaseValid := hub.RetryLeased(
		context.Background(), Event{ID: "leased-retry"}, "test",
		DeliveryLease{Owner: "worker", Generation: 3},
	)
	if !known || !leaseValid {
		t.Fatalf("retry leased: known=%v leaseValid=%v", known, leaseValid)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	want := []string{"fenced:leased-retry:test:2:worker:3"}
	if sends.Load() != 1 || len(sink.records) != 1 || sink.records[0] != want[0] {
		t.Fatalf("leased retry sends=%d records=%v want=%v", sends.Load(), sink.records, want)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_002_RetryLeasedRejectsStaleResult(t *testing.T) {
	sink := &leasedDeliverySink{leaseValid: false}
	hub := NewHubWithNotifiers([]Notifier{notifierFunc(func(context.Context, Event) error { return nil })}, 0)
	hub.SetSink(sink)

	known, leaseValid := hub.RetryLeased(
		context.Background(), Event{ID: "stale-retry"}, "test",
		DeliveryLease{Owner: "old", Generation: 1},
	)
	if !known || leaseValid {
		t.Fatalf("stale leased retry: known=%v leaseValid=%v", known, leaseValid)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_002_HubPersistsWebhookDeliveryLifecycle(t *testing.T) {
	sink := &deliverySink{}
	hub := NewHubWithNotifiers([]Notifier{notifierFunc(func(context.Context, Event) error {
		return &DeliveryError{Notifier: "failing", Attempts: 3, LastError: errors.New("boom")}
	})}, 0)
	hub.SetSink(sink)
	hub.Notify(context.Background(), Event{ID: "alert-delivery", Type: "provider.compensation.failed"})
	hub.Shutdown()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.pending) != 2 || sink.pending[0] != "alert:alert-delivery" || sink.pending[1] != "pending:alert-delivery:test" {
		t.Fatalf("pending records = %v", sink.pending)
	}
	if len(sink.results) != 1 || sink.results[0] != "failed:alert-delivery:test:3" {
		t.Fatalf("delivery results = %v", sink.results)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_002_HubRetryTargetsOneChannelAndBypassesThrottle(t *testing.T) {
	var calls atomic.Int32
	hub := NewHubWithNotifiers([]Notifier{
		notifierFunc(func(context.Context, Event) error {
			calls.Add(1)
			return nil
		}),
	}, time.Hour)
	event := Event{ID: "retry-channel", Type: "provider.compensation.failed"}
	known := hub.Retry(context.Background(), event, "test")
	unknown := hub.Retry(context.Background(), event, "unknown")
	if !known {
		t.Fatal("hub retry = false, want known channel")
	}
	if unknown {
		t.Fatal("unknown channel retry should return false")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("notifier calls = %d, want 1", got)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_002_WebhookSetsStableAlertIdempotencyKey(t *testing.T) {
	var idempotencyKey string
	var alertID string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		idempotencyKey = request.Header.Get("Idempotency-Key")
		alertID = request.Header.Get("X-RagflowX-Alert-ID")
	}))
	t.Cleanup(server.Close)

	notifier := NewWebhook("idempotency", server.URL, "")
	if err := notifier.Send(context.Background(), Event{ID: "alert-123"}); err != nil {
		t.Fatalf("send webhook: %v", err)
	}
	if idempotencyKey != "alert:alert-123" || alertID != "alert-123" {
		t.Fatalf("webhook headers: idempotency=%q alert=%q", idempotencyKey, alertID)
	}
}

// ScenarioID: SC-AUDIT-002
func TestP0_NOTIFY_002_WebhookHonorsRetryAfterHeader(t *testing.T) {
	var attempts atomic.Int32
	var requestAt []time.Time
	var requestMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestMu.Lock()
		requestAt = append(requestAt, time.Now())
		requestMu.Unlock()
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	notifier := NewWebhook("retry-after", server.URL, "", WithRetryPolicy(2, 0, time.Second))
	if err := notifier.Send(context.Background(), Event{ID: "retry-after"}); err != nil {
		t.Fatalf("send after retry-after: %v", err)
	}
	requestMu.Lock()
	defer requestMu.Unlock()
	if len(requestAt) != 2 || requestAt[1].Sub(requestAt[0]) < 900*time.Millisecond {
		t.Fatalf("retry delay = %v between %d requests, want at least 900ms", requestAt[1].Sub(requestAt[0]), len(requestAt))
	}
}

func TestRetryAfterDurationSupportsSecondsAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		header string
		want   time.Duration
	}{
		{header: "2", want: 2 * time.Second},
		{header: "Wed, 10 Sep 2026 15:00:05 GMT", want: 5 * time.Second},
		{header: "0", want: 0},
		{header: "not-a-retry-after", want: 0},
	}
	for _, test := range tests {
		if got := retryAfterDuration(test.header, now); got != test.want {
			t.Fatalf("retryAfterDuration(%q) = %v, want %v", test.header, got, test.want)
		}
	}
}

func decodeJSON(request *http.Request, out any) error {
	defer request.Body.Close()
	return json.NewDecoder(request.Body).Decode(out)
}
