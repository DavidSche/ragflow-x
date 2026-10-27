package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// 文档级治理元数据键（T2，doc/224 §2.1）。前缀 rgx_ 与 123 §3.1 冲突避免
// 约定一致；auto 通道把 X 侧标注委托同步进 RAGFlow meta_fields，作为检索
// metadata_condition 过滤的数据基础（与 123 §1.4 回填闭环同构）。
const (
	metadataKeyGovernanceLevel  = "rgx_governance_level"
	metadataKeyGovernanceStatus = "rgx_governance_status"
	metadataKeyGovernanceAuth   = "rgx_governance_authority"

	// DocumentGovernanceProjectionPolicyVersion identifies governance
	// projection policy ("governance-projection.v1").
	DocumentGovernanceProjectionPolicyVersion = "governance-projection.v1"

	// GovernanceEmptySetMessage is returned to the caller when 空集 fail-safe
	// triggers: the merged governance conditions leave no visible document in
	// the query domain, and falling back to unfiltered results is forbidden.
	GovernanceEmptySetMessage = "无当前权限内相关结果，已按文档治理投影收紧检索域"
)

// DocumentExclusion records one annotation excluded by governance projection.
type DocumentExclusion struct {
	DocumentID string `json:"document_id"`
	Level      string `json:"level"`
	Status     string `json:"status,omitempty"`
	Reason     string `json:"reason"`
}

// DocumentGovernanceProjection is the per-request result of projecting the
// document-level governance layer onto a retrieval domain (doc/224 §2.1):
// (RBAC 可见域 ∩ 密级过滤 ∩ 权威度权重)。检索主路径是 metadata_condition 下推
// （Pushdown），结果端二次过滤是临时保真兜底（SecondaryFilter 标记），两者
// 叠加后只剩「无权限内相关结果」的空集守卫可直接短路。
type DocumentGovernanceProjection struct {
	Policy         string                     `json:"policy_version"`
	DatasetID      string                     `json:"dataset_id"`
	Clearance      string                     `json:"clearance"`
	HasAnnotations bool                       `json:"has_annotations"`
	Excluded       []DocumentExclusion        `json:"excluded"`
	FilteredOut    bool                       `json:"filtered_out"`
	Pushdown       *ragflow.MetadataCondition `json:"pushdown,omitempty"`
	PushdownUsed   bool                       `json:"pushdown_used"`
	Degraded       bool                       `json:"degraded"`
	Reason         string                     `json:"reason,omitempty"`
}

// excludedDocs returns the set of excluded document ids keyed by reason.
func (p *DocumentGovernanceProjection) excludedDocs() map[string]string {
	out := make(map[string]string, len(p.Excluded))
	for _, e := range p.Excluded {
		out[e.DocumentID] = e.Reason
	}
	return out
}

// ProjectDocumentGovernance projects the document-level governance layer for
// one dataset given a user's clearance and the retrieval candidate document
// set. It never returns an error on governance-collection failure — callers
// degrade to the projection/redaction fallback (123 §6.4 philosophy). A
// non-nil error here means the projection itself could not be evaluated and
// the caller MUST NOT trust engine-side filtering for this dataset.
//
//   - clearance: user 密级 (default internal);
//   - candidates: optional retrieval-domain document ids (when known). When
//     non-empty and all candidates are excluded → FilteredOut (空集 fail-safe);
//   - un-annotated documents remain visible (never filter by absence).
func (s *Service) ProjectDocumentGovernance(
	ctx context.Context, tenantID, userID, datasetID string, candidates []string,
) (*DocumentGovernanceProjection, error) {
	clearanceLevel, err := s.ResolveClearanceLevel(ctx, tenantID, userID)
	if err != nil {
		obs.Get().RecordDocumentGovernanceProjection("degraded", 0)
		return nil, fmt.Errorf("resolve clearance: %w", err)
	}
	clearanceRank := model.GovernanceLevelRank(clearanceLevel)
	annotations, err := s.Store.ListDatasetDocumentGovernance(ctx, tenantID, datasetID)
	if err != nil {
		obs.Get().RecordDocumentGovernanceProjection("degraded", 0)
		return nil, fmt.Errorf("list document governance: %w", err)
	}
	now := time.Now().UTC()
	proj := &DocumentGovernanceProjection{
		Policy:         DocumentGovernanceProjectionPolicyVersion,
		DatasetID:      datasetID,
		Clearance:      clearanceLevel,
		HasAnnotations: len(annotations) > 0,
		Excluded:       []DocumentExclusion{},
	}
	sort.Slice(annotations, func(i, j int) bool {
		return model.GovernanceLevelRank(annotations[i].Level) > model.GovernanceLevelRank(annotations[j].Level)
	})
	for _, g := range annotations {
		switch {
		case !g.Active(now):
			proj.Excluded = append(proj.Excluded, DocumentExclusion{
				DocumentID: g.DocumentID, Level: g.Level, Status: g.Status,
				Reason: "annotation_inactive",
			})
		case model.GovernanceLevelRank(g.Level) > clearanceRank:
			proj.Excluded = append(proj.Excluded, DocumentExclusion{
				DocumentID: g.DocumentID, Level: g.Level, Status: g.Status,
				Reason: "beyond_clearance",
			})
		case g.Status == model.GovernanceStatusDraft:
			proj.Excluded = append(proj.Excluded, DocumentExclusion{
				DocumentID: g.DocumentID, Level: g.Level, Status: g.Status,
				Reason: "draft",
			})
		}
	}
	// 空集 fail-safe: when the retrieval domain is fully excluded, report
	// FilteredOut so callers short-circuit to "无当前权限内相关结果" instead of
	// falling back to the unfiltered set.
	if len(candidates) > 0 {
		excluded := proj.excludedDocs()
		visible := 0
		for _, id := range candidates {
			if _, blocked := excluded[id]; !blocked {
				visible++
			}
		}
		proj.FilteredOut = visible == 0
	}
	// Pushdown condition construction (123 §3.3 形态): exclusion form on the
	// governance level key + draft-status exclusion. Un-annotated documents
	// carry no rgx_governance_level so a "!=" condition preserves them at the
	// engine layer; the result-side filter (see FilterChunksForGovernance)
	// guarantees the outcome regardless of missing-field semantics.
	if len(proj.Excluded) > 0 {
		p := s.buildGovernancePushdownCondition(proj)
		proj.Pushdown = p
		proj.PushdownUsed = p != nil && !p.Empty()
	}
	// T2.3 obs metrics (metricsEnabled guarded inside obs.Get()).
	outcome := "ok"
	if len(proj.Excluded) > 0 {
		outcome = "excluded"
	}
	if proj.FilteredOut {
		outcome = "filtered_out"
	}
	obs.Get().RecordDocumentGovernanceProjection(outcome, len(proj.Excluded))
	return proj, nil
}

// buildGovernancePushdownCondition reduces the excluded level/status values
// into an engine metadata_condition (exclusion form, consistent with doc/123).
func (s *Service) buildGovernancePushdownCondition(proj *DocumentGovernanceProjection) *ragflow.MetadataCondition {
	levels := map[string]bool{}
	hasDraft := false
	for _, e := range proj.Excluded {
		levels[e.Level] = true
		if e.Status == model.GovernanceStatusDraft {
			hasDraft = true
		}
	}
	cond := &ragflow.MetadataCondition{Logic: "and"}
	excludedLevels := make([]string, 0, len(levels))
	for lvl := range levels {
		if lvl == "" {
			continue
		}
		excludedLevels = append(excludedLevels, lvl)
	}
	sort.Strings(excludedLevels)
	for _, lvl := range excludedLevels {
		cond.Conditions = append(cond.Conditions, ragflow.MetadataConditionOp{
			Name: metadataKeyGovernanceLevel, ComparisonOperator: operatorNotEqual, Value: lvl,
		})
	}
	if hasDraft {
		cond.Conditions = append(cond.Conditions, ragflow.MetadataConditionOp{
			Name: metadataKeyGovernanceStatus, ComparisonOperator: operatorNotEqual, Value: model.GovernanceStatusDraft,
		})
	}
	if len(cond.Conditions) == 0 {
		return nil
	}
	return cond
}

// MergeDocumentGovernanceCondition merges the governance pushdown into a base
// metadata condition (and-logic), preserving the existing version/doc IDs.
func MergeDocumentGovernanceCondition(
	base *ragflow.MetadataCondition, governance *ragflow.MetadataCondition,
) *ragflow.MetadataCondition {
	if governance == nil || governance.Empty() {
		return base
	}
	merged := cloneKnowledgeMetadataCondition(base)
	if merged == nil {
		merged = &ragflow.MetadataCondition{Logic: "and"}
	}
	if merged.Logic != "and" {
		// A non-and base cannot safely absorb an and-group in the flat op model
		// (no nesting support). Honest fallback: keep the base; the result-side
		// secondary filter (FilterChunksForGovernance) still guarantees the
		// outcome — the engine condition is a best-effort first layer
		// (123 §3.3 不变式), never the only one.
		return base
	}
	merged.Conditions = append(merged.Conditions, governance.Conditions...)
	return merged
}

// FilterChunksForGovernance applies result-side secondary filtering by
// excluding chunks owned by excluded documents. It is the temporary fidelity
// fallback (projection_degraded) — never the长期主路径. Chunks carry their
// source document under "document_id" (or legacy "doc_id").
func FilterChunksForGovernance(
	chunks []map[string]interface{}, excluded map[string]string,
) (filtered []map[string]interface{}, dropped int) {
	if len(excluded) == 0 {
		return chunks, 0
	}
	filtered = make([]map[string]interface{}, 0, len(chunks))
	for _, chunk := range chunks {
		docID := stringFromAny(chunk["document_id"])
		if docID == "" {
			docID = stringFromAny(chunk["doc_id"])
		}
		if docID != "" {
			if _, blocked := excluded[docID]; blocked {
				dropped++
				continue
			}
		}
		filtered = append(filtered, chunk)
	}
	return filtered, dropped
}

// SyncDocumentGovernanceMetadata is the auto/委托 annotation channel (T2.2):
// it syncs the X-side governance annotations into RAGFlow document
// meta_fields (rgx_governance_*) so they participate in engine-side
// metadata_condition filtering (doc/123 §5 回填闭环同构). Best-effort —
// failures must not block the caller's control-plane operation.
func (s *Service) SyncDocumentGovernanceMetadata(
	ctx context.Context, tenantID, userID, datasetID string,
) error {
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return fmt.Errorf("governance sync dataset: %w", err)
	}
	if link == nil {
		return fmt.Errorf("governance sync dataset not found")
	}
	annotations, err := s.Store.ListDatasetDocumentGovernance(ctx, tenantID, datasetID)
	if err != nil {
		return fmt.Errorf("list document governance for sync: %w", err)
	}
	if len(annotations) == 0 {
		return nil
	}
	for _, g := range annotations {
		updates := []ragflow.MetadataUpdate{
			{Key: metadataKeyGovernanceLevel, Value: g.Level},
			{Key: metadataKeyGovernanceStatus, Value: g.Status},
			{Key: metadataKeyGovernanceAuth, Value: fmt.Sprintf("%d", g.Authority)},
		}
		if err := s.RAGFlow.UpdateDocumentMetadata(ctx, link.RAGFlowDatasetID, g.DocumentID, map[string]interface{}{
			metadataKeyGovernanceLevel:  g.Level,
			metadataKeyGovernanceStatus: g.Status,
			metadataKeyGovernanceAuth:   fmt.Sprintf("%d", g.Authority),
		}); err != nil {
			_ = updates // keep structured for batching evolution
			return fmt.Errorf("sync governance metadata doc %s: %w", g.DocumentID, err)
		}
	}
	return nil
}
