package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type CompilationTemplateGroupTemplate struct {
	ID          string                 `json:"id,omitempty"`
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Kind        string                 `json:"kind,omitempty"`
	Config      map[string]interface{} `json:"config,omitempty"`
}

type CompilationTemplateGroup struct {
	ID          string                             `json:"id"`
	Name        string                             `json:"name"`
	Description string                             `json:"description,omitempty"`
	Scope       string                             `json:"scope,omitempty"`
	CreatedAt   string                             `json:"create_time,omitempty"`
	UpdatedAt   string                             `json:"update_time,omitempty"`
	Templates   []CompilationTemplateGroupTemplate `json:"templates"`
}

type CompilationTemplateGroupFilter struct {
	Keywords string
	Scope    string
	Page     int
	PageSize int
	Orderby  string
	Desc     bool
}

type CompilationTemplateGroupRequest struct {
	Name        string                             `json:"name,omitempty"`
	Description string                             `json:"description,omitempty"`
	Templates   []CompilationTemplateGroupTemplate `json:"templates,omitempty"`
}

func normalizeCompilationTemplateGroupFilter(filter CompilationTemplateGroupFilter) (CompilationTemplateGroupFilter, error) {
	if filter.Page == 0 {
		filter.Page = 1
	}
	if filter.PageSize == 0 {
		filter.PageSize = 30
	}
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > 100 {
		return filter, fmt.Errorf("compilation template group page must be between 1 and 100")
	}
	filter.Keywords = strings.TrimSpace(filter.Keywords)
	filter.Scope = strings.TrimSpace(filter.Scope)
	if filter.Scope != "" && filter.Scope != "file" && filter.Scope != "dataset" {
		return filter, fmt.Errorf("compilation template group scope must be file or dataset")
	}
	if filter.Orderby == "" {
		filter.Orderby = "create_time"
	}
	return filter, nil
}

func validateCompilationTemplateGroupRequest(request CompilationTemplateGroupRequest, create bool) error {
	if len([]rune(strings.TrimSpace(request.Name))) > 128 {
		return fmt.Errorf("compilation template group name is too long")
	}
	if len([]rune(request.Description)) > 1024 {
		return fmt.Errorf("compilation template group description is too long")
	}
	if request.Templates == nil && create {
		return fmt.Errorf("compilation template group must contain at least one template")
	}
	if request.Templates != nil && len(request.Templates) == 0 {
		return fmt.Errorf("compilation template group must contain at least one template")
	}
	seenNames := map[string]struct{}{}
	for _, template := range request.Templates {
		name := strings.TrimSpace(template.Name)
		kind := strings.TrimSpace(template.Kind)
		if name == "" || len([]rune(name)) > 128 || kind == "" {
			return fmt.Errorf("compilation template group templates require name and kind")
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("compilation template names must be unique within a group")
		}
		seenNames[name] = struct{}{}
	}
	return nil
}

func compilationTemplateGroupScope(request CompilationTemplateGroupRequest) string {
	if len(request.Templates) == 1 && strings.EqualFold(strings.TrimSpace(request.Templates[0].Kind), "wiki") {
		return "dataset"
	}
	return "file"
}

func (c *HTTPClient) ListCompilationTemplateGroups(
	ctx context.Context, filter CompilationTemplateGroupFilter,
) ([]CompilationTemplateGroup, int64, error) {
	filter, err := normalizeCompilationTemplateGroupFilter(filter)
	if err != nil {
		return nil, 0, err
	}
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", filter.Page))
	query.Set("page_size", fmt.Sprintf("%d", filter.PageSize))
	if filter.Keywords != "" {
		query.Set("keywords", filter.Keywords)
	}
	if filter.Scope != "" {
		query.Set("scope", filter.Scope)
	}
	query.Set("orderby", filter.Orderby)
	query.Set("desc", fmt.Sprintf("%t", filter.Desc))
	var page struct {
		Groups []CompilationTemplateGroup `json:"groups"`
		Total  int64                      `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, "/compilation-template-groups?"+query.Encode(), nil, "", &page); err != nil {
		return nil, 0, err
	}
	if page.Groups == nil {
		page.Groups = []CompilationTemplateGroup{}
	}
	return page.Groups, page.Total, nil
}

func (c *HTTPClient) GetCompilationTemplateGroup(ctx context.Context, groupID string) (*CompilationTemplateGroup, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil, fmt.Errorf("compilation_template_group_id is required")
	}
	var group CompilationTemplateGroup
	path := "/compilation-template-groups/" + url.PathEscape(groupID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &group); err != nil {
		return nil, err
	}
	if group.ID == "" {
		return nil, nil
	}
	return &group, nil
}

func (c *HTTPClient) SaveCompilationTemplateGroup(
	ctx context.Context, request CompilationTemplateGroupRequest,
) (*CompilationTemplateGroup, error) {
	if err := validateCompilationTemplateGroupRequest(request, true); err != nil {
		return nil, err
	}
	b, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var group CompilationTemplateGroup
	if err := c.do(ctx, http.MethodPost, "/compilation-template-groups", bytes.NewReader(b), "application/json", &group); err != nil {
		return nil, err
	}
	return &group, nil
}

func (c *HTTPClient) UpdateCompilationTemplateGroup(
	ctx context.Context, groupID string, request CompilationTemplateGroupRequest,
) (*CompilationTemplateGroup, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil, fmt.Errorf("compilation_template_group_id is required")
	}
	if err := validateCompilationTemplateGroupRequest(request, false); err != nil {
		return nil, err
	}
	b, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var group CompilationTemplateGroup
	path := "/compilation-template-groups/" + url.PathEscape(groupID)
	if err := c.do(ctx, http.MethodPut, path, bytes.NewReader(b), "application/json", &group); err != nil {
		return nil, err
	}
	if group.ID == "" {
		return nil, nil
	}
	return &group, nil
}

func (c *HTTPClient) DeleteCompilationTemplateGroup(ctx context.Context, groupID string) (bool, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return false, fmt.Errorf("compilation_template_group_id is required")
	}
	path := "/compilation-template-groups/" + url.PathEscape(groupID)
	var deleted bool
	if err := c.do(ctx, http.MethodDelete, path, nil, "", &deleted); err != nil {
		return false, err
	}
	return deleted, nil
}
