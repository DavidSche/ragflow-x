import { useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ListChecks } from "lucide-react";

export interface KnowledgeTask {
  id: string;
  source_event_id?: string;
  source_request_id?: string;
  source_attribution?: string;
  title: string;
  description?: string;
  category: string;
  owner_id: string;
  due_at?: string;
  priority: string;
  status: string;
  requires_approval: boolean;
  approval_id?: string;
  resolution_note?: string;
  regression_eval_set_id?: string;
  regression_eval_case_id?: string;
  regression_status: string;
  created_at: string;
}

export interface KnowledgeTaskSummary {
  open: number;
  in_progress: number;
  blocked: number;
  pending_approval: number;
  resolved: number;
  canceled: number;
  overdue: number;
}

interface EvalSetOption {
  id: string;
  name: string;
}

interface KnowledgeTasksCardProps {
  tasks: KnowledgeTask[];
  summary: KnowledgeTaskSummary;
  canManage: boolean;
  reviewing: string | null;
  evalSets: EvalSetOption[];
  evalSetId: string;
  onEvalSetIdChange: (value: string) => void;
  onCreateTask: (form: KnowledgeTaskForm) => Promise<void>;
  onUpdateTask: (
    task: KnowledgeTask,
    status: "in_progress" | "resolved" | "canceled",
    regression: { evalSetId: string; regressionCaseId: string },
  ) => Promise<void>;
}

export interface KnowledgeTaskForm {
  source_event_id: string;
  title: string;
  description: string;
  category: string;
  owner_id: string;
  due_at: string;
  priority: string;
  requires_approval: boolean;
}

const ATTRIBUTIONS = ["knowledge", "retrieval", "template", "model", "routing", "tool"];
const TASK_PRIORITIES = ["low", "medium", "high", "critical"];
const emptyForm: KnowledgeTaskForm = {
  source_event_id: "", title: "", description: "", category: "knowledge",
  owner_id: "", due_at: "", priority: "medium", requires_approval: false,
};

export function KnowledgeTasksCard({
  tasks, summary, canManage, reviewing, evalSets, evalSetId,
  onEvalSetIdChange, onCreateTask, onUpdateTask,
}: KnowledgeTasksCardProps) {
  const t = useTranslate();
  const [form, setForm] = useState<KnowledgeTaskForm>(emptyForm);
  const [regressionCaseId, setRegressionCaseId] = useState("");

  const submit = async () => {
    await onCreateTask(form);
    setForm(emptyForm);
  };

  const update = (task: KnowledgeTask, status: "in_progress" | "resolved" | "canceled") =>
    onUpdateTask(task, status, { evalSetId, regressionCaseId });

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <ListChecks className="size-4" /> {t("knowledgeOps.task_title")}
          <span className="ml-1 rounded bg-muted px-1.5 py-0.5 text-xs">
            {t("knowledgeOps.task_summary", {
              open: summary.open,
              in_progress: summary.in_progress,
              overdue: summary.overdue,
              pending_approval: summary.pending_approval,
            })}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {canManage ? (
          <form
            className="grid gap-2 rounded border p-3 md:grid-cols-2 xl:grid-cols-4"
            onSubmit={(event) => {
              event.preventDefault();
              void submit();
            }}
          >
            <input
              className="rounded border bg-background px-2 py-1 text-sm"
              value={form.source_event_id}
              onChange={(event) => setForm((prev) => ({ ...prev, source_event_id: event.target.value }))}
              placeholder={t("knowledgeOps.task_source_event")}
              aria-label={t("knowledgeOps.task_source_event")}
            />
            <input
              className="rounded border bg-background px-2 py-1 text-sm"
              value={form.title}
              onChange={(event) => setForm((prev) => ({ ...prev, title: event.target.value }))}
              placeholder={t("knowledgeOps.task_title_label")}
              aria-label={t("knowledgeOps.task_title_label")}
              required
            />
            <input
              className="rounded border bg-background px-2 py-1 text-sm"
              value={form.owner_id}
              onChange={(event) => setForm((prev) => ({ ...prev, owner_id: event.target.value }))}
              placeholder={t("knowledgeOps.task_owner")}
              aria-label={t("knowledgeOps.task_owner")}
              required
            />
            <select
              className="rounded border bg-background px-2 py-1 text-sm"
              value={form.category}
              onChange={(event) => setForm((prev) => ({ ...prev, category: event.target.value }))}
              aria-label={t("knowledgeOps.task_category")}
            >
              {ATTRIBUTIONS.map((category) => (
                <option key={category} value={category}>{t(`knowledgeOps.attribution_${category}`)}</option>
              ))}
            </select>
            <select
              className="rounded border bg-background px-2 py-1 text-sm"
              value={form.priority}
              onChange={(event) => setForm((prev) => ({ ...prev, priority: event.target.value }))}
              aria-label={t("knowledgeOps.task_priority")}
            >
              {TASK_PRIORITIES.map((priority) => (
                <option key={priority} value={priority}>{t(`knowledgeOps.task_priority_${priority}`)}</option>
              ))}
            </select>
            <input
              className="rounded border bg-background px-2 py-1 text-sm md:col-span-2"
              value={form.description}
              onChange={(event) => setForm((prev) => ({ ...prev, description: event.target.value }))}
              placeholder={t("knowledgeOps.task_description")}
              aria-label={t("knowledgeOps.task_description")}
            />
            <input
              type="datetime-local"
              className="rounded border bg-background px-2 py-1 text-sm"
              value={form.due_at}
              onChange={(event) => setForm((prev) => ({ ...prev, due_at: event.target.value }))}
              aria-label={t("knowledgeOps.task_due_at")}
            />
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.requires_approval}
                onChange={(event) => setForm((prev) => ({ ...prev, requires_approval: event.target.checked }))}
              />
              {t("knowledgeOps.task_requires_approval")}
            </label>
            <Button type="submit" size="sm" disabled={reviewing === "task-create"}>
              {t("knowledgeOps.task_create")}
            </Button>
          </form>
        ) : null}

        {canManage ? (
          <div className="grid gap-2 rounded border p-2 md:grid-cols-2">
            <select
              className="rounded border bg-background px-2 py-1 text-sm"
              value={evalSetId}
              onChange={(event) => onEvalSetIdChange(event.target.value)}
              aria-label={t("knowledgeOps.select_eval_set")}
            >
              <option value="">{t("knowledgeOps.select_eval_set")}</option>
              {evalSets.map((item) => (
                <option key={item.id} value={item.id}>{item.name}</option>
              ))}
            </select>
            <input
              className="rounded border bg-background px-2 py-1 text-sm"
              value={regressionCaseId}
              onChange={(event) => setRegressionCaseId(event.target.value)}
              placeholder={t("knowledgeOps.task_regression_case")}
              aria-label={t("knowledgeOps.task_regression_case")}
            />
          </div>
        ) : null}

        {tasks.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">{t("knowledgeOps.task_empty")}</p>
        ) : (
          tasks.map((task) => (
            <div key={task.id} className="rounded border p-3">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                    <span>{t(`knowledgeOps.attribution_${task.category}`)}</span>
                    <span>{t(`knowledgeOps.task_status_${task.status}`)}</span>
                    <span>{t(`knowledgeOps.task_priority_${task.priority}`)}</span>
                    <span>{t("knowledgeOps.task_owner")}: {task.owner_id}</span>
                    {task.due_at ? <span>{new Date(task.due_at).toLocaleString()}</span> : null}
                    {task.regression_eval_case_id ? (
                      <span>{t("knowledgeOps.task_regression_case")}: {task.regression_eval_case_id}</span>
                    ) : null}
                  </div>
                  <p className="mt-1 text-sm font-medium">{task.title}</p>
                  {task.description ? <p className="mt-1 text-xs text-muted-foreground">{task.description}</p> : null}
                </div>
                  {canManage && (task.status === "open" || task.status === "in_progress" || task.status === "blocked") ? (
                  <div className="flex shrink-0 flex-wrap gap-2">
                    {task.status !== "in_progress" ? (
                      <Button size="sm" variant="secondary" disabled={reviewing === `task-${task.id}`} onClick={() => void update(task, "in_progress")}>
                        {t("knowledgeOps.task_start")}
                      </Button>
                    ) : null}
                    <Button size="sm" variant="outline" disabled={reviewing === `task-${task.id}`} onClick={() => void update(task, "resolved")}>
                      {t("knowledgeOps.task_resolve")}
                    </Button>
                    <Button size="sm" variant="ghost" disabled={reviewing === `task-${task.id}`} onClick={() => void update(task, "canceled")}>
                      {t("knowledgeOps.task_cancel")}
                    </Button>
                  </div>
                ) : null}
              </div>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}
