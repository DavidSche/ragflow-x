package service

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// Section-level reuse across revisions (doc/288 §7.4 / §9.8 / R14).
//
// A regenerated revision normally rewrites every section. R14 lets the owner name
// the sections that should *not* be rewritten — the ones whose source material did
// not change, or whose wording was already approved. Those sections inherit the
// base revision's body and citation rows, are marked `rewrite_reason=reuse`, and
// point at the base version through `based_on_section_version`, so a reader can
// tell "this sentence was written for an earlier version and carried forward" from
// "the model happened to produce the same words again".
//
// Three properties make this safe to do in an asynchronous job:
//
//   - the plan is frozen on the revision row (`reuse_plan_json`) once, at creation
//     time, and never rewritten. A retried slice therefore cannot inherit a
//     different set than the one the caller asked for, and "was this reused?" is a
//     stored fact rather than a guess about the output;
//   - the copy is a real copy (own section row, own citation rows, own claim rows
//     pointing at the *same* evidence snapshots). Sharing pointers across revisions
//     would make a later retry of the old revision silently change the new one;
//   - reuse is droppable. If the base section is gone, was never written, or a
//     cited snapshot has expired, `ReuseReportSection` declines (`applied=false`)
//     and the slice writes the section normally. A missing reuse is a smaller lie
//     than a report whose appendix cannot be exported.

// reportReusePlan is the stored plan. It is a struct rather than a bare list so a
// later addition (a per-section reason, a "reuse only if unchanged" flag) can land
// without a second column.
type reportReusePlan struct {
	Sections []int `json:"sections"`
}

// reportReuseMaxSections bounds the plan at the outline size the template can
// declare. A larger list cannot come from a real outline, so accepting it would
// only let a caller spend the job's time on ords that do not exist.
const reportReuseMaxSections = 64

// parseReportReusePlan reads the frozen plan. An unparsable plan is treated as
// "nothing to reuse": the sections then get written normally, which is always a
// correct (if more expensive) answer.
func parseReportReusePlan(raw string) reportReusePlan {
	if raw == "" {
		return reportReusePlan{}
	}
	var plan reportReusePlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		logger.Warn("report reuse plan is not readable", "error", err)
		return reportReusePlan{}
	}
	return plan
}

// normalizeReportReuseSections validates a caller's reuse request against the base
// revision's outline.
//
// It answers with the ords in outline order, deduped. An ord that names a section
// the base never wrote is a bad request, not a silent no-op: the caller asked for
// something specific, and quietly rewriting that section anyway would look like
// reuse worked.
func normalizeReportReuseSections(ords []int, base []model.ReportSection) ([]int, error) {
	if len(ords) == 0 {
		return nil, nil
	}
	if len(ords) > reportReuseMaxSections {
		return nil, errReportOutlineInvalid("reuse_sections names more sections than a report can have")
	}
	written := make(map[int]struct{}, len(base))
	for _, section := range base {
		if section.Status == model.ReportSectionSucceeded {
			written[section.Ord] = struct{}{}
		}
	}
	seen := make(map[int]struct{}, len(ords))
	normalized := make([]int, 0, len(ords))
	for _, ord := range ords {
		if ord < 1 {
			return nil, errReportOutlineInvalid("reuse_sections must be positive section numbers")
		}
		if _, ok := written[ord]; !ok {
			return nil, errReportOutlineInvalid("reuse_sections names a section the base revision did not deliver")
		}
		if _, ok := seen[ord]; ok {
			continue
		}
		seen[ord] = struct{}{}
		normalized = append(normalized, ord)
	}
	sort.Ints(normalized)
	return normalized, nil
}

// reportReusePlanJSON encodes the plan for storage. An empty plan stores the empty
// string, so "no plan" reads back as "not reused" rather than as an empty list to be
// distinguished from an absent column.
func reportReusePlanJSON(ords []int) (string, error) {
	if len(ords) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(reportReusePlan{Sections: ords})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// applyReportSectionReuse inherits the planned sections before the slice writes
// anything. It returns how many sections were actually filled in.
//
// Called from the slice loop rather than at creation because the sections only
// exist as `pending` rows after the outline is confirmed: there is nothing to fill
// in earlier, and doing it later would race the writer that already started.
func (s *Service) applyReportSectionReuse(
	ctx context.Context, tenantID, jobID, stage string,
	revision *model.ReportRevision, sections []model.ReportSection, now time.Time,
) (int, error) {
	plan := parseReportReusePlan(revision.ReusePlanJSON)
	if len(plan.Sections) == 0 || revision.BasedOnRevision <= 0 {
		return 0, nil
	}
	base, err := s.Store.GetReportRevision(ctx, tenantID, revision.ReportID, revision.BasedOnRevision)
	if err != nil {
		return 0, err
	}
	if base == nil {
		// The base revision was purged between creation and the first slice. Nothing
		// to inherit; every section is written from scratch.
		logger.Warn("report reuse skipped: base revision is gone",
			"report_revision_id", revision.ID, "based_on_revision", revision.BasedOnRevision)
		return 0, nil
	}
	baseSections, err := s.Store.ListReportSections(ctx, tenantID, base.ID)
	if err != nil {
		return 0, err
	}
	baseByOrd := make(map[int]model.ReportSection, len(baseSections))
	for _, section := range baseSections {
		baseByOrd[section.Ord] = section
	}
	baseCitations, err := s.Store.ListReportCitations(ctx, tenantID, base.ID)
	if err != nil {
		return 0, err
	}
	baseClaims, err := s.Store.ListReportClaims(ctx, tenantID, base.ID)
	if err != nil {
		return 0, err
	}
	citationsBySection := make(map[string][]model.ReportCitation, len(baseSections))
	for _, citation := range baseCitations {
		citationsBySection[citation.SectionID] = append(citationsBySection[citation.SectionID], citation)
	}
	claimsBySection := make(map[string][]model.ReportClaim, len(baseSections))
	for _, claim := range baseClaims {
		claimsBySection[claim.SectionID] = append(claimsBySection[claim.SectionID], claim)
	}
	liveByOrd := make(map[int]model.ReportSection, len(sections))
	for _, section := range sections {
		liveByOrd[section.Ord] = section
	}

	applied := 0
	for _, ord := range plan.Sections {
		target, ok := liveByOrd[ord]
		if !ok || target.Status != model.ReportSectionPending {
			// A retried slice may run this after the section was already written
			// (or after a retry replaced it). Filling it in again is not a reuse.
			continue
		}
		source, ok := baseByOrd[ord]
		if !ok || source.Status != model.ReportSectionSucceeded {
			continue
		}
		citations, claims := reportReuseRows(
			tenantID, revision.ID, target, source,
			citationsBySection[source.ID], claimsBySection[source.ID],
		)
		ok, err := s.Store.ReuseReportSection(ctx, repository.ReportSectionReuse{
			TenantID: tenantID, JobID: jobID, RevisionID: revision.ID,
			SectionID: target.ID, BaseSectionID: source.ID, Now: now, Stage: stage,
			Citations: citations, Claims: claims,
		})
		if err != nil {
			return applied, err
		}
		if ok {
			applied++
		}
	}
	if applied > 0 {
		logger.Info("report sections reused from the base revision",
			"report_revision_id", revision.ID, "based_on_revision", revision.BasedOnRevision,
			"planned", len(plan.Sections), "applied", applied)
	}
	return applied, nil
}

// reportReuseRows re-keys the base section's citation and claim rows onto the new
// section.
//
// The ids are derived from `(new section id, ord)`, which is the tables' unique
// index: a reuse replayed after a transport failure collides instead of writing a
// second copy of the same reference. `evidence_snapshot_id` is deliberately *not*
// re-minted — a snapshot is an immutable observation, and two revisions citing the
// same chunk must point at the same rows or the appendix doubles.
//
// `claim_id` on the citation is remapped through the new claim ids; leaving the base
// id in place would point a new citation at a claim the reader cannot see this
// revision.
func reportReuseRows(
	tenantID, revisionID string, target, source model.ReportSection,
	citations []model.ReportCitation, claims []model.ReportClaim,
) ([]model.ReportCitation, []model.ReportClaim) {
	claimIDs := make(map[string]string, len(claims))
	claimRows := make([]model.ReportClaim, 0, len(claims))
	for _, claim := range claims {
		row := model.ReportClaim{
			ID:       reportRowID("claim", target.ID, strconv.Itoa(claim.Ord)),
			TenantID: tenantID, RevisionID: revisionID, SectionID: target.ID,
			SectionOrd: target.Ord, SectionVersion: target.SectionVersion,
			Ord: claim.Ord, ClaimText: claim.ClaimText,
			ClaimType: claim.ClaimType, SupportStatus: claim.SupportStatus,
		}
		claimIDs[claim.ID] = row.ID
		claimRows = append(claimRows, row)
	}
	citationRows := make([]model.ReportCitation, 0, len(citations))
	for _, citation := range citations {
		row := citation
		row.ID = reportRowID("citation", target.ID, strconv.Itoa(citation.Ord))
		row.TenantID = tenantID
		row.RevisionID = revisionID
		row.SectionID = target.ID
		row.SectionOrd = target.Ord
		row.SectionVersion = target.SectionVersion
		row.ClaimID = claimIDs[citation.ClaimID]
		citationRows = append(citationRows, row)
	}
	return citationRows, claimRows
}
