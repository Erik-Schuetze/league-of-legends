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
	// Trailing window of days of raw data a build reads.
	SourceWindowDays int
	// v1 publishes a single unsegmented bracket; the value is still explicit
	// because it is part of every artifact path.
	Bracket Bracket
	QueueID int
	// Empty means "use the newest patch found in the raw archive".
	Patch string
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

	defaultAggRoot         = "/var/lib/lolstats/agg"
	defaultSchemaVersion   = 1
	defaultMinCellN        = 100
	defaultSourceWindow    = 14
	defaultQueueID         = 420
	defaultBracket         = "all"
	defaultReadHeaderLimit = 10 * time.Second
	defaultReadLimit       = 30 * time.Second
	defaultWriteLimit      = 30 * time.Second
	defaultIdleLimit       = 60 * time.Second
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
			Root:             r.str(env("AGG_ROOT"), defaultAggRoot),
			SchemaVersion:    r.integer(env("AGG_SCHEMA_VERSION"), defaultSchemaVersion),
			MinCellN:         r.integer(env("AGG_MIN_CELL_N"), defaultMinCellN),
			SourceWindowDays: r.integer(env("AGG_SOURCE_WINDOW_DAYS"), defaultSourceWindow),
			Bracket:          r.str(env("AGG_BRACKET"), defaultBracket),
			QueueID:          r.integer(env("AGG_QUEUE_ID"), defaultQueueID),
			Patch:            r.str(env("AGG_PATCH"), ""),
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
