package ragflow

import (
	"strconv"
	"testing"
)

func TestPageSessionMessages(t *testing.T) {
	messages := make([]Message, 55)
	for i := range messages {
		messages[i].Content = strconv.Itoa(i)
	}

	first, next := pageSessionMessages(messages, SessionMessagePageOptions{})
	if len(first) != 50 || first[0].Content != "5" || first[49].Content != "54" || next != "5" {
		t.Fatalf("first page: len=%d first=%s last=%s next=%s", len(first), first[0].Content, first[49].Content, next)
	}

	second, next2 := pageSessionMessages(messages, SessionMessagePageOptions{Cursor: next})
	if len(second) != 5 || second[0].Content != "0" || second[4].Content != "4" || next2 != "" {
		t.Fatalf("second page: len=%d first=%s last=%s next=%s", len(second), second[0].Content, second[4].Content, next2)
	}

	short, next3 := pageSessionMessages([]Message{{Content: "0"}}, SessionMessagePageOptions{})
	if len(short) != 1 || short[0].Content != "0" || next3 != "" {
		t.Fatalf("short session: len=%d next=%s", len(short), next3)
	}
}
