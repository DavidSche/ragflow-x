import { useEffect, useState, type ReactNode } from "react";
import { LinkBase, useCanAccess, useNotify, useTranslate } from "ra-core";
import { Breadcrumb, BreadcrumbItem, BreadcrumbPage } from "@/components/admin/breadcrumb";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Loader2, RefreshCw, Save } from "lucide-react";
import { CheckCircle2, XCircle } from "lucide-react";
import { api, ApiError, unwrap } from "../../lib/api";
import { getSetupErrorMessage } from "../../lib/errors";
import { sealRSAOAEPSHA256 as seal } from "../../lib/seal";
import { NumericRangeField } from "@/components/NumericRangeField";
import { clampInteger } from "@/lib/numeric";
import { RagflowCapabilityPanel } from "./RagflowCapabilityPanel";
import { RagflowSyncPanel } from "./RagflowSyncPanel";
import { RuntimeSettingsPanel } from "./RuntimeSettingsPanel";

interface ConfigView {
  configured: boolean;
  database?: {
    driver: string; host: string; port: number; user: string; name: string;
    sslmode: string; dsn?: string; has_password: boolean;
  };
  ragflow?: {
    provider: string; base_url: string; timeout: number; max_conns: number;
    has_api_key: boolean;
  };
  redis?: {
    enabled: boolean; addr: string; username: string; db: number;
    pool_size: number; has_password: boolean;
  };
}

interface PreflightResp {
  db_up?: boolean;
  ragflow_up?: boolean;
}

interface Payload {
  database: { driver: string; host: string; port: number; user: string; name: string; sslmode: string; dsn: string; password_enc: string };
  ragflow: { provider: string; base_url: string; timeout: number; max_conns: number; api_key_enc: string };
  redis: { enabled: boolean; addr: string; username: string; db: number; pool_size: number; password_enc: string };
}

const KEEP = "__KEEP__";
const DRIVERS = ["postgres", "sqlite"] as const;

const fieldCls = "w-full rounded border bg-background px-2 py-1 text-sm";

interface DbState {
  driver: string; host: string; port: number; user: string; name: string; sslmode: string; dsn: string; has_password: boolean;
}

export const SystemConfig = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess: canReadRagflowSync, isPending: syncAccessPending } = useCanAccess({ resource: "ragflow-sync", action: "read" });

  const [publicKey, setPublicKey] = useState("");
  const [db, setDb] = useState<DbState>({ driver: "postgres", host: "", port: 5432, user: "", name: "", sslmode: "disable", dsn: "file:./ragflow-x.db?cache=shared", has_password: true });
  const [rg, setRg] = useState<NonNullable<ConfigView["ragflow"]>>({ provider: "http", base_url: "", timeout: 30, max_conns: 20, has_api_key: true });
  const [redis, setRedis] = useState<NonNullable<ConfigView["redis"]>>({ enabled: false, addr: "", username: "", db: 0, pool_size: 20, has_password: false });
  const [dbPassword, setDbPassword] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [redisPassword, setRedisPassword] = useState("");
  const [preflight, setPreflight] = useState<PreflightResp | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [activeTab, setActiveTab] = useState<"connection" | "ragflow-sync">("connection");

  const load = async () => {
    try {
      const [viewRes, keyRes] = await Promise.all([
        api.get<{ code: number; data: ConfigView }>("/system/config"),
        api.get<{ code: number; data: { public_key: string } }>("/system/config/public-key"),
      ]);
      const view = viewRes.data?.data;
      setPublicKey(keyRes.data?.data?.public_key ?? "");
      if (view?.database) {
        const d = view.database;
        setDb({
          driver: d.driver ?? "postgres",
          host: d.host ?? "", port: Number(d.port) || 5432, user: d.user ?? "",
          name: d.name ?? "", sslmode: d.sslmode || "disable",
          dsn: d.dsn ?? "file:./ragflow-x.db?cache=shared",
          has_password: d.has_password,
        });
      }
      if (view?.ragflow) setRg(view.ragflow as NonNullable<ConfigView["ragflow"]>);
      if (view?.redis) setRedis(view.redis as NonNullable<ConfigView["redis"]>);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("system.load_fail"), { type: "error" });
    } finally {
      setLoaded(true);
    }
  };

  useEffect(() => { void load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const buildPayload = async (): Promise<Payload> => ({
    database: {
      driver: db.driver,
      host: db.host,
      port: clampInteger(db.port, 1, 65535, 5432),
      user: db.user,
      name: db.name,
      sslmode: db.sslmode,
      dsn: db.dsn,
      password_enc: await seal(publicKey, dbPassword ? dbPassword : KEEP),
    },
    ragflow: {
      provider: rg.provider,
      base_url: rg.base_url,
      timeout: clampInteger(rg.timeout, 1, 600, 30),
      max_conns: clampInteger(rg.max_conns, 1, 1000, 20),
      api_key_enc: await seal(publicKey, apiKey ? apiKey : KEEP),
    },
    redis: {
      enabled: redis.enabled,
      addr: redis.addr,
      username: redis.username,
      db: clampInteger(redis.db, 0, 15, 0),
      pool_size: clampInteger(redis.pool_size, 1, 1000, 20),
      password_enc: await seal(publicKey, redisPassword ? redisPassword : KEEP),
    },
  });

  const test = async () => {
    if (!publicKey) { notify(t("system.key_loading"), { type: "warning" }); return; }
    setTesting(true);
    setPreflight(null);
    try {
      const payload = await buildPayload();
      const res = await unwrap<PreflightResp>(api.post("/system/config/preflight", payload));
      setPreflight(res);
    } catch (err) {
      notify(
        err instanceof ApiError ? getSetupErrorMessage(err.code, err.message) : t("system.test_fail"),
        { type: "error" },
      );
    } finally {
      setTesting(false);
    }
  };

  const save = async () => {
    if (!publicKey) { notify(t("system.key_loading"), { type: "warning" }); return; }
    setSaving(true);
    try {
      const payload = await buildPayload();
      await unwrap(api.post("/system/config/apply", payload));
      notify(t("system.saved"), { type: "success" });
      setDbPassword(""); setApiKey(""); setRedisPassword("");
      setPreflight(null);
      await load();
    } catch (err) {
      notify(
        err instanceof ApiError ? getSetupErrorMessage(err.code, err.message) : t("system.save_fail"),
        { type: "error" },
      );
    } finally {
      setSaving(false);
    }
  };

  if (!loaded) {
    return (
      <div className="flex items-center justify-center gap-2 p-10 text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
        <span>{t("ra.page.loading")}</span>
      </div>
    );
  }

  const isSQLite = db.driver === "sqlite";

  return (
    <div className="p-6">
      <Breadcrumb>
        <BreadcrumbItem><LinkBase to="/">{t("ra.page.dashboard")}</LinkBase></BreadcrumbItem>
        <BreadcrumbPage>{t("system.title")}</BreadcrumbPage>
      </Breadcrumb>
      <div className="my-2 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-2xl font-bold tracking-tight">{t("system.title")}</h2>
        <Button variant="outline" onClick={() => void load()} disabled={saving}>
          <RefreshCw className="size-4" aria-hidden="true" />
          {t("system.refresh")}
        </Button>
      </div>
      <p className="mb-4 max-w-2xl text-sm text-muted-foreground">{t("system.subtitle")}</p>

      <div className="mb-4 border-b" role="tablist" aria-label={t("system.title")}>
        <div className="flex gap-1">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "connection"}
            className={`rounded-t px-3 py-2 text-sm font-medium ${activeTab === "connection" ? "border-b-2 border-primary text-foreground" : "text-muted-foreground hover:text-foreground"}`}
            onClick={() => setActiveTab("connection")}
          >
            {t("system.tab_connection")}
          </button>
          {!syncAccessPending && canReadRagflowSync && (
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === "ragflow-sync"}
              className={`rounded-t px-3 py-2 text-sm font-medium ${activeTab === "ragflow-sync" ? "border-b-2 border-primary text-foreground" : "text-muted-foreground hover:text-foreground"}`}
              onClick={() => setActiveTab("ragflow-sync")}
            >
              {t("system.tab_ragflow_sync")}
            </button>
          )}
        </div>
      </div>

      {activeTab === "connection" && (
        <>
      <div className="grid gap-4 lg:grid-cols-2">
        <section className="rounded border p-4">
          <h3 className="mb-3 text-sm font-medium">{t("system.database")}</h3>
          <div className="space-y-3">
            <Field label={t("system.driver")}>
              <select className={fieldCls} value={db.driver} onChange={(e) => setDb({ ...db, driver: e.target.value })}>
                {DRIVERS.map((d) => (
                  <option key={d} value={d}>{d === "postgres" ? "PostgreSQL" : "SQLite"}</option>
                ))}
              </select>
            </Field>

            {isSQLite ? (
              <>
                <Field label={`${t("system.dsn")}${db.has_password ? "" : ""}`}>
                  <Input className={fieldCls} placeholder="file:./ragflow-x.db?cache=shared" value={db.dsn} onChange={(e) => setDb({ ...db, dsn: e.target.value })} />
                </Field>
                <p className="text-xs text-muted-foreground">{t("system.sqlite_note")}</p>
              </>
            ) : (
              <>
                <div className="grid grid-cols-2 gap-3">
                  <NumericRangeField
                    label={t("system.port")}
                    value={db.port}
                    min={1}
                    max={65535}
                    className="w-full"
                    onChange={(next) => setDb({ ...db, port: next ?? 5432 })}
                  />
                </div>
                <Field label={t("system.host")}>
                  <Input className={fieldCls} value={db.host} onChange={(e) => setDb({ ...db, host: e.target.value })} />
                </Field>
                <div className="grid grid-cols-2 gap-3">
                  <Field label={t("system.user")}>
                    <Input className={fieldCls} value={db.user} onChange={(e) => setDb({ ...db, user: e.target.value })} />
                  </Field>
                  <Field label={t("system.dbname")}>
                    <Input className={fieldCls} value={db.name} onChange={(e) => setDb({ ...db, name: e.target.value })} />
                  </Field>
                </div>
                <Field label={db.has_password ? `${t("system.password")}（${t("system.keep_hint")}）` : t("system.password")}>
                  <Input className={fieldCls} type="password" placeholder={db.has_password ? t("system.keep_hint") : ""} value={dbPassword} onChange={(e) => setDbPassword(e.target.value)} />
                </Field>
                <Field label={t("system.sslmode")}>
                  <select className={fieldCls} value={db.sslmode} onChange={(e) => setDb({ ...db, sslmode: e.target.value })}>
                    <option value="disable">disable</option>
                    <option value="require">require</option>
                    <option value="verify-ca">verify-ca</option>
                    <option value="verify-full">verify-full</option>
                  </select>
                </Field>
              </>
            )}
          </div>
        </section>

        <section className="rounded border p-4">
          <h3 className="mb-3 text-sm font-medium">{t("system.ragflow")}</h3>
          <div className="space-y-3">
            <Field label={t("system.base_url")}>
              <Input className={fieldCls} value={rg.base_url} onChange={(e) => setRg({ ...rg, base_url: e.target.value })} />
            </Field>
            <Field label={rg.has_api_key ? `${t("system.api_key")}（${t("system.keep_hint")}）` : t("system.api_key")}>
              <Input className={fieldCls} type="password" placeholder={rg.has_api_key ? t("system.keep_hint") : ""} value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <NumericRangeField
                label={t("system.timeout")}
                value={rg.timeout}
                min={1}
                max={600}
                unit="s"
                onChange={(next) => setRg({ ...rg, timeout: next ?? 30 })}
              />
              <NumericRangeField
                label={t("system.max_conns")}
                value={rg.max_conns}
                min={1}
                max={1000}
                onChange={(next) => setRg({ ...rg, max_conns: next ?? 20 })}
              />
            </div>
          </div>
        </section>
      </div>

      <section className="mt-4 rounded border p-4">
        <label className="mb-3 flex items-center gap-2 text-sm">
          <input type="checkbox" className="size-4" checked={redis.enabled} onChange={(e) => setRedis({ ...redis, enabled: e.target.checked })} />
          {t("system.enable_redis")}
        </label>
        {redis.enabled && (
          <div className="grid gap-3 md:grid-cols-3">
            <Field label={t("system.redis_addr")}>
              <Input className={fieldCls} value={redis.addr} onChange={(e) => setRedis({ ...redis, addr: e.target.value })} />
            </Field>
            <Field label={t("system.redis_username")}>
              <Input className={fieldCls} value={redis.username} onChange={(e) => setRedis({ ...redis, username: e.target.value })} />
            </Field>
            <NumericRangeField
              label={t("system.redis_db")}
              value={redis.db}
              min={0}
              max={15}
              onChange={(next) => setRedis({ ...redis, db: next ?? 0 })}
            />
            <Field label={redis.has_password ? `${t("system.redis_password")}（${t("system.keep_hint")}）` : t("system.redis_password")}>
              <Input className={fieldCls} type="password" placeholder={redis.has_password ? t("system.keep_hint") : ""} value={redisPassword} onChange={(e) => setRedisPassword(e.target.value)} />
            </Field>
          </div>
        )}
      </section>

      <RuntimeSettingsPanel />

      {preflight && (
        <div className="mt-4 space-y-1 text-sm">
          {preflight.db_up ? (
            <div className="flex items-center gap-2 text-emerald-600"><CheckCircle2 className="size-4" /> {t("system.db_ok")}</div>
          ) : (
            <div className="flex items-center gap-2 text-red-600"><XCircle className="size-4" /> {t("system.db_fail")}</div>
          )}
          {preflight.ragflow_up ? (
            <div className="flex items-center gap-2 text-emerald-600"><CheckCircle2 className="size-4" /> {t("system.ragflow_ok")}</div>
          ) : (
            <div className="flex items-center gap-2 text-red-600"><XCircle className="size-4" /> {t("system.ragflow_fail")}</div>
          )}
        </div>
      )}

      <div className="mt-4 flex justify-end gap-2">
        <Button variant="outline" onClick={() => void test()} disabled={testing || !publicKey}>
          {testing ? <Loader2 className="size-4 animate-spin" /> : t("system.test")}
        </Button>
        <Button onClick={() => void save()} disabled={saving || !publicKey}>
          {saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" aria-hidden="true" />}
          {t("system.save")}
        </Button>
      </div>
        </>
      )}

      {activeTab === "ragflow-sync" && !syncAccessPending && canReadRagflowSync && (
        <>
          <RagflowSyncPanel />
          <RagflowCapabilityPanel />
        </>
      )}
    </div>
  );
};

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="space-y-1 text-sm">
      <span className="block">{label}</span>
      {children}
    </label>
  );
}
