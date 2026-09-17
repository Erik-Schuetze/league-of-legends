// Package obs carries the two things every lolstats binary needs to be
// observable: a structured logger and the pipeline's metric surface.
//
// The metric methods live behind MetricsRecorder rather than being called on
// the concrete type, because the crawler and the aggregator are written by
// different people against this package. A recorder that is nil, or a test
// double, must be able to stand in without either of them importing
// Prometheus.
package obs

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Erik-Schuetze/league-of-legends/internal/config"
)

// Pipeline stages reported through SetPipelineStaleness. The alerting rule is
// expressed against these exact strings, so they are part of the interface.
const (
	StageCrawl = "crawl"
	StageBuild = "build"
)

// MetricsRecorder is the frozen write surface for pipeline instrumentation.
// Implementations must be safe for concurrent use.
type MetricsRecorder interface {
	// ObserveRiotRequest records one completed HTTP attempt against the Riot
	// API, including the attempts that failed: status is the HTTP status, or
	// 0 when the request never produced a response. An alert on
	// status 429 or 403 is the early-warning signal that the key is at risk.
	ObserveRiotRequest(method string, status int, seconds float64)

	// IncRiotRetry records a retry the client decided to make, with a short
	// reason such as "429", "5xx" or "transport".
	IncRiotRetry(method, reason string)

	// SetRiotKeyAge records how long ago the Riot key was issued, which is
	// how the key-expiry alert fires before the key stops working.
	SetRiotKeyAge(seconds float64)

	// AddQueueClaimed counts fetch_queue rows this process claimed.
	AddQueueClaimed(n int)

	// AddMatchesPersisted counts matches newly inserted into `matches`;
	// a re-crawl of an already-known match is not a persistence.
	AddMatchesPersisted(n int)

	// AddRawBytesWritten counts compressed bytes appended to the raw archive.
	AddRawBytesWritten(n int64)

	// SetFrontierSize records how many live crawl_frontier rows exist.
	SetFrontierSize(n int)

	// SetPipelineStaleness records the age of the newest successful output
	// for a stage, using the Stage* constants above.
	SetPipelineStaleness(stage string, seconds float64)

	// ObserveBuildDuration records how long one aggregate build took.
	ObserveBuildDuration(seconds float64)

	// AddCellsPublished and AddCellsSuppressed are recorded separately on
	// purpose: a build that publishes nothing and suppresses everything is
	// not an error, but it is an incident, and the pair is what makes it
	// visible before a user sees an empty page.
	AddCellsPublished(n int)
	AddCellsSuppressed(n int)

	// IncBuildFailure records a build that failed closed. reason is a short
	// machine-readable slug, not a formatted error.
	IncBuildFailure(reason string)
}

// Metrics is the Prometheus-backed implementation of MetricsRecorder.
type Metrics struct {
	registry *prometheus.Registry

	riotRequests     *prometheus.CounterVec
	riotDuration     *prometheus.HistogramVec
	riotRetries      *prometheus.CounterVec
	riotKeyAge       prometheus.Gauge
	queueClaimed     prometheus.Counter
	matchesPersisted prometheus.Counter
	rawBytes         prometheus.Counter
	frontierSize     prometheus.Gauge
	staleness        *prometheus.GaugeVec
	buildDuration    prometheus.Histogram
	cellsPublished   prometheus.Counter
	cellsSuppressed  prometheus.Counter
	buildFailures    *prometheus.CounterVec
}

var _ MetricsRecorder = (*Metrics)(nil)

// NewMetrics builds an isolated registry rather than using the package-level
// default one. Two Metrics in the same process - a worker and an in-process
// test - would otherwise panic on duplicate registration.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		registry: reg,
		riotRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lolstats_riot_requests_total",
			Help: "Riot API requests by method and HTTP status.",
		}, []string{"method", "status"}),
		riotDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "lolstats_riot_request_duration_seconds",
			Help: "Riot API request latency by method.",
			// Exponential from 5ms to roughly 10s: the useful resolution is
			// at the low end, and a 10s bucket is already a timeout.
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
		}, []string{"method"}),
		riotRetries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lolstats_riot_retries_total",
			Help: "Retries the Riot client decided to make, by reason.",
		}, []string{"method", "reason"}),
		riotKeyAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lolstats_riot_key_age_seconds",
			Help: "Age of the configured Riot API key, when its issue time is known.",
		}),
		queueClaimed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lolstats_queue_claimed_total",
			Help: "fetch_queue rows claimed by this process.",
		}),
		matchesPersisted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lolstats_matches_persisted_total",
			Help: "Matches newly inserted into the matches table.",
		}),
		rawBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lolstats_raw_bytes_written_total",
			Help: "Compressed bytes appended to the raw archive.",
		}),
		frontierSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lolstats_frontier_entries",
			Help: "Live crawl_frontier rows.",
		}),
		staleness: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lolstats_pipeline_staleness_seconds",
			Help: "Age of the newest successful output of a pipeline stage.",
		}, []string{"stage"}),
		buildDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lolstats_build_duration_seconds",
			Help:    "Wall-clock duration of one aggregate build.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12),
		}),
		cellsPublished: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lolstats_cells_published_total",
			Help: "Aggregate cells written into a published artifact.",
		}),
		cellsSuppressed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lolstats_cells_suppressed_total",
			Help: "Aggregate cells withheld for falling below min_cell_n.",
		}),
		buildFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lolstats_build_failures_total",
			Help: "Aggregate builds that failed closed, by reason.",
		}, []string{"reason"}),
	}

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.riotRequests,
		m.riotDuration,
		m.riotRetries,
		m.riotKeyAge,
		m.queueClaimed,
		m.matchesPersisted,
		m.rawBytes,
		m.frontierSize,
		m.staleness,
		m.buildDuration,
		m.cellsPublished,
		m.cellsSuppressed,
		m.buildFailures,
	)

	return m
}

// Registry exposes the collector set for a test to gather from.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler serves the metrics endpoint. Caddy and the cluster scrape this;
// nothing on the public request path ever touches it.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveRiotRequest(method string, status int, seconds float64) {
	m.riotRequests.WithLabelValues(method, statusLabel(status)).Inc()
	m.riotDuration.WithLabelValues(method).Observe(seconds)
}

func (m *Metrics) IncRiotRetry(method, reason string) {
	m.riotRetries.WithLabelValues(method, reason).Inc()
}

func (m *Metrics) SetRiotKeyAge(seconds float64) { m.riotKeyAge.Set(seconds) }

func (m *Metrics) AddQueueClaimed(n int) {
	if n > 0 {
		m.queueClaimed.Add(float64(n))
	}
}

func (m *Metrics) AddMatchesPersisted(n int) {
	if n > 0 {
		m.matchesPersisted.Add(float64(n))
	}
}

func (m *Metrics) AddRawBytesWritten(n int64) {
	if n > 0 {
		m.rawBytes.Add(float64(n))
	}
}

func (m *Metrics) SetFrontierSize(n int) { m.frontierSize.Set(float64(n)) }

func (m *Metrics) SetPipelineStaleness(stage string, seconds float64) {
	m.staleness.WithLabelValues(stage).Set(seconds)
}

func (m *Metrics) ObserveBuildDuration(seconds float64) { m.buildDuration.Observe(seconds) }

func (m *Metrics) AddCellsPublished(n int) {
	if n > 0 {
		m.cellsPublished.Add(float64(n))
	}
}

func (m *Metrics) AddCellsSuppressed(n int) {
	if n > 0 {
		m.cellsSuppressed.Add(float64(n))
	}
}

func (m *Metrics) IncBuildFailure(reason string) {
	m.buildFailures.WithLabelValues(reason).Inc()
}

// statusLabel keeps a transport-level failure ("0") distinct from an HTTP
// status, so a rising `status="0"` reads as a DNS or TLS problem rather than
// as application errors. The label is the bare code, which keeps the alerting
// rules readable as `status=~"403|429"`.
func statusLabel(status int) string {
	if status <= 0 {
		return "0"
	}
	return strconv.Itoa(status)
}

// NopRecorder is a MetricsRecorder that discards everything. It exists so that
// a caller can be written without a nil check at every metric call site.
type NopRecorder struct{}

var _ MetricsRecorder = NopRecorder{}

func (NopRecorder) ObserveRiotRequest(string, int, float64) {}
func (NopRecorder) IncRiotRetry(string, string)             {}
func (NopRecorder) SetRiotKeyAge(float64)                   {}
func (NopRecorder) AddQueueClaimed(int)                     {}
func (NopRecorder) AddMatchesPersisted(int)                 {}
func (NopRecorder) AddRawBytesWritten(int64)                {}
func (NopRecorder) SetFrontierSize(int)                     {}
func (NopRecorder) SetPipelineStaleness(string, float64)    {}
func (NopRecorder) ObserveBuildDuration(float64)            {}
func (NopRecorder) AddCellsPublished(int)                   {}
func (NopRecorder) AddCellsSuppressed(int)                  {}
func (NopRecorder) IncBuildFailure(string)                  {}

// NewLogger returns the structured logger every binary logs through.
func NewLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}

	var handler slog.Handler
	if cfg.LogFormat == config.FormatText {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}

	return slog.New(handler).With("env", cfg.Env)
}
