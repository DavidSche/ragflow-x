package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const compilationArtifactMaxPages = 20
const compilationRetrievalPageSize = 64
const compilationUpstreamStallThreshold = 24 * time.Hour

type compilationPipelineContractError struct {
	PipelineID string
	Contract   map[string]interface{}
}

func (e *compilationPipelineContractError) Error() string {
	return "compilation pipeline contract is invalid"
}

func normalizeCompilationKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "wiki", "artifact":
		return model.CompilationKindWiki
	case "tree", "raptor":
		return model.CompilationKindTree
	case "graph", "knowledge_graph", "knowledgegraph":
		return model.CompilationKindGraph
	case "mindmap", "mind_map":
		return model.CompilationKindMindmap
	case "timeline":
		return model.CompilationKindTimeline
	case "pageindex", "page_index", "skill":
		return model.CompilationKindPageIndex
	default:
		return ""
	}
}

func normalizeCompilationDocumentIDs(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 64 {
			return nil, httperr.BadRequest(40146, "document_ids must contain non-empty ids of at most 64 characters")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) > 100 {
		return nil, httperr.BadRequest(40146, "document_ids must contain at most 100 documents")
	}
	return out, nil
}

func selectCompilationDocuments(documents []ragflow.Document, wanted []string) ([]ragflow.Document, error) {
	if len(wanted) == 0 {
		return nil, httperr.BadRequest(40146, "at least one document_id is required")
	}
	byID := make(map[string]ragflow.Document, len(documents))
	for _, document := range documents {
		byID[document.ID] = document
	}
	selected := make([]ragflow.Document, 0, len(wanted))
	for _, documentID := range wanted {
		document, exists := byID[documentID]
		if !exists {
			return nil, httperr.NotFound("compilation document not found")
		}
		if document.Enabled != "1" || strings.TrimSpace(document.PipelineID) == "" {
			return nil, httperr.New(409, 40973, "compilation documents must be enabled and bound to an ingestion pipeline")
		}
		selected = append(selected, document)
	}
	return selected, nil
}

func decodeCompilationRunDocumentIDs(encoded string) ([]string, error) {
	documentIDs := []string{}
	if err := json.Unmarshal([]byte(encoded), &documentIDs); err != nil {
		return nil, httperr.Internal("decode compilation document ids failed")
	}
	return documentIDs, nil
}

func hashStableJSON(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func compilationDocumentManifestHash(documents []ragflow.Document) string {
	entries := make([]map[string]interface{}, 0, len(documents))
	for _, document := range documents {
		entries = append(entries, map[string]interface{}{
			"document_id": document.ID,
			"name":        document.Name,
			"enabled":     document.Enabled,
			"parser_id":   document.ParserID,
			"pipeline_id": document.PipelineID,
			"size":        document.Size,
			"created_at":  document.CreateTime,
		})
	}
	hash, err := hashStableJSON(map[string]interface{}{
		"algorithm": "source-identity-projection-v2", "documents": entries,
	})
	if err != nil {
		return ""
	}
	return hash
}

func compilationTemplateHash(kind, profileID string) string {
	hash, err := hashStableJSON(map[string]interface{}{
		"kind": kind, "profile_id": profileID, "source": "governance_intent_v1",
	})
	if err != nil {
		return ""
	}
	return hash
}

func compilationPipelineHash(documents []ragflow.Document) string {
	pipelineIDs := make([]string, 0, len(documents))
	seen := make(map[string]struct{})
	for _, document := range documents {
		pipelineID := strings.TrimSpace(document.PipelineID)
		if pipelineID == "" {
			continue
		}
		if _, exists := seen[pipelineID]; exists {
			continue
		}
		seen[pipelineID] = struct{}{}
		pipelineIDs = append(pipelineIDs, pipelineID)
	}
	sort.Strings(pipelineIDs)
	hash, err := hashStableJSON(map[string]interface{}{
		"algorithm": "pipeline_intent_v1", "pipeline_ids": pipelineIDs,
	})
	if err != nil {
		return ""
	}
	return hash
}

func compilationManifestComplete(documents []ragflow.Document) bool {
	for _, document := range documents {
		if document.ID == "" || document.PipelineID == "" || document.Enabled != "1" || document.UpdateTime == 0 {
			return false
		}
	}
	return len(documents) > 0
}

func compilationInterfaceSlice(value interface{}) ([]interface{}, bool) {
	items, ok := value.([]interface{})
	return items, ok
}

func compilationMap(value interface{}) (map[string]interface{}, bool) {
	object, ok := value.(map[string]interface{})
	return object, ok
}

func compilationHasCompilerConfiguration(value interface{}, depth int) bool {
	if depth <= 0 {
		return false
	}
	object, ok := compilationMap(value)
	if !ok {
		return false
	}
	for key, item := range object {
		if key == "compilation_template_id" || key == "compilation_template_group_id" {
			return true
		}
		if compilationHasCompilerConfiguration(item, depth-1) {
			return true
		}
	}
	return false
}

func inspectCompilationPipelineDSL(dsl map[string]interface{}) (map[string]interface{}, bool) {
	contract := map[string]interface{}{}
	componentRoot, ok := compilationMap(dsl["components"])
	if dsl == nil || !ok || len(componentRoot) == 0 {
		return contract, false
	}
	graphRoot, ok := compilationMap(dsl["graph"])
	if !ok {
		return contract, false
	}
	nodeItems, ok := compilationInterfaceSlice(graphRoot["nodes"])
	if !ok || len(nodeItems) == 0 {
		return contract, false
	}
	edgeItems, ok := compilationInterfaceSlice(graphRoot["edges"])
	if !ok {
		return contract, false
	}

	nodeTypes := make(map[string]string, len(nodeItems))
	for _, item := range nodeItems {
		node, valid := compilationMap(item)
		if !valid {
			return contract, false
		}
		nodeID, _ := node["id"].(string)
		nodeType, _ := node["type"].(string)
		nodeID, nodeType = strings.TrimSpace(nodeID), strings.TrimSpace(nodeType)
		if nodeID == "" || nodeType == "" {
			return contract, false
		}
		if _, exists := nodeTypes[nodeID]; exists {
			return contract, false
		}
		nodeTypes[nodeID] = nodeType
	}

	componentTypes := map[string]interface{}{}
	hasParser := false
	hasChunker := false
	hasCompiler := false
	hasIndexer := false
	for componentID, item := range componentRoot {
		component, valid := compilationMap(item)
		if !valid {
			return contract, false
		}
		componentObject, valid := compilationMap(component["obj"])
		if !valid {
			return contract, false
		}
		componentName, _ := componentObject["component_name"].(string)
		nodeType := nodeTypes[componentID]
		componentTypes[componentID] = map[string]interface{}{
			"component_name": componentName,
			"node_type":      nodeType,
		}
		switch {
		case nodeType == "parserNode" || componentName == "Parser":
			hasParser = true
		case nodeType == "chunkerNode" || strings.HasSuffix(componentName, "Chunker"):
			hasChunker = true
		case nodeType == "compilationNode" || componentName == "Compiler" ||
			compilationHasCompilerConfiguration(componentObject["params"], 8):
			hasCompiler = true
		case nodeType == "tokenizerNode" || componentName == "Tokenizer":
			hasIndexer = true
		}
	}

	edges := make([][2]string, 0, len(edgeItems))
	for _, item := range edgeItems {
		edge, valid := compilationMap(item)
		if !valid {
			return contract, false
		}
		source, _ := edge["source"].(string)
		target, _ := edge["target"].(string)
		source, target = strings.TrimSpace(source), strings.TrimSpace(target)
		if nodeTypes[source] == "" || nodeTypes[target] == "" {
			return contract, false
		}
		edges = append(edges, [2]string{source, target})
	}

	pathValid := hasCompiler && hasIndexer
	if pathValid {
		compilerIDs := make([]string, 0)
		for componentID, component := range componentTypes {
			componentDetail, _ := compilationMap(component)
			nodeType, _ := componentDetail["node_type"].(string)
			componentName, _ := componentDetail["component_name"].(string)
			if nodeType == "compilationNode" || componentName == "Compiler" {
				compilerIDs = append(compilerIDs, componentID)
			}
		}
		for _, compilerID := range compilerIDs {
			visited := map[string]bool{compilerID: true}
			queue := []string{compilerID}
			reachable := false
			for len(queue) > 0 && !reachable {
				current := queue[0]
				queue = queue[1:]
				for _, edge := range edges {
					if edge[0] != current || visited[edge[1]] {
						continue
					}
					visited[edge[1]] = true
					nextType := nodeTypes[edge[1]]
					nextComponent, _ := compilationMap(componentRoot[edge[1]])
					nextObject, _ := compilationMap(nextComponent["obj"])
					nextName, _ := nextObject["component_name"].(string)
					if nextType == "tokenizerNode" || nextName == "Tokenizer" {
						reachable = true
						break
					}
					queue = append(queue, edge[1])
				}
			}
			if !reachable {
				pathValid = false
				break
			}
		}
	}

	return map[string]interface{}{
		"node_count":      len(nodeItems),
		"component_types": componentTypes,
		"has_parser":      hasParser,
		"has_chunker":     hasChunker,
		"has_compiler":    hasCompiler,
		"has_indexer":     hasIndexer,
		"path_valid":      pathValid,
		"valid":           hasParser && hasChunker && hasCompiler && hasIndexer && pathValid,
	}, true
}

func (s *Service) validateCompilationPipelineContracts(
	ctx context.Context, documents []ragflow.Document,
) (map[string]map[string]interface{}, error) {
	pipelineIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, document := range documents {
		pipelineID := strings.TrimSpace(document.PipelineID)
		if pipelineID == "" {
			continue
		}
		if _, exists := seen[pipelineID]; exists {
			continue
		}
		seen[pipelineID] = struct{}{}
		pipelineIDs = append(pipelineIDs, pipelineID)
	}
	sort.Strings(pipelineIDs)

	contracts := make(map[string]map[string]interface{}, len(pipelineIDs))
	for _, pipelineID := range pipelineIDs {
		pipeline, err := s.RAGFlow.GetPipeline(ctx, pipelineID)
		if err != nil {
			return contracts, err
		}
		contract, parsed := inspectCompilationPipelineDSL(pipeline.DSL)
		if !parsed {
			contract["valid"] = false
			contract["failure_code"] = "pipeline_contract_invalid"
			return contracts, &compilationPipelineContractError{PipelineID: pipelineID, Contract: contract}
		}
		if valid, _ := contract["valid"].(bool); !valid {
			contract["failure_code"] = "pipeline_contract_invalid"
			return contracts, &compilationPipelineContractError{PipelineID: pipelineID, Contract: contract}
		}
		contracts[pipelineID] = contract
	}
	return contracts, nil
}

func compilationRunDetail(kind, reason string, extra map[string]interface{}) string {
	detail := map[string]interface{}{
		"dispatch_policy": "explicit_document_parse", "kind": kind, "reason": reason,
	}
	for key, value := range extra {
		detail[key] = value
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return `{"reason":"detail_encoding_failed"}`
	}
	return string(encoded)
}

func decodeCompilationRunDetail(raw string) map[string]interface{} {
	var detail map[string]interface{}
	if json.Unmarshal([]byte(raw), &detail) == nil && detail != nil {
		return detail
	}
	return map[string]interface{}{}
}

// ScheduleCompilationReconcile arms one bounded tenant scan. The Worker
// reschedules only while active runs remain, so completed datasets do not
// create a permanent polling job.
func (s *Service) ScheduleCompilationReconcile(ctx context.Context, tenantID, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindCompilationReconcile, tenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	return s.Runner.Enqueue(ctx, model.JobKindCompilationReconcile,
		"compilation_reconcile:"+tenantID+":"+time.Now().UTC().Format("20060102T150405.000000000Z"),
		tenantID, "", time.Time{}, 1,
	)
}

func (s *Service) ProcessCompilationRuns(ctx context.Context, tenantID, datasetID, excludeJobID string) error {
	runs, err := s.Store.ListActiveCompilationRuns(ctx, tenantID, datasetID, 20)
	if err != nil {
		return err
	}
	var failures []error
	for _, run := range runs {
		item := run
		var runErr error
		if item.State == model.CompilationRunAccepted {
			runErr = s.dispatchCompilationRun(&item)
		} else {
			runErr = s.reconcileCompilationRun(&item)
		}
		if runErr != nil {
			logger.Warn("compilation run processing failed", "run_id", item.ID, "error", runErr)
			failures = append(failures, runErr)
		}
	}
	activeAfter, err := s.Store.ListActiveCompilationRuns(ctx, tenantID, datasetID, 1)
	if err == nil && len(activeAfter) > 0 {
		if _, err := s.ScheduleCompilationReconcile(ctx, tenantID, excludeJobID); err != nil {
			logger.Warn("compilation reconcile reschedule failed", "error", err)
		}
	}
	return errors.Join(failures...)
}

func (s *Service) ReconcileCompilationDataset(
	ctx context.Context, tenantID, actorID, datasetID string,
) ([]model.CompilationRun, error) {
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, err
	}
	if dataset == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	if err := s.enforceKnowledgeStrategyTeamAdminProject(ctx, tenantID, actorID, dataset.ProjectID); err != nil {
		return nil, err
	}
	if err := s.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		return nil, err
	}
	items, _, err := s.Store.ListCompilationRuns(ctx, tenantID, repository.CompilationRunFilter{DatasetID: datasetID}, 1, 50)
	return items, err
}

func (s *Service) dispatchCompilationRun(run *model.CompilationRun) error {
	ctx := context.Background()
	dataset, err := s.Store.GetDatasetLink(ctx, run.TenantID, run.DatasetID)
	if err != nil {
		return err
	}
	if dataset == nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunFailed, "dataset_link_missing", nil)
	}
	var documentIDs []string
	if err := json.Unmarshal([]byte(run.DocumentIDs), &documentIDs); err != nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunFailed, "document_manifest_invalid", nil)
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunUnknown, "document_snapshot_unavailable", nil)
	}
	selected, err := selectCompilationDocuments(documents, documentIDs)
	if err != nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunFailed, "dispatch_readiness_failed", nil)
	}
	currentManifest := compilationDocumentManifestHash(selected)
	if !run.ManifestComplete || currentManifest != run.ManifestHash {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunFailed, "manifest_changed_before_dispatch", map[string]interface{}{
			"current_manifest_hash": currentManifest,
		})
	}
	pipelineContracts, err := s.validateCompilationPipelineContracts(ctx, selected)
	if err != nil {
		var contractErr *compilationPipelineContractError
		if errors.As(err, &contractErr) {
			return s.transitionCompilationRun(ctx, run, model.CompilationRunFailed, "pipeline_contract_invalid", map[string]interface{}{
				"pipeline_id":       contractErr.PipelineID,
				"pipeline_contract": contractErr.Contract,
			})
		}
		return s.transitionCompilationRun(ctx, run, model.CompilationRunUnknown, "pipeline_contract_probe_failed", nil)
	}
	if err := s.prepareDatasetParseReadiness(ctx, dataset); err != nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunFailed, "parse_readiness_failed", nil)
	}
	now := time.Now().UTC()
	changed, err := s.Store.TransitionCompilationRun(ctx, run.TenantID, run.ID,
		[]string{model.CompilationRunAccepted},
		map[string]interface{}{
			"state": model.CompilationRunDispatched, "dispatched_at": now,
			"detail": compilationRunDetail(run.Kind, "dispatched", map[string]interface{}{
				"document_count":     len(selected),
				"pipeline_contracts": pipelineContracts,
			}),
		},
	)
	if err != nil {
		_ = s.transitionCompilationRun(ctx, run, model.CompilationRunUnknown, "dispatched_state_unconfirmed", nil)
		return err
	}
	if !changed {
		return nil
	}
	if err := s.Store.CreateAudit(ctx, compilationRunAuditState(
		run, model.CompilationRunDispatched, "compilation_run.dispatched", run.RequestedBy, now,
	)); err != nil {
		return err
	}
	if err := s.RAGFlow.IngestDocuments(ctx, documentIDs); err != nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunUnknown, "dispatch_result_unconfirmed", nil)
	}
	// RAGFlow does not return compilation token usage in v1.0.0-rc1. The
	// preflight estimate is therefore the metered actual and is explicitly
	// marked estimated in rgx_cost_metric.
	if err := s.recordCompilationCostMetric(ctx, run); err != nil {
		obs.Get().IncEnhancementBudgetRejected(run.TenantID, "cost_record_failed")
	}
	return nil
}

func (s *Service) reconcileCompilationRun(run *model.CompilationRun) error {
	ctx := context.Background()
	dataset, err := s.Store.GetDatasetLink(ctx, run.TenantID, run.DatasetID)
	if err != nil {
		return err
	}
	if dataset == nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunFailed, "dataset_link_missing", nil)
	}
	if run.State == model.CompilationRunUnknown {
		return s.reconcileUnknownCompilationRun(ctx, run, dataset)
	}
	status, err := s.RAGFlow.GetCompilationStatusForKind(ctx, dataset.RAGFlowDatasetID, run.Kind)
	if err != nil {
		return s.transitionCompilationRun(ctx, run, model.CompilationRunUnknown, "status_probe_failed", nil)
	}
	if !ragflow.IsKnownCompilationStatusState(status.State) {
		obs.Get().ClearCompilationUpstreamStallAge(run.TenantID, run.Kind)
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "upstream_state_unrecognized", map[string]interface{}{
			"upstream_state": status.State,
		}, time.Now().UTC(), nil)
	}
	switch status.State {
	case "pending", "running":
		now := time.Now().UTC()
		// 137 §11.1: publish the upstream queue Inflight/Backlog depth
		// observed with the status contract (KC-002).
		obs.Get().SetCompilationQueueDepth(run.TenantID, run.Kind, int64(status.Inflight), int64(status.Backlog))
		if err := s.checkCompilationSourceDuringExecution(ctx, run, dataset); err != nil {
			return err
		}
		stallAge := compilationUpstreamStallAge(run, status, now)
		if stallAge >= compilationUpstreamStallThreshold.Seconds() {
			obs.Get().SetCompilationUpstreamStallAge(run.TenantID, run.Kind, stallAge)
			return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "upstream_stalled", map[string]interface{}{
				"upstream_state": status.State, "progress": status.Progress, "current_phase": status.CurrentPhase,
				"inflight": status.Inflight, "backlog": status.Backlog,
				"upstream_error": status.Error, "upstream_updated_at": status.UpdatedAt.UTC(),
				"upstream_age_seconds": stallAge, "stall_threshold_seconds": compilationUpstreamStallThreshold.Seconds(),
			}, now, nil)
		}
		obs.Get().SetCompilationUpstreamStallAge(run.TenantID, run.Kind, stallAge)
		return s.storeCompilationAudit(ctx, run, model.CompilationRunDispatched, "upstream_running", map[string]interface{}{
			"upstream_state": status.State, "progress": status.Progress, "current_phase": status.CurrentPhase,
			"inflight": status.Inflight, "backlog": status.Backlog,
			"upstream_error": status.Error, "upstream_updated_at": status.UpdatedAt.UTC(),
			"upstream_age_seconds": stallAge,
		}, now, nil)
	case "completed":
		obs.Get().ClearCompilationUpstreamStallAge(run.TenantID, run.Kind)
		obs.Get().ClearCompilationQueueDepth(run.TenantID, run.Kind)
		return s.completeCompilationRun(ctx, run, dataset, status)
	case "idle":
		obs.Get().ClearCompilationUpstreamStallAge(run.TenantID, run.Kind)
		obs.Get().ClearCompilationQueueDepth(run.TenantID, run.Kind)
		documents, sourceErr := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
		sourceTerminal := sourceErr == nil && compilationRunSourceDocumentsTerminal(run, documents)
		if compilationStatusCompletedAfterDispatch(run, status) {
			if sourceErr != nil || !sourceTerminal {
				return s.storeCompilationAudit(ctx, run, model.CompilationRunDispatched, "upstream_source_reparse_running", map[string]interface{}{
					"upstream_state": status.State,
				}, time.Now().UTC(), nil)
			}
			return s.completeCompilationRun(ctx, run, dataset, status)
		}
		if sourceTerminal {
			return s.completeCompilationRun(ctx, run, dataset, status)
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "upstream_idle_after_dispatch", map[string]interface{}{
			"upstream_state": status.State,
		}, time.Now().UTC(), nil)
	}
	return nil
}

func compilationStatusCompletedAfterDispatch(
	run *model.CompilationRun, status *ragflow.CompilationStatus,
) bool {
	if status.State == "completed" {
		return true
	}
	if run.DispatchedAt == nil || status.LastCompletedAt == nil {
		return false
	}
	return !status.LastCompletedAt.Before(*run.DispatchedAt)
}

func compilationRunSourceDocumentsTerminal(
	run *model.CompilationRun, documents []ragflow.Document,
) bool {
	documentIDs, err := decodeCompilationRunDocumentIDs(run.DocumentIDs)
	if err != nil {
		return false
	}
	selected, err := selectCompilationDocuments(documents, documentIDs)
	if err != nil {
		return false
	}
	for _, document := range selected {
		if document.Progress < 1 || !strings.EqualFold(document.IngestionStatus, "COMPLETED") {
			return false
		}
	}
	return true
}

func documentIDsIntersect(left []string, right []string) bool {
	rightSet := make(map[string]struct{}, len(right))
	for _, documentID := range right {
		rightSet[strings.TrimSpace(documentID)] = struct{}{}
	}
	for _, documentID := range left {
		if _, exists := rightSet[strings.TrimSpace(documentID)]; exists {
			return true
		}
	}
	return false
}

func compilationArtifactFilterForKind(kind string, page int) ragflow.DatasetArtifactFilter {
	filter := ragflow.DatasetArtifactFilter{Page: page, PageSize: 100}
	if kind != model.CompilationKindWiki {
		filter.PageType = kind
	}
	return filter
}

func compilationUpstreamStallAge(run *model.CompilationRun, status *ragflow.CompilationStatus, now time.Time) float64 {
	reference := status.UpdatedAt
	if reference.IsZero() && run.DispatchedAt != nil {
		reference = *run.DispatchedAt
	}
	if reference.IsZero() {
		return 0
	}
	age := now.Sub(reference)
	if age < 0 {
		age = 0
	}
	return age.Seconds()
}

func (s *Service) completeCompilationRun(
	ctx context.Context, run *model.CompilationRun,
	dataset *model.DatasetLink, status *ragflow.CompilationStatus,
) error {
	now := time.Now().UTC()
	if err := s.verifyCompilationPostRunSource(ctx, run, dataset); err != nil {
		return err
	}
	artifactCount := 0
	upstreamTotal := int64(0)
	for page := 1; page <= compilationArtifactMaxPages; page++ {
		artifacts, total, err := s.RAGFlow.ListDatasetArtifacts(
			ctx, dataset.RAGFlowDatasetID, compilationArtifactFilterForKind(run.Kind, page),
		)
		if err != nil {
			return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "artifact_probe_failed", map[string]interface{}{
				"upstream_state": status.State,
			}, now, nil)
		}
		upstreamTotal = total
		for _, artifact := range artifacts {
			artifactID := strings.TrimSpace(artifact.Slug)
			if artifactID == "" || artifact.PageType == "" {
				continue
			}
			detail, detailErr := s.RAGFlow.GetDatasetArtifact(
				ctx, dataset.RAGFlowDatasetID, artifact.PageType, artifact.Slug,
			)
			if detailErr != nil {
				return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "artifact_detail_probe_failed", map[string]interface{}{
					"upstream_state": status.State, "artifact_id": artifact.Slug,
				}, now, nil)
			}
			if detail != nil {
				documentIDs, decodeErr := decodeCompilationRunDocumentIDs(run.DocumentIDs)
				if decodeErr != nil {
					return decodeErr
				}
				if !documentIDsIntersect(documentIDs, detail.SourceDocIDs) {
					continue
				}
			}
			projection, projectionErr := artifactProjectionFromRun(run, artifact, detail)
			if projectionErr != nil {
				return projectionErr
			}
			if err := s.Store.UpsertArtifactProjection(ctx, projection); err != nil {
				return err
			}
			artifactCount++
		}
		if int64(page)*100 >= upstreamTotal {
			break
		}
	}
	if artifactCount == 0 && run.Kind == model.CompilationKindPageIndex {
		documentIDs, decodeErr := decodeCompilationRunDocumentIDs(run.DocumentIDs)
		if decodeErr != nil {
			return decodeErr
		}
		for retrievalPage := 1; retrievalPage <= compilationArtifactMaxPages; retrievalPage++ {
			result, searchErr := s.RAGFlow.SearchDataset(ctx, dataset.RAGFlowDatasetID, ragflow.SearchDatasetRequest{
				Question:                    "page index",
				DocumentIDs:                 documentIDs,
				Page:                        retrievalPage,
				Size:                        compilationRetrievalPageSize,
				IncludeKnowledgeCompilation: true,
				Keyword:                     true,
			})
			if searchErr != nil {
				return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "artifact_retrieval_probe_failed", map[string]interface{}{
					"upstream_state": status.State,
				}, now, nil)
			}
			upstreamTotal = result.Total
			for _, retrievalChunk := range result.Chunks {
				projection, projectionErr := artifactProjectionFromRetrievalChunk(run, retrievalChunk)
				if projectionErr != nil || projection == nil {
					continue
				}
				if err := s.Store.UpsertArtifactProjection(ctx, projection); err != nil {
					return err
				}
				artifactCount++
			}
			if int64(retrievalPage)*compilationRetrievalPageSize >= upstreamTotal {
				break
			}
		}
		if int64(compilationArtifactMaxPages)*compilationRetrievalPageSize < upstreamTotal {
			return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "artifact_retrieval_scan_incomplete", map[string]interface{}{
				"upstream_state": status.State, "artifact_count": artifactCount, "upstream_total": upstreamTotal,
			}, now, nil)
		}
	}
	boundCount, hashCount, bindingIncomplete := 0, 0, 0
	projections, _, err := s.Store.ListArtifactProjections(ctx, run.TenantID, run.DatasetID, run.ID, 1, compilationArtifactMaxPages*100)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		var snapshot map[string]interface{}
		if json.Unmarshal([]byte(projection.ArtifactJSON), &snapshot) != nil {
			bindingIncomplete++
			obs.Get().AddCompilationArtifactBinding(run.TenantID, "incomplete")
			continue
		}
		if runBound, _ := snapshot["run_binding"].(bool); runBound {
			boundCount++
		} else {
			bindingIncomplete++
			obs.Get().AddCompilationArtifactBinding(run.TenantID, "incomplete")
			continue
		}
		if contentBound, _ := snapshot["content_hash_binding"].(bool); contentBound {
			hashCount++
		} else {
			bindingIncomplete++
			obs.Get().AddCompilationArtifactBinding(run.TenantID, "incomplete")
			continue
		}
		obs.Get().AddCompilationArtifactBinding(run.TenantID, "verified")
	}
	if artifactCount == 0 || int64(compilationArtifactMaxPages)*100 < upstreamTotal || bindingIncomplete > 0 {
		obs.Get().AddCompilationReadyEval(run.TenantID, "incomplete")
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "artifact_scan_incomplete", map[string]interface{}{
			"upstream_state": status.State,
			"artifact_count": artifactCount, "bound_artifact_count": boundCount,
			"hash_bound_artifact_count": hashCount, "binding_incomplete_count": bindingIncomplete,
		}, now, nil)
	}
	obs.Get().AddCompilationReadyEval(run.TenantID, "ready")
	if err := s.storeCompilationAudit(ctx, run, model.CompilationRunConfirmed, "status_completed_artifact_bound", map[string]interface{}{
		"upstream_state": status.State, "artifact_count": artifactCount,
		"ready_artifact_count": len(projections), "bound_artifact_count": boundCount,
		"hash_bound_artifact_count": hashCount,
		"evidence_state":            "payload_hash_verified", "artifact_run_binding": true, "artifact_hash_binding": true,
	}, now, &now); err != nil {
		return err
	}
	s.maybeCreateCompilationGateEvaluation(ctx, run)
	return nil
}

const compilationImprovementGatePolicyVersion = "compilation-improvement-gate-v1"

type compilationReadinessSnapshot struct {
	EvaluationRunID  string `json:"evaluation_run_id"`
	StrategyID       string `json:"strategy_id"`
	EvalSetID        string `json:"eval_set_id"`
	EvalSetVersion   int64  `json:"eval_set_version"`
	EvalSetHash      string `json:"eval_set_hash"`
	CompilationRunID string `json:"compilation_run_id"`
	ActivatedAt      string `json:"activated_at"`
}

// compilationEvaluationCapability maps a compilation kind onto the scoped
// evaluation capability that paired cases must declare.
func compilationEvaluationCapability(kind string) string {
	switch kind {
	case model.CompilationKindWiki:
		return model.EvaluationCapabilityWiki
	default:
		return model.EvaluationCapabilityPageIndex
	}
}

func compilationIdentityJSON(run *model.CompilationRun) (string, error) {
	identity, err := json.Marshal(map[string]interface{}{
		"compilation_run_id": run.ID,
		"manifest_hash":      run.ManifestHash,
		"template_hash":      run.TemplateHash,
		"pipeline_hash":      run.PipelineHash,
	})
	if err != nil {
		return "", err
	}
	return string(identity), nil
}

func compilationIdentityMatches(raw string, run *model.CompilationRun) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var config struct {
		CompilationIdentity *struct {
			ManifestHash string `json:"manifest_hash"`
			TemplateHash string `json:"template_hash"`
			PipelineHash string `json:"pipeline_hash"`
		} `json:"compilation_identity"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil || config.CompilationIdentity == nil {
		return false
	}
	return config.CompilationIdentity.ManifestHash == run.ManifestHash &&
		config.CompilationIdentity.TemplateHash == run.TemplateHash &&
		config.CompilationIdentity.PipelineHash == run.PipelineHash
}

func buildCompilationGateExecutionConfig(run *model.CompilationRun) (json.RawMessage, error) {
	identity, err := compilationIdentityJSON(run)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]interface{}{
		"contract_version":                   scopedEvaluationContractVersionV3,
		"top_k":                              8,
		"min_samples":                        30,
		"min_hit_lift_pp":                    0,
		"max_p95_latency_delta_ms":           2500,
		"max_no_answer_lift_pp":              0,
		"primary_metric":                     "citation_precision",
		"min_citation_precision_lift_pp":     20,
		"min_compiled_artifact_effect_pairs": 15,
		"answer_correctness_method":          "expected_evidence_match",
		"require_citation_precision":         true,
		"compilation_identity":               json.RawMessage(identity),
	})
}

func (s *Service) maybeCreateCompilationGateEvaluation(ctx context.Context, run *model.CompilationRun) {
	if run == nil || strings.TrimSpace(run.StrategyID) == "" {
		return
	}
	strategy, err := s.GetKnowledgeStrategy(ctx, run.TenantID, run.StrategyID)
	if err != nil {
		logger.Warn("compilation improvement gate strategy lookup failed",
			"tenant_id", run.TenantID, "strategy_id", run.StrategyID, "run_id", run.ID, "error", err)
		return
	}
	if strategy == nil || strategy.StrategyType != model.KnowledgeStrategyCompiled {
		return
	}
	config, err := parseKnowledgeStrategyConfig(strategy.Config)
	if err != nil {
		logger.Warn("compilation improvement gate strategy config invalid",
			"tenant_id", run.TenantID, "strategy_id", run.StrategyID, "run_id", run.ID, "error", err)
		return
	}
	if config.ImprovementGate == nil || !config.ImprovementGate.Enabled {
		return
	}
	gate := config.ImprovementGate
	if strings.TrimSpace(gate.EvalSetID) == "" || gate.EvalSetVersion <= 0 {
		logger.Warn("compilation improvement gate eval set identity is incomplete",
			"tenant_id", run.TenantID, "strategy_id", run.StrategyID, "run_id", run.ID)
		return
	}
	evalSet, err := s.Store.GetEvaluationSetVersion(ctx, run.TenantID, gate.EvalSetID, gate.EvalSetVersion)
	if err != nil {
		logger.Warn("compilation improvement gate eval set lookup failed",
			"tenant_id", run.TenantID, "eval_set_id", gate.EvalSetID, "eval_set_version", gate.EvalSetVersion, "run_id", run.ID, "error", err)
		return
	}
	if evalSet == nil || strings.TrimSpace(evalSet.CasesSnapshotHash) == "" {
		logger.Warn("compilation improvement gate eval set is unavailable",
			"tenant_id", run.TenantID, "eval_set_id", gate.EvalSetID, "eval_set_version", gate.EvalSetVersion, "run_id", run.ID)
		return
	}
	existingRuns, _, listErr := s.Store.ListEvaluationRuns(ctx, run.TenantID, repository.EvaluationScopeFilter{
		Scope:          model.EvaluationScopeCompilationStrategy,
		StrategyID:     run.StrategyID,
		EvalSetID:      gate.EvalSetID,
		EvalSetVersion: gate.EvalSetVersion,
		Statuses:       []string{model.EvaluationRunQueued, model.EvaluationRunRunning},
	}, 1, 20)
	if listErr != nil {
		logger.Warn("compilation improvement gate dedupe lookup failed",
			"tenant_id", run.TenantID, "strategy_id", run.StrategyID, "run_id", run.ID, "error", listErr)
		return
	}
	for _, existing := range existingRuns {
		if compilationIdentityMatches(existing.ExecutionConfig, run) {
			s.attachCompilationReadinessSnapshot(ctx, run, existing.ID, gate, evalSet.Hash, time.Now().UTC())
			return
		}
	}
	execConfig, err := buildCompilationGateExecutionConfig(run)
	if err != nil {
		logger.Warn("compilation improvement gate execution config failed to encode",
			"tenant_id", run.TenantID, "strategy_id", run.StrategyID, "run_id", run.ID, "error", err)
		return
	}
	created, createErr := s.CreateEvaluationRun(ctx, run.TenantID, run.RequestedBy, EvaluationRunInput{
		EvaluationScope:          model.EvaluationScopeCompilationStrategy,
		XStrategyID:              run.StrategyID,
		EvalSetID:                gate.EvalSetID,
		EvalSetVersion:           gate.EvalSetVersion,
		EvalSetHash:              evalSet.Hash,
		EvaluationPolicyVersion:  compilationImprovementGatePolicyVersion,
		EvaluationPolicyHash:     evalSet.Hash,
		AggregationPolicyVersion: compilationImprovementGatePolicyVersion,
		AggregationPolicyHash:    evalSet.Hash,
		Capability:               compilationEvaluationCapability(run.Kind),
		ExecutionMode:            "paired_ab",
		ExecutionConfig:          execConfig,
	})
	if createErr != nil {
		logger.Warn("compilation improvement gate evaluation creation failed",
			"tenant_id", run.TenantID, "strategy_id", run.StrategyID, "run_id", run.ID, "error", createErr)
		return
	}
	s.attachCompilationReadinessSnapshot(ctx, run, created.ID, gate, evalSet.Hash, time.Now().UTC())
}

func (s *Service) attachCompilationReadinessSnapshot(
	ctx context.Context, run *model.CompilationRun, evaluationRunID string,
	gate *knowledgeStrategyImprovementGateConfig, evalSetHash string, activatedAt time.Time,
) {
	snapshot := compilationReadinessSnapshot{
		EvaluationRunID:  evaluationRunID,
		StrategyID:       run.StrategyID,
		EvalSetID:        gate.EvalSetID,
		EvalSetVersion:   gate.EvalSetVersion,
		EvalSetHash:      evalSetHash,
		CompilationRunID: run.ID,
		ActivatedAt:      activatedAt.UTC().Format(time.RFC3339),
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		logger.Warn("compilation readiness snapshot encode failed", "tenant_id", run.TenantID, "run_id", run.ID, "error", err)
		return
	}
	if _, err := s.Store.TransitionCompilationRun(
		ctx, run.TenantID, run.ID, []string{model.CompilationRunConfirmed},
		map[string]interface{}{"readiness_snapshot": string(encoded)},
	); err != nil {
		logger.Warn("compilation readiness snapshot attach failed", "tenant_id", run.TenantID, "run_id", run.ID, "error", err)
	}
}

func (s *Service) transitionCompilationRun(
	ctx context.Context, run *model.CompilationRun, state, reason string, extra map[string]interface{},
) error {
	return s.storeCompilationAudit(ctx, run, state, reason, extra, time.Now().UTC(), nil)
}

func (s *Service) storeCompilationAudit(
	ctx context.Context, run *model.CompilationRun, state, reason string,
	extra map[string]interface{}, at time.Time, completedAt *time.Time,
) error {
	detail := decodeCompilationRunDetail(run.Detail)
	detail["reason"] = reason
	for key, value := range extra {
		detail[key] = value
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	updates := map[string]interface{}{
		"state": state, "detail": string(encoded),
	}
	if completedAt != nil {
		updates["completed_at"] = *completedAt
	}
	if consistency, ok := extra["source_consistency"].(string); ok {
		updates["source_consistency"] = consistency
	}
	if snapshotID, ok := extra["downstream_source_snapshot_id"].(string); ok {
		updates["downstream_source_snapshot_id"] = snapshotID
	}
	if postRunHash, ok := extra["post_run_source_manifest_hash"].(string); ok {
		updates["post_run_source_manifest_hash"] = postRunHash
	}
	changed, err := s.Store.TransitionCompilationRun(
		ctx, run.TenantID, run.ID, []string{
			model.CompilationRunAccepted, model.CompilationRunDispatched,
			model.CompilationRunUnknown, model.CompilationRunConfirmed,
		}, updates,
	)
	if err != nil || !changed {
		return err
	}
	// 137 §11.1: every outcome transition is sampled for the run success
	// ratio and its alert thresholds.
	switch state {
	case model.CompilationRunConfirmed, model.CompilationRunFailed,
		model.CompilationRunUnknown, model.CompilationRunCancelled,
		model.CompilationRunBlocked, model.CompilationRunSkipped:
		obs.Get().AddCompilationRunTerminal(run.TenantID, state)
	}
	if state != model.CompilationRunUnknown {
		obs.Get().ClearCompilationUnknownAge(run.TenantID, run.Kind)
	}
	if state == model.CompilationRunFailed {
		if err := s.releaseCompilationReservation(ctx, run); err != nil {
			obs.Get().IncEnhancementBudgetRejected(run.TenantID, "reservation_release_failed")
		}
	}
	action := "compilation_run.reconciled"
	switch state {
	case model.CompilationRunDispatched:
		action = "compilation_run.dispatched"
	case model.CompilationRunConfirmed:
		action = "compilation_run.confirmed"
	case model.CompilationRunFailed:
		action = "compilation_run.failed"
	case model.CompilationRunUnknown:
		action = "compilation_run.unknown"
	}
	return s.Store.CreateAudit(ctx, compilationRunAuditState(run, state, action, run.RequestedBy, at))
}

func compilationRunAuditState(
	run *model.CompilationRun, state, action, userID string, at time.Time,
) *model.AuditLog {
	return &model.AuditLog{
		ID: id.New(), TenantID: run.TenantID, ActorTenantID: run.TenantID,
		TargetTenantID: run.TenantID, UserID: userID, Action: action,
		Resource: "compilation", ResourceID: run.ID,
		DetailJSON: encodeAuditDetail(map[string]interface{}{
			"run_id": run.ID, "dataset_id": run.DatasetID, "state": state, "kind": run.Kind,
		}),
		At: at, Result: "SUCCESS", AuthorizationDecision: "ALLOW",
		AuthorizationPermission:    "manage:compilation",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	}
}

type compilationReconcileWorker struct {
	svc *Service
}

func (w *compilationReconcileWorker) Kind() string { return model.JobKindCompilationReconcile }

func (w *compilationReconcileWorker) Run(ctx context.Context, job *model.Job) error {
	return w.svc.ProcessCompilationRuns(ctx, job.TenantID, "", job.ID)
}
