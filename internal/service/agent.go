package service

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ListAgents returns the tenant's agents (or all tenants for scopeAll).
func (s *Service) ListAgents(ctx context.Context, tenantID string, scopeAll bool, filter repository.AgentFilter, page, pageSize int) ([]model.AgentShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	items, total, err := s.Store.ListAgentShadows(ctx, tenantID, scopeAll, filter, page, pageSize)
	if err != nil || len(items) == 0 {
		return items, total, err
	}
	names := s.tenantNameMap(ctx)
	for index := range items {
		items[index].TenantName = names[items[index].TenantID]
	}

	categoryByID := map[string]string{}
	for pageNumber := 1; len(categoryByID) < int(total) || pageNumber == 1; pageNumber++ {
		liveAgents, liveTotal, liveErr := s.RAGFlow.ListAgents(ctx, ragflow.ListAgentsFilter{Page: pageNumber, PageSize: 100})
		if liveErr != nil {
			return items, total, nil
		}
		for _, live := range liveAgents {
			category := live.CanvasCategory
			if category == "" && live.CanvasType == "Agent" {
				category = model.AgentCanvasCategoryWorkflow
			}
			categoryByID[live.ID] = category
		}
		if len(liveAgents) < 100 || (liveTotal > 0 && len(categoryByID) >= int(liveTotal)) {
			break
		}
	}
	for index := range items {
		items[index].CanvasCategory = categoryByID[items[index].ID]
	}
	return items, total, nil
}

// ListAgentsForScope is the Resolver-backed read path. Cross-tenant listing is
// a governance view and never turns this scope into a write authorization.
func (s *Service) ListAgentsForScope(ctx context.Context, scope TenantScope, filter repository.AgentFilter, page, pageSize int) ([]model.AgentShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	items, total, err := s.Store.ListAgentShadowsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), filter, page, pageSize)
	if err != nil || len(items) == 0 {
		return items, total, err
	}

	categoryByID := map[string]string{}
	for pageNumber := 1; len(categoryByID) < int(total) || pageNumber == 1; pageNumber++ {
		liveAgents, liveTotal, liveErr := s.RAGFlow.ListAgents(ctx, ragflow.ListAgentsFilter{Page: pageNumber, PageSize: 100})
		if liveErr != nil {
			return items, total, nil
		}
		for _, live := range liveAgents {
			category := live.CanvasCategory
			if category == "" && live.CanvasType == "Agent" {
				category = model.AgentCanvasCategoryWorkflow
			}
			categoryByID[live.ID] = category
		}
		if len(liveAgents) < 100 || (liveTotal > 0 && len(categoryByID) >= int(liveTotal)) {
			break
		}
	}
	for index := range items {
		items[index].CanvasCategory = categoryByID[items[index].ID]
	}
	return items, total, nil
}

// GetAgent returns an agent shadow the caller may access.
func (s *Service) GetAgent(ctx context.Context, tenantID, agentID string, scopeAll bool) (*model.AgentShadow, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	return a, nil
}

func (s *Service) GetAgentForScope(ctx context.Context, scope TenantScope, agentID string) (*model.AgentShadow, error) {
	agent, err := s.Store.GetAgentShadowForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, httperr.NotFound("agent not found")
	}
	return agent, nil
}

// GetAgentDetail returns the live RAGFlow-side agent canvas for the config UI.
func (s *Service) GetAgentDetail(ctx context.Context, tenantID, agentID string, scopeAll bool) (*ragflow.Agent, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	live, err := s.RAGFlow.GetAgent(ctx, agentID)
	if err != nil {
		return nil, httperr.New(502, 50280, "ragflow get agent failed")
	}
	if live == nil {
		return nil, httperr.NotFound("agent not found")
	}
	return live, nil
}

func (s *Service) GetAgentDetailForScope(ctx context.Context, scope TenantScope, agentID string) (*ragflow.Agent, error) {
	agent, err := s.Store.GetAgentShadowForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, httperr.NotFound("agent not found")
	}
	live, err := s.RAGFlow.GetAgent(ctx, agentID)
	if err != nil {
		return nil, httperr.New(502, 50280, "ragflow get agent failed")
	}
	if live == nil {
		return nil, httperr.NotFound("agent not found")
	}
	return live, nil
}

// CreateAgent creates an agent in RAGFlow and records its ownership.
func (s *Service) CreateAgent(ctx context.Context, tenantID, title string, dsl map[string]interface{}, release bool, canvasCategory string) (*model.AgentShadow, error) {
	title, err := normalizeDisplayName(title, "agent title is required", 40087)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.GetAgentShadowByTitle(ctx, tenantID, title, "")
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("agent")
	}
	if len(dsl) == 0 {
		return nil, httperr.BadRequest(40088, "agent dsl is required")
	}
	normalizedCategory := strings.TrimSpace(canvasCategory)
	if normalizedCategory == "" {
		normalizedCategory = model.AgentCanvasCategoryWorkflow
	}
	created, err := s.RAGFlow.CreateAgent(ctx, ragflow.CreateAgentRequest{Title: title, Dsl: dsl, Release: release, CanvasCategory: normalizedCategory})
	if err != nil {
		return nil, httperr.New(502, 50281, "ragflow create agent failed")
	}
	now := time.Now().UTC()
	a := &model.AgentShadow{
		ID: created.ID, TenantID: tenantID, Title: title, Status: "active", Release: release,
		CanvasCategory: normalizedCategory, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.UpsertAgentShadow(ctx, a); err != nil {
		if deleteErr := s.RAGFlow.DeleteAgent(ctx, created.ID); deleteErr != nil {
			logger.Warn("failed to roll back external agent after local persistence failure",
				"agent_id", created.ID, "error", deleteErr)
		}
		return nil, err
	}
	return a, nil
}

// UpdateAgent edits an agent (title/dsl) and optionally publishes/unpublishes.
func (s *Service) UpdateAgent(ctx context.Context, tenantID, agentID, title string, dsl map[string]interface{}, release *bool, scopeAll bool) (*model.AgentShadow, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	req := ragflow.UpdateAgentRequest{}
	if title != "" {
		title, err = normalizeDisplayName(title, "agent title is required", 40087)
		if err != nil {
			return nil, err
		}
		existing, err := s.Store.GetAgentShadowByTitle(ctx, tenantID, title, agentID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, displayNameConflict("agent")
		}
		req.Title = title
	}
	if dsl != nil {
		req.Dsl = dsl
	}
	if release != nil {
		req.Release = release
	}
	if err := s.RAGFlow.UpdateAgent(ctx, agentID, req); err != nil {
		return nil, httperr.New(502, 50282, "ragflow update agent failed")
	}
	if title != "" {
		a.Title = title
	}
	if release != nil {
		a.Release = *release
	}
	a.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpsertAgentShadow(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// PublishAgent is the dedicated publish/unpublish toggle (release).
func (s *Service) PublishAgent(ctx context.Context, tenantID, agentID string, release bool, scopeAll bool) (*model.AgentShadow, error) {
	r := release
	return s.UpdateAgent(ctx, tenantID, agentID, "", nil, &r, scopeAll)
}

// DeleteAgent deletes an agent the caller owns.
func (s *Service) DeleteAgent(ctx context.Context, tenantID, agentID string, scopeAll bool) error {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return err
	}
	if a == nil {
		return httperr.NotFound("agent not found")
	}
	if err := s.RAGFlow.DeleteAgent(ctx, agentID); err != nil {
		return httperr.New(502, 50283, "ragflow delete agent failed")
	}
	if err := s.Store.DeleteAssistantCatalog(ctx, a.TenantID, model.AssistantKindAgent, agentID); err != nil {
		return err
	}
	return s.Store.DeleteAgentShadow(ctx, agentID)
}

// ListAgentVersions lists the versions of an owned agent.
func (s *Service) ListAgentVersions(ctx context.Context, tenantID, agentID string, scopeAll bool) ([]ragflow.AgentVersion, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	versions, err := s.RAGFlow.ListAgentVersions(ctx, agentID)
	if err != nil {
		return nil, httperr.New(502, 50284, "ragflow list agent versions failed")
	}
	return versions, nil
}

// GetAgentVersion returns a single version of an owned agent.
func (s *Service) GetAgentVersion(ctx context.Context, tenantID, agentID, versionID string, scopeAll bool) (*ragflow.AgentVersion, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	ver, err := s.RAGFlow.GetAgentVersion(ctx, agentID, versionID)
	if err != nil {
		return nil, httperr.New(502, 50284, "ragflow get agent version failed")
	}
	return ver, nil
}

// RollbackAgentVersion restores an agent's DSL from a saved version.
func (s *Service) RollbackAgentVersion(ctx context.Context, tenantID, agentID, versionID string, scopeAll bool) error {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return err
	}
	if a == nil {
		return httperr.NotFound("agent not found")
	}
	if err := s.RAGFlow.RollbackAgentVersion(ctx, agentID, versionID); err != nil {
		return httperr.New(502, 50282, "ragflow rollback agent version failed")
	}
	a.UpdatedAt = time.Now().UTC()
	return s.Store.UpsertAgentShadow(ctx, a)
}

// ListAgentSessions lists an owned agent's sessions.
func (s *Service) ListAgentSessions(ctx context.Context, tenantID, agentID string, scopeAll bool, opts ragflow.SessionListOptions) ([]ragflow.AgentSession, int64, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, 0, err
	}
	if a == nil {
		return nil, 0, httperr.NotFound("agent not found")
	}
	sessions, total, err := s.RAGFlow.ListAgentSessions(ctx, agentID, opts)
	if err != nil {
		return nil, 0, httperr.New(502, 50285, "ragflow list agent sessions failed")
	}
	return sessions, total, nil
}

// CreateAgentSession starts a session on an owned agent.
func (s *Service) CreateAgentSession(ctx context.Context, tenantID, agentID, name string, scopeAll bool) (*ragflow.AgentSession, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	sess, err := s.RAGFlow.CreateAgentSession(ctx, agentID, name)
	if err != nil {
		return nil, httperr.New(502, 50286, "ragflow create agent session failed: "+err.Error())
	}
	return sess, nil
}

// GetAgentSession returns an owned agent session.
func (s *Service) GetAgentSession(ctx context.Context, tenantID, agentID, sessionID string, scopeAll bool) (*ragflow.AgentSession, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	sess, err := s.RAGFlow.GetAgentSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, httperr.New(502, 50287, "ragflow get agent session failed")
	}
	return sess, nil
}

// ListAgentSessionMessages returns an owned agent session as bounded pages.
func (s *Service) ListAgentSessionMessages(ctx context.Context, tenantID, agentID, sessionID string, scopeAll bool, opts ragflow.SessionMessagePageOptions) ([]ragflow.Message, string, error) {
	if _, err := s.GetAgentSession(ctx, tenantID, agentID, sessionID, scopeAll); err != nil {
		return nil, "", err
	}
	items, next, err := s.RAGFlow.ListAgentSessionMessages(ctx, agentID, sessionID, opts)
	if err != nil {
		return nil, "", httperr.New(502, 50291, "ragflow list agent session messages failed")
	}
	return items, next, nil
}

// DeleteAgentSession deletes an owned agent session.
func (s *Service) DeleteAgentSession(ctx context.Context, tenantID, agentID, sessionID string, scopeAll bool) error {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, scopeAll)
	if err != nil {
		return err
	}
	if a == nil {
		return httperr.NotFound("agent not found")
	}
	if err := s.RAGFlow.DeleteAgentSession(ctx, agentID, sessionID); err != nil {
		return httperr.New(502, 50288, "ragflow delete agent session failed")
	}
	return nil
}

// AgentChatCompletion runs an agent completion against an owned agent and meters it.
func (s *Service) AgentChatCompletion(ctx context.Context, tenantID, agentID, userID string, messages []ragflow.Message, files []map[string]interface{}, requestID, sessionID string) (*ragflow.CompletionResponse, error) {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, false)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, httperr.NotFound("agent not found")
	}
	files, err = s.resolveAgentAttachmentTickets(tenantID, agentID, userID, files)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	if requestID == "" {
		requestID = id.New()
	}
	question := lastUserQuestion(messages)
	resp, err := s.RAGFlow.AgentChatCompletion(ctx, ragflow.CompletionRequest{ChatID: agentID, SessionID: sessionID, Messages: messages, Files: files})
	if err != nil {
		s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, a.OwnerID, "agent", agentID, sessionID, requestID, question, "", 0, 0, 0, int64(time.Since(startedAt).Milliseconds())))
		return nil, httperr.New(502, 50289, "ragflow agent completion failed")
	}
	if resp == nil || len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return nil, httperr.New(502, 50289, "ragflow agent completion returned no content")
	}
	s.meterAgentCompletion(ctx, tenantID, a.OwnerID, requestID, resp)
	answer, citations := "", int64(0)
	if resp != nil && len(resp.Choices) > 0 {
		answer = resp.Choices[0].Message.Content
	}
	tokensIn, tokensOut := int64(0), int64(0)
	if resp != nil && resp.Usage != nil {
		tokensIn, tokensOut = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, a.OwnerID, "agent", agentID, sessionID, requestID, question, answer, citations, tokensIn, tokensOut, int64(time.Since(startedAt).Milliseconds())))
	return resp, nil
}

// StreamAgentChatCompletion proxies an agent completion (SSE).
func (s *Service) StreamAgentChatCompletion(ctx context.Context, tenantID, agentID, userID string, messages []ragflow.Message, files []map[string]interface{}, requestID, sessionID string, w io.Writer) error {
	a, err := s.Store.GetAgentShadow(ctx, tenantID, agentID, false)
	if err != nil {
		return err
	}
	if a == nil {
		return httperr.NotFound("agent not found")
	}
	files, err = s.resolveAgentAttachmentTickets(tenantID, agentID, userID, files)
	if err != nil {
		return err
	}
	startedAt := time.Now()
	question := lastUserQuestion(messages)
	capture := &knowledgeOpsStreamCapture{w: w}
	err = s.RAGFlow.StreamAgentChatCompletion(ctx, ragflow.CompletionRequest{ChatID: agentID, SessionID: sessionID, Messages: messages, Files: files}, capture)
	if err != nil {
		s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, a.OwnerID, "agent", agentID, sessionID, requestID, question, "", 0, 0, 0, int64(time.Since(startedAt).Milliseconds())))
		return err
	}
	if capture.sawError {
		s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, a.OwnerID, "agent", agentID, sessionID, requestID, question, "", 0, 0, 0, int64(time.Since(startedAt).Milliseconds())))
	} else {
		s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, a.OwnerID, "agent", agentID, sessionID, requestID, question, capture.answer.String(), int64(capture.citations), capture.tokensIn, capture.tokensOut, int64(time.Since(startedAt).Milliseconds())))
		s.meterAgentCompletion(ctx, tenantID, a.OwnerID, requestID, &ragflow.CompletionResponse{Usage: &ragflow.CompletionUsage{PromptTokens: capture.tokensIn, CompletionTokens: capture.tokensOut}})
	}
	return nil
}

func (s *Service) meterAgentCompletion(ctx context.Context, tenantID, userID, requestID string, resp *ragflow.CompletionResponse) {
	tIn, tOut := int64(0), int64(0)
	if resp != nil && resp.Usage != nil {
		tIn, tOut = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	}
	if requestID == "" {
		requestID = id.New()
	}
	date := time.Now().UTC().Format("2006-01-02")
	_, _ = s.Store.RecordUsage(ctx, &model.QuotaUsage{
		TenantID: tenantID, UserID: userID, KeyID: "", RequestID: requestID,
		Date: date, TokensIn: tIn, TokensOut: tOut, Requests: 1,
	})
	_, _ = s.Store.RecordCostMetric(ctx, &model.CostMetric{
		TenantID: tenantID, UserID: userID, RequestID: requestID, KeyID: "",
		Date: date, Model: "agent", Scenario: "agent", TokensIn: tIn, TokensOut: tOut,
	})
}
