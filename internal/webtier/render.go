package webtier

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Rendering is a port of the published Astro build, and the published build
// emits its markup with no whitespace between tags. Templates here therefore
// carry their whitespace literally: a newline in a template file is a newline in
// the response, which is what makes byte-level comparison against web/dist
// meaningful instead of approximate. Keep the markup on one line.
//
//go:embed templates
var templateFS embed.FS

// Page is a rendered page before the shell is applied.
type Page struct {
	Title       string
	Description string
	// CanonicalPath is the site-relative path of the page, with the trailing
	// slash the reference build passes to BaseLayout.
	CanonicalPath string
	Noindex       bool
	// Active is the href the nav marks with aria-current="page".
	Active string
	// Body is the page's own markup, inserted inside <main id="main">.
	Body string
	// JSONLD is the serialised structured-data node for the page, if any.
	JSONLD string
	// Champion marks the pages that carry the extra scoped-style chunk.
	Champion bool
	// Partition is the snapshot the page is about. It is lib/page.ts's
	// snapshot(patch).partition: on /patch/<v>/... routes it is that patch's
	// partition rather than the newest one, and the banner, the nav patch label
	// and the footer all read the page's snapshot rather than the newest one.
	Partition *aggmodel.Partition
	// PatchLabelValue is the page's patch label when it is not simply the
	// partition's patch. page.ts calls a snapshot with no partition "not
	// published" rather than falling back to the newest patch, so a request for
	// a patch that is not in the manifest renders that, and the nav and the
	// footer say it too.
	PatchLabelValue string
}

// Renderer turns a Page plus the loaded snapshot into a response body.
type Renderer struct {
	loader  *Loader
	siteURL string
	tmpl    *template.Template
	// riotToken is Riot's site-verification token. It is empty unless the
	// deployment configured one, and an empty token means /riot.txt is not
	// published at all rather than published empty.
	riotToken string
}

// SetRiotToken configures the Riot site-verification token served at /riot.txt.
// An empty token leaves that path unpublished.
func (r *Renderer) SetRiotToken(token string) { r.riotToken = strings.TrimSpace(token) }

// RiotTokenConfigured reports whether /riot.txt is published at all. The path
// is not part of the site's data and is not listed anywhere that a crawler
// reads; a deployment may simply not need it.
func (r *Renderer) RiotTokenConfigured() bool { return r.riotToken != "" }

// NewRenderer compiles the template set. It fails rather than degrades: a
// template that does not parse is a build fault, not a request-time error.
func NewRenderer(loader *Loader, siteURL string) (*Renderer, error) {
	if siteURL == "" {
		siteURL = DefaultSiteURL
	}
	siteURL = strings.TrimRight(siteURL, "/")
	funcs := template.FuncMap{
		"integer":       IntegerAny,
		"percent":       Percent,
		"signedPP":      SignedPercentPoints,
		"decimal":       Decimal,
		"integerAny":    IntegerAny,
		"wholePercent":  WholePercent,
		"utcStamp":      UTCStampAny,
		"windowLabel":   WindowLabel,
		"shortCommit":   ShortCommit,
		"roleLabel":     RoleLabel,
		"roleSlug":      RoleSlugString,
		"tierRank":      TierRank,
		"legalDate":     legalDate,
		"championName":  championNameIn,
		"joined":        strings.Join,
		"scopedCSS":     scopedCSSChunk,
		"baseCSSHref":   func() string { return baseCSSPath },
		"riotNote":      proseFunc(RiotVerificationNote),
		"contactRoute":  prose0(ContactRoute),
		"contactEmail":  prose0(ContactEmail),
		"siteHost":      proseFunc(SiteHost),
		"notAffiliated": constProse(NotAffiliatedText),
		"nonEndorsed":   constProse(NonEndorsementText),
		"trademark":     constProse(TrademarkText),
		"freeTier":      constProse(FreeTierText),
		"noRating":      constProse(NoRatingText),
		"noBroker":      constProse(NoBrokerText),
		"operator":      constProse(OperatorIdentity),
		"versionLine":   constProse(VersionLine),
		"siteName":      constProse(SiteName),
		"empty":         emptyFunc,
	}
	tmpl, err := template.New("site").Funcs(funcs).ParseFS(templateFS, "templates/*.tmpl", "templates/pages/*.tmpl")
	if err != nil {
		return nil, err
	}
	return &Renderer{loader: loader, siteURL: siteURL, tmpl: tmpl}, nil
}

// SiteURL is the origin every canonical URL and structured-data node is built from.
func (r *Renderer) SiteURL() string { return r.siteURL }

// Loader exposes the snapshot loader the renderer reads through.
func (r *Renderer) Loader() *Loader { return r.loader }

// Site loads the snapshot for this request.
func (r *Renderer) Site() (*Site, error) { return r.loader.Site() }

// absolute returns the absolute URL of a site-relative path.
func (r *Renderer) absolute(path string) string {
	if path == "" {
		path = "/"
	}
	base, err := url.Parse(r.siteURL + "/")
	if err != nil {
		return r.siteURL + path
	}
	ref, err := url.Parse(path)
	if err != nil {
		return r.siteURL + path
	}
	return base.ResolveReference(ref).String()
}

// Render writes the complete page: the shell, the banner, the nav, the body and
// the footer, byte for byte as the reference build emits them.
func (r *Renderer) Render(w io.Writer, page *Page) error {
	site, err := r.loader.Site()
	if err != nil {
		return err
	}
	data, err := r.shellData(site, page)
	if err != nil {
		return err
	}
	return r.tmpl.ExecuteTemplate(w, "shell", data)
}

// RenderBody renders a page's own markup into the string Page.Body carries. The
// view value is the template's root.
func (r *Renderer) RenderBody(name string, data any) (string, error) {
	if _, err := r.loader.Site(); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := r.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// base is embedded by every page view so templates can reach the snapshot.
type base struct {
	Site    *Site
	SiteURL string
	R       *Renderer
}

// shellData is everything the shell needs, precomputed so the template stays a
// literal transcription of the reference markup.
type shellData struct {
	Title       string
	Description string
	Canonical   string
	Robots      string
	State       string
	Source      string
	ScopedCSS   template.CSS
	FrozenCSS   template.CSS
	Stylesheet  string
	Banner      bannerView
	Nav         navView
	Footer      footerView
	Body        template.HTML
	JSONLD      template.JS
}

type bannerView struct {
	Class    string
	Style    template.CSS
	Heading  string
	Body     template.HTML
	LinkHref string
	LinkText string
}

type navLink struct {
	Label   string
	Href    string
	Current bool
}

type navView struct {
	TierLinks    []navLink
	MatchupLinks []navLink
	Patch        string
	// ExploreCurrent marks the Data explorer entry. It is derived from the
	// canonical path as well as the active section because the data-explorer
	// view does not set Active.
	ExploreCurrent bool
}

type footerKV struct {
	Key   string
	Value string
}

type footerView struct {
	Items []footerKV
	Links []navLink
	Riot  string
}

func (r *Renderer) shellData(site *Site, page *Page) (*shellData, error) {
	path := page.CanonicalPath
	if path == "" {
		path = "/"
	}
	active := page.Active
	data := &shellData{
		Title:       page.Title,
		Description: page.Description,
		Canonical:   r.absolute(path),
		State:       string(site.State()),
		Source:      site.SourceAttribute(),
		ScopedCSS:   trustedCSS(scopedCSSChunk(page.Champion)),
		FrozenCSS:   trustedCSS(frozenCSSChunk()),
		Stylesheet:  baseCSSPath,
		Body:        trustedHTML(page.Body),
		JSONLD:      trustedJS(page.JSONLD),
	}
	if page.Noindex {
		data.Robots = "noindex,follow"
	} else {
		data.Robots = "index,follow"
	}
	data.Banner = bannerFor(site, r.siteURL, page)
	data.Nav = navFor(site, active, page.CanonicalPath, page.patchLabel(site))
	data.Footer = footerFor(site, page)
	return data, nil
}

// bannerFor reproduces layouts/StateBanner.astro. The banner is inline-styled on
// purpose: it is the one element whose visibility is a correctness requirement,
// so it must survive a missing stylesheet.
func bannerFor(site *Site, siteURL string, page *Page) bannerView {
	const warning = "background:#111111;color:#ffffff;border:4px solid #ffd400;padding:0.9rem 1rem;"
	const calm = "background:#f4f4f4;color:#111111;border:4px solid #111111;padding:0.9rem 1rem;"
	banner := site.Banner()
	if site.State() == StateLive {
		body := "Patch " + template.HTMLEscapeString(page.patchLabel(site))
		if generatedAt := site.GeneratedAt(); generatedAt != "" {
			body += " &middot; generated " + template.HTMLEscapeString(UTCStamp(generatedAt))
		}
		if window := site.SourceWindow(); window.From != "" || window.To != "" {
			body += " &middot; source window " + template.HTMLEscapeString(WindowLabel(window.From, window.To))
		}
		if site.HasMinCellN() {
			body += " &middot; cells published from n = " + template.HTMLEscapeString(IntegerAny(site.MinCellN())) + " games"
		}
		if site.SuppressedCells() > 0 {
			body += " &middot; " + template.HTMLEscapeString(IntegerAny(site.SuppressedCells())) + " cells withheld as too thin"
		}
		body += ". Read by machine from the published aggregate artifacts; no client-side fetching is involved."
		return bannerView{
			Class:    "state-banner--live",
			Style:    calm,
			Heading:  "Published snapshot",
			Body:     trustedHTML(body),
			LinkHref: banner.Href,
			LinkText: "How these numbers are produced",
		}
	}
	linkText := "What this preview is and is not"
	if banner.State == StateNoData {
		linkText = "What will be published here"
	}
	return bannerView{
		Class:    "state-banner--" + string(banner.State),
		Style:    warning,
		Heading:  banner.Heading,
		Body:     trustedHTML(template.HTMLEscapeString(banner.Body)),
		LinkHref: banner.Href,
		LinkText: linkText,
	}
}

// explorePath is the frozen URL of the data-explorer page; the nav links to it
// before the route itself lands.
const explorePath = "/explore"

// navFor reproduces components/Nav.astro, including the rule that a link is
// current when the active route matches with or without its trailing slash.
func navFor(site *Site, active string, canonicalPath string, patch string) navView {
	current := func(href string) bool { return isCurrent(active, href) }
	view := navView{Patch: patch}
	for _, role := range Roles {
		href := "/tier-list/" + RoleSlugString(role)
		view.TierLinks = append(view.TierLinks, navLink{Label: RoleLabel(role), Href: href, Current: current(href)})
		href = "/matchups/" + RoleSlugString(role)
		view.MatchupLinks = append(view.MatchupLinks, navLink{Label: RoleLabel(role), Href: href, Current: current(href)})
	}
	view.ExploreCurrent = current(explorePath) || isCurrent(canonicalPath, explorePath)
	return view
}

func isCurrent(active string, href string) bool {
	return active == href || active == href+"/"
}

// footerFor reproduces components/Footer.astro. The four compliance links are
// unconditional: no page can drop them.
func footerFor(site *Site, page *Page) footerView {
	view := footerView{
		Links: []navLink{
			{Label: "About", Href: "/about"},
			{Label: "Terms", Href: "/legal/terms"},
			{Label: "Privacy", Href: "/legal/privacy"},
			{Label: "Disclaimer", Href: "/disclaimer"},
		},
		Riot: TrademarkText + " " + NonEndorsementText,
	}
	view.Items = append(view.Items, footerKV{Key: "Patch", Value: page.patchLabel(site)})
	if window := page.sourceWindow(site); window.From != "" || window.To != "" {
		key := "Window covered"
		if site.State() == StateLive {
			key = "Matches played"
		}
		view.Items = append(view.Items, footerKV{Key: key, Value: WindowLabel(window.From, window.To)})
	}
	if generatedAt := page.generatedAt(site); generatedAt != "" {
		view.Items = append(view.Items, footerKV{Key: "Compiled", Value: UTCStamp(generatedAt)})
	}
	return view
}

// patchLabel is the patch the page is about, or "not published" when the
// snapshot has no partitions. It is page.ts's patchLabel(snapshot).
func (p *Page) patchLabel(site *Site) string {
	if p != nil && p.PatchLabelValue != "" {
		return p.PatchLabelValue
	}
	if p != nil && p.Partition != nil {
		return p.Partition.Patch
	}
	return site.PatchLabel()
}

// sourceWindow is the page's own window when it has one, so an archived patch
// does not report the newest patch's window.
func (p *Page) sourceWindow(site *Site) aggmodel.Window {
	if p != nil && p.Partition != nil {
		return p.Partition.SourceWindow
	}
	return site.SourceWindow()
}

// generatedAt is the page's own build stamp when it has one.
func (p *Page) generatedAt(site *Site) string {
	if p != nil && p.Partition != nil {
		return p.Partition.GeneratedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return site.GeneratedAt()
}

// PatchLabel on *Site is lib/page.ts's patchLabel(snapshot).
func (s *Site) PatchLabel() string {
	if latest := s.Latest(); latest != nil {
		return latest.Patch
	}
	return "not published"
}

// ChampionIDOf, championName and the JSON-LD helpers below are used by the
// statistics views.
func championNameIn(site *Site, id int) string { return site.ChampionName(id) }

// legalDate renders an ISO date the way legal.ts's legalDate does.
func legalDate(iso string) string {
	months := [...]string{"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December"}
	if len(iso) < 10 {
		return iso
	}
	year := iso[0:4]
	month := iso[5:7]
	day := iso[8:10]
	monthIndex := 0
	for i, part := range []string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12"} {
		if part == month {
			monthIndex = i
		}
	}
	return strings.TrimLeft(day, "0") + " " + months[monthIndex] + " " + year
}

// JSONLDNode serialises a structured-data node the way seo.ts does: as an array
// holding one object, with "<" escaped so the value can never close the script
// tag early. HTML escaping is otherwise off so the bytes match JSON.stringify.
func JSONLDNode(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode([]any{value}); err != nil {
		return "", err
	}
	encoded := strings.TrimRight(buf.String(), "\n")
	return strings.ReplaceAll(encoded, "<", `\u003c`), nil
}

var _ fs.FS = templateFS
