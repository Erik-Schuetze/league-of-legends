package webtier

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDeployedPostureDoesNotPublishRealData is the guard that
// deploy/base/web/go-deployment.yaml's comment block points at. The tier the
// edge proxies is publicly reachable - plan.md counts the auth gate as public -
// so its committed default has to be the labelled preview until Riot's
// production key exists and section 15 question 6 is answered. The data posture
// is one line and the edge cutover is a separate, later act, so a manifest that
// defaults to real data turns that unrelated edit into a publication:
//
//   - "off" renders LOLSTATS_AGG_ROOT, so the moment the edge points at this
//     service the real crawled snapshot is public behind the gate password
//     alone (section 12 risk R2, section 13 publication trigger).
//   - "auto" is the same hazard one step removed: it serves the real snapshot
//     silently as soon as one exists, which is the PVC's current state.
//
// The preview value therefore has to be *declared here* rather than inherited
// from the shared ConfigMap (owned by the data-plane lane), whose value is free
// to move with the static tier's phase. Real data stays supportable - "off"
// renders the aggregate tree and answers a loud 503 when it is missing - it just
// must not be the default.
//
// Commented lines are skipped, so the Phase 5 record of the value stays readable
// next to the env block it belongs to.
func TestDeployedPostureDoesNotPublishRealData(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(discoverRepoRoot(), "deploy", "base", "web")
	scanned := 0
	postures := map[string][]string{}
	for _, path := range yamlEntries(t, dir) {
		scanned++
		for number, value := range fixturesValues(t, path) {
			where := path + ":" + strconv.Itoa(number+1)
			postures[path] = append(postures[path], where+"="+value)
			switch {
			case strings.Contains(value, "off"), strings.Contains(value, "auto"):
				t.Errorf("%s sets %q: publishing the real crawled snapshot is a Phase 5 step, gated on Riot's production key (plan.md item 1, risk R2, section 15 question 6). Keep it a commented line until then.", where, value)
			case value != "only":
				t.Errorf("%s sets %q, which is not the preview value: this Deployment has to default to the labelled preview", where, value)
			}
		}
	}
	if scanned == 0 {
		t.Fatalf("%s holds no manifests to check", dir)
	}

	tier := filepath.Join(dir, "go-deployment.yaml")
	if len(postures[tier]) == 0 {
		t.Errorf("%s does not set LOLSTATS_AGG_FIXTURES at all: the preview posture has to be declared on the container rather than inherited from the shared ConfigMap, or the shared switch moving silently republishes real data", tier)
	}
}

// yamlEntries lists the manifests in a directory.
func yamlEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

// fixturesValues returns the actively set values of LOLSTATS_AGG_FIXTURES in one
// manifest, keyed by line number. Both `LOLSTATS_AGG_FIXTURES: x` and the
// two-line `- name: LOLSTATS_AGG_FIXTURES` / `value: x` form are read, because
// the second is how every env entry in these manifests is written and a check
// that read only the first would pass a manifest that sets `off` merely by
// formatting it the usual way.
func fixturesValues(t *testing.T, path string) map[int]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	const name = "LOLSTATS_AGG_FIXTURES"
	lines := strings.Split(string(raw), "\n")
	values := map[int]string{}
	for number, line := range lines {
		active := strings.TrimSpace(line)
		if strings.HasPrefix(active, "#") || !strings.Contains(active, name) {
			continue
		}
		value := active[strings.Index(active, name)+len(name):]
		if !strings.HasPrefix(strings.TrimSpace(value), ":") {
			value = ""
			for _, follow := range lines[number+1:] {
				field := strings.TrimSpace(follow)
				if field == "" || strings.HasPrefix(field, "#") {
					continue
				}
				if !strings.HasPrefix(field, "value:") {
					break
				}
				value = field
				break
			}
		}
		if index := strings.Index(value, "#"); index >= 0 {
			value = value[:index]
		}
		value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "value:"))
		value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), ":"))
		values[number] = strings.Trim(value, `"`)
	}
	return values
}
