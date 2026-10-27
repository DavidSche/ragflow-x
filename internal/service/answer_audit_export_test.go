package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestAuditExportRequiresPermission pins doc/100 §7.3 contract 2: the
// audit-grade export channel is gated by its own resource (audit-export:read),
// separate from the business export permission. A tenant_admin may export
// business artifacts, but only platform_admin / tenant_admin carry
// audit-export:read; a business_user must be denied.
func TestAuditExportRequiresPermission(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Audit Export Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "audit-admin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	answer := finalizeTestAnswer(t, svc, ctx, tenant.ID, admin.ID, "request-audit-1")

	// Allowed: tenant_admin holds audit-export:read.
	result, err := svc.CreateAuditExportForAnswer(ctx, admin.ID, tenant.ID, AuditExportInput{
		AnswerSnapshotID: answer.Snapshot.ID, IdempotencyKey: "audit-key-1", TraceID: "trace-audit-1",
	})
	if err != nil {
		t.Fatalf("tenant_admin audit export must succeed: %v", err)
	}
	if result.Job.Status != model.ExportJobSucceeded || result.Artifact.SHA256 == "" || result.Artifact.FileRef == "" {
		t.Fatalf("audit export lifecycle incomplete: %+v %+v", result.Job, result.Artifact)
	}

	// Denied: a business_user has no audit-export grant.
	user, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "audit-user", Password: "secret123", Role: "business_user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAuditExportForAnswer(ctx, user.ID, tenant.ID, AuditExportInput{
		AnswerSnapshotID: answer.Snapshot.ID, IdempotencyKey: "audit-key-2", TraceID: "trace-audit-2",
	}); err == nil {
		t.Fatal("business_user must be denied audit export")
	}

	// Denied: cross-tenant admin cannot read another tenant's snapshot.
	otherTenant, err := svc.CreateTenant(ctx, "Audit Other Tenant")
	if err != nil {
		t.Fatal(err)
	}
	otherAdmin, err := svc.CreateUser(ctx, otherTenant.ID, "", CreateUserRequest{Username: "other-audit-admin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAuditExportForAnswer(ctx, otherAdmin.ID, otherTenant.ID, AuditExportInput{
		AnswerSnapshotID: answer.Snapshot.ID, IdempotencyKey: "audit-key-3", TraceID: "trace-audit-3",
	}); err == nil {
		t.Fatal("cross-tenant audit export must be denied")
	}
}

// TestAuditExportPayloadAndIdempotency covers doc/124 §4.2: the audit channel
// carries the full governance payload (model metadata, authorization
// projection evidence, retrieval pushdown and redaction reasons) under the
// answer-audit-v1 template, its idempotency keys are namespaced with the
// "audit:" prefix, and reuse against a different snapshot conflicts.
func TestAuditExportPayloadAndIdempotency(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Audit Payload Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "payload-admin", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	answer := finalizeTestAnswer(t, svc, ctx, tenant.ID, admin.ID, "request-audit-payload")

	first, err := svc.CreateAuditExportForAnswer(ctx, admin.ID, tenant.ID, AuditExportInput{
		AnswerSnapshotID: answer.Snapshot.ID, IdempotencyKey: "shared-key", TraceID: "trace-payload",
	})
	if err != nil {
		t.Fatalf("create audit export: %v", err)
	}
	if first.Job.IdempotencyKey != auditExportIdempotencyPrefix+"shared-key" {
		t.Fatalf("audit export must namespace its idempotency key: %s", first.Job.IdempotencyKey)
	}
	content, err := os.ReadFile(first.Artifact.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	auditJSON := string(content)
	for _, fragment := range []string{
		`"audit_scope":"full"`,
		`"answer_run"`,
		`"authorization"`,
		`"policy_version"`,
		`"policy_input_hash"`,
		`"decision_hash"`,
		`"retrieval_pushdown"`,
		`"redaction_reasons"`,
		`"canonical_hash"`,
	} {
		if !strings.Contains(auditJSON, fragment) {
			t.Fatalf("audit payload missing %s: %s", fragment, auditJSON)
		}
	}

	// Same key + same snapshot returns the existing job/artifact.
	second, err := svc.CreateAuditExportForAnswer(ctx, admin.ID, tenant.ID, AuditExportInput{
		AnswerSnapshotID: answer.Snapshot.ID, IdempotencyKey: "shared-key", TraceID: "trace-payload",
	})
	if err != nil || second.Job.ID != first.Job.ID || second.Artifact.ID != first.Artifact.ID {
		t.Fatalf("audit export idempotency failed: %v", err)
	}

	// A business export may reuse the same caller key: the audit prefix keeps
	// the two channels from colliding.
	business, err := svc.CreateExportForAnswer(ctx, admin.ID, tenant.ID, ExportRequest{
		AnswerSnapshotID: answer.Snapshot.ID, Format: model.ExportFormatMarkdown,
		IdempotencyKey: "shared-key", TraceID: "trace-payload",
	})
	if err != nil || business.Job.IdempotencyKey != "shared-key" {
		t.Fatalf("business export with the same caller key must not collide: %v %s", err, business.Job.IdempotencyKey)
	}

	// Reusing the audit key for a different snapshot conflicts.
	other, err := svc.FinalizeAnswerDelivery(ctx, AnswerDeliveryInput{
		TenantID: tenant.ID, SessionID: "session-answer", AssistantID: "assistant-1",
		PrincipalID: admin.ID, Question: "different question", RequestID: "request-audit-payload-2",
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		ReasonCode: "TEST_ANSWERED", Content: "A different answer body for the conflict case.",
		Limitations: []string{}, Actions: []map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAuditExportForAnswer(ctx, admin.ID, tenant.ID, AuditExportInput{
		AnswerSnapshotID: other.Snapshot.ID, IdempotencyKey: "shared-key", TraceID: "trace-payload",
	}); err == nil {
		t.Fatal("audit idempotency key reuse for another snapshot must conflict")
	}

	// The audit artifact must be JSON, not the business renderer output.
	if !strings.HasPrefix(auditJSON, "{") {
		t.Fatalf("audit artifact must stay JSON: %s", auditJSON[:20])
	}
}
