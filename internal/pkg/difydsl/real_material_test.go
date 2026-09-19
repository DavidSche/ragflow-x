package difydsl

import (
	"os"
	"strings"
	"testing"
)

func TestConvertRealInlineAgentWithTavily(t *testing.T) {
	file, err := os.Open("../../../test/省属企业安全生产智能问答-r1.yml")
	if err != nil {
		t.Skipf("real Dify material is unavailable: %v", err)
	}
	defer file.Close()

	result, err := Convert(file, Options{LLMID: "tenant-llm", TavilyAPIKey: "test-key"})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	components := result.DSL["components"].(map[string]any)
	agent, ok := components["1787290968752"].(map[string]any)
	if !ok {
		t.Fatalf("agent component 1787290968752 is missing")
	}
	params := agent["obj"].(map[string]any)["params"].(map[string]any)
	if params["llm_id"] != "tenant-llm" {
		t.Fatalf("agent llm_id = %v", params["llm_id"])
	}
	tools, ok := params["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("agent tools = %#v, want one TavilySearch", params["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["component_name"] != labelTavilySearch {
		t.Fatalf("tool component = %#v", tool)
	}
	apiKey := tool["params"].(map[string]any)["api_key"]
	if apiKey != "test-key" {
		t.Fatalf("Tavily api_key = %#v", apiKey)
	}
	if result.Report.MappedTools[0] != "langgenius/tavily/tavily/tavily_search" {
		t.Fatalf("mapped tools = %#v", result.Report.MappedTools)
	}
}

func TestConvertWorkflowToolWithExplicitHTTPMapping(t *testing.T) {
	file, err := os.Open("../../../test/wy_安全督导检查问题_V5.yml")
	if err != nil {
		t.Skipf("real Dify material is unavailable: %v", err)
	}
	defer file.Close()

	result, err := Convert(file, Options{
		LLMID: "tenant-llm",
		ToolMapping: ToolMappings{"wy_0828_json_v6": {
			Type:       "http",
			URL:        "http://tool.example/run",
			Method:     "POST",
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       map[string]string{"input": "{sys.query}"},
			TimeoutSec: 30,
		}},
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	components := result.DSL["components"].(map[string]any)
	tool, ok := components["1788157073005"].(map[string]any)
	if !ok {
		t.Fatalf("tool component is missing")
	}
	params := tool["obj"].(map[string]any)["params"].(map[string]any)
	if params["url"] != "http://tool.example/run" || params["method"] != "POST" {
		t.Fatalf("Invoke mapping = %#v", params)
	}
	variables := params["variables"].([]any)
	if len(variables) != 1 || variables[0].(map[string]any)["value"] != "{sys.query}" {
		t.Fatalf("Invoke variables = %#v", variables)
	}
	if result.Report.MappedTools[0] != "5f3ec677-e8c3-4ef7-a73d-a044a70395f9/wy_0828_json_v6" {
		t.Fatalf("mapped tools = %#v", result.Report.MappedTools)
	}
}

func TestConvertUnmappedWorkflowToolFailsExplicitly(t *testing.T) {
	file, err := os.Open("../../../test/wy_安全督导检查问题_V5.yml")
	if err != nil {
		t.Skipf("real Dify material is unavailable: %v", err)
	}
	defer file.Close()

	_, err = Convert(file, Options{LLMID: "tenant-llm"})
	if err == nil || !strings.Contains(err.Error(), "cannot be converted") {
		t.Fatalf("Convert() error = %v, want explicit tool error", err)
	}
}

func TestConvertToolParameterSelectorsAndConstants(t *testing.T) {
	c := &converter{}
	value, err := c.toolParameterValue(map[string]any{
		"type":  "variable",
		"value": []any{"sys", "query"},
	})
	if err != nil || value != "{sys.query}" {
		t.Fatalf("variable parameter = %#v, %v", value, err)
	}
	value, err = c.toolParameterValue(map[string]any{
		"type":  "constant",
		"value": "fixed",
	})
	if err != nil || value != "fixed" {
		t.Fatalf("constant parameter = %#v, %v", value, err)
	}
	if _, err = c.toolParameterValue(map[string]any{"type": "unsupported", "value": "x"}); err == nil {
		t.Fatal("unsupported parameter type did not fail")
	}
}
