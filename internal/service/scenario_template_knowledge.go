package service

import (
	"encoding/json"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// Scenario template knowledge contract (doc/276 §7.2 / R2 + R3).
//
// The block is an *optional* extension of the v2 payload: templates written
// before it existed keep working through `datasetSuggestions`, and a template
// that carries it must be internally consistent or the write is rejected with
// 40105. Phase A only enables `mode=builtin`; `mode=pipeline` is rejected
// explicitly instead of being silently downgraded to a builtin parser.

// Capability closed set (doc/276 §6.5). Anything outside this set in a
// published baseline is a validation error, so a template can never promise a
// capability the evaluator does not model.
const (
	CapabilityVector                  = "vector"
	CapabilityMetadataFilter          = "metadata_filter"
	CapabilityMetadataFilterPushdown  = "metadata_filter_pushdown"
	CapabilityGraph                   = "graph"
	CapabilityRaptor                  = "raptor"
	CapabilityVision                  = "vision"
	CapabilityCitationLocator         = "citation_locator"
	CapabilityWiki                    = "wiki"
	scenarioAggregateAllRequired      = "all_required"
	scenarioAggregateAnyPrimary       = "any_primary"
	scenarioRequirementScopeAll       = "all"
	scenarioRequirementScopePrimary   = "primary"
	scenarioDatasetRolePrimary        = "primary"
	scenarioDatasetRoleReference      = "reference"
	scenarioRequirementLevelConfig    = "configured"
	scenarioRequirementLevelReady     = "ready"
	scenarioRequirementLevelOperation = "operational"
)

// ValidScenarioCapabilities is the closed set requiredCapabilities may name.
var ValidScenarioCapabilities = map[string]bool{
	CapabilityVector:                 true,
	CapabilityMetadataFilter:         true,
	CapabilityMetadataFilterPushdown: true,
	CapabilityGraph:                  true,
	CapabilityRaptor:                 true,
	CapabilityVision:                 true,
	CapabilityCitationLocator:        true,
	CapabilityWiki:                   true,
}

// ScenarioTemplateKnowledge is the optional `knowledge` block of the template
// payload. Every field is optional so a v2 payload stays readable.
type ScenarioTemplateKnowledge struct {
	// Aggregate is how the per-dataset verdicts fold into the template verdict:
	// all_required (default) or any_primary.
	Aggregate string `json:"aggregate,omitempty"`
	// Datasets declares each dataset the scenario expects, with the role it
	// plays and the parse intent it should be created with.
	Datasets []ScenarioKnowledgeDataset `json:"datasets,omitempty"`
	// RequiredCapabilities must be satisfied before the scenario can be marked
	// ready; OptionalCapabilities are shown but never block.
	RequiredCapabilities []ScenarioCapabilityRequirement `json:"requiredCapabilities,omitempty"`
	OptionalCapabilities []string                        `json:"optionalCapabilities,omitempty"`
	// Lineage marks payloads whose upstream parse semantics are unknown
	// (doc/276 §7.2 / R28). Templates copied from a pre-2.0 snapshot carry
	// `legacy_semantics_unknown` and must be re-forked to gain the contract.
	Lineage string `json:"lineage,omitempty"`
}

// ScenarioKnowledgeDataset is one expected dataset binding.
type ScenarioKnowledgeDataset struct {
	Role string `json:"role,omitempty"`
	// Name is the pack's display name for the dataset (doc/276 §6.4). It is
	// presentation only: binding still resolves through Suggestions.
	Name        string   `json:"name,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
	// Required and CreateIfMissing drive the wizard's dataset step: a required
	// dataset that is missing blocks instantiation unless the caller opts into
	// creating it (doc/276 §7.5).
	Required        bool                   `json:"required,omitempty"`
	CreateIfMissing bool                   `json:"createIfMissing,omitempty"`
	ParseIntent     *ScenarioParseIntent   `json:"parseIntent,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// ScenarioParseIntent is the mutually exclusive parse declaration. `mode`
// selects exactly one shape: `builtin` (chunk method + parser config) or
// `pipeline` (upstream pipeline binding, phase B).
type ScenarioParseIntent struct {
	Mode         string                  `json:"mode"`
	ChunkMethod  string                  `json:"chunkMethod,omitempty"`
	ParserConfig map[string]interface{}  `json:"parserConfig,omitempty"`
	Pipeline     *ScenarioPipelineIntent `json:"pipeline,omitempty"`
}

// ScenarioPipelineIntent binds a dataset to an immutable upstream canvas
// version. Unused in phase A but part of the stable contract.
type ScenarioPipelineIntent struct {
	FromUpstreamTemplateID string `json:"fromUpstreamTemplateId,omitempty"`
	PipelineID             string `json:"pipelineId,omitempty"`
	PipelineVersionID      string `json:"pipelineVersionId,omitempty"`
	PublishBeforeBind      bool   `json:"publishBeforeBind,omitempty"`
}

// ScenarioCapabilityRequirement is one required capability plus its scope.
type ScenarioCapabilityRequirement struct {
	Capability string `json:"capability"`
	Scope      string `json:"scope,omitempty"`
	MinLevel   string `json:"minLevel,omitempty"`
}

// legacyChunkMethodAliases maps upstream legacy parser ids to the chunk method
// vocabulary this repo validates against (doc/276 §6.5 rule 6). Aliases are
// read-only compatibility: nothing in this repo may *write* a legacy name.
var legacyChunkMethodAliases = map[string]string{
	"naive":           "general",
	"laws":            "paper",
	"manual":          "book",
	"qa":              "table",
	"resume":          "general",
	"one":             "general",
	"knowledge_graph": "graph",
}

// normalizeChunkMethod maps an upstream/legacy chunk method onto the validated
// v2 vocabulary. Unknown names are returned unchanged so callers can decide
// whether to reject them.
func normalizeChunkMethod(raw string) string {
	method := strings.ToLower(strings.TrimSpace(raw))
	if method == "" {
		return ""
	}
	if validChunkMethods[method] {
		return method
	}
	if mapped, ok := legacyChunkMethodAliases[method]; ok && validChunkMethods[mapped] {
		return mapped
	}
	return method
}

// knowledgeValidationError is the single error shape for an unusable
// `knowledge` block (doc/276 §11.2: 400 40105).
func knowledgeValidationError(code, reason string) error {
	return httperr.BadRequest(40105, "template knowledge is invalid: "+reason+" ("+code+")")
}

// ValidateScenarioTemplateKnowledge checks the optional knowledge block.
// A nil block is valid (v2 payloads without the contract keep round-tripping).
func (k *ScenarioTemplateKnowledge) Validate() error {
	if k == nil {
		return nil
	}
	switch k.Aggregate {
	case "", scenarioAggregateAllRequired, scenarioAggregateAnyPrimary:
	default:
		return knowledgeValidationError("aggregate_unknown", "aggregate must be all_required or any_primary")
	}
	if k.Lineage != "" && k.Lineage != "legacy_semantics_unknown" && k.Lineage != "confirmed_baseline" {
		return knowledgeValidationError("lineage_unknown", "lineage must be empty, confirmed_baseline or legacy_semantics_unknown")
	}
	primary := 0
	for i := range k.Datasets {
		dataset := &k.Datasets[i]
		switch dataset.Role {
		case "", scenarioDatasetRolePrimary, scenarioDatasetRoleReference:
		default:
			return knowledgeValidationError("role_unknown", "dataset role must be primary or reference")
		}
		if dataset.Role == scenarioDatasetRolePrimary {
			primary++
		}
		if err := dataset.ParseIntent.Validate(); err != nil {
			return err
		}
	}
	// The capability check has to know which dataset it may look at, so a
	// `scope=primary` requirement without a primary dataset is unanswerable.
	needsPrimary := false
	for i := range k.RequiredCapabilities {
		requirement := &k.RequiredCapabilities[i]
		if !ValidScenarioCapabilities[requirement.Capability] {
			return knowledgeValidationError("capability_outside_closed_set", "required capability "+requirement.Capability+" is not in the supported set")
		}
		switch requirement.Scope {
		case "", scenarioRequirementScopeAll:
			requirement.Scope = scenarioRequirementScopeAll
		case scenarioRequirementScopePrimary:
			needsPrimary = true
		default:
			return knowledgeValidationError("scope_unknown", "capability scope must be all or primary")
		}
		switch requirement.MinLevel {
		case "":
			requirement.MinLevel = scenarioRequirementLevelConfig
		case scenarioRequirementLevelConfig, scenarioRequirementLevelReady, scenarioRequirementLevelOperation:
		default:
			return knowledgeValidationError("min_level_unknown", "minLevel must be configured, ready or operational")
		}
	}
	for i := range k.OptionalCapabilities {
		if !ValidScenarioCapabilities[k.OptionalCapabilities[i]] {
			return knowledgeValidationError("capability_outside_closed_set", "optional capability "+k.OptionalCapabilities[i]+" is not in the supported set")
		}
	}
	if needsPrimary && primary == 0 {
		return knowledgeValidationError("primary_dataset_missing", "a scope=primary requirement needs a primary dataset")
	}
	return nil
}

// Validate checks one dataset parse intent.
func (p *ScenarioParseIntent) Validate() error {
	if p == nil {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(p.Mode))
	switch mode {
	case model.ParseModeBuiltin:
		if p.Pipeline != nil {
			return knowledgeValidationError("modes_are_mutually_exclusive", "mode=builtin cannot carry pipeline")
		}
		method := normalizeChunkMethod(p.ChunkMethod)
		if method == "" {
			return knowledgeValidationError("chunk_method_required", "mode=builtin requires chunkMethod")
		}
		if !validChunkMethods[method] {
			return knowledgeValidationError("chunk_method_unknown", "chunkMethod "+p.ChunkMethod+" is not a supported chunk method")
		}
		p.Mode = model.ParseModeBuiltin
		p.ChunkMethod = method
		return nil
	case model.ParseModePipeline:
		// Phase A does not open the pipeline path end to end (doc/276 §7.2:
		// upstream rejects a dataset create without a published pipeline
		// version, and the spike has not run). Reject instead of silently
		// degrading to builtin.
		return knowledgeValidationError("mode_not_enabled_in_phase_a", "mode=pipeline is not enabled in this release")
	default:
		return knowledgeValidationError("mode_unknown", "parseIntent.mode must be builtin or pipeline")
	}
}

// chunkMethodOf returns the normalized chunk method a knowledge block declares
// for the given dataset role, or "" when the block does not declare one.
func (k *ScenarioTemplateKnowledge) chunkMethodOf(role string) string {
	if k == nil {
		return ""
	}
	for i := range k.Datasets {
		dataset := &k.Datasets[i]
		if role != "" && dataset.Role != "" && dataset.Role != role {
			continue
		}
		if dataset.ParseIntent == nil {
			continue
		}
		if method := normalizeChunkMethod(dataset.ParseIntent.ChunkMethod); method != "" {
			return method
		}
	}
	return ""
}

// hashTemplatePayload is the drift detector used by the seed/baseline publish
// paths: identical content must not create a new immutable version.
func hashTemplatePayload(raw string) string {
	var probe interface{}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return raw
	}
	canonical, err := json.Marshal(probe)
	if err != nil {
		return raw
	}
	return string(canonical)
}
