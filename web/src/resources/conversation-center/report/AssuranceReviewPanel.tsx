/**
 * 证据复核工作台 (doc/301 T08/T09/T12) — the panel that shows what a review run
 * actually established, and where the reviewer decides what to do about it.
 *
 * Layout is three columns, and the order is the argument:
 *   左 = 结论 (eligibility + basis + counters)   中 = 逐条论断   右 = 待裁决问题
 * A reader who only looks at the left column must still learn *why*; that is why the
 * basis list is rendered under the tier rather than behind a tooltip.
 *
 * Three honesty rules are enforced here rather than trusted to the server's prose:
 *   - a null rate prints the dictionary word (`formatRate`), never 0% and not a dash;
 *   - `quote_status=ambiguous` prints "无法唯一定位" and offers no highlight;
 *   - `pending` says "尚未完成复核", never "不通过".
 *
 * A fourth rule arrived with the contrast panel (doc/301 §9 / §5.3.1): the surface must
 * say *what was compared against*. `scan_scope=cited_only` is printed as "no
 * cross-document contrast happened", because "no conflict found" and "not compared"
 * are different sentences and only one is true. The candidate excerpts themselves are
 * owner-only — a shared grantee sees the count, never the text.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslate } from "ra-core";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronRight,
  CircleDashed,
  Loader2,
  Quote,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ApiError } from "../../../lib/api";
import {
  ASSURANCE_POLICIES,
  canHighlightQuote,
  coverageCaveat,
  describeEligibility,
  describeQuoteStatus,
  findingCategoryLabel,
  findingStatusLabel,
  formatRate,
  severityLabel,
  verdictLabel,
  type AssuranceRun,
  type FindingStatus,
  type ReportReview,
  type ReviewCandidate,
  type ReviewClaim,
  type ReviewFinding,
  type ReviewSection,
} from "./report-assurance";

export interface AssuranceReviewPanelProps {
  /** Loads the projection; the parent owns the adapter so tests can inject one. */
  load: (options?: { runId?: string; sectionCursor?: string; findingCursor?: string }) => Promise<ReportReview>;
  /** Starts a run. Absent ⇒ the panel is read-only (a grantee, or assurance off). */
  trigger?: () => Promise<AssuranceRun>;
  /** Polls one run while it is queued/running. */
  poll?: (runId: string) => Promise<AssuranceRun>;
  /** Adjudicates a finding. Absent ⇒ the buttons are not rendered at all. */
  decide?: (
    findingId: string,
    body: { status: FindingStatus; note?: string; resolvedRevisionId?: string },
  ) => Promise<ReviewFinding>;
  /**
   * The revisions a `resolved` decision may point at: strictly later ones, as
   * `{ id, revision }` pairs. The id is what the API stores — the server resolves it
   * back to a revision number and refuses anything that is not later, so sending a
   * number here would be a value the server cannot check.
   */
  laterRevisions?: Array<{ id: string; revision: number }>;
  revisionIdToNumber?: Record<string, number>;
  onClose?: () => void;
}

/** Which of the three left-column icons a tier earns. */
function EligibilityIcon({ eligibility }: { eligibility: ReportReview["release_eligibility"] }) {
  switch (eligibility) {
    case "tier_1":
    case "tier_2":
      return <ShieldCheck className="h-5 w-5 text-emerald-600" />;
    case "tier_0":
      return <CircleDashed className="h-5 w-5 text-amber-600" />;
    case "blocked":
      return <ShieldAlert className="h-5 w-5 text-red-600" />;
    default:
      return <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />;
  }
}

const VERDICT_TONE: Record<string, string> = {
  supported: "bg-emerald-100 text-emerald-800",
  partially_supported: "bg-amber-100 text-amber-800",
  unsupported: "bg-red-100 text-red-800",
  conflicting: "bg-red-100 text-red-800",
  outdated: "bg-orange-100 text-orange-800",
  insufficient_evidence: "bg-slate-100 text-slate-700",
  abstained: "bg-slate-100 text-slate-600",
  not_evaluated: "bg-slate-50 text-slate-500",
  not_required: "bg-slate-50 text-slate-400",
};

const SEVERITY_TONE: Record<string, string> = {
  high: "bg-red-600 text-white",
  medium: "bg-amber-500 text-white",
  low: "bg-slate-400 text-white",
};

/** Counters row: the five numbers a reviewer actually argues about. */
function CounterRow({ review }: { review: ReportReview }) {
  const t = useTranslate();
  const c = review.counters;
  const caveat = coverageCaveat(t, c);
  return (
    <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs" data-testid="review-counters">
      <span className="text-muted-foreground">{t("conversationCenter.assurance.counter_coverage")}</span>
      <span className="font-mono" data-testid="counter-coverage">{formatRate(c.coverage, t)}</span>
      <span className="text-muted-foreground">{t("conversationCenter.assurance.counter_evaluated")}</span>
      <span className="font-mono">{c.evaluated} / {c.denominator}</span>
      <span className="text-muted-foreground">{t("conversationCenter.assurance.counter_supported")}</span>
      <span className="font-mono">{c.supported}</span>
      <span className="text-muted-foreground">{t("conversationCenter.assurance.counter_conflicting")}</span>
      <span className="font-mono">{c.conflicting}</span>
      <span className="text-muted-foreground">{t("conversationCenter.assurance.counter_not_evaluated")}</span>
      <span className="font-mono">{c.not_evaluated}</span>
      <span className="text-muted-foreground">{t("conversationCenter.assurance.counter_not_required")}</span>
      <span className="font-mono" data-testid="counter-not-required">{formatRate(c.not_required_share, t)}</span>
      {caveat ? (
        <p className="col-span-2 mt-1 rounded bg-amber-50 p-1.5 text-[11px] leading-snug text-amber-900" data-testid="coverage-caveat">
          {caveat}
        </p>
      ) : null}
    </div>
  );
}

/**
 * 反证候选与复核范围 (doc/301 §9 / §5.3.1).
 *
 * Two facts, never folded together:
 *   - `scan_scope` — what the run compared against. Printed even when the frozen pool
 *     is empty, because "not compared" is the whole point;
 *   - the candidate excerpts — owner-only. A grantee reads the count and the sentence
 *     "另有 N 条未引用候选参与对照", which is what §9 requires: the shared report must
 *     disclose that the contrast used material the grantee cannot see.
 *
 * The list renders `document_label`/`pool_rank`/`content` only. It must not grow a
 * chunk id or a score: a candidate is not evidence, and a number next to it invites
 * exactly that reading.
 */
function ContrastScope({ review }: { review: ReportReview }) {
  const t = useTranslate();
  const citedOnly = review.scan_scope === "cited_only";
  const candidates = review.candidates ?? [];
  const hidden = !citedOnly && review.candidate_count > 0 && !review.candidates_visible;
  return (
    <div className="space-y-1 text-[10px] leading-snug" data-testid="review-contrast-scope">
      <p className="text-[11px] font-medium">{t("conversationCenter.assurance.candidates_title")}</p>
      <p className="text-muted-foreground" data-testid="review-scan-scope">
        {citedOnly
          ? t("conversationCenter.assurance.scan_scope_cited_only")
          : t("conversationCenter.assurance.scan_scope_with_candidates", { count: review.candidate_count })}
      </p>
      {hidden ? (
        <p className="rounded bg-slate-50 p-1.5" data-testid="review-candidates-hidden">
          {t("conversationCenter.assurance.candidates_hidden_for_grantees", { count: review.candidate_count })}
        </p>
      ) : null}
      {candidates.length > 0 ? (
        <ul className="space-y-1" data-testid="review-candidates">
          {candidates.map((candidate: ReviewCandidate, index: number) => (
            <li
              key={`${candidate.section_ord}-${candidate.pool_rank}-${index}`}
              className="rounded border border-dashed border-border p-1.5"
              data-testid={`review-candidate-${candidate.section_ord}-${candidate.pool_rank}`}
            >
              <p className="text-muted-foreground">
                {candidate.document_label
                  ? t("conversationCenter.assurance.candidates_meta_document", {
                      ord: candidate.section_ord,
                      document: candidate.document_label,
                      rank: candidate.pool_rank,
                    })
                  : t("conversationCenter.assurance.candidates_meta", {
                      ord: candidate.section_ord,
                      rank: candidate.pool_rank,
                    })}
              </p>
              {candidate.content ? (
                <p className="whitespace-pre-wrap break-words">
                  {candidate.content}
                  {candidate.truncated
                    ? t("conversationCenter.assurance.candidate_excerpt_truncated")
                    : ""}
                </p>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
      {review.candidates_truncated ? (
        <p className="text-muted-foreground" data-testid="review-candidates-truncated">
          {t("conversationCenter.assurance.candidates_truncated", { limit: candidates.length })}
        </p>
      ) : null}
    </div>
  );
}

function ClaimRow({ claim }: { claim: ReviewClaim }) {
  const t = useTranslate();
  const tone = VERDICT_TONE[claim.support_verdict] ?? "bg-slate-100 text-slate-600";
  const label = verdictLabel(t, claim.support_verdict);
  const highlight = canHighlightQuote(claim);
  return (
    <li className="border-b border-border/60 px-3 py-2 last:border-0" data-testid={`claim-${claim.id}`}>
      <div className="flex items-start gap-2">
        <Badge className={`shrink-0 px-1.5 py-0 text-[10px] ${tone}`}>{label}</Badge>
        <p className="text-xs leading-relaxed">{claim.claim_text}</p>
      </div>
      {claim.quote_text ? (
        <div className="mt-1 flex items-start gap-1 pl-1 text-[11px] text-muted-foreground">
          <Quote className="mt-0.5 h-3 w-3 shrink-0" />
          <span className={highlight ? "" : "line-through decoration-slate-300"}>
            {claim.quote_text}
          </span>
        </div>
      ) : null}
      {/* The quote verdict is printed even when there is no quote: "未提供引文" is the
          finding, and hiding it would make an unsourced claim look merely unscored. */}
      <p className="mt-0.5 pl-1 text-[10px] text-muted-foreground" data-testid={`claim-quote-${claim.id}`}>
        {describeQuoteStatus(t, claim.quote_status)}
        {claim.cache_hit ? ` · ${t("conversationCenter.assurance.cache_hit")}` : ""}
      </p>
      {claim.rationale ? (
        <p className="mt-1 pl-1 text-[11px] italic text-muted-foreground">{claim.rationale}</p>
      ) : null}
    </li>
  );
}

function SectionBlock({ section }: { section: ReviewSection }) {
  const t = useTranslate();
  const [open, setOpen] = useState(section.claims.length > 0);
  return (
    <div className="border-b border-border last:border-0">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-medium hover:bg-muted/40"
        onClick={() => setOpen((v) => !v)}
      >
        <ChevronRight className={`h-3 w-3 transition-transform ${open ? "rotate-90" : ""}`} />
        <span className="flex-1">{section.ord}. {section.title}</span>
        {section.requires_evidence ? (
          <Badge variant="outline" className="px-1 py-0 text-[10px]">
            {t("conversationCenter.assurance.requires_evidence")}
          </Badge>
        ) : null}
        <span className="text-[10px] text-muted-foreground">
          {t("conversationCenter.assurance.claims_count", { count: section.claims.length })}
        </span>
      </button>
      {/* §5.8 items 1–2: what the writer had, shown where the reviewer already is.
          Every value is omitted rather than defaulted — "the pool had 0 chunks" and
          "nobody recorded the pool" are different statements, and only the first one
          is about the corpus. */}
      <SectionContext section={section} />
      {open ? (
        <ul className="bg-muted/10">
          {section.claims.map((claim) => <ClaimRow key={claim.id} claim={claim} />)}
        </ul>
      ) : null}
    </div>
  );
}

function SectionContext({ section }: { section: ReviewSection }) {
  const t = useTranslate();
  const pool = section.pool_hit_chunks;
  const uncited = section.pool_candidate_count;
  const degraded = section.research_planning?.startsWith("fallback_") ?? false;
  if (pool === undefined && uncited === undefined && !section.research_planning) return null;
  return (
    <p
      className="flex flex-wrap items-center gap-x-2 gap-y-1 px-8 pb-2 text-[10px] text-muted-foreground"
      data-testid={`review-section-context-${section.ord}`}
    >
      {pool !== undefined ? (
        <span>
          {t("conversationCenter.assurance.pool_hits", { count: pool })}
          {section.pool_insufficient === true
            ? ` ${t("conversationCenter.assurance.pool_insufficient")}`
            : null}
        </span>
      ) : null}
      {uncited !== undefined ? (
        <span>{t("conversationCenter.assurance.pool_uncited", { count: uncited })}</span>
      ) : null}
      {degraded ? (
        <span className="text-amber-600" data-testid={`review-section-degraded-${section.ord}`}>
          {/* The dictionary word, never the raw `fallback_budget` token: the closed set
              is the UI's vocabulary (`reportCenter.label_research_planning`), and a
              machine word in a reviewer's face is the same bug as a hardcoded string. */}
          {t("conversationCenter.assurance.research_degraded", {
            state: t(`reportCenter.label_research_planning.${section.research_planning}`, {
              _: section.research_planning ?? "",
            }),
          })}
        </span>
      ) : null}
    </p>
  );
}

function FindingCard({
  finding,
  decide,
  laterRevisions,
  revisionIdToNumber,
  onDone,
}: {
  finding: ReviewFinding;
  decide?: AssuranceReviewPanelProps["decide"];
  laterRevisions: Array<{ id: string; revision: number }>;
  revisionIdToNumber: Record<string, number>;
  onDone: () => void;
}) {
  const t = useTranslate();
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [resolvedRevision, setResolvedRevision] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const terminal = finding.status === "dismissed" || finding.status === "resolved";

  const submit = useCallback(async (status: FindingStatus) => {
    if (!decide) return;
    setBusy(true);
    setError(null);
    try {
      await decide(finding.id, {
        status,
        note: note.trim() || undefined,
        resolvedRevisionId: status === "resolved" ? resolvedRevision : undefined,
      });
      onDone();
    } catch (err) {
      // 44104 is the server refusing an illegal transition; surfacing the message is
      // the only honest option, because the local transition table can be stale (the
      // finding may have been decided in another tab).
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, [decide, finding.id, note, onDone, resolvedRevision]);

  return (
    <li className="border-b border-border/60 p-3 last:border-0" data-testid={`finding-${finding.id}`}>
      <div className="flex items-start gap-2">
        <Badge className={`shrink-0 px-1 py-0 text-[10px] ${SEVERITY_TONE[finding.severity] ?? ""}`}>
          {severityLabel(t, finding.severity)}
        </Badge>
        <div className="min-w-0 flex-1">
          <p className="text-xs font-medium">
            {finding.title || findingCategoryLabel(t, finding.category)}
          </p>
          <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{finding.detail}</p>
          {finding.suggested_action ? (
            <p className="mt-1 rounded bg-muted/60 px-1.5 py-1 text-[11px]">
              {t("conversationCenter.assurance.suggestion", { action: finding.suggested_action })}
            </p>
          ) : null}
          <p className="mt-1 text-[10px] text-muted-foreground">
            {t("conversationCenter.assurance.finding_meta", {
              section: finding.section_ord,
              category: findingCategoryLabel(t, finding.category),
              status: findingStatusLabel(t, finding.status),
            })}
            {finding.reviewed_by ? ` · ${finding.reviewed_by}` : ""}
          </p>
          {finding.status === "resolved" && finding.resolved_revision_id ? (
            <p className="text-[10px] text-muted-foreground">
              {t("conversationCenter.assurance.resolved_in_revision", {
                revision: revisionIdToNumber[finding.resolved_revision_id] ?? "?",
              })}
            </p>
          ) : null}
        </div>
      </div>

      {decide && !terminal ? (
        <div className="mt-2 space-y-1.5 pl-7">
          <input
            className="w-full rounded border border-border bg-background px-2 py-1 text-[11px]"
            placeholder={t("conversationCenter.assurance.note_placeholder")}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            maxLength={500}
          />
          {finding.status === "acknowledged" ? (
            <select
              className="w-full rounded border border-border bg-background px-2 py-1 text-[11px]"
              value={resolvedRevision}
              onChange={(e) => setResolvedRevision(e.target.value)}
            >
              <option value="">{t("conversationCenter.assurance.choose_revision")}</option>
              {laterRevisions.map((item) => (
                <option key={item.id} value={item.id}>v{item.revision}</option>
              ))}
            </select>
          ) : null}
          <div className="flex flex-wrap gap-1.5">
            {finding.status === "open" ? (
              <Button size="sm" variant="outline" className="h-6 px-2 text-[11px]"
                disabled={busy} onClick={() => void submit("acknowledged")}>
                {t("conversationCenter.assurance.action_acknowledge")}
              </Button>
            ) : null}
            {finding.status === "acknowledged" ? (
              <Button size="sm" variant="outline" className="h-6 px-2 text-[11px]"
                disabled={busy || resolvedRevision === ""} onClick={() => void submit("resolved")}>
                <CheckCircle2 className="mr-1 h-3 w-3" />
                {t("conversationCenter.assurance.action_resolve")}
              </Button>
            ) : null}
            {finding.status === "open" || finding.status === "acknowledged" ? (
              <Button size="sm" variant="ghost" className="h-6 px-2 text-[11px]"
                disabled={busy} onClick={() => void submit("dismissed")}>
                <XCircle className="mr-1 h-3 w-3" />
                {t("conversationCenter.assurance.action_dismiss")}
              </Button>
            ) : null}
            {busy ? <Loader2 className="h-3 w-3 animate-spin self-center" /> : null}
          </div>
          {error ? <p className="text-[11px] text-red-600">{error}</p> : null}
        </div>
      ) : null}
    </li>
  );
}

export function AssuranceReviewPanel({
  load,
  trigger,
  poll,
  decide,
  laterRevisions = [],
  revisionIdToNumber = {},
  onClose,
}: AssuranceReviewPanelProps) {
  const t = useTranslate();
  const [review, setReview] = useState<ReportReview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [triggering, setTriggering] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const refresh = useCallback(async (options?: { runId?: string }) => {
    setLoading(true);
    setError(null);
    try {
      setReview(await load(options));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [load]);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);

  // Poll only while the run is non-terminal, and always through `poll` (the single-run
  // read) — re-reading the whole projection every two seconds would re-page the
  // reviewer's scroll position out from under them.
  useEffect(() => {
    const run = review?.run;
    if (!run || !poll) return;
    if (run.status !== "queued" && run.status !== "running") return;
    timer.current = setTimeout(() => {
      void poll(run.id)
        .then(() => refresh({ runId: run.id }))
        .catch(() => { /* a failed poll is not a failed run; the next tick retries */ });
    }, 2000);
    return () => { if (timer.current) clearTimeout(timer.current); };
  }, [poll, refresh, review?.run]);

  const start = useCallback(async () => {
    if (!trigger) return;
    setTriggering(true);
    setError(null);
    try {
      const run = await trigger();
      await refresh({ runId: run.id });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setTriggering(false);
    }
  }, [refresh, trigger]);

  const sections = useMemo(() => review?.sections ?? [], [review]);
  const findings = useMemo(
    () => (review?.findings ?? []).filter((f) => f.status !== "resolved" && f.status !== "dismissed"),
    [review],
  );
  const decided = useMemo(
    () => (review?.findings ?? []).filter((f) => f.status === "resolved" || f.status === "dismissed"),
    [review],
  );

  if (loading && !review) {
    return (
      <div className="flex h-40 items-center justify-center text-xs text-muted-foreground">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        {t("conversationCenter.assurance.loading")}
      </div>
    );
  }

  if (error && !review) {
    return (
      <div className="rounded border border-red-200 bg-red-50 p-3 text-xs text-red-800" data-testid="review-error">
        {error}
      </div>
    );
  }

  if (!review) return null;

  const run = review.run;
  const running = run !== undefined && (run.status === "queued" || run.status === "running");

  return (
    <div className="flex h-full flex-col" data-testid="assurance-review-panel">
      <header className="flex items-center gap-2 border-b border-border px-3 py-2">
        <span className="text-xs font-semibold">{t("conversationCenter.assurance.title")}</span>
        <span className="text-[11px] text-muted-foreground">v{review.revision} · {review.revision_status}</span>
        <div className="flex-1" />
        {running ? (
          <Badge variant="outline" className="text-[10px]">
            {run?.status === "queued"
              ? t("conversationCenter.assurance.status_queued")
              : t("conversationCenter.assurance.status_running")}
          </Badge>
        ) : null}
        <Button size="sm" variant="ghost" className="h-6 px-2" onClick={() => void refresh()}
          disabled={loading} aria-label={t("conversationCenter.assurance.refresh")}>
          <RefreshCw className={`h-3 w-3 ${loading ? "animate-spin" : ""}`} />
        </Button>
        {trigger ? (
          <Button size="sm" variant="outline" className="h-6 px-2 text-[11px]" onClick={() => void start()}
            disabled={triggering || running}>
            {triggering ? <Loader2 className="mr-1 h-3 w-3 animate-spin" /> : null}
            {run
              ? t("conversationCenter.assurance.retrigger")
              : t("conversationCenter.assurance.trigger")}
          </Button>
        ) : null}
        {onClose ? (
          <Button size="sm" variant="ghost" className="h-6 px-2 text-[11px]" onClick={onClose}>
            {t("conversationCenter.assurance.close")}
          </Button>
        ) : null}
      </header>

      {error ? <p className="px-3 pt-2 text-[11px] text-red-600">{error}</p> : null}

      {/* Three columns at ≥1280px (the width this workbench is designed for), one
          column below `lg`: on a 1024px screen or a tablet the three panes would each
          be ~200px of clipped text, and a reviewer reading a truncated claim is worse
          off than one scrolling. The order is preserved when stacked — 结论 first —
          so the argument the layout makes survives the narrow breakpoint. */}
      <div
        className="grid flex-1 grid-cols-1 divide-y divide-border overflow-hidden lg:grid-cols-[minmax(180px,240px)_1fr_minmax(220px,320px)] lg:divide-x lg:divide-y-0"
        data-testid="review-body-grid"
      >
        {/* 左：结论与理由 */}
        <aside className="flex flex-col gap-2 overflow-y-auto p-3">
          <div className="flex items-center gap-2">
            <EligibilityIcon eligibility={review.release_eligibility} />
            <div>
              <p className="text-xs font-semibold" data-testid="eligibility-tier">{review.release_eligibility}</p>
              <p className="text-[10px] text-muted-foreground">
                {t("conversationCenter.assurance.policy")}{" "}
                {(ASSURANCE_POLICIES as readonly string[]).includes(review.requested_policy)
                  ? review.requested_policy : "—"}
              </p>
            </div>
          </div>
          <p className="text-[11px] leading-relaxed" data-testid="eligibility-reason">
            {describeEligibility(t, review.release_eligibility, review.release_eligibility_basis)}
          </p>
          {review.release_eligibility_basis.length > 0 ? (
            <ul className="space-y-0.5 text-[10px] text-muted-foreground" data-testid="eligibility-basis">
              {review.release_eligibility_basis.map((reason) => (
                <li key={reason}>· {reason}</li>
              ))}
            </ul>
          ) : null}
          <hr className="border-border" />
          <CounterRow review={review} />
          <hr className="border-border" />
          <ContrastScope review={review} />
          {review.acknowledged ? (
            <p className="rounded bg-emerald-50 p-1.5 text-[10px] text-emerald-900" data-testid="ack-flag">
              {t("conversationCenter.assurance.ack_acknowledged")}
            </p>
          ) : review.acknowledgeable ? (
            <p className="rounded bg-amber-50 p-1.5 text-[10px] text-amber-900" data-testid="ack-required">
              <AlertTriangle className="mr-1 inline h-3 w-3" />
              {t("conversationCenter.assurance.ack_required")}
            </p>
          ) : null}
          {review.truncated && review.truncated.length > 0 ? (
            <p className="text-[10px] text-muted-foreground" data-testid="truncated-note">
              {t("conversationCenter.assurance.truncated", { caps: review.truncated.join("、") })}
            </p>
          ) : null}
        </aside>

        {/* 中：逐条论断 */}
        <section className="overflow-y-auto" data-testid="review-sections">
          {sections.length === 0 ? (
            <p className="p-4 text-center text-xs text-muted-foreground">
              {run
                ? t("conversationCenter.assurance.claims_empty")
                : t("conversationCenter.assurance.claims_not_run")}
            </p>
          ) : (
            sections.map((section) => <SectionBlock key={`${section.section_key}-${section.ord}`} section={section} />)
          )}
        </section>

        {/* 右：待裁决问题 */}
        <aside className="flex flex-col overflow-y-auto" data-testid="review-findings">
          <p className="border-b border-border px-3 py-1.5 text-[11px] font-medium">
            {t("conversationCenter.assurance.findings_pending", { open: findings.length })}
            {decided.length > 0
              ? ` · ${t("conversationCenter.assurance.findings_decided", { decided: decided.length })}`
              : ""}
          </p>
          {findings.length === 0 ? (
            <p className="p-3 text-center text-[11px] text-muted-foreground">
              {t("conversationCenter.assurance.findings_empty")}
            </p>
          ) : (
            <ul>
              {findings.map((finding) => (
                <FindingCard
                  key={finding.id}
                  finding={finding}
                  decide={decide}
                  laterRevisions={laterRevisions}
                  revisionIdToNumber={revisionIdToNumber}
                  onDone={() => void refresh(run ? { runId: run.id } : undefined)}
                />
              ))}
            </ul>
          )}
        </aside>
      </div>
    </div>
  );
}
