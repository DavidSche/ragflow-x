package ragflow

import (
	"errors"
	"testing"
)

// T6.3 version preflight tests (223 T6 / 224 §2.4): the fail-closed gate must
// block on probe failure, pass on the verified band, require re-drill on newer
// engines, and degrade on older engines with no proven floor.

func TestCompareReleaseVersionsOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0-rc1", "v1.0.0-rc1", 0},
		{"v1.0.0-rc1", "v1.0.0-rc2", -1},
		{"v1.0.0-rc2", "v1.0.0-rc1", 1},
		{"v1.0.0-rc1", "v1.0.0", -1}, // stable release > its rc
		{"v1.0.0", "v1.0.0-rc9", 1},
		{"v1.0.0", "v0.99.0", 1},
		{"0.27.2", "v1.0.0-rc1", -1},
		{"1.0.1", "1.0.0", 1},
		{"0.27.2", "0.27.2", 0},
		{"v1.0.0-rc1", "v1.1.0", -1},
	}
	for _, c := range cases {
		got, err := CompareReleaseVersions(c.a, c.b)
		if err != nil {
			t.Errorf("Compare(%s,%s) unexpected error: %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("Compare(%s,%s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareReleaseVersionsRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"v1", "1.2.3.4", "latest", "rc", "RC1", "", "1.2", "v1.2"} {
		if _, err := CompareReleaseVersions(bad, "v1.0.0-rc1"); err == nil {
			t.Errorf("CompareReleaseVersions must reject %q", bad)
		}
	}
}

func TestVerifiedMaxVersionResolvesRC1AfterT62(t *testing.T) {
	verified := VerifiedMaxVersion()
	if verified != "v1.0.0-rc1" {
		t.Fatalf("T6.2 set MaxVerified=v1.0.0-rc1 everywhere; VerifiedMaxVersion = %q", verified)
	}
}

func TestEvaluateVersionGateFailClosed(t *testing.T) {
	// Probe failure -> block (fail-closed, no version trust).
	decision, reason := EvaluateVersionGate("", errors.New("connection refused"))
	if decision != VersionGateBlock || reason == "" {
		t.Fatalf("probe error must block, got %q reason %q", decision, reason)
	}
	// Empty observation -> block.
	if decision, _ := EvaluateVersionGate("", nil); decision != VersionGateBlock {
		t.Fatalf("empty version must block, got %q", decision)
	}
	// Malformed observation -> block.
	if decision, _ := EvaluateVersionGate("latest", nil); decision != VersionGateBlock {
		t.Fatalf("malformed version must block, got %q", decision)
	}
}

func TestEvaluateVersionGateBandSemantics(t *testing.T) {
	// Inside the verified band -> pass.
	if decision, _ := EvaluateVersionGate("v1.0.0-rc1", nil); decision != VersionGatePass {
		t.Fatalf("verified engine must pass, got %q", decision)
	}
	// Newer engine -> verification required (contract re-drill gate).
	if decision, _ := EvaluateVersionGate("v1.0.0", nil); decision != VersionGateVerificationRequired {
		t.Fatalf("newer engine must require verification, got %q", decision)
	}
	// Older engine with no proven floor -> degrade conservatively.
	if decision, _ := EvaluateVersionGate("0.27.2", nil); decision != VersionGateDegrade {
		t.Fatalf("older engine must degrade, got %q", decision)
	}
}

func TestRunVersionPreflightAggregatesPerCapability(t *testing.T) {
	// Probe error short-circuits to a face-level block.
	blocked := RunVersionPreflight("", errors.New("down"))
	if blocked.Decision != VersionGateBlock || len(blocked.Capabilities) != 0 {
		t.Fatalf("probe-error preflight must block at face level, got %+v", blocked.Decision)
	}
	// Verified band: every capability endpoint passes.
	passing := RunVersionPreflight("v1.0.0-rc1", nil)
	if passing.Decision != VersionGatePass {
		t.Fatalf("verified engine preflight must pass, got %q", passing.Decision)
	}
	if len(passing.Capabilities) == 0 {
		t.Fatal("preflight must roll up at least one capability")
	}
	capNames := map[string]bool{}
	for _, cap := range passing.Capabilities {
		capNames[cap.Name] = true
		if cap.Decision != VersionGatePass {
			t.Errorf("verified engine: capability %s must pass, got %q", cap.Name, cap.Decision)
		}
		if cap.Reason == "" {
			t.Errorf("capability %s missing verdict reason", cap.Name)
		}
	}
	for _, must := range []string{"chat", "agent", "knowledge_strategy", "search_app", "memory"} {
		if !capNames[must] {
			t.Errorf("preflight must cover capability %s, got %v", must, capNames)
		}
	}
	// Newer engine: at least one capability requires verification, overall too.
	newer := RunVersionPreflight("v1.0.0", nil)
	if newer.Decision != VersionGateVerificationRequired {
		t.Fatalf("newer engine preflight must require verification, got %q", newer.Decision)
	}
	// Older engine: capabilities degrade.
	older := RunVersionPreflight("0.27.2", nil)
	if older.Decision != VersionGateDegrade {
		t.Fatalf("older engine preflight must degrade, got %q", older.Decision)
	}
	for _, cap := range older.Capabilities {
		if cap.Decision == VersionGatePass {
			t.Errorf("older engine: capability %s must not pass", cap.Name)
		}
	}
}
