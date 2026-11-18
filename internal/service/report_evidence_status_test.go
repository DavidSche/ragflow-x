package service

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Section evidence disclosure (doc/288 §9.3 / A3a–A3c).
//
// The whole point of these three assertions is that the report tells the reader
// *what it did not have*, in the same place it shows what it wrote. A section that
// needed evidence and cited nothing must still exist (deleting it would turn an
// honest gap into a silent one), and the `[n]` markers the reader sees must be the
// citations the appendix lists — one number, one meaning.

// TestReportEvidenceStatusNeverClaimsNothingWasRequiredWhenItCited (A3a):
// `not_required` is a statement about the *template*, so a section that requires
// evidence may never be labelled with it — not when it cited, not when it did not.
func TestReportEvidenceStatusNeverClaimsNothingWasRequiredWhenItCited(t *testing.T) {
	cases := []struct {
		name             string
		requiresEvidence bool
		retrieved        int
		cited            int
		minEvidence      int
		want             string
	}{
		// Not required: nothing to disclose, and a citation does not invent a
		// requirement the template never made.
		{"no evidence needed, cited nothing", false, 0, 0, 0, model.ReportEvidenceNotRequired},
		{"no evidence needed, still cited", false, 5, 2, 0, model.ReportEvidenceSupported},
		// Required but nothing came back.
		{"required, retrieval empty", true, 0, 0, 1, model.ReportEvidenceUnsupported},
		// Required, evidence came back, the section ignored it.
		{"required, retrieved but uncited", true, 4, 0, 1, model.ReportEvidenceUnsupported},
		// Required, cited less than the template's standard.
		{"required, below min_evidence", true, 4, 2, 3, model.ReportEvidencePartiallySupported},
		// Required, met the standard.
		{"required, meets min_evidence", true, 4, 3, 3, model.ReportEvidenceSupported},
		// Required, template set no standard at all.
		{"required, no min_evidence", true, 4, 1, 0, model.ReportEvidenceSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reportEvidenceStatus(tc.requiresEvidence, tc.retrieved, tc.cited, tc.minEvidence)
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
			if tc.requiresEvidence && got == model.ReportEvidenceNotRequired {
				t.Fatalf("a section that requires evidence may never be %q", got)
			}
		})
	}
	// The invariant over the whole outcome space, so a future branch cannot quietly
	// answer `not_required` for a required section.
	for _, requiresEvidence := range []bool{true, false} {
		for retrieved := 0; retrieved <= 4; retrieved++ {
			for cited := 0; cited <= retrieved; cited++ {
				for _, minEvidence := range []int{0, 1, 3} {
					got := reportEvidenceStatus(requiresEvidence, retrieved, cited, minEvidence)
					if requiresEvidence && got == model.ReportEvidenceNotRequired {
						t.Fatalf("required section (%d retrieved, %d cited, min %d) answered %q",
							retrieved, cited, minEvidence, got)
					}
					if !requiresEvidence && got != model.ReportEvidenceNotRequired &&
						got != model.ReportEvidenceSupported {
						t.Fatalf("a section that needs no evidence answered %q", got)
					}
				}
			}
		}
	}
}

// TestEveryVisibleMarkerHasItsOwnCitationRow (A3c): the numbers a reader sees in
// the body must be exactly the citation rows the appendix can print, for every
// section — consistency 1.0, not "usually".
func TestEveryVisibleMarkerHasItsOwnCitationRow(t *testing.T) {
	fx, engine := newReportRunFixture(t, "citation-markers")
	ctx := context.Background()
	created := fx.outlineJob(t, "link-policy-citation-markers", "req-markers-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}

	// One answer per section, in outline order, each citing a distinct set of
	// evidence indexes so a mixed-up mapping cannot pass by symmetry.
	engine.mu.Lock()
	engine.replies = []string{
		`{"answer":"本次评估覆盖差旅报销流程。","usage":{"prompt_tokens":40,"completion_tokens":10}}`,
		`{"answer":"适用制度为差旅制度第三章。[2]","usage":{"prompt_tokens":50,"completion_tokens":12}}`,
		`{"answer":"逐条判定：住宿标准符合。[1][3]","usage":{"prompt_tokens":60,"completion_tokens":20}}`,
		`{"answer":"差距：超标审批留痕缺失。[3]","usage":{"prompt_tokens":70,"completion_tokens":18}}`,
		`{"answer":"整改：补齐审批留痕。[1]","usage":{"prompt_tokens":80,"completion_tokens":16}}`,
	}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	markerPattern := regexp.MustCompile(`\[(\d+)\]`)
	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) == 0 {
		t.Fatal("no sections were written")
	}
	citedSections := 0
	for _, section := range sections {
		if section.Status != model.ReportSectionSucceeded {
			t.Fatalf("section %s must be succeeded, got %q", section.SectionKey, section.Status)
		}
		var rows []model.ReportCitation
		if err := fx.gdb.Where("tenant_id = ? AND section_id = ?", fx.tenantID, section.ID).
			Order("ord").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}

		// Markers in the body → the citation rows for this section.
		markers := make(map[int]bool)
		for _, match := range markerPattern.FindAllStringSubmatch(section.ContentMD, -1) {
			index, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatalf("section %s: bad marker %q", section.SectionKey, match[0])
			}
			markers[index] = true
		}
		rowOrds := make(map[int]bool, len(rows))
		for _, row := range rows {
			if !row.UsedInText {
				t.Fatalf("section %s ord %d carries a citation row the body never used",
					section.SectionKey, row.Ord)
			}
			if row.EvidenceSnapshotID == "" {
				t.Fatalf("section %s ord %d has no evidence snapshot to point at",
					section.SectionKey, row.Ord)
			}
			if row.SectionID != section.ID {
				t.Fatalf("citation %q belongs to %q, not %q", row.ID, row.SectionID, section.ID)
			}
			if rowOrds[row.Ord] {
				t.Fatalf("section %s wrote two citation rows at ord %d", section.SectionKey, row.Ord)
			}
			rowOrds[row.Ord] = true
		}
		if len(markers) != len(rowOrds) {
			t.Fatalf("section %s cites %v in text but stores %v", section.SectionKey, keysOfIntSet(markers), keysOfIntSet(rowOrds))
		}
		for index := range markers {
			if !rowOrds[index] {
				t.Fatalf("section %s shows [%d] with no citation row (%v stored)",
					section.SectionKey, index, keysOfIntSet(rowOrds))
			}
		}
		if section.CitationCount != len(rows) {
			t.Fatalf("section %s says %d citations, stored %d", section.SectionKey, section.CitationCount, len(rows))
		}
		if len(rows) > 0 {
			citedSections++
		}

		// A3a at the row level: the disclosure must follow the requirement, and a
		// section that cited must never claim the template asked for nothing.
		if section.RequiresEvidence && len(rows) > 0 && section.EvidenceStatus == model.ReportEvidenceNotRequired {
			t.Fatalf("section %s cited %d chunks yet answered %q",
				section.SectionKey, len(rows), section.EvidenceStatus)
		}
		if section.RequiresEvidence && len(rows) > 0 && section.EvidenceStatus == model.ReportEvidenceUnsupported {
			t.Fatalf("section %s cited %d chunks yet answered %q",
				section.SectionKey, len(rows), section.EvidenceStatus)
		}
	}
	if citedSections == 0 {
		t.Fatal("the fixture cited nothing, so the consistency assertion proved nothing")
	}
}

func keysOfIntSet(set map[int]bool) []int {
	keys := make([]int, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

// TestFabricatedCitationMarkersAreNamedAndRemovedPrecisely pins the two halves of
// the A3c repair: which numbers count as citations at all, and what removing one
// does to the sentence around it.
func TestFabricatedCitationMarkersAreNamedAndRemovedPrecisely(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		evidence int
		want     []int
	}{
		{"marker beyond the evidence list", "住宿标准符合。[3]", 2, []int{3}},
		{"two-digit marker", "留痕缺失。[12][2]", 8, []int{12}},
		{"all markers resolvable", "符合。[1][2]", 2, nil},
		{"a year is not a citation", "（2024 修订）住宿标准符合。", 2, nil},
		{"zero is not a marker", "第[0]章", 2, nil},
		{"duplicates collapse", "[3] 与 [3]", 2, []int{3}},
		{"multiple sorted", "[9][4]", 2, []int{4, 9}},
		{"no evidence at all", "自述结论。[1]", 0, []int{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reportDanglingCitationMarkers(tc.content, tc.evidence)
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			for index := range got {
				if got[index] != tc.want[index] {
					t.Fatalf("want %v, got %v", tc.want, got)
				}
			}
		})
	}

	// Only the named markers go: a real citation in the same sentence must survive,
	// and the sentence must still read as prose afterwards.
	stripped := reportStripCitationMarkers("住宿标准符合。[1][9] 超标需审批。", []int{9})
	if strings.Contains(stripped, "[9]") {
		t.Fatalf("the fabricated marker survived: %q", stripped)
	}
	if !strings.Contains(stripped, "[1]") {
		t.Fatalf("the real citation was removed too: %q", stripped)
	}
	if strings.Contains(stripped, "  ") {
		t.Fatalf("stripping left a double space: %q", stripped)
	}
}

// TestFabricatedCitationIsRemovedAndTheSectionIsFlagged (A3c): a model that cites a
// number it was never given has produced a fake reference. The delivered body must
// not show it, the appendix must not need a row for it, and the section must say it
// needs review — otherwise the rewrite is invisible.
func TestFabricatedCitationIsRemovedAndTheSectionIsFlagged(t *testing.T) {
	fx, engine := newReportRunFixture(t, "dangling-citations")
	ctx := context.Background()
	created := fx.outlineJob(t, fx.datasetLink, "req-dangling-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(ctx, fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}

	// The engine returns two chunks per section, so `[9]` cannot resolve and `[1]`
	// can.
	engine.mu.Lock()
	engine.replies = []string{
		`{"answer":"本次评估覆盖差旅报销流程。","usage":{"prompt_tokens":40,"completion_tokens":10}}`,
		`{"answer":"适用制度为差旅制度第三章。[9]","usage":{"prompt_tokens":50,"completion_tokens":12}}`,
		`{"answer":"住宿标准符合。[1][9]","usage":{"prompt_tokens":60,"completion_tokens":20}}`,
		`{"answer":"差距：超标审批留痕缺失。[2]","usage":{"prompt_tokens":70,"completion_tokens":18}}`,
		`{"answer":"整改：补齐审批留痕。[1]","usage":{"prompt_tokens":80,"completion_tokens":16}}`,
	}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	flagged := 0
	for _, section := range fx.reportSections(t, created.Revision.ID) {
		if strings.Contains(section.ContentMD, "[9]") {
			t.Fatalf("section %s still shows a citation nobody can resolve: %q",
				section.SectionKey, section.ContentMD)
		}
		var rows int64
		if err := fx.gdb.Model(&model.ReportCitation{}).
			Where("tenant_id = ? AND section_id = ?", fx.tenantID, section.ID).Count(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if int(rows) != section.CitationCount {
			t.Fatalf("section %s says %d citations, stored %d", section.SectionKey, section.CitationCount, rows)
		}
		if section.QualityStatus == model.ReportQualityNeedsReview {
			flagged++
		}
		// A section whose whole answer was the fabricated marker has nothing left to
		// deliver, and must not be published as an empty-but-succeeded body.
		if section.Status == model.ReportSectionSucceeded && strings.TrimSpace(section.ContentMD) == "" {
			t.Fatalf("section %s succeeded with an empty body", section.SectionKey)
		}
	}
	if flagged == 0 {
		t.Fatal("a section that cited a fabricated source must be flagged needs_review")
	}

	// The removal is disclosed, not silent: the section event names the numbers that
	// were dropped.
	events := fx.reportEventsOf(t, created.Job.ID, reportEventSectionSucceeded)
	disclosed := false
	for _, event := range events {
		if strings.Contains(event.PayloadJSON, `"dangling_citations":[9]`) {
			disclosed = true
		}
	}
	if !disclosed {
		t.Fatalf("no section event disclosed the dropped citations: %+v", events)
	}
}
