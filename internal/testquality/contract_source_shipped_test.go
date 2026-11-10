// ScenarioID: SC-QUALITY-000
package testquality

import "testing"

// TestContractSourceMustShipWithRepository pins the doc git boundary: the
// engineering doc tree (doc/develop, doc/<NNN>-*.md, ...) is intentionally not
// committed, so a scenario catalogue that anchors its contract in those files
// passes locally and fails in CI. Only doc/user/** and code paths are valid
// contract sources.
func TestContractSourceMustShipWithRepository(t *testing.T) {
	reject := []string{
		"doc/develop/07_安全权限与合规.md",
		"doc/262-上游RAGFlow版本升级验证SOP（2026-11-05）.md",
		"doc/performance/baseline.md",
		"doc",
		"./doc/develop/../develop/07_安全权限与合规.md",
	}
	for _, path := range reject {
		if err := contractSourceShipped(path); err == nil {
			t.Errorf("contract source %q must be rejected: engineering docs are not shipped", path)
		}
	}

	accept := []string{
		"doc/user/zh-CN/permission-matrix.md",
		"doc/user/en/operations-runbook-and-failure-drill-sop.md",
		"internal/service/auth.go",
		"internal/gateway/contract/contract.go",
		"web/src/dataProvider.ts",
		"docs/README.md", // a sibling tree, not doc/ — must not trip the prefix check
		"",
	}
	for _, path := range accept {
		if err := contractSourceShipped(path); err != nil {
			t.Errorf("contract source %q must be accepted, got %v", path, err)
		}
	}
}
