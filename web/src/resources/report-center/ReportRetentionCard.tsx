import { useCallback, useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Loader2, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { api, ApiError, unwrap } from "../../lib/api";
import {
  REPORT_AUTO_CONFIRM_POLICIES,
  REPORT_BUDGET_POLICIES,
  RETENTION_FIELDS,
  reportAutoConfirmPolicyLabel,
  reportBudgetPolicyLabel,
  retentionPatch,
  type RetentionKey,
  type RetentionView,
} from "./report-center-api";

/**
 * The two governance switches (doc/296 §2.2/§2.3), with the JSON name and the
 * inherited counterpart so "inherit" can be rendered with its current value.
 */
const SWITCHES = [
  {
    key: "budget_policy",
    inherited: "system_budget_policy",
    options: REPORT_BUDGET_POLICIES,
    label: reportBudgetPolicyLabel,
  },
  {
    key: "auto_confirm_policy",
    inherited: "system_auto_confirm_policy",
    options: REPORT_AUTO_CONFIRM_POLICIES,
    label: reportAutoConfirmPolicyLabel,
  },
] as const;

type SwitchKey = (typeof SWITCHES)[number]["key"];

interface ReportRetentionCardProps {
  canManage: boolean;
}

/**
 * The tenant's report clocks (T20b), rendered from `GET /report-retention`.
 *
 * The endpoint existed with no entrance: a workspace admin could only change the
 * four report TTLs and the recovery window through the operator surface, i.e. not
 * at all. It is read for everyone who may read reports (the inherited values are
 * shown next to the effective ones so "30 (system 90)" is legible), and writable
 * only for `manage:report` — the same gate the PUT route carries.
 *
 * One rule the copy has to keep honest: **changing a clock never rewrites rows**.
 * Every row froze its own `expires_at` when it was created, so the card says
 * 「只影响新建的报告」 rather than implying yesterday's reports just moved.
 */
export function ReportRetentionCard({ canManage }: ReportRetentionCardProps) {
  const t = useTranslate();
  const [view, setView] = useState<RetentionView | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [draft, setDraft] = useState<Partial<Record<RetentionKey, string>>>({});
  // Empty string means "inherit", exactly like a blank clock: the server reads an
  // omitted switch as "keep following the system", and sending the inherited value
  // would pin it and freeze this workspace out of a later system change.
  const [switchDraft, setSwitchDraft] = useState<Partial<Record<SwitchKey, string>>>({});

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setView(await unwrap<RetentionView>(api.get("/report-retention")));
    } catch (err) {
      setError(err);
      setView(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const openEditor = () => {
    // Seed from the pinned fields only: an inherited clock starts blank so saving
    // without touching it keeps inheriting.
    const seeded: Partial<Record<RetentionKey, string>> = {};
    for (const field of RETENTION_FIELDS) {
      if (view?.overridden.includes(field.key)) seeded[field.key] = String(view[field.key]);
    }
    setDraft(seeded);
    const switched: Partial<Record<SwitchKey, string>> = {};
    for (const item of SWITCHES) {
      if (view?.overridden.includes(item.key)) switched[item.key] = String(view[item.key]);
    }
    setSwitchDraft(switched);
    setEditing(true);
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      // The server answers with the resolved view, so the card renders the new
      // numbers without a second read (and cannot disagree with what was stored).
      const patch = retentionPatch(draft);
      setView(
        await unwrap<RetentionView>(
          api.put("/report-retention", {
            ...patch,
            // Always sent, empty included: leaving the key out means "inherit" for the
            // clocks but is indistinguishable from "no opinion" for a switch, and the
            // editor has to be able to *revert* one to inheriting too.
            budget_policy: switchDraft.budget_policy ?? "",
            auto_confirm_policy: switchDraft.auto_confirm_policy ?? "",
          }),
        ),
      );
      setEditing(false);
    } catch (err) {
      setError(err);
    } finally {
      setSaving(false);
    }
  };

  const label = (key: string) => t(`reportCenter.retention_${key}`);

  return (
    <section className="rounded-lg border p-4">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <ShieldCheck className="size-4" />
            {t("reportCenter.retention_title")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">{t("reportCenter.retention_hint")}</p>
        </div>
        {canManage && view && (
          <Button variant="outline" size="sm" onClick={openEditor}>
            {t("reportCenter.retention_edit")}
          </Button>
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
          data-testid="retention-error"
          className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive"
        >
          {error instanceof ApiError ? error.displayMessage : t("reportCenter.retention_fail")}
        </div>
      )}

      {view && !loading && (
        <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
          {RETENTION_FIELDS.map((field) => {
            const pinned = view.overridden.includes(field.key);
            return (
              <div key={field.key}>
                <dt className="text-xs text-muted-foreground">{label(field.key)}</dt>
                <dd className="font-medium tabular-nums" data-testid={`retention-value-${field.key}`}>
                  {t("reportCenter.retention_days", { days: String(view[field.key]) })}
                  {!pinned && (
                    <span className="ml-1 text-xs font-normal text-muted-foreground">
                      {t("reportCenter.retention_inherited")}
                    </span>
                  )}
                </dd>
              </div>
            );
          })}
        </dl>
      )}

      {view && !loading && (
        <dl className="mt-3 grid gap-3 text-sm sm:grid-cols-2" data-testid="retention-switches">
          {SWITCHES.map((item) => {
            const pinned = view.overridden.includes(item.key);
            return (
              <div key={item.key}>
                <dt className="text-xs text-muted-foreground">{t(`reportCenter.retention_${item.key}`)}</dt>
                <dd className="font-medium" data-testid={`retention-switch-${item.key}`}>
                  {item.label(t, String(view[item.key]))}
                  {!pinned && (
                    <span className="ml-1 text-xs font-normal text-muted-foreground">
                      {t("reportCenter.retention_inherited")}
                    </span>
                  )}
                </dd>
              </div>
            );
          })}
        </dl>
      )}

      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("reportCenter.retention_edit")}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-3 text-sm">
            <p className="text-xs text-muted-foreground">{t("reportCenter.retention_blank_means_inherit")}</p>
            {RETENTION_FIELDS.map((field) => (
              <label key={field.key} className="flex items-center justify-between gap-3">
                <span>{label(field.key)}</span>
                <Input
                  className="w-28"
                  inputMode="numeric"
                  placeholder={String(view?.[field.inherited] ?? "")}
                  value={draft[field.key] ?? ""}
                  onChange={(event) =>
                    setDraft((current) => ({ ...current, [field.key]: event.target.value }))
                  }
                  data-testid={`retention-${field.key}`}
                />
              </label>
            ))}
            {/* The two switches belong in the same dialog as the clocks: they are the
                same kind of decision (one workspace-wide answer), and an operator who
                has to look in two places for "what does this tenant allow" will
                eventually read a stale one. */}
            {SWITCHES.map((item) => (
              <label key={item.key} className="flex items-center justify-between gap-3">
                <span>{t(`reportCenter.retention_${item.key}`)}</span>
                <select
                  className="h-9 rounded-md border bg-background px-2"
                  value={switchDraft[item.key] ?? ""}
                  onChange={(event) =>
                    setSwitchDraft((current) => ({ ...current, [item.key]: event.target.value }))
                  }
                  data-testid={`retention-switch-input-${item.key}`}
                >
                  <option value="">
                    {t("reportCenter.retention_switch_inherit", {
                      value: item.label(t, String(view?.[item.inherited] ?? "")),
                    })}
                  </option>
                  {item.options.map((option) => (
                    <option key={option} value={option}>
                      {item.label(t, option)}
                    </option>
                  ))}
                </select>
              </label>
            ))}
            <p className="text-xs text-muted-foreground">{t("reportCenter.retention_switch_hint")}</p>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(false)} disabled={saving}>
              {t("ra.action.cancel")}
            </Button>
            <Button onClick={() => void save()} disabled={saving}>
              {saving ? <Loader2 className="size-4 animate-spin" /> : null}
              {t("reportCenter.retention_save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
