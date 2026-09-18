// Package config loads every runtime setting from the process environment.
//
// Environment variables are the only input on purpose. These binaries run as
// Kubernetes Deployments and CronJobs, where a ConfigMap is already projected
// into the process environment; a config file would add a second source of
// truth that can disagree with the manifest that launched the process, and
// would have to be shipped inside the image - which is exactly where a secret
// must never be.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Getenv is the narrow view of the environment the loader needs, so tests can
// supply a map instead of mutating the real process environment.
type Getenv func(key string) (string, bool)

// OS reads from the real environment.
func OS(key string) (string, bool) { return os.LookupEnv(key) }

// Format is the slog handler to install.
type Format string

const (
	// FormatJSON is the default: every workload is scraped by the cluster's
	// log collector, which parses rather than reads.
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// Config is the whole runtime configuration for every lolstats binary. Fields
// an individual binary does not use are still loaded, so that `go vet`-level
// mistakes such as a typo'd variable name surface as "unused setting" rather
// than as a silent default.
type Config struct {
	Env           string
	LogLevel      slog.Level
	LogFormat     Format
	MetricsAddr   string
	ShutdownGrace time.Duration

	Riot      Riot
	Postgres  Postgres
	Raw       Raw
	Aggregate Aggregate
	HTTP      HTTP
}

// Riot is the client configuration.
type Riot struct {
	APIKey        string
	Region        string
	PlatformRoute string
	RegionalRoute string
	Timeout       time.Duration
	MaxAttempts   int

	// Riot's published rate limits are stale, so the limits that actually
	// apply are read from X-App-Rate-Limit on every response. These two
	// values are only the conservative cap used before the first response
	// of a run arrives, and the ceiling the adaptive limiter must never
	// exceed. They are deliberately far below a development key's real
	// allowance: an over-eager limiter costs crawl throughput, a wrong one
	// costs the key.
	AppRatePerSecond float64
	AppRatePer2Min   int

	// Zero unless the operator records the key's expiry, which the key-age
	// alert needs. A development key expires every 24 hours.
	KeyExpiresAt time.Time
}

// Validate reports whether the client has enough to make a request. It is
// separate from Load because a cron job that only reads the raw archive has no
// reason to hold a Riot key.
func (r Riot) Validate() error {
	var errs []error
	if strings.TrimSpace(r.APIKey) == "" {
		errs = append(errs, fmt.Errorf("%s is required", env("RIOT_API_KEY")))
	}
	if strings.TrimSpace(r.Region) == "" {
		errs = append(errs, fmt.Errorf("%s is required", env("RIOT_REGION")))
	}
	if strings.TrimSpace(r.PlatformRoute) == "" {
		errs = append(errs, fmt.Errorf("%s is required", env("RIOT_PLATFORM_ROUTE")))
	}
	if strings.TrimSpace(r.RegionalRoute) == "" {
		errs = append(errs, fmt.Errorf("%s is required", env("RIOT_REGIONAL_ROUTE")))
	}
	return errors.Join(errs...)
}

// Postgres is the control-plane database connection. Nothing on the public
// site's request path ever opens one.
type Postgres struct {
	DSN         string
	MaxConns    int32
	ConnTimeout time.Duration
}

// Validate reports whether a database connection can be attempted.
func (p Postgres) Validate() error {
	if strings.TrimSpace(p.DSN) == "" {
		return fmt.Errorf("%s is required", env("POSTGRES_DSN"))
	}
	return nil
}

// Raw configures the immutable archive, which is the primary copy of every
// payload Riot returned. Losing it loses data Riot only retains for two years.
type Raw struct {
	Root string
	// Rows per Parquet part file. Small enough that a killed run loses only
	// the part it was writing, large enough that a partition is not thousands
	// of files.
	RowsPerPart int
	// zstd level. 3 is the library default and the point where more
	// compression stops buying meaningful bytes per CPU second.
	CompressionLevel int
}

// Aggregate configures the DuckDB build step.
type Aggregate struct {
	Root string
	// Version of the artifact envelope the build writes. Bumping it is a
	// contract change and needs an ADR.
	SchemaVersion int
	// Cells with fewer observations than this are suppressed and counted,
	// never published. See docs/contracts.md.
	MinCellN int
	// How many participant rows may lack a champion or a role before a build
	// refuses to publish, as an absolute floor. Zero, the default, is "the
	// archive must classify every row it contains": a rejected row lowers
	// every rate it should have contributed to, so the build stops rather than
	// publish a biased tier list. The deployed value is an allowance measured
	// against the archive instead of zero, because Riot's own payloads contain
	// a small number of rows it marks as position-less (see the
	// MaxRejectedRows note in internal/aggregate/gate.go).
	MaxRejectedRows int
	// The window-sized part of that allowance: the share of the window's
	// participant rows a build may reject. The allowance applied is
	// max(MaxRejectedRows, ceil(MaxRejectedRate x participant rows)), so it
	// grows with the crawl. Zero, the default, means "no rate ceiling", which
	// leaves MaxRejectedRows to decide alone and keeps an unconfigured build
	// fail-closed. See the MaxRejectedRate note in internal/aggregate/gate.go.
	MaxRejectedRate float64
	// How many of the window's computable champion/role cells must survive
	// suppression for the build to publish at all. The default is 0.5, and the
	// value that is right depends on how deeply the patch the window ends on
	// has been crawled: a real window always has a tail of one-off
	// champion/role pairs, so the share of cells above min_cell_n is a
	// property of the archive's maturity, not of the pipeline's health. The
	// deployed value is measured against the live archive; see the
	// MinConfidentShare note in internal/aggregate/gate.go.
	MinConfidentShare float64
	// RequireProvenance makes the nightly build refuse to publish a snapshot
	// that names neither the revision it was built from nor the build_runs row
	// the run was recorded in.
	//
	// It is not set in `deploy/`, so the nightly build runs with the default
	// below (false) and a run whose GIT_SHA is missing or whose audit row could
	// not be opened publishes build_run_id 0 and git_sha "unknown" - values
	// that name no row and no revision, but that nothing in the artifact marks
	// as absent. Turning the gate on is one key in `deploy/base/config.yaml`
	// (LOLSTATS_AGG_REQUIRE_PROVENANCE, which the aggregate job already reads
	// from that ConfigMap); the default here is false so that a developer's
	// build over a fixture, an offline verification and `lolstats-aggregate
	// demo` keep working with no database. See the RequireProvenance note in
	// internal/aggregate/gate.go.
	RequireProvenance bool
	// Trailing window of days of raw data a build reads.
	SourceWindowDays int
	// v1 publishes a single unsegmented bracket; the value is still explicit
	// because it is part of every artifact path.
	Bracket Bracket
	QueueID int
	// Empty means "use the newest patch found in the raw archive".
	Patch string

	// DatasetRoot is the parent of the feature dataset trees, e.g.
	// /var/lib/lolstats/datasets. The dataset is written to
	// <DatasetRoot>/timeline-v1 by `lolstats-aggregate features`. It is a
	// separate root from the aggregate root on purpose; see the note on
	// defaultDatasetRoot.
	DatasetRoot string
	// FeatureMinDurationS is the duration floor the feature build records as
	// the reason a short game was excluded from the dataset.
	FeatureMinDurationS int

	// The DuckDB resource bounds. They are configuration rather than constants
	// because the value that is right depends on the pod the build runs in, and
	// this is the one part of the pipeline that can take the whole pod down with
	// it: DuckDB sizes its default memory limit from the host's RAM, not from the
	// cgroup, so an unset limit is not "no limit" but "the node's limit".
	//
	// Empty and zero mean "the conservative default in internal/aggregate"
	// (DefaultDuckDBMemoryLimit, DefaultDuckDBThreads, DefaultDuckDBMaxTempSize,
	// and the system temporary directory for the spill). The defaults live there
	// rather than here so that one place decides what a build with no operator
	// input is allowed to allocate, and so that a test can assert that default
	// still fits inside the deployed pod limit.
	DuckDBMemoryLimit string
	// DuckDBThreads caps the engine's thread pool. Zero means the default rather
	// than the host's core count.
	DuckDBThreads int
	// DuckDBTempDir is the parent of the spill directory DuckDB writes to when a
	// statement does not fit in DuckDBMemoryLimit. Empty means the process's
	// system temporary directory, which in the aggregate Job is the emptyDir
	// mounted at /tmp - the only writable path that job has, because its root
	// filesystem is read only.
	DuckDBTempDir string
	// DuckDBMaxTempSize bounds the spill directory, as a DuckDB size literal.
	DuckDBMaxTempSize string
}

// HTTP carries the listener timeouts. They exist as configuration rather than
// as literals so the metrics endpoint cannot be the thing that hangs a pod.
type HTTP struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// Bracket is a rank segment. It is declared here as a plain string because the
// config package must not depend on the artifact model; internal/aggmodel
// carries the canonical, validated set.
type Bracket = string

// envPrefix scopes every variable to this project. Without it a generic name
// like RIOT_REGION reads as a global in a shared cluster ConfigMap, and two
// tools that both define it silently overwrite each other.
const envPrefix = "LOLSTATS_"

// env resolves a short name to its full environment variable name. It is a
// function rather than a constant so a typo in a name cannot be introduced in
// one place and fixed in another.
func env(name string) string { return envPrefix + name }

// AggRequireProvenanceEnv is the variable that carries Aggregate.RequireProvenance.
//
// It is spelled out as a full name rather than through env() because the
// nightly build's own help text names it - `lolstats-aggregate build
// -require-provenance` reports where its default comes from - and a second
// literal for the same switch in another package is a drift waiting to happen.
const AggRequireProvenanceEnv = envPrefix + "AGG_REQUIRE_PROVENANCE"

// Defaults, named so a reader can see what an unset variable resolves to
// without tracing the string literals below.
const (
	defaultEnv           = "dev"
	defaultMetricsAddr   = ":9090"
	defaultShutdownGrace = 20 * time.Second

	defaultRegion        = "EUW"
	defaultPlatformRoute = "euw1"
	defaultRegionalRoute = "europe"
	defaultRiotTimeout   = 10 * time.Second
	defaultMaxAttempts   = 5
	defaultRatePerSecond = 18
	defaultRatePer2Min   = 95

	defaultPostgresMaxConns = 8
	defaultPostgresTimeout  = 5 * time.Second

	defaultRawRoot          = "/var/lib/lolstats/raw"
	defaultRowsPerPart      = 20000
	defaultCompressionLevel = 3

	defaultAggRoot       = "/var/lib/lolstats/agg"
	defaultSchemaVersion = 1
	defaultMinCellN      = 100
	// The feature dataset lives beside the aggregate root rather than inside
	// it: agg/v1 is a frozen reader contract with a published schema and the
	// dataset is neither, so keeping them in separate trees is what stops a
	// reader from assuming the dataset's paths are as stable as the tier
	// list's. See docs/decisions/ADR-012-ingest-match-timelines.md.
	defaultDatasetRoot = "/var/lib/lolstats/datasets"
	// The floor below which a game is called too short to hold a usable
	// timeline. It is the same floor the timeline backfill used to choose the
	// sample, so the ledger and the crawl explain the same exclusions.
	defaultFeatureMinDurationS = 600
	// A fail-closed archive gate: zero tolerant rows unless an operator has
	// measured a reason to allow some. See Aggregate.MaxRejectedRows.
	defaultMaxRejectedRows = 0
	// The same fail-closed rule for the window-sized part of the allowance:
	// unset means "no rate ceiling", not "some generous default rate". See
	// Aggregate.MaxRejectedRate.
	defaultMaxRejectedRate = 0.0
	// The same value as GateConfig's own default, so that a build with no
	// operator input behaves identically whether the gate set comes from here
	// or from internal/aggregate. See Aggregate.MinConfidentShare.
	defaultMinConfidentShare = 0.5
	// The same fail-closed-not-assumed rule for provenance: a build with no
	// operator input records what it can but is not required to prove it, so
	// that an offline build works. No ConfigMap sets it today. See
	// Aggregate.RequireProvenance.
	defaultRequireProvenance = false
	defaultSourceWindow      = 14
	defaultQueueID           = 420
	defaultBracket           = "all"
	defaultReadHeaderLimit   = 10 * time.Second
	defaultReadLimit         = 30 * time.Second
	defaultWriteLimit        = 30 * time.Second
	defaultIdleLimit         = 60 * time.Second
)

// Load reads the configuration from the real environment.
func Load() (Config, error) { return LoadFrom(OS) }

// LoadFrom reads the configuration from an arbitrary environment. Every
// problem found is reported at once: a pod that restarts five times to
// discover five missing variables wastes five minutes of a nightly window.
func LoadFrom(getenv Getenv) (Config, error) {
	r := &reader{env: getenv}

	cfg := Config{
		Env:           r.oneOf(env("ENV"), defaultEnv, "dev", "staging", "prod"),
		LogLevel:      r.level(env("LOG_LEVEL"), slog.LevelInfo),
		LogFormat:     Format(r.oneOf(env("LOG_FORMAT"), string(FormatJSON), string(FormatJSON), string(FormatText))),
		MetricsAddr:   r.str(env("METRICS_ADDR"), defaultMetricsAddr),
		ShutdownGrace: r.dur(env("SHUTDOWN_GRACE"), defaultShutdownGrace),

		Riot: Riot{
			APIKey:           r.str(env("RIOT_API_KEY"), ""),
			Region:           strings.ToUpper(r.str(env("RIOT_REGION"), defaultRegion)),
			PlatformRoute:    strings.ToLower(r.str(env("RIOT_PLATFORM_ROUTE"), defaultPlatformRoute)),
			RegionalRoute:    strings.ToLower(r.str(env("RIOT_REGIONAL_ROUTE"), defaultRegionalRoute)),
			Timeout:          r.dur(env("RIOT_TIMEOUT"), defaultRiotTimeout),
			MaxAttempts:      r.integer(env("RIOT_MAX_ATTEMPTS"), defaultMaxAttempts),
			AppRatePerSecond: r.float(env("RIOT_APP_RATE_PER_SECOND"), defaultRatePerSecond),
			AppRatePer2Min:   r.integer(env("RIOT_APP_RATE_PER_2MIN"), defaultRatePer2Min),
			KeyExpiresAt:     r.stamp(env("RIOT_API_KEY_EXPIRES_AT")),
		},
		Postgres: Postgres{
			DSN:         r.str(env("POSTGRES_DSN"), ""),
			MaxConns:    r.int32Range(env("POSTGRES_MAX_CONNS"), defaultPostgresMaxConns, 1, 512),
			ConnTimeout: r.dur(env("POSTGRES_CONN_TIMEOUT"), defaultPostgresTimeout),
		},
		Raw: Raw{
			Root:             r.str(env("RAW_ROOT"), defaultRawRoot),
			RowsPerPart:      r.integer(env("RAW_ROWS_PER_PART"), defaultRowsPerPart),
			CompressionLevel: r.integer(env("RAW_COMPRESSION_LEVEL"), defaultCompressionLevel),
		},
		Aggregate: Aggregate{
			Root:              r.str(env("AGG_ROOT"), defaultAggRoot),
			SchemaVersion:     r.integer(env("AGG_SCHEMA_VERSION"), defaultSchemaVersion),
			MinCellN:          r.integer(env("AGG_MIN_CELL_N"), defaultMinCellN),
			MaxRejectedRows:   r.nonNegativeInteger(env("AGG_MAX_REJECTED_ROWS"), defaultMaxRejectedRows),
			MaxRejectedRate:   r.nonNegativeShare(env("AGG_MAX_REJECTED_RATE"), defaultMaxRejectedRate),
			MinConfidentShare: r.share(env("AGG_MIN_CONFIDENT_SHARE"), defaultMinConfidentShare),
			RequireProvenance: r.boolean(AggRequireProvenanceEnv, defaultRequireProvenance),
			SourceWindowDays:  r.integer(env("AGG_SOURCE_WINDOW_DAYS"), defaultSourceWindow),
			Bracket:           r.str(env("AGG_BRACKET"), defaultBracket),
			QueueID:           r.integer(env("AGG_QUEUE_ID"), defaultQueueID),
			Patch:             r.str(env("AGG_PATCH"), ""),

			DatasetRoot:         r.str(env("AGG_DATASET_ROOT"), defaultDatasetRoot),
			FeatureMinDurationS: r.integer(env("AGG_FEATURE_MIN_DURATION_S"), defaultFeatureMinDurationS),

			DuckDBMemoryLimit: r.str(env("AGG_DUCKDB_MEMORY_LIMIT"), ""),
			DuckDBThreads:     r.integer(env("AGG_DUCKDB_THREADS"), 0),
			DuckDBTempDir:     r.str(env("AGG_DUCKDB_TEMP_DIR"), ""),
			DuckDBMaxTempSize: r.str(env("AGG_DUCKDB_MAX_TEMP_SIZE"), ""),
		},
		HTTP: HTTP{
			ReadHeaderTimeout: r.dur(env("HTTP_READ_HEADER_TIMEOUT"), defaultReadHeaderLimit),
			ReadTimeout:       r.dur(env("HTTP_READ_TIMEOUT"), defaultReadLimit),
			WriteTimeout:      r.dur(env("HTTP_WRITE_TIMEOUT"), defaultWriteLimit),
			IdleTimeout:       r.dur(env("HTTP_IDLE_TIMEOUT"), defaultIdleLimit),
		},
	}

	if err := errors.Join(r.errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// reader accumulates problems instead of returning on the first one.
type reader struct {
	env  Getenv
	errs []error
}

func (r *reader) fail(key, why string) {
	r.errs = append(r.errs, fmt.Errorf("%s: %s", key, why))
}

func (r *reader) raw(key string) (string, bool) {
	v, ok := r.env(key)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func (r *reader) str(key, def string) string {
	if v, ok := r.raw(key); ok {
		return v
	}
	return def
}

func (r *reader) oneOf(key, def string, allowed ...string) string {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	r.fail(key, fmt.Sprintf("%q is not one of %s", v, strings.Join(allowed, ", ")))
	return def
}

// boolean is for operator switches whose off value is the default: an unset or
// empty variable leaves the switch as the code sets it, and a value that is not
// a boolean is reported rather than guessed, because a switch that silently
// reads as "off" is indistinguishable from one nobody set.
func (r *reader) boolean(key string, def bool) bool {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.fail(key, "is not a boolean such as true or false")
		return def
	}
	return b
}

func (r *reader) dur(key string, def time.Duration) time.Duration {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.fail(key, "is not a duration such as 30s or 2m")
		return def
	}
	return d
}

func (r *reader) integer(key string, def int) int {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.fail(key, "is not an integer")
		return def
	}
	return n
}

// nonNegativeInteger is for counts where a negative value would invert the
// meaning of the setting rather than be wrong loudly: a "maximum of -1" is a
// maximum that allows nothing, which reads as a configured allowance but
// behaves as the strictest possible gate.
func (r *reader) nonNegativeInteger(key string, def int) int {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	switch {
	case err != nil:
		r.fail(key, "is not an integer")
	case n < 0:
		r.fail(key, "must not be negative")
	default:
		return n
	}
	return def
}

// share is for a fraction of a whole, such as the smallest share of cells that
// must survive suppression. A value outside (0,1] is refused loudly rather than
// clamped: 0 would mean "publish nothing" and a value above 1 can never be met,
// and both read like a configured threshold while behaving as a gate that no
// archive can satisfy or one that accepts anything.
func (r *reader) share(key string, def float64) float64 {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	switch {
	case err != nil:
		r.fail(key, "is not a number")
	case f <= 0 || f > 1:
		r.fail(key, "must be greater than 0 and at most 1")
	default:
		return f
	}
	return def
}

// nonNegativeShare is share's sibling for a rate whose zero is meaningful:
// zero is "off", the fail-closed default of a gate that has a second, absolute
// part (see Aggregate.MaxRejectedRate). Everything above zero is refused
// outside (0,1] because a ceiling above the whole window is not a measured
// allowance at all - it would permit every row to be rejected while looking
// like a number an operator chose.
func (r *reader) nonNegativeShare(key string, def float64) float64 {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	switch {
	case err != nil:
		r.fail(key, "is not a number")
	case f < 0 || f > 1:
		r.fail(key, "must be at least 0 and at most 1")
	default:
		return f
	}
	return def
}

// int32Range is for values a dependency demands at 32-bit width, such as
// pgxpool's MaxConns. Parsing with a bit size of 32 bounds the value before it
// is narrowed, so there is no conversion that can overflow silently.
func (r *reader) int32Range(key string, def int32, min, max int64) int32 {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	switch {
	case err != nil:
		r.fail(key, "is not an integer")
	case n < min || n > max:
		r.fail(key, fmt.Sprintf("must be between %d and %d", min, max))
	default:
		// n is within int32 by construction: ParseInt was given bitSize 32
		// and the explicit bounds check above narrows it further.
		return int32(n)
	}
	return def
}

func (r *reader) float(key string, def float64) float64 {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		r.fail(key, "is not a number")
		return def
	}
	return f
}

func (r *reader) stamp(key string) time.Time {
	v, ok := r.raw(key)
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		r.fail(key, "is not an RFC3339 timestamp")
		return time.Time{}
	}
	return t.UTC()
}

func (r *reader) level(key string, def slog.Level) slog.Level {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		r.fail(key, "is not one of debug, info, warn, error")
		return def
	}
}
