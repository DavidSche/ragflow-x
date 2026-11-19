/**
 * Report section + evidence list (doc/288 §9.5, T10).
 *
 * Renders one report revision's sections with their execution status, and, under
 * a selected section, its evidence disclosures (supported / conflict / outdated /
 * unsupported) exactly as the job snapshot exposes them. The drawer is a
 * disclosure surface, not a second definition: it reads `ReportSectionSnapshot`
 * rows verbatim and never polls.
 */
import { useState } from "react";
import { useTranslate } from "ra-core";
import {
  BadgeCheck,
  CircleAlert,
  ChevronDown,
  Clock,
  FileText,
  ShieldAlert,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  reportEvidenceStatusLabel,
  reportQualityStatusLabel,
  reportSectionStatusLabel,
} from "../../report-center/report-center-api";
import { ReportSectionDisclosure } from "../../report-center/ReportSectionDisclosure";
import type { ReportSectionSnapshot } from "./report-run";

export interface SectionDrawerProps {
  sections: ReportSectionSnapshot[];
}

/**
 * Evidence tone per doc/288 §9.3, and it is deliberately not the same colour as the
 * execution status: `succeeded` + `conflicting` is a section that ran fine and whose
 * sources disagree, so a green execution badge next to a red evidence badge is the
 * honest picture. `outdated` and `conflicting` both mean "do not quote this as-is";
 * `partially_supported`/`unsupported` are amber because they are actionable in the
 * text, `not_required` carries no colour at all.
 */
function evidenceStatusClass(status: string): string {
  switch (status) {
    case "supported":
      return "text-emerald-600 dark:text-emerald-400";
    case "partially_supported":
    case "unsupported":
      return "text-amber-600 dark:text-amber-400";
    case "conflicting":
    case "outdated":
      return "text-destructive";
    default:
      return "text-muted-foreground";
  }
}

function SectionStatusBadge({ status }: { status: string }) {
  const t = useTranslate();
  const label = reportSectionStatusLabel(t, status);
  switch (status) {
    case "succeeded":
      return (
        <Badge variant="outline" className="text-emerald-600 dark:text-emerald-400">
          <BadgeCheck className="size-3" />
          {label}
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive">
          <CircleAlert className="size-3" />
          {label}
        </Badge>
      );
    case "writing":
    case "pending":
      return (
        <Badge variant="secondary">
          <Clock className="size-3" />
          {label}
        </Badge>
      );
    case "skipped":
      return (
        <Badge variant="outline">
          <ShieldAlert className="size-3" />
          {label}
        </Badge>
      );
    default:
      return <Badge variant="outline">{label}</Badge>;
  }
}

export function SectionDrawer({ sections }: SectionDrawerProps) {
  const t = useTranslate();
  const [openKey, setOpenKey] = useState<string | null>(null);

  if (sections.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("conversationCenter.report_sections_empty")}
      </p>
    );
  }

  return (
    <ul className="space-y-2" role="list" data-testid="section-drawer">
      {sections.map((section) => {
        const open = openKey === section.section_key;
        return (
          <li
            key={section.id ?? `${section.section_key}:${section.section_version}`}
            className="rounded-md border bg-card"
            data-testid={`section-${section.section_key}`}
          >
            <button
              type="button"
              aria-expanded={open}
              onClick={() => setOpenKey(open ? null : section.section_key)}
              className="flex w-full items-center gap-2 px-3 py-2 text-left"
              data-testid={`section-toggle-${section.section_key}`}
            >
              <FileText className="size-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">
                <span className="text-sm font-medium">{section.title}</span>
                <span className="ml-2 text-xs text-muted-foreground">
                  {section.ord}
                </span>
              </span>
              {section.requires_evidence && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span className="inline-flex">
                      <ShieldAlert className="size-3.5 text-muted-foreground" />
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>
                    {t("conversationCenter.report_section_requires_evidence")}
                  </TooltipContent>
                </Tooltip>
              )}
              <SectionStatusBadge status={section.status} />
              <ChevronDown
                className={`size-4 text-muted-foreground transition-transform ${open ? "rotate-180" : ""}`}
              />
            </button>
            {open && (
              <div className="border-t px-3 py-2" data-testid={`section-body-${section.section_key}`}>
                <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-4">
                  <div>
                    <dt className="text-muted-foreground">
                      {t("conversationCenter.report_section_evidence")}
                    </dt>
                    <dd
                      className={
                        section.requires_evidence
                          ? evidenceStatusClass(section.evidence_status)
                          : "text-muted-foreground"
                      }
                      data-testid={`section-evidence-${section.section_key}`}
                    >
                      {section.requires_evidence
                        ? reportEvidenceStatusLabel(t, section.evidence_status)
                        : t("conversationCenter.report_section_not_required")}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      {t("conversationCenter.report_section_quality")}
                    </dt>
                    <dd>
                      {reportQualityStatusLabel(t, section.quality_status)}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      {t("conversationCenter.report_section_citations")}
                    </dt>
                    <dd>{section.citation_count}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      {t("conversationCenter.report_section_attempt")}
                    </dt>
                    <dd>{section.attempt}</dd>
                  </div>
                  {/* R11's count rides on the section row. Only shown when there is
                      something to count: `claim_count = 0` on a row written before
                      the claim table existed is "not measured", and printing 0 would
                      read as "this section asserts nothing". */}
                  {section.claim_count > 0 && (
                    <div>
                      <dt className="text-muted-foreground">
                        {t("conversationCenter.report_section_claims")}
                      </dt>
                      <dd data-testid={`section-claims-${section.section_key}`}>
                        {section.claim_count}
                      </dd>
                    </div>
                  )}
                </dl>
                {/* The "no evidence" line is a claim about the run, so it may only
                    be made when the count says so. It used to key off a per-section
                    evidence list the snapshot never carries, which made a section
                    with 8 citations and `evidence_status=supported` say
                    "本节未采集到证据。" (live 2026-11-18). */}
                {section.requires_evidence && section.citation_count === 0 && (
                  <p className="mt-2 text-xs text-muted-foreground">
                    {t("conversationCenter.report_section_no_evidence")}
                  </p>
                )}
                {/* R17 locators, worded and labelled by the one component the reader
                    uses: a compiled page index helps the reviewer find the source
                    page, and must never be counted as evidence for this section. */}
                <ReportSectionDisclosure section={section} />
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}
