// Package provider wires external dependency adapters (RAGFlow, and later
// direct data sources, LLM providers, object storage).
package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// NewRAGFlow builds the configured RAGFlow provider. Choose "http" for a real
// engine or "mock" for local development and tests.
func NewRAGFlow(cfg config.RAGFlow) (ragflow.Client, error) {
	switch cfg.Provider {
	case "http":
		if cfg.BaseURL == "" || cfg.APIKey == "" {
			return nil, fmt.Errorf("ragflow http provider requires base_url and api_key")
		}

		// Full middleware chain: Retry (outermost) → Logging → Metrics (innermost).
		// chainRoundTrippers applies last-argument-outermost, so order matters.
		client := ragflow.NewHTTPClientWithMiddleware(
			cfg.BaseURL, cfg.APIKey,
			time.Duration(cfg.Timeout)*time.Second, cfg.MaxConns,
			ragflow.MetricsMiddleware(),
			ragflow.LoggingMiddleware("/providers/"),
			ragflow.RetryMiddleware(cfg.RetryMaxRetries, time.Duration(cfg.RetryBackoffMs)*time.Millisecond),
		)

		// Async startup version probe (non-blocking).
		probe := ragflow.NewVersionProbe(client)
		go probe.CheckBaseline(context.Background())

		return client, nil
	case "mock":
		return ragflow.NewMock(), nil
	default:
		return nil, fmt.Errorf("unknown ragflow provider: %s", cfg.Provider)
	}
}
