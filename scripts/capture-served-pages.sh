#!/bin/sh
# scripts/capture-served-pages.sh - capture the HTML a running tier actually
# serves into a directory, so the compliance gate can scan the served corpus and
# not only the built tree.
#
#   sh scripts/capture-served-pages.sh
#
# Why this exists: checks 3 and 4 of scripts/compliance-check.sh were amended on
# 2026-09-17 (docs/compliance.md) so that a page with no <script> is legal and a
# <form> is legal when it is a no-JS server-side path. Against web/dist alone
# both amended rules are vacuous in one direction: the reference tree's filter
# bar is a client island, so that tree carries no <form> at all, and a rule about
# forms that no page can violate proves nothing. The tier renders the no-JS GET
# form, so the corpus the rules were written for is the tier's output. This
# script produces it: `LOLSTATS_SERVED_DIST=<dir> sh scripts/compliance-check.sh`
# scans it, and `make compliance-served` does both in one step.
#
# Routes come from the tier's own /sitemap.xml, never from a hardcoded list, so a
# route that appears or disappears in the build is captured or drops out by
# itself. A fixed sample of the routes that carry the interactive controls is
# always included, because those are the pages the amended rules are about: if
# the capture happened to miss every form, check 4 would fail with "no <form> in
# any of them" rather than pass - but it should not depend on the sampling.
#
# It is strict on purpose: any route that does not answer 200, any declaration of
# Content-Length that disagrees with the bytes delivered, and any capture shorter
# than the floor are failures here. A capture that silently contains error pages
# would make the gate's agreement meaningless.
#
# Environment:
#   LOLSTATS_SERVE_URL     origin to capture (default http://127.0.0.1:18099)
#   LOLSTATS_SERVED_DIST   where to write it (default <repo>/bin/served-pages)
#   LOLSTATS_SERVED_MAX    most pages to capture (default 60)
#   LOLSTATS_SERVED_MIN    fewest pages the capture may yield (default 8)

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
BASE_URL="${LOLSTATS_SERVE_URL:-http://127.0.0.1:18099}"
BASE_URL=${BASE_URL%/}
OUT="${LOLSTATS_SERVED_DIST:-$ROOT/bin/served-pages}"
if [ "${OUT#/}" = "$OUT" ]; then OUT="$ROOT/$OUT"; fi
MAX_PAGES=${LOLSTATS_SERVED_MAX:-60}
MIN_PAGES=${LOLSTATS_SERVED_MIN:-8}
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
	printf '%s\n' / /about/ /disclaimer/ /legal/terms/ /legal/privacy/ /tier-list/mid/ > "$WORK/routes.txt"
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

# The routes the amended rules are about, always captured: the two the
# coordinator measured as carrying exactly one <form> (the no-JS filter bar) and
# the champion pages that legitimately carry no <script> at all.
required='/tier-list/mid/ /champions/ahri/ /champions/ahri/mid/'
# The patch-scoped tier-list route is discovered from the tier's own links,
# because the patch is a property of the published snapshot.
curl -sS -m 30 -o "$WORK/tierlist.html" "$BASE_URL/tier-list/mid/" 2>/dev/null || true
patch=$(sed -n 's|.*href="/patch/\([0-9][0-9.]*\)/tier-list/mid.*|\1|p' "$WORK/tierlist.html" | head -1)
if [ -n "$patch" ]; then
	required="$required /patch/$patch/tier-list/mid/"
else
	note 'the tier-list page linked no patch-scoped route, so none was required in the capture'
fi
for route in $required; do
	if ! grep -qxF "$route" "$WORK/routes.txt"; then
		printf '%s\n' "$route" >> "$WORK/routes.txt"
	fi
done
LC_ALL=C sort -u "$WORK/routes.txt" -o "$WORK/routes.txt"

# A deterministic sample of the rest, so a large sitemap does not turn one gate
# run into a thousand requests: every route in the file if it is short enough,
# otherwise every Nth one, with the required routes always kept.
total=$(wc -l < "$WORK/routes.txt" | tr -d ' ')
stride=1
if [ "$total" -gt "$MAX_PAGES" ]; then
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

rm -rf "$OUT"
mkdir -p "$OUT" || { printf 'FAIL  could not create %s\n' "$OUT"; exit 1; }
: > "$OUT/served-pages.txt"

captured=0
while IFS= read -r route; do
	[ -n "$route" ] || continue
	rel=${route#/}
	case "$route" in
	*/) rel="${rel}index.html" ;;
	*.*) rel="$rel" ;;
	*) rel="$rel/index.html" ;;
	esac
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
done < "$WORK/selected.txt"

if [ "$captured" -lt "$MIN_PAGES" ]; then
	fail "the capture holds $captured HTML page(s), fewer than the $MIN_PAGES the gate's served corpus needs to mean anything"
else
	absent=''
	for route in $required; do
		rel=${route#/}
		rel="${rel}index.html"
		[ -f "$OUT/$rel" ] || absent="$absent $route"
	done
	if [ -n "$absent" ]; then
		fail "these routes the amended checks are about were not captured from $BASE_URL:$absent"
	else
		printf 'PASS  captured %s HTML page(s) from %s into %s (%s route(s) sampled of %s)\n' \
			"$captured" "$BASE_URL" "$OUT" "$(wc -l < "$WORK/selected.txt" | tr -d ' ')" "$total"
		for route in $required; do
			rel=${route#/}
			rel="${rel}index.html"
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
