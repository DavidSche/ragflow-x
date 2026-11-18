package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatToModelUsesTheChatCompletionRoute pins the route the live engine
// actually serves.
//
// The client used to POST `/providers/{p}/instances/{i}/models/{m}` and read the
// raw envelope back. RAGFlow v1.0.0 has no such endpoint (404 for every suffix,
// probed on 4.151 on 2026-11-18), so the report section writer failed 100% of
// the time with `error=ragflow error 404` while everything upstream looked
// healthy. The facts this test freezes:
//
//  1. the request goes to POST /chat/completions — any other path fails here;
//  2. the body names the model and carries the prompt as a user message, with
//     `stream:false` (the streaming form is raw SSE and the envelope parser
//     cannot read it);
//  3. the return value is the *answer text*, not the transport envelope: every
//     caller (section writer, route reranker, model test) parses what it gets
//     as the model's reply, so handing back `{"code":0,"data":{...}}` would turn
//     a healthy model into a failed section (the shape mismatch that made the
//     first live report unrecoverable).
func TestChatToModelUsesTheChatCompletionRoute(t *testing.T) {
	type captured struct {
		path string
		body map[string]interface{}
	}
	var seen captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		seen = captured{path: r.Method + " " + r.URL.EscapedPath()}
		if err := json.Unmarshal(raw, &seen.body); err != nil {
			t.Fatalf("request body is not JSON: %s", string(raw))
		}
		writeEnvelope(t, w, map[string]interface{}{"answer": "{\"title\":\"摘要\"}"})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 1)
	answer, err := client.ChatToModel(context.Background(),
		"GPUStack", "gpustack-local", "deepseek-v4-flash-0731", "写一段摘要", false, false)
	if err != nil {
		t.Fatalf("chat to model: %v", err)
	}
	if seen.path != "POST /api/v1/chat/completions" {
		t.Fatalf("chat must go to the completion route, got %q", seen.path)
	}
	if seen.body["model"] != "deepseek-v4-flash-0731" {
		t.Fatalf("body must name the model, got %+v", seen.body)
	}
	if stream, ok := seen.body["stream"].(bool); !ok || stream {
		t.Fatalf("body must ask for a single non-streamed answer, got %+v", seen.body["stream"])
	}
	messages, ok := seen.body["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("body must carry one user message, got %+v", seen.body["messages"])
	}
	message, _ := messages[0].(map[string]interface{})
	if message["role"] != "user" || message["content"] != "写一段摘要" {
		t.Fatalf("message must be the prompt, got %+v", message)
	}
	// `thinking` is a JSON **string** on this engine. Sent as a bool it answers
	// HTTP 200 with `code:400 json: cannot unmarshal bool into Go struct field
	// ChatCompletionsRequest.thinking of type string` (probed live 2026-11-18),
	// i.e. every section failed while the transport log said "200".
	if thinking, ok := seen.body["thinking"].(string); !ok || thinking != "false" {
		t.Fatalf("thinking must be the string %q, got %#v", "false", seen.body["thinking"])
	}
	// The answer is the text inside the envelope, unwrapped — including when the
	// model's own answer is JSON (the section writer parses it as JSON).
	if answer != `{"title":"摘要"}` {
		t.Fatalf("chat must return the unwrapped answer, got %q", answer)
	}
}

// TestChatToModelRefusesStreamingAndEmptyAnswers pins the two ways this call can
// look successful while delivering nothing usable.
func TestChatToModelRefusesStreamingAndEmptyAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, map[string]interface{}{"answer": "  "})
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 1)
	if _, err := client.ChatToModel(context.Background(),
		"GPUStack", "gpustack-local", "m", "hi", true, false); err == nil {
		t.Fatal("streaming must be refused: the envelope parser cannot read raw SSE")
	}
	_, err := client.ChatToModel(context.Background(),
		"GPUStack", "gpustack-local", "m", "hi", false, false)
	if err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("a blank answer envelope must fail, got %v", err)
	}
}

// TestChatToModelKeepsTheUpstreamReasonInItsError pins cause visibility.
//
// A wrapper that says only "chat to model X via Y" is undiagnosable: live, the
// section log carried exactly that line while the engine had actually answered
// `thinking of type string` — the wrapper dropped the cause, and with it the
// upstream code and message. `Error()` now appends the cause, so the operator
// sees what the engine said without re-running the call behind the app's back.
func TestChatToModelKeepsTheUpstreamReasonInItsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HTTP 200 carrying an envelope error — the shape the live engine uses for
		// a malformed request (e.g. a bool `thinking`).
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":400,"message":"json: cannot unmarshal bool into Go struct field ChatCompletionsRequest.thinking of type string"}`))
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 1)
	_, err := client.ChatToModel(context.Background(),
		"GPUStack", "gpustack-local", "deepseek-v4-flash-0731", "hi", false, false)
	if err == nil {
		t.Fatal("an envelope error at HTTP 200 must not look like a success")
	}
	msg := err.Error()
	for _, want := range []string{
		"chat to model deepseek-v4-flash-0731 via GPUStack/gpustack-local",
		"thinking of type string",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error must carry %q so the log is diagnosable, got %q", want, msg)
		}
	}
}
