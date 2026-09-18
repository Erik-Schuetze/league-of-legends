#!/bin/sh
# scripts/archive-verify.sh - the raw-archive restore drill. It is the other
# half of the evidence behind the launch gate "Postgres and the raw archive have
# both been restored from backup in a test".
#
#   sh scripts/archive-verify.sh            # a throwaway restic repository in Docker
#
# scripts/backup-verify.sh is the Postgres half of that gate. This is the raw
# archive half: the restic round trip, run locally, against a repository that
# exists only for the length of the run. No cluster, no access to the real
# archive, no pre-existing repository required.
#
# What it does, in the order that matters:
#
#   1. builds a synthetic raw archive in the layout internal/raw documents -
#      riot/<api>/dt=<date>/[region=|kind=]part-NNNNN.parquet.zst - including
#      exactly the files the CronJob's excludes have to drop: a mid-write *.tmp
#      sibling, a *.partial, and a directory carrying CACHEDIR.TAG;
#   2. initialises a restic repository with the image the CronJob pins, and
#      proves a wrong password cannot read it;
#   3. runs `restic backup` with the CronJob's exact flags, then runs the
#      CronJob's own post-backup assertion (a snapshot for this host must exist);
#   4. runs the CronJob's Sunday branch eagerly - `forget --keep-* --prune` and
#      `check --read-data-subset=2%` - and then `check --read-data`, which reads
#      every pack file rather than a 2% sample;
#   5. restores into an empty directory and compares the two trees by sha256
#      manifest, then proves the comparison can fail: it changes one restored
#      byte, and then adds a file that was never snapshotted, and asserts that
#      the comparison notices each time. A comparison that cannot fail is not a
#      comparison, and this script would rather prove that than assert it;
#   6. grows the tree and runs the nightly path a second time, which proves an
#      incremental snapshot is complete and restorable.
#
# Why Docker and not the cluster: the drill needs to *write* to a restic
# repository and to a scratch tree. The only place those can be written is the
# data volume every workload shares, so a cluster drill would be a mutation of
# live production state. Local Docker gives the same restic binary, the same
# uid, the same flags and the same volume semantics, with nothing at stake.
#
# A note on where the files live. The 2026-09 vintage of Docker Desktop for
# macOS serves host bind mounts through its `fakeowner` FUSE driver, and restic
# inside the container cannot open a file on one: every read fails with
# `input/output error` while the same uid can `cat` the same file (the mount
# options include `fakeowner`, and restic's open path then fails where a plain
# read does not). A bind-mounted drill therefore silently snapshots *directories
# and no files*, which is precisely the kind of test that cannot fail. This
# script consequently keeps every path restic touches on a Docker *named
# volume* - which is also the closer analogue of the CronJob's PVC - and never
# mounts a host directory into the container at all. The fixture is built from
# the container's own definitions (the heredocs below) and the results travel
# back over stdout.
#
# /tmp inside the container is the container's own tmpfs, mounted read-only-root
# style exactly as the CronJob's emptyDir is, and it is where restic's cache
# goes (`RESTIC_CACHE_DIR`, `HOME`). No *host* temporary directory is used
# anywhere: what the tool needs to keep across steps it keeps in the volume, and
# what a reviewer needs afterwards it keeps in .agent-artifacts/archive-verify,
# whose path is printed at the end.
#
# The repository password is a literal in this file on purpose. It protects a
# repository of synthetic data that exists for the length of the run and is then
# deleted, so there is no secret here to keep - and reading the password out of
# the environment is what scripts/backup-verify.sh does for the *real* one.
#
# Flags:
#   --keep                 leave the container and the volume in place
#   --image <ref>          override the restic image (default: the digest
#                          deploy/base/jobs/backup-archive.yaml pins)
#   --cluster              refused: see "Why Docker and not the cluster" above
#
# Nothing is written outside .agent-artifacts/archive-verify.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/.agent-artifacts/archive-verify"
CTR=lolstats-archive-verify
VOLUME=lolstats-archive-verify-data
VERIFY_LABEL=lolstats.archive-verify=1

# The image deploy/base/jobs/backup-archive.yaml and
# docs/runbooks/restore-raw.md both pin. A drill against a different restic
# would be a drill against a different tool.
RESTIC_IMAGE=restic/restic:0.19.1@sha256:136600b6ff6843d61d355f7f71f460a166429f35de6fd11b568fece3c9a4d510

DRILL_PASSWORD=archive-verify-drill-not-a-secret
RESTIC_HOST=lolstats-archive
RAW_ROOT=/data/raw

# The fixture is checked against these floors before anything is compared, so
# that a fixture that quietly failed to materialise cannot make the equality
# checks below pass by comparing two empty trees.
MIN_FILES=12
MIN_BYTES=1000000
MIN_CHECKS=24

KEEP=0

while [ $# -gt 0 ]; do
    case "$1" in
        --keep) KEEP=1 ;;
        --image)
            shift
            [ $# -gt 0 ] || { printf 'archive-verify: --image needs a value\n' >&2; exit 2; }
            RESTIC_IMAGE=$1
            ;;
        --cluster)
            printf 'archive-verify: --cluster is refused by design. The raw-archive restore path\n' >&2
            printf 'archive-verify: needs a writable restic repository and a writable scratch tree,\n' >&2
            printf 'archive-verify: and on this cluster the only writable place is the data volume\n' >&2
            printf 'archive-verify: every live workload shares. The cluster half of this gate is a\n' >&2
            printf 'archive-verify: human restore, and docs/runbooks/restore-raw.md is its procedure.\n' >&2
            exit 2
            ;;
        -h|--help)
            sed -n '2,71p' "$0"
            exit 0
            ;;
        *)
            printf 'archive-verify: unknown argument %s\n' "$1" >&2
            exit 2
            ;;
    esac
    shift
done

failures=0
checks=0
pass() { printf 'PASS  %s\n' "$1"; checks=$((checks + 1)); }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); checks=$((checks + 1)); }
note() { printf '      %s\n' "$1"; }
section() { printf '\n== %s\n' "$1"; }
die() { printf 'archive-verify: FATAL: %s\n' "$1" >&2; exit 1; }

MINE=0
cleanup() {
    [ "$MINE" -eq 1 ] || return 0
    if [ "$KEEP" -eq 1 ]; then
        printf '\narchive-verify: --keep: leaving container %s and volume %s in place\n' "$CTR" "$VOLUME" >&2
        return 0
    fi
    docker rm -f "$CTR" >/dev/null 2>&1 || true
    docker volume rm -f "$VOLUME" >/dev/null 2>&1 || true
    return 0
}
trap cleanup EXIT INT TERM

mkdir -p "$WORK/reports" "$WORK/steps"
rm -f "$WORK"/reports/* "$WORK"/steps/* 2>/dev/null || true

# ---------------------------------------------------------------- harness ----
# `step` runs one container-side script and keeps its output. The script arrives
# on stdin (`/bin/sh -s`) so that it never has to survive an argument list, and
# every line it prints to stdout follows one convention:
#
#     <TAG> <payload>
#
# which is what keeps restic's own chatter out of the machine-readable stream:
# each step sends restic's output to stderr, where a human reads it, and prints
# only tagged results. Two consequences worth stating, because they are the
# difference between this and a test that cannot fail:
#
#   - every step must end with `DONE <name>`. A `sh -s` whose script was empty
#     or truncated exits 0 having checked nothing, and this is what notices;
#   - a step that fails is a failure of the run, not a warning: there is no SKIP
#     path anywhere in this script and nothing can be silently omitted.

# field <step-output> <tag> - the payload of every matching line, in order.
field() { grep "^$2 " "$1" | sed "s|^$2 ||"; }

step() { # <name> [extra `docker exec` args...]; the container script arrives on stdin
    _name=$1
    shift
    _out="$WORK/steps/$_name.out"
    : >"$_out"
    if ! docker exec -i "$@" "$CTR" /bin/sh -s >"$_out" 2>&1; then
        sed 's/^/      /' "$_out" | tail -20
        fail "the drill step '$_name' failed inside the container (see $WORK/steps/$_name.out)"
        printf '\nFAIL  %s check(s) failed\n' "$failures"
        exit 1
    fi
    if ! grep -qx "DONE $_name" "$_out"; then
        fail "step '$_name' exited 0 without printing 'DONE $_name': it did nothing, so it proves nothing"
        printf '\nFAIL  %s check(s) failed\n' "$failures"
        exit 1
    fi
    human=$(grep -v '^[A-Z][A-Za-z0-9-]* ' "$_out" | grep -v "^DONE $_name" || true)
    if [ -n "$human" ]; then
        printf '%s\n' "$human" | sed 's/^/      /'
    fi
}

# ------------------------------------------------------------------ setup ----
section "environment"
command -v docker >/dev/null 2>&1 || die "docker is not on PATH"
docker info >/dev/null 2>&1 || die "docker is not usable (is the daemon running?)"
if docker image inspect "$RESTIC_IMAGE" >/dev/null 2>&1; then
    note "image $RESTIC_IMAGE is already present"
else
    note "pulling $RESTIC_IMAGE"
    docker pull "$RESTIC_IMAGE" >/dev/null 2>&1 || die "cannot pull $RESTIC_IMAGE"
fi
note "docker client/server $(docker version --format '{{.Client.Version}}/{{.Server.Version}}' 2>/dev/null || printf 'unknown')"
note "work dir $WORK"

if docker ps -a --format '{{.Names}}' | grep -qx "$CTR"; then
    label=$(docker inspect --format '{{index .Config.Labels "lolstats.archive-verify"}}' "$CTR" 2>/dev/null || true)
    [ "$label" = 1 ] || die "a container named $CTR exists and this script did not create it; refusing to remove it"
    note "removing the container left by an earlier run"
    docker rm -f "$CTR" >/dev/null
fi

if docker volume inspect "$VOLUME" >/dev/null 2>&1; then
    label=$(docker volume inspect --format '{{index .Labels "lolstats.archive-verify"}}' "$VOLUME" 2>/dev/null || true)
    [ "$label" = 1 ] || die "a volume named $VOLUME exists and this script did not create it; refusing to remove it"
    note "removing the volume left by an earlier run"
    docker volume rm -f "$VOLUME" >/dev/null || die "cannot remove the previous $VOLUME (is another container using it?)"
fi
docker volume create --label "$VERIFY_LABEL" "$VOLUME" >/dev/null || die "cannot create the drill volume"
MINE=1

# The CronJob's Pod gets a writable archive because the volume it mounts applies
# `fsGroup: 65532`. A Docker volume is created root:root 0755 with no fsGroup,
# so the same property is established here explicitly, by the only process that
# can - a one-shot root container. Nothing else in this script runs as root, and
# the drill container below runs as 65532 like the job.
docker run --rm -u 0:0 -v "$VOLUME:/data" --entrypoint /bin/chmod "$RESTIC_IMAGE" 777 /data \
    || die "cannot make the drill volume writable for uid 65532"

# read-only root, a tmpfs /tmp and a single writable volume: the same shape the
# CronJob's container gets from readOnlyRootFilesystem plus its tmp emptyDir.
docker run -d --name "$CTR" --label "$VERIFY_LABEL" \
    --user 65532:65532 \
    --read-only \
    --tmpfs /tmp:rw,mode=1777,size=512m \
    -v "$VOLUME:/data" \
    -e HOME=/tmp \
    -e RESTIC_CACHE_DIR=/tmp/restic-cache \
    -e RESTIC_REPOSITORY=/data/repo \
    -e RESTIC_PASSWORD="$DRILL_PASSWORD" \
    -e LOLSTATS_RAW_ROOT="$RAW_ROOT" \
    -e RESTIC_HOST="$RESTIC_HOST" \
    -e RESTIC_KEEP_DAILY=7 \
    -e RESTIC_KEEP_WEEKLY=4 \
    -e RESTIC_KEEP_MONTHLY=6 \
    --entrypoint /bin/sh "$RESTIC_IMAGE" -c 'while :; do sleep 3600; done' >/dev/null \
    || die "cannot start the drill container from $RESTIC_IMAGE"
sleep 1
docker exec "$CTR" /bin/true >/dev/null 2>&1 || die "the drill container started but cannot exec"

# ----------------------------------------------------------------- fixture ----
section "fixture: a synthetic raw archive"
step fixture <<'FIXTURE'
set -eu
umask 022
raw="$LOLSTATS_RAW_ROOT"
[ -n "$raw" ] || { echo "fixture: LOLSTATS_RAW_ROOT is empty" >&2; exit 1; }
[ "$raw" = /data/raw ] || { echo "fixture: refusing to build a fixture at $raw" >&2; exit 1; }

rm -rf "$raw" /data/restore /data/restore-final /data/lib /data/repo
mkdir -p "$raw" /data/lib

# The mirror of the CronJob's exclusions, written as a *rule about the tree*
# rather than as a list of paths, so that renaming the fixture cannot quietly
# make the comparison easier. restic drops every file whose name ends in .tmp or
# .partial (the writer stages a part at a .tmp sibling and renames it once the
# footer is on disk) and the whole of any directory carrying a CACHEDIR.TAG,
# while keeping the tag file itself.
cat > /data/lib/manifest.sh <<'MANIFEST'
manifest() { # <root> -> "sha256  <path>" lines, relative to <root>, sorted by path
    _root="$1"
    find "$_root" -type d -exec sh -c 'for d; do if [ -f "$d/CACHEDIR.TAG" ]; then printf "%s\n" "$d"; fi; done' sh {} + > /data/lib/cachedirs
    ( cd "$_root" && find . -type f ! -name '*.tmp' ! -name '*.partial' ) | sed 's|^\./||' | sort > /data/lib/candidates
    : > /data/lib/included
    while IFS= read -r _p; do
        _skip=0
        while IFS= read -r _d; do
            _rel="${_d#"$_root"/}"
            case "$_p" in
                "$_rel"/*) if [ "$_p" != "$_rel/CACHEDIR.TAG" ]; then _skip=1; fi ;;
            esac
        done < /data/lib/cachedirs
        if [ "$_skip" -eq 0 ]; then printf '%s\n' "$_p" >> /data/lib/included; fi
    done < /data/lib/candidates
    ( cd "$_root" && while IFS= read -r _p; do sha256sum "$_p"; done < /data/lib/included ) | sort -k2
}
MANIFEST

files=0
bytes=0

# Kept in the volume as well as sourced here, because the incremental step below
# writes the next night's parts with exactly the same generator: the second run
# must differ from the first only in that the archive has grown.
cat > /data/lib/gen.sh <<'GEN'
add() { # <relpath> <bytes> <seed> [binary]
    _p="$LOLSTATS_RAW_ROOT/$1"
    mkdir -p "${_p%/*}"
    {
        awk -v seed="$3" -v want="$2" 'BEGIN {
            i = 0; n = 0
            while (n < want) {
                printf "%08d%08d%08d\n", (i * 2654435761 + seed) % 2147483647, (i * 40503 + seed * 7919) % 2147483647, (i * 6971 + seed * 104729) % 2147483647
                n += 25
                i++
            }
        }'
        # One part carries bytes that are not text: a payload is zstd-compressed
        # Parquet, and a checksum comparison that only ever sees ASCII would not
        # notice a comparison that mangled the encoding.
        if [ "${4:-}" = binary ]; then
            printf 'na\303\257ve \316\251 payload \000\001\002\377\376\012'
        fi
    } > "$_p"
    printf 'fixture: added %-58s %8s bytes\n' "$1" "$(wc -c < "$_p" | tr -d ' ')" >&2
}
GEN
. /data/lib/gen.sh

count_add() { # <relpath> <bytes> <seed> [binary]
    add "$1" "$2" "$3" "${4:-}"
    printf 'EXPECT %s\n' "$1"
    files=$((files + 1))
    bytes=$((bytes + $(wc -c < "$raw/$1" | tr -d ' ')))
}

# The layout internal/raw documents: riot/<api>/dt=<fetch date>[/region=|kind=]/
count_add 'riot/match-v5/dt=2026-09-17/part-00001.parquet.zst'                    220000 101
count_add 'riot/match-v5/dt=2026-09-17/part-00002.parquet.zst'                    180000 102
count_add 'riot/match-v5/dt=2026-09-17/part-00003.parquet.zst'                     96000 103 binary
count_add 'riot/match-v5/dt=2026-09-16/part-00001.parquet.zst'                    200000 201
count_add 'riot/match-v5/dt=2026-09-15/part-00001.parquet.zst'                    200000 301
count_add 'riot/league-v4/dt=2026-09-17/region=EUW/part-00001.parquet.zst'         64000 501
count_add 'riot/league-v4/dt=2026-09-17/region=NA1/part-00001.parquet.zst'         65000 502
count_add 'riot/league-v4/dt=2026-09-16/region=EUW/part-00001.parquet.zst'         64000 503
count_add 'riot/account-v1/dt=2026-09-17/part-00001.parquet.zst'                   64000 601
count_add 'riot/ddragon/dt=2026-09-17/kind=champions/part-00001.parquet.zst'       64000 701
count_add 'riot/ddragon/dt=2026-09-17/kind=items/part-00001.parquet.zst'           64000 702

tagrel='riot/match-v5/dt=2026-09-17/cache/CACHEDIR.TAG'
mkdir -p "$raw/riot/match-v5/dt=2026-09-17/cache/sub"
{
    printf 'Signature: 8a477f597d28d172789f06886806bc55\n'
    printf '# This file is a cache directory tag.\n'
    printf '# See https://bford.info/cachedir/\n'
} > "$raw/$tagrel"
printf 'EXPECT %s\n' "$tagrel"
files=$((files + 1))
bytes=$((bytes + $(wc -c < "$raw/$tagrel" | tr -d ' ')))

# The files the excludes must drop. A .tmp and a .partial are what the writer
# holds while it is still writing; the two binaries live under the directory
# that carries CACHEDIR.TAG, one of them a level deeper, because excluding a
# cache is a statement about a directory and its contents, not about one path.
mkdir -p "$raw/riot/match-v5/dt=2026-09-17/cache"
printf 'in flight, no footer yet\n' > "$raw/riot/match-v5/dt=2026-09-17/part-00004.parquet.zst.tmp"
printf 'in flight, no footer yet\n' > "$raw/riot/match-v5/dt=2026-09-16/part-00002.parquet.zst.partial"
printf 'frame bytes\n'             > "$raw/riot/match-v5/dt=2026-09-17/cache/frames-00001.bin"
printf 'nested frame bytes\n'      > "$raw/riot/match-v5/dt=2026-09-17/cache/sub/nested.bin"
printf 'DECOY %s\n' 'riot/match-v5/dt=2026-09-17/part-00004.parquet.zst.tmp'
printf 'DECOY %s\n' 'riot/match-v5/dt=2026-09-16/part-00002.parquet.zst.partial'
printf 'DECOY %s\n' 'riot/match-v5/dt=2026-09-17/cache/frames-00001.bin'
printf 'DECOY %s\n' 'riot/match-v5/dt=2026-09-17/cache/sub/nested.bin'
decoys=4

if [ "$files" -lt 12 ] || [ "$bytes" -lt 1000000 ]; then
    echo "fixture: only $files files and $bytes bytes were written; every comparison" >&2
    echo "fixture: below would be comparing trees that are too small to mean anything" >&2
    exit 1
fi
printf 'COUNT expected=%s decoys=%s bytes=%s\n' "$files" "$decoys" "$bytes"
echo "fixture: $files files to snapshot, $decoys deliberately excluded, $bytes bytes" >&2
find "$raw" -type f | sed 's|^|fixture: file |' >&2
echo "DONE fixture"
FIXTURE

# ------------------------------------------------------------- repository ----
section "repository: init, and a password that cannot open it"
step repository <<'REPOSITORY'
set -eu
umask 077

printf 'INFO restic %s\n' "$(restic version)"
printf 'INFO uid %s gid %s\n' "$(id -u)" "$(id -g)"
printf 'INFO repository %s\n' "$RESTIC_REPOSITORY"
printf 'INFO host %s\n' "$RESTIC_HOST"
printf 'INFO raw root %s\n' "$LOLSTATS_RAW_ROOT"
printf 'INFO password length %s\n' "${#RESTIC_PASSWORD}"
echo "repository: $(restic version)" >&2
echo "repository: running as uid $(id -u) gid $(id -g), as the job's securityContext does" >&2
echo "repository: repository $RESTIC_REPOSITORY, host tag $RESTIC_HOST" >&2
echo "repository: raw root $LOLSTATS_RAW_ROOT" >&2
echo "repository: the drill password is $(printf '%s' "$RESTIC_PASSWORD" | wc -c | tr -d ' ') characters, a literal in this script because the repository is synthetic" >&2

# The job refuses to run against an empty password, because an empty password
# means "no encryption" and restic would then be protecting nothing. So does
# this.
if [ -z "${RESTIC_PASSWORD:-}" ]; then
    echo "repository: RESTIC_PASSWORD is empty; the job refuses to run in this state and so does the drill" >&2
    exit 1
fi

if restic cat config >/dev/null 2>&1; then
    printf 'INIT already-present\n'
    echo "repository: the repository already exists (it should not: earlier runs remove their volume)" >&2
else
    restic init >&2
    printf 'INIT initialised\n'
fi

# A repository that opens with the right password is only half of what the
# credential has to do. The other half: it must *not* open with any other. Both
# halves are stated here, because "restic init succeeded" alone would be
# satisfied by a repository no password protects.
set +e
restic cat config >/data/lib/open-ok.out 2>/data/lib/open-ok.err
ok_code=$?
set -e
printf 'PASSWORD-OK exit=%s\n' "$ok_code"
[ "$ok_code" -eq 0 ] || { echo "repository: the repository does not open with the configured password" >&2; exit 1; }

set +e
RESTIC_PASSWORD=definitely-not-the-drill-password restic cat config \
    >/data/lib/wrong-password.out 2>/data/lib/wrong-password.err
bad_code=$?
set -e
printf 'PASSWORD-BAD exit=%s\n' "$bad_code"
# 12 is restic's own "wrong password" exit code. Anything else - including a
# clean 0 - means this repository is not actually keyed to this password.
if [ "$bad_code" -ne 12 ]; then
    echo "repository: opening the repository with a wrong password exited $bad_code, not 12" >&2
    sed 's/^/repository: /' /data/lib/wrong-password.err >&2
    exit 1
fi
echo "repository: init ok, right password opens, wrong password is refused with exit 12" >&2
echo "DONE repository"
REPOSITORY

# ----------------------------------------------------------------- backup ----
section "backup: the CronJob's own flags, then the CronJob's own assertion"
step backup <<'BACKUP'
set -eu
umask 077

set +e
restic backup --host "$RESTIC_HOST" --tag raw-archive --one-file-system \
    --exclude '*.tmp' --exclude '*.partial' --exclude-caches \
    "$LOLSTATS_RAW_ROOT" >&2
code=$?
set -e
printf 'BACKUP exit=%s\n' "$code"
# 0 is clean, 3 is "could not read some source files" - the job treats both as
# success because an unreadable file is not an archive failure. Anything else is
# a failure, and the drill fails with it.
if [ "$code" -ne 0 ] && [ "$code" -ne 3 ]; then
    echo "backup: restic backup exited $code; the job accepts only 0 and 3" >&2
    exit 1
fi

# The job's post-backup assertion, in the same shape: a snapshot for this host
# must exist, and neither the empty string nor '[]' counts as one.
latest=$(restic snapshots --host "$RESTIC_HOST" --latest 1 --json)
case "$latest" in
    ''|'[]'|'[ ]')
        echo "backup: 'restic snapshots --host $RESTIC_HOST --latest 1 --json' returned '$latest'" >&2
        echo "backup: there is no snapshot for this host, so the archive was not backed up" >&2
        exit 1
        ;;
esac
printf 'SNAPSHOT-JSON-BYTES %s\n' "$(printf '%s' "$latest" | wc -c | tr -d ' ')"
sid=$(printf '%s\n' "$latest" | sed -n 's/.*"short_id": *"\([^"]*\)".*/\1/p')
printf 'SNAPSHOT-ID %s\n' "$sid"
[ -n "$sid" ] || { echo "backup: the snapshot JSON carries no short_id" >&2; exit 1; }

set +e
restic snapshots --host "$RESTIC_HOST" --json > /data/lib/snapshots.json 2>&1
snap_code=$?
set -e
[ "$snap_code" -eq 0 ] || { echo "backup: restic snapshots exited $snap_code" >&2; exit 1; }
printf 'SNAPSHOT-COUNT %s\n' "$(grep -o '"short_id"' /data/lib/snapshots.json | wc -l | tr -d ' ')"

# What the snapshot actually holds, listed from the repository rather than from
# the filesystem: this is the archive as restic recorded it. `-l` marks each
# entry's mode, so that files can be told from directories when the listing is
# checked below.
set +e
restic ls -l "$sid" --host "$RESTIC_HOST" > /data/lib/snapshot-ls.txt 2>&1
ls_code=$?
set -e
[ "$ls_code" -eq 0 ] || { echo "backup: restic ls $sid exited $ls_code" >&2; exit 1; }
printf 'LS-ENTRIES %s\n' "$(wc -l < /data/lib/snapshot-ls.txt | tr -d ' ')"
printf 'LS-FILES %s\n' "$(grep -c '^-' /data/lib/snapshot-ls.txt | tr -d ' ')"
# The listing of what the archive holds, files only: this is the snapshot as
# restic recorded it, which is what the exclusions below are asserted against.
grep '^-' /data/lib/snapshot-ls.txt | sed 's/^/      snapshot: /' >&2
echo "DONE backup"
BACKUP

# -------------------------------------------------------------- integrity ----
section "integrity: the CronJob's Sunday branch, plus a full read of every pack"
step integrity <<'INTEGRITY'
set -eu
umask 077

# The job prunes on Sundays. Pruning is what makes an archive survivable over
# years, and it is also the operation most likely to leave a repository that
# opens but cannot be read, so the drill runs it eagerly rather than waiting for
# a Sunday.
set +e
restic forget --keep-daily "$RESTIC_KEEP_DAILY" --keep-weekly "$RESTIC_KEEP_WEEKLY" \
    --keep-monthly "$RESTIC_KEEP_MONTHLY" --prune >&2
forget_code=$?
set -e
printf 'FORGET exit=%s\n' "$forget_code"
[ "$forget_code" -eq 0 ] || { echo "integrity: restic forget --prune exited $forget_code" >&2; exit 1; }

set +e
restic check --read-data-subset=2% >&2
sample_code=$?
set -e
printf 'CHECK-SUBSET exit=%s\n' "$sample_code"
[ "$sample_code" -eq 0 ] || { echo "integrity: 'restic check --read-data-subset=2%' exited $sample_code" >&2; exit 1; }

# The job samples 2% because that is what fits in a routine Sunday. A drill is
# not routine: it reads every pack file in the repository, which is the only way
# to know the bytes are readable rather than merely present.
set +e
restic check --read-data >&2
full_code=$?
set -e
printf 'CHECK-FULL exit=%s\n' "$full_code"
[ "$full_code" -eq 0 ] || { echo "integrity: 'restic check --read-data' exited $full_code" >&2; exit 1; }

set +e
restic snapshots --host "$RESTIC_HOST" --json > /data/lib/snapshots-after.json 2>&1
after_code=$?
set -e
[ "$after_code" -eq 0 ] || { echo "integrity: restic snapshots exited $after_code after forget" >&2; exit 1; }
printf 'SNAPSHOTS-AFTER %s\n' "$(grep -o '"short_id"' /data/lib/snapshots-after.json | wc -l | tr -d ' ')"
echo "integrity: forget --prune and a full --read-data check both succeeded" >&2
echo "DONE integrity"
INTEGRITY

# ---------------------------------------------------------------- restore ----
section "restore: into an empty directory, then compare by content"
step restore <<'RESTORE'
set -eu
umask 077

rm -rf /data/restore
mkdir -p /data/restore

set +e
restic restore latest --host "$RESTIC_HOST" --target /data/restore >&2
code=$?
set -e
printf 'RESTORE exit=%s\n' "$code"
[ "$code" -eq 0 ] || { echo "restore: restic restore exited $code" >&2; exit 1; }

# Where the archive landed. A restore of absolute paths lands under the target
# root with the leading slash stripped, so the archive root is
# $RESTORE/data/raw - and if that is not where it is, the comparison below would
# be comparing two directories that have nothing to do with each other.
printf 'RESTORE-ROOT %s\n' /data/restore/data/raw
[ -d /data/restore/data/raw ] || { echo "restore: /data/restore/data/raw does not exist after the restore" >&2; find /data/restore -maxdepth 6 | sed 's/^/restore: /' >&2; exit 1; }
find /data/restore/data/raw -type d | sort | sed 's/^/      restored dir /' >&2

. /data/lib/manifest.sh
manifest /data/restore/data/raw > /data/lib/manifest-restored.txt
awk '{ print "HASH-RESTORED " $0 }' /data/lib/manifest-restored.txt
printf 'RESTORED-FILES %s\n' "$(wc -l < /data/lib/manifest-restored.txt | tr -d ' ')"
echo "DONE restore"
RESTORE

section "compare: the live archive tree against the restored archive tree"

# A sha256 of every file restic is supposed to have archived, relative to a
# root, in the order restic excludes them. Both sides of the comparison below
# are produced by this one function, which is deliberate: two different
# enumerations could disagree for reasons that have nothing to do with the
# restore.
tree_manifest() { # <step-name> <root> <output-file>
    step "$1" -e MV_ROOT="$2" <<SMAN
set -eu
. /data/lib/manifest.sh
manifest "\$MV_ROOT" > /data/lib/manifest-live.txt
awk '{ print "HASH " \$0 }' /data/lib/manifest-live.txt
printf 'MANIFEST-ROOT %s\n' "\$MV_ROOT"
printf 'MANIFEST-FILES %s\n' "\$(wc -l < /data/lib/manifest-live.txt | tr -d ' ')"
echo "DONE $1"
SMAN
    field "$WORK/steps/$1.out" HASH | LC_ALL=C sort >"$3"
}

tree_manifest manifest-live "$RAW_ROOT" "$WORK/reports/live.manifest"
field "$WORK/steps/restore.out" HASH-RESTORED | LC_ALL=C sort >"$WORK/reports/restored.manifest"
docker cp "$CTR:/data/lib/snapshot-ls.txt" "$WORK/reports/snapshot-ls.txt" >/dev/null 2>&1 || true

live_files=$(wc -l <"$WORK/reports/live.manifest" | tr -d ' ')
restored_files=$(wc -l <"$WORK/reports/restored.manifest" | tr -d ' ')
note "live tree: $live_files files; restored tree: $restored_files files"

# The fixture's own floors. Without these, a live tree that was never written
# and a restore that produced nothing would compare equal - which is exactly the
# failure mode this whole script exists to exclude.
if [ "$live_files" -lt "$MIN_FILES" ]; then
    fail "the live archive tree holds only $live_files files, below the floor of $MIN_FILES"
else
    pass "the live archive tree holds $live_files files (floor $MIN_FILES)"
fi
if [ "$restored_files" -lt "$MIN_FILES" ]; then
    fail "the restored tree holds only $restored_files files, below the floor of $MIN_FILES"
else
    pass "the restored tree holds $restored_files files (floor $MIN_FILES)"
fi

# Every path that restic recorded has to be present and byte-identical in the
# restore, and nothing may be present in the restore that was not recorded. Two
# directions, because a comparison that only asks "is every source file in the
# restore" cannot see a restore that also invented files.
if cmp -s "$WORK/reports/live.manifest" "$WORK/reports/restored.manifest"; then
    pass "all $live_files recorded files match the restore byte for byte, and the restore holds nothing extra"
else
    fail "the live tree and the restored tree differ"
    diff -u "$WORK/reports/live.manifest" "$WORK/reports/restored.manifest" | head -40 | sed 's/^/      /'
fi

# ------------------------------------------------------------- exclusions ----
section "exclusions: what the snapshot must hold, and what it must not"
snapshot_ls="$WORK/reports/snapshot-ls.txt"
if [ ! -s "$snapshot_ls" ]; then
    fail "the snapshot listing is missing or empty ($snapshot_ls)"
else
    expected=$(field "$WORK/steps/fixture.out" EXPECT | wc -l | tr -d ' ')
    for p in $(field "$WORK/steps/fixture.out" EXPECT); do
        if grep -qF "/data/raw/$p" "$snapshot_ls"; then
            pass "the snapshot holds $p"
        else
            fail "the snapshot does not hold $p"
        fi
    done

    # The other direction, and the reason the excludes are asserted at all: the
    # CronJob drops *.tmp, *.partial and the contents of any directory carrying
    # CACHEDIR.TAG, and a drill that only checked what *is* in the snapshot would
    # not notice if those excludes stopped working and the repository began
    # filling up with half-written parts and cache frames.
    for p in $(field "$WORK/steps/fixture.out" DECOY); do
        if grep -qF "/data/raw/$p" "$snapshot_ls"; then
            fail "the snapshot holds $p, which the CronJob's excludes must drop"
        else
            pass "the snapshot excludes $p"
        fi
    done

    # And the count, so that a listing which happens to contain the expected
    # names cannot also be carrying something nobody looked at.
    snapshot_files=$(grep -c '^-' "$snapshot_ls" | tr -d ' ')
    if [ "$snapshot_files" -eq "$expected" ]; then
        pass "the snapshot holds exactly $expected files, no more and no fewer"
    else
        fail "the snapshot holds $snapshot_files files but the fixture wrote $expected non-excluded files"
        grep '^-' "$snapshot_ls" | sed 's/^/      /'
    fi
fi

# --------------------------------------------------------- negative control ----
section "negative control: the comparison above has to be able to fail"

# Everything up to here has compared two trees and found them equal. On its own
# that is worth very little: a comparison broken in the obvious way - reading an
# empty manifest, comparing a tree against itself, hashing nothing - also finds
# two trees equal. This project has been bitten by guards that cannot fail, so
# the three ways a restore can disagree with its source are introduced on
# purpose, the *same* comparison is re-run, and each one has to be caught. The
# state is put back after each, and the last check confirms the reversion, so
# that the run ends where it started.

recompare() { # <restore-root>
    step manifest-recompare -e MV_ROOT="$1" <<'RECOMPARE'
set -eu
. /data/lib/manifest.sh
manifest "$MV_ROOT" | awk '{ print "HASH " $0 }'
printf 'MANIFEST-ROOT %s\n' "$MV_ROOT"
echo "DONE manifest-recompare"
RECOMPARE
    field "$WORK/steps/manifest-recompare.out" HASH | LC_ALL=C sort >"$WORK/reports/restored-now.manifest"
}

same_as_live() { cmp -s "$WORK/reports/live.manifest" "$WORK/reports/restored-now.manifest"; }

victim=riot/match-v5/dt=2026-09-17/part-00001.parquet.zst
canary=riot/ddragon/dt=2026-09-17/kind=items/part-00001.parquet.zst
restored_root=/data/restore/data/raw

# 1. A file whose bytes changed but whose name did not. This is the failure a
#    restore can produce without any error at all: a truncated or recompressed
#    part, or a bad drive.
docker exec "$CTR" /bin/sh -c "printf 'tampered-bytes' >> $restored_root/$victim" \
    || die "cannot perturb the restored tree for the first negative control"
recompare "$restored_root"
if same_as_live; then
    fail "negative control 1: 14 bytes were appended to a restored file and the comparison still called the trees equal"
else
    pass "negative control 1 (a changed byte in a restored file): the comparison failed, as it must"
fi

docker exec "$CTR" /bin/sh -c "cp /data/raw/$victim $restored_root/$victim" \
    || die "cannot revert the first negative control"
recompare "$restored_root"
if same_as_live; then
    pass "after reverting that file the trees match again, so the comparison is tracking the data"
else
    fail "the trees still differ after the changed byte was reverted"
fi

# 2. A file the snapshot never held, sitting in the restore. Only one direction
#    of comparison - "every recorded file is present and equal" - can miss this,
#    which is why the equality is a strict comparison of both listings.
docker exec "$CTR" /bin/sh -c "printf 'ghost\n' > $restored_root/riot/match-v5/dt=2026-09-17/part-99999.parquet.zst" \
    || die "cannot perturb the restored tree for the second negative control"
recompare "$restored_root"
if same_as_live; then
    fail "negative control 2: a file that was never snapshotted was added to the restore and the comparison did not notice"
else
    pass "negative control 2 (an extra file in the restore): the comparison failed, as it must"
fi
docker exec "$CTR" /bin/sh -c "rm -f $restored_root/riot/match-v5/dt=2026-09-17/part-99999.parquet.zst" \
    || die "cannot revert the second negative control"

# 3. A recorded file missing from the restore. This is the failure a restore
#    reports as success when it skips a path, and it is the direction the extra
#    file above cannot cover.
docker exec "$CTR" /bin/sh -c "rm -f $restored_root/$canary" \
    || die "cannot perturb the restored tree for the third negative control"
recompare "$restored_root"
if same_as_live; then
    fail "negative control 3: a recorded file was deleted from the restore and the comparison did not notice"
else
    pass "negative control 3 (a recorded file missing from the restore): the comparison failed, as it must"
fi
docker exec "$CTR" /bin/sh -c "cp /data/raw/$canary $restored_root/$canary" \
    || die "cannot revert the third negative control"

recompare "$restored_root"
if same_as_live; then
    pass "all three perturbations reverted: the restore and the archive match again"
else
    fail "the restore does not match the archive after the negative controls were reverted"
fi

# ------------------------------------------------------------ incremental ----
section "incremental: the next night's run, over an archive that has grown"
step grow <<'GROW'
set -eu
umask 022
. /data/lib/gen.sh
add 'riot/match-v5/dt=2026-09-18/part-00001.parquet.zst'            200000 801
add 'riot/match-v5/dt=2026-09-18/part-00002.parquet.zst'            120000 802
add 'riot/league-v4/dt=2026-09-17/region=KR/part-00001.parquet.zst'  60000 803
printf 'GROWN 3\n'
echo "grow: three more parts landed, as a night's crawl leaves them" >&2
echo "DONE grow"
GROW
grown=$(field "$WORK/steps/grow.out" GROWN)

step backup-second <<'BACKUP2'
set -eu
umask 077

set +e
restic backup --host "$RESTIC_HOST" --tag raw-archive --one-file-system \
    --exclude '*.tmp' --exclude '*.partial' --exclude-caches \
    "$LOLSTATS_RAW_ROOT" >&2
code=$?
set -e
printf 'BACKUP2 exit=%s\n' "$code"
if [ "$code" -ne 0 ] && [ "$code" -ne 3 ]; then
    echo "backup-second: restic backup exited $code" >&2
    exit 1
fi

set +e
restic snapshots --host "$RESTIC_HOST" --json > /data/lib/snapshots-2.json 2>&1
snap_code=$?
set -e
[ "$snap_code" -eq 0 ] || { echo "backup-second: restic snapshots exited $snap_code" >&2; exit 1; }
printf 'SNAPSHOTS-AFTER2 %s\n' "$(grep -o '"short_id"' /data/lib/snapshots-2.json | wc -l | tr -d ' ')"

latest=$(restic snapshots --host "$RESTIC_HOST" --latest 1 --json)
case "$latest" in ''|'[]'|'[ ]') echo "backup-second: no snapshot for $RESTIC_HOST after the second run" >&2; exit 1 ;; esac
sid2=$(printf '%s\n' "$latest" | sed -n 's/.*"short_id": *"\([^"]*\)".*/\1/p')
printf 'SNAPSHOT2-ID %s\n' "$sid2"
restic ls -l "$sid2" --host "$RESTIC_HOST" > /data/lib/snapshot2-ls.txt
printf 'SNAPSHOT2-FILES %s\n' "$(grep -c '^-' /data/lib/snapshot2-ls.txt | tr -d ' ')"
echo "DONE backup-second"
BACKUP2

step restore-second <<'RESTORE2'
set -eu
rm -rf /data/restore-final
mkdir -p /data/restore-final
set +e
restic restore latest --host "$RESTIC_HOST" --target /data/restore-final >&2
code=$?
set -e
printf 'RESTORE2 exit=%s\n' "$code"
[ "$code" -eq 0 ] || { echo "restore-second: restic restore exited $code" >&2; exit 1; }
. /data/lib/manifest.sh
manifest /data/restore-final/data/raw > /data/lib/manifest-restored-final.txt
awk '{ print "HASH " $0 }' /data/lib/manifest-restored-final.txt
printf 'FINAL-FILES %s\n' "$(wc -l < /data/lib/manifest-restored-final.txt | tr -d ' ')"
echo "DONE restore-second"
RESTORE2

snapshots_before=$(field "$WORK/steps/backup.out" SNAPSHOT-COUNT)
snapshots_after=$(field "$WORK/steps/backup-second.out" SNAPSHOTS-AFTER2)
snapshot2_files=$(field "$WORK/steps/backup-second.out" SNAPSHOT2-FILES)
field "$WORK/steps/restore-second.out" HASH | LC_ALL=C sort >"$WORK/reports/restored-final.manifest"

# The archive grows between nights, so the second run is the interesting one:
# it is the run whose snapshot has the first night's data to fall back on, and
# an incremental snapshot that is complete only by luck of sharing packs with
# its predecessor is a known way to lose data quietly.
if [ "${snapshots_after:-0}" -gt "${snapshots_before:-0}" ]; then
    pass "the second run added a snapshot ($snapshots_before -> $snapshots_after)"
else
    fail "the second run did not add a snapshot ($snapshots_before -> $snapshots_after)"
fi

tree_manifest manifest-live "$RAW_ROOT" "$WORK/reports/live-grown.manifest"
grown_live=$(wc -l <"$WORK/reports/live-grown.manifest" | tr -d ' ')
final_files=$(wc -l <"$WORK/reports/restored-final.manifest" | tr -d ' ')

if [ "$snapshot2_files" = "$grown_live" ]; then
    pass "the newest snapshot holds all $snapshot2_files files of the grown archive"
else
    fail "the newest snapshot holds $snapshot2_files files but the grown archive holds $grown_live"
fi
if [ "$final_files" = "$grown_live" ]; then
    pass "the restore of the newest snapshot holds all $grown_live files"
else
    fail "the restore of the newest snapshot holds $final_files files but the archive holds $grown_live"
fi
if cmp -s "$WORK/reports/live-grown.manifest" "$WORK/reports/restored-final.manifest"; then
    pass "the grown archive and the restore of its newest snapshot match byte for byte"
else
    fail "the grown archive and the restore of its newest snapshot differ"
    diff -u "$WORK/reports/live-grown.manifest" "$WORK/reports/restored-final.manifest" | head -40 | sed 's/^/      /'
fi
note "the second run grew the archive by $grown file(s)"

# ------------------------------------------------------------------ result ----
section "result"

# A floor on the number of checks, so that a run which quietly stopped early -
# a step whose `DONE` line went missing, a loop over an empty listing - cannot
# report success. Indented lines and restic's own chatter are not checks.
if [ "$checks" -lt "$MIN_CHECKS" ]; then
    fail "$checks checks ran, below the floor of $MIN_CHECKS: this run did not do the work it claims"
else
    pass "$checks checks ran (floor $MIN_CHECKS)"
fi

printf '\n%d check(s) run, %d failed\n' "$checks" "$failures"
note "step output and manifests are in $WORK"

if [ "$failures" -ne 0 ]; then
    printf 'FAIL  %d check(s) failed\n' "$failures"
    exit 1
fi

printf 'PASS  raw archive: backup -> prune -> check --read-data -> restore, byte for byte\n'
printf 'PASS  the restore comparison was shown to fail on a changed byte, an extra file and a missing file\n'
printf '\n'
printf 'What this proves, and what it does not:\n'
printf '  This is the repository half of the section 14 restore gate: the archive as restic\n'
printf '  writes it, prunes it and hands it back, with the CronJob'\''s own flags and the\n'
printf '  restic image deploy/base/jobs/backup-archive.yaml pins. It proves the archive\n'
printf '  format survives a round trip through the tool that will perform the real one.\n'
printf '  It does not prove that the archive on the cluster restores: that needs the data\n'
printf '  volume, and it is a human run of docs/runbooks/restore-raw.md. Nor does it measure\n'
printf '  the archive'\''s size or growth - the corpus here is synthetic, which is what\n'
printf '  fixtures/README.md requires and what makes the drill runnable anywhere.\n'
printf '\n'
printf 'Scripts this one does not replace:\n'
printf '  scripts/backup-verify.sh      the Postgres dump/restore half of the same gate\n'
printf '  docs/runbooks/restore-raw.md  the human restore of the live archive\n'
exit 0