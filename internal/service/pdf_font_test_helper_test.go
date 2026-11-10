package service

import "testing"

// testPDFFont returns a CJK TrueType font the PDF renderer can embed, or skips
// the test when the machine has none.
//
// It reuses the production lookup (RGX_EXPORT_PDF_FONT first, then the Windows
// and Linux/Noto candidates) instead of a hardcoded `C:\Windows` path: the old
// pattern silently skipped every Linux run, which is why the absolute-path font
// bug in the renderer only ever showed up as a red CI job.
func testPDFFont(t *testing.T) string {
	t.Helper()
	// Probe the machine, not a font an earlier step of the same test pinned via
	// RGX_EXPORT_PDF_FONT (several tests set a bogus font first to prove the
	// fail-closed path).
	t.Setenv("RGX_EXPORT_PDF_FONT", "")
	if font := pdfFontPath(); font != "" {
		return font
	}
	t.Skip("valid CJK PDF font is unavailable (install fonts-noto-cjk or set RGX_EXPORT_PDF_FONT)")
	return ""
}
