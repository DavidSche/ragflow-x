package engdocs

// ScenarioID: SC-DOC-002
//
// doc/319 R-06 / P-08: the engineering evidence anchors had drifted. `doc/develop/304`
// cites implementation by `file.go:<line>`, and after a few rounds of edits every one
// of those numbers pointed somewhere else — the anchor still *resolved* to a file, so
// nothing looked broken, but the sentence above it described different code. A
// line-number anchor rots on the next `gofmt`; a symbol anchor survives refactors.
//
// The rule (doc/specification/00 §5) is: cite `file.go` + a symbol
// (func/const/type/test name), never `file.go:<line>`. That cannot be enforced by
// rewriting history — `doc/develop/` is an append-only frozen archive, so the legacy
// anchors stay and the errata section explains them. What a gate *can* do is stop the
// bleeding: the count of line-number anchors per document is frozen here, and any
// increase fails with the format rule in the message.
//
// Counts, not occurrences: freezing the numbers themselves would require updating the
// baseline every time the surrounding prose is touched, and the interesting signal is
// "did this change add a new line anchor".

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docLineAnchor matches `pkg/file.go:123` and `file.go:123` — the drifted shape. A
// bare `第 123 行` inside an errata paragraph is deliberately out of scope: the
// errata has to be able to name the defect it is correcting.
var docLineAnchor = regexp.MustCompile(`[A-Za-z0-9_./-]+\.go:[0-9]+`)

// docAnchorBaseline is the frozen legacy state, keyed by repo-relative path.
const docAnchorBaseline = "internal/engdocs/doc_anchor_baseline.json"

func TestDocAnchorsDoNotGrow(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Skipf("module root not found: %v", err)
	}
	if !Present(root) {
		t.Skip(SkipReason)
	}

	baseline := map[string]int{}
	raw, err := os.ReadFile(filepath.Join(root, docAnchorBaseline))
	if err != nil {
		t.Fatalf("read anchor baseline %s: %v", docAnchorBaseline, err)
	}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("decode anchor baseline: %v", err)
	}

	current := collectDocLineAnchors(t, root)

	// A document that did not exist when the gate was written starts from zero: a new
	// file has no legacy to grandfather.
	var problems []string
	for path, got := range current {
		want, known := baseline[path]
		if !known {
			want = 0
		}
		if got > want {
			problems = append(problems, formatAnchorDrift(path, want, got))
		}
	}
	for path, want := range baseline {
		if current[path] < want {
			// The document was rewritten to symbols (the good outcome) or the file
			// moved. Both mean the baseline is stale, and a stale baseline is exactly
			// the hole this gate is supposed to close: it would let `want` anchors
			// creep back in later.
			problems = append(problems, formatAnchorSlack(path, want, current[path]))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("evidence anchors drifted from the format rule (doc/specification/00 §5):\n%s\n\n"+
			"Cite `file.go` plus the symbol (func/const/type/test name). After a deliberate "+
			"symbol rewrite, refresh the frozen counts with:\n"+
			"  RGX_DOC_ANCHOR_UPDATE=1 go test ./internal/engdocs/ -run TestDocAnchorsDoNotGrow",
			strings.Join(problems, "\n"))
	}

	if os.Getenv("RGX_DOC_ANCHOR_UPDATE") != "1" {
		return
	}
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatalf("encode anchor baseline: %v", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(root, docAnchorBaseline), encoded, 0o644); err != nil {
		t.Fatalf("write anchor baseline: %v", err)
	}
	t.Logf("refreshed %s: %d documents, %d anchors", docAnchorBaseline, len(current), sumAnchors(current))
}

func formatAnchorDrift(path string, want, got int) string {
	return "  " + path + ": " + itoa(want) + " -> " + itoa(got) + " line-number anchors " +
		"(new ones must be symbol anchors)"
}

func formatAnchorSlack(path string, want, got int) string {
	return "  " + path + ": baseline says " + itoa(want) + ", found " + itoa(got) +
		" — stale baseline; refresh it so the removed anchors cannot return"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func sumAnchors(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

// collectDocLineAnchors counts line-number anchors per document under doc/develop and
// doc/specification. Both trees are locked to the same rule: a specification that
// quotes line numbers rots in exactly the same way, and it is the file implementers
// copy from.
func collectDocLineAnchors(t *testing.T, root string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, tree := range []string{"doc/develop", "doc/specification"} {
		dir := filepath.Join(root, filepath.FromSlash(tree))
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", tree, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			relative := tree + "/" + entry.Name()
			body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", relative, err)
			}
			if count := len(docLineAnchor.FindAll(body, -1)); count > 0 {
				counts[relative] = count
			}
		}
	}
	return counts
}
