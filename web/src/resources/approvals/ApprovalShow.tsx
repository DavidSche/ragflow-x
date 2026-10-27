import { useState } from "react";
import { Show } from "@/components/admin";
import { useCanAccess, useGetIdentity, useNavigate, useNotify, useRecordContext, useRefresh, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { ArrowLeft, Check, Plus, RotateCcw, X } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import {
  APPROVAL_STATUSES,
  approvalStepStatusVariant,
  InfoItem,
  JSONSection,
  parseJSONValue,
  StatusBadge,
} from "./approval-ui";
import { UserNameLabel } from "@/components/admin/ReferenceLabel";
import {
  type ApprovalApproverSpec,
  type ApprovalRecord,
  type ApprovalStepRecord,
} from "./approval-types";

type ApprovalDecision = "approve" | "reject" | "cancel" | "retry";

const formatDateTime = (value?: string | null) =>
  value ? new Date(value).toLocaleString() : "-";

const ApprovalActionDialog = ({
  approval,
  decision,
  onClose,
  onSuccess,
}: {
  approval: ApprovalRecord;
  decision: ApprovalDecision;
  onClose: () => void;
  onSuccess?: () => void;
}) => {
  const notify = useNotify();
  const t = useTranslate();
  const [comment, setComment] = useState("");
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    if (!comment.trim()) return;
    setSaving(true);
    try {
      await api.post(`/approvals/${approval.id}/${decision}`, { comment: comment.trim() });
      const successKey: Record<ApprovalDecision, string> = {
        approve: "approvals.approved",
        reject: "approvals.rejected",
        cancel: "approvals.canceled",
        retry: "approvals.retried",
      };
      notify(t(successKey[decision]), { type: "success" });
      onSuccess?.();
      onClose();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.action_fail"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  const titles: Record<ApprovalDecision, string> = {
    approve: t("approvals.approve_title"),
    reject: t("approvals.reject_title"),
    cancel: t("approvals.cancel_title"),
    retry: t("approvals.retry_title"),
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{titles[decision]}</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">{approval.title}</p>
        <Textarea
          value={comment}
          onChange={(event) => setComment(event.target.value)}
          placeholder={t("approvals.comment_placeholder")}
          aria-label={t("approvals.comment")}
          rows={4}
        />
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            {t("ra.action.cancel")}
          </Button>
          <Button onClick={submit} disabled={saving || !comment.trim()}>
            {t("ra.action.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};

const ApprovalActionButtons = () => {
  const record = useRecordContext<ApprovalRecord>();
  const { data: identity } = useGetIdentity();
  const { canAccess: canManageApprovals } = useCanAccess({
    resource: "approval",
    action: "manage",
  });
  const refresh = useRefresh();
  const t = useTranslate();
  const [decision, setDecision] = useState<ApprovalDecision | null>(null);

  if (!record) return null;
  const isRequester = !!identity?.id && identity.id === record.requester_id;
  const canDecide = canManageApprovals && record.status === "pending_approval";
  const canCancel =
    record.status === "pending_approval" && (isRequester || !!canManageApprovals);
  const canRetry = canManageApprovals && record.status === "execution_failed";
  if (!canDecide && !canCancel && !canRetry) return null;

  const refreshCurrentRecord = async () => {
    refresh();
  };

  return (
    <div className="flex flex-wrap gap-2">
      {canDecide && (
        <>
          <Button size="sm" onClick={() => setDecision("approve")}>
            <Check className="size-4" /> {t("approvals.approve_label")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setDecision("reject")}>
            <X className="size-4" /> {t("approvals.reject_label")}
          </Button>
        </>
      )}
      {canCancel && (
        <Button size="sm" variant="outline" onClick={() => setDecision("cancel")}>
          {t("approvals.cancel_label")}
        </Button>
      )}
      {canRetry && (
        <Button size="sm" variant="outline" onClick={() => setDecision("retry")}>
          <RotateCcw className="size-4" /> {t("approvals.retry_label")}
        </Button>
      )}
      {decision && (
        <ApprovalActionDialog
          approval={record}
          decision={decision}
          onClose={() => setDecision(null)}
          onSuccess={refreshCurrentRecord}
        />
      )}
    </div>
  );
};

const StepTimeline = ({ steps }: { steps?: ApprovalStepRecord[] }) => {
  const t = useTranslate();
  if (!steps?.length) {
    return <p className="text-sm text-muted-foreground">{t("approvals.no_steps")}</p>;
  }
  const approverText = (step: ApprovalStepRecord) => {
    const approvers = step.approvers_json
      ? parseJSONValue<ApprovalApproverSpec[]>(step.approvers_json, [])
      : [];
    if (approvers.length > 0) {
      return approvers.map((approver) => `${approver.type}:${approver.value}`).join(", ");
    }
    return `${step.approver_type}:${step.approver_value}`;
  };
  return (
    <ol className="space-y-3">
      {steps.map((step) => (
        <li key={step.id} className="relative border-l pl-4">
          <span className="absolute -left-1.5 top-2 size-3 rounded-full bg-border" />
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">
              {step.step_no}. {step.name}
            </span>
            <Badge variant={approvalStepStatusVariant(step.status)}>
              {t(`approvals.step_status.${step.status}`)}
            </Badge>
          </div>
          <div className="mt-1 grid gap-2 text-sm text-muted-foreground md:grid-cols-2">
            <span>{t("approvals.policies.approvers")}: {approverText(step)}</span>
            <span>
              {t("approvals.policies.approval_mode_title")}:{" "}
              {t(step.approval_mode === "all" ? "approvals.policies.approval_mode.all" : "approvals.policies.approval_mode.any")}
            </span>
            {step.approval_mode === "all" && (
              <span>{t("approvals.policies.required_approvals")}: {step.required_approvals || 1}</span>
            )}
            <span>{t("approvals.acted_at")}: {formatDateTime(step.acted_at)}</span>
            {step.acted_by && <span>{t("approvals.acted_by")}: <UserNameLabel id={step.acted_by} /></span>}
            <span>{t("approvals.due_at")}: {formatDateTime(step.due_at)}</span>
          </div>
          {step.actors?.length ? (
            <ul className="mt-2 space-y-1">
              {step.actors.map((actor) => (
                <li key={actor.id} className="rounded-md bg-muted p-2 text-xs">
                  <span className="font-medium"><UserNameLabel id={actor.actor_id} /></span>
                  {" · "}
                  {t(actor.decision === "reject" ? "approvals.reject_label" : "approvals.approve_label")}
                  {actor.delegated_by && (
                    <span> · {t("approvals.delegations.delegated_by")}: <UserNameLabel id={actor.delegated_by} /></span>
                  )}
                  <span> · {formatDateTime(actor.acted_at)}</span>
                  {actor.comment && <span className="block">{actor.comment}</span>}
                </li>
              ))}
            </ul>
          ) : null}
          {step.comment && (
            <p className="mt-2 rounded-md bg-muted p-2 text-sm">{step.comment}</p>
          )}
        </li>
      ))}
    </ol>
  );
};

const businessTarget = (objectType: string, objectID: string): string => {
  switch (objectType) {
    case "dataset":
      return objectID ? `/datasets/${objectID}/show` : "/datasets";
    case "api-key":
      return "/api-keys";
    case "model-provider":
    case "model-instance":
      return "/model-providers";
    default:
      return "/approvals";
  }
};

const BusinessGuidance = () => {
  const record = useRecordContext<ApprovalRecord>();
  const navigate = useNavigate();
  const t = useTranslate();
  if (!record) return null;
  const target = businessTarget(record.object_type, record.object_id);
  const status = record.status;
  const needsAdjustment = status === "rejected" || status === "canceled" || status === "expired";
  const failed = status === "execution_failed";
  const completed = status === "completed";

  return (
    <div className="rounded-lg border bg-card p-4">
      <h3 className="text-base font-semibold">{t("approvals.business_guidance.title")}</h3>
      <p className="mt-1 text-sm text-muted-foreground">
        {failed
          ? t("approvals.business_guidance.failed")
          : needsAdjustment
            ? t("approvals.business_guidance.adjust")
            : t("approvals.business_guidance.track")}
      </p>
      <div className="mt-3 flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => navigate(target)}>
          <ArrowLeft className="size-4" /> {t("approvals.business_guidance.source")}
        </Button>
        {(needsAdjustment || failed) && (
          <Button onClick={() => navigate("/approvals/create")}>
            <Plus className="size-4" /> {t("approvals.business_guidance.resubmit")}
          </Button>
        )}
        {completed && <Button onClick={() => navigate("/approvals")}>{t("approvals.business_guidance.list")}</Button>}
      </div>
    </div>
  );
};

export const ApprovalShow = () => {
  const record = useRecordContext<ApprovalRecord>();
  const t = useTranslate();
  const statusLabels = Object.fromEntries(
    APPROVAL_STATUSES.map((status) => [status, t(`approvals.status.${status}`)]),
  );

  return (
    <Show title={t("approvals.detail_title")} actions={<ApprovalActionButtons />}>
      <div className="space-y-6">
        <div className="rounded-lg border bg-card p-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 className="text-xl font-semibold">{record?.title}</h2>
              <p className="mt-1 text-sm text-muted-foreground">{record?.request_no}</p>
            </div>
            {record && <StatusBadge status={record.status} labels={statusLabels} />}
          </div>
          <dl className="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            <InfoItem label={t("approvals.object_type")}>{record?.object_type}</InfoItem>
            <InfoItem label={t("approvals.action")}>{record?.action}</InfoItem>
            <InfoItem label={t("approvals.object_id")}>
              <span className="break-all">{record?.title || record?.object_id}</span>
            </InfoItem>
            <InfoItem label={t("approvals.current_step")}>{record?.current_step}</InfoItem>
            <InfoItem label={t("approvals.requester_id")}><UserNameLabel id={record?.requester_id} /></InfoItem>
            <InfoItem label={t("approvals.created_at")}>{formatDateTime(record?.created_at)}</InfoItem>
            <InfoItem label={t("approvals.submitted_at")}>{formatDateTime(record?.submitted_at)}</InfoItem>
            <InfoItem label={t("approvals.decided_at")}>{formatDateTime(record?.decided_at)}</InfoItem>
            <InfoItem label={t("approvals.executed_at")}>{formatDateTime(record?.executed_at)}</InfoItem>
            <InfoItem label={t("approvals.expires_at")}>{formatDateTime(record?.expires_at)}</InfoItem>
          </dl>
          <div className="mt-4">
            <InfoItem label={t("approvals.reason")} className="w-full">
              <p className="whitespace-pre-wrap">{record?.reason || "-"}</p>
            </InfoItem>
          </div>
          {record?.last_error && (
            <div className="mt-4 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
              {record.last_error}
            </div>
          )}
        </div>

        <div className="rounded-lg border bg-card p-4">
          <h3 className="mb-4 text-base font-semibold">{t("approvals.steps_title")}</h3>
          <StepTimeline steps={record?.steps} />
        </div>

        <BusinessGuidance />

        <div className="grid gap-4 xl:grid-cols-2">
          <div className="rounded-lg border bg-card p-4">
            <JSONSection title={t("approvals.payload")} value={record?.payload_json} fallbackLabel={t("approvals.no_data")} />
          </div>
          <div className="rounded-lg border bg-card p-4">
            <JSONSection title={t("approvals.snapshot")} value={record?.snapshot_json} fallbackLabel={t("approvals.no_data")} />
          </div>
          <div className="rounded-lg border bg-card p-4 xl:col-span-2">
            <JSONSection title={t("approvals.result")} value={record?.result_json} fallbackLabel={t("approvals.no_data")} />
          </div>
        </div>
      </div>
    </Show>
  );
};
