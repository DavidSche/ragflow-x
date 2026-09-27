import { useNavigate, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { ApprovalHold } from "@/lib/approval-hold";

export const ApprovalHoldDialog = ({
  hold,
  onClose,
}: {
  hold: ApprovalHold | null;
  onClose: () => void;
}) => {
  const t = useTranslate();
  const navigate = useNavigate();
  if (!hold) return null;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-label={t("approvals.hold_dialog_label")}>
        <DialogHeader>
          <DialogTitle>{t("approvals.hold_title")}</DialogTitle>
          <DialogDescription>{t("approvals.hold_description")}</DialogDescription>
        </DialogHeader>
        <dl className="grid gap-2 text-sm md:grid-cols-2">
          <dt className="text-muted-foreground">{t("approvals.request_no")}</dt>
          <dd className="font-mono break-all">{hold.request_no}</dd>
          <dt className="text-muted-foreground">{t("approvals.status_title")}</dt>
          <dd>{t(`approvals.status.${hold.status}`)}</dd>
          <dt className="text-muted-foreground">{t("approvals.current_step")}</dt>
          <dd>{hold.current_step}</dd>
          <dt className="text-muted-foreground">{t("approvals.expires_at")}</dt>
          <dd>{hold.expire_at ? new Date(hold.expire_at).toLocaleString() : "-"}</dd>
        </dl>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("ra.action.cancel")}
          </Button>
          <Button onClick={() => navigate(hold.approval_path)}>
            {t("approvals.hold_view")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};
