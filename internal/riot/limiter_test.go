package riot

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"
)

var testStart = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func TestParseWindows(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   []Window
	}{
		{name: "empty", header: "", want: nil},
		{name: "whitespace", header: "   ", want: nil},
		{
			name:   "development key",
			header: "20:1,100:120",
			want:   []Window{{Limit: 20, Period: time.Second}, {Limit: 100, Period: 2 * time.Minute}},
		},
		{
			name:   "spaces around pairs",
			header: " 20:1 , 100:120 ",
			want:   []Window{{Limit: 20, Period: time.Second}, {Limit: 100, Period: 2 * time.Minute}},
		},
		{
			name:   "unparseable pairs are dropped, readable ones kept",
			header: "nonsense,20:1,:,0:1,20:0",
			want:   []Window{{Limit: 20, Period: time.Second}},
		},
		{name: "no colon", header: "20", want: []Window{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseWindows(tc.header)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseWindows(%q) = %v, want %v", tc.header, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ParseWindows(%q)[%d] = %v, want %v", tc.header, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseCounts(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   map[time.Duration]int
	}{
		{name: "empty", header: "", want: nil},
		{name: "zero is a legal count", header: "0:1,0:120", want: map[time.Duration]int{time.Second: 0, 2 * time.Minute: 0}},
		{name: "spent window", header: "100:120", want: map[time.Duration]int{2 * time.Minute: 100}},
		{name: "negative dropped", header: "-1:1,3:2", want: map[time.Duration]int{2 * time.Second: 3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCounts(tc.header)
			if len(got) != len(tc.want) {
				t.Fatalf("parseCounts(%q) = %v, want %v", tc.header, got, tc.want)
			}
			for period, want := range tc.want {
				if got[period] != want {
					t.Fatalf("parseCounts(%q)[%s] = %d, want %d", tc.header, period, got[period], want)
				}
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := testStart
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "absent", header: "", want: 0},
		{name: "seconds", header: "2", want: 2 * time.Second},
		{name: "zero seconds", header: "0", want: 0},
		{name: "http date", header: now.Add(30 * time.Second).Format(http.TimeFormat), want: 30 * time.Second},
		{name: "http date in the past", header: now.Add(-time.Minute).Format(http.TimeFormat), want: 0},
		{name: "garbage", header: "soon", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseRetryAfter(tc.header, now); got != tc.want {
				t.Fatalf("ParseRetryAfter(%q) = %s, want %s", tc.header, got, tc.want)
			}
		})
	}
}

func TestConfigWindows(t *testing.T) {
	tests := []struct {
		name      string
		perSecond float64
		per2Min   int
		want      []Window
	}{
		{
			name: "development key", perSecond: 18, per2Min: 95,
			want: []Window{{Limit: 18, Period: time.Second}, {Limit: 95, Period: 2 * time.Minute}},
		},
		{
			name: "sub-second rate has no whole-number window", perSecond: 0.5, per2Min: 95,
			want: []Window{{Limit: 95, Period: 2 * time.Minute}},
		},
		{
			name: "nothing usable falls back to the documented defaults", perSecond: 0, per2Min: 0,
			want: DevelopmentKeyWindows(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ConfigWindows(tc.perSecond, tc.per2Min)
			if len(got) != len(tc.want) {
				t.Fatalf("ConfigWindows(%v, %d) = %v, want %v", tc.perSecond, tc.per2Min, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ConfigWindows(%v, %d)[%d] = %v, want %v", tc.perSecond, tc.per2Min, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestLimiterEffectiveRateFollowsHeaders(t *testing.T) {
	ceiling := []Window{{Limit: 20, Period: time.Second}, {Limit: 1000, Period: 2 * time.Minute}}
	tests := []struct {
		name   string
		header rateLimitHeaders
		want   float64
	}{
		{
			name: "app window binds",
			header: rateLimitHeaders{
				AppLimit:       []Window{{Limit: 2, Period: time.Second}},
				HasApplication: true,
			},
			want: 2,
		},
		{
			name: "two-minute window binds, as it does on a development key",
			header: rateLimitHeaders{
				AppLimit:       []Window{{Limit: 20, Period: time.Second}, {Limit: 100, Period: 2 * time.Minute}},
				HasApplication: true,
			},
			want: 100.0 / 120.0,
		},
		{
			name: "method window is honoured alongside the app window",
			header: rateLimitHeaders{
				AppLimit:       []Window{{Limit: 20, Period: time.Second}},
				HasApplication: true,
				MethodLimit:    []Window{{Limit: 5, Period: time.Second}},
				HasMethod:      true,
			},
			want: 5,
		},
		{
			name: "an advert larger than the ceiling is clamped",
			header: rateLimitHeaders{
				AppLimit:       []Window{{Limit: 100000, Period: time.Second}},
				HasApplication: true,
			},
			want: 20,
		},
		{
			name: "an unknown period is trusted as advertised",
			header: rateLimitHeaders{
				AppLimit:       []Window{{Limit: 10, Period: 10 * time.Second}},
				HasApplication: true,
			},
			want: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := NewFakeClock(testStart)
			l := NewLimiter(LimiterOptions{Clock: clock, Ceiling: ceiling, Bootstrap: []Window{{Limit: 1, Period: time.Second}}})
			if got := l.EffectiveRate(); got != 1 {
				t.Fatalf("bootstrap rate = %v, want 1", got)
			}
			l.Observe(tc.header)
			if !l.Advertised() {
				t.Fatal("limiter did not record that Riot advertised a limit")
			}
			got := l.EffectiveRate()
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("EffectiveRate() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLimiterObserveKeepsKnowledgeForScopesRiotDidNotMention(t *testing.T) {
	clock := NewFakeClock(testStart)
	l := NewLimiter(LimiterOptions{Clock: clock, Ceiling: []Window{{Limit: 20, Period: time.Second}}})
	l.Observe(rateLimitHeaders{
		AppLimit:       []Window{{Limit: 20, Period: time.Second}},
		HasApplication: true,
		MethodLimit:    []Window{{Limit: 5, Period: time.Second}},
		HasMethod:      true,
	})
	if got := l.EffectiveRate(); got != 5 {
		t.Fatalf("EffectiveRate() = %v, want 5", got)
	}
	// A response that carries only app headers must not forget the method
	// window, and vice versa: dropping the tighter of the two would spend a
	// key that is not entitled to the spend.
	l.Observe(rateLimitHeaders{AppLimit: []Window{{Limit: 20, Period: time.Second}}, HasApplication: true})
	if got := l.EffectiveRate(); got != 5 {
		t.Fatalf("after an app-only response EffectiveRate() = %v, want 5", got)
	}
}

func TestLimiterSpendsReportedCount(t *testing.T) {
	clock := NewFakeClock(testStart)
	l := NewLimiter(LimiterOptions{
		Clock:     clock,
		Ceiling:   []Window{{Limit: 20, Period: time.Second}, {Limit: 100, Period: 2 * time.Minute}},
		Bootstrap: []Window{{Limit: 100, Period: 2 * time.Minute}},
	})
	// Riot says the two-minute window is already fully spent. The next
	// request must wait out a whole window rather than assume the tokens are
	// there because the bucket was freshly built.
	l.Observe(rateLimitHeaders{
		AppLimit:       []Window{{Limit: 20, Period: time.Second}, {Limit: 100, Period: 2 * time.Minute}},
		AppCount:       map[time.Duration]int{2 * time.Minute: 100},
		HasApplication: true,
	})
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := clock.Total(); got < 1200*time.Millisecond {
		t.Fatalf("waited %s for a spent window, want at least 1.2s", got)
	}
}

func TestLimiterPenalizeSuspendsAllCallers(t *testing.T) {
	tests := []struct {
		name      string
		penalty   time.Duration
		wantSleep time.Duration
	}{
		{name: "Retry-After is honoured", penalty: 30 * time.Second, wantSleep: 30 * time.Second},
		{name: "over-long penalty is capped", penalty: time.Hour, wantSleep: defaultMaxRetryAfter},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := NewFakeClock(testStart)
			l := NewLimiter(LimiterOptions{
				Clock:     clock,
				Ceiling:   []Window{{Limit: 1000, Period: time.Second}},
				Bootstrap: []Window{{Limit: 1000, Period: time.Second}},
			})
			l.Penalize(tc.penalty)
			if got := l.Blocked(); got != tc.wantSleep {
				t.Fatalf("Blocked() = %s, want %s", got, tc.wantSleep)
			}
			if err := l.Wait(context.Background()); err != nil {
				t.Fatalf("Wait: %v", err)
			}
			if got := clock.Total(); got != tc.wantSleep {
				t.Fatalf("waited %s, want %s", got, tc.wantSleep)
			}
			if got := l.Blocked(); got != 0 {
				t.Fatalf("Blocked() after the penalty = %s, want 0", got)
			}
		})
	}
}

func TestLimiterWaitHonoursContext(t *testing.T) {
	clock := NewFakeClock(testStart)
	l := NewLimiter(LimiterOptions{Clock: clock, Ceiling: []Window{{Limit: 1, Period: time.Minute}}, Bootstrap: []Window{{Limit: 1, Period: time.Minute}}})
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := l.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait on a cancelled context = %v, want context.Canceled", err)
	}
}
