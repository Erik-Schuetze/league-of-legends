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
// The sweep below is deliberately mechanical. It is the plan's own acceptance
// phrase list, run over the two documents this lane owns, as they are served,
// in every data posture the tier can be in. A posture left out of the sweep is
// a posture where the claim can come back unnoticed - which is how the
// "published as static pages" heading survived three edits to the branch that
// is not the one the edge serves.

// mechanismPhrases is plan §7.5's list, plus the two ways this repository
// spells the same claim in the other direction (the tier renders the page, it
// does not read a pre-built artifact) and SSG/SSR written out.
var mechanismPhrases = []string{
	"static page",
	"static pages",
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

// TestOwnedPagesNameNoRenderingMechanism sweeps / and /legal/privacy in the
// demo, live and no-data postures.
func TestOwnedPagesNameNoRenderingMechanism(t *testing.T) {
	t.Parallel()

	liveRoot, _, _, _, _, _, _ := republishedSnapshot(t)

	postures := []struct {
		name   string
		state  string
		branch string
		opts   Options
	}{
		{
			name:   "demo",
			state:  `data-state="demo"`,
			branch: "This site publishes per-patch tier lists and champion matchup tables.",
			opts: Options{
				FixturesDir:  fixtureDir(),
				FixturesMode: FixturesOnly,
				DataDir:      fixtureDataDir(),
			},
		},
		{
			name:   "live",
			state:  `data-state="live"`,
			branch: "This site aggregates League of Legends ranked matches into per-patch tier lists and champion matchup tables.",
			opts: Options{
				AggRoot:      liveRoot,
				FixturesMode: FixturesOff,
				DataDir:      fixtureDataDir(),
			},
		},
	}

	for _, posture := range postures {
		t.Run(posture.name, func(t *testing.T) {
			t.Parallel()
			_, live := newTestServer(t, posture.opts)

			home := get(t, live, "/")
			if home.status != 200 {
				t.Fatalf("GET / -> %d, want 200", home.status)
			}
			if !strings.Contains(home.text(), posture.state) {
				t.Fatalf("/ is not the %s posture: %s", posture.name, excerpt(home.text(), "data-state="))
			}
			if !strings.Contains(home.text(), "<h1>Ranked statistics, every rate with its sample size</h1>") {
				t.Errorf("/ does not carry the architecture-neutral heading: %s", excerpt(home.text(), "<h1"))
			}
			if strings.Contains(home.text(), "published as static pages") {
				t.Errorf("/ still carries the claim this row exists to remove")
			}
			if !strings.Contains(home.text(), posture.branch) {
				t.Errorf("/ does not carry the %s posture's intro: %s", posture.name, excerpt(home.text(), "<h1"))
			}
			sweepMechanismPhrases(t, "/", home.text())

			privacy := get(t, live, "/legal/privacy")
			if privacy.status != 200 {
				t.Fatalf("GET /legal/privacy -> %d, want 200", privacy.status)
			}
			if strings.Contains(privacy.text(), "the site serves static pages") {
				t.Errorf("/legal/privacy still carries the claim this row exists to remove")
			}
			sweepMechanismPhrases(t, "/legal/privacy", privacy.text())
		})
	}

	t.Run("no-data", func(t *testing.T) {
		t.Parallel()
		_, live := newTestServer(t, Options{
			AggRoot:      t.TempDir(),
			FixturesMode: FixturesOff,
			DataDir:      fixtureDataDir(),
		})
		home := get(t, live, "/")
		if home.status != 200 {
			t.Fatalf("GET / with no snapshot anywhere -> %d, want 200", home.status)
		}
		if !strings.Contains(home.text(), "No aggregate snapshot has been published yet") {
			t.Fatalf("/ is not the no-data posture: %s", excerpt(home.text(), "No aggregate snapshot"))
		}
		sweepMechanismPhrases(t, "/", home.text())
	})
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
