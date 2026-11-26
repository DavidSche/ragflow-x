#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Stamp the generated OpenAPI artifacts with the product version they describe (doc/310 T310-09).

The problem T310-09 names is small and real: `info.version` was the literal `1.0.0` from
`cmd/server/main.go`'s annotation, so two documents could not be told apart by reading them.

What "the version this spec describes" means — one rule, one tracked source:

  * `CHANGELOG.md` is tracked, so a rewriter reading it keeps generation reproducible from a
    clean checkout (`s25` compares a fresh generation against the index; anything read from
    the network or from tags would make "no drift" a statement about this machine).
  * If `## [Unreleased]` announces a target version (`**v0.7.0「…」**`), that is the version
    this tree builds: the spec in `main` describes the *next* release, not the last one.
  * Otherwise the newest `## [x.y.z]` heading is it.

All three artifacts are stamped and re-read (`docs.go`'s `SwaggerInfo.Version`,
`swagger.json`'s `info.version`, `swagger.yaml`'s `info.version`). `s25` then catches
"CHANGELOG bumped, `make swagger` not rerun" by construction, because the fresh generation
derives the same number from the same file.

Usage:
  python scripts/release-gate/openapi_stamp.py <dir>            # rewrite in place
  python scripts/release-gate/openapi_stamp.py --check <dir>    # verify only
  python scripts/release-gate/openapi_stamp.py --selftest       # prove each refusal fires

Exit codes: 0 ok, 1 the artifacts disagree, 2 could not look (no version in CHANGELOG, a
missing artifact). Exit 2 is never a pass — "I could not tell" is not "it is fine".
"""
import io
import os
import re
import shutil
import sys
import tempfile

CHANGELOG = "CHANGELOG.md"
FILES = ("docs.go", "swagger.json", "swagger.yaml")

RE_UNRELEASED = re.compile(r"^##\s*\[Unreleased\]\s*$")
RE_ANNOUNCED = re.compile(r"\*\*v(\d+\.\d+\.\d+)")
RE_RELEASED = re.compile(r"^##\s*\[(\d+\.\d+\.\d+)\]")


def target_version(text):
    """(version, why) — or (None, reason) when nothing could be found."""
    lines = text.split("\n")
    unreleased = None
    for i, line in enumerate(lines):
        if RE_UNRELEASED.match(line.strip()):
            unreleased = i
            break
    if unreleased is not None:
        for line in lines[unreleased + 1:]:
            if line.startswith("## "):
                break
            m = RE_ANNOUNCED.search(line)
            if m:
                return m.group(1), "the `## [Unreleased]` announcement (**v%s**)" % m.group(1)
    for line in lines:
        m = RE_RELEASED.match(line.strip())
        if m:
            return m.group(1), "the newest `## [%s]` heading" % m.group(1)
    return None, ("neither a `**vX.Y.Z` announcement under `## [Unreleased]` nor a "
                  "`## [X.Y.Z]` heading in %s" % CHANGELOG)


def released_version(text):
    for line in text.split("\n"):
        m = RE_RELEASED.match(line.strip())
        if m:
            return m.group(1)
    return None


def stamp_json(text, version, path):
    if not re.search(r'"info"\s*:\s*\{', text):
        raise ValueError("%s has no info object" % path)
    out, n = re.subn(r'("info"\s*:\s*\{[^{}]*?"version"\s*:\s*")[^"]*(")',
                     lambda m: m.group(1) + version + m.group(2), text, count=1, flags=re.S)
    if n != 1:
        raise ValueError("%s: info.version not rewritten (count=%d)" % (path, n))
    return out


def stamp_yaml(text, version, path):
    out, n = re.subn(r"(?m)^(\s*version:\s*).*$", lambda m: m.group(1) + version, text, count=1)
    if n != 1:
        raise ValueError("%s: version not rewritten (count=%d)" % (path, n))
    return out


def stamp_go(text, version, path):
    out, n = re.subn(r'(?m)^(\tVersion:\s+)"[^"]*"(,?)$',
                     lambda m: m.group(1) + '"' + version + '"' + m.group(2), text, count=1)
    if n != 1:
        raise ValueError("%s: SwaggerInfo.Version not rewritten (count=%d)" % (path, n))
    return out


STAMPERS = {"docs.go": stamp_go, "swagger.json": stamp_json, "swagger.yaml": stamp_yaml}


def apply(changelog_dir, root, mode):
    """changelog_dir holds CHANGELOG.md; root holds the three artifacts (same dir in a repo)."""
    path = os.path.join(changelog_dir, CHANGELOG)
    try:
        text = io.open(path, encoding="utf-8").read()
    except OSError as e:
        sys.stderr.write("FAIL  cannot read %s (%s)\n" % (path, e))
        return 2
    version, why = target_version(text)
    if not version:
        sys.stderr.write("FAIL  no version to stamp: %s\n" % why)
        return 2
    released = released_version(text)
    if released and version != released and version < released:
        sys.stderr.write("FAIL  the announced version v%s is older than the newest released v%s\n"
                         % (version, released))
        return 2

    problems = []
    for name in FILES:
        art = os.path.join(root, name)
        if not os.path.isfile(art):
            sys.stderr.write("FAIL  %s is missing — nothing to stamp\n" % art)
            return 2
        body = io.open(art, encoding="utf-8", newline="").read()
        try:
            out = STAMPERS[name](body, version, art)
        except ValueError as e:
            sys.stderr.write("FAIL  %s\n" % e)
            problems.append(name)
            continue
        if out != body:
            if mode == "stamp":
                io.open(art, "w", encoding="utf-8", newline="").write(out)
            else:
                problems.append("%s still carries another version" % name)
    if problems:
        sys.stderr.write("FAIL  the artifacts do not carry v%s (%s)\n" % (version, "; ".join(problems)))
        return 1
    print("info.version %s  <- %s%s" % (version, why, "" if mode == "stamp" else "  (verified)"))
    return 0


def selftest():
    """Every case is a refusal or an idempotence claim this file would otherwise only assert."""
    cases = []
    work = tempfile.mkdtemp(prefix="oapi-stamp-")

    def stage(changelog, files=None):
        d = os.path.join(work, "case%d" % len(cases))
        os.makedirs(d)
        io.open(os.path.join(d, CHANGELOG), "w", encoding="utf-8", newline="").write(changelog)
        for name, body in (files or {}).items():
            io.open(os.path.join(d, name), "w", encoding="utf-8", newline="").write(body)
        return d

    def read(d, name):
        return io.open(os.path.join(d, name), encoding="utf-8").read()

    good = {
        "docs.go": 'var SwaggerInfo = &swag.Spec{\n\tVersion:          "1.0.0",\n\tTitle: "x",\n}\n',
        "swagger.json": '{"info": {"title": "x", "version": "1.0.0"}, "paths": {}}',
        "swagger.yaml": "info:\n  title: x\n  version: 1.0.0\npaths: {}\n",
    }
    announced = "# Changelog\n\n## [Unreleased]\n\n> **v0.7.0「可信证据」**\n\n## [0.6.5]\n\n- x\n"

    d = stage(announced, good)
    ok = apply(d, d, "stamp") == 0
    cases.append(("the announced version lands in all three artifacts",
                  ok and '"0.7.0"' in read(d, "swagger.json")
                  and "version: 0.7.0" in read(d, "swagger.yaml")
                  and 'Version:          "0.7.0"' in read(d, "docs.go")))
    before = tuple(read(d, n) for n in FILES)
    apply(d, d, "stamp")
    cases.append(("a second stamp is a no-op", tuple(read(d, n) for n in FILES) == before))

    d = stage(announced, good)
    cases.append(("--check on unstamped artifacts is refused (1)", apply(d, d, "check") == 1))

    d = stage(announced, dict(good, **{"swagger.json": '{"info": {"title": "x", "version": "0.7.0"}}'}))
    cases.append(("--check refuses when only the JSON agrees (yaml and go must too)",
                  apply(d, d, "check") == 1))

    d = stage("# Changelog\n\n## [0.6.5]\n\n- x\n", good)
    ok = apply(d, d, "stamp") == 0
    cases.append(("with no announcement the newest release heading is used",
                  ok and '"0.6.5"' in read(d, "swagger.json")))

    d = stage("# Changelog\n\n## [Unreleased]\n\n> **v0.5.0**\n\n## [0.6.5]\n", good)
    cases.append(("an announced version older than the newest release is exit 2",
                  apply(d, d, "stamp") == 2))

    d = stage("# Changelog\n\n- nothing numbered here\n", good)
    cases.append(("no version anywhere is exit 2, never a pass", apply(d, d, "stamp") == 2))

    d = stage(announced, {"docs.go": good["docs.go"]})
    cases.append(("a missing artifact is exit 2", apply(d, d, "stamp") == 2))

    d = stage(announced, dict(good, **{"swagger.json": '{"paths": {}}'}))
    cases.append(("a JSON without an info object is refused (1)", apply(d, d, "stamp") == 1))

    d = stage(announced, dict(good, **{"docs.go": 'package docs\n\nvar X = 1\n'}))
    cases.append(("a docs.go without SwaggerInfo.Version is refused (1)", apply(d, d, "stamp") == 1))

    shutil.rmtree(work, ignore_errors=True)
    bad = [name for name, ok in cases if not ok]
    for name, ok in cases:
        print("%s  %s" % ("PASS" if ok else "FAIL", name))
    print("selftest: %s (%d cases)" % ("OK" if not bad else "FAIL", len(cases)))
    return 0 if not bad else 1


def main(argv):
    mode, rest = "stamp", []
    for a in argv:
        if a == "--check":
            mode = "check"
        elif a == "--selftest":
            mode = "selftest"
        else:
            rest.append(a)
    if mode == "selftest":
        return selftest()
    root = rest[0] if rest else "internal/docs"
    repo = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    return apply(repo, root, mode)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
