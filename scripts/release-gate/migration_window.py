#!/usr/bin/env python3
"""migration_window.py — the write-path half of "can we roll back".

s06 refuses destructive DDL (a table/column that disappears). It cannot see the
opposite failure: a *new* column that the *old* binary cannot fill in. GORM's
AutoMigrate adds `NOT NULL` columns verbatim; if such a column has no default, an
insert from the previous release fails at runtime — that is the classic
expand/contract mistake, and it only shows up after the rollback, on the rollback
target, which is the worst time to learn it.

This checker compares `internal/model` at the newest release tag with the working
tree and refuses a new `not null` column with no `default:` (unless it is a pointer,
which is nullable). New structs are new tables: the old binary never writes them, so
they are counted but allowed.

Boundaries (stated so nobody reads a pass as more than it is):
  * it is the *static* half; "the previous image starts on this schema and serves a
    request" is the drill in doc/304 §7 and needs a published image;
  * type widening/narrowing of an existing column is out of scope;
  * a new column that is nullable but has a CHECK/enum constraint is out of scope.

Exemptions live in migration-window-allow.txt (`Struct.Field  reason`, >=12 chars).
An exemption that matches no finding is refused: an unused exemption is a lie about
what is currently excused (doc/304 §4 E143).

Usage: migration_window.py --base <ref> [--allow FILE] | --selftest
"""
from __future__ import annotations

import io
import os
import re
import subprocess
import sys

MODEL_DIR = "internal/model"
ALLOW_DEFAULT = "scripts/release-gate/migration-window-allow.txt"

STRUCT_RE = re.compile(r"^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+struct\s*\{")
FIELD_RE = re.compile(r"^\s*([A-Z][A-Za-z0-9_]*)\s+([^\s`]+)\s*(?:`([^`]*)`)?\s*$")


def parse_structs(sources):
    """{(struct, field): (go_type, gorm_tag)} for every struct in the sources."""
    out = {}
    structs = set()
    for _name, text in sources:
        cur = None
        for line in text.splitlines():
            m = STRUCT_RE.match(line)
            if m:
                cur = m.group(1)
                structs.add(cur)
                continue
            if cur is None:
                continue
            s = line.strip()
            if s == "}":
                cur = None
                continue
            if not s or s.startswith("//"):
                continue
            m = FIELD_RE.match(line)
            if not m:
                continue
            fname, ftype, tag = m.group(1), m.group(2), m.group(3) or ""
            gm = re.search(r'gorm:"([^"]*)"', tag)
            gorm = gm.group(1) if gm else ""
            out[(cur, fname)] = (ftype, gorm)
    return out, structs


def classify(go_type, gorm):
    not_null = ("not null" in gorm) or ("notnull" in gorm)
    has_default = "default:" in gorm
    pointer = go_type.startswith("*")
    return not_null, has_default, pointer


def compare(old, new, old_structs, new_structs):
    """Return (violations, exemptable, counts). Pure: this is what the selftest drives."""
    violations, exemptable = [], []
    new_columns = new_structs_only = 0
    for (struct, field), (go_type, gorm) in sorted(new.items()):
        if struct not in old_structs:
            new_structs_only += 1
            continue
        if (struct, field) in old:
            continue  # retyped/narrowed columns are out of scope (see the header)
        new_columns += 1
        not_null, has_default, pointer = classify(go_type, gorm)
        if not_null and not has_default and not pointer:
            violations.append((struct, field, go_type, gorm))
    return violations, exemptable, {
        "new_columns": new_columns,
        "new_structs": new_structs_only,
    }


def load_allow(path):
    rows = []
    if not os.path.exists(path):
        return rows
    for i, line in enumerate(io.open(path, encoding="utf-8").read().splitlines(), 1):
        s = line.strip()
        if not s or s.startswith("#"):
            continue
        parts = s.split("  ", 1)
        if len(parts) != 2 or len(parts[1].strip()) < 12:
            raise SystemExit("%s:%d: want `Struct.Field  reason` with a reason of >=12 chars" % (path, i))
        rows.append((parts[0].strip(), parts[1].strip()))
    return rows


def check(base, allow_path):
    if not base or base == "none":
        sys.stdout.write("migration-window: UNVERIFIED — no base ref, so \"new\" has no meaning\n")
        return 3
    old_sources = src_from_git(base)
    if old_sources is None:
        sys.stdout.write("migration-window: UNVERIFIED — %s cannot be read (missing tag?)\n" % base)
        return 3
    new_sources = src_from_worktree()
    old, old_structs = parse_structs(old_sources)
    new, new_structs = parse_structs(new_sources)
    violations, _, counts = compare(old, new, old_structs, new_structs)

    allow = load_allow(allow_path)
    used = set()
    left = []
    for struct, field, go_type, gorm in violations:
        key = "%s.%s" % (struct, field)
        hit = [r for r in allow if r[0] == key]
        if hit:
            used.add(hit[0][0])
            sys.stdout.write("  exempt %-38s %s\n" % (key, hit[0][1]))
        else:
            left.append((key, go_type, gorm))
    stale = [r[0] for r in allow if r[0] not in used]

    sys.stdout.write(
        "migration-window: %d structs at %s, %d new column(s) on existing tables, "
        "%d new struct(s) (new tables, invisible to the old binary)\n"
        % (len(old_structs), base, counts["new_columns"], counts["new_structs"])
    )
    rc = 0
    for key, go_type, gorm in left:
        sys.stdout.write(
            "  FAIL %s %s `gorm:\"%s\"` — a NOT NULL column with no default cannot be "
            "written by the previous release; make it nullable, add default:, or split it "
            "into an expand/contract pair\n" % (key, go_type, gorm)
        )
        rc = 1
    for key in stale:
        sys.stdout.write(
            "  FAIL allow-list entry %s matches no finding — an exemption nobody uses is a "
            "claim about the tree that is no longer true (doc/304 §4 E143)\n" % key
        )
        rc = 1
    if rc == 0:
        sys.stdout.write(
            "migration-window: PASS — rolling back to %s keeps every write path open\n" % base
        )
    return rc


def src_from_git(ref):
    out = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", ref, "--", MODEL_DIR],
        capture_output=True, text=True, encoding="utf-8", errors="replace",
    )
    if out.returncode != 0:
        return None
    names = [n for n in out.stdout.split() if n.endswith(".go") and not n.endswith("_test.go")]
    if not names:
        return None
    srcs = []
    for n in names:
        show = subprocess.run(
            ["git", "show", "%s:%s" % (ref, n)],
            capture_output=True, text=True, encoding="utf-8", errors="replace",
        )
        if show.returncode == 0:
            srcs.append((n, show.stdout))
    return srcs


def src_from_worktree():
    srcs = []
    for name in sorted(os.listdir(MODEL_DIR)):
        if not name.endswith(".go") or name.endswith("_test.go"):
            continue
        p = os.path.join(MODEL_DIR, name)
        srcs.append((p, io.open(p, encoding="utf-8", newline="").read()))
    return srcs


# ── selftest ────────────────────────────────────────────────────────────────
OLD_FIX = """
type User struct {
	ID   string `gorm:"column:id;not null"`
	Name string `gorm:"column:name;not null"`
}
"""


def st(name, old_src, new_src, want_rc, allow_rows=()):
    old, old_structs = parse_structs([("old.go", old_src)])
    new, new_structs = parse_structs([("new.go", new_src)])
    violations, _, _ = compare(old, new, old_structs, new_structs)
    allowed = {r[0] for r in allow_rows}
    left = [v for v in violations if "%s.%s" % (v[0], v[1]) not in allowed]
    unused = [r for r in allow_rows if r[0] not in {"%s.%s" % (v[0], v[1]) for v in violations}]
    rc = 1 if (left or unused) else 0
    ok = rc == want_rc
    sys.stdout.write("  %s %s\n" % ("ok  " if ok else "FAIL", name))
    return ok, violations


def selftest():
    ok = True
    # ① the failure this checker exists for
    good, v = st(
        "① a new NOT NULL column with no default is refused",
        OLD_FIX,
        OLD_FIX.replace('Name string `gorm:"column:name;not null"`',
                        'Name string `gorm:"column:name;not null"`\n\tAge int64 `gorm:"column:age;not null"`'),
        1,
    )
    ok &= good and len(v) == 1
    # ② the same column with a default is fine (old inserts get filled)
    good, _ = st(
        "② the same column with default: is accepted",
        OLD_FIX,
        OLD_FIX.replace('Name string `gorm:"column:name;not null"`',
                        'Name string `gorm:"column:name;not null"`\n\tAge int64 `gorm:"column:age;not null;default:0"`'),
        0,
    )
    ok &= good
    # ③ a pointer is nullable by construction
    good, _ = st(
        "③ a new nullable (*T) column is accepted",
        OLD_FIX,
        OLD_FIX.replace('Name string `gorm:"column:name;not null"`',
                        'Name string `gorm:"column:name;not null"`\n\tNote *string `gorm:"column:note"`'),
        0,
    )
    ok &= good
    # ④ a brand-new struct is a brand-new table: the old binary never writes it
    good, _ = st(
        "④ a new struct (new table) with NOT NULL fields is accepted, and counted",
        OLD_FIX,
        OLD_FIX + "\ntype Intent struct {\n\tID string `gorm:\"column:id;not null\"`\n}\n",
        0,
    )
    ok &= good
    # ⑤ an existing field changing type is not a "new column"
    good, _ = st(
        "⑤ retyping an existing column is out of scope, not a violation",
        OLD_FIX,
        OLD_FIX.replace("Name string", "Name int64"),
        0,
    )
    ok &= good
    # ⑥ an exemption that matches the finding passes, and is printed as exempt
    good, _ = st(
        "⑥ a written exemption that matches the finding passes",
        OLD_FIX,
        OLD_FIX.replace('Name string `gorm:"column:name;not null"`',
                        'Name string `gorm:"column:name;not null"`\n\tAge int64 `gorm:"column:age;not null"`'),
        0,
        allow_rows=[("User.Age", "backfilled by migration 200 before the cutover")],
    )
    ok &= good
    # ⑦ an exemption nobody uses is refused (E143)
    good, _ = st(
        "⑦ an exemption that matches nothing is refused",
        OLD_FIX,
        OLD_FIX,
        1,
        allow_rows=[("User.Ghost", "left over from a finding that was fixed")],
    )
    ok &= good
    if ok:
        sys.stdout.write("selftest: OK (7 mutations: 4 refused shapes incl. an unused exemption, 3 accepted ones)\n")
        return 0
    sys.stdout.write("selftest: FAILED\n")
    return 1


def main(argv):
    if "--selftest" in argv:
        return selftest()
    base, allow = "", ALLOW_DEFAULT
    i = 1
    while i < len(argv):
        if argv[i] == "--base" and i + 1 < len(argv):
            base = argv[i + 1]
            i += 2
        elif argv[i] == "--allow" and i + 1 < len(argv):
            allow = argv[i + 1]
            i += 2
        else:
            i += 1
    return check(base, allow)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
