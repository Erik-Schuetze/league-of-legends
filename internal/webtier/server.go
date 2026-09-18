// The HTTP layer: one process, one port, one policy.
//
// Every route below is rendered on the server from the published snapshot, so
// every view of the data - the sort order, the filter, the page, the patch, the
// champions being compared - is a URL that can be shared and opened with
// JavaScript disabled. The three islands still enhance the same markup, but
// nothing here waits for them: the HTML the server writes is the whole page.
//
// The policy is deliberately small and uniform. Every response goes through
// respond(), so the cache directives, the entity tag, the encoding and the
// status cannot be decided differently in two places:
//
//   - rendered HTML is `private, max-age=60, stale-while-revalidate=300`. It is
//     private on purpose: the page is built per request and is republished
//     nightly, so a shared proxy (the cluster Caddy in front of this Service)
//     must not store or replay it. `stale-while-revalidate` then belongs to the
//     browser, which is the only cache that may keep it.
//   - the statistics routes are not cached by anything in front of the browser,
//     and a response to a broken snapshot is `no-store` so that a fault can
//     never be replayed after the snapshot has been fixed.
//   - the artifacts under /agg are public data and get public TTLs: the
//     versioned Data Dragon projection is immutable per version, the rest is
//     republished nightly and gets a minute.
//
// A fault is a page, not a status code with an empty body. A missing or
// corrupt artifact is a 503 that says which artifact is missing, because a
// table that quietly lost its rows is the failure mode this design exists to
// prevent. Never a truncated 200.
package webtier

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// The response policy, in one place.
const (
	htmlContentType = "text/html; charset=utf-8"
	jsonContentType = "application/json; charset=utf-8"
	xmlContentType  = "application/xml; charset=utf-8"
	textContentType = "text/plain; charset=utf-8"
	jsContentType   = "text/javascript; charset=utf-8"
	cssContentType  = "text/css; charset=utf-8"

	htmlCacheControl      = "private, max-age=60, stale-while-revalidate=300"
	feedCacheControl      = "public, max-age=300"
	staticCacheControl    = "public, max-age=3600"
	immutableCacheControl = "public, max-age=31536000, immutable"
	artifactCacheControl  = "public, max-age=60"
	ddragonCacheControl   = "public, max-age=3600"
	noStoreCacheControl   = "no-store"

	// gzipMinBytes is the size below which compressing costs more than it
	// saves: a body this small arrives in one segment either way, and the
	// gzip header and trailer are then pure overhead.
	gzipMinBytes = 512

	// maxArtifactBytes caps one artifact read. The published tree is a few
	// hundred kilobytes per partition; anything past this is not a page, it is
	// a fault with a size, and reading it would consume the pod's memory
	// instead of answering the request.
	maxArtifactBytes = 64 << 20

	// maxPathBytes caps the request path before it is split and matched.
	maxPathBytes = 512
)

// ServerOptions configures a Server. Both fields are optional; the zero value
// is a working server with its own metrics registry and a stderr logger.
type ServerOptions struct {
	Logger  *slog.Logger
	Metrics *obs.Metrics
}

// Server serves the tier.
type Server struct {
	renderer *Renderer
	log      *slog.Logger
	metrics  *obs.Metrics

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	faults   *prometheus.CounterVec
}

// NewServer builds the handler. It registers its own collectors on the metrics
// registry it was given, so every Server owns an independent registry and two
// servers in one process (as the tests build) cannot collide.
func NewServer(renderer *Renderer, opts ServerOptions) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	metrics := opts.Metrics
	if metrics == nil {
		metrics = obs.NewMetrics()
	}

	s := &Server{
		renderer: renderer,
		log:      log,
		metrics:  metrics,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "lolstats",
			Subsystem: "web",
			Name:      "requests_total",
			Help:      "HTTP requests served, by route pattern, method and status.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "lolstats",
			Subsystem: "web",
			Name:      "render_seconds",
			Help:      "Time to build a response body, by route pattern. It excludes the write, so it measures the renderer and the artifact reads.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"route"}),
		faults: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "lolstats",
			Subsystem: "web",
			Name:      "faults_total",
			Help:      "Responses that were not OK, by fault kind (not-found, artifact, schema, no-snapshot, render, method).",
		}, []string{"kind"}),
	}
	registry := metrics.Registry()
	registry.MustRegister(s.requests, s.duration, s.faults)
	registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "lolstats",
		Subsystem: "web",
		Name:      "ready",
		Help:      "1 when the tier can read a snapshot (or has published none and says so), 0 when its data layer is faulted.",
	}, func() float64 {
		if _, err := renderer.Site(); err != nil {
			return 0
		}
		return 1
	}))
	return s
}

// Handler returns the http.Handler to mount.
func (s *Server) Handler() http.Handler { return s }

// Metrics exposes the registry, which is what the tests assert against.
func (s *Server) Metrics() *obs.Metrics { return s.metrics }

// ServeHTTP routes one request and applies the response policy.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	resp := s.dispatch(r)
	rendered := time.Since(started)
	status, size := s.respond(w, r, resp)
	total := time.Since(started)

	if resp.route != "" {
		s.requests.WithLabelValues(resp.route, r.Method, strconv.Itoa(status)).Inc()
		s.duration.WithLabelValues(resp.route).Observe(rendered.Seconds())
	}
	if resp.fault != "" {
		s.faults.WithLabelValues(resp.fault).Inc()
	}

	// Probes and scrapes are excluded: they are the loudest and least
	// interesting lines in a log, and their status is already in the metrics.
	switch resp.route {
	case "healthz", "readyz", "metrics":
		return
	}
	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"route", resp.route,
		"status", status,
		"bytes", size,
		"render_ms", rendered.Milliseconds(),
		"total_ms", total.Milliseconds(),
		"fault", resp.fault,
	}
	if status >= http.StatusBadRequest {
		// A fault is the operator's problem and it carries what they need to
		// act: the route, the fault kind, and the status.
		s.log.Warn("request failed", attrs...)
		return
	}
	s.log.Debug("request", attrs...)
}

// routeUnknown is the metric label for a request that matched no route. It is
// a single label on purpose: an unmatched path is attacker-supplied, and a
// metric label per path would let a stranger fill the registry.
const routeUnknown = "unmatched"

// response is a completely built reply. Nothing is written until respond() has
// seen the whole body, which is what makes the entity tag and the encoding
// decisions possible on a body that is already final.
type response struct {
	status       int
	body         []byte
	contentType  string
	cacheControl string
	// retryAfter is set on the replies a reader can retry successfully.
	retryAfter int
	// route is the metric label, i.e. the pattern rather than the path.
	route string
	// fault is the fault kind this reply reports, if any.
	fault string
	// allow is the Allow header a 405 carries.
	allow string
	// assetAlias is the canonical path an aliased /_astro request was served
	// from, set only when the request's content hash was not this build's. It
	// is reported in a response header so a cutover can see, without guessing,
	// that a page got its stylesheet from a name this build never published.
	assetAlias string
}

// dispatch maps a request to a reply. It never writes; every failure - a bad
// path, an unknown route, a missing artifact, a broken template - comes back as
// a response so that the policy in respond() applies to it too.
func (s *Server) dispatch(r *http.Request) *response {
	path, ok := cleanPath(r.URL.Path)
	if !ok {
		return s.faultPage(r, routeUnknown, r.URL.Path, http.StatusNotFound, FaultNotFound,
			"the requested path is not a path this site serves")
	}

	// Nothing here accepts a request body: every view of the data is a URL, and
	// the tier says so rather than pretending a writer exists.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		reply := s.faultPage(r, routeUnknown, path, http.StatusMethodNotAllowed, FaultMethod,
			"this tier serves GET and HEAD; the request method was "+r.Method)
		reply.allow = "GET, HEAD"
		return reply
	}

	// The frozen design-system assets are matched before the routes: they are
	// exact paths under /_astro, /fonts and /favicon.svg, none of which can
	// collide with a page.
	if body, contentType, found := staticAsset(path); found {
		return assetResponse(path, body, contentType)
	}

	// A browser holding HTML from the tier this one replaced asks for that
	// build's content-hashed asset names. Only the hash differs between builds,
	// so an unknown hash is answered with the current asset of the same name and
	// extension; a name this tier does not have is still a 404. See
	// astroAssetAlias for why the per-family cutover depends on this.
	if canonical, body, contentType, found := astroAssetAlias(path); found {
		return aliasAssetResponse(path, canonical, body, contentType)
	}

	switch path {
	case "/":
		return s.page(r, "home", path, func() (*Page, error) { return s.renderer.HomePage() })
	case "/about":
		return s.page(r, "about", path, func() (*Page, error) { return s.renderer.AboutPage() })
	case "/disclaimer":
		return s.page(r, "disclaimer", path, func() (*Page, error) { return s.renderer.DisclaimerPage() })
	case "/legal/terms":
		return s.page(r, "legal-terms", path, func() (*Page, error) { return s.renderer.TermsPage() })
	case "/legal/privacy":
		return s.page(r, "legal-privacy", path, func() (*Page, error) { return s.renderer.PrivacyPage() })
	case "/sitemap.xml":
		{
			body, err := s.renderer.Sitemap()
			return s.feed(r, "sitemap.xml", xmlContentType, body, err)
		}
	case "/robots.txt":
		{
			body, err := s.renderer.Robots()
			return s.feed(r, "robots.txt", textContentType, body, err)
		}
	case "/riot.txt":
		return s.riot(r, path)
	case "/healthz":
		return &response{status: http.StatusOK, body: []byte("ok"), contentType: textContentType,
			cacheControl: noStoreCacheControl, route: "healthz"}
	case "/readyz":
		return s.ready(r)
	case "/metrics":
		return s.metricsResponse(r)

	// The data explorer and its two downloads. They are exact paths rather than
	// rules on the segments below, because /explore is one page with two named
	// exports and nothing else under it: a reader who invents a third name gets
	// the 404 the route table promises rather than a guess.
	case "/explore":
		return s.explore(r, path)
	case "/explore/export.csv":
		return s.exploreExportCSV(r, path)
	case "/explore/export.json":
		return s.exploreExportJSON(r, path)
	}

	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	switch {
	case parts[0] == "agg":
		return s.artifact(r, path, parts[1:])
	case len(parts) == 2 && parts[0] == "tier-list":
		return s.tierList(r, path, parts[1], "")
	case len(parts) == 2 && parts[0] == "matchups":
		return s.matchups(r, path, parts[1])
	case len(parts) == 4 && parts[0] == "patch" && parts[2] == "tier-list":
		// /patch/<version>/tier-list/<role>: the same table for a patch that is
		// no longer the newest one, which is how the patch switcher works
		// without JavaScript.
		return s.tierList(r, path, parts[3], parts[1])
	case len(parts) == 2 && parts[0] == "champions":
		return s.champion(r, path, parts[1], "")
	case len(parts) == 3 && parts[0] == "champions":
		return s.champion(r, path, parts[1], parts[2])
	}
	return s.faultPage(r, routeUnknown, path, http.StatusNotFound, FaultNotFound,
		"no route of this site matches "+path)
}

// page renders a route with no query parameters of its own.
func (s *Server) page(r *http.Request, route string, path string, build func() (*Page, error)) *response {
	page, err := build()
	if err != nil {
		return s.errResponse(r, route, path, err)
	}
	return s.pageResponse(r, route, path, page)
}

// pageResponse renders a built page through the shell.
func (s *Server) pageResponse(r *http.Request, route string, path string, page *Page) *response {
	var buf bytes.Buffer
	if err := s.renderer.Render(&buf, page); err != nil {
		return s.errResponse(r, route, path, err)
	}
	return &response{
		status:       http.StatusOK,
		body:         buf.Bytes(),
		contentType:  htmlContentType,
		cacheControl: htmlCacheControl,
		route:        route,
	}
}

// tierList serves /tier-list/<role> and /patch/<version>/tier-list/<role>,
// including their query-parameter views.
func (s *Server) tierList(r *http.Request, path string, role string, patch string) *response {
	route := "tier-list"
	if patch != "" {
		route = "patch-tier-list"
	}
	if !slugShaped(role) {
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"no role of this site matches "+role)
	}
	if patch != "" && patchShaped(patch) == "" {
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"no patch of this site matches "+patch)
	}
	if refusal := s.requireSnapshot(r, route, path); refusal != nil {
		return refusal
	}

	def := DefaultTierListQuery()
	query := ParseQuery(r.URL.Query(), def, SortKeys(TierListColumns(false)))
	// ?patch= is patch switching inside the plain route: same URL shape as the
	// newest patch, one parameter away from it. Both spellings resolve to the
	// same renderer, so a reader with JavaScript off can switch patches from
	// either the switcher's links or a hand-written URL.
	if patch == "" && query.Patch != "" {
		patch = query.Patch
		route = "patch-tier-list"
	}
	if patch != "" {
		// On the patch route the patch is already in the path, so a redundant
		// ?patch= is dropped: every control the page emits then links to the
		// canonical address, and two URLs cannot name the same view.
		query.Patch = ""
	}

	build := func() (*Page, error) {
		if patch != "" {
			return s.renderer.PatchTierListPage(role, patch, query, true)
		}
		return s.renderer.TierListPage(role, query, true)
	}
	page, err := build()
	if err != nil {
		return s.errResponse(r, route, path, err)
	}
	return s.pageResponse(r, route, path, page)
}

// matchups serves /matchups/<role>. The query's filter carries a champion's
// complete row and its window carries the rest of the role a page at a time; the
// island works on the same markup.
// explore serves /explore, the data explorer: the published aggregate snapshot
// rendered as one row per (champion, role) cell, with the sample size and the
// interval behind every rate and the whole artifact downloadable beside it.
//
// Like the tier list, it refuses to render at all when nothing has been
// published: the difference between "no cell reached the floor" and "nothing
// was measured" is the difference this site does not blur, so the answer is the
// fault page that says so rather than an empty table.
func (s *Server) explore(r *http.Request, path string) *response {
	const route = "explore"
	if refusal := s.requireSnapshot(r, route, path); refusal != nil {
		return refusal
	}
	page, err := s.renderer.ExplorePage(r.URL.Query(), true)
	if err != nil {
		return s.errResponse(r, route, path, err)
	}
	return s.pageResponse(r, route, path, page)
}

// exploreExportCSV serves /explore/export.csv: the published tier-list artifact
// projected to one row per cell. The projection is the download's own shape, and
// the page that links it says so.
func (s *Server) exploreExportCSV(r *http.Request, path string) *response {
	const route = "explore-export-csv"
	if refusal := s.requireSnapshot(r, route, path); refusal != nil {
		return refusal
	}
	body, err := s.renderer.ExploreExportCSV(r.URL.Query())
	if err != nil {
		return s.exploreExportFault(r, route, path, err)
	}
	return &response{
		status:       http.StatusOK,
		body:         body,
		contentType:  exploreCSVContentType,
		cacheControl: artifactCacheControl,
		route:        route,
	}
}

// exploreExportJSON serves /explore/export.json: the published tier-list
// artifact byte for byte, read from the root the snapshot was resolved from. It
// is a route of this tier and not a window onto the published tree - there is no
// route under /agg here, and this handler reads one file the route table chose,
// never a path the request chose.
func (s *Server) exploreExportJSON(r *http.Request, path string) *response {
	const route = "explore-export-json"
	if refusal := s.requireSnapshot(r, route, path); refusal != nil {
		return refusal
	}
	body, err := s.renderer.ExploreExportJSON(r.URL.Query())
	if err != nil {
		return s.exploreExportFault(r, route, path, err)
	}
	return &response{
		status:       http.StatusOK,
		body:         body,
		contentType:  jsonContentType,
		cacheControl: artifactCacheControl,
		route:        route,
	}
}

// exploreExportFault renders a refused download. An export this tier will not
// serve is a fault with a reason, not an error page with a stack: the reader
// asked for the artifact and the answer says which part of that promise failed.
func (s *Server) exploreExportFault(r *http.Request, route string, path string, err error) *response {
	var refused *exploreExportError
	if errors.As(err, &refused) {
		return s.faultPage(r, route, path, refused.Status, refused.Kind, refused.Detail)
	}
	return s.errResponse(r, route, path, err)
}

// requireSnapshot is the guard the number-bearing routes run before they
// render. A tier list or a matchup table is a claim about matches that were
// played; with nothing published there is nothing to claim, and the difference
// between "no games met the floor" and "no games were measured" is exactly the
// difference this site refuses to blur. The answer is a 503 whose page says so,
// not a 200 whose table is empty.
func (s *Server) requireSnapshot(r *http.Request, route string, path string) *response {
	site, err := s.renderer.Site()
	if err != nil {
		return s.errResponse(r, route, path, err)
	}
	if site.Latest() == nil {
		return s.faultPage(r, route, path, http.StatusServiceUnavailable, FaultNoSnapshot, "")
	}
	return nil
}

func (s *Server) matchups(r *http.Request, path string, role string) *response {
	const route = "matchups"
	if !slugShaped(role) {
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"no role of this site matches "+role)
	}
	if refusal := s.requireSnapshot(r, route, path); refusal != nil {
		return refusal
	}
	query := ParseQuery(r.URL.Query(), DefaultMatchupQuery(), SortKeys(TierListColumns(false)))
	page, err := s.renderer.MatchupsPage(role, query, true)
	if err != nil {
		return s.errResponse(r, route, path, err)
	}
	return s.pageResponse(r, route, path, page)
}

// champion serves /champions/<slug> and /champions/<slug>/<role>.
func (s *Server) champion(r *http.Request, path string, slug string, role string) *response {
	const route = "champions"
	if !slugShaped(slug) {
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"no champion of this site matches "+slug)
	}
	if role == "" {
		page, err := s.renderer.ChampionPage(slug)
		if err != nil {
			return s.errResponse(r, route, path, err)
		}
		return s.pageResponse(r, route, path, page)
	}
	if !slugShaped(role) {
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"no role of this site matches "+role)
	}
	page, err := s.renderer.ChampionRolePage(slug, role)
	if err != nil {
		return s.errResponse(r, route, path, err)
	}
	return s.pageResponse(r, route, path, page)
}

// feed answers the build-time feeds: /sitemap.xml and /robots.txt. They are
// generated from the same snapshot the pages are, so they change when the
// published patch does and are cached for minutes rather than for a build.
func (s *Server) feed(r *http.Request, name string, contentType string, body []byte, err error) *response {
	if err != nil {
		return s.errResponse(r, name, name, err)
	}
	return &response{
		status:       http.StatusOK,
		body:         body,
		contentType:  contentType,
		cacheControl: feedCacheControl,
		route:        name,
	}
}

// riot answers /riot.txt. An unconfigured token is a 404 rather than an empty
// 200: Riot's site-verification flow asks for a token at a fixed path, and a
// 200 with no token would assert a verification that has not happened.
func (s *Server) riot(r *http.Request, path string) *response {
	body, found := s.renderer.RiotToken()
	if !found {
		return s.faultPage(r, "riot.txt", path, http.StatusNotFound, FaultNotFound,
			"no Riot site-verification token is configured, so /riot.txt is not published")
	}
	return &response{
		status:       http.StatusOK,
		body:         body,
		contentType:  textContentType,
		cacheControl: noStoreCacheControl,
		route:        "riot.txt",
	}
}

// ready is the readiness probe: it answers for the data layer rather than for
// the process. A tier that has published no snapshot is ready (its pages say
// so), a tier whose snapshot cannot be read is not.
func (s *Server) ready(r *http.Request) *response {
	type body struct {
		Status  string `json:"status"`
		OK      bool   `json:"ok"`
		State   string `json:"state,omitempty"`
		Patch   string `json:"latest_patch,omitempty"`
		Patches int    `json:"patches,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	site, err := s.renderer.Site()
	if err != nil {
		payload, _ := json.Marshal(body{Status: "unavailable", OK: false, Error: err.Error()})
		return &response{
			status:       http.StatusServiceUnavailable,
			body:         append(payload, '\n'),
			contentType:  jsonContentType,
			cacheControl: noStoreCacheControl,
			retryAfter:   60,
			route:        "readyz",
			fault:        FaultKindOf(err),
		}
	}
	reply := body{Status: "ok", OK: true, State: string(site.State()), Patches: len(site.Patches())}
	if latest := site.Latest(); latest != nil {
		reply.Patch = latest.Patch
		// Readiness is about whether this tier can serve the pages it serves,
		// not about whether a manifest parses. The newest partition's tier list
		// is the artifact every ladder page above the fold is rendered from,
		// so a manifest that advertises it and a file that is not there is a
		// tier that must not be sent traffic - and a rolling update that would
		// replace a healthy pod with one like this has to be able to see it.
		if _, err := site.TierList(SegOf(*latest)); err != nil {
			payload, _ := json.Marshal(body{
				Status:  "unavailable",
				OK:      false,
				State:   reply.State,
				Patch:   reply.Patch,
				Patches: reply.Patches,
				Error:   err.Error(),
			})
			return &response{
				status:       http.StatusServiceUnavailable,
				body:         append(payload, '\n'),
				contentType:  jsonContentType,
				cacheControl: noStoreCacheControl,
				retryAfter:   60,
				route:        "readyz",
				fault:        FaultKindOf(err),
			}
		}
	}
	payload, _ := json.Marshal(reply)
	return &response{
		status:       http.StatusOK,
		body:         append(payload, '\n'),
		contentType:  jsonContentType,
		cacheControl: noStoreCacheControl,
		route:        "readyz",
	}
}

// metricsResponse serves the Prometheus registry through the same policy as
// everything else. promhttp writes to the writer it is given, so it is given a
// writer that only collects, which also means a scrape of a faulted tier is
// still a scrape and not an error page.
func (s *Server) metricsResponse(r *http.Request) *response {
	captured := &captureWriter{header: http.Header{}}
	// promhttp compresses a scrape on its own when the client offers gzip, and
	// it announces that through the writer it was handed. Compression is this
	// tier's job - Content-Encoding, Content-Length and the entity tag have to
	// be decided together, or a cache holds a validator for bytes it cannot
	// describe - so the scrape is rendered identity and encoded once, below.
	scrape := r.Clone(r.Context())
	scrape.Header = r.Header.Clone()
	scrape.Header.Del("Accept-Encoding")
	s.metrics.Handler().ServeHTTP(captured, scrape)
	if captured.status == 0 {
		captured.status = http.StatusOK
	}
	contentType := captured.header.Get("Content-Type")
	if contentType == "" {
		contentType = textContentType
	}
	return &response{
		status:       captured.status,
		body:         captured.body.Bytes(),
		contentType:  contentType,
		cacheControl: noStoreCacheControl,
		route:        "metrics",
	}
}

// captureWriter is a ResponseWriter that keeps the reply in memory, so that
// promhttp's output goes through respond() like every other body.
type captureWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

func (c *captureWriter) Header() http.Header    { return c.header }
func (c *captureWriter) WriteHeader(status int) { c.status, c.wrote = status, true }
func (c *captureWriter) Write(p []byte) (int, error) {
	if !c.wrote {
		c.status, c.wrote = http.StatusOK, true
	}
	return c.body.Write(p)
}

// errResponse turns a view error into a page, and reports the error itself:
// the page names what failed, and the log carries the same thing for the
// operator, because a fault whose cause is only in the page is a fault nobody
// can act on from the cluster.
func (s *Server) errResponse(r *http.Request, route string, path string, err error) *response {
	status, kind := classifyFault(err)
	s.log.Warn("request could not be rendered",
		"route", route, "path", path, "status", status, "fault", kind, "error", err.Error())
	return s.faultPage(r, route, path, status, kind, err.Error())
}

// faultPage builds the visible page a fault is answered with. The shell is
// rendered when it can be - a missing artifact under a readable manifest still
// has a state, a banner and a nav - and the standalone document is used when
// the shell itself cannot be built (an unreadable manifest is the case: the
// loader cannot describe a site it cannot read). The standalone document says
// data-state="no-data", which is the only true thing it can say, and neither
// form is given an entity tag: a fault must not be replayable from a cache
// after the snapshot has been repaired.
func (s *Server) faultPage(r *http.Request, route string, path string, status int, kind string, detail string) *response {
	if route == "" {
		route = routeUnknown
	}
	reply := &response{
		status:       status,
		contentType:  htmlContentType,
		cacheControl: noStoreCacheControl,
		retryAfter:   retryAfterFor(status),
		route:        route,
		fault:        kind,
	}

	page := s.renderer.ErrorPage(path, status, kind, detail)
	var buf bytes.Buffer
	if err := s.renderer.Render(&buf, page); err == nil {
		reply.body = buf.Bytes()
		return reply
	} else {
		s.log.Warn("the shell could not be rendered for an error page",
			"route", route, "fault", kind, "status", status, "error", err)
	}

	body, err := s.renderer.RenderStandaloneError(path, status, kind, detail)
	if err != nil {
		// A template fault this deep is a bug in this tier, and the last thing
		// it may do is answer with a 200 and no page.
		s.log.Error("the standalone error page could not be rendered",
			"route", route, "fault", kind, "status", status, "error", err)
		body = []byte(lastResortDocument(status, kind, detail))
	}
	reply.body = body
	return reply
}

// retryAfterFor is the advice a fault carries: only a fault that a republish
// can fix is worth retrying.
func retryAfterFor(status int) int {
	if status == http.StatusServiceUnavailable {
		return 60
	}
	return 0
}

// respond writes a reply under the tier's policy and returns the status it
// actually sent (which is 304 when the reader's copy is current) and the number
// of body bytes written.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, resp *response) (int, int) {
	header := w.Header()
	if resp.contentType != "" {
		header.Set("Content-Type", resp.contentType)
	}
	if resp.cacheControl != "" {
		header.Set("Cache-Control", resp.cacheControl)
	}
	if resp.retryAfter > 0 {
		header.Set("Retry-After", strconv.Itoa(resp.retryAfter))
	}
	if resp.allow != "" {
		header.Set("Allow", resp.allow)
	}
	if resp.assetAlias != "" {
		// Not a standard header: it names the canonical file an older hash was
		// answered from, which is the one fact a reader of the response cannot
		// derive from the URL.
		header.Set("X-Asset-Alias", resp.assetAlias)
	}
	header.Set("X-Content-Type-Options", "nosniff")

	compressible := isCompressible(resp.contentType)
	if compressible {
		// Without this a cache in front of the browser may replay the
		// identity-encoded body to a client that asked for gzip.
		header.Add("Vary", "Accept-Encoding")
	}

	// The entity tag is computed over the identity body and weakened when the
	// body is actually sent compressed, which is what makes it a correct
	// entity tag for both representations.
	var etag string
	if len(resp.body) > 0 && resp.cacheControl != noStoreCacheControl {
		etag = hashBody(resp.body)
	}
	encoding := ""
	if compressible && len(resp.body) >= gzipMinBytes && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		encoding = "gzip"
	}
	if etag != "" {
		if encoding == "gzip" {
			header.Set("ETag", "W/"+etag)
		} else {
			header.Set("ETag", etag)
		}
	}

	if etag != "" && etagMatches(r.Header.Get("If-None-Match"), etag) {
		// A 304 carries the validators and the cache directive and nothing
		// else, which is all a cache needs to refresh its entry.
		header.Del("Content-Type")
		header.Del("Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return http.StatusNotModified, 0
	}

	if encoding == "gzip" {
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		if _, err := zw.Write(resp.body); err != nil {
			// The body is in memory, so a write can only fail on the gzip
			// writer's own state. Nothing has been sent yet, so falling through
			// to the identity body below is a complete answer rather than a
			// truncated one.
			s.log.Error("gzip failed, sending the identity body", "route", resp.route, "error", err)
		} else if err := zw.Close(); err != nil {
			s.log.Error("gzip failed, sending the identity body", "route", resp.route, "error", err)
		} else {
			header.Set("Content-Encoding", "gzip")
			header.Set("Content-Length", strconv.Itoa(compressed.Len()))
			w.WriteHeader(resp.status)
			if r.Method == http.MethodHead {
				return resp.status, 0
			}
			n, _ := w.Write(compressed.Bytes())
			return resp.status, n
		}
		header.Del("Content-Encoding")
		if etag != "" {
			// The identity body is being sent after all, so it carries the
			// strong tag rather than the weak one that stood for "either
			// encoding is fine".
			header.Set("ETag", etag)
		}
	}

	header.Set("Content-Length", strconv.Itoa(len(resp.body)))
	w.WriteHeader(resp.status)
	if r.Method == http.MethodHead {
		return resp.status, 0
	}
	n, _ := w.Write(resp.body)
	return resp.status, n
}

// hashBody is the entity tag of a body: a quoted, strong, content-derived tag,
// so two different bodies can never share one.
func hashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// etagMatches is If-None-Match: `*` matches anything, and a listed tag matches
// whether or not either side carries the weak marker, because both sides here
// name the same identity body.
func etagMatches(ifNoneMatch string, etag string) bool {
	if strings.TrimSpace(ifNoneMatch) == "" {
		return false
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		if strings.TrimPrefix(candidate, "W/") == want {
			return true
		}
	}
	return false
}

// acceptsGzip parses Accept-Encoding, honouring an explicit q=0 and the `*`
// wildcard.
func acceptsGzip(acceptEncoding string) bool {
	if strings.TrimSpace(acceptEncoding) == "" {
		return false
	}
	wildcard, gzipAllowed := false, false
	for _, part := range strings.Split(acceptEncoding, ",") {
		fields := strings.Split(part, ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		quality, ok := 1.0, true
		for _, parameter := range fields[1:] {
			parameter = strings.TrimSpace(parameter)
			if !strings.HasPrefix(parameter, "q=") {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64)
			if err != nil {
				ok = false
				break
			}
			quality = value
		}
		if !ok || quality == 0 {
			continue
		}
		switch name {
		case "gzip", "x-gzip":
			gzipAllowed = true
		case "*":
			wildcard = true
		}
	}
	return gzipAllowed || wildcard
}

// isCompressible reports whether a content type is text that compresses. The
// binary assets (fonts, the SVG) are excluded: they are already compressed or
// nearly incompressible, and a browser reads them once.
func isCompressible(contentType string) bool {
	switch {
	case strings.HasPrefix(contentType, "text/"):
		return true
	case strings.HasPrefix(contentType, "application/json"):
		return true
	case strings.HasPrefix(contentType, "application/xml"):
		return true
	case strings.HasPrefix(contentType, "image/svg+xml"):
		return true
	}
	return false
}

// classifyFault maps a view error to a status and a fault kind. Only a fault in
// the published artifact tree is a 503: it is the one failure a republish fixes
// and therefore the one worth retrying. An unknown route is a 404 because no
// file ever existed for it, and anything else is this tier's own bug and is
// answered as one.
func classifyFault(err error) (int, string) {
	var schemaErr *SchemaVersionError
	var artifactErr *ArtifactError
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, FaultNotFound
	case errors.As(err, &schemaErr):
		return http.StatusServiceUnavailable, FaultSchema
	case errors.As(err, &artifactErr):
		return http.StatusServiceUnavailable, FaultArtifact
	case errors.Is(err, ErrNoSnapshot):
		return http.StatusServiceUnavailable, FaultNoSnapshot
	}
	return http.StatusInternalServerError, FaultRender
}

// FaultKindOf names the fault an error is, for the metrics and the tests.
func FaultKindOf(err error) string {
	_, kind := classifyFault(err)
	return kind
}

// cleanPath normalises the request path: one trailing slash is what the
// canonical links carry, so /tier-list/mid and /tier-list/mid/ are the same
// page and neither is redirected away from.
func cleanPath(raw string) (string, bool) {
	if raw == "" || raw[0] != '/' || len(raw) > maxPathBytes {
		return "", false
	}
	path := raw
	for len(path) > 1 && strings.HasSuffix(path, "/") {
		path = path[:len(path)-1]
	}
	if path == "" {
		path = "/"
	}
	if strings.Contains(path, "\x00") || strings.Contains(path, "\\") {
		return "", false
	}
	for _, segment := range strings.Split(path, "/") {
		// A decoded path may contain anything; nothing legitimate here does.
		if segment == ".." || segment == "." {
			return "", false
		}
	}
	return path, true
}

// assetResponse serves one of the embedded design-system files. The TTL is the
// one the static site could honestly promise: Astro put a content hash in every
// filename it emitted under /_astro, so those URLs can only ever name one byte
// sequence. /fonts is not content-hashed, so it keeps the shorter TTL.
func assetResponse(path string, body []byte, contentType string) *response {
	cacheControl := staticCacheControl
	if strings.HasPrefix(path, "/_astro/") {
		cacheControl = immutableCacheControl
	}
	return &response{
		status:       http.StatusOK,
		body:         body,
		contentType:  contentType,
		cacheControl: cacheControl,
		route:        "asset",
	}
}

// aliasAssetResponse serves the current asset of a name whose hash this build
// does not know. It is deliberately not covered by the immutable directive: the
// URL names the bytes of an older build and this reply is not those bytes, so
// promising a year of immutability would be a false promise that outlives the
// next three deploys. It gets the ordinary static TTL instead, and the entity
// tag still revalidates it, so a client that kept the URL converges on the
// current asset within an hour of the next build.
//
// The alias is named in a response header rather than hidden: a reader who is
// puzzled by a 200 for a hash this build never published can see which canonical
// file answered, and route="asset-alias" in the request metrics counts them.
func aliasAssetResponse(path, canonical string, body []byte, contentType string) *response {
	return &response{
		status:       http.StatusOK,
		body:         body,
		contentType:  contentType,
		cacheControl: staticCacheControl,
		route:        "asset-alias",
		assetAlias:   canonical,
	}
}

// artifact serves the published aggregate tree under /agg, byte for byte as the
// tier before this one did, so the artifacts and the pages that were rendered
// from them stay one origin and a reader can check a number against its source.
//
// The tree is served from the root the current snapshot was resolved from, so a
// tier running on the checked-in demo fixtures serves those under the same
// paths. That is the honest pairing: the pages say PREVIEW, and /agg/v1 is the
// artifact they were rendered from.
func (s *Server) artifact(r *http.Request, path string, rest []string) *response {
	const route = "agg"
	root, err := s.renderer.Loader().RootDir()
	if err != nil {
		return s.errResponse(r, route, path, err)
	}
	if root == "" {
		return s.faultPage(r, route, path, http.StatusServiceUnavailable, FaultNoSnapshot,
			"no aggregate snapshot has been published, so there is nothing under /agg yet")
	}
	relative, ok := artifactPath(rest)
	if !ok {
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"no artifact of the published tree matches "+path)
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	if !withinDir(root, full) {
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"no artifact of the published tree matches "+path)
	}

	info, statErr := os.Stat(full)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			"the published tree has no "+relative)
	case statErr != nil:
		return s.errResponse(r, route, path, artifactFault(full, "cannot be read: %v", statErr))
	case info.IsDir():
		return s.faultPage(r, route, path, http.StatusNotFound, FaultNotFound,
			relative+" is a directory of the published tree, not an artifact")
	case info.Size() > maxArtifactBytes:
		return s.faultPage(r, route, path, http.StatusInternalServerError, FaultRender,
			relative+" is larger than this tier will read in one response")
	}

	body, err := os.ReadFile(full) // #nosec G304 -- full is a route's artifact under the snapshot root; the route table, not the request, chose it
	if err != nil {
		return s.errResponse(r, route, path, artifactFault(full, "cannot be read: %v", err))
	}

	relative = filepath.ToSlash(relative)
	cacheControl := artifactCacheControl
	if strings.HasPrefix(relative, "v1/static/") {
		// The Data Dragon projection: one version's items, runes and summoner
		// spells, which cannot change while the version stands.
		cacheControl = ddragonCacheControl
	}
	return &response{
		status:       http.StatusOK,
		body:         body,
		contentType:  contentTypeForArtifact(relative),
		cacheControl: cacheControl,
		route:        route,
	}
}

// artifactPath joins the path segments under /agg, refusing anything that is
// not a plain segment: no empty ones, no dot segments, no separators smuggled
// into a segment. The tree is read-only and public, but a path is still a path.
func artifactPath(rest []string) (string, bool) {
	if len(rest) == 0 {
		return "", false
	}
	segments := make([]string, 0, len(rest))
	for _, segment := range rest {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\\x00") {
			return "", false
		}
		segments = append(segments, segment)
	}
	return strings.Join(segments, "/"), true
}

// withinDir reports whether full is inside root, which is the check that makes
// the join above safe rather than merely tidy.
func withinDir(root string, full string) bool {
	clean := filepath.Clean(root)
	return strings.HasPrefix(full, clean+string(os.PathSeparator))
}

// contentTypeForArtifact types a published artifact by its extension. The tree
// is JSON with the occasional CSV.
func contentTypeForArtifact(relative string) string {
	switch strings.ToLower(filepath.Ext(relative)) {
	case ".json":
		return jsonContentType
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".txt":
		return textContentType
	}
	return "application/octet-stream"
}

// lastResortDocument is the page of last resort: it is written when even the
// standalone error template cannot be executed. It exists so that no code path
// in this tier can answer a fault with an empty body, and it is intentionally
// plain markup with no embedded assets, because by the time it is used even the
// embedded ones have failed to render.
func lastResortDocument(status int, kind string, detail string) string {
	return "<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\">" +
		"<title>LoL Stats is unavailable</title><meta name=\"robots\" content=\"noindex,nofollow\">" +
		"</head><body data-state=\"no-data\"><main id=\"main\">" +
		"<h1>LoL Stats is unavailable</h1>" +
		"<p class=\"fallback-fault\" data-fault=\"" + EscapeString(kind) + "\" data-status=\"" + strconv.Itoa(status) + "\">" +
		"This page could not be rendered.</p>" +
		"<p>This site renders every page from the aggregate snapshot it publishes. That snapshot could not be read, " +
		"so there is no page to show, and this tier does not serve a partial one.</p>" +
		"<p><strong>Reported by this server:</strong> <code>" + EscapeString(detail) + "</code></p>" +
		"</main></body></html>"
}
