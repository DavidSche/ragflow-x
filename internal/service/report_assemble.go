package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report assembly (doc/288 §7.7 / §12 T6).
//
// The writer produces per-section markdown; nothing in the run ever produced "the
// report as one document". That gap is what this file closes: a published revision
// is assembled into a frozen block sequence — ordered sections, each with the
// version it was written at, its citation ids, and the revision-level disclosure
// (`completeness` + `gap_json` + `metrics_json`).
//
// Three rules make the result usable as a delivered artifact rather than a view:
//
//  1. The payload carries *content only*. No timestamps, no ids minted at export
//     time. Two exports of the same revision therefore produce byte-identical
//     JSON, which is what lets `canonical_hash` be the idempotency component
//     (§7.7) instead of a random value that makes every click a new artifact.
//  2. Gaps travel with the document. A partial report that loses its disclosure on
//     the way into the file is a document that lies once it is forwarded
//     (A39/A41), so `completeness`/`gaps` are part of the frozen payload and the
//     renderers read them from there — never from the read API.
//  3. Only `succeeded` sections carry body text. A failed or skipped section has no
//     content to render, and it is already named by a gap entry; inventing a
//     placeholder heading for it would fabricate structure the reviewer never
//     approved.
type reportDocument struct {
	Schema   string `json:"schema"`
	ReportID string `json:"report_id"`
	Title    string `json:"title"`
	Question string `json:"question"`

	RevisionID          string `json:"revision_id"`
	Revision            int64  `json:"revision"`
	TemplateKey         string `json:"template_key"`
	TemplateVersion     int64  `json:"template_version"`
	OutlineConfirmation string `json:"outline_confirmation"`

	Completeness string           `json:"completeness"`
	Gaps         []reportGapEntry `json:"gaps"`
	// MetricsJSON and DatasetsJSON are the revision's own frozen columns, embedded
	// rather than re-derived: re-deriving would let an export disagree with the
	// numbers the UI showed.
	MetricsJSON  json.RawMessage `json:"metrics,omitempty"`
	DatasetsJSON json.RawMessage `json:"datasets,omitempty"`

	Sections []reportDocumentSection `json:"sections"`
	// EvidenceAppendix is the frozen evidence copy (§7.5.1 / A43). It is built at
	// export time from `rgx_report_citation.snippet` + the snapshot `content`, and
	// from then on lives inside this payload — so purging the snapshots later
	// cannot make a delivered file lose its appendix. The reverse direction is why
	// `CreateReportExport` refuses to *re-export* when a snapshot is gone: an empty
	// appendix would be a lie about evidence that no longer exists.
	EvidenceAppendix []reportEvidenceEntry `json:"evidence_appendix"`
	// EvidenceVerifiable mirrors the read API's `evidence_verifiable` at export
	// time. It is frozen so a forwarded file can state "these citations could be
	// re-checked on 2026-11-18" without claiming they can be re-checked today.
	EvidenceVerifiable bool `json:"evidence_verifiable"`
}

// reportEvidenceEntry is one appendix line: what was cited, where it came from,
// and the frozen excerpt a reviewer can read without the original document.
type reportEvidenceEntry struct {
	CitationID string `json:"citation_id"`
	SectionOrd int    `json:"section_ord"`
	// SectionTitle is denormalised for the reader: an appendix grouped by
	// "which claim does this support" is usable, one keyed by ordinal is not.
	SectionTitle string `json:"section_title,omitempty"`
	Ord          int    `json:"ord"`
	SnapshotID   string `json:"snapshot_id"`
	DatasetID    string `json:"dataset_id,omitempty"`
	DocumentID   string `json:"document_id,omitempty"`
	DocumentName string `json:"document_name,omitempty"`
	ChunkID      string `json:"chunk_id,omitempty"`
	// Snippet is what the writer actually read (citation.snippet); Content is the
	// frozen snapshot body. They differ by truncation, and the appendix prints both
	// so a reviewer can tell "the model saw this" from "the source says this".
	Snippet            string  `json:"snippet,omitempty"`
	Content            string  `json:"content"`
	Truncated          bool    `json:"truncated,omitempty"`
	TruncationStrategy string  `json:"truncation_strategy,omitempty"`
	ContentHash        string  `json:"content_hash,omitempty"`
	RetrievalScore     float64 `json:"retrieval_score"`
	RetrievalScoreType string  `json:"retrieval_score_type,omitempty"`
}

// reportDocumentSection is one ordered chapter of the delivered document.
type reportDocumentSection struct {
	Ord            int    `json:"ord"`
	SectionKey     string `json:"section_key"`
	SectionVersion int    `json:"section_version"`
	Title          string `json:"title"`
	ContentMD      string `json:"content_md"`
	ContentHash    string `json:"content_hash"`
	// EvidenceStatus/QualityStatus are the two dimensions §13 forbids folding: a
	// section can be written and still be "not checked".
	EvidenceStatus string   `json:"evidence_status,omitempty"`
	QualityStatus  string   `json:"quality_status,omitempty"`
	CitationCount  int      `json:"citation_count"`
	CitationIDs    []string `json:"citation_ids"`
}

const (
	// reportDocumentSchema is the payload's own schema id. It is versioned
	// separately from the export renderer: adding a block type changes the
	// renderer, changing the JSON shape changes this.
	reportDocumentSchema = "report.document.v1"
	// reportExportRendererVersion is `rgx_export_artifact.renderer_version` for
	// report artifacts. Answers stay on `export.renderer.v1`; the report renderer
	// adds a styled DOCX (styles.xml, header/footer, gap banner), so it is a
	// capability step, not a cosmetic bump (§12 / 292 D5).
	reportExportRendererVersion = "export.renderer.v2"
	// reportExportTemplateVersion is the layout template's version. The layout
	// itself (styles, header/footer, page setup) is shared by every report template;
	// what differs per template is the *content contract* the file was rendered
	// under, and that is recorded through the id below rather than by duplicating
	// the layout.
	reportExportTemplateVersion = 1
	// reportExportPolicyVersion is the row-level policy the artifact was minted
	// under. Freezing it means a later P1 share policy cannot retroactively make a
	// delivered file look like it was authorized differently.
	reportExportPolicyVersion = "report_access_v1"
)

// reportExportLayoutID is the `rgx_export_template_version.template_id` of one
// rendered file, suffixed with the format.
//
// It follows the *content* template key on purpose. The layout is shared, but a
// postmortem exported under the id `report-compliance-v1` would make an auditor
// reading `rgx_export_job.template_id` conclude the wrong template produced the
// file — the row is evidence, so it must name what actually happened. Reports pinned
// before P0-B keep their stored `report-compliance-v1-<format>` id: this function
// reproduces exactly that string for the compliance template.
func reportExportLayoutID(templateKey, format string) string {
	key := strings.TrimSpace(templateKey)
	if key == "" {
		key = "report"
	}
	return fmt.Sprintf("%s-v%d-%s", key, reportExportTemplateVersion, format)
}

// assembleReportDocument freezes one published revision into the document model
// and returns both the struct and its canonical JSON bytes.
func (s *Service) assembleReportDocument(
	ctx context.Context, tenantID string, report *model.Report, revision *model.ReportRevision,
) (*reportDocument, []byte, error) {
	if revision == nil || revision.Status != model.ReportRevisionPublished {
		return nil, nil, errReportExportRequiresPublished()
	}
	sections, err := s.Store.ListReportSections(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, nil, err
	}
	citations, err := s.Store.ListReportCitations(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, nil, err
	}
	citationIDs := make(map[string][]string, len(sections))
	for _, citation := range citations {
		citationIDs[citation.SectionID] = append(citationIDs[citation.SectionID], citation.ID)
	}
	appendix, verifiable, err := s.buildReportEvidenceAppendix(
		ctx, tenantID, citations, sectionTitles(sections))
	if err != nil {
		return nil, nil, err
	}
	document := &reportDocument{
		Schema:              reportDocumentSchema,
		ReportID:            report.ID,
		Title:               report.Title,
		Question:            report.Question,
		RevisionID:          revision.ID,
		Revision:            revision.Revision,
		TemplateKey:         revision.TemplateKey,
		TemplateVersion:     revision.TemplateVersion,
		OutlineConfirmation: revision.OutlineConfirmation,
		Completeness:        revision.Completeness,
		Gaps:                decodeReportGaps(revision.GapJSON),
		MetricsJSON:         rawJSONOrNil(revision.MetricsJSON),
		DatasetsJSON:        rawJSONOrNil(revision.DatasetsJSON),
		Sections:            make([]reportDocumentSection, 0, len(sections)),
		EvidenceAppendix:    appendix,
		EvidenceVerifiable:  verifiable,
	}
	for _, section := range sections {
		if section.Status != model.ReportSectionSucceeded {
			continue
		}
		ids := citationIDs[section.ID]
		if ids == nil {
			ids = []string{}
		}
		document.Sections = append(document.Sections, reportDocumentSection{
			Ord:            section.Ord,
			SectionKey:     section.SectionKey,
			SectionVersion: section.SectionVersion,
			Title:          section.Title,
			ContentMD:      section.ContentMD,
			ContentHash:    section.ContentHash,
			EvidenceStatus: section.EvidenceStatus,
			QualityStatus:  section.QualityStatus,
			CitationCount:  section.CitationCount,
			CitationIDs:    ids,
		})
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, nil, err
	}
	return document, payload, nil
}

// decodeReportGaps reads the frozen gap list. An empty or unreadable column
// degrades to an empty list rather than failing the export: the disclosure text is
// generated from `completeness` as well, so a corrupt list cannot silently turn a
// partial report into a "full" one.
func decodeReportGaps(raw string) []reportGapEntry {
	gaps := make([]reportGapEntry, 0, 4)
	if strings.TrimSpace(raw) == "" {
		return gaps
	}
	if err := json.Unmarshal([]byte(raw), &gaps); err != nil {
		return make([]reportGapEntry, 0, 4)
	}
	if gaps == nil {
		return make([]reportGapEntry, 0, 4)
	}
	return gaps
}

// rawJSONOrNil keeps an invalid blob out of the payload instead of letting
// json.Marshal fail on a hand-edited column.
func rawJSONOrNil(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return nil
	}
	return json.RawMessage(trimmed)
}

// sectionTitles maps section id → title so an appendix line can name the chapter
// it supports without a second query per citation.
func sectionTitles(sections []model.ReportSection) map[string]string {
	titles := make(map[string]string, len(sections))
	for _, section := range sections {
		titles[section.ID] = section.Title
	}
	return titles
}

// buildReportEvidenceAppendix freezes the evidence copy of one revision.
//
// This is the single place where the export touches evidence, and it is where A43's
// two rules live:
//
//   - **A missing snapshot is refused, not papered over.** A citation whose
//     `evidence_snapshot_id` no longer resolves means the evidence was purged while
//     the citation remained. Rendering the appendix without it would produce a file
//     whose appendix silently claims "no evidence", which is the one outcome §7.6
//     forbids: re-export then answers `409 / 44116`, and the honest alternatives are
//     to keep reading the already-delivered file (its appendix is frozen) or export
//     an older revision that still has its evidence.
//   - **No citations is not a failure.** A report may legitimately have zero
//     citations (an empty or purely structural outline). That exports with an empty
//     appendix and `evidence_verifiable=true`, because there is nothing to verify
//     and nothing was lost.
//
// `evidence_verifiable` is computed from the same facts the read API uses so the
// file and the page agree at export time.
func (s *Service) buildReportEvidenceAppendix(
	ctx context.Context, tenantID string, citations []model.ReportCitation, titles map[string]string,
) ([]reportEvidenceEntry, bool, error) {
	appendix := make([]reportEvidenceEntry, 0, len(citations))
	if len(citations) == 0 {
		return appendix, true, nil
	}
	ids := make([]string, 0, len(citations))
	seen := make(map[string]bool, len(citations))
	for _, citation := range citations {
		if citation.EvidenceSnapshotID == "" || seen[citation.EvidenceSnapshotID] {
			continue
		}
		seen[citation.EvidenceSnapshotID] = true
		ids = append(ids, citation.EvidenceSnapshotID)
	}
	snapshots, err := s.Store.ListReportEvidenceByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, false, err
	}
	byID := make(map[string]model.ReportEvidenceSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byID[snapshot.ID] = snapshot
	}
	for _, citation := range citations {
		snapshot, ok := byID[citation.EvidenceSnapshotID]
		if !ok {
			return nil, false, errReportEvidenceExpired(citation.SectionOrd, citation.Ord)
		}
		appendix = append(appendix, reportEvidenceEntry{
			CitationID:         citation.ID,
			SectionOrd:         citation.SectionOrd,
			SectionTitle:       titles[citation.SectionID],
			Ord:                citation.Ord,
			SnapshotID:         snapshot.ID,
			DatasetID:          citation.SourceDatasetID,
			DocumentID:         snapshot.DocumentID,
			DocumentName:       citation.DocumentLabel,
			ChunkID:            snapshot.ChunkID,
			Snippet:            citation.Snippet,
			Content:            snapshot.Content,
			Truncated:          snapshot.Truncated,
			TruncationStrategy: snapshot.TruncationStrategy,
			ContentHash:        snapshot.ContentHash,
			RetrievalScore:     citation.RetrievalScore,
			RetrievalScoreType: citation.RetrievalScoreType,
		})
	}
	return appendix, true, nil
}
