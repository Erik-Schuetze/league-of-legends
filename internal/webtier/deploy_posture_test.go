package webtier

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDeployedPostureRendersRealData is the guard that
// deploy/base/web/go-deployment.yaml's env comment block points at.
//
// It was written as TestDeployedPostureDoesNotPublishRealData, and its premise
// was "until Riot's production key exists and section 15 question 6 is
// answered": the tier the edge proxies is publicly reachable, so its committed
// default had to be the labelled preview, or the separate act of pointing the
// edge at it would have published the crawled snapshot as a side effect.
//
// The owner has answered that question the other way and the answer is recorded
// in docs/decisions (commit b262dcd): the site does serve real Riot-derived
// aggregates, deliberately, behind the existing password gate. The premise is
// gone; the purpose is not. What the guard protects now is the same honesty
// requirement read from the other side - the shipped posture renders the
// *published snapshot*, and the checked-in demo/fixture path must never stand
// in for it in front of the public URL. A table of layout-exercise rows served
// where match statistics are promised is the defect this project has already
// shipped once, and it is worse than a brief outage.
//
// The declared value is resolved through the tier's own root selection
// (candidateRoots) rather than compared against a list of known values. The
// mode enum is closed and OptionsFromEnv silently normalises anything it does
// not recognise to "auto", so a string comparison would call a posture safe
// that the process actually renders as a fixture fallback - the assertion has
// to be about the data state that would be rendered, not the string setting it.
//
// The root is modelled as *not* explicitly configured, for the reason the
// original guard gave: the deployed container inherits LOLSTATS_AGG_ROOT from
// the shared ConfigMap, which is another lane's to move, and a posture whose
// honesty depends on that shared value is not one this Deployment may ship.
// Commented lines are skipped, so the record of the superseded value stays
// readable next to the env block it belongs to.
func TestDeployedPostureRendersRealData(t *testing.T) {
	// Not parallel: t.Setenv has to hold for the whole test, and it is what
	// pins the unresolved-root reading described above.
	t.Setenv(EnvAggRoot, "")

	dir := filepath.Join(discoverRepoRoot(), "deploy", "base", "web")
	scanned := 0
	postures := map[string][]string{}
	for _, path := range yamlEntries(t, dir) {
		scanned++
		for number, value := range fixturesValues(t, path) {
			where := path + ":" + strconv.Itoa(number+1)
			postures[path] = append(postures[path], where+"="+value)
			if state := renderedDataState(value); state != StateLive {
				t.Errorf("%s sets %q, which renders data-state=%q: the deployed tier reads the published snapshot and never substitutes the checked-in demo tree. Real data is the posture of record since the owner's 2026-09-17 decision (docs/decisions, commit b262dcd); a snapshot that is missing is a loud 503, not a preview.", where, value, state)
			}
		}
	}
	if scanned == 0 {
		t.Fatalf("%s holds no manifests to check", dir)
	}

	tier := filepath.Join(dir, "go-deployment.yaml")
	if len(postures[tier]) == 0 {
		t.Errorf("%s does not set LOLSTATS_AGG_FIXTURES at all: the posture has to be declared on the container rather than inherited from the shared ConfigMap, or the shared switch moving silently changes what the public tier serves", tier)
	}
}

// renderedDataState reports the data state a declared LOLSTATS_AGG_FIXTURES
// value would render, resolved the way the tier resolves it at start-up.
//
// StateDemo means the checked-in demo tree is reachable - either because the
// value names it, or because the value is one the tier does not recognise and
// therefore normalises to "auto". StateLive means the aggregate root is the
// only root read: a published snapshot renders as live, and a root that holds
// no manifest answers a loud 503 rather than substituting fixtures, which is
// the outcome the posture is chosen for.
func renderedDataState(value string) DataState {
	root := discoverRepoRoot()
	opts := Options{
		AggRoot:      filepath.Join(root, "agg"),
		FixturesDir:  filepath.Join(root, "web", "src", "fixtures"),
		FixturesMode: FixturesMode(strings.ToLower(strings.TrimSpace(value))),
	}
	for _, candidate := range candidateRoots(opts) {
		if candidate.Dir == opts.FixturesDir {
			return StateDemo
		}
	}
	return StateLive
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
