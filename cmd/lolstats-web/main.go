// Command lolstats-web serves the lolstats site, rendered on the server from
// the aggregate snapshot the pipeline publishes.
//
// It replaces a tier that rendered the same pages at build time and served them
// as files. The pages, the markup, the stylesheets and the three islands are
// unchanged; what changed is that the snapshot is read at request time, so
// sorting, filtering, paging, patch switching and comparing are query
// parameters on a plain URL and the site works with JavaScript disabled.
//
// The process is a reader. It never writes to the aggregate tree, which is
// mounted read-only, and it is the only thing that answers for the site's data:
// when the snapshot is missing or unreadable it answers with a visible error
// page naming the artifact rather than with a partial table.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/webtier"
)

// The environment this command reads on top of the data layer's own variables.
const (
	// envRiotToken carries the Riot site-verification token published at
	// /riot.txt. Unset means the file is not published at all, which is
	// answered as a 404: a 200 with no token would assert a verification that
	// has not happened.
	envRiotToken = "LOLSTATS_RIOT_TOKEN"
	// envRiotTokenFile is the mounted-Secret form of the same value.
	envRiotTokenFile = "LOLSTATS_RIOT_TOKEN_FILE"
	// envLogLevel is debug, info, warn or error.
	envLogLevel = "LOLSTATS_LOG_LEVEL"
	// envMetricsAddr is the second listener's address (LOLSTATS_METRICS_ADDR),
	// the same variable every other Go workload in this namespace reads. Unset
	// means no second listener, which is what a local run wants.
	envMetricsAddr = "LOLSTATS_METRICS_ADDR"
)

// The timeouts. A rendered page is assembled in memory and written once, so the
// read side is tight and the write side is generous enough for a large table on
// a slow link without letting a stalled client hold a goroutine forever.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second

	// shutdownGrace bounds the drain. It is deliberately shorter than the
	// Deployment's terminationGracePeriodSeconds, so that requests are
	// finished rather than killed by the kubelet.
	shutdownGrace = 15 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "lolstats-web: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	log := newLogger()

	opts := webtier.OptionsFromEnv()
	loader := webtier.NewLoader(opts)
	renderer, err := webtier.NewRenderer(loader, envValue(webtier.EnvSiteURL))
	if err != nil {
		// A template that does not parse is a build fault. Failing at startup
		// is the only honest outcome: the alternative is a tier that answers
		// every request with a page it cannot render.
		return fmt.Errorf("compile templates: %w", err)
	}
	if token := riotToken(); token != "" {
		renderer.SetRiotToken(token)
	}

	metrics := obs.NewMetrics()
	server := webtier.NewServer(renderer, webtier.ServerOptions{Logger: log, Metrics: metrics})

	// The startup lines are what an operator reads after a rollout: which
	// directory the tree resolved to, and what the tier thinks it has.
	logSnapshot(log, loader, opts, renderer)

	addr := envValue(webtier.EnvListenAddr)
	if addr == "" {
		addr = webtier.DefaultAddress
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The site listener is the one the public URL reaches. The second listener
	// exists only so that the namespace's scrape path keeps working: it is the
	// port deploy/base/ingest/service.yaml calls `metrics` and the one a
	// ServiceMonitor selects, and keeping scrapes off the site port also keeps
	// them out of the request log and out of the response-time histogram.
	failed := make(chan error, 2)
	// One send per listener, so the drain below knows how many it is waiting
	// for instead of assuming the optional listener started.
	pending := 1
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- fmt.Errorf("listen on %s: %w", addr, err)
			return
		}
		failed <- nil
	}()

	metricsAddr := envValue(envMetricsAddr)
	var metricsServer *http.Server
	if metricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", server.Metrics().Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
		})
		pending = 2
		metricsServer = &http.Server{
			Addr:              metricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
		}
		go func() {
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				failed <- fmt.Errorf("listen on %s: %w", metricsAddr, err)
				return
			}
			failed <- nil
		}()
		log.Info("observability listener started", "addr", metricsAddr)
	}
	log.Info("web tier listening",
		"addr", addr,
		"site_url", envValue(webtier.EnvSiteURL),
		"agg_root", opts.AggRoot,
		"fixtures", fixtureMode(opts.FixturesMode),
	)

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	// The signal is reported before the drain so that a rollout that hangs in
	// the shutdown has a log line saying which phase it reached.
	log.Info("shutting down", "grace", shutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if metricsServer != nil {
		// A scrape in flight is worth finishing, but a failure to drain it is
		// not worth failing the rollout over.
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			log.Warn("observability listener did not drain", "error", err.Error())
		}
	}
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	for i := 0; i < pending; i++ {
		if err := <-failed; err != nil {
			return err
		}
	}
	log.Info("web tier stopped")
	return nil
}

// logSnapshot reports what the tier resolved. A snapshot that cannot be read is
// a warning rather than a fatal error: the pages that describe the site itself
// are still served, the pages that describe the ladder answer with the fault,
// and /readyz reports the same thing so that the Deployment can act on it.
func logSnapshot(log *slog.Logger, loader *webtier.Loader, opts webtier.Options, renderer *webtier.Renderer) {
	root, err := loader.RootDir()
	if err != nil {
		log.Warn("no aggregate root could be resolved: the ladder pages will answer as unpublished",
			"agg_root", opts.AggRoot, "fault", webtier.FaultKindOf(err), "error", err.Error())
		return
	}
	if root == "" {
		log.Warn("no snapshot has been published: the ladder pages will answer as unpublished",
			"agg_root", opts.AggRoot, "fixtures", fixtureMode(opts.FixturesMode))
		return
	}
	site, err := loader.Site()
	if err != nil {
		log.Warn("the published snapshot cannot be read: the ladder pages will answer with the fault",
			"root", root, "fault", webtier.FaultKindOf(err), "error", err.Error())
		return
	}
	attrs := []any{
		"root", root,
		"state", site.State(),
		"source", site.Source(),
		"patches", len(site.Patches()),
		"min_cell_n", site.MinCellN(),
		"suppressed_cells", site.SuppressedCells(),
		"ddragon_version", site.DdragonVersion(),
		"riot_token_published", renderer.RiotTokenConfigured(),
	}
	if latest := site.Latest(); latest != nil {
		attrs = append(attrs, "latest_patch", latest.Patch, "region", latest.Region, "queue", latest.Queue)
	}
	if len(site.Patches()) == 0 {
		log.Warn("the snapshot holds no patches", attrs...)
		return
	}
	log.Info("snapshot loaded", attrs...)
}

// newLogger returns the process logger. It writes JSON to stderr, so the
// container runtime's collector sees one object per line and nothing else.
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(envValue(envLogLevel)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// riotToken reads the site-verification token from the environment or from the
// file a Secret is mounted as.
func riotToken() string {
	if token := envValue(envRiotToken); token != "" {
		return token
	}
	path := envValue(envRiotTokenFile)
	if path == "" {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		// Reported on stderr rather than logged, because the logger is built
		// after the token is read and a missing token file is worth one plain
		// line in the pod's log either way.
		fmt.Fprintf(os.Stderr, "lolstats-web: %s=%s cannot be read: %v\n", envRiotTokenFile, path, err)
		return ""
	}
	return strings.TrimSpace(string(body))
}

// fixtureMode names the fixture fallback for the startup line.
func fixtureMode(mode webtier.FixturesMode) string {
	if mode == "" {
		return string(webtier.FixturesAuto)
	}
	return string(mode)
}

func envValue(name string) string { return strings.TrimSpace(os.Getenv(name)) }
