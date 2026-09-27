package service

import "strings"

var (
	thinkOpen  = string([]byte{60, 116, 104, 105, 110, 107, 62})
	thinkClose = string([]byte{60, 47, 116, 104, 105, 110, 107, 62})
)

// visibleRAGFlowAnswer removes RAGFlow/model thinking markers from answers
// before they enter API responses, quality events or delivery snapshots.
func visibleRAGFlowAnswer(answer string) string {
	if !strings.Contains(answer, thinkOpen) {
		return answer
	}
	var visible strings.Builder
	cursor := 0
	for {
		start := strings.Index(answer[cursor:], thinkOpen)
		if start < 0 {
			visible.WriteString(answer[cursor:])
			break
		}
		start += cursor
		contentStart := start + len(thinkOpen)
		relativeClose := strings.Index(answer[contentStart:], thinkClose)
		if relativeClose < 0 {
			visible.WriteString(answer[cursor:start])
			break
		}
		visible.WriteString(answer[cursor:start])
		cursor = contentStart + relativeClose + len(thinkClose)
	}
	return strings.TrimSpace(visible.String())
}
