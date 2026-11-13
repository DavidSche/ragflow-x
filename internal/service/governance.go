package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

const scenarioTemplateSchema = "ragflow-x.scenario-template.v2"

// scenarioTemplateSchemaV1 is still accepted on import so templates exported
// before the v2 payload landed keep round-tripping (doc/228 §3.1 T7.7′).
const scenarioTemplateSchemaV1 = "ragflow-x.scenario-template.v1"

var templateKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]{1,94}$`)

// ScenarioTemplateTranslation carries the localized template copy. English is
// the only shipped translation today; missing entries fall back to the
// canonical (zh) fields and the UI labels the template as needs_review.
type ScenarioTemplateTranslation struct {
	Name                string   `json:"name,omitempty"`
	Description         string   `json:"description,omitempty"`
	DatasetSuggestions  []string `json:"datasetSuggestions,omitempty"`
	SampleQuestions     []string `json:"sampleQuestions,omitempty"`
	EvaluationQuestions []string `json:"evaluationQuestions,omitempty"`
	PermissionAdvice    string   `json:"permissionAdvice,omitempty"`
	PublishChecklist    []string `json:"publishChecklist,omitempty"`
	ExpectedOutcome     string   `json:"expectedOutcome,omitempty"`
}

// ScenarioTemplatePayload is the portable, human-readable template body.
// v2 adds the guide fields the wizard and empty state render, plus localized
// copy; every field is optional so v1 payloads stay readable.
type ScenarioTemplatePayload struct {
	Description         string                                 `json:"description,omitempty"`
	PromptPresetID      string                                 `json:"promptPresetId,omitempty"`
	ParameterProfileID  string                                 `json:"parameterProfileId,omitempty"`
	DatasetSuggestions  []string                               `json:"datasetSuggestions,omitempty"`
	EvaluationQuestions []string                               `json:"evaluationQuestions,omitempty"`
	SampleQuestions     []string                               `json:"sampleQuestions,omitempty"`
	PermissionAdvice    string                                 `json:"permissionAdvice,omitempty"`
	PublishChecklist    []string                               `json:"publishChecklist,omitempty"`
	ExpectedOutcome     string                                 `json:"expectedOutcome,omitempty"`
	Translations        map[string]ScenarioTemplateTranslation `json:"translations,omitempty"`
	// Knowledge is the optional 2.0 knowledge contract (doc/276 §7.2). It stays
	// optional so v1/v2 payloads keep round-tripping; when present it is
	// validated on every write.
	Knowledge *ScenarioTemplateKnowledge `json:"knowledge,omitempty"`
	// Pack marks this payload as an authored v3 Scenario Pack (doc/276 §6.4).
	// It is never set for v2 rows: the version's `payload_schema` column is the
	// discriminator on read, and a v3 document sets it through UnmarshalJSON.
	// Consumers that only understand v2 read the projection fields above, which
	// ProjectPayload fills at decode time.
	Pack *ScenarioPack `json:"-"`
}

// ScenarioTemplateExport is the stable import/export contract.
type ScenarioTemplateExport struct {
	Schema   string                  `json:"schema"`
	Key      string                  `json:"key"`
	Name     string                  `json:"name"`
	AppTypes []string                `json:"appTypes"`
	Payload  ScenarioTemplatePayload `json:"payload"`
	Version  int64                   `json:"version"`
	// PayloadSchema declares which dialect `payload` speaks, so an exported
	// document is self-describing instead of leaving the reader to sniff for a
	// `schema` member (a v2 payload has none by design). It is informational on
	// import: the stored discriminator is still derived from the decoded payload.
	PayloadSchema string `json:"payload_schema,omitempty"`
	// Signature is attached on export when this installation has a signing key
	// configured. Import verifies it for the audit trail but never lets it
	// change the trust level (doc/276 §8.7).
	Signature *ScenarioExportSignature `json:"signature,omitempty"`
}

// ScenarioTemplateInput validates create/update requests.
type ScenarioTemplateInput struct {
	Key         string                  `json:"key"`
	Name        string                  `json:"name"`
	AppTypes    []string                `json:"app_types"`
	Description string                  `json:"description"`
	Status      string                  `json:"status"`
	Payload     ScenarioTemplatePayload `json:"payload"`
	ChangeNote  string                  `json:"change_note"`
}

// ScenarioTemplateInstantiationInput controls how a published template becomes
// a tenant-owned draft chat assistant.
type ScenarioTemplateInstantiationInput struct {
	Name                  string        `json:"name"`
	DatasetIDs            []string      `json:"dataset_ids"`
	DatasetSuggestions    []string      `json:"dataset_suggestions"`
	CreateMissingDatasets bool          `json:"create_missing_datasets"`
	TemplateVersion       int64         `json:"template_version"`
	Authoring             ChatAuthoring `json:"authoring"`
}

// ScenarioTemplateInstantiationResult reports both the instantiated assistant
// and the datasets created while resolving template suggestions.
type ScenarioTemplateInstantiationResult struct {
	Chat              *model.ChatShadow            `json:"chat"`
	Template          *model.ScenarioTemplateAsset `json:"template"`
	TemplateVersion   int64                        `json:"template_version"`
	CreatedDatasetIDs []string                     `json:"created_dataset_ids"`
}

// PromptPolicyInput validates prompt and parameter defaults.
type PromptPolicyInput struct {
	Scope     string          `json:"scope"`
	ObjectID  string          `json:"object_id"`
	PresetID  string          `json:"preset_id"`
	ProfileID string          `json:"profile_id"`
	Payload   json.RawMessage `json:"payload"`
}

// DatasetLifecycleInput updates the governance metadata of a dataset.
type DatasetLifecycleInput struct {
	OwnerID        string     `json:"owner_id"`
	OwnerTeamID    string     `json:"owner_team_id"`
	SourceType     string     `json:"source_type"`
	BusinessDomain string     `json:"business_domain"`
	Sensitivity    string     `json:"sensitivity"`
	EffectiveAt    *time.Time `json:"effective_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastReviewedAt *time.Time `json:"last_reviewed_at"`
	ReviewStatus   string     `json:"review_status"`
	QualityScore   int        `json:"quality_score"`
}

// DatasetLifecycleView adds the derived lifecycle status for UI display.
type DatasetLifecycleView struct {
	*model.DatasetLink
	LifecycleStatus string `json:"lifecycle_status"`
}

// EvalCaseInput creates or replaces a replayable evaluation case.
type EvalCaseInput struct {
	Question               string `json:"question"`
	ExpectedAnswer         string `json:"expected_answer"`
	ExpectedKeywords       string `json:"expected_keywords"`
	ExpectedCitationDocIDs string `json:"expected_citation_doc_ids"`
	Source                 string `json:"source"`
	Status                 string `json:"status"`
}

// EvalSetInput creates or replaces an evaluation suite.
type EvalSetInput struct {
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	AppType            string          `json:"app_type"`
	AppID              string          `json:"app_id"`
	DatasetIDs         []string        `json:"dataset_ids"`
	Status             string          `json:"status"`
	Cases              []EvalCaseInput `json:"cases"`
	ScenarioTemplateID string          `json:"scenario_template_id"`
}

// GovernanceSummary is used by the asset governance board.
type GovernanceSummary struct {
	Templates      int64 `json:"templates"`
	PromptPolicies int64 `json:"prompt_policies"`
	Datasets       int64 `json:"datasets"`
	MissingOwner   int64 `json:"missing_owner"`
	DueDatasets    int64 `json:"due_datasets"`
	Expired        int64 `json:"expired_datasets"`
	EvalSets       int64 `json:"eval_sets"`
}

// KnowledgeHealthSummary is the stage-3 health contract. Its rates have
// explicit numerators and denominators so a blank value is distinguishable
// from zero readiness and quality.
type KnowledgeHealthSummary struct {
	DatasetCount          int64 `json:"dataset_count"`
	LifecycleStateCounts  `json:"lifecycle"`
	ParseHealth           `json:"parse"`
	AverageQualityScore   float64 `json:"average_quality_score"`
	LowQualityCount       int64   `json:"low_quality_count"`
	MissingClassification int64   `json:"missing_classification"`
	CitationMissingCount  int64   `json:"citation_missing_count"`
	CitationWindowDays    int     `json:"citation_window_days"`
}

type LifecycleStateCounts struct {
	Current      int64 `json:"current"`
	Due          int64 `json:"due"`
	Expired      int64 `json:"expired"`
	MissingOwner int64 `json:"missing_owner"`
	Unreviewed   int64 `json:"unreviewed"`
}

type ParseHealth struct {
	TaskCount int64   `json:"task_count"`
	Done      int64   `json:"done"`
	Running   int64   `json:"running"`
	Queued    int64   `json:"queued"`
	Failed    int64   `json:"failed"`
	Stopped   int64   `json:"stopped"`
	ReadyRate float64 `json:"ready_rate"`
}

func validAppTypes(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		if value != "chat" && value != "search" && value != "agent" {
			return nil, httperr.BadRequest(40041, "app type must be chat, search or agent")
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

func normalizeStatus(value, fallback string, allowed ...string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	value = strings.ToLower(strings.TrimSpace(value))
	for _, item := range allowed {
		if value == item {
			return value, nil
		}
	}
	return "", httperr.BadRequest(40042, "invalid status")
}

func marshalJSON(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", httperr.BadRequest(40043, "invalid payload")
	}
	return string(data), nil
}

func validPayloadJSON(value json.RawMessage, code int, message string) (string, error) {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		trimmed = "{}"
	}
	var body interface{}
	if err := json.Unmarshal([]byte(trimmed), &body); err != nil {
		return "", httperr.BadRequest(code, message)
	}
	return trimmed, nil
}

func parseTemplatePayload(raw string) (ScenarioTemplatePayload, error) {
	var payload ScenarioTemplatePayload
	if strings.TrimSpace(raw) == "" {
		return payload, nil
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return payload, httperr.BadRequest(40044, "template payload is invalid")
	}
	return payload, nil
}

// CreateScenarioTemplate stores a tenant-owned template with an immutable v1.
func (s *Service) CreateScenarioTemplate(ctx context.Context, tenantID, userID string, input ScenarioTemplateInput) (*model.ScenarioTemplateAsset, error) {
	key := strings.ToLower(strings.TrimSpace(input.Key))
	name := strings.TrimSpace(input.Name)
	if !templateKeyPattern.MatchString(key) {
		return nil, httperr.BadRequest(40045, "key must be 2-95 lowercase letters, numbers, dash or underscore")
	}
	if name == "" {
		return nil, httperr.BadRequest(40046, "name is required")
	}
	appTypes, err := validAppTypes(input.AppTypes)
	if err != nil {
		return nil, err
	}
	status, err := normalizeStatus(input.Status, model.AssetStatusDraft, model.AssetStatusDraft, model.AssetStatusPublished)
	if err != nil {
		return nil, err
	}
	if existing, err := s.Store.GetScenarioTemplateByKey(ctx, tenantID, key); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, httperr.New(409, 40901, "template key already exists")
	}
	if err := validateTemplatePayloadForStatus(input.Payload, status == model.AssetStatusDraft); err != nil {
		return nil, err
	}
	payload, err := marshalJSON(input.Payload)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	asset := &model.ScenarioTemplateAsset{
		ID: id.New(), TenantID: tenantID, Key: key, Name: name,
		AppTypes: strings.Join(appTypes, ","), Description: strings.TrimSpace(input.Description),
		LatestVersion: 1, Source: model.TemplateSourceCustom, Status: status,
		PayloadJSON: payload, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	version := &model.ScenarioTemplateVersion{
		ID: id.New(), TenantID: tenantID, TemplateID: asset.ID, Version: 1,
		PayloadJSON: payload, ChangeNote: strings.TrimSpace(input.ChangeNote), CreatedBy: userID, CreatedAt: now,
		PayloadSchema: payloadSchemaOf(input.Payload), SnapshotProvenance: model.SnapshotAuthored,
	}
	if err := s.Store.CreateScenarioTemplate(ctx, asset, version); err != nil {
		return nil, err
	}
	return asset, nil
}

// UpdateScenarioTemplate appends a new immutable version and advances the pointer.
func (s *Service) UpdateScenarioTemplate(ctx context.Context, tenantID, userID, templateID string, input ScenarioTemplateInput) (*model.ScenarioTemplateAsset, error) {
	asset, err := s.Store.GetScenarioTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, httperr.NotFound("scenario template not found")
	}
	name := strings.TrimSpace(input.Name)
	if name != "" {
		asset.Name = name
	}
	if len(input.AppTypes) > 0 {
		appTypes, err := validAppTypes(input.AppTypes)
		if err != nil {
			return nil, err
		}
		asset.AppTypes = strings.Join(appTypes, ",")
	}
	if strings.TrimSpace(input.Description) != "" {
		asset.Description = strings.TrimSpace(input.Description)
	}
	status, err := normalizeStatus(input.Status, asset.Status, model.AssetStatusDraft, model.AssetStatusPublished, model.AssetStatusArchived)
	if err != nil {
		return nil, err
	}
	// An update is a content write. Accepting an empty payload would let a PUT
	// with only `status` silently replace the stored content with nothing, which
	// is data loss dressed up as a publish (doc/276 §7.7 history is immutable,
	// so a destructive write must be impossible by construction).
	if isZeroTemplatePayload(input.Payload) {
		return nil, httperr.BadRequest(40085, "payload is required when updating a scenario template")
	}
	if err := validateTemplatePayloadForStatus(input.Payload, status != model.AssetStatusPublished); err != nil {
		return nil, err
	}
	payloadJSON, err := marshalJSON(input.Payload)
	if err != nil {
		return nil, err
	}
	// P2 quality gate (doc/276 §9): a pack that declares `requireEvalPass` may
	// only go live with evidence. A content change necessarily introduces a new
	// version that no evaluation run can cover yet, so the gate applies to the
	// status-only publish of already-evaluated content; new content goes through
	// the release-candidate flow.
	if status == model.AssetStatusPublished {
		if err := s.ensurePublishablePayload(ctx, tenantID, asset, payloadJSON); err != nil {
			return nil, err
		}
	}
	nextVersion := asset.LatestVersion + 1
	asset.LatestVersion = nextVersion
	asset.Status = status
	asset.PayloadJSON = payloadJSON
	asset.UpdatedAt = time.Now().UTC()
	version := &model.ScenarioTemplateVersion{
		ID: id.New(), TenantID: tenantID, TemplateID: asset.ID, Version: nextVersion,
		PayloadJSON: payloadJSON, ChangeNote: strings.TrimSpace(input.ChangeNote), CreatedBy: userID, CreatedAt: asset.UpdatedAt,
		PayloadSchema: payloadSchemaOf(input.Payload), SnapshotProvenance: model.SnapshotAuthored,
	}
	if err := s.Store.UpdateScenarioTemplate(ctx, asset, version); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, httperr.NotFound("scenario template not found")
		}
		return nil, err
	}
	return asset, nil
}

// isZeroTemplatePayload reports whether a payload carries no content at all.
func isZeroTemplatePayload(payload ScenarioTemplatePayload) bool {
	if payload.Pack != nil || payload.Knowledge != nil {
		return false
	}
	return payload.Description == "" && payload.PromptPresetID == "" && payload.ParameterProfileID == "" &&
		len(payload.DatasetSuggestions) == 0 && len(payload.SampleQuestions) == 0 &&
		len(payload.EvaluationQuestions) == 0 && len(payload.Translations) == 0 &&
		payload.PermissionAdvice == "" && payload.ExpectedOutcome == "" &&
		len(payload.PublishChecklist) == 0
}

// GetScenarioTemplate returns the pointer plus selected or full version history.
func (s *Service) GetScenarioTemplate(ctx context.Context, tenantID, templateID string, version int64, withVersions bool) (*model.ScenarioTemplateAsset, *model.ScenarioTemplateVersion, []model.ScenarioTemplateVersion, error) {
	// Read paths fall back to the platform baseline (doc/276 §7.1 step 3): the
	// baseline is shared read-only content, so a workspace may read/instantiate
	// it by its platform id. Rows owned by another workspace still 404.
	asset, err := s.ResolveScenarioTemplateAsset(ctx, tenantID, templateID)
	if err != nil || asset == nil {
		return nil, nil, nil, err
	}
	ownerTenant := asset.TenantID
	if version <= 0 {
		version = asset.LatestVersion
	}
	selected, err := s.Store.GetScenarioTemplateVersion(ctx, ownerTenant, templateID, version)
	if err != nil {
		return nil, nil, nil, err
	}
	if selected == nil {
		return nil, nil, nil, httperr.NotFound("scenario template version not found")
	}
	var versions []model.ScenarioTemplateVersion
	if withVersions {
		versions, err = s.Store.ListScenarioTemplateVersions(ctx, ownerTenant, templateID)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return asset, selected, versions, nil
}

// ListScenarioTemplates enforces tenant isolation except explicit platform scope.
func (s *Service) ListScenarioTemplates(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter repository.GovernanceFilter) ([]model.ScenarioTemplateAsset, int64, error) {
	if scopeAll {
		return s.Store.ListAllScenarioTemplates(ctx, page, pageSize, filter)
	}
	return s.Store.ListScenarioTemplates(ctx, tenantID, page, pageSize, filter)
}

// declaredPayloadSchema prefers the stored discriminator and falls back to the
// decoded payload, so an export never claims v2 for a document carrying a pack.
func declaredPayloadSchema(stored string, payload ScenarioTemplatePayload) string {
	if payload.Pack != nil {
		return model.TemplatePayloadSchemaV3
	}
	if stored == "" {
		return model.TemplatePayloadSchemaV2
	}
	return stored
}

// ExportScenarioTemplate returns the stable JSON contract.
func (s *Service) ExportScenarioTemplate(ctx context.Context, tenantID, templateID string, version int64) (*ScenarioTemplateExport, error) {
	asset, selected, _, err := s.GetScenarioTemplate(ctx, tenantID, templateID, version, false)
	if err != nil {
		return nil, err
	}
	payload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		return nil, err
	}
	appTypes := []string{}
	if strings.TrimSpace(asset.AppTypes) != "" {
		appTypes = strings.Split(asset.AppTypes, ",")
	}
	export := &ScenarioTemplateExport{
		Schema: scenarioTemplateSchema, Key: asset.Key, Name: asset.Name,
		AppTypes: appTypes, Payload: payload, Version: selected.Version,
		PayloadSchema: declaredPayloadSchema(selected.PayloadSchema, payload),
	}
	// Sign the exact bytes the client will store. The signature covers the
	// document without its own field, so signing must happen on a copy.
	unsigned, err := marshalJSON(export)
	if err != nil {
		return nil, err
	}
	export.Signature = s.signScenarioPackDocument([]byte(unsigned))
	return export, nil
}

// ImportScenarioTemplate validates the export contract and stores a new asset.
// The document is untrusted input (doc/276 §8.7): it is scanned for credentials
// and external endpoints, strictly decoded, compatibility-gated against the
// running engine, and always stored as a DRAFT with source=imported. The
// caller's requested status is ignored on purpose — an imported template must
// be reviewed and published explicitly.
func (s *Service) ImportScenarioTemplate(ctx context.Context, tenantID, userID string, export ScenarioTemplateExport) (*model.ScenarioTemplateAsset, error) {
	document, err := marshalJSON(export)
	if err != nil {
		return nil, err
	}
	return s.ImportScenarioTemplateDocument(ctx, tenantID, userID, []byte(document))
}

// ImportScenarioTemplateDocument is the raw-document form used by the HTTP
// handler: strict decoding must see the original JSON, otherwise unknown and
// forbidden fields would be silently dropped before the trust scan.
func (s *Service) ImportScenarioTemplateDocument(ctx context.Context, tenantID, userID string, document []byte) (*model.ScenarioTemplateAsset, error) {
	if err := ValidateScenarioImportTrust(document); err != nil {
		return nil, err
	}
	var export ScenarioTemplateExport
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&export); err != nil {
		return nil, httperr.BadRequest(40062, "invalid scenario template export payload")
	}
	// Signature verification is a tamper check, not a trust upgrade: a valid
	// signature is recorded, an invalid one rejects the document, and the asset
	// still lands as an `imported` draft either way.
	signatureVerified, err := s.verifyExportSignature(document, export.Signature)
	if err != nil {
		return nil, err
	}
	if export.Schema != scenarioTemplateSchema && export.Schema != scenarioTemplateSchemaV1 && export.Schema != ScenarioPackSchemaV3 {
		return nil, httperr.BadRequest(40047, "unsupported scenario template schema")
	}
	// `payload_schema` is informational, but a document that claims v3 while its
	// payload has no pack is internally inconsistent; rejecting it keeps the
	// "v3 means pack" invariant honest instead of silently storing v2.
	if export.PayloadSchema != "" && export.PayloadSchema != model.TemplatePayloadSchemaV2 &&
		export.PayloadSchema != model.TemplatePayloadSchemaV3 {
		return nil, httperr.BadRequest(40047, "unsupported scenario template payload_schema")
	}
	if export.PayloadSchema == model.TemplatePayloadSchemaV3 && export.Payload.Pack == nil {
		return nil, httperr.BadRequest(40062, "payload_schema is v3 but the payload carries no scenario pack")
	}
	if err := s.checkPackCompatibility(ctx, export.Payload); err != nil {
		return nil, err
	}
	input := ScenarioTemplateInput{
		Key: export.Key, Name: export.Name, AppTypes: export.AppTypes, Payload: export.Payload,
		Status: model.AssetStatusDraft, ChangeNote: "imported from JSON",
	}
	asset, err := s.CreateScenarioTemplate(ctx, tenantID, userID, input)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SetScenarioTemplateSource(ctx, tenantID, asset.ID, model.TemplateSourceImport); err != nil {
		return nil, err
	}
	asset.Source = model.TemplateSourceImport
	s.recordScenarioImportProvenance(ctx, tenantID, asset.ID, signatureVerified)
	return asset, nil
}

// verifyExportSignature re-serializes the document without its signature field
// and compares the HMAC. Serialization must be stable, so the check hashes the
// received bytes with the signature member removed by re-marshalling the decoded
// struct — the same shape the signer produced (see ExportScenarioTemplate).
func (s *Service) verifyExportSignature(document []byte, signature *ScenarioExportSignature) (bool, error) {
	if signature == nil {
		return false, nil
	}
	var export ScenarioTemplateExport
	if err := json.Unmarshal(document, &export); err != nil {
		return false, httperr.BadRequest(40062, "invalid scenario template export payload")
	}
	export.Signature = nil
	unsigned, err := marshalJSON(export)
	if err != nil {
		return false, err
	}
	return s.VerifyScenarioImportSignature([]byte(unsigned), signature)
}

// recordScenarioImportProvenance stores the signature verdict as an audit note
// so a reviewer can tell "signed by another rgx installation" apart from "plain
// document". It never changes source or status.
func (s *Service) recordScenarioImportProvenance(ctx context.Context, tenantID, templateID string, signatureVerified bool) {
	verdict := `{"signature":"unsigned"}`
	if signatureVerified {
		verdict = `{"signature":"signature_verified"}`
	}
	s.recordTemplateAudit(ctx, tenantID, "system", "scenario_template.import_provenance", templateID, verdict)
}

// checkPackCompatibility is the P2 compatibility gate (doc/276 §9): a pack that
// pins a runtime baseline or capability level this deployment cannot satisfy is
// rejected with 40914 instead of failing later at run time.
func (s *Service) checkPackCompatibility(ctx context.Context, payload ScenarioTemplatePayload) error {
	if payload.Pack == nil {
		return nil
	}
	requires := payload.Pack.Requires.RuntimeCompatibility
	if requires.Runtime == "" {
		return nil
	}
	if requires.Runtime != "ragflow" {
		return httperr.New(409, 40914, "scenario pack requires an unsupported runtime: "+requires.Runtime)
	}
	if requires.Baseline != "" {
		version, err := s.RAGFlow.EngineVersion(ctx)
		if err != nil {
			// An unreadable engine version is not a compatibility verdict; the
			// import stays allowed and the hash/version is recorded for audit.
			return nil
		}
		if !engineBaselineSatisfies(version, requires.Baseline) {
			return httperr.New(409, 40914, "scenario pack requires ragflow "+requires.Baseline+", engine is "+version)
		}
	}
	for capability, minimum := range requires.Capabilities {
		if minimum <= 0 {
			continue
		}
		if !s.engineCapabilityConfigured(capability) {
			return httperr.New(409, 40914, "scenario pack requires engine capability "+capability+" which this build does not expose")
		}
	}
	return nil
}

// engineBaselineSatisfies compares dotted versions numerically from the left.
// A pack baseline of "1.0.0" is satisfied by "1.0.0" and "1.0.1" but not by
// "0.27.2"; an unparsable pair is treated as satisfied so a malformed upstream
// version string cannot block an otherwise valid import.
func engineBaselineSatisfies(engineVersion, baseline string) bool {
	engineParts, ok := numericVersionParts(engineVersion)
	if !ok {
		return true
	}
	baselineParts, ok := numericVersionParts(baseline)
	if !ok {
		return true
	}
	for i := 0; i < len(baselineParts); i++ {
		left := 0
		if i < len(engineParts) {
			left = engineParts[i]
		}
		if left != baselineParts[i] {
			return left > baselineParts[i]
		}
	}
	return true
}

func numericVersionParts(value string) ([]int, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	if value == "" {
		return nil, false
	}
	segments := strings.Split(value, ".")
	parts := make([]int, 0, len(segments))
	for _, segment := range segments {
		digits := strings.Builder{}
		for _, char := range segment {
			if char >= '0' && char <= '9' {
				digits.WriteRune(char)
				continue
			}
			break
		}
		if digits.Len() == 0 {
			return nil, false
		}
		number, err := strconv.Atoi(digits.String())
		if err != nil {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, true
}

// engineCapabilityConfigured answers "does this build expose this engine
// capability at all" without calling the engine: it is the Capability Matrix
// declaration, which is the build's contract surface (doc/276 §9).
func (s *Service) engineCapabilityConfigured(name string) bool {
	report := s.ListRAGFlowCapabilities()
	for _, item := range report.Items {
		if item.Name == name {
			return true
		}
	}
	return false
}

// CopyScenarioTemplate lets a platform admin replicate an asset across tenants.
func (s *Service) CopyScenarioTemplate(ctx context.Context, actorID, tenantID, templateID, targetTenantID string) (*model.ScenarioTemplateAsset, error) {
	if strings.TrimSpace(targetTenantID) == "" {
		return nil, httperr.BadRequest(40048, "target tenant is required")
	}
	target, err := s.Store.GetTenant(ctx, targetTenantID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, httperr.NotFound("target tenant not found")
	}
	if err := s.AuthorizeABAC(ctx, actorID, "manage", "scenario-template", tenantID, "", ""); err != nil {
		return nil, err
	}
	if err := s.Authorize(ctx, actorID, "governance.manage", "tenant"); err != nil {
		return nil, err
	}
	if err := s.EnsureTenantActive(ctx, targetTenantID); err != nil {
		return nil, err
	}
	export, err := s.ExportScenarioTemplate(ctx, tenantID, templateID, 0)
	if err != nil {
		return nil, err
	}
	export.Key = strings.TrimSuffix(export.Key, "-copy") + "-copy-" + time.Now().UTC().Format("0102150405")
	input := ScenarioTemplateInput{Key: export.Key, Name: export.Name, AppTypes: export.AppTypes, Payload: export.Payload, ChangeNote: "copied from another tenant"}
	asset, err := s.CreateScenarioTemplate(ctx, targetTenantID, "", input)
	if err != nil {
		return nil, err
	}
	// G13 (doc/276 §7.1 step 6): the source flip must be keyed by the *target*
	// tenant. Keying it by the source tenant matched zero rows, the repository
	// swallowed the mismatch, and the response claimed `copy` while the row
	// stayed `custom`. The repository now also fails on zero affected rows.
	if err := s.Store.SetScenarioTemplateSource(ctx, targetTenantID, asset.ID, model.TemplateSourceCopy); err != nil {
		return nil, err
	}
	asset.Source = model.TemplateSourceCopy
	return asset, nil
}

// ArchiveScenarioTemplate is a soft delete; history stays immutable.
func (s *Service) ArchiveScenarioTemplate(ctx context.Context, tenantID, templateID string) error {
	asset, err := s.Store.GetScenarioTemplate(ctx, tenantID, templateID)
	if err != nil {
		return err
	}
	if asset == nil {
		return httperr.NotFound("scenario template not found")
	}
	asset.Status = model.AssetStatusArchived
	return s.Store.UpdateScenarioTemplateStatus(ctx, tenantID, templateID, asset.Status)
}

// EnsureScenarioTemplateEvalSet reuses the regression suite bound to a
// scenario template or creates one from its immutable evaluation questions.
func (s *Service) EnsureScenarioTemplateEvalSet(
	ctx context.Context, tenantID, userID, templateID, name string,
) (*model.EvalSet, bool, error) {
	_, selected, _, err := s.GetScenarioTemplate(ctx, tenantID, templateID, 0, false)
	if err != nil {
		return nil, false, err
	}
	payload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		return nil, false, err
	}
	if len(payload.EvaluationQuestions) == 0 {
		return nil, false, httperr.BadRequest(40049, "template has no evaluation questions")
	}
	existing, _, err := s.Store.ListEvalSets(ctx, tenantID, 1, 1, repository.GovernanceFilter{
		ScenarioTemplateID: templateID,
	})
	if err != nil {
		return nil, false, err
	}
	if len(existing) > 0 {
		evalSet := &existing[0]
		cases, err := s.Store.ListEvalCases(ctx, tenantID, evalSet.ID)
		if err != nil {
			return nil, false, err
		}
		expectedQuestions := make([]string, 0, len(payload.EvaluationQuestions))
		for _, question := range payload.EvaluationQuestions {
			question = strings.TrimSpace(question)
			if question != "" {
				expectedQuestions = append(expectedQuestions, question)
			}
		}
		current := make(map[string]bool, len(cases))
		for _, item := range cases {
			current[item.Question] = true
		}
		missing := false
		for _, question := range expectedQuestions {
			if !current[question] {
				missing = true
				break
			}
		}
		if missing || len(current) != len(expectedQuestions) {
			datasetIDs := []string{}
			if strings.TrimSpace(evalSet.DatasetIDs) != "" {
				datasetIDs = strings.Split(evalSet.DatasetIDs, ",")
			}
			input := EvalSetInput{
				Name: evalSet.Name, Description: evalSet.Description, AppType: evalSet.AppType,
				AppID: evalSet.AppID, Status: evalSet.Status, DatasetIDs: datasetIDs,
			}
			for _, question := range expectedQuestions {
				input.Cases = append(input.Cases, EvalCaseInput{Question: question, Source: model.EvalSourceTemplate})
			}
			evalSet, err = s.UpdateEvalSet(ctx, tenantID, userID, evalSet.ID, input)
			if err != nil {
				return nil, false, err
			}
		}
		return evalSet, false, nil
	}
	evalSet, err := s.CreateEvalSetFromTemplate(ctx, tenantID, userID, templateID, name)
	if err != nil {
		return nil, false, err
	}
	return evalSet, true, nil
}

// PublishScenarioTemplateEvalSetVersion snapshots the actual template suite
// and its cases into the immutable identities required by evaluation runs.
func (s *Service) PublishScenarioTemplateEvalSetVersion(
	ctx context.Context, tenantID, userID, templateID, evalSetName, changeSummary string,
) (*model.EvalSet, *model.EvaluationSetVersion, bool, bool, error) {
	evalSet, _, err := s.EnsureScenarioTemplateEvalSet(
		ctx, tenantID, userID, templateID, evalSetName,
	)
	if err != nil {
		return nil, nil, false, false, err
	}
	cases, err := s.Store.ListEvalCases(ctx, tenantID, evalSet.ID)
	if err != nil {
		return nil, nil, false, false, err
	}
	if len(cases) == 0 {
		return nil, nil, false, false, httperr.BadRequest(40058, "at least one evaluation case is required")
	}
	now := time.Now().UTC()
	caseVersions := make([]model.EvaluationCaseVersion, 0, len(cases))
	caseSnapshots := make([]map[string]any, 0, len(cases))
	for _, item := range cases {
		snapshot := map[string]any{
			"case_id": item.ID, "case_version": int64(1), "question": item.Question,
			"expected_answer": item.ExpectedAnswer, "expected_keywords": item.ExpectedKeywords,
			"expected_citation_doc_ids": item.ExpectedCitationDocIDs, "metadata": json.RawMessage("{}"),
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return nil, nil, false, false, httperr.BadRequest(40072, "evaluation case snapshot cannot be serialized")
		}
		hash, err := canonicalHash(raw)
		if err != nil {
			return nil, nil, false, false, err
		}
		caseVersions = append(caseVersions, model.EvaluationCaseVersion{
			ID: item.ID, TenantID: tenantID, EvalSetID: evalSet.ID, EvalSetVersion: evalSet.Version,
			CaseID: item.ID, CaseVersion: 1, Hash: hash, Question: item.Question,
			ExpectedAnswer: item.ExpectedAnswer, ExpectedKeywords: item.ExpectedKeywords,
			ExpectedCitationDocIDs: item.ExpectedCitationDocIDs, Metadata: "{}",
			CreatedBy: userID, CreatedAt: now,
		})
		caseSnapshots = append(caseSnapshots, snapshot)
	}
	casesJSON, err := json.Marshal(caseSnapshots)
	if err != nil {
		return nil, nil, false, false, httperr.BadRequest(40072, "evaluation set snapshot cannot be serialized")
	}
	setHash, err := canonicalHash(casesJSON)
	if err != nil {
		return nil, nil, false, false, err
	}
	existing, err := s.Store.GetEvaluationSetVersion(ctx, tenantID, evalSet.ID, evalSet.Version)
	if err != nil {
		return nil, nil, false, false, err
	}
	if existing != nil {
		if existing.Hash != setHash {
			return nil, nil, false, false, httperr.New(409, 40984, "evaluation set version already exists")
		}
		existingCases, err := s.Store.ListEvaluationCaseVersions(ctx, tenantID, evalSet.ID, evalSet.Version)
		if err != nil {
			return nil, nil, false, false, err
		}
		if len(existingCases) != len(caseVersions) {
			return nil, nil, false, false, httperr.New(409, 40984, "evaluation set version case snapshot is incomplete")
		}
		existingByCaseID := make(map[string]model.EvaluationCaseVersion, len(existingCases))
		for _, item := range existingCases {
			existingByCaseID[item.CaseID] = item
		}
		for _, item := range caseVersions {
			versioned, ok := existingByCaseID[item.CaseID]
			if !ok || versioned.Hash != item.Hash || versioned.CaseVersion != item.CaseVersion {
				return nil, nil, false, false, httperr.New(409, 40984, "evaluation set version case snapshot mismatch")
			}
		}
		return evalSet, existing, false, false, nil
	}
	version := &model.EvaluationSetVersion{
		ID: id.New(), TenantID: tenantID, EvalSetID: evalSet.ID, Version: evalSet.Version,
		Hash: setHash, CasesSnapshotHash: setHash, Status: model.EvalStatusPublished,
		ChangeSummary: changeSummary, CreatedBy: userID, CreatedAt: now,
	}
	if err := s.Store.CreateEvaluationSetVersionWithCases(ctx, version, caseVersions); err != nil {
		return nil, nil, false, false, httperr.New(409, 40984, "evaluation set version already exists")
	}
	return evalSet, version, true, true, nil
}

// CreateEvalSetFromTemplate builds a regression suite from template questions.
func (s *Service) CreateEvalSetFromTemplate(ctx context.Context, tenantID, userID, templateID, name string) (*model.EvalSet, error) {
	asset, selected, _, err := s.GetScenarioTemplate(ctx, tenantID, templateID, 0, false)
	if err != nil {
		return nil, err
	}
	payload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		return nil, err
	}
	if len(payload.EvaluationQuestions) == 0 {
		return nil, httperr.BadRequest(40049, "template has no evaluation questions")
	}
	appTypes := []string{}
	if strings.TrimSpace(asset.AppTypes) != "" {
		appTypes = strings.Split(asset.AppTypes, ",")
	}
	input := EvalSetInput{
		Name: name, Description: "Generated from " + asset.Name, AppType: firstString(appTypes, "chat"),
		Status: model.EvalStatusDraft, ScenarioTemplateID: asset.ID,
	}
	for _, question := range payload.EvaluationQuestions {
		question = strings.TrimSpace(question)
		if question != "" {
			input.Cases = append(input.Cases, EvalCaseInput{Question: question, Source: model.EvalSourceTemplate})
		}
	}
	return s.CreateEvalSet(ctx, tenantID, userID, input)
}

// GenerateMissingTemplateEvalSets creates one regression suite for each
// published template that does not already have one. Re-running is idempotent.
func (s *Service) GenerateMissingTemplateEvalSets(ctx context.Context, tenantID, userID string) (int, error) {
	templates, _, err := s.Store.ListScenarioTemplates(ctx, tenantID, 1, 100, repository.GovernanceFilter{
		Status: model.AssetStatusPublished,
	})
	if err != nil {
		return 0, err
	}
	evalSets, _, err := s.Store.ListEvalSets(ctx, tenantID, 1, 100, repository.GovernanceFilter{})
	if err != nil {
		return 0, err
	}
	existing := make(map[string]bool, len(evalSets))
	for _, evalSet := range evalSets {
		if evalSet.ScenarioTemplateID != "" {
			existing[evalSet.ScenarioTemplateID] = true
		}
	}
	created := 0
	for _, template := range templates {
		if template.Status != model.AssetStatusPublished || existing[template.ID] {
			continue
		}
		// Builtin templates are seeded into every tenant (doc/228 §3.1 T7.0).
		// Bulk-provisioning suites for them would drop nine eval sets into every
		// workspace; their suites are published lazily when a trial run or the
		// wizard needs one instead.
		if template.Source == model.TemplateSourceBuiltin {
			continue
		}
		payload, err := parseTemplatePayload(template.PayloadJSON)
		if err != nil {
			return created, err
		}
		if len(payload.EvaluationQuestions) == 0 {
			continue
		}
		if _, err := s.CreateEvalSetFromTemplate(ctx, tenantID, userID, template.ID, template.Name+"评测集"); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

// CreateChatFromScenarioTemplate instantiates a published chat template as an
// editable tenant-owned assistant. Dataset suggestions are names by default;
// explicit IDs remain useful when a workspace wants deterministic bindings.
func (s *Service) CreateChatFromScenarioTemplate(
	ctx context.Context, tenantID, userID, templateID string, input ScenarioTemplateInstantiationInput,
) (*ScenarioTemplateInstantiationResult, error) {
	asset, selected, templatePayload, err := s.publishedChatTemplateForInstantiation(ctx, tenantID, templateID, input.TemplateVersion)
	if err != nil {
		return nil, err
	}
	payload := templatePayload

	datasetIDs := make([]string, 0, len(input.DatasetIDs))
	for _, datasetID := range input.DatasetIDs {
		datasetID = strings.TrimSpace(datasetID)
		if datasetID == "" {
			continue
		}
		link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
		if err != nil {
			return nil, err
		}
		if link == nil {
			return nil, httperr.BadRequest(40085, "dataset not found in workspace: "+datasetID)
		}
		datasetIDs = append(datasetIDs, datasetID)
	}

	createdDatasetIDs := make([]string, 0)
	if len(input.DatasetIDs) == 0 {
		missing := make([]string, 0)
		for _, suggestion := range payload.DatasetSuggestions {
			name := strings.TrimSpace(suggestion)
			if name == "" {
				continue
			}
			link, err := s.Store.GetDatasetLinkByName(ctx, tenantID, name, "")
			if err != nil {
				return nil, err
			}
			if link != nil {
				datasetIDs = append(datasetIDs, link.ID)
				continue
			}
			if !input.CreateMissingDatasets {
				missing = append(missing, name)
				continue
			}
			dataset, err := s.CreateDataset(ctx, tenantID, name)
			if err != nil {
				return nil, err
			}
			createdDatasetIDs = append(createdDatasetIDs, dataset.ID)
			datasetIDs = append(datasetIDs, dataset.ID)
		}
		if len(missing) > 0 {
			return nil, httperr.BadRequest(40085, "dataset suggestions not found: "+strings.Join(missing, ","))
		}
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = asset.Name + " 草稿"
	}
	authoring := mergeTemplateAuthoringForVersion(payload, input.Authoring, selected.PayloadSchema, selected.SnapshotProvenance)
	chat, err := s.CreateChatWithConfig(ctx, tenantID, name, datasetIDs, authoring)
	if err != nil {
		if len(createdDatasetIDs) > 0 {
			if deleteErr := s.DeleteDatasets(ctx, tenantID, createdDatasetIDs); deleteErr != nil {
				logger.Warn("failed to roll back datasets after chat instantiation failure", "error", deleteErr)
			}
		}
		return nil, err
	}
	// Provenance: a chat created through the wizard must be traceable to the
	// template version it was instantiated from (doc/228 §0.2 correction 3).
	chat.ScenarioTemplateID = asset.ID
	chat.ScenarioTemplateVersion = selected.Version
	if err := s.Store.SetChatScenarioTemplate(ctx, tenantID, chat.ID, asset.ID, selected.Version); err != nil {
		return nil, err
	}
	return &ScenarioTemplateInstantiationResult{
		Chat: chat, Template: asset, TemplateVersion: selected.Version,
		CreatedDatasetIDs: createdDatasetIDs,
	}, nil
}

// PrepareScenarioTemplateInstantiation validates the latest (or pinned) template
// and returns a side-effect-free chat.create approval payload.
func (s *Service) PrepareScenarioTemplateInstantiation(
	ctx context.Context, tenantID, templateID string, input ScenarioTemplateInstantiationInput,
) (map[string]any, error) {
	asset, selected, templatePayload, err := s.publishedChatTemplateForInstantiation(ctx, tenantID, templateID, input.TemplateVersion)
	if err != nil {
		return nil, err
	}
	datasetIDs := make([]string, 0, len(input.DatasetIDs))
	for _, datasetID := range input.DatasetIDs {
		datasetID = strings.TrimSpace(datasetID)
		if datasetID == "" {
			continue
		}
		link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
		if err != nil {
			return nil, err
		}
		if link == nil {
			return nil, httperr.BadRequest(40085, "dataset not found in workspace: "+datasetID)
		}
		datasetIDs = append(datasetIDs, datasetID)
	}
	missingSuggestions := make([]string, 0)
	if len(input.DatasetIDs) == 0 {
		suggestions := input.DatasetSuggestions
		if len(suggestions) == 0 {
			suggestions = templatePayload.DatasetSuggestions
		}
		for _, suggestion := range suggestions {
			name := strings.TrimSpace(suggestion)
			if name == "" {
				continue
			}
			link, err := s.Store.GetDatasetLinkByName(ctx, tenantID, name, "")
			if err != nil {
				return nil, err
			}
			if link != nil {
				datasetIDs = append(datasetIDs, link.ID)
				continue
			}
			if !input.CreateMissingDatasets {
				return nil, httperr.BadRequest(40085, "dataset suggestions not found: "+name)
			}
			missingSuggestions = append(missingSuggestions, name)
		}
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = asset.Name + " 草稿"
	}
	return map[string]any{
		"name":                      name,
		"dataset_ids":               datasetIDs,
		"dataset_suggestions":       missingSuggestions,
		"create_missing_datasets":   input.CreateMissingDatasets,
		"scenario_template_id":      asset.ID,
		"scenario_template_version": selected.Version,
		"authoring":                 mergeTemplateAuthoringForVersion(templatePayload, input.Authoring, selected.PayloadSchema, selected.SnapshotProvenance),
	}, nil
}

func (s *Service) publishedChatTemplateForInstantiation(
	ctx context.Context, tenantID, templateID string, version int64,
) (*model.ScenarioTemplateAsset, *model.ScenarioTemplateVersion, ScenarioTemplatePayload, error) {
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
	if !containsString(strings.Split(asset.AppTypes, ","), "chat") {
		return nil, nil, ScenarioTemplatePayload{}, httperr.BadRequest(40085, "scenario template does not support chat assistants")
	}
	payload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		return nil, nil, ScenarioTemplatePayload{}, err
	}
	// Builtin templates ship with the product and are platform-reviewed; they
	// have no per-tenant release candidate chain, so requiring a PASS gate here
	// would make the first-run wizard unusable for every new workspace
	// (doc/228 §3.1 T7.0).
	//
	// doc/276 §7.1 step 4 extends that to an *unmodified* fork: a confirmed
	// baseline version is byte-identical to the reviewed baseline, so it is
	// platform-reviewed by definition. As soon as the workspace edits it, the
	// selecting version moves past baseline_version and the full gate applies
	// again — the exemption can never cover tenant-authored content.
	baselineReviewed := asset.Source == model.TemplateSourceBuiltin ||
		(asset.Source == model.TemplateSourceCopy &&
			asset.LineageState == model.LineageStateConfirmedBaseline &&
			asset.BaselineVersion > 0 && selected.Version == asset.BaselineVersion)
	if baselineReviewed {
		return asset, selected, payload, nil
	}
	candidate, err := s.Store.GetReleaseCandidateVersion(ctx, tenantID, asset.ID, selected.Version)
	if err != nil {
		return nil, nil, ScenarioTemplatePayload{}, err
	}
	if candidate == nil || candidate.TargetType != "template" || candidate.TargetID != asset.ID ||
		candidate.TargetVersion != fmt.Sprintf("v%d", selected.Version) {
		return nil, nil, ScenarioTemplatePayload{}, httperr.Forbidden("scenario template instantiation requires a matching release candidate")
	}
	gate, err := s.Store.GetActiveGateDecision(ctx, tenantID, asset.ID, selected.Version)
	if err != nil {
		return nil, nil, ScenarioTemplatePayload{}, err
	}
	if gate == nil || gate.Decision != model.GateDecisionPass {
		return nil, nil, ScenarioTemplatePayload{}, httperr.Forbidden("scenario template instantiation requires an active PASS release gate")
	}
	return asset, selected, payload, nil
}

// mergeTemplateAuthoring applies the template's preset/profile defaults and
// then overlays only the fields the caller explicitly supplied. It used to be
// all-or-nothing, which meant a client that set a single field (for example the
// wizard picking `llm_id`) silently dropped the template prompt and retrieval
// parameters - the exact "frontend overrides the backend template" drift that
// doc/228 T7.0 forbids.
func mergeTemplateAuthoring(payload ScenarioTemplatePayload, override ChatAuthoring) ChatAuthoring {
	return mergeTemplateAuthoringForVersion(payload, override, model.TemplatePayloadSchemaV2, model.SnapshotAuthored)
}

// mergeTemplateAuthoringForVersion is the version-aware form (doc/276 §7.7): a
// historical v2 version reconstructed during migration must resolve its preset
// text from the frozen snapshot, not from the live map.
func mergeTemplateAuthoringForVersion(payload ScenarioTemplatePayload, override ChatAuthoring, payloadSchema, provenance string) ChatAuthoring {
	authoring := parameterProfileAuthoring(payload.ParameterProfileID)
	if prompt := promptPresetTextForVersion(payload.PromptPresetID, payloadSchema, provenance); prompt != nil {
		if authoring.PromptConfig == nil {
			authoring.PromptConfig = defaultPromptConfigSeed()
		}
		authoring.PromptConfig["system"] = prompt.system
		authoring.PromptConfig["prologue"] = prompt.prologue
		authoring.PromptConfig["empty_response"] = prompt.emptyResponse
	}
	// An authored v3 pack carries its prompt and knobs inline (doc/276 §6.4):
	// they win over the v2 preset lookup, and the client override still wins
	// over both (it is the reviewed per-instantiation tweak).
	if pack := payload.Pack; pack != nil && pack.Application.Prompt.System != "" {
		if authoring.PromptConfig == nil {
			authoring.PromptConfig = map[string]interface{}{}
		}
		authoring.PromptConfig["system"] = pack.Application.Prompt.System
		authoring.PromptConfig["prologue"] = pack.Application.Prompt.Prologue
		authoring.PromptConfig["empty_response"] = pack.Application.Prompt.EmptyResponse
		if pack.Application.Parameters.TopN != 0 {
			authoring.TopN = pack.Application.Parameters.TopN
		}
		if pack.Application.Parameters.SimilarityThreshold != 0 {
			authoring.SimilarityThreshold = pack.Application.Parameters.SimilarityThreshold
		}
		if pack.Application.Parameters.VectorSimilarityWeight != 0 {
			authoring.VectorSimilarityWeight = pack.Application.Parameters.VectorSimilarityWeight
		}
		if len(pack.Application.Parameters.LLMSetting) > 0 {
			merged := map[string]interface{}{}
			for key, value := range authoring.LLMSetting {
				merged[key] = value
			}
			for key, value := range pack.Application.Parameters.LLMSetting {
				merged[key] = value
			}
			authoring.LLMSetting = merged
		}
	}
	if override.PromptConfig != nil {
		authoring.PromptConfig = override.PromptConfig
	}
	if override.Language != "" {
		authoring.Language = override.Language
	}
	if override.LLMID != "" {
		authoring.LLMID = override.LLMID
	}
	if override.RerankID != "" {
		authoring.RerankID = override.RerankID
	}
	if override.TopN != 0 {
		authoring.TopN = override.TopN
	}
	if override.TopK != 0 {
		authoring.TopK = override.TopK
	}
	if override.SimilarityThreshold != 0 {
		authoring.SimilarityThreshold = override.SimilarityThreshold
	}
	if override.VectorSimilarityWeight != 0 {
		authoring.VectorSimilarityWeight = override.VectorSimilarityWeight
	}
	if len(override.LLMSetting) > 0 {
		merged := make(map[string]interface{}, len(authoring.LLMSetting)+len(override.LLMSetting))
		for key, value := range authoring.LLMSetting {
			merged[key] = value
		}
		for key, value := range override.LLMSetting {
			merged[key] = value
		}
		authoring.LLMSetting = merged
	}
	return authoring
}

func firstString(values []string, fallback string) string {
	if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
		return values[0]
	}
	return fallback
}

// SavePromptPolicy creates a new active version atomically.
func (s *Service) SavePromptPolicy(ctx context.Context, tenantID, userID string, input PromptPolicyInput) (*model.PromptPolicyVersion, error) {
	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	switch scope {
	case model.PromptScopeTenant, model.PromptScopeChat, model.PromptScopeSearch, model.PromptScopeAgent:
	default:
		return nil, httperr.BadRequest(40050, "prompt policy scope is invalid")
	}
	objectID := strings.TrimSpace(input.ObjectID)
	if scope != model.PromptScopeTenant && objectID == "" {
		return nil, httperr.BadRequest(40051, "object_id is required for non-tenant scope")
	}
	payload, err := validPayloadJSON(input.Payload, 40052, "prompt policy payload must be a JSON object")
	if err != nil {
		return nil, err
	}
	var maxVersion int64
	policies, _, err := s.Store.ListPromptPolicies(ctx, tenantID, 1, 1, repository.GovernanceFilter{Scope: scope, ObjectID: objectID})
	if err != nil {
		return nil, err
	}
	if len(policies) > 0 {
		maxVersion = policies[0].Version
	}
	policy := &model.PromptPolicyVersion{
		ID: id.New(), TenantID: tenantID, Scope: scope, ObjectID: objectID,
		Version: maxVersion + 1, PresetID: strings.TrimSpace(input.PresetID), ProfileID: strings.TrimSpace(input.ProfileID),
		Payload: payload, CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreatePromptPolicy(ctx, policy); err != nil {
		return nil, err
	}
	return policy, nil
}

// RollbackPromptPolicy switches the active pointer without rewriting history.
func (s *Service) RollbackPromptPolicy(ctx context.Context, tenantID, policyID string) (*model.PromptPolicyVersion, error) {
	policy, err := s.Store.GetPromptPolicy(ctx, tenantID, policyID)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, httperr.NotFound("prompt policy not found")
	}
	found, err := s.Store.RollbackPromptPolicy(ctx, tenantID, policy.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, httperr.NotFound("prompt policy not found")
	}
	return policy, nil
}

// ListPromptPolicies returns one tenant's history or explicit platform scope.
func (s *Service) ListPromptPolicies(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter repository.GovernanceFilter) ([]model.PromptPolicyVersion, int64, error) {
	if scopeAll {
		return s.Store.ListAllPromptPolicies(ctx, page, pageSize, filter)
	}
	return s.Store.ListPromptPolicies(ctx, tenantID, page, pageSize, filter)
}

// ResolvePromptPolicy applies object policy first, then falls back to the
// tenant default. This is the runtime contract promised by the governance UI.
func (s *Service) ResolvePromptPolicy(ctx context.Context, tenantID, scope, objectID string) (*model.PromptPolicyVersion, error) {
	if strings.TrimSpace(objectID) != "" {
		policy, err := s.Store.GetActivePromptPolicy(ctx, tenantID, scope, objectID)
		if err != nil || policy != nil {
			return policy, err
		}
	}
	return s.Store.GetActivePromptPolicy(ctx, tenantID, scope, "")
}

func datasetLifecycleStatus(dataset *model.DatasetLink) string {
	now := time.Now().UTC()
	if dataset.ReviewStatus == model.KnowledgeReviewExpired {
		return model.KnowledgeReviewExpired
	}
	if dataset.ExpiresAt != nil && dataset.ExpiresAt.Before(now) {
		return model.KnowledgeReviewExpired
	}
	if dataset.LastReviewedAt != nil && now.Sub(*dataset.LastReviewedAt) > 90*24*time.Hour {
		return model.KnowledgeReviewDue
	}
	if strings.TrimSpace(dataset.OwnerID) == "" && strings.TrimSpace(dataset.OwnerTeamID) == "" {
		return "missing_owner"
	}
	if dataset.ReviewStatus == model.KnowledgeReviewCurrent {
		return model.KnowledgeReviewCurrent
	}
	return model.KnowledgeReviewNone
}

// ListKnowledgeLifecycle returns dataset governance rows with derived status.
func (s *Service) ListKnowledgeAssetMap(ctx context.Context, tenantID string, filter repository.GovernanceFilter) ([]repository.KnowledgeAssetView, error) {
	datasets, err := s.Store.ListKnowledgeLifecycle(ctx, tenantID, filter)
	if err != nil {
		return nil, err
	}
	assets, err := s.Store.ListKnowledgeAssetViews(ctx, tenantID, datasets, 20)
	if err != nil {
		return nil, err
	}
	for index := range assets {
		assets[index].LifecycleStatus = datasetLifecycleStatus(&assets[index].DatasetLink)
	}
	return assets, nil
}

func (s *Service) ListKnowledgeLifecycle(ctx context.Context, tenantID string, filter repository.GovernanceFilter) ([]DatasetLifecycleView, error) {
	datasets, err := s.Store.ListKnowledgeLifecycle(ctx, tenantID, filter)
	if err != nil {
		return nil, err
	}
	out := make([]DatasetLifecycleView, 0, len(datasets))
	for i := range datasets {
		out = append(out, DatasetLifecycleView{DatasetLink: &datasets[i], LifecycleStatus: datasetLifecycleStatus(&datasets[i])})
	}
	return out, nil
}

// UpdateKnowledgeLifecycle validates and persists the dataset governance fields.
func (s *Service) UpdateKnowledgeLifecycle(ctx context.Context, tenantID, datasetID string, input DatasetLifecycleInput) (*DatasetLifecycleView, error) {
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if dataset == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	if input.QualityScore < 0 || input.QualityScore > 100 {
		return nil, httperr.BadRequest(40053, "quality score must be between 0 and 100")
	}
	if input.ExpiresAt != nil && input.EffectiveAt != nil && input.ExpiresAt.Before(*input.EffectiveAt) {
		return nil, httperr.BadRequest(40054, "expires_at must be after effective_at")
	}
	status := strings.ToLower(strings.TrimSpace(input.ReviewStatus))
	switch status {
	case "":
		status = dataset.ReviewStatus
		if status == "" {
			status = model.KnowledgeReviewNone
		}
	case model.KnowledgeReviewNone, model.KnowledgeReviewCurrent, model.KnowledgeReviewDue, model.KnowledgeReviewExpired:
	default:
		return nil, httperr.BadRequest(40055, "knowledge review status is invalid")
	}
	dataset.OwnerID = strings.TrimSpace(input.OwnerID)
	dataset.OwnerTeamID = strings.TrimSpace(input.OwnerTeamID)
	dataset.SourceType = strings.TrimSpace(input.SourceType)
	dataset.BusinessDomain = strings.TrimSpace(input.BusinessDomain)
	dataset.Sensitivity = strings.TrimSpace(input.Sensitivity)
	dataset.EffectiveAt = input.EffectiveAt
	dataset.ExpiresAt = input.ExpiresAt
	dataset.LastReviewedAt = input.LastReviewedAt
	dataset.ReviewStatus = status
	dataset.QualityScore = input.QualityScore
	updated, err := s.Store.UpdateDatasetLifecycle(ctx, dataset)
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, httperr.NotFound("dataset not found")
	}
	// Retrieval pushdown backfill (doc/123 §5.1): keep meta_fields in sync
	// with the governance classification, best-effort.
	if dataset.PushdownEnabled {
		s.syncDatasetPushdownMetadataAsync(ctx, tenantID, dataset.ID)
	}
	emitKnowledgeLifecycleEvent(ctx, dataset)
	return &DatasetLifecycleView{DatasetLink: dataset, LifecycleStatus: datasetLifecycleStatus(dataset)}, nil
}

func emitKnowledgeLifecycleEvent(ctx context.Context, dataset *model.DatasetLink) {
	if datasetLifecycleStatus(dataset) != model.KnowledgeReviewExpired {
		return
	}
	notify.Emit(ctx, notify.Event{
		Title: "knowledge dataset expired", Severity: "warn",
		Type: "knowledge.expired", TenantID: dataset.TenantID,
		Resource: "dataset", ResourceID: dataset.ID,
		Fields: map[string]string{
			"review_status": dataset.ReviewStatus,
			"owner_id":      dataset.OwnerID,
		},
	})
}

func buildEvalCases(tenantID, evalSetID, userID string, inputs []EvalCaseInput) ([]model.EvalCase, error) {
	now := time.Now().UTC()
	out := make([]model.EvalCase, 0, len(inputs))
	for _, input := range inputs {
		question := strings.TrimSpace(input.Question)
		if question == "" {
			return nil, httperr.BadRequest(40056, "evaluation case question is required")
		}
		status, err := normalizeStatus(input.Status, model.EvalStatusDraft, model.EvalStatusDraft, model.EvalStatusPublished)
		if err != nil {
			return nil, err
		}
		source := strings.ToLower(strings.TrimSpace(input.Source))
		if source == "" {
			source = model.EvalSourceManual
		}
		out = append(out, model.EvalCase{
			ID: id.New(), TenantID: tenantID, EvalSetID: evalSetID, Question: question,
			ExpectedAnswer: input.ExpectedAnswer, ExpectedKeywords: input.ExpectedKeywords,
			ExpectedCitationDocIDs: input.ExpectedCitationDocIDs, Source: source,
			Status: status, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
		})
	}
	return out, nil
}

// CreateEvalSet stores a reusable suite and its cases.
func (s *Service) CreateEvalSet(ctx context.Context, tenantID, userID string, input EvalSetInput) (*model.EvalSet, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, httperr.BadRequest(40057, "evaluation set name is required")
	}
	if len(input.Cases) == 0 {
		return nil, httperr.BadRequest(40058, "at least one evaluation case is required")
	}
	now := time.Now().UTC()
	evalSet := &model.EvalSet{
		ID: id.New(), TenantID: tenantID, Name: name, Description: strings.TrimSpace(input.Description),
		Source: model.EvalSourceManual, AppType: strings.ToLower(strings.TrimSpace(input.AppType)), AppID: input.AppID,
		ScenarioTemplateID: input.ScenarioTemplateID, DatasetIDs: strings.Join(input.DatasetIDs, ","),
		Version: 1, ItemCount: int64(len(input.Cases)), Status: model.EvalStatusDraft,
		CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	if strings.TrimSpace(input.ScenarioTemplateID) != "" {
		evalSet.Source = model.EvalSourceTemplate
	}
	evalSet.Status = model.EvalStatusDraft
	if strings.TrimSpace(input.Status) != "" {
		status, err := normalizeStatus(input.Status, model.EvalStatusDraft, model.EvalStatusDraft, model.EvalStatusPublished)
		if err != nil {
			return nil, err
		}
		evalSet.Status = status
	}
	cases, err := buildEvalCases(tenantID, evalSet.ID, userID, input.Cases)
	if err != nil {
		return nil, err
	}
	if err := s.Store.CreateEvalSetWithCases(ctx, evalSet, cases); err != nil {
		return nil, err
	}
	return evalSet, nil
}

// UpdateEvalSet replaces mutable metadata and cases; suites are versioned by count.
func (s *Service) UpdateEvalSet(ctx context.Context, tenantID, userID, evalSetID string, input EvalSetInput) (*model.EvalSet, error) {
	existing, err := s.Store.GetEvalSet(ctx, tenantID, evalSetID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, httperr.NotFound("evaluation set not found")
	}
	if len(input.Cases) == 0 {
		return nil, httperr.BadRequest(40059, "at least one evaluation case is required")
	}
	existing.Name = strings.TrimSpace(input.Name)
	existing.Description = strings.TrimSpace(input.Description)
	if strings.TrimSpace(input.AppType) != "" {
		existing.AppType = strings.ToLower(strings.TrimSpace(input.AppType))
	}
	existing.AppID = input.AppID
	existing.DatasetIDs = strings.Join(input.DatasetIDs, ",")
	status, err := normalizeStatus(input.Status, existing.Status, model.EvalStatusDraft, model.EvalStatusPublished)
	if err != nil {
		return nil, err
	}
	existing.Status = status
	existing.Version++
	existing.ItemCount = int64(len(input.Cases))
	existing.UpdatedAt = time.Now().UTC()
	cases, err := buildEvalCases(tenantID, existing.ID, userID, input.Cases)
	if err != nil {
		return nil, err
	}
	if err := s.Store.UpdateEvalSetWithCases(ctx, existing, cases); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, httperr.NotFound("evaluation set not found")
		}
		return nil, err
	}
	return existing, nil
}

// GetEvalSet returns the suite and all replayable cases.
func (s *Service) GetEvalSet(ctx context.Context, tenantID, evalSetID string) (*model.EvalSet, []model.EvalCase, error) {
	evalSet, err := s.Store.GetEvalSet(ctx, tenantID, evalSetID)
	if err != nil || evalSet == nil {
		return nil, nil, err
	}
	cases, err := s.Store.ListEvalCases(ctx, tenantID, evalSetID)
	if err != nil {
		return nil, nil, err
	}
	return evalSet, cases, nil
}

// ListEvalSets enforces tenant isolation except explicit platform scope.
func (s *Service) ListEvalSets(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter repository.GovernanceFilter) ([]model.EvalSet, int64, error) {
	if scopeAll {
		return s.Store.ListAllEvalSets(ctx, page, pageSize, filter)
	}
	return s.Store.ListEvalSets(ctx, tenantID, page, pageSize, filter)
}

// DeleteEvalSet removes a suite and cases; badcase event links remain historical IDs.
func (s *Service) DeleteEvalSet(ctx context.Context, tenantID, evalSetID string) error {
	deleted, err := s.Store.DeleteEvalSet(ctx, tenantID, evalSetID)
	if err != nil {
		return err
	}
	if !deleted {
		return httperr.NotFound("evaluation set not found")
	}
	return nil
}

// ConvertBadcaseToEvalCase captures a real failure as an evaluation case.
func (s *Service) ConvertBadcaseToEvalCase(ctx context.Context, tenantID string, scopeAll bool, eventID, evalSetID string) (*model.EvalCase, error) {
	event, err := s.Store.GetKnowledgeOpsEvent(ctx, tenantID, eventID, scopeAll)
	if err != nil {
		return nil, err
	}
	if event == nil {
		return nil, httperr.NotFound("knowledge ops event not found")
	}
	eventTenantID := tenantID
	if scopeAll {
		eventTenantID = event.TenantID
	}
	evalSet, err := s.Store.GetEvalSet(ctx, eventTenantID, evalSetID)
	if err != nil {
		return nil, err
	}
	if evalSet == nil {
		return nil, httperr.NotFound("evaluation set not found")
	}
	now := time.Now().UTC()
	evalCase := &model.EvalCase{
		ID: id.New(), TenantID: eventTenantID, EvalSetID: evalSetID, Question: event.Question,
		ExpectedAnswer: "", ExpectedKeywords: "", ExpectedCitationDocIDs: "",
		Source: model.EvalSourceBadcase, SourceID: event.ID,
		SourceSessionID: event.SessionID, SourceRequestID: event.RequestID,
		SourceFeedbackComment: event.FeedbackComment, Status: model.EvalStatusDraft,
		CreatedBy: event.UserID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateEvalCases(ctx, eventTenantID, []model.EvalCase{*evalCase}); err != nil {
		return nil, err
	}
	if _, err := s.Store.LinkKnowledgeOpsEvalCase(ctx, eventTenantID, eventID, evalSetID, evalCase.ID); err != nil {
		return nil, err
	}
	return evalCase, nil
}

// GovernanceOverview aggregates board counters for the current tenant.
func (s *Service) GovernanceOverview(ctx context.Context, tenantID string) (*GovernanceSummary, error) {
	_, templateTotal, err := s.Store.ListScenarioTemplates(ctx, tenantID, 1, 1, repository.GovernanceFilter{})
	if err != nil {
		return nil, err
	}
	_, policyTotal, err := s.Store.ListPromptPolicies(ctx, tenantID, 1, 1, repository.GovernanceFilter{})
	if err != nil {
		return nil, err
	}
	datasets, err := s.Store.ListKnowledgeLifecycle(ctx, tenantID, repository.GovernanceFilter{})
	if err != nil {
		return nil, err
	}
	_, evalSetTotal, err := s.Store.ListEvalSets(ctx, tenantID, 1, 1, repository.GovernanceFilter{})
	if err != nil {
		return nil, err
	}
	summary := &GovernanceSummary{Templates: templateTotal, PromptPolicies: policyTotal, Datasets: int64(len(datasets)), EvalSets: evalSetTotal}
	for i := range datasets {
		switch datasetLifecycleStatus(&datasets[i]) {
		case "missing_owner":
			summary.MissingOwner++
		case model.KnowledgeReviewDue:
			summary.DueDatasets++
		case model.KnowledgeReviewExpired:
			summary.Expired++
		}
	}
	return summary, nil
}

// KnowledgeHealth aggregates lifecycle governance, parse projection and the
// recent citation ledger into one stage-3 health contract.
func (s *Service) KnowledgeHealth(ctx context.Context, tenantID string) (*KnowledgeHealthSummary, error) {
	datasets, err := s.Store.ListKnowledgeLifecycle(ctx, tenantID, repository.GovernanceFilter{})
	if err != nil {
		return nil, err
	}
	parse, err := s.Store.ParseTaskStatusSummary(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30).Format("2006-01-02")
	knowledge, err := s.Store.KnowledgeOpsSummary(ctx, tenantID, false, from, to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	summary := &KnowledgeHealthSummary{
		DatasetCount:         int64(len(datasets)),
		ParseHealth:          ParseHealth{TaskCount: parse.Total, Done: parse.Done, Running: parse.Running, Queued: parse.Queued, Failed: parse.Failed, Stopped: parse.Stopped},
		CitationMissingCount: knowledge.Completed - knowledge.WithCitations,
		CitationWindowDays:   30,
	}
	qualitySum := int64(0)
	for i := range datasets {
		dataset := &datasets[i]
		qualitySum += int64(dataset.QualityScore)
		if dataset.QualityScore < 80 {
			summary.LowQualityCount++
		}
		if strings.TrimSpace(dataset.Sensitivity) == "" {
			summary.MissingClassification++
		}
		switch datasetLifecycleStatus(dataset) {
		case "missing_owner":
			summary.LifecycleStateCounts.MissingOwner++
		case model.KnowledgeReviewDue:
			summary.LifecycleStateCounts.Due++
		case model.KnowledgeReviewExpired:
			summary.LifecycleStateCounts.Expired++
		case model.KnowledgeReviewCurrent:
			summary.LifecycleStateCounts.Current++
		case model.KnowledgeReviewNone:
			summary.LifecycleStateCounts.Unreviewed++
		}
	}
	if len(datasets) > 0 {
		summary.AverageQualityScore = float64(qualitySum) / float64(len(datasets))
	}
	if parse.Total > 0 {
		summary.ParseHealth.ReadyRate = float64(parse.Done) / float64(parse.Total)
	}
	if summary.CitationMissingCount < 0 {
		summary.CitationMissingCount = 0
	}
	return summary, nil
}
