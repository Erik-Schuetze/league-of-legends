#!/bin/sh
# Negative control for the render-parity gate (Makefile target `test-parity`,
# internal/webtier/parity_test.go).
#
# Why it exists. The parity tests used to be unable to fail: in CI they ran
# before `npm run build`, so web/dist was absent, all three called t.Skip, and
# the job reported success over tests that proved nothing about the Go tier
# (docs/compliance.md, docs/contracts.md section 5). `make test-parity` now
# builds the reference tree first and treats a SKIP as a failure, and
# `make test-parity WEB_DIST_SKIP=1` is the control for that half - it reproduces
# the CI ordering defect exactly and the gate has to go red.
#
# This script is the control for the other half: that the comparison is live,
# i.e. that a rendering input which changes the published bytes really does fail
# the gate, rather than the tests reporting PASS over a comparison that no longer
# compares anything. It changes one attribute in the reference page the `home`
# case is compared against, requires `make test-parity` to exit non-zero and to
# name that route as a mismatch, then restores the file and proves the restore by
# hash.
#
# It mutates web/dist in place, because internal/webtier/parity_test.go reads the
# reference tree from a fixed path (../../web/dist) and has no override. Nothing
# else may read web/dist while it runs, so the Makefile gives it its own step and
# the compliance scans run after it. The original bytes are kept under bin/, the
# restore is on a trap, and a backup left by an interrupted run is put back at
# startup - but only after it has been checked against the page, so a stale
# backup is never written over a freshly built reference tree.
#
# The direction this control deliberately does *not* assert is "the unmutated
# tree passes". That is what the gate itself asserts, in the build workflow; a
# control that asserted it would report an unrelated, already-open parity break
# as this control's failure, which is exactly the confusion the two-signal
# arrangement in .github/workflows/gates.yml exists to avoid.
#
# No network, no cluster, no package manager.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST=${LOLSTATS_DIST:-$ROOT/web/dist}
PAGE="$DIST/index.html"
BACKUP="$ROOT/bin/parity-mutation-index.html.orig"
LOG="$ROOT/bin/parity-mutation.log"
ROUTE=home
# An attribute no renderer is allowed to change silently, in a document that is
# compared byte for byte.
FROM='lang="en"'
TO='lang="zz"'

controls=0
broken=0
good() { controls=$((controls + 1)); printf 'PASS  %s\n' "$1"; }
bad() { broken=$((broken + 1)); printf 'FAIL  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }

hash_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum <"$1" | awk '{print $1}'
	else
		shasum -a 256 <"$1" | awk '{print $1}'
	fi
}

count_of() {
	grep -o "$1" "$2" 2>/dev/null | wc -l | tr -d ' '
}

if [ ! -f "$PAGE" ]; then
	printf 'FAIL  %s is not present; build the reference tree first: make web-dist\n' "$PAGE"
	exit 1
fi
mkdir -p "$ROOT/bin" || { printf 'FAIL  could not create %s/bin\n' "$ROOT"; exit 1; }

# A backup left behind means an earlier run was killed between the mutation and
# the restore, and the tree has to go back before anything is measured. The
# backup is only used when it looks like the original and the page does not:
# restoring a stale backup over a freshly built reference tree would silently
# replace the reference with an older revision, which is a worse defect than the
# one this control exists to catch. So an unusable backup stops the run and asks
# for a rebuild instead of guessing.
if [ -f "$BACKUP" ]; then
	if [ "$(count_of "$FROM" "$PAGE")" = 1 ] && [ "$(count_of "$TO" "$PAGE")" = 0 ]; then
		note "a previous run left $BACKUP behind, but $PAGE is intact: discarding the backup"
		rm -f "$BACKUP"
	elif [ "$(count_of "$FROM" "$BACKUP")" = 1 ] && [ "$(count_of "$TO" "$BACKUP")" = 0 ]; then
		note "a previous run was interrupted mid-mutation: putting $PAGE back from $BACKUP"
		cp "$BACKUP" "$PAGE" || { printf 'FAIL  could not restore %s\n' "$PAGE"; exit 1; }
		rm -f "$BACKUP"
	else
		printf 'FAIL  a backup from a previous run is present, %s is not intact, and the backup does not look like an original either\n' "$PAGE"
		printf '      rebuild the reference tree rather than letting this script restore the wrong bytes: rm -f web/dist/.make-web-dist.stamp && make web-dist\n'
		exit 1
	fi
fi

before=$(hash_of "$PAGE")
cp "$PAGE" "$BACKUP" || { printf 'FAIL  could not back up %s\n' "$PAGE"; exit 1; }

restore() {
	if [ -f "$BACKUP" ]; then
		cp "$BACKUP" "$PAGE" 2>/dev/null
		rm -f "$BACKUP"
	fi
}
trap restore EXIT INT TERM

if [ "$(count_of "$FROM" "$PAGE")" != 1 ]; then
	bad "the reference page does not carry $FROM exactly once, so the mutation has nothing to change"
	note "looked in $PAGE; if the reference build changed, update FROM in this script rather than dropping the probe"
	printf 'RESULT: FAIL - %s control(s) held, %s broken\n' "$controls" "$broken"
	exit 1
fi

sed "s/$FROM/$TO/" "$PAGE" >"$ROOT/bin/parity-mutation-index.html.new" || {
	bad "the mutation could not be written"
	printf 'RESULT: FAIL - %s control(s) held, %s broken\n' "$controls" "$broken"
	exit 1
}
mv "$ROOT/bin/parity-mutation-index.html.new" "$PAGE"

if [ "$(count_of "$TO" "$PAGE")" != 1 ]; then
	bad "the mutation did not land: $TO is not in the reference page"
	printf 'RESULT: FAIL - %s control(s) held, %s broken\n' "$controls" "$broken"
	exit 1
fi
good "a rendering input was changed in the reference page: $FROM -> $TO (the file the '$ROUTE' case is compared against)"

note "running the gate with the reference tree left as it is (WEB_DIST_SKIP=1, so nothing rebuilds over the mutation)"
( cd "$ROOT" && make test-parity WEB_DIST_SKIP=1 ) >"$LOG" 2>&1
status=$?

if [ "$status" -eq 0 ]; then
	bad "the gate exited 0 over a mutated reference page: the comparison is not live"
	note "full output in $LOG"
else
	good "the gate exited $status, so a changed rendering input is a failure"
fi

if grep -qE '(^|[[:space:]])--- SKIP:' "$LOG"; then
	bad "the run reported SKIP: the gate failed for the wrong reason, which is the defect this control exists to keep closed"
elif grep -q "render mismatch for $ROUTE" "$LOG"; then
	good "the failure is the comparison itself: 'render mismatch for $ROUTE' appears in $LOG"
else
	bad "no 'render mismatch for $ROUTE' in $LOG, so the gate did not fail because of the mutation"
	note "last lines of the run:"
	tail -3 "$LOG" | while IFS= read -r line; do note "$line"; done
fi

# Attributing the failure to the mutation rather than to some other mismatch is
# what makes this control worth running on a tree that is already red: the
# comparison reports the first differing offset with the bytes either side, so
# the mutated attribute has to appear on the reference side of that excerpt.
excerpt=$(awk -v route="$ROUTE" '
	$0 ~ "render mismatch for " route " " { hit = 1 }
	hit && /reference: / { print; exit }
' "$LOG")
if [ -n "$excerpt" ] && printf '%s' "$excerpt" | grep -qF "$TO"; then
	good "the mutation is what the comparison caught: the '$ROUTE' excerpt has $TO on the reference side"
else
	bad "the '$ROUTE' excerpt does not carry $TO on the reference side, so this run does not attribute the failure to the mutation"
	[ -n "$excerpt" ] && note "$excerpt"
fi

restore
after=$(hash_of "$PAGE")
if [ "$after" = "$before" ]; then
	good "the reference page was restored byte for byte ($before)"
else
	bad "the reference page was not restored: $before -> $after; restore it from $DIST or rebuild it (make web-dist)"
fi
if [ -f "$BACKUP" ]; then
	bad "the backup $BACKUP was left behind"
else
	good "no backup left behind"
fi

printf 'RESULT: %s - %s control(s) held, %s broken\n' \
	"$([ "$broken" -eq 0 ] && echo PASS || echo FAIL)" "$controls" "$broken"
[ "$broken" -eq 0 ] || exit 1
