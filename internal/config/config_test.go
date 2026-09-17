package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// env builds a Getenv from a map, which is why LoadFrom exists: mutating the
// real environment would make these tests order-dependent.
func envFrom(pairs map[string]string) Getenv {
	return func(key string) (string, bool) {
		v, ok := pairs[key]
		return v, ok
	}
}

func TestLoadFromUsesDefaults(t *testing.T) {
	cfg, err := LoadFrom(envFrom(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want dev", cfg.Env)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != FormatJSON {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
	if cfg.Riot.Region != "EUW" {
		t.Errorf("Riot.Region = %q, want EUW", cfg.Riot.Region)
	}
	if cfg.Riot.APIKey != "" {
		t.Errorf("Riot.APIKey = %q, want empty", cfg.Riot.APIKey)
	}
	if cfg.Aggregate.MinCellN != defaultMinCellN {
		t.Errorf("Aggregate.MinCellN = %d, want %d", cfg.Aggregate.MinCellN, defaultMinCellN)
	}
	if cfg.Aggregate.MaxRejectedRows != defaultMaxRejectedRows {
		t.Errorf("Aggregate.MaxRejectedRows = %d, want %d: an unset allowance must stay fail-closed",
			cfg.Aggregate.MaxRejectedRows, defaultMaxRejectedRows)
	}
	if cfg.Aggregate.MinConfidentShare != defaultMinConfidentShare {
		t.Errorf("Aggregate.MinConfidentShare = %v, want %v: an unset share must stay at the strict default",
			cfg.Aggregate.MinConfidentShare, defaultMinConfidentShare)
	}
	if cfg.Aggregate.QueueID != 420 {
		t.Errorf("Aggregate.QueueID = %d, want 420", cfg.Aggregate.QueueID)
	}
	if cfg.Postgres.DSN != "" {
		t.Errorf("Postgres.DSN = %q, want empty", cfg.Postgres.DSN)
	}
}

func TestLoadFromOverrides(t *testing.T) {
	cfg, err := LoadFrom(envFrom(map[string]string{
		"LOLSTATS_ENV":                      "prod",
		"LOLSTATS_LOG_LEVEL":                "debug",
		"LOLSTATS_LOG_FORMAT":               "text",
		"LOLSTATS_RIOT_REGION":              "euw",
		"LOLSTATS_RIOT_PLATFORM_ROUTE":      "EUW1",
		"LOLSTATS_RIOT_TIMEOUT":             "3s",
		"LOLSTATS_RIOT_APP_RATE_PER_SECOND": "4.5",
		"LOLSTATS_RIOT_API_KEY_EXPIRES_AT":  "2026-10-01T00:00:00Z",
		"LOLSTATS_AGG_MIN_CELL_N":           "250",
		"LOLSTATS_AGG_MAX_REJECTED_ROWS":    "25",
		"LOLSTATS_AGG_MIN_CONFIDENT_SHARE":  "0.15",
		"LOLSTATS_AGG_DUCKDB_MEMORY_LIMIT":  "768MiB",
		"LOLSTATS_AGG_DUCKDB_THREADS":       "3",
		"LOLSTATS_AGG_DUCKDB_TEMP_DIR":      "/tmp",
		"LOLSTATS_AGG_DUCKDB_MAX_TEMP_SIZE": "2GiB",
		"LOLSTATS_POSTGRES_MAX_CONNS":       "3",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want prod", cfg.Env)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.LogFormat != FormatText {
		t.Errorf("LogFormat = %q, want text", cfg.LogFormat)
	}
	// Region and routes are normalised so that a lowercase spelling in a
	// ConfigMap cannot produce a different cache key or a 404 from Riot.
	if cfg.Riot.Region != "EUW" {
		t.Errorf("Riot.Region = %q, want EUW", cfg.Riot.Region)
	}
	if cfg.Riot.PlatformRoute != "euw1" {
		t.Errorf("Riot.PlatformRoute = %q, want euw1", cfg.Riot.PlatformRoute)
	}
	if cfg.Riot.Timeout != 3*time.Second {
		t.Errorf("Riot.Timeout = %v, want 3s", cfg.Riot.Timeout)
	}
	if cfg.Riot.AppRatePerSecond != 4.5 {
		t.Errorf("Riot.AppRatePerSecond = %v, want 4.5", cfg.Riot.AppRatePerSecond)
	}
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC); !cfg.Riot.KeyExpiresAt.Equal(want) {
		t.Errorf("Riot.KeyExpiresAt = %v, want %v", cfg.Riot.KeyExpiresAt, want)
	}
	if cfg.Aggregate.MinCellN != 250 {
		t.Errorf("Aggregate.MinCellN = %d, want 250", cfg.Aggregate.MinCellN)
	}
	if cfg.Aggregate.MaxRejectedRows != 25 {
		t.Errorf("Aggregate.MaxRejectedRows = %d, want 25", cfg.Aggregate.MaxRejectedRows)
	}
	if cfg.Aggregate.MinConfidentShare != 0.15 {
		t.Errorf("Aggregate.MinConfidentShare = %v, want 0.15", cfg.Aggregate.MinConfidentShare)
	}
	// The DuckDB bounds are read as written: the engine is the only place that
	// decides what a size literal means, and "empty means the engine default" is
	// what an unset ConfigMap projects.
	if cfg.Aggregate.DuckDBMemoryLimit != "768MiB" {
		t.Errorf("Aggregate.DuckDBMemoryLimit = %q, want 768MiB", cfg.Aggregate.DuckDBMemoryLimit)
	}
	if cfg.Aggregate.DuckDBThreads != 3 {
		t.Errorf("Aggregate.DuckDBThreads = %d, want 3", cfg.Aggregate.DuckDBThreads)
	}
	if cfg.Aggregate.DuckDBTempDir != "/tmp" {
		t.Errorf("Aggregate.DuckDBTempDir = %q, want /tmp", cfg.Aggregate.DuckDBTempDir)
	}
	if cfg.Aggregate.DuckDBMaxTempSize != "2GiB" {
		t.Errorf("Aggregate.DuckDBMaxTempSize = %q, want 2GiB", cfg.Aggregate.DuckDBMaxTempSize)
	}
	if cfg.Postgres.MaxConns != 3 {
		t.Errorf("Postgres.MaxConns = %d, want 3", cfg.Postgres.MaxConns)
	}
}

func TestLoadFromReportsEveryProblemAtOnce(t *testing.T) {
	_, err := LoadFrom(envFrom(map[string]string{
		"LOLSTATS_LOG_LEVEL":             "chatty",
		"LOLSTATS_RIOT_TIMEOUT":          "soon",
		"LOLSTATS_AGG_MIN_CELL_N":        "many",
		"LOLSTATS_AGG_DUCKDB_THREADS":    "many",
		"LOLSTATS_AGG_MAX_REJECTED_ROWS": "-1",
		// A share of 0 would mean "publish nothing" and a share above 1 can
		// never be met; both must be refused rather than clamped.
		"LOLSTATS_AGG_MIN_CONFIDENT_SHARE": "1.5",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, key := range []string{"LOLSTATS_LOG_LEVEL", "LOLSTATS_RIOT_TIMEOUT", "LOLSTATS_AGG_MIN_CELL_N", "LOLSTATS_AGG_DUCKDB_THREADS", "LOLSTATS_AGG_MAX_REJECTED_ROWS", "LOLSTATS_AGG_MIN_CONFIDENT_SHARE"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not name %s: %v", key, err)
		}
	}
}

func TestLoadFromIgnoresBlankValues(t *testing.T) {
	// A ConfigMap that projects an empty variable is common; it should mean
	// "unset", not "override with the empty string".
	cfg, err := LoadFrom(envFrom(map[string]string{
		"LOLSTATS_RIOT_REGION":    "",
		"LOLSTATS_AGG_MIN_CELL_N": "   ",
		"LOLSTATS_ENV":            "",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Riot.Region != "EUW" {
		t.Errorf("Riot.Region = %q, want EUW", cfg.Riot.Region)
	}
	if cfg.Aggregate.MinCellN != defaultMinCellN {
		t.Errorf("Aggregate.MinCellN = %d, want %d", cfg.Aggregate.MinCellN, defaultMinCellN)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want dev", cfg.Env)
	}
}

func TestValidationIsPerComponent(t *testing.T) {
	// A cron job that only reads the raw archive has no key, and must not be
	// forced to pretend it has one.
	base, err := LoadFrom(envFrom(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := base.Postgres.Validate(); err == nil {
		t.Error("Postgres.Validate accepted an empty DSN")
	}
	if err := base.Riot.Validate(); err == nil {
		t.Error("Riot.Validate accepted an empty key")
	}

	ok, err := LoadFrom(envFrom(map[string]string{
		"LOLSTATS_RIOT_API_KEY": "RGAPI-example",
		"LOLSTATS_POSTGRES_DSN": "postgres://lolstats@db:5432/lolstats",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ok.Riot.Validate(); err != nil {
		t.Errorf("Riot.Validate: %v", err)
	}
	if err := ok.Postgres.Validate(); err != nil {
		t.Errorf("Postgres.Validate: %v", err)
	}
}
