package service

// Compilation artifacts as *locators* (doc/295 §2.1 / R17).
//
// Knowledge compilation produces derived structures — page indexes, wiki pages,
// document trees — that are excellent at answering "where in the corpus does this
// live" and useless at answering "is this true". Doc/288 §6.4 therefore made the
// boundary explicit: a compiled artifact may *steer* an outline (section cues,
// page jumps), but it may never appear in the citation chain, which stays
// `document_id + chunk_id` all the way to the evidence snapshot.
//
// This file implements exactly that boundary and nothing more:
//
//   - locators are read from `rgx_artifact_projection` rows whose state is `ready`
//     and whose dataset is one of the revision's frozen bindings;
//   - a locator is scoped to the section that actually retrieved from that document,
//     so a section is not handed the whole dataset's table of contents;
//   - the output shape has no score, no quote and no chunk id, because anything that
//     looked like evidence would eventually be cited as evidence;
//   - a missing or stale projection produces no locators — the ordinary answer, not
//     an error. Compilation is an enhancement, never a precondition.
//
// The gate that keeps this a *locator* is structural, not editorial: nothing in
// this file returns a value that `commitSection` writes into `ReportCitation` or
// `ReportEvidenceSnapshot`. A54 pins that with "citation rows and evidence_status
// are identical with and without hints".

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// reportLocationHintLimit caps how many locators one section carries. Three is what
// a reader can act on in a dialog; more turns the block into a search result list.
const reportLocationHintLimit = 3

// reportLocatorArtifactLimit bounds the projection read. A workspace with hundreds
// of compiled datasets must not turn one outline step into an unbounded scan.
const reportLocatorArtifactLimit = 200

// reportLocationHint is one locator. It deliberately carries no similarity, no
// excerpt and no chunk id: the moment a locator has a score, someone will sort
// citations by it.
type reportLocationHint struct {
	// Kind is the compilation kind (`page_index` / `wiki` / `tree`).
	Kind string `json:"kind"`
	// ArtifactID names the compiled artifact for traceability.
	ArtifactID string `json:"artifact_id"`
	// Title is what the reader sees, e.g. the wiki page heading or the index entry.
	Title string `json:"title"`
	// SourceDocumentIDs are the documents this locator was built from, so a reader
	// can tell which section's sources it belongs to.
	SourceDocumentIDs []string `json:"source_document_ids,omitempty"`
}

// reportLocatorKinds is the closed set of artifact kinds used as locators. Graph and
// mindmap are excluded: they assert relations, which is precisely the kind of claim
// a report must make from documents rather than from a compiled projection.
var reportLocatorKinds = []string{
	model.CompilationKindPageIndex,
	model.CompilationKindWiki,
	model.CompilationKindTree,
}

// reportLocatorKindRank orders locators deterministically: a page index is the most
// actionable locator, a document tree the least. Ties fall back to artifact id so two
// runs over the same corpus produce the same block byte for byte.
func reportLocatorKindRank(kind string) int {
	for index, candidate := range reportLocatorKinds {
		if candidate == kind {
			return index
		}
	}
	return len(reportLocatorKinds)
}

// reportLocatorArtifact is the subset of a projection payload this feature reads.
// Compilation payloads are open-ended; parsing only these keys keeps locators from
// silently depending on an artifact schema that may grow.
type reportLocatorArtifact struct {
	Title              string   `json:"title"`
	Name               string   `json:"name"`
	Summary            string   `json:"summary"`
	MatchedDocumentIDs []string `json:"matched_document_ids"`
	SourceDocumentIDs  []string `json:"source_document_ids"`
}

// reportLocationHintsFor builds one section's locators.
//
// `sourceDocs` is the set of engine document ids that section's retrieval touched,
// mapped to the logical dataset they came from. A projection is only offered when
// its dataset and one of its documents both match: that intersection is what makes
// the hint *about this section* instead of about the corpus.
func reportLocationHintsFor(
	rows []model.ArtifactProjection, sourceDocs map[string]map[string]struct{},
) []reportLocationHint {
	if len(rows) == 0 || len(sourceDocs) == 0 {
		return nil
	}
	hints := make([]reportLocationHint, 0, reportLocationHintLimit)
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if len(hints) >= reportLocationHintLimit {
			break
		}
		if row.State != model.ArtifactProjectionReady {
			continue
		}
		if reportLocatorKindRank(row.Kind) >= len(reportLocatorKinds) {
			continue
		}
		documents, ok := sourceDocs[row.DatasetID]
		if !ok || len(documents) == 0 {
			continue
		}
		artifact, parsed := reportParseLocatorArtifact(row.ArtifactJSON)
		if !parsed {
			continue
		}
		// The projection's own document list is what the locator points at. An
		// artifact that declares no documents cannot be attributed to a section, so
		// it is skipped rather than offered vaguely.
		overlap := make([]string, 0, len(artifact.SourceDocumentIDs))
		for _, documentID := range artifact.SourceDocumentIDs {
			if _, hit := documents[documentID]; hit {
				overlap = append(overlap, documentID)
			}
		}
		if len(overlap) == 0 {
			continue
		}
		title := firstNonEmptyString(artifact.Title, artifact.Name, artifact.Summary)
		if title == "" {
			title = row.ArtifactID
		}
		if _, dup := seen[row.ArtifactID]; dup {
			continue
		}
		seen[row.ArtifactID] = struct{}{}
		sort.Strings(overlap)
		hints = append(hints, reportLocationHint{
			Kind: row.Kind, ArtifactID: row.ArtifactID, Title: title, SourceDocumentIDs: overlap,
		})
	}
	sort.SliceStable(hints, func(i, j int) bool {
		left, right := reportLocatorKindRank(hints[i].Kind), reportLocatorKindRank(hints[j].Kind)
		if left != right {
			return left < right
		}
		return hints[i].ArtifactID < hints[j].ArtifactID
	})
	return hints
}

// reportParseLocatorArtifact reads the display title and the document list out of a
// projection payload. A payload that does not parse is "no locator", never an error:
// the artifact is a derived structure from another feature, and a malformed one must
// not be able to stop a report.
func reportParseLocatorArtifact(raw string) (reportLocatorArtifact, bool) {
	var artifact reportLocatorArtifact
	if strings.TrimSpace(raw) == "" {
		return artifact, false
	}
	if err := json.Unmarshal([]byte(raw), &artifact); err != nil {
		return artifact, false
	}
	documents := make([]string, 0, len(artifact.SourceDocumentIDs)+len(artifact.MatchedDocumentIDs))
	seen := make(map[string]struct{}, len(documents))
	for _, documentID := range append(append([]string{}, artifact.SourceDocumentIDs...), artifact.MatchedDocumentIDs...) {
		documentID = strings.TrimSpace(documentID)
		if documentID == "" {
			continue
		}
		if _, dup := seen[documentID]; dup {
			continue
		}
		seen[documentID] = struct{}{}
		documents = append(documents, documentID)
	}
	artifact.SourceDocumentIDs = documents
	return artifact, true
}

// reportLocationHintsJSON encodes a section's locators. An empty list encodes as ""
// (no column value) so "no locators" and "an empty array" cannot be confused by a
// reader that only checks for presence.
func reportLocationHintsJSON(hints []reportLocationHint) string {
	if len(hints) == 0 {
		return ""
	}
	encoded, err := json.Marshal(hints)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// parseReportLocationHints decodes the frozen locators. A malformed value reads as
// none: the column is advisory, and a bad value must not make a section unreadable.
func parseReportLocationHints(raw string) []reportLocationHint {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var hints []reportLocationHint
	if err := json.Unmarshal([]byte(trimmed), &hints); err != nil {
		return nil
	}
	out := make([]reportLocationHint, 0, len(hints))
	for _, hint := range hints {
		if strings.TrimSpace(hint.Title) == "" {
			continue
		}
		out = append(out, hint)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// attachReportLocationHints fills `LocationHintsJSON` on every section from the
// workspace's ready compilation projections (doc/295 A54/A55).
//
// It runs once, in the outline step, next to the evidence preview: the locators are
// frozen with the outline so the reader sees the same navigation the author did, and
// so a later recompilation cannot silently change what an already-approved outline
// pointed at.
//
// Best-effort by construction: a failed read is logged and leaves every section
// without locators. Compilation is an enhancement; a report must never depend on it.
func (s *Service) attachReportLocationHints(
	ctx context.Context, tenantID string, sections []model.ReportSection, previews []reportEvidencePreview,
) {
	// `report.locator_hints_enabled` (doc/301 §5.7 KC-1) is the switch the A54 toggle
	// regression turns off, so it has to be read *here*: a registered setting that
	// nothing consults is a promise the workspace cannot keep, and the frozen hints are
	// the one place where "off" is observable (the prompt block and the reader both
	// read this column).
	//
	// Fail closed: an unreadable settings snapshot leaves every section without
	// locators rather than guessing "on". Compilation is an enhancement and a report
	// must never depend on it, so the honest degradation is "no navigation", never
	// "navigation we could not authorize".
	settings, err := s.reportAssuranceSettingsOf(ctx)
	if err != nil {
		logger.Warn("report locators: settings read failed, locators disabled",
			"tenant_id", tenantID, "error", err)
		return
	}
	if !settings.LocatorHintsEnabled {
		return
	}
	datasetIDs := make([]string, 0, len(sections))
	seen := make(map[string]struct{}, len(sections))
	for _, preview := range previews {
		for datasetID := range preview.sources {
			if _, dup := seen[datasetID]; dup {
				continue
			}
			seen[datasetID] = struct{}{}
			datasetIDs = append(datasetIDs, datasetID)
		}
	}
	if len(datasetIDs) == 0 {
		return
	}
	sort.Strings(datasetIDs)
	rows, err := s.Store.ListReadyArtifactProjectionsByDatasets(
		ctx, tenantID, datasetIDs, reportLocatorArtifactLimit)
	if err != nil {
		logger.Warn("report locators: projection read failed",
			"tenant_id", tenantID, "datasets", len(datasetIDs), "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	byDataset := make(map[string][]model.ArtifactProjection, len(datasetIDs))
	for _, row := range rows {
		byDataset[row.DatasetID] = append(byDataset[row.DatasetID], row)
	}
	for index := range sections {
		if index >= len(previews) {
			break
		}
		hints := make([]reportLocationHint, 0, reportLocationHintLimit)
		sources := previews[index].sources
		// Iterate datasets in sorted order so two runs over the same corpus produce
		// the same block, independent of map iteration order.
		for _, datasetID := range datasetIDs {
			if len(hints) >= reportLocationHintLimit {
				break
			}
			scoped := map[string]map[string]struct{}{datasetID: sources[datasetID]}
			hints = append(hints, reportLocationHintsFor(byDataset[datasetID], scoped)...)
		}
		// One block per section: re-rank across datasets (a page index in the second
		// dataset still beats a wiki page in the first) and apply the cap once.
		sort.SliceStable(hints, func(i, j int) bool {
			left, right := reportLocatorKindRank(hints[i].Kind), reportLocatorKindRank(hints[j].Kind)
			if left != right {
				return left < right
			}
			return hints[i].ArtifactID < hints[j].ArtifactID
		})
		if len(hints) > reportLocationHintLimit {
			hints = hints[:reportLocationHintLimit]
		}
		sections[index].LocationHintsJSON = reportLocationHintsJSON(hints)
	}
}

// reportLocationHintLines renders a section's locators for a read model. Kept next to
// reportLocationHintText so the workspace, the reader and the exporters cannot grow
// three slightly different wordings of the same line.
func reportLocationHintLines(hints []reportLocationHint) []string {
	if len(hints) == 0 {
		return nil
	}
	lines := make([]string, 0, len(hints))
	for _, hint := range hints {
		if line := reportLocationHintText(hint); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// reportLocationHintText is the one-line form rendered by the prompt, the reader and
// the three exporters. It never contains a citation marker: a locator that could be
// mistaken for `[3]` would end up in a citation list.
func reportLocationHintText(hint reportLocationHint) string {
	kind := reportLocatorKindLabel(hint.Kind)
	title := strings.TrimSpace(hint.Title)
	if kind == "" {
		return title
	}
	if title == "" {
		return kind
	}
	return kind + "：" + title
}

// reportLocatorKindLabel is the single place a compilation kind becomes a word a
// reader sees. Unknown kinds return "" so a new compilation kind never leaks an
// internal identifier into the report.
func reportLocatorKindLabel(kind string) string {
	switch kind {
	case model.CompilationKindPageIndex:
		return "页面索引"
	case model.CompilationKindWiki:
		return "知识页面"
	case model.CompilationKindTree:
		return "文档目录"
	}
	return ""
}
