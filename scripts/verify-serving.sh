#!/bin/sh
# scripts/verify-serving.sh - response-integrity check for the served site.
#
#   sh scripts/verify-serving.sh [BASE_URL]
#
# BASE_URL defaults to $LOLSTATS_SITE_URL and then to http://127.0.0.1:8080, so
# it can be pointed at the deployed Service, at a port-forward, or at the real
# Caddy image run locally as uid 1000 against a copy of the data volume.
#
# Why this exists next to a probe that already answers 200: the web tier caches
# its own responses (cache-handler/Souin in deploy/base/web/caddyfile.yaml), and
# a poisoned cache entry is served as `200 OK` with the *original* Content-Length
# while the body is short. curl -o /dev/null reports success, kubectl logs show
# 200, and the page is missing its tail - which on this site is where the
# mandatory "PREVIEW - illustrative data" wording lives. A defect of exactly that
# shape was shipped once: cache-handler v0.16.0 with storages/core v0.0.18
# replays a hit truncated to 4096 bytes minus the stored header block, so every
# page after the first request lost its footer and its demo labelling.
#
# So every page here is fetched three times - a cache-busting request that cannot
# be a hit and therefore shows what the file server itself returns, then the miss,
# then the cache hit, which is where the defect lived - and each response is
# checked for three things that a smoke test does not check: the body is
# byte-for-byte what Content-Length promised, the body is a whole document (it
# ends with </html>), and it carries the data-provenance banner that its own state
# declares. A pass with a truncated page is impossible, and a pass that read
# nothing is impossible too, because the checks count what they read. The
# cache-busting reference is what keeps the comparison honest: two poisoned hits
# would otherwise agree with each other.
#
# The labelling assertion is state-aware rather than hardcoded to the preview
# wording, because the site renders three honest states (demo, live, no-data)
# with a different banner each. What must never happen is a page whose banner
# does not match the state it declares, or a page with no banner at all, so that
# is what is checked; LOLSTATS_EXPECT_STATE additionally pins which state the
# origin is allowed to serve, and it defaults to demo because the deployed tier
# builds with no Riot key and must therefore label itself as illustrative.
#
# Needs curl and a reachable origin. Exits non-zero on the first failing page
# set, after reporting every page, so one run is one diagnosis.
#
# Environment:
#   LOLSTATS_SITE_URL      the deployed address, when no argument is given
#   LOLSTATS_EXPECT_STATE  the data state the pages must declare (default demo);
#                          set it to live or no-data when checking a tier built
#                          from a real or empty snapshot

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/.agent-artifacts/verify-serving"
SITE_SRC="$ROOT/web/src/lib/site.ts"

BASE_URL=${1:-${LOLSTATS_SITE_URL:-http://127.0.0.1:8080}}
BASE_URL=${BASE_URL%/}

mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT INT TERM

failures=0
pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }
note() { printf '      %s\n' "$1"; }
check() { printf '\n== %s\n' "$1"; }

# The wording is read from the source of truth rather than repeated here, so
# that changing it in one place cannot leave this check asserting a string the
# site no longer emits. The literals are the fallback for a checkout without the
# site source (the deployed tier never has it).
PREVIEW_TEXT='PREVIEW - illustrative data generated to exercise the layout, not real match statistics'
UNVERIFIED_PREVIEW_TEXT='PREVIEW - the manifest does not declare where this snapshot came from, so its numbers are unverified'
NO_DATA_HEADING='No sample yet'
if [ -f "$SITE_SRC" ]; then
	from_source=$(sed -n "s/.*PREVIEW_TEXT = '\(.*\)'.*/\1/p" "$SITE_SRC" | head -1)
	if [ -n "$from_source" ]; then
		PREVIEW_TEXT=$from_source
		note "preview wording from web/src/lib/site.ts"
	else
		note "PREVIEW_TEXT not found in web/src/lib/site.ts; using the known wording"
	fi
	unverified=$(sed -n "s/.*UNVERIFIED_PREVIEW_TEXT = '\(.*\)'.*/\1/p" "$SITE_SRC" | head -1)
	[ -n "$unverified" ] && UNVERIFIED_PREVIEW_TEXT=$unverified
	no_data=$(sed -n "s/.*NO_DATA_HEADING = '\(.*\)'.*/\1/p" "$SITE_SRC" | head -1)
	[ -n "$no_data" ] && NO_DATA_HEADING=$no_data
else
	note "web/src/lib/site.ts not present; using the known wording"
fi

EXPECTED_STATE=${LOLSTATS_EXPECT_STATE:-demo}
note "pages must declare data-state=$EXPECTED_STATE; override with LOLSTATS_EXPECT_STATE"

byte_count() { wc -c < "$1" | tr -d ' '; }
# Header names are case-insensitive in HTTP and BSD awk has no IGNORECASE, so
# the comparison is done in awk against a lowercased copy of the field name.
header_value() { awk -v h="$2" 'tolower($0) ~ ("^" tolower(h) ":") {sub(/\r$/,""); sub(/^[^:]*:[ \t]*/,""); print; exit}' "$1"; }
# Trailing newline after </html> is fine; anything else is not.
ends_document() { tail -c 64 "$1" | grep -q '</html>' ; }
# The banner each state must carry, checked against the state the page itself
# declares. Returns non-zero and reports the reason, so the caller can treat it
# as one of its conditions.
labelling_ok() {
	state=$(sed -n 's/.*data-state="\([a-z-]*\)".*/\1/p' "$1" | head -1)
	case "$state" in
		'')
			fail "$2: no data-provenance banner at all, so the page does not say where its numbers came from"
			return 1
			;;
		"$EXPECTED_STATE") ;;
		*)
			fail "$2: the page declares data-state=$state but this check expects $EXPECTED_STATE (set LOLSTATS_EXPECT_STATE to check another state)"
			return 1
			;;
	esac
	case "$state" in
		demo)
			if grep -qF "$PREVIEW_TEXT" "$1" || grep -qF "$UNVERIFIED_PREVIEW_TEXT" "$1"; then
				return 0
			fi
			fail "$2: declares demo data but the labelling is missing: '$PREVIEW_TEXT'"
			return 1
			;;
		live)
			if grep -qF 'state-banner--live' "$1"; then
				return 0
			fi
			fail "$2: declares live data but carries no live banner"
			return 1
			;;
		no-data)
			if grep -qF "$NO_DATA_HEADING" "$1"; then
				return 0
			fi
			fail "$2: declares no data but the heading is missing: '$NO_DATA_HEADING'"
			return 1
			;;
		*)
			fail "$2: unrecognised data-state=$state"
			return 1
			;;
	esac
}

# Not every served path is an HTML document: /healthz is a bare word and the
# manifest is JSON, so the document and wording assertions apply to pages only.
PAGES='/ /tier-list/support/ /champions/ahri/ /about/'
JSON='/agg/v1/manifest.json'

printf 'served-response check: %s\n' "$BASE_URL"

if ! command -v curl >/dev/null 2>&1; then
	printf 'FAIL  curl is not installed; this check reads real responses\n'
	exit 1
fi

# ---------------------------------------------------------------------------
check '1. Pages are whole documents on a cache-busting request, a miss and a hit'
if [ ! -d "$WORK" ]; then
	fail 'work directory could not be created'
	exit 1
fi
fetched=0
# A query string makes the cache key new, so this request cannot be a hit and
# its body is the file server's own response. That reference matters because the
# two plain requests below can both be hits - a run that starts after the entry
# was already poisoned sees 3790 bytes twice, and identical poisoned bodies would
# otherwise look like agreement. This is the request that cannot agree with them.
RUN_ID="$$-$(date +%s)"
for page in $PAGES; do
	slug=$(echo "$page" | tr '/?' '__')
	ref="$WORK/$slug.ref.body"
	ref_status=$(curl -sS -m 30 -D "$ref.head" -o "$ref" -w '%{http_code}' "$BASE_URL$page?lolstats-verify=$RUN_ID" 2>"$WORK/curl.err")
	ref_bytes=$(byte_count "$ref" 2>/dev/null || echo 0)
	if [ "$ref_status" != '200' ] || [ "$ref_bytes" -eq 0 ]; then
		fail "$page (cache-busting reference): HTTP $ref_status, $ref_bytes bytes - the origin is not answering this path"
	elif ! ends_document "$ref"; then
		fail "$page (cache-busting reference): the file server itself returned a body that does not end in </html> - $ref_bytes bytes"
	elif ! labelling_ok "$ref" "$page (cache-busting reference)"; then
		: # labelling_ok reported the failure and the reason
	else
		note "$page reference (uncacheable): $ref_bytes bytes, whole document"
	fi
	i=0
	statuses=''
	while [ "$i" -lt 2 ]; do
		i=$((i + 1))
		h="$WORK/$slug.$i.head"
		b="$WORK/$slug.$i.body"
		# A response shorter than its own Content-Length would otherwise hold
		# the connection open; this check must terminate.
		status=$(curl -sS -m 30 -D "$h" -o "$b" -w '%{http_code}' "$BASE_URL$page" 2>"$WORK/curl.err")
		if [ ! -f "$b" ]; then
			fail "$page (request $i) produced no body: $(head -1 "$WORK/curl.err" 2>/dev/null)"
			continue
		fi
		fetched=$((fetched + 1))
		statuses="$statuses $status"
		bytes=$(byte_count "$b")
		label="$page (request $i, $( [ "$i" = 1 ] && echo miss || echo hit))"
		if [ "$status" != '200' ]; then
			fail "$label: HTTP $status"
		elif [ "$bytes" -eq 0 ]; then
			fail "$label: 200 with an empty body"
		elif ! ends_document "$b"; then
			fail "$label: 200 but the body does not end in </html> - $bytes bytes, last 60: $(tail -c 60 "$b" | tr -d '\n')"
		elif ! labelling_ok "$b" "$label"; then
			: # labelling_ok reported the failure and the reason
		else
			declared=$(header_value "$h" 'Content-Length')
			if [ -n "$declared" ] && [ "$declared" != "$bytes" ]; then
				fail "$label: Content-Length $declared but $bytes bytes were delivered"
			else
				state=$(sed -n 's/.*data-state="\([a-z-]*\)".*/\1/p' "$b" | head -1)
				pass "$label: HTTP 200, $bytes bytes, declared ${declared:-unchunked}, whole document, $state labelling present"
			fi
		fi
	done
	if [ "$statuses" = ' 200 200' ]; then
		cache_status=$(header_value "$WORK/$slug.2.head" 'Cache-Status')
		note "second request Cache-Status: ${cache_status:-none}"
	fi
done
if [ "$fetched" -eq 0 ]; then
	fail 'nothing was fetched; the origin is not answering'
fi
note "pages fetched: $fetched of $(( $(echo "$PAGES" | wc -w | tr -d ' ') * 2 )) expected (2 requests per page, plus one cache-busting reference each)"

# ---------------------------------------------------------------------------
check '2. A cache hit is byte-identical to the uncacheable reference response'
# The truncating module rewrote the replay, not the stream, so comparing what was
# served is the assertion with the most direct relationship to the defect: it
# fails on the first request after a cache entry exists, with no timing involved.
# The comparison is against the cache-busting reference rather than against the
# first plain request, because two poisoned hits would agree with each other.
for page in $PAGES; do
	slug=$(echo "$page" | tr '/?' '__')
	ref="$WORK/$slug.ref.body"
	if [ ! -f "$ref" ]; then
		fail "$page: the uncacheable reference response is missing, so there is nothing to compare against"
		continue
	fi
	i=0
	compared=0
	while [ "$i" -lt 2 ]; do
		i=$((i + 1))
		if [ -f "$WORK/$slug.$i.body" ]; then
			compared=$((compared + 1))
			if cmp -s "$ref" "$WORK/$slug.$i.body"; then
				pass "$page (request $i): matches the uncacheable reference byte-for-byte ($(byte_count "$ref") bytes)"
			else
				fail "$page (request $i): differs from the uncacheable reference - reference $(byte_count "$ref") bytes, served $(byte_count "$WORK/$slug.$i.body") bytes"
			fi
		else
			fail "$page (request $i): the response is missing, so it cannot be compared"
		fi
	done
	[ "$compared" -eq 2 ] || fail "$page: only $compared of 2 responses could be compared against the reference"
done

# ---------------------------------------------------------------------------
check '3. /healthz answers without the served tree'
health=$(curl -sS -m 15 -D "$WORK/health.head" -o "$WORK/health.body" -w '%{http_code}' "$BASE_URL/healthz" 2>"$WORK/curl.err")
hp=$(header_value "$WORK/health.head" 'Content-Length')
if [ "$health" = '200' ] && [ "$(cat "$WORK/health.body" 2>/dev/null)" = 'ok' ]; then
	pass "/healthz: HTTP 200, body ok (declared ${hp:-unchunked})"
else
	fail "/healthz: expected 200 and body ok, got HTTP $health and $(cat "$WORK/health.body" 2>/dev/null | head -c 40)"
fi

# ---------------------------------------------------------------------------
check '4. The aggregate manifest is served whole'
curl -sS -m 30 -D "$WORK/manifest-ref.head" -o "$WORK/manifest-ref.body" "$BASE_URL$JSON?lolstats-verify=$RUN_ID" 2>"$WORK/curl.err" || true
if [ -s "$WORK/manifest-ref.body" ] && head -c 1 "$WORK/manifest-ref.body" | grep -q '{'; then
	note "cache-busting reference: $(byte_count "$WORK/manifest-ref.body") bytes"
else
	fail "$JSON: the cache-busting reference response is empty or is not JSON, so there is nothing to compare against"
fi
i=0
while [ "$i" -lt 2 ]; do
	i=$((i + 1))
	status=$(curl -sS -m 30 -D "$WORK/manifest.$i.head" -o "$WORK/manifest.$i.body" -w '%{http_code}' "$BASE_URL$JSON" 2>"$WORK/curl.err")
	bytes=$(byte_count "$WORK/manifest.$i.body" 2>/dev/null || echo 0)
	declared=$(header_value "$WORK/manifest.$i.head" 'Content-Length')
	first=$(head -c 1 "$WORK/manifest.$i.body" 2>/dev/null)
	label="$JSON (request $i, $( [ "$i" = 1 ] && echo miss || echo hit))"
	if [ "$status" != '200' ]; then
		fail "$label: HTTP $status"
	elif [ "$bytes" -eq 0 ]; then
		fail "$label: 200 with an empty body"
	elif [ -n "$declared" ] && [ "$declared" != "$bytes" ]; then
		fail "$label: Content-Length $declared but $bytes bytes were delivered"
	elif [ ! -f "$WORK/manifest.1.body" ] || [ "$first" != '{' ]; then
		fail "$label: body does not start with { - first byte '$first'"
	else
		pass "$label: HTTP 200, $bytes bytes, declared ${declared:-unchunked}, starts with {"
	fi
done
if [ -f "$WORK/manifest-ref.body" ] && cmp -s "$WORK/manifest-ref.body" "$WORK/manifest.1.body" && cmp -s "$WORK/manifest-ref.body" "$WORK/manifest.2.body"; then
	pass "$JSON: both requests match the uncacheable reference byte-for-byte"
else
	fail "$JSON: a served response differs from the uncacheable reference ($(byte_count "$WORK/manifest-ref.body" 2>/dev/null) vs $(byte_count "$WORK/manifest.1.body" 2>/dev/null) vs $(byte_count "$WORK/manifest.2.body" 2>/dev/null) bytes)"
fi

# ---------------------------------------------------------------------------
printf '\n'
if [ "$failures" -eq 0 ]; then
	printf 'served-response check passed\n'
	exit 0
fi
printf 'served-response check failed: %s failing check(s)\n' "$failures"
exit 1
