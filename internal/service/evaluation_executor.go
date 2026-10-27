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
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/strutil"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const (
	scopedEvaluationContractVersion   = "scoped-paired-ab-v1"
	scopedEvaluationContractVersionV2 = "scoped-paired-ab-v2"
	scopedEvaluationContractVersionV3 = "scoped-paired-ab-v3"
	// scopedEvaluationContractVersionV4 keeps every v3 quality threshold and
	// changes only the retrieval semantics of the experiment arm (137 §5.4
	// compiled-first execution) and the citation counting basis for compiled
	// artifact rows (provenance via source_doc_ids instead of the raw
	// document_id, whose dataset-level value can never match a source doc).
	scopedEvaluationContractVersionV4 = "scoped-paired-ab-v4"
	// scopedRetrievalModeCompiledFirst is the only retrieval mode v4 accepts:
	// compiled artifact rows lead the returned evidence, raw chunks fill the
	// remaining top_k budget from a dedicated include=false leg.
	scopedRetrievalModeCompiledFirst = "compiled_first"
	// scopedRetrievalCandidateWindowV4 is the retrieval candidate window shared
	// by both v4 legs. The compiled leg pulls Size=window so compiled artifact
	// rows ranked below the raw top_k still reach the compiled prefix; the raw
	// leg returns at most protocol.TopK raw chunks.
	scopedRetrievalCandidateWindowV4   = 64
	compiledArtifactSourceRaw          = "raw"
	compiledArtifactSourceCompiled     = "compiled"
	compiledArtifactSourceDerived      = "compiled_derived"
	compiledArtifactSourceInternal     = "compiled_internal"
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
	// v4-only execution semantics. parsed defaults keep the zero values
	// invalid so a v4 protocol must state them explicitly.
	RetrievalMode          string `json:"retrieval_mode"`
	ExcludeInternalRows    bool   `json:"exclude_internal_rows"`
	CompiledFirstMaxChunks int    `json:"compiled_first_max_chunks"`
	// MinRepeatConsistencyPP is the v4-only pre-registered test-retest
	// reliability threshold (223 T4 初版, 外部 KnowEval P2 双遍 ≥95% gate).
	// 0 disables the repeat pass; a positive value (0,100] enables a second
	// experiment-arm pass and, when per-case pass consistency between the two
	// passes falls below the threshold, the paired gate reports WAIT.
	MinRepeatConsistencyPP float64 `json:"min_repeat_consistency_pp,omitempty"`
	// v4-only abstention calibration (223 T3 拒答校准初版): the strategy
	// stance for no-answer behaviour (conservative vs. low-confidence empty
	// answer vs. hint-gap) plus two explicit budgets - the abstention
	// precision floor (accurate abstains / classified abstains) and the
	// confident-wrong cap (answered-but-wrong / answers given). All are
	// pre-registered so recalibration requires a fresh protocol registration.
	AbstentionPolicy      string  `json:"abstention_policy,omitempty"`
	MinAbstainPrecisionPP float64 `json:"min_abstain_precision_pp,omitempty"`
	MaxConfidentWrongPP   float64 `json:"max_confident_wrong_pp,omitempty"`
	// T3 (doc/224 §2.3, doc/223 §问题 3/4) 引用精确定位与生成归因 v4-only
	// 字段。零值即关闭，老 v4 protocol 不受影响（向后兼容，不破坏判分）：
	// CitationGranularity 声明 citations 细化到的 chunk 内最小粒度
	// （paragraph/row/cell，空 = 不启用定位判分）；MinTableAccuracyPP 是
	// xlsx/table 专项准确率门槛；MinLocatorPrecisionPP 是表格定位可用率门槛
	// （定位可用 chunk 比例，防“解析缺失”成为隐蔽失分）。
	CitationGranularity   string  `json:"citation_granularity,omitempty"`
	MinTableAccuracyPP    float64 `json:"min_table_accuracy_pp,omitempty"`
	MinLocatorPrecisionPP float64 `json:"min_locator_precision_pp,omitempty"`
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
	if err := s.requireScopedEvaluationPreregistration(ctx, run, protocol); err != nil {
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

	var gateMetrics string
	var gateStatus, gateReason string
	if protocol.MinRepeatConsistencyPP > 0 {
		repeat, repeatErr := s.executeScopedEvaluationArm(ctx, run, cases, scopedRepeatArm, protocol)
		if repeatErr != nil {
			return s.failScopedEvaluationRun(ctx, run, repeatErr)
		}
		gateMetrics, gateStatus, gateReason = scopedEvaluationGateRepeated(baseline, experiment, repeat, protocol)
	} else {
		gateMetrics, gateStatus, gateReason = scopedEvaluationGate(baseline, experiment, protocol)
	}
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
		protocol.ContractVersion != scopedEvaluationContractVersionV3 &&
		protocol.ContractVersion != scopedEvaluationContractVersionV4 {
		return protocol, httperr.BadRequest(40072, "unsupported scoped evaluation contract")
	}
	if protocol.ContractVersion == scopedEvaluationContractVersionV2 &&
		(protocol.MinCompiledArtifactPairs <= 0 || protocol.MinCompiledArtifactPairs > protocol.MinSamples) {
		return protocol, httperr.BadRequest(40072, "scoped evaluation compiled artifact minimum is invalid")
	}
	if protocol.ContractVersion == scopedEvaluationContractVersion && protocol.MinCompiledArtifactPairs != 0 {
		return protocol, httperr.BadRequest(40072, "compiled artifact diagnostics require protocol version 2")
	}
	if protocol.ContractVersion == scopedEvaluationContractVersionV3 ||
		protocol.ContractVersion == scopedEvaluationContractVersionV4 {
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
		if protocol.ContractVersion == scopedEvaluationContractVersionV4 {
			if protocol.RetrievalMode != scopedRetrievalModeCompiledFirst {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 requires the compiled_first retrieval mode")
			}
			if !protocol.ExcludeInternalRows {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 must exclude internal compilation rows from returned evidence")
			}
			if protocol.CompiledFirstMaxChunks <= 0 || protocol.CompiledFirstMaxChunks > protocol.TopK {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 compiled-first cap is invalid")
			}
			if protocol.MinRepeatConsistencyPP != 0 &&
				(protocol.MinRepeatConsistencyPP < 0 || protocol.MinRepeatConsistencyPP > 100) {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 repeat consistency threshold is invalid")
			}
			if protocol.AbstentionPolicy != "" && !validScopedAbstentionPolicy(protocol.AbstentionPolicy) {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 abstention policy is invalid")
			}
			if protocol.MinAbstainPrecisionPP != 0 &&
				(protocol.MinAbstainPrecisionPP < 0 || protocol.MinAbstainPrecisionPP > 100) {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 abstention precision threshold is invalid")
			}
			if protocol.MaxConfidentWrongPP != 0 &&
				(protocol.MaxConfidentWrongPP < 0 || protocol.MaxConfidentWrongPP > 100) {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 confident wrong budget is invalid")
			}
			if protocol.CitationGranularity != "" && !validScopedCitationGranularity(protocol.CitationGranularity) {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 citation granularity is invalid")
			}
			if protocol.MinTableAccuracyPP < 0 || protocol.MinTableAccuracyPP > 100 {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 table accuracy threshold is invalid")
			}
			if protocol.MinLocatorPrecisionPP < 0 || protocol.MinLocatorPrecisionPP > 100 {
				return protocol, httperr.BadRequest(40072, "scoped evaluation v4 locator precision threshold is invalid")
			}
		} else if protocol.RetrievalMode != "" || protocol.ExcludeInternalRows ||
			protocol.CompiledFirstMaxChunks != 0 || protocol.MinRepeatConsistencyPP != 0 ||
			protocol.AbstentionPolicy != "" || protocol.MinAbstainPrecisionPP != 0 ||
			protocol.MaxConfidentWrongPP != 0 || protocol.CitationGranularity != "" ||
			protocol.MinTableAccuracyPP != 0 || protocol.MinLocatorPrecisionPP != 0 {
			return protocol, httperr.BadRequest(40072, "v4 retrieval semantics require protocol version 4")
		}
	} else if protocol.PrimaryMetric != "" || protocol.MinCitationLiftPP != 0 ||
		protocol.MinCompiledEffectPairs != 0 || protocol.MinRepeatConsistencyPP != 0 ||
		protocol.AbstentionPolicy != "" || protocol.MinAbstainPrecisionPP != 0 ||
		protocol.MaxConfidentWrongPP != 0 || protocol.CitationGranularity != "" ||
		protocol.MinTableAccuracyPP != 0 || protocol.MinLocatorPrecisionPP != 0 {
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
	// TableAssertion (doc/224 §2.3 T3.2) 声明该 case 是 xlsx/table 定位断言：
	// 期望命中表格内指定行/列（或表头+取值），替代“整块误引”式 doc 级判定。
	TableAssertion *scopedTableAssertion `json:"table_assertion"`
}

// scopedTableAssertion is the expected evidence location for a table-family
// case. Row/Col are 1-based; Col 0 means any column under the expected header;
// Header + Value performs a header-contextual cell match so a cell value alone
// does not satisfy the assertion (table-header context is mandatory).
type scopedTableAssertion struct {
	Row      int      `json:"row"`
	Col      int      `json:"col"`
	Header   string   `json:"header"`
	Value    string   `json:"value"`
	Contains []string `json:"contains"`
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
				var chunks []map[string]interface{}
				if (arm == model.EvaluationArmExperiment || arm == scopedRepeatArm) &&
					protocol.ContractVersion == scopedEvaluationContractVersionV4 {
					// v4 compiled-first execution (137 §5.4): the compiled leg
					// pulls the candidate window with compilation included so
					// artifact rows ranked below the raw top_k still reach the
					// compiled prefix, then the raw leg fills the remaining
					// top_k budget from a compilation-free retrieval.
					compiledLeg, legErr := s.RAGFlow.SearchDataset(ctx, dataset.RAGFlowDatasetID, ragflow.SearchDatasetRequest{
						Question:                    item.Question,
						DocumentIDs:                 append([]string{}, config.DocumentIDs...),
						Page:                        1,
						Size:                        scopedRetrievalCandidateWindowV4,
						TopK:                        scopedRetrievalCandidateWindowV4,
						IncludeKnowledgeCompilation: true,
						Keyword:                     config.Keyword,
					})
					if legErr != nil {
						return nil, httperr.New(502, 50280, "scoped evaluation retrieval failed")
					}
					rawLeg, legErr := s.RAGFlow.SearchDataset(ctx, dataset.RAGFlowDatasetID, ragflow.SearchDatasetRequest{
						Question:                    item.Question,
						DocumentIDs:                 append([]string{}, config.DocumentIDs...),
						Page:                        1,
						Size:                        protocol.TopK,
						TopK:                        scopedRetrievalCandidateWindowV4,
						IncludeKnowledgeCompilation: false,
						Keyword:                     config.Keyword,
					})
					if legErr != nil {
						return nil, httperr.New(502, 50280, "scoped evaluation retrieval failed")
					}
					if compiledLeg == nil {
						compiledLeg = &ragflow.SearchDatasetResult{}
					}
					if rawLeg == nil {
						rawLeg = &ragflow.SearchDatasetResult{}
					}
					// 137 §11.1: count whether the merged evidence set is led by
					// compiled artifact rows or degraded to raw-only evidence.
					if len(compiledLeg.Chunks) == 0 {
						obs.Get().AddRetrievalMode(scopedRetrievalModeCompiledFirst, "raw_fallback")
					} else {
						obs.Get().AddRetrievalMode(scopedRetrievalModeCompiledFirst, "compiled_leaded")
					}
					chunks = scopedCompiledFirstMerge(compiledLeg.Chunks, rawLeg.Chunks, protocol)
				} else {
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
					chunks = response.Chunks
				}
				result, encodeErr := scopedRetrievalResult(run, item, arm, chunks, protocol, startedAt, provenance)
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
		if arm == model.EvaluationArmBaseline {
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
			result, encodeErr := scopedMemoryResult(run, item, arm, search.Messages, startedAt)
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
	compiledInternalChunkCount := 0
	compiledCitedChunkCount := 0
	rawChunkCount := 0
	provenanceCounting := protocol.ContractVersion == scopedEvaluationContractVersionV4
	expectedIDs := expectedScopedDocIDs(item)
	compiledCitations := make([]bool, 0, len(chunks))
	references := make([]map[string]interface{}, 0, len(chunks))
	// T3.2 表格坐标统计：定位可用 chunk 数 + 表格断言（期望单元格/行/表头）是否
	// 精确落位（“整块误引”不计数）。仅当 protocol 启用 T3 字段时才写入指标。
	t3 := t3Gate(protocol)
	tableMeta := scopedCaseMetadata{}
	if t3 || protocol.CitationGranularity != "" {
		if item.Metadata != "" {
			_ = json.Unmarshal([]byte(item.Metadata), &tableMeta)
		}
	}
	locatorUsable := 0
	tableAssertionHit := false
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
		case compileKeyword != "" && isCompiledInternalArtifactKeyword(compileKeyword):
			source = compiledArtifactSourceInternal
			compiledInternalChunkCount++
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
		// v4 citation basis: compiled artifact rows cite through their
		// source_doc_ids provenance because upstream dataset-level wiki rows
		// carry the dataset id as document_id and can never match a source
		// document. v3 keeps the strict document_id-only basis.
		chunkProvenance := artifactProvenance
		cited := false
		citationBasis := ""
		switch {
		case provenanceCounting && source == compiledArtifactSourceCompiled:
			sourceDocIDs := chunkSourceDocIDs(chunk)
			cited = scopedIDsIntersect(expectedIDs, sourceDocIDs)
			citationBasis = "source_doc_ids"
			if len(chunkProvenance.SourceDocIDs) == 0 && len(sourceDocIDs) > 0 {
				// The compile_kwd match resolves the artifact family but not the
				// specific artifact's source docs; the chunk row itself carries
				// them, and the reference must record the basis that was cited.
				chunkProvenance.SourceDocIDs = sourceDocIDs
			}
		case source != compiledArtifactSourceInternal:
			citationBasis = "document_id"
		}
		if cited {
			compiledCitedChunkCount++
		}
		compiledCitations = append(compiledCitations, cited)
		locator := scopedTableLocatorForChunk(chunk)
		if locator != nil {
			locatorUsable++
			if t3 && tableMeta.TableAssertion != nil && scopedTableAssertionSatisfied(tableMeta.TableAssertion, chunk) {
				tableAssertionHit = true
			}
		}
		ref := scopedRetrievalReference(chunk, source, compileKeyword, chunkProvenance, citationBasis)
		// T3.2 表格坐标基座：仅 T3 协议把 locator（表头上下文 + 行列坐标）
		// 投影进引用；旧协议 references 保持字节级不变。
		if t3 && locator != nil {
			ref["table_locator"] = map[string]interface{}{
				"kind":        locator.Kind,
				"headers":     locator.Headers,
				"header_text": locator.HeaderText,
				"row_count":   locator.RowCount,
				"col_count":   locator.ColCount,
			}
		}
		references = append(references, ref)
	}
	hit, answerCorrect, precision := scopedEvidenceMetrics(item, docIDs, contents, compiledCitations)
	noAnswer := len(docIDs) == 0
	refs, err := json.Marshal(references)
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation references")
	}
	metricsPayload := map[string]interface{}{
		"hit_at_k": hit, "answer_correct": answerCorrect, "no_answer": noAnswer,
		"citation_precision": precision, "task_type": scopedTaskType(item),
		"expected_evidence_present": scopedExpectedEvidencePresent(item),
		"returned_count":            len(chunks), "compiled_chunk_count": compiledChunkCount,
		"compiled_derived_chunk_count":  compiledDerivedChunkCount,
		"compiled_internal_chunk_count": compiledInternalChunkCount, "raw_chunk_count": rawChunkCount,
	}
	if provenanceCounting {
		metricsPayload["compiled_cited_chunk_count"] = compiledCitedChunkCount
	}
	if t3 {
		// 定位覆盖率分母 = 表格族候选 chunk（已解析出坐标的），而非返回全集：
		// compiled-first 会用 raw 正文填满 top_k，非表格候选不稀释定位达标率。
		locatorRatio := 0.0
		if locatorUsable > 0 {
			locatorRatio = 1.0
		}
		metricsPayload["locator_usable_ratio"] = locatorRatio
		metricsPayload["table_locator_count"] = locatorUsable
		if tableMeta.TableAssertion != nil {
			metricsPayload["table_assertion_present"] = true
			metricsPayload["table_accuracy"] = tableAssertionHit
		}
	}
	metrics, err := json.Marshal(metricsPayload)
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
		"task_type":                 scopedTaskType(item),
		"expected_evidence_present": scopedExpectedEvidencePresent(item),
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
	messages []ragflow.MemoryMessage, startedAt time.Time,
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
	hit, answerCorrect, _ := scopedEvidenceMetrics(item, docIDs, contents, nil)
	noAnswer := len(contents) == 0
	encodedRefs, err := json.Marshal(refs)
	if err != nil {
		return nil, httperr.Internal("unable to serialize scoped evaluation references")
	}
	metrics, err := json.Marshal(map[string]interface{}{
		"hit_at_k": hit, "answer_correct": answerCorrect, "no_answer": noAnswer,
		"citation_precision": nil, "task_type": scopedTaskType(item),
		"expected_evidence_present": scopedExpectedEvidencePresent(item),
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
	item model.EvaluationCaseVersion, returnedIDs, contents []string, compiledCitations []bool,
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
	// v4: a compiled artifact row whose source_doc_ids intersect the expected
	// citations is evidence of the expected document in context, so it
	// satisfies hit_at_k even though its document_id carries the dataset id.
	for _, cited := range compiledCitations {
		if cited {
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
	for index, value := range returnedIDs {
		if idSet[value] && containsScopedValue(expectedIDs, value) {
			matched++
			continue
		}
		if index < len(compiledCitations) && compiledCitations[index] {
			matched++
		}
	}
	citationPrecision = float64(matched) / float64(len(returnedIDs))
	return hit, answerCorrect, citationPrecision
}

// scopedIDsIntersect reports whether any identifier appears in both slices.
func scopedIDsIntersect(expected, sourceDocIDs []string) bool {
	for _, expectedID := range expected {
		for _, sourceID := range sourceDocIDs {
			if expectedID != "" && expectedID == sourceID {
				return true
			}
		}
	}
	return false
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
	artifactProvenance compiledArtifactProvenance, citationBasis string,
) map[string]interface{} {
	reference := map[string]interface{}{"source": source}
	if citationBasis != "" {
		reference["citation_basis"] = citationBasis
	}
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

// scopedTableLocator describes the coordinate basis of a table-family chunk
// (doc/224 §2.3 T3.2): markdown tables are parsed into header + row/col
// counts (the table-header context is preserved separately so lookups stay
// header-contextual); xlsx-family chunks are recognized from their meta
// payload. Optional fields use zero values when unknown; a nil locator means
// the chunk carries no table structure and cannot be located precisely.
type scopedTableLocator struct {
	Kind       string   `json:"kind"`
	Headers    []string `json:"headers"`
	HeaderText string   `json:"header_text"`
	RowCount   int      `json:"row_count"`
	ColCount   int      `json:"col_count"`
}

func scopedTableLocatorForChunk(chunk map[string]interface{}) *scopedTableLocator {
	content := strings.ToLower(stringFromAny(chunk["content"]))
	tableRows := parseMarkdownTableRows(content)
	loc := &scopedTableLocator{}
	if len(tableRows) >= 2 {
		loc.Kind = scopedTableKindMarkdown
		loc.Headers = append([]string(nil), tableRows[0]...)
		loc.HeaderText = strings.Join(tableRows[0], " | ")
		loc.RowCount = len(tableRows) - 1
		loc.ColCount = len(tableRows[0])
		return loc
	}
	if meta, ok := chunk["meta"].(map[string]interface{}); ok {
		for key, value := range meta {
			keyLower := strings.ToLower(key)
			if strings.Contains(keyLower, "excel") || strings.Contains(keyLower, "sheet") ||
				(strings.Contains(keyLower, "file") && valueContainsKind(value, "excel")) {
				loc.Kind = scopedTableKindXLSX
				loc.HeaderText = key
				return loc
			}
		}
	}
	return nil
}

func valueContainsKind(value interface{}, kind string) bool {
	switch typed := value.(type) {
	case string:
		lower := strings.ToLower(typed)
		return strings.Contains(lower, kind) || strings.Contains(lower, ".xls") || strings.Contains(lower, ".xlsx")
	case []interface{}:
		for _, item := range typed {
			if valueContainsKind(item, kind) {
				return true
			}
		}
	case map[string]interface{}:
		for _, item := range typed {
			if valueContainsKind(item, kind) {
				return true
			}
		}
	}
	return false
}

// parseMarkdownTableRows extracts the markdown table structure (lines that
// start with '|') and splits each line into trimmed cells; separator rows
// (| --- | --- |) are dropped. The first surviving row is the header row.
func parseMarkdownTableRows(content string) [][]string {
	rows := [][]string{}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		if isMarkdownTableSeparator(trimmed) {
			continue
		}
		cells := markdownTableRow(trimmed)
		clean := make([]string, 0, len(cells))
		for _, cell := range cells {
			normalized := strings.ToLower(strings.TrimSpace(cell))
			if normalized != "" {
				clean = append(clean, normalized)
			}
		}
		if len(clean) > 0 {
			rows = append(rows, clean)
		}
	}
	return rows
}

// scopedTableAssertionSatisfied judges whether the case's expected table
// evidence is precisely located within a chunk. A cell value alone never
// satisfies the assertion: header-context or an explicit row/col coordinate
// must resolve the value, otherwise the chunk is treated as a bulk mis-cite
// (the D1 “整块误引” failure mode).
func scopedTableAssertionSatisfied(assertion *scopedTableAssertion, chunk map[string]interface{}) bool {
	if assertion == nil {
		return false
	}
	contentRaw := stringFromAny(chunk["content"])
	contentLower := strings.ToLower(contentRaw)
	for _, token := range assertion.Contains {
		if strings.TrimSpace(token) != "" && !strings.Contains(contentLower, strings.ToLower(token)) {
			return false
		}
	}
	header := strings.ToLower(strings.TrimSpace(assertion.Header))
	value := strings.ToLower(strings.TrimSpace(assertion.Value))
	if header != "" && value != "" {
		tableRows := parseMarkdownTableRows(contentRaw)
		if len(tableRows) >= 2 {
			column := -1
			for index, cell := range tableRows[0] {
				if cell == header {
					column = index
					break
				}
			}
			if column >= 0 {
				for row := 1; row < len(tableRows); row++ {
					if column < len(tableRows[row]) && strings.Contains(tableRows[row][column], value) {
						return true
					}
				}
			}
		}
	}
	if assertion.Row >= 1 && assertion.Col >= 1 && value != "" {
		tableRows := parseMarkdownTableRows(contentRaw)
		if assertion.Row < len(tableRows) && assertion.Col <= len(tableRows[assertion.Row]) {
			cell := strings.TrimSpace(tableRows[assertion.Row][assertion.Col-1])
			if strings.Contains(cell, value) {
				return true
			}
		}
	}
	return false
}

const (
	scopedTableKindMarkdown = "table"
	scopedTableKindXLSX     = "xlsx"
)

func scopedRetrievalCandidateWindow(protocol scopedEvaluationProtocol) int {
	if protocol.ContractVersion == scopedEvaluationContractVersionV3 ||
		protocol.ContractVersion == scopedEvaluationContractVersionV4 {
		return 64
	}
	return protocol.TopK
}

// scopedCompiledFirstMerge builds the v4 experiment-arm evidence set (137
// §5.4 compiled-first): non-internal compiled artifact rows lead the result
// (capped at protocol.CompiledFirstMaxChunks), then raw chunks from the
// compilation-free leg fill the remaining top_k budget. Internal plumbing
// rows never occupy evidence slots; duplicate chunk ids are dropped. The
// compiled leg's raw chunks are discarded on purpose - raw evidence must
// come from the same compilation-free retrieval the baseline arm sees.
func scopedCompiledFirstMerge(compiledLeg, rawLeg []map[string]interface{}, protocol scopedEvaluationProtocol) []map[string]interface{} {
	merged := make([]map[string]interface{}, 0, protocol.TopK)
	seen := make(map[string]bool, protocol.TopK)
	appendChunk := func(chunk map[string]interface{}) bool {
		if len(merged) >= protocol.TopK {
			return false
		}
		if chunkID := stringFromAny(chunk["id"]); chunkID != "" {
			if seen[chunkID] {
				return false
			}
			seen[chunkID] = true
		}
		merged = append(merged, chunk)
		return true
	}
	compiledTaken := 0
	for _, chunk := range compiledLeg {
		if compiledTaken >= protocol.CompiledFirstMaxChunks || len(merged) >= protocol.TopK {
			break
		}
		keyword := compiledArtifactKeyword(chunk)
		if keyword == "" || isCompiledInternalArtifactKeyword(keyword) {
			continue
		}
		if appendChunk(chunk) {
			compiledTaken++
		}
	}
	for _, chunk := range rawLeg {
		if len(merged) >= protocol.TopK {
			break
		}
		if keyword := compiledArtifactKeyword(chunk); keyword != "" && isCompiledInternalArtifactKeyword(keyword) {
			continue
		}
		appendChunk(chunk)
	}
	return merged
}

// chunkSourceDocIDs extracts the upstream dataset-level provenance carried on
// compiled artifact rows (writer.go writes source_doc_ids; the search
// response keeps the field verbatim).
func chunkSourceDocIDs(chunk map[string]interface{}) []string {
	switch value := chunk["source_doc_ids"].(type) {
	case []string:
		return value
	case []interface{}:
		ids := make([]string, 0, len(value))
		for _, item := range value {
			if id := strings.TrimSpace(stringFromAny(item)); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	case string:
		if id := strings.TrimSpace(value); id != "" {
			return []string{id}
		}
	}
	return nil
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
	if keyword := compiledArtifactKeyword(chunk); keyword != "" && !isCompiledInternalArtifactKeyword(keyword) {
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

// compiledInternalArtifactKeywords lists compile_kwd values that mark internal
// compilation plumbing rows (resume caches, planner state, reduction results,
// navigation indexes). These rows carry machine-shaped content (e.g. wiki
// draft page JSON), are never citable artifact pages, and must not be counted
// as compiled artifact participations or raw evidence. Their presence in
// experiment-arm results is still recorded via the dedicated
// compiled_internal source so retrieval pollution stays observable.
var compiledInternalArtifactKeywords = map[string]bool{
	"wiki_page_draft":       true,
	"wiki_map_extract":      true,
	"wiki_map_state":        true,
	"wiki_map_state_meta":   true,
	"wiki_reduce_result":    true,
	"wiki_compilation_plan": true,
	"wiki_plan_group":       true,
	"wiki_mode_meta":        true,
	"dataset_nav":           true,
}

func isCompiledInternalArtifactKeyword(keyword string) bool {
	return compiledInternalArtifactKeywords[keyword]
}

func scopedEvaluationRequestID(runID, caseID, arm string) string {
	value := "evaluation:" + runID + ":" + caseID + ":" + arm
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

const (
	scopedAbstentionPolicyConservative  = "abstention_conservative"
	scopedAbstentionPolicyLowConfidence = "abstention_low_confidence"
	scopedAbstentionPolicyHintGap       = "abstention_hint_gap"
)

func validScopedAbstentionPolicy(policy string) bool {
	switch policy {
	case "", scopedAbstentionPolicyConservative, scopedAbstentionPolicyLowConfidence, scopedAbstentionPolicyHintGap:
		return true
	}
	return false
}

// T3 (doc/224 §2.3) 引用精确定位与表格投影的启用开关：任一 T3 字段非零即视为
// 启用了 T3 判分/观测。旧 v4 协议（无 T3 字段）不产出 T3 诊断，保持字节级兼容。
func t3Gate(protocol scopedEvaluationProtocol) bool {
	return protocol.CitationGranularity != "" || protocol.MinTableAccuracyPP > 0 || protocol.MinLocatorPrecisionPP > 0
}

// validScopedCitationGranularity reports whether the T3 citation granularity
// (doc/224 §2.3) is one of the pre-registered locator depths. The zero value
// (empty) means locator-aware scoring is disabled, keeping old v4 protocols
// byte-for-byte compatible with the pre-T3 judgement.
func validScopedCitationGranularity(granularity string) bool {
	switch granularity {
	case "", "paragraph", "row", "cell":
		return true
	}
	return false
}

// scopedTableDiagnostics decodes the T3 table-projection metrics an arm
// wrote into an EvaluationCaseResult (locator coverage + the case's table
// assertion verdict). Zero values when the run predates T3 or the case is not
// a table case, so legacy gates aggregate identically.
type scopedTableDiagnostics struct {
	TableAssertion     bool
	TableAccuracy      bool
	LocatorUsableRatio float64
}

func decodeScopedTableDiagnostics(result model.EvaluationCaseResult) scopedTableDiagnostics {
	diagnostics := scopedTableDiagnostics{}
	if result.Metrics == "" {
		return diagnostics
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(result.Metrics), &payload); err != nil {
		return diagnostics
	}
	if value, exists := payload["table_assertion_present"].(bool); exists {
		diagnostics.TableAssertion = value
	}
	if value, exists := payload["table_accuracy"].(bool); exists {
		diagnostics.TableAccuracy = value
	}
	if value, exists := payload["locator_usable_ratio"].(float64); exists {
		diagnostics.LocatorUsableRatio = value
	}
	return diagnostics
}

// scopedExpectedEvidencePresent reports whether the case declares any expected
// evidence (an expected answer or expected citation docs). The flag lets a
// no-answer observation be classified as over-abstain (evidence expected but
// the run abstained) versus an accurate abstention (nothing citable expected)
// - 223 T3 拒答校准初版. It is baked into every persisted case metric so the
// gate can classify abstentions without a second lookup.
func scopedExpectedEvidencePresent(item model.EvaluationCaseVersion) bool {
	if strings.TrimSpace(item.ExpectedAnswer) != "" {
		return true
	}
	return len(expectedScopedDocIDs(item)) > 0
}

// scopedResultExpectedEvidence reads the persisted expected-evidence flag;
// legacy fixtures without the flag report known=false and are excluded from
// abstention classification instead of skewing it.
func scopedResultExpectedEvidence(result model.EvaluationCaseResult) (present, known bool) {
	if result.Metrics == "" {
		return false, false
	}
	var payload struct {
		ExpectedEvidencePresent *bool `json:"expected_evidence_present"`
	}
	if err := json.Unmarshal([]byte(result.Metrics), &payload); err != nil || payload.ExpectedEvidencePresent == nil {
		return false, false
	}
	return *payload.ExpectedEvidencePresent, true
}

// scopedAbstentionCalibration buckets experiment-arm cases into the abstention
// calibration observables (223 T3): abstains vs. answers, over-abstains
// (abstained while evidence was expected), accurate abstains (abstained with
// nothing expected), and confident-wrong answers (answered but judged wrong by
// the evidence matcher). Cases without a recorded answer/no-answer marker or
// expected-evidence flag are left uncounted.
type scopedAbstentionCalibration struct {
	Policy          string
	AbstainCount    int
	OverAbstain     int
	AccurateAbstain int
	Classified      int
	ConfidentWrong  int
	AnswerGiven     int
}

func collectScopedAbstentionCalibration(
	experiment map[string]model.EvaluationCaseResult, policy string,
) scopedAbstentionCalibration {
	calibration := scopedAbstentionCalibration{Policy: policy}
	for _, result := range experiment {
		expectedPresent, expectedKnown := scopedResultExpectedEvidence(result)
		switch {
		case result.NoAnswer != nil && *result.NoAnswer:
			calibration.AbstainCount++
			if expectedKnown {
				if expectedPresent {
					calibration.OverAbstain++
				} else {
					calibration.AccurateAbstain++
				}
				calibration.Classified++
			}
		case result.NoAnswer != nil && !*result.NoAnswer:
			calibration.AnswerGiven++
			if result.AnswerCorrect != nil && !*result.AnswerCorrect {
				calibration.ConfidentWrong++
			}
		}
	}
	if calibration.Policy == "" {
		calibration.Policy = scopedAbstentionPolicyConservative
	}
	return calibration
}

func scopedAbstainPrecisionPP(calibration scopedAbstentionCalibration) float64 {
	if calibration.Classified == 0 {
		return 0
	}
	return float64(calibration.AccurateAbstain) / float64(calibration.Classified) * 100
}

func scopedConfidentWrongPP(calibration scopedAbstentionCalibration) float64 {
	if calibration.AnswerGiven == 0 {
		return 0
	}
	return float64(calibration.ConfidentWrong) / float64(calibration.AnswerGiven) * 100
}

func scopedAbstentionPayload(calibration scopedAbstentionCalibration, sampleCount int) map[string]interface{} {
	abstainRate, overAbstain := 0.0, 0.0
	if sampleCount > 0 {
		abstainRate = float64(calibration.AbstainCount) / float64(sampleCount) * 100
		overAbstain = float64(calibration.OverAbstain) / float64(sampleCount) * 100
	}
	return map[string]interface{}{
		"policy":                calibration.Policy,
		"abstain_rate_pp":       safeMetric(abstainRate),
		"over_abstain_pp":       safeMetric(overAbstain),
		"abstain_precision_pp":  safeMetric(scopedAbstainPrecisionPP(calibration)),
		"confident_wrong_pp":    safeMetric(scopedConfidentWrongPP(calibration)),
		"abstain_count":         calibration.AbstainCount,
		"confident_wrong_count": calibration.ConfidentWrong,
	}
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
	// T3.2 表格投影聚合：仅在基线/实验双方都声明表格断言的对偶计入，避免单边
	// 引入的样本不均；expLocatorRatios 收集实验臂的表格定位可用率（T3.1 定位可用性）。
	tableBaseAccuracy := make([]float64, 0, sampleCount)
	tableExperimentAccuracy := make([]float64, 0, sampleCount)
	tableAccuracyLifts := make([]float64, 0, sampleCount)
	experimentLocatorRatios := make([]float64, 0, sampleCount)
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
		baseTable, experimentTable := decodeScopedTableDiagnostics(baselineResult), decodeScopedTableDiagnostics(experimentResult)
		if baseTable.TableAssertion && experimentTable.TableAssertion {
			baseAccuracy, experimentAccuracy := 0.0, 0.0
			if baseTable.TableAccuracy {
				baseAccuracy = 1
			}
			if experimentTable.TableAccuracy {
				experimentAccuracy = 1
			}
			tableBaseAccuracy = append(tableBaseAccuracy, baseAccuracy)
			tableExperimentAccuracy = append(tableExperimentAccuracy, experimentAccuracy)
			tableAccuracyLifts = append(tableAccuracyLifts, experimentAccuracy-baseAccuracy)
			// 定位覆盖率仅在双边声明表格断言的对偶中聚合（与 table_accuracy
			// 同一样本框）：普通非表格 case 不稀释 locator 达标率。
			experimentLocatorRatios = append(experimentLocatorRatios, experimentTable.LocatorUsableRatio)
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
	calibration := collectScopedAbstentionCalibration(experiment, protocol.AbstentionPolicy)
	tableAccuracyLiftPP := (mean(tableExperimentAccuracy) - mean(tableBaseAccuracy)) * 100
	tableLiftLower, tableLiftUpper := pairedDifferenceInterval(tableAccuracyLifts)
	experimentLocatorPrecisionPP := mean(experimentLocatorRatios) * 100
	gatePayload := map[string]interface{}{
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
		"abstention_calibration":                scopedAbstentionPayload(calibration, sampleCount),
	}
	// T3 段仅在启用引号/表格定位的协议下附加，确保旧 v4 协议输出字节级不变。
	if t3Gate(protocol) {
		gatePayload["table_accuracy"] = map[string]float64{
			"baseline": safeMetric(mean(tableBaseAccuracy)), "experiment": safeMetric(mean(tableExperimentAccuracy)),
			"lift_pp": safeMetric(tableAccuracyLiftPP), "baseline_pairs": float64(len(tableBaseAccuracy)),
		}
		gatePayload["table_accuracy_lift_ci95_pp"] = map[string]float64{"lower": safeMetric(tableLiftLower * 100), "upper": safeMetric(tableLiftUpper * 100)}
		gatePayload["locator_precision_pp"] = safeMetric(experimentLocatorPrecisionPP)
		gatePayload["attribution_triple"] = map[string]float64{
			"citation_precision_lift_pp": safeMetric(citationLiftPP),
			"confident_wrong_pp":         safeMetric(scopedConfidentWrongPP(calibration)),
			"abstain_precision_pp":       safeMetric(scopedAbstainPrecisionPP(calibration)),
		}
	}
	encoded, encodeErr := json.Marshal(gatePayload)
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
	if protocol.MaxConfidentWrongPP > 0 && calibration.AnswerGiven > 0 &&
		scopedConfidentWrongPP(calibration) > protocol.MaxConfidentWrongPP {
		return string(encoded), model.EvaluationGateWait, "confident wrong rate exceeds the pre-registered calibration budget"
	}
	if protocol.MinAbstainPrecisionPP > 0 && calibration.AbstainCount > 0 && calibration.Classified > 0 &&
		scopedAbstainPrecisionPP(calibration) < protocol.MinAbstainPrecisionPP {
		return string(encoded), model.EvaluationGateWait, "abstention precision is below the pre-registered calibration floor"
	}
	// T3.2：xlsx/table 专项准确率为绝对 Floor（验收口径 bd-272595 acc >= 45%），
	// 表格对偶样本存在时用实验臂均值直接对门槛，而非相对基线（表格投影的目标是
	// 提升绝对精确率，不因基线过低而放行）。
	if protocol.MinTableAccuracyPP > 0 && len(tableExperimentAccuracy) > 0 &&
		safeMetric(mean(tableExperimentAccuracy))*100 < protocol.MinTableAccuracyPP {
		return string(encoded), model.EvaluationGateWait, "table accuracy is below the pre-registered xlsx/table floor"
	}
	// T3.1：定位可用率门槛——若启用定位判分，实验臂返回的表格 chunk 必须
	// 有足够比例携带可解析坐标，否则“解析缺失”会隐蔽地压 low 判分。
	if protocol.MinLocatorPrecisionPP > 0 && len(experimentLocatorRatios) > 0 &&
		safeMetric(experimentLocatorPrecisionPP) < protocol.MinLocatorPrecisionPP {
		return string(encoded), model.EvaluationGateWait, "table locator coverage is below the pre-registered minimum"
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

// scopedRepeatArm is the test-retest reliability arm (223 T4 初版 dual-pass
// WAIT): the experiment strategy re-executes on the same case set and the two
// passes are compared for per-case pass agreement. The arm is stored as a
// distinct ExperimentArm value so the repeat observation never collides with
// the baseline/experiment pair on the unique (run, case, sample, arm) index.
const scopedRepeatArm = "exp_repeat"

// scopedRepeatConsistency measures per-case pass agreement between the
// experiment pass and its repeat pass, returned as a ratio in [0,1] plus the
// agreeing and total pair counts. Missing repeat observations count as
// disagreement so an incomplete repeat pass surfaces as a below-threshold
// result instead of silently passing.
func scopedRepeatConsistency(experiment, repeat map[string]model.EvaluationCaseResult) (float64, int, int) {
	if len(experiment) == 0 || len(repeat) == 0 {
		return 0, 0, 0
	}
	total, consistent := 0, 0
	for caseID, experimentResult := range experiment {
		repeatResult, ok := repeat[caseID]
		total++
		if ok && passMarkersAgree(experimentResult.Pass, repeatResult.Pass) {
			consistent++
		}
	}
	if total == 0 {
		return 0, 0, 0
	}
	return float64(consistent) / float64(total), consistent, total
}

// passMarkersAgree reports test-retest agreement for two pass markers: both
// unset counts as agreement, any mixed presence counts as disagreement.
func passMarkersAgree(first, second *bool) bool {
	if first == nil && second == nil {
		return true
	}
	if first == nil || second == nil {
		return false
	}
	return *first == *second
}

// scopedEvaluationGateRepeated is the repeat-enabled gate (v4): it first
// enforces the pre-registered test-retest consistency threshold as a WAIT
// guard, then runs the standard paired gate and folds the repeat diagnostics
// into the gate report.
func scopedEvaluationGateRepeated(
	baseline, experiment, repeat map[string]model.EvaluationCaseResult, protocol scopedEvaluationProtocol,
) (string, string, string) {
	repeatPP, consistent, total := scopedRepeatConsistency(experiment, repeat)
	threshold := protocol.MinRepeatConsistencyPP
	if threshold > 0 {
		if total == 0 {
			encoded, _ := json.Marshal(map[string]interface{}{
				"contract_version":      protocol.ContractVersion,
				"pair_count":            len(experiment),
				"repeat_pair_count":     0,
				"repeat_consistency_pp": 0,
			})
			return string(encoded), model.EvaluationGateWait, "repeat-run paired sample is incomplete"
		}
		if repeatPP*100 < threshold {
			encoded, _ := json.Marshal(map[string]interface{}{
				"contract_version":             protocol.ContractVersion,
				"pair_count":                   len(experiment),
				"repeat_pair_count":            total,
				"repeat_consistent_pair_count": consistent,
				"repeat_consistency_pp":        safeMetric(repeatPP * 100),
			})
			return string(encoded), model.EvaluationGateWait, "repeat-run answer consistency is below the pre-registered minimum"
		}
	}
	metrics, status, reason := scopedEvaluationGate(baseline, experiment, protocol)
	if len(metrics) == 0 {
		return metrics, status, reason
	}
	var report map[string]interface{}
	if err := json.Unmarshal([]byte(metrics), &report); err != nil {
		return metrics, status, reason
	}
	report["repeat_pair_count"] = total
	report["repeat_consistent_pair_count"] = consistent
	report["repeat_consistency_pp"] = safeMetric(repeatPP * 100)
	encoded, encodeErr := json.Marshal(report)
	if encodeErr != nil {
		return metrics, status, reason
	}
	return string(encoded), status, reason
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
		reason = strutil.Truncate(reason, 256)
	}
	if completeErr := s.Store.CompleteEvaluationRunWithGate(
		ctx, run.TenantID, run.ID, model.EvaluationRunFailed, "", nil, model.EvaluationGateFail, reason,
	); completeErr != nil {
		return nil, completeErr
	}
	return nil, evalErr
}
