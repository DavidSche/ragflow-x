package service

// Tests for the frozen conflict-candidate pool (doc/301 §5.3.1) and the frozen
// `scan_top_k` that decides its width.
//
// Two things are load-bearing and neither is visible from a happy-path report:
//
//   - `scan_top_k = 0` freezes **nothing**. The default must not quietly copy corpus
//     text into a second table for every installation.
//   - a retried section **rewrites** its candidate rows instead of appending a second
//     copy of the pool, because the row id is derived from `(section, chunk)`.
//
// The clock assertions matter for the same reason retention exists: a candidate that
// outlives its report is the last surviving copy of a chunk the workspace was told
// would be deleted.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// candidateRun builds the smallest `reportSectionRun` that can freeze a pool: the
// service for persistence/retention, the two clocks, and the frozen snapshot the
// section would have carried.
func candidateRun(fx *reportFixture, revisionID, reportID string, scanTopK int) *reportSectionRun {
	return &reportSectionRun{
		svc:      fx.svc,
		ctx:      context.Background(),
		tenantID: fx.tenantID,
		business: &model.ReportJob{ID: "job-candidate-test", TenantID: fx.tenantID},
		revision: &model.ReportRevision{ID: revisionID, TenantID: fx.tenantID, ReportID: reportID},
		report: &model.Report{
			ID: reportID, TenantID: fx.tenantID, OwnerUserID: fx.owner.ID,
			ExpiresAt: time.Now().UTC().Add(48 * time.Hour),
		},
		snapshot: reportExecutionSnapshot{
			Datasets: []reportDatasetBinding{{DatasetID: "dataset-local", EngineDatasetID: "engine-1"}},
		},
		scanTopK: scanTopK,
	}
}

func candidateEvidence() []reportEvidenceItem {
	return []reportEvidenceItem{
		{Index: 1, ChunkID: "chunk-a", DocumentID: "doc-1", EngineDataset: "engine-1", SourceContent: "差旅标准为每天 800 元。"},
		{Index: 2, ChunkID: "chunk-b", DocumentID: "doc-1", EngineDataset: "engine-1", SourceContent: "出差住宿上限为 500 元。"},
		{Index: 3, ChunkID: "chunk-c", DocumentID: "doc-2", EngineDataset: "engine-1", SourceContent: "交通费按实际发生报销。"},
	}
}

// listCandidates reads one section's frozen pool. `sectionOrd` is a required part of
// the key, so passing a sentinel would return an empty list and quietly pass every
// "nothing was frozen" assertion below.
func listCandidates(t *testing.T, fx *reportFixture, revisionID string, sectionOrd int) []model.ReportConflictCandidate {
	t.Helper()
	rows, err := fx.svc.Store.ListConflictCandidates(context.Background(), fx.tenantID, revisionID, sectionOrd)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	return rows
}

// TestFreezeCandidatesIsOffByDefault: `scan_top_k = 0` stores no candidate at all.
// This is the privacy default, so it is asserted rather than assumed.
func TestFreezeCandidatesIsOffByDefault(t *testing.T) {
	fx := newReportFixture(t, "freeze-off")
	run := candidateRun(fx, "rev-freeze-off", "report-freeze-off", 0)
	section := &model.ReportSection{ID: "sec-freeze-off", Ord: 1}

	run.freezeCandidates(section, candidateEvidence(), map[int]bool{1: true}, time.Now().UTC())

	if rows := listCandidates(t, fx, "rev-freeze-off", 1); len(rows) != 0 {
		t.Fatalf("scan_top_k=0 froze %d candidates, want 0", len(rows))
	}
}

// TestFreezeCandidatesSkipsCitedAndKeepsRetrievalOrder: the pool is the *uncited*
// remainder, and `pool_rank` is the retrieval index rather than a re-numbering — a
// contradiction that ranked 2nd is a stronger signal than one ranked 40th.
func TestFreezeCandidatesSkipsCitedAndKeepsRetrievalOrder(t *testing.T) {
	fx := newReportFixture(t, "freeze-order")
	run := candidateRun(fx, "rev-freeze-order", "report-freeze-order", 5)
	section := &model.ReportSection{ID: "sec-freeze-order", Ord: 2}
	now := time.Now().UTC()

	// Index 2 is cited, so it must be absent; 1 and 3 keep their own ranks.
	run.freezeCandidates(section, candidateEvidence(), map[int]bool{2: true}, now)

	rows := listCandidates(t, fx, "rev-freeze-order", 2)
	if len(rows) != 2 {
		t.Fatalf("froze %d candidates, want 2 (the cited chunk must be excluded)", len(rows))
	}
	byChunk := map[string]model.ReportConflictCandidate{}
	for _, row := range rows {
		byChunk[row.ChunkID] = row
	}
	if _, ok := byChunk["chunk-b"]; ok {
		t.Fatal("cited chunk chunk-b was frozen as a candidate")
	}
	for chunkID, wantRank := range map[string]int{"chunk-a": 1, "chunk-c": 3} {
		row, ok := byChunk[chunkID]
		if !ok {
			t.Fatalf("%s missing from the frozen pool", chunkID)
		}
		if row.PoolRank != wantRank {
			t.Fatalf("%s: pool_rank = %d, want %d (retrieval order, never renumbered)", chunkID, row.PoolRank, wantRank)
		}
		if row.SectionOrd != 2 || row.SectionID != "sec-freeze-order" {
			t.Fatalf("%s: wrong section locator: ord=%d id=%s", chunkID, row.SectionOrd, row.SectionID)
		}
		if row.DatasetID != "dataset-local" {
			t.Fatalf("%s: dataset id = %q, want the local id (engine ids must not leak)", chunkID, row.DatasetID)
		}
		if row.ACLScopeJSON == "" {
			t.Fatalf("%s: candidate written without an ACL scope", chunkID)
		}
	}
}

// TestFreezeCandidatesCapsAtScanTopK: the width is the frozen snapshot's, so the
// first `scan_top_k` uncited chunks are kept and the rest are not copied.
func TestFreezeCandidatesCapsAtScanTopK(t *testing.T) {
	fx := newReportFixture(t, "freeze-cap")
	run := candidateRun(fx, "rev-freeze-cap", "report-freeze-cap", 1)

	run.freezeCandidates(&model.ReportSection{ID: "sec-freeze-cap", Ord: 1}, candidateEvidence(), nil, time.Now().UTC())

	rows := listCandidates(t, fx, "rev-freeze-cap", 1)
	if len(rows) != 1 || rows[0].ChunkID != "chunk-a" {
		t.Fatalf("scan_top_k=1 froze %d rows (%v), want only chunk-a", len(rows), rows)
	}
}

// TestFreezeCandidatesIsIdempotentPerSection: a retried section rewrites the same
// rows. Ids derive from `(section, chunk)`, so a second attempt cannot accumulate a
// duplicate pool — which would double the workbench's candidate count and make
// "how much of the corpus did we copy" unanswerable.
func TestFreezeCandidatesIsIdempotentPerSection(t *testing.T) {
	fx := newReportFixture(t, "freeze-retry")
	run := candidateRun(fx, "rev-freeze-retry", "report-freeze-retry", 5)
	section := &model.ReportSection{ID: "sec-freeze-retry", Ord: 1}

	run.freezeCandidates(section, candidateEvidence(), nil, time.Now().UTC())
	first := listCandidates(t, fx, "rev-freeze-retry", 1)
	run.freezeCandidates(section, candidateEvidence(), nil, time.Now().UTC())
	second := listCandidates(t, fx, "rev-freeze-retry", 1)

	if len(first) != len(candidateEvidence()) {
		t.Fatalf("first attempt froze %d candidates, want %d", len(first), len(candidateEvidence()))
	}
	if len(second) != len(first) {
		t.Fatalf("retried section grew the pool from %d to %d rows", len(first), len(second))
	}
	for index := range first {
		if first[index].ID != second[index].ID {
			t.Fatalf("retried section minted a new id: %s -> %s", first[index].ID, second[index].ID)
		}
	}
}

// TestFreezeCandidatesClockNeverOutlivesTheReport: `expires_at` is
// `min(evidence_ttl, report.expires_at)`. The report clock here is much shorter, so a
// candidate that trusted only the evidence TTL would fail this.
func TestFreezeCandidatesClockNeverOutlivesTheReport(t *testing.T) {
	fx := newReportFixture(t, "freeze-clock")
	run := candidateRun(fx, "rev-freeze-clock", "report-freeze-clock", 5)
	reportClock := time.Now().UTC().Add(2 * time.Hour)
	run.report.ExpiresAt = reportClock
	now := time.Now().UTC()

	run.freezeCandidates(&model.ReportSection{ID: "sec-freeze-clock", Ord: 1}, candidateEvidence(), nil, now)

	rows := listCandidates(t, fx, "rev-freeze-clock", 1)
	if len(rows) == 0 {
		t.Fatal("no candidates frozen")
	}
	for _, row := range rows {
		if row.ExpiresAt == nil {
			t.Fatalf("%s: candidate has no expiry; it would outlive the report", row.ChunkID)
		}
		if row.ExpiresAt.After(reportClock) {
			t.Fatalf("%s: expiry %s is after the report clock %s", row.ChunkID, row.ExpiresAt, reportClock)
		}
		if row.ExpiresAt.Before(now) {
			t.Fatalf("%s: expiry %s is already in the past", row.ChunkID, row.ExpiresAt)
		}
	}
}

// TestReportEarliestExpiry: a zero second clock means "the report has none", and the
// first one stands; inventing "now" would purge a candidate the report still needs.
func TestReportEarliestExpiry(t *testing.T) {
	early := time.Now().UTC().Add(time.Hour)
	late := early.Add(24 * time.Hour)
	cases := []struct {
		name          string
		first, second time.Time
		want          time.Time
	}{
		{"second earlier wins", late, early, early},
		{"first earlier wins", early, late, early},
		{"zero second keeps first", early, time.Time{}, early},
		{"zero first keeps second", time.Time{}, late, late},
	}
	for _, tc := range cases {
		if got := reportEarliestExpiry(tc.first, tc.second); !got.Equal(tc.want) {
			t.Fatalf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

// TestReportFrozenScanTopKReadsOnlyTheSnapshot: the review derives its contrast scope
// from the revision's frozen value. A revision without a snapshot (every report written
// before this release) reads as 0 → `cited_only`, rather than inheriting today's
// tenant setting and retroactively rewriting what the report was reviewed against.
func TestReportFrozenScanTopKReadsOnlyTheSnapshot(t *testing.T) {
	fx := newReportFixture(t, "frozen-scope")
	ctx := context.Background()

	if got := reportFrozenScanTopK(ctx, fx.svc, nil); got != 0 {
		t.Fatalf("nil revision: got %d, want 0", got)
	}
	if got := reportFrozenScanTopK(ctx, fx.svc, &model.ReportRevision{ID: "rev-none"}); got != 0 {
		t.Fatalf("no snapshot: got %d, want 0", got)
	}
	if got := reportFrozenScanTopK(ctx, fx.svc, &model.ReportRevision{
		ID: "rev-garbage", ExecutionSnapshotJSON: "{not json",
	}); got != 0 {
		t.Fatalf("unparseable snapshot: got %d, want 0", got)
	}
	frozen := &model.ReportRevision{
		ID: "rev-frozen", ExecutionSnapshotJSON: `{"retrieval":{"scan_top_k":7}}`,
	}
	if got := reportFrozenScanTopK(ctx, fx.svc, frozen); got != 7 {
		t.Fatalf("frozen snapshot: got %d, want 7", got)
	}
}

// TestReportCandidateScanTopKChain pins the resolution order: tenant setting, then
// template, then the clamp. The review path never calls it (it reads the frozen value),
// which is exactly why its own behaviour needs a test.
func TestReportCandidateScanTopKChain(t *testing.T) {
	fx := newReportFixture(t, "scan-chain")
	ctx := context.Background()

	// With no tenant override, the template's value is inherited.
	if got, source := fx.svc.reportCandidateScanTopK(ctx, 5); got != 5 || source != "template" {
		t.Fatalf("template-only: got (%d,%q), want (5,\"template\")", got, source)
	}
	// A template value above the clamp is clamped, and the source still names where the
	// number came from rather than pretending the clamp was the setting.
	if got, _ := fx.svc.reportCandidateScanTopK(ctx, 99); got != reportAssuranceMaxScanTopK {
		t.Fatalf("clamp: got %d, want %d", got, reportAssuranceMaxScanTopK)
	}
	// Zero everywhere freezes nothing. The source is then the tenant setting, which is
	// the honest answer: nobody asked for contrast, so no template may be named as the
	// reason there is none.
	if got, source := fx.svc.reportCandidateScanTopK(ctx, 0); got != 0 || source != "tenant" {
		t.Fatalf("no width: got (%d,%q), want (0,\"tenant\")", got, source)
	}
}
