// Command lolstats-aggregate reads the raw Riot archive and writes the agg/v1
// artifacts that the static site serves. It never talks to the Riot API: the
// archive is the only input, which is what makes a rebuild reproducible from a
// pinned image digest.
//
// Subcommands:
//
//	build     derive and publish the artifacts for a patch, region, queue and bracket
//	verify    check published artifacts against the agg/v1 JSON Schema and the gate rules
//	manifest  rewrite agg/v1/manifest.json from the published tree
//	demo      write a deterministic simulated artifact set, labelled as demo data
//
// The raw archive is read by a pinned DuckDB process rather than by a linked
// library: no DuckDB Go binding builds with CGO_ENABLED=0. This binary is
// static, but the DuckDB CLI it execs is a glibc binary, which is why the
// runtime image is the `cc` distroless base rather than `static`. See
// docs/aggregation.md and ADR-007.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggregate"
	"github.com/Erik-Schuetze/league-of-legends/internal/config"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/store"
)

// Exit codes. 1 is "the build failed and nothing was published", 2 is "the
// command line was wrong". They are distinct because the CronJob backoff policy
// and a human reading `kubectl describe` want to tell a bad flag from bad data.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = `lolstats-aggregate - derives the agg/v1 artifacts from the raw archive

usage: lolstats-aggregate <subcommand> [flags]

subcommands:
  build     derive and publish the artifacts for a patch, region, queue and bracket
  verify    check published artifacts against the agg/v1 JSON Schema and the gate rules
  manifest  rewrite agg/v1/manifest.json from the published tree
  demo      write a deterministic simulated artifact set, labelled as demo data

Every subcommand defaults its flags from the same environment the other lolstats
binaries read, so a CronJob stays a one-line invocation.
Run "lolstats-aggregate <subcommand> -h" for the flags of one subcommand.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runEnv(args, stdout, stderr, config.OS)
}

// runEnv is run with an injectable environment so a test can drive a whole
// subcommand without mutating the process environment.
func runEnv(args []string, stdout, stderr io.Writer, getenv config.Getenv) int {
	if len(args) == 0 {
		printUsage(stderr, "%s", usage)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout, "%s", usage)
		return exitOK
	case "build":
		return runBuild(args[1:], stdout, stderr, getenv)
	case "verify":
		return runVerify(args[1:], stdout, stderr, getenv)
	case "manifest":
		return runManifest(args[1:], stdout, stderr, getenv)
	case "demo":
		return runDemo(args[1:], stdout, stderr, getenv)
	default:
		printUsage(stderr, "lolstats-aggregate: unknown subcommand %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

// printUsage is used only for help and argument errors. The write error is
// dropped deliberately: the stream that just failed is the only place the
// failure could be reported, and a missing help message must not change the
// exit code.
func printUsage(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func fail(stderr io.Writer, sub string, err error) int {
	printUsage(stderr, "lolstats-aggregate %s: %v\n", sub, err)
	return exitFailure
}

// environment is everything a subcommand needs from the process it runs in.
type environment struct {
	cfg     config.Config
	log     *slog.Logger
	metrics *obs.Metrics
}

func newEnvironment(getenv config.Getenv) (environment, error) {
	cfg, err := config.LoadFrom(getenv)
	if err != nil {
		return environment{}, fmt.Errorf("configuration: %w", err)
	}
	return environment{cfg: cfg, log: obs.NewLogger(cfg), metrics: obs.NewMetrics()}, nil
}

// signalContext cancels on SIGINT or SIGTERM so a build that is cut short by
// the CronJob's activeDeadlineSeconds still unwinds its staging directory
// instead of leaving half a tree inside the aggregate root.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// serveMetrics starts the scrape endpoint and returns a function that stops it.
//
// A failure to bind is logged and ignored. The build's job is to produce
// artifacts, and a port already taken must not turn a good build into a failed
// one; an address of "" disables the endpoint entirely, which is what the demo
// and the tests want.
func serveMetrics(ctx context.Context, log *slog.Logger, addr string, metrics *obs.Metrics) func() {
	if addr == "" {
		return func() {}
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// ListenConfig rather than net.Listen so the accept loop observes the same
	// context as the build it serves: a metrics endpoint that outlived its
	// process would keep a port bound for the next CronJob.
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		log.Warn("metrics endpoint not started", "addr", addr, "error", err)
		return func() {}
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Warn("metrics endpoint stopped", "addr", addr, "error", serveErr)
		}
	}()
	log.Info("metrics endpoint listening", "addr", addr)
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
}

// segFlags are the four fields every subcommand addresses a partition by.
type segFlags struct {
	patch    string
	region   string
	platform string
	queue    int
	bracket  string
}

func addSegFlags(fs *flag.FlagSet, seg *segFlags, cfg config.Config) {
	fs.StringVar(&seg.patch, "patch", cfg.Aggregate.Patch,
		"patch to address, empty means the newest patch in the window")
	fs.StringVar(&seg.region, "region", cfg.Riot.Region,
		"published region and path element, for example EUW")
	fs.StringVar(&seg.platform, "platform", cfg.Riot.PlatformRoute,
		"Riot platform id the raw archive is matched on, for example EUW1; empty derives it from the region")
	fs.IntVar(&seg.queue, "queue", cfg.Aggregate.QueueID, "queue id, 420 is ranked solo")
	fs.StringVar(&seg.bracket, "bracket", cfg.Aggregate.Bracket,
		"rank bracket, v1 publishes only \"all\"")
}

// gitSHA is the revision recorded in the manifest and the audit row so a
// published number has a commit behind it. The image sets GIT_SHA at build
// time; without it the value says so rather than pretending to be a revision.
func gitSHA(getenv config.Getenv) string {
	for _, key := range []string{"GIT_SHA", "LOLSTATS_GIT_SHA"} {
		if value, ok := getenv(key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}

// auditor opens the build_runs recorder for one run.
//
// A configured database is a requirement rather than an option: the audit row
// is part of the deliverable, and a build that quietly skipped it would publish
// numbers with no record of having done so. With no DSN at all the process is
// running outside the stack - a developer machine, an offline verification - and
// the on-disk breadcrumb is the honest substitute for a table that is not there.
func (e environment) auditor(aggRoot string) (aggregate.Auditor, error) {
	if e.cfg.Postgres.DSN == "" {
		root := filepath.Join(filepath.Dir(filepath.Clean(aggRoot)), "build-runs")
		e.log.Warn("POSTGRES_DSN is not set: recording build runs as files",
			"directory", root)
		return aggregate.FileAuditor{Root: root}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Postgres.ConnTimeout)
	defer cancel()
	opened, err := store.Open(ctx, store.Options{
		DSN:         e.cfg.Postgres.DSN,
		MaxConns:    int(e.cfg.Postgres.MaxConns),
		ConnTimeout: e.cfg.Postgres.ConnTimeout,
		Region:      e.cfg.Riot.Region,
		Metrics:     e.metrics,
	})
	if err != nil {
		return nil, err
	}
	return aggregate.StoreAuditor{Store: opened, Log: e.log}, nil
}

// crawlInputAge reports how old the newest payload in the control plane is.
//
// This is the aggregate's only view of whether the crawl is alive, and it is
// the answer to a question the archive cannot answer: an archive that has not
// grown is not a quiet night, it is a pipeline that stopped. The control plane
// knows, because every fetch writes fetched_at - so the nightly build asks
// before it publishes, and a crawl that stopped (an expired key, a worker
// that will not start) fails the job loudly and leaves the published snapshot
// exactly as it was instead of refreshing it from a frozen archive.
//
// The connection is opened for this one question and closed again. The build's
// own connection is owned by the shared aggregate package as an opaque
// Auditor, and widening that interface would be a change to shared code for a
// flag in this binary; one dial against a local postgres is the cheaper trade.
func (e environment) newestFetchedAt(ctx context.Context) (time.Time, bool, error) {
	if e.cfg.Postgres.DSN == "" {
		return time.Time{}, false, nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, e.cfg.Postgres.ConnTimeout)
	defer cancel()
	opened, err := store.Open(dialCtx, store.Options{
		DSN:         e.cfg.Postgres.DSN,
		MaxConns:    int(e.cfg.Postgres.MaxConns),
		ConnTimeout: e.cfg.Postgres.ConnTimeout,
		Region:      e.cfg.Riot.Region,
		Metrics:     e.metrics,
	})
	if err != nil {
		return time.Time{}, false, err
	}
	defer func() {
		if cerr := opened.Close(); cerr != nil {
			e.log.Warn("closing the crawl-freshness connection failed", "error", cerr)
		}
	}()
	return opened.NewestFetchedAt(ctx)
}

// requireFreshCrawl is the aggregate's half of "never publish a half-crawled
// snapshot as complete".
//
// The build reads the archive, and the archive cannot say whether the crawl
// writing it is still alive: a stopped crawler and a quiet patch look identical
// on disk. The control plane can, so the check is a query against fetched_at
// rather than a scan of the tree, and it fails closed - no DSN is a warning for
// a developer machine, but a DSN that cannot answer, or a table with nothing
// recent in it, stops the build. Publishing last night's cells as this
// morning's snapshot is the one outcome worth stopping for, and "the check
// could not run" is not a reason to publish.
func (e environment) requireFreshCrawl(ctx context.Context, maxAge time.Duration) error {
	if e.cfg.Postgres.DSN == "" {
		e.log.Warn("no POSTGRES_DSN: the crawl freshness check cannot run",
			"crawl_max_age", maxAge.String())
		return nil
	}
	newest, ok, err := e.newestFetchedAt(ctx)
	if err != nil {
		return fmt.Errorf("checking crawl freshness: %w", err)
	}
	if !ok {
		return fmt.Errorf("refusing to publish: the control plane has no fetched payload at all, " +
			"so this build would publish an archive that nothing is feeding")
	}
	age := time.Since(newest)
	e.log.Info("crawl freshness",
		"newest_fetched_at", newest.UTC().Format(time.RFC3339),
		"age", age.Truncate(time.Second).String(),
		"crawl_max_age", maxAge.String())
	if age > maxAge {
		return fmt.Errorf(
			"refusing to publish: the newest crawled payload is %s old (%s), beyond the %s budget: "+
				"the crawl has stopped - check the Riot key and the ingest worker - and the published "+
				"snapshot is left untouched rather than refreshed from a frozen archive",
			age.Truncate(time.Second), newest.UTC().Format(time.RFC3339), maxAge)
	}
	return nil
}

// statusLine is the one-line summary every subcommand prints to stdout. The
// CronJob's log is the only place an operator looks after a failed night, so
// the counts are on the same line as the outcome.
func statusLine(stdout io.Writer, sub, status string, fields ...any) {
	var b strings.Builder
	b.WriteString(sub)
	b.WriteString(": ")
	b.WriteString(status)
	for i := 0; i+1 < len(fields); i += 2 {
		b.WriteString(" ")
		_, _ = fmt.Fprint(&b, fields[i])
		b.WriteString("=")
		b.WriteString(quoteField(fields[i+1]))
	}
	b.WriteString("\n")
	_, _ = io.WriteString(stdout, b.String())
}

// quoteField renders a value for the status line, quoting anything that would
// otherwise run into the next field.
func quoteField(value any) string {
	text := fmt.Sprint(value)
	if text == "" {
		return `""`
	}
	if strings.ContainsAny(text, " \t\"") {
		return strconv.Quote(text)
	}
	return text
}
