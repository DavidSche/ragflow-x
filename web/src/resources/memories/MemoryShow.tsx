import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ChevronDown, ChevronRight, Loader2, Plus, RefreshCw, Search, Trash2 } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { NumericRangeField } from "@/components/NumericRangeField";
import { clampInteger, clampNumber } from "@/lib/numeric";

interface MemoryConfig {
  name: string;
  memory_type: string[];
  embd_id: string;
  llm_id: string;
  permissions: string;
  memory_size: number;
  forgetting_policy: string;
  temperature: number;
  description: string;
  system_prompt: string;
  user_prompt: string;
}

interface MemoryMessage {
  message_id: number;
  agent_id?: string;
  session_id?: string;
  user_input: string;
  agent_response?: string;
  status: boolean;
}

interface AgentOption {
  id: string;
  title: string;
  status: string;
}

interface AgentSessionOption {
  id: string;
  name: string;
}

export const MemoryShow = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { id } = useParams<{ id: string }>();
  const [cfg, setCfg] = useState<MemoryConfig>({
    name: "", memory_type: [], embd_id: "", llm_id: "", permissions: "me",
    memory_size: 5242880, forgetting_policy: "FIFO", temperature: 0.5,
    description: "", system_prompt: "", user_prompt: "",
  });
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [messages, setMessages] = useState<MemoryMessage[]>([]);
  const [msgForm, setMsgForm] = useState({ agent_id: "", session_id: "", user_input: "", agent_response: "" });
  const [adding, setAdding] = useState(false);
  // semantic search
  const [searchQuery, setSearchQuery] = useState("");
  const [searchThreshold, setSearchThreshold] = useState("0.2");
  const [searchTopN, setSearchTopN] = useState("5");
  const [searching, setSearching] = useState(false);
  const [searchResults, setSearchResults] = useState<MemoryMessage[]>([]);
  const [agentOptions, setAgentOptions] = useState<AgentOption[]>([]);
  const [sessionOptions, setSessionOptions] = useState<AgentSessionOption[]>([]);
  // expanded message content
  const [contentMap, setContentMap] = useState<Record<number, string>>({});
  const [openMsg, setOpenMsg] = useState<Record<number, boolean>>({});

  const load = async () => {
    if (!id) return;
    try {
      const [cfgRes, msgRes] = await Promise.all([
        api.get<{ code: number; data: any }>(`/memories/${id}/config`),
        api.get<{ code: number; data: MemoryMessage[] }>(`/memories/${id}/messages`),
      ]);
      const c = cfgRes.data?.data ?? {};
      setCfg({
        name: c.name ?? "", memory_type: c.memory_type ?? [], embd_id: c.embd_id ?? "", llm_id: c.llm_id ?? "",
        permissions: c.permissions ?? "me", memory_size: c.memory_size ?? 5242880,
        forgetting_policy: c.forgetting_policy ?? "FIFO", temperature: c.temperature ?? 0.5,
        description: c.description ?? "", system_prompt: c.system_prompt ?? "", user_prompt: c.user_prompt ?? "",
      });
      setMessages(Array.isArray(msgRes.data?.data) ? msgRes.data.data : []);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.config_load_fail"), { type: "error" });
    } finally {
      setLoaded(true);
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  useEffect(() => {
    api.get<{ code: number; data: { items: AgentOption[] } }>("/agents?page=1&page_size=200")
      .then((response) => setAgentOptions((response.data?.data?.items ?? []).filter((agent) => agent.status === "active")))
      .catch(() => setAgentOptions([]));
  }, []);

  useEffect(() => {
    if (!msgForm.agent_id) {
      setSessionOptions([]);
      return;
    }
    let cancelled = false;
    api.get<{ code: number; data: { items: AgentSessionOption[] } }>(`/agents/${msgForm.agent_id}/sessions?page=1&page_size=200`)
      .then((response) => {
        if (cancelled) return;
        const sessions = response.data?.data?.items ?? [];
        setSessionOptions(sessions);
        setMsgForm((current) => current.session_id && sessions.some((session) => session.id === current.session_id) ? current : { ...current, session_id: "" });
      })
      .catch(() => {
        if (!cancelled) setSessionOptions([]);
      });
    return () => {
      cancelled = true;
    };
  }, [msgForm.agent_id, msgForm.session_id]);

  const saveCfg = async () => {
    if (!id) return;
    const payload = {
      ...cfg,
      memory_size: clampInteger(cfg.memory_size, 1, 10485760, 5242880),
      temperature: clampNumber(cfg.temperature, 0, 2, 0.5),
    };
    setSaving(true);
    try {
      await api.put(`/memories/${id}`, payload);
      notify(t("memories.updated"), { type: "success" });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  const addMessage = async () => {
    if (!id || !msgForm.user_input.trim() || !msgForm.agent_response.trim()) return;
    setAdding(true);
    try {
      await api.post(`/memories/${id}/messages`, msgForm);
      notify(t("memories.message_added"), { type: "success" });
      setMsgForm({ agent_id: "", session_id: "", user_input: "", agent_response: "" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.message_fail"), { type: "error" });
    } finally {
      setAdding(false);
    }
  };

  const forget = async (m: MemoryMessage) => {
    if (!id) return;
    try {
      await api.delete(`/memories/${id}/messages/${m.message_id}`);
      notify(t("memories.message_forgotten"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.message_fail"), { type: "error" });
    }
  };

  const toggleStatus = async (m: MemoryMessage) => {
    if (!id) return;
    try {
      await api.put(`/memories/${id}/messages/${m.message_id}`, { status: !m.status });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.message_fail"), { type: "error" });
    }
  };

  const runSearch = async () => {
    if (!id) return;
    setSearching(true);
    setSearchResults([]);
    const threshold = clampNumber(Number(searchThreshold), 0, 1, 0.2);
    const topN = clampInteger(Number(searchTopN), 1, 100, 5);
    setSearchThreshold(String(threshold));
    setSearchTopN(String(topN));
    try {
      const res = await api.get<{ code: number; data: MemoryMessage[] }>(
        `/memories/${id}/messages/search?query=${encodeURIComponent(searchQuery)}&threshold=${encodeURIComponent(String(threshold))}&top_n=${encodeURIComponent(String(topN))}`,
      );
      setSearchResults(Array.isArray(res.data?.data) ? res.data.data : []);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.search_fail"), { type: "error" });
    } finally {
      setSearching(false);
    }
  };

  const toggleContent = async (m: MemoryMessage) => {
    if (!id) return;
    if (openMsg[m.message_id]) {
      setOpenMsg((p) => ({ ...p, [m.message_id]: false }));
      return;
    }
    setOpenMsg((p) => ({ ...p, [m.message_id]: true }));
    if (contentMap[m.message_id] === undefined) {
      try {
        const res = await api.get<{ code: number; data: any }>(`/memories/${id}/messages/${m.message_id}/content`);
        const data = res.data?.data;
        let text = "";
        if (typeof data === "string") text = data;
        else if (data && typeof data === "object") text = JSON.stringify(data, null, 2);
        else text = String(data ?? "");
        setContentMap((p) => ({ ...p, [m.message_id]: text }));
      } catch (err) {
        notify(err instanceof ApiError ? err.displayMessage : t("memories.message_fail"), { type: "error" });
      }
    }
  };

  if (!loaded) {
    return (
      <div className="flex items-center justify-center gap-2 p-10 text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
        <span>{t("memories.loading")}</span>
      </div>
    );
  }

  const renderMessageRow = (m: MemoryMessage) => {
    const open = !!openMsg[m.message_id];
    return (
      <div key={m.message_id} className="rounded border p-2 text-xs">
        <div className="flex items-center justify-between gap-2">
          <button type="button" className="flex items-center gap-1 font-medium" onClick={() => void toggleContent(m)}>
            {open ? <ChevronDown className="size-3" aria-hidden="true" /> : <ChevronRight className="size-3" aria-hidden="true" />}
            #{m.message_id} · {m.agent_id || "-"} · {m.session_id || "-"} ·{" "}
            {m.status ? t("memories.status_remembered") : t("memories.status_forgotten")}
          </button>
          <div className="flex gap-1">
            <button type="button" className="rounded border px-2 py-1" onClick={() => void toggleStatus(m)}>
              {m.status ? t("memories.forget") : t("memories.remember")}
            </button>
            <button type="button" className="rounded border px-2 py-1" onClick={() => void toggleContent(m)}>
              {t("memories.view_content")}
            </button>
            <button type="button" className="rounded border p-1 text-destructive" onClick={() => void forget(m)} aria-label={t("memories.forget")}>
              <Trash2 className="size-3" aria-hidden="true" />
            </button>
          </div>
        </div>
        <p className="mt-1 text-muted-foreground">{t("memories.ask")}: {m.user_input}</p>
        <p className="mt-1 text-muted-foreground">{t("memories.answer")}: {m.agent_response ?? ""}</p>
        {open && contentMap[m.message_id] !== undefined ? (
          <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 font-mono text-xs">
            {contentMap[m.message_id]}
          </pre>
        ) : null}
      </div>
    );
  };

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{cfg.name}</h1>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={saving}>
            <RefreshCw className="size-4" aria-hidden="true" />
            {t("memories.refresh")}
          </Button>
          <Button onClick={() => void saveCfg()} disabled={saving}>
            {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
          </Button>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <section className="rounded border p-4">
          <h2 className="mb-3 text-sm font-medium">{t("memories.config_title")}</h2>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label>{t("memories.name")}</Label>
              <Input value={cfg.name} onChange={(e) => setCfg((p) => ({ ...p, name: e.target.value }))} />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label>{t("memories.memory_type")}</Label>
                <Input value={(cfg.memory_type ?? []).join(",")} onChange={(e) => setCfg((p) => ({ ...p, memory_type: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) }))} />
              </div>
              <div className="space-y-1.5">
                <Label>{t("memories.permissions")}</Label>
                <Input value={cfg.permissions} onChange={(e) => setCfg((p) => ({ ...p, permissions: e.target.value }))} />
              </div>
              <div className="space-y-1.5">
                <Label>{t("memories.embd_id")}</Label>
                <Input value={cfg.embd_id} onChange={(e) => setCfg((p) => ({ ...p, embd_id: e.target.value }))} />
              </div>
              <div className="space-y-1.5">
                <Label>{t("memories.llm_id")}</Label>
                <Input value={cfg.llm_id} onChange={(e) => setCfg((p) => ({ ...p, llm_id: e.target.value }))} />
              </div>
              <div className="space-y-1.5">
                <Label>{t("memories.forgetting_policy")}</Label>
                <Input value={cfg.forgetting_policy} onChange={(e) => setCfg((p) => ({ ...p, forgetting_policy: e.target.value }))} />
              </div>
              <div className="space-y-1.5">
                <NumericRangeField
                  label={t("memories.temperature")}
                  value={cfg.temperature}
                  min={0}
                  max={2}
                  step={0.1}
                  onChange={(next) => setCfg((p) => ({ ...p, temperature: clampNumber(next ?? 0.5, 0, 2, 0.5) }))}
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label>{t("memories.system_prompt")}</Label>
              <textarea className="min-h-20 w-full rounded border bg-background p-2 text-xs" value={cfg.system_prompt} onChange={(e) => setCfg((p) => ({ ...p, system_prompt: e.target.value }))} />
            </div>
            <div className="space-y-1.5">
              <Label>{t("memories.user_prompt")}</Label>
              <textarea className="min-h-20 w-full rounded border bg-background p-2 text-xs" value={cfg.user_prompt} onChange={(e) => setCfg((p) => ({ ...p, user_prompt: e.target.value }))} />
            </div>
          </div>
        </section>

        <div className="flex flex-col gap-6">
          <section className="rounded border p-4">
            <h2 className="mb-3 text-sm font-medium">{t("memories.search_title")}</h2>
            <div className="space-y-2">
              <Input placeholder={t("memories.search_placeholder")} value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} />
              <div className="flex gap-2">
                <Input
                  aria-label={t("memories.search_threshold")}
                  inputMode="decimal"
                  placeholder={t("memories.search_threshold")}
                  value={searchThreshold}
                  onChange={(e) => setSearchThreshold(e.target.value)}
                  onBlur={(e) => setSearchThreshold(String(clampNumber(Number(e.target.value), 0, 1, 0.2)))}
                />
                <Input
                  aria-label={t("memories.search_topn")}
                  inputMode="numeric"
                  placeholder={t("memories.search_topn")}
                  value={searchTopN}
                  onChange={(e) => setSearchTopN(e.target.value.replace(/[^\d]/g, ""))}
                  onBlur={(e) => setSearchTopN(String(clampInteger(Number(e.target.value), 1, 100, 5)))}
                />
                <Button onClick={() => void runSearch()} disabled={searching || !searchQuery.trim()}>
                  {searching ? <Loader2 className="size-4 animate-spin" /> : <Search className="size-4" aria-hidden="true" />}
                  {t("memories.search_run")}
                </Button>
              </div>
            </div>
            {searchResults.length > 0 ? (
              <div className="mt-3 space-y-2">
                {searchResults.map(renderMessageRow)}
              </div>
            ) : searchResults.length === 0 && searching !== true ? null : null}
          </section>

          <section className="rounded border p-4">
            <h2 className="mb-3 text-sm font-medium">{t("memories.messages_title")}</h2>
            <div className="space-y-2">
              {messages.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("memories.messages_empty")}</p>
              ) : (
                messages.map(renderMessageRow)
              )}
            </div>

            <div className="mt-4 space-y-2 rounded border p-3">
              <div className="flex gap-2">
                <select
                  className="flex-1 rounded border bg-background px-2 py-1 text-sm"
                  value={msgForm.agent_id}
                  onChange={(e) => setMsgForm((p) => ({ ...p, agent_id: e.target.value, session_id: "" }))}
                >
                  <option value="">{t("memories.select_agent")}</option>
                  {agentOptions.map((agent) => (
                    <option key={agent.id} value={agent.id}>{agent.title}</option>
                  ))}
                </select>
                <select
                  className="flex-1 rounded border bg-background px-2 py-1 text-sm"
                  value={msgForm.session_id}
                  disabled={!msgForm.agent_id}
                  onChange={(e) => setMsgForm((p) => ({ ...p, session_id: e.target.value }))}
                >
                  <option value="">{t("memories.select_session")}</option>
                  {sessionOptions.map((session) => (
                    <option key={session.id} value={session.id}>{session.name}</option>
                  ))}
                </select>
              </div>
              <Input placeholder={t("memories.user_input")} value={msgForm.user_input} onChange={(e) => setMsgForm((p) => ({ ...p, user_input: e.target.value }))} />
              <Input placeholder={t("memories.agent_response")} value={msgForm.agent_response} onChange={(e) => setMsgForm((p) => ({ ...p, agent_response: e.target.value }))} />
              <Button onClick={() => void addMessage()} disabled={adding || !msgForm.user_input.trim() || !msgForm.agent_response.trim()}>
                {adding ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" aria-hidden="true" />}
                {t("memories.add_message")}
              </Button>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
};

