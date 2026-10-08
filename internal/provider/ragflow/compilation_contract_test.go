package ragflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestHTTPClient_GetCompilationStatusContract(t *testing.T) {
	completed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	updated := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC).Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v1/datasets/ds%2F1/compilation/status" {
			t.Fatalf("unexpected status request: %s %s", r.Method, r.URL.EscapedPath())
		}
		writeEnvelope(t, w, map[string]interface{}{
			"state": "running", "error": "merge retry", "inflight": 2, "backlog": 1,
			"last_completed_at": completed, "updated_at": updated,
		})
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	status, err := client.GetCompilationStatus(context.Background(), "ds/1")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "running" || status.Error != "merge retry" || status.Inflight != 2 ||
		status.Backlog != 1 || status.LastCompletedAt == nil || status.UpdatedAt.IsZero() {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestHTTPClient_ListCompilationTemplatesContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var want []CompilationTemplate
		switch r.URL.EscapedPath() {
		case "/api/v1/compilation-templates/builtins":
			want = []CompilationTemplate{{
				ID: "tree", Kind: "tree", DisplayName: "Tree",
				Description: "Hierarchy", Config: map[string]interface{}{"depth": float64(2)},
			}}
		case "/api/v1/compilation-templates/wiki-presets":
			want = []CompilationTemplate{{
				ID: "preset-1", DisplayName: "Architecture", Description: "preset",
				Config: map[string]interface{}{"topic": "architecture"},
			}}
		default:
			t.Fatalf("unexpected template request: %s %s", r.Method, r.URL.EscapedPath())
		}
		writeEnvelope(t, w, want)
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 2*time.Second, 1)
	builtins, err := client.ListCompilationTemplates(context.Background(), CompilationTemplateSourceBuiltins)
	if err != nil || len(builtins) != 1 || builtins[0].ID != "tree" || builtins[0].Kind != "tree" {
		t.Fatalf("builtins: %+v err=%v", builtins, err)
	}
	presets, err := client.ListCompilationTemplates(context.Background(), CompilationTemplateSourceWikiPresets)
	if err != nil || len(presets) != 1 || presets[0].ID != "preset-1" {
		t.Fatalf("presets: %+v err=%v", presets, err)
	}
}

func TestCompilationProviderValidation(t *testing.T) {
	client := NewHTTPClient("http://127.0.0.1:1", "key", time.Second, 1)
	ctx := context.Background()
	if _, err := client.GetCompilationStatus(ctx, " "); err == nil {
		t.Fatal("empty dataset id must fail")
	}
	if _, err := client.ListCompilationTemplates(ctx, "invalid"); err == nil {
		t.Fatal("invalid template source must fail")
	}
}

func TestMock_CompilationContracts(t *testing.T) {
	mock := NewMock()
	status := CompilationStatus{State: "running", Inflight: 1, Backlog: 2}
	templates := []CompilationTemplate{{ID: "tree", Kind: "tree", DisplayName: "Tree"}}
	mock.SetCompilationStatus("dataset-1", status)
	mock.SetCompilationTemplates(CompilationTemplateSourceBuiltins, templates)
	gotStatus, err := mock.GetCompilationStatus(context.Background(), "dataset-1")
	if err != nil || gotStatus.State != status.State || gotStatus.Inflight != status.Inflight ||
		gotStatus.Backlog != status.Backlog {
		t.Fatalf("status=%+v err=%v", gotStatus, err)
	}
	if captured := mock.LastCompilationStatusDataset(); captured != "dataset-1" {
		t.Fatalf("captured dataset=%s", captured)
	}
	gotTemplates, err := mock.ListCompilationTemplates(context.Background(), CompilationTemplateSourceBuiltins)
	if err != nil || !reflect.DeepEqual(gotTemplates, templates) {
		t.Fatalf("templates=%+v err=%v", gotTemplates, err)
	}
}
