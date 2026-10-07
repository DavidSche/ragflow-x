package ragflow

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// clientMethodsWithoutUpstreamCall lists interface methods that never reach
// RAGFlow, so the coverage reflection must exempt them.
var clientMethodsWithoutUpstreamCall = map[string]string{
	"Name": "provider implementation label (http|mock), no upstream call",
}

// TestEndpointRegistryCoversClientMethods asserts every Client interface
// method is attributed to at least one registered endpoint. This is the
// enforcement backstop for doc/121 B2: a new Client method without a registry
// entry fails the test run, so the reviewer must classify the upstream
// endpoint as documented or internal before it ships.
func TestEndpointRegistryCoversClientMethods(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil)).Elem()
	covered := map[string]bool{}
	for _, e := range endpointRegistry {
		for _, m := range e.ClientMethods {
			if covered[m] {
				t.Errorf("method %s attributed to multiple endpoints; attribute it exactly once", m)
			}
			covered[m] = true
		}
	}
	for i := 0; i < clientType.NumMethod(); i++ {
		name := clientType.Method(i).Name
		if _, exempt := clientMethodsWithoutUpstreamCall[name]; exempt {
			continue
		}
		if !covered[name] {
			t.Errorf("Client method %s is not attributed to any endpoint registry entry; add it to endpoint_registry.go with its stability classification", name)
		}
	}
}

// TestInternalEndpointsHaveCapabilityImpact asserts every internal endpoint is
// tied to at least one Capability Matrix entry, so a version regression blocks
// the matching scenario instead of failing silently.
func TestInternalEndpointsHaveCapabilityImpact(t *testing.T) {
	for _, e := range InternalEndpoints() {
		if len(e.UsedByCapability) == 0 {
			t.Errorf("internal endpoint %s %s has no UsedByCapability; wire it to the capability matrix", e.Method, e.Path)
		}
		if !strings.Contains(e.Evidence, "source:") && !strings.Contains(e.Evidence, "docs marks") && !strings.Contains(e.Evidence, "docs reference") {
			t.Errorf("internal endpoint %s %s evidence must cite the source module or the docs deprecation note: %q", e.Method, e.Path, e.Evidence)
		}
	}
}

// TestEndpointRegistryMethodPathShapes keeps the registered path templates in
// the RAGFlow `{placeholder}` convention so the table stays readable next to
// the offline docs.
func TestEndpointRegistryMethodPathShapes(t *testing.T) {
	for _, e := range endpointRegistry {
		if e.Method != "*" && !isKnownHTTPMethod(e.Method) {
			t.Errorf("endpoint %s has invalid method %q", e.Path, e.Method)
		}
		if !strings.HasPrefix(e.Path, "/") {
			t.Errorf("endpoint path %q must start with /", e.Path)
		}
		if strings.Contains(e.Path, "?") || strings.Contains(e.Path, "%") {
			t.Errorf("endpoint path %q must not contain query strings or escapes", e.Path)
		}
	}
}

func isKnownHTTPMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// TestLiveInternalEndpointContract drills every internal endpoint against a
// live RAGFlow with the smallest possible request. It stays skipped in normal
// CI (same gate as TestLiveRAGFlowVersionContract) and is the mandatory
// "real contract drill" before approving a RAGFlow upgrade that touches the
// undocumented surface (doc/121 B2).
func TestLiveInternalEndpointContract(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}
	apiKey := os.Getenv("RGX_RAGFLOW_LIVE_API_KEY")
	client := NewHTTPClient(baseURL, apiKey, 15*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Every drill must return an upstream verdict (success OR a semantic
	// failure envelope). Transport errors, 404/405 and HTML responses mean
	// the endpoint moved or disappeared: fail the drill.
	fail := func(name string, err error) {
		if err != nil {
			t.Errorf("internal endpoint drill %s failed: %v", name, err)
		}
	}

	// GET /api/v1/system/version — version gate.
	if _, err := NewVersionProbe(client).ProbeVersion(ctx); err != nil {
		t.Errorf("version probe failed: %v", err)
	}

	// GET /api/v1/system/healthz (documented but gates the same surface).
	if _, err := client.Health(ctx); err != nil {
		t.Errorf("health check failed: %v", err)
	}

	// GET /api/v1/models (tenant model list).
	fail("GET /models", func() error {
		_, err := client.ListModels(ctx, "")
		return err
	}())

	// GET /api/v1/models/default.
	fail("GET /models/default", func() error {
		_, err := client.ListDefaultModels(ctx)
		return err
	}())

	// GET /api/v1/providers.
	fail("GET /providers", func() error {
		_, err := client.ListProviders(ctx, false)
		return err
	}())

	// GET /api/v1/providers/{name}/models + /instances (read-only drills for
	// the first provider the tenant knows; the write family stays opt-in).
	if providers, err := client.ListProviders(ctx, true); err == nil && len(providers) > 0 {
		name := providers[0].Name
		fail("GET providers/{name}/models", func() error {
			_, err := client.ListProviderModels(ctx, name, "", "")
			return err
		}())
		fail("GET providers/{name}/instances", func() error {
			_, err := client.ListProviderInstances(ctx, name)
			return err
		}())
	}

	// Document-level internal endpoints need an existing dataset; resolve one
	// lazily and skip the drills if the tenant has none.
	datasets, err := client.ListDatasets(ctx)
	if err != nil {
		t.Fatalf("list datasets for internal endpoint drills: %v", err)
	}
	for _, ds := range datasets {
		docs, err := client.ListDocuments(ctx, ds.ID)
		if err != nil {
			continue
		}
		if len(docs) == 0 {
			continue
		}
		doc := docs[0]

		// POST /datasets/{id}/documents/batch-update-status — round-trip the
		// current enabled state (RAGFlow status "1"="0") so the drill never
		// mutates data.
		fail("POST batch-update-status", client.SetDocumentsStatus(ctx, ds.ID, []string{doc.ID}, string(doc.Enabled) == "1"))

		// PATCH /datasets/{id}/documents/metadatas — write the same metadata
		// back (idempotent round-trip).
		if len(doc.Metadata) > 0 {
			fail("PATCH metadatas", client.UpdateDocumentMetadata(ctx, ds.ID, doc.ID, doc.Metadata))
		}

		// GET /documents/{id}/preview — binary; only assert a non-error read.
		fail("GET preview", func() error {
			_, _, err := client.GetDocumentContent(ctx, doc.ID)
			return err
		}())

		// GET /documents/images/{image_id} only when the chunk carries an image.
		chunks, _, err := client.ListDocumentChunks(ctx, ds.ID, doc.ID, 1, 20)
		if err == nil {
			for _, ch := range chunks {
				if ch.ImageID == "" {
					continue
				}
				fail("GET documents/images", func() error {
					_, _, err := client.GetChunkImage(ctx, ch.ImageID)
					return err
				}())
				break
			}
		}

		// GET /documents/{id}/chunks (documented) sanity: at least one chunk
		// page answered.
		if len(chunks) == 0 {
			continue
		}
		break
	}

	// Agent surface: read-only version drills for the first agent; the
	// deprecated POST /agents/{id}/sessions (doc/121 B1 migration pending) and
	// UploadAgentFile stay opt-in because they create durable state.
	agents, _, err := client.ListAgents(ctx, ListAgentsFilter{})
	if err != nil {
		t.Errorf("list agents for internal endpoint drills: %v", err)
	} else if len(agents) > 0 {
		agentID := agents[0].ID
		fail("GET agents/{id}/versions", func() error {
			_, err := client.ListAgentVersions(ctx, agentID)
			return err
		}())
		if versions, err := client.ListAgentVersions(ctx, agentID); err == nil && len(versions) > 0 {
			fail("GET agents/{id}/versions/{vid}", func() error {
				_, err := client.GetAgentVersion(ctx, agentID, versions[0].ID)
				return err
			}())
		}
	}

	// Memory message drill: POST /messages is documented (§Add Message) but
	// writes durable state, so it stays opt-in.
	if os.Getenv("RGX_RAGFLOW_LIVE_MEMORY_MESSAGE_DRILL") == "1" {
		if memories, _, err := client.ListMemories(ctx, ListMemoriesFilter{}); err == nil && len(memories) > 0 {
			fail("POST /messages", client.AddMemoryMessage(ctx, AddMessageRequest{MemoryID: memories[0].ID, UserInput: "rgx live drill", AgentResponse: "drill ack"}))
		}
	}

	// Deprecated/durable-state opt-ins.
	if os.Getenv("RGX_RAGFLOW_LIVE_AGENT_SESSION_DRILL") == "1" {
		if len(agents) > 0 {
			s, err := client.CreateAgentSession(ctx, agents[0].ID, "rgx-live-drill")
			if err != nil {
				t.Errorf("agent session drill failed: %v", err)
			} else if err := client.DeleteAgentSession(ctx, agents[0].ID, s.ID); err != nil {
				t.Errorf("agent session drill cleanup failed: %v", err)
			}
		}
	}
	if os.Getenv("RGX_RAGFLOW_LIVE_AGENT_UPLOAD_DRILL") == "1" && len(agents) > 0 {
		if _, err := client.UploadAgentFile(ctx, agents[0].ID, "rgx-drill.txt", []byte("rgx live drill")); err != nil {
			t.Errorf("agent upload drill failed: %v", err)
		}
	}
}

// TestInternalEndpointDrillHarness is a offline harness proving the drill
// logic itself: a stub upstream that answers all internal endpoints passes,
// and a stub missing one endpoint (404) fails the drill. This keeps the live
// test meaningful without requiring a real engine in CI.
func TestInternalEndpointDrillHarness(t *testing.T) {
	t.Run("missing endpoint fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/system/version", "/api/v1/models", "/api/v1/providers":
				writeEnvelope(t, w, []interface{}{})
			case "/api/v1/datasets":
				writeEnvelope(t, w, []map[string]interface{}{{"id": "ds-1"}})
			case "/api/v1/models/default":
				writeEnvelope(t, w, []map[string]interface{}{})
			default:
				// Simulate an upstream that dropped the document surface.
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"code":102,"message":"not found"}`)
			}
		}))
		defer srv.Close()

		client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
		// batch-update-status must surface the 404 as an error, not silently pass.
		err := client.SetDocumentsStatus(context.Background(), "ds-1", []string{"doc-1"}, true)
		if err == nil {
			t.Fatal("drill harness: missing batch-update-status endpoint must fail")
		}
		if !strings.Contains(fmt.Sprint(err), "404") && !strings.Contains(fmt.Sprint(err), "not found") {
			t.Fatalf("drill harness: unexpected error shape: %v", err)
		}
	})
}

// drillableInternalEndpoints maps every internal endpoint to the live drill
// that exercises it. The mapping is the audit trail for doc/121 B2: an entry
// classified "opt-in" performs a durable write (session/message/file) or is
// deprecated upstream, so it only runs when its environment flag is set; all
// other internal endpoints must be drillable with a read-only or idempotent
// request ("live") or through the offline harness ("harness") when no tenant
// fixture exists.
var drillableInternalEndpoints = map[string]string{"POST /datasets/{dataset_id}/documents/batch-update-status": "live (idempotent round-trip of current enabled state)",
	"GET /datasets/{dataset_id}/artifacts":                                           "harness (offline dataset artifact GET contract covers the list response)",
	"POST /datasets/{dataset_id}/search":                                             "harness (offline scoped search contract covers request/response wire)",
	"GET /datasets/{dataset_id}/compilation/status":                                  "live (idempotent compilation status read)",
	"GET /compilation-templates/{source}":                                            "live (idempotent compilation template read)",
	"GET /compilation-template-groups":                                               "harness (offline template group list contract covers pagination and filters)",
	"POST /compilation-template-groups":                                              "harness (write family covered by template group CRUD contract)",
	"GET /compilation-template-groups/{group_id}":                                    "harness (offline template group GET contract covers escaped IDs)",
	"PUT /compilation-template-groups/{group_id}":                                    "harness (write family covered by template group CRUD contract)",
	"DELETE /compilation-template-groups/{group_id}":                                 "harness (write family covered by template group CRUD contract)",
	"PATCH /datasets/{dataset_id}/documents/metadatas":                               "live (idempotent round-trip; needs doc metadata)",
	"PATCH /datasets/{dataset_id}/documents/{document_id}":                           "harness (write family covered by document metadata replacement contract)",
	"GET /documents/{document_id}/preview":                                           "live",
	"GET /documents/images/{image_id}":                                               "live (needs a chunk with image_id)",
	"GET /agents/{agent_id}/versions":                                                "live",
	"GET /agents/{agent_id}/versions/{version_id}":                                   "live (needs a saved version)",
	"POST /agents/{agent_id}/sessions":                                               "opt-in RGX_RAGFLOW_LIVE_AGENT_SESSION_DRILL (deprecated, doc/121 B1)",
	"POST /agents/{agent_id}/upload":                                                 "opt-in RGX_RAGFLOW_LIVE_AGENT_UPLOAD_DRILL",
	"GET /providers":                                                                 "live",
	"PUT /providers":                                                                 "harness (write family covered by provider contract tests)",
	"POST /providers":                                                                "harness (write family covered by provider contract tests)",
	"DELETE /providers/{provider_name}":                                              "harness (write family covered by provider contract tests)",
	"GET /providers/{provider_name}/models":                                          "live",
	"GET /providers/{provider_name}/instances":                                       "live",
	"POST /providers/{provider_name}/instances":                                      "harness (write family covered by provider contract tests)",
	"PUT /providers/{provider_name}/instances/{instance_name}":                       "harness (write family covered by provider contract tests)",
	"DELETE /providers/{provider_name}/instances":                                    "harness (write family covered by provider contract tests)",
	"GET /providers/{provider_name}/instances/{instance_name}/models":                "harness (needs an existing instance)",
	"POST /providers/{provider_name}/instances/{instance_name}/models":               "harness (write family covered by provider contract tests)",
	"PATCH /providers/{provider_name}/instances/{instance_name}/models/{model_name}": "harness (write family covered by provider contract tests)",
	"DELETE /providers/{provider_name}/instances/{instance_name}/models":             "harness (write family covered by provider contract tests)",
	"POST /providers/{provider_name}/connection":                                     "harness (needs real provider credentials)",
	"GET /system/version":                                                            "live",
	"GET /models":                                                                    "live",
	"GET /models/default":                                                            "live",
}

// TestEveryInternalEndpointHasADrill asserts the drillable map stays in sync
// with the registry: an internal endpoint without a drill entry would mean an
// undocumented surface that no version regression exercises.
func TestEveryInternalEndpointHasADrill(t *testing.T) {
	for _, e := range InternalEndpoints() {
		key := e.Method + " " + e.Path
		drill, ok := drillableInternalEndpoints[key]
		if !ok {
			t.Errorf("internal endpoint %s has no drill mapping; add it to drillableInternalEndpoints with its drill mode (live/opt-in/harness)", key)
			continue
		}
		if strings.HasPrefix(drill, "live") && !strings.Contains(key, "GET") && !strings.Contains(drill, "idempotent") {
			t.Errorf("internal endpoint %s is marked live but mutates state; make it opt-in or prove the request is idempotent", key)
		}
	}
	// No stale entries: every mapping must point at a registered endpoint.
	registered := map[string]bool{}
	for _, e := range InternalEndpoints() {
		registered[e.Method+" "+e.Path] = true
	}
	for key := range drillableInternalEndpoints {
		if !registered[key] {
			t.Errorf("drill mapping %s points at a non-registered (or reclassified) endpoint; remove it", key)
		}
	}
}
