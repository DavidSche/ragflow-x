/**
 * R20: the report's recurring plan, as a card inside the detail drawer.
 *
 * Why the card exists at all: a scheduled report is a *promise about the future* —
 * "every morning at 09:00 this report rebuilds itself". A promise the reader cannot
 * see is a promise that quietly breaks, so the card always states three things:
 *
 *  - **when it will next run**, computed by the server (`next_run_at`), never guessed
 *    here from `cadence` + `hour`: the browser does not know the plan's UTC offset
 *    arithmetic and a client-side guess would disagree with the tick that runs it;
 *  - **what the last run did**, as a closed-set word (`last_status`). `skipped_busy`
 *    and `skipped_no_base` are not failures — the report was not rebuilt, and saying
 *    "failed" would send the reader to debug a provider that never ran;
 *  - **what a rerun rebuilds** (`full` vs `reuse_static`): keeping sections that need
 *    no evidence is a cost decision, so it is stated rather than implied.
 *
 * An absent plan answers `{"schedule": null}` with 200 — "this report has no
 * schedule" is a normal answer, so the card renders a creation form instead of an
 * error.
 */
import { useCallback, useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { CalendarClock, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, ApiError, unwrap } from "../../lib/api";
import {
  formatTZOffset,
  reportScheduleCadenceLabel,
  reportScheduleRunStatusLabel,
  reportScheduleUpdateModeLabel,
  reportScheduleWhen,
  type ReportSchedulePatch,
  type ReportScheduleView,
} from "./report-center-api";

export interface ReportScheduleCardProps {
  reportId: string;
  /** `execute:report` — a plan is a timed rerun, so it needs the rerun's permission. */
  canExecute: boolean;
  /** Local clock formatting for the absolute timestamps; the UI owns this. */
  formatTime: (value: string) => string;
}

interface ScheduleDraft {
  cadence: string;
  hour: string;
  minute: string;
  weekday: string;
  updateMode: string;
  enabled: boolean;
}

const DAILY_HOUR = "9";

function draftOf(view: ReportScheduleView | null): ScheduleDraft {
  if (!view) {
    return {
      cadence: "daily",
      hour: DAILY_HOUR,
      minute: "0",
      weekday: "1",
      updateMode: "full",
      enabled: true,
    };
  }
  return {
    cadence: view.cadence,
    hour: String(view.hour),
    minute: String(view.minute),
    weekday: String(view.weekday),
    updateMode: view.update_mode,
    enabled: view.enabled,
  };
}

/**
 * Builds the PUT body. Values are parsed here so a typo is refused before the round
 * trip; the server validates the same closed sets and answers 44105, and that answer
 * is rendered rather than replaced with a client-side apology.
 */
export function schedulePatch(draft: ScheduleDraft, initial: ReportScheduleView | null): ReportSchedulePatch {
  const hour = Number(draft.hour);
  const minute = Number(draft.minute);
  const weekday = Number(draft.weekday);
  if (!Number.isInteger(hour) || hour < 0 || hour > 23) throw new RangeError("hour out of range");
  if (!Number.isInteger(minute) || minute < 0 || minute > 59) throw new RangeError("minute out of range");
  if (!Number.isInteger(weekday) || weekday < 0 || weekday > 6) throw new RangeError("weekday out of range");
  const patch: ReportSchedulePatch = {
    cadence: draft.cadence,
    hour,
    minute,
    weekday,
    update_mode: draft.updateMode,
    enabled: draft.enabled,
  };
  // The offset is not editable here, but it must travel on the first write: creating a
  // plan without one would silently default the server to UTC+08:00 for a workspace
  // that is not there. Once stored, the server keeps it unless it is re-sent.
  if (!initial) patch.tz_offset_min = -new Date().getTimezoneOffset();
  return patch;
}

export function ReportScheduleCard({ reportId, canExecute, formatTime }: ReportScheduleCardProps) {
  const t = useTranslate();
  const [view, setView] = useState<ReportScheduleView | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState<ScheduleDraft>(draftOf(null));

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // The endpoint answers `{"schedule": null}` for "no plan"; both shapes are 200,
      // so `null` is a normal answer rather than a caught 404.
      const body = await unwrap<{ schedule: ReportScheduleView | null }>(
        api.get(`/reports/${encodeURIComponent(reportId)}/schedule`),
      );
      setView(body?.schedule ?? null);
      setDraft(draftOf(body?.schedule ?? null));
    } catch (err) {
      setError(err);
      setView(null);
    } finally {
      setLoading(false);
    }
  }, [reportId]);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const body = await unwrap<{ schedule: ReportScheduleView | null }>(
        api.put(`/reports/${encodeURIComponent(reportId)}/schedule`, schedulePatch(draft, view)),
      );
      setView(body?.schedule ?? null);
      setDraft(draftOf(body?.schedule ?? null));
    } catch (err) {
      setError(err);
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    setSaving(true);
    setError(null);
    try {
      // Delete is idempotent (204 either way): the caller's intent is "it must not
      // run again", and after the first call that intent is already true.
      await api.delete(`/reports/${encodeURIComponent(reportId)}/schedule`);
      setView(null);
      setDraft(draftOf(null));
    } catch (err) {
      setError(err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="rounded-lg border p-4" data-testid="report-schedule">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <CalendarClock className="size-4" />
            {t("reportCenter.schedule_title")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">{t("reportCenter.schedule_hint")}</p>
        </div>
        {view && (
          <span
            className="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground"
            data-testid="report-schedule-enabled"
          >
            {view.enabled ? t("reportCenter.schedule_enabled") : t("reportCenter.schedule_disabled")}
          </span>
        )}
      </div>

      {loading && (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> {t("reportCenter.loading")}
        </div>
      )}

      {error !== null && (
        <div
          role="alert"
          data-testid="report-schedule-error"
          className="mb-3 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive"
        >
          {error instanceof ApiError ? error.displayMessage : t("reportCenter.schedule_fail")}
        </div>
      )}

      {!loading && view && (
        <dl className="mb-3 grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
          <div>
            <dt className="text-xs text-muted-foreground">{t("reportCenter.schedule_when")}</dt>
            <dd className="font-medium" data-testid="report-schedule-when">
              {reportScheduleWhen(t, view)}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t("reportCenter.schedule_next")}</dt>
            <dd className="font-medium tabular-nums" data-testid="report-schedule-next">
              {view.enabled ? formatTime(view.next_run_at) : t("reportCenter.schedule_paused")}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t("reportCenter.schedule_update_mode")}</dt>
            <dd className="font-medium" data-testid="report-schedule-mode">
              {reportScheduleUpdateModeLabel(t, view.update_mode)}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t("reportCenter.schedule_last")}</dt>
            <dd className="font-medium" data-testid="report-schedule-last">
              {view.last_status ? (
                <>
                  {reportScheduleRunStatusLabel(t, view.last_status)}
                  {view.last_run_at && (
                    <span className="ml-1 text-xs font-normal text-muted-foreground tabular-nums">
                      {formatTime(view.last_run_at)}
                    </span>
                  )}
                  {view.last_revision > 0 && (
                    <span className="ml-1 text-xs font-normal text-muted-foreground">
                      {t("reportCenter.schedule_last_revision", { revision: String(view.last_revision) })}
                    </span>
                  )}
                </>
              ) : (
                t("reportCenter.schedule_never")
              )}
            </dd>
          </div>
          {view.last_error && view.last_status === "failed" && (
            // Only a real failure explains itself. A `skipped_*` status deliberately
            // clears the message: a stale error must not be read as the reason for a
            // skip that happened for a different one.
            <div className="col-span-2 sm:col-span-3">
              <dt className="text-xs text-muted-foreground">{t("reportCenter.schedule_error")}</dt>
              <dd className="text-destructive" data-testid="report-schedule-last-error">
                {view.last_error}
              </dd>
            </div>
          )}
        </dl>
      )}

      {canExecute && !loading && (
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-muted-foreground">{t("reportCenter.schedule_cadence")}</span>
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={draft.cadence}
              onChange={(event) => setDraft((current) => ({ ...current, cadence: event.target.value }))}
              data-testid="report-schedule-cadence"
            >
              {["daily", "weekly"].map((value) => (
                <option key={value} value={value}>
                  {reportScheduleCadenceLabel(t, value)}
                </option>
              ))}
            </select>
          </label>
          {draft.cadence === "weekly" && (
            <label className="flex flex-col gap-1 text-xs">
              <span className="text-muted-foreground">{t("reportCenter.schedule_weekday")}</span>
              <Input
                className="w-16"
                inputMode="numeric"
                value={draft.weekday}
                onChange={(event) => setDraft((current) => ({ ...current, weekday: event.target.value }))}
                data-testid="report-schedule-weekday"
              />
            </label>
          )}
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-muted-foreground">{t("reportCenter.schedule_hour")}</span>
            <Input
              className="w-16"
              inputMode="numeric"
              value={draft.hour}
              onChange={(event) => setDraft((current) => ({ ...current, hour: event.target.value }))}
              data-testid="report-schedule-hour"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-muted-foreground">{t("reportCenter.schedule_minute")}</span>
            <Input
              className="w-16"
              inputMode="numeric"
              value={draft.minute}
              onChange={(event) => setDraft((current) => ({ ...current, minute: event.target.value }))}
              data-testid="report-schedule-minute"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-muted-foreground">{t("reportCenter.schedule_update_mode")}</span>
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={draft.updateMode}
              onChange={(event) => setDraft((current) => ({ ...current, updateMode: event.target.value }))}
              data-testid="report-schedule-update-mode"
            >
              {["full", "reuse_static"].map((value) => (
                <option key={value} value={value}>
                  {reportScheduleUpdateModeLabel(t, value)}
                </option>
              ))}
            </select>
          </label>
          <Button
            variant={draft.enabled ? "secondary" : "outline"}
            size="sm"
            onClick={() => setDraft((current) => ({ ...current, enabled: !current.enabled }))}
            data-testid="report-schedule-toggle"
          >
            {draft.enabled ? t("reportCenter.schedule_enabled") : t("reportCenter.schedule_disabled")}
          </Button>
          <Button size="sm" onClick={() => void save()} disabled={saving} data-testid="report-schedule-save">
            {saving ? <Loader2 className="size-4 animate-spin" /> : null}
            {view ? t("reportCenter.schedule_save") : t("reportCenter.schedule_create")}
          </Button>
          {view && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => void remove()}
              disabled={saving}
              data-testid="report-schedule-delete"
            >
              {t("reportCenter.schedule_delete")}
            </Button>
          )}
          <span className="text-xs text-muted-foreground">
            {view
              ? t("reportCenter.schedule_offset_note", { offset: formatTZOffset(view.tz_offset_min) })
              : t("reportCenter.schedule_offset_default")}
          </span>
        </div>
      )}
    </section>
  );
}
