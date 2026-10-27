package obs

import (
	"os"
	"strings"
	"testing"
)

func TestCompilationRecoveryRunbookLocksTenantSafetyGates(t *testing.T) {
	raw, err := os.ReadFile("../../doc/runbooks/CompilationUpstreamRecoveryRunbook.md")
	if err != nil {
		t.Fatalf("read compilation recovery runbook: %v", err)
	}
	content := string(raw)
	for _, section := range []string{
		"## 2. 安全红线",
		"## 4. 只读对账",
		"## 5. 审批、确认与替代 run",
		"`tenant_id`",
		"禁止自动重发",
		"禁止跨租户操作",
		"禁止无审批替代",
		"30 分钟观察窗",
		"`binding_candidates`",
		"`source_doc_ids`",
		"`artifact_payload_sha256_v1`",
		"`triggered_by_x_run_id`",
	} {
		if !strings.Contains(content, section) {
			t.Fatalf("compilation recovery runbook missing %q", section)
		}
	}
}
