package webtier

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRobotsStatesWhatTheSnapshotIs renders /robots.txt for three different
// artifacts - the labelled preview, a republished snapshot and a tier with no
// snapshot at all - and requires the file's description of the data to move
// with them.
//
// The defect this replaces was a literal: the file asserted "# Snapshot state at
// build time: live." whatever the tier was serving, which is true for one
// posture and false for the other. A crawler-facing description of the data that
// disagrees with the page banner describing the same data is the dishonesty this
// project exists not to ship, and it is worse in a machine-readable file,
// because the reader that cannot notice is the one reading it.
func TestRobotsStatesWhatTheSnapshotIs(t *testing.T) {
	t.Parallel()

	t.Run("preview posture", func(t *testing.T) {
		t.Parallel()
		_, live := newTestServer(t, Options{
			FixturesDir:  fixtureDir(),
			FixturesMode: FixturesOnly,
			DataDir:      fixtureDataDir(),
		})
		resp := get(t, live, "/robots.txt")
		if resp.status != 200 {
			t.Fatalf("GET /robots.txt = %d, want 200", resp.status)
		}
		body := resp.text()
		if !strings.Contains(body, "# Snapshot state: preview.") {
			t.Errorf("/robots.txt does not name the preview posture: %s", body)
		}
		if strings.Contains(body, "Snapshot state at build time") {
			t.Errorf("/robots.txt still states its posture as a build-time fact: %s", body)
		}
		// The sentence the HTML banner carries has to appear here too: the same
		// snapshot, described in the same words wherever it is described.
		if !strings.Contains(body, "# "+PreviewText) {
			t.Errorf("/robots.txt omits the preview banner sentence the pages carry:\n%s", body)
		}
		if !strings.Contains(body, `The manifest declares source "demo".`) {
			t.Errorf("/robots.txt does not report the manifest's declared source: %s", body)
		}
		// The indexing statement is the one the sitemap is held to, so amending
		// it here and asserting the sitemap in sitemap_invariant_test.go are the
		// same claim: every page is public, and the ones meant to be indexed are
		// the ones the sitemap lists.
		for _, want := range []string{
			"# Every page here is public. The ones that are meant to be indexed are the ones",
			"# /sitemap.xml lists",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("/robots.txt dropped the indexing statement the pages are checked against, %q: %s", want, body)
			}
		}
		if strings.Contains(body, "Every page here is public and meant to be indexed") {
			t.Errorf("/robots.txt claims every page is meant to be indexed while the sitemap lists only the ones that are: %s", body)
		}
		if strings.Contains(body, "public, static and meant to be indexed") {
			t.Errorf("/robots.txt describes a per-request tier as static: %s", body)
		}
		if !strings.Contains(body, "Sitemap: "+DefaultSiteURL+"/sitemap.xml") {
			t.Errorf("/robots.txt does not point at the canonical origin %s: %s", DefaultSiteURL, body)
		}
	})

	t.Run("published snapshot", func(t *testing.T) {
		t.Parallel()
		root, published, champions, suppressed, floor, run, commit := republishedSnapshot(t)
		_, live := newTestServer(t, Options{
			AggRoot:      root,
			FixturesMode: FixturesOff,
			DataDir:      fixtureDataDir(),
		})
		resp := get(t, live, "/robots.txt")
		if resp.status != 200 {
			t.Fatalf("GET /robots.txt = %d, want 200", resp.status)
		}
		body := resp.text()
		if !strings.Contains(body, "# Snapshot state: live.") {
			t.Errorf("/robots.txt does not name the live posture for a crawled snapshot: %s", body)
		}
		if strings.Contains(body, "state: preview") || strings.Contains(body, PreviewText) {
			t.Errorf("/robots.txt describes a published snapshot as the sample: %s", body)
		}
		if !strings.Contains(body, `The manifest declares source "riot-match-v5".`) {
			t.Errorf("/robots.txt does not report the manifest's declared source: %s", body)
		}
		// These come from the republished artifact: not from the demo tree and
		// not from the template, which is the property under test.
		for _, want := range []string{
			"Source window 2031-01-02 to 2031-01-09",
			"generated 2031-01-09T01:02:03Z",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("/robots.txt does not carry %q from the manifest:\n%s", want, body)
			}
		}
		// If the rewrite had not happened, this subtest would prove nothing.
		if published != 7 || suppressed != 42 || floor != 250 || run != 9 || commit != "abcdef123456" || champions == 0 {
			t.Fatalf("republishedSnapshot produced %d/%d/%d/%d/%s, want 7/42/250/9/abcdef123456", published, suppressed, floor, run, commit)
		}
	})

	t.Run("no snapshot", func(t *testing.T) {
		t.Parallel()
		_, live := newTestServer(t, Options{
			AggRoot:      filepath.Join(t.TempDir(), "absent"),
			FixturesMode: FixturesOff,
			DataDir:      fixtureDataDir(),
		})
		resp := get(t, live, "/robots.txt")
		if resp.status != 200 {
			t.Fatalf("GET /robots.txt without a snapshot = %d, want 200: robots.txt is a prose document about the site and stays reachable when the ladder pages cannot be", resp.status)
		}
		body := resp.text()
		if !strings.Contains(body, "# Snapshot state: none.") {
			t.Errorf("/robots.txt does not say that nothing is published: %s", body)
		}
		if strings.Contains(body, "state: live") || strings.Contains(body, "state: preview") {
			t.Errorf("/robots.txt names a snapshot state it is not serving: %s", body)
		}
		if !strings.Contains(body, "No aggregate snapshot has been published yet") {
			t.Errorf("/robots.txt does not explain the empty state: %s", body)
		}
	})
}

// TestRobotsMatchesTheBannerItDescribes is the cross-check that gives the test
// above its meaning: it renders the page banner and robots.txt from the same
// artifact and requires them to agree about the posture in words a reader could
// compare by eye, rather than by decoding an enum.
func TestRobotsMatchesTheBannerItDescribes(t *testing.T) {
	t.Parallel()

	liveRoot, _, _, _, _, _, _ := republishedSnapshot(t)

	for _, posture := range []struct {
		name  string
		state DataState
		opts  Options
	}{
		{
			name:  "preview",
			state: StateDemo,
			opts: Options{
				FixturesDir:  fixtureDir(),
				FixturesMode: FixturesOnly,
				DataDir:      fixtureDataDir(),
			},
		},
		{
			name:  "live",
			state: StateLive,
			opts: Options{
				AggRoot:      liveRoot,
				FixturesMode: FixturesOff,
				DataDir:      fixtureDataDir(),
			},
		},
	} {
		posture := posture
		t.Run(posture.name, func(t *testing.T) {
			t.Parallel()
			renderer, err := NewRenderer(NewLoader(posture.opts), DefaultSiteURL)
			if err != nil {
				t.Fatalf("NewRenderer: %v", err)
			}
			site, err := renderer.Site()
			if err != nil {
				t.Fatalf("Site: %v", err)
			}
			if site.State() != posture.state {
				t.Fatalf("site state = %q, want %q", site.State(), posture.state)
			}
			robots, err := renderer.Robots()
			if err != nil {
				t.Fatalf("Robots: %v", err)
			}
			body := string(robots)
			if want := "# Snapshot state: " + site.PostureWord() + "."; !strings.Contains(body, want) {
				t.Errorf("/robots.txt does not contain %q:\n%s", want, body)
			}
			banner := site.Banner()
			if banner.PreviewText != "" && !strings.Contains(body, banner.PreviewText) {
				t.Errorf("/robots.txt omits the banner sentence %q the page carries for the same snapshot", banner.PreviewText)
			}
			if banner.PreviewText == "" && strings.Contains(body, "not real match statistics") {
				t.Errorf("/robots.txt calls a published snapshot illustrative data: %s", body)
			}
		})
	}
}
