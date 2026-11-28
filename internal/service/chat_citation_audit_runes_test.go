package service

import "testing"

// TestChatCitationVerbatimShortcutCountsRunesNotBytes is doc/319 R-05.
//
// The verbatim shortcut short-circuits the bigram measure with a stronger claim:
// "the answer reproduces the cited text". The bar was written as 8 — meaning eight
// characters — but compared against `len()`, which counts UTF-8 bytes. Three Chinese
// characters are nine bytes, so a three-rune fragment cleared a bar meant for entire
// sentences, and the finding was labelled `answer_contains_quote`: a label that
// overstates what was verified, on a board whose whole purpose is to be believable
// about citation quality.
//
// Two assertions, because either one alone can be satisfied by accident: the short
// CJK excerpt must not be called verbatim, and the boundary must still sit at eight
// runes (a "fix" that disables the shortcut entirely would also pass the first one).
func TestChatCitationVerbatimShortcutCountsRunesNotBytes(t *testing.T) {
	// 3 runes, 9 bytes. `len()` says 9 >= 8; there are only 3 characters.
	const threeRunes = "退保政"
	if len(threeRunes) != 9 || utf8RuneCount(threeRunes) != 3 {
		t.Fatalf("fixture drifted: bytes=%d runes=%d, want 9/3", len(threeRunes), utf8RuneCount(threeRunes))
	}
	answer := "按制度：" + threeRunes + "策应在 7 日内完成。"
	if score, verbatim := chatCitationSupport(answer, threeRunes); verbatim {
		t.Fatalf("a 3-rune excerpt was called verbatim (score %v): the shortcut is counting bytes, not characters", score)
	}

	// The boundary is 8 runes, and it is inclusive — that part of the original
	// intention is preserved.
	const eightRunes = "甲乙丙丁戊己庚辛"
	if utf8RuneCount(eightRunes) != 8 {
		t.Fatalf("fixture drifted: runes=%d, want 8", utf8RuneCount(eightRunes))
	}
	if _, verbatim := chatCitationSupport("制度："+eightRunes+"执行。", eightRunes); !verbatim {
		t.Fatal("an 8-rune excerpt quoted verbatim must still take the shortcut")
	}
	const sevenRunes = "甲乙丙丁戊己庚"
	if _, verbatim := chatCitationSupport("制度："+sevenRunes+"执行。", sevenRunes); verbatim {
		t.Fatal("a 7-rune excerpt must not take the shortcut: the bar is 8 characters")
	}

	// ASCII is the case the original constant was calibrated on: 8 bytes are 8 runes
	// there, so nothing about this fix changes how English citations are graded.
	const ascii = "refunded"
	if _, verbatim := chatCitationSupport("The order was "+ascii+" on time.", ascii); !verbatim {
		t.Fatal("an 8-character ASCII excerpt quoted verbatim must still take the shortcut")
	}
}

// TestChatCitationShortExcerptFallsBackToBigramCoverage pins what the fix does *not*
// change: a short excerpt that is present is still graded on bigram coverage.
//
// This is the boundary this round deliberately left alone (doc/320 §7): whether a
// two-bigram excerpt should be able to score 1.0 is a question about the measure, not
// about runes vs bytes, and answering it here would have silently re-tuned the
// supported/unsupported bands for every existing citation.
func TestChatCitationShortExcerptFallsBackToBigramCoverage(t *testing.T) {
	const threeRunes = "退保政"
	score, verbatim := chatCitationSupport("按制度："+threeRunes+"策应在 7 日内完成。", threeRunes)
	if verbatim {
		t.Fatal("verbatim must be false for a 3-rune excerpt")
	}
	if score != 1 {
		t.Fatalf("bigram coverage = %v, want 1: the two bigrams are both present, and this round does not re-tune the measure", score)
	}
}
