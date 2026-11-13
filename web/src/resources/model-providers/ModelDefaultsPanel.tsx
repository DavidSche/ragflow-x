import { useCallback, useEffect, useMemo, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { ModelTreeSelect } from "@/components/ModelTreeSelect";
import {
  ApiError,
  getModelDefaultOptions,
  getModelDefaults,
  patchModelDefault,
  type ModelDefaultOption,
  type ModelDefaultState,
} from "@/lib/api";

interface Props {
  /** Bump to force a full refetch (e.g. after provider model changes). */
  refreshToken?: number;
}

/** Human-readable canonical type labels (i18n keys compiled to a map). */
const TYPE_LABEL_KEYS: Record<string, string> = {
  chat: "providers.default_type_chat",
  embedding: "providers.default_type_embedding",
  rerank: "providers.default_type_rerank",
  speech2text: "providers.default_type_speech2text",
  image2text: "providers.default_type_image2text",
  tts: "providers.default_type_tts",
  ocr: "providers.default_type_ocr",
  decision: "providers.default_type_decision",
};

function currentDefaultLabel(state: ModelDefaultState): string | null {
  if (!state.model_id) return null;
  const parts = [state.provider_name, state.instance_name, state.model_name]
    .filter((p): p is string => !!p)
    .join(" / ");
  return parts || state.model_id;
}

function StateBadge({
  state,
  translate,
}: {
  state: ModelDefaultState;
  translate: (k: string, params?: Record<string, unknown>) => string;
}) {
  switch (state.status) {
    case "ok":
      return (
        <Badge variant="outline" className="border-emerald-500/50 text-emerald-600">
          {translate("providers.default_status_ok")}
        </Badge>
      );
    case "invalid":
      return (
        <Badge variant="outline" className="border-red-500/50 text-red-600">
          {translate("providers.default_status_invalid")}
        </Badge>
      );
    case "verify_failed":
      return (
        <Badge variant="outline" className="border-amber-500/50 text-amber-600">
          {translate("providers.default_status_verify_failed")}
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className="text-muted-foreground">
          {translate("providers.default_status_unset")}
        </Badge>
      );
  }
}

/**
 * Platform default model panel (doc/269 §4.4). Rendering is gated by
 * read:model-default; write controls by manage:model-default. Operator /
 * business_user without the read permission never see the panel. Defaults
 * data is never cached client-side (doc/269 P1-6) and every write carries the
 * mandatory CAS declaration.
 */
export function ModelDefaultsPanel({ refreshToken = 0 }: Props) {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess: canRead } = useCanAccess({ resource: "model-default", action: "read" });
  const { canAccess: canManage } = useCanAccess({ resource: "model-default", action: "manage" });

  const [states, setStates] = useState<ModelDefaultState[] | null>(null);
  const [options, setOptions] = useState<Record<string, ModelDefaultOption[]>>({});
  const [loading, setLoading] = useState(true);
  const [initialError, setInitialError] = useState<ApiError | null>(null);
  const [picked, setPicked] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<Record<string, "set" | "clear">>({});

  const load = useCallback(async () => {
    setLoading(true);
    setInitialError(null);
    try {
      const [stateList, optionMap] = await Promise.all([
        getModelDefaults(),
        getModelDefaultOptions(),
      ]);
      setStates(stateList);
      setOptions(optionMap);
    } catch (err) {
      setInitialError(err instanceof ApiError ? err : new ApiError(0, 0, String(err)));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load, refreshToken]);

  const visibleStates = useMemo(
    () => (states ?? []).filter((s) => s.supported),
    [states],
  );

  if (!canRead) return null;

  const write = async (
    state: ModelDefaultState,
    modelID: string,
    action: "set" | "clear",
  ) => {
    if (!canManage) return;
    setBusy((prev) => ({ ...prev, [state.model_type]: action }));
    try {
      // CAS declaration from the freshest read of the current default.
      const currentID = state.model_id ?? null;
      await patchModelDefault({
        model_type: state.model_type,
        model_id: modelID,
        expected_model_id: currentID,
      });
      setPicked((prev) => ({ ...prev, [state.model_type]: "" }));
      notify(
        action === "set"
          ? t("providers.default_set_success")
          : t("providers.default_clear_success"),
        { type: "success" },
      );
      await load();
    } catch (err) {
      const apiErr = err instanceof ApiError ? err : new ApiError(0, 0, String(err));
      if (apiErr.code === 40912 || apiErr.status === 409) {
        notify(t("providers.default_cas_conflict"), { type: "warning" });
      } else if (apiErr.status === 502 && (apiErr.code === 50257 || apiErr.code === 50256)) {
        notify(t("providers.default_unknown_result"), { type: "warning" });
      } else {
        notify(apiErr.displayMessage, { type: "error" });
      }
      // Refresh so the UI reflects the authoritative upstream state.
      void load();
    } finally {
      setBusy((prev) => {
        const next = { ...prev };
        delete next[state.model_type];
        return next;
      });
    }
  };

  return (
    <Card className="mb-4 p-4">
      <div className="mb-1 flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">{t("providers.default_section_title")}</h2>
        <span className="text-xs text-muted-foreground">
          {t("providers.default_global_hint")}
        </span>
      </div>

      {loading && (
        <div className="space-y-2 py-2" aria-busy>
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      )}

      {!loading && initialError && (
        <p className="rounded border border-red-500/30 bg-red-50 px-3 py-2 text-xs text-red-600">
          {initialError.displayMessage}
        </p>
      )}

      {!loading && !initialError && visibleStates.length === 0 && (
        <p className="text-xs text-muted-foreground">{t("providers.default_empty")}</p>
      )}

      {!loading && !initialError &&
        visibleStates.map((state) => {
          const opts = options[state.model_type] ?? [];
          const pickID = picked[state.model_type] ?? "";
          const busyOp = busy[state.model_type];
          const current = currentDefaultLabel(state);
          const typeLabel = TYPE_LABEL_KEYS[state.model_type]
            ? t(TYPE_LABEL_KEYS[state.model_type])
            : state.model_type;
          const hasModels = opts.length > 0;
          return (
            <div
              key={state.model_type}
              className="flex flex-col gap-2 border-t border-border py-3 first:border-t-0"
              data-testid={`default-row-${state.model_type}`}
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="w-28 shrink-0 text-sm font-medium">{typeLabel}</span>
                <StateBadge state={state} translate={t} />
                {state.status === "invalid" && (
                  <span className="max-w-80 truncate text-xs text-red-600">
                    {t("providers.default_status_invalid_hint")}
                  </span>
                )}
                {state.status === "verify_failed" && (
                  <span className="max-w-80 truncate text-xs text-amber-600">
                    {t("providers.default_status_verify_failed_hint")}
                  </span>
                )}
                <span className="text-xs text-muted-foreground">
                  {current
                    ? `${t("providers.default_current")}: ${current}`
                    : t("providers.default_current_none")}
                </span>
              </div>

              {!hasModels && (
                <p className="text-xs text-muted-foreground">
                  {t("providers.default_no_models", { type: typeLabel })}
                </p>
              )}

              {hasModels && !canManage && (
                <p className="text-xs text-muted-foreground">
                  {t("providers.default_readonly_hint")}
                </p>
              )}

              {hasModels && canManage && (
                <div className="flex flex-wrap items-center gap-2">
                  <ModelTreeSelect
                    options={opts}
                    value={pickID}
                    onSelect={(id) =>
                      setPicked((prev) => ({ ...prev, [state.model_type]: id }))
                    }
                    disabled={!!busyOp}
                  />
                  <Button
                    size="sm"
                    disabled={!pickID || !!busyOp}
                    onClick={() => void write(state, pickID, "set")}
                  >
                    {busyOp === "set" ? <Loader2 className="size-4 animate-spin" /> : null}
                    {t("providers.default_set_btn")}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={!state.model_id || !!busyOp}
                    onClick={() => void write(state, "", "clear")}
                  >
                    {busyOp === "clear" ? <Loader2 className="size-4 animate-spin" /> : null}
                    {t("providers.default_clear_btn")}
                  </Button>
                </div>
              )}
            </div>
          );
        })}
    </Card>
  );
}
