/**
 * DatasetConfigFields – reusable dataset configuration form fields
 * (chunk method, embedding model, permission, chunk tokens). Used by the
 * dataset create/edit dialogs and kept in sync with ConfigurationPanel.
 */
import { useEffect, useState } from "react";
import type { ChangeEvent } from "react";
import { useTranslate } from "ra-core";
import { api } from "../../lib/api";
import { optionalWarning } from "../../lib/optional-error";
import { NumericRangeField } from "@/components/NumericRangeField";
import { DEFAULT_CHUNK_TOKEN_NUM, resolveEmbeddingModelId } from "./embeddingModel";

export interface DatasetConfigFieldsValue {
  chunk_method: string;
  embedding_model: string;
  permission: string;
  chunk_token_num: string;
}

const CHUNK_METHODS = [
  "naive",
  "qa",
  "manual",
  "table",
  "paper",
  "book",
  "laws",
  "presentation",
  "picture",
  "picture_ocr",
  "email",
  "one",
  "tag",
];

export const DatasetConfigFields = ({
  value,
  onChange,
  disabled,
}: {
  value: DatasetConfigFieldsValue;
  onChange: (v: DatasetConfigFieldsValue) => void;
  disabled?: boolean;
}) => {
  const t = useTranslate();
  const [embeddingModels, setEmbeddingModels] = useState<{ model_id: string; name: string }[]>([]);
  const [embeddingChoice, setEmbeddingChoice] = useState("");

  useEffect(() => {
    api
      .get<{ code: number; message: string; data: { embedding: { model_id: string; name: string }[] } }>("/datasets/model-options")
      .then((res) => setEmbeddingModels(res.data?.data?.embedding ?? []))
      .catch((error) => optionalWarning(error, "Embedding 模型列表加载失败"));
  }, []);

  useEffect(() => {
    setEmbeddingChoice(resolveEmbeddingModelId(embeddingModels, value.embedding_model));
  }, [value.embedding_model, embeddingModels]);

  const resolvedEmbeddingId =
    resolveEmbeddingModelId(embeddingModels, value.embedding_model);

  const set = (patch: Partial<DatasetConfigFieldsValue>) => onChange({ ...value, ...patch });

  return (
    <div className="grid gap-3 md:grid-cols-2">
      <label className="space-y-1 text-sm">
        <span>{t("datasets.chunk_method")}</span>
        <select
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={value.chunk_method ?? "naive"}
          disabled={disabled}
          onChange={(e: ChangeEvent<HTMLSelectElement>) => set({ chunk_method: e.target.value })}
        >
          {CHUNK_METHODS.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
      </label>
      <label className="space-y-1 text-sm">
        <span>{t("datasets.embedding_model")}</span>
        <select
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={embeddingChoice}
          disabled={disabled}
          onChange={(e: ChangeEvent<HTMLSelectElement>) => {
            setEmbeddingChoice(e.target.value);
            set({ embedding_model: e.target.value });
          }}
        >
          {value.embedding_model && !resolvedEmbeddingId ? (
            <option value={value.embedding_model}>{value.embedding_model}</option>
          ) : null}
          {embeddingModels.length === 0 ? (
            <option value="">{t("datasets.embedding_default")}</option>
          ) : null}
          {embeddingModels.map((m) => (
            <option key={m.model_id} value={m.model_id}>
              {m.name}
            </option>
          ))}
        </select>
      </label>
      <label className="space-y-1 text-sm">
        <span>{t("datasets.permission")}</span>
        <select
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={value.permission ?? "team"}
          disabled={disabled}
          onChange={(e: ChangeEvent<HTMLSelectElement>) => set({ permission: e.target.value })}
        >
          <option value="me">{t("datasets.permission_me")}</option>
          <option value="team">{t("datasets.permission_team")}</option>
        </select>
      </label>
      <NumericRangeField
        label={t("datasets.chunk_tokens")}
        value={value.chunk_token_num || DEFAULT_CHUNK_TOKEN_NUM}
        min={1}
        max={2048}
        unit="tokens"
        disabled={disabled}
        required
        onChange={(next) => set({ chunk_token_num: String(next ?? DEFAULT_CHUNK_TOKEN_NUM) })}
      />
    </div>
  );
};
