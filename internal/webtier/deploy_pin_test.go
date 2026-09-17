package webtier

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The tier is deployed from a shared image repository, so the Deployment tree
// pins `:latest` and the overlay's `images` transformer is the only thing that
// moves a workload off it. That transformer matches every workload naming the
// repository, which is why this tier's digest travels in a
// `lolstats.dev/pinned-image` annotation and is copied into its own container by
// the overlay's `replacements` block - and why a digest that is merely
// *plausible* is dangerous: `sha256:bc84909b...` with two characters missing is
// accepted by every YAML reader, by kustomize, and by the Deployment, and is
// only rejected by the node, as `InvalidImageName`, hours later.
//
// This test is the cheap half of that lesson: whatever digest the manifests
// name must be a well-formed one, and the wiring that carries it to the
// container must still be present.

var pinnedAnnotation = regexp.MustCompile(`(?m)^\s*lolstats\.dev/pinned-image:\s*(\S+)\s*$`)

// digestRef matches an image reference pinned by digest with a full-length
// algorithm sum.
var digestRef = regexp.MustCompile(`^[^\s@]+@sha256:[0-9a-f]{64}$`)

// digestAt matches any digest suffix, including malformed ones, so the test can
// report them instead of silently skipping a line it failed to parse.
var digestAt = regexp.MustCompile(`@sha256:[0-9a-f]+`)

// digestSum is the shape of the sum itself: 64 lowercase hex characters.
var digestSum = regexp.MustCompile(`^[0-9a-f]{64}$`)

func readManifest(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestDeployDigestsAreWellFormed(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "deploy", "base", "web", "*.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no manifests found under deploy/base/web; the test would pass vacuously")
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, ref := range digestAt.FindAllString(string(data), -1) {
			sum := strings.TrimPrefix(ref, "@sha256:")
			if !digestSum.MatchString(sum) {
				t.Errorf("%s: %q is not a well-formed digest reference (%d hex characters)", path, ref, len(sum))
			}
		}
	}
}

func TestPinnedImageAnnotationIsAWellFormedDigest(t *testing.T) {
	manifest := readManifest(t, "deploy", "base", "web", "go-deployment.yaml")
	match := pinnedAnnotation.FindStringSubmatch(manifest)
	if match == nil {
		t.Fatal("deploy/base/web/go-deployment.yaml has no lolstats.dev/pinned-image annotation")
	}
	if !digestRef.MatchString(match[1]) {
		t.Errorf("pinned image %q is not a well-formed digest reference", match[1])
	}
	if strings.Contains(match[1], ":latest") {
		t.Errorf("pinned image %q still names a tag; a tag is not a roll-out trigger", match[1])
	}
}

func TestOverlayCopiesThePinnedImageIntoTheTier(t *testing.T) {
	overlay := readManifest(t, "deploy", "overlays", "homelab", "kustomization.yaml")
	for _, want := range []string{
		"metadata.annotations.[lolstats.dev/pinned-image]",
		"spec.template.spec.containers.[name=web].image",
	} {
		if !strings.Contains(overlay, want) {
			t.Errorf("deploy/overlays/homelab/kustomization.yaml no longer references %q, so the pin never reaches the container", want)
		}
	}
}
