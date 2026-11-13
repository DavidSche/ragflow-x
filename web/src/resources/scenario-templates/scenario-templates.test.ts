import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
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

  it("passes the eval gate and baseline provenance through to the view", () => {
    const view = localizeScenarioTemplate(
      catalogItem({
        baseline_key: "contract-compliance",
        baseline_version: 3,
        latest_baseline_version: 4,
        baseline_update_available: true,
        lineage_state: "forked",
        eval_gate: { required: true, passed: false, reason: "no evaluation run" },
      }),
      "zh",
    );
    expect(view.baselineVersion).toBe(3);
    expect(view.latestBaselineVersion).toBe(4);
    expect(view.baselineUpdateAvailable).toBe(true);
    expect(view.evalGate.required).toBe(true);
    expect(view.evalGate.passed).toBe(false);
  });

  it("defaults the eval gate to not-required when the server omits it", () => {
    const view = localizeScenarioTemplate(catalogItem(), "zh");
    expect(view.evalGate.required).toBe(false);
    expect(view.baselineUpdateAvailable).toBe(false);
  });

  it("gates the wizard on renderable app types, not the declared ones", () => {
    // A v2 template declares `agent` for documentation but ships no canvas, so
    // offering the button would guarantee a 40085 at submit time.
    const view = localizeScenarioTemplate(
      catalogItem({ app_types: ["chat", "agent"], renderable_app_types: ["chat"] }),
      "zh",
    );
    expect(view.appTypes).toEqual(["chat", "agent"]);
    expect(view.renderableAppTypes).toEqual(["chat"]);
  });

  it("falls back to the declared app types when the server omits the field", () => {
    const view = localizeScenarioTemplate(catalogItem({ app_types: ["chat", "search"] }), "zh");
    expect(view.renderableAppTypes).toEqual(["chat", "search"]);
  });
});

describe("scenario template single source of truth", () => {
  const read = (file: string) => readFileSync(path.resolve(import.meta.dirname, file), "utf8");
  const wizardSource = [
    read("ScenarioTemplateWizard.tsx"),
    read("wizard/useScenarioWizard.ts"),
    read("wizard/StepApplication.tsx"),
    read("wizard/StepKnowledge.tsx"),
    read("wizard/StepVerify.tsx"),
    read("wizard/StepScenario.tsx"),
  ].join("\n");
  const catalogSource = read("scenario-templates.ts");

  it("reads the catalog from the API instead of a static table", () => {
    expect(catalogSource).toContain('"/scenario-template-catalog"');
    expect(wizardSource).toContain("fetchScenarioTemplateCatalog");
    expect(wizardSource).not.toMatch(/const SCENARIO_TEMPLATES\s*:/);
  });

  it("creates all three forms through the unified instantiation endpoint", () => {
    expect(catalogSource).toContain("/scenario-template-assets/");
    expect(catalogSource).toContain("/instantiate");
    expect(wizardSource).toContain("instantiateScenarioTemplate");
    // No direct form-specific creation posts may come back (doc/276 §12 "删拼装").
    expect(wizardSource).not.toContain('"/search-apps"');
    expect(wizardSource).not.toContain('"/agents"');
  });

  it("sends the idempotency key in the header, never the body", () => {
    expect(catalogSource).toContain('"Idempotency-Key"');
    expect(catalogSource).not.toMatch(/idempotency_key[^,]*instantiate/);
  });

  it("keeps every wizard file under the 400-line ceiling", () => {
    [
      "ScenarioTemplateWizard.tsx",
      "wizard/useScenarioWizard.ts",
      "wizard/StepScenario.tsx",
      "wizard/StepKnowledge.tsx",
      "wizard/StepApplication.tsx",
      "wizard/StepVerify.tsx",
      "wizard/CapabilityBadge.tsx",
    ].forEach((file) => {
      const lines = read(file).split("\n").length;
      expect(lines, `${file} has ${lines} lines`).toBeLessThanOrEqual(400);
    });
  });

  it("renders capability badges with text and aria-label, not colour alone", () => {
    const badge = read("wizard/CapabilityBadge.tsx");
    expect(badge).toContain("aria-label");
    expect(badge).toContain("sr-only");
    expect(badge).toContain('aria-hidden="true"');
  });

  it("disables the forms the server cannot render and says why", () => {
    const stepApplication = read("wizard/StepApplication.tsx");
    expect(stepApplication).toContain("template.renderableAppTypes.includes(option.value)");
    expect(stepApplication).toContain("scenarioTemplates.wizard.appTypeUnavailable");
    const hook = read("wizard/useScenarioWizard.ts");
    expect(hook).toContain("item.renderableAppTypes[0]");
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
