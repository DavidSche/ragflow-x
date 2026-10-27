package difydsl

import (
	"sort"
	"strings"
)

func (c *converter) markMappedTool(mappingKey string) {
	if mappingKey != "" {
		c.mappedToolKeys[mappingKey] = true
	}
}

func (c *converter) markUnmappedTool(toolName, providerID string) {
	if toolName == "" {
		return
	}
	if providerID != "" {
		c.unmappedToolIDs[providerID+"/"+toolName] = true
	}
	c.unmappedToolIDs[toolName] = true
}

func (c *converter) mappedTools() []string {
	return sortedKeySet(c.mappedToolKeys)
}

func (c *converter) unmappedTools() []string {
	result := make([]string, 0)
	for _, item := range c.nodes {
		data := mapAt(item, "data")
		if stringAt(data, "type") != "tool" {
			continue
		}
		toolName := stringAt(data, "tool_name")
		providerID := stringAt(data, "provider_id")
		if !c.toolIsMapped(toolName, providerID) {
			if providerID != "" {
				result = append(result, providerID+"/"+toolName)
			}
			result = append(result, toolName)
		}
	}
	for _, agentPackage := range mapSliceAt(c.raw, "agent_packages") {
		for _, tool := range sliceAt(agentPackage, "soul", "tools", "dify_tools") {
			toolMap, ok := tool.(map[string]any)
			if !ok {
				continue
			}
			toolName := stringAt(toolMap, "tool_name")
			providerID := stringAt(toolMap, "provider_id")
			if !c.toolIsMapped(toolName, providerID) {
				if providerID != "" {
					result = append(result, providerID+"/"+toolName)
				}
				result = append(result, toolName)
			}
		}
	}
	sort.Strings(result)
	unique := make([]string, 0, len(result))
	for index, value := range result {
		if index == 0 || value != result[index-1] {
			unique = append(unique, value)
		}
	}
	return unique
}

func (c *converter) toolIsMapped(toolName, providerID string) bool {
	if strings.EqualFold(toolName, "tavily_search") {
		return true
	}
	if providerID != "" && c.options.ToolMapping[providerID+"/"+toolName].Type == "http" {
		return true
	}
	return c.options.ToolMapping[toolName].Type == "http"
}

func safeToolID(value string) string {
	return strings.Map(func(char rune) rune {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_', char == '.':
			return char
		default:
			return '-'
		}
	}, value)
}

func renderDifyToolReferences(value string) string {
	return difyToolReferencePattern.ReplaceAllString(value, "[$1]")
}
