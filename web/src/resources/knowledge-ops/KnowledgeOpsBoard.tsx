import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { CheckCircle2, CircleAlert, Database, RefreshCw, Tags } from "lucide-react";
import { api, ApiError } from "../../lib/api";

import {
  KnowledgeTasksCard,
  type KnowledgeTaskForm,
  type KnowledgeTask,
  type KnowledgeTaskSummary,
} from "./KnowledgeTasksCard";
import { TraceRunLookup } from "./TraceRunLookup";
import { MetricContractBoard } from "./MetricContractBoard";

interface Envelope<T> {
  code: number;
  data: T;
}

interface PageEnvelope<T> {
  items: T[];
  total: number;
}

interface Summary {
  total_turns: number;
  active_users: number;
  active_sessions: number;
  completed: number;
  no_answer: number;
  failed: number;
  with_citations: number;
  tokens_in: number;
  tokens_out: number;
  positive: number;
  negative: number;
  attribution_summary?: Record<string, number>;
  citation_rate: number;
  no_answer_rate: number;
  failure_rate: number;
  satisfaction_rate: number;
  avg_latency_ms: number;
  avg_resolution_hours: number;
}

interface TopQuery {
  question: string;
  requests: number;
  last_asked_at: string;
  no_answer_count: number;
  failed_count: number;
  citation_missing_count: number;
  avg_latency_ms: number;
}

interface KnowledgeEvent {
  id: string;
  request_id: string;
  app_type: string;
  app_id: string;
  session_id?: string;
  question: string;
  answer_excerpt?: string;
  status: string;
  citations_count: number;
  duration_ms: number;
  resolution_duration_ms?: number;
  resolved_at?: string;
  tokens_in: number;
  tokens_out: number;
  created_at: string;
  review_status: string;
  review_note?: string;
  reviewed_at?: string;
  feedback_rating?: string;
  feedback_attribution?: string;
}

interface EvalSetOption {
  id: string;
  name: string;
}

const emptySummary: Summary = {
  total_turns: 0,
  active_users: 0,
  active_sessions: 0,
  completed: 0,
  no_answer: 0,
  failed: 0,
  with_citations: 0,
  tokens_in: 0,
  tokens_out: 0,
  positive: 0,
  negative: 0,
  attribution_summary: {},
  citation_rate: 0,
  no_answer_rate: 0,
  failure_rate: 0,
  satisfaction_rate: 0,
  avg_latency_ms: 0,
  avg_resolution_hours: 0,
};

const emptyTaskSummary: KnowledgeTaskSummary = {
  open: 0,
  in_progress: 0,
  blocked: 0,
  pending_approval: 0,
  resolved: 0,
  canceled: 0,
  overdue: 0,
};

const RANGES = [
  { value: "7", labelKey: "range_7" },
  { value: "30", labelKey: "range_30" },
  { value: "90", labelKey: "range_90" },
];

const ATTRIBUTIONS = [
  "unclassified",
  "knowledge",
  "retrieval",
  "template",
  "model",
  "routing",
  "tool",
];


const percent = (value: number) => `${Math.round(value * 100)}%`;
const compact = (value: number) => {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value);
};

const statusLabelKey: Record<string, string> = {
  completed: "status_completed",
  no_answer: "status_no_answer",
  failed: "status_failed",
};

const reviewLabelKey: Record<string, string> = {
  open: "review_open",
  resolved: "review_resolved",
  ignored: "review_ignored",
};

export const KnowledgeOpsBoard = () => {
  const t = useTranslate();
  const notify = useNotify();
  const [range, setRange] = useState("7");
  const [attributionFilter, setAttributionFilter] = useState("");
  const [summary, setSummary] = useState<Summary>(emptySummary);
  const [topQueries, setTopQueries] = useState<TopQuery[]>([]);
  const [events, setEvents] = useState<KnowledgeEvent[]>([]);
  const [eventTotal, setEventTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [reviewing, setReviewing] = useState<string | null>(null);
  const [evalSets, setEvalSets] = useState<EvalSetOption[]>([]);
  const [evalSetId, setEvalSetId] = useState("");
  const [taskSummary, setTaskSummary] = useState<KnowledgeTaskSummary>(emptyTaskSummary);
  const [tasks, setTasks] = useState<KnowledgeTask[]>([]);
  const { canAccess: canManage } = useCanAccess({ resource: "knowledge-ops", action: "manage" });

  const load = useCallback(async () => {
    setLoading(true);
    const days = Number(range);
    const from = new Date(Date.now() - days * 86_400_000).toISOString().slice(0, 10);
    const query = new URLSearchParams({ date_from: from });
    const eventQuery = new URLSearchParams({
      ...Object.fromEntries(query),
      page: "1",
      page_size: "50",
      review_status: "open",
    });
    if (attributionFilter) eventQuery.set("attribution", attributionFilter);
    try {
      const [summaryRes, topRes, eventRes, evalSetRes, taskSummaryRes, taskRes] = await Promise.all([
        api.get<Envelope<Summary>>(`/knowledge-ops?${query.toString()}`),
        api.get<Envelope<TopQuery[]>>(`/knowledge-ops/top-queries?${new URLSearchParams({ ...Object.fromEntries(query), limit: "10" }).toString()}`),
        api.get<Envelope<PageEnvelope<KnowledgeEvent>>>(`/knowledge-ops/events?${eventQuery.toString()}`),
        api.get<Envelope<PageEnvelope<EvalSetOption>>>("/eval-sets?page=1&page_size=100"),
        api.get<Envelope<KnowledgeTaskSummary>>("/knowledge-tasks/summary"),
        api.get<Envelope<PageEnvelope<KnowledgeTask>>>("/knowledge-tasks?page=1&page_size=50"),
      ]);
      setSummary(summaryRes.data.data ?? emptySummary);
      setTopQueries(topRes.data.data ?? []);
      setEvents(eventRes.data.data?.items ?? []);
      setEventTotal(eventRes.data.data?.total ?? eventRes.data.data?.items.length ?? 0);
      setEvalSets(evalSetRes.data.data?.items ?? []);
      setTaskSummary(taskSummaryRes.data.data ?? emptyTaskSummary);
      setTasks(taskRes.data.data?.items ?? []);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("knowledgeOps.load_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  }, [attributionFilter, notify, range, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const review = async (event: KnowledgeEvent, status: "resolved" | "ignored") => {
    setReviewing(event.id);
    try {
      await api.patch(`/knowledge-ops/events/${event.id}/review`, { status, note: "" });
      setEvents((prev) => prev.filter((item) => item.id !== event.id));
      setEventTotal((prev) => Math.max(0, prev - 1));
      notify(status === "resolved" ? t("knowledgeOps.marked_resolved") : t("knowledgeOps.marked_ignored"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("knowledgeOps.review_failed"), { type: "error" });
    } finally {
      setReviewing(null);
    }
  };

  const convertToEval = async (event: KnowledgeEvent) => {
    if (!evalSetId) {
      notify(t("knowledgeOps.select_eval_set_required"), { type: "warning" });
      return;
    }
    setReviewing(event.id);
    try {
      await api.post(`/knowledge-ops/events/${event.id}/to-eval-case`, { eval_set_id: evalSetId });
      notify(t("knowledgeOps.converted_to_eval"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("knowledgeOps.convert_failed"), { type: "error" });
    } finally {
      setReviewing(null);
    }
  };

  const createTask = async (form: KnowledgeTaskForm) => {
    setReviewing("task-create");
    try {
      await api.post("/knowledge-tasks", {
        ...form,
        due_at: form.due_at ? new Date(form.due_at).toISOString() : undefined,
      });
      notify(t("knowledgeOps.task_created"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("knowledgeOps.task_create_failed"), { type: "error" });
    } finally {
      setReviewing(null);
    }
  };

  const updateTask = async (
    task: KnowledgeTask,
    status: "in_progress" | "resolved" | "canceled",
    regression: { evalSetId: string; regressionCaseId: string },
  ) => {
    if (status === "resolved" && (!regression.evalSetId || !regression.regressionCaseId)) {
      notify(t("knowledgeOps.task_regression_required"), { type: "warning" });
      return;
    }
    setReviewing(`task-${task.id}`);
    try {
      await api.patch(`/knowledge-tasks/${task.id}`, {
        status,
        resolution_note: status === "resolved" ? t("knowledgeOps.task_default_resolution") : undefined,
        regression_eval_set_id: status === "resolved" ? regression.evalSetId : undefined,
        regression_eval_case_id: status === "resolved" ? regression.regressionCaseId : undefined,
        regression_status: status === "resolved" ? "passed" : undefined,
      });
      notify(
        status === "resolved"
          ? t("knowledgeOps.task_resolved")
          : status === "canceled"
            ? t("knowledgeOps.task_canceled")
            : t("knowledgeOps.task_started"),
        { type: "success" },
      );
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("knowledgeOps.task_update_failed"), { type: "error" });
    } finally {
      setReviewing(null);
    }
  };

  const attributionCount = (category: string) => summary.attribution_summary?.[category] ?? 0;
  const cards = [
    { label: t("knowledgeOps.total_turns"), value: compact(summary.total_turns), hint: t("knowledgeOps.sessions_hint", { sessions: summary.active_sessions }) },
    { label: t("knowledgeOps.active_users"), value: compact(summary.active_users), hint: t("knowledgeOps.active_users_hint") },
    { label: t("knowledgeOps.citation_rate"), value: percent(summary.citation_rate), hint: t("knowledgeOps.with_citations_hint", { count: compact(summary.with_citations) }) },
    { label: t("knowledgeOps.satisfaction"), value: percent(summary.satisfaction_rate), hint: t("knowledgeOps.satisfaction_hint", { positive: summary.positive, negative: summary.negative }) },
    { label: t("knowledgeOps.no_answer_rate"), value: percent(summary.no_answer_rate), hint: t("knowledgeOps.no_answer_hint", { count: compact(summary.no_answer) }) },
    { label: t("knowledgeOps.failure_rate"), value: percent(summary.failure_rate), hint: t("knowledgeOps.failure_hint", { count: compact(summary.failed) }) },
    { label: t("knowledgeOps.avg_latency"), value: `${Math.round(summary.avg_latency_ms)} ms`, hint: t("knowledgeOps.avg_latency_hint") },
    { label: t("knowledgeOps.resolution_duration"), value: `${summary.avg_resolution_hours.toFixed(1)} ${t("knowledgeOps.hours")}`, hint: t("knowledgeOps.resolution_hint") },
  ];

  return (
    <div className="space-y-5 p-4">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold">{t("knowledgeOps.title")}</h1>
          <p className="text-sm text-muted-foreground">{t("knowledgeOps.subtitle")}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {RANGES.map((item) => (
            <Button
              key={item.value}
              size="sm"
              variant={range === item.value ? "default" : "outline"}
              onClick={() => setRange(item.value)}
            >
              {t(`knowledgeOps.${item.labelKey}`)}
            </Button>
          ))}
          <Button size="sm" variant="outline" onClick={() => void load()}>
            <RefreshCw className="size-4" /> {t("knowledgeOps.refresh")}
          </Button>
        </div>
      </header>

      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {loading
          ? Array.from({ length: 6 }).map((_, index) => <Skeleton key={index} className="h-24 rounded" />)
          : cards.map((item) => (
              <Card key={item.label}>
                <CardContent className="p-4">
                  <p className="text-sm text-muted-foreground">{item.label}</p>
                  <p className="mt-1 text-2xl font-semibold">{item.value}</p>
                  <p className="mt-1 text-xs text-muted-foreground">{item.hint}</p>
                </CardContent>
              </Card>
            ))}
      </section>

      <section className="grid gap-4 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-sm font-medium">
              <Database className="size-4" /> {t("knowledgeOps.top_queries")}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {topQueries.length === 0 ? (
              <p className="py-6 text-center text-sm text-muted-foreground">{t("knowledgeOps.no_queries")}</p>
            ) : (
              topQueries.map((item) => (
                <div key={`${item.question}-${item.last_asked_at}`} className="rounded border p-3">
                  <div className="flex items-start justify-between gap-3">
                    <p className="line-clamp-2 text-sm">{item.question}</p>
                    <span className="shrink-0 rounded bg-muted px-2 py-0.5 text-xs">
                      {t("knowledgeOps.requests", { count: compact(item.requests) })}
                    </span>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
                    <span>
                      {t("knowledgeOps.last_asked")}
                      {new Date(item.last_asked_at).toLocaleString()}
                    </span>
                    {item.no_answer_count > 0 ? <span className="text-amber-600">{t("knowledgeOps.no_answer_count", { count: item.no_answer_count })}</span> : null}
                    {item.failed_count > 0 ? <span className="text-destructive">{t("knowledgeOps.failed_count", { count: item.failed_count })}</span> : null}
                    {item.citation_missing_count > 0 ? <span>{t("knowledgeOps.citation_missing_count", { count: item.citation_missing_count })}</span> : null}
                    {item.avg_latency_ms > 0 ? <span>{t("knowledgeOps.avg_latency_count", { count: Math.round(item.avg_latency_ms) })}</span> : null}
                  </div>
                </div>
              ))
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-sm font-medium">
              <Tags className="size-4" /> {t("knowledgeOps.attribution_title")}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-xs text-muted-foreground">{t("knowledgeOps.attribution_subtitle")}</p>
            <div className="grid grid-cols-2 gap-2">
              {ATTRIBUTIONS.map((category) => (
                <div key={category} className="flex items-center justify-between rounded border p-2 text-sm">
                  <span>{t(`knowledgeOps.attribution_${category}`)}</span>
                  <span className="font-medium">{compact(attributionCount(category))}</span>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      </section>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-sm font-medium">
            <CircleAlert className="size-4" /> {t("knowledgeOps.pending_badcases")}
            <span className="ml-1 rounded bg-muted px-1.5 py-0.5 text-xs">{eventTotal}</span>
          </CardTitle>
          <select
            value={attributionFilter}
            onChange={(event) => setAttributionFilter(event.target.value)}
            aria-label={t("knowledgeOps.attribution_title")}
            className="mt-2 w-full max-w-56 rounded border bg-background px-2 py-1 text-sm"
          >
            <option value="">{t("knowledgeOps.attribution_filter_all")}</option>
            {ATTRIBUTIONS.map((category) => (
              <option key={category} value={category}>
                {t(`knowledgeOps.attribution_${category}`)}
              </option>
            ))}
          </select>
        </CardHeader>
        <CardContent className="space-y-3">
          {canManage ? (
            <div className="flex flex-wrap items-center gap-2 rounded border p-2">
              <span className="text-xs text-muted-foreground">{t("knowledgeOps.convert_target")}</span>
              <select
                className="min-w-48 rounded border bg-background px-2 py-1 text-sm"
                value={evalSetId}
                onChange={(event) => setEvalSetId(event.target.value)}
              >
                <option value="">{t("knowledgeOps.select_eval_set")}</option>
                {evalSets.map((item) => (
                  <option key={item.id} value={item.id}>{item.name}</option>
                ))}
              </select>
            </div>
          ) : null}
          {events.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">{t("knowledgeOps.no_pending_badcases")}</p>
          ) : (
            events.map((event) => (
              <div key={event.id} className="rounded border p-3">
                <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                      <span className="rounded bg-muted px-1.5 py-0.5">{event.app_type}</span>
                      <span className={event.status === "failed" ? "text-destructive" : "text-amber-600"}>
                        {t(`knowledgeOps.${statusLabelKey[event.status] ?? "status_completed"}`)}
                      </span>
                      {event.feedback_rating === "negative" ? (
                        <span className="rounded bg-primary/10 px-1.5 py-0.5 text-primary">
                          {t(`knowledgeOps.attribution_${event.feedback_attribution || "unclassified"}`)}
                        </span>
                      ) : null}
                      <span>{new Date(event.created_at).toLocaleString()}</span>
                      <span>{t("knowledgeOps.citations_count", { count: event.citations_count })}</span>
                      <span>{t("knowledgeOps.tokens_count", { count: compact(event.tokens_in + event.tokens_out) })}</span>
                    </div>
                    <p className="mt-2 line-clamp-2 text-sm font-medium">{event.question}</p>
                    {event.answer_excerpt ? (
                      <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{event.answer_excerpt}</p>
                    ) : null}
                  </div>
                  {canManage ? (
                    <div className="flex shrink-0 flex-wrap gap-2">
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={reviewing === event.id}
                        onClick={() => void convertToEval(event)}
                      >
                        {t("knowledgeOps.convert_to_eval")}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={reviewing === event.id}
                        onClick={() => void review(event, "resolved")}
                      >
                        <CheckCircle2 className="size-4" /> {t("knowledgeOps.mark_resolved")}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={reviewing === event.id}
                        onClick={() => void review(event, "ignored")}
                      >
                        {t("knowledgeOps.ignore")}
                      </Button>
                    </div>
                  ) : (
                    <span className="shrink-0 rounded bg-muted px-2 py-0.5 text-xs">
                      {t(`knowledgeOps.${reviewLabelKey[event.review_status] ?? "review_open"}`)}
                    </span>
                  )}
                </div>
              </div>
            ))
          )}
        </CardContent>
      </Card>

      <KnowledgeTasksCard
        tasks={tasks}
        summary={taskSummary}
        canManage={Boolean(canManage)}
        reviewing={reviewing}
        evalSets={evalSets}
        evalSetId={evalSetId}
        onEvalSetIdChange={setEvalSetId}
        onCreateTask={createTask}
        onUpdateTask={updateTask}
      />
      <MetricContractBoard />
      <TraceRunLookup />
    </div>
  );
};
