#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""T29 — 两个工作区之间的角色边界，在**已部署**实例上成立吗？（doc/304 §4 E175）

E175 有两条主张，单测各证了一半：

    ① 角色的归属是**写**的属性：一个工作区建的角色，别的租户看不见、改不了、
       也分配不到自己人头上；
    ② 平台身份是**持**的属性：「能通过 parent_id 走到平台管理员」不等于「是平台
       管理员」——父角色提权必须被写路径拒绝，读路径再拒绝一次。

Go 测试证明的是 service 层同意。这个探针用真租户、真登录、真 HTTP 走一遍：

    S1 造两个工作区，各一个工作区管理员，各自登录
    S2 归属  A 建角色 → 回包的 tenant_id 是 A；写进去的授权读得回来
    S3 可见  A 的列表里有它；B 的列表里没有；B 仍然看得到平台自有的内置角色
    S4 404   B 改 / 读授权 / 改授权 / 删：四个 404；同一组调用在 A 自己身上是 200
    S5 403   A 建平台角色 403；A 改平台自有的内置角色 403；两类越界父角色分别 41161 / 41162
    S6 身份  成员继承工作区管理员 → 权限是超集，`platform` 仍是 false，且建不了平台角色
    S7 分配  B 把 A 的角色分给自己人 → 40021；B 改 A 的人的角色 → 403
    S8 读写分界  共享角色的授权**读得到**（200）但写不了（403）；列表的 owned/editable
       与写路径一致；平台把角色标记为「待确认」后租户看不见、读不到、分配不到，
       改回启用后恢复；改动作用域时遗留的父角色被 41161 拦下（doc/315 1/3/新发现）
    S9 清理  强制删两个工作区、删掉 A 的角色，确认角色与账号都没了

S4 的四个 404 是这条边界唯一诚实的答案（E139）：403 会告诉「这个 id 是别人的角色」，
而那正是租户隔离要隐藏的东西。S4 同时跑一遍 A 对自己的角色做同样的事——否则一个
「所有角色都 404」的实现也能让四个断言全绿。S5 的 403 是另一种答案：平台自有的角色
本来就对每个租户可见，拒绝改写它是诚实的，不是隐瞒。

S8 是 doc/315 复审结论里三条缺陷的现场读数：**可见**与**可写**是两个不同的判断
（共享角色的权限矩阵要能打开，但不能保存），`owned`/`editable` 必须和写路径给出同一个
答案（前端靠它决定渲染不渲染按钮），而改作用域时必须重新校验已经存在的 `parent_id`
—— 否则会留下一行「平台作用域 + 工作区父角色」，把写路径刚拒绝的组合写进库。

    python scripts/probes/t29_role_tenant_isolation.py --offline   # 契约，不联网
    RGX_BASE=http://<host>:19193 RGX_VERIFY_PASS=… \\
        python scripts/probes/t29_role_tenant_isolation.py

Exit: 0 九个状态都成立 / 1 有断言失败 / 2 环境不可用或用法错误 / 3 跑到中途按开关停下
（`--keep` 留下两个工作区，此时 S8 不执行，读数不是"清理过"的读数）。
"""

from __future__ import annotations

import argparse
import os
import secrets
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from probe_common import (  # noqa: E402
    CaseFail, CaseSkip, Client, EnvUnavailable, Requirement, Usage,
    print_contract, safe,
)

# The host is configuration. This probe ships **no** default for it, unlike T27: the
# only host that exists for this drill is a private one, and a literal RFC1918 address
# in a tracked file is the fingerprint scripts/secret-scan.sh warns about. An operator
# who is about to prove a tenant boundary can copy one environment variable.
REQUIREMENTS = [
    Requirement("--base", "RGX_BASE", "deployed console, e.g. http://<host>:19193"),
    Requirement("--user", "RGX_VERIFY_USER", "platform admin used to create the workspaces",
                default="admin"),
    Requirement("--password", "RGX_VERIFY_PASS", "console password of that platform admin",
                secret=True),
]

STEPS = [
    ("S1", "provision: two workspaces, one tenant admin each, both logged in"),
    ("S2", "ownership: a tenant admin's new role is stamped with its own tenant"),
    ("S3", "visibility: the role is in A's list, absent from B's, shared builtins still visible"),
    ("S4", "boundary: B's edit/read-grants/write-grants/delete on A's role -> 404 (A itself: 200)"),
    ("S5", "boundary: platform role create, shared-role edit -> 403; cross-scope/cross-tenant parent -> 41161/41162"),
    ("S6", "identity: inheriting tenant_admin grants the closure, not a platform identity"),
    ("S7", "assignment: B cannot hand out A's role (40021) or edit A's member (403); the platform can reach both workspaces but still cannot mix them (40021)"),
    ("S8", "read vs write: shared role grants readable (200) not writable (403); owned/editable agree; a parked role disappears and returns; a scope change re-checks its parent (41161)"),
    ("S9", "cleanup: force-delete both workspaces and A's roles, then confirm they are gone"),
]

NOTES = [
    "--base has no default: this is a private drill host, and a tracked file is not the "
    "place to write one down (scripts/secret-scan.sh treats RFC1918 addresses as a warning)",
    "S1 needs a platform admin: creating a workspace is `manage|tenant`, and the point of "
    "the drill is what a *tenant* admin can then do with what it authored",
    "S9 deletes the workspaces with ?force=true, which is the only path that removes the "
    "tenant admin too: the last admin of a workspace cannot be deleted on its own (40905)",
    "S8 reads a shared role's grants as a tenant admin on purpose: those grants are what the "
    "role hands out, and a console that cannot show them cannot be used at all (doc/315 1)",
    "S7 ③ is about the holder, not the actor: a platform admin may reach every workspace and "
    "still may not give one workspace's role to another's member, because authz then refuses "
    "that account with 40305 on every request (doc/315 review)",
    "the router has no GET /roles/{id}: a role is read through the list or through "
    "/roles/{id}/permissions, and a NoRoute 404 is not evidence that a role is gone",
    "the read-side half of ② (a row written before the fix) cannot be created over HTTP "
    "any more; it is exercised by a psql UPDATE in doc/305, not by this probe",
]


def code_of(body):
    return int((body or {}).get("code") or 0)


class Drill:
    def __init__(self, args):
        self.args = args
        self.results = []
        self.kept = False
        self.tag = secrets.token_hex(3)
        self.role_name = "t29-role-a-%s" % self.tag
        self.heir_name = "t29-heir-a-%s" % self.tag
        self.pass_a = "T29a-" + secrets.token_hex(6)
        self.pass_b = "T29b-" + secrets.token_hex(6)
        self.pass_m = "T29m-" + secrets.token_hex(6)

    def step(self, sid, fn):
        try:
            fn()
            self.results.append((sid, "PASS", ""))
            print(safe("[%s] PASS" % sid))
        except CaseSkip as exc:
            self.results.append((sid, "SKIP", str(exc)))
            print(safe("[%s] SKIP  %s" % (sid, exc)))
        except CaseFail as exc:
            self.results.append((sid, "FAIL", str(exc)))
            print(safe("[%s] FAIL  %s" % (sid, exc)))

    # a call whose answer is a status *and* a code: both are asserted, because a
    # handler that returns 200 with `code: 403` inside the envelope is a shape this
    # codebase uses elsewhere, and it would make a status-only assertion lie.
    def _expect(self, client, method, path, want_status, want_code=None, payload=None, who=""):
        kw = {"json_body": payload} if payload is not None else {}
        status, body = client.api(method, path, **kw)
        if want_code is not None and code_of(body) != want_code:
            raise CaseFail("%s %s %s -> %d code=%s, want %d (%s)"
                           % (who, method, path, status, code_of(body), want_code,
                              str(body.get("message"))[:120]))
        if status != want_status:
            raise CaseFail("%s %s %s -> %d, want %d (%s)"
                           % (who, method, path, status, want_status,
                              str(body.get("message"))[:120]))
        return body

    # ── S1 ────────────────────────────────────────────────────────────────
    def s1_provision(self):
        admin = Client(self.args.base)
        admin.login(self.args.user, self.args.password)
        self.admin = admin

        def workspace(suffix):
            body = admin.ok("POST", "/api/v1/tenants",
                            json_body={"name": "t29-%s-%s" % (suffix, self.tag)})
            tid = body.get("id") or ""
            if not tid:
                raise CaseFail("tenant create returned no id: %s" % body)
            return tid

        self.tenant_a = workspace("a")
        self.tenant_b = workspace("b")
        if self.tenant_a == self.tenant_b:
            raise CaseFail("both workspaces got the same id: %s" % self.tenant_a)

        def tenant_admin(suffix, tenant_id, password):
            name = "t29_%s_%s" % (suffix, self.tag)
            body = admin.ok("POST", "/api/v1/users", json_body={
                "username": name, "password": password,
                "role": "tenant_admin", "tenant_id": tenant_id,
            })
            uid = body.get("id") or ""
            if not uid:
                raise CaseFail("user create returned no id: %s" % body)
            client = Client(self.args.base)
            client.login(name, password)
            return uid, client

        self.user_a, self.client_a = tenant_admin("a_admin", self.tenant_a, self.pass_a)
        self.user_b, self.client_b = tenant_admin("b_admin", self.tenant_b, self.pass_b)
        # A second, ordinary member of B. S7 must hand a role *to somebody*, and B's own
        # admin cannot be the target: the handler refuses self-service role changes
        # (40906) before any ownership question is asked, which would make the drill
        # pass for the wrong reason.
        body = admin.ok("POST", "/api/v1/users", json_body={
            "username": "t29_b_member_%s" % self.tag, "password": self.pass_b + "m",
            "role": "viewer", "tenant_id": self.tenant_b,
        })
        self.user_b_member = body.get("id") or ""
        if not self.user_b_member:
            raise CaseFail("second member of B was not created: %s" % body)
        print(safe("    A=%s B=%s" % (self.tenant_a, self.tenant_b)))

    # ── S2 ────────────────────────────────────────────────────────────────
    def s2_ownership(self):
        if not getattr(self, "client_a", None):
            raise CaseSkip("S1 did not produce a tenant admin session")
        body = self.client_a.ok("POST", "/api/v1/roles", json_body={
            "name": self.role_name, "scope": "tenant",
            "description": "T29 drill: belongs to workspace A",
        })
        self.role_id = body.get("id") or ""
        if not self.role_id:
            raise CaseFail("role create returned no id: %s" % body)
        owner = body.get("tenant_id")
        if owner != self.tenant_a:
            # `tenant_id: ""` is the answer that made E175 possible: a tenant-authored
            # role that belongs to the platform is visible (and assignable) everywhere.
            raise CaseFail("role tenant_id=%r, want %r (an empty owner means the role "
                           "leaked to every workspace)" % (owner, self.tenant_a))

        self.client_a.ok("PUT", "/api/v1/roles/%s/permissions" % self.role_id, json_body={
            "permissions": [{"action": "read", "resource": "chat", "effect": "allow"}],
        })
        got = self.client_a.ok("GET", "/api/v1/roles/%s/permissions" % self.role_id)
        rows = got if isinstance(got, list) else (got.get("permissions") or [])
        if not any(str(p.get("resource")) == "chat" and str(p.get("action")) == "read"
                   for p in rows):
            raise CaseFail("the grant written was not the grant read back: %s" % rows)
        print(safe("    role=%s owner=%s" % (self.role_id, owner)))

    # ── S3 ────────────────────────────────────────────────────────────────
    def s3_visibility(self):
        if not getattr(self, "role_id", ""):
            raise CaseSkip("S2 did not produce a role")

        def roles(client):
            body = client.ok("GET", "/api/v1/roles")
            return body if isinstance(body, list) else (body.get("items") or body.get("data") or [])

        a_rows = roles(self.client_a)
        b_rows = roles(self.client_b)
        a_ids = {r.get("id") for r in a_rows}
        b_ids = {r.get("id") for r in b_rows}
        if self.role_id not in a_ids:
            raise CaseFail("workspace A cannot see the role it just created")
        if self.role_id in b_ids:
            raise CaseFail("workspace B sees workspace A's role: id=%s" % self.role_id)
        # The shared built-ins must still be visible, or the isolation was achieved by
        # breaking the product: every workspace needs the roles it may hand out. The
        # built-in ids are fixed strings, so this is a name-free assertion.
        if "tenant_admin" not in b_ids:
            raise CaseFail("workspace B can no longer see the shared tenant_admin role: "
                           "isolation that hides the built-ins is not isolation")
        print(safe("    A sees %d role(s), B sees %d, B sees A's role: no"
                   % (len(a_ids), len(b_ids))))

    # ── S4 ────────────────────────────────────────────────────────────────
    def s4_cross_tenant_404(self):
        if not getattr(self, "role_id", ""):
            raise CaseSkip("S2 did not produce a role")
        calls = [
            ("PUT", "/api/v1/roles/%s" % self.role_id, {"description": "not mine"}),
            ("GET", "/api/v1/roles/%s/permissions" % self.role_id, None),
            ("PUT", "/api/v1/roles/%s/permissions" % self.role_id,
             {"permissions": [{"action": "manage", "resource": "user", "effect": "allow"}]}),
            ("DELETE", "/api/v1/roles/%s" % self.role_id, None),
        ]
        for method, path, payload in calls:
            self._expect(self.client_b, method, path, 404, 404, payload, who="B")
        # The control. Without it, an implementation that 404s every role id would pass
        # the four lines above while nobody could edit anything at all.
        self._expect(self.client_a, "PUT", "/api/v1/roles/%s" % self.role_id, 200, 0,
                     {"description": "mine after all"}, who="A")
        print(safe("    B: 4x 404 (edit/read grants/write grants/delete) · A: 200"))

    # ── S5 ────────────────────────────────────────────────────────────────
    def s5_platform_boundary(self):
        if not getattr(self, "client_a", None):
            raise CaseSkip("S1 did not produce a tenant admin session")
        # ① a tenant admin cannot author a platform-scoped role at all
        self._expect(self.client_a, "POST", "/api/v1/roles", 403, 403,
                     {"name": "t29-plat-%s" % self.tag, "scope": "platform"}, who="A")
        # ② a platform-owned (shared) role is visible but not editable: 403, not 404 —
        #    telling a tenant admin "not found" about a role they can see in their own
        #    picker would be the wrong kind of quiet.
        self._expect(self.client_a, "PUT", "/api/v1/roles/tenant_admin", 403, 403,
                     {"description": "mine to rewrite"}, who="A")
        self._expect(self.client_a, "PUT", "/api/v1/roles/platform_admin", 403, 403,
                     {"description": "mine to rewrite"}, who="A")
        # ③ the escalation itself: a tenant role whose parent is the platform admin.
        #    Refused by scope (41161), before anything looks at what it would grant.
        self._expect(self.client_a, "POST", "/api/v1/roles", 400, 41161,
                     {"name": "t29-esc-%s" % self.tag, "scope": "tenant",
                      "parent_id": "platform_admin"}, who="A")
        # ④ the other direction: a parent owned by another tenant does not exist here.
        body = self.client_a.api("POST", "/api/v1/roles", json_body={
            "name": "t29-cross-%s" % self.tag, "scope": "tenant",
            "parent_id": self.role_id,
        })
        # The role id above is A's own, so this one must be *allowed*: it is the check
        # that ④ is about the parent's owner and not about parents in general.
        if body[0] != 200:
            raise CaseFail("A's own role as a parent was refused: %d %s"
                           % (body[0], str(body[1])[:160]))
        self.child_id = (body[1].get("data") or {}).get("id") or ""
        # Now the real ④: B tries to hang its own role under A's role.
        self._expect(self.client_b, "POST", "/api/v1/roles", 400, 41162,
                     {"name": "t29-steal-%s" % self.tag, "scope": "tenant",
                      "parent_id": self.role_id}, who="B")
        print(safe("    platform create 403 · shared edit 403 · platform parent 41161 · "
                   "foreign parent 41162"))

    # ── S6 ────────────────────────────────────────────────────────────────
    def s6_identity(self):
        if not getattr(self, "client_a", None):
            raise CaseSkip("S1 did not produce a tenant admin session")
        # The legitimate inheritance: a tenant-scoped child of the shared tenant_admin.
        # E175 is not "inheritance is bad" — it is "inheritance must not move you up the
        # scope ladder or across tenants". This role is the control.
        body = self.client_a.ok("POST", "/api/v1/roles", json_body={
            "name": self.heir_name, "scope": "tenant", "parent_id": "tenant_admin",
        })
        self.heir_id = body.get("id") or ""
        if not self.heir_id:
            raise CaseFail("inheriting role create returned no id: %s" % body)
        if body.get("tenant_id") != self.tenant_a:
            raise CaseFail("inheriting role is owned by %r, want %r"
                           % (body.get("tenant_id"), self.tenant_a))

        member = self.admin.ok("POST", "/api/v1/users", json_body={
            "username": "t29_member_%s" % self.tag, "password": self.pass_m,
            "role": self.heir_id, "tenant_id": self.tenant_a,
        })
        member_id = member.get("id") or ""
        if not member_id:
            raise CaseFail("member create returned no id: %s" % member)
        client = Client(self.args.base)
        client.login("t29_member_%s" % self.tag, self.pass_m)
        self.member_client, self.member_id = client, member_id

        me = client.ok("GET", "/api/v1/auth/me")
        allows = {(str(p.get("action")), str(p.get("resource")))
                  for p in (me.get("permissions") or [])
                  if str(p.get("effect") or "allow") == "allow"}
        # ① the closure arrived (otherwise the control role inherited nothing and the
        #    `platform is False` below would be true for the boring reason)
        for want in [("manage", "user"), ("manage", "role")]:
            if want not in allows:
                raise CaseFail("member does not hold %s from tenant_admin: %d grants"
                               % (want, len(allows)))
        # ② and it did not become a platform identity. This is the assertion E175 is
        #    about: before the fix `ac.platform` was "a platform role is reachable in the
        #    closure", so any reachable platform role made the holder a platform admin.
        if me.get("platform") is True:
            raise CaseFail("a member inheriting tenant_admin reports platform=true")
        # ③ the platform answer is not merely a display flag: the privileged action that
        #    reads it must still be refused.
        self._expect(client, "POST", "/api/v1/roles", 403, 403,
                     {"name": "t29-member-plat-%s" % self.tag, "scope": "platform"},
                     who="member")
        print(safe("    member grants=%d platform=false, platform role create 403"
                   % len(allows)))

    # ── S7 ────────────────────────────────────────────────────────────────
    def s7_assignment(self):
        if not getattr(self, "member_id", ""):
            raise CaseSkip("S6 did not produce a member")
        # ① handing out another workspace's role is refused by *assignability* (40021) —
        #    the role does not exist for B, and the answer is a bad request rather than a
        #    404 because the id arrived in a request body, not a path.
        self._expect(self.client_b, "PUT", "/api/v1/users/%s/role" % self.user_b_member,
                     400, 40021, {"role": self.role_id}, who="B")
        # ② and touching another workspace's user is a 403 (E139 keeps 404 for roles;
        #    a user id is already known to the caller through their own admin screens).
        self._expect(self.client_b, "PUT", "/api/v1/users/%s/role" % self.member_id, 403, 403,
                     {"role": "tenant_admin"}, who="B")
        # ③ the platform can reach both workspaces, and that reach is not permission to
        #    mix them: the pair (role, holder) still has to belong to one workspace. Until
        #    this rule the write succeeded and the account failed every later request with
        #    40305 "a role assigned to this account belongs to another tenant".
        self._expect(self.admin, "PUT", "/api/v1/users/%s/role" % self.user_b_member,
                     400, 40021, {"role": self.role_id}, who="platform")
        # ④ the control: the same role, the same actor, a holder in its own workspace. The
        #    refusal above is about the pair, not about the role or the actor.
        self.admin.ok("PUT", "/api/v1/users/%s/role" % self.member_id,
                      json_body={"role": self.role_id})
        print(safe("    B assigning A's role -> 40021 · B editing A's member -> 403 · "
                   "platform assigning A's role inside B -> 40021, inside A -> 200"))

    # ── S8 ────────────────────────────────────────────────────────────────
    def s8_read_vs_write(self):
        if not getattr(self, "client_a", None):
            raise CaseSkip("S1 did not produce a tenant admin session")

        # ① finding 1: a shared role's grants are readable by the tenant admin that may
        #    hand the role out. This used to be a 403, so the console's permission dialog
        #    opened on an error for every built-in.
        body = self.client_a.ok("GET", "/api/v1/roles/tenant_admin/permissions")
        rows = body if isinstance(body, list) else (body.get("permissions") or [])
        if not rows:
            raise CaseFail("tenant_admin's permission matrix came back empty to a tenant admin")
        # ② and the write stays refused: "readable" must not have been implemented as
        #    "owned". tenant_admin is a built-in, so this is 403 before 40082 by design.
        self._expect(self.client_a, "PUT", "/api/v1/roles/tenant_admin/permissions", 403, 403,
                     {"permissions": [{"action": "manage", "resource": "user",
                                       "effect": "allow"}]}, who="A")

        # ③ the list flags the console renders buttons from must agree with the write
        #    path, or the UI offers a button the server refuses (doc/315 1, second half).
        by_id = {r.get("id"): r for r in self._roles(self.client_a)}
        own = by_id.get(self.role_id) or {}
        shared = by_id.get("tenant_admin") or {}
        if own.get("editable") is not True or own.get("owned") is not True:
            raise CaseFail("A's own role is not editable for A: %s"
                           % {k: own.get(k) for k in ("owned", "editable")})
        if shared.get("editable") is not False:
            raise CaseFail("a shared built-in is offered as editable: editable=%r"
                           % (shared.get("editable"),))

        # ④ a parked (quarantined) role: same row, switched by the platform. Tenants must
        #    stop seeing it entirely — list, grants and assignment — and get it back when
        #    the platform re-authorises it. Before doc/315 a legacy role with no
        #    unambiguous owner stayed shared with every workspace instead.
        body = self.client_a.ok("POST", "/api/v1/roles", json_body={
            "name": "t29_parked_%s" % self.tag, "scope": "tenant",
            "parent_id": "tenant_admin",
        })
        self.parked_id = body.get("id") or ""
        if not self.parked_id:
            raise CaseFail("parked-role create returned no id: %s" % body)
        self.admin.ok("PUT", "/api/v1/roles/%s" % self.parked_id,
                      json_body={"status": "quarantined"})
        ids = self._listed_ids(self.client_a)
        if self.parked_id in ids:
            raise CaseFail("a quarantined role is still listed to a tenant admin")
        self._expect(self.client_a, "GET",
                     "/api/v1/roles/%s/permissions" % self.parked_id, 404, 404, None, who="A")
        self._expect(self.client_b, "PUT", "/api/v1/users/%s/role" % self.user_b_member,
                     400, 40021, {"role": self.parked_id}, who="B")
        # Only the platform can lift it, and doing so needs no other field.
        self.admin.ok("PUT", "/api/v1/roles/%s" % self.parked_id,
                      json_body={"status": "active"})
        if self.parked_id not in self._listed_ids(self.client_a):
            raise CaseFail("a re-authorised role is still hidden from its tenant")
        self.client_a.ok("GET", "/api/v1/roles/%s/permissions" % self.parked_id)

        # ⑤ finding 3: widening the scope keeps the existing parent unless it is
        #    replaced in the same request. The row has parent_id=tenant_admin, so a
        #    tenant→platform move must be refused as a cross-scope parent (41161) rather
        #    than leave a pair the create path calls invalid.
        self._expect(self.admin, "PUT", "/api/v1/roles/%s" % self.parked_id, 400, 41161,
                     {"scope": "platform"}, who="platform")
        # Nothing half-written: the row is still tenant-scoped with its parent.
        # Read it from the list — the router exposes no GET /roles/{id}, and asking
        # for one would "pass" on a 404 that means "no such route" (the trap S9 used
        # to fall into before this step was added).
        stored = self._role_row(self.admin, self.parked_id)
        if not stored:
            raise CaseFail("the platform admin cannot see the role it just updated")
        if stored.get("scope") != "tenant" or stored.get("parent_id") != "tenant_admin":
            raise CaseFail("the refused scope change was partially persisted: %s"
                           % {k: stored.get(k) for k in ("scope", "parent_id")})
        # The same move succeeds when the parent is replaced in the same request: the
        # rule is about the (scope, parent) pair, not about widening as such.
        moved = self.admin.ok("PUT", "/api/v1/roles/%s" % self.parked_id,
                              json_body={"scope": "platform", "parent_id": "platform_admin"})
        if moved.get("scope") != "platform":
            raise CaseFail("a legal scope+parent change did not apply: %s" % moved.get("scope"))
        print(safe("    shared read 200 / write 403 · owned/editable agree · parked: hidden, "
                   "404, 40021, restored · scope change: 41161 then applied"))

    def _role_row(self, client, role_id):
        rows = self._roles(client)
        for row in rows:
            if row.get("id") == role_id:
                return row
        return None

    def _roles(self, client):
        status, listing = client.api("GET", "/api/v1/roles")
        if status != 200:
            raise CaseFail("role list -> %d" % status)
        rows = listing.get("data") if isinstance(listing, dict) else listing
        if isinstance(rows, dict):
            rows = rows.get("items") or rows.get("data")
        return rows or []

    def _listed_ids(self, client):
        return {r.get("id") for r in self._roles(client)}

    # ── S9 ────────────────────────────────────────────────────────────────
    def s9_cleanup(self):
        if self.kept:
            raise CaseSkip("--keep asked for the workspaces to stay")
        if not getattr(self, "admin", None):
            raise CaseSkip("S1 did not produce a platform admin session")
        # Workspaces first, roles second. `force` is what deletes the tenant admin as
        # well: the last active admin of a workspace cannot be removed on its own (40905),
        # so the other order leaves both workspaces behind and — worse — refuses to delete
        # the roles the members still hold (40083).
        for tid in (self.tenant_a, self.tenant_b):
            status, body = self.admin.api("DELETE", "/api/v1/tenants/%s?force=true" % tid)
            if status != 200:
                raise CaseFail("force-deleting workspace %s failed: %d code=%s %s"
                               % (tid[:8], status, code_of(body), str(body.get("message"))[:120]))
        # The workspace delete removes users, teams and dataset links — not roles. A role
        # left behind is a role nobody owns, so it is deleted explicitly, children first
        # (a role with children refuses to be deleted, 40084).
        for name, rid in (("parked", getattr(self, "parked_id", "")),
                          ("heir", getattr(self, "heir_id", "")),
                          ("child", getattr(self, "child_id", "")),
                          ("role", getattr(self, "role_id", ""))):
            if not rid:
                continue
            status, body = self.admin.api("DELETE", "/api/v1/roles/%s" % rid)
            if status != 200:
                raise CaseFail("deleting the drill %s role failed: %d code=%s %s"
                               % (name, status, code_of(body),
                                  str(body.get("message"))[:120]))
        # The confirmation is a second reader: the delete's 200 says the row was removed,
        # not that the role is unreachable — ask for it. Both questions are asked through
        # routes that exist: GET /roles/{id} does not (NoRoute answers 404 for any id, so
        # it would have "confirmed" the deletion of a role that was still there).
        if self._role_row(self.admin, self.role_id):
            raise CaseFail("the deleted role is still listed")
        self._expect(self.admin, "GET", "/api/v1/roles/%s/permissions" % self.role_id,
                     404, 404, None, who="platform")
        member = Client(self.args.base)
        status, body = member.api("POST", "/api/v1/auth/login",
                                  json_body={"username": "t29_member_%s" % self.tag,
                                             "password": self.pass_m})
        if status == 200:
            raise CaseFail("the deleted member can still log in")
        print(safe("    roles deleted, workspaces force-deleted, role 404, member login %d"
                   % status))


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", default="")
    ap.add_argument("--user", default="")
    ap.add_argument("--password", default="")
    ap.add_argument("--offline", action="store_true",
                    help="print the contract and exit 0 without touching the network")
    ap.add_argument("--keep", action="store_true",
                    help="leave both workspaces in place (S8 skips)")
    args = ap.parse_args(argv)

    if args.offline:
        return print_contract("T29 role tenant isolation drill", REQUIREMENTS, STEPS, NOTES)

    try:
        for key in ("base", "user", "password"):
            if not getattr(args, key):
                req = [r for r in REQUIREMENTS if r.flag == "--" + key][0]
                setattr(args, key, req.resolve(args))
    except Usage as exc:
        print(safe("usage: %s" % exc), file=sys.stderr)
        return 2

    drill = Drill(args)
    drill.kept = args.keep
    print(safe("T29 role tenant isolation drill @ %s (started %s, tag %s)"
               % (args.base, time.strftime("%Y-%m-%d %H:%M:%S"), drill.tag)))
    for sid, fn in (("S1", drill.s1_provision), ("S2", drill.s2_ownership),
                    ("S3", drill.s3_visibility), ("S4", drill.s4_cross_tenant_404),
                    ("S5", drill.s5_platform_boundary), ("S6", drill.s6_identity),
                    ("S7", drill.s7_assignment), ("S8", drill.s8_read_vs_write),
                    ("S9", drill.s9_cleanup)):
        drill.step(sid, fn)

    failed = [r for r in drill.results if r[1] == "FAIL"]
    skipped = [r for r in drill.results if r[1] == "SKIP"]
    print("")
    print(safe("summary: %d PASS / %d FAIL / %d SKIP"
               % (len(drill.results) - len(failed) - len(skipped), len(failed), len(skipped))))
    if failed:
        for sid, _, msg in failed:
            print(safe("  %s %s" % (sid, msg)), file=sys.stderr)
        return 1
    if skipped:
        print(safe("  (a skipped step is not a passed step: the drill did not fully run)"))
        return 3
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except EnvUnavailable as exc:
        print(safe("environment: %s" % exc), file=sys.stderr)
        sys.exit(2)
