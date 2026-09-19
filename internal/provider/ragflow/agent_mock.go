package ragflow

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type mockAgent struct {
	id             string
	title          string
	description    string
	canvasType     string
	canvasCategory string
	release        bool
	dsl            map[string]interface{}
	updateTime     int64
	versions       []AgentVersion
	sessions       []AgentSession
}

func fromMockAgent(a *mockAgent) Agent {
	return Agent{
		ID: a.id, Title: a.title, Description: a.description, CanvasType: a.canvasType,
		CanvasCategory: a.canvasCategory, Release: a.release, Dsl: a.dsl, UpdateTime: a.updateTime,
	}
}

func (m *Mock) agentMgr(agentID string) (*mockAgent, error) {
	a := m.agents[agentID]
	if a == nil {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}
	return a, nil
}

// ListAgents lists the mock agents.
func (m *Mock) ListAgents(ctx context.Context, f ListAgentsFilter) ([]Agent, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Agent, 0, len(m.agents))
	for _, a := range m.agents {
		if f.Keywords != "" && !strings.Contains(strings.ToLower(a.title), strings.ToLower(f.Keywords)) {
			continue
		}
		out = append(out, fromMockAgent(a))
	}
	return out, int64(len(out)), nil
}

// GetAgent returns a single mock agent.
func (m *Mock) GetAgent(ctx context.Context, agentID string) (*Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return nil, err
	}
	v := fromMockAgent(a)
	return &v, nil
}

// CreateAgent creates a mock agent.
func (m *Mock) CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	a := &mockAgent{
		id: id.New(), title: req.Title, description: req.Description, canvasType: req.CanvasType,
		canvasCategory: req.CanvasCategory, release: req.Release, dsl: req.Dsl, updateTime: now,
		versions: []AgentVersion{{
			ID: id.New(), Title: req.Title, Release: req.Release, Dsl: req.Dsl, UpdateTime: now,
		}},
	}
	m.agents[a.id] = a
	v := fromMockAgent(a)
	return &v, nil
}

// UpdateAgent updates a mock agent.
func (m *Mock) UpdateAgent(ctx context.Context, agentID string, req UpdateAgentRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return err
	}
	if req.Title != "" {
		a.title = req.Title
	}
	if req.Dsl != nil {
		a.dsl = req.Dsl
		now := time.Now().Unix()
		a.versions = append(a.versions, AgentVersion{ID: id.New(), Title: a.title, Release: a.release, Dsl: req.Dsl, UpdateTime: now})
	}
	if req.Release != nil {
		a.release = *req.Release
		if len(a.versions) > 0 {
			a.versions[len(a.versions)-1].Release = *req.Release
		}
	}
	a.updateTime = time.Now().Unix()
	return nil
}

// DeleteAgent deletes a mock agent.
func (m *Mock) DeleteAgent(ctx context.Context, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.agentMgr(agentID); err != nil {
		return err
	}
	delete(m.agents, agentID)
	return nil
}

// ListAgentVersions lists a mock agent's versions.
func (m *Mock) ListAgentVersions(ctx context.Context, agentID string) ([]AgentVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return nil, err
	}
	out := make([]AgentVersion, len(a.versions))
	copy(out, a.versions)
	return out, nil
}

// GetAgentVersion returns a single mock agent version by ID.
func (m *Mock) GetAgentVersion(ctx context.Context, agentID, versionID string) (*AgentVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return nil, err
	}
	for _, v := range a.versions {
		if v.ID == versionID {
			out := v
			return &out, nil
		}
	}
	return nil, fmt.Errorf("version not found: %s", versionID)
}

// RollbackAgentVersion restores a mock agent's DSL from a saved version.
func (m *Mock) RollbackAgentVersion(ctx context.Context, agentID, versionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return err
	}
	for _, v := range a.versions {
		if v.ID == versionID {
			a.dsl = v.Dsl
			a.updateTime = time.Now().Unix()
			return nil
		}
	}
	return fmt.Errorf("version not found: %s", versionID)
}

// ListAgentSessions lists a mock agent's sessions.
func (m *Mock) ListAgentSessions(ctx context.Context, agentID string, opts SessionListOptions) ([]AgentSession, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return nil, 0, err
	}
	all := make([]AgentSession, len(a.sessions))
	copy(all, a.sessions)
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	page, pageSize := opts.normalized()
	start := (page - 1) * pageSize
	if start >= len(all) {
		return []AgentSession{}, 0, nil
	}
	end := min(start+pageSize, len(all))
	return all[start:end], int64(len(all)), nil
}

// CreateAgentSession creates a mock agent session.
func (m *Mock) CreateAgentSession(ctx context.Context, agentID, name string) (*AgentSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return nil, err
	}
	s := AgentSession{
		ID: id.New(), DialogID: agentID, Name: name, Source: "agent",
		Messages: []Message{{Role: "assistant", Content: "Hello, how can I help?"}}, UpdateTime: time.Now().Unix(),
	}
	a.sessions = append(a.sessions, s)
	out := s
	return &out, nil
}

// GetAgentSession returns a mock agent session.
func (m *Mock) GetAgentSession(ctx context.Context, agentID, sessionID string) (*AgentSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return nil, err
	}
	for _, s := range a.sessions {
		if s.ID == sessionID {
			out := s
			return &out, nil
		}
	}
	return nil, fmt.Errorf("session not found: %s", sessionID)
}

func (m *Mock) ListAgentSessionMessages(ctx context.Context, agentID, sessionID string, opts SessionMessagePageOptions) ([]Message, string, error) {
	s, err := m.GetAgentSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, "", err
	}
	items, next := pageSessionMessages(s.Messages, opts)
	return items, next, nil
}

// DeleteAgentSession deletes a mock agent session.
func (m *Mock) DeleteAgentSession(ctx context.Context, agentID, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.agentMgr(agentID)
	if err != nil {
		return err
	}
	kept := a.sessions[:0]
	for _, s := range a.sessions {
		if s.ID != sessionID {
			kept = append(kept, s)
		}
	}
	a.sessions = kept
	return nil
}

// AgentChatCompletion returns a deterministic mock completion.
func (m *Mock) AgentChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	content := "mock agent response"
	if len(req.Messages) > 0 {
		content = "mock agent response to: " + req.Messages[len(req.Messages)-1].Content
	}
	return &CompletionResponse{
		ID:      id.New(),
		Choices: []CompletionChoice{{Message: Message{Role: "assistant", Content: content}}},
		Usage:   &CompletionUsage{PromptTokens: 8, CompletionTokens: 4},
	}, nil
}

// StreamAgentChatCompletion writes a mock agent completion as SSE.
func (m *Mock) StreamAgentChatCompletion(ctx context.Context, req CompletionRequest, w io.Writer) error {
	res, err := m.AgentChatCompletion(ctx, req)
	if err != nil {
		return err
	}
	content := "mock agent response"
	if len(req.Messages) > 0 {
		content = "mock agent response to: " + req.Messages[len(req.Messages)-1].Content
	}
	_ = content
	_ = res
	_, err = w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"mock agent response\"}}]}\n\n"))
	return err
}
