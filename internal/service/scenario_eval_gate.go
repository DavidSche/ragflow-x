package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioExportSignature is the integrity envelope attached to exported packs
// (doc/276 §8.7). It proves that a document left *this* installation unmodified;
// it does not grant trust — `source` and the review flow still decide that.
type ScenarioExportSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId,omitempty"`
	Hash      string `json:"hash"`
	Value     string `json:"value"`
}

const scenarioPackSignatureAlgorithm = "hmac-sha256"

// SetScenarioPackSigningKey configures the export signing key. An empty secret
// disables signing, which is the default: exports then carry no signature and
// every import stays `imported` + draft.
func (s *Service) SetScenarioPackSigningKey(keyID, secret string) {
	s.scenarioPackMu.Lock()
	s.scenarioPackKeyID = strings.TrimSpace(keyID)
	s.scenarioPackSecret = secret
	s.scenarioPackMu.Unlock()
}

// signScenarioPackDocument returns the signature for an export document, or nil
// when signing is not configured.
func (s *Service) signScenarioPackDocument(document []byte) *ScenarioExportSignature {
	s.scenarioPackMu.RLock()
	keyID, secret := s.scenarioPackKeyID, s.scenarioPackSecret
	s.scenarioPackMu.RUnlock()
	if secret == "" {
		return nil
	}
	digest := sha256.Sum256(document)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(document)
	return &ScenarioExportSignature{
		Algorithm: scenarioPackSignatureAlgorithm,
		KeyID:     keyID,
		Hash:      hex.EncodeToString(digest[:]),
		Value:     hex.EncodeToString(mac.Sum(nil)),
	}
}

// VerifyScenarioImportSignature checks an attached signature, if any. It returns
// (present, valid). A present-but-invalid signature is a hard failure: the
// document was modified after export, so it must not be treated as a reviewed
// pack from another installation.
func (s *Service) VerifyScenarioImportSignature(document []byte, signature *ScenarioExportSignature) (bool, error) {
	if signature == nil || signature.Value == "" {
		return false, nil
	}
	s.scenarioPackMu.RLock()
	secret := s.scenarioPackSecret
	s.scenarioPackMu.RUnlock()
	if secret == "" {
		// We cannot verify, so we do not claim it is verified. Import still
		// proceeds as an untrusted draft; the audit trail records "unverifiable".
		return false, nil
	}
	if signature.Algorithm != scenarioPackSignatureAlgorithm {
		return false, httperr.BadRequest(40116, "unsupported scenario pack signature algorithm: "+signature.Algorithm)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(document)
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(strings.ToLower(signature.Value))) {
		return false, httperr.BadRequest(40116, "scenario pack signature does not match the document")
	}
	return true, nil
}

// enforceScenarioEvalGate implements the P2 quality gate (doc/276 §9):
// `eval.requireEvalPass` means a pack may not be published until an evaluation
// run for that exact template version has passed. It reads the most recent run
// for the template's candidate identity; "no run" is a failure, not a pass —
// silence must never be read as approval.
func (s *Service) enforceScenarioEvalGate(ctx context.Context, tenantID, templateID, payloadJSON string) error {
	payload, err := parseTemplatePayload(payloadJSON)
	if err != nil {
		return err
	}
	if payload.Pack == nil || !payload.Pack.Eval.RequireEvalPass {
		return nil
	}
	version, err := s.Store.GetScenarioTemplateVersion(ctx, tenantID, templateID, 0)
	if err != nil {
		return err
	}
	runs, _, err := s.Store.ListEvaluationRuns(ctx, tenantID, repository.EvaluationScopeFilter{
		CandidateID: templateID, CandidateVer: version.Version,
	}, 1, 1)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return httperr.New(409, 40918, "scenario pack requires a passing evaluation run before publish")
	}
	run := runs[0]
	if run.Pass == nil || !*run.Pass {
		reason := run.GateReason
		if reason == "" {
			reason = "evaluation run has not passed"
		}
		return httperr.New(409, 40918, "scenario pack evaluation gate not satisfied: "+reason)
	}
	return nil
}

// scenarioEvalGateStatus is the read-side view used by the wizard to explain why
// publish is disabled, so the operator does not have to trigger a 40918 to learn
// the requirement (doc/276 §12).
type scenarioEvalGateStatus struct {
	Required bool   `json:"required"`
	Passed   bool   `json:"passed"`
	RunID    string `json:"runId,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func (s *Service) describeScenarioEvalGate(ctx context.Context, tenantID, templateID, payloadJSON string) scenarioEvalGateStatus {
	payload, err := parseTemplatePayload(payloadJSON)
	if err != nil || payload.Pack == nil || !payload.Pack.Eval.RequireEvalPass {
		return scenarioEvalGateStatus{}
	}
	status := scenarioEvalGateStatus{Required: true}
	version, err := s.Store.GetScenarioTemplateVersion(ctx, tenantID, templateID, 0)
	if err != nil {
		status.Reason = "template version is unavailable"
		return status
	}
	runs, _, err := s.Store.ListEvaluationRuns(ctx, tenantID, repository.EvaluationScopeFilter{
		CandidateID: templateID, CandidateVer: version.Version,
	}, 1, 1)
	if err != nil {
		status.Reason = "evaluation runs are unavailable"
		return status
	}
	if len(runs) == 0 {
		status.Reason = "no evaluation run for this version"
		return status
	}
	status.RunID = runs[0].ID
	status.Passed = runs[0].Pass != nil && *runs[0].Pass
	if !status.Passed {
		status.Reason = runs[0].GateReason
		if status.Reason == "" {
			status.Reason = "evaluation run has not passed"
		}
	}
	return status
}

// ensurePublishablePayload enforces the eval gate for a status-only publish: if
// the payload equals the latest stored version, evidence for that version is
// required; if the payload is new content, the caller is on the
// release-candidate path and this inline flip must not bypass review by
// pretending the new content was evaluated.
func (s *Service) ensurePublishablePayload(ctx context.Context, tenantID string, asset *model.ScenarioTemplateAsset, payloadJSON string) error {
	current, err := s.Store.GetScenarioTemplateVersion(ctx, tenantID, asset.ID, asset.LatestVersion)
	if err != nil || current == nil {
		return err
	}
	payload, err := parseTemplatePayload(payloadJSON)
	if err != nil {
		return err
	}
	if payload.Pack == nil || !payload.Pack.Eval.RequireEvalPass {
		return nil
	}
	if canonicalOf(payloadJSON) != canonicalOf(current.PayloadJSON) {
		return httperr.New(409, 40918, "new scenario pack content must be published through an evaluated release candidate")
	}
	return s.enforceScenarioEvalGate(ctx, tenantID, asset.ID, payloadJSON)
}

// canonicalOf is a best-effort string comparison key: JSON whitespace and key
// order differences must not turn "same content" into "new content".
func canonicalOf(raw string) string {
	hash, err := canonicalHash(json.RawMessage(raw))
	if err != nil {
		return raw
	}
	return hash
}

// assetPublishBlocked is the shared "may this template go live" predicate. It
// keeps the eval gate in one place for both the workspace publish path and the
// platform baseline publish path.
func (s *Service) assetPublishBlocked(ctx context.Context, tenantID string, asset *model.ScenarioTemplateAsset) error {
	if asset == nil {
		return httperr.BadRequest(40001, "scenario template is required")
	}
	current, err := s.Store.GetScenarioTemplateVersion(ctx, tenantID, asset.ID, 0)
	if err != nil || current == nil {
		return nil
	}
	return s.enforceScenarioEvalGate(ctx, tenantID, asset.ID, current.PayloadJSON)
}
