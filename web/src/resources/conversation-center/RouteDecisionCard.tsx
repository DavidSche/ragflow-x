import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { ConversationRouteDecision, ConversationRouteStatus } from "./types";

interface RouteDecisionCardProps {
  decision: ConversationRouteDecision;
  status: ConversationRouteStatus;
  autoEligible?: boolean;
  error: string | null;
  onChoose: (candidate: ConversationRouteDecision["candidates"][number]) => void;
  onDismiss: () => void;
  translate: (key: string) => string;
}

export function RouteDecisionCard({ decision, status, autoEligible = false, error, onChoose, onDismiss, translate }: RouteDecisionCardProps) {
  const busy = status === "routing" || status === "selecting" || status === "bootstrapping";
  return (
    <section aria-live="polite" aria-busy={busy} className="rounded-md border bg-background/80 p-3 shadow-sm">
      <div className="mb-2 flex items-center gap-2 text-sm font-medium">
        {status === "routing" ? <Loader2 aria-hidden className="size-4 animate-spin" /> : null}
        {status === "clarify"
          ? translate("conversationCenter.route_clarify_title")
          : status === "routing"
            ? translate("conversationCenter.route_running")
            : translate("conversationCenter.route_title")}
        <span className="ml-auto rounded border px-1.5 py-0.5 text-xs text-muted-foreground">
          {translate("conversationCenter.route_match")}
        </span>
      </div>
      {autoEligible ? (
        <p className="mb-2 text-xs text-muted-foreground">{translate("conversationCenter.route_auto_countdown")}</p>
      ) : null}
      {error ? <p className="mb-2 text-sm text-destructive">{error}</p> : null}
      {!decision.candidates.length ? (
        <p className="text-sm text-muted-foreground">
          {status === "clarify"
            ? translate("conversationCenter.route_clarify_body")
            : translate("conversationCenter.route_empty")}
        </p>
      ) : (
        <ul className="grid gap-2">
          {decision.candidates.map((candidate) => (
            <li key={`${candidate.kind}:${candidate.id}`}>
              <Button type="button" variant="outline" className="h-auto w-full justify-start" disabled={busy} onClick={() => onChoose(candidate)}>
                <span className="min-w-0 text-left">
                  <span className="block truncate text-sm font-medium">{candidate.name}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {translate(`conversationCenter.kind_${candidate.kind}`)} · {Math.round(candidate.normalized_score * 100)} · {candidate.pre_execution_risk}
                  </span>
                </span>
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-2 flex justify-end">
        <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={onDismiss}>
          {translate("conversationCenter.route_dismiss")}
        </Button>
      </div>
    </section>
  );
}
