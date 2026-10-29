import { useCallback, useEffect, useState } from "react";
import { useCreatePath, useNavigate, useTranslate } from "ra-core";
import { Loader2, RefreshCw, FlaskConical } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { formatHitRate, listTrialRuns, type TrialRun } from "./types";

const STATUS_STYLES: Record<string, string> = {
  QUEUED: "bg-muted text-muted-foreground",
  RUNNING: "bg-primary/10 text-primary",
  COMPLETED: "bg-emerald-500/10 text-emerald-600",
  FAILED: "bg-destructive/10 text-destructive",
  CANCELLED: "bg-muted text-muted-foreground",
};

/**
 * Read-only trial run history. Trial runs never appear in the release
 * evaluation list: they are practice runs, not release evidence
 * (doc/228 T7.3′, doc/231 D10).
 */
export const TrialRunList = () => {
  const t = useTranslate();
  const navigate = useNavigate();
  const createPath = useCreatePath();
  const [runs, setRuns] = useState<TrialRun[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const items = await listTrialRuns({});
      setRuns(items);
      setState("ready");
      setError("");
    } catch (err) {
      setState("error");
      setError(err instanceof ApiError ? err.displayMessage : t("scenarioTemplates.trial.loadFailed"));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    const pending = runs.some((run) => run.status === "QUEUED" || run.status === "RUNNING");
    if (!pending) return;
    const timer = window.setInterval(() => void load(), 3000);
    return () => window.clearInterval(timer);
  }, [load, runs]);

  return (
    <div className="mx-auto w-full max-w-6xl space-y-4 p-4">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-3">
          <FlaskConical className="size-6 text-primary" aria-hidden="true" />
          <div>
            <h1 className="text-xl font-semibold">{t("scenarioTemplates.trial.title")}</h1>
            <p className="text-sm text-muted-foreground">{t("scenarioTemplates.trial.subtitle")}</p>
          </div>
        </div>
        <Button variant="outline" onClick={() => void load()}>
          <RefreshCw className="size-4" /> {t("scenarioTemplates.trial.refresh")}
        </Button>
      </header>

      <p className="rounded border bg-muted/30 p-3 text-xs text-muted-foreground">
        {t("scenarioTemplates.trial.notGateEligible")}
      </p>

      {state === "loading" ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden="true" />
          {t("scenarioTemplates.trial.loading")}
        </p>
      ) : null}
      {state === "error" ? (
        <p className="rounded border border-destructive/40 p-3 text-sm text-destructive">{error}</p>
      ) : null}
      {state === "ready" && runs.length === 0 ? (
        <p className="rounded border p-4 text-sm text-muted-foreground">
          {t("scenarioTemplates.trial.empty")}
        </p>
      ) : null}

      {runs.length > 0 ? (
        <div className="overflow-x-auto rounded border">
          <table className="w-full text-sm">
            <thead className="bg-muted/40 text-left text-xs uppercase text-muted-foreground">
              <tr>
                <th className="px-3 py-2">{t("scenarioTemplates.trial.columnAssistant")}</th>
                <th className="px-3 py-2">{t("scenarioTemplates.trial.columnTemplate")}</th>
                <th className="px-3 py-2">{t("scenarioTemplates.trial.columnStatus")}</th>
                <th className="px-3 py-2">{t("scenarioTemplates.trial.columnCases")}</th>
                <th className="px-3 py-2">{t("scenarioTemplates.trial.columnHitRate")}</th>
                <th className="px-3 py-2">{t("scenarioTemplates.trial.columnCreated")}</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((run) => (
                <tr
                  key={run.id}
                  className="cursor-pointer border-t hover:bg-muted/40"
                  onClick={() => navigate(createPath({ resource: "trial-runs", type: "show", id: run.id }))}
                >
                  <td className="px-3 py-2">
                    {run.config_snapshot?.assistant_name || run.assistant_id}
                  </td>
                  <td className="px-3 py-2">
                    {run.config_snapshot?.template_name || run.scenario_template_id}
                    {run.template_version ? ` · v${run.template_version}` : ""}
                  </td>
                  <td className="px-3 py-2">
                    <span className={`rounded px-2 py-0.5 text-xs ${STATUS_STYLES[run.status] ?? ""}`}>
                      {t(`scenarioTemplates.trial.status.${run.status}`)}
                    </span>
                  </td>
                  <td className="px-3 py-2">{run.summary?.cases ?? 0}</td>
                  <td className="px-3 py-2">
                    {formatHitRate(run.summary, t("scenarioTemplates.trial.hitUndefined"))}
                  </td>
                  <td className="px-3 py-2 text-muted-foreground">
                    {new Date(run.created_at).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </div>
  );
};
