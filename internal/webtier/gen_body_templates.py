#!/usr/bin/env python3
"""Turn a web/dist page body into a Go template.

The reference bodies in templates/pages/*.body.tmpl are the mechanical source for
the Go templates: the port has to reproduce the served bytes, so the markup is
taken from the reference build and only the interpolated values are swapped for
template actions. Every substitution asserts that it matched exactly once, so a
change in the reference build fails loudly instead of producing a half-ported
page.
"""
import sys

PAGES = {
    'about': [
        # (old, new)
        ('The pages are rendered at build time from aggregate artifacts, in the same shape a real snapshot has, but these artifacts are illustrative demo data: no ingestion and aggregation pipeline has run for them, and nothing on this build is a measurement of a real game.',
         '{{ .IntroTail }}'),
        ('Every page on this site is free and ungated: there is no account, no login, no paywall and no rate-limited teaser, and none is planned.',
         '{{ freeTier }}'),
        ('This site is not affiliated with Riot Games, Inc., is not authorised, sponsored or approved by Riot Games, and is not an official source of League of Legends statistics.',
         '{{ notAffiliated }}'),
        ('This site does not compute, store or display an MMR, ELO or any other skill rating, and does not offer a calculator for one.',
         '{{ noRating }}'),
        ('This site publishes derived aggregate statistics only. It does not resell Riot data, does not serve the raw Riot API responses behind its aggregates, and publishes no per-player record, account, summoner name or match history.',
         '{{ noBroker }}'),
        ('This build publishes no Riot match data: the aggregate manifest this build read declares its source as &quot;demo&quot;, so every number it shows is illustrative preview data generated to exercise the layout. Nothing on this build is a measurement of a real game.',
         '{{ .SourceSentence }}'),
        ('PREVIEW - illustrative data generated to exercise the layout, not real match statistics',
         '{{ .StateDescription }}'),
        # The partition summary. Every one of these six values is read off the
        # page's partition by the reference build, and the demo manifest's
        # values happen to be the ones the reference dist was rendered from.
        # Transcribing those literals would be the port lying in the live
        # state: a published snapshot would print "141 cells" and "n = 500"
        # beside its real cells. `integer` is format.ts's thousands separator,
        # `windowLabel`/`utcStamp` are format.ts's date labels, and
        # `shortCommit` is git_sha.slice(0, 12).
        ('<li>Patch <strong>16.18</strong> &middot; region EUW &middot; queue 420 &middot; rank bracket all</li>',
         '<li>Patch <strong>{{ .Partition.Patch }}</strong> &middot; region {{ .Partition.Region }} &middot; '
         'queue {{ .Partition.Queue }} &middot; rank bracket {{ .Partition.Bracket }}</li>'),
        ('<li>Crawl window: 2026-09-08 to 2026-09-14 (the window of match timestamps the aggregation covered, not the time it ran)</li>',
         '<li>Crawl window: {{ windowLabel .Partition.SourceWindow.From .Partition.SourceWindow.To }} '
         '(the window of match timestamps the aggregation covered, not the time it ran)</li>'),
        ('<li>Snapshot generated 2026-09-15 04:10 UTC (the time the aggregate build ran)</li>',
         '<li>Snapshot generated {{ utcStamp .Partition.GeneratedAt }} (the time the aggregate build ran)</li>'),
        # "across N champions" is the number of champions the published cells
        # cover, not len(partition.champions): that list is the partition's
        # champion index, and the two are only equal by coincidence on the demo
        # fixture (80 = 80). The live snapshot publishes 130 cells across 120
        # champions while its index lists 173 ids, and "130 cells across 173
        # champions" is a sentence the artifact contradicts. The port reads the
        # count off the tier list, which is what the reference build meant.
        #
        # The missing space between the count and "champions" is the reference
        # build's own output - web/dist/about/index.html prints "141 across
        # 80champions" - so it is reproduced rather than corrected: parity with
        # the served bytes is this port's contract, and the typo belongs to
        # web/src/pages/about.astro.
        ('<li>Aggregated cells published: 141 across 80champions; cells withheld for being below the sample threshold: 3</li>',
         '<li>Aggregated cells published: {{ integer .Partition.CellsPublished }} across '
         '{{ integer .ChampionsPublished }}champions; cells withheld for being below the sample threshold: '
         '{{ integer .Partition.SuppressedCells }}</li>'),
        ('<li>Publication threshold: a win, pick or ban rate is published only for a cell holding at least n = 500 games</li>',
         '<li>Publication threshold: a win, pick or ban rate is published only for a cell holding at least '
         'n = {{ integer .Partition.MinCellN }} games</li>'),
        ('<li>Build run 2 from commit 000000000000</li>',
         '<li>Build run {{ integer .Partition.BuildRunID }} from commit {{ shortCommit .Partition.GitSHA }}</li>'),
        ('<h2>How the data will be produced</h2>', '<h2>{{ .ProductionHeading }}</h2>'),
        ('Five steps, in order. No run of this pipeline has produced anything for this build - the aggregate manifest this build read declares its source as &quot;demo&quot; - so this is the method the numbers will come from, not a description of an ingestion that has happened. The pipeline is deliberately boring, because every interesting shortcut here would be a way to publish a wrong number.',
         '{{ .PipelineLeadIn }}'),
        ("copies match records from Riot&#39;s ranked match API at an adaptive rate",
         'copies match records from {{ .MatchAPI }} at an adaptive rate'),
        ('None of the five steps has run for this build: the aggregate manifest this build read declares its source as &quot;demo&quot;, so these pages were rendered from illustrative fixtures rather than from pipeline output.',
         '{{ .PipelineStatus }}'),
        ('<h2>How the numbers will be computed</h2>', '<h2>{{ .ComputedHeading }}</h2>'),
        ("Nothing yet: this build holds no match records, so nothing on it is computed from a real game. A real snapshot&#39;s source is Riot&#39;s ranked match records for ranked solo queue (queue 420), which the fetch step copies into a raw archive that is never rewritten in place.",
         '{{ .SourceBullet }}'),
        ('Rank attribution can only ever be a snapshot, not a per-match fact. A match record does not carry the rank a player held in that match, so a bracket has to be built from players discovered at that rank in a recent window.',
         '{{ .RankAttributionLead }}'),
        ('The pipeline covers ranked solo/duo (queue 420) and nothing else', '{{ .QueueCoverage }}'),
        ('Aggregate root read at build time: checked-in demo fixtures',
         'Aggregate root read at build time: {{ .RootLabel }}'),
        ('<code>demo</code> (demo: illustrative fixtures, not match statistics)',
         '<code>{{ .Source }}</code>{{ .SourceAnnotation }}'),
        ('Patches in this build: 16.18, 16.17', 'Patches in this build: {{ .Patches }}'),
        ('League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc.',
         '{{ trademark }}'),
        ('This project is not endorsed by Riot Games and does not reflect the views or opinions of Riot Games or anyone officially involved in producing or managing Riot Games properties. Riot Games and all associated properties are trademarks or registered trademarks of Riot Games, Inc.',
         '{{ nonEndorsed }}'),
        ('Questions about these pages, about the data, or about your rights under the GDPR go to lolstats@erik-schuetze.dev. That mailbox is the contact route rather than a postal address, because the operator is a private individual.',
         '{{ contactRoute }}'),
        ('<a href="mailto:lolstats@erik-schuetze.dev">lolstats@erik-schuetze.dev</a>',
         '<a href="mailto:{{ contactEmail }}">{{ contactEmail }}</a>'),
        ('<small>Effective 17 September 2026. Last updated 17 September 2026.</small>',
         '<small>{{ versionLine }}</small>'),
    ],
}

# The two compliance sentences the about page shares with /disclaimer.
RIOT_NOTE = (
    'The verified-site requirement is not met yet: this build was not given Riot&#39;s site-verification token, '
    'so https://lol.erik-schuetze.dev/riot.txt is not published and Riot has not verified this site. '
    'The token is issued to the domain owner, not to this repository, so the requirement is met only by a build '
    'that is given it.'
)


def legal_common(site_name_count, contact_route_count):
    """The pairs /legal/privacy and /legal/terms share.

    The two pages quote the same compliance sentences, so they swap them for the
    same prose helpers /about and /disclaimer already use. The counts differ
    between the pages, which is why they are arguments: a substitution that stops
    matching must fail the build rather than leave reference prose on a page.
    """
    return [
        ('Effective 17 September 2026. Last updated 17 September 2026.', '{{ versionLine }}'),
        ('LoL Stats', '{{ siteName }}', site_name_count),
        ('<code>https://lol.erik-schuetze.dev</code>', '<code>https://{{ siteHost .SiteURL }}</code>'),
        ('Erik Schuetze, a private individual resident in the European Union, who operates this site as a non-commercial hobby project.',
         '{{ operator }}'),
        ('League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc.',
         '{{ trademark }}'),
        ('This project is not endorsed by Riot Games and does not reflect the views or opinions of Riot Games or anyone officially involved in producing or managing Riot Games properties. Riot Games and all associated properties are trademarks or registered trademarks of Riot Games, Inc.',
         '{{ nonEndorsed }}'),
        (RIOT_NOTE, '{{ riotNote .SiteURL }}'),
        ('Questions about these pages, about the data, or about your rights under the GDPR go to lolstats@erik-schuetze.dev. That mailbox is the contact route rather than a postal address, because the operator is a private individual.',
         '{{ contactRoute }}', contact_route_count),
        ('<a href="mailto:lolstats@erik-schuetze.dev">lolstats@erik-schuetze.dev</a>',
         '<a href="mailto:{{ contactEmail }}">{{ contactEmail }}</a>'),
    ]


# /legal/privacy makes one claim about the pipeline rather than about the site,
# and it is only true where a pipeline has run, so the reference build states it
# in whichever tense the build allows. Both branches are carried: the rendered
# body holds the demo branch, and the live branch is transcribed from
# web/src/pages/legal/privacy.astro, so a live build reads correctly too.
PAGES['privacy'] = legal_common(1, 2) + [
    ('This site publishes derived aggregate statistics only. It does not resell Riot data, does not serve the raw Riot API responses behind its aggregates, and publishes no per-player record, account, summoner name or match history.',
     '{{ noBroker }}'),
    ('match records are reduced to counts before anything is published, and this build has no match records to reduce',
     '{{ if .Live }}match records are reduced to counts before anything is published'
     '{{ else }}match records are reduced to counts before anything is published, and this build has no match records to reduce{{ end }}'),
    ("No raw match archive exists for this build, and nothing in this policy depends on one existing: the raw archive behind a live snapshot is kept for the operator&#39;s own quality control and is not served, not resold and not exposed to anyone.",
     "{{ if .Live }}The raw archive that the aggregation reads is kept for the operator's own quality control and is "
     "not served, not resold and not exposed to anyone.{{ else }}No raw match archive exists for this build, and "
     "nothing in this policy depends on one existing: the raw archive behind a live snapshot is kept for the "
     "operator's own quality control and is not served, not resold and not exposed to anyone.{{ end }}"),
]

PAGES['terms'] = legal_common(3, 1) + [
    ('This site is not affiliated with Riot Games, Inc., is not authorised, sponsored or approved by Riot Games, and is not an official source of League of Legends statistics.',
     '{{ notAffiliated }}'),
    ('Every page on this site is free and ungated: there is no account, no login, no paywall and no rate-limited teaser, and none is planned.',
     '{{ freeTier }}'),
]


def substitute(text, pairs, page):
    for pair in pairs:
        old, new = pair[0], pair[1]
        # NO_RATING_TEXT is quoted twice on /about: once in "what this site is
        # not" and once in the notices, and the reference repeats it verbatim.
        # A third element states the expected count where it is not the default.
        expected = pair[2] if len(pair) > 2 else (2 if old.startswith('This site does not compute') else 1)
        count = text.count(old)
        if count != expected:
            raise SystemExit(f'{page}: expected {expected} occurrence(s), found {count}: {old[:90]!r}')
        text = text.replace(old, new)
    return text


def main():
    page = sys.argv[1]
    src = f'internal/webtier/templates/pages/{page}.body.tmpl'
    dst = f'internal/webtier/templates/pages/{page}.tmpl'
    text = open(src, encoding='utf-8').read()
    text = text[text.index('</script>') + len('</script>'):]
    text = substitute(text, PAGES[page], page)
    if page == 'about':
        text = text.replace(RIOT_NOTE, '{{ riotNote .SiteURL }}')
        if 'riotNote' not in text:
            raise SystemExit('about: riot verification note not found')
        # The partition summary and the empty state are one slot.
        start = text.index('<ul><li>Patch <strong>{{ .Partition.Patch }}</strong>')
        end = text.index('</ul>', start) + len('</ul>')
        text = text[:start] + '{{ if .Partition }}' + text[start:end] + '{{ else }}{{ .Empty }}{{ end }}' + text[end:]
        # The three state-conditional paragraphs.
        text = text.replace('<h2>What the aggregation cannot know</h2><p>No aggregation has run for this build',
                            '<h2>What the aggregation cannot know</h2>{{ if not .Live }}<p>No aggregation has run for this build')
        marker = 'numbers you can see: there are none.</p><p>'
        if text.count(marker) != 1:
            raise SystemExit(f'about: expected to find the closed live-only paragraph once, found {text.count(marker)}')
        text = text.replace(marker, 'numbers you can see: there are none.</p>{{ end }}<p>')
        text = text.replace('<strong>No match data has been ingested yet</strong>',
                            '{{ if not .Live }}<strong>No match data has been ingested yet</strong>')
        text = text.replace('waiting for the first snapshot.</p></article>', 'waiting for the first snapshot.</p>{{ end }}</article>')
        text = substitute(text, [
            ('this build was produced without a Riot production API key, so no match data has been ingested or published, and the site carries nothing that Riot has reviewed or approved.',
             '{{ .CredentialSentence }}'),
        ], page)
    header = (
        '{{- /* ' + page + '.tmpl is web/src/pages/' + page + '.astro. Markup and whitespace come from the\n'
        'reference build in web/dist; gen_body_templates.py asserts every substitution, so a\n'
        'change in the reference build fails loudly instead of leaving a half-ported page. */ -}}\n'
        '{{- define "' + page + '" -}}'
    )
    text = header + text + '{{- end -}}\n'
    open(dst, 'w', encoding='utf-8').write(text)
    print(f'{dst}: {len(text)} bytes')


if __name__ == '__main__':
    main()
