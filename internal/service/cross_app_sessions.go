package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const recentSessionScanSize = 400

type RecentConversationSession struct {
	AppType      string    `json:"app_type"`
	TargetID     string    `json:"target_id"`
	TargetName   string    `json:"target_name"`
	ContextID    string    `json:"context_id"`
	RequestID    string    `json:"request_id"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	LastActivity time.Time `json:"last_activity"`
	LastTurnAt   time.Time `json:"last_turn_at"`
	TurnCount    int64     `json:"turn_count"`
}

func normalizeRecentAppType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat", "search", "agent":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func recentSessionKey(appType, appID, sessionID, requestID string) string {
	if sessionID != "" {
		return appType + ":" + appID + ":" + sessionID
	}
	return appType + ":" + appID + ":request:" + requestID
}

func (s *Service) ListRecentCrossAppSessions(ctx context.Context, actorID, query string, limit int) ([]RecentConversationSession, error) {
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	appTypes := []string{}
	executionTargets := []struct {
		AppType  string
		Resource string
	}{
		{AppType: "chat", Resource: "chat"},
		{AppType: "agent", Resource: "agent"},
		{AppType: "search", Resource: "search-app"},
	}
	for _, target := range executionTargets {
		if s.Authorize(ctx, actorID, "execute", target.Resource) == nil {
			appTypes = append(appTypes, target.AppType)
		}
	}
	if len(appTypes) == 0 {
		return []RecentConversationSession{}, nil
	}

	events, _, err := s.Store.ListKnowledgeOpsEvents(ctx, ac.user.TenantID, false, 1, recentSessionScanSize, repository.KnowledgeOpsFilter{
		Search: strings.TrimSpace(query),
	})
	if err != nil {
		return nil, err
	}

	chatNames := map[string]string{}
	agentNames := map[string]string{}
	searchNames := map[string]string{}
	for _, appType := range appTypes {
		switch appType {
		case "chat":
			items, _, listErr := s.Store.ListChatShadows(ctx, ac.user.TenantID, false, repository.ChatFilter{}, 1, 200)
			if listErr != nil {
				return nil, listErr
			}
			for _, item := range items {
				chatNames[item.ID] = item.Name
			}
		case "agent":
			items, _, listErr := s.Store.ListAgentShadows(ctx, ac.user.TenantID, false, repository.AgentFilter{}, 1, 200)
			if listErr != nil {
				return nil, listErr
			}
			for _, item := range items {
				agentNames[item.ID] = item.Title
			}
		case "search":
			items, _, listErr := s.Store.ListSearchAppShadows(ctx, ac.user.TenantID, false, repository.SearchAppFilter{}, 1, 200)
			if listErr != nil {
				return nil, listErr
			}
			for _, item := range items {
				searchNames[item.ID] = item.Name
			}
		}
	}

	itemsByID := map[string]*RecentConversationSession{}
	for _, event := range events {
		appType := normalizeRecentAppType(event.AppType)
		if appType == "" {
			continue
		}
		allowed := false
		for _, item := range appTypes {
			if item == appType {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		key := recentSessionKey(appType, event.AppID, event.SessionID, event.RequestID)
		item, exists := itemsByID[key]
		if !exists {
			targetName := event.AppID
			switch appType {
			case "chat":
				if name := chatNames[event.AppID]; name != "" {
					targetName = name
				}
			case "agent":
				if name := agentNames[event.AppID]; name != "" {
					targetName = name
				}
			case "search":
				if name := searchNames[event.AppID]; name != "" {
					targetName = name
				}
			}
			contextID := event.SessionID
			if contextID == "" {
				contextID = event.RequestID
			}
			item = &RecentConversationSession{
				AppType: appType, TargetID: event.AppID, TargetName: targetName,
				ContextID: contextID, RequestID: event.RequestID, Title: event.Question,
				Status: event.Status, LastActivity: event.UpdatedAt, LastTurnAt: event.CreatedAt,
			}
			itemsByID[key] = item
		}
		item.TurnCount++
		if event.UpdatedAt.After(item.LastActivity) {
			item.LastActivity = event.UpdatedAt
		}
		if event.CreatedAt.After(item.LastTurnAt) {
			item.LastTurnAt = event.CreatedAt
		}
	}

	sessions := make([]RecentConversationSession, 0, len(itemsByID))
	for _, item := range itemsByID {
		sessions = append(sessions, *item)
	}
	sort.SliceStable(sessions, func(left, right int) bool {
		return sessions[left].LastActivity.After(sessions[right].LastActivity)
	})
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}
