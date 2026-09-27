import { describe, expect, it } from "vitest";
import { PARAMETER_PROFILES, PROMPT_PRESETS } from "../workbench/prompt-presets";
import { createScenarioAgentDsl } from "./scenario-agent-dsl";
import {
  SCENARIO_TEMPLATES,
  scenarioParameterProfile,
  scenarioPromptPreset,
} from "./scenario-templates";

describe("scenario templates", () => {
  it("ships complete and executable template assets", () => {
    expect(SCENARIO_TEMPLATES).toHaveLength(9);
    SCENARIO_TEMPLATES.forEach((template) => {
      expect(template.label).toBeTruthy();
      expect(template.description).toBeTruthy();
      expect(template.appTypes.length).toBeGreaterThan(0);
      expect(PROMPT_PRESETS.some((preset) => preset.id === template.promptPresetId)).toBe(true);
      expect(PARAMETER_PROFILES.some((profile) => profile.id === template.profileId)).toBe(true);
      expect(template.suggestedDatasetNames.length).toBeGreaterThan(0);
      expect(template.sampleQuestions.length).toBeGreaterThan(0);
      expect(template.evaluationQuestions.length).toBeGreaterThan(0);
      expect(template.permissionAdvice).toBeTruthy();
      expect(template.publishChecklist.length).toBeGreaterThan(0);
      expect(template.expectedOutcome).toBeTruthy();
    });
  });

  it("resolves template prompt and parameter assets", () => {
    const contract = SCENARIO_TEMPLATES.find((template) => template.id === "contract-compliance");
    expect(contract).toBeDefined();
    expect(scenarioPromptPreset(contract!).id).toBe("contract-compliance");
    expect(scenarioParameterProfile(contract!).id).toBe("citation-strict");
  });

  it("creates a Begin → Agent → Message agent workflow", () => {
    const template = SCENARIO_TEMPLATES.find((item) => item.id === "operations")!;
    const dsl = createScenarioAgentDsl({
      description: template.description,
      preset: scenarioPromptPreset(template),
      profile: scenarioParameterProfile(template),
      datasetIds: ["ds-1", "ds-2"],
      modelId: "model-1",
    });
    expect(dsl.graph.nodes.map((node: { id: string }) => node.id)).toEqual([
      "begin",
      "Agent:Scenario",
      "Message:Output",
    ]);
    expect(dsl.components.begin.downstream).toEqual(["Agent:Scenario"]);
    expect(dsl.components["Agent:Scenario"].upstream).toEqual(["begin"]);
    expect(dsl.components["Message:Output"].upstream).toEqual(["Agent:Scenario"]);
    const agent = dsl.components["Agent:Scenario"].obj.params;
    expect(agent.llm_id).toBe("model-1");
    expect(agent.temperature).toBe(0.1);
    expect(agent.max_tokens).toBe(2048);
    expect(agent.tools[0].params.kb_ids).toEqual(["ds-1", "ds-2"]);
    expect(agent.prompts[0].content).toContain("{Retrieval:Knowledge@formalized_content}");
  });
});
