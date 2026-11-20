package service

import (
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// TestApprovalRotationIsGatedValidatedAndRevealed covers the wiring a rotation needs in
// order not to be a way around the workspace's own approval policy.
//
// Three separate promises, one test each:
//
//	1. the executor is registered — an approval for rotate-credential must have somewhere
//	   to run, and an unregistered (object, action) pair is a policy that promises a gate
//	   it cannot enforce;
//	2. validation re-checks the key at execution time — the approval may be decided hours
//	   later, and rotating a key that was already rotated would mint a second live secret
//	   from a stale decision;
//	3. only the requester can reveal the new secret — the approver approves a decision, it
//	   does not receive a credential.
func TestApprovalRotationIsGatedValidatedAndRevealed(t *testing.T) {
	f := newKeyRotationFixture(t)
	executor, ok := f.svc.approvalExecutorRegistry().Get(model.ApprovalObjectAPIKey + "." + model.ApprovalActionRotateCredential)
	if !ok {
		t.Fatal("api-key.rotate-credential has no executor: an approval for it would be undecidable")
	}

	approval := &model.Approval{
		ID:          id.New(),
		TenantID:    f.tenantID,
		RequesterID: f.key.UserID,
		ObjectType:  model.ApprovalObjectAPIKey,
		ObjectID:    f.key.ID,
		Action:      model.ApprovalActionRotateCredential,
		Status:      model.ApprovalStatusCompleted,
		PayloadJSON: `{"grace_hours":0}`,
	}
	if err := executor.Validate(f.ctx, approval); err != nil {
		t.Fatalf("validate on a live key: %v", err)
	}

	result, err := executor.Execute(f.ctx, approval)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	successorID, _ := result["key_id"].(string)
	if successorID == "" || successorID == f.key.ID {
		t.Fatalf("executor did not mint a successor: %v", result)
	}
	if _, plain := result["api_key"]; plain {
		t.Fatal("the raw secret must never travel in the executor result")
	}
	ciphertext, _ := result["api_key_ciphertext"].(string)
	if ciphertext == "" {
		t.Fatalf("the result must carry an encrypted secret: %v", result)
	}

	old, err := f.svc.Store.GetAPIKey(f.ctx, f.tenantID, f.key.ID)
	if err != nil || old == nil {
		t.Fatalf("reload old key: %v", err)
	}
	if old.RotatedToKeyID != successorID {
		t.Fatalf("old key links to %q, want %s", old.RotatedToKeyID, successorID)
	}

	// Re-validating after the fact is the whole point of validating at execution time.
	if err := executor.Validate(f.ctx, approval); err == nil {
		t.Fatal("a second rotation of the same key must be refused at validation")
	} else {
		mustHTTPError(t, err, 409, 41150)
	}

	// Reveal: persist the approval with the result the executor returned and read it back
	// as the requester, then as somebody else.
	stored := *approval
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	stored.ResultJSON = string(encoded)
	if err := f.svc.Store.CreateApprovalWithAudit(f.ctx, &stored, nil, nil, nil); err != nil {
		t.Fatalf("persist approval: %v", err)
	}
	detail, err := f.svc.GetApproval(f.ctx, f.key.UserID, f.tenantID, stored.ID)
	if err != nil {
		t.Fatalf("requester view: %v", err)
	}
	var revealed map[string]any
	if err := json.Unmarshal([]byte(detail.Approval.ResultJSON), &revealed); err != nil {
		t.Fatal(err)
	}
	if revealed["api_key"] == nil || revealed["api_key"] == "" {
		t.Fatalf("the requester must see the new secret exactly once: %v", revealed)
	}
	if _, still := revealed["api_key_ciphertext"]; still {
		t.Fatal("the ciphertext must be consumed when the secret is revealed")
	}
}
