package service

import (
	"context"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// AgentMemoryOption is one pickable memory for the canvas Knowledge node in
// `retrieval_from=memory` mode. ID is the engine memory id the DSL will store.
type AgentMemoryOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AgentCompilationTemplateOption is one compilation template the dataflow
// Compiler node can point at. ID is the engine template id the DSL stores in
// `compilation_template_id`; the label is the engine display name so the panel
// never shows a raw uuid.
type AgentCompilationTemplateOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AgentCompilationTemplateGroupOption is one governed compilation template group
// (the `compilation_template_group_id` alternative to a single template).
type AgentCompilationTemplateGroupOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AgentAuthoringOptions is the canvas inspector's picker data surface: every
// resource a node parameter may reference (LLM / rerank model, dataset, memory)
// is listed here so the property panel offers a dropdown instead of a free-text
// id input (agents live in the engine, so the ids are engine ids — the UI must
// never ask an operator to type them by hand).
//
// Each section degrades independently: a section that fails carries its reason
// in `<section>_error` and stays empty, so one unavailable dependency (engine
// model list, link table, memory table) cannot blank the whole inspector — the
// panel falls back to manual input for that field alone.
type AgentAuthoringOptions struct {
	Models        *ModelOptions            `json:"models"`
	ModelsError   string                   `json:"models_error,omitempty"`
	Datasets      []SearchAppDatasetOption `json:"datasets"`
	DatasetsError string                   `json:"datasets_error,omitempty"`
	Memories      []AgentMemoryOption      `json:"memories"`
	MemoriesError string                   `json:"memories_error,omitempty"`

	// VisionModels are the engine's image2text/ocr models: the only values the
	// dataflow Parser node accepts for a model-based `parse_method` (or for
	// `vlm.llm_id` when vision enhancement is on).
	VisionModels      []ragflow.AddedModel `json:"vision_models"`
	VisionModelsError string               `json:"vision_models_error,omitempty"`

	// Compilation templates are what the dataflow Compiler node must name: the
	// engine resolves one of these ids at run time and rejects the node without
	// it, so a free-text id was an unbuildable field.
	CompilationTemplates           []AgentCompilationTemplateOption      `json:"compilation_templates"`
	CompilationTemplatesError      string                                `json:"compilation_templates_error,omitempty"`
	CompilationTemplateGroups      []AgentCompilationTemplateGroupOption `json:"compilation_template_groups"`
	CompilationTemplateGroupsError string                                `json:"compilation_template_groups_error,omitempty"`
}

// AgentAuthoringOptions collects the picker lists for one governance scope.
//
// Reuse is deliberate: the model catalog is the same tenant-wide engine answer
// the chat/search-app editors consume (GetModelOptions), and the dataset list is
// the same governance-scoped picker the search-app wizard uses
// (SearchAppDatasetOptions) — one source of truth per resource type, no
// agent-only variant that could drift.
func (s *Service) AgentAuthoringOptions(ctx context.Context, scope TenantScope) *AgentAuthoringOptions {
	out := &AgentAuthoringOptions{
		Datasets:                  []SearchAppDatasetOption{},
		Memories:                  []AgentMemoryOption{},
		VisionModels:              []ragflow.AddedModel{},
		CompilationTemplates:      []AgentCompilationTemplateOption{},
		CompilationTemplateGroups: []AgentCompilationTemplateGroupOption{},
	}

	if models, err := s.GetModelOptions(ctx); err != nil {
		out.ModelsError = authoringOptionErr(err)
	} else {
		out.Models = models
	}

	if datasets, err := s.SearchAppDatasetOptions(ctx, scope); err != nil {
		out.DatasetsError = authoringOptionErr(err)
	} else if datasets != nil {
		out.Datasets = datasets
	}

	if models, err := s.agentVisionModels(ctx); err != nil {
		out.VisionModelsError = authoringOptionErr(err)
	} else {
		out.VisionModels = models
	}

	if templates, err := s.AgentCompilationTemplates(ctx); err != nil {
		out.CompilationTemplatesError = authoringOptionErr(err)
	} else {
		out.CompilationTemplates = templates
	}

	if groups, err := s.AgentCompilationTemplateGroups(ctx); err != nil {
		out.CompilationTemplateGroupsError = authoringOptionErr(err)
	} else {
		out.CompilationTemplateGroups = groups
	}

	shadows, _, err := s.Store.ListMemoryShadows(ctx, scope.CurrentTenantID(), scope.ScopeAll(), repository.MemoryFilter{}, 1, 200)
	if err != nil {
		out.MemoriesError = authoringOptionErr(err)
	} else {
		for _, m := range shadows {
			out.Memories = append(out.Memories, AgentMemoryOption{ID: m.ID, Name: m.Name})
		}
	}

	return out
}

// agentVisionModels lists the engine models that can parse a picture: the
// dataflow Parser stores one of these ids in `parse_method` (or in `vlm.llm_id`),
// so the field must offer them instead of a text box.
func (s *Service) agentVisionModels(ctx context.Context) ([]ragflow.AddedModel, error) {
	if s == nil || s.RAGFlow == nil {
		return nil, httperr.New(502, 50254, "engine client is not configured")
	}
	seen := map[string]bool{}
	out := []ragflow.AddedModel{}
	for _, kind := range []string{"image2text", "ocr"} {
		models, err := s.RAGFlow.ListModels(ctx, kind)
		if err != nil {
			return nil, httperr.New(502, 50254, "list ragflow vision models failed")
		}
		for _, model := range models {
			key := model.Name + "@" + model.Instance + "@" + model.Provider
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, model)
		}
	}
	return out, nil
}

// AgentCompilationTemplates lists the built-in compilation templates a dataflow
// Compiler node may name. Exported because the validator reuses it to tell an
// unknown template from an unchecked catalog.
func (s *Service) AgentCompilationTemplates(ctx context.Context) ([]AgentCompilationTemplateOption, error) {
	if s == nil || s.RAGFlow == nil {
		return nil, httperr.New(502, 50255, "engine client is not configured")
	}
	templates, err := s.RAGFlow.ListCompilationTemplates(ctx, ragflow.CompilationTemplateSourceBuiltins)
	if err != nil {
		return nil, httperr.New(502, 50255, "list ragflow compilation templates failed")
	}
	out := make([]AgentCompilationTemplateOption, 0, len(templates))
	for _, template := range templates {
		name := strings.TrimSpace(template.DisplayName)
		if name == "" {
			name = template.Kind
		}
		if name == "" {
			name = template.ID
		}
		out = append(out, AgentCompilationTemplateOption{
			ID:          template.ID,
			Name:        name,
			Description: template.Description,
		})
	}
	return out, nil
}

// AgentCompilationTemplateGroups lists the governed template groups, the other
// id a Compiler node may carry instead of a single template. Every page is read:
// the engine caps a page at 100 and a truncated list would make a valid group
// look unknown.
func (s *Service) AgentCompilationTemplateGroups(ctx context.Context) ([]AgentCompilationTemplateGroupOption, error) {
	if s == nil || s.RAGFlow == nil {
		return nil, httperr.New(502, 50256, "engine client is not configured")
	}
	out := []AgentCompilationTemplateGroupOption{}
	for page := 1; page <= 20; page++ {
		groups, total, err := s.RAGFlow.ListCompilationTemplateGroups(ctx, ragflow.CompilationTemplateGroupFilter{Page: page, PageSize: 100})
		if err != nil {
			return nil, httperr.New(502, 50256, "list ragflow compilation template groups failed")
		}
		for _, group := range groups {
			out = append(out, AgentCompilationTemplateGroupOption{
				ID:          group.ID,
				Name:        group.Name,
				Description: group.Description,
			})
		}
		if len(groups) < 100 || int64(len(out)) >= total {
			break
		}
	}
	return out, nil
}

// authoringOptionErr renders a section failure for the UI hint without leaking
// internal error wrapping: httperr carries the operator-facing message, anything
// else is stringified as-is.
func authoringOptionErr(err error) string {
	if he, ok := err.(*httperr.Error); ok {
		return he.Message
	}
	return err.Error()
}
