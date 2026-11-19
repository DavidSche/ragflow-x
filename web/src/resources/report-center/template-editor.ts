// R19 template structure authoring (doc/295 §2.3, doc/288 §11.1).
//
// The authoring endpoints (`PUT /report-templates/:key`, `POST /report-templates`)
// shipped with the P2 governance pass, but the panel only ever *re-published the row
// it had just read*: a workspace could fork the platform baseline and then never
// change a section, a prompt or a budget. A template whose content can only be
// authored with `curl` is the same gap as a missing endpoint — the difference is that
// the missing endpoint is visible in the route table.
//
// This module is the pure half of the editor: draft <-> payload, and the *same*
// rules the server applies (`validateReportTemplatePayload`), checked before the
// request so a 400/44105 is a sentence rather than a round trip. The server keeps
// its copy: this one is an early-exit, never the authority.
//
// Three shapes deserve a word:
//  - **numbers travel as strings** while editing. `""` is a legal intermediate state
//    in an input, `0` is not a legal `max_sections`; keeping the raw text means "I
//    deleted the digit" stays visible instead of silently becoming a valid zero.
//  - **section order is the array order**. `ord` is 1-based and derived from the
//    index (`reportSectionNumber` owns that interpretation, not the editor), so
//    moving a section is a move in the array and nothing else.
//  - **`depends_on` is filtered to sections that exist**, because a dependency on a
//    deleted key would make the *whole* template un-publishable with an error that
//    names a section the author can no longer see.

// The formats a template may *advertise* (server: `reportOutputFormats`). It is a
// declaration, not a gate: the export endpoint renders every format of
// `REPORT_EXPORT_FORMATS` for any published revision, so a template that omits
// one does not hide the button — it just does not promise that file.
export const TEMPLATE_OUTPUT_FORMATS = ["docx", "md", "pdf", "html", "xlsx"] as const;
export const TEMPLATE_THRESHOLD_POLICIES = ["dataset_calibrated"] as const;
export const TEMPLATE_CONFLICT_HANDLINGS = ["disclose", "block", "prefer_latest"] as const;
export const TEMPLATE_OUTDATED_EVIDENCE = ["flag", "exclude"] as const;
export const TEMPLATE_UNSUPPORTED_CLAIMS = ["prohibit", "disclose"] as const;
export const TEMPLATE_OUTLINE_CONFIRMATIONS = ["required", "auto"] as const;

/**
 * Roles the template may name as allowed to skip the outline review (292 D2).
 *
 * `auto_confirm_allowed_roles` is a forward-looking latch in this release: no route
 * lets anyone but the owner grant `auto`, so the list is recorded and not yet
 * consulted. The editor still asks for it because the payload validator *requires*
 * it whenever `outline_confirmation=auto` — without it the switch could not be
 * turned on at all.
 */
export const TEMPLATE_AUTO_ROLES = ["tenant_admin", "operator", "viewer"] as const;

/**
 * `slice_max_seconds` ceiling: 80% of the 600s runtime job timeout
 * (`sliceMaxSecondsCeiling()` on the server). A slice that outlives its job can never
 * checkpoint, so the lease expires and the work is redone forever (doc/288 §7.9).
 */
export const TEMPLATE_SLICE_MAX_SECONDS_CEILING = 480;

type T = (key: string, options?: Record<string, unknown>) => string;

/** The authoring payload, field for field what `ReportTemplatePayload` decodes. */
export interface ReportTemplatePayloadDraft {
  name: { zh: string; en: string };
  output_formats: string[];
  sections: Array<{
    key: string;
    title: string;
    prompt: string;
    requires_evidence: boolean;
    max_words: number;
    depends_on?: string[];
  }>;
  limits: {
    max_sections: number;
    slice_max_seconds: number;
    total_max_seconds: number;
    max_slices: number;
    max_tokens_per_section: number;
  };
  retrieval: { top_k: number; threshold_policy: string; min_evidence: number };
  evidence_policy: {
    conflict_handling: string;
    outdated_evidence: string;
    unsupported_claims: string;
  };
  outline_confirmation: string;
  auto_confirm_allowed_roles?: string[];
}

export interface TemplateSectionDraft {
  /** React key only: a section that is being renamed has no stable key yet. */
  id: string;
  key: string;
  title: string;
  prompt: string;
  requiresEvidence: boolean;
  maxWords: string;
  /**
   * **Section ids**, not keys.
   *
   * A dependency stored as a key breaks twice: renaming the key silently unlinks it,
   * and removing the section leaves a reference the author can no longer tick off
   * (the checkbox for a gone section is gone). Ids survive a rename and are pruned by
   * `removeSection`, so the two edits the form offers cannot produce a dead end.
   * `draftToPayload` translates them to the keys the server stores.
   */
  dependsOn: string[];
}

export interface TemplateDraft {
  nameZh: string;
  nameEn: string;
  outputFormats: string[];
  sections: TemplateSectionDraft[];
  maxSections: string;
  sliceMaxSeconds: string;
  totalMaxSeconds: string;
  maxSlices: string;
  maxTokensPerSection: string;
  topK: string;
  thresholdPolicy: string;
  minEvidence: string;
  conflictHandling: string;
  outdatedEvidence: string;
  unsupportedClaims: string;
  outlineConfirmation: string;
  autoRoles: string[];
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function text(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}

function numericText(value: unknown, fallback: number): string {
  return typeof value === "number" && Number.isFinite(value) ? String(value) : String(fallback);
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function closed(value: unknown, allowed: readonly string[], fallback: string): string {
  const raw = text(value);
  return allowed.includes(raw) ? raw : fallback;
}

/**
 * Seeds a draft from the payload the list already returned.
 *
 * Every field has a default so a half-authored row (or a row written before a field
 * existed) opens as an editable template instead of an empty form: the defaults are
 * the shipped baselines, and the author sees what will be published.
 */
export function draftFromPayload(payload: unknown, idPrefix = "s"): TemplateDraft {
  const source = record(payload);
  const name = record(source.name);
  const limits = record(source.limits);
  const retrieval = record(source.retrieval);
  const policy = record(source.evidence_policy);
  const sections = Array.isArray(source.sections) ? source.sections : [];
  const formats = strings(source.output_formats);
  const draft: TemplateDraft = {
    nameZh: text(name.zh),
    nameEn: text(name.en),
    outputFormats: formats.length > 0 ? formats : ["docx", "md", "pdf"],
    sections: sections.map((item, index) => {
      const section = record(item);
      return {
        id: `${idPrefix}-${index}`,
        key: text(section.key),
        title: text(section.title),
        prompt: text(section.prompt),
        requiresEvidence: section.requires_evidence === true,
        maxWords: numericText(section.max_words, 300),
        dependsOn: strings(section.depends_on),
      };
    }),
    maxSections: numericText(limits.max_sections, 10),
    sliceMaxSeconds: numericText(limits.slice_max_seconds, 480),
    totalMaxSeconds: numericText(limits.total_max_seconds, 3600),
    maxSlices: numericText(limits.max_slices, 8),
    maxTokensPerSection: numericText(limits.max_tokens_per_section, 1600),
    topK: numericText(retrieval.top_k, 8),
    thresholdPolicy: closed(
      retrieval.threshold_policy,
      TEMPLATE_THRESHOLD_POLICIES,
      TEMPLATE_THRESHOLD_POLICIES[0],
    ),
    minEvidence: numericText(retrieval.min_evidence, 1),
    conflictHandling: closed(
      policy.conflict_handling,
      TEMPLATE_CONFLICT_HANDLINGS,
      TEMPLATE_CONFLICT_HANDLINGS[0],
    ),
    outdatedEvidence: closed(policy.outdated_evidence, TEMPLATE_OUTDATED_EVIDENCE, TEMPLATE_OUTDATED_EVIDENCE[0]),
    unsupportedClaims: closed(
      policy.unsupported_claims,
      TEMPLATE_UNSUPPORTED_CLAIMS,
      TEMPLATE_UNSUPPORTED_CLAIMS[0],
    ),
    outlineConfirmation: closed(
      source.outline_confirmation,
      TEMPLATE_OUTLINE_CONFIRMATIONS,
      TEMPLATE_OUTLINE_CONFIRMATIONS[0],
    ),
    autoRoles: strings(source.auto_confirm_allowed_roles),
  };
  // Second pass: the stored payload names dependencies by key, the draft keeps them
  // by id (see `TemplateSectionDraft.dependsOn`). An unknown key is dropped here
  // rather than turned into a phantom checkbox.
  const seeded = draft.sections;
  const idByKey = new Map<string, string>();
  for (const section of seeded) {
    const key = section.key.trim();
    if (key) idByKey.set(key, section.id);
  }
  for (const section of seeded) {
    section.dependsOn = section.dependsOn
      .map((key) => idByKey.get(key.trim()) ?? "")
      .filter((id) => id !== "" && id !== section.id);
  }
  return draft;
}

/** A blank section appended by "添加一节". */
export function blankSection(id: string): TemplateSectionDraft {
  return {
    id,
    key: "",
    title: "",
    prompt: "",
    requiresEvidence: false,
    maxWords: "300",
    dependsOn: [],
  };
}

/** Which sections the *next* blank section's id must not collide with. */
export function nextSectionID(sections: TemplateSectionDraft[]): string {
  let index = sections.length;
  const taken = new Set(sections.map((section) => section.id));
  while (taken.has(`s${index}`)) index += 1;
  return `s${index}`;
}

/** Moves one section within the array; `ord` is the array order, nothing else. */
export function moveSection(
  sections: TemplateSectionDraft[],
  index: number,
  delta: number,
): TemplateSectionDraft[] {
  const target = index + delta;
  if (index < 0 || index >= sections.length || target < 0 || target >= sections.length) {
    return sections;
  }
  const next = [...sections];
  const [moved] = next.splice(index, 1);
  next.splice(target, 0, moved);
  return next;
}

/**
 * Drops one section and the dependencies that pointed at it.
 *
 * Pruning here is what keeps the form closing over its own operations: a dependency
 * whose target is gone cannot be unticked (its checkbox is rendered from the target),
 * so leaving it would be an error the author can only fix by reloading the dialog.
 */
export function removeSection(
  sections: TemplateSectionDraft[],
  index: number,
): TemplateSectionDraft[] {
  const removed = sections[index];
  if (!removed) return sections;
  return sections
    .filter((_, at) => at !== index)
    .map((section) =>
      section.dependsOn.includes(removed.id)
        ? { ...section, dependsOn: section.dependsOn.filter((id) => id !== removed.id) }
        : section,
    );
}

function integer(value: string): number | null {
  const raw = value.trim();
  if (raw === "") return null;
  const parsed = Number(raw);
  return Number.isInteger(parsed) ? parsed : null;
}

/**
 * The first dependency cycle as a printable path, or `null`.
 *
 * The server refuses a cycle with `section dependency cycle: a -> b -> a`; checking
 * it here as well means the author sees the same sentence before saving rather than
 * after a rejected request.
 */
export function sectionDependencyCycle(sections: TemplateSectionDraft[]): string | null {
  const edges = new Map<string, string[]>();
  const label = new Map<string, string>();
  for (const section of sections) {
    edges.set(section.id, section.dependsOn);
    label.set(section.id, section.key.trim() || section.id);
  }
  const state = new Map<string, number>();
  const walk = (node: string, path: string[]): string | null => {
    const seen = state.get(node) ?? 0;
    if (seen === 2) return null;
    if (seen === 1) {
      const at = path.indexOf(node);
      return [...path.slice(at), node].map((id) => label.get(id) ?? id).join(" -> ");
    }
    state.set(node, 1);
    for (const child of edges.get(node) ?? []) {
      const cycle = walk(child, [...path, node]);
      if (cycle) return cycle;
    }
    state.set(node, 2);
    return null;
  };
  for (const section of sections) {
    const cycle = walk(section.id, []);
    if (cycle) return cycle;
  }
  return null;
}

/**
 * Every reason the draft cannot be published, in the order the form reads.
 *
 * Mirrors `validateReportTemplatePayload` on the server (same rules, same
 * consequences) so "保存" is never the first place a rule is mentioned. Messages are
 * sentences that name the field and the offending value: "sections[2].prompt is
 * required" is a stack trace, "第 3 节的写作要求不能为空" is a correction.
 */
export function validateTemplateDraft(draft: TemplateDraft, t: T): string[] {
  const errors: string[] = [];
  if (!draft.nameZh.trim()) errors.push(t("reportCenter.template_err_name_zh"));
  if (!draft.nameEn.trim()) errors.push(t("reportCenter.template_err_name_en"));
  if (draft.outputFormats.length === 0) errors.push(t("reportCenter.template_err_output_formats"));
  if (draft.sections.length === 0) errors.push(t("reportCenter.template_err_sections_empty"));

  const seenKeys = new Map<string, number>();
  // A duplicate or empty key makes the *key space* ambiguous, so dependency errors
  // would be noise on top of the one decision the author has to make first. The
  // dependency checks below are skipped while this is true.
  let keySpaceBroken = false;
  draft.sections.forEach((section, index) => {
    const ord = String(index + 1);
    const key = section.key.trim();
    if (!key) {
      keySpaceBroken = true;
      errors.push(t("reportCenter.template_err_section_key", { ord }));
    } else if (seenKeys.has(key)) {
      keySpaceBroken = true;
      errors.push(
        t("reportCenter.template_err_section_key_dup", {
          ord,
          other: String((seenKeys.get(key) ?? 0) + 1),
          key,
        }),
      );
    } else {
      seenKeys.set(key, index);
    }
    if (!section.title.trim()) errors.push(t("reportCenter.template_err_section_title", { ord }));
    if (!section.prompt.trim()) errors.push(t("reportCenter.template_err_section_prompt", { ord }));
    const maxWords = integer(section.maxWords);
    if (maxWords === null || maxWords < 0) {
      errors.push(t("reportCenter.template_err_section_max_words", { ord }));
    }
  });

  if (draft.sections.length > 0 && !draft.sections.some((section) => section.requiresEvidence)) {
    // A report with no evidence-carrying section is an opinion piece with a
    // compliance cover page (doc/288 §11.1).
    errors.push(t("reportCenter.template_err_requires_evidence"));
  }

  if (!keySpaceBroken) {
    const ids = new Set(draft.sections.map((section) => section.id));
    draft.sections.forEach((section, index) => {
      const ord = String(index + 1);
      for (const dependency of section.dependsOn) {
        if (dependency === section.id) {
          errors.push(t("reportCenter.template_err_depends_self", { ord }));
        } else if (!ids.has(dependency)) {
          // Unreachable through the form (`removeSection` prunes), reachable from a
          // payload: better a sentence than a silent reference to nothing.
          errors.push(t("reportCenter.template_err_depends_removed", { ord }));
        }
      }
    });
    const cycle = sectionDependencyCycle(draft.sections);
    if (cycle) errors.push(t("reportCenter.template_err_cycle", { path: cycle }));
  }

  const maxSections = integer(draft.maxSections);
  const sliceMaxSeconds = integer(draft.sliceMaxSeconds);
  const totalMaxSeconds = integer(draft.totalMaxSeconds);
  const maxSlices = integer(draft.maxSlices);
  const maxTokensPerSection = integer(draft.maxTokensPerSection);
  const positive: Array<[string, string, number | null]> = [
    ["template_limit_max_sections", draft.maxSections, maxSections],
    ["template_limit_slice_max_seconds", draft.sliceMaxSeconds, sliceMaxSeconds],
    ["template_limit_max_slices", draft.maxSlices, maxSlices],
    ["template_limit_max_tokens_per_section", draft.maxTokensPerSection, maxTokensPerSection],
  ];
  for (const [key, , value] of positive) {
    if (value === null || value <= 0) {
      errors.push(t("reportCenter.template_err_limit_positive", { field: t(`reportCenter.${key}`) }));
    }
  }
  if (sliceMaxSeconds !== null && sliceMaxSeconds > TEMPLATE_SLICE_MAX_SECONDS_CEILING) {
    errors.push(
      t("reportCenter.template_err_slice_ceiling", {
        ceiling: String(TEMPLATE_SLICE_MAX_SECONDS_CEILING),
      }),
    );
  }
  if (maxSections !== null && maxSections > 0 && draft.sections.length > maxSections) {
    errors.push(
      t("reportCenter.template_err_sections_exceed", {
        count: String(draft.sections.length),
        limit: String(maxSections),
      }),
    );
  }
  // `total_max_seconds` is optional (0 = "the slice budget decides"), so it is only
  // checked when it is given — but then both relationships must hold, or a healthy
  // run is guaranteed to end `partial` for a configuration reason.
  if (totalMaxSeconds !== null && totalMaxSeconds < 0) {
    errors.push(
      t("reportCenter.template_err_limit_positive", {
        field: t("reportCenter.template_limit_total_max_seconds"),
      }),
    );
  } else if (totalMaxSeconds !== null && totalMaxSeconds > 0 && sliceMaxSeconds !== null) {
    if (totalMaxSeconds < sliceMaxSeconds) {
      errors.push(t("reportCenter.template_err_total_short"));
    }
    if (maxSlices !== null && maxSlices * sliceMaxSeconds < totalMaxSeconds) {
      errors.push(
        t("reportCenter.template_err_slice_budget", {
          cover: String(maxSlices * sliceMaxSeconds),
          total: String(totalMaxSeconds),
        }),
      );
    }
  }

  const topK = integer(draft.topK);
  if (topK === null || topK <= 0) errors.push(t("reportCenter.template_err_top_k"));
  if (!(TEMPLATE_THRESHOLD_POLICIES as readonly string[]).includes(draft.thresholdPolicy)) {
    errors.push(t("reportCenter.template_err_threshold_policy"));
  }
  const minEvidence = integer(draft.minEvidence);
  if (minEvidence === null || minEvidence < 0) errors.push(t("reportCenter.template_err_min_evidence"));

  if (!(TEMPLATE_OUTLINE_CONFIRMATIONS as readonly string[]).includes(draft.outlineConfirmation)) {
    errors.push(t("reportCenter.template_err_outline"));
  } else if (draft.outlineConfirmation === "auto" && draft.autoRoles.length === 0) {
    // Turning confirmation off without naming who may do it hands the permission to
    // everyone (292 D2) — the server refuses it too.
    errors.push(t("reportCenter.template_err_outline_auto_roles"));
  }
  return errors;
}

/**
 * Builds the authoring payload. Invalid numbers become `0` on purpose: validation
 * has already refused them, and a payload that silently "fixes" a bad value would
 * publish a template the author never saw.
 */
export function draftToPayload(draft: TemplateDraft): ReportTemplatePayloadDraft {
  const toInt = (value: string): number => {
    const parsed = integer(value);
    return parsed === null ? 0 : parsed;
  };
  const keyById = new Map(draft.sections.map((section) => [section.id, section.key.trim()]));
  const sections = draft.sections.map((section) => {
    const dependsOn = section.dependsOn
      .map((id) => keyById.get(id) ?? "")
      .filter((key) => key !== "" && key !== section.key.trim());
    return {
      key: section.key.trim(),
      title: section.title.trim(),
      prompt: section.prompt.trim(),
      requires_evidence: section.requiresEvidence,
      max_words: toInt(section.maxWords),
      ...(dependsOn.length > 0 ? { depends_on: dependsOn } : {}),
    };
  });
  return {
    name: { zh: draft.nameZh.trim(), en: draft.nameEn.trim() },
    output_formats: [...draft.outputFormats],
    sections,
    limits: {
      max_sections: toInt(draft.maxSections),
      slice_max_seconds: toInt(draft.sliceMaxSeconds),
      total_max_seconds: toInt(draft.totalMaxSeconds),
      max_slices: toInt(draft.maxSlices),
      max_tokens_per_section: toInt(draft.maxTokensPerSection),
    },
    retrieval: {
      top_k: toInt(draft.topK),
      threshold_policy: draft.thresholdPolicy,
      min_evidence: toInt(draft.minEvidence),
    },
    evidence_policy: {
      conflict_handling: draft.conflictHandling,
      outdated_evidence: draft.outdatedEvidence,
      unsupported_claims: draft.unsupportedClaims,
    },
    outline_confirmation: draft.outlineConfirmation,
    // Only present when it means something: `auto_confirm_allowed_roles` next to
    // `required` reads like a permission that is in force.
    ...(draft.outlineConfirmation === "auto" ? { auto_confirm_allowed_roles: [...draft.autoRoles] } : {}),
  };
}

/** One enum word from the template vocabulary, via the shared `label_` groups. */
export function templateOptionLabel(t: T, group: string, value: string): string {
  if (!value) return "";
  return t(`reportCenter.label_template_${group}.${value}`, { _: value });
}

/**
 * Role label for `auto_confirm_allowed_roles`. It reuses `users.role_*` on purpose:
 * one role must not be worded twice ("内容运营员" here, "操作员" there), and the
 * user list already owns the vocabulary.
 */
export function templateRoleLabel(t: T, role: string): string {
  if (!role) return "";
  return t(`users.role_${role}`, { _: role });
}
