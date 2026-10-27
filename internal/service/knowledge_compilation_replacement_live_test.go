package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestLiveCompilationReplacementRunE2E closes the remaining Milestone 179 live
// gap "真实 RAGFlow unknown → probe → 审批 → 替代 run 演练" — the replacement-run
// leg beyond the confirm-downstream-absent drill that Milestone 200 recorded:
//
//  1. a real pipeline-bound dispatch whose ingest is dropped becomes governed
//     unknown with failure_code dispatch_result_unconfirmed;
//  2. two consistent real no-match probes demand approval;
//  3. the approval-gated confirm-downstream-absent reconciliation succeeds
//     with zero re-dispatch;
//  4. a second approval (action=create) executes the replacement run, which
//     REALLY dispatches to the live pipeline (dropped ingest count stays 1),
//     carries the full recovery linkage (source run, approval, evidence,
//     governance fields), and completes as confirmed with bound artifacts.
//
// STATUS 2026-10-16: authored but not yet executed live. The RAGFlow host
// (192.168.4.151) lost network reachability to the local model pool
// (192.168.31.211:8080), so document parse (embedding) and the compilation
// chat model cannot complete; see doc/develop/202 for the outage evidence.
// Run when connectivity is restored:
//
//	RGX_RAGFLOW_LIVE_BASE_URL=... RGX_ALLOW_COMPILATION_LIVE=1 \
//	  go test ./internal/service -run '^TestLiveCompilationReplacementRunE2E$' -count=1 -timeout 40m
func TestLiveCompilationReplacementRunE2E(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is required")
	}
	if os.Getenv("RGX_ALLOW_COMPILATION_LIVE") != "1" {
		t.Skip("RGX_ALLOW_COMPILATION_LIVE=1 is required for the replacement run live drill")
	}

	rawClient := ragflow.NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 30*time.Second, 4)
	dropping := &liveIngestDroppingClient{HTTPClient: rawClient}
	counting := &liveIngestCountingClient{Client: rawClient}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	groups, _, err := rawClient.ListCompilationTemplateGroups(ctx, ragflow.CompilationTemplateGroupFilter{
		Page: 1, PageSize: 100,
	})
	if err != nil {
		t.Fatalf("list live compilation template groups: %v", err)
	}
	var group *ragflow.CompilationTemplateGroup
	for index := range groups {
		for _, template := range groups[index].Templates {
			if strings.EqualFold(template.Kind, model.CompilationKindPageIndex) {
				selected := groups[index]
				group = &selected
				break
			}
		}
		if group != nil {
			break
		}
	}
	if group == nil {
		t.Skip("live replacement run drill requires a page_index compilation template group")
	}

	embeddingModel := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_EMBEDDING_MODEL"))
	if embeddingModel == "" {
		models, modelErr := rawClient.ListModels(ctx, "embedding")
		if modelErr != nil {
			t.Fatalf("list live embedding models: %v", modelErr)
		}
		for _, candidate := range models {
			if candidate.Name == "qwen3-embedding-0.6b" && candidate.Instance == "gpustack-local" &&
				candidate.Provider == "OpenAI-API-Compatible" {
				embeddingModel = candidate.ModelID
				break
			}
		}
		if embeddingModel == "" {
			t.Fatal("live replacement run drill requires qwen3-embedding-0.6b on gpustack-local/OpenAI-API-Compatible")
		}
	}
	chatModel := strings.TrimSpace(os.Getenv("RGX_COMPILATION_LIVE_CHAT_MODEL"))
	if chatModel == "" {
		chatModels, modelErr := rawClient.ListModels(ctx, "chat")
		if modelErr != nil {
			t.Fatalf("list live chat models: %v", modelErr)
		}
		for _, candidate := range chatModels {
			if strings.HasPrefix(candidate.Name, "glm-5.3-flash") {
				chatModel = candidate.ModelID
				break
			}
		}
		if chatModel == "" {
			t.Fatal("live replacement run drill requires glm-5.3-flash chat model")
		}
	}

	suffix := id.New()[:8]
	pipelineName := "rgx-replacement-run-" + suffix
	pipeline, pipelineErr := rawClient.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title: pipelineName, Description: "Disposable RAGFlow-X replacement run live drill",
		CanvasCategory: "dataflow_canvas",
		Dsl:            liveDispatchRacePipelineDSL(group.ID, embeddingModel, chatModel),
	})
	if pipelineErr != nil {
		t.Fatalf("create live replacement run pipeline: %v", pipelineErr)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := rawClient.DeleteAgent(cleanupCtx, pipeline.ID); err != nil {
			t.Logf("cleanup live replacement run pipeline %s: %v", pipeline.ID, err)
		}
	}()
	detail, err := rawClient.GetPipeline(ctx, pipeline.ID)
	if err != nil {
		t.Fatalf("get live replacement run pipeline: %v", err)
	}
	contract, parsed := inspectCompilationPipelineDSL(detail.DSL)
	if !parsed || !contractBool(contract, "has_parser") || !contractBool(contract, "has_chunker") ||
		!contractBool(contract, "has_compiler") || !contractBool(contract, "has_indexer") ||
		!contractBool(contract, "path_valid") {
		t.Fatalf("live replacement run pipeline is not compilation ready: %+v", contract)
	}

	upstreamID, documentID := prepareLiveCompilationDataset(t, ctx, rawClient, pipeline.ID)

	svc, _, tenant, user := newGovernanceFixture(t)
	svc.RAGFlow = dropping
	svc.SetupWorker(fastWorkerConfig())
	svc.SetApprovalConfig(config.Approval{
		Enabled: true, DefaultExpireHours: 72, ExecutionMaxRetries: 1, PolicyCacheTTLSec: 0,
	})
	operator, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "replacement-operator-" + strings.ToLower(id.New()[:8]),
		Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create operator: %v", err)
	}
	approver, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "replacement-approver-" + strings.ToLower(id.New()[:8]),
		Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create approver: %v", err)
	}
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant.ID, RAGFlowDatasetID: upstreamID,
		Name: "replacement-run-drill", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create dataset link: %v", err)
	}
	upsertCompilationRecoveryPolicy(t, svc, tenant.ID, approver.ID, model.ApprovalActionConfirmDownstreamAbsent, "live-confirm")
	upsertCompilationRecoveryPolicy(t, svc, tenant.ID, approver.ID, model.ApprovalActionCreate, "live-replace")

	waitForLiveCompilationIdle(t, ctx, rawClient, upstreamID, 12*time.Minute)

	// Phase 1: dropped dispatch -> governed unknown.
	run, err := svc.TriggerCompilationRun(ctx, tenant.ID, user.ID, dataset.ID, CompilationRunTriggerInput{
		IdempotencyKey: "live-replacement-" + id.New()[:8], Kind: model.CompilationKindPageIndex,
		DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger live replacement source run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
		t.Fatalf("dispatch live replacement source run: %v", err)
	}
	unknown, err := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
	if err != nil || unknown == nil {
		t.Fatalf("load unknown run: %+v err=%v", unknown, err)
	}
	if unknown.State != model.CompilationRunUnknown ||
		!strings.Contains(unknown.Detail, `"reason":"dispatch_result_unconfirmed"`) {
		t.Fatalf("dropped live ingest must fail closed into governed unknown: %+v", unknown)
	}
	if dropping.ingestCalls != 1 {
		t.Fatalf("exactly one (dropped) ingest attempt expected: %d", dropping.ingestCalls)
	}

	// Phase 2: two consistent real no-match probes -> approval required.
	svc.RAGFlow = rawClient
	var needsApproval *model.CompilationRun
	approvalDeadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(approvalDeadline) {
		if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
			t.Fatalf("live recovery probe: %v", err)
		}
		current, loadErr := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
		if loadErr != nil || current == nil {
			t.Fatalf("load recovery run: %+v err=%v", current, loadErr)
		}
		if current.RecoveryState == model.CompilationRecoveryNeedsApproval && current.RecoveryNoMatchCount >= 2 {
			needsApproval = current
			break
		}
		if current.State != model.CompilationRunUnknown && current.State != model.CompilationRunDispatched {
			t.Fatalf("recovery probe must keep the run unknown/dispatched until approval: %+v", current)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live replacement context ended: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if needsApproval == nil {
		current, _ := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
		t.Fatalf("live recovery never reached approval state: %+v", current)
	}
	if !strings.Contains(needsApproval.RecoveryEvidence, `"manifest_complete":true`) ||
		!strings.Contains(needsApproval.RecoveryEvidence, `"no_match":true`) {
		t.Fatalf("live recovery evidence lacks no-match manifest completeness: %s", needsApproval.RecoveryEvidence)
	}

	// Phase 3: confirm downstream absence with zero re-dispatch.
	if err := svc.Authorize(ctx, operator.ID, "execute", "compilation"); err != nil {
		t.Fatalf("operator compilation authorization: %v", err)
	}
	confirmRequest := CompilationRecoveryApprovalRequest{
		Action: model.ApprovalActionConfirmDownstreamAbsent, IdempotencyKey: "live-replacement-confirm-1",
		ObservationWindowSeconds:  int64(model.CompilationRecoveryWindow / time.Second),
		ObservationFirstMatchedAt: needsApproval.RecoveryFirstNoMatchAt.UTC().Format(time.RFC3339Nano),
		Reason:                    "live drill: two consistent no-match probe responses",
	}
	confirmApproval, err := svc.RequestCompilationRecoveryApproval(ctx, operator.ID, tenant.ID, run.ID, confirmRequest)
	if err != nil {
		t.Fatalf("request live confirmation approval: %v", err)
	}
	approvedConfirm, err := svc.DecideApproval(ctx, approver.ID, tenant.ID, confirmApproval.ID,
		model.ApprovalDecisionApprove, "live evidence complete")
	if err != nil || approvedConfirm == nil || approvedConfirm.Status != model.ApprovalStatusApproved {
		t.Fatalf("approve live confirmation: %+v err=%v", approvedConfirm, err)
	}
	confirmExecutor, ok := svc.approvalExecutorRegistry().Get(
		model.ApprovalObjectCompilationRun + "." + model.ApprovalActionConfirmDownstreamAbsent)
	if !ok {
		t.Fatal("downstream absent approval executor missing")
	}
	if err := confirmExecutor.Validate(ctx, approvedConfirm); err != nil {
		t.Fatalf("validate live confirmation approval: %v", err)
	}
	if _, err := confirmExecutor.Execute(ctx, approvedConfirm); err != nil {
		t.Fatalf("execute live confirmation approval: %v", err)
	}
	confirmed, err := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
	if err != nil || confirmed == nil {
		t.Fatalf("load confirmed run: %+v err=%v", confirmed, err)
	}
	if confirmed.State != model.CompilationRunReconciled || confirmed.FailureCode != model.CompilationFailureDownstreamNotFound {
		t.Fatalf("live downstream absence was not confirmed: %+v", confirmed)
	}
	if dropping.ingestCalls != 1 {
		t.Fatalf("confirmation must never re-dispatch: ingest calls=%d", dropping.ingestCalls)
	}

	// Phase 4: approved replacement run REALLY dispatches to the live pipeline.
	replacementRequest := CompilationRecoveryApprovalRequest{
		Action: model.ApprovalActionCreate, IdempotencyKey: "live-replacement-create-1",
		ObservationWindowSeconds:  int64(model.CompilationRecoveryWindow / time.Second),
		ObservationFirstMatchedAt: needsApproval.RecoveryFirstNoMatchAt.UTC().Format(time.RFC3339Nano),
		Reason:                    "live drill: approved replacement after downstream absence",
	}
	replacementApproval, err := svc.RequestCompilationRecoveryApproval(ctx, operator.ID, tenant.ID, run.ID, replacementRequest)
	if err != nil {
		t.Fatalf("request live replacement approval: %v", err)
	}
	approvedReplacement, err := svc.DecideApproval(ctx, approver.ID, tenant.ID, replacementApproval.ID,
		model.ApprovalDecisionApprove, "approved replacement")
	if err != nil || approvedReplacement == nil || approvedReplacement.Status != model.ApprovalStatusApproved {
		t.Fatalf("approve live replacement: %+v err=%v", approvedReplacement, err)
	}
	replacementExecutor, ok := svc.approvalExecutorRegistry().Get(
		model.ApprovalObjectCompilationRun + "." + model.ApprovalActionCreate)
	if !ok {
		t.Fatal("replacement approval executor missing")
	}
	if err := replacementExecutor.Validate(ctx, approvedReplacement); err != nil {
		t.Fatalf("validate live replacement approval: %v", err)
	}
	result, err := replacementExecutor.Execute(ctx, approvedReplacement)
	if err != nil {
		t.Fatalf("execute live replacement approval: %v", err)
	}
	newRunID, _ := result["new_run_id"].(string)
	if newRunID == "" {
		t.Fatalf("unexpected replacement result: %+v", result)
	}
	svc.RAGFlow = counting
	if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
		t.Fatalf("dispatch live replacement run: %v", err)
	}
	replacement, err := svc.Store.GetCompilationRun(ctx, tenant.ID, newRunID)
	if err != nil || replacement == nil {
		t.Fatalf("load replacement run: %+v err=%v", replacement, err)
	}
	if replacement.State != model.CompilationRunDispatched || counting.ingestCalls.Load() < 1 {
		t.Fatalf("replacement run must really dispatch: state=%s ingest=%d",
			replacement.State, counting.ingestCalls.Load())
	}
	if replacement.TriggeredByXRunID != run.ID || replacement.RecoveryState != model.CompilationRecoveryReplacement ||
		replacement.RecoveryApprovedBy != approver.ID || replacement.RecoveryOperatedBy != operator.ID ||
		replacement.RecoveryEvidence != confirmed.RecoveryEvidence || replacement.ApprovalID != approvedReplacement.ID {
		t.Fatalf("replacement linkage incomplete: %+v", replacement)
	}

	// Phase 5: the replacement run completes as confirmed with bound artifacts.
	deadline := time.Now().Add(20 * time.Minute)
	var replacementConfirmed *model.CompilationRun
	for time.Now().Before(deadline) {
		if err := svc.ProcessCompilationRuns(ctx, tenant.ID, dataset.ID, ""); err != nil {
			t.Fatalf("reconcile live replacement run: %v", err)
		}
		current, loadErr := svc.Store.GetCompilationRun(ctx, tenant.ID, newRunID)
		if loadErr != nil {
			t.Fatalf("load live replacement run: %v", loadErr)
		}
		if current != nil && current.State == model.CompilationRunConfirmed {
			replacementConfirmed = current
			break
		}
		if current != nil && current.State == model.CompilationRunFailed {
			t.Fatalf("live replacement run failed: %s", current.Detail)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live replacement context ended: %v", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	if replacementConfirmed == nil {
		current, _ := svc.Store.GetCompilationRun(ctx, tenant.ID, newRunID)
		t.Fatalf("live replacement run did not confirm in time: %+v", current)
	}
	if !strings.Contains(replacementConfirmed.Detail, `"artifact_run_binding":true`) ||
		!strings.Contains(replacementConfirmed.Detail, `"artifact_hash_binding":true`) {
		t.Fatalf("confirmed live replacement lacks artifact binding evidence: %s", replacementConfirmed.Detail)
	}
	artifacts, _, err := svc.Store.ListArtifactProjections(ctx, tenant.ID, dataset.ID, newRunID, 1, 10)
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("expected live replacement artifact projections: count=%d err=%v", len(artifacts), err)
	}
	for _, artifact := range artifacts {
		if artifact.State != model.ArtifactProjectionReady || artifact.ContentHash == "" {
			t.Fatalf("live replacement artifact must be ready with content hash: %+v", artifact)
		}
		snapshot := map[string]interface{}{}
		if err := json.Unmarshal([]byte(artifact.ArtifactJSON), &snapshot); err != nil {
			t.Fatalf("decode live replacement artifact snapshot: %v", err)
		}
		matched, _ := snapshot["matched_document_ids"].([]interface{})
		if len(matched) == 0 || matched[0] != documentID {
			t.Fatalf("live replacement artifact must bind to the source document: %+v", snapshot)
		}
	}
	updatedSource, err := svc.Store.GetCompilationRun(ctx, tenant.ID, run.ID)
	if err != nil || updatedSource == nil || updatedSource.RecoveryState != model.CompilationRecoveryReplacement {
		t.Fatalf("source replacement state incomplete: %+v err=%v", updatedSource, err)
	}
	t.Logf("live replacement run closed loop complete: source=%s replacement=%s artifacts=%d",
		run.ID, newRunID, len(artifacts))
}
