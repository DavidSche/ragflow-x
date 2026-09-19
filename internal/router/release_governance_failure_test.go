package router_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

type governanceChain struct {
	CandidateID      string
	CandidateVersion int64
	SnapshotID       string
	SnapshotHash     string
	EvalSetID        string
	EvalSetVersion   int64
	EvalSetHash      string
	CaseVersionID    string
	CaseVersionHash  string
	RunID            string
	EvidenceID       string
}

func governanceManifest() string {
	sections := map[string]interface{}{}
	for _, name := range []string{
		"target", "prompt", "knowledge", "modelRoute", "policy", "catalog",
		"router", "tools", "retrievalConfig", "executionConfig",
	} {
		sections[name] = map[string]string{"version": "v1", "hash": "hash-" + name}
	}
	data, _ := json.Marshal(sections)
	return string(data)
}

func decodeGovernanceData(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response %s: %v", body, err)
	}
	if len(out.Data) == 0 {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out.Data, &data); err != nil {
		t.Fatalf("decode data %s: %v", out.Data, err)
	}
	return data
}

func governanceRequest(
	t *testing.T, app *testApp, token, action, method, path string,
	input interface{}, wantStatus int,
) map[string]interface{} {
	t.Helper()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	resp, raw := app.doAuth(t, method, path, token, body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s status = %d, want %d: %s", action, resp.StatusCode, wantStatus, raw)
	}
	data := decodeGovernanceData(t, raw)
	if wantStatus >= 200 && wantStatus < 300 && data == nil {
		t.Fatalf("%s returned no data: %s", action, raw)
	}
	return data
}

func governanceString(t *testing.T, data map[string]interface{}, field string) string {
	t.Helper()
	value, _ := data[field].(string)
	if value == "" {
		t.Fatalf("%s is empty", field)
	}
	return value
}

func governanceInt(t *testing.T, data map[string]interface{}, field string) int64 {
	t.Helper()
	value, ok := data[field].(float64)
	if !ok {
		t.Fatalf("%s is missing or not numeric: %v", field, data[field])
	}
	return int64(value)
}

func newGovernanceChainThroughRun(
	t *testing.T, app *testApp, token string, pass bool, failed bool,
) governanceChain {
	t.Helper()
	identity := id.New()[:12]
	candidate := governanceRequest(t, app, token, "create candidate", http.MethodPost, "/api/v1/release-candidates", map[string]interface{}{
		"target_type": "assistant", "target_id": identity, "target_version": "v2",
		"candidate_id": identity, "candidate_version": 1, "change_summary": "failure E2E",
		"candidate_manifest": json.RawMessage(governanceManifest()),
	}, http.StatusOK)
	candidateID := governanceString(t, candidate, "candidate_id")
	candidateVersion := governanceInt(t, candidate, "candidate_version")

	governanceRequest(t, app, token, "ready candidate", http.MethodPost,
		"/api/v1/release-candidates/"+candidateID+"/ready", map[string]interface{}{}, http.StatusOK)

	snapshot := governanceRequest(t, app, token, "create snapshot", http.MethodPost, "/api/v1/execution-snapshots", map[string]interface{}{
		"release_candidate_id": candidateID, "candidate_version": candidateVersion,
		"snapshot_schema_version": "v1", "execution_config": map[string]interface{}{"mode": "read_only"},
	}, http.StatusOK)

	evalSet := governanceRequest(t, app, token, "create eval set", http.MethodPost, "/api/v1/evaluation-set-versions", map[string]interface{}{
		"eval_set_id": identity + "-set", "version": 1,
		"cases": []map[string]interface{}{{"case_id": "case-1", "question": "What is X?"}},
	}, http.StatusOK)
	evalSetID := governanceString(t, evalSet, "eval_set_id")
	evalSetVersion := governanceInt(t, evalSet, "version")
	evalSetHash := governanceString(t, evalSet, "hash")

	caseVersion := governanceRequest(t, app, token, "create case version", http.MethodPost, "/api/v1/evaluation-case-versions", map[string]interface{}{
		"eval_set_id": evalSetID, "eval_set_version": evalSetVersion, "case_id": "case-1",
		"case_version": 1, "question": "What is X?", "expected_answer": "X is 1",
	}, http.StatusOK)

	run := governanceRequest(t, app, token, "create run", http.MethodPost, "/api/v1/evaluation-runs", map[string]interface{}{
		"release_candidate_id": candidateID, "candidate_version": candidateVersion,
		"eval_set_id": evalSetID, "eval_set_version": evalSetVersion, "eval_set_hash": evalSetHash,
		"evaluation_policy_version": "v1", "evaluation_policy_hash": "policy-hash",
		"aggregation_policy_version": "v1", "aggregation_policy_hash": "aggregation-hash",
		"execution_snapshot_id": governanceString(t, snapshot, "id"),
	}, http.StatusOK)
	runID := governanceString(t, run, "id")

	chain := governanceChain{
		CandidateID: candidateID, CandidateVersion: candidateVersion,
		SnapshotID: governanceString(t, snapshot, "id"), SnapshotHash: governanceString(t, snapshot, "snapshot_hash"),
		EvalSetID: evalSetID, EvalSetVersion: evalSetVersion, EvalSetHash: evalSetHash,
		CaseVersionID: governanceString(t, caseVersion, "id"), CaseVersionHash: governanceString(t, caseVersion, "hash"),
		RunID: runID,
	}
	if !failed {
		governanceRequest(t, app, token, "add case result", http.MethodPost, "/api/v1/evaluation-runs/"+runID+"/case-results", map[string]interface{}{
			"run_id": runID, "case_id": "case-1", "case_version_id": chain.CaseVersionID,
			"case_version_hash": chain.CaseVersionHash, "actual_answer": "X is 1",
			"references": []interface{}{}, "metrics": map[string]interface{}{}, "pass": pass,
		}, http.StatusOK)
	}
	completeStatus := http.StatusOK
	governanceRequest(t, app, token, "complete run", http.MethodPost, "/api/v1/evaluation-runs/"+runID+"/complete", map[string]interface{}{
		"failed": failed, "failure_reason": "execution crashed",
	}, completeStatus)
	return chain
}

func newGovernanceChainThroughEvidence(t *testing.T, app *testApp, token string) governanceChain {
	t.Helper()
	return newGovernanceChainThroughEvidenceWithPass(t, app, token, true)
}

func newGovernanceChainThroughEvidenceWithPass(t *testing.T, app *testApp, token string, pass bool) governanceChain {
	t.Helper()
	chain := newGovernanceChainThroughRun(t, app, token, pass, false)
	evidence := governanceRequest(t, app, token, "create evidence", http.MethodPost, "/api/v1/evidence-bundles", map[string]interface{}{
		"release_candidate_id": chain.CandidateID, "candidate_version": chain.CandidateVersion,
		"evaluation_run_id": chain.RunID, "security_evidence": map[string]interface{}{},
		"policy_evidence": map[string]interface{}{}, "risk_evidence": map[string]interface{}{},
		"permission_evidence": map[string]interface{}{}, "configuration_evidence": map[string]interface{}{},
		"approval_evidence": map[string]interface{}{}, "evidence_items": []interface{}{},
	}, http.StatusOK)
	chain.EvidenceID = governanceString(t, evidence, "id")
	return chain
}

func governanceGateInput(chain governanceChain, environment, decision string) map[string]interface{} {
	return map[string]interface{}{
		"release_candidate_id": chain.CandidateID, "candidate_version": chain.CandidateVersion,
		"evidence_bundle_id": chain.EvidenceID, "environment": environment,
		"sub_gate_states": map[string]string{
			"security": decision, "policy": decision, "risk": decision,
			"permission": decision, "configuration": decision, "approval": decision,
		},
		"environment_policy_version": "v1", "environment_policy_hash": "env-hash", "activate": true,
	}
}

func TestE2E_EvaluationBusinessFailBlocksReleaseAndCreatesIssue(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	chain := newGovernanceChainThroughEvidenceWithPass(t, app, token, false)

	gate := governanceRequest(t, app, token, "business-fail gate", http.MethodPost,
		"/api/v1/release-gates", governanceGateInput(chain, "production", "PASS"), http.StatusOK)
	if got := governanceString(t, gate, "decision"); got != "BLOCK" {
		t.Fatalf("gate decision = %s, want BLOCK", got)
	}
	release, raw := app.doAuth(t, http.MethodPost, "/api/v1/releases", token, []byte(`{
		"release_candidate_id":"`+chain.CandidateID+`","candidate_version":1,
		"snapshot_id":"`+chain.SnapshotID+`","gate_decision_id":"`+governanceString(t, gate, "id")+`",
		"environment":"production"
	}`))
	if release.StatusCode != http.StatusForbidden {
		t.Fatalf("business-fail release status = %d, want 403: %s", release.StatusCode, raw)
	}
	issue := governanceRequest(t, app, token, "create quality issue", http.MethodPost, "/api/v1/quality-issues", map[string]interface{}{
		"source": "evaluation", "source_id": chain.RunID, "title": "Evaluation business failure",
		"evidence": map[string]interface{}{"run_id": chain.RunID}, "owner": "quality",
		"resolution_target_type": "assistant", "resolution_target_id": chain.CandidateID,
		"resolution_target_version": "v2", "eval_case_id": "case-1",
	}, http.StatusOK)
	if got := governanceString(t, issue, "status"); got != "OPEN" {
		t.Fatalf("quality issue status = %s, want OPEN", got)
	}
}

func TestE2E_FailedEvaluationRunCannotBecomeEvidence(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	chain := newGovernanceChainThroughRun(t, app, token, true, true)

	resp, raw := app.doAuth(t, http.MethodPost, "/api/v1/evidence-bundles", token, []byte(`{
		"release_candidate_id":"`+chain.CandidateID+`","candidate_version":1,
		"evaluation_run_id":"`+chain.RunID+`","security_evidence":{},"policy_evidence":{},
		"risk_evidence":{},"permission_evidence":{},"configuration_evidence":{},
		"approval_evidence":{},"evidence_items":[]
	}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("failed run evidence status = %d, want 400: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "evidence chain binding mismatch") {
		t.Fatalf("unexpected evidence rejection: %s", raw)
	}
}

func TestE2E_SnapshotHashTamperingIsRejected(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	chain := newGovernanceChainThroughEvidence(t, app, token)
	governanceRequest(t, app, token, "baseline gate", http.MethodPost,
		"/api/v1/release-gates", governanceGateInput(chain, "development", "PASS"), http.StatusOK)

	if err := app.db.Exec("DROP TRIGGER IF EXISTS trg_execution_snapshot_guard").Error; err != nil {
		t.Fatal(err)
	}
	if err := app.db.Table("rgx_execution_snapshot").Where("id = ?", chain.SnapshotID).
		Update("execution_config", `{"mode":"tampered"}`).Error; err != nil {
		t.Fatal(err)
	}
	resp, raw := app.doAuth(t, http.MethodPost, "/api/v1/release-gates", token, []byte(`{
		"release_candidate_id":"`+chain.CandidateID+`","candidate_version":1,
		"evidence_bundle_id":"`+chain.EvidenceID+`","environment":"development",
		"sub_gate_states":{"security":"PASS","policy":"PASS","risk":"PASS","permission":"PASS","configuration":"PASS","approval":"PASS"},
		"environment_policy_version":"v1","environment_policy_hash":"env-hash"
	}`))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "execution snapshot hash mismatch") {
		t.Fatalf("tampered snapshot status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestE2E_EvalSetVersionTamperingIsRejected(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	chain := newGovernanceChainThroughRun(t, app, token, true, false)

	err := app.db.Table("rgx_evaluation_set_version").
		Where("eval_set_id = ? AND version = ?", chain.EvalSetID, chain.EvalSetVersion).
		Update("hash", "tampered").Error
	if err == nil {
		t.Fatal("expected immutable evaluation set tampering to fail")
	}
	resp, raw := app.doAuth(t, http.MethodPost, "/api/v1/evaluation-runs", token, []byte(`{
		"release_candidate_id":"`+chain.CandidateID+`","candidate_version":1,
		"eval_set_id":"`+chain.EvalSetID+`","eval_set_version":1,"eval_set_hash":"tampered",
		"evaluation_policy_version":"v1","evaluation_policy_hash":"policy-hash",
		"aggregation_policy_version":"v1","aggregation_policy_hash":"aggregation-hash",
		"execution_snapshot_id":"`+chain.SnapshotID+`"
	}`))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "evaluation set hash mismatch") {
		t.Fatalf("tampered eval set run status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestE2E_TerminalEvaluationPolicyTamperingIsRejected(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	chain := newGovernanceChainThroughEvidence(t, app, token)

	err := app.db.Table("rgx_evaluation_run").Where("id = ?", chain.RunID).
		Update("evaluation_policy_hash", "tampered").Error
	if err == nil {
		t.Fatal("expected terminal evaluation run policy tampering to fail")
	}
	governanceRequest(t, app, token, "gate after blocked policy tamper", http.MethodPost,
		"/api/v1/release-gates", governanceGateInput(chain, "development", "PASS"), http.StatusOK)
}

func TestE2E_GateBindingAndDuplicateDecisionVersionErrors(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	chain := newGovernanceChainThroughEvidence(t, app, token)

	resp, raw := app.doAuth(t, http.MethodPost, "/api/v1/release-gates", token, []byte(`{
		"release_candidate_id":"`+chain.CandidateID+`","candidate_version":2,
		"evidence_bundle_id":"`+chain.EvidenceID+`","environment":"development",
		"sub_gate_states":{"security":"PASS","policy":"PASS","risk":"PASS","permission":"PASS","configuration":"PASS","approval":"PASS"},
		"environment_policy_version":"v1","environment_policy_hash":"env-hash"
	}`))
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "gate candidate binding mismatch") {
		t.Fatalf("wrong gate version status = %d, body = %s", resp.StatusCode, raw)
	}

	gate := governanceRequest(t, app, token, "valid gate", http.MethodPost,
		"/api/v1/release-gates", governanceGateInput(chain, "development", "PASS"), http.StatusOK)
	var original model.ReleaseGateDecision
	if err := app.db.Where("id = ?", governanceString(t, gate, "id")).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	var tenantID string
	if err := app.db.Table("rgx_release_gate_decision").Select("tenant_id").
		Where("id = ?", governanceString(t, gate, "id")).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}
	if tenantID == "" {
		t.Fatal("gate tenant is empty")
	}
	duplicate := model.ReleaseGateDecision{
		ID: id.New(), TenantID: tenantID, ReleaseCandidateID: chain.CandidateID,
		CandidateVersion: chain.CandidateVersion, DecisionVersion: original.DecisionVersion,
		EvidenceBundleID: chain.EvidenceID, Environment: "development",
		EnvironmentPolicyVersion: "v1", EnvironmentPolicyHash: "env-hash",
		SubGateStates: "{}", Decision: model.GateDecisionPass, Actor: "admin",
	}
	if err := app.db.Create(&duplicate).Error; err == nil {
		t.Fatal("expected duplicate gate decision version to fail")
	}
}

func TestE2E_TerminalCaseResultMutationIsRejected(t *testing.T) {
	app := newTestApp(t)
	defer app.close()
	token := app.login(t)
	chain := newGovernanceChainThroughRun(t, app, token, true, false)

	err := app.db.Table("rgx_evaluation_case_result").Where("run_id = ?", chain.RunID).
		Update("actual_answer", "tampered").Error
	if err == nil {
		t.Fatal("expected terminal case result update to fail")
	}
	err = app.db.Exec("DELETE FROM rgx_evaluation_case_result WHERE run_id = ?", chain.RunID).Error
	if err == nil {
		t.Fatal("expected terminal case result delete to fail")
	}
	resp, raw := app.doAuth(t, http.MethodPost, "/api/v1/evaluation-runs/"+chain.RunID+"/case-results", token, []byte(`{
		"run_id":"`+chain.RunID+`","case_id":"case-2","case_version_id":"missing",
		"case_version_hash":"hash","actual_answer":"late","references":[],"metrics":{},"pass":true
	}`))
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(raw), "evaluation case results are immutable") {
		t.Fatalf("late case result status = %d, body = %s", resp.StatusCode, raw)
	}
}
