package ragflow

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (m *Mock) SetCompilationStatus(datasetID string, status CompilationStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compilationStatuses[datasetID] = status
}

func (m *Mock) LastCompilationStatusDataset() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastCompilationStatusDataset
}

func (m *Mock) SetCompilationTemplates(
	source CompilationTemplateSource, templates []CompilationTemplate,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compilationTemplates[source] = append([]CompilationTemplate(nil), templates...)
}

func (m *Mock) GetCompilationStatus(ctx context.Context, datasetID string) (*CompilationStatus, error) {
	return m.GetCompilationStatusForKind(ctx, datasetID, "")
}

func (m *Mock) GetCompilationStatusForKind(
	_ context.Context, datasetID, kind string,
) (*CompilationStatus, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil, fmt.Errorf("dataset_id is required")
	}
	kind = strings.TrimSpace(kind)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastCompilationStatusDataset = datasetID
	if status, ok := m.compilationStatuses[datasetID]; ok {
		status.Kind = kind
		return &status, nil
	}
	return &CompilationStatus{Kind: kind, State: "idle", UpdatedAt: time.Now().UTC()}, nil
}

func (m *Mock) ListCompilationTemplates(
	_ context.Context, source CompilationTemplateSource,
) ([]CompilationTemplate, error) {
	switch source {
	case CompilationTemplateSourceBuiltins, CompilationTemplateSourceWikiPresets:
	default:
		return nil, fmt.Errorf("compilation template source must be builtins or wiki-presets")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	templates := m.compilationTemplates[source]
	return append([]CompilationTemplate(nil), templates...), nil
}
