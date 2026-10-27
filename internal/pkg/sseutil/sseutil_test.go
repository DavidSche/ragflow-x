package sseutil

import "testing"

func TestTrackUsage_WithDataBlock(t *testing.T) {
	var tIn, tOut int64
	line := `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
	TrackUsage(line, &tIn, &tOut)
	if tIn != 10 || tOut != 5 {
		t.Fatalf("expected 10/5, got %d/%d", tIn, tOut)
	}
}

func TestTrackUsage_Accumulates(t *testing.T) {
	var tIn, tOut int64
	TrackUsage(`data: {"usage":{"prompt_tokens":3,"completion_tokens":2}}`, &tIn, &tOut)
	TrackUsage(`data: {"usage":{"prompt_tokens":7,"completion_tokens":1}}`, &tIn, &tOut)
	if tIn != 10 || tOut != 3 {
		t.Fatalf("expected 10/3, got %d/%d", tIn, tOut)
	}
}

func TestTrackUsage_IgnoresNonData(t *testing.T) {
	var tIn, tOut int64
	TrackUsage(`{"usage":{"prompt_tokens":100,"completion_tokens":50}}`, &tIn, &tOut)
	TrackUsage(`event: message`, &tIn, &tOut)
	TrackUsage(``, &tIn, &tOut)
	if tIn != 0 || tOut != 0 {
		t.Fatalf("expected 0/0, got %d/%d", tIn, tOut)
	}
}

func TestTrackUsage_IgnoresDone(t *testing.T) {
	var tIn, tOut int64
	TrackUsage(`data: [DONE]`, &tIn, &tOut)
	if tIn != 0 || tOut != 0 {
		t.Fatalf("expected 0/0, got %d/%d", tIn, tOut)
	}
}

func TestTrackUsage_IgnoresNoUsage(t *testing.T) {
	var tIn, tOut int64
	TrackUsage(`data: {"choices":[{"delta":{"content":"hello"}}]}`, &tIn, &tOut)
	if tIn != 0 || tOut != 0 {
		t.Fatalf("expected 0/0, got %d/%d", tIn, tOut)
	}
}

func TestTrackUsage_EmptyPayload(t *testing.T) {
	var tIn, tOut int64
	TrackUsage(`data: `, &tIn, &tOut)
	if tIn != 0 || tOut != 0 {
		t.Fatalf("expected 0/0, got %d/%d", tIn, tOut)
	}
}
