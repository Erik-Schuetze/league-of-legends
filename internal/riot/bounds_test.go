package riot

import (
	"context"
	"testing"
	"time"
)

// TestLimiterCountHeaderCannotDemandAnUnboundedWait is the permanent form of
// the probe that found a response header able to stall the crawl for years. The
// -Count headers report the *global* usage of the key, so a second consumer of
// the same key - another deployment, a teammate's script, Riot's own tooling -
// makes used exceed limit with no hostile input at all. A balance written from
// such a header must be bounded where it is written, so that the first
// reservation after it is already bounded, and not only where a duration is
// handed to a sleeper.
func TestLimiterCountHeaderCannotDemandAnUnboundedWait(t *testing.T) {
	clock := NewFakeClock(testStart)
	l := NewLimiter(LimiterOptions{Clock: clock, Ceiling: DevelopmentKeyWindows(), Bootstrap: DevelopmentKeyWindows()})
	l.Observe(rateLimitHeaders{
		AppLimit:       DevelopmentKeyWindows(),
		AppCount:       map[time.Duration]int{time.Second: 1_000_000_000, 2 * time.Minute: 1_000_000_000},
		HasApplication: true,
	})

	// A floored balance can ask for one window's worth of debt plus the token
	// being reserved, and the longest window on this key is two minutes, so
	// twice that is the most the first reservation can legitimately want. The
	// hard cap is far above this, which is the point: the first wait has to be
	// bounded by the balance itself rather than clipped by the cap.
	start := clock.Total()
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	first := clock.Total() - start
	if first <= 0 {
		t.Fatal("an over-spent window produced no wait at all: the count header was discarded rather than bounded")
	}
	if first > 2*2*time.Minute {
		t.Fatalf("the first reservation slept %s after a -Count header of a billion against a limit of 100, want one window of debt",
			first)
	}

	// And every wait stays under the hard cap, whatever produced it.
	for i := 2; i <= 3; i++ {
		before := clock.Total()
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait %d: %v", i, err)
		}
		if got := clock.Total() - before; got > maxWait {
			t.Fatalf("Wait %d slept %s, want at most %s", i, got, maxWait)
		}
	}
}

// TestLimiterWaitIsCappedForAnAbsurdButSaneWindow exercises the cap as the
// binding bound. A one-request-per-day window parses as a perfectly sane period
// and is kept rather than dropped - dropping an advertised window makes the
// crawl faster, not slower - so the cap is what has to keep the pause finite.
func TestLimiterWaitIsCappedForAnAbsurdButSaneWindow(t *testing.T) {
	clock := NewFakeClock(testStart)
	l := NewLimiter(LimiterOptions{Clock: clock, Ceiling: DevelopmentKeyWindows(), Bootstrap: DevelopmentKeyWindows()})
	l.Observe(rateLimitHeaders{AppLimit: []Window{{Limit: 1, Period: 24 * time.Hour}}, HasApplication: true})

	// The first request spends the day's single token and need not wait.
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	before := clock.Total()
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("second Wait: %v", err)
	}
	if got := clock.Total() - before; got != maxWait {
		t.Fatalf("a one-per-day window made Wait sleep %s, want the %s cap", got, maxWait)
	}
}

// TestHeaderDurationsCannotOverflow covers the second unbounded-input defect:
// the seconds field of every `a:b` pair arrives from strconv.Atoi, so
// `time.Duration(seconds) * time.Second` overflows int64 above about 9.2e9. The
// wrap-around is not reliably negative - 18446744074 seconds lands back inside
// int64 as a 290ms window, i.e. a window advertised as one request per ten
// billion seconds becomes 3.4 requests per second - so a sign test is not
// enough and the pair has to be rejected outright.
func TestHeaderDurationsCannotOverflow(t *testing.T) {
	t.Run("negative period wrap-around is dropped", func(t *testing.T) {
		if got := ParseWindows("1:9223372037"); len(got) != 0 {
			t.Fatalf("ParseWindows(%q) = %v, want the pair dropped: %s is not a period any multiply produced honestly",
				"1:9223372037", got, got[0].Period)
		}
	})

	t.Run("positive period wrap-around is dropped", func(t *testing.T) {
		if got := ParseWindows("1:18446744074"); len(got) != 0 {
			t.Fatalf("ParseWindows(%q) = %v, want the pair dropped: it wraps to a %s window, %.1f requests/s",
				"1:18446744074", got, got[0].Period, float64(got[0].Limit)/got[0].Period.Seconds())
		}
	})

	t.Run("count header period wrap-around is dropped", func(t *testing.T) {
		if got := parseCounts("1:18446744074"); len(got) != 0 {
			t.Fatalf("parseCounts(%q) = %v, want nil: a wrapped period must not become a map key", "1:18446744074", got)
		}
	})

	t.Run("retry-after wrap-around is zero", func(t *testing.T) {
		if got := ParseRetryAfter("9223372037", testStart); got != 0 {
			t.Fatalf("ParseRetryAfter(%q) = %s, want 0 because a delay int64 cannot hold in nanoseconds is unreadable",
				"9223372037", got)
		}
	})

	t.Run("a real production advertisement still parses", func(t *testing.T) {
		want := []Window{{Limit: 3000, Period: 10 * time.Second}, {Limit: 180000, Period: 600 * time.Second}}
		got := ParseWindows("3000:10,180000:600")
		if len(got) != len(want) {
			t.Fatalf("ParseWindows(%q) = %v, want %v", "3000:10,180000:600", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("ParseWindows(%q)[%d] = %v, want %v", "3000:10,180000:600", i, got[i], want[i])
			}
		}
	})
}
