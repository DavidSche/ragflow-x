package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestAnswerSnapshotCanonicalHashIsContentFingerprint(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Canonical Hash Tenant")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "hash-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	buildInput := func(requestID string) AnswerDeliveryInput {
		return AnswerDeliveryInput{
			TenantID: tenant.ID, SessionID: "hash-session", AssistantID: "assistant-1",
			PrincipalID: admin.ID, Question: "same question", RequestID: requestID,
			AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
			ReasonCode: "TEST_ANSWERED", Content: "same content",
		}
	}
	first, err := svc.FinalizeAnswerDelivery(ctx, buildInput("hash-request-1"))
	if err != nil {
		t.Fatalf("finalize first answer: %v", err)
	}
	second, err := svc.FinalizeAnswerDelivery(ctx, buildInput("hash-request-2"))
	if err != nil {
		t.Fatalf("finalize repeated answer: %v", err)
	}
	if first.Snapshot.ID == second.Snapshot.ID {
		t.Fatal("repeated answer must create a separate snapshot")
	}
	if first.Snapshot.CanonicalHash != second.Snapshot.CanonicalHash {
		t.Fatalf("identical canonical payload hashes differ: %s != %s", first.Snapshot.CanonicalHash, second.Snapshot.CanonicalHash)
	}
}
