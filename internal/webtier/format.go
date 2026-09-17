package webtier

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Number and date formatting for published values.
//
// These are ports of the retired web/ tree's format.ts. Grouping is done by hand rather
// than with a locale-aware formatter for the reason the original gives: the
// rendering tier is the only place these numbers are ever produced, and a
// locale-dependent formatter would let two environments emit different HTML for
// the same artifact. Deterministic output is worth a few lines here.

// Percent renders a rate as a percentage string: 0.5118 -> "51.18%". The rate
// stays a fraction in the artifact; only the view scales it.
func Percent(value float64, digits int) string {
	return fixed(value*100, digits) + "%"
}

// SignedPercentPoints renders a delta in percentage points with an explicit
// sign.
func SignedPercentPoints(value float64, digits int) string {
	points := value * 100
	sign := ""
	switch {
	case points > 0:
		sign = "+"
	case points < 0:
		sign = "-"
	}
	return sign + fixed(math.Abs(points), digits) + " pp"
}

// Integer renders a count with thousands separators: 8421 -> "8,421".
func Integer(value float64) string {
	rounded := math.Round(value)
	sign := ""
	if rounded < 0 {
		sign = "-"
	}
	digits := strconv.FormatFloat(math.Abs(rounded), 'f', 0, 64)
	var out strings.Builder
	for i := 0; i < len(digits); i++ {
		fromEnd := len(digits) - i
		out.WriteByte(digits[i])
		if fromEnd > 1 && (fromEnd-1)%3 == 0 {
			out.WriteByte(',')
		}
	}
	return sign + out.String()
}

// Decimal renders a fixed number of fractional digits.
func Decimal(value float64, digits int) string {
	return fixed(value, digits)
}

// WholePercent rounds to whole percentage points, for a scan-friendly summary.
func WholePercent(value float64) string {
	return strconv.Itoa(int(math.Round(value*100))) + "%"
}

// IsoDate keeps the date part of an ISO timestamp. Dates already come as
// YYYY-MM-DD.
func IsoDate(value string) string {
	if len(value) <= 10 {
		return value
	}
	return value[:10]
}

// UTCStamp renders an ISO instant as a UTC label, so the reader knows what it
// is. An unparseable value is passed through rather than guessed at.
func UTCStamp(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	utc := parsed.UTC()
	return utc.Format("2006-01-02 15:04") + " UTC"
}

// WindowLabel renders a source window: "2026-09-03 to 2026-09-17".
func WindowLabel(from, to string) string {
	return IsoDate(from) + " to " + IsoDate(to)
}

// commitPrefixLength is how much of a build row's commit the site prints, from
// the reference build's git_sha.slice(0, 12).
const commitPrefixLength = 12

// ShortCommit is that slice. A commit shorter than the prefix is printed whole
// rather than padded, which is what String.prototype.slice does and what the
// published manifest's "unknown" therefore renders as.
func ShortCommit(commit string) string {
	if len(commit) <= commitPrefixLength {
		return commit
	}
	return commit[:commitPrefixLength]
}

// PlusMinus renders the 95% confidence half-width in percentage points.
func PlusMinus(halfWidth float64, digits int) string {
	return "+/- " + fixed(halfWidth*100, digits) + " pp"
}

// Median is the median of a sample-size series, used for the honesty notice on
// aggregate pages. An even-length series rounds the mean of the middle pair,
// matching the original.
func Median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int, len(values))
	copy(sorted, values)
	sortInts(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return int(math.Round(float64(sorted[mid-1]+sorted[mid]) / 2))
}

// fixed renders a value with a fixed number of fractional digits. It is
// Number.prototype.toFixed, not strconv.FormatFloat: the two disagree whenever
// the scaled value lands exactly on a half, because toFixed resolves a tie
// towards the larger integer while Go rounds half to even. That is not
// hypothetical - 0.4925 is a win rate this artifact can hold, and the two rules
// publish 49.3 and 49.2 respectively for the same cell.
func fixed(value float64, digits int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "NaN"
	}
	sign := ""
	magnitude := value
	if value < 0 {
		sign, magnitude = "-", -value
	}
	// Above 10^21 the original returns the number's own representation instead
	// of a fixed-point one.
	if magnitude >= 1e21 {
		return sign + strconv.FormatFloat(magnitude, 'f', -1, 64)
	}
	scaled := new(big.Rat).SetFloat64(magnitude)
	power := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	scaled.Mul(scaled, new(big.Rat).SetInt(power))
	// The tie rule is "pick the larger n"; for a non-negative value that is
	// floor(x + 1/2), computed exactly rather than in floating point.
	scaled.Add(scaled, big.NewRat(1, 2))
	rounded := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	text := rounded.String()
	if digits > 0 {
		for len(text) <= digits {
			text = "0" + text
		}
		text = text[:len(text)-digits] + "." + text[len(text)-digits:]
	}
	return sign + text
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// IntegerAny lets a template format a count that arrives as int, int64 or
// float64 without the template having to know which.
func IntegerAny(value any) string {
	switch typed := value.(type) {
	case int:
		return Integer(float64(typed))
	case int32:
		return Integer(float64(typed))
	case int64:
		return Integer(float64(typed))
	case float64:
		return Integer(typed)
	case float32:
		return Integer(float64(typed))
	case string:
		if parsed, err := strconv.ParseFloat(typed, 64); err == nil {
			return Integer(parsed)
		}
		return typed
	default:
		return fmt.Sprint(value)
	}
}

// UTCStampAny is UTCStamp for a value that is either an RFC3339 string or a
// decoded time.Time.
func UTCStampAny(value any) string {
	switch typed := value.(type) {
	case time.Time:
		return UTCStamp(typed.Format(time.RFC3339))
	case string:
		return UTCStamp(typed)
	default:
		return fmt.Sprint(value)
	}
}
