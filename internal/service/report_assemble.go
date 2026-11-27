package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
	// ScanScope is doc/301 §5.3.1's mandatory disclosure: `cited_only` or
	// `cited+candidate:K`. Without it a delivered file that found no conflict reads as
	// "cross-document contrast was performed and found nothing", which is a much
	// stronger claim than the run made. Omitted only when the revision predates the
	// field, so an old file keeps its old bytes.
	ScanScope string `json:"scan_scope,omitempty"`
	// Claims is the R11 claim appendix (doc/288 §9.2): every sentence the report
	// asserts, with its type and the verdict of the L3 support check. It is frozen
	// with the document for the same reason as the evidence copy — a file that is
	// forwarded must keep saying which of its own sentences it could not substantiate.
	Claims []reportClaimEntry `json:"claims"`
	// ClaimScored/ClaimSupportRate travel with the list because a rate without its
	// denominator is the number K30 forbids. `not_required` claims are in `Claims`
	// but not in the denominator: they never claimed support.
	ClaimScored      int `json:"claim_scored"`
	ClaimNotRequired int `json:"claim_not_required"`
	// ClaimSupportRate is nil when nothing was scored — "0 of 0" is not "0%".
	ClaimSupportRate *float64 `json:"claim_support_rate,omitempty"`
	// ConsistencyIssues is R16's cross-section disclosure, copied from the frozen
	// metrics so the file and the page cannot disagree (the renderer never re-derives
	// it: two readers of one revision would then be reading two different numbers).
	ConsistencyIssues      []reportConsistencyIssue `json:"consistency_issues"`
	ConsistencyKeysChecked int                      `json:"consistency_keys_checked"`
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
	// LocationHints are R17's compiled-artifact locators, printed as
	// 「定位线索（非证据）」. They are a sibling of the citation fields, never input to
	// them: the schema's whole point is that a reader can see navigation and
	// evidence in the same section without the two being confused (doc/295 A54).
	LocationHints []string `json:"location_hints"`
	// ResearchPlan is R18's disclosure for this section: standard retrieval, a
	// planned deep retrieval, or which degradation happened. Omitted for a
	// standard-depth run so a normal report's payload is unchanged in shape.
	ResearchPlan *reportDocumentResearch `json:"research_plan,omitempty"`
}

// reportDocumentResearch is R18's per-section payload. `Planning` is a closed set
// (`planned`, `standard` and the three `fallback_*` words); `Merged`/`Duplicates`
// say how much the extra queries actually added.
type reportDocumentResearch struct {
	Depth      string `json:"depth"`
	Planning   string `json:"planning"`
	Merged     int    `json:"merged"`
	Duplicates int    `json:"duplicates"`
}

// reportClaimEntry is one line of the claim appendix: the sentence as written, what
// kind of statement it is, and how well the evidence it cites supports it.
//
// `Markers` are the citation numbers the sentence used, so a reader can go from
// "this sentence is unsubstantiated" to the exact chunk the writer read.
type reportClaimEntry struct {
	SectionOrd   int    `json:"section_ord"`
	SectionTitle string `json:"section_title,omitempty"`
	Ord          int    `json:"ord"`
	Text         string `json:"text"`
	Type         string `json:"type"`
	Support      string `json:"support"`
	Markers      []int  `json:"markers"`
}

const (
	// reportDocumentSchema is the payload's own schema id. It is versioned
	// separately from the export renderer: adding a block type changes the
	// renderer, changing the JSON shape changes this.
	reportDocumentSchema = "report.document.v3"
	// reportExportRendererVersion is `rgx_export_artifact.renderer_version` for
	// report artifacts. Answers stay on `export.renderer.v1`; the report renderer
	// adds a styled DOCX (styles.xml, header/footer, gap banner), so it is a
	// capability step, not a cosmetic bump (§12 / 292 D5). v3 renders `ord` as the
	// number it is (sections and citations are 1-based on disk) and styles the
	// writer's `**bold**` instead of printing the markers. v4 adds the claim
	// appendix and the cross-section consistency block, i.e. the file now discloses
	// things v3 files never said — a different document contract, not a restyle.
	reportExportRendererVersion = "export.renderer.v5"
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
	claims, err := s.Store.ListReportClaims(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, nil, err
	}
	titles := sectionTitles(sections)
	claimEntries, scored, notRequired := buildReportClaimAppendix(claims, titles)
	var metrics reportQualityMetrics
	if trimmed := strings.TrimSpace(revision.MetricsJSON); trimmed != "" {
		// A corrupt metrics blob degrades to "no consistency disclosure" instead of
		// failing the export: the file's body is still the reviewed text, and a
		// missing appendix section is a smaller lie than an unreadable document.
		_ = json.Unmarshal([]byte(trimmed), &metrics)
	}
	document := &reportDocument{
		Schema:                 reportDocumentSchema,
		ReportID:               report.ID,
		Title:                  report.Title,
		Question:               report.Question,
		RevisionID:             revision.ID,
		Revision:               revision.Revision,
		TemplateKey:            revision.TemplateKey,
		TemplateVersion:        revision.TemplateVersion,
		OutlineConfirmation:    revision.OutlineConfirmation,
		Completeness:           revision.Completeness,
		Gaps:                   decodeReportGaps(revision.GapJSON),
		MetricsJSON:            rawJSONOrNil(revision.MetricsJSON),
		DatasetsJSON:           rawJSONOrNil(revision.DatasetsJSON),
		Sections:               make([]reportDocumentSection, 0, len(sections)),
		EvidenceAppendix:       appendix,
		EvidenceVerifiable:     verifiable,
		Claims:                 claimEntries,
		ClaimScored:            scored,
		ClaimNotRequired:       notRequired,
		ScanScope:              renderScanScope(s.revisionScanScope(ctx, tenantID, revision)),
		ClaimSupportRate:       metrics.ClaimSupportRate,
		ConsistencyIssues:      metrics.ConsistencyIssues,
		ConsistencyKeysChecked: metrics.ConsistencyKeysChecked,
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
			LocationHints:  reportDocumentSectionHints(section),
			ResearchPlan:   reportDocumentSectionResearch(section),
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

// buildReportClaimAppendix turns the stored claim rows into appendix lines and
// returns the two counts the rate needs.
//
// A claim whose section row is missing from `titles` still appears: the claim table
// is keyed by section id and a reader asking "what did this revision assert" must
// not get a shorter list because a section was superseded between two reads.
func buildReportClaimAppendix(
	claims []model.ReportClaim, titles map[string]string,
) ([]reportClaimEntry, int, int) {
	entries := make([]reportClaimEntry, 0, len(claims))
	scored, notRequired := 0, 0
	for _, claim := range claims {
		switch claim.SupportStatus {
		case model.ReportClaimSupportNotRequired:
			notRequired++
		case model.ReportClaimSupportSupported, model.ReportClaimSupportPartiallySupported,
			model.ReportClaimSupportUnsupported:
			scored++
		}
		entries = append(entries, reportClaimEntry{
			SectionOrd: claim.SectionOrd, SectionTitle: titles[claim.SectionID],
			Ord: claim.Ord, Text: claim.ClaimText,
			Type: claim.ClaimType, Support: claim.SupportStatus,
			Markers: reportMarkersInText(claim.ClaimText),
		})
	}
	return entries, scored, notRequired
}

// reportMarkersInText lists the citation numbers a sentence uses, ascending and
// deduped. It reads the stored text rather than the evidence: dangling markers were
// already stripped at commit time (A3c), so every number here resolves.
func reportMarkersInText(text string) []int {
	markers := make([]int, 0, 2)
	seen := make(map[int]struct{}, 2)
	for _, match := range reportCitationMarkerPattern.FindAllStringSubmatch(text, -1) {
		index, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		markers = append(markers, index)
	}
	sort.Ints(markers)
	return markers
}

// reportClaimTypeLabel and reportClaimSupportLabel are the reader-facing names.
// The raw enum never reaches a document: `partially_supported` in the middle of
// Chinese prose is a machine identifier, and an unknown value falls back to itself
// rather than to a guess.
func reportClaimTypeLabel(kind string) string {
	switch kind {
	case model.ReportClaimTypeFact:
		return "事实陈述"
	case model.ReportClaimTypeInference:
		return "推断"
	case model.ReportClaimTypeRecommendation:
		return "建议"
	case model.ReportClaimTypeLimitation:
		return "局限说明"
	default:
		return kind
	}
}

func reportClaimSupportLabel(status string) string {
	switch status {
	case model.ReportClaimSupportSupported:
		return "可核实"
	case model.ReportClaimSupportPartiallySupported:
		return "部分可核实"
	case model.ReportClaimSupportUnsupported:
		return "无法核实"
	case model.ReportClaimSupportNotRequired:
		return "无需核实"
	default:
		return status
	}
}

// The reader-facing labels of three frozen closed sets.
//
// Renderers never print a closed-set word. `partially_supported` next to a chapter
// title is a machine identifier: the panel says 部分可核实, and a file that says
// something else makes the file and the page disagree about the same report (A4).
// The label functions therefore live next to `reportClaimTypeLabel` rather than
// inside any one renderer, so DOCX, PDF, Markdown, HTML and XLSX cannot drift apart
// one renderer at a time. An unknown value falls back to itself instead of a guess:
// a new status must show up visibly, not be silently relabelled.
// reportCompletenessLabel keeps the enum out of the file: a reviewer reading
// "partial" learns nothing that "部分完成" does not say better.
func reportCompletenessLabel(completeness string) string {
	switch completeness {
	case model.ReportCompletenessFull:
		return "完整"
	case model.ReportCompletenessPartial:
		return "部分完成"
	default:
		return completeness
	}
}

func reportBoolLabel(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

// reportXlsxSectionSheet is one row per written section. Section body text travels
// as plain text: a sheet cell cannot carry heading structure, and pretending it can
// (by stuffing `##` into the cell) would make the sheet unreadable in the very tool
// it is opened in. The body column is last so the reviewer's eye stops at the
// statuses first.
func reportXlsxSectionSheet(document *reportDocument) xlsxSheet {
	rows := [][]xlsxCell{{
		xlsxHeaderCell("序号"), xlsxHeaderCell("章节"), xlsxHeaderCell("写作版本"),
		xlsxHeaderCell("证据状态"), xlsxHeaderCell("质量状态"), xlsxHeaderCell("引用数"),
		xlsxHeaderCell("定位线索（非证据）"), xlsxHeaderCell("深研状态"), xlsxHeaderCell("正文（纯文本）"),
	}}
	for _, section := range document.Sections {
		rows = append(rows, []xlsxCell{
			xlsxNumberCell(float64(reportSectionNumber(section.Ord))),
			xlsxTextCell(section.Title),
			xlsxNumberCell(float64(section.SectionVersion)),
			xlsxTextCell(reportEvidenceStatusLabel(section.EvidenceStatus)),
			xlsxTextCell(reportQualityStatusLabel(section.QualityStatus)),
			xlsxNumberCell(float64(section.CitationCount)),
			xlsxWrapCell(reportLocationHintsLine(section.LocationHints)),
			xlsxTextCell(reportResearchPlanLine(section.ResearchPlan)),
			xlsxWrapCell(reportPlainText(section.ContentMD)),
		})
	}
	return xlsxSheet{name: "章节", rows: rows, widths: []int{6, 26, 10, 14, 14, 8, 40, 26, 80}}
}

// reportEvidenceStatusLabel / reportQualityStatusLabel are the reader-facing names of
// the two dimensions §13 forbids folding. The raw closed-set words are machine
// identifiers; a sheet that prints `needs_review` in a 质量状态 column is a sheet the
// reviewer has to translate by hand.
func reportEvidenceStatusLabel(status string) string {
	switch status {
	case model.ReportEvidenceSupported:
		return "有据"
	case model.ReportEvidencePartiallySupported:
		return "部分有据"
	case model.ReportEvidenceUnsupported:
		return "无据"
	case model.ReportEvidenceConflicting:
		return "证据冲突"
	case model.ReportEvidenceOutdated:
		return "证据过期"
	case model.ReportEvidenceNotRequired:
		return "无需证据"
	case "":
		return ""
	default:
		return status
	}
}

func reportQualityStatusLabel(status string) string {
	switch status {
	case model.ReportQualityPassed:
		return "通过"
	case model.ReportQualityNeedsReview:
		return "需复核"
	case model.ReportQualityFailed:
		return "未通过"
	case model.ReportQualityNotChecked:
		return "未检查"
	case "":
		return ""
	default:
		return status
	}
}

// reportClaimAppendixPreamble is the fixed declaration §9.2 requires next to the
// claims. It states what the support check does and does not certify: a mechanical
// "every number appears in the cited chunk" is not a truth claim, and a
// recommendation was never a fact to verify.
const reportClaimAppendixPreamble = "以下列出本报告的全部结论性句子及其证据支持度。支持度由规则检查得出（句中的数字与日期是否出现在所引证据片段中），只说明引用是否存在且贴合，不代表句子为真；推断与建议不代表已核实事实。"

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
		if !ok || snapshot.Purged() {
			// A tombstone is present but its content is gone (doc/319 R-01). Reading
			// `ok` alone would append an empty evidence entry to the appendix — an
			// export that looks complete and cites nothing, which is worse than the
			// 44116 refusal this branch already produces.
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

// reportLocationHintsLine is the single wording the three exporters use for R17's
// locators: 「定位线索（非证据）」 first, then the hints. The label is not a style
// choice — it is the contract that keeps a compiled page index from being read as a
// citation (doc/295 §2.1), so it lives in one function instead of three renderers.
func reportLocationHintsLine(hints []string) string {
	kept := make([]string, 0, len(hints))
	for _, hint := range hints {
		if trimmed := strings.TrimSpace(hint); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return "定位线索（非证据）：" + strings.Join(kept, "；")
}

// reportDocumentSectionHints renders the frozen locators as display lines. A hint
// whose kind is unknown renders as its title only; nothing here can produce a
// citation-like token.
func reportDocumentSectionHints(section model.ReportSection) []string {
	hints := parseReportLocationHints(section.LocationHintsJSON)
	lines := make([]string, 0, len(hints))
	for _, hint := range hints {
		if line := reportLocationHintText(hint); line != "" {
			lines = append(lines, line)
		}
	}
	if lines == nil {
		return []string{}
	}
	return lines
}

// reportDocumentSectionResearch lifts the frozen plan into the document payload.
// A `standard` plan is dropped: the field exists to disclose extra depth or its
// absence, and a standard section has nothing to disclose.
func reportDocumentSectionResearch(section model.ReportSection) *reportDocumentResearch {
	plan := parseReportResearchPlan(section.ResearchPlanJSON)
	if plan == nil || plan.Planning == reportResearchPlanningStandard || plan.Planning == "" {
		return nil
	}
	return &reportDocumentResearch{
		Depth: plan.Depth, Planning: plan.Planning,
		Merged: plan.Merged, Duplicates: plan.Duplicates,
	}
}
