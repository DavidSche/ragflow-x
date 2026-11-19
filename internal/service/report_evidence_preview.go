package service

// Outline evidence preview (doc/288 §9.6 证据优先 P0 漏项).
//
// The reviewer confirms an outline while the only thing they can see is a list of
// titles. That makes the confirmation a guess: a section whose knowledge base holds
// nothing looks exactly like one with twenty hits, and the shortfall is discovered
// minutes later, after the tokens are spent, as an empty body.
//
// So the outline step runs one retrieval per section and freezes what it found on
// the section row. Three rules make that preview honest rather than decorative:
//
//  1. it is a *preview*: the counts are what the engine answered for this query at
//     this moment, not a sufficiency verdict — the UI must say so;
//  2. `insufficient` is nullable. A retrieval that could not be made (engine down,
//     no bound dataset) leaves it `null`, never `false`: "we did not look" and
//     "there is plenty" must not render the same;
//  3. a preview failure never stops the run. It is advisory by construction, so it
//     is logged and the outline proceeds.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// reportEvidencePreview is one section's evidence盘面 at outline time.
type reportEvidencePreview struct {
	HitChunks  int      `json:"hit_chunks"`
	TopSources []string `json:"top_sources"`
	// Insufficient is the only judgement here, and it is nullable: `nil` means the
	// preview could not be computed, so a client must render "未评估" instead of an
	// alarm or an all-clear.
	Insufficient *bool `json:"insufficient"`
	// Note is a machine word (`no_datasets`, `retrieval_unavailable`), not prose: the
	// UI owns the wording, and a note is the only field that explains a `null`
	// `insufficient`.
	Note string `json:"note,omitempty"`
	// Excerpt is the opening of the best-ranked chunk, so the reviewer sees what the
	// section would be written from instead of only how many hits it has. Truncated
	// to `reportEvidencePreviewExcerptRunes` and whitespace-collapsed: the dialog is a
	// preview, and a wrapped 2KB paragraph is not previewable.
	Excerpt string `json:"excerpt,omitempty"`
	// ConflictCount and ConflictLabels are R13's P1 addition (doc/288 §9.6「P1 加支持度
	// 与冲突提示」): the previewed sources disagree about the same keyed fact. It is
	// the same detector the written section uses (`reportEvidenceSignals`), run early
	// enough that the reviewer can unbind a dataset before paying for the run.
	//
	// It is *not* a verdict about the section: a conflict found in the preview may
	// never reach the body (the writer cites what it uses). `0` means "no conflict
	// among the chunks we looked at", which is not "no conflict in the corpus".
	ConflictCount  int      `json:"conflict_count"`
	ConflictLabels []string `json:"conflict_labels,omitempty"`
	// sources is in-memory only (doc/295 R17): for each logical dataset this
	// section's retrieval hit, the engine document ids it touched. It is never
	// serialised — it exists so the locator step can scope compilation artifacts to
	// "documents this section actually read" instead of the whole dataset.
	sources map[string]map[string]struct{}
}

// reportEvidencePreviewLimit caps the source titles frozen per section. More than a
// handful cannot be read in a confirmation dialog, and the point is "which
// documents back this section", not a source dump.
const reportEvidencePreviewLimit = 3

// reportEvidencePreviewExcerptRunes is how much of the top chunk the preview keeps.
// Long enough to recognise the passage, short enough to fit one dialog row.
const reportEvidencePreviewExcerptRunes = 120

// reportEvidencePreviewConflictLimit caps the conflict lines kept in the preview.
// A corpus where everything disagrees is a corpus problem, and the outline dialog is
// not the place to list twenty of them.
const reportEvidencePreviewConflictLimit = 3

// attachReportEvidencePreview fills `EvidencePreviewJSON` on every section.
//
// The whole loop is best-effort: each failure is local to its section, so one
// engine hiccup cannot blank the preview of the sections that did retrieve.
func (s *Service) attachReportEvidencePreview(
	ctx context.Context, tenantID string, sections []model.ReportSection,
	bindings []reportDatasetBinding, retrieval ReportTemplateRetrieval,
) []reportEvidencePreview {
	previews := s.reportEvidencePreviews(ctx, tenantID, sections, bindings, retrieval)
	for index := range sections {
		preview := previews[index]
		if len(preview.TopSources) == 0 {
			preview.TopSources = []string{}
		}
		encoded, err := json.Marshal(preview)
		if err != nil {
			continue
		}
		sections[index].EvidencePreviewJSON = string(encoded)
	}
	return previews
}

func (s *Service) reportEvidencePreviews(
	ctx context.Context, tenantID string, sections []model.ReportSection,
	bindings []reportDatasetBinding, retrieval ReportTemplateRetrieval,
) []reportEvidencePreview {
	previews := make([]reportEvidencePreview, len(sections))
	engineDatasetIDs := make([]string, 0, len(bindings))
	// logicalByEngine is what lets a chunk's engine dataset id become the workspace
	// dataset id a projection row is keyed by (R17). A chunk from an unbound dataset
	// has no entry and is ignored: a locator must never leak beyond the bindings.
	logicalByEngine := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		if binding.EngineDatasetID != "" {
			engineDatasetIDs = append(engineDatasetIDs, binding.EngineDatasetID)
			logicalByEngine[binding.EngineDatasetID] = binding.DatasetID
		}
	}
	for index, section := range sections {
		preview := reportEvidencePreview{TopSources: []string{}}
		if len(engineDatasetIDs) == 0 {
			preview.Note = "no_datasets"
			previews[index] = preview
			continue
		}
		result, err := s.RAGFlow.RetrieveDatasets(ctx, ragflow.RetrieveDatasetsRequest{
			DatasetIDs: engineDatasetIDs, Question: section.Query,
			Page: 1, PageSize: reportRetrievalTopK(retrieval.TopK),
			SimilarityThreshold: nil, TopK: reportRetrievalTopK(retrieval.TopK),
		})
		if err != nil || result == nil {
			// `nil` insufficient, never false: an engine that did not answer has not
			// told us the section is fine.
			preview.Note = "retrieval_unavailable"
			previews[index] = preview
			if err != nil {
				logger.Warn("report evidence preview: retrieval failed",
					"tenant_id", tenantID, "section_key", section.SectionKey, "error", err)
			}
			continue
		}
		seen := make(map[string]struct{}, reportEvidencePreviewLimit)
		items := make([]reportEvidenceItem, 0, len(result.Chunks))
		for _, chunk := range result.Chunks {
			content := strings.TrimSpace(stringFromAny(chunk["content"]))
			if content == "" {
				continue
			}
			preview.HitChunks++
			label := reportChunkDocumentLabel(chunk, "")
			items = append(items, reportEvidenceItem{
				ChunkID: firstNonEmptyString(stringFromAny(chunk["chunk_id"]),
					stringFromAny(chunk["id"]), stringFromAny(chunk["row_id"])),
				DocumentID:    stringFromAny(chunk["document_id"]),
				DocumentLabel: label, Content: content, SourceContent: content,
			})
			if preview.Excerpt == "" {
				// The engine answers in rank order, so the first non-empty chunk is the
				// passage the writer would lean on most.
				preview.Excerpt = reportPreviewExcerpt(content)
			}
			if label == "" {
				continue
			}
			if _, ok := seen[label]; ok {
				continue
			}
			if len(preview.TopSources) >= reportEvidencePreviewLimit {
				continue
			}
			seen[label] = struct{}{}
			preview.TopSources = append(preview.TopSources, label)
		}
		preview.sources = previewSourcesByDocument(logicalByEngine, result.Chunks)
		preview.ConflictCount, preview.ConflictLabels = reportPreviewConflicts(items)
		preview.Insufficient = reportEvidencePreviewInsufficient(
			section.RequiresEvidence, preview.HitChunks, retrieval.MinEvidence)
		previews[index] = preview
	}
	return previews
}

// reportPreviewExcerpt collapses whitespace and clips the opening of the top chunk.
// Newlines are flattened because the preview renders in one row: a passage that keeps
// its Markdown line breaks pushes everything else out of the dialog.
func reportPreviewExcerpt(content string) string {
	collapsed := strings.Join(strings.Fields(content), " ")
	runes := []rune(collapsed)
	if len(runes) <= reportEvidencePreviewExcerptRunes {
		return collapsed
	}
	return string(runes[:reportEvidencePreviewExcerptRunes]) + "…"
}

// reportPreviewConflicts runs R12's detector over the previewed chunks and returns
// the conflict count plus the keyed-fact details.
//
// Only conflicts are reported here, not staleness: an `outdated` signal means one
// document carries two versions' worth of text, which the preview cannot distinguish
// from a single chunk quoting a superseded number — and an outline warning that
// cannot be acted on trains the reviewer to ignore the dialog.
func reportPreviewConflicts(items []reportEvidenceItem) (int, []string) {
	if len(items) < 2 {
		return 0, nil
	}
	labels := make([]string, 0, 2)
	for _, signal := range reportEvidenceSignals(items) {
		if signal.Kind != reportSignalConflicting {
			continue
		}
		for _, detail := range strings.Split(signal.Detail, "; ") {
			if strings.TrimSpace(detail) == "" {
				continue
			}
			if len(labels) >= reportEvidencePreviewConflictLimit {
				break
			}
			labels = append(labels, detail)
		}
	}
	if len(labels) == 0 {
		return 0, nil
	}
	return len(labels), labels
}

// reportChunkDocumentLabel resolves the name a reader recognises for one retrieved
// chunk, and is the single place both the outline preview and the citation ledger
// ask the question.
//
// RAGFlow v1.0.0 answers `/retrieval` with `document_id` plus `document_keyword`
// (the file name); the `document_name`/`doc_name` keys belong to other builds, so
// they are tried first and are usually absent. Omitting `document_keyword` made the
// confirmation screen print raw ids — "命中 8 段 · 725ea28d… 9a9f2f77…" — and put
// the same id in every citation label (live on 4.151, 2026-11-18). The id stays as
// the last resort: traceable beats blank.
func reportChunkDocumentLabel(chunk map[string]interface{}, fallbackID string) string {
	return firstNonEmptyString(
		stringFromAny(chunk["document_name"]), stringFromAny(chunk["doc_name"]),
		stringFromAny(chunk["document_keyword"]),
		stringFromAny(chunk["document_id"]), stringFromAny(chunk["doc_id"]), fallbackID)
}

// reportEvidencePreviewInsufficient answers "is this section short of evidence?" and
// is the only place that decides it.
//
// `min_evidence = 0` is a standard of zero, not "unset" — the same reading the
// evidence status uses (doc/288 §7.8). A section that requires no evidence has no
// shortage to report, so it is judged `false` rather than left `null`: we did look,
// and nothing is missing.
func reportEvidencePreviewInsufficient(requiresEvidence bool, hits, minEvidence int) *bool {
	insufficient := false
	if requiresEvidence && minEvidence > 0 && hits < minEvidence {
		insufficient = true
	}
	return &insufficient
}

// reportSectionEvidencePreview decodes the frozen preview for a read surface. A row
// written before this column existed (or with an unreadable payload) answers `nil`,
// which the UI renders as "未评估" — the honest reading of "nothing was frozen".
func reportSectionEvidencePreview(raw string) *reportEvidencePreview {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	preview := &reportEvidencePreview{}
	if err := json.Unmarshal([]byte(trimmed), preview); err != nil {
		return nil
	}
	if preview.TopSources == nil {
		preview.TopSources = []string{}
	}
	return preview
}

// previewSourcesByDocument groups the chunks a section retrieved by the logical
// dataset that owns them, keeping only chunks whose dataset is one of the frozen
// bindings. Each entry is a document id the section actually read — the intersection
// R17 uses to decide whether a compiled artifact is about this section.
func previewSourcesByDocument(
	logicalByEngine map[string]string, chunks []map[string]interface{},
) map[string]map[string]struct{} {
	if len(logicalByEngine) == 0 || len(chunks) == 0 {
		return nil
	}
	sources := make(map[string]map[string]struct{}, len(logicalByEngine))
	for _, chunk := range chunks {
		datasetID := logicalByEngine[stringFromAny(chunk["dataset_id"])]
		documentID := stringFromAny(chunk["document_id"])
		if datasetID == "" || documentID == "" {
			continue
		}
		if sources[datasetID] == nil {
			sources[datasetID] = make(map[string]struct{}, 4)
		}
		sources[datasetID][documentID] = struct{}{}
	}
	if len(sources) == 0 {
		return nil
	}
	return sources
}
