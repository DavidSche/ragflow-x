// Package strutil provides small string helpers that stay safe for
// non-ASCII (multi-byte UTF-8) content.
package strutil

import "unicode/utf8"

// Truncate returns at most maxBytes of s without splitting a UTF-8 sequence.
// If s already fits (len(s) <= maxBytes) it is returned unchanged; otherwise
// the result is the longest rune-boundary-aligned prefix with len <=
// maxBytes. Truncating multi-byte content on a raw byte boundary would emit
// invalid UTF-8, which PostgreSQL rejects on write — this helper exists so
// audit details, error summaries and other persisted text never hit that
// edge. Invalid UTF-8 input degrades to the longest valid prefix.
func Truncate(s string, maxBytes int) string {
	if maxBytes < 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
