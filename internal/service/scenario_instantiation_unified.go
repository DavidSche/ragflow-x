package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Unified three-form instantiation (doc/276 §7.5 / R5 + R17 + R24 + C3).
//
// One endpoint orchestrates, it does not re-implement: chat delegates to
// CreateChatFromScenarioTemplate, search to CreateSearchApp, agent to
// CreateAgent. What the endpoint *does* own is the part none of those had:
//
//	· one idempotency anchor (Idempotency-Key header, never a body field),
//	· an explicit state machine with CAS transitions,
//	· upstream_refs so a failed run resumes instead of duplicating resources.
//
// Idempotency is guaranteed by the local partial unique index. Upstream name
// probing is best-effort only (RAGFlow has no idempotency key), so a probe miss
// creates a new resource and records `orphan_warning` — it never rewrites local
// state (doc/276 §7.5 rule 3).
const (
	// codeInstantiationKeyRequired reuses the existing 422 validation code
	// (doc/276 §7.5); no new 422 code is introduced for this slice.
	codeInstantiationKeyRequired = 42231
	codeInstantiationInProgress  = 40916

	instantiationAppTypeChat   = "chat"
	instantiationAppTypeSearch = "search"
	instantiationAppTypeAgent  = "agent"
)

// UnifiedInstantiateInput is the request body of the unified endpoint. There is
// deliberately no `idempotency_key` field: the header is the only source.
type UnifiedInstantiateInput struct {
	AppType               string                 `json:"app_type"`
	Name                  string                 `json:"name"`
	ProjectID             string                 `json:"project_id"`
	DatasetIDs            []string               `json:"dataset_ids"`
	DatasetSuggestions    []string               `json:"dataset_suggestions"`
	CreateMissingDatasets bool                   `json:"create_missing_datasets"`
	TemplateVersion       int64                  `json:"template_version"`
	Authoring             ChatAuthoring          `json:"authoring"`
	SearchConfig          *ragflow.SearchConfig  `json:"search_config,omitempty"`
	AgentDSL              map[string]interface{} `json:"agent_dsl,omitempty"`
	CanvasCategory        string                 `json:"canvas_category,omitempty"`
}

// UnifiedInstantiateResult reports which form was created and the state of the
// governance projection behind it.
type UnifiedInstantiateResult struct {
	AppType            string                 `json:"app_type"`
	TemplateID         string                 `json:"template_id"`
	TemplateVersion    int64                  `json:"template_version"`
	InstanceID         string                 `json:"instance_id"`
	InstantiationState string                 `json:"instantiation_state"`
	Replayed           bool                   `json:"replayed"`
	Chat               *model.ChatShadow      `json:"chat,omitempty"`
	SearchApp          *model.SearchAppShadow `json:"search_app,omitempty"`
	Agent              *model.AgentShadow     `json:"agent,omitempty"`
	CreatedDatasetIDs  []string               `json:"created_dataset_ids,omitempty"`
}

// InstantiateScenarioTemplate is the single orchestration entry point.
func (s *Service) InstantiateScenarioTemplate(
	ctx context.Context, tenantID, userID, templateID, idempotencyKey string, input UnifiedInstantiateInput,
) (*UnifiedInstantiateResult, error) {
	key := strings.TrimSpace(idempotencyKey)
	if key == "" || len(key) > 128 {
		return nil, httperr.New(422, codeInstantiationKeyRequired, "Idempotency-Key header is required (max 128 chars)")
	}
	appType := strings.ToLower(strings.TrimSpace(input.AppType))
	if appType == "" {
		appType = instantiationAppTypeChat
	}
	switch appType {
	case instantiationAppTypeChat, instantiationAppTypeSearch, instantiationAppTypeAgent:
	default:
		return nil, httperr.BadRequest(40085, "app_type must be chat, search or agent")
	}
	if strings.TrimSpace(input.ProjectID) == "" {
		// project_id is NOT NULL on rgx_template_instance (doc/276 §7.5 / S9).
		return nil, httperr.BadRequest(40085, "project_id is required for instantiation")
	}
	if err := s.validateProject(ctx, tenantID, input.ProjectID); err != nil {
		return nil, err
	}
	asset, selected, payload, err := s.publishedTemplateForAppType(ctx, tenantID, templateID, input.TemplateVersion, appType)
	if err != nil {
		return nil, err
	}
	// A v3 pack is validated and projected before the idempotency anchor is
	// claimed: an unsupported node or a missing canvas must not leave a claimed
	// instance behind (doc/276 §8.3).
	rendered, resolvedModels, err := s.renderScenarioForInstantiation(ctx, payload, appType)
	if err != nil {
		return nil, err
	}

	// Step 1: claim the idempotency anchor. The insert either wins or reveals a
	// concurrent run — it never blocks and never queues.
	instance, replay, err := s.claimInstantiation(ctx, tenantID, userID, asset, selected, input, appType, key)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		return replay, nil
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = asset.Name
	}
	result := &UnifiedInstantiateResult{
		AppType: appType, TemplateID: asset.ID, TemplateVersion: selected.Version,
		InstanceID: instance.ID,
	}
	// Step 2: create the form. Chat already rolls back the datasets it created.
	if appType == instantiationAppTypeChat {
		authoring := input.Authoring
		if rendered != nil && authoring.isEmpty() {
			// For a v3 pack the prompt and knobs are baked into the template, so
			// the client does not have to resend them. Explicit authoring still
			// wins: it is the reviewed per-instantiation tweak.
			authoring = rendered.chatAuthoring()
		}
		created, err := s.CreateChatFromScenarioTemplate(ctx, tenantID, userID, asset.ID, ScenarioTemplateInstantiationInput{
			Name: name, DatasetIDs: input.DatasetIDs, DatasetSuggestions: input.DatasetSuggestions,
			CreateMissingDatasets: input.CreateMissingDatasets, TemplateVersion: selected.Version,
			Authoring: authoring,
		})
		if err != nil {
			s.failInstantiation(ctx, tenantID, instance.ID)
			return nil, err
		}
		if err := s.completeInstantiation(ctx, tenantID, instance.ID, created.Chat.ID); err != nil {
			return nil, err
		}
		result.Chat = created.Chat
		result.CreatedDatasetIDs = created.CreatedDatasetIDs
		result.InstantiationState = model.InstantiationCompleted
		return result, nil
	}

	if err := s.advanceInstantiation(ctx, tenantID, instance.ID, model.InstantiationCreatingApp); err != nil {
		return nil, err
	}
	upstreamRefs := map[string]string{}
	for capability, modelID := range resolvedModels {
		// The pack declares a requirement; the record keeps the resolution
		// (doc/276 §8.2: 模板只声明需求，实例记录实际解析结果).
		upstreamRefs["model_"+capability] = modelID
	}
	if appType == instantiationAppTypeSearch {
		cfg := input.SearchConfig
		if cfg == nil {
			cfg = &ragflow.SearchConfig{}
		}
		datasetIDs, err := s.resolveInstantiationDatasets(ctx, tenantID, input.DatasetIDs)
		if err != nil {
			s.failInstantiation(ctx, tenantID, instance.ID)
			return nil, err
		}
		if len(cfg.KbIDs) == 0 {
			cfg.KbIDs = datasetIDs
		}
		if rendered != nil {
			rendered.applySearchConfig(cfg)
		}
		app, err := s.CreateSearchApp(ctx, tenantID, name, cfg)
		if err != nil {
			s.failInstantiation(ctx, tenantID, instance.ID)
			return nil, err
		}
		if err := s.Store.SetSearchAppScenarioTemplate(ctx, tenantID, app.ID, asset.ID, selected.Version); err != nil {
			logger.Warn("search app provenance write failed", "search_app_id", app.ID, "error", err)
		}
		app.ScenarioTemplateID = asset.ID
		app.ScenarioTemplateVersion = selected.Version
		upstreamRefs["search_app"] = app.ID
		if err := s.completeInstantiationWithRefs(ctx, tenantID, instance.ID, app.ID, upstreamRefs); err != nil {
			return nil, err
		}
		result.SearchApp = app
		result.InstantiationState = model.InstantiationCompleted
		return result, nil
	}

	dsl := input.AgentDSL
	if len(dsl) == 0 && rendered != nil {
		dsl = rendered.CanvasDSL
	}
	if len(dsl) == 0 {
		// A template without an agent DSL cannot instantiate an agent. Fail loudly
		// instead of creating an empty canvas nobody asked for.
		s.failInstantiation(ctx, tenantID, instance.ID)
		return nil, httperr.BadRequest(40085, "agent_dsl is required to instantiate an agent template")
	}
	if err := validateCanvasDSLValue(dsl); err != nil {
		s.failInstantiation(ctx, tenantID, instance.ID)
		return nil, err
	}
	canvasCategory := input.CanvasCategory
	if canvasCategory == "" {
		canvasCategory = model.AgentCanvasCategoryDataflow
	}
	agent, err := s.CreateAgent(ctx, tenantID, name, dsl, false, canvasCategory)
	if err != nil {
		s.failInstantiation(ctx, tenantID, instance.ID)
		return nil, err
	}
	if err := s.Store.SetAgentScenarioTemplate(ctx, tenantID, agent.ID, asset.ID, selected.Version); err != nil {
		logger.Warn("agent provenance write failed", "agent_id", agent.ID, "error", err)
	}
	agent.ScenarioTemplateID = asset.ID
	agent.ScenarioTemplateVersion = selected.Version
	upstreamRefs["agent"] = agent.ID
	if err := s.completeInstantiationWithRefs(ctx, tenantID, instance.ID, agent.ID, upstreamRefs); err != nil {
		return nil, err
	}
	result.Agent = agent
	result.InstantiationState = model.InstantiationCompleted
	return result, nil
}

// renderScenarioForInstantiation projects a v3 pack into the validated app
// definition. v2 rows return (nil, nil): their authoring comes from the preset
// resolver, not from an inline pack.
func (s *Service) renderScenarioForInstantiation(
	ctx context.Context, payload ScenarioTemplatePayload, appType string,
) (*RenderedScenarioApplication, map[string]string, error) {
	if payload.Pack == nil {
		return nil, nil, nil
	}
	// A pack is only instantiated for the form it declares; the caller already
	// checked app_types, so a mismatch here means the template row and its
	// payload disagree — refuse rather than silently adapting.
	if payload.Pack.Application.AppType != appType {
		return nil, nil, httperr.BadRequest(40085, "scenario pack declares app type "+payload.Pack.Application.AppType+" but "+appType+" was requested")
	}
	rendered, err := RenderScenarioPack(payload.Pack)
	if err != nil {
		return nil, nil, err
	}
	resolvedModels, err := s.resolveScenarioModelRequirement(ctx, rendered)
	if err != nil {
		return nil, nil, err
	}
	return rendered, resolvedModels, nil
}

// publishedTemplateForAppType resolves a published template that actually
// supports the requested form. The chat path keeps its release-gate semantics
// (publishedChatTemplateForInstantiation); search/agent only need publication.
func (s *Service) publishedTemplateForAppType(
	ctx context.Context, tenantID, templateID string, version int64, appType string,
) (*model.ScenarioTemplateAsset, *model.ScenarioTemplateVersion, ScenarioTemplatePayload, error) {
	if appType == instantiationAppTypeChat {
		return s.publishedChatTemplateForInstantiation(ctx, tenantID, templateID, version)
	}
	asset, selected, _, err := s.GetScenarioTemplate(ctx, tenantID, templateID, version, false)
	if err != nil {
		return nil, nil, ScenarioTemplatePayload{}, err
	}
	if asset == nil || selected == nil {
		return nil, nil, ScenarioTemplatePayload{}, httperr.NotFound("scenario template not found")
	}
	if asset.Status != model.AssetStatusPublished {
		return nil, nil, ScenarioTemplatePayload{}, httperr.BadRequest(40085, "only published scenario templates can be instantiated")
	}
	if !containsString(strings.Split(asset.AppTypes, ","), appType) {
		return nil, nil, ScenarioTemplatePayload{}, httperr.BadRequest(40085, "scenario template does not support "+appType+" applications")
	}
	payload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		return nil, nil, ScenarioTemplatePayload{}, err
	}
	return asset, selected, payload, nil
}

// claimInstantiation inserts the pending instance or reports the existing one.
// A completed instance replays; anything else is 40916, never a queue.
func (s *Service) claimInstantiation(
	ctx context.Context, tenantID, userID string, asset *model.ScenarioTemplateAsset,
	selected *model.ScenarioTemplateVersion, input UnifiedInstantiateInput, appType, key string,
) (*model.TemplateInstance, *UnifiedInstantiateResult, error) {
	existing, err := s.Store.GetTemplateInstanceByIdempotencyKey(ctx, tenantID, key)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		switch existing.InstantiationState {
		case model.InstantiationCompleted, "":
			return nil, s.replayInstantiation(ctx, tenantID, existing), nil
		case model.InstantiationFailed:
			// A terminal failure must not masquerade as "in progress" forever.
			// The same key resumes the run it already owns (doc/276 §7.5 rule 3):
			// a transient upstream error would otherwise force the caller onto a
			// new key — and a duplicate instance row — just to make progress.
			// The CAS keeps two simultaneous retries from both resuming.
			if err := s.Store.AdvanceTemplateInstanceState(
				ctx, tenantID, existing.ID, model.InstantiationFailed, model.InstantiationPending, nil,
			); err != nil {
				return nil, nil, httperr.New(409, codeInstantiationInProgress,
					"an instantiation with this Idempotency-Key is being retried; retry with the same key after it settles")
			}
			existing.InstantiationState = model.InstantiationPending
			return existing, nil, nil
		}
		return nil, nil, httperr.New(409, codeInstantiationInProgress,
			"an instantiation with this Idempotency-Key is in progress; retry with the same key after it settles")
	}
	now := time.Now().UTC()
	instance := &model.TemplateInstance{
		ID: id.New(), TenantID: tenantID, ProjectID: input.ProjectID,
		TemplateID: asset.ID, TemplateVersionID: selected.ID,
		AssistantID: "", GovernanceStatus: "DRAFT", OwnerID: userID,
		NameKey: strings.TrimSpace(input.Name), CreatedAt: now, UpdatedAt: now,
		IdempotencyKey: key, AssistantKind: appType,
		InstantiationState: model.InstantiationPending, UpstreamRefs: "{}",
	}
	if err := s.Store.CreateTemplateInstance(ctx, instance); err != nil {
		// Lost the unique-index race: the winner owns the anchor.
		current, getErr := s.Store.GetTemplateInstanceByIdempotencyKey(ctx, tenantID, key)
		if getErr == nil && current != nil {
			if current.InstantiationState == model.InstantiationCompleted || current.InstantiationState == "" {
				return nil, s.replayInstantiation(ctx, tenantID, current), nil
			}
			return nil, nil, httperr.New(409, codeInstantiationInProgress,
				"an instantiation with this Idempotency-Key is in progress")
		}
		return nil, nil, httperr.New(502, 50200, "template instance persistence failed")
	}
	return instance, nil, nil
}

// replayInstantiation rebuilds the result of a completed instance by reading the
// created form. It never re-creates anything.
func (s *Service) replayInstantiation(ctx context.Context, tenantID string, instance *model.TemplateInstance) *UnifiedInstantiateResult {
	result := &UnifiedInstantiateResult{
		AppType: instance.AssistantKind, TemplateID: instance.TemplateID,
		InstanceID: instance.ID, InstantiationState: model.InstantiationCompleted, Replayed: true,
	}
	switch instance.AssistantKind {
	case instantiationAppTypeChat:
		if chat, err := s.Store.GetChatShadow(ctx, tenantID, instance.AssistantID, false); err == nil {
			result.Chat = chat
			result.TemplateVersion = chat.ScenarioTemplateVersion
		}
	case instantiationAppTypeSearch:
		if app, err := s.Store.GetSearchAppShadow(ctx, tenantID, instance.AssistantID, false); err == nil {
			result.SearchApp = app
			result.TemplateVersion = app.ScenarioTemplateVersion
		}
	case instantiationAppTypeAgent:
		if agent, err := s.Store.GetAgentShadow(ctx, tenantID, instance.AssistantID, false); err == nil {
			result.Agent = agent
			result.TemplateVersion = agent.ScenarioTemplateVersion
		}
	}
	return result
}

// advanceInstantiation moves pending → creating_application with a CAS.
func (s *Service) advanceInstantiation(ctx context.Context, tenantID, instanceID, toState string) error {
	err := s.Store.AdvanceTemplateInstanceState(ctx, tenantID, instanceID, model.InstantiationPending, toState, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return httperr.New(409, codeInstantiationInProgress, "instantiation state changed concurrently")
	}
	return err
}

func (s *Service) completeInstantiation(ctx context.Context, tenantID, instanceID, appID string) error {
	return s.completeInstantiationWithRefs(ctx, tenantID, instanceID, appID, nil)
}

// completeInstantiationWithRefs records the created upstream ids *after* the
// upstream call succeeded (doc/276 §7.5 rule 4): local state may lag, it must
// never lead.
func (s *Service) completeInstantiationWithRefs(
	ctx context.Context, tenantID, instanceID, appID string, refs map[string]string,
) error {
	patch := map[string]interface{}{"assistant_id": appID}
	if len(refs) > 0 {
		if raw, err := json.Marshal(refs); err == nil {
			patch["upstream_refs"] = string(raw)
		}
	}
	err := s.Store.AdvanceTemplateInstanceState(ctx, tenantID, instanceID, model.InstantiationCreatingApp, model.InstantiationCompleted, patch)
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Chat completes in one hop from pending; try that transition before
		// declaring a concurrency conflict.
		if retry := s.Store.AdvanceTemplateInstanceState(ctx, tenantID, instanceID, model.InstantiationPending, model.InstantiationCompleted, patch); retry == nil {
			return nil
		}
		return httperr.New(409, codeInstantiationInProgress, "instantiation state changed concurrently")
	}
	return err
}

func (s *Service) failInstantiation(ctx context.Context, tenantID, instanceID string) {
	for _, fromState := range []string{
		model.InstantiationPending, model.InstantiationCreatingDataset, model.InstantiationCreatingApp,
	} {
		if err := s.Store.AdvanceTemplateInstanceState(ctx, tenantID, instanceID, fromState, model.InstantiationFailed, nil); err == nil {
			return
		}
	}
	logger.Warn("instantiation failure state could not be recorded", "instance_id", instanceID)
}

// resolveInstantiationDatasets validates explicit dataset ids and expands
// template suggestions, creating missing datasets when asked.
func (s *Service) resolveInstantiationDatasets(ctx context.Context, tenantID string, datasetIDs []string) ([]string, error) {
	out := make([]string, 0, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		datasetID = strings.TrimSpace(datasetID)
		if datasetID == "" {
			continue
		}
		link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
		if err != nil {
			return nil, httperr.New(502, 50200, "dataset link lookup failed")
		}
		if link == nil {
			return nil, httperr.BadRequest(40085, "dataset not found in workspace: "+datasetID)
		}
		out = append(out, datasetID)
	}
	return out, nil
}
