// Package ragflow is the engine integration boundary. RAGFlow-X talks to the
// RAGFlow engine exclusively through this provider so that a mock can be used
// in development and tests without changing the services.
package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
)

// Client is the RAGFlow provider contract. It is intentionally small for M1
// and grows as more engine capabilities are wired up.
type Client interface {
	// EngineVersion returns the RAGFlow engine version for capability and
	// compatibility verification.
	EngineVersion(ctx context.Context) (string, error)
	// CreateDataset creates a dataset (knowledge base) in RAGFlow.
	CreateDataset(ctx context.Context, req CreateDatasetRequest) (*Dataset, error)
	// ListDatasets lists datasets belonging to the contract scope.
	ListDatasets(ctx context.Context) ([]Dataset, error)
	// DeleteDataset deletes a dataset by its RAGFlow id.
	DeleteDataset(ctx context.Context, id string) error
	// CreateDocument uploads a single document into a dataset.
	CreateDocument(ctx context.Context, datasetID string, doc *DocumentUpload) (*Document, error)
	// ListDocuments lists documents of a dataset.
	ListDocuments(ctx context.Context, datasetID string) ([]Document, error)
	// ParseDocuments triggers parsing for the given document ids.
	ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error
	// StopDocuments stops in-flight parsing for the given document ids.
	StopDocuments(ctx context.Context, datasetID string, documentIDs []string) error
	// DeleteDocuments removes the given documents from a dataset.
	DeleteDocuments(ctx context.Context, datasetID string, documentIDs []string) error
	// SetDocumentsStatus enables (status "1") or disables (status "0") documents.
	SetDocumentsStatus(ctx context.Context, datasetID string, documentIDs []string, enabled bool) error
	// UpdateDocumentMetadata replaces a document's metadata configuration.
	UpdateDocumentMetadata(ctx context.Context, datasetID, documentID string, metadata map[string]interface{}) error
	// ListDocumentChunks lists the parsed chunks of a document.
	ListDocumentChunks(ctx context.Context, datasetID, documentID string, page, pageSize int) ([]Chunk, int64, error)
	// GetDocumentContent returns a document's raw file bytes and content type.
	GetDocumentContent(ctx context.Context, documentID string) ([]byte, string, error)
	// GetChunk fetches a single parsed chunk by RAGFlow dataset/doc/chunk ids.
	GetChunk(ctx context.Context, datasetID, documentID, chunkID string) (*Chunk, error)
	// DeleteChunks deletes the given chunks of a document.
	DeleteChunks(ctx context.Context, datasetID, documentID string, chunkIDs []string) error
	// SetChunksAvailable enables (1) or disables (0) the given chunks of a document.
	SetChunksAvailable(ctx context.Context, datasetID, documentID string, chunkIDs []string, enabled bool) error
	// GetDatasetConfig returns a dataset's parsing/embedding/permission config.
	GetDatasetConfig(ctx context.Context, datasetID string) (*DatasetConfig, error)
	// UpdateDatasetConfig updates a dataset's config (parser, embedding, permission...).
	UpdateDatasetConfig(ctx context.Context, datasetID string, cfg DatasetConfigUpdate) error
	// Health probes connectivity with the engine.
	Health(ctx context.Context) (*Health, error)
	// UpsertModelProvider registers an LLM provider (factory + instance + model)
	// into the engine so chat configurations can use it.
	UpsertModelProvider(ctx context.Context, req RegisterModelProviderRequest) error
	// Name returns the provider implementation name (http|mock).
	Name() string

	// ListProviders lists all available system factories (available=true) or the
	// tenant-configured providers, mirroring RAGFlow's provider API.
	ListProviders(ctx context.Context, available bool) ([]ProviderInfo, error)
	// AddProvider registers a factory for the tenant in RAGFlow.
	AddProvider(ctx context.Context, providerName string) error
	// DeleteProvider removes a factory and all its instances/models from RAGFlow.
	DeleteProvider(ctx context.Context, providerName string) error
	// ListProviderModels returns the models the provider exposes (static catalog
	// plus remote discovery when api key/base url are provided).
	ListProviderModels(ctx context.Context, providerName, apiKey, baseURL string) ([]ProviderModel, error)
	// ListProviderInstances lists a provider's instances.
	ListProviderInstances(ctx context.Context, providerName string) ([]ProviderInstance, error)
	// CreateProviderInstance creates an instance (api key/base url/region) and its
	// models in RAGFlow.
	CreateProviderInstance(ctx context.Context, req RegisterModelProviderRequest) (*ProviderInstance, error)
	// UpdateProviderInstance updates an instance and reconciles its models.
	UpdateProviderInstance(ctx context.Context, providerName, instanceName string, req RegisterModelProviderRequest) error
	// DeleteProviderInstances drops the named instances of a provider.
	DeleteProviderInstances(ctx context.Context, providerName string, instanceIDs []string) error
	// ListInstanceModels lists models configured on an instance (supported-only when
	// supported is true).
	ListInstanceModels(ctx context.Context, providerName, instanceName string, supported bool) ([]ProviderModel, error)
	// AddModelToInstance adds a model to an instance.
	AddModelToInstance(ctx context.Context, providerName, instanceName string, model ModelInfo) error
	// UpdateModel alters a model's status/max_tokens/model_type/extra.
	UpdateModel(ctx context.Context, providerName, instanceName, modelName string, update ModelUpdate) error
	// DeleteModelsFromInstance removes models from an instance.
	DeleteModelsFromInstance(ctx context.Context, providerName, instanceName string, modelNames []string) error
	// VerifyConnection validates an api key/base url and returns per-model verify
	// status for the given model info.
	VerifyConnection(ctx context.Context, providerName, apiKey, baseURL, region string, modelInfo []ModelInfo) (map[string]string, error)
	// ChatToModel sends a test message to a configured model on an instance.
	ChatToModel(ctx context.Context, providerName, instanceName, modelName, message string, stream, thinking bool) (string, error)
	// ListChats lists the authenticated tenant's chat assistants.
	ListChats(ctx context.Context) ([]Chat, error)
	// GetChat returns a single chat assistant.
	GetChat(ctx context.Context, chatID string) (*Chat, error)
	// CreateChat creates a chat assistant bound to datasets.
	CreateChat(ctx context.Context, req CreateChatRequest) (*Chat, error)
	// UpdateChat updates a chat assistant's mutable fields.
	UpdateChat(ctx context.Context, chatID string, req UpdateChatRequest) (*Chat, error)
	// DeleteChat deletes a chat assistant.
	DeleteChat(ctx context.Context, chatID string) error
	// ListChatSessions lists the conversation sessions of a chat assistant.
	ListChatSessions(ctx context.Context, chatID string, opts SessionListOptions) ([]Session, error)
	// GetChatSession returns a session including its messages.
	GetChatSession(ctx context.Context, chatID, sessionID string) (*Session, error)
	// ListChatSessionMessages returns a bounded page of session messages.
	ListChatSessionMessages(ctx context.Context, chatID, sessionID string, opts SessionMessagePageOptions) ([]Message, string, error)
	// CreateChatSession creates a new session under a chat assistant.
	CreateChatSession(ctx context.Context, chatID, name string) (*Session, error)
	// DeleteChatSessions deletes the given sessions of a chat assistant.
	DeleteChatSessions(ctx context.Context, chatID string, sessionIDs []string) error
	// UpdateChatSession renames a session of a chat assistant.
	UpdateChatSession(ctx context.Context, chatID, sessionID, name string) (*Session, error)
	// ListSessionMessages returns the messages of a session.
	ListSessionMessages(ctx context.Context, chatID, sessionID string) ([]Message, error)
	// ChatCompletion runs a non-streaming completion against a chat assistant.
	ChatCompletion(ctx context.Context, chatID string, req CompletionRequest) (*CompletionResponse, error)
	// ListSearchApps lists the authenticated tenant's Search Apps.
	ListSearchApps(ctx context.Context, f ListSearchAppsFilter) ([]SearchAppListItem, int64, error)
	// GetSearchApp returns a single Search App.
	GetSearchApp(ctx context.Context, searchAppID string) (*SearchApp, error)
	// CreateSearchApp creates a Search App bound to datasets.
	CreateSearchApp(ctx context.Context, req CreateSearchAppRequest) (string, error)
	// UpdateSearchApp updates a Search App's mutable fields.
	UpdateSearchApp(ctx context.Context, searchAppID string, req UpdateSearchAppRequest) (*SearchApp, error)
	// DeleteSearchApp deletes a Search App.
	DeleteSearchApp(ctx context.Context, searchAppID string) error
	// SearchAppCompletion runs a retrieval against a Search App for debugging.
	SearchAppCompletion(ctx context.Context, searchAppID string, req SearchAppCompletionRequest) (*SearchAppCompletionResult, error)
	// StreamSearchAppCompletion proxies the Search App SSE completion to w.
	StreamSearchAppCompletion(ctx context.Context, searchAppID string, req SearchAppCompletionRequest, w io.Writer) error
	// ListMemories lists the accessible memories.
	ListMemories(ctx context.Context, f ListMemoriesFilter) ([]Memory, int64, error)
	// GetMemoryConfig returns a memory's live configuration.
	GetMemoryConfig(ctx context.Context, memoryID string) (*Memory, error)
	// CreateMemory creates a memory and returns its id.
	CreateMemory(ctx context.Context, req CreateMemoryRequest) (string, error)
	// UpdateMemory updates a memory's settings.
	UpdateMemory(ctx context.Context, memoryID string, req UpdateMemoryRequest) (*Memory, error)
	// DeleteMemory deletes a memory.
	DeleteMemory(ctx context.Context, memoryID string) error
	// ListMemoryMessages lists a memory's messages.
	ListMemoryMessages(ctx context.Context, memoryID string) ([]MemoryMessage, error)
	// AddMemoryMessage stores a memory message.
	AddMemoryMessage(ctx context.Context, req AddMessageRequest) error
	// DeleteMemoryMessage forgets a memory message.
	DeleteMemoryMessage(ctx context.Context, memoryID string, messageID int64) error
	// UpdateMemoryMessageStatus sets a message's status.
	UpdateMemoryMessageStatus(ctx context.Context, memoryID string, messageID int64, status bool) error
	// SearchMemoryMessages runs a semantic retrieval against a memory.
	SearchMemoryMessages(ctx context.Context, memoryID string, p MemorySearchParams) ([]MemoryMessage, error)
	// GetMemoryMessageContent returns a message's full content.
	GetMemoryMessageContent(ctx context.Context, memoryID string, messageID int64) (json.RawMessage, error)
	// ListAgents lists the caller's agents.
	ListAgents(ctx context.Context, f ListAgentsFilter) ([]Agent, int64, error)
	// GetAgent returns a single agent canvas.
	GetAgent(ctx context.Context, agentID string) (*Agent, error)
	// CreateAgent creates an agent.
	CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error)
	// UpdateAgent updates an agent (title/dsl/release).
	UpdateAgent(ctx context.Context, agentID string, req UpdateAgentRequest) error
	// DeleteAgent deletes an agent.
	DeleteAgent(ctx context.Context, agentID string) error
	// ListAgentVersions lists the saved versions of an agent.
	ListAgentVersions(ctx context.Context, agentID string) ([]AgentVersion, error)
	// GetAgentVersion returns a single version of an agent by its ID.
	GetAgentVersion(ctx context.Context, agentID, versionID string) (*AgentVersion, error)
	// RollbackAgentVersion restores an agent's DSL from a saved version.
	RollbackAgentVersion(ctx context.Context, agentID, versionID string) error
	// ListAgentSessions lists an agent's sessions.
	ListAgentSessions(ctx context.Context, agentID string, opts SessionListOptions) ([]AgentSession, int64, error)
	// CreateAgentSession creates an agent session.
	CreateAgentSession(ctx context.Context, agentID, name string) (*AgentSession, error)
	// GetAgentSession returns an agent session with messages.
	GetAgentSession(ctx context.Context, agentID, sessionID string) (*AgentSession, error)
	// ListAgentSessionMessages returns a bounded page of agent session messages.
	ListAgentSessionMessages(ctx context.Context, agentID, sessionID string, opts SessionMessagePageOptions) ([]Message, string, error)
	// DeleteAgentSession deletes an agent session.
	DeleteAgentSession(ctx context.Context, agentID, sessionID string) error
	// AgentChatCompletion runs a non-streaming agent chat completion.
	AgentChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	// StreamAgentChatCompletion proxies an agent chat completion (SSE).
	StreamAgentChatCompletion(ctx context.Context, req CompletionRequest, w io.Writer) error
	// StreamChatCompletion streams a completion (SSE) to w.
	StreamChatCompletion(ctx context.Context, chatID string, req CompletionRequest, w io.Writer) error
	// UploadChatFile uploads a temporary chat attachment blob.
	UploadChatFile(ctx context.Context, filename string, content []byte) (*UploadedFile, error)
	// UploadAgentFile uploads a temporary attachment into an Agent's RAGFlow file context.
	UploadAgentFile(ctx context.Context, agentID, filename string, content []byte) (*UploadedFile, error)
	GetChunkImage(ctx context.Context, imageID string) ([]byte, string, error)
	// ListModels returns the tenant's added models for a type (chat, rerank...).
	ListModels(ctx context.Context, modelType string) ([]AddedModel, error)
	// ListDefaultModels returns the tenant's RAGFlow default model settings.
	ListDefaultModels(ctx context.Context) ([]DefaultModel, error)
}

// RegisterModelProviderRequest carries everything needed to register an LLM
// provider instance and one or more models into RAGFlow.
type RegisterModelProviderRequest struct {
	FactoryName  string
	InstanceName string
	APIKey       string
	BaseURL      string
	ModelName    string
	MaxTokens    int
	ModelTypes   []string
	IsTools      bool
	Thinking     bool
	// Region is the provider region (e.g. default / intl).
	Region string
	// Models is the full list of models to configure on the instance. When empty the
	// instance is created/updated without explicit model selection.
	Models []ModelInfo
}

// ProviderInfo describes a system or tenant-configured provider factory.
type ProviderInfo struct {
	Name        string          `json:"name"`
	URL         ProviderURLs    `json:"url"`
	ModelTypes  []string        `json:"model_types"`
	HasInstance bool            `json:"has_instance,omitempty"`
	Extra       json.RawMessage `json:"extra,omitempty"`
}

// ProviderURLs carries the default (and optional intl) base URLs of a factory.
type ProviderURLs struct {
	Default string `json:"default"`
	Intl    string `json:"intl,omitempty"`
}

// ProviderModel is a model as reported by RAGFlow (static catalog or remote).
type ProviderModel struct {
	Name       string                 `json:"name"`
	MaxTokens  int                    `json:"max_tokens"`
	ModelTypes []string               `json:"model_types"`
	Features   []string               `json:"features"`
	Status     string                 `json:"status,omitempty"`
	Verify     string                 `json:"verify,omitempty"`
	Extra      map[string]interface{} `json:"extra,omitempty"`
}

// ProviderInstance is a named configuration unit under a provider.
type ProviderInstance struct {
	ID           string `json:"id"`
	InstanceName string `json:"instance_name"`
	ProviderID   string `json:"provider_id"`
	Region       string `json:"region"`
	BaseURL      string `json:"base_url,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	Status       string `json:"status"`
}

// ModelInfo describes a single model within a model_info list.
type ModelInfo struct {
	ModelType []string               `json:"model_type"`
	ModelName string                 `json:"model_name"`
	MaxTokens int                    `json:"max_tokens,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

// ModelUpdate carries the patchable fields of a model.
type ModelUpdate struct {
	Status    string                 `json:"status,omitempty"`
	MaxTokens int                    `json:"max_tokens,omitempty"`
	ModelType []string               `json:"model_type,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

// AddedModel is a model the RAGFlow tenant has added/configured, as returned
// by GET /models.
type AddedModel struct {
	ModelID  string   `json:"model_id"`
	Name     string   `json:"name"`
	Type     []string `json:"model_type"`
	Provider string   `json:"provider_name"`
	Instance string   `json:"instance_name"`
}

// DefaultModel is a tenant default model setting returned by /models/default.
type DefaultModel struct {
	ModelID string `json:"model_id"`
	Name    string `json:"name"`
	Type    string `json:"model_type"`
}

// Dataset mirrors the RAGFlow dataset fields used by RAGFlow-X.
type Dataset struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ChunkCount    int64  `json:"chunk_count"`
	DocumentCount int64  `json:"document_count"`
	CreateTime    int64  `json:"create_time"`
	UpdateTime    int64  `json:"update_time"`
}

// CreateDatasetRequest carries dataset creation parameters.
// DatasetConfig mirrors a dataset's parse/embedding/permission configuration.
type DatasetConfig struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	ChunkMethod    string                 `json:"chunk_method"`
	EmbeddingModel string                 `json:"embedding_model"`
	Permission     string                 `json:"permission"`
	ParserConfig   map[string]interface{} `json:"parser_config"`
}

// DatasetConfigUpdate carries optional dataset config fields to update.
type DatasetConfigUpdate struct {
	Name           *string                `json:"name,omitempty"`
	Description    *string                `json:"description,omitempty"`
	ChunkMethod    *string                `json:"chunk_method,omitempty"`
	EmbeddingModel *string                `json:"embedding_model,omitempty"`
	Permission     *string                `json:"permission,omitempty"`
	ParserConfig   map[string]interface{} `json:"parser_config,omitempty"`
}
type CreateDatasetRequest struct {
	Name           string `json:"name"`
	ChunkMethod    string `json:"chunk_method,omitempty"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
	ParserID       string `json:"parser_id,omitempty"`
}

// Document mirrors RAGFlow document fields used by RAGFlow-X.
type Document struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"run"`
	Enabled    flexString `json:"status"`
	ChunkCount int64      `json:"chunk_count"`
	TokenCount int64      `json:"token_count"`

	ProcessBeginAt  flexString `json:"process_begin_at"`
	ProcessDuration flexString `json:"process_duration"`
	Progress        float64    `json:"progress"`
	ProgressMsg     string     `json:"progress_msg"`
	CreateTime      int64      `json:"create_time"`
	UpdateTime      int64      `json:"update_time"`
	Size            int64      `json:"size"`
}

// flexString decodes either a JSON string or a number into a string, because
// RAGFlow versions differ in whether timestamp/duration fields are numbers.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexString(n.String())
		return nil
	}
	return nil
}

// DocumentUpload carries a document's name and content for upload.
// Chunk mirrors a parsed chunk returned by RAGFlow.
type Chunk struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	DocumentID string   `json:"document_id"`
	DocName    string   `json:"docnm_kwd"`
	Keywords   []string `json:"important_keywords"`
	Questions  []string `json:"questions"`
	Available  bool     `json:"available"`
}
type DocumentUpload struct {
	Name    string
	Content []byte
}

// Health reflects engine and its dependencies' status.
type Health struct {
	Status    string `json:"status"`
	DB        string `json:"db"`
	Redis     string `json:"redis"`
	DocEngine string `json:"doc_engine"`
}

// Chat is an RAGFlow chat assistant (assistant configuration).
type Chat struct {
	ID                     string                 `json:"id"`
	Name                   string                 `json:"name"`
	Status                 string                 `json:"status,omitempty"`
	Language               string                 `json:"language,omitempty"`
	LLMID                  string                 `json:"llm_id,omitempty"`
	PromptConfig           map[string]interface{} `json:"prompt_config,omitempty"`
	DatasetIDs             []string               `json:"dataset_ids,omitempty"`
	KBNames                []string               `json:"kb_names,omitempty"`
	TopN                   int                    `json:"top_n,omitempty"`
	TopK                   int                    `json:"top_k,omitempty"`
	RerankID               string                 `json:"rerank_id,omitempty"`
	SimilarityThreshold    float64                `json:"similarity_threshold,omitempty"`
	VectorSimilarityWeight float64                `json:"vector_similarity_weight,omitempty"`
	CreateTime             int64                  `json:"create_time,omitempty"`
	UpdateTime             int64                  `json:"update_time,omitempty"`
}

// CreateChatRequest carries the fields to create a chat assistant.
type CreateChatRequest struct {
	Name       string   `json:"name"`
	DatasetIDs []string `json:"dataset_ids,omitempty"`
	Language   string   `json:"language,omitempty"`

	// Authoring fields passed through to RAGFlow so individuals can tune the
	// prompt engine, retrieval and model locally instead of only creating a shell.
	PromptConfig           map[string]interface{} `json:"prompt_config,omitempty"`
	LLMID                  string                 `json:"llm_id,omitempty"`
	RerankID               string                 `json:"rerank_id,omitempty"`
	TopN                   int                    `json:"top_n,omitempty"`
	TopK                   int                    `json:"top_k,omitempty"`
	SimilarityThreshold    float64                `json:"similarity_threshold,omitempty"`
	VectorSimilarityWeight float64                `json:"vector_similarity_weight,omitempty"`
}

// UpdateChatRequest carries the mutable fields of a chat assistant.
type UpdateChatRequest struct {
	Name         string                 `json:"name,omitempty"`
	DatasetIDs   []string               `json:"dataset_ids,omitempty"`
	Language     string                 `json:"language,omitempty"`
	PromptConfig map[string]interface{} `json:"prompt_config,omitempty"`

	LLMID                  string                 `json:"llm_id,omitempty"`
	RerankID               string                 `json:"rerank_id,omitempty"`
	TopN                   int                    `json:"top_n,omitempty"`
	TopK                   int                    `json:"top_k,omitempty"`
	SimilarityThreshold    float64                `json:"similarity_threshold,omitempty"`
	VectorSimilarityWeight float64                `json:"vector_similarity_weight,omitempty"`
	LLMSetting             map[string]interface{} `json:"llm_setting,omitempty"`
}

// Message is a chat message within a session.
type Message struct {
	Role      string                   `json:"role"`
	Content   string                   `json:"content"`
	Files     []map[string]interface{} `json:"files,omitempty"`
	Citations []map[string]interface{} `json:"citations,omitempty"`
}

// UploadedFile is the metadata of a temporary chat attachment blob.
type UploadedFile struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Size         int64   `json:"size"`
	Extension    string  `json:"extension"`
	MimeType     string  `json:"mime_type"`
	PreviewURL   string  `json:"preview_url,omitempty"`
	CreatedAt    float64 `json:"created_at,omitempty"`
	CreatedBy    string  `json:"created_by,omitempty"`
	Content      string  `json:"content,omitempty"`
	UploadTicket string  `json:"upload_ticket,omitempty"`
}

// Session is a conversation under a chat assistant.
type Session struct {
	ID         string                   `json:"id"`
	ChatID     string                   `json:"chat_id"`
	Name       string                   `json:"name"`
	UserID     string                   `json:"user_id,omitempty"`
	Messages   []Message                `json:"messages,omitempty"`
	Reference  []map[string]interface{} `json:"reference,omitempty"`
	CreateTime int64                    `json:"create_time,omitempty"`
}

// SessionListOptions transparently maps platform pagination to RAGFlow
// session-list endpoints. It intentionally does not introduce a new storage
// model; RAGFlow remains the session source of truth.
type SessionListOptions struct {
	Page     int
	PageSize int
	Name     string
	Keywords string
}

// SessionMessagePageOptions controls the local message-page contract when the
// upstream session detail returns the full message array.
type SessionMessagePageOptions struct {
	Limit  int
	Cursor string
}

func (o SessionListOptions) normalized() (page, pageSize int) {
	if o.Page < 1 {
		page = 1
	} else {
		page = o.Page
	}
	if o.PageSize < 1 {
		pageSize = 20
	} else if o.PageSize > 100 {
		pageSize = 100
	} else {
		pageSize = o.PageSize
	}
	return page, pageSize
}

func pageSessionMessages(messages []Message, opts SessionMessagePageOptions) ([]Message, string) {
	limit := opts.Limit
	if limit < 1 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}
	cursor := 0
	if opts.Cursor != "" {
		value, err := strconv.Atoi(opts.Cursor)
		if err != nil || value < 1 || value > len(messages) {
			return []Message{}, ""
		}
		cursor = value
	} else if len(messages) > limit {
		cursor = len(messages)
	}
	end := cursor
	if end == 0 {
		end = len(messages)
	}
	start := max(0, end-limit)
	next := ""
	if start > 0 {
		next = strconv.Itoa(start)
	}
	return append([]Message(nil), messages[start:end]...), next
}

// attachSessionReferences pairs RAGFlow session-level references with assistant
// replies. The prologue precedes the first user turn and is therefore skipped.
func attachSessionReferences(messages []Message, references []map[string]interface{}) {
	referenceIndex := 0
	sawUser := false
	for index := range messages {
		switch messages[index].Role {
		case "user":
			sawUser = true
		case "assistant":
			if !sawUser || referenceIndex >= len(references) {
				continue
			}
			chunks, _ := references[referenceIndex]["chunks"].([]interface{})
			citations := make([]map[string]interface{}, 0, len(chunks))
			for _, chunk := range chunks {
				if item, ok := chunk.(map[string]interface{}); ok {
					citations = append(citations, item)
				}
			}
			if len(citations) > 0 {
				messages[index].Citations = citations
			}
			referenceIndex++
		}
	}
}

// CompletionRequest is an OpenAI-shaped completion request with an optional
// chat assistant and session id.
type CompletionRequest struct {
	ChatID    string                   `json:"chat_id,omitempty"`
	SessionID string                   `json:"session_id,omitempty"`
	Messages  []Message                `json:"messages"`
	Files     []map[string]interface{} `json:"files,omitempty"`
	Stream    bool                     `json:"stream"`
}

// CompletionResponse mirrors the OpenAI-shaped non-streaming completion.
type CompletionResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model,omitempty"`
	Answer  string             `json:"answer,omitempty"`
	Choices []CompletionChoice `json:"choices"`
	Usage   *CompletionUsage   `json:"usage,omitempty"`
}

// CompletionChoice is a single completion choice.
type CompletionChoice struct {
	Message Message `json:"message"`
}

// CompletionUsage reports token usage.
type CompletionUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}
