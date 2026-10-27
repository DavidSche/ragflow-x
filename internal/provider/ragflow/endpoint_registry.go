package ragflow

import "regexp"

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
	// MinVersion is the lowest upstream RAGFlow release this endpoint is
	// known to serve reliably ("" = unclassified; T6.2 deliberately left it
	// empty for every entry rather than over-claim a floor without evidence,
	// 223 T6 / 224 §2.4). The 版本×能力×契约 preflight (T6.3) therefore degrades
	// conservatively below the verified band instead of trusting an unproven
	// floor.
	MinVersion string
	// MaxVerified is the newest upstream release this endpoint has been
	// verified against. T6.2 populated every entry with v1.0.0-rc1: X's
	// production gateway runs on the 151 rc1 engine and every Client method
	// here is exercised end-to-end there (production call surface + live
	// contract drills in endpoint_registry_contract_test.go + the D1
	// three-arm measurements, doc/222 §12). "" = never verified to a release.
	MaxVerified string
}

// endpointRegistry is the authoritative list. Order: datasets, documents,
// chunks, chats, agents, memory, search apps, models/providers, misc.
var endpointRegistry = []EndpointRecord{
	// ── Datasets (documented: HTTP API Reference §Dataset Management) ──
	{Method: "POST", Path: "/datasets", Stability: EndpointDocumented, Evidence: "docs §Create dataset", ClientMethods: []string{"CreateDataset"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/datasets", Stability: EndpointDocumented, Evidence: "docs §List datasets", ClientMethods: []string{"ListDatasets"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/datasets/{dataset_id}", Stability: EndpointDocumented, Evidence: "docs §Update dataset", ClientMethods: []string{"UpdateDatasetConfig"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/datasets/{dataset_id}", Stability: EndpointDocumented, Evidence: "docs §List datasets (id filter) / Update dataset response; source-learned GET form", ClientMethods: []string{"GetDatasetConfig"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/datasets", Stability: EndpointDocumented, Evidence: "docs §Delete datasets (ids array; delete_all must never be sent)", ClientMethods: []string{"DeleteDataset"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},

	// ── Documents (documented: §Upload/Update/Download/List/Delete/Parse/Stop) ──
	{Method: "GET", Path: "/datasets/{dataset_id}/graph", Stability: EndpointDocumented, Evidence: "docs §Retrieve knowledge graph; upstream handler dataset.go GetKnowledgeGraph", ClientMethods: []string{"GetDatasetGraph"}, UsedByCapability: []string{"knowledge-ops"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/datasets/{dataset_id}/documents", Stability: EndpointDocumented, Evidence: "docs §Upload documents", ClientMethods: []string{"CreateDocument"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/datasets/{dataset_id}/documents", Stability: EndpointDocumented, Evidence: "docs §List documents (paged); metadata read source for per-document meta_fields (doc/129 §P0-B)", ClientMethods: []string{"ListDocuments", "GetDatasetDocumentMetadata"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/datasets/{dataset_id}/documents", Stability: EndpointDocumented, Evidence: "docs §Delete documents", ClientMethods: []string{"DeleteDocuments"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/datasets/{dataset_id}/documents/parse", Stability: EndpointDocumented, Evidence: "docs §Parse documents", ClientMethods: []string{"ParseDocuments"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/documents/ingest", Stability: EndpointDocumented, Evidence: "docs §Ingest documents; required for pipeline-bound ingestion", ClientMethods: []string{"IngestDocuments"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/datasets/{dataset_id}/documents/stop", Stability: EndpointDocumented, Evidence: "docs §Stop parsing documents", ClientMethods: []string{"StopDocuments"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},

	// ── Documents: internal endpoints learned from the RAGFlow source tree ──
	{Method: "POST", Path: "/datasets/{dataset_id}/documents/batch-update-status", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py (not in docs v0.27.2)", ClientMethods: []string{"SetDocumentsStatus"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PATCH", Path: "/datasets/{dataset_id}/documents/metadatas", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py (docs only list dataset-level POST /datasets/{id}/metadata/update); UpdateDocumentMetadata fans out to GET documents + this PATCH", ClientMethods: []string{"UpdateDocumentMetadata"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PATCH", Path: "/datasets/{dataset_id}/documents/{document_id}", Stability: EndpointInternal, Evidence: "source: ragflow v1.0.0-rc1 internal/service/document/document_dataset_update.go UpdateDatasetDocument; supports meta_fields replacement and parse_type/parser_id/pipeline_id/parser_config", ClientMethods: []string{"ReplaceDatasetDocumentMetadata", "UpdateDocumentParseConfig"}, UsedByCapability: []string{"chat", "ingestion_contract"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/datasets/{dataset_id}/metadata/update", Stability: EndpointDocumented, Evidence: "docs §Update or delete metadata (selector + updates/deletes); batch backfill channel for retrieval pushdown (doc/123 §5.2)", ClientMethods: []string{"BatchUpdateDatasetMetadata"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/documents/{document_id}/preview", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py preview route (not in docs v0.27.2)", ClientMethods: []string{"GetDocumentContent"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/documents/images/{image_id}", Stability: EndpointInternal, Evidence: "source: api/apps/document_app.py image route; critical for PDF citation image embedding (doc/117)", ClientMethods: []string{"GetChunkImage"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/agents/{agent_id}/upload", Stability: EndpointInternal, Evidence: "docs reference legacy /v1/canvas/upload/{agent_id} as agent-file workaround; provider uses source-learned /api/v1 form", ClientMethods: []string{"UploadAgentFile"}, UsedByCapability: []string{"agentic_task"}, MaxVerified: "v1.0.0-rc1"},

	// ── Chunks (documented: §Chunk Management) ──
	{Method: "GET", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks", Stability: EndpointDocumented, Evidence: "docs §List chunks (paged)", ClientMethods: []string{"ListDocumentChunks"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks/{chunk_id}", Stability: EndpointDocumented, Evidence: "docs §Get chunk", ClientMethods: []string{"GetChunk"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks", Stability: EndpointDocumented, Evidence: "docs §Delete chunks", ClientMethods: []string{"DeleteChunks"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PATCH", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks", Stability: EndpointDocumented, Evidence: "docs §Update chunk availability (available_int)", ClientMethods: []string{"SetChunksAvailable"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks", Stability: EndpointDocumented, Evidence: "docs §Add chunk (content required)", ClientMethods: []string{"AddChunk"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/datasets/{dataset_id}/documents/{document_id}/chunks/{chunk_id}", Stability: EndpointDocumented, Evidence: "docs §Update chunk (content/keywords/questions/available)", ClientMethods: []string{"UpdateChunk"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/retrieval", Stability: EndpointDocumented, Evidence: "docs §Retrieve chunks; generic multi-dataset retrieval with metadata_condition (doc/129 §P1-C)", ClientMethods: []string{"RetrieveDatasets"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/datasets/{dataset_id}/search", Stability: EndpointInternal, Evidence: "source: ragflow-src/api/apps/restful_apis/dataset_api.py search and ragflow-src/api/utils/validation_utils.py SearchDatasetReq; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"SearchDataset"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/datasets/{dataset_id}/artifacts", Stability: EndpointInternal, Evidence: "source: api/apps/restful_apis/dataset_api.py list_wiki_pages; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"ListDatasetArtifacts"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/datasets/{dataset_id}/artifacts/{page_type}/{slug}", Stability: EndpointInternal, Evidence: "source: api/apps/restful_apis/dataset_api.py get_wiki_page; verified in local ragflow-src", ClientMethods: []string{"GetDatasetArtifact"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/datasets/{dataset_id}/compilation/status", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/dataset.go GetCompilationStatus; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"GetCompilationStatus", "GetCompilationStatusForKind"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/compilation-templates/{source}", Stability: EndpointInternal, Evidence: "source: ragflow-src/api/apps/restful_apis/compilation_template_api.py and ragflow-src/internal/handler/compilation_template.go; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"ListCompilationTemplates"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/compilation-template-groups", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go List and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"ListCompilationTemplateGroups"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/compilation-template-groups", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Save and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"SaveCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/compilation-template-groups/{group_id}", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Get and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"GetCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/compilation-template-groups/{group_id}", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Update and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"UpdateCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/compilation-template-groups/{group_id}", Stability: EndpointInternal, Evidence: "source: ragflow-src/internal/handler/compilation_template_group.go Delete and ragflow-src/api/apps/restful_apis/compilation_template_group_api.py; verified in local ragflow-src (doc/129 §P1-C)", ClientMethods: []string{"DeleteCompilationTemplateGroup"}, UsedByCapability: []string{"knowledge_strategy"}, MaxVerified: "v1.0.0-rc1"},

	// ── Chat assistants (documented: §Chat Assistant Management) ──
	{Method: "GET", Path: "/chats", Stability: EndpointDocumented, Evidence: "docs §List chat assistants", ClientMethods: []string{"ListChats"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/chats/{chat_id}", Stability: EndpointDocumented, Evidence: "docs §Get chat assistant", ClientMethods: []string{"GetChat"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/chats", Stability: EndpointDocumented, Evidence: "docs §Create chat assistant", ClientMethods: []string{"CreateChat"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/chats/{chat_id}", Stability: EndpointDocumented, Evidence: "docs §Update chat assistant (full replace; PATCH for partial)", ClientMethods: []string{"UpdateChat"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/chats/{chat_id}", Stability: EndpointDocumented, Evidence: "docs §Delete chat assistant", ClientMethods: []string{"DeleteChat"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},

	// ── Chat sessions (documented: §Session Management) ──
	{Method: "GET", Path: "/chats/{chat_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §List chat assistant's sessions", ClientMethods: []string{"ListChatSessions"}, UsedByCapability: []string{"session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/chats/{chat_id}/sessions/{session_id}", Stability: EndpointDocumented, Evidence: "docs §Get chat assistant's session", ClientMethods: []string{"GetChatSession", "ListChatSessionMessages", "ListSessionMessages"}, UsedByCapability: []string{"session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/chats/{chat_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §Create session with chat assistant", ClientMethods: []string{"CreateChatSession"}, UsedByCapability: []string{"session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PATCH", Path: "/chats/{chat_id}/sessions/{session_id}", Stability: EndpointDocumented, Evidence: "docs deprecated table: PUT .../sessions replaced by PATCH", ClientMethods: []string{"UpdateChatSession"}, UsedByCapability: []string{"session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/chats/{chat_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §Delete chat assistant's sessions", ClientMethods: []string{"DeleteChatSessions"}, UsedByCapability: []string{"session"}, MaxVerified: "v1.0.0-rc1"},

	// ── Chat completion (documented: unified endpoint) ──
	// NOTE (doc/123 §9): X sends metadata_condition on the top-level body of
	// the unified endpoint. The OpenAI-Compatible variant documents it inside
	// extra_body; the top-level form is source-aligned and pending live-drill
	// confirmation. The endpoint itself is documented; the field usage is the
	// only undocumented aspect.
	{Method: "POST", Path: "/chat/completions", Stability: EndpointDocumented, Evidence: "docs §Converse with chat assistant (legacy chats/{id}/completions deprecated)", ClientMethods: []string{"ChatCompletion", "StreamChatCompletion"}, UsedByCapability: []string{"chat", "stream", "references"}, MaxVerified: "v1.0.0-rc1"},

	// ── Temporary upload (documented via deprecated alias table) ──
	{Method: "POST", Path: "/documents/upload", Stability: EndpointDocumented, Evidence: "docs deprecated table: POST /v1/document/upload_info -> POST /api/v1/documents/upload", ClientMethods: []string{"UploadChatFile"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},

	// ── Agents (documented: §Agent Management) ──
	{Method: "GET", Path: "/agents", Stability: EndpointDocumented, Evidence: "docs §List agents", ClientMethods: []string{"ListAgents"}, UsedByCapability: []string{"agent"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/agents/{agent_id}", Stability: EndpointDocumented, Evidence: "docs §List agents (id filter); source-learned single GET form", ClientMethods: []string{"GetAgent"}, UsedByCapability: []string{"agent"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/agents", Stability: EndpointDocumented, Evidence: "docs §Create agent", ClientMethods: []string{"CreateAgent"}, UsedByCapability: []string{"agent"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/agents/{agent_id}", Stability: EndpointDocumented, Evidence: "docs §Update agent", ClientMethods: []string{"UpdateAgent"}, UsedByCapability: []string{"agent"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/agents/{agent_id}", Stability: EndpointDocumented, Evidence: "docs §Delete agent", ClientMethods: []string{"DeleteAgent"}, UsedByCapability: []string{"agent"}, MaxVerified: "v1.0.0-rc1"},

	// ── Agent versions: internal endpoints learned from source ──
	{Method: "GET", Path: "/agents/{agent_id}/versions", Stability: EndpointInternal, Evidence: "source: agent/canvas version routes (not in docs v0.27.2)", ClientMethods: []string{"ListAgentVersions"}, UsedByCapability: []string{"agent"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/agents/{agent_id}/versions/{version_id}", Stability: EndpointInternal, Evidence: "source: agent/canvas version routes (not in docs v0.27.2)", ClientMethods: []string{"GetAgentVersion", "RollbackAgentVersion"}, UsedByCapability: []string{"agent"}, MaxVerified: "v1.0.0-rc1"},
	// RollbackAgentVersion is a client-side composite (GetAgentVersion +
	// UpdateAgent); it performs no upstream call of its own, so it is
	// attributed to the GET version endpoint that fetches the DSL.

	// ── Agent sessions: create is DOCUMENTED-DEPRECATED, others documented ──
	{Method: "POST", Path: "/agents/{agent_id}/sessions", Stability: EndpointInternal, Evidence: "docs marks POST /agents/{id}/sessions DEPRECATED in v0.27.2: 'Use Converse with agent instead'; treated as internal pending migration (doc/121 B1)", ClientMethods: []string{"CreateAgentSession"}, UsedByCapability: []string{"agent_session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/agents/{agent_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §List agent sessions", ClientMethods: []string{"ListAgentSessions"}, UsedByCapability: []string{"agent_session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/agents/{agent_id}/sessions/{session_id}", Stability: EndpointDocumented, Evidence: "docs §List agent sessions (id filter); source-learned single GET form", ClientMethods: []string{"GetAgentSession", "ListAgentSessionMessages"}, UsedByCapability: []string{"agent_session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/agents/{agent_id}/sessions", Stability: EndpointDocumented, Evidence: "docs §Delete agent's sessions (ids body)", ClientMethods: []string{"DeleteAgentSession"}, UsedByCapability: []string{"agent_session"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/agents/chat/completions", Stability: EndpointDocumented, Evidence: "docs §Converse with agent (openai-compatible mode per deprecated alias table)", ClientMethods: []string{"AgentChatCompletion", "StreamAgentChatCompletion"}, UsedByCapability: []string{"agent", "agent_session", "stream"}, MaxVerified: "v1.0.0-rc1"},

	// ── Memory (documented: §Memory Management) ──
	{Method: "GET", Path: "/memories", Stability: EndpointDocumented, Evidence: "docs §List Memory", ClientMethods: []string{"ListMemories"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/memories", Stability: EndpointDocumented, Evidence: "docs §Create Memory", ClientMethods: []string{"CreateMemory"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/memories/{memory_id}", Stability: EndpointDocumented, Evidence: "docs §Update Memory", ClientMethods: []string{"UpdateMemory"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/memories/{memory_id}", Stability: EndpointDocumented, Evidence: "docs §Delete Memory", ClientMethods: []string{"DeleteMemory"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/memories/{memory_id}/config", Stability: EndpointDocumented, Evidence: "docs §Get Memory Config", ClientMethods: []string{"GetMemoryConfig"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/memories/{memory_id}", Stability: EndpointDocumented, Evidence: "docs §List messages of a memory (agent_id, keywords, pagination)", ClientMethods: []string{"ListMemoryMessages", "ListMemoryMessagesFiltered"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/messages", Stability: EndpointDocumented, Evidence: "docs §Add Message (body: memory_id array, agent_id, session_id, user_id, user_input, agent_response)", ClientMethods: []string{"AddMemoryMessage"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "*", Path: "/messages/{memory_id}:{message_id}", Stability: EndpointDocumented, Evidence: "docs §Delete Message / Update message status (DELETE + PUT)", ClientMethods: []string{"DeleteMemoryMessage", "UpdateMemoryMessageStatus"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/messages/search", Stability: EndpointDocumented, Evidence: "docs §Search Message", ClientMethods: []string{"SearchMemoryMessages"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/messages/{memory_id}:{message_id}/content", Stability: EndpointDocumented, Evidence: "docs §Get Message Content", ClientMethods: []string{"GetMemoryMessageContent"}, UsedByCapability: []string{"memory"}, MaxVerified: "v1.0.0-rc1"},

	// ── Search apps (documented: §Search App Management) ──
	{Method: "GET", Path: "/searches", Stability: EndpointDocumented, Evidence: "docs §List search apps", ClientMethods: []string{"ListSearchApps"}, UsedByCapability: []string{"search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/searches", Stability: EndpointDocumented, Evidence: "docs §Create search app", ClientMethods: []string{"CreateSearchApp"}, UsedByCapability: []string{"search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/searches/{search_id}", Stability: EndpointDocumented, Evidence: "docs §Get search app", ClientMethods: []string{"GetSearchApp"}, UsedByCapability: []string{"search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/searches/{search_id}", Stability: EndpointDocumented, Evidence: "docs §Update search app", ClientMethods: []string{"UpdateSearchApp"}, UsedByCapability: []string{"search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/searches/{search_id}", Stability: EndpointDocumented, Evidence: "docs §Delete search app", ClientMethods: []string{"DeleteSearchApp"}, UsedByCapability: []string{"search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/searches/{search_id}/completions", Stability: EndpointDocumented, Evidence: "docs §Search completion", ClientMethods: []string{"SearchAppCompletion", "StreamSearchAppCompletion"}, UsedByCapability: []string{"search_app"}, MaxVerified: "v1.0.0-rc1"},

	// ── Models & providers: ALL internal, learned from source ──
	// docs v0.27.2 §Models covers client-side provider configuration only;
	// the /providers management API is not part of the published reference
	// and 0.26 refactored it ("API & Model Provider Refactoring"), so treat
	// the whole family as unstable.
	{Method: "GET", Path: "/providers", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py (docs v0.27.2 do not document it; 0.26 refactored this family)", ClientMethods: []string{"ListProviders"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/providers", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"UpsertModelProvider"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/providers", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"AddProvider"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/providers/{provider_name}", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"DeleteProvider"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/providers/{provider_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"ListProviderModels"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/providers/{provider_name}/instances", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"ListProviderInstances"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/providers/{provider_name}/instances", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"CreateProviderInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PUT", Path: "/providers/{provider_name}/instances/{instance_name}", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"UpdateProviderInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/providers/{provider_name}/instances", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"DeleteProviderInstances"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/providers/{provider_name}/instances/{instance_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"ListInstanceModels"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/providers/{provider_name}/instances/{instance_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"AddModelToInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "PATCH", Path: "/providers/{provider_name}/instances/{instance_name}/models/{model_name}", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"UpdateModel"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "DELETE", Path: "/providers/{provider_name}/instances/{instance_name}/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"DeleteModelsFromInstance"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "POST", Path: "/providers/{provider_name}/connection", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py", ClientMethods: []string{"VerifyConnection", "ChatToModel"}, UsedByCapability: []string{"chat", "agent", "search_app"}, MaxVerified: "v1.0.0-rc1"},

	// ── Misc ──
	{Method: "GET", Path: "/system/healthz", Stability: EndpointDocumented, Evidence: "docs §System Check (legacy /v1/system/healthz deprecated)", ClientMethods: []string{"Health"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/pipelines", Stability: EndpointInternal, Evidence: "source: ragflow v1.0.0-rc1 internal/handler/pipeline.go ListPipelines", ClientMethods: []string{"ListPipelines"}, UsedByCapability: []string{"ingestion_contract"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/pipelines/{pipeline_id}", Stability: EndpointInternal, Evidence: "source: ragflow v1.0.0-rc1 internal/handler/pipeline.go GetPipeline", ClientMethods: []string{"GetPipeline"}, UsedByCapability: []string{"ingestion_contract"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/system/version", Stability: EndpointInternal, Evidence: "source: version probe (not in docs v0.27.2); startup version gate depends on it", ClientMethods: []string{"EngineVersion", "VersionProbe.ProbeVersion"}, UsedByCapability: []string{"chat", "agent", "agent_session", "search_app", "memory", "session", "references", "stream"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/models", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py (tenant LLM list)", ClientMethods: []string{"ListModels"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
	{Method: "GET", Path: "/models/default", Stability: EndpointInternal, Evidence: "source: api/apps/llm_management_api.py (tenant default chat model)", ClientMethods: []string{"ListDefaultModels"}, UsedByCapability: []string{"chat"}, MaxVerified: "v1.0.0-rc1"},
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
	// UnclassifiedVersion counts endpoints that declare neither MinVersion
	// nor MaxVerified - the T6.2 population backlog the version preflight
	// cannot yet gate.
	UnclassifiedVersion int
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
		if e.MinVersion == "" && e.MaxVerified == "" {
			c.UnclassifiedVersion++
		}
	}
	return c
}

// VersionBandGapReport lists every endpoint that declares no version band, so
// the T6.2 reconciliation backlog stays inspectable. After T6.2 classified
// every endpoint (MaxVerified=v1.0.0-rc1) this is empty; unit test
// TestVersionBandGapReportIsEmptyAfterT62 pins the收口. 224 §2.4.
func VersionBandGapReport() []EndpointRecord {
	var out []EndpointRecord
	for _, e := range endpointRegistry {
		if e.MinVersion == "" && e.MaxVerified == "" {
			out = append(out, e)
		}
	}
	return out
}

// VersionIssueStatus classifies a registered version-band observation.
// (223 T6 / 224 §2.4).
type VersionIssueStatus string

const (
	// VersionIssueObserved is a delta confirmed against a live engine release.
	VersionIssueObserved VersionIssueStatus = "observed"
	// VersionIssueRegression is a delta that broke a previously working
	// contract on a newer release (for example the /searches completion
	// singular-to-plural drift -> 50265, doc/222 §12.4).
	VersionIssueRegression VersionIssueStatus = "regression"
	// VersionIssueUntested is a delta pending T6.2 per-endpoint verification
	// against v1.0.0-rc1 before it folds into MinVersion/MaxVerified data.
	VersionIssueUntested VersionIssueStatus = "untested"
)

// VersionKnownIssue is one capability-level 版本×能力×契约 observation row.
// The rows below anchor the six cross-version deltas surfaced by the external
// audits and named in 223 T6 §方案1 (GPU parsing / graph retrieval / Team-Me
// permissions / the 18-endpoint restful_apis family / use_kg semantics /
// sandbox), plus the confirmed /searches completion regression (doc/222
// §12.4). Detailed per-endpoint reconciliation against v1.0.0-rc1 is T6.2.
type VersionKnownIssue struct {
	// Capability names the related Capability Matrix entry ("*" = family-wide).
	Capability string
	// Version is the release the delta was observed against.
	Version string
	// Finding summarises the delta for the 对账表 and the upgrade preflight.
	Finding string
	// Status anchors the row per the enum above.
	Status VersionIssueStatus
}

var versionIssueRegistry = []VersionKnownIssue{
	{Capability: "ingestion_contract", Version: "v1.0.0-rc1", Finding: "GPU-based parsing observed to differ across releases; D1 parsing deltas", Status: VersionIssueUntested},
	{Capability: "knowledge-ops", Version: "v1.0.0-rc1", Finding: "graph retrieval deviates from the documented form; K-class version-conflict scoring", Status: VersionIssueUntested},
	{Capability: "memory", Version: "v1.0.0-rc1", Finding: "Team-Me permission model differs between releases; memory scope", Status: VersionIssueUntested},
	{Capability: "*", Version: "v1.0.0-rc1", Finding: "18-endpoint restful_apis family moved/renamed; adapter internal surface must stay contract-drilled", Status: VersionIssueObserved},
	{Capability: "knowledge_strategy", Version: "v1.0.0-rc1", Finding: "use_kg boolean semantics differ (keyword-extraction gate)", Status: VersionIssueUntested},
	{Capability: "agent", Version: "v1.0.0-rc1", Finding: "sandbox/skills surface differs; X governs/audits, never re-implements the executor", Status: VersionIssueUntested},
	{Capability: "search_app", Version: "v1.0.0-rc1", Finding: "completion endpoint is plural /searches/{id}/completions; single /completion regressed -> 50265, fixed", Status: VersionIssueRegression},
}

func validVersionIssueStatus(status VersionIssueStatus) bool {
	switch status {
	case VersionIssueObserved, VersionIssueRegression, VersionIssueUntested:
		return true
	}
	return false
}

// KnownVersionIssues returns a copy of the capability-level observations.
func KnownVersionIssues() []VersionKnownIssue {
	out := make([]VersionKnownIssue, len(versionIssueRegistry))
	copy(out, versionIssueRegistry)
	return out
}

// VersionIssueSummary computes the observation totals for the capability
// report and the upgrade preflight.
type VersionIssueSummary struct {
	Total       int
	Regression  int
	Observed    int
	Untested    int
	RegressionMap map[string]string // capability -> finding for regressions
}

func SummarizeVersionIssues() VersionIssueSummary {
	s := VersionIssueSummary{RegressionMap: map[string]string{}}
	for _, i := range versionIssueRegistry {
		s.Total++
		switch i.Status {
		case VersionIssueRegression:
			s.Regression++
			s.RegressionMap[i.Capability] = i.Finding
		case VersionIssueObserved:
			s.Observed++
		default:
			s.Untested++
		}
	}
	return s
}

var (
	versionSemverPattern = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){1,2}(-[a-z0-9]+(\.[0-9]+)?)?$`)
	versionRCPattern     = regexp.MustCompile(`^rc[0-9]+$`)
)

// validVersionString accepts the loose RAGFlow release shapes used in the
// registry: 0.27.2 / v0.27.2 / v1.0.0-rc1 / rc1. Empty is not a version.
func validVersionString(version string) bool {
	if version == "" {
		return false
	}
	return versionSemverPattern.MatchString(version) || versionRCPattern.MatchString(version)
}
