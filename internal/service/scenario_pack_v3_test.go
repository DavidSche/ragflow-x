package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// validPackDocument returns a minimal complete v3 pack document. Each test
// mutates exactly one field so the failure it asserts is unambiguous.
func validPackDocument() map[string]interface{} {
	return map[string]interface{}{
		"schema": ScenarioPackSchemaV3,
		"business": map[string]interface{}{
			"role": "contract reviewer",
		},
		"application": map[string]interface{}{
			"appType": "chat",
			"prompt": map[string]interface{}{
				"system": "answer with citations",
			},
			"parameters": map[string]interface{}{
				"topN": 8, "similarityThreshold": 0.5, "vectorSimilarityWeight": 0.3,
			},
			"modelRequirement": map[string]interface{}{
				"capabilities": []interface{}{"chat"}, "allowTenantOverride": true,
			},
		},
		"requires": map[string]interface{}{"packSchema": "v3"},
	}
}

func encodePack(t *testing.T, document map[string]interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal pack: %v", err)
	}
	return raw
}

func TestDecodeScenarioPackProjectsBusinessFields(t *testing.T) {
	document := validPackDocument()
	document["business"].(map[string]interface{})["sampleQuestions"] = []interface{}{"what is the renewal date?"}
	document["knowledge"] = map[string]interface{}{
		"aggregate": "all_required",
		"datasets": []interface{}{map[string]interface{}{
			"role": "primary", "name": "contracts", "required": true,
			"suggestions": []interface{}{"contracts", "legal"},
		}},
	}
	raw := encodePack(t, document)

	pack, err := DecodeScenarioPack(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	projected := pack.ProjectPayload()
	if projected.Pack == nil {
		t.Fatal("projection must keep the pack pointer so v3 rows stay distinguishable")
	}
	if len(projected.SampleQuestions) != 1 {
		t.Fatalf("sample questions not projected: %#v", projected.SampleQuestions)
	}
	// Dataset names are a projection of `name` + suggestions, deduplicated, so
	// the wizard can offer free-text-less suggestions without a second resolver.
	if len(projected.DatasetSuggestions) != 2 || projected.DatasetSuggestions[0] != "contracts" {
		t.Fatalf("dataset suggestions = %#v", projected.DatasetSuggestions)
	}
	if projected.Knowledge == nil || len(projected.Knowledge.Datasets) != 1 {
		t.Fatalf("knowledge not projected: %#v", projected.Knowledge)
	}
	if !projected.Knowledge.Datasets[0].Required {
		t.Fatal("required flag must survive projection")
	}
}

func TestDecodeScenarioPackRejectsUnknownFields(t *testing.T) {
	document := validPackDocument()
	document["unexpected"] = true
	if _, err := DecodeScenarioPack(encodePack(t, document)); err == nil {
		t.Fatal("unknown top-level field must be rejected, not ignored (doc/276 §8.7)")
	}
}

func TestScenarioPackValidateRequiresAuthoredCopy(t *testing.T) {
	document := validPackDocument()
	document["application"].(map[string]interface{})["prompt"] = map[string]interface{}{}
	pack, err := DecodeScenarioPack(encodePack(t, document))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := pack.Validate(); err == nil {
		t.Fatal("a published pack without prompt.system must fail validation")
	}
	// The draft contract is the opposite: structural only, so a skeleton import
	// does not have to invent copy (doc/276 §8.4).
	if err := pack.ValidateDraft(); err != nil {
		t.Fatalf("draft validation must tolerate missing copy: %v", err)
	}
}

func TestScenarioPackValidateKeepsCapabilityClosedSet(t *testing.T) {
	document := validPackDocument()
	document["application"].(map[string]interface{})["modelRequirement"] = map[string]interface{}{
		"capabilities": []interface{}{"chat", "telepathy"},
	}
	pack, err := DecodeScenarioPack(encodePack(t, document))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := pack.ValidateDraft(); err == nil {
		t.Fatal("a capability outside the closed set must be rejected")
	}
}

func TestValidateScenarioImportTrustRejectsCredentialsAndExternalLinks(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{"api key", func(document map[string]interface{}) {
			document["application"].(map[string]interface{})["llm"] = map[string]interface{}{"api_key": "sk-1"}
		}},
		{"token in dsl", func(document map[string]interface{}) {
			document["application"].(map[string]interface{})["prompt"] = map[string]interface{}{
				"system": "x", "access_token": "abc",
			}
		}},
		{"external endpoint", func(document map[string]interface{}) {
			document["runtimeExtension"] = map[string]interface{}{
				"ragflow": map[string]interface{}{"baseUrl": "https://exfiltration.example.com/collect"},
			}
		}},
		{"external pipeline url", func(document map[string]interface{}) {
			document["runtimeExtension"] = map[string]interface{}{
				"ragflow": map[string]interface{}{"pipelineBinding": map[string]interface{}{"url": "http://10.0.0.9/hook"}},
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			document := validPackDocument()
			testCase.mutate(document)
			if err := ValidateScenarioImportTrust(encodePack(t, document)); err == nil {
				t.Fatalf("%s must be rejected at import", testCase.name)
			}
		})
	}
}

func TestValidateScenarioImportTrustAllowsInlineExampleURL(t *testing.T) {
	document := validPackDocument()
	// A prompt may legitimately quote a URL; only link-shaped *fields* are
	// rejected, otherwise every support doc would be unimportable.
	document["application"].(map[string]interface{})["prompt"] = map[string]interface{}{
		"system": "see https://example.com/policy for the policy text",
	}
	if err := ValidateScenarioImportTrust(encodePack(t, document)); err != nil {
		t.Fatalf("inline prose URL must stay importable: %v", err)
	}
}

func TestRenderScenarioPackRejectsUnknownNode(t *testing.T) {
	document := validPackDocument()
	document["application"].(map[string]interface{})["appType"] = "agent"
	document["runtimeExtension"] = map[string]interface{}{
		"ragflow": map[string]interface{}{
			"canvasDsl": map[string]interface{}{
				"components": []interface{}{
					map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}},
					map[string]interface{}{"obj": map[string]interface{}{"component_name": "ShellExec"}},
				},
			},
		},
	}
	pack, err := DecodeScenarioPack(encodePack(t, document))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := RenderScenarioPack(pack); err == nil {
		t.Fatal("a component outside the whitelist must be rejected before the engine sees it")
	}
}

func TestRenderScenarioPackProducesAgentDefinition(t *testing.T) {
	document := validPackDocument()
	document["application"].(map[string]interface{})["appType"] = "agent"
	document["runtimeExtension"] = map[string]interface{}{
		"ragflow": map[string]interface{}{
			"canvasDsl": map[string]interface{}{
				"components": []interface{}{
					map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}},
					map[string]interface{}{"obj": map[string]interface{}{"component_name": "Retrieval"}},
					map[string]interface{}{"obj": map[string]interface{}{"component_name": "Generate"}},
				},
			},
		},
	}
	pack, err := DecodeScenarioPack(encodePack(t, document))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	rendered, err := RenderScenarioPack(pack)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered.AppType != "agent" || len(rendered.CanvasDSL) == 0 {
		t.Fatalf("rendered agent definition incomplete: %#v", rendered)
	}
	if rendered.PromptConfig["system"] != "answer with citations" {
		t.Fatalf("prompt not projected: %#v", rendered.PromptConfig)
	}
}

func TestScenarioPayloadRoundTripsPackDocument(t *testing.T) {
	document := validPackDocument()
	document["application"].(map[string]interface{})["appType"] = "agent"
	document["runtimeExtension"] = map[string]interface{}{
		"ragflow": map[string]interface{}{
			"canvasDsl": map[string]interface{}{
				"components": []interface{}{map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}}},
			},
		},
	}
	raw := encodePack(t, document)
	var payload ScenarioTemplatePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Pack == nil {
		t.Fatal("payload must recognise the v3 schema")
	}
	if payloadSchemaOf(payload) != model.TemplatePayloadSchemaV3 {
		t.Fatalf("payload schema = %q", payloadSchemaOf(payload))
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	redecoded, err := DecodeScenarioPack(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if redecoded.RuntimeExtension.RAGFlow == nil || len(redecoded.RuntimeExtension.RAGFlow.CanvasDSL) == 0 {
		t.Fatal("export must round-trip the canvas DSL, not strip it")
	}
}

func TestSkeletonImportCreatesDraftWithWhitelistedDSL(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Skip("mock provider unavailable")
	}
	mock.SetAgentTemplate(ragflow.AgentTemplate{
		ID: "support-agent", CanvasType: "agent", CanvasCategory: "agent_canvas",
		Title: map[string]interface{}{"zh": "客服助手"},
		DSL: map[string]interface{}{"components": []interface{}{
			map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}},
		}},
	})
	tenant, err := svc.CreateTenant(ctx, "Skeleton Tenant")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	actor := actorID(t, svc)
	asset, err := svc.ImportScenarioSkeleton(ctx, tenant.ID, actor, ScenarioSkeletonSourceAgent+":support-agent")
	if err != nil {
		t.Fatalf("import skeleton: %v", err)
	}
	if asset.Status != model.AssetStatusDraft {
		t.Fatalf("skeleton import must land as a draft, got %q", asset.Status)
	}
	if asset.Source != model.TemplateSourceImport {
		t.Fatalf("skeleton import source = %q", asset.Source)
	}
	// The draft must be structurally renderable but not publishable: business
	// copy is still missing, and publish re-runs the strict validation.
	_, _, _, err = svc.GetScenarioTemplate(ctx, tenant.ID, asset.ID, 0, false)
	if err != nil {
		t.Fatalf("read back draft: %v", err)
	}
	// Publishing the imported content must fail: the draft has no authored copy,
	// so the strict (published) validation rejects it.
	_, selected, _, err := svc.GetScenarioTemplate(ctx, tenant.ID, asset.ID, 0, false)
	if err != nil {
		t.Fatalf("read draft version: %v", err)
	}
	draftPayload, err := parseTemplatePayload(selected.PayloadJSON)
	if err != nil {
		t.Fatalf("parse draft payload: %v", err)
	}
	if _, err := svc.UpdateScenarioTemplate(ctx, tenant.ID, actor, asset.ID, ScenarioTemplateInput{
		Status: model.AssetStatusPublished, Payload: draftPayload,
	}); err == nil {
		t.Fatal("an incomplete imported draft must not be publishable")
	}
}

func TestListScenarioSkeletonsDegradesPerSource(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Skip("mock provider unavailable")
	}
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "p1", Title: "Parser"}, map[string]interface{}{"components": []interface{}{}})
	catalog, err := svc.ListScenarioSkeletons(ctx)
	if err != nil {
		t.Fatalf("list skeletons: %v", err)
	}
	if len(catalog.Sources) != 4 {
		t.Fatalf("expected four upstream catalogues, got %d", len(catalog.Sources))
	}
	found := false
	for _, item := range catalog.Items {
		if item.Source == ScenarioSkeletonSourcePipeline && item.UpstreamID == "p1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pipeline skeleton missing from %#v", catalog.Items)
	}
}

func TestScenarioTemplateHealthReportsUnknownInsteadOfZero(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Health Tenant")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	actor := actorID(t, svc)
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, actor, ScenarioTemplateInput{
		Key: "health-template", Name: "Health Template", AppTypes: []string{"chat"},
		Payload: ScenarioTemplatePayload{PromptPresetID: "preset-contract-review"},
		Status:  model.AssetStatusPublished,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	health, err := svc.ScenarioTemplateHealth(ctx, tenant.ID, asset.ID)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if len(health.Metrics) != 5 {
		t.Fatalf("expected five metrics (doc/276 §8.6), got %d", len(health.Metrics))
	}
	for _, metric := range health.Metrics {
		if metric.Key == "repair_completion_rate" && metric.Available {
			t.Fatal("repair_completion_rate has no evidence source yet and must report unavailable")
		}
		if !metric.Available && metric.Reason == "" {
			t.Fatalf("unavailable metric %q must explain why", metric.Key)
		}
		if metric.Definition == "" {
			t.Fatalf("metric %q must carry its formula", metric.Key)
		}
	}
}

// TestScenarioTemplateHealthRepairCompletionFromPersistedChecks pins the §8.6
// repair metric against the migration-167 evidence table: a dataset that had a
// repairable verdict and later a satisfied one counts as repaired; one with only
// a repairable verdict does not. Without any gap row the metric must read
// unavailable, never 0 (covered by the test above).
func TestScenarioTemplateHealthRepairCompletionFromPersistedChecks(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Repair Metric Tenant")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, actorID(t, svc), ScenarioTemplateInput{
		Key: "repair-metric", Name: "Repair Metric", AppTypes: []string{"chat"},
		Payload: ScenarioTemplatePayload{PromptPresetID: "preset-contract-review"},
		Status:  model.AssetStatusPublished,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	check := func(ds, verdict string, at time.Time) model.ScenarioCapabilityCheck {
		return model.ScenarioCapabilityCheck{
			ID: id.New(), TenantID: tenant.ID, TemplateID: asset.ID,
			TemplateVersion: asset.LatestVersion, DatasetID: ds, Role: scenarioDatasetRolePrimary,
			Verdict: verdict, ProbeStatus: CapabilityProbeSuccess,
			CapabilitySetVersion: CapabilityCheckVersion, CreatedAt: at,
		}
	}
	if err := svc.Store.RecordScenarioCapabilityChecks(ctx, []model.ScenarioCapabilityCheck{
		check("ds-a", CapabilityVerdictRepairable, now.Add(-48*time.Hour)),
		check("ds-a", CapabilityVerdictSatisfied, now.Add(-24*time.Hour)),
		check("ds-b", CapabilityVerdictRepairable, now.Add(-24*time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	health, err := svc.ScenarioTemplateHealth(ctx, tenant.ID, asset.ID)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	var repair *ScenarioTemplateMetric
	for i := range health.Metrics {
		if health.Metrics[i].Key == "repair_completion_rate" {
			repair = &health.Metrics[i]
		}
	}
	if repair == nil {
		t.Fatal("repair_completion_rate missing from health metrics")
	}
	if !repair.Available {
		t.Fatalf("expected repair metric available, got reason %q", repair.Reason)
	}
	if repair.Numerator != 1 || repair.Denominator != 2 || repair.Value != 0.5 {
		t.Fatalf("expected 1/2 = 0.5, got %d/%d = %v", repair.Numerator, repair.Denominator, repair.Value)
	}
}

// TestValidateScenarioImportTrustAcceptsExportEnvelope pins the export→import
// round trip: an export document carries the envelope schema at top level and
// the v3 pack under `payload`. Decoding the envelope as a pack used to reject
// every exported v3 template with `unknown field "key"` (live drill 2026-11-13).
func TestValidateScenarioImportTrustAcceptsExportEnvelope(t *testing.T) {
	envelope := map[string]interface{}{
		"schema":    scenarioTemplateSchema,
		"key":       "roundtrip",
		"name":      "round trip",
		"app_types": []interface{}{"chat"},
		"version":   1,
		"payload":   validPackDocument(),
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateScenarioImportTrust(raw); err != nil {
		t.Fatalf("our own export document must stay importable: %v", err)
	}
}

// TestValidateScenarioImportTrustAllowsIncompleteDraftPacks keeps the round
// trip honest for the draft side: an exported skeleton asset intentionally
// carries an empty system prompt (drafts are structurally sound but business-
// incomplete, doc/276 §8.4), and re-importing that draft must succeed as an
// `imported` draft. The trust scan must only enforce structure + security, not
// the business completeness that publish's full Validate owns (live drill
// 2026-11-14, "application.prompt.system is required" broke export→import).
func TestValidateScenarioImportTrustAllowsIncompleteDraftPacks(t *testing.T) {
	document := validPackDocument()
	application := document["application"].(map[string]interface{})
	prompt := application["prompt"].(map[string]interface{})
	prompt["system"] = ""
	delete(document, "business") // role absent: another draft-only gap
	envelope := map[string]interface{}{
		"schema": scenarioTemplateSchema, "key": "draft-roundtrip", "name": "draft round trip",
		"app_types": []interface{}{"chat"}, "payload": document,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateScenarioImportTrust(raw); err != nil {
		t.Fatalf("an incomplete-but-structural draft must stay importable: %v", err)
	}
	// An incomplete pack must still fail the security half: credentials are
	// rejected regardless of draft status.
	credentialDocument := validPackDocument()
	application = credentialDocument["application"].(map[string]interface{})
	prompt = application["prompt"].(map[string]interface{})
	prompt["system"] = ""
	prompt["apiKey"] = "sk-live-roundtrip"
	if err := ValidateScenarioImportTrust(encodePack(t, credentialDocument)); err == nil {
		t.Fatal("a credential in an incomplete draft must still be rejected")
	}
}

// TestValidateScenarioImportTrustScansEnvelopePayload keeps the trust scan
// honest for the nested shape: a credential inside `payload` must still be
// caught, and an invalid nested pack must still fail.
func TestValidateScenarioImportTrustScansEnvelopePayload(t *testing.T) {
	document := validPackDocument()
	document["runtimeExtension"] = map[string]interface{}{
		"ragflow": map[string]interface{}{"baseUrl": "https://exfiltration.example.com/collect"},
	}
	envelope := map[string]interface{}{
		"schema": scenarioTemplateSchema, "key": "k", "name": "n",
		"app_types": []interface{}{"chat"}, "payload": document,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateScenarioImportTrust(raw); err == nil {
		t.Fatal("an external endpoint nested in payload must be rejected")
	}
}

// TestValidateCanvasDSLAcceptsEngineMapShape pins the live-drill shape: RAGFlow
// v1.0.0 agent templates serialise `components` as an object keyed by node id,
// while client-authored canvases use an array. Only the array used to be
// accepted, so every real agent skeleton import failed validation.
func TestValidateCanvasDSLAcceptsEngineMapShape(t *testing.T) {
	mapShape := &ScenarioPackRAGFlowExtension{CanvasDSL: map[string]interface{}{
		"components": map[string]interface{}{
			"begin":              map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}},
			"Agent:Planner":      map[string]interface{}{"obj": map[string]interface{}{"component_name": "Agent"}},
			"Message:AnswerUser": map[string]interface{}{"obj": map[string]interface{}{"component_name": "Message"}},
		},
	}}
	if err := validateCanvasDSL(mapShape); err != nil {
		t.Fatalf("engine map shape must validate: %v", err)
	}
	arrayShape := &ScenarioPackRAGFlowExtension{CanvasDSL: map[string]interface{}{
		"components": []interface{}{
			map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}},
		},
	}}
	if err := validateCanvasDSL(arrayShape); err != nil {
		t.Fatalf("array shape must keep validating: %v", err)
	}
	// The whitelist still applies to the map shape.
	unknown := &ScenarioPackRAGFlowExtension{CanvasDSL: map[string]interface{}{
		"components": map[string]interface{}{
			"begin": map[string]interface{}{"obj": map[string]interface{}{"component_name": "Telepathy"}},
		},
	}}
	if err := validateCanvasDSL(unknown); err == nil {
		t.Fatal("an off-whitelist component inside the map shape must be rejected")
	}
}

// TestExportImportRoundTripsV3Pack walks the whole contract a customer sees when
// they move a template between installations: export a v3 tenant template,
// import it back as a new draft, and require the pack to survive verbatim. The
// envelope must also declare its payload dialect, because a v2 payload has no
// `schema` member to sniff (drill 2026-11-13 reported `payload_schema` missing).
func TestExportImportRoundTripsV3Pack(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Pack Round Trip")
	if err != nil {
		t.Fatal(err)
	}
	userID := actorID(t, svc)

	document := validPackDocument()
	document["runtimeExtension"] = map[string]interface{}{
		"ragflow": map[string]interface{}{
			"canvasDsl": map[string]interface{}{
				"components": []interface{}{map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}}},
			},
		},
	}
	var payload ScenarioTemplatePayload
	if err := json.Unmarshal(encodePack(t, document), &payload); err != nil {
		t.Fatalf("decode pack payload: %v", err)
	}
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, userID, ScenarioTemplateInput{
		Key: "pack-round-trip", Name: "Pack round trip", AppTypes: []string{"chat"},
		Payload: payload, ChangeNote: "authored v3 pack",
	})
	if err != nil {
		t.Fatalf("create v3 template: %v", err)
	}

	export, err := svc.ExportScenarioTemplate(ctx, tenant.ID, asset.ID, 0)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if export.PayloadSchema != model.TemplatePayloadSchemaV3 {
		t.Fatalf("export payload_schema = %q, want v3", export.PayloadSchema)
	}
	encoded, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("encode export: %v", err)
	}
	var envelope struct {
		Payload map[string]interface{} `json:"payload"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Payload["schema"] != ScenarioPackSchemaV3 {
		t.Fatalf("exported payload lost its pack schema: %v", envelope.Payload["schema"])
	}

	// Import into a second workspace: keys are unique per tenant, and a customer
	// moving a template is exactly the cross-installation case.
	importer, err := svc.CreateTenant(ctx, "Pack Round Trip Importer")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := svc.ImportScenarioTemplateDocument(ctx, importer.ID, userID, encoded)
	if err != nil {
		t.Fatalf("import exported document: %v", err)
	}
	_, version, _, err := svc.GetScenarioTemplate(ctx, importer.ID, imported.ID, 0, false)
	if err != nil {
		t.Fatalf("read imported version: %v", err)
	}
	if version.PayloadSchema != model.TemplatePayloadSchemaV3 {
		t.Fatalf("imported payload_schema = %q, want v3 (the pack was downgraded)", version.PayloadSchema)
	}
	roundTripped, err := parseTemplatePayload(version.PayloadJSON)
	if err != nil {
		t.Fatalf("parse imported payload: %v", err)
	}
	if roundTripped.Pack == nil || roundTripped.Pack.RuntimeExtension.RAGFlow == nil {
		t.Fatal("imported pack lost its runtime extension")
	}
	if len(roundTripped.Pack.RuntimeExtension.RAGFlow.CanvasDSL) == 0 {
		t.Fatal("imported pack lost its canvas DSL")
	}
}

// TestExportDeclaresPayloadSchemaForV2Rows covers the legacy half: a v2 payload
// carries no `schema`, so the envelope is the only place the dialect can be
// stated. Claiming "v3" for a pack-less payload would be a lie the importer
// would then have to guess about.
func TestExportDeclaresPayloadSchemaForV2Rows(t *testing.T) {
	if got := declaredPayloadSchema("", ScenarioTemplatePayload{}); got != model.TemplatePayloadSchemaV2 {
		t.Fatalf("empty payload declared %q, want v2", got)
	}
	if got := declaredPayloadSchema(model.TemplatePayloadSchemaV3, ScenarioTemplatePayload{}); got != model.TemplatePayloadSchemaV3 {
		t.Fatalf("stored v3 declared %q", got)
	}
	pack := &ScenarioPack{}
	if got := declaredPayloadSchema("", ScenarioTemplatePayload{Pack: pack}); got != model.TemplatePayloadSchemaV3 {
		t.Fatalf("pack payload declared %q, want v3", got)
	}
}

// TestImportRejectsPayloadSchemaContradiction keeps the discriminator honest: a
// document may not claim v3 while shipping a pack-less payload, and an unknown
// dialect is a schema error rather than a silent downgrade.
func TestImportRejectsPayloadSchemaContradiction(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Schema Contradiction")
	if err != nil {
		t.Fatal(err)
	}
	userID := actorID(t, svc)
	document := map[string]interface{}{
		"schema":         scenarioTemplateSchema,
		"key":            "schema-contradiction",
		"name":           "Schema contradiction",
		"appTypes":       []interface{}{"chat"},
		"payload":        map[string]interface{}{"description": "v2 shape"},
		"version":        1,
		"payload_schema": "v3",
	}
	if _, err := svc.ImportScenarioTemplateDocument(ctx, tenant.ID, userID, encodePack(t, document)); err == nil {
		t.Fatal("a v3 claim over a pack-less payload must be rejected")
	}
	document["payload_schema"] = "v9"
	if _, err := svc.ImportScenarioTemplateDocument(ctx, tenant.ID, userID, encodePack(t, document)); err == nil {
		t.Fatal("an unknown payload_schema must be rejected")
	}
}
