package webtier

import (
	"net/http"
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
// site-relative paths, which is the form the routes are served under.
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
		paths = append(paths, path)
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
				served[route.path] = true
			}

			listed, omitted, faulted := 0, 0, 0
			for _, route := range routes {
				inSitemap := advertised[route.path]
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
				if advertised["/explore"] {
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
				if !advertised[path] {
					t.Errorf("%s is an indexable page of statistics in the %s posture, but /sitemap.xml does not advertise it",
						path, posture.name)
				}
			}
		})
	}
}
