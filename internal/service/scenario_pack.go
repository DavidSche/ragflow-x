package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ScenarioPackSchemaV3 is the portable Scenario Pack schema (doc/276 §6.4).
// A v3 pack is stored verbatim as the version `payload_json` with
// `payload_schema='v3'`; every legacy consumer keeps reading the v2 projection
// that ProjectPayload() derives, so v3 never rewrites history.
const ScenarioPackSchemaV3 = "ragflow-x.scenario-pack.v3"

// Scenario pack closed sets (doc/276 §6.4 + §8.2 + §9).
var (
	scenarioPackModelCapabilities = map[string]bool{"chat": true, "embedding": true, "rerank": true, "vision": true}
	scenarioPackRuntimes          = map[string]bool{"ragflow": true}
)

// ScenarioPackBusiness is the human-facing half of a pack: what the template
// promises, independent of any engine.
type ScenarioPackBusiness struct {
	Role             string                                 `json:"role,omitempty"`
	ExpectedOutcome  string                                 `json:"expectedOutcome,omitempty"`
	SampleQuestions  []string                               `json:"sampleQuestions,omitempty"`
	PublishChecklist []string                               `json:"publishChecklist,omitempty"`
	Translations     map[string]ScenarioTemplateTranslation `json:"translations,omitempty"`
}

// ScenarioPackPrompt is the application prompt of a pack. Unlike v2, which
// references a platform preset id, v3 carries the text inline so a pack can be
// delivered across environments without depending on our preset table.
type ScenarioPackPrompt struct {
	System        string            `json:"system"`
	Prologue      string            `json:"prologue,omitempty"`
	EmptyResponse string            `json:"emptyResponse,omitempty"`
	Translations  map[string]string `json:"translations,omitempty"`
}

// ScenarioPackParameters are the retrieval/generation knobs a pack applies.
type ScenarioPackParameters struct {
	TopN                   int                    `json:"topN,omitempty"`
	SimilarityThreshold    float64                `json:"similarityThreshold,omitempty"`
	VectorSimilarityWeight float64                `json:"vectorSimilarityWeight,omitempty"`
	LLMSetting             map[string]interface{} `json:"llmSetting,omitempty"`
}

// ScenarioPackModelRequirement declares what the application needs from a
// model instead of pinning a provider model id (doc/276 §8.2).
type ScenarioPackModelRequirement struct {
	Capabilities        []string `json:"capabilities,omitempty"`
	PreferredFamily     string   `json:"preferredFamily,omitempty"`
	AllowTenantOverride bool     `json:"allowTenantOverride"`
}

// ScenarioPackApplication is the engine-independent application half.
type ScenarioPackApplication struct {
	AppType          string                       `json:"appType"`
	Prompt           ScenarioPackPrompt           `json:"prompt"`
	Parameters       ScenarioPackParameters       `json:"parameters,omitempty"`
	ProfileSource    string                       `json:"profileSource,omitempty"`
	ModelRequirement ScenarioPackModelRequirement `json:"modelRequirement,omitempty"`
}

// ScenarioPackEvalQuestion is one evaluation question shipped with a pack.
type ScenarioPackEvalQuestion struct {
	Question         string   `json:"question"`
	ExpectedKeywords []string `json:"expectedKeywords,omitempty"`
	ExpectedCitation bool     `json:"expectedCitation,omitempty"`
}

// ScenarioPackEvalThresholds are the pack's own acceptance thresholds. They are
// advisory in P1 (the gate is P2 §9) but validated here so a bad pack cannot be
// published.
type ScenarioPackEvalThresholds struct {
	ReturnRate            float64 `json:"returnRate,omitempty"`
	EvidenceLocatableRate float64 `json:"evidenceLocatableRate,omitempty"`
}

// ScenarioPackEval is the pack's evaluation fixture set.
type ScenarioPackEval struct {
	Questions       []ScenarioPackEvalQuestion `json:"questions,omitempty"`
	Thresholds      ScenarioPackEvalThresholds `json:"thresholds,omitempty"`
	RequireEvalPass bool                       `json:"requireEvalPass,omitempty"`
}

// ScenarioPackRuntimeCompatibility pins the engine contract a pack needs
// (doc/276 §6.7 + §9: the compatibility gate returns 40914 on mismatch).
type ScenarioPackRuntimeCompatibility struct {
	Runtime      string         `json:"runtime,omitempty"`
	Baseline     string         `json:"baseline,omitempty"`
	Capabilities map[string]int `json:"capabilities,omitempty"`
}

// ScenarioPackRequires is the pack's environment requirement block.
type ScenarioPackRequires struct {
	RuntimeCompatibility ScenarioPackRuntimeCompatibility `json:"runtimeCompatibility,omitempty"`
	MinAppVersion        string                           `json:"minAppVersion,omitempty"`
	PackSchema           string                           `json:"packSchema,omitempty"`
}

// ScenarioPackRAGFlowExtension carries the engine-specific tail of a pack. It
// is deliberately namespaced: a pack that carries no engine extension stays
// portable, and a canvas DSL is only ever read after the whitelist validation
// in RenderScenarioPack.
type ScenarioPackRAGFlowExtension struct {
	CanvasDSL       map[string]interface{} `json:"canvasDsl,omitempty"`
	PipelineBinding map[string]interface{} `json:"pipelineBinding,omitempty"`
}

// ScenarioPackRuntimeExtension is the runtime namespace of a pack.
type ScenarioPackRuntimeExtension struct {
	RAGFlow *ScenarioPackRAGFlowExtension `json:"ragflow,omitempty"`
}

// ScenarioPack is the v3 payload: engine-independent core plus a namespaced
// runtime extension (doc/276 §6.2).
type ScenarioPack struct {
	Schema           string                       `json:"schema"`
	Business         ScenarioPackBusiness         `json:"business,omitempty"`
	Knowledge        *ScenarioTemplateKnowledge   `json:"knowledge,omitempty"`
	Application      ScenarioPackApplication      `json:"application"`
	Eval             ScenarioPackEval             `json:"eval,omitempty"`
	RuntimeExtension ScenarioPackRuntimeExtension `json:"runtimeExtension,omitempty"`
	Requires         ScenarioPackRequires         `json:"requires,omitempty"`
}

// ValidateDraft is the structural half of Validate: it checks every closed set
// and machine-checkable invariant but tolerates the parts only a human author can
// supply (business copy, prompt, evaluation questions). Skeleton imports land
// here (doc/276 §8.4: "允许缺题集/缺业务文案"), and publish re-runs the full
// Validate so an incomplete draft can never go live.
func (p *ScenarioPack) ValidateDraft() error {
	if p == nil {
		return nil
	}
	if p.Schema != ScenarioPackSchemaV3 {
		return scenarioPackError("scenario pack schema must be "+ScenarioPackSchemaV3, nil)
	}
	if _, err := validAppTypes([]string{p.Application.AppType}); err != nil {
		return scenarioPackError("application.appType must be chat, search or agent", nil)
	}
	for _, capability := range p.Application.ModelRequirement.Capabilities {
		if !scenarioPackModelCapabilities[strings.TrimSpace(capability)] {
			return scenarioPackError("modelRequirement.capabilities contains an unsupported value: "+capability, nil)
		}
	}
	if p.Knowledge != nil {
		if err := p.Knowledge.Validate(); err != nil {
			return err
		}
	}
	for _, threshold := range []float64{p.Eval.Thresholds.ReturnRate, p.Eval.Thresholds.EvidenceLocatableRate} {
		if threshold < 0 || threshold > 1 {
			return scenarioPackError("eval thresholds must be within [0,1]", nil)
		}
	}
	requires := p.Requires
	if requires.PackSchema != "" && requires.PackSchema != "v3" {
		return scenarioPackError("requires.packSchema must be v3", nil)
	}
	if runtime := requires.RuntimeCompatibility.Runtime; runtime != "" && !scenarioPackRuntimes[runtime] {
		return scenarioPackError("requires.runtimeCompatibility.runtime is not supported: "+runtime, nil)
	}
	for name, version := range requires.RuntimeCompatibility.Capabilities {
		if version < 0 {
			return scenarioPackError("requires.runtimeCompatibility.capabilities["+name+"] must be >= 0", nil)
		}
	}
	return nil
}

// DecodeScenarioPack strictly decodes a pack: unknown fields are rejected, not
// ignored (doc/276 §8.7 "未知字段拒绝"). Import is the untrusted path, so the
// strict decoder is the default everywhere.
func DecodeScenarioPack(raw []byte) (*ScenarioPack, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var pack ScenarioPack
	if err := decoder.Decode(&pack); err != nil {
		return nil, scenarioPackError("scenario pack is not a valid v3 document", err)
	}
	return &pack, nil
}

// Validate enforces the closed sets and mutual exclusions of §6.4/§7.2.
func (p *ScenarioPack) Validate() error {
	if p == nil {
		return nil
	}
	if p.Schema != ScenarioPackSchemaV3 {
		return scenarioPackError("scenario pack schema must be "+ScenarioPackSchemaV3, nil)
	}
	if _, err := validAppTypes([]string{p.Application.AppType}); err != nil {
		return scenarioPackError("application.appType must be chat, search or agent", nil)
	}
	if strings.TrimSpace(p.Application.Prompt.System) == "" {
		return scenarioPackError("application.prompt.system is required", nil)
	}
	if strings.TrimSpace(p.Business.Role) == "" {
		return scenarioPackError("business.role is required", nil)
	}
	for _, capability := range p.Application.ModelRequirement.Capabilities {
		if !scenarioPackModelCapabilities[strings.TrimSpace(capability)] {
			return scenarioPackError("modelRequirement.capabilities contains an unsupported value: "+capability, nil)
		}
	}
	if p.Knowledge != nil {
		if err := p.Knowledge.Validate(); err != nil {
			return err
		}
	}
	for _, threshold := range []float64{p.Eval.Thresholds.ReturnRate, p.Eval.Thresholds.EvidenceLocatableRate} {
		if threshold < 0 || threshold > 1 {
			return scenarioPackError("eval thresholds must be within [0,1]", nil)
		}
	}
	for i, question := range p.Eval.Questions {
		if strings.TrimSpace(question.Question) == "" {
			return scenarioPackError(fmt.Sprintf("eval.questions[%d].question is required", i), nil)
		}
	}
	requires := p.Requires
	if requires.PackSchema != "" && requires.PackSchema != "v3" {
		return scenarioPackError("requires.packSchema must be v3", nil)
	}
	if runtime := requires.RuntimeCompatibility.Runtime; runtime != "" && !scenarioPackRuntimes[runtime] {
		return scenarioPackError("requires.runtimeCompatibility.runtime is not supported: "+runtime, nil)
	}
	for name, version := range requires.RuntimeCompatibility.Capabilities {
		if version < 0 {
			return scenarioPackError("requires.runtimeCompatibility.capabilities["+name+"] must be >= 0", nil)
		}
	}
	return nil
}

// ProjectPayload turns a pack into the v2 payload shape every legacy consumer
// reads (catalog projection, chat instantiation, release bindings). The pack is
// kept alongside so the render path can use the richer structure, but no stored
// field is ever recomputed from the projection: the projection is a read model.
func (p *ScenarioPack) ProjectPayload() ScenarioTemplatePayload {
	if p == nil {
		return ScenarioTemplatePayload{}
	}
	payload := ScenarioTemplatePayload{
		Description:        p.Business.ExpectedOutcome,
		DatasetSuggestions: packDatasetNames(p.Knowledge),
		SampleQuestions:    append([]string(nil), p.Business.SampleQuestions...),
		PublishChecklist:   append([]string(nil), p.Business.PublishChecklist...),
		Translations:       p.Business.Translations,
		Knowledge:          p.Knowledge,
		Pack:               p,
	}
	for _, question := range p.Eval.Questions {
		payload.EvaluationQuestions = append(payload.EvaluationQuestions, question.Question)
	}
	if profile := strings.TrimSpace(p.Application.ProfileSource); profile != "" {
		if _, ok := scenarioParameterProfiles[profile]; ok {
			payload.ParameterProfileID = profile
		}
	}
	return payload
}

func packDatasetNames(knowledge *ScenarioTemplateKnowledge) []string {
	if knowledge == nil {
		return []string{}
	}
	names := make([]string, 0, len(knowledge.Datasets))
	seen := map[string]bool{}
	for _, dataset := range knowledge.Datasets {
		candidates := dataset.Suggestions
		if name := strings.TrimSpace(dataset.Name); name != "" {
			candidates = append([]string{name}, candidates...)
		}
		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" || seen[candidate] {
				continue
			}
			seen[candidate] = true
			names = append(names, candidate)
		}
	}
	return names
}

// payloadSchemaOf reports which schema a payload must be stamped with. Only
// authored v3 packs get 'v3'; everything else stays 'v2' (doc/276 §7.7).
func payloadSchemaOf(payload ScenarioTemplatePayload) string {
	if payload.Pack != nil {
		return model.TemplatePayloadSchemaV3
	}
	return model.TemplatePayloadSchemaV2
}

// validateTemplatePayload runs the write-path validation shared by create,
// update and import: the knowledge contract plus, for v3, the whole pack.
// validateTemplatePayloadForStatus applies the draft/production split: drafts
// only need to be structurally sound, everything else must be complete.
func validateTemplatePayloadForStatus(payload ScenarioTemplatePayload, draft bool) error {
	if draft && payload.Pack != nil {
		if err := payload.Pack.ValidateDraft(); err != nil {
			return err
		}
		// A draft still has to be renderable for preview, so the runtime
		// extension is validated even when the copy is missing.
		if err := validateCanvasDSL(payload.Pack.RuntimeExtension.RAGFlow); err != nil {
			return err
		}
	} else if err := validateTemplatePayload(payload); err != nil {
		return err
	}
	if payload.Pack != nil {
		if err := scanScenarioContent(payload.Pack.RuntimeExtension, "runtimeExtension"); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplatePayload(payload ScenarioTemplatePayload) error {
	if payload.Pack != nil {
		if err := payload.Pack.Validate(); err != nil {
			return err
		}
		if _, err := RenderScenarioPack(payload.Pack); err != nil {
			return err
		}
		return nil
	}
	return payload.Knowledge.Validate()
}

func scenarioPackError(message string, cause error) error {
	if cause != nil {
		var httpError *httperr.Error
		if errors.As(cause, &httpError) {
			message += ": " + httpError.Message
		} else {
			message += ": " + cause.Error()
		}
	}
	return httperr.BadRequest(40105, message)
}

// UnmarshalJSON makes a v3 pack a first-class payload document: a payload whose
// top-level `schema` is the pack schema is decoded strictly and projected into
// the v2 read fields, so every legacy consumer keeps working without knowing
// about v3 (doc/276 §7.7 "payload_schema 是唯一判别列").
func (p *ScenarioTemplatePayload) UnmarshalJSON(data []byte) error {
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe.Schema == ScenarioPackSchemaV3 {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var pack ScenarioPack
		if err := decoder.Decode(&pack); err != nil {
			return err
		}
		*p = pack.ProjectPayload()
		return nil
	}
	type payloadAlias ScenarioTemplatePayload
	var decoded payloadAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = ScenarioTemplatePayload(decoded)
	return nil
}

// MarshalJSON writes a v3 pack back as the pack document it was authored from,
// and a v2 payload unchanged. Export therefore round-trips byte-shape (field
// order aside), which the import trust gate depends on.
func (p ScenarioTemplatePayload) MarshalJSON() ([]byte, error) {
	if p.Pack != nil {
		return json.Marshal(*p.Pack)
	}
	type payloadAlias ScenarioTemplatePayload
	return json.Marshal(payloadAlias(p))
}
