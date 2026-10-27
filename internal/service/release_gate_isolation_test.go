package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gateInputFirewallForbidden are the evidence families that must never become an
// input of the release gate:
//
//   - trial_run / TrialRun: doc 231 D11 — the v0.4.0 trial run is an independent
//     object with gate_eligible=false by construction; it is not a release
//     input. Only C4.5 (v0.4.1) may wire quality evidence, and only after the
//     D1 holdout criterion of doc 231 D7 is met.
//   - testquality: doc 234 §2 — the frozen D1 regression bundle is checked by
//     its own contract; feeding it into assistant_release would silently make a
//     draft bundle gate-eligible.
//   - gate_eligible / GateEligible: the field exists so the UI can state that a
//     run does not gate; reading it inside gate code would invert that meaning.
var gateInputFirewallForbidden = []string{
	"trial_run",
	"TrialRun",
	"testquality",
	"gate_eligible",
	"GateEligible",
}

// gateInputFirewallScope selects the release-gate implementation files: every
// non-test service file whose name starts with release or assistant_release,
// plus the relcheck package that renders the release evidence report.
func gateInputFirewallScope(t *testing.T) []string {
	t.Helper()
	var files []string
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read service dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if strings.HasPrefix(name, "release") || strings.HasPrefix(name, "assistant_release") {
			files = append(files, name)
		}
	}
	relcheckEntries, err := os.ReadDir(filepath.Join("..", "governance", "relcheck"))
	if err != nil {
		t.Fatalf("read relcheck dir: %v", err)
	}
	for _, entry := range relcheckEntries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join("..", "governance", "relcheck", name))
	}
	if len(files) < 5 {
		t.Fatalf("release gate scope looks too small (%d files); the scan would be vacuous", len(files))
	}
	return files
}

// forbiddenGateInputHits reports every non-comment line that mentions a
// forbidden evidence family, as "line:token".
func forbiddenGateInputHits(raw []byte) []string {
	var hits []string
	for lineNo, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		for _, forbidden := range gateInputFirewallForbidden {
			if strings.Contains(line, forbidden) {
				hits = append(hits, forbidden+"@"+strconv.Itoa(lineNo+1))
			}
		}
	}
	return hits
}

// TestReleaseGateSourcesNeverConsumeTrialOrQualityEvidence is the C4.5 negative
// firewall (doc 231 D7/D11, doc/234 §2): until the quality gate is wired on
// purpose, the release gate must not be able to read trial runs or the frozen
// D1 bundle. This is a source-level guard on purpose — a behavioural test could
// not prove the absence of a code path that never runs in tests.
func TestReleaseGateSourcesNeverConsumeTrialOrQualityEvidence(t *testing.T) {
	scope := gateInputFirewallScope(t)
	for _, path := range scope {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, hit := range forbiddenGateInputHits(raw) {
			t.Errorf("%s declares %s: the release gate must not consume trial-run or D1 bundle evidence (doc 231 D7/D11)", path, hit)
		}
	}

	// Control: the detector must actually fire on a file that does reference the
	// forbidden families, otherwise a typo in the token list would keep the
	// guard green forever. trial_run.go is outside the gate scope by name.
	control, err := os.ReadFile("trial_run.go")
	if err != nil {
		t.Fatalf("read control file: %v", err)
	}
	if len(forbiddenGateInputHits(control)) == 0 {
		t.Fatalf("control file trial_run.go produced no hits; the firewall token list is broken")
	}
}
