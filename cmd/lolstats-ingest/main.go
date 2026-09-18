// Command lolstats-ingest is the single long-running writer of raw Riot data.
// Everything that touches the Riot API is driven from here: the continuous
// queue worker, the seed discoverer, the static-data mirror, the backfill
// runner and the maintenance job.
//
// Subcommands:
//
//	worker          run the fetch-queue consumer until a signal arrives
//	discover-seeds  enumerate seed summoner PUUIDs for the configured ladder
//	static-sync     mirror Data Dragon versions and static data into the archive
//	backfill        re-run a bounded key range through the crawl path
//	maintain        reclaim abandoned claims, prune the frontier, re-rank it,
//	                replay dead letters with -replay-dead-letters
//	migrate up      apply the forward migrations
//
// The Riot API key is optional. With no key the process starts, serves health
// and metrics, logs one warning and does not crawl, because the alternative -
// a crash loop - is indistinguishable from a broken deployment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/config"
	"github.com/Erik-Schuetze/league-of-legends/internal/crawl"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/raw"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
	"github.com/Erik-Schuetze/league-of-legends/internal/store"
)

// exitUsage is for argument and configuration errors. Everything else that
// fails returns 1, which cron and a Kubernetes Job both treat as a failure.
const exitUsage = 2

const usage = `lolstats-ingest - writes raw Riot data into the archive and the queue

usage: lolstats-ingest <subcommand> [flags]

subcommands:
  worker          run the fetch-queue consumer until a signal arrives
  discover-seeds  enumerate seed summoner PUUIDs for the configured ladder
  static-sync     mirror Data Dragon versions and static data into the archive
  backfill        re-run a bounded key range through the crawl path
  maintain        reclaim abandoned claims, prune the frontier, re-rank it,
                  replay dead letters with -replay-dead-letters
  migrate up      apply the forward migrations

run "lolstats-ingest <subcommand> -h" for the flags of a subcommand.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// writeUsage is used only for help and argument errors. The write error is
// dropped deliberately: the stream that just failed is the only place the
// failure could be reported, and a missing help message must not change the
// exit code.
func writeUsage(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeUsage(stderr, "%s", usage)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		writeUsage(stdout, "%s", usage)
		return 0
	case "worker":
		return runWorker(args[1:], stderr)
	case "discover-seeds":
		return runDiscoverSeeds(args[1:], stderr)
	case "static-sync":
		return runStaticSync(args[1:], stderr)
	case "backfill":
		return runBackfill(args[1:], stderr)
	case "maintain":
		return runMaintain(args[1:], stderr)
	case "migrate":
		return runMigrate(args[1:], stderr)
	default:
		writeUsage(stderr, "lolstats-ingest: unknown subcommand %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

// bootstrap loads configuration and wires logging and metrics. It does not
// validate: a subcommand may only fail on the configuration it actually reads,
// so validation is applied per subcommand with require.
func bootstrap() (config.Config, *slog.Logger, *obs.Metrics, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	log := obs.NewLogger(cfg)
	return cfg, log, obs.NewMetrics(), nil
}

// require applies the per-component validators named by the caller. A cron job
// that only touches the raw archive must not be stopped by an unset Postgres
// DSN, which is why this is not folded into config.Load - and why the Riot
// key, whose absence is a supported state, is never in this list.
func require(vs ...interface{ Validate() error }) error {
	errs := make([]error, 0, len(vs))
	for _, v := range vs {
		errs = append(errs, v.Validate())
	}
	return errors.Join(errs...)
}

// requireArchive checks the one archive setting there is no sensible default
// for: writing payloads into the working directory is how an immutable archive
// gets confused with a scratch file.
func requireArchive(cfg config.Config) error {
	if strings.TrimSpace(cfg.Raw.Root) == "" {
		return errors.New("LOLSTATS_RAW_ROOT is required")
	}
	return nil
}

func fail(stderr io.Writer, sub string, err error) int {
	writeUsage(stderr, "lolstats-ingest %s: %v\n", sub, err)
	return 1
}

// openStore connects the control plane.
//
// The logger is passed through because the first thing to fail in a cluster
// event is this connect, and the retry it runs is only useful if the operator
// can see it happening rather than watching a pod that looks stuck. The window
// itself is the store's default: only the workload knows the budget it fits in,
// and every caller here has minutes of it.
func openStore(ctx context.Context, cfg config.Config, log *slog.Logger, metrics obs.MetricsRecorder) (*store.Store, error) {
	return store.Open(ctx, store.Options{
		DSN:         cfg.Postgres.DSN,
		MaxConns:    int(cfg.Postgres.MaxConns),
		ConnTimeout: cfg.Postgres.ConnTimeout,
		Region:      cfg.Riot.Region,
		Metrics:     metrics,
		Logger:      log,
	})
}

// openArchive opens the immutable payload archive.
func openArchive(cfg config.Config, metrics obs.MetricsRecorder) (*raw.Writer, error) {
	return raw.New(raw.Options{
		Root:             cfg.Raw.Root,
		RowsPerPart:      cfg.Raw.RowsPerPart,
		CompressionLevel: cfg.Raw.CompressionLevel,
		Metrics:          metrics,
	})
}

// newRiotClient wires the adaptive client. The key is optional on purpose: the
// client and its key provider both tolerate an absent key, so an operator can
// bring the process up before the key exists and watch it start crawling when
// the key arrives.
func newRiotClient(cfg config.Config, log *slog.Logger, metrics obs.MetricsRecorder, keys *riot.KeyProvider) (*riot.Client, error) {
	platform, regional := riot.BaseURLs(cfg.Riot.PlatformRoute, cfg.Riot.RegionalRoute)
	windows := riot.ConfigWindows(cfg.Riot.AppRatePerSecond, cfg.Riot.AppRatePer2Min)
	return riot.NewClient(riot.Options{
		PlatformBaseURL: platform,
		RegionalBaseURL: regional,
		Timeout:         cfg.Riot.Timeout,
		MaxAttempts:     cfg.Riot.MaxAttempts,
		Limiter: riot.NewLimiter(riot.LimiterOptions{
			Ceiling:   windows,
			Bootstrap: windows,
		}),
		KeyProvider: keys,
		Metrics:     metrics,
		Logger:      log,
	})
}

// keyWarning is the single clear line an operator greps for. It says what
// happens next rather than what failed.
func keyWarning(log *slog.Logger, keys *riot.KeyProvider) {
	if _, ok := keys.Key(); ok {
		log.Info("Riot API key loaded", "source", keys.Source())
		return
	}
	log.Warn("no Riot API key configured: crawling is disabled, health and metrics continue",
		"env", riot.EnvAPIKey, "env_file", riot.EnvAPIKeyFile)
}

// requireKeyNotExpired refuses to start key-spending work on a key whose
// declared deadline has passed.
//
// The check belongs here, before the store is opened and before the first
// request, because that is the whole point of declaring the deadline. A
// scheduled job that starts, spends its startup on connections and its first
// calls on refusals, and then fails with "rate limited" or "unexpected status
// 403" has told the operator almost nothing; this exits 1 with the expiry, the
// fix, and nothing else.
//
// An undeclared deadline is not an error. Most deployments do not know their
// key's expiry and a Riot production key does not have one; there the first 401
// is what reports a dead key, and it already reports it as a failed job. The
// log line says so, so that "the pipeline did not notice" is never the
// operator's conclusion.
func requireKeyNotExpired(log *slog.Logger, cfg config.Config) error {
	expiry := riot.NewKeyExpiry(cfg.Riot.KeyExpiresAt)
	now := time.Now()
	if err := expiry.Check(now); err != nil {
		log.Error("refusing to start: the declared Riot key expiry has passed",
			"err", err, "expired_at", expiry.At())
		return err
	}
	remaining, declared := expiry.Remaining(now)
	if !declared {
		log.Info("no declared Riot key expiry: a dead key is reported by Riot refusing it",
			"hint", "set LOLSTATS_RIOT_API_KEY_EXPIRES_AT to fail before the first refused call",
			"key_age_warning_after", crawl.KeyWarnAge.String())
		return nil
	}
	log.Info("Riot API key expiry declared",
		"expires_at", expiry.At(), "remaining", remaining.Truncate(time.Second).String())
	if remaining < keyExpiryWarnLead {
		// A development key is rotated daily, so a rotation notice is only
		// useful while there is still time in the working day to act on it.
		log.Warn("Riot API key expires soon: rotate it before the next scheduled run",
			"expires_at", expiry.At(), "remaining", remaining.Truncate(time.Second).String())
	}
	return nil
}

// keyExpiryWarnLead is how long before a declared expiry the warning starts. It
// is the same lead the crawl worker uses for a key that is merely old - the two
// notices describe the same event from different information, and an operator
// who sees both should not have to work out which one is later.
const keyExpiryWarnLead = crawl.KeyWarnAge

// service is the pair of background components every long-running subcommand
// needs: the health/metrics listener and the signal-scoped context.
type service struct {
	cfg     config.Config
	log     *slog.Logger
	metrics *obs.Metrics
	ready   func(context.Context) map[string]any
}

// signalContext returns a context cancelled by SIGINT or SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// serve runs the health and metrics listener until ctx is cancelled. It is
// started before the work so that a process which is still starting up is
// already observable.
func (s *service) serve(ctx context.Context) (<-chan error, *http.Server) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", s.metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Liveness is about this process, not about its dependencies: a
		// keyless or database-less process is still a process a supervisor
		// must not restart.
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{"status": "ok", "ok": true}
		status := http.StatusOK
		if s.ready != nil {
			body = s.ready(r.Context())
			if ok, _ := body["ok"].(bool); !ok {
				status = http.StatusServiceUnavailable
			}
		}
		writeJSON(w, status, body)
	})

	srv := &http.Server{
		Addr:              s.cfg.MetricsAddr,
		Handler:           mux,
		ReadHeaderTimeout: s.cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       s.cfg.HTTP.ReadTimeout,
		WriteTimeout:      s.cfg.HTTP.WriteTimeout,
		IdleTimeout:       s.cfg.HTTP.IdleTimeout,
	}
	failed := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- fmt.Errorf("listener on %s: %w", s.cfg.MetricsAddr, err)
		}
	}()
	_ = ctx
	s.log.Info("observability listener started", "addr", s.cfg.MetricsAddr)
	return failed, srv
}

// stop shuts the listener down within the configured grace period. A shutdown
// that overruns is reported rather than ignored: it means in-flight scrapes
// were cut off.
func (s *service) stop(log *slog.Logger, srv *http.Server) error {
	if srv == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("listener shutdown: %w", err)
	}
	log.Info("observability listener stopped")
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// runWorker is the long-running crawler.
func runWorker(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jobBatch := fs.Int("job-batch", crawl.DefaultJobBatch, "fetch_queue rows claimed per pass")
	frontierBatch := fs.Int("frontier-batch", crawl.DefaultFrontierBatch, "frontier rows claimed per pass")
	history := fs.Int("history-count", crawl.DefaultHistoryCount, "match ids requested per player")
	queue := fs.Int("queue", 420, "queue id filter for player history")
	maxAttempts := fs.Int("max-attempts", crawl.DefaultMaxAttempts, "claim ceiling before a row is dead-lettered")
	poll := fs.Duration("poll-interval", crawl.DefaultPollInterval, "idle poll interval")
	retryBase := fs.Duration("retry-base", crawl.DefaultRetryBase, "base delay for a retried row")
	retryMax := fs.Duration("retry-max", crawl.DefaultRetryMax, "ceiling for a retried row")
	jobTimeout := fs.Duration("job-timeout", crawl.DefaultJobTimeout, "timeout for one Riot fetch")
	claimGrace := fs.Duration("claim-grace", crawl.DefaultClaimGrace,
		"age at which a claim held by a dead process is reclaimed at startup")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, log, metrics, err := bootstrap()
	if err != nil {
		return fail(stderr, "worker", err)
	}
	if err := errors.Join(require(cfg.Postgres), requireArchive(cfg)); err != nil {
		return fail(stderr, "worker", err)
	}

	ctx, stop := signalContext()
	defer stop()

	archive, err := openArchive(cfg, metrics)
	if err != nil {
		return fail(stderr, "worker", err)
	}
	defer func() {
		if cerr := archive.Close(); cerr != nil {
			log.Error("archive close failed", "err", cerr)
		}
	}()

	ctrl, err := openStore(ctx, cfg, log, metrics)
	if err != nil {
		return fail(stderr, "worker", err)
	}
	defer func() {
		if cerr := ctrl.Close(); cerr != nil {
			log.Error("store close failed", "err", cerr)
		}
	}()

	keys := riot.NewKeyProvider()
	keyWarning(log, keys)
	if err := requireKeyNotExpired(log, cfg); err != nil {
		return fail(stderr, "worker", err)
	}
	client, err := newRiotClient(cfg, log, metrics, keys)
	if err != nil {
		return fail(stderr, "worker", err)
	}
	defer client.CloseIdleConnections()

	worker, err := crawl.NewWorker(crawl.WorkerOptions{
		Deps: crawl.Deps{
			Store:   ctrl,
			Fetcher: client,
			Writer:  archive,
			Region:  cfg.Riot.Region,
			Log:     log,
			Metrics: metrics,
			Clock:   riot.RealClock{},
			// The loop re-checks this every pass. A worker that started with a
			// valid declaration must not keep crawling once the deadline it was
			// given has passed, which is the one expiry a long-running process
			// can notice without asking Riot.
			KeyExpiry: riot.NewKeyExpiry(cfg.Riot.KeyExpiresAt),
		},
		Queue:         *queue,
		JobBatch:      *jobBatch,
		FrontierBatch: *frontierBatch,
		HistoryCount:  *history,
		MaxAttempts:   *maxAttempts,
		PollInterval:  *poll,
		RetryBase:     *retryBase,
		RetryMax:      *retryMax,
		JobTimeout:    *jobTimeout,
		ClaimGrace:    *claimGrace,
	})
	if err != nil {
		return fail(stderr, "worker", err)
	}

	svc := &service{cfg: cfg, log: log, metrics: metrics, ready: readiness(ctrl, keys, cfg)}
	failed, srv := svc.serve(ctx)
	go func() {
		if lerr := <-failed; lerr != nil {
			log.Error("observability listener failed", "err", lerr)
			stop()
		}
	}()

	runErr := worker.Run(ctx)
	if serr := svc.stop(log, srv); serr != nil {
		log.Error("shutdown overran", "err", serr)
	}
	if runErr != nil {
		return fail(stderr, "worker", runErr)
	}
	log.Info("worker exited cleanly")
	return 0
}

// readiness reports what the probes and a human both want to know: is the
// database reachable, and can the crawler still use its key. It never reports
// the key itself.
func readiness(ctrl *store.Store, keys *riot.KeyProvider, cfg config.Config) func(context.Context) map[string]any {
	expiry := riot.NewKeyExpiry(cfg.Riot.KeyExpiresAt)
	return func(probeCtx context.Context) map[string]any {
		body := map[string]any{"ok": true, "store": "ok"}
		pingCtx, cancel := context.WithTimeout(probeCtx, 3*time.Second)
		defer cancel()
		if err := ctrl.Ping(pingCtx); err != nil {
			body["ok"], body["store"] = false, "unreachable: "+err.Error()
		}
		_, hasKey := keys.Key()
		body["riot_key"] = hasKey
		now := time.Now()
		switch expired := expiry.Check(now); {
		case expired != nil:
			// A declared expiry is a fact, so readiness reports it as one: the
			// worker exits on it, and a probe that said "ok" while the process
			// was refusing to crawl would be the probe lying.
			body["ok"] = false
			body["crawling"] = "disabled: " + expired.Error()
		case !hasKey:
			body["crawling"] = "disabled: no Riot API key"
		}
		if hasKey {
			body["riot_key_source"] = keys.Source()
			body["riot_key_age_seconds"] = int(keys.Age().Seconds())
		}
		if remaining, declared := expiry.Remaining(now); declared {
			body["riot_key_expires_at"] = expiry.At().Format(time.RFC3339)
			body["riot_key_expires_in_seconds"] = int(remaining.Seconds())
		}
		return body
	}
}

func runDiscoverSeeds(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("discover-seeds", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tier := fs.String("tier", crawl.DefaultSeedTier, "ladder tier to seed from")
	division := fs.String("division", crawl.DefaultSeedDivision, "ladder division to seed from")
	queue := fs.String("queue", crawl.DefaultSeedQueue, "Riot queue type, e.g. RANKED_SOLO_5x5")
	pages := fs.Int("pages", crawl.DefaultSeedPages, "maximum ladder pages to walk")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, log, metrics, err := bootstrap()
	if err != nil {
		return fail(stderr, "discover-seeds", err)
	}
	if err := errors.Join(require(cfg.Postgres), requireArchive(cfg)); err != nil {
		return fail(stderr, "discover-seeds", err)
	}

	ctx, stop := signalContext()
	defer stop()

	archive, err := openArchive(cfg, metrics)
	if err != nil {
		return fail(stderr, "discover-seeds", err)
	}
	defer func() { _ = archive.Close() }()
	ctrl, err := openStore(ctx, cfg, log, metrics)
	if err != nil {
		return fail(stderr, "discover-seeds", err)
	}
	defer func() { _ = ctrl.Close() }()

	keys := riot.NewKeyProvider()
	keyWarning(log, keys)
	if _, ok := keys.Key(); !ok {
		log.Error("seeding cannot run without a Riot API key")
		return 1
	}
	if err := requireKeyNotExpired(log, cfg); err != nil {
		return fail(stderr, "discover-seeds", err)
	}
	client, err := newRiotClient(cfg, log, metrics, keys)
	if err != nil {
		return fail(stderr, "discover-seeds", err)
	}
	defer client.CloseIdleConnections()

	result, err := crawl.DiscoverSeeds(ctx, crawl.SeedOptions{
		Deps: crawl.Deps{
			Store: ctrl, Fetcher: client, Writer: archive,
			Region: cfg.Riot.Region, Log: log, Metrics: metrics,
		},
		Queue:    *queue,
		Tier:     strings.ToUpper(*tier),
		Division: strings.ToUpper(*division),
		MaxPages: *pages,
	})
	if err != nil {
		return fail(stderr, "discover-seeds", err)
	}
	log.Info("seed discovery complete",
		"run_id", result.RunID, "tier", result.Tier, "division", result.Division,
		"pages", result.Pages, "entries", result.Entries, "added", result.Added)
	return 0
}

func runStaticSync(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("static-sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "Data Dragon version; empty means the newest")
	locale := fs.String("locale", crawl.DefaultDDragonLocale, "Data Dragon locale")
	baseURL := fs.String("base-url", crawl.DefaultDDragonURL, "Data Dragon origin")
	kinds := fs.String("kinds", strings.Join(crawl.StaticKinds(), ","), "comma-separated document kinds")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, log, metrics, err := bootstrap()
	if err != nil {
		return fail(stderr, "static-sync", err)
	}
	if err := requireArchive(cfg); err != nil {
		// Static data is public: no key and no database are needed.
		return fail(stderr, "static-sync", err)
	}

	ctx, stop := signalContext()
	defer stop()

	archive, err := openArchive(cfg, metrics)
	if err != nil {
		return fail(stderr, "static-sync", err)
	}
	defer func() { _ = archive.Close() }()

	result, err := crawl.StaticSync(ctx, crawl.StaticOptions{
		Writer:  archive,
		Log:     log,
		Metrics: metrics,
		BaseURL: *baseURL,
		Version: *version,
		Locale:  *locale,
		Kinds:   splitList(*kinds),
	})
	if err != nil {
		return fail(stderr, "static-sync", err)
	}
	for _, doc := range result.Documents {
		log.Info("static document archived",
			"kind", doc.Kind, "version", doc.Version, "bytes", doc.Bytes, "url", doc.URL)
	}
	log.Info("static sync complete", "version", result.Version, "documents", len(result.Documents))
	return 0
}

// splitList parses a comma-separated flag value, dropping empty entries.
func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func runBackfill(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	puuid := fs.String("puuid", "", "player whose history is re-crawled")
	from := fs.String("from", "", "inclusive lower bound, RFC 3339")
	to := fs.String("to", "", "exclusive upper bound, RFC 3339")
	matchIDs := fs.String("match-ids", "", "comma-separated match ids to re-run")
	idsFile := fs.String("ids-file", "", "file of match ids, one per line")
	limit := fs.Int("limit", crawl.DefaultBackfillLimit, "maximum keys fetched in this run")
	queue := fs.Int("queue", 420, "queue id filter for the history lookup")
	force := fs.Bool("force", false, "re-fetch keys the control plane already knows")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, log, metrics, err := bootstrap()
	if err != nil {
		return fail(stderr, "backfill", err)
	}
	if err := errors.Join(require(cfg.Postgres), requireArchive(cfg)); err != nil {
		return fail(stderr, "backfill", err)
	}

	ids := splitList(*matchIDs)
	if *idsFile != "" {
		fileIDs, readErr := readIDFile(*idsFile)
		if readErr != nil {
			return fail(stderr, "backfill", readErr)
		}
		ids = append(ids, fileIDs...)
	}
	if len(ids) == 0 && *puuid == "" {
		writeUsage(stderr, "lolstats-ingest backfill: pass -match-ids, -ids-file or -puuid\n")
		return exitUsage
	}
	fromTime, err := parseTime(*from)
	if err != nil {
		return fail(stderr, "backfill", err)
	}
	toTime, err := parseTime(*to)
	if err != nil {
		return fail(stderr, "backfill", err)
	}

	ctx, stop := signalContext()
	defer stop()

	archive, err := openArchive(cfg, metrics)
	if err != nil {
		return fail(stderr, "backfill", err)
	}
	defer func() { _ = archive.Close() }()
	ctrl, err := openStore(ctx, cfg, log, metrics)
	if err != nil {
		return fail(stderr, "backfill", err)
	}
	defer func() { _ = ctrl.Close() }()

	keys := riot.NewKeyProvider()
	keyWarning(log, keys)
	if _, ok := keys.Key(); !ok {
		log.Error("backfill cannot run without a Riot API key")
		return 1
	}
	if err := requireKeyNotExpired(log, cfg); err != nil {
		return fail(stderr, "backfill", err)
	}
	client, err := newRiotClient(cfg, log, metrics, keys)
	if err != nil {
		return fail(stderr, "backfill", err)
	}
	defer client.CloseIdleConnections()

	result, err := crawl.Backfill(ctx, crawl.BackfillOptions{
		Deps: crawl.Deps{
			Store: ctrl, Fetcher: client, Writer: archive,
			Region: cfg.Riot.Region, Log: log, Metrics: metrics,
		},
		MatchIDs: ids,
		PUUID:    *puuid,
		From:     fromTime,
		To:       toTime,
		Limit:    *limit,
		Queue:    *queue,
		Force:    *force,
	})
	log.Info("backfill summary",
		"candidates", result.Candidates, "fetched", result.Fetched,
		"inserted", result.Inserted, "known", result.Known, "failed", result.Failed)
	if err != nil {
		return fail(stderr, "backfill", err)
	}
	return 0
}

// readIDFile reads one match id per line. Blank lines and #-comments are
// ignored so an operator can annotate a gap list.
func readIDFile(path string) ([]string, error) {
	//nolint:gosec // the path is an operator-supplied flag, not request input
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ids file: %w", err)
	}
	lines := strings.Split(string(payload), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	return out, nil
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", value, err)
	}
	return parsed, nil
}

func runMaintain(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("maintain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	claimGrace := fs.Duration("claim-grace", crawl.DefaultClaimGrace, "age at which a claim is treated as abandoned")
	retention := fs.Duration("frontier-retention", crawl.DefaultFrontierRetention, "age at which a dead frontier entry is pruned")
	maxEmpty := fs.Int("max-consecutive-empty", crawl.DefaultMaxConsecutiveMiss, "fruitless crawls before a frontier entry is pruned")
	limit := fs.Int("limit", crawl.DefaultMaintainLimit, "rows changed per statement")
	replayDeadLetters := fs.Bool("replay-dead-letters", false,
		"return dead-lettered rows to the queue (use after the key that retired them has been fixed)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg, log, metrics, err := bootstrap()
	if err != nil {
		return fail(stderr, "maintain", err)
	}
	if err := require(cfg.Postgres); err != nil {
		return fail(stderr, "maintain", err)
	}

	ctx, stop := signalContext()
	defer stop()

	ctrl, err := openStore(ctx, cfg, log, metrics)
	if err != nil {
		return fail(stderr, "maintain", err)
	}
	defer func() { _ = ctrl.Close() }()

	result, err := crawl.Maintain(ctx, crawl.MaintainOptions{
		Deps: crawl.Deps{
			Store: ctrl, Region: cfg.Riot.Region, Log: log, Metrics: metrics,
		},
		ClaimGrace:          *claimGrace,
		FrontierRetention:   *retention,
		MaxConsecutiveEmpty: *maxEmpty,
		Limit:               *limit,
		ReplayDeadLetters:   *replayDeadLetters,
		DryRun:              *dryRun,
	})
	if err != nil {
		return fail(stderr, "maintain", err)
	}
	log.Info("maintenance complete",
		"dry_run", result.DryRun,
		"pruned", result.Pruned,
		"reclaimed_claims", result.ReclaimedClaims,
		"reprioritised", result.Reprioritised,
		"replayed_dead_letters", result.ReplayedDeadLetters,
		"frontier_size", result.FrontierSize,
		"dead_frontier", result.DeadFrontierSize)
	return 0
}

// runMigrate applies the forward migrations. It is a subcommand rather than a
// startup side effect: a crawler that migrates on boot turns a schema problem
// into a crawl outage, and two replicas racing to migrate is a failure mode the
// advisory lock prevents but does not make cheap.
func runMigrate(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		writeUsage(stderr, "lolstats-ingest migrate: expected \"up\"\n")
		return exitUsage
	}
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	switch args[0] {
	case "up":
	case "list":
		versions, err := store.MigrationVersions()
		if err != nil {
			return fail(stderr, "migrate", err)
		}
		writeUsage(stderr, "lolstats-ingest migrate: available versions %v\n", versions)
		return 0
	default:
		writeUsage(stderr, "lolstats-ingest migrate: unknown action %q (expected \"up\")\n", args[0])
		return exitUsage
	}

	cfg, log, _, err := bootstrap()
	if err != nil {
		return fail(stderr, "migrate", err)
	}
	if err := require(cfg.Postgres); err != nil {
		return fail(stderr, "migrate", err)
	}

	ctx, stop := signalContext()
	defer stop()

	applied, err := store.MigrateUp(ctx, cfg.Postgres.DSN)
	if err != nil {
		return fail(stderr, "migrate", err)
	}
	if len(applied) == 0 {
		log.Info("migrations up: schema is already current")
		return 0
	}
	log.Info("migrations up: schema updated", "versions", applied)
	return 0
}
