import { useTranslate } from "ra-core";
import { CircleCheck, CircleX, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ConversationToolCall, Message } from "./workbench-types";

interface AgentExecutionTimelineProps {
  message: Message;
  isStreaming: boolean;
}

type TimelineStep = {
  key: string;
  label: string;
  summary?: string;
  status: "running" | "success" | "failed";
};

function toolCallStatus(
  toolCall: ConversationToolCall,
  fallback: TimelineStep["status"],
): TimelineStep["status"] {
  if (toolCall.status === "success" || toolCall.status === "failed" || toolCall.status === "running") {
    return toolCall.status;
  }
  return fallback;
}

export function AgentExecutionTimeline({ message, isStreaming }: AgentExecutionTimelineProps) {
  const translate = useTranslate();
  const failed = Boolean(message.error) || message.status === "failed";
  const defaultStatus = failed ? "failed" : isStreaming ? "running" : "success";
  const steps: TimelineStep[] = (message.toolCalls ?? []).map((toolCall, index) => ({
    key: `${index}:${toolCall.name ?? "tool"}`,
    label: toolCall.name || translate("workbench.agent_execution_tool"),
    summary: toolCall.summary,
    status: toolCallStatus(toolCall, defaultStatus),
  }));
  steps.push({
    key: "generate-result",
    label: translate("workbench.agent_execution_generate"),
    status: defaultStatus,
  });

  return (
    <section className="mt-2 rounded-md border bg-background/50 p-2" aria-label={translate("workbench.agent_execution_timeline")}>
      <p className="mb-1 text-xs font-medium text-muted-foreground">{translate("workbench.agent_execution_timeline")}</p>
      <ol className="space-y-1.5">
        {steps.map((step) => (
          <li key={step.key} className="flex min-w-0 items-start gap-2 text-xs">
            <span
              className={cn(
                "mt-0.5 flex size-4 shrink-0 items-center justify-center",
                step.status === "success" && "text-emerald-600",
                step.status === "running" && "text-primary",
                step.status === "failed" && "text-destructive",
              )}
              aria-hidden="true"
            >
              {step.status === "success" ? <CircleCheck className="size-3.5" /> : null}
              {step.status === "running" ? <Loader2 className="size-3.5 animate-spin" /> : null}
              {step.status === "failed" ? <CircleX className="size-3.5" /> : null}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate font-medium">{step.label}</span>
              {step.summary ? <span className="block truncate text-muted-foreground">{step.summary}</span> : null}
            </span>
            <span className="sr-only">
              {step.status === "success"
                ? translate("workbench.agent_execution_success")
                : step.status === "failed"
                  ? translate("workbench.agent_execution_failed")
                  : translate("workbench.agent_execution_running")}
            </span>
          </li>
        ))}
      </ol>
    </section>
  );
}
