package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// The four P0-A isolation faces (doc/288 §7.6 "隔离测试，P0-A，不得后移"): every
// by-ID accessor must carry `tenant_id = ?`, so a foreign tenant always sees
// NotFound / an empty list — never a cross-tenant row, never a hint that the ID
// exists anywhere.
//
// This file seeds one tenant (t-a) through the real report write paths
// (CreateReport → CreateReportRevision → CreateReportJob → CreateReportSection →
// CreateReportExport; evidence + citation rows are fixture-seeded through the
// same GORM handle the service suite uses for direct state plumbing) and then
// proves tenant t-b cannot observe a single row through any report-family read.
//
// The handler-level object checks (guess an ID → failure before any state walk)
// live in internal/handler/report_tenant_test.go; here we pin the store so the
// four §7.6 faces are asserted at the layer that actually builds the SQL.

type reportTenantSeed struct {
	tenantID   string
	reportID   string
	revisionID string
	jobID      string
	artifactID string
}

func seedReportTenantA(t *testing.T, st *store) reportTenantSeed {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	report := &model.Report{
		TenantID:     "t-a",
		Title:        "隔离种子报告",
		Question:     "跨租户是否可见？",
		OwnerUserID:  "owner-a",
		TemplateKey:  "financial_review",
		ExpiresAt:    now.AddDate(0, 0, 90),
		AccessPolicy: model.ReportAccessOwnerOnly,
		Status:       model.ReportStatusActive,
	}
	if err := st.CreateReport(ctx, report); err != nil {
		t.Fatalf("create report: %v", err)
	}

	revision := &model.ReportRevision{
		ReportID:            report.ID,
		TenantID:            "t-a",
		Revision:            1,
		OutlineConfirmation: model.ReportOutlineRequired,
		DatasetsJSON:        "[]",
		TemplateKey:         "financial_review",
		TemplateVersion:     1,
		EngineVersion:       "test",
		Status:              model.ReportRevisionPublished,
		Completeness:        model.ReportCompletenessFull,
		CreatedBy:           "owner-a",
		CreatedAt:           now,
	}
	if err := st.CreateReportRevision(ctx, revision); err != nil {
		t.Fatalf("create revision: %v", err)
	}

	job := &model.ReportJob{
		TenantID:       "t-a",
		ReportID:       report.ID,
		RevisionID:     revision.ID,
		JobKind:        model.ReportJobKindGenerate,
		Status:         model.JobStatusSucceeded,
		Stage:          model.ReportStageAssembling,
		AttemptSeq:     1,
		SliceSeq:       1,
		ProgressDone:   1,
		ProgressTotal:  1,
		IdempotencyKey: "tenant-seed-ik-0001",
		UpdatedAt:      now,
	}
	if err := st.CreateReportJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	section := &model.ReportSection{
		TenantID:         "t-a",
		RevisionID:       revision.ID,
		Ord:              1,
		SectionKey:       "intro",
		SectionVersion:   1,
		Title:            "概述",
		RequiresEvidence: true,
		Status:           model.ReportSectionSucceeded,
		QualityStatus:    model.ReportQualityNotChecked,
		EvidenceStatus:   model.ReportEvidenceSupported,
		ContentMD:        "## 概述\n结论文本",
		ContentHash:      "hash-1",
		CitationCount:    1,
	}
	if err := st.CreateReportSection(ctx, section); err != nil {
		t.Fatalf("create section: %v", err)
	}

	// Evidence + citation rows are normally written by CommitReportSection; to
	// keep this isolation file out of the whole slice state machine they are
	// fixture-seeded through the GORM handle, matching the service-suite
	// convention ("fx.gdb 直改") for state plumbing.
	citation := &model.ReportCitation{
		TenantID:           "t-a",
		RevisionID:         revision.ID,
		SectionID:          section.ID,
		SectionOrd:         1,
		SectionVersion:     1,
		Ord:                1,
		EvidenceSnapshotID: "snap-a",
		SourceDocumentID:   "doc-a",
		ReferencedAt:       now,
		ReferencedBy:       "engine",
		UsedInText:         true,
	}
	if err := st.DB.Create(citation).Error; err != nil {
		t.Fatalf("seed citation: %v", err)
	}
	evidence := &model.ReportEvidenceSnapshot{
		TenantID:          "t-a",
		ChunkID:           "chunk-a",
		SourceContentHash: "src-hash-a",
		Content:           "原始证据片段",
		ContentHash:       "content-hash-a",
		ContentSize:       int64(len("原始证据片段")),
		ExpiresAt:         now.AddDate(0, 0, 90),
	}
	if err := st.DB.Create(evidence).Error; err != nil {
		t.Fatalf("seed evidence: %v", err)
	}

	snapshotID := id.New()
	exportJobID := id.New()
	artifact, err := st.CreateReportExport(ctx, ReportExportRows{
		TenantID:   "t-a",
		RevisionID: revision.ID,
		Snapshot: &model.ExportSnapshot{
			ID:                snapshotID,
			TenantID:          "t-a",
			SubjectType:       model.ExportSubjectReport,
			ReportRevisionID:  revision.ID,
			AnswerSnapshotIDs: "[]",
			TemplateID:        "tpl",
			TemplateVersion:   1,
			PolicyVersion:     "v1",
			PolicyEvaluatedAt: now,
			PolicyInputHash:   "in",
			CanonicalHash:     "canonical-a",
			PayloadJSON:       "{}",
			CreatedAt:         now,
		},
		Job: &model.ExportJob{
			ID:                exportJobID,
			TenantID:          "t-a",
			ExportSnapshotID:  snapshotID,
			SubjectType:       model.ExportSubjectReport,
			ReportRevisionID:  revision.ID,
			AnswerSnapshotID:  "ans-a",
			RequestedBy:       "owner-a",
			Format:            model.ExportFormatDOCX,
			Status:            model.ExportJobSucceeded,
			TemplateVersion:   1,
			PolicyVersion:     "v1",
			PolicyEvaluatedAt: now,
			PolicyInputHash:   "in",
			IdempotencyKey:    "tenant-seed-export-ik-0001",
			CreatedAt:         now,
		},
		Artifact: &model.ExportArtifact{
			ID:               id.New(),
			TenantID:         "t-a",
			ExportJobID:      exportJobID,
			SubjectType:      model.ExportSubjectReport,
			ReportRevisionID: revision.ID,
			ArtifactStatus:   model.ExportArtifactReady,
			Format:           model.ExportFormatDOCX,
			RendererVersion:  "go-docx-v1",
			FileRef:          filepath.Join("t-a", "art.docx"),
			Filename:         "report.docx",
			MimeType:         "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			ByteSize:         42,
			SHA256:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			CreatedAt:        now,
			ExpiresAt:        now.AddDate(0, 0, 30),
		},
	})
	if err != nil {
		t.Fatalf("create export: %v", err)
	}

	return reportTenantSeed{
		tenantID:   "t-a",
		reportID:   report.ID,
		revisionID: revision.ID,
		jobID:      job.ID,
		artifactID: artifact.ID,
	}
}

// ① by-ID accessors: a foreign tenant gets NotFound on every one, while the own
// tenant still sees the row (no cross-tenant visibility, no existence oracle).
func TestReportTenantIsolationRepoByIDAccessors(t *testing.T) {
	st := newAuditStore(t).(*store)
	ctx := context.Background()
	seed := seedReportTenantA(t, st)

	// Own tenant sanity first: the record exists behind t-a.
	if got, err := st.GetReport(ctx, seed.tenantID, seed.reportID); err != nil || got == nil {
		t.Fatalf("sanity: own tenant load report: err=%v", err)
	}
	if got, err := st.GetReportRevisionByID(ctx, seed.tenantID, seed.revisionID); err != nil || got == nil {
		t.Fatalf("sanity: own tenant load revision: err=%v", err)
	}
	if got, err := st.GetLatestReportJob(ctx, seed.tenantID, seed.reportID); err != nil || got == nil {
		t.Fatalf("sanity: own tenant load job: err=%v", err)
	}
	if arts, err := st.ListReportArtifacts(ctx, seed.tenantID, seed.revisionID); err != nil || len(arts) != 1 {
		t.Fatalf("sanity: own tenant artifacts: n=%d err=%v", len(arts), err)
	}

	// Foreign tenant: every by-ID read is a clean NotFound.
	if got, err := st.GetReport(ctx, "t-b", seed.reportID); err != nil || got != nil {
		t.Fatalf("cross-tenant GetReport must be NotFound: row=%v err=%v", got, err)
	}
	if got, err := st.GetReportRevision(ctx, "t-b", seed.reportID, 1); err != nil || got != nil {
		t.Fatalf("cross-tenant GetReportRevision must be NotFound: row=%v err=%v", got, err)
	}
	if got, err := st.GetReportRevisionByID(ctx, "t-b", seed.revisionID); err != nil || got != nil {
		t.Fatalf("cross-tenant GetReportRevisionByID must be NotFound: row=%v err=%v", got, err)
	}
	if got, err := st.GetLatestReportJob(ctx, "t-b", seed.reportID); err != nil || got != nil {
		t.Fatalf("cross-tenant GetLatestReportJob must be NotFound: row=%v err=%v", got, err)
	}
	if got, err := st.GetReportJob(ctx, "t-b", seed.jobID); err != nil || got != nil {
		t.Fatalf("cross-tenant GetReportJob must be NotFound: row=%v err=%v", got, err)
	}
	if got, err := st.GetReportJobByIdempotencyKey(ctx, "t-b", "tenant-seed-ik-0001"); err != nil || got != nil {
		t.Fatalf("cross-tenant GetReportJobByIdempotencyKey must be NotFound: row=%v err=%v", got, err)
	}
	if got, err := st.ReportExportArtifact(ctx, "t-b", seed.artifactID); err != nil || got != nil {
		t.Fatalf("cross-tenant ReportExportArtifact must be NotFound: row=%v err=%v", got, err)
	}
}

// A15's trend read is a list face too, and it joins the report row to reach
// `owner_user_id` — the join is exactly where a forgotten tenant predicate shows up as
// another workspace's quality numbers. It is asserted separately from ListReports
// because the two queries narrow differently (the trend filters on the revision's
// clock and drops soft-deleted parents).
func TestReportMetricsQueryIsTenantScoped(t *testing.T) {
	st := newAuditStore(t).(*store)
	ctx := context.Background()
	seed := seedReportTenantA(t, st)
	now := time.Now().UTC()

	window := ReportMetricsQuery{
		TenantID: seed.tenantID,
		From:     now.AddDate(0, 0, -1),
		To:       now.AddDate(0, 0, 1),
	}
	rows, err := st.ListReportRevisionsForMetrics(ctx, window)
	if err != nil {
		t.Fatalf("own tenant trend read: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != seed.revisionID {
		t.Fatalf("sanity: own tenant must see the published revision: %+v", rows)
	}

	foreign := window
	foreign.TenantID = "t-b"
	if rows, err := st.ListReportRevisionsForMetrics(ctx, foreign); err != nil || len(rows) != 0 {
		t.Fatalf("cross-tenant trend read must be empty: n=%d err=%v", len(rows), err)
	}

	// A soft-deleted report stops counting immediately: the trend may not keep
	// reporting the quality of a report the user has deleted (A47).
	if err := st.SoftDeleteReport(ctx, seed.tenantID, seed.reportID, now, now.AddDate(0, 0, 7)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if rows, err := st.ListReportRevisionsForMetrics(ctx, window); err != nil || len(rows) != 0 {
		t.Fatalf("a deleted report must leave the trend: n=%d err=%v", len(rows), err)
	}
}

// ④ list frames: a foreign tenant gets empty collections, never rows; the own
// tenant's lists still return the seed.
func TestReportTenantIsolationRepoListFaces(t *testing.T) {
	st := newAuditStore(t).(*store)
	ctx := context.Background()
	seed := seedReportTenantA(t, st)

	if rows, err := st.ListReports(ctx, "t-b", "", 100, 0); err != nil || len(rows) != 0 {
		t.Fatalf("cross-tenant ListReports must be empty: n=%d err=%v", len(rows), err)
	}
	if n, err := st.CountReports(ctx, "t-b", ""); err != nil || n != 0 {
		t.Fatalf("cross-tenant CountReports must be zero: n=%d err=%v", n, err)
	}
	if sections, err := st.ListReportSections(ctx, "t-b", seed.revisionID); err != nil || len(sections) != 0 {
		t.Fatalf("cross-tenant ListReportSections must be empty: n=%d err=%v", len(sections), err)
	}
	if citations, err := st.ListReportCitations(ctx, "t-b", seed.revisionID); err != nil || len(citations) != 0 {
		t.Fatalf("cross-tenant ListReportCitations must be empty: n=%d err=%v", len(citations), err)
	}
	if arts, err := st.ListReportArtifacts(ctx, "t-b", seed.revisionID); err != nil || len(arts) != 0 {
		t.Fatalf("cross-tenant ListReportArtifacts must be empty: n=%d err=%v", len(arts), err)
	}

	if rows, err := st.ListReports(ctx, seed.tenantID, "", 100, 0); err != nil || len(rows) != 1 {
		t.Fatalf("own tenant ListReports must see the seed: n=%d err=%v", len(rows), err)
	}
	if sections, err := st.ListReportSections(ctx, seed.tenantID, seed.revisionID); err != nil || len(sections) != 1 {
		t.Fatalf("own tenant ListReportSections must see the seed: n=%d err=%v", len(sections), err)
	}
	if citations, err := st.ListReportCitations(ctx, seed.tenantID, seed.revisionID); err != nil || len(citations) != 1 {
		t.Fatalf("own tenant ListReportCitations must see the seed: n=%d err=%v", len(citations), err)
	}
	if arts, err := st.ListReportArtifacts(ctx, seed.tenantID, seed.revisionID); err != nil || len(arts) != 1 {
		t.Fatalf("own tenant ListReportArtifacts must see the seed: n=%d err=%v", len(arts), err)
	}
}

// TestReportTenantIsolationP0BAccessors closes the T15 gap for the accessors added
// by P0-B: the evidence appendix read path (T14/A43), the ledger sums (T3), the
// event replay (T17) and the tenant retention overlay (T20b). Each is a by-id or
// by-tenant read, so each must be blind to another workspace for the same reason the
// P0-A accessors are.
//
// These are asserted at the store because that is where the `WHERE tenant_id` lives:
// a missing clause here cannot be repaired by a handler, since the handler never sees
// the row to reject.
func TestReportTenantIsolationP0BAccessors(t *testing.T) {
	st := newAuditStore(t).(*store)
	ctx := context.Background()
	seed := seedReportTenantA(t, st)
	now := time.Now().UTC()

	// Evidence appendix reads (T14): ids are guessed from the owning tenant's rows.
	var evidence model.ReportEvidenceSnapshot
	if err := st.DB.Where("tenant_id = ?", seed.tenantID).First(&evidence).Error; err != nil {
		t.Fatalf("load seeded evidence: %v", err)
	}
	// Point the seeded citation at the seeded evidence row so "verifiable" is a
	// fact about this fixture rather than a dangling pointer.
	if err := st.DB.Model(&model.ReportCitation{}).
		Where("tenant_id = ? AND revision_id = ?", seed.tenantID, seed.revisionID).
		Update("evidence_snapshot_id", evidence.ID).Error; err != nil {
		t.Fatalf("repoint citation: %v", err)
	}
	if rows, err := st.ListReportEvidenceByIDs(ctx, seed.tenantID, []string{evidence.ID}); err != nil || len(rows) != 1 {
		t.Fatalf("own tenant evidence lookup must return the row: n=%d err=%v", len(rows), err)
	}
	if rows, err := st.ListReportEvidenceByIDs(ctx, "t-b", []string{evidence.ID}); err != nil || len(rows) != 0 {
		t.Fatalf("cross-tenant evidence lookup must be empty: n=%d err=%v", len(rows), err)
	}
	// A43's count answers "are the citations verifiable" — a foreign tenant must not
	// even be able to ask the question about another workspace's revision, so the
	// tenant-scoped count is zero rather than "1 unverifiable".
	if n, err := st.CountUnverifiableReportEvidence(ctx, seed.tenantID, seed.revisionID, now); err != nil || n != 0 {
		t.Fatalf("own tenant citations are verifiable: n=%d err=%v", n, err)
	}
	if n, err := st.CountUnverifiableReportEvidence(ctx, "t-b", seed.revisionID, now); err != nil || n != 0 {
		t.Fatalf("cross-tenant evidence count must be zero: n=%d err=%v", n, err)
	}

	// Ledger sums (T3/T20b): keyed by tenant + business job.
	if sums, err := st.SumReportCharges(ctx, "t-b", seed.jobID); err != nil || sums.Held != 0 || sums.Actual != 0 {
		t.Fatalf("cross-tenant ledger sums must be zero: %+v err=%v", sums, err)
	}

	// Event replay (T17): a foreign job id yields no events, not someone's timeline.
	if events, err := st.ListReportEvents(ctx, "t-b", seed.jobID, 0, 100); err != nil || len(events) != 0 {
		t.Fatalf("cross-tenant event replay must be empty: n=%d err=%v", len(events), err)
	}

	// Retention overlay (T20b): one policy row per tenant, never shared.
	evidenceDays := 120
	if err := st.UpsertTenantReportPolicy(ctx, &model.TenantReportPolicy{
		TenantID: seed.tenantID, ReportEvidenceDays: &evidenceDays, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	if policy, err := st.GetTenantReportPolicy(ctx, "t-b"); err != nil || policy != nil {
		t.Fatalf("a workspace without an overlay must read nil, got %+v err=%v", policy, err)
	}
	if policy, err := st.GetTenantReportPolicy(ctx, seed.tenantID); err != nil || policy == nil ||
		policy.ReportEvidenceDays == nil || *policy.ReportEvidenceDays != evidenceDays {
		t.Fatalf("own overlay must round-trip: %+v err=%v", policy, err)
	}

	// Template catalog (A16): a tenant fork is invisible to another workspace.
	fork := &model.ReportTemplate{
		TenantID:            seed.tenantID,
		TemplateKey:         "financial_review",
		Version:             1,
		NameJSON:            `{"zh":"租户克隆","en":"tenant fork"}`,
		Status:              model.ReportTemplateStatusActive,
		OutlineConfirmation: model.ReportOutlineRequired,
		CreatedAt:           now,
	}
	if err := st.CreateReportTemplate(ctx, fork); err != nil {
		t.Fatalf("create fork: %v", err)
	}
	if got, err := st.GetReportTemplate(ctx, "t-b", "financial_review", 1); err != nil || got != nil {
		t.Fatalf("cross-tenant template load must be NotFound: row=%v err=%v", got, err)
	}
	if rows, err := st.ListReportTemplates(ctx, "t-b"); err != nil || len(rows) != 0 {
		t.Fatalf("cross-tenant template list must be empty: n=%d err=%v", len(rows), err)
	}
	if rows, err := st.ListReportTemplates(ctx, seed.tenantID); err != nil || len(rows) != 1 {
		t.Fatalf("own template list must see the fork: n=%d err=%v", len(rows), err)
	}
}
