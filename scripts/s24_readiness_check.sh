#!/usr/bin/env bash
# S24 readiness check (doc/41 §19, doc/125 §6). Read-only: the script never
# mutates state. It verifies the prerequisites for the S24 real-machine demo
# (scenes A–D) and prints a PASS/FAIL checklist with remediation hints.
#
# Usage: bash scripts/s24_readiness_check.sh
set -u

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

PASS_COUNT=0
FAIL_COUNT=0

pass() { printf '  [PASS] %s\n' "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { printf '  [FAIL] %s\n' "$1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
hint() { printf '         -> %s\n' "$1"; }

printf 'S24 readiness check (%s)\n\n' "$PROJECT_ROOT"

# 1. PDF font: RGX_EXPORT_PDF_FONT set, or one of the pdfFontPath candidates.
printf '1. PDF export font (scene C)\n'
if [ -n "${RGX_EXPORT_PDF_FONT:-}" ]; then
  if [ -f "$RGX_EXPORT_PDF_FONT" ]; then
    pass "RGX_EXPORT_PDF_FONT is set and exists: $RGX_EXPORT_PDF_FONT"
  else
    fail "RGX_EXPORT_PDF_FONT is set but the file does not exist: $RGX_EXPORT_PDF_FONT"
    hint "Point RGX_EXPORT_PDF_FONT at an existing CJK TTF/TTC font."
  fi
else
  font_found=""
  for candidate in \
    "/c/Windows/Fonts/Deng.ttf" \
    "/c/Windows/Fonts/msyh.ttc" \
    "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc" \
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
  do
    if [ -f "$candidate" ]; then
      font_found="$candidate"
      break
    fi
  done
  if [ -n "$font_found" ]; then
    pass "Candidate CJK font found: $font_found"
  else
    fail "No CJK font found in pdfFontPath candidates"
    hint "Set RGX_EXPORT_PDF_FONT to a CJK TTF/TTC font (PDF export returns 40132 without it)."
  fi
fi
printf '\n'

# 2. RAGFlow base URL reachable (scene A/D depend on the provider).
printf '2. RAGFlow provider reachability\n'
ragflow_base="${RGX_RAGFLOW_BASE_URL:-http://192.168.4.151}"
if command -v curl >/dev/null 2>&1; then
  if curl -sk -o /dev/null -m 3 -w '' "$ragflow_base"; then
    curl_status="$(curl -sk -o /dev/null -m 3 -w '%{http_code}' "$ragflow_base" 2>/dev/null || echo 000)"
    if [ "$curl_status" != "000" ]; then
      pass "RAGFlow base URL responded (HTTP $curl_status): $ragflow_base"
    else
      fail "RAGFlow base URL did not respond within 3s: $ragflow_base"
      hint "Start the RAGFlow host or update RGX_RAGFLOW_BASE_URL before running scenes A-D."
    fi
  else
    fail "curl could not connect to $ragflow_base"
    hint "Verify network/VPN access to the RAGFlow host."
  fi
else
  fail "curl is not available; cannot probe $ragflow_base"
  hint "Install curl or probe the host manually."
fi
printf '\n'

# 3. Backend binary freshness vs the latest commit.
printf '3. Backend build freshness\n'
binary=""
for candidate in bin/ragflow-x.exe bin/ragflow-x; do
  if [ -f "$candidate" ]; then
    binary="$candidate"
    break
  fi
done
if [ -z "$binary" ]; then
  fail "No backend binary found (bin/ragflow-x[.exe])"
  hint "Run: go build -o bin/ragflow-x ./cmd/server (or your usual build command)."
else
  head_time=""
  if command -v git >/dev/null 2>&1; then
    head_time="$(git log -1 --format=%ct 2>/dev/null || true)"
  fi
  if [ -n "$head_time" ] && [ "$head_time" -eq "$head_time" ] 2>/dev/null; then
    bin_time="$(date -r "$binary" +%s 2>/dev/null || echo 0)"
    if [ "$bin_time" -ge "$head_time" ]; then
      pass "$binary is newer than the latest commit"
    else
      fail "$binary is older than the latest commit; rebuild before the demo"
      hint "Run: go build -o bin/ragflow-x ./cmd/server"
    fi
  else
    pass "$binary exists (git unavailable; freshness not checked)"
  fi
fi
printf '\n'

# 4. Dataset corpus: doc/41 §1 five test directories under test/.
printf '4. Dataset corpus (doc/41 §1 five categories)\n'
missing=0
for dir in \
  "test/互检问题-docs" \
  "test/互检问题2-docs" \
  "test/安全生产政策法规-docs" \
  "test/安全生产风险点防控统计表" \
  "test/隐患检查-docs"
do
  if [ -d "$dir" ]; then
    pass "corpus present: $dir"
  else
    fail "corpus missing: $dir"
    missing=1
  fi
done
if [ "$missing" -ne 0 ]; then
  hint "Restore the corpus directories or adjust the demo dataset mapping."
fi
printf '\n'

printf 'Summary: %d passed, %d failed\n' "$PASS_COUNT" "$FAIL_COUNT"
if [ "$FAIL_COUNT" -gt 0 ]; then
  printf 'Resolve the FAIL items above, then re-run this script before the S24 demo.\n'
  exit 1
fi
printf 'All readiness checks passed; scenes A-D can be executed (doc/41 §19).\n'
