import { useEffect, useRef, useState, type ReactNode } from "react";
import { useParams } from "react-router-dom";
import { useGetList, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  ChevronDown,
  Loader2,
  RefreshCw,
  Search,
  Settings2,
  StopCircle,
} from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { Show } from "@/components/admin";
import { datasetOptions } from "../chats/chat-types";
import type { DatasetLike } from "../chats/chat-types";
import { ConversationShell } from "../workbench/ConversationShell";
import { downloadConversation } from "../workbench/conversation-export";
import { classifyConversationError, streamRead, type Message } from "../workbench/workbench-types";
import { ConversationExportMenu } from "../workbench/ConversationExportMenu";
import { PromptPresetPicker } from "../workbench/PromptPresetPicker";
import {
  applyParameterProfileToSearchConfig,
  matchSearchParameterProfileId,
} from "../workbench/search-preset-mapping";

interface SearchAppConfig {
  kb_ids: string[];
  doc_ids: string[];
  similarity_threshold: number;
  vector_similarity_weight: number;
  rerank_id: string;
  top_k: number;
  chat_id: string;
  use_kg: boolean;
  use_rerank: boolean;
  summary: boolean;
  related_search: boolean;
  query_mindmap: boolean;
  web_search: boolean;
  reference_metadata: { include: boolean; fields: string[] };
  llm_temperature: number;
}

const emptyConfig = (): SearchAppConfig => ({
  kb_ids: [],
  doc_ids: [],
  similarity_threshold: 0.2,
  vector_similarity_weight: 0.3,
  rerank_id: "",
  top_k: 1024,
  chat_id: "",
  use_kg: false,
  use_rerank: false,
  summary: false,
  related_search: false,
  query_mindmap: false,
  web_search: false,
  reference_metadata: { include: false, fields: [] },
  llm_temperature: 0.1,
});

interface ModelOption {
  name: string;
  model_id: string;
  model_type: string;
}
interface ModelOptionsData {
  chat: ModelOption[];
  rerank: ModelOption[];
  defaults: ModelOption[];
}

interface DocumentOption {
  id: string;
  name: string;
}

const col = <T,>(a: T[]): T[] => (Array.isArray(a) ? a : []);

const SliderField = ({
  label,
  value,
  min,
  max,
  step,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  onChange: (v: number) => void;
}) => (
  <div className="space-y-1.5">
    <div className="flex items-center justify-between">
      <Label>{label}</Label>
      <span className="text-xs text-muted-foreground">{value.toFixed(step < 1 ? 2 : 0)}</span>
    </div>
    <input
      type="range"
      className="w-full accent-foreground"
      min={min}
      max={max}
      step={step}
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
    />
  </div>
);

const CollapsibleSection = ({
  title,
  icon,
  open,
  onToggle,
  children,
}: {
  title: string;
  icon?: ReactNode;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) => {
  if (!open) {
    return (
      <button
        type="button"
        className="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded border bg-background"
        onClick={onToggle}
        aria-label={title}
        aria-expanded={false}
      >
        {icon}
      </button>
    );
  }
  return (
    <section className="rounded border">
      <button type="button" className="flex w-full items-center gap-2 border-b p-3 text-left text-sm font-medium" onClick={onToggle} aria-expanded={true}>
        {icon}
        <span className="flex-1">{title}</span>
        <ChevronDown className="size-4" aria-hidden="true" />
      </button>
      <div className="p-4">{children}</div>
    </section>
  );
};

const switchClass = "h-4 w-4 accent-foreground";

export const SearchAppShow = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { id } = useParams<{ id: string }>();
  const [name, setName] = useState("");
  const [cfg, setCfg] = useState<SearchAppConfig>(emptyConfig);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [configOpen, setConfigOpen] = useState(true);
  const [debugOpen, setDebugOpen] = useState(true);
  // retrieval debug
  const [question, setQuestion] = useState("");
  const [debugging, setDebugging] = useState(false);
  const [messages, setMessages] = useState<Message[]>([]);
  const abortRef = useRef<AbortController | null>(null);
  const { data: datasets } = useGetList("datasets", { pagination: { page: 1, perPage: 200 } });
  const { values: datasetValues, mapId } = datasetOptions(datasets as DatasetLike[] | undefined);
  const [modelOptions, setModelOptions] = useState<ModelOptionsData | null>(null);
  const [documentOptions, setDocumentOptions] = useState<DocumentOption[]>([]);
  const [documentOptionsLoading, setDocumentOptionsLoading] = useState(false);

  const loadConfig = async () => {
    if (!id) return;
    setLoading(true);
    try {
      const [res, optRes] = await Promise.all([
        api.get<{ code: number; data: any }>(`/search-apps/${id}/config`),
        api.get<{ code: number; data: ModelOptionsData }>(`/search-apps/model-options`),
      ]);
      setModelOptions(optRes.data?.data ?? null);
      const defaultChatId = (optRes.data?.data?.defaults ?? []).find((d) => d.model_type === "chat")?.model_id ?? "";
      const data = res.data?.data;
      if (data) {
        setName(data.name ?? "");
        const c = data.search_config ?? {};
        const llm = (c.llm_setting && typeof c.llm_setting === "object" ? c.llm_setting : {}) as Record<string, unknown>;
        const sim = Number(c.similarity_threshold);
        const weight = Number(c.vector_similarity_weight);
        const topk = Number(c.top_k);
        setCfg({
          kb_ids: col(c.kb_ids),
          doc_ids: col(c.doc_ids),
          similarity_threshold: sim > 0 ? sim : 0.2,
          vector_similarity_weight: weight > 0 ? weight : 0.3,
          rerank_id: c.rerank_id ?? "",
          top_k: topk > 0 ? topk : 1024,
          chat_id: (c.chat_id as string) || (llm.llm_id as string) || defaultChatId,
          use_kg: !!c.use_kg,
          use_rerank: !!(c.use_rerank ?? c.rerank_id),
          summary: !!c.summary,
          related_search: !!c.related_search,
          query_mindmap: !!c.query_mindmap,
          web_search: !!c.web_search,
          reference_metadata: {
            include: !!c.reference_metadata?.include,
            fields: col(c.reference_metadata?.fields),
          },
          llm_temperature: Number(llm.temperature ?? 0.1),
        });
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("searchApps.config_load_fail"), { type: "error" });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadConfig();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  useEffect(() => {
    const datasetIds = cfg.kb_ids;
    if (datasetIds.length === 0) {
      setDocumentOptions([]);
      setDocumentOptionsLoading(false);
      return;
    }
    let cancelled = false;
    setDocumentOptionsLoading(true);
    Promise.all(datasetIds.map(async (datasetId) => {
      const response = await api.get<{ code: number; data: DocumentOption[] }>(`/datasets/${datasetId}/documents`);
      return response.data?.data ?? [];
    }))
      .then((groups) => {
        if (cancelled) return;
        const options = groups.flat().filter(
          (document, index, items) => items.findIndex((item) => item.id === document.id) === index,
        );
        setDocumentOptions(options);
        setCfg((current) => ({ ...current, doc_ids: current.doc_ids.filter((documentId) => options.some((option) => option.id === documentId)) }));
      })
      .catch(() => {
        if (!cancelled) setDocumentOptions([]);
      })
      .finally(() => {
        if (!cancelled) setDocumentOptionsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [cfg.kb_ids]);

  const save = async () => {
    if (!id) return;
    const { llm_temperature, ...rest } = cfg;
    void rest;
    setSaving(true);
    try {
      await api.put(`/search-apps/${id}`, {
        name,
        search_config: {
          ...cfg,
          rerank_id: cfg.use_rerank ? cfg.rerank_id : "",
          chat_id: cfg.chat_id,
          llm_setting: { llm_id: cfg.chat_id, temperature: cfg.llm_temperature },
        },
      });
      notify(t("searchApps.updated"), { type: "success" });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("searchApps.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  const runDebug = async () => {
    if (!id || !question.trim()) return;
    setDebugging(true);
    const turnId = `search-${Date.now()}`;
    setMessages([
      { id: `${turnId}-user`, role: "user", content: question.trim(), kind: "search", createdAt: new Date().toISOString(), status: "completed" },
      { id: `${turnId}-assistant`, role: "assistant", content: "", turnId, kind: "search", createdAt: new Date().toISOString(), status: "streaming" },
    ]);
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const res = await fetch(`/api/v1/search-apps/${id}/completion/stream`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ question: question.trim() }),
        signal: controller.signal,
      });
      if (!res.ok || !res.body) {
        notify(t("searchApps.debug_fail"), { type: "error" });
        const fail = `HTTP ${res.status}`;
        setMessages((prev) => prev.map((message, index) => index === prev.length - 1 && message.role === "assistant"
          ? { ...message, status: "failed", error: fail, errorCode: classifyConversationError(res.status, fail) }
          : message));
        return;
      }
      await streamRead(
        res.body,
        (delta, refs) => {
          setMessages((prev) => prev.map((message, index) => index === prev.length - 1 && message.role === "assistant"
            ? { ...message, content: message.content + delta, status: "streaming", citations: refs.length ? refs : message.citations }
            : message));
        },
        (message) => {
          setMessages((prev) => prev.map((item, index) => index === prev.length - 1 && item.role === "assistant"
            ? { ...item, status: "failed", error: message, errorCode: classifyConversationError(undefined, message) }
            : item));
        },
      );
      setMessages((prev) => {
        const next = [...prev];
        const last = next[next.length - 1];
        if (last && last.role === "assistant") last.status = "completed";
        return next;
      });
    } catch (err) {
      if (!(err instanceof DOMException && err.name === "AbortError")) {
        notify(t("searchApps.debug_fail"), { type: "error" });
        const fail = err instanceof Error ? err.message : String(err);
        setMessages((prev) => prev.map((message, index) => index === prev.length - 1 && message.role === "assistant"
          ? { ...message, status: "failed", error: fail, errorCode: classifyConversationError(undefined, fail) }
          : message));
      }
    } finally {
      setDebugging(false);
      abortRef.current = null;
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center gap-2 p-10 text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
        <span>{t("searchApps.loading")}</span>
      </div>
    );
  }

  const toggleDataset = (dsId: string) =>
    setCfg((p) =>
      p.kb_ids.includes(dsId)
        ? { ...p, kb_ids: p.kb_ids.filter((x) => x !== dsId) }
        : { ...p, kb_ids: [...p.kb_ids, dsId] },
    );

  return (
    <Show resource="search-apps">
    <div className="mx-auto flex max-w-7xl flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{name}</h1>
        <div className="flex gap-2">
          <ConversationExportMenu
            messages={messages}
            title={name || "search-app"}
            filenamePrefix="search-app"
            className="h-9"
          />
          <Button variant="outline" onClick={() => void loadConfig()} disabled={saving}>
            <RefreshCw className="size-4" aria-hidden="true" />
            {t("searchApps.refresh")}
          </Button>
          <Button onClick={() => void save()} disabled={saving || (cfg.use_rerank && !cfg.rerank_id)}>
            {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
          </Button>
        </div>
      </div>

      <div className="flex flex-col gap-6 lg:flex-row">
        <div className="min-w-0 flex-1">
        {/* Debug panel (left) */}
        <CollapsibleSection
          title={t("searchApps.debug_title")}
          icon={<Search className="size-4" aria-hidden="true" />}
          open={debugOpen}
          onToggle={() => setDebugOpen((v) => !v)}
        >
          <ConversationShell
            className="min-h-40"
            messages={messages}
            busy={debugging}
            emptyText={t("searchApps.debug_empty")}
            messageListClassName="space-y-3"
            messagesAriaLabel={t("searchApps.debug_title")}
            onCopy={async (content) => {
              try {
                await navigator.clipboard.writeText(content);
                notify(t("workbench.copied"), { type: "success" });
              } catch {
                notify(t("workbench.copy_failed"), { type: "error" });
              }
            }}
            toolbar={
              <>
                <Button onClick={() => void runDebug()} disabled={debugging || !question.trim()}>
                  {debugging ? <Loader2 className="size-4 animate-spin" /> : <Search className="size-4" aria-hidden="true" />}
                  {t("searchApps.debug_run")}
                </Button>
                {debugging ? (
                  <Button variant="outline" onClick={() => abortRef.current?.abort()}>
                    <StopCircle className="size-4" aria-hidden="true" />
                    {t("searchApps.debug_stop")}
                  </Button>
                ) : null}
              </>
            }
            input={
              <Input
                placeholder={t("searchApps.debug_placeholder")}
                value={question}
                onChange={(e) => setQuestion(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void runDebug();
                }}
              />
            }
          />
        </CollapsibleSection>
        </div>

        <div className={configOpen ? "shrink-0 lg:w-[380px]" : "shrink-0"}>
        {/* Config panel (right) */}
        <CollapsibleSection
          title={t("searchApps.config_title")}
          icon={<Settings2 className="size-4" aria-hidden="true" />}
          open={configOpen}
          onToggle={() => setConfigOpen((v) => !v)}
        >
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label>{t("searchApps.name")}</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </div>

            <div className="space-y-1.5">
              <Label>{t("searchApps.datasets")}</Label>
              <div className="max-h-40 space-y-1 overflow-y-auto rounded border p-2">
                {datasetValues.length === 0 ? (
                  <div className="text-sm text-muted-foreground">{t("searchApps.no_datasets")}</div>
                ) : (
                  datasetValues.map((d: DatasetLike) => {
                    const dsId = mapId(d);
                    return (
                      <label key={d.id} className="flex items-center gap-2 text-sm">
                        <input
                          type="checkbox"
                          className={switchClass}
                          checked={(cfg.kb_ids ?? []).includes(dsId)}
                          onChange={() => toggleDataset(dsId)}
                        />
                        {d.name}
                      </label>
                    );
                  })
                )}
              </div>
            </div>

            <div className="space-y-1.5">
              <Label>{t("searchApps.llm_model")}</Label>
              <select
                className="w-full rounded border bg-background px-2 py-1.5 text-sm"
                value={cfg.chat_id}
                onChange={(e) => setCfg((p) => ({ ...p, chat_id: e.target.value }))}
              >
                <option value="">{t("searchApps.llm_model_default")}</option>
                {(modelOptions?.chat ?? []).map((m) => (
                  <option key={m.model_id} value={m.model_id}>
                    {m.name}
                  </option>
                ))}
                {cfg.chat_id && !(modelOptions?.chat ?? []).some((m) => m.model_id === cfg.chat_id) ? (
                  <option value={cfg.chat_id}>{cfg.chat_id}</option>
                ) : null}
              </select>
            </div>

            <PromptPresetPicker
              showPrompts={false}
              activeProfileId={matchSearchParameterProfileId(cfg)}
              onSelectPreset={() => undefined}
              onSelectProfile={(profileId) =>
                setCfg((current) => applyParameterProfileToSearchConfig(current, profileId))
              }
            />

            <SliderField
              label={t("searchApps.similarity_threshold")}
              value={cfg.similarity_threshold}
              min={0.1}
              max={1}
              step={0.01}
              onChange={(v) => setCfg((p) => ({ ...p, similarity_threshold: v }))}
            />
            <SliderField
              label={t("searchApps.vector_weight")}
              value={cfg.vector_similarity_weight}
              min={0.1}
              max={1}
              step={0.01}
              onChange={(v) => setCfg((p) => ({ ...p, vector_similarity_weight: v }))}
            />
            <SliderField
              label={t("searchApps.top_k")}
              value={cfg.top_k}
              min={1}
              max={1024}
              step={1}
              onChange={(v) => setCfg((p) => ({ ...p, top_k: v }))}
            />

            <div className="space-y-1.5">
              <Label>{t("searchApps.doc_ids")}</Label>
              {cfg.kb_ids.length === 0 ? (
                <p className="text-xs text-muted-foreground">{t("searchApps.datasets_empty_hint")}</p>
              ) : documentOptionsLoading ? (
                <div className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="size-3 animate-spin" aria-hidden="true" />{t("ra.page.loading")}</div>
              ) : documentOptions.length === 0 ? (
                <p className="text-xs text-muted-foreground">{t("searchApps.documents_empty_hint")}</p>
              ) : (
                <div className="max-h-40 space-y-1 overflow-y-auto rounded border p-2">
                  {documentOptions.map((document) => (
                    <label key={document.id} className="flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        className={switchClass}
                        checked={cfg.doc_ids.includes(document.id)}
                        onChange={() => setCfg((current) => ({
                          ...current,
                          doc_ids: current.doc_ids.includes(document.id)
                            ? current.doc_ids.filter((documentId) => documentId !== document.id)
                            : [...current.doc_ids, document.id],
                        }))}
                      />
                      {document.name}
                    </label>
                  ))}
                </div>
              )}
            </div>

            <div className="space-y-2 rounded border p-3">
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className={switchClass} checked={cfg.use_rerank} onChange={(e) => setCfg((p) => ({ ...p, use_rerank: e.target.checked }))} />
                <span>{t("searchApps.rerank_model")}</span>
              </label>
              {cfg.use_rerank && (
                <>
                <select
                  className="w-full rounded border bg-background px-2 py-1.5 text-sm"
                  value={cfg.rerank_id}
                  onChange={(e) => setCfg((p) => ({ ...p, rerank_id: e.target.value }))}
                >
                  <option value="">{t("searchApps.rerank_id")}</option>
                  {(modelOptions?.rerank ?? []).map((m) => (
                    <option key={m.model_id} value={m.model_id}>{m.name}</option>
                  ))}
                </select>
                {cfg.use_rerank && !cfg.rerank_id ? (
                  <p className="text-xs text-destructive">{t("searchApps.rerank_required")}</p>
                ) : null}
                </>
              )}

              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className={switchClass} checked={cfg.summary} onChange={(e) => setCfg((p) => ({ ...p, summary: e.target.checked }))} />
                <span>{t("searchApps.ai_summary")}</span>
              </label>
              {cfg.summary && (
                <SliderField
                  label={t("searchApps.llm_temperature")}
                  value={cfg.llm_temperature}
                  min={0}
                  max={1}
                  step={0.01}
                  onChange={(v) => setCfg((p) => ({ ...p, llm_temperature: v }))}
                />
              )}

              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className={switchClass} checked={cfg.related_search} onChange={(e) => setCfg((p) => ({ ...p, related_search: e.target.checked }))} />
                <span>{t("searchApps.related_search")}</span>
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className={switchClass} checked={cfg.query_mindmap} onChange={(e) => setCfg((p) => ({ ...p, query_mindmap: e.target.checked }))} />
                <span>{t("searchApps.query_mindmap")}</span>
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className={switchClass} checked={cfg.use_kg} onChange={(e) => setCfg((p) => ({ ...p, use_kg: e.target.checked }))} />
                <span>{t("searchApps.use_kg")}</span>
              </label>
            </div>

            <div className="space-y-2 rounded border p-3">
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" className={switchClass} checked={cfg.reference_metadata.include} onChange={(e) => setCfg((p) => ({ ...p, reference_metadata: { ...p.reference_metadata, include: e.target.checked } }))} />
                <span>{t("searchApps.show_metadata")}</span>
              </label>
              {cfg.reference_metadata.include && (
                <Input
                  placeholder={t("searchApps.metadata_fields")}
                  value={(cfg.reference_metadata.fields ?? []).join(",")}
                  onChange={(e) =>
                    setCfg((p) => ({
                      ...p,
                      reference_metadata: { ...p.reference_metadata, fields: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) },
                    }))
                  }
                />
              )}
            </div>
          </div>
        </CollapsibleSection>
        </div>
      </div>
    </div>
    </Show>
  );
};
