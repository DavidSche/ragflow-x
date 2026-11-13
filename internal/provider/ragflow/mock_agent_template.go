package ragflow

import "context"

// SetAgentTemplate registers one canvas template so skeleton aggregation and
// import can be exercised without an upstream engine.
func (m *Mock) SetAgentTemplate(template AgentTemplate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.agentTemplates == nil {
		m.agentTemplates = map[string]AgentTemplate{}
	}
	m.agentTemplateOrder = append(m.agentTemplateOrder, template.ID)
	m.agentTemplates[template.ID] = template
}

// GetAgentTemplate implements the Client contract. It returns ErrSkeletonNotFound
// style errors so the service layer can map them to 404/503 consistently.
func (m *Mock) GetAgentTemplate(_ context.Context, id string) (AgentTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	template, ok := m.agentTemplates[id]
	if !ok {
		return AgentTemplate{}, protocolError("agent template not found: " + id)
	}
	return template, nil
}

func (m *Mock) ListAgentTemplates(context.Context) ([]AgentTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AgentTemplate, 0, len(m.agentTemplateOrder))
	for _, id := range m.agentTemplateOrder {
		if template, ok := m.agentTemplates[id]; ok {
			out = append(out, template)
		}
	}
	return out, nil
}
