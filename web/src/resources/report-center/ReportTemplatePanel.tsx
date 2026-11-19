/**
 * R19: template governance for the workspace.
 *
 * What this panel is for: before P2 a workspace could only *fork* the platform
 * baseline (`POST :key/clone`), so every customisation was re-cloned from upstream
 * and nothing could be taken out of service without losing a report's provenance.
 * The panel adds the two missing verbs — publish a new version, retire the newest
 * one — plus the provenance the list needs to explain itself:
 *
 *  - **source**: `platform` rows ship with the release. They are read-only here on
 *    purpose (`UpdateReportTemplate` only ever writes a workspace row), so the panel
 *    says why instead of offering a button that answers 400;
 *  - **version**: a version row is immutable. "Edit" means "publish N+1", which is
 *    what keeps a report that pinned N rendering N — and what makes the version
 *    number honest as an audit coordinate;
 *  - **retirement** only affects *new* reports: historical revisions resolve by
 *    explicit version and keep working, so nothing is deleted.
 *
 * The panel is not a schema editor, but it does own the *entry* to one: "编辑结构"
 * opens `TemplateStructureEditor` seeded with the newest version's payload. That
 * button is what closed the last "有接口无入口" gap on this surface — `PUT
 * /report-templates/:key` had been callable since P2 while every workspace
 * customisation needed `curl` (doc/288 §8.7).
 *
 * The blind "publish the row back" button is gone on purpose: re-sending a
 * byte-identical payload is an idempotent replay (`created=false`), so it could
 * never mint the version its label promised.
 *
 * "New template" is hidden — with a reason — for a caller inside the **platform**
 * tenant: `PublishReportTemplate` refuses `PlatformTenantID` because a runtime write
 * there would change every workspace's baseline at once (live 2026-11-19: the button
 * was there and answered `400/44105`). Offering an action the server refuses is the
 * same defect as hiding one it allows.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { useGetIdentity, useLocaleState, useTranslate } from "ra-core";
import { FileCog, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { api, ApiError, unwrap } from "../../lib/api";
import { PLATFORM_TENANT_ID } from "../../lib/tenant";
import { TemplateStructureEditor } from "./TemplateStructureEditor";
import {
  reportTemplateEditBlocker,
  reportTemplateName,
  reportTemplateSourceLabel,
  reportTemplateStatusLabel,
  type ReportTemplateRow,
} from "./report-center-api";

export interface ReportTemplatePanelProps {
  /** `manage:report-template`; the server enforces it and the list stays readable. */
  canManage: boolean;
}

interface TemplateGroup {
  key: string;
  /** Newest row wins: it is the version a new report would pin. */
  newest: ReportTemplateRow;
  /**
   * How many versions of this key exist.
   *
   * The list is **one row per key** (the service resolves "newest wins"), so counting
   * the rows in hand would always say 1 — the version column printed "v2（共 1 个版本）"
   * on a real deploy (2026-11-19). The service now publishes `versions`; the row count
   * stays as a fallback for rows from an older server.
   */
  versions: number;
  /** Every row of this key is retired, so the key cannot be pinned for a new run. */
  retired: boolean;
}

/** Groups flat rows into one line per key; a key may exist at several versions. */
export function groupTemplateRows(rows: ReportTemplateRow[]): TemplateGroup[] {
  const byKey = new Map<string, ReportTemplateRow[]>();
  for (const row of rows) {
    const key = String(row.template_key ?? "");
    if (!key) continue;
    const bucket = byKey.get(key);
    if (bucket) bucket.push(row);
    else byKey.set(key, [row]);
  }
  const groups: TemplateGroup[] = [];
  for (const [key, bucket] of byKey) {
    const sorted = [...bucket].sort((a, b) => Number(b.version) - Number(a.version));
    const newest = sorted[0];
    groups.push({
      key,
      newest,
      versions: Number(newest.versions ?? 0) || sorted.length,
      retired: sorted.every((row) => row.retired || row.status === "retired"),
    });
  }
  return groups.sort((a, b) => a.key.localeCompare(b.key));
}

export function ReportTemplatePanel({ canManage }: ReportTemplatePanelProps) {
  const t = useTranslate();
  const [locale] = useLocaleState();
  const { data: identity } = useGetIdentity();
  // `GET /tenants` never returns the platform tenant, so identity is the only place
  // the UI learns it is in the control plane.
  const platformTenant =
    identity !== undefined && String((identity as { tenant_id?: string }).tenant_id ?? "") === PLATFORM_TENANT_ID;
  const [rows, setRows] = useState<ReportTemplateRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string>("");
  const [error, setError] = useState<unknown>(null);
  /** The key whose structure is open in the editor; `null` means "closed". */
  const [editing, setEditing] = useState<
    { mode: "create" } | { mode: "edit"; group: TemplateGroup } | null
  >(null);
  /** "已保存为 v4" — the version number is the server's, never a client guess. */
  const [notice, setNotice] = useState<string>("");

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setRows(await unwrap<ReportTemplateRow[]>(api.get("/report-templates")));
    } catch (err) {
      setError(err);
      setRows([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const groups = useMemo(() => groupTemplateRows(rows), [rows]);

  const clone = async (key: string) => {
    setBusy(key);
    setError(null);
    try {
      await unwrap(api.post(`/report-templates/${encodeURIComponent(key)}/clone`));
      await load();
    } catch (err) {
      setError(err);
    } finally {
      setBusy("");
    }
  };

  const retire = async (key: string) => {
    setBusy(key);
    setError(null);
    try {
      await unwrap(api.post(`/report-templates/${encodeURIComponent(key)}/retire`));
      await load();
    } catch (err) {
      setError(err);
    } finally {
      setBusy("");
    }
  };

  return (
    <section className="rounded-lg border p-4" data-testid="report-template-panel">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <FileCog className="size-4" />
            {t("reportCenter.template_title")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">{t("reportCenter.template_hint")}</p>
          {canManage && platformTenant && (
            <p className="mt-1 text-xs text-muted-foreground" data-testid="template-create-blocked">
              {t("reportCenter.template_create_platform_blocked")}
            </p>
          )}
        </div>
        <div className="flex gap-2">
          {canManage && !platformTenant && (
            <Button
              size="sm"
              onClick={() => {
                setNotice("");
                setEditing({ mode: "create" });
              }}
              data-testid="template-create"
            >
              {t("reportCenter.template_create")}
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            {loading ? <Loader2 className="size-4 animate-spin" /> : null}
            {t("reportCenter.refresh")}
          </Button>
        </div>
      </div>

      {notice !== "" && (
        <p className="mb-3 text-xs text-muted-foreground" data-testid="report-template-notice">
          {notice}
        </p>
      )}

      {error !== null && (
        <div
          role="alert"
          data-testid="report-template-error"
          className="mb-3 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive"
        >
          {error instanceof ApiError ? error.displayMessage : t("reportCenter.template_fail")}
        </div>
      )}

      {loading && groups.length === 0 ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> {t("reportCenter.loading")}
        </div>
      ) : groups.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("reportCenter.template_empty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="px-3 py-2 font-medium">{t("reportCenter.template_col_name")}</th>
                <th className="px-3 py-2 font-medium">{t("reportCenter.template_col_source")}</th>
                <th className="px-3 py-2 font-medium">{t("reportCenter.template_col_version")}</th>
                <th className="px-3 py-2 font-medium">{t("reportCenter.template_col_status")}</th>
                <th className="px-3 py-2 text-right font-medium">{t("reportCenter.template_col_actions")}</th>
              </tr>
            </thead>
            <tbody>
              {groups.map((group) => {
                // Two different "cannot edit here": the row itself is not editable
                // (platform baseline, retired, someone else's), or this caller lacks
                // `manage:report-template`. Both print a reason — a hidden button with
                // no explanation reads as a missing feature rather than a permission.
                const blocker =
                  reportTemplateEditBlocker(t, group.newest) ??
                  (!canManage && group.newest.editable ? t("reportCenter.template_readonly") : null);
                return (
                  <tr key={group.key} className="border-t">
                    <td className="px-3 py-2">
                      <div className="font-medium">{reportTemplateName(group.newest, locale)}</div>
                      <div className="text-xs text-muted-foreground">{group.key}</div>
                    </td>
                    <td className="px-3 py-2" data-testid={`template-source-${group.key}`}>
                      {reportTemplateSourceLabel(t, group.newest.source)}
                    </td>
                    <td className="px-3 py-2 tabular-nums" data-testid={`template-version-${group.key}`}>
                      {t("reportCenter.template_version_value", {
                        version: String(group.newest.version),
                        count: String(group.versions),
                      })}
                    </td>
                    <td className="px-3 py-2">
                      <span data-testid={`template-status-${group.key}`}>
                        {reportTemplateStatusLabel(t, group.newest.status)}
                      </span>
                      {blocker && (
                        // The reason replaces the action: a disabled button with no
                        // explanation reads as a permission bug.
                        <div className="text-xs text-muted-foreground">{blocker}</div>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex justify-end gap-2">
                        {canManage && group.newest.source === "platform" && (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => void clone(group.key)}
                            disabled={busy === group.key}
                            data-testid={`template-clone-${group.key}`}
                          >
                            {t("reportCenter.template_clone")}
                          </Button>
                        )}
                        {canManage && group.newest.editable && (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => {
                              setNotice("");
                              setEditing({ mode: "edit", group });
                            }}
                            disabled={busy === group.key}
                            data-testid={`template-edit-${group.key}`}
                          >
                            {t("reportCenter.template_edit")}
                          </Button>
                        )}
                        {canManage && group.newest.editable && !group.retired && (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => void retire(group.key)}
                            disabled={busy === group.key}
                            data-testid={`template-retire-${group.key}`}
                          >
                            {t("reportCenter.template_retire")}
                          </Button>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <p className="mt-3 text-xs text-muted-foreground">{t("reportCenter.template_retire_note")}</p>

      <Dialog
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) setEditing(null);
        }}
      >
        {editing !== null && (
          <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
            <DialogHeader>
              <DialogTitle>
                {editing.mode === "create"
                  ? t("reportCenter.template_editor_title_create")
                  : t("reportCenter.template_editor_title", { key: editing.group.key })}
              </DialogTitle>
              <DialogDescription>{t("reportCenter.template_editor_hint")}</DialogDescription>
            </DialogHeader>
            <TemplateStructureEditor
              mode={editing.mode}
              templateKey={editing.mode === "edit" ? editing.group.key : undefined}
              payload={editing.mode === "edit" ? editing.group.newest.payload : undefined}
              onCancel={() => setEditing(null)}
              onSaved={(saved) => {
                setEditing(null);
                // `created=false` is the server saying "byte-identical, no new version":
                // printing "已保存为 v2" there would imply a version was minted.
                setNotice(
                  saved.created && saved.version !== undefined
                    ? t("reportCenter.template_editor_saved", { version: String(saved.version) })
                    : t("reportCenter.template_editor_replayed"),
                );
                void load();
              }}
            />
          </DialogContent>
        )}
      </Dialog>
    </section>
  );
}
