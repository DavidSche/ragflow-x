package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ListChats lists the authenticated tenant's chat assistants.
func (c *HTTPClient) ListChats(ctx context.Context) ([]Chat, error) {
	// Walk pages until a short page is returned so the list is not capped at
	// a single RAGFlow page_size.
	const pageSize = 100
	var all []Chat
	for page := 1; ; page++ {
		var out struct {
			Chats []Chat `json:"chats"`
			Total int64  `json:"total"`
		}
		path := fmt.Sprintf("/chats?page=%d&page_size=%d", page, pageSize)
		if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
			return nil, err
		}
		all = append(all, out.Chats...)
		if len(out.Chats) < pageSize {
			break
		}
	}
	return all, nil
}

// GetChat returns a single chat assistant.
func (c *HTTPClient) GetChat(ctx context.Context, chatID string) (*Chat, error) {
	var out Chat
	path := "/chats/" + url.PathEscape(chatID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateChat creates a chat assistant bound to the given datasets.
func (c *HTTPClient) CreateChat(ctx context.Context, req CreateChatRequest) (*Chat, error) {
	body, _ := json.Marshal(req)
	var out Chat
	if err := c.do(ctx, http.MethodPost, "/chats", bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateChat updates a chat assistant's mutable fields.
func (c *HTTPClient) UpdateChat(ctx context.Context, chatID string, req UpdateChatRequest) (*Chat, error) {
	body, _ := json.Marshal(req)
	var out Chat
	path := "/chats/" + url.PathEscape(chatID)
	if err := c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteChat deletes a chat assistant.
func (c *HTTPClient) DeleteChat(ctx context.Context, chatID string) error {
	path := "/chats/" + url.PathEscape(chatID)
	return c.do(ctx, http.MethodDelete, path, nil, "", nil)
}

// ListChatSessions lists the conversation sessions of a chat assistant.
func (c *HTTPClient) ListChatSessions(ctx context.Context, chatID string, opts SessionListOptions) ([]Session, error) {
	var out []Session
	page, pageSize := opts.normalized()
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	if opts.Name != "" {
		q.Set("name", opts.Name)
	}
	path := "/chats/" + url.PathEscape(chatID) + "/sessions?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetChatSession returns a session, including its messages.
func (c *HTTPClient) GetChatSession(ctx context.Context, chatID, sessionID string) (*Session, error) {
	var out Session
	path := "/chats/" + url.PathEscape(chatID) + "/sessions/" + url.PathEscape(sessionID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	attachSessionReferences(out.Messages, out.Reference)
	return &out, nil
}

// CreateChatSession creates a new session under a chat assistant.
func (c *HTTPClient) CreateChatSession(ctx context.Context, chatID, name string) (*Session, error) {
	if strings.TrimSpace(name) == "" {
		name = "New session"
	}
	body, _ := json.Marshal(map[string]string{"name": name})
	var out Session
	path := "/chats/" + url.PathEscape(chatID) + "/sessions"
	if err := c.do(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteChatSessions deletes the given sessions of a chat assistant.
func (c *HTTPClient) DeleteChatSessions(ctx context.Context, chatID string, sessionIDs []string) error {
	body, _ := json.Marshal(map[string][]string{"ids": sessionIDs})
	path := "/chats/" + url.PathEscape(chatID) + "/sessions"
	return c.do(ctx, http.MethodDelete, path, bytes.NewReader(body), "application/json", nil)
}

// ListSessionMessages returns the messages of a session.
func (c *HTTPClient) ListSessionMessages(ctx context.Context, chatID, sessionID string) ([]Message, error) {
	s, err := c.GetChatSession(ctx, chatID, sessionID)
	if err != nil {
		return nil, err
	}
	return s.Messages, nil
}

// ListChatSessionMessages returns a bounded message page without exposing the
// entire upstream session payload to ragflow-x clients.
func (c *HTTPClient) ListChatSessionMessages(ctx context.Context, chatID, sessionID string, opts SessionMessagePageOptions) ([]Message, string, error) {
	s, err := c.GetChatSession(ctx, chatID, sessionID)
	if err != nil {
		return nil, "", err
	}
	items, next := pageSessionMessages(s.Messages, opts)
	return items, next, nil
}

// ChatCompletion runs a non-streaming completion against a chat assistant.
func (c *HTTPClient) ChatCompletion(ctx context.Context, chatID string, req CompletionRequest) (*CompletionResponse, error) {
	req.ChatID = chatID
	req.Stream = false
	body, _ := json.Marshal(req)
	var out CompletionResponse
	if err := c.do(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	// RAGFlow v0.27's assistant completion returns its RAGFlow answer object
	// in `data`. Preserve that payload while exposing the OpenAI-compatible
	// `choices` shape expected by the platform.
	if len(out.Choices) == 0 && strings.TrimSpace(out.Answer) != "" {
		out.Choices = []CompletionChoice{{Message: Message{Role: "assistant", Content: out.Answer}}}
	}
	return &out, nil
}

// StreamChatCompletion streams a chat completion (SSE) from RAGFlow to w,
// passing the body through unchanged so the gateway can re-emit the stream.
func (c *HTTPClient) StreamChatCompletion(ctx context.Context, chatID string, req CompletionRequest, w io.Writer) error {
	req.ChatID = chatID
	req.Stream = true
	body, _ := json.Marshal(req)
	r, err := c.buildRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return wrapError("request ragflow stream", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return NewErrorFromResponse(resp.StatusCode, raw)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// UpdateChatSession renames a session of a chat assistant (PATCH).
func (c *HTTPClient) UpdateChatSession(ctx context.Context, chatID, sessionID, name string) (*Session, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	var out Session
	path := "/chats/" + url.PathEscape(chatID) + "/sessions/" + url.PathEscape(sessionID)
	if err := c.do(ctx, http.MethodPatch, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetChunk fetches a single parsed chunk by RAGFlow dataset/doc/chunk ids.
func (c *HTTPClient) GetChunk(ctx context.Context, datasetID, documentID, chunkID string) (*Chunk, error) {
	var out Chunk
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(documentID) + "/chunks/" + url.PathEscape(chunkID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
