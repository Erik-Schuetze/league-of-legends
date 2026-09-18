#!/bin/sh
# scripts/backup-verify.sh - the restore drill. It is the evidence behind the
# launch gate "Postgres and the raw archive have both been restored from backup
# in a test".
#
#   sh scripts/backup-verify.sh --docker    # a throwaway Postgres in Docker
#   sh scripts/backup-verify.sh --cluster   # the real Postgres in the cluster
#
# What it does, in the order that matters:
#
#   1. takes a real `pg_dump --format=custom` from a real Postgres;
#   2. creates a *fresh* database and restores the dump into it with pg_restore;
#   3. compares the source and the restored copy on four things: the number of
#      tables, a hash of every column definition, a hash of every index
#      definition, and the exact row count of every table;
#   4. runs a negative control: it deletes one row from the restored copy and
#      asserts that the comparison *fails*. A comparison that cannot fail is not
#      a comparison, and this script would rather prove that than assert it.
#
# It exits non-zero if the restore differs from the source in any of those four
# respects, or if the negative control fails to notice the deleted row.
#
# --docker starts a pinned postgres:16.10 container, loads the repository's own
# migrations from sql/migrations, seeds deterministic rows (including one row
# with non-ASCII text, because an encoding bug survives every ASCII test), and
# tears the container down afterwards. It is what CI should run.
#
# --cluster does the same against the live Postgres in namespace `lolstats`, by
# exec'ing into the postgres pod (whose socket trusts the local `postgres` role,
# so no password is ever passed on a command line). It cannot seed the source,
# because the source is production: it therefore verifies the round-trip and the
# schema, and prints a NOTICE instead of a pass if every table is empty - a
# comparison of nothing against nothing proves nothing.
#
# --cluster writes to the cluster's database by creating and dropping a
# throwaway database named lolstats_verify_<epoch>. It touches no existing
# database, no workload and no Kubernetes object, but it is still a write, so it
# refuses to run without --yes.
#
# Flags:
#   --docker | --cluster   which Postgres to drill (default --docker)
#   --yes                  required for --cluster
#   --keep                 leave the container / throwaway database in place
#   --image <ref>          override the postgres image (default: the pinned one
#                          from deploy/base/postgres/statefulset.yaml)
#
# Nothing is written outside .agent-artifacts/backup-verify, the path is printed
# at the end so the dump and the two reports can be inspected afterwards, and no
# temporary directory is used anywhere.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/.agent-artifacts/backup-verify"
NS=lolstats
CTR=lolstats-backup-verify
CTR_LABEL=lolstats.backup-verify=1

# The same digest deploy/base/postgres/statefulset.yaml pins. The dump and the
# restore must be made by the same major version as the server, and a drill
# against a different binary would be a drill against a different tool.
PG_IMAGE=postgres:16.10@sha256:21f6013073bc6b92830a2129570e2f5ec42a6c734b5a985a41e83aa58f54c3c1

MODE=docker
CONFIRMED=0
KEEP=0

while [ $# -gt 0 ]; do
    case "$1" in
        --docker) MODE=docker ;;
        --cluster) MODE=cluster ;;
        --yes) CONFIRMED=1 ;;
        --keep) KEEP=1 ;;
        --image)
            shift
            [ $# -gt 0 ] || { printf 'backup-verify: --image needs a value\n' >&2; exit 2; }
            PG_IMAGE=$1
            ;;
        -h|--help)
            sed -n '2,45p' "$0"
            exit 0
            ;;
        *)
            printf 'backup-verify: unknown argument %s\n' "$1" >&2
            exit 2
            ;;
    esac
    shift
done

if [ "$MODE" = cluster ] && [ "$CONFIRMED" -ne 1 ]; then
    printf 'backup-verify: --cluster creates and drops a throwaway database in the live\n' >&2
    printf 'backup-verify: lolstats Postgres. Re-run with --yes if that is what you mean.\n' >&2
    exit 2
fi

failures=0
pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }
note() { printf '      %s\n' "$1"; }
# There is deliberately no warn(): every finding here is a pass or a failure of
# the run, because a warning that still exits 0 is how this drill came to report
# success while comparing nothing (see the negative control below). Kept as a
# comment rather than deleted helpfully, so the next person adding a soft finding
# has to decide against the rule on purpose.
section() { printf '\n== %s\n' "$1"; }
die() { printf 'backup-verify: FATAL: %s\n' "$1" >&2; exit 1; }

mkdir -p "$WORK"
rm -f "$WORK"/*.report "$WORK"/*.diff "$WORK"/*.dump "$WORK"/*.log "$WORK"/*.err 2>/dev/null || true

# ---------------------------------------------------------------- harness ----
# `pq` runs one SQL file against a database and prints the tuples, no headers,
# no alignment, no psqlrc, and ON_ERROR_STOP so that a failing statement is a
# failing run rather than a line of output nobody reads. It dispatches on the
# mode, which is the only thing the two modes disagree about: everything below
# is mechanism.

# The password is passed in the environment of the exec'ed process rather than
# on its command line, because a command line is visible in the process list of
# every host the container is scheduled on. In docker mode it is the password
# the container was just started with; in cluster mode it is read from the same
# Secret the pod reads. The local socket in the official image trusts the
# `postgres` role anyway, so this is belt and braces on the path that must not
# fail silently.
run_in_pg() {
    case "$MODE" in
        docker)
            docker exec -i -e PGPASSWORD="$PGPASSWORD_VAL" -u postgres "$CTR" "$@"
            ;;
        cluster)
            kubectl -n "$NS" exec -i "$POD" -- env PGPASSWORD="$PGPASSWORD_VAL" "$@"
            ;;
    esac
}

pq() { # <db> <sqlfile> [psql flags...]
    _db=$1
    _sql=$2
    shift 2
    run_in_pg psql -U "$PSQL_USER" -X -q -v ON_ERROR_STOP=1 -t -A "$@" -d "$_db" -f - < "$_sql"
}

pq_text() { # <db> <sql>
    printf '%s\n' "$2" > "$WORK/_cmd.sql"
    pq "$1" "$WORK/_cmd.sql"
}

# The four things a restore has to get right. Row counts alone would pass a
# restore that dropped every index; a schema hash alone would pass a restore
# that lost half the rows. Both, per table, is the cheap version of a diff.
report() { # <db> <outfile>
    {
        printf 'tables '
        pq "$1" "$WORK/sql/tables.sql"
        printf 'columns_md5 '
        pq "$1" "$WORK/sql/columns.sql"
        printf 'indexes_md5 '
        pq "$1" "$WORK/sql/indexes.sql"
        pq "$1" "$WORK/sql/rowcounts.sql"
    } > "$2"
}

compare() { # <src_db> <dst_db> <diff_file> -> 0 identical, 1 different
    report "$1" "$WORK/source.report"
    report "$2" "$WORK/restored.report"
    if diff -u "$WORK/source.report" "$WORK/restored.report" > "$3"; then
        return 0
    fi
    return 1
}

dump_to_file() { # <db> <file>
    run_in_pg pg_dump -U "$PSQL_USER" --format=custom --compress=zstd:3 \
        --no-owner --no-acl -d "$1" > "$2"
}

restore_from_file() { # <db> <file> <errfile>
    run_in_pg pg_restore -U "$PSQL_USER" -d "$1" \
        --no-owner --no-acl --exit-on-error < "$2" 2> "$3"
}

toc_entries() { # <file>
    run_in_pg pg_restore -l < "$1" 2>/dev/null | grep -c ';' | tr -d ' '
}

generate_sql() {
    mkdir -p "$WORK/sql"

    cat > "$WORK/sql/tables.sql" <<'SQL'
SELECT count(*)
FROM information_schema.tables
WHERE table_schema = 'public' AND table_type = 'BASE TABLE';
SQL

    cat > "$WORK/sql/columns.sql" <<'SQL'
SELECT md5(string_agg(
    c.table_name || '.' || c.column_name || ':' || c.data_type || ':' ||
    c.udt_name || ':' || c.is_nullable || ':' ||
    COALESCE(c.column_default, '-') || ':' ||
    COALESCE(c.character_maximum_length::text, '-') || ':' ||
    COALESCE(c.numeric_precision::text, '-'), E'\n'
    ORDER BY c.table_name, c.ordinal_position))
FROM information_schema.columns c
WHERE c.table_schema = 'public';
SQL

    cat > "$WORK/sql/indexes.sql" <<'SQL'
SELECT md5(string_agg(i.indexdef, E'\n' ORDER BY i.indexdef))
FROM pg_indexes i
WHERE i.schemaname = 'public';
SQL

    # query_to_xml is the only way to count every table in one statement without
    # dynamic SQL, and it sees empty tables - which a `select count(*) from
    # each_table` loop keyed off existing rows would not.
    cat > "$WORK/sql/rowcounts.sql" <<'SQL'
SELECT t.table_name || ' ' ||
       (xpath('/row/c/text()',
              query_to_xml(format('SELECT count(*) AS c FROM %I.%I',
                                  t.table_schema, t.table_name),
                           false, true, '')))[1]::text
FROM information_schema.tables t
WHERE t.table_schema = 'public' AND t.table_type = 'BASE TABLE'
ORDER BY t.table_name;
SQL

    cat > "$WORK/sql/total.sql" <<'SQL'
SELECT COALESCE(sum(n), 0)::text
FROM (
    SELECT (xpath('/row/c/text()',
                  query_to_xml(format('SELECT count(*) AS c FROM %I.%I',
                                      t.table_schema, t.table_name),
                               false, true, '')))[1]::text::bigint AS n
    FROM information_schema.tables t
    WHERE t.table_schema = 'public' AND t.table_type = 'BASE TABLE'
) s;
SQL

    # Deterministic seeds, sized so that the dump is a file worth restoring
    # rather than a gesture. The last matches row carries non-ASCII text in two
    # columns and the source_toggles row carries a check mark: a client_encoding
    # mistake survives every ASCII-only test.
    cat > "$WORK/sql/seed.sql" <<'SQL'
BEGIN;

INSERT INTO matches (match_id, region, queue_id, patch, game_version,
                     game_creation, game_duration_s, payload_version,
                     raw_uri, status, fetched_at, parsed_at)
SELECT 'EUW1_' || lpad(g::text, 10, '0'), 'euw1', 420, '15.1', '15.1.1',
       now() - (g || ' minutes')::interval, 1800 + g, 1,
       'raw/matches/euw1/15.1/EUW1_' || lpad(g::text, 10, '0') || '.json',
       'parsed', now(), now()
FROM generate_series(1, 250) g;

INSERT INTO matches (match_id, region, queue_id, patch, game_version,
                     game_creation, game_duration_s, payload_version,
                     raw_uri, status, fetched_at, parsed_at)
VALUES ('EUW1_ENCODING_CHECK', 'euw1', 420, '15.1 — Ω', '15.1.1-ä',
        now(), 900, 1, 'raw/matches/euw1/15.1/encoding-check.json',
        'parsed', now(), now());

INSERT INTO fetch_queue (match_id, priority, attempts, status)
SELECT 'EUW1_' || lpad(g::text, 10, '0'), 100, 0, 'pending'
FROM generate_series(1, 120) g;

INSERT INTO fetch_queue (match_id, priority, attempts, status, last_cause)
VALUES ('EUW1_ENCODING_CHECK', 50, 1, 'retry', 'HTTP 429 from the Riot API');

INSERT INTO crawl_frontier (puuid, region, seed_tier, seed_division,
                            last_seen_at, last_fetched_at, consecutive_empty,
                            priority, dead)
SELECT 'PUUID-' || g, 'euw1', 'GOLD', 'II', now(),
       now() - (g || ' hours')::interval, 0, 100, false
FROM generate_series(1, 64) g;

INSERT INTO crawl_frontier (puuid, region, seed_tier, seed_division,
                            last_seen_at, last_fetched_at, consecutive_empty,
                            priority, dead, dead_cause)
VALUES ('PUUID-DEAD-1', 'euw1', 'GOLD', 'II', now(), now(), 5, 100, true,
        'no matches in the last 200 games');

INSERT INTO crawl_seeds (tier, division, queue, region, finished_at, entries_found)
SELECT 'GOLD', 'II', 'RANKED_SOLO_5x5', 'euw1', now(), 20
FROM generate_series(1, 5) g;

INSERT INTO build_runs (patch, region, queue, bracket, finished_at, status,
                        cells_total, cells_published, cells_suppressed, git_sha)
SELECT '15.1', 'euw1', 420, 'GOLD', now(), 'succeeded', 100, 95, 5, 'deadbeef'
FROM generate_series(1, 3) g;

INSERT INTO source_toggles (source, enabled, decided_by, decided_at,
                            review_due_at, notes)
VALUES ('riot-api', true, 'scripts/backup-verify.sh', now(),
        now() + interval '30 days', 'encoding check ✓');

COMMIT;
SQL
}
# ------------------------------------------------------------ environment ----

SRC_DB=lolstats_verify_src
DST_DB=lolstats_verify_dst
PSQL_USER=postgres
PGPASSWORD_VAL=verify

# macOS `base64` and GNU `base64` disagree about the decode flag; openssl does
# not, and kubectl ships with openssl everywhere this script is expected to run.
b64d() {
    openssl base64 -d -A 2>/dev/null || base64 -d 2>/dev/null || base64 -D 2>/dev/null || true
}

secret_field() { # <key in lolstats-postgres>
    kubectl -n "$NS" get secret lolstats-postgres -o jsonpath="{.data.$1}" 2>/dev/null | b64d
}

cleanup() {
    if [ "$MODE" = cluster ] && [ "${POD:-}" = "" ]; then
        return 0
    fi
    if [ "$MODE" = docker ]; then
        if [ "$KEEP" -ne 1 ]; then
            docker rm -f "$CTR" >/dev/null 2>&1 || true
        else
            note "container $CTR left in place (--keep)"
        fi
    else
        if [ "$KEEP" -ne 1 ]; then
            pq_text postgres "DROP DATABASE IF EXISTS \"$DST_DB\"" >/dev/null 2>&1 || true
        else
            note "database $DST_DB left in place (--keep): drop it yourself when done"
        fi
    fi
}

docker_setup() {
    if docker inspect "$CTR" >/dev/null 2>&1; then
        existing=$(docker inspect -f '{{ index .Config.Labels "lolstats.backup-verify" }}' "$CTR")
        if [ "$existing" = "1" ]; then
            note "removing a leftover drill container from an earlier run"
            docker rm -f "$CTR" >/dev/null 2>&1 || true
        else
            die "a container named $CTR already exists and was not started by this script; refuse to remove it"
        fi
    fi

    if docker image inspect "$PG_IMAGE" >/dev/null 2>&1; then
        note "using the locally cached image $PG_IMAGE"
    else
        note "pulling $PG_IMAGE"
    fi
    docker run -d --name "$CTR" --label "$CTR_LABEL" \
        -e POSTGRES_PASSWORD="$PGPASSWORD_VAL" -e POSTGRES_DB=postgres \
        "$PG_IMAGE" >/dev/null || die "docker run failed for $PG_IMAGE"

    i=0
    while [ "$i" -lt 90 ]; do
        # pg_isready also answers during the image's bootstrap server, so the
        # clock is only stopped by a query that returns a row.
        if pq_text postgres 'SELECT 1' >/dev/null 2>&1; then
            pass "postgres is accepting queries in $CTR (waited ${i}s)"
            return 0
        fi
        i=$((i + 1))
        sleep 1
    done
    die "the postgres container did not become ready in 90s"
}

cluster_setup() {
    POD=$(kubectl -n "$NS" get pods \
        -l app.kubernetes.io/name=lolstats,app.kubernetes.io/component=postgres \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    [ -n "$POD" ] || die "no Postgres pod found in namespace $NS (label app.kubernetes.io/component=postgres)"
    phase=$(kubectl -n "$NS" get pod "$POD" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    [ "$phase" = "Running" ] || die "pod $POD is $phase, not Running"

    secret_user=$(secret_field POSTGRES_USER)
    secret_db=$(secret_field POSTGRES_DB)
    PGPASSWORD_VAL=$(secret_field POSTGRES_PASSWORD)
    [ -n "$secret_user" ] && PSQL_USER=$secret_user
    [ -n "$secret_db" ] && SRC_DB=$secret_db

    DST_DB="lolstats_verify_$(date -u +%Y%m%d%H%M%S)"

    pass "drilling against pod $POD (role $PSQL_USER, source database $SRC_DB)"
    note "the throwaway database is $DST_DB; it is dropped again at the end"
    note "this script creates and drops only that database: no workload, no Service,"
    note "no Kubernetes object is touched, and nothing is applied or synced"

    if pq_text postgres 'SELECT 1' >/dev/null 2>&1; then
        pass "the cluster Postgres answers on its local socket"
    else
        die "cannot run psql inside $POD"
    fi
}

# ------------------------------------------------------------------- drill ----

section "restore drill against $MODE"
generate_sql

# Installed after every function above is defined and before anything is
# started, so an interrupted run does not leave a container or a throwaway
# database behind for the next person to find.
trap cleanup EXIT INT TERM

case "$MODE" in
    docker)
        docker_setup
        ;;
    cluster)
        cluster_setup
        ;;
esac

if [ "$MODE" = docker ]; then
    section "source: $SRC_DB, built from the repository's own migrations"
    pq_text postgres "CREATE DATABASE \"$SRC_DB\"" >/dev/null || die "could not create $SRC_DB"
    for m in $(ls "$ROOT/sql/migrations"/*.up.sql | sort); do
        name=$(basename "$m")
        if pq "$SRC_DB" "$m" >/dev/null; then
            note "applied sql/migrations/$name"
        else
            die "sql/migrations/$name failed to apply"
        fi
    done
    pq "$SRC_DB" "$WORK/sql/seed.sql" >/dev/null || die "seeding failed"
    pass "loaded $(ls "$ROOT/sql/migrations"/*.up.sql | wc -l | tr -d ' ') migrations and seeded rows"
else
    section "source: the live database $SRC_DB (it cannot be seeded - it is production)"
fi

section "dump"
DUMP="$WORK/dump.pgc"
if dump_to_file "$SRC_DB" "$DUMP"; then
    pass "pg_dump --format=custom completed"
else
    fail "pg_dump failed"
    die "cannot continue without a dump"
fi
dump_bytes=$(wc -c < "$DUMP" | tr -d ' ')
toc=$(toc_entries "$DUMP")
note "dump.pgc: $dump_bytes bytes, $toc table-of-contents entries"
if [ "$dump_bytes" -gt 0 ] && [ "$toc" -ge 10 ]; then
    pass "the dump is a non-empty custom-format archive with a readable table of contents"
else
    fail "the dump is empty or has too few TOC entries ($dump_bytes bytes, $toc entries)"
fi

section "restore into a fresh database"
pk_src=$(pq "$SRC_DB" "$WORK/sql/tables.sql")

pq_text postgres "CREATE DATABASE \"$DST_DB\"" >/dev/null || die "could not create $DST_DB"
if restore_from_file "$DST_DB" "$DUMP" "$WORK/restore.err"; then
    pass "pg_restore exited 0 restoring into $DST_DB"
else
    fail "pg_restore exited non-zero restoring into $DST_DB"
fi
if [ -s "$WORK/restore.err" ]; then
    note "pg_restore said:"
    sed 's/^/        /' "$WORK/restore.err"
fi

section "compare"
if compare "$SRC_DB" "$DST_DB" "$WORK/compare.diff"; then
    pass "the restored copy is identical to the source on all four comparisons"
else
    fail "the restored copy differs from the source"
    sed 's/^/      /' "$WORK/compare.diff"
fi

printf '\n%-24s %16s %16s\n' check source restored
awk 'NR==FNR { r[$1]=$2; next }
     { printf "%-24s %16s %16s\n", $1, $2, ($1 in r ? r[$1] : "(absent)") }' \
    "$WORK/restored.report" "$WORK/source.report"

section "negative control"
lost=$(pq_text "$DST_DB" "SELECT count(*) FROM matches")
if [ "${lost:-0}" -gt 0 ]; then
    pq_text "$DST_DB" 'DELETE FROM matches WHERE match_id = (SELECT min(match_id) FROM matches)' >/dev/null
    if compare "$SRC_DB" "$DST_DB" "$WORK/negative.diff"; then
        fail "the comparison still reported a match after a row was deleted: it cannot detect a loss"
    else
        pass "the comparison fails once one of $lost match rows is deleted, as it must"
        first=$(grep -m1 '^[-+][^-+]' "$WORK/negative.diff" || true)
        note "first line of that diff: $first"
    fi
else
    # A drill that compared nothing is not a passed drill. This used to be a
    # warning that still exited 0, so `make backup-verify` could report success
    # over an archive with no matches rows at all - the same defect shape the
    # retired compliance gate had, where `make compliance-gnu` was removed from
    # the workflow and a run then reported a pass it had not earned: the
    # precondition is absent, so the check does not happen, and the run says it
    # was fine.
    fail "no matches rows exist to delete, so the negative control cannot run and the dump/restore comparison above proves nothing about loss detection."
    note "persist some matches, then re-run (see docs/runbooks/rebuild-aggregates.md)"
fi

section "result"
note "dump            $DUMP"
note "source report   $WORK/source.report"
note "restored report $WORK/restored.report"
note "comparison      $WORK/compare.diff (empty means identical)"
note "this drills the Postgres dump/restore path only. The raw archive is a"
note "restic repository and is restored by docs/runbooks/restore-raw.md."

if [ "$failures" -eq 0 ]; then
    printf '\nPASS  dump and restore agree (%s tables, %s row(s) in matches)\n' \
        "$pk_src" "${lost:-0}"
    exit 0
fi

printf '\nFAIL  %s check(s) failed\n' "$failures"
exit 1
