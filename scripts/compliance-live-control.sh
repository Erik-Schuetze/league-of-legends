#!/bin/sh
# The control for the half of check 11 that CI never ran.
#
# Check 11 scans the served corpus twice: every page that declares a state must
# carry the banner that state promises. The corpus the gate scans in CI is a
# capture of the checked-in fixture tree, whose manifest declares a demo source,
# so every page in it is `demo` and the `live` half of the scan sees an empty
# list. That is exactly the shape that produced the defect this control exists
# for: `xargs -0 grep -L` with an empty list still runs grep, grep then reads its
# own standard input, and GitHub's runner reported a phantom page named
# "(standard input)" as a live page with no banner. CI went red on a tree that
# passes everywhere else.
#
# So it is not enough for the gate to pass in the posture CI captures. This
# control renders a *live* posture from the same checked-in fixtures (nothing
# here contacts the cluster, the network or a package manager - it writes one
# field of a copy of fixtures/site/v1 into bin/, starts bin/lolstats-web on
# loopback, and reads that tier's own pages), and then asserts both directions:
#
#   1. the live corpus passes, and the gate says out loud that it scanned live
#      pages - a run that silently scanned 0 live pages proves nothing;
#   2. one live page with its live banner removed makes the gate fail, naming a
#      real file path, and specifically NOT "(standard input)".
#
# Every precondition is fail-closed. If the tier does not come up, does not
# render a live state, or the capture has no live page with a banner to remove,
# this script fails and says which one it was: a control that quietly reduces to
# "the gate passed" when its precondition is missing is the defect, not the fix.
#
#   LOLSTATS_LIVE_CONTROL_PORT  loopback port for the ephemeral tier (default 18095)
#
# Needs sh, curl, sed, grep and a built bin/lolstats-web (`make build`).

set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PORT=${LOLSTATS_LIVE_CONTROL_PORT:-18095}
WORK="$ROOT/bin/compliance-live-control"
AGG="$WORK/agg"
CAPTURE="$WORK/captured"
BROKEN="$WORK/captured-broken"
LOG="$WORK/gate.log"
TIER_LOG="$WORK/tier.log"

controls=0
broken=0
good() { controls=$((controls + 1)); printf 'PASS  %s\n' "$1"; }
bad() { broken=$((broken + 1)); printf 'FAIL  %s\n' "$1"; }

pid=""
cleanup() {
	if [ -n "$pid" ]; then kill "$pid" 2>/dev/null; fi
}
trap cleanup EXIT INT TERM

if [ ! -x "$ROOT/bin/lolstats-web" ]; then
	echo "FAIL: bin/lolstats-web is not built, so the live posture cannot be rendered." >&2
	echo "      Run 'make build' first; this control refuses to report a verdict it did not reach." >&2
	exit 1
fi

rm -rf "$WORK"
mkdir -p "$AGG" "$CAPTURE"

# ---------------------------------------------------------------------------
printf '\nsetting up: a live posture rendered from the checked-in fixtures\n'
cp -R "$ROOT/fixtures/site/v1" "$AGG/v1" || exit 1
manifest="$AGG/v1/manifest.json"
if [ ! -f "$manifest" ]; then
	echo "FAIL: $manifest does not exist, so there is no artifact contract to rewrite." >&2
	exit 1
fi
# The one field the posture turns on. A copy, so the checked-in fixture is
# untouched and the gate's own demo capture is unaffected.
sed 's/"source"[ ]*:[ ]*"[^"]*"/"source": "riot-match-v5"/' "$manifest" > "$manifest.new" || exit 1
if ! grep -qF '"source": "riot-match-v5"' "$manifest.new"; then
	echo "FAIL: the manifest's source field could not be rewritten, so the tier would stay in the demo" >&2
	echo "      posture and every control below would be vacuous. $manifest:" >&2
	sed 's/^/      /' "$manifest" >&2
	exit 1
fi
mv "$manifest.new" "$manifest"
printf 'ok: fixture manifest copied to %s with source rewritten to riot-match-v5\n' "${manifest#"$ROOT"/}"

( export LOLSTATS_AGG_ROOT="$AGG"; \
  export LOLSTATS_WEB_ADDR="127.0.0.1:$PORT"; \
  [ -n "${LOLSTATS_SITE_URL:-}" ] && export LOLSTATS_SITE_URL; \
  exec "$ROOT/bin/lolstats-web" ) >"$TIER_LOG" 2>&1 &
pid=$!
i=0
while [ "$i" -lt 40 ]; do
	if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then break; fi
	kill -0 "$pid" 2>/dev/null || break
	i=$((i + 1))
	sleep 0.5
done
if ! curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
	echo "FAIL: the tier did not answer /healthz on 127.0.0.1:$PORT, so no live page could be captured." >&2
	sed 's/^/      /' "$TIER_LOG" >&2
	exit 1
fi

# Fail closed on the precondition itself: a tier that is not in the live posture
# would make both controls below pass for the wrong reason.
root_state=$(curl -fsS "http://127.0.0.1:$PORT/" | grep -o 'data-state="[a-z-]*"' | head -1)
if [ "$root_state" != 'data-state="live"' ]; then
	echo "FAIL: the tier answered / with ${root_state:-no data-state at all}, not data-state=\"live\"," >&2
	echo "      so the live-state half of check 11 would still not run and this control proves nothing." >&2
	exit 1
fi
printf 'ok: the ephemeral tier on 127.0.0.1:%s renders %s\n' "$PORT" "$root_state"

LOLSTATS_SERVE_URL="http://127.0.0.1:$PORT" \
	LOLSTATS_SERVED_DIST="$CAPTURE" \
	sh "$ROOT/scripts/capture-served-pages.sh" || exit 1

live_pages=$(grep -rl 'data-state="live"' "$CAPTURE" --include='*.html' 2>/dev/null | wc -l | tr -d ' ')
if [ "$live_pages" -lt 1 ]; then
	echo "FAIL: the capture carries 0 pages in the live state, so check 11 has nothing to scan." >&2
	exit 1
fi

run_gate() {
	LOLSTATS_DIST="$1" sh "$ROOT/scripts/compliance-check.sh" >"$LOG" 2>&1
	return $?
}

# ---------------------------------------------------------------------------
printf '\ncontrol 1: the live corpus passes, and the gate says it scanned live pages\n'
if run_gate "$CAPTURE"; then
	scan_line=$(grep -m1 'for a final </html> and for the banner their state declares' "$LOG")
	printf '      %s\n' "$scan_line"
	# The scan line is the only place the gate reports the population it actually
	# examined, so it is parsed rather than trusted: a future change that made the
	# gate scan 0 live pages while still passing must break this control.
	counts=$(printf '%s\n' "$scan_line" | sed -n 's/.*: \([0-9][0-9]*\) demo, \([0-9][0-9]*\) live, \([0-9][0-9]*\) no-data.*/\1 \2 \3/p')
	demo=$(printf '%s\n' "$counts" | cut -d' ' -f1)
	live=$(printf '%s\n' "$counts" | cut -d' ' -f2)
	no_data=$(printf '%s\n' "$counts" | cut -d' ' -f3)
	if [ -z "$counts" ]; then
		bad 'the gate passed but did not report the state populations it scanned, so this control cannot tell whether the live scan ran'
	elif [ "$demo" != 0 ] || [ "$no_data" != 0 ] || [ "$live" != "$live_pages" ]; then
		bad "the gate scanned $demo demo, $live live, $no_data no-data page(s) over a capture of $live_pages live page(s); the corpus is not the posture this control built"
	elif [ "$live" -eq 0 ]; then
		bad 'the gate passed over 0 live pages, which is the vacuous shape the phantom page came from'
	else
		good "the gate passes the live corpus and scanned $live live page(s) to say so"
	fi
else
	bad 'the gate failed over the intact live corpus, which is a defect in the gate or the tier and not a missing banner:'
	grep -m5 '^FAIL' "$LOG" | sed 's/^/      /'
fi

# ---------------------------------------------------------------------------
printf '\ncontrol 2: one live page stripped of its live banner fails, by real path\n'
rm -rf "$BROKEN"
cp -R "$CAPTURE" "$BROKEN" || exit 1
target=$(grep -rl 'state-banner--live' "$BROKEN" --include='*.html' 2>/dev/null | head -1)
if [ -z "$target" ]; then
	echo "FAIL: no page in the capture carries state-banner--live, so the banner could not be removed" >&2
	echo "      and this control would have passed without ever exercising the scan." >&2
	exit 1
fi
rel=${target#"$BROKEN"/}
sed 's/state-banner--live/state-banner-not-live/' "$target" > "$target.new" || exit 1
if grep -qF 'state-banner--live' "$target.new"; then
	echo "FAIL: the live banner was not removed from $rel, so the plant did not land." >&2
	exit 1
fi
mv "$target.new" "$target"
printf 'ok: removed the live banner from %s (data-state="live" left in place)\n' "$rel"

if run_gate "$BROKEN"; then
	bad "the gate passed a corpus whose page $rel declares data-state=\"live\" and carries no live banner"
else
	printf '      %s\n' "$(grep -m1 '^FAIL' "$LOG")"
	if ! grep -qF 'live page(s) carry no live banner' "$LOG"; then
		bad 'the gate failed, but not for the live banner:'
		grep -m5 '^FAIL' "$LOG" | sed 's/^/      /'
	elif grep -qF '(standard input)' "$LOG"; then
		bad 'the gate reported "(standard input)" as the offending page: the scan read the runner'"'"'s stdin rather than the corpus, which is the defect this control exists for'
	elif ! grep -qF "$rel" "$LOG"; then
		bad "the gate failed on the live banner but did not name $rel, so the reported page is not the planted one:"
		grep -m5 '^FAIL' "$LOG" | sed 's/^/      /'
	else
		good "a live page with no live banner fails the gate by its real path ($rel), never as a pseudo-file"
	fi
fi

# ---------------------------------------------------------------------------
printf '\n--- summary ---\n'
if [ "$broken" -eq 0 ]; then
	printf 'RESULT: PASS - %s live-posture control(s) held and 0 broken\n' "$controls"
	exit 0
fi
printf 'RESULT: FAIL - %s live-posture control(s) broken of %s\n' "$broken" "$((controls + broken))"
exit 1
