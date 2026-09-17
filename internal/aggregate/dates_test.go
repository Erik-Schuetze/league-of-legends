package aggregate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The window helpers used to carry two predicates of their own - one that
// validated a window and one that tested whether it contained a date. Both were
// dead: the containing test moved into the SQL the engine runs, and the
// validation is a property of how the window is constructed rather than
// something a caller has to assert afterwards. Deleting them removed the only
// place those guarantees were written down, so they are pinned here instead.

// TestParseDateIsCanonical covers the shape every date in the build has to have.
//
// The round trip is the point: time.Parse accepts several spellings and
// normalises impossible days, so a window or a partition prefix that reaches a
// SQL literal, an artifact path or a published envelope is only canonical if
// the formatted result equals the input.
func TestParseDateIsCanonical(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		date  string
		valid bool
	}{
		{name: "an ordinary day", date: "2026-09-14", valid: true},
		{name: "the first day of a patch", date: "2026-09-01", valid: true},
		{name: "a leap day in a leap year", date: "2028-02-29", valid: true},
		{name: "a leap day in a common year", date: "2027-02-29"},
		{name: "a day that the parser would normalise", date: "2026-02-31"},
		{name: "an unpadded month", date: "2026-9-01"},
		{name: "an unpadded day", date: "2026-09-1"},
		{name: "a slashed date", date: "2026/09/14"},
		{name: "a timestamp", date: "2026-09-14T00:00:00Z"},
		{name: "a month out of range", date: "2026-13-01"},
		{name: "empty", date: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseDate(tc.date)
			if tc.valid {
				if err != nil {
					t.Fatalf("parseDate(%q) failed: %v", tc.date, err)
				}
				if formatted := got.Format(dateLayout); formatted != tc.date {
					t.Errorf("parseDate(%q) round-tripped to %q", tc.date, formatted)
				}
				return
			}
			if err == nil {
				t.Errorf("parseDate(%q) accepted an input that is not canonical YYYY-MM-DD", tc.date)
			}
		})
	}
}

// TestWindowForEndIsInclusiveAndBounded pins the arithmetic a reader relies on.
//
// Both ends are days the build reads, so the span is `days` long and the
// closing day is the one asked for - the same rule the patch boundary test
// exercises through the SQL, applied here to the values that reach the
// published source_window.
func TestWindowForEndIsInclusiveAndBounded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		end   string
		days  int
		want  aggmodel.Window
		valid bool
	}{
		{
			name: "the default window",
			end:  "2026-09-17", days: DefaultWindowDays,
			want:  aggmodel.Window{From: "2026-09-04", To: "2026-09-17"},
			valid: true,
		},
		{
			name: "a single day",
			end:  "2026-09-17", days: 1,
			want:  aggmodel.Window{From: "2026-09-17", To: "2026-09-17"},
			valid: true,
		},
		{
			name: "a window crossing a month boundary",
			end:  "2026-10-02", days: 5,
			want:  aggmodel.Window{From: "2026-09-28", To: "2026-10-02"},
			valid: true,
		},
		{
			name: "a window crossing a year boundary",
			end:  "2027-01-03", days: 7,
			want:  aggmodel.Window{From: "2026-12-28", To: "2027-01-03"},
			valid: true,
		},
		{name: "zero days", end: "2026-09-17", days: 0},
		{name: "a negative window", end: "2026-09-17", days: -1},
		{name: "an implausibly long window", end: "2026-09-17", days: 401},
		{name: "a malformed end", end: "2026-9-17", days: 14},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := windowForEnd(tc.end, tc.days)
			if !tc.valid {
				if err == nil {
					t.Fatalf("windowForEnd(%q, %d) = %+v, want a refusal", tc.end, tc.days, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("windowForEnd(%q, %d) failed: %v", tc.end, tc.days, err)
			}
			if got != tc.want {
				t.Errorf("windowForEnd(%q, %d) = %s..%s, want %s..%s",
					tc.end, tc.days, got.From, got.To, tc.want.From, tc.want.To)
			}
			// The window is published as the range the build read, so it must
			// span exactly the number of days asked for.
			from, _ := parseDate(got.From)
			to, _ := parseDate(got.To)
			if span := int(to.Sub(from).Hours()/24) + 1; span != tc.days {
				t.Errorf("window %s..%s spans %d days, want %d", got.From, got.To, span, tc.days)
			}
		})
	}
}

// TestVerifyRejectsAnInvertedWindow restores the check the deleted
// validateWindow helper was reaching for.
//
// The writer cannot invert a window - windowForEnd derives the start from a day
// count - so the only place an impossible window can appear is an artifact
// damaged after the fact, which is the verifier's whole job. The manifest is
// damaged alongside the envelope so that the ordering rule is the only thing
// left to fail: otherwise the envelope/manifest agreement check would report it
// and this test would pass without the new rule.
func TestVerifyRejectsAnInvertedWindow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	result, err := Demo(DemoOptions{OutDir: root, MinCellN: 2})
	if err != nil {
		t.Fatalf("demo: %v", err)
	}

	seg := result.Seg
	relPath := seg.TierListPath()
	doc, err := readJSONDoc[map[string]any](filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("read tier list: %v", err)
	}
	doc["source_window"] = map[string]any{"from": "2026-09-17", "to": "2026-09-04"}
	if err := writeDoc(root, relPath, doc); err != nil {
		t.Fatalf("write tier list: %v", err)
	}

	manifestPath := filepath.Join(root, filepath.FromSlash(aggmodel.ManifestPath))
	manifest, err := readJSONDoc[map[string]any](manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	partitions, ok := manifest["partitions"].([]any)
	if !ok || len(partitions) != 1 {
		t.Fatalf("manifest holds %v, want one partition", manifest["partitions"])
	}
	entry, ok := partitions[0].(map[string]any)
	if !ok {
		t.Fatalf("manifest partition is not an object")
	}
	entry["source_window"] = map[string]any{"from": "2026-09-17", "to": "2026-09-04"}
	if err := writeDoc(root, aggmodel.ManifestPath, manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// The damaged tree is otherwise sound, so it must fail on the window alone.
	_, err = Verify(VerifyOptions{AggRoot: root, Source: aggmodel.SourceDemo})
	if err == nil {
		t.Fatal("verification accepted a window that ends before it starts")
	}
	if !strings.Contains(err.Error(), "ends before it starts") {
		t.Errorf("verification reported:\n%v\nwant a problem naming the inverted window", err)
	}
}
