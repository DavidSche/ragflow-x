package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Agent mirrors the RAGFlow agent canvas fields used by RAGFlow-X for the agent
// administration surface. RAGFlow remains the source of truth for the DSL.
type Agent struct {
	ID              string                 `json:"id"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description,omitempty"`
	CanvasType      string                 `json:"canvas_type,omitempty"`
	CanvasCategory  string                 `json:"canvas_category,omitempty"`
	Release         bool                   `json:"release"`
	Dsl             map[string]interface{} `json:"dsl,omitempty"`
	LastPublishTime int64                  `json:"last_publish_time,omitempty"`
	UpdateTime      int64                  `json:"update_time,omitempty"`
}

// CreateAgentRequest mirrors RAGFlow's POST /agents body.
type CreateAgentRequest struct {
	Title          string                 `json:"title"`
	Description    string                 `json:"description,omitempty"`
	CanvasType     string                 `json:"canvas_type,omitempty"`
	CanvasCategory string                 `json:"canvas_category,omitempty"`
	Release        bool                   `json:"release"`
	Dsl            map[string]interface{} `json:"dsl"`
}

// UpdateAgentRequest mirrors RAGFlow's PUT /agents/<id> body.
type UpdateAgentRequest struct {
	Title      string                 `json:"title,omitempty"`
	CanvasType string                 `json:"canvas_type,omitempty"`
	Release    *bool                  `json:"release,omitempty"`
	Dsl        map[string]interface{} `json:"dsl,omitempty"`
}

// AgentVersion is a saved agent canvas version.
type AgentVersion struct {
	ID         string                 `json:"id"`
	Title      string                 `json:"title"`
	Release    bool                   `json:"release"`
	Dsl        map[string]interface{} `json:"dsl,omitempty"`
	UpdateTime int64                  `json:"update_time,omitempty"`
}

// AgentSession is an agent conversation session.
type AgentSession struct {
	ID         string    `json:"id"`
	DialogID   string    `json:"dialog_id,omitempty"`
	Name       string    `json:"name,omitempty"`
	UserID     string    `json:"user_id,omitempty"`
	Source     string    `json:"source,omitempty"`
	Messages   []Message `json:"message,omitempty"`
	UpdateTime int64     `json:"update_time,omitempty"`
}

// ListAgentsFilter mirrors the agent list query parameters.
type ListAgentsFilter struct {
	Keywords string
	Page     int
	PageSize int
}

// ListAgents lists the caller's agents.
func (c *HTTPClient) ListAgents(ctx context.Context, f ListAgentsFilter) ([]Agent, int64, error) {
	page, size := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 30
	}
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(size))
	if f.Keywords != "" {
		q.Set("keywords", f.Keywords)
	}
	var out struct {
		Canvas []Agent `json:"canvas"`
		Total  int64   `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, "/agents?"+q.Encode(), nil, "", &out); err != nil {
		return nil, 0, err
	}
	return out.Canvas, out.Total, nil
}

// GetAgent returns a single agent canvas.
func (c *HTTPClient) GetAgent(ctx context.Context, agentID string) (*Agent, error) {
	var out Agent
	path := "/agents/" + url.PathEscape(agentID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateAgent creates an agent.
func (c *HTTPClient) CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	body, _ := json.Marshal(req)
	var out Agent
	if err := c.do(ctx, http.MethodPost, "/agents", bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAgent updates an agent (title/dsl/release -> publish or unpublish).
func (c *HTTPClient) UpdateAgent(ctx context.Context, agentID string, req UpdateAgentRequest) error {
	body, _ := json.Marshal(req)
	path := "/agents/" + url.PathEscape(agentID)
	return c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json", nil)
}

// DeleteAgent deletes an agent.
func (c *HTTPClient) DeleteAgent(ctx context.Context, agentID string) error {
	path := "/agents/" + url.PathEscape(agentID)
	return c.do(ctx, http.MethodDelete, path, nil, "", nil)
}

// ListAgentVersions lists the saved versions of an agent.
func (c *HTTPClient) ListAgentVersions(ctx context.Context, agentID string) ([]AgentVersion, error) {
	var out []AgentVersion
	path := "/agents/" + url.PathEscape(agentID) + "/versions"
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAgentVersion returns a single version of an agent by its version ID.
func (c *HTTPClient) GetAgentVersion(ctx context.Context, agentID, versionID string) (*AgentVersion, error) {
	var out AgentVersion
	path := "/agents/" + url.PathEscape(agentID) + "/versions/" + url.PathEscape(versionID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RollbackAgentVersion restores an agent's DSL from a saved version.
// It fetches the version's DSL and applies it to the agent via UpdateAgent.
func (c *HTTPClient) RollbackAgentVersion(ctx context.Context, agentID, versionID string) error {
	ver, err := c.GetAgentVersion(ctx, agentID, versionID)
	if err != nil {
		return err
	}
	return c.UpdateAgent(ctx, agentID, UpdateAgentRequest{Dsl: ver.Dsl})
}

// ListAgentSessions lists an agent's sessions.
// RAGFlow returns {code, message, data: [...sessions], total} where total is
// at the envelope level, not inside data. We parse the raw response manually
// using a flexible approach since RAGFlow session objects contain complex
// nested fields (message reference chunks, datetime strings, etc.) that can
// cause strict struct unmarshaling to fail.
func (c *HTTPClient) ListAgentSessions(ctx context.Context, agentID string, opts SessionListOptions) ([]AgentSession, int64, error) {
	page, pageSize := opts.normalized()
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	if opts.Keywords != "" {
		q.Set("keywords", opts.Keywords)
	}
	path := "/agents/" + url.PathEscape(agentID) + "/sessions?" + q.Encode()
	req, err := c.buildRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, wrapError("request ragflow", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, wrapError("read ragflow response", err)
	}
	if resp.StatusCode >= 300 {
		return nil, 0, NewErrorFromResponse(resp.StatusCode, raw)
	}
	var env struct {
		Code  int             `json:"code"`
		Data  json.RawMessage `json:"data"`
		Total int64           `json:"total"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, 0, wrapError("decode ragflow response", err)
	}
	if env.Code != 0 {
		return nil, 0, NewErrorFromResponse(resp.StatusCode, raw)
	}
	var sessions []AgentSession
	if len(env.Data) > 0 && string(env.Data) != "null" {
		// First try strict unmarshal.
		if err := json.Unmarshal(env.Data, &sessions); err != nil {
			// Fallback: unmarshal each session as a raw map and extract
			// only the fields we need, ignoring complex nested types.
			var rawSessions []map[string]json.RawMessage
			if uerr := json.Unmarshal(env.Data, &rawSessions); uerr != nil {
				return nil, 0, &Error{Type: ErrorTypeProtocol, Message: "failed to decode response data", Cause: uerr}
			}
			sessions = make([]AgentSession, 0, len(rawSessions))
			for _, raw := range rawSessions {
				s := AgentSession{
					Messages: []Message{},
				}
				getString(raw, "id", &s.ID)
				getString(raw, "name", &s.Name)
				getString(raw, "user_id", &s.UserID)
				getString(raw, "source", &s.Source)
				getString(raw, "dialog_id", &s.DialogID)
				getInt64(raw, "update_time", &s.UpdateTime)
				// Parse messages from raw JSON, ignoring any parse errors
				// on individual message fields.
				if msgRaw, ok := raw["message"]; ok {
					var msgs []struct {
						Role    string          `json:"role"`
						Content string          `json:"content"`
						Files   json.RawMessage `json:"files"`
					}
					if json.Unmarshal(msgRaw, &msgs) == nil {
						for _, m := range msgs {
							s.Messages = append(s.Messages, Message{
								Role:    m.Role,
								Content: m.Content,
							})
						}
					}
				}
				sessions = append(sessions, s)
			}
		}
	}
	return sessions, env.Total, nil
}

// getString extracts a string value from a raw JSON map.
func getString(raw map[string]json.RawMessage, key string, dest *string) {
	if v, ok := raw[key]; ok && string(v) != "null" {
		var s string
		if json.Unmarshal(v, &s) == nil {
			*dest = s
		}
	}
}

// getInt64 extracts an int64 value from a raw JSON map.
func getInt64(raw map[string]json.RawMessage, key string, dest *int64) {
	if v, ok := raw[key]; ok && string(v) != "null" {
		var n int64
		if json.Unmarshal(v, &n) == nil {
			*dest = n
		}
	}
}

// CreateAgentSession creates an agent session.
func (c *HTTPClient) CreateAgentSession(ctx context.Context, agentID, name string) (*AgentSession, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	path := "/agents/" + url.PathEscape(agentID) + "/sessions"
	req, err := c.buildRequest(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, wrapError("request ragflow agent session", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapError("read ragflow agent session", err)
	}
	if resp.StatusCode >= 300 {
		return nil, NewErrorFromResponse(resp.StatusCode, raw)
	}
	var env struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, wrapError("decode ragflow agent session", err)
	}
	if env.Code != 0 {
		return nil, NewErrorFromResponse(resp.StatusCode, raw)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, &Error{Type: ErrorTypeProtocol, Message: "ragflow agent session data missing"}
	}
	session := AgentSession{Messages: []Message{}}
	if err := json.Unmarshal(env.Data, &session); err != nil {
		var rawSessions []map[string]json.RawMessage
		if uerr := json.Unmarshal(env.Data, &rawSessions); uerr != nil || len(rawSessions) == 0 {
			return nil, &Error{Type: ErrorTypeProtocol, Message: "failed to decode agent session", Cause: err}
		}
		session = parseAgentSession(rawSessions[0])
	} else if session.ID == "" {
		var rawSession map[string]json.RawMessage
		if json.Unmarshal(env.Data, &rawSession) == nil {
			session = parseAgentSession(rawSession)
		}
	}
	if session.ID == "" {
		return nil, &Error{Type: ErrorTypeProtocol, Message: "ragflow agent session id missing"}
	}
	return &session, nil
}

func parseAgentSession(raw map[string]json.RawMessage) AgentSession {
	session := AgentSession{Messages: []Message{}}
	getString(raw, "id", &session.ID)
	getString(raw, "session_id", &session.ID)
	getString(raw, "dialog_id", &session.DialogID)
	getString(raw, "name", &session.Name)
	getString(raw, "user_id", &session.UserID)
	getString(raw, "source", &session.Source)
	getInt64(raw, "update_time", &session.UpdateTime)
	if messages, ok := raw["messages"]; ok {
		_ = json.Unmarshal(messages, &session.Messages)
	}
	if len(session.Messages) == 0 {
		if messages, ok := raw["message"]; ok {
			_ = json.Unmarshal(messages, &session.Messages)
		}
	}
	return session
}

// GetAgentSession returns an agent session with its messages.
func (c *HTTPClient) GetAgentSession(ctx context.Context, agentID, sessionID string) (*AgentSession, error) {
	var out AgentSession
	path := "/agents/" + url.PathEscape(agentID) + "/sessions/" + url.PathEscape(sessionID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAgentSessionMessages returns a bounded message page for an agent session.
func (c *HTTPClient) ListAgentSessionMessages(ctx context.Context, agentID, sessionID string, opts SessionMessagePageOptions) ([]Message, string, error) {
	s, err := c.GetAgentSession(ctx, agentID, sessionID)
	if err != nil {
		return nil, "", err
	}
	items, next := pageSessionMessages(s.Messages, opts)
	return items, next, nil
}

// DeleteAgentSession deletes an agent session.
func (c *HTTPClient) DeleteAgentSession(ctx context.Context, agentID, sessionID string) error {
	path := "/agents/" + url.PathEscape(agentID) + "/sessions/" + url.PathEscape(sessionID)
	return c.do(ctx, http.MethodDelete, path, nil, "", nil)
}

// AgentChatCompletion runs a non-streaming agent chat completion (OpenAI mode).
func (c *HTTPClient) AgentChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	payloadMap := map[string]interface{}{
		"agent_id": req.ChatID, "session_id": req.SessionID, "messages": req.Messages,
		"openai-compatible": true, "stream": false,
	}
	if len(req.Files) > 0 {
		payloadMap["files"] = req.Files
	}
	payload, _ := json.Marshal(payloadMap)

	// RAGFlow v0.27 returns a raw OpenAI-compatible body from this endpoint.
	// Unlike CRUD endpoints, choices are not wrapped in {"data":...}.
	httpReq, err := c.buildRequest(ctx, http.MethodPost, "/agents/chat/completions", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, wrapError("request ragflow agent completion", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapError("read ragflow agent completion", err)
	}
	if resp.StatusCode >= 300 {
		return nil, NewErrorFromResponse(resp.StatusCode, raw)
	}

	var out CompletionResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, wrapError("decode ragflow agent completion", err)
	}
	return &out, nil
}

// StreamAgentChatCompletion proxies an agent chat completion (SSE).
func (c *HTTPClient) StreamAgentChatCompletion(ctx context.Context, req CompletionRequest, w io.Writer) error {
	payloadMap := map[string]interface{}{
		"agent_id": req.ChatID, "session_id": req.SessionID, "messages": req.Messages,
		"openai-compatible": true, "stream": true,
	}
	if len(req.Files) > 0 {
		payloadMap["files"] = req.Files
	}
	payload, _ := json.Marshal(payloadMap)
	r, err := c.buildRequest(ctx, http.MethodPost, "/agents/chat/completions", bytes.NewReader(payload), "application/json")
	if err != nil {
		return err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return wrapError("request ragflow agent stream", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return NewErrorFromResponse(resp.StatusCode, raw)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}
