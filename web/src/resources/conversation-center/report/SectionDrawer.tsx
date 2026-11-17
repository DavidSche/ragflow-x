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
  TriangleAlert,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { ReportSectionSnapshot } from "./report-run";

export interface SectionEvidenceEntry {
  section_key: string;
  /** disclosure status: supported / partially_supported / conflicting / outdated / unsupported */
  status: string;
  claims?: string[];
  citations?: Array<{ id: string; snippet?: string }>;
  reason?: string;
}

export interface SectionDrawerProps {
  sections: ReportSectionSnapshot[];
  /** evidence disclosures, keyed by section key (job snapshot `evidence` surface). */
  evidenceBySection?: Record<string, SectionEvidenceEntry[]>;
}

function SectionStatusBadge({ status }: { status: string }) {
  const t = useTranslate();
  const label = t(`conversationCenter.report_section_${status}`, {
    _: status,
  });
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

export function SectionDrawer({
  sections,
  evidenceBySection = {},
}: SectionDrawerProps) {
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
        const evidence = evidenceBySection[section.section_key] ?? [];
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
                  {section.ord + 1}
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
                    <dd>
                      {section.requires_evidence
                        ? t(
                            `conversationCenter.report_evidence_status_${section.evidence_status}`,
                            { _: section.evidence_status },
                          )
                        : t("conversationCenter.report_section_not_required")}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      {t("conversationCenter.report_section_quality")}
                    </dt>
                    <dd>
                      {t(
                        `conversationCenter.report_quality_status_${section.quality_status}`,
                        { _: section.quality_status },
                      )}
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
                </dl>
                {evidence.length > 0 && (
                  <div
                    className="mt-2 space-y-1"
                    data-testid={`section-evidence-${section.section_key}`}
                  >
                    {evidence.map((entry, index) => (
                      <div
                        key={`${section.section_key}:${index}`}
                        className="flex items-start gap-2 rounded border bg-muted/40 px-2 py-1.5 text-xs"
                      >
                        {entry.status === "conflicting" ||
                        entry.status === "outdated" ||
                        entry.status === "unsupported" ? (
                          <TriangleAlert className="mt-0.5 size-3 shrink-0 text-amber-500" />
                        ) : (
                          <BadgeCheck className="mt-0.5 size-3 shrink-0 text-emerald-600" />
                        )}
                        <div className="min-w-0">
                          <p className="text-muted-foreground">
                            {t(
                              `conversationCenter.report_evidence_status_${entry.status}`,
                              { _: entry.status },
                            )}
                          </p>
                          {entry.reason && <p>{entry.reason}</p>}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
                {evidence.length === 0 && section.requires_evidence && (
                  <p className="mt-2 text-xs text-muted-foreground">
                    {t("conversationCenter.report_section_no_evidence")}
                  </p>
                )}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}
