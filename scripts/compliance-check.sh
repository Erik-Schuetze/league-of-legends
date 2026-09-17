#!/bin/sh
# scripts/compliance-check.sh - the launch-blocking compliance gate.
#
#   sh scripts/compliance-check.sh
#
# No network, no npm, no package manager: it reads the source tree, the built
# site in web/dist, and the shared wording in web/src/lib/legal.ts. Build the
# site first, because four of the checks are about what the deployment actually
# serves rather than about what the source intends.
#
# It exits non-zero if any launch-blocking check fails, and prints PASS or FAIL
# for each one together with the number of files the scan read. That count is
# not decoration: a check that passes because it scanned nothing is worse than no
# check at all, so every scan asserts that it read something, and the rating scan
# prints the lines it exempted so that a reviewer can see the exemption is a
# negation in prose rather than an absence of hits.
#
# Environment:
#   LOLSTATS_RIOT_VERIFICATION_TOKEN  set -> /riot.txt must be published, unset ->
#                                     no page may claim Riot verification
#   LOLSTATS_SITE_URL                 the deployed address, also read by the build
#   LOLSTATS_CONTACT_EMAIL            the published contact address
#
# Where a check can only be satisfied by a decision that is not the code's to
# make, it says so and keeps going; where the code can violate a prohibition, it
# fails. docs/compliance.md records the trigger each check belongs to.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="$ROOT/web/dist"
LEGAL="$ROOT/web/src/lib/legal.ts"
WORK="$ROOT/.agent-artifacts/compliance-check"

mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT INT TERM

failures=0
pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }
warn() { printf 'WARN  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }
check() { printf '\n== %s\n' "$1"; }

count_lines() { grep -c '' "$1" 2>/dev/null | tr -d ' '; }
count_files() { find "$1" -type f -name '*.html' | grep -c '' | tr -d ' '; }
count_nul() { tr -cd '\0' < "$1" | wc -c | tr -d ' '; }
lower() { tr '[:upper:]' '[:lower:]'; }
# Built pages are one enormous line each, so report matches without the line.
trim() { cut -c1-200; }

# A rating-like value in any language this project uses. The delimiters are
# spelled out rather than \b, because macOS ships BSD grep and portable word
# boundaries differ between GNU and BSD.
RATING_TOKENS='(^|[^A-Za-z0-9_])(mmr|elo|skill[ _-]?rating|matchmaking[ _-]?rating|player[ _-]?rating|hidden[ _-]?rating|rating)([^A-Za-z0-9_]|$)'
# An occurrence is exempt only when a negation word precedes the token on the
# same line, which is what "this site does not compute an MMR" is. The exemption
# is line-local and positional: a line that negates one token and states another
# is reported as a violation, which is the conservative direction.
RATING_NEGATED="(no|not|never|without|forbid|prohibit|none|nothing|neither|avoid).{0,160}${RATING_TOKENS}"
# A rating-like identifier in a declaration position: `mmr:`, `"elo":`, `MMR =`.
QUOTES="[\"']"
RATING_IDENTIFIER="(^|[^A-Za-z0-9_])${QUOTES}?(mmr|elo|skill[_-]?rating|matchmaking[_-]?rating|player[_-]?rating)${QUOTES}?[[:space:]]*[:=]"
# Services that would mean the site tracks its readers.
TRACKERS='google-analytics|googletagmanager|gtag\(|analytics\.google|doubleclick|facebook\.net|fbq\(|hotjar|clarity\.ms|plausible\.io|matomo|piwik|posthog|mixpanel|segment\.(com|io)|sentry\.io|newrelic|cloudflareinsights|umami'

DD_ORIGIN='ddragon.leagueoflegends.com'
DD_ORIGIN_URL='https://ddragon.leagueoflegends.com'
NON_ENDORSEMENT_MARKER='not endorsed by Riot Games'

printf 'compliance gate: %s\n' "$ROOT"
printf 'this is a build of %s\n' "${LOLSTATS_SITE_URL:-an unconfigured address}"

# ---------------------------------------------------------------------------
check '0. The built site is present'
if [ ! -d "$DIST" ]; then
	fail 'web/dist is missing; build it first: (cd web && npm run build)'
	exit 1
fi
htmls=$(count_files "$DIST")
if [ "$htmls" -lt 10 ]; then
	fail "web/dist holds only $htmls HTML pages; build it first: (cd web && npm run build)"
	exit 1
fi
pass "web/dist holds $htmls built HTML pages"

# ---------------------------------------------------------------------------
check '1. No MMR, ELO or rating-like value anywhere (Riot prohibition)'
find "$ROOT" \
	\( -name node_modules -o -name .git -o -name .agent-artifacts -o -name .astro \) -prune -o \
	-type f \( -name '*.go' -o -name '*.sql' -o -name '*.ts' -o -name '*.astro' -o -name '*.json' \
	-o -name '*.mjs' -o -name '*.js' -o -name '*.html' -o -name '*.css' \) -print0 \
	> "$WORK/rating-scan-files.bin" 2>/dev/null
rating_scanned=$(count_nul "$WORK/rating-scan-files.bin")
note "scanned $rating_scanned files: Go, SQL, TS/JS, Astro, JSON, HTML and CSS (node_modules, .git and .agent-artifacts excluded)"
if [ "$rating_scanned" -lt 200 ]; then
	fail "the rating scan read only $rating_scanned files, which is too few to be evidence; the find expression is wrong, not the code clean"
else
	xargs -0 grep -InE "$RATING_TOKENS" < "$WORK/rating-scan-files.bin" > "$WORK/rating-hits.txt" 2>/dev/null || true
	rating_hits=$(count_lines "$WORK/rating-hits.txt")
	if [ "$rating_hits" -eq 0 ]; then
		fail 'the rating scan found no mention of a rating at all, not even the standing prohibition in web/src/lib/legal.ts; the pattern is wrong rather than the code clean'
	else
		grep -iE "$RATING_NEGATED" "$WORK/rating-hits.txt" > "$WORK/rating-exempt.txt" 2>/dev/null || true
		rating_exempt=$(count_lines "$WORK/rating-exempt.txt")
		rating_violations=$((rating_hits - rating_exempt))
		xargs -0 grep -InE "$RATING_IDENTIFIER" < "$WORK/rating-scan-files.bin" > "$WORK/rating-identifiers.txt" 2>/dev/null || true
		rating_ids=$(count_lines "$WORK/rating-identifiers.txt")
		if [ "$rating_violations" -eq 0 ] && [ "$rating_ids" -eq 0 ]; then
			pass "0 violations: $rating_hits lines mention a rating, all $rating_exempt are the standing prohibition, and no rating-like identifier or key exists"
			note 'the exempt lines, so a reviewer can see the exemption is a negation rather than assume it:'
			trim < "$WORK/rating-exempt.txt" | sed 's/^/      /'
		else
			fail "$rating_violations rating mention(s) are not negations and $rating_ids rating-like identifier(s) or key(s) exist"
			note 'unexplained mentions (no negation precedes the token on that line):'
			grep -viE "$RATING_NEGATED" "$WORK/rating-hits.txt" | trim | sed 's/^/      /' | head -20
			note 'rating-like identifiers (a declaration, a JSON key or a column):'
			trim < "$WORK/rating-identifiers.txt" | sed 's/^/      /' | head -20
		fi
	fi
fi

# ---------------------------------------------------------------------------
check '2. Only permitted Riot assets: every image comes from Data Dragon'
find "$DIST" -type f \( -name '*.html' -o -name '*.css' \) -print0 |
	xargs -0 grep -hoIE '<img[^>]*>|<source[^>]*>|<link[^>]*rel="[^"]*icon[^"]*"[^>]*>|<meta[^>]*(og:image|twitter:image)[^>]*>|url\([^)]*\)' \
	> "$WORK/image-refs.txt" 2>/dev/null || true
image_refs=$(count_lines "$WORK/image-refs.txt")
dd_refs=$(grep -ohIE '<img[^>]*>' "$WORK/image-refs.txt" 2>/dev/null | grep -cF "$DD_ORIGIN" | tr -d ' ')
note "scanned image references in the built HTML and CSS: $image_refs matching tags or url() values, of which $dd_refs belong to an <img> tag on Riot's Data Dragon CDN"
if [ "$image_refs" -lt 20 ] || [ "$dd_refs" -lt 1 ]; then
	fail "the asset scan read only $image_refs image references and $dd_refs Data Dragon ones; it is not evidence of anything"
else
	grep -ohE 'https?://[A-Za-z0-9.-]+' "$WORK/image-refs.txt" 2>/dev/null | lower | sort -u > "$WORK/image-origins.txt" || true
	grep -vxF "$DD_ORIGIN_URL" "$WORK/image-origins.txt" > "$WORK/image-origins-bad.txt" 2>/dev/null || true
	bad_origins=$(count_lines "$WORK/image-origins-bad.txt")
	if [ "$bad_origins" -eq 0 ]; then
		pass "every absolute image origin is $DD_ORIGIN_URL; no champion art, splash art, loading screen or Riot mark is loaded from anywhere else, and every other reference is same-origin"
	else
		fail "$bad_origins external image origin(s) are not the Data Dragon CDN:"
		trim < "$WORK/image-origins-bad.txt" | sed 's/^/      /'
	fi
fi

# ---------------------------------------------------------------------------
check '3. No third-party scripts, embeds, fonts or tracking in the served pages'
find "$DIST" -type f -name '*.html' -print0 |
	xargs -0 grep -hoIE '<script[^>]*>|<iframe[^>]*>|<link[^>]*rel="[^"]*(preconnect|dns-prefetch)[^"]*"[^>]*>|@import[^;]*' \
	> "$WORK/resource-tags.txt" 2>/dev/null || true
resource_tags=$(count_lines "$WORK/resource-tags.txt")
script_tags=$(grep -ohE '<script[^>]*>' "$WORK/resource-tags.txt" 2>/dev/null | wc -l | tr -d ' ')
script_srcs=$(grep -ohE '<script[^>]*src="[^"]*"' "$WORK/resource-tags.txt" 2>/dev/null | wc -l | tr -d ' ')
note "scanned tags in the built HTML: $resource_tags lines, $script_tags <script> tags, $script_srcs of them with a src"
if [ "$script_tags" -lt 1 ]; then
	fail 'no <script> tag was found in the built site, so this scan cannot tell whether a third-party one exists'
else
	{
		grep -ohE '(src|href)="[^"]*"' "$WORK/resource-tags.txt" 2>/dev/null | grep -E 'https?:' || true
		grep -ohiE "$TRACKERS" "$WORK/resource-tags.txt" 2>/dev/null || true
	} > "$WORK/external-resources.txt"
	external_resources=$(count_lines "$WORK/external-resources.txt")
	if [ "$external_resources" -eq 0 ]; then
		pass 'no executable third-party resource: every script, embed and preconnect in the built pages is same-origin, and no tracking service is referenced'
	else
		fail "$external_resources external resource reference(s) or tracker name(s) found:"
		sed 's/^/      /' "$WORK/external-resources.txt" | sort -u | head -20
	fi
fi

# ---------------------------------------------------------------------------
check '4. The free tier is genuinely free and ungated: no account, no paywall'
find "$DIST" -type f -name '*.html' -print0 |
	xargs -0 grep -hoIE '<form[^>]*>|type="password"|type="email"|name="(password|email)"|href="[^"]*(login|sign-in|signin|sign-up|signup|register|subscribe|pricing|checkout)"|data-paywall|>Sign (in|up)<' \
	> "$WORK/gating.txt" 2>/dev/null || true
gating=$(count_lines "$WORK/gating.txt")
search_inputs=$(find "$DIST" -type f -name '*.html' -print0 | xargs -0 grep -hoIE '<input[^>]*type="search"[^>]*>' 2>/dev/null | grep -c '<input' | tr -d ' ')
note "scanned $(count_files "$DIST") built pages for a form, a password or email field, a sign-in, registration, subscription or checkout route, or a paywall"
note "excluded deliberately: $search_inputs <input type=\"search\"> elements, which are the same-origin table filters (data-island), not a gate; the tables render without them"
if [ "$gating" -eq 0 ]; then
	pass 'no form, no credential field, no auth route and no paywall in any built page; every route renders for an anonymous reader'
else
	fail "$gating gating element(s) found in the built pages:"
	trim < "$WORK/gating.txt" | sort -u | sed 's/^/      /' | head -20
fi

# ---------------------------------------------------------------------------
check "5. Riot's verified-site requirement is claimed only when it is satisfied"
token=${LOLSTATS_RIOT_VERIFICATION_TOKEN:-}
# Only the positive form of an assertion is matched: an auxiliary verb, no
# intervening negation, and then the verb. The shipped negations ("is not
# endorsed by Riot Games", "Riot has not verified this site") and the
# prohibitions in the terms therefore cannot be mistaken for a claim, which is
# what lets this check stay exemption-free and still be trusted.
CLAIMS='(is|are|was|were|has been|have been)[[:space:]]+(now[[:space:]]+|fully[[:space:]]+)?(verified|approved|endorsed|reviewed|authorised|licensed|sponsored|partnered)[[:space:]]+(by|with)[[:space:]]+Riot|Riot[- ](verified|approved|endorsed|sponsored)|verification[[:space:]]+(is[[:space:]]+)?(complete|completed|passed|granted)|(api|production|developer)[[:space:]]+key[[:space:]]+(has been|was|is)[[:space:]]+(granted|approved|issued)'
PENDING_MARKER='Riot has not verified this site'
pages=$(count_files "$DIST")
find "$DIST" -type f -name '*.html' -print0 | xargs -0 grep -hoiE "$CLAIMS" > "$WORK/claims.txt" 2>/dev/null || true
claims=$(count_lines "$WORK/claims.txt")
note "scanned $pages built pages for a positive claim that Riot has reviewed, verified or endorsed this site: $claims match(es)"
if [ -n "$token" ]; then
	if [ -f "$DIST/riot.txt" ]; then
		published=$(tr -d '\r\n' < "$DIST/riot.txt")
		if [ "$published" = "$token" ]; then
			pass '/riot.txt is published and holds exactly the configured token, so the site may assert its verified status'
		else
			fail '/riot.txt is published but does not hold the configured token; Riot would fail to verify a file the build offers as evidence'
		fi
	else
		fail 'LOLSTATS_RIOT_VERIFICATION_TOKEN is set but the build published no /riot.txt'
	fi
	note "pages that assert a Riot review: $claims, which the published token allows"
else
	if [ -f "$DIST/riot.txt" ]; then
		fail '/riot.txt exists although no verification token was configured; the file is then a false claim that Riot has verified this site'
	else
		pass 'no /riot.txt is published, so the site does not offer a verification file it cannot own'
	fi
	if [ "$claims" -eq 0 ]; then
		pass 'no page claims that Riot has reviewed, verified, approved or endorsed this site'
	else
		fail "$claims page fragment(s) claim a Riot review although no verification token is configured:"
		sort -u "$WORK/claims.txt" | trim | sed 's/^/      /'
	fi
	missing=''
	for page in about disclaimer legal/terms legal/privacy; do
		if ! grep -qF "$PENDING_MARKER" "$DIST/$page/index.html" 2>/dev/null; then
			missing="$missing $page"
		fi
	done
	if [ -n "$missing" ]; then
		fail "these pages no longer state the pending verification honestly ($PENDING_MARKER):$missing"
	else
		pass "4 of 4 compliance pages state the pending state: '$PENDING_MARKER'"
	fi
fi

# ---------------------------------------------------------------------------
check '6. The non-endorsement notice is visible, and the wording has not drifted'
if [ ! -f "$LEGAL" ]; then
	fail 'web/src/lib/legal.ts is missing, so the shared wording cannot be verified'
else
	sed -n "s/^export const NON_ENDORSEMENT_TEXT = '\(.*\)';$/\1/p" "$LEGAL" > "$WORK/non-endorsement.txt"
	approved=$(count_lines "$WORK/non-endorsement.txt")
	note "read the approved wording back out of web/src/lib/legal.ts: $approved line"
	if [ "$approved" -ne 1 ] || ! grep -qF "$NON_ENDORSEMENT_MARKER" "$WORK/non-endorsement.txt"; then
		fail 'the approved sentence could not be read back from web/src/lib/legal.ts, so this check cannot prove anything'
	else
		if grep -qF "$(cat "$WORK/non-endorsement.txt")" "$DIST/disclaimer/index.html" 2>/dev/null; then
			pass 'the frozen non-endorsement sentence is rendered on /disclaimer, word for word'
		else
			fail 'the frozen non-endorsement sentence is missing from the built /disclaimer page'
		fi
		find "$DIST" -type f -name '*.html' -print0 |
			xargs -0 grep -lF "$(cat "$WORK/non-endorsement.txt")" > "$WORK/non-endorsement-pages.txt" 2>/dev/null || true
		note "pages that render the sentence itself: $(count_lines "$WORK/non-endorsement-pages.txt") of $(count_files "$DIST")"
	fi
fi
find "$DIST" -type f -name '*.html' -print0 | xargs -0 grep -LF 'href="/disclaimer"' > "$WORK/no-disclaimer-link.txt" 2>/dev/null || true
unlinked=$(count_lines "$WORK/no-disclaimer-link.txt")
if [ "$unlinked" -eq 0 ]; then
	pass 'every built page links to /disclaimer, so the notice is one click from anywhere on the site'
else
	fail "$unlinked built page(s) do not link to /disclaimer:"
	sed 's/^/      /' "$WORK/no-disclaimer-link.txt" | head -10
fi

# ---------------------------------------------------------------------------
check '7. The legal pages publish a contact route'
email=${LOLSTATS_CONTACT_EMAIL:-}
if [ -z "$email" ] && [ -f "$LEGAL" ]; then
	email=$(sed -n "s/^export const OPERATOR_CONTACT_EMAIL = '\(.*\)';$/\1/p" "$LEGAL" | head -1)
fi
if [ -z "$email" ]; then
	fail 'no contact address is configured and none could be read from web/src/lib/legal.ts'
else
	note "contact address in force for this check: $email"
	missing=''
	checked=0
	for page in about disclaimer legal/terms legal/privacy; do
		checked=$((checked + 1))
		if ! grep -qF "$email" "$DIST/$page/index.html" 2>/dev/null; then
			missing="$missing $page"
		fi
	done
	if [ -n "$missing" ]; then
		fail "these legal pages carry no contact address:$missing"
	else
		pass "$checked of $checked compliance pages publish $email"
	fi
fi

# ---------------------------------------------------------------------------
check '8. The served address is the deployed one, not a reserved placeholder'
find "$DIST" -type f -name '*.html' -print0 | xargs -0 grep -lF 'lolstats.example.invalid' > "$WORK/placeholder.txt" 2>/dev/null || true
placeholder=$(count_lines "$WORK/placeholder.txt")
if [ "$placeholder" -eq 0 ]; then
	pass 'no built page carries a reserved placeholder hostname'
elif [ -n "${LOLSTATS_SITE_URL:-}" ]; then
	fail "$placeholder built page(s) still carry the reserved placeholder hostname although LOLSTATS_SITE_URL is set to $LOLSTATS_SITE_URL"
else
	warn "$placeholder of $(count_files "$DIST") built pages carry the reserved placeholder hostname lolstats.example.invalid, because this build was not given LOLSTATS_SITE_URL"
	note 'the deployed build must set LOLSTATS_SITE_URL to the registered domain; until it does, the canonical URLs and the sitemap name a host that is not this site'
fi

# ---------------------------------------------------------------------------
check '9. Nothing per-player is published, so the site cannot be a data broker'
# The control plane legitimately stores PUUIDs: it has to, because MATCH-V5 is
# addressed by puuid. The claim here is narrower and stronger - that no such
# field reaches the published artifact schema or the served files. The scan is
# over the machine-readable surface, where a field name is a shape a consumer
# depends on, rather than over prose that may describe the prohibition.
PERSONAL='(^|[^A-Za-z0-9_])(puuid|puuids|summoner_?id|account_?id|riot_?id|profile_?icon_?id|summoner_?name)([^A-Za-z0-9_]|$)'
: > "$WORK/personal-files.bin"
schemas=0
for candidate in "$ROOT/web/src/types/agg.d.ts" "$ROOT/web/src/types/agg.schema.json"; do
	if [ -s "$candidate" ]; then
		printf '%s\0' "$candidate" >> "$WORK/personal-files.bin"
		schemas=$((schemas + 1))
	else
		fail "the published artifact shape $candidate is missing or empty, so this claim cannot be checked"
	fi
done
find "$DIST" -type f -name '*.json' -print0 >> "$WORK/personal-files.bin" 2>/dev/null || true
published_json=$(find "$DIST" -type f -name '*.json' | grep -c '' | tr -d ' ')
published_scanned=$(count_nul "$WORK/personal-files.bin")
note "scanned the published artifact schema ($schemas of 2 files) and $published_json JSON file(s) under web/dist: $published_scanned file(s) in all"
# A scan that reads nothing looks exactly like a scan that finds nothing, so the
# schema is checked for a field it is known to declare.
shape_probe=$(grep -cE 'StaticSummonerSpells' "$ROOT/web/src/types/agg.d.ts" 2>/dev/null | tr -d ' ')
control=$(grep -icE "$PERSONAL" "$ROOT/internal/contract/contract.go" 2>/dev/null | tr -d ' ')
if [ "$schemas" -ne 2 ] || [ "$shape_probe" -lt 1 ]; then
	fail 'the artifact schema was not read, so an empty result would be meaningless'
elif [ "$control" -lt 1 ]; then
	fail 'the pattern finds no personal identifier even in internal/contract/contract.go, where puuid is genuinely used; the scan proves nothing'
else
	note "the pattern works: internal/contract/contract.go carries $control line(s) with such a field, as the crawler requires, and none of them is an aggregate type"
	xargs -0 grep -InE "$PERSONAL" < "$WORK/personal-files.bin" > "$WORK/personal-hits.txt" 2>/dev/null || true
	personal_hits=$(count_lines "$WORK/personal-hits.txt")
	if [ "$personal_hits" -eq 0 ]; then
		pass 'the published artifact schema carries no PUUID, summoner id, account id, Riot id or profile icon id, and neither does any served JSON file'
	else
		fail "$personal_hits per-player identifier(s) in the published surface:"
		trim < "$WORK/personal-hits.txt" | sed 's/^/      /' | head -20
	fi
fi
archive_paths=$(find "$DIST" \( -path '*/raw/*' -o -path '*/archive/*' \) | grep -c '' | tr -d ' ')
if [ "$archive_paths" -eq 0 ]; then
	pass 'the served tree holds no raw archive path; the verbatim Riot payloads stay in the cluster and are never fetched by a visitor'
else
	fail "$archive_paths raw-archive path(s) are inside the served tree"
	find "$DIST" \( -path '*/raw/*' -o -path '*/archive/*' \) | head -10 | sed 's/^/      /'
fi

# ---------------------------------------------------------------------------
check '10. Committed payloads carry no real player identifier'
# A committed capture is the privacy mistake that outlives whoever committed it,
# so the fixtures are hand-authored and their identifiers say so. This asserts the
# property rather than trusting the README that claims it. Deleting the fixtures
# to make this check quiet is a failure, not a pass: they are load-bearing for the
# Go tests, and an empty corpus is not evidence of a clean corpus.
committed_count=$(find "$ROOT/fixtures" -type f \( -name '*.json' -o -name '*.jsonl' \) -print0 2>/dev/null | { tr -cd '\0' | wc -c; } | tr -d ' ')
if [ ! -d "$ROOT/fixtures" ] || [ "$committed_count" -eq 0 ]; then
	fail 'no payload fixture was found under fixtures/, so the committed-provenance claim cannot be checked and the earlier result would have been vacuous'
else
	find "$ROOT/fixtures" -type f \( -name '*.json' -o -name '*.jsonl' \) -print0 > "$WORK/committed-files.bin" 2>/dev/null || true
	xargs -0 grep -hoE '"(puuid|summonerId|riotIdGameName|riotIdTagline)"[[:space:]]*:[[:space:]]*"[^"]*"' < "$WORK/committed-files.bin" > "$WORK/committed-ids.txt" 2>/dev/null || true
	committed_ids=$(count_lines "$WORK/committed-ids.txt")
	note "scanned $committed_count committed payload fixture(s) under fixtures/: $committed_ids player-identifier value(s)"
	if [ "$committed_ids" -lt 10 ]; then
		fail "only $committed_ids player-identifier value(s) were found in $committed_count fixture(s); the scan is not reaching the payloads, so a clean result would be meaningless"
	else
		# The identifying fields must carry the reserved 'fixture-' prefix. The
		# Riot ID tagline cannot: it is a 3-8 character tag, so there is no room
		# for a prefix. It is held to the reserved 'FIXT' literal instead, and
		# that literal is cross-checked against the generator that writes the
		# fixtures so the exemption is justified rather than assumed.
		grep -vE '"[^"]*"[[:space:]]*:[[:space:]]*"fixture-' "$WORK/committed-ids.txt" > "$WORK/committed-other.txt" || true
		non_synthetic=$(grep -vE '"[^"]*[Tt]agline"[[:space:]]*:[[:space:]]*"FIXT"' "$WORK/committed-other.txt" | grep -c '' | tr -d ' ')
		if [ "$non_synthetic" -eq 0 ]; then
			tagline=$(grep -cE '"[^"]*[Tt]agline"[[:space:]]*:[[:space:]]*"FIXT"' "$WORK/committed-other.txt" | tr -d ' ')
			pass "every one of the $committed_ids committed identifier value(s) is synthetic: $((committed_ids - tagline)) carry the reserved 'fixture-' prefix and $tagline Riot ID tagline(s) carry the reserved 'FIXT' placeholder"
		else
			fail "$non_synthetic committed identifier value(s) are neither the reserved 'fixture-' prefix nor the reserved 'FIXT' tagline:"
			grep -vE '"[^"]*[Tt]agline"[[:space:]]*:[[:space:]]*"FIXT"' "$WORK/committed-other.txt" | sort -u | trim | sed 's/^/      /' | head -10
		fi
		if grep -qF '"riotIdTagline":"FIXT"' "$ROOT/internal/aggregate/fixture_test.go" 2>/dev/null; then
			note "'FIXT' is the placeholder internal/aggregate/fixture_test.go writes when it regenerates these payloads, so the exemption above is the generator's own reserved value"
		else
			fail "the reserved 'FIXT' tagline is no longer what internal/aggregate/fixture_test.go writes, so the exemption above is stale"
		fi
	fi
	if grep -qF 'Nothing in this directory is real Riot data' "$ROOT/fixtures/README.md" 2>/dev/null; then
		pass 'fixtures/README.md records the provenance: nothing in fixtures/ is real Riot data'
	else
		fail 'fixtures/README.md no longer states that nothing in fixtures/ is real Riot data'
	fi
fi

# ---------------------------------------------------------------------------
printf '\n--- summary ---\n'
if [ "$failures" -eq 0 ]; then
	printf 'RESULT: PASS - 0 launch-blocking violations\n'
	exit 0
fi
printf 'RESULT: FAIL - %s launch-blocking violation(s)\n' "$failures"
exit 1
