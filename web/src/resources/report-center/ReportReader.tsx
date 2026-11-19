/**
 * Full reading view for one report revision (doc/288 §7.10 / §9.5, doc/44 「好用」 gap #4).
 *
 * Why this exists: the centre could list versions and download files, but it could
 * not show the document. Reading a report meant leaving the product to open a DOCX,
 * which is also why "jump to section" had nothing to jump to. This renders the stored
 * bodies with a table of contents that scrolls, keeps the quality/evidence status of
 * each section visible, and never invents a section that was not delivered.
 *
 * Deliberate rules:
 *  - a section with an empty body renders its *reason* (skipped / failed / gap), not
 *    a blank heading — an empty section is a disclosure, not a rendering bug;
 *  - an unjudged quality status prints the server's word, never "passed";
 *  - the reader is read-only; exporting stays on the detail drawer so there is one
 *    place that mints artifacts.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslate } from "ra-core";
import { FileText, Loader2, ListTree, TriangleAlert } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { api, ApiError, unwrap } from "../../lib/api";
import { ReportSectionBody } from "./ReportSectionBody";
import { ReportSectionDisclosure } from "./ReportSectionDisclosure";
import {
  reportClaimSupportLabel,
  reportClaimTypeLabel,
  reportEvidenceStatusLabel,
  reportGapReasonLabel,
  reportQualityStatusLabel,
  reportSectionStatusLabel,
  type ReportClaimView,
  type ReportDetailRecord,
  type ReportRow,
} from "./report-center-api";

/** One section as `GET /reports/:id/revisions/:revision/sections` answers it. */
export interface ReportSectionView {
  id: string;
  ord: number;
  section_key: string;
  title: string;
  section_version: number;
  status: string;
  quality_status: string;
  evidence_status: string;
  requires_evidence: boolean;
  citation_count: number;
  /** `reuse` when the body was carried over from an earlier revision (R14). */
  rewrite_reason?: string;
  /** R11 claim list; absent on revisions written before the claim table existed. */
  claims?: ReportClaimView[];
  content_md: string;
  gap_reason: string;
  /** R17 locators as display lines; never evidence (doc/295 §2.1). */
  location_hints?: string[];
  /** R18 disclosure; absent for a standard-depth section and for pre-P2 revisions. */
  research_plan?: { depth: string; planning: string; merged: number; duplicates: number };
}

/**
 * Claim-supporter tones, fixed by doc/288 §9.3: green only when every number and
 * date of the sentence was found in the cited chunks; amber for "not checked out";
 * red for the two states where the evidence speaks against the sentence.
 * `not_required` gets no badge — an opinion is not an unverified fact.
 */
function claimSupportClass(support: string): string {
  switch (support) {
    case "supported":
      return "border-emerald-300 text-emerald-700 dark:border-emerald-800 dark:text-emerald-300";
    case "partially_supported":
    case "unsupported":
      return "border-amber-300 text-amber-700 dark:border-amber-800 dark:text-amber-300";
    case "conflicting":
    case "outdated":
      return "border-destructive/40 text-destructive";
    default:
      return "border-muted text-muted-foreground";
  }
}

/**
 * The claim list of one section (R11, doc/288 §9.2).
 *
 * Every claim is printed, uncited ones included: the whole reason this table exists
 * is that a reader should be able to see which sentences the report could not tie to
 * a source. The preamble is not decoration either — `可核实` is a rule check on the
 * numbers, and without saying so the list would read as a truth certificate.
 */
function SectionClaims({ claims }: { claims: ReportClaimView[] }) {
  const t = useTranslate();
  if (claims.length === 0) return null;
  return (
    <details className="mt-2 rounded border bg-muted/30 px-2 py-1" data-testid="report-reader-claims">
      <summary className="cursor-pointer text-xs font-medium text-muted-foreground">
        {t("reportCenter.reader_claims_title")} · {claims.length}
      </summary>
      <p className="mt-1 text-[11px] text-muted-foreground">
        {t("reportCenter.reader_claims_preamble")}
      </p>
      <ul className="mt-1 space-y-1">
        {claims.map((claim) => {
          const badge = claim.support_status !== "not_required";
          return (
            <li
              key={claim.ord}
              className="flex flex-wrap items-baseline gap-1 text-xs"
              data-testid={`report-reader-claim-${claim.ord}`}
            >
              {badge && (
                <span
                  className={`rounded border px-1 text-[10px] ${claimSupportClass(claim.support_status)}`}
                  data-testid={`report-reader-claim-support-${claim.ord}`}
                >
                  {reportClaimSupportLabel(t, claim.support_status)}
                </span>
              )}
              <span className="text-muted-foreground">
                {reportClaimTypeLabel(t, claim.claim_type)}
              </span>
              <span className="min-w-0 flex-1">{claim.claim_text}</span>
              {claim.markers.length === 0 && claim.support_status !== "not_required" && (
                <span className="text-[10px] text-muted-foreground">
                  {t("reportCenter.reader_claims_uncited")}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </details>
  );
}

export interface ReportReaderProps {
  report: ReportRow | null;
  /**
   * Which revision to read. `null`/`0` means "whatever is live", which is resolved
   * from the server's `current_revision_id` — never from "the highest number",
   * the defect class §7.10 exists to prevent (a running regenerate is newest and
   * is not what the report shows).
   */
  revision?: number | null;
  onOpenChange: (open: boolean) => void;
}

export function ReportReader({ report, revision, onOpenChange }: ReportReaderProps) {
  const t = useTranslate();
  const [sections, setSections] = useState<ReportSectionView[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [resolvedRevision, setResolvedRevision] = useState(0);
  const bodyRef = useRef<HTMLDivElement | null>(null);

  const load = useCallback(async (id: string, revisionNo: number) => {
    setLoading(true);
    setError(null);
    try {
      let target = revisionNo;
      if (target <= 0) {
        const detail = await unwrap<ReportDetailRecord>(
          api.get(`/reports/${encodeURIComponent(id)}`),
        );
        target = detail.revision?.revision ?? 0;
      }
      if (target <= 0) {
        // Nothing published and nothing readable: say so instead of rendering an
        // empty document that looks like a report with no sections.
        setSections([]);
        setResolvedRevision(0);
        return;
      }
      const payload = await unwrap<{ sections: ReportSectionView[] }>(
        api.get(`/reports/${encodeURIComponent(id)}/revisions/${target}/sections`),
      );
      setSections(payload.sections ?? []);
      setResolvedRevision(target);
    } catch (err) {
      setError(err);
      setSections([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (report) void load(report.id, revision ?? 0);
    else {
      setSections([]);
      setResolvedRevision(0);
    }
  }, [report, revision, load]);

  const scrollTo = useCallback((sectionKey: string) => {
    const target = bodyRef.current?.querySelector<HTMLElement>(
      `[data-section-key="${CSS.escape(sectionKey)}"]`,
    );
    target?.scrollIntoView({ behavior: "smooth", block: "start" });
    // Move focus too, so the jump is not only visual: keyboard and screen-reader
    // users land where the pointer user does.
    target?.focus();
  }, []);

  const heading = useMemo(() => {
    if (!report) return t("reportCenter.reader_title");
    return resolvedRevision > 0 ? `${report.title} · v${resolvedRevision}` : report.title;
  }, [report, resolvedRevision, t]);

  return (
    <Dialog open={report !== null} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-5xl" data-testid="report-reader">
        <DialogHeader>
          <DialogTitle>{heading}</DialogTitle>
        </DialogHeader>

        {loading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> {t("reportCenter.loading")}
          </div>
        )}

        {error !== null && (
          <div role="alert" className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
            {error instanceof ApiError ? error.displayMessage : t("reportCenter.reader_fail")}
          </div>
        )}

        {!loading && error === null && report !== null && (
          <div className="flex max-h-[72vh] gap-4 overflow-hidden text-sm">
            <nav
              aria-label={t("reportCenter.reader_toc")}
              className="w-48 shrink-0 overflow-y-auto border-r pr-2"
              data-testid="report-reader-toc"
            >
              <h3 className="mb-1 flex items-center gap-1 text-xs font-semibold text-muted-foreground">
                <ListTree className="size-3.5" />
                {t("reportCenter.reader_toc")}
              </h3>
              {sections.length === 0 ? (
                <p className="text-xs text-muted-foreground">{t("reportCenter.reader_empty")}</p>
              ) : (
                <ol className="space-y-0.5">
                  {sections.map((section) => (
                    <li key={section.id}>
                      <button
                        type="button"
                        className="w-full truncate rounded px-1 py-0.5 text-left hover:bg-muted"
                        onClick={() => scrollTo(section.section_key)}
                        data-testid={`report-reader-toc-${section.ord}`}
                      >
                        {section.ord}. {section.title || section.section_key}
                      </button>
                    </li>
                  ))}
                </ol>
              )}
            </nav>

            <div ref={bodyRef} className="flex-1 overflow-y-auto pr-1" data-testid="report-reader-body">
              {sections.map((section) => (
                <section
                  key={section.id}
                  tabIndex={-1}
                  data-section-key={section.section_key}
                  className="mb-5 scroll-mt-2 outline-none"
                >
                  <h2 className="mb-1 flex flex-wrap items-baseline gap-2 text-base font-semibold">
                    <span>
                      {section.ord}. {section.title || section.section_key}
                    </span>
                    <span className="rounded bg-muted px-1 text-[10px] font-normal text-muted-foreground">
                      {reportSectionStatusLabel(t, section.status)} ·{" "}
                      {reportQualityStatusLabel(t, section.quality_status)} ·{" "}
                      {reportEvidenceStatusLabel(t, section.evidence_status)}
                    </span>
                    {/* R14: a paragraph carried over from an earlier revision is
                        still this revision's body, but the reader deserves to know
                        it was not written against this run's evidence. */}
                    {section.rewrite_reason === "reuse" && (
                      <span
                        className="rounded border px-1 text-[10px] font-normal text-muted-foreground"
                        data-testid={`report-reader-reused-${section.ord}`}
                      >
                        {t("reportCenter.reader_reused_body")}
                      </span>
                    )}
                  </h2>
                  {section.content_md ? (
                    // `content_md` is Markdown, not plain text: rendering it as a
                    // pre-wrap string printed the markup itself (pipes, `**`, `---`).
                    <ReportSectionBody
                      content={section.content_md}
                      testId={`report-reader-body-${section.ord}`}
                    />
                  ) : (
                    // An empty section says why: gap_reason when the server gave one,
                    // otherwise the honest "this section produced no text".
                    <p className="flex items-start gap-1 text-muted-foreground">
                      <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                      <span data-testid={`report-reader-gap-${section.ord}`}>
                        {section.gap_reason
                          ? t("reportCenter.reader_section_gap", {
                              reason: reportGapReasonLabel(t, section.gap_reason),
                            })
                          : t("reportCenter.reader_section_empty")}
                      </span>
                    </p>
                  )}
                  {section.citation_count > 0 && (
                    <p className="mt-1 text-xs text-muted-foreground">
                      <FileText className="mr-1 inline size-3" />
                      {t("reportCenter.reader_citations", { count: section.citation_count })}
                    </p>
                  )}
                  {/* R17 / R18 disclosures sit after the body and before the claim
                      table: they explain how the section was found and researched, and
                      a locator must never be adjacent to the citation count as if it
                      were one of them. */}
                  <ReportSectionDisclosure section={section} />
                  <SectionClaims claims={section.claims ?? []} />
                </section>
              ))}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
