package ragflow

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClient_SearchAppContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/v1/searches":
			if r.Method == http.MethodPost {
				if !strings.Contains(string(b), `"name":"kb-search"`) {
					http.Error(w, "bad create body "+string(b), http.StatusBadRequest)
					return
				}
				writeEnvelope(t, w, map[string]interface{}{"search_id": "s1"})
				return
			}
			writeEnvelope(t, w, map[string]interface{}{
				"search_apps": []map[string]interface{}{
					{"id": "s1", "name": "kb-search", "tenant_id": "t1", "update_time": 1},
				},
				"total": 1,
			})
		case "/api/v1/searches/s1":
			switch r.Method {
			case http.MethodGet:
				writeEnvelope(t, w, map[string]interface{}{
					"id": "s1", "name": "kb-search", "tenant_id": "t1",
					"search_config": map[string]interface{}{"kb_ids": []string{"d1"}, "top_k": 1024, "vector_similarity_weight": 0.3},
				})
			case http.MethodPut:
				if !strings.Contains(string(b), `"search_config"`) {
					http.Error(w, "bad update body "+string(b), http.StatusBadRequest)
					return
				}
				writeEnvelope(t, w, map[string]interface{}{"id": "s1", "name": "renamed"})
			case http.MethodDelete:
				writeEnvelope(t, w, true)
			}
		case "/api/v1/searches/s1/completion":
			writeEnvelope(t, w, map[string]interface{}{})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	ctx := context.Background()

	id, err := client.CreateSearchApp(ctx, CreateSearchAppRequest{Name: "kb-search"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "s1" {
		t.Fatalf("unexpected created id: %s", id)
	}

	list, total, err := client.ListSearchApps(ctx, ListSearchAppsFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Name != "kb-search" {
		t.Fatalf("unexpected list: %+v total=%d", list, total)
	}

	got, err := client.GetSearchApp(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "s1" || got.SearchConfig == nil || len(got.SearchConfig.KbIDs) != 1 {
		t.Fatalf("unexpected get: %+v", got)
	}

	upd, err := client.UpdateSearchApp(ctx, "s1", UpdateSearchAppRequest{Name: "renamed", SearchConfig: &SearchConfig{KbIDs: []string{"d1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Name != "renamed" {
		t.Fatalf("unexpected updated: %+v", upd)
	}

	if err := client.DeleteSearchApp(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClient_SearchAppSSECompletion(t *testing.T) {
	stream := "data:{\"code\":0,\"message\":\"\",\"data\":{\"content\":\"Hello\",\"reference\":[{\"id\":\"c1\",\"content\":\"hit\",\"docnm_kwd\":\"a.txt\"}]}}\n\n" +
		"data:{\"code\":0,\"message\":\"\",\"data\":true}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/searches/s1/completion" {
			http.Error(w, r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)

	res, err := client.SearchAppCompletion(context.Background(), "s1", SearchAppCompletionRequest{Question: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Answer, "Hello") || len(res.Reference) != 1 {
		t.Fatalf("unexpected completion: %+v", res)
	}

	var buf bytes.Buffer
	if err := client.StreamSearchAppCompletion(context.Background(), "s1", SearchAppCompletionRequest{Question: "hi"}, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != stream {
		t.Fatalf("stream mismatch:\n%s", buf.String())
	}
}

func TestMock_SearchAppLifecycle(t *testing.T) {
	mock := NewMock()
	ctx := context.Background()

	id, err := mock.CreateSearchApp(ctx, CreateSearchAppRequest{Name: "kb-search"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := mock.GetSearchApp(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "kb-search" || got.SearchConfig.TopK == 0 {
		t.Fatalf("unexpected mock search app: %+v", got)
	}

	upd, err := mock.UpdateSearchApp(ctx, id, UpdateSearchAppRequest{Name: "renamed", SearchConfig: &SearchConfig{TopK: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Name != "renamed" || upd.SearchConfig.TopK != 100 {
		t.Fatalf("unexpected renamed: %+v", upd)
	}

	res, err := mock.SearchAppCompletion(ctx, id, SearchAppCompletionRequest{Question: "rake"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Answer, "rake") || len(res.Reference) == 0 {
		t.Fatalf("unexpected completion: %+v", res)
	}

	list, total, err := mock.ListSearchApps(ctx, ListSearchAppsFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Name != "renamed" {
		t.Fatalf("unexpected list: %+v total=%d", list, total)
	}

	if err := mock.DeleteSearchApp(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := mock.GetSearchApp(ctx, id); err == nil {
		t.Fatal("expected search app to be deleted")
	}
}
