package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/sql/migrations"
)

// migrationLockKey is an arbitrary but fixed advisory-lock key. It only has to
// be stable across the processes that might migrate the same database.
const migrationLockKey int64 = 0x1064_6c6f_6c73 // "lols"

const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    bigint      PRIMARY KEY,
    name       text        NOT NULL,
    checksum   text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`

// migrationFile is one parsed `<version>_<name>.up.sql`.
type migrationFile struct {
	version  int64
	name     string
	body     string
	checksum string
}

var migrationNamePattern = regexp.MustCompile(`^(\d+)_([A-Za-z0-9._-]+)\.up\.sql$`)

// MigrateUp applies every unapplied forward migration and returns their
// versions.
//
// It is forward-only by design. The `.down.sql` files exist so that the reverse
// of a change is written down while the reasoning is fresh, but the tool does
// not run them: a production database is not rolled back, it is replaced from
// the archive, and a rollback path that is never exercised in production is a
// liability dressed as a safety feature.
//
// Three properties matter for a restart:
//
//   - A session advisory lock serialises concurrent migrators, so two replicas
//     starting at once do not race. The lock is held on one connection for the
//     whole run and released by closing it.
//
//   - Each migration's version row is inserted inside the same explicit
//     transaction as the migration body, so a crash mid-way leaves the version
//     unapplied and the run is simply repeated. The bodies carry their own
//     BEGIN/COMMIT (0001 is frozen and written that way); the wrappers are
//     stripped and replaced so that the bookkeeping and the DDL commit together
//     instead of the DDL committing first.
//
//   - The checksum of every applied migration is verified before anything runs.
//     The migration files are part of the contract, so a body that changes after
//     it has been applied means two environments disagree about the schema while
//     both claim the same version.
func MigrateUp(ctx context.Context, dsn string) ([]int64, error) {
	if dsn == "" {
		return nil, errors.New("store: MigrateUp: DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: MigrateUp: open: %w", err)
	}
	defer func() { _ = db.Close() }()

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: MigrateUp: connect: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return nil, fmt.Errorf("store: MigrateUp: lock: %w", err)
	}
	// The lock is session-scoped and the session is the connection, so closing
	// the connection releases it. Unlocking explicitly first keeps that
	// independent of Close's behaviour.
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if _, err := conn.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		return nil, fmt.Errorf("store: MigrateUp: bookkeeping table: %w", err)
	}

	available, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}

	touched := make([]int64, 0, len(available))
	for _, m := range available {
		if prev, ok := applied[m.version]; ok {
			if prev.checksum != m.checksum {
				return touched, fmt.Errorf("store: MigrateUp: migration %d (%s) was applied with checksum %s but the embedded file is %s",
					m.version, m.name, prev.checksum[:12], m.checksum[:12])
			}
			continue
		}
		script := wrapMigration(m)
		if _, err := conn.ExecContext(ctx, script); err != nil {
			return touched, fmt.Errorf("store: MigrateUp: migration %d (%s): %w", m.version, m.name, err)
		}
		touched = append(touched, m.version)
	}
	return touched, nil
}

type appliedMigration struct {
	name     string
	checksum string
}

func appliedMigrations(ctx context.Context, conn *sql.Conn) (map[int64]appliedMigration, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: MigrateUp: read applied: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[int64]appliedMigration)
	for rows.Next() {
		var (
			version int64
			rec     appliedMigration
		)
		if err := rows.Scan(&version, &rec.name, &rec.checksum); err != nil {
			return nil, fmt.Errorf("store: MigrateUp: read applied: %w", err)
		}
		applied[version] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: MigrateUp: read applied: %w", err)
	}
	return applied, nil
}

// loadMigrations reads the embedded files and returns them in version order.
func loadMigrations() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("store: MigrateUp: read embedded migrations: %w", err)
	}
	out := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		match := migrationNamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("store: MigrateUp: %s: bad version: %w", entry.Name(), err)
		}
		body, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("store: MigrateUp: %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(body)
		out = append(out, migrationFile{
			version:  version,
			name:     match[2],
			body:     string(body),
			checksum: hex.EncodeToString(sum[:]),
		})
	}
	if len(out) == 0 {
		return nil, errors.New("store: MigrateUp: no embedded migrations")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("store: MigrateUp: duplicate migration version %d", out[i].version)
		}
	}
	return out, nil
}

// wrapMigration turns one file into a single transaction that applies the
// migration and records it.
//
// The embedded bodies are written with their own BEGIN/COMMIT so that they can
// also be pasted into a psql session by hand; exactly one leading BEGIN and one
// trailing COMMIT are removed here so the bookkeeping insert joins the same
// transaction. Both wrappers are optional: a file without them is still applied.
func wrapMigration(m migrationFile) string {
	body := m.body
	trimmed := strings.TrimSpace(body)
	trimmed = strings.TrimPrefix(trimmed, "BEGIN;")
	trimmed = strings.TrimSuffix(trimmed, "COMMIT;")
	trimmed = strings.TrimSpace(trimmed)

	var sb strings.Builder
	sb.WriteString("BEGIN;\n")
	sb.WriteString(trimmed)
	sb.WriteString("\n")
	// now() rather than a bound parameter: the whole script is executed as one
	// statement batch, and a batch with placeholders is not a simple query.
	fmt.Fprintf(&sb, "INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (%d, '%s', '%s', now());\n",
		m.version, strings.ReplaceAll(m.name, "'", "''"), m.checksum)
	sb.WriteString("COMMIT;\n")
	return sb.String()
}

// MigrationVersions lists the versions the binary knows about, in order. It is
// used by the `migrate status` path and by tests that assert the embedded set
// rather than a hard-coded number.
func MigrationVersions() ([]int64, error) {
	files, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	versions := make([]int64, 0, len(files))
	for _, f := range files {
		versions = append(versions, f.version)
	}
	return versions, nil
}
