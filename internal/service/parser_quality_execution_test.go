package service

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestExtractHeuristicParseMetrics(t *testing.T) {
	metrics := extractHeuristicParseMetrics([]string{
		"Report Title\nOperating revenue increased.",
		"Report Title\nOperating revenue declined.",
		"Report Title\n\ufffd",
		"   ",
	})

	if metrics["empty_page_ratio"] <= 0 || metrics["empty_page_ratio"] >= 1 {
		t.Fatalf("unexpected empty_page_ratio: %v", metrics["empty_page_ratio"])
	}
	if metrics["garbled_char_ratio"] <= 0 {
		t.Fatalf("garbled character must be observed: %+v", metrics)
	}
	if metrics["repeated_header_ratio"] <= 0 {
		t.Fatalf("repeated header must be observed: %+v", metrics)
	}
	if metrics["text_density"] <= 0 {
		t.Fatalf("healthy chunks must contribute text density: %+v", metrics)
	}
	for _, name := range []string{"column_count_consistency", "table_structure_consistency", "reading_order_anomaly"} {
		if _, ok := metrics[name]; !ok {
			t.Fatalf("metric %s must always be present", name)
		}
	}
}

func TestSyncTaskProgressRecordsParseQualityOnce(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	uploaded, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.pdf", "connector:policy:1", []byte("healthy policy"))
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}
	profile, err := svc.CreateQualityProfile(ctx, tenant.ID, "admin", qualityProfileInput("parse-quality-v1"))
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	policyInput := parserPolicyInput(profile.ID)
	policyInput.DocumentType = ""
	policyInput.DatasetID = ""
	policyInput.ProjectID = ""
	if _, err := svc.CreateParserPolicy(ctx, tenant.ID, policyInput); err != nil {
		t.Fatalf("create parser policy: %v", err)
	}
	datasetLink, err := svc.Store.GetDatasetLink(ctx, tenant.ID, dataset.ID)
	if err != nil || datasetLink == nil {
		t.Fatalf("load dataset link: %v %+v", err, datasetLink)
	}
	svc.RAGFlow.(*ragflow.Mock).SeedDocumentChunk(datasetLink.RAGFlowDatasetID, uploaded.ID, "chunk-1", "healthy policy content", true)

	for range 2 {
		if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
			t.Fatalf("sync parse progress: %v", err)
		}
	}

	report, err := svc.LatestParseQualityReport(ctx, tenant.ID, uploaded.ID)
	if err != nil || report == nil {
		t.Fatalf("latest parse quality report: %v %+v", err, report)
	}
	if report.QualityStatus != model.QualityStatusPass || report.GateAction != model.GateActionPublish {
		t.Fatalf("unexpected quality decision: %+v", report)
	}
	attempts, total, err := svc.ListParseAttempts(ctx, tenant.ID, uploaded.ID, 1, 10)
	if err != nil || total != 1 || len(attempts) != 1 {
		t.Fatalf("quality recording must be idempotent: attempts=%+v total=%d err=%v", attempts, total, err)
	}
}

func TestSyncTaskProgressMarksQuarantinedParseFailed(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	uploaded, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.pdf", "connector:policy:1", []byte("garbled"))
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}
	profile, err := svc.CreateQualityProfile(ctx, tenant.ID, "admin", qualityProfileInput("parse-quality-strict-v1"))
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	policyInput := parserPolicyInput(profile.ID)
	policyInput.DocumentType = ""
	policyInput.DatasetID = ""
	policyInput.ProjectID = ""
	_, err = svc.CreateParserPolicy(ctx, tenant.ID, policyInput)
	if err != nil {
		t.Fatalf("create parser policy: %v", err)
	}
	datasetLink, err := svc.Store.GetDatasetLink(ctx, tenant.ID, dataset.ID)
	if err != nil || datasetLink == nil {
		t.Fatalf("load dataset link: %v %+v", err, datasetLink)
	}
	svc.RAGFlow.(*ragflow.Mock).SeedDocumentChunk(datasetLink.RAGFlowDatasetID, uploaded.ID, "chunk-1", "\ufffd", true)

	if err := svc.SyncTaskProgress(ctx, tenant.ID); err != nil {
		t.Fatalf("sync parse progress: %v", err)
	}
	report, err := svc.LatestParseQualityReport(ctx, tenant.ID, uploaded.ID)
	if err != nil || report == nil {
		t.Fatalf("latest parse quality report: %v %+v", err, report)
	}
	if report.GateAction != model.GateActionQuarantine {
		t.Fatalf("expected QUARANTINE, got %+v", report)
	}
	ledger, err := svc.Store.GetIncrementalLedgerByDocument(ctx, tenant.ID, uploaded.ID)
	if err != nil || ledger == nil {
		t.Fatalf("load incremental ledger: %v %+v", err, ledger)
	}
	if ledger.State != model.IncrementalStateFailed {
		t.Fatalf("quarantined document must fail incremental activation: %+v", ledger)
	}
}

func TestRecordParseQualityAutomaticallyExecutesFallbackChain(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	uploaded, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.pdf", "connector:policy:1", []byte("bad"))
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}
	profile, err := svc.CreateQualityProfile(ctx, tenant.ID, "admin", qualityProfileInput("parse-quality-strict-v1"))
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	primaryInput := parserPolicyInput(profile.ID)
	primaryInput.DocumentType = ""
	primaryInput.DatasetID = ""
	primaryInput.ProjectID = ""
	primary, err := svc.CreateParserPolicy(ctx, tenant.ID, primaryInput)
	if err != nil {
		t.Fatalf("create primary policy: %v", err)
	}
	fallbackInput := parserPolicyInput(profile.ID)
	fallbackInput.DocumentType = "pdf_text"
	fallbackInput.DatasetID = ""
	fallbackInput.ProjectID = ""
	fallbackInput.ChunkMethod = "general"
	fallback, err := svc.CreateParserPolicy(ctx, tenant.ID, fallbackInput)
	if err != nil {
		t.Fatalf("create fallback policy: %v", err)
	}
	raw := json.RawMessage(`{"fallback_policy_id":"` + fallback.ID + `","max_attempts":2}`)
	if _, err := svc.UpdateParserPolicy(ctx, tenant.ID, primary.ID, ParserPolicyPatchInput{FallbackPolicy: &raw}); err != nil {
		t.Fatalf("configure fallback: %v", err)
	}
	datasetLink, err := svc.Store.GetDatasetLink(ctx, tenant.ID, dataset.ID)
	if err != nil || datasetLink == nil {
		t.Fatalf("load dataset link: %v %+v", err, datasetLink)
	}
	mock := svc.RAGFlow.(*ragflow.Mock)
	mock.SeedDocumentChunk(datasetLink.RAGFlowDatasetID, uploaded.ID, "bad-1", "\ufffd", true)
	documents, err := mock.ListDocuments(ctx, datasetLink.RAGFlowDatasetID)
	if err != nil {
		t.Fatalf("list mock documents: %v", err)
	}
	var document ragflow.Document
	for _, item := range documents {
		if item.ID == uploaded.ID {
			document = item
			break
		}
	}
	if document.ID == "" {
		t.Fatal("mock document not found")
	}

	report, err := svc.recordParseQualityForDocument(ctx, tenant.ID, datasetLink, document, "system", document.ID)
	if err != nil {
		t.Fatalf("record first quality: %v", err)
	}
	if report.GateAction != model.GateActionRetry {
		t.Fatalf("expected retry, got %+v", report)
	}
	config := mock.LastDocumentParseConfigUpdate()
	if config == nil || config.Pipeline || config.BuiltinParserID != "general" || config.MetaFields["rgx_parser_policy_id"] != fallback.ID {
		t.Fatalf("fallback parse config was not applied: %+v", config)
	}
	parseIDs := mock.LastParseDocumentIDs()
	if len(parseIDs) != 1 || parseIDs[0] != uploaded.ID {
		t.Fatalf("fallback reparse was not triggered: %v", parseIDs)
	}

	document.UpdateTime = time.Now().Add(time.Millisecond).UnixMilli()
	report, err = svc.recordParseQualityForDocument(ctx, tenant.ID, datasetLink, document, "system", document.ID)
	if err != nil {
		t.Fatalf("record fallback quality: %v", err)
	}
	if report.ParserPolicyID != fallback.ID || report.GateAction != model.GateActionQuarantine {
		t.Fatalf("expected fallback report quarantine: %+v", report)
	}
	attempts, total, err := svc.ListParseAttempts(ctx, tenant.ID, uploaded.ID, 1, 10)
	if err != nil || total != 2 || len(attempts) != 2 {
		t.Fatalf("expected full attempt chain: attempts=%+v total=%d err=%v", attempts, total, err)
	}
}

func TestDocumentParseQualityReadsStayInsideDataset(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	uploaded, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.pdf", "connector:policy:1", []byte("policy"))
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}
	profile, err := svc.CreateQualityProfile(ctx, tenant.ID, "admin", qualityProfileInput("parse-quality-v1"))
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	policy, err := svc.CreateParserPolicy(ctx, tenant.ID, parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatalf("create parser policy: %v", err)
	}
	startedAt := time.Now().UTC()
	if _, _, _, err := svc.RecordParseQuality(ctx, tenant.ID, ParseQualityRequest{
		ParserPolicyID:   policy.ID,
		DatasetID:        dataset.ID,
		DocumentID:       uploaded.ID,
		UserID:           "admin",
		StartedAt:        startedAt,
		FinishedAt:       startedAt,
		HeuristicMetrics: map[string]float64{"text_density": 0.98, "empty_page_ratio": 0},
	}); err != nil {
		t.Fatalf("record quality: %v", err)
	}

	attempts, total, err := svc.ListDocumentParseAttempts(ctx, tenant.ID, dataset.ID, uploaded.ID, 1, 10)
	if err != nil || total != 1 || len(attempts) != 1 {
		t.Fatalf("list document attempts: %v %d %+v", err, total, attempts)
	}
	report, err := svc.GetLatestDocumentParseQualityReport(ctx, tenant.ID, dataset.ID, uploaded.ID)
	if err != nil || report == nil || report.QualityStatus != model.QualityStatusPass {
		t.Fatalf("latest document report: %v %+v", err, report)
	}
	if _, _, err := svc.ListDocumentParseAttempts(ctx, tenant.ID, dataset.ID, "other-document", 1, 10); err == nil {
		t.Fatal("cross-document access must fail")
	}
}

func TestListDatasetLatestParseQualityReports(t *testing.T) {
	svc, ctx, tenant, dataset := createIncrementalFixture(t)
	uploaded, err := svc.UploadDocumentWithSourceKey(ctx, tenant.ID, dataset.ID, "admin", "policy.pdf", "connector:policy:1", []byte("policy"))
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}
	profile, err := svc.CreateQualityProfile(ctx, tenant.ID, "admin", qualityProfileInput("parse-quality-v1"))
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	policy, err := svc.CreateParserPolicy(ctx, tenant.ID, parserPolicyInput(profile.ID))
	if err != nil {
		t.Fatalf("create parser policy: %v", err)
	}
	startedAt := time.Now().UTC()
	for attempt := range 2 {
		if _, _, _, err := svc.RecordParseQuality(ctx, tenant.ID, ParseQualityRequest{
			ParserPolicyID:   policy.ID,
			DatasetID:        dataset.ID,
			DocumentID:       uploaded.ID,
			UserID:           "admin",
			StartedAt:        startedAt,
			FinishedAt:       startedAt,
			HeuristicMetrics: map[string]float64{"text_density": 0.90 + float64(attempt)*0.05, "empty_page_ratio": 0},
		}); err != nil {
			t.Fatalf("record quality attempt %d: %v", attempt+1, err)
		}
	}

	reports, total, err := svc.ListDatasetLatestParseQualityReports(ctx, tenant.ID, dataset.ID, 1, 20)
	if err != nil || total != 1 || len(reports) != 1 {
		t.Fatalf("dataset reports: %v total=%d items=%+v", err, total, reports)
	}
	if reports[0].DocumentID != uploaded.ID || math.Abs(reports[0].HeuristicScore-0.95) > 0.000001 {
		t.Fatalf("latest report not selected: %+v", reports[0])
	}
}
