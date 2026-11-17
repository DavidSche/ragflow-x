package service

// Prompt, snapshot and evidence shaping for the report generate path
// (doc/288 §7.3, §7.5, §9.3, §9.4).
//
// Everything here is pure: no database, no clock, no ids. That is deliberate —
// the prompt is part of what gets hashed into the section row and the frozen
// execution snapshot, so it must be reproducible from its inputs alone. A helper
// that read config would make the hash describe the caller's moment instead of
// the text that was actually sent.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

const (
	// reportSectionWordDefault is the word budget used when a template section
	// omits `max_words` (the validator allows zero = "no per-section limit").
	reportSectionWordDefault = 400
	// reportEvidenceSnippetRunes / reportEvidenceTotalRunes bound one section's
	// evidence block. The bound exists because the per-section budget is in
	// tokens: an unbounded snippet list would let one long chunk consume the whole
	// envelope before the model even answers.
	reportEvidenceSnippetRunes = 1200
	reportEvidenceTotalRunes   = 12000
	// reportEvidenceSnippetChars is the disclosure-only citation snippet length.
	reportEvidenceSnippetChars = 240
	// reportQueryMaxRunes bounds the retrieval question built from a section.
	reportQueryMaxRunes = 512
	// reportPriorSectionExcerptRunes is how much of a dependency's body is shown
	// to the writer of the dependent section.
	reportPriorSectionExcerptRunes = 400
	// reportTokensPerRuneDivisor is the fallback estimate when the provider
	// answers without a `usage` block: characters/4 is the usual approximation for
	// mixed CJK/Latin text and is clearly labelled as an estimate (see
	// estimateReportTokens). Settling such a call at zero would hide real spend.
	reportTokensPerRuneDivisor = 4
)

// reportDatasetBinding is one frozen knowledge binding. It is stored twice on
// purpose: `rgx_report_revision.datasets_json` is the report's own record of what
// it was allowed to cite, and the execution snapshot embeds the same list so a
// reader of the snapshot alone can answer "which datasets produced this".
type reportDatasetBinding struct {
	DatasetID       string `json:"dataset_id"`
	EngineDatasetID string `json:"engine_dataset_id"`
	Role            string `json:"role"`
}

// parseReportDatasetBindings decodes the frozen binding list. An unparsable or
// empty list is a hard failure for the generate path: retrieving from "whatever
// the workspace holds today" would make the report unreproducible and could cite
// knowledge the creator never selected (§13).
func parseReportDatasetBindings(raw string) ([]reportDatasetBinding, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("report revision has no frozen dataset bindings")
	}
	var bindings []reportDatasetBinding
	if err := json.Unmarshal([]byte(trimmed), &bindings); err != nil {
		return nil, fmt.Errorf("report dataset bindings are invalid: %w", err)
	}
	usable := make([]reportDatasetBinding, 0, len(bindings))
	for _, binding := range bindings {
		if strings.TrimSpace(binding.EngineDatasetID) == "" {
			continue
		}
		usable = append(usable, binding)
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("report revision has no retrieval-capable dataset binding")
	}
	return usable, nil
}

// reportSnapshotModel is the writer model as resolved *before* the first call.
// It records the composite `model_id` the workspace default names and the triple
// the provider endpoint actually needs, because those are different identities
// and only the second one reproduces the call.
type reportSnapshotModel struct {
	ModelID  string `json:"model_id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Instance string `json:"instance"`
}

// reportSnapshotRetrieval is the retrieval configuration of the run.
//
// `SimilarityThreshold` is a pointer and P0-A sends none: the template declares
// the *policy* (`dataset_calibrated`) and the workspace holds no calibrated value
// yet, so the honest record is "we did not send an absolute cutoff, the engine
// applied its own" (§9.4 — a stale absolute value silently produces an empty
// report). A `null` here is information, not a missing field.
type reportSnapshotRetrieval struct {
	TopK                int      `json:"top_k"`
	ThresholdPolicy     string   `json:"threshold_policy"`
	SimilarityThreshold *float64 `json:"similarity_threshold"`
	ThresholdSource     string   `json:"threshold_source"`
	MinEvidence         int      `json:"min_evidence"`
}

// reportSnapshotGeneration records the generation knobs. `Temperature` is a
// string and not a number because the provider endpoint (`ChatToModel`) exposes no
// sampling parameter at all: writing `0` would claim a determinism the call does
// not have.
type reportSnapshotGeneration struct {
	Temperature         string `json:"temperature"`
	MaxTokensPerSection int    `json:"max_tokens_per_section"`
	Stream              bool   `json:"stream"`
	Sections            int    `json:"sections"`
	BudgetTokens        int64  `json:"budget_tokens"`
}

// reportExecutionSnapshot is `rgx_report_revision.execution_snapshot_json`
// (doc/288 §7.3). It answers A8 ("can I tell how this was produced?") and it is
// written once, before the first model call, by the outline step.
type reportExecutionSnapshot struct {
	TemplateKey     string                   `json:"template_key"`
	TemplateVersion int64                    `json:"template_version"`
	EngineVersion   string                   `json:"engine_version"`
	Model           reportSnapshotModel      `json:"model"`
	Datasets        []reportDatasetBinding   `json:"datasets"`
	Retrieval       reportSnapshotRetrieval  `json:"retrieval"`
	Generation      reportSnapshotGeneration `json:"generation"`
	PromptHashes    map[string]string        `json:"prompt_hashes"`
}

// encodeReportExecutionSnapshot serialises the snapshot and its canonical hash.
//
// The hash is SHA-256 over the marshalled bytes: struct field order is fixed at
// compile time and `encoding/json` sorts map keys, so the bytes are stable across
// processes and releases without a separate canonicalisation pass. It is not a
// promise that a re-run produces the same report — §7.3 forbids that claim — it is
// a promise that the recorded conditions cannot change unnoticed.
func encodeReportExecutionSnapshot(snapshot reportExecutionSnapshot) (string, string, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(encoded)
	return string(encoded), hex.EncodeToString(sum[:]), nil
}

// parseReportExecutionSnapshot reads back what the outline step froze.
func parseReportExecutionSnapshot(raw string) (reportExecutionSnapshot, error) {
	var snapshot reportExecutionSnapshot
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return snapshot, fmt.Errorf("report revision has no execution snapshot")
	}
	if err := json.Unmarshal([]byte(trimmed), &snapshot); err != nil {
		return snapshot, fmt.Errorf("report execution snapshot is invalid: %w", err)
	}
	if snapshot.Model.Provider == "" || snapshot.Model.Name == "" {
		return snapshot, fmt.Errorf("report execution snapshot has no writer model")
	}
	return snapshot, nil
}

// reportOutlineSection is one entry of the reviewable outline. It is a copy of the
// template's declaration plus the retrieval query the outline step resolved, so
// the reviewer confirms what will actually be retrieved, not a guess.
type reportOutlineSection struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	Ord              int      `json:"ord"`
	RequiresEvidence bool     `json:"requires_evidence"`
	MaxWords         int      `json:"max_words"`
	DependsOn        []string `json:"depends_on,omitempty"`
	// Instruction is the template's own wording for this section. The reviewer
	// confirms the actual ask, and the writer receives it verbatim instead of a
	// paraphrase invented here.
	Instruction string `json:"instruction"`
	Query       string `json:"query"`
}

// reportOutline is `rgx_report_revision.outline_json` (§11.1). P0-A generates it
// from the pinned template rather than from a model call: the sections, their
// order and their dependencies are the template's contract, and letting a model
// invent them would make "required section missing" a normal outcome instead of a
// failure.
type reportOutline struct {
	TemplateKey     string                 `json:"template_key"`
	TemplateVersion int64                  `json:"template_version"`
	Question        string                 `json:"question"`
	Sections        []reportOutlineSection `json:"sections"`
}

// buildReportOutline turns the pinned template into the outline, the section rows
// and the reviewable JSON. One function for the three so they cannot disagree.
func buildReportOutline(
	tenantID, revisionID, question string,
	templateKey string, templateVersion int64,
	payload *ReportTemplatePayload,
) (reportOutline, []model.ReportSection) {
	outline := reportOutline{
		TemplateKey: templateKey, TemplateVersion: templateVersion,
		Question: question, Sections: make([]reportOutlineSection, 0, len(payload.Sections)),
	}
	sections := make([]model.ReportSection, 0, len(payload.Sections))
	for index, declared := range payload.Sections {
		ord := index + 1
		maxWords := declared.MaxWords
		if maxWords <= 0 {
			maxWords = reportSectionWordDefault
		}
		query := reportSectionQuery(question, declared.Title)
		outline.Sections = append(outline.Sections, reportOutlineSection{
			Key: declared.Key, Title: declared.Title, Ord: ord,
			RequiresEvidence: declared.RequiresEvidence, MaxWords: maxWords,
			DependsOn: declared.DependsOn, Instruction: declared.Prompt, Query: query,
		})
		dependsJSON := ""
		if len(declared.DependsOn) > 0 {
			if encoded, err := json.Marshal(declared.DependsOn); err == nil {
				dependsJSON = string(encoded)
			}
		}
		sections = append(sections, model.ReportSection{
			ID: reportSectionID(tenantID, revisionID, declared.Key), TenantID: tenantID,
			RevisionID: revisionID, Ord: ord, SectionKey: declared.Key,
			// Version 1 is the first materialisation of this section. A P1 rewrite
			// writes version 2 and marks this row `superseded` (§7.4).
			SectionVersion: 1, Title: declared.Title, Query: query,
			DependsOnSectionsJSON: dependsJSON,
			RequiresEvidence:      declared.RequiresEvidence,
			Status:                model.ReportSectionPending,
			QualityStatus:         model.ReportQualityNotChecked,
		})
	}
	return outline, sections
}

// reportSectionQuery builds the per-section retrieval question. The section title
// leads so the embedding is about the section's subject and not about the report's
// opening sentence, which every section would otherwise share.
func reportSectionQuery(question, title string) string {
	trimmedTitle := strings.TrimSpace(title)
	trimmedQuestion := strings.TrimSpace(question)
	combined := trimmedQuestion
	if trimmedTitle != "" {
		combined = trimmedTitle + " " + trimmedQuestion
	}
	runes := []rune(combined)
	if len(runes) > reportQueryMaxRunes {
		return string(runes[:reportQueryMaxRunes])
	}
	return combined
}

// reportWriterSystemPrompt is the fixed instruction every section call shares. It
// is hashed into the snapshot, so changing it is visible as a different
// `prompt_hashes.system` value rather than as a silent behaviour change.
const reportWriterSystemPrompt = `You are writing one section of an evidence-based report.
Rules:
1. Use ONLY the numbered evidence below. Do not add outside knowledge, and do not
   invent facts, numbers, dates, people or quotations.
2. Cite the evidence you use with its number in square brackets, e.g. [2].
3. If the evidence cannot answer the section's question, say so plainly in one
   sentence. An admitted gap is correct; a plausible invention is not.
4. Return GitHub-flavoured Markdown only: no section heading, no title line, no
   preamble, no closing summary of what you did.`

// reportSectionPrompt assembles one section's call. The order is fixed (system →
// report context → section task → prior sections → evidence) so
// `hashReportPrompt` describes a stable text.
func reportSectionPrompt(
	question string, section model.ReportSection, instruction string, maxWords int,
	prior []reportPriorSection, evidence []reportEvidenceItem,
) string {
	var builder strings.Builder
	builder.WriteString(reportWriterSystemPrompt)
	builder.WriteString("\n\n# Report question\n")
	builder.WriteString(strings.TrimSpace(question))
	builder.WriteString("\n\n# Section to write\n")
	builder.WriteString("Title: ")
	builder.WriteString(strings.TrimSpace(section.Title))
	builder.WriteString("\nLength: about ")
	builder.WriteString(fmt.Sprintf("%d words", maxWords))
	if trimmedInstruction := strings.TrimSpace(instruction); trimmedInstruction != "" {
		builder.WriteString("\nInstruction: ")
		builder.WriteString(trimmedInstruction)
	}
	if section.RequiresEvidence {
		builder.WriteString("\nThis section must cite evidence. If none of the evidence below " + "supports a statement, state that the knowledge base does not cover it.")
	} else {
		builder.WriteString("\nThis section may summarise without citations, but it still " + "must not introduce facts that the evidence contradicts.")
	}
	for _, previous := range prior {
		builder.WriteString("\n\n# Already written (for coherence, do not repeat it)\n")
		builder.WriteString(previous.Title)
		if previous.Excerpt != "" {
			builder.WriteString(": ")
			builder.WriteString(previous.Excerpt)
		}
	}
	builder.WriteString("\n\n# Evidence\n")
	if len(evidence) == 0 {
		builder.WriteString("(none)\n")
	}
	for _, item := range evidence {
		builder.WriteString(fmt.Sprintf("[%d] source: %s / %s\n", item.Index, item.DatasetLabel, item.DocumentLabel))
		builder.WriteString(item.Content)
		builder.WriteString("\n\n")
	}
	return strings.TrimRight(builder.String(), "\n") + "\n"
}

// reportPriorSection is the coherence hint for a section that declares
// dependencies.
type reportPriorSection struct {
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

// hashReportPrompt is the `prompt_hash` of a section row (§7.4): the identity of
// the exact text that was sent, so "the input changed but the output did not" is
// provable.
func hashReportPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// hashReportContent is the section's `content_hash`: SHA-256 over the normalised
// body, which is what makes a citation to *this* revision of the text checkable.
func hashReportContent(content string) string {
	sum := sha256.Sum256([]byte(reportNormalizeContent(content)))
	return hex.EncodeToString(sum[:])
}

// reportNormalizeContent strips trailing whitespace per line and collapses the
// trailing newlines, so a re-run that differs only in line endings hashes equal.
func reportNormalizeContent(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// reportEvidenceItem is one retrieved chunk, shaped for prompting and for the
// evidence snapshot. `Index` is the citation number the model sees.
type reportEvidenceItem struct {
	Index         int
	ChunkID       string
	DocumentID    string
	DatasetID     string
	EngineDataset string
	DatasetLabel  string
	DocumentLabel string
	Content       string
	ContentHash   string
	Score         float64
}

// boundReportEvidence caps the evidence block per section. It returns the kept
// items and whether anything was dropped, because a truncated evidence block is
// disclosed (`truncated`) rather than silently shortened.
func boundReportEvidence(items []reportEvidenceItem) ([]reportEvidenceItem, bool) {
	kept := make([]reportEvidenceItem, 0, len(items))
	used := 0
	truncated := false
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			truncated = true
			continue
		}
		runes := []rune(content)
		if len(runes) > reportEvidenceSnippetRunes {
			content = string(runes[:reportEvidenceSnippetRunes])
			truncated = true
		}
		if used+len([]rune(content)) > reportEvidenceTotalRunes {
			truncated = true
			break
		}
		used += len([]rune(content))
		item.Index = len(kept) + 1
		item.Content = content
		kept = append(kept, item)
	}
	return kept, truncated
}

// reportCitedIndexes extracts the citation numbers a section body actually used.
// Only these become `used_in_text=true` citations: recording every retrieved chunk
// as a citation would inflate `citation_coverage` and hide the sections that cite
// nothing.
func reportCitedIndexes(content string, count int) map[int]bool {
	used := make(map[int]bool, count)
	for index := 1; index <= count; index++ {
		marker := fmt.Sprintf("[%d]", index)
		if strings.Contains(content, marker) {
			used[index] = true
		}
	}
	return used
}

// reportEvidenceStatus derives the section's evidence disclosure (§9.3) from what
// was retrieved and what was cited. It never reports `conflicting`/`outdated`:
// those require comparing sources against each other or against the source clock,
// which P0-A does not do, and guessing them would be a false accusation.
func reportEvidenceStatus(requiresEvidence bool, retrieved int, cited int, minEvidence int) string {
	if !requiresEvidence {
		if cited > 0 {
			return model.ReportEvidenceSupported
		}
		return model.ReportEvidenceNotRequired
	}
	if retrieved == 0 {
		return model.ReportEvidenceUnsupported
	}
	if cited == 0 {
		return model.ReportEvidenceUnsupported
	}
	if minEvidence > 0 && cited >= minEvidence {
		return model.ReportEvidenceSupported
	}
	return model.ReportEvidencePartiallySupported
}

// reportModelReply is one decoded model answer.
type reportModelReply struct {
	Text          string
	TokensIn      int64
	TokensOut     int64
	UsageReported bool
}

// decodeReportModelReply accepts the three shapes the engine actually returns for
// a raw model call — `{answer}`, `{message}`, `{choices:[{message:{content}}]}` —
// and falls back to treating the body as plain text. Every shape carries the same
// fact (the answer), and refusing an unexpected-but-readable envelope would turn a
// working model into a failed section.
func decodeReportModelReply(raw string) (reportModelReply, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return reportModelReply{}, fmt.Errorf("model reply is empty")
	}
	var envelope struct {
		Answer   string `json:"answer"`
		Content  string `json:"content"`
		Message  string `json:"message"`
		Text     string `json:"text"`
		Response string `json:"response"`
		Usage    struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			InputTokens      int64 `json:"input_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			OutputTokens     int64 `json:"output_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err == nil {
		reply := reportModelReply{
			TokensIn:  envelope.Usage.PromptTokens + envelope.Usage.InputTokens,
			TokensOut: envelope.Usage.CompletionTokens + envelope.Usage.OutputTokens,
		}
		reply.Text = firstNonEmptyString(
			envelope.Answer, envelope.Content, envelope.Message, envelope.Text, envelope.Response)
		if reply.Text == "" {
			for _, choice := range envelope.Choices {
				if text := firstNonEmptyString(choice.Message.Content, choice.Text); text != "" {
					reply.Text = text
					break
				}
			}
		}
		reply.UsageReported = reply.TokensIn > 0 || reply.TokensOut > 0
		if strings.TrimSpace(reply.Text) != "" {
			return reply, nil
		}
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		// A JSON body with no readable answer is a protocol oddity, not a section.
		return reportModelReply{}, fmt.Errorf("model reply has no answer text")
	}
	// Tokens stay zero on purpose: the caller has the prompt text and owns the
	// estimate (see `reportSectionRun.writeSection`).
	return reportModelReply{Text: trimmed}, nil
}

// estimateReportTokens is the fallback when a provider answers without `usage`:
// roughly four characters per token. It is an estimate and the section event says
// so; the alternative (settling at zero) would report a free report that was
// actually billed.
func estimateReportTokens(text string) int64 {
	runes := len([]rune(text))
	if runes == 0 {
		return 0
	}
	return int64((runes + reportTokensPerRuneDivisor - 1) / reportTokensPerRuneDivisor)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return value
		}
	}
	return ""
}

// reportSectionID derives a section's id from its identity instead of minting a
// random one: `(revision, section_key)` is unique, so a retried outline insert
// collides on the primary key and the transaction aborts rather than adding a
// second copy of the same section.
func reportSectionID(tenantID, revisionID, sectionKey string) string {
	sum := sha256.Sum256([]byte(tenantID + "\x00" + revisionID + "\x00" + sectionKey))
	return hex.EncodeToString(sum[:16])
}
