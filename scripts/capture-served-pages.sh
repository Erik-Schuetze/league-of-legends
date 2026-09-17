#!/bin/sh
# scripts/capture-served-pages.sh - capture what a running tier serves into a
# directory, which is the corpus scripts/compliance-check.sh scans.
#
#   sh scripts/capture-served-pages.sh
#
# Why this is the gate's corpus: the compiled Astro reference tree used to be the
# input (web/dist), and it was removed on 2026-09-17 when the Go SSR tier became
# the only published site. The tier's own output was already the corpus the
# amended checks 3 and 4 were written for - against a pre-rendered tree both were
# vacuous in one direction, because that tree's filter bar was a client island
# and so carried no <form> at all, and a rule about forms that no page can
# violate proves nothing. What the gate reads now is what a reader receives:
# `make compliance` starts the tier on loopback over the checked-in fixture
# artifact tree, runs this script, and scans the result.
#
# Routes come from the tier's own /sitemap.xml, never from a hardcoded list, so a
# route that appears or disappears in the build is captured or drops out by
# itself. The whole site is captured by default: several checks refuse to accept
# a corpus small enough to be a sample rather than evidence. A fixed set of the
# routes that carry the interactive controls is always included, because those
# are the pages the amended rules are about: if the capture happened to miss
# every form, check 4 would fail with "no <form> in any of them" rather than pass
# - but it should not depend on the sampling.
#
# Three things that are not pages are captured too, because the gate reads them
# and they are part of what the deployment publishes: /robots.txt, /sitemap.xml
# and /riot.txt. The first two are addressed by check 8; the third is the
# evidence check 5 turns on, so it is kept when the tier offers it and left out
# when the tier does not, which is the honest reproduction of both states.
#
# It is strict on purpose: any route that does not answer 200, any declaration of
# Content-Length that disagrees with the bytes delivered, any two routes that map
# onto one destination, and any capture shorter than the floor are failures here.
# A capture that silently contains error pages, or that half-wrote one, would
# make the gate's agreement meaningless.
#
# Environment:
#   LOLSTATS_SERVE_URL     origin to capture (default http://127.0.0.1:18099)
#   LOLSTATS_SERVED_DIST   where to write it (default <repo>/bin/served-pages)
#   LOLSTATS_SERVED_MAX    most pages to capture; 0 means every route (default 0)
#   LOLSTATS_SERVED_MIN    fewest pages the capture may yield (default 100)

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BASE_URL="${LOLSTATS_SERVE_URL:-http://127.0.0.1:18099}"
BASE_URL=${BASE_URL%/}
OUT="${LOLSTATS_SERVED_DIST:-$ROOT/bin/served-pages}"
if [ "${OUT#/}" = "$OUT" ]; then OUT="$ROOT/$OUT"; fi
MAX_PAGES=${LOLSTATS_SERVED_MAX:-0}
MIN_PAGES=${LOLSTATS_SERVED_MIN:-100}
WORK="$ROOT/.agent-artifacts/capture-served-pages.$$"

failures=0
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }
note() { printf '      %s\n' "$1"; }

mkdir -p "$WORK" || { printf 'FAIL  could not create %s\n' "$WORK"; exit 1; }
trap 'rm -rf "$WORK"' EXIT INT TERM

if ! command -v curl >/dev/null 2>&1; then
	printf 'FAIL  curl is not installed; this capture reads real responses\n'
	exit 1
fi

# The sitemap is the tier's own statement of which pages exist.
curl -sS -m 30 -o "$WORK/sitemap.xml" -w '%{http_code}' "$BASE_URL/sitemap.xml" > "$WORK/sitemap.status" 2>"$WORK/curl.err" ||
	printf '000' > "$WORK/sitemap.status"
if [ "$(cat "$WORK/sitemap.status")" != 200 ]; then
	fail "$BASE_URL/sitemap.xml: HTTP $(cat "$WORK/sitemap.status"), expected 200 - the capture cannot ask the tier which pages it has"
	# Fall back to the routes every build has, so a broken sitemap produces a
	# small honest capture that the gate's own floors then reject, rather than a
	# silent empty one.
	printf '%s\n' / /about /disclaimer /legal/terms /legal/privacy /tier-list/mid > "$WORK/routes.txt"
else
	# The path of each <loc>, whether it is absolute, protocol-relative or already
	# a path. The scheme is matched with a character class rather than `\?`
	# because BSD sed does not support that escape: `s|^https\?://[^/]*||` silently
	# strips nothing on macOS, which then makes the route list empty and the
	# capture look like a tier with no pages rather than a broken extractor.
	sed -n 's|.*<loc>\([^<]*\)</loc>.*|\1|p' "$WORK/sitemap.xml" |
		sed -e 's|^[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*||' -e 's|^//[^/]*||' |
		grep -E '^/' | LC_ALL=C sort -u > "$WORK/routes.txt"
	note "the tier's sitemap names $(wc -l < "$WORK/routes.txt" | tr -d ' ') route(s)"
fi

# /about and /about/ are the same document - measured, byte for byte - and the
# tier's sitemap names the form without the trailing slash while a hand-written
# list tends to name the other. Routes are normalized to one form so that a route
# named twice is captured once and, more importantly, so that two routes can
# never map onto one path in the corpus: /patch/16.17/tier-list/mid written as a
# file and /patch/16.17/tier-list/mid/index.html needing it to be a directory is
# how an earlier capture failed with a mkdir error instead of a HTTP status.
normalize_route() {
	case "$1" in
	/) printf '/\n' ;;
	*/) printf '%s\n' "${1%/}" ;;
	*) printf '%s\n' "$1" ;;
	esac
}
LC_ALL=C sort -u "$WORK/routes.txt" | while IFS= read -r route; do
	normalize_route "$route"
done > "$WORK/routes-normalized.txt"
mv "$WORK/routes-normalized.txt" "$WORK/routes.txt"

# The routes the amended rules are about, always captured: the two the
# coordinator measured as carrying exactly one <form> (the no-JS filter bar) and
# the champion pages that legitimately carry no <script> at all.
required='/ /about /disclaimer /legal/terms /legal/privacy /tier-list/mid /champions/ahri /champions/ahri/mid'
# The patch-scoped tier-list route is discovered from the tier's own links,
# because the patch is a property of the published snapshot.
curl -sS -m 30 -o "$WORK/tierlist.html" "$BASE_URL/tier-list/mid" 2>/dev/null || true
patch=$(sed -n 's|.*href="/patch/\([0-9][0-9.]*\)/tier-list/mid.*|\1|p' "$WORK/tierlist.html" | head -1)
if [ -n "$patch" ]; then
	required="$required /patch/$patch/tier-list/mid"
else
	note 'the tier-list page linked no patch-scoped route, so none was required in the capture'
fi
for route in $required; do
	if ! grep -qxF "$route" "$WORK/routes.txt"; then
		printf '%s\n' "$route" >> "$WORK/routes.txt"
	fi
done
LC_ALL=C sort -u "$WORK/routes.txt" -o "$WORK/routes.txt"

# Every route by default: the checks in the gate refuse a corpus small enough to
# be a sample rather than evidence, and the whole site is a little over a thousand
# pages, which this walks in well under a minute. LOLSTATS_SERVED_MAX asks for a
# deterministic sample instead - every Nth route, with the required ones always
# kept - and is then a deliberately smaller corpus, which the gate's floors may
# reject; that is the honest outcome rather than a quiet one.
total=$(wc -l < "$WORK/routes.txt" | tr -d ' ')
stride=1
if [ "$MAX_PAGES" -gt 0 ] && [ "$total" -gt "$MAX_PAGES" ]; then
	stride=$((total / MAX_PAGES + 1))
fi
: > "$WORK/selected.txt"
i=0
while IFS= read -r route; do
	[ -n "$route" ] || continue
	i=$((i + 1))
	case " $required " in
	*" $route "*) printf '%s\n' "$route" >> "$WORK/selected.txt"; continue ;;
	esac
	if [ $((i % stride)) -eq 0 ]; then
		printf '%s\n' "$route" >> "$WORK/selected.txt"
	fi
done < "$WORK/routes.txt"
LC_ALL=C sort -u "$WORK/selected.txt" -o "$WORK/selected.txt"

# Where each selected route lands in the corpus, decided before anything is
# written, so that two routes mapping onto one path is a failure here rather than
# a silent overwrite of one page by another. A leaf with an extension is a file
# (/robots.txt); everything else is a page whose path ends in /index.html, which
# keeps the corpus browsable as a tree. The extension is read off the last
# segment alone: reading it off the whole path made /patch/16.17/tier-list/mid a
# file, because "16.17" contains a dot, and the next route that needed
# /patch/16.17/tier-list/mid to be a directory failed with a mkdir error.
: > "$WORK/targets.txt"
while IFS= read -r route; do
	[ -n "$route" ] || continue
	case "$route" in
	/) rel=index.html ;;
	*)
		case "${route##*/}" in
		*.*) rel=${route#/} ;;
		*) rel="${route#/}/index.html" ;;
		esac
		;;
	esac
	printf '%s\t%s\n' "$route" "$rel" >> "$WORK/targets.txt"
done < "$WORK/selected.txt"

duplicated=$(cut -f2 "$WORK/targets.txt" | LC_ALL=C sort | uniq -d)
if [ -n "$duplicated" ]; then
	fail 'two routes map onto one path in the corpus, so one page would overwrite the other:'
	printf '%s\n' "$duplicated" | while IFS= read -r dup; do
		awk -F'\t' -v d="$dup" '$2 == d { print "      " $1 " -> " $2 }' "$WORK/targets.txt"
	done | head -5
fi

rm -rf "$OUT"
mkdir -p "$OUT" || { printf 'FAIL  could not create %s\n' "$OUT"; exit 1; }
: > "$OUT/served-pages.txt"

captured=0
while IFS='	' read -r route rel; do
	[ -n "$route" ] || continue
	dest="$OUT/$rel"
	mkdir -p "$(dirname "$dest")"
	status=$(curl -sS -m 30 -D "$WORK/last.head" -o "$dest" -w '%{http_code}' "$BASE_URL$route" 2>"$WORK/curl.err") || status=000
	bytes=$(wc -c < "$dest" 2>/dev/null | tr -d ' ')
	declared=$(awk 'tolower($0) ~ /^content-length:/ {sub(/\r$/,""); sub(/^[^:]*:[ \t]*/,""); print; exit}' "$WORK/last.head")
	ctype=$(awk 'tolower($0) ~ /^content-type:/ {sub(/\r$/,""); sub(/^[^:]*:[ \t]*/,""); print; exit}' "$WORK/last.head")
	if [ "$status" != 200 ]; then
		# A 503 with a visible page is the tier's correct answer when no snapshot
		# is published, so it is not captured as evidence about the site's pages.
		if [ "$status" = 503 ]; then
			note "$route: HTTP 503 (no snapshot published); not captured"
		else
			fail "$route: HTTP $status, expected 200 - a capture must not contain error pages"
		fi
		rm -f "$dest"
		continue
	fi
	if [ -n "$declared" ] && [ "$declared" != "$bytes" ]; then
		fail "$route: Content-Length $declared but $bytes bytes were delivered"
	fi
	case "$ctype" in
	*text/html*) ;;
	*)
		# /robots.txt and /sitemap.xml are not HTML and are not the gate's
		# subject; they are recorded and then dropped from the corpus.
		printf '%s\t%s\t%s\t%s\n' "$route" "$status" "$bytes" "$ctype" >> "$OUT/served-pages.txt"
		rm -f "$dest"
		continue
		;;
	esac
	printf '%s\t%s\t%s\t%s\n' "$route" "$status" "$bytes" "$ctype" >> "$OUT/served-pages.txt"
	captured=$((captured + 1))
done < "$WORK/targets.txt"

# The three published things that are not pages. /robots.txt and /sitemap.xml are
# read by check 8, which takes the origin out of them; the sitemap has been
# downloaded once already and is kept rather than fetched twice. /riot.txt is the
# evidence check 5 turns on: the tier serves it only while a verification token is
# configured, so a 404 is its expected answer and is recorded as absent, while a
# configured token that the tier does not publish must be visible to the gate
# rather than missing from the corpus.
cp "$WORK/sitemap.xml" "$OUT/sitemap.xml" 2>/dev/null ||
	fail "could not keep the tier's /sitemap.xml in the corpus"
for extra in /robots.txt /riot.txt; do
	extra_rel=${extra#/}
	extra_status=$(curl -sS -m 30 -o "$OUT/$extra_rel" -w '%{http_code}' "$BASE_URL$extra" 2>/dev/null) || extra_status=000
	case "$extra_status" in
	200)
		printf '%s\t%s\t%s\t%s\n' "$extra" "$extra_status" "$(wc -c < "$OUT/$extra_rel" | tr -d ' ')" text/plain >> "$OUT/served-pages.txt"
		;;
	404)
		rm -f "$OUT/$extra_rel"
		printf '%s\t%s\t%s\t%s\n' "$extra" "$extra_status" 0 absent >> "$OUT/served-pages.txt"
		note "$extra: HTTP 404, which is the tier's answer when it publishes no such file"
		;;
	*)
		rm -f "$OUT/$extra_rel"
		fail "$extra: HTTP $extra_status, expected 200 or 404"
		;;
	esac
done

if [ "$captured" -lt "$MIN_PAGES" ]; then
	fail "the capture holds $captured HTML page(s), fewer than the $MIN_PAGES the gate's served corpus needs to mean anything"
else
	absent=''
	for route in $required; do
		rel=$(awk -F'\t' -v r="$route" '$1 == r { print $2; exit }' "$WORK/targets.txt")
		if [ -z "$rel" ] || [ ! -f "$OUT/$rel" ]; then absent="$absent $route"; fi
	done
	if [ -n "$absent" ]; then
		fail "these routes the amended checks are about were not captured from $BASE_URL:$absent"
	else
		printf 'PASS  captured %s HTML page(s) from %s into %s (%s route(s) selected of %s)\n' \
			"$captured" "$BASE_URL" "$OUT" "$(wc -l < "$WORK/selected.txt" | tr -d ' ')" "$total"
		for route in $required; do
			rel=$(awk -F'\t' -v r="$route" '$1 == r { print $2; exit }' "$WORK/targets.txt")
			note "$route: $(grep -o '<script' "$OUT/$rel" | wc -l | tr -d ' ') <script>, $(grep -o '<form' "$OUT/$rel" | wc -l | tr -d ' ') <form>"
		done
	fi
fi

if [ "$failures" -eq 0 ]; then
	printf 'RESULT: PASS - the served corpus was captured with 0 problem(s)\n'
	exit 0
fi
printf 'RESULT: FAIL - %s problem(s) capturing the served corpus\n' "$failures"
exit 1
