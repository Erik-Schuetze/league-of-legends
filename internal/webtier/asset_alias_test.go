package webtier

import (
	"net/http"
	"strings"
	"testing"
)

// The asset-continuity tests. They exist because the cutover plan requires a
// mixed state - some route families on this tier, the rest on the tier it
// replaced, for long enough to drill the rollback - and the two tiers hash their
// asset maps independently, so their /_astro paths are disjoint. A page from one
// tier asking the other for /_astro/JsonLd.<its-own-hash>.css would get a 404
// stylesheet and render unstyled, which is why the tier answers an unknown
// content hash with the current asset of the same name and extension.
//
// The pair of tests below is deliberately two-sided. Tolerating a stale hash is
// only half of the behaviour; the other half is that a name this tier does not
// have stays a 404, because "resolve anything" would turn every typo in an asset
// URL into a 200 and hide real breakage.

// TestStaleAstroHashServesTheCurrentAsset is the positive direction: a hash that
// belongs to the tier this one replaced, and that this build has never
// published, is answered with the current bytes of the same asset name. The
// first case is not synthetic - DQ88JkSx is the hash the static tier emits for
// the same stylesheet this tier serves as BEq7AnVK, measured against both tiers
// before this test was written.
func TestStaleAstroHashServesTheCurrentAsset(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	cases := []struct {
		path      string
		canonical string
		want      string
	}{
		{"/_astro/JsonLd.DQ88JkSx.css", baseCSSPath, cssContentType},
		{"/_astro/JsonLd.AAAAAAAAAA.css", baseCSSPath, cssContentType},
		{
			"/_astro/TableIsland.astro_astro_type_script_index_0_lang.DQ88JkSx.js",
			"/_astro/TableIsland.astro_astro_type_script_index_0_lang.UqXLqAt9.js",
			jsContentType,
		},
		{
			"/_astro/table-island.client.DQ88JkSx.js",
			"/_astro/table-island.client.6YN-J6Z7.js",
			jsContentType,
		},
		{
			"/_astro/heatmap-island.client.DQ88JkSx.js",
			"/_astro/heatmap-island.client.Ba_X43a2.js",
			jsContentType,
		},
		{
			"/_astro/preload-helper.DQ88JkSx.js",
			"/_astro/preload-helper.DJSjwBkS.js",
			jsContentType,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()
			exact := get(t, live, testCase.canonical)
			if exact.status != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200 (the canonical path is broken, so this test proves nothing)", testCase.canonical, exact.status)
			}

			resp := get(t, live, testCase.path)
			if resp.status != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200", testCase.path, resp.status)
			}
			if string(resp.body) != string(exact.body) {
				t.Errorf(
					"GET %s answered with %d bytes, but %s answered with %d bytes; the alias must serve the current asset byte for byte",
					testCase.path, len(resp.body), testCase.canonical, len(exact.body),
				)
			}
			if got := resp.header.Get("Content-Type"); got != testCase.want {
				t.Errorf("GET %s: Content-Type = %q, want %q", testCase.path, got, testCase.want)
			}
			if got := resp.header.Get("X-Asset-Alias"); got != testCase.canonical {
				t.Errorf("GET %s: X-Asset-Alias = %q, want %q", testCase.path, got, testCase.canonical)
			}
			if body := resp.text(); strings.Contains(body, `data-fault=`) {
				t.Errorf("GET %s: the alias served a fault page: %s", testCase.path, body)
			}
		})
	}
}

// TestAliasedAssetIsNotImmutable pins the one promise the tier declines to make.
// The URL names the bytes of an older build and the reply is not those bytes, so
// an immutable directive would be a year-long lie that outlives several deploys.
// The alias gets the ordinary static TTL instead: a client that kept the old URL
// revalidates within the hour and converges on the current asset, while the
// canonical URL keeps the immutable promise. The last two cases are the control
// - the exact path must still be immutable, and must not claim to be an alias.
func TestAliasedAssetIsNotImmutable(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	cases := []struct {
		path             string
		cacheControl     string
		wantAssetAlias   bool
		wantEntityTagged bool
	}{
		{"/_astro/JsonLd.DQ88JkSx.css", staticCacheControl, true, true},
		{"/_astro/JsonLd.BEq7AnVK.css", immutableCacheControl, false, true},
		{
			"/_astro/TableIsland.astro_astro_type_script_index_0_lang.DQ88JkSx.js",
			staticCacheControl, true, true,
		},
		{
			"/_astro/TableIsland.astro_astro_type_script_index_0_lang.UqXLqAt9.js",
			immutableCacheControl, false, true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()
			resp := get(t, live, testCase.path)
			if resp.status != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200", testCase.path, resp.status)
			}
			if got := resp.header.Get("Cache-Control"); got != testCase.cacheControl {
				t.Errorf("GET %s: Cache-Control = %q, want %q", testCase.path, got, testCase.cacheControl)
			}
			aliased := resp.header.Get("X-Asset-Alias") != ""
			if aliased != testCase.wantAssetAlias {
				t.Errorf("GET %s: X-Asset-Alias present = %t, want %t", testCase.path, aliased, testCase.wantAssetAlias)
			}
			if tagged := resp.header.Get("ETag") != ""; tagged != testCase.wantEntityTagged {
				t.Errorf("GET %s: ETag present = %t, want %t", testCase.path, tagged, testCase.wantEntityTagged)
			}
		})
	}
}

// TestUnknownAstroAssetIsStillMissing is the negative control. Every path here
// is one the alias must refuse: an asset name this tier never published, an
// extension it does not serve, a path with no hash segment to stand for content,
// an empty or oversized hash segment, and a traversed name. If the alias ever
// starts answering these with 200, this test fails and the 200s are the bug.
func TestUnknownAstroAssetIsStillMissing(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	overlong := strings.Repeat("A", maxAstroHashBytes+1)
	cases := []string{
		"/_astro/NoSuchAsset.abcdefgh.css",
		"/_astro/NoSuchAsset.abcdefgh.js",
		"/_astro/JsonLd.DQ88JkSx.wasm",
		"/_astro/JsonLd.css",
		"/_astro/JsonLd..css",
		"/_astro/.DQ88JkSx.css",
		"/_astro/JsonLd." + overlong + ".css",
		"/_astro/JsonLd.DQ88%20JkSx.css",
		"/_astro/JsonLd.DQ88JkSx.CSS",
		"/_astro/sub/dir/JsonLd.DQ88JkSx.css",
		"/_astro/JsonLd.DQ88JkSx.css/extra",
		"/_astro/JsonLd.BEq7AnVK.css.gz",
		// Well-formed asset URLs whose name or extension this tier does not
		// serve. The parse accepts them - the interesting part is that the
		// lookup behind it does not.
		"/_astro/foo.bar.baz",
		"/_astro/caf%C3%A9.DQ88JkSx.css",
	}

	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			resp := get(t, live, path)
			if resp.status != http.StatusNotFound {
				t.Fatalf("GET %s: status = %d, want 404", path, resp.status)
			}
			if body := resp.text(); !strings.Contains(body, `data-fault="not-found"`) {
				t.Errorf("GET %s: a 404 must be the site's own visible page, got: %.200s", path, body)
			}
		})
	}
}

// TestAstroAssetIDIsOneReadingForBothSides pins the property the alias rests on:
// every canonical path this tier serves parses through astroAssetID into the
// same name and extension a request is parsed into, so the two sides cannot
// disagree about which part of a path is the hash. It also pins the parse the
// other way round, on inputs that are not Astro asset URLs at all.
func TestAstroAssetIDIsOneReadingForBothSides(t *testing.T) {
	t.Parallel()

	canonical := []struct {
		path string
		ext  string
	}{
		{baseCSSPath, "css"},
	}
	for path := range islandChunks {
		canonical = append(canonical, struct {
			path string
			ext  string
		}{path, "js"})
	}

	for _, item := range canonical {
		name, hash, ext, ok := astroAssetID(item.path)
		if !ok {
			t.Errorf("astroAssetID(%q) reported not-an-asset", item.path)
			continue
		}
		if name == "" || hash == "" {
			t.Errorf("astroAssetID(%q) = name %q, hash %q; both must be present", item.path, name, hash)
		}
		if ext != item.ext {
			t.Errorf("astroAssetID(%q): ext = %q, want %q", item.path, ext, item.ext)
		}
		if !strings.HasSuffix(item.path, "."+hash+"."+ext) {
			t.Errorf("astroAssetID(%q) read %q as the hash, which is not the segment before the extension", item.path, hash)
		}
		// A canonical path is its own alias: the same asset, under the hash this
		// build published. The exact-path lookup already answered it before the
		// alias runs, so this is a statement about the parse agreeing, not about
		// which response a browser gets.
		gotPath, body, _, found := astroAssetAlias(item.path)
		if !found || gotPath != item.path {
			t.Errorf("astroAssetAlias(%q) = (%q, %d bytes, found=%t), want itself", item.path, gotPath, len(body), found)
		}
	}

	notAnAsset := []string{
		"",
		"/",
		"/_astro",
		"/_astro/",
		"/_astro/foo",
		"/_astro/.css",
		"/_astro/foo..js",
		"/_astro/foo.bar",
		"/fonts/inter-400.woff2",
		"/agg/v1/manifest.json",
	}
	for _, path := range notAnAsset {
		if name, hash, ext, ok := astroAssetID(path); ok {
			t.Errorf("astroAssetID(%q) = (%q, %q, %q, true); a path that is not an Astro asset URL must not be parsed as one", path, name, hash, ext)
		}
	}
}

// TestAstroAssetAliasCoversEveryServedName is the coverage claim the cutover
// plan depends on, stated as a test: for every asset name and extension this
// tier serves, an unknown hash resolves. A name added later without a test case
// here is not covered by the loop, so the count is asserted too - adding a
// served asset should be a deliberate change to a number in this file.
func TestAstroAssetAliasCoversEveryServedName(t *testing.T) {
	t.Parallel()

	served := map[string]bool{baseCSSPath: true}
	for path := range islandChunks {
		served[path] = true
	}
	if len(served) != 6 {
		t.Fatalf("this tier serves %d /_astro assets, but this test was written against 6; add the new name to the coverage case below", len(served))
	}

	for path := range served {
		name, _, ext, ok := astroAssetID(path)
		if !ok {
			t.Errorf("astroAssetID(%q) reported not-an-asset", path)
			continue
		}
		stale := "/_astro/" + name + ".ZZZZZZZZ." + ext
		canonical, body, _, found := astroAssetAlias(stale)
		if !found {
			t.Errorf("astroAssetAlias(%q) found nothing, but %q is served: a page from the tier this one replaced would get a 404 asset", stale, path)
			continue
		}
		if canonical != path {
			t.Errorf("astroAssetAlias(%q) = %q, want %q", stale, canonical, path)
		}
		if len(body) == 0 {
			t.Errorf("astroAssetAlias(%q) returned an empty body", stale)
		}
	}
}
