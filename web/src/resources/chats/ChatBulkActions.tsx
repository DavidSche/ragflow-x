import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Ban, CheckCircle2, Trash2 } from "lucide-react";
import { useListContext, useNotify, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { ConfirmDialog } from "../../components/ConfirmDialog";

export const BulkActions = () => {
  const { selectedIds, onUnselectItems } = useListContext();
  const notify = useNotify();
  const qc = useQueryClient();
  const count = selectedIds?.length ?? 0;
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const t = useTranslate();

  const setStatus = async (status: "active" | "disabled") => {
    if (!count) return;
    try {
      await api.post("/chats/batch-status", { ids: selectedIds, status });
      notify(status === "active" ? t("chats.batch_enable") : t("chats.batch_disable"), {
        type: "success",
      });
      onUnselectItems();
      qc.invalidateQueries({ queryKey: ["chats"] });
    } catch (err) {
      notify(
        err instanceof ApiError ? err.displayMessage : t("tenants.bulk_enable_fail"),
        { type: "error" },
      );
    }
  };

  const doDelete = async () => {
    if (!count) return;
    setDeleting(true);
    try {
      await api.delete("/chats", { data: { ids: selectedIds } });
      notify(t("chats.batch_delete"), { type: "success" });
      onUnselectItems();
      qc.invalidateQueries({ queryKey: ["chats"] });
    } catch (err) {
      notify(
        err instanceof ApiError ? err.displayMessage : t("chats.delete_fail"),
        { type: "error" },
      );
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };

  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" onClick={() => setStatus("active")}>
          <CheckCircle2 /> {t("chats.status_active")}
        </Button>
        <Button size="sm" variant="outline" onClick={() => setStatus("disabled")}>
          <Ban /> {t("chats.status_disabled")}
        </Button>
        <Button size="sm" variant="outline" onClick={() => setConfirmOpen(true)}>
          <Trash2 /> {t("confirm.delete_label")}
        </Button>
      </div>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("chats.batch_delete_title")}
        message={t("chats.batch_delete_confirm", { count: String(count) })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};
