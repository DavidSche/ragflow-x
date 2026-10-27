package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// EvaluationProtocolRegistrationInput freezes a scoped paired-A/B evaluation
// protocol before any run that carries it may execute (137 §11.3 / §14
// preregistration governance).
type EvaluationProtocolRegistrationInput struct {
	Capability    string          `json:"capability"`
	Protocol      json.RawMessage `json:"protocol"`
	EvalSetID     string          `json:"eval_set_id"`
	SampleSetHash string          `json:"sample_set_hash"`
	Note          string          `json:"note"`
}

// EvaluationProtocolRegistrationRevokeInput revokes an active registration.
type EvaluationProtocolRegistrationRevokeInput struct {
	Reason string `json:"reason"`
}

// protocolCanonicalJSON renders the parsed protocol canonically. Because the
// protocol is first passed through parseScopedEvaluationProtocol, semantically
// identical protocols (key order differences, explicitly-set defaults) render
// identically while any threshold change produces a different rendering.
func protocolCanonicalJSON(protocol scopedEvaluationProtocol) (string, error) {
	encoded, err := json.Marshal(protocol)
	if err != nil {
		return "", httperr.BadRequest(40072, "scoped evaluation protocol cannot be canonicalized")
	}
	return string(encoded), nil
}

func (s *Service) RegisterEvaluationProtocol(
	ctx context.Context, tenantID, userID string, input EvaluationProtocolRegistrationInput,
) (*model.EvaluationProtocolRegistration, error) {
	capability := strings.ToLower(strings.TrimSpace(input.Capability))
	if capability == "" || len(capability) > 32 {
		return nil, httperr.BadRequest(40072, "evaluation protocol capability is invalid")
	}
	protocolRaw := strings.TrimSpace(string(input.Protocol))
	if protocolRaw == "" {
		protocolRaw = "{}"
	}
	protocol, err := parseScopedEvaluationProtocol(protocolRaw)
	if err != nil {
		return nil, err
	}
	protocolSnapshot, err := protocolCanonicalJSON(protocol)
	if err != nil {
		return nil, err
	}
	evalSetID := strings.TrimSpace(input.EvalSetID)
	if len(evalSetID) > 32 {
		return nil, httperr.BadRequest(40072, "evaluation protocol eval_set_id is invalid")
	}
	sampleSetHash := strings.ToLower(strings.TrimSpace(input.SampleSetHash))
	if sampleSetHash != "" && len(sampleSetHash) != 64 {
		return nil, httperr.BadRequest(40072, "evaluation protocol sample_set_hash must be a sha256 hex digest")
	}
	note := strings.TrimSpace(input.Note)
	if len(note) > 512 {
		return nil, httperr.BadRequest(40072, "evaluation protocol note is too long")
	}
	user, err := s.Store.GetUser(ctx, userID)
	if err != nil || user == nil {
		return nil, httperr.BadRequest(40072, "evaluation protocol registrar is invalid")
	}
	registration := &model.EvaluationProtocolRegistration{
		ID: id.New(), TenantID: tenantID, Capability: capability,
		ContractVersion: protocol.ContractVersion, ProtocolHash: sha256Hex(protocolSnapshot),
		ProtocolSnapshot: protocolSnapshot, EvalSetID: evalSetID,
		SampleSetHash: sampleSetHash, Status: model.EvaluationProtocolRegistrationActive,
		RegisteredBy: userID, Note: note,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Store.CreateEvaluationProtocolRegistration(ctx, registration); err != nil {
		return nil, httperr.New(409, 40990, "evaluation protocol registration already exists")
	}
	return registration, nil
}

func (s *Service) ListEvaluationProtocolRegistrations(
	ctx context.Context, tenantID string, filter repository.EvaluationProtocolRegistrationFilter, page, pageSize int,
) ([]model.EvaluationProtocolRegistration, int64, error) {
	return s.Store.ListEvaluationProtocolRegistrations(ctx, tenantID, filter, page, pageSize)
}

func (s *Service) RevokeEvaluationProtocolRegistration(
	ctx context.Context, tenantID, userID, registrationID string, input EvaluationProtocolRegistrationRevokeInput,
) (*model.EvaluationProtocolRegistration, error) {
	registration, err := s.Store.GetEvaluationProtocolRegistration(ctx, tenantID, registrationID)
	if err != nil {
		return nil, err
	}
	if registration == nil {
		return nil, httperr.NotFound("evaluation protocol registration not found")
	}
	if registration.Status != model.EvaluationProtocolRegistrationActive {
		return nil, httperr.New(409, 40990, "evaluation protocol registration is not active")
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return nil, httperr.BadRequest(40072, "evaluation protocol revoke reason is required")
	}
	if len(reason) > 256 {
		return nil, httperr.BadRequest(40072, "evaluation protocol revoke reason is too long")
	}
	now := time.Now().UTC()
	registration.Status = model.EvaluationProtocolRegistrationRevoked
	registration.RevokedBy = userID
	registration.RevokeReason = reason
	registration.RevokedAt = &now
	registration.UpdatedAt = now
	if err := s.Store.RevokeEvaluationProtocolRegistration(ctx, registration); err != nil {
		return nil, err
	}
	return s.Store.GetEvaluationProtocolRegistration(ctx, tenantID, registrationID)
}

// requireScopedEvaluationPreregistration fails closed unless the run's
// protocol matches the latest preregistration for its (tenant, capability,
// contract version, protocol hash) identity. The binding is exact: when the
// registration pinned an evaluation set identity or sample set hash, the run
// must carry the same values.
func (s *Service) requireScopedEvaluationPreregistration(
	ctx context.Context, run *model.EvaluationRun, protocol scopedEvaluationProtocol,
) error {
	protocolSnapshot, err := protocolCanonicalJSON(protocol)
	if err != nil {
		return err
	}
	registration, err := s.Store.FindEvaluationProtocolRegistration(
		ctx, run.TenantID, run.Capability, protocol.ContractVersion, sha256Hex(protocolSnapshot),
	)
	if err != nil {
		return httperr.New(502, 50282, "scoped evaluation preregistration lookup failed")
	}
	if registration == nil || registration.Status != model.EvaluationProtocolRegistrationActive {
		return httperr.BadRequest(40077, "scoped evaluation protocol is not pre-registered")
	}
	if registration.EvalSetID != "" && registration.EvalSetID != run.EvalSetID {
		return httperr.BadRequest(40077, "scoped evaluation protocol is preregistered for a different evaluation set")
	}
	if registration.SampleSetHash != "" && !strings.EqualFold(registration.SampleSetHash, run.EvalSetHash) {
		return httperr.BadRequest(40077, "scoped evaluation sample set does not match its preregistration")
	}
	return nil
}
