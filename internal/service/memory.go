package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// supportedMemoryTypes are the memory kinds RAGFlow accepts. A usable memory
// must include the raw type as its baseline store.
var supportedMemoryTypes = map[string]struct{}{
	"raw": {}, "semantic": {}, "episodic": {}, "procedural": {},
}

// ListMemories returns the tenant's memories (or all tenants for scopeAll).
func (s *Service) ListMemories(ctx context.Context, tenantID string, scopeAll bool, filter repository.MemoryFilter, page, pageSize int) ([]model.MemoryShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListMemoryShadows(ctx, tenantID, scopeAll, filter, page, pageSize)
}

// GetMemory returns a memory the caller may access.
func (s *Service) GetMemory(ctx context.Context, tenantID, memoryID string, scopeAll bool) (*model.MemoryShadow, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	return mem, nil
}

// GetMemoryConfig returns the live RAGFlow-side memory config for the editor.
func (s *Service) GetMemoryConfig(ctx context.Context, tenantID, memoryID string, scopeAll bool) (*ragflow.Memory, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	live, err := s.RAGFlow.GetMemoryConfig(ctx, memoryID)
	if err != nil {
		return nil, httperr.New(502, 50270, "ragflow get memory config failed")
	}
	if live == nil {
		return nil, httperr.NotFound("memory not found")
	}
	return live, nil
}

// validateMemoryCreate enforces the same baseline rules RAGFlow's own create
// dialog applies: a non-empty name, at least the "raw" memory type, and both a
// chat (LLM) and embedding model so the memory is actually usable.
func validateMemoryCreate(name string, memoryType []string, embdID, llmID string) error {
	if strings.TrimSpace(name) == "" {
		return httperr.BadRequest(40095, "memory name is required")
	}
	if len(name) > 100 {
		return httperr.BadRequest(40095, "memory name exceeds the 100 character limit")
	}
	if len(memoryType) == 0 {
		return httperr.BadRequest(40097, "memory type is required")
	}
	hasRaw := false
	for _, mt := range memoryType {
		if _, ok := supportedMemoryTypes[mt]; !ok {
			return httperr.BadRequest(40097, "unsupported memory type: "+mt)
		}
		if mt == "raw" {
			hasRaw = true
		}
	}
	if !hasRaw {
		return httperr.BadRequest(40097, "memory type must include raw")
	}
	if strings.TrimSpace(embdID) == "" {
		return httperr.BadRequest(40098, "embedding model is required")
	}
	if strings.TrimSpace(llmID) == "" {
		return httperr.BadRequest(40099, "llm model is required")
	}
	return nil
}

// CreateMemory creates a memory in RAGFlow and records its ownership.
func (s *Service) CreateMemory(ctx context.Context, tenantID, name string, memoryType []string, embdID, llmID string) (*model.MemoryShadow, error) {
	if err := validateMemoryCreate(name, memoryType, embdID, llmID); err != nil {
		return nil, err
	}
	id, err := s.RAGFlow.CreateMemory(ctx, ragflow.CreateMemoryRequest{Name: name, MemoryType: memoryType, EmbdID: embdID, LLMID: llmID})
	if err != nil {
		return nil, httperr.New(502, 50271, "ragflow create memory failed")
	}
	now := time.Now().UTC()
	mem := &model.MemoryShadow{
		ID: id, TenantID: tenantID, Name: name, MemoryType: strings.Join(memoryType, ","),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.UpsertMemoryShadow(ctx, mem); err != nil {
		return nil, err
	}
	return mem, nil
}

// UpdateMemory updates a memory the caller owns.
func (s *Service) UpdateMemory(ctx context.Context, tenantID, memoryID string, req ragflow.UpdateMemoryRequest, scopeAll bool) (*model.MemoryShadow, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return nil, httperr.BadRequest(40095, "memory name is required")
	}
	if req.MemoryType != nil && len(req.MemoryType) == 0 {
		return nil, httperr.BadRequest(40097, "memory type is required")
	}
	if _, err := s.RAGFlow.UpdateMemory(ctx, memoryID, req); err != nil {
		return nil, httperr.New(502, 50272, "ragflow update memory failed")
	}
	if req.Name != nil {
		mem.Name = *req.Name
	}
	if req.MemoryType != nil {
		mem.MemoryType = strings.Join(req.MemoryType, ",")
	}
	mem.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpsertMemoryShadow(ctx, mem); err != nil {
		return nil, err
	}
	return mem, nil
}

// DeleteMemory deletes a memory the caller owns.
func (s *Service) DeleteMemory(ctx context.Context, tenantID, memoryID string, scopeAll bool) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	if err := s.RAGFlow.DeleteMemory(ctx, memoryID); err != nil {
		return httperr.New(502, 50273, "ragflow delete memory failed")
	}
	return s.Store.DeleteMemoryShadow(ctx, memoryID)
}

// ListMemoryMessages lists the messages of an owned memory.
func (s *Service) ListMemoryMessages(ctx context.Context, tenantID, memoryID string, scopeAll bool) ([]ragflow.MemoryMessage, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	msgs, err := s.RAGFlow.ListMemoryMessages(ctx, memoryID)
	if err != nil {
		return nil, httperr.New(502, 50274, "ragflow list memory messages failed")
	}
	return msgs, nil
}

// AddMemoryMessage stores a message into an owned memory.
func (s *Service) AddMemoryMessage(ctx context.Context, tenantID, memoryID, agentID, sessionID, userInput, agentResponse string, scopeAll bool) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	if strings.TrimSpace(userInput) == "" || strings.TrimSpace(agentResponse) == "" {
		return httperr.BadRequest(40096, "user_input and agent_response are required")
	}
	if err := s.RAGFlow.AddMemoryMessage(ctx, ragflow.AddMessageRequest{MemoryID: memoryID, AgentID: agentID, SessionID: sessionID, UserInput: userInput, AgentResponse: agentResponse}); err != nil {
		return httperr.New(502, 50275, "ragflow add memory message failed")
	}
	return nil
}

// DeleteMemoryMessage forgets a message of an owned memory.
func (s *Service) DeleteMemoryMessage(ctx context.Context, tenantID, memoryID string, messageID int64, scopeAll bool) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	if err := s.RAGFlow.DeleteMemoryMessage(ctx, memoryID, messageID); err != nil {
		return httperr.New(502, 50276, "ragflow forget memory message failed")
	}
	return nil
}

// UpdateMemoryMessageStatus sets a message's remember/forget status.
func (s *Service) UpdateMemoryMessageStatus(ctx context.Context, tenantID, memoryID string, messageID int64, status bool, scopeAll bool) error {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return err
	}
	if mem == nil {
		return httperr.NotFound("memory not found")
	}
	if err := s.RAGFlow.UpdateMemoryMessageStatus(ctx, memoryID, messageID, status); err != nil {
		return httperr.New(502, 50277, "ragflow update memory message failed")
	}
	return nil
}

// SearchMemoryMessages runs a semantic retrieval against an owned memory.
func (s *Service) SearchMemoryMessages(ctx context.Context, tenantID, memoryID, query string, threshold, weight float64, topN int, scopeAll bool) ([]ragflow.MemoryMessage, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	rows, err := s.RAGFlow.SearchMemoryMessages(ctx, memoryID, ragflow.MemorySearchParams{Query: query, SimilarityThreshold: threshold, KeywordsSimilarityWeight: weight, TopN: topN})
	if err != nil {
		return nil, httperr.New(502, 50278, "ragflow search memory messages failed")
	}
	return rows, nil
}

// GetMemoryMessageContent returns an owned memory message's full content.
func (s *Service) GetMemoryMessageContent(ctx context.Context, tenantID, memoryID string, messageID int64, scopeAll bool) (json.RawMessage, error) {
	mem, err := s.Store.GetMemoryShadow(ctx, tenantID, memoryID, scopeAll)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, httperr.NotFound("memory not found")
	}
	content, err := s.RAGFlow.GetMemoryMessageContent(ctx, memoryID, messageID)
	if err != nil {
		return nil, httperr.New(502, 50279, "ragflow get memory message content failed")
	}
	return content, nil
}
