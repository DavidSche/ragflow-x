package main

import (
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestClassifyProbe(t *testing.T) {
	// The embedding-link canary contract (doc/268 §3 P1-1): recovered means
	// the engine produced both references and an answer.
	recovered := &ragflow.SearchAppCompletionResult{
		Answer:    "山东能源对超一类企业的判定基于 …",
		Reference: []map[string]interface{}{{"id": 1}, {"id": 2}},
	}
	empty := &ragflow.SearchAppCompletionResult{}

	cases := []struct {
		name       string
		res        *ragflow.SearchAppCompletionResult
		err        error
		wantStatus string
		wantExit   int
	}{
		{"recovered", recovered, nil, "recovered", 0},
		{"silent empty (i-0002 class)", empty, nil, "no-hit", 2},
		{"engine error", &ragflow.SearchAppCompletionResult{}, errors.New("ragflow error 500: boom"), "error", 3},
		{"nil result", nil, nil, "error", 3},
		{"transport error", nil, errors.New("connection reset"), "error", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, exit := classifyProbe(tc.res, tc.err)
			if status != tc.wantStatus || exit != tc.wantExit {
				t.Fatalf("classifyProbe = (%q, %d), want (%q, %d)", status, exit, tc.wantStatus, tc.wantExit)
			}
		})
	}
}

func TestProbeRunUsage(t *testing.T) {
	if code := probeRun([]string{"--search-app", "s1"}, func(string) string { return "" }); code != 1 {
		t.Fatalf("missing env must exit 1 (usage), got %d", code)
	}
}
