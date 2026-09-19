package ragflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type mockSearchApp struct {
	id           string
	name         string
	description  string
	tenantID     string
	createdBy    string
	searchConfig SearchConfig
	createTime   int64
	updateTime   int64
}

func defaultMockSearchConfig() SearchConfig {
	return SearchConfig{
		KbIDs:                  []string{},
		DocIDs:                 []string{},
		SimilarityThreshold:    0.2,
		VectorSimilarityWeight: 0.3,
		UseKG:                  false,
		RerankID:               "",
		TopK:                   1024,
		Summary:                false,
		ChatID:                 "",
		LLMSetting:             map[string]interface{}{"temperature": 0.1},
	}
}

func fromMockSearchApp(m *mockSearchApp) SearchApp {
	cfg := m.searchConfig
	return SearchApp{
		ID: m.id, Name: m.name, Description: m.description, TenantID: m.tenantID, CreatedBy: m.createdBy,
		SearchConfig: &cfg, CreateTime: m.createTime, UpdateTime: m.updateTime,
	}
}

func fromMockSearchAppListItem(m *mockSearchApp) SearchAppListItem {
	return SearchAppListItem{
		ID: m.id, Name: m.name, Description: m.description, TenantID: m.tenantID, UpdateTime: m.updateTime,
	}
}

// ListSearchApps returns the mock Search Apps.
func (m *Mock) ListSearchApps(ctx context.Context, f ListSearchAppsFilter) ([]SearchAppListItem, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SearchAppListItem, 0, len(m.searchApps))
	for _, sa := range m.searchApps {
		if f.Keywords != "" && !strings.Contains(strings.ToLower(sa.name), strings.ToLower(f.Keywords)) {
			continue
		}
		out = append(out, fromMockSearchAppListItem(sa))
	}
	total := int64(len(out))
	// Paginate deterministically for tests.
	page, size := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 100
	}
	start := (page - 1) * size
	if start > len(out) {
		out = []SearchAppListItem{}
	} else {
		end := start + size
		if end > len(out) {
			end = len(out)
		}
		out = out[start:end]
	}
	return out, total, nil
}

// GetSearchApp returns a single mock Search App.
func (m *Mock) GetSearchApp(ctx context.Context, searchAppID string) (*SearchApp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sa, ok := m.searchApps[searchAppID]
	if !ok {
		return nil, fmt.Errorf("search app not found: %s", searchAppID)
	}
	v := fromMockSearchApp(sa)
	return &v, nil
}

// CreateSearchApp creates a mock Search App and returns its id.
func (m *Mock) CreateSearchApp(ctx context.Context, req CreateSearchAppRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	sa := &mockSearchApp{
		id: id.New(), name: req.Name, description: req.Description,
		searchConfig: defaultMockSearchConfig(), createTime: now, updateTime: now,
	}
	m.searchApps[sa.id] = sa
	return sa.id, nil
}

// UpdateSearchApp updates a mock Search App's name and search_config.
func (m *Mock) UpdateSearchApp(ctx context.Context, searchAppID string, req UpdateSearchAppRequest) (*SearchApp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sa, ok := m.searchApps[searchAppID]
	if !ok {
		return nil, fmt.Errorf("search app not found: %s", searchAppID)
	}
	if req.Name != "" {
		sa.name = req.Name
	}
	if req.SearchConfig != nil {
		merged := sa.searchConfig
		if req.SearchConfig.KbIDs != nil {
			merged.KbIDs = req.SearchConfig.KbIDs
		}
		if req.SearchConfig.DocIDs != nil {
			merged.DocIDs = req.SearchConfig.DocIDs
		}
		if req.SearchConfig.SimilarityThreshold != 0 {
			merged.SimilarityThreshold = req.SearchConfig.SimilarityThreshold
		}
		if req.SearchConfig.VectorSimilarityWeight != 0 {
			merged.VectorSimilarityWeight = req.SearchConfig.VectorSimilarityWeight
		}
		merged.UseKG = req.SearchConfig.UseKG
		if req.SearchConfig.RerankID != "" {
			merged.RerankID = req.SearchConfig.RerankID
		}
		if req.SearchConfig.TopK != 0 {
			merged.TopK = req.SearchConfig.TopK
		}
		merged.Summary = req.SearchConfig.Summary
		if req.SearchConfig.ChatID != "" {
			merged.ChatID = req.SearchConfig.ChatID
		}
		if req.SearchConfig.LLMSetting != nil {
			merged.LLMSetting = req.SearchConfig.LLMSetting
		}
		sa.searchConfig = merged
	}
	sa.updateTime = time.Now().Unix()
	v := fromMockSearchApp(sa)
	return &v, nil
}

// DeleteSearchApp deletes a mock Search App.
func (m *Mock) DeleteSearchApp(ctx context.Context, searchAppID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.searchApps[searchAppID]; !ok {
		return fmt.Errorf("search app not found: %s", searchAppID)
	}
	delete(m.searchApps, searchAppID)
	return nil
}

// SearchAppCompletion returns a deterministic mock answer.
func (m *Mock) SearchAppCompletion(ctx context.Context, searchAppID string, req SearchAppCompletionRequest) (*SearchAppCompletionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.searchApps[searchAppID]
	if !ok {
		return nil, fmt.Errorf("search app not found: %s", searchAppID)
	}
	if strings.TrimSpace(req.Question) == "" {
		return nil, fmt.Errorf("question is required")
	}
	return &SearchAppCompletionResult{
		Answer: "mock answer for: " + req.Question,
		Reference: []map[string]interface{}{
			{"id": "chunk-1", "content": "mock hit", "docnm_kwd": "doc-1.txt"},
			{"id": "chunk-2", "content": "mock hit 2", "docnm_kwd": "doc-1.txt"},
		},
	}, nil
}

// StreamSearchAppCompletion writes a JSON mock answer to w.
func (m *Mock) StreamSearchAppCompletion(ctx context.Context, searchAppID string, req SearchAppCompletionRequest, w io.Writer) error {
	res, err := m.SearchAppCompletion(ctx, searchAppID, req)
	if err != nil {
		return err
	}
	resp := map[string]interface{}{
		"code": 0, "message": "",
		"data": map[string]interface{}{"content": res.Answer, "reference": res.Reference},
	}
	payload, _ := json.Marshal(resp)
	if _, err := io.WriteString(w, "data:"+string(payload)+"\n\n"); err != nil {
		return err
	}
	_, err = io.WriteString(w, "data:{\"code\":0,\"message\":\"\",\"data\":true}\n\n")
	return err
}
