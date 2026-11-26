#!/usr/bin/env bash
# s32.release-upgrade-rollback — the write-path half of "can we roll back to the
# previous release".
#
# s06 refuses DDL that *removes* a table or column. It cannot see the mirror image:
# a new column the *old* binary cannot fill in. GORM's AutoMigrate will happily add
# `not null` without a default, and then every insert from the previous release fails
# — on the rollback target, after the fact.
#
# doc/develop/313 §7 C8 requires "升级与回滚验证" before v0.7.0 GA. This is the part
# of it a machine can judge offline. The other half — the previous image starting on
# the new schema and serving a request — needs a published image and is the drill
# tracked in doc/304 §7.
#
# usage: migration-window.sh [BASE_REF]   (empty BASE => unverified, exit 3)
#        migration-window.sh --selftest
set -uo pipefail
export PYTHONIOENCODING=utf-8

root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not a git repo" >&2; exit 3; }
cd "$root" || exit 3

if [ "${1:-}" = "--selftest" ]; then
  exec python scripts/release-gate/migration_window.py --selftest
fi

base="${1:-${BASE:-}}"
if [ -z "$base" ] || [ "$base" = "none" ]; then
  echo "migration-window: UNVERIFIED — no base ref, so \"new\" has no meaning" >&2
  exit 3
fi

exec python scripts/release-gate/migration_window.py --base "$base"
