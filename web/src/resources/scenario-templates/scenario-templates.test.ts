import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { createScenarioAgentDsl } from "./scenario-agent-dsl";
import {
  SCENARIO_TEMPLATE_KEYS,
  localizeScenarioTemplate,
  type ScenarioTemplateCatalogItem,
} from "./scenario-templates";

function catalogItem(overrides: Partial<ScenarioTemplateCatalogItem> = {}): ScenarioTemplateCatalogItem {
  return {
    id: "asset-1",
    key: "contract-compliance",
    name: "合同法务审阅",
    description: "合同条款比对与风险提示。",
    app_types: ["chat", "agent"],
    latest_version: 3,
    source: "builtin",
    status: "published",
    payload_schema: "ragflow-x.scenario-template.v2",
    prompt_preset_id: "contract-compliance",
    parameter_profile_id: "citation-strict",
    prompt: {
      system: "你是合同与合规辅助助手。",
      prologue: "请粘贴合同问题或条款主题。",
      empty_response: "未找到对应条款，不能进行合规判断。",
    },
    parameters: {
      top_n: 8,
      similarity_threshold: 0.32,
      vector_similarity_weight: 0.35,
      temperature: 0.05,
      top_p: 0.8,
      max_tokens: 2048,
    },
    dataset_suggestions: ["合同模板"],
    sample_questions: ["付款条件是什么？"],
    evaluation_questions: ["违约责任如何约定？"],
    permission_advice: "法务与商务可访问。",
    publish_checklist: ["绑定最新合同库"],
    expected_outcome: "合同审阅更快，口径一致。",
    translations: {
      en: {
        name: "Contract compliance review",
        description: "Clause comparison and risk flags.",
        datasetSuggestions: ["Contract templates"],
        sampleQuestions: ["What are the payment terms?"],
        evaluationQuestions: ["How is liability defined?"],
        permissionAdvice: "Legal and sales can access.",
        publishChecklist: ["Bind the latest contract library"],
        expectedOutcome: "Faster review with consistent wording.",
      },
    },
    ...overrides,
  };
}

describe("scenario template catalog projection", () => {
  it("projects a catalog row into the wizard view model", () => {
    const view = localizeScenarioTemplate(catalogItem(), "zh");
    expect(view.id).toBe("asset-1");
    expect(view.key).toBe("contract-compliance");
    expect(view.label).toBe("合同法务审阅");
    expect(view.latestVersion).toBe(3);
    expect(view.appTypes).toEqual(["chat", "agent"]);
    expect(view.promptPresetId).toBe("contract-compliance");
    expect(view.profileId).toBe("citation-strict");
    expect(view.translationMissing).toBe(false);
  });

  it("uses the bundled translation for the requested locale", () => {
    const view = localizeScenarioTemplate(catalogItem(), "en");
    expect(view.label).toBe("Contract compliance review");
    expect(view.sampleQuestions).toEqual(["What are the payment terms?"]);
    expect(view.publishChecklist).toEqual(["Bind the latest contract library"]);
    expect(view.translationMissing).toBe(false);
  });

  it("falls back to canonical copy and flags the missing translation", () => {
    const view = localizeScenarioTemplate(catalogItem({ translations: {} }), "en");
    expect(view.label).toBe("合同法务审阅");
    expect(view.sampleQuestions).toEqual(["付款条件是什么？"]);
    expect(view.translationMissing).toBe(true);
  });

  it("keeps the builtin key list aligned with the backend seed", () => {
    expect(SCENARIO_TEMPLATE_KEYS).toHaveLength(9);
    expect(new Set(SCENARIO_TEMPLATE_KEYS).size).toBe(9);
  });

  it("passes the server-resolved prompt and parameters through untouched", () => {
    const view = localizeScenarioTemplate(catalogItem(), "zh");
    expect(view.promptPresetId).toBe("contract-compliance");
    expect(view.profileId).toBe("citation-strict");
    expect(view.prompt.system).toBe("你是合同与合规辅助助手。");
    expect(view.parameters.top_n).toBe(8);
    expect(view.parameters.similarity_threshold).toBe(0.32);
  });

  it("degrades to empty defaults instead of inventing a preset", () => {
    const view = localizeScenarioTemplate(catalogItem({ prompt: undefined, parameters: undefined }), "zh");
    expect(view.prompt.system).toBe("");
    expect(view.parameters).toEqual({
      top_n: 0,
      similarity_threshold: 0,
      vector_similarity_weight: 0,
      temperature: 0,
      top_p: 0,
      max_tokens: 0,
    });
  });

  it("creates a Begin → Agent → Message agent workflow", () => {
    const view = localizeScenarioTemplate(catalogItem({ key: "operations" }), "zh");
    const dsl = createScenarioAgentDsl({
      description: view.description,
      prompt: view.prompt,
      parameters: view.parameters,
      datasetIds: ["ds-1", "ds-2"],
      modelId: "model-1",
    });
    expect(dsl.graph.nodes.map((node: { id: string }) => node.id)).toEqual([
      "begin",
      "Agent:Scenario",
      "Message:Output",
    ]);
    const agent = dsl.components["Agent:Scenario"].obj.params;
    expect(agent.llm_id).toBe("model-1");
    expect(agent.sys_prompt).toBe(view.prompt.system);
    expect(agent.temperature).toBe(view.parameters.temperature);
    expect(agent.tools[0].params.kb_ids).toEqual(["ds-1", "ds-2"]);
    expect(agent.tools[0].params.top_n).toBe(view.parameters.top_n);
  });
});

describe("scenario template single source of truth", () => {
  const read = (file: string) => readFileSync(path.resolve(import.meta.dirname, file), "utf8");
  const wizardSource = read("ScenarioTemplateWizard.tsx");
  const catalogSource = read("scenario-templates.ts");

  it("reads the catalog from the API instead of a static table", () => {
    expect(catalogSource).toContain('"/scenario-template-catalog"');
    expect(wizardSource).toContain("fetchScenarioTemplateCatalog");
    expect(wizardSource).not.toMatch(/const SCENARIO_TEMPLATES\s*:/);
  });

  it("creates assistants through the template instantiation endpoint", () => {
    expect(catalogSource).toContain("/scenario-template-assets/");
    expect(wizardSource).toContain("instantiateScenarioChat");
  });

  it("never re-sends prompt or parameters as an override", () => {
    // The wizard used to build prompt_config from the local preset table and
    // post it back, which made the server ignore the template payload.
    expect(wizardSource).not.toContain("prompt_config");
    expect(wizardSource).not.toContain("PROMPT_PRESETS");
    expect(wizardSource).not.toContain("PARAMETER_PROFILES");
    expect(wizardSource).not.toContain("PromptPresetPicker");
    expect(catalogSource).not.toContain("PROMPT_PRESETS");
    expect(catalogSource).not.toContain("PARAMETER_PROFILES");
    expect(wizardSource).toContain("template.prompt.system");
    expect(wizardSource).toContain("template.parameters");
  });

  it("keeps no builtin template copy in the frontend catalog module", () => {
    ["企业知识问答", "合同法务审阅", "客服 FAQ", "财务口径助手"].forEach((label) => {
      expect(wizardSource).not.toContain(label);
      expect(catalogSource).not.toContain(label);
    });
  });
});
