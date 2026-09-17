package webtier

import (
	"testing"
)

// The byte-parity tests that used to live beside this helper compared the Go
// tier's HTML against web/dist - the Astro build of the tree this project has
// retired - and were retired with it (docs/contracts.md section 5). The tree
// itself was deleted on 2026-09-18. What is left
// here is the fixture renderer those tests were built on, which the a11y and
// freeze assertions below use as a fixed, network-free corpus.

// newFixtureRenderer builds a renderer over the checked-in fixtures.
func newFixtureRenderer(t *testing.T) *Renderer {
	t.Helper()
	opts := OptionsFromEnv()
	opts.FixturesMode = FixturesOnly
	loader := NewLoader(opts)
	renderer, err := NewRenderer(loader, DefaultSiteURL)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	if _, err := loader.Site(); err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	return renderer
}

// TestRiotTokenIsUnpublishedByDefault pins the honest default: with no token
// configured, /riot.txt does not exist, because publishing an empty file there
// would assert a verification Riot has not performed.
func TestRiotTokenIsUnpublishedByDefault(t *testing.T) {
	renderer := newFixtureRenderer(t)
	if body, found := renderer.RiotToken(); found {
		t.Errorf("riot.txt published without a token: %q", body)
	}
	renderer.SetRiotToken("  abc123  ")
	body, found := renderer.RiotToken()
	if !found {
		t.Fatal("riot.txt not published after a token was set")
	}
	if string(body) != "abc123\n" {
		t.Errorf("riot.txt body = %q, want %q", body, "abc123\n")
	}
}
