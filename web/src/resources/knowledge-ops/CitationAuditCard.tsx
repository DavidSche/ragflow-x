import { useCallback, useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "../../lib/api";

// QA-2: the weekly chat citation sample (doc/301 §5.10 / T19).
//
// This card is the *reader* of the sample, and three plan rules are enforced by the
// markup rather than by a comment:
//
//   - **A rate never renders without its denominator.** The pass rate prints `n/m`
//     underneath; a week that judged nothing prints 暂无判定, never 0%.
//   - **`skipped` is not `passed`.** The three dispositions are separate columns, so
//     "we could not judge it" cannot be folded into good news by a reader in a hurry.
//   - **The budget line is a disclosure, not decoration.** A run that stopped early says
//     so and names its cap, because a truncated sample read as a full week would look
//     like a quality drop.
//
// It never shows an excerpt or an answer body: the finding rows carry ids and hashes,
// and the answer snapshot stays behind its own access rules.

interface Envelope<T> {
  code: number;
  data: T;
}

export interface CitationAuditRun {
  id: string;
  period_start: string;
  window_from: string;
  window_to: string;
  method_version: string;
  sample_target: number;
  sampled: number;
  judged: number;
  passed: number;
  failed: number;
  skipped: number;
  findings: number;
  tokens_in: number;
  tokens_out: number;
  budget_tokens: number;
  semantic: boolean;
  status: string;
  failure_reason?: string;
  started_at: string;
  finished_at?: string;
}

export interface CitationAuditFinding {
  id: string;
  request_id: string;
  citation_id?: string;
  document_id?: string;
  chunk_id?: string;
  verdict: string;
  method: string;
  score: number;
  reason: string;
  excerpt_hash_ok: boolean;
}

/**
 * Shape check before the payload reaches the render.
 *
 * One malformed 200 body must not blank the whole board: React throws during render and
 * the other panels go with it. Treating an unexpected shape as a failed load keeps the
 * failure local to this card.
 */
export function isCitationAuditRunsPayload(
  value: unknown,
): value is { runs: CitationAuditRun[] } {
  if (!value || typeof value !== "object") return false;
  return Array.isArray((value as { runs?: unknown }).runs);
}

export function isCitationAuditFindingsPayload(
  value: unknown,
): value is { findings: CitationAuditFinding[]; total: number } {
  if (!value || typeof value !== "object") return false;
  return Array.isArray((value as { findings?: unknown }).findings);
}

/** Exported for the test: `null` is a word, never 0%. */
export function formatAuditRate(
  numerator: number,
  denominator: number,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  if (!denominator) return t("knowledgeOps.audit_ratio_unavailable");
  return `${((numerator / denominator) * 100).toFixed(1)}%`;
}

function auditStatusLabel(
  status: string,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  return t(`knowledgeOps.audit_status_${status}`);
}

function auditVerdictLabel(
  verdict: string,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  return t(`knowledgeOps.audit_verdict_${verdict}`);
}

function auditReasonLabel(
  reason: string,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  return t(`knowledgeOps.audit_reason_${reason}`);
}

export function CitationAuditCard() {
  const t = useTranslate();
  const [runs, setRuns] = useState<CitationAuditRun[] | null>(null);
  const [findings, setFindings] = useState<CitationAuditFinding[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [loading, setLoading] = useState(true);
  const [triggering, setTriggering] = useState(false);
  const [triggerError, setTriggerError] = useState<string | null>(null);

  const loadFindings = useCallback(async (runID: string) => {
    try {
      const res = await api.get<Envelope<{ findings: CitationAuditFinding[]; total: number }>>(
        `/knowledge-ops/citation-audit/runs/${runID}/findings?page_size=20`,
      );
      const data: unknown = res?.data?.data;
      // Findings are a secondary read: a shape change here must not hide the counters.
      setFindings(isCitationAuditFindingsPayload(data) ? data.findings : []);
    } catch {
      setFindings([]);
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.get<Envelope<{ runs: CitationAuditRun[] }>>(
        "/knowledge-ops/citation-audit/runs?limit=8",
      );
      const data: unknown = res?.data?.data;
      if (!isCitationAuditRunsPayload(data)) {
        setRuns(null);
        setFailed(true);
        return;
      }
      setRuns(data.runs);
      setFailed(false);
      if (data.runs.length > 0) await loadFindings(data.runs[0].id);
      else setFindings([]);
    } catch {
      setRuns(null);
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, [loadFindings]);

  useEffect(() => {
    void load();
  }, [load]);

  const trigger = useCallback(async () => {
    setTriggering(true);
    setTriggerError(null);
    try {
      await api.post("/knowledge-ops/citation-audit", {});
      await load();
    } catch {
      setTriggerError(t("knowledgeOps.audit_trigger_failed"));
    } finally {
      setTriggering(false);
    }
  }, [load, t]);

  const unavailable = t("knowledgeOps.audit_ratio_unavailable");
  const latest = runs && runs.length > 0 ? runs[0] : null;

  return (
    <Card data-testid="citation-audit">
      <CardHeader>
        <CardTitle className="flex items-center justify-between gap-2 text-sm font-medium">
          <span>{t("knowledgeOps.audit_title")}</span>
          <Button
            size="sm"
            variant="outline"
            onClick={() => void trigger()}
            disabled={triggering}
            data-testid="citation-audit-trigger"
          >
            {triggering ? t("knowledgeOps.audit_triggering") : t("knowledgeOps.audit_trigger")}
          </Button>
        </CardTitle>
        <p className="text-xs text-muted-foreground">{t("knowledgeOps.audit_subtitle")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        {triggerError ? (
          <p className="text-sm text-destructive" data-testid="citation-audit-trigger-error">
            {triggerError}
          </p>
        ) : null}
        {loading ? (
          <Skeleton className="h-20 w-full" />
        ) : failed || !runs ? (
          <p className="text-sm text-muted-foreground">{t("knowledgeOps.audit_load_failed")}</p>
        ) : runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("knowledgeOps.audit_empty")}</p>
        ) : (
          <>
            {latest ? (
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                <span data-testid="citation-audit-period">{latest.period_start}</span>
                <span data-testid="citation-audit-status">
                  {auditStatusLabel(latest.status, t)}
                </span>
                <span data-testid="citation-audit-method">{latest.method_version}</span>
                <span data-testid="citation-audit-semantic">
                  {latest.semantic
                    ? t("knowledgeOps.audit_semantic_on", { tokens: latest.budget_tokens })
                    : t("knowledgeOps.audit_semantic_off")}
                </span>
                <span data-testid="citation-audit-sample">
                  {t("knowledgeOps.audit_sample", {
                    sampled: latest.sampled,
                    target: latest.sample_target,
                  })}
                </span>
              </div>
            ) : null}
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs text-muted-foreground">
                    <th className="px-3 py-2">{t("knowledgeOps.audit_period")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.audit_status_column")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.audit_pass_rate")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.audit_failed")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.audit_skipped")}</th>
                    <th className="px-3 py-2">{t("knowledgeOps.audit_tokens")}</th>
                  </tr>
                </thead>
                <tbody>
                  {runs.map((run) => (
                    <tr key={run.id} className="border-t">
                      <td className="px-3 py-2 align-top text-xs">{run.period_start}</td>
                      <td className="px-3 py-2 align-top text-xs">{auditStatusLabel(run.status, t)}</td>
                      <td className="px-3 py-2 align-top" data-testid={`audit-pass-${run.id}`}>
                        <div
                          className={
                            run.judged
                              ? "text-sm font-medium"
                              : "text-sm text-muted-foreground"
                          }
                        >
                          {formatAuditRate(run.passed, run.judged, t)}
                        </div>
                        <div className="text-xs text-muted-foreground">
                          {t("knowledgeOps.audit_denominator", {
                            numerator: run.passed,
                            denominator: run.judged,
                          })}
                        </div>
                      </td>
                      <td className="px-3 py-2 align-top text-sm">{run.failed}</td>
                      <td className="px-3 py-2 align-top text-sm">{run.skipped}</td>
                      <td className="px-3 py-2 align-top text-xs text-muted-foreground">
                        {t("knowledgeOps.audit_token_split", {
                          input: run.tokens_in,
                          output: run.tokens_out,
                        })}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {latest?.failure_reason ? (
              <p className="text-xs text-destructive" data-testid="citation-audit-failure">
                {latest.failure_reason}
              </p>
            ) : null}
            {findings && findings.length > 0 ? (
              <div className="space-y-1">
                <p className="text-xs font-medium">{t("knowledgeOps.audit_findings_title")}</p>
                <ul className="space-y-1" data-testid="citation-audit-findings">
                  {findings.slice(0, 10).map((finding) => (
                    <li key={finding.id} className="text-xs text-muted-foreground">
                      <span className="font-medium text-foreground">
                        {auditVerdictLabel(finding.verdict, t)}
                      </span>
                      {" · "}
                      {auditReasonLabel(finding.reason, t)}
                      {" · "}
                      {t("knowledgeOps.audit_finding_scope", {
                        method: finding.method,
                        request: finding.request_id,
                        score: finding.score.toFixed(2),
                      })}
                      {finding.excerpt_hash_ok ? null : (
                        <span className="text-destructive">
                          {" · "}
                          {t("knowledgeOps.audit_hash_mismatch")}
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            ) : latest ? (
              <p className="text-xs text-muted-foreground" data-testid="citation-audit-no-findings">
                {t("knowledgeOps.audit_no_findings")}
              </p>
            ) : null}
            {latest && !latest.judged ? (
              <p className="text-xs text-muted-foreground" data-testid="citation-audit-unavailable">
                {unavailable}
              </p>
            ) : null}
          </>
        )}
      </CardContent>
    </Card>
  );
}
