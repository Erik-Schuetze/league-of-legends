package webtier

import (
	"fmt"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Every state-conditional sentence the prose pages carry. They live here rather
// than in the templates because the demo and live wording differ in ways that are
// compliance-relevant: the pages must not borrow live wording for a preview
// build, and this is the one place that decision is made.

const (
	demoSource = string(aggmodel.SourceDemo)
	liveSource = string(aggmodel.SourceRiotMatchV5)
)

// declaredSourceClause is the clause the demo and no-data states quote in front
// of statements about the numbers, so the reader is told what the manifest
// actually declared rather than what the page wishes it had declared.
func declaredSourceClause(site *Site) string {
	if site.NoData() {
		return "this build read no aggregate manifest at all"
	}
	if site.SourceRecognised() {
		return fmt.Sprintf("the aggregate manifest this build read declares its source as %q", site.Source())
	}
	if site.Source() == "" {
		return "the aggregate manifest this build read declares no source at all"
	}
	return fmt.Sprintf("the aggregate manifest this build read declares the unrecognised source %q, so its numbers are unverified", site.Source())
}

// dataSourceSentence is the "what this build publishes" paragraph.
func dataSourceSentence(site *Site) string {
	switch {
	case site.Live():
		return "The statistics are this project's own aggregates of ranked solo-queue match records fetched from Riot's MATCH-V5 API under a registered Riot API key. They measure a sample of that data, and they are not Riot's own figures."
	case site.Demo():
		return fmt.Sprintf("This build publishes no Riot match data: %s, so every number it shows is illustrative preview data generated to exercise the layout. Nothing on this build is a measurement of a real game.", declaredSourceClause(site))
	default:
		return "This build publishes no Riot match data because no aggregate snapshot exists yet. The statistics routes render an explicit empty state rather than numbers."
	}
}

// stateDescription is the short state line under the source sentence.
func stateDescription(site *Site) string {
	switch {
	case site.Live():
		return "This build reads a published snapshot of ingested Riot match data."
	case site.Demo():
		if site.SourceRecognised() {
			return PreviewText
		}
		return UnverifiedPreviewText
	default:
		return "No aggregate snapshot has been published for this site yet."
	}
}

// aboutIntroTail is the state-conditional opening claim of /about.
func aboutIntroTail(site *Site) string {
	switch {
	case site.Live():
		return "The pages read aggregate artifacts that a separate ingestion and aggregation pipeline produces from Riot's MATCH-V5 match feed."
	case site.Demo():
		return "The pages read aggregate artifacts in the same shape a real snapshot has, but these artifacts are illustrative " + demoSource + " data: no ingestion and aggregation pipeline has run for them, and nothing on this build is a measurement of a real game."
	default:
		return "No aggregate snapshot exists yet, so this build read none and serves the layout of those pages in its empty state: the pages show no numbers from any game, and the aggregate artifacts they will one day read are the kind an ingestion and aggregation pipeline writes."
	}
}

// pipelineLeadIn introduces the five pipeline steps.
func pipelineLeadIn(site *Site) string {
	if site.Live() {
		return "Five steps, in order, produced the snapshot this build reads. The pipeline is deliberately boring, because every interesting shortcut here would be a way to publish a wrong number."
	}
	return fmt.Sprintf("Five steps, in order. No run of this pipeline has produced anything for this build - %s - so this is the method the numbers will come from, not a description of an ingestion that has happened. The pipeline is deliberately boring, because every interesting shortcut here would be a way to publish a wrong number.", declaredSourceClause(site))
}

// pipelineStatusSentence says which of the five steps has actually run.
func pipelineStatusSentence(site *Site) string {
	switch {
	case site.Live():
		return "Every step above ran for the snapshot this build reads, and the build record printed on this page is its receipt."
	case site.Demo():
		return fmt.Sprintf("None of the five steps has run for this build: %s, so these pages read illustrative fixtures rather than pipeline output.", declaredSourceClause(site))
	default:
		return "None of the five steps has run for this build, so there is no snapshot and no number anywhere on this site; the steps above are the method that will produce the first one."
	}
}

// matchAPIName names the API the pipeline is described as fetching from. The demo
// and no-data states do not name MATCH-V5, because naming it beside "how the
// numbers are produced" reads as a claim about this build's provenance.
func matchAPIName(site *Site) string {
	if site.Live() {
		return "Riot's MATCH-V5 API"
	}
	return "Riot's ranked match API"
}

// credentialSentence is the second of the two open compliance items.
func credentialSentence(site *Site) string {
	if site.Live() {
		return "this build rendered a snapshot whose manifest declares it was crawled through Riot's MATCH-V5 API under a registered key, so the key is in the ingestion path; the key itself is never part of this build and nothing here carries anything Riot has reviewed or approved."
	}
	return "this build was produced without a Riot production API key, so no match data has been ingested or published, and the site carries nothing that Riot has reviewed or approved."
}

// rankAttributionLead is the snapshot-rank caveat, worded for the state.
func rankAttributionLead(site *Site) string {
	if site.Live() {
		return "Rank attribution is a snapshot, not a per-match fact. Riot's match feed does not tell an aggregator the rank a player held in each match, so a bracket is built from players discovered at that rank in a recent window."
	}
	return "Rank attribution can only ever be a snapshot, not a per-match fact. A match record does not carry the rank a player held in that match, so a bracket has to be built from players discovered at that rank in a recent window."
}

// queueCoverage describes how the pipeline treats queues.
func queueCoverage(site *Site) string {
	if site.Live() {
		return "This site currently aggregates ranked solo/duo (queue 420)"
	}
	return "The pipeline covers ranked solo/duo (queue 420) and nothing else"
}

// sourceBullet is the "Source." bullet of the computation section. queue is the
// snapshot's queue when there is one and falls back to ranked solo/duo.
func sourceBullet(site *Site, queue int) string {
	if site.Live() {
		return fmt.Sprintf("Riot's MATCH-V5 match records for ranked solo queue (queue %d), fetched under a registered key and archived append-only before aggregation. Raw archives are never rewritten.", queue)
	}
	held := "this build holds no match records, so nothing on it is computed from a real game."
	if site.NoData() {
		held = "no snapshot exists, so there are no source records at all."
	}
	return fmt.Sprintf("Nothing yet: %s A real snapshot's source is Riot's ranked match records for ranked solo queue (queue %d), which the fetch step copies into a raw archive that is never rewritten in place.", held, queue)
}

// sourceAnnotation is the parenthetical after the declared source.
func sourceAnnotation(site *Site) string {
	if site.Live() {
		return fmt.Sprintf(" (%s: ingested match data)", liveSource)
	}
	if site.Demo() {
		if site.SourceRecognised() {
			return fmt.Sprintf(" (%s: illustrative fixtures, not match statistics)", demoSource)
		}
		return " (unrecognised, so this build treats the numbers as unverified)"
	}
	return ""
}

// productionHeading, aboutComputedHeading and computedHeading say "is" only
// when it is.
//
// The two computed headings are not the same sentence: /about's sits above a
// description of the arithmetic the whole site uses, while a statistics page's
// sits above the arithmetic of the page the reader is looking at. The reference
// spells them differently and the port keeps both spellings.
func productionHeading(site *Site) string {
	if site.Live() {
		return "How the data is produced"
	}
	return "How the data will be produced"
}

func aboutComputedHeading(site *Site) string {
	if site.Live() {
		return "How the numbers are computed"
	}
	return "How the numbers will be computed"
}

// computedHeading is ChampionBody's heading, from legal.ts.
func computedHeading(site *Site) string {
	if site.NoData() {
		return "How these pages are produced"
	}
	return "How this page is computed"
}

// computedFromSentence is the arithmetic paragraph under computedHeading. In the
// no-data state there is nothing to describe the method of, so it describes the
// promise instead.
func computedFromSentence(site *Site, minCellN int, hasMinCellN bool) string {
	threshold := "the publication threshold"
	if hasMinCellN {
		threshold = "n = " + Integer(float64(minCellN))
	}
	method := "Win rate is wins divided by games in this cell; the 95% interval is reported so that a small sample cannot look " +
		"like a precise one, and cells below " + threshold + " games are withheld rather than published."

	switch {
	case site.Live():
		return "The numbers come from Riot's MATCH-V5 match feed, aggregated per patch and per queue, and this page reads " +
			"the published aggregate artifact for that patch and queue. " + method
	case site.Demo():
		return "No Riot match data has been ingested for this build: " + declaredSourceClause(site) + ", so the numbers on this " +
			"page are a preview of the layout rather than a measurement of anything. The arithmetic below is the real " +
			"arithmetic, applied to illustrative figures. " + method
	default:
		return "This build publishes no numbers at all, so there is nothing on this page measured from match data. When a " +
			"snapshot exists, win rate is wins divided by games in the cell, the 95% interval is reported so that a small " +
			"sample cannot look like a precise one, and cells below the publication threshold are withheld rather than published."
	}
}

// patchesInBuild lists the patches the loaded manifest covers.
func patchesInBuild(site *Site) string {
	patches := site.Patches()
	if len(patches) == 0 {
		return "none published"
	}
	return strings.Join(patches, ", ")
}

// previewNumbersSentence is lib/legal.ts's previewNumbersSentence: the sentence
// that says what the numbers on a statistics page actually are. It is empty in
// the live state, so the live wording is the live wording and nothing is
// weakened for a preview.
func previewNumbersSentence(site *Site) string {
	switch {
	case site.NoData():
		return "No aggregate snapshot has been published yet, so this build has no rates to show."
	case site.Live():
		return ""
	default:
		return "This build is a labelled preview: the rates and sample sizes below are illustrative values " +
			"generated to exercise the layout, not measurements of real games."
	}
}
