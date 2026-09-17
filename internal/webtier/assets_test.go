package webtier

import (
	"bytes"
	"testing"
)

// TestScopedCIDsArePresentInCSS asserts every documented component token still
// appears in the embedded stylesheet, and that the one chunk the build emits
// only on a champion page is the only difference between the two variants.
//
// The templates hardcode the `data-astro-cid-*` attribute rather than reading
// ScopedCIDs, so a token that drifted out of the CSS would render an unstyled
// component that still looks plausible in a diff. This is the assertion the
// package's own comment promises.
func TestScopedCIDsArePresentInCSS(t *testing.T) {
	champion := scopedCSS(true)
	common := scopedCSS(false)
	for component, cid := range ScopedCIDs {
		attr := []byte("data-astro-cid-" + cid)
		if !hasScopedCID(cid) {
			t.Errorf("component %q: %s is not in the champion stylesheet", component, attr)
		}
		// The champion page renders layouts/fallback/BuildList, so its chunk is
		// the single difference: every other component's rules are in both.
		inCommon := bytes.Contains(common, attr)
		wantInCommon := component != "fallback-build-list"
		if inCommon != wantInCommon {
			t.Errorf("component %q: %s in common stylesheet = %t, want %t", component, attr, inCommon, wantInCommon)
		}
	}
	if bytes.Equal(champion, common) {
		t.Fatal("the champion and common stylesheets are identical, so the champion page would lose the build list's styles")
	}
}
