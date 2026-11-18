package service

// EvidenceUsage: content is deduplicated, references are not (doc/288 §7.5.1,
// A37/A27).
//
// The two layers have opposite requirements, which is why they are separate rows:
//
//   - `rgx_report_evidence_snapshot` is keyed by `(tenant_id, chunk_id,
//     source_content_hash)` and stores content only. Two sections citing the same
//     chunk share one snapshot row.
//   - `rgx_report_citation` is keyed by `(section_id, ord)` and stores the
//     per-reference context: who cited it, under which frozen dataset scope, at
//     which byte/rune window, when.
//
// v2.2 put the permission scope on the *snapshot* row. That is unsound by
// construction: the second citer hits the deduplicated row and has nowhere to write
// its own context, so an audit query answers "user A, scope of A" for a citation
// made by user B (K46). The assertions below are the ones that keep the split real:
// the snapshot type must have no permission column at all, and every citation must
// carry its own two JSON columns.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// citationsForGeneration runs a report whose every evidence section cites the same
// single chunk, so the snapshot has to be deduplicated across sections.
func citationsForGeneration(t *testing.T, name string) (*reportFixture, CreateReportResult) {
	t.Helper()
	fx, engine := newReportRunFixture(t, name)
	created := fx.outlineJob(t, "link-policy-"+name, "req-"+name+"-0001")
	fx.runSlice(t, created.Job.ID)
	if _, err := fx.svc.ConfirmReportOutline(context.Background(), fx.owner.ID, fx.tenantID,
		created.Report.ID, 1, ConfirmReportOutlineRequest{}); err != nil {
		t.Fatalf("confirm outline: %v", err)
	}
	engine.mu.Lock()
	// Exactly one chunk, so two sections cannot avoid sharing it: dedup is exercised
	// by the scenario rather than assumed.
	engine.chunks = &ragflow.RetrieveDatasetsResult{Chunks: []map[string]interface{}{
		{
			"chunk_id": "chunk-shared", "document_id": "doc-travel",
			"document_name": "差旅制度.md", "dataset_id": fx.datasetLink,
			"content": "差旅住宿标准为每晚上限 600 元，超标需提前审批。", "similarity": 0.9,
		},
	}}
	engine.replies = []string{
		`{"answer":"总体结论：合规。[1]","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		`{"answer":"适用制度为差旅制度。[1]","usage":{"prompt_tokens":12,"completion_tokens":6}}`,
		`{"answer":"逐条判定：住宿标准符合。[1]","usage":{"prompt_tokens":14,"completion_tokens":7}}`,
		`{"answer":"差距：审批留痕缺失。[1]","usage":{"prompt_tokens":16,"completion_tokens":8}}`,
		`{"answer":"整改：补齐审批留痕。[1]","usage":{"prompt_tokens":18,"completion_tokens":9}}`,
	}
	engine.mu.Unlock()
	fx.runSlice(t, created.Job.ID)
	return fx, created
}

func TestReportCitationsCarryTheirOwnEvidenceUsage(t *testing.T) {
	fx, created := citationsForGeneration(t, "evidence-usage")
	ctx := context.Background()

	var snapshots []model.ReportEvidenceSnapshot
	if err := fx.gdb.Where("tenant_id = ?", fx.tenantID).Find(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	// One chunk, several citing sections: dedup by content is the point.
	if len(snapshots) != 1 {
		t.Fatalf("the same chunk cited by several sections must share one snapshot row, got %d", len(snapshots))
	}
	snapshot := snapshots[0]

	citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, created.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(citations) < 2 {
		t.Fatalf("usage is never deduplicated: expected at least two citation rows, got %d", len(citations))
	}

	for _, citation := range citations {
		if citation.SectionID == "" || citation.SectionOrd == 0 {
			t.Fatalf("citation must name its section: %+v", citation)
		}
		if citation.EvidenceSnapshotID != snapshot.ID {
			t.Fatalf("citation must point at the shared snapshot %q, got %q",
				snapshot.ID, citation.EvidenceSnapshotID)
		}
		// A37's four auditable fields. `referenced_by`/`referenced_at` were already
		// written; the two JSON columns used to stay empty, which made "谁在什么范围下
		// 引用了它" unanswerable in practice.
		if citation.ReferencedBy == "" || citation.ReferencedAt.IsZero() {
			t.Fatalf("citation must record who referenced it and when: %+v", citation)
		}
		if strings.TrimSpace(citation.SourcePermissionScopeJSON) == "" {
			t.Fatal("citation must record the frozen dataset scope it was retrieved under")
		}
		if strings.TrimSpace(citation.ReferenceWindowJSON) == "" {
			t.Fatal("citation must record the window it quoted")
		}

		var scope struct {
			Datasets []struct {
				DatasetID       string `json:"dataset_id"`
				EngineDatasetID string `json:"engine_dataset_id"`
			} `json:"datasets"`
		}
		if err := json.Unmarshal([]byte(citation.SourcePermissionScopeJSON), &scope); err != nil {
			t.Fatalf("permission scope must be readable JSON: %v (%q)", err, citation.SourcePermissionScopeJSON)
		}
		if len(scope.Datasets) == 0 {
			t.Fatalf("permission scope must name the frozen datasets: %q", citation.SourcePermissionScopeJSON)
		}
		if scope.Datasets[0].DatasetID != fx.datasetLink {
			t.Fatalf("permission scope must name the workspace dataset %q, got %q",
				fx.datasetLink, scope.Datasets[0].DatasetID)
		}

		var window struct {
			Start int    `json:"start"`
			End   int    `json:"end"`
			Unit  string `json:"unit"`
		}
		if err := json.Unmarshal([]byte(citation.ReferenceWindowJSON), &window); err != nil {
			t.Fatalf("reference window must be readable JSON: %v (%q)", err, citation.ReferenceWindowJSON)
		}
		if window.Start != 0 || window.End != len([]rune(citation.Snippet)) || window.Unit != "rune" {
			t.Fatalf("reference window must be the prefix [0,%d) in runes, got %+v",
				len([]rune(citation.Snippet)), window)
		}
		// The window has to describe the stored text, not a copy of the snippet: the
		// snippet is the frozen content's prefix (A33), so the offsets must land
		// inside it and reproduce the same runes.
		content := []rune(snapshot.Content)
		if window.End > len(content) {
			t.Fatalf("window %d exceeds the frozen content length %d", window.End, len(content))
		}
		if string(content[window.Start:window.End]) != citation.Snippet {
			t.Fatalf("the recorded window must reproduce the snippet exactly: %q vs %q",
				string(content[window.Start:window.End]), citation.Snippet)
		}
	}

	// A37's field-level half: the snapshot row must have no permission column to
	// write into, otherwise the second citer's context is silently dropped by dedup.
	snapshotType := reflect.TypeOf(model.ReportEvidenceSnapshot{})
	for i := 0; i < snapshotType.NumField(); i++ {
		name := strings.ToLower(snapshotType.Field(i).Name)
		for _, forbidden := range []string{"permission", "scope", "referenced"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("snapshot row must stay content-only, found %q (§7.5.1 / K46)",
					snapshotType.Field(i).Name)
			}
		}
	}
}

// TestReportCitationsKeepSectionCoordinates pins A27: the redundant
// `(revision_id, section_ord, section_version)` columns must agree with the section
// row they describe, and ordinals are unique per section rather than globally — three
// sections may each own an `ord = 1`.
func TestReportCitationsKeepSectionCoordinates(t *testing.T) {
	fx, created := citationsForGeneration(t, "citation-coordinates")
	ctx := context.Background()

	sections := fx.reportSections(t, created.Revision.ID)
	byID := make(map[string]model.ReportSection, len(sections))
	for _, section := range sections {
		byID[section.ID] = section
	}

	citations, err := fx.svc.Store.ListReportCitations(ctx, fx.tenantID, created.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(citations) < 2 {
		t.Fatalf("expected several citations, got %d", len(citations))
	}

	firstOrdinals := make(map[int]map[string]bool)
	for _, citation := range citations {
		if citation.SectionID == "" {
			t.Fatalf("citation.section_id is mandatory: %+v", citation)
		}
		section, ok := byID[citation.SectionID]
		if !ok {
			t.Fatalf("citation points at section %q which does not exist in revision %q",
				citation.SectionID, created.Revision.ID)
		}
		if citation.RevisionID != section.RevisionID ||
			citation.SectionOrd != section.Ord ||
			citation.SectionVersion != section.SectionVersion {
			t.Fatalf("citation coordinates must equal the section row: citation=%+v section.ord=%d section.version=%d revision=%q",
				citation, section.Ord, section.SectionVersion, section.RevisionID)
		}
		if citation.Ord < 1 {
			t.Fatalf("citation ordinals start at 1: %+v", citation)
		}
		if firstOrdinals[citation.Ord] == nil {
			firstOrdinals[citation.Ord] = map[string]bool{}
		}
		if firstOrdinals[citation.Ord][citation.SectionID] {
			t.Fatalf("duplicate (section_id, ord) = (%q, %d): usage must not dedup citations",
				citation.SectionID, citation.Ord)
		}
		firstOrdinals[citation.Ord][citation.SectionID] = true
	}

	owners := firstOrdinals[1]
	if len(owners) < 2 {
		t.Fatalf("each section numbers its own citations from 1, so ord=1 must be shared by several sections; got %d", len(owners))
	}
}
