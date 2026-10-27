import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { Show } from "@/components/admin";
import { useNotify, useRecordContext, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Loader2, Plus, RefreshCw, Trash2 } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { optionalWarning } from "../../lib/optional-error";

interface ProjectInfo {
  id: string;
  name: string;
  description?: string;
}

interface DatasetOption {
  id: string;
  name: string;
  project_id?: string;
  bound: boolean;
}

interface TeamOption {
  id: string;
  name: string;
  bound: boolean;
}

interface Member {
  id: string;
  username?: string;
}

export const ProjectShow = () => (
  <Show>
    <ProjectDetail />
  </Show>
);

const ProjectDetail = () => {
  const t = useTranslate();
  const notify = useNotify();
  const record = useRecordContext<ProjectInfo>();
  const { id } = useParams<{ id: string }>();

  const [datasets, setDatasets] = useState<DatasetOption[]>([]);
  const [teams, setTeams] = useState<TeamOption[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [userOptions, setUserOptions] = useState<Member[]>([]);
  const [checkedDatasets, setCheckedDatasets] = useState<Set<string>>(new Set());
  const [checkedTeams, setCheckedTeams] = useState<Set<string>>(new Set());

  const [savingDS, setSavingDS] = useState(false);
  const [savingTeams, setSavingTeams] = useState(false);
  const [addingMember, setAddingMember] = useState(false);
  const [removingMember, setRemovingMember] = useState<string | null>(null);
  const [memberPick, setMemberPick] = useState("");

  const load = async () => {
    if (!id) return;
    try {
      const [dsRes, teamRes, memberRes] = await Promise.all([
        api.get<{ code: number; data: DatasetOption[] }>(`/projects/${id}/datasets`),
        api.get<{ code: number; data: TeamOption[] }>(`/projects/${id}/teams`),
        api.get<{ code: number; data: Member[] }>(`/projects/${id}/users`),
      ]);
      const ds = dsRes.data?.data ?? [];
      setDatasets(ds);
      setCheckedDatasets(new Set(ds.filter((d) => d.bound).map((d) => d.id)));
      const tm = teamRes.data?.data ?? [];
      setTeams(tm);
      setCheckedTeams(new Set(tm.filter((x) => x.bound).map((x) => x.id)));
      setMembers(Array.isArray(memberRes.data?.data) ? memberRes.data.data : []);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("projects.load_fail"), {
        type: "error",
      });
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  useEffect(() => {
    if (!id) return;
    api
      .get<{ code: number; data: { items: Member[] } | Member[] }>("/users?scope=all")
      .then((res) => {
        const data = res.data?.data;
        setUserOptions(
          Array.isArray(data) ? data : Array.isArray(data?.items) ? data.items : [],
        );
      })
      .catch((error) => optionalWarning(error, "用户列表加载失败"));
  }, [id]);

  const toggleDS = (did: string) =>
    setCheckedDatasets((prev) => {
      const n = new Set(prev);
      n.has(did) ? n.delete(did) : n.add(did);
      return n;
    });

  const toggleTeam = (tid: string) =>
    setCheckedTeams((prev) => {
      const n = new Set(prev);
      n.has(tid) ? n.delete(tid) : n.add(tid);
      return n;
    });

  const saveDatasets = async () => {
    if (!id) return;
    setSavingDS(true);
    try {
      await api.put(`/projects/${id}/datasets`, { ids: [...checkedDatasets] });
      notify(t("projects.bind_saved"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("projects.bind_fail"), {
        type: "error",
      });
    } finally {
      setSavingDS(false);
    }
  };

  const saveTeams = async () => {
    if (!id) return;
    setSavingTeams(true);
    try {
      await api.put(`/projects/${id}/teams`, { ids: [...checkedTeams] });
      notify(t("projects.bind_saved"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("projects.bind_fail"), {
        type: "error",
      });
    } finally {
      setSavingTeams(false);
    }
  };

  const addMember = async () => {
    if (!id || !memberPick) return;
    setAddingMember(true);
    try {
      await api.post(`/projects/${id}/users`, { user_id: memberPick });
      notify(t("projects.added_member"), { type: "success" });
      setMemberPick("");
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("projects.member_fail"), {
        type: "error",
      });
    } finally {
      setAddingMember(false);
    }
  };

  const removeMember = async (uid: string) => {
    if (!id) return;
    setRemovingMember(uid);
    try {
      await api.delete(`/projects/${id}/users/${uid}`);
      notify(t("projects.removed_member"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("projects.member_fail"), {
        type: "error",
      });
    } finally {
      setRemovingMember(null);
    }
  };

  const memberIds = new Set(members.map((m) => m.id));

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <p className="max-w-2xl text-sm text-muted-foreground">
          {record?.description || "-"}
        </p>
        <Button variant="outline" onClick={() => void load()} size="sm">
          <RefreshCw className="size-4" aria-hidden="true" />
          {t("projects.refresh")}
        </Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <section className="rounded border p-4">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-sm font-medium">{t("projects.dataset_binding")}</h3>
              <Button onClick={() => void saveDatasets()} disabled={savingDS} size="sm">
                {savingDS ? <Loader2 className="size-4 animate-spin" /> : t("projects.save_bindings")}
              </Button>
            </div>
            {datasets.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("projects.no_datasets")}</p>
            ) : (
              <ul className="grid gap-1 sm:grid-cols-2">
                {datasets.map((d) => (
                  <li key={d.id} className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={checkedDatasets.has(d.id)}
                      onCheckedChange={() => toggleDS(d.id)}
                      aria-label={d.name}
                    />
                    <span className="flex-1 truncate">{d.name}</span>
                    {d.bound ? (
                      <span className="shrink-0 text-xs text-muted-foreground">{t("projects.bound")}</span>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section className="rounded border p-4">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-sm font-medium">{t("projects.team_binding")}</h3>
              <Button onClick={() => void saveTeams()} disabled={savingTeams} size="sm">
                {savingTeams ? <Loader2 className="size-4 animate-spin" /> : t("projects.save_bindings")}
              </Button>
            </div>
            {teams.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("projects.no_teams")}</p>
            ) : (
              <ul className="grid gap-1 sm:grid-cols-2">
                {teams.map((x) => (
                  <li key={x.id} className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={checkedTeams.has(x.id)}
                      onCheckedChange={() => toggleTeam(x.id)}
                      aria-label={x.name}
                    />
                    <span className="flex-1 truncate">{x.name}</span>
                    {x.bound ? (
                      <span className="shrink-0 text-xs text-muted-foreground">{t("projects.bound")}</span>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>

        <div className="space-y-4">
          <section className="rounded border p-4">
            <h3 className="mb-3 text-sm font-medium">{t("projects.project_info")}</h3>
            <dl className="space-y-2 text-sm">
              <div className="flex justify-between gap-3">
                <dt className="shrink-0 text-muted-foreground">{t("projects.name")}</dt>
                <dd className="text-right font-medium">{record?.name || "-"}</dd>
              </div>
              <div className="flex justify-between gap-3">
                <dt className="shrink-0 text-muted-foreground">{t("projects.description")}</dt>
                <dd className="truncate text-right">{record?.description || "-"}</dd>
              </div>
            </dl>
          </section>

          <section className="rounded border p-4">
            <h3 className="mb-3 text-sm font-medium">{t("projects.member_manage")}</h3>
            <div className="mb-3 flex items-center gap-2">
              <select
                className="flex-1 rounded border bg-background px-2 py-1 text-sm"
                value={memberPick}
                onChange={(e) => setMemberPick(e.target.value)}
                aria-label={t("projects.select_user")}
              >
                <option value="">{t("projects.select_user")}</option>
                {userOptions
                  .filter((u) => !memberIds.has(u.id))
                  .map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.username || u.id}
                    </option>
                  ))}
              </select>
              <Button onClick={() => void addMember()} disabled={addingMember || !memberPick} size="sm">
                {addingMember ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" aria-hidden="true" />}
                {t("projects.add_member")}
              </Button>
            </div>
            {members.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("projects.no_members")}</p>
            ) : (
              <ul className="space-y-1">
                {members.map((m) => (
                  <li key={m.id} className="flex items-center gap-2 text-sm">
                    <span className="flex-1 truncate">{m.username || m.id}</span>
                    <button
                      type="button"
                      className="rounded border p-1 text-destructive"
                      onClick={() => void removeMember(m.id)}
                      disabled={removingMember === m.id}
                      aria-label={t("projects.remove_member")}
                    >
                      <Trash2 className="size-3" aria-hidden="true" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      </div>
    </div>
  );
};
