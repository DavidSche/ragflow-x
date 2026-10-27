package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// governance_auto_projection (T1, doc/224 §2.2)：把治理 schema 对文档内容/
// 元数据的匹配结果自动写入 document_governance（source=auto），供 T2 检索
// 投影消费。抽取质量门槛（有效抽取质量 < schema.quality_min 不进入投影）；
// 已存在非 auto 人工/审批标注时人工优先，不覆盖。目标只落在 T2 可识别档位
//（level 三档 / status 两档）；扩展词条命中记为 dormant（投影待 T2 level 展期）。

const (
	// GovernanceAutoProjectionVersion 标注自动投影参数版本。
	GovernanceAutoProjectionVersion = "governance-auto-projection.v1"

	// autoProjectionQualityDefault 无质量报告时按无证据放行（不降级），见
	// T6.2「无证据不填下限」同口径。
	autoProjectionQualityDefault = 100
)

// GovernanceAutoProjectionResult 汇总一次自动投影执行。
type GovernanceAutoProjectionResult struct {
	TenantID          string `json:"tenant_id"`
	DatasetID         string `json:"dataset_id"`
	Scanned           int    `json:"scanned"`         // 参与匹配的文档数
	Matched           int    `json:"matched"`         // 至少命中一个可投影 schema 的文档数
	QualitySkipped    int    `json:"quality_skipped"` // 抽取质量未达门槛被拦
	ManualOverride    int    `json:"manual_override"` // 已存在人工/审批标注，auto 不覆盖
	Dormant           int    `json:"dormant"`         // 命中扩展词条（暂不可投影）
	Written           int    `json:"written"`         // 写入/更新 auto 标注数
	SyncFailed        int    `json:"sync_failed"`     // 回填 RAGFlow 元数据失败（降级计数）
	SchemaUsed        int    `json:"schema_used"`     // 实际使用的生效 schema 数
	ProjectionVersion string `json:"projection_version"`
}

var autoProjectableLevel = map[string]bool{
	model.GovernanceLevelInternal:     true,
	model.GovernanceLevelConfidential: true,
	model.GovernanceLevelTopSecret:    true,
}

var autoProjectableStatus = map[string]bool{
	model.GovernanceStatusDraft:     true,
	model.GovernanceStatusPublished: true,
}

// AutoProjectGovernance 对 dataset 内文档执行自动治理投影。仅 governor 可
// 触发（RBAC execute:document-governance 由调用方/handler 保证）。
func (s *Service) AutoProjectGovernance(
	ctx context.Context, tenantID, userID, datasetID string,
) (*GovernanceAutoProjectionResult, error) {
	link, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, httperr.NotFound("dataset not found")
	}

	schemas, err := s.Store.ListGovernanceSchemas(ctx, tenantID, repository.GovernanceSchemaFilter{DatasetID: datasetID})
	if err != nil {
		return nil, err
	}
	active := make([]*model.GovernanceSchema, 0, len(schemas))
	for i := range schemas {
		if schemas[i].Active {
			active = append(active, &schemas[i])
		}
	}

	docs, err := s.RAGFlow.ListDocuments(ctx, link.RAGFlowDatasetID)
	if err != nil {
		return nil, fmt.Errorf("list documents for auto projection: %w", err)
	}

	docIDs := make([]string, 0, len(docs))
	for _, d := range docs {
		docIDs = append(docIDs, d.ID)
	}
	// 批量取抽取质量报告，缺失 → 无证据放行。
	quality, err := s.governanceQualityByDocument(ctx, tenantID, docIDs)
	if err != nil {
		return nil, err
	}

	result := &GovernanceAutoProjectionResult{
		TenantID: tenantID, DatasetID: datasetID,
		Scanned: len(docs), SchemaUsed: len(active),
		ProjectionVersion: GovernanceAutoProjectionVersion,
	}
	authorityAutoProjectionDefault := model.GovernanceDefaultAuthority

	// 预编译 regex（schema 已在创建/更新时校验可编译）。
	compiled := map[string]*regexp.Regexp{}
	for _, sc := range active {
		if sc.MatcherType == model.SchemaMatcherRegex {
			r, err := regexp.Compile(sc.Pattern)
			if err != nil {
				continue // 非法 regex 的 schema 跳过（编辑期已校验）
			}
			compiled[sc.ID] = r
		}
	}

	for _, doc := range docs {
		targetLevel, targetStatus, dormantHit := s.governanceMatchTargets(datasetID, active, compiled, doc)
		if targetLevel == "" && targetStatus == "" {
			if dormantHit > 0 {
				result.Dormant += dormantHit
			}
			continue
		}
		result.Matched++

		qualityScore, ok := quality[doc.ID]
		if !ok {
			qualityScore = autoProjectionQualityDefault
		}
		minQuality := 0
		for _, sc := range active {
			if sc.AppliesTo(datasetID, doc.ID) && sc.QualityMin > minQuality {
				minQuality = sc.QualityMin
			}
		}
		if qualityScore < float64(minQuality) {
			result.QualitySkipped++
			obs.Get().RecordGovernanceAutoProjection("quality_skipped")
			continue
		}

		// 人工/审批标注优先：已存在非 auto 记录则不覆盖。
		existing, err := s.Store.GetDocumentGovernance(ctx, tenantID, datasetID, doc.ID)
		if err != nil {
			return nil, err
		}
		if existing != nil && existing.Source != model.GovernanceSourceAuto {
			result.ManualOverride++
			obs.Get().RecordGovernanceAutoProjection("manual_override")
			continue
		}

		level := targetLevel
		if level == "" {
			level = model.GovernanceLevelInternal
		}
		status := targetStatus
		if status == "" {
			status = model.GovernanceStatusPublished
		}
		if _, err := s.SetDocumentGovernance(ctx, tenantID, userID, datasetID, doc.ID, DocumentGovernanceInput{
			Level:     level,
			Status:    status,
			Authority: &authorityAutoProjectionDefault,
			Source:    model.GovernanceSourceAuto,
			Reason:    "auto-projection schema match",
		}); err != nil {
			return nil, err
		}
		result.Written++
		obs.Get().RecordGovernanceAutoProjection("matched")
	}

	// 回填 RAGFlow 元数据（降级计数 = 同步失败）。
	if result.Written > 0 {
		if err := s.SyncDocumentGovernanceMetadata(ctx, tenantID, userID, datasetID); err != nil {
			result.SyncFailed = result.Written
			obs.Get().RecordGovernanceAutoProjection("degraded")
		} else {
			for i := 0; i < result.Written; i++ {
				obs.Get().RecordGovernanceAutoProjection("synced")
			}
		}
	}
	return result, nil
}

// governanceQualityByDocument 拉取文档级有效抽取质量（EffectiveScore）。无报告
// 的文档不出现在 map 中，调用方按无证据放行。
func (s *Service) governanceQualityByDocument(ctx context.Context, tenantID string, documentIDs []string) (map[string]float64, error) {
	if len(documentIDs) == 0 {
		return map[string]float64{}, nil
	}
	reports, _, err := s.Store.ListLatestParseQualityReports(ctx, tenantID, documentIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(reports))
	for _, r := range reports {
		out[r.DocumentID] = r.EffectiveScore
	}
	return out, nil
}

// governanceMatchTargets 匹配全部 schema，返回可投影的 level/status 目标
// （同轴取 Rank 最高的字典条目）与 dormant 命中数。
func (s *Service) governanceMatchTargets(
	datasetID string, active []*model.GovernanceSchema, compiled map[string]*regexp.Regexp, doc ragflow.Document,
) (level string, status string, dormant int) {
	text := doc.Name
	meta, _ := json.Marshal(doc.Metadata)
	if len(meta) > 0 {
		text = text + " " + string(meta)
	}
	text = strings.ToLower(text)

	levelRank := -1
	statusRank := -1
	for _, sc := range active {
		if !sc.AppliesTo(datasetID, doc.ID) {
			continue
		}
		if !matchGovernanceSchema(sc, compiled, text) {
			continue
		}
		switch sc.Category {
		case model.DictCategoryLevel:
			if autoProjectableLevel[sc.TargetKey] {
				r := model.GovernanceLevelRank(sc.TargetKey)
				if r > levelRank {
					levelRank = r
					level = sc.TargetKey
				}
			} else {
				dormant++
			}
		case model.DictCategoryStatus:
			if autoProjectableStatus[sc.TargetKey] {
				r := statusRankOf(sc.TargetKey)
				if r > statusRank {
					statusRank = r
					status = sc.TargetKey
				}
			} else {
				dormant++
			}
		}
	}
	return level, status, dormant
}

func statusRankOf(status string) int {
	if status == model.GovernanceStatusPublished {
		return 2
	}
	return 1
}

func matchGovernanceSchema(sc *model.GovernanceSchema, compiled map[string]*regexp.Regexp, text string) bool {
	if sc.MatcherType == model.SchemaMatcherRegex {
		r := compiled[sc.ID]
		if r == nil {
			return false
		}
		return r.MatchString(text)
	}
	return strings.Contains(text, strings.ToLower(sc.Pattern))
}
