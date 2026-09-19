package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/pkg/difydsl"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type datasetFlags map[string]string

func (m datasetFlags) String() string {
	pairs := make([]string, 0, len(m))
	for key, value := range m {
		pairs = append(pairs, key+"="+value)
	}
	return strings.Join(pairs, ",")
}

func (m datasetFlags) Set(value string) error {
	key, mapped, found := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	mapped = strings.TrimSpace(mapped)
	if !found || key == "" || mapped == "" {
		return fmt.Errorf("dataset mapping must use DifyID=RAGFlowDatasetID")
	}
	m[key] = mapped
	return nil
}

type toolMappingFlags map[string]difydsl.ToolMapping

func (m toolMappingFlags) String() string {
	pairs := make([]string, 0, len(m))
	for key := range m {
		pairs = append(pairs, key+"=<json>")
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (m toolMappingFlags) Set(value string) error {
	key, encoded, found := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	encoded = strings.TrimSpace(encoded)
	if !found || key == "" || encoded == "" {
		return fmt.Errorf("tool mapping must use ToolName={type,url,method,headers,body,timeout_sec}")
	}
	var mapping difydsl.ToolMapping
	if err := json.Unmarshal([]byte(encoded), &mapping); err != nil {
		return fmt.Errorf("decode tool mapping %q: %w", key, err)
	}
	m[key] = mapping
	return nil
}
func main() {
	input := flag.String("in", "", "Dify application YAML export")
	output := flag.String("out", "-", `output JSON file ("-" writes to stdout)`)
	title := flag.String("title", "", "RAGFlow agent title")
	llmID := flag.String("llm-id", "", "RAGFlow tenant LLM ID used by every converted LLM node")
	payload := flag.Bool("payload", false, "wrap the DSL in a POST /api/v1/agents create payload")
	importAgent := flag.Bool("import", false, "import the converted DSL into RAGFlow after conversion")
	baseURL := flag.String("base-url", os.Getenv("RGX_RAGFLOW_BASE_URL"), "RAGFlow base URL used with -import")
	release := flag.Bool("release", false, "release the imported RAGFlow agent")
	allowUnmapped := flag.Bool("allow-unmapped", false, "allow import when the report still contains unmapped datasets or tools")
	smokeQuery := flag.String("query", "", "send one non-streaming smoke query after import")
	sessionName := flag.String("session-name", "Dify DSL smoke", "RAGFlow session name used by the smoke query")
	timeout := flag.Duration("timeout", 30*time.Second, "RAGFlow HTTP timeout")
	datasets := datasetFlags{}
	flag.Var(&datasets, "dataset", "map a Dify knowledge base ID to a RAGFlow dataset ID (repeatable)")
	toolMappings := toolMappingFlags{}
	flag.Var(&toolMappings, "tool-mapping", `map a Dify tool name to an explicit HTTP bridge as NAME=JSON (repeatable)`)
	flag.Parse()

	if *input == "" {
		fmt.Fprintln(os.Stderr, "usage: difydsl -in <dify.yml> [-out <ragflow.json>] [-title ...] [-llm-id ...] [-dataset ...] [-tool-mapping ...] [-import -base-url ...]")
		os.Exit(2)
	}

	file, err := os.Open(*input)
	if err != nil {
		fail("open input", err)
	}
	defer file.Close()

	result, err := difydsl.Convert(file, difydsl.Options{
		Title:          *title,
		LLMID:          *llmID,
		DatasetMapping: difydsl.DatasetMappings(datasets),
		ToolMapping:    difydsl.ToolMappings(toolMappings),
		TavilyAPIKey:   os.Getenv("RAGFLOW_TAVILY_API_KEY"),
	})
	if err != nil {
		fail("convert Dify DSL", err)
	}

	if *importAgent {
		if err := importResult(*baseURL, os.Getenv("RGX_RAGFLOW_API_KEY"), *timeout, result, importOptions{
			Title:         *title,
			Release:       *release,
			AllowUnmapped: *allowUnmapped,
			Query:         *smokeQuery,
			SessionName:   *sessionName,
		}); err != nil {
			fail("import RAGFlow DSL", err)
		}
	}

	value := any(result.DSL)
	if *payload {
		value = map[string]any{"title": result.Report.Title, "dsl": result.DSL}
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fail("encode RAGFlow DSL", err)
	}
	encoded = append(encoded, '\n')
	if err := writeJSON(*output, encoded); err != nil {
		fail("write output", err)
	}

	report, err := json.MarshalIndent(result.Report, "", "  ")
	if err != nil {
		fail("encode conversion report", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", report)
}

func writeJSON(path string, content []byte) error {
	if path == "-" {
		_, err := os.Stdout.Write(content)
		return err
	}
	return os.WriteFile(path, content, 0o600)
}

type importOptions struct {
	Title         string
	Release       bool
	AllowUnmapped bool
	Query         string
	SessionName   string
}

func importResult(baseURL, apiKey string, timeout time.Duration, result difydsl.Result, options importOptions) error {
	if strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("RAGFlow base URL is required; set -base-url or RGX_RAGFLOW_BASE_URL")
	}
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("RAGFlow API key is required; set RGX_RAGFLOW_API_KEY")
	}
	title := strings.TrimSpace(options.Title)
	if title == "" {
		title = result.Report.Title
	}
	if title == "" {
		return fmt.Errorf("RAGFlow agent title is required")
	}
	if !options.AllowUnmapped && (len(result.Report.UnmappedDatasets) > 0 || len(result.Report.UnmappedTools) > 0) {
		return fmt.Errorf("refusing import with unmapped datasets %v or tools %v; map them first or pass -allow-unmapped explicitly", result.Report.UnmappedDatasets, result.Report.UnmappedTools)
	}
	client := ragflow.NewHTTPClientWithMiddleware(baseURL, apiKey, timeout, 20)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	created, err := client.CreateAgent(ctx, ragflow.CreateAgentRequest{
		Title:          title,
		Dsl:            result.DSL,
		Release:        options.Release,
		CanvasCategory: "agent_canvas",
	})
	if err != nil {
		return fmt.Errorf("create RAGFlow agent: %w", err)
	}
	output := map[string]any{
		"agent_id": created.ID,
		"title":    created.Title,
		"release":  created.Release,
	}
	if strings.TrimSpace(options.Query) != "" {
		session, err := client.CreateAgentSession(ctx, created.ID, strings.TrimSpace(options.SessionName))
		if err != nil {
			return fmt.Errorf("create RAGFlow agent session for agent %s: %w", created.ID, err)
		}
		completion, err := client.AgentChatCompletion(ctx, ragflow.CompletionRequest{
			ChatID:    created.ID,
			SessionID: session.ID,
			Messages:  []ragflow.Message{{Role: "user", Content: strings.TrimSpace(options.Query)}},
		})
		if err != nil {
			return fmt.Errorf("run RAGFlow smoke query for agent %s/session %s: %w", created.ID, session.ID, err)
		}
		answer := completion.Answer
		if answer == "" && len(completion.Choices) > 0 {
			answer = completion.Choices[0].Message.Content
		}
		output["session_id"] = session.ID
		output["smoke_status"] = "passed"
		output["answer"] = answer
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("encode import result: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", encoded)
	return nil
}
func fail(operation string, err error) {
	fmt.Fprintf(os.Stderr, "difydsl: %s: %v\n", operation, err)
	os.Exit(1)
}
