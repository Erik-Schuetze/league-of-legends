#!/bin/sh
# scripts/compliance-check.sh - the launch-blocking compliance gate.
#
#   sh scripts/compliance-check.sh
#
# No network, no npm, no package manager: it reads the source tree, the corpus of
# pages a running tier served, and the shared wording in internal/webtier. Capture
# the corpus first (make compliance does both), because four of the checks are
# about what the deployment actually serves rather than about what the source
# intends.
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
#   LOLSTATS_SITE_URL                 the deployed address, also read by the build.
#                                     Check 8 fails on a reserved placeholder hostname
#                                     whether or not this is set, and requires every
#                                     canonical, the sitemap and robots.txt to name one
#                                     host: this variable when it is set, and the build's
#                                     own deliberate default when it is not
#   LOLSTATS_DIST                     scan another capture instead of the default one
#                                     (bin/served-pages). Used to check a demo, no-data
#                                     or live tier separately; it changes only which
#                                     files are read, never a rule
#   LOLSTATS_CONTACT_EMAIL            the published contact address
#
# Where a check can only be satisfied by a decision that is not the code's to
# make, it says so and keeps going; where the code can violate a prohibition, it
# fails. docs/compliance.md records the trigger each check belongs to.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# The corpus is what a running tier served, captured by
# scripts/capture-served-pages.sh. It replaced the compiled Astro reference tree
# (web/dist): that tree stopped being built on 2026-09-17, when the Go SSR tier
# became the only published site, and it was deleted on 2026-09-18. The capture is
# the stronger corpus and it is the only one now: it is the deployment's own
# output, it covers the whole site rather than a sample, and it carries the no-JS
# <form> the pre-rendered tree never had - which is the markup checks 3 and 4 are
# about. A reviewer who has to verify a specific data state can point the scans at
# a capture of that state instead of the shared one, which a concurrent capture
# replaces wholesale:
#   LOLSTATS_DIST=.agent-artifacts/capture-final-demo sh scripts/compliance-check.sh
DIST="${LOLSTATS_DIST:-$ROOT/bin/served-pages}"
if [ "${LOLSTATS_DIST:-}" != "" ] && [ "${DIST#/}" = "$DIST" ]; then DIST="$ROOT/$DIST"; fi
# The shared wording, and the two Go files that carry it: brand.go holds the
# non-endorsement notice, the trademarks, the operator's identity and the contact
# address; site.go holds the three labels a page's data state is rendered with.
# They were ported from web/src/lib/legal.ts and web/src/lib/site.ts, and they are
# now the source of truth that the pages are checked against.
LEGAL="$ROOT/internal/webtier/brand.go"
SITE="$ROOT/internal/webtier/site.go"
# Scratch space for the scans. It is named after this process so that two
# reviewers running the gate at the same time cannot delete each other's tally
# files half way through: that clobbering made whole checks report "0 of 0
# pages" and fail for a reason that had nothing to do with the site.
WORK="$ROOT/.agent-artifacts/compliance-check.$$"

mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT INT TERM

failures=0
pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }
warn() { printf 'WARN  %s\n' "$1"; }
note() { printf '      %s\n' "$1"; }
check() {
	# Scans write their tallies into $WORK. Re-create it at every check: a reviewer
	# who deletes that directory mid-run, or another process sharing the checkout,
	# would otherwise make whole checks read nothing and report a clean result for
	# no reason. The guard before the summary catches the case where it cannot be
	# re-created at all.
	mkdir -p "$WORK" 2>/dev/null || true
	printf '\n== %s\n' "$1"
}

count_lines() { grep -c '' "$1" 2>/dev/null | tr -d ' '; }
count_files() { find "$1" -type f -name '*.html' | grep -c '' | tr -d ' '; }
count_nul() { tr -cd '\0' < "$1" | wc -c | tr -d ' '; }
lower() { tr '[:upper:]' '[:lower:]'; }
# Built pages are one enormous line each, so report matches without the line.
trim() { cut -c1-200; }

# Search a NUL-delimited list of paths:  list_grep <list-file> <grep args...>
#
# grep must never be invoked with an empty operand list. GNU xargs, which is what
# the CI runner has, still runs the command once when the list is empty, so
# `xargs -0 grep -LE pattern < empty.bin` becomes `grep -LE pattern` with no file
# operands and grep reads *standard input* instead of failing. The check then
# reported "(standard input)" as a page carrying no honesty banner, because the
# runner's stdin was not empty: a false FAIL on a clean tree. The reverse is
# worse, and is why this is a correctness bug rather than a nuisance - a pattern
# that happens to match stdin hides a real violation behind a pass. BSD xargs and
# BSD grep read an empty stdin and stay silent, so the hole does not exist on the
# development machine, only in CI. Short-circuiting the empty list is the
# portable fix, and every scan below that reads a list file goes through it.
list_grep() {
	list_grep_list=$1
	shift
	if [ -s "$list_grep_list" ]; then
		xargs -0 grep "$@" < "$list_grep_list" 2>/dev/null
	fi
	return 0
}

# Read one string constant out of a Go source file, or print nothing.
#
# The constants these checks turn into patterns are declared in
# internal/webtier/brand.go and internal/webtier/site.go. This reader used to
# parse TypeScript, because the wording lived in web/src/lib/legal.ts and
# web/src/lib/site.ts until the retired reference tree was deleted; the two rules
# it learned there still apply and neither is cosmetic:
#
#  - a declaration may be wrapped over several lines, so newlines are flattened
#    to spaces before matching. site.ts wrote UNVERIFIED_PREVIEW_TEXT that way,
#    and a line-oriented `sed -n "s/.*NAME = '(.*)';.*/\1/p"` returned the empty
#    string for it - and that empty string then became a grep pattern. An ERE
#    with a trailing `|` has an empty alternative that matches every page, and
#    `grep -LF ""` lists no file at all, so the check that used it passed while
#    proving nothing.
#  - the declaration, not a mention of the name, is what is matched.
#
# Go declares these inside a const block, so there is no `const` keyword on the
# line to anchor on. The name is anchored by the `=` and by requiring a quoted
# literal immediately after it, which is what keeps a reference to the constant
# (`Heading: NoDataHeading`, `Prose(PreviewText)`) from being read as a
# declaration of it. Both the interpreted literal and the raw one (in backticks,
# which Go uses for a string with no escapes in it) are accepted, and single
# quotes are not: they are not a Go string at all, and accepting them would only
# mean a pattern read out of a syntax error.
read_const() {
	tr '\n' ' ' < "$1" 2>/dev/null | awk -v name="$2" '
		BEGIN { dq = sprintf("%c", 34); bq = sprintf("%c", 96) }
		{
			re = "(^|[^A-Za-z0-9_])" name "[[:space:]]*=[[:space:]]*"
			for (i = 1; i <= 2; i++) {
				q = (i == 1 ? dq : bq)
				if (match($0, re q "([^" q "]*)" q)) {
					s = substr($0, RSTART, RLENGTH)
					sub("^[^=]*=[[:space:]]*" q, "", s)
					sub(q "[^" q "]*$", "", s)
					print s
					exit
				}
			}
		}
	'
}

# assert_read <file> <constant> <value> <what it labels>: a constant that could
# not be read is a failure, not a skip, and its message says which one and why.
# This is the file's own rule from the header - every scan asserts that it read
# something - applied to the patterns themselves, because an empty pattern is
# worse than no scan: it matches everything and reports PASS.
assert_read() {
	if [ -n "$3" ]; then return 0; fi
	fail "$2 could not be read from $1, so $4 cannot be checked. An empty pattern is not a skipped check: it is a pattern that matches (or discards) every file, which is how this gate has passed while proving nothing. Expected a single-literal declaration, which may be wrapped over several lines and may be quoted with \" or \`: $2 = \"...\";"
	return 1
}

# The corpus is written by another scripts/capture-served-pages.sh, which removes
# the directory and rebuilds it. A gate run that lands mid-capture reads a torn
# tree: /disclaimer exists but is empty, no page carries the notice, no canonical
# can be read - and the gate then reports five content violations that are really
# one race, which is both alarming and wrong. Five missing-content failures at once is a shape, not a
# coincidence, so the tree is checked for it up front and the run stops with a
# diagnosis rather than a verdict. This exits 2: it is not a compliance failure,
# and no check has been evaluated yet.
preflight_dist() {
	torn=''
	for probe in index.html disclaimer/index.html about/index.html legal/privacy/index.html; do
		if [ ! -s "$DIST/$probe" ]; then
			torn="$torn $probe"
		elif ! grep -qF '</html>' "$DIST/$probe" 2>/dev/null; then
			torn="$torn $probe(truncated)"
		fi
	done
	if [ "$torn" != '' ]; then
		printf 'CANNOT RUN  the served corpus at %s is incomplete:%s\n' "$DIST" "$torn"
		printf '            another process is writing it (capture-served-pages.sh replaces it\n'
		printf '            wholesale) or nothing has been captured yet. Capture it, let the\n'
		printf '            capture settle, and re-run: these are missing or half-written files,\n'
		printf '            not compliance failures.\n'
		exit 2
	fi
}

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
check '0. The served corpus is present'
if [ ! -d "$DIST" ]; then
	fail "no served corpus at $DIST; capture one first: make compliance (or make served-pages)"
	exit 1
fi
htmls=$(count_files "$DIST")
if [ "$htmls" -lt 10 ]; then
	fail "$DIST holds only $htmls HTML pages; capture a tier's pages first: make served-pages"
	exit 1
fi
pass "$DIST holds $htmls served HTML pages"

preflight_dist

# ---------------------------------------------------------------------------
check '1. No MMR, ELO or rating-like value anywhere (Riot prohibition)'
find "$ROOT" \
	\( -name node_modules -o -name .git -o -name .agent-artifacts -o -name .astro \) -prune -o \
	-type f \( -name '*.go' -o -name '*.sql' -o -name '*.ts' -o -name '*.astro' -o -name '*.json' \
	-o -name '*.mjs' -o -name '*.js' -o -name '*.html' -o -name '*.css' \) -print0 \
	> "$WORK/rating-scan-files.bin" 2>/dev/null
rating_scanned=$(count_nul "$WORK/rating-scan-files.bin")
note "scanned $rating_scanned files: Go, SQL, TS/JS, JSON, HTML and CSS, the captured served pages included (node_modules, .git, .agent-artifacts and build caches excluded)"
if [ "$rating_scanned" -lt 200 ]; then
	fail "the rating scan read only $rating_scanned files, which is too few to be evidence; the find expression is wrong, not the code clean"
else
	list_grep "$WORK/rating-scan-files.bin" -InE "$RATING_TOKENS" > "$WORK/rating-hits.txt"
	rating_hits=$(count_lines "$WORK/rating-hits.txt")
	if [ "$rating_hits" -eq 0 ]; then
		fail 'the rating scan found no mention of a rating at all, not even the standing prohibition in internal/webtier/brand.go; the pattern is wrong rather than the code clean'
	else
		grep -iE "$RATING_NEGATED" "$WORK/rating-hits.txt" > "$WORK/rating-exempt.txt" 2>/dev/null || true
		rating_exempt=$(count_lines "$WORK/rating-exempt.txt")
		rating_violations=$((rating_hits - rating_exempt))
		list_grep "$WORK/rating-scan-files.bin" -InE "$RATING_IDENTIFIER" > "$WORK/rating-identifiers.txt"
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
note "scanned image references in the served HTML and CSS: $image_refs matching tags or url() values, of which $dd_refs belong to an <img> tag on Riot's Data Dragon CDN"
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
# AMENDED 2026-09-17 - see docs/compliance.md, "Amendment: checks 3 and 4, the
# dynamic-serving amendment". The check no longer fails a page for having fewer
# than one <script> tag. That floor was written for a pre-rendered site whose
# every page carried a hydration bundle; in the server-rendered tier a page is
# legitimately script-free (the /champions/<champion>/ pages emit no <script> at
# all, and elsewhere the only one is the JSON-LD block), and a tag *count* was
# never the invariant anyway. The invariant is that no executable resource or
# tracking service on a served page comes from anywhere but this origin, and the
# honesty labelling that the floor was standing in for is asserted directly by
# check 11, which requires every page's banner to match the data-state the page
# declares. What replaced the floor is a negative control: the extractor and the
# origin filter below are run against markup that does phone home, and the check
# fails if they do not flag it. This is strictly more than the floor proved, and
# it is fail-closed in the same direction: a scan that reads a suspiciously small
# number of pages still fails.
#
# The origin a reference may legally name is the one this build declares for
# itself, read from the canonical link of the served index rather than from an
# environment variable, so a checkout with no site URL configured still knows
# which absolute references are its own.
SELF_ORIGIN=$(grep -ohE '<link[^>]*rel="canonical"[^>]*>' "$DIST/index.html" 2>/dev/null |
	grep -ohE 'https?://[A-Za-z0-9.:-]+' | head -1)
# The tags that can load or execute something, extracted by a function so that
# the control below runs through exactly the code the scan runs through.
resource_tags_of() {
	grep -hoIE '<script[^>]*>|<iframe[^>]*>|<link[^>]*rel="[^"]*(stylesheet|preload|modulepreload|prefetch|preconnect|dns-prefetch)[^"]*"[^>]*>|@import[^;]*' "$@"
}
# Everything in those tags that is not this origin: an absolute URL, a
# protocol-relative URL, or the name of a tracking service. It takes a file
# rather than standard input because two greps read it.
external_of() {
	{
		grep -ohE '(src|href)="[^"]*"' "$1" | grep -E '="(https?:)?//' | grep -vF "\"$SELF_ORIGIN/" || true
		grep -ohiE "$TRACKERS" "$1" || true
	} 2>/dev/null || true
}
pages_scanned=$(count_files "$DIST")
if [ -z "$SELF_ORIGIN" ]; then
	fail "the origin this build declares for itself could not be read from $DIST/index.html (its canonical link), so an absolute same-origin reference could not be told apart from a third-party one"
else
	note "this build's own origin, from the canonical link of its index: $SELF_ORIGIN"
	find "$DIST" -type f -name '*.html' | LC_ALL=C sort > "$WORK/pages.txt" 2>/dev/null || true
	: > "$WORK/resource-tags.txt"
	# A loop rather than xargs: resource_tags_of is a shell function, and xargs
	# would try to execute it as a program, find nothing, and leave an empty scan
	# that passes. The count of tags read is asserted below for that reason.
	while IFS= read -r page; do
		[ -n "$page" ] || continue
		resource_tags_of "$page" >> "$WORK/resource-tags.txt" 2>/dev/null || true
	done < "$WORK/pages.txt"
	resource_tags=$(count_lines "$WORK/resource-tags.txt")
	script_tags=$(grep -ohE '<script[^>]*>' "$WORK/resource-tags.txt" 2>/dev/null | wc -l | tr -d ' ')
	script_srcs=$(grep -ohE '<script[^>]*src="[^"]*"' "$WORK/resource-tags.txt" 2>/dev/null | wc -l | tr -d ' ')
	external_of "$WORK/resource-tags.txt" > "$WORK/external-resources.txt"
	sort -u < "$WORK/external-resources.txt" > "$WORK/external-resources-uniq.txt" 2>/dev/null || cp "$WORK/external-resources.txt" "$WORK/external-resources-uniq.txt"
	external_resources=$(count_lines "$WORK/external-resources-uniq.txt")
	note "scanned $pages_scanned served pages: $resource_tags usable-resource tags, $script_tags <script> tags, $script_srcs of them with a src"
	note 'a page with no <script> is not a failure and is not evidence of anything by itself: what is asserted is that no <script>, stylesheet, font preload, embed or @import in the served markup names any origin but the one above, and that no tracking service appears at all'
	# The negative control. Each fragment below is one the check exists to catch,
	# and the two positive controls must be flagged while the two negative ones
	# must not be: a filter that flags everything is as useless as one that flags
	# nothing, and the third probe is the ordinary same-origin reference the tier
	# itself emits on every page.
	probe() { printf '%s\n' "$1" | resource_tags_of > "$WORK/probe.txt"; external_of "$WORK/probe.txt"; }
	probe_prefix() { printf '<link rel="stylesheet" href="%s/_astro/tokens.css">\n' "$SELF_ORIGIN" | resource_tags_of > "$WORK/probe.txt"; external_of "$WORK/probe.txt"; }
	ctrl_tracker=$(probe '<script src="https://www.googletagmanager.com/gtag/js?id=G-1"></script>' | grep -c 'googletagmanager' | tr -d ' ')
	ctrl_font=$(probe '<link rel="preconnect" href="https://fonts.gstatic.com">' | grep -c 'fonts\.gstatic\.com' | tr -d ' ')
	ctrl_self=$(probe_prefix | grep -c 'tokens\.css' | tr -d ' ')
	ctrl_image=$(probe '<img src="https://example.invalid/x.png">' | grep -c 'example\.invalid' | tr -d ' ')
	if [ "$pages_scanned" -lt 100 ]; then
		fail "the resource scan read $pages_scanned served page(s); that is too few to be evidence about a site of this size"
	elif [ "$resource_tags" -eq 0 ]; then
		fail "the resource scan read $pages_scanned pages and extracted no resource tag at all, so its clean result is evidence of nothing: the extractor no longer matches the markup"
	elif [ "$ctrl_tracker" -lt 1 ] || [ "$ctrl_font" -lt 1 ]; then
		fail "the negative control did not fire: an analytics <script> was flagged $ctrl_tracker time(s) and a third-party font preconnect $ctrl_font time(s), so a clean scan of the served pages would prove nothing"
	elif [ "$ctrl_self" -ne 0 ] || [ "$ctrl_image" -ne 0 ]; then
		fail "the control is over-broad: a same-origin stylesheet was flagged $ctrl_self time(s) and a third-party <img> $ctrl_image time(s) (images are check 2's business, not this check's)"
	elif [ "$external_resources" -eq 0 ]; then
		pass "no executable third-party resource and no tracker: all $resource_tags resource tags across $pages_scanned pages are same-origin, and the control flagged the analytics and third-party-font probes as designed"
	else
		fail "$external_resources external resource reference(s) or tracker name(s) found:"
		trim < "$WORK/external-resources-uniq.txt" | sed 's/^/      /' | head -20
	fi
fi

# ---------------------------------------------------------------------------
check '4. The free tier is genuinely free and ungated: no account, no paywall'
# AMENDED 2026-09-17 - see docs/compliance.md, "Amendment: checks 3 and 4, the
# dynamic-serving amendment". The check no longer fails on the presence of a
# <form>. "Any form is a gate" was a proxy for a property that is now asserted
# directly, because the server-rendered tier deliberately ships forms: the
# tier-list filter bar is a GET form, which is the no-JS path for sorting,
# filtering and paging, and it is the reason the site works with JavaScript
# disabled. Banning the tag would have banned the accessibility mechanism the
# redesign is built on.
#
# Three invariants replace it, each checked on its own and none of them weaker
# than what the tag ban caught:
#   R1  no credential field, no auth/subscription/checkout route and no paywall -
#       the original pattern, kept, minus the bare <form> alternative.
#   R2  every form is a no-JS server-side path: method="get" and an explicit
#       action on this origin. A form that can change state, or that posts to
#       somewhere that is not this site, is a failure. An action is required
#       rather than optional so that the destination of every control is
#       checkable in the markup instead of inherited from whatever URL the page
#       was reached by.
#   R3  every named control (<input>, <select>, <textarea>, <button> with a name)
#       sits inside a form: a named control submits something, and if it is not
#       in a form then it submits to nothing and the control has no server-side
#       path at all. A control with no name - the islands' own filter and sort
#       widgets are exactly that - cannot submit anything and is a JS
#       enhancement on top of a working page, so it is not this check's subject.
#
# R1 is exercised by a two-fragment control, as before, and R2/R3 by their own
# controls: bad markup must be flagged and the tier's own markup must not be.
GATING='type="password"|type="email"|name="(password|email)"|href="[^"]*(login|sign-in|signin|sign-up|signup|register|subscribe|pricing|checkout)"|data-paywall|>Sign (in|up)<'
FORM_TAG='<form[^>]*>'
NAMED_CONTROL='<(input|select|textarea|button)[^>]*[[:space:]]name="[^"]+"'
# A form tag that is a no-JS server-side path: GET, and an action that is a path
# on this origin. Both are read case-insensitively, because attribute names and
# values are case-insensitive in HTML and a check that only recognised the
# lowercase spelling would pass a form written in the other spelling.
form_is_nojs_path() {
	printf '%s' "$1" | grep -qiE 'method="get"' || return 1
	printf '%s' "$1" | grep -qE 'action="/[^"[:space:]]*"' || return 1
	return 0
}
# Remove every <form ...> ... </form> span from the pages on standard input. The
# every captured page is one line, so a line-oriented state machine is
# enough; nested forms are invalid HTML and are not considered.
strip_forms() {
	awk '{
		line = $0; out = "";
		while (match(line, /<form[^>]*>/)) {
			out = out substr(line, 1, RSTART - 1);
			line = substr(line, RSTART + RLENGTH);
			if (match(line, /<\/form>/)) { line = substr(line, RSTART + RLENGTH); }
			else { line = ""; }
		}
		print out line;
	}'
}
find "$DIST" -type f -name '*.html' -print0 |
	xargs -0 grep -hoIE "$GATING" \
	> "$WORK/gating.txt" 2>/dev/null || true
gating=$(count_lines "$WORK/gating.txt")
search_inputs=$(find "$DIST" -type f -name '*.html' -print0 | xargs -0 grep -hoIE '<input[^>]*type="search"[^>]*>' 2>/dev/null | grep -c '<input' | tr -d ' ')
note "scanned $(count_files "$DIST") served pages for a credential field, a sign-in, registration, subscription or checkout route, or a paywall"
# A site that gates nothing and a pattern that matches nothing produce the same
# silence, so the pattern is exercised against the markup this check exists to
# catch before its silence is believed - the same control check 9 applies to its
# pattern, and the reason this check has no exemption list to hide behind.
gating_probe=$(printf '%s\n' '<form action="/login"><input type="password" name="password"></form>' '<a href="/pricing">Pricing</a>' | grep -cE "$GATING" | tr -d ' ')
if [ "$gating_probe" -lt 2 ]; then
	fail "the gating pattern matches only $gating_probe of the two pieces of markup it exists to catch, so a clean scan of the served pages would prove nothing"
elif [ "$gating" -eq 0 ]; then
	pass 'no credential field, no auth route and no paywall in any served page; every route renders for an anonymous reader'
else
	fail "$gating gating element(s) found in the served pages:"
	trim < "$WORK/gating.txt" | sort -u | sed 's/^/      /' | head -20
fi

# R1's control is above; R2 and R3 get their own, because a form rule that flags
# nothing is indistinguishable from a site without forms, and the tier's own
# filter bar is a form this check has to accept.
find "$DIST" -type f -name '*.html' -print0 |
	xargs -0 grep -HoIE "$FORM_TAG" > "$WORK/forms.txt" 2>/dev/null || true
forms=$(count_lines "$WORK/forms.txt")
: > "$WORK/forms-bad.txt"
while IFS= read -r formline; do
	[ -n "$formline" ] || continue
	if ! form_is_nojs_path "${formline#*:}"; then
		printf '%s\n' "$formline" >> "$WORK/forms-bad.txt"
	fi
done < "$WORK/forms.txt"
forms_bad=$(count_lines "$WORK/forms-bad.txt")
# Every named control that is left after the forms are removed has no form to
# submit through, so it has no server-side path.
find "$DIST" -type f -name '*.html' -print0 | xargs -0 cat 2>/dev/null | strip_forms |
	grep -ohIE "$NAMED_CONTROL" > "$WORK/controls-outside-form.txt" 2>/dev/null || true
controls_outside=$(count_lines "$WORK/controls-outside-form.txt")
note "scanned $forms form(s) in the served pages, and every named control outside one; $search_inputs <input type=\"search\"> elements are the islands' own filters, which carry no name and therefore submit nothing"
# The controls. Both must fire on markup that is a gate or a dead control, and
# neither may fire on the tier's own filter bar.
r2_ctrl=$(printf '%s\n' '<form action="https://example.invalid/login" method="post"><input name="u"></form>' '<form method="get"><input name="q"></form>' | grep -cE "$FORM_TAG" | tr -d ' ')
r2_ctrl_bad=$(printf '%s\n' '<form action="https://example.invalid/login" method="post"><input name="u"></form>' '<form method="get"><input name="q"></form>' |
	grep -oE "$FORM_TAG" | while IFS= read -r tag; do form_is_nojs_path "$tag" || printf 'bad\n'; done | grep -c 'bad' | tr -d ' ')
r2_ctrl_good=$(printf '%s\n' '<form class="ds-filter-bar" action="/tier-list/mid" method="get" aria-label="Filters">' |
	grep -oE "$FORM_TAG" | while IFS= read -r tag; do form_is_nojs_path "$tag" || printf 'bad\n'; done | grep -c 'bad' | tr -d ' ')
r3_ctrl=$(printf '%s\n' '<input name="q"><select name="sort"></select>' | strip_forms | grep -oE "$NAMED_CONTROL" | wc -l | tr -d ' ')
r3_ctrl_good=$(printf '%s\n' '<form action="/tier-list/mid" method="get"><input name="q"><select name="sort"></select></form>' | strip_forms | grep -oE "$NAMED_CONTROL" | wc -l | tr -d ' ')
if [ "$r2_ctrl" -lt 2 ] || [ "$r2_ctrl_bad" -lt 2 ] || [ "$r2_ctrl_good" -ne 0 ]; then
	fail "the no-JS-path control is wrong: of two forms (one posting off-origin, one with no action) it flagged $r2_ctrl_bad of 2, and it flagged $r2_ctrl_good of the tier's own GET form"
elif [ "$r3_ctrl" -lt 2 ] || [ "$r3_ctrl_good" -ne 0 ]; then
	fail "the named-control control is wrong: $r3_ctrl of 2 named controls outside a form were found, and $r3_ctrl_good of the two named controls inside the tier's own form were reported as outside one"
elif [ "$forms" -eq 0 ]; then
	# The corpus is the tier's own output and the tier renders the no-JS filter
	# bar, so a corpus with no <form> in it is either not the tier's HTML or has
	# stopped exercising the rule this check exists for. Both readings are bad and
	# neither is a pass: a rule about forms that no page can violate proves
	# nothing. This was the served half of this check while the corpus and the
	# served pages were two different things.
	fail "no <form> appears in any of the $(count_files "$DIST") served page(s), so the no-JS-path rule proves nothing: either the tier stopped shipping its filter bar or this corpus is not the tier's HTML"
elif [ "$forms_bad" -eq 0 ] && [ "$controls_outside" -eq 0 ]; then
	pass "every form in the served pages is a GET form on this origin ($forms checked) and every named control is inside one (0 of $controls_outside outside); no credential field, no auth route and no paywall"
else
	fail "$forms_bad form(s) are not a no-JS server-side path and $controls_outside named control(s) sit outside a form:"
	trim < "$WORK/forms-bad.txt" | sed 's/^/      /' | head -10
	trim < "$WORK/controls-outside-form.txt" | sed 's/^/      /' | head -10
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
note "scanned $pages served pages for a positive claim that Riot has reviewed, verified or endorsed this site: $claims match(es)"
# The site is supposed to make no such claim, so this scan is silent by design and
# a pattern that had stopped matching would be silent too. The pattern is therefore
# exercised against the sentence it exists to catch first, as check 9 does with its
# pattern, so that silence here is evidence rather than an absence of evidence.
claim_probe=$(printf '%s\n' 'This site has been verified by Riot Games.' | grep -cE "$CLAIMS" | tr -d ' ')
if [ "$claim_probe" -lt 1 ]; then
	fail 'the claim pattern does not match the sentence it exists to catch, so a scan that found nothing would prove nothing'
elif [ -n "$token" ]; then
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
	fail 'internal/webtier/brand.go is missing, so the shared wording cannot be verified'
else
	read_const "$LEGAL" NonEndorsementText > "$WORK/non-endorsement.txt"
	approved=$(count_lines "$WORK/non-endorsement.txt")
	note "read the approved wording back out of internal/webtier/brand.go: $approved line"
	if [ "$approved" -ne 1 ] || ! grep -qF "$NON_ENDORSEMENT_MARKER" "$WORK/non-endorsement.txt"; then
		fail 'the approved sentence could not be read back from internal/webtier/brand.go, so this check cannot prove anything'
	else
		if grep -qF "$(cat "$WORK/non-endorsement.txt")" "$DIST/disclaimer/index.html" 2>/dev/null; then
			pass 'the frozen non-endorsement sentence is rendered on /disclaimer, word for word'
		else
			fail 'the frozen non-endorsement sentence is missing from the served /disclaimer page'
		fi
		find "$DIST" -type f -name '*.html' -print0 |
			xargs -0 grep -lF "$(cat "$WORK/non-endorsement.txt")" > "$WORK/notice-approved.txt" 2>/dev/null || true
		find "$DIST" -type f -name '*.html' -print0 |
			xargs -0 grep -lF "$NON_ENDORSEMENT_MARKER" > "$WORK/notice-any.txt" 2>/dev/null || true
		notice_any=$(count_lines "$WORK/notice-any.txt")
		notice_approved=$(count_lines "$WORK/notice-approved.txt")
		pages=$(count_files "$DIST")
		note "pages rendering the approved sentence: $notice_approved of $pages"
		# The notice itself has to be everywhere, or the counts below prove nothing:
		# a footer that stopped serving it would take every paraphrase with it.
		if [ "$notice_any" -eq 0 ]; then
			fail 'no served page carries the non-endorsement notice at all'
		elif [ "$notice_any" -ne "$pages" ]; then
			fail "$notice_any of $pages served pages carry the non-endorsement notice; the footer serves it on every page"
		else
			pass "all $pages served pages carry the non-endorsement notice"
		fi
		# Drift: a page that states the notice in wording that is not the frozen
		# sentence is the failure this check exists for. The retired reference tree's
		# footer carried its own paraphrase on every page while the approved sentence
		# appeared on four, and this check passed anyway. It does not now.
		sort "$WORK/notice-any.txt" > "$WORK/notice-any.sorted"
		sort "$WORK/notice-approved.txt" > "$WORK/notice-approved.sorted"
		comm -23 "$WORK/notice-any.sorted" "$WORK/notice-approved.sorted" > "$WORK/notice-drift.txt" 2>/dev/null || true
		drift=$(count_lines "$WORK/notice-drift.txt")
		if [ "$drift" -ne 0 ]; then
			fail "$drift served page(s) state the non-endorsement notice in wording that is not the frozen sentence from internal/webtier/brand.go:"
			sed 's/^/      /' "$WORK/notice-drift.txt" | head -5
			note 'the footer and the legal pages must render NonEndorsementText itself rather than a paraphrase'
		else
			pass "every page that states the notice uses the frozen sentence, so no paraphrase of it is served ($notice_approved of $pages)"
		fi
		# And the source invariant behind that, so the drift is caught even in a
		# state where every page happens to render the sentence for some other
		# reason. The footer is the one place the notice is repeated on every single
		# page, so its wording has to be the constants rather than a copy of them:
		# footerFor in render.go builds the footer sentence as
		# TrademarkText + " " + NonEndorsementText, and shell.tmpl renders that field.
		# A footer that carried its own copy of the sentence is exactly the drift
		# this check exists for - the retired reference tree did that on every page
		# while the approved sentence appeared on four, and this check passed anyway.
		# The legal pages' own body copy is not covered by this: stating the notice
		# is what those pages are for, and the page scans above assert its wording.
		footer_wiring=''
		if ! grep -qF 'TrademarkText + " " + NonEndorsementText' "$ROOT/internal/webtier/render.go" 2>/dev/null; then
			footer_wiring="$footer_wiring internal/webtier/render.go (footerFor no longer builds the footer sentence from the two constants)"
		fi
		if ! grep -qF '.Footer.Riot }' "$ROOT/internal/webtier/templates/shell.tmpl" 2>/dev/null; then
			footer_wiring="$footer_wiring internal/webtier/templates/shell.tmpl (the shell no longer renders the footer sentence)"
		fi
		if [ -n "$footer_wiring" ]; then
			fail "the footer no longer renders the approved sentence from the shared constant, so it can drift from the approved wording:$footer_wiring"
		else
			pass 'the footer sentence is the shared constants themselves (render.go concatenates TrademarkText and NonEndorsementText, shell.tmpl renders the field), not a copy of the wording'
		fi
	fi
fi
find "$DIST" -type f -name '*.html' -print0 | xargs -0 grep -LF 'href="/disclaimer"' > "$WORK/no-disclaimer-link.txt" 2>/dev/null || true
unlinked=$(count_lines "$WORK/no-disclaimer-link.txt")
if [ "$unlinked" -eq 0 ]; then
	pass 'every served page links to /disclaimer, so the notice is one click from anywhere on the site'
else
	fail "$unlinked served page(s) do not link to /disclaimer:"
	sed 's/^/      /' "$WORK/no-disclaimer-link.txt" | head -10
fi

# ---------------------------------------------------------------------------
check '7. The legal pages publish a contact route'
email=${LOLSTATS_CONTACT_EMAIL:-}
if [ -z "$email" ] && [ -f "$LEGAL" ]; then
	email=$(read_const "$LEGAL" OperatorContactEmail)
fi
if [ -z "$email" ]; then
	fail 'no contact address is configured and none could be read from internal/webtier/brand.go'
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
# Every address the deployment publishes, not just the pages: the sitemap and
# robots.txt carry the origin too, and they are what a crawler reads first.
PLACEHOLDER='lolstats.example.invalid'
find "$DIST" -type f \( -name '*.html' -o -name '*.xml' -o -name '*.txt' \) -print0 |
	xargs -0 grep -lF "$PLACEHOLDER" > "$WORK/placeholder.txt" 2>/dev/null || true
placeholder=$(count_lines "$WORK/placeholder.txt")
if [ "$placeholder" -eq 0 ]; then
	pass 'no served page, sitemap or robots.txt carries a reserved placeholder hostname'
else
	fail "$placeholder published file(s) carry the reserved placeholder hostname $PLACEHOLDER; a missing LOLSTATS_SITE_URL must never reach the canonicals or the sitemap"
	sed 's/^/      /' "$WORK/placeholder.txt" | head -5
	note 'the renderer publishes a stated default instead of a placeholder (internal/webtier/artifacts.go, DefaultSiteURL), so a reserved hostname can only appear here if one is introduced deliberately'
fi

: > "$WORK/hosts.txt"
find "$DIST" -type f -name '*.html' -print0 |
	xargs -0 grep -oh 'rel="canonical" href="[^"]*"' 2>/dev/null |
	sed 's/.*href="//; s/".*$//; s|^http://|https://|; s|\(^https://[^/]*\).*|\1|' >> "$WORK/hosts.txt"
if [ -f "$DIST/sitemap.xml" ]; then
	grep -oh '<loc>https\{0,1\}://[^<]*' "$DIST/sitemap.xml" 2>/dev/null |
		sed 's|<loc>||; s|^http://|https://|; s|\(^https://[^/]*\).*|\1|' >> "$WORK/hosts.txt"
fi
if [ -f "$DIST/robots.txt" ]; then
	grep -oh '^Sitemap: https\{0,1\}://[^ ]*' "$DIST/robots.txt" 2>/dev/null |
		sed 's|^Sitemap: ||; s|^http://|https://|; s|\(^https://[^/]*\).*|\1|' >> "$WORK/hosts.txt"
fi
sort -u "$WORK/hosts.txt" > "$WORK/hosts-uniq.txt"
addressed=$(count_lines "$WORK/hosts.txt")
hosts=$(count_lines "$WORK/hosts-uniq.txt")
if [ "$addressed" -eq 0 ]; then
	fail 'no canonical URL could be read out of the served site, so this check cannot prove where it points'
elif [ "$hosts" -ne 1 ]; then
	fail "the served site addresses $hosts different hosts; every canonical and the sitemap must name one origin:"
	sed 's/^/      /' "$WORK/hosts-uniq.txt" | head -5
else
	pass "all $addressed canonical, sitemap and robots.txt addresses name $(cat "$WORK/hosts-uniq.txt")"
	if [ -n "${LOLSTATS_SITE_URL:-}" ]; then
		expected=$(printf '%s' "$LOLSTATS_SITE_URL" | sed 's|/$||; s|^http://|https://|')
		# A degenerate value (a bare "/", say) reduces to the empty pattern, and an
		# empty pattern matches every line, so "the served origin is the configured
		# address" would be asserted without comparing anything.
		if [ -z "${expected#https://}" ] || [ "$expected" = '/' ]; then
			fail "LOLSTATS_SITE_URL is set to '$LOLSTATS_SITE_URL', which names no host, so the served origin cannot be compared with it"
		elif grep -qxF "$expected" "$WORK/hosts-uniq.txt"; then
			pass "the served origin is the configured LOLSTATS_SITE_URL ($expected)"
		else
			fail "the served origin $(cat "$WORK/hosts-uniq.txt") is not LOLSTATS_SITE_URL ($expected)"
		fi
	else
		note "LOLSTATS_SITE_URL is not set, so the build used its own default; the addresses above are that default, not a placeholder"
	fi
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
for candidate in "$ROOT/schema/agg.d.ts" "$ROOT/schema/agg.schema.json"; do
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
note "scanned the published artifact schema ($schemas of 2 files) and $published_json served JSON file(s): $published_scanned file(s) in all"
# A scan that reads nothing looks exactly like a scan that finds nothing, so the
# schema is checked for a field it is known to declare.
shape_probe=$(grep -cE 'StaticSummonerSpells' "$ROOT/schema/agg.d.ts" 2>/dev/null | tr -d ' ')
control=$(grep -icE "$PERSONAL" "$ROOT/internal/contract/contract.go" 2>/dev/null | tr -d ' ')
if [ "$schemas" -ne 2 ] || [ "$shape_probe" -lt 1 ]; then
	fail 'the artifact schema was not read, so an empty result would be meaningless'
elif [ "$control" -lt 1 ]; then
	fail 'the pattern finds no personal identifier even in internal/contract/contract.go, where puuid is genuinely used; the scan proves nothing'
else
	note "the pattern works: internal/contract/contract.go carries $control line(s) with such a field, as the crawler requires, and none of them is an aggregate type"
	list_grep "$WORK/personal-files.bin" -InE "$PERSONAL" > "$WORK/personal-hits.txt"
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
	list_grep "$WORK/committed-files.bin" -hoE '"(puuid|summonerId|riotIdGameName|riotIdTagline)"[[:space:]]*:[[:space:]]*"[^"]*"' > "$WORK/committed-ids.txt"
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
check '11. Every served page is a whole document that carries its data-provenance labelling'
# The corpus is the tier's own output, so this is the same property that
# scripts/verify-serving.sh asserts against a live origin, asserted here for the
# whole site rather than for the handful of routes that script polls. The two are
# not redundant: this one runs in CI without a cluster and asserts the labelling
# on 1000+ captured pages, and that one polls a deployment after it is rolled out,
# so it catches a tier that damages a page on the way out.
#
# The labelling is mandatory rather than decorative. With no Riot API key the
# site publishes demo data on purpose, and a page that has lost its banner is
# presenting illustrative numbers as if they were statistics. That is what a page
# cut short looks like from the outside, and no status-code check can see it: a
# damaged page is still a 200.
#
# What is asserted is that the banner matches the state the page itself declares,
# rather than that every page carries one fixed string, because the site renders
# three honest states with different wording each (demo, live, no-data) and a
# check hardcoded to the demo wording would fail a live build for being live.
# A page that declares no state at all still fails, and the demo wording is read
# from its declaration so that changing it cannot quietly leave this check
# asserting a string the site no longer emits. An unreadable declaration is a
# failure rather than a skip, because a scan that matched nothing would otherwise
# report a clean result.
#
# All three constants are asserted, not just the first. The demo scan is a union
# of two alternatives and the no-data scan is an exclusion, so an empty constant
# does not weaken those scans, it deletes them: `PREVIEW|` matches every page,
# and `grep -LF ""` lists nothing. UNVERIFIED_PREVIEW_TEXT was empty because it is
# declared across two lines and the extractor used to read one line at a time, so
# a demo page that had lost its banner passed this check. read_const reads the
# declaration whole, and assert_read fails the gate by name when it cannot.
PREVIEW_TEXT=$(read_const "$SITE" PreviewText)
UNVERIFIED_PREVIEW_TEXT=$(read_const "$SITE" UnverifiedPreviewText)
NO_DATA_HEADING=$(read_const "$SITE" NoDataHeading)
readable=1
assert_read "$SITE" PreviewText "$PREVIEW_TEXT" 'the demo labelling of every served page' || readable=0
assert_read "$SITE" UnverifiedPreviewText "$UNVERIFIED_PREVIEW_TEXT" 'the demo labelling of a snapshot whose manifest does not declare its source' || readable=0
assert_read "$SITE" NoDataHeading "$NO_DATA_HEADING" 'the no-data labelling of every served page' || readable=0
# Escape a literal for use inside an extended regular expression: everything
# except the characters that appear in this site's wording is escaped, which is
# cheaper to read than a bracket expression and cannot under-escape a dot.
ere() { printf '%s' "$1" | sed 's/[^A-Za-z0-9 _,-]/\\&/g'; }
if [ "$readable" -eq 0 ]; then
	note 'the three constants are read out of internal/webtier/site.go by read_const; an empty one is reported above rather than searched for'
else
	find "$DIST" -type f -name '*.html' -print0 > "$WORK/pages.bin" 2>/dev/null || true
	pages_checked=$(count_nul "$WORK/pages.bin")
	# grep -L lists the files that do NOT match. The served pages are one long
	# line each, so an anchored </html>$ matches only a page that really ends
	# where it should; a page cut short has no line that ends with it.
	list_grep "$WORK/pages.bin" -LE '</html>$' > "$WORK/pages-truncated.txt"
	# A page must declare a state, and then carry the banner for that state.
	list_grep "$WORK/pages.bin" -LE 'data-state="(demo|live|no-data)"' > "$WORK/pages-bannerless.txt"
	for state in demo live no-data; do
		list_grep "$WORK/pages.bin" -Fl "data-state=\"$state\"" > "$WORK/pages-$state.txt"
	done
	tr '\n' '\0' < "$WORK/pages-demo.txt" > "$WORK/pages-demo.bin"
	list_grep "$WORK/pages-demo.bin" -LE "$(ere "$PREVIEW_TEXT")|$(ere "$UNVERIFIED_PREVIEW_TEXT")" > "$WORK/pages-unlabelled.txt"
	tr '\n' '\0' < "$WORK/pages-live.txt" > "$WORK/pages-live.bin"
	list_grep "$WORK/pages-live.bin" -LF 'state-banner--live' > "$WORK/pages-live-broken.txt"
	tr '\n' '\0' < "$WORK/pages-no-data.txt" > "$WORK/pages-no-data.bin"
	list_grep "$WORK/pages-no-data.bin" -LF "$NO_DATA_HEADING" > "$WORK/pages-no-data-broken.txt"
	truncated=$(count_lines "$WORK/pages-truncated.txt")
	bannerless=$(count_lines "$WORK/pages-bannerless.txt")
	unlabelled=$(count_lines "$WORK/pages-unlabelled.txt")
	live_broken=$(count_lines "$WORK/pages-live-broken.txt")
	no_data_broken=$(count_lines "$WORK/pages-no-data-broken.txt")
	note "scanned $pages_checked served page(s) for a final </html> and for the banner their state declares: $(count_lines "$WORK/pages-demo.txt") demo, $(count_lines "$WORK/pages-live.txt") live, $(count_lines "$WORK/pages-no-data.txt") no-data"
	if [ "$pages_checked" -lt 10 ]; then
		fail "only $pages_checked served page(s) were inspected, so the scan is not reaching the pages and a clean result would be meaningless"
	elif [ "$truncated" -eq 0 ] && [ "$bannerless" -eq 0 ] && [ "$unlabelled" -eq 0 ] && [ "$live_broken" -eq 0 ] && [ "$no_data_broken" -eq 0 ]; then
		pass "all $pages_checked served page(s) end with </html> and carry the labelling their declared state requires"
	else
		if [ "$truncated" -gt 0 ]; then
			fail "$truncated served page(s) do not end with </html>:"
			sort -u "$WORK/pages-truncated.txt" | sed "s|^$DIST/||" | sed 's/^/      /' | head -10
		fi
		if [ "$bannerless" -gt 0 ]; then
			fail "$bannerless served page(s) declare no data state at all, so they carry no provenance:"
			sort -u "$WORK/pages-bannerless.txt" | sed "s|^$DIST/||" | sed 's/^/      /' | head -10
		fi
		if [ "$unlabelled" -gt 0 ]; then
			fail "$unlabelled demo page(s) do not carry '$PREVIEW_TEXT':"
			sort -u "$WORK/pages-unlabelled.txt" | sed "s|^$DIST/||" | sed 's/^/      /' | head -10
		fi
		if [ "$live_broken" -gt 0 ]; then
			fail "$live_broken live page(s) carry no live banner:"
			sort -u "$WORK/pages-live-broken.txt" | sed "s|^$DIST/||" | sed 's/^/      /' | head -10
		fi
		if [ "$no_data_broken" -gt 0 ]; then
			fail "$no_data_broken no-data page(s) do not carry '$NO_DATA_HEADING':"
			sort -u "$WORK/pages-no-data-broken.txt" | sed "s|^$DIST/||" | sed 's/^/      /' | head -10
		fi
	fi
fi

# ---------------------------------------------------------------------------
check '12. The scan harness cannot mistake its own standard input for a page'
# Every scan above hands a NUL-delimited list of paths to list_grep. Two harness
# faults would make those scans lie, and both are cheaper to rule out than to
# assume: an empty list that grep answers from its standard input - a false FAIL
# on a clean tree, and a false PASS whenever the pattern matches stdin - and a
# guard that has stopped feeding grep the non-empty list at all, which silently
# turns every scan above into a no-op. The stdin that was read came from the CI
# runner, which is not empty, and that is exactly why the fault was absent on the
# development machine and present only in CI.
: > "$WORK/harness-empty.bin"
harness_stdin=0
# Both flags the scans use, because they fail differently: GNU grep -L reports an
# empty input as a file with no matching lines and prints "(standard input)",
# while -l prints nothing for it. Checking only -l would have passed the broken
# harness that produced the CI failure, since check 11's two broken scans use -L.
for flag in -LF -lF; do
	harness_stdin=$((harness_stdin +
		$(printf 'must-not-be-read\n' |
			{ list_grep "$WORK/harness-empty.bin" "$flag" 'must-not-be-read' > "$WORK/harness-empty-out.txt"; count_lines "$WORK/harness-empty-out.txt"; })))
done
printf '%s\0' "$DIST/index.html" > "$WORK/harness-one.bin"
harness_probe=0
# The positive half, for both flags: -L must list a file whose text cannot
# contain the pattern, and -l must list the same file for a pattern it does
# contain. Either call returning nothing means the guard has stopped handing
# grep its list, and the scans above are no-ops that pass.
harness_probe=$((harness_probe +
	$(list_grep "$WORK/harness-one.bin" -LF 'no-built-page-contains-this-marker' > "$WORK/harness-one-out.txt" </dev/null
		count_lines "$WORK/harness-one-out.txt")))
harness_probe=$((harness_probe +
	$(list_grep "$WORK/harness-one.bin" -lF '<html' > "$WORK/harness-one-out.txt" </dev/null
		count_lines "$WORK/harness-one-out.txt")))
if [ "$harness_stdin" -eq 0 ] && [ "$harness_probe" -eq 2 ]; then
	pass 'an empty path list yields no match with either flag, even when the pattern is on standard input, and a one-file list yields its file for both -L and -l'
else
	fail "the scan harness is broken, so the scans above prove nothing: the empty list produced $harness_stdin match(es) where 0 is required, and the one-file list produced $harness_probe where 2 are required"
fi

# ---------------------------------------------------------------------------
# Scans write their tallies into $WORK. If that directory is deleted while the
# gate runs - two reviewers sharing one scratch tree, or a script that clears it -
# the counts above read as zero and the run can look clean for a reason that has
# nothing to do with the site. Say so instead of trusting it.
if [ ! -d "$WORK" ]; then
	printf '\n'
	fail "the work directory $WORK was deleted while the gate was running, so the scans above did not all read the site and this result cannot be trusted: re-run, and give each concurrent reviewer their own checkout or LOLSTATS_DIST"
fi

# ---------------------------------------------------------------------------
printf '\n--- summary ---\n'
if [ "$failures" -eq 0 ]; then
	printf 'RESULT: PASS - 0 launch-blocking violations\n'
	exit 0
fi
printf 'RESULT: FAIL - %s launch-blocking violation(s)\n' "$failures"
exit 1
