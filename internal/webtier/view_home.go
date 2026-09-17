package webtier

import (
	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// HomePage renders /, a port of web/src/pages/index.astro.
func (r *Renderer) HomePage() (*Page, error) {
	site, err := r.Site()
	if err != nil {
		return nil, err
	}
	live := site.State() == StateLive
	description := ""
	switch {
	case live:
		description = "Patch-specific League of Legends ranked tier lists and champion matchups, computed from Riot match data. " +
			"Every win rate carries its sample size, and thin samples are withheld rather than shown."
	case site.State() == StateDemo:
		description = "What this site publishes, how the numbers are produced, and an honest statement of where the numbers on " +
			"this build come from: it is a labelled preview, not measured Riot match data."
	default:
		description = "What this site publishes and how the numbers will be produced once a snapshot exists. No aggregate " +
			"snapshot has been published yet, so this build carries no match statistics at all."
	}
	title := "LoL Stats - League of Legends ranked statistics"
	if live {
		title = "LoL Stats - League of Legends ranked statistics from Riot match data"
	}
	url := r.absolute("/")
	jsonld, err := JSONLDNode(websiteNode(site, url, description))
	if err != nil {
		return nil, err
	}
	view := homeView{
		base:        base{Site: site, SiteURL: r.siteURL, R: r},
		Description: description,
		IntroTail:   homeIntroTail(site),
		EmptyReason: emptyReason(site),
		Threshold:   thresholdPhrase(site),
		Partition:   site.Latest(),
		Roles:       Roles,
	}
	body, err := r.RenderBody("index", view)
	if err != nil {
		return nil, err
	}
	return &Page{
		Title:         title,
		Description:   description,
		CanonicalPath: "/",
		Active:        "/",
		Body:          body,
		JSONLD:        jsonld,
	}, nil
}

type homeView struct {
	base
	Description string
	IntroTail   string
	EmptyReason string
	Threshold   string
	Partition   *aggmodel.Partition
	Roles       []aggmodel.Role
}

func homeIntroTail(site *Site) string {
	switch site.State() {
	case StateDemo:
		return "This build is a labelled preview: the numbers on it are illustrative, and no Riot match data has been ingested."
	case StateNoData:
		return "No aggregate snapshot has been published yet, so this build carries no match statistics at all."
	default:
		return ""
	}
}

// emptyReason is page.ts's emptyReason: what is missing, in the reader's terms.
func emptyReason(site *Site) string {
	if site.Latest() == nil {
		return "No aggregate snapshot has been published yet, so there are no match statistics to read. " +
			"This page will fill in as soon as one exists."
	}
	partition := site.Latest()
	return "The published snapshot for patch " + partition.Patch + " (" + partition.Region + ", queue " +
		IntegerAny(partition.Queue) + ", bracket " + string(partition.Bracket) + ") has no artifact for this selection, so nothing is shown rather than an estimate."
}

// thresholdPhrase is index.astro's "n = the publication threshold" fallback.
func thresholdPhrase(site *Site) string {
	if site.HasMinCellN() {
		return "n = " + IntegerAny(site.MinCellN()) + " games"
	}
	return "n = the publication threshold games"
}

// WebsiteNode is seo.ts's jsonLdWebsite. isAccessibleForFree is claimed only
// when the snapshot is not a live Riot-derived one.
type WebsiteNode struct {
	Context             string            `json:"@context"`
	Type                string            `json:"@type"`
	Name                string            `json:"name"`
	URL                 string            `json:"url"`
	Description         string            `json:"description"`
	InLanguage          string            `json:"inLanguage"`
	Publisher           *OrganizationNode `json:"publisher"`
	IsAccessibleForFree *bool             `json:"isAccessibleForFree,omitempty"`
}

// OrganizationNode is the publisher every node carries.
type OrganizationNode struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

// WebPageNode is seo.ts's jsonLdWebPage.
type WebPageNode struct {
	Context     string          `json:"@context"`
	Type        string          `json:"@type"`
	URL         string          `json:"url"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InLanguage  string          `json:"inLanguage"`
	IsPartOf    *WebSiteRefNode `json:"isPartOf"`
}

// WebSiteRefNode is the relative back-reference each WebPage carries.
type WebSiteRefNode struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func websiteNode(site *Site, url string, description string) WebsiteNode {
	node := WebsiteNode{
		Context:     "https://schema.org",
		Type:        "WebSite",
		Name:        SiteName,
		URL:         url,
		Description: description,
		InLanguage:  "en",
		Publisher:   &OrganizationNode{Type: "Organization", Name: SiteName},
	}
	if site.State() != StateLive {
		accessible := true
		node.IsAccessibleForFree = &accessible
	}
	return node
}

func webPageNode(url string, name string, description string) WebPageNode {
	return WebPageNode{
		Context:     "https://schema.org",
		Type:        "WebPage",
		URL:         url,
		Name:        name,
		Description: description,
		InLanguage:  "en",
		IsPartOf:    &WebSiteRefNode{Type: "WebSite", Name: SiteName, URL: "/"},
	}
}
