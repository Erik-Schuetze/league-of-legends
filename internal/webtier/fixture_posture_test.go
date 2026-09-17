package webtier

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFixturesTreeCannotBeServedAsCrawledData is the structural half of the
// deployed-posture guard. deploy_posture_test.go holds the committed value;
// this holds the code path underneath it, so the two postures cannot be
// confused by an artifact rather than by a setting.
//
// The checked-in demo tree is labelled preview because of where its bytes came
// from, not because of what a manifest inside that directory says. A tree in the
// fixtures directory that declares source "riot-match-v5" is therefore refused
// outright - loudly, as an artifact fault - rather than rendered with a live
// banner. The alternative, trusting the declaration, would mean that copying a
// crawled snapshot into fixtures/site publishes it: the exact substitution this
// project has already shipped once, arrived at by accident instead of by design.
func TestFixturesTreeCannotBeServedAsCrawledData(t *testing.T) {
	t.Parallel()

	root := copyFixtureTree(t)
	manifestPath := filepath.Join(root, "v1", "manifest.json")
	manifest := readJSONDocument(t, manifestPath)
	manifest["source"] = string("riot-match-v5")
	writeJSONDocument(t, manifestPath, manifest)

	_, live := newTestServer(t, Options{
		FixturesDir:  root,
		FixturesMode: FixturesOnly,
		DataDir:      fixtureDataDir(),
	})
	resp := get(t, live, "/tier-list/mid")
	if resp.status != 503 {
		t.Fatalf("GET /tier-list/mid from a fixtures tree declaring crawled data = %d, want 503", resp.status)
	}
	body := resp.text()
	if strings.Contains(body, `data-state="live"`) {
		t.Errorf("the page is labelled live while the bytes came from the fixtures directory: %s", body[:min(len(body), 400)])
	}
	if !strings.Contains(body, "data-state") {
		t.Errorf("the refusal has no labelled state: %s", body[:min(len(body), 400)])
	}
	if !strings.Contains(body, "checked-in demo tree declares source") {
		t.Errorf("the refusal does not say what is wrong with the artifact:\n%s", body)
	}
	if got := resp.header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want an HTML error page a visitor can read", got)
	}
}

// TestPostureWordsAreDistinctPerState holds the vocabulary /robots.txt and the
// banners are written in to one function per posture. Three states exist - the
// sample, a published snapshot, and a tier with nothing to serve - and each has
// to be nameable in prose that a reader can tell apart, because the failure this
// guards against is not a missing label but two postures wearing the same one.
func TestPostureWordsAreDistinctPerState(t *testing.T) {
	t.Parallel()

	seen := map[string]DataState{}
	for _, posture := range []struct {
		state DataState
		want  string
	}{
		{StateDemo, "preview"},
		{StateLive, "live"},
		{StateNoData, "none"},
	} {
		site := &Site{state: posture.state}
		if got := site.PostureWord(); got != posture.want {
			t.Errorf("data-state %q is described as %q, want %q", posture.state, got, posture.want)
		}
		if other, ok := seen[posture.want]; ok {
			t.Errorf("data-state %q and %q share the description %q", other, posture.state, posture.want)
		}
		seen[posture.want] = posture.state
	}
}

// TestServedSampleIsRecognisedAndLabelledPreview checks the arithmetic-free
// half of the honesty requirement on the checked-in tree: the tier recognises
// the source its manifest declares, and labels the snapshot by where the bytes
// came from rather than by that word.
func TestServedSampleIsRecognisedAndLabelledPreview(t *testing.T) {
	t.Parallel()

	renderer, err := NewRenderer(NewLoader(Options{
		FixturesDir:  fixtureDir(),
		FixturesMode: FixturesOnly,
		DataDir:      fixtureDataDir(),
	}), DefaultSiteURL)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	site, err := renderer.Site()
	if err != nil {
		t.Fatalf("Site: %v", err)
	}
	if !site.sourceRecognised {
		t.Fatalf("the checked-in tree declares source %q, which this tier does not recognise", site.Source())
	}
	if site.State() != StateDemo {
		t.Errorf("the demo tree renders data-state=%q, want %q", site.State(), StateDemo)
	}
	if site.PostureWord() != "preview" {
		t.Errorf("the demo tree is described as %q, want %q", site.PostureWord(), "preview")
	}
}
