/**
 * The share panel of one report (doc/296 §2.1).
 *
 * Why it is a panel and not a dropdown: `access_policy` has four answers, two of which
 * name grantees. A select that silently kept the old grantees when the policy moved
 * from `selected_users` to `tenant_members` would leave rows behind that the policy no
 * longer honours, and the next reader of the panel would see names that mean nothing.
 * So the editor always sends the *whole* list it is showing, and the server replaces
 * the grants wholesale (`ReportAccessWrite`).
 *
 * Two rendering rules that are deliberate:
 *
 *  - the current policy is printed in words, never as `owner_only`. The enum is the
 *    server's vocabulary; the reader's is the dictionary's;
 *  - a grantee whose id no longer resolves is shown as「已失效」rather than dropped.
 *    The grant is still there (revoking it silently would change the policy behind the
 *    owner's back), and hiding it would make the panel disagree with the rows.
 */
import { useCallback, useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Loader2, Share2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { api, ApiError, unwrap } from "../../lib/api";
import {
  REPORT_ACCESS_POLICIES,
  reportAccessGrantKindLabel,
  reportAccessGranteeLabel,
  reportAccessPolicyLabel,
  reportAccessPolicyTakesTeams,
  reportAccessPolicyTakesUsers,
  type ReportAccessView,
  type SetReportAccessRequest,
} from "./report-center-api";

export interface ReportSharePanelProps {
  reportId: string;
  /** The sharing state from `GET /reports/:id`; `undefined` means the server is older. */
  access: ReportAccessView | undefined;
  /** Called with the server's answer so the drawer re-renders the stored state. */
  onSaved?: (view: ReportAccessView) => void;
}

interface GranteeOption {
  id: string;
  name: string;
}

/**
 * Reads a list payload defensively: the user and team endpoints have different
 * envelopes (a paged `{items}` and a bare list), and a share panel that crashed on the
 * shape would hide the policy as well.
 */
function readOptions(response: unknown, labelKey: "username" | "name"): GranteeOption[] {
  const body = (response as { data?: { data?: unknown } } | undefined)?.data?.data;
  let rows: Array<Record<string, unknown>> = [];
  if (Array.isArray(body)) {
    rows = body as Array<Record<string, unknown>>;
  } else if (body && typeof body === "object" && Array.isArray((body as { items?: unknown }).items)) {
    rows = ((body as { items: Array<Record<string, unknown>> }).items ?? []) as Array<
      Record<string, unknown>
    >;
  }
  return rows
    .map((row) => ({
      id: String(row.id ?? ""),
      name: String(row[labelKey] ?? "").trim(),
    }))
    .filter((option) => option.id !== "");
}

export function ReportSharePanel({ reportId, access, onSaved }: ReportSharePanelProps) {
  const t = useTranslate();
  const [view, setView] = useState<ReportAccessView | undefined>(access);
  const [editing, setEditing] = useState(false);
  const [policy, setPolicy] = useState<string>(access?.policy ?? "owner_only");
  const [userIDs, setUserIDs] = useState<string[]>([]);
  const [teamIDs, setTeamIDs] = useState<string[]>([]);
  const [users, setUsers] = useState<GranteeOption[]>([]);
  const [teams, setTeams] = useState<GranteeOption[]>([]);
  const [loadingOptions, setLoadingOptions] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    setView(access);
    setPolicy(access?.policy ?? "owner_only");
  }, [access]);

  // The options are loaded when the editor opens, not on mount: a reader who cannot
  // share anything never pays for two list requests.
  const openEditor = useCallback(async () => {
    const current = view;
    setPolicy(current?.policy ?? "owner_only");
    setUserIDs(current?.grantees.filter((g) => g.kind === "user").map((g) => g.id) ?? []);
    setTeamIDs(current?.grantees.filter((g) => g.kind === "team").map((g) => g.id) ?? []);
    setError(null);
    setEditing(true);
    setLoadingOptions(true);
    try {
      const [userList, teamList] = await Promise.all([
        api.get("/users", { params: { page: 1, page_size: 200 } }).catch(() => null),
        api.get("/teams").catch(() => null),
      ]);
      setUsers(readOptions(userList, "username"));
      setTeams(readOptions(teamList, "name"));
    } catch (err) {
      setError(err);
    } finally {
      setLoadingOptions(false);
    }
  }, [view]);

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const payload: SetReportAccessRequest = { policy };
      // Only the list the chosen policy honours is sent. Sending the other one would
      // look like it was stored (and `access_scope_json` would record a request the
      // policy ignores), so the panel and the server would describe different shares.
      if (reportAccessPolicyTakesUsers(policy)) payload.user_ids = userIDs;
      if (reportAccessPolicyTakesTeams(policy)) payload.team_ids = teamIDs;
      const saved = await unwrap<ReportAccessView>(
        api.put(`/reports/${encodeURIComponent(reportId)}/access`, payload),
      );
      setView(saved);
      setEditing(false);
      onSaved?.(saved);
    } catch (err) {
      setError(err);
    } finally {
      setSaving(false);
    }
  };

  const toggle = (ids: string[], value: string): string[] =>
    ids.includes(value) ? ids.filter((entry) => entry !== value) : [...ids, value];

  return (
    <section className="rounded-lg border p-4" data-testid="report-share-panel">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <Share2 className="size-4" />
            {t("reportCenter.share_title")}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">{t("reportCenter.share_hint")}</p>
        </div>
        {view?.can_manage && !editing && (
          <Button variant="outline" size="sm" onClick={() => void openEditor()} data-testid="report-share-edit">
            {t("reportCenter.share_edit")}
          </Button>
        )}
      </div>

      {error !== null && (
        <div
          role="alert"
          data-testid="report-share-error"
          className="mb-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive"
        >
          {error instanceof ApiError ? error.displayMessage : t("reportCenter.share_fail")}
        </div>
      )}

      {!view ? (
        // An older server sends no `access` at all. Saying so is honest; guessing
        // `owner_only` would be a statement about other people's access.
        <p className="text-sm text-muted-foreground" data-testid="report-share-unavailable">
          {t("reportCenter.share_unavailable")}
        </p>
      ) : editing ? (
        <div className="flex flex-col gap-3 text-sm">
          <label className="flex items-center justify-between gap-3">
            <span>{t("reportCenter.share_policy")}</span>
            <select
              className="h-9 rounded-md border bg-background px-2"
              value={policy}
              onChange={(event) => setPolicy(event.target.value)}
              data-testid="report-share-policy"
            >
              {REPORT_ACCESS_POLICIES.map((option) => (
                <option key={option} value={option}>
                  {reportAccessPolicyLabel(t, option)}
                </option>
              ))}
            </select>
          </label>

          {reportAccessPolicyTakesUsers(policy) && (
            <fieldset className="rounded-md border p-2">
              <legend className="px-1 text-xs text-muted-foreground">
                {t("reportCenter.share_pick_users")}
              </legend>
              {loadingOptions ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <div className="grid max-h-40 gap-1 overflow-auto sm:grid-cols-2">
                  {users.map((option) => (
                    <label key={option.id} className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        checked={userIDs.includes(option.id)}
                        onChange={() => setUserIDs((current) => toggle(current, option.id))}
                        data-testid={`report-share-user-${option.id}`}
                      />
                      <span>{option.name || t("reportCenter.access_grantee_missing")}</span>
                    </label>
                  ))}
                </div>
              )}
            </fieldset>
          )}

          {reportAccessPolicyTakesTeams(policy) && (
            <fieldset className="rounded-md border p-2">
              <legend className="px-1 text-xs text-muted-foreground">
                {t("reportCenter.share_pick_teams")}
              </legend>
              {loadingOptions ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <div className="grid max-h-40 gap-1 overflow-auto sm:grid-cols-2">
                  {teams.map((option) => (
                    <label key={option.id} className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        checked={teamIDs.includes(option.id)}
                        onChange={() => setTeamIDs((current) => toggle(current, option.id))}
                        data-testid={`report-share-team-${option.id}`}
                      />
                      <span>{option.name || t("reportCenter.access_grantee_missing")}</span>
                    </label>
                  ))}
                </div>
              )}
            </fieldset>
          )}

          <p className="text-xs text-muted-foreground">{t("reportCenter.share_replace_hint")}</p>

          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setEditing(false)} disabled={saving}>
              {t("ra.action.cancel")}
            </Button>
            <Button size="sm" onClick={() => void save()} disabled={saving} data-testid="report-share-save">
              {saving ? <Loader2 className="size-4 animate-spin" /> : null}
              {t("reportCenter.share_save")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2 text-sm">
          <div>
            <span className="text-xs text-muted-foreground">{t("reportCenter.share_policy")}</span>{" "}
            <span className="font-medium" data-testid="report-share-current">
              {reportAccessPolicyLabel(t, view.policy)}
            </span>
          </div>
          {view.grantees.length > 0 && (
            <ul className="flex flex-wrap gap-1" data-testid="report-share-grantees">
              {view.grantees.map((grantee) => (
                <li
                  key={`${grantee.kind}-${grantee.id}`}
                  className="rounded border px-2 py-0.5 text-xs"
                  data-missing={grantee.missing ? "true" : "false"}
                >
                  <span className="text-muted-foreground">
                    {reportAccessGrantKindLabel(t, grantee.kind)}
                  </span>
                  <span className="ml-1">{reportAccessGranteeLabel(t, grantee)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {/* A share never widens a write: saying which actions stay owner-only is part of
          explaining the policy rather than a decoration. */}
      <p className="mt-2 text-xs text-muted-foreground">{t("reportCenter.share_readonly_scope")}</p>
    </section>
  );
}
