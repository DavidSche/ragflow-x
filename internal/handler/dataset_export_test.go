package handler

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func containsExportEvidence(detail string, rows int64) bool {
	return strings.Contains(detail, `"rows":`+strconv.FormatInt(rows, 10)) &&
		strings.Contains(detail, `"limit":10000`)
}

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_DatasetExportAuditEvidenceAndCSVSafety(t *testing.T) {
	env := setupTestEnv(t)
	if err := env.svc.Store.UpsertDatasetLink(t.Context(), &model.DatasetLink{
		ID: "=dataset-id", TenantID: env.tenantID, RAGFlowDatasetID: "@ragflow-id",
		Name: "+dangerous-name", DocumentCount: 3,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	token := env.tokenFor(t, env.adminID, env.tenantID, "tenant_admin")
	w := env.doRequest(t, http.MethodGet, "/api/v1/datasets/export", nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("csv records = %d, want header plus one dataset", len(records))
	}
	for index, value := range records[1] {
		if index == 4 || value == "" {
			continue
		}
		switch value[0] {
		case '=', '+', '-', '@', '\t', '\r':
			t.Fatalf("csv column %d has an unneutralized formula prefix: %q", index, value)
		}
	}

	audits, _, err := env.svc.Store.ListAudits(t.Context(), env.tenantID, 1, 100, repository.AuditFilter{Action: "dataset.exported"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("dataset.exported records = %d, want 1", len(audits))
	}
	audit := audits[0]
	if audit.Scope != string(service.TenantScopeCurrent) ||
		audit.ActorTenantID != env.tenantID || audit.TargetTenantID != env.tenantID {
		t.Fatalf("export scope evidence mismatch: %+v", audit)
	}
	if audit.Result != "SUCCESS" || audit.AuthorizationDecision != "ALLOW" ||
		audit.AuthorizationPermission != "read:dataset-export" ||
		audit.AuthorizationPolicyVersion != "explicit-rbac-v1" {
		t.Fatalf("export authorization evidence mismatch: %+v", audit)
	}
	if !containsExportEvidence(audit.DetailJSON, 1) {
		t.Fatalf("export result evidence mismatch: %s", audit.DetailJSON)
	}
}

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_PlatformSpecificDatasetExportCarriesTargetTenant(t *testing.T) {
	env := setupTestEnv(t)
	if err := env.svc.Store.UpsertDatasetLink(t.Context(), &model.DatasetLink{
		ID: "dataset-id", TenantID: env.tenantID, RAGFlowDatasetID: "ragflow-id",
		Name: "target dataset", DocumentCount: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	token := env.tokenFor(t, env.platformID, model.PlatformTenantID, "platform_admin")
	path := "/api/v1/datasets/export?scope=specific&tenant_id=" + env.tenantID
	w := env.doRequest(t, http.MethodGet, path, nil, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	records, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("csv records = %d, want header plus one dataset", len(records))
	}

	audits, _, err := env.svc.Store.ListAudits(t.Context(), model.PlatformTenantID, 1, 100, repository.AuditFilter{Action: "dataset.exported"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("dataset.exported records = %d, want 1", len(audits))
	}
	audit := audits[0]
	if audit.Scope != string(service.TenantScopeSpecific) ||
		audit.ActorTenantID != model.PlatformTenantID || audit.TargetTenantID != env.tenantID {
		t.Fatalf("specific export scope evidence mismatch: %+v", audit)
	}
	if !containsExportEvidence(audit.DetailJSON, 1) {
		t.Fatalf("specific export rows evidence mismatch: %s", audit.DetailJSON)
	}
}
