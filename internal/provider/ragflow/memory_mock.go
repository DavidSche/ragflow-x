package ragflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type mockMemory struct {
	id               string
	name             string
	description      string
	memoryType       []string
	storageType      string
	embdID           string
	llmID            string
	permissions      string
	memorySize       int
	forgettingPolicy string
	temperature      float64
	systemPrompt     string
	userPrompt       string
	createTime       int64
	updateTime       int64
	messages         []MemoryMessage
	nextMessageID    int64
}

func fromMockMemory(m *mockMemory) Memory {
	return Memory{
		ID: m.id, Name: m.name, Description: m.description,
		MemoryType: m.memoryType, StorageType: m.storageType, EmbdID: m.embdID, LLMID: m.llmID,
		Permissions: m.permissions, MemorySize: m.memorySize, ForgettingPolicy: m.forgettingPolicy,
		Temperature: m.temperature, SystemPrompt: m.systemPrompt, UserPrompt: m.userPrompt,
		CreateTime: m.createTime, UpdateTime: m.updateTime,
	}
}

// ListMemories returns the mock memories.
func (m *Mock) ListMemories(ctx context.Context, f ListMemoriesFilter) ([]Memory, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.memories) == 0 {
		return []Memory{}, 0, nil
	}
	out := make([]Memory, 0, len(m.memories))
	for _, mem := range m.memories {
		if f.Keywords != "" && !strings.Contains(strings.ToLower(mem.name), strings.ToLower(f.Keywords)) {
			continue
		}
		out = append(out, fromMockMemory(mem))
	}
	if len(out) == 0 {
		return []Memory{}, 0, nil
	}
	return out, int64(len(out)), nil
}

// GetMemoryConfig returns a mock memory.
func (m *Mock) GetMemoryConfig(ctx context.Context, memoryID string) (*Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memories[memoryID]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	v := fromMockMemory(mem)
	return &v, nil
}

// CreateMemory creates a mock memory.
func (m *Mock) CreateMemory(ctx context.Context, req CreateMemoryRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	mem := &mockMemory{
		id: id.New(), name: req.Name, memoryType: req.MemoryType,
		embdID: req.EmbdID, llmID: req.LLMID, storageType: "table", permissions: "me",
		memorySize: 5242880, forgettingPolicy: "FIFO", temperature: 0.5,
		createTime: now, updateTime: now, nextMessageID: 1,
	}
	if len(mem.memoryType) == 0 {
		mem.memoryType = []string{"raw"}
	}
	m.memories[mem.id] = mem
	return mem.id, nil
}

// UpdateMemory updates a mock memory.
func (m *Mock) UpdateMemory(ctx context.Context, memoryID string, req UpdateMemoryRequest) (*Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memories[memoryID]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	if req.Name != nil {
		mem.name = *req.Name
	}
	if req.Permissions != nil {
		mem.permissions = *req.Permissions
	}
	if req.LLMID != nil {
		mem.llmID = *req.LLMID
	}
	if req.EmbdID != nil {
		mem.embdID = *req.EmbdID
	}
	if req.MemoryType != nil {
		mem.memoryType = req.MemoryType
	}
	if req.MemorySize != nil {
		mem.memorySize = *req.MemorySize
	}
	if req.ForgettingPolicy != nil {
		mem.forgettingPolicy = *req.ForgettingPolicy
	}
	if req.Temperature != nil {
		mem.temperature = *req.Temperature
	}
	if req.Description != nil {
		mem.description = *req.Description
	}
	if req.SystemPrompt != nil {
		mem.systemPrompt = *req.SystemPrompt
	}
	if req.UserPrompt != nil {
		mem.userPrompt = *req.UserPrompt
	}
	mem.updateTime = time.Now().Unix()
	v := fromMockMemory(mem)
	return &v, nil
}

// DeleteMemory deletes a mock memory.
func (m *Mock) DeleteMemory(ctx context.Context, memoryID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.memories[memoryID]; !ok {
		return fmt.Errorf("memory not found: %s", memoryID)
	}
	delete(m.memories, memoryID)
	return nil
}

// ListMemoryMessages returns a mock memory's messages.
func (m *Mock) ListMemoryMessages(ctx context.Context, memoryID string) ([]MemoryMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memories[memoryID]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	out := make([]MemoryMessage, len(mem.messages))
	copy(out, mem.messages)
	return out, nil
}

// AddMemoryMessage appends a message to all of the memory ids present.
func (m *Mock) AddMemoryMessage(ctx context.Context, req AddMessageRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.MemoryID == "" {
		return fmt.Errorf("memory_id is required")
	}
	mem, ok := m.memories[req.MemoryID]
	if !ok {
		return fmt.Errorf("memory not found: %s", req.MemoryID)
	}
	msg := MemoryMessage{
		MessageID: mem.nextMessageID, MemoryID: mem.id, AgentID: req.AgentID, SessionID: req.SessionID,
		UserID: "", UserInput: req.UserInput, AgentResponse: req.AgentResponse,
		Status: true, CreateTime: time.Now().Unix(),
	}
	mem.messages = append(mem.messages, msg)
	mem.nextMessageID++
	return nil
}

// DeleteMemoryMessage forgets a message.
func (m *Mock) DeleteMemoryMessage(ctx context.Context, memoryID string, messageID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memories[memoryID]
	if !ok {
		return fmt.Errorf("memory not found: %s", memoryID)
	}
	kept := mem.messages[:0]
	for _, msg := range mem.messages {
		if msg.MessageID != messageID {
			kept = append(kept, msg)
		}
	}
	mem.messages = kept
	return nil
}

// UpdateMemoryMessageStatus sets a message's status.
func (m *Mock) UpdateMemoryMessageStatus(ctx context.Context, memoryID string, messageID int64, status bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memories[memoryID]
	if !ok {
		return fmt.Errorf("memory not found: %s", memoryID)
	}
	for i := range mem.messages {
		if mem.messages[i].MessageID == messageID {
			mem.messages[i].Status = status
			return nil
		}
	}
	return fmt.Errorf("message not found: %d", messageID)
}

// SearchMemoryMessages returns messages whose text matches the query.
func (m *Mock) SearchMemoryMessages(ctx context.Context, memoryID string, p MemorySearchParams) ([]MemoryMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memories[memoryID]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	q := strings.ToLower(strings.TrimSpace(p.Query))
	var out []MemoryMessage
	for _, msg := range mem.messages {
		if q == "" || strings.Contains(strings.ToLower(msg.UserInput), q) || strings.Contains(strings.ToLower(msg.AgentResponse), q) {
			out = append(out, msg)
		}
	}
	if p.TopN > 0 && len(out) > p.TopN {
		out = out[:p.TopN]
	}
	return out, nil
}

// GetMemoryMessageContent returns a message's content as JSON.
func (m *Mock) GetMemoryMessageContent(ctx context.Context, memoryID string, messageID int64) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memories[memoryID]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	for _, msg := range mem.messages {
		if msg.MessageID == messageID {
			raw, err := json.Marshal(map[string]interface{}{"user_input": msg.UserInput, "agent_response": msg.AgentResponse})
			return raw, err
		}
	}
	return nil, fmt.Errorf("message not found: %d", messageID)
}
