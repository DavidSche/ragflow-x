package ragflow

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// mockChat and mockSession model the in-memory chat state for the Mock
// provider. State is keyed per Mock instance so tests never share data.
type mockChat struct {
	id         string
	name       string
	status     string
	datasetIDs []string
	created    int64
}

type mockSession struct {
	id       string
	name     string
	messages []Message
}

type mockChatDB struct {
	chats    map[string]*mockChat
	sessions map[string]map[string]*mockSession
}

var (
	mockStateMu sync.Mutex
	mockChatMap = map[*Mock]*mockChatDB{}
)

func (m *Mock) chatDB() *mockChatDB {
	mockStateMu.Lock()
	defer mockStateMu.Unlock()
	if d := mockChatMap[m]; d != nil {
		return d
	}
	d := &mockChatDB{
		chats:    map[string]*mockChat{},
		sessions: map[string]map[string]*mockSession{},
	}
	mockChatMap[m] = d
	return d
}

func (m *Mock) ListChats(ctx context.Context) ([]Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	out := make([]Chat, 0, len(db.chats))
	for _, ch := range db.chats {
		out = append(out, Chat{ID: ch.id, Name: ch.name, Status: ch.status, DatasetIDs: append([]string(nil), ch.datasetIDs...), CreateTime: ch.created})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Mock) GetChat(ctx context.Context, chatID string) (*Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	ch, ok := db.chats[chatID]
	if !ok {
		return nil, fmt.Errorf("chat not found: %s", chatID)
	}
	return &Chat{ID: ch.id, Name: ch.name, Status: ch.status, DatasetIDs: append([]string(nil), ch.datasetIDs...), CreateTime: ch.created}, nil
}

func (m *Mock) CreateChat(ctx context.Context, req CreateChatRequest) (*Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatCreateRequests = append(m.chatCreateRequests, req)
	db := m.chatDB()
	ch := &mockChat{id: id.New(), name: req.Name, status: "active", datasetIDs: append([]string(nil), req.DatasetIDs...), created: time.Now().Unix()}
	db.chats[ch.id] = ch
	db.sessions[ch.id] = map[string]*mockSession{}
	return &Chat{ID: ch.id, Name: ch.name, Status: ch.status, DatasetIDs: append([]string(nil), ch.datasetIDs...), CreateTime: ch.created}, nil
}

func (m *Mock) UpdateChat(ctx context.Context, chatID string, req UpdateChatRequest) (*Chat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	ch, ok := db.chats[chatID]
	if !ok {
		return nil, fmt.Errorf("chat not found: %s", chatID)
	}
	if req.Name != "" {
		ch.name = req.Name
	}
	if req.DatasetIDs != nil {
		ch.datasetIDs = append([]string(nil), req.DatasetIDs...)
	}
	return &Chat{ID: ch.id, Name: ch.name, Status: ch.status, DatasetIDs: append([]string(nil), ch.datasetIDs...), CreateTime: ch.created}, nil
}

func (m *Mock) DeleteChat(ctx context.Context, chatID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	if _, ok := db.chats[chatID]; !ok {
		return fmt.Errorf("chat not found: %s", chatID)
	}
	delete(db.chats, chatID)
	delete(db.sessions, chatID)
	return nil
}

func (m *Mock) ListChatSessions(ctx context.Context, chatID string, opts SessionListOptions) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	if _, ok := db.chats[chatID]; !ok {
		return nil, fmt.Errorf("chat not found: %s", chatID)
	}
	out := make([]Session, 0, len(db.sessions[chatID]))
	for _, s := range db.sessions[chatID] {
		out = append(out, Session{ID: s.id, ChatID: chatID, Name: s.name, Messages: append([]Message(nil), s.messages...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Mock) GetChatSession(ctx context.Context, chatID, sessionID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	s, ok := db.sessions[chatID][sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return &Session{ID: s.id, ChatID: chatID, Name: s.name, Messages: append([]Message(nil), s.messages...)}, nil
}

func (m *Mock) CreateChatSession(ctx context.Context, chatID, name string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	if _, ok := db.chats[chatID]; !ok {
		return nil, fmt.Errorf("chat not found: %s", chatID)
	}
	s := &mockSession{id: id.New(), name: name}
	if db.sessions[chatID] == nil {
		db.sessions[chatID] = map[string]*mockSession{}
	}
	db.sessions[chatID][s.id] = s
	return &Session{ID: s.id, ChatID: chatID, Name: s.name}, nil
}

func (m *Mock) DeleteChatSessions(ctx context.Context, chatID string, sessionIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	byID := map[string]bool{}
	for _, sid := range sessionIDs {
		byID[sid] = true
	}
	for sid := range db.sessions[chatID] {
		if byID[sid] {
			delete(db.sessions[chatID], sid)
		}
	}
	return nil
}

func (m *Mock) ListSessionMessages(ctx context.Context, chatID, sessionID string) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	s, ok := db.sessions[chatID][sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return append([]Message(nil), s.messages...), nil
}

func (m *Mock) ListChatSessionMessages(ctx context.Context, chatID, sessionID string, opts SessionMessagePageOptions) ([]Message, string, error) {
	s, err := m.GetChatSession(ctx, chatID, sessionID)
	if err != nil {
		return nil, "", err
	}
	items, next := pageSessionMessages(s.Messages, opts)
	return items, next, nil
}

func (m *Mock) ChatCompletion(ctx context.Context, chatID string, req CompletionRequest) (*CompletionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	if _, ok := db.chats[chatID]; !ok {
		return nil, fmt.Errorf("chat not found: %s", chatID)
	}
	s := m.mockEnsureSessionLocked(db, chatID, req.SessionID)
	s.messages = append(s.messages, req.Messages...)
	return &CompletionResponse{
		ID:      "mock-cmpl-" + chatID,
		Choices: []CompletionChoice{{Message: Message{Role: "assistant", Content: "ok (mock chat)"}}},
		Usage:   &CompletionUsage{PromptTokens: 10, CompletionTokens: 5},
	}, nil
}

func (m *Mock) StreamChatCompletion(ctx context.Context, chatID string, req CompletionRequest, w io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	if _, ok := db.chats[chatID]; !ok {
		return fmt.Errorf("chat not found: %s", chatID)
	}
	_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"role":"assistant","content":"ok (mock stream)"}}]}`+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	return nil
}

func (m *Mock) mockEnsureSessionLocked(db *mockChatDB, chatID, sessionID string) *mockSession {
	if s := db.sessions[chatID][sessionID]; s != nil {
		return s
	}
	if db.sessions[chatID] == nil {
		db.sessions[chatID] = map[string]*mockSession{}
	}
	name := "New session"
	if sessionID == "" {
		sessionID = id.New()
	}
	s := &mockSession{id: sessionID, name: name}
	db.sessions[chatID][sessionID] = s
	return s
}

func (m *Mock) UpdateChatSession(ctx context.Context, chatID, sessionID, name string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := m.chatDB()
	s, ok := db.sessions[chatID][sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	s.name = name
	return &Session{ID: s.id, ChatID: chatID, Name: s.name, Messages: append([]Message(nil), s.messages...)}, nil
}

func (m *Mock) GetChunk(ctx context.Context, datasetID, documentID, chunkID string) (*Chunk, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.datasets {
		if d.id != datasetID {
			continue
		}
		for _, doc := range d.docs {
			if doc.id == documentID {
				return &Chunk{ID: chunkID, DocumentID: documentID, DocName: doc.name, Content: "mock chunk content for " + doc.name}, nil
			}
		}
	}
	return nil, fmt.Errorf("chunk not found")
}

func (m *Mock) ListModels(ctx context.Context, modelType string) ([]AddedModel, error) {
	return nil, nil
}

func (m *Mock) ListDefaultModels(ctx context.Context) ([]DefaultModel, error) {
	return nil, nil
}
