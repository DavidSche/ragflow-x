package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type failingDatasetStore struct {
	repository.Store
	createError error
}

func (s *failingDatasetStore) CreateDatasetLink(ctx context.Context, link *model.DatasetLink) error {
	if s.createError != nil {
		return s.createError
	}
	return s.Store.CreateDatasetLink(ctx, link)
}

type rollbackRecorder struct {
	ragflow.Client
	mu      sync.Mutex
	deleted []string
}

func (r *rollbackRecorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, name)
}

func (r *rollbackRecorder) DeleteDataset(ctx context.Context, id string) error {
	r.record("dataset:" + id)
	return r.Client.DeleteDataset(ctx, id)
}

func (r *rollbackRecorder) DeleteChat(ctx context.Context, id string) error {
	r.record("chat:" + id)
	return r.Client.DeleteChat(ctx, id)
}

func (r *rollbackRecorder) DeleteProvider(ctx context.Context, name string) error {
	r.record("provider:" + name)
	return r.Client.DeleteProvider(ctx, name)
}

// ScenarioID: SC-COMP-001
func TestP0_COMP_001_CreateDatasetRollsBackExternalResourceWhenLocalFails(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "Tenant")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &rollbackRecorder{Client: ragflow.NewMock()}
	svc.RAGFlow = recorder
	failure := errors.New("disk full")
	svc.Store = &failingDatasetStore{Store: svc.Store, createError: failure}

	if _, err := svc.CreateDataset(context.Background(), tenant.ID, "orphan"); !errors.Is(err, failure) {
		t.Fatalf("expected local persistence failure, got %v", err)
	}
	recorder.mu.Lock()
	deleted := append([]string(nil), recorder.deleted...)
	recorder.mu.Unlock()
	if len(deleted) != 1 {
		t.Fatalf("expected external dataset rollback, got %v", deleted)
	}
}

type failingChatStore struct {
	repository.Store
	upsertError error
}

func (s *failingChatStore) UpsertChatShadow(ctx context.Context, chat *model.ChatShadow) error {
	if s.upsertError != nil {
		return s.upsertError
	}
	return s.Store.UpsertChatShadow(ctx, chat)
}

// ScenarioID: SC-COMP-001
func TestP0_COMP_001_CreateChatRollsBackExternalResourceWhenLocalFails(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "Tenant")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &rollbackRecorder{Client: ragflow.NewMock()}
	svc.RAGFlow = recorder
	failure := errors.New("disk full")
	svc.Store = &failingChatStore{Store: svc.Store, upsertError: failure}

	if _, err := svc.CreateChat(context.Background(), tenant.ID, "orphan", nil); !errors.Is(err, failure) {
		t.Fatalf("expected local persistence failure, got %v", err)
	}
	recorder.mu.Lock()
	deleted := len(recorder.deleted)
	recorder.mu.Unlock()
	if deleted != 1 {
		t.Fatalf("expected external chat rollback, got %d", deleted)
	}
}

type failingProviderStore struct {
	repository.Store
	createError error
}

type failingAuditStore struct {
	repository.Store
	auditError error
}

func (s *failingAuditStore) CreateAudit(ctx context.Context, entry *model.AuditLog) error {
	return s.auditError
}

func (s *failingProviderStore) CreateModelProvider(ctx context.Context, provider *model.ModelProvider) error {
	if s.createError != nil {
		return s.createError
	}
	return s.Store.CreateModelProvider(ctx, provider)
}

// ScenarioID: SC-COMP-001
func TestP0_COMP_001_CreateModelProviderRollsBackExternalResourceWhenLocalFails(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "Tenant")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &rollbackRecorder{Client: ragflow.NewMock()}
	svc.RAGFlow = recorder
	svc.RegisterLLM = true
	svc.SetProviderURLPolicy(true)
	failure := errors.New("disk full")
	svc.Store = &failingProviderStore{Store: svc.Store, createError: failure}

	register := true
	if _, err := svc.CreateModelProvider(context.Background(), tenant.ID, CreateModelProviderRequest{
		Name:         "orphan",
		BaseURL:      "https://llm.example.com/v1",
		ProviderType: "openai",
		ModelName:    "gpt-4o-mini",
		Register:     &register,
	}); !errors.Is(err, failure) {
		t.Fatalf("expected local persistence failure, got %v", err)
	}
	recorder.mu.Lock()
	deleted := len(recorder.deleted)
	recorder.mu.Unlock()
	if deleted != 1 {
		t.Fatalf("expected external provider rollback, got %d", deleted)
	}
}

// ScenarioID: SC-AUTH-001
func TestP0_AUTH_001_LoginAuditFailureRevokesSession(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(context.Background(), tenant.ID, "", CreateUserRequest{
		Username: "audited", Password: "secret123", Role: "tenant_admin",
	}); err != nil {
		t.Fatal(err)
	}
	auditErr := errors.New("audit unavailable")
	svc.Store = &failingAuditStore{Store: svc.Store, auditError: auditErr}

	if _, err := svc.Login(context.Background(), "audited", "secret123", "127.0.0.1"); !errors.Is(err, auditErr) {
		t.Fatalf("expected mandatory audit failure, got %v", err)
	}
}

// ScenarioID: SC-GATEWAY-001
func TestP0_GATEWAY_001_GatewayChatRejectsCrossTenantChat(t *testing.T) {
	svc := newAuthzSvc(t)
	mock := ragflow.NewMock()
	svc.RAGFlow = mock
	ctx := context.Background()

	ownerTenant, err := svc.CreateTenant(ctx, "Owner")
	if err != nil {
		t.Fatal(err)
	}
	attackerTenant, err := svc.CreateTenant(ctx, "Attacker")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, ownerTenant.ID, "private", nil)
	if err != nil {
		t.Fatal(err)
	}
	key := &model.APIKey{TenantID: attackerTenant.ID, UserID: "attacker"}

	err = svc.AuthorizeChatTarget(ctx, key, chat.ID, "")
	if err == nil {
		t.Fatal("cross-tenant chat must be rejected")
	}
	var apiErr *httperr.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("expected tenant-neutral 404, got %v", err)
	}
	if _, err := svc.ChatAppCompletion(ctx, key, chat.ID, "", []byte(`{"messages":[]}`), "req-1"); err == nil {
		t.Fatal("completion must not forward cross-tenant chat")
	}
	if _, _, err := svc.StreamChatAppCompletion(ctx, key, chat.ID, "", []byte(`{"messages":[]}`), &strings.Builder{}, "req-2"); err == nil {
		t.Fatal("stream must not forward cross-tenant chat")
	}
}

// ScenarioID: SC-GATEWAY-001
func TestP0_GATEWAY_001_GatewayChatRejectsForeignSession(t *testing.T) {
	svc := newAuthzSvc(t)
	mock := ragflow.NewMock()
	svc.RAGFlow = mock
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Tenant")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := svc.CreateChat(ctx, tenant.ID, "owned", nil)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := svc.CreateChat(ctx, tenant.ID, "foreign", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := mock.CreateChatSession(ctx, foreign.ID, "session")
	if err != nil {
		t.Fatal(err)
	}
	key := &model.APIKey{TenantID: tenant.ID, UserID: "user"}
	if err := svc.AuthorizeChatTarget(ctx, key, owned.ID, session.ID); err == nil {
		t.Fatal("session from another chat must be rejected")
	}
}

// ScenarioID: SC-NETPOLICY-001
func TestP0_NETPOLICY_001_ValidateProviderBaseURLBlocksPrivateAndSSRFShapes(t *testing.T) {
	cases := []string{
		"",
		"ftp://example.com",
		"http://user:pass@example.com",
		"http://example.com#fragment",
		"http://127.0.0.1:8000",
		"http://10.0.0.1:8000",
		"http://169.254.169.254/latest/meta-data",
	}
	for _, raw := range cases {
		if err := ValidateProviderBaseURL(raw, false); err == nil {
			t.Fatalf("expected rejection for %q", raw)
		}
	}
}

// ScenarioID: SC-NETPOLICY-001
func TestP0_NETPOLICY_001_GatewayRedirectsFollowProviderNetworkPolicy(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetGatewayClients(DefaultGatewayConfig())
	svc.SetProviderURLPolicy(false)
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:9/private", http.StatusFound)
	}))
	t.Cleanup(redirectServer.Close)

	request, err := http.NewRequest(http.MethodGet, redirectServer.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.httpClient.Do(request); err == nil {
		t.Fatal("private redirect target must be rejected")
	}
}

// ScenarioID: SC-AUTH-001
func TestP0_AUTH_001_RefreshTokenRotationIsAtomicUnderConcurrency(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "refresh.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "concurrent", Password: "secret123", Role: "tenant_admin"}); err != nil {
		t.Fatal(err)
	}
	login, err := svc.Login(ctx, "concurrent", "secret123", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 8
	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.RefreshAccessToken(ctx, login.Refresh)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var succeeded int
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected one concurrent refresh to succeed, got %d", succeeded)
	}
}
