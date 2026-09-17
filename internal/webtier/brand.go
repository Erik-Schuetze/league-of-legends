package webtier

import (
	"fmt"
	"net/url"
)

// The compliance surface, ported verbatim from web/src/lib/legal.ts.
//
// These strings are read from the published reference build rather than
// paraphrased: the non-endorsement notice, the trademark sentence and the
// verified-site note appear on every page, and a second weaker copy of any of
// them would be exactly the drift web/src/lib/legal.ts exists to prevent. They
// are constants, not templates, so nothing on the request path can alter them.

const (
	SiteName = "LoL Stats"

	TrademarkText = "League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc."

	NonEndorsementText = "This project is not endorsed by Riot Games and does not reflect the views or opinions of Riot Games or anyone officially involved in producing or managing Riot Games properties. Riot Games and all associated properties are trademarks or registered trademarks of Riot Games, Inc."

	NotAffiliatedText = "This site is not affiliated with Riot Games, Inc., is not authorised, sponsored or approved by Riot Games, and is not an official source of League of Legends statistics."

	FreeTierText = "Every page on this site is free and ungated: there is no account, no login, no paywall and no rate-limited teaser, and none is planned."

	NoRatingText = "This site does not compute, store or display an MMR, ELO or any other skill rating, and does not offer a calculator for one."

	NoBrokerText = "This site publishes derived aggregate statistics only. It does not resell Riot data, does not serve the raw Riot API responses behind its aggregates, and publishes no per-player record, account, summoner name or match history."

	OperatorName     = "Erik Schuetze"
	OperatorIdentity = OperatorName + ", a private individual resident in the European Union, who operates this site as a non-commercial hobby project."

	// ContactEmailEnv names the variable that overrides the operator's mailbox.
	ContactEmailEnv      = "LOLSTATS_CONTACT_EMAIL"
	OperatorContactEmail = "lolstats@erik-schuetze.dev"

	// RiotTokenEnv names the variable carrying Riot's site-verification token.
	// While it is unset, /riot.txt is not published and the site says so, which
	// is what the reference build does.
	RiotTokenEnv = "LOLSTATS_RIOT_VERIFICATION_TOKEN" // #nosec G101 -- an environment variable name, not a credential value

	EffectiveDate = "2026-09-17"
	// VersionLine is the frozen "Effective ... Last updated ..." sentence.
	VersionLine = "Effective 17 September 2026. Last updated 17 September 2026."

	// riotVerificationNote is the paragraph rendered wherever the site explains
	// the verified-site requirement it does not yet meet.
	riotVerificationNote = "The verified-site requirement is not met yet: this build was not given Riot's site-verification token, so %s is not published and Riot has not verified this site. The token is issued to the domain owner, not to this repository, so the requirement is met only by a build that is given it."
)

// RiotVerificationConfigured reports whether a token was supplied. A build that
// has one publishes /riot.txt and drops the "not met yet" wording.
func RiotVerificationConfigured() bool { return envValue(RiotTokenEnv) != "" }

// RiotVerificationNote is the verified-site sentence for this build.
func RiotVerificationNote(siteURL string) string {
	return fmt.Sprintf(riotVerificationNote, siteURL+"/riot.txt")
}

// ContactEmail is the mailbox the compliance pages publish.
func ContactEmail() string {
	if v := envValue(ContactEmailEnv); v != "" {
		return v
	}
	return OperatorContactEmail
}

// ContactRoute is the frozen "where do I write" sentence.
func ContactRoute() string {
	return "Questions about these pages, about the data, or about your rights under the GDPR go to " +
		ContactEmail() + ". That mailbox is the contact route rather than a postal address, because the operator is a private individual."
}

// SiteHost is the host of the site URL, used by the code samples on the legal pages.
func SiteHost(siteURL string) string {
	if u, err := url.Parse(siteURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "this domain"
}
