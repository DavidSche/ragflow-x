import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useGetIdentity, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Loader2, RefreshCw, ShieldOff } from "lucide-react";
import { api, ApiError, unwrap, type ApiEnvelope } from "@/lib/api";

interface MemoryOperation {
  id: string;
  operation_type: string;
  state: string;
  policy_id: string;
  memory_id: string;
  assistant_id: string;
  project_id: string;
  tombstone_active: boolean;
  user_id?: string;
  policy_generation: number;
  consent_version: number;
  outbox_event_id?: string;
  request_id?: string;
  payload_hash?: string;
  reason_code?: string;
  created_at: string;
}

interface PageEnvelope<T> {
  items: T[];
  total: number;
}

const memoryOperationStateKeys: Record<string, string> = {
  accepted: "memoryOperation.state_accepted",
  admitted: "memoryOperation.state_admitted",
  dispatched: "memoryOperation.state_dispatched",
  executed: "memoryOperation.state_executed",
  failed: "memoryOperation.state_failed",
  unknown: "memoryOperation.state_unknown",
  reconciled: "memoryOperation.state_reconciled",
  blocked: "memoryOperation.state_blocked",
  skipped: "memoryOperation.state_skipped",
};

const memoryOperationReasonKeys: Record<string, string> = {
  memory_dispatch_provider_unconfirmed: "memoryOperation.reason_provider_unconfirmed",
  memory_dispatch_envelope_unreadable: "memoryOperation.reason_envelope_unreadable",
  memory_dispatch_envelope_invalid: "memoryOperation.reason_envelope_invalid",
  memory_dispatch_tombstone_active: "memoryOperation.reason_tombstone_active",
  memory_dispatch_only_write_supported: "memoryOperation.reason_only_write_supported",
  memory_dispatch_policy_not_active: "memoryOperation.reason_policy_not_active",
  memory_dispatch_policy_not_found: "memoryOperation.reason_policy_not_found",
  memory_dispatch_policy_generation_stale: "memoryOperation.reason_policy_generation_stale",
  memory_dispatch_consent_not_current: "memoryOperation.reason_consent_not_current",
  memory_dispatch_scope_mismatch: "memoryOperation.reason_scope_mismatch",
  memory_dispatch_write_policy_denied: "memoryOperation.reason_write_policy_denied",
  memory_dispatch_memory_binding_mismatch: "memoryOperation.reason_memory_binding_mismatch",
  memory_dispatch_principal_not_active: "memoryOperation.reason_principal_not_active",
  memory_dispatch_principal_not_project_member: "memoryOperation.reason_principal_not_project_member",
  memory_tombstone_cancelled: "memoryOperation.reason_tombstone_cancelled",
  memory_tombstone_compensation_required: "memoryOperation.reason_compensation_required",
  memory_dispatch_external_message_unconfirmed: "memoryOperation.reason_external_message_unconfirmed",
  memory_dispatch_external_message_absent: "memoryOperation.reason_external_message_absent",
  memory_dispatch_external_accepted: "memoryOperation.reason_external_accepted",
  memory_tombstone_compensated: "memoryOperation.reason_tombstone_compensated",
  memory_tombstone_external_absent: "memoryOperation.reason_tombstone_external_absent",
};

export const MemoryOperationPanel = ({ memoryId }: { memoryId?: string }) => {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess } = useCanAccess({ resource: "memory-operation", action: "read" });
  const { canAccess: canTombstone } = useCanAccess({ resource: "memory-forget", action: "execute" });
  const { data: identity } = useGetIdentity();
  const [operations, setOperations] = useState<MemoryOperation[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [tombstoningId, setTombstoningId] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!canAccess) return;
    setLoading(true);
    try {
      const params = new URLSearchParams({ page: "1", page_size: "20" });
      if (memoryId) params.set("memory_id", memoryId);
      const response = await unwrap(api.get<ApiEnvelope<PageEnvelope<MemoryOperation>>>(`/memory-operations?${params.toString()}`));
      setOperations(response.items ?? []);
      setTotal(response.total ?? 0);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryOperation.load_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  }, [canAccess, memoryId, notify, t]);

  const tombstone = useCallback(async (operation: MemoryOperation) => {
    if (!window.confirm(t("memoryOperation.tombstone_confirm"))) return;
    setTombstoningId(operation.id);
    try {
      const response = await unwrap(api.post<ApiEnvelope<MemoryOperation>>(
        `/memory-operations/${operation.id}/tombstone`,
      ));
      const updated = response;
      setOperations((items) => items.map((item) => (item.id === operation.id ? updated : item)));
      notify(t("memoryOperation.tombstone_requested"), { type: "success" });
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryOperation.tombstone_failed"), { type: "error" });
    } finally {
      setTombstoningId(null);
    }
  }, [notify, t]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!canAccess) return null;

  return (
    <Card className="mt-4">
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>{t("memoryOperation.title")}</CardTitle>
        <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
          {loading ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <RefreshCw className="size-4" aria-hidden="true" />}
          {t("memoryOperation.refresh")}
        </Button>
      </CardHeader>
      <CardContent className="space-y-2">
        <div className="text-sm text-muted-foreground">{t("memoryOperation.metadata_only")}</div>
        {loading && operations.length === 0 ? (
          <div className="text-sm text-muted-foreground">{t("common.loading")}</div>
        ) : operations.length === 0 ? (
          <div className="text-sm text-muted-foreground">{t("memoryOperation.empty")}</div>
        ) : (
          <ul className="space-y-2" aria-label={t("memoryOperation.title")}>
            {operations.map((operation) => (
              <li key={operation.id} className="rounded border p-2 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{operation.operation_type}</span>
                  <span>{t(memoryOperationStateKeys[operation.state] ?? "memoryOperation.state_unknown")}</span>
                  <span>{operation.assistant_id}</span>
                  <span>{t("memoryOperation.tombstone")}: {operation.tombstone_active ? "true" : "false"}</span>
                  {canTombstone && identity?.id === operation.user_id && !operation.tombstone_active ? (
                    <Button
                      size="sm"
                      variant="outline"
                      className="ml-auto"
                      onClick={() => void tombstone(operation)}
                      disabled={loading || tombstoningId === operation.id}
                      aria-label={`${t("memoryOperation.tombstone_action")} ${operation.id}`}
                    >
                      {tombstoningId === operation.id ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <ShieldOff className="size-4" aria-hidden="true" />}
                      {t("memoryOperation.tombstone_action")}
                    </Button>
                  ) : null}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  {t("memoryOperation.policy")}: {operation.policy_id || "—"} · {t("memoryOperation.generation")}: {operation.policy_generation} · {t("memoryOperation.consent")}: {operation.consent_version}
                </div>
                <div className="text-xs text-muted-foreground">
                  {t("memoryOperation.request")}: {operation.request_id || "—"} · {t("memoryOperation.outbox")}: {operation.outbox_event_id || "—"}
                  {operation.reason_code ? ` · ${t("memoryOperation.reason")}: ${memoryOperationReasonKeys[operation.reason_code] ? t(memoryOperationReasonKeys[operation.reason_code]) : operation.reason_code}` : ""}
                </div>
                <div className="text-xs text-muted-foreground">
                  {t("memoryOperation.payload_hash")}: {operation.payload_hash || "—"}
                </div>
                <div className="text-xs text-muted-foreground">{new Date(operation.created_at).toLocaleString()}</div>
              </li>
            ))}
          </ul>
        )}
        {total > operations.length ? (
          <div className="text-xs text-muted-foreground">{t("memoryOperation.more", { count: total - operations.length })}</div>
        ) : null}
      </CardContent>
    </Card>
  );
};
