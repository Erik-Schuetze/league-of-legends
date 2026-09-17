// Package webtier is the Go server-rendered web tier that replaced the Astro
// build (see DECISION-dynamic-architecture.md, option B). It reads the
// published agg/v1 snapshot read-only and renders the same route families the
// static site published, from one binary and one language.
//
// The package is deliberately split along the same lines the Astro site was:
// this file holds the frozen design-system assets, snapshot.go reads and
// enforces the artifacts, site.go holds the provenance state machine, the
// view_*.go files build one route family each, and server.go applies the HTTP
// policy. Nothing here writes to the aggregate tree.
package webtier

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
)

// ScopedCIDs are the `data-astro-cid-*` tokens the Astro compiler assigned to
// each component, keyed by the component name.
//
// They are the scoped-style suffixes of the design system that was kept as-is:
// the stylesheets below are the ones web/src/*.astro compiled to, and the
// selectors in them are `[data-astro-cid-<token>]` attribute selectors. A
// template that emits the matching attribute therefore gets the component's
// styles without the CSS having to be rewritten, and the rendered output stays
// comparable to the site the tier replaced. The templates hardcode the
// attribute, so TestScopedCIDsArePresentInCSS asserts every token here against
// the embedded stylesheet: a mistyped token fails a test instead of rendering
// an unstyled component that still looks plausible.
var ScopedCIDs = map[string]string{
	"fallback-build-list": "4wwh6mzq",
	"build-list":          "hqssc25b",
	"card":                "yk4hkwyg",
	"data-table":          "3jeeeo45",
	"filter-bar":          "2t5tmnod",
	"footer":              "jo6i4kqk",
	"heatmap-island":      "5wqubv3u",
	"nav":                 "wpvy4v7s",
	"sample-size-notice":  "6yasdyvb",
	"stat-value":          "owrb7bwm",
	"table-island":        "zt6f6xwj",
	"tier-badge":          "n644qoiz",
}

//go:embed assets
var assetsFS embed.FS

// asset opens one embedded asset, panicking on a missing name: these are
// compile-time constants, so a failure here is a build error rather than a
// runtime condition.
func asset(name string) []byte {
	buf, err := fs.ReadFile(assetsFS, "assets/"+name)
	if err != nil {
		panic(fmt.Sprintf("webtier: embedded asset %s: %v", name, err))
	}
	return buf
}

// baseCSS is the design system's own stylesheet (tokens.css, global.css,
// fonts.css and layouts/fallback/base.css, bundled by the Astro build). It is
// served at the same path the static site served it from, with the same bytes,
// so a browser or a reader that cached it sees no change.
const baseCSSPath = "/_astro/JsonLd.BEq7AnVK.css"

func baseCSS() []byte { return asset("astro/JsonLd.BEq7AnVK.css") }

// scopedCSS is every component-scoped rule, concatenated in the order the
// Astro build emitted them. The build produced exactly two variants of it: all
// twelve chunks on a champion page (which renders layouts/fallback/BuildList),
// and the same block without that chunk everywhere else.
func scopedCSS(withFallbackBuildList bool) []byte {
	if withFallbackBuildList {
		return asset("css/scoped-champion.css")
	}
	return asset("css/scoped-common.css")
}

// IslandChunks is the built JavaScript of the three vanilla islands, keyed by
// the exact path the static site served each file from. They are served
// unchanged: the islands enhance a DOM that this tier renders by itself, so a
// browser that never runs them reads the same table.
var islandChunks = map[string]string{
	"/_astro/heatmap-island.client.Ba_X43a2.js":                              "astro/heatmap-island.client.Ba_X43a2.js",
	"/_astro/HeatmapIsland.astro_astro_type_script_index_0_lang.Bkbkcf-c.js": "astro/HeatmapIsland.astro_astro_type_script_index_0_lang.Bkbkcf-c.js",
	"/_astro/preload-helper.DJSjwBkS.js":                                     "astro/preload-helper.DJSjwBkS.js",
	"/_astro/table-island.client.6YN-J6Z7.js":                                "astro/table-island.client.6YN-J6Z7.js",
	"/_astro/TableIsland.astro_astro_type_script_index_0_lang.UqXLqAt9.js":   "astro/TableIsland.astro_astro_type_script_index_0_lang.UqXLqAt9.js",
}

// staticAsset returns the body and content type of a path served without
// rendering, or found=false for anything this tier does not serve directly.
func staticAsset(path string) (body []byte, contentType string, found bool) {
	switch path {
	case baseCSSPath:
		return baseCSS(), "text/css; charset=utf-8", true
	case "/favicon.svg":
		return asset("favicon.svg"), "image/svg+xml", true
	}
	if name, ok := islandChunks[path]; ok {
		return asset(name), "text/javascript; charset=utf-8", true
	}
	if len(path) > len("/fonts/") && path[:7] == "/fonts/" {
		switch path {
		case "/fonts/inter-400.woff2", "/fonts/inter-600.woff2", "/fonts/inter-700.woff2",
			"/fonts/jetbrains-mono-400.woff2", "/fonts/jetbrains-mono-700.woff2",
			"/fonts/montserrat-700.woff2":
			return asset("fonts/" + path[7:]), "font/woff2", true
		case "/fonts/OFL-Inter.txt", "/fonts/OFL-JetBrainsMono.txt", "/fonts/OFL-Montserrat.txt":
			return asset("fonts/" + path[7:]), "text/plain; charset=utf-8", true
		}
	}
	return nil, "", false
}

// scopedCSSChunk is the scopedCSS value the templates inline.
func scopedCSSChunk(withFallbackBuildList bool) string {
	return string(scopedCSS(withFallbackBuildList))
}

// hasScopedCID reports whether a component's scoped suffix appears in the
// champion stylesheet, which is the one that carries every chunk.
func hasScopedCID(cid string) bool {
	return bytes.Contains(scopedCSS(true), []byte("data-astro-cid-"+cid))
}
