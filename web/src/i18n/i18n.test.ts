import { describe, expect, it } from "vitest";
import en from "./en";
import zh from "./zh";

function keys(value: unknown, prefix = ""): string[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) return [prefix];
  return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) =>
    keys(child, prefix ? `${prefix}.${key}` : key),
  );
}

/** Namespaces that the WS7 onboarding surface renders (doc/229 T7.7′). */
const WS7_NAMESPACES = ["scenarioTemplates", "conversationCenter"] as const;
const CJK = /[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]/;

function leaves(value: unknown, path = ""): { path: string; value: unknown }[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return [{ path, value }];
  }
  return Object.entries(value as Record<string, unknown>).flatMap(([key, child]) =>
    leaves(child, path ? `${path}.${key}` : key),
  );
}

describe("i18n connectors namespace", () => {
  it("keeps the connectors keys in sync between zh and en", () => {
    expect(keys(zh.connectors).sort()).toEqual(keys(en.connectors).sort());
  });

  it("labels the connectors resource in both locales", () => {
    expect(zh.resources.connectors.name).toBeTruthy();
    expect(en.resources.connectors.name).toBeTruthy();
  });
});

describe("WS7 onboarding i18n", () => {
  it.each(WS7_NAMESPACES)("keeps %s keys in sync between zh and en", (namespace) => {
    const zhKeys = keys((zh as Record<string, unknown>)[namespace]).sort();
    const enKeys = keys((en as Record<string, unknown>)[namespace]).sort();
    expect(enKeys).toEqual(zhKeys);
    expect(zhKeys.length).toBeGreaterThan(0);
  });

  it.each(WS7_NAMESPACES)("has no CJK left in the %s English namespace", (namespace) => {
    const offenders = leaves((en as Record<string, unknown>)[namespace]).filter(
      (leaf) => typeof leaf.value === "string" && CJK.test(leaf.value),
    );
    expect(offenders).toEqual([]);
  });

  it("labels the trial run resource in both locales", () => {
    expect(zh.scenarioTemplates.trial.resourceLabel).toBeTruthy();
    expect(en.scenarioTemplates.trial.resourceLabel).toBeTruthy();
  });

  it("interpolates placeholders the same way in both locales", () => {
    const placeholders = (value: unknown) =>
      [...String(value).matchAll(/%\{(\w+)\}/g)].map((match) => match[1]).sort();
    const zhLeaves = leaves(zh.scenarioTemplates);
    const enMap = new Map(leaves(en.scenarioTemplates).map((leaf) => [leaf.path, leaf.value]));
    zhLeaves.forEach((leaf) => {
      const counterpart = enMap.get(leaf.path);
      if (typeof leaf.value !== "string" || typeof counterpart !== "string") return;
      expect(placeholders(counterpart)).toEqual(placeholders(leaf.value));
    });
  });
});
