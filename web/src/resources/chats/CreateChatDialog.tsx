import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Plus } from "lucide-react";
import { useGetList, useNotify, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { FormField, useFieldValidation } from "../../components/FormField";
import { ChatConfigEditor } from "./ChatConfigEditor";
import type { ChatConfigValues, DatasetLike } from "./chat-types";
import { EMPTY_CONFIG, parseLLMSetting, datasetOptions } from "./chat-types";

export const CreateChatDialog = () => {
  const notify = useNotify();
  const qc = useQueryClient();
  const { data: datasets } = useGetList("datasets", {
    pagination: { page: 1, perPage: 200 },
  });
  const [open, setOpen] = useState(false);
  const nameField = useFieldValidation("", { required: true });
  const [selected, setSelected] = useState<string[]>([]);
  const [cfg, setCfg] = useState<ChatConfigValues>(EMPTY_CONFIG);
  const t = useTranslate();

  const toggle = (id: string) =>
    setSelected((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    );

  const payload = (c: ChatConfigValues) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let llmSetting: any;
    try {
      llmSetting = parseLLMSetting(c.llmSetting);
    } catch (err) {
      throw err;
    }
    return {
      name: nameField.value,
      language: "Chinese",
      dataset_ids: selected,
      llm_id: c.llmId || undefined,
      rerank_id: c.rerankId || undefined,
      system: c.system || undefined,
      prologue: c.prologue || undefined,
      empty_response: c.emptyResponse || undefined,
      top_n: c.topN === "" ? undefined : Number(c.topN),
      similarity_threshold: c.similarity === "" ? undefined : Number(c.similarity),
      vector_similarity_weight: c.vectorWeight === "" ? undefined : Number(c.vectorWeight),
      llm_setting: llmSetting,
    };
  };

  const saveCreate = async () => {
    if (!nameField.validate()) return;
    let body: Record<string, unknown>;
    try {
      body = payload(cfg);
    } catch (err) {
      notify(err instanceof Error ? err.message : t("chats.config_error"), { type: "error" });
      return;
    }
    try {
      await api.post("/chats", body);
      notify(t("chats.create_chat"), { type: "success" });
      setOpen(false);
      nameField.setValue("");
      setSelected([]);
      setCfg(EMPTY_CONFIG);
      qc.invalidateQueries({ queryKey: ["chats"] });
    } catch (err) {
      notify(
        err instanceof ApiError ? err.displayMessage : t("chats.create_chat"),
        { type: "error" },
      );
    }
  };

  const { values, mapId } = datasetOptions(datasets as DatasetLike[] | undefined);
  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <Plus /> {t("chats.session_create")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t("chats.create_chat")}</DialogTitle>
          </DialogHeader>
          <div className="max-h-[86vh] space-y-4 overflow-y-auto pr-1">
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
            <ChatConfigEditor value={cfg} onChange={setCfg} />
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)}>
                {t("confirm.cancel")}
              </Button>
              <Button onClick={saveCreate}>{t("ra.action.save")}</Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

/* ── Shared dataset selector (used by create & edit) ─────────────── */

export function DatasetSelector({
  values,
  mapId,
  selected,
  onToggle,
}: {
  values: DatasetLike[];
  mapId: (d: DatasetLike) => string;
  selected: string[];
  onToggle: (id: string) => void;
}) {
  const t = useTranslate();
  return (
    <div className="space-y-2">
      <Label>{t("chats.bind_datasets")}</Label>
      <div className="max-h-40 space-y-1 overflow-y-auto rounded border p-2">
        {values.map((d: DatasetLike) => {
          const dsId = mapId(d);
          return (
            <label key={d.id} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={selected.includes(dsId)}
                onChange={() => onToggle(dsId)}
              />
              {d.name}
            </label>
          );
        })}
        {values.length === 0 && (
          <div className="text-sm text-muted-foreground">
            {t("chats.no_bindable_datasets")}
          </div>
        )}
      </div>
    </div>
  );
}
