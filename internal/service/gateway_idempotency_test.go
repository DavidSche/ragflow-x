package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ScenarioID: SC-IDEM-001
func TestP0_IDEM_001_BeginGatewayIdempotencyConflictAndReplay(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Idempotency Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin user: %+v err=%v", user, err)
	}
	key := &model.APIKey{ID: "key-idem", TenantID: tenant.ID, UserID: user.ID}

	first, err := svc.BeginGatewayIdempotency(ctx, key, "same-key", "POST", "/api/v1/chat/completions", []byte(`{"a":1}`), "req-1")
	if err != nil || first.Record == nil || first.Replayed {
		t.Fatalf("begin first: state=%+v err=%v", first, err)
	}
	if err := svc.Store.CompleteGatewayIdempotency(ctx, first.Record.ID, "req-1", `{"ok":true}`); err != nil {
		t.Fatal(err)
	}

	replay, err := svc.BeginGatewayIdempotency(ctx, key, "same-key", "POST", "/api/v1/chat/completions", []byte(`{"a":1}`), "req-2")
	if err != nil || !replay.Replayed || replay.Record.ResponseRef != `{"ok":true}` {
		t.Fatalf("begin replay: state=%+v err=%v", replay, err)
	}

	_, err = svc.BeginGatewayIdempotency(ctx, key, "same-key", "POST", "/api/v1/chat/completions", []byte(`{"a":2}`), "req-3")
	var conflict *httperr.Error
	if !errors.As(err, &conflict) || conflict.Status != 409 || conflict.Code != 40980 {
		t.Fatalf("expected 409 conflict, got %v", err)
	}
}

// ScenarioID: SC-IDEM-001
func TestP0_IDEM_001_BeginGatewayIdempotencyInProgressUsesSameRequestID(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Idempotency Progress Tenant")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin user: %+v err=%v", user, err)
	}
	key := &model.APIKey{ID: "key-progress", TenantID: tenant.ID, UserID: user.ID}
	body := []byte(`{"stream":true}`)

	first, err := svc.BeginGatewayIdempotency(ctx, key, "stream-key", "POST", "/api/v1/chat/completions", body, "req-stream")
	if err != nil || first.Record.RequestID != "req-stream" {
		t.Fatalf("begin first: state=%+v err=%v", first, err)
	}
	second, err := svc.BeginGatewayIdempotency(ctx, key, "stream-key", "POST", "/api/v1/chat/completions", body, "req-stream-retry")
	if err != nil || second.Replayed || second.Record.ID != first.Record.ID || second.Record.RequestID != "req-stream" {
		t.Fatalf("begin retry: state=%+v err=%v", second, err)
	}
}
