import { useEffect, useMemo, useState } from "react";
import { useCanAccess, useGetIdentity, useGetList, useTranslate } from "ra-core";
import { api, ApiError } from "../../lib/api";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";
import { approvalHoldFromResponse, approvalIdempotencyKey, type ApprovalHold } from "@/lib/approval-hold";
import { getProviderLogo, sortProviders } from "./provider-logos";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Cpu, Search } from "lucide-react";

export interface CatalogItem {
  name: string;
  default_url?: string;
  model_types?: string[];
  intl_url?: string;
}

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: () => void;
}

export const ProviderSelectDialog = ({ open, onOpenChange, onAdded }: Props) => {
  const t = useTranslate();
  const [catalog, setCatalog] = useState<CatalogItem[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [search, setSearch] = useState("");
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const { data: identityData } = useGetIdentity();
  const identity = identityData as { id: string; tenant_id: string; role?: string } | undefined;
  const { data: workspaceRecords } = useGetList("tenants", {
    pagination: { page: 1, perPage: 200 },
  });
  const workspaces = (workspaceRecords ?? []).map((workspace: { id: string; name: string }) => ({
    id: String(workspace.id),
    name: String(workspace.name),
  }));
  const { canAccess: platformAdmin } = useCanAccess({ resource: "model-provider", action: "governance.manage" });
  const [targetWorkspaceID, setTargetWorkspaceID] = useState("");

  useEffect(() => {
    if (!open || catalog !== null) return;
    api.get<{ code: number; message?: string; data: CatalogItem[] }>("/model-providers/catalog")
      .then((res) => {
        if (res.data.code === 0) setCatalog(sortProviders(res.data.data));
        else throw new ApiError(res.status, res.data.code, res.data.message || "catalog failed");
      })
      .catch((e) => setError(e instanceof ApiError ? e.displayMessage : t("providers.load_catalog_fail")));
  }, [open, catalog, t]);

  useEffect(() => {
    if (open) return;
    setError("");
    setSearch("");
    setTargetWorkspaceID("");
  }, [open]);

  useEffect(() => {
    if (!open || platformAdmin) return;
    setTargetWorkspaceID(identity?.tenant_id ?? "");
  }, [open, identity?.tenant_id, platformAdmin]);

  const filtered = useMemo(() => {
    if (!catalog) return [];
    const q = search.trim().toLowerCase();
    if (!q) return catalog;
    return catalog.filter((item) => {
      const meta = getProviderLogo(item.name);
      return item.name.toLowerCase().includes(q) || meta.tag.toLowerCase().includes(q);
    });
  }, [catalog, search]);

  const addProvider = async (name: string) => {
    setError("");
    if (platformAdmin && !targetWorkspaceID) {
      setError(t("providers.select_workspace_required"));
      return;
    }
    setBusy(true);
    try {
      const scopeQuery = platformAdmin && targetWorkspaceID
        ? `?scope=specific&tenant_id=${encodeURIComponent(targetWorkspaceID)}`
        : "";
      const res = await api.post<{ code: number; message?: string }>(
        `/model-providers${scopeQuery}`,
        { provider_name: name },
        { headers: { "Idempotency-Key": approvalIdempotencyKey("provider-create") } },
      );
      const responseHold = approvalHoldFromResponse(res);
      if (responseHold) { setHold(responseHold); onOpenChange(false); return; }
      if (res.data.code !== 0) throw new ApiError(res.status, res.data.code, res.data.message || t("providers.add_fail"));
      onOpenChange(false);
      onAdded();
    } catch (e) {
      setError(e instanceof ApiError ? e.displayMessage : t("providers.add_fail"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
          <DialogHeader className="shrink-0">
            <DialogTitle className="flex items-center gap-2">
              <Cpu className="size-4" />
              {t("providers.add_dialog")}
            </DialogTitle>
          <DialogDescription>{t("providers.add_desc")}</DialogDescription>
          </DialogHeader>
          {platformAdmin ? (
            <div className="shrink-0 pb-3">
              <label className="mb-1 block text-sm font-medium" htmlFor="provider-target-workspace">
                {t("providers.target_workspace")}
              </label>
              <select
                id="provider-target-workspace"
                className="h-9 w-full rounded-md border bg-background px-3 text-sm"
                value={targetWorkspaceID}
                onChange={(event) => setTargetWorkspaceID(event.target.value)}
                disabled={busy}
              >
                <option value="">{t("providers.select_workspace")}</option>
                {workspaces.map((workspace) => (
                  <option key={workspace.id} value={workspace.id}>
                    {workspace.name}
                  </option>
                ))}
              </select>
            </div>
          ) : null}
          <div className="relative shrink-0 pb-3">
            <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label={t("providers.search_placeholder")}
              placeholder={t("providers.search_placeholder")}
              type="search"
              className="pl-9"
            />
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto pr-1">
            {error ? <div className="mb-3 text-sm text-destructive">{error}</div> : null}
            {catalog === null ? (
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                {Array.from({ length: 9 }).map((_, i) => <Skeleton key={i} className="h-24 rounded-lg" />)}
              </div>
            ) : filtered.length === 0 ? (
              <div className="py-12 text-center text-sm text-muted-foreground">
                {search ? t("providers.search_empty") : t("providers.add_empty")}
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                {filtered.map((item) => {
                  const { tag, Icon, color } = getProviderLogo(item.name);
                  return (
                    <button
                      key={item.name}
                      type="button"
                      disabled={busy}
                      onClick={() => void addProvider(item.name)}
                      className="group flex flex-col items-center gap-2 rounded-lg border bg-background p-4 text-center transition-colors hover:border-primary hover:bg-muted/50 disabled:opacity-50"
                    >
                      <Icon className={`size-8 shrink-0 transition-colors group-hover:text-primary ${color}`} />
                      <span className="line-clamp-1 text-sm font-medium leading-tight">{tag}</span>
                      {item.default_url ? (
                        <span className="line-clamp-1 text-xs text-muted-foreground">{item.default_url.replace(/^https?:\/\//, "")}</span>
                      ) : null}
                      {item.model_types && item.model_types.length > 0 ? (
                        <div className="flex flex-wrap justify-center gap-1">
                          {item.model_types.slice(0, 3).map((mt) => (
                            <Badge key={mt} variant="secondary" className="px-1.5 py-0 text-[10px]">{mt}</Badge>
                          ))}
                          {item.model_types.length > 3 ? (
                            <Badge variant="outline" className="px-1.5 py-0 text-[10px]">+{item.model_types.length - 3}</Badge>
                          ) : null}
                        </div>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
          <div className="shrink-0 border-t pt-3 text-center">
            <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
              {t("providers.cancel")}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};
