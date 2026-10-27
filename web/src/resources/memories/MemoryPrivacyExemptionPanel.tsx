import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, ApiError, unwrap, type ApiEnvelope } from "@/lib/api";

interface MemoryPrivacyExemption {
  id: string;
  label: string;
  pattern: string;
  rule_version: string;
  version: number;
  active: boolean;
  created_by: string;
  created_at: string;
}

// High-risk labels can never be exempted (plan-137 §6.4/§10.1 rule 15). The
// panel renders them as read-only context so admins do not attempt an
// exempt request that the backend will always refuse.
const HIGH_RISK_LABELS = [
  "credential",
  "api_key",
  "db_connection_string",
  "jwt",
  "private_key_pem",
];

// Ordinary v1 labels that may carry exemptions (internal order numbers
// excluded from phone_cn etc.).
const EXEMPTABLE_LABELS = [
  "phone_cn",
  "id_card_cn",
  "bank_card",
  "email",
  "ipv4",
  "business_license_cn",
  "certificate_no",
  "upload_link",
];

export const MemoryPrivacyExemptionPanel = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess } = useCanAccess({ resource: "memory-policy", action: "manage" });
  const [loading, setLoading] = useState(false);
  const [items, setItems] = useState<MemoryPrivacyExemption[]>([]);
  const [label, setLabel] = useState("");
  const [pattern, setPattern] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmID, setConfirmID] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    if (!canAccess) return;
    setLoading(true);
    try {
      const data = await unwrap(
        api.get<ApiEnvelope<MemoryPrivacyExemption[]>>("/memory-privacy/pii-exemptions"),
      );
      setItems(Array.isArray(data) ? data : []);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryPrivacy.load_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  }, [canAccess, notify, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const create = async () => {
    if (!label.trim() || !pattern.trim()) return;
    setBusy(true);
    try {
      await unwrap(
        api.put<ApiEnvelope<MemoryPrivacyExemption>>("/memory-privacy/pii-exemptions", {
          label: label.trim(),
          pattern: pattern.trim(),
        }),
      );
      setLabel("");
      setPattern("");
      notify(t("memoryPrivacy.created"), { type: "success" });
      await load();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryPrivacy.create_failed"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const doDelete = async () => {
    if (!confirmID) return;
    setDeleting(true);
    try {
      await unwrap(api.delete<ApiEnvelope<unknown>>(`/memory-privacy/pii-exemptions/${confirmID}`));
      notify(t("memoryPrivacy.deleted"), { type: "success" });
      setConfirmID(null);
      await load();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryPrivacy.delete_failed"), { type: "error" });
    } finally {
      setDeleting(false);
    }
  };

  if (!canAccess) return null;

  return (
    <Card className="mb-4" aria-label={t("memoryPrivacy.title")}>
      <CardHeader className="pb-2">
        <CardTitle className="text-base">{t("memoryPrivacy.title")}</CardTitle>
        <p className="text-xs text-muted-foreground">{t("memoryPrivacy.description")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <select
            className="rounded border bg-background px-2 py-1 text-sm"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            aria-label={t("memoryPrivacy.label")}
          >
            <option value="">{t("memoryPrivacy.label_placeholder")}</option>
            {EXEMPTABLE_LABELS.map((item) => (
              <option key={item} value={item}>{item}</option>
            ))}
          </select>
          <Input
            className="max-w-xs flex-1"
            placeholder={t("memoryPrivacy.pattern_placeholder")}
            value={pattern}
            onChange={(e) => setPattern(e.target.value)}
            aria-label={t("memoryPrivacy.pattern")}
          />
          <Button onClick={() => void create()} disabled={busy || !label.trim() || !pattern.trim()}>
            {t("memoryPrivacy.create")}
          </Button>
          <div className="flex flex-wrap items-center gap-1">
            <span className="text-xs text-muted-foreground">{t("memoryPrivacy.high_risk")}</span>
            {HIGH_RISK_LABELS.map((item) => (
              <Badge key={item} variant="outline" className="text-destructive">
                {item}
              </Badge>
            ))}
          </div>
        </div>
        {loading ? (
          <p className="text-sm text-muted-foreground">{t("memoryPrivacy.loading")}</p>
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("memoryPrivacy.empty")}</p>
        ) : (
          <ul className="divide-y rounded border" aria-label={t("memoryPrivacy.list")}>
            {items.map((item) => (
              <li key={item.id} className="flex flex-wrap items-center justify-between gap-2 p-2 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="secondary">{item.label}</Badge>
                  <code className="rounded bg-muted px-1 py-0.5">{item.pattern}</code>
                  <span className="text-muted-foreground">{item.rule_version}</span>
                  <span className="text-muted-foreground">v{item.version}</span>
                  <span className="text-muted-foreground">{item.created_at}</span>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  className="text-destructive"
                  onClick={() => setConfirmID(item.id)}
                  aria-label={t("memoryPrivacy.delete")}
                >
                  {t("memoryPrivacy.delete")}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      <Dialog open={confirmID !== null} onOpenChange={(open) => !open && setConfirmID(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("memoryPrivacy.delete_confirm_title")}</DialogTitle>
            <DialogDescription>{t("memoryPrivacy.delete_confirm_msg")}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmID(null)}>
              {t("ra.action.cancel")}
            </Button>
            <Button variant="destructive" onClick={() => void doDelete()} disabled={deleting}>
              {t("memoryPrivacy.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
};
