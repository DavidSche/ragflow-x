package difydsl

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	labelBegin              = "Begin"
	labelLLM                = "LLM"
	labelRetrieval          = "Retrieval"
	labelCodeExec           = "CodeExec"
	labelSwitch             = "Switch"
	labelMessage            = "Message"
	labelInvoke             = "Invoke"
	labelVariableAggregator = "VariableAggregator"
	labelVariableAssigner   = "VariableAssigner"
	labelAgent              = "Agent"
	labelTavilySearch       = "TavilySearch"
)

type DatasetMappings map[string]string

type ToolMapping struct {
	Type       string            `json:"type"`
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       map[string]string `json:"body,omitempty"`
	TimeoutSec int               `json:"timeout_sec,omitempty"`
}

type ToolMappings map[string]ToolMapping

type Options struct {
	Title          string
	LLMID          string
	DatasetMapping DatasetMappings
	ToolMapping    ToolMappings
	TavilyAPIKey   string
}

type Report struct {
	Title            string   `json:"title"`
	NodeCount        int      `json:"node_count"`
	EdgeCount        int      `json:"edge_count"`
	MappedDatasets   []string `json:"mapped_datasets,omitempty"`
	UnmappedDatasets []string `json:"unmapped_datasets,omitempty"`
	MappedTools      []string `json:"mapped_tools,omitempty"`
	UnmappedTools    []string `json:"unmapped_tools,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type Result struct {
	DSL    map[string]any `json:"dsl"`
	Report Report         `json:"report"`
}

type converter struct {
	options         Options
	nodes           []map[string]any
	edges           []map[string]any
	byID            map[string]map[string]any
	raw             map[string]any
	mappedToolKeys  map[string]bool
	unmappedToolIDs map[string]bool
	report          Report
}

func Convert(input io.Reader, options Options) (Result, error) {
	var raw map[string]any
	if err := yaml.NewDecoder(input).Decode(&raw); err != nil {
		return Result{}, fmt.Errorf("decode Dify YAML: %w", err)
	}
	mode := stringAt(raw, "app", "mode")
	if mode != "advanced-chat" {
		return Result{}, fmt.Errorf("only Dify advanced-chat workflows are supported; got %q", mode)
	}
	graph := mapAt(raw, "workflow", "graph")
	if graph == nil {
		return Result{}, fmt.Errorf("Dify DSL has no workflow.graph")
	}
	if options.DatasetMapping == nil {
		options.DatasetMapping = DatasetMappings{}
	}
	if options.ToolMapping == nil {
		options.ToolMapping = ToolMappings{}
	}
	c := &converter{
		options:         options,
		byID:            map[string]map[string]any{},
		raw:             raw,
		mappedToolKeys:  map[string]bool{},
		unmappedToolIDs: map[string]bool{},
		report:          Report{Title: options.Title},
	}
	if c.report.Title == "" {
		c.report.Title = stringAt(raw, "app", "name")
	}
	for _, item := range sliceAt(graph, "nodes") {
		node, ok := item.(map[string]any)
		if !ok {
			return Result{}, fmt.Errorf("workflow.graph.nodes contains a non-object node")
		}
		id := stringAt(node, "id")
		if id == "" {
			return Result{}, fmt.Errorf("Dify node has no id")
		}
		if _, exists := c.byID[id]; exists {
			return Result{}, fmt.Errorf("duplicate Dify node id %q", id)
		}
		c.nodes = append(c.nodes, node)
		c.byID[id] = node
	}
	for _, item := range sliceAt(graph, "edges") {
		edge, ok := item.(map[string]any)
		if !ok {
			return Result{}, fmt.Errorf("workflow.graph.edges contains a non-object edge")
		}
		c.edges = append(c.edges, edge)
	}
	if len(c.nodes) == 0 {
		return Result{}, fmt.Errorf("Dify workflow has no nodes")
	}
	if err := c.validateEdges(); err != nil {
		return Result{}, err
	}
	dsl, err := c.buildDSL(raw)
	if err != nil {
		return Result{}, err
	}
	if err := validateGraph(dsl); err != nil {
		return Result{}, err
	}
	c.report.NodeCount = len(c.nodes)
	c.report.EdgeCount = len(c.edges)
	c.report.MappedDatasets = sortedKeys(options.DatasetMapping)
	c.report.UnmappedDatasets = c.unmappedDatasets()
	c.report.MappedTools = c.mappedTools()
	c.report.UnmappedTools = c.unmappedTools()
	return Result{DSL: dsl, Report: c.report}, nil
}
func (c *converter) buildDSL(raw map[string]any) (map[string]any, error) {
	graphNodes := make([]map[string]any, 0, len(c.nodes))
	graphEdges := make([]map[string]any, 0, len(c.edges))
	components := map[string]any{}
	for _, node := range c.nodes {
		id := c.ragID(stringAt(node, "id"))
		data := mapAt(node, "data")
		difyType := stringAt(data, "type")
		title := stringAt(data, "title")
		if title == "" {
			title = difyType
		}
		label, nodeType, form, err := c.convertNode(id, data)
		if err != nil {
			return nil, fmt.Errorf("node %s (%s): %w", id, title, err)
		}
		graphNodes = append(graphNodes, map[string]any{
			"id": id, "type": nodeType, "position": mapAt(node, "position"),
			"data":           map[string]any{"label": label, "name": title, "form": form},
			"sourcePosition": "right", "targetPosition": "left",
		})
		components[id] = map[string]any{
			"obj":        map[string]any{"component_name": label, "params": form},
			"downstream": c.downstream(id),
			"upstream":   c.upstream(id),
		}
	}
	for _, edge := range c.edges {
		graphEdges = append(graphEdges, map[string]any{
			"id": edgeID(edge), "source": c.ragID(stringAt(edge, "source")), "target": c.ragID(stringAt(edge, "target")),
			"type": "edge", "sourceHandle": edgeHandle(edge),
			"targetHandle":   firstNonEmpty(anyString(edge["targetHandle"]), "target"),
			"sourcePosition": "right", "targetPosition": "left",
		})
	}
	globals, variables, err := c.convertGlobals(mapSliceAt(raw, "workflow", "conversation_variables"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": "difydsl-" + fmt.Sprint(len(c.nodes)), "title": c.report.Title, "type": "agent_canvas", "version": "0.1.0",
		"graph":      map[string]any{"nodes": toAnySlice(graphNodes), "edges": toAnySlice(graphEdges)},
		"components": components, "globals": globals, "variables": variables,
		"history": []any{}, "retrieval": []any{}, "path": []any{"begin"}, "messages": []any{},
	}, nil
}

func edgeID(edge map[string]any) string {
	return firstNonEmpty(anyString(edge["id"]), stringAt(edge, "source")+"-"+stringAt(edge, "target"))
}

func (c *converter) ragID(id string) string {
	if id == "" {
		return ""
	}
	if stringAt(mapAt(c.byID[id], "data"), "type") == "start" {
		return "begin"
	}
	return id
}

func (c *converter) convertNode(id string, data map[string]any) (string, string, map[string]any, error) {
	switch stringAt(data, "type") {
	case "start":
		if len(sliceAt(data, "variables")) != 0 {
			return "", "", nil, fmt.Errorf("Dify start variables have no exact Begin input equivalent")
		}
		return labelBegin, "beginNode", map[string]any{
			"mode": "conversational", "prologue": "", "enablePrologue": false,
			"description": "", "user_prompt": "This is the order you need to send to the agent.",
			"reasoning": "Explain why this agent is invoked and what is expected.",
			"context":   "All relevant background information needed by the agent.",
		}, nil
	case "llm":
		return labelLLM, "llmNode", c.convertLLM(id, data), nil
	case "knowledge-retrieval":
		return labelRetrieval, "retrievalNode", c.convertRetrieval(id, data), nil
	case "code":
		return labelCodeExec, "ragNode", c.convertCode(data), nil
	case "if-else":
		return c.convertSwitch(id, data)
	case "answer":
		return c.convertMessage(data)
	case "template-transform":
		return labelCodeExec, "ragNode", c.convertTemplate(data), nil
	case "http-request":
		return labelInvoke, "ragNode", c.convertInvoke(data), nil
	case "variable-aggregator":
		return labelVariableAggregator, "variableAggregatorNode", c.convertVariableAggregator(data), nil
	case "assigner":
		return labelVariableAssigner, "variableAssignerNode", c.convertVariableAssigner(data), nil
	case "tool":
		form, err := c.convertTool(data)
		return labelInvoke, "ragNode", form, err
	case "agent":
		form, err := c.convertAgent(data)
		return labelAgent, "agentNode", form, err
	default:
		return "", "", nil, fmt.Errorf("unsupported Dify node type %q", stringAt(data, "type"))
	}
}

func (c *converter) convertLLM(id string, data map[string]any) map[string]any {
	model := mapAt(data, "model")
	completion := mapAt(model, "completion_params")
	llmID := c.options.LLMID
	if llmID == "" {
		llmID = stringAt(model, "name")
		c.warn(fmt.Sprintf("LLM %s uses Dify model name %q as llm_id; override it with --llm-id for this RAGFlow tenant", id, llmID))
	}
	contextRef := ""
	context := mapAt(data, "context")
	if boolAt(context, "enabled") {
		contextRef = c.reference(sliceAt(context, "variable_selector"), mapComponentOutput)
	}
	sysParts := make([]string, 0)
	prompts := make([]any, 0)
	for _, item := range sliceAt(data, "prompt_template") {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := stringAt(message, "role")
		text := c.renderTemplate(stringAt(message, "text"), contextRef)
		if role == "system" {
			sysParts = append(sysParts, text)
			continue
		}
		prompts = append(prompts, map[string]any{"role": role, "content": text})
	}
	if len(prompts) == 0 {
		prompts = append(prompts, map[string]any{"role": "user", "content": "{sys.query}"})
	}
	return map[string]any{
		"llm_id": llmID, "sys_prompt": strings.Join(sysParts, "\n\n"), "prompts": prompts,
		"max_tokens": 2048, "maxTokensEnabled": true,
		"temperature": floatOr(completion, "temperature", 0.1), "temperatureEnabled": true,
		"top_p": floatOr(completion, "top_p", 0.3), "topPEnabled": false,
		"presence_penalty": 0.1, "presencePenaltyEnabled": false,
		"frequency_penalty": 0.4, "frequencyPenaltyEnabled": false,
		"cite": true, "visual_files_var": "",
		"message_history_window_size": intOr(mapAt(data, "memory", "window"), "size", 6),
		"max_retries":                 3, "delay_after_error": 1, "max_rounds": 1,
	}
}

func (c *converter) convertAgent(data map[string]any) (map[string]any, error) {
	binding := mapAt(data, "agent_binding")
	if binding == nil || stringAt(binding, "binding_type") != "inline_agent" {
		return nil, fmt.Errorf("only Dify inline agents can be mapped to RAGFlow Agent")
	}
	packageRef := stringAt(binding, "package_ref")
	if packageRef == "" {
		return nil, fmt.Errorf("Dify agent node has no agent_binding.package_ref")
	}
	agentPackage := mapAt(c.raw, "agent_packages", packageRef)
	if agentPackage == nil {
		return nil, fmt.Errorf("Dify agent package %q does not exist", packageRef)
	}
	model := mapAt(agentPackage, "soul", "model")
	modelSettings := mapAt(model, "model_settings")
	llmID := c.options.LLMID
	if llmID == "" {
		llmID = stringAt(model, "model")
		c.warn(fmt.Sprintf("Dify agent package %s uses Dify model name %q as llm_id; override it with --llm-id for this RAGFlow tenant", packageRef, llmID))
	}
	sysPrompt := renderDifyToolReferences(stringAt(agentPackage, "soul", "prompt", "system_prompt"))
	if sysPrompt == "" {
		return nil, fmt.Errorf("Dify agent package %q has no system prompt", packageRef)
	}
	tools := make([]any, 0, len(sliceAt(agentPackage, "soul", "tools", "dify_tools")))
	for _, item := range sliceAt(agentPackage, "soul", "tools", "dify_tools") {
		tool, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Dify agent package %q contains a non-object dify_tools entry", packageRef)
		}
		mappedTool, err := c.convertAgentTool(tool)
		if err != nil {
			return nil, fmt.Errorf("agent package %q: %w", packageRef, err)
		}
		tools = append(tools, mappedTool)
	}
	return map[string]any{
		"llm_id": llmID, "sys_prompt": sysPrompt,
		"prompts":    []any{map[string]any{"role": "user", "content": "{sys.query}"}},
		"max_tokens": intOr(modelSettings, "max_tokens", 2048), "maxTokensEnabled": true,
		"temperature": floatOr(modelSettings, "temperature", 0.1), "temperatureEnabled": true,
		"top_p": floatOr(modelSettings, "top_p", 0.3), "topPEnabled": false,
		"presence_penalty": 0.1, "presencePenaltyEnabled": false,
		"frequency_penalty": 0.4, "frequencyPenaltyEnabled": false,
		"cite": false, "visual_files_var": "", "message_history_window_size": 6,
		"max_retries": 3, "delay_after_error": 1, "max_rounds": 5, "mcp": []any{},
		"tools": tools, "outputs": map[string]any{"content": map[string]any{"type": "string", "value": ""}},
	}, nil
}

func (c *converter) convertAgentTool(tool map[string]any) (map[string]any, error) {
	toolName := stringAt(tool, "tool_name")
	toolLabel := firstNonEmpty(stringAt(tool, "tool_label"), toolName)
	providerID := stringAt(tool, "provider_id")
	mappingKey := toolName
	if providerID != "" {
		mappingKey = providerID + "/" + toolName
	}
	mapping, mapped := c.options.ToolMapping[mappingKey]
	if !mapped && providerID != "" {
		mapping, mapped = c.options.ToolMapping[toolName]
	}
	if mapped {
		c.markMappedTool(mappingKey)
	}
	if strings.EqualFold(toolName, "tavily_search") {
		c.markMappedTool(mappingKey)
		if tool["enabled"] == false {
			return nil, fmt.Errorf("disabled Dify tool %q must be removed before conversion", toolLabel)
		}
		if mapped && mapping.Type != "http" {
			return nil, fmt.Errorf("tool mapping for %q must use type http; got %q", toolLabel, mapping.Type)
		}
		if c.options.TavilyAPIKey == "" {
			c.warn("TavilySearch has no API key; set RAGFLOW_TAVILY_API_KEY and import the converted DSL into a tenant with Tavily enabled")
		}
		return map[string]any{
			"component_name": labelTavilySearch, "name": labelTavilySearch,
			"id": labelTavilySearch + ":" + safeToolID(mappingKey),
			"params": map[string]any{
				"api_key": c.options.TavilyAPIKey, "days": 7,
				"exclude_domains": []any{}, "include_answer": false, "include_domains": []any{},
				"include_image_descriptions": false, "include_images": false, "include_raw_content": false,
				"max_results": 6,
				"outputs": map[string]any{
					"formalized_content": map[string]any{"type": "string", "value": ""},
					"json":               map[string]any{"type": "Array<Object>", "value": []any{}},
				},
				"query": "sys.query", "search_depth": "basic", "topic": "general",
			},
		}, nil
	}
	if !mapped {
		c.markUnmappedTool(toolName, providerID)
		return nil, fmt.Errorf("Dify agent tool %q cannot be converted without an explicit HTTP mapping", toolLabel)
	}
	params, err := c.invokeParamsFromMapping(mapping, mapAt(tool, "runtime_parameters"))
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", toolLabel, err)
	}
	return map[string]any{
		"component_name": labelInvoke, "name": labelInvoke,
		"id": labelInvoke + ":" + safeToolID(mappingKey), "params": params,
	}, nil
}

func (c *converter) convertTool(data map[string]any) (map[string]any, error) {
	toolName := firstNonEmpty(stringAt(data, "tool_name"), stringAt(data, "tool_label"), "Dify tool")
	providerID := stringAt(data, "provider_id")
	mappingKey := toolName
	if providerID != "" {
		mappingKey = providerID + "/" + toolName
	}
	mapping, mapped := c.options.ToolMapping[mappingKey]
	if !mapped && providerID != "" {
		mapping, mapped = c.options.ToolMapping[toolName]
	}
	if !mapped {
		c.markUnmappedTool(toolName, providerID)
		return nil, fmt.Errorf("Dify plugin tool %q cannot be converted without changing its runtime contract", toolName)
	}
	c.markMappedTool(mappingKey)
	return c.invokeParamsFromMapping(mapping, mapAt(data, "tool_parameters"))
}

func (c *converter) invokeParamsFromMapping(mapping ToolMapping, parameters map[string]any) (map[string]any, error) {
	if mapping.Type != "http" {
		return nil, fmt.Errorf("tool mapping type must be http; got %q", mapping.Type)
	}
	if mapping.URL == "" {
		return nil, fmt.Errorf("tool mapping URL is required")
	}
	if !isHTTPURL(mapping.URL) {
		return nil, fmt.Errorf("tool mapping URL must be an absolute HTTP or HTTPS URL; got %q", mapping.URL)
	}
	method := strings.ToUpper(mapping.Method)
	switch method {
	case "GET", "POST", "PUT":
	default:
		return nil, fmt.Errorf("tool mapping method must be GET, POST, or PUT; got %q", mapping.Method)
	}
	variables := make([]any, 0, len(mapping.Body)+len(parameters))
	seen := map[string]bool{}
	appendVariable := func(key, value string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		variables = append(variables, map[string]any{"key": key, "value": c.renderTemplate(value, "")})
	}
	for _, key := range sortedKeys(mapping.Body) {
		appendVariable(key, mapping.Body[key])
	}
	headers := make(map[string]any, len(mapping.Headers))
	for key, value := range mapping.Headers {
		if key != "" {
			headers[key] = value
		}
	}
	parameterKeys := make([]string, 0, len(parameters))
	for key := range parameters {
		parameterKeys = append(parameterKeys, key)
	}
	sort.Strings(parameterKeys)
	for _, key := range parameterKeys {
		value, err := c.toolParameterValue(parameters[key])
		if err != nil {
			return nil, fmt.Errorf("tool parameter %q: %w", key, err)
		}
		appendVariable(key, value)
	}
	headerJSON, err := json.Marshal(headers)
	if err != nil {
		return nil, fmt.Errorf("encode tool mapping headers: %w", err)
	}
	timeout := mapping.TimeoutSec
	if timeout <= 0 {
		timeout = 60
	}
	if isInternalURL(mapping.URL) {
		c.warn("HTTP tool mapping targets a private or loopback address; RAGFlow SSRF guard blocks it unless ALLOW_ANY_HOST=true is explicitly enabled for the trusted pilot environment")
	}
	return map[string]any{
		"url": mapping.URL, "method": method, "headers": string(headerJSON), "variables": variables,
		"timeout": timeout, "datatype": "json", "clean_html": false, "proxy": "",
		"outputs": map[string]any{"result": map[string]any{"type": "String", "value": ""}},
	}, nil
}

func (c *converter) toolParameterValue(value any) (string, error) {
	switch item := value.(type) {
	case nil:
		return "", nil
	case string:
		return c.renderTemplate(item, ""), nil
	case []any:
		return "{" + c.reference(item, mapComponentOutput) + "}", nil
	case map[string]any:
		parameterType := stringAt(item, "type")
		switch parameterType {
		case "", "constant":
			switch raw := item["value"].(type) {
			case nil:
				return "", nil
			case string:
				return c.renderTemplate(raw, ""), nil
			default:
				return anyString(raw), nil
			}
		case "variable":
			selector, ok := item["value"].([]any)
			if !ok {
				return "", fmt.Errorf("variable selector must be a list")
			}
			return "{" + c.reference(selector, mapComponentOutput) + "}", nil
		default:
			return "", fmt.Errorf("unsupported type %q", parameterType)
		}
	default:
		return anyString(item), nil
	}
}

func (c *converter) convertRetrieval(id string, data map[string]any) map[string]any {
	queryRef := c.reference(sliceAt(data, "query_variable_selector"), mapComponentOutput)
	if queryRef == "" {
		queryRef = "sys.query"
	}
	queryRef = "{" + queryRef + "}"
	datasets := make([]any, 0)
	for _, item := range sliceAt(data, "dataset_ids") {
		difyID, ok := item.(string)
		if !ok || difyID == "" {
			continue
		}
		if mapped, exists := c.options.DatasetMapping[difyID]; exists && mapped != "" {
			datasets = append(datasets, mapped)
		} else {
			datasets = append(datasets, difyID)
		}
	}
	config := mapAt(data, "multiple_retrieval_config")
	vectorWeight := 0.5
	if weights := mapAt(config, "weights"); weights != nil {
		vectorWeight = floatOr(mapAt(weights, "vector_setting"), "vector_weight", 0)
		if vectorWeight == 0 {
			vectorWeight = 1 - floatOr(mapAt(weights, "keyword_setting"), "keyword_weight", 0)
		}
	}
	topK := intOr(config, "top_k", 4)
	if topK < 1 {
		topK = 4
	}
	similarity := floatOr(config, "score_threshold", 0.2)
	return map[string]any{
		"query": queryRef, "dataset_ids": datasets, "top_k": topK,
		"vector_similarity_weight": 1 - vectorWeight, "top_n": topK,
		"empty_response": "", "empty_response_type": 0, "kb_ids": datasets,
		"similarity_threshold": similarity, "keywords_similarity_weight": vectorWeight,
		"rerank_id": "", "summary": false,
		"outputs": map[string]any{"formalized_content": map[string]any{"type": "String", "value": ""}},
	}
}

func codeLanguage(language string) string {
	if strings.EqualFold(language, "javascript") {
		return "javascript"
	}
	return "python"
}

func codeType(contractType string) string {
	switch strings.ToLower(contractType) {
	case "string":
		return "String"
	case "number", "integer":
		return "Number"
	case "object":
		return "Object"
	case "array", "array<object>":
		return "Array<Object>"
	default:
		return "String"
	}
}

func switchOperator(operator string) string {
	switch operator {
	case "equals":
		return "is"
	case "not equals":
		return "is not"
	default:
		return operator
	}
}

func (c *converter) convertCode(data map[string]any) map[string]any {
	arguments := map[string]any{}
	for _, item := range sliceAt(data, "variables") {
		variable, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name := stringAt(variable, "variable"); name != "" {
			arguments[name] = "{" + c.reference(sliceAt(variable, "value_selector"), mapComponentOutput) + "}"
		}
	}
	outputType := "String"
	for _, value := range sliceAt(data, "outputs") {
		if contract, ok := value.(map[string]any); ok {
			outputType = codeType(stringAt(contract, "type"))
		}
	}
	return map[string]any{
		"lang": codeLanguage(stringAt(data, "code_language")), "script": stringAt(data, "code"), "arguments": arguments,
		"outputs": map[string]any{"result": map[string]any{"type": outputType, "value": nil}},
	}
}

func (c *converter) convertSwitch(id string, data map[string]any) (string, string, map[string]any, error) {
	conditions := make([]any, 0)
	for _, item := range sliceAt(data, "cases") {
		difyCase, ok := item.(map[string]any)
		if !ok {
			continue
		}
		caseID := firstNonEmpty(anyString(difyCase["case_id"]), anyString(difyCase["id"]))
		if caseID == "" {
			return "", "", nil, fmt.Errorf("case has no id")
		}
		targets := c.targetsForHandle(id, caseID)
		if len(targets) == 0 {
			return "", "", nil, fmt.Errorf("case %q has no downstream target", caseID)
		}
		items := make([]any, 0)
		for _, condition := range sliceAt(difyCase, "conditions") {
			conditionMap, ok := condition.(map[string]any)
			if !ok {
				continue
			}
			items = append(items, map[string]any{
				"cpn_id":   c.reference(sliceAt(conditionMap, "variable_selector"), mapComponentOutput),
				"operator": switchOperator(stringAt(conditionMap, "comparison_operator")),
				"value":    stringAt(conditionMap, "value"),
			})
		}
		conditions = append(conditions, map[string]any{
			"uuid": caseID, "items": items,
			"logical_operator": stringOr(stringAt(difyCase, "logical_operator"), "and"),
			"to":               targets,
		})
	}
	elseTargets := c.targetsForHandle(id, "false")
	if len(elseTargets) == 0 {
		elseTargets = c.targetsForHandle(id, "else")
	}
	if len(elseTargets) == 0 {
		return "", "", nil, fmt.Errorf("ELSE branch has no downstream target; RAGFlow Switch requires end_cpn_ids")
	}
	return labelSwitch, "switchNode", map[string]any{"conditions": conditions, "end_cpn_ids": elseTargets}, nil
}

func (c *converter) convertMessage(data map[string]any) (string, string, map[string]any, error) {
	var text string
	switch value := data["answer"].(type) {
	case string:
		text = value
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			parts = append(parts, fmt.Sprint(item))
		}
		text = strings.Join(parts, "\n")
	default:
		return "", "", nil, fmt.Errorf("answer content must be a string or list")
	}
	if strings.TrimSpace(text) == "" {
		return "", "", nil, fmt.Errorf("answer content is empty")
	}
	return labelMessage, "messageNode", map[string]any{
		"content": []any{c.renderTemplate(text, "")}, "stream": true, "auto_play": false, "output_format": nil,
		"outputs": map[string]any{
			"content":   map[string]any{"type": "String"},
			"downloads": map[string]any{"type": "Array<Object>"},
		},
	}, nil
}

func (c *converter) convertTemplate(data map[string]any) map[string]any {
	template := stringAt(data, "template")
	if strings.Contains(template, "{%") {
		c.warn("Dify template-transform contains Jinja control syntax; converted CodeExec only substitutes simple {{ variable }} tokens")
	}
	arguments := map[string]any{}
	names := make([]string, 0)
	for _, item := range sliceAt(data, "variables") {
		variable, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name := stringAt(variable, "variable"); name != "" {
			names = append(names, name)
			arguments[name] = "{" + c.reference(sliceAt(variable, "value_selector"), mapComponentOutput) + "}"
		}
	}
	script := "import base64\nimport re\n\nTEMPLATE_B64 = \"" + base64String(template) + "\"\n\n\ndef main(**kwargs) -> dict:\n    template = base64.b64decode(TEMPLATE_B64).decode(\"utf-8\")\n    for name, value in kwargs.items():\n        replacement = \"\" if value is None else str(value)\n        for token in (\"{{ \" + name + \" }}\", \"{{\" + name + \"}}\"):\n            template = template.replace(token, replacement)\n    return {\"result\": template}\n"
	if len(names) > 0 {
		c.warn(fmt.Sprintf("Dify template variables %s are passed as CodeExec arguments; only simple token substitution is supported", strings.Join(names, ", ")))
	}
	return map[string]any{
		"lang": "python", "script": script, "arguments": arguments,
		"outputs": map[string]any{"result": map[string]any{"type": "String", "value": nil}},
	}
}

func (c *converter) convertInvoke(data map[string]any) map[string]any {
	invokeURL := stringAt(data, "url")
	if isInternalURL(invokeURL) {
		c.warn("Invoke URL targets a private or loopback address; RAGFlow SSRF guard blocks it unless ALLOW_ANY_HOST=true is explicitly enabled for the trusted pilot environment")
	}
	variables := make([]any, 0)
	body := mapAt(data, "body")
	bodyType := stringOr(stringAt(body, "type"), "json")
	for _, item := range sliceAt(body, "data") {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key := stringAt(entry, "key")
		value := c.renderTemplate(stringAt(entry, "value"), "")
		if key == "" {
			var object map[string]any
			if err := json.Unmarshal([]byte(value), &object); err != nil {
				c.warn("HTTP body entry without a key is not flat JSON; inspect Invoke variables before runtime import")
				continue
			}
			for name, itemValue := range object {
				variables = append(variables, map[string]any{"key": name, "value": c.renderTemplate(fmt.Sprint(itemValue), "")})
			}
			continue
		}
		variables = append(variables, map[string]any{"key": key, "value": value})
	}
	headers := map[string]any{}
	for _, item := range sliceAt(data, "headers") {
		header, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if key := stringAt(header, "key"); key != "" {
			headers[key] = stringAt(header, "value")
		}
	}
	headerJSON, _ := json.Marshal(headers)
	timeout := intOr(data, "timeout", 60)
	if timeout <= 0 {
		timeout = 60
	}
	return map[string]any{
		"url": invokeURL, "method": strings.ToUpper(stringOr(stringAt(data, "method"), "GET")),
		"headers": string(headerJSON), "variables": variables, "timeout": timeout, "datatype": bodyType,
		"clean_html": false, "proxy": "",
		"outputs": map[string]any{"result": map[string]any{"type": "String", "value": ""}},
	}
}

func (c *converter) convertVariableAggregator(data map[string]any) map[string]any {
	variables := make([]any, 0)
	for _, item := range sliceAt(data, "variables") {
		selector, ok := item.([]any)
		if !ok {
			continue
		}
		variables = append(variables, c.reference(selector, mapComponentOutput))
	}
	return map[string]any{
		"groups":  []any{map[string]any{"group_name": "default", "variables": variables}},
		"outputs": map[string]any{"default": map[string]any{"type": "Any", "value": nil}},
	}
}

func (c *converter) convertVariableAssigner(data map[string]any) map[string]any {
	variables := make([]any, 0)
	for _, item := range sliceAt(data, "items") {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		parameter := entry["value"]
		if stringAt(entry, "input_type") == "variable" {
			if selector, ok := entry["value"].([]any); ok {
				parameter = c.reference(selector, mapComponentOutput)
			}
		}
		variables = append(variables, map[string]any{
			"variable": map[string]any{
				"cpn_id": stringAt(data, "assigned_variable_selector"),
				"field":  stringAt(entry, "variable"),
			},
			"parameter": parameter,
		})
	}
	return map[string]any{
		"items":   variables,
		"outputs": map[string]any{"result": map[string]any{"type": "Any", "value": nil}},
	}
}

func (c *converter) convertGlobals(items []map[string]any) (map[string]any, map[string]any, error) {
	globals := map[string]any{}
	variables := map[string]any{}
	for _, item := range items {
		name := firstNonEmpty(stringAt(item, "name"), stringAt(item, "variable"))
		if name == "" {
			return nil, nil, fmt.Errorf("conversation variable has no name")
		}
		variableType := stringAt(item, "value_type")
		if variableType == "" {
			variableType = "string"
		}
		if _, exists := globals[name]; exists {
			return nil, nil, fmt.Errorf("duplicate conversation variable %q", name)
		}
		globals[name] = item["value"]
		variables[name] = map[string]any{"type": variableType, "value": item["value"]}
	}
	return globals, variables, nil
}

func (c *converter) reference(selector []any, mapper func(c *converter, source, field string) string) string {
	parts := make([]string, 0, len(selector))
	for _, item := range selector {
		if value, ok := item.(string); ok {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return mapper(c, parts[0], strings.Join(parts[1:], "."))
}

func mapComponentOutput(c *converter, source, field string) string {
	switch {
	case source == "sys":
		return "sys." + field
	case source == "conversation":
		return "env." + field
	}
	source = c.ragID(source)
	if c.componentLabel(source) == labelRetrieval {
		return source + "@formalized_content"
	}
	switch {
	case strings.HasPrefix(field, "result"):
		if field == "result" {
			return source + "@result"
		}
		return source + "@result." + strings.TrimPrefix(field, "result.")
	}
	if c.componentLabel(source) == labelLLM && field == "text" {
		return source + "@content"
	}
	if c.componentLabel(source) == labelInvoke && (field == "body" || field == "text") {
		return source + "@result"
	}
	if c.componentLabel(source) == labelVariableAggregator {
		return source + "@default"
	}
	if c.componentLabel(source) == labelCodeExec {
		return source + "@result." + field
	}
	return source + "@" + field
}

func (c *converter) renderTemplate(value, contextRef string) string {
	value = strings.ReplaceAll(value, "{{#sys.query#}}", "{sys.query}")
	value = strings.ReplaceAll(value, "{{#sys.files#}}", "{sys.files}")
	value = strings.ReplaceAll(value, "{{#sys.user_id#}}", "{sys.user_id}")
	if contextRef != "" {
		value = strings.ReplaceAll(value, "{{#context#}}", "{"+contextRef+"}")
	}
	var result strings.Builder
	for {
		start := strings.Index(value, "{{#")
		if start < 0 {
			result.WriteString(value)
			break
		}
		end := strings.Index(value[start:], "#}}")
		if end < 0 {
			result.WriteString(value)
			break
		}
		end += start
		result.WriteString(value[:start])
		selector := splitSelector(value[start+3 : end])
		result.WriteString("{" + c.reference(selector, mapComponentOutput) + "}")
		value = value[end+3:]
	}
	return result.String()
}

func splitSelector(value string) []any {
	parts := strings.Split(value, ".")
	selector := make([]any, 0, len(parts))
	for _, part := range parts {
		selector = append(selector, part)
	}
	return selector
}

func (c *converter) componentLabel(id string) string {
	data := mapAt(c.byID[id], "data")
	switch stringAt(data, "type") {
	case "llm":
		return labelLLM
	case "knowledge-retrieval":
		return labelRetrieval
	case "code", "template-transform":
		return labelCodeExec
	case "answer":
		return labelMessage
	case "http-request", "tool":
		return labelInvoke
	case "variable-aggregator":
		return labelVariableAggregator
	case "assigner":
		return labelVariableAssigner
	case "agent":
		return labelAgent
	default:
		return stringAt(data, "type")
	}
}

func (c *converter) downstream(id string) []any {
	return c.connected(id, "source")
}

func (c *converter) upstream(id string) []any {
	return c.connected(id, "target")
}

func (c *converter) connected(id, field string) []any {
	result := make([]any, 0)
	other := "target"
	if field == "target" {
		other = "source"
	}
	for _, edge := range c.edges {
		if c.ragID(stringAt(edge, field)) == id {
			result = append(result, c.ragID(stringAt(edge, other)))
		}
	}
	return result
}

func (c *converter) targetsForHandle(id, handle string) []string {
	result := make([]string, 0)
	for _, edge := range c.edges {
		if c.ragID(stringAt(edge, "source")) == id && edgeHandle(edge) == handle {
			result = append(result, c.ragID(stringAt(edge, "target")))
		}
	}
	sort.Strings(result)
	return result
}

func (c *converter) validateEdges() error {
	for index, edge := range c.edges {
		source := stringAt(edge, "source")
		target := stringAt(edge, "target")
		if source == "" || target == "" {
			return fmt.Errorf("edge %d has no source or target", index)
		}
		if _, ok := c.byID[source]; !ok {
			return fmt.Errorf("edge %d references missing source %q", index, source)
		}
		if _, ok := c.byID[target]; !ok {
			return fmt.Errorf("edge %d references missing target %q", index, target)
		}
	}
	return c.validateAcyclic()
}

func (c *converter) validateAcyclic() error {
	inDegree := make(map[string]int, len(c.nodes))
	adjacency := make(map[string][]string, len(c.nodes))
	for _, node := range c.nodes {
		id := stringAt(node, "id")
		inDegree[id] = 0
		adjacency[id] = []string{}
	}
	for _, edge := range c.edges {
		source := stringAt(edge, "source")
		target := stringAt(edge, "target")
		adjacency[source] = append(adjacency[source], target)
		inDegree[target]++
	}
	queue := make([]string, 0)
	for _, node := range c.nodes {
		id := stringAt(node, "id")
		if inDegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, target := range adjacency[id] {
			inDegree[target]--
			if inDegree[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	if visited != len(c.nodes) {
		return fmt.Errorf("Dify workflow graph contains a cycle; RAGFlow requires a DAG")
	}
	return nil
}

func (c *converter) unmappedDatasets() []string {
	seen := map[string]bool{}
	for _, node := range c.nodes {
		data := mapAt(node, "data")
		if stringAt(data, "type") != "knowledge-retrieval" {
			continue
		}
		for _, item := range sliceAt(data, "dataset_ids") {
			if id, ok := item.(string); ok && id != "" && c.options.DatasetMapping[id] == "" && !seen[id] {
				seen[id] = true
			}
		}
	}
	return sortedKeySet(seen)
}

func (c *converter) warn(message string) {
	c.report.Warnings = append(c.report.Warnings, message)
}

func validateGraph(dsl map[string]any) error {
	graph := mapAt(dsl, "graph")
	nodes := sliceAt(graph, "nodes")
	edges := sliceAt(graph, "edges")
	nodeIDs := map[string]bool{}
	beginCount := 0
	for _, item := range nodes {
		node, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("RAGFlow graph node is not an object")
		}
		id := stringAt(node, "id")
		if nodeIDs[id] {
			return fmt.Errorf("duplicate RAGFlow node id %q", id)
		}
		nodeIDs[id] = true
		if stringAt(node, "data", "label") == labelBegin {
			beginCount++
		}
	}
	if beginCount != 1 {
		return fmt.Errorf("RAGFlow agent DSL must contain exactly one Begin; got %d", beginCount)
	}
	for _, item := range edges {
		edge, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("RAGFlow graph edge is not an object")
		}
		source := stringAt(edge, "source")
		target := stringAt(edge, "target")
		if source == "" || target == "" {
			return fmt.Errorf("RAGFlow graph edge has no source or target")
		}
		if !nodeIDs[source] || !nodeIDs[target] {
			return fmt.Errorf("RAGFlow graph edge references a missing node")
		}
	}
	return nil
}
