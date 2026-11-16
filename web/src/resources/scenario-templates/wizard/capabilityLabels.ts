/**
 * Labels for the scenario-template capability vocabulary.
 *
 * Two different vocabularies appear on the same screen, and only one of them was
 * ever translated: the closed set a template may *require*
 * (`internal/service/scenario_template_knowledge.go`) and the probe's own
 * capability names. The wizard rendered requirement names through
 * `t('scenarioTemplates.capability.name.' + capability, { defaultValue })`, so
 * `citation_locator` and `metadata_filter` came out as the raw key path — in red,
 * which reads like an error message rather than a capability name (seen at 0:19
 * of the v0.6.0 promo, reproduced in the wizard).
 *
 * The `defaultValue` half of that bug is the sneaky one: the app's i18n provider
 * is `ra-i18n-polyglot`, and node-polyglot reads its default from the `_` option
 * — `defaultValue` is i18next's spelling and is silently ignored, so a missing
 * key renders the whole path. `translateOr` below is the only sanctioned way to
 * ask for a label with a fallback; `capabilityLabels.test.ts` pins both halves.
 */

export type Translate = (key: string, options?: Record<string, unknown>) => string;

/**
 * Mirror of `ValidScenarioCapabilities` in
 * `internal/service/scenario_template_knowledge.go`. A Go test cannot see this
 * file and the compiler cannot see that one, so `capabilityLabels.test.ts` reads
 * the Go source and fails when the two drift apart.
 */
export const SCENARIO_TEMPLATE_CAPABILITIES = [
  "vector",
  "metadata_filter",
  "metadata_filter_pushdown",
  "graph",
  "raptor",
  "vision",
  "citation_locator",
  "wiki",
] as const;

/** `RequirementVerdict.Reason` values written by `knowledge_capability.go`. */
export const REQUIREMENT_REASONS = [
  "capability_not_supported",
  "metadata_not_ready",
  "below_min_level",
  "chunk_method_mismatch",
] as const;

/** `identifier_case` → `identifier case`: a readable last resort, never a key. */
export function humanizeEnum(value: string): string {
  return value.replace(/_/g, " ").trim() || value;
}

/**
 * Translate with a guaranteed-readable fallback. Returns `fallback` when the key
 * is missing (polyglot hands back the key itself) or empty, so a dictionary gap
 * degrades to `metadata filter` instead of
 * `scenarioTemplates.capability.name.metadata_filter`.
 */
export function translateOr(t: Translate, key: string, fallback: string): string {
  const text = t(key, { _: fallback });
  if (typeof text !== "string" || text.length === 0 || text === key) return fallback;
  return text;
}

export function capabilityName(t: Translate, capability: string): string {
  return translateOr(
    t,
    `scenarioTemplates.capability.name.${capability}`,
    humanizeEnum(capability),
  );
}

export function capabilityReason(t: Translate, reason: string): string {
  return translateOr(
    t,
    `scenarioTemplates.capability.reason.${reason}`,
    humanizeEnum(reason),
  );
}

export function capabilityRole(t: Translate, role: string): string {
  return translateOr(t, `scenarioTemplates.capability.role.${role}`, humanizeEnum(role));
}

export function capabilityVerdict(t: Translate, verdict: string): string {
  return translateOr(
    t,
    `scenarioTemplates.capability.verdict.${verdict}`,
    humanizeEnum(verdict),
  );
}
