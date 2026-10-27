package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_AgentValidationPropagatesStoreFailure(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.Store = &failingAgentShadowStore{Store: svc.Store}
	approval := &model.Approval{
		TenantID:     "tenant-1",
		ObjectType:   model.ApprovalObjectAgent,
		ObjectID:     "agent-1",
		SnapshotJSON: `{"agent_updated_at":"2026-01-01T00:00:00Z"}`,
	}

	err := validateApprovalAgentAction(context.Background(), svc, approval)
	if err == nil {
		t.Fatal("expected agent shadow store failure")
	}
	if !errors.Is(err, errAgentShadowUnavailable) {
		t.Fatalf("store failure was not propagated: %v", err)
	}
	if herr, ok := err.(*httperr.Error); ok && herr.Status == 404 {
		t.Fatalf("store failure must not be reported as not found: %v", err)
	}
}

type failingAgentShadowStore struct {
	repository.Store
}

func (s *failingAgentShadowStore) GetAgentShadow(context.Context, string, string, bool) (*model.AgentShadow, error) {
	return nil, errAgentShadowUnavailable
}
