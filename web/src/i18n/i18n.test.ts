import { describe, expect, it } from "vitest";
import {
  ARTIFACT_STATUSES,
  COMPLETENESS_VALUES,
  EVIDENCE_STATUSES,
  QUALITY_STATUSES,
  RESEARCH_DEPTHS,
  RESEARCH_PLANNING_STATES,
  REPORT_LIFECYCLE_STATUSES,
  GAP_IMPACTS,
  GAP_REASONS,
  JOB_STATUSES,
  REPORT_STAGES,
  REVISION_STATUSES,
  SCHEDULE_CADENCES,
  SCHEDULE_RUN_STATUSES,
  SCHEDULE_UPDATE_MODES,
  SECTION_STATUSES,
  TEMPLATE_SOURCES,
  TEMPLATE_STATUSES,
} from "../resources/report-center/report-center-api";
import { REPORT_EXPORT_FORMATS } from "../resources/conversation-center/report/report-contract";
import {
  TEMPLATE_AUTO_ROLES,
  TEMPLATE_CONFLICT_HANDLINGS,
  TEMPLATE_OUTLINE_CONFIRMATIONS,
  TEMPLATE_OUTDATED_EVIDENCE,
  TEMPLATE_OUTPUT_FORMATS,
  TEMPLATE_THRESHOLD_POLICIES,
  TEMPLATE_UNSUPPORTED_CLAIMS,
} from "../resources/report-center/template-editor";
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

describe("search apps i18n", () => {
  it("keeps the searchApps keys in sync between zh and en", () => {
    expect(keys(zh.searchApps).sort()).toEqual(keys(en.searchApps).sort());
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

describe("message placeholders", () => {
  // react-admin's `translate` interpolates `%{name}`. A message written with
  // `{{name}}` therefore renders the braces verbatim: the report workspace printed
  // "{{count}} 节 · {{count}} 节需证据" and "工作区 {{workspace}} · …" on screen
  // (found live on 4.151, 2026-11-18). Every message in both locales must use the
  // syntax the translator actually implements.
  const DOUBLED = /\{\{[^}]*\}\}/;

  it("has no mustache-style placeholders in any message", () => {
    const offenders = [...leaves(zh), ...leaves(en)].filter(
      (leaf) => typeof leaf.value === "string" && DOUBLED.test(leaf.value),
    );
    expect(offenders).toEqual([]);
  });

  it("keeps the report placeholders the translator can fill", () => {
    // The three the live pass proved broken, now in the working syntax.
    expect(zh.conversationCenter.report_template_sections).toContain("%{count}");
    expect(zh.conversationCenter.report_template_evidence).toContain("%{count}");
    expect(zh.conversationCenter.report_workspace_footer).toContain("%{workspace}");
    expect(en.conversationCenter.report_workspace_footer).toContain("%{workspace}");
    expect(zh.reportCenter.reader_citations).toContain("%{count}");
    expect(zh.reportCenter.detail_viewing_history).toContain("%{revision}");
  });

  it("uses one interpolation syntax across the dictionaries", () => {
    // `%{...}` and `{...}` mixed in one message is the same bug wearing a hat.
    const mixed = [...leaves(zh), ...leaves(en)].filter(
      (leaf) =>
        typeof leaf.value === "string" &&
        /%\{[^}]*\}/.test(leaf.value) &&
        /(?<!%)%?\{(?!\{)[^}%]*\}/.test(leaf.value.replace(/%\{[^}]*\}/g, "")),
    );
    expect(mixed.map((leaf) => leaf.path)).toEqual([]);
  });
});

describe("report enum labels", () => {
  // Every user-visible enum word lives in the dictionary (a TS constant map made
  // the English build print Chinese, 2026-11-18). The closed sets are exported by
  // the view model, so "did we translate every value the backend can write" is a
  // mechanical question rather than a review question.
  const GROUPS: Array<[string, readonly string[]]> = [
    ["revision", REVISION_STATUSES],
    ["lifecycle", REPORT_LIFECYCLE_STATUSES],
    ["completeness", COMPLETENESS_VALUES],
    ["section", SECTION_STATUSES],
    ["quality", QUALITY_STATUSES],
    ["evidence", EVIDENCE_STATUSES],
    ["artifact", ARTIFACT_STATUSES],
    ["stage", REPORT_STAGES],
    ["job", JOB_STATUSES],
    ["gap", GAP_REASONS],
    ["gap_impact", GAP_IMPACTS],
    // P2 (doc/295): R18's depth and planning states, R20's cadence / rebuild scope /
    // run status, R19's source and status. Each is a closed set the server writes.
    ["research_depth", RESEARCH_DEPTHS],
    ["research_planning", RESEARCH_PLANNING_STATES],
    ["schedule_cadence", SCHEDULE_CADENCES],
    ["schedule_update_mode", SCHEDULE_UPDATE_MODES],
    ["schedule_status", SCHEDULE_RUN_STATUSES],
    ["template_source", TEMPLATE_SOURCES],
    ["template_status", TEMPLATE_STATUSES],
    // R19 authoring (the structure editor): the six closed sets a workspace may
    // choose from when publishing its own template version.
    ["template_format", TEMPLATE_OUTPUT_FORMATS],
    ["template_threshold", TEMPLATE_THRESHOLD_POLICIES],
    ["template_conflict", TEMPLATE_CONFLICT_HANDLINGS],
    ["template_outdated", TEMPLATE_OUTDATED_EVIDENCE],
    ["template_unsupported", TEMPLATE_UNSUPPORTED_CLAIMS],
    ["template_outline", TEMPLATE_OUTLINE_CONFIRMATIONS],
    // The five report delivery formats of the export buttons (queue item 4).
    ["export_format", REPORT_EXPORT_FORMATS],
  ];

  it.each(GROUPS)("labels every %s value in both locales", (group, values) => {
    const missing: string[] = [];
    for (const value of values) {
      for (const [locale, dictionary] of [["zh", zh], ["en", en]] as const) {
        const path = `reportCenter.label_${group}.${value}`;
        const entry = leaves(dictionary).find((leaf) => leaf.path === path);
        if (typeof entry?.value !== "string" || entry.value.trim() === "") {
          missing.push(`${locale}:${path}`);
        }
      }
    }
    expect(missing).toEqual([]);
  });

  it("labels every role the template editor may grant auto confirmation to", () => {
    // The editor does not invent a second role vocabulary: it reuses `users.role_*`,
    // so this asserts the keys exist rather than that a copy is up to date.
    const missing: string[] = [];
    for (const role of TEMPLATE_AUTO_ROLES) {
      for (const [locale, dictionary] of [["zh", zh], ["en", en]] as const) {
        const path = `users.role_${role}`;
        const entry = leaves(dictionary).find((leaf) => leaf.path === path);
        if (typeof entry?.value !== "string" || entry.value.trim() === "") {
          missing.push(`${locale}:${path}`);
        }
      }
    }
    expect(missing).toEqual([]);
  });

  it("keeps the two dictionary enum groups the same size", () => {
    expect(keys(zh.reportCenter.label_evidence).sort()).toEqual(
      keys(en.reportCenter.label_evidence).sort(),
    );
    expect(keys(zh.reportCenter.label_revision).sort()).toEqual(
      keys(en.reportCenter.label_revision).sort(),
    );
  });

  it("has no second vocabulary for one enum", () => {
    // The workspace drawer used to word evidence statuses its own way
    // ("已支持" vs the reader's "证据充分"), which reads like two judgements.
    const duplicates = leaves(zh)
      .map((leaf) => leaf.path)
      .filter((path) => /^conversationCenter\.report_(evidence|quality|section)_status_/.test(path));
    expect(duplicates).toEqual([]);
  });
});

describe("agents i18n", () => {
  // The agent canvas, chat panel and version diff shipped their labels as
  // Chinese string literals, so the English build printed Chinese (found live on
  // 4.151, 2026-11-19). The keys live under `agents.*`; both dictionaries must
  // stay in sync and the English side must not fall back to CJK.
  it("keeps the agents keys in sync between zh and en", () => {
    expect(keys(zh.agents).sort()).toEqual(keys(en.agents).sort());
  });

  it("has no CJK left in the English agents namespace", () => {
    const offenders = leaves((en as Record<string, unknown>).agents).filter(
      (leaf) => typeof leaf.value === "string" && CJK.test(leaf.value),
    );
    expect(offenders).toEqual([]);
  });

  it("interpolates the agent placeholders the same way in both locales", () => {
    const placeholders = (value: unknown) =>
      [...String(value).matchAll(/%\{(\w+)\}/g)].map((match) => match[1]).sort();
    const enMap = new Map(leaves(en.agents).map((leaf) => [leaf.path, leaf.value]));
    leaves(zh.agents).forEach((leaf) => {
      const counterpart = enMap.get(leaf.path);
      if (typeof leaf.value !== "string" || typeof counterpart !== "string") return;
      expect(placeholders(counterpart)).toEqual(placeholders(leaf.value));
    });
  });

  it("names the platform workspace instead of the deleted fallback", () => {
    // `GET /tenants` never returns the platform tenant, so a lookup miss is
    // expected for platform-owned rows; reading "已删除" there is a rendering
    // fallback that lies about the data.
    expect(zh.common.platform_tenant).toBeTruthy();
    expect(en.common.platform_tenant).toBeTruthy();
    expect(zh.common.platform_tenant).not.toBe(zh.common.deleted_reference);
    expect(en.common.platform_tenant).not.toBe(en.common.deleted_reference);
  });

  it("labels every canvas connection rejection the DSL validator can return", () => {
    const reasons = [
      "missing_endpoint",
      "self_loop",
      "duplicate_edge",
      "terminal_source",
      "begin_target",
      "cycle",
    ];
    reasons.forEach((reason) => {
      const key = `canvas_connect_${reason}`;
      expect((zh.agents as Record<string, string>)[key]).toBeTruthy();
      expect((en.agents as Record<string, string>)[key]).toBeTruthy();
    });
    expect(zh.agents.canvas_connect_rejected).toBeTruthy();
    expect(en.agents.canvas_connect_rejected).toBeTruthy();
  });
});
