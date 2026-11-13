package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Parse profile repair (doc/276 §7.4 / R4, R22).
//
// Knowledge contract and dataset reality are separate facts: the template says
// "a book-style knowledge base", the dataset in hand may still be `naive`. The
// platform never rewrites an engine dataset silently — `POST
// /datasets/:id/parse-profile` is the explicit, audited repair, and it refuses
// to destroy already-parsed content unless the caller opts into a re-parse.
const (
	codeParseProfileInvalid         = 40106
	codeParseProfileRequiresRewrite = 40915
	codeDatasetSharedUnacknowledged = 40917
)

// ParseProfileInput is the repair request body.
type ParseProfileInput struct {
	ChunkMethod     string                 `json:"chunk_method"`
	ParserConfig    map[string]interface{} `json:"parser_config,omitempty"`
	RewriteExisting bool                   `json:"rewrite_existing"`
	// AcknowledgeSharedImpact is the explicit confirmation doc/276 §7.4 requires
	// when other applications already retrieve from this dataset.
	AcknowledgeSharedImpact bool `json:"acknowledge_shared_impact"`
}

// ParseProfileView reports the current (or post-repair) parse profile.
type ParseProfileView struct {
	DatasetID        string                 `json:"dataset_id"`
	ChunkMethod      string                 `json:"chunk_method"`
	ParserConfig     map[string]interface{} `json:"parser_config,omitempty"`
	EmbeddingModel   string                 `json:"embedding_model,omitempty"`
	PushdownEnabled  bool                   `json:"pushdown_enabled"`
	DocumentCount    int                    `json:"document_count"`
	ParsedCount      int                    `json:"parsed_count"`
	ReparseTriggered bool                   `json:"reparse_triggered"`
}

// GetParseProfile returns the dataset's current parse profile without touching
// the engine.
func (s *Service) GetParseProfile(ctx context.Context, tenantID, datasetID string) (*ParseProfileView, error) {
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil || link == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	view := &ParseProfileView{DatasetID: link.ID, PushdownEnabled: link.PushdownEnabled}
	cfg, err := s.RAGFlow.GetDatasetConfig(ctx, link.RAGFlowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50294, "ragflow get dataset config failed")
	}
	view.ChunkMethod = normalizeChunkMethod(cfg.ParseMethod())
	view.ParserConfig = cfg.ParserConfig
	view.EmbeddingModel = cfg.EmbeddingModel
	if docs, err := s.RAGFlow.ListDocuments(ctx, link.RAGFlowDatasetID); err == nil {
		view.DocumentCount = len(docs)
		for _, doc := range docs {
			if doc.ChunkCount > 0 {
				view.ParsedCount++
			}
		}
	}
	return view, nil
}

// ApplyParseProfile updates chunk_method/parser_config and, when requested,
// re-parses the existing documents under the new profile.
func (s *Service) ApplyParseProfile(
	ctx context.Context, tenantID, actorID, datasetID string, in ParseProfileInput,
) (*ParseProfileView, error) {
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil || link == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	chunkMethod := normalizeChunkMethod(strings.TrimSpace(in.ChunkMethod))
	if chunkMethod == "" {
		return nil, httperr.BadRequest(codeParseProfileInvalid, "chunk_method is required")
	}
	if !isKnownChunkMethod(chunkMethod) {
		return nil, httperr.BadRequest(codeParseProfileInvalid, "unsupported chunk_method: "+in.ChunkMethod)
	}
	current, err := s.GetParseProfile(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	// Fixed check order (doc/276 §7.4 / S23): 40915 before 40917, because "has
	// documents" already makes the change unsafe on its own — reporting the
	// shared-consumer conflict first would hide the more fundamental blocker.
	if current.ParsedCount > 0 && !in.RewriteExisting {
		s.recordParseProfileAudit(ctx, tenantID, actorID, datasetID, "REJECTED", current.ChunkMethod, chunkMethod)
		return nil, httperr.New(409, codeParseProfileRequiresRewrite,
			"dataset already has parsed documents; set rewrite_existing=true to re-parse under the new profile")
	}
	if err := s.ensureSharedImpactAcknowledged(ctx, tenantID, datasetID, in.AcknowledgeSharedImpact); err != nil {
		s.recordParseProfileAudit(ctx, tenantID, actorID, datasetID, "REJECTED", current.ChunkMethod, chunkMethod)
		return nil, err
	}
	cfg := ragflow.DatasetConfigUpdate{ChunkMethod: &chunkMethod}
	if in.ParserConfig != nil {
		cfg.ParserConfig = in.ParserConfig
	}
	if err := s.UpdateDatasetConfig(ctx, tenantID, datasetID, cfg); err != nil {
		return nil, err
	}
	view := &ParseProfileView{
		DatasetID: datasetID, ChunkMethod: chunkMethod, ParserConfig: in.ParserConfig,
		PushdownEnabled: current.PushdownEnabled, DocumentCount: current.DocumentCount,
		ParsedCount: current.ParsedCount,
	}
	if in.RewriteExisting && current.DocumentCount > 0 {
		docIDs, err := s.enabledDocumentIDs(ctx, link.RAGFlowDatasetID)
		if err != nil {
			return nil, err
		}
		if len(docIDs) > 0 {
			if err := s.ParseDocuments(ctx, tenantID, datasetID, docIDs); err != nil {
				return nil, err
			}
			view.ReparseTriggered = true
		}
	}
	s.recordParseProfileAudit(ctx, tenantID, actorID, datasetID, "SUCCESS", current.ChunkMethod, chunkMethod)
	s.invalidateDatasets()
	return view, nil
}

// ensureSharedImpactAcknowledged blocks a destructive knowledge change when
// other applications are already bound to the dataset and the caller has not
// explicitly confirmed the impact (doc/276 §7.3 step 3 / §7.4).
func (s *Service) ensureSharedImpactAcknowledged(ctx context.Context, tenantID, datasetID string, acknowledged bool) error {
	usage, err := s.datasetSharedUsage(ctx, tenantID, datasetID)
	if err != nil {
		return err
	}
	if usage.Shared() == 0 || acknowledged {
		if acknowledged && usage.Shared() > 0 {
			s.recordSharedImpactAudit(ctx, tenantID, datasetID, usage)
		}
		return nil
	}
	return httperr.New(409, codeDatasetSharedUnacknowledged,
		"dataset is shared by "+itoa(usage.Shared())+" applications; set acknowledge_shared_impact=true to proceed")
}

// datasetSharedUsage merges usage recorded against the local dataset id and the
// upstream RAGFlow id. Shadow rows (chat, search app, release binding) store the
// upstream id, so a local-only lookup reports "not shared" for every dataset
// created through this platform — a check that silently lies.
func (s *Service) datasetSharedUsage(ctx context.Context, tenantID, datasetID string) (repository.DatasetUsage, error) {
	usage, err := s.Store.CountDatasetSharedUsage(ctx, tenantID, datasetID)
	if err != nil {
		return usage, err
	}
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return usage, err
	}
	if link == nil || link.RAGFlowDatasetID == "" || link.RAGFlowDatasetID == datasetID {
		return usage, nil
	}
	upstream, err := s.Store.CountDatasetSharedUsage(ctx, tenantID, link.RAGFlowDatasetID)
	if err != nil {
		return usage, err
	}
	return mergeDatasetUsage(usage, upstream), nil
}

func mergeDatasetUsage(base, extra repository.DatasetUsage) repository.DatasetUsage {
	merged := repository.DatasetUsage{DatasetID: base.DatasetID}
	merged.ChatIDs = mergeIDList(base.ChatIDs, extra.ChatIDs)
	merged.SearchIDs = mergeIDList(base.SearchIDs, extra.SearchIDs)
	merged.ReleaseIDs = mergeIDList(base.ReleaseIDs, extra.ReleaseIDs)
	return merged
}

func mergeIDList(lists ...[]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, list := range lists {
		for _, item := range list {
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) recordSharedImpactAudit(ctx context.Context, tenantID, datasetID string, usage repository.DatasetUsage) {
	entry := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, Action: "dataset.shared_impact_acknowledged",
		Resource: "dataset", ResourceID: datasetID, Result: "SUCCESS",
		DetailJSON: `{"chats":` + itoa(len(usage.ChatIDs)) + `,"search_apps":` + itoa(len(usage.SearchIDs)) +
			`,"releases":` + itoa(len(usage.ReleaseIDs)) + `}`,
	}
	if err := s.RecordAudit(ctx, entry); err != nil {
		logger.Warn("shared impact audit failed", "dataset_id", datasetID, "error", err)
	}
}

// SetDatasetPushdown is the writer that finally makes `pushdown_enabled` real
// (doc/276 §7.4 / S13, R26). It is a local execution policy: the flag is stored
// on rgx_dataset_link and only *triggers* the meta_fields backfill.
func (s *Service) SetDatasetPushdown(
	ctx context.Context, tenantID, actorID, datasetID string, enabled, acknowledgeSharedImpact bool,
) error {
	if err := s.ensureSharedImpactAcknowledged(ctx, tenantID, datasetID, acknowledgeSharedImpact); err != nil {
		return err
	}
	if err := s.Store.SetDatasetPushdownEnabled(ctx, tenantID, datasetID, enabled); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return httperr.NotFound("dataset not found")
		}
		return err
	}
	if enabled {
		// The check must see `pending_sync` until this backfill lands, otherwise a
		// pushdown requirement would look unsatisfied for reasons that are about
		// to disappear.
		s.syncDatasetPushdownMetadataAsync(ctx, tenantID, datasetID)
	}
	s.recordPushdownAudit(ctx, tenantID, actorID, datasetID, enabled)
	s.invalidateDatasets()
	return nil
}

func (s *Service) recordPushdownAudit(ctx context.Context, tenantID, actorID, datasetID string, enabled bool) {
	entry := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: actorID, Action: "dataset.pushdown_enabled",
		Resource: "dataset", ResourceID: datasetID, Result: "SUCCESS",
		DetailJSON: `{"pushdown_enabled":` + boolLiteral(enabled) + `}`,
	}
	if err := s.RecordAudit(ctx, entry); err != nil {
		logger.Warn("pushdown audit failed", "dataset_id", datasetID, "error", err)
	}
}

func boolLiteral(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (s *Service) enabledDocumentIDs(ctx context.Context, ragflowDatasetID string) ([]string, error) {
	docs, err := s.RAGFlow.ListDocuments(ctx, ragflowDatasetID)
	if err != nil {
		return nil, httperr.New(502, 50202, "ragflow list documents failed")
	}
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		if strings.TrimSpace(doc.ID) == "" {
			continue
		}
		ids = append(ids, doc.ID)
	}
	return ids, nil
}

// isKnownChunkMethod is the narrow engine vocabulary the platform will write.
// Legacy aliases are normalized first, so `naive` is accepted as `general` but
// unknown engine strings are not silently passed through.
func isKnownChunkMethod(method string) bool {
	for _, candidate := range []string{"general", "table", "book", "presentation", "picture", "paper", "graph"} {
		if method == candidate {
			return true
		}
	}
	return false
}

func (s *Service) recordParseProfileAudit(
	ctx context.Context, tenantID, actorID, datasetID, result, previous, next string,
) {
	entry := &model.AuditLog{
		ID: id.New(), TenantID: tenantID, UserID: actorID, Action: "dataset.parse_profile",
		Resource: "dataset", ResourceID: datasetID, Result: result,
		DetailJSON: `{"previous_chunk_method":"` + previous + `","chunk_method":"` + next + `"}`,
	}
	if err := s.RecordAudit(ctx, entry); err != nil {
		logger.Warn("parse profile audit failed", "dataset_id", datasetID, "error", err)
	}
}
