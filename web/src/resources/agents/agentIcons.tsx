// Operator icons for the agent workflow canvas. Downloaded from Hugeicons
// (https://hugeicons.com/icons) and bundled locally; stroke color is
// normalized to `currentColor` so each operator can be tinted per-theme.

import beginIcon from "@/assets/agents/icons/begin.svg?raw";
import generateIcon from "@/assets/agents/icons/generate.svg?raw";
import knowledgeIcon from "@/assets/agents/icons/knowledge.svg?raw";
import categorizeIcon from "@/assets/agents/icons/categorize.svg?raw";
import chunkerIcon from "@/assets/agents/icons/chunker.svg?raw";
import toolIcon from "@/assets/agents/icons/tool.svg?raw";
import codeIcon from "@/assets/agents/icons/code.svg?raw";
import answerIcon from "@/assets/agents/icons/answer.svg?raw";
import agentIcon from "@/assets/agents/icons/agent.svg?raw";

const SVG_CONTENT_FILL = /#141B34/gi;

function colorize(raw: string): string {
  return raw.replace(SVG_CONTENT_FILL, "currentColor");
}

const ICON_BY_KEY: Record<string, string> = {
  Begin: colorize(beginIcon),
  Generate: colorize(generateIcon),
  LLM: colorize(generateIcon),
  Knowledge: colorize(knowledgeIcon),
  Retrieval: colorize(knowledgeIcon),
  Categorize: colorize(categorizeIcon),
  Chunker: colorize(chunkerIcon),
  Tool: colorize(toolIcon),
  Code: colorize(codeIcon),
  Answer: colorize(answerIcon),
  Agent: colorize(agentIcon),
};

export function resolveAgentIcon(label?: string): string | undefined {
  if (!label) return ICON_BY_KEY.Agent;
  for (const k of Object.keys(ICON_BY_KEY)) {
    if (label === k || label.includes(k)) return ICON_BY_KEY[k];
  }
  return ICON_BY_KEY.Agent;
}

export function AgentIcon({
  label,
  color,
  className = "size-4",
}: {
  label?: string;
  color?: string;
  className?: string;
}) {
  const svg = resolveAgentIcon(label);
  if (!svg) return null;
  return (
    <span
      aria-hidden="true"
      className={`inline-flex shrink-0 items-center justify-center ${className} [&_svg]:h-full [&_svg]:w-full [&_svg]:stroke-current`}
      style={color ? { color } : undefined}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}