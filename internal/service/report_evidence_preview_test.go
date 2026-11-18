package service

// Outline evidence preview (doc/288 §9.6).
//
// The confirmation screen is the only human decision point in the whole run, so it
// is also the only place where "the knowledge base has nothing for this section" can
// still be acted on cheaply. These tests pin the three properties that make the
// preview trustworthy rather than decorative: it is frozen from a real retrieval, a
// retrieval that did not happen stays *unjudged* (never "plenty"), and a preview
// failure never blocks the run.

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestOutlineFreezesAnEvidencePreviewPerSection(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-0001")
	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) == 0 {
		t.Fatal("the outline must materialise sections")
	}
	// One retrieval per section: the preview describes *this* section's query, so a
	// single shared search would report the report's opening hits for every section.
	if got := engine.retrievalCount(); got != len(sections) {
		t.Fatalf("%d sections must make %d preview retrievals, got %d", len(sections), len(sections), got)
	}
	for _, section := range sections {
		preview := reportSectionEvidencePreview(section.EvidencePreviewJSON)
		if preview == nil {
			t.Fatalf("section %s has no frozen preview", section.SectionKey)
		}
		if preview.HitChunks != 2 {
			t.Fatalf("section %s must report the 2 chunks the fixture engine returned, got %d",
				section.SectionKey, preview.HitChunks)
		}
		if len(preview.TopSources) != 1 || preview.TopSources[0] != "制度汇编.md" {
			t.Fatalf("section %s must name the source document, got %v",
				section.SectionKey, preview.TopSources)
		}
		if preview.Insufficient == nil {
			t.Fatalf("section %s retrieved successfully, so the shortage must be judged", section.SectionKey)
		}
		if preview.Note != "" {
			t.Fatalf("a successful preview carries no note, got %q", preview.Note)
		}
	}

	// The read surface hands the same preview to the confirmation screen.
	snapshot, err := fx.svc.GetReportJobSnapshot(context.Background(), fx.owner.ID, fx.tenantID, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sections) != len(sections) {
		t.Fatalf("snapshot must carry every section, got %d of %d", len(snapshot.Sections), len(sections))
	}
	for _, section := range snapshot.Sections {
		if section.EvidencePreview == nil {
			t.Fatalf("snapshot section %s lost its preview", section.SectionKey)
		}
		if section.EvidencePreview.HitChunks != 2 {
			t.Fatalf("snapshot preview disagrees with the row: %d", section.EvidencePreview.HitChunks)
		}
	}
}

// TestAnUnavailablePreviewIsUnjudgedAndDoesNotBlockTheOutline: the engine is down,
// which is a normal morning. The preview must say "did not look" — a `false`
// here would tell the reviewer the section is well supported by evidence nobody
// fetched, and an error would cancel a run that only wanted to be helpful.
func TestAnUnavailablePreviewIsUnjudgedAndDoesNotBlockTheOutline(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview-down")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-down")
	engine.mu.Lock()
	engine.retrieveErr = errors.New("engine is unreachable")
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	revision, err := fx.svc.Store.GetReportRevision(context.Background(), fx.tenantID, created.Report.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != model.ReportRevisionAwaitingOutline {
		t.Fatalf("a preview failure must not stop the outline, revision is %q", revision.Status)
	}
	sections := fx.reportSections(t, created.Revision.ID)
	if len(sections) == 0 {
		t.Fatal("the outline must still materialise its sections")
	}
	for _, section := range sections {
		preview := reportSectionEvidencePreview(section.EvidencePreviewJSON)
		if preview == nil {
			t.Fatalf("section %s must still carry a preview payload", section.SectionKey)
		}
		if preview.Insufficient != nil {
			t.Fatalf("section %s was never looked at, yet the preview judged it %v",
				section.SectionKey, *preview.Insufficient)
		}
		if preview.Note != "retrieval_unavailable" {
			t.Fatalf("section %s must say why it is unjudged, got %q", section.SectionKey, preview.Note)
		}
		if preview.TopSources == nil {
			t.Fatalf("section %s must serialise an empty source list, not null", section.SectionKey)
		}
	}
}

// TestEvidencePreviewShortageIsMeasuredAgainstMinEvidence is the one judgement in
// the preview, so it is pinned per case rather than by example.
func TestEvidencePreviewShortageIsMeasuredAgainstMinEvidence(t *testing.T) {
	cases := []struct {
		name     string
		requires bool
		hits     int
		min      int
		want     bool
	}{
		{"nothing found where two were required", true, 0, 2, true},
		{"exactly the minimum", true, 2, 2, false},
		{"more than the minimum", true, 5, 2, false},
		{"one short", true, 1, 2, true},
		{"an optional-evidence section has no shortage", false, 0, 2, false},
		// `min_evidence = 0` is a standard of zero, not "unset" (§7.8): there is no
		// scarcity to report, and calling it insufficient would invent a rule the
		// template never declared.
		{"no declared minimum", true, 0, 0, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := reportEvidencePreviewInsufficient(testCase.requires, testCase.hits, testCase.min)
			if got == nil {
				t.Fatal("a computed preview must judge the section")
			}
			if *got != testCase.want {
				t.Fatalf("insufficient = %v, want %v", *got, testCase.want)
			}
		})
	}
}

// TestEvidencePreviewCapsItsSourceList: a preview is a summary. Five chunks from two
// documents are five hits but two sources, and a section that hits ten documents
// names three — the list is for orientation, not for reading.
func TestEvidencePreviewCapsItsSourceList(t *testing.T) {
	fx, engine := newReportRunFixture(t, "evidence-preview-cap")
	created := fx.outlineJob(t, fx.datasetLink, "req-evidence-preview-cap")
	chunks := make([]map[string]interface{}, 0, 6)
	for index, name := range []string{"甲.md", "甲.md", "乙.md", "丙.md", "丁.md", "甲.md"} {
		chunks = append(chunks, map[string]interface{}{
			"chunk_id":    "chunk-" + name + string(rune('0'+index)),
			"document_id": "doc-" + name, "document_name": name,
			"dataset_id": "engine-ds", "content": "内容 " + name,
		})
	}
	engine.mu.Lock()
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: chunks}
	engine.mu.Unlock()

	fx.runSlice(t, created.Job.ID)

	sections := fx.reportSections(t, created.Revision.ID)
	preview := reportSectionEvidencePreview(sections[0].EvidencePreviewJSON)
	if preview == nil {
		t.Fatal("no preview")
	}
	if preview.HitChunks != 6 {
		t.Fatalf("hits count chunks, got %d", preview.HitChunks)
	}
	want := []string{"甲.md", "乙.md", "丙.md"}
	if len(preview.TopSources) != len(want) {
		t.Fatalf("top sources must be capped at %d distinct documents, got %v",
			reportEvidencePreviewLimit, preview.TopSources)
	}
	for index, name := range want {
		if preview.TopSources[index] != name {
			t.Fatalf("top sources must keep first-seen order, got %v", preview.TopSources)
		}
	}
}

// TestARowWithoutAPreviewReadsAsUnjudged, not as "supported": rows written before
// v175 (and unreadable payloads) have nothing frozen, so the read surface must hand
// back nil.
func TestARowWithoutAPreviewReadsAsUnjudged(t *testing.T) {
	if got := reportSectionEvidencePreview(""); got != nil {
		t.Fatalf("an empty column means nothing was frozen, got %+v", got)
	}
	if got := reportSectionEvidencePreview("{not json"); got != nil {
		t.Fatalf("an unreadable preview must not be guessed at, got %+v", got)
	}
	decoded := reportSectionEvidencePreview(`{"hit_chunks":3,"top_sources":[],"insufficient":null}`)
	if decoded == nil || decoded.TopSources == nil {
		t.Fatalf("a null source list must decode to an empty list, got %+v", decoded)
	}
	if decoded.Insufficient != nil {
		t.Fatalf("null insufficient must stay unjudged, got %v", *decoded.Insufficient)
	}
}
