/**
 * The two P2 disclosures a section can carry, in one place so the reader and the
 * section drawer cannot word them differently.
 *
 *  - R17 locators: compiled structures (page index / wiki / tree) that help a reader
 *    find the source page. They are **navigation, not evidence** — the label is part
 *    of the contract, not a caption: a locator printed next to a citation list reads
 *    like one more citation, and the server deliberately sends no score, excerpt or
 *    chunk id that could make it behave like one (doc/295 A55).
 *  - R18 research plan: whether deep research ran, and if it did not, which upgrade
 *    was skipped. The three `fallback_*` states are degradations, so they are shown
 *    as a neutral note — never as an error, and never on a section that succeeded
 *    from its own retrieval.
 *
 * Neither block adds facts. Both are rendered from the read API only.
 */
import { useTranslate } from "ra-core";
import { Compass, Radar } from "lucide-react";
import {
  reportResearchPlanningLabel,
  reportSectionLocationHints,
  reportSectionResearchPlan,
  shouldShowResearchPlan,
} from "./report-center-api";

export interface ReportSectionDisclosureProps {
  section: {
    ord?: number;
    location_hints?: unknown;
    research_plan?: unknown;
  };
}

export function ReportSectionDisclosure({ section }: ReportSectionDisclosureProps) {
  const t = useTranslate();
  const hints = reportSectionLocationHints(section);
  const plan = reportSectionResearchPlan(section);
  const showPlan = shouldShowResearchPlan(plan);
  if (hints.length === 0 && !showPlan) return null;
  const ord = section.ord ?? 0;

  return (
    <div className="mt-2 space-y-1 text-xs text-muted-foreground">
      {hints.length > 0 && (
        <p data-testid={`report-section-hints-${ord}`}>
          <Compass className="mr-1 inline size-3" />
          {/* The label is fixed wording: 「定位线索（非证据）」. */}
          <span className="font-medium">{t("reportCenter.reader_location_hints")}</span>
          {hints.map((hint) => (
            <span key={hint} className="ml-1">
              {hint}
            </span>
          ))}
        </p>
      )}
      {showPlan && plan && (
        <p data-testid={`report-section-research-${ord}`}>
          <Radar className="mr-1 inline size-3" />
          <span className="font-medium">{t("reportCenter.reader_research_plan")}</span>
          {reportResearchPlanningLabel(t, plan.planning)}
          {plan.depth === "deep" && (
            <span className="ml-1 tabular-nums">
              {t("reportCenter.reader_research_merged", {
                merged: String(plan.merged),
                duplicates: String(plan.duplicates),
              })}
            </span>
          )}
        </p>
      )}
    </div>
  );
}
