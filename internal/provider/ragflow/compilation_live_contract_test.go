package ragflow

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

func TestLiveCompilationStateObservation(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}

	client := NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 15*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	datasets, err := client.ListDatasets(ctx)
	if err != nil {
		t.Fatalf("list live datasets: %v", err)
	}
	if len(datasets) == 0 {
		t.Skip("live compilation state drill requires at least one existing dataset")
	}

	kinds := []string{"", "wiki", "tree", "graph", "mindmap", "timeline", "pageindex"}
	observedStates := map[string][]string{}
	for _, dataset := range datasets {
		for _, kind := range kinds {
			status, err := client.GetCompilationStatusForKind(ctx, dataset.ID, kind)
			if err != nil {
				t.Errorf("read compilation status dataset=%s kind=%q: %v", dataset.ID, kind, err)
				continue
			}
			key := fmt.Sprintf("state=%s kind=%q", status.State, status.Kind)
			observedStates[key] = append(observedStates[key], fmt.Sprintf(
				"dataset=%s progress=%.2f phase=%q msg=%q inflight=%d backlog=%d error=%q",
				dataset.ID, status.Progress, status.CurrentPhase, status.ProgressMsg,
				status.Inflight, status.Backlog, status.Error,
			))
		}
	}

	states := make([]string, 0, len(observedStates))
	for state := range observedStates {
		states = append(states, state)
	}
	sort.Strings(states)
	for _, state := range states {
		samples := observedStates[state]
		if len(samples) > 3 {
			samples = samples[:3]
		}
		for _, sample := range samples {
			t.Logf("live compilation observation %s; %s", state, sample)
		}
	}
	t.Logf("live compilation state observation complete: datasets=%d states=%d", len(datasets), len(states))
}
