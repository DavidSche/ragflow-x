package ragflow

import (
	"context"
	"os"
	"testing"
	"time"
)

// This test intentionally stays skipped in normal CI. Set
// RGX_RAGFLOW_LIVE_BASE_URL (and RGX_RAGFLOW_LIVE_API_KEY for authenticated
// endpoints) to validate the version of a local RAGFlow deployment.
func TestLiveRAGFlowVersionContract(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}

	client := NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 10*time.Second, 4)
	version, err := NewVersionProbe(client).ProbeVersion(context.Background())
	if err != nil {
		t.Fatalf("probe live RAGFlow version: %v", err)
	}
	if version != baselineVersion {
		t.Fatalf("live RAGFlow version = %q, want %q", version, baselineVersion)
	}
}
