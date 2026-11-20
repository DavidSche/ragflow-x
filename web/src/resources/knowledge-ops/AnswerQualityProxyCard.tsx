import { useCallback, useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "../../lib/api";

// Q&A proxy metrics (doc/301 §5.9 QA-3 / T20).
//
// Two rules from the plan are enforced by the markup rather than by a comment:
//
//   - **A rate never renders without its denominator.** Every cell prints the ratio and,
//     underneath, `n/m`. A reader who distrusts the percentage can redo the division.
//   - **`null` is 暂无判定, never 0%.** `formatRate` answers the dictionary word when the
//     rate is null/undefined/NaN, because "no denominator" and "measured zero" are
//     different facts and only one of them is good news.
//
// The caveat line under the title is not decoration: the plan requires the page to state
// that these are online proxies with selection bias, so the coverage number is rendered
// from the payload's own `bias` block instead of being hard-coded in the UI.

interface Envelope<T> {
  code: number;
  data: T;
}

interface QualityBucket {
  week_start?: string;
  week_end?: string;
  turns: number;
  answered: number;
  abstained: number;
  citations_missing: number;
  feedback_answered: number;
  negative_answered: number;
  abstain_rate: number | null;
  citation_missing_rate: number | null;
  false_answer_proxy: number | null;
  feedback_coverage: number | null;
  similarity_samples: number;
  similarity_p50: number | null;
  similarity_p90: number | null;
}

interface QualityBias {
  methodology: string;
  week_basis: string;
  feedback_coverage: number | null;
  unlabelled_answered: number;
  window_weeks: number;
  weeks_clamped: boolean;
}

export interface AnswerQualityPayload {
  window: { from: string; to: string };
  weeks: QualityBucket[];
  totals: QualityBucket;
  bias: QualityBias;
}

/**
 * Shape check before the payload reaches the render.
 *
 * A card that trusts any 200 body blanks the whole board when the response shape moves:
 * React throws during render, and the page loses its other panels too. Treating an
 * unexpected shape as a failed load keeps the failure local and legible.
 */
export function isAnswerQualityPayload(value: unknown): value is AnswerQualityPayload {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<AnswerQualityPayload>;
  return (
    Array.isArray(candidate.weeks) &&
    typeof candidate.totals === "object" &&
    candidate.totals !== null &&
    typeof candidate.bias === "object" &&
    candidate.bias !== null
  );
}

/** Exported for the test: a `null` rate is a word, not a number. */
export function formatRate(
  value: number | null | undefined,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return t("knowledgeOps.quality_ratio_unavailable");
  }
  return `${(value * 100).toFixed(1)}%`;
}

/** The denominator hint under a rate: `n/m`, so the reader can recompute it. */
function denominator(
  numerator: number,
  total: number,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  return t("knowledgeOps.quality_denominator", { numerator, denominator: total });
}

function weekLabel(bucket: QualityBucket): string {
  if (!bucket.week_start) return "";
  return bucket.week_start.slice(0, 10);
}

/** One metric cell: rate on top, denominator underneath. */
function RateCell({
  rate,
  hint,
  label,
  unavailableLabel,
  t,
}: {
  rate: string;
  hint: string;
  label: string;
  unavailableLabel: string;
  t: (key: string, options?: Record<string, unknown>) => string;
}) {
  const unavailable = rate === unavailableLabel;
  return (
    <td className="px-3 py-2 align-top" data-testid={`quality-cell-${label}`}>
      <div className={unavailable ? "text-sm text-muted-foreground" : "text-sm font-medium"}>
        {rate}
      </div>
      <div className="text-xs text-muted-foreground">{hint}</div>
    </td>
  );
}

export function AnswerQualityProxyCard({ weeks = 8 }: { weeks?: number }) {
  const t = useTranslate();
  const [payload, setPayload] = useState<AnswerQualityPayload | null>(null);
  const [failed, setFailed] = useState(false);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.get<Envelope<AnswerQualityPayload>>(
        `/knowledge-ops/answer-quality?weeks=${weeks}`,
      );
      const data: unknown = res?.data?.data;
      if (!isAnswerQualityPayload(data)) {
        setPayload(null);
        setFailed(true);
        return;
      }
      setPayload(data);
      setFailed(false);
    } catch {
      // A failed load must not leave the previous numbers on screen: stale proxy metrics
      // are indistinguishable from current ones, and the board is used to decide staffing.
      setPayload(null);
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, [weeks]);

  useEffect(() => {
    void load();
  }, [load]);

  const unavailable = t("knowledgeOps.quality_ratio_unavailable");
  const totals = payload?.totals;

  return (
    <Card data-testid="answer-quality-proxy">
      <CardHeader>
        <CardTitle>{t("knowledgeOps.quality_title")}</CardTitle>
        <p className="text-xs text-muted-foreground">{t("knowledgeOps.quality_subtitle")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading ? (
          <Skeleton className="h-24 w-full" />
        ) : failed || !payload ? (
          <p className="text-sm text-muted-foreground">{t("knowledgeOps.quality_load_failed")}</p>
        ) : payload.weeks.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("knowledgeOps.quality_empty")}</p>
        ) : (
          <>
            <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
              <span>{t(`knowledgeOps.quality_methodology_${payload.bias.methodology}`)}</span>
              <span>{t(`knowledgeOps.quality_week_basis_${payload.bias.week_basis}`)}</span>
              <span data-testid="quality-bias-coverage">
                {payload.bias.feedback_coverage === null ||
                payload.bias.feedback_coverage === undefined
                  ? t("knowledgeOps.quality_bias_coverage_unavailable")
                  : t("knowledgeOps.quality_bias_coverage", {
                      rate: formatRate(payload.bias.feedback_coverage, t),
                      count: payload.bias.unlabelled_answered,
                    })}
              </span>
              {payload.bias.weeks_clamped ? (
                <span>{t("knowledgeOps.quality_clamped", { weeks: payload.bias.window_weeks })}</span>
              ) : null}
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs text-muted-foreground">
                    <th className="px-3 py-2">{t("knowledgeOps.quality_week")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.quality_abstain_rate")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.quality_citation_missing_rate")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.quality_false_answer_proxy")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.quality_feedback_coverage")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.quality_similarity_p50")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.quality_similarity_p90")}</th>
                  </tr>
                </thead>
                <tbody>
                  {payload.weeks.map((week) => (
                    <tr key={week.week_start} className="border-t">
                      <td className="px-3 py-2 align-top text-xs">{weekLabel(week)}</td>
                      <RateCell
                        label="abstain_rate"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(week.abstain_rate, t)}
                        hint={denominator(week.abstained, week.turns, t)}
                      />
                      <RateCell
                        label="citation_missing_rate"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(week.citation_missing_rate, t)}
                        hint={denominator(week.citations_missing, week.answered, t)}
                      />
                      <RateCell
                        label="false_answer_proxy"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(week.false_answer_proxy, t)}
                        hint={denominator(week.negative_answered, week.feedback_answered, t)}
                      />
                      <RateCell
                        label="feedback_coverage"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(week.feedback_coverage, t)}
                        hint={denominator(week.feedback_answered, week.answered, t)}
                      />
                      <td className="px-3 py-2 align-top" data-testid="quality-cell-similarity_p50">
                        <div className="text-sm font-medium">
                          {week.similarity_p50 === null || week.similarity_p50 === undefined
                            ? unavailable
                            : week.similarity_p50.toFixed(3)}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          {t("knowledgeOps.quality_similarity_hint", {
                            count: week.similarity_samples,
                          })}
                        </div>
                      </td>
                      <td className="px-3 py-2 align-top" data-testid="quality-cell-similarity_p90">
                        <div className="text-sm font-medium">
                          {week.similarity_p90 === null || week.similarity_p90 === undefined
                            ? unavailable
                            : week.similarity_p90.toFixed(3)}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          {t("knowledgeOps.quality_similarity_hint", {
                            count: week.similarity_samples,
                          })}
                        </div>
                      </td>
                    </tr>
                  ))}
                  {totals ? (
                    <tr className="border-t bg-muted/40">
                      <td className="px-3 py-2 align-top text-xs font-medium">
                        {t("knowledgeOps.quality_week_total")}
                      </td>
                      <RateCell
                        label="total_abstain_rate"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(totals.abstain_rate, t)}
                        hint={denominator(totals.abstained, totals.turns, t)}
                      />
                      <RateCell
                        label="total_citation_missing_rate"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(totals.citation_missing_rate, t)}
                        hint={denominator(totals.citations_missing, totals.answered, t)}
                      />
                      <RateCell
                        label="total_false_answer_proxy"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(totals.false_answer_proxy, t)}
                        hint={denominator(totals.negative_answered, totals.feedback_answered, t)}
                      />
                      <RateCell
                        label="total_feedback_coverage"
                        t={t}
                        unavailableLabel={unavailable}
                        rate={formatRate(totals.feedback_coverage, t)}
                        hint={denominator(totals.feedback_answered, totals.answered, t)}
                      />
                      <td className="px-3 py-2 align-top" data-testid="quality-cell-total_similarity">
                        <div className="text-sm font-medium">
                          {totals.similarity_p50 === null || totals.similarity_p50 === undefined
                            ? unavailable
                            : totals.similarity_p50.toFixed(3)}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          {t("knowledgeOps.quality_similarity_hint", {
                            count: totals.similarity_samples,
                          })}
                        </div>
                      </td>
                      <td className="px-3 py-2 align-top">
                        <div className="text-sm font-medium">
                          {totals.similarity_p90 === null || totals.similarity_p90 === undefined
                            ? unavailable
                            : totals.similarity_p90.toFixed(3)}
                        </div>
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
