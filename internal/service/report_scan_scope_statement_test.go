package service

// doc/301 §5.3.1 / T08+T10 — the delivered file must state `scan_scope`.
//
// The panel proves the surface; this proves the *data* the surface and the five
// renderers read, from real rows rather than a hand-built struct:
//
//   - a revision whose frozen snapshot says `scan_top_k = 0` reports `cited_only`
//     and says so in a sentence about *not comparing*, not about finding nothing;
//   - a revision that froze a pool reports `cited+candidate:K` with the count of rows
//     it still has, so the sentence a reader sees is derivable from what was stored.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestRevisionScanScopeStatementMatchesStoredPool(t *testing.T) {
	fx := newReportFixture(t, "scan-scope-statement")
	ctx := context.Background()

	// 1) Nothing frozen: the honest answer is "we did not compare".
	narrow := &model.ReportRevision{
		ID: "rev-scope-narrow", TenantID: fx.tenantID,
		ExecutionSnapshotJSON: `{"retrieval":{"scan_top_k":0}}`,
	}
	scope, count := fx.svc.revisionScanScope(ctx, fx.tenantID, narrow)
	if scope != "cited_only" || count != 0 {
		t.Fatalf("no pool: got (%q,%d), want (cited_only,0)", scope, count)
	}
	line := renderScanScope(scope, count)
	if !strings.Contains(line, "未做跨文档冲突对照") {
		t.Fatalf("cited_only must say no contrast happened, got %q", line)
	}
	if strings.Contains(line, "未发现冲突") {
		t.Fatalf("cited_only must not read as 'compared and found nothing': %q", line)
	}

	// 2) A frozen pool: the width comes from the snapshot, the count from the rows.
	width := 5
	run := candidateRun(fx, "rev-scope-wide", "report-scope-wide", width)
	section := &model.ReportSection{ID: "sec-scope-wide", Ord: 1}
	run.freezeCandidates(section, candidateEvidence(), map[int]bool{2: true}, time.Now().UTC())

	wide := &model.ReportRevision{
		ID: "rev-scope-wide", TenantID: fx.tenantID,
		ExecutionSnapshotJSON: `{"retrieval":{"scan_top_k":5}}`,
	}
	scope, count = fx.svc.revisionScanScope(ctx, fx.tenantID, wide)
	if scope != "cited+candidate:5" {
		t.Fatalf("scope = %q, want cited+candidate:5 (the frozen width, never the tenant's current one)", scope)
	}
	if count != 2 {
		t.Fatalf("count = %d, want the 2 uncited rows actually frozen", count)
	}
	line = renderScanScope(scope, count)
	if !strings.Contains(line, "未引用候选 2 条") {
		t.Fatalf("the count in the sentence must be the stored rows, got %q", line)
	}
	if !strings.Contains(line, "候选不入证据链") {
		t.Fatalf("the sentence must deny candidacy the status of evidence, got %q", line)
	}

	// 3) A revision that predates the field prints nothing at all.
	if line := renderScanScope("", 0); line != "" {
		t.Fatalf("legacy revision: got %q, want silence", line)
	}
}
