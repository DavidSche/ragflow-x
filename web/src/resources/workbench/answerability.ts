/**
 * Answerability rendering helpers (doc/301 T12 / QA-1).
 *
 * The server stores one claim per turn (`service.AnswerabilityClaim`). This module
 * turns it into a *state* and a dictionary key, and deliberately does nothing else:
 * no wording, no tone by accident, no defaults that could turn "we did not measure"
 * into "it was fine".
 *
 * Three rules, same shape as the assurance panel's:
 *   1. a missing claim renders as `not_recorded`, never as `answerable`;
 *   2. a value outside the closed set is rendered **raw** — a grown server enum must
 *      show up as itself rather than as a generic "unknown" that hides which value
 *      arrived;
 *   3. `self_abstain` (the model declined) is a different fact from `no_evidence`
 *      (the corpus was empty). They share a surface but never share a sentence.
 */
import type { Translate } from "../conversation-center/report/report-assurance";
import type { AnswerabilityClaim } from "./workbench-types";

/** Mirrors `service.AnswerabilityReasons()`; `none` is a real value, not "missing". */
export const ANSWERABILITY_REASONS = [
  "none",
  "no_answer",
  "self_abstain",
  "low_similarity",
  "no_evidence",
] as const;

export type AnswerabilityReason = (typeof ANSWERABILITY_REASONS)[number];

/** A frontend-only state: the turn predates the field, or never reached the ledger. */
export const ANSWERABILITY_NOT_RECORDED = "not_recorded";

export type AnswerabilityState = AnswerabilityReason | typeof ANSWERABILITY_NOT_RECORDED;

export function isAnswerabilityReason(value: string): value is AnswerabilityReason {
  return (ANSWERABILITY_REASONS as readonly string[]).includes(value);
}

/**
 * `answerabilityState` resolves the claim to exactly one state.
 *
 * `answerable === true` with no reason is an old row written before the reason was
 * always set: it *was* answered, so it renders as `answerable` rather than as
 * "not recorded". Anything else with no reason stays `not_recorded` — we cannot name
 * what we did not record.
 */
export function answerabilityState(claim?: AnswerabilityClaim | null): AnswerabilityState {
  if (!claim) return ANSWERABILITY_NOT_RECORDED;
  const reason = (claim.abstain_reason ?? "").trim();
  if (!reason || reason === "none") {
    return claim.answerable === true ? "answerable" : ANSWERABILITY_NOT_RECORDED;
  }
  // Outside the closed set: keep the raw server value so it is visible.
  return reason as AnswerabilityState;
}

function keyOf(state: AnswerabilityState): string {
  return state === "none" ? "answerable" : state;
}

/** `labelOr` semantics: an unknown state renders as itself, never as a blank badge. */
export function answerabilityLabel(t: Translate, state: AnswerabilityState): string {
  const key = `qa.answerability.${keyOf(state)}`;
  return t(key, { _: state });
}

/**
 * A tooltip only exists where the dictionary has one, and it is only offered for the
 * three states where a reader can act differently. Returning `undefined` keeps the
 * badge from showing a `title` that says nothing.
 */
export function answerabilityTooltip(t: Translate, state: AnswerabilityState): string | undefined {
  switch (state) {
    case "answerable":
      return knownText(t, "qa.answerability.tooltip_answerable");
    case "no_evidence":
      return knownText(t, "qa.answerability.tooltip_no_evidence");
    case "self_abstain":
      return knownText(t, "qa.answerability.tooltip_self_abstain");
    default:
      return undefined;
  }
}

/**
 * `knownText` returns `undefined` when the dictionary has no such key. `ra-core`
 * answers an unknown key with the key itself, so an absent tooltip would otherwise
 * surface as a `title` attribute reading `qa.answerability.tooltip_…`.
 */
function knownText(t: Translate, key: string): string | undefined {
  const text = t(key);
  return text === key ? undefined : text;
}

/**
 * `answerabilityIsKnowledgeGap`: `no_evidence` and `low_similarity` are statements
 * about the corpus; `self_abstain` is a statement about the model. The board sorts by
 * this, because "the knowledge base does not cover it" is addressable content work and
 * "the model refused" is not.
 */
export function answerabilityIsKnowledgeGap(state: AnswerabilityState): boolean {
  return state === "no_evidence" || state === "low_similarity";
}

/**
 * The board's filter groups. Ordering is the point: a corpus gap is content work an
 * operator can start today, a refusal is a model judgement they cannot, and
 * `answerable`/`no_answer` are the two ends of the outcome axis rather than reasons to
 * chase. The values are server reasons (`none`, not `answerable`).
 */
export const ANSWERABILITY_FILTER_GROUPS: { labelKey: string; reasons: AnswerabilityReason[] }[] = [
  { labelKey: "qa.answerability.filter_group_gap", reasons: ["no_evidence", "low_similarity"] },
  { labelKey: "qa.answerability.filter_group_model", reasons: ["self_abstain"] },
  { labelKey: "qa.answerability.filter_group_other", reasons: ["none", "no_answer"] },
];

/** Tailwind classes for the badge; tone is presentation, never meaning. */
export function answerabilityTone(state: AnswerabilityState): string {
  switch (state) {
    case "answerable":
      return "border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300";
    case "no_evidence":
    case "low_similarity":
      return "border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-300";
    case "no_answer":
    case "self_abstain":
      return "border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300";
    default:
      return "border-dashed border-muted-foreground/40 bg-transparent text-muted-foreground";
  }
}

/**
 * `describeAnswerabilityEvidence` renders the numbers the claim carries, and omits
 * them when there are none: "0 段引用" on a `not_recorded` turn would read as a fact
 * about retrieval rather than as an absence of measurement.
 */
export function describeAnswerabilityEvidence(t: Translate, claim?: AnswerabilityClaim | null): string {
  if (!claim) return "";
  const parts: string[] = [];
  if (typeof claim.citation_count === "number" && claim.citation_count > 0) {
    parts.push(t("qa.answerability.citations_count", { count: claim.citation_count, _: `${claim.citation_count} 段引用` }));
  }
  if (typeof claim.distinct_docs === "number" && claim.distinct_docs > 0) {
    parts.push(t("qa.answerability.distinct_docs", { count: claim.distinct_docs, _: `${claim.distinct_docs} 篇文档` }));
  }
  if (typeof claim.confidence === "number" && claim.confidence > 0) {
    parts.push(t("qa.answerability.confidence", { value: claim.confidence.toFixed(2), _: `最高相关度 ${claim.confidence.toFixed(2)}` }));
  }
  return parts.join(" · ");
}
