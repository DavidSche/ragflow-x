/**
 * Shared types and utility functions for model provider management.
 */

export const MODEL_TYPES = [
  "chat",
  "embedding",
  "asr",
  "vision",
  "rerank",
  "tts",
  "ocr",
] as const;

export interface InstanceRow {
  id: string;
  instance_name: string;
  base_url?: string;
  region?: string;
  status?: string;
  model_type?: number;
}

export interface ModelRow {
  id: string;
  model_name: string;
  model_type?: number;
  max_tokens?: number;
  status?: string;
  verify?: string;
  is_tools?: boolean;
  thinking?: boolean;
  extra_json?: string;
}

export interface ProviderForm {
  instance_name: string;
  api_key: string;
  base_url: string;
  region: string;
  model_name: string;
  model_type: string[];
  max_tokens: string;
  is_tools: boolean;
  thinking: boolean;
}

export const EMPTY_FORM: ProviderForm = {
  instance_name: "",
  api_key: "",
  base_url: "",
  region: "default",
  model_name: "",
  model_type: [],
  max_tokens: "8192",
  is_tools: true,
  thinking: false,
};

export function parseExtra(s?: string): {
  is_tools?: boolean;
  thinking?: boolean;
  max_tokens?: number;
} {
  if (!s) return {};
  try {
    const o = JSON.parse(s);
    return {
      is_tools:
        typeof o.is_tools === "boolean" ? o.is_tools : undefined,
      thinking:
        typeof o.thinking === "boolean" ? o.thinking : undefined,
      max_tokens:
        typeof o.max_tokens === "number" ? o.max_tokens : undefined,
    };
  } catch {
    return {};
  }
}

export const modelTools = (m: ModelRow) =>
  parseExtra(m.extra_json).is_tools ?? m.is_tools ?? false;

export const modelThinking = (m: ModelRow) =>
  parseExtra(m.extra_json).thinking ?? m.thinking ?? false;

export function typeLabels(mask?: number): string[] {
  if (!mask) return ["chat"];
  const labels: string[] = [];
  const map: [number, string][] = [
    [1, "chat"],
    [2, "embedding"],
    [4, "asr"],
    [8, "vision"],
    [16, "rerank"],
    [32, "tts"],
    [64, "ocr"],
  ];
  for (const [bit, name] of map) {
    if (mask & bit) labels.push(name);
  }
  return labels.length ? labels : ["chat"];
}
