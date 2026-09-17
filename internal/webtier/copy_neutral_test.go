package webtier

import (
	"strings"
	"testing"
)

// Plan §7.5: the copy has to describe the product, not the machinery that
// produced the page. The dynamic tier is the one that has to obey it, because
// it re-derives every page from a snapshot on every request - a sentence about
// what happened "at build time" is false the moment the snapshot changes
// underneath it, even though the same sentence was true of the static tier.
//
// The sweep below is deliberately mechanical: it is the plan's own acceptance
// phrase list, run over every route this tier serves, as it is served, in every
// data posture the tier can be in. A route left out of the sweep is a route
// where the claim can come back unnoticed, and a posture left out is the same
// hole per posture - which is how the "... as static pages" heading on /about
// and the "rendered at build time" sentences in prose.go survived the first
// edits to this row, all of which landed on the other tier's copy.

// mechanismPhrases is plan §7.5's list, plus the other ways this repository
// spells the same claim, plus SSG/SSR written out.
var mechanismPhrases = []string{
	"static page",
	"static pages",
	"static content",
	"pre-rendered",
	"prerendered",
	"statically generated",
	"static site",
	"server-rendered",
	"server rendered",
	"build pipeline",
	"ssg",
	"ssr",
	"at build time",
	"rendered at build time",
}

// copyRoute is one served URL. The name is what a failure reports, so it has to
// identify the page without the path.
type copyRoute struct {
	name string
	path string
}

// proseRoutes are the pages made of prose. They are served in every posture and
// their sentences differ per posture, so they are swept in all of them.
var proseRoutes = []copyRoute{
	{"home", "/"},
	{"about", "/about"},
	{"disclaimer", "/disclaimer"},
	{"legal-terms", "/legal/terms"},
	{"legal-privacy", "/legal/privacy"},
}

// dataRoutes are the pages made of numbers. They carry prose.go's
// posture-dependent sentences too, but only a posture with a snapshot can serve
// them, so they are swept in the demo and live postures.
var dataRoutes = []copyRoute{
	{"tier-list-top", "/tier-list/top"},
	{"tier-list-mid", "/tier-list/mid"},
	{"matchups-top", "/matchups/top"},
	{"matchups-mid", "/matchups/mid"},
	{"champion-ahri", "/champions/ahri"},
	{"champion-ahri-mid", "/champions/ahri/mid"},
	{"patch-tier-list-top", "/patch/16.18/tier-list/top"},
}

// TestOwnedPagesNameNoRenderingMechanism sweeps every route in every posture and
// fails on the mechanism phrase, with the sentence it was found in.
func TestOwnedPagesNameNoRenderingMechanism(t *testing.T) {
	t.Parallel()

	liveRoot, _, _, _, _, _, _ := republishedSnapshot(t)

	postures := []struct {
		name     string
		state    string
		opts     Options
		routes   []copyRoute
		mustShow []string
	}{
		{
			name:  "demo",
			state: `data-state="demo"`,
			opts: Options{
				FixturesDir:  fixtureDir(),
				FixturesMode: FixturesOnly,
				DataDir:      fixtureDataDir(),
			},
			routes: append(append([]copyRoute{}, proseRoutes...), dataRoutes...),
			mustShow: []string{
				"<h1>Ranked statistics, every rate with its sample size</h1>",
				"This site publishes per-patch tier lists and champion matchup tables.",
			},
		},
		{
			name:  "live",
			state: `data-state="live"`,
			opts: Options{
				AggRoot:      liveRoot,
				FixturesMode: FixturesOff,
				DataDir:      fixtureDataDir(),
			},
			routes: append(append([]copyRoute{}, proseRoutes...), dataRoutes...),
			mustShow: []string{
				"<h1>Ranked statistics, every rate with its sample size</h1>",
				"This site aggregates League of Legends ranked matches into per-patch tier lists and champion matchup tables.",
			},
		},
		{
			name: "no-data",
			opts: Options{
				AggRoot:      t.TempDir(),
				FixturesMode: FixturesOff,
				DataDir:      fixtureDataDir(),
			},
			routes:   proseRoutes,
			mustShow: []string{"No aggregate snapshot has been published yet"},
		},
	}

	for _, posture := range postures {
		t.Run(posture.name, func(t *testing.T) {
			t.Parallel()
			_, live := newTestServer(t, posture.opts)

			for _, route := range posture.routes {
				res := get(t, live, route.path)
				if res.status != 200 {
					t.Errorf("GET %s in the %s posture -> %d, want 200", route.path, posture.name, res.status)
					continue
				}
				if route.name == "home" && posture.state != "" && !strings.Contains(res.text(), posture.state) {
					t.Fatalf("/ is not the %s posture: %s", posture.name, excerpt(res.text(), "data-state="))
				}
				sweepMechanismPhrases(t, posture.name+" "+route.path, res.text())
			}

			home := get(t, live, "/")
			for _, want := range posture.mustShow {
				if !strings.Contains(home.text(), want) {
					t.Errorf("the home page in the %s posture does not carry %q: %s",
						posture.name, want, excerpt(home.text(), "<h1"))
				}
			}
		})
	}
}

// sweepMechanismPhrases fails on every mechanism phrase the served document
// carries, with the surrounding text so the failure says which sentence it was.
func sweepMechanismPhrases(t *testing.T, path, document string) {
	t.Helper()
	lowered := strings.ToLower(document)
	for _, phrase := range mechanismPhrases {
		if !strings.Contains(lowered, phrase) {
			continue
		}
		t.Errorf("%s carries the mechanism phrase %q: %s", path, phrase, excerpt(document, phrase))
	}
}
