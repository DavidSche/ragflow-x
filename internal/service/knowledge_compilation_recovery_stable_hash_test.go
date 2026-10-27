package service

import (
	"testing"
	"time"
)

// M204 live drill finding #4: the background duty prober refreshes evidence
// (QueriedAt, ProbeError, live status) every few seconds, so the approval
// snapshot hash must only cover stability-relevant fields or every
// compilation-run approval fails execution-time revalidation.
func TestCompilationRecoveryHashIgnoresProbeVolatileFields(t *testing.T) {
	base := &CompilationRecoveryEvidence{
		TenantID: "t1", DatasetID: "d1", Kind: "page_index",
		QueriedAt:                time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC),
		ManifestComplete:         true,
		NoMatch:                  true,
		EvidenceState:            "unknown",
		ObservationWindowSeconds: 1800,
	}
	later := *base
	later.QueriedAt = base.QueriedAt.Add(37 * time.Second)
	later.ProbeError = "timeout"
	if compilationRecoveryHash(base) != compilationRecoveryHash(&later) {
		t.Fatal("probe-volatile fields must not change the evidence hash")
	}
	changed := *base
	changed.NoMatch = false
	if compilationRecoveryHash(base) == compilationRecoveryHash(&changed) {
		t.Fatal("no-match verdict must change the evidence hash")
	}
}
