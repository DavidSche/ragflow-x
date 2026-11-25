#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""T27 — 只对话的角色，在**已部署**实例上成立吗？（doc/301 §10.3 T27）

doc/304 §4 E155 把能力拆出来了：`session:create@chat` 是独立动作，路由、Handler 守卫、
内置矩阵、迁移 191 四处一致，Go 测试与变异都可以证明*代码*同意。但"代码同意"和
"部署环境上这个人真的只能用对话中心"是两件事，中间隔着一次 migrate（151 的
`rgx_schema_version` 停在 190 时，`business_user` 根本没有 `chat|session:create`）。

这个探针造一个**真的用户**，用真的登录、真的 HTTP，走完五个状态：

    S1 配置   租户管理员建自定义角色（四项 allow）、建用户、把角色指派给他
    S2 自述   `/auth/me` 返回的生效权限就是这四项，且**不含** `chat|manage`
    S3 能问   列出助手（`chat|read`）→ 在助手上开一条会话（`chat|session:create`）
    S4 边界   改/删助手、建助手、读助手（`agent|read`）全部 403
    S5 视图   前端据以推导导航的权限集里只有 `chat:execute` 这一族 → 落点是对话中心

S4 是 S3 的反面：只有"能给"没有"不能给"，这个角色等于 `operator` 的别名，E155 的拆分
就没有意义。S2 断言**等于**四项而不是"至少"包含，否则一条偷偷加上的 `chat|manage`
会让 S3 永远绿、S4 永远红，而红的那半总被当成环境问题。

一条必须说出来的副作用：前端登录后第一件事是 `GET /auth/me`，而该路由要求
`user|read`，所以最小可用集合里包含 `user|read` —— 它同时允许列出本工作区用户。
这是当前设计（T27 记账），不是本探针发明的，探针把它算进期望值而不是隐藏它。

    python scripts/probes/t27_chat_only_role.py --offline   # 契约，不联网
    RGX_VERIFY_PASS=… python scripts/probes/t27_chat_only_role.py

Exit: 0 五个状态都成立 / 1 有断言失败 / 2 环境不可用或用法错误 / 3 跑到中途按开关停下
（`--keep` 留下角色与用户，此时 S6 不执行，读数不是"清理过"的读数）。
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

# 期望的生效权限集：四项，一项都不能多。
#
#   chat|read          列出助手（对话中心左侧列表）
#   chat|execute       提问 / 检索（run）
#   chat|session:create 开新会话（E155 拆出来的那个动作）
#   user|read          GET /auth/me —— 前端登录后的第一跳，见模块注释
#
# `chat|manage` 刻意不在里面：它蕴含 read 且允许改/删助手，正是 E155 要区分的那条边。
WANT_GRANTS = [
    ("read", "chat"),
    ("execute", "chat"),
    ("session:create", "chat"),
    ("read", "user"),
]
FORBIDDEN_ACTIONS = [("manage", "chat")]

REQUIREMENTS = [
    Requirement("--base", "RGX_BASE", "deployed console, e.g. http://192.168.4.151:19193",
                default="http://192.168.4.151:19193"),
    Requirement("--user", "RGX_VERIFY_USER", "tenant admin used to provision the role",
                default="admin"),
    Requirement("--password", "RGX_VERIFY_PASS", "console password of that admin",
                secret=True),
]

STEPS = [
    ("S1", "provision: create a tenant role with exactly four grants, a user, assign it"),
    ("S2", "self-view: GET /auth/me returns exactly those four allows and no chat|manage"),
    ("S3", "talk: list chats (chat|read) and open a session (chat|session:create)"),
    ("S4", "boundary: rename/delete/create a chat and read agents -> 403"),
    ("S5", "nav: the granted set derives the conversation-center view, not a wider one"),
    ("S6", "cleanup: delete the drill user and role"),
]

NOTES = [
    "needs migration 191 (session:create@chat) deployed: on schema 190 the drill user "
    "still gets the permission — it comes from the role rows this probe writes, not from "
    "the builtin matrix — but builtin business_user does not, so probe that separately",
    "user|read is part of the minimum because /auth/me requires it; it also allows "
    "listing workspace users (recorded, not hidden)",
]


def grant_key(perm):
    return (str(perm.get("action") or ""), str(perm.get("resource") or ""))


class Drill:
    def __init__(self, args):
        self.args = args
        self.results = []
        self.kept = False
        self.role_name = "t27-chat-only-%s" % secrets.token_hex(3)
        self.user_name = "t27_member_%s" % secrets.token_hex(3)
        self.user_pass = "T27-" + secrets.token_hex(6)

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

    # ── S1 ────────────────────────────────────────────────────────────────
    def s1_provision(self):
        admin = Client(self.args.base)
        admin.login(self.args.user, self.args.password)
        self.admin = admin

        role = admin.ok("POST", "/api/v1/roles", json_body={
            "name": self.role_name,
            "scope": "tenant",
            "description": "T27 drill: talk, never manage",
        })
        self.role_id = role.get("id") or ""
        if not self.role_id:
            raise CaseFail("role create returned no id: %s" % role)

        admin.ok("PUT", "/api/v1/roles/%s/permissions" % self.role_id, json_body={
            "permissions": [
                {"action": a, "resource": r, "effect": "allow"} for a, r in WANT_GRANTS
            ],
        })

        got = admin.ok("GET", "/api/v1/roles/%s/permissions" % self.role_id)
        rows = got if isinstance(got, list) else (got.get("permissions") or got.get("items") or [])
        have = {grant_key(p) for p in rows if str(p.get("effect") or "allow") == "allow"}
        missing = [g for g in WANT_GRANTS if g not in have]
        if missing:
            # The write is not the read: the permissions endpoint is the second
            # transport that says what was stored.
            raise CaseFail("role stored %d grant(s), missing %s" % (len(have), missing))

        user = admin.ok("POST", "/api/v1/users", json_body={
            "username": self.user_name,
            "password": self.user_pass,
            # The role id, not the display name: `GetRole` resolves both, but the
            # drill should fail on "the role I created" and not on a name collision.
            "role": self.role_id,
        })
        self.user_id = user.get("id") or ""
        if not self.user_id:
            raise CaseFail("user create returned no id: %s" % user)
        # The assignment is kept even though CreateUser already took the role: it is
        # the endpoint the UI's role picker calls, and the one that has to accept a
        # custom (non-builtin) role name.
        admin.ok("PUT", "/api/v1/users/%s/role" % self.user_id,
                 json_body={"role": self.role_id})
        print(safe("    role=%s user=%s" % (self.role_id, self.user_id)))

    # ── S2 ────────────────────────────────────────────────────────────────
    def s2_self_view(self):
        member = Client(self.args.base)
        member.login(self.user_name, self.user_pass)
        self.member = member
        me = member.ok("GET", "/api/v1/auth/me")
        perms = me.get("permissions") or []
        allows = {grant_key(p) for p in perms
                  if str(p.get("effect") or "allow") == "allow"}
        denies = {grant_key(p) for p in perms
                  if str(p.get("effect") or "") == "deny"}
        # Equality, not ≥: a superset means the drill's "minimum" is a wish.
        extra = sorted(allows - set(WANT_GRANTS))
        missing = sorted(set(WANT_GRANTS) - allows)
        if missing or extra:
            raise CaseFail("/auth/me allows=%s missing=%s extra=%s" %
                           (sorted(allows), missing, extra))
        bad = [g for g in FORBIDDEN_ACTIONS if g in allows]
        if bad:
            raise CaseFail("role holds forbidden grant(s) %s" % bad)
        print(safe("    allows=%s denies=%d" % (sorted(allows), len(denies))))

    # ── S3 ────────────────────────────────────────────────────────────────
    def s3_talk(self):
        if not getattr(self, "member", None):
            raise CaseSkip("S2 did not produce a member session")
        chats = self.admin.ok("GET", "/api/v1/chats")
        items = chats.get("items") if isinstance(chats, dict) else chats
        if not items:
            made = self.admin.ok("POST", "/api/v1/chats",
                                 json_body={"name": "T27 只对话演练助手"})
            self.chat_id = made.get("id") or ""
        else:
            self.chat_id = items[0].get("id") or ""
        if not self.chat_id:
            raise CaseFail("no chat assistant to talk to")

        status, body = self.member.api("GET", "/api/v1/chats")
        if status != 200:
            raise CaseFail("member cannot list assistants (chat|read): %d %s"
                           % (status, str(body)[:200]))

        sess = self.member.ok("POST", "/api/v1/chats/%s/sessions" % self.chat_id,
                              json_body={"name": "T27 第一条线程"})
        self.session_id = sess.get("id") or ""
        if not self.session_id:
            raise CaseFail("session create returned no id: %s" % sess)
        print(safe("    chat=%s session=%s" % (self.chat_id, self.session_id)))

    # ── S4 ────────────────────────────────────────────────────────────────
    def s4_boundary(self):
        if not getattr(self, "chat_id", ""):
            raise CaseSkip("S3 did not produce a chat id")
        probes = [
            ("PUT", "/api/v1/chats/%s" % self.chat_id, {"name": "renamed by a talker"}),
            ("DELETE", "/api/v1/chats/%s" % self.chat_id, None),
            ("POST", "/api/v1/chats", {"name": "made by a talker"}),
            ("GET", "/api/v1/agents", None),
            ("GET", "/api/v1/datasets", None),
        ]
        wrong = []
        for method, path, payload in probes:
            kw = {"json_body": payload} if payload is not None else {}
            status, body = self.member.api(method, path, **kw)
            if status != 403:
                wrong.append("%s %s -> %d" % (method, path.split("?")[0], status))
            print(safe("    %-6s %-42s %d" % (method, path.split("?")[0], status)))
        if wrong:
            raise CaseFail("chat-only role was not stopped: %s" % "; ".join(wrong))

    # ── S5 ────────────────────────────────────────────────────────────────
    def s5_navigation(self):
        # 前端只据生效权限推导视图（web/src/lib/navigationRoles.ts）。这里不跑 JS，
        # 而是断言它读的那几个键：有 chat|execute 就落对话中心；没有任何别的视图键。
        if not getattr(self, "member", None):
            raise CaseSkip("S2 did not produce a member session")
        me = self.member.ok("GET", "/api/v1/auth/me")
        allows = {grant_key(p) for p in (me.get("permissions") or [])
                  if str(p.get("effect") or "allow") == "allow"}
        if ("execute", "chat") not in allows:
            raise CaseFail("no chat|execute: the UI could not derive the chat view")
        # 这些键一旦出现，侧栏就会有别的入口；它们是"更宽的视图"的机器可判形状。
        wider = [
            ("read", "dataset"), ("manage", "dataset"), ("read", "audit"),
            ("manage", "user"), ("read", "role"), ("manage", "chat"),
            ("read", "system"), ("read", "gateway"), ("read", "report"),
        ]
        present = [g for g in wider if g in allows]
        if present:
            raise CaseFail("role would also see %s" % present)
        if me.get("platform") is True:
            raise CaseFail("drill role must not be platform-scoped")
        print(safe("    view=conversation-center (chat|execute), wider keys=0"))

    # ── S6 ────────────────────────────────────────────────────────────────
    def s6_cleanup(self):
        if self.kept:
            raise CaseSkip("--keep asked for the accounts to stay")
        admin = getattr(self, "admin", None)
        if admin is None:
            raise CaseSkip("S1 did not produce an admin session")
        gone = []
        for kind, rid in (("user", getattr(self, "user_id", "")),
                          ("role", getattr(self, "role_id", ""))):
            if not rid:
                continue
            status, _ = admin.api("DELETE", "/api/v1/%ss/%s" % (kind, rid))
            gone.append("%s=%d" % (kind, status))
        print(safe("    cleanup: %s" % ", ".join(gone)))


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", default="")
    ap.add_argument("--user", default="")
    ap.add_argument("--password", default="")
    ap.add_argument("--offline", action="store_true",
                    help="print the contract and exit 0 without touching the network")
    ap.add_argument("--keep", action="store_true",
                    help="leave the drill role and user in place (S6 skips)")
    args = ap.parse_args(argv)

    if args.offline:
        return print_contract("T27 chat-only role drill", REQUIREMENTS, STEPS, NOTES)

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
    print(safe("T27 chat-only role drill @ %s (started %s)"
               % (args.base, time.strftime("%Y-%m-%d %H:%M:%S"))))
    for sid, fn in (("S1", drill.s1_provision), ("S2", drill.s2_self_view),
                    ("S3", drill.s3_talk), ("S4", drill.s4_boundary),
                    ("S5", drill.s5_navigation), ("S6", drill.s6_cleanup)):
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
        return 3
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except EnvUnavailable as exc:
        print(safe("environment: %s" % exc), file=sys.stderr)
        sys.exit(2)
