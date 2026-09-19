package ragflow

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// Mock is an in-memory RAGFlow provider used for local development and tests.
// It is NOT safe for production; it exists so the full service flow can run
// without a live RAGFlow instance.
type Mock struct {
	mu                 sync.Mutex
	datasets           map[string]*mockDataset
	documents          map[string][]*mockDocument
	providers          map[string]*mockProvider
	metadata           map[string]map[string]map[string]interface{}
	chunks             map[string][]*mockChunk
	searchApps         map[string]*mockSearchApp
	memories           map[string]*mockMemory
	agents             map[string]*mockAgent
	chatCreateRequests []CreateChatRequest
}

// Compile-time assertion that Mock implements Client.
var _ Client = (*Mock)(nil)

type mockDataset struct {
	id   string
	name string
	docs []*mockDocument
}

type mockDocument struct {
	id     string
	name   string
	status string
}

type mockChunk struct {
	id         string
	content    string
	documentID string
	docName    string
	available  bool
}

// NewMock creates an empty Mock.
func NewMock() *Mock {
	return &Mock{
		datasets:   map[string]*mockDataset{},
		documents:  map[string][]*mockDocument{},
		providers:  map[string]*mockProvider{},
		metadata:   map[string]map[string]map[string]interface{}{},
		chunks:     map[string][]*mockChunk{},
		searchApps: map[string]*mockSearchApp{},
		memories:   map[string]*mockMemory{},
		agents:     map[string]*mockAgent{},
	}
}

// Name implements the Client interface.
func (m *Mock) Name() string { return "mock" }

// ChatCreateRequests exposes captured chat create calls for service tests.
func (m *Mock) ChatCreateRequests() []CreateChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	requests := make([]CreateChatRequest, len(m.chatCreateRequests))
	copy(requests, m.chatCreateRequests)
	return requests
}

func (m *Mock) EngineVersion(ctx context.Context) (string, error) {
	return "0.27.1", nil
}

func (m *Mock) CreateDataset(ctx context.Context, req CreateDatasetRequest) (*Dataset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ds := &mockDataset{id: id.New(), name: req.Name}
	m.datasets[ds.id] = ds
	return &Dataset{ID: ds.id, Name: ds.name}, nil
}

func (m *Mock) ListDatasets(ctx context.Context) ([]Dataset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Dataset, 0, len(m.datasets))
	for _, ds := range m.datasets {
		out = append(out, Dataset{ID: ds.id, Name: ds.name, DocumentCount: int64(len(ds.docs))})
	}
	return out, nil
}

func (m *Mock) DeleteDataset(ctx context.Context, idStr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.datasets[idStr]; !ok {
		return fmt.Errorf("dataset not found: %s", idStr)
	}
	delete(m.datasets, idStr)
	delete(m.documents, idStr)
	return nil
}

func (m *Mock) CreateDocument(ctx context.Context, datasetID string, doc *DocumentUpload) (*Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ds, ok := m.datasets[datasetID]
	if !ok {
		return nil, fmt.Errorf("dataset not found: %s", datasetID)
	}
	d := &mockDocument{id: id.New(), name: doc.Name, status: "pending"}
	ds.docs = append(ds.docs, d)
	m.documents[datasetID] = ds.docs
	return &Document{ID: d.id, Name: d.name, Status: d.status}, nil
}

func (m *Mock) ListDocuments(ctx context.Context, datasetID string) ([]Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	docs := m.documents[datasetID]
	out := make([]Document, 0, len(docs))
	for _, d := range docs {
		out = append(out, Document{ID: d.id, Name: d.name, Status: d.status})
	}
	return out, nil
}

func (m *Mock) ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	docs := m.documents[datasetID]
	idset := make(map[string]bool, len(documentIDs))
	for _, docID := range documentIDs {
		idset[docID] = true
	}
	changed := false
	for _, d := range docs {
		if idset[strings.TrimSpace(d.id)] {
			d.status = "parsed"
			changed = true
		}
	}
	if !changed {
		return fmt.Errorf("no matching documents to parse in dataset %s", datasetID)
	}
	return nil
}

func (m *Mock) StopDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = datasetID
	_ = documentIDs
	return nil
}

func (m *Mock) DeleteDocuments(ctx context.Context, datasetID string, documentIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	docs := m.documents[datasetID]
	idset := make(map[string]bool, len(documentIDs))
	for _, id := range documentIDs {
		idset[id] = true
	}
	kept := make([]*mockDocument, 0, len(docs))
	for _, d := range docs {
		if !idset[d.id] {
			kept = append(kept, d)
		}
	}
	m.documents[datasetID] = kept
	if ds, ok := m.datasets[datasetID]; ok {
		ds.docs = kept
	}
	return nil
}

func (m *Mock) SetDocumentsStatus(ctx context.Context, datasetID string, documentIDs []string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nil
}

func (m *Mock) UpdateDocumentMetadata(ctx context.Context, datasetID, documentID string, metadata map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.documents[datasetID]; !ok {
		return fmt.Errorf("dataset not found: %s", datasetID)
	}
	found := false
	for _, doc := range m.documents[datasetID] {
		if doc.id == documentID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("document not found: %s", documentID)
	}
	if m.metadata[datasetID] == nil {
		m.metadata[datasetID] = map[string]map[string]interface{}{}
	}
	m.metadata[datasetID][documentID] = metadata
	return nil
}

func (m *Mock) ListDocumentChunks(ctx context.Context, datasetID, documentID string, page, pageSize int) ([]Chunk, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if page < 1 || pageSize < 1 {
		return nil, 0, fmt.Errorf("invalid pagination")
	}
	chunks := m.chunks[datasetID]
	start := (page - 1) * pageSize
	if start >= len(chunks) {
		return []Chunk{}, int64(len(chunks)), nil
	}
	end := start + pageSize
	if end > len(chunks) {
		end = len(chunks)
	}
	out := make([]Chunk, 0, end-start)
	for _, chunk := range chunks[start:end] {
		if chunk.documentID != documentID {
			continue
		}
		out = append(out, Chunk{
			ID: chunk.id, Content: chunk.content, DocumentID: chunk.documentID,
			DocName: chunk.docName, Available: chunk.available,
		})
	}
	return out, int64(len(chunks)), nil
}

func (m *Mock) GetDocumentContent(ctx context.Context, documentID string) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nil, "", nil
}

func (m *Mock) DeleteChunks(ctx context.Context, datasetID, documentID string, chunkIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	chunks := m.chunks[datasetID]
	remove := make(map[string]bool, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		remove[chunkID] = true
	}
	kept := make([]*mockChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if !remove[chunk.id] {
			kept = append(kept, chunk)
		}
	}
	m.chunks[datasetID] = kept
	return nil
}

func (m *Mock) SetChunksAvailable(ctx context.Context, datasetID, documentID string, chunkIDs []string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make(map[string]bool, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		ids[chunkID] = true
	}
	for _, chunk := range m.chunks[datasetID] {
		if ids[chunk.id] && chunk.documentID == documentID {
			chunk.available = enabled
		}
	}
	return nil
}

// SeedDocumentChunk adds a chunk to the in-memory mock engine for tests.
func (m *Mock) SeedDocumentChunk(datasetID, documentID, chunkID, content string, available bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	docName := documentID
	for _, doc := range m.documents[datasetID] {
		if doc.id == documentID {
			docName = doc.name
			break
		}
	}
	m.chunks[datasetID] = append(m.chunks[datasetID], &mockChunk{
		id: chunkID, content: content, documentID: documentID, docName: docName, available: available,
	})
}

func (m *Mock) GetDatasetConfig(ctx context.Context, datasetID string) (*DatasetConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &DatasetConfig{ID: datasetID, ChunkMethod: "naive", Permission: "team", ParserConfig: map[string]interface{}{}}, nil
}

func (m *Mock) UpdateDatasetConfig(ctx context.Context, datasetID string, cfg DatasetConfigUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nil
}

func (m *Mock) Health(ctx context.Context) (*Health, error) {
	return &Health{Status: "green", DB: "ok", Redis: "ok", DocEngine: "ok"}, nil
}

func (m *Mock) UpsertModelProvider(ctx context.Context, req RegisterModelProviderRequest) error {
	return nil
}

type mockProvider struct {
	name      string
	instances map[string]*mockInstance // keyed by instance name
}

type mockInstance struct {
	id      string
	name    string
	apiKey  string
	baseURL string
	region  string
	status  string
	models  map[string]*mockModel // keyed by model name
}

type mockModel struct {
	id        string
	name      string
	modelType int
	status    string
	maxTokens int
	verify    string
}

func (m *Mock) providerMgr(providerName string, create bool) *mockProvider {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.providers[providerName]
	if p == nil && create {
		p = &mockProvider{name: providerName, instances: map[string]*mockInstance{}}
		m.providers[providerName] = p
	}
	return p
}

func mockModelTypeMaske(mods []ModelInfo) int {
	mask := 0
	for _, mod := range mods {
		for _, t := range mod.ModelType {
			switch t {
			case "chat":
				mask |= 1
			case "embedding":
				mask |= 2
			case "asr":
				mask |= 4
			case "vision":
				mask |= 8
			case "rerank":
				mask |= 16
			case "tts":
				mask |= 32
			case "ocr":
				mask |= 64
			}
		}
	}
	return mask
}

func (m *Mock) ListProviders(ctx context.Context, available bool) ([]ProviderInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if available {
		return []ProviderInfo{
			{Name: "OpenAI-API-Compatible", URL: ProviderURLs{Default: "https://api.openai.com/v1"}, ModelTypes: []string{"chat", "embedding", "vision"}},
			{Name: "Ollama", URL: ProviderURLs{Default: "http://localhost:11434/v1"}, ModelTypes: []string{"chat", "embedding"}},
			{Name: "VLLM", URL: ProviderURLs{Default: "http://localhost:8000/v1"}, ModelTypes: []string{"chat"}},
		}, nil
	}
	out := make([]ProviderInfo, 0, len(m.providers))
	for name, p := range m.providers {
		out = append(out, ProviderInfo{Name: name, HasInstance: len(p.instances) > 0})
	}
	return out, nil
}

func (m *Mock) AddProvider(ctx context.Context, providerName string) error {
	_ = m.providerMgr(providerName, true)
	return nil
}

func (m *Mock) DeleteProvider(ctx context.Context, providerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.providers, providerName)
	return nil
}

func (m *Mock) ListProviderModels(ctx context.Context, providerName, apiKey, baseURL string) ([]ProviderModel, error) {
	if providerName == "Ollama" {
		return []ProviderModel{{Name: "llama3.1", ModelTypes: []string{"chat"}, MaxTokens: 8192}, {Name: "nomic-embed-text", ModelTypes: []string{"embedding"}, MaxTokens: 2048}}, nil
	}
	return []ProviderModel{{Name: "gpt-4o", ModelTypes: []string{"chat", "vision"}, MaxTokens: 128000, Features: []string{"is_tools", "thinking"}}}, nil
}

func (m *Mock) ListProviderInstances(ctx context.Context, providerName string) ([]ProviderInstance, error) {
	p := m.providerMgr(providerName, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil {
		return nil, nil
	}
	out := make([]ProviderInstance, 0, len(p.instances))
	for _, inst := range p.instances {
		out = append(out, ProviderInstance{ID: inst.id, InstanceName: inst.name, Region: inst.region, BaseURL: inst.baseURL, APIKey: inst.apiKey, Status: inst.status})
	}
	return out, nil
}

func (m *Mock) CreateProviderInstance(ctx context.Context, req RegisterModelProviderRequest) (*ProviderInstance, error) {
	inst := &mockInstance{id: id.New(), name: req.InstanceName, apiKey: req.APIKey, baseURL: req.BaseURL, region: req.Region, status: "active", models: map[string]*mockModel{}}
	p := m.providerMgr(req.FactoryName, true)
	m.mu.Lock()
	defer m.mu.Unlock()
	p.instances[inst.name] = inst
	for _, model := range req.Models {
		maxTokens := model.MaxTokens
		if maxTokens <= 0 {
			maxTokens = 8192
		}
		inst.models[model.ModelName] = &mockModel{
			id: id.New(), name: model.ModelName,
			modelType: mockModelTypeMaske([]ModelInfo{model}),
			status:    "active", maxTokens: maxTokens, verify: "success",
		}
	}
	return &ProviderInstance{ID: inst.id, InstanceName: inst.name, Region: inst.region, BaseURL: inst.baseURL, APIKey: inst.apiKey, Status: inst.status}, nil
}

func (m *Mock) UpdateProviderInstance(ctx context.Context, providerName, instanceName string, req RegisterModelProviderRequest) error {
	p := m.providerMgr(providerName, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil {
		return fmt.Errorf("provider %s not found", providerName)
	}
	inst := p.instances[instanceName]
	if inst == nil {
		return fmt.Errorf("instance %s not found", instanceName)
	}
	inst.apiKey = req.APIKey
	inst.baseURL = req.BaseURL
	inst.region = req.Region
	submitted := make(map[string]ModelInfo, len(req.Models))
	for _, model := range req.Models {
		submitted[model.ModelName] = model
	}
	for name := range inst.models {
		if _, exists := submitted[name]; !exists {
			delete(inst.models, name)
		}
	}
	for name, model := range submitted {
		maxTokens := model.MaxTokens
		if maxTokens <= 0 {
			maxTokens = 8192
		}
		inst.models[name] = &mockModel{id: id.New(), name: name, modelType: mockModelTypeMaske([]ModelInfo{model}), status: "active", maxTokens: maxTokens, verify: "success"}
	}
	return nil
}

func (m *Mock) DeleteProviderInstances(ctx context.Context, providerName string, instanceIDs []string) error {
	p := m.providerMgr(providerName, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil {
		return nil
	}
	for _, id := range instanceIDs {
		for name, inst := range p.instances {
			if inst.id == id {
				delete(p.instances, name)
			}
		}
	}
	return nil
}

func (m *Mock) ListInstanceModels(ctx context.Context, providerName, instanceName string, supported bool) ([]ProviderModel, error) {
	p := m.providerMgr(providerName, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if supported {
		models, err := m.ListProviderModels(ctx, providerName, "", "")
		if err != nil {
			return nil, err
		}
		return models, nil
	}
	if p == nil {
		return nil, nil
	}
	inst := p.instances[instanceName]
	if inst == nil {
		return nil, fmt.Errorf("instance %s not found", instanceName)
	}
	out := make([]ProviderModel, 0, len(inst.models))
	for _, mod := range inst.models {
		out = append(out, ProviderModel{Name: mod.name, MaxTokens: mod.maxTokens, Status: mod.status, Verify: mod.verify})
	}
	return out, nil
}

func (m *Mock) AddModelToInstance(ctx context.Context, providerName, instanceName string, model ModelInfo) error {
	p := m.providerMgr(providerName, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil {
		return fmt.Errorf("provider %s not found", providerName)
	}
	inst := p.instances[instanceName]
	if inst == nil {
		return fmt.Errorf("instance %s not found", instanceName)
	}
	maxTokens := model.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	inst.models[model.ModelName] = &mockModel{id: id.New(), name: model.ModelName, modelType: mockModelTypeMaske([]ModelInfo{model}), status: "active", maxTokens: maxTokens, verify: "success"}
	return nil
}

func (m *Mock) UpdateModel(ctx context.Context, providerName, instanceName, modelName string, update ModelUpdate) error {
	p := m.providerMgr(providerName, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil || p.instances[instanceName] == nil {
		return fmt.Errorf("instance not found")
	}
	mod := p.instances[instanceName].models[modelName]
	if mod == nil {
		return fmt.Errorf("model %s not found", modelName)
	}
	if update.Status != "" {
		mod.status = update.Status
	}
	if update.MaxTokens > 0 {
		mod.maxTokens = update.MaxTokens
	}
	if update.ModelType != nil {
		mod.modelType = mockModelTypeMaske(modelInfosFromUpdate(update))
	}
	return nil
}

func modelInfosFromUpdate(update ModelUpdate) []ModelInfo {
	return []ModelInfo{{ModelType: update.ModelType}}
}

func (m *Mock) DeleteModelsFromInstance(ctx context.Context, providerName, instanceName string, modelNames []string) error {
	p := m.providerMgr(providerName, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p == nil || p.instances[instanceName] == nil {
		return nil
	}
	for _, name := range modelNames {
		delete(p.instances[instanceName].models, name)
	}
	return nil
}

func (m *Mock) VerifyConnection(ctx context.Context, providerName, apiKey, baseURL, region string, modelInfo []ModelInfo) (map[string]string, error) {
	out := map[string]string{}
	for _, mi := range modelInfo {
		out[mi.ModelName] = "success"
	}
	return out, nil
}

func (m *Mock) ChatToModel(ctx context.Context, providerName, instanceName, modelName, message string, stream, thinking bool) (string, error) {
	return fmt.Sprintf("{\"message\":\"ok (mock) %s\",\"model\":\"%s\"}", message, modelName), nil
}

func (m *Mock) UploadChatFile(ctx context.Context, filename string, content []byte) (*UploadedFile, error) {
	return &UploadedFile{
		ID:        "mock-upload-" + filename,
		Name:      filename,
		Size:      int64(len(content)),
		Extension: "txt",
		MimeType:  "text/plain",
	}, nil
}

func (m *Mock) UploadAgentFile(ctx context.Context, agentID, filename string, content []byte) (*UploadedFile, error) {
	return &UploadedFile{
		ID:        "mock-agent-upload-" + agentID + "-" + filename,
		Name:      filename,
		Size:      int64(len(content)),
		Extension: strings.TrimPrefix(path.Ext(filename), "."),
		MimeType:  "application/octet-stream",
		CreatedBy: agentID,
	}, nil
}

func (m *Mock) GetChunkImage(ctx context.Context, imageID string) ([]byte, string, error) {
	return []byte("mock-image-" + imageID), "image/png", nil
}
