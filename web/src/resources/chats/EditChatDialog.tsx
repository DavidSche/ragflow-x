import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Pencil } from "lucide-react";
import { useNotify, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { FormField, useFieldValidation } from "../../components/FormField";
import { ChatConfigEditor } from "./ChatConfigEditor";
import { CreateChatDialog, DatasetSelector } from "./CreateChatDialog";
import type { ChatConfigLike, ChatConfigValues, ChatLike, DatasetLike } from "./chat-types";
import { EMPTY_CONFIG, toCfg, parseLLMSetting, datasetOptions } from "./chat-types";
import { useGetList } from "ra-core";

/* ── EditChatDialog ──────────────────────────────────────────────── */

export const EditChatDialog = ({
  chat,
  open,
  onOpenChange,
}: {
  chat: Pick<ChatLike, "id" | "name">;
  open: boolean;
  onOpenChange: (v: boolean) => void;
}) => {
  const notify = useNotify();
  const qc = useQueryClient();
  const { data: datasets } = useGetList("datasets", {
    pagination: { page: 1, perPage: 200 },
  });
  const nameField = useFieldValidation(chat.name ?? "", { required: true });
  const [selected, setSelected] = useState<string[]>([]);
  const [cfg, setCfg] = useState<ChatConfigValues>(EMPTY_CONFIG);
  const [loading, setLoading] = useState(false);
  const [loadedId, setLoadedId] = useState("");
  const t = useTranslate();

  useEffect(() => {
    if (!open || !chat.id || loadedId === chat.id) return;
    setLoading(true);
    (async () => {
      try {
        const res = await api.get<{ code: number; data: ChatConfigLike }>(
          `/chats/${chat.id}/config`,
        );
        const d = res.data.data;
        nameField.setValue(d.name ?? chat.name ?? "");
        setSelected(d.dataset_ids ?? []);
        setCfg(toCfg(d));
        setLoadedId(chat.id);
      } catch (err) {
        notify(
          err instanceof ApiError ? err.displayMessage : t("chats.config_load_fail"),
          { type: "error" },
        );
      } finally {
        setLoading(false);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, chat.id, chat.name, loadedId, notify]);

  const toggle = (id: string) =>
    setSelected((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    );

  const save = async () => {
    if (!nameField.validate()) return;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let llmSetting: any;
    try {
      llmSetting = parseLLMSetting(cfg.llmSetting);
    } catch (err) {
      notify(err instanceof Error ? err.message : t("chats.config_error"), { type: "error" });
      return;
    }
    const body: Record<string, unknown> = {
      name: nameField.value.trim(),
      language: cfg.language,
      dataset_ids: selected,
      llm_id: cfg.llmId || undefined,
      rerank_id: cfg.rerankId || undefined,
      system: cfg.system || undefined,
      prologue: cfg.prologue || undefined,
      empty_response: cfg.emptyResponse || undefined,
      top_n: cfg.topN === "" ? undefined : Number(cfg.topN),
      similarity_threshold: cfg.similarity === "" ? undefined : Number(cfg.similarity),
      vector_similarity_weight: cfg.vectorWeight === "" ? undefined : Number(cfg.vectorWeight),
      llm_setting: llmSetting,
    };
    try {
      await api.put(`/chats/${chat.id}`, body);
      notify(t("chats.config_saved"), { type: "success" });
      onOpenChange(false);
      setLoadedId("");
      qc.invalidateQueries({ queryKey: ["chats"] });
    } catch (err) {
      notify(
        err instanceof ApiError ? err.displayMessage : t("chats.config_error"),
        { type: "error" },
      );
    }
  };

  const { values, mapId } = datasetOptions(datasets as DatasetLike[] | undefined);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("chats.edit_chat_title")}</DialogTitle>
        </DialogHeader>
        <div className="max-h-[86vh] space-y-4 overflow-y-auto pr-1">
          {loading ? (
            <div className="text-sm text-muted-foreground">{t("chats.config_loading")}</div>
          ) : (
            <>
              <FormField label={t("chats.chat_name")} required error={nameField.error}>
                <Input
                  {...nameField.inputProps}
                  aria-required="true"
                  placeholder={t("chats.chat_name_placeholder")}
                />
              </FormField>
              <DatasetSelector
                values={values}
                mapId={mapId}
                selected={selected}
                onToggle={toggle}
              />
              <ChatConfigEditor value={cfg} onChange={setCfg} initialExpanded />
            </>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              {t("confirm.cancel")}
            </Button>
            <Button onClick={save} disabled={loading}>
              {t("ra.action.save")}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
};

/* ── EditChatButton ──────────────────────────────────────────────── */

export const EditChatButton = ({
  chat,
}: {
  chat: Pick<ChatLike, "id" | "name">;
}) => {
  const [open, setOpen] = useState(false);
  const t = useTranslate();
  return (
    <>
      <IconButtonWithTooltip
        label={t("chats.edit_config")}
        onClick={(e) => {
          e.stopPropagation();
          setOpen(true);
        }}
      >
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <EditChatDialog chat={chat} open={open} onOpenChange={setOpen} />
    </>
  );
};

/* ── ChatActions ─────────────────────────────────────────────────── */

export const ChatActions = () => (
  <div className="flex items-center gap-2">
    <CreateChatDialog />
  </div>
);

