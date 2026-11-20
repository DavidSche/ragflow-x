/**
 * AnswerabilityBadge – the per-turn honesty badge (doc/301 T12 / QA-1).
 *
 * It renders one state and nothing else: no percentage, no green tick by default, and
 * no sentence of its own. All wording lives in `qa.answerability.*`; the component
 * keeps one decision — a turn with no claim says "not recorded" instead of silently
 * looking answerable.
 */
import { useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import type { AnswerabilityClaim } from "./workbench-types";
import {
  answerabilityLabel,
  answerabilityState,
  answerabilityTone,
  answerabilityTooltip,
  describeAnswerabilityEvidence,
} from "./answerability";

interface AnswerabilityBadgeProps {
  claim?: AnswerabilityClaim | null;
  /** Ask for the evidence counts in the tooltip; off in dense list rows. */
  showEvidence?: boolean;
  className?: string;
}

export function AnswerabilityBadge({ claim, showEvidence = false, className }: AnswerabilityBadgeProps) {
  const t = useTranslate();
  const state = answerabilityState(claim);
  const label = answerabilityLabel(t, state);
  const tooltip = answerabilityTooltip(t, state);
  const evidence = showEvidence ? describeAnswerabilityEvidence(t, claim) : "";
  const title = [tooltip, evidence].filter(Boolean).join(" · ") || undefined;

  return (
    <Badge
      variant="outline"
      className={`${answerabilityTone(state)} ${className ?? ""}`}
      data-answerability={state}
      title={title}
    >
      {label}
    </Badge>
  );
}
