package service

// The review workbench read model (doc/301 §7.1 GET .../review, §7.3).
//
// One request must answer "what did the audit find, and can I ship this?" without
// the client stitching three endpoints together and inventing its own notion of
// readiness. So the projection carries the run's counters, the tier *and its basis*,
// the sections with their per-claim verdicts, and the findings — each of the three
// lists capped and cursor-paged rather than refused.
//
// The cap-and-cursor choice is deliberate (§7.1): a revision with 800 claims is
// normal, and answering 400 "too many claims" would make the workbench unusable on
// exactly the reports that need review most.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ReportReviewView is the workbench projection.
type ReportReviewView struct {
	ReportID   string `json:"report_id"`
	RevisionID string `json:"revision_id"`
	Revision   int64  `json:"revision"`
	Status     string `json:"revision_status"`

	Run *ReportAssuranceRunView `json:"run,omitempty"`
	// Eligibility and Basis are repeated on the top level because they are the two
	// fields the release banner reads. `Basis` is the list of failed conditions, so
	// the banner can say *why* a revision is not tier_1 instead of only that it is
	// not (§4.2).
	Eligibility string   `json:"release_eligibility"`
	Basis       []string `json:"release_eligibility_basis"`
	// RequestedPolicy is what the run was allowed to aim at; `required` here with
	// `eligibility=tier_1` is the honest v0.7.0 combination (§5.5).
	RequestedPolicy string `json:"requested_policy"`
	// Acknowledgeable marks a revision whose conflicts are known and disclosed:
	// exporting it needs an explicit `acknowledge_assurance` (§7.2). It is computed
	// server-side so the checkbox and the 44121 refusal cannot disagree.
	Acknowledgeable bool `json:"acknowledgeable"`
	Acknowledged    bool `json:"acknowledged"`

	Counters ReportReviewCounters `json:"counters"`

	Sections []ReportReviewSection `json:"sections"`
	// Findings are the open/acknowledged problems, newest severity first.
	Findings []*ReportReviewFindingView `json:"findings"`

	// ScanScope is the frozen contrast scope of this revision: `cited_only` or
	// `cited+candidate:K` (doc/301 §5.3.1). It is *required* disclosure — without it
	// "no conflicts found" reads as "cross-document contrast was performed and found
	// nothing", which is a much stronger claim than the run made.
	ScanScope string `json:"scan_scope"`
	// CandidateCount is how many uncited candidates were frozen for this revision. It
	// is a count on purpose: candidate *content* needs a second check against the
	// caller's current dataset permission, and v0.7.0 exposes the number without the
	// text (§5.3.1 ACL 读).
	CandidateCount int64 `json:"candidate_count"`
	// CandidatesVisible states *why* `candidates` is empty (doc/301 §9 反证候选面板):
	// the report owner sees the frozen excerpts, a share grantee sees the count and the
	// sentence "另有 N 条未引用候选参与对照". It is a field rather than an inference so
	// the panel cannot read "retention purged them" and "not your content" as the same
	// thing.
	CandidatesVisible bool `json:"candidates_visible"`
	// Candidates are the frozen pool of the sections in this response, owner only.
	// They never carry a chunk id or a score: a candidate is not evidence, and the
	// moment it has an identifier a renderer will sort it next to a citation (§5.3.1).
	Candidates []ReportReviewCandidate `json:"candidates"`
	// CandidatesTruncated says the pool was longer than the projection cap, so the
	// panel prints "前 N 条" instead of implying it showed the whole pool.
	CandidatesTruncated bool `json:"candidates_truncated,omitempty"`

	NextSectionCursor string `json:"next_section_cursor,omitempty"`
	NextFindingCursor string `json:"next_finding_cursor,omitempty"`
	// Truncated states which caps were hit, so a client that ignores cursors at
	// least knows it is looking at a partial answer instead of a complete one.
	Truncated []string `json:"truncated,omitempty"`
}

// ReportReviewCandidate is one frozen uncited candidate as the workbench may see it.
type ReportReviewCandidate struct {
	SectionOrd int `json:"section_ord"`
	// PoolRank is the retrieval order the candidate ranked at, which is the only
	// signal that makes "the runner-up source contradicted the report" readable.
	PoolRank int `json:"pool_rank"`
	// DocumentLabel is the human name, resolved from this revision's own citation
	// rows when the same document is cited somewhere else. Empty when the document is
	// not cited anywhere in the report — a label we cannot derive is left out instead
	// of being re-read from the live index, which could have been renamed or deleted
	// since (the same rule the citation appendix follows).
	DocumentLabel string `json:"document_label,omitempty"`
	Content       string `json:"content,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
}

// ReportReviewCounters is the run summary the header row renders.
type ReportReviewCounters struct {
	TotalClaims  int `json:"total_claims"`
	Denominator  int `json:"denominator"`
	Evaluated    int `json:"evaluated"`
	Supported    int `json:"supported"`
	Partially    int `json:"partially_supported"`
	Unsupported  int `json:"unsupported"`
	Conflicting  int `json:"conflicting"`
	Insufficient int `json:"insufficient_evidence"`
	Abstained    int `json:"abstained"`
	NotEvaluated int `json:"not_evaluated"`
	NotRequired  int `json:"not_required"`

	HighFindings int `json:"high_findings"`
	OpenFindings int `json:"open_findings"`

	// NotRequiredShare is nullable: an empty revision has no share, and printing
	// 0.000 would read as "no claims were exempt" (§4.3).
	NotRequiredShare *float64 `json:"not_required_share"`
	DenominatorSmall bool     `json:"denominator_small"`
	Coverage         *float64 `json:"coverage"`
}

// ReportReviewSection is one section of the projection.
type ReportReviewSection struct {
	Ord              int    `json:"ord"`
	SectionKey       string `json:"section_key"`
	Title            string `json:"title"`
	RequiresEvidence bool   `json:"requires_evidence"`
	// EvidenceStatus is the section-level roll-up the report already stores; it is
	// echoed rather than recomputed so the workbench and the document agree.
	EvidenceStatus string `json:"evidence_status,omitempty"`
	// PoolHitChunks / PoolInsufficient / PoolCandidateCount are the *writing-time*
	// pool (doc/301 §5.8 item 1), copied from the section row. They are pointers
	// because `NULL` means "this revision never recorded it" and `0` means "the pool
	// was empty": a reviewer deciding whether a weak claim is the writer's fault or
	// the corpus's needs that distinction, and a zeroed field would erase it.
	PoolHitChunks      *int  `json:"pool_hit_chunks,omitempty"`
	PoolInsufficient   *bool `json:"pool_insufficient,omitempty"`
	PoolCandidateCount *int  `json:"pool_candidate_count,omitempty"`
	// ResearchPlanning is the frozen plan state (`standard` / `planned` /
	// `fallback_*`). §5.8 item 2 shows the degradation where the reviewer already
	// looks, instead of leaving it in a JSON blob only the export renders.
	ResearchPlanning string `json:"research_planning,omitempty"`
	ResearchDepth    string `json:"research_depth,omitempty"`

	Claims []ReportReviewClaim `json:"claims"`
	// ClaimCursor is the cursor of the last claim in this section, used when a
	// single section is itself too long to return whole.
	NextClaimCursor string `json:"next_claim_cursor,omitempty"`
}

// ReportReviewClaim is one claim with its verdict.
type ReportReviewClaim struct {
	ID           string `json:"id"`
	Ord          int    `json:"ord"`
	ClaimText    string `json:"claim_text"`
	ClaimType    string `json:"claim_type"`
	Verdict      string `json:"support_verdict"`
	QuoteStatus  string `json:"quote_status"`
	Structure    string `json:"structure_verdict,omitempty"`
	Relevance    string `json:"relevance,omitempty"`
	Rationale    string `json:"rationale,omitempty"`
	QuoteText    string `json:"quote_text,omitempty"`
	QuoteSpanEnd int    `json:"quote_span_end,omitempty"`
	// SnapshotID is what the UI highlights inside; `MatchedText` alone cannot point
	// at a document.
	SnapshotID   string   `json:"matched_snapshot_id,omitempty"`
	Abstain      string   `json:"abstain_reason,omitempty"`
	NotEvaluated string   `json:"not_evaluated_reason,omitempty"`
	Insufficient string   `json:"insufficient_reason,omitempty"`
	Confidence   *float64 `json:"confidence,omitempty"`
	CacheHit     bool     `json:"cache_hit"`
	// FindingID lets the claim row open the matching finding without a join on the
	// client, which would need the same "which finding is about this claim" rule
	// this function already implements.
	FindingIDs []string `json:"finding_ids,omitempty"`
}

// reviewCursor encodes a position in the review projection.
//
// It is `(section_ord, claim_ord)` and never an offset: an offset would shift when a
// section is re-read after a partial retry, and the client would silently skip or
// repeat rows (the "page 3 of 2" defect in a different costume).
func reviewCursor(sectionOrd, claimOrd int) string {
	if sectionOrd <= 0 && claimOrd <= 0 {
		return ""
	}
	return strconv.Itoa(sectionOrd) + ":" + strconv.Itoa(claimOrd)
}

func parseReviewCursor(raw string) (int, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return 0, 0, httperr.New(400, codeReportOutlineInvalid,
			"section_cursor must look like <section_ord>:<claim_ord>")
	}
	sectionOrd, err := strconv.Atoi(parts[0])
	if err != nil || sectionOrd < 0 {
		return 0, 0, httperr.New(400, codeReportOutlineInvalid, "section_cursor section is not a number")
	}
	claimOrd, err := strconv.Atoi(parts[1])
	if err != nil || claimOrd < 0 {
		return 0, 0, httperr.New(400, codeReportOutlineInvalid, "section_cursor claim is not a number")
	}
	return sectionOrd, claimOrd, nil
}

// reviewScanScope answers "what did this revision's review compare against".
//
// The number comes from the frozen snapshot and the count from the frozen rows, so
// the pair can only disagree if the janitor already deleted candidates — and then the
// truthful answer is the *rows we still have*, with the scope still naming what was
// attempted. A failure to count is not fatal: the scope is the disclosure, the count
// is the detail.
func (s *Service) reviewScanScope(
	ctx context.Context, tenantID string, revision *model.ReportRevision,
) (string, int64) {
	width := reportFrozenScanTopK(ctx, s, revision)
	count, err := s.Store.CountConflictCandidates(ctx, tenantID, revision.ID)
	if err != nil {
		logger.Warn("report candidate count failed",
			"tenant_id", tenantID, "revision_id", revision.ID, "error", err)
		count = 0
	}
	if width <= 0 {
		return "cited_only", count
	}
	return "cited+candidate:" + itoa(width), count
}

// revisionScanScope is the frozen contrast scope of a revision, as the data pair
// behind `reviewScanScope`. It exists so the export path can state the same fact
// without a fake read model.
func (s *Service) revisionScanScope(
	ctx context.Context, tenantID string, revision *model.ReportRevision,
) (string, int64) {
	if revision == nil {
		return "", 0
	}
	return s.reviewScanScope(ctx, tenantID, revision)
}

// renderScanScope turns the scope into the sentence a reader can act on, *without* a
// label: each renderer prints its own ("复核范围：" in prose, a `<dt>复核范围</dt>` pair
// in HTML, a cell in XLSX), and a sentence that carried the label would print it twice
// in two of the five formats.
//
// It returns "" for a revision that predates the field: silence is not the same claim
// as `cited_only`, and an old file must not acquire a scope it never had.
func renderScanScope(scanScope string, count int64) string {
	switch {
	case scanScope == "":
		return ""
	case scanScope == "cited_only":
		return "仅引用证据（本次未做跨文档冲突对照）"
	default:
		// `cited+candidate:K` (or any future widening): state the width and the fact
		// that candidates never enter the evidence chain.
		return fmt.Sprintf("引用证据 + 生成时冻结的未引用候选 %d 条（候选不入证据链）", count)
	}
}

// ReportReview reads the workbench projection of one revision.
//
// `runID` empty means "the newest run of this revision": the workbench opens on
// whatever the latest audit said, and a reviewer who wants an older run passes its
// id explicitly (that is the reproducibility affordance of D5).
func (s *Service) ReportReview(
	ctx context.Context, actorID, tenantID, reportID string, revisionNo int64,
	runID, sectionCursor, findingCursor string,
) (*ReportReviewView, error) {
	revision, err := s.assuranceRevisionForRead(ctx, actorID, tenantID, reportID, revisionNo)
	if err != nil {
		return nil, err
	}
	fromSection, fromClaim, err := parseReviewCursor(sectionCursor)
	if err != nil {
		return nil, err
	}
	view := &ReportReviewView{
		ReportID: reportID, RevisionID: revision.ID, Revision: revision.Revision,
		Status: revision.Status,
		// A revision with no run is `pending`, never `tier_0`: no measurement has
		// happened, so nothing has been established (§5.5).
		Eligibility:     model.ReportAssuranceEligibilityPending,
		Basis:           []string{"not_run"},
		RequestedPolicy: model.ReportAssurancePolicyAdvisory,
	}

	// Disclosure before measurement: the scope is a property of the *revision* (it was
	// frozen when the report was written), so it is answered even for a revision with
	// no run at all. Reading it from the run would leave a pending revision unable to
	// say whether contrast was ever in scope.
	view.ScanScope, view.CandidateCount = s.reviewScanScope(ctx, tenantID, revision)
	view.Candidates = []ReportReviewCandidate{}
	report, err := s.Store.GetReport(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	// The owner gate is on *this report*, not on the actor's role: a grantee with
	// `execute:report` may review, but the uncited pool is corpus content their
	// sharing grant does not carry (§5.3.1). A missing report row would already have
	// failed `assuranceRevisionForRead`, so `report == nil` here is not reachable.
	owner := report != nil && report.OwnerUserID == actorID
	view.CandidatesVisible = owner

	run, err := s.reviewRunOf(ctx, tenantID, revision, runID)
	if err != nil {
		return nil, err
	}
	evaluations := []model.ReportClaimEvaluation{}
	if run != nil {
		view.Run = assuranceRunViewOf(run)
		view.RequestedPolicy = run.RequestedPolicy
		view.Eligibility = run.ReleaseEligibility
		view.Basis = reviewEligibilityBasis(run)
		rows, err := s.Store.ListClaimEvaluations(ctx, tenantID, run.ID)
		if err != nil {
			return nil, err
		}
		evaluations = rows
	}

	sections, err := s.Store.ListReportSections(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	claims, err := s.Store.ListReportClaims(ctx, tenantID, revision.ID)
	if err != nil {
		return nil, err
	}
	// The workbench shows every finding with its own status; filtering is the
	// client's job because "open only" would hide the resolution history that makes
	// a dismissal auditable. The limit is the projection cap, and a longer list is
	// paged by `next_finding_cursor` (§7.1).
	findings, err := s.Store.ListReviewFindings(ctx, tenantID, repository.ReviewFindingFilter{
		ReportID: reportID, RevisionID: revision.ID,
		Limit: reportAssuranceReviewFindingLimit + 1,
	})
	if err != nil {
		return nil, err
	}

	evaluationByClaim := make(map[string]model.ReportClaimEvaluation, len(evaluations))
	for _, evaluation := range evaluations {
		evaluationByClaim[evaluation.ClaimID] = evaluation
	}
	findingsByClaim := map[string][]string{}
	for _, finding := range findings {
		if finding.ClaimID == nil {
			continue
		}
		findingsByClaim[*finding.ClaimID] = append(findingsByClaim[*finding.ClaimID], finding.ID)
	}

	view.Counters = reviewCountersOf(evaluations, findings)
	view.Acknowledgeable, view.Acknowledged = reviewAckState(revision, findings)

	// Sections are paged by `section_ord`, claims inside a section by `ord`. The
	// caps are per response, not per section, so one enormous section cannot blow
	// the budget by itself; its remaining claims are handed back as a cursor.
	claimsBySection := make(map[int][]model.ReportClaim, len(sections))
	for _, claim := range claims {
		claimsBySection[claim.SectionOrd] = append(claimsBySection[claim.SectionOrd], claim)
	}
	returnedClaims := 0
	sectionsReturned := 0
	for _, section := range sections {
		if section.Ord < fromSection {
			continue
		}
		if sectionsReturned >= reportAssuranceReviewSectionLimit ||
			returnedClaims >= reportAssuranceReviewClaimLimit {
			view.NextSectionCursor = reviewCursor(section.Ord, 0)
			view.Truncated = appendUnique(view.Truncated, "sections")
			break
		}
		entry := ReportReviewSection{
			Ord: section.Ord, SectionKey: section.SectionKey, Title: section.Title,
			RequiresEvidence: section.RequiresEvidence, EvidenceStatus: section.EvidenceStatus,
			PoolHitChunks: section.PoolHitChunks, PoolInsufficient: section.PoolInsufficient,
			PoolCandidateCount: section.PoolCandidateCount,
		}
		if plan := parseReportResearchPlan(section.ResearchPlanJSON); plan != nil {
			entry.ResearchPlanning = plan.Planning
			entry.ResearchDepth = plan.Depth
		}
		sectionClaims := claimsBySection[section.Ord]
		claimFloor := 0
		if section.Ord == fromSection {
			claimFloor = fromClaim
		}
		for _, claim := range sectionClaims {
			if claim.Ord < claimFloor {
				continue
			}
			if returnedClaims >= reportAssuranceReviewClaimLimit {
				entry.NextClaimCursor = reviewCursor(section.Ord, claim.Ord)
				view.NextSectionCursor = reviewCursor(section.Ord, claim.Ord)
				view.Truncated = appendUnique(view.Truncated, "claims")
				break
			}
			entry.Claims = append(entry.Claims, reviewClaimOf(claim, evaluationByClaim[claim.ID],
				findingsByClaim[claim.ID]))
			returnedClaims++
		}
		view.Sections = append(view.Sections, entry)
		sectionsReturned++
	}
	if view.Sections == nil {
		view.Sections = []ReportReviewSection{}
	}

	// Findings are ordered oldest-first by the repository; the projection reverses
	// by severity so the reviewer sees the blocking ones without scrolling. The cap
	// is a cursor, never a silent cut.
	ordered := orderReviewFindings(findings)
	start := 0
	if cursor := strings.TrimSpace(findingCursor); cursor != "" {
		for index, finding := range ordered {
			if finding.ID == cursor {
				start = index + 1
				break
			}
		}
	}
	for index := start; index < len(ordered); index++ {
		if index-start >= reportAssuranceReviewFindingLimit {
			view.NextFindingCursor = ordered[index].ID
			view.Truncated = appendUnique(view.Truncated, "findings")
			break
		}
		view.Findings = append(view.Findings, reviewFindingViewOf(&ordered[index]))
	}
	if view.Findings == nil {
		view.Findings = []*ReportReviewFindingView{}
	}
	if owner && view.CandidateCount > 0 {
		s.attachReviewCandidates(ctx, tenantID, revision.ID, view)
	}
	return view, nil
}

// reportAssuranceReviewCandidateLimit caps the candidate pool in one projection. A
// revision freezes at most `candidate_scan_top_k` (≤20) per section, so the cap is
// generous for a normal report and still bounded for a pathological one.
const reportAssuranceReviewCandidateLimit = 200

// attachReviewCandidates fills the owner-visible candidate pool of a revision.
//
// The pool is read for the *revision*, not for the returned sections: a candidate's
// `section_ord` is on the row, so the panel groups it beside its section, and a pool
// whose section row was purged still appears instead of turning into a count that
// nothing can explain. One query, bounded by the cap.
//
// A failed read leaves the pool empty and logs; the count already told the reader the
// contrast happened, and a workbench that fails to open because a list query failed
// would hide the whole audit.
func (s *Service) attachReviewCandidates(
	ctx context.Context, tenantID, revisionID string, view *ReportReviewView,
) {
	labels := map[string]string{}
	if citations, err := s.Store.ListReportCitations(ctx, tenantID, revisionID); err == nil {
		for _, citation := range citations {
			if citation.SourceDocumentID == "" || citation.DocumentLabel == "" {
				continue
			}
			labels[citation.SourceDocumentID] = citation.DocumentLabel
		}
	} else {
		logger.Warn("report review: citation labels unavailable",
			"tenant_id", tenantID, "revision_id", revisionID, "error", err)
	}
	rows, err := s.Store.ListConflictCandidatesByRevision(
		ctx, tenantID, revisionID, reportAssuranceReviewCandidateLimit+1)
	if err != nil {
		logger.Warn("report review: candidate read failed",
			"tenant_id", tenantID, "revision_id", revisionID, "error", err)
		return
	}
	if len(rows) > reportAssuranceReviewCandidateLimit {
		rows = rows[:reportAssuranceReviewCandidateLimit]
		view.CandidatesTruncated = true
	}
	for _, row := range rows {
		view.Candidates = append(view.Candidates, ReportReviewCandidate{
			SectionOrd: row.SectionOrd, PoolRank: row.PoolRank,
			DocumentLabel: labels[row.DocumentID],
			Content:       row.Content, Truncated: row.Truncated,
		})
	}
}

// reviewRunOf resolves which run the projection reads.
func (s *Service) reviewRunOf(
	ctx context.Context, tenantID string, revision *model.ReportRevision, runID string,
) (*model.ReportAssuranceRun, error) {
	if strings.TrimSpace(runID) != "" {
		run, err := s.Store.GetAssuranceRun(ctx, tenantID, runID)
		if err != nil {
			return nil, err
		}
		if run == nil || run.RevisionID != revision.ID {
			return nil, errReportNotFound()
		}
		return run, nil
	}
	if revision.AssuranceRunID == "" {
		return nil, nil
	}
	run, err := s.Store.GetAssuranceRun(ctx, tenantID, revision.AssuranceRunID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.RevisionID != revision.ID {
		// The projection points at a purged run: answer with no run rather than a
		// broken banner. `release_eligibility` then falls back to `pending`, which is
		// the honest reading of "we no longer have the measurement".
		return nil, nil
	}
	return run, nil
}

// reviewEligibilityBasis decodes the stored failure list. A malformed blob yields a
// generic basis rather than an empty one: "we do not know why" must not render as
// "nothing failed".
func reviewEligibilityBasis(run *model.ReportAssuranceRun) []string {
	if strings.TrimSpace(run.MetricsJSON) == "" {
		return []string{"no_metrics"}
	}
	var payload struct {
		Basis json.RawMessage `json:"release_eligibility_basis"`
	}
	if err := json.Unmarshal([]byte(run.MetricsJSON), &payload); err != nil {
		return []string{"metrics_unreadable"}
	}
	var basis []string
	if err := json.Unmarshal(payload.Basis, &basis); err != nil {
		return []string{"metrics_unreadable"}
	}
	if basis == nil {
		return []string{}
	}
	return basis
}

// reviewCountersOf builds the header counters. Coverage and the share are
// recomputed from the evaluation rows rather than read from `metrics_json`: the
// rows are the record, the JSON is a snapshot, and a workbench that shows a number
// the rows cannot reproduce is showing a rumour.
func reviewCountersOf(
	evaluations []model.ReportClaimEvaluation, findings []model.ReportReviewFinding,
) ReportReviewCounters {
	counters := ReportReviewCounters{TotalClaims: len(evaluations)}
	for _, evaluation := range evaluations {
		switch evaluation.SupportVerdict {
		case model.ReportVerdictSupported:
			counters.Supported++
		case model.ReportVerdictPartiallySupported:
			counters.Partially++
		case model.ReportVerdictUnsupported:
			counters.Unsupported++
		case model.ReportVerdictConflicting:
			counters.Conflicting++
		case model.ReportVerdictInsufficient:
			counters.Insufficient++
		case model.ReportVerdictAbstained:
			counters.Abstained++
		case model.ReportVerdictNotEvaluated:
			counters.NotEvaluated++
		case model.ReportVerdictNotRequired:
			counters.NotRequired++
		}
		if model.ReportVerdictScored(evaluation.SupportVerdict) {
			counters.Evaluated++
		}
	}
	counters.Denominator = counters.TotalClaims - counters.NotRequired
	counters.Coverage = ratioPtr(counters.Evaluated, counters.Denominator)
	counters.NotRequiredShare = ratioPtr(counters.NotRequired, counters.TotalClaims)
	if counters.NotRequiredShare != nil && *counters.NotRequiredShare > reportAssuranceNotRequiredCeiling {
		counters.DenominatorSmall = true
	}
	for _, finding := range findings {
		if finding.Status == model.ReportFindingStatusOpen ||
			finding.Status == model.ReportFindingStatusAcknowledged {
			counters.OpenFindings++
		}
		if finding.Severity == model.ReportSeverityHigh &&
			finding.Status != model.ReportFindingStatusResolved &&
			finding.Status != model.ReportFindingStatusDismissed {
			counters.HighFindings++
		}
	}
	return counters
}

// reviewClaimOf renders one claim row.
func reviewClaimOf(
	claim model.ReportClaim, evaluation model.ReportClaimEvaluation, findingIDs []string,
) ReportReviewClaim {
	row := ReportReviewClaim{
		ID: claim.ID, Ord: claim.Ord, ClaimText: claim.ClaimText, ClaimType: claim.ClaimType,
		FindingIDs: findingIDs,
	}
	if evaluation.ID == "" {
		// No run covered this claim (or the run had no row for it): say
		// `not_evaluated`, not `unsupported`. A missing measurement is not a verdict.
		row.Verdict = model.ReportVerdictNotEvaluated
		row.NotEvaluated = model.ReportNotEvaluatedTrigger
		return row
	}
	row.Verdict = evaluation.SupportVerdict
	row.QuoteStatus = evaluation.QuoteStatus
	row.Structure = evaluation.StructureVerdict
	row.Relevance = evaluation.Relevance
	row.Rationale = evaluation.Rationale
	row.QuoteText = evaluation.QuoteText
	row.QuoteSpanEnd = evaluation.QuoteSpanEnd
	row.SnapshotID = evaluation.MatchedSnapshotID
	row.Abstain = evaluation.AbstainReason
	row.NotEvaluated = evaluation.NotEvaluatedReason
	row.Insufficient = evaluation.InsufficientReason
	row.Confidence = evaluation.Confidence
	row.CacheHit = evaluation.CacheHit
	return row
}

// reviewAckState decides the two flags the export gate reads (§7.2).
//
// `Acknowledged` is true when the *previous* export of this revision recorded an
// acknowledgement while the conflict set has not changed since. Comparing against
// the current conflict count rather than a boolean is what makes a new run
// re-open the gate: a fresh conflict must not inherit an old "I know".
func reviewAckState(revision *model.ReportRevision, findings []model.ReportReviewFinding) (bool, bool) {
	conflicts := 0
	for _, finding := range findings {
		if finding.Status == model.ReportFindingStatusResolved ||
			finding.Status == model.ReportFindingStatusDismissed {
			continue
		}
		if finding.Category == model.ReportFindingContradiction ||
			finding.Category == model.ReportFindingSourceConflict ||
			finding.Category == model.ReportFindingUnsupported ||
			finding.Category == model.ReportFindingQuoteUnverifiable {
			conflicts++
		}
	}
	if conflicts == 0 {
		return false, false
	}
	acknowledged := false
	if strings.TrimSpace(revision.AssuranceAckJSON) != "" {
		var ack struct {
			Conflicts int `json:"conflicts"`
		}
		if err := json.Unmarshal([]byte(revision.AssuranceAckJSON), &ack); err == nil {
			acknowledged = ack.Conflicts >= conflicts
		}
	}
	return true, acknowledged
}

// appendUnique keeps a stable, duplicate-free list. `Truncated` is a set of
// section/claim/finding caps, and naming one twice would read like two different
// cuts.
func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// orderReviewFindings sorts high severity first, then oldest, so a stable read
// keeps its position between pages.
func orderReviewFindings(rows []model.ReportReviewFinding) []model.ReportReviewFinding {
	ordered := append([]model.ReportReviewFinding(nil), rows...)
	rank := func(severity string) int {
		switch severity {
		case model.ReportSeverityHigh:
			return 0
		case model.ReportSeverityMedium:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		a, b := ordered[left], ordered[right]
		if rank(a.Severity) != rank(b.Severity) {
			return rank(a.Severity) < rank(b.Severity)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	return ordered
}

// reportRevisionAckPayload is what `acknowledge_assurance=true` writes onto the
// revision. It records *how many* conflicts were accepted, so a later run with more
// conflicts is a new decision rather than a silent continuation.
func reportRevisionAckPayload(conflicts int, actorID string, now time.Time) string {
	payload, err := json.Marshal(map[string]interface{}{
		"conflicts": conflicts, "acknowledged_by": actorID, "acknowledged_at": now,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}
