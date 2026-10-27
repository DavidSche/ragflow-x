package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// newGovernanceService bootstraps a service with a platform admin, a tenant,
// a tenant_admin governor and an ordinary member, plus one dataset link.
// Returns svc, tenantID, governorID, memberID, datasetLinkID.
func newGovernanceService(t *testing.T) (*Service, string, string, string, string) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "gov-svc.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	mock := ragflow.NewMock()
	svc := New(repository.NewStore(gdb), mock, nil, "test-encryption-key")
	t.Cleanup(func() { _ = svc.Store.Close() })

	ctx := context.Background()
	admin, err := svc.CreateUser(ctx, model.PlatformTenantID, string(model.RolePlatformAdmin), CreateUserRequest{Username: "gov-admin", Password: "password123", Role: model.RolePlatformAdmin})
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(ctx, "gov-tenant")
	if err != nil {
		t.Fatal(err)
	}
	governor, err := svc.CreateUser(ctx, tenant.ID, admin.ID, CreateUserRequest{Username: "gov-governor", Password: "password123", Role: model.RoleTenantAdmin})
	if err != nil {
		t.Fatal(err)
	}
	member, err := svc.CreateUser(ctx, tenant.ID, admin.ID, CreateUserRequest{Username: "gov-member", Password: "password123", Role: model.RoleTeamAdmin})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := svc.CreateDataset(ctx, tenant.ID, "governance-test-dataset")
	if err != nil {
		t.Fatal(err)
	}
	return svc, tenant.ID, governor.ID, member.ID, ds.ID
}

func governanceInput(level, status string) DocumentGovernanceInput {
	return DocumentGovernanceInput{Level: level, Status: status}
}

func TestSetDocumentGovernanceValidationErrors(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, _, memberID, dsID := newGovernanceService(t)

	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "", governanceInput(model.GovernanceLevelInternal, "")); err == nil || errCode(err) != 40121 {
		t.Fatalf("missing document_id expected 40121, got %v", err)
	}
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "doc1", governanceInput("top", "")); err == nil || errCode(err) != 40122 {
		t.Fatalf("bad level expected 40122, got %v", err)
	}
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "doc1", governanceInput(model.GovernanceLevelInternal, "archived")); err == nil || errCode(err) != 40123 {
		t.Fatalf("bad status expected 40123, got %v", err)
	}
	bad := 101
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "doc1", DocumentGovernanceInput{Level: model.GovernanceLevelInternal, Authority: &bad}); err == nil || errCode(err) != 40124 {
		t.Fatalf("bad authority expected 40124, got %v", err)
	}
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "doc1", DocumentGovernanceInput{Level: model.GovernanceLevelInternal, Source: "robot"}); err == nil || errCode(err) != 40125 {
		t.Fatalf("bad source expected 40125, got %v", err)
	}
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, "missing-dataset", "doc1", governanceInput(model.GovernanceLevelInternal, "")); err == nil || errHTTPStatus(err) != 404 {
		t.Fatalf("missing dataset expected 404, got %v", err)
	}
}

func errCode(err error) int {
	if he, ok := err.(*httperr.Error); ok {
		return he.Code
	}
	return -1
}

func errHTTPStatus(err error) int {
	if he, ok := err.(*httperr.Error); ok {
		return he.Status
	}
	return -1
}

func TestSetDocumentGovernanceNonGovernorTopSecretGoesToApproval(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, _, memberID, dsID := newGovernanceService(t)

	rec, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "doc-secret", governanceInput(model.GovernanceLevelTopSecret, ""))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ApprovalState != model.GovernanceApprovalPending {
		t.Fatalf("non-governor 绝密 must land pending, got %s", rec.ApprovalState)
	}
	if rec.Source != model.GovernanceSourceApproval {
		t.Fatalf("non-governor 绝密 source must be approval, got %s", rec.Source)
	}
	// The document must NOT be active for retrieval until approved.
	if rec.Active(time.Now().UTC()) {
		t.Fatal("pending annotation must be inactive")
	}
}

func TestSetDocumentGovernanceGovernorBypassesApproval(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, _, dsID := newGovernanceService(t)

	rec, err := svc.SetDocumentGovernance(ctx, tenantID, governorID, dsID, "doc-secret", governanceInput(model.GovernanceLevelTopSecret, ""))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ApprovalState != model.GovernanceApprovalApproved {
		t.Fatalf("governor 绝密 must land approved, got %s", rec.ApprovalState)
	}
	if rec.ApprovedBy == nil || *rec.ApprovedBy != governorID {
		t.Fatalf("governor must be recorded as approver, got %v", rec.ApprovedBy)
	}
	if !rec.Active(time.Now().UTC()) {
		t.Fatal("governor-approved annotation must be active")
	}
}

func TestApproveDocumentGovernanceFlowAndConflict(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, memberID, dsID := newGovernanceService(t)

	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "doc-secret", governanceInput(model.GovernanceLevelTopSecret, "")); err != nil {
		t.Fatal(err)
	}
	// Non-governor cannot decide.
	if _, err := svc.ApproveDocumentGovernance(ctx, tenantID, memberID, dsID, "doc-secret", true); errCode(err) != 40321 {
		t.Fatalf("member approve expected 40321, got %v", err)
	}
	approved, err := svc.ApproveDocumentGovernance(ctx, tenantID, governorID, dsID, "doc-secret", true)
	if err != nil {
		t.Fatal(err)
	}
	if approved.ApprovalState != model.GovernanceApprovalApproved || approved.ApprovedBy == nil || *approved.ApprovedBy != governorID {
		t.Fatalf("approve failed to finalize, got %+v", approved)
	}
	// Approving an already-decided annotation is a 40921 conflict.
	if _, err := svc.ApproveDocumentGovernance(ctx, tenantID, governorID, dsID, "doc-secret", true); errCode(err) != 40921 {
		t.Fatalf("double-approve expected 40921, got %v", err)
	}
	// Reject path.
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, "doc-reject", governanceInput(model.GovernanceLevelTopSecret, "")); err != nil {
		t.Fatal(err)
	}
	rejected, err := svc.ApproveDocumentGovernance(ctx, tenantID, governorID, dsID, "doc-reject", false)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.ApprovalState != model.GovernanceApprovalRejected {
		t.Fatalf("reject must set rejected, got %s", rejected.ApprovalState)
	}
	if rejected.Active(time.Now().UTC()) {
		t.Fatal("rejected annotation must be inactive")
	}
	// Approving a missing record is 404.
	if _, err := svc.ApproveDocumentGovernance(ctx, tenantID, governorID, dsID, "nope", true); err == nil || errHTTPStatus(err) != 404 {
		t.Fatalf("approve missing expected 404, got %v", err)
	}
}

func TestSetUserClearanceGovernorOnlyAndDefaultResolution(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, memberID, _ := newGovernanceService(t)

	// 40321 越权：member 不能提升别人的 clearance。
	if _, err := svc.SetUserClearance(ctx, tenantID, "target-user", memberID, model.GovernanceLevelConfidential); errCode(err) != 40321 {
		t.Fatalf("member clearance assignment expected 40321, got %v", err)
	}
	if _, err := svc.SetUserClearance(ctx, tenantID, "target-user", governorID, "weird"); err == nil || errCode(err) != 40122 {
		t.Fatalf("bad clearance level expected 40122, got %v", err)
	}
	rec, err := svc.SetUserClearance(ctx, tenantID, "target-user", governorID, model.GovernanceLevelTopSecret)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Level != model.GovernanceLevelTopSecret || rec.UserID != "target-user" || rec.UpdatedBy != governorID {
		t.Fatalf("unexpected clearance record %+v", rec)
	}
	// 缺省 → internal 最小权限。
	level, err := svc.ResolveClearanceLevel(ctx, tenantID, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if level != model.GovernanceLevelInternal {
		t.Fatalf("absent clearance must resolve to internal, got %s", level)
	}
	level, err = svc.ResolveClearanceLevel(ctx, tenantID, "target-user")
	if err != nil {
		t.Fatal(err)
	}
	if level != model.GovernanceLevelTopSecret {
		t.Fatalf("elevated clearance must resolve, got %s", level)
	}
}

func TestDeleteDocumentGovernanceGovernorOnly(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, memberID, dsID := newGovernanceService(t)

	if _, err := svc.SetDocumentGovernance(ctx, tenantID, governorID, dsID, "doc-a", governanceInput(model.GovernanceLevelInternal, "")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDocumentGovernance(ctx, tenantID, memberID, dsID, "doc-a"); errCode(err) != 40321 {
		t.Fatalf("member delete expected 40321, got %v", err)
	}
	if err := svc.DeleteDocumentGovernance(ctx, tenantID, governorID, dsID, "doc-a"); err != nil {
		t.Fatal(err)
	}
	if rec, err := svc.GetDocumentGovernance(ctx, tenantID, dsID, "doc-a"); err != nil || rec != nil {
		t.Fatalf("expected deleted, got %v err %v", rec, err)
	}
}

func TestDocumentGovernanceAuditTrailEmitted(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, _, dsID := newGovernanceService(t)

	if _, err := svc.SetDocumentGovernance(ctx, tenantID, governorID, dsID, "doc-a", governanceInput(model.GovernanceLevelInternal, "")); err != nil {
		t.Fatal(err)
	}
	audits, total, err := svc.Store.ListAudits(ctx, tenantID, 1, 20, repository.AuditFilter{Resource: "document-governance", Action: "created"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(audits) != 1 {
		t.Fatalf("expected one created audit, total=%d", total)
	}
	if audits[0].AuthorizationPolicyVersion != "t2-governance-v1" || audits[0].AuthorizationPermission != "execute:document-governance" {
		t.Fatalf("audit policy metadata missing: %+v", audits[0])
	}
	if audits[0].Action != "document_governance.created" {
		t.Fatalf("unexpected action %s", audits[0].Action)
	}
}

// ---------- 检索投影（T2.1/T2.2） ----------

// seedProjectionAnnotations writes annotations directly for projection tests.
func seedProjectionAnnotations(t *testing.T, svc *Service, tenantID string, governorID, dsID string, docs map[string]*model.DocumentGovernance) {
	t.Helper()
	ctx := context.Background()
	for _, g := range docs {
		g.TenantID = tenantID
		g.DatasetID = dsID
		g.CreatedAt = time.Now().UTC()
		g.UpdatedAt = g.CreatedAt
		if err := svc.Store.UpsertDocumentGovernance(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	_ = governorID
}

func annotationDoc(docID, level string, opts ...func(*model.DocumentGovernance)) *model.DocumentGovernance {
	now := time.Now().UTC()
	g := &model.DocumentGovernance{
		ID: id.New(), DocumentID: docID, Level: level,
		Authority: model.GovernanceDefaultAuthority, Status: model.GovernanceStatusPublished,
		Source: model.GovernanceSourceManual, ApprovalState: model.GovernanceApprovalApproved,
		CreatedBy: id.New(), ApprovedBy: stringPtr(id.New()), ApprovedAt: &now,
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

func stringPtr(s string) *string { return &s }

func TestProjectDocumentGovernanceNoAnnotationsOpenVisible(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, _, _, dsID := newGovernanceService(t)

	proj, err := svc.ProjectDocumentGovernance(ctx, tenantID, id.New(), dsID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if proj.PushdownUsed || proj.Pushdown != nil {
		t.Fatal("no-annotation projection must not push down a condition")
	}
	if len(proj.Excluded) != 0 || proj.FilteredOut {
		t.Fatalf("no-annotation must not exclude anything: %+v", proj)
	}
	if proj.Clearance != model.GovernanceLevelInternal {
		t.Fatalf("absent clearance must resolve internal, got %s", proj.Clearance)
	}
	if proj.Policy != DocumentGovernanceProjectionPolicyVersion {
		t.Fatalf("unexpected policy version %s", proj.Policy)
	}
}

func TestProjectDocumentGovernanceClearanceFiltering(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, _, memberID, dsID := newGovernanceService(t)
	seedProjectionAnnotations(t, svc, tenantID, "", dsID, map[string]*model.DocumentGovernance{
		"doc-internal":  annotationDoc("doc-internal", model.GovernanceLevelInternal),
		"doc-conf":      annotationDoc("doc-conf", model.GovernanceLevelConfidential),
		"doc-topsecret": annotationDoc("doc-topsecret", model.GovernanceLevelTopSecret),
	})
	// Member has default internal clearance → confidential & top_secret excluded.
	proj, err := svc.ProjectDocumentGovernance(ctx, tenantID, memberID, dsID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.Excluded) != 2 {
		t.Fatalf("internal clearance must exclude 2 sensitive docs, got %d: %+v", len(proj.Excluded), proj.Excluded)
	}
	if !proj.PushdownUsed || proj.Pushdown == nil {
		t.Fatal("expected pushdown condition")
	}
	reasons := map[string]string{}
	for _, e := range proj.Excluded {
		reasons[e.DocumentID] = e.Reason
	}
	if reasons["doc-conf"] != "beyond_clearance" || reasons["doc-topsecret"] != "beyond_clearance" {
		t.Fatalf("unexpected reasons %+v", reasons)
	}
}

func TestProjectDocumentGovernanceDraftAndInactiveExclusion(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, _, memberID, dsID := newGovernanceService(t)
	past := time.Now().UTC().Add(-time.Hour)
	seedProjectionAnnotations(t, svc, tenantID, "", dsID, map[string]*model.DocumentGovernance{
		"doc-draft": annotationDoc("doc-draft", model.GovernanceLevelInternal, func(g *model.DocumentGovernance) {
			g.Status = model.GovernanceStatusDraft
		}),
		"doc-rejected": annotationDoc("doc-rejected", model.GovernanceLevelInternal, func(g *model.DocumentGovernance) {
			g.ApprovalState = model.GovernanceApprovalRejected
		}),
		"doc-pending": annotationDoc("doc-pending", model.GovernanceLevelInternal, func(g *model.DocumentGovernance) {
			g.ApprovalState = model.GovernanceApprovalPending
		}),
		"doc-expired": annotationDoc("doc-expired", model.GovernanceLevelInternal, func(g *model.DocumentGovernance) {
			g.ExpiresAt = &past
		}),
		"doc-live": annotationDoc("doc-live", model.GovernanceLevelInternal),
	})
	proj, err := svc.ProjectDocumentGovernance(ctx, tenantID, memberID, dsID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.Excluded) != 4 {
		t.Fatalf("expected 4 inactive/draft exclusions, got %d: %+v", len(proj.Excluded), proj.Excluded)
	}
}

func TestProjectDocumentGovernanceEmptySetGuard(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, _, memberID, dsID := newGovernanceService(t)
	seedProjectionAnnotations(t, svc, tenantID, "", dsID, map[string]*model.DocumentGovernance{
		"doc-a": annotationDoc("doc-a", model.GovernanceLevelTopSecret),
		"doc-b": annotationDoc("doc-b", model.GovernanceLevelConfidential),
	})
	// Candidate retrieval domain = {doc-a, doc-b}, both beyond member clearance.
	proj, err := svc.ProjectDocumentGovernance(ctx, tenantID, memberID, dsID, []string{"doc-a", "doc-b"})
	if err != nil {
		t.Fatal(err)
	}
	if !proj.FilteredOut {
		t.Fatal("empty-set fail-safe must trigger FilteredOut when the domain is fully excluded")
	}
	if proj.Pushdown == nil || !proj.PushdownUsed {
		t.Fatal("pushdown must still be issued for engine-side defense")
	}
}

func TestMergeDocumentGovernanceConditionPreservesBase(t *testing.T) {
	base := &ragflow.MetadataCondition{
		Logic: "and",
		Conditions: []ragflow.MetadataConditionOp{
			{Name: "rgx_version", ComparisonOperator: operatorEqual, Value: "v2"},
		},
	}
	gov := &ragflow.MetadataCondition{
		Logic: "and",
		Conditions: []ragflow.MetadataConditionOp{
			{Name: metadataKeyGovernanceLevel, ComparisonOperator: operatorNotEqual, Value: model.GovernanceLevelTopSecret},
		},
	}
	merged := MergeDocumentGovernanceCondition(base, gov)
	if merged == nil || merged.Logic != "and" || len(merged.Conditions) != 2 {
		t.Fatalf("expected and-merged 2 ops, got %+v", merged)
	}
	if merged.Conditions[0].Value != "v2" || merged.Conditions[1].Name != metadataKeyGovernanceLevel {
		t.Fatalf("merge order/content wrong: %+v", merged.Conditions)
	}
	// nil governance → base preserved without clone surprises.
	if got := MergeDocumentGovernanceCondition(base, nil); len(got.Conditions) != 1 || got.Conditions[0].Value != "v2" {
		t.Fatalf("nil governance must keep base untouched, got %+v", got)
	}
}

func TestFilterChunksForGovernanceSecondaryFallback(t *testing.T) {
	excluded := map[string]string{"doc-b": "beyond_clearance"}
	chunks := []map[string]interface{}{
		{"document_id": "doc-a", "content": "visible"},
		{"document_id": "doc-b", "content": "secret"},
		{"doc_id": "doc-b", "content": "legacy-secret"},
		{"content": "no-id"},
	}
	filtered, dropped := FilterChunksForGovernance(chunks, excluded)
	if dropped != 2 || len(filtered) != 2 {
		t.Fatalf("expected 2 dropped 2 kept, got dropped=%d len=%d", dropped, len(filtered))
	}
	for _, c := range filtered {
		if c["document_id"] == "doc-b" || c["doc_id"] == "doc-b" {
			t.Fatalf("chunk from excluded document leaked: %+v", c)
		}
	}
	if got, _ := FilterChunksForGovernance(chunks, nil); len(got) != len(chunks) {
		t.Fatalf("nil exclusions must return chunks unchanged")
	}
}

func TestSyncDocumentGovernanceMetadataWritesMetaFields(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, _, dsID := newGovernanceService(t)
	mock := svc.RAGFlow.(*ragflow.Mock)

	link, err := svc.Store.GetDatasetLink(ctx, tenantID, dsID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := mock.CreateDocument(ctx, link.RAGFlowDatasetID, &ragflow.DocumentUpload{Name: "doc-sync.txt", Content: []byte("governance sync probe document")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, governorID, dsID, doc.ID, governanceInput(model.GovernanceLevelTopSecret, "")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncDocumentGovernanceMetadata(ctx, tenantID, governorID, dsID); err != nil {
		t.Fatal(err)
	}
	meta, err := mock.GetDatasetDocumentMetadata(ctx, link.RAGFlowDatasetID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta[metadataKeyGovernanceLevel] != model.GovernanceLevelTopSecret {
		t.Fatalf("rgx_governance_level not synced: %+v", meta)
	}
	if meta[metadataKeyGovernanceStatus] != model.GovernanceStatusPublished {
		t.Fatalf("rgx_governance_status not synced: %+v", meta)
	}
}
