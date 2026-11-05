// chatdrill performs a single-round live chat completion against a RAGFlow
// engine through the platform's own adapter code path.
//
// It exists to re-validate the rc3 chat/session/reference contract on a
// real engine without spinning up the full platform stack. Each invocation
// performs exactly ONE completion round-trip ($RGX_DRILL_STREAM selects
// non-stream vs SSE), then removes the temporary session.
//
// Red lines honoured: single call per invocation, no concurrency, uses the
// chat binding configured on the target chat (expected to be an external AMD
// model; the drill fails loudly if the resolved model is NOT radeon-external).
//
// Usage:
//
//	RGX_RAGFLOW_LIVE_BASE_URL=... RGX_RAGFLOW_LIVE_API_KEY=... \
//	  go run ./cmd/chatdrill -chat <engine-chat-id>
//	# optional: -stream=1, -question "..." (default), -keep-session=1 (debug)
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type verdict struct {
	ok   bool
	name string
	line string
}

func main() {
	var chatID string
	var stream bool
	var keep bool
	var question string
	for i := 0; i < len(os.Args); i++ {
		switch {
		case os.Args[i] == "-chat" && i+1 < len(os.Args):
			chatID, i = os.Args[i+1], i+1
		case os.Args[i] == "-stream":
			if i+1 < len(os.Args) {
				stream, i = os.Args[i+1] == "1", i+1
			}
		case os.Args[i] == "-keep-session":
			keep, i = os.Args[i] == "1", i
		case os.Args[i] == "-question" && i+1 < len(os.Args):
			question, i = os.Args[i+1], i+1
		case os.Args[i] == "-h":
			fmt.Println("chatdrill: single-round live chat contract drill (rc3 revalidation)")
			fmt.Println("  env RGX_RAGFLOW_LIVE_BASE_URL, RGX_RAGFLOW_LIVE_API_KEY")
			fmt.Println("  flags: -chat <id> -stream {0|1} -keep-session {0|1} -question <str>")
			return
		}
	}
	if chatID == "" {
		fmt.Println("chatdrill: missing -chat")
		os.Exit(2)
	}
	base := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	key := os.Getenv("RGX_RAGFLOW_LIVE_API_KEY")
	if base == "" || key == "" {
		fmt.Println("chatdrill: missing env RGX_RAGFLOW_LIVE_BASE_URL / RGX_RAGFLOW_LIVE_API_KEY")
		os.Exit(2)
	}
	if question == "" {
		question = "用自己的话说明安全生产费用的用途和管理要求要点"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client := ragflow.NewHTTPClient(base, key, 110*time.Second, 1)

	pass := func(name, line string) { fmt.Printf("PASS|%s|%s\n", name, line) }
	fail := func(name, line string) { fmt.Printf("FAIL|%s|%s\n", name, line) }
	ok := true
	failIf := func(cond bool, name, line string) { if cond { ok = false; fail(name, line) } }

	// 1) contract: list chats and confirm this chat's LLM binding is external (AMD).
	chats, err := client.ListChats(ctx)
	failIf(err != nil, "ListChats", fmt.Sprint(err))
	bound := ""
	for _, c := range chats {
		if c.ID == chatID {
			bound = c.LLMID
		}
	}
	failIf(bound == "", "ChatBinding", "chat "+chatID+" not found in /chats")
	failIf(!strings.Contains(bound, "radeon-external"), "ChatBindingAMD",
		fmt.Sprintf("resolved LLM=%q must be radeon-external (red line: no 31.211)", bound))
	pass("ChatBinding", "resolved LLM="+bound)

	// 2) create a disposable session.
	sess, err := client.CreateChatSession(ctx, chatID, "rc3-revalidate-"+time.Now().Format("150405"))
	failIf(err != nil, "CreateChatSession", fmt.Sprint(err))
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
		pass("CreateChatSession", "session="+sessionID)
	}
	defer func() {
		if sessionID == "" || keep {
			return
		}
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		if err := client.DeleteChatSessions(dctx, chatID, []string{sessionID}); err != nil {
			fmt.Println("WARN|DeleteChatSession|" + fmt.Sprint(err))
		} else {
			fmt.Println("OK|DeleteChatSession|session removed")
		}
	}()

	req := ragflow.CompletionRequest{
		SessionID: sessionID,
		Messages:  []ragflow.Message{{Role: "user", Content: question}},
	}

	if stream {
		// 2b/3) single SSE completion round.
		var buf bytes.Buffer
		started := time.Now()
		err = client.StreamChatCompletion(ctx, chatID, req, &buf)
		cost := time.Since(started).Milliseconds()
		failIf(err != nil, "StreamChatCompletion", fmt.Sprintf("%v (%dms)", err, cost))
		if err == nil {
			lines, datas, tok := summarizeSSE(buf.Bytes())
			refs := countReference(buf.Bytes())
			pass("StreamChatCompletion", fmt.Sprintf("%dms %d lines %d data-events %s-role", cost, lines, datas, tok))
			failIf(datas == 0, "StreamSSEEvents", "no data lines in SSE body")
			failIf(!strings.Contains(tok, "assistant") && !bytes.Contains(buf.Bytes(), []byte("answer")), "StreamSSEContent",
				"SSE body lacks assistant/answer content")
			if datas > 0 {
				pass("StreamSSEChunks", fmt.Sprintf("%d data events received", datas))
			}
			if refs == 0 {
				fail("StreamSSEReference", "no reference/citation keys in SSE payload")
				ok = false
			} else {
				pass("StreamSSEReference", fmt.Sprintf("%d reference/citation keys present in SSE data events", refs))
			}
		}
		// 4) post-hoc: session now holds the exchange + references.
		verifySession(ctx, client, chatID, sessionID, pass, failIf, &ok)
	} else {
		// 3) single non-stream completion round.
		started := time.Now()
		resp, err := client.ChatCompletion(ctx, chatID, req)
		cost := time.Since(started).Milliseconds()
		failIf(err != nil, "ChatCompletion", fmt.Sprintf("%v (%dms)", err, cost))
		if err == nil {
			pass("ChatCompletion", fmt.Sprintf("%dms model=%s", cost, resp.Model))
			failIf(resp.Model == "", "CompletionModel", "empty model on response")
			failIf(!strings.Contains(resp.Model, "radeon-external"), "CompletionModelAMD",
				fmt.Sprintf("completion ran on %q, expected radeon-external (red line)", resp.Model))
			ans := ""
			if len(resp.Choices) > 0 {
				ans = resp.Choices[0].Message.Content
			}
			if ans == "" {
				ans = resp.Answer
			}
			failIf(strings.TrimSpace(ans) == "", "CompletionAnswer", "empty answer")
			if strings.TrimSpace(ans) != "" {
				pass("CompletionAnswer", truncate(ans, 120))
			}
			if resp.Usage != nil {
				pass("CompletionUsage", fmt.Sprintf("prompt=%d completion=%d", resp.Usage.PromptTokens, resp.Usage.CompletionTokens))
			}
		}
		verifySession(ctx, client, chatID, sessionID, pass, failIf, &ok)
	}

	if ok {
		fmt.Println("RESULT=ALL_PASS")
	} else {
		fmt.Println("RESULT=FAIL")
		os.Exit(1)
	}
}

func verifySession(ctx context.Context, client *ragflow.HTTPClient, chatID, sessionID string, pass func(string, string), failIf func(bool, string, string), ok *bool) {
	s, err := client.GetChatSession(ctx, chatID, sessionID)
	failIf(err != nil, "GetChatSession", fmt.Sprint(err))
	if err != nil {
		return
	}
	failIf(len(s.Messages) < 2, "SessionMessages",
		fmt.Sprintf("expected >=2 messages (user+assistant), got %d", len(s.Messages)))
	if len(s.Messages) >= 2 {
		pass("SessionMessages", fmt.Sprintf("%d messages recorded", len(s.Messages)))
	}
	failIf(len(s.Reference) == 0, "SessionReferences", "no reference (retrieval evidence) present")
	if len(s.Reference) > 0 {
		pass("SessionReferences", fmt.Sprintf("%d reference entries (source_doc_ids projection route)", len(s.Reference)))
	}
	if len(s.Reference) > 0 {
		b, _ := json.Marshal(s.Reference[0])
		pass("SessionReferencesMap", truncate(string(b), 200))
	}
}

func summarizeSSE(raw []byte) (lines, dataEvents int, marker string) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		l := sc.Text()
		lines++
		if strings.HasPrefix(l, "data:") && strings.TrimSpace(strings.TrimPrefix(l, "data:")) != "" {
			dataEvents++
			var v map[string]interface{}
			if json.Unmarshal([]byte(strings.TrimPrefix(l, "data:")), &v) == nil {
				if role, _ := v["role"].(string); role != "" {
					marker = role
				}
			}
		}
	}
	return
}

func countReference(raw []byte) int {
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		l := sc.Text()
		if !strings.HasPrefix(l, "data:") {
			continue
		}
		var v map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimPrefix(l, "data:")), &v) != nil {
			continue
		}
		if _, ok := v["reference"]; ok {
			n++
		}
		if _, ok := v["citations"]; ok {
			n++
		}
		if arr, ok := v["reference"].([]map[string]interface{}); ok && len(arr) > 0 {
			n += len(arr)
		}
	}
	if n == 0 {
		for _, marker := range []string{"\"reference\"", "\"citations\"", "\"source"} {
			if bytes.Contains(raw, []byte(marker)) {
				n++
				break
			}
		}
	}
	return n
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

var _ = io.Discard
