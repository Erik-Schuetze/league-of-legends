package webtier

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The render-parity test is the gate this tier has to pass: for a route the
// reference build published, the Go renderer must produce the same bytes,
// normalising only for the differences the two engines are known to make in
// character escaping. It is deliberately a byte comparison rather than a DOM
// comparison - a DOM comparison cannot see a lost `n`, a dropped `aria-current`
// or a reworded provenance banner, which is exactly what this port can break.
//
// It is skipped when web/dist is absent, so it runs in CI only on a checkout
// that built the reference site, and locally wherever one exists.

// ReferenceDir is the published build the tier is compared against.
func referenceDir(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{
		filepath.Join("..", "..", "web", "dist"),
		filepath.Join("..", "..", "..", "web", "dist"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	t.Skip("web/dist is not present: run `npm run build` in web/ to enable render-parity checks")
	return ""
}

// newFixtureRenderer builds a renderer over the checked-in fixtures, which is
// the snapshot the published build in web/dist was rendered from.
func newFixtureRenderer(t *testing.T) *Renderer {
	t.Helper()
	opts := OptionsFromEnv()
	opts.FixturesMode = FixturesOnly
	loader := NewLoader(opts)
	renderer, err := NewRenderer(loader, DefaultSiteURL)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	if _, err := loader.Site(); err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	return renderer
}

// normaliseEscaping rewrites the escapes the two template engines spell
// differently, and nothing else. html/template escapes a double quote, an
// apostrophe, a plus and an equals sign inside text and attribute values;
// Astro's escaper leaves most of them literal. Every rewrite is applied to both
// sides, so it cannot hide a dropped node, a lost count or a reworded sentence -
// only a change in how one character is spelled.
func normaliseEscaping(html string) string {
	replacer := strings.NewReplacer(
		"&#34;", `"`,
		"&quot;", `"`,
		"&#39;", "'",
		"&#43;", "+",
		"&#61;", "=",
	)
	return replacer.Replace(html)
}

type parityCase struct {
	name  string
	dist  string
	build func(*Renderer) (*Page, error)
	// interactive marks the routes whose deployed page is the reference page
	// plus the no-JS filter bar. The distinction matters: on a static route any
	// difference at all is a port regression, while on an interactive route
	// exactly one inserted element is expected and everything around it must
	// still be byte-identical.
	interactive bool
}

// The no-JS filter bar is the one element the deployed tier adds to a reference
// page. It is emitted by templates/interactive.tmpl from the design system's
// FilterBar component - which web/src defines, styles into every page's
// stylesheet, and no page ever renders, because the static build had no way to
// serve a query. Removing exactly this element, and only when it is present, is
// the entire allowance the interactive routes get.
const noJSBarOpen = `<form class="ds-filter-bar ds-print-hidden"`

const noJSBarClose = `</form>`

// removeNoJSBar returns the document with the first filter bar removed, whether
// a bar was there, and the bar itself. The bar contains no nested form, so the
// first closing tag after it terminates it.
func removeNoJSBar(html string) (string, bool, string) {
	start := strings.Index(html, noJSBarOpen)
	if start < 0 {
		return html, false, ""
	}
	end := strings.Index(html[start:], noJSBarClose)
	if end < 0 {
		return html, false, ""
	}
	end += start + len(noJSBarClose)
	return html[:start] + html[end:], true, html[start:end]
}

// parityCases covers the routes the port claims parity for. `page` is the whole
// document, so the comparison also covers the shell, the banner, the nav and the
// footer on every route.
func parityCases(t *testing.T) []parityCase {
	t.Helper()
	return []parityCase{
		{name: "home", dist: "index.html", build: func(r *Renderer) (*Page, error) { return r.HomePage() }},
		{name: "about", dist: "about/index.html", build: func(r *Renderer) (*Page, error) { return r.AboutPage() }},
		{name: "disclaimer", dist: "disclaimer/index.html", build: func(r *Renderer) (*Page, error) { return r.DisclaimerPage() }},
		{name: "tier-list-mid", dist: "tier-list/mid/index.html", interactive: true, build: func(r *Renderer) (*Page, error) {
			return r.TierListPage("mid", DefaultTierListQuery(), false)
		}},
		{name: "patch-tier-list-mid", dist: "patch/16.18/tier-list/mid/index.html", interactive: true, build: func(r *Renderer) (*Page, error) {
			return r.PatchTierListPage("mid", "16.18", DefaultTierListQuery(), false)
		}},
		{name: "matchups-mid", dist: "matchups/mid/index.html", build: func(r *Renderer) (*Page, error) {
			return r.MatchupsPage("mid", DefaultMatchupQuery(), false)
		}},
		{name: "legal-terms", dist: "legal/terms/index.html", build: func(r *Renderer) (*Page, error) { return r.TermsPage() }},
		{name: "legal-privacy", dist: "legal/privacy/index.html", build: func(r *Renderer) (*Page, error) { return r.PrivacyPage() }},
		{name: "champions-ahri", dist: "champions/ahri/index.html", build: func(r *Renderer) (*Page, error) {
			return r.ChampionPage("ahri")
		}},
		{name: "champions-ahri-mid", dist: "champions/ahri/mid/index.html", build: func(r *Renderer) (*Page, error) {
			return r.ChampionRolePage("ahri", "mid")
		}},
		{name: "champions-ahri-top", dist: "champions/ahri/top/index.html", build: func(r *Renderer) (*Page, error) {
			return r.ChampionRolePage("ahri", "top")
		}},
	}
}

func TestRenderParity(t *testing.T) {
	dir := referenceDir(t)
	renderer := newFixtureRenderer(t)
	for _, testCase := range parityCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, testCase.dist))
			if err != nil {
				t.Fatalf("read reference %s: %v", testCase.dist, err)
			}
			page, err := testCase.build(renderer)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var got bytes.Buffer
			if err := renderer.Render(&got, page); err != nil {
				t.Fatalf("shell: %v", err)
			}
			if equalIgnoringEscaping(want, got.Bytes()) {
				return
			}
			t.Errorf("render mismatch for %s (%d reference bytes, %d rendered bytes)\n%s",
				testCase.name, len(want), got.Len(), firstDifference(normaliseEscaping(string(want)), normaliseEscaping(got.String())))
		})
	}
}

func equalIgnoringEscaping(want []byte, got []byte) bool {
	return normaliseEscaping(string(want)) == normaliseEscaping(string(got))
}

// TestInteractiveRenderParity pins the variant the tier actually serves for the
// two tier-list routes: the reference page with the no-JS filter bar inserted.
// Both halves are asserted - the bar is present, and removing it leaves the
// published bytes untouched - so neither a missing bar (the page would stop
// being sortable, filterable or paginated without JavaScript) nor a changed
// table (the port would stop being a port) can pass.
func TestInteractiveRenderParity(t *testing.T) {
	dir := referenceDir(t)
	renderer := newFixtureRenderer(t)

	cases := []struct {
		name  string
		dist  string
		build func(*Renderer) (*Page, error)
	}{
		{name: "tier-list-mid", dist: "tier-list/mid/index.html", build: func(r *Renderer) (*Page, error) {
			return r.TierListPage("mid", DefaultTierListQuery(), true)
		}},
		{name: "patch-tier-list-mid", dist: "patch/16.18/tier-list/mid/index.html", build: func(r *Renderer) (*Page, error) {
			return r.PatchTierListPage("mid", "16.18", DefaultTierListQuery(), true)
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, testCase.dist))
			if err != nil {
				t.Fatalf("read reference %s: %v", testCase.dist, err)
			}
			if reference, found, _ := removeNoJSBar(string(want)); found {
				t.Fatalf("the reference %s already contains a filter bar; the allowance would hide a real difference (%d bytes)", testCase.dist, len(reference))
			}
			page, err := testCase.build(renderer)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var got bytes.Buffer
			if err := renderer.Render(&got, page); err != nil {
				t.Fatalf("shell: %v", err)
			}
			stripped, found, bar := removeNoJSBar(got.String())
			if !found {
				t.Fatal("the served variant has no filter bar: the page would not be sortable, filterable or paginated without JavaScript")
			}
			if !strings.Contains(bar, `method="get"`) {
				t.Errorf("filter bar is not a GET form, so its selections are not URLs:\n%s", bar)
			}
			if equalIgnoringEscaping(want, []byte(stripped)) {
				return
			}
			t.Errorf("interactive render does not reduce to the reference for %s (%d reference bytes, %d served bytes)\n%s",
				testCase.name, len(want), len(stripped), firstDifference(normaliseEscaping(string(want)), normaliseEscaping(stripped)))
		})
	}
}

// firstDifference reports the first differing offset with context, which is what
// makes a failed parity run actionable.
func firstDifference(want string, got string) string {
	limit := len(want)
	if len(got) < limit {
		limit = len(got)
	}
	for i := 0; i < limit; i++ {
		if want[i] != got[i] {
			return snippet(i, want, got)
		}
	}
	if len(want) != len(got) {
		return snippet(limit, want, got)
	}
	return "identical after normalisation"
}

func snippet(at int, want string, got string) string {
	window := func(s string) string {
		start := at - 90
		if start < 0 {
			start = 0
		}
		end := at + 130
		if end > len(s) {
			end = len(s)
		}
		return s[start:end]
	}
	return "\n first differing offset: " + itoa(at) +
		"\n reference: ..." + window(want) + "...\n rendered:  ..." + window(got) + "..."
}

// TestFeedParity compares the two generated non-HTML documents against the
// published build. They are not pages, so they are checked separately from the
// shell comparison above, but they are still byte comparisons for the same
// reason: a sitemap that quietly drops a route, or a robots.txt whose Sitemap
// line names a host the site is not served from, is a real fault that a
// structural check would not catch.
func TestFeedParity(t *testing.T) {
	dir := referenceDir(t)
	renderer := newFixtureRenderer(t)

	cases := []struct {
		name string
		dist string
		got  func() ([]byte, error)
	}{
		{name: "sitemap", dist: "sitemap.xml", got: renderer.Sitemap},
		{name: "robots", dist: "robots.txt", got: renderer.Robots},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, testCase.dist))
			if err != nil {
				t.Fatalf("read reference %s: %v", testCase.dist, err)
			}
			got, err := testCase.got()
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("feed mismatch for %s (%d reference bytes, %d rendered bytes)\n%s",
					testCase.name, len(want), len(got), firstDifference(string(want), string(got)))
			}
		})
	}
}

// TestRiotTokenIsUnpublishedByDefault pins the honest default: with no token
// configured, /riot.txt does not exist, because publishing an empty file there
// would assert a verification Riot has not performed.
func TestRiotTokenIsUnpublishedByDefault(t *testing.T) {
	renderer := newFixtureRenderer(t)
	if body, found := renderer.RiotToken(); found {
		t.Errorf("riot.txt published without a token: %q", body)
	}
	renderer.SetRiotToken("  abc123  ")
	body, found := renderer.RiotToken()
	if !found {
		t.Fatal("riot.txt not published after a token was set")
	}
	if string(body) != "abc123\n" {
		t.Errorf("riot.txt body = %q, want %q", body, "abc123\n")
	}
}
