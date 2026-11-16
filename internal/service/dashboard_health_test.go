package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// healthEngine stubs only the probe; the rest of the provider interface comes
// from the mock so the test stays about the status contract.
type healthEngine struct {
	*ragflow.Mock
	health *ragflow.Health
	err    error
}

func (h healthEngine) Health(context.Context) (*ragflow.Health, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.health, nil
}

// TestSystemHealthNormalisesEngineVocabulary pins the API contract the console
// reads. RAGFlow answers `{"db":"ok","redis":"ok","doc_engine":"ok"}`, and the
// dashboard paints ✅ only on "up" — before normalisation a healthy engine
// rendered `DB: ❌ Redis: ❌ DocEngine: ❌` next to the green "在线" badge.
func TestSystemHealthNormalisesEngineVocabulary(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "engine ok", value: "ok", want: "up"},
		{name: "already up", value: "up", want: "up"},
		{name: "mixed case", value: " OK ", want: "up"},
		{name: "down", value: "down", want: "down"},
		{name: "empty", value: "", want: "down"},
		{name: "unknown word", value: "degraded", want: "down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := healthState(tc.value); got != tc.want {
				t.Fatalf("healthState(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}

	svc := &Service{RAGFlow: healthEngine{
		Mock:   ragflow.NewMock(),
		health: &ragflow.Health{Status: "ok", DB: "ok", Redis: "ok", DocEngine: "ok"},
	}}
	out, err := svc.SystemHealth(context.Background())
	if err != nil {
		t.Fatalf("SystemHealth: %v", err)
	}
	for _, key := range []string{"engine", "db", "redis", "doc_engine"} {
		if out[key] != "up" {
			t.Fatalf("%s = %q, want up (response: %+v)", key, out[key], out)
		}
	}
}

// TestSystemHealthReportsProbeFailure keeps the failure path loud: an engine
// that cannot be probed must return the gateway error, not a green badge.
func TestSystemHealthReportsProbeFailure(t *testing.T) {
	svc := &Service{RAGFlow: healthEngine{Mock: ragflow.NewMock(), err: errors.New("connection refused")}}
	out, err := svc.SystemHealth(context.Background())
	if err == nil {
		t.Fatalf("expected error, got %+v", out)
	}
	if code := err.(*httperr.Error).Code; code != 50210 {
		t.Fatalf("error code = %d, want 50210", code)
	}
	if out["engine"] != "down" {
		t.Fatalf("engine = %q, want down", out["engine"])
	}
}
