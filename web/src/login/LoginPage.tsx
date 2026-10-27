import { useEffect, useState } from "react";
import type { SubmitHandler, FieldValues } from "react-hook-form";
import { Form, required, useLogin, useNotify } from "ra-core";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/admin/text-input";
import { Notification } from "@/components/admin/notification";
import { Eye, EyeOff, Shell } from "lucide-react";
import { api, unwrap, ApiError } from "../lib/api";
import { getLoginErrorMessage } from "../lib/errors";
import { optionalWarning } from "../lib/optional-error";
import { SetupWizard } from "./SetupWizard";

interface Branding {
  name: string;
  logo: string;
}

interface SetupStatus {
  initialized: boolean;
}

interface OIDCStatus {
  enabled: boolean;
}

const PasswordInput = () => {
  const [visible, setVisible] = useState(false);
  return (
    <div className="relative">
      <TextInput
        label="密码"
        source="password"
        type={visible ? "text" : "password"}
        validate={required()}
      />
      <button
        type="button"
        onClick={() => setVisible((v) => !v)}
        className="absolute right-2 top-[30px] text-muted-foreground hover:text-foreground"
        tabIndex={-1}
        aria-label={visible ? "隐藏密码" : "显示密码"}
      >
        {visible ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
      </button>
    </div>
  );
};

export const LoginPage = (props: { redirectTo?: string }) => {
  const { redirectTo } = props;
  const [loading, setLoading] = useState(false);
  const [initialized, setInitialized] = useState<boolean | null>(null);
  const [brand, setBrand] = useState<Branding>({ name: "RAGFlow-X", logo: "" });
  const [oidcEnabled, setOidcEnabled] = useState(false);
  const login = useLogin();
  const notify = useNotify();

  useEffect(() => {
    api
      .get<{ code: number; data: Branding }>("/branding/public")
      .then((r) => {
        if (r.data.code === 0 && r.data.data) setBrand(r.data.data);
      })
      .catch((error) => optionalWarning(error, "登录页品牌信息加载失败"));

    unwrap<SetupStatus>(api.get("/system/setup/status"))
      .then((s) => setInitialized(s.initialized))
      .catch(() => setInitialized(true));

    unwrap<OIDCStatus>(api.get("/auth/oidc/status"))
      .then((status) => setOidcEnabled(Boolean(status.enabled)))
      .catch(() => setOidcEnabled(false));
  }, []);

  const handleSubmit: SubmitHandler<FieldValues> = (values) => {
    setLoading(true);
    login(values, redirectTo)
      .then(() => setLoading(false))
      .catch((error: unknown) => {
        setLoading(false);
        notify(error instanceof ApiError
          ? getLoginErrorMessage(error.code, error.status, error.displayMessage)
          : "ra.auth.sign_in_error", {
          type: "error",
        });
      });
  };

  if (initialized === false) {
    return (
      <>
        <SetupWizard onInitialized={() => setInitialized(true)} />
        <Notification />
      </>
    );
  }

  if (initialized === null) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="text-sm text-muted-foreground">加载中...</div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-6" aria-label="登录页面">
      <div className="w-full max-w-sm space-y-8">
        <div className="flex flex-col items-center gap-3 text-center">
          <div className="flex items-center gap-2 text-lg font-semibold">
            {brand.logo ? (
              <img src={brand.logo} alt={brand.name} className="h-8 w-8 object-contain" />
            ) : (
              <Shell className="size-6" />
            )}
            {brand.name}
          </div>
          <p className="text-sm text-muted-foreground">
            企业级 RAG 控制面与接入网关
          </p>
        </div>
        <Form className="space-y-6" onSubmit={handleSubmit} aria-label="登录表单">
          <TextInput
            label="用户名"
            source="username"
            validate={required()}
            autoFocus
          />
          <PasswordInput />
          <Button
            type="submit"
            className="w-full cursor-pointer"
            disabled={loading}
          >
            登录
          </Button>
        </Form>
        {oidcEnabled && (
          <div className="space-y-3">
            <div className="flex items-center gap-3 text-xs text-muted-foreground" role="separator">
              <div className="h-px flex-1 bg-border" />
              <span>或</span>
              <div className="h-px flex-1 bg-border" />
            </div>
            <a href="/api/v1/auth/oidc/start" aria-label="使用企业账号登录">
              <Button type="button" variant="outline" className="w-full cursor-pointer">
                企业账号登录
              </Button>
            </a>
          </div>
        )}
      </div>
      <Notification />
    </div>
  );
};
