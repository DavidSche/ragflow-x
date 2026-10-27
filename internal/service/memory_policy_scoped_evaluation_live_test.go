package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type memoryScopedEvaluationLiveSample struct {
	CaseID            string   `json:"case_id"`
	PairedSampleID    string   `json:"paired_sample_id"`
	Question          string   `json:"question"`
	Answer            string   `json:"answer"`
	ExpectedKeywords  string   `json:"expected_keywords"`
	TaskType          string   `json:"task_type,omitempty"`
	ForbiddenKeywords []string `json:"forbidden_keywords,omitempty"`
}

type memoryScopedEvaluationLiveReport struct {
	RunID          string                             `json:"run_id"`
	PolicyID       string                             `json:"policy_id"`
	MemoryID       string                             `json:"memory_id"`
	AssistantID    string                             `json:"assistant_id"`
	SampleCount    int                                `json:"sample_count"`
	TopK           int                                `json:"top_k"`
	Status         string                             `json:"status"`
	GateStatus     string                             `json:"gate_status"`
	GateReason     string                             `json:"gate_reason"`
	CompletedAt    time.Time                          `json:"completed_at"`
	GateMetrics    json.RawMessage                    `json:"gate_metrics"`
	ArmSummaries   json.RawMessage                    `json:"arm_summaries"`
	FailedCases    []memoryScopedEvaluationFailedCase `json:"failed_cases,omitempty"`
	TaskTypeCounts map[string]int                     `json:"task_type_counts,omitempty"`
}

type memoryScopedEvaluationFailedCase struct {
	CaseID         string `json:"case_id"`
	PairedSampleID string `json:"paired_sample_id"`
	TaskType       string `json:"task_type,omitempty"`
	HitAtK         bool   `json:"hit_at_k"`
	AnswerCorrect  bool   `json:"answer_correct"`
	NoAnswer       bool   `json:"no_answer"`
}

func decodeMemoryScopedEvaluationLiveSamples(t *testing.T, path string) []memoryScopedEvaluationLiveSample {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read memory scoped evaluation live samples: %v", err)
	}
	var samples []memoryScopedEvaluationLiveSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatalf("decode memory scoped evaluation live samples: %v", err)
	}
	if err := validateMemoryScopedEvaluationLiveSamples(samples); err != nil {
		t.Fatalf("invalid memory scoped evaluation live samples: %v", err)
	}
	return samples
}

func validateMemoryScopedEvaluationLiveSamples(samples []memoryScopedEvaluationLiveSample) error {
	if len(samples) < 20 || len(samples) > 50 {
		return fmt.Errorf("must contain 20-50 pairs, got %d", len(samples))
	}
	seenCases := map[string]bool{}
	seenPairs := map[string]bool{}
	for index, sample := range samples {
		if strings.TrimSpace(sample.CaseID) == "" || strings.TrimSpace(sample.PairedSampleID) == "" ||
			strings.TrimSpace(sample.Question) == "" || strings.TrimSpace(sample.Answer) == "" {
			return fmt.Errorf("sample %d requires case_id, paired_sample_id, question and answer", index)
		}
		if seenCases[sample.CaseID] {
			return fmt.Errorf("duplicate case_id %q", sample.CaseID)
		}
		if seenPairs[sample.PairedSampleID] {
			return fmt.Errorf("duplicate paired_sample_id %q", sample.PairedSampleID)
		}
		if sample.TaskType != "" && !isValidMemoryEvaluationTaskType(sample.TaskType) {
			return fmt.Errorf("sample %d has invalid task_type %q", index, sample.TaskType)
		}
		negative := sample.TaskType == "boundary"
		if negative && (strings.TrimSpace(sample.ExpectedKeywords) != "" || len(sample.ForbiddenKeywords) == 0) {
			return fmt.Errorf("negative sample %d requires forbidden_keywords and no expected_keywords", index)
		}
		if !negative && strings.TrimSpace(sample.ExpectedKeywords) == "" {
			return fmt.Errorf("positive sample %d requires expected_keywords", index)
		}
		if sample.TaskType == "conflict" && len(sample.ForbiddenKeywords) == 0 {
			return fmt.Errorf("conflict sample %d requires forbidden_keywords", index)
		}
		seenCases[sample.CaseID] = true
		seenPairs[sample.PairedSampleID] = true
	}
	return nil
}

func mustMarshalJSON(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("encode failed: %v", err)
	}
	return string(encoded)
}

func validateExtendedMemoryScopedEvaluationLiveSamples(samples []memoryScopedEvaluationLiveSample) error {
	if err := validateMemoryScopedEvaluationLiveSamples(samples); err != nil {
		return err
	}
	if len(samples) < 40 {
		return fmt.Errorf("extended validation requires 40-50 pairs, got %d", len(samples))
	}
	counts := map[string]int{}
	for _, sample := range samples {
		taskType := sample.TaskType
		if taskType == "" {
			taskType = "unclassified"
		}
		counts[taskType]++
	}
	for _, taskType := range memoryEvaluationTaskTypes {
		if counts[taskType] < 5 {
			return fmt.Errorf("extended validation requires at least 5 %s samples, got %d", taskType, counts[taskType])
		}
	}
	return nil
}

func TestParseMemoryScopedEvaluationLiveSamples(t *testing.T) {
	samples := make([]memoryScopedEvaluationLiveSample, 0, 20)
	for index := range 20 {
		samples = append(samples, memoryScopedEvaluationLiveSample{
			CaseID:           fmt.Sprintf("case-%02d", index+1),
			PairedSampleID:   fmt.Sprintf("pair-%02d", index+1),
			Question:         "What is the controlled answer?",
			Answer:           fmt.Sprintf("The controlled answer %02d is verified.", index+1),
			ExpectedKeywords: fmt.Sprintf("controlled answer %02d", index+1),
		})
	}
	if err := validateMemoryScopedEvaluationLiveSamples(samples); err != nil {
		t.Fatalf("valid samples rejected: %v", err)
	}

	extended := make([]memoryScopedEvaluationLiveSample, 0, 40)
	taskTypes := []string{"preference", "project_state", "cross_project", "conflict", "boundary"}
	for index := range 40 {
		taskType := taskTypes[index%len(taskTypes)]
		sample := memoryScopedEvaluationLiveSample{
			CaseID:         fmt.Sprintf("extended-case-%02d", index+1),
			PairedSampleID: fmt.Sprintf("extended-pair-%02d", index+1),
			Question:       "What is the controlled extended fact?",
			Answer:         fmt.Sprintf("The controlled extended fact %02d is verified.", index+1),
			TaskType:       taskType,
		}
		if taskType == "boundary" {
			sample.ExpectedKeywords = ""
			sample.ForbiddenKeywords = []string{"controlled extended fact"}
		} else {
			sample.ExpectedKeywords = fmt.Sprintf("controlled extended fact %02d", index+1)
			if taskType == "conflict" {
				sample.ForbiddenKeywords = []string{"obsolete extended fact"}
			}
		}
		extended = append(extended, sample)
	}
	if err := validateExtendedMemoryScopedEvaluationLiveSamples(extended); err != nil {
		t.Fatalf("valid extended samples rejected: %v", err)
	}

	invalid := append(samples, memoryScopedEvaluationLiveSample{
		CaseID: "case-duplicate-pair", PairedSampleID: "pair-01", Question: "q",
		Answer: "a", ExpectedKeywords: "keyword",
	})
	err := validateMemoryScopedEvaluationLiveSamples(invalid)
	if err == nil || !strings.Contains(err.Error(), `duplicate paired_sample_id "pair-01"`) {
		t.Fatalf("expected duplicate pair error, got %v", err)
	}
}

func TestLiveMemoryPolicyScopedEvaluationPairedAB(t *testing.T) {
	if os.Getenv("RGX_ALLOW_SCOPED_EVALUATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_SCOPED_EVALUATION_LIVE=1 is required for the real RAGFlow Memory Policy A/B drill")
	}
	samplePath := os.Getenv("RGX_MEMORY_SCOPED_EVALUATION_LIVE_SAMPLE_FILE")
	if strings.TrimSpace(samplePath) == "" {
		t.Skip("RGX_MEMORY_SCOPED_EVALUATION_LIVE_SAMPLE_FILE is required")
	}
	samples := decodeMemoryScopedEvaluationLiveSamples(t, samplePath)
	requireTaskTypeCoverage := os.Getenv("RGX_MEMORY_SCOPED_EVALUATION_LIVE_TASK_TYPES") == "1"
	if requireTaskTypeCoverage {
		if err := validateExtendedMemoryScopedEvaluationLiveSamples(samples); err != nil {
			t.Fatalf("invalid extended memory scoped evaluation live samples: %v", err)
		}
	}
	svc, client, target, policy, memoryID, _ := newLiveMemoryE2EFixture(t, "policy-scoped-ab")
	ctx := context.Background()

	caseManifest, err := json.Marshal(samples)
	if err != nil {
		t.Fatalf("encode memory scoped evaluation case manifest: %v", err)
	}
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, policy.TenantID, target.ID, EvaluationSetVersionInput{
		EvalSetID: policy.ID, Version: 1,
		ChangeSummary: "real RAGFlow memory policy scoped paired A/B", Cases: caseManifest,
	})
	if err != nil {
		t.Fatalf("create memory scoped evaluation set: %v", err)
	}
	for index, sample := range samples {
		caseMetadata := map[string]interface{}{"capability": "memory"}
		if sample.TaskType != "" {
			caseMetadata["task_type"] = sample.TaskType
		}
		if len(sample.ForbiddenKeywords) > 0 {
			caseMetadata["forbidden_keywords"] = sample.ForbiddenKeywords
		}
		encodedMetadata, metadataErr := json.Marshal(caseMetadata)
		if metadataErr != nil {
			t.Fatalf("encode memory scoped case metadata: %v", metadataErr)
		}
		expectedAnswer := sample.Answer
		if sample.TaskType == "boundary" {
			expectedAnswer = ""
		}
		if _, err := svc.CreateEvaluationCaseVersion(ctx, policy.TenantID, target.ID, EvaluationCaseVersionInput{
			EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version,
			CaseID: sample.CaseID, CaseVersion: int64(index + 1), Question: sample.Question,
			ExpectedAnswer: expectedAnswer, ExpectedKeywords: sample.ExpectedKeywords,
			Metadata: encodedMetadata, PairedSampleID: sample.PairedSampleID,
		}); err != nil {
			t.Fatalf("create memory scoped case %s: %v", sample.CaseID, err)
		}
	}

	run, err := svc.CreateEvaluationRun(ctx, policy.TenantID, target.ID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeMemoryPolicy, XMemoryPolicyID: policy.ID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "memory-live-minimal-v1", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "memory-live-minimal-v1", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityMemory, ExecutionMode: "paired_ab",
		ExecutionConfig: memoryScopedEvaluationExecutionConfig(requireTaskTypeCoverage, len(samples)),
	})
	if err != nil {
		t.Fatalf("create memory scoped evaluation run: %v", err)
	}
	registerScopedEvaluationPreregistration(t, svc, ctx, policy.TenantID, target.ID, model.EvaluationCapabilityMemory,
		memoryScopedEvaluationExecutionConfig(requireTaskTypeCoverage, len(samples)))
	attribution := "rgx:" + policy.TenantID + ":" + target.ID
	sessionID := "evaluation-run-" + run.ID
	for _, sample := range samples {
		if err := client.Client.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
			MemoryIDs: []string{memoryID}, AgentID: policy.AssistantID, SessionID: sessionID,
			UserID: attribution, UserInput: sample.Question, AgentResponse: sample.Answer,
		}); err != nil {
			t.Fatalf("seed live memory sample %s: %v", sample.CaseID, err)
		}
	}
	waitForLiveMemorySeed(t, client, ctx, memoryID, policy.AssistantID, sessionID, attribution, len(samples))

	completed, err := svc.ExecuteScopedEvaluationRun(ctx, policy.TenantID, run.ID)
	if err != nil {
		t.Fatalf("execute live memory scoped evaluation: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("memory scoped evaluation did not complete: %+v", completed)
	}
	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(completed.Metrics), &metrics); err != nil {
		t.Fatalf("decode memory scoped gate metrics: %v", err)
	}
	if int(metrics["pair_count"].(float64)) != len(samples) {
		t.Fatalf("expected %d paired samples, got %+v", len(samples), metrics)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, policy.TenantID, completed.ID)
	if err != nil {
		t.Fatalf("list memory scoped case results: %v", err)
	}
	if len(results) != len(samples)*2 {
		t.Fatalf("expected %d case results, got %d", len(samples)*2, len(results))
	}
	caseBySample := map[string]model.EvaluationCaseResult{}
	for _, result := range results {
		if result.ExperimentArm == model.EvaluationArmExperiment {
			caseBySample[result.PairedSampleID] = result
		}
	}
	failedCases := make([]memoryScopedEvaluationFailedCase, 0)
	for _, sample := range samples {
		result, found := caseBySample[sample.PairedSampleID]
		if !found {
			t.Fatalf("missing experiment result for %s", sample.PairedSampleID)
		}
		if result.HitAtK != nil && *result.HitAtK {
			continue
		}
		if sample.TaskType == "boundary" && result.HitAtK != nil && !*result.HitAtK {
			continue
		}
		failedCase := memoryScopedEvaluationFailedCase{
			CaseID: sample.CaseID, PairedSampleID: sample.PairedSampleID,
			TaskType: sample.TaskType,
		}
		if result.HitAtK != nil {
			failedCase.HitAtK = *result.HitAtK
		}
		if result.AnswerCorrect != nil {
			failedCase.AnswerCorrect = *result.AnswerCorrect
		}
		if result.NoAnswer != nil {
			failedCase.NoAnswer = *result.NoAnswer
		}
		failedCases = append(failedCases, failedCase)
	}
	t.Logf("live memory policy failed experiment cases: %s", mustMarshalJSON(failedCases))
	t.Logf("live memory policy scoped evaluation run=%s gate=%s reason=%s pair_count=%d hit_lift_pp=%v p95_delta_ms=%v",
		completed.ID, completed.GateStatus, completed.GateReason,
		int(metrics["pair_count"].(float64)), metrics["hit_lift_pp"], metrics["p95_latency_delta_ms"])

	if reportPath := os.Getenv("RGX_MEMORY_SCOPED_EVALUATION_LIVE_REPORT"); strings.TrimSpace(reportPath) != "" {
		armSummaries := summarizeScopedEvaluationArms(results)
		encodedArms, encodeErr := json.Marshal(armSummaries)
		if encodeErr != nil {
			t.Fatalf("encode memory scoped arm summaries: %v", encodeErr)
		}
		report := memoryScopedEvaluationLiveReport{
			RunID: completed.ID, PolicyID: policy.ID, MemoryID: memoryID,
			AssistantID: policy.AssistantID, SampleCount: len(samples), TopK: 10,
			Status: completed.Status, GateStatus: completed.GateStatus, GateReason: completed.GateReason,
			CompletedAt: time.Now().UTC(), GateMetrics: json.RawMessage(completed.Metrics),
			ArmSummaries: encodedArms,
		}
		if requireTaskTypeCoverage {
			taskTypeCounts := map[string]int{}
			for _, sample := range samples {
				taskType := sample.TaskType
				if taskType == "" {
					taskType = "unclassified"
				}
				taskTypeCounts[taskType]++
			}
			report.TaskTypeCounts = taskTypeCounts
		}
		if len(failedCases) > 0 {
			report.FailedCases = failedCases
		}
		encoded, encodeErr := json.MarshalIndent(report, "", "  ")
		if encodeErr != nil {
			t.Fatalf("encode memory scoped live report: %v", encodeErr)
		}
		if dir := filepath.Dir(reportPath); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("create memory scoped report directory: %v", err)
			}
		}
		if err := os.WriteFile(reportPath, encoded, 0o600); err != nil {
			t.Fatalf("write memory scoped live report: %v", err)
		}
	}
}

func memoryScopedEvaluationExecutionConfig(requireTaskTypeCoverage bool, sampleCount int) json.RawMessage {
	config := map[string]interface{}{
		"contract_version":           scopedEvaluationContractVersion,
		"top_k":                      10,
		"min_samples":                sampleCount,
		"min_hit_lift_pp":            2,
		"max_p95_latency_delta_ms":   3000,
		"max_no_answer_lift_pp":      0,
		"answer_correctness_method":  "expected_evidence_match",
		"require_citation_precision": true,
	}
	if requireTaskTypeCoverage {
		config["require_task_type_coverage"] = true
		config["min_task_type_samples"] = 5
	}
	encoded, _ := json.Marshal(config)
	return encoded
}

func waitForLiveMemorySeed(
	t *testing.T, client *liveMemoryDispatchClient, ctx context.Context,
	memoryID, agentID, sessionID, attribution string, expected int,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		messages, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
			AgentID: agentID, SessionID: sessionID, UserID: attribution, Page: 1, PageSize: 100,
		})
		if err != nil {
			t.Logf("live memory seed list poll failed: %v", err)
		} else {
			active := 0
			for _, message := range messages {
				if memoryMessageActive(message) {
					active++
				}
			}
			t.Logf("live memory seed visibility: visible=%d active=%d expected=%d", len(messages), active, expected)
			if active >= expected {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live memory seed did not become visible before timeout: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	t.Fatalf("live memory seed did not become visible for %d messages", expected)
}
