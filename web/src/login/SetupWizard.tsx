import { useEffect, useState } from "react";
import type { SubmitHandler, FieldValues } from "react-hook-form";
import { Form, minLength, required, useNotify } from "ra-core";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/admin/text-input";
import { Shell, CheckCircle2, XCircle } from "lucide-react";
import { api, unwrap, ApiError } from "../lib/api";
import { getSetupErrorMessage } from "../lib/errors";
import { optionalWarning } from "../lib/optional-error";
import { sealRSAOAEPSHA256 as seal } from "../lib/seal";
import { wantsAdminOnly } from "./wizardMode";

const DEFAULT_PORT = 5432;

interface PublicKeyResp {
  public_key: string;
}

interface Status {
	configured: boolean;
	initialized: boolean;
	db_up: boolean;
	/** "engine": full router is serving (admin-only step works); "wizard": bootstrap router. */
	mode?: "engine" | "wizard";
}

interface PreflightResult {
  db_up: boolean;
  ragflow_up: boolean;
  configured?: boolean;
  /** Driver message and hint key for a failed probe (setup.ProbeHint*). */
  db_error?: string;
  db_hint?: string;
  ragflow_error?: string;
}

/** Turns a probe hint key into the next action an operator can actually take. */
const DB_HINTS: Record<string, string> = {
  database_missing:
    "目标数据库不存在。先在 PostgreSQL 里建库（CREATE DATABASE ragflow_x OWNER <user>;），或把「数据库名」改成已存在的库。",
  auth_failed: "用户名或密码不对。用同一组凭据执行 psql \"host=… user=… dbname=…\" 验证后再填。",
  unreachable: "连不上这个地址和端口。确认 PostgreSQL 已启动、监听 0.0.0.0 或本机地址，且防火墙放行该端口。",
  not_writable:
    "SQLite 数据库文件不可写。把 DSN 改成绝对路径（如 /data/ragflow-x/ragflow-x.db），并确认运行账号对该目录有写权限。",
  unknown: "请查看服务器日志中 database 相关的那一行，里面有驱动返回的原始错误。",
};

const hintFor = (key?: string): string | undefined =>
  key ? (DB_HINTS[key] ?? DB_HINTS.unknown) : undefined;

interface ApplyPayload {
  database: {
    driver: string;
    host: string;
    port: number;
    user: string;
    name: string;
    sslmode: string;
    dsn: string;
    password_enc: string;
  };
  ragflow: {
    provider: string;
    base_url: string;
    timeout: number;
    max_conns: number;
    api_key_enc: string;
  };
  admin: { username: string; password_enc: string };
  redis: {
	    enabled: boolean;
	    addr: string;
	    username: string;
	    db: number;
	    pool_size: number;
	    password_enc: string;
  };
}

interface SetupWizardProps {
  onInitialized: () => void;
}

/** First-run wizard: encrypted connection config + first admin creation. */
export const SetupWizard = ({ onInitialized }: SetupWizardProps) => {
  const notify = useNotify();
	const [publicKey, setPublicKey] = useState("");
	const [configured, setConfigured] = useState(false);
	const [dbUp, setDbUp] = useState(false);
	const [mode, setMode] = useState<"engine" | "wizard" | "">("");
	const [busy, setBusy] = useState(false);
	const [redisEnabled, setRedisEnabled] = useState(false);
	const [driver, setDriver] = useState("postgres");
	const [sqliteDSN, setSqliteDSN] = useState("file:./ragflow-x.db?cache=shared");
  const [preflight, setPreflight] = useState<PreflightResult | null>(null);
  const [payload, setPayload] = useState<ApplyPayload | null>(null);

  useEffect(() => {
    unwrap<PublicKeyResp>(api.get("/system/setup/public-key"))
      .then((r) => setPublicKey(r.public_key))
      .catch((error) => optionalWarning(error, "初始化公钥加载失败"));
		unwrap<Status>(api.get("/system/setup/status"))
			.then((s) => {
				setConfigured(s.configured);
				setDbUp(s.db_up);
				setMode(s.mode ?? "");
			})
			.catch(() => setConfigured(false));
  }, []);

  const buildPayload = async (values: FieldValues): Promise<ApplyPayload> => {
    const passEnc = await seal(publicKey, String(values.db_password ?? ""));
	const apiEnc = await seal(publicKey, String(values.api_key ?? ""));
	const adminEnc = await seal(publicKey, String(values.admin_password ?? ""));
	const port = Number(values.port) || DEFAULT_PORT;
	const redisPw = redisEnabled
	  ? await seal(publicKey, String(values.redis_password ?? ""))
	  : "";
	return {
	  database: {
	    driver,
	    host: driver === "sqlite" ? "" : String(values.host ?? ""),
	    port,
	    user: String(values.user ?? ""),
	    name: String(values.name ?? ""),
	    sslmode: String(values.sslmode ?? "disable"),
	    dsn: driver === "sqlite" ? sqliteDSN : "",
	    password_enc: passEnc,
	  },
	  ragflow: {
	    provider: "http",
	    base_url: String(values.base_url ?? ""),
	    timeout: 30,
	    max_conns: 20,
	    api_key_enc: apiEnc,
	  },
	  admin: {
	    username: String(values.admin_username ?? ""),
	    password_enc: adminEnc,
	  },
	  redis: {
		    enabled: redisEnabled,
		    addr: String(values.redis_addr ?? ""),
		    username: String(values.redis_username ?? ""),
		    db: Number(values.redis_db) || 0,
		    pool_size: 20,
		    password_enc: redisPw,
		  },
	};
	};

  const handlePreflight: SubmitHandler<FieldValues> = async (values) => {
    if (!publicKey) {
      notify("安全密钥加载中，请稍候", { type: "warning" });
      return;
    }
    setBusy(true);
    setPreflight(null);
    try {
      const p = await buildPayload(values);
      setPayload(p);
      const res = await unwrap<PreflightResult>(api.post("/system/setup/preflight", p));
      setPreflight(res);
    } catch (err) {
      notify(
        err instanceof ApiError ? getSetupErrorMessage(err.code, err.message) : "连接测试失败",
        { type: "error" },
      );
    } finally {
      setBusy(false);
    }
  };

  const handleApply = async () => {
    if (!payload) return;
    setBusy(true);
    try {
      await unwrap(api.post("/system/setup/apply", payload));
      notify("系统初始化完成，请登录", { type: "success" });
      onInitialized();
    } catch (err) {
      notify(
        err instanceof ApiError ? getSetupErrorMessage(err.code, err.message) : "应用配置失败",
        { type: "error" },
      );
      setBusy(false);
    }
  };

  const handleAdminOnly: SubmitHandler<FieldValues> = async (values) => {
    if (!publicKey) {
      notify("安全密钥加载中，请稍候", { type: "warning" });
      return;
    }
		setBusy(true);
		try {
			await unwrap(
				api.post("/system/setup/admin", {
				username: values.admin_username,
				password_enc: await seal(publicKey, String(values.admin_password ?? "")),
			}),
		);
      notify("初始化完成，请登录", { type: "success" });
      onInitialized();
    } catch (err) {
      notify(
        err instanceof ApiError ? getSetupErrorMessage(err.code, err.message) : "创建管理员失败",
        { type: "error" },
      );
      setBusy(false);
    }
  };

  const ready = publicKey !== "" && !busy;
  // The admin-only step posts to /system/setup/admin, which only the full
  // application router mounts. `mode` says who is answering; the old
  // "configured && dbUp" guess was true in bootstrap mode too, so a launcher
  // trial (SQLite up, engine not configured yet) submitted a form that 404ed.
  const adminOnly = wantsAdminOnly({mode, configured, db_up: dbUp});

  return (
    <div className="min-h-screen flex items-center justify-center px-6">
      <div className="w-full max-w-md space-y-6">
        <div className="flex flex-col items-center gap-3 text-center">
          <div className="flex items-center gap-2 text-lg font-semibold">
            <Shell className="size-6" />
            RAGFlow-X
          </div>
          <p className="text-sm text-muted-foreground">首次启动初始化</p>
        </div>

        {adminOnly ? (
          <div className="rounded-lg border p-4 space-y-3">
            <h2 className="text-sm font-semibold">创建首个管理员</h2>
            <Form className="space-y-3" onSubmit={handleAdminOnly}>
              <TextInput label="用户名" source="admin_username" validate={required()} autoFocus />
              <TextInput label="密码" source="admin_password" type="password" validate={[required(), minLength(8)]} />
              <Button type="submit" className="w-full cursor-pointer" disabled={!ready}>
                {busy ? "提交中..." : "创建并完成初始化"}
              </Button>
            </Form>
          </div>
        ) : (
          <div className="rounded-lg border p-4 space-y-3">
            <h2 className="text-sm font-semibold">数据库与引擎配置</h2>
            <Form className="space-y-3" onSubmit={handlePreflight}>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1">
                  <label className="text-sm">数据库类型</label>
                  <select
                    className="w-full rounded border bg-background px-2 py-1 text-sm"
                    value={driver}
                    onChange={(e) => setDriver(e.target.value)}
                  >
                    <option value="postgres">PostgreSQL</option>
                    <option value="sqlite">SQLite</option>
                  </select>
                </div>
                {driver === "postgres" ? (
                  <TextInput label="端口" source="port" defaultValue={String(DEFAULT_PORT)} />
                ) : (
                  <div className="space-y-1">
                    <label className="text-sm">数据库文件（DSN）</label>
                    <input
                      className="w-full rounded border bg-background px-2 py-1 text-sm"
                      value={sqliteDSN}
                      onChange={(e) => setSqliteDSN(e.target.value)}
                    />
                  </div>
                )}
              </div>
              {driver === "sqlite" ? (
                <p className="text-xs text-muted-foreground">SQLite 使用数据库文件（DSN）路径，文件需可写。</p>
              ) : (
                <>
                  <TextInput label="数据库地址" source="host" validate={required()} />
                  <TextInput label="用户名" source="user" validate={required()} />
                  <TextInput label="密码" source="db_password" type="password" validate={required()} />
                  <TextInput label="数据库名" source="name" validate={required()} />
                </>
              )}
              <TextInput label="引擎地址" source="base_url" validate={required()} />
              <TextInput label="引擎 API Key" source="api_key" type="password" validate={required()} />
              <div className="pt-2 space-y-3 border-t">
                <label className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    checked={redisEnabled}
                    onChange={(e) => setRedisEnabled(e.target.checked)}
                    className="size-4"
                  />
                  启用 Redis 分布式限流（可选，多副本部署）
                </label>
                {redisEnabled && (
                  <div className="space-y-3">
						<TextInput label="Redis 地址" source="redis_addr" placeholder="localhost:6379" />
						<TextInput label="Redis 密码" source="redis_password" type="password" />
						<TextInput label="Redis 用户名" source="redis_username" placeholder="default" />
						<TextInput label="DB" source="redis_db" defaultValue="0" />
                  </div>
                )}
              </div>
              <TextInput label="管理员用户名" source="admin_username" validate={required()} />
              <TextInput label="管理员密码" source="admin_password" type="password" validate={[required(), minLength(8)]} />
				<Button type="submit" className="w-full cursor-pointer" disabled={!ready}>
					{busy ? "检测中..." : "测试连接"}
				</Button>
			</Form>


			{preflight && payload && (
              <div className="space-y-3">
                <div className="space-y-1 text-sm">
                  {preflight.db_up ? (
                    <div className="flex items-center gap-2 text-emerald-600">
                      <CheckCircle2 className="size-4" /> 数据库连接正常
                    </div>
                  ) : (
                    <div className="flex items-start gap-2 text-red-600">
                      <XCircle className="mt-0.5 size-4 shrink-0" />
                      <div className="space-y-1">
                        <p>数据库不可达</p>
                        {hintFor(preflight.db_hint) && (
                          <p className="text-xs text-muted-foreground">{hintFor(preflight.db_hint)}</p>
                        )}
                        {preflight.db_error && (
                          <p className="max-h-24 overflow-auto whitespace-pre-wrap break-all rounded bg-muted px-2 py-1 font-mono text-[11px] leading-relaxed text-muted-foreground">
                            {preflight.db_error}
                          </p>
                        )}
                      </div>
                    </div>
                  )}
                  {preflight.ragflow_up ? (
                    <div className="flex items-center gap-2 text-emerald-600">
                      <CheckCircle2 className="size-4" /> 引擎连接正常
                    </div>
                  ) : (
                    <div className="flex items-start gap-2 text-red-600">
                      <XCircle className="mt-0.5 size-4 shrink-0" />
                      <div className="space-y-1">
                        <p>引擎不可达</p>
                        <p className="text-xs text-muted-foreground">
                          确认 RAGFlow 已启动，且「引擎地址」能被本机访问（如 http://127.0.0.1:9380）。
                        </p>
                        {preflight.ragflow_error && (
                          <p className="max-h-24 overflow-auto whitespace-pre-wrap break-all rounded bg-muted px-2 py-1 font-mono text-[11px] leading-relaxed text-muted-foreground">
                            {preflight.ragflow_error}
                          </p>
                        )}
                      </div>
                    </div>
                  )}
                </div>
                <Button
                  type="button"
                  className="w-full cursor-pointer"
                  disabled={busy || !preflight.db_up}
                  onClick={handleApply}
                >
                  {busy ? "应用配置中..." : "确认应用配置并完成初始化"}
                </Button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
};

