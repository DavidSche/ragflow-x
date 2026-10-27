package relcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GateEvidenceSummary is the digest of one pre-registered A/B evidence file.
type GateEvidenceSummary struct {
	File            string `json:"file"`
	ContractVersion string `json:"contract_version"`
	GateStatus      string `json:"gate_status"`
	GateReason      string `json:"gate_reason"`
}

// GateStats aggregates every recorded scoped A/B gate verdict.
type GateStats struct {
	Evidence []GateEvidenceSummary `json:"evidence"`
	Counts   map[string]int        `json:"counts"`
}

var (
	gateStatusPattern  = regexp.MustCompile(`"gate_status"\s*:\s*"([^"]+)"`)
	gateVersionPattern = regexp.MustCompile(`"contract_version"\s*:\s*"([^"]+)"`)
	gateReasonPattern  = regexp.MustCompile(`"gate_reason"\s*:\s*"([^"]*)"`)
)

// CollectGateStats walks the known evidence directories and extracts the
// pre-registered gate verdicts (doc 137 §5.4/§14: Wiki/PageIndex/Memory A/B).
// Only files that declare a gate_status verdict are counted; see the note on
// the status check below for why contract_version alone is not enough.
func CollectGateStats(root string) *GateStats {
	stats := &GateStats{Counts: map[string]int{}}
	dirs := []string{
		filepath.Join(root, "internal", "service", "test-results"),
		filepath.Join(root, "test-results"),
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			full := filepath.Join(dir, entry.Name())
			if seen[full] {
				continue
			}
			seen[full] = true
			raw, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			status := gateStatusPattern.FindSubmatch(raw)
			version := gateVersionPattern.FindSubmatch(raw)
			// A file only enters the A/B ledger when it declares a verdict.
			// Naming a contract version without a gate_status is not evidence
			// (compile logs, quality bundles, trial-run exports); admitting
			// those rows would let a draft quality bundle or a trial run look
			// like a registered A/B verdict in the release evidence report
			// (doc 231 D11: trial runs never reach the release gate).
			if status == nil {
				continue
			}
			summary := GateEvidenceSummary{File: full}
			summary.GateStatus = string(status[1])
			stats.Counts[string(status[1])]++
			if version != nil {
				summary.ContractVersion = string(version[1])
			}
			if reason := gateReasonPattern.FindSubmatch(raw); reason != nil {
				summary.GateReason = string(reason[1])
			}
			stats.Evidence = append(stats.Evidence, summary)
		}
	}
	sort.Slice(stats.Evidence, func(i, j int) bool {
		return stats.Evidence[i].File < stats.Evidence[j].File
	})
	return stats
}

// PrivacyScanResult reports the red-line scan outcome (doc 137 §6.4: logs and
// audit payloads must never carry memory content fields).
type PrivacyScanResult struct {
	ScannedFiles int      `json:"scanned_files"`
	Violations   []string `json:"violations"`
}

var (
	loggerCallPattern = regexp.MustCompile(`logger\.(Info|Warn|Error|Debug)\(`)
	auditDetailCall   = regexp.MustCompile(`encodeAuditDetail\(`)
	contentFieldNames = []string{".Content", "UserInput", "AgentResponse", "Payload"}
)

// ScanPrivacyRedLines trips when any non-test Go file under internal/ logs or
// audits memory content fields. The baseline is zero violations.
func ScanPrivacyRedLines(root string) (*PrivacyScanResult, error) {
	result := &PrivacyScanResult{Violations: []string{}}
	serviceDir := filepath.Join(root, "internal")
	err := filepath.Walk(serviceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		result.ScannedFiles++
		rel, _ := filepath.Rel(root, path)
		for lineNo, line := range strings.Split(string(raw), "\n") {
			if !loggerCallPattern.MatchString(line) && !auditDetailCall.MatchString(line) {
				continue
			}
			for _, field := range contentFieldNames {
				if strings.Contains(line, field) {
					result.Violations = append(result.Violations,
						rel+":"+strconv.Itoa(lineNo+1)+" references "+field)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GovernanceCheck verifies a set of named conditions over the source tree.
type GovernanceCheck struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Pass        bool     `json:"pass"`
	Details     []string `json:"details,omitempty"`
}

func symbolCheck(name, description string, symbols []string, testSymbols map[string]bool) GovernanceCheck {
	check := GovernanceCheck{Name: name, Description: description, Pass: true}
	for _, symbol := range symbols {
		if !testSymbols[symbol] {
			check.Pass = false
			check.Details = append(check.Details, "missing test "+symbol)
		}
	}
	return check
}

func fileContentCheck(root, name, description string, files map[string][]string) GovernanceCheck {
	check := GovernanceCheck{Name: name, Description: description, Pass: true}
	for rel, tokens := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			check.Pass = false
			check.Details = append(check.Details, "missing file "+rel)
			continue
		}
		for _, token := range tokens {
			if !strings.Contains(string(raw), token) {
				check.Pass = false
				check.Details = append(check.Details, rel+" missing token "+token)
			}
		}
	}
	return check
}

// RunGovernanceChecks validates the cross-cutting REL-003 gates:
//   - layered idempotency evidence (X-layer linearization vs downstream no-auto-retry);
//   - UI source consistency surfaces;
//   - built-in permission reconciliation evidence;
//   - deterministic Memory/Knowledge conflict rule presence.
func RunGovernanceChecks(root string, testSymbols map[string]bool) []GovernanceCheck {
	return []GovernanceCheck{
		symbolCheck("layered_idempotency_evidence",
			"X 层授权线性化（accepted→admitted 原子条件）与下游不自动重试（unknown/补偿对账）分层验收",
			[]string{
				"TestMemoryDispatchAdmissionUsesCurrentPolicyAndActivePrincipal",
				"TestMemoryDispatchAbsentExternalMessageRequiresCompensation",
				"TestOutboxEventLeaseAndRetry",
				"TestCompilationUnknownRecoveryRequiresTwoNoMatchResultsAndApproval",
			}, testSymbols),
		symbolCheck("builtin_permission_reconciliation",
			"内置权限 reconciliation 证据：catalog 与端点矩阵回归",
			[]string{
				"TestBuiltinRBACReconciliationMigrationBackfillsExistingDatabase",
				"TestBuiltinRoleCatalogMatchesDoc30",
				"TestEveryInternalEndpointHasADrill",
			}, testSymbols),
		fileContentCheck(root, "ui_source_consistency",
			"UI 来源一致性：ContextSourceDialog/Memory 工作台存在且 i18n 键齐备",
			map[string][]string{
				"web/src/resources/conversation-center/ContextSourceDialog.tsx": {"context_policy_version"},
				"web/src/i18n/en.ts":                                            {"context_source_context_policy_version"},
				"web/src/i18n/zh.ts":                                            {"context_source_context_policy_version"},
			}),
		fileContentCheck(root, "memory_knowledge_conflict_rule",
			"确定性 Memory/Knowledge 冲突降级窄规则存在并接入受管检索（doc 137 §6.3 #8、§30 P1-4）",
			map[string][]string{
				"internal/service/memory_conflict.go": {"memoryConflictPolicyVersion", "memory_conflict_downgraded", "memory_conflict_unknown", "not_applicable"},
			}),
		symbolCheck("memory_knowledge_conflict_tests",
			"冲突降级规则的代表性回归（含 mixed path 接线）",
			[]string{
				"TestEvaluateMemoryConflictsValueConflictDowngrades",
				"TestEvaluateMemoryConflictsWithoutKnowledgeEvidenceFailsClosed",
				"TestExecuteRoutedMixedToolAnswerSupportsMemoryRetrieval",
			}, testSymbols),
	}
}
