package service

import (
	"context"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Capability check (doc/276 §6.5 + §7.3 / R3, R15, R21).
//
// The check answers one question per dataset: *can this knowledge base actually
// serve the template's declared requirements, and if not, can the gap be closed
// without losing data?* It never mutates anything and never writes back to
// RAGFlow; repairs are proposed as data and only applied through the explicit
// parse-profile endpoint.
//
// Evidence model (four states, doc/276 §6.5):
//
//	supported   the capability exists in this engine build
//	configured  the dataset config asks for it
//	ready       the configuration is internally consistent (parser_config keys,
//	            meta_fields present, pushdown synced)
//	operational observed working at runtime (graph nodes, compiled wiki, parsed
//	            documents)
//
// `ready` may be unknown (nil): RAGFlow exposes no per-dataset readiness
// endpoint for some capabilities. Unknown is *not* "satisfied" — it caps the
// verdict at probe_limited, which is the honest answer and the reason the
// verdict order lists probe_limited above repairable.
const (
	CapabilityStatusSupported   = "supported"
	CapabilityStatusConfigured  = "configured"
	CapabilityStatusReady       = "ready"
	CapabilityStatusOperational = "operational"

	CapabilityVerdictSatisfied    = "satisfied"
	CapabilityVerdictRepairable   = "repairable"
	CapabilityVerdictProbeLimited = "probe_limited"
	CapabilityVerdictBlocked      = "blocked"

	CapabilityProbeSuccess     = "success"
	CapabilityProbePartial     = "partial"
	CapabilityProbePendingSync = "pending_sync"
	CapabilityProbeFailed      = "probe_failed"

	CapabilityCheckVersion = "capability-set/v1"
)

// CapabilityEvidence is one capability observation for one dataset.
type CapabilityEvidence struct {
	Capability string `json:"capability"`
	// Status is the highest state proven; empty means "not supported".
	Status string `json:"status"`
	// Source names the evidence origin so the UI never shows a bare badge
	// without provenance (doc/276 §6.5 rule 4).
	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
}

// DatasetCapabilityProfile is the read-only description of one dataset.
type DatasetCapabilityProfile struct {
	DatasetID       string               `json:"dataset_id"`
	Name            string               `json:"name"`
	ChunkMethod     string               `json:"chunk_method"`
	EmbeddingModel  string               `json:"embedding_model"`
	DocumentCount   int                  `json:"document_count"`
	ParsedCount     int                  `json:"parsed_count"`
	PushdownEnabled bool                 `json:"pushdown_enabled"`
	Capabilities    []CapabilityEvidence `json:"capabilities"`
	SharedUsage     DatasetSharedUsage   `json:"shared_usage"`
}

// DatasetSharedUsage is the write-impact pre-check (doc/276 §7.3 step 3).
type DatasetSharedUsage struct {
	Count      int      `json:"count"`
	ChatIDs    []string `json:"chat_ids,omitempty"`
	SearchIDs  []string `json:"search_ids,omitempty"`
	ReleaseIDs []string `json:"release_ids,omitempty"`
}

// CapabilityRepair is a proposed, not yet applied, fix.
type CapabilityRepair struct {
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	Severity string `json:"severity"`
}

// RequirementVerdict is the per-requirement outcome for one dataset.
type RequirementVerdict struct {
	Capability string `json:"capability"`
	Scope      string `json:"scope"`
	MinLevel   string `json:"min_level"`
	Achieved   string `json:"achieved"`
	Satisfied  bool   `json:"satisfied"`
	Reason     string `json:"reason,omitempty"`
}

// DatasetCapabilityVerdict is the per-dataset verdict.
type DatasetCapabilityVerdict struct {
	Role         string               `json:"role"`
	DatasetID    string               `json:"dataset_id"`
	Verdict      string               `json:"verdict"`
	Aggregate    string               `json:"aggregate,omitempty"`
	ProbeStatus  string               `json:"probe_status"`
	Requirements []RequirementVerdict `json:"requirements"`
	Capabilities []CapabilityEvidence `json:"capabilities"`
	Repairs      []CapabilityRepair   `json:"repairs"`
	SharedUsage  DatasetSharedUsage   `json:"shared_usage"`
}

// CapabilityCheckView is the aggregate response of the check endpoint.
type CapabilityCheckView struct {
	TemplateID           string                     `json:"template_id"`
	TemplateVersion      int64                      `json:"template_version"`
	AppType              string                     `json:"app_type"`
	Verdict              string                     `json:"verdict"`
	Aggregate            string                     `json:"aggregate,omitempty"`
	ProbeStatus          string                     `json:"probe_status"`
	CapabilitySetVersion string                     `json:"capability_set_version"`
	Datasets             []DatasetCapabilityVerdict `json:"datasets"`
}

// CapabilityCheckInput is the request body of the capability-check endpoint.
type CapabilityCheckInput struct {
	AppType  string                   `json:"app_type"`
	Datasets []CapabilityCheckDataset `json:"datasets"`
}

// CapabilityCheckDataset binds a knowledge role to a concrete dataset.
type CapabilityCheckDataset struct {
	Role      string `json:"role"`
	DatasetID string `json:"dataset_id"`
}

// knowledgeRoleDataset is the resolved role/dataset pair.
type knowledgeRoleDataset struct {
	Role      string
	DatasetID string
}

// CheckScenarioTemplateCapabilities resolves the template knowledge contract,
// probes every supplied dataset and aggregates the result.
func (s *Service) CheckScenarioTemplateCapabilities(
	ctx context.Context, tenantID string, templateID string, input CapabilityCheckInput,
) (*CapabilityCheckView, error) {
	asset, selected, _, err := s.GetScenarioTemplate(ctx, tenantID, templateID, 0, false)
	if err != nil {
		return nil, err
	}
	payload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		return nil, httperr.BadRequest(40105, "scenario template payload is invalid")
	}
	knowledge := payload.Knowledge
	if knowledge == nil {
		// A v2 template without an explicit contract still has an implicit one:
		// every supplied dataset is a primary dataset and only vector retrieval is
		// required. Refusing here would make old templates un-checkable.
		knowledge = &ScenarioTemplateKnowledge{
			Aggregate:            scenarioAggregateAllRequired,
			RequiredCapabilities: []ScenarioCapabilityRequirement{{Capability: "vector", Scope: scenarioRequirementScopeAll, MinLevel: CapabilityStatusOperational}},
		}
	}
	roles := normalizedRoleDatasets(input.Datasets)
	if len(roles) == 0 {
		return nil, httperr.BadRequest(40105, "datasets[] with role and dataset_id is required")
	}
	view := &CapabilityCheckView{
		TemplateID: asset.ID, TemplateVersion: asset.LatestVersion,
		AppType: strings.TrimSpace(input.AppType), CapabilitySetVersion: CapabilityCheckVersion,
	}
	anyBlocked := false
	anyLimited := false
	anyRepairable := false
	for _, role := range roles {
		profile, probeStatus, err := s.DescribeDataset(ctx, tenantID, role.DatasetID)
		if err != nil {
			return nil, err
		}
		verdict := EvaluateRequirements(knowledge, role.Role, *profile)
		verdict.ProbeStatus = probeStatus
		view.Datasets = append(view.Datasets, verdict)
		switch verdict.Verdict {
		case CapabilityVerdictBlocked:
			anyBlocked = true
		case CapabilityVerdictProbeLimited:
			anyLimited = true
		case CapabilityVerdictRepairable:
			anyRepairable = true
		}
	}
	allSupported := len(view.Datasets) > 0
	for _, item := range view.Datasets {
		if item.Verdict != CapabilityVerdictSatisfied {
			allSupported = false
		}
	}
	view.Aggregate = AggregateVerdicts(knowledge, view.Datasets)
	view.Verdict = aggregateVerdict(anyBlocked, anyLimited, anyRepairable, allSupported)
	view.ProbeStatus = aggregateProbeStatus(view.Datasets)
	return view, nil
}

// normalizedRoleDatasets drops empty entries and de-duplicates by dataset id,
// keeping the first role that referenced it.
func normalizedRoleDatasets(items []CapabilityCheckDataset) []knowledgeRoleDataset {
	seen := map[string]bool{}
	out := make([]knowledgeRoleDataset, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.DatasetID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = scenarioDatasetRolePrimary
		}
		out = append(out, knowledgeRoleDataset{Role: role, DatasetID: id})
	}
	return out
}

// aggregateVerdicts applies the fixed evaluation order from doc/276 §6.5:
// blocked > probe_limited > repairable > satisfied.
func aggregateVerdict(blocked, limited, repairable, allSatisfied bool) string {
	switch {
	case blocked:
		return CapabilityVerdictBlocked
	case limited:
		return CapabilityVerdictProbeLimited
	case repairable:
		return CapabilityVerdictRepairable
	case allSatisfied:
		return CapabilityVerdictSatisfied
	default:
		return CapabilityVerdictProbeLimited
	}
}

func aggregateProbeStatus(verdicts []DatasetCapabilityVerdict) string {
	if len(verdicts) == 0 {
		return CapabilityProbeFailed
	}
	success, partial, failed, pending := 0, 0, 0, 0
	for _, item := range verdicts {
		switch item.ProbeStatus {
		case CapabilityProbeSuccess:
			success++
		case CapabilityProbePendingSync:
			pending++
		case CapabilityProbeFailed:
			failed++
		default:
			partial++
		}
	}
	switch {
	case failed > 0:
		return CapabilityProbeFailed
	case pending > 0 && success == 0:
		return CapabilityProbePendingSync
	case partial > 0 || pending > 0:
		return CapabilityProbePartial
	default:
		return CapabilityProbeSuccess
	}
}

// AggregateVerdicts applies the template's aggregate mode. `all_required` needs
// every dataset satisfied; `any_primary` accepts one satisfied primary dataset
// (reference datasets never satisfy an any_primary contract alone).
func AggregateVerdicts(knowledge *ScenarioTemplateKnowledge, verdicts []DatasetCapabilityVerdict) string {
	mode := scenarioAggregateAllRequired
	if knowledge != nil && knowledge.Aggregate != "" {
		mode = knowledge.Aggregate
	}
	if mode == scenarioAggregateAnyPrimary {
		for _, item := range verdicts {
			if item.Role == scenarioDatasetRolePrimary && item.Verdict == CapabilityVerdictSatisfied {
				return "any_primary_satisfied"
			}
		}
		return "any_primary_unsatisfied"
	}
	for _, item := range verdicts {
		if item.Verdict != CapabilityVerdictSatisfied {
			return "all_required_unsatisfied"
		}
	}
	return "all_required_satisfied"
}

// DescribeDataset probes one dataset and returns its capability profile with
// the probe status that the aggregation needs.
func (s *Service) DescribeDataset(ctx context.Context, tenantID, datasetID string) (*DatasetCapabilityProfile, string, error) {
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil || link == nil {
		return nil, CapabilityProbeFailed, httperr.NotFound("dataset not found")
	}
	profile := &DatasetCapabilityProfile{
		DatasetID: link.ID, Name: link.Name, PushdownEnabled: link.PushdownEnabled,
		Capabilities: []CapabilityEvidence{},
	}
	if link.DocumentCount > 0 {
		profile.DocumentCount = int(link.DocumentCount)
	}

	probeStatus := CapabilityProbeSuccess
	cfg, cfgErr := s.RAGFlow.GetDatasetConfig(ctx, link.RAGFlowDatasetID)
	if cfgErr != nil {
		// No config ⇒ no capability claims beyond "we could not look". Partial is
		// the honest status; the verdict will cap at probe_limited.
		probeStatus = CapabilityProbePartial
	} else {
		profile.ChunkMethod = normalizeChunkMethod(cfg.ParseMethod())
		profile.EmbeddingModel = cfg.EmbeddingModel
	}
	parserConfig := map[string]interface{}{}
	if cfg != nil && cfg.ParserConfig != nil {
		parserConfig = cfg.ParserConfig
	}

	docs, docsErr := s.RAGFlow.ListDocuments(ctx, link.RAGFlowDatasetID)
	if docsErr != nil {
		if probeStatus == CapabilityProbeSuccess {
			probeStatus = CapabilityProbePartial
		}
	} else {
		profile.DocumentCount = len(docs)
		metaReady := 0
		for _, doc := range docs {
			if doc.ChunkCount > 0 {
				profile.ParsedCount++
			}
			if len(doc.Metadata) > 0 {
				metaReady++
			}
		}
		profile.Capabilities = append(profile.Capabilities,
			metadataEvidence(metaReady, len(docs), link.PushdownEnabled, parserConfig),
		)
	}

	profile.Capabilities = append(profile.Capabilities, vectorEvidence(profile)...)
	profile.Capabilities = append(profile.Capabilities, parserConfigEvidence(parserConfig, profile.ChunkMethod)...)
	profile.Capabilities = append(profile.Capabilities, s.graphEvidence(ctx, link, parserConfig)...)

	usage, usageErr := s.datasetSharedUsage(ctx, tenantID, datasetID)
	if usageErr == nil {
		profile.SharedUsage = DatasetSharedUsage{
			Count: usage.Shared(), ChatIDs: usage.ChatIDs, SearchIDs: usage.SearchIDs, ReleaseIDs: usage.ReleaseIDs,
		}
	} else if probeStatus == CapabilityProbeSuccess {
		probeStatus = CapabilityProbePartial
	}
	// A dataset whose documents are still being parsed cannot be called
	// operational; RAGFlow keeps a `pending_sync` window between parse submit and
	// chunk availability, and a check must not advertise it as ready.
	if profile.DocumentCount > 0 && profile.ParsedCount < profile.DocumentCount {
		probeStatus = CapabilityProbePendingSync
	}
	sort.SliceStable(profile.Capabilities, func(i, j int) bool {
		return profile.Capabilities[i].Capability < profile.Capabilities[j].Capability
	})
	return profile, probeStatus, nil
}

func vectorEvidence(profile *DatasetCapabilityProfile) []CapabilityEvidence {
	if profile.EmbeddingModel == "" {
		return []CapabilityEvidence{{
			Capability: "vector", Status: CapabilityStatusSupported, Source: "dataset.config.embedding_model",
			Note: "no embedding model bound; retrieval cannot run",
		}}
	}
	status := CapabilityStatusConfigured
	note := "embedding model bound"
	if profile.ParsedCount > 0 {
		status = CapabilityStatusOperational
		note = "chunks parsed and retrievable"
	}
	return []CapabilityEvidence{{
		Capability: "vector", Status: status, Source: "dataset.config.embedding_model + document.chunk_count", Note: note,
	}}
}

func metadataEvidence(metaReady, total int, pushdown bool, parserConfig map[string]interface{}) CapabilityEvidence {
	if total == 0 {
		return CapabilityEvidence{
			Capability: "metadata_filter", Status: CapabilityStatusSupported, Source: "document.meta_fields",
			Note: "dataset has no documents; metadata readiness unknown",
		}
	}
	if metaReady == 0 {
		return CapabilityEvidence{
			Capability: "metadata_filter", Status: CapabilityStatusConfigured, Source: "document.meta_fields",
			Note: "no document carries meta_fields",
		}
	}
	return CapabilityEvidence{
		Capability: "metadata_filter", Status: CapabilityStatusReady, Source: "document.meta_fields",
		Note: pushdownNote(pushdown, parserConfig),
	}
}

func pushdownNote(pushdown bool, parserConfig map[string]interface{}) string {
	if !pushdown {
		return "metadata present; pushdown disabled on the link"
	}
	if value, ok := parserConfig["metadata_filter"]; ok && value != nil {
		return "metadata present and pushed down to the engine"
	}
	return "pushdown enabled, engine-side meta schema not observed"
}

// parserConfigEvidence derives the capabilities that RAGFlow drives purely from
// parser_config (doc/276 §6.6). These can be ready without a runtime probe; the
// verdict treats them as ready, not operational.
func parserConfigEvidence(parserConfig map[string]interface{}, chunkMethod string) []CapabilityEvidence {
	out := make([]CapabilityEvidence, 0, 5)
	if nestedBool(parserConfig, "raptor", "use_raptor") {
		out = append(out, CapabilityEvidence{
			Capability: "raptor", Status: CapabilityStatusReady, Source: "dataset.config.parser_config.raptor",
			Note: "RAPTOR summarisation enabled",
		})
	}
	if value, ok := parserConfig["image2text"]; ok && value != nil {
		out = append(out, CapabilityEvidence{
			Capability: "vision", Status: CapabilityStatusConfigured, Source: "dataset.config.parser_config.image2text",
			Note: "vision parser model configured; no runtime probe exists",
		})
	}
	if chunkMethod == "paper" || chunkMethod == "book" {
		out = append(out, CapabilityEvidence{
			Capability: "citation_locator", Status: CapabilityStatusConfigured,
			Source: "dataset.config.chunk_method", Note: "structured chunking keeps section anchors",
		})
	}
	if nestedBool(parserConfig, "graphrag", "use_graphrag") {
		out = append(out, CapabilityEvidence{
			Capability: "graph", Status: CapabilityStatusReady, Source: "dataset.config.parser_config.graphrag",
			Note: "GraphRAG enabled in parser_config",
		})
	}
	return out
}

// graphEvidence upgrades graph to operational only when the engine returns
// actual nodes. RAGFlow's graph endpoint is read-only and may be empty while a
// build is still running, so an empty graph is `configured`, never `blocked`.
func (s *Service) graphEvidence(ctx context.Context, link *model.DatasetLink, parserConfig map[string]interface{}) []CapabilityEvidence {
	graph, err := s.RAGFlow.GetDatasetGraph(ctx, link.RAGFlowDatasetID)
	if err != nil || graph == nil || len(graph.Nodes) == 0 {
		return nil
	}
	return []CapabilityEvidence{{
		Capability: "graph", Status: CapabilityStatusOperational, Source: "dataset.graph.nodes",
		Note: "knowledge graph built with " + itoa(len(graph.Nodes)) + " nodes",
	}}
}

// EvaluateRequirements compares the template contract against one dataset
// profile and produces the per-requirement verdict plus repairable gaps.
func EvaluateRequirements(knowledge *ScenarioTemplateKnowledge, role string, profile DatasetCapabilityProfile) DatasetCapabilityVerdict {
	verdict := DatasetCapabilityVerdict{
		Role: role, DatasetID: profile.DatasetID, ProbeStatus: CapabilityProbeSuccess,
		Capabilities: profile.Capabilities, SharedUsage: profile.SharedUsage,
		Repairs: []CapabilityRepair{},
	}
	byCapability := map[string]CapabilityEvidence{}
	for _, evidence := range profile.Capabilities {
		byCapability[evidence.Capability] = evidence
	}
	contract := knowledge
	if contract == nil {
		contract = &ScenarioTemplateKnowledge{}
	}
	requirements := applicableRequirements(contract.RequiredCapabilities, role)
	verdict.Aggregate = contract.Aggregate
	for _, requirement := range requirements {
		achieved := capabilityRank(byCapability[requirement.Capability].Status)
		needed := capabilityRank(requirement.MinLevel)
		met := achieved >= needed
		item := RequirementVerdict{
			Capability: requirement.Capability, Scope: requirement.Scope,
			MinLevel: requirement.MinLevel, Achieved: rankLabel(achieved), Satisfied: met,
		}
		if !met {
			// A capability the engine does not support at all cannot be repaired
			// by configuration: that is a hard block, not a repairable gap.
			if achieved == 0 && byCapability[requirement.Capability].Status == "" && !capabilitySupportedByEngine(requirement.Capability) {
				item.Reason = "capability_not_supported"
				verdict.Repairs = append(verdict.Repairs, CapabilityRepair{
					Action: "remove_requirement", Severity: CapabilityVerdictBlocked,
					Reason: "engine does not support " + requirement.Capability + " in this deployment",
				})
			} else if requirement.Capability == "metadata_filter" || requirement.Capability == "metadata_filter_pushdown" {
				item.Reason = "metadata_not_ready"
				verdict.Repairs = append(verdict.Repairs, CapabilityRepair{
					Action: "enable_pushdown_and_sync_meta", Severity: CapabilityVerdictRepairable,
					Reason: "enable metadata pushdown on the dataset and re-sync document meta_fields",
				})
			} else {
				item.Reason = "below_min_level"
				verdict.Repairs = append(verdict.Repairs, CapabilityRepair{
					Action: "reconfigure_dataset", Severity: CapabilityVerdictRepairable,
					Reason: "dataset config does not reach " + requirement.MinLevel + " for " + requirement.Capability,
				})
			}
		}
		verdict.Requirements = append(verdict.Requirements, item)
	}
	// A chunk-method mismatch is the other repairable gap: it never blocks, and
	// applying it is the dedicated parse-profile endpoint's job.
	if repairs := contract.chunkMethodRepairs(role, &profile); len(repairs) > 0 {
		verdict.Repairs = append(verdict.Repairs, repairs...)
	}
	verdict.Verdict = datasetVerdict(verdict.Requirements, profile, len(verdict.Repairs) > 0)
	return verdict
}

// datasetVerdict applies the fixed order blocked > probe_limited > repairable >
// satisfied for one dataset.
func datasetVerdict(requirements []RequirementVerdict, profile DatasetCapabilityProfile, hasRepairs bool) string {
	blocked, repairable, limited := false, false, false
	for _, item := range requirements {
		if item.Satisfied {
			continue
		}
		if item.Reason == "capability_not_supported" {
			blocked = true
			continue
		}
		if item.Reason == "metadata_not_ready" {
			// Metadata gaps are repairable in principle, but only when the dataset
			// already has documents with meta_fields or pushdown enabled; with no
			// documents at all the answer is "unknown", not "repairable".
			if profile.DocumentCount == 0 {
				limited = true
				continue
			}
			repairable = true
			continue
		}
		repairable = true
	}
	if hasRepairs {
		repairable = true
	}
	switch {
	case blocked:
		return CapabilityVerdictBlocked
	case limited:
		return CapabilityVerdictProbeLimited
	case repairable:
		return CapabilityVerdictRepairable
	default:
		return CapabilityVerdictSatisfied
	}
}

// aggregateChunkMethod reports whether the dataset chunk method needs a repair
// to satisfy the template's parse intent for this role. The check is advisory:
// it does not fail the read, it only explains the repairable verdict.
func (k ScenarioTemplateKnowledge) chunkMethodRepairs(role string, profile *DatasetCapabilityProfile) []CapabilityRepair {
	wanted := k.chunkMethodForRole(role)
	if wanted == "" || profile == nil || profile.ChunkMethod == "" {
		return nil
	}
	if normalizeChunkMethod(profile.ChunkMethod) == wanted {
		return nil
	}
	return []CapabilityRepair{{
		Action: "apply_parse_profile", Severity: CapabilityVerdictRepairable,
		Reason: "chunk_method is " + profile.ChunkMethod + ", template expects " + wanted,
	}}
}

func (k ScenarioTemplateKnowledge) chunkMethodForRole(role string) string {
	for _, dataset := range k.Datasets {
		if dataset.Role != role {
			continue
		}
		if dataset.ParseIntent != nil && dataset.ParseIntent.ChunkMethod != "" {
			return normalizeChunkMethod(dataset.ParseIntent.ChunkMethod)
		}
	}
	return ""
}

func applicableRequirements(requirements []ScenarioCapabilityRequirement, role string) []ScenarioCapabilityRequirement {
	out := make([]ScenarioCapabilityRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.Scope == scenarioRequirementScopePrimary && role != scenarioDatasetRolePrimary {
			continue
		}
		out = append(out, requirement)
	}
	return out
}

func capabilityRank(status string) int {
	switch status {
	case CapabilityStatusOperational:
		return 4
	case CapabilityStatusReady:
		return 3
	case CapabilityStatusConfigured:
		return 2
	case CapabilityStatusSupported:
		return 1
	default:
		return 0
	}
}

func rankLabel(rank int) string {
	switch rank {
	case 4:
		return CapabilityStatusOperational
	case 3:
		return CapabilityStatusReady
	case 2:
		return CapabilityStatusConfigured
	case 1:
		return CapabilityStatusSupported
	default:
		return "unknown"
	}
}

// capabilitySupportedByEngine separates "not configured yet" from "this engine
// build cannot do it at all". Only `wiki` is out of scope for the P0 provider
// (compilation is a platform feature, not a RAGFlow dataset capability).
func capabilitySupportedByEngine(capability string) bool {
	return capability != "wiki"
}

func nestedBool(config map[string]interface{}, outer, inner string) bool {
	value, ok := config[outer]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		if flag, ok := typed[inner].(bool); ok {
			return flag
		}
	case bool:
		return typed
	}
	return false
}
