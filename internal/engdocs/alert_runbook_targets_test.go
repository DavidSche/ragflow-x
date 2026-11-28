package engdocs

// ScenarioID: SC-DOC-003
//
// Every alert in `config/observability/prometheus-alerts.yml` carries a
// `runbook:` annotation — the line an on-call engineer opens at 03:00. All eight
// distinct targets in the shipped file are untracked: seven live under
// `doc/operations/runbooks/` (below `.gitignore`'s `/doc/*`) and one under
// `doc/architecture/`. So the annotation points at a file a clone does not have,
// and nothing noticed, for the same reason `doc/321` records: **a reference to
// something absent looks exactly like a reference to something fine**, because
// nothing opens it.
//
// This gate cannot make those documents shippable — that needs a redaction pass
// over operator runbooks that currently quote real hosts and tokens, which is a
// separate decision. What it can do is freeze today's debt and stop it growing:
// a new alert must point at a file that is actually in the index, and the frozen
// list must shrink as the debt is paid (a stale baseline is the hole a baseline
// is supposed to close).
//
// It reads the git index rather than the filesystem on purpose: a file can sit
// on the author's disk forever and still be missing from every clone, which is
// precisely the defect.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const alertRunbookBaseline = "internal/engdocs/alert_runbook_baseline.json"

// alertRunbookTarget matches `runbook: "doc/..."` in the alert rules. The value is
// a repository-relative path, not a URL: these are read in the source tree.
var alertRunbookTarget = regexp.MustCompile(`runbook:\s*"([^"]+)"`)

func TestAlertRunbookTargetsAreTrackedOrFrozen(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Skipf("module root not found: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, ".git")); err != nil || !info.IsDir() {
		t.Skip("not a git checkout: the question is what the index contains")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	referenced := collectAlertRunbookTargets(t, root)
	if len(referenced) == 0 {
		t.Fatalf("no runbook: annotation found under config/ — either the alerts moved or this regexp rotted")
	}

	untracked := map[string]bool{}
	for _, path := range referenced {
		if !gitTracks(t, root, path) {
			untracked[path] = true
		}
	}

	frozen := readAlertRunbookBaseline(t, root)

	var problems []string
	for path := range untracked {
		if !frozen[path] {
			problems = append(problems, formatNewDanglingRunbook(path))
		}
	}
	for path := range frozen {
		if !untracked[path] {
			problems = append(problems, formatStaleRunbookBaseline(path))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%s\n\nfrozen debt (%d entries): %s",
			strings.Join(problems, "\n"),
			len(frozen), alertRunbookBaseline)
	}
	t.Logf("%d runbook: targets, all tracked or in the frozen debt of %d", len(referenced), len(frozen))
}

func collectAlertRunbookTargets(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	configDir := filepath.Join(root, "config")
	err := filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yml", ".yaml":
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range alertRunbookTarget.FindAllStringSubmatch(string(raw), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk config/: %v", err)
	}
	sort.Strings(out)
	return out
}

// gitTracks asks the index, not the working tree.
func gitTracks(t *testing.T, root, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "--literal-pathspecs", "ls-files", "--error-unmatch", "--", path)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_LITERAL_PATHSPECS=1")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			return false
		}
		t.Fatalf("git ls-files %s: %v", path, err)
	}
	return true
}

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func readAlertRunbookBaseline(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, alertRunbookBaseline))
	if err != nil {
		t.Fatalf("read runbook baseline %s: %v", alertRunbookBaseline, err)
	}
	var baseline struct {
		Untracked []string `json:"untracked"`
	}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("decode runbook baseline: %v", err)
	}
	out := map[string]bool{}
	for _, path := range baseline.Untracked {
		out[path] = true
	}
	return out
}

func formatNewDanglingRunbook(path string) string {
	return "a shipped alert points at an untracked runbook: " + path +
		"\n  → either track the document (and redact it first) or point the annotation at one that is in the index; " +
		"a runbook nobody has is worse than none, because it reads as documentation"
}

func formatStaleRunbookBaseline(path string) string {
	return "the frozen runbook debt lists a path that is no longer dangling: " + path +
		"\n  → the debt was paid (good) or the alert was deleted (also fine): remove the entry from " + alertRunbookBaseline +
		", otherwise the frozen budget silently re-allows it later"
}
