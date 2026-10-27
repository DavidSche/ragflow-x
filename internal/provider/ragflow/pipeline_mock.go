package ragflow

import (
	"context"
	"net/http"
)

func (m *Mock) SetPipelineTemplate(template PipelineTemplate, dsl map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pipelines[template.ID]; !exists {
		m.pipelineOrder = append(m.pipelineOrder, template.ID)
	}
	m.pipelines[template.ID] = PipelineTemplateDetail{DSL: dsl}
}

func (m *Mock) LastPipelineID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastPipelineID
}

func (m *Mock) ListPipelines(context.Context) ([]PipelineTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PipelineTemplate, 0, len(m.pipelineOrder))
	for _, pipelineID := range m.pipelineOrder {
		out = append(out, PipelineTemplate{ID: pipelineID})
	}
	return out, nil
}

func (m *Mock) GetPipeline(_ context.Context, pipelineID string) (*PipelineTemplateDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastPipelineID = pipelineID
	pipeline, ok := m.pipelines[pipelineID]
	if !ok {
		return nil, &Error{
			Type:       ErrorTypeBusiness,
			HTTPStatus: http.StatusNotFound,
			Code:       102,
			Message:    "pipeline not found",
		}
	}
	captured := pipeline
	return &captured, nil
}
