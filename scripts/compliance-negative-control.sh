#!/bin/sh
# Negative controls for the two launch-blocking compliance checks that the
# dynamic-serving amendment rewrote: check 3 (third-party scripts) and check 4
# (the free tier is free and ungated). The amendment is recorded, with its
# reason and the wording it replaced, in docs/compliance.md - see "Amendment:
# checks 3 and 4, the dynamic-serving amendment".
#
# An amended check is only trustworthy if it still fails on a genuinely bad
# page, so this script plants one violation at a time into a copy of the served
# capture the gate reads, and asserts that the gate exits non-zero, names the
# right check and says what it found. It also asserts the direction the
# amendment claims - a page with no <script> at all is legal now - because a rule
# that only ever fails is not a control either.
#
# It never writes to the capture: the plants go into a scratch copy under
# .agent-artifacts/, that copy is asserted to pass the gate *before* anything is
# planted into it (so a probe cannot pass because the copy was already broken),
# and every plant is asserted to have landed before the gate is believed.
#
#   LOLSTATS_DIST          the served capture to copy (default bin/served-pages)
#   LOLSTATS_CONTROL_PAGES how many pages the scratch copy keeps (default 150);
#                          the gate refuses a resource scan of fewer than 100,
#                          and the full tree is not needed to plant one page
#
# Needs sh, curl is not used, no network, no package manager.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GATE="$ROOT/scripts/compliance-check.sh"
DIST=${LOLSTATS_DIST:-$ROOT/bin/served-pages}
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
	printf 'FAIL  %s is not present; capture the served pages first: make served-pages\n' "$DIST"
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
# under one of them, so these pages are always kept on top of the first
# $KEEP_PAGES in path order:
#
#   - index.html, about/, disclaimer/ and the two legal pages: the gate's
#     preflight names them, and dropping one makes it exit 2 with "the served
#     corpus is incomplete" before it reaches any check, which would look like a
#     broken control rather than a broken copy.
#   - $plant_page and a page carrying the filter bar: check 4 asserts that a
#     no-JS path exists, so a corpus with no <form> in it is a violation, and
#     the alphabetically-first pages are all champion pages with nothing to
#     submit through.
keep_required='index.html about/index.html disclaimer/index.html legal/privacy/index.html legal/terms/index.html tier-list/mid/index.html'
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
# plant_inside puts the fragment before the closing </html>, for the controls
# whose plant must leave a *well-formed* page: the gate asserts that a page ends
# with </html>, so appending to the end of the file would fail it for a reason
# that has nothing to do with the control. Escapes the two characters sed reads
# in a replacement.
plant_inside() {
	frag=$(printf '%s' "$1" | sed -e 's/[\\&]/\\\\&/g')
	sed -e "s|</html>|$frag</html>|" "$SCRATCH/$plant_page" > "$SCRATCH/$plant_page.inside" &&
		mv "$SCRATCH/$plant_page.inside" "$SCRATCH/$plant_page"
}

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
	fail_line 'gating element(s) found in the served pages'
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
# reporting "(standard input)" as a live page with no banner. The captured
# corpus has no live page at all - it is a capture of a demo build - so the
# non-empty half of that scan never ran in CI, and a guard that had silently
# disabled it would have looked identical. Declaring one demo page live, with the banner it would need missing,
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
printf '\ncontrol 7: a fabricated analytics script from an arbitrary third-party origin (check 3)\n'
# The other half of check 3's rule, and the one the tracker-name list cannot
# cover: an origin nobody has heard of. The check must catch it because it
# asserts the *origin* of every executable resource a page loads, not a list of
# known trackers, so a script hosted on a hostname invented here - and therefore
# on no denylist anywhere - is still a third-party script that phones home.
plant '<script src="https://third-party.example/analytics.js" defer></script>'
if grep -qF 'third-party.example/analytics.js' "$SCRATCH/$plant_page"; then
	run_gate
	fail_line 'external resource reference(s) or tracker name(s) found'
else
	bad 'the fabricated third-party <script> did not land in the page'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\ncontrol 8: a form whose controls have no server-side no-JS fallback (check 4)\n'
# The amendment allows a <form> only when it is a no-JS server-side path. This
# plants the shape the amendment is meant to reject: a form carrying real
# controls - a search box and a sort selector, exactly the filter bar's own
# widgets - with no method and no action, so the controls exist, look like the
# tier's, and submit nowhere the server can answer without JavaScript.
plant '<form class="ds-filter-bar" data-negcontrol><input type="search" name="q"><select name="sort"><option value="pick_rate">pick rate</option></select></form>'
if grep -qF 'data-negcontrol' "$SCRATCH/$plant_page"; then
	run_gate
	fail_line 'form(s) are not a no-JS server-side path'
else
	bad 'the JS-only form did not land in the page'
fi
restore_page

# ---------------------------------------------------------------------------
printf '\ncontrol 9: a page carrying the tier filter bar itself passes (check 4, the amendment is not a lacuna)\n'
# The direction the amendment has to keep: the tier's filter bar is a real
# <form>, and a rule that rejected every form would fail the page a reader
# actually gets. The markup below is copied from a live response
# (?sort=pick_rate is a real query the tier answers), so this control fails if a
# future tightening of check 4 starts rejecting the tier's own controls.
# The fragment goes before </html> so the page stays well-formed: check 4 is not
# what this control is about, and the end-of-document rule would otherwise
# decide the outcome.
plant_inside '<form class="ds-filter-bar ds-print-hidden" action="/tier-list/mid" method="get" aria-label="Filters"><input id="filter-q" type="search" name="q"><select id="filter-sort" name="sort"><option value="win_rate" selected>win rate</option><option value="pick_rate">pick rate</option></select><select id="filter-dir" name="dir"><option value="desc" selected>desc</option></select><select id="filter-per" name="per"><option value="0" selected>all</option></select></form>'
if grep -qF 'id="filter-sort"' "$SCRATCH/$plant_page"; then
	run_gate
	if [ "$gate_status" -eq 0 ]; then
		good "the tier's own no-JS GET filter bar passes the amended check 4, so the amendment is not a lacuna"
	else
		bad "the tier's own filter bar fails the amended check 4, which would reject the page a reader receives:"
		grep -m5 '^FAIL' "$LOG" | sed 's/^/      /'
	fi
else
	bad 'the filter bar did not land in the page, so the control proved nothing'
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
