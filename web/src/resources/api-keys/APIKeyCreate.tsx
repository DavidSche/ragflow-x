import { useState } from "react";
import { useNavigate, useNotify } from "ra-core";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { dataProvider } from "../../dataProvider";
import { ApiError } from "../../lib/api";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";
import { ApprovalHoldError, type ApprovalHold } from "@/lib/approval-hold";
import { parseBoundedInteger } from "@/lib/numeric";

interface CreatedKey {
  id: string;
  name: string;
  secret?: string;
}

export const APIKeyCreate = () => {
  const t = useTranslate();
  const [name, setName] = useState("");
  const [tokenQuota, setTokenQuota] = useState("");
  const [requestQuota, setRequestQuota] = useState("");
  const [allowedIPs, setAllowedIPs] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const [loading, setLoading] = useState(false);
  const notify = useNotify();
  const navigate = useNavigate();

  const submit = async () => {
    if (!name || loading) return;
    const parsedTokenQuota = tokenQuota === "" ? 0 : parseBoundedInteger(tokenQuota, 0, 1_000_000_000_000, 0);
    const parsedRequestQuota = requestQuota === "" ? 0 : parseBoundedInteger(requestQuota, 0, 1_000_000_000, 0);
    if (Number(tokenQuota) > 1_000_000_000_000) {
      notify(t("api_keys.token_quota_too_large"), { type: "warning" });
      return;
    }
    if (Number(requestQuota) > 1_000_000_000) {
      notify(t("api_keys.request_quota_too_large"), { type: "warning" });
      return;
    }
    setLoading(true);
    try {
      const res = await dataProvider.create<CreatedKey>("api-keys", {
        data: {
          name,
          ...(tokenQuota !== "" ? { token_quota: parsedTokenQuota } : {}),
          ...(requestQuota !== "" ? { request_quota: parsedRequestQuota } : {}),
          ...(allowedIPs.trim() !== ""
            ? {
                allowed_ips: allowedIPs
                  .split(/[,\n]/)
                  .map((value) => value.trim())
                  .filter(Boolean),
              }
            : {}),
        },
      });
      const key = res.data;
      if (key.secret) {
        setSecret(key.secret);
      } else {
        notify(t("api_keys.created_once"), { type: "success" });
        navigate("/api-keys");
      }
    } catch (error) {
      if (error instanceof ApprovalHoldError) {
        setHold(error.hold);
        return;
      }
      notify(error instanceof ApiError ? error.displayMessage : t("api_keys.create_fail"), {
        type: "error",
      });
    } finally {
      setLoading(false);
    }
  };

  if (secret) {
    return (
      <div className="max-w-xl space-y-4">
        <div className="space-y-3 rounded-md border p-4">
          <p className="text-sm text-muted-foreground">
            {t("api_keys.secret_hint")}
          </p>
          <code className="block break-all rounded bg-muted p-2 text-sm">
            {secret}
          </code>
          <Button onClick={() => navigate("/api-keys")}>{t("api_keys.secret_done")}</Button>
        </div>
      </div>
    );
  }

  return (
    <>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <div className="max-w-md space-y-4">
      <div className="space-y-2">
        <Label htmlFor="key-name">{t("api_keys.key_name")}</Label>
        <Input
          id="key-name"
          value={name}
          placeholder={t("api_keys.key_name_placeholder")}
          onChange={(e) => setName(e.target.value)}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="key-token-quota">{t("api_keys.token_quota")}</Label>
        <Input
          id="key-token-quota"
          inputMode="numeric"
          min={0}
          value={tokenQuota}
          placeholder={t("api_keys.token_quota_placeholder")}
          onChange={(e) => setTokenQuota(e.target.value.replace(/[^\d]/g, ""))}
          onBlur={(e) => tokenQuota && setTokenQuota(String(parseBoundedInteger(e.target.value, 0, 1_000_000_000_000, 0)))}
        />
        <p className="text-xs text-muted-foreground">{t("api_keys.token_quota_hint")}</p>
      </div>
      <div className="space-y-2">
        <Label htmlFor="key-request-quota">{t("api_keys.request_quota")}</Label>
        <Input
          id="key-request-quota"
          inputMode="numeric"
          min={0}
          value={requestQuota}
          placeholder={t("api_keys.request_quota_placeholder")}
          onChange={(e) => setRequestQuota(e.target.value.replace(/[^\d]/g, ""))}
          onBlur={(e) => requestQuota && setRequestQuota(String(parseBoundedInteger(e.target.value, 0, 1_000_000_000, 0)))}
        />
        <p className="text-xs text-muted-foreground">{t("api_keys.request_quota_hint")}</p>
      </div>
      <div className="space-y-2">
        <Label htmlFor="key-allowed-ips">{t("api_keys.allowed_ips")}</Label>
        <Input
          id="key-allowed-ips"
          value={allowedIPs}
          placeholder={t("api_keys.allowed_ips_placeholder")}
          onChange={(e) => setAllowedIPs(e.target.value)}
        />
        <p className="text-xs text-muted-foreground">{t("api_keys.allowed_ips_hint")}</p>
      </div>
      <Button onClick={submit} disabled={loading || !name}>
        {t("api_keys.create_action")}
      </Button>
      </div>
    </>
  );
};
