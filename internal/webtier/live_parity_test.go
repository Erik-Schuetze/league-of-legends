package webtier

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The parity test above proves the renderer agrees with the published build when
// it renders from fixtures. This one proves the tier that is actually deployed
// agrees with it, over the wire: same routes, same reference files, same
// normalisation. The two differences the live tier is allowed to have, and the
// only ones, are the ones HTTP imposes - it is asked for a path rather than a
// page constructor, and it may answer any request with the same bytes.
//
// It is opt-in through LOLSTATS_PARITY_URL because it needs a running tier, and
// because a request to a host that is not the tier would pass or fail for
// reasons that have nothing to do with this port. Point it at a port-forward of
// the tier's own Service.

// liveRoutes maps a request path to the file the reference build published for
// it. The patch route names the version the published build was rendered from;
// a tier serving a newer snapshot is a difference the diff below will show
// rather than hide. `interactive` marks the routes whose served page is the
// reference page plus the no-JS filter bar, which is the only difference the
// deployed tier is allowed to have from the build it replaces.
func liveRoutes(t *testing.T) []parityCase {
	t.Helper()
	return []parityCase{
		{name: "home", dist: "index.html"},
		{name: "about", dist: "about/index.html"},
		{name: "disclaimer", dist: "disclaimer/index.html"},
		{name: "tier-list-mid", dist: "tier-list/mid/index.html", interactive: true},
		{name: "patch-tier-list-mid", dist: "patch/16.18/tier-list/mid/index.html", interactive: true},
		{name: "matchups-mid", dist: "matchups/mid/index.html"},
		{name: "champions-ahri", dist: "champions/ahri/index.html"},
		{name: "champions-ahri-mid", dist: "champions/ahri/mid/index.html"},
		{name: "champions-ahri-top", dist: "champions/ahri/top/index.html"},
		{name: "legal-terms", dist: "legal/terms/index.html"},
		{name: "legal-privacy", dist: "legal/privacy/index.html"},
	}
}

// livePath is the request path for a reference file: a directory index is its
// directory, a file is itself.
func livePath(dist string) string {
	path := strings.TrimSuffix(dist, "index.html")
	if path == "" {
		return "/"
	}
	return "/" + strings.TrimSuffix(path, "/")
}

func TestLiveRenderParity(t *testing.T) {
	base := os.Getenv("LOLSTATS_PARITY_URL")
	if base == "" {
		t.Skip("LOLSTATS_PARITY_URL is not set: port-forward the tier's Service and set it to enable live parity checks")
	}
	dir := referenceDir(t)
	base = strings.TrimSuffix(base, "/")
	client := &http.Client{}

	fetch := func(t *testing.T, path string) ([]byte, int) {
		t.Helper()
		// #nosec G107 G704 -- the URL is the operator's own tier, passed in
		// through an environment variable for this test only.
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return body, resp.StatusCode
	}

	check := func(t *testing.T, path string, want []byte, normalise bool, interactive bool) {
		t.Helper()
		got, status := fetch(t, path)
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, status)
		}
		served := string(got)
		if interactive {
			stripped, found, bar := removeNoJSBar(served)
			if !found {
				t.Fatalf("GET %s: no filter bar, so the served page is not sortable, filterable or paginated without JavaScript", path)
			}
			if !strings.Contains(bar, `action="`+path+`"`) {
				t.Errorf("GET %s: filter bar does not post back to %s:\n%s", path, path, bar)
			}
			if _, refFound, _ := removeNoJSBar(string(want)); refFound {
				t.Fatalf("the reference for %s already contains a filter bar; the allowance would hide a real difference", path)
			}
			served = stripped
		}
		if normalise {
			if equalIgnoringEscaping(want, []byte(served)) {
				return
			}
			t.Errorf("live mismatch for %s (%d reference bytes, %d served bytes)\n%s",
				path, len(want), len(served), firstDifference(normaliseEscaping(string(want)), normaliseEscaping(served)))
			return
		}
		if !bytes.Equal(want, []byte(served)) {
			t.Errorf("live mismatch for %s (%d reference bytes, %d served bytes)\n%s",
				path, len(want), len(served), firstDifference(string(want), served))
		}
	}

	for _, testCase := range liveRoutes(t) {
		t.Run(testCase.name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, testCase.dist))
			if err != nil {
				t.Fatalf("read reference %s: %v", testCase.dist, err)
			}
			check(t, livePath(testCase.dist), want, true, testCase.interactive)
		})
	}

	for _, feed := range []string{"sitemap.xml", "robots.txt"} {
		t.Run(feed, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, feed))
			if err != nil {
				t.Fatalf("read reference %s: %v", feed, err)
			}
			check(t, "/"+feed, want, false, false)
		})
	}
}
