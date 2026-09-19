package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/performance"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "application YAML config path")
	outputDir := flag.String("out", "doc/performance", "directory for JSON and Markdown reports")
	duration := flag.Duration("duration", 5*time.Second, "sampling window per concurrency level")
	warmup := flag.Duration("warmup", 2*time.Second, "warmup window per concurrency level")
	timeout := flag.Duration("timeout", 15*time.Second, "single attempt timeout")
	concurrency := flag.String("concurrency", "1,4,8,16", "comma-separated concurrency levels")
	version := flag.String("version", "", "application/model version recorded in the report")
	revision := flag.String("revision", "", "git revision recorded in the report")
	cpu := flag.String("cpu", "", "CPU model visible to the deployment")
	memory := flag.String("memory", "", "memory visible to the deployment")
	dataScale := flag.String("data-scale", "baseline seed: 1 tenant/user, 1 chat/session, controlled provider", "data scale description")
	knowledgeBases := flag.Int("knowledge-bases", 0, "knowledge-base count used by the run")
	flag.Parse()

	levels, err := parseLevels(*concurrency)
	if err != nil {
		fatal(err)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	policy := performance.SamplingPolicy{
		DurationPerConcurrency: *duration,
		WarmupPerConcurrency:   *warmup,
		ConcurrencyLevels:      levels,
		TimeoutPerAttempt:      *timeout,
	}
	report, err := performance.RunApp(context.Background(), performance.RunnerOptions{
		Config: cfg, Version: *version, GitRevision: *revision,
		CPUModel: *cpu, MemoryGB: *memory, DataScale: *dataScale,
		KnowledgeBases: *knowledgeBases,
	}, performance.NewSampler(&policy))
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*outputDir, 0o750); err != nil {
		fatal(err)
	}
	base := filepath.Join(*outputDir, "baseline")
	jsonData, err := performance.WriteJSON(report)
	if err != nil {
		fatal(err)
	}
	markdown, err := performance.RenderMarkdown(report)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(base+".json", jsonData, 0o600); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(base+".md", markdown, 0o600); err != nil {
		fatal(err)
	}
	fmt.Printf("performance baseline written: %s.json, %s.md\n", base, base)
}

func parseLevels(value string) ([]int, error) {
	if strings.TrimSpace(value) == "" {
		return performance.DefaultConcurrencyLevels(), nil
	}
	parts := strings.Split(value, ",")
	levels := make([]int, 0, len(parts))
	for _, part := range parts {
		var level int
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &level); err != nil {
			return nil, fmt.Errorf("invalid concurrency level %q", part)
		}
		if level <= 0 {
			return nil, fmt.Errorf("concurrency level must be positive: %d", level)
		}
		levels = append(levels, level)
	}
	return levels, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "perf-baseline:", err)
	os.Exit(1)
}
