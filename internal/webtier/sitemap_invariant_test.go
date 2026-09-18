package webtier

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The sitemap advertises a route when, and only when, the page behind it asks to
// be indexed. That is one claim in two directions, and this tier shipped a
// violation of each of them: every champion and champion/role route was
// advertised whether or not its page carried noindex,follow (617 of the 1058
// entries on the published snapshot), and /explore, which renders index,follow,
// was in none of the 1058. The two defects were visible in the same census, and
// the doc comment claiming the lists "agree by construction" was true of
// neither.
//
// So the sweep below reads both answers for every route the tier can serve, as
// they are served, in every data posture: whether /sitemap.xml names the route,
// and which robots directive the page at that route carries. Asserting one
// direction only would have called the old tree green.
//
// The controls are what keep the equivalence from being satisfied by a sitemap
// that merely lists nothing: a snapshot posture must advertise /explore and the
// canonical tier list, must omit at least one route the snapshot cannot back,
// and must not be able to omit all of them; a posture with no snapshot must
// advertise the prose routes and nothing else, because every statistics page in
// it is an explicit empty state that asks not to be indexed. Neither direction
// of the equivalence is asserted without a non-empty case in front of it.

// servedRoutes is every path this tier can serve. It is derived from the site's
// own champion, role and patch lists rather than from the sitemap, so the two
// sets being compared are built independently: a route only one of them knows
// about shows up as a disagreement instead of being invisible to both.
func servedRoutes(t *testing.T, site *Site) []copyRoute {
	t.Helper()

	routes := append([]copyRoute{}, proseRoutes...)
	routes = append(routes, copyRoute{"explore", "/explore"})
	for _, role := range Roles {
		slug := RoleSlugString(role)
		routes = append(routes,
			copyRoute{"tier-list-" + slug, "/tier-list/" + slug},
			copyRoute{"matchups-" + slug, "/matchups/" + slug})
	}
	for _, patch := range site.Patches() {
		for _, role := range Roles {
			slug := RoleSlugString(role)
			routes = append(routes, copyRoute{
				name: "patch-" + patch + "-tier-list-" + slug,
				path: "/patch/" + patch + "/tier-list/" + slug,
			})
		}
	}
	for _, champion := range site.Champions() {
		routes = append(routes, copyRoute{name: "champion-" + champion.Slug, path: "/champions/" + champion.Slug})
		for _, role := range Roles {
			slug := RoleSlugString(role)
			routes = append(routes, copyRoute{
				name: "champion-" + champion.Slug + "-" + slug,
				path: "/champions/" + champion.Slug + "/" + slug,
			})
		}
	}
	return routes
}

// sitemapPaths reads the <loc> elements out of /sitemap.xml and returns them as
// site-relative paths in canonical form, which is the form the pages address
// themselves by and the form the route names in servedRoutes are normalised to
// before they are compared.
func sitemapPaths(t *testing.T, body string) []string {
	t.Helper()

	parts := strings.Split(body, "<loc>")
	paths := make([]string, 0, len(parts))
	for _, part := range parts[1:] {
		end := strings.Index(part, "</loc>")
		if end < 0 {
			t.Fatalf("sitemap has a <loc> with no closing tag: %s", firstLine(body))
		}
		path := strings.TrimSpace(part[:end])
		path = strings.TrimPrefix(path, DefaultSiteURL)
		if path == "" {
			path = "/"
		}
		paths = append(paths, CanonicalPath(path))
	}
	return paths
}

// TestSitemapAdvertisesExactlyTheRoutesThatAskToBeIndexed is the invariant, run
// over every route in every posture the tier can be in.
func TestSitemapAdvertisesExactlyTheRoutesThatAskToBeIndexed(t *testing.T) {
	t.Parallel()

	liveRoot, _, _, _, _, _, _ := republishedSnapshot(t)
	postures := []struct {
		name string
		opts Options
	}{
		{name: "demo", opts: fixtureOptions()},
		{name: "live", opts: Options{AggRoot: liveRoot, FixturesMode: FixturesOff, DataDir: fixtureDataDir()}},
		{name: "no-data", opts: Options{AggRoot: t.TempDir(), FixturesMode: FixturesOff, DataDir: fixtureDataDir()}},
	}

	for _, posture := range postures {
		t.Run(posture.name, func(t *testing.T) {
			t.Parallel()

			server, live := newTestServer(t, posture.opts)
			site, err := server.renderer.Site()
			if err != nil {
				t.Fatalf("the %s posture cannot be loaded: %v", posture.name, err)
			}

			feed := get(t, live, "/sitemap.xml")
			if feed.status != http.StatusOK {
				t.Fatalf("GET /sitemap.xml in the %s posture -> %d, want 200", posture.name, feed.status)
			}
			advertised := map[string]bool{}
			for _, path := range sitemapPaths(t, feed.text()) {
				advertised[path] = true
			}

			routes := servedRoutes(t, site)
			served := map[string]bool{}
			for _, route := range routes {
				served[CanonicalPath(route.path)] = true
			}

			listed, omitted, faulted := 0, 0, 0
			for _, route := range routes {
				inSitemap := advertised[CanonicalPath(route.path)]
				if inSitemap {
					listed++
				} else {
					omitted++
				}

				res := get(t, live, route.path)
				page := res.text()
				indexable := strings.Contains(page, `<meta name="robots" content="index,follow">`)
				noindexed := strings.Contains(page, `<meta name="robots" content="noindex,follow">`)
				switch {
				case !indexable && !noindexed:
					t.Errorf("%s (%s) -> %d in the %s posture and declares no robots directive at all",
						route.name, route.path, res.status, posture.name)
				case res.status == http.StatusOK:
					switch {
					case inSitemap && !indexable:
						t.Errorf("/sitemap.xml advertises %s (%s) in the %s posture, but the page asks not to be indexed",
							route.name, route.path, posture.name)
					case !inSitemap && indexable:
						t.Errorf("%s (%s) renders index,follow in the %s posture but /sitemap.xml does not advertise it",
							route.name, route.path, posture.name)
					}
				case res.status == http.StatusServiceUnavailable:
					// A route that needs a snapshot and has none is a fault rather
					// than a page: the tier says so in 503 and the fault page asks
					// not to be indexed. It cannot be advertised either, because an
					// entry in the sitemap is a promise that a crawler can fetch a
					// page there.
					faulted++
					if inSitemap {
						t.Errorf("/sitemap.xml advertises %s (%s), but the %s posture cannot serve it: %d",
							route.name, route.path, posture.name, res.status)
					}
					if indexable {
						t.Errorf("the fault page for %s (%s) in the %s posture renders index,follow",
							route.name, route.path, posture.name)
					}
				default:
					t.Errorf("%s (%s) -> %d in the %s posture, want 200 or 503",
						route.name, route.path, res.status, posture.name)
				}
			}

			// The same equivalence from the sitemap's side: nothing may be
			// advertised that the tier cannot serve, because an entry a crawler
			// can fetch is a promise about a page.
			for _, path := range sitemapPaths(t, feed.text()) {
				if !served[path] {
					t.Errorf("/sitemap.xml advertises %s in the %s posture, which is not a route this tier serves",
						path, posture.name)
				}
			}

			if listed == 0 || omitted == 0 {
				t.Errorf("the %s posture advertises %d route(s) and omits %d, so an equivalence asserted over them means nothing",
					posture.name, listed, omitted)
			}
			if site.Latest() == nil {
				// Every statistics route is an empty state that asks not to be
				// indexed when there is no snapshot - and the data routes are not
				// even served, which the fault count asserts - so the sitemap is
				// the prose routes and nothing else.
				if faulted == 0 {
					t.Errorf("the %s posture has no snapshot, yet none of the %d routes it swept is a fault: the sweep is not reaching the data routes",
						posture.name, len(routes))
				}
				if len(advertised) != len(proseRoutes) {
					t.Errorf("the %s posture has no snapshot, yet /sitemap.xml advertises %d route(s), want the %d prose routes",
						posture.name, len(advertised), len(proseRoutes))
				}
				if advertised[CanonicalPath("/explore")] {
					t.Errorf("the %s posture has no snapshot, so /explore asks not to be indexed, yet /sitemap.xml advertises it",
						posture.name)
				}
				return
			}
			// With a snapshot every route in the sweep is served, so the routes
			// the sitemap omits have to be the pages that asked to be omitted
			// rather than pages that failed.
			if faulted != 0 {
				t.Errorf("the %s posture has a snapshot and still faulted on %d route(s) of %d, so its omissions cannot be read as decisions",
					posture.name, faulted, len(routes))
			}
			// The positive control: pages that exist and can be indexed must be
			// advertised. /explore is the route this file's own regression lost,
			// so it is named here rather than left to the sweep.
			for _, path := range []string{"/explore", "/tier-list/top", "/matchups/top"} {
				if !advertised[CanonicalPath(path)] {
					t.Errorf("%s is an indexable page of statistics in the %s posture, but /sitemap.xml does not advertise it",
						path, posture.name)
				}
			}
		})
	}
}

// The sitemap's <loc> entries are absolute URLs under DefaultSiteURL, built from
// the path CanonicalPath produced for the route.
func sitemapLocs(t *testing.T, body string) []string {
	t.Helper()

	parts := strings.Split(body, "<loc>")
	locs := make([]string, 0, len(parts))
	for _, part := range parts[1:] {
		end := strings.Index(part, "</loc>")
		if end < 0 {
			t.Fatalf("sitemap has a <loc> with no closing tag: %s", firstLine(body))
		}
		locs = append(locs, strings.TrimSpace(part[:end]))
	}
	return locs
}

// canonicalOf reads the rel=canonical the page declares as its own address. An
// empty result means the page declares none, which the check below treats as a
// disagreement rather than as agreement.
func canonicalOf(body string) string {
	const marker = `<link rel="canonical" href="`
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// locFetch is one fetch of one advertised URL: the status it answered with, how
// many redirects it took to get there, and the canonical the page claims.
type locFetch func(loc string) (status int, redirects int, canonical string, err error)

// locFaults is the whole check. It is a function returning the reasons a
// sitemap's promise fails to hold rather than a chain of assertions so that the
// controls at the bottom of this file can run the identical logic over inputs
// that must be rejected. Without that, "the check passes" would only mean "the
// check is not looking": this file's own subject shipped for months with every
// entry pointing at a URL that was not the canonical of the page behind it, and
// a check that cannot say so is not evidence.
//
// The three things asserted of every entry are the three clauses of the
// promise: the URL is at the site's canonical shape, a crawler reaches the page
// in one request without being redirected first, and the page agrees that is
// where it lives.
func locFaults(locs []string, fetch locFetch) []string {
	if len(locs) == 0 {
		return []string{"the sitemap lists no <loc> at all, so it promises a crawler nothing and satisfies that trivially"}
	}

	var faults []string
	for _, loc := range locs {
		if !strings.HasPrefix(loc, DefaultSiteURL+"/") {
			faults = append(faults, loc+" is not an absolute URL under "+DefaultSiteURL)
			continue
		}
		if loc != DefaultSiteURL+"/" && !strings.HasSuffix(loc, "/") {
			faults = append(faults, loc+" is not in the site's canonical shape, which ends in /")
		}

		status, redirects, canonical, err := fetch(loc)
		if err != nil {
			faults = append(faults, loc+": "+err.Error())
			continue
		}
		if redirects != 0 {
			faults = append(faults, fmt.Sprintf("%s took %d redirect(s) to arrive, so the sitemap advertises a URL that is not served there", loc, redirects))
		}
		if status != http.StatusOK {
			faults = append(faults, fmt.Sprintf("%s -> %d, so the sitemap promises a crawler a page it cannot fetch", loc, status))
			continue
		}
		if canonical != loc {
			faults = append(faults, fmt.Sprintf("%s answers with rel=canonical %q, so the sitemap advertises a URL that is not the canonical of the page it points at", loc, canonical))
		}
	}
	return faults
}

// locFetcherFor fetches advertised URLs from a test server. It counts redirects
// instead of refusing them, so a redirecting entry is reported as the defect it
// is rather than being followed silently into a 200 and read as agreement.
func locFetcherFor(t *testing.T, live *httptest.Server) locFetch {
	t.Helper()

	return func(loc string) (int, int, string, error) {
		path := strings.TrimPrefix(loc, DefaultSiteURL)
		if path == "" {
			path = "/"
		}

		redirects := 0
		client := *live.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			redirects++
			return nil
		}

		res, err := client.Get(live.URL + path)
		if err != nil {
			return 0, redirects, "", err
		}
		defer res.Body.Close()

		body, err := io.ReadAll(res.Body)
		if err != nil {
			return 0, redirects, "", err
		}
		return res.StatusCode, redirects, canonicalOf(string(body)), nil
	}
}

// TestSitemapEntriesAreTheCanonicalURLOfThePageTheyPointAt is the invariant.
//
// The defect it was written for: every page declared rel=canonical with the
// trailing slash, while every one of the sitemap's 448 <loc> entries named the
// bare path - and, on the tier that served a redirect there, arriving at the
// advertised URL took a 308 before the crawler saw the page. The page said one
// address, the sitemap said another, and nothing compared them.
//
// It is run in all three postures because the shape of a URL is the one claim
// in the sitemap that must not vary with the snapshot: a route that is canonical
// in the preview posture is canonical in the live one.
func TestSitemapEntriesAreTheCanonicalURLOfThePageTheyPointAt(t *testing.T) {
	t.Parallel()

	liveRoot, _, _, _, _, _, _ := republishedSnapshot(t)
	postures := []struct {
		name string
		opts Options
	}{
		{name: "demo", opts: fixtureOptions()},
		{name: "live", opts: Options{AggRoot: liveRoot, FixturesMode: FixturesOff, DataDir: fixtureDataDir()}},
		{name: "no-data", opts: Options{AggRoot: t.TempDir(), FixturesMode: FixturesOff, DataDir: fixtureDataDir()}},
	}

	for _, posture := range postures {
		t.Run(posture.name, func(t *testing.T) {
			t.Parallel()

			_, live := newTestServer(t, posture.opts)

			feed := get(t, live, "/sitemap.xml")
			if feed.status != http.StatusOK {
				t.Fatalf("GET /sitemap.xml in the %s posture -> %d, want 200", posture.name, feed.status)
			}

			locs := sitemapLocs(t, feed.text())
			if len(locs) == 0 {
				t.Fatalf("the sitemap in the %s posture lists no <loc>, so this check has nothing to compare", posture.name)
			}

			for _, fault := range locFaults(locs, locFetcherFor(t, live)) {
				t.Errorf("%s posture: %s", posture.name, fault)
			}

			// The trailing-slash form is the one asserted above, so say which
			// shape was chosen rather than leaving it implied by the sweeps.
			for _, path := range sitemapPaths(t, feed.text()) {
				if path != "/" && !strings.HasSuffix(path, "/") {
					t.Errorf("the sitemap in the %s posture lists %s, which is not the canonical shape", posture.name, path)
				}
			}
		})
	}
}

// TestSitemapCanonicalCheckCanFail is the check's own control, and it is the
// reason the test above is evidence. Each case is an input the check must
// reject; if any of them came back clean, TestSitemapEntriesAreTheCanonicalURLOf
// thePageTheyPointAt would be reporting on something other than what it claims.
func TestSitemapCanonicalCheckCanFail(t *testing.T) {
	t.Parallel()

	_, live := newTestServer(t, fixtureOptions())
	fetch := locFetcherFor(t, live)

	cases := []struct {
		name   string
		locs   []string
		want   string
		reason string
		fetch  locFetch
	}{
		{
			name:   "a sitemap with no loc entries",
			locs:   nil,
			want:   "no <loc>",
			reason: "an empty sitemap must fail, or an empty sitemap would pass",
		},
		{
			name:   "a loc that does not exist",
			locs:   []string{DefaultSiteURL + "/no-such-route-abcdefgh/"},
			want:   "404",
			reason: "a 404ing entry must fail, or a sitemap could advertise anything",
		},
		{
			name:   "the bare-path shape this tier used to emit",
			locs:   []string{DefaultSiteURL + "/about"},
			want:   "canonical shape",
			reason: "the shape of the defect must fail, or this check cannot see the defect it was written for",
		},
		{
			name:   "a loc that redirects before it answers",
			locs:   []string{DefaultSiteURL + "/about"},
			want:   "redirect",
			reason: "counting redirects is only worth it if a redirect is a fault",
			fetch: func(string) (int, int, string, error) {
				// This tier redirects nothing: it serves /about and /about/ as
				// 200 so a pre-cutover URL keeps working, which is the property
				// that makes the cutover non-breaking. A redirecting entry
				// therefore has to be simulated to show the counter is wired at
				// all - it is the one fault below the tier cannot currently
				// produce for real.
				return http.StatusOK, 1, DefaultSiteURL + "/about", nil
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			f := fetch
			if testCase.fetch != nil {
				f = testCase.fetch
			}

			faults := locFaults(testCase.locs, f)
			if len(faults) == 0 {
				t.Fatalf("the check accepted %s: %s", testCase.name, testCase.reason)
			}
			joined := strings.Join(faults, "\n")
			if !strings.Contains(joined, testCase.want) {
				t.Fatalf("the check rejected %s for the wrong reason; want a fault mentioning %q, got:\n%s",
					testCase.name, testCase.want, joined)
			}
		})
	}

	// A loc whose page claims a different canonical cannot be produced from a
	// working tier, so it is the one case driven by a stub: the comparison
	// itself, with the fetch taken out of the way.
	faults := locFaults([]string{DefaultSiteURL + "/about/"}, func(string) (int, int, string, error) {
		return http.StatusOK, 0, DefaultSiteURL + "/tier-list/mid/", nil
	})
	if len(faults) == 0 {
		t.Fatal("the check accepted an entry whose page declares a different canonical, so its central comparison does not run")
	}
	if !strings.Contains(strings.Join(faults, "\n"), "not the canonical of the page it points at") {
		t.Fatalf("the check rejected a disagreeing canonical for the wrong reason:\n%s", strings.Join(faults, "\n"))
	}
}
