package service

import (
	"context"
	"encoding/json"
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
	EvidenceStatus string `json:"evidence_status,omitempty"`
	QualityStatus  string `json:"quality_status,omitempty"`
	CitationCount  int    `json:"citation_count"`
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
	// reportExportTemplateID is the layout template id, suffixed with the format:
	// the content structure lives in `rgx_report_template`, the rendering template
	// here, and the two are only weakly joined by `template_key` (§7.2).
	reportExportTemplateID      = "report-compliance-v1"
	reportExportTemplateVersion = 1
	// reportExportPolicyVersion is the row-level policy the artifact was minted
	// under. Freezing it means a later P1 share policy cannot retroactively make a
	// delivered file look like it was authorized differently.
	reportExportPolicyVersion = "report_access_v1"
)

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
