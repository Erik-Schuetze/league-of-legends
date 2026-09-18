#!/bin/sh
# scripts/compliance-live-preconditions.sh - the fail-closed direction of
# scripts/compliance-live-control.sh, which `make compliance-live-control`
# exercises in its passing direction.
#
# That control exists for one thing: the live-state half of check 11 was never
# scanned in CI (the captured corpus is all-demo), so a scan that had quietly
# stopped looking at live pages would have been indistinguishable from a scan
# that passed. It refuses to report a verdict unless it can create the condition
# it needs - a built tier and a tier actually rendering `data-state="live"` -
# and this script proves that refusal, because a control that reduces to "the
# gate passed" when its precondition is missing is the very defect it guards.
#
# It does not read the control for the word "fail". It builds a scratch lane root
# - a copy of the control and of the capture helper, a symlink to the checked-in
# fixtures, and a bin/ of its own - and removes one precondition at a time:
#
#   1. no bin/lolstats-web: nothing can be rendered, so the control must fail and
#      name the missing build;
#   2. a tier built, but the posture rewrite pointed at `demo`: every page is in
#      the demo state, the live scan would still see an empty list, so the control
#      must fail and name the state it saw.
#
# The passing direction is not duplicated here: it is `make compliance-live-control`,
# which CI runs, and the same script's control 1 already asserts the gate reports
# the live pages it scanned rather than merely exiting 0.
#
# Fails closed on its own preconditions too: no fixtures, no capture helper or no
# writable scratch root is a failure with a reason, not a skip.
#
# Needs sh, curl, sed, grep, ln, cp, mkdir. No cluster, no network, no build.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/bin/compliance-live-preconditions"
PROBE="$WORK/probe"
LOGB="$WORK/probe-a.log"
LOGC="$WORK/probe-b.log"

controls=0
broken=0
good() { controls=$((controls + 1)); printf 'PASS  %s\n' "$1"; }
bad() { broken=$((broken + 1)); printf 'FAIL  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }

for f in "$ROOT/scripts/compliance-live-control.sh" "$ROOT/scripts/capture-served-pages.sh" \
	"$ROOT/fixtures/site/v1/manifest.json" "$ROOT/bin/lolstats-web"; do
	if [ ! -e "$f" ]; then
		printf 'FAIL  %s does not exist, so the control it belongs to cannot be probed\n' "${f#"$ROOT"/}" >&2
		printf '      (run "make build" for the tier); this script refuses to report a verdict it did not reach.\n' >&2
		exit 1
	fi
done

rm -rf "$WORK"
mkdir -p "$PROBE/scripts" "$PROBE/bin" || { printf 'FAIL  could not create %s\n' "$PROBE" >&2; exit 1; }
ln -s "$ROOT/fixtures" "$PROBE/fixtures" || { printf 'FAIL  could not link the fixtures into %s\n' "$PROBE" >&2; exit 1; }
cp "$ROOT/scripts/compliance-live-control.sh" "$PROBE/scripts/" || exit 1
cp "$ROOT/scripts/capture-served-pages.sh" "$PROBE/scripts/" || exit 1
note "scratch lane root: ${PROBE#"$ROOT"/} (probe port 18095, nothing here touches the cluster)"

# --- 1. the tier is not built at all -----------------------------------------
printf '\nprobe A: bin/lolstats-web absent - the control has to fail and say so\n'
if sh "$PROBE/scripts/compliance-live-control.sh" >"$LOGB" 2>&1; then
	bad 'the control exited 0 with no tier to render, so it reported a verdict about a posture it never built'
else
	rc=$?
	good "the control exits non-zero with the tier unbuilt (exit $rc)"
fi
if grep -q 'lolstats-web is not built' "$LOGB"; then
	good "the failure names the missing build: $(grep -m1 'lolstats-web is not built' "$LOGB")"
else
	bad 'the failure does not name the missing build, so it failed for some other reason and proves nothing about the precondition'
	tail -5 "$LOGB" | while IFS= read -r line; do note "$line"; done
fi
note "captured output: ${LOGB#"$ROOT"/}"

# --- 2. the tier is built but stays in the demo posture ----------------------
printf '\nprobe B: the tier renders demo, not live - the control has to fail and say so\n'
ln -s "$ROOT/bin/lolstats-web" "$PROBE/bin/lolstats-web" || exit 1
# The one edit under probe: the posture the control asks the tier for. Pointing
# it at `demo` is the condition the control is supposed to refuse to run under,
# because check 11's live scan would then see an empty path list - the shape that
# produced the phantom "(standard input)" page in CI.
if ! sed 's/riot-match-v5/demo/' "$PROBE/scripts/compliance-live-control.sh" > "$PROBE/scripts/posture-demo.sh"; then
	bad 'the probe copy could not be rewritten, so probe B did not run'
else
	if grep -q 'riot-match-v5' "$PROBE/scripts/posture-demo.sh"; then
		bad 'the probe copy still asks the tier for the live posture, so probe B did not run'
	else
		if sh "$PROBE/scripts/posture-demo.sh" >"$LOGC" 2>&1; then
			bad 'the control exited 0 over a demo posture, so the live-state half of check 11 was never scanned and it passed anyway'
		else
			rc=$?
			good "the control exits non-zero over a demo posture (exit $rc)"
		fi
		if grep -q 'not data-state="live"' "$LOGC"; then
			good "the failure names the state it saw: $(grep -m1 'not data-state' "$LOGC")"
		else
			bad 'the failure does not name the state, so it failed for some other reason and proves nothing about the posture'
			tail -5 "$LOGC" | while IFS= read -r line; do note "$line"; done
		fi
		note "captured output: ${LOGC#"$ROOT"/}"
	fi
fi

printf '\n--- summary ---\n'
if [ "$broken" -eq 0 ]; then
	printf 'RESULT: PASS - %s fail-closed probe(s) held and 0 broken\n' "$controls"
	printf 'ok: make compliance-live-control refuses to report a verdict it cannot reach; the passing\n'
	printf '    direction of the same control is `make compliance-live-control` itself\n'
	exit 0
fi
printf 'RESULT: FAIL - %s probe(s) broken of %s\n' "$broken" "$((controls + broken))"
exit 1
