import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";

export const APPROVAL_STATUSES = [
  "pending_approval",
  "approved",
  "rejected",
  "canceled",
  "expired",
  "executing",
  "completed",
  "execution_failed",
] as const;

export const APPROVAL_OBJECTS = [
	"dataset",
	"document",
	"document-chunk",
	"api-key",
	"chat",
	"agent",
	"model-provider",
  "model-instance",
  "model-model",
] as const;

export const APPROVAL_ACTIONS = ["create", "update", "delete", "revoke", "test", "parse", "stop", "enable", "disable"] as const;

export const APPROVAL_APPROVER_TYPES = ["user", "role", "team"] as const;

export const APPROVAL_CONDITION_OPS = ["eq", "neq", "prefix", "in", "exists"] as const;

export const approvalStatusVariant = (
  status: string,
): "default" | "secondary" | "destructive" | "outline" => {
  switch (status) {
    case "pending_approval":
      return "secondary";
    case "completed":
      return "default";
    case "rejected":
    case "canceled":
    case "expired":
    case "execution_failed":
      return "destructive";
    default:
      return "outline";
  }
};

export const approvalStepStatusVariant = (
  status: string,
): "default" | "secondary" | "destructive" | "outline" => {
  switch (status) {
    case "current":
      return "secondary";
    case "approved":
      return "default";
    case "rejected":
    case "expired":
      return "destructive";
    default:
      return "outline";
  }
};

export function parseJSONValue<T>(raw: string | undefined, fallback: T): T {
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

export function JSONSection({
  title,
  value,
  fallbackLabel,
}: {
  title: string;
  value: string | undefined;
  fallbackLabel: string;
}) {
  const parsed = parseJSONValue<Record<string, unknown>>(value, {});
  const text = Object.keys(parsed).length ? JSON.stringify(parsed, null, 2) : "";
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">{title}</h3>
      {text ? (
        <pre className="max-h-80 overflow-auto rounded-md bg-muted p-3 text-xs leading-5">{text}</pre>
      ) : (
        <p className="text-sm text-muted-foreground">{fallbackLabel}</p>
      )}
    </section>
  );
}

export function StatusBadge({
  status,
  labels,
}: {
  status: string;
  labels: Record<string, string>;
}) {
  return (
    <Badge variant={approvalStatusVariant(status)}>
      {labels[status] ?? status}
    </Badge>
  );
}

export function InfoItem({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={className}>
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm">{children}</dd>
    </div>
  );
}
