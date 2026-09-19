package difydsl

import (
	"os"
	"strings"
	"testing"
)

const sampleDifyDSL = `
app:
  name: sample
  mode: advanced-chat
workflow:
  conversation_variables:
    - name: last_query
      value: ""
      value_type: string
      description: last query
  graph:
    nodes:
      - id: start
        position: {x: 0, y: 0}
        data: {type: start, title: Start, variables: []}
      - id: retrieve
        position: {x: 200, y: 0}
        data:
          type: knowledge-retrieval
          title: Retrieve
          query_variable_selector: [sys, query]
          dataset_ids: [dify-dataset]
          multiple_retrieval_config: {top_k: 12}
      - id: explain
        position: {x: 400, y: 0}
        data:
          type: llm
          title: Explain
          model:
            name: sample-model
            completion_params: {temperature: 0.2}
          context: {enabled: true, variable_selector: [retrieve, result]}
          prompt_template:
            - {role: system, text: "Answer using {{#context#}} for {{#sys.query#}}"}
      - id: branch
        position: {x: 600, y: 0}
        data:
          type: if-else
          title: Branch
          cases:
            - case_id: yes
              logical_operator: and
              conditions:
                - variable_selector: [explain, text]
                  comparison_operator: not empty
                  value: ""
      - id: answer
        position: {x: 800, y: 0}
        data: {type: answer, title: Answer, answer: "{{#explain.text#}}"}
    edges:
      - id: e1
        source: start
        sourceHandle: source
        target: retrieve
      - id: e2
        source: retrieve
        sourceHandle: source
        target: explain
      - id: e3
        source: explain
        sourceHandle: source
        target: branch
      - id: e4
        source: branch
        sourceHandle: yes
        target: answer
      - id: e5
        source: branch
        sourceHandle: false
        target: answer
`

func TestConvertMapsCanonicalRAGFlowOperators(t *testing.T) {
	result, err := Convert(strings.NewReader(sampleDifyDSL), Options{
		Title:          "Converted",
		LLMID:          "tenant-llm",
		DatasetMapping: DatasetMappings{"dify-dataset": "ragflow-dataset"},
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	components := result.DSL["components"].(map[string]any)
	expected := map[string]string{
		"begin":    labelBegin,
		"retrieve": labelRetrieval,
		"explain":  labelLLM,
		"branch":   labelSwitch,
		"answer":   labelMessage,
	}
	for id, label := range expected {
		component := components[id].(map[string]any)
		if got := component["obj"].(map[string]any)["component_name"]; got != label {
			t.Fatalf("component %s = %v, want %s", id, got, label)
		}
	}

	retrieval := components["retrieve"].(map[string]any)["obj"].(map[string]any)["params"].(map[string]any)
	if got := retrieval["dataset_ids"].([]any); len(got) != 1 || got[0] != "ragflow-dataset" {
		t.Fatalf("mapped dataset_ids = %#v", got)
	}
	if got := retrieval["query"]; got != "{sys.query}" {
		t.Fatalf("query = %v", got)
	}

	llm := components["explain"].(map[string]any)["obj"].(map[string]any)["params"].(map[string]any)
	if got := llm["sys_prompt"]; got != "Answer using {retrieve@formalized_content} for {sys.query}" {
		t.Fatalf("sys_prompt = %v", got)
	}
	if got := llm["llm_id"]; got != "tenant-llm" {
		t.Fatalf("llm_id = %v", got)
	}

	switchParams := components["branch"].(map[string]any)["obj"].(map[string]any)["params"].(map[string]any)
	conditions := switchParams["conditions"].([]any)
	if got := conditions[0].(map[string]any)["to"].([]string); len(got) != 1 || got[0] != "answer" {
		t.Fatalf("switch condition target = %#v", got)
	}
	if got := switchParams["end_cpn_ids"].([]string); len(got) != 1 || got[0] != "answer" {
		t.Fatalf("switch else target = %#v", got)
	}

	message := components["answer"].(map[string]any)["obj"].(map[string]any)["params"].(map[string]any)
	if got := message["content"].([]any); len(got) != 1 || got[0] != "{explain@content}" {
		t.Fatalf("message content = %#v", got)
	}

	if len(result.Report.UnmappedDatasets) != 0 {
		t.Fatalf("unmapped datasets = %#v", result.Report.UnmappedDatasets)
	}
}

func TestConvertRejectsDifyToolExplicitly(t *testing.T) {
	input := strings.ReplaceAll(sampleDifyDSL, "type: start, title: Start, variables: []", "type: tool, title: Tool")
	_, err := Convert(strings.NewReader(input), Options{})
	if err == nil || !strings.Contains(err.Error(), "cannot be converted") {
		t.Fatalf("Convert() error = %v, want explicit tool error", err)
	}
}

func TestConvertRealStatisticsWorkflow(t *testing.T) {
	path := "../../../test/安全生产风险点防控统计表问答-r2.yml"
	file, err := os.Open(path)
	if err != nil {
		t.Skipf("real Dify material is unavailable: %v", err)
	}
	defer file.Close()

	result, err := Convert(file, Options{
		Title:          "安全生产风险点防控统计表问答-r2",
		DatasetMapping: DatasetMappings{"dify": "ragflow"},
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if result.Report.NodeCount != 16 || result.Report.EdgeCount != 18 {
		t.Fatalf("report = %d nodes / %d edges, want 16 / 18", result.Report.NodeCount, result.Report.EdgeCount)
	}
	if len(result.Report.UnmappedDatasets) != 1 {
		t.Fatalf("unmapped datasets = %#v, want the real Dify dataset ID", result.Report.UnmappedDatasets)
	}
}
