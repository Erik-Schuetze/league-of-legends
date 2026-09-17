package webtier

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// The fault pages.
//
// A fault is answered with a page, not with a status code and an empty body.
// The reason is the one the whole design is built around: a reader cannot tell
// a table that has been truncated from a table that has few rows, so a tier
// that cannot render the real numbers has to say what went wrong in the place
// the numbers would have been. The page carries the fault kind in
// `data-fault`, the status in `data-status` and the server's own account of the
// failure, which is what makes it checkable from a shell as well as readable in
// a browser.
//
// The shell is used whenever it can be: a missing artifact under a readable
// manifest still has a state, a banner and a nav, and the reader reaches the
// rest of the site. When the manifest itself cannot be read there is no shell
// to render - the loader cannot describe a site it cannot read - and the
// standalone document is used instead. Neither form carries an entity tag.

// The fault kinds. They are the `data-fault` values, the metric labels, and
// what the tests assert on, so they are constants rather than literals.
const (
	FaultNotFound   = "not-found"
	FaultArtifact   = "artifact"
	FaultSchema     = "schema"
	FaultNoSnapshot = "no-snapshot"
	FaultRender     = "render"
	FaultMethod     = "method"
)

// errorView is the data both fault forms render. The two templates share it so
// that the standalone document cannot say something the shelled page does not.
type errorView struct {
	Title       string
	Path        string
	Status      int
	Kind        string
	Heading     string
	Lead        string
	Explanation template.HTML
	Detail      string
	Remedy      string
	Routes      []linkView
	HasRoutes   bool
	RoutesNote  string

	// The standalone form has no shell to carry the stylesheets, so it carries
	// the same three sources itself, in the shell's order: the base sheet by
	// link, then the scoped chunk, then the frozen layer. The frozen layer is
	// last because that is the only reason any of its rules beat the scoped
	// layer's, and it is present because the faults this form answers are the
	// ones where a reader has least else to look at. The shelled form ignores
	// these three; it gets them from shell.tmpl.
	Stylesheet string
	ScopedCSS  template.CSS
	FrozenCSS  template.CSS
	Source     string
	State      string
}

// ErrorPage builds the visible page a fault is answered with, wrapped in the
// site's shell. It cannot fail: a fault page that needs a working snapshot to
// render is not a fault page.
func (r *Renderer) ErrorPage(path string, status int, kind string, detail string) *Page {
	view := r.faultView(path, status, kind, detail)
	body, err := r.executeTemplate("page:error", view)
	if err != nil {
		// This is the template failing, which is a bug in this tier. The
		// standalone document is a different template and is tried by the
		// caller; here the body is left as the plainest possible sentence so
		// the shell around it is still a page.
		body = "<article><h1>" + EscapeString(view.Heading) + "</h1><p>" + EscapeString(view.Lead) + "</p></article>"
	}
	return &Page{
		Title:         view.Title,
		Description:   view.Description(),
		CanonicalPath: view.Path,
		Noindex:       true,
		Body:          body,
	}
}

// RenderStandaloneError builds the whole document for a fault that struck while
// the snapshot itself was unreadable. It says data-state="no-data" because that
// is the only provenance the tier can honestly claim at that moment: it has not
// read a snapshot, so it will not dress the page up as one.
func (r *Renderer) RenderStandaloneError(path string, status int, kind string, detail string) ([]byte, error) {
	view := r.faultView(path, status, kind, detail)
	body, err := r.executeTemplate("page:error-standalone", view)
	if err != nil {
		return nil, err
	}
	return []byte(body), nil
}

// Description is the meta description of a fault page. It is written rather
// than derived because it is the one line a search engine keeps.
func (v errorView) Description() string {
	switch v.Kind {
	case FaultNotFound:
		return "There is no page at this address. The site serves a fixed set of routes, listed on this page."
	case FaultArtifact:
		return "The aggregate snapshot this site publishes is incomplete, so this page has no numbers to show rather than incomplete ones."
	case FaultSchema:
		return "The published snapshot declares a schema version this tier does not implement, so it refuses to read it rather than guess at its meaning."
	case FaultNoSnapshot:
		return "No aggregate snapshot has been published yet, so the routes that describe the ladder are not available."
	case FaultMethod:
		return "This route is read-only: it answers GET and HEAD."
	}
	return "This page could not be rendered by the web tier that serves it."
}

// faultView assembles the copy. The wording of a fault page is part of the
// honesty machinery, not decoration: it has to say what happened, that nothing
// was substituted for the missing data, and what would make the page work.
func (r *Renderer) faultView(path string, status int, kind string, detail string) errorView {
	name := strings.TrimSpace(path)
	if name == "" || !strings.HasPrefix(name, "/") {
		name = "/"
	}
	view := errorView{
		Path:        name,
		Status:      status,
		Kind:        kind,
		Detail:      strings.TrimSpace(detail),
		Title:       "Data unavailable - " + SiteName,
		Explanation: "",
		Stylesheet:  baseCSSPath,
		ScopedCSS:   trustedCSS(string(scopedCSS(false))),
		FrozenCSS:   trustedCSS(frozenCSSChunk()),
		Source:      "fault",
		State:       "no-data",
	}
	switch kind {
	case FaultNotFound:
		view.Title = "Page not found - " + SiteName
		view.Heading = "Page not found"
		view.Lead = "There is no page at this address."
		view.Explanation = trustedHTML("This site serves a fixed set of routes and the one you asked for is not among " +
			"them. Nothing is missing from the published snapshot and no number was withheld: the page does not exist, " +
			"which is a different thing from a page with no data.")
		view.Remedy = "Every route this site serves is listed below. The newest patch's pages are the ones worth reading " +
			"first if you were looking for statistics."
		view.Routes, view.RoutesNote = r.errorRoutes()
		view.HasRoutes = len(view.Routes) > 0 || view.RoutesNote != ""
	case FaultArtifact:
		view.Title = "Published snapshot incomplete - " + SiteName
		view.Heading = "The published snapshot is incomplete"
		view.Lead = "The artifact this page is rendered from is missing or unreadable, so there is no table to show."
		view.Explanation = trustedHTML("The manifest at the root of the published tree advertises this artifact, and an " +
			"advertised artifact is a promise that it is there. This site does not keep quiet about a broken promise: a " +
			"table with rows quietly missing cannot be told apart from a table with few rows, so the page is withheld " +
			"whole. Nothing has been substituted, no older copy has been served in its place, and no rate has been " +
			"estimated to fill the gap.")
		view.Remedy = "The snapshot is republished from the pipeline that produces it, and this page answers normally " +
			"once it has. The report below names the artifact that could not be read."
	case FaultSchema:
		view.Title = "Snapshot schema not supported - " + SiteName
		view.Heading = "The published snapshot is not a version this tier reads"
		view.Lead = "The snapshot declares a schema version this reader does not implement."
		view.Explanation = trustedHTML("Artifacts are read against a schema, and a version that is not the one this " +
			"tier implements is refused whole rather than decoded on a guess: a field that moved or changed meaning " +
			"would otherwise be published as a number with a different definition than the one the page claims for it.")
		view.Remedy = "Either the tier is redeployed at a version that implements what was published, or the snapshot is " +
			"republished at the version this tier implements. Until one of those has happened, this route stays as it is."
	case FaultNoSnapshot:
		view.Title = "No snapshot published yet - " + SiteName
		view.Heading = "No snapshot has been published yet"
		view.Lead = "This site is deployed, but nothing has been published for the tier to read."
		view.Explanation = trustedHTML("There is no manifest at the root of the aggregate tree, so there is no patch, no " +
			"sample window and no floor for a page to describe. The pages that describe the site itself are complete; " +
			"the pages that describe the ranked ladder are not published.")
		view.Remedy = "The pipeline publishes a tree per patch. These routes answer as soon as it has."
	case FaultMethod:
		view.Title = "Method not allowed - " + SiteName
		view.Heading = "This route is read-only"
		view.Lead = "This site has no forms to post to: every view of the data is a URL."
		view.Explanation = trustedHTML("Sorting, filtering, paging, patch switching and comparing are query parameters " +
			"on a plain GET, which is what makes every view of the data shareable, bookmarkable and usable with " +
			"JavaScript disabled. There is nothing here to accept a request body.")
		view.Remedy = "Repeat the request with GET (or HEAD) to read the page."
	default:
		view.Title = "Page could not be rendered - " + SiteName
		view.Heading = "This page could not be rendered"
		view.Lead = "The server failed while building this page."
		view.Explanation = trustedHTML("This is a fault in the web tier rather than in the published data: the snapshot " +
			"was readable enough to start from, and the page still could not be assembled. It is reported in the " +
			"server's log. No partial page is served, and the status you were given is the status of the failure.")
		view.Remedy = "Retrying may succeed. If it does not, the report below is what the process recorded."
	}
	return view
}

// errorRoutes is the route list shown on a 404, derived from the same generator
// the sitemap is built from, so the two cannot disagree about what exists. The
// per-champion and per-patch routes are counted rather than listed: a 404 page
// that carried a link per route would weigh more than the page that was asked
// for, and the reader who wants the whole list wants /sitemap.xml anyway.
func (r *Renderer) errorRoutes() ([]linkView, string) {
	routes, err := r.RouteList()
	if err != nil {
		// A route list that cannot be derived is not worth a second fault: the
		// 404 page keeps its explanation and loses its links.
		return nil, ""
	}
	links := make([]linkView, 0, len(routes))
	champions, patches := 0, 0
	for _, route := range routes {
		switch {
		case strings.HasPrefix(route.Path, "/champions/"):
			champions++
		case strings.HasPrefix(route.Path, "/patch/"):
			patches++
		default:
			links = append(links, linkView{Href: route.Path, Name: routeLabel(route)})
		}
	}
	return links, errorRouteNote(champions, patches)
}

// errorRouteNote says what the 404's list leaves out. The count and the list
// come from the one pass over the routes above, so they cannot disagree.
func errorRouteNote(champions int, patches int) string {
	switch {
	case champions > 0 && patches > 0:
		return "Also not listed above: " + IntegerAny(float64(champions)) + " champion pages and " +
			IntegerAny(float64(patches)) + " archived patch pages. /sitemap.xml lists every route this site serves."
	case champions > 0:
		return "Also not listed above: " + IntegerAny(float64(champions)) +
			" champion pages, which /sitemap.xml lists in full."
	case patches > 0:
		return "Also not listed above: " + IntegerAny(float64(patches)) +
			" archived patch pages, which /sitemap.xml lists in full."
	}
	return ""
}

// routeLabel is the human name of a route in the 404's list: the page's own
// path, except that the two feed files are named for what they are and the
// navigation routes are named for what they show.
func routeLabel(route Route) string {
	switch route.Path {
	case "/":
		return "Home (what this site is, and what the numbers mean)"
	case "/about":
		return "About the data (how the statistics are produced)"
	case "/sitemap.xml":
		return "sitemap.xml (every route, for crawlers)"
	case "/robots.txt":
		return "robots.txt (crawler policy)"
	}
	return route.Path
}

// executeTemplate renders one named template of the set without going through
// the shell, which is what the fault pages need: RenderBody asks the loader for
// a site first, and a fault may be exactly that the site cannot be loaded.
func (r *Renderer) executeTemplate(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := r.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// faultStatusText is the status line both fault forms print next to the kind,
// so a reader sees the number the HTTP client saw.
func (v errorView) StatusText() string { return strconv.Itoa(v.Status) }

// MethodNotAllowedPage is the fault page for a request this tier does not
// serve, with the Allow header's value.
func (r *Renderer) MethodNotAllowedPage(path string) *Page {
	return r.ErrorPage(path, http.StatusMethodNotAllowed, FaultMethod, "only GET and HEAD are served")
}
