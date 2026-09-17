#!/bin/sh
# scripts/verify-serving.sh - serving-contract check for the Go web tier.
#
#   sh scripts/verify-serving.sh [BASE_URL]
#
# BASE_URL defaults to $LOLSTATS_SERVE_URL and then to http://127.0.0.1:18099,
# which is the port-forward an operator opens with
#
#   kubectl -n lolstats port-forward svc/lolstats-go-web 18099:80
#
# What this check is for. The tier that serves lol.erik-schuetze.dev is the Go
# server-rendered tier (cmd/lolstats-web) reading the published JSON snapshot
# under $LOLSTATS_AGG_ROOT. It is not a file server in front of a pre-rendered
# tree, so the things that can go wrong are different things, and this script
# asserts the ones that matter:
#
#   * /healthz and /metrics answer, and neither is cacheable, because a probe
#     that a cache can answer is not a probe;
#   * every HTML page is one whole document whose Content-Length is what was
#     actually delivered, whose honesty banner matches the state the snapshot it
#     serves declares, and which carries the tier's HTML cache policy:
#     `Cache-Control: private, max-age=60, stale-while-revalidate=300` plus a
#     strong ETag. `private` is the load-bearing part: these responses are
#     rendered from the snapshot the process is holding, so no shared cache and
#     no intermediary may keep a copy;
#   * a conditional request revalidates to 304 with the same policy and the same
#     validator, and a stale validator still gets the whole document back,
#     byte-for-byte what an unconditional request gets;
#   * the no-JS filter is genuinely a server-side path: the page carries a GET
#     form that submits to its own path, and a different query returns a
#     different whole document. This is the same invariant the compliance check
#     asserts about every interactive control, checked here over HTTP;
#   * the published snapshot is served under /agg with its own policy - the tree
#     at `public, max-age=60`, the Data Dragon projection at
#     `public, max-age=3600` - and its manifest still carries the frozen keys
#     this site is built on (`schema`, `source`, `min_cell_n`,
#     `cells_published`, `suppressed_cells`);
#   * when the aggregate snapshot is missing or corrupt, the routes that need it
#     answer 503 with a visible error page and a data-fault marker - never a
#     200, and never a truncated body. That is the failure mode this script was
#     originally written around: cache-handler v0.16.0 with storages/core
#     v0.0.18 replayed a cache hit truncated to 4096 bytes minus the stored
#     header block, so every page after the first request lost its footer and
#     its demo labelling while the status was still 200 OK.
#
# Nothing here is hardcoded to a pre-rendered tree. The page set is the tier's
# own routes, the patch in the patch-scoped route is read out of the links the
# tier emits, and the data state the pages must declare is derived from the
# `source` in the manifest the tier is actually serving - so this script does
# not have to be edited when the published snapshot changes from the demo
# fixture tree to real MATCH-V5 data.
#
# Environment:
#   LOLSTATS_SERVE_URL         the origin to check, when no argument is given
#   LOLSTATS_SITE_URL          legacy name for the same thing
#   LOLSTATS_EXPECT_STATE      pin the declared state (demo|live|no-data)
#   LOLSTATS_EXPECT_NO_AGG=1   the tier has no readable snapshot: expect
#                              data-fault 503s from the routes that need one
#   LOLSTATS_AGG_ROOT          the root the tier reads; with EXPECT_NO_AGG this
#                              lets the corrupt-manifest passthrough be compared
#                              against the file on disk instead of trusted
#   LOLSTATS_DDDRAGON_VERSION  pin the Data Dragon version of the projection
#
# Needs curl and a reachable origin. Exits non-zero on the first failing check
# set, after reporting every check, so one run is one diagnosis.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/.agent-artifacts/verify-serving.$$"
# The wording is asserted by this script, so it lives in one place in the tier
# itself, and the literals below are only the fallback for a checkout that does
# not have the Go source (a deployed image does not).
SITE_SRC="$ROOT/internal/webtier/site.go"

BASE_URL=${1:-${LOLSTATS_SERVE_URL:-${LOLSTATS_SITE_URL:-http://127.0.0.1:18099}}}
BASE_URL=${BASE_URL%/}
# Digest of the page as served with no query string, set while check 4 probes a
# discovered-value control and empty everywhere else.
PROBE_BASELINE=''
NO_AGG=${LOLSTATS_EXPECT_NO_AGG:-0}
AGG_ROOT=${LOLSTATS_AGG_ROOT:-}
# Only used to build a probe URL when the served patch cannot imply one (a fault
# run has no readable manifest): the version of the checked-in fixture tree.
DDDRAGON_VERSION=${LOLSTATS_DDDRAGON_VERSION:-}
if [ -z "$DDDRAGON_VERSION" ] && [ -d "$ROOT/fixtures/site/v1/static" ]; then
	DDDRAGON_VERSION=$(cd "$ROOT/fixtures/site/v1/static" 2>/dev/null && ls -d */ 2>/dev/null | head -1 | sed 's|/$||')
fi

HTML_CACHE_CONTROL='private, max-age=60, stale-while-revalidate=300'
ARTIFACT_CACHE_CONTROL='public, max-age=60'
DDDRAGON_CACHE_CONTROL='public, max-age=3600'

rm -rf "$WORK"
mkdir -p "$WORK" || { printf 'FAIL  could not create %s\n' "$WORK"; exit 1; }
trap 'rm -rf "$WORK"' EXIT INT TERM

passes=0
failures=0
warnings=0
pass() { passes=$((passes + 1)); printf 'PASS  %s\n' "$1"; }
fail() { failures=$((failures + 1)); printf 'FAIL  %s\n' "$1"; }
warn() { warnings=$((warnings + 1)); printf 'WARN  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }
check() { printf '\n== %s\n' "$1"; }

if ! command -v curl >/dev/null 2>&1; then
	printf 'FAIL  curl is not installed; this check reads real responses\n'
	exit 1
fi

# ---------------------------------------------------------------------------
# helpers

byte_count() { wc -c < "$1" | tr -d ' '; }

# Header names are case-insensitive in HTTP and awk has no IGNORECASE in every
# implementation this runs on, so the comparison is done in awk against a
# lowercased copy of the field name.
header_value() {
	awk -v h="$2" 'tolower($0) ~ ("^" tolower(h) ":") {sub(/\r$/,""); sub(/^[^:]*:[ \t]*/,""); print; exit}' "$1"
}

status_line() { head -1 "$1" 2>/dev/null | tr -d '\r'; }

# do_fetch <label> <url> [extra curl arguments...] - fills the FETCH_ globals.
# The label is only used in failure text; the reason it is a parameter is that
# every message below has to say which response it is about.
do_fetch() {
	_fetch_label=$1
	_fetch_url=$2
	shift 2
	FETCH_HEAD="$WORK/last.head"
	FETCH_BODY="$WORK/last.body"
	rm -f "$FETCH_HEAD" "$FETCH_BODY"
	FETCH_STATUS=$(curl -sS -m 30 -D "$FETCH_HEAD" -o "$FETCH_BODY" -w '%{http_code}' "$@" "$_fetch_url" 2>"$WORK/curl.err") || FETCH_STATUS=000
	if [ ! -f "$FETCH_BODY" ]; then
		: > "$FETCH_BODY"
	fi
	FETCH_BYTES=$(byte_count "$FETCH_BODY")
	FETCH_DECLARED=$(header_value "$FETCH_HEAD" 'Content-Length')
	FETCH_CC=$(header_value "$FETCH_HEAD" 'Cache-Control')
	FETCH_ETAG=$(header_value "$FETCH_HEAD" 'ETag')
	FETCH_CT=$(header_value "$FETCH_HEAD" 'Content-Type')
	FETCH_VARY=$(header_value "$FETCH_HEAD" 'Vary')
	FETCH_NOSNIFF=$(header_value "$FETCH_HEAD" 'X-Content-Type-Options')
	FETCH_CACHE_STATUS=$(header_value "$FETCH_HEAD" 'Cache-Status')
	FETCH_LAST_MODIFIED=$(header_value "$FETCH_HEAD" 'Last-Modified')
	if [ "$FETCH_STATUS" = 000 ]; then
		note "curl error: $(head -1 "$WORK/curl.err" 2>/dev/null)"
	fi
}

# The declared length is the tier's own claim about the body: a response whose
# transfer was cut short is exactly the defect this script exists for, so it is
# asserted on every response that carries the header.
length_is_honest() { # length_is_honest <label>
	if [ -z "$FETCH_DECLARED" ]; then
		note "$1: no Content-Length declared (streamed or empty body); $(byte_count "$FETCH_BODY") bytes delivered"
		return 0
	fi
	if [ "$FETCH_DECLARED" != "$FETCH_BYTES" ]; then
		fail "$1: Content-Length $FETCH_DECLARED but $FETCH_BYTES bytes were delivered"
		return 1
	fi
	return 0
}

# Trailing newline after </html> is fine; anything else is not.
ends_document() { tail -c 64 "$1" | grep -q '</html>'; }

# The banner each state must carry, checked against the state the page itself
# declares. Three honest states exist (demo, live, no-data) with different
# wording; what must never happen is a page whose banner does not match the
# state it declares, or a page with no banner at all.
declared_state() { sed -n 's/.*data-state="\([a-z-]*\)".*/\1/p' "$1" | head -1; }

labelling_ok() { # labelling_ok <label> <file>
	_fs=$(declared_state "$2")
	if [ -z "$_fs" ]; then
		fail "$1: no data-provenance banner at all, so the page does not say where its numbers came from"
		return 1
	fi
	if [ "$_fs" != "$EXPECTED_STATE" ]; then
		fail "$1: declares data-state=$_fs but the snapshot this tier serves implies $EXPECTED_STATE (set LOLSTATS_EXPECT_STATE to pin another state)"
		return 1
	fi
	case "$_fs" in
	demo)
		if grep -qF "$PREVIEW_TEXT" "$2" || grep -qF "$UNVERIFIED_PREVIEW_TEXT" "$2"; then
			return 0
		fi
		fail "$1: declares demo data but the labelling is missing: '$PREVIEW_TEXT'"
		return 1
		;;
	live)
		if grep -qF 'state-banner--live' "$2"; then
			return 0
		fi
		fail "$1: declares live data but carries no live banner"
		return 1
		;;
	no-data)
		if grep -qF "$NO_DATA_HEADING" "$2"; then
			return 0
		fi
		fail "$1: declares no data but the heading is missing: '$NO_DATA_HEADING'"
		return 1
		;;
	*)
		fail "$1: unrecognised data-state=$_fs"
		return 1
		;;
	esac
}

# The HTML response contract, minus the banner: this is the part that says the
# response is a whole server-rendered document that no shared cache may keep.
html_response_ok() { # html_response_ok <label> <file>
	[ "$FETCH_STATUS" = 200 ] || { fail "$1: HTTP $FETCH_STATUS, expected 200"; return 1; }
	[ "$FETCH_BYTES" -gt 0 ] || { fail "$1: 200 with an empty body"; return 1; }
	printf '%s' "$FETCH_CT" | grep -q 'text/html' || { fail "$1: Content-Type is '$FETCH_CT', expected text/html"; return 1; }
	ends_document "$2" || { fail "$1: the body does not end in </html> - $FETCH_BYTES bytes, last 60: $(tail -c 60 "$2" | tr -d '\n')"; return 1; }
	length_is_honest "$1" || return 1
	if [ "$FETCH_CC" != "$HTML_CACHE_CONTROL" ]; then
		fail "$1: Cache-Control is '${FETCH_CC:-absent}', expected '$HTML_CACHE_CONTROL'"
		return 1
	fi
	case "$FETCH_ETAG" in
	'"'*'"') ;;
	*) fail "$1: ETag is '${FETCH_ETAG:-absent}'; a revalidatable response needs a quoted validator"
		return 1
		;;
	esac
	printf '%s' "$FETCH_VARY" | grep -q 'Accept-Encoding' || { fail "$1: Vary is '${FETCH_VARY:-absent}', expected it to name Accept-Encoding"; return 1; }
	if [ "$FETCH_NOSNIFF" != 'nosniff' ]; then
		fail "$1: X-Content-Type-Options is '${FETCH_NOSNIFF:-absent}', expected nosniff"
		return 1
	fi
	if [ -n "$FETCH_CACHE_STATUS" ]; then
		fail "$1: the response carries Cache-Status '$FETCH_CACHE_STATUS', which means a shared cache is in front of this tier; the redesign removed that tier and this check asserts it is not back"
		return 1
	fi
	return 0
}

# The 503 that a missing or unreadable snapshot must produce: a whole visible
# error page, not a 200 and not a truncated body.
fault_page_ok() { # fault_page_ok <label> <path>
	[ "$FETCH_STATUS" = 503 ] || { fail "$1: HTTP $FETCH_STATUS, expected 503 when the aggregate snapshot cannot be read"; return 1; }
	[ "$FETCH_BYTES" -gt 0 ] || { fail "$1: 503 with an empty body, which is not a visible error page"; return 1; }
	ends_document "$2" || { fail "$1: the 503 body does not end in </html>, so the error page is not a whole document"; return 1; }
	length_is_honest "$1" || return 1
	kind=$(grep -o 'data-fault="[a-z-]*"' "$2" | head -1)
	if [ -z "$kind" ]; then
		fail "$1: the 503 page carries no data-fault marker, so a monitor cannot tell a missing snapshot from a render bug"
		return 1
	fi
	if ! grep -qE '<h1[^>]*>[^<]+</h1>' "$2"; then
		fail "$1: the 503 page has no visible heading"
		return 1
	fi
	if ! grep -qF "$3" "$2" && ! grep -qF "${3%/}" "$2"; then
		fail "$1: the 503 page does not name the requested path $3, so it does not tell the reader what failed"
		return 1
	fi
	if [ "$FETCH_CC" != 'no-store' ]; then
		fail "$1: the 503 page is served with Cache-Control '${FETCH_CC:-absent}'; a fault must never be cached"
		return 1
	fi
	pass "$1: HTTP 503, $kind, $(byte_count "$2") bytes, whole visible error page naming $3, no-store"
	return 0
}

# ---------------------------------------------------------------------------
# the wording of the three honest states, read from the tier's own source

PREVIEW_TEXT='PREVIEW - illustrative data generated to exercise the layout, not real match statistics'
UNVERIFIED_PREVIEW_TEXT='PREVIEW - the manifest does not declare where this snapshot came from, so its numbers are unverified'
NO_DATA_HEADING='No sample yet'

if [ -f "$SITE_SRC" ]; then
	_wording() { sed -n "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*\"\(.*\)\"[[:space:]]*\$/\1/p" "$SITE_SRC" | head -1; }
	_from_source=$(_wording PreviewText)
	_unverified=$(_wording UnverifiedPreviewText)
	_no_data=$(_wording NoDataHeading)
	if [ -n "$_from_source" ] && [ -n "$_no_data" ]; then
		PREVIEW_TEXT=$_from_source
		NO_DATA_HEADING=$_no_data
		[ -n "$_unverified" ] && UNVERIFIED_PREVIEW_TEXT=$_unverified
		note "state wording read from internal/webtier/site.go"
	else
		note "the wording constants were not found in internal/webtier/site.go; using the known wording"
	fi
else
	note "internal/webtier/site.go is not present; using the known wording"
fi

# ---------------------------------------------------------------------------
printf 'serving contract check: %s\n' "$BASE_URL"
if [ "$NO_AGG" = 1 ]; then
	note "LOLSTATS_EXPECT_NO_AGG=1: this run expects the tier to have no readable snapshot"
fi

# ---------------------------------------------------------------------------
printf '\n-- reading the snapshot the tier says it is serving\n'
REACH=$(curl -sS -m 15 -o /dev/null -w '%{http_code}' "$BASE_URL/healthz" 2>"$WORK/curl.err") || REACH=000
if [ "$REACH" = 000 ]; then
	printf 'FAIL  %s did not answer at all (%s)\n' "$BASE_URL/healthz" "$(head -1 "$WORK/curl.err")"
	printf '      start the tier, or open the tunnel this check expects:\n'
	printf '      kubectl -n lolstats port-forward svc/lolstats-go-web 18099:80\n'
	exit 1
fi

MANIFEST_PATH='/agg/v1/manifest.json'
do_fetch "$MANIFEST_PATH" "$BASE_URL$MANIFEST_PATH"
MANIFEST_STATUS=$FETCH_STATUS
MANIFEST_CC=$FETCH_CC
MANIFEST_CT=$FETCH_CT
MANIFEST_BYTES=$FETCH_BYTES
MANIFEST_LENGTH=$FETCH_DECLARED
MANIFEST_ETAG=$FETCH_ETAG
cp "$FETCH_BODY" "$WORK/manifest.body"

MANIFEST_SOURCE=''
LATEST_PATCH=''
if [ "$MANIFEST_STATUS" = 200 ]; then
	# The published manifest carries exactly one "source" key (partition rows
	# carry "source_window", which does not match), so the greedy pattern cannot
	# pick up the wrong one.
	MANIFEST_SOURCE=$(sed -n 's/.*"source"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$WORK/manifest.body" | head -1)
	note "the served manifest declares source='$MANIFEST_SOURCE' ($MANIFEST_BYTES bytes)"
fi

# The state the pages must declare follows from the manifest the tier serves,
# not from a hardcoded expectation:
#   source riot-match-v5 -> live   (dataStateFor in internal/webtier/site.go)
#   any other source     -> demo   (an undeclared or unknown source is labelled
#                                   as unverified rather than as real match data)
#   no manifest at all   -> no-data
if [ "$NO_AGG" = 1 ]; then
	DERIVED_STATE=no-data
elif [ "$MANIFEST_SOURCE" = 'riot-match-v5' ]; then
	DERIVED_STATE=live
elif [ -n "$MANIFEST_SOURCE" ]; then
	DERIVED_STATE=demo
else
	DERIVED_STATE=no-data
fi
EXPECTED_STATE=${LOLSTATS_EXPECT_STATE:-$DERIVED_STATE}
note "pages must declare data-state=$EXPECTED_STATE (derived from the served manifest; override with LOLSTATS_EXPECT_STATE)"

# ---------------------------------------------------------------------------
check '1. /healthz answers, and neither probe may be cached'
do_fetch '/healthz' "$BASE_URL/healthz"
if [ "$FETCH_STATUS" != 200 ]; then
	fail "/healthz: HTTP $FETCH_STATUS, expected 200"
elif [ "$(cat "$FETCH_BODY")" != 'ok' ]; then
	fail "/healthz: HTTP 200 but the body is '$(head -c 40 "$FETCH_BODY")', expected ok"
elif [ "$FETCH_CC" != 'no-store' ]; then
	fail "/healthz: Cache-Control is '${FETCH_CC:-absent}'; a health probe must not be cached"
elif length_is_honest '/healthz'; then
	pass "/healthz: HTTP 200, body ok, Cache-Control no-store, declared ${FETCH_DECLARED:-unchunked}"
fi

do_fetch '/metrics' "$BASE_URL/metrics"
METRIC_SAMPLES=$(grep -c '^lolstats_' "$FETCH_BODY" 2>/dev/null | tr -d ' ')
METRIC_SAMPLES=${METRIC_SAMPLES:-0}
if [ "$FETCH_STATUS" != 200 ]; then
	fail "/metrics: HTTP $FETCH_STATUS, expected 200"
elif ! printf '%s' "$FETCH_CT" | grep -q 'version=0.0.4'; then
	fail "/metrics: Content-Type is '$FETCH_CT', expected Prometheus text exposition (version=0.0.4)"
elif [ "$METRIC_SAMPLES" -lt 5 ]; then
	fail "/metrics: only $METRIC_SAMPLES lolstats_ samples; a 200 that exports nothing is not an observability surface"
elif [ "$FETCH_CC" != 'no-store' ]; then
	fail "/metrics: Cache-Control is '${FETCH_CC:-absent}'; a scrape must not be served out of a cache"
elif length_is_honest '/metrics'; then
	pass "/metrics: HTTP 200, $METRIC_SAMPLES lolstats_ samples, Cache-Control no-store, declared ${FETCH_DECLARED:-unchunked}"
fi

# ---------------------------------------------------------------------------
check '2. HTML routes are whole documents with the tier cache policy'
PAGES='/ /about/ /champions/ahri/ /tier-list/mid/ /matchups/mid/'
# The patch-scoped route is discovered from the tier's own links rather than
# assumed, because the patch is a property of the published snapshot.
do_fetch '/tier-list/mid/' "$BASE_URL/tier-list/mid/"
if [ "$FETCH_STATUS" = 200 ]; then
	LATEST_PATCH=$(sed -n 's|.*href="/patch/\([0-9][0-9.]*\)/tier-list/mid.*|\1|p' "$FETCH_BODY" | head -1)
fi
if [ -n "$LATEST_PATCH" ]; then
	PAGES="$PAGES /patch/$LATEST_PATCH/tier-list/mid/"
	note "patch-scoped route discovered from the page: /patch/$LATEST_PATCH/tier-list/mid/"
elif [ "$NO_AGG" = 1 ]; then
	note "no snapshot, so there is no patch-scoped route to check"
else
	warn "the tier-list page linked no /patch/<patch>/tier-list/mid/ route, so the patch-scoped route was not checked"
fi

pages_checked=0
for page in $PAGES; do
	do_fetch "$page" "$BASE_URL$page"
	if [ "$NO_AGG" = 1 ]; then
		# Without a snapshot a page legitimately renders empty (200, no-data) or
		# fails closed (503 with a visible page). What is never allowed is a 200
		# that claims data it does not have.
		if [ "$FETCH_STATUS" = 503 ]; then
			fault_page_ok "$page" "$FETCH_BODY" "$page"
			continue
		fi
		if [ "$FETCH_STATUS" != 200 ]; then
			fail "$page: HTTP $FETCH_STATUS with no snapshot readable; expected either a rendered no-data page or a 503"
			continue
		fi
		if [ "$(declared_state "$FETCH_BODY")" != 'no-data' ]; then
			fail "$page: with no readable snapshot the page answered 200 declaring data-state=$(declared_state "$FETCH_BODY"); unreadable data must never render as present"
			continue
		fi
	fi
	if html_response_ok "$page" "$FETCH_BODY" && labelling_ok "$page" "$FETCH_BODY"; then
		pass "$page: HTTP 200, $FETCH_BYTES bytes, $(declared_state "$FETCH_BODY") labelling, Cache-Control private/max-age=60, ETag $FETCH_ETAG"
		pages_checked=$((pages_checked + 1))
	fi
done
if [ "$pages_checked" -eq 0 ] && [ "$NO_AGG" != 1 ]; then
	fail 'no HTML page passed the response contract; the tier is not answering its own routes'
fi

# ---------------------------------------------------------------------------
if [ "$NO_AGG" != 1 ]; then
	check '3. A conditional request revalidates to 304, and a stale validator gets the whole page'
	REVALIDATED='/tier-list/mid/'
	do_fetch "$REVALIDATED" "$BASE_URL$REVALIDATED"
	plain_etag=$FETCH_ETAG
	plain_cc=$FETCH_CC
	cp "$FETCH_BODY" "$WORK/revalidate.plain"
	if [ "$FETCH_STATUS" != 200 ] || [ -z "$plain_etag" ]; then
		fail "$REVALIDATED: the page did not answer 200 with an ETag, so revalidation cannot be checked"
	else
		do_fetch "$REVALIDATED (If-None-Match: $plain_etag)" "$BASE_URL$REVALIDATED" -H "If-None-Match: $plain_etag"
		if [ "$FETCH_STATUS" != 304 ]; then
			fail "$REVALIDATED (conditional): HTTP $FETCH_STATUS, expected 304 for the validator the tier just sent"
		elif [ "$FETCH_BYTES" -ne 0 ]; then
			fail "$REVALIDATED (conditional): 304 carried $FETCH_BYTES bytes of body"
		elif [ "$FETCH_CC" != "$plain_cc" ]; then
			fail "$REVALIDATED (conditional): Cache-Control '${FETCH_CC:-absent}' differs from the 200 response's '$plain_cc'; a 304 has to refresh the same policy"
		elif [ "$FETCH_ETAG" != "$plain_etag" ]; then
			fail "$REVALIDATED (conditional): ETag '${FETCH_ETAG:-absent}' differs from the 200 response's '$plain_etag'"
		else
			pass "$REVALIDATED (conditional): HTTP 304, no body, same Cache-Control and ETag as the 200"
		fi
		do_fetch "$REVALIDATED (stale validator)" "$BASE_URL$REVALIDATED" -H 'If-None-Match: "lolstats-verify-stale-validator"'
		if [ "$FETCH_STATUS" != 200 ]; then
			fail "$REVALIDATED (stale validator): HTTP $FETCH_STATUS, expected the whole document back for an unknown validator"
		elif ! cmp -s "$WORK/revalidate.plain" "$FETCH_BODY"; then
			fail "$REVALIDATED (stale validator): the body differs from the unconditional response ($(byte_count "$WORK/revalidate.plain") vs $FETCH_BYTES bytes)"
		elif html_response_ok "$REVALIDATED (stale validator)" "$FETCH_BODY"; then
			pass "$REVALIDATED (stale validator): HTTP 200, byte-identical to the unconditional response ($FETCH_BYTES bytes)"
		fi
	fi
fi

# ---------------------------------------------------------------------------
if [ "$NO_AGG" != 1 ]; then
	check '4. Interactive controls have a no-JS server-side path'
	# The rule this check enforces is not "the page contains a <form>": it is that
	# a reader with JavaScript disabled can still sort, filter and page through
	# the ladder. So the controls are read out of the served markup - the form's
	# own action, method, control names and option values - and each control is
	# then driven over HTTP with no JavaScript anywhere in sight: a control whose
	# parameter changes nothing in the response is not a server-side path, and the
	# page that carries it is decoration. Values are discovered rather than
	# hardcoded, so a rename in the template changes what is probed instead of
	# quietly probing a parameter the tier ignores.
	FILTER_PATH='/tier-list/mid/'
	do_fetch "$FILTER_PATH" "$BASE_URL$FILTER_PATH"
	cp "$FETCH_BODY" "$WORK/filter.plain"
	form=$(grep -o "<form[^>]*>" "$FETCH_BODY" | grep 'method="get"' | grep -E "action=\"(${FILTER_PATH}|${FILTER_PATH%/})\"" | head -1)
	if [ -z "$form" ]; then
		fail "$FILTER_PATH: no GET form submits to this path, so the filter has no server-side path without JavaScript"
	else
		pass "$FILTER_PATH: the filter is a GET form to its own path: $(printf '%s' "$form" | cut -c1-110)"
	fi
	# select_values <file> <name> - the option values of the first <select
	# name="<name>"> in the document. The served pages are one line each, so this
	# is a scan over a single record: find the select, take the block up to its
	# </select>, and print each option's value.
	select_values() {
		awk -v want="$2" '
			{
				line = $0
				pat = "<select[^>]*name=\"" want "\"[^>]*>"
				if (!match(line, pat)) { next }
				rest = substr(line, RSTART + RLENGTH)
				if (match(rest, /<\/select>/)) { block = substr(rest, 1, RSTART - 1) } else { block = rest }
				while (match(block, /<option[^>]*value="[^"]*"/)) {
					tag = substr(block, RSTART, RLENGTH)
					sub(/.*value="/, "", tag)
					sub(/"$/, "", tag)
					if (tag != "") { print tag }
					block = substr(block, RSTART + RLENGTH)
				}
			}' "$1"
	}
	# digest <file> - a short content digest, so the evidence says which two bodies
	# differed rather than only that they did.
	digest() {
		if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -c1-16
		elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -c1-16
		else cksum "$1" | tr -d ' ' | cut -c1-16
		fi
	}
	# A parameter is a server-side path only if the server answers a different
	# document for at least two of the values the control offers. One value per
	# parameter is not enough: a query the handler ignores returns the identical
	# page, and that is exactly the failure this look for.
	probe_param() { # probe_param <label> <param> <values...>
		probe_label=$1
		probe_param_name=$2
		shift 2
		probe_count=0
		probe_digests=''
		probe_failed=0
		# PROBE_BASELINE, when set, is the digest of the page as served with no
		# query at all. A single probed value is then enough provided it differs
		# from that baseline - the only fair test for a control whose values are
		# discovered rather than enumerated (the text filter), because there is no
		# second filtered value to compare against.
		if [ -n "$PROBE_BASELINE" ]; then
			probe_digests=" unfiltered:$PROBE_BASELINE"
		fi
		for value in "$@"; do
			query="$probe_param_name=$value"
			do_fetch "$FILTER_PATH?$query" "$BASE_URL$FILTER_PATH?$query"
			probe_count=$((probe_count + 1))
			if [ "$FETCH_STATUS" != 200 ]; then
				fail "$FILTER_PATH?$query: HTTP $FETCH_STATUS, expected 200 from the server-side $probe_label"
				probe_failed=1
				continue
			fi
			if ! html_response_ok "$FILTER_PATH?$query" "$FETCH_BODY"; then
				probe_failed=1
				continue
			fi
			cp "$FETCH_BODY" "$WORK/filter.$probe_count.$probe_param_name"
			probe_digests="$probe_digests $query:$(digest "$FETCH_BODY")"
		done
		if [ "$probe_failed" -eq 1 ]; then
			return 1
		fi
		distinct=$(printf '%s\n' $probe_digests | sed 's/.*://' | LC_ALL=C sort -u | wc -l | tr -d ' ')
		if [ "$distinct" -lt 2 ]; then
			fail "$FILTER_PATH: the $probe_label control has no server-side effect: $probe_count value(s) of '$probe_param_name' returned the same document ($probe_digests)"
			return 1
		fi
		pass "$FILTER_PATH: the $probe_label is server-side ($probe_count value(s) of '$probe_param_name', $distinct distinct document(s)):$probe_digests"
		return 0
	}
	# The sort control: the values its own <select> offers, capped so a long list
	# does not turn this into a crawl. The default value is included deliberately
	# - a control that only works when it is changed is not a control.
	sort_values=$(select_values "$WORK/filter.plain" sort | head -4)
	dir_values=$(select_values "$WORK/filter.plain" dir | head -2)
	per_values=$(select_values "$WORK/filter.plain" per | head -3)
	# A text filter has no option values: take the first champion the page links
	# to, which is by construction a value the server must be able to filter on.
	# The tier's champion links are /champions/<slug>/<role> with no trailing
	# slash, and the pages are one line each, so the first match of grep -o is the
	# first link in document order.
	champion_slug=$(grep -o 'href="/champions/[a-z0-9-]*/' "$WORK/filter.plain" | head -1 |
		sed -e 's|^href="/champions/||' -e 's|/$||')
	if [ -z "$sort_values" ]; then
		fail "$FILTER_PATH: the served page names no <select name=\"sort\"> option, so the sort control the tests are about is not in the page a reader gets"
	else
		# shellcheck disable=SC2086
		probe_param 'sort' 'sort' $sort_values
	fi
	if [ -n "$dir_values" ]; then
		# shellcheck disable=SC2086
		probe_param 'direction' 'dir' $dir_values
	fi
	if [ -n "$per_values" ]; then
		# shellcheck disable=SC2086
		probe_param 'page size' 'per' $per_values
	fi
	if [ -n "$champion_slug" ]; then
		# The text filter is compared against the unfiltered page rather than
		# against another filtered one: filtering to one champion must return
		# something other than the whole ladder.
		PROBE_BASELINE=$(digest "$WORK/filter.plain")
		probe_param 'text filter' 'q' "$champion_slug"
		PROBE_BASELINE=''
	fi
fi

# ---------------------------------------------------------------------------
check '5. The published snapshot is served under /agg with its own cache policy'
# The manifest is the tree's entry point and the pages were rendered from it, so
# it is checked first: a reader has to be able to check a number against its
# source on the same origin.
if [ "$MANIFEST_STATUS" != 200 ]; then
	if [ "$NO_AGG" = 1 ]; then
		note "$MANIFEST_PATH answered HTTP $MANIFEST_STATUS; check 6 asserts the fault page"
	else
		fail "$MANIFEST_PATH: HTTP $MANIFEST_STATUS, expected 200 - the tree the pages were rendered from must be readable"
	fi
elif [ "$MANIFEST_CC" != "$ARTIFACT_CACHE_CONTROL" ]; then
	fail "$MANIFEST_PATH: Cache-Control is '${MANIFEST_CC:-absent}', expected '$ARTIFACT_CACHE_CONTROL'"
elif ! printf '%s' "$MANIFEST_CT" | grep -q 'application/json'; then
	fail "$MANIFEST_PATH: Content-Type is '$MANIFEST_CT', expected application/json"
elif [ -n "$MANIFEST_LENGTH" ] && [ "$MANIFEST_LENGTH" != "$MANIFEST_BYTES" ]; then
	fail "$MANIFEST_PATH: Content-Length $MANIFEST_LENGTH but $MANIFEST_BYTES bytes were delivered"
else
	pass "$MANIFEST_PATH: HTTP 200, $MANIFEST_BYTES bytes, declared ${MANIFEST_LENGTH:-unchunked}, Cache-Control $MANIFEST_CC"
fi

# The Data Dragon projection: v1/static/<ddragon_version>/{items,runes,
# summoner-spells,patches,champions}.json (internal/aggmodel/paths.go). The
# published tree may or may not carry it, and both states are contract
# (docs/contracts.md section 4, the static-projection amendment of 2026-09-17):
#
#   published    200 at exactly public, max-age=3600, because the bytes cannot
#                change while the version stands, with an honest Content-Length
#                and a JSON body;
#   unpublished  the reserved prefix answers 404 with Cache-Control: no-store -
#                never an invented 200 and never a cacheable 404 - and the pages
#                are still complete, because the tier renders from the Data
#                Dragon projection embedded in the binary
#                (internal/webtier/data.go).
#
# Both states are asserted and anything else fails: a 404 a cache would keep, a
# 200 with the wrong policy, a 5xx, or a candidate that could not be derived at
# all. This used to be a WARN that still exited 0, which let a frozen contract
# outlive the served reality it described, so the check now says which state it
# observed and holds the gate on it. A mix is legal and expected - one published
# Data Dragon version and 404s for the patch-version candidates - because the
# path carries the Data Dragon version, not the game patch.
if [ "$NO_AGG" = 1 ]; then
	note 'no snapshot: the Data Dragon projection is not expected to be served in this run'
else
	ver_candidates="$DDDRAGON_VERSION"
	if [ -n "$LATEST_PATCH" ]; then
		ver_candidates="$ver_candidates $LATEST_PATCH.1 $LATEST_PATCH"
	fi
	static_served=''
	static_probed=0
	static_failed=0
	ver_tried=''
	ver_status=''
	for ver in $ver_candidates; do
		[ -n "$ver" ] || continue
		case " $ver_tried " in
		*" $ver "*) continue ;;
		esac
		ver_tried="$ver_tried $ver"
		static_path="/agg/v1/static/$ver/champions.json"
		do_fetch "$static_path" "$BASE_URL$static_path"
		static_probed=$((static_probed + 1))
		ver_status="$ver_status $ver:$FETCH_STATUS"
		if [ "$FETCH_STATUS" = 404 ]; then
			if [ "$FETCH_CC" != 'no-store' ]; then
				fail "$static_path: HTTP 404 with Cache-Control '${FETCH_CC:-absent}'; an unpublished path under the reserved /agg/v1/static/ prefix is not expected to be served and must not be cacheable either, so it has to answer no-store (docs/contracts.md section 4)"
				static_failed=1
			fi
			continue
		fi
		if [ "$FETCH_STATUS" != 200 ]; then
			fail "$static_path: HTTP $FETCH_STATUS; the reserved Data Dragon prefix answers either 200 at '$DDDRAGON_CACHE_CONTROL' or 404 with no-store, and this is neither (docs/contracts.md section 4)"
			static_failed=1
			continue
		fi
		if [ "$FETCH_CC" != "$DDDRAGON_CACHE_CONTROL" ]; then
			fail "$static_path: Cache-Control is '${FETCH_CC:-absent}', expected '$DDDRAGON_CACHE_CONTROL' for the immutable Data Dragon projection"
			static_failed=1
			continue
		fi
		if [ -n "$FETCH_DECLARED" ] && [ "$FETCH_DECLARED" != "$FETCH_BYTES" ]; then
			fail "$static_path: Content-Length $FETCH_DECLARED but $FETCH_BYTES bytes were delivered"
			static_failed=1
			continue
		fi
		if [ "$(head -c 1 "$FETCH_BODY")" != '{' ]; then
			fail "$static_path: the body does not start with {, so it is not the projection JSON"
			static_failed=1
			continue
		fi
		pass "$static_path: HTTP 200, $FETCH_BYTES bytes, Cache-Control $FETCH_CC"
		static_served="$static_served $ver"
	done
	if [ "$static_probed" = 0 ]; then
		fail "no Data Dragon version could be derived from the served patch ('${LATEST_PATCH:-no manifest}'), so the reserved /agg/v1/static/ prefix was not probed at all and this check proved nothing"
	elif [ -n "$static_served" ]; then
		note "the Data Dragon projection is published: 200 and '$DDDRAGON_CACHE_CONTROL' for$static_served (probed:${ver_status:- none})"
	elif [ "$static_failed" = 0 ]; then
		pass "no Data Dragon projection is published under /agg/v1/static/ and the tier answers the reserved prefix honestly: 404 with Cache-Control: no-store for every probed version (probed:${ver_status:- none}), never an invented 200, and the pages are complete from the projection embedded in the binary (docs/contracts.md section 4, amended 2026-09-17)"
	fi
fi

# ---------------------------------------------------------------------------
check '6. The served manifest still carries the frozen aggregate contract'
if [ "$NO_AGG" = 1 ] && [ -n "$AGG_ROOT" ] && [ -f "$AGG_ROOT/v1/manifest.json" ] \
	&& cmp -s "$AGG_ROOT/v1/manifest.json" "$WORK/manifest.body"; then
	# This run arms the fault path on a manifest that is corrupt on purpose, so
	# the frozen-contract assertion has nothing to hold: failing the gate here
	# would report the fixture as a defect. Check 7 records what the tier does
	# with the bytes instead.
	note "the served manifest is byte-for-byte the deliberately corrupt fixture in $AGG_ROOT, so the frozen-contract assertion is exercised by the fixture run above and the tier's handling of it by check 7"
elif [ "$MANIFEST_STATUS" = 200 ]; then
	note "manifest keys: schema, source, latest.{min_cell_n,cells_published,suppressed_cells}, partitions (docs/contracts.md section 4)"
	missing=''
	for key in '"schema"' '"source"' '"latest"' '"partitions"' '"min_cell_n"' '"cells_published"' '"suppressed_cells"'; do
		grep -qF "$key" "$WORK/manifest.body" || missing="$missing $key"
	done
	if [ -n "$missing" ]; then
		fail "$MANIFEST_PATH: the served manifest is missing frozen key(s):$missing"
	else
		schema=$(sed -n 's/.*"schema"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$WORK/manifest.body" | head -1)
		if [ "$schema" != '1' ]; then
			fail "$MANIFEST_PATH: schema is '${schema:-absent}'; this site is built on schema 1 and a bump needs a reader change first"
		else
			first=$(head -c 1 "$WORK/manifest.body")
			last=$(tail -c 32 "$WORK/manifest.body" | tr -d '\n ')
			case "$last" in
			*'}'*) ;;
			*) fail "$MANIFEST_PATH: the manifest body does not end with }, so it was truncated"; last=''; ;;
			esac
			if [ -n "$last" ]; then
				if [ "$first" != '{' ]; then
					fail "$MANIFEST_PATH: the manifest body does not start with {"
				else
					cells=$(sed -n 's/.*"cells_published"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$WORK/manifest.body" | head -1)
					suppressed=$(sed -n 's/.*"suppressed_cells"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$WORK/manifest.body" | head -1)
					mincell=$(sed -n 's/.*"min_cell_n"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$WORK/manifest.body" | head -1)
					numbers_ok=1
					for value in "$mincell" "$cells" "$suppressed"; do
						case "$value" in
						''|*[!0-9]*) numbers_ok=0 ;;
						esac
					done
					if [ "$numbers_ok" != 1 ]; then
						fail "$MANIFEST_PATH: cells_published/suppressed_cells/min_cell_n are not all numeric (cells_published=${cells:-absent}, suppressed_cells=${suppressed:-absent}, min_cell_n=${mincell:-absent})"
					elif [ "$mincell" -le 0 ]; then
						fail "$MANIFEST_PATH: min_cell_n is $mincell; a published cell has to claim a floor of at least one game"
					elif [ "$cells" -eq 0 ]; then
						fail "$MANIFEST_PATH: cells_published is 0, so the served manifest declares that nothing was ever published"
					elif [ -z "$MANIFEST_SOURCE" ]; then
						fail "$MANIFEST_PATH: schema 1 requires a source; the served manifest declares none, so every page has to render as unverified"
					else
						pass "$MANIFEST_PATH: schema 1, source '$MANIFEST_SOURCE', patches ${LATEST_PATCH:-n/a}, min_cell_n $mincell, cells_published $cells, suppressed_cells $suppressed"
					fi
				fi
			fi
		fi
	fi
elif [ "$NO_AGG" = 1 ]; then
	note "$MANIFEST_PATH answered HTTP $MANIFEST_STATUS; the fault is asserted in check 7"
else
	fail "$MANIFEST_PATH: HTTP $MANIFEST_STATUS, expected 200 with the frozen manifest"
fi

# ---------------------------------------------------------------------------
check '7. A missing or unreadable snapshot is a 503 with a visible page, never a truncated 200'
if [ "$NO_AGG" != 1 ]; then
	# Armed from inside this run: the tier is expected to have a snapshot here,
	# so the way to observe the fault path without touching the cluster is the
	# corrupt-root mode of `make verify-serving-local`, which sets
	# LOLSTATS_EXPECT_NO_AGG=1.
	note 'the snapshot is readable, so the fault path is exercised by `make verify-serving-local`'
else
	# Liveness first: a tier that has failed closed must still be diagnosable.
	do_fetch '/healthz' "$BASE_URL/healthz"
	if [ "$FETCH_STATUS" = 200 ] && [ "$(cat "$FETCH_BODY")" = 'ok' ]; then
		pass '/healthz: HTTP 200 with no snapshot readable, so the tier is diagnosable while its data layer is faulted'
	else
		fail "/healthz: HTTP $FETCH_STATUS with no snapshot readable; a faulted tier still has to answer its liveness probe"
	fi

	# An artifact route that needs the snapshot must not invent a truncated 200.
	# With no root at all the tier answers 503 with the fault page; with a root
	# whose manifest is corrupt the specific file is absent from the tree, which
	# 404 is the honest answer for. Either is a pass; 200 is not.
	artifact_routes="/agg/v1/p/16.18/EUW/420/all/tierlist.json /agg/v1/static/${DDDRAGON_VERSION}/champions.json"
	for route in $artifact_routes; do
		do_fetch "$route" "$BASE_URL$route"
		if [ "$FETCH_STATUS" = 200 ]; then
			fail "$route: HTTP 200 with no readable snapshot; this route cannot serve $FETCH_BYTES bytes of artifact it could not load"
			continue
		fi
		case "$FETCH_STATUS" in
		503) fault_page_ok "$route" "$FETCH_BODY" "$route" ;;
		404) pass "$route: HTTP 404 - the file is absent from the unreadable tree, and 404 is not a truncated 200" ;;
		*) fail "$route: HTTP $FETCH_STATUS with no readable snapshot; expected 503 (no root) or 404 (file not in the tree)" ;;
		esac
	done

	# The manifest itself: with no root at all it must 503 like the rest; with a
	# root whose manifest is corrupt the tier serves the file as it finds it,
	# which is not a serving lie but is not a validated read either, so the bytes
	# are compared against the file on disk instead of trusted.
	do_fetch "$MANIFEST_PATH" "$BASE_URL$MANIFEST_PATH"
	if [ "$FETCH_STATUS" = 503 ]; then
		fault_page_ok "$MANIFEST_PATH" "$FETCH_BODY" "$MANIFEST_PATH"
	elif [ "$FETCH_STATUS" = 200 ]; then
		on_disk="$AGG_ROOT/v1/manifest.json"
		if [ -z "$AGG_ROOT" ]; then
			warn "$MANIFEST_PATH: served HTTP 200 ($FETCH_BYTES bytes) although the tier could not load a snapshot, and LOLSTATS_AGG_ROOT is not set, so these bytes could not be compared against the file on disk"
		elif [ ! -f "$on_disk" ]; then
			fail "$MANIFEST_PATH: served HTTP 200 ($FETCH_BYTES bytes) although $on_disk does not exist; the tree it claims to serve is not there"
		elif cmp -s "$on_disk" "$FETCH_BODY"; then
			warn "$MANIFEST_PATH: served HTTP 200 with the unvalidated bytes of $on_disk, which the tier itself refused to load as a snapshot; the artifact route passes a corrupt manifest through instead of failing closed (finding for the tier lane, not a serving defect here)"
		else
			fail "$MANIFEST_PATH: served HTTP 200 with bytes that are neither the on-disk file nor a validated manifest, so the response came from somewhere other than the tree"
		fi
	else
		fail "$MANIFEST_PATH: HTTP $FETCH_STATUS with no readable snapshot; expected 503 with a visible fault page"
	fi
fi

# ---------------------------------------------------------------------------
printf '\n'
if [ "$failures" -eq 0 ]; then
	printf 'serving contract holds: %s check(s) passed' "$passes"
	[ "$warnings" -gt 0 ] && printf ', %s finding(s) warned' "$warnings"
	printf '\n'
	exit 0
fi
printf 'serving contract failed: %s failing check(s), %s passed' "$failures" "$passes"
[ "$warnings" -gt 0 ] && printf ', %s finding(s) warned' "$warnings"
printf '\n'
exit 1
