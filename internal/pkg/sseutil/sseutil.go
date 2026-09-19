// Package sseutil provides helpers for scanning OpenAI-compatible SSE streams
// to extract token usage metadata from data blocks.
package sseutil

import (
	"encoding/json"
	"strings"
)

// TrackUsage scans a single SSE line for OpenAI-style usage blocks and
// accumulates prompt/completion token counts into the provided pointers.
// It is safe to call on non-data lines; they are silently ignored.
func TrackUsage(line string, tIn, tOut *int64) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "[DONE]" || payload == "" {
		return
	}
	var chunk struct {
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(payload), &chunk) == nil && chunk.Usage != nil {
		*tIn += chunk.Usage.PromptTokens
		*tOut += chunk.Usage.CompletionTokens
	}
}
