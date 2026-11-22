package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A gold file is never judged alone: `assemble` writes a manifest next to it, and that
// manifest is the only record of *whether the assembly accepted its own output*. The two
// closed-loop problems this file exists to stop:
//
//   - `assemble` refuses (exit 1) but still writes the JSONL, because a refused file is
//     how an operator sees *what* was wrong. The next command in the manual's chain then
//     reads that same path. Without a check it judges a draft as if it were gold, and the
//     problems the assembly named become rows that look measured.
//   - the gold file can be edited after the assembly (an annotator "fixes" one verdict
//     by hand). The manifest's sha256 is what says the counts and κ still describe the
//     file in front of you.
//
// The check is deliberately *additive*: no sidecar means nothing is claimed (a worksheet,
// a hand-built fixture, an older artifact), so the command proceeds exactly as before.
// Only a sidecar that is present and says "this was refused" or "this is a different
// file" stops the run — "I could not check" is not turned into a silent pass.
const sidecarManifestSuffix = ".manifest.json"

// sidecarManifestPath is where `assemble` puts a gold file's manifest: same directory,
// same stem, `.manifest.json` (`…-gold.v1.jsonl` → `…-gold.v1.manifest.json`).
func sidecarManifestPath(goldPath string) string {
	return strings.TrimSuffix(goldPath, filepath.Ext(goldPath)) + sidecarManifestSuffix
}

// goldSidecar is the part of the manifest a *consumer* needs: which file it describes,
// and what the assembly thought of it. Unknown fields are ignored on purpose — a
// consumer must not break because `assemble` learned a new count.
type goldSidecar struct {
	// File is the name the assembly wrote next to the gold file (a basename, resolved
	// against the manifest's own directory). It is compared by *identity*, never by string:
	// `tmp/x.jsonl` and `./tmp/x.jsonl` are the same file, and a string compare would refuse
	// correct usage. `os.SameFile` is the comparison, and a name that cannot be resolved is
	// "cannot compare" rather than "different" — the additive rule holds (304 §7 item 20).
	File     string    `json:"file"`
	SHA256   string    `json:"sha256"`
	Items    int       `json:"items"`
	Problems []Problem `json:"problems"`
}

// goldRefusals answers one question before a command reads a gold file: may this file be
// used at all? It returns one sentence per reason it may not, and nothing when there is
// no sidecar (nothing to contradict) or the sidecar records a clean assembly.
func goldRefusals(goldPath string) []string {
	mpath := sidecarManifestPath(goldPath)
	if _, err := os.Stat(mpath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{fmt.Sprintf("%s cannot be read (%v): a sidecar manifest next to a gold file must be "+
			"checkable, otherwise its absence and its refusal look the same", mpath, err)}
	}
	raw, err := os.ReadFile(mpath)
	if err != nil {
		return []string{fmt.Sprintf("%s cannot be read (%v)", mpath, err)}
	}
	var ms goldSidecar
	if err := json.Unmarshal(raw, &ms); err != nil {
		return []string{fmt.Sprintf("%s is not a manifest (%v): a sidecar that cannot be parsed is worse than "+
			"none, because it looks like evidence", mpath, err)}
	}

	var out []string
	// Identity: does the manifest name the file we were handed? The manifest records a
	// basename, resolved against its own directory — a *different* file next to a copied
	// manifest would otherwise be judged with another file's counts and κ. A name that
	// cannot be resolved is not evidence of a mismatch (the corpus may have moved with the
	// manifest), so it is left to the sha256 below, which is the claim that actually matters.
	if rec := strings.TrimSpace(ms.File); rec != "" {
		if !filepath.IsAbs(rec) {
			rec = filepath.Join(filepath.Dir(mpath), rec)
		}
		ri, ierr := os.Stat(rec)
		gi, gerr := os.Stat(goldPath)
		if ierr == nil && gerr == nil && !os.SameFile(ri, gi) {
			out = append(out, fmt.Sprintf("%s names %s, which is a different file from %s: a manifest that "+
				"describes another file's count, κ and problems would be quoted as this file's evidence",
				mpath, ms.File, goldPath))
		}
	}
	if ms.SHA256 != "" {
		sum, _, err := fileSum(goldPath)
		if err != nil {
			out = append(out, fmt.Sprintf("%s cannot be hashed (%v)", goldPath, err))
		} else if sum != ms.SHA256 {
			out = append(out, fmt.Sprintf("%s describes a different file: it records sha256 %s, but %s hashes "+
				"to %s — the gold file changed after the assembly, so the manifest's counts, κ and problems no "+
				"longer describe it", mpath, head12(ms.SHA256), goldPath, head12(sum)))
		}
	}
	if len(ms.Problems) > 0 {
		out = append(out, fmt.Sprintf("%s was written by an assembly that refused it (%s records %d "+
			"problem(s); first: %s): a refused assembly's output is a draft, and a later step that reads it as "+
			"gold turns a known problem into a number that looks measured", goldPath, mpath, len(ms.Problems),
			headRunes(ms.Problems[0].String(), 120)))
	}
	return out
}

// sidecarFingerprint reports the manifest a run was judged against, and that manifest's own
// sha256, so the receipt records *which* file made the claim. This is provenance, not
// authentication (304 §7 item 19): it lets a reviewer pin the manifest in a review note and
// notice that it changed later; it does not stop someone editing the manifest and the gold
// file together, which is why the documented threat model is operator error, not tampering.
// Empty strings when there is no sidecar — a worksheet or a hand-built fixture claims nothing.
func sidecarFingerprint(goldPath string) (path, sum string) {
	mpath := sidecarManifestPath(goldPath)
	s, _, err := fileSum(mpath)
	if err != nil {
		return "", ""
	}
	return mpath, s
}

// fileSum hashes the bytes on disk and reports their size. It is the one place a gold
// file's identity is computed, so the manifest `assemble` writes and the hash `eval` and
// `preds` re-check cannot drift apart.
func fileSum(path string) (string, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), len(raw), nil
}

// headRunes shortens a message to n runes: the first problem of a refused assembly can
// itself be a paragraph, and a refusal nobody finishes reading is a refusal nobody acts
// on. The full text stays in the manifest.
func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// head12 shortens a hash for a message: enough to tell two files apart, not so much that
// the sentence stops being readable.
func head12(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}
