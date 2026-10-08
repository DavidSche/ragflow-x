package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type KnowledgeStrategyInput struct {
	DatasetID          string `json:"dataset_id" binding:"required"`
	StrategyType       string `json:"strategy_type" binding:"required"`
	RAGFlowCapability  string `json:"ragflow_capability"`
	Config             string `json:"config"`
	FallbackStrategy   string `json:"fallback_strategy"`
	AuthorizationScope string `json:"authorization_scope" binding:"required"`
	Active             *bool  `json:"active"`
}

type KnowledgeStrategyRetrieveInput struct {
	Question        string                                 `json:"question" binding:"required"`
	DocumentIDs     []string                               `json:"document_ids"`
	Page            *int                                   `json:"page"`
	PageSize        *int                                   `json:"page_size"`
	VersionResolver *KnowledgeStrategyVersionResolverInput `json:"version_resolver,omitempty"`
}

type KnowledgeStrategyVersionResolverInput struct {
	Mode              string     `json:"mode" binding:"required"`
	LogicalDocumentID string     `json:"logical_document_id" binding:"required"`
	Version           *int64     `json:"version"`
	AsOf              *time.Time `json:"as_of"`
}

type KnowledgeStrategyVersionResolution struct {
	Mode              string `json:"mode"`
	LogicalDocumentID string `json:"logical_document_id"`
	VersionID         string `json:"version_id"`
	Version           int64  `json:"version"`
	PushdownUsed      bool   `json:"pushdown_used"`
	PostFiltered      bool   `json:"post_filtered"`
}

const (
	KnowledgeStrategyVersionCurrent = "current"
	KnowledgeStrategyVersionAsOf    = "as_of"
	KnowledgeStrategyVersionExact   = "version"

	metadataKeyLogicalDocumentID = "rgx_logical_doc_id"
	metadataKeyVersion           = "rgx_version"
)

type KnowledgeStrategyExecution struct {
	StrategyID            string                              `json:"strategy_id"`
	RequestedStrategyType string                              `json:"requested_strategy_type"`
	UsedStrategyType      string                              `json:"used_strategy_type"`
	FallbackUsed          bool                                `json:"fallback_used"`
	MetadataPushdown      bool                                `json:"metadata_pushdown"`
	Chunks                []map[string]interface{}            `json:"chunks"`
	Total                 int64                               `json:"total"`
	VersionResolver       *KnowledgeStrategyVersionResolution `json:"version_resolver,omitempty"`
}

var knowledgeStrategyTypes = map[string]struct{}{
	model.KnowledgeStrategySemantic:   {},
	model.KnowledgeStrategyLexical:    {},
	model.KnowledgeStrategyHybrid:     {},
	model.KnowledgeStrategyCompiled:   {},
	model.KnowledgeStrategyStructured: {},
}

var knowledgeStrategyCapabilities = map[string]struct{}{
	model.RAGFlowCapabilityRetrieval:            {},
	model.RAGFlowCapabilityKnowledgeCompilation: {},
	model.RAGFlowCapabilityMetadataCondition:    {},
}

var knowledgeStrategyConfigKeys = map[string]struct{}{
	"compilation_template": {},
	"dataset_id":           {},
	"document_ids":         {},
	"probe_key":            {},
	"thresholds":           {},
	"version":              {},
	"structured_filters":   {},
	"keyword":              {},
	"rerank_id":            {},
}

type knowledgeStrategyConfigValues struct {
	CompilationTemplate string
	ProbeDatasetID      string
	ProbeKey            string
	LatencyThresholdMs  int64
	DocumentIDs         []string
	StructuredFilters   []ragflow.MetadataConditionOp
	Keyword             bool
	RerankID            string
}

func (s *Service) CreateKnowledgeStrategy(
	ctx context.Context, tenantID, userID string, input KnowledgeStrategyInput,
) (*model.KnowledgeStrategy, error) {
	strategy, err := buildKnowledgeStrategy(tenantID, userID, input, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.validateKnowledgeStrategyDataset(ctx, strategy); err != nil {
		return nil, err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, strategy.ProjectID); err != nil {
		return nil, err
	}
	if err := s.validateKnowledgeStrategyCompilationTemplate(ctx, strategy); err != nil {
		return nil, err
	}
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateKnowledgeStrategy(ctx, strategy); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForKnowledgeStrategy(strategy, "knowledge_strategy.created", userID, strategy.CreatedAt))
	}); err != nil {
		return nil, err
	}
	return strategy, nil
}

func (s *Service) ListKnowledgeStrategies(
	ctx context.Context, tenantID, actorID string, filter repository.KnowledgeStrategyFilter, page, pageSize int,
) ([]model.KnowledgeStrategy, int64, error) {
	filter.DatasetID = strings.TrimSpace(filter.DatasetID)
	filter.ProjectID = strings.TrimSpace(filter.ProjectID)
	filter.StrategyType = strings.TrimSpace(filter.StrategyType)
	if filter.ProjectID != "" {
		if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, filter.ProjectID); err != nil {
			return nil, 0, err
		}
		return s.Store.ListKnowledgeStrategies(ctx, tenantID, filter, page, pageSize)
	}
	projectIDs, scoped, err := s.teamAdminOwnedProjectIDs(ctx, tenantID, actorID)
	if err != nil {
		return nil, 0, err
	}
	if scoped {
		if len(projectIDs) == 0 {
			return []model.KnowledgeStrategy{}, 0, nil
		}
		filter.ProjectIDs = projectIDs
	}
	return s.Store.ListKnowledgeStrategies(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) GetKnowledgeStrategy(ctx context.Context, tenantID, strategyID string) (*model.KnowledgeStrategy, error) {
	strategy, err := s.Store.GetKnowledgeStrategy(ctx, tenantID, strategyID)
	if err != nil {
		return nil, err
	}
	if strategy == nil || strategy.ID == "" {
		return nil, httperr.NotFound("knowledge strategy not found")
	}
	return strategy, nil
}

func (s *Service) UpdateKnowledgeStrategy(
	ctx context.Context, tenantID, userID, strategyID string, input KnowledgeStrategyInput,
) (*model.KnowledgeStrategy, error) {
	existing, err := s.GetKnowledgeStrategy(ctx, tenantID, strategyID)
	if err != nil {
		return nil, err
	}
	strategy, err := buildKnowledgeStrategy(tenantID, userID, input, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := s.validateKnowledgeStrategyDataset(ctx, strategy); err != nil {
		return nil, err
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, userID, strategy.ProjectID); err != nil {
		return nil, err
	}
	if err := s.validateKnowledgeStrategyCompilationTemplate(ctx, strategy); err != nil {
		return nil, err
	}
	strategy.ID = existing.ID
	strategy.CreatedBy = existing.CreatedBy
	strategy.CreatedAt = existing.CreatedAt
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForKnowledgeStrategy(strategy, "knowledge_strategy.updated", userID, strategy.UpdatedAt))
	}); err != nil {
		return nil, err
	}
	return strategy, nil
}

func (s *Service) enforceKnowledgeStrategyTeamAdminProject(
	ctx context.Context, tenantID, actorID, projectID string,
) error {
	projectIDs, scoped, err := s.teamAdminOwnedProjectIDs(ctx, tenantID, actorID)
	if err != nil {
		return err
	}
	if scoped && !containsString(projectIDs, projectID) {
		return ErrForbidden
	}
	return nil
}

func (s *Service) teamAdminOwnedProjectIDs(
	ctx context.Context, tenantID, actorID string,
) ([]string, bool, error) {
	roles, err := s.Store.ListRolesByUser(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	if !hasRoleID(roles, model.RoleTeamAdmin) {
		return nil, false, nil
	}
	user, err := s.Store.GetUser(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	if user == nil || user.Status != model.UserStatusActive || user.TenantID != tenantID ||
		user.Role == model.RolePlatformAdmin || user.Role == model.RoleTenantAdmin {
		return nil, false, ErrForbidden
	}
	teamIDs, err := s.Store.ListTeamIDsOwnedBy(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	tenantTeamIDs := make([]string, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		team, err := s.Store.GetTeam(ctx, tenantID, teamID)
		if err != nil {
			return nil, false, err
		}
		if team != nil {
			tenantTeamIDs = append(tenantTeamIDs, team.ID)
		}
	}
	if len(tenantTeamIDs) == 0 {
		return []string{}, true, nil
	}
	projectIDs, err := s.Store.ListProjectIDsBoundToTeams(ctx, tenantTeamIDs)
	if err != nil {
		return nil, false, err
	}
	return projectIDs, true, nil
}

func hasRoleID(roles []model.Role, roleID string) bool {
	for _, role := range roles {
		if role.ID == roleID {
			return true
		}
	}
	return false
}

func (s *Service) DeleteKnowledgeStrategy(ctx context.Context, tenantID, userID, strategyID string) error {
	strategy, err := s.GetKnowledgeStrategy(ctx, tenantID, strategyID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.DeleteKnowledgeStrategy(ctx, tenantID, strategy.ID); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForKnowledgeStrategy(strategy, "knowledge_strategy.deleted", userID, now))
	})
}

func (s *Service) ProbeKnowledgeStrategy(
	ctx context.Context, tenantID, userID, strategyID string,
) (*model.KnowledgeCapabilityProbe, error) {
	strategy, err := s.GetKnowledgeStrategy(ctx, tenantID, strategyID)
	if err != nil {
		return nil, err
	}
	config, err := parseKnowledgeStrategyConfig(strategy.Config)
	if err != nil {
		return nil, err
	}
	probeDataset, err := s.Store.GetDatasetLink(ctx, tenantID, config.ProbeDatasetID)
	if err != nil {
		return nil, err
	}
	if probeDataset == nil || probeDataset.ID == "" {
		return nil, httperr.BadRequest(40120, "config dataset_id must reference a tenant probe dataset")
	}
	if probeDataset.ID == strategy.DatasetID {
		return nil, httperr.BadRequest(40120, "probe dataset must be dedicated and differ from the strategy dataset")
	}
	if strategy.StrategyType == model.KnowledgeStrategyCompiled && config.CompilationTemplate == "" {
		return nil, httperr.BadRequest(40120, "compiled strategy probe requires compilation_template")
	}

	threshold := config.LatencyThresholdMs
	if threshold <= 0 {
		threshold = 3000
	}
	request := ragflow.RetrieveDatasetsRequest{
		DatasetIDs:                  []string{probeDataset.RAGFlowDatasetID},
		DocumentIDs:                 config.DocumentIDs,
		Question:                    "rgx-knowledge-strategy-probe-v1",
		IncludeKnowledgeCompilation: strategy.StrategyType == model.KnowledgeStrategyCompiled,
		Page:                        1, PageSize: 1,
	}
	request.MetadataCondition = structuredFiltersCondition(config.StructuredFilters)
	request.Keyword = config.Keyword
	request.RerankID = config.RerankID
	startedAt := time.Now()
	result, retrievalErr := s.RAGFlow.RetrieveDatasets(ctx, request)
	latencyMs := time.Since(startedAt).Milliseconds()
	version, versionErr := s.RAGFlow.EngineVersion(ctx)
	history, err := s.Store.ListKnowledgeCapabilityProbes(ctx, tenantID, strategy.ID, 20)
	if err != nil {
		return nil, err
	}
	historyLatencies := make([]int64, 0, len(history)+1)
	for _, probe := range history {
		historyLatencies = append(historyLatencies, probe.LatencyMs)
	}
	historyLatencies = append(historyLatencies, latencyMs)
	rollingP95 := knowledgeProbeP95(historyLatencies)
	checks := knowledgeProbeChecks{
		RequestAccepted:     retrievalErr == nil && result != nil,
		StrategySelected:    false,
		ArtifactPresent:     false,
		LatencyWithinLimit:  retrievalErr == nil && result != nil && rollingP95 < threshold,
		EngineVersionValid:  versionErr == nil && strings.TrimSpace(version) != "",
		LatencyMs:           latencyMs,
		LatencyThresholdMs:  threshold,
		RollingP95Ms:        rollingP95,
		RollingWindowCount:  len(historyLatencies),
		ProbeDatasetID:      probeDataset.ID,
		CompilationTemplate: config.CompilationTemplate,
	}
	if result != nil {
		for _, chunk := range result.Chunks {
			datasetID, _ := chunk["dataset_id"].(string)
			if datasetID != probeDataset.RAGFlowDatasetID {
				continue
			}
			if strategy.StrategyType == model.KnowledgeStrategyCompiled {
				compileKwd := valueString(chunk["compile_kwd"])
				checks.ArtifactPresent = strings.TrimSpace(compileKwd) != ""
				checks.StrategySelected = compileKwd == config.CompilationTemplate
			} else {
				checks.ArtifactPresent = true
				checks.StrategySelected = true
			}
			break
		}
	}
	checks.ArtifactVerified = strategy.StrategyType != model.KnowledgeStrategyCompiled
	if strategy.StrategyType == model.KnowledgeStrategyCompiled && checks.StrategySelected {
		artifacts, artifactTotal, artifactErr := s.RAGFlow.ListDatasetArtifacts(
			ctx, probeDataset.RAGFlowDatasetID,
			ragflow.DatasetArtifactFilter{Page: 1, PageSize: 100},
		)
		artifactCount := artifactTotal
		if artifactErr == nil && int64(len(artifacts)) > artifactCount {
			artifactCount = int64(len(artifacts))
		}
		checks.ArtifactCount = artifactCount
		checks.ArtifactVerified = artifactErr == nil && artifactCount > 0
	}
	status := model.KnowledgeProbeStatusPassed
	if !checks.Passed() {
		status = model.KnowledgeProbeStatusFailed
	}
	now := time.Now().UTC()
	detail, err := json.Marshal(map[string]interface{}{
		"checks": checks, "probe_request": "retrieve_datasets", "rolling_p95_ms": rollingP95,
		"artifact_verified": checks.ArtifactVerified,
	})
	if err != nil {
		return nil, err
	}
	probe := &model.KnowledgeCapabilityProbe{
		ID: id.New(), TenantID: tenantID, StrategyID: strategy.ID, Status: status,
		LatencyMs: latencyMs, ProbeKey: config.ProbeKey, RAGFlowVersion: version,
		Detail: string(detail), CheckedAt: now,
	}
	err = s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateKnowledgeCapabilityProbe(ctx, probe); err != nil {
			return err
		}
		recent, err := tx.ListKnowledgeCapabilityProbes(ctx, tenantID, strategy.ID, 3)
		if err != nil {
			return err
		}
		strategy.ProbeStatus = knowledgeProbeAggregateStatus(status, recent)
		strategy.LastProbeAt = &now
		if err := tx.UpdateKnowledgeStrategy(ctx, strategy); err != nil {
			return err
		}
		return tx.CreateAudit(ctx, auditForKnowledgeCapabilityProbe(strategy, probe, userID, now))
	})
	if err != nil {
		return nil, err
	}
	return probe, nil
}

func (s *Service) ExecuteKnowledgeStrategy(
	ctx context.Context, tenantID, userID, strategyID string, input KnowledgeStrategyRetrieveInput,
) (*KnowledgeStrategyExecution, error) {
	strategy, err := s.GetKnowledgeStrategy(ctx, tenantID, strategyID)
	if err != nil {
		return nil, err
	}
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return nil, httperr.BadRequest(40120, "question is required")
	}
	documentIDs, err := normalizeKnowledgeDocumentIDs(input.DocumentIDs)
	if err != nil {
		return nil, err
	}
	resolvedVersion, versionResolution, err := s.resolveKnowledgeStrategyVersion(
		ctx, tenantID, strategy.DatasetID, input.VersionResolver,
	)
	if err != nil {
		return nil, err
	}
	page, pageSize := 1, 10
	if input.Page != nil {
		page = *input.Page
	}
	if input.PageSize != nil {
		pageSize = *input.PageSize
	}
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, strategy.DatasetID)
	if err != nil {
		return nil, err
	}
	if dataset == nil || dataset.ID == "" {
		return nil, httperr.BadRequest(40120, "strategy dataset is unavailable")
	}
	config, err := parseKnowledgeStrategyConfig(strategy.Config)
	if err != nil {
		return nil, err
	}
	condition, err := s.buildPushdownCondition(ctx, tenantID, []string{dataset.ID})
	if err != nil {
		return nil, err
	}
	versionPushdownUsed := false
	if resolvedVersion != nil {
		documentIDs, err = mergeKnowledgeVersionDocumentIDs(documentIDs, resolvedVersion)
		if err != nil {
			return nil, err
		}
		condition, versionPushdownUsed, err = s.mergeKnowledgeVersionPushdownCondition(
			ctx, tenantID, condition, resolvedVersion, dataset.RAGFlowDatasetID, question, documentIDs,
			strategy.StrategyType == model.KnowledgeStrategyCompiled,
		)
		if err != nil {
			return nil, err
		}
		versionResolution.PushdownUsed = versionPushdownUsed
		versionResolution.PostFiltered = true
	}
	condition, err = mergeKnowledgeStructuredCondition(condition, config.StructuredFilters)
	if err != nil {
		return nil, err
	}
	primaryRequest := s.buildKnowledgeStrategyRetrieveRequest(
		dataset.RAGFlowDatasetID, question, documentIDs, condition,
		strategy.StrategyType, page, pageSize,
		config,
	)
	primaryEligible := strategy.Active && strategy.ProbeStatus == model.KnowledgeProbeStatusPassed
	result, retrievalErr := (*ragflow.RetrieveDatasetsResult)(nil), error(nil)
	var usedStrategyType string
	fallbackUsed := false
	if primaryEligible {
		result, retrievalErr = s.RAGFlow.RetrieveDatasets(ctx, primaryRequest)
		usedStrategyType = strategy.StrategyType
		if retrievalErr != nil || !knowledgeResultUsesCompilation(strategy, result, config.CompilationTemplate) {
			fallbackUsed = true
		}
	} else {
		fallbackUsed = true
	}
	if fallbackUsed {
		usedStrategyType = strategy.FallbackStrategy
		request := s.buildKnowledgeStrategyRetrieveRequest(
			dataset.RAGFlowDatasetID, question, documentIDs, condition,
			usedStrategyType, page, pageSize,
			config,
		)
		result, retrievalErr = s.RAGFlow.RetrieveDatasets(ctx, request)
	}
	if retrievalErr != nil {
		return nil, retrievalErr
	}
	if result == nil {
		result = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{}}
	}
	if result.Chunks == nil {
		result.Chunks = []map[string]interface{}{}
	}
	if resolvedVersion != nil {
		result.Chunks = filterKnowledgeChunksForVersion(result.Chunks, resolvedVersion)
	}
	execution := &KnowledgeStrategyExecution{
		StrategyID: strategy.ID, RequestedStrategyType: strategy.StrategyType,
		UsedStrategyType: usedStrategyType, FallbackUsed: fallbackUsed,
		MetadataPushdown: condition != nil && !condition.Empty(),
		Chunks:           result.Chunks, Total: result.Total,
		VersionResolver: versionResolution,
	}
	now := time.Now().UTC()
	if err := s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		return tx.CreateAudit(ctx, auditForKnowledgeStrategyExecution(strategy, execution, userID, now))
	}); err != nil {
		return nil, err
	}
	return execution, nil
}

func buildKnowledgeStrategy(
	tenantID, userID string, input KnowledgeStrategyInput, now time.Time,
) (*model.KnowledgeStrategy, error) {
	datasetID := strings.TrimSpace(input.DatasetID)
	strategyType := strings.TrimSpace(input.StrategyType)
	capability := strings.TrimSpace(input.RAGFlowCapability)
	fallback := strings.TrimSpace(input.FallbackStrategy)
	if datasetID == "" || len(datasetID) > 32 {
		return nil, httperr.BadRequest(40120, "dataset_id is required")
	}
	if _, ok := knowledgeStrategyTypes[strategyType]; !ok {
		return nil, httperr.BadRequest(40136, "strategy_type must be semantic, lexical, hybrid, compiled or structured")
	}
	if capability == "" {
		capability = model.RAGFlowCapabilityRetrieval
		if strategyType == model.KnowledgeStrategyCompiled {
			capability = model.RAGFlowCapabilityKnowledgeCompilation
		}
		if strategyType == model.KnowledgeStrategyStructured {
			capability = model.RAGFlowCapabilityMetadataCondition
		}
	}
	if _, ok := knowledgeStrategyCapabilities[capability]; !ok {
		return nil, httperr.BadRequest(40136, "ragflow_capability is unsupported")
	}
	if strategyType == model.KnowledgeStrategyCompiled &&
		capability != model.RAGFlowCapabilityKnowledgeCompilation {
		return nil, httperr.BadRequest(40136, "compiled strategy must use knowledge_compilation capability")
	}
	if strategyType == model.KnowledgeStrategyStructured &&
		capability != model.RAGFlowCapabilityMetadataCondition {
		return nil, httperr.BadRequest(40136, "structured strategy must use metadata_condition capability")
	}
	if fallback == "" {
		fallback = model.KnowledgeStrategyHybrid
	}
	if _, ok := knowledgeStrategyTypes[fallback]; !ok || fallback == strategyType {
		return nil, httperr.BadRequest(40136, "fallback_strategy must differ from strategy_type")
	}
	config, err := validateKnowledgeStrategyConfig(input.Config, strategyType)
	if err != nil {
		return nil, err
	}
	authorizationScope, err := validateAuthorizationScope(input.AuthorizationScope, "authorization_scope")
	if err != nil {
		return nil, err
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	return &model.KnowledgeStrategy{
		ID: id.New(), TenantID: tenantID, DatasetID: datasetID,
		StrategyType: strategyType, RAGFlowCapability: capability, Config: config,
		FallbackStrategy: fallback, AuthorizationScope: authorizationScope,
		ProbeStatus: model.KnowledgeProbeStatusUnknown, Active: active,
		CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func normalizeKnowledgeDocumentIDs(documentIDs []string) ([]string, error) {
	if len(documentIDs) > 20 {
		return nil, httperr.BadRequest(40120, "document_ids must contain at most 20 values")
	}
	for _, documentID := range documentIDs {
		if strings.TrimSpace(documentID) == "" || len(documentID) > 64 {
			return nil, httperr.BadRequest(40120, "document_ids values must contain 1 to 64 characters")
		}
	}
	if len(documentIDs) == 0 {
		return nil, nil
	}
	return append([]string(nil), documentIDs...), nil
}

func (s *Service) buildKnowledgeStrategyRetrieveRequest(
	datasetID, question string, documentIDs []string, condition *ragflow.MetadataCondition,
	strategyType string, page, pageSize int, config knowledgeStrategyConfigValues,
) ragflow.RetrieveDatasetsRequest {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	var vectorSimilarityWeight *float64
	switch strategyType {
	case model.KnowledgeStrategySemantic:
		weight := 1.0
		vectorSimilarityWeight = &weight
	case model.KnowledgeStrategyHybrid:
		weight := 0.5
		vectorSimilarityWeight = &weight
	case model.KnowledgeStrategyLexical, model.KnowledgeStrategyStructured:
		weight := 0.0
		vectorSimilarityWeight = &weight
	}
	return ragflow.RetrieveDatasetsRequest{
		DatasetIDs: []string{datasetID}, Question: question, DocumentIDs: documentIDs,
		MetadataCondition: condition, IncludeKnowledgeCompilation: strategyType == model.KnowledgeStrategyCompiled,
		Page: page, PageSize: pageSize, VectorSimilarityWeight: vectorSimilarityWeight,
		Keyword: config.Keyword, RerankID: config.RerankID,
	}
}

func (s *Service) resolveKnowledgeStrategyVersion(
	ctx context.Context, tenantID, datasetID string, resolver *KnowledgeStrategyVersionResolverInput,
) (*model.DocumentVersion, *KnowledgeStrategyVersionResolution, error) {
	if resolver == nil {
		return nil, nil, nil
	}
	mode := strings.TrimSpace(resolver.Mode)
	if mode != KnowledgeStrategyVersionCurrent && mode != KnowledgeStrategyVersionAsOf && mode != KnowledgeStrategyVersionExact {
		return nil, nil, httperr.BadRequest(40120, "version_resolver.mode must be current, as_of or version")
	}
	logicalDocumentID := strings.TrimSpace(resolver.LogicalDocumentID)
	if logicalDocumentID == "" || len(logicalDocumentID) > 32 {
		return nil, nil, httperr.BadRequest(40120, "version_resolver.logical_document_id is invalid")
	}
	if mode == KnowledgeStrategyVersionCurrent && (resolver.Version != nil || resolver.AsOf != nil) {
		return nil, nil, httperr.BadRequest(40120, "current version resolver must not specify version or as_of")
	}
	if mode == KnowledgeStrategyVersionAsOf && (resolver.AsOf == nil || resolver.Version != nil) {
		return nil, nil, httperr.BadRequest(40120, "as_of version resolver requires as_of only")
	}
	if mode == KnowledgeStrategyVersionExact && (resolver.Version == nil || *resolver.Version <= 0 || resolver.AsOf != nil) {
		return nil, nil, httperr.BadRequest(40120, "version resolver requires a positive version only")
	}

	document, err := s.Store.GetLogicalDocument(ctx, tenantID, logicalDocumentID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve logical document: %w", err)
	}
	if document == nil || document.ID == "" {
		return nil, nil, httperr.New(404, 404, "document version not found")
	}
	if document.DatasetID != datasetID {
		return nil, nil, httperr.New(404, 404, "document version not found")
	}
	versions, err := s.Store.ListGovernanceDocumentVersions(ctx, tenantID, document.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve document versions: %w", err)
	}
	var version *model.DocumentVersion
	switch mode {
	case KnowledgeStrategyVersionCurrent:
		version, err = s.Store.GetActiveDocumentVersion(ctx, tenantID, document.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve active version: %w", err)
		}
	case KnowledgeStrategyVersionExact:
		for index := range versions {
			if versions[index].Version == *resolver.Version {
				version = &versions[index]
				break
			}
		}
	case KnowledgeStrategyVersionAsOf:
		for index := range versions {
			candidate := &versions[index]
			if candidate.EffectiveFrom.After(*resolver.AsOf) {
				continue
			}
			if candidate.EffectiveTo != nil && !candidate.EffectiveTo.After(*resolver.AsOf) {
				continue
			}
			version = candidate
			break
		}
	}
	if version == nil || version.RAGFlowDocumentID == "" {
		return nil, nil, httperr.New(404, 404, "document version not found")
	}
	resolution := &KnowledgeStrategyVersionResolution{
		Mode: mode, LogicalDocumentID: document.ID,
		VersionID: version.ID, Version: version.Version,
	}
	return version, resolution, nil
}

func mergeKnowledgeVersionDocumentIDs(documentIDs []string, version *model.DocumentVersion) ([]string, error) {
	if len(documentIDs) == 0 {
		return []string{version.RAGFlowDocumentID}, nil
	}
	for _, documentID := range documentIDs {
		if documentID != version.RAGFlowDocumentID {
			return nil, httperr.BadRequest(40120, "document_ids conflict with the resolved document version")
		}
	}
	return []string{version.RAGFlowDocumentID}, nil
}

func (s *Service) mergeKnowledgeVersionPushdownCondition(
	ctx context.Context, tenantID string, base *ragflow.MetadataCondition,
	version *model.DocumentVersion, datasetID, question string, documentIDs []string, includeCompilation bool,
) (*ragflow.MetadataCondition, bool, error) {
	policy, err := s.Store.GetVersionFilterPolicy(ctx, tenantID)
	if err != nil {
		return nil, false, fmt.Errorf("version filter policy: %w", err)
	}
	maxPushdownIDs, maxRequestBytes := 200, 32768
	if policy != nil && policy.MaxPushdownIDs > 0 {
		maxPushdownIDs = policy.MaxPushdownIDs
	}
	if policy != nil && policy.MaxRequestBytes > 0 {
		maxRequestBytes = policy.MaxRequestBytes
	}
	pushdownCondition := cloneKnowledgeMetadataCondition(base)
	if pushdownCondition == nil {
		pushdownCondition = &ragflow.MetadataCondition{Logic: "and"}
	}
	if pushdownCondition.Logic == "" {
		pushdownCondition.Logic = "and"
	}
	if pushdownCondition.Logic != "and" {
		return base, false, nil
	}
	pushdownCondition.Conditions = append(pushdownCondition.Conditions,
		ragflow.MetadataConditionOp{
			Name: metadataKeyLogicalDocumentID, ComparisonOperator: operatorEqual, Value: version.LogicalDocumentID,
		},
		ragflow.MetadataConditionOp{
			Name: metadataKeyVersion, ComparisonOperator: operatorEqual, Value: strconv.FormatInt(version.Version, 10),
		},
	)
	request := buildKnowledgeStrategyRetrieveRequestForEstimate(
		datasetID, question, documentIDs, pushdownCondition, includeCompilation,
	)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, false, fmt.Errorf("estimate version pushdown: %w", err)
	}
	if len(pushdownCondition.Conditions) > maxPushdownIDs || len(payload) > maxRequestBytes {
		return base, false, nil
	}
	return pushdownCondition, true, nil
}

func buildKnowledgeStrategyRetrieveRequestForEstimate(
	datasetID, question string, documentIDs []string, condition *ragflow.MetadataCondition,
	includeCompilation bool,
) ragflow.RetrieveDatasetsRequest {
	return ragflow.RetrieveDatasetsRequest{
		DatasetIDs: []string{datasetID}, Question: question, DocumentIDs: documentIDs,
		MetadataCondition: condition, IncludeKnowledgeCompilation: includeCompilation,
		Page: 1, PageSize: 10,
	}
}

func cloneKnowledgeMetadataCondition(condition *ragflow.MetadataCondition) *ragflow.MetadataCondition {
	if condition == nil {
		return nil
	}
	cloned := &ragflow.MetadataCondition{Logic: condition.Logic}
	if condition.Conditions != nil {
		cloned.Conditions = append([]ragflow.MetadataConditionOp(nil), condition.Conditions...)
	}
	return cloned
}

func filterKnowledgeChunksForVersion(
	chunks []map[string]interface{}, version *model.DocumentVersion,
) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(chunks))
	for _, chunk := range chunks {
		if documentID := valueString(chunk["document_id"]); documentID == version.RAGFlowDocumentID {
			filtered = append(filtered, chunk)
		}
	}
	return filtered
}

func knowledgeResultUsesCompilation(
	strategy *model.KnowledgeStrategy, result *ragflow.RetrieveDatasetsResult, compilationTemplate string,
) bool {
	if strategy.StrategyType != model.KnowledgeStrategyCompiled || result == nil {
		return true
	}
	for _, chunk := range result.Chunks {
		compileKwd, _ := chunk["compile_kwd"].(string)
		if strings.TrimSpace(compileKwd) != "" && compileKwd == compilationTemplate {
			return true
		}
	}
	return false
}

func auditForKnowledgeStrategyExecution(
	strategy *model.KnowledgeStrategy, execution *KnowledgeStrategyExecution, userID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: strategy.TenantID, ActorTenantID: strategy.TenantID,
		TargetTenantID: strategy.TenantID, UserID: userID, Action: "knowledge_strategy.executed",
		Resource: "knowledge-strategy", ResourceID: strategy.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"strategy_id": strategy.ID, "requested_strategy_type": execution.RequestedStrategyType,
			"used_strategy_type": execution.UsedStrategyType, "fallback_used": execution.FallbackUsed,
			"metadata_pushdown": execution.MetadataPushdown, "chunk_count": len(execution.Chunks),
			"version_pushdown":    execution.VersionResolver != nil && execution.VersionResolver.PushdownUsed,
			"version_post_filter": execution.VersionResolver != nil && execution.VersionResolver.PostFiltered,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

func validateKnowledgeStrategyConfig(raw string, strategyType string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if len(raw) > 16384 {
		return "", httperr.BadRequest(40120, "config must be at most 16KB")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil || values == nil {
		return "", httperr.BadRequest(40120, "config must be a JSON object")
	}
	for key := range values {
		if _, allowed := knowledgeStrategyConfigKeys[key]; !allowed {
			return "", httperr.BadRequest(40120, "config contains an unsupported key")
		}
	}
	config, err := parseKnowledgeStrategyConfig(raw)
	if err != nil {
		return "", err
	}
	if strategyType == model.KnowledgeStrategyStructured && len(config.StructuredFilters) == 0 {
		return "", httperr.BadRequest(40120, "structured strategy requires structured_filters")
	}
	if strategyType != model.KnowledgeStrategyStructured && len(config.StructuredFilters) > 0 {
		return "", httperr.BadRequest(40120, "structured_filters are only supported by structured strategies")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", httperr.BadRequest(40120, "config must be a JSON object")
	}
	return string(encoded), nil
}

func parseKnowledgeStrategyConfig(raw string) (knowledgeStrategyConfigValues, error) {
	config := knowledgeStrategyConfigValues{ProbeKey: "knowledge-strategy-probe", LatencyThresholdMs: 3000}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil || values == nil {
		return config, httperr.BadRequest(40120, "config must be a JSON object")
	}
	if rawValue, ok := values["dataset_id"]; ok {
		value, err := decodeKnowledgeConfigString(rawValue, "dataset_id")
		if err != nil {
			return config, err
		}
		config.ProbeDatasetID = value
	}
	if rawValue, ok := values["compilation_template"]; ok {
		value, err := decodeKnowledgeConfigString(rawValue, "compilation_template")
		if err != nil {
			return config, err
		}
		config.CompilationTemplate = value
	}
	if rawValue, ok := values["probe_key"]; ok {
		value, err := decodeKnowledgeConfigString(rawValue, "probe_key")
		if err != nil {
			return config, err
		}
		if len(value) > 64 {
			return config, httperr.BadRequest(40120, "probe_key must contain at most 64 characters")
		}
		config.ProbeKey = value
	}
	if rawValue, ok := values["document_ids"]; ok {
		var documentIDs []string
		if err := json.Unmarshal(rawValue, &documentIDs); err != nil {
			return config, httperr.BadRequest(40120, "document_ids must be an array of strings")
		}
		if len(documentIDs) > 20 {
			return config, httperr.BadRequest(40120, "document_ids must contain at most 20 values")
		}
		for _, documentID := range documentIDs {
			if strings.TrimSpace(documentID) == "" || len(documentID) > 64 {
				return config, httperr.BadRequest(40120, "document_ids values must contain 1 to 64 characters")
			}
		}
		config.DocumentIDs = documentIDs
	}
	if rawValue, ok := values["thresholds"]; ok {
		var thresholds map[string]float64
		if err := json.Unmarshal(rawValue, &thresholds); err != nil {
			return config, httperr.BadRequest(40120, "thresholds must be an object with numeric values")
		}
		for key, value := range thresholds {
			if key != "latency_ms" {
				return config, httperr.BadRequest(40120, "thresholds contains an unsupported key")
			}
			if value <= 0 || value > 60000 {
				return config, httperr.BadRequest(40120, "thresholds.latency_ms must be between 1 and 60000")
			}
			config.LatencyThresholdMs = int64(value)
		}
	}
	if rawValue, ok := values["structured_filters"]; ok {
		var filters []ragflow.MetadataConditionOp
		decoder := json.NewDecoder(bytes.NewReader(rawValue))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&filters); err != nil {
			return config, httperr.BadRequest(40120, "structured_filters must be an array of condition objects")
		}
		if len(filters) == 0 || len(filters) > 20 {
			return config, httperr.BadRequest(40120, "structured_filters must contain 1 to 20 conditions")
		}
		for index := range filters {
			if err := validateKnowledgeStructuredFilter(&filters[index]); err != nil {
				return config, err
			}
		}
		config.StructuredFilters = append([]ragflow.MetadataConditionOp(nil), filters...)
	}
	if rawValue, ok := values["keyword"]; ok {
		var keyword bool
		if err := json.Unmarshal(rawValue, &keyword); err != nil {
			return config, httperr.BadRequest(40120, "keyword must be a boolean")
		}
		config.Keyword = keyword
	}
	if rawValue, ok := values["rerank_id"]; ok {
		value, err := decodeKnowledgeConfigString(rawValue, "rerank_id")
		if err != nil {
			return config, err
		}
		config.RerankID = value
	}
	return config, nil
}

func validateKnowledgeStructuredFilter(filter *ragflow.MetadataConditionOp) error {
	filter.Name = strings.TrimSpace(filter.Name)
	filter.Value = strings.TrimSpace(filter.Value)
	switch filter.ComparisonOperator {
	case "is", "not is", "!=", ">=", "<=":
	default:
		return httperr.BadRequest(40120, "structured_filters.comparison_operator must be is, not is, !=, >= or <=")
	}
	if filter.Name == "" || len(filter.Name) > 64 {
		return httperr.BadRequest(40120, "structured_filters.name must contain 1 to 64 characters")
	}
	if filter.Value == "" || len(filter.Value) > 256 {
		return httperr.BadRequest(40120, "structured_filters.value must contain 1 to 256 characters")
	}
	return nil
}

func structuredFiltersCondition(filters []ragflow.MetadataConditionOp) *ragflow.MetadataCondition {
	if len(filters) == 0 {
		return nil
	}
	return &ragflow.MetadataCondition{Logic: "and", Conditions: append([]ragflow.MetadataConditionOp(nil), filters...)}
}

func mergeKnowledgeStructuredCondition(
	base *ragflow.MetadataCondition, filters []ragflow.MetadataConditionOp,
) (*ragflow.MetadataCondition, error) {
	if len(filters) == 0 {
		return base, nil
	}
	if base == nil || base.Empty() {
		return structuredFiltersCondition(filters), nil
	}
	if base.Logic != "and" {
		return nil, httperr.Internal("structured filters cannot be combined with the governance condition")
	}
	merged := cloneKnowledgeMetadataCondition(base)
	merged.Conditions = append(merged.Conditions, filters...)
	return merged, nil
}

func decodeKnowledgeConfigString(raw json.RawMessage, key string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", httperr.BadRequest(40120, key+" must be a string")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", httperr.BadRequest(40120, key+" must not be empty")
	}
	if len(value) > 64 {
		return "", httperr.BadRequest(40120, key+" must contain at most 64 characters")
	}
	return value, nil
}

func valueString(value interface{}) string {
	text, _ := value.(string)
	return text
}

func (s *Service) validateKnowledgeStrategyDataset(ctx context.Context, strategy *model.KnowledgeStrategy) error {
	dataset, err := s.Store.GetDatasetLink(ctx, strategy.TenantID, strategy.DatasetID)
	if err != nil {
		return err
	}
	if dataset == nil || dataset.ID == "" {
		return httperr.BadRequest(40120, "dataset_id must reference a tenant dataset")
	}
	strategy.ProjectID = dataset.ProjectID
	return nil
}

func (s *Service) validateKnowledgeStrategyCompilationTemplate(
	ctx context.Context, strategy *model.KnowledgeStrategy,
) error {
	if strategy.StrategyType != model.KnowledgeStrategyCompiled {
		return nil
	}
	config, err := parseKnowledgeStrategyConfig(strategy.Config)
	if err != nil {
		return err
	}
	templates, err := s.RAGFlow.ListCompilationTemplates(ctx, ragflow.CompilationTemplateSourceBuiltins)
	if err != nil {
		return httperr.Internal("compilation template catalog is unavailable")
	}
	for _, template := range templates {
		if strings.EqualFold(config.CompilationTemplate, template.ID) ||
			strings.EqualFold(config.CompilationTemplate, template.Kind) ||
			strings.EqualFold(config.CompilationTemplate, template.DisplayName) {
			return nil
		}
	}
	return httperr.BadRequest(40120, "compilation_template must reference a RAGFlow built-in template")
}

type knowledgeProbeChecks struct {
	RequestAccepted     bool   `json:"request_accepted"`
	StrategySelected    bool   `json:"strategy_selected"`
	ArtifactPresent     bool   `json:"artifact_present"`
	ArtifactVerified    bool   `json:"artifact_verified"`
	ArtifactCount       int64  `json:"artifact_count"`
	LatencyWithinLimit  bool   `json:"latency_within_limit"`
	EngineVersionValid  bool   `json:"engine_version_valid"`
	LatencyMs           int64  `json:"latency_ms"`
	LatencyThresholdMs  int64  `json:"latency_threshold_ms"`
	RollingP95Ms        int64  `json:"rolling_p95_ms"`
	RollingWindowCount  int    `json:"rolling_window_count"`
	ProbeDatasetID      string `json:"probe_dataset_id"`
	CompilationTemplate string `json:"compilation_template,omitempty"`
}

func (checks knowledgeProbeChecks) Passed() bool {
	return checks.RequestAccepted && checks.StrategySelected && checks.ArtifactPresent &&
		checks.ArtifactVerified && checks.LatencyWithinLimit && checks.EngineVersionValid
}

func knowledgeProbeP95(latencies []int64) int64 {
	if len(latencies) == 0 {
		return 0
	}
	values := append([]int64(nil), latencies...)
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	rank := int(math.Ceil(0.95 * float64(len(values))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(values) {
		rank = len(values)
	}
	return values[rank-1]
}

func knowledgeProbeAggregateStatus(current string, recent []model.KnowledgeCapabilityProbe) string {
	if current == model.KnowledgeProbeStatusFailed {
		return model.KnowledgeProbeStatusFailed
	}
	consecutivePassed := 0
	for _, probe := range recent {
		if probe.Status != model.KnowledgeProbeStatusPassed {
			break
		}
		consecutivePassed++
	}
	if consecutivePassed >= 3 {
		return model.KnowledgeProbeStatusPassed
	}
	return model.KnowledgeProbeStatusUnknown
}

func auditForKnowledgeCapabilityProbe(
	strategy *model.KnowledgeStrategy, probe *model.KnowledgeCapabilityProbe, userID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: strategy.TenantID, ActorTenantID: strategy.TenantID,
		TargetTenantID: strategy.TenantID, UserID: userID, Action: "knowledge_strategy.probed",
		Resource: "knowledge-strategy", ResourceID: strategy.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"strategy_id": strategy.ID, "probe_id": probe.ID, "probe_status": probe.Status,
			"probe_key": probe.ProbeKey, "ragflow_version": probe.RAGFlowVersion,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

func auditForKnowledgeStrategy(
	strategy *model.KnowledgeStrategy, action, userID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: strategy.TenantID, ActorTenantID: strategy.TenantID,
		TargetTenantID: strategy.TenantID, UserID: userID, Action: action,
		Resource: "knowledge-strategy", ResourceID: strategy.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"strategy_id": strategy.ID, "dataset_id": strategy.DatasetID,
			"strategy_type": strategy.StrategyType, "ragflow_capability": strategy.RAGFlowCapability,
			"fallback_strategy": strategy.FallbackStrategy, "active": strategy.Active,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}
