package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ResolveChat returns an explicit chat id or falls back to the tenant's first
// owned chat, so a logged-in workbench user does not have to know chat ids.
func (s *Service) ResolveChat(ctx context.Context, tenantID, chatID string) (string, error) {
	if chatID != "" {
		return chatID, nil
	}
	chats, total, err := s.Store.ListChatShadows(ctx, tenantID, false, repository.ChatFilter{}, 1, 1)
	if err != nil {
		return "", err
	}
	if total == 0 || len(chats) == 0 {
		return "", httperr.NotFound("no chat configured for this tenant")
	}
	return chats[0].ID, nil
}
