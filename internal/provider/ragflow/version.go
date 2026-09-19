package ragflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

const baselineVersion = "0.27.1"

// VersionProbe checks the RAGFlow version at startup and compares it against
// the pinned baseline. It reuses the HTTPClient's http.Client for connection
// pool sharing.
type VersionProbe struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewVersionProbe creates a probe that shares the HTTPClient's http.Client.
func NewVersionProbe(c *HTTPClient) *VersionProbe {
	return &VersionProbe{
		client:  c.http,
		baseURL: c.baseURL,
		apiKey:  c.apiKey,
	}
}

// ProbeVersion sends GET /api/v1/system/version and returns the version string.
func (p *VersionProbe) ProbeVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.baseURL+"/api/v1/system/version", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("version probe request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("version probe returned %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read version: %w", err)
	}

	var envelope struct {
		Code    int             `json:"code"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("decode version envelope: %w", err)
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("version probe returned code %d", envelope.Code)
	}
	var version string
	if err := json.Unmarshal(envelope.Data, &version); err != nil {
		var object struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(envelope.Data, &object); err != nil {
			return "", fmt.Errorf("decode version: %w", err)
		}
		version = object.Version
	}
	return strings.TrimPrefix(version, "v"), nil
}

// CheckBaseline probes the version and logs a warning if it doesn't match.
// Intended to be called as a goroutine at startup.
func (p *VersionProbe) CheckBaseline(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ver, err := p.ProbeVersion(ctx)
	if err != nil {
		logger.Warn("ragflow version probe failed", "error", err.Error(),
			"baseline", baselineVersion)
		return
	}
	if ver != baselineVersion {
		logger.Warn("ragflow version mismatch",
			"detected", ver,
			"baseline", baselineVersion,
			"msg", "contract tests need re-validation")
	} else {
		logger.Info("ragflow version check passed", "version", ver)
	}
}
