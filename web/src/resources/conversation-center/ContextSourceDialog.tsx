import { useEffect, useState } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Loader2 } from "lucide-react";
import { api, ApiError } from "../../lib/api";

type ContextDecision = {
  id?: string;
  request_id?: string;
  decision?: string;
  detail?: string;
};

type ContextSourceDialogProps = {
  requestId?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

function decisionDetail(decision?: ContextDecision): Record<string, unknown> {
  if (!decision?.detail) return {};
  try {
    const parsed = JSON.parse(decision.detail);
    return parsed && typeof parsed === "object"
      ? (parsed as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
}

function textValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean")
    return String(value);
  if (Array.isArray(value))
    return value.map((item) => textValue(item)).join("、");
  return "";
}

function targetKindLabel(value: unknown, translate: (key: string) => string): string {
  const targetKind = textValue(value);
  if (!targetKind) return "";
  return translate(`conversationCenter.target_kind_${targetKind}`);
}

export function ContextSourceDialog({
  requestId,
  open,
  onOpenChange,
}: ContextSourceDialogProps) {
  const translate = useTranslate();
  const notify = useNotify();
  const [loading, setLoading] = useState(false);
  const [decision, setDecision] = useState<ContextDecision | null>(null);

  useEffect(() => {
    if (!open || !requestId) return;
    let active = true;
    setLoading(true);
    setDecision(null);
    api
      .get(
        `/conversation-context/decisions/by-request/${encodeURIComponent(requestId)}`,
      )
      .then((result) => {
        if (!active) return;
        setDecision((result?.data?.data ?? null) as ContextDecision | null);
      })
      .catch((error) => {
        if (!active) return;
        notify(
          error instanceof ApiError
            ? error.displayMessage
            : translate("conversationCenter.context_source_failed"),
          {
            type: "error",
          },
        );
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [notify, open, requestId]);

  const detail = decisionDetail(decision ?? undefined);
  const allowed = decision?.decision === "memory_allowed";
  const targetKind = targetKindLabel(detail.target_kind, translate);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent aria-describedby="context-source-description">
        <DialogHeader>
          <DialogTitle>
            {translate("conversationCenter.context_source_title")}
          </DialogTitle>
          <DialogDescription id="context-source-description">
            {translate("conversationCenter.context_source_description")}
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            {translate("conversationCenter.context_source_loading")}
          </div>
        ) : !decision ? (
          <p className="text-sm text-muted-foreground">
            {translate("conversationCenter.context_source_missing")}
          </p>
        ) : (
          <div className="space-y-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={allowed ? "default" : "secondary"}>
                {allowed
                  ? translate("conversationCenter.context_source_allowed")
                  : translate("conversationCenter.context_source_denied")}
              </Badge>
              {textValue(detail.reason) ? (
                <Badge variant="outline">{textValue(detail.reason)}</Badge>
              ) : null}
            </div>
            <dl className="grid gap-2 sm:grid-cols-2">
              {(
                [
                  ["context_sources", detail.context_sources],
                  ["target_kind", targetKind],
                  ["policy_scope", detail.policy_scope],
                  ["policy_generation", detail.policy_generation],
                  ["consent_version", detail.consent_version],
                  ["context_policy_version", detail.context_policy_version],
                  ["release_manifest_status", detail.release_manifest_status],
                  ["memory_retrieved", detail.memory_retrieved],
                  ["memory_result_count", detail.memory_result_count],
                  ["search_side_effect", detail.search_side_effect],
                  ["memory_operation_created", detail.memory_operation_created],
                ] as [string, unknown][]
              ).map(([key, value]) => (
                <div key={key} className="rounded border p-2">
                  <dt className="text-xs text-muted-foreground">
                    {translate(`conversationCenter.context_source_${key}`)}
                  </dt>
                  <dd className="break-words text-sm">
                    {textValue(value) || "—"}
                  </dd>
                </div>
              ))}
            </dl>
            <p className="text-xs text-muted-foreground">
              {translate("conversationCenter.context_source_privacy")}
            </p>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
