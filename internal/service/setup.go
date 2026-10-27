package service

import (
	"context"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// SetupStatus reports whether the platform has been initialized and whether
// its external dependencies are reachable, so a first-run wizard can guide
// the operator.
type SetupStatus struct {
	Initialized  bool   `json:"initialized"`
	DBConfigured bool   `json:"db_configured"`
	RAGFlowUp    bool   `json:"ragflow_up"`
	RAGFlowName  string `json:"ragflow_provider"`
	Configured   bool   `json:"configured"`
	DBUp         bool   `json:"db_up"`
}

// SetupStatus reports the first-run state of the control plane.
func (s *Service) SetupStatus(ctx context.Context) (*SetupStatus, error) {
	n, err := s.Store.CountUsers(ctx)
	if err != nil {
		return nil, err
	}
	up := false
	if _, err := s.RAGFlow.Health(ctx); err == nil {
		up = true
	}
	return &SetupStatus{
		Initialized:  n > 0,
		DBConfigured: true,
		Configured:   true,
		RAGFlowUp:    up,
		RAGFlowName:  s.RAGFlow.Name(),
		DBUp:         true,
	}, nil
}

// CreateFirstAdmin creates the platform super-admin on first launch. It
// refuses to run once any user exists, replacing the previous behavior of
// bootstrapping a default-password admin.
func (s *Service) CreateFirstAdmin(ctx context.Context, username, secret string) error {
	username = strings.TrimSpace(username)
	if len(username) < 3 {
		return httperr.BadRequest(40001, "username must be at least 3 characters")
	}
	if len(secret) < 8 {
		return httperr.BadRequest(40002, "password must be at least 8 characters")
	}
	n, err := s.Store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return httperr.New(409, 40901, "system already initialized")
	}
	return s.BootstrapAdmin(ctx, username, secret)
}

// PreflightResult reports whether a proposed engine connection works without
// persisting any secret.
type PreflightResult struct {
	Provider  string `json:"provider"`
	Reachable bool   `json:"reachable"`
	Status    string `json:"status,omitempty"`
	DB        string `json:"db,omitempty"`
	Message   string `json:"message,omitempty"`
}

// PreflightRAGFlow validates a proposed RAGFlow endpoint against the real
// engine, returning a reachability report without storing the api key.
func (s *Service) PreflightRAGFlow(ctx context.Context, baseURL, apiKey string) (*PreflightResult, error) {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil, httperr.BadRequest(40003, "base_url and api_key are required")
	}
	client := ragflow.NewHTTPClient(baseURL, apiKey, 5*time.Second, 5)
	h, err := client.Health(ctx)
	if err != nil {
		return &PreflightResult{Provider: "http", Reachable: false, Message: "ragflow endpoint is unreachable"}, nil
	}
	return &PreflightResult{
		Provider:  "http",
		Reachable: true,
		Status:    h.Status,
		DB:        h.DB,
	}, nil
}
