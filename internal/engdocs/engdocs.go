// Package engdocs answers one question: is the internal engineering
// documentation tree (doc/develop, doc/operations/runbooks, doc/<NNN>-*.md) present next to
// this repository?
//
// Only doc/user/** ships with the repository (see AGENTS.local.md, "doc git
// boundary"). Gates that read the engineering tree therefore have to degrade to
// an explicit skip on a checkout of the published repository, instead of turning
// into a permanent red build that nobody can fix without widening the shipped
// surface. A skip is loud: every helper here returns a reason the test is
// expected to log.
package engdocs

import (
	"fmt"
	"os"
	"path/filepath"
)

// RepoRoot walks up from the working directory until it finds the module root
// (go.mod). It is independent of the engineering docs, which may be absent.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("engdocs: module root (go.mod) not found from %s", dir)
		}
		dir = parent
	}
}

// Present reports whether the engineering doc tree exists at root.
func Present(root string) bool {
	info, err := os.Stat(filepath.Join(root, "doc", "develop"))
	return err == nil && info.IsDir()
}

// PresentFromWorkingDirectory is Present for callers that only have a package
// directory. It returns false (never an error) when the module root cannot be
// located, because that only happens outside a source checkout.
func PresentFromWorkingDirectory() bool {
	root, err := RepoRoot()
	if err != nil {
		return false
	}
	return Present(root)
}

// SkipReason is the message tests log when they skip because the engineering
// docs are not shipped: it names the boundary so the skip is not mistaken for a
// silently weakened gate.
const SkipReason = "engineering docs (doc/develop, doc/operations/runbooks, doc/<NNN>-*.md) are not part of the published repository; only doc/user/** ships"
