package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestGovernanceTemplateLifecycleAndRollback(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenantA, err := svc.CreateTenant(ctx, "Tenant A")
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := svc.CreateTenant(ctx, "Tenant B")
	if err != nil {
		t.Fatal(err)
	}

	input := ScenarioTemplateInput{
		Key: "policy-assistant", Name: "制度助手", AppTypes: []string{"chat", "search"},
		Payload: ScenarioTemplatePayload{
			Description: "统一制度口径", DatasetSuggestions: []string{"员工手册"},
			EvaluationQuestions: []string{"年假如何申请？"},
		},
	}
	asset, err := svc.CreateScenarioTemplate(ctx, tenantA.ID, "user-1", input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if asset.LatestVersion != 1 || asset.Source != model.TemplateSourceCustom {
		t.Fatalf("unexpected template: %+v", asset)
	}

	input.Name = "制度助手 V2"
	input.ChangeNote = "补充口径"
	asset, err = svc.UpdateScenarioTemplate(ctx, tenantA.ID, "user-1", asset.ID, input)
	if err != nil {
		t.Fatalf("update template: %v", err)
	}
	if asset.LatestVersion != 2 {
		t.Fatalf("expected version 2, got %d", asset.LatestVersion)
	}
	versions, err := svc.Store.ListScenarioTemplateVersions(ctx, tenantA.ID, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 2 {
		t.Fatalf("unexpected version history: %+v", versions)
	}

	exported, err := svc.ExportScenarioTemplate(ctx, tenantA.ID, asset.ID, 1)
	if err != nil {
		t.Fatalf("export template: %v", err)
	}
	if exported.Schema != scenarioTemplateSchema || exported.Version != 1 || exported.Name != "制度助手 V2" {
		t.Fatalf("unexpected export: %+v", exported)
	}

	imported, err := svc.ImportScenarioTemplate(ctx, tenantB.ID, "user-2", *exported)
	if err != nil {
		t.Fatalf("import template: %v", err)
	}
	if imported.TenantID != tenantB.ID || imported.Source != model.TemplateSourceImport || imported.LatestVersion != 1 {
		t.Fatalf("unexpected import: %+v", imported)
	}

	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatal(err)
	}
	copied, err := svc.CopyScenarioTemplate(ctx, admin.ID, tenantA.ID, asset.ID, tenantB.ID)
	if err != nil {
		t.Fatalf("copy template: %v", err)
	}
	if copied.TenantID != tenantB.ID || copied.Source != model.TemplateSourceCopy {
		t.Fatalf("unexpected copy: %+v", copied)
	}

	if _, err = svc.CreateEvalSetFromTemplate(ctx, tenantA.ID, "user-1", asset.ID, "制度回归"); err != nil {
		t.Fatalf("create eval from template: %v", err)
	}
	if err = svc.ArchiveScenarioTemplate(ctx, tenantA.ID, asset.ID); err != nil {
		t.Fatalf("archive template: %v", err)
	}
	archived, _ := svc.Store.GetScenarioTemplate(ctx, tenantA.ID, asset.ID)
	if archived == nil || archived.Status != model.AssetStatusArchived {
		t.Fatalf("template was not archived: %+v", archived)
	}
}

func TestPromptPolicySaveAndRollback(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Prompt Tenant")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.SavePromptPolicy(ctx, tenant.ID, "admin", PromptPolicyInput{
		Scope: model.PromptScopeTenant, PresetID: "policy", ProfileID: "safe", Payload: []byte(`{"temperature":0.2}`),
	})
	if err != nil {
		t.Fatalf("save first policy: %v", err)
	}
	second, err := svc.SavePromptPolicy(ctx, tenant.ID, "admin", PromptPolicyInput{
		Scope: model.PromptScopeTenant, PresetID: "policy", ProfileID: "strict", Payload: []byte(`{"temperature":0.1}`),
	})
	if err != nil {
		t.Fatalf("save second policy: %v", err)
	}
	active, err := svc.Store.GetActivePromptPolicy(ctx, tenant.ID, model.PromptScopeTenant, "")
	if err != nil || active == nil || active.ID != second.ID {
		t.Fatalf("active pointer is wrong: active=%+v err=%v", active, err)
	}
	rolledBack, err := svc.RollbackPromptPolicy(ctx, tenant.ID, first.ID)
	if err != nil || rolledBack == nil || rolledBack.ID != first.ID {
		t.Fatalf("rollback failed: %+v err=%v", rolledBack, err)
	}
	active, err = svc.Store.GetActivePromptPolicy(ctx, tenant.ID, model.PromptScopeTenant, "")
	if err != nil || active == nil || active.ID != first.ID {
		t.Fatalf("rollback pointer is wrong: active=%+v err=%v", active, err)
	}
}

func TestDatasetLifecycleAndBadcaseEvalLink(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Knowledge Tenant")
	if err != nil {
		t.Fatal(err)
	}
	dataset := &model.DatasetLink{ID: "ds-1", TenantID: tenant.ID, RAGFlowDatasetID: "rf-ds-1", Name: "员工手册"}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-24 * time.Hour)
	view, err := svc.UpdateKnowledgeLifecycle(ctx, tenant.ID, dataset.ID, DatasetLifecycleInput{
		OwnerID: "owner-1", SourceType: "manual", Sensitivity: "internal",
		ExpiresAt: &past, ReviewStatus: model.KnowledgeReviewCurrent, QualityScore: 90,
	})
	if err != nil {
		t.Fatalf("update lifecycle: %v", err)
	}
	if view.LifecycleStatus != model.KnowledgeReviewExpired {
		t.Fatalf("expected expired, got %s", view.LifecycleStatus)
	}

	event := &model.KnowledgeOpsEvent{
		RequestID: "bad-1", TenantID: tenant.ID, UserID: "user-1", AppType: "chat",
		AppID: "chat-1", SessionID: "session-1", Question: "报销流程是什么", QuestionHash: "hash",
		AnswerExcerpt: "错误答案", Status: model.KnowledgeOpsFailed,
		FeedbackComment: "答案错误",
	}
	if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	evalSet, err := svc.CreateEvalSet(ctx, tenant.ID, "owner-1", EvalSetInput{
		Name: "坏例回归", AppType: "chat", Cases: []EvalCaseInput{{Question: "初始问题"}},
	})
	if err != nil {
		t.Fatalf("create eval set: %v", err)
	}
	evalCase, err := svc.ConvertBadcaseToEvalCase(ctx, tenant.ID, false, event.ID, evalSet.ID)
	if err != nil {
		t.Fatalf("convert badcase: %v", err)
	}
	if evalCase.Source != model.EvalSourceBadcase || evalCase.SourceID != event.ID ||
		evalCase.SourceSessionID != event.SessionID || evalCase.SourceRequestID != event.RequestID ||
		evalCase.SourceFeedbackComment != event.FeedbackComment {
		t.Fatalf("unexpected eval case: %+v", evalCase)
	}
	updated, err := svc.Store.GetKnowledgeOpsEvent(ctx, tenant.ID, event.ID, false)
	if err != nil || updated == nil || updated.EvalSetID != evalSet.ID || updated.EvalCaseID != evalCase.ID {
		t.Fatalf("event link missing: %+v err=%v", updated, err)
	}
}

func TestGovernanceListsAreTenantIsolated(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenantA, _ := svc.CreateTenant(ctx, "Isolated A")
	tenantB, _ := svc.CreateTenant(ctx, "Isolated B")
	if _, err := svc.CreateScenarioTemplate(ctx, tenantA.ID, "u1", ScenarioTemplateInput{
		Key: "only-a", Name: "Only A", AppTypes: []string{"chat"}, Payload: ScenarioTemplatePayload{Description: "a"},
	}); err != nil {
		t.Fatal(err)
	}
	templatesB, _, err := svc.ListScenarioTemplates(ctx, tenantB.ID, false, 1, 10, repository.GovernanceFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(templatesB) != 0 {
		t.Fatalf("tenant B can see tenant A templates: %+v", templatesB)
	}
}

func TestGenerateMissingTemplateEvalSetsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Eval Generation Tenant")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.CreateScenarioTemplate(ctx, tenant.ID, "owner", ScenarioTemplateInput{
		Key: "policy", Name: "Policy", AppTypes: []string{"chat"}, Status: model.AssetStatusPublished,
		Payload: ScenarioTemplatePayload{EvaluationQuestions: []string{"年假如何申请？"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateScenarioTemplate(ctx, tenant.ID, "owner", ScenarioTemplateInput{
		Key: "support", Name: "Support", AppTypes: []string{"chat"}, Status: model.AssetStatusPublished,
		Payload: ScenarioTemplatePayload{EvaluationQuestions: []string{"工单如何提交？"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	noQuestions, err := svc.CreateScenarioTemplate(ctx, tenant.ID, "owner", ScenarioTemplateInput{
		Key: "empty", Name: "Empty", AppTypes: []string{"chat"}, Status: model.AssetStatusPublished,
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.GenerateMissingTemplateEvalSets(ctx, tenant.ID, "owner")
	if err != nil || created != 2 {
		t.Fatalf("first generation: created=%d err=%v", created, err)
	}
	created, err = svc.GenerateMissingTemplateEvalSets(ctx, tenant.ID, "owner")
	if err != nil || created != 0 {
		t.Fatalf("second generation: created=%d err=%v", created, err)
	}
	evalSets, total, err := svc.ListEvalSets(ctx, tenant.ID, false, 1, 100, repository.GovernanceFilter{})
	if err != nil || total != 2 || len(evalSets) != 2 {
		t.Fatalf("unexpected eval sets: total=%d len=%d err=%v", total, len(evalSets), err)
	}
	names := map[string]bool{}
	for _, item := range evalSets {
		names[item.Name] = true
		if item.Source != model.EvalSourceTemplate {
			t.Fatalf("unexpected eval set source: %+v", item)
		}
	}
	if !names[first.Name+"评测集"] || !names[second.Name+"评测集"] {
		t.Fatalf("generated suites missing: %+v", names)
	}
	if noQuestions == nil || noQuestions.ID == "" {
		t.Fatal("template fixture missing")
	}
}

func TestCreateChatFromScenarioTemplate(t *testing.T) {
	ctx := context.Background()
	svc, ctx, tenantA, _, template := newTemplateReleaseGateFixture(t, model.GateDecisionPass)
	tenantB, err := svc.CreateTenant(ctx, "Template Isolation B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChatFromScenarioTemplate(ctx, tenantA.ID, "owner", template.ID, ScenarioTemplateInstantiationInput{}); err == nil {
		t.Fatal("missing dataset suggestion should fail without opt-in")
	}
	out, err := svc.CreateChatFromScenarioTemplate(ctx, tenantA.ID, "owner", template.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.CreatedDatasetIDs) != 1 || out.Chat == nil {
		t.Fatalf("unexpected instantiation result: %+v", out)
	}
	datasets, err := svc.ListDatasets(ctx, tenantA.ID, repository.DatasetFilter{})
	if err != nil || len(datasets) != 1 {
		t.Fatalf("dataset suggestions were not materialized: len=%d err=%v", len(datasets), err)
	}
	if out.Chat.Name != "Enterprise Chat 草稿" || out.Chat.DatasetIDs == "" {
		t.Fatalf("unexpected chat: %+v", out.Chat)
	}
	if _, err := svc.CreateChatFromScenarioTemplate(ctx, tenantB.ID, "owner", template.ID, ScenarioTemplateInstantiationInput{}); err == nil {
		t.Fatal("tenant B must not instantiate tenant A template")
	}
}

func newTemplateReleaseGateFixture(t *testing.T, decision string) (*Service, context.Context, model.Tenant, *model.User, *model.ScenarioTemplateAsset) {
	t.Helper()
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Template Release Gate "+id.New()[:8])
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin: %+v err=%v", user, err)
	}
	asset, err := svc.CreateScenarioTemplate(ctx, tenant.ID, user.ID, ScenarioTemplateInput{
		Key: "gated-chat", Name: "Enterprise Chat", AppTypes: []string{"chat"},
		Status: model.AssetStatusPublished,
		Payload: ScenarioTemplatePayload{
			DatasetSuggestions:  []string{"gated-kb"},
			EvaluationQuestions: []string{"What is the return policy?"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidateResult, err := svc.CreateScenarioTemplateReleaseCandidate(ctx, tenant.ID, user.ID, asset.ID, ScenarioTemplateReleaseCandidateInput{})
	if err != nil {
		t.Fatal(err)
	}
	caseVersions, err := svc.Store.ListEvaluationCaseVersions(ctx, tenant.ID, candidateResult.EvalSetVersion.EvalSetID, candidateResult.EvalSetVersion.Version)
	if err != nil || len(caseVersions) == 0 {
		t.Fatalf("template case versions: len=%d err=%v", len(caseVersions), err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenant.ID, user.ID, EvaluationRunInput{
		ReleaseCandidateID:      candidateResult.Candidate.CandidateID,
		CandidateVersion:        candidateResult.Candidate.CandidateVersion,
		EvalSetID:               candidateResult.EvalSetVersion.EvalSetID,
		EvalSetVersion:          candidateResult.EvalSetVersion.Version,
		EvalSetHash:             candidateResult.EvalSetVersion.Hash,
		EvaluationPolicyVersion: "v1", EvaluationPolicyHash: "policy-hash",
		AggregationPolicyVersion: "v1", AggregationPolicyHash: "aggregation-hash",
		ExecutionSnapshotID: candidateResult.ExecutionSnapshot.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	casePass := decision != "FAIL_RUN"
	if _, err := svc.AddEvaluationCaseResult(ctx, tenant.ID, user.ID, EvaluationCaseResultInput{
		RunID: run.ID, CaseID: caseVersions[0].CaseID, CaseVersionID: caseVersions[0].ID,
		CaseVersionHash: caseVersions[0].Hash, ActualAnswer: "within policy", References: json.RawMessage(`[]`),
		Metrics: json.RawMessage(`{}`), Pass: &casePass,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteEvaluationRun(ctx, tenant.ID, run.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	bundle, err := svc.CreateEvidenceBundle(ctx, tenant.ID, user.ID, EvidenceBundleInput{
		ReleaseCandidateID: candidateResult.Candidate.CandidateID,
		CandidateVersion:   candidateResult.Candidate.CandidateVersion,
		EvaluationRunID:    run.ID,
		SecurityEvidence:   json.RawMessage(`{}`), PolicyEvidence: json.RawMessage(`{}`),
		RiskEvidence: json.RawMessage(`{}`), PermissionEvidence: json.RawMessage(`{}`),
		ConfigurationEvidence: json.RawMessage(`{}`), ApprovalEvidence: json.RawMessage(`{}`),
		EvidenceItems: json.RawMessage(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision == "" {
		return svc, ctx, tenant, user, asset
	}
	subGates := map[string]string{
		"security": "PASS", "policy": "PASS", "risk": "PASS", "permission": "PASS",
		"configuration": "PASS", "approval": "PASS",
	}
	expected := model.GateDecisionPass
	switch decision {
	case "BLOCK":
		subGates["approval"] = model.GateDecisionBlock
		expected = model.GateDecisionBlock
	case "FAIL_RUN":
		expected = model.GateDecisionBlock
	case "WAIVED_PRODUCTION":
		subGates["approval"] = "WAIVED"
		expected = model.GateDecisionBlock
	default:
		if decision != model.GateDecisionPass {
			t.Fatalf("unsupported gate fixture decision: %s", decision)
		}
	}
	subGatesJSON, err := json.Marshal(subGates)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := svc.EvaluateReleaseGate(ctx, tenant.ID, user.ID, GateDecisionInput{
		ReleaseCandidateID: candidateResult.Candidate.CandidateID,
		CandidateVersion:   candidateResult.Candidate.CandidateVersion,
		EvidenceBundleID:   bundle.ID, Environment: "production",
		SubGateStates: subGatesJSON, EnvironmentPolicyVersion: "v1",
		EnvironmentPolicyHash: "env-hash", Activate: true,
		Waiver: json.RawMessage(`{"actor":"admin","reason":"temporary","scope":"approval","expires_at":"2030-01-01T00:00:00Z","approval_id":"approval-1"}`),
	})
	if err != nil || gate.Decision != expected {
		t.Fatalf("template gate: %+v err=%v", gate, err)
	}
	return svc, ctx, tenant, user, asset
}

func TestCreateChatFromScenarioTemplateRequiresActivePassReleaseGate(t *testing.T) {
	svc, ctx, tenant, _, template := newTemplateReleaseGateFixture(t, "")
	if _, err := svc.CreateChatFromScenarioTemplate(ctx, tenant.ID, "owner", template.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
	}); err == nil {
		t.Fatal("template without an active PASS gate must not instantiate")
	}
	passSvc, passCtx, passTenant, _, passTemplate := newTemplateReleaseGateFixture(t, model.GateDecisionPass)
	out, err := passSvc.CreateChatFromScenarioTemplate(passCtx, passTenant.ID, "owner", passTemplate.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
	})
	if err != nil || out.Chat == nil || len(out.CreatedDatasetIDs) != 1 {
		t.Fatalf("active PASS gate should instantiate: %+v err=%v", out, err)
	}
	blockSvc, blockCtx, blockTenant, _, blockTemplate := newTemplateReleaseGateFixture(t, model.GateDecisionBlock)
	if _, err := blockSvc.CreateChatFromScenarioTemplate(blockCtx, blockTenant.ID, "owner", blockTemplate.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
	}); err == nil {
		t.Fatal("template with an active BLOCK gate must not instantiate")
	}
	waivedSvc, waivedCtx, waivedTenant, _, waivedTemplate := newTemplateReleaseGateFixture(t, "WAIVED_PRODUCTION")
	if _, err := waivedSvc.CreateChatFromScenarioTemplate(waivedCtx, waivedTenant.ID, "owner", waivedTemplate.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
	}); err == nil {
		t.Fatal("template with a production waiver must not instantiate")
	}
}

func TestScenarioTemplateApprovalExecutorRequiresActivePassReleaseGate(t *testing.T) {
	svc, ctx, tenant, admin, template := newTemplateReleaseGateFixture(t, "")
	payload, err := json.Marshal(map[string]any{
		"name": "Unapproved Chat", "dataset_suggestions": []string{"gated-kb"},
		"create_missing_datasets": true, "scenario_template_id": template.ID,
		"scenario_template_version": template.LatestVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval := &model.Approval{
		ID: "approval-template-chat", TenantID: tenant.ID, TargetTenantID: tenant.ID,
		RequesterID: admin.ID, ObjectType: model.ApprovalObjectChat,
		Action: model.ApprovalActionCreate, ObjectID: "new:Unapproved Chat",
		PayloadJSON: string(payload), SnapshotJSON: "{}",
	}
	if _, err := svc.executeChatCreate(ctx, nil, approval); err == nil {
		t.Fatal("approval executor must not bypass the active PASS gate")
	}
}

func TestCreateChatFromScenarioTemplateRejectsUnpublishedAndNonChat(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Instantiation Guards")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.CreateScenarioTemplate(ctx, tenant.ID, "owner", ScenarioTemplateInput{
		Key: "draft", Name: "Draft", AppTypes: []string{"chat"},
		Payload: ScenarioTemplatePayload{DatasetSuggestions: []string{"draft-kb"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChatFromScenarioTemplate(ctx, tenant.ID, "owner", draft.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
	}); err == nil {
		t.Fatal("draft template must not instantiate")
	}
	nonChat, err := svc.CreateScenarioTemplate(ctx, tenant.ID, "owner", ScenarioTemplateInput{
		Key: "search-only", Name: "Search Only", AppTypes: []string{"search"}, Status: model.AssetStatusPublished,
		Payload: ScenarioTemplatePayload{DatasetSuggestions: []string{"search-kb"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChatFromScenarioTemplate(ctx, tenant.ID, "owner", nonChat.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
	}); err == nil {
		t.Fatal("non-chat template must not instantiate")
	}
}

func TestScenarioTemplateChatApprovalExecutorCreatesPinnedVersion(t *testing.T) {
	ctx := context.Background()
	svc, ctx, tenant, admin, template := newTemplateReleaseGateFixture(t, model.GateDecisionPass)
	payload, err := svc.PrepareScenarioTemplateInstantiation(ctx, tenant.ID, template.ID, ScenarioTemplateInstantiationInput{
		CreateMissingDatasets: true,
		TemplateVersion:       template.LatestVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	approval := &model.Approval{
		ID: "approval-template-chat", TenantID: tenant.ID, TargetTenantID: tenant.ID,
		RequesterID: admin.ID, ObjectType: model.ApprovalObjectChat,
		Action: model.ApprovalActionCreate, ObjectID: "new:Approved Chat 草稿",
		PayloadJSON: string(raw), SnapshotJSON: "{}",
	}
	if err := validateApprovalChatCreate(ctx, svc, approval); err != nil {
		t.Fatal(err)
	}
	result, err := svc.executeChatCreate(ctx, nil, approval)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.Store.GetChatShadow(ctx, tenant.ID, result["chat_id"].(string), false)
	if err != nil || chat == nil {
		t.Fatalf("approved chat missing: %+v err=%v", chat, err)
	}
	if chat.OwnerID != admin.ID {
		t.Fatalf("approved chat owner mismatch: got %s", chat.OwnerID)
	}
	datasets, err := svc.ListDatasets(ctx, tenant.ID, repository.DatasetFilter{})
	if err != nil || len(datasets) != 1 {
		t.Fatalf("approved dataset suggestion not created: len=%d err=%v", len(datasets), err)
	}
	invalid := *approval
	invalid.PayloadJSON = fmt.Sprintf(`{"name":"draft","scenario_template_id":"%s","scenario_template_version":0}`, template.ID)
	if err := validateApprovalChatCreate(ctx, svc, &invalid); err == nil {
		t.Fatal("template approval without pinned version must fail")
	}
}
