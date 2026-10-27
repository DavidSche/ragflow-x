package handler

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_ExportAuditEvidenceAndCSVSafety(t *testing.T) {
	env := setupTestEnv(t)
	seed := &model.AuditLog{
		TenantID:   env.tenantID,
		UserID:     "=HYPERLINK(\"https://attacker.example\",\"pwn\")",
		Action:     "@audit.command",
		Resource:   "+dataset",
		ResourceID: "-cmd|' /C calc'!A0",
		DetailJSON: "\t{\"key\":\"value\"}",
		IP:         "\r127.0.0.1",
		TraceID:    "=trace",
	}
	if err := env.svc.RecordAudit(t.Context(), seed); err != nil {
		t.Fatal(err)
	}

	token := env.tokenFor(t, env.adminID, env.tenantID, "tenant_admin")
	w := env.doRequest(t, http.MethodGet, "/api/v1/audit/export", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("csv rows = %d, want header plus one seeded audit", len(rows))
	}
	for index, value := range rows[1] {
		if index == 0 || value == "" {
			continue
		}
		switch value[0] {
		case '=', '+', '-', '@', '\t', '\r':
			t.Fatalf("csv column %d has an unneutralized formula prefix: %q", index, value)
		}
	}
	audits, _, err := env.svc.Store.ListAudits(t.Context(), env.tenantID, 1, 100, repository.AuditFilter{Action: "audit.exported"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit.exported records = %d, want 1", len(audits))
	}
	audit := audits[0]
	if audit.Scope != string(service.TenantScopeCurrent) ||
		audit.ActorTenantID != env.tenantID || audit.TargetTenantID != env.tenantID {
		t.Fatalf("export scope evidence mismatch: %+v", audit)
	}
	if audit.Result != "SUCCESS" || audit.AuthorizationDecision != "ALLOW" ||
		audit.AuthorizationPermission != "read:audit" ||
		audit.AuthorizationPolicyVersion != "explicit-rbac-v1" {
		t.Fatalf("export authorization evidence mismatch: %+v", audit)
	}
	if !strings.Contains(audit.DetailJSON, `"rows":1`) || !strings.Contains(audit.DetailJSON, `"limit":10000`) {
		t.Fatalf("export result evidence mismatch: %s", audit.DetailJSON)
	}
}

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_PlatformSpecificExportCarriesTargetTenant(t *testing.T) {
	env := setupTestEnv(t)
	if err := env.svc.RecordAudit(t.Context(), &model.AuditLog{
		TenantID: env.tenantID, UserID: "actor", Action: "dataset.create",
		Resource: "dataset", ResourceID: "dataset-1",
	}); err != nil {
		t.Fatal(err)
	}

	token := env.tokenFor(t, env.platformID, model.PlatformTenantID, "platform_admin")
	path := "/api/v1/audit/export?scope=specific&tenant_id=" + env.tenantID
	w := env.doRequest(t, http.MethodGet, path, nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	audits, _, err := env.svc.Store.ListAudits(t.Context(), model.PlatformTenantID, 1, 100, repository.AuditFilter{Action: "audit.exported"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit.exported records = %d, want 1", len(audits))
	}
	audit := audits[0]
	if audit.Scope != string(service.TenantScopeSpecific) ||
		audit.ActorTenantID != model.PlatformTenantID || audit.TargetTenantID != env.tenantID {
		t.Fatalf("specific export scope evidence mismatch: %+v", audit)
	}
	if !strings.Contains(audit.DetailJSON, `"rows":1`) {
		t.Fatalf("specific export rows evidence mismatch: %s", audit.DetailJSON)
	}
}
