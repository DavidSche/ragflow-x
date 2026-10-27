import { useEffect, useState } from "react";
import type { ChangeEvent } from "react";
import { Show } from "@/components/admin";
import { useCanAccess, useGetIdentity, useGetList, useNotify, useRecordContext, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Crown, Link, Save, UserMinus, UserPlus } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { optionalWarning } from "../../lib/optional-error";
import { queryClient } from "../../lib/query";
import { UserNameLabel, WorkspaceLabel } from "@/components/admin/ReferenceLabel";

interface Member {
  id: string;
  username: string;
  role: string;
  status: string;
  tenant_id: string;
  tenant_name?: string;
}

interface ProjectLike {
  id: string;
  name: string;
  description?: string;
}

interface RoleLike {
  id: string;
  name: string;
}

interface Envelope<T> {
  code: number;
  message: string;
  data: T;
}

interface TeamRecord {
  id?: string;
  name?: string;
  owner_id?: string;
  owner_name?: string;
  tenant_name?: string;
  tenant_id?: string;
}

function statusText(t: ReturnType<typeof useTranslate>, status: string) {
  return status === "active" ? t("teams.status_active") : status === "disabled" ? t("teams.status_disabled") : status;
}

function errText(err: unknown, fallback: string) {
  return err instanceof ApiError ? err.displayMessage : fallback;
}

export const TeamShow = () => {
  return (
  <Show>
    <TeamHeader />
    <div className="mt-4 grid gap-6 lg:grid-cols-2">
      <OwnerPanel />
      <ProjectsPanel />
    </div>
    <MembersPanel />
  </Show>
  );
};

const TeamHeader = () => {
  const record = useRecordContext<TeamRecord>();
  const { data: user } = useGetIdentity();
  const isOwner = user && record?.owner_id && user.id === record.owner_id;
  if (!record?.id) return null;
  const t = useTranslate();
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-md border bg-muted/20 p-3">
      <div className="flex items-center gap-2">
        <Crown className="size-5 text-amber-500" />
        <div>
          <div className="font-medium">{record.name ?? record.id}</div>
          <div className="text-xs text-muted-foreground">
            {t("teams.team_admin")}：{record.owner_name ?? <UserNameLabel id={record.owner_id} />}
            {isOwner ? t("teams.admin_self") : ""}
          </div>
        </div>
      </div>
      <div className="ml-auto text-xs text-muted-foreground">
        {t("teams.tenant_label")}：{record.tenant_name ?? <WorkspaceLabel id={record.tenant_id} />}
      </div>
    </div>
  );
};

const OwnerPanel = () => {
  const record = useRecordContext<TeamRecord>();
  const teamId = record?.id;
  const { data: user } = useGetIdentity();
  const { canAccess: canManageTeam } = useCanAccess({ resource: "team", action: "manage" });
  const { canAccess: canReadGovernanceUsers } = useCanAccess({ resource: "user", action: "governance.read" });
  const notify = useNotify();
  const t = useTranslate();
  const isAdmin = canManageTeam;
  const [candidates, setCandidates] = useState<Member[]>([]);
  const [owner, setOwner] = useState(record?.owner_id ?? "");

  useEffect(() => {
    if (!isAdmin) return;
    api
      .get<Envelope<{ items: Member[] }>>(canReadGovernanceUsers ? "/users?scope=all" : "/users")
      .then((r) => setCandidates(r.data.data.items ?? []))
      .catch((error) => optionalWarning(error, "团队成员候选列表加载失败"));
  }, [canReadGovernanceUsers, isAdmin]);

  useEffect(() => {
    setOwner(record?.owner_id ?? "");
  }, [record?.owner_id]);

  if (!teamId) return null;

  const save = async () => {
    try {
      await api.put(`/teams/${teamId}`, { owner_id: owner });
      notify(t("teams.owner_updated"), { type: "success" });
      queryClient.invalidateQueries({ queryKey: ["teams"] });
    } catch (err) {
      notify(errText(err, t("teams.owner_fail")), { type: "error" });
    }
  };

  return (
    <section className="space-y-3">
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        <Crown className="size-4" /> {t("teams.owner_panel")}
      </h3>
      <div className="rounded-md border p-3 text-sm">
        <div className="mb-2 text-muted-foreground">
          {t("teams.current_owner")}：{record.owner_name ?? <UserNameLabel id={record.owner_id} />}
        </div>
        {isAdmin ? (
          <div className="flex flex-col gap-2 sm:flex-row">
            <select
              className="rounded border bg-background px-2 py-1 text-sm"
              value={owner}
              onChange={(e: ChangeEvent<HTMLSelectElement>) => setOwner(e.target.value)}
            >
              <option value="">{t("teams.owner_select")}</option>
              {candidates.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.username}
                </option>
              ))}
            </select>
            <Button size="sm" onClick={save} disabled={!owner}>
              <Save /> {t("teams.save_owner")}
            </Button>
          </div>
        ) : (
          <div className="text-xs text-muted-foreground">{t("teams.owner_readonly")}</div>
        )}
      </div>
    </section>
  );
};

const ProjectsPanel = () => {
  const record = useRecordContext<TeamRecord>();
  const teamId = record?.id;
  const notify = useNotify();
  const t = useTranslate();
  const { canAccess: isAdmin } = useCanAccess({ resource: "team", action: "manage" });
  const { data: allProjects } = useGetList("projects", {
    pagination: { page: 1, perPage: 200 },
  });
  const [boundIds, setBoundIds] = useState<string[]>([]);

  const load = async () => {
    if (!teamId) return;
    try {
      const res = await api.get<Envelope<ProjectLike[]>>(`/teams/${teamId}/projects`);
      setBoundIds((res.data.data ?? []).map((p) => p.id));
    } catch {
      notify(t("teams.projects_load_fail"), { type: "error" });
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [teamId]);

  if (!teamId) return null;
  const projects = (allProjects ?? []) as ProjectLike[];

  const toggle = (id: string, checked: boolean) => {
    setBoundIds((prev) => (checked ? [...prev, id] : prev.filter((x) => x !== id)));
  };

  const save = async () => {
    try {
      await api.put(`/teams/${teamId}/projects`, { project_ids: boundIds });
      notify(t("teams.projects_saved"), { type: "success" });
      queryClient.invalidateQueries({ queryKey: ["teams"] });
    } catch (err) {
      notify(errText(err, t("teams.projects_save_fail")), { type: "error" });
    }
  };

  return (
    <section className="space-y-3">
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        <Link className="size-4" /> {t("teams.projects_panel")}
      </h3>
      <div className="rounded-md border p-3">
        <ul className="mb-2 space-y-1 text-sm">
          {projects.length === 0 ? (
            <li className="text-muted-foreground">{t("teams.projects_empty")}</li>
          ) : (
            projects.map((p) => (
              <li key={p.id} className="flex items-center gap-2">
                {isAdmin && (
                  <Checkbox
                    checked={boundIds.includes(p.id)}
                    onCheckedChange={(v) => toggle(p.id, Boolean(v))}
                  />
                )}
                <span>{p.name}</span>
              </li>
            ))
          )}
        </ul>
        {isAdmin && (
          <Button size="sm" onClick={save}>
            <Save /> {t("teams.projects_save")}
          </Button>
        )}
      </div>
    </section>
  );
};

const MembersPanel = () => {
  const record = useRecordContext<{ id?: string; owner_id?: string; tenant_name?: string }>();
  const teamId = record?.id;
  const { canAccess: canManageTeam } = useCanAccess({ resource: "team", action: "manage" });
  const { canAccess: canReadGovernanceUsers } = useCanAccess({ resource: "user", action: "governance.read" });
  const notify = useNotify();
  const t = useTranslate();
  const isAdmin = canManageTeam;
  const { data: roles } = useGetList("roles", {
    pagination: { page: 1, perPage: 200 },
  });
  const roleName = new Map((roles ?? []).map((r: RoleLike) => [r.id, r.name]));
  const [members, setMembers] = useState<Member[]>([]);
  const [candidates, setCandidates] = useState<Member[]>([]);
  const [selected, setSelected] = useState("");

  const load = async () => {
    if (!teamId) return;
    try {
      const membersRes = await api.get<Envelope<Member[]>>(`/teams/${teamId}/users`);
      setMembers(membersRes.data.data ?? []);
      const usersRes = await api.get<Envelope<{ items: Member[] }>>(canReadGovernanceUsers ? "/users?scope=all" : "/users");
      setCandidates(usersRes.data.data.items ?? []);
    } catch {
      notify(t("teams.members_load_fail"), { type: "error" });
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canReadGovernanceUsers, teamId]);

  const addMember = async () => {
    if (!teamId || !selected) return;
    try {
      await api.post(`/teams/${teamId}/users`, { user_id: selected });
      setSelected("");
      await load();
      queryClient.invalidateQueries({ queryKey: ["teams"] });
    } catch (err) {
      notify(errText(err, t("teams.members_add_fail")), { type: "error" });
    }
  };

  const removeMember = async (userId: string) => {
    if (!teamId) return;
    try {
      await api.delete(`/teams/${teamId}/users/${userId}`);
      await load();
    } catch (err) {
      notify(errText(err, t("teams.members_remove_fail")), { type: "error" });
    }
  };

  if (!teamId) return null;
  const memberIds = new Set(members.map((m) => m.id));
  const options = candidates.filter((u) => !memberIds.has(u.id));

  return (
    <section className="mt-6 space-y-3">
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        <UserPlus className="size-4" /> {t("teams.members_title")}
      </h3>
      {isAdmin && (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <select
            className="rounded border bg-background px-2 py-1 text-sm"
            value={selected}
            onChange={(e: ChangeEvent<HTMLSelectElement>) => setSelected(e.target.value)}
          >
            <option value="">{t("teams.member_select")}</option>
            {options.map((u) => (
              <option key={u.id} value={u.id}>
                {u.username}（{u.tenant_name ?? t("teams.member_tenant")}）
              </option>
            ))}
          </select>
          <Button size="sm" onClick={addMember} disabled={!selected}>
            <UserPlus /> {t("teams.member_add")}
          </Button>
        </div>
      )}
      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b text-left text-muted-foreground">
              <th className="p-2">{t("teams.members_user")}</th>
              <th className="p-2">{t("teams.members_role")}</th>
              <th className="p-2 hidden md:table-cell">{t("teams.members_status")}</th>
              <th className="p-2 hidden md:table-cell">{t("teams.members_tenant")}</th>
              {isAdmin && <th className="p-2" />}
            </tr>
          </thead>
          <tbody>
            {members.length === 0 ? (
              <tr>
                <td colSpan={isAdmin ? 5 : 4} className="p-2">
                  {t("teams.members_empty")}
                </td>
              </tr>
            ) : (
              members.map((m) => {
                const isOwner = m.id === record?.owner_id;
                return (
                  <tr key={m.id} className="border-b">
                    <td className="p-2 font-medium">
                      {m.username}
                      {isOwner && (
                        <span className="ml-2 inline-flex items-center gap-1 rounded bg-amber-500/15 px-1.5 py-0.5 text-xs text-amber-600">
                          <Crown className="size-3" /> {t("teams.team_admin")}
                        </span>
                      )}
                    </td>
                    <td className="p-2">
                      {isOwner ? t("teams.team_admin") : roleName.get(m.role) ?? m.role}
                    </td>
                    <td className="p-2 hidden md:table-cell">{statusText(t, m.status)}</td>
                    <td className="p-2 hidden md:table-cell">
                      {record?.tenant_name ?? m.tenant_name ?? <WorkspaceLabel id={m.tenant_id} />}
                    </td>
                    {isAdmin && (
                      <td className="p-2">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => removeMember(m.id)}
                          disabled={isOwner}
                        >
                          <UserMinus /> {t("teams.members_remove")}
                        </Button>
                      </td>
                    )}
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
};
