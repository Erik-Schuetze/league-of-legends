package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// A database that is not up yet is a startup condition, not a failure.
//
// Open pings before it hands back a store, and that ping stays: a process that
// logs "ready" and only then discovers that its database is wrong is a process
// whose readiness signal lied. What the ping must not be is a single attempt
// whose failure is fatal. The store and its callers come up together after any
// cluster event - a node reboot, a volume reattach, a Postgres pod being
// rescheduled - and in that window the first dial is refused. Failing there
// makes the process's recovery depend on the kubelet restarting a crashed
// container, which is a crash loop with backoff and not a retry: it is the
// reason `store: connect: ... connection refused` appears as the last line of a
// dead pod rather than as a line of a startup log, and it costs the pipeline
// the whole backoff after every cluster event.
//
// The retry is bounded, because a dependency that never arrives must still fail
// the job loudly. A process that waits forever holds its slot and tells nobody
// anything, which is worse than the crash loop it replaced.

const (
	// defaultConnectWindow bounds the startup connect when Options leaves
	// ConnectWindow zero. Order of a minute: long enough to cover a Postgres
	// pod coming back and its volume reattaching, short enough that the ingest
	// container is still inside the startup-probe budget
	// deploy/base/ingest/deployment.yaml gives it - the two numbers are a
	// pair, and that file says so.
	defaultConnectWindow = 60 * time.Second

	// connectRetryBase and connectRetryMax bound the wait between two
	// attempts. Half a second catches a database that was a moment late on the
	// second attempt rather than the tenth; the cap keeps the last sleep
	// before the deadline from being a long one that overshoots it.
	connectRetryBase = 500 * time.Millisecond
	connectRetryMax  = 8 * time.Second
)

// connectPolicy is the startup connect's retry policy with its defaults
// resolved. It is kept apart from the pool so that a test can drive it with a
// ping that fails on demand and with waits it does not have to spend.
type connectPolicy struct {
	// window bounds the whole retry. A zero Options.ConnectWindow has already
	// become defaultConnectWindow by the time this exists; a negative one is
	// the caller asking for no retry at all, and is passed through.
	window time.Duration
	// timeout is the ceiling for one ping. It is clamped to what is left of
	// the window, so an attempt cannot outlive the deadline it belongs to.
	timeout time.Duration
	base    time.Duration
	max     time.Duration
	log     *slog.Logger
}

func connectPolicyFrom(opts Options) connectPolicy {
	p := connectPolicy{
		window:  opts.ConnectWindow,
		timeout: opts.ConnTimeout,
		base:    connectRetryBase,
		max:     connectRetryMax,
		log:     opts.Logger,
	}
	if p.window == 0 {
		p.window = defaultConnectWindow
	}
	if p.timeout <= 0 {
		p.timeout = defaultConnTimeout
	}
	if p.base <= 0 {
		p.base = connectRetryBase
	}
	if p.max < p.base {
		p.max = p.base
	}
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	return p
}

// run pings until the database answers, the window is spent, or ctx ends.
//
// Every attempt that fails is logged with its number and the wait that follows
// it, and the attempt that finally succeeds says how many it took. Giving up is
// logged at Error level as well as returned, because the return value is a
// caller's error to print and this is the line an operator reading a dead pod's
// log needs to see first.
func (p connectPolicy) run(ctx context.Context, ping func(context.Context) error) error {
	if p.window < 0 {
		// Opting out keeps the historical behaviour exactly: one attempt, its
		// own error, no waiting and no log line.
		return fmt.Errorf("store: connect: %w", pingWithin(ctx, ping, p.timeout))
	}

	started := time.Now()
	deadline := started.Add(p.window)
	wait := p.base

	for attempt := 1; ; attempt++ {
		timeout := p.timeout
		if left := time.Until(deadline); left < timeout {
			timeout = left
		}
		err := pingWithin(ctx, ping, timeout)
		if err == nil {
			if attempt > 1 {
				p.log.Info("store: database answered; startup continues",
					"attempt", attempt, "waited", elapsed(started))
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stoppedError(elapsed(started), attempt, err, ctxErr)
		}
		left := time.Until(deadline)
		if left <= 0 {
			p.log.Error("store: giving up: the database has not answered within the startup window",
				"attempts", attempt, "window", p.window.String(), "err", err)
			return fmt.Errorf("store: connect: gave up after %s and %d attempts: %w",
				elapsed(started), attempt, err)
		}
		sleep := wait
		if sleep > left {
			sleep = left
		}
		p.log.Warn("store: database is not up yet; retrying",
			"attempt", attempt, "wait", sleep.Round(time.Millisecond).String(), "err", err)

		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return stoppedError(elapsed(started), attempt, err, ctx.Err())
		case <-timer.C:
		}
		wait *= 2
		if wait > p.max {
			wait = p.max
		}
	}
}

// pingWithin gives one attempt its own deadline, so a connection that is
// accepted and then never answers is bounded by ConnTimeout rather than by the
// operating system's TCP timeout.
func pingWithin(ctx context.Context, ping func(context.Context) error, timeout time.Duration) error {
	if timeout <= 0 {
		// Only reachable when the window expired between two attempts, and it
		// must still make an attempt: the ping reports the cancellation, which
		// is the honest error, instead of this function inventing one.
		timeout = time.Millisecond
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return ping(pingCtx)
}

func elapsed(started time.Time) string {
	return time.Since(started).Round(time.Millisecond).String()
}

// stoppedError reports a startup that was told to stop rather than one that ran
// out of window. Both errors are wrapped - Go allows more than one %w - so a
// caller can tell "this process was asked to exit" from "the database never
// answered" with errors.Is, and an operator reading the line gets the reason and
// the last refusal in the order they happened.
func stoppedError(spent string, attempts int, cause, ctxErr error) error {
	return fmt.Errorf("store: connect: gave up after %s and %d attempts, startup stopped: %w: %w",
		spent, attempts, ctxErr, cause)
}
