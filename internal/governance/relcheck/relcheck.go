// Package relcheck implements the REL-003 external-review gate validation
// described in doc 137 §14 (REL-003) and §32 P2-1/P2-8: an automated, evidence
// based audit of the four external review mapping tables (139/140/141/143),
// document engineering lint for the plan and status documents, and the
// cross-cutting governance gates (WAIT statistics, privacy red lines, layered
// idempotency evidence, UI source consistency, built-in permission
// reconciliation and the deterministic Memory/Knowledge conflict rule).
//
// The package is exercised by relcheck_test.go which writes a JSON report to
// test-results/rel3-external-review-validation.json and fails when any check
// regresses. It is a tripwire, not a proof: review PASS evidence still comes
// from the recorded live drills and milestone docs.
package relcheck

import (
	"fmt"
	"os"
	"path/filepath"
)

// RepoRoot locates the ragflow-x repository root from the working directory by
// walking up until go.mod and the plan document are both present.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) &&
			fileExists(filepath.Join(dir, "doc", "develop", "137_知识编译与Memory无声落地实施方案.md")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("relcheck: repository root not found from %s", dir)
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// docPath resolves the plan and status document paths.
func docPaths(root string) (plan string, status string) {
	return filepath.Join(root, "doc", "develop", "137_知识编译与Memory无声落地实施方案.md"),
		filepath.Join(root, "doc", "develop", "145_知识编译与Memory无声落地实施状态.md")
}
