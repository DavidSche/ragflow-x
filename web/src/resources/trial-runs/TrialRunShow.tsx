import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useCreatePath, useNavigate, useParams, useTranslate } from "ra-core";
import { ArrowLeft, Ban, CheckCircle2, Loader2, RefreshCw, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import {
  cancelTrialRun,
  formatHitRate,
  getTrialRun,
  type TrialRun,
} from "./types";

function snapshotRows(run: TrialRun): { label: string; value: string }[] {
  const snapshot = run.config_snapshot;
  if (!snapshot) return [];
  return [
    { label: "assistant", value: `${snapshot.assistant_name} (${snapshot.assistant_kind})` },
    { label: "datasets", value: (snapshot.dataset_ids ?? []).join(", ") },
    { label: "template", value: `${snapshot.template_name} v${snapshot.template_version}` },
    { label: "preset", value: snapshot.prompt_preset_id },
    { label: "profile", value: snapshot.parameter_profile_id },
    { label: "llm", value: snapshot.llm_id || "—" },
    { label: "rerank", value: snapshot.rerank_id || "—" },
    { label: "top_n", value: String(snapshot.top_n) },
    { label: "top_k", value: String(snapshot.top_k) },
    { label: "similarity", value: String(snapshot.similarity_threshold) },
    { label: "vectorWeight", value: String(snapshot.vector_similarity_weight) },
    { label: "mode", value: snapshot.mode },
    { label: "policy", value: snapshot.policy_id || "—" },
    { label: "language", value: snapshot.language || "—" },
  ];
}

/**
 * Trial run detail: summary metrics, reproducibility snapshot and per-case
 * retrieval results. Read-only on purpose — a trial run is never converted into
 * release evidence (doc/228 T7.3′, doc/231 D10).
 */
export const TrialRunShow = () => {
  const { id } = useParams<{ id: string }>();
  const t = useTranslate();
  const navigate = useNavigate();
  const createPath = useCreatePath();
  const [run, setRun] = useState<TrialRun | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [cancelling, setCancelling] = useState(false);
  // Cancelling is a write (`trial-run:execute`). A read-only consumer (viewer /
  // business_user) would otherwise be offered a button that always 403s — the
  // same dead-end rule AssistantEmptyState follows (doc/244).
  const { canAccess: canExecute } = useCanAccess({ resource: "trial-run", action: "execute" });

  const load = useCallback(async () => {
    if (!id) return;
    try {
      setRun(await getTrialRun(id));
      setState("ready");
      setError("");
    } catch (err) {
      setState("error");
      setError(err instanceof ApiError ? err.displayMessage : t("scenarioTemplates.trial.loadFailed"));
    }
  }, [id, t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!run || (run.status !== "QUEUED" && run.status !== "RUNNING")) return;
    const timer = window.setInterval(() => void load(), 2000);
    return () => window.clearInterval(timer);
  }, [load, run]);

  const cancel = async () => {
    if (!id) return;
    setCancelling(true);
    try {
      setRun(await cancelTrialRun(id));
    } catch (err) {
      setError(err instanceof ApiError ? err.displayMessage : t("scenarioTemplates.trial.cancelFailed"));
    } finally {
      setCancelling(false);
    }
  };

  if (state === "loading") {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <Loader2 className="mr-2 size-4 animate-spin" aria-hidden="true" />
        {t("scenarioTemplates.trial.loading")}
      </div>
    );
  }

  if (state === "error" || !run) {
    return (
      <div className="mx-auto w-full max-w-4xl space-y-3 p-4">
        <p className="rounded border border-destructive/40 p-3 text-sm text-destructive">{error}</p>
        <Button variant="outline" onClick={() => navigate(createPath({ resource: "trial-runs", type: "list" }))}>
          <ArrowLeft className="size-4" /> {t("scenarioTemplates.trial.back")}
        </Button>
      </div>
    );
  }

  const summary = run.summary;
  const pending = run.status === "QUEUED" || run.status === "RUNNING";

  return (
    <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h1 className="text-xl font-semibold">
            {t("scenarioTemplates.trial.detailTitle", { name: run.config_snapshot?.assistant_name || run.assistant_id })}
          </h1>
          <p className="text-sm text-muted-foreground">
            {t(`scenarioTemplates.trial.status.${run.status}`)}
            {run.failure_reason ? ` · ${run.failure_reason}` : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => void load()}>
            <RefreshCw className="size-4" /> {t("scenarioTemplates.trial.refresh")}
          </Button>
          {pending && canExecute === true ? (
            <Button variant="outline" disabled={cancelling} onClick={() => void cancel()}>
              <Ban className="size-4" /> {t("scenarioTemplates.trial.cancel")}
            </Button>
          ) : null}
          <Button
            variant="ghost"
            onClick={() => navigate(createPath({ resource: "trial-runs", type: "list" }))}
          >
            <ArrowLeft className="size-4" /> {t("scenarioTemplates.trial.back")}
          </Button>
        </div>
      </header>

      {error ? <p className="rounded border border-destructive/40 p-3 text-sm text-destructive">{error}</p> : null}

      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded border p-3">
          <p className="text-xs text-muted-foreground">{t("scenarioTemplates.trial.columnHitRate")}</p>
          <p className="mt-1 text-lg font-semibold">
            {formatHitRate(summary, t("scenarioTemplates.trial.hitUndefined"))}
          </p>
          {summary.undefined_cases > 0 ? (
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("scenarioTemplates.trial.undefinedCases", { count: summary.undefined_cases })}
            </p>
          ) : null}
        </div>
        <div className="rounded border p-3">
          <p className="text-xs text-muted-foreground">{t("scenarioTemplates.trial.columnCases")}</p>
          <p className="mt-1 text-lg font-semibold">
            {summary.returned_cases}/{summary.cases}
          </p>
          {summary.failed_cases > 0 ? (
            <p className="mt-1 text-[11px] text-destructive">
              {t("scenarioTemplates.trial.failedCases", { count: summary.failed_cases })}
            </p>
          ) : null}
        </div>
        <div className="rounded border p-3">
          <p className="text-xs text-muted-foreground">{t("scenarioTemplates.trial.latencyP95")}</p>
          <p className="mt-1 text-lg font-semibold">{summary.latency_p95_ms} ms</p>
        </div>
        <div className="rounded border p-3">
          <p className="text-xs text-muted-foreground">{t("scenarioTemplates.trial.mode")}</p>
          <p className="mt-1 text-lg font-semibold">{summary.mode}</p>
          <p className="mt-1 text-[11px] text-muted-foreground">
            {t("scenarioTemplates.trial.gateEligibleFalse")}
          </p>
        </div>
      </section>

      <section className="rounded border p-4">
        <h2 className="text-sm font-medium">{t("scenarioTemplates.trial.snapshotTitle")}</h2>
        <p className="mt-1 text-xs text-muted-foreground">{t("scenarioTemplates.trial.snapshotHint")}</p>
        <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
          {snapshotRows(run).map((row) => (
            <div key={row.label} className="flex min-w-0 gap-2">
              <dt className="shrink-0 text-muted-foreground">{row.label}</dt>
              <dd className="truncate" title={row.value}>
                {row.value || "—"}
              </dd>
            </div>
          ))}
        </dl>
      </section>

      <section className="rounded border">
        <h2 className="border-b px-4 py-3 text-sm font-medium">{t("scenarioTemplates.trial.casesTitle")}</h2>
        {(run.cases ?? []).length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">{t("scenarioTemplates.trial.noCases")}</p>
        ) : (
          <ul className="divide-y">
            {(run.cases ?? []).map((item) => (
              <li key={item.case_id} className="flex flex-wrap items-start gap-3 p-3 text-sm">
                <span className="mt-0.5" aria-hidden="true">
                  {item.returned ? (
                    <CheckCircle2 className="size-4 text-emerald-600" />
                  ) : (
                    <XCircle className="size-4 text-muted-foreground" />
                  )}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate">{item.question}</p>
                  <p className="mt-1 text-[11px] text-muted-foreground">
                    {joinParts([
                      item.hit_at_k === null
                        ? t("scenarioTemplates.trial.hitUndefined")
                        : `${t("scenarioTemplates.trial.hit")}: ${
                            item.hit_at_k ? t("scenarioTemplates.trial.hitYes") : t("scenarioTemplates.trial.hitNo")
                          }`,
                      `top: ${item.top_score}`,
                      `${item.latency_ms} ms`,
                      item.error_code ? `${t("scenarioTemplates.trial.errorCode")}: ${item.error_code}` : "",
                    ])}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
};

function joinParts(parts: string[]): string {
  return parts.filter(Boolean).join(" · ");
}
