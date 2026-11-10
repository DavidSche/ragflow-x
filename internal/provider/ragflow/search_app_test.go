package ragflow

import (
	"bytes"
	"context"
	"errors"
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
	stream := "data:{\"code\":0,\"message\":\"\",\"data\":{\"content\":\"Hello\",\"reference\":{\"chunks\":[{\"id\":\"c1\",\"content\":\"hit\",\"docnm_kwd\":\"a.txt\"}]}}}\n\n" +
		"data:{\"code\":0,\"message\":\"\",\"data\":true}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/searches/s1/completions" {
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

// TestSearchAppSSE_DeduplicateSameFrameAnswer covers P3: the search-app engine
// mirrors the stream delta into both `content` and `answer`; accumulating both
// verbatim doubled the visible answer. Identical frames must collapse to one copy.
func TestSearchAppSSE_DeduplicateSameFrameAnswer(t *testing.T) {
	stream := "data:{\"code\":0,\"message\":\"\",\"data\":{\"content\":\"Hel\",\"answer\":\"Hel\"}}\n\n" +
		"data:{\"code\":0,\"message\":\"\",\"data\":{\"content\":\"lo\",\"answer\":\"lo\",\"reference\":{\"chunks\":[{\"id\":\"c1\",\"content\":\"hit\",\"docnm_kwd\":\"a.txt\"}]}}}\n\n" +
		"data:{\"code\":0,\"message\":\"\",\"data\":true}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	defer srv.Close()
	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)

	res, err := client.SearchAppCompletion(context.Background(), "s1", SearchAppCompletionRequest{Question: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "Hello" {
		t.Fatalf("embedded content+answer mirrors must deduplicate; got %q want \"Hello\"", res.Answer)
	}
	if len(res.Reference) != 1 || res.Reference[0]["id"] != "c1" {
		t.Fatalf("reference lost: %+v", res.Reference)
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

// TestHTTPClient_SearchAppCompletionRetriesTransient500 verifies the bounded
// retry contract on the completion call (doc/268 §3 P0-1): a transient engine
// 500 is retried with backoff and the follow-up success is returned; the
// success code path replays the idempotent body via GetBody.
func TestHTTPClient_SearchAppCompletionRetriesTransient500(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, `{"code":500,"message":"transient engine glitch"}`, http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "data: {\"code\":0,\"data\":{\"answer\":\"ok answer\"}}\n\n")
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	res, err := client.SearchAppCompletion(context.Background(), "s1", SearchAppCompletionRequest{Question: "q"})
	if err != nil {
		t.Fatalf("retry should recover: %v", err)
	}
	if !strings.Contains(res.Answer, "ok answer") {
		t.Fatalf("expected recovered answer, got %+v", res)
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 engine calls (1 failure + 1 retry), got %d", calls)
	}
}

// TestHTTPClient_SearchAppCompletionRetriesRawNetworkError verifies raw
// transport failures (before any *Error exists) are retried too.
func TestHTTPClient_SearchAppCompletionRetriesRawNetworkError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Hijack and drop the connection to simulate a network reset.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("hijack unsupported")
			}
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		_, _ = io.WriteString(w, "data: {\"code\":0,\"data\":{\"answer\":\"recovered\"}}\n\n")
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 2)
	res, err := client.SearchAppCompletion(context.Background(), "s1", SearchAppCompletionRequest{Question: "q"})
	if err != nil {
		t.Fatalf("network retry should recover: %v", err)
	}
	if !strings.Contains(res.Answer, "recovered") {
		t.Fatalf("expected recovered answer, got %+v", res)
	}
	if calls != 2 {
		t.Fatalf("expected 2 engine calls, got %d", calls)
	}
}

// TestHTTPClient_SearchAppCompletionGivesUpOnPersistent500 verifies the retry
// budget is bounded: a request that keeps failing surfaces the last error
// instead of looping forever.
func TestHTTPClient_SearchAppCompletionGivesUpOnPersistent500(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"code":500,"message":"persistent failure"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 3*time.Second, 2)
	_, err := client.SearchAppCompletion(context.Background(), "s1", SearchAppCompletionRequest{Question: "q"})
	if err == nil {
		t.Fatal("persistent 500 must fail after the retry budget")
	}
	if calls != searchCompletionMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", searchCompletionMaxAttempts, calls)
	}
}

// TestSearchCompletionRetryBackoff verifies the backoff ladder and cap
// (200ms → 600ms → 1800ms, then capped at 2s).
func TestSearchCompletionRetryBackoff(t *testing.T) {
	got := []time.Duration{
		searchCompletionRetryBackoff(0),
		searchCompletionRetryBackoff(1),
		searchCompletionRetryBackoff(2),
		searchCompletionRetryBackoff(3),
		searchCompletionRetryBackoff(10),
	}
	want := []time.Duration{0, 200 * time.Millisecond, 600 * time.Millisecond, 1800 * time.Millisecond, 2 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestRetryableSearchCompletionErrorNeverRetriesCancellation locks in the
// contract that a caller cancellation is never replayed.
func TestRetryableSearchCompletionErrorNeverRetriesCancellation(t *testing.T) {
	if _, retry := retryableSearchCompletionError(context.Canceled); retry {
		t.Fatal("context.Canceled must not be retried")
	}
	if _, retry := retryableSearchCompletionError(&Error{Type: ErrorTypeBusiness, Code: 400, HTTPStatus: 400}); retry {
		t.Fatal("a persistent 4xx business error must not be retried")
	}
	if _, retry := retryableSearchCompletionError(&Error{Type: ErrorTypeTimeout}); !retry {
		t.Fatal("a timeout must be retried")
	}
	if _, retry := retryableSearchCompletionError(&Error{Type: ErrorTypeTransport, HTTPStatus: 500}); !retry {
		t.Fatal("a 5xx must be retried")
	}
	if _, retry := retryableSearchCompletionError(errors.New("bare transport error")); !retry {
		t.Fatal("a bare network error must be retried")
	}
}

// TestParseSearchAppSSESurfacesBusinessError verifies the silent-empty fix
// (doc/268 §3 P0-3): a terminal SSE frame code!=0 with a message yields a
// structured business error instead of an empty success result. Success code 0
// and legacy 200 keep their current behaviour.
func TestParseSearchAppSSESurfacesBusinessError(t *testing.T) {
	// Legacy success 200 stays a success.
	res, err := parseSearchAppSSE(strings.NewReader(
		"data: {\"code\":200,\"data\":{\"content\":\"ok\"}}\n\ndata:false\n\n"))
	if err != nil {
		t.Fatalf("legacy 200 must stay a success: %v", err)
	}
	if !strings.Contains(res.Answer, "ok") {
		t.Fatalf("legacy 200 answer lost: %+v", res)
	}

	// Engine-level no-relevant frame surfaces as a business error.
	_, err = parseSearchAppSSE(strings.NewReader(
		"data: {\"code\":500,\"message\":\"Sorry, no relevant information provided.\",\"reference\":[]}\n\ndata:false\n\n"))
	if err == nil {
		t.Fatal("a code!=0 frame with a message must surface as an error")
	}
	var ragErr *Error
	if !errors.As(err, &ragErr) {
		t.Fatalf("expected a structured *Error, got %T", err)
	}
	if ragErr.Type != ErrorTypeBusiness || ragErr.Code != 500 {
		t.Fatalf("unexpected error classification: %+v", ragErr)
	}
}
