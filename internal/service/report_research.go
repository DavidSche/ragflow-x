package service

// Deep research (doc/295 §2.2 / R18).
//
// "Deep" here means one thing only: before writing a section, a *planner* call turns
// the section's ask into a handful of targeted retrieval queries, and the section's
// evidence is the union of what those queries returned. It does **not** mean several
// writing agents voting on the answer. Doc/288 §6.4 rejected that ("多写作 Agent 投票")
// for three concrete reasons: the report's structure is the template's, the writer
// must be one voice, and a vote would make the citation ledger ambiguous — three
// agents citing three different chunk sets for one sentence cannot be reconciled
// into one `evidence_status`.
//
// The planner is therefore an *evidence gatherer named after the section's intent*,
// and every one of its failures is a degradation, never a section failure:
//
//	fallback_budget        the token gate refused the planner call
//	fallback_planner_error the planner call or its reply could not be used
//	fallback_unparseable   the planner answered, but not with a query list
//
// In all three cases the section is written from its own retrieval exactly as a
// standard-depth section would be, and `research_plan_json` says so. A deep section
// that degrades is still a correct section; it is only a less thorough one.
//
// Accounting: the planner call is its own ledger row (`reportResearchPlannerReserve`
// tokens, allocated a distinct `call_seq`), because "how much did the extra depth
// cost" must be answerable from the ledger and not inferred from a difference.

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

const (
	// reportResearchPlannerReserve is the envelope for the planner call. It is a
	// fixed reserve like the outline's: the planner's output is a short query list,
	// so its cost does not scale with the section count or the corpus.
	reportResearchPlannerReserve = 600
	// reportResearchQueryLimit caps how many extra retrievals one deep section may
	// run. Each one costs a request per bound dataset; four is enough to cover
	// "definition / numbers / dates / exceptions" without turning one section into
	// a corpus sweep.
	reportResearchQueryLimit = 4
	// reportResearchQueryMaxRunes bounds a single generated query. The engine's
	// retrieval degrades with paragraph-length questions, and a planner that answers
	// with one is not planning.
	reportResearchQueryMaxRunes = 200
)

// research plan states. They are stored in `research_plan_json` and read by the UI,
// so the closed set is also the UI's vocabulary.
const (
	reportResearchPlanningStandard     = "standard"
	reportResearchPlanningPlanned      = "planned"
	reportResearchPlanningPlannerError = "fallback_planner_error"
	reportResearchPlanningBudget       = "fallback_budget"
	reportResearchPlanningUnparseable  = "fallback_unparseable"
)

// decodeReportPlannerReply reads the planner's answer.
//
// `decodeReportModelReply` treats a top-level JSON array as a protocol oddity
// ("JSON body with no answer text"), which is correct for a *writer* reply but
// exactly wrong here: the planner is asked to answer with a bare array, and several
// providers (including the engine's chat endpoint) hand that text back unchanged. A
// planner that answered correctly must not be recorded as `fallback_planner_error`,
// so the array shape is accepted as text and left to `parseReportResearchQueries` to
// judge.
func decodeReportPlannerReply(raw string) (reportModelReply, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "[") {
		var queries []interface{}
		if err := json.Unmarshal([]byte(trimmed), &queries); err == nil {
			// No `usage`: a bare array cannot carry it, so the caller estimates.
			return reportModelReply{Text: trimmed}, nil
		}
	}
	return decodeReportModelReply(raw)
}

// reportResearchQuery is one planned query and what it actually contributed. `Hits`
// is kept because a plan whose queries all returned nothing is the evidence a user
// needs to unbind a dataset — the same reason the outline preview records counts.
type reportResearchQuery struct {
	Query string `json:"query"`
	Hits  int    `json:"hits"`
}

// reportResearchPlan is `rgx_report_section.research_plan_json`: what this section
// asked for and what came back.
//
// `Merged`/`Duplicates` are not decoration: with several queries over the same
// datasets, most hits repeat, and a reader deciding whether "deep" helped needs to
// know that 12 hits meant 4 new passages.
type reportResearchPlan struct {
	Depth      string                `json:"depth"`
	Planning   string                `json:"planning"`
	Queries    []reportResearchQuery `json:"queries,omitempty"`
	Merged     int                   `json:"merged"`
	Duplicates int                   `json:"duplicates"`
}

// reportResearchPlannerPrompt asks for queries, not for prose. Markers around the
// requested shape are what the parser looks for, so the prompt and the parser must
// change together.
func reportResearchPlannerPrompt(question, title, instruction string) string {
	var builder strings.Builder
	builder.WriteString("# Task\n")
	builder.WriteString("You plan retrieval for one section of a report. ")
	builder.WriteString("Answer with a JSON array of search queries and nothing else.\n")
	builder.WriteString("# Report question\n")
	builder.WriteString(strings.TrimSpace(question))
	builder.WriteString("\n# Section\nTitle: ")
	builder.WriteString(strings.TrimSpace(title))
	if trimmed := strings.TrimSpace(instruction); trimmed != "" {
		builder.WriteString("\nInstruction: ")
		builder.WriteString(trimmed)
	}
	builder.WriteString("\n# Rules\n")
	builder.WriteString("- At most 4 queries, each a short keyword-style phrase.\n")
	builder.WriteString("- Each query must target a different facet: definitions, numbers, dates, exceptions.\n")
	builder.WriteString("- Do not restate the section title verbatim, and do not answer the question.\n")
	builder.WriteString("- Do not invent document names.\n")
	builder.WriteString("Example: [\"reimbursement limit\", \"approval threshold\", \"receipt deadline\"]\n")
	return builder.String()
}

// parseReportResearchQueries reads the planner's answer as a query list.
//
// Models wrap JSON in prose or fences even when told not to, so the parser takes the
// outermost bracket pair rather than the whole body. Anything that is not a JSON
// array of strings is `fallback_unparseable` — never a section failure, and never a
// partial parse pretending the plan was followed.
func parseReportResearchQueries(raw string) ([]string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	start := strings.Index(trimmed, "[")
	end := strings.LastIndex(trimmed, "]")
	if start < 0 || end <= start {
		return nil, false
	}
	var declared []string
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &declared); err != nil {
		return nil, false
	}
	queries := make([]string, 0, len(declared))
	seen := make(map[string]struct{}, len(declared))
	for _, query := range declared {
		// Whitespace and case are normalised before dedup: "Reimbursement limit" and
		// "reimbursement  limit" are one query, and retrieving both pays twice for
		// the same chunks.
		query = strings.Join(strings.Fields(query), " ")
		if query == "" {
			continue
		}
		if runes := []rune(query); len(runes) > reportResearchQueryMaxRunes {
			query = string(runes[:reportResearchQueryMaxRunes])
		}
		key := strings.ToLower(query)
		if _, dup := seen[key]; dup {
			continue
		}
		if len(queries) >= reportResearchQueryLimit {
			break
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
	}
	if len(queries) == 0 {
		return nil, false
	}
	return queries, true
}

// mergeReportEvidence appends the passages a planned query added, keeping the
// section's own retrieval first: its ranking is the one the writer would have seen
// at standard depth, so a deep section is a superset rather than a different report.
//
// Dedup is by chunk id, which is the citation's own identity — a duplicate kept
// would become a second citation row for one chunk, inflating `citation_count`.
func mergeReportEvidence(items []reportEvidenceItem, extra []reportEvidenceItem) (
	merged []reportEvidenceItem, added, duplicates int,
) {
	merged = items
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[reportEvidenceIdentity(item)] = struct{}{}
	}
	for _, item := range extra {
		identity := reportEvidenceIdentity(item)
		if _, dup := seen[identity]; dup {
			duplicates++
			continue
		}
		seen[identity] = struct{}{}
		merged = append(merged, item)
		added++
	}
	return merged, added, duplicates
}

// reportEvidenceIdentity is the citation identity of one passage. Chunk id is the
// primary key; a chunk that arrived without one (a build that omits `chunk_id`)
// falls back to document + content hash so it still cannot be cited twice.
func reportEvidenceIdentity(item reportEvidenceItem) string {
	if item.ChunkID != "" {
		return item.ChunkID
	}
	return item.DocumentID + "\x00" + item.ContentHash
}

// researchDepthOf normalises the revision's switch. Anything other than an explicit
// `deep` is `standard`: a typo must not silently buy more model calls.
func researchDepthOf(depth string) string {
	if strings.TrimSpace(depth) == model.ReportResearchDepthDeep {
		return model.ReportResearchDepthDeep
	}
	return model.ReportResearchDepthStandard
}

// researchDepthRequested validates the creation switch. An unknown word is a 400,
// not a silent `standard`: a typo that quietly bought fewer tokens would make the
// feature impossible to verify from the outside.
func researchDepthRequested(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return model.ReportResearchDepthStandard
	}
	if trimmed != model.ReportResearchDepthDeep && trimmed != model.ReportResearchDepthStandard {
		return ""
	}
	return trimmed
}

// researchDepthInherited is the regeneration rule: an empty request keeps the base's
// depth, an explicit value replaces it.
func researchDepthInherited(requested, base string) string {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return researchDepthOf(base)
	}
	if trimmed != model.ReportResearchDepthDeep && trimmed != model.ReportResearchDepthStandard {
		return ""
	}
	return trimmed
}

// planResearch runs the planner for one section and returns the queries to retrieve.
// A degraded result is returned with a nil error: the caller must write the section.
func (r *reportSectionRun) planResearch(section *model.ReportSection) ([]string, string) {
	now := time.Now().UTC()
	charge, err := r.svc.ReserveReportCharge(r.ctx, r.tenantID, ReportChargeCall{
		JobID: r.business.ID, SectionID: section.ID, AttemptSeq: r.attemptSeq,
		SliceSeq: r.sliceSeq, SectionAttempt: section.Attempt + 1,
		ReservedTokens: reportResearchPlannerReserve, ModelID: r.snapshot.Model.ModelID,
		RuntimeJobID: r.runtimeJobID,
	}, now)
	if err != nil {
		if reportCodeOf(err) == codeReportBudgetExhausted {
			logger.Info("report research planner skipped: budget exhausted",
				"report_job_id", r.business.ID, "section_key", section.SectionKey)
			return nil, reportResearchPlanningBudget
		}
		logger.Warn("report research planner could not reserve",
			"report_job_id", r.business.ID, "section_key", section.SectionKey, "error", err)
		return nil, reportResearchPlanningPlannerError
	}
	raw, callErr := r.svc.RAGFlow.ChatToModel(r.ctx,
		r.snapshot.Model.Provider, r.snapshot.Model.Instance, r.snapshot.Model.Name,
		reportResearchPlannerPrompt(r.report.Question, section.Title,
			r.sectionInstruction(section.SectionKey)), false, false)
	if callErr != nil {
		// Nothing billable came back: refund the reservation instead of writing it
		// down as unknown spend (same rule as a failed writer call).
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			logger.Warn("report research planner release failed",
				"report_job_id", r.business.ID, "ledger_key", charge.LedgerKey, "error", err)
		}
		logger.Warn("report research planner call failed",
			"report_job_id", r.business.ID, "section_key", section.SectionKey, "error", callErr)
		return nil, reportResearchPlanningPlannerError
	}
	reply, decodeErr := decodeReportPlannerReply(raw)
	if decodeErr != nil {
		if _, err := r.svc.ReleaseReportCharge(r.ctx, r.tenantID, r.business.ID, charge.LedgerKey, now); err != nil {
			logger.Warn("report research planner release failed",
				"report_job_id", r.business.ID, "ledger_key", charge.LedgerKey, "error", err)
		}
		logger.Warn("report research planner reply unreadable",
			"report_job_id", r.business.ID, "section_key", section.SectionKey, "error", decodeErr)
		return nil, reportResearchPlanningPlannerError
	}
	if !reply.UsageReported {
		reply.TokensIn = estimateReportTokens(reportResearchPlannerPrompt(r.report.Question,
			section.Title, r.sectionInstruction(section.SectionKey)))
		reply.TokensOut = estimateReportTokens(reply.Text)
	}
	if _, err := r.svc.SettleReportCharge(r.ctx, r.tenantID, r.business.ID,
		charge.LedgerKey, reply.TokensIn+reply.TokensOut, now); err != nil {
		// A settled planner is not worth failing a section over, but it must be
		// visible: the ledger now disagrees with the provider.
		logger.Warn("report research planner settle failed",
			"report_job_id", r.business.ID, "ledger_key", charge.LedgerKey, "error", err)
	}
	if detail, isEnvelope := reportModelErrorContent(reply.Text); isEnvelope {
		// An error envelope is not the planner's *shape* problem: the call failed, and
		// recording it as `fallback_unparseable` would blame the parser for a provider
		// outage — the same reason `decodeReportPlannerReply` exists.
		logger.Warn("report research planner answered with a provider error envelope",
			"report_job_id", r.business.ID, "section_key", section.SectionKey,
			"detail", detail)
		return nil, reportResearchPlanningPlannerError
	}
	queries, ok := parseReportResearchQueries(reply.Text)
	if !ok {
		return nil, reportResearchPlanningUnparseable
	}
	return queries, reportResearchPlanningPlanned
}

// gatherEvidence is the section's evidence step: its own retrieval, plus — at deep
// depth, for sections that must cite — the planned queries.
//
// The returned plan is always non-nil so the row and the UI have something to say
// even at standard depth. A planned-query retrieval failure is skipped per query:
// one narrow query that the engine could not answer must not cost the section its
// whole evidence set.
func (r *reportSectionRun) gatherEvidence(section *model.ReportSection) (
	[]reportEvidenceItem, bool, reportResearchPlan, error,
) {
	evidence, truncated, err := r.retrieve(section)
	if err != nil {
		// The historical retrieve failure: the section fails, exactly as it would
		// have before R18 existed.
		return nil, false, reportResearchPlan{
			Depth: researchDepthOf(r.depth()), Planning: reportResearchPlanningStandard,
		}, err
	}
	plan := reportResearchPlan{
		Depth: researchDepthOf(r.depth()), Planning: reportResearchPlanningStandard,
	}
	if plan.Depth != model.ReportResearchDepthDeep || !section.RequiresEvidence {
		return evidence, truncated, plan, nil
	}
	queries, planning := r.planResearch(section)
	plan.Planning = planning
	if len(queries) == 0 {
		return evidence, truncated, plan, nil
	}
	for _, query := range queries {
		extra, extraTruncated, err := r.retrieveQuery(section, query)
		if err != nil {
			logger.Warn("report research query failed",
				"report_job_id", r.business.ID, "section_key", section.SectionKey,
				"query", query, "error", err)
			plan.Queries = append(plan.Queries, reportResearchQuery{Query: query})
			continue
		}
		truncated = truncated || extraTruncated
		mergedEvidence, added, duplicates := mergeReportEvidence(evidence, extra)
		evidence = mergedEvidence
		plan.Merged += added
		plan.Duplicates += duplicates
		plan.Queries = append(plan.Queries, reportResearchQuery{Query: query, Hits: len(extra)})
	}
	// The union can exceed the prompt envelope; bounding it here keeps the writer's
	// input identical in shape to a standard section's.
	bounded, boundedTruncated := boundReportEvidence(evidence)
	return bounded, truncated || boundedTruncated, plan, nil
}

// depth reads the revision's frozen switch. It is frozen at creation (doc/295 §2.2),
// so a template or workspace change cannot deepen a report that is already running.
func (r *reportSectionRun) depth() string {
	if r.revision == nil {
		return model.ReportResearchDepthStandard
	}
	return r.revision.ResearchDepth
}

// researchPlanJSON encodes the plan for `research_plan_json`. An empty plan is ""
// so a reader can tell "no plan was attempted" from "a plan with no queries".
func researchPlanJSON(plan reportResearchPlan) string {
	if plan.Depth == "" && plan.Planning == "" {
		return ""
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// parseReportResearchPlan decodes a frozen plan for the read path. A malformed value
// reads as none: it is a disclosure, and a bad one must not hide the section body.
func parseReportResearchPlan(raw string) *reportResearchPlan {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var plan reportResearchPlan
	if err := json.Unmarshal([]byte(trimmed), &plan); err != nil {
		return nil
	}
	return &plan
}

// reportResearchPlanningLabel is the single place a plan state becomes words. An
// unknown state returns "" so a new state never leaks into the UI as a raw token.
func reportResearchPlanningLabel(state string) string {
	switch state {
	case reportResearchPlanningStandard:
		return "标准检索"
	case reportResearchPlanningPlanned:
		return "深研检索"
	case reportResearchPlanningPlannerError:
		return "深研降级（规划调用失败）"
	case reportResearchPlanningBudget:
		return "深研降级（预算不足）"
	case reportResearchPlanningUnparseable:
		return "深研降级（规划结果不可解析）"
	}
	return ""
}

// researchPlanNote is what the reader sees under the section: the depth, the state,
// and how much the extra queries actually added.
func researchPlanNote(plan *reportResearchPlan) string {
	if plan == nil {
		return ""
	}
	label := reportResearchPlanningLabel(plan.Planning)
	if label == "" {
		return ""
	}
	if plan.Planning != reportResearchPlanningPlanned {
		return label
	}
	return label + "（新增 " + strconv.Itoa(plan.Merged) + " 段，重复 " + strconv.Itoa(plan.Duplicates) + " 段）"
}

// reportResearchPlanLine is the exporter/reader wording for a section's frozen
// research plan. "" for a standard section: the three files must stay silent about a
// feature the run did not use, not print "标准检索" on every chapter.
func reportResearchPlanLine(plan *reportDocumentResearch) string {
	if plan == nil {
		return ""
	}
	label := reportResearchPlanningLabel(plan.Planning)
	if label == "" {
		return ""
	}
	if plan.Planning != reportResearchPlanningPlanned {
		return "深研状态：" + label
	}
	return "深研状态：" + label + "（新增 " + strconv.Itoa(plan.Merged) +
		" 段，重复 " + strconv.Itoa(plan.Duplicates) + " 段）"
}
