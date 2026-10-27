package relcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollectGateStatsAdmitsOnlyRegisteredABVerdicts locks the release evidence
// ledger boundary. Only a file that declares a "gate_status" verdict is A/B
// evidence; naming a contract version is not enough. This keeps two families of
// non-gate artifacts out of the release evidence report even if someone drops
// them into test-results/:
//
//   - a frozen D1 quality bundle (doc 234 §2 / doc 231 D7) — it carries
//     sample_size/gate_eligible but no A/B verdict, and stays draft until the
//     holdout criterion is met;
//   - a trial-run export (doc 231 D11) — v0.4.0 trial runs never feed the gate.
func TestCollectGateStatsAdmitsOnlyRegisteredABVerdicts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir evidence dir: %v", err)
	}
	files := map[string]string{
		"ab-pass.json":    `{"contract_version":"scoped-paired-ab-v4","gate_status":"PASS","gate_reason":"lift +0.40pp"}`,
		"ab-wait.json":    `{"contract_version":"scoped-paired-ab-v4","gate_status":"WAIT"}`,
		"d1-bundle.json":  `{"contract_version":"d1-frozen-regression-v1","sample_size":51,"passes":2,"primary_metric":"citation_precision","gate_eligible":false,"status":"draft"}`,
		"trial-run.json":  `{"id":"run-1","status":"COMPLETED","gate_eligible":false,"summary":{"cases":2,"returned_cases":0}}`,
		"build-log.json":  `{"contract_version":"local-build","duration_ms":1234}`,
		"no-metadata.txt": `not json at all`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	stats := CollectGateStats(root)

	if len(stats.Evidence) != 2 {
		var names []string
		for _, evidence := range stats.Evidence {
			names = append(names, filepath.Base(evidence.File))
		}
		t.Fatalf("expected exactly the two A/B verdicts in the ledger, got %d: %v", len(stats.Evidence), names)
	}
	for _, evidence := range stats.Evidence {
		base := filepath.Base(evidence.File)
		if base != "ab-pass.json" && base != "ab-wait.json" {
			t.Errorf("%s must not be admitted as A/B evidence", base)
		}
		if evidence.GateStatus == "" {
			t.Errorf("%s admitted without a gate_status verdict", base)
		}
		if evidence.ContractVersion != "scoped-paired-ab-v4" {
			t.Errorf("%s lost its contract version: %q", base, evidence.ContractVersion)
		}
	}
	if stats.Counts["PASS"] != 1 || stats.Counts["WAIT"] != 1 || len(stats.Counts) != 2 {
		t.Fatalf("counts must only aggregate verdicts, got %v", stats.Counts)
	}
	if stats.Counts[""] != 0 {
		t.Fatalf("verdict-less files must not create an empty-status bucket: %v", stats.Counts)
	}
	if strings.Contains(strings.Join([]string{stats.Evidence[0].File, stats.Evidence[1].File}, ","), "d1") {
		t.Fatalf("a D1 bundle leaked into the A/B ledger: %v", stats.Evidence)
	}
}
