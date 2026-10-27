package strutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateReturnsShortStringsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{
		{"ascii under", "hello", 10, "hello"},
		{"ascii exact", "hello", 5, "hello"},
		{"cjk exact", "中文", 6, "中文"},
		{"negative limit", "hello", -1, "hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.in, tc.maxBytes); got != tc.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.in, tc.maxBytes, got, tc.want)
			}
		})
	}
}

func TestTruncateNeverSplitsRune(t *testing.T) {
	// 1000 CJK chars = 3000 bytes. Truncating to 1800 bytes must land on a
	// rune boundary (1800 % 3 == 0 here, so exactly 600 runes).
	cjk := strings.Repeat("知", 1000)
	got := Truncate(cjk, 1800)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated CJK string is invalid UTF-8 (len %d)", len(got))
	}
	if len(got) > 1800 {
		t.Fatalf("len = %d, want <= 1800", len(got))
	}
	if want := 1800; len(got) != want {
		t.Fatalf("len = %d, want %d (boundary aligned)", len(got), want)
	}
}

func TestTruncateBacksOffToRuneBoundary(t *testing.T) {
	// "中文a中" (10 bytes): the cut point 8 lands inside the final CJK rune
	// (bytes 7..9), so the result must back off to 7: "中文a".
	got := Truncate("中文a中", 8)
	if got != "中文a" {
		t.Fatalf("Truncate = %q, want %q", got, "中文a")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("result is invalid UTF-8: %q", got)
	}
}

func TestTruncateZeroLimit(t *testing.T) {
	if got := Truncate("中文x", 0); got != "" {
		t.Fatalf("Truncate with 0 = %q, want empty", got)
	}
}

func TestTruncateMixedContentAtOddBoundary(t *testing.T) {
	// ascii(4) + CJK x3 (9) + emoji(4): 17 bytes total. The cut point 12
	// lands inside the third CJK rune (bytes 10..12), so the result backs
	// off to byte 10: "abcd中文".
	in := "abcd" + "中文知" + "😀"
	if len(in) != 17 {
		t.Fatalf("fixture length = %d, want 17", len(in))
	}
	got := Truncate(in, 12)
	if !utf8.ValidString(got) {
		t.Fatalf("result invalid: %q", got)
	}
	if len(got) > 12 {
		t.Fatalf("len = %d, want <= 12", len(got))
	}
	if want := "abcd中文"; got != want {
		t.Fatalf("Truncate = %q, want %q", got, want)
	}
}
