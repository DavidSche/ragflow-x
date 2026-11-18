package service

// The PDF renderer cannot ship without a font in the image (doc/288 §12 / T14).
//
// A live run found the gap the hard way: the binary was correct, the tests were
// green on the workstation, and the deployed container returned `failed`/`not
// downloadable` for every `format=pdf` export because `alpine` ships no CJK font
// and the drill image never installed one. Doc/44 R10 could therefore only record
// an ENV-LIMIT note instead of a PASS — "works on my machine" wearing a passing
// test suite.
//
// This test keeps image and renderer in step: whichever font package the Dockerfiles
// install must be on the renderer's probe list, otherwise the font is installed and
// silently unused. It reads the Dockerfiles rather than asserting on a constant,
// because the constant alone cannot notice that the image stopped installing it.

import (
	"os"
	"strings"
	"testing"
)

func TestEveryImageFontPackageHasAProbePath(t *testing.T) {
	// Alpine package → where it lands, and whether fpdf can embed it.
	// font-noto-cjk is deliberately absent: it ships CFF collections, which the
	// renderer refuses (`sfntFontBytes` rejects them) — an image installing only
	// Noto would probe, reject, and fail every PDF with a confusing message.
	packages := map[string]string{
		"font-wqy-zenhei": "/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc",
	}

	for _, path := range []string{
		"../../docker/Dockerfile",
		"../../localenv/151-test/Dockerfile.test",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		installed := apkPackages(string(raw))
		for pkg, fontPath := range packages {
			if !installed[pkg] {
				t.Fatalf("%s must install %s, otherwise PDF export fails at runtime "+
					"(no CJK font in the image)", path, pkg)
			}
			if !containsPath(pdfFontCandidates(), fontPath) {
				t.Fatalf("%s installs %s at %s but pdfFontCandidates() does not probe it: %v",
					path, pkg, fontPath, pdfFontCandidates())
			}
		}
		if installed["font-noto-cjk"] {
			t.Fatalf("%s must not install font-noto-cjk: it ships CFF outlines the "+
				"renderer cannot embed, so the font would be present and still unusable", path)
		}
	}
}

// TestPdfFontProbeShipsTheAlpineLayout pins the layout detail that made the live
// gap invisible: Alpine puts WenQuanYi under /usr/share/fonts/wqy-zenhei/, while
// every Debian-derived path in the list uses truetype/wqy/. Probing only the
// Debian layout means "font installed, still no PDF".
func TestPdfFontProbeShipsTheAlpineLayout(t *testing.T) {
	if !containsPath(pdfFontCandidates(), "/usr/share/fonts/wqy-zenhei/wqy-zenhei.ttc") {
		t.Fatalf("pdfFontCandidates() lost the Alpine font path: %v", pdfFontCandidates())
	}
}

// apkPackages reads the package names a Dockerfile actually installs, ignoring
// the comments that explain *why* a package is installed or avoided.
func apkPackages(dockerfile string) map[string]bool {
	installed := map[string]bool{}
	for _, line := range strings.Split(dockerfile, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "RUN ") || !strings.Contains(line, "apk add") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if field == "" || strings.HasPrefix(field, "-") || strings.ContainsAny(field, "\"'=:") {
				continue
			}
			installed[field] = true
		}
	}
	return installed
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
