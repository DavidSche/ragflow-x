package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func testDatasetRAGFlowID(t *testing.T, svc *Service, tenantID, dsID string) (string, *ragflow.Mock) {
	t.Helper()
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		// In case the harness is ever backed by a real client, fall back to a
		// fresh mock so document listing works deterministically.
		t.Fatalf("governance auto projection test requires a mock RAGFlow client")
	}
	link, err := svc.Store.GetDatasetLink(context.Background(), tenantID, dsID)
	if err != nil || link == nil {
		t.Fatalf("dataset link for %s unavailable: %v", dsID, err)
	}
	return link.RAGFlowDatasetID, mock
}

func addQualityReport(t *testing.T, svc *Service, tenantID, documentID string, score float64) {
	t.Helper()
	report := &model.ParseQualityReport{
		ID: id.New(), TenantID: tenantID, DocumentID: documentID,
		ParserPolicyID: id.New(), AttemptID: id.New(), ParseMode: "deepdoc",
		QualityStatus: model.QualityStatusPass, GateAction: model.GateActionPublish,
		EffectiveScore: score, Metrics: "{}", CreatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateParseQualityReport(context.Background(), report); err != nil {
		t.Fatal(err)
	}
}

func TestAutoProjectGovernanceHappyPath(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, _, dsID := newGovernanceService(t)
	ragID, mock := testDatasetRAGFlowID(t, svc, tenantID, dsID)

	_, err := svc.CreateGovernanceSchema(ctx, tenantID, governorID, GovernanceSchemaInput{
		Name: "绝密识别", Category: model.DictCategoryLevel, MatcherType: model.SchemaMatcherRegex,
		Pattern: `(?i)top.?secret`, TargetKey: "top_secret", QualityMin: 60,
		BindScope: model.SchemaBindDataset, DatasetID: dsID,
	})
	if err != nil {
		t.Fatal(err)
	}

	doc, err := mock.CreateDocument(ctx, ragID, &ragflow.DocumentUpload{Name: "Q3-top_secret-board.pdf"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.AutoProjectGovernance(ctx, tenantID, governorID, dsID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 1 || res.Matched != 1 || res.Written != 1 || res.SchemaUsed != 1 {
		t.Fatalf("auto projection counters unexpected: %+v", res)
	}
	if res.SyncFailed != 0 {
		t.Fatalf("sync should succeed in mock, got SyncFailed=%d", res.SyncFailed)
	}

	// Annotation written as auto source, approved.
	rec, err := svc.GetDocumentGovernance(ctx, tenantID, dsID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.Level != model.GovernanceLevelTopSecret || rec.Source != model.GovernanceSourceAuto ||
		rec.ApprovalState != model.GovernanceApprovalApproved {
		t.Fatalf("auto annotation unexpected: %+v", rec)
	}

	// Metadata pushed back to RAGFlow (T1 自动投影回填).
	docs, err := mock.ListDocuments(ctx, ragID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("mock documents = %d", len(docs))
	}
	if docs[0].Metadata[metadataKeyGovernanceLevel] != model.GovernanceLevelTopSecret {
		t.Fatalf("governance level metadata not synced: %+v", docs[0].Metadata)
	}
}

func TestAutoProjectGovernanceQualityGate(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, _, dsID := newGovernanceService(t)
	ragID, mock := testDatasetRAGFlowID(t, svc, tenantID, dsID)

	_, err := svc.CreateGovernanceSchema(ctx, tenantID, governorID, GovernanceSchemaInput{
		Name: "绝密识别", Category: model.DictCategoryLevel, MatcherType: model.SchemaMatcherRegex,
		Pattern: `(?i)top.?secret`, TargetKey: "top_secret", QualityMin: 90,
		BindScope: model.SchemaBindDataset, DatasetID: dsID,
	})
	if err != nil {
		t.Fatal(err)
	}

	doc, err := mock.CreateDocument(ctx, ragID, &ragflow.DocumentUpload{Name: "Q3-top_secret-board.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	// 抽取质量 40 < 门槛 90 → 不进入投影。
	addQualityReport(t, svc, tenantID, doc.ID, 40)

	res, err := svc.AutoProjectGovernance(ctx, tenantID, governorID, dsID)
	if err != nil {
		t.Fatal(err)
	}
	if res.QualitySkipped != 1 || res.Written != 0 {
		t.Fatalf("quality gate should skip the document, got %+v", res)
	}
	rec, err := svc.GetDocumentGovernance(ctx, tenantID, dsID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatalf("quality-gated document must not enter projection, got %+v", rec)
	}
}

func TestAutoProjectGovernanceManualOverride(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, memberID, dsID := newGovernanceService(t)
	ragID, mock := testDatasetRAGFlowID(t, svc, tenantID, dsID)

	_, err := svc.CreateGovernanceSchema(ctx, tenantID, governorID, GovernanceSchemaInput{
		Name: "绝密识别", Category: model.DictCategoryLevel, MatcherType: model.SchemaMatcherRegex,
		Pattern: `(?i)top.?secret`, TargetKey: "top_secret", QualityMin: 60,
		BindScope: model.SchemaBindDataset, DatasetID: dsID,
	})
	if err != nil {
		t.Fatal(err)
	}

	doc, err := mock.CreateDocument(ctx, ragID, &ragflow.DocumentUpload{Name: "Q3-top_secret-board.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	// Member 先在内部密级手动标注 → auto 不得覆盖（人工优先）。
	if _, err := svc.SetDocumentGovernance(ctx, tenantID, memberID, dsID, doc.ID, DocumentGovernanceInput{
		Level: model.GovernanceLevelInternal, Status: model.GovernanceStatusPublished,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.AutoProjectGovernance(ctx, tenantID, governorID, dsID)
	if err != nil {
		t.Fatal(err)
	}
	if res.ManualOverride != 1 || res.Written != 0 {
		t.Fatalf("auto must not override manual annotation, got %+v", res)
	}
	rec, err := svc.GetDocumentGovernance(ctx, tenantID, dsID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Level != model.GovernanceLevelInternal || rec.Source != model.GovernanceSourceManual {
		t.Fatalf("manual annotation must survive, got %+v", rec)
	}
}

func TestAutoProjectGovernanceDormantExtension(t *testing.T) {
	ctx := context.Background()
	svc, tenantID, governorID, _, dsID := newGovernanceService(t)
	ragID, mock := testDatasetRAGFlowID(t, svc, tenantID, dsID)

	// 租户扩展词条 approved，但不在 T2 可投影档位 → dormant。
	entry, err := svc.CreateGovernanceDictionaryEntry(ctx, tenantID, governorID, dictEntryInput(model.DictCategoryLevel, "restricted", "受控"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideGovernanceDictionaryEntry(ctx, tenantID, governorID, entry.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateGovernanceSchema(ctx, tenantID, governorID, GovernanceSchemaInput{
		Name: "受控识别", Category: model.DictCategoryLevel, MatcherType: model.SchemaMatcherKeyword,
		Pattern: "restricted", TargetKey: "restricted", QualityMin: 60,
		BindScope: model.SchemaBindDataset, DatasetID: dsID,
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := mock.CreateDocument(ctx, ragID, &ragflow.DocumentUpload{Name: "restricted-report.pdf"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.AutoProjectGovernance(ctx, tenantID, governorID, dsID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dormant != 1 || res.Written != 0 {
		t.Fatalf("extension target must be dormant (not projectable yet), got %+v", res)
	}
	rec, err := svc.GetDocumentGovernance(ctx, tenantID, dsID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatalf("dormant extension must not write annotation, got %+v", rec)
	}
}
