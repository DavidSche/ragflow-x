package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func createCompilationRecoveryFixture(t *testing.T) (*Service, *ragflow.Mock, string, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	svc := newAuthzSvc(t)
	mock := svc.RAGFlow.(*ragflow.Mock)
	tenant, err := svc.CreateTenant(ctx, "Compilation Recovery")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	operator, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "recovery-operator", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create operator: %v", err)
	}
	approver, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "recovery-approver", Password: "secret123", Role: model.RoleTenantAdmin,
	})
	if err != nil {
		t.Fatalf("create approver: %v", err)
	}
	ragflowDataset, err := mock.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "Recovery Docs"})
	if err != nil {
		t.Fatalf("create ragflow dataset: %v", err)
	}
	datasetID := id.New()
	dataset := &model.DatasetLink{
		ID: datasetID, TenantID: tenant.ID, RAGFlowDatasetID: ragflowDataset.ID,
		Name: "Recovery Docs", ProjectID: "", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatalf("create dataset link: %v", err)
	}
	document, err := mock.CreateDocument(ctx, dataset.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "recovery.pdf", Content: []byte("recovery")})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "pipeline-1"}, compilationPipelineDSL())
	if err := mock.UpdateDocumentParseConfig(ctx, dataset.RAGFlowDatasetID, document.ID, ragflow.DocumentParseConfigUpdate{
		Pipeline: true, PipelineID: "pipeline-1",
	}); err != nil {
		t.Fatalf("configure pipeline: %v", err)
	}
	svc.SetupWorker(fastWorkerConfig())
	svc.SetApprovalConfig(config.Approval{
		Enabled: true, DefaultExpireHours: 72, ExecutionMaxRetries: 1, PolicyCacheTTLSec: 0,
	})
	return svc, mock, tenant.ID, datasetID, document.ID, operator.ID, approver.ID
}

func dispatchCompilationForRecovery(t *testing.T, svc *Service, tenantID, datasetID, documentID string) *model.CompilationRun {
	t.Helper()
	ctx := context.Background()
	run, err := svc.TriggerCompilationRun(ctx, tenantID, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "recovery-" + documentID, Kind: model.CompilationKindPageIndex,
		DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger compilation run: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("dispatch compilation run: %v", err)
	}
	return run
}

func upsertCompilationRecoveryPolicy(t *testing.T, svc *Service, tenantID, approverID, action, idempotencyPrefix string) {
	t.Helper()
	policy := &model.ApprovalPolicy{
		ID: idempotencyPrefix + "-policy", TenantID: tenantID,
		ObjectType: model.ApprovalObjectCompilationRun, Action: action,
		Enabled: true, Priority: 100, ConditionsJSON: "{}",
		StepsJSON:   `[{"step_no":1,"name":"recovery approval","approver_type":"user","approver_value":"` + approverID + `","expire_hours":48}]`,
		ExpireHours: 48, Version: 1, CreatedBy: "system",
	}
	if err := svc.Store.UpsertApprovalPolicy(context.Background(), policy); err != nil {
		t.Fatalf("upsert approval policy: %v", err)
	}
}

func TestCompilationUnknownRecoveryRequiresTwoNoMatchResultsAndApproval(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenantID, datasetID, documentID, operatorID, approverID := createCompilationRecoveryFixture(t)
	upsertCompilationRecoveryPolicy(t, svc, tenantID, approverID, model.ApprovalActionConfirmDownstreamAbsent, "confirm")
	upsertCompilationRecoveryPolicy(t, svc, tenantID, approverID, model.ApprovalActionCreate, "replace")
	run := dispatchCompilationForRecovery(t, svc, tenantID, datasetID, documentID)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load dataset: %+v err=%v", upstream, err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "idle", UpdatedAt: time.Now().UTC()})

	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("first recovery probe: %v", err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("first recovery evidence pass: %v", err)
	}
	first, err := svc.Store.GetCompilationRun(ctx, tenantID, run.ID)
	if err != nil || first == nil {
		t.Fatalf("load first recovery run: %+v err=%v", first, err)
	}
	if first.RecoveryState != model.CompilationRecoveryInsufficientEvidence || first.RecoveryNoMatchCount != 1 {
		t.Fatalf("first probe must be insufficient: %+v %s", first, first.RecoveryEvidence)
	}

	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("second recovery probe: %v", err)
	}
	second, err := svc.Store.GetCompilationRun(ctx, tenantID, run.ID)
	if err != nil || second == nil {
		t.Fatalf("load second recovery run: %+v err=%v", second, err)
	}
	if second.RecoveryState != model.CompilationRecoveryNeedsApproval || second.RecoveryNoMatchCount != 2 {
		t.Fatalf("second probe must require approval: %+v %s", second, second.RecoveryEvidence)
	}
	if !strings.Contains(second.RecoveryEvidence, `"manifest_complete":true`) {
		t.Fatalf("recovery evidence lacks manifest completeness: %s", second.RecoveryEvidence)
	}

	queue, total, err := svc.ListCompilationRecovery(ctx, approverID, tenantID, datasetID, 1, 10)
	if err != nil || total != 1 || len(queue) != 1 {
		t.Fatalf("recovery queue: total=%d items=%+v err=%v", total, queue, err)
	}
	if queue[0].Evidence == nil || !queue[0].Evidence.NoMatch || queue[0].UnknownAgeBucket != "0-24h" {
		t.Fatalf("unexpected queue item: %+v", queue[0])
	}

	request := CompilationRecoveryApprovalRequest{
		Action: model.ApprovalActionConfirmDownstreamAbsent, IdempotencyKey: "confirm-absent-1",
		ObservationWindowSeconds:  int64(model.CompilationRecoveryWindow / time.Second),
		ObservationFirstMatchedAt: first.RecoveryFirstNoMatchAt.UTC().Format(time.RFC3339Nano),
		Reason:                    "two consistent no-match responses",
	}
	if err := svc.Authorize(ctx, operatorID, "execute", "compilation"); err != nil {
		t.Fatalf("operator compilation authorization: %v", err)
	}
	approval, err := svc.RequestCompilationRecoveryApproval(ctx, operatorID, tenantID, run.ID, request)
	if err != nil {
		t.Fatalf("request recovery approval: %v", err)
	}
	approved, err := svc.DecideApproval(ctx, approverID, tenantID, approval.ID, model.ApprovalDecisionApprove, "evidence complete")
	if err != nil || approved == nil || approved.Status != model.ApprovalStatusApproved {
		t.Fatalf("approve recovery: %+v err=%v", approved, err)
	}
	executor, ok := svc.approvalExecutorRegistry().Get(model.ApprovalObjectCompilationRun + "." + model.ApprovalActionConfirmDownstreamAbsent)
	if !ok {
		t.Fatal("downstream absent approval executor missing")
	}
	if err := executor.Validate(ctx, approved); err != nil {
		t.Fatalf("validate recovery approval: %v", err)
	}
	if _, err := executor.Execute(ctx, approved); err != nil {
		t.Fatalf("execute recovery approval: %v", err)
	}
	confirmed, err := svc.Store.GetCompilationRun(ctx, tenantID, run.ID)
	if err != nil || confirmed == nil {
		t.Fatalf("load confirmed run: %+v err=%v", confirmed, err)
	}
	if confirmed.State != model.CompilationRunReconciled || confirmed.FailureCode != model.CompilationFailureDownstreamNotFound {
		t.Fatalf("downstream absence was not confirmed: %+v", confirmed)
	}
	if confirmed.RecoveryState != model.CompilationRecoveryConfirmed || confirmed.RecoveryApprovedBy != approverID ||
		confirmed.RecoveryOperatedBy != operatorID || confirmed.RecoveryObservationSeconds < 0 {
		t.Fatalf("confirmation governance fields missing: %+v", confirmed)
	}
	confirmedQueue, confirmedTotal, err := svc.ListCompilationRecovery(ctx, approverID, tenantID, datasetID, 1, 10)
	if err != nil || confirmedTotal != 1 || len(confirmedQueue) != 1 || confirmedQueue[0].Run.ID != confirmed.ID {
		t.Fatalf("confirmed queue: total=%d items=%+v err=%v", confirmedTotal, confirmedQueue, err)
	}

	replacementRequest := CompilationRecoveryApprovalRequest{
		Action: model.ApprovalActionCreate, IdempotencyKey: "create-replacement-1",
		ObservationWindowSeconds:  int64(model.CompilationRecoveryWindow / time.Second),
		ObservationFirstMatchedAt: first.RecoveryFirstNoMatchAt.UTC().Format(time.RFC3339Nano),
		Reason:                    "approved replacement after downstream absence",
	}
	replacementApproval, err := svc.RequestCompilationRecoveryApproval(ctx, operatorID, tenantID, run.ID, replacementRequest)
	if err != nil {
		t.Fatalf("request replacement approval: %v", err)
	}
	approvedReplacement, err := svc.DecideApproval(ctx, approverID, tenantID, replacementApproval.ID, model.ApprovalDecisionApprove, "approved")
	if err != nil || approvedReplacement == nil || approvedReplacement.Status != model.ApprovalStatusApproved {
		t.Fatalf("approve replacement: %+v err=%v", approvedReplacement, err)
	}
	pendingQueue, pendingTotal, err := svc.ListCompilationRecovery(ctx, approverID, tenantID, datasetID, 1, 10)
	if err != nil || pendingTotal != 1 || len(pendingQueue) != 1 ||
		pendingQueue[0].Run.RecoveryState != model.CompilationRecoveryReplacementPending {
		t.Fatalf("replacement pending queue: total=%d items=%+v err=%v", pendingTotal, pendingQueue, err)
	}
	replacementExecutor, ok := svc.approvalExecutorRegistry().Get(model.ApprovalObjectCompilationRun + "." + model.ApprovalActionCreate)
	if !ok {
		t.Fatal("replacement approval executor missing")
	}
	if err := replacementExecutor.Validate(ctx, approvedReplacement); err != nil {
		t.Fatalf("validate replacement approval: %v", err)
	}
	result, err := replacementExecutor.Execute(ctx, approvedReplacement)
	if err != nil {
		t.Fatalf("execute replacement approval: %v", err)
	}
	newRunID, _ := result["new_run_id"].(string)
	if newRunID == "" {
		t.Fatalf("unexpected replacement result: %+v", result)
	}
	replacement, err := svc.Store.GetCompilationRun(ctx, tenantID, newRunID)
	if err != nil || replacement == nil {
		t.Fatalf("load replacement run: %+v err=%v", replacement, err)
	}
	if replacement.TriggeredByXRunID != run.ID || replacement.RecoveryState != model.CompilationRecoveryReplacement ||
		replacement.RecoveryApprovedBy != approverID || replacement.RecoveryOperatedBy != operatorID ||
		replacement.RecoveryEvidence != confirmed.RecoveryEvidence {
		t.Fatalf("replacement linkage incomplete: %+v", replacement)
	}
	if replacement.RecoveryDecisionAt == nil || replacement.RecoveryObservationSeconds < 0 ||
		replacement.ApprovalID != approvedReplacement.ID {
		t.Fatalf("replacement approval evidence incomplete: %+v", replacement)
	}
	updatedSource, err := svc.Store.GetCompilationRun(ctx, tenantID, run.ID)
	if err != nil || updatedSource == nil || updatedSource.RecoveryState != model.CompilationRecoveryReplacement {
		t.Fatalf("source replacement state incomplete: %+v err=%v", updatedSource, err)
	}
}

func TestCompilationRecoveryStopsWhenDownstreamMayExist(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenantID, datasetID, documentID, _, _ := createCompilationRecoveryFixture(t)
	run := dispatchCompilationForRecovery(t, svc, tenantID, datasetID, documentID)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load dataset: %+v err=%v", upstream, err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "idle", UpdatedAt: time.Now().UTC()})
	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("first no-match probe: %v", err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{
		State: "running", Progress: 0.2, Inflight: 2, UpdatedAt: time.Now().UTC(),
	})
	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("downstream exists probe: %v", err)
	}
	current, err := svc.Store.GetCompilationRun(ctx, tenantID, run.ID)
	if err != nil || current == nil {
		t.Fatalf("load recovery run: %+v err=%v", current, err)
	}
	if current.State != model.CompilationRunDispatched || current.RecoveryState != model.CompilationRecoveryWaitingDownstream {
		t.Fatalf("possible downstream must stay bound to old run: %+v", current)
	}
	if current.TriggeredByXRunID != "" || current.RecoveryNoMatchCount != 0 {
		t.Fatalf("replacement must not be created when downstream may exist: %+v", current)
	}
}

func TestCompilationRecoveryCrossTenantFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenantID, datasetID, documentID, _, _ := createCompilationRecoveryFixture(t)
	run := dispatchCompilationForRecovery(t, svc, tenantID, datasetID, documentID)
	upstream, err := svc.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil || upstream == nil {
		t.Fatalf("load dataset: %+v err=%v", upstream, err)
	}
	mock.SetCompilationStatus(upstream.RAGFlowDatasetID, ragflow.CompilationStatus{State: "unknown-lifecycle"})
	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("create unknown run: %v", err)
	}

	otherTenant, err := svc.CreateTenant(ctx, "Other Compilation Recovery")
	if err != nil {
		t.Fatalf("create other tenant: %v", err)
	}
	otherUser, err := svc.CreateUser(ctx, otherTenant.ID, "platform_admin", CreateUserRequest{
		Username: "other-recovery-operator", Password: "secret123", Role: model.RoleOperator,
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	queue, total, err := svc.ListCompilationRecovery(ctx, otherUser.ID, otherTenant.ID, "", 1, 10)
	if err != nil || total != 0 || len(queue) != 0 {
		t.Fatalf("cross tenant queue must be empty: total=%d items=%+v err=%v", total, queue, err)
	}
	if _, _, err := svc.ListCompilationRecovery(ctx, otherUser.ID, otherTenant.ID, datasetID, 1, 10); err == nil {
		t.Fatal("cross tenant dataset filter must not be visible")
	}
	if _, err := svc.ProbeCompilationRecoveryRun(ctx, otherUser.ID, otherTenant.ID, run.ID); err == nil {
		t.Fatal("cross tenant probe must fail closed")
	}
	approvalRequest := CompilationRecoveryApprovalRequest{
		Action: model.ApprovalActionConfirmDownstreamAbsent, IdempotencyKey: "cross-tenant-recovery",
		ObservationWindowSeconds:  int64(model.CompilationRecoveryWindow / time.Second),
		ObservationFirstMatchedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := svc.RequestCompilationRecoveryApproval(ctx, otherUser.ID, otherTenant.ID, run.ID, approvalRequest); err == nil {
		t.Fatal("cross tenant recovery approval must fail closed")
	}

	sameTenantQueue, sameTenantTotal, err := svc.ListCompilationRecovery(ctx, otherUser.ID, tenantID, "", 1, 10)
	if err == nil || sameTenantTotal != 0 || len(sameTenantQueue) != 0 {
		t.Fatalf("other tenant user must not read source tenant queue: total=%d items=%+v err=%v", sameTenantTotal, sameTenantQueue, err)
	}
}
