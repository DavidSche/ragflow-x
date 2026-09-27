import { createEmptyAgentDsl } from "../agents/dslUtils";
import type { ParameterProfile, PromptPreset } from "../workbench/prompt-presets";

export function createScenarioAgentDsl(options: {
  description: string;
  preset: PromptPreset;
  profile: ParameterProfile;
  datasetIds: string[];
  modelId?: string;
}): Record<string, any> {
  const base = createEmptyAgentDsl();
  const retrievalId = "Retrieval:Knowledge";
  const agentId = "Agent:Scenario";
  const messageId = "Message:Output";
  const retrievalParams = {
    cross_languages: [],
    description: "授权知识库检索",
    empty_response: options.preset.emptyResponse,
    retrieval_from: "dataset",
    keywords_similarity_weight: Number((1 - options.profile.vectorWeight).toFixed(2)),
    outputs: { formalized_content: { type: "string", value: "" } },
    rerank_id: "",
    similarity_threshold: options.profile.similarityThreshold,
    top_k: 1024,
    top_n: options.profile.topN,
    use_kg: false,
    kb_ids: options.datasetIds,
    dataset_ids: options.datasetIds,
  };
  const agentParams = {
    delay_after_error: 1,
    description: options.description,
    exception_comment: "",
    exception_default_value: "",
    exception_goto: [],
    exception_method: null,
    frequencyPenaltyEnabled: false,
    frequency_penalty: 0,
    llm_id: options.modelId ?? "",
    maxTokensEnabled: true,
    max_retries: 2,
    max_rounds: 1,
    max_tokens: options.profile.maxTokens,
    mcp: [],
    message_history_window_size: 12,
    outputs: { content: { type: "string", value: "" } },
    presencePenaltyEnabled: false,
    presence_penalty: 0,
    prompts: [
      {
        role: "user",
        content: `请基于检索结果完成任务：{sys.query}\n参考资料：{${retrievalId}@formalized_content}`,
      },
    ],
    sys_prompt: options.preset.system,
    temperature: options.profile.temperature,
    temperatureEnabled: true,
    tools: [
      {
        id: retrievalId,
        component_name: "Retrieval",
        name: "Retrieval",
        params: retrievalParams,
      },
    ],
    topPEnabled: true,
    top_p: options.profile.topP,
    user_prompt: "",
    visual_files_var: "",
  };

  return {
    ...base,
    graph: {
      nodes: [
        {
          id: "begin",
          type: "beginNode",
          position: { x: 60, y: 220 },
          data: {
            label: "Begin",
            name: "begin",
            form: { mode: "conversational", enablePrologue: true, prologue: options.preset.prologue },
          },
          sourcePosition: "right",
          targetPosition: "left",
        },
        {
          id: agentId,
          type: "agentNode",
          position: { x: 360, y: 220 },
          data: { label: "Agent", name: "scenario_agent", form: agentParams },
          sourcePosition: "right",
          targetPosition: "left",
        },
        {
          id: messageId,
          type: "generalNode",
          position: { x: 660, y: 220 },
          data: {
            label: "Message",
            name: "output",
            form: { content: [`{${agentId}@content}`] },
          },
          sourcePosition: "right",
          targetPosition: "left",
        },
      ],
      edges: [
        { id: "edge-begin-agent", source: "begin", target: agentId, sourceHandle: "start", targetHandle: "end" },
        { id: "edge-agent-message", source: agentId, target: messageId, sourceHandle: "start", targetHandle: "end" },
      ],
    },
    components: {
      begin: {
        obj: { component_name: "Begin", params: { mode: "conversational", enablePrologue: true, prologue: options.preset.prologue } },
        downstream: [agentId],
        upstream: [],
      },
      [agentId]: {
        obj: { component_name: "Agent", params: agentParams },
        downstream: [messageId],
        upstream: ["begin"],
      },
      [messageId]: {
        obj: { component_name: "Message", params: { content: [`{${agentId}@content}`] } },
        downstream: [],
        upstream: [agentId],
      },
    },
  };
}
