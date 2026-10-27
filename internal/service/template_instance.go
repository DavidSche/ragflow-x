package service

import (
	"context"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// TemplateInstanceView pairs the mutable instance governance projection with
// its assistant and current release (doc/107 §3.1.5 GET /template-instances).
type TemplateInstanceView struct {
	model.TemplateInstance
	Assistant      *model.Assistant        `json:"assistant,omitempty"`
	CurrentRelease *model.AssistantRelease `json:"current_release,omitempty"`
}

// TemplateInstanceListPage is the paged result of GET /template-instances.
type TemplateInstanceListPage struct {
	Items []TemplateInstanceView `json:"items"`
	Total int64                  `json:"total"`
}

// TemplateInstanceDryRunCheck is one read-only validation item.
type TemplateInstanceDryRunCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass | fail | warn
	Detail string `json:"detail,omitempty"`
}

// TemplateInstanceDryRunReport is the response of POST :id/dry-run. Dry runs
// never write and never call RAGFlow (doc/124 §2.3).
type TemplateInstanceDryRunReport struct {
	InstanceID string                        `json:"instance_id"`
	OK         bool                          `json:"ok"`
	Checks     []TemplateInstanceDryRunCheck `json:"checks"`
}

// TemplateInstanceHealthView aggregates release/lifecycle/health for
// GET :id/health (doc/107 §3.1.5).
type TemplateInstanceHealthView struct {
	Instance           *model.TemplateInstance `json:"instance"`
	Assistant          *model.Assistant        `json:"assistant,omitempty"`
	StableRelease      *model.AssistantRelease `json:"stable_release,omitempty"`
	CanaryRelease      *model.AssistantRelease `json:"canary_release,omitempty"`
	Health             *model.RuntimeHealth    `json:"health,omitempty"`
	ReconcileStatus    string                  `json:"reconcile_status"`
	CapabilityBindings int                     `json:"capability_binding_count"`
	DatasetBindings    int                     `json:"dataset_binding_count"`
}

// InstantiateTemplateInstanceInput drives POST /template-instances.
type InstantiateTemplateInstanceInput struct {
	TemplateID        string   `json:"template_id" binding:"required"`
	TemplateVersion   int64    `json:"template_version"`
	Name              string   `json:"name"`
	ProjectID         string   `json:"project_id" binding:"required"`
	OwnerID           string   `json:"owner_id"`
	DatasetIDs        []string `json:"dataset_ids"`
	CapabilityProfile string   `json:"capability_profile"`
	IdempotencyKey    string   `json:"idempotency_key" binding:"required"`
}

// TemplateInstanceDryRunInput drives POST :id/dry-run.
type TemplateInstanceDryRunInput struct {
	SampleQuestions []string `json:"sample_questions"`
	DatasetIDs      []string `json:"dataset_ids"`
}

// InstantiateTemplateInstance creates the instance governance projection and
// delegates to CreateAssistantRelease so the canonical object chain
// (TemplateInstance → AssistantVersion → AssistantRelease) is built by the
// existing saga (doc/124 §2.2). The capability profile v1 only supports the
// chat adapter: the template payload drives CreateChatWithConfig, the created
// chat becomes the release's capability target.
func (s *Service) InstantiateTemplateInstance(ctx context.Context, tenantID, userID string, input InstantiateTemplateInstanceInput) (*TemplateInstanceView, error) {
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	if input.TemplateID == "" || input.ProjectID == "" {
		return nil, httperr.New(422, 42231, "template_id and project_id are required")
	}
	if strings.TrimSpace(input.CapabilityProfile) == "" {
		input.CapabilityProfile = model.CapabilityKnowledgeChat
	}
	if input.CapabilityProfile != model.CapabilityKnowledgeChat {
		return nil, httperr.New(422, 42231, "capability_profile is not supported for instantiation yet")
	}
	if err := s.validateProject(ctx, tenantID, input.ProjectID); err != nil {
		return nil, err
	}

	// Resolve the published template payload (shared with the chat
	// instantiation path, doc/124 §2.2 note 1).
	_, selected, payload, err := s.publishedChatTemplateForInstantiation(ctx, tenantID, input.TemplateID, input.TemplateVersion)
	if err != nil {
		return nil, err
	}

	// Resolve dataset bindings: every id must exist as an X-side link.
	// Missing datasets are an error (no auto-create; dry-run covers the
	// suggestion workflow).
	datasetIDs := make([]string, 0, len(input.DatasetIDs))
	for _, datasetID := range input.DatasetIDs {
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
		datasetIDs = append(datasetIDs, datasetID)
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = input.TemplateID
	}
	ownerID := strings.TrimSpace(input.OwnerID)
	if ownerID == "" {
		ownerID = userID
	}

	// Idempotent instance lookup: the release idempotency key makes the whole
	// instantiation replayable through CreateAssistantRelease. Find an
	// existing instance for the same template/project/name and reuse it.
	instance, err := s.Store.GetTemplateInstanceByNameKey(ctx, tenantID, input.ProjectID, input.TemplateID, name)
	if err != nil {
		return nil, httperr.New(502, 50200, "template instance lookup failed")
	}
	if instance != nil && instance.AssistantID != "" {
		return s.enrichTemplateInstanceView(ctx, tenantID, instance)
	}
	if instance == nil {
		instance = &model.TemplateInstance{
			ID: id.New(), TenantID: tenantID, ProjectID: input.ProjectID,
			TemplateID: input.TemplateID, TemplateVersionID: selected.ID,
			AssistantID: "", GovernanceStatus: "DRAFT", OwnerID: ownerID,
			NameKey: name, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := s.Store.CreateTemplateInstance(ctx, instance); err != nil {
			return nil, httperr.New(502, 50200, "template instance persistence failed")
		}
	}

	// Create the provider-side chat, then the canonical release through the
	// existing saga. Chat failure aborts before any release row is written.
	authoring := mergeTemplateAuthoring(payload, ChatAuthoring{})
	chat, err := s.CreateChatWithConfig(ctx, tenantID, name, datasetIDs, authoring)
	if err != nil {
		return nil, err
	}

	desiredState := map[string]any{"prompt_config": authoring.PromptConfig}
	desiredStateJSON, err := canonicalJSON(desiredState)
	if err != nil {
		return nil, httperr.New(422, 42231, "desired state serialization failed")
	}

	if _, err := s.CreateAssistantRelease(ctx, tenantID, userID, CreateAssistantReleaseInput{
		ProjectID:            input.ProjectID,
		TemplateInstanceID:   instance.ID,
		ScenarioTemplateID:   input.TemplateID,
		TemplateVersionID:    selected.ID,
		Name:                 name,
		OwnerID:              ownerID,
		ScenarioPackSchema:   "1",
		ScenarioPackPayload:  selected.PayloadJSON,
		ExecutionContract:    "{}",
		PolicyVersion:        "template-policy-v1",
		PolicyJSON:           "{}",
		RuntimeProfile:       "{}",
		DesiredState:         desiredStateJSON,
		DesiredTargetType:    "chat",
		DesiredTargetID:      chat.ID,
		DesiredTargetVersion: "1",
		CapabilityBindings: []CapabilityBindingInput{{
			BindingID: "capability-main", Capability: model.CapabilityKnowledgeChat,
			Adapter: "ragflow_chat", TargetType: "chat", TargetID: chat.ID,
			TargetVersion: "1", RGXResourceID: chat.ID, OwnershipVerified: true,
		}},
		DatasetBindings: datasetBindingInputs(datasetIDs),
		IdempotencyKey:  input.IdempotencyKey,
	}); err != nil {
		return nil, err
	}
	fresh, err := s.getTemplateInstanceOrNotFound(ctx, tenantID, instance.ID)
	if err != nil {
		return nil, err
	}
	return s.enrichTemplateInstanceView(ctx, tenantID, fresh)
}

// DryRunTemplateInstance validates the instance bindings read-only: no rows
// are written and no upstream call is made (doc/124 §2.3).
func (s *Service) DryRunTemplateInstance(ctx context.Context, tenantID, instanceID string, input TemplateInstanceDryRunInput) (*TemplateInstanceDryRunReport, error) {
	view, err := s.GetTemplateInstance(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	checks := make([]TemplateInstanceDryRunCheck, 0, 8)
	addCheck := func(name, status, detail string) {
		checks = append(checks, TemplateInstanceDryRunCheck{Name: name, Status: status, Detail: detail})
	}
	ok := true

	if _, _, _, err := s.publishedChatTemplateForInstantiation(ctx, tenantID, view.TemplateID, 0); err != nil {
		addCheck("template_published", "fail", err.Error())
		ok = false
	} else {
		addCheck("template_published", "pass", "template is published with an active PASS gate")
	}

	datasetIDs := input.DatasetIDs
	for _, datasetID := range datasetIDs {
		link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
		if err != nil {
			addCheck("dataset_link:"+datasetID, "fail", "lookup failed")
			ok = false
			continue
		}
		if link == nil {
			addCheck("dataset_link:"+datasetID, "fail", "dataset not found in workspace")
			ok = false
			continue
		}
		addCheck("dataset_link:"+datasetID, "pass", link.RAGFlowDatasetID)
	}

	for _, question := range input.SampleQuestions {
		if strings.TrimSpace(question) == "" {
			addCheck("sample_question", "warn", "empty question ignored")
			continue
		}
		addCheck("sample_question", "pass", "accepted (not executed)")
	}

	if view.AssistantID == "" {
		addCheck("assistant_provisioned", "warn", "assistant not created yet; run instantiate first")
	} else {
		assistant, err := s.Store.GetAssistant(ctx, tenantID, view.AssistantID)
		if err != nil || assistant == nil {
			addCheck("assistant_provisioned", "fail", "assistant record missing")
			ok = false
		} else {
			addCheck("assistant_provisioned", "pass", assistant.LifecycleStatus)
		}
	}

	return &TemplateInstanceDryRunReport{InstanceID: instanceID, OK: ok, Checks: checks}, nil
}

// EvaluateTemplateInstance creates the release candidate/evidence chain for
// the instance's template (doc/124 §2.2 evaluate row).
func (s *Service) EvaluateTemplateInstance(ctx context.Context, tenantID, userID, instanceID string) (*ScenarioTemplateReleaseCandidateResult, error) {
	instance, err := s.getTemplateInstanceOrNotFound(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	return s.CreateScenarioTemplateReleaseCandidate(ctx, tenantID, userID, instance.TemplateID, ScenarioTemplateReleaseCandidateInput{})
}

// ReleaseTemplateInstance materializes the AssistantRelease with the supplied
// gate/approval evidence and drives Apply/Verify to VERIFIED. Traffic is never
// started here (doc/107 §3.1.5 release row).
func (s *Service) ReleaseTemplateInstance(ctx context.Context, tenantID, userID, instanceID string, input CreateAssistantReleaseInput) (*model.AssistantRelease, error) {
	instance, err := s.getTemplateInstanceOrNotFound(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if input.IdempotencyKey == "" {
		return nil, httperr.New(422, 42231, "Idempotency-Key is required")
	}
	if input.EvidenceBundleID == "" || input.GateDecisionID == "" || input.ApprovalID == "" {
		return nil, httperr.New(422, 42231, "evidence bundle, gate decision and approval are required")
	}
	input.ProjectID = instance.ProjectID
	input.ScenarioTemplateID = instance.TemplateID
	input.TemplateVersionID = instance.TemplateVersionID
	input.TemplateInstanceID = instance.ID
	if input.AssistantID == "" {
		input.AssistantID = instance.AssistantID
	}
	release, err := s.CreateAssistantRelease(ctx, tenantID, userID, input)
	if err != nil {
		return nil, err
	}
	if err := s.advanceReleaseToVerified(ctx, tenantID, userID, release); err != nil {
		return nil, err
	}
	return s.getAssistantReleaseOrNotFound(ctx, tenantID, release.ID)
}

// advanceReleaseToVerified walks an existing release through the state
// machine up to VERIFIED. The release must already carry valid evidence
// (validated inside TransitionAssistantRelease at the APPROVED hop).
func (s *Service) advanceReleaseToVerified(ctx context.Context, tenantID, userID string, release *model.AssistantRelease) error {
	if release.ReleaseState == model.ReleaseVerified {
		return nil
	}
	transitions := []struct{ from, to string }{
		{model.ReleaseDraft, model.ReleaseValidating},
		{model.ReleaseValidating, model.ReleaseSnapshotted},
		{model.ReleaseSnapshotted, model.ReleaseEvaluating},
		{model.ReleaseEvaluating, model.ReleaseGated},
		{model.ReleaseGated, model.ReleaseApproved},
	}
	for _, step := range transitions {
		current, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, release.ID)
		if err != nil {
			return err
		}
		if current.ReleaseState != step.from {
			continue
		}
		if _, err := s.TransitionAssistantRelease(ctx, tenantID, userID, release.ID, step.from, step.to, OperationInput{
			IdempotencyKey: "advance-" + release.ID + "-" + step.to,
			FencingToken:   current.FencingToken,
		}); err != nil {
			return err
		}
	}
	current, err := s.getAssistantReleaseOrNotFound(ctx, tenantID, release.ID)
	if err != nil {
		return err
	}
	if current.ReleaseState != model.ReleaseApproved {
		return nil
	}
	_, err = s.ApplyAssistantRelease(ctx, tenantID, release.ID, userID, OperationInput{
		IdempotencyKey: "apply-" + release.ID,
		FencingToken:   current.FencingToken,
	})
	return err
}

// TemplateInstanceHealth aggregates doc/107 §3.1.5 health fields.
func (s *Service) TemplateInstanceHealth(ctx context.Context, tenantID, instanceID string) (*TemplateInstanceHealthView, error) {
	view, err := s.GetTemplateInstance(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	healthView := &TemplateInstanceHealthView{Instance: &view.TemplateInstance, ReconcileStatus: model.ReconcileUnknown}
	if view.Assistant != nil {
		healthView.Assistant = view.Assistant
	}
	if view.AssistantID == "" {
		return healthView, nil
	}
	stable, canary := s.stableAndCanaryReleases(ctx, tenantID, view.AssistantID)
	healthView.StableRelease = stable
	healthView.CanaryRelease = canary
	if stable != nil {
		healthView.ReconcileStatus = stable.ReconcileStatus
		if health, err := s.Store.GetRuntimeHealth(ctx, tenantID, stable.ID); err == nil {
			healthView.Health = health
		}
		if capabilities, err := s.Store.ListCapabilityBindingVersions(ctx, tenantID, stable.ID); err == nil {
			healthView.CapabilityBindings = len(capabilities)
		}
		if datasets, err := s.Store.ListDatasetBindingVersions(ctx, tenantID, stable.ID); err == nil {
			healthView.DatasetBindings = len(datasets)
		}
	}
	return healthView, nil
}

// ListTemplateInstanceReleases lists releases under the instance's assistant.
func (s *Service) ListTemplateInstanceReleases(ctx context.Context, tenantID, instanceID string, page, pageSize int) (AssistantReleasePage, error) {
	instance, err := s.getTemplateInstanceOrNotFound(ctx, tenantID, instanceID)
	if err != nil {
		return AssistantReleasePage{}, err
	}
	return s.ListAssistantReleases(ctx, tenantID, instance.AssistantID, "", page, pageSize)
}

func (s *Service) getTemplateInstanceOrNotFound(ctx context.Context, tenantID, instanceID string) (*model.TemplateInstance, error) {
	if instanceID == "" {
		return nil, httperr.New(422, 42200, "template instance id is required")
	}
	instance, err := s.Store.GetTemplateInstance(ctx, tenantID, instanceID)
	if err != nil {
		return nil, httperr.New(502, 50200, "template instance lookup failed")
	}
	if instance == nil {
		return nil, httperr.NotFound("template instance not found")
	}
	return instance, nil
}

// GetTemplateInstance returns the enriched instance view.
func (s *Service) GetTemplateInstance(ctx context.Context, tenantID, instanceID string) (*TemplateInstanceView, error) {
	instance, err := s.getTemplateInstanceOrNotFound(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	return s.enrichTemplateInstanceView(ctx, tenantID, instance)
}

// ListTemplateInstances returns the paged, enriched instance list.
func (s *Service) ListTemplateInstances(ctx context.Context, tenantID, projectID, status string, page, pageSize int) (TemplateInstanceListPage, error) {
	items, total, err := s.Store.ListTemplateInstances(ctx, tenantID, projectID, status, page, pageSize)
	if err != nil {
		return TemplateInstanceListPage{}, httperr.New(502, 50200, "template instance list failed")
	}
	views := make([]TemplateInstanceView, 0, len(items))
	for index := range items {
		view, err := s.enrichTemplateInstanceView(ctx, tenantID, &items[index])
		if err != nil {
			return TemplateInstanceListPage{}, err
		}
		views = append(views, *view)
	}
	return TemplateInstanceListPage{Items: views, Total: total}, nil
}

func (s *Service) enrichTemplateInstanceView(ctx context.Context, tenantID string, instance *model.TemplateInstance) (*TemplateInstanceView, error) {
	view := &TemplateInstanceView{TemplateInstance: *instance}
	if instance.AssistantID == "" {
		return view, nil
	}
	if assistant, err := s.Store.GetAssistant(ctx, tenantID, instance.AssistantID); err == nil && assistant != nil {
		view.Assistant = assistant
		if assistant.CurrentAssistantReleaseID != "" {
			if release, err := s.Store.GetAssistantRelease(ctx, tenantID, assistant.CurrentAssistantReleaseID); err == nil && release != nil {
				view.CurrentRelease = release
			}
		}
	}
	return view, nil
}

func (s *Service) stableAndCanaryReleases(ctx context.Context, tenantID, assistantID string) (*model.AssistantRelease, *model.AssistantRelease) {
	var stableRelease, canaryRelease *model.AssistantRelease
	if assistant, err := s.Store.GetAssistant(ctx, tenantID, assistantID); err == nil && assistant != nil &&
		assistant.CurrentAssistantReleaseID != "" {
		if release, err := s.Store.GetAssistantRelease(ctx, tenantID, assistant.CurrentAssistantReleaseID); err == nil {
			stableRelease = release
		}
	}
	if canaries, _, err := s.Store.ListAssistantReleases(ctx, tenantID, assistantID, model.ReleaseCanaryActive, 1, 1); err == nil && len(canaries) > 0 {
		canaryRelease = &canaries[0]
	}
	return stableRelease, canaryRelease
}

func datasetBindingInputs(datasetIDs []string) []DatasetBindingInput {
	bindings := make([]DatasetBindingInput, 0, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		bindings = append(bindings, DatasetBindingInput{
			BindingID: "dataset-" + datasetID, DatasetID: datasetID, DatasetVersion: "1",
		})
	}
	return bindings
}

var _ = time.Now
