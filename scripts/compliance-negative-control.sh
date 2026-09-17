#!/bin/sh
# Negative controls for the two launch-blocking compliance checks that the
# dynamic-serving amendment rewrote: check 3 (third-party scripts) and check 4
# (the free tier is free and ungated). The amendment is recorded, with its
# reason and the wording it replaced, in docs/compliance.md - see "Amendment:
# checks 3 and 4, the dynamic-serving amendment".
#
# An amended check is only trustworthy if it still fails on a genuinely bad
# page, so this script plants one violation at a time into a copy of the built
# tree and asserts that the gate exits non-zero, names the right check and says
# what it found. It also asserts the direction the amendment claims - a page
# with no <script> at all is legal now - because a rule that only ever fails is
# not a control either.
#
# It never writes to web/dist: the plants go into a scratch copy under
# .agent-artifacts/, that copy is asserted to pass the gate *before* anything is
# planted into it (so a probe cannot pass because the tree was already broken),
# and every plant is asserted to have landed before the gate is believed.
#
#   LOLSTATS_DIST          the built tree to copy (default web/dist)
#   LOLSTATS_CONTROL_PAGES how many pages the scratch copy keeps (default 150);
#                          the gate refuses a resource scan of fewer than 100,
#                          and the full tree is not needed to plant one page
#
# Needs sh, curl is not used, no network, no package manager.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GATE="$ROOT/scripts/compliance-check.sh"
DIST=${LOLSTATS_DIST:-$ROOT/web/dist}
KEEP_PAGES=${LOLSTATS_CONTROL_PAGES:-150}
WORK="$ROOT/.agent-artifacts/compliance-negative-control.$$"
SCRATCH="$WORK/dist"
LOG="$WORK/gate.log"

plant_page='tier-list/mid/index.html'

controls=0
broken=0
good() { controls=$((controls + 1)); printf 'PASS  %s\n' "$1"; }
bad() { broken=$((broken + 1)); printf 'FAIL  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }

if [ ! -d "$DIST" ]; then
	printf 'FAIL  %s is not present; build the site first: (cd web && npm run build)\n' "$DIST"
	exit 1
fi
if [ ! -f "$DIST/$plant_page" ]; then
	printf 'FAIL  %s is not present in %s, so there is no page to plant a violation into\n' "$plant_page" "$DIST"
	exit 1
fi

rm -rf "$WORK"
mkdir -p "$WORK" || { printf 'FAIL  could not create %s\n' "$WORK"; exit 1; }
trap 'rm -rf "$WORK"' EXIT INT TERM

printf 'planting into a scratch copy of %s\n' "$DIST"
cp -R "$DIST" "$SCRATCH" || { printf 'FAIL  could not copy %s\n' "$DIST"; exit 1; }

# Keep $KEEP_PAGES pages and drop the rest, so a probe run costs seconds rather
# than a full-tree scan. The gate's own floors are what make a page count
# meaningful, and the clean-copy control below proves the pruning did not fall
# under one of them. The four pages the gate's preflight names are always kept:
# dropping one makes the gate exit 2 with "the built site is incomplete" before
# it reaches any check, which would look like a broken control rather than a
# broken copy.
keep_required='index.html about/index.html disclaimer/index.html legal/privacy/index.html legal/terms/index.html'
find "$SCRATCH" -type f -name '*.html' | LC_ALL=C sort |
	sed "s|^$SCRATCH/||" > "$WORK/relpaths.txt"
awk -v req="$keep_required" -v keep="$KEEP_PAGES" '
	BEGIN { n = split(req, r, " "); for (i = 1; i <= n; i++) must[r[i]] = 1 }
	{
		if ($0 in must) { print "keep " $0; next }
		n++;
		if (n <= keep) { print "keep " $0 } else { print "drop " $0 }
	}' "$WORK/relpaths.txt" > "$WORK/plan.txt"
while IFS=' ' read -r action rel; do
	[ "$action" = drop ] || continue
	rm -f "$SCRATCH/$rel"
done < "$WORK/plan.txt"
kept=$(find "$SCRATCH" -type f -name '*.html' | wc -l | tr -d ' ')
note "$kept page(s) kept for the probes; the gate is run with LOLSTATS_DIST=$SCRATCH"

# run_gate - leaves the exit status in $gate_status and the output in $LOG.
run_gate() {
	LOLSTATS_DIST="$SCRATCH" sh "$GATE" > "$LOG" 2>&1
	gate_status=$?
	return 0
}
# fail_line <text> - the gate must have failed and must say why in those words.
fail_line() {
	if [ "$gate_status" -eq 0 ]; then
		bad "$1: the gate still exited 0 with the violation planted, so the check no longer catches it"
		return 1
	fi
	if ! grep -qF "$1" "$LOG"; then
		bad "$1: the gate failed, but for a different reason: $(grep -m1 '^FAIL' "$LOG" || echo 'no FAIL line')"
		return 1
	fi
	good "$1"
	return 0
}
# The page the plant goes into, restored from the pristine copy between probes.
restore_page() { cp "$DIST/$plant_page" "$SCRATCH/$plant_page"; }
plant() { printf '%s\n' "$1" >> "$SCRATCH/$plant_page"; }

# ---------------------------------------------------------------------------
printf '\ncontrol 0: the clean copy must pass, or every probe below proves nothing\n'
run_gate
if [ "$gate_status" -eq 0 ]; then
	good "the unmodified scratch copy passes the gate (exit 0), so a failing probe below is caused by the plant"
else
	bad "the unmodified scratch copy already fails the gate; the pruning or the copy is at fault, not a plant:"
	grep -m5 '^FAIL' "$LOG" | sed 's/^/      /'
fi

# ---------------------------------------------------------------------------
printf '\ncontrol 1: a third-party script in a page (check 3, the tracker rule)\n'
plant '<script src="https://www.googletagmanager.com/gtag/js?id=G-NEGCONTROL"></script>'
if grep -qF 'G-NEGCONTROL' "$SCRATCH/$plant_page"; then
	run_gate
	fail_line 'googletagmanager'
else
	bad 'the analytics <script> did not land in the page'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\ncontrol 2: a form that posts off this origin (check 4, no-JS path rule)\n'
plant '<form action="https://example.invalid/negcontrol" method="post"><input name="q"></form>'
if grep -qF 'example.invalid/negcontrol' "$SCRATCH/$plant_page"; then
	run_gate
	fail_line 'form(s) are not a no-JS server-side path'
else
	bad 'the off-origin form did not land in the page'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\ncontrol 3: a named control with no form to submit through (check 4)\n'
plant '<input name="negcontrol" type="text">'
if grep -qF 'name="negcontrol"' "$SCRATCH/$plant_page"; then
	run_gate
	fail_line 'named control(s) sit outside a form'
else
	bad 'the orphan named control did not land in the page'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\ncontrol 4: a credential field (check 4, the original gating rule)\n'
plant '<form action="/negcontrol" method="get"><input type="password" name="password"></form>'
if grep -qF 'name="password"' "$SCRATCH/$plant_page"; then
	run_gate
	fail_line 'gating element(s) found in the built pages'
else
	bad 'the password field did not land in the page'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\ncontrol 5: a page with no <script> at all is legal (the amendment, not a lacuna)\n'
if perl -0pi -e 's|<script\b[^>]*>.*?</script>||gs' "$SCRATCH/$plant_page" 2>/dev/null; then
	if [ "$(grep -c '<script' "$SCRATCH/$plant_page")" -eq 0 ]; then
		run_gate
		if [ "$gate_status" -eq 0 ]; then
			good "a page stripped of every <script> passes: the check asserts the origin of the resources a page loads, not the presence of a tag"
		else
			bad "a script-free page fails the gate: the amendment forbids a tag it is supposed to allow:"
			grep -m5 '^FAIL' "$LOG" | sed 's/^/      /'
		fi
	else
		bad 'the <script> tags were not removed from the page, so the control proved nothing'
	fi
else
	bad 'perl is not available, so the script-free page could not be built; this control did not run'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\ncontrol 6: a live page with no live banner fails (check 11, the scan the CI bug hid)\n'
# This is the control for the defect that made CI red: check 11 hands a list of
# paths to grep -L, and grep answered an empty list from its own standard input,
# reporting "(standard input)" as a live page with no banner. The built tree has
# no live page at all - every page is demo - so the non-empty half of that scan
# never ran in CI, and a guard that had silently disabled it would have looked
# identical. Declaring one demo page live, with the banner it would need missing,
# is the case the scan exists for.
if sed 's|data-state="demo"|data-state="live"|g' "$DIST/$plant_page" > "$SCRATCH/$plant_page" &&
	grep -qF 'data-state="live"' "$SCRATCH/$plant_page" &&
	! grep -qF 'state-banner--live' "$SCRATCH/$plant_page"; then
	run_gate
	fail_line 'live page(s) carry no live banner'
else
	bad 'the page could not be made into a live page without a live banner, so the control proved nothing'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\n--- summary ---\n'
if [ "$broken" -eq 0 ]; then
	printf 'RESULT: PASS - %s negative control(s) held and 0 broken\n' "$controls"
	exit 0
fi
printf 'RESULT: FAIL - %s negative control(s) broken of %s\n' "$broken" "$((controls + broken))"
exit 1
