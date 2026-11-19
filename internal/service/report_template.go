package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Report content template (doc/288 §11.1). This is the *content* template —
// sections, prompts, budget, retrieval policy. The rendering template is
// `rgx_export_template_version`; folding the two together would make "edit what
// a compliance report says" and "edit how a docx looks" the same permission on
// the same lifecycle (doc/288 §7.2).

// ReportTemplateName is the bilingual display name. A single-language field
// would force the UI to invent a fallback string, and invented strings are what
// the catalogue exists to replace.
type ReportTemplateName struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

// ReportTemplateSection declares one section of the report body.
type ReportTemplateSection struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Prompt is the section-specific instruction. It is a *question shape*, not
	// an answer, so the same template can serve any knowledge base.
	Prompt string `json:"prompt"`
	// RequiresEvidence marks sections whose claims must carry citations. At
	// least one section must require evidence, otherwise the report reduces to
	// an opinion piece with a compliance cover page.
	RequiresEvidence bool `json:"requires_evidence"`
	MaxWords         int  `json:"max_words"`
	// DependsOn orders the outline: a section may only be written after the
	// sections it depends on, which is what makes "先结论后依据" enforceable
	// instead of a prompt suggestion.
	DependsOn []string `json:"depends_on,omitempty"`
}

// ReportTemplateLimits is the budget envelope. `SliceMaxSeconds` is capped
// against the runtime job timeout: a slice that outlives its runtime job can
// never checkpoint, so the lease expires and the work is redone forever
// (doc/288 §7.9).
type ReportTemplateLimits struct {
	MaxSections         int `json:"max_sections"`
	SliceMaxSeconds     int `json:"slice_max_seconds"`
	TotalMaxSeconds     int `json:"total_max_seconds"`
	MaxSlices           int `json:"max_slices"`
	MaxTokensPerSection int `json:"max_tokens_per_section"`
}

// ReportTemplateRetrieval is the per-section retrieval policy. The threshold
// itself is deliberately NOT part of the template: an absolute similarity value
// drifts with engine and embedding model and a stale one silently produces an
// empty report (doc/288 §9.4). The template declares the *policy* and the
// minimum evidence count; the resolved value is frozen into the revision's
// execution snapshot instead.
type ReportTemplateRetrieval struct {
	TopK            int    `json:"top_k"`
	ThresholdPolicy string `json:"threshold_policy"`
	MinEvidence     int    `json:"min_evidence"`
}

// ReportTemplateEvidencePolicy controls how unsupported, conflicting and
// outdated evidence is disclosed (doc/288 §9.5).
type ReportTemplateEvidencePolicy struct {
	ConflictHandling  string `json:"conflict_handling"`
	OutdatedEvidence  string `json:"outdated_evidence"`
	UnsupportedClaims string `json:"unsupported_claims"`
}

// ReportTemplatePayload is the stored template body.
type ReportTemplatePayload struct {
	Name ReportTemplateName `json:"name"`
	// OutputFormats is the closed set of formats a template *advertises*: docx, md,
	// pdf, html and xlsx. A template that omits one simply does not advertise it —
	// this list is a declaration, not an authorization gate: the export endpoint
	// accepts every format in `validReportExportFormat` for any published revision,
	// because the rendering contract is the frozen document, not the template's list
	// (the two lists are therefore deliberately separate, and the report delivery
	// subset lives in one place on each side: `validReportExportFormat` in Go,
	// `REPORT_EXPORT_FORMATS` in `report-contract.ts`).
	//
	// pdf became legal in P0-B, html and xlsx with the fourth-format slice. The three
	// seeded templates still advertise docx/md/pdf: widening a *stored* payload would
	// mint a template version every workspace forks from, and a format nobody asked
	// for is not a reason to change what an already-published report was written
	// for.
	OutputFormats       []string                     `json:"output_formats"`
	Sections            []ReportTemplateSection      `json:"sections"`
	Limits              ReportTemplateLimits         `json:"limits"`
	Retrieval           ReportTemplateRetrieval      `json:"retrieval"`
	EvidencePolicy      ReportTemplateEvidencePolicy `json:"evidence_policy"`
	OutlineConfirmation string                       `json:"outline_confirmation"`
	// AutoConfirmAllowedRoles stays empty in P0-A: turning the outline
	// confirmation off is a permission on the report owner, not a template
	// default (292 D2).
	AutoConfirmAllowedRoles []string `json:"auto_confirm_allowed_roles,omitempty"`
}

// Closed sets of the template payload (doc/288 §11.1 / §9.4 / §9.5).
var (
	reportOutputFormats        = []string{"docx", "md", "pdf", "html", "xlsx"}
	reportThresholdPolicies    = []string{"dataset_calibrated"}
	reportConflictHandlings    = []string{"disclose", "block", "prefer_latest"}
	reportOutdatedEvidence     = []string{"flag", "exclude"}
	reportUnsupportedClaims    = []string{"prohibit", "disclose"}
	reportOutlineConfirmations = []string{model.ReportOutlineRequired, model.ReportOutlineAuto}
)

// P0-A only implements these two values; the others are frozen in the closed set
// so the contract does not have to change when P0-B opens them.
const (
	reportConflictHandlingP0  = "disclose"
	reportOutdatedEvidenceP0  = "flag"
	reportUnsupportedClaimsP0 = "prohibit"
)

// reportSliceMaxSecondsRatio bounds `slice_max_seconds` against the async
// runtime job timeout: a slice must checkpoint well inside its lease. 0.8 is the
// safety band from doc/288 §11.1 (480s of a 600s job).
const reportSliceMaxSecondsRatio = 0.8

// defaultJobTimeoutSeconds mirrors the async worker's default job timeout, so
// the ceiling cannot drift away from the runtime it protects.
const defaultJobTimeoutSeconds = 600

// sliceMaxSecondsCeiling is the largest legal slice length.
func sliceMaxSecondsCeiling() int {
	return int(float64(defaultJobTimeoutSeconds) * reportSliceMaxSecondsRatio)
}

// validateReportTemplatePayload enforces the §11.1 contract. Every rule below has
// a downstream consumer, so a violation would otherwise surface as a runtime
// oddity (unknown dependency, slice that always overruns, report with no
// citations) rather than as the configuration error it actually is.
func validateReportTemplatePayload(payload *ReportTemplatePayload) error {
	if payload == nil {
		return fmt.Errorf("report template payload is required")
	}
	if strings.TrimSpace(payload.Name.Zh) == "" || strings.TrimSpace(payload.Name.En) == "" {
		return fmt.Errorf("name.zh and name.en are required")
	}
	if len(payload.OutputFormats) == 0 {
		return fmt.Errorf("output_formats must not be empty")
	}
	for _, format := range payload.OutputFormats {
		if !containsString(reportOutputFormats, format) {
			return fmt.Errorf("unsupported output format %q", format)
		}
	}
	if !containsString(reportOutlineConfirmations, payload.OutlineConfirmation) {
		return fmt.Errorf("outline_confirmation must be one of %v", reportOutlineConfirmations)
	}
	if payload.OutlineConfirmation == model.ReportOutlineAuto && len(payload.AutoConfirmAllowedRoles) == 0 {
		// Turning confirmation off without naming who may do it hands the
		// permission to everyone (292 D2).
		return fmt.Errorf("outline_confirmation=auto requires auto_confirm_allowed_roles")
	}
	if len(payload.Sections) == 0 {
		return fmt.Errorf("sections must not be empty")
	}
	if payload.Limits.MaxSections > 0 && len(payload.Sections) > payload.Limits.MaxSections {
		return fmt.Errorf("sections (%d) exceed limits.max_sections (%d)",
			len(payload.Sections), payload.Limits.MaxSections)
	}
	if err := validateReportSections(payload.Sections); err != nil {
		return err
	}
	if err := validateReportLimits(payload.Limits); err != nil {
		return err
	}
	if payload.Retrieval.TopK <= 0 {
		return fmt.Errorf("retrieval.top_k must be positive")
	}
	if !containsString(reportThresholdPolicies, payload.Retrieval.ThresholdPolicy) {
		return fmt.Errorf("retrieval.threshold_policy must be one of %v", reportThresholdPolicies)
	}
	if payload.Retrieval.MinEvidence < 0 {
		return fmt.Errorf("retrieval.min_evidence must not be negative")
	}
	for name, value := range map[string]string{
		"evidence_policy.conflict_handling":  payload.EvidencePolicy.ConflictHandling,
		"evidence_policy.outdated_evidence":  payload.EvidencePolicy.OutdatedEvidence,
		"evidence_policy.unsupported_claims": payload.EvidencePolicy.UnsupportedClaims,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if !containsString(reportConflictHandlings, payload.EvidencePolicy.ConflictHandling) {
		return fmt.Errorf("evidence_policy.conflict_handling must be one of %v", reportConflictHandlings)
	}
	if !containsString(reportOutdatedEvidence, payload.EvidencePolicy.OutdatedEvidence) {
		return fmt.Errorf("evidence_policy.outdated_evidence must be one of %v", reportOutdatedEvidence)
	}
	if !containsString(reportUnsupportedClaims, payload.EvidencePolicy.UnsupportedClaims) {
		return fmt.Errorf("evidence_policy.unsupported_claims must be one of %v", reportUnsupportedClaims)
	}
	return nil
}

// validateReportSections checks the section graph: unique keys, non-empty
// titles/prompts, resolvable dependencies, no cycles, and at least one section
// that requires evidence.
func validateReportSections(sections []ReportTemplateSection) error {
	position := make(map[string]int, len(sections))
	for i, section := range sections {
		key := strings.TrimSpace(section.Key)
		if key == "" {
			return fmt.Errorf("sections[%d].key is required", i)
		}
		if _, exists := position[key]; exists {
			return fmt.Errorf("duplicate section key %q", key)
		}
		position[key] = i
		if strings.TrimSpace(section.Title) == "" {
			return fmt.Errorf("sections[%d] (%s) title is required", i, key)
		}
		if strings.TrimSpace(section.Prompt) == "" {
			return fmt.Errorf("sections[%d] (%s) prompt is required", i, key)
		}
		if section.MaxWords < 0 {
			return fmt.Errorf("sections[%d] (%s) max_words must not be negative", i, key)
		}
	}
	requiresEvidence := false
	for i, section := range sections {
		if section.RequiresEvidence {
			requiresEvidence = true
		}
		for _, dep := range section.DependsOn {
			if dep == section.Key {
				return fmt.Errorf("sections[%d] (%s) depends on itself", i, section.Key)
			}
			if _, exists := position[dep]; !exists {
				return fmt.Errorf("sections[%d] (%s) depends on unknown section %q", i, section.Key, dep)
			}
		}
	}
	if !requiresEvidence {
		return fmt.Errorf("at least one section must require evidence")
	}
	if cycle := reportSectionCycle(sections, position); cycle != "" {
		return fmt.Errorf("section dependency cycle: %s", cycle)
	}
	return nil
}

// reportSectionCycle returns a human-readable cycle path, or "" when the graph
// is acyclic. Dependencies read as "B depends on A"; the walk is iterative so a
// template with many sections cannot blow the stack.
func reportSectionCycle(sections []ReportTemplateSection, position map[string]int) string {
	deps := make(map[string][]string, len(sections))
	for _, section := range sections {
		deps[section.Key] = section.DependsOn
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(sections))
	stack := make([]string, 0, len(sections))
	next := make([]int, 0, len(sections))
	for _, section := range sections {
		start := section.Key
		if color[start] != white {
			continue
		}
		color[start] = grey
		stack = append(stack, start)
		next = append(next, 0)
		for len(stack) > 0 {
			top := len(stack) - 1
			node := stack[top]
			edges := deps[node]
			if next[top] >= len(edges) {
				color[node] = black
				stack = stack[:top]
				next = next[:top]
				continue
			}
			child := edges[next[top]]
			next[top]++
			switch color[child] {
			case grey:
				at := 0
				for i, node := range stack {
					if node == child {
						at = i
						break
					}
				}
				cycle := append(append([]string{}, stack[at:]...), child)
				return strings.Join(cycle, " -> ")
			case black:
				continue
			default:
				color[child] = grey
				stack = append(stack, child)
				next = append(next, 0)
			}
		}
	}
	return ""
}

// validateReportLimits checks the budget envelope, including the slice/timeout
// relationship that makes checkpointing possible at all.
func validateReportLimits(limits ReportTemplateLimits) error {
	if limits.MaxSections <= 0 {
		return fmt.Errorf("limits.max_sections must be positive")
	}
	if limits.MaxSlices <= 0 {
		return fmt.Errorf("limits.max_slices must be positive")
	}
	if limits.MaxTokensPerSection <= 0 {
		return fmt.Errorf("limits.max_tokens_per_section must be positive")
	}
	if limits.SliceMaxSeconds <= 0 {
		return fmt.Errorf("limits.slice_max_seconds must be positive")
	}
	if ceiling := sliceMaxSecondsCeiling(); limits.SliceMaxSeconds > ceiling {
		return fmt.Errorf("limits.slice_max_seconds (%d) exceeds %d (%.0f%% of the %ds runtime job timeout)",
			limits.SliceMaxSeconds, ceiling, reportSliceMaxSecondsRatio*100, defaultJobTimeoutSeconds)
	}
	if limits.TotalMaxSeconds > 0 && limits.TotalMaxSeconds < limits.SliceMaxSeconds {
		return fmt.Errorf("limits.total_max_seconds must be >= slice_max_seconds")
	}
	if limits.TotalMaxSeconds > 0 && limits.MaxSlices*limits.SliceMaxSeconds < limits.TotalMaxSeconds {
		// The slice budget must be able to cover the declared total, otherwise a
		// healthy run is guaranteed to end `partial` for a configuration reason.
		return fmt.Errorf("limits.max_slices × slice_max_seconds (%d) cannot cover total_max_seconds (%d)",
			limits.MaxSlices*limits.SliceMaxSeconds, limits.TotalMaxSeconds)
	}
	return nil
}

// hashReportTemplatePayload is the seed's "did this template change?" test. It
// hashes the canonical JSON of the payload, so neither key ordering nor the
// column layout can fake a change — and because both sides go through the same
// canonicalisation, a decoded row and an authored literal compare equal.
func hashReportTemplatePayload(payload *ReportTemplatePayload) string {
	if payload == nil {
		return ""
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// reportTemplateSectionOrder returns the section keys in declaration order. The
// outline generator starts from this order; dependencies only constrain when a
// section may be written, never where it appears in the body.
func reportTemplateSectionOrder(payload *ReportTemplatePayload) []string {
	if payload == nil {
		return nil
	}
	keys := make([]string, 0, len(payload.Sections))
	for _, section := range payload.Sections {
		keys = append(keys, section.Key)
	}
	return keys
}
