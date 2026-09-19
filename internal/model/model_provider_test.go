package model

import (
	"reflect"
	"testing"
)

func TestModelTypeMaskAndLabels(t *testing.T) {
	mask := ModelTypeMask([]string{"chat", "embedding", "vision"})
	if mask != ModelTypeChat|ModelTypeEmbedding|ModelTypeVision {
		t.Fatalf("unexpected mask %d", mask)
	}
	labels := ModelTypeLabels(mask)
	want := []string{"chat", "embedding", "vision"}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
}

func TestModelTypeMaskUnknownIgnored(t *testing.T) {
	if got := ModelTypeMask([]string{"chat", "unknown-kind"}); got != ModelTypeChat {
		t.Fatalf("unknown type must be ignored, got %d", got)
	}
	if got := ModelTypeLabels(0); len(got) != 0 {
		t.Fatalf("expected empty labels for zero mask, got %v", got)
	}
}

func TestModelTypeExactBits(t *testing.T) {
	// Mirrors RAGFlow's ModelTypeBinary values 1,2,4,8,16,32,64.
	want := map[string]int{
		"chat": 1, "embedding": 2, "asr": 4, "vision": 8,
		"rerank": 16, "tts": 32, "ocr": 64,
	}
	for name, bit := range want {
		if got := ModelTypeMask([]string{name}); got != bit {
			t.Fatalf("type %s = %d, want %d", name, got, bit)
		}
	}
}
