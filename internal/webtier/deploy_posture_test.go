package webtier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeployedPostureDoesNotPublishRealData is the guard that
// deploy/base/web/go-deployment.yaml's comment block points at. The tier that
// the edge proxies is publicly reachable - plan.md counts the basic_auth gate as
// public - so it has to keep serving the labelled preview until Riot's
// production key exists and section 15 question 6 is answered. Nothing in that
// directory may actively set the fixtures switch to "off" or "auto":
//
//   - "off" renders the published aggregate root, so the moment the edge
//     selector points at this service the real crawled snapshot is public.
//   - "auto" silently falls back to the demo tree when the artifact is missing
//     or unreadable, which is how a tier decides it is live and serves preview
//     numbers.
//
// Left unset, the container inherits LOLSTATS_AGG_FIXTURES from the shared
// ConfigMap (owned by the data-plane lane), which is what keeps this tier on the
// preview. Commented lines are ignored, so the Phase 5 record of the value can
// stay next to the env block it belongs to.
func TestDeployedPostureDoesNotPublishRealData(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(discoverRepoRoot(), "deploy", "base", "web")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		scanned++
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for number, line := range strings.Split(string(raw), "\n") {
			active := strings.TrimSpace(line)
			if strings.HasPrefix(active, "#") || !strings.Contains(active, "LOLSTATS_AGG_FIXTURES") {
				continue
			}
			value := active[strings.Index(active, "LOLSTATS_AGG_FIXTURES")+len("LOLSTATS_AGG_FIXTURES"):]
			if !strings.HasPrefix(strings.TrimSpace(value), ":") {
				// The entry names the variable and sets it on the next line,
				// which is how every env entry in these manifests is written.
				value = ""
				for _, follow := range strings.Split(string(raw), "\n")[number+1:] {
					entry := strings.TrimSpace(follow)
					if entry == "" || strings.HasPrefix(entry, "#") {
						continue
					}
					if !strings.HasPrefix(entry, "value:") {
						break
					}
					value = entry
					break
				}
			}
			if index := strings.Index(value, "#"); index >= 0 {
				value = value[:index]
			}
			if strings.Contains(value, "off") || strings.Contains(value, "auto") {
				t.Errorf("%s:%d sets %q: publishing the real crawled snapshot is a Phase 5 step gated on Riot's production key (plan.md item 1, risk R2, section 15 question 6)", path, number+1, strings.TrimSpace(active+" "+value))
			}
		}
	}
	if scanned == 0 {
		t.Fatalf("%s holds no manifests to check", dir)
	}
}
