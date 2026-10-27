import { useState } from "react";
import { useNavigate, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { dataProvider } from "../../dataProvider";
import { ApiError } from "../../lib/api";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";
import { ApprovalHoldError, type ApprovalHold } from "@/lib/approval-hold";

export const ModelProviderCreate = () => {
  const t = useTranslate();
  const [name, setName] = useState("");
  const [saving, setSaving] = useState(false);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const notify = useNotify();
  const navigate = useNavigate();

  const save = async () => {
    if (!name.trim() || saving) return;
    setSaving(true);
    try {
      await dataProvider.create("model-providers", { data: { provider_name: name.trim() } });
      notify(t("providers.add_success"), { type: "success" });
      navigate("/model-providers");
    } catch (error) {
      if (error instanceof ApprovalHoldError) {
        setHold(error.hold);
        return;
      }
      notify(error instanceof ApiError ? error.displayMessage : t("providers.add_fail"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <div className="mx-auto max-w-md space-y-4">
        <div className="space-y-2">
          <Label htmlFor="provider-name">{t("providers.provider_name")}</Label>
          <Input
            id="provider-name"
            value={name}
            placeholder={t("providers.name_examples")}
            onChange={(event) => setName(event.target.value)}
          />
        </div>
        <Button onClick={() => void save()} disabled={saving || !name.trim()}>
          {saving ? t("providers.create") : t("providers.add_btn")}
        </Button>
      </div>
    </>
  );
};
