package ragflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func (m *Mock) SetCompilationTemplateGroups(groups []CompilationTemplateGroup) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compilationTemplateGroups = map[string]CompilationTemplateGroup{}
	m.compilationTemplateOrder = make([]string, 0, len(groups))
	for _, group := range groups {
		if group.ID == "" {
			continue
		}
		m.compilationTemplateGroups[group.ID] = group
		m.compilationTemplateOrder = append(m.compilationTemplateOrder, group.ID)
	}
}

func (m *Mock) LastCompilationTemplateGroupListFilter() *CompilationTemplateGroupFilter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastTemplateGroupListFilter == nil {
		return nil
	}
	captured := *m.lastTemplateGroupListFilter
	return &captured
}

func (m *Mock) ListCompilationTemplateGroups(
	_ context.Context, filter CompilationTemplateGroupFilter,
) ([]CompilationTemplateGroup, int64, error) {
	filter, err := normalizeCompilationTemplateGroupFilter(filter)
	if err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTemplateGroupListFilter = &filter

	matches := make([]CompilationTemplateGroup, 0, len(m.compilationTemplateOrder))
	for _, groupID := range m.compilationTemplateOrder {
		group := m.compilationTemplateGroups[groupID]
		if filter.Keywords != "" && !strings.Contains(group.Name, filter.Keywords) {
			continue
		}
		if filter.Scope != "" && group.Scope != filter.Scope {
			continue
		}
		matches = append(matches, group)
	}
	total := int64(len(matches))
	start := (filter.Page - 1) * filter.PageSize
	if start >= len(matches) {
		return []CompilationTemplateGroup{}, total, nil
	}
	end := start + filter.PageSize
	if end > len(matches) {
		end = len(matches)
	}
	return matches[start:end], total, nil
}

func (m *Mock) GetCompilationTemplateGroup(_ context.Context, groupID string) (*CompilationTemplateGroup, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil, fmt.Errorf("compilation_template_group_id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	group, ok := m.compilationTemplateGroups[groupID]
	if !ok {
		return nil, nil
	}
	captured := group
	return &captured, nil
}

func (m *Mock) SaveCompilationTemplateGroup(
	_ context.Context, request CompilationTemplateGroupRequest,
) (*CompilationTemplateGroup, error) {
	if err := validateCompilationTemplateGroupRequest(request, true); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTemplateGroupSaved = &request
	group := CompilationTemplateGroup{
		ID: id.New(), Name: strings.TrimSpace(request.Name), Description: request.Description,
		Scope:     compilationTemplateGroupScope(request),
		Templates: append([]CompilationTemplateGroupTemplate(nil), request.Templates...),
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	group.CreatedAt = now
	group.UpdatedAt = now
	m.compilationTemplateGroups[group.ID] = group
	m.compilationTemplateOrder = append(m.compilationTemplateOrder, group.ID)
	captured := group
	return &captured, nil
}

func (m *Mock) UpdateCompilationTemplateGroup(
	_ context.Context, groupID string, request CompilationTemplateGroupRequest,
) (*CompilationTemplateGroup, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil, fmt.Errorf("compilation_template_group_id is required")
	}
	if err := validateCompilationTemplateGroupRequest(request, false); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTemplateGroupUpdatedID = groupID
	m.lastTemplateGroupUpdate = &request
	group, ok := m.compilationTemplateGroups[groupID]
	if !ok {
		return nil, nil
	}
	if request.Name != "" {
		group.Name = strings.TrimSpace(request.Name)
	}
	if request.Description != "" {
		group.Description = request.Description
	}
	if len(request.Templates) > 0 {
		group.Scope = compilationTemplateGroupScope(request)
		group.Templates = append([]CompilationTemplateGroupTemplate(nil), request.Templates...)
	}
	group.UpdatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
	m.compilationTemplateGroups[groupID] = group
	captured := group
	return &captured, nil
}

func (m *Mock) DeleteCompilationTemplateGroup(_ context.Context, groupID string) (bool, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return false, fmt.Errorf("compilation_template_group_id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTemplateGroupDeletedID = groupID
	if _, ok := m.compilationTemplateGroups[groupID]; !ok {
		return false, nil
	}
	delete(m.compilationTemplateGroups, groupID)
	order := make([]string, 0, len(m.compilationTemplateOrder)-1)
	for _, id := range m.compilationTemplateOrder {
		if id != groupID {
			order = append(order, id)
		}
	}
	m.compilationTemplateOrder = order
	return true, nil
}
