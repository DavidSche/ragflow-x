package service

// G-A3 (doc/301 §10.5) — a published revision is immutable, including against the
// pipeline that produced it.
//
// The gate's sentence is "发布后重跑不改行", and the danger is not a hypothetical
// second job: the reconciler, a retried slice and a manually replayed finalize all
// reach the same code, and every one of them runs *after* the revision has been
// delivered to someone. A revision whose `content_hash` or `metrics_json` can move
// after publication invalidates every export, every acknowledgement and every audit
// row that pointed at it — silently, because the ids stay the same.
//
// The assertion therefore snapshots the delivered row, replays the terminal write, and
// compares the columns a reader or an export could observe. A `false` from
// `BeginReportFinalization` is part of the contract (the guard did not match), but it is
// not sufficient on its own: the test must also prove nothing was written.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// deliveredRevisionFingerprint is the set of columns an export, a review or a metrics
// rollup can observe. Ids are excluded on purpose: they are the fields that stay equal
// while the content changes, so keeping them would let the test pass on a rewrite.
func deliveredRevisionFingerprint(t *testing.T, revisionID string, fx *reportFixture) map[string]interface{} {
	t.Helper()
	revision := fx.reportRevision(t, revisionID)
	if revision.Status != "published" && revision.Status != "superseded" {
		t.Fatalf("fingerprint requested on a %s revision: the test would compare nothing", revision.Status)
	}
	fingerprint := map[string]interface{}{
		// `content_hash` on a revision is its frozen execution snapshot hash: the
		// document itself is the section rows, which the loop below compares.
		"status":                  revision.Status,
		"completeness":            revision.Completeness,
		"metrics_json":            revision.MetricsJSON,
		"gap_json":                revision.GapJSON,
		"execution_snapshot_hash": revision.ExecutionSnapshotHash,
		"assurance_status":        revision.AssuranceStatus,
		"assurance_summary_json":  revision.AssuranceSummaryJSON,
	}
	if revision.PublishedAt != nil {
		fingerprint["published_at"] = revision.PublishedAt.UTC().Format(time.RFC3339Nano)
	}
	return fingerprint
}

func TestAPublishedRevisionSurvivesAReplayedFinalize(t *testing.T) {
	fx := newReportFixture(t, "ga3-immutable")
	ctx := context.Background()
	// The job id is needed to replay the terminal write, so the publish path is
	// inlined rather than called: `publishReport` returns the revision only.
	created := fx.createReport(t, fx.datasetLink, "req-ga3-immutable")
	fx.seedSections(t, created.Revision.ID, successSeeds())
	runtimeID := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, runtimeID)
	if _, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, runtimeID); err != nil {
		t.Fatalf("begin finalization: %v", err)
	}
	if _, err := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, runtimeID, time.Now().UTC()); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	revision := fx.reportRevision(t, created.Revision.ID)
	if revision.Status != model.ReportRevisionPublished {
		t.Fatalf("revision must be published before the replay: %s", revision.Status)
	}

	before := deliveredRevisionFingerprint(t, revision.ID, fx)
	sectionsBefore := fx.reportSections(t, revision.ID)
	sectionHashes := map[int]string{}
	for _, section := range sectionsBefore {
		sectionHashes[section.Ord] = section.ContentHash
	}

	// Replay the *same* terminal write on the same job. The guard is expected to
	// refuse (`applied=false`); the revision must be untouched even if a future
	// implementation lets the call through, so both halves are asserted below.
	replayRuntime := fx.reportRuntimeJob(t, model.JobStatusSucceeded)
	fx.claimReportJob(t, created.Job.ID, replayRuntime)
	applied, err := fx.svc.BeginReportFinalization(ctx, fx.tenantID, created.Job.ID, 1, 1, replayRuntime)
	if err == nil && applied {
		if _, finalizeErr := fx.svc.FinalizeReportJob(ctx, fx.tenantID, created.Job.ID, replayRuntime, time.Now().UTC()); finalizeErr != nil {
			t.Logf("replayed finalize reported %v; the revision is compared either way", finalizeErr)
		}
	}
	if applied {
		t.Log("the replayed finalize was applied; the immutability assertion below is the one that matters")
	}

	after := deliveredRevisionFingerprint(t, revision.ID, fx)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a published revision changed:\n before=%v\n after =%v", before, after)
	}
	for _, section := range fx.reportSections(t, revision.ID) {
		if sectionHashes[section.Ord] != section.ContentHash {
			t.Fatalf("section %d content changed after publication: %q → %q",
				section.Ord, sectionHashes[section.Ord], section.ContentHash)
		}
	}
}
