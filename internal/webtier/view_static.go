package webtier

import (
	"html/template"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The prose routes: /about, /disclaimer and the two legal pages. They carry the
// compliance surface, so their wording is transcribed from the reference build
// rather than rewritten, and the only dynamic values are the provenance ones.
// The state-conditional sentences come from prose.go, which is the single place
// the demo/live/no-data wording is decided.

type proseBase struct {
	base
	Preview     bool
	PreviewText template.HTML
	Partition   *aggmodel.Partition
	Live        bool
	Demo        bool
	NoData      bool
	RootLabel   template.HTML
	Source      template.HTML
	Patches     template.HTML
}

func (r *Renderer) proseBase(site *Site) proseBase {
	return proseBase{
		base:        base{Site: site, SiteURL: r.siteURL, R: r},
		Preview:     site.Demo(),
		PreviewText: Prose(PreviewText),
		Partition:   site.Latest(),
		Live:        site.Live(),
		Demo:        site.Demo(),
		NoData:      site.NoData(),
		RootLabel:   Prose(site.RootLabel()),
		Source:      Prose(site.Source()),
		Patches:     Prose(patchesInBuild(site)),
	}
}

type aboutView struct {
	proseBase
	IntroTail           template.HTML
	SourceSentence      template.HTML
	StateDescription    template.HTML
	CredentialSentence  template.HTML
	ProductionHeading   string
	ComputedHeading     string
	PipelineLeadIn      template.HTML
	PipelineStatus      template.HTML
	MatchAPI            template.HTML
	SourceBullet        template.HTML
	RankAttributionLead template.HTML
	QueueCoverage       template.HTML
	SourceAnnotation    template.HTML
	Empty               template.HTML
}

// AboutPage renders /about: how the numbers are produced, and what this build
// actually is.
func (r *Renderer) AboutPage() (*Page, error) {
	site, err := r.Site()
	if err != nil {
		return nil, err
	}
	title := "About the data - how these statistics are produced - " + SiteName
	description := "Where the numbers on this site will come from, how they are produced, the sample-size threshold below which a rate is withheld, the snapshot limitation on rank attribution, and what this site is not. This build publishes no Riot match data."
	if site.Live() {
		description = "Where the numbers on this site come from: Riot MATCH-V5 match records aggregated per patch, the crawl window, the sample-size threshold below which a rate is withheld, the snapshot limitation on rank attribution, and what this site is not."
	}
	jsonld, err := JSONLDNode(webPageNode(r.absolute("/about/"), title, description))
	if err != nil {
		return nil, err
	}
	queue := 420
	if latest := site.Latest(); latest != nil {
		queue = latest.Queue
	}
	view := aboutView{
		proseBase:           r.proseBase(site),
		IntroTail:           Prose(aboutIntroTail(site)),
		SourceSentence:      Prose(dataSourceSentence(site)),
		StateDescription:    Prose(stateDescription(site)),
		CredentialSentence:  Prose(credentialSentence(site)),
		ProductionHeading:   productionHeading(site),
		ComputedHeading:     aboutComputedHeading(site),
		PipelineLeadIn:      Prose(pipelineLeadIn(site)),
		PipelineStatus:      Prose(pipelineStatusSentence(site)),
		MatchAPI:            Prose(matchAPIName(site)),
		SourceBullet:        Prose(sourceBullet(site, queue)),
		RankAttributionLead: Prose(rankAttributionLead(site)),
		QueueCoverage:       Prose(queueCoverage(site)),
		SourceAnnotation:    Prose(sourceAnnotation(site)),
		Empty: emptyFunc(
			NoDataHeading,
			"No aggregate snapshot has been published, so there is no window, no threshold and no cell count to report. The layout, the labels and the sample-size notices are all in place; the numbers appear when the first snapshot is published.",
			nil,
		),
	}
	body, err := r.RenderBody("about", view)
	if err != nil {
		return nil, err
	}
	return &Page{
		Title:         title,
		Description:   description,
		CanonicalPath: "/about/",
		Active:        "/about",
		Body:          body,
		JSONLD:        jsonld,
	}, nil
}

// DisclaimerPage renders /disclaimer.
func (r *Renderer) DisclaimerPage() (*Page, error) {
	site, err := r.Site()
	if err != nil {
		return nil, err
	}
	title := "Disclaimer - " + SiteName
	description := "Non-endorsement and accuracy disclaimer for " + SiteName + ": an unofficial League of Legends statistics project, not endorsed by Riot Games and not a source of official or betting-usable figures."
	jsonld, err := JSONLDNode(webPageNode(r.absolute("/disclaimer/"), title, description))
	if err != nil {
		return nil, err
	}
	body, err := r.RenderBody("disclaimer", struct {
		proseBase
		Empty template.HTML
	}{
		proseBase: r.proseBase(site),
		Empty: emptyFunc(
			NoDataHeading,
			"Nothing is published yet, so there are no figures to disclaim. The disclaimers above apply regardless.",
			nil,
		),
	})
	if err != nil {
		return nil, err
	}
	return &Page{
		Title:         title,
		Description:   description,
		CanonicalPath: "/disclaimer/",
		Active:        "/disclaimer",
		Body:          body,
		JSONLD:        jsonld,
	}, nil
}

// PrivacyPage renders /legal/privacy.
//
// The page makes one claim about the pipeline rather than about the site - that
// a raw match archive exists behind the aggregates - and that claim is only true
// where a pipeline has run, so the template states it in whichever tense the
// build allows. Everything else on the page is prose plus the compliance
// constants, which are declared once in prose.go.
func (r *Renderer) PrivacyPage() (*Page, error) {
	site, err := r.Site()
	if err != nil {
		return nil, err
	}
	title := "Privacy Policy - " + SiteName
	description := SiteName + " privacy policy: the site serves static pages, sets no cookies of its own, has no accounts, forms or analytics, and the only third-party request a page makes is for champion icons from Riot Data Dragon."
	jsonld, err := JSONLDNode(webPageNode(r.absolute("/legal/privacy/"), title, description))
	if err != nil {
		return nil, err
	}
	body, err := r.RenderBody("privacy", r.proseBase(site))
	if err != nil {
		return nil, err
	}
	return &Page{
		Title:         title,
		Description:   description,
		CanonicalPath: "/legal/privacy/",
		Active:        "/legal/privacy",
		Body:          body,
		JSONLD:        jsonld,
	}, nil
}

// TermsPage renders /legal/terms.
func (r *Renderer) TermsPage() (*Page, error) {
	site, err := r.Site()
	if err != nil {
		return nil, err
	}
	title := "Terms of Service - " + SiteName
	description := SiteName + " terms of service: a free, unauthenticated site of pre-computed League of Legends statistics, " +
		"provided as is, with no warranty of accuracy, and not affiliated with or endorsed by Riot Games."
	jsonld, err := JSONLDNode(webPageNode(r.absolute("/legal/terms/"), title, description))
	if err != nil {
		return nil, err
	}
	body, err := r.RenderBody("terms", r.proseBase(site))
	if err != nil {
		return nil, err
	}
	return &Page{
		Title:         title,
		Description:   description,
		CanonicalPath: "/legal/terms/",
		Active:        "/legal/terms",
		Body:          body,
		JSONLD:        jsonld,
	}, nil
}
