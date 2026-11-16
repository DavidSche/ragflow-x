import { useCallback, useState } from "react";
import { useNotify, useTranslate } from "ra-core";
import { ApiError } from "@/lib/api";
import { applyDatasetParseProfile, updateDatasetPushdown } from "../scenario-templates";
import { postRepairStep } from "./repairPlan";
import type { PushdownRequest, RepairRequest } from "./repairPlan";

export interface RepairActions {
  /** A pushdown repair only *starts* an asynchronous metadata backfill, so the
   *  capability conclusion kept by the wizard predates it (`postRepairStep`). */
  checkStale: boolean;
  clearStale: () => void;
  repairDataset: (datasetId: string, body: RepairRequest) => Promise<void>;
  repairDatasetPushdown: (datasetId: string, body: PushdownRequest) => Promise<void>;
}

/**
 * The two write channels behind the capability repairs, plus the rule for what
 * the wizard may claim afterwards. Kept out of `useScenarioWizard` because the
 * wizard hook sits at the 400-line ceiling (doc/276 §12) and because "what to
 * say after a repair" is exactly what went wrong in doc/285 §10: a queued
 * asynchronous write is not a passed check.
 */
export function useRepairActions(runCapabilityCheck: () => Promise<void>): RepairActions {
  const notify = useNotify();
  const t = useTranslate();
  const [checkStale, setCheckStale] = useState(false);

  const reportFailure = useCallback(
    (error: unknown) => {
      notify(
        error instanceof ApiError ? error.displayMessage : t("scenarioTemplates.wizard.repairFailed"),
        { type: "error" },
      );
    },
    [notify, t],
  );

  /**
   * `POST /datasets/:id/parse-profile`. An approval policy answers 202 + an
   * approval ticket, which is not a write at all, so it must be reported as
   * "submitted for approval" instead of "repair complete" (doc/285 §10.2).
   */
  const repairDataset = useCallback(
    async (datasetId: string, body: RepairRequest) => {
      try {
        const result = await applyDatasetParseProfile(datasetId, body);
        if (result.state === "pending_approval") {
          notify(
            t("scenarioTemplates.wizard.repairPushdownPendingApproval", { request: result.hold.request_no }),
            { type: "info" },
          );
          return;
        }
        // A successful write is not a passed check: the panel must be re-probed.
        // A re-parse is asynchronous, so the re-probe honestly reports
        // `pending_sync` until the engine finishes — say that instead of
        // pretending the repair is already complete.
        notify(
          t(
            result.data.reparse_triggered
              ? "scenarioTemplates.wizard.repairQueued"
              : "scenarioTemplates.wizard.repairApplied",
          ),
          { type: "info" },
        );
        if (postRepairStep("parse_profile") === "recheck") {
          await runCapabilityCheck();
        }
      } catch (error) {
        reportFailure(error);
      }
    },
    [notify, reportFailure, runCapabilityCheck, t],
  );

  /**
   * `PUT /datasets/:id {pushdown_enabled}`. Unlike the parse-profile channel the
   * metadata backfill behind it runs inside the engine, so an immediate re-check
   * reads the pre-backfill documents, answers `repairable` with the same repair,
   * and makes a working fix look broken (E2E 2026-11-16: the metadata landed
   * 15–90 s later). Keep the old conclusion, mark it stale, and let the operator
   * re-check once the backfill lands.
   */
  const repairDatasetPushdown = useCallback(
    async (datasetId: string, body: PushdownRequest) => {
      try {
        const result = await updateDatasetPushdown(datasetId, body);
        if (result.state === "pending_approval") {
          notify(
            t("scenarioTemplates.wizard.repairPushdownPendingApproval", { request: result.hold.request_no }),
            { type: "info" },
          );
          return;
        }
        notify(t("scenarioTemplates.wizard.repairPushdownQueued"), { type: "info" });
        if (postRepairStep("pushdown") === "wait_for_backfill") {
          setCheckStale(true);
        } else {
          await runCapabilityCheck();
        }
      } catch (error) {
        reportFailure(error);
      }
    },
    [notify, reportFailure, runCapabilityCheck, t],
  );

  return {
    checkStale,
    clearStale: useCallback(() => setCheckStale(false), []),
    repairDataset,
    repairDatasetPushdown,
  };
}
