import { useEffect, useMemo, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, ApiError } from "@/lib/api";
import {
  ApprovalHoldError,
  approvalHoldFromResponse,
  approvalIdempotencyKey,
  type ApprovalHold,
} from "@/lib/approval-hold";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";

type FieldSpec = {
  key: string;
  label: string;
  placeholder?: string;
  secret?: boolean;
  required?: boolean;
  default?: unknown;
};

type ProviderMeta = {
  type: string;
  label: string;
  tier?: string;
  closure?: string;
  target_version?: string;
  spec_fields?: FieldSpec[];
  cred_fields?: FieldSpec[];
};

type DatasetOption = { id: string; name?: string };

type Envelope<T> = { code: number; message?: string; data: T };

const STEP_COUNT = 4;

interface ConnectorWizardDialogProps {
  onCreated?: () => void;
}

/**
 * ConnectorWizardDialog – Step1 provider → Step2 spec/credential + test →
 * Step3 schedule/target → Step4 confirm (doc/225 §11.1). Provider form fields
 * come from `/connectors/providers/meta`, so adding a provider needs no UI
 * change; credentials are transmitted once and never read back.
 */
export function ConnectorWizardDialog({ onCreated }: ConnectorWizardDialogProps) {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState(1);
  const [providers, setProviders] = useState<ProviderMeta[]>([]);
  const [provider, setProvider] = useState("");
  const [spec, setSpec] = useState<Record<string, string>>({});
  const [cred, setCred] = useState<Record<string, string>>({});
  const [name, setName] = useState("");
  const [targetDataset, setTargetDataset] = useState("");
  const [datasets, setDatasets] = useState<DatasetOption[]>([]);
  const [scheduleKind, setScheduleKind] = useState("manual");
  const [scheduleValue, setScheduleValue] = useState(30);
  const [sensitive, setSensitive] = useState(false);
  const [testing, setTesting] = useState(false);
  const [tested, setTested] = useState(false);
  const [saving, setSaving] = useState(false);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const { canAccess: canManage } = useCanAccess({ resource: "connector", action: "manage" });

  const meta = useMemo(() => providers.find((p) => p.type === provider), [providers, provider]);

  useEffect(() => {
    if (!open) return;
    void (async () => {
      try {
        const [metaRes, dsRes] = await Promise.all([
          api.get<Envelope<ProviderMeta[]>>("/connectors/providers/meta"),
          api.get<Envelope<DatasetOption[]>>("/datasets"),
        ]);
        setProviders(metaRes.data.data ?? []);
        setDatasets(dsRes.data.data ?? []);
      } catch (err) {
        notify(err instanceof ApiError ? err.displayMessage : t("connectors.wizard.load_failed"), {
          type: "error",
        });
      }
    })();
  }, [open, notify, t]);

  const reset = () => {
    setStep(1);
    setProvider("");
    setSpec({});
    setCred({});
    setName("");
    setTargetDataset("");
    setScheduleKind("manual");
    setScheduleValue(30);
    setSensitive(false);
    setTested(false);
  };

  const close = (next: boolean) => {
    setOpen(next);
    if (!next) reset();
  };

  const missingRequired = useMemo(() => {
    if (!meta) return true;
    return (meta.spec_fields ?? []).some((f) => f.required && !String(spec[f.key] ?? "").trim());
  }, [meta, spec]);

  const test = async () => {
    setTesting(true);
    try {
      await api.post("/connectors/test", {
        provider,
        spec: coerceSpec(meta, spec),
        credential: cred,
      });
      setTested(true);
      notify(t("connectors.wizard.test_ok"), { type: "success" });
    } catch (err) {
      setTested(false);
      notify(err instanceof ApiError ? err.displayMessage : t("connectors.wizard.test_failed"), {
        type: "error",
      });
    } finally {
      setTesting(false);
    }
  };

  const create = async () => {
    setSaving(true);
    try {
      const res = await api.post(
        "/connectors",
        {
          name,
          provider,
          target_dataset_id: targetDataset,
          spec: coerceSpec(meta, spec),
          credential: cred,
          schedule:
            scheduleKind === "manual"
              ? { kind: "manual" }
              : scheduleKind === "interval"
                ? { kind: "interval", minutes: scheduleValue }
                : { kind: "daily", hhmm: minutesToHHMM(scheduleValue) },
          // doc/225 F7: daily HHMM is wall-clock in the display offset.
          schedule_tz_offset_min: 480,
          sensitive,
        },
        { headers: { "Idempotency-Key": approvalIdempotencyKey("connector-create") } },
      );
      const responseHold = approvalHoldFromResponse(res);
      if (responseHold) {
        setHold(responseHold);
        close(false);
        return;
      }
      notify(t("connectors.wizard.created"), { type: "success" });
      await qc.invalidateQueries({ queryKey: ["connectors"] });
      onCreated?.();
      close(false);
    } catch (err) {
      if (err instanceof ApprovalHoldError) {
        setHold(err.hold);
        close(false);
        return;
      }
      notify(err instanceof ApiError ? err.displayMessage : t("connectors.wizard.create_failed"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  if (!canManage) return null;

  if (hold) {
    return <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />;
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <Plus /> {t("connectors.create")}
      </Button>
      <Dialog open={open} onOpenChange={close}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {t("connectors.wizard.title")} · {t("connectors.wizard.step", { step, total: STEP_COUNT })}
            </DialogTitle>
          </DialogHeader>

          {step === 1 ? (
            <div className="space-y-3">
              <p className="text-sm text-muted-foreground">{t("connectors.wizard.provider_hint")}</p>
              <div className="grid gap-2 sm:grid-cols-3">
                {providers.map((p) => (
                  <button
                    key={p.type}
                    type="button"
                    aria-pressed={provider === p.type}
                    onClick={() => {
                      setProvider(p.type);
                      setSpec({});
                      setCred({});
                      setTested(false);
                    }}
                    className={`rounded border p-3 text-left text-sm hover:border-primary ${
                      provider === p.type ? "border-primary bg-primary/5" : ""
                    }`}
                  >
                    <span className="font-medium">{p.label || p.type}</span>
                    <span className="mt-1 block text-xs text-muted-foreground">{p.type}</span>
                    {p.tier ? (
                      <span className="mt-1 block text-xs text-muted-foreground">
                        Tier {p.tier}
                        {p.closure ? ` · ${p.closure}` : ""}
                        {p.target_version ? ` · ${p.target_version}` : ""}
                      </span>
                    ) : null}
                  </button>
                ))}
              </div>
            </div>
          ) : null}

          {step === 2 && meta ? (
            <div className="space-y-4">
              <div className="grid gap-3 sm:grid-cols-2">
                {(meta.spec_fields ?? []).map((f) => (
                  <label key={f.key} className="space-y-1 text-sm">
                    <span className="font-medium">
                      {f.label || f.key}
                      {f.required ? <span className="text-destructive"> *</span> : null}
                    </span>
                    <Input
                      value={spec[f.key] ?? ""}
                      placeholder={f.placeholder}
                      aria-required={f.required ? "true" : undefined}
                      onChange={(e) => {
                        setSpec((prev) => ({ ...prev, [f.key]: e.target.value }));
                        setTested(false);
                      }}
                    />
                  </label>
                ))}
              </div>
              {(meta.cred_fields ?? []).length > 0 ? (
                <div className="grid gap-3 sm:grid-cols-2">
                  {(meta.cred_fields ?? []).map((f) => (
                    <label key={f.key} className="space-y-1 text-sm">
                      <span className="font-medium">
                        {f.label || f.key}
                        {f.required ? <span className="text-destructive"> *</span> : null}
                      </span>
                      <Input
                        type={f.secret ? "password" : "text"}
                        autoComplete="off"
                        value={cred[f.key] ?? ""}
                        placeholder={f.placeholder}
                        onChange={(e) => {
                          setCred((prev) => ({ ...prev, [f.key]: e.target.value }));
                          setTested(false);
                        }}
                      />
                    </label>
                  ))}
                </div>
              ) : null}
              <div className="flex items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void test()}
                  disabled={testing || missingRequired}
                >
                  {testing ? t("connectors.wizard.testing") : t("connectors.wizard.test")}
                </Button>
                {tested ? <Badge variant="default">{t("connectors.wizard.tested")}</Badge> : null}
              </div>
            </div>
          ) : null}

          {step === 3 ? (
            <div className="space-y-4">
              <label className="block space-y-1 text-sm">
                <span className="font-medium">{t("connectors.name")} *</span>
                <Input value={name} onChange={(e) => setName(e.target.value)} />
              </label>
              <label className="block space-y-1 text-sm">
                <span className="font-medium">{t("connectors.target_dataset")} *</span>
                <select
                  className="h-9 w-full rounded-md border bg-background px-3 text-sm"
                  value={targetDataset}
                  onChange={(e) => setTargetDataset(e.target.value)}
                >
                  <option value="">{t("connectors.wizard.pick_dataset")}</option>
                  {datasets.map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.name || d.id}
                    </option>
                  ))}
                </select>
              </label>
              <label className="block space-y-1 text-sm">
                <span className="font-medium">{t("connectors.schedule")}</span>
                <select
                  className="h-9 w-full rounded-md border bg-background px-3 text-sm"
                  value={scheduleKind}
                  onChange={(e) => setScheduleKind(e.target.value)}
                >
                  <option value="manual">{t("connectors.schedule_manual")}</option>
                  <option value="interval">{t("connectors.schedule_interval")}</option>
                  <option value="daily">{t("connectors.schedule_daily")}</option>
                </select>
              </label>
              {scheduleKind === "interval" ? (
                <label className="block space-y-1 text-sm">
                  <span className="font-medium">{t("connectors.schedule_minutes")}</span>
                  <Input
                    type="number"
                    min={5}
                    value={scheduleValue}
                    onChange={(e) => setScheduleValue(Number(e.target.value))}
                  />
                </label>
              ) : null}
              {scheduleKind === "daily" ? (
                <label className="block space-y-1 text-sm">
                  <span className="font-medium">{t("connectors.schedule_time_local")}</span>
                  <Input
                    type="time"
                    value={minutesToHHMM(scheduleValue)}
                    onChange={(e) => setScheduleValue(hhmmToMinutes(e.target.value))}
                  />
                </label>
              ) : null}
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
                {t("connectors.sensitive")}
              </label>
            </div>
          ) : null}

          {step === 4 ? (
            <dl className="grid gap-2 text-sm sm:grid-cols-2">
              <Summary label={t("connectors.name")} value={name} />
              <Summary label={t("connectors.provider")} value={meta?.label || provider} />
              <Summary
                label={t("connectors.target_dataset")}
                value={datasets.find((d) => d.id === targetDataset)?.name || targetDataset}
              />
              <Summary label={t("connectors.schedule")} value={scheduleKind} />
              <Summary
                label={t("connectors.has_credential")}
                value={Object.keys(cred).length > 0 ? t("connectors.wizard.yes") : t("connectors.wizard.no")}
              />
            </dl>
          ) : null}

          <DialogFooter className="flex items-center justify-between gap-2">
            <div className="text-xs text-muted-foreground">{t("connectors.wizard.readonly_hint")}</div>
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => (step === 1 ? close(false) : setStep(step - 1))}>
                {step === 1 ? t("connectors.wizard.cancel") : t("connectors.wizard.back")}
              </Button>
              {step < STEP_COUNT ? (
                <Button
                  onClick={() => setStep(step + 1)}
                  disabled={
                    (step === 1 && !provider) ||
                    (step === 2 && (!tested || missingRequired)) ||
                    (step === 3 && (!name.trim() || !targetDataset))
                  }
                >
                  {t("connectors.wizard.next")}
                </Button>
              ) : (
                <Button onClick={() => void create()} disabled={saving}>
                  {saving ? t("connectors.wizard.saving") : t("connectors.create")}
                </Button>
              )}
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Summary({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border p-2">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="break-words font-medium">{value || "—"}</dd>
    </div>
  );
}

function coerceSpec(meta: ProviderMeta | undefined, values: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of meta?.spec_fields ?? []) {
    const raw = values[f.key];
    if (raw === undefined || raw === "") continue;
    out[f.key] = typeof f.default === "number" && !Number.isNaN(Number(raw)) ? Number(raw) : raw;
  }
  return out;
}

function minutesToHHMM(minutes: number): string {
  const m = Math.max(0, Math.min(1439, minutes || 0));
  return `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
}

function hhmmToMinutes(value: string): number {
  const [h, m] = value.split(":").map(Number);
  if (Number.isNaN(h) || Number.isNaN(m)) return 0;
  return h * 60 + m;
}
