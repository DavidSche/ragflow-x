package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// EnsureTenant validates that the target tenant exists before a platform admin
// creates a user in a tenant other than their own.
func (s *Service) EnsureTenant(ctx context.Context, tenantID string) error {
	tenant, err := s.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if tenant == nil {
		return httperr.NotFound("tenant not found")
	}
	return nil
}

func (s *Service) SetAgentOwner(ctx context.Context, agent *model.AgentShadow, ownerID string) error {
	agent.OwnerID = ownerID
	return s.Store.UpsertAgentShadow(ctx, agent)
}

func (s *Service) SetChatOwner(ctx context.Context, chat *model.ChatShadow, ownerID string) error {
	chat.OwnerID = ownerID
	return s.Store.UpsertChatShadow(ctx, chat)
}

func (s *Service) SetMemoryOwner(ctx context.Context, memory *model.MemoryShadow, ownerID string) error {
	memory.OwnerID = ownerID
	return s.Store.UpsertMemoryShadow(ctx, memory)
}

func (s *Service) SetSearchAppOwner(ctx context.Context, app *model.SearchAppShadow, ownerID string) error {
	app.OwnerID = ownerID
	return s.Store.UpsertSearchAppShadow(ctx, app)
}
