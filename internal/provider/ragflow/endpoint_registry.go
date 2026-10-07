package ragflow

// EndpointStability classifies how each upstream RAGFlow endpoint used by this
// provider is anchored. The registry is the review surface for RAGFlow
// upgrades: every endpoint below must be re-checked against the offline docs
// snapshot (currently v0.27.2) and, where applicable, the live contract tests
// in endpoint_registry_contract_test.go.
//
// Sources:
//   - documented: the endpoint exists in the RAGFlow HTTP API Reference
//     (offline docs snapshot v0.27.2, ragflow-offline-doc/pages/http_api_reference.html).
//   - internal: the endpoint was learned from the RAGFlow source tree and is
//     not (yet) part of the published API reference. These carry no upstream
//     stability guarantee and MUST stay covered by the Capability Matrix
//     version gate plus live contract drills before production upgrades.
//
// Keep this table in sync when adding new Client methods; the contract test in
// endpoint_registry_contract_test.go enforces that every Client method maps to
// a registered endpoint.
type EndpointStability string

const (
	// EndpointDocumented appears in the official HTTP API Reference.
	EndpointDocumented EndpointStability = "documented"
	// EndpointInternal was learned from source; no documented stability
	// guarantee. Requires a live contract drill on every RAGFlow upgrade.
	EndpointInternal EndpointStability = "internal"
)

// EndpointRecord is one registered upstream endpoint.
type EndpointRecord struct {
	// Method is the HTTP verb used by the provider ("*" covers helpers that
	// fan out to multiple verbs on the same path).
	Method string
	// Path is the upstream path relative to the API root (normally /api/v1),
	// written with RAGFlow's `{placeholder}` convention. Query strings are
	// omitted; pagination/filter parameters are not part of the identity.
	Path string
	// Stability anchors the endpoint per the definitions above.
	Stability EndpointStability
	// Evidence points at the offline docs section or the source module the
	// endpoint was learned from, so a reviewer can re-verify quickly.
	Evidence string
	// ClientMethods lists the provider methods that reach this endpoint.
	ClientMethods []string
	// UsedByCapability names the Capability Matrix entries that depend on
	// this endpoint, so a regression blocks the matching scenario.
	UsedByCapability []string
}

// endpointRegistry is the authoritative list. Order: datasets, documents,
// chunks, chats, agents, memory, search apps, models/providers, misc.
var endpointRegistry = []EndpointRecord{
	// ── Datasets (documented: HTTP API Reference §Dataset Management) ──
	{Method: "POST", Path: "/datasets", Stability: EndpointDocumented, Evidence: "docs §Create dataset", ClientMethods: []string{"CreateDataset"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/datasets", Stability: EndpointDocumented, Evidence: "docs §List datasets", ClientMethods: []string{"ListDatasets"}, UsedByCapability: []string{"chat"}},
	{Method: "PUT", Path: "/datasets/{dataset_id}", Stability: EndpointDocumented, Evidence: "docs §Update dataset", ClientMethods: []string{"UpdateDatasetConfig"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/datasets/{dataset_id}", Stability: EndpointDocumented, Evidence: "docs §List datasets (id filter) / Update dataset response; source-learned GET form", ClientMethods: []string{"GetDatasetConfig"}, UsedByCapability: []string{"chat"}},
	{Method: "DELETE", Path: "/datasets", Stability: EndpointDocumented, Evidence: "docs §Delete datasets (ids array; delete_all must never be sent)", ClientMethods: []string{"DeleteDataset"}, UsedByCapability: []string{"chat"}},

	// ── Documents (documented: §Upload/Update/Download/List/Delete/Parse/Stop) ──
	{Method: "POST", Path: "/datasets/{dataset_id}/documents", Stability: EndpointDocumented, Evidence: "docs §Upload documents", ClientMethods: []string{"CreateDocument"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/datasets/{dataset_id}/documents", Stability: EndpointDocumented, Evidence: "docs §List documents (paged); metadata read source for per-document meta_fields (doc/129 §P0-B)", ClientMethods: []string{"ListDocuments", "GetDatasetDocumentMetadata"}, UsedByCapability: []string{"chat"}},
	{Method: "DELETE", Path: "/datasets/{dataset_id}/documents", Stability: EndpointDocumented, Evidence: "docs §Delete documents", ClientMethods: []string{"DeleteDocuments"}, UsedByCapability: []string{"chat"}},
	{Method: "POST", Path: "/datasets/{dataset_id}/documents/parse", Stability: EndpointDocumented, Evidence: "docs §Parse documents", ClientMethods: []string{"ParseDocuments"}, UsedByCapability: []string{"chat"}},
	{Method: "POST", Path: "/datasets/{dataset_id}/documents/stop", Stability: EndpointDocumented, Evidence: "docs §Stop parsing documents", ClientMethods: []string{"StopDocuments"}, UsedByCapability: []string{"chat"}},

	// ── Documents: internal endpoints learned from the RAGFlow source tree ──
	{Method: "POST", Path: "/datasets/{dataset_id}/documents/batch-update-status", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py (not in docs v0.27.2)", ClientMethods: []string{"SetDocumentsStatus"}, UsedByCapability: []string{"chat"}},
	{Method: "PATCH", Path: "/datasets/{dataset_id}/documents/metadatas", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py (docs only list dataset-level POST /datasets/{id}/metadata/update); UpdateDocumentMetadata fans out to GET documents + this PATCH", ClientMethods: []string{"UpdateDocumentMetadata"}, UsedByCapability: []string{"chat"}},
	{Method: "PATCH", Path: "/datasets/{dataset_id}/documents/{document_id}", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py replaceDocumentMetadata; required for version publish/rollback meta_fields full replacement (doc/129 §P0-B)", ClientMethods: []string{"ReplaceDatasetDocumentMetadata"}, UsedByCapability: []string{"chat"}},
	{Method: "POST", Path: "/datasets/{dataset_id}/metadata/update", Stability: EndpointDocumented, Evidence: "docs §Update or delete metadata (selector + updates/deletes); batch backfill channel for retrieval pushdown (doc/123 §5.2)", ClientMethods: []string{"BatchUpdateDatasetMetadata"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/documents/{document_id}/preview", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py preview route (not in docs v0.27.2)", ClientMethods: []string{"GetDocumentContent"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/documents/images/{image_id}", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py image route; critical for PDF citation image embedding (doc/117)", ClientMethods: []string{"GetChunkImage"}, UsedByCapability: []string{"chat"}},
	{Method: "POST", Path: "/agents/{agent_id}/upload", Stability: EndpointInternal, Evidence: "docs reference legacy /v1/canvas/upload/{agent_id} as agent-file workaround; provider uses source-learned /api/v1 form", ClientMethods: []string{"UploadAgentFile"}, UsedByCapability: []string{"agentic_task"}},

	// ── Chunks (documented: §Chunk Management) ──
	{Method: "GET", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks", Stability: EndpointDocumented, Evidence: "docs §List chunks (paged)", ClientMethods: []string{"ListDocumentChunks"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks/{chunk_id}", Stability: EndpointDocumented, Evidence: "docs §Get chunk", ClientMethods: []string{"GetChunk"}, UsedByCapability: []string{"chat"}},
	{Method: "DELETE", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks", Stability: EndpointDocumented, Evidence: "docs §Delete chunks", ClientMethods: []string{"DeleteChunks"}, UsedByCapability: []string{"chat"}},
	{Method: "PATCH", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks", Stability: EndpointDocumented, Evidence: "docs §Update chunk availability (available_int)", ClientMethods: []string{"SetChunksAvailable"}, UsedByCapability: []string{"chat"}},
	{Method: "POST", Path: "/retrieval", Stability: EndpointDocumented, Evidence: "docs §Retrieve chunks; generic multi-dataset retrieval with metadata_condition (doc/129 §P1-C)", ClientMethods: []string{"RetrieveDatasets"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "POST", Path: "/datasets/{dataset_id}/search", Stability: EndpointInternal, Evidence: "source: ragflow-src/api/apps/restful_apis/dataset_api.py search and ragflow-src/api/utils/validation_utils.py SearchDatasetReq; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"SearchDataset"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "GET", Path: "/datasets/{dataset_id}/artifacts", Stability: EndpointInternal, Evidence: "source: api/apps/restful_apis/dataset_api.py list_wiki_pages; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"ListDatasetArtifacts"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "GET", Path: "/datasets/{dataset_id}/compilation/status", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/dataset.go GetCompilationStatus; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"GetCompilationStatus"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "GET", Path: "/compilation-templates/{source}", Stability: EndpointInternal, Evidence: "source: ragflow-src/api/apps/restful_apis/compilation_template_api.py and ragflow-src/internal/handler/compilation_template.go; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"ListCompilationTemplates"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "GET", Path: "/compilation-template-groups", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go List and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"ListCompilationTemplateGroups"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "POST", Path: "/compilation-template-groups", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Save and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"SaveCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "GET", Path: "/compilation-template-groups/{group_id}", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Get and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"GetCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "PUT", Path: "/compilation-template-groups/{group_id}", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Update and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"UpdateCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}},
	{Method: "DELETE", Path: "/compilation-template-groups/{group_id}", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Delete and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"DeleteCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}},

	// ── Chat assistants (documented: §Chat Assistant Management) ──
	{Method: "GET", Path: "/chats", Stability: EndpointDocumented, Evidence: "docs §List chat assistants", ClientMethods: []string{"ListChats"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/chats/{chat_id}", Stability: EndpointDocumented, Evidence: "docs §Get chat assistant", ClientMethods: []string{"GetChat"}, UsedByCapability: []string{"chat"}},
	{Method: "POST", Path: "/chats", Stability: EndpointDocumented, Evidence: "docs §Create chat assistant", ClientMethods: []string{"CreateChat"}, UsedByCapability: []string{"chat"}},
	{Method: "PUT", Path: "/chats/{chat_id}", Stability: EndpointDocumented, Evidence: "docs §Update chat assistant (full replace; PATCH for partial)", ClientMethods: []string{"UpdateChat"}, UsedByCapability: []string{"chat"}},
	{Method: "DELETE", Path: "/chats/{chat_id}", Stability: EndpointDocumented, Evidence: "docs §Delete chat assistant", ClientMethods: []string{"DeleteChat"}, UsedByCapability: []string{"chat"}},

	// ── Chat sessions (documented: §Session Management) ──
	{Method: "GET", Path: "/chats/{chat_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §List chat assistant's sessions", ClientMethods: []string{"ListChatSessions"}, UsedByCapability: []string{"session"}},
	{Method: "GET", Path: "/chats/{chat_id}/sessions/{session_id}", Stability: EndpointDocumented, Evidence: "docs §Get chat assistant's session", ClientMethods: []string{"GetChatSession", "ListChatSessionMessages", "ListSessionMessages"}, UsedByCapability: []string{"session"}},
	{Method: "POST", Path: "/chats/{chat_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §Create session with chat assistant", ClientMethods: []string{"CreateChatSession"}, UsedByCapability: []string{"session"}},
	{Method: "PATCH", Path: "/chats/{chat_id}/sessions/{session_id}", Stability: EndpointDocumented, Evidence: "docs deprecated table: PUT .../sessions replaced by PATCH", ClientMethods: []string{"UpdateChatSession"}, UsedByCapability: []string{"session"}},
	{Method: "DELETE", Path: "/chats/{chat_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §Delete chat assistant's sessions", ClientMethods: []string{"DeleteChatSessions"}, UsedByCapability: []string{"session"}},

	// ── Chat completion (documented: unified endpoint) ──
	// NOTE (doc/123 §9): X sends metadata_condition on the top-level body of
	// the unified endpoint. The OpenAI-Compatible variant documents it inside
	// extra_body; the top-level form is source-aligned and pending live-drill
	// confirmation. The endpoint itself is documented; the field usage is the
	// only undocumented aspect.
	{Method: "POST", Path: "/chat/completions", Stability: EndpointDocumented, Evidence: "docs §Converse with chat assistant (legacy chats/{id}/completions deprecated)", ClientMethods: []string{"ChatCompletion", "StreamChatCompletion"}, UsedByCapability: []string{"chat", "stream", "references"}},

	// ── Temporary upload (documented via deprecated alias table) ──
	{Method: "POST", Path: "/documents/upload", Stability: EndpointDocumented, Evidence: "docs deprecated table: POST /v1/document/upload_info -> POST /api/v1/documents/upload", ClientMethods: []string{"UploadChatFile"}, UsedByCapability: []string{"chat"}},

	// ── Agents (documented: §Agent Management) ──
	{Method: "GET", Path: "/agents", Stability: EndpointDocumented, Evidence: "docs §List agents", ClientMethods: []string{"ListAgents"}, UsedByCapability: []string{"agent"}},
	{Method: "GET", Path: "/agents/{agent_id}", Stability: EndpointDocumented, Evidence: "docs §List agents (id filter); source-learned single GET form", ClientMethods: []string{"GetAgent"}, UsedByCapability: []string{"agent"}},
	{Method: "POST", Path: "/agents", Stability: EndpointDocumented, Evidence: "docs §Create agent", ClientMethods: []string{"CreateAgent"}, UsedByCapability: []string{"agent"}},
	{Method: "PUT", Path: "/agents/{agent_id}", Stability: EndpointDocumented, Evidence: "docs §Update agent", ClientMethods: []string{"UpdateAgent"}, UsedByCapability: []string{"agent"}},
	{Method: "DELETE", Path: "/agents/{agent_id}", Stability: EndpointDocumented, Evidence: "docs §Delete agent", ClientMethods: []string{"DeleteAgent"}, UsedByCapability: []string{"agent"}},

	// ── Agent versions: internal endpoints learned from source ──
	{Method: "GET", Path: "/agents/{agent_id}/versions", Stability: EndpointInternal, Evidence: "source: agent/canvas version routes (not in docs v0.27.2)", ClientMethods: []string{"ListAgentVersions"}, UsedByCapability: []string{"agent"}},
	{Method: "GET", Path: "/agents/{agent_id}/versions/{version_id}", Stability: EndpointInternal, Evidence: "source: agent/canvas version routes (not in docs v0.27.2)", ClientMethods: []string{"GetAgentVersion", "RollbackAgentVersion"}, UsedByCapability: []string{"agent"}},
	// RollbackAgentVersion is a client-side composite (GetAgentVersion +
	// UpdateAgent); it performs no upstream call of its own, so it is
	// attributed to the GET version endpoint that fetches the DSL.

	// ── Agent sessions: create is DOCUMENTED-DEPRECATED, others documented ──
	{Method: "POST", Path: "/agents/{agent_id}/sessions", Stability: EndpointInternal, Evidence: "docs marks POST /agents/{id}/sessions DEPRECATED in v0.27.2: 'Use Converse with agent instead'; treated as internal pending migration (doc/121 B1)", ClientMethods: []string{"CreateAgentSession"}, UsedByCapability: []string{"agent_session"}},
	{Method: "GET", Path: "/agents/{agent_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §List agent sessions", ClientMethods: []string{"ListAgentSessions"}, UsedByCapability: []string{"agent_session"}},
	{Method: "GET", Path: "/agents/{agent_id}/sessions/{session_id}", Stability: EndpointDocumented, Evidence: "docs §List agent sessions (id filter); source-learned single GET form", ClientMethods: []string{"GetAgentSession", "ListAgentSessionMessages"}, UsedByCapability: []string{"agent_session"}},
	{Method: "DELETE", Path: "/agents/{agent_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §Delete agent's sessions (ids body)", ClientMethods: []string{"DeleteAgentSession"}, UsedByCapability: []string{"agent_session"}},
	{Method: "POST", Path: "/agents/chat/completions", Stability: EndpointDocumented, Evidence: "docs §Converse with agent (openai-compatible mode per deprecated alias table)", ClientMethods: []string{"AgentChatCompletion", "StreamAgentChatCompletion"}, UsedByCapability: []string{"agent", "agent_session", "stream"}},

	// ── Memory (documented: §Memory Management) ──
	{Method: "GET", Path: "/memories", Stability: EndpointDocumented, Evidence: "docs §List Memory", ClientMethods: []string{"ListMemories"}, UsedByCapability: []string{"memory"}},
	{Method: "POST", Path: "/memories", Stability: EndpointDocumented, Evidence: "docs §Create Memory", ClientMethods: []string{"CreateMemory"}, UsedByCapability: []string{"memory"}},
	{Method: "PUT", Path: "/memories/{memory_id}", Stability: EndpointDocumented, Evidence: "docs §Update Memory", ClientMethods: []string{"UpdateMemory"}, UsedByCapability: []string{"memory"}},
	{Method: "DELETE", Path: "/memories/{memory_id}", Stability: EndpointDocumented, Evidence: "docs §Delete Memory", ClientMethods: []string{"DeleteMemory"}, UsedByCapability: []string{"memory"}},
	{Method: "GET", Path: "/memories/{memory_id}/config", Stability: EndpointDocumented, Evidence: "docs §Get Memory Config", ClientMethods: []string{"GetMemoryConfig"}, UsedByCapability: []string{"memory"}},
	{Method: "GET", Path: "/memories/{memory_id}", Stability: EndpointDocumented, Evidence: "docs §List messages of a memory", ClientMethods: []string{"ListMemoryMessages"}, UsedByCapability: []string{"memory"}},
	{Method: "POST", Path: "/messages", Stability: EndpointDocumented, Evidence: "docs §Add Message (body: memory_id array, agent_id, session_id, user_input, agent_response)", ClientMethods: []string{"AddMemoryMessage"}, UsedByCapability: []string{"memory"}},
	{Method: "*", Path: "/messages/{memory_id}:{message_id}", Stability: EndpointDocumented, Evidence: "docs §Delete Message / Update message status (DELETE + PUT)", ClientMethods: []string{"DeleteMemoryMessage", "UpdateMemoryMessageStatus"}, UsedByCapability: []string{"memory"}},
	{Method: "GET", Path: "/messages/search", Stability: EndpointDocumented, Evidence: "docs §Search Message", ClientMethods: []string{"SearchMemoryMessages"}, UsedByCapability: []string{"memory"}},
	{Method: "GET", Path: "/messages/{memory_id}:{message_id}/content", Stability: EndpointDocumented, Evidence: "docs §Get Message Content", ClientMethods: []string{"GetMemoryMessageContent"}, UsedByCapability: []string{"memory"}},

	// ── Search apps (documented: §Search App Management) ──
	{Method: "GET", Path: "/searches", Stability: EndpointDocumented, Evidence: "docs §List search apps", ClientMethods: []string{"ListSearchApps"}, UsedByCapability: []string{"search_app"}},
	{Method: "POST", Path: "/searches", Stability: EndpointDocumented, Evidence: "docs §Create search app", ClientMethods: []string{"CreateSearchApp"}, UsedByCapability: []string{"search_app"}},
	{Method: "GET", Path: "/searches/{search_id}", Stability: EndpointDocumented, Evidence: "docs §Get search app", ClientMethods: []string{"GetSearchApp"}, UsedByCapability: []string{"search_app"}},
	{Method: "PUT", Path: "/searches/{search_id}", Stability: EndpointDocumented, Evidence: "docs §Update search app", ClientMethods: []string{"UpdateSearchApp"}, UsedByCapability: []string{"search_app"}},
	{Method: "DELETE", Path: "/searches/{search_id}", Stability: EndpointDocumented, Evidence: "docs §Delete search app", ClientMethods: []string{"DeleteSearchApp"}, UsedByCapability: []string{"search_app"}},
	{Method: "POST", Path: "/searches/{search_id}/completions", Stability: EndpointDocumented, Evidence: "docs §Search completion", ClientMethods: []string{"SearchAppCompletion", "StreamSearchAppCompletion"}, UsedByCapability: []string{"search_app"}},

	// ── Models & providers: ALL internal, learned from source ──
	// docs v0.27.2 §Models covers client-side provider configuration only;
	// the /providers management API is not part of the published reference
	// and 0.26 refactored it ("API & Model Provider Refactoring"), so treat
	// the whole family as unstable.
	{Method: "GET", Path: "/providers", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py (docs v0.27.2 do not document it; 0.26 refactored this family)", ClientMethods: []string{"ListProviders"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "PUT", Path: "/providers", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"UpsertModelProvider"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "POST", Path: "/providers", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"AddProvider"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "DELETE", Path: "/providers/{provider_name}", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"DeleteProvider"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "GET", Path: "/providers/{provider_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"ListProviderModels"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "GET", Path: "/providers/{provider_name}/instances", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"ListProviderInstances"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "POST", Path: "/providers/{provider_name}/instances", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"CreateProviderInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "PUT", Path: "/providers/{provider_name}/instances/{instance_name}", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"UpdateProviderInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "DELETE", Path: "/providers/{provider_name}/instances", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"DeleteProviderInstances"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "GET", Path: "/providers/{provider_name}/instances/{instance_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"ListInstanceModels"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "POST", Path: "/providers/{provider_name}/instances/{instance_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"AddModelToInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "PATCH", Path: "/providers/{provider_name}/instances/{instance_name}/models/{model_name}", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"UpdateModel"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "DELETE", Path: "/providers/{provider_name}/instances/{instance_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"DeleteModelsFromInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}},
	{Method: "POST", Path: "/providers/{provider_name}/connection", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"VerifyConnection", "ChatToModel"}, UsedByCapability: []string{"chat", "agent", "search_app"}},

	// ── Misc ──
	{Method: "GET", Path: "/system/healthz", Stability: EndpointDocumented, Evidence: "docs §System Check (legacy /v1/system/healthz deprecated)", ClientMethods: []string{"Health"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/system/version", Stability: EndpointInternal, Evidence: "source: version probe (not in docs v0.27.2); startup version gate depends on it", ClientMethods: []string{"EngineVersion", "VersionProbe.ProbeVersion"}, UsedByCapability: []string{"chat", "agent", "agent_session", "search_app", "memory", "session", "references", "stream"}},
	{Method: "GET", Path: "/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py (tenant LLM list)", ClientMethods: []string{"ListModels"}, UsedByCapability: []string{"chat"}},
	{Method: "GET", Path: "/models/default", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py (tenant default chat model)", ClientMethods: []string{"ListDefaultModels"}, UsedByCapability: []string{"chat"}},
}

// EndpointRegistry returns the registered upstream endpoints.
func EndpointRegistry() []EndpointRecord {
	out := make([]EndpointRecord, len(endpointRegistry))
	copy(out, endpointRegistry)
	return out
}

// InternalEndpoints returns every endpoint without a documented stability
// guarantee. These are the review focus of doc/121 B2.
func InternalEndpoints() []EndpointRecord {
	var out []EndpointRecord
	for _, e := range endpointRegistry {
		if e.Stability == EndpointInternal {
			out = append(out, e)
		}
	}
	return out
}

// EndpointCoverage summarizes the registry for the Capability Matrix report
// and the system capability panel.
type EndpointCoverage struct {
	Total      int
	Documented int
	Internal   int
}

// EndpointCoverageSummary computes registry totals.
func EndpointCoverageSummary() EndpointCoverage {
	c := EndpointCoverage{Total: len(endpointRegistry)}
	for _, e := range endpointRegistry {
		if e.Stability == EndpointInternal {
			c.Internal++
		} else {
			c.Documented++
		}
	}
	return c
}
