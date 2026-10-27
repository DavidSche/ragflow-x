// Package performance provides a deterministic workload sampler and report
// renderer for reproducible P50/P95/P99 performance baselines.
package performance

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"
)

// Attempt carries one operation result. TTFT is optional and only meaningful
// for streaming scenarios.
type Attempt struct {
	TTFT   *time.Duration
	Status string
}

// Operation executes one request or repository probe. It must be safe for
// concurrent use and must return promptly when ctx is cancelled.
type Operation func(context.Context) (Attempt, error)

// Scenario identifies one baseline workload. Kind distinguishes the measured
// boundary from the transport protocol in reports.
type Scenario struct {
	Name        string
	Kind        string
	Description string
	Operation   Operation
}

// ConcurrencyResult contains the observations for one concurrency level.
type ConcurrencyResult struct {
	Concurrency  int              `json:"concurrency"`
	Total        int64            `json:"total"`
	Successes    int64            `json:"successes"`
	Errors       int64            `json:"errors"`
	ErrorRate    float64          `json:"error_rate_percent"`
	Duration     time.Duration    `json:"duration_ms"`
	Throughput   float64          `json:"throughput_per_second"`
	P50          time.Duration    `json:"p50_ms"`
	P95          time.Duration    `json:"p95_ms"`
	P99          time.Duration    `json:"p99_ms"`
	Max          time.Duration    `json:"max_ms"`
	TTFTP50      *time.Duration   `json:"ttft_p50_ms,omitempty"`
	TTFTP95      *time.Duration   `json:"ttft_p95_ms,omitempty"`
	TTFTP99      *time.Duration   `json:"ttft_p99_ms,omitempty"`
	ErrorSamples map[string]int64 `json:"error_samples,omitempty"`
}

// ScenarioResult groups concurrency sweeps for one scenario.
type ScenarioResult struct {
	Name        string              `json:"name"`
	Kind        string              `json:"kind"`
	Description string              `json:"description"`
	Results     []ConcurrencyResult `json:"results"`
}

// Report is the machine-readable baseline artifact.
type Report struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Version     string            `json:"version"`
	Environment Environment       `json:"environment"`
	Sampling    SamplingPolicy    `json:"sampling"`
	Resources   ResourcePeak      `json:"resource_peak"`
	Scenarios   []ScenarioResult  `json:"scenarios"`
	Summary     map[string]string `json:"summary,omitempty"`
}

// ResourcePeak records observed process peaks while the sampler is active.
type ResourcePeak struct {
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes   uint64 `json:"heap_sys_bytes"`
	Goroutines     int    `json:"goroutines"`
	SampledAt      int64  `json:"sample_count"`
}

// SamplingPolicy makes the run reproducible beyond code defaults.
type SamplingPolicy struct {
	DurationPerConcurrency time.Duration `json:"duration_per_concurrency_ms"`
	WarmupPerConcurrency   time.Duration `json:"warmup_per_concurrency_ms"`
	ConcurrencyLevels      []int         `json:"concurrency_levels"`
	TimeoutPerAttempt      time.Duration `json:"timeout_per_attempt_ms"`
}

// Environment captures the report dimensions required by doc/65. CPU and
// memory descriptions are explicitly supplied by the runner so containerized
// runs cannot accidentally record host values that the process cannot see.
type Environment struct {
	Host           string `json:"host"`
	OS             string `json:"os"`
	GoVersion      string `json:"go_version"`
	GOMAXPROCS     int    `json:"go_max_procs"`
	CPUModel       string `json:"cpu_model"`
	MemoryGB       string `json:"memory_gb"`
	Database       string `json:"database"`
	Upstream       string `json:"upstream"`
	DataScale      string `json:"data_scale"`
	KnowledgeBases int    `json:"knowledge_bases"`
	GitRevision    string `json:"git_revision"`
}

type sample struct {
	duration time.Duration
	ttft     *time.Duration
	success  bool
	err      error
}

// DefaultConcurrencyLevels is the minimum useful capacity sweep.
func DefaultConcurrencyLevels() []int { return []int{1, 4, 8, 16} }

// DefaultSamplingPolicy returns a short local run. Production baselines should
// use at least five seconds per concurrency level after a two-second warmup.
func DefaultSamplingPolicy() SamplingPolicy {
	return SamplingPolicy{
		DurationPerConcurrency: 5 * time.Second,
		WarmupPerConcurrency:   2 * time.Second,
		ConcurrencyLevels:      DefaultConcurrencyLevels(),
		TimeoutPerAttempt:      15 * time.Second,
	}
}

// Sampler runs each scenario at each concurrency level and aggregates latency.
type Sampler struct {
	Policy       SamplingPolicy
	resources    ResourcePeak
	resourceStop chan struct{}
	resourceDone sync.WaitGroup
	resourceMu   sync.Mutex
}

// NewSampler returns a sampler with the supplied or default policy.
func NewSampler(policy *SamplingPolicy) *Sampler {
	if policy == nil {
		defaultPolicy := DefaultSamplingPolicy()
		policy = &defaultPolicy
	}
	if len(policy.ConcurrencyLevels) == 0 {
		policy.ConcurrencyLevels = DefaultConcurrencyLevels()
	}
	if policy.DurationPerConcurrency <= 0 {
		policy.DurationPerConcurrency = 5 * time.Second
	}
	if policy.WarmupPerConcurrency < 0 {
		policy.WarmupPerConcurrency = 0
	}
	if policy.TimeoutPerAttempt <= 0 {
		policy.TimeoutPerAttempt = 15 * time.Second
	}
	return &Sampler{Policy: *policy}
}

// Run executes all scenarios and returns a report skeleton.
func (s *Sampler) Run(ctx context.Context, scenarios []Scenario, environment Environment) (*Report, error) {
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("performance: no scenarios configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.startResourceMonitor()
	defer s.stopResourceMonitor()
	report := &Report{
		GeneratedAt: time.Now().UTC(),
		Environment: environment,
		Sampling:    s.Policy,
		Resources:   s.ResourcePeak(),
	}
	for _, scenario := range scenarios {
		result, err := s.runScenario(ctx, scenario)
		if err != nil {
			return nil, err
		}
		report.Scenarios = append(report.Scenarios, result)
	}
	report.Resources = s.ResourcePeak()
	return report, nil
}

// startResourceMonitor samples process memory and goroutines. It avoids OS
// process metrics so the same implementation works on every supported host.
func (s *Sampler) startResourceMonitor() {
	s.resourceStop = make(chan struct{})
	s.resources = ResourcePeak{}
	s.resourceDone.Add(1)
	go func() {
		defer s.resourceDone.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.resourceStop:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				s.resourceMu.Lock()
				if stats.HeapAlloc > s.resources.HeapAllocBytes {
					s.resources.HeapAllocBytes = stats.HeapAlloc
				}
				if stats.HeapSys > s.resources.HeapSysBytes {
					s.resources.HeapSysBytes = stats.HeapSys
				}
				if count := runtime.NumGoroutine(); count > s.resources.Goroutines {
					s.resources.Goroutines = count
				}
				s.resources.SampledAt++
				s.resourceMu.Unlock()
			}
		}
	}()
}

func (s *Sampler) stopResourceMonitor() {
	if s.resourceStop != nil {
		close(s.resourceStop)
	}
	s.resourceDone.Wait()
	s.resourceStop = nil
}

// ResourcePeak returns the highest observed process values.
func (s *Sampler) ResourcePeak() ResourcePeak {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	return s.resources
}

func (s *Sampler) runScenario(ctx context.Context, scenario Scenario) (ScenarioResult, error) {
	result := ScenarioResult{Name: scenario.Name, Kind: scenario.Kind, Description: scenario.Description}
	if scenario.Operation == nil {
		return result, fmt.Errorf("performance: scenario %q has no operation", scenario.Name)
	}
	for _, concurrency := range s.Policy.ConcurrencyLevels {
		if concurrency <= 0 {
			return result, fmt.Errorf("performance: invalid concurrency level %d", concurrency)
		}
		outcome, err := s.runConcurrency(ctx, scenario.Operation, concurrency)
		if err != nil {
			return result, err
		}
		result.Results = append(result.Results, outcome)
	}
	return result, nil
}

func (s *Sampler) runConcurrency(ctx context.Context, operation Operation, concurrency int) (ConcurrencyResult, error) {
	outcome := ConcurrencyResult{Concurrency: concurrency}
	if err := warmup(ctx, operation, concurrency, s.Policy.WarmupPerConcurrency); err != nil {
		return outcome, err
	}

	runDuration := s.Policy.DurationPerConcurrency
	drain := 2 * time.Second
	if drain > s.Policy.TimeoutPerAttempt {
		drain = s.Policy.TimeoutPerAttempt
	}
	ctx, cancel := context.WithTimeout(ctx, runDuration+drain)
	defer cancel()
	deadline := time.Now().Add(runDuration)
	runStarted := time.Now()

	var (
		mu      sync.Mutex
		samples []sample
		runErr  error
		wg      sync.WaitGroup
	)
	wg.Add(concurrency)
	for worker := 0; worker < concurrency; worker++ {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				attemptCtx, cancel := context.WithTimeout(ctx, s.Policy.TimeoutPerAttempt)
				started := time.Now()
				attempt, err := operation(attemptCtx)
				elapsed := time.Since(started)
				cancel()

				if attemptErr := attemptCtx.Err(); attemptErr != nil &&
					(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
					continue
				}
				if ctx.Err() != nil {
					continue
				}

				mu.Lock()
				samples = append(samples, sample{duration: elapsed, ttft: attempt.TTFT, success: err == nil, err: err})
				if err != nil {
					outcome.Errors++
					key := errorKey(err)
					if outcome.ErrorSamples == nil {
						outcome.ErrorSamples = map[string]int64{}
					}
					outcome.ErrorSamples[key]++
				} else {
					outcome.Successes++
				}
				mu.Unlock()
				_ = attempt.Status
			}
		}()
	}
	wg.Wait()
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if runErr != nil {
		return outcome, runErr
	}
	if len(samples) == 0 {
		return outcome, fmt.Errorf("performance: no samples captured")
	}
	outcome.Total = int64(len(samples))
	outcome.Duration = time.Since(runStarted)
	if outcome.Duration <= 0 {
		outcome.Duration = time.Nanosecond
	}
	outcome.Throughput = float64(outcome.Successes) / outcome.Duration.Seconds()
	outcome.ErrorRate = float64(outcome.Errors) / float64(outcome.Total) * 100
	outcome.P50, outcome.P95, outcome.P99, outcome.Max = latencies(samples)
	outcome.TTFTP50, outcome.TTFTP95, outcome.TTFTP99 = ttfts(samples)
	return outcome, nil
}

func warmup(ctx context.Context, operation Operation, concurrency int, duration time.Duration) error {
	if duration == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				_, _ = operation(attemptCtx)
				cancel()
			}
		}()
	}
	wg.Wait()
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func latencies(samples []sample) (time.Duration, time.Duration, time.Duration, time.Duration) {
	durations := make([]time.Duration, len(samples))
	maximum := time.Duration(0)
	for index, item := range samples {
		durations[index] = item.duration
		if item.duration > maximum {
			maximum = item.duration
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return percentile(durations, 50), percentile(durations, 95), percentile(durations, 99), maximum
}

func ttfts(samples []sample) (*time.Duration, *time.Duration, *time.Duration) {
	durations := make([]time.Duration, 0, len(samples))
	for _, item := range samples {
		if item.ttft != nil {
			durations = append(durations, *item.ttft)
		}
	}
	if len(durations) == 0 {
		return nil, nil, nil
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50 := percentile(durations, 50)
	p95 := percentile(durations, 95)
	p99 := percentile(durations, 99)
	return &p50, &p95, &p99
}

// percentile uses index = ceil(p*N)-1 so a P99 result is always an observed
// sample. This is intentionally conservative for small local baselines.
func percentile(sorted []time.Duration, percent int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := (percent*len(sorted) + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

func errorKey(err error) string {
	if err == nil {
		return ""
	}
	key := err.Error()
	if len(key) > 120 {
		key = key[:120]
	}
	return key
}
