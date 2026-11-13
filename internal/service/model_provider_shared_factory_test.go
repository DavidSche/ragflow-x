package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// recordingProviderDeleteClient records every upstream DeleteProvider call so
// tests can assert whether the shared RAGFlow factory was (or was not) removed.
type recordingProviderDeleteClient struct {
	ragflow.Client
	deleteCalls []string
}

func (c *recordingProviderDeleteClient) DeleteProvider(_ context.Context, providerName string) error {
	c.deleteCalls = append(c.deleteCalls, providerName)
	return nil
}

// Two providers of the same type share one upstream RAGFlow factory. Deleting
// one of them must NOT delete the shared factory, otherwise the surviving
// provider's "add instance" calls break with "provider does not exist".
func TestP0_PROVIDER_001_DeleteModelProviderKeepsSharedFactoryWhileOthersExist(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)

	p1, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider 1: %v", err)
	}
	p2, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider 2: %v", err)
	}
	if p1.ProviderType != p2.ProviderType {
		t.Fatalf("providers should share a factory type, got %q vs %q", p1.ProviderType, p2.ProviderType)
	}

	rec := &recordingProviderDeleteClient{Client: svc.RAGFlow}
	svc.RAGFlow = rec

	// Deleting p1 while p2 still exists must leave the upstream factory alone.
	if err := svc.DeleteModelProvider(ctx, "tenant-1", p1.ID); err != nil {
		t.Fatalf("delete first provider: %v", err)
	}
	if len(rec.deleteCalls) != 0 {
		t.Fatalf("upstream DeleteProvider called %d time(s) while a same-type provider still exists: %v", len(rec.deleteCalls), rec.deleteCalls)
	}
	remaining, err := svc.Store.ListModelProviders(ctx, "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("local provider count = %d, want 1", len(remaining))
	}

	// Deleting the last same-type provider must remove the upstream factory.
	if err := svc.DeleteModelProvider(ctx, "tenant-1", p2.ID); err != nil {
		t.Fatalf("delete last provider: %v", err)
	}
	if len(rec.deleteCalls) != 1 || rec.deleteCalls[0] != p2.ProviderType {
		t.Fatalf("expected upstream factory %q deleted once, got %v", p2.ProviderType, rec.deleteCalls)
	}
}

// Deleting a provider of a type that no other provider references still removes
// the upstream factory (the original cleanup semantics are preserved for the
// last provider of each type).
func TestP0_PROVIDER_001_DeleteModelProviderRemovesFactoryForLastProvider(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)

	p, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	rec := &recordingProviderDeleteClient{Client: svc.RAGFlow}
	svc.RAGFlow = rec

	if err := svc.DeleteModelProvider(ctx, "tenant-1", p.ID); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	if len(rec.deleteCalls) != 1 || rec.deleteCalls[0] != p.ProviderType {
		t.Fatalf("expected upstream factory %q deleted once, got %v", p.ProviderType, rec.deleteCalls)
	}
}

// failingProviderInstanceCreateClient makes the upstream create instance call
// fail so the service can surface the real reason alongside 50205.
type failingProviderInstanceCreateClient struct {
	ragflow.Client
	createErr error
}

func (c *failingProviderInstanceCreateClient) CreateProviderInstance(context.Context, ragflow.RegisterModelProviderRequest) (*ragflow.ProviderInstance, error) {
	return nil, c.createErr
}

// The 50205 response must carry the upstream reason (e.g. "provider ... does not
// exist") instead of a silent fixed message, so operators can diagnose failures.
func TestP0_PROVIDER_001_CreateInstanceSurfacesUpstreamReason(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	svc.SetProviderURLPolicy(true)

	provider, err := svc.AddModelProvider(ctx, "tenant-1", "openai-api-compatible")
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	svc.RAGFlow = &failingProviderInstanceCreateClient{
		Client:    svc.RAGFlow,
		createErr: ragflow.NewErrorFromResponse(200, []byte(`{"code":404,"message":"provider 'OpenAI-API-Compatible' does not exist"}`)),
	}

	_, err = svc.CreateProviderInstance(ctx, "tenant-1", provider.ID, CreateProviderInstanceRequest{
		InstanceName: "31.211",
		APIKey:       "key",
		BaseURL:      "http://192.168.31.211:8080/v1",
		Region:       "default",
		Models:       []ModelInfoInput{{ModelName: "deepseek-v4-flash-0731", ModelTypes: []string{"chat"}, MaxTokens: 8192}},
	})
	he, ok := err.(*httperr.Error)
	if !ok {
		t.Fatalf("expected *httperr.Error, got %T: %v", err, err)
	}
	if he.Status != 502 || he.Code != 50205 {
		t.Fatalf("expected 502/50205, got %d/%d", he.Status, he.Code)
	}
	if !strings.Contains(he.Message, "does not exist") {
		t.Fatalf("50205 must surface upstream reason, got message %q", he.Message)
	}
}
