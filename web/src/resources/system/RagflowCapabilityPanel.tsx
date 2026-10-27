import { useEffect, useState } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Loader2, RefreshCw, ShieldCheck } from "lucide-react";
import { api, ApiError, unwrap } from "../../lib/api";

interface CapabilityReport {
  provider: string;
  provider_version?: string;
  pinned_version: string;
  detected_version?: string;
  runtime_health: string;
  upgrade_decision?: string;
  blocking_capabilities?: string[];
  trial_capabilities?: string[];
  items: {
    name: string;
    api_profile: string;
    lifecycle: string;
    status: string;
    verified_version?: string;
    runtime_health: string;
  }[];
}

const STATUS_TONES: Record<string, string> = {
  VERIFIED: "bg-emerald-50 text-emerald-700",
  COMPATIBLE: "bg-sky-50 text-sky-700",
  UNVERIFIED: "bg-amber-50 text-amber-700",
  DEPRECATED: "bg-red-50 text-red-700",
  HEALTHY: "bg-emerald-50 text-emerald-700",
  UNAVAILABLE: "bg-red-50 text-red-700",
  UNKNOWN: "bg-amber-50 text-amber-700",
};

const STATUS_LABELS: Record<string, string> = {
  VERIFIED: "capability_verified",
  COMPATIBLE: "capability_compatible",
  UNVERIFIED: "capability_unverified",
  DEPRECATED: "capability_deprecated",
  HEALTHY: "capability_runtime_healthy",
  UNAVAILABLE: "capability_runtime_unavailable",
  UNKNOWN: "capability_runtime_unknown",
};

const UPGRADE_LABELS: Record<string, string> = {
  READY: "capability_upgrade_ready",
  UNKNOWN_RUNTIME: "capability_upgrade_unknown_runtime",
  BLOCKED_RUNTIME_UNAVAILABLE: "capability_upgrade_blocked_runtime",
  BLOCKED_CONTRACT_DRILL_REQUIRED: "capability_upgrade_blocked_contract_drill",
  BLOCKED_VERSION_NOT_APPROVED: "capability_upgrade_blocked_version",
};

function StatusBadge({ value }: { value: string }) {
  const tone = STATUS_TONES[value] ?? "bg-amber-50 text-amber-700";
  const label = STATUS_LABELS[value] ?? UPGRADE_LABELS[value] ?? value;
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}>
      {label}
    </span>
  );
}

export const RagflowCapabilityPanel = () => {
  const t = useTranslate();
  const notify = useNotify();
  const [report, setReport] = useState<CapabilityReport | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [verifying, setVerifying] = useState(false);

  const load = async () => {
    try {
      const result = await unwrap<CapabilityReport>(api.get("/system/capabilities"));
      setReport(result);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("system.capabilities_load_fail"), { type: "error" });
    } finally {
      setLoaded(true);
    }
  };

  useEffect(() => { void load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const verify = async () => {
    setVerifying(true);
    try {
      const result = await unwrap<CapabilityReport>(api.post("/system/capabilities/verify", {}));
      setReport(result);
      notify(t("system.capabilities_verified"), { type: "success" });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("system.capabilities_verify_fail"), { type: "error" });
      await load();
    } finally {
      setVerifying(false);
    }
  };

  if (!loaded) {
    return (
      <section className="mt-4 rounded border p-4 text-sm text-muted-foreground">
        <Loader2 className="mr-2 inline size-4 animate-spin" aria-hidden="true" />
        {t("ra.page.loading")}
      </section>
    );
  }

  return (
    <section className="mt-4 rounded border p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-medium">{t("system.capabilities_title")}</h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {report?.pinned_version || "-"}
            {report?.detected_version ? ` · ${report.detected_version}` : ""}
          </p>
          {report?.provider_version ? (
            <p className="mt-1 text-xs text-muted-foreground">
              {t("system.capability_provider_adapter")}: {report.provider_version}
            </p>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          {report?.runtime_health ? <StatusBadge value={report.runtime_health} /> : null}
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={verifying}>
            <RefreshCw className="size-4" aria-hidden="true" />
            {t("system.refresh")}
          </Button>
          <Button size="sm" onClick={() => void verify()} disabled={verifying}>
            {verifying ? <Loader2 className="size-4 animate-spin" /> : <ShieldCheck className="size-4" aria-hidden="true" />}
            {t("system.capabilities_verify")}
          </Button>
        </div>
      </div>

      {report?.upgrade_decision ? (
        <div className="mb-3 rounded border bg-muted/30 p-3 text-sm">
          <div className="flex items-center gap-2">
            <StatusBadge value={report.upgrade_decision} />
            <span className="text-muted-foreground">{t("system.capability_upgrade_title")}</span>
          </div>
          {report.blocking_capabilities?.length ? (
            <p className="mt-2 text-xs text-muted-foreground">
              {t("system.capability_upgrade_blocking")}: {report.blocking_capabilities.join(", ")}
            </p>
          ) : null}
          {report.trial_capabilities?.length ? (
            <p className="mt-1 text-xs text-muted-foreground">
              {t("system.capability_upgrade_trial")}: {report.trial_capabilities.join(", ")}
            </p>
          ) : null}
        </div>
      ) : null}

      {report?.items?.length ? (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="text-xs text-muted-foreground">
              <tr>
                <th className="py-2">{t("system.capability_name")}</th>
                <th className="py-2">{t("system.capability_api_profile")}</th>
                <th className="py-2">{t("system.capability_status")}</th>
                <th className="py-2">{t("system.capability_runtime")}</th>
              </tr>
            </thead>
            <tbody>
              {report.items.map((item) => (
                <tr key={item.name} className="border-t">
                  <td className="py-2">{item.name}</td>
                  <td className="py-2 text-muted-foreground">{item.api_profile}</td>
                  <td className="py-2"><StatusBadge value={item.status} /></td>
                  <td className="py-2"><StatusBadge value={item.runtime_health} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="text-sm text-muted-foreground">{t("common.empty")}</div>
      )}
    </section>
  );
};
