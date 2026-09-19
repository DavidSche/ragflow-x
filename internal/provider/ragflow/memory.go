package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// Memory mirrors the RAGFlow Memory (long-term memory) fields used by
// RAGFlow-X for the memory administration surface.
type Memory struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	TenantID         string   `json:"tenant_id,omitempty"`
	MemoryType       []string `json:"memory_type,omitempty"`
	StorageType      string   `json:"storage_type,omitempty"`
	EmbdID           string   `json:"embd_id,omitempty"`
	LLMID            string   `json:"llm_id,omitempty"`
	Permissions      string   `json:"permissions,omitempty"`
	MemorySize       int      `json:"memory_size,omitempty"`
	ForgettingPolicy string   `json:"forgetting_policy,omitempty"`
	Temperature      float64  `json:"temperature,omitempty"`
	SystemPrompt     string   `json:"system_prompt,omitempty"`
	UserPrompt       string   `json:"user_prompt,omitempty"`
	CreateTime       int64    `json:"create_time,omitempty"`
	UpdateTime       int64    `json:"update_time,omitempty"`
}

// MemoryList mirrors the paginated GET /memories response.
type MemoryList struct {
	Items      []Memory `json:"memory_list"`
	TotalCount int64    `json:"total_count"`
}

// ListMemoriesFilter carries the memory list filter parameters.
type ListMemoriesFilter struct {
	Keywords    string
	MemoryType  string
	StorageType string
	Page        int
	PageSize    int
}

// CreateMemoryRequest mirrors RAGFlow's POST /memories body.
type CreateMemoryRequest struct {
	Name       string   `json:"name"`
	MemoryType []string `json:"memory_type"`
	EmbdID     string   `json:"embd_id"`
	LLMID      string   `json:"llm_id"`
}

// UpdateMemoryRequest mirrors the whitelist of PUT /memories/<id> fields.
type UpdateMemoryRequest struct {
	Name             *string  `json:"name,omitempty"`
	Permissions      *string  `json:"permissions,omitempty"`
	LLMID            *string  `json:"llm_id,omitempty"`
	EmbdID           *string  `json:"embd_id,omitempty"`
	MemoryType       []string `json:"memory_type,omitempty"`
	MemorySize       *int     `json:"memory_size,omitempty"`
	ForgettingPolicy *string  `json:"forgetting_policy,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	Description      *string  `json:"description,omitempty"`
	SystemPrompt     *string  `json:"system_prompt,omitempty"`
	UserPrompt       *string  `json:"user_prompt,omitempty"`
}

// MemoryMessage is a single stored memory message.
type MemoryMessage struct {
	MessageID     int64  `json:"message_id"`
	MemoryID      string `json:"memory_id"`
	AgentID       string `json:"agent_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	UserInput     string `json:"user_input,omitempty"`
	AgentResponse string `json:"agent_response,omitempty"`
	Status        bool   `json:"status"`
	Content       string `json:"content,omitempty"`
	CreateTime    int64  `json:"create_time,omitempty"`
}

// AddMessageRequest mirrors POST /messages.
type AddMessageRequest struct {
	MemoryID      string `json:"memory_id"`
	AgentID       string `json:"agent_id"`
	SessionID     string `json:"session_id"`
	UserInput     string `json:"user_input"`
	AgentResponse string `json:"agent_response"`
}

// ListMemories lists the accessible memories.
func (c *HTTPClient) ListMemories(ctx context.Context, f ListMemoriesFilter) ([]Memory, int64, error) {
	page, size := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(size))
	if f.Keywords != "" {
		q.Set("keywords", f.Keywords)
	}
	if f.StorageType != "" {
		q.Set("storage_type", f.StorageType)
	}
	var out MemoryList
	if err := c.do(ctx, http.MethodGet, "/memories?"+q.Encode(), nil, "", &out); err != nil {
		return nil, 0, err
	}
	return out.Items, out.TotalCount, nil
}

// GetMemoryConfig returns a memory's live configuration.
func (c *HTTPClient) GetMemoryConfig(ctx context.Context, memoryID string) (*Memory, error) {
	var out Memory
	path := "/memories/" + url.PathEscape(memoryID) + "/config"
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateMemory creates a memory and returns its id.
func (c *HTTPClient) CreateMemory(ctx context.Context, req CreateMemoryRequest) (string, error) {
	body, _ := json.Marshal(req)
	var out Memory
	if err := c.do(ctx, http.MethodPost, "/memories", bytes.NewReader(body), "application/json", &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UpdateMemory updates a memory's settings.
func (c *HTTPClient) UpdateMemory(ctx context.Context, memoryID string, req UpdateMemoryRequest) (*Memory, error) {
	body, _ := json.Marshal(req)
	var out Memory
	path := "/memories/" + url.PathEscape(memoryID)
	if err := c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMemory deletes a memory.
func (c *HTTPClient) DeleteMemory(ctx context.Context, memoryID string) error {
	path := "/memories/" + url.PathEscape(memoryID)
	return c.do(ctx, http.MethodDelete, path, nil, "", nil)
}

// ListMemoryMessages lists the messages of a memory (GET /memories/<id>).
func (c *HTTPClient) ListMemoryMessages(ctx context.Context, memoryID string) ([]MemoryMessage, error) {
	var out struct {
		Messages struct {
			MessageList []MemoryMessage `json:"message_list"`
		} `json:"messages"`
	}
	path := "/memories/" + url.PathEscape(memoryID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out.Messages.MessageList, nil
}

// AddMessage stores a memory message (POST /messages).
func (c *HTTPClient) AddMemoryMessage(ctx context.Context, req AddMessageRequest) error {
	body, _ := json.Marshal(req)
	return c.do(ctx, http.MethodPost, "/messages", bytes.NewReader(body), "application/json", nil)
}

// DeleteMemoryMessage forgets a message (DELETE /messages/<memory_id>:<message_id>).
func (c *HTTPClient) DeleteMemoryMessage(ctx context.Context, memoryID string, messageID int64) error {
	path := "/messages/" + url.PathEscape(memoryID) + ":" + strconv.FormatInt(messageID, 10)
	return c.do(ctx, http.MethodDelete, path, nil, "", nil)
}

// UpdateMemoryMessageStatus sets a message's status (PUT /messages/<memory_id>:<message_id>).
func (c *HTTPClient) UpdateMemoryMessageStatus(ctx context.Context, memoryID string, messageID int64, status bool) error {
	body, _ := json.Marshal(map[string]bool{"status": status})
	path := "/messages/" + url.PathEscape(memoryID) + ":" + strconv.FormatInt(messageID, 10)
	return c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json", nil)
}

// MemorySearchParams carries the semantic memory-search parameters.
type MemorySearchParams struct {
	Query                    string
	SimilarityThreshold      float64
	KeywordsSimilarityWeight float64
	TopN                     int
}

// SearchMemoryMessages runs a semantic retrieval against a memory.
func (c *HTTPClient) SearchMemoryMessages(ctx context.Context, memoryID string, p MemorySearchParams) ([]MemoryMessage, error) {
	q := url.Values{}
	q.Set("memory_id", memoryID)
	if p.Query != "" {
		q.Set("query", p.Query)
	}
	if p.SimilarityThreshold != 0 {
		q.Set("similarity_threshold", strconv.FormatFloat(p.SimilarityThreshold, 'f', -1, 64))
	}
	if p.KeywordsSimilarityWeight != 0 {
		q.Set("keywords_similarity_weight", strconv.FormatFloat(p.KeywordsSimilarityWeight, 'f', -1, 64))
	}
	if p.TopN > 0 {
		q.Set("top_n", strconv.Itoa(p.TopN))
	}
	var out []MemoryMessage
	if err := c.do(ctx, http.MethodGet, "/messages/search?"+q.Encode(), nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetMemoryMessageContent returns a message's full content.
func (c *HTTPClient) GetMemoryMessageContent(ctx context.Context, memoryID string, messageID int64) (json.RawMessage, error) {
	var out json.RawMessage
	path := "/messages/" + url.PathEscape(memoryID) + ":" + strconv.FormatInt(messageID, 10) + "/content"
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}
