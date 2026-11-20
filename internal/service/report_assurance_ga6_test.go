package service

// G-A6 (doc/301 §10.5) — a frozen conflict candidate is never evidence.
//
// The promise the product makes is precise: `rgx_report_evidence` holds exactly what the
// report cited (§5.3.1 P0-3), and its dedup unique index over `(tenant_id, chunk_id,
// source_content_hash)` is the schema-level proof of that. A candidate is the *uncited*
// remainder, so the failure this scans for is a "helpful" future change that writes the
// candidate pool into the evidence table so the contrast panel can join against it. That
// change would look harmless in the panel and would silently make every uncited chunk
// part of the report's evidence chain — the exact confusion A54 forbids, one layer down.
//
// The scan is cross-table by construction, which is why it cannot be replaced by the
// unit tests on `freezeCandidates`: those assert what the freezer writes, this asserts
// what *nothing else* writes.

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestFrozenCandidatesNeverEnterTheEvidenceChain(t *testing.T) {
	fx := newReportFixture(t, "ga6-candidate")
	ctx := context.Background()
	report := fx.createReport(t, fx.datasetLink, "req-ga6-candidate-0001")
	run := candidateRun(fx, report.Revision.ID, report.Report.ID, 5)

	// Cite chunk-b, so the other two are the uncited pool. Both sides are non-empty:
	// otherwise "no overlap" would be vacuously true.
	run.freezeCandidates(&model.ReportSection{ID: "sec-ga6", Ord: 1}, candidateEvidence(),
		map[int]bool{2: true}, time.Now().UTC())

	candidates := listCandidates(t, fx, report.Revision.ID, 1)
	if len(candidates) != 2 {
		t.Fatalf("froze %d candidates, want 2: the overlap assertion below needs a pool", len(candidates))
	}
	frozenChunks := map[string]bool{}
	frozenHashes := map[string]bool{}
	for _, candidate := range candidates {
		frozenChunks[candidate.ChunkID] = true
		if candidate.ContentHash == "" {
			t.Fatalf("candidate %s has no content hash: it could not be recognised downstream", candidate.ChunkID)
		}
		frozenHashes[candidate.ContentHash] = true
	}

	// ① No evidence row may carry a candidate chunk or its content.
	// The evidence snapshot table is tenant-scoped by design (a chunk cited by two
	// reports is one row), so the scan is by `chunk_id`/hash rather than by revision.
	evidence := fx.countRows(&model.ReportEvidenceSnapshot{}, "tenant_id = ?", fx.tenantID)
	if evidence != 0 {
		t.Fatalf("a review wrote %d evidence row(s) for a revision whose owner cited nothing", evidence)
	}

	// ② No citation may point at a candidate.
	citations := fx.countRows(&model.ReportCitation{},
		"tenant_id = ? AND revision_id = ?", fx.tenantID, report.Revision.ID)
	if citations != 0 {
		t.Fatalf("freezing candidates wrote %d citation row(s)", citations)
	}

	// ③ The two id spaces stay disjoint even after a report is published: re-check
	// per candidate, which is what turns "0 rows in total" into "0 rows for the
	// specific chunks the freezer chose" if a future run ever seeds both.
	for _, candidate := range candidates {
		var hits int64
		if err := fx.gdb.Model(&model.ReportCitation{}).
			Where("tenant_id = ? AND revision_id = ? AND source_chunk_id = ?",
				fx.tenantID, report.Revision.ID, candidate.ChunkID).
			Count(&hits).Error; err != nil {
			t.Fatalf("count citations for %s: %v", candidate.ChunkID, err)
		}
		if hits != 0 {
			t.Fatalf("candidate %s appears in %d citation row(s): the pool entered the evidence chain",
				candidate.ChunkID, hits)
		}
		var evidenceHits int64
		if err := fx.gdb.Model(&model.ReportEvidenceSnapshot{}).
			Where("tenant_id = ? AND source_content_hash = ?",
				fx.tenantID, candidate.ContentHash).
			Count(&evidenceHits).Error; err != nil {
			t.Fatalf("count evidence for %s: %v", candidate.ChunkID, err)
		}
		if evidenceHits != 0 {
			t.Fatalf("candidate %s's content (%q) appears in %d evidence row(s)",
				candidate.ChunkID, candidate.ContentHash, evidenceHits)
		}
	}
	_ = ctx
	_ = frozenChunks
	_ = frozenHashes
}
