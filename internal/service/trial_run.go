package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Trial runs are the "试跑" (practice run) surface: a workspace admin can
// check an assistant against its template eval suite before publishing. They
// are deliberately not release evidence (doc/231 D10, doc/228 §3.1 T7.3′):
//
//   - gate_eligible is absent by construction (no such column/field);
//   - no pre-registration, no minimum sample count, no paired A/B;
//   - retrieval-only, retrieval parameters come from the assistant snapshot;
//   - the summary never renders a hit rate when no case defines expected
//     citations (nil, not 0).
//
// Anything that looks like release evidence must go through the existing
// evaluation executor instead.
const (
	trialPolicyID   = "trial-retrieval-v1"
	trialMaxCases   = 10
	trialMinCases   = 1
	trialCaseTopN   = 3
	trialPendingTTL = 30 * time.Minute
)

// trialRunCancels tracks in-flight runs so a cancel request can stop the loop
// between cases. Trial runs are single-process diagnostics: a restart
// terminalizes them as FAILED/interrupted instead of resuming.
var trialRunCancels sync.Map

// TrialRunInput is the only accepted request body. Retrieval parameters are
// intentionally absent: they are read back from the assistant so a run can
// never claim a configuration it did not use.
type TrialRunInput struct {
	ScenarioTemplateID string `json:"scenario_template_id"`
	AssistantID        string `json:"assistant_id"`
	IdempotencyKey     string `json:"idempotency_key"`
}

// TrialAuthoringSnapshot is the immutable configuration a run executed under.
type TrialAuthoringSnapshot struct {
	AssistantID            string                 `json:"assistant_id"`
	AssistantName          string                 `json:"assistant_name"`
	AssistantKind          string                 `json:"assistant_kind"`
	DatasetIDs             []string               `json:"dataset_ids"`
	Language               string                 `json:"language"`
	PromptConfig           map[string]interface{} `json:"prompt_config"`
	LLMID                  string                 `json:"llm_id"`
	RerankID               string                 `json:"rerank_id"`
	TopN                   int                    `json:"top_n"`
	TopK                   int                    `json:"top_k"`
	SimilarityThreshold    float64                `json:"similarity_threshold"`
	VectorSimilarityWeight float64                `json:"vector_similarity_weight"`
	TemplateID             string                 `json:"template_id"`
	TemplateKey            string                 `json:"template_key"`
	TemplateName           string                 `json:"template_name"`
	TemplateVersion        int64                  `json:"template_version"`
	PromptPresetID         string                 `json:"prompt_preset_id"`
	ParameterProfileID     string                 `json:"parameter_profile_id"`
	Mode                   string                 `json:"mode"`
	PolicyID               string                 `json:"policy_id"`
}

// TrialRunSummary is the aggregate projection. HitAtK stays nil when no case
// can be judged (see HitRequired/UndefinedCases).
type TrialRunSummary struct {
	Cases          int      `json:"cases"`
	ReturnedCases  int      `json:"returned_cases"`
	FailedCases    int      `json:"failed_cases"`
	HitAtK         *float64 `json:"hit_at_k"`
	HitDenominator int      `json:"hit_denominator"`
	UndefinedCases int      `json:"undefined_cases"`
	TopScoreAvg    float64  `json:"top_score_avg"`
	LatencyP95Ms   int64    `json:"latency_p95_ms"`
	TopN           int      `json:"top_n"`
	Mode           string   `json:"mode"`
	// GateEligible is always false and exists only so the UI can state it.
	GateEligible bool `json:"gate_eligible"`
}

// TrialCaseView is one immutable case result.
type TrialCaseView struct {
	CaseID             string    `json:"case_id"`
	Question           string    `json:"question"`
	Returned           bool      `json:"returned"`
	HitAtK             *int      `json:"hit_at_k"`
	HitUndefinedReason string    `json:"hit_undefined_reason,omitempty"`
	TopScore           float64   `json:"top_score"`
	TopNScores         []float64 `json:"top_n_scores"`
	LatencyMs          int64     `json:"latency_ms"`
	ErrorCode          string    `json:"error_code,omitempty"`
}

// TrialRunView is the run plus its case rows.
type TrialRunView struct {
	ID                 string                 `json:"id"`
	ScenarioTemplateID string                 `json:"scenario_template_id"`
	TemplateVersion    int64                  `json:"template_version"`
	AssistantID        string                 `json:"assistant_id"`
	AssistantKind      string                 `json:"assistant_kind"`
	EvalSetID          string                 `json:"eval_set_id"`
	EvalSetVersion     int64                  `json:"eval_set_version"`
	EvalSetHash        string                 `json:"eval_set_hash"`
	PolicyID           string                 `json:"policy_id"`
	PolicyHash         string                 `json:"policy_hash"`
	Mode               string                 `json:"mode"`
	Status             string                 `json:"status"`
	FailureReason      string                 `json:"failure_reason,omitempty"`
	IdempotencyKey     string                 `json:"idempotency_key"`
	Snapshot           TrialAuthoringSnapshot `json:"config_snapshot"`
	Summary            TrialRunSummary        `json:"summary"`
	GateEligible       bool                   `json:"gate_eligible"`
	Cases              []TrialCaseView        `json:"cases,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	StartedAt          *time.Time             `json:"started_at,omitempty"`
	CompletedAt        *time.Time             `json:"completed_at,omitempty"`
}

// CreateTrialRun validates, snapshots and launches a retrieval-only run.
// Re-submitting the same (tenant, idempotency_key) returns the existing run
// instead of starting a second one.
func (s *Service) CreateTrialRun(ctx context.Context, tenantID, actorID string, in TrialRunInput) (*TrialRunView, error) {
	templateID := strings.TrimSpace(in.ScenarioTemplateID)
	assistantID := strings.TrimSpace(in.AssistantID)
	key := strings.TrimSpace(in.IdempotencyKey)
	if templateID == "" || assistantID == "" {
		return nil, httperr.BadRequest(40121, "scenario_template_id and assistant_id are required")
	}
	if key == "" || len(key) > 128 {
		return nil, httperr.BadRequest(40122, "idempotency_key is required (max 128 chars)")
	}
	if tenantID == "" {
		return nil, httperr.BadRequest(40121, "a concrete tenant is required for a trial run")
	}
	existing, err := s.Store.GetTrialRunByIdempotencyKey(ctx, tenantID, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ScenarioTemplateID != templateID || existing.AssistantID != assistantID {
			return nil, httperr.New(409, 40941, "idempotency_key already used by a different trial run")
		}
		return s.trialRunView(ctx, existing, true)
	}

	asset, err := s.Store.GetScenarioTemplate(ctx, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, httperr.NotFound("scenario template not found")
	}
	chat, err := s.Store.GetChatShadow(ctx, tenantID, assistantID, false)
	if err != nil {
		return nil, err
	}
	if chat == nil {
		return nil, httperr.NotFound("assistant not found")
	}
	if chat.ScenarioTemplateID == "" || chat.ScenarioTemplateID != asset.ID {
		return nil, httperr.New(409, 40942, "assistant is not linked to this scenario template")
	}
	datasetIDs := splitCSV(chat.DatasetIDs)
	if len(datasetIDs) == 0 {
		return nil, httperr.New(409, 40943, "assistant has no datasets bound")
	}
	payload, err := parseTemplatePayload(asset.PayloadJSON)
	if err != nil {
		return nil, err
	}
	authoring := ChatAuthoring{}
	if strings.TrimSpace(chat.ConfigJSON) != "" {
		if err := json.Unmarshal([]byte(chat.ConfigJSON), &authoring); err != nil {
			return nil, httperr.New(409, 40944, "assistant configuration cannot be read")
		}
	}
	if reason := trialProfileMismatch(payload.ParameterProfileID, authoring); reason != "" {
		return nil, httperr.New(409, 40940, reason)
	}

	evalSet, setVersion, _, _, err := s.PublishScenarioTemplateEvalSetVersion(
		ctx, tenantID, actorID, asset.ID, asset.Name+" 试跑题集", "trial run snapshot",
	)
	if err != nil {
		return nil, err
	}
	cases, err := s.Store.ListEvalCases(ctx, tenantID, evalSet.ID)
	if err != nil {
		return nil, err
	}
	if len(cases) < trialMinCases {
		return nil, httperr.New(409, 40945, "template has no evaluation cases to trial run")
	}
	if len(cases) > trialMaxCases {
		cases = cases[:trialMaxCases]
	}

	snapshot := TrialAuthoringSnapshot{
		AssistantID: assistantID, AssistantName: chat.Name, AssistantKind: model.TrialAssistantKindChat,
		DatasetIDs: datasetIDs, Language: chatLanguage(authoring.Language),
		PromptConfig: authoring.PromptConfig, LLMID: authoring.LLMID, RerankID: authoring.RerankID,
		TopN: authoring.TopN, TopK: authoring.TopK,
		SimilarityThreshold:    authoring.SimilarityThreshold,
		VectorSimilarityWeight: authoring.VectorSimilarityWeight,
		TemplateID:             asset.ID, TemplateKey: asset.Key, TemplateName: asset.Name,
		TemplateVersion: asset.LatestVersion, PromptPresetID: payload.PromptPresetID,
		ParameterProfileID: payload.ParameterProfileID, Mode: model.TrialModeRetrievalOnly,
		PolicyID: trialPolicyID,
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, httperr.New(500, 50090, "trial snapshot cannot be serialized")
	}
	policyHash, err := trialPolicyHash(snapshot)
	if err != nil {
		return nil, err
	}
	datasetJSON, err := json.Marshal(snapshot.DatasetIDs)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	run := &model.TrialRun{
		ID: id.New(), TenantID: tenantID, IdempotencyKey: key,
		ScenarioTemplateID: asset.ID, TemplateVersion: asset.LatestVersion,
		AssistantID: assistantID, AssistantKind: model.TrialAssistantKindChat,
		EvalSetID: evalSet.ID, EvalSetVersion: setVersion.Version, EvalSetHash: setVersion.CasesSnapshotHash,
		PolicyID: trialPolicyID, PolicyHash: policyHash, Mode: model.TrialModeRetrievalOnly,
		AuthoringSnapshot: string(snapshotJSON), DatasetIDs: string(datasetJSON),
		Status: model.TrialRunQueued, CreatedAt: now, UpdatedAt: now, Actor: actorID,
	}
	if err := s.Store.CreateTrialRun(ctx, run); err != nil {
		// Lost an idempotency race: return the winner instead of a 500.
		if current, getErr := s.Store.GetTrialRunByIdempotencyKey(ctx, tenantID, key); getErr == nil && current != nil {
			return s.trialRunView(ctx, current, true)
		}
		return nil, err
	}
	s.recordTrialAudit(ctx, run, "trial_run.create", "SUCCESS")
	go s.executeTrialRun(run, evalSet.ID, cases)
	return s.trialRunView(ctx, run, false)
}

// trialProfileMismatch asserts the assistant retrieval knobs equal the
// template's parameter profile. A mismatch is a 409 (config drift), never a
// silent re-read: the run must describe the configuration it actually used.
func trialProfileMismatch(profileID string, a ChatAuthoring) string {
	profile, ok := scenarioParameterProfiles[profileID]
	if !ok {
		return ""
	}
	if a.TopN != profile.topN {
		return "assistant top_n no longer matches the template parameter profile"
	}
	if diff := math.Abs(a.SimilarityThreshold - profile.similarityThreshold); diff > 1e-9 {
		return "assistant similarity_threshold no longer matches the template parameter profile"
	}
	if diff := math.Abs(a.VectorSimilarityWeight - profile.vectorWeight); diff > 1e-9 {
		return "assistant vector_similarity_weight no longer matches the template parameter profile"
	}
	return ""
}

func (s *Service) executeTrialRun(run *model.TrialRun, evalSetID string, cases []model.EvalCase) {
	ctx := context.Background()
	now := time.Now().UTC()
	run.Status = model.TrialRunRunning
	run.StartedAt = &now
	run.UpdatedAt = now
	if err := s.Store.UpdateTrialRun(ctx, run); err != nil {
		logger.Warn("trial run start transition failed", "run_id", run.ID, "error", err)
		return
	}
	snapshot := TrialAuthoringSnapshot{}
	if err := json.Unmarshal([]byte(run.AuthoringSnapshot), &snapshot); err != nil {
		s.finishTrialRun(ctx, run, model.TrialRunFailed, "snapshot_unreadable", nil)
		return
	}
	cancel := make(chan struct{})
	trialRunCancels.Store(run.ID, cancel)
	defer trialRunCancels.Delete(run.ID)

	results := make([]model.TrialCaseResult, 0, len(cases))
	for _, item := range cases {
		select {
		case <-cancel:
			s.finishTrialRun(ctx, run, model.TrialRunCancelled, "", results)
			return
		default:
		}
		results = append(results, s.executeTrialCase(ctx, run, snapshot, evalSetID, item))
	}
	s.finishTrialRun(ctx, run, model.TrialRunCompleted, "", results)
}

func (s *Service) executeTrialCase(
	ctx context.Context, run *model.TrialRun, snapshot TrialAuthoringSnapshot, evalSetID string, item model.EvalCase,
) model.TrialCaseResult {
	started := time.Now()
	row := model.TrialCaseResult{
		ID: id.New(), TenantID: run.TenantID, RunID: run.ID, CaseID: item.ID,
		Question: item.Question, CreatedAt: started.UTC(), UpdatedAt: started.UTC(),
	}
	expected := splitCSV(item.ExpectedCitationDocIDs)
	var chunks []map[string]interface{}
	for _, datasetID := range snapshot.DatasetIDs {
		result, err := s.RAGFlow.RetrieveDatasets(ctx, ragflow.RetrieveDatasetsRequest{
			DatasetIDs: []string{datasetID}, Question: item.Question, Page: 1, PageSize: trialCaseTopN,
			SimilarityThreshold:    floatPtr(snapshot.SimilarityThreshold),
			VectorSimilarityWeight: floatPtr(snapshot.VectorSimilarityWeight),
			Keyword:                true, RerankID: snapshot.RerankID,
		})
		if err != nil {
			row.ErrorCode = "retrieval_failed"
			row.HitUndefinedReason = "retrieval_error"
			row.LatencyMs = time.Since(started).Milliseconds()
			return row
		}
		if result != nil && len(result.Chunks) > 0 {
			chunks = append(chunks, result.Chunks[:min(len(result.Chunks), trialCaseTopN)]...)
		}
	}
	if len(chunks) > trialCaseTopN {
		chunks = chunks[:trialCaseTopN]
	}
	scores := make([]float64, 0, len(chunks))
	docIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		scores = append(scores, chunkScore(chunk))
		docID := stringFromAny(chunk["document_id"])
		if docID == "" {
			docID = stringFromAny(chunk["doc_id"])
		}
		if docID != "" {
			docIDs = append(docIDs, docID)
		}
	}
	row.Returned = len(chunks) > 0
	row.LatencyMs = time.Since(started).Milliseconds()
	if len(scores) > 0 {
		row.TopScore = scores[0]
	}
	rawScores, _ := json.Marshal(scores)
	row.TopNScores = string(rawScores)
	excerpt, _ := json.Marshal(shapeTrialEvidence(chunks))
	row.Evidence = string(excerpt)
	row.HitAtK = trialHitAtK(expected, docIDs)
	if row.HitAtK == nil {
		if len(expected) == 0 {
			row.HitUndefinedReason = "no_expected_citations"
		} else if !row.Returned {
			row.HitUndefinedReason = "no_results"
		} else {
			row.HitUndefinedReason = "no_expected_doc_in_results"
		}
	}
	return row
}

// trialHitAtK returns 1/0 only when the case defines expected citations and
// the run returned any evidence. Undefined stays nil so the UI never shows a
// fabricated 0% (doc/228 §3.1 T7.3′ ③).
func trialHitAtK(expected, returned []string) *int {
	if len(expected) == 0 || len(returned) == 0 {
		return nil
	}
	hits := 1
	for _, candidate := range returned {
		for _, want := range expected {
			if candidate == want {
				return &hits
			}
		}
	}
	miss := 0
	return &miss
}

func (s *Service) finishTrialRun(ctx context.Context, run *model.TrialRun, status, failureReason string, results []model.TrialCaseResult) {
	if len(results) > 0 {
		if err := s.Store.CreateTrialCaseResults(ctx, results); err != nil {
			logger.Warn("trial run case persistence failed", "run_id", run.ID, "error", err)
			status = model.TrialRunFailed
			failureReason = "case_persistence_failed"
		}
	}
	summary := buildTrialSummary(results)
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		summaryJSON = []byte("{}")
	}
	now := time.Now().UTC()
	run.Status = status
	run.FailureReason = failureReason
	run.Summary = string(summaryJSON)
	run.CompletedAt = &now
	run.UpdatedAt = now
	if err := s.Store.UpdateTrialRun(ctx, run); err != nil {
		logger.Warn("trial run finish transition failed", "run_id", run.ID, "error", err)
	}
	result := "SUCCESS"
	if status == model.TrialRunFailed {
		result = "FAILED"
	}
	s.recordTrialAudit(ctx, run, "trial_run."+strings.ToLower(status), result)
}

func buildTrialSummary(results []model.TrialCaseResult) TrialRunSummary {
	summary := TrialRunSummary{Cases: len(results), TopN: trialCaseTopN, Mode: model.TrialModeRetrievalOnly}
	hits, judged := 0, 0
	var scoreSum float64
	latencies := make([]int64, 0, len(results))
	for _, row := range results {
		if row.Returned {
			summary.ReturnedCases++
		}
		if row.ErrorCode != "" {
			summary.FailedCases++
		}
		if row.HitAtK != nil {
			judged++
			hits += *row.HitAtK
		} else {
			summary.UndefinedCases++
		}
		scoreSum += row.TopScore
		latencies = append(latencies, row.LatencyMs)
	}
	summary.HitDenominator = judged
	if judged > 0 {
		rate := float64(hits) / float64(judged)
		summary.HitAtK = &rate
	}
	if len(results) > 0 {
		summary.TopScoreAvg = roundFloat(scoreSum/float64(len(results)), 4)
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		idx := int(math.Ceil(0.95*float64(len(latencies)))) - 1
		if idx < 0 {
			idx = 0
		}
		summary.LatencyP95Ms = latencies[idx]
	}
	return summary
}

// GetTrialRun returns one run with its immutable case rows.
func (s *Service) GetTrialRun(ctx context.Context, tenantID, runID string) (*TrialRunView, error) {
	if tenantID == "" {
		return nil, httperr.BadRequest(40121, "a concrete tenant is required for a trial run")
	}
	run, err := s.Store.GetTrialRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("trial run not found")
	}
	return s.trialRunView(ctx, run, true)
}

// ListTrialRuns lists runs for one template or assistant, newest first.
func (s *Service) ListTrialRuns(ctx context.Context, tenantID string, in TrialRunInput) ([]TrialRunView, error) {
	if tenantID == "" {
		return nil, httperr.BadRequest(40121, "a concrete tenant is required for a trial run")
	}
	runs, err := s.Store.ListTrialRuns(ctx, tenantID, repository.TrialRunFilter{
		ScenarioTemplateID: strings.TrimSpace(in.ScenarioTemplateID),
		AssistantID:        strings.TrimSpace(in.AssistantID),
	}, 20)
	if err != nil {
		return nil, err
	}
	out := make([]TrialRunView, 0, len(runs))
	for i := range runs {
		view, err := s.trialRunView(ctx, &runs[i], false)
		if err != nil {
			return nil, err
		}
		out = append(out, *view)
	}
	return out, nil
}

// CancelTrialRun stops a queued/running run between cases. Terminal runs are
// returned unchanged (cancel is idempotent, never rewrites history).
func (s *Service) CancelTrialRun(ctx context.Context, tenantID, actorID, runID string) (*TrialRunView, error) {
	view, err := s.GetTrialRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if view.Status != model.TrialRunQueued && view.Status != model.TrialRunRunning {
		return view, nil
	}
	if cancel, ok := trialRunCancels.Load(runID); ok {
		if ch, isChan := cancel.(chan struct{}); isChan {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}
	now := time.Now().UTC()
	run, _ := s.Store.GetTrialRun(ctx, tenantID, runID)
	if run != nil {
		run.CancelRequestedAt = &now
		run.Status = model.TrialRunCancelled
		run.CompletedAt = &now
		run.FailureReason = "cancelled_by_user"
		_ = s.Store.UpdateTrialRun(ctx, run)
		s.recordTrialAudit(ctx, run, "trial_run.cancel", "SUCCESS")
	}
	return s.GetTrialRun(ctx, tenantID, runID)
}

// FailInterruptedTrialRuns terminalizes runs orphaned by a restart; they are
// never resumed. Called at startup.
func (s *Service) FailInterruptedTrialRuns(ctx context.Context) {
	if n, err := s.Store.FailInterruptedTrialRuns(ctx, time.Now().UTC().Add(-trialPendingTTL)); err != nil {
		logger.Warn("trial run interrupted sweep failed", "error", err)
	} else if n > 0 {
		logger.Warn("trial runs marked interrupted", "count", n)
	}
}

func (s *Service) trialRunView(ctx context.Context, run *model.TrialRun, withCases bool) (*TrialRunView, error) {
	snapshot := TrialAuthoringSnapshot{}
	_ = json.Unmarshal([]byte(run.AuthoringSnapshot), &snapshot)
	summary := TrialRunSummary{Mode: run.Mode, TopN: trialCaseTopN}
	if strings.TrimSpace(run.Summary) != "" {
		_ = json.Unmarshal([]byte(run.Summary), &summary)
	}
	view := &TrialRunView{
		ID: run.ID, ScenarioTemplateID: run.ScenarioTemplateID, TemplateVersion: run.TemplateVersion,
		AssistantID: run.AssistantID, AssistantKind: run.AssistantKind,
		EvalSetID: run.EvalSetID, EvalSetVersion: run.EvalSetVersion, EvalSetHash: run.EvalSetHash,
		PolicyID: run.PolicyID, PolicyHash: run.PolicyHash, Mode: run.Mode, Status: run.Status,
		FailureReason: run.FailureReason, IdempotencyKey: run.IdempotencyKey,
		Snapshot: snapshot, Summary: summary, GateEligible: false,
		CreatedAt: run.CreatedAt, StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
	}
	if !withCases {
		return view, nil
	}
	rows, err := s.Store.ListTrialCaseResults(ctx, run.TenantID, run.ID)
	if err != nil {
		return nil, err
	}
	view.Cases = make([]TrialCaseView, 0, len(rows))
	for _, row := range rows {
		var scores []float64
		_ = json.Unmarshal([]byte(row.TopNScores), &scores)
		view.Cases = append(view.Cases, TrialCaseView{
			CaseID: row.CaseID, Question: row.Question, Returned: row.Returned,
			HitAtK: row.HitAtK, HitUndefinedReason: row.HitUndefinedReason,
			TopScore: row.TopScore, TopNScores: scores, LatencyMs: row.LatencyMs,
			ErrorCode: row.ErrorCode,
		})
	}
	return view, nil
}

func (s *Service) recordTrialAudit(ctx context.Context, run *model.TrialRun, action, result string) {
	entry := &model.AuditLog{
		ID: id.New(), TenantID: run.TenantID, UserID: run.Actor, Action: action,
		Resource: "trial-run", ResourceID: run.ID, Result: result, At: time.Now().UTC(),
		DetailJSON: fmt.Sprintf(`{"scenario_template_id":%q,"assistant_id":%q,"status":%q}`,
			run.ScenarioTemplateID, run.AssistantID, run.Status),
	}
	if err := s.RecordAudit(ctx, entry); err != nil {
		logger.Warn("trial run audit failed", "run_id", run.ID, "error", err)
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func chunkScore(chunk map[string]interface{}) float64 {
	for _, key := range []string{"similarity", "score"} {
		if value, ok := chunk[key]; ok {
			return floatFromAny(value)
		}
	}
	return 0
}

func floatFromAny(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func intFromAny(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	}
	return 0
}

func shapeTrialEvidence(chunks []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(chunks))
	for _, chunk := range chunks {
		content := stringFromAny(chunk["content"])
		if len(content) > 200 {
			content = content[:200]
		}
		out = append(out, map[string]interface{}{
			"document_id": stringFromAny(chunk["document_id"]),
			"content":     content,
			"similarity":  chunkScore(chunk),
		})
	}
	return out
}

func floatPtr(v float64) *float64 { return &v }

func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".")
}

func roundFloat(v float64, digits int) float64 {
	scale := math.Pow(10, float64(digits))
	return math.Round(v*scale) / scale
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
