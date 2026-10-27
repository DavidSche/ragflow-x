import { useEffect, useState } from "react";
import { useCanAccess, useCreatePath, usePermissions, useTranslate } from "ra-core";
import { Link } from "react-router-dom";
import { BookOpenCheck, CheckCircle2, Circle, MessageSquare, Rocket, FlaskConical, Info } from "lucide-react";
import { getOnboardingReadiness, type OnboardingReadiness } from "../trial-runs/types";

type ReadinessKey = "dataset_ready" | "assistant_ready" | "trial_completed" | "first_cited_answer";

interface ReadinessStep {
  key: ReadinessKey;
  resource: string;
  icon: typeof BookOpenCheck;
  /**
   * Permission that makes the step reachable. Steps whose gate the viewer does
   * not hold are hidden instead of rendered as a link that lands on 403 —
   * doc/228 §3.1 T7.2 and the v0.4.0 exit criterion "business_user 无 403 旅程".
   */
  gate?: "dataset" | "create" | "trial";
}

const STEPS: ReadinessStep[] = [
  { key: "dataset_ready", resource: "datasets", icon: BookOpenCheck, gate: "dataset" },
  { key: "assistant_ready", resource: "scenario-templates", icon: Rocket, gate: "create" },
  { key: "trial_completed", resource: "trial-runs", icon: FlaskConical, gate: "trial" },
  { key: "first_cited_answer", resource: "conversation-center", icon: MessageSquare },
];

/**
 * Empty state for the conversation center: shows the four onboarding milestones
 * (doc/228 T7.1′) as deep links plus starter questions, so a brand-new tenant is
 * never dropped into a blank chat box.
 *
 * The empty state is permission-aware: creation and trial-run milestones are
 * only offered to roles that hold them. A business user instead gets "view the
 * assistants I can use" plus a request path through the existing approval chain
 * (no new resource, no 403 page).
 */
export function AssistantEmptyState({ assistantId }: { assistantId?: string }) {
  const t = useTranslate();
  const createPath = useCreatePath();
  const { data: permissions } = usePermissions();
  const { canAccess: canReadDatasets } = useCanAccess({ resource: "dataset", action: "read" });
  const { canAccess: canCreateAssistant } = useCanAccess({ resource: "chat", action: "manage" });
  const { canAccess: canReadTemplates } = useCanAccess({ resource: "scenario-template", action: "read" });
  const { canAccess: canRaiseApproval } = useCanAccess({ resource: "approval", action: "execute" });
  const [readiness, setReadiness] = useState<OnboardingReadiness | null>(null);
  const [copied, setCopied] = useState(false);

  const canBuild = canCreateAssistant === true;
  const canTrial = canReadTemplates === true;
  const canBrowseDatasets = canReadDatasets === true;
  const approvalsEnabled = (permissions as { approvalsEnabled?: boolean } | undefined)?.approvalsEnabled === true;
  // POST /approvals requires approval:execute, which business_user does not hold
  // (doc/240). Linking them there would be a 403 dead end, so they get a
  // copyable request note instead.
  const canSubmitApproval = canRaiseApproval === true && approvalsEnabled;

  useEffect(() => {
    // /onboarding/readiness itself requires scenario-template:read: do not fire a
    // request the caller is guaranteed to be denied.
    if (!assistantId || !canTrial) return;
    let mounted = true;
    getOnboardingReadiness(assistantId)
      .then((data) => {
        if (mounted) setReadiness(data);
      })
      .catch(() => {
        if (mounted) setReadiness(null);
      });
    return () => {
      mounted = false;
    };
  }, [assistantId, canTrial]);

  const done = new Map((readiness?.milestones ?? []).map((item) => [item.key, item.done]));
  const steps = STEPS.filter((step) => {
    switch (step.gate) {
      case "create":
        return canBuild;
      case "trial":
        return canTrial;
      case "dataset":
        return canBrowseDatasets;
      default:
        // No gate: the step is the current page or universally readable.
        return true;
    }
  });

  return (
    <div className="mb-4 rounded border bg-muted/30 p-4">
      <h2 className="text-lg font-semibold">{t("conversationCenter.empty_question")}</h2>
      <p className="mt-1 text-sm text-muted-foreground">{t("scenarioTemplates.emptyState.subtitle")}</p>

      {readiness ? (
        <p className="mt-2 text-xs text-muted-foreground">
          {t("scenarioTemplates.readiness.progress", {
            completed: readiness.completed,
            total: readiness.total,
          })}
        </p>
      ) : null}

      <ol className="mt-3 grid gap-2 sm:grid-cols-2">
        {steps.map((step, index) => {
          const Icon = step.icon;
          const isDone = done.get(step.key) === true;
          return (
            <li key={step.key}>
              <Link
                to={createPath({ resource: step.resource, type: "list" })}
                className="flex items-start gap-2 rounded border bg-background p-2 text-sm transition-colors hover:bg-muted/40"
              >
                <span className="mt-0.5" aria-hidden="true">
                  {isDone ? (
                    <CheckCircle2 className="size-4 text-emerald-600" />
                  ) : (
                    <Circle className="size-4 text-muted-foreground" />
                  )}
                </span>
                <span className="min-w-0">
                  <span className="flex items-center gap-1.5 font-medium">
                    <Icon className="size-3.5" aria-hidden="true" />
                    {index + 1}. {t(`scenarioTemplates.readiness.${step.key}`)}
                  </span>
                  <span className="mt-0.5 block text-xs text-muted-foreground">
                    {t(`scenarioTemplates.readiness.${step.key}Hint`)}
                  </span>
                </span>
              </Link>
            </li>
          );
        })}
      </ol>

      {canCreateAssistant === false ? (
        <div className="mt-3 rounded border border-dashed bg-background p-3">
          <p className="flex items-center gap-1.5 text-sm font-medium">
            <Info className="size-3.5 text-muted-foreground" aria-hidden="true" />
            {t("conversationCenter.requestAssistant.title")}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">{t("conversationCenter.requestAssistant.hint")}</p>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-sm">
            <Link
              to={createPath({ resource: "conversation-center", type: "list" })}
              className="rounded border bg-background px-2 py-1 font-medium hover:bg-muted/40"
            >
              {t("conversationCenter.requestAssistant.viewMine")}
            </Link>
            {canSubmitApproval ? (
              <Link
                to={createPath({ resource: "approvals", type: "create" })}
                className="rounded border bg-background px-2 py-1 font-medium hover:bg-muted/40"
              >
                {t("conversationCenter.requestAssistant.request")}
              </Link>
            ) : approvalsEnabled ? (
              <button
                type="button"
                className="rounded border bg-background px-2 py-1 font-medium hover:bg-muted/40"
                onClick={() => {
                  const body = t("conversationCenter.requestAssistant.copyBody");
                  void navigator.clipboard?.writeText(body).catch(() => undefined);
                  setCopied(true);
                }}
              >
                {copied
                  ? t("conversationCenter.requestAssistant.copied")
                  : t("conversationCenter.requestAssistant.copyRequest")}
              </button>
            ) : (
              <span className="text-xs text-muted-foreground">
                {t("conversationCenter.requestAssistant.unavailable")}
              </span>
            )}
          </div>
        </div>
      ) : null}

      <ul className="mt-3 grid gap-1 text-sm text-muted-foreground sm:grid-cols-2">
        <li>{t("conversationCenter.example_1")}</li>
        <li>{t("conversationCenter.example_2")}</li>
        <li>{t("conversationCenter.example_3")}</li>
        <li>{t("conversationCenter.example_4")}</li>
      </ul>
    </div>
  );
}
