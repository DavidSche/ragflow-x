package relcheck

import (
	"fmt"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// VersionBandSummary is the T6.3 capability version-band closure folded into
// the REL-003 release-review auto-gate (doc/223 T6 / doc/224 §2.4, v0.3.0
// version-line merge): the relcheck release-review report ships the registry's
// single verified ceiling and the set of endpoints that still declare no
// version band. A non-empty gap turns the auto-gate red, so the release review
// cannot pass while the version-band reconciliation backlog is open.
type VersionBandSummary struct {
	// VerifiedCeiling is the newest MaxVerified across the registry
	// (ragflow.VerifiedMaxVersion; v1.0.0-rc1 after T6.2).
	VerifiedCeiling string `json:"verified_ceiling,omitempty"`
	// GapEndpoints lists registry endpoints that declare neither MinVersion
	// nor MaxVerified (Method + " " + Path).
	GapEndpoints []string `json:"gap_endpoints,omitempty"`
	// Pass is true only when the gap is empty.
	Pass bool `json:"pass"`
	// Message explains the pass/fail verdict for the release review report.
	Message string `json:"message"`
}

// CollectVersionBandGap snapshots the registry version-band gap for the
// release-review gate. It is the relcheck-side wiring of T6.3: production
// release already fails closed at CreateRelease when a confident band
// violation is detected, and this gate keeps the review evidence honest by
// refusing a clean auto-gate while any endpoint lacks a version band.
func CollectVersionBandGap() VersionBandSummary {
	summary := VersionBandSummary{VerifiedCeiling: ragflow.VerifiedMaxVersion()}
	for _, e := range ragflow.VersionBandGapReport() {
		summary.GapEndpoints = append(summary.GapEndpoints, e.Method+" "+e.Path)
	}
	if len(summary.GapEndpoints) == 0 {
		summary.Pass = true
		summary.Message = fmt.Sprintf(
			"version band clean: every registered endpoint declares a verified band (ceiling %s); T6.2 reconciliation closed (224 §2.4)",
			summary.VerifiedCeiling)
	} else {
		summary.Pass = false
		summary.Message = fmt.Sprintf(
			"%d endpoint(s) declare no version band; reconcile before release (223 T6 / 224 §2.4)",
			len(summary.GapEndpoints))
	}
	return summary
}
