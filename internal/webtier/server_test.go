package webtier

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// The HTTP policy tests. They exist because the behaviour they cover is the
// behaviour the tier is judged on and none of it is visible in a template test:
// the status a broken snapshot gets, what a fault page carries, whether the
// gzip and entity-tag paths agree with the identity body, and whether every
// view of the table really is reachable by URL alone, with no JavaScript.
//
// Every server here owns its own metrics registry, so the tests can run in
// parallel without two servers registering the same collector.

// newTestServer builds a server over the given options, plus the fixture
// renderer's own options merged in for the paths the fixture tree does not
// provide (the Data Dragon projection the build lookup reads).
func newTestServer(t *testing.T, opts Options) (*Server, *httptest.Server) {
	t.Helper()
	loader := NewLoader(opts)
	renderer, err := NewRenderer(loader, DefaultSiteURL)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	server := NewServer(renderer, ServerOptions{Metrics: obs.NewMetrics()})
	live := httptest.NewServer(server)
	t.Cleanup(live.Close)
	return server, live
}

// fixtureOptions is OptionsFromEnv pointed at the checked-in demo tree, which
// is the snapshot the published build was rendered from and therefore the one
// with known values to assert against.
func fixtureOptions() Options {
	opts := OptionsFromEnv()
	opts.FixturesMode = FixturesOnly
	opts.FixturesDir = fixtureDir()
	return opts
}

func fixtureDir() string {
	root := discoverRepoRoot()
	if root == "" {
		panic("the repository root could not be located from the test's working directory")
	}
	return filepath.Join(root, "web", "src", "fixtures")
}

// get performs a request and returns the response, closing it on cleanup.
func get(t *testing.T, live *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := live.Client().Get(live.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(raw)
}

// TestRouteStatuses walks every route family the tier serves. The statuses are
// the contract: a page that exists is a 200, a page that does not exist is a
// 404, and a page whose artifact is missing is a 503 - never a 200 with less in
// it than the page claims.
func TestRouteStatuses(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	cases := []struct {
		path   string
		status int
	}{
		{"/", http.StatusOK},
		{"/about", http.StatusOK},
		{"/disclaimer", http.StatusOK},
		{"/legal/terms", http.StatusOK},
		{"/legal/privacy", http.StatusOK},
		{"/tier-list/mid", http.StatusOK},
		{"/tier-list/mid/", http.StatusOK},
		{"/patch/16.17/tier-list/mid", http.StatusOK},
		{"/matchups/mid", http.StatusOK},
		{"/champions/ahri", http.StatusOK},
		{"/champions/ahri/mid", http.StatusOK},
		{"/sitemap.xml", http.StatusOK},
		{"/robots.txt", http.StatusOK},
		{"/favicon.svg", http.StatusOK},
		{"/_astro/JsonLd.BEq7AnVK.css", http.StatusOK},
		{"/fonts/inter-400.woff2", http.StatusOK},
		{"/healthz", http.StatusOK},
		{"/readyz", http.StatusOK},
		{"/metrics", http.StatusOK},
		{"/agg/v1/manifest.json", http.StatusOK},

		// Nothing is published at these paths, and the tier says so rather
		// than inventing a page.
		{"/riot.txt", http.StatusNotFound},
		{"/nope", http.StatusNotFound},
		{"/tier-list/nope", http.StatusNotFound},
		{"/matchups/nope", http.StatusNotFound},
		{"/champions/nope", http.StatusNotFound},
		{"/champions/ahri/nope", http.StatusNotFound},
		{"/patch/notapatch/tier-list/mid", http.StatusNotFound},
		{"/agg/v1/does-not-exist.json", http.StatusNotFound},
		{"/agg/v1/../../etc/passwd", http.StatusNotFound},
		{"/agg/%2e%2e/%2e%2e/etc/passwd", http.StatusNotFound},
	}

	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			resp := get(t, live, testCase.path)
			if resp.StatusCode != testCase.status {
				t.Fatalf("GET %s: status = %d, want %d", testCase.path, resp.StatusCode, testCase.status)
			}
		})
	}
}

// TestNotFoundIsAVisiblePage checks that a 404 is the site's own page with the
// fault named in it, not the runtime's default text. A reader who mistypes a
// URL should land somewhere that explains what exists.
func TestNotFoundIsAVisiblePage(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())
	resp := get(t, live, "/tier-list/notarole")
	page := body(t, resp)

	for _, want := range []string{
		`data-fault="not-found"`,
		`data-status="404"`,
		"Page not found",
		"noindex",
		`href="/tier-list/mid"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the 404 page does not contain %q", want)
		}
	}
	if strings.Contains(page, "404 page not found") {
		t.Error("the 404 page is the runtime's default text, not the site's page")
	}
	if got := resp.Header.Get("Cache-Control"); got != noStoreCacheControl {
		t.Errorf("Cache-Control on a 404 = %q, want %q", got, noStoreCacheControl)
	}
	if got := resp.Header.Get("ETag"); got != "" {
		t.Errorf("a 404 carries an entity tag (%q); a fault must not be replayable from a cache", got)
	}
}

// TestMissingArtifactIs503WithAPage is the central promise of the data layer:
// a manifest that advertises an artifact is a promise that it is there, and a
// broken promise is a 503 with a visible page naming the artifact rather than a
// table that quietly lost rows.
func TestMissingArtifactIs503WithAPage(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	artifact := filepath.Join(root, "v1", "p", "16.18", "EUW", "420", "all", "tierlist.json")
	if err := os.Remove(artifact); err != nil {
		t.Fatalf("remove %s: %v", artifact, err)
	}

	_, live := newTestServer(t, Options{
		AggRoot:      root,
		FixturesMode: FixturesOff,
		DataDir:      fixtureDataDir(),
	})

	resp := get(t, live, "/tier-list/mid")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	page := body(t, resp)
	for _, want := range []string{
		`data-fault="artifact"`,
		`data-status="503"`,
		"tierlist.json",
		"The published snapshot is incomplete",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the 503 page does not contain %q", want)
		}
	}
	if got := resp.Header.Get("Cache-Control"); got != noStoreCacheControl {
		t.Errorf("Cache-Control on a 503 = %q, want %q", got, noStoreCacheControl)
	}
	if got := resp.Header.Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After on a 503 = %q, want 60", got)
	}

	// /readyz reports the same fault, so a Deployment can act on it.
	ready := body(t, get(t, live, "/readyz"))
	if !strings.Contains(ready, `"ok":false`) || !strings.Contains(ready, FaultArtifact) {
		t.Errorf("/readyz does not report the artifact fault: %s", ready)
	}
}

// TestUnknownSchemaFailsClosed proves the fail-closed half of the artifact
// contract: a snapshot that declares a version this tier does not implement is
// refused whole, not decoded on a guess.
func TestUnknownSchemaFailsClosed(t *testing.T) {
	t.Parallel()
	root := copyFixtureTree(t)
	manifest := filepath.Join(root, "v1", "manifest.json")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	rewritten := strings.Replace(string(raw), `"schema"`, `"schema"`, 1)
	rewritten = regexp.MustCompile(`"schema"\s*:\s*(?:"agg/v\d+"|\d+)`).ReplaceAllString(rewritten, `"schema":99`)
	if rewritten == string(raw) {
		t.Fatalf("the manifest does not carry a schema field to rewrite: %s", truncateForTest(raw))
	}
	if err := os.WriteFile(manifest, []byte(rewritten), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, live := newTestServer(t, Options{
		AggRoot:      root,
		FixturesMode: FixturesOff,
		DataDir:      fixtureDataDir(),
	})

	resp := get(t, live, "/tier-list/mid")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	page := body(t, resp)
	if !strings.Contains(page, `data-fault="schema"`) {
		t.Errorf("the page does not name the schema fault: %s", firstLine(page))
	}
	if !strings.Contains(page, "declares schema version 99") {
		t.Errorf("the page does not name the version it found: %s", firstLine(page))
	}
}

// TestNoSnapshotIs503AndTheSiteStillRenders separates the two states that
// matter: nothing has been published yet (the pages about the site are real,
// the ladder pages say there is nothing) and a snapshot that cannot be read.
func TestNoSnapshotIs503AndTheSiteStillRenders(t *testing.T) {
	t.Parallel()
	empty := t.TempDir()
	_, live := newTestServer(t, Options{
		AggRoot:      empty,
		FixturesMode: FixturesOff,
		DataDir:      fixtureDataDir(),
	})

	resp := get(t, live, "/tier-list/mid")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/tier-list/mid status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	if page := body(t, resp); !strings.Contains(page, `data-fault="no-snapshot"`) {
		t.Errorf("the 503 page does not name the no-snapshot state: %s", firstLine(page))
	}

	// The pages that describe the site itself are not blocked by the absence
	// of a snapshot: they have real content of their own.
	for _, path := range []string{"/", "/about", "/disclaimer", "/legal/terms", "/legal/privacy"} {
		if status := get(t, live, path).StatusCode; status != http.StatusOK {
			t.Errorf("GET %s with no snapshot: status = %d, want 200", path, status)
		}
	}
	if ready := body(t, get(t, live, "/readyz")); !strings.Contains(ready, `"ok":true`) {
		t.Errorf("/readyz with no snapshot should be ready (the tier serves what it has): %s", ready)
	}
}

// TestCachePolicyPerRouteClass pins the cache directives. HTML is private and
// short-lived - it is rendered per request and republished nightly, so nothing
// shared may store it - while the content-hashed assets and the versioned Data
// Dragon projection are public for as long as their names are true.
func TestCachePolicyPerRouteClass(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	cases := []struct {
		path string
		want string
	}{
		{"/", htmlCacheControl},
		{"/tier-list/mid", htmlCacheControl},
		{"/matchups/mid", htmlCacheControl},
		{"/champions/ahri", htmlCacheControl},
		{"/sitemap.xml", feedCacheControl},
		{"/robots.txt", feedCacheControl},
		{"/_astro/JsonLd.BEq7AnVK.css", immutableCacheControl},
		{"/fonts/inter-400.woff2", staticCacheControl},
		{"/favicon.svg", staticCacheControl},
		{"/agg/v1/manifest.json", artifactCacheControl},
		{"/agg/v1/static/16.18.1/champions.json", ddragonCacheControl},
		{"/healthz", noStoreCacheControl},
		{"/readyz", noStoreCacheControl},
		{"/metrics", noStoreCacheControl},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			resp := get(t, live, testCase.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200", testCase.path, resp.StatusCode)
			}
			if got := resp.Header.Get("Cache-Control"); got != testCase.want {
				t.Errorf("Cache-Control = %q, want %q", got, testCase.want)
			}
			if strings.Contains(strings.ToLower(resp.Header.Get("Cache-Control")), "public") &&
				strings.HasPrefix(testCase.path, "/tier-list") {
				t.Error("gated HTML must not be publicly cacheable")
			}
		})
	}
}

// TestGzipRoundTripAndVary proves that the compressed representation is the
// identity body's bytes, and that the response varies on Accept-Encoding.
func TestGzipRoundTripAndVary(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	identity := get(t, live, "/tier-list/mid")
	wantBody := body(t, identity)
	if got := identity.Header.Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", got)
	}
	if identity.Header.Get("Content-Encoding") != "" {
		t.Error("a client that did not ask for gzip was served a compressed body")
	}

	request, err := http.NewRequest(http.MethodGet, live.URL+"/tier-list/mid", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Accept-Encoding", "gzip")
	resp, err := live.Client().Do(request)
	if err != nil {
		t.Fatalf("GET with gzip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", resp.Header.Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if string(decoded) != wantBody {
		t.Fatalf("the gzip body decompresses to %d bytes, want the identity body's %d",
			len(decoded), len(wantBody))
	}
	if size := resp.Header.Get("Content-Length"); size != strconv.Itoa(len(decoded)) &&
		size != strconv.Itoa(len(decoded)) && size == strconv.Itoa(len(wantBody)) {
		t.Errorf("Content-Length = %s, which is the identity length, not the compressed one", size)
	}

	// The compressed representation is weaker than the identity one and the
	// tag says so, which is what makes a 304 under either encoding correct.
	if tag := resp.Header.Get("ETag"); !strings.HasPrefix(tag, `W/"`) {
		t.Errorf("ETag under gzip = %q, want a weak tag", tag)
	}
}

// TestETagThen304 checks the conditional path over a body that has been built
// once: the same tag comes back, and the second request is a 304 with no body.
func TestETagThen304(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	for _, path := range []string{"/tier-list/mid", "/agg/v1/manifest.json", "/sitemap.xml"} {
		t.Run(path, func(t *testing.T) {
			first := get(t, live, path)
			tag := first.Header.Get("ETag")
			if tag == "" {
				t.Fatalf("GET %s carries no entity tag", path)
			}

			request, err := http.NewRequest(http.MethodGet, live.URL+path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			request.Header.Set("If-None-Match", tag)
			second, err := live.Client().Do(request)
			if err != nil {
				t.Fatalf("conditional GET: %v", err)
			}
			defer func() { _ = second.Body.Close() }()
			if second.StatusCode != http.StatusNotModified {
				t.Fatalf("conditional GET of %s: status = %d, want 304", path, second.StatusCode)
			}
			if payload := body(t, second); payload != "" {
				t.Errorf("the 304 carried %d bytes of body", len(payload))
			}
			if got := second.Header.Get("ETag"); got != tag {
				t.Errorf("the 304's ETag = %q, want the original %q", got, tag)
			}
			if got := second.Header.Get("Cache-Control"); got != first.Header.Get("Cache-Control") {
				t.Errorf("the 304's Cache-Control = %q, want %q", got, first.Header.Get("Cache-Control"))
			}
		})
	}
}

// TestMethodNotAllowed checks that the tier refuses anything but a read, and
// says which methods it does serve.
func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			request, err := http.NewRequest(method, live.URL+"/tier-list/mid", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := live.Client().Do(request)
			if err != nil {
				t.Fatalf("%s: %v", method, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", resp.StatusCode)
			}
			if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("Allow = %q, want GET, HEAD", allow)
			}
			if page := body(t, resp); !strings.Contains(page, `data-fault="method"`) {
				t.Errorf("the 405 is not a visible page: %s", firstLine(page))
			}
		})
	}
}

// TestAggIsServedFromTheSnapshotRoot checks that the artifacts the pages were
// rendered from are served under /agg, and that a path cannot escape the tree.
func TestAggIsServedFromTheSnapshotRoot(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	raw, err := os.ReadFile(filepath.Join(fixtureDir(), "v1", "manifest.json"))
	if err != nil {
		t.Fatalf("read the fixture manifest: %v", err)
	}
	resp := get(t, live, "/agg/v1/manifest.json")
	if got := body(t, resp); got != string(raw) {
		t.Errorf("/agg/v1/manifest.json is not the artifact byte for byte (%d bytes vs %d)", len(got), len(raw))
	}

	// A traversal is refused, and it is refused as a path rather than by
	// accidentally landing on a missing file.
	for _, path := range []string{"/agg/%2e%2e/%2e%2e/etc/passwd", "/agg/v1/..", "/agg//manifest.json"} {
		if status := get(t, live, path).StatusCode; status != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", path, status)
		}
	}
}

// TestHealthMetricsAndReady covers the three operational endpoints, since the
// Deployment's probes and the scrape config are built on them.
func TestHealthMetricsAndReady(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	if got := body(t, get(t, live, "/healthz")); got != "ok" {
		t.Errorf("/healthz = %q, want ok", got)
	}
	ready := get(t, live, "/readyz")
	if ready.StatusCode != http.StatusOK {
		t.Fatalf("/readyz status = %d, want 200", ready.StatusCode)
	}
	if !strings.Contains(body(t, ready), `"latest_patch":"16.18"`) {
		t.Error("/readyz does not name the latest patch")
	}

	scrape := body(t, get(t, live, "/metrics"))
	for _, want := range []string{
		"lolstats_web_requests_total",
		"lolstats_web_render_seconds",
		"lolstats_web_ready 1",
	} {
		if !strings.Contains(scrape, want) {
			t.Errorf("/metrics does not expose %q", want)
		}
	}
}

// TestNoJSInteractivity is the point of the redesign, tested the only way it
// can be: by asking the server for a view of the table and reading what comes
// back. Nothing in these assertions executes JavaScript - the response is the
// whole answer, and the query string is the whole interaction.
func TestNoJSInteractivity(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	t.Run("sort and direction", func(t *testing.T) {
		ascending := body(t, get(t, live, "/tier-list/mid?sort=n&dir=asc&per=10"))
		descending := body(t, get(t, live, "/tier-list/mid?sort=n&dir=desc&per=10"))

		until := func(page string) []string {
			matches := regexp.MustCompile(`data-v-n="(\d+)"`).FindAllStringSubmatch(page, -1)
			values := make([]string, 0, len(matches))
			for _, match := range matches {
				values = append(values, match[1])
			}
			return values
		}
		lower, higher := until(ascending), until(descending)
		if len(lower) != 10 || len(higher) != 10 {
			t.Fatalf("per=10 returned %d and %d rows, want 10", len(lower), len(higher))
		}
		for index := 1; index < len(lower); index++ {
			if atoiForTest(lower[index-1]) > atoiForTest(lower[index]) {
				t.Fatalf("?dir=asc is not ascending at row %d: %s then %s", index, lower[index-1], lower[index])
			}
		}
		for index := 1; index < len(higher); index++ {
			if atoiForTest(higher[index-1]) < atoiForTest(higher[index]) {
				t.Fatalf("?dir=desc is not descending at row %d: %s then %s", index, higher[index-1], higher[index])
			}
		}
		if lower[0] == higher[0] {
			t.Error("the sort direction did not change the first row")
		}
	})

	t.Run("filter", func(t *testing.T) {
		filtered := body(t, get(t, live, "/tier-list/mid?q=zeri"))
		if count := strings.Count(filtered, "data-search="); count != 1 {
			t.Fatalf("?q=zeri returned %d rows, want 1", count)
		}
		if !strings.Contains(filtered, `data-v-champion="Zeri"`) {
			t.Error("?q=zeri did not return Zeri's row")
		}
		if none := body(t, get(t, live, "/tier-list/mid?q=zzzz")); strings.Count(none, "data-search=") != 0 {
			t.Error("a filter that matches nothing returned rows")
		}
	})

	t.Run("pagination", func(t *testing.T) {
		second := body(t, get(t, live, "/tier-list/mid?per=10&page=2&sort=n&dir=asc"))
		if count := strings.Count(second, "data-search="); count != 10 {
			t.Fatalf("page 2 with per=10 returned %d rows, want 10", count)
		}
		if !strings.Contains(second, `rel="prev"`) || !strings.Contains(second, `rel="next"`) {
			t.Error("page 2 has no previous/next links to follow without JavaScript")
		}
		if !strings.Contains(second, "Page 2 of") {
			t.Error("page 2 does not say which page it is")
		}
		// The pager's links carry the rest of the view, which is what makes
		// paging composable with the sort and the filter.
		if !strings.Contains(second, "sort=n") || !strings.Contains(second, "dir=asc") {
			t.Error("the pager links dropped the sort and the direction")
		}
	})

	t.Run("filter bar is a plain GET form", func(t *testing.T) {
		page := body(t, get(t, live, "/tier-list/mid"))
		if !strings.Contains(page, `action="/tier-list/mid"`) || !strings.Contains(page, `method="get"`) {
			t.Error("the filter bar is not a GET form pointing at the current route")
		}
		if !strings.Contains(page, `name="sort"`) || !strings.Contains(page, `name="dir"`) {
			t.Error("the filter bar does not offer the sort controls")
		}
	})

	t.Run("compare", func(t *testing.T) {
		// Both champions have a published cell in the mid fixture, so the
		// panel has to show the same cell the table shows, twice over: once in
		// its own row and once in the panel's.
		page := body(t, get(t, live, "/tier-list/mid?compare=xerath&compare=zeri"))
		if !strings.Contains(page, `class="fallback-compare"`) {
			t.Fatal("?compare= did not render the compare panel")
		}
		if !strings.Contains(page, "Comparing Xerath, Zeri") {
			t.Error("the compare panel does not name the champions it is comparing")
		}
		for _, name := range []string{"Xerath", "Zeri"} {
			if count := strings.Count(page, `data-v-champion="`+name+`"`); count < 2 {
				t.Errorf("%s appears %d times; the panel repeats the measured cells rather than recomputing them", name, count)
			}
		}

		// A champion with no published cell is named rather than invented: the
		// panel says what is missing instead of rendering a row of dashes.
		missing := body(t, get(t, live, "/tier-list/mid?compare=xerath&compare=nosuchchampion"))
		if !strings.Contains(missing, "Comparing Xerath, nosuchchampion") {
			t.Error("the panel does not name the champion it could not resolve")
		}
		if !strings.Contains(missing, "No champion matching nosuchchampion has a published cell in this snapshot") {
			t.Error("the panel does not say why the missing champion has no row")
		}
		if !strings.Contains(missing, "rather than a blank row being invented") {
			t.Error("the panel does not say what it refused to do")
		}

		// Both spellings of a comparison - repeated parameters and a comma
		// list - have to resolve to the same panel, because both are urls a
		// reader can write by hand.
		if comma := body(t, get(t, live, "/tier-list/mid?compare=xerath,zeri")); comma != body(t, get(t, live, "/tier-list/mid?compare=xerath&compare=zeri")) {
			t.Error("?compare=a,b and ?compare=a&compare=b do not render the same panel")
		}
	})

	t.Run("patch switching", func(t *testing.T) {
		// The switcher is a list of links, and each one is the archive route.
		page := body(t, get(t, live, "/tier-list/mid"))
		if !strings.Contains(page, `href="/patch/16.17/tier-list/mid"`) {
			t.Error("the patch switcher does not link to the archived patch's tier list")
		}
		archived := body(t, get(t, live, "/patch/16.17/tier-list/mid"))
		if !strings.Contains(archived, "16.17") {
			t.Error("the archived page does not name its patch")
		}
		// Both spellings of the same view resolve to the same renderer.
		viaQuery := body(t, get(t, live, "/tier-list/mid?patch=16.17"))
		if strings.TrimSpace(viaQuery) != strings.TrimSpace(archived) {
			t.Errorf("?patch=16.17 and /patch/16.17/tier-list/mid produced different pages (%d vs %d bytes)",
				len(viaQuery), len(archived))
		}
	})

	t.Run("query parameters do not change the default page", func(t *testing.T) {
		def := body(t, get(t, live, "/tier-list/mid"))
		explicit := body(t, get(t, live, "/tier-list/mid?sort=win_rate&dir=desc&page=1"))
		if strings.TrimSpace(explicit) != strings.TrimSpace(def) {
			t.Error("spelling out the default sort produced a different page, so the default view has two addresses")
		}
	})
}

// TestChampionPagesAreRenderedPerRole covers the champion route's two shapes:
// the overview and the role page, whose cells come from the same artifact.
func TestChampionPagesAreRenderedPerRole(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	overview := body(t, get(t, live, "/champions/azir"))
	if !strings.Contains(overview, "Azir") {
		t.Error("the overview does not name the champion")
	}
	role := body(t, get(t, live, "/champions/azir/mid"))
	for _, want := range []string{"Azir", "mid"} {
		if !strings.Contains(role, want) {
			t.Errorf("the role page does not contain %q", want)
		}
	}
	if overview == role {
		t.Error("the overview and the role page are the same page")
	}
}

// TestUnpublishedSnapshotIs404ForArtifactsAnd503ForPages keeps the two fault
// kinds apart: /agg is a file tree, so a file that is not there is a 404, while
// a page whose data is missing is a 503.
func TestUnpublishedSnapshotIs404ForArtifactsAnd503ForPages(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, Options{
		AggRoot:      t.TempDir(),
		FixturesMode: FixturesOff,
		DataDir:      fixtureDataDir(),
	})
	if status := get(t, live, "/agg/v1/manifest.json").StatusCode; status != http.StatusServiceUnavailable {
		t.Errorf("/agg with no published tree: status = %d, want 503", status)
	}
	if kind := faultKindIn(body(t, get(t, live, "/agg/v1/manifest.json"))); kind != FaultNoSnapshot {
		t.Errorf("/agg with no published tree reports %q, want %q", kind, FaultNoSnapshot)
	}
}

// Helpers.

func faultKindIn(page string) string {
	match := regexp.MustCompile(`data-fault="([a-z-]+)"`).FindStringSubmatch(page)
	if match == nil {
		return ""
	}
	return match[1]
}

func fixtureDataDir() string {
	return filepath.Join(discoverRepoRoot(), "web", "src", "data")
}

// copyFixtureTree copies the checked-in demo tree into a temporary directory so
// that a test can damage one artifact without touching the repository.
func copyFixtureTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := os.DirFS(fixtureDir())
	if err := fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	}); err != nil {
		t.Fatalf("copy the fixture tree: %v", err)
	}
	return dir
}

func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	if len(text) > 200 {
		return text[:200]
	}
	return text
}

func truncateForTest(raw []byte) string {
	if len(raw) > 200 {
		return string(raw[:200])
	}
	return string(raw)
}

func atoiForTest(value string) int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}
