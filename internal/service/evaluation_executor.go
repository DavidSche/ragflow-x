package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const (
	scopedEvaluationContractVersion    = "scoped-paired-ab-v1"
	scopedEvaluationContractVersionV2  = "scoped-paired-ab-v2"
	scopedEvaluationContractVersionV3  = "scoped-paired-ab-v3"
	compiledArtifactSourceRaw          = "raw"
	compiledArtifactSourceCompiled     = "compiled"
	compiledArtifactSourceDerived      = "compiled_derived"
	compiledArtifactParticipationError = "compiled artifact participation is below the pre-registered minimum"
	compiledArtifactEffectError        = "compiled artifact effect is below the pre-registered minimum"
)

func isSearchDatasetCompilationCapability(capability string) bool {
	return capability == model.EvaluationCapabilityPageIndex ||
		capability == model.EvaluationCapabilityWiki
}

type scopedEvaluationProtocol struct {
	ContractVersion          string   `json:"contract_version"`
	TopK                     int      `json:"top_k"`
	MinSamples               int      `json:"min_samples"`
	MinHitLiftPP             float64  `json:"min_hit_lift_pp"`
	MaxP95LatencyDeltaMS     int64    `json:"max_p95_latency_delta_ms"`
	MaxNoAnswerLiftPP        float64  `json:"max_no_answer_lift_pp"`
	AnswerCorrectnessMethod  string   `json:"answer_correctness_method"`
	RequireCitationPrecision bool     `json:"require_citation_precision"`
	RequireTaskTypeCoverage  bool     `json:"require_task_type_coverage"`
	RequiredTaskTypes        []string `json:"required_task_types"`
	MinTaskTypeSamples       int      `json:"min_task_type_samples"`
	MinCompiledArtifactPairs int      `json:"min_compiled_artifact_pairs"`
	PrimaryMetric            string   `json:"primary_metric"`
	MinCitationLiftPP        float64  `json:"min_citation_precision_lift_pp"`
	MinCompiledEffectPairs   int      `json:"min_compiled_artifact_effect_pairs"`
}

type scopedEvaluationSample struct {
	cases map[string]model.EvaluationCaseVersion
}

func (s *Service) ExecuteScopedEvaluationRun(ctx context.Context, tenantID, runID string) (*model.EvaluationRun, error) {
	run, err := s.Store.GetEvaluationRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("evaluation run not found")
	}
	if run.EvaluationScope == model.EvaluationScopeRelease {
		return nil, httperr.BadRequest(40072, "release evaluation uses the release evaluator")
	}
	if run.ExecutionMode != "paired_ab" {
		return nil, httperr.BadRequest(40072, "evaluation run is not a paired A/B execution")
	}
	if run.Status == model.EvaluationRunQueued {
		if err := s.Store.StartEvaluationRun(ctx, run.ID); err != nil {
			return nil, err
		}
	} else if run.Status != model.EvaluationRunRunning {
		return nil, httperr.New(409, 40989, "evaluation run is already terminal")
	}

	protocol, err := parseScopedEvaluationProtocol(run.ExecutionConfig)
	if err != nil {
		return s.failScopedEvaluationRun(ctx, run, err)
	}
	cases, err := s.Store.ListEvaluationCaseVersions(ctx, tenantID, run.EvalSetID, run.EvalSetVersion)
	if err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return s.failScopedEvaluationRun(ctx, run, httperr.BadRequest(40073, "evaluation set has no cases"))
	}
	if err := validateScopedEvaluationCases(cases, run, protocol); err != nil {
		return s.failScopedEvaluationRun(ctx, run, err)
	}

	baseline, err := s.executeScopedEvaluationArm(ctx, run, cases, model.EvaluationArmBaseline, protocol)
	if err != nil {
		return s.failScopedEvaluationRun(ctx, run, err)
	}
	experiment, err := s.executeScopedEvaluationArm(ctx, run, cases, model.EvaluationArmExperiment, protocol)
	if err != nil {
		return s.failScopedEvaluationRun(ctx, run, err)
	}

	gateMetrics, gateStatus, gateReason := scopedEvaluationGate(baseline, experiment, protocol)
	pass := gateStatus == model.EvaluationGatePass
	if err := s.Store.CompleteEvaluationRunWithGate(ctx, tenantID, run.ID, model.EvaluationRunCompleted, gateMetrics, &pass, gateStatus, gateReason); err != nil {
		return nil, err
	}
	return s.Store.GetEvaluationRun(ctx, tenantID, run.ID)
}

type evaluationRunWorker struct {
	svc *Service
}

func (worker *evaluationRunWorker) Kind() string { return model.JobKindEvaluationRun }

func (worker *evaluationRunWorker) Run(ctx context.Context, job *model.Job) error {
	var payload struct {
		EvaluationRunID string `json:"evaluation_run_id"`
	}
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil || payload.EvaluationRunID == "" {
		return fmt.Errorf("invalid evaluation run payload")
	}
	if _, err := worker.svc.ExecuteScopedEvaluationRun(ctx, job.TenantID, payload.EvaluationRunID); err != nil {
		return err
	}
	return nil
}

func (s *Service) enqueueScopedEvaluationRun(ctx context.Context, run *model.EvaluationRun) error {
	if run == nil || run.EvaluationScope == model.EvaluationScopeRelease || run.ExecutionMode != "paired_ab" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"evaluation_run_id": run.ID})
	if err != nil {
		return httperr.Internal("encode evaluation run payload failed")
	}
	if _, err := s.Store.EnqueueJob(ctx, &model.Job{
		Kind: model.JobKindEvaluationRun, Key: "evaluation_run:" + run.ID,
		TenantID: run.TenantID, Payload: string(payload),
	}); err != nil {
		return httperr.Internal("enqueue evaluation run failed")
	}
	return nil
}

func parseScopedEvaluationProtocol(raw string) (scopedEvaluationProtocol, error) {
	protocol := scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersion, TopK: 10, MinSamples: 20,
		MinHitLiftPP: 2, MaxP95LatencyDeltaMS: 300, MaxNoAnswerLiftPP: 0,
		AnswerCorrectnessMethod: "expected_evidence_match", RequireCitationPrecision: true,
	}
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return protocol, nil
	}
	if err := json.Unmarshal([]byte(raw), &protocol); err != nil {
		return protocol, httperr.BadRequest(40072, "invalid scoped evaluation protocol")
	}
	if protocol.ContractVersion != scopedEvaluationContractVersion &&
		protocol.ContractVersion != scopedEvaluationContractVersionV2 &&
		protocol.ContractVersion != scopedEvaluationContractVersionV3 {
		return protocol, httperr.BadRequest(40072, "unsupported scoped evaluation contract")
	}
	if protocol.ContractVersion == scopedEvaluationContractVersionV2 &&
		(protocol.MinCompiledArtifactPairs <= 0 || protocol.MinCompiledArtifactPairs > protocol.MinSamples) {
		return protocol, httperr.BadRequest(40072, "scoped evaluation compiled artifact minimum is invalid")
	}
	if protocol.ContractVersion == scopedEvaluationContractVersion && protocol.MinCompiledArtifactPairs != 0 {
		return protocol, httperr.BadRequest(40072, "compiled artifact diagnostics require protocol version 2")
	}
	if protocol.ContractVersion == scopedEvaluationContractVersionV3 {
		if protocol.PrimaryMetric != "citation_precision" {
			return protocol, httperr.BadRequest(40072, "scoped evaluation v3 primary metric must be citation_precision")
		}
		if protocol.MinHitLiftPP != 0 {
			return protocol, httperr.BadRequest(40072, "scoped evaluation v3 uses citation precision as its primary metric")
		}
		if protocol.MinCitationLiftPP <= 0 || protocol.MinCitationLiftPP > 100 ||
			protocol.MinCompiledEffectPairs <= 0 || protocol.MinCompiledEffectPairs > protocol.MinSamples {
			return protocol, httperr.BadRequest(40072, "scoped evaluation v3 thresholds are invalid")
		}
		if protocol.MinCompiledArtifactPairs != 0 {
			return protocol, httperr.BadRequest(40072, "compiled artifact effect requires protocol version 3 diagnostics")
		}
	} else if protocol.PrimaryMetric != "" || protocol.MinCitationLiftPP != 0 || protocol.MinCompiledEffectPairs != 0 {
		return protocol, httperr.BadRequest(40072, "v3 metrics require protocol version 3")
	}
	if protocol.TopK <= 0 || protocol.TopK > 100 {
		return protocol, httperr.BadRequest(40072, "scoped evaluation top_k must be between 1 and 100")
	}
	if protocol.MinSamples <= 0 || protocol.MinSamples > 10000 {
		return protocol, httperr.BadRequest(40072, "scoped evaluation sample size is invalid")
	}
	if protocol.MinHitLiftPP < 0 || protocol.MinHitLiftPP > 100 ||
		protocol.MaxNoAnswerLiftPP < 0 || protocol.MaxNoAnswerLiftPP > 100 ||
		protocol.MaxP95LatencyDeltaMS < 0 || protocol.AnswerCorrectnessMethod != "expected_evidence_match" {
		return protocol, httperr.BadRequest(40072, "scoped evaluation thresholds are invalid")
	}
	if protocol.RequireTaskTypeCoverage {
		if len(protocol.RequiredTaskTypes) == 0 {
			protocol.RequiredTaskTypes = append([]string{}, memoryEvaluationTaskTypes...)
		}
		seenTypes := map[string]bool{}
		for _, taskType := range protocol.RequiredTaskTypes {
			if !isValidMemoryEvaluationTaskType(taskType) || seenTypes[taskType] {
				return protocol, httperr.BadRequest(40072, "scoped evaluation task types are invalid")
			}
			seenTypes[taskType] = true
		}
		if protocol.MinTaskTypeSamples <= 0 || protocol.MinTaskTypeSamples > protocol.MinSamples {
			return protocol, httperr.BadRequest(40072, "scoped evaluation task type sample minimum is invalid")
		}
	} else if len(protocol.RequiredTaskTypes) > 0 || protocol.MinTaskTypeSamples != 0 {
		return protocol, httperr.BadRequest(40072, "task type coverage must be enabled to configure task type thresholds")
	}
	return protocol, nil
}

func validateScopedEvaluationCases(
	cases []model.EvaluationCaseVersion, run *model.EvaluationRun, protocol scopedEvaluationProtocol,
) error {
	pairs := map[string]scopedEvaluationSample{}
	seenCases := map[string]bool{}
	for _, item := range cases {
		if item.CaseID == "" || seenCases[item.CaseID] {
			return httperr.BadRequest(40073, "evaluation case IDs must be unique")
		}
		seenCases[item.CaseID] = true
		if strings.TrimSpace(item.Question) == "" {
			return httperr.BadRequest(40073, "evaluation case question is required")
		}
		if item.PairedSampleID == "" {
			return httperr.BadRequest(40073, "paired_sample_id is required")
		}
		if run.EvaluationScope == model.EvaluationScopeCompilationStrategy {
			if capabilityFromScopedCase(item) != run.Capability {
				return httperr.BadRequest(40073, "evaluation case capability does not match run")
			}
		}
		taskType := scopedTaskType(item)
		if run.EvaluationScope == model.EvaluationScopeMemoryPolicy && !isValidMemoryEvaluationTaskType(taskType) {
			return httperr.BadRequest(40073, "memory evaluation task type is invalid")
		}
		hasExpectedEvidence := len(expectedScopedDocIDs(item)) > 0 || expectedScopedKeywords(item) != "" ||
			strings.TrimSpace(item.ExpectedAnswer) != ""
		if run.EvaluationScope == model.EvaluationScopeMemoryPolicy {
			if isNegativeMemoryEvaluationTaskType(taskType) && hasExpectedEvidence {
				return httperr.BadRequest(40073, "negative memory evaluation case must not declare expected evidence")
			}
			if !isNegativeMemoryEvaluationTaskType(taskType) && !hasExpectedEvidence {
				return httperr.BadRequest(40073, "positive memory evaluation case requires expected evidence")
			}
		} else if !hasExpectedEvidence {
			return httperr.BadRequest(40073, "evaluation case requires expected evidence")
		}
		if run.EvaluationScope == model.EvaluationScopeCompilationStrategy && len(expectedScopedDocIDs(item)) == 0 {
			return httperr.BadRequest(40073, "compilation evaluation case requires expected citation document IDs")
		}
		sample := pairs[item.PairedSampleID]
		if sample.cases == nil {
			sample.cases = map[string]model.EvaluationCaseVersion{}
		}
		if _, exists := sample.cases[item.CaseID]; exists {
			return httperr.BadRequest(40073, "paired sample case conflict")
		}
		sample.cases[item.CaseID] = item
		pairs[item.PairedSampleID] = sample
	}
	if len(pairs) < protocol.MinSamples {
		return nil
	}
	if protocol.RequireTaskTypeCoverage {
		typeCounts := map[string]int{}
		for _, item := range cases {
			typeCounts[scopedTaskType(item)]++
		}
		for _, taskType := range protocol.RequiredTaskTypes {
			if typeCounts[taskType] < protocol.MinTaskTypeSamples {
				return nil
			}
		}
	}
	return nil
}

func capabilityFromScopedCase(item model.EvaluationCaseVersion) string {
	var metadata struct {
		Capability string `json:"capability"`
	}
	if item.Metadata != "" {
		_ = json.Unmarshal([]byte(item.Metadata), &metadata)
	}
	return metadata.Capability
}

func expectedScopedDocIDs(item model.EvaluationCaseVersion) []string {
	fields := strings.FieldsFunc(item.ExpectedCitationDocIDs, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	values := make([]string, 0, len(fields))
	for _, value := range fields {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func expectedScopedKeywords(item model.EvaluationCaseVersion) string {
	return strings.TrimSpace(strings.Join(strings.FieldsFunc(item.ExpectedKeywords, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	}), "\x1f"))
}

type scopedCaseMetadata struct {
	Capability        string   `json:"capability"`
	TaskType          string   `json:"task_type"`
	ForbiddenKeywords []string `json:"forbidden_keywords"`
}

var memoryEvaluationTaskTypes = []string{
	"preference", "project_state", "cross_project", "conflict", "boundary",
}

func scopedTaskType(item model.EvaluationCaseVersion) string {
	var metadata scopedCaseMetadata
	if item.Metadata != "" {
		_ = json.Unmarshal([]byte(item.Metadata), &metadata)
	}
	if strings.TrimSpace(metadata.TaskType) == "" {
		return "unclassified"
	}
	return strings.TrimSpace(metadata.TaskType)
}

func isValidMemoryEvaluationTaskType(taskType string) bool {
	switch taskType {
	case "preference", "project_state", "cross_project", "conflict", "boundary":
		return true
	default:
		return false
	}
}

func isNegativeMemoryEvaluationTaskType(taskType string) bool {
	return taskType == "boundary"
}

func (s *Service) executeScopedEvaluationArm(
	ctx context.Context, run *model.EvaluationRun, cases []model.EvaluationCaseVersion,
	arm string, protocol scopedEvaluationProtocol,
) (map[string]model.EvaluationCaseResult, error) {
	results := map[string]model.EvaluationCaseResult{}
	switch run.EvaluationScope {
	case model.EvaluationScopeCompilationStrategy:
		strategy, err := s.Store.GetKnowledgeStrategy(ctx, run.TenantID, scopedStringValue(run.XStrategyID))
		if err != nil || strategy == nil {
			return nil, httperr.BadRequest(40072, "knowledge strategy not found")
		}
		dataset, err := s.Store.GetDatasetLink(ctx, run.TenantID, strategy.DatasetID)
		if err != nil || dataset == nil || dataset.RAGFlowDatasetID == "" {
			return nil, httperr.BadRequest(40072, "strategy dataset is unavailable")
		}
		config, err := parseKnowledgeStrategyConfig(strategy.Config)
		if err != nil {
			return nil, err
		}
		var provenance *compiledArtifactProvenanceIndex
		if protocol.ContractVersion == scopedEvaluationContractVersionV3 &&
			isSearchDatasetCompilationCapability(run.Capability) {
			provenance, err = s.buildCompiledArtifactProvenance(ctx, dataset.RAGFlowDatasetID)
			if err != nil {
				return nil, err
			}
		}
		for _, item := range cases {
			startedAt := time.Now()
			if isSearchDatasetCompilationCapability(run.Capability) {
				response, searchErr := s.RAGFlow.SearchDataset(ctx, dataset.RAGFlowDatasetID, ragflow.SearchDatasetRequest{
					Question:                    item.Question,
					DocumentIDs:                 append([]string{}, config.DocumentIDs...),
					Page:                        1,
					Size:                        protocol.TopK,
					TopK:                        scopedRetrievalCandidateWindow(protocol),
					IncludeKnowledgeCompilation: arm == model.EvaluationArmExperiment,
					Keyword:                     config.Keyword,
				})
				if searchErr != nil {
					return nil, httperr.New(502, 50280, "scoped evaluation retrieval failed")
				}
				if response == nil {
					response = &ragflow.SearchDatasetResult{}
				}
				result, encodeErr := scopedRetrievalResult(run, item, arm, response.Chunks, protocol, startedAt, provenance)
				if encodeErr != nil {
					return nil, encodeErr
				}
				if err := s.Store.CreateEvaluationCaseResult(ctx, result); err != nil {
					return nil, httperr.New(409, 40985, "scoped evaluation result already exists")
				}
				results[item.CaseID] = *result
				continue
			}
			request := ragflow.RetrieveDatasetsRequest{
				DatasetIDs:                  []string{dataset.RAGFlowDatasetID},
				Question:                    item.Question,
				DocumentIDs:                 append([]string{}, config.DocumentIDs...),
				IncludeKnowledgeCompilation: arm == model.EvaluationArmExperiment,
				Page:                        1, PageSize: protocol.TopK,
				Keyword: config.Keyword, RerankID: config.RerankID,
			}
			if len(config.StructuredFilters) > 0 {
				condition := structuredFiltersCondition(config.StructuredFilters)
				request.MetadataCondition = condition
			}
			response, retrievalErr := s.RAGFlow.RetrieveDatasets(ctx, request)
			if retrievalErr != nil {
				return nil, httperr.New(502, 50280, "scoped evaluation retrieval failed")
			}
			if response == nil {
				response = &ragflow.RetrieveDatasetsResult{}
			}
			result, encodeErr := scopedRetrievalResult(run, item, arm, response.Chunks, protocol, startedAt, nil)
			if encodeErr != nil {
				return nil, encodeErr
			}
			if err := s.Store.CreateEvaluationCaseResult(ctx, result); err != nil {
				return nil, httperr.New(409, 40985, "scoped evaluation result already exists")
			}
			results[item.CaseID] = *result
		}
	case model.EvaluationScopeMemoryPolicy:
		policy, err := s.Store.GetMemoryPolicy(ctx, run.TenantID, scopedStringValue(run.XMemoryPolicyID))
		if err != nil || policy == nil {
			return nil, httperr.BadRequest(40072, "memory policy not found")
		}
		if arm != model.EvaluationArmExperiment {
			for _, item := range cases {
				result, encodeErr := scopedEmptyResult(run, item, arm)
				if encodeErr != nil {
					return nil, encodeErr
				}
				if err := s.Store.CreateEvaluationCaseResult(ctx, result); err != nil {
					return nil, httperr.New(409, 40985, "scoped evaluation result already exists")
				}
				results[item.CaseID] = *result
			}
			return results, nil
		}
		for _, item := range cases {
			startedAt := time.Now()
			search, searchErr := s.SearchGovernedMemory(ctx, run.TenantID, run.Actor, policy.RagflowMemoryID, GovernedMemorySearchInput{
				MemoryID: policy.RagflowMemoryID, Query: item.Question,
				AssistantID: policy.AssistantID, SessionID: "evaluation-run-" + run.ID,
				RequestID: scopedEvaluationRequestID(run.ID, item.CaseID, arm), TopN: protocol.TopK,
			})
			if searchErr != nil {
				return nil, httperr.New(502, 50280, "scoped evaluation memory retrieval failed")
			}
			result, encodeErr := scopedMemoryResult(run, item, arm, search.Messages, protocol, startedAt)
			if encodeErr != nil {
				return nil, encodeErr
			}
			if err := s.Store.CreateEvaluationCaseResult(ctx, result); err != nil {
				return nil, httperr.New(409, 40985, "scoped evaluation result already exists")
			}
			results[item.CaseID] = *result
		}
	default:
		return nil, httperr.BadRequest(40072, "unsupported scoped evaluation scope")
	}
	return results, nil
}

func scopedRetrievalResult(
	run *model.EvaluationRun, item model.EvaluationCaseVersion, arm string,
	chunks []map[string]interface{}, protocol scopedEvaluationProtocol, startedAt time.Time,
	provenance *compiledArtifactProvenanceIndex,
) (*model.EvaluationCaseResult, error) {
	docIDs := make([]string, 0, len(chunks))
	contents := make([]string, 0, len(chunks))
	compiledChunkCount := 0
	compiledDerivedChunkCount := 0
	rawChunkCount := 0
	references := make([]map[string]interface{}, 0, len(chunks))
	for _, chunk := range chunks {
		if documentID := stringFromAny(chunk["document_id"]); documentID != "" {
			docIDs = append(docIDs, documentID)
		}
		if content := stringFromAny(chunk["content"]); content != "" {
			contents = append(contents, content)
		}
		compileKeyword := compiledArtifactKeyword(chunk)
		artifactProvenance := compiledArtifactForChunk(chunk, provenance)
		source := compiledArtifactSourceRaw
		switch {
		case compileKeyword != "" || artifactProvenance.Direct:
			source = compiledArtifactSourceCompiled
			compiledChunkCount++
		case artifactProvenance.Derived:
			source = compiledArtifactSourceDerived
			compiledDerivedChunkCount++
			rawChunkCount++
		default:
			rawChunkCount++
		}
		references = append(references, scopedRetrievalReference(chunk, source, compileKeyword, artifactProvenance))
	}
	hit, answerCorrect, precision := scopedEvidenceMetrics(item, docIDs, contents)
	noAnswer := len(docIDs) == 0
	refs, err := json.Marshal(references)
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation references")
	}
	metrics, err := json.Marshal(map[string]interface{}{
		"hit_at_k": hit, "answer_correct": answerCorrect, "no_answer": noAnswer,
		"citation_precision": precision, "task_type": scopedTaskType(item),
		"returned_count": len(chunks), "compiled_chunk_count": compiledChunkCount,
		"compiled_derived_chunk_count": compiledDerivedChunkCount, "raw_chunk_count": rawChunkCount,
	})
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation metrics")
	}
	pass := hit
	latency := time.Since(startedAt).Milliseconds()
	return &model.EvaluationCaseResult{
		ID: id.New(), TenantID: run.TenantID, RunID: run.ID, CaseID: item.CaseID,
		CaseVersionID: item.ID, CaseVersionHash: item.Hash, PairedSampleID: item.PairedSampleID,
		ExperimentArm: arm, References: string(refs), Metrics: string(metrics), Pass: &pass,
		HitAtK: &hit, AnswerCorrect: &answerCorrect, NoAnswer: &noAnswer,
		CitationPrecision: &precision, LatencyMs: latency, CreatedAt: time.Now().UTC(),
	}, nil
}

func scopedEmptyResult(
	run *model.EvaluationRun, item model.EvaluationCaseVersion, arm string,
) (*model.EvaluationCaseResult, error) {
	refs, err := json.Marshal([]interface{}{})
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation references")
	}
	metrics, err := json.Marshal(map[string]interface{}{
		"hit_at_k": false, "answer_correct": false, "no_answer": true,
		"task_type": scopedTaskType(item),
	})
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation metrics")
	}
	pass := false
	hit, answerCorrect, noAnswer := false, false, true
	return &model.EvaluationCaseResult{
		ID: id.New(), TenantID: run.TenantID, RunID: run.ID, CaseID: item.CaseID,
		CaseVersionID: item.ID, CaseVersionHash: item.Hash, PairedSampleID: item.PairedSampleID,
		ExperimentArm: arm, References: string(refs), Metrics: string(metrics), Pass: &pass,
		HitAtK: &hit, AnswerCorrect: &answerCorrect, NoAnswer: &noAnswer,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func scopedMemoryResult(
	run *model.EvaluationRun, item model.EvaluationCaseVersion, arm string,
	messages []ragflow.MemoryMessage, protocol scopedEvaluationProtocol, startedAt time.Time,
) (*model.EvaluationCaseResult, error) {
	docIDs := make([]string, 0, len(messages))
	contents := make([]string, 0, len(messages))
	refs := make([]map[string]interface{}, 0, len(messages))
	for _, message := range messages {
		docIDs = append(docIDs, fmt.Sprintf("message:%d", message.MessageID))
		content := message.Content
		if content == "" {
			content = message.AgentResponse
		}
		if content != "" {
			contents = append(contents, content)
		}
		refs = append(refs, map[string]interface{}{"message_id": message.MessageID})
	}
	hit, answerCorrect, _ := scopedEvidenceMetrics(item, docIDs, contents)
	noAnswer := len(contents) == 0
	encodedRefs, err := json.Marshal(refs)
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation references")
	}
	metrics, err := json.Marshal(map[string]interface{}{
		"hit_at_k": hit, "answer_correct": answerCorrect, "no_answer": noAnswer,
		"citation_precision": nil, "task_type": scopedTaskType(item),
	})
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation metrics")
	}
	pass := hit
	latency := time.Since(startedAt).Milliseconds()
	return &model.EvaluationCaseResult{
		ID: id.New(), TenantID: run.TenantID, RunID: run.ID, CaseID: item.CaseID,
		CaseVersionID: item.ID, CaseVersionHash: item.Hash, PairedSampleID: item.PairedSampleID,
		ExperimentArm: arm, References: string(encodedRefs), Metrics: string(metrics), Pass: &pass,
		HitAtK: &hit, AnswerCorrect: &answerCorrect, NoAnswer: &noAnswer,
		LatencyMs: latency, CreatedAt: time.Now().UTC(),
	}, nil
}

func scopedEvidenceMetrics(
	item model.EvaluationCaseVersion, returnedIDs, contents []string,
) (hit, answerCorrect bool, citationPrecision float64) {
	expectedIDs := expectedScopedDocIDs(item)
	expectedKeywords := strings.Split(expectedScopedKeywords(item), "\x1f")
	if len(expectedKeywords) == 1 && expectedKeywords[0] == "" {
		expectedKeywords = nil
	}
	expectedAnswer := strings.TrimSpace(item.ExpectedAnswer)
	var metadata scopedCaseMetadata
	if item.Metadata != "" {
		_ = json.Unmarshal([]byte(item.Metadata), &metadata)
	}

	contentBlob := strings.ToLower(strings.Join(contents, "\n"))
	for _, keyword := range metadata.ForbiddenKeywords {
		if strings.TrimSpace(keyword) != "" && strings.Contains(contentBlob, strings.ToLower(keyword)) {
			return false, false, 0
		}
	}
	idSet := map[string]bool{}
	for _, value := range returnedIDs {
		idSet[value] = true
	}
	keywordHit := len(expectedKeywords) > 0
	for _, keyword := range expectedKeywords {
		if keyword != "" && !strings.Contains(contentBlob, strings.ToLower(keyword)) {
			keywordHit = false
			break
		}
	}
	answerHit := expectedAnswer != "" && strings.Contains(contentBlob, strings.ToLower(expectedAnswer))
	answerCorrect = answerHit || keywordHit

	idHit := false
	for _, value := range expectedIDs {
		if idSet[value] {
			idHit = true
			break
		}
	}
	hit = idHit || answerCorrect
	if len(returnedIDs) == 0 || len(expectedIDs) == 0 {
		citationPrecision = 0
		return hit, answerCorrect, citationPrecision
	}
	matched := 0
	for _, value := range returnedIDs {
		if idSet[value] && containsScopedValue(expectedIDs, value) {
			matched++
		}
	}
	citationPrecision = float64(matched) / float64(len(returnedIDs))
	return hit, answerCorrect, citationPrecision
}

func containsScopedValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func scopedRetrievalReference(
	chunk map[string]interface{}, source, compileKeyword string,
	artifactProvenance compiledArtifactProvenance,
) map[string]interface{} {
	reference := map[string]interface{}{"source": source}
	if value := stringFromAny(chunk["id"]); value != "" {
		reference["chunk_id"] = value
	}
	if value := stringFromAny(chunk["document_id"]); value != "" {
		reference["document_id"] = value
	}
	if compileKeyword != "" {
		reference["compile_kwd"] = compileKeyword
	}
	if source != compiledArtifactSourceRaw {
		reference["compiled_provenance"] = map[string]interface{}{
			"match_type":      artifactProvenance.MatchType,
			"artifact_id":     artifactProvenance.ArtifactID,
			"page_type":       artifactProvenance.PageType,
			"title":           artifactProvenance.Title,
			"source_chunk_id": artifactProvenance.SourceChunkID,
			"source_doc_ids":  artifactProvenance.SourceDocIDs,
		}
	}
	if artifactProvenance.ArtifactID != "" {
		reference["compiled_artifact_id"] = artifactProvenance.ArtifactID
		reference["compiled_artifact_page_type"] = artifactProvenance.PageType
	}
	if artifactProvenance.SourceChunkID != "" {
		reference["compiled_source_chunk_id"] = artifactProvenance.SourceChunkID
	}
	return reference
}

func scopedRetrievalCandidateWindow(protocol scopedEvaluationProtocol) int {
	if protocol.ContractVersion == scopedEvaluationContractVersionV3 {
		return 64
	}
	return protocol.TopK
}

type compiledArtifactProvenance struct {
	ArtifactID    string
	PageType      string
	Title         string
	SourceChunkID string
	SourceDocIDs  []string
	MatchType     string
	Direct        bool
	Derived       bool
}

type compiledArtifactProvenanceIndex struct {
	byContent     map[string]compiledArtifactProvenance
	bySourceChunk map[string]compiledArtifactProvenance
}

func (s *Service) buildCompiledArtifactProvenance(
	ctx context.Context, datasetID string,
) (*compiledArtifactProvenanceIndex, error) {
	index := &compiledArtifactProvenanceIndex{
		byContent:     map[string]compiledArtifactProvenance{},
		bySourceChunk: map[string]compiledArtifactProvenance{},
	}
	const pageSize = 100
	for page := 1; page <= 10; page++ {
		artifacts, total, err := s.RAGFlow.ListDatasetArtifacts(ctx, datasetID, ragflow.DatasetArtifactFilter{
			Page: page, PageSize: pageSize,
		})
		if err != nil {
			return nil, httperr.New(502, 50281, "scoped evaluation artifact provenance unavailable")
		}
		for _, artifact := range artifacts {
			detail, detailErr := s.RAGFlow.GetDatasetArtifact(ctx, datasetID, artifact.PageType, artifact.Slug)
			if detailErr != nil {
				return nil, httperr.New(502, 50281, "scoped evaluation artifact provenance unavailable")
			}
			if detail == nil {
				continue
			}
			digest := sha256.Sum256([]byte(detail.ContentMDRendered))
			provenance := compiledArtifactProvenance{
				ArtifactID: detail.Slug, PageType: detail.PageType, Title: detail.Title,
				SourceDocIDs: append([]string{}, detail.SourceDocIDs...), Direct: true,
			}
			if detail.ContentMDRendered != "" {
				index.byContent[hex.EncodeToString(digest[:])] = provenance
			}
			for _, sourceChunkID := range detail.SourceChunkIDs {
				if strings.TrimSpace(sourceChunkID) == "" {
					continue
				}
				derived := provenance
				derived.Direct = false
				derived.Derived = true
				derived.SourceChunkID = sourceChunkID
				if _, exists := index.bySourceChunk[sourceChunkID]; !exists {
					index.bySourceChunk[sourceChunkID] = derived
				}
			}
		}
		if int64(page*pageSize) >= total || len(artifacts) == 0 {
			break
		}
	}
	return index, nil
}

func compiledArtifactForChunk(
	chunk map[string]interface{}, index *compiledArtifactProvenanceIndex,
) compiledArtifactProvenance {
	if index == nil {
		return compiledArtifactProvenance{}
	}
	if keyword := compiledArtifactKeyword(chunk); keyword != "" {
		return compiledArtifactProvenance{
			ArtifactID: keyword, MatchType: "compile_kwd", Direct: true,
		}
	}
	digest := sha256.Sum256([]byte(stringFromAny(chunk["content"])))
	if provenance, exists := index.byContent[hex.EncodeToString(digest[:])]; exists {
		provenance.MatchType = "content_sha256"
		return provenance
	}
	chunkID := strings.TrimSpace(stringFromAny(chunk["id"]))
	if provenance, exists := index.bySourceChunk[chunkID]; exists {
		provenance.MatchType = "source_chunk_id"
		return provenance
	}
	return compiledArtifactProvenance{}
}

func compiledArtifactKeyword(chunk map[string]interface{}) string {
	switch value := chunk["compile_kwd"].(type) {
	case string:
		return strings.TrimSpace(value)
	case []interface{}:
		for _, item := range value {
			if keyword := strings.TrimSpace(stringFromAny(item)); keyword != "" {
				return keyword
			}
		}
	}
	return ""
}

func scopedEvaluationRequestID(runID, caseID, arm string) string {
	value := "evaluation:" + runID + ":" + caseID + ":" + arm
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

func scopedEvaluationGate(
	baseline, experiment map[string]model.EvaluationCaseResult, protocol scopedEvaluationProtocol,
) (string, string, string) {
	pairs := map[string]bool{}
	for caseID := range baseline {
		pairs[caseID] = true
	}
	for caseID := range experiment {
		pairs[caseID] = true
	}
	sampleCount := len(pairs)
	observations := map[string]*scopedTaskObservation{}
	for caseID := range pairs {
		base, experimentExists := experiment[caseID]
		baseResult, baselineExists := baseline[caseID]
		if !baselineExists || !experimentExists {
			return metricsJSON(protocol, map[string]interface{}{"pair_count": sampleCount}), model.EvaluationGateWait, "paired sample is incomplete"
		}
		baseTaskType, experimentTaskType := resultTaskType(baseResult), resultTaskType(base)
		if baseTaskType != experimentTaskType {
			return metricsJSON(protocol, map[string]interface{}{"pair_count": sampleCount}), model.EvaluationGateWait, "paired sample task types are inconsistent"
		}
		observation := observations[baseTaskType]
		if observation == nil {
			observation = &scopedTaskObservation{}
			observations[baseTaskType] = observation
		}
		observation.add(baseResult, base)
	}

	positiveHitDiffs, positiveNoAnswerBaseline, positiveNoAnswerExperiment := positiveHitAndNoAnswer(observations)
	positiveCitationBaseline, positiveCitationExperiment, positiveLatencyBaseline, positiveLatencyExperiment := positiveEvidence(observations)
	hitRateBaseline, hitRateExperiment := positiveRates(observations, true)
	answerRateBaseline, answerRateExperiment := positiveRates(observations, false)
	noAnswerRateBaseline, noAnswerRateExperiment := mean(positiveNoAnswerBaseline), mean(positiveNoAnswerExperiment)
	citationPrecisionBaseline, citationPrecisionExperiment := mean(positiveCitationBaseline), mean(positiveCitationExperiment)
	p95Baseline, p95Experiment := percentile95(positiveLatencyBaseline), percentile95(positiveLatencyExperiment)
	lower, upper := pairedDifferenceInterval(positiveHitDiffs)
	hitLiftPP := (hitRateExperiment - hitRateBaseline) * 100
	taskTypeMetrics := map[string]interface{}{}
	for _, taskType := range scopedTaskTypeOrder() {
		observation := observations[taskType]
		if observation == nil {
			continue
		}
		taskTypeMetrics[taskType] = observation.metrics(isNegativeMemoryEvaluationTaskType(taskType))
	}
	compiledArtifactPairs := 0
	baselineCompiledArtifactPairs := 0
	compiledDerivedArtifactPairs := 0
	compiledArtifactEffectPairs := 0
	citationDiffs := make([]float64, 0, sampleCount)
	pairDiagnostics := make([]map[string]interface{}, 0, sampleCount)
	for _, caseID := range sortedMapKeys(pairs) {
		baselineResult, experimentResult := baseline[caseID], experiment[caseID]
		baselineDiagnostics := artifactDiagnostics(baselineResult)
		experimentDiagnostics := artifactDiagnostics(experimentResult)
		if experimentDiagnostics.CompiledCount > 0 {
			compiledArtifactPairs++
		}
		if baselineDiagnostics.CompiledCount > 0 {
			baselineCompiledArtifactPairs++
		}
		if experimentDiagnostics.CompiledDerivedCount > 0 {
			compiledDerivedArtifactPairs++
		}
		experimentArtifactCount := experimentDiagnostics.CompiledCount + experimentDiagnostics.CompiledDerivedCount
		baselineArtifactCount := baselineDiagnostics.CompiledCount + baselineDiagnostics.CompiledDerivedCount
		if experimentArtifactCount > baselineArtifactCount {
			compiledArtifactEffectPairs++
		}
		if baselineResult.CitationPrecision != nil && experimentResult.CitationPrecision != nil {
			citationDiffs = append(citationDiffs, *experimentResult.CitationPrecision-*baselineResult.CitationPrecision)
		}
		pairDiagnostics = append(pairDiagnostics, map[string]interface{}{
			"case_id":          caseID,
			"paired_sample_id": baselineResult.PairedSampleID,
			"baseline":         artifactDiagnosticPayload(baselineResult, baselineDiagnostics),
			"experiment":       artifactDiagnosticPayload(experimentResult, experimentDiagnostics),
		})
	}
	compiledArtifactRate := 0.0
	if sampleCount > 0 {
		compiledArtifactRate = float64(compiledArtifactPairs) / float64(sampleCount)
	}
	citationLiftPP := (citationPrecisionExperiment - citationPrecisionBaseline) * 100
	citationLower, citationUpper := pairedDifferenceInterval(citationDiffs)
	encoded, encodeErr := json.Marshal(map[string]interface{}{
		"contract_version": protocol.ContractVersion, "pair_count": sampleCount,
		"positive_pair_count": len(positiveHitDiffs),
		"hit_rate":            map[string]float64{"baseline": safeMetric(hitRateBaseline), "experiment": safeMetric(hitRateExperiment)},
		"hit_lift_pp":         safeMetric(hitLiftPP), "hit_lift_ci95_pp": map[string]float64{"lower": safeMetric(lower * 100), "upper": safeMetric(upper * 100)},
		"answer_correct_rate":                   map[string]float64{"baseline": safeMetric(answerRateBaseline), "experiment": safeMetric(answerRateExperiment)},
		"answer_correctness_method":             protocol.AnswerCorrectnessMethod,
		"no_answer_rate":                        map[string]float64{"baseline": safeMetric(noAnswerRateBaseline), "experiment": safeMetric(noAnswerRateExperiment)},
		"citation_precision":                    map[string]float64{"baseline": safeMetric(citationPrecisionBaseline), "experiment": safeMetric(citationPrecisionExperiment)},
		"p95_latency_ms":                        map[string]int64{"baseline": p95Baseline, "experiment": p95Experiment},
		"p95_latency_delta_ms":                  p95Experiment - p95Baseline,
		"task_types":                            taskTypeMetrics,
		"compiled_artifact_pair_count":          compiledArtifactPairs,
		"baseline_compiled_artifact_pair_count": baselineCompiledArtifactPairs,
		"compiled_artifact_pair_rate":           safeMetric(compiledArtifactRate),
		"compiled_derived_artifact_pair_count":  compiledDerivedArtifactPairs,
		"compiled_artifact_effect_pair_count":   compiledArtifactEffectPairs,
		"citation_precision_lift_pp":            safeMetric(citationLiftPP),
		"citation_precision_lift_ci95_pp":       map[string]float64{"lower": safeMetric(citationLower * 100), "upper": safeMetric(citationUpper * 100)},
		"pair_diagnostics":                      pairDiagnostics,
	})
	if encodeErr != nil {
		return "", model.EvaluationGateFail, "gate metrics encoding failed: " + encodeErr.Error()
	}
	if sampleCount < protocol.MinSamples {
		return string(encoded), model.EvaluationGateWait, "paired sample count is below the pre-registered minimum"
	}
	if protocol.RequireTaskTypeCoverage {
		for _, taskType := range protocol.RequiredTaskTypes {
			observation := observations[taskType]
			if observation == nil || observation.PairCount < protocol.MinTaskTypeSamples {
				return string(encoded), model.EvaluationGateWait, "task type coverage is below the pre-registered minimum"
			}
		}
		for _, taskType := range protocol.RequiredTaskTypes {
			observation := observations[taskType]
			if isNegativeMemoryEvaluationTaskType(taskType) {
				if mean(observation.hitExperiment) > mean(observation.hitBaseline) {
					return string(encoded), model.EvaluationGateFail, "boundary or no-answer task produced a false hit"
				}
				continue
			}
			if (mean(observation.hitExperiment)-mean(observation.hitBaseline))*100 < protocol.MinHitLiftPP ||
				mean(observation.answerExperiment) < mean(observation.answerBaseline) ||
				mean(observation.noAnswerExperiment) > mean(observation.noAnswerBaseline)+protocol.MaxNoAnswerLiftPP/100 ||
				(protocol.RequireCitationPrecision && len(observation.citationBaseline) > 0 &&
					mean(observation.citationExperiment) < mean(observation.citationBaseline)) ||
				percentile95(observation.latencyExperiment)-percentile95(observation.latencyBaseline) > protocol.MaxP95LatencyDeltaMS {
				return string(encoded), model.EvaluationGateFail, "task type quality, citation, no-answer or latency budget degraded"
			}
		}
	}
	if compiledArtifactPairs < protocol.MinCompiledArtifactPairs {
		return string(encoded), model.EvaluationGateWait, compiledArtifactParticipationError
	}
	if compiledArtifactEffectPairs < protocol.MinCompiledEffectPairs {
		return string(encoded), model.EvaluationGateWait, compiledArtifactEffectError
	}
	if protocol.MinCitationLiftPP > 0 && citationLower*100 < protocol.MinCitationLiftPP {
		return string(encoded), model.EvaluationGateWait, "citation precision confidence interval is below the pre-registered lift"
	}
	if lower*100 < protocol.MinHitLiftPP {
		return string(encoded), model.EvaluationGateWait, "hit lift confidence interval is below the pre-registered lift"
	}
	if hitLiftPP < 0 || answerRateExperiment < answerRateBaseline ||
		noAnswerRateExperiment > noAnswerRateBaseline+protocol.MaxNoAnswerLiftPP/100 ||
		(protocol.RequireCitationPrecision && len(positiveCitationBaseline) > 0 &&
			citationPrecisionExperiment < citationPrecisionBaseline) ||
		p95Experiment-p95Baseline > protocol.MaxP95LatencyDeltaMS {
		return string(encoded), model.EvaluationGateFail, "quality, citation, no-answer or latency budget degraded"
	}
	reason := "pre-registered paired quality gate passed"
	if protocol.MinCitationLiftPP > 0 {
		reason = "pre-registered PageIndex citation precision gate passed"
	}
	return string(encoded), model.EvaluationGatePass, reason
}

type scopedTaskObservation struct {
	PairCount          int
	hitBaseline        []float64
	hitExperiment      []float64
	answerBaseline     []float64
	answerExperiment   []float64
	noAnswerBaseline   []float64
	noAnswerExperiment []float64
	citationBaseline   []float64
	citationExperiment []float64
	latencyBaseline    []int64
	latencyExperiment  []int64
	hitDiffs           []float64
}

type compiledArtifactDiagnostic struct {
	ReturnedCount        int
	CompiledCount        int
	CompiledDerivedCount int
	RawCount             int
}

func artifactDiagnostics(result model.EvaluationCaseResult) compiledArtifactDiagnostic {
	var metrics struct {
		ReturnedCount             int `json:"returned_count"`
		CompiledChunkCount        int `json:"compiled_chunk_count"`
		CompiledDerivedChunkCount int `json:"compiled_derived_chunk_count"`
		RawChunkCount             int `json:"raw_chunk_count"`
	}
	if result.Metrics != "" {
		_ = json.Unmarshal([]byte(result.Metrics), &metrics)
	}
	return compiledArtifactDiagnostic{
		ReturnedCount: metrics.ReturnedCount, CompiledCount: metrics.CompiledChunkCount,
		CompiledDerivedCount: metrics.CompiledDerivedChunkCount, RawCount: metrics.RawChunkCount,
	}
}

func artifactDiagnosticPayload(
	result model.EvaluationCaseResult, diagnostics compiledArtifactDiagnostic,
) map[string]interface{} {
	hit, answerCorrect, noAnswer := false, false, true
	precision := 0.0
	if result.HitAtK != nil {
		hit = *result.HitAtK
	}
	if result.AnswerCorrect != nil {
		answerCorrect = *result.AnswerCorrect
	}
	if result.NoAnswer != nil {
		noAnswer = *result.NoAnswer
	}
	if result.CitationPrecision != nil {
		precision = *result.CitationPrecision
	}
	return map[string]interface{}{
		"arm": result.ExperimentArm, "hit_at_k": hit, "answer_correct": answerCorrect,
		"no_answer": noAnswer, "citation_precision": precision, "latency_ms": result.LatencyMs,
		"returned_count": diagnostics.ReturnedCount, "compiled_chunk_count": diagnostics.CompiledCount,
		"compiled_derived_chunk_count": diagnostics.CompiledDerivedCount, "raw_chunk_count": diagnostics.RawCount,
	}
}

func (observation *scopedTaskObservation) add(baseline, experiment model.EvaluationCaseResult) {
	observation.PairCount++
	observation.hitBaseline = append(observation.hitBaseline, boolValue(baseline.HitAtK))
	observation.hitExperiment = append(observation.hitExperiment, boolValue(experiment.HitAtK))
	observation.answerBaseline = append(observation.answerBaseline, boolValue(baseline.AnswerCorrect))
	observation.answerExperiment = append(observation.answerExperiment, boolValue(experiment.AnswerCorrect))
	if baseline.NoAnswer != nil && experiment.NoAnswer != nil {
		observation.noAnswerBaseline = append(observation.noAnswerBaseline, boolValue(baseline.NoAnswer))
		observation.noAnswerExperiment = append(observation.noAnswerExperiment, boolValue(experiment.NoAnswer))
	}
	if baseline.CitationPrecision != nil && experiment.CitationPrecision != nil {
		observation.citationBaseline = append(observation.citationBaseline, *baseline.CitationPrecision)
		observation.citationExperiment = append(observation.citationExperiment, *experiment.CitationPrecision)
	}
	observation.latencyBaseline = append(observation.latencyBaseline, baseline.LatencyMs)
	observation.latencyExperiment = append(observation.latencyExperiment, experiment.LatencyMs)
	observation.hitDiffs = append(observation.hitDiffs, boolValue(experiment.HitAtK)-boolValue(baseline.HitAtK))
}

func (observation *scopedTaskObservation) metrics(negative bool) map[string]interface{} {
	return map[string]interface{}{
		"role":        map[bool]string{true: "negative", false: "positive"}[negative],
		"pair_count":  observation.PairCount,
		"hit_rate":    map[string]float64{"baseline": safeMetric(mean(observation.hitBaseline)), "experiment": safeMetric(mean(observation.hitExperiment))},
		"hit_lift_pp": safeMetric((mean(observation.hitExperiment) - mean(observation.hitBaseline)) * 100),
		"answer_correct_rate": map[string]float64{
			"baseline": safeMetric(mean(observation.answerBaseline)), "experiment": safeMetric(mean(observation.answerExperiment)),
		},
		"no_answer_rate": map[string]float64{
			"baseline": safeMetric(mean(observation.noAnswerBaseline)), "experiment": safeMetric(mean(observation.noAnswerExperiment)),
		},
		"citation_precision": map[string]float64{
			"baseline": safeMetric(mean(observation.citationBaseline)), "experiment": safeMetric(mean(observation.citationExperiment)),
		},
		"p95_latency_ms": map[string]int64{
			"baseline": percentile95(observation.latencyBaseline), "experiment": percentile95(observation.latencyExperiment),
		},
		"p95_latency_delta_ms": percentile95(observation.latencyExperiment) - percentile95(observation.latencyBaseline),
	}
}

func resultTaskType(result model.EvaluationCaseResult) string {
	var metrics struct {
		TaskType string `json:"task_type"`
	}
	if result.Metrics != "" {
		_ = json.Unmarshal([]byte(result.Metrics), &metrics)
	}
	if strings.TrimSpace(metrics.TaskType) == "" {
		return "unclassified"
	}
	return strings.TrimSpace(metrics.TaskType)
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func scopedTaskTypeOrder() []string {
	return []string{"boundary", "conflict", "cross_project", "preference", "project_state", "unclassified"}
}

func positiveHitAndNoAnswer(
	observations map[string]*scopedTaskObservation,
) ([]float64, []float64, []float64) {
	hitDiffs, noAnswerBaseline, noAnswerExperiment := []float64{}, []float64{}, []float64{}
	for _, taskType := range scopedTaskTypeOrder() {
		observation := observations[taskType]
		if observation == nil || isNegativeMemoryEvaluationTaskType(taskType) {
			continue
		}
		hitDiffs = append(hitDiffs, observation.hitDiffs...)
		noAnswerBaseline = append(noAnswerBaseline, observation.noAnswerBaseline...)
		noAnswerExperiment = append(noAnswerExperiment, observation.noAnswerExperiment...)
	}
	return hitDiffs, noAnswerBaseline, noAnswerExperiment
}

func positiveEvidence(
	observations map[string]*scopedTaskObservation,
) ([]float64, []float64, []int64, []int64) {
	citationBaseline, citationExperiment, latencyBaseline, latencyExperiment := []float64{}, []float64{}, []int64{}, []int64{}
	for _, taskType := range scopedTaskTypeOrder() {
		observation := observations[taskType]
		if observation == nil || isNegativeMemoryEvaluationTaskType(taskType) {
			continue
		}
		citationBaseline = append(citationBaseline, observation.citationBaseline...)
		citationExperiment = append(citationExperiment, observation.citationExperiment...)
		latencyBaseline = append(latencyBaseline, observation.latencyBaseline...)
		latencyExperiment = append(latencyExperiment, observation.latencyExperiment...)
	}
	return citationBaseline, citationExperiment, latencyBaseline, latencyExperiment
}

func positiveRates(observations map[string]*scopedTaskObservation, hit bool) (float64, float64) {
	baseline, experiment := []float64{}, []float64{}
	for _, taskType := range scopedTaskTypeOrder() {
		observation := observations[taskType]
		if observation == nil || isNegativeMemoryEvaluationTaskType(taskType) {
			continue
		}
		if hit {
			baseline = append(baseline, observation.hitBaseline...)
			experiment = append(experiment, observation.hitExperiment...)
		} else {
			baseline = append(baseline, observation.answerBaseline...)
			experiment = append(experiment, observation.answerExperiment...)
		}
	}
	return mean(baseline), mean(experiment)
}

func metricsJSON(protocol scopedEvaluationProtocol, values map[string]interface{}) string {
	values["contract_version"] = protocol.ContractVersion
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func safeMetric(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func scopedStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valuesFor(results map[string]model.EvaluationCaseResult, hit bool) []float64 {
	values := make([]float64, 0, len(results))
	for _, result := range results {
		if hit {
			values = append(values, boolValue(result.HitAtK))
		} else {
			values = append(values, boolValue(result.AnswerCorrect))
		}
	}
	return values
}

func boolValue(value *bool) float64 {
	if value != nil && *value {
		return 1
	}
	return 0
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func pairedDifferenceInterval(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	meanDifference := mean(values)
	if len(values) == 1 {
		return meanDifference, meanDifference
	}
	variance := 0.0
	for _, difference := range values {
		variance += (difference - meanDifference) * (difference - meanDifference)
	}
	variance /= float64(len(values) - 1)
	standardError := math.Sqrt(variance / float64(len(values)))
	return meanDifference - 1.96*standardError, meanDifference + 1.96*standardError
}

func percentile95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64{}, values...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	index := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

func (s *Service) failScopedEvaluationRun(ctx context.Context, run *model.EvaluationRun, evalErr error) (*model.EvaluationRun, error) {
	reason := "scoped evaluation failed"
	if evalErr != nil {
		reason = evalErr.Error()
	}
	if len(reason) > 256 {
		reason = reason[:256]
	}
	if completeErr := s.Store.CompleteEvaluationRunWithGate(
		ctx, run.TenantID, run.ID, model.EvaluationRunFailed, "", nil, model.EvaluationGateFail, reason,
	); completeErr != nil {
		return nil, completeErr
	}
	return nil, evalErr
}
