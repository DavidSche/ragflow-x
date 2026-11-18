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
}

// reportEvidencePreviewLimit caps the source titles frozen per section. More than a
// handful cannot be read in a confirmation dialog, and the point is "which
// documents back this section", not a source dump.
const reportEvidencePreviewLimit = 3

// attachReportEvidencePreview fills `EvidencePreviewJSON` on every section.
//
// The whole loop is best-effort: each failure is local to its section, so one
// engine hiccup cannot blank the preview of the sections that did retrieve.
func (s *Service) attachReportEvidencePreview(
	ctx context.Context, tenantID string, sections []model.ReportSection,
	bindings []reportDatasetBinding, retrieval ReportTemplateRetrieval,
) {
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
}

func (s *Service) reportEvidencePreviews(
	ctx context.Context, tenantID string, sections []model.ReportSection,
	bindings []reportDatasetBinding, retrieval ReportTemplateRetrieval,
) []reportEvidencePreview {
	previews := make([]reportEvidencePreview, len(sections))
	engineDatasetIDs := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding.EngineDatasetID != "" {
			engineDatasetIDs = append(engineDatasetIDs, binding.EngineDatasetID)
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
		for _, chunk := range result.Chunks {
			if strings.TrimSpace(stringFromAny(chunk["content"])) == "" {
				continue
			}
			preview.HitChunks++
			label := firstNonEmptyString(
				stringFromAny(chunk["document_name"]), stringFromAny(chunk["doc_name"]),
				stringFromAny(chunk["document_id"]), stringFromAny(chunk["doc_id"]))
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
		preview.Insufficient = reportEvidencePreviewInsufficient(
			section.RequiresEvidence, preview.HitChunks, retrieval.MinEvidence)
		previews[index] = preview
	}
	return previews
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
