package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRepairAuditChainReanchorsTenant(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	for _, action := range []string{"first", "second"} {
		if err := svc.RecordAudit(ctx, &model.AuditLog{TenantID: "audit-tenant", Action: action, Resource: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	valid, err := svc.RepairAuditChain(ctx, "audit-tenant")
	if err != nil || !valid {
		t.Fatalf("repair chain: valid=%t err=%v", valid, err)
	}
	if _, err := svc.RepairAuditChain(ctx, ""); err == nil {
		t.Fatal("expected empty tenant to be rejected")
	}
}
