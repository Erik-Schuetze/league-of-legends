#!/usr/bin/env bash
# Measure the Go tier against the plan.md 7.4 Lighthouse budget.
#
#   scripts/perf/lighthouse-routes.sh --round 1 --out docs/evidence
#
# Writes one raw Lighthouse JSON report per route per round. Nothing here is
# installed into package.json on purpose: Lighthouse lives in a scratch tools
# directory (PERF_TOOLS_DIR, default /tmp/a11y-tools).
#
#   mkdir -p /tmp/a11y-tools && npm --prefix /tmp/a11y-tools install lighthouse
#
# The origin is the in-cluster Go tier reached through a port-forward, which must
# outlive this script's shell:
#
#   kubectl -n lolstats port-forward svc/lolstats-go-web 18921:80
#
# ...or the live edge itself once the cutover has happened:
#
#   scripts/perf/lighthouse-routes.sh --round 9 --base https://lol.erik-schuetze.dev \
#     --auth "$EDGE_AUTH"
#
# A dead port-forward and a missing route both look like a connection failure, so
# every run starts with a positive control on /.
#
# --auth USER:PASS passes the edge's basic-auth gate. Lighthouse is handed the
# credential inside the URL, which Chrome needs because the gate covers the
# subresources as well as the document; Lighthouse masks it in its own url fields
# but NOT everywhere in the JSON, so the credential is stripped from every report
# before it is written (see "redaction" below). Reports never carry a secret.
set -euo pipefail

BASE_URL="http://127.0.0.1:18921"
OUT_DIR="docs/evidence"
ROUND="1"
AUTH=""
PERF_TOOLS_DIR="${PERF_TOOLS_DIR:-/tmp/a11y-tools}"
CHROME_PATH="${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"

# Route set: the brief's mandated five, plus a second champion route in a
# different role, plus a second tier list and matchup role.
ROUTES=(
  "/"
  "/tier-list/mid/"
  "/tier-list/top/"
  "/champions/ahri/mid/"
  "/champions/ahri/top/"
  "/matchups/mid/"
  "/matchups/top/"
  "/about/"
)

while [[ $# -gt 0 ]]; do
  case "$1" in
    --round) ROUND="$2"; shift 2 ;;
    --base) BASE_URL="$2"; shift 2 ;;
    --auth) AUTH="$2"; shift 2 ;;
    --out) OUT_DIR="$2"; shift 2 ;;
    --tools) PERF_TOOLS_DIR="$2"; shift 2 ;;
    --only) ROUTES=("$2"); shift 2 ;;
    --routes) IFS=' ' read -r -a ROUTES <<< "$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

LH="$PERF_TOOLS_DIR/node_modules/.bin/lighthouse"
[[ -x "$LH" || -f "$LH" ]] || { echo "lighthouse not found at $LH; set PERF_TOOLS_DIR" >&2; exit 2; }
[[ -n "${CHROME_PATH:-}" && -x "$CHROME_PATH" ]] || { echo "Chrome not found at CHROME_PATH=$CHROME_PATH" >&2; exit 2; }

CURL_AUTH=()
LH_BASE="$BASE_URL"
if [[ -n "$AUTH" ]]; then
  CURL_AUTH=(-u "$AUTH")
  LH_BASE="$(printf '%s' "$BASE_URL" | sed -E "s#^(https?://)#\1${AUTH}@#")"
fi

# The credential rides inside the URL Chrome is handed. Lighthouse masks it in the
# url fields it records, but not in every string in the report, so strip it before
# the report is read by anything else: no report may carry a secret.
redact() {
  [[ -n "$AUTH" ]] || return 0
  AUTH="$AUTH" node -e '
    const fs = require("node:fs");
    const file = process.argv[1];
    const [user, ...rest] = process.env.AUTH.split(":");
    const pass = rest.join(":");
    let s = fs.readFileSync(file, "utf8");
    const needles = [process.env.AUTH, `${user}:${encodeURIComponent(pass)}`, encodeURIComponent(pass), pass];
    for (const n of needles) if (n) s = s.split(n).join("REDACTED");
    fs.writeFileSync(file, s);
  ' "$1"
}

echo "== positive control =="
control=$(curl -s ${CURL_AUTH[@]+"${CURL_AUTH[@]}"} -o /dev/null -w '%{http_code}' "$BASE_URL/")
echo "GET $BASE_URL/ -> $control"
if [[ "$control" != "200" ]]; then
  echo "FATAL: origin is not serving; refusing to record scores that would be indistinguishable from a dead port-forward" >&2
  exit 1
fi
echo "posture: $(curl -s ${CURL_AUTH[@]+"${CURL_AUTH[@]}"} "$BASE_URL/" | grep -o 'data-state="[^"]*"' | head -1)"

mkdir -p "$OUT_DIR"

for route in "${ROUTES[@]}"; do
  slug=$(echo "$route" | sed -e 's|^/||' -e 's|/$||' -e 's|/|-|g')
  [[ -z "$slug" ]] && slug="home"
  out="$OUT_DIR/lh-r${ROUND}-${slug}.json"
  code=$(curl -s ${CURL_AUTH[@]+"${CURL_AUTH[@]}"} -o /dev/null -w '%{http_code}' "$BASE_URL$route")
  if [[ "$code" != "200" ]]; then
    echo "SKIP $route -> $code (route not served; not scored)"
    continue
  fi
  echo "--- round $ROUND $route ---"
  CHROME_PATH="$CHROME_PATH" "$LH" "$LH_BASE$route" \
    --output=json --output-path="$out" \
    --only-categories=performance,accessibility,best-practices,seo \
    --chrome-flags="--headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage" \
    --quiet --max-wait-for-load=45000
  redact "$out"
  echo "wrote $out"
done

echo "== extraction =="
node "$(dirname "$0")/extract-lh.mjs" "$OUT_DIR"/lh-r${ROUND}-*.json
