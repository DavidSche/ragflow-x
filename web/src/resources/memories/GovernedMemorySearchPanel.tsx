import { useEffect, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Loader2, Search, ShieldCheck } from "lucide-react";
import { api, ApiError } from "@/lib/api";

interface AgentOption {
  id: string;
  title: string;
  status: string;
}

interface AgentSessionOption {
  id: string;
  name: string;
}

interface GovernedMemoryMessage {
  message_id: number;
  user_input: string;
  agent_response?: string;
  content?: string;
}

interface GovernedMemoryDecision {
  id: string;
  decision: string;
  memory_policy_id: string;
  request_id: string;
}

interface GovernedMemorySearchResult {
  decision: GovernedMemoryDecision;
  messages: GovernedMemoryMessage[];
}

export const GovernedMemorySearchPanel = ({ memoryId }: { memoryId: string }) => {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess } = useCanAccess({ resource: "memory-policy", action: "execute" });
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [agentID, setAgentID] = useState("");
  const [sessions, setSessions] = useState<AgentSessionOption[]>([]);
  const [sessionID, setSessionID] = useState("");
  const [query, setQuery] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<GovernedMemorySearchResult | null>(null);

  useEffect(() => {
    if (!canAccess) return;
    api.get<{ code: number; data: { items: AgentOption[] } }>("/agents?page=1&page_size=200")
      .then((response) => setAgents((response.data?.data?.items ?? []).filter((agent) => agent.status === "active")))
      .catch(() => setAgents([]));
  }, [canAccess]);

  useEffect(() => {
    if (!canAccess || !agentID) {
      setSessions([]);
      setSessionID("");
      return;
    }
    let cancelled = false;
    api.get<{ code: number; data: { items: AgentSessionOption[] } }>(`/agents/${agentID}/sessions?page=1&page_size=200`)
      .then((response) => {
        if (!cancelled) setSessions(response.data?.data?.items ?? []);
      })
      .catch(() => {
        if (!cancelled) setSessions([]);
      });
    return () => { cancelled = true; };
  }, [agentID, canAccess]);

  const runSearch = async () => {
    if (!agentID || !sessionID || !query.trim()) {
      notify(t("memories.governed_required"), { type: "error" });
      return;
    }
    setRunning(true);
    try {
      const response = await api.post<{ code: number; data: GovernedMemorySearchResult }>(
        `/memories/${memoryId}/governed-search`,
        {
          query: query.trim(), assistant_id: agentID, session_id: sessionID,
          request_id: crypto.randomUUID(), top_n: 5,
        },
      );
      const searchResult = response.data?.data;
      setResult(searchResult ?? null);
      notify(t("memories.governed_done"), { type: "success" });
    } catch (error) {
      setResult(null);
      notify(error instanceof ApiError ? error.displayMessage : t("memories.governed_failed"), { type: "error" });
    } finally {
      setRunning(false);
    }
  };

  if (!canAccess) return null;

  return (
    <Card className="mt-4">
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle className="flex items-center gap-2">
          <ShieldCheck className="size-4" aria-hidden="true" />
          {t("memories.governed_title")}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="text-sm text-muted-foreground">{t("memories.governed_hint")}</div>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="governed-agent">{t("memories.governed_agent")}</Label>
            <select
              id="governed-agent"
              className="h-9 w-full rounded border bg-background px-2 text-sm"
              value={agentID}
              onChange={(event) => setAgentID(event.target.value)}
            >
              <option value="">{t("memories.select_agent")}</option>
              {agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.title || agent.id}</option>)}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="governed-session">{t("memories.governed_session")}</Label>
            <select
              id="governed-session"
              className="h-9 w-full rounded border bg-background px-2 text-sm"
              value={sessionID}
              onChange={(event) => setSessionID(event.target.value)}
            >
              <option value="">{t("memories.select_session")}</option>
              {sessions.map((session) => <option key={session.id} value={session.id}>{session.name || session.id}</option>)}
            </select>
          </div>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("memories.search_placeholder")} aria-label={t("memories.search_placeholder")} />
          <Button type="button" onClick={() => void runSearch()} disabled={running}>
            {running ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <Search className="size-4" aria-hidden="true" />}
            {t("memories.governed_run")}
          </Button>
        </div>
        {result ? (
          <div className="space-y-2">
            <div className="rounded border p-2 text-xs">
              {t("memories.governed_decision")}: {result.decision.decision} · {t("memories.governed_policy")}: {result.decision.memory_policy_id || "—"}
            </div>
            {result.messages.length === 0 ? (
              <div className="text-sm text-muted-foreground">{t("memories.messages_empty")}</div>
            ) : result.messages.map((message) => (
              <div key={message.message_id} className="rounded border p-2 text-xs">
                <p className="font-medium">#{message.message_id} · {t("memories.user_input")}: {message.user_input}</p>
                <p className="mt-1 text-muted-foreground">{t("memories.agent_response")}: {message.agent_response ?? ""}</p>
              </div>
            ))}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
};
