/**
 * R19 structure editor — the pure half (doc/288 §8.7 / §11.1, doc/295 §2.3).
 *
 * These tests are about the *contract*, not the widgets: the draft must round-trip a
 * payload the server can accept, the client validation must be the server's rules
 * rather than a friendlier subset (a rule the form forgets is a 400/44105 the author
 * discovers after typing), and the two identity rules must hold — `ord` is the array
 * order, and a version is never edited in place because there is nothing here that
 * could overwrite one.
 */
import { describe, expect, it } from "vitest";
import {
  TEMPLATE_SLICE_MAX_SECONDS_CEILING,
  blankSection,
  draftFromPayload,
  draftToPayload,
  moveSection,
  nextSectionID,
  removeSection,
  sectionDependencyCycle,
  templateOptionLabel,
  templateRoleLabel,
  validateTemplateDraft,
  type TemplateDraft,
} from "./template-editor";
import { tZh } from "../../test/translate";

/** The shipped compliance shape, trimmed to three sections. */
const payload = {
  name: { zh: "制度符合性分析报告", en: "Compliance analysis report" },
  output_formats: ["docx", "md", "pdf"],
  sections: [
    {
      key: "background",
      title: "背景与范围",
      prompt: "说明评估范围。",
      requires_evidence: false,
      max_words: 300,
    },
    {
      key: "compliance",
      title: "逐条符合性判定",
      prompt: "逐条判定符合性。",
      requires_evidence: true,
      max_words: 1200,
      depends_on: ["background"],
    },
    {
      key: "remediation",
      title: "整改建议",
      prompt: "给出整改动作。",
      requires_evidence: true,
      max_words: 600,
      depends_on: ["compliance"],
    },
  ],
  limits: {
    max_sections: 10,
    slice_max_seconds: 480,
    total_max_seconds: 3600,
    max_slices: 8,
    max_tokens_per_section: 1600,
  },
  retrieval: { top_k: 8, threshold_policy: "dataset_calibrated", min_evidence: 1 },
  evidence_policy: {
    conflict_handling: "disclose",
    outdated_evidence: "flag",
    unsupported_claims: "prohibit",
  },
  outline_confirmation: "required",
};

function draft(overrides: Partial<TemplateDraft> = {}): TemplateDraft {
  return { ...draftFromPayload(payload), ...overrides };
}

describe("draftFromPayload", () => {
  it("round-trips a published payload without losing a field", () => {
    expect(draftToPayload(draftFromPayload(payload))).toEqual(payload);
  });

  it("opens a half-authored row as an editable template instead of an empty form", () => {
    const empty = draftFromPayload({ name: { zh: "只有名字" } });
    expect(empty.nameZh).toBe("只有名字");
    expect(empty.nameEn).toBe("");
    expect(empty.outputFormats).toEqual(["docx", "md", "pdf"]);
    expect(empty.sections).toEqual([]);
    // Budget defaults are the shipped baselines, so the author sees what a save would
    // publish rather than zeros the server would refuse.
    expect(empty.maxSections).toBe("10");
    expect(empty.sliceMaxSeconds).toBe("480");
    expect(empty.thresholdPolicy).toBe("dataset_calibrated");
    expect(empty.conflictHandling).toBe("disclose");
    expect(empty.outlineConfirmation).toBe("required");
  });

  it("falls back on an unknown enum value rather than rendering it", () => {
    const seeded = draftFromPayload({
      ...payload,
      outline_confirmation: "sometimes",
      evidence_policy: { ...payload.evidence_policy, conflict_handling: "guess" },
    });
    expect(seeded.outlineConfirmation).toBe("required");
    expect(seeded.conflictHandling).toBe("disclose");
  });
});

describe("validateTemplateDraft", () => {
  it("accepts the shipped shape", () => {
    expect(validateTemplateDraft(draft(), tZh)).toEqual([]);
  });

  it("names the section instead of a stack index", () => {
    const broken = draft();
    broken.sections[1] = { ...broken.sections[1], prompt: "   " };
    const errors = validateTemplateDraft(broken, tZh);
    expect(errors).toEqual(["第 2 节的写作要求不能为空。"]);
  });

  it("refuses a duplicate key and points at the earlier section", () => {
    const broken = draft();
    broken.sections[2] = { ...broken.sections[2], key: "compliance" };
    expect(validateTemplateDraft(broken, tZh)).toEqual([
      "第 3 节的 key「compliance」与第 2 节重复。",
    ]);
  });

  it("refuses a template with no evidence-carrying section", () => {
    const broken = draft();
    broken.sections = broken.sections.map((section) => ({
      ...section,
      requiresEvidence: false,
      dependsOn: [],
    }));
    expect(validateTemplateDraft(broken, tZh)).toEqual([
      "至少要有一节需要证据，否则报告会退化成观点文。",
    ]);
  });

  it("refuses a dependency on a section id that is not in the draft", () => {
    const broken = draft();
    broken.sections[2] = { ...broken.sections[2], dependsOn: ["ghost"] };
    expect(validateTemplateDraft(broken, tZh)).toEqual([
      "第 3 节依赖的章节已不在模板里，请重新选择。",
    ]);
  });

  it("refuses a dependency cycle with the path, like the server", () => {
    const broken = draft();
    // Dependencies are ids in the draft; the cycle is still printed with keys, because
    // a reader cannot act on "s0 -> s2 -> s1".
    broken.sections[0] = { ...broken.sections[0], dependsOn: [broken.sections[2].id] };
    expect(validateTemplateDraft(broken, tZh)).toEqual([
      "章节依赖成环：background -> remediation -> compliance -> background。",
    ]);
  });

  it("keeps the slice cap under the runtime job timeout", () => {
    const broken = draft({ sliceMaxSeconds: String(TEMPLATE_SLICE_MAX_SECONDS_CEILING + 1) });
    expect(validateTemplateDraft(broken, tZh)).toContain(
      "单切片秒数不能超过 480 秒（运行时任务超时的 80%）。",
    );
  });

  it("refuses a slice budget that cannot cover the declared total", () => {
    // The server's rule: a healthy run must not be guaranteed to end `partial` for a
    // configuration reason.
    const broken = draft({ maxSlices: "4", totalMaxSeconds: "3600" });
    expect(validateTemplateDraft(broken, tZh)).toContain(
      "最多切片数 × 单切片秒数（1920 秒）覆盖不了总秒数上限（3600 秒）。",
    );
  });

  it("treats an empty number input as invalid, not as zero", () => {
    const broken = draft({ maxSections: "" });
    expect(validateTemplateDraft(broken, tZh)).toEqual(["最多章节数 必须是正整数。"]);
  });

  it("requires roles before outline confirmation may be switched off", () => {
    const broken = draft({ outlineConfirmation: "auto" });
    expect(validateTemplateDraft(broken, tZh)).toEqual([
      "选择「允许自动确认」时必须指定可自动确认的角色。",
    ]);
    expect(validateTemplateDraft({ ...broken, autoRoles: ["operator"] }, tZh)).toEqual([]);
  });
});

describe("draftToPayload", () => {
  it("omits depends_on and auto_confirm_allowed_roles when they mean nothing", () => {
    const out = draftToPayload(draft());
    expect(out.sections[0]).not.toHaveProperty("depends_on");
    // A role list next to `required` would read like a permission that is in force.
    expect(out).not.toHaveProperty("auto_confirm_allowed_roles");
  });

  it("carries the roles once auto is chosen", () => {
    const out = draftToPayload(draft({ outlineConfirmation: "auto", autoRoles: ["operator"] }));
    expect(out.outline_confirmation).toBe("auto");
    expect(out.auto_confirm_allowed_roles).toEqual(["operator"]);
  });

  it("translates dependency ids back into the keys the server stores", () => {
    const rows = draft();
    const out = draftToPayload(rows);
    expect(out.sections[1].depends_on).toEqual(["background"]);
  });

  it("prunes a dependency whose section was removed, so the rest stays publishable", () => {
    // `removeSection` is what the form calls; slicing the array would leave a
    // dependency on an id that no longer exists.
    const rows = draft();
    const trimmed = { ...rows, sections: removeSection(rows.sections, 2) };
    const out = draftToPayload(trimmed);
    expect(out.sections.map((section) => section.key)).toEqual(["background", "compliance"]);
    expect(out.sections[1].depends_on).toEqual(["background"]);
  });

  it("keeps a dependency across a rename, because it is stored by id", () => {
    const rows = draft();
    const renamed = {
      ...rows,
      sections: rows.sections.map((section) =>
        section.key === "background" ? { ...section, key: "scope" } : section,
      ),
    };
    expect(validateTemplateDraft(renamed, tZh)).toEqual([]);
    expect(draftToPayload(renamed).sections[1].depends_on).toEqual(["scope"]);
  });

  it("sends ord as array order, never as a stored number", () => {
    const rows = draft();
    const moved = { ...rows, sections: moveSection(rows.sections, 2, -1) };
    expect(draftToPayload(moved).sections.map((section) => section.key)).toEqual([
      "background",
      "remediation",
      "compliance",
    ]);
  });
});

describe("section helpers", () => {
  it("moves only inside the array", () => {
    const rows = draft().sections;
    expect(moveSection(rows, 0, -1)).toBe(rows);
    expect(moveSection(rows, rows.length - 1, 1)).toBe(rows);
    expect(moveSection(rows, 0, 1).map((section) => section.key)).toEqual([
      "compliance",
      "background",
      "remediation",
    ]);
  });

  it("hands out an id that is not already taken", () => {
    expect(nextSectionID([])).toBe("s0");
    expect(nextSectionID([blankSection("s0"), blankSection("s1")])).toBe("s2");
    // A removed-then-added section never reuses an id that is still on screen.
    expect(nextSectionID([blankSection("s0"), blankSection("s2")])).toBe("s3");
  });

  it("finds no cycle in an acyclic graph", () => {
    expect(sectionDependencyCycle(draft().sections)).toBeNull();
  });
});

describe("label helpers", () => {
  it("resolves every closed-set value through the dictionary", () => {
    expect(templateOptionLabel(tZh, "format", "docx")).toBe("Word（docx）");
    expect(templateOptionLabel(tZh, "conflict", "prefer_latest")).toBe("取最新一条");
    expect(templateOptionLabel(tZh, "outline", "auto")).toBe("允许自动确认");
  });

  it("reuses the user vocabulary for a role instead of inventing a second one", () => {
    expect(templateRoleLabel(tZh, "operator")).toBe("内容运营员");
  });
});
