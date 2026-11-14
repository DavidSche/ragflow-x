package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// agentTemplateEngine lets a test hand the skeleton lister an arbitrary upstream
// answer. Everything else falls through to the embedded client.
type agentTemplateEngine struct {
	ragflow.Client
	templates []ragflow.AgentTemplate
	err       error
}

func (e agentTemplateEngine) ListAgentTemplates(context.Context) ([]ragflow.AgentTemplate, error) {
	return e.templates, e.err
}

func (e agentTemplateEngine) GetAgentTemplate(_ context.Context, id string) (ragflow.AgentTemplate, error) {
	if e.err != nil {
		return ragflow.AgentTemplate{}, e.err
	}
	for _, template := range e.templates {
		if template.ID == id {
			return template, nil
		}
	}
	return ragflow.AgentTemplate{}, errors.New("agent template not found")
}

// TestListAgentSkeletonsNormalisesAppTypes is the live-drill regression: the
// upstream `canvas_types` field is a catalogue bucket ("Recommended",
// "Customer Support"), not an app type, so using it as `app_types` made every
// imported agent skeleton declare an app type the platform does not have.
func TestListAgentSkeletonsNormalisesAppTypes(t *testing.T) {
	svc := &Service{RAGFlow: agentTemplateEngine{templates: []ragflow.AgentTemplate{
		{ID: "tpl-1", CanvasType: "Recommended", CanvasCategory: "dataflow", DSL: map[string]interface{}{"components": map[string]interface{}{}}},
		{ID: "tpl-2", CanvasType: "", CanvasCategory: "dataflow"},
	}}}
	items, err := svc.listAgentSkeletons(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 skeletons, got %d", len(items))
	}
	for _, item := range items {
		if len(item.AppTypes) != 1 || item.AppTypes[0] != "agent" {
			t.Errorf("%s: app_types=%v, want [agent]", item.ID, item.AppTypes)
		}
		if item.AppTypes[0] == "Recommended" {
			t.Errorf("%s: upstream canvas type leaked into app_types", item.ID)
		}
		if item.Metadata["canvas_type"] == nil && item.UpstreamID == "tpl-1" {
			t.Errorf("%s: canvas_type must be preserved in metadata", item.ID)
		}
	}
}

// TestListAgentSkeletonsDropsRowsWithoutUpstreamID guards the drill's junk row:
// upstream can list an entry with no id, and import splits the skeleton id into
// (source, upstream_id) — an empty upstream id can never import, so the UI must
// not be offered the button.
func TestListAgentSkeletonsDropsRowsWithoutUpstreamID(t *testing.T) {
	svc := &Service{RAGFlow: agentTemplateEngine{templates: []ragflow.AgentTemplate{
		{ID: "", CanvasType: "Recommended"},
		{ID: "tpl-ok", CanvasType: "Recommended"},
	}}}
	items, err := svc.listAgentSkeletons(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].UpstreamID != "tpl-ok" {
		t.Fatalf("expected only the identifiable skeleton, got %+v", items)
	}
	if _, _, ok := splitSkeletonID(items[0].ID); !ok {
		t.Fatalf("skeleton id %q must be importable", items[0].ID)
	}
}

// TestRenderableScenarioAppTypesMatchesInstantiation is the wizard-contract test:
// every advertised form must be one the unified endpoint can actually build.
func TestRenderableScenarioAppTypesMatchesInstantiation(t *testing.T) {
	v2 := ScenarioTemplatePayload{}
	if got := renderableScenarioAppTypes([]string{"chat", "search", "agent"}, v2); len(got) != 2 || got[0] != "chat" || got[1] != "search" {
		t.Fatalf("v2 renderable=%v, want [chat search]", got)
	}

	canvas := &ScenarioPackRAGFlowExtension{CanvasDSL: map[string]interface{}{"components": map[string]interface{}{}}}
	agentPack := ScenarioTemplatePayload{Pack: &ScenarioPack{
		Schema: ScenarioPackSchemaV3, Application: ScenarioPackApplication{AppType: "agent"},
		RuntimeExtension: ScenarioPackRuntimeExtension{RAGFlow: canvas},
	}}
	if got := renderableScenarioAppTypes([]string{"chat", "agent"}, agentPack); len(got) != 1 || got[0] != "agent" {
		t.Fatalf("agent pack renderable=%v, want [agent]", got)
	}

	agentPackWithoutCanvas := ScenarioTemplatePayload{Pack: &ScenarioPack{
		Schema: ScenarioPackSchemaV3, Application: ScenarioPackApplication{AppType: "agent"},
	}}
	if got := renderableScenarioAppTypes([]string{"chat", "agent"}, agentPackWithoutCanvas); len(got) != 0 {
		t.Fatalf("canvas-less agent pack renderable=%v, want none", got)
	}

	chatPack := ScenarioTemplatePayload{Pack: &ScenarioPack{
		Schema: ScenarioPackSchemaV3, Application: ScenarioPackApplication{AppType: "chat"},
	}}
	if got := renderableScenarioAppTypes([]string{"chat", "search"}, chatPack); len(got) != 1 || got[0] != "chat" {
		t.Fatalf("chat pack renderable=%v, want [chat]", got)
	}
}

// TestScenarioTemplateCatalogExposesRenderableAppTypes keeps the catalog honest:
// the field the wizard gates buttons on must be present even for seeded v2 rows.
func TestScenarioTemplateCatalogExposesRenderableAppTypes(t *testing.T) {
	svc := newScenarioSeedService(t)
	ctx := context.Background()
	seedPlatformBaseline(t, svc)
	tenant, err := svc.CreateTenant(ctx, "Renderable Catalog Tenant")
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.ScenarioTemplateCatalog(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected seeded templates")
	}
	for _, item := range items {
		if len(item.RenderableAppTypes) == 0 {
			t.Errorf("%s: renderable_app_types is empty; the wizard would disable every form", item.Key)
		}
		for _, appType := range item.RenderableAppTypes {
			if !containsString(item.AppTypes, appType) {
				t.Errorf("%s: renderable app type %q is not declared in app_types%v", item.Key, appType, item.AppTypes)
			}
			if appType == "agent" {
				t.Errorf("%s: seeded v2 template must not advertise a renderable agent", item.Key)
			}
		}
	}
}

// TestCreateChatFailureSeparatesUpstreamRejection covers the drill's 50240: an
// upstream business refusal (HTTP 200 + envelope code) is actionable and must not
// be reported as a gateway failure.
func TestCreateChatFailureSeparatesUpstreamRejection(t *testing.T) {
	rejected := &ragflow.Error{HTTPStatus: 200, Code: 102, Message: "the dataset 69e2 doesn't own parsed file"}
	assertUpstreamHTTPError(t, createChatFailure(rejected), 400, 40085, "doesn't own parsed file")
	assertUpstreamHTTPError(t, createChatFailure(errors.New("dial tcp: connection refused")), 502, 50240, "")
	assertUpstreamHTTPError(t, createChatFailure(&ragflow.Error{HTTPStatus: 500, Message: "internal"}), 502, 50240, "")
}

// assertUpstreamHTTPError checks the (status, business code) pair and, when wantMsg is
// non-empty, that the message kept the upstream reason.
func assertUpstreamHTTPError(t *testing.T, err error, wantStatus, wantCode int, wantMsg string) {
	t.Helper()
	var httpErr *httperr.Error
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected *httperr.Error, got %T", err)
	}
	if httpErr.Status != wantStatus || httpErr.Code != wantCode {
		t.Fatalf("mapped to %d/%d, want %d/%d (%s)", httpErr.Status, httpErr.Code, wantStatus, wantCode, httpErr.Message)
	}
	if wantMsg != "" && !strings.Contains(httpErr.Message, wantMsg) {
		t.Fatalf("message %q dropped the upstream reason %q", httpErr.Message, wantMsg)
	}
}

// TestImportAgentSkeletonUsesAgentAppType is the live-drill regression for
// `agent_template:1`: the engine's canvas type ("deep_research",
// "task_oriented") is a catalogue bucket, and passing it as app_type made the
// import fail with 40041 before any asset was created.
func TestImportAgentSkeletonUsesAgentAppType(t *testing.T) {
	svc := newScenarioSeedService(t)
	svc.RAGFlow = agentTemplateEngine{templates: []ragflow.AgentTemplate{{
		ID: "tpl-deep-research", Title: map[string]interface{}{"en": "Deep research"},
		CanvasType: "deep_research", CanvasCategory: "dataflow",
		DSL: map[string]interface{}{"components": map[string]interface{}{
			"begin":                     map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin"}},
			"Agent:DeepResearchPlanner": map[string]interface{}{"obj": map[string]interface{}{"component_name": "Agent"}},
		}},
	}}}
	ctx := context.Background()
	asset, err := svc.ImportScenarioSkeleton(ctx, model.PlatformTenantID, actorID(t, svc), "agent_template:tpl-deep-research")
	if err != nil {
		t.Fatalf("agent skeleton import must succeed: %v", err)
	}
	if asset.AppTypes != "agent" {
		t.Fatalf("app_types=%q, want agent", asset.AppTypes)
	}
	payload, err := parseTemplatePayload(asset.PayloadJSON)
	if err != nil {
		t.Fatalf("stored payload does not parse: %v", err)
	}
	if payload.Pack == nil || payload.Pack.Schema != ScenarioPackSchemaV3 {
		t.Fatalf("the stored payload must be a v3 pack: %+v", payload.Pack)
	}
	if payload.Pack.RuntimeExtension.RAGFlow == nil || len(payload.Pack.RuntimeExtension.RAGFlow.CanvasDSL) == 0 {
		t.Fatal("the canvas DSL must survive the import")
	}
	if got := renderableScenarioAppTypes([]string{"agent"}, payload); len(got) != 1 || got[0] != "agent" {
		t.Fatalf("renderable=%v, want [agent]", got)
	}
	if asset.Source != model.TemplateSourceImport {
		t.Fatalf("source=%q, want imported", asset.Source)
	}
}

// The credential scan matches its pattern against key names, and `token` is one
// of its substrings: an RAGFlow agent DSL node carries `maxTokensEnabled` and
// `max_tokens` as ordinary model parameters. Rejecting those made every exported
// agent pack fail to import (40116) even though it held no credential at all.
func TestScanScenarioContentKeepsNonSecretTokenKeys(t *testing.T) {
	t.Parallel()
	pack := map[string]interface{}{
		"runtimeExtension": map[string]interface{}{
			"ragflow": map[string]interface{}{
				"canvasDsl": map[string]interface{}{
					"components": map[string]interface{}{
						"Agent:One": map[string]interface{}{
							"obj": map[string]interface{}{
								"params": map[string]interface{}{
									"maxTokensEnabled": true,
									"max_tokens":       float64(4096),
									"tokenLimit":       float64(0),
									"tokenEvent":       "",
								},
							},
						},
					},
				},
			},
		},
	}
	if err := scanScenarioContent(pack, "document.payload"); err != nil {
		t.Fatalf("model parameters were read as credentials: %v", err)
	}
}

func TestScanScenarioContentStillRejectsCredentials(t *testing.T) {
	t.Parallel()
	cases := map[string]interface{}{
		"apiKey":            "sk-live-123",
		"maxTokensOverride": "sk-live-123",
		"client_secret":     "shh",
		"authorization":     "Bearer abc",
		"tokens":            []interface{}{"sk-live-123"},
		"credentials":       map[string]interface{}{"user": "u"},
	}
	for key, value := range cases {
		pack := map[string]interface{}{"obj": map[string]interface{}{"params": map[string]interface{}{key: value}}}
		err := scanScenarioContent(pack, "document.payload")
		if err == nil {
			t.Fatalf("key %q was not rejected", key)
		}
		var apiErr *httperr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != 40116 {
			t.Fatalf("key %q produced %v, want 40116", key, err)
		}
	}
}
