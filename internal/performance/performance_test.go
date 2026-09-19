package performance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSamplerAggregatesLatencyAndErrors(t *testing.T) {
	t.Parallel()
	policy := SamplingPolicy{DurationPerConcurrency: 50 * time.Millisecond, ConcurrencyLevels: []int{2}, TimeoutPerAttempt: time.Second}
	sampler := NewSampler(&policy)
	scenario := Scenario{
		Name: "test", Kind: "test",
		Operation: func(ctx context.Context) (Attempt, error) {
			time.Sleep(time.Millisecond)
			return Attempt{}, nil
		},
	}
	report, err := sampler.Run(context.Background(), []Scenario{scenario}, Environment{Host: "test"})
	if err != nil {
		t.Fatalf("run sampler: %v", err)
	}
	if len(report.Scenarios) != 1 || len(report.Scenarios[0].Results) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	result := report.Scenarios[0].Results[0]
	if result.Successes == 0 || result.Errors != 0 || result.ErrorRate != 0 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	if result.P50 == 0 || result.P95 < result.P50 || result.P99 < result.P95 {
		t.Fatalf("invalid percentiles: %+v", result)
	}
}

func TestHTTPOperationValidatesStatusAndSSE(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/fail") {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unavailable"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"ok\":true}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	operation := HTTPOperation(server.Client(), HTTPProbe{URL: server.URL + "/fail", Method: http.MethodGet, ExpectedCode: http.StatusOK})
	if _, err := operation(context.Background()); err == nil || !strings.Contains(err.Error(), "http status 503") {
		t.Fatalf("expected status failure, got %v", err)
	}
	streamOperation := HTTPOperation(server.Client(), HTTPProbe{URL: server.URL + "/stream", Method: http.MethodGet, ExpectedCode: http.StatusOK, Stream: true})
	attempt, err := streamOperation(context.Background())
	if err != nil {
		t.Fatalf("stream operation: %v", err)
	}
	if attempt.TTFT == nil || *attempt.TTFT <= 0 {
		t.Fatalf("expected positive TTFT, got %+v", attempt)
	}
}

func TestPercentileIsObservedValue(t *testing.T) {
	t.Parallel()
	values := []time.Duration{time.Millisecond, 2 * time.Millisecond, 100 * time.Millisecond}
	if got := percentile(values, 99); got != 100*time.Millisecond {
		t.Fatalf("P99 = %v", got)
	}
}

func TestSamplerRejectsCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSampler(nil).Run(ctx, []Scenario{{Name: "test", Operation: func(context.Context) (Attempt, error) { return Attempt{}, errors.New("unused") }}}, Environment{}); err == nil {
		t.Fatal("expected context error")
	}
}
