package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ListChats returns the tenant's chat assistants (or all tenants for scopeAll).
func (s *Service) ListChats(ctx context.Context, tenantID string, scopeAll bool, filter repository.ChatFilter, page, pageSize int) ([]model.ChatShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListChatShadows(ctx, tenantID, scopeAll, filter, page, pageSize)
}

// ListChatsForScope is the Resolver-backed governance read path. It preserves
// tenant attribution and never grants cross-tenant mutation rights.
func (s *Service) ListChatsForScope(ctx context.Context, scope TenantScope, filter repository.ChatFilter, page, pageSize int) ([]model.ChatShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListChatShadowsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), filter, page, pageSize)
}

// GetChat returns a chat assistant the caller may access.
func (s *Service) GetChat(ctx context.Context, tenantID, chatID string, scopeAll bool) (*model.ChatShadow, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	return cs, nil
}

// GetChatForScope reads a chat assistant only when it belongs to the resolved
// tenant scope.
func (s *Service) GetChatForScope(ctx context.Context, scope TenantScope, chatID string) (*model.ChatShadow, error) {
	cs, err := s.Store.GetChatShadowForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), chatID)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	return cs, nil
}

// GetChatConfig returns the live RAGFlow-side editable config of an owned
// chat so the operator can prefill an edit form with current values.
func (s *Service) GetChatConfig(ctx context.Context, tenantID, chatID string, scopeAll bool) (*ChatConfig, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	live, err := s.RAGFlow.GetChat(ctx, chatID)
	if err != nil {
		return nil, httperr.New(502, 50249, "ragflow get chat config failed")
	}
	if live == nil {
		return nil, httperr.NotFound("chat not found")
	}
	cfg := &ChatConfig{
		ID:                     cs.ID,
		Name:                   live.Name,
		Language:               live.Language,
		DatasetIDs:             live.DatasetIDs,
		KBNames:                live.KBNames,
		PromptConfig:           live.PromptConfig,
		LLMID:                  live.LLMID,
		RerankID:               live.RerankID,
		TopN:                   live.TopN,
		TopK:                   live.TopK,
		SimilarityThreshold:    live.SimilarityThreshold,
		VectorSimilarityWeight: live.VectorSimilarityWeight,
	}
	return cfg, nil
}

// GetChatConfigForScope returns live configuration for a governance read. The
// caller must still go through the current-workspace update endpoint to edit it.
func (s *Service) GetChatConfigForScope(ctx context.Context, scope TenantScope, chatID string) (*ChatConfig, error) {
	cs, err := s.GetChatForScope(ctx, scope, chatID)
	if err != nil {
		return nil, err
	}
	live, err := s.RAGFlow.GetChat(ctx, chatID)
	if err != nil {
		return nil, httperr.New(502, 50249, "ragflow get chat config failed")
	}
	if live == nil {
		return nil, httperr.NotFound("chat not found")
	}
	return &ChatConfig{
		ID:                     cs.ID,
		Name:                   live.Name,
		Language:               live.Language,
		DatasetIDs:             live.DatasetIDs,
		KBNames:                live.KBNames,
		PromptConfig:           live.PromptConfig,
		LLMID:                  live.LLMID,
		RerankID:               live.RerankID,
		TopN:                   live.TopN,
		TopK:                   live.TopK,
		SimilarityThreshold:    live.SimilarityThreshold,
		VectorSimilarityWeight: live.VectorSimilarityWeight,
	}, nil
}

// ModelOptions carries the RAGFlow tenant's model catalog and defaults for the
// Chat config editor (LLM / rerank pickers).
type ModelOptions struct {
	Chat      []ragflow.AddedModel   `json:"chat"`
	Rerank    []ragflow.AddedModel   `json:"rerank"`
	Defaults  []ragflow.DefaultModel `json:"defaults"`
	Embedding []ragflow.AddedModel   `json:"embedding"`
}

// GetModelOptions returns concrete RAGFlow models and tenant default settings
// so the UI lists real model names instead of a blank "use RAGFlow default".
func (s *Service) GetModelOptions(ctx context.Context) (*ModelOptions, error) {
	chat, err := s.RAGFlow.ListModels(ctx, "chat")
	if err != nil {
		return nil, httperr.New(502, 50250, "list ragflow chat models failed")
	}
	rerank, err := s.RAGFlow.ListModels(ctx, "rerank")
	if err != nil {
		return nil, httperr.New(502, 50251, "list ragflow rerank models failed")
	}
	defaults, err := s.RAGFlow.ListDefaultModels(ctx)
	if err != nil {
		return nil, httperr.New(502, 50252, "list ragflow default models failed")
	}
	embedding, err := s.RAGFlow.ListModels(ctx, "embedding")
	if err != nil {
		return nil, httperr.New(502, 50253, "list ragflow embedding models failed")
	}
	return &ModelOptions{Chat: chat, Rerank: rerank, Defaults: defaults, Embedding: embedding}, nil
}

// CreateChat creates a chat assistant in RAGFlow and records its ownership.
// CreateChat creates a chat assistant in RAGFlow and records its ownership.
func (s *Service) CreateChat(ctx context.Context, tenantID, name string, datasetIDs []string) (*model.ChatShadow, error) {
	if strings.TrimSpace(name) == "" {
		return nil, httperr.BadRequest(40090, "chat name is required")
	}
	resolvedDatasetIDs, resolveErr := s.resolveRAGFlowDatasetIDs(ctx, tenantID, datasetIDs)
	if resolveErr != nil {
		return nil, resolveErr
	}
	datasetIDs = resolvedDatasetIDs
	if err := s.validateOwnedDatasets(ctx, datasetIDs); err != nil {
		return nil, err
	}
	created, err := s.RAGFlow.CreateChat(ctx, ragflow.CreateChatRequest{
		Name: name, DatasetIDs: datasetIDs, Language: chatLanguage(""),
	})
	if err != nil {
		return nil, httperr.New(502, 50240, "ragflow create chat failed")
	}
	now := time.Now().UTC()
	cs := &model.ChatShadow{
		ID: created.ID, TenantID: tenantID, Name: created.Name, Status: "active",
		DatasetIDs: strings.Join(datasetIDs, ","), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.UpsertChatShadow(ctx, cs); err != nil {
		if deleteErr := s.RAGFlow.DeleteChat(ctx, cs.ID); deleteErr != nil {
			logger.Warn("failed to roll back external chat after local persistence failure",
				"chat_id", cs.ID, "error", deleteErr)
		}
		return nil, err
	}
	return cs, nil
}

// UpdateChat updates a chat assistant the caller owns.
func (s *Service) UpdateChat(ctx context.Context, tenantID, chatID, name string, datasetIDs []string, scopeAll bool) (*model.ChatShadow, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	req := ragflow.UpdateChatRequest{}
	if name != "" {
		req.Name = name
	}
	if datasetIDs != nil {
		resolvedDatasetIDs, resolveErr := s.resolveRAGFlowDatasetIDs(ctx, tenantID, datasetIDs)
		if resolveErr != nil {
			return nil, resolveErr
		}
		datasetIDs = resolvedDatasetIDs
		if err := s.validateOwnedDatasets(ctx, datasetIDs); err != nil {
			return nil, err
		}
		req.DatasetIDs = datasetIDs
	}
	if _, err := s.RAGFlow.UpdateChat(ctx, chatID, req); err != nil {
		return nil, httperr.New(502, 50241, "ragflow update chat failed")
	}
	if name != "" {
		cs.Name = name
	}
	if datasetIDs != nil {
		cs.DatasetIDs = strings.Join(datasetIDs, ",")
	}
	cs.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpsertChatShadow(ctx, cs); err != nil {
		return nil, err
	}
	return cs, nil
}
func (s *Service) DeleteChat(ctx context.Context, tenantID, chatID string, scopeAll bool) error {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return err
	}
	if cs == nil {
		return httperr.NotFound("chat not found")
	}
	if err := s.RAGFlow.DeleteChat(ctx, chatID); err != nil {
		return httperr.New(502, 50242, "ragflow delete chat failed")
	}
	if err := s.Store.DeleteAssistantCatalog(ctx, cs.TenantID, model.AssistantKindChat, chatID); err != nil {
		return err
	}
	return s.Store.DeleteChatShadow(ctx, chatID)
}

// ListChatSessions lists the sessions of an owned chat assistant.
func (s *Service) ListChatSessions(ctx context.Context, tenantID, chatID string, scopeAll bool, opts ragflow.SessionListOptions) ([]ragflow.Session, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	sessions, err := s.RAGFlow.ListChatSessions(ctx, chatID, opts)
	if err != nil {
		return nil, httperr.New(502, 50243, "ragflow list sessions failed")
	}
	return sessions, nil
}

// GetChatSession returns an owned chat's session with messages.
func (s *Service) GetChatSession(ctx context.Context, tenantID, chatID, sessionID string, scopeAll bool) (*ragflow.Session, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	sess, err := s.RAGFlow.GetChatSession(ctx, chatID, sessionID)
	if err != nil {
		return nil, httperr.New(502, 50244, "ragflow get session failed")
	}
	return sess, nil
}

// ListChatSessionMessages returns an owned chat session as bounded pages.
func (s *Service) ListChatSessionMessages(ctx context.Context, tenantID, chatID, sessionID string, scopeAll bool, opts ragflow.SessionMessagePageOptions) ([]ragflow.Message, string, error) {
	if _, err := s.GetChatSession(ctx, tenantID, chatID, sessionID, scopeAll); err != nil {
		return nil, "", err
	}
	items, next, err := s.RAGFlow.ListChatSessionMessages(ctx, chatID, sessionID, opts)
	if err != nil {
		return nil, "", httperr.New(502, 50245, "ragflow list session messages failed")
	}
	return items, next, nil
}

// CreateChatSession creates a session on an owned chat assistant.
func (s *Service) CreateChatSession(ctx context.Context, tenantID, chatID, name string, scopeAll bool) (*ragflow.Session, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	sess, err := s.RAGFlow.CreateChatSession(ctx, chatID, name)
	if err != nil {
		return nil, httperr.New(502, 50245, "ragflow create session failed")
	}
	return sess, nil
}

// DeleteChatSessions deletes sessions of an owned chat assistant.
func (s *Service) DeleteChatSessions(ctx context.Context, tenantID, chatID string, sessionIDs []string, scopeAll bool) error {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return err
	}
	if cs == nil {
		return httperr.NotFound("chat not found")
	}
	if err := s.RAGFlow.DeleteChatSessions(ctx, chatID, sessionIDs); err != nil {
		return httperr.New(502, 50246, "ragflow delete sessions failed")
	}
	return nil
}

// getOwnedChat resolves a chat shadow scoped to the caller's tenant (unless
// scopeAll is set for platform admins). Returns nil when not found/not owned.
func (s *Service) getOwnedChat(ctx context.Context, tenantID, chatID string, scopeAll bool) (*model.ChatShadow, error) {
	cs, err := s.Store.GetChatShadow(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	return cs, nil
}

// ChatAuthoring carries the RAGFlow authoring knobs exposed to the platform so
// tenants can configure prompts, retrieval and model locally instead of only
// creating a shell.
type ChatAuthoring struct {
	PromptConfig           map[string]interface{} `json:"prompt_config,omitempty"`
	Language               string                 `json:"language,omitempty"`
	LLMID                  string                 `json:"llm_id,omitempty"`
	RerankID               string                 `json:"rerank_id,omitempty"`
	TopN                   int                    `json:"top_n,omitempty"`
	TopK                   int                    `json:"top_k,omitempty"`
	SimilarityThreshold    float64                `json:"similarity_threshold,omitempty"`
	VectorSimilarityWeight float64                `json:"vector_similarity_weight,omitempty"`
	LLMSetting             map[string]interface{} `json:"llm_setting,omitempty"`
}

func (a ChatAuthoring) isEmpty() bool {
	return a.Language == "" && a.PromptConfig == nil && a.LLMID == "" && a.RerankID == "" &&
		a.TopN == 0 && a.TopK == 0 && a.SimilarityThreshold == 0 && a.VectorSimilarityWeight == 0 && a.LLMSetting == nil
}

func chatLanguage(language string) string {
	if strings.TrimSpace(language) == "" {
		return "Chinese"
	}
	return language
}

func chatConfigJSON(a ChatAuthoring) string {
	b, _ := json.Marshal(a)
	return string(b)
}

// ChatConfig is the editable RAGFlow-side configuration of a chat assistant,
// used to prefill and persist the operator's prompt/model/retrieval knobs.
type ChatConfig struct {
	ID                     string                 `json:"id"`
	Name                   string                 `json:"name"`
	Language               string                 `json:"language,omitempty"`
	DatasetIDs             []string               `json:"dataset_ids"`
	KBNames                []string               `json:"kb_names,omitempty"`
	PromptConfig           map[string]interface{} `json:"prompt_config"`
	LLMID                  string                 `json:"llm_id"`
	RerankID               string                 `json:"rerank_id"`
	TopN                   int                    `json:"top_n"`
	TopK                   int                    `json:"top_k"`
	SimilarityThreshold    float64                `json:"similarity_threshold"`
	VectorSimilarityWeight float64                `json:"vector_similarity_weight"`
}

// CreateChatWithConfig creates a chat assistant with authoring config.
// mergePromptConfig layers the operator's prompt keys over the RAGFlow-side
// prompt config so an edit/create never drops standard retrieval keys (e.g.
// empty_response, parameters, quote) the tenant did not explicitly touch.
func (s *Service) mergePromptConfig(ctx context.Context, chatID string, a ChatAuthoring) map[string]interface{} {
	if a.PromptConfig == nil {
		return nil
	}
	merged := defaultPromptConfigSeed()
	if live, err := s.RAGFlow.GetChat(ctx, chatID); err == nil && live != nil && live.PromptConfig != nil {
		for k, v := range live.PromptConfig {
			merged[k] = v
		}
	}
	for k, v := range a.PromptConfig {
		merged[k] = v
	}
	return merged
}

// defaultPromptConfigSeed mirrors RAGFlow's standard chat prompt so a
// chat/update always carries the keys RAGFlow reads (system, empty_response,
// parameters, quote, refine_multiturn) and never regresses to a stripped
// config that would crash completion with a missing 'system'/'empty_response'.
func defaultPromptConfigSeed() map[string]interface{} {
	return map[string]interface{}{
		"system":   "你是企业知识助手。请基于知识库内容回答用户的问题，优先引用知识库信息；如果知识库内容与问题无关，请明确说明未找到相关内容。\n以下是知识库：\n{knowledge}\n以上是知识库。",
		"prologue": "你好！我是企业知识助手，请问有什么可以帮您？",
		"parameters": []interface{}{
			map[string]interface{}{"key": "knowledge", "optional": false},
			map[string]interface{}{"key": "date", "optional": true},
		},
		"empty_response":   "抱歉，当前数据范围内没有找到相关内容，请换个问法试试。",
		"quote":            true,
		"tts":              false,
		"refine_multiturn": true,
	}
}
func (s *Service) CreateChatWithConfig(ctx context.Context, tenantID, name string, datasetIDs []string, a ChatAuthoring) (*model.ChatShadow, error) {
	if a.isEmpty() {
		cs, err := s.CreateChat(ctx, tenantID, name, datasetIDs)
		if err != nil {
			return nil, err
		}
		return cs, nil
	}
	resolvedDatasetIDs, err := s.resolveRAGFlowDatasetIDs(ctx, tenantID, datasetIDs)
	if err != nil {
		return nil, err
	}
	if err := s.validateOwnedDatasets(ctx, resolvedDatasetIDs); err != nil {
		return nil, err
	}
	a.PromptConfig = s.mergePromptConfig(ctx, "", a)
	created, err := s.RAGFlow.CreateChat(ctx, ragflow.CreateChatRequest{
		Name:                   name,
		DatasetIDs:             resolvedDatasetIDs,
		Language:               chatLanguage(a.Language),
		PromptConfig:           a.PromptConfig,
		LLMID:                  a.LLMID,
		RerankID:               a.RerankID,
		TopN:                   a.TopN,
		TopK:                   a.TopK,
		SimilarityThreshold:    a.SimilarityThreshold,
		VectorSimilarityWeight: a.VectorSimilarityWeight,
	})
	if err != nil {
		return nil, httperr.New(502, 50240, "ragflow create chat failed")
	}
	cs := &model.ChatShadow{
		ID: created.ID, TenantID: tenantID, Name: created.Name, Status: "active",
		DatasetIDs: strings.Join(resolvedDatasetIDs, ","), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	cs.ConfigJSON = chatConfigJSON(a)
	if err := s.Store.UpsertChatShadow(ctx, cs); err != nil {
		if deleteErr := s.RAGFlow.DeleteChat(ctx, cs.ID); deleteErr != nil {
			logger.Warn("failed to roll back external chat after local persistence failure",
				"chat_id", cs.ID, "error", deleteErr)
		}
		return nil, err
	}
	return cs, nil
}

// UpdateChatWithConfig updates a chat assistant with authoring config.
func (s *Service) UpdateChatWithConfig(ctx context.Context, tenantID, chatID, name string, datasetIDs []string, a ChatAuthoring, scopeAll bool) (*model.ChatShadow, error) {
	cs, err := s.UpdateChat(ctx, tenantID, chatID, name, datasetIDs, scopeAll)
	if err != nil {
		return nil, err
	}
	if a.isEmpty() {
		return cs, nil
	}
	a.PromptConfig = s.mergePromptConfig(ctx, chatID, a)
	updateReq := chatAuthoringToProvider(a)
	updateReq.Language = chatLanguage(a.Language)
	if _, err := s.RAGFlow.UpdateChat(ctx, chatID, updateReq); err != nil {
		return nil, httperr.New(502, 50241, "ragflow update chat failed")
	}
	cs.ConfigJSON = chatConfigJSON(a)
	if err := s.Store.UpsertChatShadow(ctx, cs); err != nil {
		return nil, err
	}
	return cs, nil
}

func chatAuthoringToProvider(a ChatAuthoring) ragflow.UpdateChatRequest {
	req := ragflow.UpdateChatRequest{}
	if a.PromptConfig != nil {
		req.PromptConfig = a.PromptConfig
	}
	// Only send fields the operator explicitly set so an edit does not
	// blank out RAGFlow-side model/retrieval values left empty.
	if a.LLMID != "" {
		req.LLMID = a.LLMID
	}
	if a.RerankID != "" {
		req.RerankID = a.RerankID
	}
	if a.TopN > 0 {
		req.TopN = a.TopN
	}
	if a.TopK > 0 {
		req.TopK = a.TopK
	}
	if a.SimilarityThreshold > 0 {
		req.SimilarityThreshold = a.SimilarityThreshold
	}
	if a.VectorSimilarityWeight > 0 {
		req.VectorSimilarityWeight = a.VectorSimilarityWeight
	}
	if a.LLMSetting != nil {
		req.LLMSetting = a.LLMSetting
	}
	return req
}

// UpdateChatSession renames a session of an owned chat assistant.
func (s *Service) UpdateChatSession(ctx context.Context, tenantID, chatID, sessionID, name string, scopeAll bool) (*ragflow.Session, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	sess, err := s.RAGFlow.UpdateChatSession(ctx, chatID, sessionID, name)
	if err != nil {
		return nil, httperr.New(502, 50247, "ragflow rename session failed")
	}
	return sess, nil
}

// BatchUpdateChatStatus enables/disables chatbots the caller may manage.
func (s *Service) BatchUpdateChatStatus(ctx context.Context, tenantID string, scopeAll bool, ids []string, status string) error {
	if status != model.TenantStatusActive && status != model.TenantStatusDisabled {
		return httperr.BadRequest(40002, "invalid status")
	}
	return s.Store.BatchUpdateChatStatus(ctx, tenantID, scopeAll, ids, status)
}

// BatchDeleteChats deletes the given chatbots (shadow + RAGFlow).
func (s *Service) BatchDeleteChats(ctx context.Context, tenantID string, chatIDs []string, scopeAll bool) error {
	for _, id := range chatIDs {
		cs, err := s.getOwnedChat(ctx, tenantID, id, scopeAll)
		if err != nil {
			return err
		}
		if cs == nil {
			continue
		}
		if err := s.RAGFlow.DeleteChat(ctx, id); err != nil {
			return httperr.New(502, 50242, "ragflow delete chat failed")
		}
		if err := s.Store.DeleteChatShadow(ctx, id); err != nil {
			return err
		}
		if err := s.Store.DeleteAssistantCatalog(ctx, cs.TenantID, model.AssistantKindChat, id); err != nil {
			return err
		}
	}
	return nil
}

// ChatUsage returns the metered usage/cost attributed to a chat.
func (s *Service) ChatUsage(ctx context.Context, tenantID, chatID string, scopeAll bool) (*repository.ChatUsageAgg, error) {
	cs, err := s.getOwnedChat(ctx, tenantID, chatID, scopeAll)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		return nil, httperr.NotFound("chat not found")
	}
	return s.Store.SumCostMetricByChat(ctx, tenantID, chatID)
}

// validateOwnedDatasets verifies that the given dataset ids are owned by the
// current RAGFlow engine account before binding them to a chat. It only runs
// against the real HTTP engine (mock skips so offline/tests stay fast).
func (s *Service) validateOwnedDatasets(ctx context.Context, datasetIDs []string) error {
	if len(datasetIDs) == 0 || s.RAGFlow.Name() != "http" {
		return nil
	}
	owned, err := s.RAGFlow.ListDatasets(ctx)
	if err != nil {
		return httperr.New(502, 50248, "无法校验数据集归属")
	}
	has := map[string]bool{}
	for _, d := range owned {
		has[d.ID] = true
	}
	var missing []string
	for _, id := range datasetIDs {
		if !has[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return httperr.BadRequest(40092, "以下数据集不属于当前RAGFlow引擎账号，无法绑定到该助手: "+strings.Join(missing, ","))
	}
	return nil
}

// resolveRAGFlowDatasetIDs maps any platform rgx_dataset_link ids to their
// RAGFlow dataset ids so both id styles can be used when binding knowledge
// bases to a chat.
func (s *Service) resolveRAGFlowDatasetIDs(ctx context.Context, tenantID string, ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		link, err := s.Store.GetDatasetLink(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		if link == nil || link.RAGFlowDatasetID == "" {
			out = append(out, id)
			continue
		}
		out = append(out, link.RAGFlowDatasetID)
	}
	return out, nil
}
