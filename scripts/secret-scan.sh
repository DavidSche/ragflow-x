#!/usr/bin/env bash
# secret-scan.sh -- secret & local-environment leakage guard.
#
# Scans changed files (pre-commit, default) or every tracked file (CI, --all)
# for credential material and local-environment fingerprints that must never
# be committed. Exits 1 (hard block) on real-credential patterns or red-line
# paths; prints warnings (does not block) for soft signals such as RFC1918
# addresses or local user paths so developers can judge test placeholders.
#
# Usage:
#   bash scripts/secret-scan.sh           # scan staged changes (pre-commit)
#   bash scripts/secret-scan.sh --all     # scan every tracked file (CI)
#
# Install as a local pre-commit hook:
#   cp scripts/git-hooks/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit
# or set the hooks path once:
#   git config core.hooksPath scripts/git-hooks
set -u

MODE="${1:-staged}"

# Build a temporary file list (newline-separated). A temp file avoids piping
# git output into a consumer that exits early (e.g. `grep -q`), which can
# stall git on some platforms when it keeps writing to a closed pipe.
list_file="$(mktemp)"
if [ "$MODE" = "staged" ]; then
  git diff --cached --name-only --diff-filter=ACM > "$list_file"
else
  git ls-files > "$list_file"
fi

if [ ! -s "$list_file" ]; then
  echo "secret-scan: nothing to scan ($MODE)"
  rm -f "$list_file"
  exit 0
fi

hard_failed=0
soft_count=0

# path-based hard blocks: this project's red-line files must never be staged.
path_redline() {
  case "$1" in
    config/config.drill*.yaml | config/.master.key | config/.master.key.* | \
    config/runtime.secrets.json | config/runtime.secrets.json.* | \
    config/rsa_setup.pem | *.secret)
      echo "HARD BLOCK: red-line secret path staged: $1" >&2
      return 0
      ;;
  esac
  return 1
}

scan_file() {
  local f="$1" out aws
  [ -f "$f" ] || return 0
  [ -n "$f" ] || return 0
  if path_redline "$f"; then hard_failed=1; fi

  # The scanner itself defines the token patterns, so it (and its hook) must
  # not be scanned; and files that explicitly declare synthetic fixtures (fake
  # credentials used only to exercise masking logic) are exempt from the hard
  # credential check -- their content is deliberate.
  case "$f" in
    scripts/secret-scan.sh | scripts/git-hooks/pre-commit) return 0 ;;
    *) ;;
  esac
  if grep -q 'secret-scan-exempt: synthetic fixture only' "$f" 2>/dev/null; then
    return 0
  fi

  # One grep per file (single process; Git Bash forks are expensive), then a
  # single awk classifies every hit as hard-block or soft-warning.
  out="$(grep -nE -- '-----BEGIN (RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----|(^|[^A-Za-z0-9])(sk-[A-Za-z0-9]{24,}|ghp_[A-Za-z0-9]{24,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{20,})([^A-Za-z0-9]|$)|192\.168\.[0-9]+\.[0-9]+|10\.[0-9]+\.[0-9]+\.[0-9]+|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]+\.[0-9]+|/data/[A-Za-z0-9_-]+/|zhixiang|llm2024' "$f" 2>/dev/null)"
  [ -n "$out" ] || return 0

  aws="$(printf '%s\n' "$out" | awk -v fn="$f" '
    /PRIVATE KEY-----/ || /(^|[^A-Za-z0-9])(sk-[A-Za-z0-9]{24,}|ghp_[A-Za-z0-9]{24,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{20,})([^A-Za-z0-9]|$)/ {
      print "HARD BLOCK: credential -> " fn ":" $1; exit 2
    }
    {
      print "WARN: RFC1918 address/local path -> " fn ":" $1
    }
  ' 2>/dev/null)"
  while IFS= read -r m; do
    [ -n "$m" ] || continue
    echo "$m" >&2
    case "$m" in
      HARD*) hard_failed=1 ;;
      *) soft_count=$((soft_count + 1)) ;;
    esac
  done <<< "$aws"
}

while IFS= read -r f; do
  [ -n "$f" ] || continue
  scan_file "$f"
done < "$list_file"
rm -f "$list_file"

if [ "$hard_failed" -ne 0 ]; then
  echo "secret-scan: FAIL (hard blocks found); fix before committing." >&2
  exit 1
fi
if [ "$soft_count" -gt 0 ]; then
  echo "secret-scan: WARNINGS=$soft_count (review before push)." >&2
else
  echo "secret-scan: clean ($MODE)." >&2
fi
exit 0
