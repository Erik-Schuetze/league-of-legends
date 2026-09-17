package aggregate

import (
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Window helpers. The window itself is aggmodel.Window so that what the build
// filters on is byte-for-byte what the artifact publishes; these helpers only
// add the parsing and comparison the build needs.

const dateLayout = time.DateOnly

// parseDate validates a YYYY-MM-DD date and returns it as a UTC midnight time.
//
// The round-trip check rejects inputs that time.Parse would silently normalise
// (2026-02-31) and inputs with non-padded components (2026-9-1), so a window
// that reaches an artifact path or a SQL literal is always canonical.
func parseDate(date string) (time.Time, error) {
	if date == "" {
		return time.Time{}, fmt.Errorf("date is empty")
	}
	parsed, err := time.ParseInLocation(dateLayout, date, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q is not YYYY-MM-DD", date)
	}
	if parsed.Format(dateLayout) != date {
		return time.Time{}, fmt.Errorf("date %q is not YYYY-MM-DD", date)
	}
	return parsed, nil
}

// validateWindow reports whether a window is well formed.
func validateWindow(window aggmodel.Window) error {
	from, err := parseDate(window.From)
	if err != nil {
		return fmt.Errorf("window from: %w", err)
	}
	to, err := parseDate(window.To)
	if err != nil {
		return fmt.Errorf("window to: %w", err)
	}
	if to.Before(from) {
		return fmt.Errorf("window to %s precedes from %s", window.To, window.From)
	}
	return nil
}

// windowForEnd returns the window of the given number of days ending on end,
// inclusive of both ends: a 14 day window ending 2026-09-17 starts 2026-09-04.
//
// aggmodel.WindowFor subtracts the full length instead, which yields a 15 day
// span. The build needs the inclusive form because it publishes source_window
// as the range it actually read, and an off-by-one day there is a claim about
// data the artifact does not contain.
func windowForEnd(end string, days int) (aggmodel.Window, error) {
	to, err := parseDate(end)
	if err != nil {
		return aggmodel.Window{}, err
	}
	if days < 1 {
		return aggmodel.Window{}, fmt.Errorf("window days must be >= 1, got %d", days)
	}
	if days > 400 {
		return aggmodel.Window{}, fmt.Errorf("window days %d is implausible", days)
	}
	from := to.AddDate(0, 0, -(days - 1))
	return aggmodel.Window{From: from.Format(dateLayout), To: to.Format(dateLayout)}, nil
}

// windowContains reports whether a YYYY-MM-DD date falls inside the window.
func windowContains(window aggmodel.Window, date string) bool {
	if date == "" {
		return false
	}
	return date >= window.From && date <= window.To
}

// comparePatch orders two major.minor patch strings numerically, so that 16.9
// sorts before 16.10. Lexical ordering is wrong for exactly one patch a year
// and would make the "newest patch" choice silently pick the older one.
func comparePatch(a, b string) int {
	amaj, amin, aok := splitPatch(a)
	bmaj, bmin, bok := splitPatch(b)
	if !aok || !bok {
		return 0
	}
	switch {
	case amaj != bmaj:
		if amaj < bmaj {
			return -1
		}
		return 1
	case amin != bmin:
		if amin < bmin {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func splitPatch(patch string) (int, int, bool) {
	var major, minor int
	if _, err := fmt.Sscanf(patch, "%d.%d", &major, &minor); err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// newestPatch returns the numerically newest patch in the list, or "" if the
// list is empty.
func newestPatch(patches []string) string {
	newest := ""
	for _, patch := range patches {
		if newest == "" || comparePatch(patch, newest) > 0 {
			newest = patch
		}
	}
	return newest
}
