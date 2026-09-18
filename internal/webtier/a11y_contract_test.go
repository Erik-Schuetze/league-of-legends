package webtier

import (
	"bytes"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// a11y_contract_test.go is the executable form of design-tokens.md §3: the eight
// accessibility behaviours the ported design language must have and upstream did
// not. §3 says "fix, do not inherit", so each item is asserted against the bytes
// the tier actually serves -- not against a stylesheet in isolation -- because
// the interesting failures (a lost aria-current, an overflow clip that eats the
// table, an h1 that became an h2) live in the join between markup and CSS.
//
// Every check carries its own positive control. A check is a function of a
// document, and the harness hands it a deliberately broken copy of the same
// document; if the broken copy still passes, the check is vacuous and the test
// fails. This is the difference between "the assertion ran" and "the assertion
// can fail", which is the only kind of evidence worth handing over.

// document is one route as the browser receives it: the markup, the active nav
// path the page declares, and the three stylesheets concatenated in the order
// the browser sees them (external base sheet, inlined scoped chunk, inlined
// frozen layer). Everything a check needs is here, so a check cannot accidentally
// read a file on disk instead of the bytes under test.
type document struct {
	route string
	html  string
	css   string
	tok   map[string]string
}

type a11yFix struct {
	item  string
	what  string
	check func(document) error
	// breakDocs each return a copy of d with the fix removed in a different
	// way -- losing the attribute, or keeping it and pointing it at the wrong
	// page. Every one of them must be rejected by check on at least one route,
	// or the clause it targets is untested. A check that survives a mutation
	// does not test what it claims to test.
	breakDocs []func(document) document
	// mutations names each entry in breakDocs, in the same order, so a passing
	// run can print which control it rejected and the report can cite the test
	// output instead of asserting that controls exist.
	mutations []string
}

// colourRatio is the WCAG 2.x relative-luminance ratio. Kept here so the
// contract can re-measure a colour it reads out of the served CSS rather than
// trusting a number quoted in a comment.
func colourRatio(fg, bg string) (float64, error) {
	lum := func(hex string) (float64, error) {
		hex = strings.TrimPrefix(hex, "#")
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		if len(hex) != 6 {
			return 0, fmt.Errorf("not a 6-digit hex: %q", hex)
		}
		var v [3]float64
		for i := 0; i < 3; i++ {
			var n int
			if _, err := fmt.Sscanf(hex[i*2:i*2+2], "%02x", &n); err != nil {
				return 0, err
			}
			c := float64(n) / 255
			if c <= 0.04045 {
				v[i] = c / 12.92
			} else {
				v[i] = math.Pow((c+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*v[0] + 0.7152*v[1] + 0.0722*v[2], nil
	}
	lf, err := lum(fg)
	if err != nil {
		return 0, err
	}
	lb, err := lum(bg)
	if err != nil {
		return 0, err
	}
	hi, lo := lf, lb
	if hi < lo {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05), nil
}

var (
	reH1       = regexp.MustCompile(`<h1[ >]`)
	reAnyHead  = regexp.MustCompile(`<h[1-6][ >]`)
	reMain     = regexp.MustCompile(`(?s)<main id="main">(.*)</main>`)
	reRule     = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	reMediaRM  = regexp.MustCompile(`(?s)@media\s*\(\s*prefers-reduced-motion\s*:\s*reduce\s*\)\s*\{`)
	reNav      = regexp.MustCompile(`(?s)<nav\b[^>]*>(.*?)</nav>`)
	reSiteNav  = regexp.MustCompile(`(?s)<nav class="bar"[^>]*>(.*?)</nav>`)
	reLinkTag  = regexp.MustCompile(`<a\b[^>]*>`)
	reMarkedA  = regexp.MustCompile(`<a\b[^>]*aria-current="page"[^>]*>`)
	reHrefAttr = regexp.MustCompile(`href="([^"]*)"`)
	rePatchPfx = regexp.MustCompile(`^/patch/[^/]+`)
	reCssCmt   = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// markup drops the parts of the document that are not markup: the inlined <style>
// and <script> payloads and HTML comments. The served CSS discusses <h1> and
// aria-current="page" in its own comments, so a check that greps the raw bytes
// counts its own documentation as a finding -- and worse, a mutation aimed at
// "<h1" lands in the stylesheet and proves nothing about the document.
func markup(html string) string {
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?s)<!--.*?-->`),
		regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`),
		regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`),
	} {
		html = re.ReplaceAllString(html, "")
	}
	return html
}

// stripComments removes CSS comments for the same reason: a comment is not a
// declaration. The sheets that ship mention :focus-visible,
// prefers-reduced-motion and tabular-nums in prose, and a check that greps raw
// bytes would pass on the prose alone.
func stripComments(css string) string {
	return reCssCmt.ReplaceAllString(css, "")
}

// cssBlocks returns the contents of every at-rule whose prelude matches header,
// with nested braces respected. A flat [^{}]+{[^{}]*} scan cannot see inside
// @media: the outer block comes back empty and its nested rules lose the parent
// that gives them meaning, which is how "the reduced-motion block neutralises
// motion" can end up measuring an empty string.
func cssBlocks(css string, header *regexp.Regexp) []string {
	var out []string
	for _, loc := range header.FindAllStringIndex(css, -1) {
		depth, i := 1, loc[1]
		for i < len(css) && depth > 0 {
			switch css[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			i++
		}
		if depth == 0 {
			out = append(out, css[loc[1]:i-1])
		}
	}
	return out
}

// hasPseudo reports whether sel selects on pseudo itself rather than on a longer
// identifier that merely starts with it. ":focus-visible-disabled" contains the
// substring ":focus-visible", so a substring test would happily pass a
// stylesheet whose focus ring had been renamed out of existence.
func hasPseudo(sel, pseudo string) bool {
	for off := 0; ; {
		i := strings.Index(sel[off:], pseudo)
		if i < 0 {
			return false
		}
		end := off + i + len(pseudo)
		if end >= len(sel) || !isNameByte(sel[end]) {
			return true
		}
		off = end
	}
}

func isNameByte(c byte) bool {
	return c == '-' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// pathKey normalises a link target to the page it opens: no query, no fragment,
// no patch prefix, no trailing slash. /patch/16.18/tier-list/top and
// /tier-list/top compare equal -- the same page one patch-switcher click apart.
//
// The query is dropped for the same reason, and the data explorer is why. This
// site carries the patch in two shapes: the tier list and the matchups put it in
// the path, and the explorer has no per-patch path at all, so its switcher sends
// the same page as /explore?patch=16.18&per=30 (view_explore.go's explorePatches).
// A marker whose only difference from the route is view state still names the
// page being served, and the canonical the explorer publishes is path-only.
func pathKey(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = rePatchPfx.ReplaceAllString(p, "")
	if p = strings.TrimSuffix(p, "/"); p == "" {
		return "/"
	}
	return p
}

// samePage compares two link targets the way this site's routes do: without the
// trailing slash and without the query, which selects a view of a page and not
// the page. Unlike pathKey it keeps the patch prefix, because a nav link to the
// unpatched route is a different page from a patched one.
func samePage(a, b string) bool {
	path := func(p string) string {
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		return strings.TrimSuffix(p, "/")
	}
	return path(a) == path(b)
}

// cssColour pulls one declaration out of an inline style attribute.
func cssColour(style, prop string) string {
	m := regexp.MustCompile(`(?:^|;)\s*` + prop + `\s*:\s*([^;]+)`).FindStringSubmatch(style)
	if m == nil {
		return ""
	}
	v := strings.TrimSpace(m[1])
	return v
}

// withTokens re-reads the token table from a document's CSS. Mutations edit the
// CSS, so re-deriving here is what makes a stylesheet mutation reach the checks
// that measure colours instead of only the ones that grep for selectors.
func withTokens(d document) document {
	d.tok = parseRootTokens(d.css)
	return d
}

// hexOf resolves a token name -- or accepts a literal -- to a hex colour, and
// reports whether it got one. Alias chains are followed so that a token declared
// as var(--other) measures the colour the browser will actually paint.
func (d document) hexOf(name string) (string, bool) {
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		if strings.HasPrefix(name, "#") {
			if len(name) == 4 {
				return "#" + strings.Repeat(string(name[1]), 2) + strings.Repeat(string(name[2]), 2) +
					strings.Repeat(string(name[3]), 2), true
			}
			if len(name) == 7 {
				return name, true
			}
			return "", false
		}
		if seen[name] {
			return "", false
		}
		seen[name] = true
		v, ok := d.tok[name]
		if !ok {
			return "", false
		}
		if m := regexp.MustCompile(`^var\(\s*(--[a-z0-9-]+)\s*(?:,[^)]*)?\)$`).FindStringSubmatch(v); m != nil {
			name = m[1]
			continue
		}
		// rgb(r g b / a) is turned into hex only when fully opaque; a
		// translucent token is not a text colour and is reported as unresolved.
		if m := regexp.MustCompile(`^rgb\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)\s*\)$`).FindStringSubmatch(v); m != nil {
			var r, g, b int
			fmt.Sscanf(m[1], "%d", &r)
			fmt.Sscanf(m[2], "%d", &g)
			fmt.Sscanf(m[3], "%d", &b)
			return fmt.Sprintf("#%02x%02x%02x", r, g, b), true
		}
		if strings.HasPrefix(v, "#") {
			name = v
			continue
		}
		return "", false
	}
	return "", false
}

// parseRootTokens collects the custom properties the browser will have in scope,
// with the cascade applied: the base sheet first, then the inlined scoped chunk,
// then the inlined frozen layer, each later declaration winning. That is exactly
// the precedence the three stylesheets have in the document.
func parseRootTokens(css string) map[string]string {
	out := map[string]string{}
	for _, block := range regexp.MustCompile(`(?s):root\s*\{([^}]*)\}`).FindAllStringSubmatch(stripComments(css), -1) {
		for _, decl := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;{}]+)`).FindAllStringSubmatch(block[1], -1) {
			out[decl[1]] = strings.TrimSpace(decl[2])
		}
	}
	return out
}

func mainBody(html string) string {
	if m := reMain.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return ""
}

// The eight fixes. Order follows design-tokens.md §3.
func a11yFixes() []a11yFix {
	return []a11yFix{
		{
			item: "§3.1", what: "prefers-reduced-motion is honoured",
			check: func(d document) error {
				blocks := cssBlocks(d.css, reMediaRM)
				if len(blocks) == 0 {
					return fmt.Errorf("no @media (prefers-reduced-motion: reduce) block in the served CSS")
				}
				// The block must neutralise motion, not merely exist.
				body := strings.Join(blocks, "\n")
				if !strings.Contains(body, "transition-duration") && !strings.Contains(body, "animation-duration") {
					return fmt.Errorf("the reduced-motion blocks neutralise neither transition-duration nor animation-duration")
				}
				// And nothing may opt back out: if the sheets animate anything,
				// a reduced-motion block that only caps transitions leaves the
				// animation running.
				if regexp.MustCompile(`@keyframes`).MatchString(d.css) &&
					!strings.Contains(body, "animation-duration") {
					return fmt.Errorf("@keyframes exists but no reduced-motion block caps animation-duration")
				}
				return nil
			},
			breakDocs: []func(document) document{
				func(d document) document {
					// The block disappears, so motion is unconditional again.
					d.css = reMediaRM.ReplaceAllString(d.css, "@media print {")
					return d
				},
				func(d document) document {
					// The block survives but shortens nothing: the "the block
					// exists, so we are accessible" failure.
					d.css = strings.ReplaceAll(d.css, "transition-duration:.001ms!important", "transition-delay:0s")
					d.css = strings.ReplaceAll(d.css, "animation-duration:.001ms!important", "animation-delay:0s")
					return d
				},
			},
			mutations: []string{
				"the reduced-motion at-rule is replaced, so motion is unconditional again",
				"the at-rule survives but neutralises neither transition-duration nor animation-duration",
			},
		},
		{
			item: "§3.2", what: "explicit :focus-visible ring",
			check: func(d document) error {
				found := false
				for _, m := range reRule.FindAllStringSubmatch(d.css, -1) {
					if !hasPseudo(m[1], ":focus-visible") {
						continue
					}
					if strings.Contains(m[2], "outline") || strings.Contains(m[2], "box-shadow") {
						found = true
					}
					// A ring that is only ever drawn on :focus-visible cannot be
					// suppressed without also suppressing the base outline, so
					// "none" here is a removal, not a reset.
					if regexp.MustCompile(`outline\s*:\s*(none|0)\s*(;|$)`).MatchString(m[2]) {
						return fmt.Errorf("a :focus-visible rule removes the outline")
					}
				}
				if !found {
					return fmt.Errorf("no :focus-visible rule declares an outline or box-shadow")
				}
				// The skip link is parked off-screen by default and the base
				// sheet reveals it on plain :focus, so that is the state whose
				// ring has to be stated rather than inherited.
				skip := false
				for _, m := range reRule.FindAllStringSubmatch(d.css, -1) {
					if !strings.Contains(m[1], ".fallback-skip") || !hasPseudo(m[1], ":focus") {
						continue
					}
					if strings.Contains(m[2], "outline") {
						skip = true
					}
				}
				if !skip {
					return fmt.Errorf("the off-screen skip link has no ring on :focus of its own")
				}
				return nil
			},
			breakDocs: []func(document) document{
				func(d document) document {
					d.css = strings.ReplaceAll(d.css, ":focus-visible", ":focus-visible-disabled")
					return d
				},
				func(d document) document {
					// The skip link keeps its reveal rule but loses the ring,
					// which is the state the baseline shipped in.
					d.css = strings.ReplaceAll(d.css, ".fallback-skip:focus", ".fallback-skip:focusable")
					return d
				},
			},
			mutations: []string{
				"every :focus-visible in the served CSS is renamed, so no explicit ring is declared",
				".fallback-skip:focus is renamed while .fallback-skip:focus-visible still exists, which is the state the baseline shipped",
			},
		},
		{
			item: "§3.3", what: "aria-current=\"page\" on the active nav item",
			check: func(d document) error {
				route := pathKey(d.route)
				marked := reMarkedA.FindAllString(d.html, -1)
				// (1) the marker belongs on a link, and on a link inside a nav
				// region: an aria-current stranded on a paragraph describes
				// nothing a screen reader can navigate to.
				if n := strings.Count(d.html, `aria-current="page"`); n != len(marked) {
					return fmt.Errorf(`%d aria-current="page" values but only %d of them are on an <a>`, n, len(marked))
				}
				navs := reNav.FindAllStringSubmatch(d.html, -1)
				if len(navs) == 0 {
					return fmt.Errorf("no <nav> region in the document")
				}
				inNav := 0
				for _, nav := range navs {
					n := 0
					for _, l := range reLinkTag.FindAllString(nav[1], -1) {
						if strings.Contains(l, `aria-current="page"`) {
							n++
						}
					}
					if n > 1 {
						return fmt.Errorf("one <nav> marks %d of its own links as the current page", n)
					}
					inNav += n
				}
				if inNav != len(marked) {
					return fmt.Errorf("%d of %d current-page markers sit outside a <nav>", len(marked)-inNav, len(marked))
				}
				// (2) and it has to name the page actually being served. The
				// patch prefix is dropped here because the patch switcher marks
				// the patched URL, which is the same page.
				for _, l := range marked {
					m := reHrefAttr.FindStringSubmatch(l)
					if m == nil {
						return fmt.Errorf("a current-page marker has no href: %s", l)
					}
					if pathKey(m[1]) != route {
						return fmt.Errorf("the nav marks %q as the current page on route %q", m[1], d.route)
					}
				}
				// (3) the nav must mark the route at all when it lists a link to
				// exactly that page. Routes whose only nav match is the brand --
				// the logo link on / -- are the exception, because the brand is
				// not one of the nav's page items.
				site := reSiteNav.FindStringSubmatch(d.html)
				if site == nil {
					return fmt.Errorf(`no <nav class="bar" aria-label="Site"> region`)
				}
				for _, l := range reLinkTag.FindAllString(site[1], -1) {
					if strings.Contains(l, `class="brand"`) {
						continue
					}
					m := reHrefAttr.FindStringSubmatch(l)
					if m == nil || !samePage(m[1], d.route) {
						continue
					}
					if !strings.Contains(l, `aria-current="page"`) {
						return fmt.Errorf("the site nav links to %q but does not mark it as the current page", m[1])
					}
				}
				return nil
			},
			breakDocs: []func(document) document{
				func(d document) document {
					// (a) the marker is not emitted at all. The shell writes it
					// with spaces on both sides.
					d.html = strings.Replace(d.html, ` aria-current="page" `, " ", 1)
					return d
				},
				func(d document) document {
					// (b) a marker is emitted, but on a different page.
					d.html = strings.Replace(d.html, `<a class="link" href="/explore" `,
						`<a class="link" href="/explore" aria-current="page" `, 1)
					return d
				},
				func(d document) document {
					// (c) the marker keeps a query but is moved to another
					// page's path. The explorer's own switcher marks
					// /explore?patch=... , so the query has to be ignored to
					// read that as /explore -- this control proves ignoring it
					// did not also stop the path from being compared.
					d.html = reMarkedA.ReplaceAllStringFunc(d.html, func(tag string) string {
						return strings.Replace(tag, `href="/explore?`, `href="/matchups/mid?`, 1)
					})
					return d
				},
			},
			mutations: []string{
				"the aria-current=\"page\" marker is not emitted at all",
				"a marker is emitted on /explore regardless of the page being served",
				"a query-carrying marker is emitted on another page's path",
			},
		},
		{
			item: "§3.4", what: "tabular-nums on numeric and table cells",
			check: func(d document) error {
				n := strings.Count(d.css, "tabular-nums")
				if n == 0 {
					return fmt.Errorf("no tabular-nums in the served CSS")
				}
				// Numeric cells have to exist for the rule to matter, and the
				// rule has to reach a cell selector rather than only a caption.
				if strings.Contains(mainBody(d.html), "<td") {
					hit := false
					for _, m := range reRule.FindAllStringSubmatch(d.css, -1) {
						if !strings.Contains(m[2], "tabular-nums") {
							continue
						}
						if strings.Contains(m[1], "td") || strings.Contains(m[1], "cell") ||
							strings.Contains(m[1], "num") || strings.Contains(m[1], "th") {
							hit = true
						}
					}
					if !hit {
						return fmt.Errorf("the page has table cells but no tabular-nums rule reaches a cell selector")
					}
				}
				return nil
			},
			breakDocs: []func(document) document{func(d document) document {
				d.css = strings.ReplaceAll(d.css, "tabular-nums", "proportional-nums")
				return d
			}},
			mutations: []string{
				"tabular-nums is replaced by proportional-nums",
			},
		},
		{
			item: "§3.5", what: "the page title is an <h1> and it comes first",
			check: func(d document) error {
				body := mainBody(d.html)
				if body == "" {
					return fmt.Errorf("no <main id=\"main\"> region")
				}
				if n := len(reH1.FindAllString(body, -1)); n != 1 {
					return fmt.Errorf("%d <h1> elements in <main>, want exactly 1", n)
				}
				heads := reAnyHead.FindAllString(body, -1)
				if len(heads) == 0 {
					return fmt.Errorf("no heading elements in <main>")
				}
				if !strings.HasPrefix(heads[0], "<h1") {
					return fmt.Errorf("the first heading is %q, not an <h1>", heads[0])
				}
				return nil
			},
			breakDocs: []func(document) document{func(d document) document {
				d.html = strings.Replace(d.html, "<h1", "<h2", 1)
				return d
			}, func(d document) document {
				// The title is an h1 but no longer the first heading, which is
				// the shape 9 of 10 upstream pages shipped in.
				d.html = strings.Replace(d.html, `<main id="main">`,
					`<main id="main"><h2>A section before the title</h2>`, 1)
				return d
			}},
			mutations: []string{
				"the page title becomes an <h2>",
				"a heading is inserted before the <h1>",
			},
		},
		{
			item: "§3.6", what: "landmarks, labelled nav, and a working skip link",
			check: func(d document) error {
				for _, want := range []string{"<header", "<footer", `<nav class="bar" aria-label="Site"`, `<main id="main">`} {
					if !strings.Contains(d.html, want) {
						return fmt.Errorf("missing landmark %q", want)
					}
				}
				m := regexp.MustCompile(`<a class="fallback-skip" href="#([^"]+)">`).FindStringSubmatch(d.html)
				if m == nil {
					return fmt.Errorf("no skip link")
				}
				if !strings.Contains(d.html, `id="`+m[1]+`"`) {
					return fmt.Errorf("the skip link targets #%s, which does not exist", m[1])
				}
				// A skip link that is display:none is not a skip link.
				for _, mm := range reRule.FindAllStringSubmatch(d.css, -1) {
					if !strings.Contains(mm[1], "fallback-skip") {
						continue
					}
					if regexp.MustCompile(`display\s*:\s*none`).MatchString(mm[2]) {
						return fmt.Errorf("a .fallback-skip rule hides it with display:none")
					}
				}
				return nil
			},
			breakDocs: []func(document) document{func(d document) document {
				d.html = strings.Replace(d.html, `href="#main"`, `href="#no-such-target"`, 1)
				return d
			}},
			mutations: []string{
				"the skip link points at a target that does not exist",
			},
		},
		{
			item: "§3.7", what: "no overflow-x:hidden on body (wide tables must scroll)",
			check: func(d document) error {
				for _, m := range reRule.FindAllStringSubmatch(d.css, -1) {
					if !regexp.MustCompile(`(^|[\s,>])body\b`).MatchString(m[1]) {
						continue
					}
					if regexp.MustCompile(`overflow(-x)?\s*:\s*hidden`).MatchString(m[2]) {
						return fmt.Errorf("body sets overflow-x:hidden in %q", strings.TrimSpace(m[1]))
					}
				}
				// The requirement is not "no clipping" but "scrolling instead",
				// so the scroll container has to exist.
				if !regexp.MustCompile(`overflow-x\s*:\s*auto`).MatchString(d.css) {
					return fmt.Errorf("no overflow-x:auto scroll container anywhere in the served CSS")
				}
				return nil
			},
			breakDocs: []func(document) document{func(d document) document {
				d.css += "body{overflow-x:hidden}"
				return d
			}},
			mutations: []string{
				"body gains overflow-x:hidden",
			},
		},
		{
			item: "§3.8", what: "every text/background pair the served CSS creates meets its floor",
			// The values are read out of the served CSS rather than quoted here,
			// so this measures what ships. A hard-coded table would keep passing
			// after somebody re-declared --text-muted as §1's literal #666, which
			// on --bg-color is 4.25:1 and fails AA -- the exact regression §3.8's
			// "prefer darker for small text" note exists to prevent.
			check: func(d document) error {
				pairs := []struct {
					what, fg, bg string
					min          float64
				}{
					{"body ink on paper", "--text-color", "--bg-color", 4.5},
					{"body ink on surface", "--text-color", "--surface", 4.5},
					{"muted on paper (the tightest pair in the language)", "--text-muted", "--bg-color", 4.5},
					{"muted on surface", "--text-muted", "--surface", 4.5},
					{"muted-alt on paper", "--text-muted-alt", "--bg-color", 4.5},
					{"muted-alt on surface", "--text-muted-alt", "--surface", 4.5},
					{"accent link on paper", "--accent-color", "--bg-color", 4.5},
					{"accent link on surface", "--accent-color", "--surface", 4.5},
					{"accent card title on hover", "--fallback-accent", "--surface", 4.5},
					{"heading on paper", "--primary-color", "--bg-color", 4.5},
					{"heading on surface", "--primary-color", "--surface", 4.5},
					{"tier ink on S+ badge", "--tier-ink", "--tier-splus-bg", 4.5},
					{"tier ink on S badge", "--tier-ink", "--tier-s-bg", 4.5},
					{"tier ink on A badge", "--tier-ink", "--tier-a-bg", 4.5},
					{"tier ink on B badge", "--tier-ink", "--tier-b-bg", 4.5},
					{"tier ink on C badge", "--tier-ink", "--tier-c-bg", 4.5},
					{"tier ink on D badge", "--tier-ink", "--tier-d-bg", 4.5},
					{"tier ink on up cell", "--tier-ink", "--dev-up-bg", 4.5},
					{"tier ink on up-strong cell", "--tier-ink", "--dev-up-bg-strong", 4.5},
					{"tier ink on down cell", "--tier-ink", "--dev-down-bg", 4.5},
					{"tier ink on down-strong cell", "--tier-ink", "--dev-down-bg-strong", 4.5},
					{"tier ink on empty cell", "--tier-ink", "--dev-empty-bg", 4.5},
					{"row-head hover on the dark island", "--accent-on-dark", "--primary-color", 4.5},
					{"island label on the dark island", "--text-on-dark", "--primary-color", 4.5},
					{"island muted label on the dark island", "--text-on-dark-muted", "--primary-color", 4.5},
				}
				for _, p := range pairs {
					fg, ok := d.hexOf(p.fg)
					if !ok {
						return fmt.Errorf("%s: %s does not resolve to a colour in the served CSS", p.what, p.fg)
					}
					bg, ok := d.hexOf(p.bg)
					if !ok {
						return fmt.Errorf("%s: %s does not resolve to a colour in the served CSS", p.what, p.bg)
					}
					r, err := colourRatio(fg, bg)
					if err != nil {
						return fmt.Errorf("%s: %w", p.what, err)
					}
					if r < p.min {
						return fmt.Errorf("%s (%s on %s): %.2f:1, below the %.1f floor",
							p.what, fg, bg, r, p.min)
					}
				}

				// The provenance banner carries its colours as an inline style
				// written by render.go, so it is the one surface whose colours
				// the stylesheets cannot constrain. Read them out of the markup.
				m := regexp.MustCompile(`class="state-banner[^"]*"[^>]*style="([^"]*)"`).FindStringSubmatch(d.html)
				if m == nil {
					return fmt.Errorf("no provenance banner with an inline style in the served markup")
				}
				bgHex := cssColour(m[1], "background")
				fgHex := cssColour(m[1], "color")
				if bgHex == "" || fgHex == "" {
					return fmt.Errorf("the provenance banner style %q does not state both colours", m[1])
				}
				r, err := colourRatio(fgHex, bgHex)
				if err != nil {
					return fmt.Errorf("provenance banner: %w", err)
				}
				if r < 4.5 {
					return fmt.Errorf("provenance banner text %s on %s: %.2f:1, below the 4.5 floor",
						fgHex, bgHex, r)
				}
				// The banner's own link must also clear the floor on the
				// banner's background, because render.go's inline style is the
				// only thing setting that background.
				if link := regexp.MustCompile(`class="state-banner__link"[^>]*>\s*<a[^>]*>`).MatchString(d.html); link {
					// .state-banner__link a is {color:inherit}, so the ink is
					// fgHex and the check above already covers it. Asserting the
					// inherit here keeps a future rule that hard-codes the
					// accent from silently dropping the pair to 2.58:1.
					if !regexp.MustCompile(`\.state-banner__link a[^{]*\{[^}]*color\s*:\s*inherit`).MatchString(d.css) {
						return fmt.Errorf("the provenance banner link no longer inherits its colour, so it no longer clears its floor")
					}
				}
				return nil
			},
			breakDocs: []func(document) document{
				func(d document) document {
					// §1's literal #666, which is 4.25:1 on paper: the
					// regression the served #615f57 was chosen to avoid.
					d.css = strings.ReplaceAll(d.css, "--text-muted: #615f57", "--text-muted: #666666")
					return d
				},
				func(d document) document {
					// An accent-as-text rule that stops inheriting, i.e. the
					// upstream bug where the link hover halves its contrast.
					d.css = strings.ReplaceAll(d.css, "color:inherit;font-weight:700",
						"color:var(--accent-color);font-weight:700")
					return d
				},
			},
			mutations: []string{
				"--text-muted is re-declared as §1's literal #666, which measures 4.25:1 on the page field",
				"the provenance banner link stops inheriting and hard-codes the accent",
			},
		},
	}
}

func TestServedA11yContract(t *testing.T) {
	renderer := newFixtureRenderer(t)

	routes := []struct {
		name string
		path string
		page func(*Renderer) (*Page, error)
	}{
		{"home", "/", func(r *Renderer) (*Page, error) { return r.HomePage() }}, // route comes from the page
		{"tier-list-top", "/tier-list/top", func(r *Renderer) (*Page, error) {
			return r.TierListPage("top", DefaultTierListQuery(), true)
		}},
		{"tier-list-mid", "/tier-list/mid", func(r *Renderer) (*Page, error) {
			return r.TierListPage("mid", DefaultTierListQuery(), true)
		}},
		{"patch-tier-list-top", "/patch/16.18/tier-list/top", func(r *Renderer) (*Page, error) {
			return r.PatchTierListPage("top", "16.18", DefaultTierListQuery(), true)
		}},
		{"champion", "/champions/ahri", func(r *Renderer) (*Page, error) { return r.ChampionPage("ahri") }},
		{"champion-mid", "/champions/ahri/mid", func(r *Renderer) (*Page, error) {
			return r.ChampionRolePage("ahri", "mid")
		}},
		{"matchups-top", "/matchups/top", func(r *Renderer) (*Page, error) {
			return r.MatchupsPage("top", DefaultMatchupQuery(), true)
		}},
		{"matchups-mid", "/matchups/mid", func(r *Renderer) (*Page, error) {
			return r.MatchupsPage("mid", DefaultMatchupQuery(), true)
		}},
		{"explore", "/explore", func(r *Renderer) (*Page, error) {
			return r.ExplorePage(url.Values{"per": {"30"}}, true)
		}},
		{"about", "/about", func(r *Renderer) (*Page, error) { return r.AboutPage() }},
		{"legal-terms", "/legal/terms", func(r *Renderer) (*Page, error) { return r.TermsPage() }},
		{"legal-privacy", "/legal/privacy", func(r *Renderer) (*Page, error) { return r.PrivacyPage() }},
		{"disclaimer", "/disclaimer", func(r *Renderer) (*Page, error) { return r.DisclaimerPage() }},
	}

	docs := make([]document, 0, len(routes))
	for _, rt := range routes {
		page, err := rt.page(renderer)
		if err != nil {
			t.Fatalf("%s: build page: %v", rt.name, err)
		}
		var buf bytes.Buffer
		if err := renderer.Render(&buf, page); err != nil {
			t.Fatalf("%s: render: %v", rt.name, err)
		}
		// The order here is the order the browser sees: the external base
		// sheet, the inlined scoped chunk, the inlined frozen layer. :root
		// declarations in a later sheet win, which is how the frozen layer
		// takes over the palette. Comments are dropped from both halves because
		// the browser ignores them and the checks must measure what it applies.
		docs = append(docs, withTokens(document{
			route: page.Active,
			html:  markup(buf.String()),
			css:   stripComments(string(baseCSS()) + "\n" + string(scopedCSS(page.Champion)) + "\n" + frozenCSS()),
		}))
	}

	for _, fix := range a11yFixes() {
		t.Run(fix.item, func(t *testing.T) {
			// Positive control first: every mutation must be rejected by the
			// check on at least one route, otherwise the clause it targets is
			// untested and a green run means nothing.
			for i, breakIt := range fix.breakDocs {
				broke := false
				route := ""
				for _, d := range docs {
					if err := fix.check(withTokens(breakIt(d))); err != nil {
						broke = true
						route = d.route
						break
					}
				}
				name := fmt.Sprintf("mutation %d", i+1)
				if i < len(fix.mutations) && fix.mutations[i] != "" {
					name = fix.mutations[i]
				}
				if !broke {
					t.Errorf("VACUOUS CHECK: %s (%s) accepts %s, so it does not test what it claims",
						fix.item, fix.what, name)
					continue
				}
				t.Logf("control rejected on %s: %s", route, name)
			}

			for _, d := range docs {
				if err := fix.check(d); err != nil {
					t.Errorf("%s %s on %s: %v", fix.item, fix.what, d.route, err)
				}
			}
		})
	}
}
