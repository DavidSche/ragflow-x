import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { i18nProvider } from "@/i18nProvider";
import enMessages from "@/i18n/en";
import zhMessages from "@/i18n/zh";
import {
  REQUIREMENT_REASONS,
  SCENARIO_TEMPLATE_CAPABILITIES,
  capabilityName,
  capabilityReason,
  capabilityRole,
  capabilityVerdict,
  humanizeEnum,
  translateOr,
  type Translate,
} from "./capabilityLabels";

/** vitest runs from `web/`; the vocabulary this mirror has to match lives in Go. */
function readGoFile(relative: string): string {
  return readFileSync(resolve(process.cwd(), "..", "internal", "service", relative), "utf8");
}

const goSource = readGoFile("scenario_template_knowledge.go");

function lookup(messages: unknown, dotted: string): unknown {
  return dotted.split(".").reduce<unknown>((node, part) => {
    if (node && typeof node === "object" && part in (node as Record<string, unknown>)) {
      return (node as Record<string, unknown>)[part];
    }
    return undefined;
  }, messages);
}

describe("capability vocabulary mirror", () => {
  it("matches the Go closed set, so a new capability cannot ship without a label", () => {
    const fromGo = [...goSource.matchAll(/^\s*Capability\w+\s+= "([a-z_]+)"/gm)].map((m) => m[1]);
    expect(fromGo.length).toBeGreaterThan(0);
    expect([...SCENARIO_TEMPLATE_CAPABILITIES].sort()).toEqual([...fromGo].sort());
  });

  it("keeps the reason vocabulary in step with knowledge_capability.go", () => {
    const capabilitySource = readGoFile("knowledge_capability.go");
    const fromGo = [...capabilitySource.matchAll(/item\.Reason = "([a-z_]+)"/g)].map((m) => m[1]);
    expect([...new Set(fromGo)].sort()).toEqual([...REQUIREMENT_REASONS].sort());
  });
});

describe("capability labels are translated in both locales", () => {
  const dictionaries: Array<[string, unknown]> = [
    ["zh", zhMessages],
    ["en", enMessages],
  ];

  for (const [locale, messages] of dictionaries) {
    it(`${locale}: every capability, reason, role, verdict and state has a label`, () => {
      const missing: string[] = [];
      for (const capability of SCENARIO_TEMPLATE_CAPABILITIES) {
        if (typeof lookup(messages, `scenarioTemplates.capability.name.${capability}`) !== "string") {
          missing.push(`name.${capability}`);
        }
      }
      for (const reason of REQUIREMENT_REASONS) {
        if (typeof lookup(messages, `scenarioTemplates.capability.reason.${reason}`) !== "string") {
          missing.push(`reason.${reason}`);
        }
      }
      for (const role of ["primary", "reference"]) {
        if (typeof lookup(messages, `scenarioTemplates.capability.role.${role}`) !== "string") {
          missing.push(`role.${role}`);
        }
      }
      for (const verdict of ["satisfied", "probe_limited", "repairable", "blocked"]) {
        if (typeof lookup(messages, `scenarioTemplates.capability.verdict.${verdict}`) !== "string") {
          missing.push(`verdict.${verdict}`);
        }
        if (typeof lookup(messages, `scenarioTemplates.capability.state.${verdict}`) !== "string") {
          missing.push(`state.${verdict}`);
        }
      }
      expect(missing).toEqual([]);
    });
  }
});

describe("render path used by the wizard (real polyglot provider)", () => {
  const t: Translate = (key, options) => i18nProvider.translate(key, options);

  it("names the two capabilities that showed up as raw keys in the wizard", () => {
    expect(capabilityName(t, "citation_locator")).toBe("引用定位");
    expect(capabilityName(t, "metadata_filter")).toBe("元数据过滤");
    expect(capabilityName(t, "metadata_filter_pushdown")).toBe("元数据过滤（下推）");
    expect(capabilityName(t, "vector")).toBe("向量");
    expect(capabilityName(t, "raptor")).toBe("RAPTOR 摘要");
    expect(capabilityName(t, "wiki")).toBe("知识库条目");
  });

  it("never renders an i18n key path, even for a capability nobody has labelled yet", () => {
    const label = capabilityName(t, "hyper_custom_probe");
    expect(label).toBe("hyper custom probe");
    expect(label).not.toContain("scenarioTemplates");
  });

  it("labels the requirement reasons instead of leaking the enum", () => {
    expect(capabilityReason(t, "below_min_level")).toBe("未达到最低等级");
    expect(capabilityReason(t, "metadata_not_ready")).toBe("元数据未就绪");
    expect(capabilityReason(t, "capability_not_supported")).toBe("引擎不支持该能力");
  });

  it("labels roles and verdicts", () => {
    expect(capabilityRole(t, "primary")).toBe("主库");
    expect(capabilityVerdict(t, "repairable")).toBe("可修复后通过");
  });

  it("keeps the polyglot trap explicit: `defaultValue` is i18next, not polyglot", () => {
    // This is why the wizard printed the whole key: polyglot reads its default
    // from `_`. Anyone re-introducing `defaultValue` gets the key back.
    const missing = "scenarioTemplates.capability.name.not_a_capability";
    expect(i18nProvider.translate(missing)).toBe(missing);
    expect(i18nProvider.translate(missing, { defaultValue: "引用定位" })).toBe(missing);
    expect(i18nProvider.translate(missing, { _: "引用定位" })).toBe("引用定位");
  });
});

describe("translateOr", () => {
  it("prefers the dictionary", () => {
    const t: Translate = (key) => (key === "a.b" ? "translated" : key);
    expect(translateOr(t, "a.b", "fallback")).toBe("translated");
  });

  it("falls back to the humanised identifier when polyglot hands the key back", () => {
    const t: Translate = (key) => key;
    expect(translateOr(t, "a.b.missing", "missing value")).toBe("missing value");
  });

  it("falls back on an empty string", () => {
    const t: Translate = () => "";
    expect(translateOr(t, "a.b", "fallback")).toBe("fallback");
  });
});

describe("humanizeEnum", () => {
  it("turns an identifier into readable words", () => {
    expect(humanizeEnum("metadata_filter_pushdown")).toBe("metadata filter pushdown");
    expect(humanizeEnum("")).toBe("");
  });
});
