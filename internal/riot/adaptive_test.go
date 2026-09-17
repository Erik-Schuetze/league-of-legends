package riot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The adaptive-rate proof.
//
// The client must derive its request rate from the headers Riot sends, never
// from a constant compiled into it. These tests drive a fake Riot server whose
// advertised limits change mid-run and measure the rate the client actually
// runs at, in a virtual clock so the measurement is exact and costs no wall
// time.
//
// The fake server reports the count headers the way Riot does: the number of
// requests it has seen inside each window. A server that always claimed nothing
// was spent would let the limiter refill to full on every response, which would
// hide the very behaviour under test.

// phase is one advertised regime of the fake server.
type phase struct {
	name    string
	advert  string
	perSec  float64
	request int
}

// countHeader renders the `used:period` count the fake server reports for the
// one-second window it advertises: how many requests it has seen in the last
// second of virtual time, including the one being answered.
func (a *arrivals) countInWindow(window time.Duration) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.at) == 0 {
		return 0
	}
	cutoff := a.at[len(a.at)-1].Add(-window)
	count := 0
	for _, at := range a.at {
		if !at.Before(cutoff) {
			count++
		}
	}
	return count
}

func TestClientRateFollowsAdvertisedHeaders(t *testing.T) {
	phases := []phase{
		{name: "advertised four per second", advert: "4:1", perSec: 4, request: 20},
		{name: "advertised sixteen per second", advert: "16:1", perSec: 16, request: 24},
		{name: "advertised two per second", advert: "2:1", perSec: 2, request: 14},
	}

	clock := NewFakeClock(testStart)
	seen := &arrivals{clock: clock}

	// The server switches regime by request ordinal, so the client learns about
	// each change from a response in the middle of a run.
	ordinal := 0
	bounds := make([]int, 0, len(phases))
	for _, p := range phases {
		ordinal += p.request
		bounds = append(bounds, ordinal)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.mark()
		n := seen.count()
		which := 0
		for which < len(phases)-1 && n > bounds[which] {
			which++
		}
		setRateHeaders(w, phases[which].advert, fmt.Sprintf("%d:1", seen.countInWindow(time.Second)), "", "")
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	// The ceiling is deliberately loose: it is a safety cap, and this test is
	// about what the advertised window does below it.
	ceiling := ConfigWindows(20, 4800)
	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.Limiter = NewLimiter(LimiterOptions{Clock: clock, Ceiling: ceiling, Bootstrap: ceiling})
		o.MaxAttempts = 1
	})

	means := make([]time.Duration, 0, len(phases))
	rates := make([]float64, 0, len(phases))
	for _, p := range phases {
		for i := 0; i < p.request; i++ {
			if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "fixture-puuid-01", Count: 20}); err != nil {
				t.Fatalf("%s: MatchIDs: %v", p.name, err)
			}
		}
		// The advertised headers say what has been spent in the current
		// window, so each response re-shapes the buckets. Skip the first few
		// requests of a regime and measure the tail, where the client has
		// settled onto the new limit rather than bursting into it.
		means = append(means, seen.tailMeanGap(8, 6))
		rates = append(rates, client.Limiter().EffectiveRate())
	}

	for i, p := range phases {
		t.Logf("%s: advertised %.0f/s, limiter reports %.4f/s, observed mean gap %s",
			p.name, p.perSec, rates[i], means[i])
	}

	// The limiter's own view must equal what Riot advertised, to within the
	// ceiling. Nothing here is a constant: the three values differ.
	for i, p := range phases {
		if rates[i] != p.perSec {
			t.Fatalf("%s: limiter effective rate = %v, want %v", p.name, rates[i], p.perSec)
		}
	}

	// The observed pacing has to follow. A client with a hardcoded rate could
	// not satisfy all three of these bounds at once, because the bounds move
	// in opposite directions with the advertised limit.
	if means[0] < 100*time.Millisecond || means[0] > 800*time.Millisecond {
		t.Fatalf("at 4/s the mean gap was %s, want a pace of roughly 250ms per request", means[0])
	}
	if means[1] >= means[0] {
		t.Fatalf("raising the advert to 16/s did not speed the client up: %s then %s", means[0], means[1])
	}
	if means[1] < 20*time.Millisecond || means[1] > 400*time.Millisecond {
		t.Fatalf("at 16/s the mean gap was %s, want a pace of roughly 62ms per request", means[1])
	}
	if means[2] <= means[0] {
		t.Fatalf("lowering the advert to 2/s did not slow the client down: %s then %s", means[0], means[2])
	}
	if means[2] < 250*time.Millisecond {
		t.Fatalf("at 2/s the mean gap was %s, want at least 250ms per request", means[2])
	}
}

// tailMeanGap is the mean spacing between consecutive requests over the last n
// gaps, starting after skip requests. It is the observed request rate over that
// stretch.
func (a *arrivals) tailMeanGap(skip, n int) time.Duration {
	gaps := a.gaps()
	if len(gaps) < skip+n {
		return 0
	}
	window := gaps[len(gaps)-n:]
	var total time.Duration
	for _, gap := range window {
		total += gap
	}
	return total / time.Duration(len(window))
}

// TestClientNeverExceedsTheCeilingEvenIfRiotAdvertisesMore drives the client
// with advertisements whose window periods are not the ones the ceiling
// configures. The ceiling used here is the documented development-key one, and
// the periods in the fixtures are the ones Riot production keys actually send,
// so the mismatched case is the normal case, not an exotic one.
func TestClientNeverExceedsTheCeilingEvenIfRiotAdvertisesMore(t *testing.T) {
	tests := []struct {
		name   string
		advert string
		count  string
	}{
		{
			// The lucky case: the periods happen to be the ceiling's own.
			name:   "periods that match the ceiling",
			advert: "100000:1,100000:120",
			count:  "1:1,1:120",
		},
		{
			// A shared, stale or misread header claiming a huge allowance,
			// in the shape a production application limit has. Before the
			// fix this made the effective rate 300/s - neither period
			// matched the ceiling, so no window was clamped: 3,300 tokens
			// could be spent in the first second of a key whose
			// documented ceiling is 18/s.
			name:   "the periods a production key advertises",
			advert: "3000:10,180000:600",
			count:  "1:10,1:600",
		},
		{
			// A window shape nobody has ever sent. An unfamiliar period
			// may slow the limiter down; it may not speed it up.
			name:   "a period nobody has ever seen",
			advert: "5000:45",
			count:  "1:45",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := NewFakeClock(testStart)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				setRateHeaders(w, tc.advert, tc.count, "", "")
				writeBody(w, `["EUW1_0000000000"]`)
			}))
			defer srv.Close()

			ceiling := ConfigWindows(18, 95)
			client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
				o.Limiter = NewLimiter(LimiterOptions{Clock: clock, Ceiling: ceiling, Bootstrap: ceiling})
				o.MaxAttempts = 1
			})
			if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p", Count: 1}); err != nil {
				t.Fatalf("MatchIDs: %v", err)
			}
			// The advertised two minutes is worth 180000 requests, the
			// ceiling's two minutes is worth 95, and the 95/120 window is
			// what binds - whatever periods the advertisement mentions.
			want := 95.0 / 120.0
			if got := client.Limiter().EffectiveRate(); got != want {
				t.Fatalf("advertised %s: effective rate = %v, want %v clamped by the ceiling", tc.advert, got, want)
			}
		})
	}
}

// TestClientBurstIsCappedByTheCeilingWhateverPeriodsRiotAdvertises is the same
// promise measured in the request path rather than in the limiter's own view: a
// ceiling of three requests per minute must not be outspent because the server
// advertised a period the ceiling does not configure.
func TestClientBurstIsCappedByTheCeilingWhateverPeriodsRiotAdvertises(t *testing.T) {
	clock := NewFakeClock(testStart)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, "3000:10,180000:600", "1:10,1:600", "", "")
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	ceiling := []Window{{Limit: 3, Period: time.Minute}}
	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.Limiter = NewLimiter(LimiterOptions{Clock: clock, Ceiling: ceiling, Bootstrap: ceiling})
		o.MaxAttempts = 1
	})

	for i := 0; i < 3; i++ {
		if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p", Count: 1}); err != nil {
			t.Fatalf("MatchIDs %d: %v", i, err)
		}
	}
	if got := clock.Total(); got != 0 {
		t.Fatalf("the first three requests are inside the ceiling's window, but waited %s", got)
	}
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p", Count: 1}); err != nil {
		t.Fatalf("MatchIDs: %v", err)
	}
	// The fourth has to wait for the one minute window of three to refill one
	// token: 20 seconds. Only the advertisement's 300/s would let it through
	// immediately.
	if got := clock.Total(); got < 20*time.Second {
		t.Fatalf("the fourth request waited %s, want at least 20s for the ceiling's window to refill", got)
	}
}
