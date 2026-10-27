package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// T3 (doc/224 §2.3) 引用精确定位 + 表格投影核心。v4-only 字段零值即关闭，
// 老 v4 protocol 判分字节级不变（DOI 回归防线）。本文件覆盖：
//   - T3.1 协议扩展解析与拒绝（v4-only，v3 拒绝）
//   - T3.2 表格 locator 提取与坐标断言（header-contextual，防“整块误引”）
//   - T3.2/T3.4 gate 表格准确率绝对 Floor + 三指标并集段（attribution_triple）
//   - T3.3 归因防火墙 warn/block 分档
//
// 验收口径（bd-272595）：xlsx/table acc >= 45%；locator 可解析覆盖 >= 门槛；
// T3 判分向前兼容（旧 protocol 不产出新诊断、不触发新 gate）。

// scopedPreregistrationT3Protocol extends the canonical v4 protocol with the
// T3 locator fields: cell-granularity citation scoring plus an absolute 45%
// xlsx/table accuracy floor and a 90% locator coverage budget.
const scopedPreregistrationT3Protocol = `{
	"contract_version":"scoped-paired-ab-v4","top_k":8,"min_samples":1,
	"min_hit_lift_pp":0,"max_p95_latency_delta_ms":300,
	"max_no_answer_lift_pp":0,"primary_metric":"citation_precision",
	"min_citation_precision_lift_pp":1,"min_compiled_artifact_effect_pairs":1,
	"retrieval_mode":"compiled_first","exclude_internal_rows":true,
	"compiled_first_max_chunks":2,"citation_granularity":"cell",
	"min_table_accuracy_pp":45,"min_locator_precision_pp":90,
	"answer_correctness_method":"expected_evidence_match",
	"require_citation_precision":true
}`

func TestParseScopedEvaluationProtocolT3(t *testing.T) {
	protocol, err := parseScopedEvaluationProtocol(scopedPreregistrationT3Protocol)
	if err != nil {
		t.Fatalf("valid T3 protocol must parse: %v", err)
	}
	if protocol.CitationGranularity != "cell" {
		t.Fatalf("T3 citation granularity not parsed: %+v", protocol.CitationGranularity)
	}
	if protocol.MinTableAccuracyPP != 45 || protocol.MinLocatorPrecisionPP != 90 {
		t.Fatalf("T3 floors not parsed: %+v / %+v", protocol.MinTableAccuracyPP, protocol.MinLocatorPrecisionPP)
	}
	// Zero value must keep legacy v4 protocols valid (their defaults remain).
	legacy, err := parseScopedEvaluationProtocol(strings.Replace(scopedPreregistrationT3Protocol,
		`"min_table_accuracy_pp":45,`, ``, 1))
	if err != nil || legacy.MinTableAccuracyPP != 0 {
		t.Fatalf("T3 floors must default to off for legacy v4: %+v err=%v", legacy, err)
	}
}

func TestParseScopedEvaluationProtocolT3Rejections(t *testing.T) {
	cases := map[string]string{
		"unknown granularity": strings.Replace(scopedPreregistrationT3Protocol,
			`"citation_granularity":"cell"`, `"citation_granularity":"dataset"`, 1),
		"negative table floor": strings.Replace(scopedPreregistrationT3Protocol,
			`"min_table_accuracy_pp":45`, `"min_table_accuracy_pp":-1`, 1),
		"table floor above 100": strings.Replace(scopedPreregistrationT3Protocol,
			`"min_table_accuracy_pp":45`, `"min_table_accuracy_pp":101`, 1),
		"locator floor above 100": strings.Replace(scopedPreregistrationT3Protocol,
			`"min_locator_precision_pp":90`, `"min_locator_precision_pp":150`, 1),
		"granularity on v3": strings.Replace(scopedPreregistrationV3Protocol,
			`"contract_version":"scoped-paired-ab-v3"`,
			`"contract_version":"scoped-paired-ab-v3","citation_granularity":"cell"`, 1),
		"table floor on v3": strings.Replace(scopedPreregistrationV3Protocol,
			`"contract_version":"scoped-paired-ab-v3"`,
			`"contract_version":"scoped-paired-ab-v3","min_table_accuracy_pp":45`, 1),
	}
	for name, raw := range cases {
		if _, err := parseScopedEvaluationProtocol(raw); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestScopedTableLocatorParsing(t *testing.T) {
	// Full markdown table: header row + a separator row + data rows.
	chunk := map[string]interface{}{
		"content": "| name | revenue |\n| --- | ---: |\n| north | 1200 |\n| south | 400 |",
	}
	locator := scopedTableLocatorForChunk(chunk)
	if locator == nil || locator.Kind != scopedTableKindMarkdown {
		t.Fatalf("markdown table must be located: %+v", locator)
	}
	if locator.ColCount != 2 || locator.RowCount != 2 {
		t.Fatalf("table dimensions wrong: %+v", locator)
	}
	if len(locator.Headers) != 2 || locator.Headers[0] != "name" || locator.Headers[1] != "revenue" {
		t.Fatalf("table header context must be preserved: %+v", locator.Headers)
	}
	if locator.HeaderText != "name | revenue" {
		t.Fatalf("table header text wrong: %q", locator.HeaderText)
	}
	// xlsx-family chunk recognised from meta without any markdown table.
	xlsx := scopedTableLocatorForChunk(map[string]interface{}{
		"content": "bin", "meta": map[string]interface{}{"file_name": "report.xlsx"},
	})
	if xlsx == nil || xlsx.Kind != scopedTableKindXLSX {
		t.Fatalf("xlsx meta must be located: %+v", xlsx)
	}
	// Plain prose carries no locator.
	if scopedTableLocatorForChunk(map[string]interface{}{"content": "just a paragraph"}) != nil {
		t.Fatal("prose chunk must not carry a table locator")
	}
	// Single header-only row is not enough to locate a table.
	if scopedTableLocatorForChunk(map[string]interface{}{"content": "| name | revenue |"}) != nil {
		t.Fatal("header-only row must not be treated as a located table")
	}
}

func TestScopedTableAssertionSatisfied(t *testing.T) {
	chunk := map[string]interface{}{
		"content": "| name | revenue |\n| --- | --- |\n| north | 1200 |\n| south | 400 |",
	}
	// A cell value without header context must NOT satisfy the assertion:
	// “整块误引” — the value appears somewhere in the table but not under the
	// expected header — is the exact failure T3.2 targets.
	if scopedTableAssertionSatisfied(&scopedTableAssertion{Value: "1200"}, chunk) {
		t.Fatal("value-only assertion must not pass without header context")
	}
	// Header-contextual cell match: 1200 lives under the revenue column.
	if !scopedTableAssertionSatisfied(&scopedTableAssertion{Header: "revenue", Value: "1200"}, chunk) {
		t.Fatal("header+value under the revenue column must satisfy the assertion")
	}
	if !scopedTableAssertionSatisfied(&scopedTableAssertion{Header: "name", Value: "south"}, chunk) {
		t.Fatal("south under name must satisfy the assertion")
	}
	// Value under a different header must not satisfy (cross-column guard).
	if scopedTableAssertionSatisfied(&scopedTableAssertion{Header: "revenue", Value: "south"}, chunk) {
		t.Fatal("a name value must not satisfy a revenue assertion")
	}
	// Explicit row/col coordinate must resolve precisely.
	if !scopedTableAssertionSatisfied(&scopedTableAssertion{Row: 1, Col: 1, Value: "north"}, chunk) {
		t.Fatal("row 1 col 1 must be north")
	}
	if scopedTableAssertionSatisfied(&scopedTableAssertion{Row: 1, Col: 1, Value: "400"}, chunk) {
		t.Fatal("row 1 col 1 must not contain south's revenue")
	}
	// Contains precondition gates the whole assertion.
	if scopedTableAssertionSatisfied(&scopedTableAssertion{Header: "revenue", Value: "1200", Contains: []string{"east"}}, chunk) {
		t.Fatal("contains precondition referencing absent content must reject")
	}
	if !scopedTableAssertionSatisfied(&scopedTableAssertion{Header: "revenue", Value: "1200", Contains: []string{"north"}}, chunk) {
		t.Fatal("contains precondition satisfied by present content must pass")
	}
	if scopedTableAssertionSatisfied(nil, chunk) {
		t.Fatal("nil assertion must never satisfy")
	}
}

// scopedTableGateResult builds a persisted case metric that carries the T3
// table diagnostics so scopedEvaluationGate aggregates them through the same
// decode path production results use.
func scopedTableGateResult(caseID string, tableAssertion, tableAccuracy bool, locatorRatio float64, hit bool) model.EvaluationCaseResult {
	metrics, _ := json.Marshal(map[string]interface{}{
		"task_type": "table", "hit_at_k": hit, "no_answer": false,
		"expected_evidence_present": true,
		"table_assertion_present":   tableAssertion,
		"table_accuracy":            tableAccuracy,
		"locator_usable_ratio":      locatorRatio,
	})
	pass := hit
	noAnswer := false
	return model.EvaluationCaseResult{
		ID: id.New(), CaseID: caseID, PairedSampleID: caseID, Metrics: string(metrics),
		Pass: &pass, HitAtK: &pass, AnswerCorrect: &pass, NoAnswer: &noAnswer,
	}
}

func TestScopedEvaluationT3GateTableAccuracy(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, false, 1.0, false),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, true, 1.0, true),
	}
	protocol := scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersionV4, TopK: 8, MinSamples: 1,
		MinHitLiftPP: 0, MaxP95LatencyDeltaMS: 300, MaxNoAnswerLiftPP: 0,
		AnswerCorrectnessMethod: "expected_evidence_match", RetrievalMode: scopedRetrievalModeCompiledFirst,
		ExcludeInternalRows: true, CompiledFirstMaxChunks: 2,
		CitationGranularity: "cell", MinTableAccuracyPP: 45, MinLocatorPrecisionPP: 90,
	}
	encoded, status, reason := scopedEvaluationGate(baseline, experiment, protocol)
	if status != model.EvaluationGatePass {
		t.Fatalf("table accuracy lift must pass the pre-registered floor: %s/%s", status, reason)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatal(err)
	}
	tableAccuracy, ok := payload["table_accuracy"].(map[string]interface{})
	if !ok {
		t.Fatalf("gate payload must carry the T3 table_accuracy segment: %+v", payload)
	}
	if tableAccuracy["lift_pp"].(float64) != 100 || tableAccuracy["baseline_pairs"].(float64) != 1 {
		t.Fatalf("table accuracy segment wrong: %+v", tableAccuracy)
	}
	triple, ok := payload["attribution_triple"].(map[string]interface{})
	if !ok {
		t.Fatalf("gate payload must carry the T3.4 attribution_triple segment: %+v", payload)
	}
	if _, exists := triple["citation_precision_lift_pp"]; !exists {
		t.Fatalf("attribution_triple must aggregate citation precision: %+v", triple)
	}
	if _, exists := triple["confident_wrong_pp"]; !exists {
		t.Fatalf("attribution_triple must include confident_wrong: %+v", triple)
	}
	if locator := payload["locator_precision_pp"].(float64); locator != 100 {
		t.Fatalf("locator precision must aggregate to 100: %+v", locator)
	}
}

func TestScopedEvaluationT3GateRejectsTableFloorShortfall(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, false, 1.0, false),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, false, 1.0, true),
	}
	status, reason := func() (string, string) {
		_, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
			ContractVersion: scopedEvaluationContractVersionV4, TopK: 8, MinSamples: 1,
			MinHitLiftPP: 0, MaxP95LatencyDeltaMS: 300, MaxNoAnswerLiftPP: 0,
			AnswerCorrectnessMethod: "expected_evidence_match", RetrievalMode: scopedRetrievalModeCompiledFirst,
			ExcludeInternalRows: true, CompiledFirstMaxChunks: 2,
			CitationGranularity: "cell", MinTableAccuracyPP: 45, MinLocatorPrecisionPP: 0,
		})
		return status, reason
	}()
	if status != model.EvaluationGateWait || !strings.Contains(reason, "table accuracy") {
		t.Fatalf("experiment table accuracy 0 below floor 45 must WAIT: %s/%s", status, reason)
	}
}

func TestScopedEvaluationT3GateRejectsLocatorCoverageShortfall(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, true, 1.0, true),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, true, 0.5, true),
	}
	_, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersionV4, TopK: 8, MinSamples: 1,
		MinHitLiftPP: 0, MaxP95LatencyDeltaMS: 300, MaxNoAnswerLiftPP: 0,
		AnswerCorrectnessMethod: "expected_evidence_match", RetrievalMode: scopedRetrievalModeCompiledFirst,
		ExcludeInternalRows: true, CompiledFirstMaxChunks: 2,
		CitationGranularity: "cell", MinTableAccuracyPP: 0, MinLocatorPrecisionPP: 90,
	})
	if status != model.EvaluationGateWait || !strings.Contains(reason, "locator") {
		t.Fatalf("locator coverage 50 below floor 90 must WAIT: %s/%s", status, reason)
	}
}

// TestScopedEvaluationT3GateLocatorSampleFrameHonorsTablePairs pins the locator
// sample-frame contract: locator coverage is averaged only over table-assertion
// pairs (same frame as table_accuracy), so ordinary non-table pairs cannot
// dilute the pre-registered locator budget below its floor.
func TestScopedEvaluationT3GateLocatorSampleFrameHonorsTablePairs(t *testing.T) {
	baseline := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, false, 1.0, false),
		"pair-2": scopedArtifactResult("pair-2", true, 0, 1),
		"pair-3": scopedArtifactResult("pair-3", true, 0, 1),
	}
	experiment := map[string]model.EvaluationCaseResult{
		"pair-1": scopedTableGateResult("pair-1", true, true, 1.0, true),
		"pair-2": scopedArtifactResult("pair-2", true, 0, 1),
		"pair-3": scopedArtifactResult("pair-3", true, 0, 1),
	}
	encoded, status, reason := scopedEvaluationGate(baseline, experiment, scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersionV4, TopK: 8, MinSamples: 1,
		MinHitLiftPP: 0, MaxP95LatencyDeltaMS: 300, MaxNoAnswerLiftPP: 0,
		AnswerCorrectnessMethod: "expected_evidence_match", RetrievalMode: scopedRetrievalModeCompiledFirst,
		ExcludeInternalRows: true, CompiledFirstMaxChunks: 2,
		CitationGranularity: "cell", MinTableAccuracyPP: 45, MinLocatorPrecisionPP: 90,
	})
	if status != model.EvaluationGatePass {
		t.Fatalf("non-table pairs must not dilute the locator floor: %s/%s", status, reason)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatal(err)
	}
	tableAccuracy := payload["table_accuracy"].(map[string]interface{})
	if tableAccuracy["baseline_pairs"].(float64) != 1 {
		t.Fatalf("table sample frame must be the table-assertion pairs only: %+v", tableAccuracy)
	}
	if payload["locator_precision_pp"].(float64) != 100 {
		t.Fatalf("locator precision must aggregate over table pairs only: %+v", payload["locator_precision_pp"])
	}
}

// TestScopedEvaluationT3BackwardCompatibleWithoutTableDiagnostics pins the
// regression line: a v4 run without table assertions produces no table
// metrics and no table gate — a legacy protocol byte-for-byte unchanged by T3.
func TestScopedEvaluationT3BackwardCompatibleWithoutTableDiagnostics(t *testing.T) {
	protocol := scopedEvaluationProtocol{
		ContractVersion: scopedEvaluationContractVersionV4, TopK: 8, MinSamples: 1,
		MinHitLiftPP: 0, MaxP95LatencyDeltaMS: 300, MaxNoAnswerLiftPP: 0,
		AnswerCorrectnessMethod: "expected_evidence_match", RetrievalMode: scopedRetrievalModeCompiledFirst,
		ExcludeInternalRows: true, CompiledFirstMaxChunks: 2,
	}
	encoded, status, reason := scopedEvaluationGate(
		map[string]model.EvaluationCaseResult{
			"pair-1": scopedArtifactResult("pair-1", true, 1, 0),
		},
		map[string]model.EvaluationCaseResult{
			"pair-1": scopedArtifactResult("pair-1", true, 1, 0),
		},
		protocol,
	)
	if status != model.EvaluationGatePass {
		t.Logf("legacy v4 gate reason: %s", reason)
		t.Logf("legacy v4 gate metrics: %s", encoded)
		t.Fatalf("legacy v4 gate must pass unchanged: %s/%s", status, reason)
	}
	if strings.Contains(encoded, "table_accuracy") || strings.Contains(encoded, "attribution_triple") {
		t.Fatal("legacy v4 gate must not emit T3 segments")
	}
}

// createScopedPairEvaluationT3 drives a v4 run whose case declares a table
// assertion and whose protocol carries the T3 floors, so the arm metrics must
// carry the injected table diagnostics.
func createScopedPairEvaluationT3(
	t *testing.T, svc *Service, ctx context.Context, tenantID, userID, strategyID string,
) *model.EvaluationRun {
	t.Helper()
	evalSet, err := svc.CreateEvaluationSetVersion(ctx, tenantID, userID, EvaluationSetVersionInput{
		EvalSetID: strategyID, Version: 1,
		Cases: json.RawMessage(`[{"case_id":"t3-table","paired_sample_id":"sample-t3"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEvaluationCaseVersion(ctx, tenantID, userID, EvaluationCaseVersionInput{
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, CaseID: "t3-table", CaseVersion: 1,
		Question: "north revenue?", ExpectedAnswer: "1200", ExpectedCitationDocIDs: "doc-1",
		Metadata:       json.RawMessage(`{"capability":"page_index","table_assertion":{"header":"revenue","value":"1200","contains":["north"]}}`),
		PairedSampleID: "sample-t3",
	}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.CreateEvaluationRun(ctx, tenantID, userID, EvaluationRunInput{
		EvaluationScope: model.EvaluationScopeCompilationStrategy, XStrategyID: strategyID,
		EvalSetID: evalSet.EvalSetID, EvalSetVersion: evalSet.Version, EvalSetHash: evalSet.Hash,
		EvaluationPolicyVersion: "t3-locator-v4", EvaluationPolicyHash: evalSet.Hash,
		AggregationPolicyVersion: "t3-locator-v4", AggregationPolicyHash: evalSet.Hash,
		Capability: model.EvaluationCapabilityPageIndex, ExecutionMode: "paired_ab",
		ExecutionConfig: json.RawMessage(scopedPreregistrationT3Protocol),
	})
	if err != nil {
		t.Fatal(err)
	}
	registerScopedEvaluationPreregistration(t, svc, ctx, tenantID, userID, model.EvaluationCapabilityPageIndex,
		json.RawMessage(scopedPreregistrationT3Protocol))
	return run
}

// TestScopedEvaluationT3ProjectionTablesLocateCellEvidence e2e-drives the T3
// protocol: the compiled leg returns a table-fragment chunk whose markdown
// rows carry the expected cell, the learner locator resolves it, the case
// assertion fires, and the packed reference records the coordinate basis.
func TestScopedEvaluationT3ProjectionTablesLocateCellEvidence(t *testing.T) {
	svc, ctx, tenant, user, strategy := newScopedEvaluationFixture(t)
	tableChunk := map[string]interface{}{
		"id": "tbl-1", "document_id": "kb-1",
		"content":     "| name | revenue |\n| --- | --- |\n| north | 1200 |\n| south | 400 |",
		"compile_kwd": "wiki", "source_doc_ids": []interface{}{"doc-1"},
	}
	stub := &scopedEvaluationStub{searchResults: map[bool]ragflow.SearchDatasetResult{
		true: {Chunks: []map[string]interface{}{tableChunk}},
		false: {Chunks: []map[string]interface{}{
			{"id": "raw-1", "document_id": "doc-1", "content": "| name | revenue |\n| north | 1200 |"},
		}},
	}}
	svc.RAGFlow = stub
	run := createScopedPairEvaluationT3(t, svc, ctx, tenant.ID, user.ID, strategy.ID)
	completed, err := svc.ExecuteScopedEvaluationRun(ctx, tenant.ID, run.ID)
	if err != nil {
		t.Fatalf("execute T3 A/B: %v", err)
	}
	if completed.Status != model.EvaluationRunCompleted {
		t.Fatalf("T3 run must complete: %+v", completed)
	}
	results, err := svc.Store.ListEvaluationCaseResults(ctx, tenant.ID, completed.ID)
	if err != nil || len(results) != 2 {
		t.Fatalf("expected 2 paired results: count=%d err=%v", len(results), err)
	}
	for _, result := range results {
		var metrics map[string]interface{}
		if err := json.Unmarshal([]byte(result.Metrics), &metrics); err != nil {
			t.Fatal(err)
		}
		if _, exists := metrics["table_assertion_present"]; !exists {
			t.Fatalf("T3 protocol must emit table diagnostics: %+v", metrics)
		}
		if exists := metrics["table_assertion_present"].(bool); exists != true {
			t.Fatalf("case declares a table assertion: %+v", metrics)
		}
		if metrics["table_accuracy"].(bool) != true {
			t.Fatalf("north/1200 under revenue must satisfy the table assertion: %+v", metrics)
		}
		if metrics["locator_usable_ratio"].(float64) != 1.0 {
			t.Fatalf("all returned table chunks must resolve a locator: %+v", metrics)
		}
		var refs []map[string]interface{}
		if err := json.Unmarshal([]byte(result.References), &refs); err != nil {
			t.Fatal(err)
		}
		sawLocator := false
		for _, ref := range refs {
			locator, ok := ref["table_locator"].(map[string]interface{})
			if !ok {
				continue
			}
			sawLocator = true
			headers := locator["headers"].([]interface{})
			if len(headers) != 2 || headers[0] != "name" || headers[1] != "revenue" {
				t.Fatalf("reference locator must preserve the full header context: %+v", locator)
			}
		}
		if !sawLocator {
			t.Fatal("T3 references must carry table_locator coordinate bases")
		}
	}
}

// TestAssessAttributionFirewall covers the generation attribution firewall
// (doc/224 §2.3 T3.3) warn/block two-level decisions.
func TestAssessAttributionFirewall(t *testing.T) {
	guarded := &model.ClaimValidation{Action: model.ClaimActionKeep}
	unguarded := &model.ClaimValidation{Action: model.ClaimActionFlag, ValidationStatus: model.ClaimValidationUnsupported, LLMClaim: "north revenue 1200"}
	// All claims guarded -> allowed, nothing to act on.
	allowed := assessAttributionFirewall([]*model.ClaimValidation{guarded}, factGuardModeWarn)
	if allowed.Decision != attributionFirewallDecisionAllowed || allowed.Ungated != 0 {
		t.Fatalf("guarded claims must be allowed: %+v", allowed)
	}
	// warn mode (the observational default): flag stays in the output but the
	// run records the ungated assertion so the spread is watchable.
	warned := assessAttributionFirewall([]*model.ClaimValidation{guarded, unguarded}, factGuardModeWarn)
	if warned.Decision != attributionFirewallDecisionWarn || warned.Ungated != 1 || warned.UngatedList[0]["llm_claim"] != "north revenue 1200" {
		t.Fatalf("warn mode must record the ungated assertion: %+v", warned)
	}
	// strict mode: the same unguarded assertion hard-blocks.
	blocked := assessAttributionFirewall([]*model.ClaimValidation{guarded, unguarded}, factGuardModeStrict)
	if blocked.Decision != attributionFirewallDecisionBlock || blocked.Ungated != 1 {
		t.Fatalf("strict mode must block unguarded assertions: %+v", blocked)
	}
	// regen mode stays observational like warn (contradicted claims already
	// 409 through validatePlannedFactClaimGate).
	regen := assessAttributionFirewall([]*model.ClaimValidation{guarded, unguarded}, factGuardModeRegen)
	if regen.Decision != attributionFirewallDecisionWarn {
		t.Fatalf("regen mode must stay observational for this stage: %+v", regen)
	}
	// off mode never invents attribution pressure.
	off := assessAttributionFirewall([]*model.ClaimValidation{guarded, unguarded}, factGuardModeOff)
	if off.Decision != attributionFirewallDecisionAllowed || off.Ungated != 0 {
		t.Fatalf("off mode must report allowed: %+v", off)
	}
	// Empty mode defaults to warn.
	defaulted := assessAttributionFirewall([]*model.ClaimValidation{guarded, unguarded}, "")
	if defaulted.Decision != attributionFirewallDecisionWarn || defaulted.Mode != factGuardModeWarn {
		t.Fatalf("empty mode must default to warn: %+v", defaulted)
	}
	// Payload bundles the ungated list for answer-run persistence.
	payload := attributionFirewallPayload(warned)
	if payload["ungated_claim_count"].(int) != 1 || len(payload["ungated_claims"].([]map[string]interface{})) != 1 {
		t.Fatalf("firewall payload must carry the ungated set: %+v", payload)
	}
}

// TestPlanRoutedMixedToolAnswerAttributionFirewallSegment e2e-drives the
// T3.3 wall segment through the planned answer main path (doc/224 §2.3): in
// warn mode the unverified assertion is projected with decision=warn and the
// ungated claim list; when every claim is verified the decision falls to
// allowed with nothing unguarded. strict still hard-fails through the existing
// 40996 gate (no wall segment is emitted because the gate never returns).
func TestPlanRoutedMixedToolAnswerAttributionFirewallSegment(t *testing.T) {
	ctx := context.Background()

	// warn mode: an unsupported concrete assertion stays in the answer and the
	// wall records it for the T3.4 confident-wrong spread watch.
	warnFixture := newPlannedFactGuardFixture(t, factGuardModeWarn)
	warnRequestID := warnFixture.answerRun.RequestID
	warnResult, err := warnFixture.service.PlanRoutedMixedToolAnswer(ctx, warnFixture.tenantID, warnFixture.userID, warnFixture.agentID, RoutedPlannedToolAnswerInput{
		SessionID: warnFixture.answerRun.SessionID, AssistantID: warnFixture.agentID,
		RequestID: warnRequestID, Question: "What was revenue?", Synthesize: true,
		FactClaims: []PlannedFactClaimInput{{
			LLMClaim: "Profit was ten million", FactIDs: []string{warnFixture.revenueFact.ID},
			FactKey: "profit", Value: "10,000,000", Unit: "usd", TimeRange: "FY2025",
		}},
	})
	if err != nil {
		t.Fatalf("plan flagged fact guarded answer in warn mode: %v", err)
	}
	var warnContent struct {
		FactGuard struct {
			Attribution struct {
				Decision         string `json:"decision"`
				Mode             string `json:"mode"`
				Total            int    `json:"total_claim_count"`
				Ungated          int    `json:"ungated_claim_count"`
				UngatedClaimsLen int    `json:"-"`
			} `json:"attribution"`
		} `json:"fact_guard"`
	}
	if err := json.Unmarshal([]byte(warnResult.Snapshot.Content), &warnContent); err != nil {
		t.Fatalf("decode attribution segment: %v", err)
	}
	if warnContent.FactGuard.Attribution.Decision != attributionFirewallDecisionWarn ||
		warnContent.FactGuard.Attribution.Mode != factGuardModeWarn ||
		warnContent.FactGuard.Attribution.Total != 1 || warnContent.FactGuard.Attribution.Ungated != 1 {
		t.Fatalf("warn attribution segment wrong: %+v", warnContent.FactGuard.Attribution)
	}

	// warn mode with a verified claim: nothing unguarded -> allowed.
	verified := newPlannedFactGuardFixture(t, factGuardModeWarn)
	verifiedResult, err := verified.service.PlanRoutedMixedToolAnswer(ctx, verified.tenantID, verified.userID, verified.agentID, RoutedPlannedToolAnswerInput{
		SessionID: verified.answerRun.SessionID, AssistantID: verified.agentID,
		RequestID: verified.answerRun.RequestID, Question: "What was revenue?", Synthesize: true,
		FactClaims: []PlannedFactClaimInput{{
			LLMClaim: "Revenue was ten million", FactIDs: []string{verified.revenueFact.ID},
			FactKey: "revenue", Value: "10,000,000", Unit: "usd", TimeRange: "FY2025",
		}},
	})
	if err != nil {
		t.Fatalf("plan verified answer in warn mode: %v", err)
	}
	var verifiedContent struct {
		FactGuard struct {
			Attribution struct {
				Decision string `json:"decision"`
				Mode     string `json:"mode"`
				Total    int    `json:"total_claim_count"`
				Ungated  int    `json:"ungated_claim_count"`
			} `json:"attribution"`
		} `json:"fact_guard"`
	}
	if err := json.Unmarshal([]byte(verifiedResult.Snapshot.Content), &verifiedContent); err != nil {
		t.Fatalf("decode verified attribution segment: %v", err)
	}
	if verifiedContent.FactGuard.Attribution.Decision != attributionFirewallDecisionAllowed ||
		verifiedContent.FactGuard.Attribution.Ungated != 0 {
		t.Fatalf("verified attribution must be allowed: %+v", verifiedContent.FactGuard.Attribution)
	}

	// strict mode with an unsupported assertion fails at the existing 40996
	// gate and never reaches the wall segment.
	strictFixture := newPlannedFactGuardFixture(t, factGuardModeStrict)
	_, err = strictFixture.service.PlanRoutedMixedToolAnswer(ctx, strictFixture.tenantID, strictFixture.userID, strictFixture.agentID, RoutedPlannedToolAnswerInput{
		SessionID: strictFixture.answerRun.SessionID, AssistantID: strictFixture.agentID,
		RequestID: strictFixture.answerRun.RequestID, Question: "What was revenue?", Synthesize: true,
		FactClaims: []PlannedFactClaimInput{{
			LLMClaim: "Profit was ten million", FactIDs: []string{strictFixture.revenueFact.ID},
			FactKey: "profit", Value: "10,000,000", Unit: "usd", TimeRange: "FY2025",
		}},
	})
	if businessErr, ok := err.(*httperr.Error); !ok || businessErr.Status != http.StatusConflict ||
		businessErr.Code != 40996 {
		t.Fatalf("strict unverified assertion must hard-fail 40996: %#v", err)
	}
}
