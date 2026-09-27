import { useEffect, useState } from "react";
import { useLocaleState, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { KeyRound, Loader2, RefreshCw, Save, ShieldAlert, Undo2 } from "lucide-react";
import { api, ApiError, unwrap, type ApiEnvelope } from "../../lib/api";
import { sealRSAOAEPSHA256 } from "../../lib/seal";

type SettingPolicy = {
  source: string;
  editable: boolean;
  database_override: boolean;
  restart_required: boolean;
  risk_level: string;
  impact?: string;
  effective_mode?: string;
  type?: string;
  min?: number;
  max?: number;
  allowed?: string[];
};

type RuntimeInstance = {
  runtime_instance_id: string;
  apply_status: string;
  apply_error: string;
  last_seen_at: string;
  heartbeat_state: string;
};

type Revision = {
  id: string;
  revision: number;
  revision_status: string;
  created_by: string;
  created_at: string;
  note: string;
};

type SettingsView = {
  revision: number;
  desired_revision_id: string;
  deployment_effective_state: string;
  groups: Record<string, Record<string, { effective: unknown } & SettingPolicy>>;
  runtime_instances: RuntimeInstance[];
  secret_references: Record<string, { id: string; version: number }>;
};

type RevisionPage = { items: Revision[] };

const secretKeys = [
  "observability.metrics_token",
  "approval.notify_webhook_secret",
  "runtime.report_token",
  "alerting.webhook_secret",
] as const;

const inputCls = "w-full rounded border bg-background px-2 py-1 text-sm";

// Deployment-controlled keys (doc/104 §15): rendered read-only with a mono
// input so operators can inspect the configured template but never edit it
// through the UI — changes require a deployment configuration update.
const deploymentControlledKeys = new Set(["observability.otel_trace_ui_url_template"]);

function clampNumber(value: string, min?: number, max?: number): string {
  const number = Number(value);
  if (!Number.isFinite(number)) return value;
  if (typeof min === "number" && number < min) return String(min);
  if (typeof max === "number" && number > max) return String(max);
  return String(number);
}

function numberStep(key: string): string {
  return key === "observability.sample_ratio" ? "0.01" : "1";
}

function validateDraft(
  settings: SettingsView | null,
  draft: Record<string, string>,
): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const [key, rawValue] of Object.entries(draft)) {
    const [group, ...fieldParts] = key.split(".");
    const item = settings?.groups[group]?.[fieldParts.join(".")];
    if (item?.type === "webhook_list") {
      try {
        const parsed = JSON.parse(rawValue);
        if (!Array.isArray(parsed) || parsed.length > 20) {
          errors[key] = "system.settings_webhook_invalid";
        }
      } catch {
        errors[key] = "system.settings_webhook_invalid";
      }
      continue;
    }
    if (item?.type !== "number") continue;
    const value = Number(rawValue);
    if (!Number.isFinite(value)) {
      errors[key] = "system.settings_number_invalid";
      continue;
    }
    if (
      (typeof item.min === "number" && value < item.min) ||
      (typeof item.max === "number" && value > item.max)
    ) {
      errors[key] = "system.settings_number_out_of_range";
    }
  }
  return errors;
}

const settingImpactZh: Record<string, string> = {
  "allows credentialed cross-origin requests": "允许携带凭据的跨源请求",
  "allows provider requests to private networks": "允许 Provider 访问私有网络",
  "allows selected networks to read metrics": "允许指定网段读取 Metrics",
  "changes allowed HTTP methods": "修改允许的 HTTP 方法",
  "changes allowed request headers": "修改允许的请求头",
  "changes approval maintenance interval": "修改审批维护任务间隔",
  "changes approval policy cache freshness": "修改审批策略缓存刷新频率",
  "changes approval record retention": "修改审批记录保留期",
  "changes approval reminder lead time": "修改审批提醒提前量",
  "changes approved action retry budget": "修改已批准操作的重试次数",
  "changes assistant routing": "修改助手路由",
  "changes browser content loading policy": "修改浏览器内容加载策略",
  "changes CORS preflight cache": "修改 CORS 预检缓存",
  "changes default approval expiry": "修改默认审批有效期",
  "changes duplicate alert throttle": "修改重复告警节流时间",
  "changes metrics endpoint": "修改 Metrics 地址",
  "changes request body limit": "修改请求体大小上限",
  "changes trace sampling": "修改链路追踪采样率",
  "changes trace service identity": "修改链路追踪服务标识",
  "changes when runtime instances are marked stale": "修改运行实例标记超时的时间",
  "controls browser HTTPS pin duration": "控制浏览器 HTTPS 记忆有效期",
  "controls frame embedding": "控制 iframe 嵌入",
  "enables or disables OpenTelemetry": "启用或停用 OpenTelemetry",
  "enforces or bypasses governance approvals": "启用或绕过治理审批",
  "expands cross-origin browser access": "扩大浏览器跨源访问范围",
  "exposes or hides metrics endpoint": "暴露或隐藏 Metrics 端点",
  "extends HTTPS requirement to subdomains": "将 HTTPS 要求扩展到子域名",
  "renames Prometheus metric namespace": "重命名 Prometheus 指标命名空间",
  "requires HTTPS and can lock out HTTP clients": "强制 HTTPS，可能导致 HTTP 客户端无法访问",
  "sends alerts to external systems": "将告警发送到外部系统",
  "sends approval events to external system": "将审批事件发送到外部系统",
  "sends traces to external collector": "将链路数据发送到外部 Collector",
  "starts or stops outbound alert delivery": "启用或停用外发告警",
};

export const RuntimeSettingsPanel = () => {
  const t = useTranslate();
  const notify = useNotify();
  const [settings, setSettings] = useState<SettingsView | null>(null);
  const [revisions, setRevisions] = useState<Revision[]>([]);
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [secretDraft, setSecretDraft] = useState<Record<string, string>>({});
  const [publicKey, setPublicKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [locale] = useLocaleState();
  const [confirmed, setConfirmed] = useState(false);
  const [rollbackNote, setRollbackNote] = useState("");
  const [conflict, setConflict] = useState("");
  const draftErrors = validateDraft(settings, draft);

  const highRiskChanges = Object.entries(draft).some(([key]) => {
    const [group, ...fieldParts] = key.split(".");
    return settings?.groups[group]?.[fieldParts.join(".")]?.risk_level === "high";
  });

  const load = async () => {
    try {
      const [settingsRes, revisionsRes, keyRes] = await Promise.all([
        unwrap(api.get<ApiEnvelope<SettingsView>>("/system/settings")),
        unwrap(api.get<ApiEnvelope<RevisionPage>>("/system/settings/revisions?page=1&page_size=20")),
        unwrap(api.get<ApiEnvelope<{ public_key: string }>>("/system/config/public-key")),
      ]);
      setSettings({ ...settingsRes });
      setRevisions(revisionsRes.items ?? []);
      setPublicKey(keyRes.public_key);
      setConfirmed(false);
      setRollbackNote("");
      setConflict("");
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("system.settings_load_fail"), { type: "error" });
    } finally {
      setLoaded(true);
    }
  };

  useEffect(() => { void load(); }, []);

  const save = async () => {
    if (!settings) return;
    if (Object.keys(draftErrors).length > 0) return;
    setBusy(true);
    try {
      if (Object.keys(draftErrors).length > 0 || (highRiskChanges && !confirmed)) return;
      await api.patch("/system/settings", {
        expected_revision: settings.revision,
        changes: Object.fromEntries(Object.entries(draft).map(([key, value]) => {
          const group = key.split(".")[0];
          const field = key.slice(group.length + 1);
          const item = settings.groups[group]?.[field];
          const type = item?.type ?? "";
          if (type === "bool") return [key, value === "true"];
          if (type === "webhook_list") {
            try {
              return [key, JSON.parse(value)];
            } catch {
              return [key, value];
            }
          }
          if (type.includes("list")) {
            return [key, value.split(/\r?\n/).map((entry) => entry.trim()).filter(Boolean)];
          }
          if (type === "number") return [key, Number(value)];
          return [key, value];
        })),
        note: t("system.settings_updated_by_ui"),
        confirmed: highRiskChanges,
      });
      setDraft({});
      setConfirmed(false);
      setConflict("");
      notify(t("system.settings_saved"), { type: "success" });
      await load();
    } catch (err) {
      if (err instanceof ApiError && (err.status === 409 || err.code === 40901)) {
        setConflict(t("system.settings_revision_conflict"));
        notify(t("system.settings_revision_conflict"), { type: "error" });
        await load();
        return;
      }
      notify(err instanceof ApiError ? err.displayMessage : t("system.settings_save_fail"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const saveSecret = async (key: string) => {
    if (!confirmed) {
      notify(t("system.settings_confirmation_required"), { type: "error" });
      return;
    }
    setBusy(true);
    try {
      const sealed = await sealRSAOAEPSHA256(publicKey, secretDraft[key]);
      await api.post(`/system/settings/secrets/${key}`, {
        secret: sealed,
        note: t("system.settings_secret_updated_by_ui"),
        confirmed: true,
      });
      setSecretDraft((current) => ({ ...current, [key]: "" }));
      notify(t("system.settings_secret_saved"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("system.settings_secret_save_fail"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const rollback = async (revisionId: string) => {
    if (!confirmed) {
      notify(t("system.settings_confirmation_required"), { type: "error" });
      return;
    }
    if (rollbackNote.trim().length < 8) {
      notify(t("system.settings_rollback_note_invalid"), { type: "error" });
      return;
    }
    setBusy(true);
    try {
      await api.post(`/system/settings/revisions/${revisionId}/rollback`, {
        confirmed: true,
        note: rollbackNote.trim(),
      });
      notify(t("system.settings_rolled_back"), { type: "success" });
      await load();
    } catch (err) {
      if (err instanceof ApiError && (err.status === 409 || err.code === 40901)) {
        setConflict(t("system.settings_revision_conflict"));
        notify(t("system.settings_revision_conflict"), { type: "error" });
        await load();
        return;
      }
      notify(err instanceof ApiError ? err.displayMessage : t("system.settings_save_fail"), { type: "error" });
    } finally {
      setBusy(false);
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
        <h3 className="text-sm font-medium">{t("system.settings_title")}</h3>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => void load()}>
            <RefreshCw className="size-4" aria-hidden="true" />
            {t("system.refresh")}
          </Button>
          <Button size="sm" onClick={() => void save()} disabled={busy || Object.keys(draft).length === 0 || Object.keys(draftErrors).length > 0}>
            {busy ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" aria-hidden="true" />}
            {t("system.settings_save")}
          </Button>
        </div>
      </div>
      {conflict ? <div className="mb-3 rounded border border-amber-300 bg-amber-50 p-2 text-sm text-amber-800">{conflict}</div> : null}
      <label className="mb-3 flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={confirmed}
          onChange={(event) => setConfirmed(event.target.checked)}
          aria-label={t("system.settings_confirm_high_risk")}
        />
        {t("system.settings_confirm_high_risk")}
      </label>
      <p className="mb-3 text-sm text-muted-foreground">
        {t("system.settings_revision")}: {settings?.revision ?? "-"} · {settings?.deployment_effective_state ?? "-"}
      </p>
      <div className="grid gap-3 md:grid-cols-3">
        {Object.entries(settings?.groups ?? {}).flatMap(([group, fields]) => Object.entries(fields).map(([field, item]) => {
          const key = `${group}.${field}`;
          const type = item.type ?? "string";
          const displayValue = () => {
            if (type === "bool") return item.effective ? "true" : "false";
            if (type === "webhook_list") return JSON.stringify(item.effective ?? [], null, 2);
            if (type.includes("list")) return Array.isArray(item.effective) ? item.effective.join("\n") : "";
            return String(item.effective ?? "");
          };
          const value = draft[key] ?? displayValue();
          return (
            <label key={key} className="space-y-1 text-sm">
              <span>{key}</span>
              {type === "bool" || type === "enum" ? (
                <select className={inputCls} disabled={!item.editable} value={value} onChange={(event) => setDraft({ ...draft, [key]: event.target.value })}>
                  {type === "bool"
                    ? ["true", "false"].map((entry) => <option key={entry} value={entry}>{entry}</option>)
                    : (item.allowed ?? []).map((entry) => <option key={entry} value={entry}>{entry}</option>)}
                </select>
              ) : type === "webhook_list" || type.includes("list") ? (
                <Textarea className={inputCls} disabled={!item.editable} value={value} onChange={(event) => setDraft({ ...draft, [key]: event.target.value })} />
              ) : type === "number" ? (
                <>
                  <Input
                    aria-label={key}
                    className={inputCls}
                    type="number"
                    min={item.min}
                    max={item.max}
                    step={numberStep(key)}
                    disabled={!item.editable}
                    value={value}
                    onChange={(event) => setDraft({ ...draft, [key]: event.target.value })}
                  />
                  {typeof item.min === "number" && typeof item.max === "number" && item.max > item.min && item.editable ? (
                    <input
                      type="range"
                      aria-label={`${key} ${t("system.settings_allowed_range")}`}
                      min={item.min}
                      max={item.max}
                      step={numberStep(key)}
                      value={clampNumber(value, item.min, item.max)}
                      onChange={(event) => setDraft({ ...draft, [key]: event.target.value })}
                    />
                  ) : null}
                </>
              ) : deploymentControlledKeys.has(key) ? (
                <Input
                  aria-label={key}
                  className={`${inputCls} font-mono`}
                  type="text"
                  readOnly
                  value={value}
                  onChange={(event) => setDraft({ ...draft, [key]: event.target.value })}
                />
              ) : (
                <Input
                  className={inputCls}
                  type="text"
                  disabled={!item.editable}
                  value={value}
                  onChange={(event) => setDraft({ ...draft, [key]: event.target.value })}
                />
              )}
              {draftErrors[key] ? (
                <span className="block text-xs text-red-600">
                  {t(draftErrors[key])}
                  {draftErrors[key] === "system.settings_number_out_of_range" && typeof item.min === "number" && typeof item.max === "number"
                    ? `: ${item.min} ~ ${item.max}`
                    : ""}
                </span>
              ) : type === "number" && typeof item.min === "number" && typeof item.max === "number" ? (
                <span className="block text-xs text-muted-foreground">
                  {t("system.settings_allowed_range")}: {item.min} ~ {item.max}
                </span>
              ) : null}
              <span className="text-xs text-muted-foreground">
                {item.source} · {t(item.database_override ? "system.settings_db_override" : "system.settings_no_db_override")}
                {item.restart_required ? ` · ${t("system.settings_restart_required")}` : ""}
                {` · ${item.effective_mode ?? ""}`}
                {deploymentControlledKeys.has(key) ? ` · ${t("system.settings_deployment_controlled")}` : ""}
              </span>
              {item.impact ? (
                <span className="block text-xs text-muted-foreground">
                  {locale === "zh" ? settingImpactZh[item.impact] ?? item.impact : item.impact}
                </span>
              ) : null}
            </label>
          );
        }))}
      </div>

      <div className="mt-6 rounded border p-3">
        <h4 className="mb-2 flex items-center gap-2 text-sm font-medium">
          <KeyRound className="size-4" aria-hidden="true" />
          {t("system.settings_secrets")}
        </h4>
        <div className="grid gap-3 md:grid-cols-3">
          {secretKeys.map((key) => {
            const reference = settings?.secret_references?.[key];
            return (
              <label key={key} className="space-y-1 text-sm">
                <span>{key}</span>
                <Input type="password" autoComplete="new-password" value={secretDraft[key] ?? ""} onChange={(event) => setSecretDraft({ ...secretDraft, [key]: event.target.value })} />
                <span className="text-xs text-muted-foreground">
                  {reference ? `${t("system.settings_secret_version")}: ${reference.version}` : t("system.settings_secret_missing")}
                </span>
                <Button size="sm" variant="outline" disabled={busy || !secretDraft[key]} onClick={() => void saveSecret(key)}>
                  <ShieldAlert className="size-4" aria-hidden="true" />
                  {t("system.settings_secret_save")}
                </Button>
              </label>
            );
          })}
        </div>
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <div>
          <h4 className="mb-2 text-sm font-medium">{t("system.settings_runtime_instances")}</h4>
          <ul className="space-y-1 text-sm">
            {(settings?.runtime_instances ?? []).map((instance) => (
              <li key={instance.runtime_instance_id} className="rounded border px-2 py-1">
                {instance.runtime_instance_id} · {instance.apply_status} · {t(`system.heartbeat_${instance.heartbeat_state || "live"}`)}
                {instance.apply_error ? <div className="text-xs text-red-600">{instance.apply_error}</div> : null}
              </li>
            ))}
          </ul>
        </div>
        <div>
          <h4 className="mb-2 text-sm font-medium">{t("system.settings_revisions")}</h4>
          <label className="mb-2 block text-sm">
            {t("system.settings_rollback_note")}
            <Textarea
              aria-label={t("system.settings_rollback_note")}
              className={`${inputCls} mt-1 h-16`}
              value={rollbackNote}
              onChange={(event) => setRollbackNote(event.target.value)}
            />
          </label>
          <ul className="space-y-1 text-sm">
            {revisions.map((revision) => (
              <li key={revision.id} className="flex items-center justify-between gap-2 rounded border px-2 py-1">
                <span>
                  #{revision.revision} · {revision.revision_status} · {revision.created_by}
                </span>
                <Button size="sm" variant="outline" disabled={busy || revision.id === settings?.desired_revision_id} onClick={() => void rollback(revision.id)}>
                  <Undo2 className="size-4" aria-hidden="true" />
                  {t("system.settings_rollback")}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  );
};
