/**
 * InstanceCard – renders a single provider instance with its model table.
 */
import { Boxes, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useTranslate } from "ra-core";
import { api, ApiError } from "../../lib/api";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import {
  type InstanceRow,
  type ModelRow,
  type ProviderForm,
  EMPTY_FORM,
  modelTools,
  modelThinking,
  typeLabels,
} from "./provider-types";

interface InstanceCardProps {
  providerId: string;
  crossWorkspace?: boolean;
  instances: InstanceRow[];
  modelsByInstance: Record<string, ModelRow[]>;
  loading: boolean;
  error: string;
  onReload: () => void;
  onNewModel: (instanceId: string) => void;
  onEditInstance: (inst: InstanceRow) => void;
  onDeleteInstance: (id: string) => void;
  onTestModel: (m: ModelRow) => void;
  onEditModel: (instanceId: string, m: ModelRow) => void;
  onDeleteModel: (instanceId: string, modelId: string) => void;
  onNewInstance: () => void;
}

export function InstanceCardList({
  providerId,
  crossWorkspace = false,
  instances,
  modelsByInstance,
  loading,
  error,
  onReload,
  onNewModel,
  onEditInstance,
  onDeleteInstance,
  onTestModel,
  onEditModel,
  onDeleteModel,
  onNewInstance,
}: InstanceCardProps) {
  const t = useTranslate();
  const instanceCount = instances.length;
  const modelCount = Object.values(modelsByInstance).flat().length;

  return (
    <>
      <div className="flex shrink-0 items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Boxes className="size-4" />
          {t("providers.instance_count", { i: instanceCount, m: modelCount })}
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={onReload}>
            <RefreshCw className="size-4" /> {t("providers.reload")}
          </Button>
          <Button size="sm" onClick={onNewInstance} disabled={crossWorkspace}>
            <Plus className="size-4" /> {t("providers.new_instance")}
          </Button>
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto pr-1">
        {error ? (
          <div className="text-sm text-destructive">{error}</div>
        ) : null}
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : instances.length === 0 ? (
          <div className="py-10 text-center text-sm text-muted-foreground">
            {t("providers.instance_empty")}
          </div>
        ) : (
          instances.map((inst) => (
            <InstanceCard
              key={inst.id}
              providerId={providerId}
              crossWorkspace={crossWorkspace}
              instance={inst}
              models={modelsByInstance[inst.id] ?? []}
              onNewModel={onNewModel}
              onEditInstance={onEditInstance}
              onDeleteInstance={onDeleteInstance}
              onTestModel={onTestModel}
              onEditModel={onEditModel}
              onDeleteModel={onDeleteModel}
            />
          ))
        )}
      </div>
    </>
  );
}

function InstanceCard({
  providerId,
  crossWorkspace = false,
  instance: inst,
  models,
  onNewModel,
  onEditInstance,
  onDeleteInstance,
  onTestModel,
  onEditModel,
  onDeleteModel,
}: {
  providerId: string;
  crossWorkspace?: boolean;
  instance: InstanceRow;
  models: ModelRow[];
  onNewModel: (instanceId: string) => void;
  onEditInstance: (inst: InstanceRow) => void;
  onDeleteInstance: (id: string) => void;
  onTestModel: (m: ModelRow) => void;
  onEditModel: (instanceId: string, m: ModelRow) => void;
  onDeleteModel: (instanceId: string, modelId: string) => void;
}) {
  const t = useTranslate();
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0 py-3">
        <div className="min-w-0">
          <CardTitle className="text-sm">
            {inst.instance_name}
            <Badge
              variant="outline"
              className="ml-2 align-middle capitalize"
            >
              {inst.region || "default"}
            </Badge>
          </CardTitle>
          <div className="mt-1 truncate text-xs text-muted-foreground">
            {inst.base_url || t("providers.base_url_unset")}
          </div>
        </div>
        <div className="flex shrink-0 gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => onNewModel(inst.id)}
            disabled={crossWorkspace}
          >
            <Plus className="size-4" /> {t("providers.add_model")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onEditInstance(inst)}
            disabled={crossWorkspace}
          >
            <Pencil className="size-4" /> {t("providers.edit")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onDeleteInstance(inst.id)}
            disabled={crossWorkspace}
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("providers.model_table")}</TableHead>
              <TableHead className="hidden md:table-cell">{t("providers.capability_col")}</TableHead>
              <TableHead className="hidden md:table-cell">
                {t("providers.max_tokens")}
              </TableHead>
              <TableHead>{t("providers.tool_calling")}</TableHead>
              <TableHead>{t("providers.thinking")}</TableHead>
              <TableHead>{t("providers.status")}</TableHead>
              <TableHead className="text-right">{t("providers.action")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {models.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="text-center text-sm text-muted-foreground"
                >
                  {t("providers.model_empty")}
                </TableCell>
              </TableRow>
            ) : (
              models.map((m) => (
                <TableRow key={m.id}>
                  <TableCell className="font-medium">
                    {m.model_name}
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <div className="flex flex-wrap gap-1">
                      {typeLabels(m.model_type).map((tl) => (
                        <Badge key={tl} variant="secondary">
                          {tl}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    {m.max_tokens ?? 8192}
                  </TableCell>
                  <TableCell>
                    <Badge variant={modelTools(m) ? "default" : "outline"}>
                      {modelTools(m) ? t("providers.toggle_on") : t("providers.toggle_off")}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={modelThinking(m) ? "default" : "outline"}>
                      {modelThinking(m) ? t("providers.toggle_on") : t("providers.toggle_off")}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        m.status === "active" ? "default" : "outline"
                      }
                    >
                      {m.status === "active" ? t("providers.status_active") : t("providers.status_disabled")}
                    </Badge>
                  </TableCell>
                  <TableCell className="flex items-center justify-end gap-1 whitespace-nowrap">
                    <IconButtonWithTooltip
                      label={t("providers.test_chat")}
                      onClick={() => onTestModel(m)}
                      disabled={crossWorkspace}
                    >
                      <svg className="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M22 2L11 13" /><path d="M22 2L15 22L11 13L2 9L22 2Z" /></svg>
                    </IconButtonWithTooltip>
                    <IconButtonWithTooltip
                      label={t("providers.edit")}
                      onClick={() => onEditModel(inst.id, m)}
                      disabled={crossWorkspace}
                    >
                      <Pencil className="size-4" />
                    </IconButtonWithTooltip>
                    <IconButtonWithTooltip
                      label={t("providers.delete")}
                      onClick={() => onDeleteModel(inst.id, m.id)}
                      disabled={crossWorkspace}
                    >
                      <Trash2 className="size-4" />
                    </IconButtonWithTooltip>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
