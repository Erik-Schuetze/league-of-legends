package webtier

import (
	"strconv"
	"strings"
)

// The two non-HTML documents the site publishes, plus the Riot verification
// token file.
//
// Both are generated from the same route list the statistics pages are rendered
// from rather than from a crawl of what was served, so a route that exists
// cannot be missing from the sitemap and a route that is not published cannot be
// advertised. robots.txt points at the sitemap, so the two agree by
// construction. A snapshot that does not exist produces the five prose routes
// only: the statistics pages are deliberate empty states that ask not to be
// indexed, and advertising an empty table in a sitemap would be dishonest about
// what the site has.

// Route is one sitemap entry.
type Route struct {
	// Path is the site-relative path, without the origin.
	Path string
	// Lastmod is the snapshot date the page was rendered from, or "".
	Lastmod string
	// Priority is written with one decimal, as the reference build writes it.
	Priority string
	// Changefreq is daily for a data route and monthly for a prose one.
	Changefreq string
}

// RouteList is every URL this tier publishes, in the order the reference sitemap
// listed them.
func (r *Renderer) RouteList() ([]Route, error) {
	site, err := r.Site()
	if err != nil {
		return nil, err
	}
	return routeList(site), nil
}

func routeList(site *Site) []Route {
	lastmod := ""
	if generated := site.ManifestGeneratedAt(); len(generated) >= 10 {
		lastmod = generated[:10]
	}
	dataRoute := func(path string, priority float64) Route {
		return Route{Path: path, Lastmod: lastmod, Priority: fixed1(priority), Changefreq: "daily"}
	}
	page := func(path string, priority float64) Route {
		return Route{Path: path, Priority: fixed1(priority), Changefreq: "monthly"}
	}

	routes := []Route{
		page("/", 1),
		page("/about", 0.6),
		page("/disclaimer", 0.3),
		page("/legal/terms", 0.3),
		page("/legal/privacy", 0.3),
	}
	if site.Latest() == nil {
		return routes
	}

	for _, role := range Roles {
		slug := RoleSlugString(role)
		routes = append(routes,
			dataRoute("/tier-list/"+slug, 0.9),
			dataRoute("/matchups/"+slug, 0.8),
		)
	}
	for _, patch := range site.Patches() {
		for _, role := range Roles {
			routes = append(routes, dataRoute("/patch/"+patch+"/tier-list/"+RoleSlugString(role), 0.7))
		}
	}
	for _, champion := range site.Champions() {
		routes = append(routes, dataRoute("/champions/"+champion.Slug, 0.6))
	}
	for _, champion := range site.Champions() {
		for _, role := range Roles {
			routes = append(routes, dataRoute("/champions/"+champion.Slug+"/"+RoleSlugString(role), 0.5))
		}
	}
	return routes
}

// Sitemap is /sitemap.xml.
func (r *Renderer) Sitemap() ([]byte, error) {
	routes, err := r.RouteList()
	if err != nil {
		return nil, err
	}
	entries := make([]string, 0, len(routes))
	for _, route := range routes {
		loc := escapeXML(r.absolute(route.Path))
		lastmod := ""
		if route.Lastmod != "" {
			lastmod = "<lastmod>" + route.Lastmod + "</lastmod>"
		}
		entries = append(entries,
			"  <url>"+
				"<loc>"+loc+"</loc>"+
				lastmod+
				"<changefreq>"+route.Changefreq+"</changefreq>"+
				"<priority>"+route.Priority+"</priority>"+
				"</url>")
	}
	body := strings.Join([]string{
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>",
		"<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">",
		strings.Join(entries, "\n"),
		"</urlset>",
		"",
	}, "\n")
	return []byte(body), nil
}

// Robots is /robots.txt. Every claim in it that a reader could check against
// the data is read from the same manifest the pages are rendered from: which
// posture is being served, what the manifest declares about its own source, the
// window and generation time, and - when the snapshot is the labelled sample -
// the banner sentence that says so. The Sitemap line is derived from the
// configured canonical origin, so it cannot disagree with the sitemap's own URLs.
//
// Two statements this file used to make were literals rather than readings, and
// both were wrong once the tier stopped being an Astro build: that every page
// here is "static", and that the snapshot state is whatever "live" meant when
// the line was typed. The second is the one that matters, because it is the
// same claim the PREVIEW banner makes in HTML, and a crawler-facing file that
// contradicts the pages it describes is the honesty defect this project exists
// to avoid. Neither survives as a constant: the state line, the source line and
// the preview sentence are all derived, in the site's own words.
func (r *Renderer) Robots() ([]byte, error) {
	site, err := r.Site()
	if err != nil {
		return nil, err
	}
	lines := []string{
		"# Every page here is public and meant to be indexed.",
		"# This tier is not static: each page is rendered per request from the snapshot",
		"# named below, so what the sitemap lists is what is published now rather than an",
		"# earlier build of it. A route this snapshot cannot back says so on the page and",
		"# carries meta robots noindex.",
		"User-agent: *",
		"Allow: /",
		"",
		"Sitemap: " + r.siteURL + "/sitemap.xml",
		"",
		"# Snapshot state: " + site.PostureWord() + ".",
	}
	if site.State() == StateNoData {
		lines = append(lines,
			"# No aggregate snapshot has been published yet, so these pages carry no match",
			"# statistics: every route renders its real layout with an explicit empty state,",
			"# and the sitemap advertises the prose routes only.")
	} else {
		lines = append(lines, "# "+site.ProvenanceLine())
		if source := site.Source(); source != "" {
			lines = append(lines, "# The manifest declares source \""+source+"\".")
		} else {
			lines = append(lines, "# The manifest does not declare where this snapshot came from.")
		}
		if preview := site.Banner().PreviewText; preview != "" {
			lines = append(lines, "# "+preview)
		}
	}
	lines = append(lines,
		"# Structurally identical pages are never emitted twice: the patch-specific",
		"# tier list of the newest patch declares the latest tier list as canonical.",
		"",
	)
	return []byte(strings.Join(lines, "\n")), nil
}

// RiotToken is /riot.txt. Riot's site-verification flow asks for a token at a
// fixed path and nothing else there, so the file is published only when a token
// is configured: a tier that always answered 200 with an empty body would be
// claiming a verification that has not happened. found=false is a 404.
func (r *Renderer) RiotToken() (body []byte, found bool) {
	if r.riotToken == "" {
		return nil, false
	}
	return []byte(r.riotToken + "\n"), true
}

// fixed1 renders a priority the way toFixed(1) does.
func fixed1(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

// escapeXML escapes the five characters an XML text node cannot carry raw.
func escapeXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
	)
	return replacer.Replace(value)
}
