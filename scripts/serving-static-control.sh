#!/bin/sh
# scripts/serving-static-control.sh - the negative control for check 5 of the
# serving contract, the Data Dragon projection under /agg/v1/static/.
#
# Check 5 used to report an unpublished projection as a WARN and exit 0, so a
# contract that described a prefix the deployed Service does not serve outlived
# the served reality and the gate waved it through. It now asserts the two states
# the contract allows - published: 200 at exactly public, max-age=3600;
# unpublished: 404 with Cache-Control: no-store - and fails on anything else
# (docs/contracts.md section 4, the static-projection amendment).
#
# An assertion is only worth what its failure direction is worth, so this script
# stands in a deliberately bad origin for the tier and requires check 5 to fail
# on it, twice:
#
#   1. a served projection with no cache policy at all: a cache would keep
#      immutable-looking bytes past the version that produced them;
#   2. a 404 for the reserved prefix with no cache policy either: a shared cache
#      would keep the miss and answer it after the projection is published.
#
# The origin is python3's own file server, so the responses are real HTTP and
# nothing here is a stub of the gate itself. It is not a tier: every other check
# in the gate fails against it too, which is why each control asserts the
# *specific* line check 5 has to print, and reports the gate's exit status
# alongside it rather than as the whole of the evidence.
#
# The positive direction is the other half of the control and it runs against the
# real tier in `make verify-serving-local`: once over the fixture tree, which does
# publish the projection (200 and public, max-age=3600), and once over a copy with
# v1/static removed (404 and no-store). A rule that only ever fails is not a
# control either.
#
# There is no skip: if the origin cannot be created the control fails, because a
# control that quietly does nothing when its precondition is missing is worse
# than no control at all.
#
# Needs sh, python3 and curl. No cluster, no container, no network.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GATE="$ROOT/scripts/verify-serving.sh"
PORT=${LOLSTATS_STATIC_CONTROL_PORT:-18095}
WORK="$ROOT/.agent-artifacts/serving-static-control.$$"
SERVE="$WORK/serve"
VERSION='16.18.1'
PROJECTION="/agg/v1/static/$VERSION/champions.json"
REL="agg/v1/static/$VERSION/champions.json"
BASE="http://127.0.0.1:$PORT"

controls=0
broken=0
good() { controls=$((controls + 1)); printf 'PASS  %s\n' "$1"; }
bad() { broken=$((broken + 1)); printf 'FAIL  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }

if ! command -v python3 >/dev/null 2>&1; then
	printf 'FAIL  python3 is not installed, so this control cannot stand in the bad origin it needs.\n'
	printf '      This is a failure and not a skip: without it the failure direction of check 5 is\n'
	printf '      untested, and the check is the one that used to wave an unserved prefix through.\n'
	exit 1
fi
if [ ! -f "$GATE" ]; then
	printf 'FAIL  %s is not present, so there is nothing to control.\n' "$GATE"
	exit 1
fi

rm -rf "$WORK"
mkdir -p "$SERVE/$(dirname -- "$REL")" || { printf 'FAIL  could not create %s\n' "$SERVE"; exit 1; }
printf '{"champions": [], "control": "serving-static-control"}\n' > "$SERVE/$REL" || exit 1

python3 -m http.server "$PORT" --bind 127.0.0.1 --directory "$SERVE" >"$WORK/origin.log" 2>&1 &
origin=$!
cleanup() { kill "$origin" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT INT TERM

i=0
while [ "$i" -lt 40 ]; do
	if curl -sS -m 5 -o /dev/null "$BASE/" 2>/dev/null; then break; fi
	kill -0 "$origin" 2>/dev/null || break
	i=$((i + 1))
	sleep 0.25
done
if ! curl -sS -m 5 -o /dev/null "$BASE/" 2>/dev/null; then
	printf 'FAIL  the control origin did not answer on %s; see %s\n' "$BASE" "$WORK/origin.log"
	exit 1
fi

printf 'standing in a deliberately bad origin for the tier: %s (python3 http.server over %s)\n' "$BASE" "$SERVE"

# --- control 1: the projection is served, with no cache policy and no length ---
printf '\n-- control 1: a served projection with no cache policy at all\n'
sh "$GATE" "$BASE" > "$WORK/served-without-policy.log" 2>&1
rc=$?
note "scripts/verify-serving.sh exit $rc"
expected="Cache-Control is 'absent', expected 'public, max-age=3600'"
if grep -qF "$PROJECTION: $expected" "$WORK/served-without-policy.log"; then
	good "check 5 fails on a served projection with no cache policy: $(grep -m1 -F "$PROJECTION:" "$WORK/served-without-policy.log")"
else
	bad "check 5 did not report '$expected' for the served projection, so it does not read the cache policy of a served 200"
	grep -F "$PROJECTION:" "$WORK/served-without-policy.log" | head -3 || note "no line for $PROJECTION at all"
fi
if [ "$rc" -eq 0 ]; then
	bad "the gate exited 0 against an origin serving the projection with no cache policy"
else
	good "the gate exits non-zero against that origin (exit $rc)"
fi

# --- control 2: the prefix 404s, and the miss is cacheable --------------------
printf '\n-- control 2: a 404 for the reserved prefix with no cache policy either\n'
rm -f "$SERVE/$REL"
sh "$GATE" "$BASE" > "$WORK/absent-without-policy.log" 2>&1
rc=$?
note "scripts/verify-serving.sh exit $rc"
expected="HTTP 404 with Cache-Control 'absent'"
if grep -qF "$PROJECTION: $expected" "$WORK/absent-without-policy.log"; then
	good "check 5 fails on a cacheable 404 under the reserved prefix: $(grep -m1 -F "$PROJECTION:" "$WORK/absent-without-policy.log")"
else
	bad "check 5 did not report '$expected', so it accepts a 404 whose miss a shared cache may keep"
	grep -F "$PROJECTION:" "$WORK/absent-without-policy.log" | head -3 || note "no line for $PROJECTION at all"
fi
if [ "$rc" -eq 0 ]; then
	bad "the gate exited 0 against an origin whose 404 for the reserved prefix is cacheable"
else
	good "the gate exits non-zero against that origin (exit $rc)"
fi

# --- control 3: the direction the contract allows is not a failure ------------
printf '\n-- control 3: the same origin with the projection absent and the gate is not asked to pass it\n'
if grep -qF 'PASS  no Data Dragon projection is published' "$WORK/absent-without-policy.log"; then
	bad "check 5 passed the absence verdict for an origin whose 404 was cacheable, so the verdict follows the status and not the policy it is supposed to assert"
else
	good "the absence verdict is not granted to a cacheable 404, so the pass depends on the served policy and not on the status alone"
fi

printf '\n-- result --\n'
printf 'the positive direction of the same check runs against the real tier in `make verify-serving-local`\n'
printf '  state 1 (fixture tree, projection published):      PASS 200 + public, max-age=3600\n'
printf '  state 2 (fixture tree with v1/static removed):     PASS 404 + no-store\n'
printf 'serving-static-control: %s control(s) held, %s broken\n' "$controls" "$broken"
if [ "$broken" -ne 0 ]; then
	exit 1
fi
printf 'ok: check 5 fails on a served projection with no policy and on a cacheable 404, and passes on both contract states\n'
