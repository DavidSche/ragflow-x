package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Skeleton sources (doc/276 §8.4). They are read-only mirrors of upstream
// catalogues: nothing here is stored, forked or versioned until the operator
// explicitly imports one.
const (
	ScenarioSkeletonSourcePipeline    = "ingestion_pipeline"
	ScenarioSkeletonSourceAgent       = "agent_template"
	ScenarioSkeletonSourceCompilation = "compilation_builtin"
	ScenarioSkeletonSourceWiki        = "wiki_preset"
)

// ScenarioSkeleton is one upstream catalogue row projected into the read-only
// mirror. `ID` is `"<source>:<upstreamId>"` so the import endpoint never has to
// guess which upstream catalogue a row came from.
type ScenarioSkeleton struct {
	ID             string                 `json:"id"`
	Source         string                 `json:"source"`
	UpstreamID     string                 `json:"upstream_id"`
	Title          string                 `json:"title"`
	Description    string                 `json:"description,omitempty"`
	Kind           string                 `json:"kind,omitempty"`
	CanvasCategory string                 `json:"canvas_category,omitempty"`
	AppTypes       []string               `json:"app_types"`
	HasDSL         bool                   `json:"has_dsl"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// ScenarioSkeletonSourceStatus reports per-catalogue health so a partial outage
// does not blank the whole mirror (doc/276 §8.4).
type ScenarioSkeletonSourceStatus struct {
	Source      string `json:"source"`
	Available   bool   `json:"available"`
	Count       int    `json:"count"`
	ErrorDetail string `json:"error,omitempty"`
}

type ScenarioSkeletonCatalog struct {
	Items      []ScenarioSkeleton             `json:"items"`
	Sources    []ScenarioSkeletonSourceStatus `json:"sources"`
	Degraded   bool                           `json:"degraded"`
	UpstreamOK bool                           `json:"upstream_ok"`
}

// ListScenarioSkeletons aggregates the four upstream catalogues. A failing
// catalogue degrades to a per-source status; only a total outage returns
// 50303, because the wizard must keep working with the nine builtin templates
// even when the engine is unreachable.
func (s *Service) ListScenarioSkeletons(ctx context.Context) (*ScenarioSkeletonCatalog, error) {
	catalog := &ScenarioSkeletonCatalog{Items: []ScenarioSkeleton{}}
	type collector func(context.Context) ([]ScenarioSkeleton, error)
	collectors := []struct {
		source string
		fn     collector
	}{
		{ScenarioSkeletonSourcePipeline, s.listPipelineSkeletons},
		{ScenarioSkeletonSourceAgent, s.listAgentSkeletons},
		{ScenarioSkeletonSourceCompilation, s.listCompilationSkeletons},
		{ScenarioSkeletonSourceWiki, s.listWikiSkeletons},
	}
	available := 0
	for _, entry := range collectors {
		items, err := entry.fn(ctx)
		status := ScenarioSkeletonSourceStatus{Source: entry.source, Available: err == nil, Count: len(items)}
		if err != nil {
			status.ErrorDetail = err.Error()
			logger.Warn("scenario skeleton source unavailable", "source", entry.source, "error", err)
		} else {
			available++
			catalog.Items = append(catalog.Items, items...)
		}
		catalog.Sources = append(catalog.Sources, status)
	}
	if available == 0 {
		return nil, httperr.New(503, 50303, "upstream_skeleton_unavailable")
	}
	catalog.UpstreamOK = available == len(collectors)
	catalog.Degraded = !catalog.UpstreamOK
	sort.SliceStable(catalog.Items, func(i, j int) bool {
		if catalog.Items[i].Source != catalog.Items[j].Source {
			return catalog.Items[i].Source < catalog.Items[j].Source
		}
		return catalog.Items[i].ID < catalog.Items[j].ID
	})
	return catalog, nil
}

func (s *Service) listPipelineSkeletons(ctx context.Context) ([]ScenarioSkeleton, error) {
	pipelines, err := s.RAGFlow.ListPipelines(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ScenarioSkeleton, 0, len(pipelines))
	for _, pipeline := range pipelines {
		if strings.TrimSpace(pipeline.ID) == "" {
			continue
		}
		items = append(items, ScenarioSkeleton{
			ID:          ScenarioSkeletonSourcePipeline + ":" + pipeline.ID,
			Source:      ScenarioSkeletonSourcePipeline,
			UpstreamID:  pipeline.ID,
			Title:       pipeline.Title,
			Description: pipeline.Description,
			// A builtin ingestion pipeline is usable by every app type: it
			// describes parsing, not answering.
			AppTypes: []string{"chat", "search", "agent"},
			Metadata: map[string]interface{}{"filename": pipeline.Filename},
		})
	}
	return items, nil
}

func (s *Service) listAgentSkeletons(ctx context.Context) ([]ScenarioSkeleton, error) {
	templates, err := s.RAGFlow.ListAgentTemplates(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ScenarioSkeleton, 0, len(templates))
	for _, template := range templates {
		// Upstream occasionally lists an empty row (no id, no dsl). It is not a
		// skeleton: importing it would 40085 on the id, so drop it here instead of
		// offering a button that cannot work.
		if strings.TrimSpace(template.ID) == "" {
			continue
		}
		canvasType := template.PickCanvasType()
		items = append(items, ScenarioSkeleton{
			ID: ScenarioSkeletonSourceAgent + ":" + template.ID, Source: ScenarioSkeletonSourceAgent,
			UpstreamID: template.ID, Title: template.AgentTemplateTitle("zh-CN"),
			Description: template.AgentTemplateDescription("zh-CN"), CanvasCategory: template.CanvasCategory,
			// An agent template always yields an agent: `canvas_types` carries the
			// upstream catalogue bucket ("Recommended", "Customer Support"), which
			// is not an app type. Keeping it in metadata preserves the information
			// without corrupting the app-type vocabulary the wizard filters on.
			AppTypes: []string{"agent"},
			HasDSL:   len(template.DSL) > 0,
			Metadata: map[string]interface{}{"canvas_type": canvasType},
		})
	}
	return items, nil
}

func (s *Service) listCompilationSkeletons(ctx context.Context) ([]ScenarioSkeleton, error) {
	templates, err := s.RAGFlow.ListCompilationTemplates(ctx, ragflow.CompilationTemplateSourceBuiltins)
	if err != nil {
		return nil, err
	}
	return compilationSkeletons(templates, ScenarioSkeletonSourceCompilation), nil
}

func (s *Service) listWikiSkeletons(ctx context.Context) ([]ScenarioSkeleton, error) {
	templates, err := s.RAGFlow.ListCompilationTemplates(ctx, ragflow.CompilationTemplateSourceWikiPresets)
	if err != nil {
		return nil, err
	}
	return compilationSkeletons(templates, ScenarioSkeletonSourceWiki), nil
}

func compilationSkeletons(templates []ragflow.CompilationTemplate, source string) []ScenarioSkeleton {
	items := make([]ScenarioSkeleton, 0, len(templates))
	for _, template := range templates {
		if strings.TrimSpace(template.ID) == "" {
			continue
		}
		items = append(items, ScenarioSkeleton{
			ID: source + ":" + template.ID, Source: source, UpstreamID: template.ID,
			Title: template.DisplayName, Description: template.Description, Kind: template.Kind,
			AppTypes: []string{"chat", "search"},
			Metadata: map[string]interface{}{"compilation_template_id": template.ID},
		})
	}
	return items
}

// ImportScenarioSkeleton materialises one upstream skeleton into a workspace
// DRAFT (doc/276 §8.4). The draft is deliberately incomplete-friendly: the
// author still has to supply business copy, so publish validation is stricter
// than create validation.
func (s *Service) ImportScenarioSkeleton(ctx context.Context, tenantID, userID, skeletonID string) (*model.ScenarioTemplateAsset, error) {
	source, upstreamID, ok := splitSkeletonID(skeletonID)
	if !ok {
		return nil, httperr.BadRequest(40085, "skeleton id must be <source>:<upstream_id>")
	}
	payload, key, name, appTypes, err := s.skeletonPayload(ctx, source, upstreamID)
	if err != nil {
		return nil, err
	}
	key = s.uniqueTemplateKey(ctx, tenantID, key)
	asset, err := s.CreateScenarioTemplate(ctx, tenantID, userID, ScenarioTemplateInput{
		Key: key, Name: name, AppTypes: appTypes, Payload: payload,
		Status: model.AssetStatusDraft, ChangeNote: "imported from " + source + " skeleton " + upstreamID,
	})
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetScenarioTemplateSource(ctx, tenantID, asset.ID, model.TemplateSourceImport); err != nil {
		return nil, err
	}
	asset.Source = model.TemplateSourceImport
	return asset, nil
}

func splitSkeletonID(skeletonID string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(skeletonID), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	switch parts[0] {
	case ScenarioSkeletonSourcePipeline, ScenarioSkeletonSourceAgent,
		ScenarioSkeletonSourceCompilation, ScenarioSkeletonSourceWiki:
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

// skeletonPayload builds the v3 pack for one skeleton. Fields that only the
// author can decide (business copy, questions) stay empty on purpose: filler
// text would look authored and would silently ship to users.
func (s *Service) skeletonPayload(ctx context.Context, source, upstreamID string) (ScenarioTemplatePayload, string, string, []string, error) {
	switch source {
	case ScenarioSkeletonSourcePipeline:
		pipeline, err := s.RAGFlow.GetPipeline(ctx, upstreamID)
		if err != nil {
			return ScenarioTemplatePayload{}, "", "", nil, upstreamUnavailable(source, err)
		}
		pack := newSkeletonPack("chat")
		pack.Knowledge = &ScenarioTemplateKnowledge{
			Aggregate: scenarioAggregateAllRequired,
			Datasets: []ScenarioKnowledgeDataset{{
				Role: scenarioDatasetRolePrimary,
				ParseIntent: &ScenarioParseIntent{
					Mode:     "pipeline",
					Pipeline: &ScenarioPipelineIntent{FromUpstreamTemplateID: upstreamID},
				},
			}},
		}
		if pipeline != nil && len(pipeline.DSL) > 0 {
			pack.RuntimeExtension.RAGFlow = &ScenarioPackRAGFlowExtension{
				PipelineBinding: map[string]interface{}{"template_id": upstreamID},
			}
		}
		return pack.ProjectPayload(), "skeleton-" + upstreamID, "Skeleton " + upstreamID, []string{"chat"}, nil
	case ScenarioSkeletonSourceAgent:
		template, err := s.RAGFlow.GetAgentTemplate(ctx, upstreamID)
		if err != nil {
			return ScenarioTemplatePayload{}, "", "", nil, upstreamUnavailable(source, err)
		}
		// The engine's canvas type ("task_oriented", "deep_research", ...) is a
		// catalogue bucket, not a platform app type: an imported agent skeleton is
		// always an `agent` template. The bucket survives only as upstream metadata
		// on the skeleton list, where it cannot reach the app_type validator.
		pack := newSkeletonPack("agent")
		pack.RuntimeExtension.RAGFlow = &ScenarioPackRAGFlowExtension{CanvasDSL: template.DSL}
		title := template.AgentTemplateTitle("zh-CN")
		if title == "" {
			title = "Agent skeleton " + upstreamID
		}
		return pack.ProjectPayload(), "skeleton-" + upstreamID, title, []string{"agent"}, nil
	case ScenarioSkeletonSourceCompilation, ScenarioSkeletonSourceWiki:
		upstreamSource := ragflow.CompilationTemplateSourceBuiltins
		if source == ScenarioSkeletonSourceWiki {
			upstreamSource = ragflow.CompilationTemplateSourceWikiPresets
		}
		templates, err := s.RAGFlow.ListCompilationTemplates(ctx, upstreamSource)
		if err != nil {
			return ScenarioTemplatePayload{}, "", "", nil, upstreamUnavailable(source, err)
		}
		for _, template := range templates {
			if template.ID != upstreamID {
				continue
			}
			pack := newSkeletonPack("chat")
			pack.Knowledge = &ScenarioTemplateKnowledge{
				Aggregate: scenarioAggregateAllRequired,
				Datasets: []ScenarioKnowledgeDataset{{
					Role:     scenarioDatasetRolePrimary,
					Metadata: map[string]interface{}{"compilation_template_id": upstreamID},
				}},
			}
			name := template.DisplayName
			if name == "" {
				name = "Skeleton " + upstreamID
			}
			return pack.ProjectPayload(), "skeleton-" + upstreamID, name, []string{"chat"}, nil
		}
		return ScenarioTemplatePayload{}, "", "", nil, httperr.NotFound("compilation skeleton not found")
	default:
		return ScenarioTemplatePayload{}, "", "", nil, httperr.BadRequest(40085, "unsupported skeleton source")
	}
}

func upstreamUnavailable(source string, cause error) error {
	logger.Warn("scenario skeleton import failed upstream", "source", source, "error", cause)
	return httperr.New(503, 50303, "upstream_skeleton_unavailable: "+cause.Error())
}

// newSkeletonPack seeds the parts of a pack that are structural rather than
// authored. Prompt copy stays empty: it must come from a human (doc/276 §6.2
// contract 1).
func newSkeletonPack(appType string) *ScenarioPack {
	return &ScenarioPack{
		Schema: ScenarioPackSchemaV3,
		Application: ScenarioPackApplication{
			AppType:       appType,
			Prompt:        ScenarioPackPrompt{},
			ProfileSource: "skeleton",
		},
	}
}

// uniqueTemplateKey keeps repeated skeleton imports from colliding on the
// asset key, which is unique per tenant.
func (s *Service) uniqueTemplateKey(ctx context.Context, tenantID, base string) string {
	base = slug(base)
	if base == "" {
		base = "skeleton"
	}
	if existing, err := s.Store.GetScenarioTemplateByKey(ctx, tenantID, base); err == nil && existing != nil {
		for suffix := 2; suffix < 100; suffix++ {
			candidate := fmt.Sprintf("%s-%d", base, suffix)
			if found, err := s.Store.GetScenarioTemplateByKey(ctx, tenantID, candidate); err == nil && found == nil {
				return candidate
			}
		}
	}
	return base
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	builder := strings.Builder{}
	lastDash := false
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			lastDash = false
		case !lastDash:
			builder.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
