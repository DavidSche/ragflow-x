package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// scenarioCanvasComponents is the RAGFlow v1.0.0 agent component whitelist
// (doc/276 §8.3). The names come from `internal/agent/component/*.go` in the
// engine source: every `componentName*` constant plus the literal
// registrations (Agent, Categorize, LLM, SearchMyDataset). Anything outside the
// set is rejected at import/instantiation time instead of being forwarded to
// the engine, where an unknown node would fail mid-run with a less actionable
// error.
//
// Deliberately absent: WebSearch / Vision / KeywordExtract. They exist in the
// Python engine but not in the Go engine this deployment runs, so accepting
// them would promise a capability the runtime does not have.
var scenarioCanvasComponents = map[string]bool{
	"agent":              true,
	"begin":              true,
	"browser":            true,
	"categorize":         true,
	"codeexec":           true,
	"dataoperations":     true,
	"docgenerator":       true,
	"docsgenerator":      true,
	"excelprocessor":     true,
	"exesql":             true,
	"fillup":             true,
	"generate":           true,
	"invoke":             true,
	"iteration":          true,
	"iterationitem":      true,
	"keywordextract":     false,
	"listoperations":     true,
	"llm":                true,
	"loop":               true,
	"message":            true,
	"parallel":           true,
	"retrieval":          true,
	"searchmydataset":    true,
	"search_my_dataset":  true,
	"search_my_dateset":  true, // upstream compatibility alias (sic)
	"stringtransform":    true,
	"switch":             true,
	"userfillup":         true,
	"variableaggregator": true,
	"variableassigner":   true,
}

// scenarioForbiddenKeyPattern catches credential-shaped field names anywhere in
// an imported pack (doc/276 §8.7 "凭据"). Matching is case-insensitive on the
// normalized key, so `llm.api_key`, `apiKey` and `API-KEY` all hit.
var scenarioForbiddenKeyPattern = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passwd|credential|authorization|access[_-]?key)`)

// scenarioLinkKeyPattern marks fields whose string value must not leave our
// origin. Only these keys are URL-checked: a prompt may legitimately contain
// "https://" inside example text, a `base_url` may not.
var scenarioLinkKeyPattern = regexp.MustCompile(`(?i)^(url|uri|endpoint|base[_-]?url|host|callback|webhook)$`)

// RenderedScenarioApplication is the validated, engine-independent application
// definition produced from a pack (doc/276 §8.3 "Validated App Definition"):
// schema-checked, node-whitelisted, reference-checked. It is the only shape the
// adapters (chat / search / agent) are allowed to consume.
type RenderedScenarioApplication struct {
	AppType          string
	PromptConfig     map[string]interface{}
	LLMSetting       map[string]interface{}
	TopN             int
	Similarity       float64
	VectorWeight     float64
	ModelRequirement ScenarioPackModelRequirement
	CanvasDSL        map[string]interface{}
	Datasets         []ScenarioKnowledgeDataset
	Eval             ScenarioPackEval
	ProfileSource    string
}

// RenderScenarioPack validates a pack and projects it into the application
// definition. It never mutates the pack and never talks to the engine: all
// reference checks that need upstream state are done by the caller with the
// returned requirement.
func RenderScenarioPack(pack *ScenarioPack) (*RenderedScenarioApplication, error) {
	if pack == nil {
		return nil, httperr.BadRequest(40105, "scenario pack is required")
	}
	if err := pack.Validate(); err != nil {
		return nil, err
	}
	if err := scanScenarioContent(pack.RuntimeExtension, "runtimeExtension"); err != nil {
		return nil, err
	}
	if err := validateCanvasDSL(pack.RuntimeExtension.RAGFlow); err != nil {
		return nil, err
	}
	promptConfig := map[string]interface{}{
		"system":         pack.Application.Prompt.System,
		"prologue":       pack.Application.Prompt.Prologue,
		"empty_response": pack.Application.Prompt.EmptyResponse,
	}
	llmSetting := map[string]interface{}{}
	for key, value := range pack.Application.Parameters.LLMSetting {
		llmSetting[key] = value
	}
	if len(llmSetting) == 0 {
		llmSetting = nil
	}
	rendered := &RenderedScenarioApplication{
		AppType:          pack.Application.AppType,
		PromptConfig:     promptConfig,
		LLMSetting:       llmSetting,
		TopN:             pack.Application.Parameters.TopN,
		Similarity:       pack.Application.Parameters.SimilarityThreshold,
		VectorWeight:     pack.Application.Parameters.VectorSimilarityWeight,
		ModelRequirement: pack.Application.ModelRequirement,
		Datasets:         knowledgeDatasets(pack.Knowledge),
		Eval:             pack.Eval,
		ProfileSource:    pack.Application.ProfileSource,
	}
	if pack.RuntimeExtension.RAGFlow != nil {
		rendered.CanvasDSL = pack.RuntimeExtension.RAGFlow.CanvasDSL
	}
	// §8.3: an agent pack without a DSL cannot be instantiated. Failing here
	// (instead of creating an empty canvas) keeps the engine free of canvases
	// that no one can run.
	if rendered.AppType == "agent" && len(rendered.CanvasDSL) == 0 {
		return nil, httperr.BadRequest(40105, "agent scenario pack must carry runtimeExtension.ragflow.canvasDsl")
	}
	return rendered, nil
}

func knowledgeDatasets(knowledge *ScenarioTemplateKnowledge) []ScenarioKnowledgeDataset {
	if knowledge == nil || len(knowledge.Datasets) == 0 {
		return nil
	}
	return knowledge.Datasets
}

// validateCanvasDSL enforces the node whitelist and the structural contract of
// an agent canvas: `{components: [{obj: {component_name: ...}}]}`.
func validateCanvasDSL(extension *ScenarioPackRAGFlowExtension) error {
	if extension == nil || len(extension.CanvasDSL) == 0 {
		return nil
	}
	rawComponents, ok := extension.CanvasDSL["components"]
	if !ok {
		return httperr.BadRequest(40105, "canvasDsl.components is required")
	}
	// RAGFlow v1.0.0 serialises the canvas as an object keyed by node id
	// ({"begin": {...}, "Agent:x": {...}}); older/client-authored canvases use an
	// array of nodes. Both carry the same node body, so both are accepted — keying
	// off the array shape alone would reject every real agent template
	// (live drill 2026-11-13: "canvasDsl.components must be an array").
	var components []interface{}
	switch typed := rawComponents.(type) {
	case []interface{}:
		components = typed
	case map[string]interface{}:
		for _, nodeID := range sortedComponentIDs(typed) {
			node, ok := typed[nodeID].(map[string]interface{})
			if !ok {
				return httperr.BadRequest(40105, "canvasDsl.components."+nodeID+" must be an object")
			}
			components = append(components, node)
		}
	default:
		return httperr.BadRequest(40105, "canvasDsl.components must be an array or an object keyed by node id")
	}
	if len(components) == 0 {
		return httperr.BadRequest(40105, "canvasDsl.components must not be empty")
	}
	for index, raw := range components {
		component, ok := raw.(map[string]interface{})
		if !ok {
			return httperr.BadRequest(40105, fmt.Sprintf("canvasDsl.components[%d] must be an object", index))
		}
		obj, _ := component["obj"].(map[string]interface{})
		name, _ := obj["component_name"].(string)
		if name == "" {
			name, _ = component["component_name"].(string)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return httperr.BadRequest(40105, fmt.Sprintf("canvasDsl.components[%d].obj.component_name is required", index))
		}
		if !scenarioCanvasComponents[name] {
			return httperr.BadRequest(40105, fmt.Sprintf("canvasDsl.components[%d] uses a component outside the whitelist: %s", index, name))
		}
	}
	return nil
}

// sortedComponentIDs keeps validation deterministic: map iteration order in Go is
// random, and an error message must point at the same node twice in a row.
func sortedComponentIDs(components map[string]interface{}) []string {
	ids := make([]string, 0, len(components))
	for nodeID := range components {
		ids = append(ids, nodeID)
	}
	sort.Strings(ids)
	return ids
}

// carriesSecretValue reports whether a value under a credential-shaped key can
// actually carry a credential. The key pattern matches `token` as a substring,
// so ordinary model parameters such as `maxTokensEnabled: true` or
// `max_tokens: 4096` would otherwise be rejected as credentials. A secret is a
// non-empty string (or a container holding one); a boolean or a number cannot
// carry one, so those keys are skipped and the walk continues into containers.
func carriesSecretValue(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case map[string]interface{}:
		return len(typed) > 0
	case []interface{}:
		return len(typed) > 0
	}
	// json.Number, float64, int, ...: a count or a flag, not a secret.
	return false
}

// scanScenarioContent rejects credential-shaped keys and cross-origin link
// fields anywhere in the provided value (doc/276 §8.7).
func scanScenarioContent(value interface{}, path string) error {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			childPath := path + "." + key
			if scenarioForbiddenKeyPattern.MatchString(key) && carriesSecretValue(child) {
				return httperr.BadRequest(40116, "scenario pack must not contain credentials: "+childPath)
			}
			if text, ok := child.(string); ok && scenarioLinkKeyPattern.MatchString(key) && isExternalURL(text) {
				return httperr.BadRequest(40116, "scenario pack must not reference an external endpoint: "+childPath)
			}
			if err := scanScenarioContent(child, childPath); err != nil {
				return err
			}
		}
	case []interface{}:
		for index, child := range typed {
			if err := scanScenarioContent(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

// isExternalURL reports whether a link value points outside our origin. Local
// paths (`/api/v1/...`), empty values and non-URL strings are allowed.
func isExternalURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Host != ""
	default:
		return false
	}
}

// ValidateScenarioImportTrust is the untrusted-input gate for `import` and for
// published-pack reads (doc/276 §8.7). It decodes strictly, then scans the raw
// document for credentials and external endpoints. Imported assets are always
// stored as drafts regardless of the document's requested status.
//
// Two shapes reach this gate: a bare v3 pack, and an export document whose
// `payload` holds the pack. In the export shape the top-level `schema` belongs to
// the envelope, so decoding the *document* as a pack would reject every exported
// v3 template with `unknown field "key"`. The pack is therefore validated where
// it actually is.
func ValidateScenarioImportTrust(raw []byte) error {
	var probe struct {
		Schema  string          `json:"schema"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return httperr.BadRequest(40116, "scenario template document is not valid JSON")
	}
	var scanned interface{}
	if err := json.Unmarshal(raw, &scanned); err != nil {
		return httperr.BadRequest(40116, "scenario template document is not valid JSON")
	}
	if err := scanScenarioContent(scanned, "document"); err != nil {
		return err
	}
	packRaw := raw
	if len(probe.Payload) > 0 && string(probe.Payload) != "null" {
		packRaw = probe.Payload
	}
	if !packsAsV3(packRaw) {
		return nil
	}
	pack, err := DecodeScenarioPack(packRaw)
	if err != nil {
		return err
	}
	// The trust scan guards the untrusted path: credentials, external endpoints
	// and unknown fields. Import always lands as an `imported` draft, so only
	// the structural half of validation runs here — the business parts a human
	// author owns (system prompt, business copy, evaluation questions) are
	// tolerated and enforced later by publish's full Validate. Running the full
	// Validate here would make an exported draft un-reimportable (drill 2026-11-14,
	// "application.prompt.system is required" on a legitimately empty skeleton draft).
	return pack.ValidateDraft()
}

// packsAsV3 reports whether a payload declares the v3 pack schema.
func packsAsV3(raw []byte) bool {
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Schema == ScenarioPackSchemaV3
}

// validateCanvasDSLValue applies the node whitelist to a canvas that did not
// come from a pack (the client supplied it). It exists so both entry points
// share one whitelist instead of two drifting copies.
func validateCanvasDSLValue(dsl map[string]interface{}) error {
	if len(dsl) == 0 {
		return nil
	}
	if err := scanScenarioContent(dsl, "canvasDsl"); err != nil {
		return err
	}
	return validateCanvasDSL(&ScenarioPackRAGFlowExtension{CanvasDSL: dsl})
}

// chatAuthoring projects the rendered definition into the authoring struct that
// CreateChatFromScenarioTemplate consumes.
func (r *RenderedScenarioApplication) chatAuthoring() ChatAuthoring {
	if r == nil {
		return ChatAuthoring{}
	}
	return ChatAuthoring{
		PromptConfig:           r.PromptConfig,
		TopN:                   r.TopN,
		SimilarityThreshold:    r.Similarity,
		VectorSimilarityWeight: r.VectorWeight,
		LLMSetting:             r.LLMSetting,
	}
}

// applySearchConfig fills a search app config from the pack. Fields the caller
// already set win, because the caller is the reviewed override.
func (r *RenderedScenarioApplication) applySearchConfig(cfg *ragflow.SearchConfig) {
	if r == nil || cfg == nil {
		return
	}
	if cfg.TopK == 0 && r.TopN > 0 {
		cfg.TopK = r.TopN
	}
	if cfg.SimilarityThreshold == 0 && r.Similarity > 0 {
		cfg.SimilarityThreshold = r.Similarity
	}
	if cfg.VectorSimilarityWeight == 0 && r.VectorWeight > 0 {
		cfg.VectorSimilarityWeight = r.VectorWeight
	}
	if len(cfg.LLMSetting) == 0 && len(r.LLMSetting) > 0 {
		cfg.LLMSetting = r.LLMSetting
	}
}

// resolveScenarioModelRequirement checks that the tenant can satisfy the pack's
// model requirements before anything is created (doc/276 §6.3 / §8.2) and
// returns the models that were actually resolved so the instantiation record can
// carry the real choice instead of repeating the template's request.
//
// Blocking rules: `chat` and `vision` are hard requirements (an assistant with no
// model cannot answer), `embedding` is a read-side hint only — a workspace whose
// dataset embedding model differs from the tenant default must not be locked out
// of every template by a warning (doc/276 §8.2).
func (s *Service) resolveScenarioModelRequirement(ctx context.Context, rendered *RenderedScenarioApplication) (map[string]string, error) {
	if rendered == nil || len(rendered.ModelRequirement.Capabilities) == 0 {
		return nil, nil
	}
	statuses, err := s.GetTenantDefaultModels(ctx)
	if err != nil {
		return nil, err
	}
	configured := map[string]string{}
	for _, status := range statuses {
		if status.Status == "configured" && status.ModelID != "" {
			configured[status.ModelType] = status.ModelID
		}
	}
	resolved := map[string]string{}
	missing := make([]string, 0, len(rendered.ModelRequirement.Capabilities))
	for _, capability := range rendered.ModelRequirement.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			continue
		}
		modelType, ok := scenarioModelTypeForCapability(capability)
		if !ok {
			missing = append(missing, capability)
			continue
		}
		if modelID := configured[modelType]; modelID != "" {
			resolved[capability] = modelID
			continue
		}
		if capability == "embedding" {
			logger.Warn("scenario pack embedding requirement has no tenant default", "template_profile", rendered.ProfileSource)
			continue
		}
		missing = append(missing, capability)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, httperr.BadRequest(40085, "scenario pack requires model capabilities with no tenant default: "+strings.Join(missing, ", "))
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	return resolved, nil
}

// scenarioModelTypeForCapability maps a pack capability onto the tenant default
// model type that satisfies it. `rerank` is deliberately absent: reranking is
// optional in RAGFlow, so an unset rerank default is not a blocker.
func scenarioModelTypeForCapability(capability string) (string, bool) {
	switch capability {
	case "chat":
		return "chat", true
	case "embedding":
		return "embedding", true
	case "vision":
		return "image2text", true
	default:
		return "", false
	}
}
