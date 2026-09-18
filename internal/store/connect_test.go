package store

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// The startup connect is the one place where "the database is not answering"
// has to be told apart from "the database is wrong". These tests drive the
// policy with a ping that fails on demand, so they can check the two halves that
// matter - that a late database is waited for, and that a database that never
// arrives is given up on inside the window instead of hanging - without waiting
// on a real socket. The last two run Open itself against a port that cannot be
// listening, which is the shape the production failure had.

var errRefused = errors.New("store: ping: dial tcp 10.43.91.134:5432: connect: connection refused")

// logLine is one record a test can assert on. The retry's observable behaviour
// besides its error is the log, so "retried and said so" is checked rather than
// inferred from the elapsed time.
type logLine struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

type captureHandler struct {
	lines []logLine
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.lines = append(h.lines, logLine{level: r.Level, msg: r.Message, attrs: attrs})
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) at(level slog.Level) []logLine {
	var out []logLine
	for _, l := range h.lines {
		if l.level == level {
			out = append(out, l)
		}
	}
	return out
}

func testPolicy(h *captureHandler, window time.Duration) connectPolicy {
	return connectPolicy{
		window:  window,
		timeout: 50 * time.Millisecond,
		base:    40 * time.Millisecond,
		max:     80 * time.Millisecond,
		log:     slog.New(h),
	}
}

func TestConnectPolicyDefaultsToABoundedWindow(t *testing.T) {
	p := connectPolicyFrom(Options{})
	if p.window != defaultConnectWindow {
		t.Fatalf("window = %s, want %s", p.window, defaultConnectWindow)
	}
	if p.timeout != defaultConnTimeout {
		t.Fatalf("timeout = %s, want %s", p.timeout, defaultConnTimeout)
	}
	if p.base != connectRetryBase || p.max != connectRetryMax {
		t.Fatalf("backoff = %s..%s, want %s..%s", p.base, p.max, connectRetryBase, connectRetryMax)
	}
	if p.log == nil {
		t.Fatal("log = nil, want a logger that discards rather than one that panics")
	}
	// A zero window is the default, not an opt-out: a caller that sets nothing
	// gets the retry, which is the whole point of the change.
	if p := connectPolicyFrom(Options{ConnectWindow: -1}); p.window != -1 {
		t.Fatalf("window = %s, want the opt-out passed through", p.window)
	}
}

func TestConnectRetriesUntilTheDatabaseAnswers(t *testing.T) {
	h := &captureHandler{}
	p := testPolicy(h, 5*time.Second)

	calls := 0
	ping := func(context.Context) error {
		calls++
		if calls < 3 {
			return errRefused
		}
		return nil
	}
	if err := p.run(context.Background(), ping); err != nil {
		t.Fatalf("run = %v, want nil once the database answers", err)
	}
	if calls != 3 {
		t.Fatalf("ping calls = %d, want 3", calls)
	}

	retries := h.at(slog.LevelWarn)
	if len(retries) != 2 {
		t.Fatalf("retry lines = %d, want one per failed attempt: %+v", len(retries), h.lines)
	}
	for i, line := range retries {
		if got := line.attrs["attempt"]; got != int64(i+1) {
			t.Fatalf("retry %d logged attempt = %v, want %d", i, got, i+1)
		}
		if _, ok := line.attrs["wait"].(string); !ok {
			t.Fatalf("retry %d logged wait = %v, want the wait as a duration string", i, line.attrs["wait"])
		}
		if !errors.Is(line.attrs["err"].(error), errRefused) {
			t.Fatalf("retry %d logged err = %v, want the connection failure", i, line.attrs["err"])
		}
	}
	done := h.at(slog.LevelInfo)
	if len(done) != 1 || done[0].attrs["attempt"] != int64(3) {
		t.Fatalf("info lines = %+v, want one line saying the third attempt answered", done)
	}
}

func TestConnectGivesUpWithinItsWindow(t *testing.T) {
	h := &captureHandler{}
	const window = 250 * time.Millisecond
	p := testPolicy(h, window)

	calls := 0
	ping := func(context.Context) error {
		calls++
		return errRefused
	}

	started := time.Now()
	err := p.run(context.Background(), ping)
	spent := time.Since(started)

	if err == nil {
		t.Fatal("run succeeded against a database that never answers")
	}
	if !strings.Contains(err.Error(), "store: connect:") {
		t.Fatalf("err = %v, want it to keep the store: connect: prefix", err)
	}
	if !strings.Contains(err.Error(), "gave up after") {
		t.Fatalf("err = %v, want it to say it gave up rather than to look like a single refusal", err)
	}
	if !errors.Is(err, errRefused) {
		t.Fatalf("err = %v, want the last connection failure wrapped", err)
	}
	if calls < 2 {
		t.Fatalf("ping calls = %d, want a retry rather than a single attempt", calls)
	}
	if spent < window {
		t.Fatalf("gave up after %s, before the %s window was spent", spent, window)
	}
	if spent > window+2*time.Second {
		t.Fatalf("gave up after %s, want the %s window to bound it", spent, window)
	}

	// The loud half: a dead pod's log has to say the window was spent, and
	// how many attempts went into it.
	final := h.at(slog.LevelError)
	if len(final) != 1 {
		t.Fatalf("error lines = %+v, want exactly one", final)
	}
	if final[0].attrs["window"] != window.String() {
		t.Fatalf("error logged window = %v, want %s", final[0].attrs["window"], window)
	}
	if final[0].attrs["attempts"] == nil {
		t.Fatalf("error logged %+v, want the attempt count", final[0].attrs)
	}
}

func TestConnectStopsPromptlyWhenTheStartupIsStopped(t *testing.T) {
	h := &captureHandler{}
	p := testPolicy(h, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	ping := func(context.Context) error {
		calls++
		if calls == 2 {
			// The kubelet sent SIGTERM while the retry was waiting.
			cancel()
		}
		return errRefused
	}

	started := time.Now()
	err := p.run(ctx, ping)
	spent := time.Since(started)

	if err == nil {
		t.Fatal("run succeeded with a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation reported", err)
	}
	if !strings.Contains(err.Error(), "startup stopped") {
		t.Fatalf("err = %v, want it to name the interrupted startup", err)
	}
	// An hour-long window must not mean an hour-long shutdown.
	if spent > 30*time.Second {
		t.Fatalf("returned after %s, want a cancelled startup to return at once", spent)
	}
}

func TestConnectWithoutARetryReportsTheFirstFailure(t *testing.T) {
	h := &captureHandler{}
	p := testPolicy(h, -1)

	calls := 0
	ping := func(context.Context) error {
		calls++
		return errRefused
	}
	err := p.run(context.Background(), ping)
	if err == nil {
		t.Fatal("run succeeded against a database that never answers")
	}
	if calls != 1 {
		t.Fatalf("ping calls = %d, want the opt-out to make exactly one", calls)
	}
	if got, want := err.Error(), "store: connect: "+errRefused.Error(); got != want {
		t.Fatalf("err = %q, want %q: the opt-out keeps the historical error", got, want)
	}
	if len(h.lines) != 0 {
		t.Fatalf("logged %+v, want nothing: the caller's error is the report", h.lines)
	}
}

// TestOpenGivesUpWithinTheConnectWindow is the same failure the cluster showed,
// driven through Open against a port that cannot be listening. It is the half a
// fake ping cannot cover: the real driver, the real refused dial, and the
// per-attempt timeout coming from Options.
func TestOpenGivesUpWithinTheConnectWindow(t *testing.T) {
	h := &captureHandler{}
	const window = 300 * time.Millisecond

	started := time.Now()
	s, err := Open(context.Background(), Options{
		DSN:           "postgres://nobody@127.0.0.1:1/nothing?sslmode=disable&connect_timeout=1",
		ConnTimeout:   2 * time.Second,
		ConnectWindow: window,
		Logger:        slog.New(h),
	})
	spent := time.Since(started)
	if err == nil {
		_ = s.Close()
		t.Fatal("Open succeeded against a port that cannot be listening")
	}
	if !strings.Contains(err.Error(), "store: connect:") {
		t.Fatalf("err = %v, want it to keep the store: connect: prefix", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want the driver's refusal in it", err)
	}
	if len(h.at(slog.LevelWarn)) < 1 {
		t.Fatalf("logged %+v, want at least one retry line", h.lines)
	}
	if spent < window {
		t.Fatalf("Open returned after %s, before the %s window was spent", spent, window)
	}
	if spent > window+2*time.Second {
		t.Fatalf("Open returned after %s, want the %s window to bound it", spent, window)
	}
}

func TestOpenWithoutARetryReportsTheFirstPingFailure(t *testing.T) {
	started := time.Now()
	s, err := Open(context.Background(), Options{
		DSN:           "postgres://nobody@127.0.0.1:1/nothing?sslmode=disable&connect_timeout=1",
		ConnTimeout:   2 * time.Second,
		ConnectWindow: -1,
	})
	spent := time.Since(started)
	if err == nil {
		_ = s.Close()
		t.Fatal("Open succeeded against a port that cannot be listening")
	}
	if strings.Contains(err.Error(), "gave up after") {
		t.Fatalf("err = %v, want the single-attempt form when the retry is opted out", err)
	}
	if spent > time.Second {
		t.Fatalf("Open returned after %s, want the opt-out to fail on the first attempt", spent)
	}
}
