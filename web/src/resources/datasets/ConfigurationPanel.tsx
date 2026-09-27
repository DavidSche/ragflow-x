/**
 * ConfigurationPanel – dataset configuration editor (chunk method, embedding, etc.).
 */
import { useEffect, useState } from "react";
import type { ChangeEvent } from "react";
import { useCanAccess, useGetIdentity, useNotify, useRecordContext, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { api } from "../../lib/api";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";
import { approvalHoldFromResponse, type ApprovalHold } from "@/lib/approval-hold";
import { optionalWarning } from "../../lib/optional-error";
import { NumericRangeField } from "@/components/NumericRangeField";
import { type DatasetConfig, errText } from "./dataset-types";
import { DEFAULT_CHUNK_TOKEN_NUM, resolveEmbeddingModelId } from "./embeddingModel";

export const ConfigurationPanel = () => {
  const record = useRecordContext<{ id?: string; tenant_id?: string }>();
  const datasetId = record?.id;
  const { data: identity } = useGetIdentity();
  const identityTenantID = (identity as { tenant_id?: string } | undefined)?.tenant_id;
  const scopeQuery =
    identityTenantID && record?.tenant_id && identityTenantID !== record.tenant_id
      ? `?scope=specific&tenant_id=${encodeURIComponent(record.tenant_id)}`
      : "";
  const { canAccess: isAdmin } = useCanAccess({ resource: "dataset", action: "manage" });
  const notify = useNotify();
  const [cfg, setCfg] = useState<DatasetConfig | null>(null);
  const [chunkToken, setChunkToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const [embeddingModels, setEmbeddingModels] = useState<{ model_id: string; name: string }[]>([]);
  const [embeddingChoice, setEmbeddingChoice] = useState("");
  const [origChunkToken, setOrigChunkToken] = useState("");
  const t = useTranslate();

  const load = async () => {
    if (!datasetId) return;
    try {
      const res = await api.get<{
        code: number;
        message: string;
        data: DatasetConfig;
      }>(`/datasets/${datasetId}/config${scopeQuery}`);
      const d = res.data.data;
      setCfg(d);
      const savedToken = d.parser_config?.chunk_token_num as number | undefined;
      setChunkToken(String(savedToken ?? DEFAULT_CHUNK_TOKEN_NUM));
      setOrigChunkToken(String(savedToken ?? DEFAULT_CHUNK_TOKEN_NUM));
    } catch (err) {
      notify(errText(err, t("datasets.config_load_fail")), { type: "error" });
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [datasetId]);

  useEffect(() => {
    api
      .get<{ code: number; message: string; data: { embedding: { model_id: string; name: string }[] } }>("/datasets/model-options")
      .then((res) => {
        const list = (res.data?.data?.embedding ?? [])
          .map((m) => ({ model_id: m.model_id, name: m.name }))
          .filter((m) => m.name);
        setEmbeddingModels(list);
      })
      .catch((error) => optionalWarning(error, "Embedding 模型列表加载失败"));
  }, []);

  const save = async () => {
    if (!datasetId || !cfg) return;
    setSaving(true);
    try {
      const payload: Record<string, unknown> = {
        chunk_method: cfg.chunk_method,
        permission: cfg.permission,
        description: cfg.description,
      };
      // Only forward the token number when it changed. Omitting parser_config lets
      // RAGFlow keep its rich parser settings, which its update schema would
      // otherwise reject as extra inputs (image_context_size / llm_id / ...).
      if (chunkToken !== origChunkToken) {
        const token = Number(chunkToken);
        if (chunkToken !== "" && Number.isFinite(token) && token > 0) {
          payload.parser_config = { chunk_token_num: token };
        }
      }
      // Only change the embedding model when the user picks one; the value is the
      // tenant model id which RAGFlow accepts.
      if (embeddingChoice) {
        payload.embedding_model = embeddingChoice;
      }
      const res = await api.put(`/datasets/${datasetId}/config${scopeQuery}`, payload);
      const responseHold = approvalHoldFromResponse(res);
      if (responseHold) {
        setHold(responseHold);
        return;
      }
      notify(t("datasets.config_saved"), { type: "success" });
      await load();
    } catch (err) {
      notify(errText(err, t("datasets.config_save_fail")), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  if (!datasetId || !cfg) return null;
  const resolvedEmbeddingId =
    resolveEmbeddingModelId(embeddingModels, cfg.embedding_model);

  return (
    <section className="mt-6 space-y-3" aria-label={t("datasets.aria_config_panel")}>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <h3 className="text-sm font-semibold">{t("datasets.config_title")}</h3>
      <div className="grid gap-3 rounded-md border p-4 md:grid-cols-2">
        <label className="space-y-1 text-sm">
          <span>{t("datasets.chunk_method")}</span>
          <select
            className="w-full rounded border bg-background px-2 py-1 text-sm"
            value={cfg.chunk_method ?? "naive"}
            disabled={!isAdmin}
            onChange={(e: ChangeEvent<HTMLSelectElement>) =>
              setCfg({ ...cfg, chunk_method: e.target.value })
            }
          >
            {[
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
            ].map((m) => (
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
            value={embeddingChoice || resolvedEmbeddingId}
            disabled={!isAdmin}
            onChange={(e: ChangeEvent<HTMLSelectElement>) =>
              setEmbeddingChoice(e.target.value)
            }
          >
            {cfg.embedding_model && !resolvedEmbeddingId ? (
              <option value={cfg.embedding_model}>{cfg.embedding_model}</option>
            ) : null}
            {embeddingModels.map((m) => (
              <option key={m.model_id} value={m.model_id}>{m.name}</option>
            ))}
          </select>
        </label>
        <label className="space-y-1 text-sm">
          <span>{t("datasets.permission")}</span>
          <select
            className="w-full rounded border bg-background px-2 py-1 text-sm"
            value={cfg.permission ?? "team"}
            disabled={!isAdmin}
            onChange={(e: ChangeEvent<HTMLSelectElement>) =>
              setCfg({ ...cfg, permission: e.target.value })
            }
          >
            <option value="me">{t("datasets.permission_me")}</option>
            <option value="team">{t("datasets.permission_team")}</option>
          </select>
        </label>
        <NumericRangeField
          label={t("datasets.chunk_tokens")}
          value={chunkToken}
          min={1}
          max={2048}
          unit="tokens"
          disabled={!isAdmin}
          required
          onChange={(next) => setChunkToken(String(next ?? DEFAULT_CHUNK_TOKEN_NUM))}
        />
        <label className="space-y-1 text-sm md:col-span-2">
          <span>{t("datasets.description")}</span>
          <textarea
            className="w-full rounded border bg-background p-2 text-sm"
            value={cfg.description ?? ""}
            disabled={!isAdmin}
            onChange={(e: ChangeEvent<HTMLTextAreaElement>) =>
              setCfg({ ...cfg, description: e.target.value })
            }
          />
        </label>
      </div>
      {isAdmin && (
        <Button onClick={save} disabled={saving} aria-label={t("datasets.config_save_label")}>
          {t("datasets.config_save")}
        </Button>
      )}
    </section>
  );
};
