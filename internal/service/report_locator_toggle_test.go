package service

// doc/301 T10 ④ — A54 as an *end-to-end* assertion over the toggle itself.
//
// `TestReportLocatorsNeverEnterEvidence` proves "a projection present vs absent" does
// not move the citation chain. The setting is a different switch with the same promise:
// turning `report.locator_hints_enabled` off must change only what the writer was
// allowed to *look at*, never a single stored citation or `evidence_status`. So this
// test runs the same request twice against the same dataset — hints on, hints off — and
// compares the rows rather than reading any prompt.
//
// A row-level diff is used deliberately: an assertion on counts or on the rendered text
// would pass while a chunk id, an ordinal or a snapshot pointer drifted.

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// setLocatorHintsEnabled flips the KC-1 switch, advancing the settings revision so the
// read path actually sees it.
func setLocatorHintsEnabled(t *testing.T, fx *reportFixture, enabled bool) {
	t.Helper()
	ctx := context.Background()
	current, err := fx.svc.Store.GetCurrentSettingRevision(ctx)
	if err != nil {
		t.Fatalf("current settings revision: %v", err)
	}
	expected := int64(0)
	if current != nil {
		expected = current.Revision
	}
	if _, err := fx.svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: expected,
		Changes: map[string]interface{}{
			"report.locator_hints_enabled": enabled,
		},
		Note:      "A54 toggle regression",
		Confirmed: true,
	}); err != nil {
		t.Fatalf("set locator_hints_enabled=%v: %v", enabled, err)
	}
}

// citationFingerprint is every stored column that describes *what was cited*, with the
// per-row identifiers removed: two runs of one request must agree on all of it.
type citationFingerprint struct {
	SectionOrd        int
	Ord               int
	ClaimID           string
	SnapshotID        string
	DatasetID         string
	DocumentID        string
	DocumentLabel     string
	ChunkID           string
	SourceContentHash string
	PermissionScope   string
	ReferenceWindow   string
	Score             float64
	ScoreType         string
	Snippet           string
}

func fingerprintCitations(rows []model.ReportCitation) []citationFingerprint {
	out := make([]citationFingerprint, 0, len(rows))
	for _, row := range rows {
		out = append(out, citationFingerprint{
			SectionOrd: row.SectionOrd, Ord: row.Ord, ClaimID: row.ClaimID,
			SnapshotID: row.EvidenceSnapshotID, DatasetID: row.SourceDatasetID,
			DocumentID: row.SourceDocumentID, DocumentLabel: row.DocumentLabel,
			ChunkID: row.SourceChunkID, SourceContentHash: row.SourceContentHash,
			PermissionScope: row.SourcePermissionScopeJSON,
			ReferenceWindow: row.ReferenceWindowJSON,
			Score:           row.RetrievalScore, ScoreType: row.RetrievalScoreType,
			Snippet: row.Snippet,
		})
	}
	return out
}

// TestReportLocatorToggleDoesNotMoveTheEvidenceChain is A54 at the toggle:
// `report.locator_hints_enabled=false` and `=true` must produce byte-identical citation
// rows and identical per-section `evidence_status`.
func TestReportLocatorToggleDoesNotMoveTheEvidenceChain(t *testing.T) {
	fx, engine := newReportRunFixture(t, "a54-toggle")
	ctx := context.Background()
	datasetID := "link-policy-a54-toggle"

	// The projection is seeded once, before either run, so the only variable between
	// them is the switch.
	seedReportLocatorArtifact(t, fx, datasetID, "artifact-a54-toggle", "制度目录：差旅与报销")

	setLocatorHintsEnabled(t, fx, false)
	offRun, offSections, offCitations, _ := runReportToSections(t, fx, engine, datasetID, "req-a54-off", "")
	for _, section := range offSections {
		if section.LocationHintsJSON != "" && section.LocationHintsJSON != "[]" {
			t.Fatalf("hints are off, yet section %d froze %q",
				section.Ord, section.LocationHintsJSON)
		}
	}

	setLocatorHintsEnabled(t, fx, true)
	onRun, onSections, onCitations, _ := runReportToSections(t, fx, engine, datasetID, "req-a54-on", "")
	hinted := 0
	for _, section := range onSections {
		if section.LocationHintsJSON != "" && section.LocationHintsJSON != "[]" {
			hinted++
		}
	}
	if hinted == 0 {
		t.Fatal("hints are on with a ready projection, yet no section froze a hint: the test would prove nothing")
	}

	offFingerprint := fingerprintCitations(offCitations)
	onFingerprint := fingerprintCitations(onCitations)
	if len(offFingerprint) != len(onFingerprint) {
		t.Fatalf("citation count changed with the toggle: %d off vs %d on",
			len(offFingerprint), len(onFingerprint))
	}
	for index := range offFingerprint {
		if offFingerprint[index] != onFingerprint[index] {
			t.Fatalf("citation row %d changed with the toggle:\n off=%+v\n on =%+v",
				index, offFingerprint[index], onFingerprint[index])
		}
	}

	offView, err := fx.svc.ListReportSections(ctx, fx.owner.ID, fx.tenantID, offRun.Report.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	onView, err := fx.svc.ListReportSections(ctx, fx.owner.ID, fx.tenantID, onRun.Report.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(offView) != len(onView) {
		t.Fatalf("section count changed with the toggle: %d vs %d", len(offView), len(onView))
	}
	for index := range offView {
		if offView[index].EvidenceStatus != onView[index].EvidenceStatus {
			t.Fatalf("evidence_status of section %d changed with the toggle: %q → %q",
				offView[index].Ord, offView[index].EvidenceStatus, onView[index].EvidenceStatus)
		}
		if offView[index].CitationCount != onView[index].CitationCount {
			t.Fatalf("citation_count of section %d changed with the toggle: %d → %d",
				offView[index].Ord, offView[index].CitationCount, onView[index].CitationCount)
		}
	}
}
