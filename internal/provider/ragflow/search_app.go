package ragflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SearchConfig is the nested JSON configuration of a RAGFlow Search App,
// mirrored from RAGFlow's Search model (api/db/db_models.py).
type SearchConfig struct {
	KbIDs                  []string                 `json:"kb_ids"`
	DocIDs                 []string                 `json:"doc_ids"`
	SimilarityThreshold    float64                  `json:"similarity_threshold"`
	VectorSimilarityWeight float64                  `json:"vector_similarity_weight"`
	UseKG                  bool                     `json:"use_kg"`
	RerankID               string                   `json:"rerank_id"`
	TopK                   int                      `json:"top_k"`
	Summary                bool                     `json:"summary"`
	ChatID                 string                   `json:"chat_id"`
	LLMSetting             map[string]interface{}   `json:"llm_setting,omitempty"`
	UseRerank              *bool                    `json:"use_rerank,omitempty"`
	RelatedSearch          *bool                    `json:"related_search,omitempty"`
	QueryMindmap           *bool                    `json:"query_mindmap,omitempty"`
	WebSearch              *bool                    `json:"web_search,omitempty"`
	ReferenceMetadata      *SearchReferenceMetadata `json:"reference_metadata,omitempty"`
	MetaDataFilter         map[string]interface{}   `json:"meta_data_filter,omitempty"`
}

// SearchReferenceMetadata controls whether chunk metadata is shown and which
// metadata fields to expose in search references.
type SearchReferenceMetadata struct {
	Include bool     `json:"include"`
	Fields  []string `json:"fields,omitempty"`
}

// SearchApp mirrors the RAGFlow Search App detail returned by
// GET /searches/<id>.
type SearchApp struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	TenantID     string        `json:"tenant_id,omitempty"`
	CreatedBy    string        `json:"created_by,omitempty"`
	SearchConfig *SearchConfig `json:"search_config,omitempty"`
	CreateTime   int64         `json:"create_time,omitempty"`
	UpdateTime   int64         `json:"update_time,omitempty"`
}

// SearchAppListItem is the compact Search App row returned by GET /searches.
type SearchAppListItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	TenantID    string `json:"tenant_id,omitempty"`
	UpdateTime  int64  `json:"update_time,omitempty"`
}

// SearchAppList is the paginated response body of GET /searches.
type SearchAppList struct {
	Items []SearchAppListItem `json:"search_apps"`
	Total int64               `json:"total"`
}

// ListSearchAppsFilter mirrors RAGFlow's list query parameters.
type ListSearchAppsFilter struct {
	Keywords string
	Page     int
	PageSize int
	OrderBy  string
	Desc     bool
}

// CreateSearchAppRequest mirrors RAGFlow's POST /searches body.
type CreateSearchAppRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// UpdateSearchAppRequest mirrors RAGFlow's PUT /searches/<id> body.
type UpdateSearchAppRequest struct {
	Name         string        `json:"name"`
	SearchConfig *SearchConfig `json:"search_config"`
}

// SearchAppCompletionRequest is an SSE completion request against a Search App.
type SearchAppCompletionRequest struct {
	Question string   `json:"question"`
	KbIDs    []string `json:"kb_ids,omitempty"`
}

// SearchAppCompletionResult is the accumulated outcome of the SSE completion,
// surfaced as a structured result for the retrieval-debugging UI.
type SearchAppCompletionResult struct {
	Answer    string                   `json:"answer,omitempty"`
	Reference []map[string]interface{} `json:"reference,omitempty"`
}

// ListSearchApps lists the authenticated tenant's Search Apps.
func (c *HTTPClient) ListSearchApps(ctx context.Context, f ListSearchAppsFilter) ([]SearchAppListItem, int64, error) {
	page, pageSize := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 100
	}
	if f.OrderBy == "" {
		f.OrderBy = "create_time"
	}
	desc := "true"
	if !f.Desc {
		desc = "false"
	}
	q := url.Values{}
	q.Set("page", fmt.Sprintf("%d", page))
	q.Set("page_size", fmt.Sprintf("%d", pageSize))
	q.Set("orderby", f.OrderBy)
	q.Set("desc", desc)
	if f.Keywords != "" {
		q.Set("keywords", f.Keywords)
	}
	var out SearchAppList
	path := "/searches?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, 0, err
	}
	return out.Items, out.Total, nil
}

// GetSearchApp returns a single Search App including its search_config.

func (c *HTTPClient) GetSearchApp(ctx context.Context, searchAppID string) (*SearchApp, error) {
	var out SearchApp
	path := "/searches/" + url.PathEscape(searchAppID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSearchApp creates a Search App and returns its id.
func (c *HTTPClient) CreateSearchApp(ctx context.Context, req CreateSearchAppRequest) (string, error) {
	body, _ := json.Marshal(req)
	var out struct {
		SearchID string `json:"search_id"`
	}
	if err := c.do(ctx, http.MethodPost, "/searches", bytes.NewReader(body), "application/json", &out); err != nil {
		return "", err
	}
	return out.SearchID, nil
}

// UpdateSearchApp updates a Search App's name and merged search_config.
func (c *HTTPClient) UpdateSearchApp(ctx context.Context, searchAppID string, req UpdateSearchAppRequest) (*SearchApp, error) {
	body, _ := json.Marshal(req)
	var out SearchApp
	path := "/searches/" + url.PathEscape(searchAppID)
	if err := c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSearchApp deletes a Search App.
func (c *HTTPClient) DeleteSearchApp(ctx context.Context, searchAppID string) error {
	path := "/searches/" + url.PathEscape(searchAppID)
	return c.do(ctx, http.MethodDelete, path, nil, "", nil)
}

// StreamSearchAppCompletion proxies the Search App SSE completion to w.
func (c *HTTPClient) StreamSearchAppCompletion(ctx context.Context, searchAppID string, req SearchAppCompletionRequest, w io.Writer) error {
	body, _ := json.Marshal(req)
	r, err := c.buildRequest(ctx, http.MethodPost,
		"/searches/"+url.PathEscape(searchAppID)+"/completion", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return wrapError("request ragflow search stream", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return NewErrorFromResponse(resp.StatusCode, raw)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// SearchAppCompletion consumes the SSE completion and returns the accumulated
// answer plus references for the retrieval-debugging UI.
func (c *HTTPClient) SearchAppCompletion(ctx context.Context, searchAppID string, req SearchAppCompletionRequest) (*SearchAppCompletionResult, error) {
	body, _ := json.Marshal(req)
	r, err := c.buildRequest(ctx, http.MethodPost,
		"/searches/"+url.PathEscape(searchAppID)+"/completion", bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, NewErrorFromResponse(resp.StatusCode, raw)
	}
	return parseSearchAppSSE(resp.Body)
}

type searchSSEData struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

func parseSearchAppSSE(r io.Reader) (*SearchAppCompletionResult, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var out SearchAppCompletionResult
	lastRef := []map[string]interface{}{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var evt searchSSEData
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			continue
		}
		switch v := evt.Data.(type) {
		case bool:
			// terminal marker
		case map[string]interface{}:
			if ref, ok := v["reference"]; ok {
				if arr, ok := ref.([]interface{}); ok {
					var refs []map[string]interface{}
					for _, it := range arr {
						if m, ok := it.(map[string]interface{}); ok {
							refs = append(refs, m)
						}
					}
					lastRef = refs
				}
			}
			out.Answer += coalesceString(v["content"]) + coalesceString(v["answer"])
		}
	}
	if len(lastRef) > 0 {
		out.Reference = lastRef
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &out, nil
}

func coalesceString(v interface{}) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
