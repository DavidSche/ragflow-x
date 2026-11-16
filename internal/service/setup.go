package service

import (
	"context"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/password"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Setup modes reported to the first-run wizard. They are not cosmetic: the
// admin-only step posts to /system/setup/admin, which only exists on the full
// application router. Offering that form while the bootstrap router serves the
// request used to end in a bare 404.
const (
	ModeWizard = "wizard"
	ModeEngine = "engine"
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
	// Mode tells the wizard which half of itself is valid right now: "engine"
	// means the full application router is serving this request (so the
	// admin-only step has POST /system/setup/admin), "wizard" means only the
	// bootstrap router is up and the operator must submit the full form.
	Mode string `json:"mode"`
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
		Mode:         ModeEngine,
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

// ResetUserPassword overwrites the password of an existing account.
//
// This is deliberately NOT an HTTP surface: it exists for two trusted,
// out-of-band callers — the first-run wizard (an operator is standing in front
// of the server, holding the setup RSA workflow) and the
// `ragflow-x reset-admin-password` recovery command. Callers must check the
// account exists first; this only writes.
func (s *Service) ResetUserPassword(ctx context.Context, username, plain string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return httperr.BadRequest(40001, "username is required")
	}
	if len(plain) < 8 {
		return httperr.BadRequest(40002, "password must be at least 8 characters")
	}
	u, err := s.Store.GetUserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if u == nil {
		return httperr.BadRequest(40004, "account does not exist")
	}
	hash, err := password.Hash(plain)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	return s.Store.UpdateUser(ctx, u)
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
