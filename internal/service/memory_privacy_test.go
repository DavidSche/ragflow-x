package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestMemoryPrivacyGateDeterministicLabels verifies the deterministic PII rule
// set: label+count output, BLOCK for high-risk labels, REDACT placeholders for
// ordinary labels, ALLOW when nothing matches.
func TestMemoryPrivacyGateDeterministicLabels(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "PII Gate")
	if err != nil {
		t.Fatal(err)
	}

	evaluate := func(input string) *MemoryPrivacyEvaluation {
		t.Helper()
		evaluation, err := svc.EvaluateMemoryPrivacy(ctx, tenant.ID, nil, input, "")
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		return evaluation
	}

	// Clean text is ALLOW with no labels.
	clean := evaluate("please remember that I prefer concise summaries about golang")
	if clean.Action != MemoryPrivacyActionAllow || len(clean.Labels) != 0 {
		t.Fatalf("clean text must be allow with no labels: %+v", clean)
	}
	if clean.RuleVersion != MemoryPrivacyRuleVersion {
		t.Fatalf("rule version must be recorded: %+v", clean)
	}

	// Phone numbers: label+count with redaction placeholder, boundaries kept.
	// nil policy means the fail-safe default action (REDACT).
	phone := evaluate("联系我 13812345678 或者 15987654321")
	if phone.Action != MemoryPrivacyActionRedact || len(phone.Labels) != 1 {
		t.Fatalf("phone under default redact action must not block: %+v", phone)
	}
	if phone.Labels[0].Label != "phone_cn" || phone.Labels[0].Count != 2 {
		t.Fatalf("phone label/count mismatch: %+v", phone.Labels)
	}
	if strings.Contains(phone.RedactedInput, "13812345678") || !strings.Contains(phone.RedactedInput, "[REDACTED:phone_cn#1]") {
		t.Fatalf("phone spans must be redacted with boundaries kept: %s", phone.RedactedInput)
	}

	// ALLOW posture (monitor mode): hits are counted but text is untouched.
	allowed, err := svc.EvaluateMemoryPrivacy(
		ctx, tenant.ID, &model.MemoryPolicy{Config: `{"pii":{"action":"ALLOW"}}`},
		"联系我 13812345678", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Action != MemoryPrivacyActionAllow || allowed.Labels[0].Count != 1 {
		t.Fatalf("allow posture must count without rewriting: %+v", allowed)
	}
	if !strings.Contains(allowed.RedactedInput, "13812345678") {
		t.Fatalf("allow posture must not rewrite text: %s", allowed.RedactedInput)
	}

	// High-risk labels always block, independent of policy action.
	highRiskCases := map[string]string{
		"credential":           "my password: super-secret-99",
		"api_key":              "use sk-abcdef1234567890abcdef for billing",
		"jwt":                  "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3OH0.kKxcVFaYIOvWRLtYqYKVoRB4C1XKP6cbkdcXZLMcaOY",
		"private_key_pem":      "-----BEGIN RSA PRIVATE KEY-----",
		"db_connection_string": "postgres://admin:hunter2@db.internal:5432/prod",
	}
	for label, input := range highRiskCases {
		evaluation := evaluate(input)
		if evaluation.Action != MemoryPrivacyActionBlock {
			t.Fatalf("high-risk label %s must block, got %s (%+v)", label, evaluation.Action, evaluation.Labels)
		}
		found := false
		for _, hit := range evaluation.Labels {
			if hit.Label == label {
				found = true
			}
		}
		if !found {
			t.Fatalf("high-risk label %s missing from %v", label, evaluation.Labels)
		}
		if strings.Contains(evaluation.RedactedInput, "super-secret-99") ||
			strings.Contains(evaluation.RedactedInput, "hunter2") {
			t.Fatalf("blocked evaluation must not carry raw secrets: %+v", evaluation)
		}
	}

	// REDACT action rewrites ordinary matches into placeholders.
	policy := &model.MemoryPolicy{Config: `{"pii":{"action":"REDACT"}}`}
	redacted, err := svc.EvaluateMemoryPrivacy(ctx, tenant.ID, policy, "email me at alice@example.com please", "")
	if err != nil {
		t.Fatal(err)
	}
	if redacted.Action != MemoryPrivacyActionRedact {
		t.Fatalf("redact action mismatch: %s", redacted.Action)
	}
	if strings.Contains(redacted.RedactedInput, "alice@example.com") {
		t.Fatalf("raw email must not survive redaction: %s", redacted.RedactedInput)
	}
	if !strings.Contains(redacted.RedactedInput, "[REDACTED:email#1]") {
		t.Fatalf("placeholder format mismatch: %s", redacted.RedactedInput)
	}
	if redacted.Labels[0].Label != "email" || redacted.Labels[0].Count != 1 {
		t.Fatalf("email label/count mismatch: %+v", redacted.Labels)
	}

	// BLOCK action reports without rewriting.
	blocked, err := svc.EvaluateMemoryPrivacy(ctx, tenant.ID, &model.MemoryPolicy{Config: `{"pii":{"action":"BLOCK"}}`}, "email me at alice@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Action != MemoryPrivacyActionBlock {
		t.Fatalf("block action mismatch: %s", blocked.Action)
	}
	if blocked.RedactedInput != "" {
		t.Fatalf("blocked evaluation must not carry raw text: %s", blocked.RedactedInput)
	}
}

// TestMemoryPrivacyGateExemptions verifies that tenant exemptions suppress
// ordinary label hits with audit metadata, never high-risk categories.
func TestMemoryPrivacyGateExemptions(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "PII Exemptions")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v", err)
	}

	// Governance boundary: business users cannot configure exemptions.
	business, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "exempt-bu-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMemoryPrivacyExemption(ctx, tenant.ID, business.ID, MemoryPrivacyExemptionInput{
		Label: "phone_cn", Pattern: "13812345678",
	}); err == nil {
		t.Fatal("business user must not configure PII exemptions")
	}

	exemption, err := svc.CreateMemoryPrivacyExemption(ctx, tenant.ID, admin.ID, MemoryPrivacyExemptionInput{
		Label: "phone_cn", Pattern: "13812345678",
	})
	if err != nil {
		t.Fatalf("create exemption: %v", err)
	}
	if exemption.RuleVersion != MemoryPrivacyRuleVersion || exemption.Version != 1 {
		t.Fatalf("exemption must pin rule version: %+v", exemption)
	}

	// High-risk labels are never exemptable.
	if _, err := svc.CreateMemoryPrivacyExemption(ctx, tenant.ID, admin.ID, MemoryPrivacyExemptionInput{
		Label: "api_key", Pattern: "sk-.*",
	}); err == nil {
		t.Fatal("high-risk label must not be exemptable")
	}
	// Invalid patterns are rejected.
	if _, err := svc.CreateMemoryPrivacyExemption(ctx, tenant.ID, admin.ID, MemoryPrivacyExemptionInput{
		Label: "phone_cn", Pattern: "([unclosed",
	}); err == nil {
		t.Fatal("invalid regex must be rejected")
	}

	evaluation, err := svc.EvaluateMemoryPrivacy(
		ctx, tenant.ID, &model.MemoryPolicy{Config: `{"pii":{"action":"REDACT"}}`},
		"internal order uses 13812345678, personal phone is 15987654321", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evaluation.RedactedInput, "13812345678") {
		t.Fatalf("exempted span must survive redaction: %s", evaluation.RedactedInput)
	}
	if strings.Contains(evaluation.RedactedInput, "15987654321") {
		t.Fatalf("non-exempted span must be redacted: %s", evaluation.RedactedInput)
	}
	phoneCount := 0
	for _, hit := range evaluation.Labels {
		if hit.Label == "phone_cn" {
			phoneCount = hit.Count
		}
	}
	if phoneCount != 1 {
		t.Fatalf("exempted match must not be counted: %+v", evaluation.Labels)
	}
	if len(evaluation.ExemptionsHit) != 1 || evaluation.ExemptionsHit[0] != "phone_cn" {
		t.Fatalf("exemption hit must be recorded as label metadata: %+v", evaluation.ExemptionsHit)
	}

	// High-risk detection still blocks even with an unrelated exemption live.
	secret, err := svc.EvaluateMemoryPrivacy(ctx, tenant.ID, nil, "password: super-secret-99", "")
	if err != nil {
		t.Fatal(err)
	}
	if secret.Action != MemoryPrivacyActionBlock {
		t.Fatalf("exemptions must not weaken high-risk blocking: %+v", secret)
	}

	// Delete restores strict behavior and audits.
	if err := svc.DeleteMemoryPrivacyExemption(ctx, tenant.ID, admin.ID, exemption.ID); err != nil {
		t.Fatalf("delete exemption: %v", err)
	}
	if err := svc.DeleteMemoryPrivacyExemption(ctx, tenant.ID, admin.ID, exemption.ID); err == nil {
		t.Fatal("deleting a missing exemption must 404")
	}
	rows, err := svc.ListMemoryPrivacyExemptions(ctx, tenant.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("exemptions must be empty after delete: rows=%d err=%v", len(rows), err)
	}
}

// TestMemoryPrivacyGateSealsOperationMetadata verifies that the write chain
// stamps pii_action/pii_labels/pii_rule_version onto the operation and that
// BLOCKed payloads never create an operation.
func TestMemoryPrivacyGateSealsOperationMetadata(t *testing.T) {
	svc, _, target, policy, memoryID := newMemoryDispatchAlertFixture(t, "pii-seal")
	ctx := context.Background()

	// REDACT policy: payload redacted before sealing; metadata stamped.
	redactPolicy := *policy
	redactPolicy.Config = `{"pii":{"action":"REDACT"}}`
	if err := svc.Store.UpdateMemoryPolicy(ctx, &redactPolicy); err != nil {
		t.Fatal(err)
	}
	operation, err := svc.CreateMemoryDispatchOperation(ctx, redactPolicy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-pii-redact", IdempotencyKey: "idempotency-pii-redact",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "call me at 13812345678", AgentResponse: "done",
	})
	if err != nil {
		t.Fatalf("redacted write must be accepted: %v", err)
	}
	if operation.PIIAction != MemoryPrivacyActionRedact || operation.PIIRuleVersion != MemoryPrivacyRuleVersion {
		t.Fatalf("operation must stamp privacy metadata: %+v", operation)
	}
	if !strings.Contains(operation.PIILabels, "phone_cn") {
		t.Fatalf("operation must record label summary: %s", operation.PIILabels)
	}

	// BLOCK policy: secret payload is rejected, no operation is created.
	blockPolicy := *policy
	blockPolicy.Config = `{"pii":{"action":"BLOCK"}}`
	if err := svc.Store.UpdateMemoryPolicy(ctx, &blockPolicy); err != nil {
		t.Fatal(err)
	}
	before, _, err := svc.Store.ListMemoryOperations(ctx, blockPolicy.TenantID, repository.MemoryOperationFilter{}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMemoryDispatchOperation(ctx, blockPolicy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-pii-block", IdempotencyKey: "idempotency-pii-block",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "my password: super-secret-99", AgentResponse: "",
	}); err == nil {
		t.Fatal("blocked payload must be rejected")
	}
	after, _, err := svc.Store.ListMemoryOperations(ctx, blockPolicy.TenantID, repository.MemoryOperationFilter{}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("blocked payload must not create an operation: before=%d after=%d", len(before), len(after))
	}

	// Invalid pii.action is rejected instead of silently downgraded.
	invalidPolicy := *policy
	invalidPolicy.Config = `{"pii":{"action":"OBFUSCATE"}}`
	if err := svc.Store.UpdateMemoryPolicy(ctx, &invalidPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMemoryDispatchOperation(ctx, invalidPolicy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-pii-invalid", IdempotencyKey: "idempotency-pii-invalid",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "hello", AgentResponse: "acknowledged",
	}); err == nil {
		t.Fatal("invalid pii.action must be rejected")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40095 {
		t.Fatalf("invalid pii.action must map to 40095: %v", err)
	}
}
