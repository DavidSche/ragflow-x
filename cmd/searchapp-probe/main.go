// searchapp-probe is an on-demand retrieval/embedding-link canary for a
// single RAGFlow search app (doc/268 §3 P1-1). It asks one canary question and
// reports whether the engine actually retrieved references and produced an
// answer — the exact link that the A-arm tri-matrix audit proved was the
// failure surface (recovery without kNN fallback ⇒ hit=0 ⇒ silent empty
// answer). Intended to be run when embedding wiring changes (e.g. the local
// qwen3-embedding:0.6b switch that took the A arm from 10.4% to 89.7% hits):
//
//	RGX_RAGFLOW_LIVE_BASE_URL=http://…:9380 \
//	RGX_RAGFLOW_LIVE_API_KEY=… \
//	go run ./cmd/searchapp-probe \
//	  --search-app <id> --question "山东能源超一类企业的判定标准是?"
//
// It is strictly on-demand (one request) so it stays inside the local
// environment rule that forbids high-frequency loops against shared LLMs.
//
// Exit codes:
//
//	0  recovered  references found AND an answer was produced
//	2  no-hit     the engine reported success but retrieved nothing usable —
//	             check the embedding service / KB linkage (the i-0002 class)
//	3  error      the engine surfaced a structured/transport error
//	1  usage      missing arguments
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const (
	probeExitRecovered = 0
	probeExitUsage     = 1
	probeExitNoHit     = 2
	probeExitError     = 3
)

// probeReport is the machine-readable probe verdict the orchestrating
// wrapper (scripts/ or the 268 doc) archives alongside a wiring change.
type probeReport struct {
	ProbedAt      time.Time `json:"probed_at"`
	BaseURL       string    `json:"base_url"`
	SearchAppID   string    `json:"search_app_id"`
	Question      string    `json:"question"`
	Status        string    `json:"status"` // recovered | no-hit | error
	AnswerRunes   int       `json:"answer_runes"`
	ReferenceHits int       `json:"reference_hits"`
	DurationMs    int64     `json:"duration_ms"`
	Error         string    `json:"error,omitempty"`
}

// classifyProbe reduces a completion result to the small verdict set the
// exit code branches on. It is a pure function so the grading logic is unit
// testable without a live engine.
func classifyProbe(res *ragflow.SearchAppCompletionResult, err error) (status string, exit int) {
	if err != nil {
		return "error", probeExitError
	}
	if res == nil {
		return "error", probeExitError
	}
	if len(res.Answer) == 0 && len(res.Reference) == 0 {
		return "no-hit", probeExitNoHit
	}
	return "recovered", probeExitRecovered
}

func probeRun(args []string, getenv func(string) string) int {
	fs := flag.NewFlagSet("searchapp-probe", flag.ContinueOnError)
	baseURL := fs.String("base-url", getenv("RGX_RAGFLOW_LIVE_BASE_URL"), "RAGFlow API base URL (env RGX_RAGFLOW_LIVE_BASE_URL)")
	apiKey := fs.String("api-key", getenv("RGX_RAGFLOW_LIVE_API_KEY"), "RAGFlow API key (env RGX_RAGFLOW_LIVE_API_KEY)")
	searchAppID := fs.String("search-app", "", "search app id to probe")
	question := fs.String("question", "", "canary question expected to hit KB content")
	timeout := fs.Duration("timeout", 300*time.Second, "overall probe deadline")
	asJSON := fs.Bool("json", false, "emit the verdict as JSON")
	if err := fs.Parse(args); err != nil {
		return probeExitUsage
	}
	if strings.TrimSpace(*baseURL) == "" || strings.TrimSpace(*apiKey) == "" ||
		strings.TrimSpace(*searchAppID) == "" || strings.TrimSpace(*question) == "" {
		fmt.Fprintln(os.Stderr, "searchapp-probe: --base-url/--api-key/--search-app/--question are all required")
		fmt.Fprintln(os.Stderr, "  example: go run ./cmd/searchapp-probe --search-app s1 --question \"山东能源超一类企业判定标准是?\"")
		return probeExitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	start := time.Now()
	client := ragflow.NewHTTPClient(*baseURL, *apiKey, *timeout, 2)
	res, err := client.SearchAppCompletion(ctx, *searchAppID, ragflow.SearchAppCompletionRequest{Question: *question})
	duration := time.Since(start)

	status, exit := classifyProbe(res, err)
	report := probeReport{
		ProbedAt:    start,
		BaseURL:     *baseURL,
		SearchAppID: *searchAppID,
		Question:    *question,
		Status:      status,
		DurationMs:  duration.Milliseconds(),
	}
	if err != nil {
		report.Error = err.Error()
	}
	if res != nil {
		report.AnswerRunes = utf8.RuneCountInString(res.Answer)
		report.ReferenceHits = len(res.Reference)
	}

	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(report)
	} else {
		fmt.Printf("status      : %s\n", report.Status)
		fmt.Printf("answer_runes: %d\n", report.AnswerRunes)
		fmt.Printf("ref_hits    : %d\n", report.ReferenceHits)
		fmt.Printf("duration_ms : %d\n", report.DurationMs)
		if report.Error != "" {
			fmt.Printf("error       : %s\n", report.Error)
		}
	}
	return exit
}

func main() {
	os.Exit(probeRun(os.Args[1:], os.Getenv))
}
