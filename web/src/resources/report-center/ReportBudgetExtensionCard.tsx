/**
 * "Ask for more budget" on the report's live job (doc/296 §2.2 / A71).
 *
 * Why this is a card rather than a button: an extension is a governance event, and the
 * three facts it involves have to be on screen before it is sent — what the envelope
 * currently is, that one request may at most add the same amount again, and that an
 * approval may be required. A bare "+4000 tokens" button would turn a controlled
 * spend into a reflex.
 *
 * The answer has two states and they are not interchangeable:
 *
 *  - `applied` — the envelope was raised, and `budget_tokens` is the new number;
 *  - `pending_approval` — nothing has changed yet. Rendering that as success would
 *    tell the owner their run may continue when it will stop at the old limit.
 */
import { useState } from "react";
import { useTranslate } from "ra-core";
import { Coins, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, ApiError, unwrap } from "../../lib/api";
import {
  reportBudgetExtensionMaxFor,
  reportBudgetExtensionPending,
  reportBudgetExtensionStatusLabel,
  type ReportBudgetExtensionView,
} from "./report-center-api";

export interface ReportBudgetExtensionCardProps {
  reportId: string;
  /**
   * The job's frozen envelope (`reserved_tokens`). It is the number the bound is
   * stated against; when the job is unknown the card says so instead of inventing a
   * ceiling.
   */
  budget: number | null;
  /** The frozen policy word, so `observe` can be explained rather than guessed. */
  budgetPolicy?: string;
  /** `execute:report` — an extension is an operation on the owner's own run. */
  canExecute: boolean;
}

export function ReportBudgetExtensionCard({
  reportId,
  budget,
  budgetPolicy,
  canExecute,
}: ReportBudgetExtensionCardProps) {
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  const [tokens, setTokens] = useState("");
  const [reason, setReason] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState<ReportBudgetExtensionView | null>(null);

  const max = budget === null ? null : reportBudgetExtensionMaxFor(budget);

  const submit = async () => {
    const increment = Number(tokens);
    if (!Number.isFinite(increment) || !Number.isInteger(increment) || increment <= 0) {
      // The server answers 44105 for this; refusing here as well keeps the round trip
      // out of a conversation that cannot succeed. The message is the same sentence.
      setError(new RangeError(t("reportCenter.budget_extension_tokens_invalid")));
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const view = await unwrap<ReportBudgetExtensionView>(
        api.post(`/reports/${encodeURIComponent(reportId)}/budget-extension`, {
          tokens: increment,
          reason: reason.trim(),
        }),
      );
      setResult(view);
      setOpen(false);
      setTokens("");
      setReason("");
    } catch (err) {
      setError(err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="rounded-lg border p-4" data-testid="report-budget-extension">
      <div className="mb-2 flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <Coins className="size-4" />
            {t("reportCenter.budget_extension_title")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {budget === null
              ? t("reportCenter.budget_extension_unknown")
              : t("reportCenter.budget_extension_envelope", {
                  budget: String(budget),
                  max: String(max ?? 0),
                })}
            {budgetPolicy === "observe" ? ` · ${t("reportCenter.budget_extension_observe")}` : ""}
          </p>
        </div>
        {canExecute && budget !== null && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setOpen((current) => !current);
              setError(null);
            }}
            data-testid="report-budget-extension-open"
          >
            {t("reportCenter.budget_extension_request")}
          </Button>
        )}
      </div>

      {error !== null && (
        <div
          role="alert"
          data-testid="report-budget-extension-error"
          className="mb-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive"
        >
          {error instanceof ApiError
            ? error.displayMessage
            : error instanceof Error && error.message
              ? error.message
              : t("reportCenter.budget_extension_fail")}
        </div>
      )}

      {result !== null && (
        <p
          className="text-sm"
          data-testid="report-budget-extension-result"
          data-status={result.status}
        >
          {t("reportCenter.budget_extension_result", {
            status: reportBudgetExtensionStatusLabel(t, result.status),
            added: String(result.added_tokens),
            budget: String(result.budget_tokens),
          })}
          {reportBudgetExtensionPending(result)
            ? ` ${t("reportCenter.budget_extension_pending_hint")}`
            : ""}
        </p>
      )}

      {open && (
        <div className="mt-2 flex flex-col gap-2 text-sm">
          <label className="flex items-center justify-between gap-3">
            <span>{t("reportCenter.budget_extension_tokens")}</span>
            <Input
              className="w-32"
              inputMode="numeric"
              placeholder={max === null ? "" : String(max)}
              value={tokens}
              onChange={(event) => setTokens(event.target.value)}
              data-testid="report-budget-extension-tokens"
            />
          </label>
          <label className="flex items-center justify-between gap-3">
            <span>{t("reportCenter.budget_extension_reason")}</span>
            <Input
              className="w-64"
              maxLength={200}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              data-testid="report-budget-extension-reason"
            />
          </label>
          <p className="text-xs text-muted-foreground">{t("reportCenter.budget_extension_rule")}</p>
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setOpen(false)} disabled={saving}>
              {t("ra.action.cancel")}
            </Button>
            <Button size="sm" onClick={() => void submit()} disabled={saving} data-testid="report-budget-extension-submit">
              {saving ? <Loader2 className="size-4 animate-spin" /> : null}
              {t("reportCenter.budget_extension_send")}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
